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
| `contracts/test/` | 58 tests: lifecycle, 30+ negative cases, 6 cross-language |
| `extension/go/internal/xrpl/` | Restricted XRPL binary codec, address derivation, signing |
| `extension/go/internal/extension/` | Policy engine, authorize/sign handlers, ledger reader |
| `extension/go/internal/codec/` | ABI bridge between the enclave and the contracts |
| `extension/go/cmd/payload-builder/` | Loopback ECIES sealing for the console |
| `services/` | Broadcaster and FDC settlement worker |
| `infra/` | Self-hosted C-chain indexer configuration |
| `apps/web/` | Console with the execution trace |
| `scripts/` | Preflight, key generation, tunnel, deploy, secret and binding checks |

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

Addresses are written to `docs/deployment.json` by `scripts/deploy-coston2.sh`.
Both contracts are verifiable on the Coston2 Blockscout explorer with no API key.

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

## Honest limitations

Stated plainly because a submission that overclaims is worse than one that
scopes:

- **Not trustless.** TEE hardware, the cloud platform, FCC relays, this code,
  XRPL availability and FDC provider consensus are all in the trust base.
- **Not a production bridge.** No liquidity management, solvency invariants,
  upgrade governance or audit.
- **Not permanently private.** XRPL is public. Confidentiality is before
  execution only.
- **Simulated attestation by default.** Instruction routing, registration and
  signature verification are real; the hardware measurement is not. Promotion to
  a GCP Confidential Space VM is a configuration change, documented in
  `docs/threat-model.md`.

## Traction

None claimed. This is a hackathon prototype with no users, no pilot and no
partner conversations. Saying otherwise would be inventing evidence.

What exists instead is a working testnet system, a documented threat model, and a
demo script that takes a reviewer from an empty machine to a settled payment.

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
