#!/usr/bin/env bash
# check-no-reasoning-channel.sh
#
# Regression guard for the D1 deletion of the messenger reasoning channel
# (docs/internal/specs/thinking-reasoning-spec.md, US-9, Group F scenario
# "The messenger reasoning channel leaves no trace").
#
# This script fails the build if any of the deleted surfaces comes back:
#
#   1. The per-channel config key `reasoning_channel_id` and its env var
#      spellings (OMNIPUS_CHANNELS_*_REASONING_CHANNEL_ID).
#   2. The publish-path symbols `handleReasoning` / `spawnReasoningPublish`.
#   3. Any reasoning-channel mention in the 13 connector docs, the channels
#      README, and the AS-IS architecture doc.
#   4. The shipped config files' reasoning-channel keys
#      (config/config.example.json, pkg/gateway/config.json).
#
# WHY A SCRIPT AND NOT JUST A NOTE
#
# A note in CLAUDE.md tells a human. It does not stop `git merge`. Branches
# cut before the D1 deletion still contain the reasoning channel; merging or
# rebasing any of them re-adds it as an ordinary, conflict-free addition.
# Resolve by keeping the deletion.
#
# UNLIKE check-no-shell-deny-patterns.sh, this guard drops NO comment lines:
# its primary surfaces are prose docs and JSON, where "# " prefixes are
# markdown headings, not comments — dropping them would manufacture a green
# on a heading like "# Reasoning channel". Every match is an offender. The
# only exclusions are the enforcement surfaces themselves, excluded by exact
# path:
#
#   - scripts/check-no-reasoning-channel.sh            (this file)
#   - scripts/check-no-reasoning-channel-selfcheck.sh  (its companion)
#   - pkg/config/reasoning_channel_zero_trace_test.go  (the Go sweep)
#
# Exit: 0 clean, 1 offenders found, 2 the check itself could not run.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
cd "$REPO_ROOT" || { echo "check-no-reasoning-channel: cannot cd to $REPO_ROOT" >&2; exit 2; }

for d in pkg cmd src contracts docs config; do
  if [ ! -d "$d" ]; then
    echo "check-no-reasoning-channel: expected directory '$d' not found under $REPO_ROOT" >&2
    echo "  (wrong cwd, a renamed package, or a partial checkout — refusing to report a green" >&2
    echo "   verdict for a tree this script never actually scanned)" >&2
    exit 2
  fi
done

# Format: <pattern>::<space-separated roots>::<extended-regex of paths to skip>
# The exclude regex matches against the full "path:line:content" grep output
# line. $^ never matches, i.e. no exclusions beyond the rule's own skip field.
RULES=(
  # 1. The deleted Go/publish-path symbols and the config key in code and
  #    wire/config files. The zero-trace Go sweep covers the whole tree; this
  #    rule keeps CI coverage of the code roots even where the Go sweep does
  #    not run.
  'ReasoningChannelID|reasoning_channel_id|REASONING_CHANNEL_ID|spawnReasoningPublish|handleReasoning::pkg cmd src contracts::^pkg/config/reasoning_channel_zero_trace_test\.go:'
  # 2. The doc surfaces the spec names: all 13 connector docs, the channels
  #    README, and the AS-IS architecture doc. Prose and case variants alike:
  #    "reasoning channel", "reasoning-channel", the snake_case key, the
  #    all-caps env var, and the camelCase Go identifier quoted in prose.
  '(?i)reasoning[ _-]channel|ReasoningChannelID|spawnReasoningPublish|handleReasoning::docs/connectors pkg/channels/README.md docs/internal/architecture/AS-IS-architecture.md::$^'
  # 3. The shipped config files (Group F sweep list).
  '(?i)reasoning_channel_id::config pkg/gateway/config.json::$^'
)

HITS=""
for rule in "${RULES[@]}"; do
  pattern="${rule%%::*}"
  rest="${rule#*::}"
  roots="${rest%%::*}"
  skip="${rest##*::}"
  # shellcheck disable=SC2086 — roots is an intentional space-separated list.
  found="$(grep -rInE "$pattern" \
    --include='*.go' --include='*.ts' --include='*.tsx' --include='*.json' --include='*.md' \
    --include='*.yaml' --include='*.yml' \
    $roots 2>/dev/null | grep -vE "$skip" || true)"
  [ -n "$found" ] && HITS="${HITS}${found}"$'\n'
done

OFFENDERS="$(printf '%s\n' "$HITS" | grep -v '^\s*$' || true)"

if [ -n "$OFFENDERS" ]; then
  echo "check-no-reasoning-channel: FOUND reasoning-channel traces in the tree:" >&2
  echo "" >&2
  printf '%s\n' "$OFFENDERS" >&2
  echo "" >&2
  echo "The messenger reasoning channel was deleted deliberately (spec" >&2
  echo "thinking-reasoning-spec.md US-9, founder decision D1): thinking is a" >&2
  echo "web-chat feature; the per-channel reasoning_channel_id settings, the" >&2
  echo "publish path, its env vars, its connector docs and the fix to that path" >&2
  echo "are gone outright — no shim, no deprecation comment." >&2
  echo "" >&2
  echo "If you hit this after a merge or rebase from an older branch: that branch" >&2
  echo "predates the deletion and git re-added the machinery as an ordinary" >&2
  echo "addition. RESOLVE BY KEEPING THE DELETION." >&2
  echo "" >&2
  echo "If a reasoning channel is genuinely needed again, that reverses an" >&2
  echo "accepted spec decision: supersede the spec first, then update this" >&2
  echo "guard in the same commit." >&2
  exit 1
fi

echo "check-no-reasoning-channel: OK (reasoning-channel config key, env bindings, publish path, doc mentions and shipped-config keys absent per US-9/D1)"
exit 0
