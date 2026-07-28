package xrpl

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// fixedKey returns a deterministic signing key so the vectors below are stable.
func fixedKey(t *testing.T) *secp256k1.PrivateKey {
	t.Helper()
	var scalar [32]byte
	for i := range scalar {
		scalar[i] = byte(i + 1)
	}
	return secp256k1.PrivKeyFromBytes(scalar[:])
}

const (
	wantAddress = "r9h1jtYfLtUsP33aETeFwnQhpVGqiATK3a"
	wantAcctID  = "587c9bfc87a837a056f716c2f9de891adbc46c90"
	destination = "rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe"
)

func testMemo() []byte {
	return append([]byte("BSF1"), bytes.Repeat([]byte{0xAB}, 32)...)
}

func testPayment(t *testing.T) (*Payment, *secp256k1.PrivateKey) {
	t.Helper()
	key := fixedKey(t)
	pub := key.PubKey().SerializeCompressed()
	acct, err := AccountIDFromPublicKey(pub)
	if err != nil {
		t.Fatalf("derive account id: %v", err)
	}
	dest, err := DecodeClassicAddress(destination)
	if err != nil {
		t.Fatalf("decode destination: %v", err)
	}
	return &Payment{
		Account:            acct,
		Destination:        dest,
		AmountDrops:        25_000_000,
		FeeDrops:           12,
		Sequence:           7,
		LastLedgerSequence: 19_500_000,
		Memo:               testMemo(),
		SigningPubKey:      pub,
	}, key
}

// --- Address derivation ---------------------------------------------------

