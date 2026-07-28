// Package xrplsubmit submits already-signed transactions to the XRP Ledger.
//
// This package holds no key and cannot construct a transaction. It takes an
// opaque signed blob and puts it on the wire. That is the whole of the
// broadcaster's power: it can delay or replay a payment the enclave already
// authorized, and it can do nothing else — it cannot change the destination, the
// amount, or the memo, because all three are covered by a signature it cannot
// produce.
package xrplsubmit

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal XRPL JSON-RPC client.
type Client struct {
	endpoint string
	http     *http.Client
}

// New builds a client against an XRPL JSON-RPC endpoint.
func New(endpoint string) *Client {
	return &Client{endpoint: endpoint, http: &http.Client{Timeout: 30 * time.Second}}
}

// Result describes the outcome of a submission.
type Result struct {
	// EngineResult is XRPL's provisional code, e.g. tesSUCCESS or terQUEUED.
	EngineResult string
	// EngineResultMessage is its human-readable form.
	EngineResultMessage string
	// TxID is the transaction's hash.
	TxID string
	// Accepted reports whether the ledger took the transaction at all.
	Accepted bool
	// AlreadySubmitted is true when this exact transaction was already known.
	AlreadySubmitted bool
}

func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	body, err := json.Marshal(map[string]any{"method": method, "params": []any{params}})
	if err != nil {
		return fmt.Errorf("marshalling %s: %w", method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building %s request: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%s: endpoint returned %d: %s", method, resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Submit puts a signed transaction blob on the ledger.
//
// Submission is idempotent by nature: an XRPL transaction is identified by the
// hash of its signed bytes, and the ledger applies a given sequence number once.
// Resubmitting after a relayer restart therefore cannot produce a second
// payment — it is answered with tefALREADY or tefPAST_SEQ, which this function
// reports as AlreadySubmitted rather than as an error.
func (c *Client) Submit(ctx context.Context, txBlobHex string) (*Result, error) {
	blob := strings.ToUpper(strings.TrimPrefix(txBlobHex, "0x"))
	if _, err := hex.DecodeString(blob); err != nil {
		return nil, fmt.Errorf("transaction blob is not hex: %w", err)
	}

	var out struct {
		Result struct {
			EngineResult        string `json:"engine_result"`
			EngineResultMessage string `json:"engine_result_message"`
			Accepted            bool   `json:"accepted"`
			TxJSON              struct {
				Hash string `json:"hash"`
			} `json:"tx_json"`
			Error        string `json:"error"`
			ErrorMessage string `json:"error_message"`
		} `json:"result"`
	}
	if err := c.call(ctx, "submit", map[string]any{"tx_blob": blob}, &out); err != nil {
		return nil, err
	}
	r := out.Result
	if r.Error != "" {
		return nil, fmt.Errorf("submit rejected: %s (%s)", r.Error, r.ErrorMessage)
	}

	res := &Result{
		EngineResult:        r.EngineResult,
		EngineResultMessage: r.EngineResultMessage,
		TxID:                strings.ToUpper(r.TxJSON.Hash),
	}
	switch {
	case r.EngineResult == "tesSUCCESS" || strings.HasPrefix(r.EngineResult, "ter"):
		// tes = applied; ter = retryable, will be reconsidered in a later ledger.
		res.Accepted = true
	case r.EngineResult == "tefALREADY" || r.EngineResult == "tefPAST_SEQ":
		res.Accepted = true
		res.AlreadySubmitted = true
	}
	return res, nil
}

// Validated reports whether a transaction has been validated, and whether it
// succeeded once it was.
func (c *Client) Validated(ctx context.Context, txID string) (validated bool, succeeded bool, err error) {
	var out struct {
		Result struct {
			Validated bool `json:"validated"`
			Meta      struct {
				TransactionResult string `json:"TransactionResult"`
			} `json:"meta"`
			Error string `json:"error"`
		} `json:"result"`
	}
	if e := c.call(ctx, "tx", map[string]any{"transaction": strings.ToUpper(txID)}, &out); e != nil {
		return false, false, e
	}
	if out.Result.Error != "" {
		// txnNotFound simply means it has not been seen yet.
		return false, false, nil
	}
	return out.Result.Validated, out.Result.Meta.TransactionResult == "tesSUCCESS", nil
}

// WaitForValidation blocks until a transaction is validated or the context ends.
func (c *Client) WaitForValidation(ctx context.Context, txID string, poll time.Duration) (bool, error) {
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		validated, succeeded, err := c.Validated(ctx, txID)
		if err != nil {
			return false, err
		}
		if validated {
			return succeeded, nil
		}
		select {
		case <-ctx.Done():
			return false, fmt.Errorf("waiting for %s to validate: %w", txID, ctx.Err())
		case <-ticker.C:
		}
	}
}
