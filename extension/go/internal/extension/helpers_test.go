package extension

import (
	"math/big"
	"testing"

	"bridgesafe-extension/internal/codec"
	"bridgesafe-extension/pkg/types"

	"github.com/ethereum/go-ethereum/common"
)

// Thin wrappers so the tests read as intent rather than plumbing.

func encodeHeaderPolicy(t *testing.T, h types.InstructionHeader, p types.Policy) []byte {
	t.Helper()
	b, err := codec.EncodeHeaderAndPolicy(h, p)
	if err != nil {
		t.Fatalf("encoding (header, policy): %v", err)
	}
	return b
}

func encodeHeaderPayload(t *testing.T, h types.InstructionHeader, payload []byte) []byte {
	t.Helper()
	b, err := codec.EncodeHeaderAndPayload(h, payload)
	if err != nil {
		t.Fatalf("encoding (header, payload): %v", err)
	}
	return b
}

func encodeHeaderOnly(t *testing.T, h types.InstructionHeader) []byte {
	t.Helper()
	b, err := codec.EncodeHeader(h)
	if err != nil {
		t.Fatalf("encoding (header): %v", err)
	}
	return b
}

// encodePaymentInstruction builds the plaintext the treasury owner would seal to
// the enclave, bound to the request in the header.
func encodePaymentInstruction(t *testing.T, h types.InstructionHeader, destination string, drops int64) []byte {
	t.Helper()
	b, err := codec.EncodePaymentInstruction(types.PaymentInstruction{
		ChainId:     h.ChainId,
		Controller:  h.Controller,
		TreasuryId:  h.TreasuryId,
		RequestId:   h.RequestId,
		Destination: destination,
		AmountDrops: big.NewInt(drops),
		Reference:   "contractor invoice",
	})
	if err != nil {
		t.Fatalf("encoding payment instruction: %v", err)
	}
	return b
}

func decodeSignedPayment(t *testing.T, data []byte) (*big.Int, common.Address, *big.Int, [32]byte, [32]byte, []byte) {
	t.Helper()
	chainID, controller, requestID, memoRef, txID, blob, err := codec.DecodeSignedPayment(data)
	if err != nil {
		t.Fatalf("decoding signed payment: %v", err)
	}
	return chainID, controller, requestID, memoRef, txID, blob
}
