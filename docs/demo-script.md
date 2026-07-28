# Running BridgeSafe end to end

From an empty machine to a settled payment. Roughly 40 minutes, most of it
waiting for the indexer to sync and for an FDC voting round.

Read [SECURITY.md](../SECURITY.md) first — step 4 publishes a port to the
internet, and you should know exactly what that does before you run it.

## 0. Prerequisites

Go 1.25+ · Foundry · Docker · Node 20+ · `jq` · `cloudflared`

```bash
cp .env.example .env
scripts/new-testnet-keys.sh     # generates disposable testnet keys into .env
scripts/install-hooks.sh
```

Fund the deployer address it prints at <https://faucet.flare.network/coston2>,
then:

```bash
scripts/preflight.sh
```

This checks the toolchain, every endpoint the system uses, the deployer balance,
and that nothing secret is tracked. Fix anything it reports before continuing —
these failures are much harder to diagnose later.

## 1. Contracts

```bash
cd contracts && forge test        # 64 tests
cd .. && scripts/deploy-coston2.sh
```

Addresses are written to `.env`, `apps/web/.env.local` and
`docs/deployment.json`. Nothing needs copying by hand.

## 2. Local C-chain indexer

Flare's extension proxy reads signing policies from a C-chain indexer database.
Rather than wait on credentials to Flare's own, BridgeSafe runs one:

```bash
docker compose -f infra/docker-compose.yml up -d
docker compose -f infra/docker-compose.yml logs -f indexer
```

Wait for the health endpoint to go green — it returns 503 during catch-up:

```bash
until curl -sf http://127.0.0.1:8080/health >/dev/null; do sleep 5; done
```

First sync takes a few minutes. FSP mode only indexes what the protocol needs.

## 3. Point the extension proxy at it

```bash
cp extension/config/proxy/extension_proxy.coston2.docker.toml.example \
   extension/config/proxy/extension_proxy.coston2.docker.toml
```

Edit the `[db]` block:

```toml
[db]
host = "host.docker.internal"
port = 3306
database = "flare_ftso_indexer"
username = "indexer"
password = "indexer"
```

> Flare's documentation points this at their indexer (`34.38.42.208`) with
> credentials supplied on request. Both work; the local one removes the
> dependency on a human replying.

## 4. Publish the proxy

```bash
scripts/tunnel.sh
```

**This is the only inbound exposure in the project.** It publishes
`127.0.0.1:6674` and nothing else, the URL is unauthenticated, and there are no
funds or keys behind it. Leave it running in its own terminal for the session and
Ctrl-C it when you finish. The script writes `EXT_PROXY_URL` into `.env`.

## 5. Deploy the extension

In a new terminal:

```bash
cd extension
./scripts/use-chain.sh local coston2 go     # SIMULATED_TEE=true, LOCAL_MODE=false
./scripts/pre-build.sh                      # deploys + registers the extension
./scripts/start-services.sh                 # redis, ext-proxy, extension-tee
./scripts/post-build.sh                     # whitelists the code hash, registers the machine
```

Confirm the enclave is serving:

```bash
curl -s "$EXT_PROXY_URL/info" | jq '.machineData'
```

Then wire the enclave's address into the controller and let it discover its
extension id:

```bash
cd ..
TEE_ADDR=$(curl -s "$EXT_PROXY_URL/info" | jq -r '.machineData.address')
source .env
cast send "$BRIDGESAFE_CONTROLLER" "setTeeAddress(address)" "$TEE_ADDR" \
  --rpc-url "$CHAIN_URL" --private-key "$DEPLOYMENT_PRIVATE_KEY"
cast send "$BRIDGESAFE_CONTROLLER" "setExtensionId()" \
  --rpc-url "$CHAIN_URL" --private-key "$DEPLOYMENT_PRIVATE_KEY"
```

## 6. Start the supporting services

Four terminals:

```bash
cd extension/go && go run ./cmd/payload-builder            # seals instructions, loopback only
cd services && go run ./cmd/result-relay                   # carries enclave results on chain
cd services && go run ./cmd/broadcaster                    # submits signed blobs
cd services && go run ./cmd/fdc-worker                     # drives FDC attestation
```

`result-relay` is the one that makes the rest move. The contract dispatches an
instruction and the enclave answers asynchronously, leaving a signed result on the
extension proxy; the relay watches for the four dispatching events, collects each
result and delivers it to the matching controller method. Without it a request
stays in `CREATED` and nothing downstream ever fires.

