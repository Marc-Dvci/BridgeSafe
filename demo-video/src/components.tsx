import React from "react";
import {interpolate, useCurrentFrame} from "remotion";

import {colors, fonts, radius, shadow} from "./theme";
import {FPS} from "./story";

/* ------------------------------------------------------------------ motion */

/** Fade+rise, used for anything entering the frame. */
export const Enter: React.FC<{
  at: number;
  children: React.ReactNode;
  rise?: number;
  duration?: number;
  style?: React.CSSProperties;
}> = ({at, children, rise = 18, duration = 0.45, style}) => {
  const frame = useCurrentFrame();
  const end = at + duration * FPS;
  const t = interpolate(frame, [at, end], [0, 1], {
    extrapolateLeft: "clamp",
    extrapolateRight: "clamp",
  });
  const eased = 1 - Math.pow(1 - t, 3);
  return (
    <div
      style={{
        opacity: eased,
        transform: `translateY(${(1 - eased) * rise}px)`,
        ...style,
      }}
    >
      {children}
    </div>
  );
};

/** Holds children only inside [from, to) frames of the parent sequence. */
export const Window: React.FC<{
  from: number;
  to: number;
  children: React.ReactNode;
}> = ({from, to, children}) => {
  const frame = useCurrentFrame();
  if (frame < from || frame >= to) return null;
  return <>{children}</>;
};

/* ------------------------------------------------------------------ layout */

export const Slide: React.FC<{
  eyebrow?: string;
  title?: string;
  children?: React.ReactNode;
}> = ({eyebrow, title, children}) => (
  <div
    style={{
      position: "absolute",
      inset: 0,
      display: "flex",
      flexDirection: "column",
      justifyContent: "center",
      padding: "0 150px 150px",
      fontFamily: fonts.sans,
      color: colors.text,
    }}
  >
    {eyebrow ? (
      <Enter at={0}>
        <div
          style={{
            fontSize: 19,
            letterSpacing: ".18em",
            textTransform: "uppercase",
            color: colors.accent,
            fontWeight: 650,
            marginBottom: 22,
          }}
        >
          {eyebrow}
        </div>
      </Enter>
    ) : null}
    {title ? (
      <Enter at={4}>
        <div
          style={{
            fontSize: 62,
            lineHeight: 1.14,
            fontWeight: 680,
            letterSpacing: "-0.022em",
            maxWidth: 1380,
            marginBottom: 46,
          }}
        >
          {title}
        </div>
      </Enter>
    ) : null}
    {children}
  </div>
);

/* ----------------------------------------------------------------- console */

export const Card: React.FC<{
  title: string;
  children: React.ReactNode;
  style?: React.CSSProperties;
}> = ({title, children, style}) => (
  <div
    style={{
      background: colors.panel,
      border: `1px solid ${colors.line}`,
      borderRadius: radius,
      padding: 26,
      ...style,
    }}
  >
    <div
      style={{
        fontSize: 15,
        textTransform: "uppercase",
        letterSpacing: ".08em",
        color: colors.muted,
        fontWeight: 600,
        marginBottom: 20,
      }}
    >
      {title}
    </div>
    {children}
  </div>
);

export const Row: React.FC<{k: string; v: React.ReactNode; last?: boolean}> = ({
  k,
  v,
  last,
}) => (
  <div
    style={{
      display: "flex",
      justifyContent: "space-between",
      gap: 20,
      padding: "9px 0",
      borderBottom: last ? "none" : `1px solid rgba(34,48,74,.5)`,
    }}
  >
    <span style={{color: colors.muted, fontSize: 17, whiteSpace: "nowrap"}}>{k}</span>
    <span
      style={{
        fontFamily: fonts.mono,
        fontSize: 17,
        textAlign: "right",
        wordBreak: "break-all",
      }}
    >
      {v}
    </span>
  </div>
);

