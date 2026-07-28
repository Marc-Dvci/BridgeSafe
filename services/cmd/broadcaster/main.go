// Command broadcaster puts enclave-signed XRPL payments on the ledger.
//
// It watches BridgeSafeController for PaymentSigned, submits the signed blob the
// event carries, and reports the resulting transaction id back on chain.
//
// What it deliberately cannot do: create a treasury, open a request, authorize a
// payment, or alter one. It holds no XRPL key. Its Flare key can call exactly one
// controller method, reportBroadcast, and that method only accepts the
// transaction id the enclave already predicted. Compromising this service delays
// payments; it does not redirect them.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"bridgesafe-services/internal/bridgesafe"
	"bridgesafe-services/internal/xrplsubmit"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func main() {
	var (
		rpcURL     = flag.String("rpc", env("CHAIN_URL", "https://coston2-api.flare.network/ext/C/rpc"), "Flare RPC endpoint")
		controller = flag.String("controller", env("BRIDGESAFE_CONTROLLER", ""), "BridgeSafeController address")
		xrplURL    = flag.String("xrpl", env("XRPL_RPC_URL", "https://s.altnet.rippletest.net:51234/"), "XRPL JSON-RPC endpoint")
		keyHex     = flag.String("key", env("BROADCASTER_PRIVATE_KEY", env("DEPLOYMENT_PRIVATE_KEY", "")), "Flare key used only for reportBroadcast")
		fromBlock  = flag.Uint64("from", 0, "block to start scanning from (0 = current head)")
		poll       = flag.Duration("poll", 5*time.Second, "chain poll interval")
		once       = flag.Bool("once", false, "process the backlog and exit")
	)
	flag.Parse()

	if *controller == "" {
		log.Fatal("no controller address: set BRIDGESAFE_CONTROLLER or pass -controller")
	}
	if *keyHex == "" {
		log.Fatal("no signing key: set BROADCASTER_PRIVATE_KEY")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chain, err := bridgesafe.Dial(ctx, *rpcURL, *keyHex)
	if err != nil {
		log.Fatalf("connecting to Flare: %v", err)
	}
	defer chain.Close()

	b := &broadcaster{
		chain:      chain,
		controller: common.HexToAddress(*controller),
		xrpl:       xrplsubmit.New(*xrplURL),
		seen:       make(map[string]bool),
	}

	log.Printf("broadcaster ready")
	log.Printf("  controller %s", b.controller.Hex())
	log.Printf("  reporting as %s", chain.From().Hex())
	log.Printf("  xrpl %s", *xrplURL)

	if err := b.run(ctx, *fromBlock, *poll, *once); err != nil && ctx.Err() == nil {
		log.Fatalf("broadcaster stopped: %v", err)
	}
}

type broadcaster struct {
	chain      *bridgesafe.Chain
	controller common.Address
	xrpl       *xrplsubmit.Client

	// seen suppresses duplicate work within a single run. It is an optimisation,
	// not a safety mechanism: correctness comes from XRPL applying a sequence
	// number once and from reportBroadcast refusing a second call, both of which
	// survive a restart that empties this map.
	seen map[string]bool
}

func (b *broadcaster) run(ctx context.Context, fromBlock uint64, poll time.Duration, once bool) error {
	topic := bridgesafe.ControllerABI.Events["PaymentSigned"].ID

	next := fromBlock
	if next == 0 {
		head, err := b.chain.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("reading head: %w", err)
		}
		// Look a little way back so a payment signed moments before startup is
		// still picked up.
		if head > 100 {
			next = head - 100
		}
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		head, err := b.chain.BlockNumber(ctx)
		if err != nil {
			log.Printf("reading head: %v", err)
		} else if head >= next {
			logs, err := b.chain.FilterLogs(ctx, b.controller, topic,
				new(big.Int).SetUint64(next), new(big.Int).SetUint64(head))
			if err != nil {
				log.Printf("fetching logs %d-%d: %v", next, head, err)
			} else {
				for _, l := range logs {
					if err := b.handle(ctx, l); err != nil {
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

func (b *broadcaster) handle(ctx context.Context, l types.Log) error {
	event := bridgesafe.ControllerABI.Events["PaymentSigned"]

	values, err := event.Inputs.NonIndexed().Unpack(l.Data)
	if err != nil {
		return fmt.Errorf("decoding PaymentSigned: %w", err)
	}
	if len(values) != 3 {
		return fmt.Errorf("PaymentSigned had %d non-indexed fields, want 3", len(values))
	}
	expectedTxID, _ := values[0].([32]byte)
	blob, _ := values[2].([]byte)

	if len(l.Topics) < 2 {
		return fmt.Errorf("PaymentSigned log has no indexed requestId")
	}
	requestID := new(big.Int).SetBytes(l.Topics[1].Bytes())

	key := requestID.String()
	if b.seen[key] {
		return nil
	}

	blobHex := strings.ToUpper(fmt.Sprintf("%x", blob))
	log.Printf("request %s: submitting %d-byte payment, expecting tx %x", key, len(blob), expectedTxID)

	res, err := b.xrpl.Submit(ctx, blobHex)
	if err != nil {
		return fmt.Errorf("submitting to XRPL: %w", err)
	}
	if !res.Accepted {
		return fmt.Errorf("XRPL rejected the payment: %s (%s)", res.EngineResult, res.EngineResultMessage)
	}
	if res.AlreadySubmitted {
		log.Printf("request %s: already on the ledger (%s)", key, res.EngineResult)
	}

	// The ledger's id must be the one the enclave predicted. A mismatch would mean
	// the blob we broadcast is not the blob the contract recorded, so stop rather
	// than report something the contract will refuse anyway.
	if res.TxID != "" && !strings.EqualFold(res.TxID, fmt.Sprintf("%X", expectedTxID)) {
		return fmt.Errorf("XRPL returned tx %s but the enclave predicted %X", res.TxID, expectedTxID)
	}

	if _, err := b.chain.Send(ctx, bridgesafe.ControllerABI, b.controller, nil,
		"reportBroadcast", requestID, expectedTxID); err != nil {
		// A duplicate report is expected after a restart and is not a failure.
		if strings.Contains(err.Error(), "would revert") {
			log.Printf("request %s: already reported on chain", key)
			b.seen[key] = true
			return nil
		}
		return fmt.Errorf("reporting broadcast: %w", err)
	}

	b.seen[key] = true
	log.Printf("request %s: broadcast reported, tx %X", key, expectedTxID)
	return nil
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
