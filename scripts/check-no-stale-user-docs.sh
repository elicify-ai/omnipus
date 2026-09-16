#!/usr/bin/env bash
# check-no-stale-user-docs.sh — fail if a retired product surface reappears in USER documentation.
#
# Three things were removed from the product and must never come back in the handbook:
#   * the Command Center screen (deleted; scheduled work lives in a workspace's Calendar tab)
#   * Max as a core agent (retired; the base roster is Mia, Jim, Ava and Ray)
#   * "five teammates" (there are four base agents)
#
# Scope: markdown under docs/, EXCLUDING docs/internal/** — decision records and specs legitimately
# discuss what was removed and why. A user page never should.
#
# On "Max": the word is also the ordinary abbreviation of "maximum", which appears all over the
# operator pages ("Max tokens", "Max cached search results"). Matching a bare `Max` produced five
# false positives for every real one. So the check fires only where Max is used as an AGENT: next to
# another agent's name, or introduced as an agent in words.
#
# Exit: 0 clean, 1 a retired surface is present, 2 the check itself could not run.
set -uo pipefail

# ── self-test ───────────────────────────────────────────────────────────────
# The guard runner requires every guard to prove it can both PASS and FAIL.
# Run: scripts/check-no-stale-user-docs.sh --self-test
if [ "${1:-}" = "--self-test" ]; then
  me="$(cd "$(dirname "$0")" && pwd)/$(basename "$0")"
  fails=0
  run_case() { # <name> <expected-exit> <file> <content>
    local name="$1" want="$2" file="$3" body="$4"
    local t; t="$(mktemp -d)"
    mkdir -p "$t/scripts" "$t/docs/internal" "$t/$(dirname "$file")" 2>/dev/null
    cp "$me" "$t/scripts/$(basename "$me")"
    printf '# Page\n\nOrdinary text.\n' > "$t/docs/agents.md"
    printf '%b\n' "$body" >> "$t/$file"
    ( cd "$t" && bash "scripts/$(basename "$me")" >/dev/null 2>&1 ); local got=$?
    if [ "$got" = "$want" ]; then echo "  ok  $name (exit $got)"; else echo "  FAIL $name: expected exit $want, got $got"; fails=$((fails+1)); fi
    rm -rf "$t"
  }
  run_case "a clean handbook passes"                     0 docs/tasks.md  "Create a task on the board."
  run_case "the deleted Command Center is caught"        1 docs/tasks.md  "Open the Command Center to schedule work."
  run_case "'five teammates' is caught"                  1 docs/concepts.md "You get five teammates."
  run_case "Max in the roster is caught"                 1 docs/agents.md "Your roster is Mia, Jim, Ava, Ray and Max."
  run_case "Max introduced as an agent is caught"        1 docs/tools.md  "Delegate it to the agent Max."
  run_case "'Max tokens' is NOT a false positive"        0 docs/settings.md "| Max tokens | 4096 | Max cached results | Max concurrent searches |"
  run_case "internal documents are out of scope"         0 docs/internal/history.md "The Command Center was deleted and Max was retired."
  if [ "$fails" -eq 0 ]; then echo "check-no-stale-user-docs: self-test passed"; exit 0; fi
  echo "check-no-stale-user-docs: self-test FAILED ($fails)"; exit 1
fi

cd "$(dirname "$0")/.." || exit 2
hits=$(mktemp) || exit 2
trap 'rm -f "$hits"' EXIT

find_md() { find docs -name '*.md' -not -path 'docs/internal/*' -print0; }

scan() { # <flags> <pattern> <explanation>
  local flags="$1" pattern="$2" label="$3"
  while IFS= read -r line; do
    [ -n "$line" ] && printf '%s  <- %s\n' "$line" "$label" >> "$hits"
  done < <(find_md | xargs -0 grep -n $flags -- "$pattern" 2>/dev/null || true)
}

scan '-iE' 'Command Center'  'the Command Center screen was deleted'
scan '-iE' 'five teammates'  'there are four base agents, not five'
# Max only where it reads as an agent name.
scan '-E'  '(Mia|Jim|Ava|Ray)[^|]{0,60}\bMax\b'          'Max listed with the base roster; Max was retired'
scan '-E'  '\bMax\b[^|]{0,60}(Mia|Jim|Ava|Ray)'          'Max listed with the base roster; Max was retired'
scan '-iE' '(agent|teammate|assistant) Max\b'            'Max named as an agent; Max was retired'

if [ -s "$hits" ]; then
  cat "$hits"
  echo "FAILED: $(wc -l < "$hits" | tr -d ' ') retired-surface reference(s) in user documentation"
  echo "If a page genuinely needs to name one, it belongs in docs/internal/, not the handbook."
  exit 1
fi
echo "ok: no retired product surface in user documentation"
