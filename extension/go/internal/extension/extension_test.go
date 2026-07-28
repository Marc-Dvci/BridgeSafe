package extension

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"bridgesafe-extension/internal/codec"
	"bridgesafe-extension/internal/config"
	"bridgesafe-extension/internal/xrpl"
	"bridgesafe-extension/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"
)

// --- fixtures --------------------------------------------------------------

type stubLedger struct {
	sequence uint32
	ledger   uint32
	err      error
}

func (s *stubLedger) AccountSequence(string) (uint32, error) {
	return s.sequence, s.err
}
func (s *stubLedger) CurrentLedger() (uint32, error) {
	return s.ledger, s.err
}

const (
	testController = "0x00000000000000000000000000000000000C0DE1"
	testChainID    = 114 // Coston2
	testPayee      = "rPT1Sjq2YGrBMTttX4GZHjKu9dyfzbpAYe"
	xrpDrop        = 1_000_000
)

func newTestExtension(t *testing.T) *Extension {
	t.Helper()
	e := &Extension{
		ledger:         &stubLedger{sequence: 7, ledger: 19_500_000},
		treasuries:     make(map[string]*treasury),
		authorizations: make(map[string]*authorization),
	}
	// The enclave's /decrypt is stubbed out as identity: these tests exercise the
	// policy engine and the signer, not ECIES itself.
	e.decrypt = func(ciphertext []byte) ([]byte, error) { return ciphertext, nil }
	return e
}

func testPolicy() types.Policy {
	return types.Policy{
		MaxPerPaymentDrops: big.NewInt(100 * xrpDrop),
		MaxTotalDrops:      big.NewInt(500 * xrpDrop),
		RequestTtlSeconds:  1800,
	}
}

func header(t *testing.T, treasuryID, requestID int64, memoRef [32]byte) types.InstructionHeader {
	t.Helper()
	commitment, err := codec.PolicyCommitment(testPolicy())
	if err != nil {
		t.Fatalf("policy commitment: %v", err)
	}
	return types.InstructionHeader{
		ChainId:          big.NewInt(testChainID),
		Controller:       common.HexToAddress(testController),
		TreasuryId:       big.NewInt(treasuryID),
		RequestId:        big.NewInt(requestID),
		Nonce:            1,
		ExpiresAt:        uint64(nowUnix()) + 1800,
		MemoRef:          memoRef,
		PolicyCommitment: commitment,
	}
}

func memoRefFixture() [32]byte {
	var m [32]byte
	for i := range m {
		m[i] = byte(i + 1)
	}
	return m
}

// action wraps an ABI-encoded message the way the TEE node delivers it.
func action(command string, message []byte) (teetypes.Action, *instruction.DataFixed) {
	df := &instruction.DataFixed{
		OPType:          teeutils.ToHash(config.OPTypeTreasury),
		OPCommand:       teeutils.ToHash(command),
		OriginalMessage: message,
	}
	a := teetypes.Action{}
	a.Data.ID = teeutils.ToHash("action-id")
	a.Data.SubmissionTag = "tag"
	return a, df
}

// createTreasury runs CREATE_TREASURY_KEY and returns the generated address.
func createTreasury(t *testing.T, e *Extension, treasuryID int64) string {
	t.Helper()
	h := header(t, treasuryID, 0, [32]byte{})
	msg := encodeHeaderPolicy(t, h, testPolicy())
	a, df := action(config.OPCommandCreateTreasuryKey, msg)

	res := e.handleCreateTreasuryKey(a, df)
	if res.Status != 1 {
		t.Fatalf("CREATE_TREASURY_KEY failed: %s", res.Log)
	}

	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.treasuries[big.NewInt(treasuryID).String()].address
}

func authorizePayment(t *testing.T, e *Extension, treasuryID, requestID int64, dest string, drops int64) teetypes.ActionResult {
	t.Helper()
	h := header(t, treasuryID, requestID, memoRefFixture())
	payload := encodePaymentInstruction(t, h, dest, drops)
	msg := encodeHeaderPayload(t, h, payload)
	a, df := action(config.OPCommandAuthorizePayment, msg)
	return e.handleAuthorizePayment(a, df)
}

// --- CREATE_TREASURY_KEY ---------------------------------------------------

