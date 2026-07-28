// Package types holds the data the BridgeSafe extension exchanges with the
// BridgeSafeController contract, plus the state it exposes over GET /state.
package types

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
)

// InstructionHeader is prepended by the controller to every instruction.
//
// These fields are trustworthy without any further proof: the TeeExtensionRegistry
// only accepts instructions from the one address registered as the extension's
// InstructionSender, and that address is BridgeSafeController. Nothing else can
// put a header on the wire.
//
// Mirrors BridgeSafeController.InstructionHeader.
type InstructionHeader struct {
	ChainId          *big.Int
	Controller       common.Address
	TreasuryId       *big.Int
	RequestId        *big.Int
	Nonce            uint64
	ExpiresAt        uint64
	MemoRef          [32]byte
	PolicyCommitment [32]byte
}

// Policy is the spending rule set the enclave enforces.
//
// Mirrors BridgeSafeController.Policy.
type Policy struct {
	MaxPerPaymentDrops *big.Int
	MaxTotalDrops      *big.Int
	RequestTtlSeconds  uint64
}

// PaymentInstruction is the plaintext the treasury owner ECIES-encrypts to the
// enclave. It never appears on chain in the clear.
//
// Every routing field is repeated here even though the header already carries it.
// That redundancy is the point: the enclave requires the decrypted payload to
// name the same chain, controller, treasury and request as the header, so a
// ciphertext captured from one request cannot be replayed into another.
type PaymentInstruction struct {
	ChainId           *big.Int
	Controller        common.Address
	TreasuryId        *big.Int
	RequestId         *big.Int
	Destination       string // XRPL classic r-address
	AmountDrops       *big.Int
	DestinationTag    uint32
	HasDestinationTag bool
	Reference         string // free-text note; stays inside the enclave
}

// State is what GET /state reports. It deliberately exposes no key material and
// no payment terms — only enough to tell whether the enclave is provisioned.
type State struct {
	// Treasuries the enclave currently holds a key for.
	TreasuryCount int `json:"treasuryCount"`
	// XRPL addresses of those treasuries. Public information: they appear on the
	// ledger the moment the treasury is funded.
	Addresses map[string]string `json:"addresses"`
	// Requests the enclave has authorized and not yet signed.
	PendingAuthorizations int `json:"pendingAuthorizations"`
}

// --- DO NOT MODIFY below this line. ---

// StateResponse is the envelope returned by GET /state.
type StateResponse struct {
	StateVersion common.Hash `json:"stateVersion"`
	State        State       `json:"state"`
}
