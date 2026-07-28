/**
 * Lifted verbatim from apps/web/app/globals.css.
 *
 * The console shots in this video are rebuilt in Remotion rather than screen-
 * recorded, so they have to be the same product rather than a lookalike. Keeping
 * the tokens identical is what makes that true — if the app's palette changes,
 * these are the values to change with it.
 */

export const colors = {
  bg: "#0b0f17",
  panel: "#121826",
  panel2: "#172033",
  line: "#22304a",
  text: "#e8edf7",
  muted: "#8ea0bf",
  accent: "#ff6a3d", // Flare orange
  accentSoft: "#ff8f6b",
  ok: "#35d0a5",
  pending: "#f0b429",
  bad: "#ff5c7a",
} as const;

export const fonts = {
  sans: '-apple-system, BlinkMacSystemFont, "Segoe UI", Inter, Roboto, sans-serif',
  mono: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
} as const;

export const radius = 12;

export const shadow = "0 40px 110px rgba(0,0,0,.55), 0 2px 14px rgba(0,0,0,.4)";

/** Chain identity colours, used only to keep the two networks visually distinct. */
export const chain = {
  flare: colors.accent,
  xrpl: "#5ea9ff",
  enclave: "#b49cf8",
} as const;
