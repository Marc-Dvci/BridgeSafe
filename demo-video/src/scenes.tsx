import React from "react";
import {interpolate, useCurrentFrame} from "remotion";

import {Badge, Card, Chrome, ConsoleHeader, Enter, Mono, Notice, Pill, Row, Slide} from "./components";
import {chain, colors, fonts, radius} from "./theme";
import {FPS, demo, facts} from "./story";

const s = (n: number) => Math.round(n * FPS);

/* =================================================================== 1. problem */

export const SceneProblem: React.FC = () => {
  const frame = useCurrentFrame();
  const options = [
    {
      title: "Give a bot the key",
      body: "Unlimited authority, no enforced ceiling, and one compromise drains the account.",
      tone: colors.bad,
    },
    {
      title: "Put a human in the loop",
      body: "Every payment waits on a person. Does not scale, and still proves nothing afterwards.",
      tone: colors.pending,
    },
  ];

  return (
    <Slide eyebrow="The problem" title="An XRP treasury has no safe way to let software spend it.">
      <div style={{display: "flex", gap: 30, maxWidth: 1320}}>
        {options.map((o, i) => (
          <Enter key={o.title} at={s(0.9) + i * s(0.5)} style={{flex: 1}}>
            <div
              style={{
                background: colors.panel,
                border: `1px solid ${colors.line}`,
                borderLeft: `3px solid ${o.tone}`,
                borderRadius: radius,
                padding: "28px 30px",
                height: "100%",
              }}
            >
              <div style={{fontSize: 30, fontWeight: 660, marginBottom: 12}}>{o.title}</div>
              <div style={{fontSize: 21, color: colors.muted, lineHeight: 1.5}}>{o.body}</div>
            </div>
          </Enter>
        ))}
      </div>

      <Enter at={s(2.6)}>
        <div
          style={{
            marginTop: 44,
            fontSize: 26,
            color: colors.text,
            opacity: interpolate(frame, [s(2.6), s(3.2)], [0, 1], {
              extrapolateLeft: "clamp",
              extrapolateRight: "clamp",
            }),
          }}
        >
          Neither gives you <strong>enforced limits</strong> plus{" "}
          <strong>proof the payment you authorised is the payment that happened.</strong>
        </div>
      </Enter>
    </Slide>
  );
};

/* ==================================================================== 2. loop */

const LoopNode: React.FC<{
  at: number;
  label: string;
  sub: string;
  accent: string;
  active: boolean;
}> = ({at, label, sub, accent, active}) => (
  <Enter at={at} style={{flex: 1}}>
    <div
      style={{
        background: colors.panel,
        border: `1px solid ${active ? accent : colors.line}`,
        borderRadius: radius,
        padding: "26px 24px",
        textAlign: "center",
        boxShadow: active ? `0 0 0 1px ${accent}55, 0 18px 50px ${accent}22` : "none",
        transition: "none",
      }}
    >
      <div style={{fontSize: 15, letterSpacing: ".12em", textTransform: "uppercase", color: accent, fontWeight: 650}}>
        {label}
      </div>
      <div style={{fontSize: 21, color: colors.text, marginTop: 12, lineHeight: 1.4}}>{sub}</div>
    </div>
  </Enter>
);

