/**
 * Checks the things a render would only reveal after several minutes of work.
 *
 *   npx tsx scripts/self-test.ts
 *
 * A Remotion render is slow and mostly succeeds even when the result is wrong —
 * a missing mp3 renders as silence, a scene with no narration renders as a held
 * frame, and neither throws. These assertions fail fast instead.
 */

import {existsSync, readFileSync} from "node:fs";
import {dirname, join} from "node:path";
import {fileURLToPath} from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

type Block = {
  id: string;
  file: string;
  start: number;
  duration: number;
  captions: string[];
  captionCues: {start: number; end: number}[];
};

const FPS = 30;
const DURATION_SECONDS = 190;
const MIN_GAP = 0.2;

const failures: string[] = [];
const fail = (message: string) => failures.push(message);

const narrationPath = join(ROOT, "src", "generated-narration.json");
if (!existsSync(narrationPath)) {
  console.error("✖ src/generated-narration.json is missing — run: npm run narrate");
  process.exit(1);
}
const blocks: Block[] = JSON.parse(readFileSync(narrationPath, "utf8"));

/* Every scene id in the composition must have narration, and vice versa. */
const sceneIds = [
  "problem", "pitch", "policy", "seal", "enclave",
  "execute", "proof", "trust", "real", "close",
];
for (const id of sceneIds) {
  if (!blocks.some((b) => b.id === id)) fail(`scene "${id}" has no narration block`);
}
for (const block of blocks) {
  if (!sceneIds.includes(block.id)) fail(`narration block "${block.id}" has no scene`);
}

/* The audio has to exist, or the render is silent and still "succeeds". */
for (const block of blocks) {
  const audio = join(ROOT, "public", block.file);
  if (!existsSync(audio)) fail(`${block.id}: missing audio at public/${block.file}`);
}

/* Captions and cues must correspond one to one. */
for (const block of blocks) {
  if (block.captions.length !== block.captionCues.length) {
    fail(
      `${block.id}: ${block.captions.length} captions but ${block.captionCues.length} cues`
    );
  }
  const last = block.captionCues.at(-1);
  if (last && Math.abs(last.end - block.duration) > 0.05) {
    fail(`${block.id}: caption cues end at ${last.end}s but audio is ${block.duration}s`);
  }
}

/* No two voice tracks may play at once. */
for (let i = 0; i < blocks.length - 1; i++) {
  const end = blocks[i].start + blocks[i].duration;
  const next = blocks[i + 1].start;
  if (end + MIN_GAP > next) {
    fail(
      `${blocks[i].id} overlaps ${blocks[i + 1].id} ` +
        `(ends ${end.toFixed(2)}s, next starts ${next.toFixed(2)}s)`
    );
  }
}

/* Nothing may run past the composition. */
const tail = blocks.at(-1)!;
const tailEnd = tail.start + tail.duration;
if (tailEnd > DURATION_SECONDS - 1) {
  fail(`${tail.id} ends at ${tailEnd.toFixed(2)}s, past the ${DURATION_SECONDS}s composition`);
}

/* A scene that outlasts its narration by a lot is a held frame nobody wants. */
for (let i = 0; i < blocks.length; i++) {
  const start = blocks[i].start;
  const nextStart = blocks[i + 1]?.start ?? DURATION_SECONDS;
  const sceneLength = nextStart - start;
  const spoken = blocks[i].duration;
  if (sceneLength - spoken > 6) {
    fail(
      `${blocks[i].id}: scene runs ${sceneLength.toFixed(1)}s but narration is only ` +
        `${spoken.toFixed(1)}s — ${(sceneLength - spoken).toFixed(1)}s of held frame`
    );
  }
}

/* Frame arithmetic must land on whole frames. */
if (!Number.isInteger(DURATION_SECONDS * FPS)) {
  fail(`composition is ${DURATION_SECONDS}s x ${FPS}fps, which is not a whole frame count`);
}

if (failures.length > 0) {
  console.error("\nSelf-test failed:\n");
  for (const f of failures) console.error(`  ✖ ${f}`);
  console.error("");
  process.exit(1);
}

const spokenTotal = blocks.reduce((sum, b) => sum + b.duration, 0);
console.log(`\n  ✔ ${blocks.length} scenes, all narrated and cued`);
console.log(`  ✔ ${spokenTotal.toFixed(1)}s of speech across a ${DURATION_SECONDS}s composition`);
console.log(`  ✔ no overlaps, no orphan scenes, no missing audio\n`);
