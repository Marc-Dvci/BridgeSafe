# Threat model

What BridgeSafe defends against, and which component has to misbehave for each
failure. Companion to [SECURITY.md](../SECURITY.md), which covers operator safety.

## Trust base

Every system has one; naming it precisely is what lets an operator size the
guarantee. BridgeSafe's correctness depends on:

| Component | What it can do if compromised |
|---|---|
| TEE hardware / GCP Confidential Space | Sign a payment the policy forbids — but only up to the on-chain caps, which are re-checked independently. |
| Flare consensus | Reorder or censor instructions. Cannot forge an FDC proof. |
| FDC data providers (>50% weight) | Attest a payment that did not happen, settling a request falsely. |
| This application's contracts | Anything. They are the root of the on-chain rules. |
| The relayer | Delay a payment, or replay one already signed. Cannot alter, create or authorize one. |
| XRPL | Refuse to include a payment. Cannot alter a signed one. |

The claim BridgeSafe does make:

> Policy-controlled and independently verifiable cross-chain execution, using
> Flare consensus for authorization and attested confidential compute for signing.

## Attacks considered

### Redirect a payment to an attacker's account
The destination is inside a ciphertext sealed to the enclave and is covered by
the XRPL signature. Changing it requires the enclave key. Settlement additionally
requires the FDC-proved `receivingAddressHash` to equal the hash the enclave
committed to at authorization time.
*Tested:* `test_Reject_WrongRecipient`.

### Settle a request with an unrelated payment
An attacker pays the payee the right amount from their own account and presents
that proof. Rejected: the proof's `sourceAddressHash` must equal the treasury's.
*Tested:* `test_Reject_WrongSourceAccount`.

### Settle two requests with one payment
Each XRPL transaction id is recorded in `settledBy` on first use and can never
settle another request.
*Tested:* `test_Reject_ReusedXrplTransactionProof`.

### Satisfy request B with the payment for request A
Same treasury, same payee, same amount — only the memo differs. Each request
carries a distinct `memoRef` derived from
`keccak256(domain, chainId, controller, treasuryId, requestId, nonce)`, and the
proof's `firstMemoData` must equal `"BSF1" || memoRef` exactly.
*Tested:* `test_Reject_MemoFromAnotherRequest`.

### Replay an enclave result
Every result binds `chainId` and the controller address, and is checked against
the request's own `memoRef` and `payloadHash`. A result signed for another
deployment, another chain, or another request is refused.
*Tested:* `test_Reject_ResultBoundToAnotherController`, `test_Reject_ResultBoundToAnotherChain`, `test_Reject_AuthorizationForWrongPayload`.

### Replay a ciphertext into a different request
The decrypted payload repeats the chain, controller, treasury and request id, and
the enclave requires them to match the instruction header.
*Tested:* `TestAuthorizePayment_RejectsCiphertextFromAnotherRequest`.

### Get two signatures from one authorization
The enclave deletes the authorization when it signs.
*Tested:* `TestSignPayment_ConsumesTheAuthorization`.

### Drain the treasury through many concurrent requests
Budget is reserved at authorization, before any signature exists, so the
cumulative cap is enforced against reserved rather than settled totals.
*Tested:* `test_Reject_PaymentOverCumulativeCap`.

### Make the enclave sign something other than a payment
The XRPL codec can serialize exactly one shape. There is no field table, no
transaction-type parameter, and the type constant is hard-coded to `Payment`. A
`TrustSet`, an `AccountSet` that rekeys the account, an `EscrowFinish` or a
token transfer cannot be expressed, whatever the decrypted payload contains. The
amount encoder always clears the issued-currency bit.
*Tested:* `TestCodecCannotExpressAnotherTransactionType`, `TestValidateRejectsBadPayments`.

### Strand funds by withholding service
Settlement is permissionless — anyone can deliver a proof. The signed blob is
published in the `PaymentSigned` event, so anyone can broadcast it. An
uncooperative relayer delays; it cannot strand.

### Expire a request whose payment can still land
Releasing reserved budget while a broadcastable blob is still valid would let a
new request reuse the same allowance. The enclave sets `LastLedgerSequence` from
the request deadline, and the contract adds `SETTLEMENT_GRACE` before a signed
request may be expired.
*Tested:* `test_Reject_ExpiringSignedRequestBeforeGracePeriod`.

