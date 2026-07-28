package codec_test

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"bridgesafe-extension/internal/codec"
	"bridgesafe-extension/pkg/types"

	"github.com/ethereum/go-ethereum/common"
)

// TestWriteCrossLanguageVectors emits the exact bytes the enclave would sign, so
// the Solidity side can prove it decodes them.
//
// This is the seam most likely to break silently: an ABI mismatch between Go and
// Solidity does not fail any build. It produces a correctly-signed result that
// the contract then refuses, which looks like a signing bug and is not one. The
// vectors written here are consumed by contracts/test/CrossLanguage.t.sol, so a
// drift on either side turns into a failing test rather than a mystery on chain.
//
// Run `go test ./internal/codec/` then `forge test` to check both directions.
func TestWriteCrossLanguageVectors(t *testing.T) {
	chainID := big.NewInt(114) // Coston2
	controller := common.HexToAddress("0x00000000000000000000000000000000000C0DE1")
	treasuryID := big.NewInt(1)
	requestID := big.NewInt(42)

	var memoRef [32]byte
	for i := range memoRef {
		memoRef[i] = byte(i + 1)
	}
	var txID [32]byte
	for i := range txID {
		txID[i] = byte(0xF0 - i)
	}
	destinationHash := codec.Keccak256String("rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe")
	payloadHash := codec.Keccak256Hash([]byte("ciphertext"))

	policy := types.Policy{
		MaxPerPaymentDrops: big.NewInt(100_000_000),
		MaxTotalDrops:      big.NewInt(500_000_000),
		RequestTtlSeconds:  1800,
	}
	commitment, err := codec.PolicyCommitment(policy)
	if err != nil {
		t.Fatalf("policy commitment: %v", err)
	}

	signedBlob, err := hex.DecodeString("120000228000000024000000012E00000000")
	if err != nil {
		t.Fatalf("decode blob: %v", err)
	}

	bind, err := codec.EncodeBindTreasury(chainID, controller, treasuryID, "rafZ4XSb7yjk5Rptmu9iTYLkUQBhznDuPf", commitment)
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	confirm, err := codec.EncodeConfirmPolicy(chainID, controller, treasuryID, policy, commitment)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	authz, err := codec.EncodeAuthorization(chainID, controller, requestID, memoRef, big.NewInt(25_000_000), destinationHash, payloadHash)
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	signed, err := codec.EncodeSignedPayment(chainID, controller, requestID, memoRef, txID, signedBlob)
	if err != nil {
		t.Fatalf("signed: %v", err)
	}
	failure, err := codec.EncodeFailure(chainID, controller, requestID, "destination is not a valid r-address")
	if err != nil {
		t.Fatalf("failure: %v", err)
	}

	out := map[string]string{
		"bindTreasury":     "0x" + hex.EncodeToString(bind),
		"confirmPolicy":    "0x" + hex.EncodeToString(confirm),
		"authorization":    "0x" + hex.EncodeToString(authz),
		"signedPayment":    "0x" + hex.EncodeToString(signed),
		"failure":          "0x" + hex.EncodeToString(failure),
		"policyCommitment": "0x" + hex.EncodeToString(commitment[:]),
		"destinationHash":  "0x" + hex.EncodeToString(destinationHash[:]),
		"payloadHash":      "0x" + hex.EncodeToString(payloadHash[:]),
		"memoRef":          "0x" + hex.EncodeToString(memoRef[:]),
		"txId":             "0x" + hex.EncodeToString(txID[:]),
	}

	dir := filepath.Join("..", "..", "..", "..", "contracts", "test", "vectors")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating vector directory: %v", err)
	}
	body, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshalling vectors: %v", err)
	}
	path := filepath.Join(dir, "tee-results.json")
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	t.Logf("wrote %s", path)
}

// TestResultsRoundTripInGo catches an encoder/decoder mismatch on the Go side
// before the Solidity test has anything to say about it.
func TestResultsRoundTripInGo(t *testing.T) {
	chainID := big.NewInt(114)
	controller := common.HexToAddress("0x00000000000000000000000000000000000C0DE1")
	requestID := big.NewInt(42)
	var memoRef, txID [32]byte
	memoRef[0], txID[0] = 1, 2
	blob := []byte{0x12, 0x00, 0x00}

	encoded, err := codec.EncodeSignedPayment(chainID, controller, requestID, memoRef, txID, blob)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	gotChain, gotController, gotRequest, gotMemo, gotTx, gotBlob, err := codec.DecodeSignedPayment(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotChain.Cmp(chainID) != 0 || gotController != controller || gotRequest.Cmp(requestID) != 0 {
		t.Error("routing fields did not survive the round trip")
	}
	if gotMemo != memoRef || gotTx != txID || string(gotBlob) != string(blob) {
		t.Error("payload fields did not survive the round trip")
	}
}