func TestCreateTreasuryKey_GeneratesValidAddress(t *testing.T) {
	e := newTestExtension(t)
	address := createTreasury(t, e, 1)

	if err := xrpl.ValidateClassicAddress(address); err != nil {
		t.Fatalf("generated address %q is not a valid r-address: %v", address, err)
	}
	if !strings.HasPrefix(address, "r") {
		t.Errorf("address %q does not start with 'r'", address)
	}
}

func TestCreateTreasuryKey_RefusesToRegenerate(t *testing.T) {
	// Regenerating would orphan any XRP already sent to the first address.
	e := newTestExtension(t)
	createTreasury(t, e, 1)

	h := header(t, 1, 0, [32]byte{})
	a, df := action(config.OPCommandCreateTreasuryKey, encodeHeaderPolicy(t, h, testPolicy()))
	if res := e.handleCreateTreasuryKey(a, df); res.Status == 1 {
		t.Error("enclave regenerated a key for an existing treasury")
	}
}

func TestCreateTreasuryKey_RejectsMismatchedPolicyCommitment(t *testing.T) {
	e := newTestExtension(t)
	h := header(t, 1, 0, [32]byte{})
	h.PolicyCommitment = [32]byte{0xFF} // does not match the policy sent alongside

	a, df := action(config.OPCommandCreateTreasuryKey, encodeHeaderPolicy(t, h, testPolicy()))
	res := e.handleCreateTreasuryKey(a, df)
	if res.Status == 1 {
		t.Error("enclave cached a policy the contract did not commit to")
	}
	if !strings.Contains(res.Log, "commitment") {
		t.Errorf("unhelpful error: %s", res.Log)
	}
}

// TestNoHandlerEverExposesKeyMaterial guards the property the whole design rests
// on: the treasury key must never leave the enclave.
func TestNoHandlerEverExposesKeyMaterial(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)

	e.mu.RLock()
	secret := hex.EncodeToString(e.treasuries["1"].key.Serialize())
	e.mu.RUnlock()

	res := authorizePayment(t, e, 1, 1, testPayee, 25*xrpDrop)
	if res.Status != 1 {
		t.Fatalf("authorize failed: %s", res.Log)
	}
	signRes := signPayment(t, e, 1, 1)
	if signRes.Status != 1 {
		t.Fatalf("sign failed: %s", signRes.Log)
	}

	for name, blob := range map[string][]byte{
		"authorization result": res.Data,
		"signed payment result": signRes.Data,
	} {
		if strings.Contains(strings.ToLower(hex.EncodeToString(blob)), strings.ToLower(secret)) {
			t.Errorf("%s leaked the treasury private key", name)
		}
	}
}

// --- AUTHORIZE_PAYMENT -----------------------------------------------------

func TestAuthorizePayment_AcceptsCompliantPayment(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)

	res := authorizePayment(t, e, 1, 1, testPayee, 25*xrpDrop)
	if res.Status != 1 {
		t.Fatalf("authorize failed: %s", res.Log)
	}

	e.mu.RLock()
	auth := e.authorizations["1"]
	e.mu.RUnlock()
	if auth == nil {
		t.Fatal("authorization was not retained for the signing step")
	}
	if auth.amountDrops != 25*xrpDrop {
		t.Errorf("amount: got %d, want %d", auth.amountDrops, 25*xrpDrop)
	}
	if auth.destination != testPayee {
		t.Errorf("destination: got %s, want %s", auth.destination, testPayee)
	}
}

func TestAuthorizePayment_RejectsOverPerPaymentCap(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)

	res := authorizePayment(t, e, 1, 1, testPayee, 101*xrpDrop)
	if res.Status == 1 {
		t.Fatal("enclave authorized a payment above the per-payment cap")
	}
	if !strings.Contains(res.Log, "per-payment cap") {
		t.Errorf("unhelpful error: %s", res.Log)
	}
}

func TestAuthorizePayment_RejectsInvalidDestination(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)

	for name, dest := range map[string]string{
		"ethereum address": "0x1111111111111111111111111111111111111111",
		"bad checksum":     "rHb9CJAWyB4rj91VRWn96DkukG4bwdtyTX",
		"empty":            "",
	} {
		res := authorizePayment(t, e, 1, int64(len(name)), dest, 1*xrpDrop)
		if res.Status == 1 {
			t.Errorf("%s: enclave authorized a payment to %q", name, dest)
		}
	}
}

