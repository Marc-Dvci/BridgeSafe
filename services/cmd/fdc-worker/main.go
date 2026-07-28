// Command fdc-worker turns a settled XRPL payment into an on-chain proof.
//
// It watches BridgeSafeController for PaymentBroadcast, then for each payment
// runs the Flare Data Connector round trip: prepare an XRPPayment attestation
// request, submit it to FdcHub with the required fee, wait for the voting round
// to finalise, fetch the Merkle proof from the Data Availability layer, and hand
// it to BridgeSafeFdcVerifier.
//
// The worker is unprivileged. It cannot settle anything by assertion — it can
// only deliver a proof, and the contract decides whether that proof matches the
// request. Anyone can run one; a request cannot be stranded by this process
// going away.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bridgesafe-services/internal/bridgesafe"
	"bridgesafe-services/internal/fdc"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func main() {
	var (
		rpcURL      = flag.String("rpc", env("CHAIN_URL", "https://coston2-api.flare.network/ext/C/rpc"), "Flare RPC endpoint")
		controller  = flag.String("controller", env("BRIDGESAFE_CONTROLLER", ""), "BridgeSafeController address")
		verifierAdr = flag.String("verifier", env("BRIDGESAFE_FDC_VERIFIER", ""), "BridgeSafeFdcVerifier address")
		fdcHub      = flag.String("fdc-hub", env("FDC_HUB", "0x48aC463d7975828989331F4De43341627b9c5f1D"), "FdcHub address")
		feeConfig   = flag.String("fee-config", env("FDC_FEE_CONFIG", "0x191a1282Ac700edE65c5B0AaF313BAcC3eA7fC7e"), "FdcRequestFeeConfigurations address")
		relay       = flag.String("relay", env("FLARE_RELAY", "0xa10B672D1c62e5457b17af63d4302add6A99d7dE"), "Relay address")
		verifierURL = flag.String("verifier-url", env("VERIFIER_URL_TESTNET", "https://fdc-verifiers-testnet.flare.network"), "FDC verifier service")
		daURL       = flag.String("da-url", env("COSTON2_DA_LAYER_URL", "https://ctn2-data-availability.flare.network"), "Data Availability layer")
		apiKey      = flag.String("api-key", env("VERIFIER_API_KEY_TESTNET", "00000000-0000-0000-0000-000000000000"), "verifier API key")
		keyHex      = flag.String("key", env("FDC_WORKER_PRIVATE_KEY", env("DEPLOYMENT_PRIVATE_KEY", "")), "Flare key for attestation and finalisation")
		fromBlock   = flag.Uint64("from", 0, "block to start scanning from (0 = current head)")
		poll        = flag.Duration("poll", 10*time.Second, "chain poll interval")
		once        = flag.Bool("once", false, "process the backlog and exit")
	)
	flag.Parse()

	for name, value := range map[string]string{
		"BRIDGESAFE_CONTROLLER":   *controller,
		"BRIDGESAFE_FDC_VERIFIER": *verifierAdr,
		"FDC_WORKER_PRIVATE_KEY":  *keyHex,
	} {
		if value == "" {
			log.Fatalf("missing %s", name)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chain, err := bridgesafe.Dial(ctx, *rpcURL, *keyHex)
	if err != nil {
		log.Fatalf("connecting to Flare: %v", err)
	}
	defer chain.Close()

	w := &worker{
		chain:      chain,
		controller: common.HexToAddress(*controller),
		verifier:   common.HexToAddress(*verifierAdr),
		fdcHub:     common.HexToAddress(*fdcHub),
		feeConfig:  common.HexToAddress(*feeConfig),
		relay:      common.HexToAddress(*relay),
		fdc:        fdc.New(*verifierURL, *daURL, *apiKey),
		done:       make(map[string]bool),
	}

	log.Printf("fdc-worker ready")
	log.Printf("  controller %s", w.controller.Hex())
	log.Printf("  verifier   %s", w.verifier.Hex())
	log.Printf("  submitting as %s", chain.From().Hex())

	if err := w.run(ctx, *fromBlock, *poll, *once); err != nil && ctx.Err() == nil {
		log.Fatalf("fdc-worker stopped: %v", err)
	}
}

type worker struct {
	chain      *bridgesafe.Chain
	controller common.Address
	verifier   common.Address
	fdcHub     common.Address
	feeConfig  common.Address
	relay      common.Address
	fdc        *fdc.Client

	done map[string]bool
}

func (w *worker) run(ctx context.Context, fromBlock uint64, poll time.Duration, once bool) error {
	topic := bridgesafe.ControllerABI.Events["PaymentBroadcast"].ID

	next := fromBlock
	if next == 0 {
		head, err := w.chain.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("reading head: %w", err)
		}
		if head > 500 {
			next = head - 500
		}
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		head, err := w.chain.BlockNumber(ctx)
		if err != nil {
			log.Printf("reading head: %v", err)
		} else if head >= next {
			logs, err := w.chain.FilterLogs(ctx, w.controller, topic,
				new(big.Int).SetUint64(next), new(big.Int).SetUint64(head))
			if err != nil {
				log.Printf("fetching logs %d-%d: %v", next, head, err)
			} else {
				for _, l := range logs {
					if err := w.settle(ctx, l); err != nil {
						log.Printf("request %s: %v", requestIDOf(l), err)
					}
				}
				next = head + 1
			}
		}

		if once {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (w *worker) settle(ctx context.Context, l types.Log) error {
	if len(l.Topics) < 2 {
		return fmt.Errorf("PaymentBroadcast log has no indexed requestId")
	}
	requestID := new(big.Int).SetBytes(l.Topics[1].Bytes())
	key := requestID.String()
	if w.done[key] {
		return nil
	}

	values, err := bridgesafe.ControllerABI.Events["PaymentBroadcast"].Inputs.NonIndexed().Unpack(l.Data)
	if err != nil {
		return fmt.Errorf("decoding PaymentBroadcast: %w", err)
	}
	txID, _ := values[0].([32]byte)
	txIDHex := fmt.Sprintf("%X", txID)

	// Skip anything already settled, so a restart does not redo finished work.
	if settled, err := w.alreadySettled(ctx, requestID); err != nil {
		log.Printf("request %s: could not read state (%v); attempting settlement anyway", key, err)
	} else if settled {
		w.done[key] = true
		return nil
	}

	log.Printf("request %s: requesting attestation for XRPL tx %s", key, txIDHex)

	// 1. Encode the request. The proof owner is the verifier contract, since that
	//    is the only address that will present the proof.
	requestBytes, err := w.fdc.PrepareXRPPayment(ctx, txIDHex, w.verifier.Hex())
	if err != nil {
		return fmt.Errorf("preparing attestation: %w", err)
	}

	// 2. Submit it to FdcHub with the configured fee.
	fee, err := w.requestFee(ctx, requestBytes)
	if err != nil {
		return fmt.Errorf("reading request fee: %w", err)
	}
	receipt, err := w.chain.Send(ctx, bridgesafe.FdcHubABI, w.fdcHub, fee, "requestAttestation", requestBytes)
	if err != nil {
		return fmt.Errorf("submitting attestation request: %w", err)
	}

	blockTime, err := w.chain.BlockTimestamp(ctx, receipt.BlockNumber.Uint64())
	if err != nil {
		return fmt.Errorf("reading block timestamp: %w", err)
	}
	round := fdc.VotingRoundFor(blockTime)
	log.Printf("request %s: attestation submitted in voting round %d (fee %s wei)", key, round, fee)

	// 3. Wait for the round to finalise. 90-180s is normal; this is the honest
	//    latency of the protocol, not a bug to be hidden.
	if err := w.waitForRound(ctx, round); err != nil {
		return err
	}

	// 4. Fetch the proof.
	proofCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	proof, err := w.fdc.WaitForProof(proofCtx, round, requestBytes, 10*time.Second)
	if err != nil {
		return fmt.Errorf("fetching proof: %w", err)
	}
	log.Printf("request %s: proof retrieved (%d Merkle nodes)", key, len(proof.MerkleProof))

	// 5. Hand it to the verifier, which decides whether it matches the request.
	if _, err := w.chain.Send(ctx, bridgesafe.VerifierABI, w.verifier, nil,
		"finalizePayment", requestID, toContractProof(proof)); err != nil {
		return fmt.Errorf("finalising payment: %w", err)
	}

	w.done[key] = true
	log.Printf("request %s: SETTLED", key)
	return nil
}

func (w *worker) requestFee(ctx context.Context, requestBytes []byte) (*big.Int, error) {
	out, err := w.chain.Call(ctx, bridgesafe.FeeConfigABI, w.feeConfig, "getRequestFee", requestBytes)
	if err != nil {
		return nil, err
	}
	fee, ok := out[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("fee is %T, want *big.Int", out[0])
	}
	return fee, nil
}

func (w *worker) waitForRound(ctx context.Context, round uint64) error {
	deadline, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		out, err := w.chain.Call(deadline, bridgesafe.RelayABI, w.relay, "isFinalized",
			big.NewInt(bridgesafe.FdcProtocolID), new(big.Int).SetUint64(round))
		if err == nil {
			if final, ok := out[0].(bool); ok && final {
				return nil
			}
		}
		select {
		case <-deadline.Done():
			return fmt.Errorf("voting round %d was not finalised within 10 minutes", round)
		case <-ticker.C:
		}
	}
}

func (w *worker) alreadySettled(ctx context.Context, requestID *big.Int) (bool, error) {
	out, err := w.chain.Call(ctx, bridgesafe.ControllerABI, w.controller, "getRequest", requestID)
	if err != nil {
		return false, err
	}
	// The tuple decodes into an anonymous struct; reach the state field by name.
	type request struct {
		TreasuryId      *big.Int
		Requester       common.Address
		Nonce           uint64
		CreatedAt       uint64
		ExpiresAt       uint64
		PayloadHash     [32]byte
		MemoRef         [32]byte
		AmountDrops     *big.Int
		DestinationHash [32]byte
		ExpectedTxId    [32]byte
		SignedBlobHash  [32]byte
		XrplTxId        [32]byte
		State           uint8
	}
	var r request
	if err := bridgesafe.CopyStruct(out[0], &r); err != nil {
		return false, err
	}
	const stateSettled = 5
	return r.State == stateSettled, nil
}

// toContractProof converts the FDC client's proof into the tuple shape the
// verifier's ABI expects.
func toContractProof(p *fdc.Proof) any {
	type requestBody struct {
		TransactionId [32]byte
		ProofOwner    common.Address
	}
	type responseBody struct {
		BlockNumber                  uint64
		BlockTimestamp               uint64
		SourceAddress                string
		SourceAddressHash            [32]byte
		ReceivingAddressHash         [32]byte
		IntendedReceivingAddressHash [32]byte
		SpentAmount                  *big.Int
		IntendedSpentAmount          *big.Int
		ReceivedAmount               *big.Int
		IntendedReceivedAmount       *big.Int
		HasMemoData                  bool
		FirstMemoData                []byte
		HasDestinationTag            bool
		DestinationTag               *big.Int
		Status                       uint8
	}
	type response struct {
		AttestationType     [32]byte
		SourceId            [32]byte
		VotingRound         uint64
		LowestUsedTimestamp uint64
		RequestBody         requestBody
		ResponseBody        responseBody
	}
	type proof struct {
		MerkleProof [][32]byte
		Data        response
	}

	rb := p.Response.ResponseBody
	return proof{
		MerkleProof: p.MerkleProof,
		Data: response{
			AttestationType:     p.Response.AttestationType,
			SourceId:            p.Response.SourceID,
			VotingRound:         p.Response.VotingRound,
			LowestUsedTimestamp: p.Response.LowestUsedTimestamp,
			RequestBody: requestBody{
				TransactionId: p.Response.RequestBody.TransactionID,
				ProofOwner:    common.BytesToAddress(p.Response.RequestBody.ProofOwner[:]),
			},
			ResponseBody: responseBody{
				BlockNumber:                  rb.BlockNumber,
				BlockTimestamp:               rb.BlockTimestamp,
				SourceAddress:                rb.SourceAddress,
				SourceAddressHash:            rb.SourceAddressHash,
				ReceivingAddressHash:         rb.ReceivingAddressHash,
				IntendedReceivingAddressHash: rb.IntendedReceivingAddressHash,
				SpentAmount:                  rb.SpentAmount,
				IntendedSpentAmount:          rb.IntendedSpentAmount,
				ReceivedAmount:               rb.ReceivedAmount,
				IntendedReceivedAmount:       rb.IntendedReceivedAmount,
				HasMemoData:                  rb.HasMemoData,
				FirstMemoData:                rb.FirstMemoData,
				HasDestinationTag:            rb.HasDestinationTag,
				DestinationTag:               rb.DestinationTag,
				Status:                       rb.Status,
			},
		},
	}
}

func requestIDOf(l types.Log) string {
	if len(l.Topics) < 2 {
		return "?"
	}
	return new(big.Int).SetBytes(l.Topics[1].Bytes()).String()
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
