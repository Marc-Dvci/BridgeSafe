// Package config holds the extension's operation identifiers and runtime settings.
package config

import (
	"os"
	"strconv"
	"time"
)

const (
	// Version is reported in GET /state and stamped on every ActionResult.
	Version = "0.1.0"

	// Operation identifiers. These strings are hashed and compared against the
	// bytes32 constants in contracts/src/BridgeSafeController.sol. A mismatch
	// surfaces as "unsupported op type" at run time rather than at build time,
	// so the contract and this file must be edited together.
	//
	//   Solidity: bytes32 OP_TYPE_TREASURY = bytes32("TREASURY")
	OPTypeTreasury = "TREASURY"

	OPCommandCreateTreasuryKey = "CREATE_TREASURY_KEY"
	OPCommandRegisterPolicy    = "REGISTER_POLICY"
	OPCommandAuthorizePayment  = "AUTHORIZE_PAYMENT"
	OPCommandSignXRPLPayment   = "SIGN_XRPL_PAYMENT"

	TimeoutShutdown = 5 * time.Second
)

// XRPL settings.
const (
	// DefaultFeeDrops is the fee stamped on every payment. XRPL's base fee is
	// 10 drops; 12 gives headroom without being worth policing per-transaction.
	DefaultFeeDrops uint64 = 12

	// LedgerSecondsPerLedger converts a request's remaining lifetime into a
	// LastLedgerSequence window. XRPL closes a ledger roughly every 4 seconds.
	LedgerSecondsPerLedger = 4

	// MinLedgerWindow is the floor on LastLedgerSequence - currentLedger, so a
	// payment signed just before expiry still has a chance to be included.
	MinLedgerWindow uint32 = 10

	// MaxLedgerWindow caps how far ahead a payment can remain includable. This is
	// what bounds the contract's SETTLEMENT_GRACE: past this window the signed
	// blob is dead on XRPL and the reserved budget is safe to release.
	MaxLedgerWindow uint32 = 750 // ~50 minutes
)

// Defaults, overridden by environment variables in init().
var (
	ExtensionPort = 7702
	SignPort      = 7701
	ConfigPort    = 5501

	// XRPLEndpoint is the JSON-RPC the enclave reads account sequence and ledger
	// state from. It is read-only: the enclave never submits, so a compromised
	// endpoint can stall a payment but cannot redirect one — destination and
	// amount come from the instruction and are covered by the signature.
	XRPLEndpoint = "https://s.altnet.rippletest.net:51234/"
)

func init() {
	intFromEnv("EXTENSION_PORT", &ExtensionPort)
	intFromEnv("SIGN_PORT", &SignPort)
	intFromEnv("CONFIG_PORT", &ConfigPort)
	if v := os.Getenv("XRPL_RPC_URL"); v != "" {
		XRPLEndpoint = v
	}
}

func intFromEnv(name string, target *int) {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*target = n
		}
	}
}