const Arrow: React.FC<{at: number}> = ({at}) => {
  const frame = useCurrentFrame();
  const w = interpolate(frame, [at, at + s(0.35)], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  return (
    <div style={{width: 54, display: "flex", alignItems: "center", justifyContent: "center"}}>
      <div style={{width: `${w * 100}%`, height: 2, background: colors.line, position: "relative"}}>
        <div
          style={{
            position: "absolute",
            right: -1,
            top: -4,
            width: 0,
            height: 0,
            borderTop: "5px solid transparent",
            borderBottom: "5px solid transparent",
            borderLeft: `8px solid ${colors.line}`,
            opacity: w > 0.9 ? 1 : 0,
          }}
        />
      </div>
    </div>
  );
};

export const SceneLoop: React.FC = () => {
  const frame = useCurrentFrame();
  const stage = frame < s(4.5) ? 0 : frame < s(7) ? 1 : frame < s(10) ? 2 : 3;

  return (
    <Slide eyebrow="What BridgeSafe is" title="A closed loop across two chains.">
      <div style={{display: "flex", alignItems: "stretch", maxWidth: 1420}}>
        <LoopNode at={s(0.8)} label="Flare" sub="Authorises under a public policy" accent={chain.flare} active={stage >= 0} />
        <Arrow at={s(1.4)} />
        <LoopNode at={s(1.7)} label="Enclave" sub="Holds the key. Checks. Signs." accent={chain.enclave} active={stage >= 1} />
        <Arrow at={s(2.3)} />
        <LoopNode at={s(2.6)} label="XRPL" sub="Executes the payment" accent={chain.xrpl} active={stage >= 2} />
        <Arrow at={s(3.2)} />
        <LoopNode at={s(3.5)} label="FDC" sub="Proves it landed, back on Flare" accent={colors.ok} active={stage >= 3} />
      </div>

      <Enter at={s(5.2)}>
        <div style={{marginTop: 46, fontSize: 25, color: colors.muted, maxWidth: 1320, lineHeight: 1.55}}>
          No wrapped token. No bridge liquidity. XRP stays XRP — Flare just becomes the
          place that decides whether it may move, and the place that finds out whether it did.
        </div>
      </Enter>
    </Slide>
  );
};

/* ================================================================== 3. policy */

export const ScenePolicy: React.FC = () => (
  <div style={{position: "absolute", inset: 0, display: "flex", alignItems: "center", padding: "0 130px 130px", gap: 60}}>
    <div style={{flex: "0 0 500px", fontFamily: fonts.sans, color: colors.text}}>
      <Enter at={0}>
        <div
          style={{
            fontSize: 19,
            letterSpacing: ".18em",
            textTransform: "uppercase",
            color: colors.accent,
            fontWeight: 650,
            marginBottom: 20,
          }}
        >
          Public rules
        </div>
      </Enter>
      <Enter at={s(0.3)}>
        <div style={{fontSize: 50, fontWeight: 680, letterSpacing: "-0.02em", lineHeight: 1.16, marginBottom: 28}}>
          The limits are auditable. The payments are not.
        </div>
      </Enter>
      <Enter at={s(0.8)}>
        <div style={{fontSize: 22, color: colors.muted, lineHeight: 1.55}}>
          A treasury's spending policy lives on Flare in the open, so anyone can check what
          this account is permitted to do — without learning a single thing about who it pays.
        </div>
      </Enter>
    </div>

    <Enter at={s(0.6)} style={{flex: 1}}>
      <Chrome url={`${facts.coston2Explorer}/address/${facts.controller.slice(0, 12)}…`}>
        <div style={{padding: 34, background: colors.bg}}>
          <ConsoleHeader />
          <Card title="Treasury">
            <Row k="Owner" v="0xd02Be5Bc…F2fBA" />
            <Row k="XRPL account" v={<span style={{color: colors.accentSoft}}>{facts.treasuryXrpl}</span>} />
            <Row k="Per-payment cap" v={`${demo.perPaymentCapXrp} XRP`} />
            <Row k="Lifetime cap" v={`${demo.lifetimeCapXrp} XRP`} />
            <Row k="Spent / reserved" v="0 XRP" />
            <Row k="Request lifetime" v={`${demo.ttlMinutes} min`} last />
          </Card>
        </div>
      </Chrome>
    </Enter>
  </div>
);

/* ==================================================================== 4. seal */

export const SceneSeal: React.FC = () => {
  const frame = useCurrentFrame();
  const sealed = frame > s(6.5);

  return (
    <div style={{position: "absolute", inset: 0, display: "flex", alignItems: "center", padding: "0 130px 130px", gap: 56}}>
      <Enter at={0} style={{flex: 1}}>
        <Chrome url="127.0.0.1:3000">
          <div style={{padding: 34, background: colors.bg}}>
            <ConsoleHeader />
            <Card title="New confidential payment">
              <FormField label="Destination (XRPL r-address)" value={facts.payeeXrpl} at={s(1.2)} />
              <FormField label="Amount (XRP)" value={String(demo.amountXrp)} at={s(2.4)} />
              <FormField label="Reference (stays inside the enclave)" value="contractor invoice" at={s(3.4)} />
              <Enter at={s(4.4)}>
                <div
                  style={{
                    background: sealed ? colors.panel2 : colors.accent,
                    color: sealed ? colors.muted : "#16110e",
                    borderRadius: 8,
                    padding: "13px 20px",
                    fontWeight: 650,
                    fontSize: 19,
                    marginTop: 24,
                    textAlign: "center",
                    border: sealed ? `1px solid ${colors.line}` : "none",
                  }}
                >
                  {sealed ? "Sealing the instruction to the enclave…" : "Seal and submit"}
                </div>
              </Enter>
            </Card>
          </div>
        </Chrome>
      </Enter>

      <div style={{flex: "0 0 560px", fontFamily: fonts.sans}}>
        <Enter at={s(5.4)}>
          <div
            style={{
              fontSize: 19,
              letterSpacing: ".18em",
              textTransform: "uppercase",
              color: colors.accent,
              fontWeight: 650,
              marginBottom: 20,
            }}
          >
            What reaches the chain
          </div>
        </Enter>

        <Enter at={s(5.8)}>
          <div
            style={{
              background: colors.panel,
              border: `1px solid ${colors.line}`,
              borderRadius: radius,
              padding: 26,
              fontFamily: fonts.mono,
              fontSize: 18,
              lineHeight: 1.9,
            }}
          >
            <div style={{color: colors.muted}}>createPaymentRequest(</div>
            <div style={{paddingLeft: 22}}>
              treasuryId: <Mono color={colors.text}>1</Mono>
            </div>
            <div style={{paddingLeft: 22, color: colors.ok}}>
              payloadHash: {demo.payloadHash}
            </div>
            <div style={{color: colors.muted}}>)</div>
          </div>
        </Enter>

        <Enter at={s(7.4)}>
          <div style={{marginTop: 26, fontSize: 22, color: colors.muted, lineHeight: 1.55}}>
            The destination and the amount are <strong style={{color: colors.text}}>not there</strong>.
            Only the hash of a ciphertext only the enclave can open.
          </div>
        </Enter>
      </div>
    </div>
  );
};

const FormField: React.FC<{label: string; value: string; at: number}> = ({label, value, at}) => {
  const frame = useCurrentFrame();
  const chars = Math.max(
    0,
    Math.min(value.length, Math.round(interpolate(frame, [at, at + s(0.9)], [0, value.length], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
    })))
  );
  const typed = value.slice(0, chars);
  const caret = chars > 0 && chars < value.length;

  return (
    <>
      <div style={{fontSize: 16, color: colors.muted, margin: "18px 0 8px"}}>{label}</div>
      <div
        style={{
          border: `1px solid ${caret ? colors.accent : colors.line}`,
          background: colors.panel2,
          borderRadius: 8,
          padding: "12px 15px",
          fontFamily: fonts.mono,
          fontSize: 18,
          color: typed ? colors.text : colors.muted,
          minHeight: 48,
        }}
      >
        {typed || " "}
        {caret ? <span style={{color: colors.accent}}>▋</span> : null}
      </div>
    </>
  );
};

