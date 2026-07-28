// Verify the console's hand-written ABI still matches the compiled contract.
//
//   node --experimental-strip-types scripts/check-web-abi.ts
//
// apps/web/lib/bridgesafe.ts declares only the fragments the UI needs, rather
// than importing a generated artifact, so the bundle stays small and the console
// can be read without a build step. The cost is a seam that fails silently in
// the worst way: change a struct in Solidity and the UI keeps compiling, keeps
// type-checking, and then throws at runtime the first time a judge opens it —
// or, if a field is merely reordered, renders confidently wrong values.
//
// contracts/test/CrossLanguage.t.sol pins the same class of seam between Go and
// Solidity. This is its counterpart for TypeScript.
//
// Run after `forge build`. scripts/preflight.sh does both.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { controllerAbi } from "../apps/web/lib/bridgesafe.ts";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const ARTIFACT = join(
  root,
  "contracts/out/BridgeSafeController.sol/BridgeSafeController.json",
);

type AbiParam = {
  name?: string;
  type: string;
  components?: readonly AbiParam[];
};
type AbiEntry = {
  type: string;
  name?: string;
  stateMutability?: string;
  inputs?: readonly AbiParam[];
  outputs?: readonly AbiParam[];
};

const RED = "\x1b[0;31m";
const GRN = "\x1b[0;32m";
const NC = "\x1b[0m";

let artifact: { abi: AbiEntry[] };
try {
  artifact = JSON.parse(readFileSync(ARTIFACT, "utf8"));
} catch {
  console.error(
    `${RED}✖ no compiled artifact at ${ARTIFACT}${NC}\n    run: cd contracts && forge build`,
  );
  process.exit(1);
}

/** Canonical signature, e.g. `getTreasury(uint256)`. */
function signature(e: AbiEntry): string {
  return `${e.name}(${(e.inputs ?? []).map(flatten).join(",")})`;
}

/** Solidity type of a parameter, with tuples expanded to their components. */
function flatten(p: AbiParam): string {
  if (!p.type.startsWith("tuple")) return p.type;
  const inner = (p.components ?? []).map(flatten).join(",");
  return `(${inner})${p.type.slice("tuple".length)}`;
}

/** Full shape including parameter names, which is what the UI destructures by. */
function shape(params: readonly AbiParam[] = []): string {
  return params
    .map((p) =>
      p.type.startsWith("tuple")
        ? `${p.name ?? ""}:(${shape(p.components)})${p.type.slice("tuple".length)}`
        : `${p.name ?? ""}:${p.type}`,
    )
    .join(",");
}

const onChain = new Map<string, AbiEntry>();
for (const e of artifact.abi) {
  if (e.type === "function" || e.type === "event" || e.type === "error") {
    onChain.set(`${e.type}:${signature(e)}`, e);
  }
}

const problems: string[] = [];
let checked = 0;

for (const declared of controllerAbi as readonly AbiEntry[]) {
  if (!["function", "event", "error"].includes(declared.type)) continue;
  checked++;

  const key = `${declared.type}:${signature(declared)}`;
  const actual = onChain.get(key);

  if (!actual) {
    problems.push(
      `${declared.type} ${signature(declared)}\n    declared by the console, absent from the compiled contract`,
    );
    continue;
  }

  const wantIn = shape(actual.inputs);
  const gotIn = shape(declared.inputs);
  if (wantIn !== gotIn) {
    problems.push(
      `${signature(declared)} inputs differ\n    contract: ${wantIn}\n    console:  ${gotIn}`,
    );
  }

  const wantOut = shape(actual.outputs);
  const gotOut = shape(declared.outputs);
  if (wantOut !== gotOut) {
    problems.push(
      `${signature(declared)} outputs differ\n    contract: ${wantOut}\n    console:  ${gotOut}`,
    );
  }

  if (
    declared.stateMutability &&
    actual.stateMutability &&
    declared.stateMutability !== actual.stateMutability
  ) {
    problems.push(
      `${signature(declared)} mutability differs\n    contract: ${actual.stateMutability}\n    console:  ${declared.stateMutability}`,
    );
  }
}

console.log();
if (problems.length > 0) {
  for (const p of problems) console.error(`${RED}✖ ${p}${NC}`);
  console.error(
    `\n${RED}${problems.length} mismatch(es) between apps/web/lib/bridgesafe.ts and BridgeSafeController.${NC}`,
  );
  process.exit(1);
}

console.log(
  `${GRN}✔ console ABI matches the compiled contract (${checked} fragment(s))${NC}`,
);
