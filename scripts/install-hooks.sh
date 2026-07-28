#!/usr/bin/env bash
# Install the git hooks that keep secrets out of the public repository.
#   scripts/install-hooks.sh
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
hooks_dir="$(git rev-parse --git-path hooks)"
mkdir -p "$hooks_dir"

cat > "$hooks_dir/pre-commit" <<'HOOK'
#!/usr/bin/env bash
# BridgeSafe: block secrets and mainnet endpoints. Bypass with --no-verify.
set -euo pipefail
repo_root="$(git rev-parse --show-toplevel)"
exec "$repo_root/scripts/check-secrets.sh"
HOOK

cat > "$hooks_dir/pre-push" <<'HOOK'
#!/usr/bin/env bash
# BridgeSafe: full-repo scan plus port-binding check before anything goes public.
set -euo pipefail
repo_root="$(git rev-parse --show-toplevel)"
"$repo_root/scripts/check-secrets.sh" --all
"$repo_root/scripts/check-bindings.sh"
HOOK

chmod +x "$hooks_dir/pre-commit" "$hooks_dir/pre-push"

echo "✔ installed pre-commit and pre-push hooks in $hooks_dir"
echo "  pre-commit → scripts/check-secrets.sh"
echo "  pre-push   → scripts/check-secrets.sh --all + scripts/check-bindings.sh"
