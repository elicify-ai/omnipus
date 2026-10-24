# G3 heavy100k test placement — developer workflow dependency

## Why move, not weaken

Candidate7d2d03675 Go group1 log /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squad-1266/ci-7d2d03675/120316210599.log records package timeout10m, with TestChatHistoryIndex_HealthyCutAndBoundedReads running4m52s and its then-active prefix100000_limit100 child running21s. The4m52s is the parent matrix duration, NOT the individual child's duration. Each prefix/limit case constructs its own real90-day fixture, so repeating100k setup consumes the ordinary package's budget. This is test placement, not a product timing threshold or a waiver of failure.

QA extracts the original assertion body unchanged into historyHealthyCutAndBoundedReads. Ordinary TestChatHistoryIndex_HealthyCutAndBoundedReads keeps1k limit40/100 plus ALL0/1/63/64/65 recovery and missing-baseline cases. Both100k limit40/100 cases move intact to TestChatHistoryIndex_HealthyCutAndBoundedReads100K in the new test-only history_heavy build-tag file. No skip, smaller fixture, removed row/byte/position/read oracle, broader tolerance or timeout-based behavioral assertion. The shared function ensures both lanes keep identical exact IDs/flags/actual body-I/O assertions.

## Required workflow change — implementing developer only

Source read: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/.github/workflows/pr.yml::history-e2e-fixtures (display name Prepare history E2E fixtures and measurement), current fixture build step and T18 measurement step. Add one explicit sequential step between official fixture preparation and Section-H measurement, using the candidate's pinned Go from setup-go/go.mod and the same CGO_ENABLED=0 job environment:

```bash
CGO_ENABLED=0 go test -tags goolm,stdjson,history_heavy -count=1 -v -p 1 -timeout 30m -run '^TestChatHistoryIndex_HealthyCutAndBoundedReads100K$' ./pkg/session > build/tools/history-index-100k-test.log 2>&1
rc=$?
printf 'exit=%s\n' "$rc" >> build/tools/history-index-100k-test.log
# The shell must propagate rc before evaluating named-run assertions.
# On rc0, require BOTH exact named PASS lines, not a vacuous zero-test green:
# TestChatHistoryIndex_HealthyCutAndBoundedReads100K/prefix100000_limit40
# TestChatHistoryIndex_HealthyCutAndBoundedReads100K/prefix100000_limit100
exit "$rc"
```

The developer must accommodate the workflow shell's errexit so the raw exit is always saved (for example set +e only around the go command, then capture rc and restore set -e). Require both named children and no skips; missing names fail the step. Preserve/upload history-index-100k-test.log on every outcome alongside the existing history-measurement log. This dependency must land with this test split BEFORE candidate CI; absent wiring remains a coverage gap, not a pass. QA does not edit workflow or runner production scripts.

The CI target is **history-e2e-fixtures**, not Go Tests(group1) and not a browser shard. /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/tests/e2e/shards.json::ui-heavy already assigns chat-history-paging.prefix-and-real-session-evidence.spec.ts, but that Playwright shard does not execute Go tests. No shard reassignment is needed. The existing CI aggregate includes history-e2e-fixtures, so a required heavy-step failure must remain red there. Fly needs equivalent targeted wiring only if separately commissioned; no Fly or CI run is started by QA.

## Verification limits

Locally run only a quick ordinary1k subcase and heavy-tag discovery/compile (list the100k entry WITHOUT executing it), serial through the shared slot wrapper. Actual100k execution belongs exclusively to the heavy job. The test split's source correctness and discovery are separate claims from workflow wiring and execution; report each explicitly. Exact CI job change is handed to the developer; until the edited workflow and its saved named run are read, heavy coverage execution is UNVERIFIED.
