// Package codec translates between the enclave's Go types and the ABI encoding
// BridgeSafeController speaks.
//
// Every argument list here has a counterpart in the Solidity source, named in the
// comment above it. Getting one wrong does not fail loudly at build time — it
// produces a signed result the contract refuses to decode — so the round-trip
// tests in codec_test.go exist to keep the two definitions honest.
package codec

import (
	"fmt"
	"math/big"

	"bridgesafe-extension/pkg/types"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

func mustType(t string, components []abi.ArgumentMarshaling) abi.Type {
	ty, err := abi.NewType(t, "", components)
	if err != nil {
		panic(fmt.Sprintf("codec: bad ABI type %q: %v", t, err))
	}
	return ty
}

var (
	uint256T = mustType("uint256", nil)
	uint64T  = mustType("uint64", nil)
	uint32T  = mustType("uint32", nil)
	addressT = mustType("address", nil)
	bytes32T = mustType("bytes32", nil)
	bytesT   = mustType("bytes", nil)
	stringT  = mustType("string", nil)
	boolT    = mustType("bool", nil)

	headerT = mustType("tuple", []abi.ArgumentMarshaling{
		{Name: "chainId", Type: "uint256"},
		{Name: "controller", Type: "address"},
		{Name: "treasuryId", Type: "uint256"},
		{Name: "requestId", Type: "uint256"},
		{Name: "nonce", Type: "uint64"},
		{Name: "expiresAt", Type: "uint64"},
		{Name: "memoRef", Type: "bytes32"},
		{Name: "policyCommitment", Type: "bytes32"},
	})

	policyT = mustType("tuple", []abi.ArgumentMarshaling{
		{Name: "maxPerPaymentDrops", Type: "uint256"},
		{Name: "maxTotalDrops", Type: "uint256"},
		{Name: "requestTtlSeconds", Type: "uint64"},
	})
)

// --- Instruction messages (controller -> enclave) --------------------------

// argsHeaderAndPolicy matches `abi.encode(InstructionHeader, Policy)`, sent by
// createTreasury and requestPolicyUpdate.
var argsHeaderAndPolicy = abi.Arguments{{Type: headerT}, {Type: policyT}}

// argsHeaderAndBytes matches `abi.encode(InstructionHeader, bytes)`, sent by
// createPaymentRequest with the ECIES ciphertext.
var argsHeaderAndBytes = abi.Arguments{{Type: headerT}, {Type: bytesT}}

// argsHeaderOnly matches `abi.encode(InstructionHeader)`, sent by requestSignature.
var argsHeaderOnly = abi.Arguments{{Type: headerT}}

// DecodeHeaderAndPolicy unpacks a CREATE_TREASURY_KEY or REGISTER_POLICY message.
func DecodeHeaderAndPolicy(data []byte) (types.InstructionHeader, types.Policy, error) {
	var h types.InstructionHeader
	var p types.Policy

	vals, err := argsHeaderAndPolicy.Unpack(data)
	if err != nil {
		return h, p, fmt.Errorf("decoding (header, policy): %w", err)
	}
	if len(vals) != 2 {
		return h, p, fmt.Errorf("expected 2 values, got %d", len(vals))
	}
	if err := copyStruct(vals[0], &h); err != nil {
		return h, p, fmt.Errorf("header: %w", err)
	}
	if err := copyStruct(vals[1], &p); err != nil {
		return h, p, fmt.Errorf("policy: %w", err)
	}
	return h, p, nil
}

// DecodeHeaderAndPayload unpacks an AUTHORIZE_PAYMENT message.
func DecodeHeaderAndPayload(data []byte) (types.InstructionHeader, []byte, error) {
	var h types.InstructionHeader

	vals, err := argsHeaderAndBytes.Unpack(data)
	if err != nil {
		return h, nil, fmt.Errorf("decoding (header, bytes): %w", err)
	}
	if len(vals) != 2 {
		return h, nil, fmt.Errorf("expected 2 values, got %d", len(vals))
	}
	if err := copyStruct(vals[0], &h); err != nil {
		return h, nil, fmt.Errorf("header: %w", err)
	}
	payload, ok := vals[1].([]byte)
	if !ok {
		return h, nil, fmt.Errorf("payload is %T, want []byte", vals[1])
	}
	return h, payload, nil
}

// DecodeHeader unpacks a SIGN_XRPL_PAYMENT message.
func DecodeHeader(data []byte) (types.InstructionHeader, error) {
	var h types.InstructionHeader
	vals, err := argsHeaderOnly.Unpack(data)
	if err != nil {
		return h, fmt.Errorf("decoding (header): %w", err)
	}
	if len(vals) != 1 {
		return h, fmt.Errorf("expected 1 value, got %d", len(vals))
	}
	err = copyStruct(vals[0], &h)
	return h, err
}

// --- Encrypted payment payload (owner -> enclave) --------------------------

// argsPaymentInstruction matches the plaintext the treasury owner encrypts. The
// same tuple is built by the frontend in apps/web/lib/payload.ts.
var argsPaymentInstruction = abi.Arguments{
	{Type: uint256T}, // chainId
	{Type: addressT}, // controller
	{Type: uint256T}, // treasuryId
	{Type: uint256T}, // requestId
	{Type: stringT},  // destination
	{Type: uint256T}, // amountDrops
	{Type: uint32T},  // destinationTag
	{Type: boolT},    // hasDestinationTag
	{Type: stringT},  // reference
}

// DecodePaymentInstruction unpacks the decrypted payment payload.
func DecodePaymentInstruction(data []byte) (types.PaymentInstruction, error) {
	var pi types.PaymentInstruction
	vals, err := argsPaymentInstruction.Unpack(data)
	if err != nil {
		return pi, fmt.Errorf("decoding payment instruction: %w", err)
	}
	if len(vals) != 9 {
		return pi, fmt.Errorf("expected 9 fields, got %d", len(vals))
	}
	var ok bool
	if pi.ChainId, ok = vals[0].(*big.Int); !ok {
		return pi, fmt.Errorf("chainId is %T", vals[0])
	}
	if pi.Controller, ok = vals[1].(common.Address); !ok {
		return pi, fmt.Errorf("controller is %T", vals[1])
	}
	if pi.TreasuryId, ok = vals[2].(*big.Int); !ok {
		return pi, fmt.Errorf("treasuryId is %T", vals[2])
	}
	if pi.RequestId, ok = vals[3].(*big.Int); !ok {
		return pi, fmt.Errorf("requestId is %T", vals[3])
	}
	if pi.Destination, ok = vals[4].(string); !ok {
		return pi, fmt.Errorf("destination is %T", vals[4])
	}
	if pi.AmountDrops, ok = vals[5].(*big.Int); !ok {
		return pi, fmt.Errorf("amountDrops is %T", vals[5])
	}
	if pi.DestinationTag, ok = vals[6].(uint32); !ok {
		return pi, fmt.Errorf("destinationTag is %T", vals[6])
	}
	if pi.HasDestinationTag, ok = vals[7].(bool); !ok {
		return pi, fmt.Errorf("hasDestinationTag is %T", vals[7])
	}
	if pi.Reference, ok = vals[8].(string); !ok {
		return pi, fmt.Errorf("reference is %T", vals[8])
	}
	return pi, nil
}

// EncodePaymentInstruction is the inverse, used by tests and tooling.
func EncodePaymentInstruction(pi types.PaymentInstruction) ([]byte, error) {
	return argsPaymentInstruction.Pack(
		pi.ChainId, pi.Controller, pi.TreasuryId, pi.RequestId,
		pi.Destination, pi.AmountDrops, pi.DestinationTag, pi.HasDestinationTag, pi.Reference,
	)
}

// --- Results (enclave -> controller) ---------------------------------------

// argsBindTreasury matches BridgeSafeController.bindTreasuryAddress.
var argsBindTreasury = abi.Arguments{
	{Type: uint256T}, {Type: addressT}, {Type: uint256T}, {Type: stringT}, {Type: bytes32T},
}

// argsConfirmPolicy matches BridgeSafeController.confirmPolicy.
var argsConfirmPolicy = abi.Arguments{
	{Type: uint256T}, {Type: addressT}, {Type: uint256T}, {Type: policyT}, {Type: bytes32T},
}

// argsAuthorization matches BridgeSafeController.submitAuthorization.
var argsAuthorization = abi.Arguments{
	{Type: uint256T}, {Type: addressT}, {Type: uint256T}, {Type: bytes32T},
	{Type: uint256T}, {Type: bytes32T}, {Type: bytes32T},
}

// argsSignedPayment matches BridgeSafeController.submitSignedPayment.
var argsSignedPayment = abi.Arguments{
	{Type: uint256T}, {Type: addressT}, {Type: uint256T}, {Type: bytes32T},
	{Type: bytes32T}, {Type: bytesT},
}

// argsFailure matches BridgeSafeController.submitFailure.
var argsFailure = abi.Arguments{
	{Type: uint256T}, {Type: addressT}, {Type: uint256T}, {Type: stringT},
}

// EncodeBindTreasury builds the result for bindTreasuryAddress.
func EncodeBindTreasury(chainID *big.Int, controller common.Address, treasuryID *big.Int, xrplAddress string, policyCommitment [32]byte) ([]byte, error) {
	return argsBindTreasury.Pack(chainID, controller, treasuryID, xrplAddress, policyCommitment)
}

// EncodeConfirmPolicy builds the result for confirmPolicy.
func EncodeConfirmPolicy(chainID *big.Int, controller common.Address, treasuryID *big.Int, p types.Policy, commitment [32]byte) ([]byte, error) {
	return argsConfirmPolicy.Pack(chainID, controller, treasuryID, p, commitment)
}

// EncodeAuthorization builds the result for submitAuthorization.
func EncodeAuthorization(chainID *big.Int, controller common.Address, requestID *big.Int, memoRef [32]byte, amountDrops *big.Int, destinationHash, payloadHash [32]byte) ([]byte, error) {
	return argsAuthorization.Pack(chainID, controller, requestID, memoRef, amountDrops, destinationHash, payloadHash)
}

// EncodeSignedPayment builds the result for submitSignedPayment.
func EncodeSignedPayment(chainID *big.Int, controller common.Address, requestID *big.Int, memoRef, expectedTxID [32]byte, signedTxBlob []byte) ([]byte, error) {
	return argsSignedPayment.Pack(chainID, controller, requestID, memoRef, expectedTxID, signedTxBlob)
}

// EncodeFailure builds the result for submitFailure.
func EncodeFailure(chainID *big.Int, controller common.Address, requestID *big.Int, reason string) ([]byte, error) {
	return argsFailure.Pack(chainID, controller, requestID, reason)
}

// PolicyCommitment reproduces BridgeSafeController.policyCommitment:
// `keccak256(abi.encode(policy))`.
func PolicyCommitment(p types.Policy) ([32]byte, error) {
	packed, err := abi.Arguments{{Type: policyT}}.Pack(p)
	if err != nil {
		return [32]byte{}, fmt.Errorf("packing policy: %w", err)
	}
	return Keccak256Hash(packed), nil
}

// --- Encoders for instruction messages -------------------------------------
//
// The contract builds these in production. They exist here so tests and the
// local dev harness can drive the enclave through the exact wire format the
// contract emits, rather than a hand-rolled approximation of it.

// EncodeHeaderAndPolicy builds a CREATE_TREASURY_KEY or REGISTER_POLICY message.
func EncodeHeaderAndPolicy(h types.InstructionHeader, p types.Policy) ([]byte, error) {
	return argsHeaderAndPolicy.Pack(h, p)
}

// EncodeHeaderAndPayload builds an AUTHORIZE_PAYMENT message.
func EncodeHeaderAndPayload(h types.InstructionHeader, payload []byte) ([]byte, error) {
	return argsHeaderAndBytes.Pack(h, payload)
}

// EncodeHeader builds a SIGN_XRPL_PAYMENT message.
func EncodeHeader(h types.InstructionHeader) ([]byte, error) {
	return argsHeaderOnly.Pack(h)
}

// DecodeSignedPayment unpacks a submitSignedPayment result. Used by the relayer
// to recover the broadcastable blob, and by tests.
func DecodeSignedPayment(data []byte) (chainID *big.Int, controller common.Address, requestID *big.Int, memoRef, txID [32]byte, blob []byte, err error) {
	vals, e := argsSignedPayment.Unpack(data)
	if e != nil {
		err = fmt.Errorf("decoding signed payment: %w", e)
		return
	}
	if len(vals) != 6 {
		err = fmt.Errorf("expected 6 fields, got %d", len(vals))
		return
	}
	chainID, _ = vals[0].(*big.Int)
	controller, _ = vals[1].(common.Address)
	requestID, _ = vals[2].(*big.Int)
	memoRef, _ = vals[3].([32]byte)
	txID, _ = vals[4].([32]byte)
	blob, _ = vals[5].([]byte)
	return
}

// DecodeAuthorization unpacks a submitAuthorization result.
func DecodeAuthorization(data []byte) (chainID *big.Int, controller common.Address, requestID *big.Int, memoRef [32]byte, amountDrops *big.Int, destinationHash, payloadHash [32]byte, err error) {
	vals, e := argsAuthorization.Unpack(data)
	if e != nil {
		err = fmt.Errorf("decoding authorization: %w", e)
		return
	}
	if len(vals) != 7 {
		err = fmt.Errorf("expected 7 fields, got %d", len(vals))
		return
	}
	chainID, _ = vals[0].(*big.Int)
	controller, _ = vals[1].(common.Address)
	requestID, _ = vals[2].(*big.Int)
	memoRef, _ = vals[3].([32]byte)
	amountDrops, _ = vals[4].(*big.Int)
	destinationHash, _ = vals[5].([32]byte)
	payloadHash, _ = vals[6].([32]byte)
	return
}
