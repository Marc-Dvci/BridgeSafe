#!/usr/bin/env bash
# Stop the BridgeSafe stack and close any tunnel. Data is preserved.
#
#   scripts/stop-services.sh
set -uo pipefail
GRN=$'\033[0;32m'; NC=$'\033[0m'
repo_root="$(git rev-parse --show-toplevel)"; cd "$repo_root"

printf 'Stopping extension stack...\n'
if [[ -d extension ]]; then
  (cd extension && bash scripts/stop-services.sh >/dev/null 2>&1) || \
  (cd extension && docker compose -f docker-compose.yaml -f docker-compose.coston2.yaml down >/dev/null 2>&1) || true
fi

printf 'Stopping indexer...\n'
docker compose -f infra/docker-compose.yml down >/dev/null 2>&1 || true

# The tunnel is the one thing that must not outlive a session — it is the only
# inbound exposure this project has. See SECURITY.md section 1.2.
printf 'Closing any cloudflared tunnel...\n'
pkill -f 'cloudflared tunnel --url http://127.0.0.1:6674' 2>/dev/null || true

printf '%s✔ stopped%s  (volumes kept; use scripts/teardown.sh to remove them)\n' "$GRN" "$NC"
