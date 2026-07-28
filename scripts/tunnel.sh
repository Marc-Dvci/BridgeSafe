#!/usr/bin/env bash
# Expose the local ext-proxy so Flare's TEE infrastructure can reach it.
#
#   scripts/tunnel.sh
#
# READ SECURITY.md SECTION 1.2 BEFORE RUNNING THIS.
#
# This is the only part of BridgeSafe that accepts traffic from the internet.
# It publishes exactly one port — 6674, the extension proxy — over a cloudflared
# quick tunnel, and nothing else on this machine becomes reachable. The tunnel
# lives only as long as this process: close it and the exposure closes with it.
#
# cloudflared is used rather than ngrok because a quick tunnel needs no account.
# Flare's documentation accepts either.
set -euo pipefail

YEL=$'\033[0;33m'; GRN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'
PORT=6674

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

command -v cloudflared >/dev/null 2>&1 || {
  printf '%scloudflared not found.%s\n' "$RED" "$NC" >&2
  printf 'Install it:  winget install Cloudflare.cloudflared\n' >&2
  printf '             brew install cloudflared\n' >&2
  exit 1
}

cat <<EOF

${YEL}This publishes 127.0.0.1:${PORT} to the public internet.${NC}

  What is exposed : the extension proxy HTTP API, and nothing else.
  Who can call it : anyone who learns the URL. It is unauthenticated.
  What is behind it: testnet instruction routing. No funds, no private keys.
  How to stop it  : Ctrl-C. The exposure ends with this process.

Leave this running only while you are testing or demoing.

EOF

log="$(mktemp -t bridgesafe-tunnel.XXXXXX)"
trap 'rm -f "$log"' EXIT

printf 'Starting tunnel...\n'
cloudflared tunnel --url "http://127.0.0.1:${PORT}" --no-autoupdate >"$log" 2>&1 &
tunnel_pid=$!
trap 'kill "$tunnel_pid" 2>/dev/null || true; rm -f "$log"' EXIT INT TERM

url=""
for _ in $(seq 1 60); do
  url="$(grep -oE 'https://[a-z0-9-]+\.trycloudflare\.com' "$log" 2>/dev/null | head -1 || true)"
  [[ -n "$url" ]] && break
  if ! kill -0 "$tunnel_pid" 2>/dev/null; then
    printf '%scloudflared exited before publishing a URL:%s\n' "$RED" "$NC" >&2
    cat "$log" >&2
    exit 1
  fi
  sleep 1
done

if [[ -z "$url" ]]; then
  printf '%sTimed out waiting for a tunnel URL.%s Log:\n' "$RED" "$NC" >&2
  cat "$log" >&2
  exit 1
fi

printf '\n%s✔ tunnel live%s\n\n  %s  ->  127.0.0.1:%s\n\n' "$GRN" "$NC" "$url" "$PORT"

# Write it where the FCC scripts look, so the operator does not have to copy it.
if [[ -f .env ]]; then
  if grep -qE '^EXT_PROXY_URL=' .env; then
    awk -v u="$url" 'BEGIN{FS=OFS="="} $1=="EXT_PROXY_URL"{print "EXT_PROXY_URL=" u; next} {print}' \
      .env > .env.tmp && mv .env.tmp .env
  else
    printf 'EXT_PROXY_URL=%s\n' "$url" >> .env
  fi
  printf '  written to .env as EXT_PROXY_URL\n\n'
else
  printf '  %sno .env yet%s — copy this into EXT_PROXY_URL when you create one\n\n' "$YEL" "$NC"
fi

printf 'Press Ctrl-C to close the tunnel.\n\n'
wait "$tunnel_pid"