/* ================================================================= 5. enclave */

export const SceneEnclave: React.FC = () => {
  const frame = useCurrentFrame();
  const steps = [
    {at: s(1.6), label: "Recompute keccak256(ciphertext)", note: "must equal the hash Flare committed to"},
    {at: s(3.2), label: "Decrypt inside the TEE", note: "25 XRP → rPT1Sjq2…zbpAYe"},
    {at: s(4.8), label: "Check against the policy", note: "25 ≤ 100 per-payment cap"},
    {at: s(6.4), label: "Build a canonical XRPL Payment", note: "one transaction shape, nothing else"},
    {at: s(8.0), label: "Sign", note: "key never leaves this boundary"},
  ];

  return (
    <Slide eyebrow="Inside the enclave" title="It can sign exactly one kind of thing.">
      <div style={{display: "flex", gap: 54, maxWidth: 1440, alignItems: "flex-start"}}>
        <div style={{flex: 1}}>
          {steps.map((step) => {
            const done = frame > step.at + s(0.5);
            return (
              <Enter key={step.label} at={step.at}>
                <div style={{display: "flex", gap: 18, alignItems: "flex-start", marginBottom: 22}}>
                  <div
                    style={{
                      width: 24,
                      height: 24,
                      borderRadius: 999,
                      flex: "0 0 24px",
                      marginTop: 4,
                      border: `2px solid ${done ? chain.enclave : colors.line}`,
                      background: done ? chain.enclave : "transparent",
                    }}
                  />
                  <div>
                    <div style={{fontSize: 25, fontWeight: 620}}>{step.label}</div>
                    <div style={{fontSize: 19, color: colors.muted, fontFamily: fonts.mono, marginTop: 3}}>
                      {step.note}
                    </div>
                  </div>
                </div>
              </Enter>
            );
          })}
        </div>

        <Enter at={s(9.2)} style={{flex: "0 0 520px"}}>
          <div
            style={{
              background: colors.panel,
              border: `1px solid ${chain.enclave}55`,
              borderRadius: radius,
              padding: 28,
            }}
          >
            <div
              style={{
                fontSize: 15,
                letterSpacing: ".1em",
                textTransform: "uppercase",
                color: chain.enclave,
                fontWeight: 650,
                marginBottom: 16,
              }}
            >
              Cannot be expressed at all
            </div>
            {["TrustSet", "AccountSet (rekey)", "EscrowFinish", "Token transfer", "Arbitrary bytes"].map((x) => (
              <div
                key={x}
                style={{
                  fontFamily: fonts.mono,
                  fontSize: 19,
                  color: colors.muted,
                  padding: "7px 0",
                  textDecoration: "line-through",
                  textDecorationColor: colors.bad,
                }}
              >
                {x}
              </div>
            ))}
            <div style={{fontSize: 18, color: colors.muted, marginTop: 16, lineHeight: 1.5}}>
              The codec serialises a native-XRP <Mono>Payment</Mono> with one memo. There is no
              field table to extend.
            </div>
          </div>
        </Enter>
      </div>
    </Slide>
  );
};

