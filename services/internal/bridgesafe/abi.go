// Package bridgesafe wraps the on-chain surface the relayer services need.
//
// The ABI is written out by hand rather than generated. The services touch five
// methods and three events out of two contracts, and a hand-written fragment
// keeps that surface visible — anyone reading this file can see the entire set
// of chain interactions the relayer is capable of, which matters for a component
// that is deliberately unprivileged.
package bridgesafe

import (
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

// controllerABI covers only what the relayer services use on BridgeSafeController.
//
// Note what is absent: nothing here can create a treasury, open a request, or
// choose a policy. The result-submitting methods below look powerful but are not —
// each one verifies an enclave signature over its payload before it changes
// anything, so a caller can only deliver a decision the enclave already made. The
// services carry results; they do not author them.
const controllerABI = `[
  {"type":"function","name":"reportBroadcast","stateMutability":"nonpayable",
   "inputs":[{"name":"_requestId","type":"uint256"},{"name":"_xrplTxId","type":"bytes32"}],
   "outputs":[]},
  {"type":"function","name":"getRequest","stateMutability":"view",
   "inputs":[{"name":"_requestId","type":"uint256"}],
   "outputs":[{"name":"","type":"tuple","components":[
     {"name":"treasuryId","type":"uint256"},
     {"name":"requester","type":"address"},
     {"name":"nonce","type":"uint64"},
     {"name":"createdAt","type":"uint64"},
     {"name":"expiresAt","type":"uint64"},
     {"name":"payloadHash","type":"bytes32"},
     {"name":"memoRef","type":"bytes32"},
     {"name":"amountDrops","type":"uint256"},
     {"name":"destinationHash","type":"bytes32"},
     {"name":"expectedTxId","type":"bytes32"},
     {"name":"signedBlobHash","type":"bytes32"},
     {"name":"xrplTxId","type":"bytes32"},
     {"name":"state","type":"uint8"}]}]},
  {"type":"event","name":"PaymentSigned","anonymous":false,
   "inputs":[
     {"name":"requestId","type":"uint256","indexed":true},
     {"name":"expectedTxId","type":"bytes32","indexed":false},
     {"name":"signedBlobHash","type":"bytes32","indexed":false},
     {"name":"signedTxBlob","type":"bytes","indexed":false}]},
  {"type":"event","name":"PaymentBroadcast","anonymous":false,
   "inputs":[
     {"name":"requestId","type":"uint256","indexed":true},
     {"name":"xrplTxId","type":"bytes32","indexed":false}]},
  {"type":"event","name":"PaymentSettled","anonymous":false,
   "inputs":[
     {"name":"requestId","type":"uint256","indexed":true},
     {"name":"xrplTxId","type":"bytes32","indexed":false},
     {"name":"amountDrops","type":"uint256","indexed":false}]},

  {"type":"event","name":"TreasuryCreated","anonymous":false,
   "inputs":[
     {"name":"treasuryId","type":"uint256","indexed":true},
     {"name":"owner","type":"address","indexed":true},
     {"name":"policyCommitment","type":"bytes32","indexed":false},
     {"name":"instructionId","type":"bytes32","indexed":false}]},
  {"type":"event","name":"PolicyUpdateRequested","anonymous":false,
   "inputs":[
     {"name":"treasuryId","type":"uint256","indexed":true},
     {"name":"policyCommitment","type":"bytes32","indexed":false},
     {"name":"instructionId","type":"bytes32","indexed":false}]},
  {"type":"event","name":"PaymentRequested","anonymous":false,
   "inputs":[
     {"name":"requestId","type":"uint256","indexed":true},
     {"name":"treasuryId","type":"uint256","indexed":true},
     {"name":"requester","type":"address","indexed":true},
     {"name":"nonce","type":"uint64","indexed":false},
     {"name":"memoRef","type":"bytes32","indexed":false},
     {"name":"expiresAt","type":"uint64","indexed":false},
     {"name":"payloadHash","type":"bytes32","indexed":false},
     {"name":"instructionId","type":"bytes32","indexed":false}]},
  {"type":"event","name":"SignatureRequested","anonymous":false,
   "inputs":[
     {"name":"requestId","type":"uint256","indexed":true},
     {"name":"instructionId","type":"bytes32","indexed":false}]},

  {"type":"function","name":"bindTreasuryAddress","stateMutability":"nonpayable",
   "inputs":[
     {"name":"_resultData","type":"bytes"},
     {"name":"_actionId","type":"bytes32"},
     {"name":"_submissionTag","type":"string"},
     {"name":"_status","type":"uint8"},
     {"name":"_signature","type":"bytes"}],
   "outputs":[]},
  {"type":"function","name":"confirmPolicy","stateMutability":"nonpayable",
   "inputs":[
     {"name":"_resultData","type":"bytes"},
     {"name":"_actionId","type":"bytes32"},
     {"name":"_submissionTag","type":"string"},
     {"name":"_status","type":"uint8"},
     {"name":"_signature","type":"bytes"}],
   "outputs":[]},
  {"type":"function","name":"submitAuthorization","stateMutability":"nonpayable",
   "inputs":[
     {"name":"_resultData","type":"bytes"},
     {"name":"_actionId","type":"bytes32"},
     {"name":"_submissionTag","type":"string"},
     {"name":"_status","type":"uint8"},
     {"name":"_signature","type":"bytes"}],
   "outputs":[]},
  {"type":"function","name":"submitSignedPayment","stateMutability":"nonpayable",
   "inputs":[
     {"name":"_resultData","type":"bytes"},
     {"name":"_actionId","type":"bytes32"},
     {"name":"_submissionTag","type":"string"},
     {"name":"_status","type":"uint8"},
     {"name":"_signature","type":"bytes"}],
   "outputs":[]},
  {"type":"function","name":"submitFailure","stateMutability":"nonpayable",
   "inputs":[
     {"name":"_resultData","type":"bytes"},
     {"name":"_actionId","type":"bytes32"},
     {"name":"_submissionTag","type":"string"},
     {"name":"_status","type":"uint8"},
     {"name":"_signature","type":"bytes"}],
   "outputs":[]}
]`

// verifierABI covers only finalizePayment on BridgeSafeFdcVerifier.
const verifierABI = `[
  {"type":"function","name":"finalizePayment","stateMutability":"nonpayable",
   "inputs":[
     {"name":"_requestId","type":"uint256"},
     {"name":"_proof","type":"tuple","components":[
       {"name":"merkleProof","type":"bytes32[]"},
       {"name":"data","type":"tuple","components":[
         {"name":"attestationType","type":"bytes32"},
         {"name":"sourceId","type":"bytes32"},
         {"name":"votingRound","type":"uint64"},
         {"name":"lowestUsedTimestamp","type":"uint64"},
         {"name":"requestBody","type":"tuple","components":[
           {"name":"transactionId","type":"bytes32"},
           {"name":"proofOwner","type":"address"}]},
         {"name":"responseBody","type":"tuple","components":[
           {"name":"blockNumber","type":"uint64"},
           {"name":"blockTimestamp","type":"uint64"},
           {"name":"sourceAddress","type":"string"},
           {"name":"sourceAddressHash","type":"bytes32"},
           {"name":"receivingAddressHash","type":"bytes32"},
           {"name":"intendedReceivingAddressHash","type":"bytes32"},
           {"name":"spentAmount","type":"int256"},
           {"name":"intendedSpentAmount","type":"int256"},
           {"name":"receivedAmount","type":"int256"},
           {"name":"intendedReceivedAmount","type":"int256"},
           {"name":"hasMemoData","type":"bool"},
           {"name":"firstMemoData","type":"bytes"},
           {"name":"hasDestinationTag","type":"bool"},
           {"name":"destinationTag","type":"uint256"},
           {"name":"status","type":"uint8"}]}]}]}],
   "outputs":[]}
]`

// fdcHubABI covers requestAttestation on Flare's FdcHub.
const fdcHubABI = `[
  {"type":"function","name":"requestAttestation","stateMutability":"payable",
   "inputs":[{"name":"_data","type":"bytes"}],"outputs":[]}
]`

// feeConfigABI covers the per-type request fee lookup.
const feeConfigABI = `[
  {"type":"function","name":"getRequestFee","stateMutability":"view",
   "inputs":[{"name":"_data","type":"bytes"}],
   "outputs":[{"name":"","type":"uint256"}]}
]`

// relayABI covers the round-finalisation check.
const relayABI = `[
  {"type":"function","name":"isFinalized","stateMutability":"view",
   "inputs":[{"name":"_protocolId","type":"uint256"},{"name":"_votingRoundId","type":"uint256"}],
   "outputs":[{"name":"","type":"bool"}]}
]`

// Parsed ABIs, resolved once at startup.
var (
	ControllerABI = mustParse("controller", controllerABI)
	VerifierABI   = mustParse("verifier", verifierABI)
	FdcHubABI     = mustParse("fdcHub", fdcHubABI)
	FeeConfigABI  = mustParse("feeConfig", feeConfigABI)
	RelayABI      = mustParse("relay", relayABI)
)

func mustParse(name, raw string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(raw))
	if err != nil {
		panic(fmt.Sprintf("bridgesafe: %s ABI is malformed: %v", name, err))
	}
	return parsed
}

// FdcProtocolID is the Flare Systems Protocol id for the Data Connector, used
// when asking the Relay whether a voting round has been finalised.
const FdcProtocolID = 200
