#!/usr/bin/env bash
# Builds the mergecanary demo/test fixture under DIR (must not exist or be empty):
#   DIR/repo     main checkout (base branch "main")
#   DIR/agent-1  worktree: adds code calling getUser(id)
#   DIR/agent-2  worktree: unrelated harmless feature
#   DIR/agent-3  worktree: renames getUser -> fetchUser
# Each branch passes its own tests and merges cleanly with main;
# agent-1 + agent-3 together fail to compile.
set -euo pipefail

DIR=${1:?usage: make-fixture.sh DIR}
SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")/fixture" && pwd)"
mkdir -p "$DIR"
DIR="$(cd "$DIR" && pwd)"
[ -z "$(ls -A "$DIR")" ] || { echo "make-fixture: $DIR is not empty" >&2; exit 1; }

git init -q -b main "$DIR/repo"
cd "$DIR/repo"
git config user.name fixture
git config user.email fixture@example.invalid
git config core.autocrlf false
cp "$SRC"/base/* .
git add -A && git commit -q -m "base: getUser"

for n in 1 2 3; do
  git worktree add -q -b "agent-$n" "$DIR/agent-$n" main
  cp "$SRC/agent-$n"/* "$DIR/agent-$n"/
  git -C "$DIR/agent-$n" add -A
  git -C "$DIR/agent-$n" commit -q -m "agent-$n work"
done
echo "fixture ready in $DIR"
