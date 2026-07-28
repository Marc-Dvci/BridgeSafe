# 🚀 TEE Extension Deployment — Step by Step

Linear recipe to deploy a TEE extension to Flare Coston or Coston2. Run the steps top to bottom.

> [!NOTE]
> **Two deployment modes.** The main path (Steps 1–9) deploys to a real **GCP
> Confidential Space VM** — production attestation (`SIMULATED_TEE=false`, real
> `codeHash`), proxy hosted by devops. For development you can instead run the TEE
> and proxy as **local Docker containers** with a **simulated** TEE
> (`SIMULATED_TEE=true`, `MODE=1`) exposed via **ngrok** — no VM and no devops
> hand-off. Steps 1–4 and 8–9 are identical; only Steps 5–7 change. See
> [Local / simulated deployment](#local--simulated-deployment-docker--ngrok).

## Prerequisites

- 🐳 Docker Desktop (Linux containers)
- 🐹 Go 1.25.1+
- 🔨 Foundry (`forge`, `cast`)
- `jq` and `curl`
- Bash (Git Bash on Windows works)
- **Abelium VPN** — required **only when deploying against Coston** (Coston's indexer DB is VPN-gated). **Coston2's** indexer is reachable without VPN, so for a Coston2 deploy you don't need it. The `ext-proxy` queries the indexer in both deployment modes (deployed and local); host + read-only creds are in [Indexer DB credentials](#indexer-db-credentials).
- **ngrok** — only for the **local / simulated** flow ([Local / simulated deployment](#local--simulated-deployment-docker--ngrok)). A free account is enough; its reserved domain gives a stable public URL for your local proxy.
- A **GCP Confidential Space VM** (or a devops contact to hand the image off to) — only for the **deployed** flow ([Step 6](#6-deploy-the-image-on-a-confidential-space-vm)).

## Indexer DB credentials

Both flows run the same `ext-proxy`, and the proxy queries Flare's **indexer DB**
to find TEE events and instruction responses — so you need these creds in either
flow (deployed and local). The proxy reads them from
`config/proxy/extension_proxy.<chain>.docker.toml`, which the chain's compose
overlay mounts as the container's `config.toml`. Those files are **gitignored**
(they hold DB creds), so create the one for your chain and fill its `[db]` block.
The creds are **chain-specific** (read-only, throwaway hackathon creds — fine to
commit/use as-is) — **don't cross them between chains**.

### Coston2

Reachable **without VPN**. Copy the bundled example, then confirm its `[db]` and
`[addresses]` blocks:

```bash
cp config/proxy/extension_proxy.coston2.docker.toml.example \
   config/proxy/extension_proxy.coston2.docker.toml
```

```toml
[db]
host     = "34.38.42.208"
port     = 3306
database = "indexer"
username = ""
password = ""
log_queries = false

[addresses]
flare_systems_manager = "0xA90Db6D10F856799b10ef2A77EBCbF460aC71e52"
relay                 = "0xa10B672D1c62e5457b17af63d4302add6A99d7dE"
voter_registry        = "0x6a0AF07b7972177B176d3D422555cbc98DfDe914"
```

### Coston

Requires **Abelium VPN** — this indexer host is VPN-gated. There's no bundled
example for Coston, so create `extension_proxy.coston.docker.toml` from the Coston2
one, set `chain_id = 16`, and use this `[db]` and `[addresses]`:

```toml
[db]
host     = "35.241.249.150"
port     = 3306
database = "indexer"
username = ""
password = ""
log_queries = false

[addresses]
flare_systems_manager = "0x85680Dd93755Fe5d0789773fd0896cEE51F9e358"
relay                 = "0x051f214D346Cfd97B107BECb87E2B35D1b4287E9"
voter_registry        = "0x42F4526BFC6f892DB515a832a52eFc9edFADf6c0"
```

**Only the Coston indexer (`35.241.249.150`) is VPN-gated** — reaching it needs
**Abelium VPN**. The **Coston2** indexer (`34.38.42.208`) is reachable without VPN.
If the `[db]` block is missing or wrong (or you can't reach the host), the proxy
can't read the chain indexer and `test.sh` fails the round-trip (the proxy never
sees the instruction responses).

## 1. Repository layout & dependencies

No sibling repos are required for the Go path — not for building the image, and
not for the deploy/registration/test tooling. Everything resolves `tee-node`
and `tee-proxy` from the public `github.com/flare-foundation` repos:

- **Extension image** (`Dockerfile`) and **deploy tooling** (`go/tools/`, used by
  `pre-build.sh` / `post-build.sh` / `test.sh` / `check-tee-state`) pull them as
  **Go modules** through `GOPROXY` (`proxy.golang.org`, falling back to `direct`),
  pinned by hash in `go/go.sum` (`tee-node v0.0.20`) and `go/tools/go.sum`
  (`tee-node v0.0.20` + `tee-proxy v0.0.17`). No `replace` directives, nothing
  copied from disk. (Versions here are illustrative — `go.sum` is the
  authoritative pin and will move ahead of this doc on the next bump.)
- **Proxy image** (`proxy/Dockerfile`) `git clone`s `tee-proxy` + `tee-node`
  straight from GitHub at build time (override the refs with the
  `TEE_PROXY_VERSION` / `TEE_NODE_VERSION` build args).

So a flat checkout of just your extension is enough:

```text
<workspace>/
└── extensions/
    └── <your-extension>/
```

> [!TIP]
> You **can** instead clone the two dependency repos locally and point the
> builds/tooling at them:
>
> ```bash
> git clone https://github.com/flare-foundation/tee-node.git
> git clone https://github.com/flare-foundation/tee-proxy.git
> ```
>
> But this is the **less ideal** option — you'd then have to keep pulling their
> changes to stay up to date, and you lose the hash-pinned reproducibility the
> default flow gives you for free. Prefer the clone-free path above unless you're
> actively developing against `tee-node` / `tee-proxy`.

> [!NOTE]
> All three languages are sibling-free. The **Python** and **TypeScript** image
> builds also `git clone` `tee-node` from GitHub at build time (no local
> checkout) and build from this same dir — `start-services.sh` just points
> `EXTENSION_DOCKERFILE` at the right one per `LANGUAGE`. The only caveat is
> reproducibility: the Python/TS images reach same-machine determinism but not
> cross-machine bit-for-bit parity — see [`REPRODUCIBILITY.md`](REPRODUCIBILITY.md).

## 2. Generate a funded deployer key

```bash
cast wallet new
cast wallet address --private-key 0x<private-key>
```

The derived address becomes your `INITIAL_OWNER`. Fund it from the target chain's faucet.

| Chain   | Faucet                                 |
| ------- | -------------------------------------- |
| Coston  | `https://faucet.flare.network/coston`  |
| Coston2 | `https://faucet.flare.network/coston2` |

## 3. Create `.env.<chain>`

Copy `.env.example` to `.env.coston` or `.env.coston2` in the root folder:

```bash
cp .env.example .env.coston2   # or: cp .env.example .env.coston
```

The template carries the full variable set with inline docs. Edit the values for
the target chain — the ones that matter for Coston/Coston2:

```bash
CHAIN=coston2                                                         # or coston
CHAIN_URL=https://coston2-api.flare.network/ext/C/rpc                 # chain RPC
ADDRESSES_FILE=./config/coston2/deployed-addresses.json
NORMAL_PROXY_URL=https://tee-proxy-coston2-1.flare.rocks              # FTDC proxy
EXT_PROXY_URL=                                                        # leave empty — set in Step 6

LOCAL_MODE=false
SIMULATED_TEE=false
DEPLOYMENT_PRIVATE_KEY=<private key, no 0x prefix>
INITIAL_OWNER=0x<derived address from Step 2>

LANGUAGE=go                                                          # go | python | typescript (which Dockerfile build-image.sh builds)
```

Activate it (optionally selecting the extension language):

```bash
bash ./scripts/use-chain.sh <chain> [go|python|typescript]         # deployed: coston | coston2
bash ./scripts/use-chain.sh local <chain> [go|python|typescript]   # local/simulated (Docker + ngrok)
bash ./scripts/use-chain.sh --list                                 # list templates + languages
bash ./scripts/use-chain.sh --help                                 # usage
```

> [!TIP]
> The `local` variant copies `.env.local.<chain>` instead of `.env.<chain>` — see
> [Local / simulated deployment](#local--simulated-deployment-docker--ngrok). For the
> normal deployed path, omit it.

Copies `.env.<chain>` → `.env` (and sets `LANGUAGE` if you passed one), which all
scripts auto-load. The language you pick here is what `build-image.sh` builds in [Step 5](#5-build-the-docker-image).

## 4. Register the extension on-chain

```bash
bash ./scripts/pre-build.sh
```

Generates Go bindings, compiles Solidity, deploys `InstructionSender`, and registers the extension on-chain. Writes `EXTENSION_ID` and `INSTRUCTION_SENDER` to `config/extension.env`.

On success it prints a `Pre-build complete` banner listing `EXTENSION_ID`, `INSTRUCTION_SENDER`, and the config-file path.

> [!IMPORTANT]
> Each run mints a **new** extension + InstructionSender and overwrites `config/extension.env`, so the script refuses to run if that file already exists.
>
> - **First deploy:** run the command above as-is.
> - **Want a new extension + InstructionSender** (e.g. after a diamond redeploy): opt in with `--force`:
>   ```bash
>   bash ./scripts/pre-build.sh --force   # or: PRE_BUILD_FORCE=1 bash ./scripts/pre-build.sh
>   ```
>
> Forcing a re-run against an existing TEE on a shared proxy orphans it, and `test.sh` later reverts with `MachineManager.TooMany()`. See [Troubleshooting](#troubleshooting) to recover.

Read the new values — `EXTENSION_ID` is part of the hand-off in [Step 6](#6-deploy-the-image-on-a-confidential-space-vm):

```bash
cat config/extension.env
```

## 5. Build the Docker image

`build-image.sh` builds the image for the language in your `.env` (set by
`use-chain.sh` in [Step 3](#3-create-envchain)), pins `SOURCE_DATE_EPOCH` for a reproducible
`codeHash`, verifies `MODE=0` is baked in (production attestation — FTDC rejects
`MODE=1`), and saves a tar for the hand-off:

```bash
bash ./scripts/build-image.sh                 # build .env's LANGUAGE, tag v0.1.0, save tar
bash ./scripts/build-image.sh -l typescript   # override the language
bash ./scripts/build-image.sh -v v0.1.1       # set the version/tag
```

It writes `sign-extension-<language>-<version>.tar` and prints the image ID to hand to devops.

<details>
<summary>Equivalent manual commands</summary>

```bash
export SOURCE_DATE_EPOCH=$(git log -1 --format=%ct)
docker build -f typescript/Dockerfile -t sign-extension-ts:v0.1.0 .   # or Dockerfile / python/Dockerfile
docker save sign-extension-ts:v0.1.0 -o sign-extension-ts-v0.1.0.tar
```

`docker build -t` names + tags in one step (no separate `docker tag`), and
BuildKit auto-reads `SOURCE_DATE_EPOCH` from the env. Confirm `MODE=0` with:

```bash
docker inspect sign-extension-ts:v0.1.0 --format '{{range .Config.Env}}{{println .}}{{end}}' | grep MODE
# expected: MODE=0
```

</details>

## 6. Deploy the image on a Confidential Space VM

Hand off (or deploy yourself) to a GCP Confidential Space VM with:

- The image (tar or registry URL+tag)
- Workload-launch env: `INITIAL_OWNER`, `CHAIN_URL`, `EXTENSION_ID` (from [Step 4](#4-register-the-extension-on-chain)), `PROXY_URL` (proxy URL reachable from the TEE)
- Public HTTPS routed to port `6664` of the proxy container

> [!NOTE]
> However the proxy is run, it needs the indexer DB credentials to serve `/info`
> and process instructions — see [Indexer DB credentials](#indexer-db-credentials).

You receive back the **public proxy URL** — which you only learn now, after the
hand-off. Put it in your `.env.<chain>` template, then re-run `use-chain.sh` so
the active `.env` picks it up. (`post-build.sh` / `test.sh` read `EXT_PROXY_URL`
from `.env`; the convention is to edit the chain template, never `.env`
directly.)

```bash
# in .env.<chain>
EXT_PROXY_URL=<public proxy URL>
```

```bash
bash ./scripts/use-chain.sh <chain>   # re-copies .env.<chain> → .env, now with EXT_PROXY_URL set
```

## 7. Verify the proxy `/info`

```bash
curl -s "$EXT_PROXY_URL/info" | jq '.machineData'
```

Required values:

| Field          | Expected                                                      |
| -------------- | ------------------------------------------------------------- |
| `platform`     | starts with `0x4743505f414d445f534556…` (GCP_AMD_SEV)         |
| `codeHash`     | real measured hash (**not** `0x194844cf…` — that's simulated) |
| `extensionId`  | matches your `config/extension.env` `EXTENSION_ID`            |
| `initialOwner` | matches your `INITIAL_OWNER`                                  |

If `extensionId` is wrong, ask the VM operator to restart the container with the correct `EXTENSION_ID` env override (no image rebuild needed — it's a launch-policy override).

## 8. Register the TEE machine

> [!NOTE]
> `post-build.sh` already invokes `register-tee` with `-command rRap` (not the default `rap`) — this is a load-bearing detail; don't revert it. Step `a` (availability check) needs a one-time **challenge** — a random number from the contract that the TEE signs to prove it's alive. By default only `r` issues it, but `r` skips itself once the TEE is registered on-chain. So re-runs (image changes, diamond cuts, retries) would revert with `Verification.ChallengeExpired`. Capital `R` issues the challenge directly — decoupled from `r` — so re-runs work.

Run:

```bash
bash ./scripts/post-build.sh
```

- `allow-tee-version` whitelists the codeHash for your extension.
- `register-tee -command rRap` pre-registers the TEE, requests fresh attestation, runs the FTDC availability check, promotes to production.

On success it prints a `Post-build complete` banner. If it reverts instead, jump to [Troubleshooting](#troubleshooting) before re-running.

## 9. End-to-end test

```bash
bash ./scripts/test.sh
```

Sends test instructions through the deployed TEE and verifies the round-trip. On success it prints a `Tests passed` banner; a revert sends you to [Troubleshooting](#troubleshooting).

---

## Local / simulated deployment (Docker + ngrok)

Run the TEE + proxy as **local Docker containers** with a **simulated** TEE,
reachable from the chain via an **ngrok** tunnel. No GCP Confidential Space VM and
no devops hand-off — useful for development against the real Coston/Coston2 chain.

**What differs from the production path:** Steps 1–4 and 8–9 are unchanged. You
activate the `local` variant in Step 3, and replace Steps 5–7 (build image → VM
hand-off → verify) with the Docker + ngrok flow below.

### What `local` changes in `.env`

`use-chain.sh local <chain>` copies `.env.local.<chain>` instead of `.env.<chain>`.
Only **two** values differ from the deployed template:

| Variable        | Deployed                      | Local / simulated |
| --------------- | ----------------------------- | ----------------- |
| `SIMULATED_TEE` | `false`                       | `true`            |
| `EXT_PROXY_URL` | devops proxy (`…flare.rocks`) | your ngrok URL    |

Everything else is identical. In particular:

- `LOCAL_MODE` stays **`false`** — you're still on the real chain; only the TEE is simulated.
- `MODE` is **not** in `.env`. `docker-compose.yaml` injects `MODE=1` into the
  container at runtime (`MODE=${MODE:-1}`), so the simulated attestation matches
  `SIMULATED_TEE=true` with no Dockerfile change.

### Steps

1. **Activate local mode** (replaces Step 3's activation):

   ```bash
   bash ./scripts/use-chain.sh local coston2 go   # or: local coston <language>
   ```

2. **Register the extension on-chain** — exactly Step 4:

   ```bash
   bash ./scripts/pre-build.sh
   ```

3. **Start the ngrok tunnel** to the proxy's public port — host `6674`, which maps
   to the proxy container's `6664` (`docker-compose.yaml`):

   ```bash
   ngrok http 6674
   ```

   ngrok then displays a live status screen — copy the public URL from its
   **`Forwarding`** line:

   ```text
   Session Status    online
   Forwarding        https://your-domain.ngrok-free.dev -> http://localhost:6674
                     ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ copy this
   ```

   Paste that URL into `EXT_PROXY_URL` in `.env.local.<chain>`, then re-activate
   so `.env` picks it up:

   ```bash
   # edit .env.local.coston2 → EXT_PROXY_URL=https://your-domain.ngrok-free.dev
   bash ./scripts/use-chain.sh local coston2 go
   ```

   > [!NOTE]
   > ngrok's free tier gives your account one **reserved domain**, so this
   > Forwarding URL stays the same across restarts — you normally set
   > `EXT_PROXY_URL` once and leave it. Only re-paste if it ever changes.

4. **Configure the proxy's indexer DB.** ⚠️ Load-bearing. `start-services.sh` (next
   step) runs your own ext-proxy, which queries the indexer directly. Create
   `config/proxy/extension_proxy.<chain>.docker.toml` and fill its `[db]` block —
   see [Indexer DB credentials](#indexer-db-credentials) (Coston2 and Coston have
   **different** creds). Without it the proxy can't read the chain indexer and
   `test.sh` fails the round-trip.

5. **Start the local containers** (Docker Desktop must be running). `start-services.sh`
   builds the extension image for your `LANGUAGE` and runs redis + ext-proxy +
   extension-tee. It auto-detects the chain from `.env` — **no `--chain` needed**:

   ```bash
   bash ./scripts/start-services.sh     # build + run the local stack (teardown in step 8)
   ```

6. **Verify `/info`** (replaces Step 7). For a simulated TEE the `codeHash` is the
   well-known simulated value — the inverse of the production check:

   ```bash
   curl -s "$EXT_PROXY_URL/info" | jq '.machineData'
   ```

   | Field          | Expected (simulated)                                               |
   | -------------- | ------------------------------------------------------------------ |
   | `codeHash`     | `0x194844cf…` (the **simulated** hash — production _rejects_ this) |
   | `extensionId`  | matches `config/extension.env` `EXTENSION_ID`                      |
   | `initialOwner` | matches your `INITIAL_OWNER`                                       |

7. **Register the TEE and test** — exactly Steps 8–9:

   ```bash
   bash ./scripts/post-build.sh
   bash ./scripts/test.sh
   ```

8. **Tear down** when you're finished — stops and removes the local containers.
   Like `start-services.sh`, it auto-detects the chain from `.env`, so no
   `--chain` is needed. (This is local-only; the deployed path has no local stack
   to stop.)

   ```bash
   bash ./scripts/stop-services.sh
   ```

> [!TIP]
> Re-running after a code change: keep `ngrok` running (its reserved URL is
> stable, so `EXT_PROXY_URL` stays valid) and just re-run `start-services.sh`
> before `post-build.sh` / `test.sh`.

## When the extension image changes

1. Rebuild and hand off the new image.
2. The VM is re-deployed → `codeHash` changes.
3. `bash ./scripts/post-build.sh` whitelists the new codeHash.
4. `bash ./scripts/test.sh`.

## When the `FlareTeeManager` diamond is re-deployed

All extension registrations on that chain are wiped:

1. `bash ./scripts/pre-build.sh --force` — mints a fresh `EXTENSION_ID`. The `--force` opt-in is required because `config/extension.env` still has the now-invalid values from the previous deploy (see [Step 4](#4-register-the-extension-on-chain)).
2. Send the new `EXTENSION_ID` to the VM operator. They restart the container with `EXTENSION_ID=<new value>` as a launch-policy env override — no image rebuild needed.
3. Re-curl `/info` and confirm `extensionId` matches.
4. `bash ./scripts/post-build.sh`.
5. `bash ./scripts/test.sh`.

## Troubleshooting

If `test.sh` reverts or `post-build.sh` fails, run the diagnostic before changing anything:

```bash
cd go/tools
go run ./cmd/check-tee-state \
    -a "../../config/$CHAIN/deployed-addresses.json" \
    -c "$CHAIN_URL" \
    -p "$EXT_PROXY_URL" \
    -instructionSender "$INSTRUCTION_SENDER"
```

It reads (read-only — no transactions) the TEE proxy's `/info`, the on-chain TEE machine record, `InstructionSender._extensionId` (via storage slot 0), and `getActiveTeeMachines` for each extension involved. The output ends with a verdict that maps directly to a fix:

| Symptom                                                          | Verdict line                                                         | Fix                                                                                                                                                             |
| ---------------------------------------------------------------- | -------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `test.sh` reverts with `0xd65ac61e` (`MachineManager.TooMany()`) | `MISMATCH: InstructionSender ext=X ≠ TEE on-chain ext=Y`             | Set `INSTRUCTION_SENDER` in `config/extension.env` to the address the diag prints under `[TEE record ext=Y]`.                                                   |
| `post-build.sh` reverts with `MachineManager.InvalidTeeStatus()` | `toProduction will revert: status=1 (PRODUCTION)`                    | TEE is already promoted. Skip post-build entirely, or rely on the idempotency guard in `registration.go` (which short-circuits when status=PRODUCTION).         |
| `check-tee-state` says active set is empty _and_ IDs all agree   | (no MISMATCH line; "active set was emptied for a non-status reason") | TEE was banned or its version disabled. Investigate via on-chain events; `pause()` → re-promote is the recovery path, but only the TEE machine owner can do it. |

### Deploying from a fresh clone (without re-minting)

`pre-build.sh` (Step 4) does **two** jobs: it (1) generates the Go contract bindings and (2) mints + registers a **new** extension. If a TEE is already deployed and you just want to re-run `post-build.sh` / `test.sh` from a clean checkout, you must **not** run `pre-build.sh` — it would orphan the existing TEE. But a fresh clone is missing two things `pre-build.sh` would otherwise have produced, both gitignored generated artifacts:

**1. Missing contract bindings — `test.sh` fails to compile:**

```text
# sign-extension/tools/pkg/utils
pkg/utils/instructions.go:34: undefined: sign.InstructionSender
pkg/utils/instructions.go:42: undefined: sign.DeployInstructionSender
pkg/utils/instructions.go:65: undefined: sign.NewInstructionSender
...
```

The generated binding `go/tools/pkg/contracts/sign/autogen.go` doesn't exist yet. Generate it on its own — this only runs `forge build` + `abigen`, it does **not** deploy or touch the chain:

```bash
bash ./scripts/generate-bindings.sh
```

**2. Missing `config/extension.env` — `test.sh` aborts before running:**

```text
[test] ERROR: INSTRUCTION_SENDER not set. Run pre-build.sh first or set it manually.
```

`config/extension.env` is generated per-deploy and gitignored, so it never comes with a clone. Recover both values without re-minting:

- `EXTENSION_ID` — read it from the deployed proxy: `curl -s "$EXT_PROXY_URL/info" | jq '.machineData.extensionId'`.
- `INSTRUCTION_SENDER` — query the on-chain extension→sender mapping on the `FlareTeeManager` diamond (its address is the `FlareTeeManager` entry in `config/<chain>/deployed-addresses.json`):

  ```bash
  # CHAIN= prefix stops cast from treating the .env CHAIN=coston2 as a --chain alias it doesn't know
  CHAIN= cast call <FlareTeeManager-address> \
      "getTeeExtensionInstructionsSender(uint256)(address)" \
      <EXTENSION_ID> \
      --rpc-url "$CHAIN_URL"
  ```

  A non-zero address is your `INSTRUCTION_SENDER`. The zero address means no sender is registered for that `EXTENSION_ID` — a real mismatch; run `check-tee-state` (above) before going further.

Then write the file and continue:

```bash
cat > config/extension.env <<EOF
EXTENSION_ID=<EXTENSION_ID>
INSTRUCTION_SENDER=<address from the cast call>
EOF

bash ./scripts/post-build.sh   # idempotent — skips steps already done on-chain
bash ./scripts/test.sh
```
