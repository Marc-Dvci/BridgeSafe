#!/usr/bin/env bash
# Block secrets and mainnet endpoints from entering the public repository.
#
#   scripts/check-secrets.sh            # scan the staged diff (pre-commit hook)
#   scripts/check-secrets.sh --all      # scan every tracked file (CI / pre-push)
#
# Exits non-zero on the first violation. See SECURITY.md section 1.4.
set -uo pipefail

RED=$'\033[0;31m'; YEL=$'\033[0;33m'; GRN=$'\033[0;32m'; NC=$'\033[0m'
violations=0

report() {
  printf '%s✖ %s%s\n  %s\n' "$RED" "$1" "$NC" "$2" >&2
  violations=$((violations + 1))
}

mode="staged"
[[ "${1:-}" == "--all" ]] && mode="all"

repo_root="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo "not a git repository" >&2; exit 2; }
cd "$repo_root"

if [[ "$mode" == "all" ]]; then
  mapfile -t files < <(git ls-files)
else
  mapfile -t files < <(git diff --cached --name-only --diff-filter=ACMR)
fi

if [[ ${#files[@]} -eq 0 ]]; then
  echo "${GRN}✔ nothing to scan${NC}"; exit 0
fi

# Files that are allowed to contain example-shaped values.
# Two different questions, deliberately answered by two different predicates.
#
# `is_example` answers "is this file allowed to be tracked at all?" — an example
# env or proxy config is, a real one is not.
is_example() {
  case "$1" in
    *.example|*.example.*|*/.env.example|SECURITY.md|docs/threat-model.md) return 0 ;;
    *) return 1 ;;
  esac
}

# `skip_key_scan` answers "may this file contain a 64-hex literal harmlessly?"
# Only prose that quotes hashes as illustration.
#
# Note what is deliberately NOT here: `.env.example`. An example env file is
# committed by definition, which makes it the likeliest place for a real key to be
# pasted and published — exempting it inverts the check. Upstream's fce-sign
# example shipped a live testnet key with 1300 transactions in exactly that spot,
# and folding this into `is_example` meant the scanner never said a word about it.
skip_key_scan() {
  case "$1" in
    SECURITY.md|docs/threat-model.md|docs/architecture.md) return 0 ;;
    *) return 1 ;;
  esac
}

# Lockfiles and checksum manifests are full of 64-hex integrity digests. Those are
# not secrets, and flagging them would train the operator to ignore this scanner —
# which is worse than the false negative. The mainnet-endpoint and PEM checks still
# apply to these files; only the private-key heuristic is skipped.
is_hash_manifest() {
  case "$1" in
    *.lock|*.sum|*-lock.json|*.lockb|*/go.sum|*/soldeer.lock) return 0 ;;
    *) return 1 ;;
  esac
}

