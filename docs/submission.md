# Submission notes

Everything the Flare Summer Signal submission form asks for, in one place.

## Project

**BridgeSafe — Private Treasury**

An XRPL treasury controlled from Flare. Payment instructions stay encrypted until
a confidential enclave checks them against a published spending policy and signs
an XRP payment; nothing is treated as settled until the Flare Data Connector
proves the payment happened.

**Bounties:** both — Confidential Compute Apps *and* Interoperable Asset Products.

## Target user

The primary demo persona is a **DAO treasury paying contractors in XRP**.

They hold XRP, need software to spend it under enforced limits rather than a
human approving each transfer, and need an auditable record that the payment
authorized is the payment that happened. Today they choose between handing a bot
a private key or putting a person in the loop every time.

Secondary users, same machinery: protocols holding XRP, crypto companies running
XRP payroll, and Flare contracts that need to trigger an XRP payment.

## How BridgeSafe uses Flare

Not decoratively — remove either protocol and the product stops existing.

**Flare Confidential Compute** is where the treasury lives. The extension
generates the XRPL key inside the TEE, decrypts payment instructions there,
enforces the spending policy there, and signs a canonical XRPL `Payment` there.
The key has no path out of the enclave. `BridgeSafeController` is itself the
registered `InstructionSender`, so it is the only address Flare's
`TeeExtensionRegistry` will accept instructions from for this extension — which
is what makes the routing fields the enclave receives trustworthy without any
further proof.

**The Flare Data Connector** is the only route to `SETTLED`. `BridgeSafeFdcVerifier`
takes an `XRPPayment` attestation and checks the Merkle proof, the attestation
type, the source network, XRPL success status, the treasury's own source account,
the authorized destination, the exact amount, a per-request memo, and that the
transaction id has never settled another request.

**FTSO is deliberately not used.** BridgeSafe has no price condition. Adding a
price feed to show another logo would be exactly the superficial integration the
judging criteria warn about. It appears in the roadmap only where a real price
condition would exist.

## Newly built during the program

**Written from scratch:**

| Path | What |
|---|---|
| `contracts/src/BridgeSafeController.sol` | Treasury registry, request state machine, FCC InstructionSender |
| `contracts/src/BridgeSafeFdcVerifier.sol` | The eight conditions for settlement |
| `contracts/src/lib/TeeResult.sol` | Enclave `ActionResult` signature verification |
| `contracts/test/` | 64 tests: lifecycle, 44 negative cases, 6 cross-language |
| `extension/go/internal/xrpl/` | Restricted XRPL binary codec, address derivation, signing |
| `extension/go/internal/extension/` | Policy engine, authorize/sign handlers, ledger reader |
| `extension/go/internal/codec/` | ABI bridge between the enclave and the contracts |
| `extension/go/cmd/payload-builder/` | Loopback ECIES sealing for the console |
| `services/` | Enclave result relay, broadcaster, and FDC settlement worker |
| `infra/` | Self-hosted C-chain indexer configuration |
| `apps/web/` | Console with the execution trace |
| `scripts/` | Preflight, key generation, tunnel, deploy, secret, port and ABI-parity checks |

**Forked and substantially rewritten:** `extension/` began as
`flare-foundation/fce-sign`, kept for its deployment and TEE-registration tooling
(`scripts/`, `go/tools/`, Dockerfiles, compose). Its handlers, types, config and
contract were replaced entirely. One upstream change worth naming: the compose
file defaulted the proxy ports to `0.0.0.0`; BridgeSafe binds them to loopback and
reaches them through a tunnel instead.

**Reused unchanged:** Flare's TEE node and proxy images, the
`flare-system-c-chain-indexer` source, `flare-periphery-contracts` 0.1.52, and
the `ITeeExtensionRegistry` / `ITeeMachineRegistry` interface shapes, which are
reproduced from Flare's published examples and attributed in the source.

## Deployment

**Network:** Coston2 (chain id 114) and XRPL Testnet.

**Deployed and source-verified on Coston2:**

