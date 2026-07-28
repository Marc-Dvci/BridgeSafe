/**
 * Render the submission video.
 *
 *   npx tsx scripts/render.ts
 *
 * Runs the self-test first. A Remotion render takes minutes and succeeds even
 * when the narration is missing or misaligned, so the cheap checks go first.
 */

import {execFileSync} from "node:child_process";
import {existsSync, mkdirSync, statSync} from "node:fs";
import {dirname, join} from "node:path";
import {fileURLToPath} from "node:url";

const ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");
const OUT = join(ROOT, "output", "bridgesafe-demo.mp4");

const run = (command: string, args: string[]) =>
  execFileSync(command, args, {cwd: ROOT, stdio: "inherit", shell: process.platform === "win32"});

console.log("\n→ self-test");
run("npx", ["tsx", "scripts/self-test.ts"]);

console.log("\n→ render");
mkdirSync(dirname(OUT), {recursive: true});
run("npx", [
  "remotion",
  "render",
  "src/index.ts",
  "BridgeSafeDemo",
  OUT,
  "--codec=h264",
  "--crf=17",
  "--jpeg-quality=95",
  "--concurrency=4",
]);

if (!existsSync(OUT)) {
  console.error("\n✖ render reported success but produced no file");
  process.exit(1);
}

const mb = statSync(OUT).size / (1024 * 1024);
console.log(`\n  ✔ ${OUT}`);
console.log(`  ✔ ${mb.toFixed(1)} MB\n`);
