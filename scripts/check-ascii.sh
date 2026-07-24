#!/usr/bin/env bash
# Fail if any tracked source file contains non-ASCII bytes. Guards against Unicode
# homoglyphs or other characters that could be used to disguise authorship; the
# code is deliberately plain ASCII.
set -euo pipefail
cd "$(dirname "$0")/.."

bad=0
while IFS= read -r f; do
  case "$f" in
    *.png|*.jpg|*.jpeg|*.gif|*.ico|*.pdf|*.bin|*.img|*.ext4) continue ;;
  esac
  if LC_ALL=C grep -nP '[^\x00-\x7F]' "$f" >/dev/null 2>&1; then
    echo "non-ASCII bytes in: $f"
    LC_ALL=C grep -nP '[^\x00-\x7F]' "$f" | head -3
    bad=1
  fi
done < <(git ls-files)

if [ "$bad" -eq 0 ]; then
  echo "ASCII_CLEAN: no non-ASCII bytes in any tracked source file"
else
  echo "ASCII_CHECK_FAILED"
  exit 1
fi
