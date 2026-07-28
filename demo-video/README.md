# Demo video

A scripted 3:10 walkthrough, rendered with [Remotion](https://remotion.dev) and
narrated with [edge-tts](https://github.com/rany2/edge-tts). No screen recording,
no manual editing — `npm run render` reproduces the exact submission file.

```bash
npm install
npm run narrate    # edge-tts → public/audio/*.mp3, measures them, writes cues
npm run check      # typecheck + self-test
npm run render     # → output/bridgesafe-demo.mp4
```

## Why it is built this way

**The console shots are rebuilt, not recorded.** `src/theme.ts` is lifted verbatim
from `apps/web/app/globals.css`, so the panels in the video are the product's own
palette, spacing and type rather than a lookalike. Rebuilding also means the
frames stay sharp at 1080p and the whole thing re-renders when a number changes.

**Scene timing is derived from the audio, not guessed.**
`scripts/generate-narration.py` renders each block, measures it with `ffprobe`,
and writes real durations and caption cues into `src/generated-narration.json`.
The composition keys scenes to narration ids, so rewriting a line changes its
duration and the visuals follow automatically. The generator refuses to write
output if two blocks would overlap or if the last one runs past the composition —
mistakes that are otherwise invisible until you watch the render and hear two
voices at once.

**Everything on screen is real.** The contract addresses are the deployed,
source-verified Coston2 contracts. The XRPL transaction is a validated testnet
payment produced by `TestLive_PaymentIsAcceptedByTheLedger`, memo and all. Anyone
watching can open an explorer and check.

## Layout

```
src/narration-source.json    the script — edit this, then re-run narrate
src/generated-narration.json measured durations and caption cues (generated)
src/story.ts                 composition constants and the on-chain facts
src/theme.ts                 product palette, mirrored from apps/web
src/scenes.tsx               the ten scenes
src/components.tsx           console chrome, cards, captions, motion helpers
scripts/generate-narration.py  edge-tts + ffprobe
scripts/self-test.ts         fails fast on the things a render would not catch
scripts/render.ts            self-test, then render
```

## Editing the script

Change the text in `src/narration-source.json`, then:

```bash
npm run narrate
```

If a block grows past its slot the generator tells you which one and by how much;
adjust the `start` values and re-run. `npm run check` will then confirm no scene
is left holding a frame with nothing being said over it.

Requirements: Node 20+, Python with `edge_tts`, and `ffmpeg`/`ffprobe` on PATH.
