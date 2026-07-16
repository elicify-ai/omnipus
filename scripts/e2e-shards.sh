#!/usr/bin/env bash
#
# e2e-shards.sh — single entry point for the E2E shard plan.
#
# The plan itself lives in tests/e2e/shards.json (the single source of truth).
# This script is consumed by BOTH CI surfaces so they can never drift apart:
#
#   - .github/workflows/pr.yml       → `e2e-shards.sh matrix`  (GitHub Actions
#                                        strategy matrix JSON, fed to fromJSON)
#   - deploy/ci-worker/runci.sh      → `e2e-shards.sh list`    (one TSV row per
#                                        shard, fanned out as parallel gateways)
#
# Subcommands:
#   check   Assert every tests/e2e/*.spec.ts is assigned to EXACTLY one shard and
#           every assigned path exists on disk. Exit 1 on any drift. This is the
#           guard against a spec silently never running (false green) or the plan
#           referencing a deleted spec. `matrix` and `list` run it implicitly.
#   matrix  Emit the GitHub Actions strategy matrix: {"include":[{group,key_slot,specs}]}
#           where specs is the space-joined spec list for that shard.
#   list    Emit "group<TAB>key_slot<TAB>port<TAB>space-joined-specs", one row per shard.
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLAN="$ROOT/tests/e2e/shards.json"
CMD="${1:-check}"

[ -f "$PLAN" ] || { echo "e2e-shards: plan not found: $PLAN" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "e2e-shards: jq is required but not installed" >&2; exit 2; }

# Sorted list of every spec path assigned across all shards (with duplicates kept,
# so `check` can detect a spec assigned to more than one shard).
assigned_specs() { jq -r '.shards[].specs[]' "$PLAN" | sort; }

# Sorted list of every spec file that actually exists on disk.
disk_specs() { (cd "$ROOT" && ls tests/e2e/*.spec.ts) | sort; }

check() {
  local assigned disk dup missing extra
  assigned="$(assigned_specs)"
  disk="$(disk_specs)"

  # A spec assigned to two shards would run twice (and, worse, invites cross-shard
  # state assumptions). Not allowed.
  dup="$(printf '%s\n' "$assigned" | uniq -d)"
  if [ -n "$dup" ]; then
    echo "e2e-shards: FAIL — spec(s) assigned to more than one shard:" >&2
    printf '  %s\n' $dup >&2
    exit 1
  fi

  # On disk but assigned nowhere → it would SILENTLY NEVER RUN (false green). This is
  # the exact drift that let 7 specs stop running in the old hardcoded pr.yml matrix.
  missing="$(comm -23 <(printf '%s\n' "$disk") <(printf '%s\n' "$assigned" | uniq))"
  if [ -n "$missing" ]; then
    echo "e2e-shards: FAIL — spec file(s) on disk are not assigned to any shard (they would never run):" >&2
    printf '  %s\n' $missing >&2
    echo "  Fix: add each path to exactly one shard in tests/e2e/shards.json." >&2
    exit 1
  fi

  # Assigned but not on disk → a dead reference (renamed/deleted spec). Fail loudly
  # instead of letting `playwright test <missing>` error deep into the run.
  extra="$(comm -13 <(printf '%s\n' "$disk") <(printf '%s\n' "$assigned" | uniq))"
  if [ -n "$extra" ]; then
    echo "e2e-shards: FAIL — shard plan references spec file(s) that do not exist:" >&2
    printf '  %s\n' $extra >&2
    echo "  Fix: remove each stale path from tests/e2e/shards.json." >&2
    exit 1
  fi

  echo "e2e-shards: OK — $(printf '%s\n' "$disk" | wc -l | tr -d ' ') spec file(s), each assigned to exactly one shard." >&2
}

matrix() {
  check
  jq -c '{include: [.shards[] | {group, key_slot, specs: (.specs | join(" "))}]}' "$PLAN"
}

list() {
  check
  jq -r '.shards[] | [.group, .key_slot, (.port | tostring), (.specs | join(" "))] | @tsv' "$PLAN"
}

case "$CMD" in
  check)  check ;;
  matrix) matrix ;;
  list)   list ;;
  *) echo "usage: e2e-shards.sh {check|matrix|list}" >&2; exit 64 ;;
esac
