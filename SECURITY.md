# Security model and operator safety

BridgeSafe is **testnet-only prototype software**. This document covers two
different audiences:

1. **Operator safety** — what running this repo does to the machine it runs on.
   Read this first if you are about to run the stack.
2. **Product security model** — what BridgeSafe does and does not guarantee as a
   system. See also [`docs/threat-model.md`](docs/threat-model.md).

---

## 1. Operator safety

### 1.1 No real value, anywhere

Every network this repo touches is a test network with valueless tokens:

| Component | Network | Token | Source |
|---|---|---|---|
| Flare contracts | Coston2 (chain ID `114`) | C2FLR | [faucet](https://faucet.flare.network/coston2) |
| External payments | XRPL Testnet | test XRP | [faucet](https://xrpl.org/xrp-testnet-faucet.html) |

There is no mainnet configuration in this repository. `scripts/check-secrets.sh`
fails the build if a Flare Mainnet or Songbird RPC URL, or an XRPL Mainnet
endpoint, appears anywhere in tracked files. Do not add one.

**Never** reuse a key that holds real funds for any variable in this project.

### 1.2 The one genuine inbound exposure: the proxy tunnel

This is the only part of the stack that accepts traffic from the internet, and
you should understand it before running it.

Flare's TEE infrastructure has to reach your extension proxy to deliver
instructions, so `ext-proxy`'s external port (`6674`) must be publicly
reachable. `scripts/tunnel.sh` opens a [cloudflared](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/do-more-with-tunnels/trycloudflare/)
quick tunnel that maps a random `*.trycloudflare.com` hostname to
`127.0.0.1:6674`.

What this means concretely:

- **Only port 6674 is published.** The tunnel is a userspace process. It does not
  modify your firewall, does not open any other port, and gives no inbound route
  to anything else on the machine. Nothing else in the stack is reachable.
- **Anyone who learns the URL can call the proxy API.** The hostname is random
  and unadvertised, but it is not a secret and not authenticated. Flare's own
  documentation carries the same warning. On a testnet proxy the realistic blast
  radius is nuisance traffic and junk instruction results — there are no funds
  and no private keys behind that port.
- **It is not persistent.** The tunnel lives only as long as the `cloudflared`
  process. `scripts/tunnel.sh` runs in the foreground so closing it closes the
  exposure, and `scripts/stop-services.sh` kills any stray tunnel.

**Operator rule: start the tunnel when you begin a demo or test run, stop it when
you finish.** Do not leave it running unattended overnight.

If you would rather not expose anything at all, the local-mode stack
(`scripts/dev-local.sh`) runs the whole system against a local chain with no
tunnel. You lose the real-Coston2 deployment, not the functionality.

### 1.3 Every other service is bound to loopback only

All local infrastructure is published to `127.0.0.1` explicitly, never to
`0.0.0.0`. Nothing below is reachable from your LAN, let alone the internet:

| Service | Binding | Purpose |
|---|---|---|
| MySQL (C-chain indexer) | `127.0.0.1:3306` | indexer storage |
| Redis | `127.0.0.1:6382` | proxy queue |
| ext-proxy internal | `127.0.0.1:6673` | container-to-container |
| ext-proxy external | `127.0.0.1:6674` | **tunnelled** (see 1.2) |
| Extension TEE server | container-internal only | not published |
| TEE sign port | container-internal only | not published |
| Types server | `127.0.0.1:8100` | decoding for the UI |
| Frontend dev server | `127.0.0.1:3000` | UI |

`scripts/check-bindings.sh` greps the compose files for `0.0.0.0` and any
unqualified port mapping, and fails if it finds one. It runs as part of
`scripts/preflight.sh`.

### 1.4 Key handling

Four distinct keys exist. They are deliberately separated so no single one is
both privileged and exposed:

| Key | Held by | Risk if leaked |
|---|---|---|
| Coston2 deployer | your `.env`, on disk | loss of testnet C2FLR; attacker could own your test contracts |
| Proxy key | your `.env`, on disk | can relay instructions for your extension on testnet |
| **XRPL treasury key** | **generated inside the enclave; never leaves it** | — |
| Broadcaster | your `.env`, on disk | can submit already-signed blobs; **cannot create payments** |

The design point worth noting: the **broadcaster never holds the treasury key.**
It receives an already-signed transaction blob and can only submit it. Stealing
the broadcaster key does not let you move treasury funds.

Rules the repo enforces:

- `.env` and every `*.key` / `config/extension.env` / generated proxy TOML are
  gitignored (see [`.gitignore`](.gitignore)).
- Only `.env.example` files — containing placeholders, never values — are
  tracked.
- `scripts/check-secrets.sh` scans the staged diff for private-key-shaped
  strings (64-hex, `0x`-prefixed 64-hex, XRPL family seeds `s...`, PEM headers)
  and blocks the commit. Install it as a pre-commit hook with
  `scripts/install-hooks.sh`.
- Generate fresh keys with `scripts/new-testnet-keys.sh`. Do not paste in an
  existing key.

### 1.5 Third-party code you will be running

Be aware of what executes on your machine:

- **Flare container images** (`ext-proxy`, TEE node) pulled from Flare's registry.
  These are operated by the Flare Foundation, and the FCC stack cannot run
  without them. They are pinned by digest in `infra/versions.env` so an image
  cannot silently change under you; `scripts/verify-images.sh` re-checks the
  digests before starting.
- **The extension image is built locally from this repo's source** — it is not
  pulled. That is also what makes the code hash reproducible.
- **Go/npm dependencies** are pinned via `go.sum` and lockfiles.

Everything runs in Docker containers with no bind-mounts into your home
directory — only into this project folder.

### 1.6 Removing everything

`scripts/teardown.sh` stops all containers, deletes the local volumes (indexer
DB, redis), removes locally-built images, and kills any tunnel. On-chain testnet
state cannot be deleted, but it holds nothing of value.

---

## 2. Product security model

### 2.1 What BridgeSafe claims

> Policy-controlled and independently verifiable cross-chain execution, using
> Flare consensus for authorization and attested confidential compute for
> signing.

### 2.2 What BridgeSafe explicitly does not claim

- **Not trustless.** The trust base includes TEE hardware, the cloud platform,
  Flare's FCC relays, this application's code, XRPL availability, and the
  contract implementation. Flare's own documentation states FCC is still in
  development.
- **Not a production bridge.** No liquidity management, solvency invariants,
  emergency upgrade governance, or audit. It is a treasury execution prototype.
- **Not permanently private.** XRPL is a public ledger. Once a payment settles,
  its recipient and amount are observable forever. BridgeSafe provides
  confidentiality *before* execution — payment instructions, treasury policies,
  and batch contents stay sealed until release — and restricted key usage inside
  the enclave. It cannot provide private XRP transfers.
- **Not real attestation in the demo configuration.** The default Coston2
  deployment uses `SIMULATED_TEE=true` (`MODE=1`). The instruction flow, the
  on-chain registration, and the signature verification are all real; the
  hardware attestation is simulated. Promoting to genuine AMD SEV attestation
  requires a GCP Confidential Space VM — see [`docs/threat-model.md`](docs/threat-model.md)
  for exactly which guarantees that changes.

### 2.3 Enforced invariants

The contracts enforce these regardless of what the enclave or the relayer does:

- A request cannot reach `SETTLED` without an FDC proof of the actual XRPL
  payment.
- The proof must match the expected source account, destination account, exact
  amount, and the BridgeSafe request ID carried in the payment memo.
- An XRPL transaction ID can settle at most one request, ever.
- Requests carry sequential nonces and hard expiries.
- Only the registered treasury owner can initiate a payment.
- The enclave signs only a typed XRPL `Payment` structure. There is no
  arbitrary-message signing endpoint.

## 3. Reporting

This is hackathon software with no production deployment. Open a GitHub issue
for anything you find.
