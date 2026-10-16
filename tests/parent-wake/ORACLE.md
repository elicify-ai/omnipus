# Parent wake visibility — RED oracle

Spec: `docs/internal/specs/parent-wake-visibility-spec.md` (section 22 wins where it disagrees with section 21).
Expected values are from that spec, not from a run of today's code.
Step 4 items 2 (green) and 3 (mutation) and the proof-of-failability checklist are deferred to CHECK.

| Test | FR / BDD | Why it fails on today's code |
|---|---|---|
| `TestClassifyEnvelope_ProgressCheckpointArtifactDoNotWake` | FR-011, FR-012, FR-026. Chatter outline. Cap refusal. | `classifyEnvelope` sets progress, checkpoint and artifact `WakeEligible=true`. A `stopped_child:` prefix is also wake-eligible. The spec says neither wakes. Caps already refuse a second progress line; that half is a control. |
| `TestWakeChannel_MainBecomesWebchatStoredUntouched` | FR-001, FR-002. Stream-channel outline. | An ordinary root keeps `meta.Channel`. `main` and empty stay off `webchat`, and the chat id stays the empty peer instead of the session id. A steered child already streams on `webchat` (control). |
| `TestWakeSession_RejectsUnknownReason` | FR-018, PW-D10. Unknown reason. | `AgentLoop.WakeSession` does not exist. BLOCKED. |
| `TestHelperEventId_RepeatIsOneRow` | FR-022. One row per id. | Same missing method. The repeat-id assertion sits behind the probe. |
| `TestResumeSkipsHistoricalInvocationNotCallId` | FR-014, PW-D21, SC-005. | Boot marks a running native helper stopped and does not dispatch, so a new occurrence that reuses `call_0` never runs. This harness has no tool-executor hook. The historical pair's executor count is not observed here; a transcript row is not that count (PW-D21). CHECK should add the executor counter once resume calls tools. |
| `TestGoalOutcome_WrittenOnHelperAndParent` | FR-004. Goal met on the parent; helper keeps its row. | `recordGoalOutcome` writes the helper transcript only. The parent transcript has no `goal_outcome` row. Parent id shape is `goal-outcome-<parent>-<goal>-<nanos>`. |
| `TestDeliver_MetVerdictSaveThenPlace` | FR-005, FR-013, PW-D16, Test 6. | A met helper completes and the verdict inbox id is already acknowledged. The parent transcript has no `saved-input-<child>:<generation>:final` chat-membership row. Place-once, crash replay, and `ErrAlreadyConsumed` stay with the unwritten Tests 20–22. |
| `TestStopNotice_IntentionalStopNoWake` | FR-008, FR-010, PW-D13. | A running parent is woken for a landed Stop. The spec wakes nobody, including a replay, and writes one stopped row that says the helper will not resume. |
| `TestStopNotice_RestartWakesIdleParent` | FR-008, FR-009. | A completed parent is not woken for a restart notice, and the parent transcript has no "Interrupted by a restart." row. |
| `TestStopNotice_LandedStopWithoutMarkerBlocksWake` | FR-010, PW-D19. | Clearing the Stop marker on a landed stop still wakes, because the gate only looks at `Stop.Generation`. |
| `TestStopNotice_HistoricalTimeoutWakesNoResume` | FR-009. | An idle parent is not woken for a historical timeout notice. |
| `TestBoot_NativeHelperResumesFromHistory` | FR-014, PW-D6. | Boot leaves the helper stopped instead of dispatching it. |
| `TestBoot_ResumeFailureStillWakesOnce` | FR-015. | No "Interrupted by a restart." row and no "Resume did not start." row. |
| `TestBoot_UserStopNotResumed` | FR-010, Test 16. | Today's boot already does not dispatch a stopped helper. This test is a regression pin and may be green. The wake half of a user Stop is Test 10. |
| `TestBoot_ExternalHelperNotReplaced` | FR-016. | No parent restart row. Session count already stays put (control). |
| `TestWakeSession_NeverTurnedMain` | FR-017, Test 18. | `WakeSession` missing. BLOCKED. |
| `TestWakeSession_GoneParentNoSubstitute` | FR-019, PW-D12. | `WakeSession` missing. BLOCKED. |
| `TestProgress_DoesNotCallWakeParent` | FR-011, SC-004. | Delivering progress returns `woke`, not `stored_not_woken`. |
| `TestDeliver_QuestionBlockerFailureWakeOnce` | FR-025. | The wake may already happen. The parent transcript has no `helper_event` row carrying the question, blocker, or failure text. A second delivery must not wake again. |
| `TestDeliver_OrdinaryNonFatalDoesNotWake` | FR-026. | A `stopped_child:` notice is delivered as a wake. Ordinary non-fatal text and `max_tool_iterations:` are controls (no wake, and still a wake). |
| `TestWakeSession_CreatorNoticeOneWebchatTurn` | FR-017, PW-D8, Test 32. | `WakeSession` missing. BLOCKED. |
| `parent-wake-rows.test.ts` goal headline | FR-004, Test 25. | The store already inserts a `goal_outcome` frame into the frame's session bucket. This half may be green. The backend parent write is the Go test above. |
| `parent-wake-rows.test.ts` helper event | FR-022, Tests 24 and 33. | `handleFrame` ignores `type: helper_event`, so the restarted sentence never becomes one row. |
| `ParentWakeRows.test.tsx` | §10, §18, §19. | The rendered chat has no restarted disclosure, so it cannot announce it once, keep focus in the composer, or toggle it from the keyboard. The jsdom harness mounts `VirtualSystemMessageRow` (plain list). It does not mount the assistant-ui `SystemMessage` branch; that second renderer is a gap for CHECK. |
| `TestGetStreamer_MissingSessionDoesNotGuess` | FR-003, Test 29. | `GetStreamer` already refuses an empty session id (DEL-25). This test may be green. It is a regression pin, not a witness that a new wake path was missing. |
| `parent-wake-stream.spec.ts` | FR-003, Test 26, #1317. | Written for CI. Not run locally (no gateway binary build; the dispatch says e2e runs in CI). Expected red: the wake turn still streams on stored channel `main`, so neither browser shows `WOKEN_PARTIAL` before completion. |
| UAT checklist | UAT-1..6 | Written, not executed. `tests/uat/parent-wake-visibility-checklist.md`. |

