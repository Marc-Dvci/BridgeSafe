import React from "react";
import {AbsoluteFill, Audio, Sequence, interpolate, staticFile, useCurrentFrame} from "remotion";

import narration from "./generated-narration.json";
import {
  SceneClose,
  SceneEnclave,
  SceneExecute,
  SceneLoop,
  ScenePolicy,
  SceneProblem,
  SceneProof,
  SceneReal,
  SceneSeal,
  SceneTrust,
} from "./scenes";
import {colors, fonts} from "./theme";
import {DURATION_IN_FRAMES, FPS, sec} from "./story";

type Block = {
  id: string;
  file: string;
  start: number;
  duration: number;
  captions: string[];
  captionCues: {start: number; end: number}[];
};

const blocks = narration as Block[];

/**
 * Scenes are keyed to narration ids, not to hardcoded frames.
 *
 * generate-narration.py measures the real mp3s, so a scene's start comes from
 * the audio that actually exists. Rewriting a line changes its duration; the
 * visuals follow automatically instead of drifting out of sync.
 */
const SCENES: Record<string, React.FC> = {
  problem: SceneProblem,
  pitch: SceneLoop,
  policy: ScenePolicy,
  seal: SceneSeal,
  enclave: SceneEnclave,
  execute: SceneExecute,
  proof: SceneProof,
  trust: SceneTrust,
  real: SceneReal,
  close: SceneClose,
};

/** A scene runs from its own narration start until the next one begins. */
function sceneWindows() {
  return blocks.map((block, index) => {
    const next = blocks[index + 1];
    const from = index === 0 ? 0 : sec(block.start - 0.9);
    const until = next ? sec(next.start - 0.9) : DURATION_IN_FRAMES;
    return {id: block.id, from, durationInFrames: Math.max(1, until - from)};
  });
}

const Backdrop: React.FC = () => (
  <AbsoluteFill style={{background: colors.bg}}>
    {/* A single warm source, low and left, so full-bleed slides are not flat. */}
    <AbsoluteFill
      style={{
        background: `radial-gradient(1200px 700px at 12% 108%, ${colors.accent}14, transparent 62%),
                     radial-gradient(900px 600px at 92% -8%, #5ea9ff0e, transparent 60%)`,
      }}
    />
  </AbsoluteFill>
);

const Captions: React.FC = () => {
  const frame = useCurrentFrame();
  const t = frame / FPS;

  const block = blocks.find((b) => t >= b.start && t < b.start + b.duration);
  if (!block) return null;

  const local = t - block.start;
  const cueIndex = block.captionCues.findIndex((c) => local >= c.start && local < c.end);
  if (cueIndex === -1) return null;

  const cue = block.captionCues[cueIndex];
  const fade = 0.16;
  const opacity = Math.min(
    interpolate(local, [cue.start, cue.start + fade], [0, 1], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
    }),
    interpolate(local, [cue.end - fade, cue.end], [1, 0], {
      extrapolateLeft: "clamp",
      extrapolateRight: "clamp",
    })
  );

  return (
    <AbsoluteFill style={{justifyContent: "flex-end", alignItems: "center", paddingBottom: 62}}>
      <div
        style={{
          opacity,
          maxWidth: 1500,
          textAlign: "center",
          fontFamily: fonts.sans,
          fontSize: 30,
          lineHeight: 1.4,
          fontWeight: 520,
          color: colors.text,
          background: "rgba(8,11,18,.82)",
          border: `1px solid ${colors.line}`,
          borderRadius: 12,
          padding: "16px 30px",
          backdropFilter: "blur(6px)",
        }}
      >
        {block.captions[cueIndex]}
      </div>
    </AbsoluteFill>
  );
};

/** Brief dip to black between scenes, so cuts read as deliberate. */
const SceneTransitions: React.FC = () => {
  const frame = useCurrentFrame();
  const windows = sceneWindows();

  let dim = 0;
  for (const w of windows.slice(1)) {
    const d = Math.abs(frame - w.from);
    if (d < 5) dim = Math.max(dim, 1 - d / 5);
  }
  if (dim === 0) return null;

  return <AbsoluteFill style={{background: "#000", opacity: dim * 0.55, pointerEvents: "none"}} />;
};

export const BridgeSafeDemo: React.FC = () => {
  const windows = sceneWindows();

  return (
    // Font and colour belong at the root, not per scene. Remotion renders in a
    // bare Chromium page with no stylesheet, so anything that does not inherit
    // them falls back to black Times — which is invisible against the panels and
    // was silently affecting every nested console component.
    <AbsoluteFill
      style={{
        background: colors.bg,
        fontFamily: fonts.sans,
        color: colors.text,
      }}
    >
      <Backdrop />

      {windows.map((w) => {
        const Scene = SCENES[w.id];
        if (!Scene) return null;
        return (
          <Sequence key={w.id} from={w.from} durationInFrames={w.durationInFrames} name={w.id}>
            <Scene />
          </Sequence>
        );
      })}

      <SceneTransitions />
      <Captions />

      {blocks.map((block) => (
        <Sequence key={block.id} from={sec(block.start)} durationInFrames={sec(block.duration) + 2} name={`vo-${block.id}`}>
          <Audio src={staticFile(block.file)} />
        </Sequence>
      ))}
    </AbsoluteFill>
  );
};
