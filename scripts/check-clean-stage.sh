#!/usr/bin/env bash
# Fail if any tracked (indexed) path matches a .gitignore pattern. Guards
# against a force-add (`git add -f`/`-A -f`, or an IDE "add all") silently
# staging build artifacts and binaries that .gitignore deliberately excludes
# (kernel/build/, bin/, bench/results/*.json, .DS_Store, ...). `git check-ignore`
# alone can't see this once a path is indexed -- ignore rules only govern
# untracked paths -- so this uses --no-index, which matches purely against the
# pattern regardless of index state.
set -euo pipefail
cd "$(dirname "$0")/.."

bad=0
while IFS= read -r f; do
  if git check-ignore --no-index -q -- "$f"; then
    echo "tracked path matches .gitignore: $f"
    bad=1
  fi
done < <(git ls-files)

if [ "$bad" -eq 0 ]; then
  echo "STAGE_CLEAN: no tracked path matches .gitignore"
else
  echo "STAGE_CHECK_FAILED: unstage these before committing (git reset -- <path>);"
  echo "they were likely force-added (git add -f / -A -f) bypassing .gitignore."
  exit 1
fi
