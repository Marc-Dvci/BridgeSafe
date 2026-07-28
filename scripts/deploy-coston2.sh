#!/usr/bin/env bash
# Deploy the BridgeSafe contracts to Coston2 and wire them together.
#
#   scripts/deploy-coston2.sh
#
# Writes the resulting addresses back into .env and apps/web/.env.local, so no
# address has to be copied by hand.
#
# This deploys contracts only. Registering the extension and the TEE machine is a
# separate lifecycle owned by extension/scripts — see docs/demo-script.md.
set -euo pipefail

RED=$'\033[0;31m'; YEL=$'\033[0;33m'; GRN=$'\033[0;32m'; NC=$'\033[0m'

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

[[ -f .env ]] || { printf '%sNo .env.%s Run: cp .env.example .env && scripts/new-testnet-keys.sh\n' "$RED" "$NC" >&2; exit 1; }
set -a; . ./.env; set +a

# Foundry reads $CHAIN as if it were `--chain`, and its name for this network is
# `flare-coston2` — a stray `CHAIN=coston2` in the environment makes every forge and
# cast call below fail with "invalid value 'coston2' for '--chain'". .env no longer
# sets it, but a user's shell might. The RPC URL selects the network here.
unset CHAIN

: "${DEPLOYMENT_PRIVATE_KEY:?set DEPLOYMENT_PRIVATE_KEY in .env (scripts/new-testnet-keys.sh)}"
CHAIN_URL="${CHAIN_URL:-https://coston2-api.flare.network/ext/C/rpc}"

# Both TEE registries live on the same FlareTeeManager diamond.
FLARE_TEE_MANAGER="$(jq -r '.[] | select(.name=="FlareTeeManager") | .address' \
  extension/config/coston2/deployed-addresses.json | head -1)"
if [[ -z "$FLARE_TEE_MANAGER" || "$FLARE_TEE_MANAGER" == "null" ]]; then
  printf '%sFlareTeeManager not found in extension/config/coston2/deployed-addresses.json%s\n' "$RED" "$NC" >&2
  exit 1
fi

# bytes32("testXRP"), the FDC source id for XRPL Testnet.
SOURCE_TEST_XRP="0x7465737458525000000000000000000000000000000000000000000000000000"

printf '\nDeploying BridgeSafe to Coston2\n'
printf '  RPC              %s\n' "$CHAIN_URL"
printf '  FlareTeeManager  %s\n' "$FLARE_TEE_MANAGER"
printf '  Deployer         %s\n\n' "${INITIAL_OWNER:-<derived from key>}"

cd contracts

printf 'Building...\n'
forge build >/dev/null

printf 'Deploying BridgeSafeController...\n'
# `--constructor-args` is variadic, so anything after it is swallowed as another
# argument. It has to come last, or forge reports an argument-count mismatch.
CONTROLLER="$(forge create src/BridgeSafeController.sol:BridgeSafeController \
  --rpc-url "$CHAIN_URL" \
  --private-key "$DEPLOYMENT_PRIVATE_KEY" \
  --broadcast \
  --json \
  --constructor-args "$FLARE_TEE_MANAGER" "$FLARE_TEE_MANAGER" | jq -r '.deployedTo')"
[[ -n "$CONTROLLER" && "$CONTROLLER" != "null" ]] || { printf '%sController deployment failed.%s\n' "$RED" "$NC" >&2; exit 1; }
printf '  %s✔%s controller %s\n' "$GRN" "$NC" "$CONTROLLER"

printf 'Deploying BridgeSafeFdcVerifier...\n'
# The third argument is the FdcVerification override. Zero on a real network, so
# the address is resolved through Flare's ContractRegistry at call time and
# cannot be repointed after deployment.
VERIFIER="$(forge create src/BridgeSafeFdcVerifier.sol:BridgeSafeFdcVerifier \
  --rpc-url "$CHAIN_URL" \
  --private-key "$DEPLOYMENT_PRIVATE_KEY" \
  --broadcast \
  --json \
  --constructor-args "$CONTROLLER" "$SOURCE_TEST_XRP" "0x0000000000000000000000000000000000000000" | jq -r '.deployedTo')"
[[ -n "$VERIFIER" && "$VERIFIER" != "null" ]] || { printf '%sVerifier deployment failed.%s\n' "$RED" "$NC" >&2; exit 1; }
printf '  %s✔%s verifier   %s\n' "$GRN" "$NC" "$VERIFIER"

printf 'Registering the verifier on the controller...\n'
cast send "$CONTROLLER" "setFdcVerifier(address)" "$VERIFIER" \
  --rpc-url "$CHAIN_URL" --private-key "$DEPLOYMENT_PRIVATE_KEY" >/dev/null
printf '  %s✔%s linked\n' "$GRN" "$NC"

cd "$repo_root"

# --- persist the addresses -------------------------------------------------

set_env() {
  local file="$1" key="$2" value="$3"
  [[ -f "$file" ]] || : > "$file"
  if grep -qE "^${key}=" "$file"; then
    awk -v k="$key" -v v="$value" 'BEGIN{FS=OFS="="} $1==k {print k "=" v; next} {print}' \
      "$file" > "$file.tmp" && mv "$file.tmp" "$file"
  else
    printf '%s=%s\n' "$key" "$value" >> "$file"
  fi
}

set_env .env BRIDGESAFE_CONTROLLER "$CONTROLLER"
set_env .env BRIDGESAFE_FDC_VERIFIER "$VERIFIER"
set_env apps/web/.env.local NEXT_PUBLIC_CONTROLLER "$CONTROLLER"
set_env apps/web/.env.local NEXT_PUBLIC_FDC_VERIFIER "$VERIFIER"
set_env apps/web/.env.local NEXT_PUBLIC_PAYLOAD_BUILDER "http://127.0.0.1:8110"

mkdir -p docs
cat > docs/deployment.json <<EOF
{
  "network": "coston2",
  "chainId": 114,
  "deployedAt": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "contracts": {
    "BridgeSafeController": "$CONTROLLER",
    "BridgeSafeFdcVerifier": "$VERIFIER"
  },
  "dependencies": {
    "FlareTeeManager": "$FLARE_TEE_MANAGER",
    "FdcHub": "0x48aC463d7975828989331F4De43341627b9c5f1D",
    "FdcVerification": "resolved via ContractRegistry at call time",
    "Relay": "0xa10B672D1c62e5457b17af63d4302add6A99d7dE"
  },
  "fdcSourceId": "testXRP"
}
EOF

cat <<EOF

${GRN}✔ deployed${NC}

  BridgeSafeController   $CONTROLLER
  BridgeSafeFdcVerifier  $VERIFIER

  https://coston2-explorer.flare.network/address/$CONTROLLER
  https://coston2-explorer.flare.network/address/$VERIFIER

  Written to .env, apps/web/.env.local and docs/deployment.json.

${YEL}Next:${NC}
  1. Register the extension and TEE machine  — see docs/demo-script.md step 4
  2. controller.setTeeAddress(<enclave address>) once the machine is registered
  3. controller.setExtensionId()

Verify the source on the explorer (no API key needed):
  cd contracts && forge verify-contract $CONTROLLER \\
    src/BridgeSafeController.sol:BridgeSafeController \\
    --verifier blockscout --verifier-url https://coston2-explorer.flare.network/api/ \\
    --chain 114
EOF
