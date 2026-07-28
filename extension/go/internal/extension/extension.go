package extension

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"

	"bridgesafe-extension/internal/codec"
	"bridgesafe-extension/internal/config"
	"bridgesafe-extension/internal/xrpl"
	"bridgesafe-extension/pkg/types"

	"github.com/ethereum/go-ethereum/common"
	"github.com/flare-foundation/go-flare-common/pkg/tee/instruction"
	teetypes "github.com/flare-foundation/tee-node/pkg/types"
	"github.com/flare-foundation/tee-node/pkg/processorutils"
	teeutils "github.com/flare-foundation/tee-node/pkg/utils"

	secp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// treasury is the enclave's private record for one XRPL treasury.
//
// The signing key exists only here, in enclave memory. It is generated inside the
// TEE, never written anywhere, and never leaves — there is no handler that
// returns it and no serialization path that includes it.
type treasury struct {
	key       *secp256k1.PrivateKey
	accountID []byte
	address   string

	policy           types.Policy
	policyCommitment [32]byte
}

// authorization is a payment the enclave has approved but not yet signed.
//
// Splitting authorize from sign is what lets the contract reserve budget before
// a signature exists. The enclave keeps the approved terms here so the signing
// step cannot be handed different ones.
type authorization struct {
	treasuryID  *big.Int
	destination string
	amountDrops uint64
	tag         uint32
	hasTag      bool
	memoRef     [32]byte
	expiresAt   uint64
	payloadHash [32]byte
}

// Extension is the HTTP server the TEE node delivers instructions to.
type Extension struct {
	mu     sync.RWMutex
	Server *http.Server

	// signPort is the TEE node's port, used for its /decrypt endpoint.
	signPort int

	// ledger reads XRPL account state. Read-only by construction.
	ledger LedgerReader

	// decrypt unseals an ECIES payload. In production this is the TEE node's
	// /decrypt endpoint, which holds the enclave key; it is a field so tests can
	// drive the full handler path without a running node.
	decrypt func([]byte) ([]byte, error)

	treasuries     map[string]*treasury      // keyed by treasuryId.String()
	authorizations map[string]*authorization // keyed by requestId.String()
}

// LedgerReader is the slice of XRPL the enclave needs. Injected so tests can run
// the full handler path without a network.
type LedgerReader interface {
	AccountSequence(address string) (uint32, error)
	CurrentLedger() (uint32, error)
}

// New builds the extension server.
func New(extensionPort, signPort int) *Extension {
	e := &Extension{
		signPort:       signPort,
		ledger:         NewJSONRPCLedger(config.XRPLEndpoint),
		treasuries:     make(map[string]*treasury),
		authorizations: make(map[string]*authorization),
	}
	e.decrypt = func(ciphertext []byte) ([]byte, error) {
		return decryptViaNode(e.signPort, ciphertext)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", e.stateHandler)
	mux.HandleFunc("POST /action", e.actionHandler)

	e.Server = &http.Server{Addr: fmt.Sprintf(":%d", extensionPort), Handler: mux}
	return e
}

// stateHandler reports provisioning status. No key material, no payment terms.
func (e *Extension) stateHandler(w http.ResponseWriter, r *http.Request) {
	e.mu.RLock()
	addresses := make(map[string]string, len(e.treasuries))
	for id, t := range e.treasuries {
		addresses[id] = t.address
	}
	resp := types.StateResponse{
		StateVersion: teeutils.ToHash(config.Version),
		State: types.State{
			TreasuryCount:         len(e.treasuries),
			Addresses:             addresses,
			PendingAuthorizations: len(e.authorizations),
		},
	}
	e.mu.RUnlock()

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("sending response: %v", err), http.StatusInternalServerError)
	}
}

func (e *Extension) processAction(action teetypes.Action) (int, []byte) {
	dataFixed, err := processorutils.Parse[instruction.DataFixed](action.Data.Message)
	if err != nil {
		return http.StatusBadRequest, []byte(fmt.Sprintf("decoding fixed data: %v", err))
	}

	if dataFixed.OPType != teeutils.ToHash(config.OPTypeTreasury) {
		return http.StatusNotImplemented, []byte(fmt.Sprintf(
			"unsupported op type: received %s, expected %s (%s)",
			dataFixed.OPType.Hex(), teeutils.ToHash(config.OPTypeTreasury).Hex(), config.OPTypeTreasury,
		))
	}
	return e.processTreasury(action, dataFixed)
}