/* ================================================================= 6. execute */

export const SceneExecute: React.FC = () => (
  <div style={{position: "absolute", inset: 0, display: "flex", alignItems: "center", padding: "0 130px 130px", gap: 56}}>
    <div style={{flex: "0 0 520px", fontFamily: fonts.sans, color: colors.text}}>
      <Enter at={0}>
        <div
          style={{
            fontSize: 19,
            letterSpacing: ".18em",
            textTransform: "uppercase",
            color: chain.xrpl,
            fontWeight: 650,
            marginBottom: 20,
          }}
        >
          Executed on XRPL Testnet
        </div>
      </Enter>
      <Enter at={s(0.3)}>
        <div style={{fontSize: 48, fontWeight: 680, letterSpacing: "-0.02em", lineHeight: 1.16, marginBottom: 26}}>
          A real payment, with the id predicted before it was sent.
        </div>
      </Enter>
      <Enter at={s(1.2)}>
        <div style={{fontSize: 21, color: colors.muted, lineHeight: 1.55}}>
          An XRPL transaction id is the hash of the signed blob, so the enclave knows it in
          advance. The relay cannot substitute a different payment — the contract only accepts
          the id the enclave already committed to.
        </div>
      </Enter>
    </div>

    <Enter at={s(0.7)} style={{flex: 1}}>
      <Chrome url={`${facts.xrplExplorer}/transactions/${facts.xrplTxId.slice(0, 16)}…`}>
        <div style={{padding: 32, background: colors.bg}}>
          <div style={{display: "flex", alignItems: "center", gap: 14, marginBottom: 22}}>
            <Pill tone="settled">tesSUCCESS</Pill>
            <span style={{color: colors.muted, fontSize: 17}}>Payment · validated</span>
          </div>
          <Card title="Transaction">
            <Row k="Hash" v={<span style={{fontSize: 15}}>{facts.xrplTxId.slice(0, 32)}…</span>} />
            <Row k="From" v={facts.treasuryXrpl} />
            <Row k="To" v={facts.payeeXrpl} />
            <Row k="Amount" v={`${demo.amountXrp} XRP`} />
            <Row k="Memo (hex)" v={<span style={{color: colors.ok}}>{demo.memoBytes}</span>} last />
          </Card>
          <Notice>
            The memo is <Mono color={colors.text}>"BSF1"</Mono> followed by this request's 32-byte
            reference — which is what lets the contract tell this payment apart from any other.
          </Notice>
        </div>
      </Chrome>
    </Enter>
  </div>
);

/* =================================================================== 7. proof */

