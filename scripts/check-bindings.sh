#!/usr/bin/env bash
# Verify that no container port is published to anything but loopback.
#
# Docker publishes to 0.0.0.0 when a mapping omits the host address, which on
# Windows and macOS also punches through the host firewall. Every mapping in
# this project must be written explicitly as "127.0.0.1:HOST:CONTAINER".
#
# See SECURITY.md section 1.3.
set -uo pipefail

RED=$'\033[0;31m'; GRN=$'\033[0;32m'; NC=$'\033[0m'
violations=0

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

mapfile -t compose_files < <(find . -name 'docker-compose*.y*ml' -not -path './node_modules/*' -not -path './.git/*' | sort)

if [[ ${#compose_files[@]} -eq 0 ]]; then
  echo "no compose files found; nothing to check"; exit 0
fi

for f in "${compose_files[@]}"; do
  # Collect the "ports:" entries: lines starting with a dash inside a ports block.
  in_ports=0
  lineno=0
  while IFS= read -r line; do
    lineno=$((lineno + 1))
    if [[ "$line" =~ ^[[:space:]]*ports:[[:space:]]*$ ]]; then in_ports=1; continue; fi
    # A non-list, non-blank line at lower indentation ends the block.
    if [[ $in_ports -eq 1 && ! "$line" =~ ^[[:space:]]*- && -n "${line// /}" ]]; then in_ports=0; fi
    [[ $in_ports -eq 1 ]] || continue
    [[ "$line" =~ ^[[:space:]]*-[[:space:]]* ]] || continue

    entry=$(printf '%s' "$line" | sed -E 's/^[[:space:]]*-[[:space:]]*//; s/^["'"'"']//; s/["'"'"']$//; s/#.*$//' | tr -d '[:space:]')
    [[ -z "$entry" ]] && continue

    if [[ "$entry" == 0.0.0.0:* || "$entry" == "::"* ]]; then
      printf '%s✖ %s:%d publishes to all interfaces%s\n    %s\n' \
        "$RED" "$f" "$lineno" "$NC" "$entry" >&2
      violations=$((violations + 1))
    elif [[ "$entry" == 127.0.0.1:* || "$entry" == localhost:* ]]; then
      : # explicit loopback — correct
    else
      # "6674:6664" or a bare "6674" both default to 0.0.0.0
      printf '%s✖ %s:%d port mapping has no host address (defaults to 0.0.0.0)%s\n    %s\n    Write it as 127.0.0.1:%s\n' \
        "$RED" "$f" "$lineno" "$NC" "$entry" "$entry" >&2
      violations=$((violations + 1))
    fi
  done < "$f"
done

echo
if [[ $violations -gt 0 ]]; then
  printf '%s%d port mapping(s) would be reachable beyond this machine.%s\n' "$RED" "$violations" "$NC" >&2
  exit 1
fi

printf '%s✔ all published ports bound to 127.0.0.1 (%d compose file(s))%s\n' "$GRN" "${#compose_files[@]}" "$NC"