## Planned tests not given their own function

| Planned test | Where the oracle is |
|---|---|
| Test 7 unmet verdict | Not written. The met harness above is not reused for an unmet round. |
| Test 8 not verified | Not written. The existing `notVerifiedConclusion` sentence was not asserted on a parent transcript. |
| Test 20 approval join and parked card | Not written. PW-D15's hold is not driven. |
| Test 21 crash save/place | Not written. |
| Test 22 polled final | Not written. |
| Test 30 write failure not shown | Not written. |
| Test 31 distinct wake ids | Not written. PW-D17's identity table is not asserted beyond the repeat-id probe. |
| Test 34 repair after compaction | Not written. The pending-save sentence is asserted only as absent on an empty thread. |

These gaps are real. They are not skipped tests. A `t.Skip` would hide them; a BLOCKED fatal for a symbol that is not the missing piece would be the wrong red.

## Legacy reconciliation — 2026-10-16

`TestClassifySessionMessage_AcceptedReportKindsWakeEligible` now agrees with this pack: progress, checkpoint and artifact **do not** wake (PW-D4). The legacy Stop/replay, native-restart, met-ending identity and progress-diagnostic expectations were reconciled to the same spec, without changing production. Legacy function names are retained for traceability; no tests were deleted or skipped. Compatible cap, Stop-authority, input-retention, row-identity and delivery-failure assertions remain. Implementation GREEN, safe-placement/mutation proof and the coverage gaps above still belong to a different CHECK instance; reconciliation is not a feature-completion claim.

## GitNexus

GitNexus MCP tools are not in this session. No production symbol was edited, so no impact run was required. Callers were not rewritten.
