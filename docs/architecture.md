# Architecture

## The loop

```
┌──────────────────────── Flare (Coston2, chain 114) ────────────────────────┐
│                                                                            │
│  BridgeSafeController ──── the registered InstructionSender                 │
│    · treasuries + published policies                                        │
│    · request state machine                                                  │
│    · verifies enclave ActionResult signatures                               │
│                                                                            │
│  BridgeSafeFdcVerifier ──── the only path to SETTLED                        │
│    · IFdcVerification.verifyXRPPayment                                      │
│    · eight conditions, one XRPL tx id spent once                            │
└───────────┬────────────────────────────────────────────────▲───────────────┘
            │ sendInstructions()                             │ finalizePayment()
            ▼                                                │
┌───────────────────────── FCC enclave ──────────┐   ┌────────┴───────────────┐
│  XRPL key, generated inside, never leaves      │   │  fdc-worker            │
│  decrypt · policy check · sign Payment         │   │  prepare → FdcHub →    │
└───────────┬────────────────────────────────────┘   │  round → DA proof      │
            │ signed blob (via PaymentSigned event)   └────────▲───────────────┘
            ▼                                                  │
     ┌──────────────┐        submit          ┌─────────────────┴──────┐
     │ broadcaster  │ ─────────────────────► │   XRPL Testnet         │
     │ holds no key │                        │   Payment + memo       │
     └──────────────┘                        └────────────────────────┘
```

## Components

### `BridgeSafeController`

Three roles in one contract, deliberately:

1. **Treasury registry.** Policies are public so limits are auditable, even
   though individual instructions are not.
2. **FCC InstructionSender.** Flare's `TeeExtensionRegistry` accepts instructions
   only from the address bound to the extension at registration. Because that
   address is this contract, every field it puts into an instruction header —
   treasury id, request id, memo reference, deadline, policy commitment — is
   authenticated by construction. The enclave needs no signature check to trust
   them; nothing else could have produced them.
3. **Request state machine.**

```
CREATED ──► AUTHORIZED ──► SIGNED ──► BROADCAST ──► SETTLED
   │            │             │           │
   └── CANCELLED┘             └───────────┴──► EXPIRED
   └── FAILED (enclave declined, signature verified)
```

`SETTLED` is reachable only from `BridgeSafeFdcVerifier`.

### `BridgeSafeFdcVerifier`

Kept separate and small on purpose: settlement is the security-critical half, and
every condition that lets value be recorded as delivered should fit on one
screen. A proof must satisfy all eight:

1. `IFdcVerification.verifyXRPPayment` accepts the Merkle proof
2. attestation type is `XRPPayment`
3. source is the expected XRPL network (`testXRP`)
4. XRPL status is success
5. funds left the treasury's own r-address
6. they reached the authorized destination
7. the received amount equals the authorized amount exactly
8. the first memo is exactly `"BSF1" || memoRef`, and this tx id is unused

### The enclave extension

Commands, matching `bytes32` constants in the controller:

| Command | Does |
|---|---|
| `CREATE_TREASURY_KEY` | Generate the XRPL keypair in-enclave, cache the policy, return only the address |
| `REGISTER_POLICY` | Replace the enforced policy; contract records it only once acknowledged |
| `AUTHORIZE_PAYMENT` | Decrypt, validate, check policy, reveal amount + destination hash |
| `SIGN_XRPL_PAYMENT` | Build and sign the canonical Payment for an authorized request |

**Why authorize and sign are separate.** Budget is reserved on chain at
authorization, before a signature exists. That ordering closes the race where two
concurrent requests are each signed against the same remaining allowance. It also
means the signing step is told only *which* request to sign — the terms come from
enclave memory, so a payment cannot be authorized on one set of terms and signed
on another. The authorization is deleted when it is used.

### The XRPL codec

`extension/go/internal/xrpl` serializes exactly one thing: a native-XRP `Payment`
with a single memo. That restriction is the security property. There is no field
table to extend, no transaction-type parameter, and no path from a decrypted
instruction to an arbitrary object. A `TrustSet`, an `AccountSet` that rekeys the
account, an `EscrowFinish` or a token transfer cannot be expressed at all.

Written by hand rather than pulled in as a dependency for two further reasons:
the enclave image needs a reproducible code hash, and a smaller dependency tree
is a smaller attack surface for the one component holding a key.

`LastLedgerSequence` is derived from the request deadline, which is what makes
expiry enforceable on XRPL rather than merely asserted on Flare.

### Services

Both are unprivileged, and their hand-written ABI fragments are the whole of
their on-chain reach.

**broadcaster** watches `PaymentSigned`, submits the blob the event carries, and
reports the transaction id. It holds no XRPL key and can call exactly one
controller method, which only accepts the id the enclave already predicted.

**fdc-worker** watches `PaymentBroadcast` and runs the attestation round trip. It
cannot settle by assertion — it delivers a proof and the contract decides.

Settlement is permissionless and the signed blob is public, so neither service
can strand a payment by going away.

### Local indexer

Flare's extension proxy reads signing policies from a C-chain indexer database,
and Flare supplies credentials to theirs on request. `infra/` runs
`flare-system-c-chain-indexer` in FSP mode against the public Coston2 RPC into a
loopback-bound MySQL. The proxy cannot tell the difference, and the stack has one
fewer human dependency.

## Data flow of one payment

1. Owner submits ciphertext. Contract stores `keccak256(ciphertext)`, assigns a
   nonce, derives `memoRef`, dispatches `AUTHORIZE_PAYMENT`.
2. Enclave recomputes the payload hash, decrypts, requires the plaintext to name
   the same chain / controller / treasury / request as the header, validates the
   destination, checks the policy, caches the authorization.
3. Contract verifies the enclave signature, re-checks the policy against the
   published limits, reserves budget, records amount and destination hash.
4. Owner requests a signature. Enclave reads the account sequence and current
   ledger, builds the Payment, signs, deletes the authorization.
5. Contract verifies the signature, derives the blob hash from the blob itself
   rather than trusting an asserted one, and publishes the blob so anyone can
   broadcast it.
6. Broadcaster submits. XRPL applies a sequence number once, so resubmission
   after a restart cannot produce a second payment.
7. fdc-worker prepares an attestation, pays the fee, waits ~90–180s for round
   finalisation, fetches the proof, calls `finalizePayment`.
8. Verifier checks the eight conditions, marks the tx id spent, and the request
   becomes `SETTLED`.

## Cross-language seams

Two places where Go and Solidity must agree exactly, both pinned by tests
because neither fails at build time:

**ABI encoding of enclave results.** A mismatch produces a correctly-signed
result the contract refuses to decode, which looks like a signing bug.
`contracts/test/CrossLanguage.t.sol` decodes real Go encoder output.

**The TEE signing domain.** The node signs
`keccak256(abi.encode("TEE_ACTION_RESULT", chainId, ActionResult.Hash()))` under
the EIP-191 prefix. `TeeResult.sol` reproduces it; the chain id is what stops a
result signed for Coston2 being replayed elsewhere.
