package extension

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// JSONRPCLedger is a read-only XRPL client.
//
// The enclave reads two things and writes nothing: the treasury account's next
// sequence number, and the current validated ledger. It never submits — that is
// the relayer's job, and keeping submission outside the enclave means a
// compromised RPC endpoint can stall a payment but cannot alter one, since the
// destination and amount are fixed by the authorization and covered by the
// signature.
type JSONRPCLedger struct {
	endpoint string
	client   *http.Client
}

// NewJSONRPCLedger builds a ledger reader against an XRPL JSON-RPC endpoint.
func NewJSONRPCLedger(endpoint string) *JSONRPCLedger {
	return &JSONRPCLedger{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

func (l *JSONRPCLedger) call(method string, params any, out any) error {
	body, err := json.Marshal(map[string]any{
		"method": method,
		"params": []any{params},
	})
	if err != nil {
		return fmt.Errorf("marshalling %s: %w", method, err)
	}

	resp, err := l.client.Post(l.endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: endpoint returned %d: %s", method, resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s response: %w", method, err)
	}
	return nil
}

// AccountSequence returns the next sequence number for an account.
func (l *JSONRPCLedger) AccountSequence(address string) (uint32, error) {
	var out struct {
		Result struct {
			AccountData struct {
				Sequence uint32 `json:"Sequence"`
			} `json:"account_data"`
			Error        string `json:"error"`
			ErrorMessage string `json:"error_message"`
		} `json:"result"`
	}
	params := map[string]any{"account": address, "ledger_index": "validated"}
	if err := l.call("account_info", params, &out); err != nil {
		return 0, err
	}
	if out.Result.Error != "" {
		// actNotFound is the common case: the treasury exists on Flare but nobody
		// has funded its XRPL account yet. Say so plainly — it is the single most
		// likely first-run stumble.
		if out.Result.Error == "actNotFound" {
			return 0, fmt.Errorf("XRPL account %s does not exist yet — fund it from the testnet faucet before requesting a payment", address)
		}
		return 0, fmt.Errorf("account_info: %s (%s)", out.Result.Error, out.Result.ErrorMessage)
	}
	if out.Result.AccountData.Sequence == 0 {
		return 0, fmt.Errorf("account_info returned no sequence for %s", address)
	}
	return out.Result.AccountData.Sequence, nil
}

// CurrentLedger returns the latest validated ledger index.
func (l *JSONRPCLedger) CurrentLedger() (uint32, error) {
	var out struct {
		Result struct {
			Info struct {
				ValidatedLedger struct {
					Seq uint32 `json:"seq"`
				} `json:"validated_ledger"`
			} `json:"info"`
		} `json:"result"`
	}
	if err := l.call("server_info", map[string]any{}, &out); err != nil {
		return 0, err
	}
	seq := out.Result.Info.ValidatedLedger.Seq
	if seq == 0 {
		return 0, fmt.Errorf("server_info returned no validated ledger")
	}
	return seq, nil
}

// nowUnix is a variable so tests can pin the clock.
var nowUnix = func() int64 { return time.Now().Unix() }

func hashFromHex(s string) ([32]byte, error) {
	var out [32]byte
	b, err := hex.DecodeString(strings.TrimPrefix(s, "0x"))
	if err != nil {
		return out, err
	}
	if len(b) != 32 {
		return out, fmt.Errorf("expected 32 bytes, got %d", len(b))
	}
	copy(out[:], b)
	return out, nil
}

func bytesFromHex(s string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}