func TestAuthorizePayment_RejectsPaymentToItself(t *testing.T) {
	e := newTestExtension(t)
	own := createTreasury(t, e, 1)

	if res := authorizePayment(t, e, 1, 1, own, 1*xrpDrop); res.Status == 1 {
		t.Error("enclave authorized the treasury paying itself")
	}
}

// TestAuthorizePayment_RejectsCiphertextFromAnotherRequest is the replay case:
// a payload sealed for request 1 must not be usable as request 2.
func TestAuthorizePayment_RejectsCiphertextFromAnotherRequest(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)

	// Payload names request 1 ...
	h1 := header(t, 1, 1, memoRefFixture())
	payload := encodePaymentInstruction(t, h1, testPayee, 25*xrpDrop)

	// ... but is delivered under a header for request 2.
	h2 := header(t, 1, 2, memoRefFixture())
	a, df := action(config.OPCommandAuthorizePayment, encodeHeaderPayload(t, h2, payload))

	res := e.handleAuthorizePayment(a, df)
	if res.Status == 1 {
		t.Fatal("enclave accepted a ciphertext belonging to a different request")
	}
	if !strings.Contains(res.Log, "request id") {
		t.Errorf("unhelpful error: %s", res.Log)
	}
}

func TestAuthorizePayment_RejectsStalePolicyCommitment(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)

	h := header(t, 1, 1, memoRefFixture())
	payload := encodePaymentInstruction(t, h, testPayee, 25*xrpDrop)
	h.PolicyCommitment = [32]byte{0xAB} // contract believes a different policy is live
	a, df := action(config.OPCommandAuthorizePayment, encodeHeaderPayload(t, h, payload))

	if res := e.handleAuthorizePayment(a, df); res.Status == 1 {
		t.Error("enclave authorized under a policy the contract no longer believes is live")
	}
}

func TestAuthorizePayment_RejectsUnknownTreasury(t *testing.T) {
	e := newTestExtension(t)
	if res := authorizePayment(t, e, 99, 1, testPayee, 1*xrpDrop); res.Status == 1 {
		t.Error("enclave authorized against a treasury it holds no key for")
	}
}

// --- SIGN_XRPL_PAYMENT -----------------------------------------------------

func signPayment(t *testing.T, e *Extension, treasuryID, requestID int64) teetypes.ActionResult {
	t.Helper()
	h := header(t, treasuryID, requestID, memoRefFixture())
	a, df := action(config.OPCommandSignXRPLPayment, encodeHeaderOnly(t, h))
	return e.handleSignPayment(a, df)
}

func TestSignPayment_ProducesBroadcastableTransaction(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)
	if res := authorizePayment(t, e, 1, 1, testPayee, 25*xrpDrop); res.Status != 1 {
		t.Fatalf("authorize: %s", res.Log)
	}

	res := signPayment(t, e, 1, 1)
	if res.Status != 1 {
		t.Fatalf("sign failed: %s", res.Log)
	}

	_, _, _, memoRef, txID, blob := decodeSignedPayment(t, res.Data)
	if memoRef != memoRefFixture() {
		t.Error("signed result carries the wrong memo reference")
	}
	if txID == ([32]byte{}) {
		t.Error("signed result has no transaction id")
	}
	// A Payment always serializes with TransactionType 0x12 0x0000 first.
	if !strings.HasPrefix(strings.ToUpper(hex.EncodeToString(blob)), "120000") {
		t.Errorf("signed blob is not a Payment: starts %x", blob[:3])
	}
	// The memo the contract will compare against must be embedded verbatim.
	wantMemo := hex.EncodeToString(memoBytes(memoRefFixture()))
	if !strings.Contains(hex.EncodeToString(blob), wantMemo) {
		t.Error("signed blob does not embed the BridgeSafe memo")
	}
}

func TestSignPayment_RequiresPriorAuthorization(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)

	res := signPayment(t, e, 1, 1)
	if res.Status == 1 {
		t.Fatal("enclave signed a payment that was never authorized")
	}
	if !strings.Contains(res.Log, "never authorized") {
		t.Errorf("unhelpful error: %s", res.Log)
	}
}