for f in "${files[@]}"; do
  [[ -f "$f" ]] || continue

  # 1. Files that must never be tracked at all, regardless of content.
  case "$f" in
    .env|.env.*)
      is_example "$f" || report "tracked env file: $f" \
        "Only .env.example may be committed. Run: git rm --cached '$f'" ;;
    *.key|*.pem|*.p12|*.keystore)
      report "tracked key material: $f" "Run: git rm --cached '$f'" ;;
    config/extension.env)
      report "tracked generated config: $f" "Run: git rm --cached '$f'" ;;
    config/proxy/*.toml)
      is_example "$f" || report "tracked proxy config: $f" \
        "This file holds DB credentials. Only .toml.example may be committed." ;;
  esac

  # Skip binary files for content scanning.
  grep -Iq . "$f" 2>/dev/null || continue

  # 2. Private-key-shaped strings.
  #
  #    A bare 64-hex run is ambiguous: it is equally the shape of a secp256k1
  #    private key and of every keccak hash, event topic and test vector in a
  #    codebase like this one. Flagging all of them produced ~20 false positives
  #    per commit here, and a scanner that cries wolf gets bypassed, which is
  #    strictly worse than a narrower one.
  #
  #    So outside of env files, a 64-hex literal is reported only when the same
  #    line also names a secret. That is where keys actually end up — pasted into
  #    an assignment called PRIVATE_KEY, deployerKey, secret, seed. Inside .env
  #    files every 64-hex literal is reported regardless, because nothing else
  #    belongs there.
  if ! is_hash_manifest "$f"; then
    # POSIX ERE — case-insensitivity comes from grep -i, not an inline (?i) flag.
    key_context='(priv[-_ ]?key|private[-_ ]?key|privatekey|secret|seed|mnemonic|passphrase|deployer[-_ ]?key|signing[-_ ]?key|wallet[-_ ]?key|keystore)'
    case "$f" in
      .env|.env.*|*/.env|*/.env.*) require_context=0 ;;
      *) require_context=1 ;;
    esac
    while IFS=: read -r lineno line; do
      [[ -z "${lineno:-}" ]] && continue
      hex=$(printf '%s' "$line" | grep -oiE '(0x)?[0-9a-f]{64}' | head -1)
      [[ -z "$hex" ]] && continue
      stripped=${hex#0x}; stripped=${stripped#0X}
      # single repeated character => placeholder
      if [[ "$stripped" =~ ^(.)\1{63}$ ]]; then continue; fi
      # Keys that are public by design, so flagging them is pure noise:
      #   - the standard Hardhat/Anvil dev accounts,
      #   - the default local key baked into Flare's own FCC tooling
      #     (extension/go/tools/cmd/start-tee, local chain only),
      #   - the obvious "abcdef1234..." dummy in Flare's validate tests.
      # Anything not on this list is still reported.
      case "$stripped" in
        ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80|\
        59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d|\
        5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a|\
        804b01a8c27a65cc694a867be76edae3ccce7a7161cda1f67a8349df696d2207|\
        abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890) continue ;;
      esac
      skip_key_scan "$f" && continue
      # Outside env files, require the line to name a secret (see above).
      if [[ $require_context -eq 1 ]] && ! printf '%s' "$line" | grep -qiE "$key_context"; then
        continue
      fi
      report "possible private key in $f:$lineno" \
        "A 64-hex literal on a line naming a secret. Move it to .env (gitignored) and reference it by name."
    done < <(grep -nE '(0x)?[0-9a-fA-F]{64}' "$f" 2>/dev/null | cut -d: -f1,2 --output-delimiter=: | head -50)
  fi

  # 3. XRPL family seeds: base58 starting with 's', 28-31 chars.
  if grep -nqE '\bs[1-9A-HJ-NP-Za-km-z]{27,30}\b' "$f" 2>/dev/null; then
    is_example "$f" || report "possible XRPL family seed in $f" \
      "XRPL secrets start with 's'. Move it to .env (gitignored)."
  fi

  # 4. PEM blocks. The pattern is assembled at runtime so this scanner does not
  #    match its own source.
  pem_pat="-----BEGIN .*PRIVATE"" KEY-----"
  if grep -qE -- "$pem_pat" "$f" 2>/dev/null; then
    report "PEM private key block in $f" "Remove it; store key material outside the repo."
  fi

  # 5. Mainnet endpoints. This project is testnet-only by policy (SECURITY.md 1.1).
  while IFS=: read -r lineno line; do
    [[ -z "${lineno:-}" ]] && continue
    is_example "$f" && continue
    report "mainnet endpoint in $f:$lineno" \
      "This repo is testnet-only. Offending line: $(printf '%s' "$line" | tr -s ' ' | cut -c1-100)"
  done < <(grep -nE 'flare-api\.flare\.network|songbird-api\.flare\.network|s1\.ripple\.com|s2\.ripple\.com|xrplcluster\.com' "$f" 2>/dev/null | head -10)
done

echo
if [[ $violations -gt 0 ]]; then
  printf '%s%d violation(s) found — commit blocked.%s\n' "$RED" "$violations" "$NC" >&2
  printf 'Override only if you are certain: %sgit commit --no-verify%s\n' "$YEL" "$NC" >&2
  exit 1
fi

printf '%s✔ no secrets or mainnet endpoints in %d file(s)%s\n' "$GRN" "${#files[@]}" "$NC"
