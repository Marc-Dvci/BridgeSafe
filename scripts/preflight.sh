#!/usr/bin/env bash
# Check everything BridgeSafe needs before a Coston2 run.
#
#   scripts/preflight.sh
#
# Fails loudly and specifically. Most first-run problems in this stack surface
# much later as an opaque proxy or registration error, so they are caught here.
set -uo pipefail

RED=$'\033[0;31m'; YEL=$'\033[0;33m'; GRN=$'\033[0;32m'; NC=$'\033[0m'
fail=0; warn=0

ok()   { printf '  %s✔%s %s\n' "$GRN" "$NC" "$1"; }
bad()  { printf '  %s✖%s %s\n     %s\n' "$RED" "$NC" "$1" "$2"; fail=$((fail+1)); }
note() { printf '  %s!%s %s\n     %s\n' "$YEL" "$NC" "$1" "$2"; warn=$((warn+1)); }

repo_root="$(git rev-parse --show-toplevel)"; cd "$repo_root"

printf '\nToolchain\n'
for t in go docker git jq; do
  if command -v "$t" >/dev/null 2>&1; then ok "$t"; else bad "$t missing" "Install $t and re-run."; fi
done
if command -v forge >/dev/null 2>&1; then ok "forge"; else bad "forge missing" "Install Foundry: https://getfoundry.sh"; fi
if command -v cloudflared >/dev/null 2>&1; then ok "cloudflared"; else
  note "cloudflared missing" "Needed by scripts/tunnel.sh. winget install Cloudflare.cloudflared"; fi

printf '\nDocker daemon\n'
if docker info >/dev/null 2>&1; then ok "daemon reachable"; else bad "daemon unreachable" "Start Docker Desktop."; fi

printf '\nConfiguration\n'
if [[ -f .env ]]; then
  ok ".env present"
  set -a; . ./.env 2>/dev/null || true; set +a
  [[ -n "${DEPLOYMENT_PRIVATE_KEY:-}" ]] && ok "DEPLOYMENT_PRIVATE_KEY set" \
    || bad "DEPLOYMENT_PRIVATE_KEY empty" "Run scripts/new-testnet-keys.sh"
  [[ -n "${EXT_PROXY_URL:-}" ]] && ok "EXT_PROXY_URL set ($EXT_PROXY_URL)" \
    || note "EXT_PROXY_URL empty" "Run scripts/tunnel.sh before deploying the extension."
else
  bad ".env missing" "cp .env.example .env && scripts/new-testnet-keys.sh"
fi

if git check-ignore -q .env 2>/dev/null; then ok ".env is gitignored"; else bad ".env is NOT gitignored" "Do not commit until fixed."; fi

printf '\nNetwork\n'
probe() {
  if curl -sf -m 15 -o /dev/null "$1"; then ok "$2"; else bad "$2 unreachable" "Checked $1"; fi
}
if curl -sf -m 15 -X POST -H 'Content-Type: application/json' \
     -d '{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}' \
     "${CHAIN_URL:-https://coston2-api.flare.network/ext/C/rpc}" | grep -q '"0x72"'; then
  ok "Coston2 RPC (chain 114)"
else
  bad "Coston2 RPC" "Checked ${CHAIN_URL:-https://coston2-api.flare.network/ext/C/rpc}"
fi
probe "https://fdc-verifiers-testnet.flare.network/verifier/xrp/api/health" "FDC XRP verifier"
if curl -sf -m 15 -o /dev/null "https://ctn2-data-availability.flare.network/api-doc"; then
  ok "FDC Data Availability layer"; else bad "DA layer unreachable" "https://ctn2-data-availability.flare.network"; fi
if curl -sf -m 15 -X POST -H 'Content-Type: application/json' -d '{"method":"server_info","params":[{}]}' \
     "${XRPL_RPC_URL:-https://s.altnet.rippletest.net:51234/}" | grep -q validated_ledger; then
  ok "XRPL Testnet"; else bad "XRPL Testnet unreachable" "${XRPL_RPC_URL:-https://s.altnet.rippletest.net:51234/}"; fi

printf '\nBalances\n'
if [[ -n "${INITIAL_OWNER:-}" ]] && command -v cast >/dev/null 2>&1; then
  bal=$(cast balance "$INITIAL_OWNER" --rpc-url "${CHAIN_URL:-https://coston2-api.flare.network/ext/C/rpc}" 2>/dev/null || echo 0)
  if [[ "$bal" == "0" ]]; then
    bad "deployer has no C2FLR" "Fund $INITIAL_OWNER at https://faucet.flare.network/coston2"
  else
    ok "deployer funded ($(cast from-wei "$bal" 2>/dev/null || echo "$bal") C2FLR)"
  fi
else
  note "balance not checked" "INITIAL_OWNER unset or cast unavailable"
fi

printf '\nRepository hygiene\n'
if bash scripts/check-bindings.sh >/dev/null 2>&1; then ok "all container ports bound to loopback"
else bad "a container port is exposed beyond loopback" "Run scripts/check-bindings.sh"; fi
if bash scripts/check-secrets.sh --all >/dev/null 2>&1; then ok "no secrets tracked"
else bad "possible secret in tracked files" "Run scripts/check-secrets.sh --all"; fi

# The console declares its own ABI fragments, so a Solidity struct change would
# otherwise surface only as a runtime decode failure in front of a judge.
if [[ -f contracts/out/BridgeSafeController.sol/BridgeSafeController.json ]]; then
  if node --experimental-strip-types scripts/check-web-abi.ts >/dev/null 2>&1; then
    ok "console ABI matches the compiled contract"
  else
    bad "console ABI has drifted from the contract" \
      "Run: node --experimental-strip-types scripts/check-web-abi.ts"
  fi
else
  note "console ABI not checked" "no artifact yet — run: cd contracts && forge build"
fi

printf '\n'
if [[ $fail -gt 0 ]]; then
  printf '%s%d blocking problem(s), %d warning(s).%s\n\n' "$RED" "$fail" "$warn" "$NC"; exit 1
fi
printf '%s✔ ready%s%s\n\n' "$GRN" "$([[ $warn -gt 0 ]] && printf ' (%d warning(s))' "$warn")" "$NC"