func (e *Extension) processTreasury(action teetypes.Action, df *instruction.DataFixed) (int, []byte) {
	var result teetypes.ActionResult

	switch df.OPCommand {
	case teeutils.ToHash(config.OPCommandCreateTreasuryKey):
		result = e.handleCreateTreasuryKey(action, df)
	case teeutils.ToHash(config.OPCommandRegisterPolicy):
		result = e.handleRegisterPolicy(action, df)
	case teeutils.ToHash(config.OPCommandAuthorizePayment):
		result = e.handleAuthorizePayment(action, df)
	case teeutils.ToHash(config.OPCommandSignXRPLPayment):
		result = e.handleSignPayment(action, df)
	default:
		return http.StatusNotImplemented, []byte(fmt.Sprintf(
			"unsupported op command: received %s, expected one of [%s, %s, %s, %s]",
			df.OPCommand.Hex(),
			config.OPCommandCreateTreasuryKey, config.OPCommandRegisterPolicy,
			config.OPCommandAuthorizePayment, config.OPCommandSignXRPLPayment,
		))
	}

	b, err := json.Marshal(result)
	if err != nil {
		return http.StatusInternalServerError, []byte(fmt.Sprintf("marshalling result: %v", err))
	}
	return http.StatusOK, b
}

// -----------------------------------------------------------------------
// CREATE_TREASURY_KEY
// -----------------------------------------------------------------------

// handleCreateTreasuryKey generates the treasury's XRPL keypair inside the
// enclave and returns only its public address.
func (e *Extension) handleCreateTreasuryKey(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	header, policy, err := codec.DecodeHeaderAndPolicy(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding instruction: %w", err))
	}
	if err := validateHeaderCommitment(header, policy); err != nil {
		return buildResult(a, df, nil, 0, err)
	}

	id := header.TreasuryId.String()

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.treasuries[id]; exists {
		// Regenerating a key would orphan any funds already sent to the old
		// address, and the contract will not rebind anyway.
		return buildResult(a, df, nil, 0, fmt.Errorf("treasury %s already has a key", id))
	}

	key, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("generating key: %w", err))
	}
	pub := key.PubKey().SerializeCompressed()
	accountID, err := xrpl.AccountIDFromPublicKey(pub)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("deriving account id: %w", err))
	}
	address, err := xrpl.EncodeClassicAddress(accountID)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("encoding address: %w", err))
	}

	e.treasuries[id] = &treasury{
		key:              key,
		accountID:        accountID,
		address:          address,
		policy:           policy,
		policyCommitment: header.PolicyCommitment,
	}

	data, err := codec.EncodeBindTreasury(
		header.ChainId, header.Controller, header.TreasuryId, address, header.PolicyCommitment,
	)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("encoding result: %w", err))
	}
	return buildResult(a, df, data, 1, nil)
}

// -----------------------------------------------------------------------
// REGISTER_POLICY
// -----------------------------------------------------------------------

// handleRegisterPolicy replaces the rules the enclave enforces for a treasury.
func (e *Extension) handleRegisterPolicy(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	header, policy, err := codec.DecodeHeaderAndPolicy(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding instruction: %w", err))
	}
	if err := validateHeaderCommitment(header, policy); err != nil {
		return buildResult(a, df, nil, 0, err)
	}

	e.mu.Lock()
	t, ok := e.treasuries[header.TreasuryId.String()]
	if ok {
		t.policy = policy
		t.policyCommitment = header.PolicyCommitment
	}
	e.mu.Unlock()

	if !ok {
		return buildResult(a, df, nil, 0, fmt.Errorf("unknown treasury %s", header.TreasuryId))
	}

	data, err := codec.EncodeConfirmPolicy(
		header.ChainId, header.Controller, header.TreasuryId, policy, header.PolicyCommitment,
	)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("encoding result: %w", err))
	}
	return buildResult(a, df, data, 1, nil)
}

// -----------------------------------------------------------------------
// AUTHORIZE_PAYMENT
// -----------------------------------------------------------------------