export const Pill: React.FC<{tone: "settled" | "progress" | "dead"; children: React.ReactNode}> = ({
  tone,
  children,
}) => {
  const tones = {
    settled: {bg: "rgba(53,208,165,.15)", fg: colors.ok},
    progress: {bg: "rgba(240,180,41,.15)", fg: colors.pending},
    dead: {bg: "rgba(255,92,122,.15)", fg: colors.bad},
  }[tone];
  return (
    <span
      style={{
        display: "inline-block",
        fontSize: 14,
        padding: "3px 11px",
        borderRadius: 999,
        fontWeight: 600,
        textTransform: "uppercase",
        letterSpacing: ".05em",
        background: tones.bg,
        color: tones.fg,
      }}
    >
      {children}
    </span>
  );
};

export const Notice: React.FC<{
  tone?: "plain" | "ok";
  children: React.ReactNode;
}> = ({tone = "plain", children}) => {
  const styles =
    tone === "ok"
      ? {border: "#1b4d3f", bg: "#0e2620", fg: "#9fe9d3"}
      : {border: colors.line, bg: colors.panel2, fg: colors.muted};
  return (
    <div
      style={{
        border: `1px solid ${styles.border}`,
        background: styles.bg,
        color: styles.fg,
        borderRadius: 8,
        padding: "14px 17px",
        fontSize: 16.5,
        lineHeight: 1.5,
        marginTop: 18,
      }}
    >
      {children}
    </div>
  );
};

/** The browser frame the console sits in. */
export const Chrome: React.FC<{url: string; children: React.ReactNode; style?: React.CSSProperties}> = ({
  url,
  children,
  style,
}) => (
  <div
    style={{
      borderRadius: 14,
      overflow: "hidden",
      border: `1px solid ${colors.line}`,
      boxShadow: shadow,
      background: colors.bg,
      ...style,
    }}
  >
    <div
      style={{
        display: "flex",
        alignItems: "center",
        gap: 12,
        padding: "13px 18px",
        background: "#0d1220",
        borderBottom: `1px solid ${colors.line}`,
      }}
    >
      {["#ff5f57", "#febc2e", "#28c840"].map((c) => (
        <div key={c} style={{width: 12, height: 12, borderRadius: 999, background: c}} />
      ))}
      <div
        style={{
          marginLeft: 12,
          flex: 1,
          background: colors.bg,
          border: `1px solid ${colors.line}`,
          borderRadius: 7,
          padding: "5px 14px",
          fontFamily: fonts.mono,
          fontSize: 15,
          color: colors.muted,
        }}
      >
        {url}
      </div>
    </div>
    {children}
  </div>
);

/** Product header, as rendered by apps/web. */
export const ConsoleHeader: React.FC = () => (
  <div
    style={{
      display: "flex",
      alignItems: "center",
      justifyContent: "space-between",
      marginBottom: 22,
    }}
  >
    <div style={{display: "flex", alignItems: "baseline", gap: 14}}>
      <div style={{fontSize: 27, fontWeight: 680, letterSpacing: "-0.01em"}}>
        BridgeSafe<span style={{color: colors.accent}}>.</span>
      </div>
      <Badge>Coston2 · XRPL Testnet</Badge>
    </div>
    <Badge>0xd02Be5…F2fBA</Badge>
  </div>
);

export const Badge: React.FC<{children: React.ReactNode}> = ({children}) => (
  <span
    style={{
      fontSize: 14,
      textTransform: "uppercase",
      letterSpacing: ".08em",
      padding: "4px 11px",
      borderRadius: 999,
      border: `1px solid ${colors.line}`,
      color: colors.muted,
      background: colors.panel,
    }}
  >
    {children}
  </span>
);

export const Mono: React.FC<{children: React.ReactNode; color?: string}> = ({children, color}) => (
  <span style={{fontFamily: fonts.mono, color: color ?? colors.text}}>{children}</span>
);
