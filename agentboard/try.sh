#!/usr/bin/env bash
# agentboard — hands-on launcher with the real Claude agent.
#
#   ./agentboard/try.sh [repo-path]
#
# Runs agentboard against [repo-path], or a scratch repo at
# /tmp/agentboard-try (created on first run). Agent: Claude Code.
set -euo pipefail

repo="${1:-/tmp/agentboard-try}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ ! -d "$repo/.git" ]; then
  echo "creating scratch repo: $repo"
  mkdir -p "$repo"
  git -C "$repo" init -q
  git -C "$repo" config user.email agentboard@example.com
  git -C "$repo" config user.name agentboard
  printf 'def greet(name):\n    return "hello, " + name\n\n\nif __name__ == "__main__":\n    print(greet("world"))\n' > "$repo/main.py"
  printf '# scratch project\n\nA tiny repo for trying agentboard by hand.\n' > "$repo/README.md"
  git -C "$repo" add -A
  git -C "$repo" commit -qm "initial"
fi

echo "agentboard → repo: $repo   agent: claude"
exec env AGENTBOARD_REPO="$repo" AGENTBOARD_AGENT=claude \
  "$root/ramune" run "$root/agentboard/agentboard.ts"