// handleAuthorizePayment decrypts a payment instruction and checks it against
// the treasury's policy. It reveals the amount and a hash of the destination so
// the contract can reserve budget, and nothing else.
func (e *Extension) handleAuthorizePayment(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	header, ciphertext, err := codec.DecodeHeaderAndPayload(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding instruction: %w", err))
	}
	if len(ciphertext) == 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("payment payload is empty"))
	}

	// The contract committed to keccak256(ciphertext). Recomputing it here means a
	// relay that swapped the payload for a different one is caught before any
	// policy decision is made.
	payloadHash := codec.Keccak256Hash(ciphertext)

	e.mu.RLock()
	t, ok := e.treasuries[header.TreasuryId.String()]
	e.mu.RUnlock()
	if !ok {
		return buildResult(a, df, nil, 0, fmt.Errorf("unknown treasury %s", header.TreasuryId))
	}
	if header.PolicyCommitment != t.policyCommitment {
		return buildResult(a, df, nil, 0, fmt.Errorf(
			"policy commitment mismatch: the contract and the enclave disagree about the live policy"))
	}

	plaintext, err := e.decrypt(ciphertext)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decrypting payload: %w", err))
	}

	pi, err := codec.DecodePaymentInstruction(plaintext)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding payment instruction: %w", err))
	}

	// The decrypted payload must name the same request as the header. Without
	// this, a ciphertext captured from one request could be replayed into another
	// with a different budget or deadline.
	if err := bindPayloadToHeader(pi, header); err != nil {
		return buildResult(a, df, nil, 0, err)
	}

	if err := xrpl.ValidateClassicAddress(pi.Destination); err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("destination %q: %w", pi.Destination, err))
	}
	if pi.Destination == t.address {
		return buildResult(a, df, nil, 0, fmt.Errorf("destination is the treasury itself"))
	}

	if !pi.AmountDrops.IsUint64() {
		return buildResult(a, df, nil, 0, fmt.Errorf("amount does not fit in uint64"))
	}
	amount := pi.AmountDrops.Uint64()
	if err := checkPolicy(t.policy, pi.AmountDrops); err != nil {
		return buildResult(a, df, nil, 0, err)
	}

	e.mu.Lock()
	e.authorizations[header.RequestId.String()] = &authorization{
		treasuryID:  header.TreasuryId,
		destination: pi.Destination,
		amountDrops: amount,
		tag:         pi.DestinationTag,
		hasTag:      pi.HasDestinationTag,
		memoRef:     header.MemoRef,
		expiresAt:   header.ExpiresAt,
		payloadHash: payloadHash,
	}
	e.mu.Unlock()

	data, err := codec.EncodeAuthorization(
		header.ChainId,
		header.Controller,
		header.RequestId,
		header.MemoRef,
		pi.AmountDrops,
		codec.Keccak256String(pi.Destination),
		payloadHash,
	)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("encoding result: %w", err))
	}
	return buildResult(a, df, data, 1, nil)
}

// -----------------------------------------------------------------------
// SIGN_XRPL_PAYMENT
// -----------------------------------------------------------------------

// handleSignPayment builds and signs the canonical XRPL Payment for a request
// the enclave previously authorized.
//
// The terms come from enclave memory, not from this instruction: the only thing
// the signing step is told is *which* request to sign. That is what makes it
// impossible to authorize one payment and sign a different one.
func (e *Extension) handleSignPayment(a teetypes.Action, df *instruction.DataFixed) teetypes.ActionResult {
	header, err := codec.DecodeHeader(df.OriginalMessage)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("decoding instruction: %w", err))
	}

	e.mu.RLock()
	auth, hasAuth := e.authorizations[header.RequestId.String()]
	var t *treasury
	if hasAuth {
		t = e.treasuries[auth.treasuryID.String()]
	}
	e.mu.RUnlock()

	if !hasAuth {
		return buildResult(a, df, nil, 0, fmt.Errorf("request %s was never authorized", header.RequestId))
	}
	if t == nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("treasury %s has no key", auth.treasuryID))
	}
	if auth.memoRef != header.MemoRef {
		return buildResult(a, df, nil, 0, fmt.Errorf("memo reference does not match the authorization"))
	}
	if header.TreasuryId.Cmp(auth.treasuryID) != 0 {
		return buildResult(a, df, nil, 0, fmt.Errorf("treasury does not match the authorization"))
	}

	destID, err := xrpl.DecodeClassicAddress(auth.destination)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("authorized destination is invalid: %w", err))
	}

	sequence, err := e.ledger.AccountSequence(t.address)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("reading account sequence: %w", err))
	}
	currentLedger, err := e.ledger.CurrentLedger()
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("reading current ledger: %w", err))
	}

	payment := &xrpl.Payment{
		Account:            t.accountID,
		Destination:        destID,
		AmountDrops:        auth.amountDrops,
		FeeDrops:           config.DefaultFeeDrops,
		Sequence:           sequence,
		LastLedgerSequence: lastLedgerFor(currentLedger, auth.expiresAt),
		DestinationTag:     auth.tag,
		HasDestinationTag:  auth.hasTag,
		Memo:               memoBytes(auth.memoRef),
		SigningPubKey:      t.key.PubKey().SerializeCompressed(),
	}

	signed, err := payment.Sign(t.key)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("signing payment: %w", err))
	}

	txID, err := hashFromHex(signed.TxID)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("parsing transaction id: %w", err))
	}
	blob, err := bytesFromHex(signed.TxBlob)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("parsing signed blob: %w", err))
	}

	// The authorization is consumed: one authorization yields one signature.
	e.mu.Lock()
	delete(e.authorizations, header.RequestId.String())
	e.mu.Unlock()

	data, err := codec.EncodeSignedPayment(
		header.ChainId, header.Controller, header.RequestId, header.MemoRef, txID, blob,
	)
	if err != nil {
		return buildResult(a, df, nil, 0, fmt.Errorf("encoding result: %w", err))
	}
	return buildResult(a, df, data, 1, nil)
}

