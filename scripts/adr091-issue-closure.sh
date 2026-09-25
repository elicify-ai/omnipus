#!/usr/bin/env bash
# scripts/adr091-issue-closure.sh
#
# Run once at CP-7 after the ADR-091 delivery has merged, with the merging
# PR number as PR=<n>. Verifies the tracker matches what shipped: each issue
# the delivery closes carries a comment that names the merging PR AND a real
# ADR-091 section identifier; the two issues left open by design carry a
# comment that names the ADR-091 section and says why it stays open.
#
# Exit codes (per ADR-091 WP-F US-3):
#   0  every issue in the expected state with a qualifying comment
#   1  a tracker mismatch (wrong state, or no qualifying comment)
#   2  the GitHub API could not be read (gh failed) — never masked by 1
#
# This file is intentionally outside the scripts/check-*.sh guard pattern
# discovered by scripts/guards.sh: it can only pass once the delivery has
# merged, so it is not a CI gate. It is run by the person who closes the
# issues and its output is recorded on the closing issue comment.

set -u
: "${PR:?set PR=<merging PR number>}"
repo=elicify-ai/omnipus
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fail=0

fetch() {  # $1 issue, $2 field — writes to a file and checks gh's own exit status explicitly
  if ! gh issue view "$1" --repo "$repo" --json "$2" -q ".$2 | if type==\"array\" then .[].body else . end" > "$tmp/$1.$2" 2> "$tmp/$1.$2.err"; then
    echo "gh failed for #$1 ($2): $(cat "$tmp/$1.$2.err")" >&2
    exit 2
  fi
}

# a qualifying closure comment names the merging PR AND a real ADR section, e.g. "#812 … ADR-091 §D3"
cite_re="(#${PR}\b.*ADR-091 §[A-Z0-9][A-Za-z0-9.-]*)|(ADR-091 §[A-Z0-9][A-Za-z0-9.-]*.*#${PR}\b)"
# a qualifying open-issue comment names a real ADR section and says why the issue stays open
open_re="ADR-091 §[A-Z0-9][A-Za-z0-9.-]*.*stays open because"

# Issues the delivery closes. Each must be CLOSED with a comment matching cite_re.
for n in 658 614 670 755 763 764 765; do
  fetch "$n" state; fetch "$n" comments
  s=$(cat "$tmp/$n.state"); ok=$(grep -c -E "$cite_re" "$tmp/$n.comments" || true)
  [ "$s" = "CLOSED" ] && [ "$ok" -ge 1 ] || { echo "issue #$n: state=$s citing-comments=$ok" >&2; fail=1; }
done

# Issues the delivery leaves open by design. Each must be OPEN with a comment matching open_re.
# 803 was CLOSED by founder decision 2026-09-25, not left open: its premise was
# disproved. A delegated worker does not run out of context — AgentLoop::windowTrim
# evicts the oldest turns proactively before the provider call, and buildBreadcrumb
# hands the agent an explicit list of what was evicted, while the full archive is
# retained. There is no cliff to warn a parent about, so there is nothing to assert
# a "stays open because" reason for. 784 remains, and genuinely cannot be fixed at
# this layer (see its comment).
for n in 784; do
  fetch "$n" state; fetch "$n" comments
  s=$(cat "$tmp/$n.state"); ok=$(grep -c -E "$open_re" "$tmp/$n.comments" || true)
  [ "$s" = "OPEN" ] && [ "$ok" -ge 1 ] || { echo "issue #$n: state=$s reason-comments=$ok" >&2; fail=1; }
done

exit $fail