export const SceneProof: React.FC = () => {
  const frame = useCurrentFrame();
  const conditions = [
    "FDC Merkle proof verifies",
    "Attestation type is XRPPayment",
    "Source network is testXRP",
    "XRPL status is success",
    "Funds left the treasury's own account",
    "They reached the authorised destination",
    "The amount matches exactly",
    "The memo binds it to this request",
  ];
  const revealStart = s(2.0);
  const per = s(0.42);

  return (
    <Slide eyebrow="Proof, not assertion" title="Eight conditions stand between a transaction and SETTLED.">
      <div style={{display: "flex", gap: 50, maxWidth: 1460, alignItems: "flex-start"}}>
        <div style={{flex: 1, display: "grid", gridTemplateColumns: "1fr 1fr", gap: "14px 28px"}}>
          {conditions.map((c, i) => {
            const at = revealStart + i * per;
            const on = frame > at;
            return (
              <Enter key={c} at={at}>
                <div style={{display: "flex", gap: 13, alignItems: "flex-start"}}>
                  {/* Drawn rather than typed: Chromium resolves "✓" through the
                      emoji font here, which renders a chunky coloured tile. */}
                  <div
                    style={{
                      width: 22,
                      height: 22,
                      borderRadius: 6,
                      flex: "0 0 22px",
                      marginTop: 3,
                      background: on ? colors.ok : "transparent",
                      border: `2px solid ${on ? colors.ok : colors.line}`,
                      display: "flex",
                      alignItems: "center",
                      justifyContent: "center",
                    }}
                  >
                    {on ? (
                      <svg width="13" height="13" viewBox="0 0 24 24" fill="none">
                        <path
                          d="M4.5 12.5 L9.5 17.5 L19.5 6.5"
                          stroke="#07231c"
                          strokeWidth="3.4"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        />
                      </svg>
                    ) : null}
                  </div>
                  <div style={{fontSize: 21, lineHeight: 1.35}}>{c}</div>
                </div>
              </Enter>
            );
          })}
        </div>

        <Enter at={s(6.2)} style={{flex: "0 0 470px"}}>
          <div
            style={{
              background: colors.panel,
              border: `1px solid ${colors.ok}55`,
              borderRadius: radius,
              padding: 28,
            }}
          >
            <div
              style={{
                fontSize: 15,
                letterSpacing: ".1em",
                textTransform: "uppercase",
                color: colors.ok,
                fontWeight: 650,
                marginBottom: 14,
              }}
            >
              And the ninth
            </div>
            <div style={{fontSize: 26, fontWeight: 640, lineHeight: 1.35, marginBottom: 14}}>
              This transaction id has never settled another request.
            </div>
            <div style={{fontSize: 19, color: colors.muted, lineHeight: 1.55}}>
              One XRPL payment settles at most one request, ever. Without it, a single real
              transfer could be replayed to clear an unlimited number of obligations.
            </div>
          </div>
        </Enter>
      </div>
    </Slide>
  );
};

/* =================================================================== 8. trust */

export const SceneTrust: React.FC = () => {
  const rows = [
    {who: "The enclave", can: "Sign a payment", cannot: "Mark anything settled"},
    {who: "The relay", can: "Deliver and broadcast", cannot: "Alter, create or authorise"},
    {who: "A compromised enclave", can: "Misbehave within limits", cannot: "Exceed the published caps"},
  ];

  return (
    <Slide eyebrow="Why this is not a signing service" title="Every actor is bounded by something other than trust.">
      <div style={{maxWidth: 1400}}>
        <div style={{display: "flex", padding: "0 26px 12px", fontSize: 16, letterSpacing: ".1em", textTransform: "uppercase", color: colors.muted, fontWeight: 650}}>
          <div style={{flex: "0 0 380px"}} />
          <div style={{flex: 1}}>Can</div>
          <div style={{flex: 1, color: colors.bad}}>Cannot</div>
        </div>
        {rows.map((r, i) => (
          <Enter key={r.who} at={s(0.9) + i * s(0.55)}>
            <div
              style={{
                display: "flex",
                alignItems: "center",
                background: colors.panel,
                border: `1px solid ${colors.line}`,
                borderRadius: radius,
                padding: "24px 26px",
                marginBottom: 14,
                fontSize: 23,
              }}
            >
              <div style={{flex: "0 0 380px", fontWeight: 650}}>{r.who}</div>
              <div style={{flex: 1, color: colors.muted}}>{r.can}</div>
              <div style={{flex: 1, color: colors.text}}>{r.cannot}</div>
            </div>
          </Enter>
        ))}

        <Enter at={s(3.4)}>
          <div style={{marginTop: 34, fontSize: 24, color: colors.muted, lineHeight: 1.55}}>
            The policy is re-checked on chain after the enclave has already enforced it. That
            second check is public, independent, and is what makes the published limits mean
            something even if the first one is lying.
          </div>
        </Enter>
      </div>
    </Slide>
  );
};

