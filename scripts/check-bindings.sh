#!/usr/bin/env bash
# Verify that no container port is published to anything but loopback.
#
# Docker publishes to 0.0.0.0 when a mapping omits the host address, which on
# Windows and macOS also traverses the host firewall — a service meant for this
# machine ends up reachable from the whole local network. Every mapping in this
# project must name 127.0.0.1 explicitly.
#
# The one intentional public path is the cloudflared tunnel in scripts/tunnel.sh,
# which forwards to loopback rather than binding a container to 0.0.0.0.
#
# See SECURITY.md section 1.3.
set -uo pipefail

RED=$'\033[0;31m'; GRN=$'\033[0;32m'; NC=$'\033[0m'
violations=0

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

mapfile -t compose_files < <(
  find . -name 'docker-compose*.y*ml' \
    -not -path './node_modules/*' -not -path './.git/*' | sort
)

if [[ ${#compose_files[@]} -eq 0 ]]; then
  echo "no compose files found; nothing to check"; exit 0
fi

report() {
  printf '%s✖ %s:%d %s%s\n    %s\n' "$RED" "$1" "$2" "$3" "$NC" "$4" >&2
  violations=$((violations + 1))
}

for f in "${compose_files[@]}"; do
  in_ports=0
  lineno=0
  while IFS= read -r line; do
    lineno=$((lineno + 1))

    if [[ "$line" =~ ^[[:space:]]*ports:[[:space:]]*$ ]]; then in_ports=1; continue; fi
    # Any non-list, non-blank line ends the block.
    if [[ $in_ports -eq 1 && ! "$line" =~ ^[[:space:]]*- && -n "${line// /}" ]]; then in_ports=0; fi
    [[ $in_ports -eq 1 ]] || continue
    [[ "$line" =~ ^[[:space:]]*-[[:space:]]* ]] || continue

    # Strip the list dash, surrounding quotes, trailing comment and whitespace.
    entry=$(printf '%s\n' "$line" \
      | sed -e 's/^[[:space:]]*-[[:space:]]*//' -e 's/#.*$//' \
      | tr -d '"'"'"' \t\r')
    [[ -z "$entry" ]] && continue

    # Compose entries are commonly "${VAR:-127.0.0.1:6382}:6379". The default is
    # what binds when VAR is unset, which is the normal case, so resolve it
    # before judging the mapping.
    resolved=$(printf '%s\n' "$entry" | sed -E 's/\$\{[A-Za-z_][A-Za-z0-9_]*:-([^}]*)\}/\1/g')

    # A variable with no default gives no guarantee about where it binds.
    if [[ "$resolved" == *'${'* ]]; then
      report "$f" "$lineno" "port mapping has no default bind address" "$entry"
      continue
    fi

    case "$resolved" in
      127.0.0.1:*|localhost:*)
        : # explicit loopback — correct
        ;;
      0.0.0.0:*|::*|\[::\]:*)
        report "$f" "$lineno" "publishes to all interfaces" "$resolved"
        ;;
      *)
        # "6674:6664" or a bare "6674" both default to 0.0.0.0.
        report "$f" "$lineno" "port mapping has no host address (defaults to 0.0.0.0)" \
          "$resolved   — write it as 127.0.0.1:$resolved"
        ;;
    esac
  done < "$f"
done

echo
if [[ $violations -gt 0 ]]; then
  printf '%s%d port mapping(s) would be reachable beyond this machine.%s\n' "$RED" "$violations" "$NC" >&2
  exit 1
fi

printf '%s✔ all published ports bound to 127.0.0.1 (%d compose file(s))%s\n' \
  "$GRN" "${#compose_files[@]}" "$NC"