// -----------------------------------------------------------------------
// Validation helpers
// -----------------------------------------------------------------------

// checkPolicy enforces the per-payment cap inside the enclave.
//
// The cumulative cap is enforced on chain rather than here: the contract holds
// the authoritative reserved total, and duplicating that counter in enclave
// memory would let the two drift after a restart. The enclave's job is to refuse
// an oversized single payment before a signature can exist.
func checkPolicy(p types.Policy, amount *big.Int) error {
	if amount.Sign() <= 0 {
		return fmt.Errorf("amount must be positive")
	}
	if p.MaxPerPaymentDrops == nil || p.MaxPerPaymentDrops.Sign() <= 0 {
		return fmt.Errorf("policy has no per-payment cap")
	}
	if amount.Cmp(p.MaxPerPaymentDrops) > 0 {
		return fmt.Errorf("amount %s drops exceeds the per-payment cap of %s drops",
			amount, p.MaxPerPaymentDrops)
	}
	if p.MaxTotalDrops != nil && amount.Cmp(p.MaxTotalDrops) > 0 {
		return fmt.Errorf("amount %s drops exceeds the cumulative cap of %s drops",
			amount, p.MaxTotalDrops)
	}
	return nil
}

// bindPayloadToHeader requires the decrypted instruction to describe the same
// request the contract dispatched.
func bindPayloadToHeader(pi types.PaymentInstruction, h types.InstructionHeader) error {
	if pi.ChainId == nil || pi.ChainId.Cmp(h.ChainId) != 0 {
		return fmt.Errorf("payload chain id does not match the instruction")
	}
	if pi.Controller != h.Controller {
		return fmt.Errorf("payload controller does not match the instruction")
	}
	if pi.TreasuryId == nil || pi.TreasuryId.Cmp(h.TreasuryId) != 0 {
		return fmt.Errorf("payload treasury does not match the instruction")
	}
	if pi.RequestId == nil || pi.RequestId.Cmp(h.RequestId) != 0 {
		return fmt.Errorf("payload request id does not match the instruction: this ciphertext belongs to another request")
	}
	return nil
}

// validateHeaderCommitment checks the contract's policy commitment against the
// policy it sent, so the enclave never caches rules the contract did not commit to.
func validateHeaderCommitment(h types.InstructionHeader, p types.Policy) error {
	if h.Controller == (common.Address{}) {
		return fmt.Errorf("instruction header has no controller address")
	}
	want, err := codec.PolicyCommitment(p)
	if err != nil {
		return fmt.Errorf("computing policy commitment: %w", err)
	}
	if want != h.PolicyCommitment {
		return fmt.Errorf("policy commitment does not match the policy in the instruction")
	}
	return nil
}

// lastLedgerFor converts a request deadline into an XRPL ledger bound.
//
// This is what makes expiry real. Flare can refuse to accept a late settlement,
// but only LastLedgerSequence stops the payment from landing on XRPL in the
// first place.
func lastLedgerFor(currentLedger uint32, expiresAt uint64) uint32 {
	remaining := int64(expiresAt) - int64(nowUnix())
	window := int64(config.MinLedgerWindow)
	if remaining > 0 {
		window = remaining / config.LedgerSecondsPerLedger
	}
	if window < int64(config.MinLedgerWindow) {
		window = int64(config.MinLedgerWindow)
	}
	if window > int64(config.MaxLedgerWindow) {
		window = int64(config.MaxLedgerWindow)
	}
	return currentLedger + uint32(window)
}

// memoBytes renders the memo the contract expects: "BSF1" || memoRef.
func memoBytes(memoRef [32]byte) []byte {
	out := make([]byte, 0, 36)
	out = append(out, 'B', 'S', 'F', '1')
	return append(out, memoRef[:]...)
}