## Operating envelope

Design decisions that define what the system guarantees, and the reasoning behind
each.

**Confidentiality is pre-execution.** XRPL is a public ledger, so once a payment
lands its recipient and amount are visible and the memo links it to its request.
That is the correct boundary: what treasury operators need protected is the
*pending* payment file — who is about to be paid, and how much — and that stays
sealed inside the enclave until settlement. Enclave state and spending decisions
are confidential throughout.

**Attestation mode is configurable.** Instruction routing, on-chain registration
and signature verification are identical in both modes. `SIMULATED_TEE=true`
(`MODE=1`) is the development configuration; `MODE=0` on a GCP Confidential Space
VM adds the hardware measurement binding the running code to this repository, and
moves the TEE row of the trust table from "trusted" to "attested". Nothing else
changes, which is what makes the promotion a configuration decision rather than a
rewrite.

**One enclave per extension, by design for this release.** Flare's registry
already supports fanning an instruction to several machines
(`getRandomTeeIds(id, n)`), and the controller calls through that interface, so
raising the count is a parameter rather than a redesign.

**The ciphertext travels in calldata.** It is encrypted to the enclave's key and
publicly readable in that form. Flare's documentation notes that on-chain
encrypted data may become decryptable in future, which is precisely why the only
thing ever sent this way is a single payment instruction whose contents become
public on XRPL within minutes. Long-lived secrets never touch this path — the
treasury key is generated inside the enclave and has no serialization route out.

**Static analysis.** Slither 0.11.5 reports no high- or medium-severity
findings across both contracts, and `forge lint` is clean on `src/`. Slither's 73
remaining results are all informational: 63 are the `_leadingUnderscore`
parameter convention Flare's own contracts use, and one is a storage write inside
the `setExtensionId()` scan loop, which runs once at deployment. Reproduce with:

```bash
cd contracts
slither . --foundry-out-directory out --exclude-dependencies \
  --filter-paths "test|dependencies" --exclude-informational --exclude-low
```

Static analysis is a floor. It finds known bug patterns; it cannot know what a
system is supposed to mean. The invariants below come from reading the contracts
against this trust model, and each is pinned by a regression test.

**Replay of enclave results is guarded per handler.** An enclave `ActionResult`
signature never expires, so every handler that accepts one carries its own reason
why the same signed bytes cannot be applied twice. For payment requests that
reason is the state machine — a request leaves `CREATED` once. For treasury
binding it is the `bound` flag. A policy has no equivalent monotonic state, so
`confirmPolicy` uses an explicit pending-commitment slot that
`requestPolicyUpdate` sets and confirmation spends: an acknowledgement is valid
exactly once, for exactly the terms the owner published on chain, and the enclave
cannot install limits of its own choosing.

This matters in both directions. Published limits cannot be rolled back to a
looser earlier version, and the on-chain commitment cannot drift out of step with
the enclave's cached policy — the enclave declines any instruction whose header
commitment disagrees with its own, so keeping the two in lockstep is what keeps
the treasury able to pay at all. `test_Reject_ReplayingAnOldPolicyConfirmation`
and four sibling cases hold this. Any new handler accepting an enclave result
states its own anti-replay argument.

**A policy cannot be set below committed spend.** `requestPolicyUpdate` refuses a
cumulative cap lower than the treasury's already-reserved drops, keeping
`reservedDrops ≤ maxTotalDrops` true for the life of the treasury.

**Governance.** The contract owner sets the TEE address and the FDC verifier.
Both are one-way in the directions that matter — the verifier is the only route to
`SETTLED`, and the source network and FDC anchor are immutable at deployment. The
owner is an ordinary address that can be transferred to a multisig with a timelock
via `transferOwnership`, which is the expected production configuration.

## Scope

BridgeSafe executes and proves XRP treasury payments. It is not a bridge, issues
no token, and holds no liquidity: XRP stays XRP, FXRP already covers the wrapped
case, and avoiding invented collateral is what keeps the security surface small
enough to reason about completely.

Outside that boundary: Bitcoin execution, custom FDC attestation types,
multi-TEE governance and arbitrary-message signing. Each was considered and cut
deliberately — the XRPL signer accepts exactly one transaction shape, and there is
no code path from a decrypted instruction to an arbitrary object.
