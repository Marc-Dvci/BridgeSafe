// Command result-relay carries enclave decisions back to Flare.
//
// The contract dispatches an instruction and the enclave answers asynchronously,
// leaving a signed result on the extension proxy. Something has to collect that
// result and put it on chain, or a request sits in CREATED forever. That is this
// service, and it is the hinge the rest of the pipeline turns on:
//
//	TreasuryCreated        → bindTreasuryAddress
//	PolicyUpdateRequested  → confirmPolicy
//	PaymentRequested       → submitAuthorization
//	SignatureRequested     → submitSignedPayment
//
// From there broadcaster takes over on PaymentSigned, and fdc-worker on
// PaymentBroadcast.
//
// It is unprivileged in the way that matters. Every method it calls verifies the
// enclave's signature over the result before changing anything, so this service
// cannot invent an authorization, alter an amount, or redirect a payment — it can
// only deliver a decision the enclave already signed, or fail to. Losing it
// delays payments; anyone can run another one and the backlog clears.
//
// A refusal is delivered too. When the enclave declines an instruction it still
// signs the refusal, and submitFailure records it so the request fails cleanly
// and releases its reserved budget instead of expiring silently.
package main

import (
	"context"
	"errors"
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
	"bridgesafe-services/internal/teeproxy"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// handler maps one controller event to the method that consumes its result.
type handler struct {
	// event is the controller event that dispatched the instruction.
	event string
	// method is called with the enclave's result when the enclave succeeded.
	method string
	// subject names the thing in logs: a treasury id or a request id.
	subject string
}

// handlers is the whole routing table. Adding an enclave command means adding a
// row here and nothing else.
var handlers = []handler{
	{event: "TreasuryCreated", method: "bindTreasuryAddress", subject: "treasury"},
	{event: "PolicyUpdateRequested", method: "confirmPolicy", subject: "treasury"},
	{event: "PaymentRequested", method: "submitAuthorization", subject: "request"},
	{event: "SignatureRequested", method: "submitSignedPayment", subject: "request"},
}

func main() {
	var (
		rpcURL     = flag.String("rpc", env("CHAIN_URL", "https://coston2-api.flare.network/ext/C/rpc"), "Flare RPC endpoint")
		controller = flag.String("controller", env("BRIDGESAFE_CONTROLLER", ""), "BridgeSafeController address")
		proxyURL   = flag.String("proxy", env("EXT_PROXY_URL", ""), "extension proxy base URL")
		keyHex     = flag.String("key", env("RELAY_PRIVATE_KEY", env("DEPLOYMENT_PRIVATE_KEY", "")), "Flare key used only to deliver signed results")
		fromBlock  = flag.Uint64("from", 0, "block to start scanning from (0 = current head)")
		poll       = flag.Duration("poll", 5*time.Second, "chain poll interval")
		patience   = flag.Duration("patience", 5*time.Minute, "how long to keep retrying one action result")
		once       = flag.Bool("once", false, "process the backlog and exit")
	)
	flag.Parse()

	if *controller == "" {
		log.Fatal("no controller address: set BRIDGESAFE_CONTROLLER or pass -controller")
	}
	if *proxyURL == "" {
		log.Fatal("no extension proxy URL: set EXT_PROXY_URL (scripts/tunnel.sh writes it) or pass -proxy")
	}
	if *keyHex == "" {
		log.Fatal("no signing key: set RELAY_PRIVATE_KEY")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	chain, err := bridgesafe.Dial(ctx, *rpcURL, *keyHex)
	if err != nil {
		log.Fatalf("connecting to Flare: %v", err)
	}
	defer chain.Close()

	proxy := teeproxy.New(*proxyURL)

	r := &relay{
		chain:      chain,
		controller: common.HexToAddress(*controller),
		proxy:      proxy,
		patience:   *patience,
		done:       make(map[common.Hash]bool),
		firstSeen:  make(map[common.Hash]time.Time),
	}

	log.Printf("result-relay ready")
	log.Printf("  controller %s", r.controller.Hex())
	log.Printf("  proxy      %s", *proxyURL)
	log.Printf("  submitting as %s", chain.From().Hex())

	// Confirm the proxy answers before waiting on events, so a bad tunnel URL is
	// reported now rather than as silence later.
	if info, err := proxy.Info(ctx); err != nil {
		log.Printf("  warning: extension proxy not reachable yet: %v", err)
	} else if md, ok := info["machineData"].(map[string]any); ok {
		log.Printf("  enclave    %v", md["address"])
	}

	if err := r.run(ctx, *fromBlock, *poll, *once); err != nil && ctx.Err() == nil {
		log.Fatalf("result-relay stopped: %v", err)
	}
}

type relay struct {
	chain      *bridgesafe.Chain
	controller common.Address
	proxy      *teeproxy.Client
	patience   time.Duration

	// done marks action ids already delivered, to avoid re-fetching them every
	// tick. It is an optimisation: the contract's state machine is what actually
	// prevents a result being applied twice, and that survives a restart.
	done map[common.Hash]bool
	// firstSeen bounds how long a single action is retried before being given up
	// on, so one stuck instruction cannot pin the loop forever.
	firstSeen map[common.Hash]time.Time
}

func (r *relay) run(ctx context.Context, fromBlock uint64, poll time.Duration, once bool) error {
	topics := make([]common.Hash, 0, len(handlers))
	byTopic := make(map[common.Hash]handler, len(handlers))
	for _, h := range handlers {
		ev, ok := bridgesafe.ControllerABI.Events[h.event]
		if !ok {
			return fmt.Errorf("controller ABI has no event %s", h.event)
		}
		topics = append(topics, ev.ID)
		byTopic[ev.ID] = h
	}

	next := fromBlock
	if next == 0 {
		head, err := r.chain.BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("reading head: %w", err)
		}
		// Look back far enough to pick up an instruction dispatched moments before
		// startup, which is the normal case when the operator starts the services
		// in sequence.
		if head > 200 {
			next = head - 200
		}
	}

	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		head, err := r.chain.BlockNumber(ctx)
		if err != nil {
			log.Printf("reading head: %v", err)
		} else if head >= next {
			logs, err := r.chain.FilterLogsAny(ctx, r.controller, topics,
				new(big.Int).SetUint64(next), new(big.Int).SetUint64(head))
			if err != nil {
				log.Printf("fetching logs %d-%d: %v", next, head, err)
			} else {
				pending := false
				for _, l := range logs {
					h, ok := byTopic[l.Topics[0]]
					if !ok {
						continue
					}
					switch err := r.handle(ctx, h, l); {
					case err == nil:
					case errors.Is(err, teeproxy.ErrNotReady):
						// Normal: the enclave is still working. Re-scan this range
						// next tick rather than advancing past it.
						pending = true
					default:
						log.Printf("%s: %v", h.event, err)
					}
				}
				if !pending {
					next = head + 1
				}
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

func (r *relay) handle(ctx context.Context, h handler, l types.Log) error {
	ev := bridgesafe.ControllerABI.Events[h.event]

	values, err := ev.Inputs.NonIndexed().Unpack(l.Data)
	if err != nil {
		return fmt.Errorf("decoding %s: %w", h.event, err)
	}
	// instructionId is the last non-indexed field of every dispatching event.
	if len(values) == 0 {
		return fmt.Errorf("%s carried no non-indexed fields", h.event)
	}
	raw, ok := values[len(values)-1].([32]byte)
	if !ok {
		return fmt.Errorf("%s: last field is not the instruction id", h.event)
	}
	actionID := common.Hash(raw)

	if r.done[actionID] {
		return nil
	}

	subject := "?"
	if len(l.Topics) > 1 {
		subject = new(big.Int).SetBytes(l.Topics[1].Bytes()).String()
	}
	label := fmt.Sprintf("%s %s (action %s)", h.subject, subject, short(actionID))

	if _, seen := r.firstSeen[actionID]; !seen {
		r.firstSeen[actionID] = time.Now()
	}

	res, err := r.proxy.Result(ctx, actionID)
	if err != nil {
		if errors.Is(err, teeproxy.ErrNotReady) {
			if time.Since(r.firstSeen[actionID]) > r.patience {
				r.done[actionID] = true
				return fmt.Errorf("%s: no result after %s, giving up on it", label, r.patience)
			}
			return err
		}
		return fmt.Errorf("%s: %w", label, err)
	}

	method := h.method
	if !res.Succeeded() {
		// The enclave refused, and signed that refusal. Record it so the request
		// fails cleanly and its reserved budget is released.
		method = "submitFailure"
		log.Printf("%s: enclave declined — %s", label, strings.TrimSpace(res.Result.Log))
	}

	receipt, err := r.chain.Send(ctx, bridgesafe.ControllerABI, r.controller, nil,
		method,
		[]byte(res.Result.Data),
		[32]byte(res.Result.ID),
		res.Result.SubmissionTag,
		res.Result.Status,
		[]byte(res.Signature),
	)
	if err != nil {
		// The contract's state machine rejects a result that has already been
		// applied, which is exactly what happens after a restart. Treat that as
		// done rather than as an error worth shouting about.
		if isAlreadyApplied(err) {
			r.done[actionID] = true
			log.Printf("%s: already on chain", label)
			return nil
		}
		return fmt.Errorf("%s: calling %s: %w", label, method, err)
	}

	r.done[actionID] = true
	log.Printf("%s: %s accepted (tx %s)", label, method, short(receipt.TxHash))
	return nil
}

// isAlreadyApplied recognises the reverts that mean "this result is not new".
func isAlreadyApplied(err error) bool {
	s := err.Error()
	for _, marker := range []string{
		"would revert",
		"WrongState",
		"TreasuryAlreadyBound",
		"NoPendingPolicy",
		"ResultBindingMismatch",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func short(h common.Hash) string {
	s := h.Hex()
	if len(s) < 14 {
		return s
	}
	return s[:10] + "…" + s[len(s)-4:]
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