func TestAccountIDAndAddressDerivation(t *testing.T) {
	key := fixedKey(t)
	acct, err := AccountIDFromPublicKey(key.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	if got := hex.EncodeToString(acct); got != wantAcctID {
		t.Errorf("account id: got %s, want %s", got, wantAcctID)
	}
	addr, err := EncodeClassicAddress(acct)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if addr != wantAddress {
		t.Errorf("address: got %s, want %s", addr, wantAddress)
	}
}

// TestDecodesRealMainnetAndTestnetAddresses checks the base58 alphabet and the
// checksum against addresses this code did not produce, so a self-consistent but
// wrong implementation cannot pass.
func TestDecodesRealAddresses(t *testing.T) {
	for _, addr := range []string{
		"rHb9CJAWyB4rj91VRWn96DkukG4bwdtyTh", // XRPL genesis account
		"rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe",
		"rafZ4XSb7yjk5Rptmu9iTYLkUQBhznDuPf", // treasury from the live test
	} {
		id, err := DecodeClassicAddress(addr)
		if err != nil {
			t.Errorf("%s: %v", addr, err)
			continue
		}
		if len(id) != 20 {
			t.Errorf("%s: account id is %d bytes, want 20", addr, len(id))
		}
		back, err := EncodeClassicAddress(id)
		if err != nil || back != addr {
			t.Errorf("%s: round trip gave %s (%v)", addr, back, err)
		}
	}
}

func TestRejectsMalformedAddresses(t *testing.T) {
	cases := map[string]string{
		"empty":            "",
		"bad checksum":     "rHb9CJAWyB4rj91VRWn96DkukG4bwdtyTX",
		"invalid alphabet": "rHb9CJAWyB4rj91VRWn96DkukG4bwdt0Th", // '0' is not in the XRPL alphabet
		"too short":        "rHb9CJAW",
		"ethereum address": "0x1111111111111111111111111111111111111111",
	}
	for name, addr := range cases {
		if err := ValidateClassicAddress(addr); err == nil {
			t.Errorf("%s: accepted %q, want rejection", name, addr)
		}
	}
}

// --- Serialization --------------------------------------------------------

// TestSigningPayloadIsCanonical pins the exact bytes signed.
//
// These vectors are golden: they were produced by this implementation and then
// confirmed end to end by TestLive_PaymentIsAcceptedByTheLedger, which submitted
// an equivalently-built payment and had the ledger validate it and report back a
// matching transaction id and memo. Their job here is regression protection —
// the live test is the independent oracle.
func TestSigningPayloadIsCanonical(t *testing.T) {
	p, _ := testPayment(t)
	payload, err := p.SigningPayload()
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	got := hex.EncodeToString(payload)

	// Assembled field by field rather than pasted as one blob, so a failure
	// points at the field that moved and the encoding stays readable.
	want := strings.Join([]string{
		"53545800",                 // "STX\0" signing prefix
		"12", "0000",               // TransactionType = Payment (0)
		"22", "00000000",           // Flags = 0
		"24", "00000007",           // Sequence = 7
		"201b", "01298be0",         // LastLedgerSequence = 19,500,000
		"61", "40000000017d7840",   // Amount = 25,000,000 drops, native + positive bits
		"68", "400000000000000c",   // Fee = 12 drops (also an Amount, so same bits)
		"73", "21",                 // SigningPubKey, VL length 33
		"0284bf7562262bbd6940085748f3be6afa52ae317155181ece31b66351ccffa4b0",
		"8114",                     // Account, VL length 20
		"587c9bfc87a837a056f716c2f9de891adbc46c90",
		"8314",                     // Destination, VL length 20
		"f667b0ca50cc7709a220b0561b85e53a48461fa8",
		"f9",                       // Memos array
		"ea",                       // Memo object
		"7c", "0a", hex.EncodeToString([]byte(memoTypeBridgeSafe)),
		"7d", "24",                 // MemoData, VL length 36
		hex.EncodeToString(testMemo()),
		"e1",                       // end of Memo object
		"f1",                       // end of Memos array
	}, "")

	if got != want {
		t.Errorf("signing payload mismatch\n got: %s\nwant: %s", got, want)
	}

	// Structural assertions, so a failure above says *what* moved.
	if !strings.HasPrefix(got, "53545800") {
		t.Error("missing the STX\\0 signing prefix")
	}
	if !strings.Contains(got, "6140000000017d7840") {
		t.Error("amount is not encoded as a positive native XRP value (bit 62 set, bit 63 clear)")
	}
	if strings.Contains(got, "7447") || strings.Contains(got, "7446") {
		t.Error("signing payload must not contain a TxnSignature field")
	}
}

func TestSignedBlobAndTransactionID(t *testing.T) {
	p, key := testPayment(t)
	signed, err := p.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	const wantTxID = "4E2B02520B82C04522C6D0BEB15A83C914DCDA7712C2A1EE475C86EF576FF44E"
	if signed.TxID != wantTxID {
		t.Errorf("tx id: got %s, want %s", signed.TxID, wantTxID)
	}
	if !strings.Contains(signed.TxBlob, "7447") && !strings.Contains(signed.TxBlob, "7446") {
		t.Error("signed blob is missing the TxnSignature field")
	}
	// XRPL requires DER; a 65-byte [r||s||v] signature would be a silent rejection.
	if signed.Signature[0] != 0x30 {
		t.Errorf("signature is not DER-encoded: first byte 0x%02x, want 0x30", signed.Signature[0])
	}
}

// TestSigningIsDeterministic matters because the contract records the expected
// transaction id before the payment is broadcast. A non-deterministic signature
// would make that prediction wrong.
func TestSigningIsDeterministic(t *testing.T) {
	p1, key := testPayment(t)
	p2, _ := testPayment(t)

	a, err := p1.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	b, err := p2.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if a.TxID != b.TxID || a.TxBlob != b.TxBlob {
		t.Error("signing the same payment twice produced different results")
	}
}

func TestMemoIsOmittedWhenEmpty(t *testing.T) {
	p, key := testPayment(t)
	p.Memo = nil
	signed, err := p.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if strings.Contains(signed.TxBlob, "F9EA") {
		t.Error("empty memo still emitted a Memos array")
	}
}

func TestDestinationTagIsOptional(t *testing.T) {
	p, key := testPayment(t)
	withoutTag, err := p.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	p2, _ := testPayment(t)
	p2.HasDestinationTag = true
	p2.DestinationTag = 12345
	withTag, err := p2.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if strings.Contains(withoutTag.TxBlob, "2E00003039") {
		t.Error("destination tag emitted when HasDestinationTag is false")
	}
	if !strings.Contains(withTag.TxBlob, "2E00003039") {
		t.Error("destination tag missing when HasDestinationTag is true")
	}
}

// --- Validation -----------------------------------------------------------

func TestValidateRejectsBadPayments(t *testing.T) {
	cases := map[string]func(*Payment){
		"zero amount":            func(p *Payment) { p.AmountDrops = 0 },
		"amount above supply":    func(p *Payment) { p.AmountDrops = MaxDrops + 1 },
		"zero fee":               func(p *Payment) { p.FeeDrops = 0 },
		"short account":          func(p *Payment) { p.Account = p.Account[:19] },
		"short destination":      func(p *Payment) { p.Destination = p.Destination[:10] },
		"oversized memo":         func(p *Payment) { p.Memo = bytes.Repeat([]byte{1}, MaxMemoLength+1) },
		"bad public key":         func(p *Payment) { p.SigningPubKey = []byte{1, 2, 3} },
		"no last ledger":         func(p *Payment) { p.LastLedgerSequence = 0 },
	}
	for name, mutate := range cases {
		p, _ := testPayment(t)
		mutate(p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: Validate accepted an invalid payment", name)
		}
	}
}

func TestSignRejectsMismatchedPublicKey(t *testing.T) {
	p, key := testPayment(t)
	other, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	p.SigningPubKey = other.PubKey().SerializeCompressed()

	if _, err := p.Sign(key); err == nil {
		t.Error("signed a payment whose SigningPubKey belongs to a different key")
	}
}

// TestCodecCannotExpressAnotherTransactionType is the security property in test
// form: the serialized type field is hard-coded, so no input reachable from a
// decrypted instruction can turn a Payment into anything else.
func TestCodecCannotExpressAnotherTransactionType(t *testing.T) {
	p, key := testPayment(t)
	signed, err := p.Sign(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// 1200 00 == TransactionType field id 0x12 with value 0x0000 (Payment).
	if !strings.HasPrefix(signed.TxBlob, "120000") {
		t.Fatalf("transaction type is not Payment: blob starts %s", signed.TxBlob[:8])
	}
	if txTypePayment != 0 {
		t.Fatal("txTypePayment must remain 0 (Payment)")
	}
}
