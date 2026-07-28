package main

import (
	"testing"

	"bridgesafe-services/internal/bridgesafe"
)

// The relay reads the instruction id as the *last non-indexed field* of each
// dispatching event. That is a convention, not something the compiler enforces:
// adding a field to one of these events in Solidity would leave this service
// quietly fetching the wrong action id and every payment would stall with no
// error anywhere. Pin it.
func TestEveryHandledEventEndsWithTheInstructionId(t *testing.T) {
	for _, h := range handlers {
		ev, ok := bridgesafe.ControllerABI.Events[h.event]
		if !ok {
			t.Fatalf("%s: not in the controller ABI", h.event)
		}

		nonIndexed := ev.Inputs.NonIndexed()
		if len(nonIndexed) == 0 {
			t.Fatalf("%s: has no non-indexed fields, so it carries no instruction id", h.event)
		}

		last := nonIndexed[len(nonIndexed)-1]
		if last.Name != "instructionId" {
			t.Errorf("%s: last non-indexed field is %q, want \"instructionId\" — "+
				"the relay would fetch the wrong action result", h.event, last.Name)
		}
		if got := last.Type.String(); got != "bytes32" {
			t.Errorf("%s: instructionId is %s, want bytes32", h.event, got)
		}
	}
}

// Each handler must name a method that exists and takes the enclave result tuple
// the relay packs. A typo here would only surface at runtime, against a funded
// chain, after a judge had already started the demo.
func TestHandlerMethodsAcceptAnEnclaveResult(t *testing.T) {
	want := []string{"bytes", "bytes32", "string", "uint8", "bytes"}

	methods := make([]string, 0, len(handlers)+1)
	for _, h := range handlers {
		methods = append(methods, h.method)
	}
	// The refusal path is routed to unconditionally, so it is not in the table.
	methods = append(methods, "submitFailure")

	for _, name := range methods {
		m, ok := bridgesafe.ControllerABI.Methods[name]
		if !ok {
			t.Errorf("%s: not in the controller ABI", name)
			continue
		}
		if len(m.Inputs) != len(want) {
			t.Errorf("%s: takes %d arguments, want %d", name, len(m.Inputs), len(want))
			continue
		}
		for i, typ := range want {
			if got := m.Inputs[i].Type.String(); got != typ {
				t.Errorf("%s: argument %d is %s, want %s", name, i, got, typ)
			}
		}
	}
}

// Every enclave command the controller can dispatch needs somewhere for its
// result to go. An unrouted one is a request that never leaves CREATED.
func TestEveryDispatchingEventIsRouted(t *testing.T) {
	// Events that carry an instructionId are, by construction, the ones that send
	// an instruction to the enclave and therefore produce a result.
	dispatching := map[string]bool{}
	for name, ev := range bridgesafe.ControllerABI.Events {
		for _, in := range ev.Inputs {
			if in.Name == "instructionId" {
				dispatching[name] = true
			}
		}
	}

	routed := map[string]bool{}
	for _, h := range handlers {
		routed[h.event] = true
	}

	for name := range dispatching {
		if !routed[name] {
			t.Errorf("%s dispatches an instruction but no handler consumes its result", name)
		}
	}
	if len(dispatching) == 0 {
		t.Fatal("no dispatching events found — the ABI fragment is probably wrong")
	}
}

func TestIsAlreadyApplied(t *testing.T) {
	cases := []struct {
		err  string
		want bool
	}{
		{"estimating gas for submitAuthorization (the call would revert): x", true},
		{"execution reverted: custom error WrongState", true},
		{"execution reverted: custom error TreasuryAlreadyBound", true},
		{"connection refused", false},
		{"estimating gas for submitAuthorization: nonce too low", false},
	}
	for _, c := range cases {
		if got := isAlreadyApplied(errString(c.err)); got != c.want {
			t.Errorf("isAlreadyApplied(%q) = %v, want %v", c.err, got, c.want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }
