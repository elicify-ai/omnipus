# ADR-091 WP-F — Residual audit, deletions, closure

- **Decision record:** [ADR-091](../architecture/ADR-091-steered-sessions-replace-subagents.md) D10, AC-13
- **Landing order:** [adr-091-landing-order.md](adr-091-landing-order.md) — after A–E; CP-6 and CP-7
- **Owner files:** landing order §3, row F
- **Status:** Draft rev 2 (consolidated after three grills; supersedes every earlier sentence of rev 1)

## Summary

When the five building packages are done, this package proves nothing of the old mechanism is left, consolidates the one shared piece that must survive, updates the documentation that still describes sub-agents as a special case, and — after the delivery has merged — closes every issue it resolved with a citation. It is an audit with a fixed checklist and runnable checks, not open-ended cleanup.

## Existing codebase context

| Item | Symbol | Expected end state |
|---|---|---|
| External-CLI runner | `pkg/agent/external_dispatch.go::runExternalCLISubTurn` | **kept**, exactly one production caller (`task_executor_run.go::processTaskDirectExternalCLI`, parameterised by the edge); workspace enforcement, cancellation wiring, deadlines and transcript handling intact |
| Unused internal channel | `pkg/constants/channels.go::internalChannels["subagent"]` | removed; job-category `subagent` literals elsewhere **kept** |
| Per-site delegate booleans | any `depth == 0`, `IsTaskRun`, `parentSpawnCallID != ""` used to decide publication | removed in favour of I-5 |
| `ParentDurableKey` readers | 15 non-test files (ADR-091 D2, by owner) | every one reads the edge; the field is deleted by WP-A — this package verifies 0 production references |
| The three D11 containments | `deliverToolOutput` media predicate; hand-built `SendResponse: true` and `routingSessionID` restore in `processSystemMessage` | gone: audience (WP-B) and reconstruction (WP-A) replace them; no hand-built options remain in `loop_inbound.go` |
| Docs | `docs/AGENTS.md` ("Workers and delegation"), ADR-057 amendment banner, `pkg/agent/CLAUDE.md`, `pkg/tools/CLAUDE.md` | updated to the steered-session model |
| Budget rows | `scripts/budgets/files.txt` | rows for deleted files removed; rows for shrunk files re-keyed downward only |
| Issue tracker | #658, #614, #670, #755, #763, #764, #765 open today; #784, #803 open by design | closed with citations / left open with a reason — **after merge** (CP-7) |

## User stories and acceptance criteria

### US-1 — Nothing of the old mechanism remains (P0)

1. **Given** the tree after A–E, **When** the audit checks run, **Then** every deletion-manifest symbol returns the expected count in production code.
2. **Given** `runExternalCLISubTurn`, **When** production callers are counted, **Then** there is exactly one and the runner's enforcement paths are unchanged.
3. **Given** every publication boundary, **When** inspected, **Then** each decides audience only through I-5.
4. **Given** the guard script, **When** a banned symbol is reintroduced in a scratch branch, **Then** CI fails naming the guard (the guard's self-test proves this).

### US-2 — Documentation describes the product as it is (P1)

1. **Given** `docs/AGENTS.md`, **When** read, **Then** its delegation section says a worker runs in its own session, reports to the delegating agent, and can be opened like any session.
2. **Given** ADR-057, **When** read, **Then** its banner points to ADR-091 for D2(a) and D4.

### US-3 — Every issue this delivery solves is closed with a citation, after merge (P0)

*As the founder, I want the tracker to reflect what shipped, without hunting.* (ADR-091 AC-13; repo convention: release-branch PRs do not auto-close.)

1. **Given** the delivery has merged as PR N, **When** the closure script runs with `PR=N`, **Then** #658, #614, #670, #755, #763, #764 and #765 are each `CLOSED` with a comment that cites `#N` (or the merge commit) **and** an ADR-091 section.
2. **Given** #784 and #803, **When** the script runs, **Then** each is `OPEN` with a comment that names the ADR-091 section and says why it stays open.
3. **Given** any issue on the list whose fix is only partial, **When** the checklist runs, **Then** the comment states the remaining gap instead of closing it, and the script reports it.
4. **Given** the script's self-test fixtures (an unrelated `#12` comment; a random hex string; an "ADR-091" mention without a section), **When** the self-test runs, **Then** each is rejected.

## Machine-verifiable constraints

Conventions (landing order §5 item 6): every grep scans **production code only** (`--exclude='*_test.go'`; for `src/`, `--exclude='*.test.*'`); test files may hold a banned name as a negative fixture and the guard scripts hold their own banned-name lists. Each check is a script step that compares grep's count to the **exact** expected number; a count of 0 is confirmed by grep's no-match exit (1); any other non-zero exit is a failure of the check itself.

| Check | Command | Expected |
|---|---|---|
| Ring gone | `grep -rn -e newEphemeralSession -e maxEphemeralHistorySize -e ephemeralSessionStore pkg/ --include='*.go' --exclude='*_test.go'` | 0 |
| Wait-inline gone | `grep -rn -e executeSync -e DelegationModeAwait -e allow_blocking_question pkg/ contracts/ src/ --include='*.go' --include='*.yaml' --include='*.ts' --include='*.tsx' --exclude='*_test.go' --exclude='*.test.*'` | 0 |
| Prompt clean | `grep -rn 'async=false' pkg/agent/delegation_context.go` | 0 |
| Address not borrowed | `grep -rn -e parentTS.channel -e parentTS.chatID pkg/agent/ --include='subturn*.go' --exclude='*_test.go'` | 0 |
| SubTurn config gone | `grep -rn 'SubTurn\.' pkg/ --include='*.go' --exclude='*_test.go'` | 0 |
| One runner caller | `grep -rn 'runExternalCLISubTurn(' pkg/ --include='*.go' --exclude='*_test.go' --exclude='external_dispatch.go'` | **exactly 1** (baseline today: 2 production + 23 test calls) |
| ParentDurableKey gone | `grep -rn 'ParentDurableKey' pkg/ contracts/ --include='*.go' --include='*.yaml' --exclude='*_test.go'` | 0 |
| ProducingSessionID gone | `grep -n 'ProducingSessionID' pkg/agent/events.go pkg/gateway/websocket_forward.go` (scoped to the retired payloads and their readers; `steer.UpwardEvent` names its field `ChildSessionID`, so no other production use exists) | 0 |
| Internal channel | `grep -n '"subagent"' pkg/constants/channels.go` | 0 |
| Per-site booleans | `grep -rn 'depth == 0' pkg/agent/loop_run_turn_tools.go` | 0 |
| Sibling notifier gone | `grep -rn 'notifyParentIfAllSiblingsDone' pkg/ --include='*.go' --exclude='*_test.go'` | 0 |
| Nested replay gone | `grep -rn 'emitNestedToolCalls' pkg/ --include='*.go' --exclude='*_test.go'` | 0 |
| FR-047 guard gone | `test ! -e src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts` | exit 0 |
| Containments gone | `grep -c -e 'SendResponse:' -e 'routingSessionID' pkg/agent/loop_inbound.go` | 0 (reconstruction builds options; nothing hand-built remains) |
| Media gate is audience | not a grep — `persistToolResult` and `recordToolCompletion` legitimately test media presence for persistence and replay; containment is proven by WP-B's boundary-4 test (`TestBoundary_4_Contained`, media reaches no user address, `Observe` called) | test green |
| Budgets | `make lint-budgets` | green; no row grew |
| Guards | `make lint-guards` | green; `scripts/check-no-subagent-special-case.sh` present and self-tested |
| Issue closure (post-merge, CP-7) | `PR=<n> scripts/adr091-issue-closure.sh` | exit 0; see below |

The closure verifier is **not** a CI guard: it is named outside the `check-*.sh` pattern that `scripts/guards.sh` discovers, because it can only pass after the delivery has merged. It is run once at CP-7 by the person who closes the issues.

```bash
# scripts/adr091-issue-closure.sh — run after merge with PR=<merging PR number>
# Exit 0 = every issue in the expected state with a qualifying comment.
# Exit 1 = a tracker mismatch. Exit 2 = the GitHub API could not be read (never masked).
set -u
: "${PR:?set PR=<merging PR number>}"
repo=elicify-ai/omnipus
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fail=0
fetch() {  # $1 issue, $2 field — writes to a file and checks gh's own exit status explicitly
  if ! gh issue view "$1" --repo "$repo" --json "$2" -q ".$2 | if type==\"array\" then .[].body else . end" > "$tmp/$1.$2" 2> "$tmp/$1.$2.err"; then
    echo "gh failed for #$1 ($2): $(cat "$tmp/$1.$2.err")"; exit 2
  fi
}
# a qualifying closure comment names the merging PR AND a real ADR section, e.g. "#812 … ADR-091 §D3"
cite_re="(#${PR}\b.*ADR-091 §[A-Z0-9][A-Za-z0-9.-]*)|(ADR-091 §[A-Z0-9][A-Za-z0-9.-]*.*#${PR}\b)"
open_re="ADR-091 §[A-Z0-9][A-Za-z0-9.-]*.*stays open because"
for n in 658 614 670 755 763 764 765; do
  fetch "$n" state; fetch "$n" comments
  s=$(cat "$tmp/$n.state"); ok=$(grep -c -E "$cite_re" "$tmp/$n.comments" || true)
  [ "$s" = "CLOSED" ] && [ "$ok" -ge 1 ] || { echo "issue #$n: state=$s citing-comments=$ok"; fail=1; }
done
for n in 784 803; do
  fetch "$n" state; fetch "$n" comments
  s=$(cat "$tmp/$n.state"); ok=$(grep -c -E "$open_re" "$tmp/$n.comments" || true)
  [ "$s" = "OPEN" ] && [ "$ok" -ge 1 ] || { echo "issue #$n: state=$s reason-comments=$ok"; fail=1; }
done
exit $fail
```

Self-test (`scripts/adr091-issue-closure.selftest.sh`): stubs `gh` on `PATH` with fixture JSON and asserts, one case each, that the script **rejects**: an unrelated `#12` comment; a bare hex string; `#<PR> ADR-091 §` with no section identifier; an `ADR-091` mention without `§`; an open-issue comment without "stays open because"; a comment citing the right section but a different PR number; **exits 2** when `gh` prints a qualifying comment and then exits non-zero, and when every API call fails — and **accepts** `"Closed by #<PR> — ADR-091 §D3"` and `"ADR-091 §12: stays open because content-level quoting is a separate decision"`.

## TDD plan

Implementers load the `test-driven-development` skill first, as every other package does; here the "failing test first" is the audit test written against the expected end state before the deletions land. This package's tests are the audit checks above, encoded as one Go test (`TestADR091_ResidualAudit`) that runs each grep against the tree and compares the count to the exact expectation, plus a guard script `scripts/check-no-subagent-special-case.sh` registered with `scripts/guards.sh` so the deletions cannot be reintroduced by a later merge (the "retired surfaces" pattern in `CLAUDE.md`), plus the closure script and its self-test.

| Order | Test | Level | Traces to |
|---|---|---|---|
| 1 | `TestADR091_ResidualAudit` (every grep row; exact counts; exit-code handling) | Unit (static) | US-1/AS-1, AS-3 |
| 2 | `TestExternalRunner_SingleProductionCaller` | Unit (static) | US-1/AS-2 |
| 3 | `check-no-subagent-special-case.sh` + self-test (reintroduce each banned symbol in a temp tree → guard fails naming it) | Guard | US-1/AS-4 |
| 4 | docs link check + a grep that `docs/AGENTS.md` contains "own session" in the delegation section | Unit | US-2 |
| 5 | `adr091-issue-closure.selftest.sh` | Script self-test | US-3/AS-4 |
| 6 | CP-7 execution record: the script's output with `PR=<n>`, attached to the closing comment on the last issue | Operational | US-3/AS-1,2,3 |

## Functional requirements

| ID | Requirement |
|---|---|
| FR-F-001 | Production code MUST contain no deletion-manifest symbol after landing; each audit check MUST state an exact expected count and compare to it. |
| FR-F-002 | `runExternalCLISubTurn` MUST remain with exactly one production caller and unchanged enforcement. |
| FR-F-003 | A guard script MUST prevent reintroduction of the ring, wait-inline, the borrowed address, `ParentDurableKey`, `ProducingSessionID` (scoped to `events.go` and `websocket_forward.go`), the sibling notifier and `emitNestedToolCalls`, and MUST have a self-test proving it fails on each; it MUST NOT ban media-presence checks used for persistence or replay. |
| FR-F-004 | User and module documentation MUST describe the steered-session model. |
| FR-F-005 | After merge, every solved issue MUST be `CLOSED` with a comment citing the merging PR number **and** a real ADR-091 section identifier; #784 and #803 MUST be `OPEN` with a comment naming the section and "stays open because …"; verified by `scripts/adr091-issue-closure.sh`, which is not a CI guard, checks every `gh` call's exit status explicitly (exit 2 on any API failure), and has a self-test with the listed negative fixtures. |
| FR-F-006 | Every audit grep MUST scan production code only, treat grep's no-match exit as the pass for an expected 0, treat any other failure as a failed check, and MUST NOT rely on prose markers as oracles. |

## Success criteria

| ID | Criterion |
|---|---|
| SC-F-1 | All audit checks pass in CI with their exact counts. |
| SC-F-2 | The guard's self-test proves it fails on every reintroduced symbol. |
| SC-F-3 | The closure script exits 0 at CP-7 with `PR=<n>`, and its self-test rejects all five negative fixtures. |

## Traceability

| Requirement | Story | Tests |
|---|---|---|
| FR-F-001 | US-1 | 1 |
| FR-F-002 | US-1 | 2 |
| FR-F-003 | US-1 | 3 |
| FR-F-004 | US-2 | 4 |
| FR-F-005 | US-3 | 5, 6 |
| FR-F-006 | US-1 | 1 |

ADR ACs covered: AC-10, AC-13; AC-11 (the containments' removal is proven by the "Containments gone" and "Media gate is audience" checks together with WP-B's boundary tests).

## Non-goals

No behaviour change. No refactoring beyond consolidating the runner's callers.

## Holdout evaluation scenarios (not for development)

1. Read `docs/AGENTS.md` as a new user: the delegation section matches what you see in the product.
2. Reintroduce `newEphemeralSession` in a scratch branch: CI fails with a named guard.
3. After merge, open each of the seven issues: closed, with a comment naming the PR and the ADR section; open #784 and #803: one comment each saying why they stay open.

## Definition of done

1. *Code correct and tested:* audit test, guard and closure self-test green; budgets and guards green.
2. *Reachable:* n/a for code — this package changes no user-facing behaviour; state that explicitly in the report.
3. *Closed (CP-7, after merge):* the closure script executed with the merging PR number and its output recorded.
