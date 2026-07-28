"use client";

import { XRPL_EXPLORER, coston2, isZero32, shorten, type PaymentRequest } from "@/lib/bridgesafe";

/**
 * The execution trace.
 *
 * This component is the point of the whole demo. A judge should be able to read
 * one screen and see where authority came from at each step: which stage the
 * enclave decided, which stage XRPL executed, and which stage the Flare Data
 * Connector proved. So each step names the thing that vouches for it rather than
 * just reporting a status.
 */

type Props = { request: PaymentRequest | null; treasuryXrpl: string };

const STAGES = [
  {
    key: "requested",
    title: "Requested",
    desc: "Instruction sealed to the enclave. Only its hash is on Flare.",
  },
  {
    key: "authorized",
    title: "Authorized by the enclave",
    desc: "Terms decrypted inside the TEE and checked against the policy. Budget reserved.",
  },
  {
    key: "signed",
    title: "XRPL payment signed",
    desc: "Enclave built and signed a Payment. The key never left the TEE.",
  },
  {
    key: "broadcast",
    title: "Submitted to XRPL",
    desc: "Relayer put the signed blob on the ledger. It holds no key.",
  },
  {
    key: "verified",
    title: "Proved by the Flare Data Connector",
    desc: "FDC attested the payment; the contract matched source, destination, amount and memo.",
  },
] as const;

/** Map the on-chain state to how far the trace has advanced. */
function progress(state: number): { reached: number; failed: boolean } {
  switch (state) {
    case 1: return { reached: 1, failed: false }; // Created
    case 2: return { reached: 2, failed: false }; // Authorized
    case 3: return { reached: 3, failed: false }; // Signed
    case 4: return { reached: 4, failed: false }; // Broadcast
    case 5: return { reached: 5, failed: false }; // Settled
    case 6: case 7: case 8: return { reached: 1, failed: true }; // Expired/Cancelled/Failed
    default: return { reached: 0, failed: false };
  }
}

export default function Trace({ request, treasuryXrpl }: Props) {
  if (!request) {
    return (
      <div className="card">
        <h2>Execution trace</h2>
        <p style={{ color: "var(--muted)", fontSize: 13, margin: 0 }}>
          Select a request to follow it from instruction to proof.
        </p>
      </div>
    );
  }

  const { reached, failed } = progress(request.state);
  const txId = !isZero32(request.xrplTxId)
    ? request.xrplTxId.slice(2).toUpperCase()
    : !isZero32(request.expectedTxId)
      ? request.expectedTxId.slice(2).toUpperCase()
      : "";

  return (
    <div className="card">
      <h2>Execution trace</h2>
      <ul className="trace">
        {STAGES.map((stage, i) => {
          const index = i + 1;
          let cls = "todo";
          if (index <= reached) cls = "done";
          else if (index === reached + 1 && !failed) cls = "active";
          if (failed && index === reached + 1) cls = "failed";

          return (
            <li key={stage.key} className={`step ${cls}`}>
              <span className="pip" />
              <div className="body">
                <div className="title">{stage.title}</div>
                <div className="desc">{stage.desc}</div>

                {stage.key === "requested" && index <= reached && (
                  <div className="meta">
                    payload hash {shorten(request.payloadHash, 12, 8)}
                    <br />
                    memo reference {shorten(request.memoRef, 12, 8)}
                  </div>
                )}

                {stage.key === "authorized" && index <= reached && (
                  <div className="meta">
                    destination hash {shorten(request.destinationHash, 12, 8)}
                    <br />
                    <span style={{ color: "var(--muted)" }}>
                      the address itself stays sealed until XRPL makes it public
                    </span>
                  </div>
                )}

                {stage.key === "signed" && index <= reached && txId && (
                  <div className="meta">
                    predicted tx id{" "}
                    <a href={`${XRPL_EXPLORER}/transactions/${txId}`} target="_blank" rel="noreferrer">
                      {shorten(txId, 10, 8)}
                    </a>
                  </div>
                )}

                {stage.key === "broadcast" && index <= reached && txId && (
                  <div className="meta">
                    <a href={`${XRPL_EXPLORER}/transactions/${txId}`} target="_blank" rel="noreferrer">
                      view on XRPL Testnet →
                    </a>
                  </div>
                )}

                {stage.key === "verified" && index === reached + 1 && !failed && reached >= 4 && (
                  <div className="meta" style={{ color: "var(--pending)" }}>
                    waiting for the FDC voting round — typically 90–180 seconds
                  </div>
                )}

                {stage.key === "verified" && index <= reached && (
                  <div className="meta">
                    <a
                      href={`${coston2.blockExplorers.default.url}/address/${process.env.NEXT_PUBLIC_FDC_VERIFIER ?? ""}`}
                      target="_blank"
                      rel="noreferrer"
                    >
                      verifier contract →
                    </a>
                  </div>
                )}
              </div>
            </li>
          );
        })}
      </ul>

      {failed && (
        <div className="notice error">
          This request ended as <strong>{["", "", "", "", "", "", "Expired", "Cancelled", "Failed"][request.state]}</strong>.
          Any reserved budget has been returned to the treasury.
        </div>
      )}

      {request.state === 5 && (
        <div className="notice ok">
          Settled. The contract accepted an FDC proof that {treasuryXrpl ? <code>{shorten(treasuryXrpl, 8, 6)}</code> : "the treasury"} paid
          the authorized destination the exact authorized amount, carrying this request&apos;s memo — and that this XRPL
          transaction had never settled another request.
        </div>
      )}
    </div>
  );
}
