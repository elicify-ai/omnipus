# Squad T report — issue #768

## Outcome

The freeze had two separate causes, both now covered:

- `list_jobs` read delegated-job recency from the lifecycle record, which is updated on lifecycle transitions rather than on each child transcript write. The child session's composed metadata was being persisted correctly; the roster read the wrong clock.
- The SPA agent endpoint already calculated live turn status, but the Agents screen fetched it only once and worker cards did not render an active state.

No issue #769 delegate-wait behavior was changed.

## Investigation and root cause

The observed transcript activity was not lost:

- `pkg/session/unified_api.go::UnifiedStore.AppendTranscriptStrict` and `pkg/session/unified_write.go::UnifiedStore.AppendTranscript` persist transcript entries and advance `UnifiedMeta.UpdatedAt`.
- `pkg/session/lifecycle.go::LifecycleStore.persistLocked` advances `LifecycleRecord.UpdatedAt` only when a lifecycle record itself is persisted.
- `pkg/tools/list_jobs_sources.go::collectSubagentRows` used only the lifecycle timestamp for `last_activity_at`. The writer was called and persisted; this surface did not read its result.
- `pkg/gateway/rest_agents.go::activeAgentIDSet` and `pkg/gateway/rest_agents.go::computeAgentStatus` already derive live agent status from `pkg/agent/turn.go::AgentLoop.GetActiveAgentIDs`.
- `src/components/screens/AgentListScreen.tsx::AgentListScreen` did not refresh that endpoint while open, and `src/components/agents/WorkerCard.tsx::WorkerCard` did not show the returned `active` state.

This contradicts a “writer never ran” theory: the live recency writer worked, while the two presentation paths either read the lifecycle clock or did not refresh/render the live status.

## RED receipt

Backend reproduction was added first as `pkg/agent/list_jobs_activity_test.go::TestListJobs_SubagentLastActivityAdvancesWithTranscript`. Before the product change, a real transcript append advanced session metadata but not the `list_jobs` row:

```text
expected last_activity_at: 2026-09-20T14:15:27Z
actual last_activity_at:   2026-09-20T14:13:27Z
list_jobs must advance last_activity_at after child transcript activity
FAIL
exit=1
```

The SPA reproductions also failed before the UI change:

```text
WorkerCard: Unable to find an element with the text: Running
AgentListScreen: expected fetchAgents to be called 2 times, but got 1
2 failed
exit=1
```

## Fix

### `list_jobs`

- Added `pkg/tools/list_jobs_sources.go::JobSessionActivityReader`, using lifecycle identity as the ownership check and composed session metadata as the live-recency source.
- `pkg/tools/list_jobs_sources.go::collectSubagentRows` requests recency only for nonterminal jobs and takes the later of session recency and lifecycle time, so telemetry cannot move backward.
- Missing, corrupt, or wrong-owner session metadata leaves the lifecycle timestamp in place and adds an explicit degradation note rather than silently presenting the fallback as current.
- `pkg/agent/loop_wire.go::agentLoopJobSessionActivityReader.LastActivityBySessionID` searches the shared and legacy agent stores using the same ownership ordering as session resolution. It rejects mismatched agent/workspace identity and does not mask a corrupt owning record with a duplicate ID in a later store.
- `pkg/agent/loop_wire.go::AgentLoop.wireJobRosterForAgent` wires the reader into the production `list_jobs` tool.

### SPA agent list

- `src/components/screens/AgentListScreen.tsx::AgentListScreen` refreshes agent runtime status every five seconds while the roster is open.
- `src/components/agents/WorkerCard.tsx::WorkerCard` renders a visible green `Running` state for an active worker. This is live turn state, not the separate heartbeat feature.

No REST, WebSocket, or persisted wire shape changed, so contract regeneration was not required.

## GREEN receipt

| Check | Result |
|---|---|
| `CGO_ENABLED=0 go test -v -count=1 -tags goolm,stdjson -run '^TestListJobs_SubagentLastActivityAdvancesWithTranscript$' -p 1 ./pkg/agent` | PASS, exit 0 |
| `CGO_ENABLED=0 go test -count=1 -tags goolm,stdjson -run '^TestListJobs_' -p 1 ./pkg/tools` | PASS, exit 0 |
| `npx vitest run src/components/agents/WorkerCard.test.tsx src/components/screens/AgentListScreen.test.tsx --reporter=verbose` | 47 passed, exit 0 |
| `npm run typecheck` | PASS, exit 0 |
| ESLint on the four changed SPA files | PASS, exit 0 |
| `make lint-guards` | All 27 guards passed, exit 0 |
| `make lint-budgets` | File and function budget gates passed, exit 0 |
| `git diff --check` | PASS, exit 0 |

The backend regression also covers missing metadata, wrong-owner session metadata, and the rule that an older session timestamp cannot move `last_activity_at` backward. The SPA test proves an idle worker becomes visibly `Running` after the five-second refresh without reloading the page.

## Revert-proof receipts

I temporarily reversed the production portion of each fix while leaving the new tests intact, ran the tests, and restored the exact patch through a shell trap.

Backend mutation:

```text
expected last_activity_at: 2026-09-20T16:18:44Z
actual last_activity_at:   2026-09-20T16:16:44Z
list_jobs must advance last_activity_at after child transcript activity
FAIL
exit=1
```

SPA mutation:

```text
WorkerCard: Unable to find an element with the text: Running
AgentListScreen: expected fetchAgents to be called 2 times, but got 1
2 failed, 45 skipped
exit=1
```

After restoration, the targeted backend test, the 47-test SPA run, typecheck, guards, budgets, and `git diff --check` all passed as recorded above.

## Issue #769 overlap

If a delegated child truly stops producing activity while the parent remains wedged in its wait loop, issue #768 telemetry should correctly become stale; it must not manufacture activity. That can make issue #769 visible, but this branch does not alter the delegate wait loop, cancellation, timeout, or completion behavior.

## Orchestrator Playwright check

1. Build and start the gateway from this branch, authenticate, and open the Agents screen before dispatching work.
2. Confirm the target worker does not show `Running` while idle.
3. From Mia, start an asynchronous native delegation to that worker which lasts more than ten seconds and produces multiple transcript or tool records.
4. Keep the Agents screen open without reloading. Confirm the worker shows `Running` within five seconds.
5. Invoke `list_jobs(kind=subagent)` twice, separated by a known child transcript or tool record. Confirm the same row's second `last_activity_at` is later than the first and later than `started_at`.
6. Let the child finish or stop it. Confirm `Running` disappears within five seconds.
7. If separately reproducing issue #769, confirm that an inactive child eventually appears stale; do not treat this branch as a #769 fix.

## Honest gaps

- Live Playwright acceptance was not run in this squad worktree; the orchestrator owns that gateway/UI check.
- The activity adapter probes each nonterminal session ID across deduplicated shared/legacy stores. It avoids a duplicate successful read and excludes terminal rows, but a true batch index would be more efficient. Adding that storage API is outside issue #768.
- The full Go suite was not run, as explicitly prohibited for this environment. Only scoped Go tests were run; CI remains the authority for the wider suite.
- No push, pull request, or CI run was performed.
- GitNexus reported 7 changed tracked files, 13 indexed symbols, 0 affected indexed processes, and low risk. Its index did not recognize every newly added symbol, so the scoped tests and manual call-path review remain the stronger evidence for this patch.

Code correct and tested: the focused backend and frontend regressions pass and fail when their production fixes are reversed.

Reachable by a user/agent: production `list_jobs` wiring is present and the Agents screen polls and renders live status; real gateway/UI reachability still requires the Playwright acceptance above.
