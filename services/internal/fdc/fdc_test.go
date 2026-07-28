package fdc

import (
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// realResponse is an actual FDC XRPPayment response, captured from the live
// testnet verifier for the payment BridgeSafe's own codec produced and submitted
// (XRPL tx 64FA5251...7170, ledger 19432285).
//
// Parsing is where a settlement quietly fails: every numeric field arrives as a
// decimal string because the values exceed JavaScript's safe integer range, and
// misreading one produces a proof the contract rejects for the wrong reason.
const realResponse = `{
  "attestationType": "0x5852505061796d656e7400000000000000000000000000000000000000000000",
  "sourceId": "0x7465737458525000000000000000000000000000000000000000000000000000",
  "votingRound": "1041284",
  "lowestUsedTimestamp": "1785231421",
  "requestBody": {
    "transactionId": "0x64fa5251fedea72f251ed3003232e2f6f0cc06625ab568b94c9d314ee7007170",
    "proofOwner": "0x0000000000000000000000000000000000000000"
  },
  "responseBody": {
    "blockNumber": "19432461",
    "blockTimestamp": "1785231421",
    "sourceAddress": "rafZ4XSb7yjk5Rptmu9iTYLkUQBhznDuPf",
    "sourceAddressHash": "0x9e8210c86bb1c64baca166226a158dd51e2bb079f42fac910bb306d4e3d558b6",
    "receivingAddressHash": "0x7f5b4967a9fbe9b447fed6d4e3699051516b6afe5f94db2e77ccf86470bfd74d",
    "intendedReceivingAddressHash": "0x7f5b4967a9fbe9b447fed6d4e3699051516b6afe5f94db2e77ccf86470bfd74d",
    "spentAmount": "1000012",
    "intendedSpentAmount": "1000012",
    "receivedAmount": "1000000",
    "intendedReceivedAmount": "1000000",
    "hasMemoData": true,
    "firstMemoData": "0x42534631abababababababababababababababababababababababababababababababab",
    "hasDestinationTag": false,
    "destinationTag": "0",
    "status": "0"
  }
}`

func TestParseRealVerifierResponse(t *testing.T) {
	resp, err := parseResponse(json.RawMessage(realResponse))
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}

	if got := "0x" + hex.EncodeToString(resp.AttestationType[:]); got != TypeXRPPayment {
		t.Errorf("attestation type: got %s, want %s", got, TypeXRPPayment)
	}
	if got := "0x" + hex.EncodeToString(resp.SourceID[:]); got != SourceTestXRP {
		t.Errorf("source id: got %s, want %s", got, SourceTestXRP)
	}
	if resp.VotingRound != 1041284 {
		t.Errorf("voting round: got %d", resp.VotingRound)
	}

	b := resp.ResponseBody
	if b.SourceAddress != "rafZ4XSb7yjk5Rptmu9iTYLkUQBhznDuPf" {
		t.Errorf("source address: got %s", b.SourceAddress)
	}
	if b.ReceivedAmount.Cmp(big.NewInt(1_000_000)) != 0 {
		t.Errorf("received amount: got %s, want 1000000", b.ReceivedAmount)
	}
	if b.Status != 0 {
		t.Errorf("status: got %d, want 0 (success)", b.Status)
	}
	if !b.HasMemoData {
		t.Fatal("hasMemoData is false")
	}

	// The memo must survive byte-for-byte: this is what BridgeSafeFdcVerifier
	// compares against `"BSF1" || memoRef`.
	wantMemo := append([]byte("BSF1"), repeat(0xAB, 32)...)
	if hex.EncodeToString(b.FirstMemoData) != hex.EncodeToString(wantMemo) {
		t.Errorf("memo: got %x, want %x", b.FirstMemoData, wantMemo)
	}
	if len(b.FirstMemoData) != 36 {
		t.Errorf("memo length: got %d, want 36", len(b.FirstMemoData))
	}

	// The source address hash must be keccak256 of the address as-is. The
	// contract stores the same value at treasury-binding time, so a mismatch here
	// would make every settlement fail with SourceAccountMismatch.
	if hex.EncodeToString(b.SourceAddressHash[:]) != "9e8210c86bb1c64baca166226a158dd51e2bb079f42fac910bb306d4e3d558b6" {
		t.Errorf("source address hash: got %x", b.SourceAddressHash)
	}
}

func TestAttestationIdentifiersArePadded(t *testing.T) {
	// FDC right-pads ASCII identifiers to 32 bytes. Getting this wrong produces
	// a request the verifier rejects outright.
	if TypeXRPPayment != "0x5852505061796d656e7400000000000000000000000000000000000000000000" {
		t.Errorf("XRPPayment identifier: got %s", TypeXRPPayment)
	}
	if SourceTestXRP != "0x7465737458525000000000000000000000000000000000000000000000000000" {
		t.Errorf("testXRP identifier: got %s", SourceTestXRP)
	}
	if len(strings.TrimPrefix(TypeXRPPayment, "0x")) != 64 {
		t.Error("identifier is not 32 bytes")
	}
}

func TestVotingRoundDerivation(t *testing.T) {
	// roundId = (timestamp - firstVotingRoundStartTs) / votingEpochDurationSeconds
	cases := map[uint64]uint64{
		FirstVotingRoundStartTs:                     0,
		FirstVotingRoundStartTs + VotingEpochSeconds: 1,
		FirstVotingRoundStartTs + VotingEpochSeconds*3 + 89: 3, // still inside round 3
		1785231421: (1785231421 - FirstVotingRoundStartTs) / VotingEpochSeconds,
	}
	for ts, want := range cases {
		if got := VotingRoundFor(ts); got != want {
			t.Errorf("timestamp %d: got round %d, want %d", ts, got, want)
		}
	}
}

func TestParseRejectsMalformedFields(t *testing.T) {
	cases := map[string]string{
		"short hash":       strings.Replace(realResponse, `"0x9e8210c86bb1c64baca166226a158dd51e2bb079f42fac910bb306d4e3d558b6"`, `"0xdeadbeef"`, 1),
		"non-numeric":      strings.Replace(realResponse, `"receivedAmount": "1000000"`, `"receivedAmount": "not-a-number"`, 1),
		"bad memo hex":     strings.Replace(realResponse, `"0x42534631abababababababababababababababababababababababababababababababab"`, `"0xzz"`, 1),
	}
	for name, body := range cases {
		if _, err := parseResponse(json.RawMessage(body)); err == nil {
			t.Errorf("%s: parsed a malformed response without error", name)
		}
	}
}

func repeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