// TestSignPayment_ConsumesTheAuthorization proves one authorization yields at
// most one signature, so a replayed signing instruction cannot mint a second
// broadcastable payment against the same reserved budget.
func TestSignPayment_ConsumesTheAuthorization(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)
	if res := authorizePayment(t, e, 1, 1, testPayee, 25*xrpDrop); res.Status != 1 {
		t.Fatalf("authorize: %s", res.Log)
	}

	if res := signPayment(t, e, 1, 1); res.Status != 1 {
		t.Fatalf("first sign: %s", res.Log)
	}
	if res := signPayment(t, e, 1, 1); res.Status == 1 {
		t.Error("enclave signed the same authorization twice")
	}
}

func TestSignPayment_RejectsMismatchedMemoReference(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)
	if res := authorizePayment(t, e, 1, 1, testPayee, 25*xrpDrop); res.Status != 1 {
		t.Fatalf("authorize: %s", res.Log)
	}

	var other [32]byte
	other[0] = 0xFF
	h := header(t, 1, 1, other)
	a, df := action(config.OPCommandSignXRPLPayment, encodeHeaderOnly(t, h))

	if res := e.handleSignPayment(a, df); res.Status == 1 {
		t.Error("enclave signed against a memo reference it never authorized")
	}
}

// TestSignPayment_BindsLastLedgerSequenceToExpiry checks that expiry is actually
// enforceable on XRPL rather than only asserted on Flare.
func TestSignPayment_BindsLastLedgerSequenceToExpiry(t *testing.T) {
	e := newTestExtension(t)
	current := uint32(19_500_000)
	e.ledger = &stubLedger{sequence: 7, ledger: current}

	createTreasury(t, e, 1)
	if res := authorizePayment(t, e, 1, 1, testPayee, 25*xrpDrop); res.Status != 1 {
		t.Fatalf("authorize: %s", res.Log)
	}
	res := signPayment(t, e, 1, 1)
	if res.Status != 1 {
		t.Fatalf("sign: %s", res.Log)
	}

	_, _, _, _, _, blob := decodeSignedPayment(t, res.Data)
	// LastLedgerSequence is field 0x201B; the 4 bytes after it are the value.
	idx := strings.Index(hex.EncodeToString(blob), "201b")
	if idx < 0 {
		t.Fatal("signed transaction has no LastLedgerSequence — it could be included indefinitely")
	}
	raw := hex.EncodeToString(blob)[idx+4 : idx+12]
	var got uint32
	for i := 0; i < 8; i += 2 {
		var b uint32
		_, err := hexByte(raw[i:i+2], &b)
		if err != nil {
			t.Fatalf("parsing LastLedgerSequence: %v", err)
		}
		got = got<<8 | b
	}
	if got <= current {
		t.Errorf("LastLedgerSequence %d is not in the future (current %d)", got, current)
	}
	if got > current+config.MaxLedgerWindow {
		t.Errorf("LastLedgerSequence %d exceeds the %d-ledger cap", got, config.MaxLedgerWindow)
	}
}

func TestSignPayment_SurfacesUnfundedAccount(t *testing.T) {
	e := newTestExtension(t)
	createTreasury(t, e, 1)
	if res := authorizePayment(t, e, 1, 1, testPayee, 25*xrpDrop); res.Status != 1 {
		t.Fatalf("authorize: %s", res.Log)
	}
	e.ledger = &stubLedger{err: errUnfunded{}}

	res := signPayment(t, e, 1, 1)
	if res.Status == 1 {
		t.Fatal("enclave signed despite being unable to read the account sequence")
	}
}

type errUnfunded struct{}

func (errUnfunded) Error() string { return "actNotFound" }

// --- routing ---------------------------------------------------------------

func TestRoutingRejectsUnknownOperations(t *testing.T) {
	e := newTestExtension(t)

	a, df := action("NOT_A_COMMAND", []byte{})
	status, body := e.processTreasury(a, df)
	if status == 200 {
		t.Error("unknown op command was routed to a handler")
	}
	if !strings.Contains(string(body), "unsupported op command") {
		t.Errorf("unhelpful error: %s", body)
	}
}

// --- encoding helpers ------------------------------------------------------

func hexByte(s string, out *uint32) (int, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return 0, err
	}
	*out = uint32(b[0])
	return 1, nil
}
