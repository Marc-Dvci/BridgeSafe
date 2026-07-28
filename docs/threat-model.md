# Threat model

What BridgeSafe defends against, what it does not, and which component has to
misbehave for each failure. Companion to [SECURITY.md](../SECURITY.md), which
covers operator safety.

## Trust base

BridgeSafe is **not trustless**, and calling it that would create more questions
than it answers. Its correctness depends on:

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

## Known limitations

**Privacy ends at settlement.** XRPL is a public ledger. Once the payment lands,
recipient and amount are visible forever, and the memo links it to the request.
BridgeSafe provides confidentiality *before* execution and confidential enclave
state — not private transfers. Claiming otherwise would be false.

**Simulated attestation by default.** The documented Coston2 configuration runs
`SIMULATED_TEE=true` (`MODE=1`). The instruction routing, the on-chain
registration and the signature verification are all real. What is *not* real is
the hardware measurement: nothing proves the code running in the enclave is the
code in this repository. Promoting to `MODE=0` on a GCP Confidential Space VM
adds that, and changes the TEE row of the trust table from "trusted" to
"attested". Everything else is unchanged.

**Single enclave.** One TEE machine serves the extension. Flare's registry
supports fanning an instruction to several (`getRandomTeeIds(id, n)`), which
would remove the single point of failure. Not done here.

**The ciphertext is public.** It travels in transaction calldata, so it is
readable forever even though it is encrypted. Flare's own documentation warns
that on-chain encrypted data may be decryptable in future. For a one-shot payment
instruction whose contents become public on XRPL within minutes this is an
acceptable trade — it would **not** be acceptable for a long-lived secret, which
is why no long-lived secret is ever sent this way.

**No audit.** The contracts have not been reviewed by anyone but their author.

**Governance is a single key.** The contract owner sets the TEE address and the
verifier. On a real deployment that should be a multisig with a timelock.

## Deliberately out of scope

Bitcoin execution · mainnet funds · a new wrapped token · bridge liquidity pools ·
application-level provider slashing · custom FDC attestation types · permanent
transaction privacy · multi-TEE governance · arbitrary-message signing ·
production security claims.

These are exclusions, not oversights. Each was considered and cut to keep the
security surface small enough to reason about.
