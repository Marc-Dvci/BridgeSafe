//go:build live

// Live end-to-end validation of the codec against XRPL Testnet.
//
//	go test -tags live ./internal/xrpl/ -run TestLive -v
//
// Excluded from the default build because it needs the network and the public
// testnet faucet. It is the test that actually matters for the codec: unit
// vectors prove the bytes match what we think they should be, but only the
// ledger can confirm the serialization, the signature encoding and the
// transaction id are all simultaneously correct.
package xrpl

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

const (
	testnetRPC    = "https://s.altnet.rippletest.net:51234/"
	testnetFaucet = "https://faucet.altnet.rippletest.net/accounts"
)

func rpc(t *testing.T, method string, params any) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"method": method,
		"params": []any{params},
	})
	if err != nil {
		t.Fatalf("marshal %s: %v", method, err)
	}
	resp, err := http.Post(testnetRPC, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	defer resp.Body.Close()

	var out struct {
		Result map[string]any `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", method, err)
	}
	return out.Result
}

// TestLive_PaymentIsAcceptedByTheLedger walks the exact path the enclave takes:
// generate a key, derive its address, fund it, build a Payment carrying a
// BridgeSafe memo, sign, submit, and confirm the ledger validated it with the
// amount, destination and memo intact.
func TestLive_PaymentIsAcceptedByTheLedger(t *testing.T) {
	// 1. A key generated exactly as the enclave generates a treasury key.
	key, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pub := key.PubKey().SerializeCompressed()

	accountID, err := AccountIDFromPublicKey(pub)
	if err != nil {
		t.Fatalf("derive account id: %v", err)
	}
	address, err := EncodeClassicAddress(accountID)
	if err != nil {
		t.Fatalf("encode address: %v", err)
	}
	t.Logf("treasury address: %s", address)

	// The address must survive a round trip, or FDC's address hash would not
	// match what we register on Flare.
	if back, err := DecodeClassicAddress(address); err != nil || !bytes.Equal(back, accountID) {
		t.Fatalf("address round trip failed: %v", err)
	}

	// 2. Fund it from the testnet faucet.
	fundAddress(t, address)

	// 3. Read the account's sequence and the current ledger.
	info := rpc(t, "account_info", map[string]any{"account": address, "ledger_index": "validated"})
	acct, ok := info["account_data"].(map[string]any)
	if !ok {
		t.Fatalf("faucet did not fund %s: %v", address, info)
	}
	sequence := uint32(acct["Sequence"].(float64))

	state := rpc(t, "server_info", map[string]any{})
	ledgerSeq := currentLedger(t, state)

	// 4. Build the payment the enclave would build.
	destination := "rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe" // a well-known testnet account
	destID, err := DecodeClassicAddress(destination)
	if err != nil {
		t.Fatalf("decode destination: %v", err)
	}

	// "BSF1" || 32-byte reference — the exact shape BridgeSafeController expects.
	memo := append([]byte("BSF1"), bytes.Repeat([]byte{0xAB}, 32)...)

	p := &Payment{
		Account:            accountID,
		Destination:        destID,
		AmountDrops:        1_000_000, // 1 test XRP
		FeeDrops:           12,
		Sequence:           sequence,
		LastLedgerSequence: ledgerSeq + 20,
		Memo:               memo,
		SigningPubKey:      pub,
	}

	signed, err := p.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	t.Logf("predicted tx id: %s", signed.TxID)

	// 5. Submit the blob.
	res := rpc(t, "submit", map[string]any{"tx_blob": signed.TxBlob})
	engine, _ := res["engine_result"].(string)
	t.Logf("engine_result: %s (%v)", engine, res["engine_result_message"])
	if engine != "tesSUCCESS" && engine != "terQUEUED" {
		t.Fatalf("ledger rejected our serialization: %s — %v", engine, res)
	}

	// The id we predicted before submission must be the id the ledger reports.
	if submittedHash, ok := res["tx_json"].(map[string]any)["hash"].(string); ok {
		if !strings.EqualFold(submittedHash, signed.TxID) {
			t.Fatalf("transaction id mismatch: predicted %s, ledger says %s", signed.TxID, submittedHash)
		}
		t.Logf("predicted transaction id matches the ledger")
	}

	// 6. Wait for validation and confirm the payment landed as specified.
	tx := waitForValidation(t, signed.TxID)

	if got := jsonString(tx, "Account"); got != address {
		t.Errorf("source: got %s, want %s", got, address)
	}
	if got := jsonString(tx, "Destination"); got != destination {
		t.Errorf("destination: got %s, want %s", got, destination)
	}
	if got := jsonString(tx, "Amount"); got != "1000000" {
		t.Errorf("amount: got %s, want 1000000", got)
	}

	memos, _ := tx["Memos"].([]any)
	if len(memos) != 1 {
		t.Fatalf("expected exactly one memo, got %d", len(memos))
	}
	gotMemo := memos[0].(map[string]any)["Memo"].(map[string]any)["MemoData"].(string)
	wantMemo := strings.ToUpper(hex.EncodeToString(memo))
	if !strings.EqualFold(gotMemo, wantMemo) {
		t.Errorf("memo: got %s, want %s", gotMemo, wantMemo)
	} else {
		t.Logf("memo survived intact: %s", gotMemo)
	}

	t.Logf("VALIDATED: https://testnet.xrpl.org/transactions/%s", signed.TxID)
}

func fundAddress(t *testing.T, address string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"destination": address})
	resp, err := http.Post(testnetFaucet, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("faucet: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		t.Fatalf("faucet returned %d: %s", resp.StatusCode, b)
	}
	t.Logf("faucet: %s", strings.TrimSpace(string(b)))

	// The faucet funds on the next validated ledger.
	for i := 0; i < 30; i++ {
		info := rpc(t, "account_info", map[string]any{"account": address, "ledger_index": "validated"})
		if _, ok := info["account_data"]; ok {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("account %s never appeared after funding", address)
}

func currentLedger(t *testing.T, state map[string]any) uint32 {
	t.Helper()
	info, _ := state["info"].(map[string]any)
	vl, _ := info["validated_ledger"].(map[string]any)
	seq, _ := vl["seq"].(float64)
	if seq == 0 {
		t.Fatal("could not read the current validated ledger")
	}
	return uint32(seq)
}

func waitForValidation(t *testing.T, txID string) map[string]any {
	t.Helper()
	for i := 0; i < 30; i++ {
		tx := rpc(t, "tx", map[string]any{"transaction": txID})
		if validated, _ := tx["validated"].(bool); validated {
			return tx
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("transaction %s was not validated in time", txID)
	return nil
}

func jsonString(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}
