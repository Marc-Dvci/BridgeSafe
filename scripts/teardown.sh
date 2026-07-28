#!/usr/bin/env bash
# Remove everything BridgeSafe created on this machine.
#
#   scripts/teardown.sh [--yes]
#
# Deletes containers, local volumes (indexer database, redis) and locally-built
# images, and closes any tunnel. On-chain testnet state cannot be deleted, but it
# holds nothing of value.
set -uo pipefail
YEL=$'\033[0;33m'; GRN=$'\033[0;32m'; NC=$'\033[0m'
repo_root="$(git rev-parse --show-toplevel)"; cd "$repo_root"

if [[ "${1:-}" != "--yes" ]]; then
  printf '%sThis deletes the local indexer database and all BridgeSafe containers.%s\n' "$YEL" "$NC"
  printf 'Your .env, keys and deployed contract addresses are NOT touched.\n\n'
  read -r -p 'Proceed? [y/N] ' reply
  [[ "$reply" =~ ^[Yy]$ ]] || { printf 'Cancelled.\n'; exit 0; }
fi

bash scripts/stop-services.sh || true

printf 'Removing volumes and locally-built images...\n'
docker compose -f infra/docker-compose.yml down -v --rmi local >/dev/null 2>&1 || true
if [[ -d extension ]]; then
  (cd extension && docker compose -f docker-compose.yaml -f docker-compose.coston2.yaml down -v --rmi local >/dev/null 2>&1) || true
fi

printf '%s✔ removed%s\n' "$GRN" "$NC"
printf '\nStill on disk (deliberately): .env, extension/config/extension.env, contracts/out\n'
