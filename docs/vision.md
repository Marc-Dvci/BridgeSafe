# Vision — DoraHacks submission field

## BridgeSafe — Private Treasury

XRP is one of the largest crypto assets in the world, and almost none of it is
under programmatic control. An organisation holding XRP today has two options
when it wants software to spend it: hand a bot the private key and hope, or put a
human in the loop for every single payment. The first has no ceiling and no
recourse. The second does not scale and still proves nothing after the fact.
Neither gives a treasurer what they actually need — enforced limits, plus evidence
that the payment authorised is the payment that happened.

BridgeSafe makes XRP programmable without changing what it is. A treasury's XRPL
account is controlled from Flare: spending rules are published on chain where
anyone can audit them, individual payment instructions stay encrypted until a
confidential enclave opens them, and the enclave — which generated the signing key
inside itself and has no path to release it — checks each instruction against the
policy before signing a single, tightly constrained XRPL payment. A relay that
holds no key puts it on the ledger. Then the Flare Data Connector attests the
transaction, and the contract checks the source account, the destination, the
exact amount, a per-request memo, the success status, and that this transaction
has never settled another request. Only then is anything marked settled.

That last step is the point. Most cross-chain systems ask you to trust that
something happened. BridgeSafe requires a proof, checked on chain, before value is
recorded as delivered. The enclave can sign, but it cannot declare success. The
relay can broadcast, but it cannot alter or invent a payment. The published limits
are re-checked independently on chain, so even a compromised enclave cannot exceed
what the treasury advertised. Authority is bounded at every step by something
other than trust in an operator.

Deliberately, this is not a bridge and there is no new token. FXRP already solves
the wrapped case, and inventing another collateral model would add risk without
adding capability. XRP stays XRP. Flare contributes what it is genuinely best at:
a place to express and enforce policy, confidential compute to hold a key and make
a decision privately, and a data connector that can prove what happened on another
chain. Remove either Flare protocol and the product stops existing — which is the
honest test of whether an integration is real.

The immediate users are DAOs and companies paying contractors in XRP, protocol
treasuries that need spending discipline without a multisig ceremony per transfer,
and any Flare contract that needs to trigger an XRP payment as part of a larger
flow. The mechanism generalises well beyond that. Confidential recipient
allowlists let an enclave enforce "only these payees" without ever publishing who
they are. Batch payroll becomes one authorisation instead of fifty. Cosigner
thresholds above a value line, spending windows, and multi-role approval are all
policy extensions rather than redesigns.

Longer term, the interesting claim is not about XRP at all. The pattern — Flare
authorises, attested confidential compute executes, and the Data Connector proves
the result — is a general way to give a smart-contract platform safe, auditable
authority over assets that live on chains without smart contracts. Bitcoin is the
obvious next target once the XRPL security model has been reviewed in anger. What
BridgeSafe is really arguing is that Flare can be the control plane for assets
that live somewhere else, and that the way to earn that role is not to wrap them,
but to govern them and prove what happened.

The prototype is live and testable now: both contracts are deployed and
source-verified on Coston2, the enclave signs real payments accepted by the XRPL
Testnet ledger, and 112 tests across Solidity, Go and TypeScript hold the
invariants — 44 of them dedicated to the ways the system is supposed to refuse.
