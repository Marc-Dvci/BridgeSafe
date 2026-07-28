# BridgeSafe — Private Treasury

**An XRPL treasury controlled from Flare.** Payment instructions stay encrypted
until a confidential enclave checks them against a published spending policy and
signs an XRP payment. Nothing is treated as settled until the Flare Data
Connector proves the payment actually happened.

Built for **Flare Summer Signal**, targeting both bounties:

| Bounty | How BridgeSafe qualifies |
|---|---|
| **Confidential Compute Apps** | A Flare Compute Extension holds the treasury's XRPL key, decrypts payment instructions inside the TEE, enforces spending policy there, and signs a canonical XRPL transaction. The key never leaves the enclave. |
| **Interoperable Asset Products** | A closed loop across two chains: Flare authorizes, XRPL executes, FDC proves settlement back on Flare. XRP becomes controllable from an EVM contract without a wrapped token or a new bridge. |

> Runs on **Coston2 + XRPL Testnet**. See [SECURITY.md](SECURITY.md) before
> running the stack — step 4 of the demo publishes a port to the internet.

**Live on Coston2**, both source-verified:

| Contract | Address |
|---|---|
| `BridgeSafeController` | [`0x32176FCA…f48b752`](https://coston2-explorer.flare.network/address/0x32176FCA80690938194F30844501ea24Cf48b752) |
| `BridgeSafeFdcVerifier` | [`0x0B1B4371…fd5b1D8`](https://coston2-explorer.flare.network/address/0x0B1B437183571ba99a5A27E1Ac980CA2ffd5b1D8) |

---

## The problem

An organisation holding XRP has no good way to let software spend it under
rules. The options today are: give a bot the private key and hope, or put a human
in the loop for every payment. Neither gives you *enforced* limits plus an
auditable record that the payment you authorized is the payment that happened.

Meanwhile, a payment file is commercially sensitive before it executes. Publishing
"we are about to pay this counterparty this much" to a public mempool ahead of
time is a real disclosure, even though the payment itself becomes public a minute
later.

## What BridgeSafe does

1. A treasury is created on Flare with a **public policy** — per-payment cap,
   lifetime cap, request lifetime. The enclave generates the XRPL key and returns
   only the address.
2. The owner submits a payment instruction **encrypted to the enclave**. Flare
   stores the hash of that ciphertext, nothing more.
3. The enclave decrypts it, checks it against the policy, and returns an
   authorization revealing only the amount and a hash of the destination — enough
   for the contract to reserve budget.
4. The enclave signs a canonical XRPL `Payment` carrying a per-request memo.
5. A relay — which holds no key and can only carry decisions the enclave already
   signed — brings each result back on chain, and puts the signed blob on the
   ledger.
6. The **Flare Data Connector** attests the XRPL transaction. The contract checks
   source account, destination, exact amount, memo and success status, and that
   this transaction id has never settled another request. Only then: `SETTLED`.

```
   Flare (Coston2)                 Enclave (FCC)              XRPL Testnet
   ──────────────                  ─────────────              ────────────
   createPaymentRequest ─────────► decrypt + policy check
        (ciphertext)          ◄─── authorization (amount, dest hash)
   requestSignature      ─────────► build + sign Payment
                         ◄─────────  signed blob + predicted tx id
                                                        ──────► submit
   finalizePayment ◄─── FDC proof ◄───────────────────────────  payment
        │
        └─ matches source, destination, amount, memo, unused tx id → SETTLED
```

---

## Why this is not just "a signer with extra steps"

Three properties do the work, and each is enforced by something other than trust
in the enclave:

**The contract is the only address that can instruct the enclave.** Flare's
`TeeExtensionRegistry` rejects instructions from anyone but the registered
`InstructionSender`. So the treasury id, request id, memo reference and deadline
that the enclave receives are authenticated *by construction* — no signature
check needed, because nothing else could have produced them.

**Policy is re-checked on chain.** The enclave enforces the per-payment cap, and
then `BridgeSafeController` checks it again against the published policy before
reserving budget. A compromised enclave cannot quietly exceed the limits the
treasury advertised.

**Settlement requires proof, not assertion.** The enclave can sign; it cannot
mark anything settled. `BridgeSafeFdcVerifier` is a separate contract holding
exactly eight conditions, and an FDC Merkle proof has to satisfy all of them.
One XRPL transaction settles at most one request, ever.

Authorize and sign are also deliberately separate steps. Budget is reserved *at
authorization*, before any signature exists, which closes the race where two
concurrent requests both get signed against the same remaining allowance.

---

## Verified end to end

Every claim below is something that was run, not something that should work:

- **The XRPL codec against the real ledger.** `TestLive_PaymentIsAcceptedByTheLedger`
  generates a key the way the enclave does, funds it from the testnet faucet,
  builds and signs a payment, submits it, and the ledger returns `tesSUCCESS`
  with the transaction id the code predicted *before* submission and the memo
  intact.
- **FDC reports it the way the contract expects.** Feeding that transaction to
  Flare's XRPPayment verifier returns `firstMemoData` equal to the exact 36 memo
  bytes the contract compares, and `sourceAddressHash` equal to
  `keccak256(r-address)` — the same value the contract stores at treasury binding.
- **Go and Solidity agree on the wire format.** The Go encoder writes real
  vectors; `contracts/test/CrossLanguage.t.sol` decodes them with the production
  tuples. This seam fails silently otherwise: an ABI mismatch produces a
  correctly-signed result that the contract simply refuses.
- **ECIES matches the enclave.** The payload builder's output is decrypted with
  the same `go-ethereum/crypto/ecies` configuration the TEE node uses.
- **The console's ABI matches the deployed shape.** `scripts/check-web-abi.ts`
  compares every fragment `apps/web` declares against the compiled contract. This
  seam is silent in the worst way — change a struct and the UI still compiles,
  still type-checks, then throws the first time anyone opens it.

```
contracts   64 tests   (lifecycle, 44 negative cases, 6 cross-language)
extension   34 tests   (+ 1 live ledger test behind -tags live)
services    14 tests   (FDC response parsing, enclave result transport, event routing)
```

---

## Repository

```
contracts/     BridgeSafeController.sol   treasury, request state machine, FCC InstructionSender
               BridgeSafeFdcVerifier.sol  the eight conditions for SETTLED
extension/     Go Flare Compute Extension — policy engine + XRPL signer
               internal/xrpl/             restricted XRPL codec (Payment only)
               cmd/payload-builder/       loopback ECIES sealing for the UI
services/      result-relay/              carries signed enclave results on chain
               broadcaster/               submits signed blobs, holds no key
               fdc-worker/                drives the FDC attestation round trip
infra/         self-hosted C-chain indexer, so the stack needs nothing from Flare support
apps/web/      console with the execution trace
docs/          architecture, threat model, demo script, submission notes
scripts/       preflight, key generation, tunnel, deploy, secret and binding checks
               check-web-abi.ts           pins the console's ABI to the compiled contract
```

## Running it

```bash
cp .env.example .env
scripts/new-testnet-keys.sh      # fresh disposable testnet keys
scripts/install-hooks.sh         # secret scanning on commit and push
scripts/preflight.sh             # toolchain, endpoints, balances, hygiene
```

Then follow **[docs/demo-script.md](docs/demo-script.md)**, which walks the whole
path from an empty machine to a settled payment.

Requirements: Go 1.25+, Foundry, Docker, Node 20+, `jq`, `cloudflared`.

---

## What was newly built during the program

Everything in `contracts/`, `services/`, `infra/`, `apps/web/`, `scripts/`, and
all of `extension/go/internal` and `extension/go/cmd` is new work. See
[docs/submission.md](docs/submission.md) for the line-by-line split between what
was written, what was forked, and what was reused unchanged.

In short: `extension/` began as a fork of Flare's `fce-sign` example, kept for its
deployment and TEE-registration tooling, with its handlers replaced entirely.
Contract interfaces for the TEE registries are reproduced from Flare's published
examples, attributed in the source.

## Roadmap

**Near term.** Promote from simulated attestation to a real GCP Confidential
Space VM, so the code hash is hardware-measured. Add cosigner approval above a
value threshold. Batch payroll in one authorization.

**Medium term.** Fund treasuries from FXRP via FAssets. Confidential recipient
allowlists using a Merkle commitment, so the enclave can enforce "only these
payees" without publishing who they are. Multi-role approvals and spending
windows.

**Long term.** Bitcoin once the XRPL security model has been reviewed. Native
Protocol Managed Wallets when Flare publishes a developer interface. External
audit before anything touches real value.

## Licence

MIT. See [LICENSE](LICENSE).