/* ==================================================================== 9. real */

export const SceneReal: React.FC = () => (
  <Slide eyebrow="Live on Coston2" title="Deployed, source-verified, and testable today.">
    <div style={{maxWidth: 1420}}>
      {[
        {name: "BridgeSafeController", addr: facts.controller},
        {name: "BridgeSafeFdcVerifier", addr: facts.verifier},
      ].map((c, i) => (
        <Enter key={c.name} at={s(0.8) + i * s(0.5)}>
          <div
            style={{
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              background: colors.panel,
              border: `1px solid ${colors.line}`,
              borderRadius: radius,
              padding: "22px 28px",
              marginBottom: 14,
            }}
          >
            <div style={{fontSize: 24, fontWeight: 640}}>{c.name}</div>
            <div style={{display: "flex", alignItems: "center", gap: 18}}>
              <Mono color={colors.accentSoft}>{c.addr}</Mono>
              <Pill tone="settled">verified</Pill>
            </div>
          </div>
        </Enter>
      ))}

      <Enter at={s(2.2)}>
        <div style={{display: "flex", gap: 16, marginTop: 30}}>
          {[
            {n: facts.tests.contracts, l: "contract tests", d: "44 of them negative"},
            {n: facts.tests.extension, l: "enclave tests", d: "incl. a live ledger run"},
            {n: facts.tests.services, l: "service tests", d: "transport and routing"},
          ].map((stat) => (
            <div
              key={stat.l}
              style={{
                flex: 1,
                background: colors.panel,
                border: `1px solid ${colors.line}`,
                borderRadius: radius,
                padding: "24px 26px",
              }}
            >
              <div style={{fontSize: 46, fontWeight: 700, color: colors.accent, lineHeight: 1}}>{stat.n}</div>
              <div style={{fontSize: 21, marginTop: 10}}>{stat.l}</div>
              <div style={{fontSize: 18, color: colors.muted, marginTop: 3}}>{stat.d}</div>
            </div>
          ))}
        </div>
      </Enter>

      <Enter at={s(3.6)}>
        <div style={{marginTop: 26, fontSize: 21, color: colors.muted, lineHeight: 1.55}}>
          The FDC anchor resolves through Flare's <Mono>ContractRegistry</Mono> at call time —
          confirmed live to return <Mono color={colors.text}>{facts.fdcVerification}</Mono> — so
          it cannot be repointed after deployment.
        </div>
      </Enter>
    </div>
  </Slide>
);

/* =================================================================== 10. close */

export const SceneClose: React.FC = () => {
  const frame = useCurrentFrame();
  const glow = interpolate(frame, [0, s(1.4)], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });

  return (
    <div
      style={{
        position: "absolute",
        inset: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        paddingBottom: 120,
        fontFamily: fonts.sans,
        color: colors.text,
      }}
    >
      <Enter at={0}>
        <div style={{fontSize: 78, fontWeight: 700, letterSpacing: "-0.03em"}}>
          BridgeSafe<span style={{color: colors.accent, opacity: glow}}>.</span>
        </div>
      </Enter>
      <Enter at={s(0.5)}>
        <div style={{fontSize: 33, color: colors.muted, marginTop: 18, textAlign: "center", maxWidth: 1050, lineHeight: 1.45}}>
          Flare as the control plane for assets that live somewhere else.
        </div>
      </Enter>
      <Enter at={s(1.6)}>
        <div style={{display: "flex", gap: 14, marginTop: 44}}>
          <Badge>Confidential Compute</Badge>
          <Badge>Interoperable Assets</Badge>
          <Badge>Coston2 · XRPL Testnet</Badge>
        </div>
      </Enter>
      <Enter at={s(2.4)}>
        <div style={{fontFamily: fonts.mono, fontSize: 21, color: colors.accentSoft, marginTop: 40}}>
          github.com/Marc-Dvci/BridgeSafe
        </div>
      </Enter>
    </div>
  );
};