| Contract | Address |
|---|---|
| `BridgeSafeController` | [`0x32176FCA80690938194F30844501ea24Cf48b752`](https://coston2-explorer.flare.network/address/0x32176FCA80690938194F30844501ea24Cf48b752) |
| `BridgeSafeFdcVerifier` | [`0x0B1B437183571ba99a5A27E1Ac980CA2ffd5b1D8`](https://coston2-explorer.flare.network/address/0x0B1B437183571ba99a5A27E1Ac980CA2ffd5b1D8) |

Both are verified on the Coston2 Blockscout explorer (solc v0.8.27), so every
condition described in this document can be read as source on chain rather than
taken on trust. The verifier is bound to the controller immutably at construction,
its accepted source network is fixed to `testXRP`, and its FDC anchor is resolved
through Flare's `ContractRegistry` at call time — confirmed live to return
`0x906507E0B64bcD494Db73bd0459d1C667e14B933`, Flare's published `FdcVerification`
address, which means the trust anchor cannot be repointed after deployment.

One command, `scripts/deploy-coston2.sh`, does the whole thing and writes every
address into `.env`, `apps/web/.env.local` and `docs/deployment.json`, so nothing
is copied by hand. It was rehearsed against a fork of live Coston2 before being
run for real.

Flare infrastructure used:

| Contract | Address |
|---|---|
| FlareTeeManager (both TEE registries) | `0x1a9C4A0f9D76c0b1D91d22E24E573a9b377618aE` |
| FdcHub | `0x48aC463d7975828989331F4De43341627b9c5f1D` |
| FdcRequestFeeConfigurations | `0x191a1282Ac700edE65c5B0AaF313BAcC3eA7fC7e` |
| Relay | `0xa10B672D1c62e5457b17af63d4302add6A99d7dE` |
| FdcVerification | resolved via `ContractRegistry` at call time |

FDC source id: `testXRP`. Attestation type: `XRPPayment` (`0x08`).

## Evidence it works

- `TestLive_PaymentIsAcceptedByTheLedger` — generates a key as the enclave does,
  funds it from the faucet, signs a payment with the hand-written codec, submits
  it, and the ledger returns `tesSUCCESS` with the transaction id predicted
  before submission and the memo intact.
- Feeding that same transaction to Flare's XRPPayment verifier returns
  `firstMemoData` equal to the exact 36 bytes the contract compares, and
  `sourceAddressHash` equal to `keccak256(r-address)` — the value the contract
  stores at treasury binding.
- `contracts/test/CrossLanguage.t.sol` decodes real Go encoder output with the
  production tuples, pinning the seam that otherwise fails silently.
- The payload builder's ciphertext round-trips through the same
  `go-ethereum/crypto/ecies` configuration the TEE node decrypts with.
- `scripts/check-web-abi.ts` pins the console's hand-written ABI to the compiled
  contract, closing the TypeScript counterpart of the same seam.

## Security engineering

Slither 0.11.5 reports no high- or medium-severity findings on either contract,
and `forge lint` is clean on `src/`.

Beyond the tooling, the contracts were reviewed against their own trust model,
which is where the interesting work is. Enclave signatures never expire, so every
handler that accepts one needs its own reason why the same bytes cannot be
submitted twice. For payment requests that reason is the state machine — a request
leaves `CREATED` once. A policy has no equivalent monotonic state, so
`confirmPolicy` carries an explicit pending-commitment slot that
`requestPolicyUpdate` sets and confirmation spends: an acknowledgement is valid
exactly once, for exactly the terms the treasury owner published. That property is
regression-tested by `test_Reject_ReplayingAnOldPolicyConfirmation` and four
sibling cases, and it is the kind of invariant static analysis cannot check
because it is a property of what the system means rather than of a code pattern.

The same principle runs through the design: 44 of the 64 contract tests are
negative cases, and each one names a specific way the system is supposed to
refuse.

## Scope

BridgeSafe is a **treasury execution and settlement-proof layer**, deliberately
scoped to that. It is not a bridge and issues no token: XRP stays XRP, FXRP
already exists, and a product that avoids inventing collateral has a much shorter
path to being trusted with real money.

The security model is stated in full in `docs/threat-model.md` — what each
component is trusted for, and what a compromise of each would and would not
achieve. It runs on Coston2 and XRPL Testnet, with attestation configurable
between the simulated mode used for development and a GCP Confidential Space VM,
which is a configuration change rather than a rewrite.

Confidentiality is pre-execution: instructions, spending decisions and batch
contents stay sealed until the payment settles, at which point XRPL makes the
transfer public as it does for every XRP payment. That is the property treasury
operators actually need — nobody learns who is about to be paid, or how much,
before it happens.

## Where it stands

A working testnet system: two contracts deployed and source-verified on Coston2,
an enclave that holds the signing key and enforces policy, three unprivileged
relay services, a web console reading the live deployment and showing the
execution trace, a documented threat model, and a demo script that takes a
reviewer from an empty machine to a settled payment in about forty minutes.

112 tests across the three languages — 64 on the contracts (44 of them negative
cases), 34 on the enclave including one that signs and submits a real payment to
the XRPL Testnet ledger, and 14 on the services.

## Roadmap

**Near term** — real attestation on GCP Confidential Space; cosigner approval
above a value threshold; batch payroll in one authorization; fan instructions to
multiple enclaves.

**Medium term** — fund treasuries from FXRP via FAssets; confidential recipient
allowlists via a Merkle commitment, so the enclave enforces "only these payees"
without publishing who they are; multi-role approvals and spending windows;
FTSO-triggered payments where a genuine price condition exists.

**Long term** — Bitcoin once the XRPL security model has been reviewed; native
Protocol Managed Wallets when Flare publishes a developer interface; institutional
policy templates; formal verification and external audit before real value.

## Links

- Repository: <https://github.com/Marc-Dvci/BridgeSafe>
- Architecture: [`docs/architecture.md`](architecture.md)
- Threat model: [`docs/threat-model.md`](threat-model.md)
- Run it yourself: [`docs/demo-script.md`](demo-script.md)
- Operator safety: [`SECURITY.md`](../SECURITY.md)
