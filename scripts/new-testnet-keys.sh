#!/usr/bin/env bash
# Generate fresh, disposable testnet keys and write them into .env.
#
#   scripts/new-testnet-keys.sh
#
# Never paste an existing key into this project. These keys are for Coston2 and
# XRPL Testnet only and must never be reused anywhere that holds real value.
#
# Note what is NOT generated here: the XRPL treasury key. That key is created
# inside the TEE enclave by the CREATE_TREASURY_KEY command and never leaves it.
# That is the whole point of the design — see SECURITY.md section 1.4.
set -euo pipefail

YEL=$'\033[0;33m'; GRN=$'\033[0;32m'; RED=$'\033[0;31m'; NC=$'\033[0m'

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

command -v cast >/dev/null 2>&1 || {
  printf '%scast not found.%s Install Foundry: https://getfoundry.sh\n' "$RED" "$NC" >&2
  exit 1
}

env_file="$repo_root/.env"

if [[ -f "$env_file" ]] && grep -qE '^DEPLOYMENT_PRIVATE_KEY=.+' "$env_file"; then
  printf '%s.env already contains DEPLOYMENT_PRIVATE_KEY.%s\n' "$YEL" "$NC"
  printf 'Refusing to overwrite. Delete the line first if you want fresh keys.\n'
  exit 1
fi

[[ -f "$env_file" ]] || cp "$repo_root/.env.example" "$env_file"

new_key() {  # -> "privkey address"
  local out; out=$(cast wallet new)
  local addr priv
  addr=$(printf '%s' "$out" | grep -i 'Address' | awk '{print $NF}')
  priv=$(printf '%s' "$out" | grep -i 'Private key' | awk '{print $NF}')
  printf '%s %s' "$priv" "$addr"
}

read -r DEPLOY_KEY DEPLOY_ADDR <<<"$(new_key)"
read -r PROXY_KEY  PROXY_ADDR  <<<"$(new_key)"

# Write values in place, without disturbing the rest of the file.
set_var() {
  local k="$1" v="$2"
  if grep -qE "^${k}=" "$env_file"; then
    # Use a temp file: portable in-place edit across GNU/BSD sed.
    awk -v k="$k" -v v="$v" 'BEGIN{FS=OFS="="} $1==k {print k "=" v; next} {print}' \
      "$env_file" > "$env_file.tmp" && mv "$env_file.tmp" "$env_file"
  else
    printf '%s=%s\n' "$k" "$v" >> "$env_file"
  fi
}

set_var DEPLOYMENT_PRIVATE_KEY "$DEPLOY_KEY"
set_var INITIAL_OWNER          "$DEPLOY_ADDR"
set_var PROXY_PRIVATE_KEY      "$PROXY_KEY"

chmod 600 "$env_file" 2>/dev/null || true

cat <<EOF

${GRN}✔ generated fresh testnet keys → .env${NC}

  Deployer / owner   ${DEPLOY_ADDR}
  Proxy              ${PROXY_ADDR}

  XRPL treasury      (not generated here — created inside the TEE enclave)

${YEL}Next:${NC}
  1. Fund the deployer with C2FLR: https://faucet.flare.network/coston2
     Paste this address:  ${DEPLOY_ADDR}
  2. Confirm .env is ignored:      git check-ignore -v .env
  3. Install the secret hooks:     scripts/install-hooks.sh

${RED}These keys are disposable and testnet-only. Never send real value to them.${NC}
EOF
