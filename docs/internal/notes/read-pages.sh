#!/usr/bin/env bash
# Read paged notes one screen at a time without relying on terminal scrollback.
# Usage: bash docs/internal/notes/read-pages.sh [dir]
set -euo pipefail
DIR="${1:-docs/internal/notes/security-ux-pages}"
shopt -s nullglob
pages=("$DIR"/page-*.txt)
if ((${#pages[@]}==0)); then
  echo "No pages in $DIR" >&2
  exit 1
fi
i=0
total=${#pages[@]}
for f in "${pages[@]}"; do
  i=$((i+1))
  clear 2>/dev/null || true
  echo "======== page $i / $total  ($f) ========"
  cat "$f"
  echo ""
  if ((i<total)); then
    echo "-------- press Enter for next page (or Ctrl-C to stop) --------"
    read -r _
  else
    echo "======== end ========"
  fi
done