It should log its enclave address at startup:

```
result-relay ready
  controller 0x…
  proxy      https://….trycloudflare.com
  enclave    0x…
```

If `enclave` is missing, `EXT_PROXY_URL` is wrong or the tunnel is down.

## 7. Create a treasury

```bash
source .env
# 100 XRP per payment, 500 XRP lifetime, 30-minute request lifetime
cast send "$BRIDGESAFE_CONTROLLER" \
  "createTreasury((uint256,uint256,uint64))" "(100000000,500000000,1800)" \
  --value 0.01ether --rpc-url "$CHAIN_URL" --private-key "$DEPLOYMENT_PRIVATE_KEY"
```

The enclave generates the XRPL key inside the TEE and returns the address.
`result-relay` picks that result up and calls `bindTreasuryAddress` for you —
watch for `treasury 1 … bindTreasuryAddress accepted`.

Then read the address back and **fund it** from the
[XRPL testnet faucet](https://faucet.altnet.rippletest.net/accounts):

```bash
cast call "$BRIDGESAFE_CONTROLLER" "getTreasury(uint256)" 1 --rpc-url "$CHAIN_URL"
```

The account must exist on XRPL before a payment can be signed — the enclave needs
its sequence number. `fdc-worker` and the enclave both say so explicitly if you
skip this.

## 8. Make a payment

```bash
cd apps/web && npm install && npm run dev
```

Open <http://127.0.0.1:3000>, connect the treasury owner's wallet, and submit a
payment. The console shows the trace as it advances:

| Stage | What is happening | Typical |
|---|---|---|
| Requested | ciphertext on chain, instruction dispatched | seconds |
| Authorized | enclave decrypted and checked the policy | ~10s |
| Signed | enclave built and signed the XRPL payment | ~10s |
| Submitted | relayer put it on the ledger | ~5s |
| Verified | FDC round finalised and proof accepted | **90–180s** |

The last stage is genuinely slow. FDC data providers have to reach consensus and
publish a Merkle root; the UI shows it as a pending stage rather than pretending
settlement is instant.

## 9. Check the negative paths

Worth doing on camera — they are the interesting part:

- Ask for more than the per-payment cap. The enclave refuses and the contract
  would refuse too.
- Send to a malformed r-address. Caught before the request is even opened.
- Let a request expire, then try to authorize it.
- Re-run `fdc-worker` against a settled request. `TxIdAlreadyUsed`.

## Shutting down

```bash
scripts/stop-services.sh     # stops everything, closes the tunnel, keeps data
scripts/teardown.sh          # also deletes volumes and local images
```

Stopping the tunnel is the one that matters. `stop-services.sh` kills it even if
the terminal running it is gone.

## When something goes wrong

**`Verification.TeeNotFound`** — `NORMAL_PROXY_URL` points at the wrong chain's
FTDC proxy.

**`Verification.ChallengeExpired`** — re-run `post-build.sh`.

**`code hashes do not match`** — `SIMULATED_TEE` and the image's `MODE` disagree.
Simulated mode needs `SIMULATED_TEE=true` with `MODE=1`.

**`MachineManager.TooMany()`** — `config/extension.env` holds an extension id
that no longer matches the on-chain record, usually after `pre-build.sh --force`.
Full reset, or keep `extension.env` and re-run only `post-build.sh` and `test.sh`.

**ext-proxy will not start** — check `docker compose logs ext-proxy`. Almost
always the indexer database: confirm it is healthy and that the `[db]` block uses
`host.docker.internal`, not `127.0.0.1`, from inside a container.

**`invalid value 'coston2' for '--chain'`** — something in your shell exports
`CHAIN=coston2`. Foundry auto-loads `.env` from the working directory and reads
`$CHAIN` as `--chain`, and its name for this network is `flare-coston2`. This
repo's `.env` deliberately does not set it; `unset CHAIN` and re-run.

**`Constructor argument count mismatch`** — a flag was written after
`--constructor-args`, which is variadic and swallows whatever follows it. Put
`--constructor-args` last.

**`actNotFound` when signing** — the treasury's XRPL account has not been funded.

**Verifier rejects the attestation request** — the payment needs 3 XRPL
confirmations, about 12 seconds. `fdc-worker` retries.
