# Feature Specification: Notifications and chat system messages

**Created**: 2026-10-30 · **Revised**: 2026-10-31 (grill round-1 fix); 2026-11-01 (grill round-2 correction — the one final fix round; no third grill); 2026-11-05 (founder answers NS1, OQ-1, OQ-2 applied as D81–D83; W1 applied as D84); 2026-11-15 (**#1857 amendment — draft for review**: §13.8 turn-failure kinds and wording, FR-092/FR-093, §30.4 open questions, D85 placeholder — pending founder review); 2026-11-15 (**#1857 correction pass 1** after the architect read: the curated-text-on-`unknown` mechanism fixed (F-1), per-surface table added (§13.8.4), CLI classification pinned to a CLI-only function (R-2), A4/A5 narrowed per R-4, FR-093 restated to the no-stderr-on-the-wire status quo (R-7/OQ-1857-7), missing kinds M1–M10 added, wording fixes W1–W15, Mac team comment folded in); 2026-11-15 (**#1857 grill round-1 fix**: save stages split by observed producer (MAJ-001), resume/workspace causes separated (MAJ-002), parent surfaces separated — lifecycle summary / completion message / tool result (MAJ-003), the unknown-guard oracle corrected and split (MAJ-004), the §13.8.5 disposition matrix added (MAJ-005), integration tests from the real adapters + UAT rows (MAJ-006), upstream-refused attribution restored to `ambiguous` per R-2 (MAJ-007), nonfatal CLI diagnostics given their own state (MAJ-008); MIN-001…003 applied; §30.4 holds Q1–Q4 for the founder — until the answers the spec text follows the status quo); 2026-11-15 (**#1857 grill round-2 final fix** — the one final fix round: every save/goal disposition bound to the exact failed operation and caller, not the shared reporter (R2-MAJ-001) — the terminal continuation-stop failure split from the continuing receipt loss (new B23), finalization-without-an-answer (new B21) and the active-turn goal-read refusal (new B22) given their own rows, the completed-answer save bound to the finalize branches including the direct `Sessions.Save` emission, the late gateway failure marked turn-failing (B20); the builtin-only duplicate exemption removed — one code `tool_assembly_duplicate`, attribution `ambiguous`, two truthful variants, no generic negative control (R2-MAJ-003); B16 completed to four classes with the new `delegation_start_failed` storage refusal and W14's `{cli}` slots fixed (R2-MAJ-004); C11's baseline corrected — a nonfatal diagnostic currently emits/persists an error line — and the Settings disposition pinned with its retained failed-test behaviour (R2-MAJ-005); W15's re-run advice replaced with check-the-existing-result (R2-MAJ-006); the duplicated-argument Settings acceptance case added (R2-MIN-001); recounted 41 kinds, 27 can end generic today; §36 holds the round-2 dispositions); 2026-11-15 (**#1857 narrow correction after the independent check** — chief ruling A, not a new grill: every execution-state claim re-bound to the caller that decides it — B11's "answered" gated on a non-empty model response the emitter must carry (the `appendFinalModelResponse` branch runs for empty and default-response turns too), the direct `Sessions.Save` failure moved to B21 with no answer-state fact (it emits via `TranslateLLMError`, not the shared reporter, and is shared by stopped-silent, truncated-empty, default-response and answered turns), the stopped-silent-calls-the-reporter claim withdrawn, B14's "no active turn" scoping corrected (the mid-turn rubric-budget note reports inside an active turn and the turn continues), B7 bound to the root admission with the mid-turn steering-admission producer given its own truthful variant, B16's classes re-partitioned by error identity (`errors.Is` on the sentinels — the resume sentinel is returned from `reserveExternalConversation`, `beginExternalRun` and `driverForRun`, not only `startAndDrain`), B10's variants re-keyed to MCP-involved vs no-MCP-involved (all the code records is `CategoryMCP` vs everything else — "operator-configured"/"internal defect" removed as not decided by code), B19's "non-terminal" qualified by the shared reporter's `markTurnFailed` flag, B21/B22/B23 proposed copy reworded so no blind re-run is invited after earlier tool rounds, the six remaining `messageNotSavedError` producers given a named residual row B24 in the T-54 allow-list with per-site reasons, and `agent_request_limit` removed from the matrix-code list until OQ-1857-5 decides its scope; recounted 42 kinds, 28 can end generic today); 2026-11-18 (**#1857 amendment — founder answers applied (spec only)**: E1 = OQ-1857-1 **B** — an outside program's own error text never reaches the chat, fixed safe sentences only; E2 = OQ-1857-5 **A** — the own-rate-limiter correction B3/W11 is in scope, `agent_request_limit` restored to the contract list with its matrix row and oracle; E3 = OQ-1857-7 **A by chief ruling** (not answered by the founder's form; ruled to fit E1 B — technical details carry only program name, exit status, failure kind); E4 = OQ-1857-8 **A** — the pattern is kept, W1–W16 stay proposed until the founder reviews the corrected examples, exact-copy manifest tests wait for that review; E5 — the duplicate-flag prevention follow-up is issue #1880; E6 — no issue filed for the reserved `delegated_task_limit`, filing promises withdrawn; sec2 A — the two raw-error-text failure cases folded in as B25/B26 under the fail-closed copy rule, raw text in the server log only, and the already-compliant `stop_pending`/`secret_resume_pending` producers named; Settings-path gap G7 added to §37; recounted 44 kinds, 30 can end generic today)
**Status**: Approved
**Status notes**: Approved (founder, 2026-11-07) — round-2 correction applied (R2-MAJ-002…014 and R2-MIN-001…004; dispositions in §34); founder decisions D63–D80 unchanged (§32); the items held for the founder are answered and applied: NS1 = A → D81 (R2-MAJ-001, mask the whole response before first-line extraction), OQ-1 = A → D82, OQ-2 = A → D83, W1 = A → D84 (server-loss banner and chat line, §11.3) (§30.2, §30.3, §32) — **amendment for #1857 in review** (draft: §13.8 not yet founder-approved; §30.4 open; D85 placeholder)
**Author**: architect (spec author), for team-lead (Mac squad)
**Branch / base**: `landing/notifications`, created from `release/v0.1.1` at `4cad412bf`; requirements merged at `e21febf4f`; round-0 spec `eafa2b1c5`; voice skill landed at `e77af6cc9`
**Change size**: feature (structural, cross-tree: `contracts/`, `pkg/notifications`, `pkg/gateway`, `pkg/agent`, `pkg/providers`, `src/`, user docs under `docs/`). Flow: grill round 2 done (the last spec grill) → round-2 correction → founder answers to NS1, OQ-1, OQ-2 applied (D81–D83) → founder answer W1 = A applied (D84, 2026-11-05) → RED/GREEN/CHECK → **one** 5-reviewer gate on the one frozen Part-1 cut (D63) → candidate CI → hands-on UAT on the exact commit (touches `pkg/agent` and gateway WebSocket handlers) → founder's yes → one landing.
**Input (founder-confirmed)**:
- Requirements, stable `R-…` ids, Decisions Log D1–D62, build order: `docs/internal/specs/notifications-and-chat-system-messages-requirements-2026-10-22.md` (cited below as **REQ**)
- Agreed wording baseline: `docs/internal/specs/notifications-and-chat-system-messages-wording-2026-10-22.md` (cited as **WORDING**)
- The Omnipus voice skill: `.claude/skills/omnipus-voice/SKILL.md` and `knowledge/` (cited as **VOICE**; founder decision N3 = D65). Its section "The N2 pattern" is the pattern for every transient and connection message (N2 = D64). This spec references it and does not copy it.
- Founder review data (read only): `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/review.json`, `messages-review.json`, `revised/revised.json`, `toast-audit/result-w1..w4.jsonl`
- Round-1 review: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/notifications-spec-20261030/notifications-spec-review.md` (21 MAJOR, 5 MINOR, 2 OBSERVATION; dispositions in §33)
- Round-2 review: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/notifications-spec-20261030/notifications-spec-review-round2.md` (14 MAJOR, 4 MINOR, no new founder questions; dispositions, author clarifications and process receipts in §34)
- Issues: #645 (superseded open question; its hardening list is itemised as riders in §1.5), #177 (mobile notification preferences — compatibility only, §9.5), #1421 (extra accounts see every chat — the access model the recipient rule depends on, §8.1)
- ADR changed by decision D36: **ADR-051 — Provider-Capability-Aware Media Handling and User-Facing Error Translation** (`docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md`; not the two other ADR-051 files). The proposed amendment text is §28; ADR-051 itself is not edited by this spec.
- Prior approved spec this one partly supersedes: `docs/internal/specs/provider-messages-spec.md` (#711, Status Approved) — its founder decision D1 ("raw provider text is sent but displayed only under Verbose chat") is replaced by REQ D28 for the provider's own message, and its D5 (residual key fragments accepted in the raw Verbose view) is reconciled in §15.1 and §28.

---

## 0. Reading guide and bottom line

**Bottom line.** Omnipus gets one model for telling the user about events: a fixed **event catalogue** (§6) whose entries go to the **notification centre**, optionally a **pop-up**, and a **line in the affected chat**; one **system-line** renderer for every system message inside a chat; one **detail-level** setting (Normal / Verbose / Debug) replacing the Verbose-chat switch; honest wording written with the Omnipus voice skill, showing the provider's own (masked) message; and connection logging that tells **network failures** (provider not reachable) apart from **API failures** (provider answered with an error). Omnipus never claims "the internet is down" from failed requests (D64). The build scope is **Part 1 (in-app)**, built as ordered work packages and landed as **one** frozen, reviewed cut (D63). **Part 2 (external delivery, REQ §5.10)** is specified as the second step (§29) and is not built now (D62). User-facing documentation is a first-class deliverable (§25).

| Section | What it holds |
|---|---|
| §1 | Scope, build order, work packages, ownership, Epic and riders |
| §2 | Existing codebase context (GitNexus status, symbols, impact) |
| §3 | Re-verified current state, with every difference from the requirements |
| §4–§5 | User stories, behavioural contract, edge cases, non-behaviours (behavioural language only) |
| §6 | Event catalogue — kinds, categories, defaults, severities, resolve rules, targets (no implementation hooks) |
| §7 | Wire contract changes — shapes decided before any hook design |
| §8 | Notification centre: source and recipient map (hooks), router, lifecycle, reconciliation, retention, UI and its states |
| §9 | Preferences: model, inheritance, wire semantics, UI and its states, #177 compatibility |
| §10 | Pop-ups and the browser-local condition indicators |
| §11 | In-chat system lines, carriers, connection lines, component composition |
| §12 | Detail levels |
| §13 | Wording: rules (voice skill), catalogue, N2 lines, extraction, voice guard, manifest, titles, per-code facts |
| §14 | Connection handling and logging: classification, per-attempt observation, incident state machine, browser outage report |
| §15 | Security and privacy: masking contract, file names, locks, STRIDE |
| §16 | Removals and the replacement-function journeys |
| §16A | Accessibility, keyboard, announcements and narrow screens |
| §17 | Reachability |
| §18–§20 | BDD scenarios, TDD plan and gates, test datasets, regression |
| §21–§23 | Functional requirements, success criteria, traceability R-… → FR → scenario → test → documentation |
| §24–§25 | Hands-on UAT rows, user-facing documentation (pages and TODOs) |
| §26–§27 | Integration boundaries, holdout evaluation |
| §28 | ADR-051 amendment — exact proposed text |
| §29 | Part 2 — external delivery (second step) |
| §30 | Founder questions: all answered (FQ-1…FQ-17, Q1–Q7; NS1, OQ-1, OQ-2 → D81–D83; W1 → D84) |
| §31 | Ambiguity warnings, assumptions, accidental findings, clarifications |
| §32 | Decisions Log D63–D84 (continues REQ D1–D62) |
| §33 | Round-1 finding dispositions |
| §34 | Round-2 finding dispositions, author clarifications, process receipts |

Phases §4–§5 follow plan-spec's no-implementation-detail rule. Every later section (§6 onward) is design detail and cites `file::symbol`.

---

## 1. Scope, build order and size

### 1.1 In scope (Part 1 — the build)

| Area | Requirements |
|---|---|
| Notification centre: categories, owner, resolve-everywhere, 30-day retention, delete and clear, navigation, loading/error states | R-NC-1…6, R-SEC-2 |
| Event catalogue with a real producer per kind; network vs API as separate kinds | R-EV-1…4 |
| Preferences per kind (Notify me, Also pop up), global + per-workspace override per switch | R-PREF-1…4 |
| Pop-up rules (duration, cap, one action, no own-action pop-ups, no event modals); browser-local conditions as page/app indicators | R-PU-1…6 |
| One in-chat system-line mechanism, five types, in-progress state, connection lines | R-SL-1…7 |
| Detail levels Normal / Verbose / Debug replacing the Verbose-chat switch | R-DL-1…3 |
| Wording rules, the agreed baseline and the voice skill; the remaining messages rewritten with the skill | R-WD-1…9 |
| Connection logging at the default level; network vs API; browser outage report | R-LOG-1…4 |
| Secret masking on every surface | R-SEC-1 |
| Removals: own-action pop-ups (audited sites), event-raised modals, top-right badges | R-RM-1, R-RM-1a, R-RM-2, R-RM-3 |
| Keyboard, focus, announcement and narrow-screen behaviour of every new or changed surface | R-NC-2, R-PU-1…4, R-SL-2/4/7, R-PREF-1 (§16A) |
| User-facing documentation for every changed behaviour | root `CLAUDE.md` Definition of Done (§25) |
| #645 hardening, itemised as riders (§1.5) | REQ F4; #645 |

### 1.2 Second step (Part 2 — described, not built now)

External delivery through notification channels, R-EXT-1…12, R-SEC-3, R-SEC-4 (D62). §29 describes it to the depth needed to keep Part 1's contracts forward-compatible: the reserved third preference switch, the `notification_delivery_failed` kind (reserved, not in the Part-1 enum), and the replacement of the old scheduled-task channel alert (R-EXT-12). Part 2 gets its own grill before its build.

### 1.3 Size verdict and landing shape (D63 — founder N1 = A)

All of Part 1 — the core **and** the pop-up clean-up (R-RM-1: 328 call sites, §16) — ships as **ordered work packages, frozen together into one cut, with one 5-reviewer gate and one landing**. There is no "clean-up rides a later landing" option: no gate approves code that is not in its cut commit (root `CLAUDE.md`, "Change sizes and the review gate"). Part 2 stays described, not built; describing it here costs about 10 % of the document and protects Part 1's contracts, so it is not split out.

### 1.4 Work packages, order and ownership

```
 WP-0 contracts (backend-lead edits contracts/ and regenerates)
   │   all §7 shapes, atomically with generated artifacts, before any feature code;
   │   carries only the mechanical call-site fixes the push needs to compile (§7)
   ├─> WP-1 backend: router, source map, recipients, preferences, retention,
   │        revisions, delete/clear, locks (pkg/notifications, pkg/gateway, pkg/agent hooks)
   ├─> WP-2 backend: failure classification, per-attempt observation, incidents,
   │        provider message, outage endpoint, system_line persistence + history
   │        (pkg/providers, pkg/agent, pkg/gateway)
   ├─> WP-3 frontend: SystemLine renderer + adapters, detail levels (src/components/chat, src/lib, src/store)
   ├─(after WP-1)─> WP-4 frontend: centre (states, reconciliation, delete/clear), preferences UI,
   │                     pop-up host, local condition indicators (src/components/layout,
   │                     src/components/settings, src/components/workspaces, src/store)
   ├─(after WP-4)─> WP-5 frontend: own-action pop-up triage, 328 sites, and the replacement-function homes (R-RM-1/1a)
   ├─(after WP-5's disposition list is fixed)─> WP-6 wording: the disposition manifest (§13.5)
   │                     and the remaining rewrites, executed BY THE AREA OWNERS in their own files
   └─> WP-7 docs: implementing leads draft every page in §25; docs-verifier audits
                                 v
          one frozen cut of all of WP-0…WP-7 → one 5-reviewer gate → candidate CI →
          hands-on UAT (§24) on the exact commit → founder's yes → one landing
```

Ownership follows root `CLAUDE.md` "Feature ownership" (one owner per file area, never split by finding):

| Owner | Work packages | File areas |
|---|---|---|
| One backend implementer (backend-lead role) | WP-0, WP-1, WP-2, the backend half of WP-6, backend pages of WP-7 | `contracts/` (edit + `scripts/gen-contracts.sh`), `pkg/notifications`, `pkg/gateway`, `pkg/agent`, `pkg/providers`, `pkg/fileutil` hook only, `scripts/check-system-message-voice.sh` |
| One frontend implementer (frontend-lead role) | WP-3, WP-4, WP-5 (in that order — they share `src/store/ui.ts` and the pop-up host), the frontend half of WP-6, frontend pages of WP-7 | `src/` (incl. `src/components/ui/toast-container.tsx` rework), no `packages/ui/` publication (§11.1) |
| One test author (qa-lead RED) | every test of §19 | `pkg/**/*_test.go` for the new tests, `src/**/*.test.ts(x)`, `tests/e2e/` |
| CHECK (a different qa-lead instance) | test-integrity audit | read-only |
| docs-verifier | audit of every §25 page | read-only |

WP-6 is not a separate writer: its manifest (§13.5) is a task list, and each text change is made by the owner of the file that holds it (catalogue YAML and Go literals by the backend implementer, TS literals by the frontend implementer). This removes the parallel-writer overlap the round-1 review found.

### 1.5 Epic and riders

| Item | State |
|---|---|
| Epic | The notifications Epic waits on the founder (E1). |
| Riders (same file areas, chief to confirm) | #645 itemised below; #1513 (`pkg/notifications/goal_pause_b1_store_contract_test.go` marked "SCRATCH DRAFT ONLY", AF-1) — same package, proposed as a rider. Chief confirmation: **Unknown** until team-lead records it |

| #645 item | Disposition in this spec |
|---|---|
| Defect 1 — no ID minted on no-recipient / store-failure paths | Fixed: one router mints the id first (§8.2, FR-005) |
| Defect 2 — `hydrate` replaces the list | Fixed: revision-based reconciliation (§8.4, FR-010) |
| Defect 3 — the only badge sits in the sidebar | Kept by D33/D34; pop-ups become the always-visible signal (§10) |
| No file lock | Fixed on Linux and macOS with the sidecar lock convention; Windows keeps its documented in-process-only degradation (§15.3, FR-074) — residual for chief disposition |
| Non-injective file-name sanitising | Fixed: every recipient file is named by a hash of the exact username (§15.2, FR-072) |
| Synthetic identities (`_dev_bypass`, `_env_token`, `cli`) never receive stored notifications | Unchanged in Part 1; **Unknown** (not re-verified, AF-4); chief disposition |
| "Unify with toasts, or give the centre its own" question | Superseded by REQ (D1, D2) and this spec |

---


## 2. Existing codebase context

**GitNexus status.** The GitNexus MCP tools are not connected in this session, and `gitnexus list` shows no index for this checkout (only `omnipus-websearch/integ` at `c298c3b` and `omnipus-wt/crash-recovery` at `e0ab58a`). Per `omnipus-shared-rules` rule 9 the exploration below is Read/Grep at `4cad412bf`, and every impact row is labelled **Inferred**. Re-indexing this worktree was not done (heavy, not needed for a text deliverable).

**Round-1 fix (2026-10-31).** GitNexus was still not available to the spec author at fix time (no GitNexus tool in the session). The round-1 additions below were read with Read/Grep on `landing/notifications` at `e77af6cc9`; their impact rows stay **Inferred**.

### 2.1 Symbols involved

| Symbol | Role | Context (read in this task) |
|---|---|---|
| `pkg/notifications/store.go::Store.Create` / `retain` / `MarkRead` / `MarkAllRead` / `ListForUser` | modifies | Per-recipient JSON file, coalescing by `CoalesceKey` on unread rows, cap 50 newest, no delete, no age limit, process-local mutex only |
| `pkg/notifications/store.go` type constants | modifies | `TypeScheduleFailed`, `TypeGoalPaused`, `TypeKnowledgeDrift`, `TypeStopNotice` |
| `pkg/gateway/schedules.go::scheduledRunner.onFailure`, `resolveRecipients`, `pushNotification`, `publishChannelAlert`, `resolveAgentDefaultChannel` | modifies | Schedule-failure producer; raw `runErr.Error()` as body; no ID minted when persistence is skipped; channel alert to a binding lookup |
| `pkg/gateway/schedules.go` REST handler (the `/api/v1/notifications` switch) + `toNotification`, `handleListNotifications` | modifies | List, read-all, read-one; no delete |
| `pkg/gateway/knowledge_drift_notify.go::knowledgeDriftNotifier`, `knowledgeDriftRecipients` | calls / modifies | Drift producer; notifies every user; mints ID when persistence did not |
| `pkg/gateway/goal_pause_notify.go::goalPausedNotifier` | modifies | `goal_paused` producer (landed after the requirements) |
| `pkg/gateway/stop_notice.go::WSHandler.publishStopNotice` | modifies | `stop_notice` producer (landed after the requirements); called from `websocket_cancel.go` and `approval_stop.go` |
| `pkg/gateway/ws_tool_approval.go::WSHandler.broadcastToolApprovalRequired`; `pkg/gateway/ws_ask_user.go::WSHandler.broadcastAskUserCard` | calls | Needs-you sources; both broadcast to every connected client |
| `pkg/agent/task_executor.go` (`EmitTaskStatusChanged` call), `pkg/agent/loop_events.go::AgentLoop.EmitPlanStatusChanged` | calls | Task and plan status sources |
| `pkg/agent/goal_pause_notify.go::AgentLoop.notifyGoalPaused` | calls | Goal pause source with owner read from session meta |
| `pkg/agent/admission.go::memoryAdmissionCap`, `applyMemoryCap` | calls | Memory pressure lowers the admission cap to 2 (`unmeasurableHostAgentFloor`); no event today |
| `pkg/config/config_retention.go::MemoryPressureHigh` | calls | One shared memory-pressure check |
| `pkg/providers/error_classifier.go::ClassifyError`, `IsTransportFailure`, `connectionDropPatterns` | modifies | Transport vocabulary mixes "never reached" (DNS, refused, TLS) with "dropped mid-stream" (reset, EOF, GOAWAY) |
| `pkg/providers/types.go::FailoverReason`, `FailoverError.IsRetriable` | calls | No network/API distinction; `bad_model` became retriable on 2026-10-24 (Q70) |
| `pkg/providers/fallback.go::FallbackChain.Execute` | modifies | Per-attempt failures are classified and cooled down but not logged per attempt |
| `pkg/agent/translate_error.go::TranslateLLMError`, `classifyByHTTPStatus`, `buildDetail`, `defaultUserMessage`, `CodeNetwork` | modifies | 29 codes; `network` = 408/5xx/transport; `Detail` live-only |
| `pkg/agent/translate_error_provider_messages.go::WireLLMError`, `providerMessageFor`, `scrubDetailText`, `keyShapedToken` | modifies / calls | The one assembly point to the wire `LLMError`; masking helper reused for `provider_says` |
| `pkg/agent/loop_run_turn_response.go` (timeout retry path emitting `EventKindLLMRetry` and the "Retrying — please wait..." progress notice; the `"LLM call failed"` error log) | modifies | Network retry has no structured chat line today |
| `pkg/gateway/websocket_forward_hub.go` (`EventKindLLMRetry` → `hubContextWindowNotice`, `EventKindProviderRetry` → `hubProviderRetry`) | modifies | Hub-forwarded, sequenced session frames |
| `pkg/gateway/websocket.go` (`slog.Debug("ws: connection closed unexpectedly"…)`) | modifies | Browser↔server close invisible at the default log level |
| `pkg/agent/task_trigger.go::TaskTriggerScheduler.RunScheduled` → `TaskExecutor.SpawnTriggeredRun` | calls | The Calendar-task dispatch path (round-1 MAJ-004); a dispatch error returns before a run opens |
| `pkg/agent/task_executor.go::TaskExecutor.ExecuteTask`, `StartOccurrenceRun`; `pkg/agent/task_executor_run.go::TaskExecutor.runTask`, `openRun`, `closeRun`, `emitRunStatus`; `pkg/agent/task_executor_judge.go::TaskExecutor.completeTaskWithResult` | calls / modifies (additive) | Run lifecycle: automatic runs are `TaskRunKindScheduled`, Run now is `TaskRunKindManual`. Run history is best-effort (`openRun` degrades to nil, `closeRun` returns before `emitRunStatus` on a nil run or failed close), so task notifications use the dispatch origin captured in `runTask` and the applied completion in `completeTaskWithResult` (§8.1, round-2 R2-MAJ-009) |
| `pkg/task/task.go::Task` (`Owner` / `CreatedBy` NAMESPACE WARNING, `CancelReason`, trigger types) | calls | Attribution fields may hold a person's or an agent's name — never a recipient (round-1 MAJ-005) |
| `pkg/agent/loop_scheduler.go::LoopScheduler.RunScheduled` | — (not a producer) | `/loop` fires inside the user's chat; no notification today or in Part 1 |
| `pkg/agent/prompt_cache_accounting.go::agentLoopRunTurn.accountProviderAttempt` (called from `loop_run_turn_attempt.go::runProviderAttempt` and the `loop_run_turn.go` candidate closure) | modifies (additive) | The single per-attempt observation seam (round-1 MAJ-009), guarded by an explicit call-started fact from both callers (round-2 R2-MAJ-008, §14.2) |
| `pkg/gateway/history_replay_entry.go::stampHistoryOrigin`; `pkg/gateway/history_page_prepare.go::validateHistorySourceFrame`, `prepareGroup`, `historyWireMessage` | modifies | Each rejects an unknown carrier today; all four gain `system_line` (round-1 MAJ-002) |
| `pkg/fileutil/sidecar_lock.go::SidecarLockPath`; `pkg/fileutil/flock_windows.go::WithFlock`; `pkg/fileutil/file.go::WriteFileAtomic` | calls / hook | Sidecar lock convention (in-process only on Windows); the disk-full observation point, reported through a non-blocking queue (D73; round-2 R2-MAJ-005, §14.3) |
| `pkg/logger/sensitive.go::ScrubSensitiveValues` | calls | Registered exact values only; cannot see unregistered or cut fragments (round-1 MAJ-006) |
| `src/store/notifications.ts::useNotificationsStore` (`apply`, `hydrate`, `markRead`, `markAllRead`) | modifies | `apply` upserts by id; `hydrate` replaces the whole list |
| `src/components/layout/NotificationPanel.tsx::NotificationPanel` | modifies | Right-hand sheet; click marks read and navigates to session or `/tasks` |
| `src/components/layout/Sidebar.tsx` (account menu "Notifications" item, `sidebar-notification-badge`) | modifies | The only badge; inside the sidebar |
| `src/store/ui.ts::addToast` / `removeToast`; `src/components/ui/toast-container.tsx::ToastContainer` | modifies | 4 s default, no hover pause, no cap; fixed bottom-right |
| `src/store/chatPreferences.ts::useChatPreferencesStore` (`verboseChatEnabled`), `src/components/settings/ChatSection.tsx::ChatSection` | modifies | Per-device boolean in localStorage |
| `src/lib/toolVisibility.ts` | modifies | Which tool calls hide at non-Verbose |
| `src/components/chat/SystemMessageRows.tsx::SystemMessage`, `ProviderFallbackNoteLine.tsx`, `ProviderRetryIndicator.tsx`, `RateLimitIndicator.tsx`, `ConnectionStatus.tsx::ChatConnectionStatusLine`, `ErrorDetailDisclosure.tsx`, `UnavailableChatNotice.tsx` | modifies | Today's separate system-message renderers |
| `src/components/agents/ToolApprovalModal.tsx`; `src/lib/featureSwitches.ts::INLINE_TOOL_APPROVALS_ENABLED` | removes | Event-raised app-wide modal, mounted only while the switch is false (it is `true`) |
| `src/store/chat/slices/replay-and-status-frames.ts` (`case 'notification'`) | calls | Live notification frame → notifications store |

### 2.2 Impact assessment (Inferred — Grep sweep, no graph)

| Symbol modified | Risk | d=1 dependents (files) | d=2 / notes |
|---|---|---|---|
| `notifications.Store.Create` | MEDIUM | `schedules.go`, `knowledge_drift_notify.go`, `goal_pause_notify.go`, `stop_notice.go`, `store_test.go`, `goal_pause_b1_store_contract_test.go` | REST list + WS push of every producer |
| `agent.TranslateLLMError` | HIGH | `session_worker.go`, `external_dispatch.go`, `translate_error_provider_messages.go`, `loop_unroutable.go`, `loop_run_turn.go`, `loop.go`, `turn_transcript.go`, `gateway/rest_executor_smoketest.go` | Every failed turn, live and replay; transcript format |
| `agent.WireLLMError` | HIGH | `gateway/websocket_forward_hub.go` | Every live error frame |
| `providers.IsTransportFailure` | MEDIUM | `providers/error_classifier.go`, `agent/loop.go`, `agent/translate_error.go` | Retry/fallback decisions — must not change *whether* a retry happens, only how it is labelled |
| `providers.ClassifyError` / `FallbackChain.Execute` | HIGH | `providers/fallback.go`, `agent/loop_run_turn_response.go` | Cooldown and failover; changes are additive (logging, labels) |
| `useNotificationsStore` | LOW | `AppShell.tsx`, `NotificationPanel.tsx`, `Sidebar.tsx`, `replay-and-status-frames.ts` | — |
| `verboseChatEnabled` | MEDIUM | 27 non-test files (19 under `src/components/chat/`, plus `toolVisibility.ts`, `llm-error.ts`, 4 store slices, `ChatSection.tsx`) | Every chat component's visibility rule |
| `addToast` | HIGH (breadth) | 328 call sites in 101 files | WP-5 |
| `accountProviderAttempt` | MEDIUM | `loop_run_turn_attempt.go`, `loop_run_turn.go` | additive observation; accounting unchanged |
| `stampHistoryOrigin` / `validateHistorySourceFrame` / `historyWireMessage` | MEDIUM | every history page and replay | additive case per function; byte budget unchanged |
| `TaskExecutor.runTask`, `TaskExecutor.completeTaskWithResult` | MEDIUM | every task execution | origin capture and outcome publication only; status and run-history semantics unchanged |

HIGH rows: the change to `TranslateLLMError`/`WireLLMError`/`ClassifyError` is flagged to team-lead before GREEN; the design keeps retry and failover *decisions* byte-identical and only adds labels, masked text and log lines (FR-063).

### 2.3 Relevant flows and cluster placement

| Flow | Relevance |
|---|---|
| Turn → provider call → fallback chain → classify → retry/fallback/terminal error → transcript + live frame | Network vs API label, provider message on the main line, per-attempt logs, provider incidents |
| Calendar trigger → `TaskTriggerScheduler.RunScheduled` → `SpawnTriggeredRun` → `ExecuteTask` → `runTask` (origin captured) → `completeTaskWithResult` (applied) | Calendar `schedule_finished` / `schedule_failed` (§8.1) |
| Gateway schedule (heartbeat, API schedule) → `scheduledRunner` → success/failure | gateway-job `schedule_finished` (new) and `schedule_failed` (existing) |
| Tool approval park → broadcast → decision from any tab → `tool_approval_resolved` | Needs-you notification and resolve-everywhere |
| Ask-user park → broadcast → answered/cancelled | Needs-you notification and resolve-everywhere |
| Browser WebSocket lifecycle → reconnect phases → catch-up | System line + app-wide indicator + outage report |

The feature spans the **notifications/gateway** cluster, the **agent turn loop / provider** cluster and the **SPA chat + layout + settings** clusters. Cross-cluster coupling is confined to: the contract (§7), one producer path in `pkg/notifications` (§8.2), and the incident tracker (§14.3).

### 2.4 Reference patterns

The plan-spec skill's `docs/reference/go-implementation/` library does not exist in this repository (`ls` at `4cad412bf`: no such directory). Reused in-repo patterns instead: `CoalesceKey` coalescing (`pkg/notifications/store.go`), the drift producer's ID minting (`knowledge_drift_notify.go`), `scrubDetailText` masking, the `provider_retry` per-turn projection (provider-messages spec §7.4), the agent-profile "Save not confirmed + Retry" and `AutoSaveIndicator` page patterns named in REQ §2.1.

---


## 3. Current state re-verified at `4cad412bf`

The requirements were captured at `c90310f1f` (22 Oct); 1,398 commits landed before `4cad412bf`. Every claim this spec relies on was re-read at `4cad412bf`. **Status** = *same* (requirements still true), *changed* (code moved since), *new* (fact the requirements did not record).

### 3.1 Requirements claims (REQ §2)

| # | Requirements claim | At `4cad412bf` | Status | Evidence |
|---|---|---|---|---|
| C1 | Pop-up container fixed bottom-right | Same | same | `src/components/ui/toast-container.tsx::ToastContainer` (`fixed bottom-4 right-4`) |
| C2 | 322 non-test pop-up call sites in 101 files | **328** in 101 files; +4 `src/components/settings/PerformanceSection.tsx`, +1 `src/store/chat/model-choice.ts` (new file), +1 `src/store/chat/slices/outbound-lifecycle.ts`. The six new sites are **not in the audit** | **changed** | same grep at both commits (322 at `c90310f1f`, 328 at `4cad412bf`); per-file diff |
| C3 | Notification types `schedule_failed`, `knowledge_drift` only | **Four**: also `goal_paused` and `stop_notice` | **changed** → D67, D68 | `contracts/components/schemas/Notification.yaml` (`type` enum); `pkg/notifications/store.go` constants |
| C4 | Two producers | **Four**: + `pkg/gateway/goal_pause_notify.go::goalPausedNotifier` (commit `f24785784`), + `pkg/gateway/stop_notice.go::WSHandler.publishStopNotice` (commit `94408e56b`) | **changed** | files read |
| C5 | Retention newest 50 per user, no age limit | Same | same | `pkg/notifications/store.go::notificationCap`, `retain` |
| C6 | Stored per recipient, one file each | Same | same | `pkg/notifications/store.go::Store.userFile` |
| C7 | Sidebar attention cue | Same | same | `src/components/layout/sidebar/attentionCue.ts::attentionCountLabel` |
| C8 | Browser offline / server unreachable copy | Same text | same | `src/components/chat/ConnectionStatus.tsx::ChatConnectionStatusLine` |
| C9 | Browser↔server unexpected close logged at debug only | Same | same | `pkg/gateway/websocket.go` (`slog.Debug("ws: connection closed unexpectedly", …)`) |
| C10 | No client-side event reporting endpoint | Same | same | no matching path in `contracts/openapi.yaml` |
| C11 | `FailoverReason` set; transport failures matched as transient | Same set; **`bad_model` is now retriable** (falls back to the next candidate, founder Q70, 2026-10-24) and `ProviderFallbackFrame.unavailable_code` gained `model_not_found` | **changed** (no question — affects only the `bad_model` wording context) | `pkg/providers/types.go::FailoverError.IsRetriable`; `contracts/components/schemas/ProviderFallbackFrame.yaml` |
| C12 | No reason value separates network from API | Confirmed (was Inferred) | same | `pkg/providers/types.go::FailoverReason` has no such value; `pkg/providers/error_classifier.go::connectionDropPatterns` mixes never-reached (`no such host`, `connection refused`, `tls handshake`, `dial tcp`) with mid-stream drops (`connection reset by peer`, `unexpected eof`, `http2: server sent goaway`) → D72 (mid-stream drop = network) |
| C13 | Provider retry logging partial | Confirmed; and the **default log level is `warn`**, so any Info line is invisible on a default install | same + **new** | `pkg/agent/loop_run_turn_response.go` (Warn on timeout retry and stream reset; Error `"LLM call failed"` only after retries); `pkg/providers/fallback.go::FallbackChain.Execute` (no per-attempt log); `pkg/config/defaults.go` (`LogLevel: "warn"`) |
| C14 | ADR-051: raw provider text never reaches the wire; 29 sentences, 7 with a provider template | **Corrected at round 1 (MIN-004):** the 7 templates are as stated (`provider_auth_failed`, `rate_limited`, `quota_billing`, `model_retired`, `bad_model`, `provider_request_rejected`, `model_route_unavailable` carry `provider_message`), but the claim "never reaches the wire" is too broad: the **live** `LLMError.detail` already carries a bounded, masked preview of the provider's text; it is not persisted and not replayed | **changed** (round-0 wording was wrong) | `contracts/components/schemas/LLMError.yaml` (`detail` description, `x-user-messages`); `pkg/agent/translate_error_provider_messages.go::scrubDetailText` |
| C15 | `network` covers unreachable and 408/5xx | Same | same | `pkg/agent/translate_error.go::classifyByHTTPStatus`, `classifyByMessage`, `CodeNetwork` |
| C16 | Scheduled-task channel alert with full error text; never delivers | Same code path | same | `pkg/gateway/schedules.go::scheduledRunner.publishChannelAlert`, `resolveAgentDefaultChannel` (reads `cfg.Bindings` channel-specific entries); `pkg/gateway/rest_channels.go` still removes stale channel-wildcard bindings; `pkg/channels/telegram/telegram.go` still errors `invalid chat ID` |
| C17 | Allow From filters incoming only | Same | same | `pkg/config/config.go` (`AllowFrom`), `pkg/channels/base.go::BaseChannel.IsAllowed` |
| C18 | Email is an agent tool, not a channel | Not re-read beyond `pkg/email/CLAUDE.md` existing | Unknown (Part 2 only) | — |
| C19 | 18 chat components check the Verbose switch | 19 files under `src/components/chat/` (one is a hook), 27 non-test files overall | changed (count) | grep `verboseChatEnabled` |

### 3.2 New facts the requirements did not record

| # | Fact | Consequence for this spec | Evidence |
|---|---|---|---|
| N1 | The Verbose switch is a **per-device** browser setting (localStorage), not per user | Detail level stays per device (§12) — simplest, unchanged scope | `src/store/chatPreferences.ts` (`name: 'omnipus-chat-preferences'`, `partialize`) |
| N2 | An **event-raised app-wide approval modal** exists but is not mounted: `ToolApprovalModal` renders only while `INLINE_TOOL_APPROVALS_ENABLED` is false, and it is `true` | R-RM-2 = delete the modal and the switch (superseded code) | `src/components/layout/AppShell.tsx` (`{!INLINE_TOOL_APPROVALS_ENABLED && <ToolApprovalModal />}`); `src/lib/featureSwitches.ts` |
| N3 | Founder ruling **2026-10-18**: *no "waiting in another chat" notice*; the sidebar attention cue is the signal elsewhere. D30 (2026-10-22) makes Needs-you pop up always | Later decision wins, but the conflict is surfaced → D70 | `src/components/agents/ToolApprovalPanel.tsx` header comment |
| N4 | `system_overload` frame has a consumer (pop-up) but **no producer** | Not used for `concurrency_reduced`; its consumer pop-up is removed with WP-5 (dead path) | `pkg/gateway/websocket_forward.go` comment block; grep |
| N5 | Memory pressure already lowers concurrency (to 2) — also **permanently on hosts where memory cannot be measured** (Windows, some containers) | Producer for `concurrency_reduced` exists in logic but emits nothing → D74 | `pkg/agent/admission.go::memoryAdmissionCap`, `applyMemoryCap` |
| N6 | No disk-space monitor and no internet-loss detector exist | Producers must be built → D64 (no internet detector; replaced), D73 | grep `Statfs`/free-space/connectivity: none outside unrelated code |
| N7 | Schedule-failure notification body is the raw run error (`runErr.Error()`), unmasked and untranslated; on the no-recipient and store-failure paths **no ID is minted**, so the frame is rejected by the SPA (`id` minLength 1) — #645 Defect 1 still present | Fixed by the one producer path (§8.1, FR-005) | `pkg/gateway/schedules.go::scheduledRunner.onFailure`, `pushNotification` |
| N8 | `hydrate` replaces the whole list; `AppShell` refetches with `staleTime: 60_000` and default focus refetch — #645 Defect 2 still present | Revision-based reconciliation (FR-010, §8.4) | `src/store/notifications.ts::hydrate`; `src/components/layout/AppShell.tsx` |
| N9 | The only unread badge is inside the sidebar (account menu) — #645 Defect 3 | Pop-ups (R-PU) become the always-visible signal; badge placement unchanged (D33 keeps the sidebar as is) | `src/components/layout/Sidebar.tsx` (`sidebar-notification-badge`) |
| N10 | Marking read is not pushed to the user's other tabs | Resolve-everywhere needs a push (FR-007) | `pkg/gateway/schedules.go` REST switch (`MarkRead` then 204, no emit) |
| N11 | A timeout retry posts the text "Retrying — please wait..." as an outbound progress notice; `EventKindLLMRetry` is forwarded only as a context-window notice | Network retries get a structured line (FR-033) | `pkg/agent/loop_run_turn_response.go`; `pkg/gateway/websocket_forward_hub.go` |
| N12 | The provider-messages spec (Approved, #711) decided raw provider text is sent but **displayed only under Verbose**, and `LLMError.detail` is **live-only, never persisted** | REQ D28 replaces this for the provider's own message: a masked, bounded *provider message* is persisted and shown on the main line; the raw `detail` stays Verbose-only and live-only. Recorded in the ADR-051 amendment (§28) | `docs/internal/specs/provider-messages-spec.md` (D1); `contracts/components/schemas/LLMError.yaml` (`detail` description) |
| N13 | Stop outcomes (`stop_notice`) are results of the user's own Stop action, yet are centre notifications | Conflicts with D31 (own-action results never in the centre) → D68 | `pkg/gateway/stop_notice.go`; `pkg/gateway/websocket_cancel.go` (4 call sites), `approval_stop.go` (1) |
| N14 | The 2026-10-24 bad-model fallback means a wrong model with a backup configured now produces a fallback note, not an error | Wording only; no question | C11 |
| N15 | Calendar tasks are **not** run by the gateway's `scheduledRunner`: they run through `TaskTriggerScheduler.RunScheduled` → `SpawnTriggeredRun` → `ExecuteTask` (run kind `TaskRunKindScheduled`, which queue drain, dependency unblock and plan dispatch also use); Run now is `TaskRunKindManual` | The producer map classifies at run close (§8.1, round-1 MAJ-004) | `pkg/agent/task_trigger.go`, `pkg/agent/task_executor.go`, `pkg/agent/task_executor_run.go::closeRun` |
| N16 | `Task.Owner` / `Task.CreatedBy` carry a NAMESPACE WARNING: either a person's or an agent's name | Never a recipient (§8.1 R2) | `pkg/task/task.go::Task` |
| N17 | No per-account access gate exists for workspace objects ("all post-gate role/multi-user checks were removed"); issue #1421 tracks extra accounts seeing every chat | Workspace-object recipients = every configured account until an access model exists (D83, OQ-2 = A) | `pkg/gateway/rest_workspaces.go` (caller comment); `pkg/gateway/schedules.go::resolveRecipients` ("Single-user product"); #1421 |
| N18 | `accountProviderAttempt` already owns per-attempt accounting at the real call boundary, before a retry or fallback replaces the failed candidate | The one place to observe every dispatched attempt exactly once (§14.2, round-1 MAJ-009) | `pkg/agent/prompt_cache_accounting.go`; note (round-2 R2-MAJ-008): `runProviderAttempt` reaches it also for an image-attachment failure before the call, so the observation is guarded by a call-started fact (§14.2) |
| N19 | The four history stages reject an unknown carrier: `stampHistoryOrigin` (exhaustive switch), `validateHistorySourceFrame` (names map, "unclassified historical frame"), `prepareGroup` byte weighing, `historyWireMessage` projection | All four are named in §7.5 (round-1 MAJ-002) | `pkg/gateway/history_replay_entry.go`, `pkg/gateway/history_page_prepare.go` |
| N20 | `/loop` fires run through a third scheduler (`LoopScheduler.RunScheduled`) inside the user's own chat | Not a producer (§8.1) | `pkg/agent/loop_scheduler.go` |
| N21 | `fileutil.WithFlock` is in-process only on Windows (warns) | Lock limitation stated (§15.3, round-1 MIN-005) | `pkg/fileutil/flock_windows.go` |

---

## 4. User stories and acceptance criteria

> Behavioural language only (plan-spec Phases 1–2.5). Requirement ids in brackets.

### US-1 — See only the events I opted into, in one place (P0)

A person running agents wants one notification centre that collects the events they chose — an agent waiting for them, a task that failed overnight, a provider that is down — without it becoming a log. Today only four event types reach the centre and two of them are by-products; most of what an agent or the system does is invisible unless the right chat is open. [R-NC-1, R-NC-2, R-NC-5, R-NC-6, R-EV-1, R-EV-4]

**Why this priority**: the centre is the anchor every other surface (pop-up, preferences, external delivery) hangs from.
**Independent test**: fire each catalogue event through its real source against a running instance and check the centre lists exactly the defaults-on kinds, filterable by category, each with a working "open" target.

1. **Given** fresh-install defaults, **When** a Calendar task's scheduled occurrence fails, **Then** the centre shows one entry under **Tasks & plans** naming the task, and opening it shows the run (or the task, when no run was started).
2. **Given** fresh-install defaults, **When** a task that is not on the Calendar starts, **Then** no centre entry appears.
3. **Given** entries in all three categories, **When** the user filters by **Needs you**, **Then** only Needs-you entries remain visible.
4. **Given** a provider becomes unreachable while three chats are replying, **When** the condition is detected, **Then** the centre shows one entry for the provider (not three), and each of the three chats shows its own line.
5. **Given** any centre entry, **When** the user activates it, **Then** the app navigates to the screen where the item can be handled, or says plainly that the item no longer exists.
6. **Given** the user changes a task on the Board themselves, **When** the change is saved, **Then** no centre entry and no pop-up appear for it.

### US-2 — Handle it once, cleared everywhere (P0)

A person who answers an agent's question in the chat does not want the same item still lit up in the centre, in a pop-up, or in another open tab. [R-NC-3]

**Why this priority**: stale badges teach people to ignore notifications.
**Independent test**: two tabs open; resolve in one; observe the other, including a tab that was offline at the time.

1. **Given** an approval request shown as pop-up and centre entry in two tabs, **When** the user approves it in the chat in tab A, **Then** within 2 s the pop-up disappears in both tabs and the centre entry shows as handled in both.
2. **Given** an unread entry in two tabs, **When** the user opens it from tab A, **Then** tab B shows it read and the badge count drops.
3. **Given** an approval that times out on the server, **When** it expires, **Then** its entry is handled everywhere without user action.
4. **Given** tab B was offline while tab A deleted an entry, **When** tab B reconnects, **Then** the entry is gone in tab B too, and nothing newer that tab B already showed is rolled back.

### US-3 — Old notifications go away, and I can delete them (P1)

[R-NC-4, R-SEC-2]

1. **Given** an entry older than 30 days, **When** the retention sweep runs, **Then** the entry is gone from the centre and from storage — also when it belongs to a request that is still waiting (the request itself keeps waiting in its chat).
2. **Given** 60 entries including one unhandled Needs-you entry older than all others, **When** a new entry arrives, **Then** the Needs-you entry is still there.
3. **Given** a centre entry, **When** the user deletes it, **Then** it disappears from every open tab and from storage.
4. **Given** several entries, **When** the user chooses Clear all and confirms, **Then** the centre is empty in every open tab.
5. **Given** an entry for an approval that is still waiting, **When** the user deletes it or clears all, **Then** the approval is neither approved, denied nor cancelled: its card stays in the chat and the sidebar still marks the chat as needing attention.

### US-4 — Choose per event kind what notifies me (P1)

[R-PREF-1…4]

1. **Given** the preferences screen, **When** it opens, **Then** every catalogue kind is listed under its category with **Notify me** and **Also pop up** switches set to the fresh-install defaults.
2. **Given** **Task or plan failed** set to Notify off globally, **When** a task fails, **Then** no centre entry and no pop-up appear, and the task's own chat still shows the failure.
3. **Given** a workspace override turning **Task or plan started** on for workspace W, **When** a task starts in W, **Then** an entry appears; **When** a task starts in workspace V, **Then** none appears.
4. **Given** the preferences screen, **When** the user looks at a System-wide kind inside a workspace override, **Then** it is not offered (global only).
5. **Given** the three Needs-you kinds, **When** the user tries to switch either of their switches off, **Then** the switch is locked on with an explanation.
6. **Given** a workspace W, **When** the user turns only **Also pop up** off for one kind there, **Then** **Notify me** for that kind keeps following the global setting, and the user can later return just that kind to the global setting without losing W's other overrides.
7. **Given** the saved preferences could not be read, **When** the user opens the screen, **Then** the screen says the saved preferences could not be read, offers Retry, and saving asks for confirmation before replacing them.
8. **Given** the preferences changed in another tab, **When** the user saves in this tab, **Then** nothing is overwritten silently: the screen says the settings changed elsewhere and offers to reload them.

### US-5 — Pop-ups that interrupt only when worth it (P0)

[R-PU-1…6]

1. **Given** an info event with pop-up on, **When** it arrives, **Then** a pop-up shows bottom-right and disappears after 5 s unless the pointer hovers on it or keyboard focus is in it.
2. **Given** a warning event, **When** it arrives, **Then** the pop-up stays 10 s.
3. **Given** an error or Needs-you event, **When** it arrives, **Then** the pop-up stays until dismissed or the item is handled.
4. **Given** three pop-ups visible, **When** a fourth event arrives, **Then** three stay visible and a line "+1 more — open notifications" appears; it opens the centre, where the hidden event and its action are listed.
5. **Given** any pop-up, **When** it shows, **Then** it offers at most one main action that navigates to where the item is handled, plus close.
6. **Given** the user saves a setting, **When** the save succeeds or fails, **Then** no pop-up appears and nothing enters the centre; the page shows the result.
7. **Given** any event, **When** it arrives, **Then** no full-screen modal opens by itself.
8. **Given** a browser-only condition (for example a new app version is available), **When** it occurs, **Then** it appears as a lasting indicator on the page or app frame with its action, not as a pop-up that can vanish.

### US-6 — One consistent line for system messages in a chat (P0)

[R-SL-1…7]

1. **Given** a reply that fails because the provider rejected the key, **When** the failure is shown, **Then** it appears as one Error line with icon, colour and weight of the Error type, our sentence and the provider's own message quoted.
2. **Given** a provider that cannot be reached while a reply is waiting, **When** the system retries, **Then** an animated amber Warning line says it cannot reach the provider, the cause it observed, when it retries next and which attempt this is (for example "attempt 2 of 5").
3. **Given** that retry later succeeds, **When** the reply continues, **Then** at Normal the line is gone; at Verbose and Debug one line remains recording the connection problem.
4. **Given** the browser loses its connection to the server, **When** the loss lasts, **Then** an app-wide indicator and one line in the open chat show it; after reconnecting neither appears in the chat history.
5. **Given** a line that offers an action (e.g. Switch model), **When** the user activates it, **Then** the action happens or the right screen opens; if the target no longer exists, the line says so instead of acting on something else.
6. **Given** a chat reloaded or paged back in history, **When** its system lines render, **Then** they read the same as they did live.

### US-7 — Pick how much detail the chat shows (P1)

[R-DL-1…3]

1. **Given** Settings → Chat, **When** it opens, **Then** a three-way **Detail level** (Normal, Verbose, Debug) replaces the Verbose-chat on/off switch.
2. **Given** each level, **When** the same chat with tool calls, a delegation, a retried provider failure and a final failure is shown, **Then** each row type shows or hides exactly as the normative table in §12.
3. **Given** any user, **When** they select Debug, **Then** it is allowed, it shows the internal ids, request ids and full failure details that already exist, and secrets are still masked.

### US-8 — Messages that say what happened, why, and what to do (P1)

[R-WD-1…9]

1. **Given** a provider balance error, **When** the failure is shown, **Then** the main line names the provider, quotes its message ("Insufficient balance") and says what the user can do.
2. **Given** a system line inside a chat, **When** the product would have been the actor, **Then** the line says "I" (agent's perspective) and never "Omnipus", "we", "our" or "us".
3. **Given** the same event outside a chat (centre, pop-up), **When** it is shown, **Then** the wording is neutral.
4. **Given** a reply that finally fails because the provider could not be reached, **When** the failure is shown, **Then** the line names the provider and its host, the cause observed, and one fix (for example "check your network or proxy").
5. **Given** several providers fail to connect, **When** the user reads the centre, **Then** it says connections to AI providers are failing and lists the observed causes; it never says the internet is down.

### US-9 — Connection problems are diagnosable afterwards (P1)

[R-LOG-1…4, R-EV-2]

1. **Given** a provider that cannot be reached, **When** an attempt fails, **Then** the log at the default level records exactly one network-failure line for that attempt, naming provider, model and reason.
2. **Given** a provider that answers 503, **When** an attempt fails, **Then** the log records one API-failure line with the status and the provider's (masked) message.
3. **Given** the browser lost the server for 40 s, **When** it reconnects, **Then** the server log records the outage once, with start, end, the observed duration and the longest possible duration since the last confirmed connection.
4. **Given** a connection line that left the chat at Normal, **When** an operator reads the log, **Then** the same episode is there.

### US-10 — Secrets never leak through a message (P0)

[R-SEC-1]

1. **Given** a provider message that echoes part of an API key, **When** it is shown on the main line, in technical details, in the centre or in the log, **Then** the recognisable key fragment is replaced by a mask on every surface and every level.

### US-11 — Fewer, honest pop-ups for my own actions (P1)

[R-RM-1, R-RM-1a, R-RM-2, R-RM-3]

1. **Given** an action whose page already shows the outcome, **When** the action completes, **Then** no pop-up appears.
2. **Given** a failed save whose page showed nothing before, **When** the save fails, **Then** the page shows a "not saved" indicator with Retry.
3. **Given** a moved calendar event, **When** the move completes, **Then** Undo is available in a fixed, visible place on the page for its whole time limit, even when the moved event is no longer in view.
4. **Given** any tool approval, **When** it arrives, **Then** no app-wide modal opens; the request is handled in its chat.

### US-12 — Use notifications with the keyboard and on a narrow screen (P1)

[R-NC-2, R-NC-6, R-PU-1…4, R-SL-2, R-SL-4, R-SL-7, R-PREF-1]

**Why this priority**: notifications that only work with a mouse on a wide screen fail the people who need them most.
**Independent test**: complete the centre, preferences and a system-line action with the keyboard only, then repeat at 320 px width.

1. **Given** the keyboard only, **When** the user opens the centre, filters, opens an entry, deletes another and clears all, **Then** every step is reachable in a predictable order and Escape returns focus to where it started.
2. **Given** a focused pop-up or centre row, **When** the item is resolved in another tab and disappears, **Then** focus moves to a sensible neighbour, never to nowhere.
3. **Given** a retry line counting down, **When** a screen reader is running, **Then** the line is announced once per attempt, not every second, and reloaded history is not announced.
4. **Given** a 320 px wide window or 400 % zoom, **When** a long system line with a quoted provider message and an action shows, **Then** the text wraps and the action stays visible and usable.

---

## 5. Behavioural contract, edge cases and non-behaviours

### 5.1 Behavioural contract

Primary flows:
- When an event of an opted-in kind occurs, the system creates one centre entry for each of the event's recipients and, if the kind's pop-up preference is on, shows one pop-up in every open tab of that recipient.
- When a recurring condition (a provider outage, low memory) is still unresolved, the system updates the existing entry instead of creating a new one, and raises no second pop-up.
- When an item is handled anywhere, the system marks it handled in storage and on every open tab within 2 s; a tab that was offline catches up when it reconnects.
- When a reply is affected by a provider problem, the system shows one system line in that chat; idle chats get none.
- When a provider failure ends a reply, the main line shows our sentence plus the provider's own message, masked.
- When a connection attempt is retried, the line names the cause as soon as it is observed, the countdown and the attempt counter.
- When the detail level changes, every chat component re-renders by the one table in §12, without reloading and without losing details already received.
- When a connection problem starts, ends or repeats, the system writes a log line at the default level saying network or API.

Error flows:
- When the notification store cannot be written, the system still delivers the live entry and pop-up to open tabs, marks it as not saved, and logs an error.
- When the browser cannot reach the server, the system shows the app-wide indicator and a chat line, keeps retrying, and reports the outage when back — retrying the report until the server accepts it.
- When the preferences cannot be read, the system uses the fresh-install defaults, keeps working, says so on the preferences screen, and replaces the saved preferences only after the user confirms.
- When the centre cannot be loaded, the system says so with Retry instead of showing an empty list.
- When a provider message cannot be extracted from a response, the main line shows our sentence alone (never a blank quote).
- When a notification's target no longer exists, activating it says so and changes nothing.

Boundary conditions:
- When more than three pop-ups are due, the system shows three and a "+N more — open notifications" line.
- When an entry is 30 days old or older, the next retention sweep deletes it, whatever its state.
- When a provider message is longer than 300 code points, the main line shows the first 300 and an ellipsis; the full masked text (≤ 2,000 bytes) is in technical details.

### 5.2 Edge cases

| Situation | Expected behaviour |
|---|---|
| Two producers report the same provider outage within one second | One centre entry, one pop-up |
| Provider recovers and fails again within 10 minutes | The same entry re-opens; no second pop-up; the restored entry already shown stays (nothing is retracted) |
| A Needs-you item is handled while the user's browser is offline | On reconnect the entry shows handled and no pop-up appears |
| User deletes an unhandled Needs-you entry | The entry is deleted; the request keeps waiting in its chat and the sidebar cue is unchanged |
| Session metadata cannot be read for a session-scoped event | No notification for any account; the failure is logged (keeps today's goal-pause rule) |
| An agent and a person share the same name | Recipients never come from task or plan attribution text, so the agent's name cannot route anything (§8.1) |
| No user account exists (first start before onboarding) | Live pop-up only, with a minted id; nothing persisted; log warning |
| Detail level changed while a retry line is in progress | The in-progress line stays at every level (R-SL-5) |
| Detail level raised after a failure arrived live | Its technical details appear (they were kept, not discarded) |
| Page reloaded during a browser↔server outage | The outage is still reported once after reconnect (the record survives the reload in that tab) |
| Report accepted by the server but the answer was lost | The retry is recognised; the server logs the outage once |
| Laptop slept during the outage | The report states the observed duration and, separately, the longest possible duration since the last confirmed connection |
| Provider message contains HTML or Markdown | Shown as plain text |
| Provider message is not English | Shown as received (masked), our sentence stays English |
| A workspace with an override is deleted | Its override is ignored and removed on the next preferences save |
| Debug selected on a shared screen | Secrets are still masked; no raw credential value is ever rendered |
| A Calendar task is run by hand (Run now) | Its result counts as a task result, not a scheduled-task result; starting it is the user's own action and raises nothing |
| Gateway restarts while a provider outage is open | The open entry stays open and is resolved by the next success to that provider |

### 5.3 Non-behaviours and safeguards

Qualitative prohibitions:
- The system must not log or store raw credential values in any message, log line or entry, because providers echo partial keys.
- The system must not create a centre entry or pop-up for the result of the user's own action, because the screen where they acted shows it (D31).
- The system must not open a full-screen modal because of an event (D17, D37).
- The system must not put a system line into chats that were not affected (D14).
- The system must not hide the provider's own error message behind a generic sentence (D28), and must not invent one when none was received.
- The system must not claim that the internet or the server's internet connection is down from failed provider requests (D64).
- The system must not approve, deny or cancel a request because its notification was deleted or cleared (D80).
- The system must not choose a notification recipient from task or plan attribution text that can hold either a person's or an agent's name (§8.1).
- The system must not add any new outbound send in Part 1 (D62); the existing scheduled-task channel alert stays exactly as it is until Part 2 replaces it (R-EXT-12).
- The system must not change *whether* a provider call is retried or falls back; only labels, wording and logs change.
- The system must not introduce raw cron text, a Command Center screen, a JPEG screencast, a default agent or any other retired surface (root `CLAUDE.md`).
- The system must not use "Omnipus", "we", "our" or "us" as the actor in any system message (except the one bug-report line, D48).
- The system must not add new badges in the top-right corner (D34).
- An implementing agent must not "helpfully" add a notification sound, a browser (OS) notification, an email, a mobile push, an active connectivity probe, or capture of successful provider traffic in Part 1.

Machine-verifiable constraints:

| Category | Constraint |
|---|---|
| REST errors | Delete of an unknown id → 404. Preferences update: unknown kind → 400 `unknown_notification_kind`; locked switch set off → 400 `notification_switch_locked`; workspace override for a System-wide kind → 400 `system_wide_kind_global_only`; unknown workspace id → 400 `unknown_workspace`; stale `base_revision` → 409 `preferences_changed` (body: current preferences); saving over unreadable preferences without `replace_unreadable: true` → 409 `preferences_unreadable`. Outage report with `ended_at` before `started_at`, `last_confirmed_at` after `started_at`, duration > 7 days, or `ended_at` more than 5 min after the server's clock → 400 `invalid_outage_report`; more than 10 reports per minute per user → 429 with `Retry-After`; a repeated `report_id` → 204 and no second log line |
| Timing | A handled item shows handled in another open tab ≤ 2 s after the handling request returns (local instance) |
| Retention | An entry with `created_at_ms` ≥ 30 × 24 h old is absent after the next sweep, waiting or not; the sweep runs at start-up and every 6 h |
| Size | Provider message on the main line ≤ 300 code points (+ "…"); stored provider message ≤ 2,000 bytes; outage report body ≤ 4 KiB |
| Pop-ups | ≤ 3 visible; info/success 5,000 ms, warning 10,000 ms, error and Needs-you until dismissed or handled; hover or focus pauses the countdown |
| Logging | Every dispatched provider request that fails produces exactly one WARN line "provider attempt failed" with `failure_class` ∈ {`network`, `api`, `unknown`}; every accepted browser outage report produces exactly one WARN line with `client_outage_kind` ∈ {`offline`, `server_unreachable`} |
| Voice | A guard fails CI if any in-chat sentence in a scanned source contains `Omnipus` as a word (except the D48 sentence) or `we`, `our`, `us`; if any outside-chat sentence contains `I`, `my`, `me`, `we`, `our`, `us`; or if any scanned sentence contains `!`, an emoji, `sorry`, `oops`, or `please` outside the D48 sentence (§13.4) |
| Reflow | No horizontal scrolling of the centre, preferences, pop-ups or system lines at 320 CSS px width |

---

## 6. Event catalogue (R-EV-1…4, R-PREF-4, D15, D40; D64, D67, D68, D69, D71)

> From here on: design detail (plan-spec Phase 4+), implementation names allowed. This section is the behavioural catalogue only; where each kind is produced and who receives it is §8.1, after the wire contract (§7).

Kind ids are stable strings — the one vocabulary for the centre, preferences, Part 2 external delivery and a future mobile client (#177, §9.5). Existing ids `schedule_failed`, `knowledge_drift` and `goal_paused` are kept. Part 1 has **19 kinds**.

| Kind id | Category | Event (centre label) | Notify | Pop-up | Severity | Chat line | Ext. (P2) | Coalesce key / resolve trigger | Target screen |
|---|---|---|---|---|---|---|---|---|---|
| `approval_requested` | needs_you | Agent needs your approval | On (locked, D69) | Always (locked) | needs_you | unchanged (inline card) | Yes | `approval:<approval_id>`; resolved by every path that emits `tool_approval_resolved` (decision from any tab, timeout, Stop, agent deletion, shutdown) | chat |
| `question_asked` | needs_you | Agent asked a question and is waiting | On (locked) | Always (locked) | needs_you | unchanged (inline card) | Yes | `ask:<card_id>`; resolved on the card's terminal transition (answered, cancelled, auto-resolved) | chat |
| `goal_paused` | needs_you (D67) | Goal paused — waiting for you or blocked | On (locked) | Always (locked) | needs_you (sticky) | its own session (existing) | Yes | `goal_paused:<goal_id>` (existing); resolved when the goal resumes or ends | chat |
| `task_started` | tasks_plans | Task or plan started | Off | Off | info | its own session | No | none; info, auto-handled | task / plan |
| `task_finished` | tasks_plans | Task or plan finished (not scheduled) | Off | Off | success | its own session | No | `task:<task_id>` / `plan:<plan_id>`; info, auto-handled | task / plan |
| `task_failed` | tasks_plans | Task or plan failed | On | On | error | its own session | Yes | `task:<task_id>` resolved when a later run of the same task reaches `done`; `plan:<plan_id>` has no automatic resolve (a failed plan is terminal, `pkg/plan/plan.go::IsTerminal`) | task / plan |
| `schedule_finished` | tasks_plans | Scheduled task finished | On | On | success | its own session | Yes | `schedule:<task_id>` (Calendar) / `schedule:<job_id>` (gateway job); info, auto-handled | task (run) |
| `schedule_failed` | tasks_plans | Scheduled task failed | On | On | error | its own session | Yes | same key; resolved by the next successful run of the same Calendar task / gateway job | task (run) / chat / agent |
| `goal_reached` | tasks_plans | Goal reached | Off | Off | success | its own session | No | `goal:<goal_id>`; auto-handled | chat |
| `goal_given_up` | tasks_plans | Goal given up | Off | Off | warning | its own session | No | `goal:<goal_id>`; auto-handled | chat |
| `provider_unreachable` | system_wide | Provider unreachable (network failure) | On | On | error | affected chats only | Yes | `provider_unreachable:<provider_id>`; incident §14.3 | providers settings |
| `provider_error` | system_wide | Provider returned an error (API failure) | On | On | error | affected chats only | Yes | `provider_error:<provider_id>`; only provider- or account-wide conditions (D71) | providers settings |
| `provider_quota_exhausted` | system_wide | Provider balance or quota exhausted | On | On | error | affected chats only | Yes | `provider_quota:<provider_id>` | providers settings |
| `provider_rate_limited` | system_wide | Provider rate limit hit | On | On | warning | affected chats only | No | `provider_rate:<provider_id>`; only a provider's own 429, never Omnipus's own limiter | providers settings |
| `provider_connections_failing` | system_wide | Connections to AI providers are failing (replaces "Omnipus lost its internet connection", D64) | On | On | error | affected chats only (their own provider lines) | No | `provider_connections`; open while ≥ 2 distinct providers have open `provider_unreachable` incidents (§14.3) | providers settings |
| `connection_restored` | system_wide | Provider connection restored | On | On | success | affected chats only (as the record line) | No | `restored:<provider_id>` / `restored:provider_connections`; info, auto-handled | providers settings |
| `resources_low` | system_wide | System resources low (memory, disk) | On | On | warning | — | Yes | `resources:memory` / `resources:disk`; incident §14.3 | performance settings |
| `concurrency_reduced` | system_wide | Concurrency limit reduced because resources are low | On | On | warning | — | No | `concurrency`; resolved when memory stops binding (measured hosts only, D74) | performance settings |
| `knowledge_drift` | system_wide | Knowledge base search index rebuilt after drift (existing) | On | Off | warning | — | No | existing key | knowledge base |

Removed and reserved:
- `stop_notice` is **removed** from the centre (D68): its three outcomes become a Warning system line in the chat where Stop was pressed (§11.2).
- `notification_delivery_failed` is **reserved** for Part 2 (§29) and is **not** in the Part-1 enum; adding it is a Part-2 contract change.
- REQ's "Omnipus lost its internet connection" kind is **replaced** by `provider_connections_failing` (D64): failed requests cannot prove the internet is down (round-1 MAJ-021). There is no active probe.

Rules:
- **R-EV-1.** The catalogue is closed: the `NotificationKind` enum in the contract (§7.1) is the only list; a producer call with a kind not in it fails without storing or pushing (FR-001), and a unit test walks every router call site's kind against the generated enum.
- **R-EV-3 / D14.** "Affected chat" = a chat whose turn made a provider request that failed, retried, waited or fell back because of the event. The line in that chat is the turn's own retry/error/fallback line (§11); nothing is broadcast to idle chats.
- **R-EV-4 / D12.** A system-wide event appears once in the centre (coalesce key) and at most once as a pop-up per episode (§14.3), plus one line in each affected chat.
- **Scheduled vs not scheduled** (round-1 MAJ-004). A run is *scheduled* only when it was dispatched by the Calendar trigger (`TaskTriggerScheduler.RunScheduled`) or by a gateway schedule job (`scheduledRunner.RunScheduled`). Scheduled runs produce `schedule_*` kinds and never also `task_*` kinds. A manual Run-now of a Calendar task (`TaskRunKindManual`) produces `task_finished` / `task_failed` only (§8.1).
- **Own actions** (D31). Kinds are produced from engine outcomes, never from the user's own edit: a task moved on the Board, a plan stopped by the user, a goal stopped by the user (`stopped_by_user`), a run the user stopped (`Task.CancelReason` set) and the start of a run the user launched produce nothing (§8.1).
- **Needs-you locks** (D69, D67). The three Needs-you kinds have both switches locked on, severity `needs_you` (sticky pop-up), and are listed in every lock rule, test and document as *three* kinds.
- **Turn-failure chat lines (#1857 amendment, §13.8).** The kinds above are unchanged by the #1857 amendment: a failed turn's chat line is not a centre kind. The failure kinds of §13.8 (including the external CLI worker kinds) surface only as the turn's own chat error line (§11) and, through the existing `task_failed` body, as a neutral cause sentence (§13.6).

---

## 7. Wire contract changes (Hard Constraint #8)

> Shapes decided here (architect owns contract shapes); `backend-lead` writes the YAML under `contracts/`, regenerates with `scripts/gen-contracts.sh`, and commits spec + generated artifacts atomically **before any feature code** (WP-0; the one mechanical exception is stated below). Field names below are the wire names. No YAML in this spec. **No schema here is a discriminated union**: every lifecycle variant is its own strict frame type, so the ADR-034 inline-union rule is not needed.

**Hand-kept inline copies (Team B review, 2026-11-06).** `contracts/asyncapi.yaml` holds its own inline copies of schemas that also exist as component files, and the generators read those copies. WP-0 changes both, or the generated types go stale. Besides the two `ProviderFallback` shapes named in §7.4: `ProviderRetryFrame`, `LLMError`, `ReplayErrorFrame`, `ReplayErrorPayload` and `NotificationFrame` (the frame message references this copy, not the component file). WP-0 also searches the file for "in sync by hand", so a copy not listed here is not missed.

**WP-0 must compile at its own push (Team B review, 2026-11-06).** The regenerated types break existing code: `src/store/notifications.ts`, `src/store/chat/slices/replay-and-status-frames.ts` (`case 'notification'`), `src/lib/llm-error.ts` (`provider_message` becomes `message_assembled`), `src/lib/ws.ts`, `pkg/gateway/schedules.go::toNotification` and `pkg/gateway/websocket_forward.go`. WP-0 carries the smallest mechanical fix at those sites, with no behaviour change. The real behaviour stays with WP-1 to WP-4 and their owners; this is the one exception to the file-area table in §1.4.

### 7.1 Changed: `Notification` (`contracts/components/schemas/Notification.yaml`) and `NotificationList`

`Notification` stays strict (`additionalProperties: false`). Greenfield: fields moved into `target` are removed from the top level, no alias.

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | non-empty, minted by the router before any branch (FR-005) |
| `type` | yes | `NotificationKind` (§6; 19 values) |
| `category` | yes, **new** | `needs_you` · `tasks_plans` · `system_wide` (R-NC-2) |
| `severity` | yes | `info` · `success` · `warning` · `error` · `needs_you` (R-PU-1) |
| `title` | yes | neutral, voice-ruled (§13.6) |
| `body` | no | neutral, masked (§15.1) |
| `read` | yes | per-user read state |
| `resolved_at_ms` | no, **new** | set when handled anywhere / condition cleared (R-NC-3) |
| `created_at_ms`, `updated_at_ms` | yes / no | unchanged |
| `revision` | yes, **new** | int64 ≥ 0: the recipient store's revision at this item's last **saved** change; 0 when the item was never saved (§8.4) |
| `persisted` | yes, **new** | bool: false while the item's latest change exists only in the server's memory — a **volatile** item (store write failed, or no account exists; §8.4.2) |
| `live_seq` | yes, **new** | int64 ≥ 0: 0 when the item's latest state is saved; otherwise the recipient's volatile counter at its latest unsaved change (§8.4.1) |
| `workspace_id` | no, **new** | per-workspace preferences and navigation |
| `target` | yes, **new** | `NotificationTarget`, §7.6 |
| `provider_id`, `provider` | no, **new** | provider id and display name for System-wide provider kinds |
| `affected_chat_count` | no, **new** | int ≥ 0, updated on coalesce for provider incidents |
| `causes` | no, **new** | array (≤ 10) of `{ provider: string, reason: FailureReason }` — only for `provider_connections_failing`: the observed causes (D64), ordered by when each provider's incident opened (earliest first, then provider id) |
| `causes_omitted` | no, **new** | int ≥ 0 — only for `provider_connections_failing`: how many further failing providers are not listed in `causes` (R2-MAJ-004); never silently truncated |
| `agent_id` | no | display only |

`schedule_id` and `session_id` move into `target` (greenfield, removed from the top level).

`NotificationList` = `{ items: Notification[], unread_count: int, revision: int64, live_seq: int64 }` — `revision` is the store revision and `live_seq` the recipient's volatile counter at the moment the snapshot was read (§8.4). `items` holds the saved entries **and** the volatile ones (`persisted: false`). `unread_count` = items with `read: false` (a resolved item is read).

### 7.2 Changed: the notification frames (`contracts/asyncapi.yaml`)

The single flat `NotificationFrame` is replaced by **four strict frame types**, each delivered only to the recipient's own connections (today's forwarder filter on the user), none session-sequenced. Every invariant is expressed by the schema itself — a closed `type`, `required`, enums and `additionalProperties: false` — so the generated Go types and the generated Zod schemas both enforce it; no rule depends on a conditional `if/then` or on a server-only assertion (round-2 R2-MAJ-013):

| Frame `type` | Required fields | Meaning |
|---|---|---|
| `notification_created` | `notification` (full `Notification`), `popup` (bool) | a new item. `popup` is the server's verdict from preferences; the browser never re-derives it |
| `notification_changed` | `change` ∈ {`updated`, `read`, `resolved`}, `notification` (full `Notification`) | a change to an existing item. It has **no** `popup` field, so a change can never raise a pop-up |
| `notification_deleted` | `id` (non-empty), `revision` (int64 ≥ 0), `live_seq` (int64 ≥ 0) | one item removed (user delete or sweep of one item) |
| `notification_cleared` | `cleared_ids` (array of non-empty ids, 1…1,000, unique), `revision`, `live_seq` | several items removed at once (Clear all, or a sweep). Idempotent: removing an absent id is a no-op, so a replayed or duplicated frame changes nothing |

- **Counters on removals.** Removal frames carry both counters as they stand right after the removal; §8.4.3 says which counter governs each local item (`revision` for a saved item, `live_seq` for a volatile one).
- **Batches (round-2 R2-MAJ-004).** One operation that removes more than 1,000 items (Clear all, a sweep — D80 allows any number of waiting entries) is still **one** store change with one revision, sent as consecutive `notification_cleared` batches of at most 1,000 ids that all carry that operation's counters. Batches hold disjoint ids and each id is applied on its own by the per-item rule of §8.4.3, so no batch can invalidate another and their arrival order does not matter.
- `WsFrameType` loses `notification` and gains `notification_created`, `notification_changed`, `notification_deleted`, `notification_cleared` (greenfield, no alias). Positive and negative contract fixtures exist for every variant, including 1,000 vs 1,001 ids and 10 vs 11 causes (DS-FRAME, §20). Volatile items travel in the same frames with `persisted: false` and `live_seq` > 0.

### 7.3 New and changed REST operations (`contracts/openapi.yaml`, tag `notifications`)

| Operation id | Method + path | Request | Responses |
|---|---|---|---|
| `listNotifications` (existing) | GET `/notifications` | — | 200 `NotificationList` (now with `revision`) |
| `markNotificationRead` / `markAllNotificationsRead` (existing paths) | unchanged | — | 204 for saved and volatile items; each change is pushed to every connection (FR-007). A read state that cannot be written is kept as a volatile change (§8.4.2), not reported as a failure; 404 unknown id |
| `deleteNotification` | DELETE `/notifications/{id}` | — | 204 for a saved or a volatile item; 404 only when the id is neither saved nor volatile; 500 `notification_store_unwritable` when removing a saved item could not be written (nothing removed, nothing pushed; §8.6). **No 409**: a waiting Needs-you entry may be deleted (D80) |
| `clearNotifications` | DELETE `/notifications` | — | 204; removes every saved and volatile entry of the caller, waiting ones included (D80), as one store change sent in batches (§7.2); 500 `notification_store_unwritable` when the file could not be written (nothing removed, nothing pushed) |
| `getNotificationPreferences` | GET `/notification-preferences` | — | 200 `NotificationPreferences` |
| `updateNotificationPreferences` | PATCH `/notification-preferences` | `NotificationPreferencesUpdate` | 200 `NotificationPreferences`; 400 codes of §5.3; 409 `preferences_changed` (body `NotificationPreferences`, current) or `preferences_unreadable` |
| `reportConnectionOutage` | POST `/client-events/connection-outage` | `ConnectionOutageReport` | 204 (also for a repeated `report_id`); 400 `invalid_outage_report`; 429 with `Retry-After` |

New schema files (`contracts/components/schemas/`):

| Schema | Shape |
|---|---|
| `NotificationKind` | the closed enum of §6 (referenced by `Notification.type`, preferences) |
| `NotificationTarget` | §7.6 |
| `FailureReason` | enum `dns_failure` · `connection_refused` · `network_unreachable` · `tls_failure` · `timed_out` · `connection_dropped` · `http_status` · `invalid_response` · `unknown` (§14.1) |
| `NotificationPreferences` | `{ revision: int64, load_state: ok · defaults · unreadable, catalogue: [ { kind, category, workspace_overridable: bool, default_notify, default_popup, notify_locked, popup_locked } ] (read-only, every Part-1 kind), global: map kind → { notify: bool, popup: bool } (every kind present), workspaces: map workspace_id → map kind → { notify: inherit · on · off, popup: inherit · on · off } (only overridable kinds; only workspaces with at least one non-inherit field) }`. `load_state: defaults` = no file yet (fresh install); `unreadable` = a file exists but could not be read, defaults are in effect (§9.1) |
| `NotificationPreferencesUpdate` | `{ base_revision: int64 (required), replace_unreadable?: bool (default false), global?: map kind → { notify?: bool, popup?: bool }, workspaces?: map workspace_id → map kind → { notify?: inherit · on · off, popup?: inherit · on · off } }` — field-level partial update; `inherit` removes that one field's override; a workspace whose fields are all `inherit` is dropped. The Part-2 switch `external` is **not** added now; it is reserved by name (§29) |
| `ConnectionOutageReport` | `{ report_id: string (UUID, required), kind: offline · server_unreachable, started_at: date-time (first unexpected close observed), last_confirmed_at: date-time (last moment the connection was confirmed alive, ≤ started_at), ended_at: date-time, reconnect_attempts: int ≥ 0, page_reloaded: bool }`, `additionalProperties: false` |
| `SystemLine` | §7.5 |

### 7.4 Changed: provider error, retry and fallback shapes (round-1 MAJ-008; round-2 R2-MAJ-006, R2-MAJ-007, R2-MAJ-011, R2-MAJ-013)

| Schema (owner) | Change |
|---|---|
| `LLMError` (`LLMError.yaml`) | **new optional `provider_says`** (string ≤ 2,000 bytes): the provider's own error message, extracted (§13.3), masked (§15.1), **persisted and replayed** (amends ADR-051, §28). **New optional `failure_class`** enum `network` · `api` (absent for non-provider codes and for `unknown` class). `facts` gains optional `http_status` (int) and `host` (string ≤ 253) — `http_status` renders only in technical details at Verbose/Debug, never on the Normal main line (VOICE hard rule 6, §13.1 VS-2); `host` is named by the final network/API sentence (D64). Enum: **new code `provider_error`** for a provider-side API failure that has no more specific code (408, 5xx, 529, other account-level failures) — `network` keeps only network-class failures. An authentication rejection (401, or 403 for a bad key) keeps code `provider_auth_failed` and its own remedy: the **code** decides the chat line and its action; the **incident kind** of §14.1 is decided separately, so a 401 shows the auth remedy in the chat and raises the account-level `provider_error` incident (D71). `message` stays the finished, server-assembled sentence. **The trust flag `provider_message` (bool) is replaced by `message_assembled` (bool)** with the meaning of §11.4 item 4 (greenfield, no alias). `x-user-messages` is replaced per §13.2 |
| `ReplayErrorPayload`, `ReplayErrorFrame` | carry the same `provider_says`, `failure_class`, `facts.host` and `message_assembled`; the persisted transcript error entry stores them (§13.7); `detail`, `facts.http_status` and `facts.request_id` stay live-only |
| `ProviderRetryFrame` (`ProviderRetryFrame.yaml`) | **new required `text`** (string 1…4,096): the finished cause sentence assembled by the server **without the retry clause**, e.g. `I can't reach OpenRouter: connection refused.` (R2-MAJ-006). `error_code` may now be `network` or `provider_error` (today always `rate_limited`); **new optional `reason`** (`FailureReason`); **new optional `http_status`** int (technical details only); **new optional `provider_says`** (masked, API class only). The existing required `retry_at`, `retry_after_seconds`, `sent_at`, `attempt` and `max_attempts` keep their meaning and feed the retry clause (below) |
| `ProviderFallbackFrame` (live; `ProviderFallbackFrame.yaml` **and** its hand-synced inline copy under `components.schemas` in `contracts/asyncapi.yaml`) | **new required `text`** (string 1…4,096): the finished note sentence, byte-identical to the note text persisted for that turn; **new optional `provider_says`** (masked) and **`failure_class`**, so a reply changed by a fallback quotes why the first model failed (L3) |
| `ProviderFallbackNote` (replay; `ProviderFallbackNote.yaml` **and** its hand-synced inline copy in `contracts/asyncapi.yaml`) | `message` stays the persisted note text verbatim; **new optional `provider_says`** and **`failure_class`**, read from the persisted transcript entry |
| `Message` (`Message.yaml`, REST history; the `provider_fallback` entry) | **new optional `provider_says`** and **`failure_class`** beside the existing `model` / `unavailable_model` / `unavailable_code` facts, so REST history and replay carry the same facts |
| `WsFrameType` | adds `system_line` and the four notification frame types of §7.2; removes `notification` |

**Who writes what on a retry line (R2-MAJ-006).** The server owns every word except the countdown: the line is the frame's `text` followed by **one fixed retry clause**, `Retrying in {countdown} (attempt {attempt} of {max})…`, a single string listed in the wording manifest (§13.5) and checked with the voice skill. The existing `ProviderRetryIndicator` fills that clause from the frame's typed facts only: the countdown from `retry_at` and `sent_at` with today's skew-immune rule (whole seconds, "8 s"), and `attempt` / `max_attempts`. This is the one place the browser fills slots; it fills numbers only, and there is exactly one countdown composer, so no server text can carry a stale "8 s". The finished fallback note is not composed in the browser at all: live it is `ProviderFallbackFrame.text`, after reload and in paged history it is the persisted `message` — the same bytes.

### 7.5 New: `SystemLine` content, `SystemLineFrame` transport and `Message.system_line` history (round-1 MAJ-002)

A server-authored, persisted chat line for messages that **have no structured carrier today** (D79: existing typed frames stay and are mapped to the renderer; only new lines use this carrier). The persisted **content** and the live **transport** are separate:

`SystemLine` (content — persisted, shared by live and history):

| Field | Meaning |
|---|---|
| `line_id` | stable id; a later line with `replaces_line_id` updates it in place; the client de-duplicates live and history copies by `line_id` |
| `line_type` | `info` · `warning` · `error` · `resolved` · `needs_you` (R-SL-3) |
| `in_progress` | bool (R-SL-4) |
| `text` | final wording, server-authored, written with the voice skill |
| `provider_says` | optional, masked |
| `action` | optional `SystemLineAction` `{ kind: switch_model · open_provider_settings · open_task · open_performance_settings · retry_reply · review, label, session_id?, turn_id?, task_id?, workspace_id?, provider_id? }` — required ids per kind in §7.6 (R-SL-7) |
| `details` | optional masked technical text — rendered only at Verbose (collapsed) / Debug (open) (L9) |
| `min_level` | `normal` · `verbose` — the lowest detail level that shows the line (record lines are `verbose`, R-SL-5) |
| `replaces_line_id` | optional |
| `created_at` | date-time |

`SystemLineFrame` (transport) = `{ type: system_line, session_id, seq, line: SystemLine, history_origin?: HistoryEntryOrigin }` — `seq` for live hub-sequenced delivery; `history_origin` required on every entry-derived historical copy, exactly as for the other carriers (`contracts/components/schemas/HistoryEntryOrigin.yaml`).

History integration (all four are named because each one rejects an unknown carrier today):
1. **Persist**: transcript entry `type: system`, `Message.system_subtype` gains `system_line`, `Message` gains optional `system_line: SystemLine` (`contracts/components/schemas/Message.yaml`).
2. **Replay**: `pkg/gateway/replay.go` discriminates on the stamped subtype and re-emits a `SystemLineFrame`; `pkg/gateway/history_replay_entry.go::stampHistoryOrigin` gains the `generated.SystemLineFrame` case (today its default returns `unclassified historical frame`).
3. **Prepared history page**: `pkg/gateway/history_page_prepare.go::validateHistorySourceFrame` gains `"system_line": "SystemLineFrame"`; `prepareGroup` weighs the frame's encoded bytes like every other carrier.
4. **REST history**: `pkg/gateway/history_page_prepare.go::historyWireMessage` projects the entry to `Message.system_line`.

Parity is semantic: live, reload and paged history render the same `SystemLine` content; live `seq` and historical `history_origin` differ by design and are not compared. When history is unavailable, the existing history-unavailable state is shown and no system line is invented. Lines that must leave history (browser↔server loss, R-SL-6) are **client-only** and never use this carrier.

### 7.6 Targets and actions — required identifiers per screen (round-1 MAJ-013)

`NotificationTarget` = `{ screen, workspace_id?, session_id?, task_id?, run_id?, plan_id?, agent_id?, root_path?, collection_id?, provider_id? }`. Every entry has a usable screen (R-NC-6); there is no `none`.

| `screen` | Required ids | Used by | When the target is gone |
|---|---|---|---|
| `chat` | `session_id` | Needs-you kinds, goal kinds, gateway schedule run with a session | The centre/pop-up says "This chat no longer exists." and stays on the current screen |
| `task` | `workspace_id`, `task_id`; `run_id` + `session_id` when a run exists | task and Calendar schedule kinds | Opens the task without the run when the run is gone; "This task no longer exists." when the task is gone |
| `plan` | `workspace_id`, `plan_id` | plan kinds | "This plan no longer exists." |
| `agent` | `agent_id` | a gateway schedule job (heartbeat) that failed before any session existed (`scheduledRunner.RunScheduled` calls `onFailure` with an empty session on owner/setup failures) | "This agent no longer exists." |
| `providers_settings` | `provider_id` optional | provider kinds | Settings → Providers opens without a selection |
| `performance_settings` | — | `resources_low`, `concurrency_reduced` | — |
| `knowledge_base` | `workspace_id`, `root_path` (workspace-relative, the same form as `KnowledgeBaseInfo.root_path`), `collection_id` (opaque, as `KnowledgeBaseInfo.collection_id`) | `knowledge_drift` (mapping below) | the destination loads `getKnowledgeBaseInfo` (`GET /library/{workspace_id}/knowledge?path={root_path}`); a 404, `is_knowledge_base: false` or a different `collection_id` → "This knowledge base is no longer available here." |

| Line action `kind` | Required ids | Behaviour |
|---|---|---|
| `switch_model` | `session_id` | opens that chat's model picker (existing) |
| `open_provider_settings` | `provider_id` optional | navigates |
| `open_task` | `workspace_id`, `task_id` | navigates |
| `open_performance_settings` | — | navigates |
| `retry_reply` | `session_id`, `turn_id` | re-sends through the existing regenerate path; if that turn is no longer the latest, the line says "This reply can no longer be retried." and does nothing |
| `review` | `session_id` | opens the chat with the pending card |

Navigation never trusts a carried id: the destination screen loads its object through the normal authenticated REST call and shows the "no longer exists" text on 404. Ids are never parsed from text and no session id is fabricated.

**Knowledge-drift mapping (round-2 R2-MAJ-012).** The producer `knowledgeDriftNotifier` (`pkg/gateway/knowledge_drift_notify.go`) receives a `knowledge.DriftReport`, whose only identity is `Root`, the collection root's resolved real path (`pkg/knowledge/drift.go::DriftReport`). The target is built from the existing mount registry, never guessed: `KnowledgeLifecycle` (`pkg/gateway/knowledge_lifecycle.go`) keys every collection on that same resolved root and records the mounts holding it (`knowledgeCollection.holders`, keyed by `knowledgeMountKey{workspaceID, name}`), and `KnowledgeLifecycle.CollectionIDFor` derives the `collection_id`. One new lifecycle accessor returns a root's holders as `(workspace_id, root_path)` pairs, where `root_path` is the collection root relative to that workspace's work tree (a mount sits in `work/` under its name, a single path segment — `pkg/workspace/mount.go::Mount`); the backend owner reuses the path rules `getKnowledgeBaseInfo` applies to `root_path`, and T-43 pins the mapping against the real lifecycle.
- **One mount**: the target names it.
- **Several mounts** (one folder mounted into several workspaces, sharing one `collection_id`): the first pair in (`workspace_id`, mount name) order — deterministic; the entry body names the folder, so the reader knows which collection it is.
- **No mount at the moment of the report** (the last mount was just removed): no entry is created and one WARN "knowledge drift without a mount" is logged; the drift schedule stops when the last holder leaves, so this is a race, not a state.
- **Mount removed later**: the destination's own check above shows "This knowledge base is no longer available here."
Recipients are unchanged (`knowledgeDriftRecipients`: every account, §8.1); the coalesce key stays `knowledge_drift:<root>`.

---

## 8. Notification centre (R-NC-1…6, R-SEC-2; D19, D23, D24, D80)

### 8.1 Source and recipient map (round-1 MAJ-004, MAJ-005)

Every kind has exactly one producing source in today's code. Producers listen to **engine outcomes** (run lifecycle, approval and card lifecycle, provider attempts), never to REST status edits, so the user's own Board, plan and goal actions produce nothing (D31).

| Kind(s) | Producing source (verified symbol) | Classification rule | Recipient |
|---|---|---|---|
| `approval_requested` | `pkg/gateway/policy_approver.go` → `WSHandler.broadcastToolApprovalRequired` (`pkg/gateway/ws_tool_approval.go`) | every pending approval | root session owner (rule R1 below) |
| `question_asked` | `WSHandler.broadcastAskUserCard` (`pkg/gateway/ws_ask_user.go`) with a `pending` card | every pending card | R1 |
| `goal_paused` | `AgentLoop.notifyGoalPaused` (`pkg/agent/goal_pause_notify.go`) → `goalPausedNotifier` (`pkg/gateway/goal_pause_notify.go`) — existing | existing | R1 (keeps today's unreadable-owner rule) |
| `goal_reached`, `goal_given_up` | goal outcome event (`pkg/agent/events.go::EventKindGoalOutcome`, `pkg/agent/goal_outcome.go`) | `met` → reached; `rounds_exhausted`, `other` → given up; `stopped_by_user` → nothing (own action) | R1 |
| `schedule_finished`, `schedule_failed` (Calendar task) | `TaskTriggerScheduler.RunScheduled` (`pkg/agent/task_trigger.go`) → `TaskExecutor.SpawnTriggeredRun` → `ExecuteTask` → `TaskExecutor.runTask` (dispatch origin captured) → `TaskExecutor.completeTaskWithResult` (`pkg/agent/task_executor_judge.go`, outcome applied) — see "Task outcome source" below | by the **dispatch origin**: run kind `TaskRunKindScheduled` **and** trigger type `once` or `recurring` at dispatch → schedule kind; status `done` → finished, `failed` without `CancelReason` → failed; a run the user stopped (`Task.CancelReason` set) → nothing | R2 (workspace object) |
| `schedule_failed` (Calendar dispatch failure) | `TaskTriggerScheduler.RunScheduled` returning an error **before** a run opens (task missing, spawn refused) | one `schedule_failed`, target `task` without `run_id` | R2 |
| `schedule_finished`, `schedule_failed` (gateway job: heartbeat or API schedule) | `scheduledRunner.RunScheduled` (`pkg/gateway/schedules.go`) success path (new hook) and `onFailure` (existing) | coalesce key `schedule:<job_id>`; a failure before any session exists (owner unavailable, worker, session pick) gets target `agent` | R1 when the run has a session; otherwise R2 |
| `task_started`, `task_finished`, `task_failed` (task) | the same execution (`runTask` entry → started; applied `completeTaskWithResult` → finished / failed) — see "Task outcome source" below | **automatic runs** that are not Calendar-scheduled (queue drain, dependency unblock, plan dispatch: `TaskRunKindScheduled` with trigger `manual`): started, finished, failed. **Manual runs** (`TaskRunKindManual`: Run now, `StartOccurrenceRun`): finished and failed only — starting it is the user's own action. A user Stop (`CancelReason`) → nothing | R2 |
| `task_started`, `task_finished`, `task_failed` (plan) | `EmitPlanStatusChanged` (`pkg/agent/loop_events.go`) with a new internal, **non-wire** `origin` (`engine` · `rest`) on the plan-status hook | only `origin: engine`; `running` (first time) → started, `done` → finished, `failed` → failed; REST mutations are own actions → nothing | R2 |
| `provider_*`, `provider_connections_failing`, `connection_restored` | incident tracker (§14.3) fed by the per-attempt observation (§14.2) | §14.3 | R3 |
| `resources_low` (memory), `concurrency_reduced` | `config.MemoryPressureHigh` transitions; `applyMemoryCap` (`pkg/agent/admission.go`) becoming memory-binding on a measured host (D74) | §14.3 | R3 |
| `resources_low` (disk) | `fileutil.WriteFileAtomic` (`pkg/fileutil/file.go`) failing with no-space (Unix `ENOSPC`; Windows `ERROR_DISK_FULL`, `ERROR_HANDLE_DISK_FULL`) (D73) | §14.3 | R3 |
| `knowledge_drift` | `knowledgeDriftNotifier` (`pkg/gateway/knowledge_drift_notify.go`) — existing; its target is built from the mount registry (§7.6, "Knowledge-drift mapping") | existing | existing (`knowledgeDriftRecipients`: every account) |

**Task outcome source (round-2 R2-MAJ-009).** Run history is best-effort by design: `TaskExecutor.openRun` logs ERROR and returns nil when the run record cannot be opened, and execution continues; `TaskExecutor.closeRun` returns before `emitRunStatus` for a nil run or a failed `CloseRun` (`pkg/agent/task_executor_run.go`). Task and schedule notifications therefore do **not** hang off `emitRunStatus`:
1. **Dispatch origin.** At execution entry, `TaskExecutor.runTask` captures one immutable value: task id, workspace id, task session id, run kind, the trigger type as dispatched, occurrence time, plan id when the task belongs to a plan, and the run id when `openRun` succeeded. It is a copy, never re-read from the task later: a task edit can replace `Trigger` (`pkg/task/store.go::storeUpdateLocked.applyRemainingFields`), and an execution keeps the class it was launched with. `task_started` (automatic runs only) is published here.
2. **Outcome.** The terminal outcome is published from the authoritative completion write, `TaskExecutor.completeTaskWithResult` (`pkg/agent/task_executor_judge.go`), only when it reports `applied`; its conditional status update is the duplicate suppression, so a second close (panic recovery, post-completion housekeeping) never notifies twice. Every other terminal path (user Stop, a failure after the origin was captured) is enumerated by T-37 and publishes through the same function.
3. **Run id.** The target carries `run_id` only when a run record exists. A failed run open or close is still logged ERROR exactly as today; no `run_id` is invented.

Not a producer in Part 1 (unchanged behaviour): `/loop` fires (`LoopScheduler.RunScheduled`, `pkg/agent/loop_scheduler.go`) run inside the chat the user started the loop in and report there; they raise no notification today and none is added (§31.1). The Stop outcome (`publishStopNotice`) becomes a chat line (D68).

**Recipient rules.**
- **R1 — session-scoped.** The root session's `UnifiedMeta.Owner` (stamped by authentication when the session is created, `pkg/session/unified.go`). Readable and set → that user. Readable but empty (a session created by a schedule or channel with no authenticated user) → **every configured account**, until per-person access exists (#1421) (D83; founder answer OQ-2 = A). Unreadable → **no** recipient and one ERROR line (keeps `notifyGoalPaused`'s rule; D24).
- **R2 — workspace object** (task, plan, Calendar schedule). **Every configured account**, until per-person access exists (#1421) (D83; founder answer OQ-2 = A, 2026-11-05). Background for the choice: no per-account access gate exists for workspace objects today (`pkg/gateway/rest_workspaces.go` caller comment: "all post-gate role/multi-user checks were removed"; issue #1421), so every account can already see and act on them; and `Task.Owner` / `Task.CreatedBy` / `Plan.Owner` / `job.CreatedBy` may hold either a person's or an agent's name (`pkg/task/task.go` NAMESPACE WARNING), so they are never used as recipients.
- **R3 — system-wide.** Every configured account.
- **No accounts at all** (first start before onboarding): a volatile item (§8.4.2) with a minted id, `persisted: false`, `revision: 0`, log WARN (closes #645 Defect 1).
- Recipient lists are computed per call; nothing assumes one user (D24).

### 8.2 One producer path — the router

All producers call one function in `pkg/notifications`, never `Store.Create`:

```
producer ──> router.Notify(kind, recipient input, title, body, target, coalesce key)
              │ 1. reject a kind not in the enum (error, nothing stored, nothing pushed)
              │ 2. resolve recipients (§8.1 R1–R3)
              │ 3. per recipient: read preferences (workspace field → global → catalogue default)
              │ 4. notify off → stop (debug log only; the event is in logs/transcripts)
              │ 5. mint id at construction (never empty), mask title/body (§15.1)
              │ 6. under the recipient's sidecar lock (§15.3): coalesce on the unresolved key or
              │    create; bump the store revision; write atomically
              │    write fails → log ERROR, keep the change in the volatile table, deliver it
              │    with persisted:false and a new live_seq (§8.4.2)
              │ 7. emit `notification_created` {notification, popup} or
              │    `notification_changed` {change, notification} to that recipient
              v
             store file  +  live frame  ──> SPA store (reconciliation §8.4) ──> centre / pop-up
```

Resolution, read, delete and clear take the same lock and emit the same frames (§7.2). "Handled anywhere = resolved everywhere" (D19): the approval path already emits `tool_approval_resolved`; the router resolves `approval:<id>` in the same step and emits `notification_changed` with `change: resolved`.

### 8.3 Lifecycle

| State | Set by | Frame | Effect |
|---|---|---|---|
| created (unread) | router | `notification_created` | badge +1; pop-up if `popup` |
| updated | router coalesce | `notification_changed` `updated` | content refreshed (e.g. `affected_chat_count`, `causes`); no new pop-up; read state unchanged |
| read | user opens it or marks read | `notification_changed` `read` | badge −1 |
| resolved | condition cleared (§6 resolve trigger), or handled through its action anywhere | `notification_changed` `resolved` | also read; pop-up removed in every tab; entry greyed "Handled" |
| re-opened | incident re-fails within 10 min of resolving (§14.3) | `notification_changed` `updated` (resolved_at_ms removed) | unread again; **no** new pop-up |
| deleted | user, or sweep of one item | `notification_deleted` | gone from storage (R-SEC-2) |
| cleared | user Clear all, or a sweep of several | `notification_cleared` | gone from storage |

Deleting or clearing never touches the underlying request (D80): an approval or question stays pending in its chat, its sidebar cue stays, and its resolve trigger simply finds nothing to resolve.

### 8.4 Reconciliation between tabs, pushes and refetches (round-1 MAJ-003; round-2 R2-MAJ-002, R2-MAJ-003)

#### 8.4.1 Two counters

The server keeps two monotonic counters per recipient:
- the **store revision**, persisted in the recipient's file and incremented once per saved change (create, update, read, resolve, delete, clear, sweep) under the lock;
- the **volatile counter** `live_seq`, in memory, incremented once per unsaved change (§8.4.2); its next value is max(previous + 1, current Unix time in microseconds), so it keeps increasing across a server restart unless the system clock steps back.

Every item carries `revision` (the store revision of its last saved change; 0 if never saved) and `live_seq` (0 when its latest state is saved). Every removal frame and every snapshot carries both counters, as they stand right after the removal or at the moment the snapshot was read.

#### 8.4.2 Volatile items (store write failed, or no account)

When the recipient's store cannot be written, the router keeps the change in an in-memory **volatile table** for that recipient and pushes it with `persisted: false`, a new `live_seq`, and `revision` = the item's last saved revision (0 if it was never saved). This covers both a never-saved item and a failed update of a saved one (the file keeps the older saved state). The table holds at most 200 items per recipient (eviction like the count cap of §8.5, never an unresolved Needs-you item) and is lost when the server process stops. Volatile items:
- are updated, coalesced and resolved by the router like saved ones (the router looks in the store, then in the volatile table);
- are included in `GET /notifications` with `persisted: false`, so a page reload while the same server process runs keeps them; each id appears once — a volatile overlay is listed instead of the saved copy it overlays;
- accept mark read, delete and Clear all through the normal REST calls (204; 404 only for an id that is neither saved nor volatile, §7.3);
- become saved at the recipient's next successful write: the router first folds that recipient's volatile items into the file and pushes each one as a saved change (`persisted: true`, a new `revision`, `live_seq: 0`);
- show "Not saved — lost if the server restarts" in the centre (§8.6).

A failed **delete or clear** of a saved item is never turned into a volatile change, because that would claim a removal that did not happen: the call returns 500 `notification_store_unwritable`, nothing is pushed, and the browser restores the item (§8.6).

#### 8.4.3 One ordering rule for upserts, removals and snapshots

One module, `src/store/notifications.ts`, replacing today's replace-on-hydrate. For every id the browser keeps a **known revision** and a **known volatile value** — each the larger of the local item's value and the id's tombstone value — and two **snapshot floors**, F_r and F_v: the counters of the newest snapshot applied. The rule:

> An incoming item state, or a removal, applies to an id only if it is newer **on the counter that governs it**. A saved state (`live_seq` 0) and the removal of a saved local item are governed by `revision`: they apply only if their revision is greater than the id's known revision **and** greater than F_r. A volatile state (`live_seq` > 0) and the removal of a volatile local item are governed by `live_seq`: they apply only if their `live_seq` is greater than the id's known volatile value **and** greater than F_v, and the volatile state's `revision` is not older than the local item's saved revision. A saved state whose revision is greater than a volatile local item's saved base replaces it — the saved write is newer.

Applied to each input:
1. **Upsert** (`notification_created`, `notification_changed`): applies by the rule.
2. **Removal** (`notification_deleted`, each id of a `notification_cleared` batch): removes the local item if the rule says the removal is newer (so a late old delete never removes a re-created item), and records a tombstone (id → both counters) unless a newer local item exists — also for an id this tab never saw, so a later stale upsert of it is rejected.
3. **Snapshot** (`GET /notifications`, counters R and L): a snapshot that is not newer than the floors (R < F_r, or R = F_r and L ≤ F_v) is stale and ignored entirely. Otherwise each snapshot item is accepted by the rule (a tombstone or a newer local copy wins); local items the snapshot covers but does not contain are removed (saved items with revision ≤ R, volatile items with `live_seq` ≤ L — a volatile item missing from a newer snapshot was deleted or lost in a server restart); local items newer than the snapshot are kept. Then F_r = R and F_v = L, and every tombstone at or below the new floors is retired: the floor now rejects any older state, so the tombstone is no longer needed.
4. **Refetch** on every successful WebSocket (re-)authentication and on window focus; the unread badge is recomputed from the merged list after every frame and every snapshot.

Results (each pinned by a DS-RECON row, T-40): a tab offline during a delete loses the entry on reconnect (US-2 AC-4); a slow GET never rolls back a newer push; a delayed older snapshot cannot bring back an entry deleted after it was read; an old upsert arriving after its tombstone was retired is rejected by the floor; a late old delete never removes a re-created entry; volatile updates and resolutions apply in order; a volatile entry turns into a saved one without flicker.

### 8.5 Retention (R-NC-4, R-SEC-2; D23, D80)

- **Age.** Every entry with `created_at_ms` ≥ 30 × 24 h old is deleted — waiting Needs-you entries included (D80; the request keeps waiting). Sweep at start-up, every 6 h, and on every write to that recipient's file; the sweep removes everything due for a recipient in one store change and sends the ids in `notification_cleared` batches of at most 1,000 (§7.2). Quarantined corrupt files (`<file>.corrupt-<unixnano>`, written by `pkg/notifications/store.go`) older than 30 days are deleted by the same sweep.
- **Count.** The cap of 50 stays as a safety bound, but eviction removes only handled or informational entries, oldest first; unresolved Needs-you entries are **never** evicted, so a file may hold more than 50 entries when more than 50 requests are waiting (MIN-002). The cap is therefore not a hard bound; the age rule is.
- **Delete one / Clear all.** Real removal from the file, waiting entries included (D80); no 409. A Clear all of more than 1,000 entries is one store change sent as several batches (§7.2).

### 8.6 Centre UI and states (R-NC-2, R-NC-6; round-1 MAJ-016, MAJ-017)

- Same place: account menu → **Notifications** opens the right-hand `sheet` (design-system catalogue entry) — focus moves into it, focus is trapped while open, **Escape** closes it and focus returns to the menu button (D33 keeps the sidebar).
- Filter: one `segmented-control` **All · Needs you · Tasks & plans · System-wide**, each with its count (unread items in that category); arrow keys move between options.
- Each row: a primary `button` (icon and colour by severity, title, short body, relative time, "Handled" text when resolved) that navigates to its target (§7.6), and a separate `icon-button` **Delete** with an accessible name "Delete notification: {title}". Today the whole row is one button (`src/components/layout/NotificationPanel.tsx`); it is split so delete is a real, separate control.
- Header: **Mark all read** (`button`) and **Clear all** (`button` → `confirm-dialog`, a dialog the user opens, D17).
- Focus repair: when the focused row disappears (deleted, cleared in another tab), focus moves to the next row, else the previous, else the filter.
- New live entries are announced once (polite) on `created` only; snapshot hydration and replays are never announced.

| State | When | What the user sees |
|---|---|---|
| Loading | first fetch in flight, nothing cached | `skeleton` rows |
| Empty (all) | fetch succeeded, no items | `empty-state` "No notifications" |
| Empty (filter) | items exist, none in this category | `empty-state` "Nothing under {category}" with **Show all** |
| Load failed, nothing cached | fetch failed | `query-error-state` "Notifications could not be loaded." with **Retry**; never an empty list |
| Load failed, cached items | refetch failed | cached list stays; a `FormError` line "Could not refresh — showing the last loaded list." with **Retry** |
| Item not saved | `persisted: false` (volatile, §8.4.2) | row shows "Not saved — lost if the server restarts"; it can be opened, marked read and deleted like any entry, in every tab, and survives a page reload while the server keeps running |
| Mutation failed | delete or Clear all rejected (500 `notification_store_unwritable`) or a network error | the item is restored (no optimistic loss) and the row or header shows "Could not delete — Retry" (or "Could not clear — Retry"); **Retry** repeats the same call. A read state that cannot be saved is not a failure: it becomes a not-saved change (§8.4.2) |
| Target gone | navigation found 404 | the destination says "This … no longer exists." (§7.6); the entry stays |

---

## 9. Preferences (R-PREF-1…4; D3, D20, D22, D30, D69; round-1 MAJ-012)

### 9.1 Model and storage

- Per user, server-side, in `u_<hex>.prefs.json` next to the notification file (§15.2), atomic write, same sidecar lock. Server-side because the router must apply it when no browser is open, and Part 2 / a mobile client need the same record.
- The file holds `revision` (int64, +1 per accepted update), `global` (kind → `{notify, popup}`) and `workspaces` (workspace id → kind → field overrides). An absent field means **inherit**.
- **Resolution per event and per field**: workspace override for the event's `workspace_id` (Tasks & plans and Needs-you kinds only) → global → catalogue default. Notify and pop-up resolve independently, so a workspace may override only one of them (US-4 AC-6).
- **Also pop up** is effective only when **Notify me** resolves on; the server ignores pop-up otherwise and the UI disables that switch.
- **Locked switches** (D69, D67): both switches of `approval_requested`, `question_asked`, `goal_paused` are on and locked; the server rejects setting them off (400 `notification_switch_locked`) and the catalogue part of the GET response reports `notify_locked` / `popup_locked`.
- **load_state**: `defaults` = no file (fresh install, normal); `ok` = file read; `unreadable` = a file exists but could not be read or parsed → defaults are in effect, one ERROR is logged per read, and the file is **not** overwritten until the user confirms (`replace_unreadable: true`).
- **Workspace removal**: an override for a workspace that no longer exists is ignored at resolution and dropped on the next accepted update.

### 9.2 Update protocol

- PATCH with `base_revision` (required). If it differs from the stored revision → 409 `preferences_changed` with the current preferences; nothing is written.
- If `load_state` is `unreadable` and `replace_unreadable` is not true → 409 `preferences_unreadable`; nothing is written.
- Unknown kind → 400 `unknown_notification_kind`; System-wide kind under a workspace → 400 `system_wide_kind_global_only`; unknown workspace id → 400 `unknown_workspace`; locked switch off → 400 `notification_switch_locked`. Any 400 rejects the whole update.
- A field set to `inherit` removes that override; a workspace whose fields are all inherited is dropped.
- The browser serialises auto-saves (one request in flight per screen; later edits are queued and coalesced) so its own saves never conflict with each other.

### 9.3 UI

- **Settings → Notifications** (new tab beside Chat in `SettingsScreen`): three category groups; per kind one row with a one-line description and two `switch` controls **Notify me** / **Also pop up**; Needs-you rows show the lock and "Always on — an agent is waiting for you". Save state via `AutoSaveIndicator` ("Saved" / "Saving…" / "Not saved — Retry").
- **Workspace → Settings tab** (`src/components/workspaces/WorkspaceSettingsTab.tsx`): "Notifications for this workspace" — Tasks & plans and Needs-you kinds; per field a three-option `segmented-control` **Use global · On · Off** (Needs-you shown locked); System-wide kinds are not listed (R-PREF-2).
- Own-action rule: no pop-up for any save result (R-PU-5).

| State | What the user sees |
|---|---|
| Loading | `skeleton` |
| `defaults` / `ok` | the switches |
| `unreadable` | `error-state` banner "Your saved notification settings could not be read. Defaults are in effect." with **Retry**; the first save opens a `confirm-dialog` "Replace the saved settings?" and then sends `replace_unreadable: true` |
| Load failed | `query-error-state` with **Retry**; no switches shown (never default-looking switches that are not the real state) |
| Save conflict (409 `preferences_changed`) | `FormError` "These settings changed in another tab." with **Reload settings**; the unsaved edit is kept visible until reload |
| Save failed (network, 5xx) | `AutoSaveIndicator` "Not saved — Retry" |
| Save rejected (400) | `FormError` with the reason (e.g. "This switch is always on."); the switch returns to its saved value |

### 9.4 What preferences do not govern

Chat lines (always shown per detail level), logs, transcripts, the sidebar attention cue, page-level indicators (§10.2).

### 9.5 Relationship to #177 (round-1 OBS-001)

#177 asks for per-**device** push categories (`tool_approvals`, `agent_dms`, `task_completion`) for a future mobile channel. This spec's per-user, per-kind model is **not** a superset of it: `agent_dms` (chat messages) has no kind here, because chat content never leaves through notifications (R-SEC-3), and a device-level filter is a separate dimension. The mapping, when #177 is built: `tool_approvals` → `approval_requested` + `question_asked` (+ `goal_paused`); `task_completion` → `task_finished`, `task_failed`, `schedule_finished`, `schedule_failed`. A device filter would apply after the user's per-kind switches. Nothing in Part 1 blocks #177; Part 2 must state its own mapping (§29).

---

## 10. Pop-ups and page-level indicators (R-PU-1…6; D21, D30, D31, D39, D70, D77)

### 10.1 Pop-ups — centre-backed events only

| Rule | Design |
|---|---|
| Source | Only a `notification_created` frame with `popup: true`. Never a re-open, an update or an own-action result. Browser-local conditions never pop up (§10.2, D77) |
| Host | The existing bottom-right `ToastContainer` (`toast-container`, design-system catalogue) becomes the pop-up host; `addToast` is deleted when WP-5 has removed every call |
| Duration | info / success 5,000 ms; warning 10,000 ms; error and needs_you until dismissed or handled. Pointer hover **or** keyboard focus inside pauses the countdown; leaving resumes it with the remaining time (not a fresh full duration) |
| Cap | ≤ 3 visible, newest at the bottom; further ones collapse into one line "+N more — open notifications" that opens the centre, where every hidden pop-up is a centre entry (all pop-ups are centre-backed, so overflow is always recoverable — MAJ-015). A resolved pop-up frees its slot and the oldest queued one takes it with its full duration |
| Action | ≤ 1 main action from `target` (**Open**, **Review**, **Open settings**), plus close (×) with the accessible name "Dismiss". The action navigates (D39); it never approves or denies in place |
| Remove | `resolved`, `notification_deleted` and `notification_cleared` remove a showing pop-up in every tab |
| Needs-you in the open chat (D70) | No pop-up in a tab where that chat is open **and** the tab is visible (`document.visibilityState` visible); the inline card is the surface. Other tabs, and the same tab when hidden, pop up |
| No modals | No event opens a modal; `ToolApprovalModal` and its feature switch are deleted (R-RM-2) |
| Accessibility | The host is a polite live region; error and needs_you items use `role="alert"`; each pop-up is announced once when it appears, never again on countdown or re-render; keyboard users reach the host with the existing skip order and close with Escape when focus is inside; reduced motion turns slide-in into an instant appear |

### 10.2 Page-level indicators for browser-local conditions (D77)

The audit's ten "keep as event pop-up" sites (§3.1 C2) describe conditions only the browser knows. They become **persistent indicators** on the page or app frame (US-5 AC-8), each with its action, and never enter the centre:

| Audit row (`file::symbol`) | Condition (audit message) | Home |
|---|---|---|
| w3-018 `src/components/chat/OmnipusRuntimeProvider.tsx::WsLifecycle` | saved history is large and loads progressively | a loading line at the top of that chat's message list until history has loaded |
| w3-058 `src/components/agents/useToolIterationLimitRefusal.ts::useToolIterationLimitRefusal::save` | an agent's own tool-call limit was not saved | inline `FormError` beside that field with **Retry** (stays until saved or changed) |
| w4-035 `src/components/browser/BrowserLiveView.tsx::BrowserLiveView::onAutomaticRecovery` | live-browser input resumed; some recent actions were not sent | a persistent notice inside the live-browser panel until the next user input |
| w4-047 `src/hooks/useVersionCheck.ts::useVersionCheck::checkVersion` | a new app version is available | app-frame banner (`AppShell` banner area) with **Refresh** |
| w4-048 `src/hooks/useRunningActivity.ts::useRunningActivity` | task and scheduler runs could not be refreshed | a stale-data line on the running-activity list with **Retry**, until a refresh succeeds |
| w4-062 `src/lib/queryClient.ts::_handleApiSchemaError` | the server sent data the page could not read | `error-state` on the affected view naming the endpoint, with **Refresh** |
| w4-068 `src/store/session.ts::attachToSession` | not connected; the session finishes loading when the connection is back | the chat's loading state plus the app-wide connection indicator (§11.3) |
| w4-085 `src/store/chat/slices/frames.ts::scheduleGapReattachRetry` | the conversation may be out of sync; re-attaching keeps failing | a chat-level banner with **Refresh** until re-attached |
| w4-086 `src/store/chat/slices/frames.ts::createFrameSlice` | the server sends events this UI does not understand | app-frame banner with **Refresh** |
| w4-087 `src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` | system at capacity (`system_overload`) | Warning system line in that chat (§11.2). Round-1 reading found no server producer of this frame today; the implementing lead confirms with a search before WP-5, and if none exists the branch is deleted with its toast instead |

The condition text above is the audit record's message, abbreviated; the final wording is rewritten with the voice skill (§13.5). The implementing lead re-reads each audit row and its symbol before editing (the code may have moved since the audit). The two `dev-only (keep)` rows (w4-061, w4-082) are unchanged.

---

## 11. In-chat system lines (R-SL-1…7; D5, D6, D45, D68, D79)

### 11.1 One renderer

One frontend component, **`SystemLine`** (a chat domain component in `src/components/chat/`, not published to the design-system catalogue, so the four-part publication contract does not apply), renders every system message in a chat, live and replayed, server- or browser-originated (R-SL-1). It is one compact message unit, not a card (R-SL-2): it may wrap onto several visual lines and never scrolls horizontally. The frontend owner loads the `omnipus-design-system` skill first and composes catalogued primitives only (`icon-button`, `button`, `disclosure-row`, `tooltip`).

| Line type | Icon (Phosphor) | Colour token | Text weight | Accessible prefix (visually hidden) | Example |
|---|---|---|---|---|---|
| info | `Info` | `--color-info` | regular | "Info:" | "Working — reading the CRM export" (in progress) |
| warning | `Warning` | `--color-warning` | medium | "Warning:" | "I can't reach OpenRouter: connection refused. Retrying in 8 s (attempt 2 of 5)…" (in progress) |
| error | `XCircle` | `--color-error` | semibold | "Error:" | `OpenRouter says your account is out of credit: "Insufficient balance". Top up your OpenRouter account, or switch to another provider.` |
| resolved | `CheckCircle` | `--color-success` | regular | "Resolved:" | "Connection to OpenRouter restored after 2 retries (14 s)." (Verbose record line) |
| needs_you | `HandPalm` | `--color-accent` | semibold | "Needs you:" | "Waiting for your approval to run `git push`." |

- **In progress** (R-SL-4): any type; the icon pulses and the line ends with an animated ellipsis; `prefers-reduced-motion` makes both static.
- **Announcements**: a live line is announced once when it appears and once per new retry attempt (the existing `ProviderRetryIndicator` once-per-attempt live region is kept); the countdown is never announced; replayed and paged history is never announced.
- **Provider message**: on the main line in quotes, plain text (no Markdown/HTML), cut at 300 code points with "…" (§13.3).
- **Action** (R-SL-7): at most one, as a small text `button` at the end of the line with its required ids (§7.6); a stale action says so instead of acting.
- **Technical details** (L9): a `disclosure-row` "Technical details" under the line — Verbose collapsed, Debug open, never at Normal. Details received live are kept in the client store whatever the current level, so raising the level later shows them (they are not discarded at Normal).
- **Width**: at 320 CSS px and 400 % zoom the text wraps and the action stays visible and operable (target ≥ 24 × 24 CSS px).

### 11.2 Carrier → line mapping (D79)

Existing typed frames and transcript entries stay; each maps through **one adapter module** (one table, unit-tested row by row) into `SystemLine` props. Only lines with no carrier today use `SystemLineFrame` (§7.5).

| Carrier (today) | Renderer today | Line type | In progress | Min level | Action |
|---|---|---|---|---|---|
| `ErrorFrame` / `ReplayErrorFrame` with `LLMError` (turn ended in failure) | inline error + `ErrorDetailDisclosure` | error (`turn_canceled` → info "You stopped this reply.") | no | normal | by code: Switch model (`provider_rejected`, `rate_limited`, `model_retired`, `bad_model`, `media_unsupported`, `request_too_large`, `context_too_long`, `content_policy`, `network`, `provider_error`, `unknown`); Open provider settings (`provider_auth_failed`, `quota_billing`, `needs_provider`, `model_route_unavailable`, `context_window_unknown`); Open settings (`judge_time_limit`); Open task (`delegated_task_limit`); none otherwise (unchanged from round 0) |
| `ProviderRetryFrame` | `ProviderRetryIndicator` | warning: the frame's `text` + the one fixed retry clause (§7.4) | yes (countdown + attempt counter, filled from the frame's typed facts) | every level while the turn waits | — |
| `ProviderFallbackFrame` / persisted fallback note | `ProviderFallbackNoteLine` | info: `ProviderFallbackFrame.text` live, the persisted `message` after reload (the same bytes), quoting why the first model failed (`provider_says`) | no | normal while the turn runs; **verbose after the reply ends** (D75, L8 literal) | Pick a new model when `model_retired` |
| `RateLimitFrame` (Omnipus's own limiter, SEC-26) | `RateLimitIndicator` | warning | yes until `retry_after` | normal | — |
| `ContextWindowNoticeFrame` | system pill | info | yes during compaction | normal while happening; verbose after | — |
| `CancelStageFrame` | stop-button state | info | yes | normal | — |
| Goal outcome entry (`system_subtype: goal_outcome`) | `GoalOutcomeRow` | resolved (`met`) / warning (`rounds_exhausted`, `other`) / info (`stopped_by_user`) | no | normal (L2) | — |
| Helper event entry (`helper_event`) | `HelperEventRow` | info / warning by event | per event | normal | — |
| Browser handover notice, goal acknowledgement, clear-context divider | system pill | info | no | normal | — |
| Delegation line | `DelegationEventLine` | info | yes while running | normal (L7 summary) | — |
| Restart-interrupted notice | `RestartInterruptedNotice` | warning | no | normal | Retry reply |
| Unavailable linked chat | `UnavailableChatNotice` | error | no | normal | Retry |
| Browser↔server loss (client-only) | `ChatConnectionStatusLine` | warning ("Can't reach the server right now. This page reconnects on its own when it's back." / offline form, §11.3) → resolved ("Up to date", then removed) | yes | normal; never persisted (R-SL-6, D84) | Try again |
| **New** network/API retry outcome record | — (`SystemLineFrame`) | resolved | no | **verbose** (R-SL-5 record line) | — |
| **New** Stop outcome (D68; `publishStopNotice` today feeds the centre) | — (`SystemLineFrame`) | warning | no | normal | — |
| **New** `system_overload` (w4-087, if a producer exists — §10.2) | toast today | warning | no | normal | — |
| **New** "working on …" step line | `AgentStatusIndicator` | info | yes | normal (L4) | — |

### 11.3 Connection lines (R-SL-5, R-SL-6; D10, D18, D42, D64, D84)

- **Omnipus↔provider problem while a reply waits**: in-progress Warning at every level, driven by `ProviderRetryFrame` with network or API class. Every retry line carries the observed cause (as soon as observed), the countdown and the attempt counter (§13.2). On **this turn's own** successful attempt after at least one failed one: at Normal the line disappears; the turn's execution appends one `SystemLine` record (`line_type: resolved`, `min_level: verbose`) "Connection to {provider} restored after {n} retries ({seconds} s)." with **its own** retry count and waiting time. On final failure: the terminal Error line (L3, N2 final form naming host, cause and one fix) replaces it at every level. **Only the turn that waited resolves its line (round-2 R2-MAJ-010):** a provider-wide recovery (§14.3) closes the centre incident but never resolves, rewrites or records another chat's turn — a turn still retrying keeps its warning line, and a turn that then runs out of attempts gets its final Error line and no record line.
- **Browser↔server loss**: one app-wide indicator (the `AppShell` banner area, shown in every reconnecting phase, not only on terminal errors) plus the chat line; both client-only, neither enters history; the outage is reported to the server when back (§14.4). The browser cannot tell a dropped connection from a stopped gateway, so neither surface claims what the server or the agents are doing (round-2 R2-MAJ-013; founder answer W1 = A → D84): server unreachable → "Can't reach the server right now. This page reconnects on its own when it's back." on both the banner and the chat line; offline → the offline form (VOICE samples §18). After reconnect, "Up to date" shows briefly, the chat refetches history, and the user sees what happened. If only the connection dropped, the user then sees what the agents did meanwhile; if the gateway restarted, the existing restart behaviour applies (`docs/using-omnipus-ui.md`, "If the gateway restarts during a reply": a restart ends an in-flight answer and the chat shows **Interrupted**). The baseline sentence "Your agents keep working." (WORDING FE-076, VOICE samples §17) is not used on either surface (§13.1, VS-5, D84).

### 11.4 Sentence composition (round-1 MAJ-008; round-2 R2-MAJ-006, R2-MAJ-007, R2-MAJ-011)

The server assembles the finished sentence; the browser never composes wording from facts, with one named exception (the retry clause, item 3).
1. At the emit site, **one assembly function** in `pkg/agent` (the extended `TranslateLLMError` path, used by the error, retry and fallback emitters alike, and by the separate stalled path in `TranslateTurnError`) picks the sentence variant by the facts it actually has: *no facts* · *provider/model known, no provider message* · *provider/model known and `provider_says` present* · for `network`/`provider_error`/`provider_stalled` additionally *retrying* vs *final* (§13.2).
2. It fills every slot from the facts at that site (§13.7) and never renders an empty slot or empty quotes.
3. Where the finished text goes: the error's `LLMError.message`; the retry frame's `text` — the cause sentence only; the browser appends the one fixed retry clause filled from `retry_at` / `sent_at` / `attempt` / `max_attempts` (§7.4); the fallback frame's `text`, byte-identical to the note persisted for that turn and replayed as `ProviderFallbackNote.message`.
4. **Trust (R2-MAJ-007).** The assembly function — and only it — sets `message_assembled: true`, whenever it rendered a catalogue variant with every slot filled from emit-site facts, for **any** code, provider-related or not (for example `judge_conclusion_failed` with its reason, `workspace_unavailable` with its path). `src/lib/llm-error.ts::getLLMErrorDisplay` shows `message` when `message_assembled` is true and the text is non-empty, and the code's catalogue copy otherwise; its `judge_conclusion_failed` exclusion is removed, and its `delegated_task_limit` exception (controller-authored text) stays as it is. Text that did not pass through the assembly — the own-limiter's internal `rate_limited` row, raw provider text, an untranslated error — never carries the flag and is never shown as the main line. This replaces today's server condition, under which `WireLLMError` sets the flag only when `providerMessageFor` finds a provider-message template, which needs a provider identity (`pkg/agent/translate_error_provider_messages.go`).
5. **Persistence.** `message`, `message_assembled`, `provider_says`, `failure_class` and `facts.provider` / `facts.model` / `facts.host` are persisted; `detail`, `facts.http_status` and `facts.request_id` stay live-only (C14 corrected, §3). The fallback note is saved once per **(turn, unavailable model, answering model)** (R2-MAJ-011): the check inside one reply stays, but `sessionTranscriptHasFallbackNote` (`pkg/agent/fallback_note.go`), which today matches the model pair anywhere in the session, also matches the turn id (`queueProviderFallbackNote` already records `TurnID` on the entry) — so a later reply with the same pair and a different cause saves its own note and quotes its own cause. Replay therefore renders the same main line without re-composing.

---

## 12. Detail levels (R-DL-1…3; D8, D16, D25, D41, D43, D66, D75)

### 12.1 Setting

- **Settings → Chat → Detail level**: a `segmented-control` **Normal · Verbose · Debug** replacing the Verbose-chat switch in `src/components/settings/ChatSection.tsx`. Per device, like today's switch (§3.2 N1). Default Normal. Every user may pick Debug (R-DL-3). Greenfield: the stored boolean is not migrated (an old `verboseChatEnabled: true` reads as Normal).
- One rule function in `src/lib/` decides visibility for every row kind and level; every current consumer of `verboseChatEnabled` (27 at round 0, re-counted by the implementer before editing) calls it and nothing else (R-DL-1, D8). A test enumerates every row kind × level against the table below. Changing the level re-renders open chats immediately, without a reload, and without losing details received earlier.

### 12.2 Normative table (REQ §5.6 rows; L6 and L8 literal per D75)

| # | What | Normal | Verbose | Debug | Implementation mapping |
|---|---|---|---|---|---|
| L1 | Your messages and the agent's replies | Shown | Shown | Shown | message rows |
| L2 | Task and goal results; "agent needs you" requests | Shown | Shown | Shown | goal outcome, helper results, approval and ask cards |
| L3 | A failure that stopped or changed a reply (our sentence + provider's message) | Shown | Shown + Technical details incl. masked payload (collapsed) | Shown + Technical details open | Error `SystemLine`; details = masked `LLMError.detail` (live) and `http_status`, `request_id` |
| L4 | One "working…" line naming the current step | Shown | Shown | Shown | `AgentStatusIndicator` as info in-progress line |
| L5 | Tool calls with visible output (file, web page, image) | Shown | Shown | Shown | cards of `FileReadPreview`, `FileWriteConfirm`, `FileTreeView`, `WebFetchPreview`, `WebServeUI`, `BrowserTool`/`BrowserNavigate` (image), `DraftLink`, `WebSearchResult` |
| L6 | Every other tool call | Compact line (name only) — **every** such call, including calls hidden at Normal today (D75 a) | Line + result | Line + result + raw | `GenericToolCall`, `BashOutput`, `ToolCallBadge` |
| L7 | Delegation — which agent was asked what | Summary only | Every step | Every step | `DelegationEventLine` vs `delegate` badges |
| L8 | Automatic retries, backup-model switch, connection problems | While happening only — the fallback note too hides at Normal once the reply ends (D75 b) | Shown, record line stays | Shown, record line stays | §11.3; fallback note |
| L9 | Technical details on a system line (status code, request ID) | — | Collapsed link | Open by default | `SystemLine` details disclosure |
| L10 | Timings, token counts, cost per reply | — | Shown | Shown | `DoneStats` (`tokens`, `cost`, `duration_ms`) |
| L11 | Internal IDs and full failure details | — | — | Shown | what exists today (D66): session, turn, tool-call, approval and request ids already on the wire, and the full masked failure detail. **Raw request/response of successful provider calls is not captured in Part 1** — tracked issue #1546 (proposed target 2026-12-15; §30, N4) |

Rules: technical details only at Verbose and Debug (R-DL-2, D25); the masked failure payload is the only raw payload shown (D43, D66); secrets masked at every level (R-SEC-1, D16); the connection-problem line shows at every level while it lasts (R-SL-5).

---

## 13. Wording (R-WD-1…9; D26, D28, D35, D36, D47–D50, D64, D65, D84)

### 13.1 Rules — the voice skill is the authority (D65)

Every new or surviving system message in this feature is written with the **`omnipus-voice` skill** (`.claude/skills/omnipus-voice/SKILL.md`) and checked against its "Self-check before committing a message" list before it is committed. The founder reviews the pattern (the skill), not each message (D65). This spec does not restate the skill's rules; it fixes only what the code must enforce:

| Enforced here | Source |
|---|---|
| In a chat the agent speaks as "I"; "Omnipus", "we", "our", "us" never act; the only product-name use is the D48 bug-report sentence | skill hard rule 1; R-WD-5, R-WD-7 |
| Outside a chat (centre, pop-ups, banners, settings) neutral, no first person; a message used in both places has two forms | skill hard rule 1; R-WD-6, R-WD-8 |
| The provider's own message is quoted on the main line when received; never an empty slot or empty quotes | skill hard rules 3, 12; R-WD-2, D28 |
| Transient and connection failures follow **the N2 pattern** (skill section "The N2 pattern — transient and connection failures"): cause named as soon as observed, countdown, attempt counter on every retry line; final line names host, observed cause and one Fix — `network`, `provider_error` and `provider_stalled` alike; never claim the internet is down | D64 |
| No status codes, request ids or paths on the Normal main line; they belong in technical details | skill hard rule 6 (VS-2) |
| No `!`, no emoji, no "sorry", no "oops", and no "please" except inside the D48 sentence, in any system message | skill hard rules 9, 11 (VS-1) |

Differences between the skill, its examples and the agreed baseline are resolved in the table below (round-2 R2-MIN-002); none is resolved silently. The rule: **the skill's hard rule wins, and the conflicting example or baseline entry changes** — no exception is invented and no self-check receipt may certify a message that fails the current skill text.

| VS | Conflict | Resolution | Who changes what |
|---|---|---|---|
| VS-1 | "please": WORDING BE-008 "…Please restate it." and `schema` "…please report it." vs hard rule 9 (only the D48 sentence may say "please") | BE-008 becomes "The goal could not be compiled. Restate it."; `schema` ends with the D48 sentence "Retry; if it repeats, please report it to the Omnipus.ai team." The guard checks "please" (§13.4) | implementing leads, through the manifest (§13.5) |
| VS-2 | status codes on the Normal main line: hard rule 4's example `(503)` and SAMPLES §3 vs hard rule 6 | hard rule 6 wins: API lines carry no status code at Normal (§13.2); the status stays in technical details at Verbose/Debug (`facts.http_status`) | voice owner updates rule 4's example and SAMPLES §3 |
| VS-3 | the skill's slot list lacks `{host}` and `{fix}` | both added to "Naming things", `{fix}` with its closed values (§13.2) | voice owner |
| VS-4 | SAMPLES §2 and §14 show pre-N2 final lines (no host, no `Fix:`) | updated to the N2 final lines of §13.2 (network final; stalled final) | voice owner |
| VS-5 | SAMPLES §17 and the skill's banner row keep "Your agents keep working" | dropped from the banner and the chat line (D84, §11.3). Both read "Can't reach the server right now. This page reconnects on its own when it's back." After reconnecting, the user sees what happened | voice owner |
| VS-6 | sentence length, sentence case and units are self-check items, not guard checks | unchanged (round-2 review §8, point 6); each manifest receipt names the manual check | — |

The voice owner (`prometheus-prompt-engineer`, dispatched by team-lead) lands VS-2…VS-5 in `.claude/skills/omnipus-voice/` before WP-6 starts collecting self-check receipts. This spec does not edit the skill.

### 13.2 Catalogue (ADR-051 codes) and the N2 lines

- `contracts/components/schemas/LLMError.yaml` `x-user-messages` takes the WORDING §1 final sentences for all 29 codes, plus `provider_error` (new, from the `network` split), with the N2 refinements below. Each entry carries explicit variants — no clause logic in code: `message` (no facts), `provider_message` (provider/model known, no provider message), `provider_message_quoted` (`provider_says` present), and for `network` / `provider_error` the `retrying` and `final` forms.
- The generators (`scripts/gen-asyncapi-go`, `scripts/_gen-asyncapi-types.mjs`) validate slots against the closed vocabulary of the skill ("Naming things": `{provider}` `{model}` `{provider_says}` `{reason}` `{status}` `{n}` `{seconds}` `{minutes}` `{tool}` `{agent}` `{path}` `{model_requested}` `{model_used}` `{limit_name}` `{value}` `{task}` `{attempt}` `{max}` `{countdown}` `{answered_model}` `{unavailable_model}`) **plus `{host}` and `{fix}`** (both new, required by N2's final lines; the skill's list is synchronised, VS-3). `{fix}` is a **closed slot**: its only values are the Fix sentences listed below this table. A template naming a slot outside the vocabulary, or a `{fix}` value outside that list, fails generation — a negative fixture in T-14 proves it (round-2 R2-MIN-001).
- `x-user-message-attributions` stays.
- The copy-rule tests (`pkg/agent/translate_error_test.go`, `src/lib/llm-error.test.ts`) change narrowly (MIN-004): the rule "no `product` or `config` message tells the user to switch models" is kept as a ban on **unqualified** model-shopping — a qualified remedy ("switch to a model with a larger limit", "switch to a model that reads it") stays allowed, as in the baseline; "no message tells the user to contact support" is relaxed by exactly the D48 sentence.

N2 lines (D64; replaces the round-0 network split table). Retry rows show the server's `text` followed by the one fixed retry clause `Retrying in {countdown} (attempt {attempt} of {max})…` (§7.4); no server text contains a countdown (round-2 R2-MAJ-006). No line carries a status code at Normal (VS-2).

| Situation | In chat (server `text`; + retry clause on retry rows) | Outside chat (centre / pop-up title) |
|---|---|---|
| network, retrying, cause observed | `I can't reach {provider}: {reason}.` + retry clause | `{provider} can't be reached: {reason}` |
| network or API, retrying, no cause known yet | `I can't get a reply from {provider} yet.` + retry clause | `{provider} isn't answering` |
| network, final | `I couldn't reach {provider} ({host}): {reason}. Fix: {fix}` | `{provider} can't be reached: {reason}` |
| API, retrying | `{provider} answered with an error: "{provider_says}".` + retry clause (no-quote form: `{provider} answered with an error.`) | `{provider} returned an error` |
| API, final | `{provider} ({host}) answered with an error: "{provider_says}". Fix: {fix}` (no-quote form drops the quote) | `{provider} returned an error` |
| stalled (`provider_stalled`), final | `{provider} ({host}) stopped sending data for {minutes} min, so I ended the call. Fix: {fix}` (no-host form drops `({host})`) | `{provider} can't be reached: it did not answer in time` (network class, §14.1) |
| several providers failing (centre only) | — (each affected chat shows its own provider line) | title `Connections to AI providers are failing`; body lists each `{provider}: {reason}` from `causes`, then `and {n} more` when `causes_omitted` is above 0 |

- `{reason}` words (fixed; aligned with the skill's examples): `dns_failure` → "its address could not be found"; `connection_refused` → "connection refused"; `network_unreachable` → "the network is unreachable"; `tls_failure` → "the secure connection failed"; `timed_out` → "it did not answer in time"; `connection_dropped` → "the connection dropped". `http_status`, `invalid_response` and `unknown` never fill `{reason}` (they use the API or no-cause forms).
- `{fix}` values (closed list): network `dns_failure` → "check your network or the provider's address in Settings → Providers."; network `tls_failure` → "check your proxy or certificate settings."; every other network reason → "check your network or proxy."; API final → "retry in a moment; if it repeats, switch model."; stalled final → "retry; if it happens again, switch model."
- The countdown is rendered only by the retry clause, from `retry_at` and `sent_at` (whole seconds, "8 s"); the clause ticks with the existing indicator, and the server sentence never goes stale.

### 13.3 Provider message extraction (`provider_says`)

From the failing attempt's response: JSON body → first non-empty string at `error.message`, `error.error.message`, `message`, `error` (when a string), `detail`, `error_description`; otherwise a non-JSON body's first line; otherwise a stream error event's message.

**Order: mask the whole response first, then select (D81; founder answer NS1 = A to round-2 R2-MAJ-001).** The masker (§15.1, M1–M6) runs over the **whole** response — the body as captured (`ProviderError.Body`; at most 8 KiB on the shared HTTP helpers, `pkg/providers/common/common.go::handleErrorBodyCap`), or the whole stream error event message — **before** any of the selections above; extraction then reads only the masked text. Reason: selecting a non-JSON body's first line first can cut a registered credential value in two, so the second half is gone before M2 can match the whole value and the first half stays visible. A replacement (`[redacted]`) inside a JSON string keeps the body valid JSON; if masking turns a valid JSON body into invalid JSON (a match spanning JSON syntax), the body is handled as non-JSON — the first line of the **masked** text, never of the unmasked body. Tested through the real extraction-to-display path by T-51 (DS-SEC row 15) and at unit level by T-12 (DS-EXTRACT row 17).

After selection (unchanged): mask (§15.1), strip control characters, collapse whitespace, **mask again** (catches values joined by the normalisation), cap 2,000 bytes on a UTF-8 boundary (never splitting a code point). The main line shows ≤ 300 code points + "…". Never the whole raw body. Valid JSON with no eligible field and an empty body both → absent (the no-quote sentence is used).

### 13.4 Voice guard (round-1 MAJ-018)

New CI guard `scripts/check-system-message-voice.sh` (picked up by `scripts/guards.sh`). It scans the sources listed in the wording manifest (§13.5) — the `LLMError.yaml` catalogue, the server-side system-line text table, the centre title/body table, and every frontend string module the manifest names — with these checks:

| Check | Applies to | Rule |
|---|---|---|
| Product as actor | in-chat and outside | `\bOmnipus\b` fails except inside the exact D48 sentence |
| First person plural | in-chat | `\b(we|our|us)\b` (case-insensitive) fails |
| First person outside a chat | outside | `\b(I|my|me|we|our|us)\b` fails |
| Tone | all | `!`, any emoji code point, `\bsorry\b`, `\boops\b` fail |
| "please" | all | `\bplease\b` (case-insensitive) fails except inside the exact D48 sentence (VS-1) |

Sentence length, sentence case and the unit format are self-check items of the skill, not guard checks (VS-6). Self-check of the guard: one planted fixture per check and per surface (in-chat "we", outside "I", a "!", an emoji, "sorry", a "please" outside the D48 sentence); each must make the script fail, and a clean fixture must pass. The guard scans the manifest's sources; that list is itself checked by the independent inventory of §13.5.

### 13.5 Wording manifest (R-WD-4, R-WD-9; D65; round-1 MAJ-018)

A manifest file `docs/internal/specs/notifications-wording-manifest.json` (created in WP-6) lists **every** message id from the founder review data (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/revised/revised.json`, 392 ids: 29 ADR-051 codes + 363 others) with: id, surface (in-chat / outside), source file and symbol, disposition, final text, and the self-check receipt (who checked it with the skill).

| Disposition | Count (verified at round 1) | Treatment |
|---|---|---|
| ADR-051 codes | 29 | WORDING §1 baseline + N2 refinements (§13.2) |
| Changed by the voice rule or a founder edit (WORDING §2) | 38 (35 voice rule + 3 voice rule + edit) | baseline text |
| Approved proposals absent from the baseline | 148 (129 agreed by default + 19 rated) | the approved proposal text, checked with the skill |
| Kept as today | 14 | unchanged; checked with the skill |
| No proposal yet | 163 | **rewritten by the implementing leads with the skill** once the WP-5 clean-up list is fixed; no founder rating page (D65) |
| Surface removed by this feature (pop-ups removed by WP-5, the deleted `ToolApprovalModal`) | subset of the above, recorded per id | dropped, not rewritten |

A unit test fails if any manifest id has no disposition, if a surviving id has no final text or no self-check receipt, or if a scanned source contains a system-message string whose id is not in the manifest. The two VS-1 baseline changes are recorded as dispositions "changed by the voice rule (round-2 sync)". FE-076 (the browser↔server banner and the matching chat line) is disposition "changed by the voice rule (VS-5, D84)": its final text, on both surfaces, is the unreachable sentence in §11.3; the baseline reassurance sentence is not the final text.

**Independent inventory (round-2 R2-MIN-004).** The manifest's own source list is not trusted on its own. T-47 builds an inventory of every call site of the message-emitting interfaces — the router's `Notify` title and body, the `SystemLine` constructors and the carrier adapter table, the `LLMError` catalogue, the pop-up host, and the banner and page-indicator components — across `pkg/`, `cmd/`, `internal/` and `src/`, with the same whole-tree call-site method as the own-action audit of §16, and fails when a call site's file is missing from the manifest's source list. Its instrument check plants one bad string in a source that is **not** listed in the manifest and must fail; ordinary prose in unrelated files is not scanned.

### 13.6 Neutral centre titles (new texts, written with the skill)

| Kind | Title | Body |
|---|---|---|
| `approval_requested` | {agent} is waiting for your approval | {tool}: {short summary} in {chat} |
| `question_asked` | {agent} asked you a question | in {chat} |
| `goal_paused` | Goal paused in {chat} | {reason sentence, neutral} |
| `task_started` / `task_finished` / `task_failed` | Task "{task}" started / finished / failed | failed: {cause sentence, neutral} |
| `schedule_finished` / `schedule_failed` | Scheduled task "{name}" finished / failed | failed: {cause sentence, neutral, masked}; never the raw run error |
| `goal_reached` / `goal_given_up` | Goal reached / Goal given up in {chat} | {goal text, ≤ 160 chars} |
| `provider_unreachable` | {provider} can't be reached: {reason} | {affected_chat_count} chats waiting |
| `provider_error` | {provider} returned an error | "{provider_says}" |
| `provider_quota_exhausted` | {provider}: account out of credit | "{provider_says}" |
| `provider_rate_limited` | {provider} is limiting requests | {affected_chat_count} chats waiting |
| `provider_connections_failing` | Connections to AI providers are failing | one line per listed cause: {provider}: {reason}; then "and {n} more" when `causes_omitted` is above 0 |
| `connection_restored` | {provider} reachable again / Connections to AI providers restored | after {duration} |
| `resources_low` | Memory is running low / Disk space is running low | memory: Fewer agents run at once until it recovers · disk: Saving files fails until space is freed |
| `concurrency_reduced` | Fewer agents run at once | Limited to {n} because memory is low |

### 13.7 Per-code facts and where they come from (round-1 MAJ-008)

Slot values are taken at the emit site and baked into the finished sentence; only the facts in the "Wire" column travel as fields.

| Code(s) | Slots beyond provider/model/provider_says | Fact source at the emit site | Wire / persisted |
|---|---|---|---|
| `network` | `{reason}`, `{host}`, `{fix}`; retry clause `{attempt}`, `{max}` | per-attempt observation (§14.2): reason, host of the failing candidate's endpoint; retry policy for attempt/max; the countdown from the retry frame's `retry_at` | `failure_class`, `facts.host`, retry frame `reason`, `attempt`, `max_attempts` — persisted on the error entry |
| `provider_error` | `{host}`, `{fix}`; retry clause `{attempt}`, `{max}` | observation: host; the HTTP status goes to technical details only (VS-2) | `failure_class`, `facts.host` persisted; `facts.http_status` live-only |
| `provider_stalled` | `{minutes}`, `{host}`, `{fix}` (N2 final pattern, round-2 R2-MAJ-013) | the provider's silence limit in effect; host of the stalled candidate's endpoint. The separate stalled path in `pkg/agent/translate_error.go::TranslateTurnError` uses the same catalogue variants, with the no-host form when the host is unknown | `failure_class`, `facts.host` persisted |
| `turn_timed_out`, `judge_time_limit` | `{seconds}` | the turn deadline / the Judge time limit in effect | in `message` only |
| `tool_args`, `tool_call_truncated` | `{tool}`, `{reason}` | the failing tool call's name and validation reason | in `message` only |
| `workspace_unavailable` | `{path}`, `{reason}`; outside form `{agent}` | the agent's working folder and the OS error text (masked) | in `message` only |
| `judge_conclusion_failed`, `model_route_unavailable` | `{reason}` | the save/delivery error, the provider set-up error (masked) | in `message` only |
| `delegated_task_limit` | `{task}`, `{limit_name}`, `{value}` | the delegated task's title and the limit that stopped it | in `message` only · **(#1857 amendment, R-6; amended by founder E6, 2026-11-18): reserved** — no producer exists at release head `5d20e539f` (§13.8.2 B9); **no issue is filed** for it (the earlier filing promise is withdrawn) and the manifest lists it as reserved until an emitter exists |
| `model_unavailable` | `{model_requested}`, `{model_used}` | model selection of the turn | in `message` only |
| `agent_not_configured`, `model_unassigned` | `{agent}` | agent display name | in `message` only |
| every other code | provider/model/provider_says only | failing attempt | `facts.provider`, `facts.model` persisted; `provider_says` persisted |

When a slot's fact is unknown at the emit site, the variant without that slot is used (§11.4). Codes that are non-terminal notices today (e.g. `model_unavailable` reporting a substitution while the reply continues) stay non-terminal: only their wording changes. The exact emit symbol for each code is pinned by the WP-1 RED test for that code (T-45).

### 13.8 Turn-failure kinds and wording (#1857 amendment — founder exposure answers applied 2026-11-18; wording W1–W16 still proposed, E4 copy review pending)

Issue #1857 (child of the notifications Epic #1604, founder decision 2026-11-15): when a turn fails, the user too often sees only the generic `unknown` line — "This turn didn't finish, and we can't tell why. Retry — …" — even when the server knows the reason. The requirement is **FR-092** (§21): no turn failure ends in the generic line when a safe reason is known; the generic line remains only for a genuinely unknown reason. The sanitizer stays **fail-closed** (ADR-051, §13.3, §15.1): no raw provider or CLI text, no secrets, no file paths and no command lines in the user message; masking runs exactly as FR-070/D81 define it.

This subsection is the amendment's deliverable set: the failure-kind list (§13.8.2, swept from release head `5d20e539f` and corrected by the architect's read — items F-1…F-15, extended 2026-11-18 by the founder's sec2 answer with B25/B26), the per-surface rules (§13.8.4), the proposed wording (§13.8.3, written with the `omnipus-voice` skill), the decisions (§30.4 — the founder's answers E1/E2/E4 + chief-ruled E3 applied 2026-11-18) and decision row **D85** (§32). The Mac team's error-case comment on #1857 (posted 2026-11-15, saved with this amendment) is folded in: its first case (a duplicated `--model` flag passed via the worker profile's **Additional CLI arguments** setting) is row C1 and wording row W1; its second case (the CLI's model unavailable, nested `error.type` `provider.no-route`) is row C2 and also surfaces in Settings → "Send a test message" (row B18). Status: the exposure decisions are made and applied (§30.4); the wording table stays **proposed** until the founder's E4 copy review (§13.8.3 status note); nothing here is implemented.

#### 13.8.1 Where the generic line comes from today

- The catalogue copy: `unknown` in `contracts/components/schemas/LLMError.yaml` `x-user-messages` (generated into `src/lib/api/generated/llm-error-messages.ts` and `pkg/api/generated/llm_error_messages.gen.go`), returned by `pkg/agent/translate_error.go::defaultUserMessage` for `CodeUnknown`. A transcript entry classified `unknown` replays the same line (the trusted-stage guard in `pkg/agent/turn_transcript.go` exists so a *typed* copy survives reload; an `unknown` one never had one).
- **What actually renders is decided by the SPA trust rule (F-1 — the draft's central correction) — for typed `llm_error` frames.** `src/lib/llm-error.ts::getLLMErrorDisplay` shows a frame's `message` only when `provider_message === true`; `pkg/agent/translate_error_provider_messages.go::WireLLMError` sets that flag only when the code has a provider-message template — and `unknown` has none. Consequence: **every Omnipus-written sentence that travels on code `unknown` in a typed frame renders as the generic catalogue line, whatever the server attached to `message`.** That is why B5, B6, C8 and the M-kind rows below are generic today even though their emitters attach curated text: the curated text is dead in the SPA. The draft's first version called several of them "own text shown as written"; the architect's read (F-1) corrected this. **This mechanism does not generalize to every user surface:** the gateway's late save notice (`pkg/gateway/websocket_streamer.go::publishSaveFailureNotice`) emits the fixed `agent.MessageNotSavedReply` text in an error frame **without** a typed `llm_error` payload, and authored replies (the B8 panic line, B4's tool-limit notice) reach the user as reply text — those surfaces show their own text today and are handled as their own rows, not through this bullet.
- The classifier's residual verdict: `pkg/agent/translate_error.go::classifyByMessage` (reached for every error with no HTTP status — including the whole external CLI worker path, which builds a synthetic `ProviderError{Status: 0}` in `pkg/agent/external_dispatch.go::SanitizeRunnerError`) matches only capability/media shapes, content policy, context overflow, tool-args/schema, rate-limit wording and transport wording (`providers.IsTransportFailure` — which recognises e.g. `unexpected eof` and `broken pipe`, so *some* stream-read causes already classify `network` today; row C6). CLI flag-parse, CLI model-unavailable, CLI sign-in and bare exit-status wording match none of them → `CodeUnknown` → the generic line. That is the exact mechanism the OpenCode hands-on found (#1857).
- **CLI classification becomes its own function (R-2, architect-decided).** The CLI path never shares `classifyByMessage`: a separate CLI-only classifier maps driver-marked structural kinds and structured error fields to the six `external_cli_*` codes; `classifyByMessage` stays byte-identical (its provider-side matchers must not grow CLI wording, or provider text could start matching CLI kinds and the reverse). Drivers mark the structural kind (flag rejection, turn cap, run timeout) on a non-wire field at parse time; OpenCode's nested shape (`{"type":"error","error":{"type":"provider.no-route",…}}`) is classified from the structured `error.type` field, not by message matching — at release head the nested field is lost (row C2). The CLI path never yields a provider or network code; its only pass-through typed exits are `turn_canceled`/`turn_timed_out` (row C10). **Classification alone does not declare run termination (MAJ-008):** fatality is an independent fact the drivers already carry (`pkg/agent/runner/driver_opencode.go::parseLine` emits a malformed input line as an error event with `Fatal: false`) and `pkg/agent/external_dispatch.go::drainExternalRun` emits every error event *before* testing `ev.Err.Fatal` — only the fatal branch sets the run error. A nonfatal diagnostic therefore gets no terminal-failure sentence (row C11) — and the **baseline correction (grill round 2, R2-MAJ-005)**: today such a diagnostic is *not* display-free either. `drainExternalRun` sanitizes, appends and **emits the error event before** testing `ev.Err.Fatal`, so a run that goes on to succeed still leaves an emitted and persisted error line in the chat record; this amendment removes those user-facing diagnostic emissions on the successful normal-run path (the classified kind stays in gateway.log). The Settings drain's separate stricter behaviour (any error event fails the test) is a different consumer with a different outcome — pinned in row C11's disposition, not silently inherited.
- A second, older generic line: the panic-recovery sentence `"Error processing message: the agent turn failed unexpectedly. Please try again."` (`pkg/agent/session_worker.go::processTurn`, `pkg/agent/loop_unroutable.go::dispatchUnroutableMessage`, and the third site `pkg/agent/revive_support.go::runRevivedOrdinaryTurn` added by the architect read) — not the `unknown` catalogue copy, but the same "no reason" shape, and it breaks voice rule 9 ("please"). Listed below as row B8.
- The release catalogue has **31** codes (the spec's round-0 count of 29 grew by `provider_empty_reply` and `model_route_unavailable` before landing); every row below names the code a kind maps to **today** in that catalogue. `provider_error` is **not** among them — it is introduced by this amendment's WP-0.

#### 13.8.2 Failure-kind list (swept at release head `5d20e539f`)

Certainty per row: **Verified** = read in this amendment's sweep; **Inferred** = reasoned from read code without executing it. "Generic today?" = the user-visible chat line is the `unknown` copy.

**A. Provider-attempt kinds (the control group — already typed)**

| # | Failure kind | Comes from (`file::symbol`) | Code today | Generic today? | Safe facts known (no raw text) | Proposed |
|---|---|---|---|---|---|---|
| A1 | Provider error with a pinned body shape (401/403 auth, 402/insufficient-quota billing, 413, 404 retirement phrases, media shapes, content policy, context overflow, tool-args/schema, 429) | `pkg/agent/translate_error.go::classifyByHTTPStatus` | own codes | No | provider, model, `provider_says` (masked, §13.3), status (Verbose only, VS-2) | unchanged — the control that already works |
| A2 | Transport failure of a dispatched attempt (DNS, refused, unreachable, TLS, timeout, dropped connection) | `pkg/agent/translate_error.go::isNetworkMessage` + `pkg/providers.IsTransportFailure`; classification table §14.1 | `network` | No | provider, model, host, `reason` | unchanged — N2 lines (§13.2) |
| A3 | Provider error event inside a 200 stream | `pkg/agent/translate_error.go::TranslateTurnError` (`StreamProviderError` branch) | per-kind (`network`/`rate_limited`/`quota_billing`/`provider_auth_failed`/`provider_rejected`/`content_policy`) | No | provider, stream error kind | unchanged |
| A4 | Residual dispatched-provider failure with no pinned shape — **narrowed per R-4**: only when an HTTP status ≥ 400 was received and its body matched nothing, or a 200 stream ended with an unparseable body. A pre-send failure and an attempt with no status are **not** promoted — they stay `unknown` | `pkg/agent/translate_error.go::classifyByHTTPStatus` (residual → `unknown`); `::TranslateTurnError` promotes only a residual **400** to `provider_request_rejected` | `unknown` (400 → `provider_request_rejected`) | **Yes, conditionally** | provider, model, status (when one arrived); the provider's own sentence quoted when `provider_says` is present (R-4) | at the **terminal presentation only**, name the promoted residual `provider_error` — no-quote API form (§13.2), quoted form when `provider_says` is present — never inside `classifyByProviderError`. `provider_error` is **introduced by WP-0**, not a release-head code. Two gates are pinned, not one: `pkg/agent/media_downgrade.go::outcomeFallbackEligible` **and** `pkg/agent/turn_transcript.go::outcomeRelabelApplies` both keep reading `CodeUnknown`, or the FR-017 media strip-retry relabel breaks (F-4). **OQ-1857-4 — architect-decided (R-4)** |
| A5 | Fallback chain exhausted, last attempt unclassifiable | `pkg/agent/translate_error.go::errorToProviderError` (reverse walk) → same residual verdict | `unknown` | **Yes, conditionally** (same shape and same R-4 narrowing as A4) | provider, model of the last attempt | same as A4 |
| A6 | Wait budget exhausted (D14, 10 min/turn) | `pkg/providers/fallback_retry.go::WaitBudget` (`MaxWaitBudgetPerTurn`), checked before each wait | none — retries stop; the turn ends on the **last attempt's** error | inherits A4/A5 | budget fact (waited, not failed anew) | no own code; the final line is the last attempt's classified line. Stated here so the list is complete (Inferred: no separate budget error exists — verified by reading `fallback_retry.go`) |
| A7 | Stall, empty stream, request timeout, cancellation | `common.ErrStreamStalled`/`ErrEmptyStream` → `provider_stalled`/`provider_empty_reply`; `typedExitCode` → `turn_timed_out`/`turn_canceled` | own codes | No | stall minutes, timeout | unchanged |

**B. Pre-turn and loop kinds**

| # | Failure kind | Comes from (`file::symbol`) | Code today | Generic today? | Safe facts known | Proposed |
|---|---|---|---|---|---|---|
| B1 | Pre-turn refusals (no provider, no model, unknown context window, agent on no workspace, working folder unopenable, Judge time limit, Judge conclusion not saved) | `pkg/agent/translate_error.go::TranslateTurnError` sentinel branches (`ErrAgentNeedsProvider`, `ErrAgentModelUnassigned`, `ErrContextWindowUnknown`, `ErrAgentNotWorkspaceMember`, `ErrWorkspaceWorkDirUnavailable`, `ErrJudgeTimeLimit`, `CodeJudgeConclusionFailed` in `pkg/agent/goal_child_completion.go`) | own codes | No | agent name, path (masked), limit | unchanged |
| B2 | Model switch failure (non-terminal) and unresolved explicit pair (terminal) | `pkg/agent/loop_run_turn.go` switch-fail branch (`model_switch` stage); `pkg/agent/effort_pair_switch.go::refuseUnresolvedPair` | `model_unavailable` | No | requested model, loaded model | unchanged (wording already specific) |
| B3 | Omnipus's own rate limiter (SEC-26) refuses an iteration | `pkg/agent/loop_run_turn.go::beginIteration` → `"rate limit: %s (retry after %.0fs)"` → substring match → `rate_limited` | `rate_limited` — **misattributed**: the copy blames the provider ("temporarily overloaded") for Omnipus's own limit (§11.4 already keeps this row unassembled) | No — but wrong | policy rule name, retry-after seconds | own code `agent_request_limit` (attribution `config`, no "retry", W11's wording — shape architect-decided R-5); **in scope — founder E2, 2026-11-18 (OQ-1857-5 A)**: matrix row §13.8.5 B3, oracle S-139/T-55 |
| B4 | Tool-iteration ceiling (native turn) | `pkg/agent/loop_run_turn.go::finalizeTurn` → `toolLimitResponse` (`pkg/agent/loop.go::toolLimitResponse`) | none (fixed content notice + `turn_failed`) | No | limit, agent name | unchanged (already plain and actionable) |
| B5 | Tool-denial budget aborts the turn | `pkg/agent/tool_denial.go::toolDenialAbortReason` → `abortTurn` (curated text attached to the turn error) | `unknown` carrying the curated text | **Yes — corrected (F-1)**: the curated text rides `unknown`, so the SPA renders the generic line; the internal-flavoured text (`blocked: tool %q denied …`) never shows | tool name, denial count, enumerated denial reason (timeout/denied/saturated) | typed code (matrix §13.8.5) + wording row (§13.8.3 W8) |
| B6 | Hook abort / hard abort | `pkg/agent/loop.go::hookAbortError`, `::abortTurn` (curated hook reason) | `unknown` carrying the curated reason | **Yes — corrected (F-1)**: same mechanism; the hook's reason is dead in the SPA | hook's own decision text (caller-curated, trusted stage) | **mechanism pinned (MAJ-005)**: typed code `agent_hook_aborted` (proposed, matrix) — the hook's reason moves to technical details, never into a free-text `message` |
| B7 | The sender's **message could not be saved** — two stage-bound sites share `messageNotSavedError{role: "user"}` (narrowed in the post-check: the old "only admission, before any work ran" claim was wrong): **(root admission)** the turn's first message fails its window append **before any work ran** (`pkg/agent/window_runtime.go::appendWindowMessage` via `pkg/agent/loop_run_turn.go:825-826`, ending through `::contextWindowTurnExit`) — "wasn't sent / nothing ran" is established **here only**; **(mid-turn steering admission)** a dequeued steering input fails the same append **after earlier work already ran** (`pkg/agent/wake_input.go:252` via `pkg/agent/loop_run_turn.go:1381-1392` → `::contextWindowTurnExit` — also terminal, but "nothing ran" is false there) | the two call sites above; consumed in `pkg/agent/translate_error.go::TranslateTurnError` (`messageNotSavedError` branch) | `unknown` carrying the fixed reply | **Yes — corrected (F-1)**: the fixed reply is dead in the SPA; the generic line shows | that the message was not saved — and, at the root site only, that nothing had run yet (MAJ-001, as corrected by the post-check) | typed code `reply_not_saved` with **two catalogue variants** (matrix §13.8.5): root admission → W9 ("wasn't sent" valid **only** there); steering admission → a variant that claims only "your message could not be added to this conversation", never "nothing ran" |
| B8 | Panic during a turn (recovery line) | `pkg/agent/session_worker.go::processTurn`, `pkg/agent/loop_unroutable.go::dispatchUnroutableMessage`, **`pkg/agent/revive_support.go::runRevivedOrdinaryTurn`** (third site — added by the architect read, F-12) | none (fixed text) | No (own text published as the reply) — same no-reason shape as the generic line; "Please" violates the guard | none | wording row (W10) |
| B9 | `delegated_task_limit` | **no producer** — `grep` over the whole release tree finds only the constant (`pkg/agent/translate_error.go`), the `mustNotDropEvent` gate (`pkg/agent/eventbus.go`) and the generated catalogue; nothing emits the code. §13.7 pins one reserved row for it | n/a | n/a | — | **R-6 (agreed), amended by founder E6, 2026-11-18**: team-lead marks the code and its §13.7 row reserved (done in §13.7 by this amendment); **no issue is filed** — the code stays reserved until the delegated-task limit path enters an Epic's scope |
| B10 | Tool set has a duplicate tool name — tool assembly fails (M1) | `pkg/agent/loop_run_turn.go::prepareToolSurface` dedup branch (stage `tool_assembly`; the user-facing emit at `loop_run_turn.go:965` is **hard-coded `CodeUnknown`** today — `pkg/tools/result.go::ToolAssemblyDuplicateCode` labels the structured synthetic-deny payload, not an `LLMError` code); the detector `pkg/agent/loop_policy.go::checkToolDedupInvariant` records exactly one source distinction: `CategoryMCP` → tag `mcp:unknown`, **everything else** → tag `builtin` (`loop_policy.go:153-156`) — the code does **not** record "operator-configured", which tools the non-MCP side is, or a structured variant fact (the tags exist only inside the `fmt.Errorf` text and audit details) | `unknown` + generic copy attached (dead in the SPA per F-1) | **Yes** | that tool assembly failed on a duplicate name — and, at the detector, only whether **an MCP tool is involved or not**. The "operator-configured" and "internal defect" characterisations of the round-2 text are **not decided by the code** and are withdrawn (post-check) | one typed code `tool_assembly_duplicate`, attribution **`ambiguous`** (attribution is per code, never per instance — R2-MAJ-003), **two catalogue variants keyed on the only fact the code carries — MCP-involved vs no-MCP-involved** (matrix §13.8.5): MCP-involved → a config-remedy sentence pointing at the worker's tool/MCP setup; no-MCP-involved → consequence + an honest "why is not recorded" + the D48 report line. **The round-1 builtin-duplicate exemption stays removed.** WP-0 must add the structured MCP/non-MCP fact at the emit site so the variant can be selected; until it exists the fail-closed fallback is the no-MCP variant — never the generic line |
| B11 | **Answer-present save failure at finalization** — a model response was produced in the turn and its final append failed (re-bound in the post-check: the round-2 binding claimed more than the code decides) | the `appendFinalModelResponse` failure branch of `pkg/agent/loop_run_turn.go::finalizeTurn` (`:86-91`: wraps `messageNotSavedError{assistant}`, reports via `pkg/agent/turn_exit.go::reportSessionSaveFailure`, sets `TurnEndStatusError`, returns the error) — **but that branch runs whenever `!stoppedSilent`**, including a truncated turn with empty content and a `DefaultResponse` fall-through with no model reply, so the branch alone does **not** establish "answered". W15's "answered" sentence is therefore **gated on a non-empty model response — a fact the emitter must carry** (WP-0 work: the branch must pass the answer-state fact; today it does not). Until the emitter carries it, the fail-closed rule applies: **the no-"answered" variant (B21's) is shown, never W15.** The direct `Sessions.Save` failure is **not** this row — it moves to B21 | `unknown` → generic | **Yes — the save failure, not the answer claim**: the save failure is terminal (`TurnEndStatusError`, cited above); whether an answer existed is decided only by the gated fact above | that the final append failed and the turn reports failed; "answered" only when the emitter carries the non-empty-response fact | typed code `reply_not_saved` (variant "answered, save failed", shown **only** under the gate) + wording row (W15) — not the panic line |
| B12 | Session restore fails while a turn is being aborted (M3) | `pkg/agent/loop.go::abortTurn` (`restoreLLM`, stage `session_restore`) | `unknown` → generic | **Yes** | that the turn could not be closed cleanly | typed code + copy per the matrix (§13.8.5; manifest row `M-1857-B12`, oracle S-139/T-55) |
| B13 | Context-window checkpoint storage failure (M4) | `pkg/agent/tool_result_checkpoint.go::contextWindowTurnExit` → `TranslateTurnError` | `unknown` → generic | **Yes** | that the context checkpoint could not be stored | typed code + copy per the matrix (§13.8.5; manifest row `M-1857-B13`, oracle S-139/T-55) |
| B14 | Goal-state notice failures on the **non-refusing** paths (M5) — a reminder could not be admitted, goal state could not be read for a background reminder, **or the mid-turn rubric-budget estimate could not read it** (re-scoped in the post-check: the round-2 "background paths only / no active turn involved" claim was wrong — `pkg/agent/goal_loop_forcing.go::goalRubricNoteForBudget` reports inside an **active turn**, sets `holds = true` and the turn continues). What all listed paths share: the site logs, reports and **continues** — no turn stops and no request is refused. The refusal path is row B22 | `pkg/agent/goal_reminder_error.go` (stage `goal_reminder_admission`), `pkg/agent/goal_record_error.go::reportGoalReadError` on its background reminder paths **and** from `goal_loop_forcing.go::goalRubricNoteForBudget` (mid-turn, turn continues) — **not** `::refuseUnreadGoalState`, whose caller `pkg/agent/loop_run_turn.go::prepareToolSurface` refuses an active request (B22) | `unknown` + curated text → generic (F-1); `goal_record_read` also embeds the raw `err` (`%v`) in its message | **Yes**; the raw-`err` embedding is a possible path leak (Inferred — reported to team-lead, not fixed here) | that goal state could not be read or written; on the rubric-budget path, that the turn continued with a conservative estimate | typed code `goal_state_unavailable` (proposed; non-refusing consequence variant — matrix), raw `err` out of the message |
| B15 | Previous reply still finishing — admission refusal (M6) | `pkg/agent/translate_error.go::TranslateTurnError` (`ErrPreviousExecutionPending` branch) | `unknown` + curated text → generic (F-1) | **Yes** | that another reply is still running | typed code `previous_reply_pending` (proposed, matrix) + own sentence (wait for the current reply to finish) |
| B16 | CLI dispatch refusals before the run starts (M7) — **four distinct classes (MAJ-002 + grill round 2, R2-MAJ-004; class boundaries re-keyed to error identity in the post-check)** | `pkg/agent/external_dispatch.go::runExternalCLISubTurn` propagates every refusal **before the run starts** (verified order: `externalRunResumeOnly` :362 → `beginExternalRun` :368 → `markExternalRunStarted` :424 → `driverForRun` :428, each returning an error that refuses the dispatch). **The classes are selected by error identity (`errors.Is`), not by call site** — the post-check corrected the round-2 partition: the resume sentinel `pkg/agent/external_run_session.go::errExternalResumeUnavailable` is returned from **three** sites (`reserveExternalConversation` :366/:371, `beginExternalRun` :444, `driverForRun` :469 — **not** from `startAndDrain`), so a `beginExternalRun` failure is either the sentinel (resume) or a storage error, and a `driverForRun` failure is either the sentinel or a driver-construction error (`newExternalDriver`). Classes: **(config gap)** missing executor config / empty `executor.cli` (pre-checks `external_dispatch.go:207-213`) and driver-construction failure (`newExternalDriver` when the error is not the sentinel); **(storage/preflight refusal)** lifecycle-read errors (`externalRunResumeOnly` :362-366, `externalRunPriorStart` via `beginExternalRun` :425-428 — a read failure is an error, never "no record", NEW-3), `beginExternalRun`'s non-sentinel errors, the start-marker write (`markExternalRunStarted` — "a failure to record it refuses the run"); **(resume unavailable)** `errors.Is(err, errExternalResumeUnavailable)` from whichever site returned it; **(workspace changed)** `::errExternalWorkspaceChanged` via `reportWorkspaceRefusal` | `unknown` (all plain `fmt.Errorf` texts) → generic | **Yes** | which refusal class: a config gap, a storage/preflight refusal, a conversation that cannot be resumed, or a changed workspace — resume-unavailable does **not** imply the workspace changed | config gap → `external_cli_invalid_config` (config — shared with B17b's invalid-cap variant, each its own catalogue variant); storage/preflight → new code `delegation_start_failed` (`ambiguous` — one truthful outcome for the storage sites); resume-unavailable → W14's first form (`delegation_not_resumable` variant 1); workspace-changed → W14's second form (variant 2) — both keep "start a new delegation". The catalogue's `agent_not_configured` is **not** used here (R2-MAJ-004): its current meaning is a missing workspace membership (B1), not a CLI-executor config gap |
| B17 | CLI run timeout / invalid resolved turn cap (M8) | `pkg/agent/runner/driver_claude.go`, `driver_codex.go`, `driver_opencode.go` (`"run exceeded timeout (FR-5.4)"`); `pkg/agent/runner/parse.go::invalidMaxTurnsEvent` | `unknown` → generic | **Yes** | that the run exceeded its time limit / that the cap value was invalid | timeout → the CLI form of `turn_timed_out` (R-2 allows the typed exits); invalid cap → its own config-attributed code `external_cli_invalid_config` (proposed — `external_cli_failed` is `ambiguous` and cannot carry a config remedy; matrix). Note: issue #1306 removed the run deadlines, so the timeout branch may be unreachable (Inferred) |
| B18 | Settings "Send a test message" for a CLI worker fails (M10) | `pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun` → `::sanitizeSmokeTestErrorMessage` (which has separate empty-message, permission and timeout paths alongside the shared translator) | conditional — the shared-translator paths end generic; the dedicated paths show their own appended guidance | **Conditional — not uniformly generic (MIN-003)**; fixtures must cover each path, not assume `unknown` | same safe facts as the corresponding C kind | the same CLI-only classifier as the run path (neutral voice); the Mac team's second case (model unavailable) surfaced exactly here — W2's neutral form (MIN-001); the Settings drain currently treats **any** error event, including a `Fatal: false` diagnostic, as a failed test (MAJ-008 divergence noted in the matrix, row C11) |
| B19 | **Continuing record failure** — a save fails while the turn's work keeps running: a started tool's receipt (`messageNotSavedError{role: "tool"}`) (MAJ-001; narrowed in grill round 2, qualified in the post-check) | `pkg/agent/started_tool_outcome.go::beginStartedTool` via the shared `pkg/agent/turn_exit.go::reportSessionSaveFailure` (stage `session_save`) — the caller **returns `true` after reporting** and its caller (`pkg/agent/loop_run_turn_tools.go:1467`) proceeds to execution: the tool still runs. **Two qualifications from the post-check:** (1) the shared reporter itself calls `ts.markTurnFailed()` (`turn_exit.go:57-58`), so the turn is **flagged failed on the done frame** even though this tool keeps running — "non-terminal" describes the tool's execution, not the flag; (2) the "only continuing caller" enumeration of the round-2 text is withdrawn as not enumerated — the other tool-role reporter callers (`started_tool_outcome.go:91`, `cancel.go:699`) are cancel paths, and the intermediate assistant continuation's failure is row B23 | `unknown` → generic | **Yes** — and the stage proves neither "not sent" nor "answered": the tool's execution continues (while the done frame reports failed) | that a part of the conversation could not be recorded while the tool keeps running — and that the turn reports failed | typed code `transcript_save_failed` (proposed, matrix — continuing variant), **non-terminal for the tool**: consequence line only (W16), no retry invite, no terminal-failure sentence; stage-specific live/reload tests (S-140/T-55) asserting both the continued execution and the failed flag |
| B20 | Gateway late save failure — the gateway cannot persist the exchange after streaming | `pkg/gateway/websocket_streamer.go::publishSaveFailureNotice` — emits the fixed `agent.MessageNotSavedReply` in an error frame **without** a typed `llm_error` payload; **the failure also fails the turn (corrected in grill round 2, R2-MAJ-001)**: `::Finalize` returns the late persistence error, and `::publishDone` sets `TurnFailed` on the done frame — the notice is **not** a post-success annotation of a completed turn | none (own fixed text shown as written; the §13.8.1 typed-frame rule does not apply here) | **No (own text) — but the turn is failed**: the done frame reports the failure, so the notice must never be read as "everything else succeeded"; its text also breaks voice rule 9 ("Sorry", "please") | that the exchange may not be persisted **and** that the turn reports failed | its own wording row in the WP-6 manifest (id `M-1857-B20`, oracle S-140) — rewritten to state both facts; listed in §13.8.4 as its own surface |
| B21 | **Finalization save failure without answer evidence** — the turn's closing save fails and **no fact in the failure establishes that an answer exists** (re-bound in the post-check: the round-2 text wrongly put the stopped-silent branch on the shared reporter and the direct Save under B11) — two producers: **(direct `Sessions.Save` failure)** `pkg/agent/loop_run_turn.go:92-107` — that save runs for **stopped-silent, truncated-empty, default-response and answered turns alike** and on failure emits through `TranslateLLMError(nil, err.Error())` (:97) with its own event, **not** through the shared reporter and **not** as a `messageNotSavedError`, so it carries **no answer-state fact**: whether an answer existed is unknown to the server on this path, and no "answered" sentence may show (the stopped-silent branch itself calls nothing — `:85-86`, "Nothing to append"; the round-2 claim that it calls the reporter is withdrawn); **(iteration-limit notice save failure)** `pkg/agent/loop_run_turn.go:137-144` — the shared reporter is called with `messageNotSavedError{role: "system"}`, `TurnEndStatusError` is set, and the final content was the tool-limit notice after `currentIteration() >= MaxIterations` — no answer exists here, decided by the caller | `pkg/agent/loop_run_turn.go::finalizeTurn` — the direct `Sessions.Save` failure block (:92-107) and the `recordIterationLimitNotice` failure branch (:137-144) | `unknown` → generic | **Yes** — the turn reports failed on both producers (`TurnEndStatusError` at :93/:141); what is **not** claimed is any answer state | that the closing save failed and the turn reports failed; for the iteration-limit producer additionally that the turn reached its limit without a final reply — "answered" is **never** claimable on this row (that conflation was R2-MAJ-001's second failure scenario, completed by the post-check) | typed code `transcript_save_failed` (finalization variant, no "answered" claim — matrix; manifest `M-1857-B21`, oracle S-140/T-55). Keying this variant on the direct-Save path requires the producer to carry the stage/answer fact (WP-0 work — today the direct Save emits a classifier code only, issue noted for the RED author) |
| B22 | **Active-turn goal-read refusal** — an active turn cannot read the goal state it needs while preparing its provider request, and refuses (new row, grill round 2, R2-MAJ-002) | `pkg/agent/goal_record_error.go::refuseUnreadGoalState` — calls the reporter, sets `TurnEndStatusError` and returns an error result; called from `pkg/agent/loop_run_turn.go::prepareToolSurface` (failed goal-forcing read, failed compressed tool-surface construction) — **terminal for the active request**: request preparation ends before the provider call. The background goal-loop paths are row B14 | `unknown` + curated text → generic (F-1) | **Yes** | that the request stopped because goal state could not be read — a stopped/refused active request, not a background event | typed code `goal_state_unavailable` (active-turn terminal variant — matrix; manifest `M-1857-B22`, oracle S-139/T-55 with a no-provider-call assertion) |
| B23 | **Terminal continuation-save failure** — an intermediate assistant continuation's save fails and the turn **stops**: no new tool round is admitted (new row, grill round 2, R2-MAJ-001) | `pkg/agent/loop_run_turn_response.go::recordToolCalls` — wraps the failed continuation flush in `messageNotSavedError{role: "assistant"}` and returns it; the caller `pkg/agent/loop_run_turn_iterations.go::dispatchToolCalls` sets `TurnEndStatusError` and returns **before `executeToolCalls`** (regression proof: `pkg/agent/wu22_turn_archive_failure_test.go::TestWU22FlushContinuationArchiveFailureStopsToolRound` — "no new tool round is admitted after the failed continuation flush") | `unknown` → generic | **Yes** | that the turn stopped at its next step because the running record could not be saved — a real turn failure, not a continuing-work notice (the round-1 text wrongly put this row under "the work continues") | typed code `transcript_save_failed` (terminal continuation variant — matrix; manifest `M-1857-B23`, oracle S-140/T-55 with the execution outcome asserted: `TurnEndStatusError`, no new tool round) |
| B24 | **Remaining record-save refusals** — the `messageNotSavedError` producers this amendment does not re-key (added by the post-check so no emitter hides): the final streamer ensure failure (`pkg/agent/loop_run_turn.go:125-130`, role assistant, terminal), the dedup synthetic-deny save (`pkg/agent/loop_run_turn.go:944-948`, role system, terminal), the cancel-marker append (`pkg/agent/cancel.go:698-699`, native turn, role system), a cancelled tool's receipt append (`pkg/agent/started_tool_outcome.go:90-92`, role tool, cancel path), a sub-turn result save (`pkg/agent/loop_run_turn_tools.go:2254-2257`, user role, terminal via `contextWindowExit`), and a saved wake input that cannot be placed (`pkg/agent/loop_window.go:73`, user role, "this turn's input is not saved either") | the six sites above — all through the same `messageNotSavedError` branch of `pkg/agent/translate_error.go::TranslateTurnError` | `unknown` → generic | **Yes** | that the record could not be saved (per site: which record — streamer, deny, cancel marker, cancelled receipt, sub-turn result, wake input) | **no new code promised in this amendment**: these are the named residual emitters of the T-54 allow-list (S-138/FR-092), each with its reason — they stay generic today, are listed so none hides, and their re-keying is left to a tracked follow-up (same treatment as B9's reserved pattern, without reserving a code here) |
| B25 | Turn-settlement storage failure (founder sec2 answer, 2026-11-18 — the raw-text case is folded into this amendment). `finishExecutionDisposition` fails after an ordinary wake's execution has finished, and the reporter **appends the raw error text to its user-facing sentence today**: `Message: "This turn finished, but saving the conversation's final state failed. Repair the gateway storage, then continue: " + err.Error()` on code `settlement_failed` (`pkg/agent/execution_disposition.go::reportOrdinarySettlementFailure`; the raw text also goes to the log there). Both call sites run at settlement time, after the execution — `pkg/agent/loop_inbound.go::finishOrdinaryWakeExecution` and the deferred call in `pkg/agent/session_worker.go`; no turn-stop or continuation claim is made by this row beyond that | the producer above; the frame reaches the browser through `pkg/gateway/websocket_forward_hub.go::hubError` → `pkg/agent/translate_error_provider_messages.go::WireLLMError` (a non-catalogue code passes the producer message onto the frame) | `settlement_failed` (not in the catalogue — the SPA's fallback renders the generic `unknown` copy, `src/lib/llm-error.ts::codeToMessage`) | **Yes** — and the raw `err.Error()` rides the wire frame's `Message` unrendered today | program-side fact only: the conversation's final state could not be saved; the raw cause belongs in the server log | **fail-closed copy rule (founder sec2 A)**: the sentence becomes a fixed, safe text — no raw error text in the message, the raw text stays in the server log only. Code mapping (R-2 shape, drafter-decided): re-key to the **already-proposed** `transcript_save_failed` (settlement catalogue variant) — the cause class is exactly "a record could not be saved" (B19/B21/B23's code), so no new enum entry is added; the internal `settlement_failed` code retires with the fix. Matrix §13.8.5 B25 |
| B26 | Goal-read failure with raw text in chat history (founder sec2 answer, 2026-11-18). The goal-read reporter builds `"Goal state could not be read; %s deferred: %v"` with the **raw error**, writes it into the **chat transcript** via `pkg/agent/goal_loop.go::writeGoalSystemTranscript` (also `::writeGoalSystemTranscriptErr`), and emits an `CodeUnknown` error event carrying the same message (`pkg/agent/goal_record_error.go::reportGoalReadError`). This is the producer behind B14's background-reminder read path (the turn continues there — binding per B14's row); the refusal path is B22, whose `pkg/agent/goal_record_error.go::refuseUnreadGoalState` sets the failed turn's **final content** to the same raw-text message today — both carriers are fixed by this row | the producer above (`reportGoalReadError`; history persistence via `writeGoalSystemTranscript`) | `unknown` carrying the raw-text message | **Yes** — and worse than the SPA-frame case: the raw text **persists in chat history** through the transcript write (a rendered surface), independent of the F-1 dead-message rule | program-side fact only: the goal state could not be read, so the deferred work waits; the raw cause belongs in the server log | **fail-closed copy rule (founder sec2 A)**: the fixed safe sentence replaces the raw `%v` in the transcript line **and** the event message; the raw error text goes to the server log only. Code mapping (R-2 shape, drafter-decided): `goal_state_unavailable` (already proposed for B14/B22 — no new code), consequence variant for the deferred path. Matrix §13.8.5 B26 |

Sibling settlement producers already comply — named so the sec2 sweep is complete (messages verified to carry no raw text): `pkg/agent/stop_settlement_notify.go::reportStopSettlementFailure` emits code `stop_pending` with the fixed sentence "Stop for session %s could not finish; required storage or notice publication failed. Retry after storage is repaired." (the raw failure text goes to the log call only), and `pkg/agent/secret_request_wire.go::deferSecretResume` emits code `secret_resume_pending` with its own fixed sentence. Neither is changed by this amendment.

**C. External CLI worker kinds (the #1857 core — all currently generic)**

All C rows reach the classifier through `pkg/agent/external_dispatch.go::SanitizeRunnerError` (synthetic status-0 `ProviderError` → today `classifyByMessage`, tomorrow the CLI-only classifier of §13.8.1) and surface as the `external_cli` error event + transcript entry (`emitExternalCLIErrorEvent`), then — when fatal — as the curated turn error prefixed `"external-cli run failed: …"`. Raw stderr stays in `SanitizedRunnerError.LogText` / gateway.log only. **Correction pass (F-2/F-3, R-2):** the curated CLI error carries its own code from dispatch to every surface — `pkg/agent/translate_error.go::turnErrorUserText` and every other surface builder use that code without re-classifying — and the `"external-cli run failed: "` prefix is dropped from every user surface (it survives only in gateway.log); Verbose technical details carry the CLI's display name, the exit status and the matched kind, never stderr text (FR-093). **State rule (MAJ-008):** the C codes describe *classified causes*, not run termination — classification runs for fatal and nonfatal error events alike (`drainExternalRun` emits every error event before testing `ev.Err.Fatal`; only the fatal branch sets the run error), and the W4-style terminal sentences apply **only** when the run actually terminates. Nonfatal diagnostics are row C11.

| # | Failure kind | Comes from (`file::symbol`) | Code today | Generic today? | Safe facts known (no raw text) | Proposed code (OQ-1857-2) |
|---|---|---|---|---|---|---|
| C1 | CLI rejects its command line — flag parse/usage error (e.g. the Mac team's case: a duplicated `--model` flag passed via the worker profile's **Additional CLI arguments**; an unknown option) | non-zero exit stderr tail: `pkg/agent/runner/driver_opencode.go::Run` post-`cmd.Wait` branch (same shape `driver_claude.go`, `driver_codex.go`) | `unknown` | **Yes** | CLI name (claude/codex/opencode); that the command line was rejected before any work ran. Not the flag text itself (raw command line — OQ-1857-3) | `external_cli_invalid_args` |
| C2 | CLI cannot use/route the model (the Mac team's second case: nested `error.type` `provider.no-route`, "Model unavailable: …") | OpenCode: the stream `error` event's **structured `error.type`** — at release head `pkg/agent/runner/driver_opencode.go::parseLine` reads only the top-level `message` and substitutes "unknown opencode error" when absent, so the nested `error.type` is lost before classification; the fix parses the structured field. Claude/Codex equivalents in their result/error events | `unknown` | **Yes** | CLI name; the model string Omnipus passed (`runner.RunOptions.Model`, user-chosen) | `external_cli_model_unavailable` |
| C3 | CLI auth/sign-in failure (expired or missing CLI sign-in) | Claude: not-signed-in arrives **on the stream result** — `is_error: true` with "Not logged in · Please run /login" (`pkg/agent/runner/driver_claude.go` result handling), not via stderr; Codex: `error`/`turn.failed` events (`pkg/agent/runner/driver_codex.go`); OpenCode: its error event (`pkg/agent/runner/external_event.go`). **No dispatch-time pre-check exists** — `pkg/agent/runner/conntest.go::FailureReason` (`unauthenticated`) is the Settings connection test only | `unknown` | **Yes** | CLI name; that sign-in is needed | `external_cli_needs_signin` (attribution `user`, mirroring `needs_provider`) |
| C4 | CLI exits non-zero with no recognised cause (everything else in the stderr tail) | same exit path as C1 | `unknown` | **Yes** | CLI name; exit status belongs in technical details only (VS-2) | `external_cli_failed` |
| C5 | CLI turn cap exceeded (the CLI worker's own iteration limit) | `pkg/agent/runner/driver_opencode.go::parseLine` `"turn cap exceeded: %d turns (max %d)"` (fatal; claude/codex equivalents) | `unknown` | **Yes** | CLI name, turn count, cap (numbers — safe) | `external_cli_turn_limit` |
| C6 | CLI stream read error (stdout pipe failed mid-run) | `pkg/agent/runner/driver_stream.go::streamParser` (`"stream read error: %v"`, fatal) | depends on the cause wording (MIN-003): a transport phrase (`unexpected eof`, `broken pipe` — both in `pkg/providers.IsTransportFailure`) classifies **`network`**; other causes end `unknown` | **Partly** — transport-wording causes already show the typed `network` line; other causes the generic line | CLI name; that reading the CLI's output failed | `external_cli_failed` — **corrected (F-7, R-2)**: no `network` reuse in the new design; the CLI path never yields provider or network codes, and both input classes (recognised transport phrase, unrecognised scanner failure) take the agreed CLI route without touching provider classification |
| C7 | CLI program fails to launch after the path check passed (exec race) | `pkg/agent/runner/driver_opencode.go::Run` `cmd.Start` error → `"external-cli dispatch: driver start (%s): %w"` (`pkg/agent/external_dispatch.go`) → the turn-error path | `unknown` | **Yes** (surface of the spawn error Inferred — the classification path is the same `TranslateTurnError` substrate as every turn error) | CLI name; that the program could not be started | `external_cli_failed` (start variant) |
| C8 | Missing CLI binary / failing configured program path | `pkg/agent/external_run_program.go::resolveRunProgram` → `thirdpartycli.RunProgram` refusal → `reportProgramRefusal` (curated refusal attached to the turn error) | `unknown` carrying the curated refusal | **Yes — corrected (F-1)**: the curated refusal is dead in the SPA; the generic line shows | program-path fact (curated text) | the refusal travels on its own typed code — `external_cli_not_found` proposed (beyond R-2's six classifier codes, folded into OQ-1857-2), attribution `config`; its sentence stays WP-6 wording-manifest scope (row `M-1857-C8`, oracle S-139/T-55) |
| C9 | CLI-relayed upstream refusal — the CLI's own model provider refuses (rate limit, out of credit) (M9) | CLI error wording relayed through the driver events (sources as C2/C3) | `rate_limited` when the wording matches the rate-limit matcher — **wrong remedy**, it points at Omnipus's provider settings; `unknown` otherwise (credit wording matches nothing) | **Partly** — limit wording shows `rate_limited` copy with the wrong remedy; credit wording shows the generic line | CLI name; the closed reason word (`limit`, `out of credit`) | `external_cli_upstream_refused` (R-2), attribution **`ambiguous`** — restored per R-2 (MAJ-007 transcription correction); closed `{reason}` word set (W13) |
| C10 | Control row — the CLI run is cancelled or times out as a typed exit | `pkg/agent/translate_error.go::typedExitCode` | `turn_canceled` / `turn_timed_out` | No — control row (the CLI path's only pass-through typed exits, R-2) | the typed-exit facts | unchanged — listed so the six-code rule's stated exceptions stay visible |
| C11 | Control row (MAJ-008) — a **nonfatal** CLI diagnostic during an otherwise successful run (e.g. OpenCode's malformed-JSON line, `Fatal: false`) | `pkg/agent/runner/driver_opencode.go::parseLine` (`Fatal: false` error events); `pkg/agent/external_dispatch.go::drainExternalRun` sanitizes, appends and emits every error event **before** testing `ev.Err.Fatal` — only the fatal branch sets `runErr`, so the emission is not gated on fatality (the baseline fact corrected in grill round 2, R2-MAJ-005) | none as a *terminal* code — but the diagnostic itself **currently emits and persists an error line** on the normal-run path | **No terminal failure — and not display-free either**: the run outcome is unchanged, yet today's emitted/persisted diagnostic line is a user-facing error line this amendment removes | the diagnostic's classified kind, for the record | **normal run — no terminal sentence, no retry invite, no code, and the emitted/persisted diagnostic line removed** (content stays in gateway.log; if the run later fails, the terminal sentence stands on the later event alone). **Settings — pinned now (R2-MAJ-005), not left to WP-0**: `pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun` returns `ok: false` on every error event and `::buildResponse` publishes that failed result — the stricter failed-test behaviour is **retained** unchanged (changing the drain rule would be a separate deliberate decision, never an implicit WP-0 implementation choice); the test-level copy (manifest `M-1857-C11-settings`, WP-6) states truthfully that `{cli}` produced output the test could not read and never claims the diagnostic proves a failed CLI run |

Totals (recounted after the founder answers, 2026-11-18): **44 kinds** listed (A1–A7, B1–B26, C1–C11 — B25/B26 added by the sec2 answer); **30 can end in the generic line today** — 25 always (C1–C5, C7, C8; B5–B7; B10–B17; B19; B21–B26), 3 conditionally (A4/A5 as narrowed by R-4; B18's shared-translator paths), 2 partially (C6: transport-wording causes already classify `network`; C9: limit wording shows `rate_limited` with the wrong remedy, credit wording the generic line) — and the totals are **stated per exact input and surface**, not as a reconciled whole (MIN-003). The amendment gives every one of them a specific line or an explicit disposition: C1–C5, C7, C9 on the five R-2 CLI codes, C4/C6/C7 (and the B17 timeout) on `external_cli_failed` and the typed exits, C8 on the proposed `external_cli_not_found`, A4/A5 on the WP-0-introduced `provider_error`/`provider_request_rejected` forms, B3 on `agent_request_limit` (in scope — founder E2), B5–B7/B10–B17/B19/B21–B26 on typed codes with their own sentences or manifest rows (matrix §13.8.5), B20 its own manifest row with the corrected turn-failed state, B24 explicitly **not re-keyed** — the named residual emitters of the T-54 allow-list (stated with reasons, not hidden), C11 explicitly none on the normal run with the Settings disposition pinned. The near-miss rewrites are W8–W11 and W16.

Proposed new codes are **contract data** (Hard Constraint #8, one atomic WP-0-style change: enum + `x-user-messages` + attribution rows → the `{cli}` slot in the generators → regenerate → code): `external_cli_invalid_args`, `external_cli_model_unavailable`, `external_cli_needs_signin`, `external_cli_failed`, `external_cli_turn_limit`, `external_cli_upstream_refused` (R-2, architect-decided) join the `LLMError.yaml` enum with catalogue copy and attributions (`config` for invalid-args/model-unavailable/turn-limit, `user` for needs-signin, **`ambiguous` for failed and upstream-refused** — MAJ-007 restored the read's R-2 map); `external_cli_not_found` (C8), `external_cli_invalid_config` (B16 config gap + B17b) and the non-CLI codes of matrix §13.8.5 (`agent_hook_aborted`, `tool_assembly_duplicate`, `reply_not_saved`, `transcript_save_failed`, `goal_state_unavailable`, `previous_reply_pending`, `delegation_not_resumable`, `delegation_start_failed`) are drafter-proposed and ride OQ-1857-2; `provider_error` (A4/A5) is also WP-0-introduced. `agent_request_limit` (B3) **is in that list — restored 2026-11-18: OQ-1857-5 was answered "include" (founder E2)**, which supersedes the post-check removal; its matrix row (§13.8.5 B3) carries the attribution (`config`) and the oracle that the removal had found missing. B25 and B26 add **no** new codes: the settlement case re-keys to the already-proposed `transcript_save_failed` (settlement variant) and the goal-read case to the already-proposed `goal_state_unavailable` (consequence variant). Attribution is **per code, never per instance** (MAJ-005) — a code whose instances differ in ownership takes `ambiguous` with a truthful catalogue variant per instance class (B10's MCP-involved and no-MCP-involved variants, R2-MAJ-003), or is split when the classes need different actions (B16's four classes). `{cli}` joins the closed slot vocabulary and renders a **fixed display name** from a closed map ("Claude Code", "Codex", "OpenCode") keyed by driver — never the raw configured `executor.cli` value (F-14). The contract work also covers: the SPA action-table rows for the new codes (§11.2, the `ErrorFrame` action mapping), the `{cli}` slot in the generator vocabulary and in the `omnipus-voice` skill's slot list (the VS-3 pattern — voice owner), and the replay/attribution routing in `src/lib/llm-error.ts` (a typed CLI code replays through the same routing as `provider_says`, never through the generic-line path). OQ-1857-2 puts the alternatives to the architect/founder before any contract edit.

#### 13.8.3 Proposed wording (chat forms, written with the `omnipus-voice` skill)

These are the sentences for the founder's wording review. Chat voice ("I"); every row also carries a **neutral twin** for the surfaces outside a chat (R-WD-8) — second table below. `{cli}` is the new slot and renders a fixed display name ("Claude Code", "Codex", "OpenCode") from a closed map keyed by driver, never the raw configured value; `{n}`, `{seconds}`, `{minutes}`, `{model}`, `{provider}`, `{host}` exist. Every line passes the skill self-check: what happened · observed why · one action; no blame; no status codes, paths or flag text at Normal; sentence case; no "please" outside the D48 sentence; no emoji; one to three short sentences. Copy-rule-test constraints (verified in `pkg/agent/translate_error_test.go`): a `config`-attributed code's copy may not contain the banned model-switch phrases (`bannedSwitch`, including "pick a model") and may not tell the user to retry; `Omnipus` as a word fails the voice guard everywhere outside the D48 sentence (F-9, F-10).

**Status (founder 2026-11-18, E4 A — OQ-1857-8):** the wording pattern is kept; every row below is **proposed — founder review pending (E4)**. The founder reviews the corrected example messages (W1–W16 and their neutral twins, including the §37-corrected rows) against the pattern before anything is built; exact-copy wording-manifest tests (T-47 and the matrix `M-1857-*` texts) are written only after that review. The classification, no-raw-text and code/attribution tests (T-52–T-56's non-copy assertions) do not wait for it.

| W | Kind | Current line | Proposed line (chat) | What the user can do | Notes |
|---|---|---|---|---|---|
| W1 | C1 invalid args | "This turn didn't finish, and we can't tell why. Retry — …" | `I couldn't start {cli}: it rejected its own command line, so nothing ran. Check the worker's Additional CLI arguments setting.` | Check the Additional CLI arguments setting; fix the duplicated or unknown option | Names no flag text (OQ-1857-3); the setting's real label is **Additional CLI arguments** (worker profile → Runtime — the Mac team's case, folded in); nothing ran, so retry alone cannot help |
| W2 | C2 model unavailable in the CLI | generic line | `I couldn't run the task in {cli}: it couldn't use the model {model}. Set this worker's model to one {cli} supports, in the agent's settings.` | Set the worker's model to one the CLI supports | **Corrected (F-9)**: the draft's "Pick a model {cli} offers" is a banned switch phrase in `config`-attributed copy (`bannedSwitch` in `translate_error_test.go`); reworded per the architect's example. `{model}` is the string Omnipus passed — the name the user picked |
| W3 | C3 CLI sign-in | generic line | `{cli} is not signed in on the machine it runs on, so the run stopped. Sign in to {cli} there, then retry the task.` | Sign in to the CLI on that machine, retry | **Corrected (F-14)**: says where to sign in — the CLI on its own machine (its own login), not an in-app sign-in; worded without the product name so the voice guard passes. Attribution `user`, so the retry advice is allowed |
| W4 | C4 exit, unknown cause | generic line | `I couldn't finish the run in {cli}: it stopped with an error I couldn't identify. Retry the task; if it keeps happening, check that {cli} works on its own.` | Retry; then test the CLI outside Omnipus | **Corrected (F-14)**: the draft's "didn't say why" claimed a fact the server cannot always observe (stderr may exist and simply match nothing); "an error I couldn't identify" states our knowledge honestly (D7). **Terminal-only (MAJ-008)**: this sentence is shown only when the run actually terminated — a nonfatal diagnostic never receives it (row C11). Exit status stays in technical details (VS-2) |
| W5 | C5 CLI turn cap | generic line | `I stopped the run in {cli}: it reached its limit of {n} turns for this task. Split the work into smaller steps, or raise the limit in the agent's settings.` | Split the task, or raise the limit | `{n}` = the CLI's resolved max turns |
| W6 | C6 stream read error | generic line | `I couldn't finish the run in {cli}: reading its output failed. Retry the task; if it keeps happening, check that {cli} works on its own.` | Retry; then test the CLI outside Omnipus | **Corrected (F-7/R-2)**: one code (`external_cli_failed`), no transport split — the CLI path never yields `network`; the sentence states the observed fact |
| W7 | C7 launch failure | generic line | `I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings.` | Check the program path | The `external_cli_failed` start variant; same remedy family as C8's typed refusal |
| W8 | B5 denial-budget abort | `blocked: tool "X" denied (timeout) for agent "Y" — the turn reached its limit of 10 tool denials` (today generic in the SPA, F-1) | `I stopped this turn: it reached its limit of {n} blocked tool calls. Check the approval that was asked for, or the tool's permissions, then run the task again.` | Check the pending approval or the tool's permissions, re-run | **Corrected (F-14)**: the draft's "{tool} kept being denied" named one tool — the budget spans several — and "denied" is wrong when the reason is an unanswered approval; "blocked tool calls" covers both. Enumerated denial reasons stay available for Verbose details |
| W9 | B7 message **admission** refused (MAJ-001: stage-scoped) | `Sorry, I couldn't save your message — please try again later.` (today generic in the SPA, F-1) | `I couldn't save your message, so it wasn't sent. Try sending it again.` | Send it again | **Corrected (F-14)**: the draft named a cause nobody observed (the save failure could be disk space, permissions or a lock); the only observed fact is that the save failed. Removes "Sorry"/"please" (voice rule 9). **"Wasn't sent" is valid only for the admission stage** — the same error type also fires mid-run (B19/W16), where nothing was "not sent" |
| W10 | B8 panic line | `Error processing message: the agent turn failed unexpectedly. Please try again.` | `I couldn't finish this turn because of an internal error. Retry; if it keeps happening, please report it to the Omnipus.ai team.` | Retry; report if it repeats | **Corrected (F-10)**: the draft's "something inside Omnipus failed" breaks the voice guard (`\bOmnipus\b` fails everywhere outside the D48 sentence); reworded per the architect's example. The D48 sentence stays the one allowed "please". Now also covers the third recovery site (`revive_support.go`) |
| W11 | B3 own rate limiter | `The model provider is temporarily overloaded. Wait a moment, then retry.` (misattributed) | `I've reached my limit of model requests for this hour. I can continue in {minutes} minutes, or you can raise the limit in Settings.` | Wait for the window, or raise the limit | **Corrected (R-5, architect-decided shape)**: own code `agent_request_limit`, attribution `config` (the operator sets SEC-26's hourly limit), so the copy must not say "retry" and must not blame the provider — the draft's "Wait {seconds} s, then retry" broke both copy rules. **In scope (founder E2, 2026-11-18)**; `{minutes}` = the limiter's wait. **Operator path — verified on the current tree (2026-11-18), a wiring gap, not an invented path:** the Settings screen exposes the field (`src/components/settings/SecuritySection.tsx` — hydrated from and saved back to `security.rate_limits.max_agent_llm_calls_per_hour` via `src/lib/api/config.ts::updateConfig`), but no server code reads that key — `pkg/config/platform_auth.go::SecurityConfig` carries only `platform_auth`; the enforced limit is `sandbox.rate_limits.max_agent_llm_calls_per_hour` (`pkg/config/sandbox.go::OmnipusRateLimitsConfig`, consumed at `pkg/agent/loop_run_turn.go::beginIteration`), written only by PUT `/api/v1/security/rate-limits` (`pkg/gateway/rest_rate_limits.go::putRateLimits`, hot-reloaded) — an endpoint no SPA code calls. The screen→enforced-key wiring is **gap G7 (§37)**: the build either wires the save to the enforced path or W11's "in Settings" clause is reworded at the founder's copy review |
| W12 | A4/A5 dispatched-but-unclassified (as narrowed by R-4) | generic line | no quote: `{provider} ({host}) answered with an error. Fix: retry in a moment; if it repeats, switch model.` · with `provider_says`: the §13.2 quoted API form | Retry; switch model if it repeats | **Narrowed (R-4)**: only a promoted residual (HTTP ≥ 400 received, or 200 unparseable); quoted form exactly when the provider's sentence is present. `provider_error` is WP-0-introduced; `unknown` remains only for the genuinely nothing-known case |
| W13 | C9 upstream refused (two closed reason forms) | limit wording: the `rate_limited` copy (wrong remedy); credit wording: generic line | limit: `The model {cli} uses is limiting requests right now. Try the task again in a moment.` · out of credit: `{cli}'s model provider account is out of credit. Top up that account to run tasks with {cli}.` | limit: try again in a moment · out of credit: top up the CLI's own account | `{reason}` is a closed word set (`limit`, `out of credit`); attribution `ambiguous` (R-2, MAJ-007). The remedy points at the CLI's own provider account, never at the providers configured in this app — that misattribution is exactly today's `rate_limited` bug |
| W14 | B16 delegation cannot continue — **two cause forms, never merged (MAJ-002)** | generic line | resume unavailable (`errExternalResumeUnavailable`): `I couldn't continue this delegation in {cli}: its previous conversation can't be resumed. Start a new delegation.` · workspace changed (`errExternalWorkspaceChanged`): `I couldn't continue this delegation in {cli}: its workspace changed since it started. Start a new delegation.` | Start a new delegation | Each form is bound to its typed sentinel only — resume-unavailable states nothing about the workspace (an unchanged-workspace control is in the tests); both keep the supported action; the config-gap and storage refusals ride their own typed codes (B16 — `external_cli_invalid_config` / `delegation_start_failed`) instead. **Both forms require `{cli}` (fixed in grill round 2, R2-MAJ-004)**; the absent-fact fallback is defined: when no driver (and with it no display name) is available, the no-fact form drops the CLI clause — `I couldn't continue this delegation: its previous conversation can't be resumed. Start a new delegation.` (workspace form likewise) — and never renders an empty slot (§13.2) |
| W15 | B11 completed-answer persistence failure (final save) | generic line | `I answered, but I couldn't save this conversation. This exchange may be missing from your history; check the answer above before asking me to repeat anything.` | Check the delivered answer first; repeat only if the task itself needs it | **Corrected (MAJ-001), action rewritten in grill round 2 (R2-MAJ-006), binding re-gated in the post-check**: shown only when the emitter carries the answer-state fact — a **non-empty model response** at the `appendFinalModelResponse` branch of `finalizeTurn` (`loop_run_turn.go:86-91`). The branch alone does not establish "answered": it runs whenever `!stoppedSilent`, including a truncated turn with empty content and a `DefaultResponse` fall-through, so WP-0 must add the answer-state fact to the emitter; **until it does, the no-"answered" form (B21's) is shown — fail-closed, never W15.** The direct `Sessions.Save` failure (`loop_run_turn.go:92-107`, shared by stopped-silent, truncated-empty, default-response and answered turns, with no answer-state fact) never shows W15. The round-1 action ("ask again if you need it saved") is **removed**: repeating a task whose message or tool effects already went out is not a history repair — it can duplicate real work, it does not restore the original transcript, and the second save can fail too (the same distinction `docs/tools.md::Background results` states for reporting failures — cited by analogy, not as a code decision). The safe, true action is to check the existing result before deciding to repeat anything. No invented persistence retry, no durability promise. Mid-run record failures get W16; finalization save failures without answer evidence get B21 |
| W16 | B19 continuing record failure (a started tool's receipt save; **non-terminal**, MAJ-001 — bound to `beginStartedTool`, whose caller returns `true`) | generic line | `Part of this conversation could not be recorded while I kept working. The work continues; recent messages may be missing after you reopen the chat.` | none — "the work continues" is the stated system action | States only what is observed (a record failed, the tool keeps running); no terminal-failure sentence, no "not sent", no "answered", no retry invite. **Valid only for the continuing caller (grill round 2, R2-MAJ-001)**: an intermediate-assistant save failure stops the turn (B23) and must never receive this line. **Qualified in the post-check**: the shared reporter sets the turn-failed flag (`pkg/agent/turn_exit.go::reportSessionSaveFailure` → `ts.markTurnFailed()`), so the turn's done frame reports failed even though this tool keeps running — "the work continues" refers to the tool's execution only, and the WP-6 wording review must reconcile the line with that flag (the line may not promise an outcome the done frame contradicts); the stage-splitting tests cover the split and the flag (S-140/T-55) |

Neutral twins (R-WD-8) — every row's form for the surfaces outside a chat (`task_failed` centre body, parent tool result, Settings test message; §13.8.4):

| W | Neutral form |
|---|---|
| W1 | `{cli} rejected its own command line, so nothing ran. Check the worker's Additional CLI arguments setting.` |
| W2 | `The run in {cli} could not use the model {model}. Set this worker's model to one {cli} supports, in the agent's settings.` |
| W3 | `{cli} is not signed in on the machine it runs on, so the run stopped. Sign in to {cli} there, then retry the task.` |
| W4 | `The run in {cli} stopped with an error that could not be identified. Retry the task; if it keeps happening, check that {cli} works on its own.` |
| W5 | `The run in {cli} reached its limit of {n} turns for this task. Split the work into smaller steps, or raise the limit in the agent's settings.` |
| W6 | `The run in {cli} ended because reading its output failed. Retry the task; if it keeps happening, check that {cli} works on its own.` |
| W7 | `{cli} could not be launched. Check the CLI worker's program path in Settings.` |
| W8 | `This turn reached its limit of {n} blocked tool calls. Check the approval that was asked for, or the tool's permissions, then run the task again.` |
| W9 | `The message could not be saved, so it was not sent. Try sending it again.` |
| W10 | `This turn ended because of an internal error. Retry; if it keeps happening, please report it to the Omnipus.ai team.` |
| W11 | `This agent reached its limit of model requests for this hour. It can continue in {minutes} minutes, or the limit can be raised in Settings.` |
| W12 | `{provider} ({host}) answered with an error. Fix: retry in a moment; if it repeats, switch model.` (already neutral) |
| W13 | limit: `The model {cli} uses is limiting requests right now. Try the task again in a moment.` · out of credit: `{cli}'s model provider account is out of credit. Top up that account to run tasks with {cli}.` (already neutral) |
| W14 | resume unavailable: `This delegation could not continue in {cli}: its previous conversation can't be resumed. Start a new delegation.` — no-fact form (no CLI display name; approved, Prometheus, 2026-11-23): `This delegation could not continue: its previous conversation can't be resumed. Start a new delegation.` · workspace changed: `This delegation could not continue in {cli}: its workspace changed since it started. Start a new delegation.` |
| W15 | `The reply completed but the conversation could not be saved. This exchange may be missing from the history; check the delivered result before repeating any part of the task.` |
| W16 | `Part of this conversation could not be recorded while the work continued. The work continues; recent messages may be missing after you reopen the chat.` |

The generic line itself keeps today's copy for the reserved case — a failure with no known safe reason, where "known safe reason" is defined in FR-092 as: a typed sentinel matched, a structured fact present, or a pinned matcher matched. **W1–W7 and W12–W16 are the #1857 deliverable**; W8–W11 are adjacent voice rewrites found by the same sweep, offered in the same review so the founder rules on the pattern once. All sixteen rows stay **proposed** until the founder's E4 copy review (status note above). Every row's two forms and every per-code variant/no-fact form are pinned in matrix §13.8.5.

#### 13.8.4 Surfaces a turn failure reaches (F-2)

| Surface | Producer (`file::symbol`) | Voice | Line it shows |
|---|---|---|---|
| Chat error line / error frame | `pkg/gateway/websocket_forward_hub.go::hubError` → `pkg/agent/translate_error_provider_messages.go::WireLLMError` | agent "I" | the catalogue copy for the (typed) code; `provider_says` quoted when present and allowed |
| Published turn-failure reply text | `pkg/agent/session_worker.go::processTurn` (via `userVisibleTurnError`) | agent "I" | the same catalogue copy — built from the code, never re-classified, never the `"external-cli run failed: "` prefix |
| Task/centre body (`task_failed`) | `pkg/agent/translate_error.go::turnErrorUserText` | neutral twin | the neutral form of the same cause sentence (R-WD-8) |
| Parent's **tool result** (delegation, agent-readable) | the `ForLLM` field built in `pkg/agent/external_dispatch.go::drainExternalRun` (not `translate_error.go::ForLLM` — that is a consumer, not the producer; MAJ-003) | neutral, agent-readable | the typed code and its sentence; no raw stderr; the draft's double `"external-cli run failed: "` prefix removed |
| Parent's **completion/inbox message** (agent-readable, separate route) | `pkg/agent/steer_completion.go::completionDisposition` → `::completionMessage` (constructs a `SessionMessageError`, child-to-parent) | neutral, agent-readable | the failure text carries the typed cause to the parent agent — asserted on **this** route, not only on the tool result (MAJ-003) |
| Parent's **human lifecycle summary** (delegation timeline) | `src/lib/delegationEvents.ts::spanTerminalEvent` → `src/lib/delegationEventLine.ts::delegationEventLineText` ("{agent} stopped without finishing") + `::delegationEventShowsOpen` (the **Open** affordance) | neutral, human | **unchanged by this amendment** — the approved lifecycle-summary design stands: the summary stays generic, the journey is summary → **Open** → the child's specific error line (S-141); no child text is folded into the summary (MAJ-003) |
| Settings "Send a test message" result | `pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun` → `::sanitizeSmokeTestErrorMessage` — `::buildResponse` publishes the result | neutral | a curated per-kind sentence — **W2's neutral form** for the model-unavailable case (the Mac team's second case; MIN-001) and **W1's neutral form** for the duplicated-arguments case (added in grill round 2, R2-MIN-001: the process-exit path reaches Settings too, asserted with a failed-test status, no flag/value text in the serialized result, and a corrected-arguments success control). **Nonfatal-diagnostic disposition pinned (grill round 2, R2-MAJ-005)**: the drain treats any error event, including a `Fatal: false` diagnostic, as a failed test (`ok: false`) — that stricter behaviour is **retained** by this amendment (changing it would be a separate deliberate decision), with truthful test-level copy (manifest `M-1857-C11-settings`) that never claims the diagnostic proves a failed CLI run |
| Gateway late save notice | `pkg/gateway/websocket_streamer.go::publishSaveFailureNotice` — fixed `agent.MessageNotSavedReply`, **untyped** error frame (no `llm_error` payload) | agent "I" (in-chat) | its own fixed text — the §13.8.1 typed-frame rule does not apply; its wording row is `M-1857-B20` (WP-6 manifest) |
| Verbose technical details | §12.2 detail levels (L9) | neutral, technical | the CLI's display name, the exit status, the matched kind — never stderr text, never the outside program's own error text (FR-093; **decided 2026-11-18**: founder E1 B and chief-ruled E3 A — technical details carry only the facts: program name, exit status, kind of failure) |
| gateway.log | `pkg/agent/external_dispatch.go::SanitizeRunnerError` (`LogText`) | operator | raw stderr stays here only — the one surface for the raw text (decided 2026-11-18: E1 B / E3 A; the same rule fixes the sec2 raw-text cases B25/B26, whose raw error text belongs in the server log only) |

Rules: (1) every surface builder reads the code's catalogue copy — no surface re-classifies or re-derives wording; (2) the curated CLI error carries its code from dispatch to every surface; (3) `"external-cli run failed: "` never reaches a user surface; (4) every W row above has both voice forms; (5) the parent side is **three separate surfaces** (tool result, completion message, lifecycle summary) — fixing the tool result alone does not touch the other two (MAJ-003).

#### 13.8.5 Amendment disposition matrix (MAJ-005 — the acceptance oracle for every newly promised cause)

Rows that already carry a complete mapping — C1–C7 (W1–W7), A4/A5 (W12), C9 (W13) — are not repeated. Every other cause this amendment promises gets its full disposition here, so the RED author never invents a code, an attribution or a copy. Codes and attributions marked *proposed* are the drafter's completion of the architect-decided shape; the architect closes any remaining contract-shape gap at WP-0 (OQ-1857-2), and WP-6 rows name their manifest id and acceptance oracle so the copy is not invented by the implementer either. Attribution is per code, never per instance.

| Cause | Observed trigger (no raw text) | Terminal? | Code (proposed) | Attribution | Copy — chat / neutral (manifest row if written at WP-6) | Required slots · no-fact form | Surfaces | Oracle |
|---|---|---|---|---|---|---|---|---|
| B3 own rate limiter (SEC-26) — in scope (founder E2, 2026-11-18) | the agent's own hourly LLM-call limit is reached before the provider call (`ri.cfg.Sandbox.RateLimits.MaxAgentLLMCallsPerHour > 0`, hour exhausted) → `recordRateLimitDenial` (RateLimitPayload: policy rule, retry-after seconds, agent id) and the turn ends `TurnEndStatusError` with the internal error `"rate limit: %s (retry after %.0fs)"` (`pkg/agent/loop_run_turn.go::beginIteration`, the emit block — today substring-matched to the provider-overloaded `rate_limited` copy) | Yes — the iteration is refused and the turn ends in error (decided by the caller: `ri.turnStatus` / `ri.ret1` in the same block) | `agent_request_limit` | `config` | W11 + neutral twin (no "retry", no provider blame — the config-attribution copy rules) | `{minutes}` (the limiter's wait, from the retry-after seconds) · no-fact form drops the wait clause | chat line, task/centre body | S-139, T-55 (fixture drives the emit block and asserts `agent_request_limit` with the fixed copy; a mutation re-matching the provider-overloaded copy fails) |
| B5 denial budget | the turn's denial budget of {n} was reached (reasons enumerated for details) | Yes | `agent_denial_limit` | `user` | W8 + neutral twin | `{n}` · the copy has no unslotted variant | chat line, task/centre body | S-137, S-139, T-55 |
| B6 hook abort | a hook returned an abort decision | Yes | `agent_hook_aborted` | `ambiguous` | manifest `M-1857-B6` (WP-6; both voices from the approved pattern) | none — the hook's reason goes to technical details, never into `message` · no-fact form is the code copy itself | chat line, details | S-139, T-55 |
| B7 message save refused — root admission | the turn's first message could not be saved at admission, before any work ran (`pkg/agent/window_runtime.go::appendWindowMessage` via `loop_run_turn.go:825-826` → `contextWindowTurnExit`; matrix row added in grill round 2, split in the post-check) | Yes — the message never entered the turn and nothing had run | `reply_not_saved` (catalogue variant "not sent") | `ambiguous` | W9 + neutral twin | none · no-fact form is the copy | the refused message's chat line | S-139, S-140, T-55 |
| B7 message save refused — mid-turn steering admission | a dequeued steering input could not be saved at the same append (`pkg/agent/wake_input.go:252` via `loop_run_turn.go:1381-1392` → `contextWindowTurnExit`) — **after earlier work already ran**, so "not sent/nothing ran" is false here and W9 must never show (post-check) | Yes — the steering input was not added and the turn ends in error | `reply_not_saved` (catalogue variant "steering input not saved") | `ambiguous` | manifest `M-1857-B7steer` (WP-6; proposed text: chat `I couldn't add your message to this conversation — it couldn't be saved. What ran earlier in this turn is unaffected.` · neutral twin likewise — claims only the save failure, never "nothing ran") | none · no-fact form is the copy | the refused message's chat line | S-140, T-55 (fixture drives the steering site, not the root admission) |
| B10 tool assembly duplicate — MCP tool involved | a duplicate tool name with an **MCP tool among the colliding pair** — the only source fact the detector records (`pkg/agent/loop_policy.go::checkToolDedupInvariant`: `CategoryMCP` → tag, everything else → tag; re-keyed in the post-check: "operator-configured" is not decided by the code) | Yes | `tool_assembly_duplicate` | `ambiguous` | chat: `I couldn't prepare this agent's tools: two of them have the same name, and one comes from an MCP server. Check the worker's tool and MCP setup for the duplicated tool.` · neutral: `This agent's tools could not be prepared: two of them have the same name, and one comes from an MCP server. Check the worker's tool and MCP setup for the duplicated tool.` (manifest `M-1857-B10`) | none (no tool names — they may be internal ids) · no-fact form is the code copy | chat line | S-139, T-55 |
| B10 tool assembly duplicate — no MCP tool involved (exemption stays removed) | a duplicate with **no MCP tool involved** — the detector's only other class. What the non-MCP tools are, and why they collide, is **unknown to the server** (not recorded as a structured value), so no "internal defect" or "operator-configured" claim is made (post-check) | Yes | `tool_assembly_duplicate` (same code, second catalogue variant — attribution is per code) | `ambiguous` | chat: `I couldn't prepare this agent's tools: two of them have the same name, and neither comes from an MCP server. Why they collide isn't recorded — please report it to the Omnipus.ai team.` · neutral: `This agent's tools could not be prepared: two of them have the same name, and neither comes from an MCP server. Why they collide isn't recorded — please report it to the Omnipus.ai team.` (manifest `M-1857-B10`, variant 2) | none (no tool names) · no-fact form is the copy | chat line | S-139 (both fixtures positive; a mutation restoring the generic result fails), T-55. WP-0 must add the structured MCP/non-MCP fact at the emit site (`loop_run_turn.go:965` is hard-coded `CodeUnknown` today); until then the no-MCP variant is the fail-closed fallback |
| B11 answer-present save failure | the final append failed at `finalizeTurn`'s `appendFinalModelResponse` branch (`loop_run_turn.go:86-91`, shared reporter, `TurnEndStatusError`) **and the emitter carries the answer-state fact — a non-empty model response**. The branch alone runs for empty and default-response turns too, so the fact is a WP-0 emitter requirement (re-gated in the post-check); until it exists the no-"answered" variant (B21) is shown — fail-closed | Yes (the turn reports failed); the answer claim rides the gate, not the branch | `reply_not_saved` (catalogue variant "answered, save failed", shown only under the gate) | `ambiguous` | W15 + neutral twin (rewritten: check-the-existing-result, no re-run advice) | none · no-fact form is the copy | chat line; task/centre body for task runs | S-140, T-55 (fixture asserts W15 only with the fact present, and never on the direct `Sessions.Save` failure) |
| B12 session restore in abort | `restoreLLM` failed while the turn was being aborted | Yes | `session_restore_failed` | `ambiguous` | manifest `M-1857-B12` (WP-6) | none · no-fact form is the copy | chat line | S-139, T-55 |
| B13 checkpoint storage | the context checkpoint write failed | Yes | `checkpoint_save_failed` | `ambiguous` | manifest `M-1857-B13` (WP-6) | none · no-fact form is the copy | chat line | S-139, T-55 |
| B14 goal-state notice, non-refusing paths | a goal-loop reminder could not start, goal state could not be read on a background reminder path, **or the mid-turn rubric-budget estimate could not read it** (`goal_loop_forcing.go::goalRubricNoteForBudget` — inside an active turn; scoping corrected in the post-check) — every listed site logs, reports and **continues**; no turn stops and no request is refused. The refusal path is row B22 | **No** — none of these paths stops a turn or refuses a request (the rubric-budget path continues with a conservative estimate; the refusal claim is B22's, not this row's) | `goal_state_unavailable` (non-refusing consequence variant) | `ambiguous` | manifest `M-1857-B14` (WP-6; consequence form, no action invented) | none · no-fact form is the copy | the goal loop's error event | S-139, T-55 (the background control stays separate from B22's caller-driven fixture) |
| B15 previous reply pending | `ErrPreviousExecutionPending` at admission | No — admission refusal; the running reply is untouched | `previous_reply_pending` | `ambiguous` | manifest `M-1857-B15` (WP-6; proposed text: "I couldn't start this reply: the previous one is still finishing. Try again in a moment.") | none · no-fact form is the copy | the refused message's chat line | S-139, T-55 |
| B16-config CLI executor config gap | missing executor config or empty `executor.cli` (pre-checks `external_dispatch.go:207-213`), or a driver-construction failure — `newExternalDriver` inside `driverForRun` **when the error is not the resume sentinel**. Classes are selected by **error identity, not call site** (post-check): the sentinel sites also carry non-sentinel errors, so only `errors.Is` separates the classes | Yes (refusal before the run) | `external_cli_invalid_config` (config-gap catalogue variant; shared code with B17b's invalid-cap variant) | `config` | manifest `M-1857-B16cfg` (WP-6; points at the agent's executor/CLI worker settings) | `{cli}` · no-fact form drops the CLI clause | chat line, Settings test (the executor resolves for the test too) | S-139, T-56 |
| B16-storage pre-run storage refusal | a non-sentinel error from the lifecycle sites: the session record could not be read (`externalRunResumeOnly` :362-366 / `externalRunPriorStart` via `beginExternalRun` :425-428 — a read failure is an error, never "no record", NEW-3), `beginExternalRun`'s other errors, or the start-marker write failed (`markExternalRunStarted` — a failure refuses the run); all propagate from `runExternalCLISubTurn` before the run starts (verified order :362/:368/:424/:428) | Yes (refusal before the run) | `delegation_start_failed` (new proposed code, grill round 2) | `ambiguous` | manifest `M-1857-B16sto` (WP-6; proposed text: chat `I couldn't continue this delegation: this agent's saved conversation state could not be read or recorded. Start a new delegation.` · neutral twin likewise) | `{cli}` · no-fact form drops the CLI clause | chat line, parent tool result, parent completion message | S-139, T-56 (fixture drives the real store refusal, not a config error) |
| B16a resume unavailable | `errors.Is(err, errExternalResumeUnavailable)` from whichever site returned it — `reserveExternalConversation` (:366/:371), `beginExternalRun` (:444) or `driverForRun` (:469); not from `startAndDrain` (origin corrected in the post-check) | Yes (refusal before the run) | `delegation_not_resumable` | `ambiguous` | W14 form 1 + neutral twin | `{cli}` (corrected in grill round 2 — both W14 forms require it) · no-fact form drops the CLI clause (defined at W14) | chat line, parent tool result, parent completion message | S-141 (unchanged-workspace control), T-56 |
| B16b workspace changed | `errExternalWorkspaceChanged` — the workspace relationship changed for a continuation | Yes (refusal before the run) | `delegation_not_resumable` (second catalogue variant, selected by the sentinel) | `ambiguous` | W14 form 2 + neutral twin | `{cli}` (corrected in grill round 2) · no-fact form drops the CLI clause (defined at W14) | chat line, parent tool result, parent completion message | S-141, T-56 |
| B17a CLI run timeout | the run exceeded Omnipus's time limit (post-#1306 branch possibly unreachable — Inferred) | Yes | `turn_timed_out` (existing code, CLI form — R-2's typed-exit exception) | existing row's attribution | manifest `M-1857-B17a` (WP-6; the existing copy's CLI variant with `{cli}`) | `{cli}` · no-fact form is the copy | chat line, task/centre body | S-139, T-55 |
| B17b invalid resolved turn cap | `invalidMaxTurnsEvent` — the cap value could not be used | Yes (the run cannot start) | `external_cli_invalid_config` | `config` | manifest `M-1857-B17b` (WP-6; points at the agent's runner settings) | `{cli}` · no-fact form is the copy | chat line, Settings test (the cap is resolved for the test too) | S-139, T-56 |
| B19 continuing record failure | a started tool's receipt (`role: "tool"`) save failed; the tool keeps running — restricted to the continuing caller in grill round 2; **qualified in the post-check**: the shared reporter sets the turn-failed flag (`pkg/agent/turn_exit.go::reportSessionSaveFailure` → `ts.markTurnFailed()`), so the done frame reports failed while the tool runs | **No** — the tool's execution is not stopped (verified: `pkg/agent/started_tool_outcome.go::beginStartedTool` reports, then returns `true`, and its caller `loop_run_turn_tools.go:1467` proceeds) — but the turn is flagged failed on the done frame | `transcript_save_failed` (continuing variant) | `ambiguous` | W16 + neutral twin (the WP-6 wording review reconciles "the work continues" with the failed flag) | none · no-fact form is the copy | in-chat system line | S-140, T-55 (asserts the tool still runs **and** the failed flag) |
| B20 gateway late save failure | the gateway could not persist the exchange after streaming — and the failure **fails the turn** (state corrected in grill round 2, R2-MAJ-001: `Finalize` returns the error; `publishDone` sets `TurnFailed`) | **Yes** — the turn reports failed; the untyped notice text is not a post-success annotation | none (untyped frame) | — | manifest `M-1857-B20` (WP-6 rewrite of `agent.MessageNotSavedReply`; removes "Sorry"/"please" and states both facts — the exchange may be missing **and** the turn reports failed) | none | in-chat error frame + the done frame's failed state | S-140 (asserts the TurnFailed outcome as well as the text) |
| B21 finalization save failure without answer evidence | the direct `Sessions.Save` failure of `finalizeTurn` (`loop_run_turn.go:92-107` — shared by stopped-silent, truncated-empty, default-response and answered turns; emits via `TranslateLLMError(nil, err.Error())`, not the shared reporter, so it carries **no answer-state fact**: answer existence unknown to the server) **or** the failed `recordIterationLimitNotice` branch (:137-144, reporter called, the final content was the limit notice — no answer exists, decided by the caller). The stopped-silent branch itself calls nothing (:85-86) — the round-2 reporter claim is withdrawn (post-check) | Yes — the turn reports failed on both producers (`TurnEndStatusError`); what answer existed is unknown (direct Save) or none (iteration limit) | `transcript_save_failed` (finalization variant — no "answered" claim) | `ambiguous` | manifest `M-1857-B21` (WP-6; proposed text: chat `I couldn't save the record of this turn, which ended without a final reply. Check what already ran before deciding to repeat anything.` · neutral: `The record of this turn, which ended without a final reply, could not be saved. Check what already ran before deciding to repeat anything.` — never "answered", never a blind re-run: earlier tool rounds may already have run, matrix rule 5) | none · no-fact form is the copy | chat line; task/centre body for task runs | S-140 (the direct-Save fixture asserts W15 is **not** shown), T-55 |
| B22 active-turn goal-read refusal | `refuseUnreadGoalState` refused an active request while `prepareToolSurface` built its provider request — failed goal-forcing read or failed compressed tool-surface construction (new row, grill round 2, R2-MAJ-002) | **Yes** — the active request stops: `TurnEndStatusError`, an error result is returned, and request preparation ends **before the provider call** | `goal_state_unavailable` (active-turn terminal variant) | `ambiguous` | manifest `M-1857-B22` (WP-6; proposed text: chat `I stopped this request: I couldn't read the goal state it needs. Check what this turn already did before trying again.` · neutral: `This request stopped: the goal state it needs could not be read. Check what the turn already did before trying again.` — earlier iterations may already have run tools, so no blind re-run is invited, matrix rule 5) | none · no-fact form is the copy | chat line | S-139, T-55 (caller-driven fixture through `prepareToolSurface` asserting no provider call and the stopped outcome) |
| B23 terminal continuation-save failure | an intermediate assistant continuation's save failed (`recordToolCalls` wraps it, `dispatchToolCalls` sets `TurnEndStatusError` and returns **before `executeToolCalls`** — no new tool round is admitted; regression proof `TestWU22FlushContinuationArchiveFailureStopsToolRound`) (new row, grill round 2, R2-MAJ-001) | **Yes** — the turn stops; W16's "the work continues" is false here and must never show | `transcript_save_failed` (terminal continuation variant) | `ambiguous` | manifest `M-1857-B23` (WP-6; proposed text: chat `I stopped this turn: I couldn't save the record of its last step, so I didn't start the next one. Earlier steps did run; check what was done before deciding to repeat anything.` · neutral: `This turn stopped: the record of its last step could not be saved, so the next step was not started. Earlier steps did run; check what was done before deciding to repeat anything.` — a prior tool round has already run, so the copy never invites a blind re-run, matrix rule 5) | none · no-fact form is the copy | chat line; task/centre body for task runs | S-140 (asserts `TurnEndStatusError` and that no new tool round runs), T-55 |
| B24 remaining record-save refusals | the six `messageNotSavedError` producers this amendment does not re-key (added by the post-check): final streamer ensure (`loop_run_turn.go:125-130`, assistant), dedup synthetic-deny save (`:944-948`, system), cancel-marker append (`cancel.go:698-699`, system), cancelled tool receipt (`started_tool_outcome.go:90-92`, tool), sub-turn result save (`loop_run_turn_tools.go:2254-2257`, user), saved wake input placement (`loop_window.go:73`, user) | Yes on the terminal sites; the cancel-path sites end on their own cancel exits | none — **not re-keyed by this amendment** | — | **stays generic today**, deliberately: these are the named residual emitters of the T-54 allow-list (S-138/FR-092), each listed with its reason so none hides; re-keying is left to a tracked follow-up | — | the generic line (per site) | T-54 (the allow-list fixture asserts each named site) |
| B25 turn-settlement storage failure (founder sec2 answer, 2026-11-18) | `finishExecutionDisposition` fails after an ordinary wake's execution — `pkg/agent/execution_disposition.go::reportOrdinarySettlementFailure` emits `EventKindError` with `Code: "settlement_failed"` and, today, `Message: "This turn finished, but saving the conversation's final state failed. Repair the gateway storage, then continue: " + err.Error()`; callers `pkg/agent/loop_inbound.go::finishOrdinaryWakeExecution` and the deferred call in `pkg/agent/session_worker.go` (both run at settlement, after the execution) | Unknown — not claimed: this row fixes the raw text; the callers read run after the execution, and no turn-stop/continuation claim is made | `transcript_save_failed` (settlement catalogue variant — re-keyed, **no new code**; the internal `settlement_failed` retires with the fix) | `ambiguous` | manifest `M-1857-B25` (WP-6; both voices from the approved pattern — the fixed safe sentence states that saving the conversation's final state failed; **never the raw error text** — founder sec2 A, fail-closed copy rule) | none · no-fact form is the code copy | chat error frame (via `hubError` → `WireLLMError`); gateway.log keeps the raw text | S-139, T-55 (fixture drives the real reporter: the frame's message equals the fixed sentence and carries no substring of the raw error; the log call keeps it) |
| B26 goal-read failure with history persistence (founder sec2 answer, 2026-11-18) | `pkg/agent/goal_record_error.go::reportGoalReadError` builds `"Goal state could not be read; %s deferred: %v"` with the **raw error**, persists it into the chat transcript (`pkg/agent/goal_loop.go::writeGoalSystemTranscript`, `::writeGoalSystemTranscriptErr`) and emits a `CodeUnknown` event with the same message; the refusal carrier `::refuseUnreadGoalState` sets the failed turn's final content to the same raw-text message today — both carriers are fixed by this row | B14's continuation binding applies to the background paths (unchanged); B22's refusal stays terminal there — this row changes neither outcome, only the raw text | `goal_state_unavailable` (consequence variant — already proposed for B14/B22, **no new code**) | `ambiguous` | manifest `M-1857-B26` (WP-6; the fixed safe sentence — the goal state could not be read and the deferred work waits; **never the raw error text** — founder sec2 A) | none · no-fact form is the code copy | chat history (the transcript line — the rendered surface), the goal loop's error event; gateway.log keeps the raw text | S-139, T-55 (fixture drives the real reporter: the persisted transcript line and the event message contain no substring of the raw error; the log keeps it) |
| C8 program path refusal | the configured program path is missing or failing its check | Yes (refusal before the run) | `external_cli_not_found` | `config` | manifest `M-1857-C8` (WP-6; both voices) | `{cli}` · no-fact form is the copy | chat line, Settings test | S-139, T-55, T-56 |
| C11 nonfatal diagnostic | a driver error event with `Fatal: false` (e.g. OpenCode malformed-JSON), run continues and may succeed | **No** — explicitly non-terminal on the normal run | none — no code, no failure line **and the emitted/persisted diagnostic line is removed** (baseline corrected in grill round 2, R2-MAJ-005: today the event is emitted before the `Fatal` test, so a successful run still leaves an error line) | — | **normal run**: none — no user-facing sentence exists or is added; the classified kind is recorded for gateway.log only. **Settings — pinned now**: the drain's stricter rule is retained (`drainSmokeTestRun` returns `ok: false` on every error event; `buildResponse` publishes it); the test-level copy (manifest `M-1857-C11-settings`, WP-6) states truthfully that `{cli}` produced output the test could not read — proposed text: `The test did not pass: {cli} produced output that could not be read. Run the test again; if it keeps happening, check that {cli} works on its own.` — and never claims the diagnostic proves a failed CLI run | `{cli}` (Settings copy) | gateway.log only on the normal run; the Settings result stays a failed test | S-142 (normal-run control asserting the removal; Settings fixture asserting `ok: false`, the truthful copy and absence of a failed-run claim); T-56(g) |

Matrix rules: (1) only the assembly function may set `message_assembled` — a producer never attaches free text to earn the flag; (2) a WP-6 row's sentence is written from its manifest id against the approved pattern and its acceptance oracle before the tests that assert it are written; (3) per-code variant/no-fact forms are contract fixtures (the contract/variant-parity dataset of §19.1); (4) state (terminal vs non-terminal, continuing vs stopped, answered vs not) is bound to the **exact failed operation and its caller's outcome** — the shared reporting helper (`reportSessionSaveFailure`) proves nothing about the caller: `beginStartedTool` returns `true` and continues (B19), `recordToolCalls` returns an error that `dispatchToolCalls` turns into `TurnEndStatusError` before the next tool round (B23), and the late gateway save failure fails the turn (B20) — never inferred from the error type or the helper alone (grill round 2, R2-MAJ-001/002); (5) every `deny`-of-rerun rule follows W15's principle: a reporting or save failure is not proof that repeating the work is safe (`docs/tools.md::Background results`).

---

## 14. Connection handling and logging (R-LOG-1…4, R-EV-2; D13, D32, D44, D64, D72, D73, D74)

### 14.1 Network vs API classification (D72 — FQ-7 = network)

One pure function in `pkg/providers` maps a failed, **dispatched** provider attempt to `(failure_class, reason)`. It is independent of `providers.ClassifyError` — which returns nil for several inputs and drives retry/fallback (`pkg/providers/error_classifier.go`, `pkg/providers/fallback.go::FallbackChain.Execute`) — and nothing in retry, cooldown or failover reads its result (FR-063).

| Observation on a dispatched provider attempt | Class | `reason` |
|---|---|---|
| DNS failure (`no such host`) | network | `dns_failure` |
| `connection refused` | network | `connection_refused` |
| `network is unreachable` | network | `network_unreachable` |
| TLS handshake or certificate failure | network | `tls_failure` |
| dial or response-header timeout (`i/o timeout`, `dial tcp … timeout`, the turn deadline before any response) | network | `timed_out` |
| connection dropped before response headers (`connection reset by peer`, `broken pipe`, `unexpected EOF`) | network | `connection_dropped` |
| connection dropped **after** a 200 started streaming (reset, EOF, `http2: server sent GOAWAY`, `stream error:`) | network (D72) | `connection_dropped` |
| silence-limit stall after a 200 (`provider_stalled`) | network (D72) | `timed_out` |
| any HTTP status ≥ 400 received (incl. 408, 429, 5xx, 529) | api | `http_status` |
| provider error event inside a 200 stream (`common.StreamProviderError`) | api | `http_status` (the event's status when given) |
| 200 whose body cannot be parsed as the expected response | api | `invalid_response` (new) |
| any other failure of a dispatched request | unknown | `unknown` |

Not observed (no class, no log line, no incident): every failure returned before the provider call starts (§14.2) — a candidate skipped by cooldown, a route error before dispatch (`routeErr`), Omnipus's own rate limiter, an inspection-image attachment failure (`attachTurnInspectionImagesWithBudget` in `runProviderAttempt`), and the request-build refusals `callProviderOnce` returns before calling the provider (the call-accounting error, the context-window checks) — and context cancellation (Stop, shutdown).

**Kind per class** (D71 — FQ-6 in review-revised form): network → `provider_unreachable`; api with code `quota_billing` → `provider_quota_exhausted`; api with a provider 429 → `provider_rate_limited`; api with 401/403 at account level, 408, 5xx or 529 → `provider_error`; model-specific API conditions (`model_retired`, `bad_model`, `context_too_long`, `content_policy`, `request_too_large`, `media_unsupported`, a 400 `context_length_exceeded`) stay chat-only and raise no incident. `unknown` raises no incident.

**Kind and code are separate (round-2 R2-MAJ-013).** The chat error **code** decides the chat line and its action; the incident **kind** decides the centre entry. An authentication rejection (401, or 403 for a bad key) keeps chat code `provider_auth_failed` with its own remedy (Update the key; **Open settings**) and, being account-level, raises the `provider_error` incident (D71). 408, 5xx and 529 are chat code `provider_error` and raise the same kind.

**Outside this table (#1857 amendment).** Failure kinds that do not come from a dispatched provider attempt — the external CLI worker kinds (C1–C11) and the pre-turn and loop kinds (B1–B26, the two sec2 rows included) — are listed with their codes, their generic-line status and their proposed wording in §13.8; this section's classification is unchanged by that amendment. The residual **dispatched** cases (A4/A5) are listed in §13.8 too, but only their *terminal presentation* is narrowed there (R-4): this section's classification still applies to them. A6 (the wait budget) produces no own line — the turn ends on the last attempt's classified error.

### 14.2 Per-attempt observation (round-1 MAJ-009; round-2 R2-MAJ-008)

The single owner is the existing seam **`agentLoopRunTurn.accountProviderAttempt`** (`pkg/agent/prompt_cache_accounting.go`: it "owns accounting at the real call boundary, before a retry/fallback can replace the failed candidate's identity"). It is reached from two callers that differ: `runProviderAttempt` (`pkg/agent/loop_run_turn_attempt.go`) installs its deferred accounting **before** attaching inspection images and returns an attachment error before `Chat`/`ChatStream`; `callProviderOnce` (`pkg/agent/loop_run_turn.go`) returns its pre-call refusals before its accounting defer and accounts after `p.Chat`.

So the observation is guarded by an explicit **call-started fact**: each caller sets a per-attempt `callStarted` flag immediately before it calls the provider (`Chat` / `ChatStream` in `runProviderAttempt`, `p.Chat` in `callProviderOnce`) and passes it to `accountProviderAttempt`; the observation runs only when the flag is set. Existing prompt-cache accounting is unchanged, and `providers.ClassifyError` is not touched. Every dispatched request of a turn is therefore seen exactly once, whatever retry layer issued it, and nothing that failed before the call is seen at all. Stated limit: a failure inside a provider adapter before any byte is sent (for example a request the adapter cannot encode) still counts as started; the classifier gives it `unknown` (no HTTP status, no network error), so it writes the WARN line and raises no incident (T-38 fixture).

For every failed observed attempt it writes **one WARN line** "provider attempt failed" with: `failure_class`, `reason`, `provider`, `model`, `host`, `http_status` (api), `provider_says` (masked, ≤ 500 bytes), `attempt_seq` (1, 2, 3 … per turn across all layers), `candidate` (index in the fallback chain), `retry_round`, `session_id`, `turn_id`, `request_id`. There is **no** `next` field: what happened next is the next line (another attempt, a fallback) or the existing ERROR "LLM call failed", which gains `failure_class`. WARN because the shipped default level is `warn` (§3.1 C13). Successful attempts write nothing to the log but feed the incident tracker (resolution).

Provider calls made outside an agent turn (if any) are not observed in Part 1; the test author's T-38 enumerates the callers of the provider chat interface so any such path is named, not missed (§31.1).

### 14.3 Incident tracker — one state machine (round-1 MAJ-010)

In memory, one per gateway process, keyed by **(kind, provider)** for provider kinds and by **resource** for `resources_low` / `concurrency_reduced`.

| From | Event | To | Effects |
|---|---|---|---|
| closed | first failure of kind K at provider P (§14.1) | open | router creates the K entry (pop-up per preferences); WARN "provider incident started" (kind, provider, reason) |
| open | another failure at P (any session) | open | entry updated (`affected_chat_count`, last reason); no pop-up |
| open | any successful attempt at P (any model) | resolved | entry resolved; `connection_restored` entry created (info, auto-handled; pop-up per preferences); WARN "provider incident ended" (duration, attempts, affected chat count). Chat lines are **not** touched here: each turn resolves its own line and writes its own record (§11.3, round-2 R2-MAJ-010) |
| resolved | failure of kind K at P within 10 min of resolving | open (re-opened) | the **same** entry re-opened (`updated`, unread, no pop-up); the restored entry stays — nothing is retracted |
| resolved | failure of kind K at P after 10 min | open (new episode) | a new entry, pop-up per preferences |
| any | gateway restart | — | at start-up, every unresolved provider/resource entry in the store seeds the tracker as `open` (no pop-up, no new entry); it resolves on the next success. Resolution is emitted even if the recipient has since switched Notify off for that kind (the entry exists, so it must not stay open) |

`provider_connections_failing` is derived: open while ≥ 2 distinct providers have an open `provider_unreachable` incident (one entry; `causes` lists up to 10 providers with their reasons, earliest incident first, and `causes_omitted` counts the rest — round-2 R2-MAJ-004); resolved — with a `connection_restored` entry "Connections to AI providers restored" — when fewer than 2 remain. No probe traffic is ever sent (D64).

Resources (measured hosts only): memory opens on `config.MemoryPressureHigh` false→true and resolves on true→false; `concurrency_reduced` opens when `applyMemoryCap` becomes the binding limit and resolves when it stops binding (D74). Disk (D73; round-2 R2-MAJ-005): `fileutil.WriteFileAtomic` reports each write's outcome to a **non-blocking observer** — it hands `{path, no_space, ok}` to a bounded in-memory queue (256 events; a full queue drops the event and counts the drop) and returns. No callback runs on the writer's goroutine, so no caller's lock is re-entered and nothing routes recursively; for example `Store.Create` holds `s.mu` through `saveLocked` → `WriteFileAtomic` (`pkg/notifications/store.go`). One gateway goroutine drains the queue outside every lock and feeds the tracker. Only writes under the data folder count, and writes to the notification and preference stores (`<data>/notifications/`) are **excluded in both directions**: they neither open nor resolve the disk incident, so the incident's own bookkeeping can never trigger or clear it. A no-space failure opens `resources:disk` (repeats update the same entry); the next successful observed write resolves it. A dropped event can delay a transition until the next observed write; it cannot invert one. No polling (D73). Hosts without a memory measurement (some Windows and container hosts) never raise the memory kinds; instead **Settings → Performance** shows one permanent line "Memory can't be measured on this host, so at most 2 agents run at once." (the floor `unmeasurableHostAgentFloor` = 2 in `pkg/agent/admission.go`), and no notification (D74).

### 14.4 Browser↔server outage report (R-LOG-3; round-1 MAJ-011)

Client (`src/lib/ws.ts` area, one state machine):
1. On the first unexpected close (not a page unload), record `{report_id: new UUID, kind, started_at: now, last_confirmed_at: time of the last frame or pong received, reconnect_attempts: 0, page_reloaded: false}` in that tab's session storage. `kind` = `offline` when `navigator.onLine` is false at that moment, else `server_unreachable`.
2. Each reconnect attempt increments `reconnect_attempts`; a reload during the outage sets `page_reloaded: true` and keeps the record.
3. On the next successful authentication: set `ended_at: now`, move the record to a pending queue (≤ 5 records; beyond 5 the oldest is dropped and a console line names it), and POST it. The pending queue lives in the tab's session storage, like the active record, so a reload after reconnecting but before the 204 re-sends the same `report_id` (round-2 R2-MIN-003).
4. Retry while connected: 5 s, 30 s, 2 min, then every 10 min. Drop on 204, on 400 (logged to the browser console), or when older than 24 h. On 429, wait `Retry-After` before the next try.

Server:
- Idempotent on (user, `report_id`) **within its bounds**: a repeated id returns 204 and writes nothing new while it is in the LRU (the last 1,000 ids per process, each kept 24 h). Duplicate boundaries (stated): a repeat may be logged a second time after a gateway restart, or after more than 1,000 newer ids evicted it within 24 h.
- Logs WARN "client connection outage" with `user`, `client_outage_kind`, `started_at`, `ended_at`, `observed_duration_ms` (= ended − started), `max_possible_duration_ms` (= ended − last_confirmed), `reconnect_attempts`, `page_reloaded`. No free-text field exists, so nothing user-typed reaches the log.
- Two tabs produce two reports (one per tab); that is expected and documented.
- The existing `ws: connection closed unexpectedly` line moves from Debug to WARN with user and connection age (O4).
- Loss boundary (stated, not hidden): an outage is not reported if the tab is closed before it reconnects or before its report is accepted, if every retry fails for 24 h, or if more than 5 reports are pending (the oldest is dropped). This is a bounded best-effort record, not exactly-once delivery.

**R-LOG-4**: every line that leaves the chat (R-SL-5 at Normal, R-SL-6) has its episode in the log (§14.2, §14.3, this subsection).

---

## 15. Security and privacy (R-SEC-1, R-SEC-2; D16, D23, D28)

### 15.1 Masking contract (round-1 MAJ-006)

One masking function (today's `scrubDetailText` in `pkg/agent/translate_error_provider_messages.go`, extended and exported, or moved to `pkg/logger` — the backend owner's choice) applies, in this order, before persistence and before the wire, to: `provider_says`, technical details, every notification title/body built from external text, every log field carrying provider text, the fallback note and the retry frame text.

| Rule | Masks | Replacement |
|---|---|---|
| M1 | every registered credential value (today's `logger.ScrubSensitiveValues`) — on the **whole response before any selection** (D81), then on the selected text **and** on the normalised text (§13.3) | `[redacted]` |
| M2 | a registered value split by whitespace or a line break (the value is matched with any whitespace run between its characters) — both halves reach the masker together because the whole response is masked before a line is selected (D81, §13.3) | `[redacted]` |
| M3 | authentication carriers: `Authorization:` / `Proxy-Authorization:` header values, `Bearer <token>`, `Basic <token>`, and the values of JSON or query keys named `api_key`, `apikey`, `key`, `token`, `access_token`, `refresh_token`, `secret`, `password`, `x-api-key` | the value → `[redacted]` |
| M4 | key-shaped tokens: the existing `sk-[A-Za-z0-9][A-Za-z0-9_-]{4,}` (`keyShapedToken`) plus JWT shape (`eyJ` + base64url `.` base64url `.` base64url) | `[redacted]` |
| M5 | **partial echoes anchored on a known key prefix**: one of `sk-`, `sk-ant-`, `sk-or-`, `sk-proj-`, `xai-`, `gsk_`, `AIza`, `ghp_`, `github_pat_`, `hf_` followed by **≥ 1** further character from `[A-Za-z0-9_*.…-]` (catches `sk-…xyz`, `sk-****abcd`, `sk-ab`) | the whole token → `[redacted]` |
| M6 | long opaque runs: ≥ 24 consecutive characters from `[A-Za-z0-9_-]` mixing letters and digits | `[redacted]` |

**Stated limit — accepted and documented (D82; founder answer OQ-1 = A, 2026-11-05).** Masking recognises **known key shapes only**: registered values (also split by whitespace), authentication carriers, key-shaped tokens and JWTs, partial echoes anchored on a known prefix, and long opaque runs (M1–M6). A short fragment with no known prefix and fewer than 24 characters (e.g. `…a1b2c3`) cannot be told apart from ordinary text and is **not masked**: it is shown as the provider sent it. This is the one accepted exception to R-SEC-1's "partial key echoes". It is stated for users on the security page (DOC-12, a user-facing "limits" passage) and pinned by DS-SEC row 13, so any change to it is visible. It is a different case from D81: a registered value the provider returned whole but split across lines is masked (DS-SEC row 15); only a fragment that arrives without its known shape is not. It replaces the prior provider-messages spec's D5 (which accepted residual fragments in the raw Verbose view): from this feature on, the same contract applies to every surface and level, and the residual is only the class above (§28).

Side effect accepted: M6 may mask a long opaque id inside provider text (e.g. a request id quoted in a message); request ids remain visible in their own `request_id` field at Verbose/Debug.

When masking fails (panic, invalid UTF-8 that cannot be repaired), the text is **dropped**, not shown: the no-quote sentence is used and one ERROR is logged without the text.

### 15.2 Recipient file names (round-1 MAJ-007)

Every per-user file is named `u_<lowercase hex SHA-256 of the exact username bytes>.json` (notifications) and `u_<same>.prefs.json` (preferences) — for every username, whether or not sanitising would change it. The file stores the exact username, and a read verifies it (a mismatch is treated as unreadable, quarantined and logged). This is collision-free in practice, case-sensitive by construction (it does not depend on a case-insensitive file system) and free of reserved names and length limits. Greenfield: the old `sanitize`-based files (`pkg/notifications/store.go::userFile`) are not migrated; existing entries are not carried over (§31.1).

### 15.3 Locks (round-1 MIN-005)

Every read-modify-write of a recipient's files (create, coalesce, read, mark read, resolve, delete, clear, sweep, preferences update) runs under the store's process mutex **and** the cross-process sidecar lock of the file-store family: `fileutil.WithFlock` on `fileutil.SidecarLockPath(<file>)` (`pkg/fileutil/sidecar_lock.go`). On Linux and macOS this excludes a second process. On Windows `WithFlock` is in-process only and warns (`pkg/fileutil/flock_windows.go`); the single-gateway-process design makes that sufficient in normal operation, and the limitation is documented in §31.1 and listed as a residual rider for the chief (§1.5).

### 15.4 Other rules

| Topic | Rule |
|---|---|
| Debug | Available to every user (D16); shows nothing unmasked; shows only data that already exists (D66) |
| Schedule-failure body | No longer the raw run error: the translated sentence plus masked `provider_says`; the raw error stays in the log only |
| Retention | §8.5; quarantined corrupt files are deleted after 30 days |
| Outage endpoint | Authenticated (bearer), rate-limited (10/min/user → 429 with `Retry-After`), body ≤ 4 KiB, enumerated fields only, `additionalProperties: false` |
| Preferences endpoint | Authenticated; a user reads and writes only their own record; workspace ids are checked against existing workspaces (400 `unknown_workspace`) |
| Notification frames | Delivered only to the recipient's own connections (unchanged forwarder filter) |
| Navigation | A carried id never grants access; the destination loads its object through its normal authenticated call (§7.6) |
| Legacy channel alert | Unchanged in Part 1 (FR-090); its body is today's raw run error — recorded as accidental finding AF-6 (§31.3), not fixed here |
| Out of scope (recorded) | Synthetic identities (`_dev_bypass`, `_env_token`, `cli`) as recipients — unchanged; AF-4 |

security-lead reviews `pkg/notifications` (storage, deletion, file names, locks), the masking change and the new endpoints in the 5-reviewer gate (focus areas: gateway auth and rate limiting).

### 15.5 STRIDE summary (spoofing, tampering, repudiation, information disclosure, denial of service, elevation of privilege)

| Component | Threat | Design answer | Test |
|---|---|---|---|
| Recipient resolution | S/E — an agent's name in task attribution routes a notification to a person, or a person receives another's | recipients never come from attribution text (§8.1 R1–R3); session owner is authentication-stamped | T-22, T-24, T-39 |
| Per-user files | T/I — two usernames share a file | SHA-256 names + stored-username check (§15.2) | T-08 |
| Provider text | I — partial key echo reaches screen, store or log | M1–M6 before persistence and wire (§15.1); the whole response is masked before a line is selected, so a split value is masked whole (D81, §13.3); short fragments without a known prefix are the documented limit (D82) | T-12, T-13, T-30, T-51 |
| Provider text | T — HTML/Markdown injection via provider message | plain text rendering, control characters stripped (§13.3) | T-12, T-18 |
| Lifecycle | T — a stale snapshot, a late upsert or a late delete shows the wrong state | one ordering rule over two counters, tombstones and snapshot floors (§8.4.3) | T-20, T-40 |
| Lifecycle | D — more than 1,000 removals in one operation | one store change in independent batches of ≤ 1,000 ids (§7.2) | T-06, T-48 |
| Disk observation | D — the observer re-enters the store lock or feeds on its own writes | non-blocking queue drained outside every lock; notification and preference store writes excluded (§14.3) | T-24, T-46 |
| Error display | T/I — unassembled or internal text shown as the main line | only the server's assembly sets `message_assembled` (§11.4) | T-45 |
| Lifecycle | D — more than 50 waiting items | protected items never evicted; age rule bounds the file (§8.5) | T-05 |
| Outage endpoint | D/R — flood, duplicates, forged durations | auth, 10/min, 4 KiB, idempotent id, server logs both durations and treats client times as a report, not proof (§14.4) | T-27, T-41 |
| Preferences | T/E — stale write overwrites another tab, writes into an unknown workspace | `base_revision`, 409, workspace check (§9.2) | T-22, T-42 |
| Navigation and line actions | E/I — a carried id opens an object the user may not see | destination re-loads through authenticated REST; 404 → "no longer exists" (§7.6) | T-43 |
| External channels | — | Part 2 only; Part 1 sends nothing new (FR-090) | T-44 |

---

## 16. Removals and the replacement-function journeys (R-RM-1, R-RM-1a, R-RM-2, R-RM-3; D31, D34, D37, D46, D77, D78)

| Item | Action |
|---|---|
| Own-action pop-ups (WP-5) | Work list = the audit records `toast-audit/result-w1..w4.jsonl` (322 rows) **plus the 6 unaudited sites** (§3.1 C2), re-audited with the same fields before editing; any call site added to the tree since the audit is audited the same way first (a WP-5 step: `grep` for every `addToast` call and compare with the list). Per recommendation: *remove (page already shows it)* 98 → delete the call; *remove + add page indicator* 107 → add the indicator first (reuse `AutoSaveIndicator`, the agent-profile "Save not confirmed + Retry" pattern, error cards with Retry), then delete; *… with Retry* 87 → same with Retry; *move to chat system line* 8 → `SystemLine` (those citing `ToolApprovalModal` vanish with it); *keep as event pop-up* 10 → page-level indicators (§10.2, D77); *delete* 3; *dev-only* 2 → unchanged; *needs founder decision* 7 → homes per D78 (below). Removal only where the page shows the outcome (D46) |
| Event-raised modals (R-RM-2) | Delete `src/components/agents/ToolApprovalModal.tsx`, its tests and stories, and `INLINE_TOOL_APPROVALS_ENABLED` (always true). A test asserts no component opens a dialog from a WebSocket frame handler |
| Top-right badges (R-RM-3) | None exist at `4cad412bf`: the only badge is the sidebar unread count; the God Mode corner dot is top-left (`GodModeIndicators.tsx`, `absolute left-0`). A UAT row confirms visually, also at 320 px (§24 U14) |
| Superseded "Verbose chat" | The switch, its store field and every "Verbose chat" mention in UI copy are replaced by "Detail level" (no alias, no deprecation comment) |

**Homes for the seven founder-decision sites (D78 — FQ-15/Q7 in review-revised form).** No pop-up is removed until its home exists and is tested:

| Audit row (`file::symbol`) | Function today | Home |
|---|---|---|
| w2-055 `src/components/screens/CalendarScreen.tsx::CalendarScreen::handleEventDrop` | "Rescheduled to …" with Undo | a stable **result area** at the top of the Calendar page: "Rescheduled "{event}" to {time}." with **Undo** for 10 s, independent of whether the event is still in view |
| w2-048 `src/components/workspaces/team/useTeamMembership.ts::useTeamMembership::write` | "Added {agent} to {team}" with Undo | the same result-area pattern at the top of the team page: "Added {agent} to {team}." with **Undo** for 10 s, also when the added row is out of view |
| w3-010 `src/components/chat/IframePreview.tsx::IframePreview::handleCopy` | "Link copied. Anyone with this link can view the preview until it expires." | permanent helper text under the **Copy link** button stating who can open the link and until when; the button's own label changes to "Copied" for 2 s |
| w4-001, w4-002, w4-003 `src/components/panel-shell/panelTabSwitch.ts` | "{panel} is already open in another tab — switch" | a banner in the panel area of the tab that tried to open the panel: "{panel} is open in another tab." with **Switch**. It stays until **Switch** succeeds or the user closes the panel in this tab; when focusing the other tab fails, it stays and adds the manual-switch sentence |
| w2-041 `src/components/workspaces/CreateTaskSlideOver.tsx::CreateTaskSlideOver::warnIfAssigneeCannotFinish` | assignee warning after save | a persistent warning on the task card and in the task detail until the assignment changes |
| w4-088 `src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` | a status frame's message | a Verbose `SystemLine` (info) in that chat |

The seven audit rows marked "needs founder decision" are w2-041, w2-055, w3-010, w4-001, w4-002, w4-003 and w4-088; w2-048 is marked "remove (page already shows it)" in the audit, but its Undo has no page home today, so D78 gives it the same result area (round-1 MAJ-016).

**Journeys** (each one a UAT row in §24 and an E2E path in T-36):
1. Move a calendar event, scroll it out of view, then press **Undo** in the result area → the event is back at its old time; no pop-up appeared.
2. A save fails on any audited page → the page shows "Not saved — Retry"; **Retry** succeeds → "Saved"; no pop-up at any step.
3. Open a panel already open in another tab → the banner appears; **Switch** focuses the other tab (or the manual sentence shows).
4. Copy a preview link → the helper text is visible before and after copying.

---

## 16A. Accessibility, keyboard, announcements and narrow screens (round-1 MAJ-017)

| Surface | Keyboard | Focus | Announcements | Narrow / zoom |
|---|---|---|---|---|
| Centre sheet | Opens from the account-menu entry with Enter/Space; Tab order: filter → Mark all read → Clear all → rows (primary, then Delete) | trapped while open; Escape closes; focus returns to the opener; repaired when a row disappears (§8.6) | new live entries once (polite); hydration never | full width below 480 px; no horizontal scroll at 320 px |
| Filter | `segmented-control`: arrow keys move, the selected option is announced with its count | — | — | wraps to two rows if needed |
| Clear all | `confirm-dialog`: focus on the safe action first; Escape cancels | returns to Clear all | — | — |
| Pop-ups | reachable in the normal Tab order (host after main content); Escape dismisses the focused pop-up; the action is a real button | when the focused pop-up is removed, focus goes to the next pop-up, else back to where it was before entering the host | polite region; error and needs_you as `role="alert"`; once per pop-up | stacked at full width below 480 px; ≤ 3 still applies |
| System lines | the action and the details disclosure are Tab stops | — | once on appear, once per new attempt; countdown never; history never | wraps; action stays visible (§11.1) |
| Preferences | each `switch` / `segmented-control` labelled with the kind name and the switch name; locked switches are focusable, `aria-disabled`, with the lock explanation as their description | errors (`FormError`) are linked to their control | save status in a polite region (`AutoSaveIndicator`) | single column at 320 px |
| Page indicators (§10.2) and result areas (§16) | the action is a Tab stop | — | announced once when they appear (polite) | wrap |

Motion: every animation (pop-up slide, in-progress pulse, ellipsis) is static under `prefers-reduced-motion: reduce`. Colour is never the only carrier of a line's type: the icon and the visually hidden prefix (§11.1) carry it too.

---

## 17. Reachability (Definition of Done)

| Part | Who reaches it | Path |
|---|---|---|
| Notification centre + filters + delete + clear | user | sidebar → account menu → **Notifications** (existing entry) |
| Pop-ups | user | automatic, bottom-right, every screen (`AppShell` host) |
| Page-level indicators (§10.2) and result areas (§16) | user | on the page where the condition or action happened |
| Preferences (global) | user | **Settings → Notifications** (new tab in `SettingsScreen`) |
| Preferences (workspace) | user | workspace → **Settings** tab → "Notifications for this workspace" |
| Detail level | user | **Settings → Chat → Detail level** |
| System lines | user | any chat, live and after reload / history paging |
| Browser outage indicator | user | app-wide banner in `AppShell` + open chat |
| Network/API logs, incident lines, outage lines | operator | gateway log at the default level (`warn`) |
| Outage report | browser → server | automatic on reconnect, retried until accepted; visible in the gateway log |
| Each event kind | producer → centre | §8.1 source map; reachability test: one integration test per kind drives the **real** producer named in §8.1 (Calendar trigger path for Calendar tasks, gateway runner for heartbeats/API schedules, run lifecycle for tasks, the plan engine for plans) and reads `GET /notifications` (FR-002, T-24) |
| User documentation | user | the pages of §25, reachable from `docs/README.md` |
| Agents | — | No new agent tool: notifications are a user surface. Agents keep reporting in their own chats (D51). Part 1 adds no agent-managed property, so no tool-parity row is needed (OBS-002); Part 2 needs a parity table for channel-facing settings (§29). Hard Constraint #6 untouched |

---

## 18. BDD scenarios

> Ids `S-xx` are used by the TDD plan and the traceability matrix. One action per When. Round 1 keeps every round-0 id whose meaning holds and adds new ids (S-09, S-15…S-19, S-26…S-29, S-37…S-39, S-47…S-49, S-57…S-59, S-63…S-66, S-73…S-78, S-86…S-89, S-91…S-94, S-98…S-104, S-106…S-121, S-123; S-105 and S-122 are unused); S-23 and S-24 changed meaning with D80. Round 2 adds S-124…S-132 and extends S-07, S-14, S-29, S-50, S-51, S-53, S-55, S-86, S-90, S-92, S-100…S-103, S-107, S-109, S-112 and S-114 (§34).

### Feature: notification centre and routing

#### S-01: Opted-in kind creates one centre entry per recipient
**Traces to**: US-1 AC-1 · **Category**: Happy Path
- **Given** fresh-install defaults, accounts `dana` and `lee`, and Calendar task "Nightly report" with a recurring trigger
- **When** its scheduled occurrence fails with a provider 402 "Insufficient balance" (dispatched by the Calendar trigger, run kind scheduled)
- **Then** each account's centre has one `schedule_failed` entry, category Tasks & plans, title `Scheduled task "Nightly report" failed`, target `task` with `run_id`
- **And** its body contains no raw error text and the provider message appears masked
- **And** no `task_failed` entry exists for that run

#### S-02: Default-off kind creates nothing
**Traces to**: US-1 AC-2 · **Category**: Alternate Path
- **Given** fresh-install defaults
- **When** a non-scheduled task moves to in progress
- **Then** no centre entry and no pop-up exist for `task_started`

#### S-03: Category filter
**Traces to**: US-1 AC-3 · **Category**: Happy Path
- **Given** one entry per category
- **When** the user selects the **Needs you** filter
- **Then** exactly the Needs-you entry is listed and the chip shows count 1

#### S-04: System-wide event appears once, lines in affected chats only
**Traces to**: US-1 AC-4 · **Category**: Happy Path
- **Given** chats A, B, C replying through provider P and chat D idle
- **When** P becomes unreachable
- **Then** the centre has one `provider_unreachable` entry for P with `affected_chat_count` 3
- **And** chats A, B, C each show one Warning line and chat D shows none

#### S-05: Every entry navigates to its target, with the ids it needs
**Traces to**: US-1 AC-5 · **Category**: Happy Path
- **Given** one entry of every Part-1 kind in §6, each with a target per §7.6
- **When** the user activates each entry
- **Then** the app opens the screen named by `target.screen` showing the object named by its required ids
- **And** no entry has a missing screen or a missing required id (DS-TARGET)

#### S-06: Unknown kind is rejected at the producer
**Traces to**: US-1 AC-1 · **Category**: Error Path
- **Given** a producer call with a kind not in the contract enum
- **When** the router is called
- **Then** the call returns an error and nothing is stored or pushed, and the error is logged

#### S-07: Store write fails — live delivery still happens
**Traces to**: US-1 AC-1 · **Category**: Error Path
- **Given** the notifications folder is not writable
- **When** a `task_failed` event occurs
- **Then** the open tab receives the entry with a non-empty id, `persisted: false` and a pop-up
- **And** the log has one ERROR line naming the recipient and kind
- **And** the entry shows "Not saved — lost if the server restarts" and is still listed after a page reload while the server keeps running

#### S-08: Unreadable owner never broadcasts
**Traces to**: US-1 AC-1 · **Category**: Error Path
- **Given** a session whose metadata cannot be read
- **When** an approval is requested in it
- **Then** no account receives a notification and an ERROR line is logged

### Feature: resolve everywhere

#### S-10: Approval handled in chat clears pop-up and entry in every tab
**Traces to**: US-2 AC-1 · **Category**: Happy Path
- **Given** tabs T1 and T2 show the approval pop-up and entry
- **When** the user approves in the chat in T1
- **Then** within 2 s both tabs remove the pop-up and show the entry handled

#### S-11: Read in one tab, read in all
**Traces to**: US-2 AC-2 · **Category**: Happy Path
- **Given** an unread entry in T1 and T2
- **When** the user opens it in T1
- **Then** T2 shows it read and the badge drops by 1

#### S-12: Server-side expiry resolves
**Traces to**: US-2 AC-3 · **Category**: Alternate Path
- **Given** an approval with a short expiry
- **When** it expires
- **Then** its entry is handled and its pop-up removed without user action

#### S-13: Outline — every resolve trigger
**Traces to**: US-2 AC-3 · **Category**: Alternate Path
- **Given** an unresolved entry of `<kind>`
- **When** `<trigger>` happens
- **Then** the entry is handled everywhere

| kind | trigger |
|---|---|
| question_asked | the card is answered in another tab |
| question_asked | the card is cancelled by Stop |
| schedule_failed | the next run of the same Calendar task succeeds |
| schedule_failed | the next run of the same gateway job succeeds |
| task_failed | a later run of the same task reaches done |
| provider_unreachable | any later attempt at that provider succeeds, with any model |
| provider_connections_failing | fewer than 2 providers remain unreachable |
| resources_low (memory) | memory pressure ends |
| resources_low (disk) | the next data-folder write succeeds |
| goal_paused | the goal resumes |

#### S-14: Refetch keeps volatile entries
**Traces to**: US-2 AC-2 · **Category**: Edge Case
- **Given** a volatile entry (`persisted: false`, revision 0, `live_seq` 10) shown in the centre
- **When** the browser refetches the list on window focus while the server keeps running
- **Then** the snapshot contains the entry, it is still shown, marked "Not saved — lost if the server restarts", and the badge count is unchanged
- **And** after a server restart the next snapshot no longer contains it and the entry is removed (DS-RECON rows 5, 16)

#### S-15: Attribution text never chooses a recipient
**Traces to**: US-1 AC-1 · **Category**: Error Path · per D83 (OQ-2 = A)
- **Given** accounts `dana` and `lee`, and an agent also named `dana` that created task "Sync" (the task's creator field reads `dana`)
- **When** the task's automatic run fails
- **Then** both `dana` and `lee` receive one `task_failed` entry (workspace object, every configured account)
- **And** the recipient list was not derived from the creator field (the same result with the creator field empty)

#### S-16: Session without an owner reaches every account
**Traces to**: US-1 AC-1 · **Category**: Alternate Path · per D83 (OQ-2 = A)
- **Given** a session created by a gateway schedule whose metadata is readable and has no owner, and accounts `dana` and `lee`
- **When** an approval is requested in it
- **Then** both accounts receive one `approval_requested` entry

#### S-17: The user's own Board change raises nothing
**Traces to**: US-1 AC-6 · **Category**: Happy Path
- **Given** fresh-install defaults with Task or plan started switched on
- **When** the user moves a task to In progress on the Board
- **Then** no centre entry and no pop-up appear

#### S-18: Calendar dispatch fails before a run exists
**Traces to**: US-1 AC-1, AC-5 · **Category**: Error Path
- **Given** a Calendar task whose trigger fires while the task cannot be spawned
- **When** the trigger dispatch fails
- **Then** one `schedule_failed` entry exists with target `task` and no `run_id`, and opening it shows the task

#### S-19: Heartbeat fails before any session exists
**Traces to**: US-1 AC-5 · **Category**: Error Path
- **Given** an agent heartbeat whose session cannot be picked
- **When** the run fails before a session is created
- **Then** one `schedule_failed` entry exists with target `agent` and opening it shows that agent

#### S-09: Run now of a Calendar task counts as a task result
**Traces to**: US-1 AC-1 · **Category**: Alternate Path
- **Given** Calendar task "Nightly report" with a recurring trigger and fresh-install defaults
- **When** the user presses **Run now** and the run fails
- **Then** one `task_failed` entry exists, no `schedule_failed` entry, and no `task_started` entry

#### S-100: A deletion missed while offline is applied on reconnect
**Traces to**: US-2 AC-4 · **Category**: Edge Case
- **Given** tab B offline while tab A deletes entry E
- **When** tab B reconnects and re-authenticates
- **Then** tab B no longer shows E and its badge is recomputed
- **And** a snapshot read before the delete that arrives after it does not bring E back (DS-RECON row 8)

#### S-101: A slow snapshot never rolls back a newer push
**Traces to**: US-2 AC-4 · **Category**: Edge Case
- **Given** a list request answered with revision 10 and, before it arrives, a pushed update of entry F with revision 11
- **When** the snapshot is applied
- **Then** F keeps its revision-11 content (DS-RECON row 2)
- **And** an older upsert that arrives after a newer snapshot retired its tombstone is ignored, while a genuinely newer update is applied (DS-RECON rows 9, 11, 12)

#### S-102: A repeated clear frame changes nothing
**Traces to**: US-2 AC-2 · **Category**: Edge Case
- **Given** a `notification_cleared` frame already applied
- **When** the same frame arrives again
- **Then** the list and badge are unchanged
- **And** a late deletion older than a re-created entry leaves the re-created entry in place (DS-RECON row 10)

#### S-103: Frame variants are strict
**Traces to**: US-2 AC-1 · **Category**: Error Path
- **Given** contract fixtures for every frame variant
- **When** a `notification_changed` frame carrying a `popup` field, a `notification_changed` frame with `change: created`, a `notification_deleted` frame carrying a `notification` object, or a `notification_cleared` frame with 1,001 ids is validated by the generated Go types and the generated Zod schemas
- **Then** validation fails in both; every positive fixture passes, including a 1,000-id batch (DS-FRAME)


#### S-125: A not-saved entry has a full lifecycle
**Traces to**: US-2 AC-1, US-3 AC-3 · **Category**: Error Path
- **Given** the notifications folder is not writable, tabs T1 and T2 open, and an approval requested in chat A
- **When** the approval is handled in the chat, and later the user deletes the entry in T1
- **Then** the entry appears in both tabs marked "Not saved — lost if the server restarts", turns "Handled" in both (each change with a higher `live_seq`), and the delete answers 204 and removes it from both tabs — no 404, no restore
- **And** a second not-saved entry that is still listed when the folder becomes writable again is saved at the next write for that user: both tabs receive it with `persisted: true`, a revision and `live_seq` 0, and the "Not saved" mark disappears (DS-RECON rows 13–15)

#### S-128: Outline — knowledge drift opens the right folder
**Traces to**: US-1 AC-5 · **Category**: Edge Case
- **Given** a knowledge-base folder whose index has drifted, mounted as `<mounts>`
- **When** the drift check reports it and the user opens the entry
- **Then** `<result>`

| mounts | result |
|---|---|
| once, in workspace `ws_a` | the Library opens that knowledge base in `ws_a` |
| in `ws_b` and in `ws_a` | one entry; it opens the mount in `ws_a` (first by workspace id, then mount name); the body names the folder |
| in `ws_a`, removed after the entry was created | "This knowledge base is no longer available here." |
| nowhere at the moment of the report | no entry; one WARN "knowledge drift without a mount" |

#### S-129: A task result arrives although run history failed
**Traces to**: US-1 AC-1, AC-5 · **Category**: Error Path
- **Given** a Calendar task with a recurring trigger whose run record cannot be opened (and, separately, one whose run record cannot be closed)
- **When** the scheduled occurrence runs and fails
- **Then** exactly one `schedule_failed` entry exists — target `task`, with `run_id` only when a run record exists — and the run-history ERROR line is still logged
- **And** when the task's trigger is changed to manual while that occurrence runs, the entry is still `schedule_failed` (the dispatch origin decides), and a second close of the same run raises no second entry

### Feature: retention and deletion

#### S-20: 30-day sweep deletes from storage
**Traces to**: US-3 AC-1 · **Category**: Boundary
- **Given** entries aged 29 d 23 h, 30 d 0 h and 31 d
- **When** the sweep runs
- **Then** only the 29 d 23 h entry remains, and the deleted ones are absent from the file

#### S-21: Count cap never evicts an unresolved Needs-you entry
**Traces to**: US-3 AC-2 · **Category**: Edge Case
- **Given** 50 entries, the oldest an unresolved `approval_requested`
- **When** a 51st entry is created
- **Then** the approval entry remains and the oldest handled entry is removed

#### S-22: Delete one, everywhere
**Traces to**: US-3 AC-3 · **Category**: Happy Path
- **Given** an entry visible in T1 and T2
- **When** the user deletes it in T1
- **Then** it is gone in T2 and absent from storage

#### S-23: Deleting a waiting Needs-you entry leaves the request waiting
**Traces to**: US-3 AC-3, AC-5 · **Category**: Happy Path
- **Given** an unresolved `question_asked` entry
- **When** the user deletes it
- **Then** the server answers 204, the entry is gone in every tab and from storage
- **And** the question card is still pending in its chat and the sidebar still marks the chat

#### S-24: Clear all removes everything and changes no request
**Traces to**: US-3 AC-4, AC-5 · **Category**: Happy Path
- **Given** 5 handled entries and 1 unresolved approval
- **When** the user confirms Clear all
- **Then** the centre is empty in every tab and the approval is still pending in its chat (not approved, denied or cancelled)

#### S-25: Quarantined corrupt file is deleted after 30 days
**Traces to**: US-3 AC-1 · **Category**: Edge Case
- **Given** a `<recipient>.json.corrupt-<n>` file 31 days old
- **When** the sweep runs
- **Then** the file is gone

#### S-26: A waiting entry is deleted at 30 days; the request keeps waiting
**Traces to**: US-3 AC-1 · **Category**: Boundary
- **Given** an unresolved `approval_requested` entry aged exactly 30 d 0 h and its approval still pending
- **When** the sweep runs
- **Then** the entry is gone from storage and every tab, and the approval is still pending in its chat

#### S-27: More than 50 waiting entries are all kept
**Traces to**: US-3 AC-2 · **Category**: Boundary
- **Given** 51 unresolved Needs-you entries and 3 handled ones
- **When** a new informational entry is created
- **Then** all 51 waiting entries remain and the oldest handled entry is the one removed

#### S-28: Handling a request whose entry was deleted resurrects nothing
**Traces to**: US-3 AC-5 · **Category**: Edge Case
- **Given** the entry of a pending approval was deleted
- **When** the user approves it in the chat
- **Then** the approval proceeds and no centre entry reappears

#### S-29: Concurrent create and clear lose nothing
**Traces to**: US-3 AC-4 · **Category**: Edge Case
- **Given** a Clear all and a new `task_failed` entry for the same user at the same moment
- **When** both complete under the recipient's lock, in either order
- **Then** create-then-clear leaves an empty file, the tabs receive `notification_created` (revision r) then `notification_cleared` (r + 1) and show no entry; clear-then-create leaves the new entry alone, the tabs receive `notification_cleared` (r) then `notification_created` (r + 1) and show only the new entry
- **And** in both orders every tab converges to exactly what a following `GET /notifications` returns, the file is never half-written, and the revision increased once per change


#### S-126: Clearing more than 1,000 entries
**Traces to**: US-3 AC-4, AC-5 · **Category**: Edge Case
- **Given** a user with 1,001 waiting Needs-you entries open in two tabs
- **When** the user chooses Clear all and confirms
- **Then** the server makes one store change and sends two `notification_cleared` batches (1,000 ids and 1 id) with the same revision; both tabs end with 0 entries and badge 0, whichever batch arrives first
- **And** every underlying request is still pending in its chat

### Feature: preferences

#### S-30: Defaults listed
**Traces to**: US-4 AC-1 · **Category**: Happy Path
- **Given** no preferences file
- **When** the user opens Settings → Notifications
- **Then** every §6 kind (Part 1) is listed with its default switches

#### S-31: Notify off suppresses entry and pop-up but not the chat line
**Traces to**: US-4 AC-2 · **Category**: Happy Path
- **Given** `task_failed` Notify off globally
- **When** a task fails
- **Then** no entry and no pop-up appear, and the task's chat shows the Error line

#### S-32: Workspace override
**Traces to**: US-4 AC-3 · **Category**: Alternate Path
- **Given** workspace W overrides `task_started` to On
- **When** a task starts in W and another in V
- **Then** one entry exists, for the task in W

#### S-33: System-wide kinds are global only
**Traces to**: US-4 AC-4 · **Category**: Error Path
- **Given** a preferences update with a workspace override for `provider_unreachable`
- **When** it is saved
- **Then** the server answers 400 `system_wide_kind_global_only` and nothing changes

#### S-34: Locked Needs-you switches (three kinds, both switches)
**Traces to**: US-4 AC-5 · **Category**: Error Path
- **Given** a preferences update turning off `<switch>` of `<kind>`
- **When** it is saved
- **Then** the server answers 400 `notification_switch_locked`, nothing changes, and the UI shows the switch locked with its explanation

| kind | switch |
|---|---|
| approval_requested | notify |
| approval_requested | popup |
| question_asked | notify |
| question_asked | popup |
| goal_paused | notify |
| goal_paused | popup |

#### S-35: Unreadable preferences file
**Traces to**: US-4 AC-7 · **Category**: Error Path
- **Given** a corrupt preferences file
- **When** an event occurs and the user opens the preferences screen
- **Then** defaults apply, an ERROR is logged, `load_state` is `unreadable`, and the screen says the saved settings could not be read, with Retry

#### S-36: Pop-up switch without Notify has no effect
**Traces to**: US-4 AC-2 · **Category**: Edge Case
- **Given** a kind with notify off and popup on
- **When** the event occurs
- **Then** neither entry nor pop-up appears, and the UI shows the pop-up switch disabled

#### S-37: Workspace override per field, and back to global
**Traces to**: US-4 AC-6 · **Category**: Alternate Path
- **Given** global `task_failed` Notify on, Pop-up on
- **When** the user sets only Pop-up to Off for workspace W
- **Then** a task failing in W creates an entry without a pop-up, and changing global Notify to Off then stops entries in W too
- **And** setting W's Pop-up back to **Use global** removes that override from the stored preferences

#### S-38: Replacing unreadable preferences needs confirmation
**Traces to**: US-4 AC-7 · **Category**: Error Path
- **Given** `load_state` `unreadable`
- **When** the client saves without `replace_unreadable`
- **Then** the server answers 409 `preferences_unreadable` and the file is untouched; after the user confirms, the save with `replace_unreadable: true` succeeds and `load_state` becomes `ok`

#### S-39: A stale save is refused, not merged silently
**Traces to**: US-4 AC-8 · **Category**: Error Path
- **Given** tab A and tab B loaded preferences at revision 4, and tab A saved (now revision 5)
- **When** tab B saves with base revision 4
- **Then** the server answers 409 `preferences_changed` with revision-5 preferences, and tab B shows "These settings changed in another tab." with **Reload settings**

#### S-104: Override for an unknown workspace
**Traces to**: US-4 AC-3 · **Category**: Error Path
- **Given** a preferences update with an override for a workspace id that does not exist
- **When** it is saved
- **Then** the server answers 400 `unknown_workspace` and nothing changes

### Feature: pop-ups

#### S-40: Outline — duration by severity
**Traces to**: US-5 AC-1, AC-2, AC-3 · **Category**: Happy Path
- **Given** a pop-up of `<severity>`
- **When** `<elapsed>` passes without hover
- **Then** the pop-up is `<state>`

| severity | elapsed | state |
|---|---|---|
| info | 4.9 s | visible |
| info | 5.1 s | gone |
| success | 5.1 s | gone |
| warning | 9.9 s | visible |
| warning | 10.1 s | gone |
| error | 10 min | visible |
| needs_you | 10 min | visible |

#### S-41: Hover pauses
**Traces to**: US-5 AC-1 · **Category**: Edge Case
- **Given** an info pop-up 4 s old
- **When** the pointer rests on it for 10 s and then leaves
- **Then** it stays during the hover and disappears about 1 s after leaving

#### S-42: Cap of three, overflow recoverable
**Traces to**: US-5 AC-4 · **Category**: Boundary
- **Given** three visible pop-ups
- **When** a fourth and a fifth arrive
- **Then** three remain visible and a line reads "+2 more — open notifications"
- **And** activating it opens the centre, where both hidden events are listed with their actions

#### S-43: One action, navigates
**Traces to**: US-5 AC-5 · **Category**: Happy Path
- **Given** a `schedule_failed` pop-up for a Calendar task run
- **When** the user activates **Open**
- **Then** the task opens on that run and the pop-up closes

#### S-44: Own-action results never pop up
**Traces to**: US-5 AC-6 · **Category**: Happy Path
- **Given** the user changes a setting on Settings → Performance
- **When** the save succeeds, and later a save fails
- **Then** no pop-up appears, no centre entry exists, and the page shows "Saved" and then "Not saved — Retry"

#### S-45: No event opens a modal
**Traces to**: US-5 AC-7 · **Category**: Happy Path
- **Given** an approval request arrives while Settings is open
- **When** the frame is handled
- **Then** no dialog opens; a Needs-you pop-up appears with **Review**

#### S-46: Needs-you in the open chat (D70)
**Traces to**: US-5 AC-3 · **Category**: Alternate Path
- **Given** chat X open in T1 and T1 visible, T2 on another screen, T3 with chat X open but hidden (another browser tab in front)
- **When** an approval for chat X arrives
- **Then** T1 shows the inline card and no pop-up; T2 and T3 show the pop-up

#### S-47: Keyboard focus pauses a pop-up
**Traces to**: US-5 AC-1 · **Category**: Edge Case
- **Given** an info pop-up 3 s old
- **When** keyboard focus moves into it for 10 s and then leaves
- **Then** it stays while focused and disappears about 2 s after focus left (the remaining time, not a fresh 5 s)

#### S-48: A queued pop-up gets its full duration
**Traces to**: US-5 AC-4 · **Category**: Edge Case
- **Given** three visible pop-ups and one queued info pop-up
- **When** one visible pop-up is resolved in another tab
- **Then** the queued pop-up becomes visible and stays its full 5 s from that moment

#### S-49: A browser-only condition is a lasting indicator, not a pop-up
**Traces to**: US-5 AC-8 · **Category**: Happy Path
- **Given** the app detects that a new version is available
- **When** the condition is detected
- **Then** an app-frame banner with **Refresh** appears and stays, no pop-up appears and nothing enters the centre

### Feature: system lines

#### S-50: Provider rejection as one Error line with the provider's words
**Traces to**: US-6 AC-1, US-8 AC-1 · **Category**: Happy Path
- **Given** provider OpenRouter answers 401 `{"error":{"message":"Incorrect API key provided: sk-or-v1-0123456789abcdef"}}`
- **When** the reply fails
- **Then** one Error line reads `OpenRouter rejected the API key: "Incorrect API key provided: [redacted]". Update the key in Settings → Providers.` with an **Open settings** action (chat code `provider_auth_failed`)
- **And** one `provider_error` centre entry for OpenRouter exists — the account-level incident kind (§14.1), separate from the chat code

#### S-51: Network retry line names the cause, countdown and attempt
**Traces to**: US-6 AC-2 · **Category**: Happy Path
- **Given** provider P refuses connections and the retry policy allows 5 attempts
- **When** the agent retries after the first failed attempt
- **Then** an animated Warning line reads `I can't reach P: connection refused. Retrying in <n> s (attempt 2 of 5)…` at every detail level — the first sentence from the frame's `text`, the retry clause filled from its `retry_at`, `attempt` and `max_attempts`

#### S-52: Outline — after recovery, by level
**Traces to**: US-6 AC-3 · **Category**: Happy Path
- **Given** the network retry of S-51 succeeds on attempt 3
- **When** the chat is viewed at `<level>`
- **Then** `<result>`

| level | result |
|---|---|
| Normal | no connection line remains |
| Verbose | one resolved line "Connection to P restored after 2 retries (<s> s)." |
| Debug | the same resolved line, technical details open |

#### S-53: Browser loses the server while the gateway keeps running
**Traces to**: US-6 AC-4 · **Category**: Happy Path · per D84 (W1 = A)
- **Given** a reply in progress and only the browser's connection to the running gateway cut for 20 s
- **When** the browser notices the loss
- **Then** an app-wide indicator and one chat line show "Can't reach the server right now. This page reconnects on its own when it's back." and no text claims what the agents are doing (D84)
- **And** after reconnecting both disappear, the chat shows the reply the agent finished meanwhile, and the reloaded chat history contains neither line

#### S-54: Line action
**Traces to**: US-6 AC-5 · **Category**: Happy Path
- **Given** a `model_retired` Error line
- **When** the user activates **Switch model**
- **Then** the chat's model picker opens

#### S-55: Replay parity, live and history de-duplicated
**Traces to**: US-6 AC-6 · **Category**: Edge Case
- **Given** a chat with an Error line carrying a provider message and a Verbose record line
- **When** the page is reloaded and the history is paged back
- **Then** both lines render with the same main text, quoted provider message, line type and action as live
- **And** the live-only fields (`LLMError.detail`, `facts.http_status`, `facts.request_id`) are absent from the reloaded and the paged copies, so their technical details show only what was persisted
- **And** a line received live and again in history is shown once (same `line_id`)

#### S-56: Provider message absent
**Traces to**: US-8 AC-1 · **Category**: Edge Case
- **Given** a provider 503 with an empty body
- **When** the reply fails
- **Then** the line uses the no-quote sentence and contains no empty quotes

#### S-57: No cause known yet
**Traces to**: US-6 AC-2 · **Category**: Edge Case
- **Given** a dispatched attempt to P that fails with an unclassifiable error
- **When** the agent retries
- **Then** the line reads `I can't get a reply from P yet. Retrying in <n> s (attempt 2 of 5)…` and names no cause

#### S-58: Final network failure names host, cause and one fix
**Traces to**: US-8 AC-4 · **Category**: Error Path
- **Given** provider P at host `p.example` refuses connections on every attempt
- **When** the retries are used up
- **Then** the Error line reads `I couldn't reach P (p.example): connection refused. Fix: check your network or proxy.` and never mentions the internet

#### S-59: A stale line action says so
**Traces to**: US-6 AC-5 · **Category**: Error Path
- **Given** a Restart-interrupted line with **Retry reply** for turn T, and a newer turn after T
- **When** the user activates **Retry reply**
- **Then** the line says "This reply can no longer be retried." and nothing is sent

#### S-106: A system line lives in the prepared history page
**Traces to**: US-6 AC-6 · **Category**: Edge Case
- **Given** a chat whose oldest page boundary falls next to a `system_line` entry
- **When** the user pages back
- **Then** the line appears once, the page respects the byte budget, and no "unclassified historical frame" error is logged

#### S-107: The fallback note quotes why the first model failed
**Traces to**: US-6 AC-1, US-8 AC-1 · **Category**: Happy Path
- **Given** model M1 fails with 503 `{"error":{"message":"overloaded"}}` and backup model M2 answers
- **When** the reply is shown live and after reload
- **Then** the fallback note names M1, M2 and quotes "overloaded" in both renders
- **And** when a later reply in the same chat falls back from M1 to M2 again because of a 429 "rate limit exceeded", that reply saves its own note quoting "rate limit exceeded" — live, after reload and when paged back — and the first reply's note still quotes "overloaded"

#### S-117: Stop outcome is a chat line, not a notification
**Traces to**: US-6 AC-1, US-1 AC-6 · **Category**: Happy Path
- **Given** a Stop that could not finish every request
- **When** the outcome is known
- **Then** one Warning line appears in that chat and no centre entry or pop-up exists


#### S-124: The gateway stops during a reply
**Traces to**: US-6 AC-4 · **Category**: Error Path · per D84 (W1 = A)
- **Given** a reply in progress and the gateway stopped for 20 s, then started again
- **When** the browser notices the loss and later reconnects
- **Then** during the outage the indicator and chat line read as in S-53 and no text says the agents keep working (D84)
- **And** after reconnecting the chat shows the existing **Interrupted** notice for the cut-off reply, and the reloaded history contains neither connection line

#### S-130: Another chat's recovery does not resolve mine
**Traces to**: US-6 AC-3, US-1 AC-4 · **Category**: Edge Case
- **Given** chats A and B both retrying replies through provider P, and A at attempt 3 of 5
- **When** B's next attempt succeeds
- **Then** the centre's incident for P resolves and B's line follows S-52
- **And** A keeps its own warning line; if A then runs out of attempts it shows its final Error line and gets no "restored" record line

#### S-131: The retry countdown stays live
**Traces to**: US-6 AC-2 · **Category**: Edge Case
- **Given** a retry frame with `text` "I can't reach P: connection refused.", `retry_at` 8 s after `sent_at`, attempt 2 of 5
- **When** 3 s pass
- **Then** the line reads `I can't reach P: connection refused. Retrying in 5 s (attempt 2 of 5)…`; the server sentence is unchanged and only the clause's number moved

#### S-132: An assembled sentence is shown, unassembled text is not
**Traces to**: US-8 AC-1, US-6 AC-6 · **Category**: Error Path
- **Given** a `judge_conclusion_failed` error whose sentence was assembled with its reason (`message_assembled: true`), and an own-limiter `rate_limited` error carrying its internal text without the flag
- **When** both are shown live and after a reload
- **Then** the first shows the assembled sentence with its reason; the second shows the catalogue copy for `rate_limited` and never its internal text

### Feature: detail levels

#### S-60: Outline — row visibility
**Traces to**: US-7 AC-2 · **Category**: Happy Path
- **Given** a chat with each row kind L1…L11
- **When** the detail level is `<level>`
- **Then** row `<row>` is `<shown>`

(Examples: every cell of the §12.2 table — 33 cells, dataset DS-DL; L6 and L8 literal, D75.)

#### S-61: Switch replaced
**Traces to**: US-7 AC-1 · **Category**: Happy Path
- **Given** Settings → Chat
- **When** it opens
- **Then** a three-way Detail level control is shown and no "Verbose chat" switch exists anywhere

#### S-62: Debug masks secrets
**Traces to**: US-7 AC-3, US-10 AC-1 · **Category**: Error Path
- **Given** Debug selected and a failure whose payload contains `"api_key":"sk-live-1234567890"`
- **When** technical details render
- **Then** the key appears only as `[redacted]`

#### S-63: Raising the level shows details received earlier
**Traces to**: US-7 AC-2 · **Category**: Edge Case
- **Given** a failure received live while the level was Normal
- **When** the user switches to Verbose without reloading
- **Then** the failure's Technical details disclosure appears with the details that arrived live

#### S-64: Debug shows what exists, and no successful raw exchange
**Traces to**: US-7 AC-3 · **Category**: Happy Path
- **Given** Debug selected and a chat with one successful reply and one failed reply
- **When** the chat renders
- **Then** session, turn, tool-call, approval and request ids and the full masked failure detail are shown
- **And** no raw request or response of the successful call is shown (D66)

#### S-65: Every other tool call at Normal, including ones hidden today
**Traces to**: US-7 AC-2 · **Category**: Happy Path
- **Given** a turn with a delegate status poll and a tool-loading call
- **When** the chat shows at Normal
- **Then** each appears as a compact name-only line (L6 literal)

#### S-66: Fallback note leaves Normal after the reply ends
**Traces to**: US-7 AC-2 · **Category**: Happy Path
- **Given** a reply answered by a backup model
- **When** the reply ends and the level is Normal, then Verbose
- **Then** the fallback note is hidden at Normal and shown at Verbose (L8 literal)

### Feature: wording

#### S-70: Voice in chat vs outside
**Traces to**: US-8 AC-2, AC-3 · **Category**: Happy Path
- **Given** a `workspace_unavailable` failure for agent Jim
- **When** it is shown in the chat and as a task-failed centre entry
- **Then** the chat line starts "I couldn't open my working folder" and the centre body reads "Jim's working folder (…) could not be opened: …"

#### S-71: Voice guard fails on a planted "we"
**Traces to**: US-8 AC-2 · **Category**: Error Path
- **Given** a catalogue fixture with "We could not reach the provider"
- **When** the voice guard runs
- **Then** it exits non-zero naming the entry

#### S-72: Catalogue equals the agreed baseline plus N2
**Traces to**: US-8 AC-1 · **Category**: Happy Path
- **Given** WORDING §1 and the N2 lines of §13.2
- **When** the generated catalogues are compared with them
- **Then** every code's sentence matches WORDING §1 exactly (placeholders included), except the `network` and `provider_error` lines, which match §13.2 exactly

#### S-73: Voice guard fails on first person outside a chat
**Traces to**: US-8 AC-3 · **Category**: Error Path
- **Given** a centre-title fixture "I could not reach the provider"
- **When** the voice guard runs
- **Then** it exits non-zero naming the entry

#### S-74: Outline — voice guard tone checks
**Traces to**: US-8 AC-2, AC-3 · **Category**: Error Path
- **Given** a fixture containing `<text>`
- **When** the voice guard runs
- **Then** it exits non-zero

| text |
|---|
| `Done!` |
| a sentence with an emoji |
| `Sorry, that failed.` |
| `Oops, that failed.` |

#### S-75: Wording manifest must be complete
**Traces to**: US-8 AC-1 · **Category**: Error Path
- **Given** a manifest with one id lacking a disposition, or a surviving id lacking a self-check receipt
- **When** the manifest test runs
- **Then** it fails naming the id; with the complete manifest it passes

#### S-76: Several providers failing — never "internet down"
**Traces to**: US-8 AC-5 · **Category**: Happy Path
- **Given** providers P1 (connection refused) and P2 (address could not be found) both unreachable
- **When** the user opens the centre
- **Then** one entry reads "Connections to AI providers are failing" and lists `P1: connection refused` and `P2: its address could not be found`
- **And** no text mentions the internet

#### S-77: Qualified model advice stays allowed
**Traces to**: US-8 AC-1 · **Category**: Alternate Path
- **Given** the `request_too_large` sentence "…or switch to a model with a larger limit."
- **When** the copy-rule test runs
- **Then** it passes, while an unqualified "switch model" in a `product`/`config` message fails

#### S-78: One message, two voices
**Traces to**: US-8 AC-2, AC-3 · **Category**: Happy Path
- **Given** a network failure at P while a task runs
- **When** it is shown in the task chat and as the centre entry title
- **Then** the chat says `I can't reach P: …` and the centre says `P can't be reached: …`


#### S-127: More than ten failing providers
**Traces to**: US-8 AC-5 · **Category**: Edge Case
- **Given** eleven providers with open `provider_unreachable` incidents
- **When** the user opens the centre
- **Then** one "Connections to AI providers are failing" entry lists ten causes, earliest incident first, and ends with "and 1 more"; its `causes_omitted` is 1

#### S-133: No CLI-worker failure ends in the generic line (#1857)
**Traces to**: FR-092 · **Category**: Error Path
- **Given** each external CLI worker failure shape of §13.8.2 C1–C7 and C9 (a flag-parse stderr tail — the Mac team's duplicated-flag case, a stream `"error"` event with a structured `error.type` no-route, a not-signed-in stream result / `error`/`turn.failed` event, a bare non-zero exit, a turn-cap message, a stream read error with a transport phrase **and** one with an unrecognised cause (MIN-003), a driver start failure, a limit/credit upstream refusal — arriving via **stderr tails and stdout error envelopes alike**), driven through the real parser and the CLI-only classifier (integration paths in S-143/T-56)
- **When** each is classified
- **Then** none returns `unknown` and none returns a provider or network code; each returns its row's proposed code (`external_cli_invalid_args`, `external_cli_model_unavailable`, `external_cli_needs_signin`, `external_cli_failed`, `external_cli_turn_limit`, `external_cli_upstream_refused`), and the chat line is that code's §13.8.3 sentence with `{cli}` filled

#### S-134: CLI sign-in failure says what to do and nothing raw
**Traces to**: FR-092, FR-093 · **Category**: Happy Path
- **Given** a fake CLI failing with a not-signed-in stream result and a stderr tail carrying a token-shaped string
- **When** the run fails in a chat
- **Then** the error line reads the W3 sign-in sentence with the CLI's display name and no stderr text on it; the technical details carry the CLI's display name, the exit status and the matched kind — never stderr text (FR-093); retryable is false

#### S-135: The generic line remains only for the unknown case
**Traces to**: FR-092 · **Category**: Alternate Path
- **Given** a failure with no typed sentinel, no structured fact, no pinned matcher and no dispatch (the reserved case of §13.8.2, FR-092's definition)
- **When** it is translated
- **Then** the generic `unknown` line is shown unchanged — the one case the generic copy keeps

#### S-136: CLI stderr never crosses the gateway/SPA boundary
**Traces to**: FR-093 · **Category**: Error Path
- **Given** a CLI stderr tail **and a stdout error envelope** each containing a file path, a flag with its value, and a key-shaped string
- **When** the failure is shown at Normal, Verbose and Debug, the parent's tool result and completion message are built, and the live frames, Settings responses and persisted/replayed error records are serialized
- **Then** no stderr/stdout raw text — no path, no flag-with-value text, no key-shaped string, no masked tail — appears in **any serialized or persisted surface** at any level: the main line is the fixed sentence, the technical details carry display name + exit status + matched kind, the tool result and completion message carry the typed code and its sentence, and gateway.log keeps the raw text (status quo, R-7; inspected at the serialization boundary, not from helper return values — MAJ-006)

#### S-137: The new lines pass the voice guard and the copy rules
**Traces to**: FR-092 · **Category**: Happy Path
- **Given** the §13.8.3 proposed lines W1–W16 **and every catalogue sentence written in matrix §13.8.5** (B10's two variants, B7's steering variant, and the proposed WP-6 texts of B16-storage/B21/B22/B23 and the C11 Settings result) as catalogue fixtures
- **When** the voice guard (§13.4) and the catalogue copy-rule tests (`pkg/agent/translate_error_test.go` — the `bannedSwitch` set, the config-attribution no-retry rule, the contact-support phrases) run over them
- **Then** both pass: no product-as-actor (`Omnipus` only in the D48 sentence), no "we/our/us", no "please" outside the D48 sentence, no "!", no emoji, sentence case; no banned switch phrase in `config`-attributed copy; no retry advice on a `config` code; no "contact support" phrasing; `{cli}` is in the closed slot vocabulary and every slot renders a real value

#### S-138: `unknown` never carries a non-catalogue message (the F-1 guard — corrected, MAJ-004)
**Traces to**: FR-092 · **Category**: Error Path
- **Given** the T-54 allow-list of residual `CodeUnknown` emit sites — **exactly two groups, each named with its reason** (final list per the post-check): (1) A4/A5's non-promoted dispatched-provider residuals — genuinely unclassified; (2) the six **B24** record-save refusals (`loop_run_turn.go` final streamer ensure and dedup synthetic-deny save, `cancel.go` cancel marker, `started_tool_outcome.go` cancelled receipt, `loop_run_turn_tools.go` sub-turn result, `loop_window.go` wake-input placement) — a known class of cause (a record could not be saved) that **#1857 deliberately does not re-key**: they stay generic today, each listed with its site and reason so none hides. The B10 duplicate site is **not** on the allow-list (removed in grill round 2 — it carries `tool_assembly_duplicate` with two variants keyed on the MCP fact). Each listed site attaches the catalogue fallback copy, never authored text — plus three mutations: a new emitter that attaches curated text to `CodeUnknown`, a regression inside an already-allowed emitter that swaps its catalogue fallback for authored text, and a mutation that restores a known-cause site (the B10 duplicate) to the generic result
- **When** the guard test runs and the frames are rendered
- **Then** all three mutations fail the build; every allowed site's payload contains the catalogue fallback (asserted on the payload, not only on the rendering); and untrusted attached text is never displayed by `WireLLMError` → `getLLMErrorDisplay` — three separate assertions, so hiding a bad payload can never stand in for preventing it (MAJ-004)

#### S-139: Every known-cause kind produces its agreed outcome, live and after reload (MAJ-004/MAJ-005)
**Traces to**: FR-092 · **Category**: Happy Path
- **Given** each §13.8.2 kind that this amendment moves off the generic line — B3 (founder E2), B5–B7 (both B7 sites), B10–B17, B19, B21, B22, B23, B25, B26 (the sec2 rows), C1–C9, C8 included, with its matrix §13.8.5 row as the independent oracle (code, attribution, copy source, slots) — **both B10 fixtures counted as positive known-cause coverage** (MCP-involved and no-MCP-involved — the only source fact the detector records; grill round 2, R2-MAJ-003, re-keyed in the post-check)
- **When** each producer runs against its triggering fact at its **real caller** — the B22 fixture through `prepareToolSurface`, asserting the refusal happens **before any provider call**; the B7 steering fixture through the steering admission site, not the root admission; the B3 fixture through the rate-limiter emit block in `beginIteration`; the B25/B26 fixtures through their real reporters — and the transcript is reloaded
- **Then** the live frame and the reloaded transcript both carry the row's typed code and its agreed sentence — never the generic line; per-code variant and no-fact forms render their own agreed text (contract/variant-parity fixtures); the B25/B26 fixtures additionally assert **no substring of the raw error text** on any emitted or persisted surface (the raw text stays in the server log — founder sec2 A; copy-independent, so it does not wait for the E4 review); the B11 fixture asserts W15 **only** when the emitter carries the non-empty-response fact; and a mutation that restores any converted row — the duplicate included — to the generic result fails

#### S-140: Save stages are distinguished — each stage's text and execution outcome are asserted at the real caller (MAJ-001; re-bound to the post-check's corrected bindings)
**Traces to**: FR-092 · **Category**: Error Path
- **Given** the save-failure stages, each faulted at its real caller: the root admission refusal (B7 — `window_runtime.go::appendWindowMessage` via `loop_run_turn.go:825`), the mid-turn steering admission refusal (B7's second variant — `wake_input.go:252` via `loop_run_turn.go:1381`), a started tool's receipt failure (B19 — `beginStartedTool`), an intermediate assistant continuation save failure (B23 — `recordToolCalls` → `dispatchToolCalls`), the answer-present finalization save failure (B11 — `appendFinalModelResponse`, driven with and without the answer-state fact), the finalization save failure without answer evidence (B21 — the direct `Sessions.Save` failure of `finalizeTurn` driven from a stopped-silent turn and from an answered turn alike, **plus** the failed `recordIterationLimitNotice` branch), and the gateway's late save failure (B20 — `websocket_streamer.go`)
- **When** each stage fails
- **Then** both the text **and the execution outcome** are asserted: the root admission says "wasn't sent" (W9) and the steering admission never does (its variant claims only the failed save — earlier work is unaffected, and the fixture includes an earlier tool round to prove the copy makes no "nothing ran" claim); B19's tool **keeps running** (its execution continues) with only the non-terminal consequence line (W16) **and the done frame reports failed** (the `markTurnFailed` flag); B23 **stops the turn** — `TurnEndStatusError`, no new tool round is admitted, the terminal variant shows, never W16; B11 says "answered" (W15) **only when the emitter carries the non-empty-response fact** — without the fact it shows B21's form; B21 never claims "answered" on either producer (the direct-Save fixture runs from a stopped-silent turn, where W15 would be false) and its copy invites **no blind re-run** after earlier tool rounds — the fixture rejects any copy advising a re-run of completed work (R2-MAJ-006); B20's done frame reports `TurnFailed` while its notice shows its own fixed text (manifest `M-1857-B20`) — never read as a successful turn; live and reloaded records agree

#### S-141: The delegation parent journey — summary, Open, specific child error (MAJ-003)
**Traces to**: FR-092, FR-093 · **Category**: Happy Path
- **Given** a CLI delegation that fails, with the parent's three surfaces distinct: the lifecycle summary ("{agent} stopped without finishing"), the **Open** affordance (`delegationEventShowsOpen`), and the child's specific error line
- **When** the parent views the delegation timeline and follows **Open**, and the parent agent receives its completion message (`steer_completion.go::completionMessage`)
- **Then** the summary stays as approved (no child text folded in); **Open** leads to the child's typed error line; the completion message carries the typed cause and neutral wording; both B16 sentinel forms show their own cause with an unchanged-workspace control for the resume-unavailable form; no stderr text on any parent surface

#### S-142: A nonfatal CLI diagnostic never becomes a terminal failure — and no diagnostic line remains on a successful run (MAJ-008, baseline corrected in grill round 2)
**Traces to**: FR-092, FR-093 · **Category**: Edge Case
- **Given** a fake CLI that emits a malformed line (`Fatal: false`), then valid output, then completes successfully — driven through the real drain path — and the **same input driven through the real Settings handler** (`drainSmokeTestRun` → `buildResponse`), a separate consumer
- **When** the run finishes; the Settings test completes
- **Then** on the normal run: no terminal-failure sentence, no parent failure message, and **no emitted or persisted user-facing diagnostic line** (the amendment's removal asserted — today's baseline leaves one); the diagnostic's content stays in gateway.log; the run's success is unaffected — the control counterpart of S-133's fatal shapes. On the Settings side: the result **stays a failed test (`ok: false`)** — the retained stricter rule — with truthful test-level copy (manifest `M-1857-C11-settings`) that never claims the diagnostic proves a failed CLI run; a mutation that re-adds the diagnostic line to the successful run, or that makes the Settings copy claim a failed CLI run, fails this scenario

#### S-143: End-to-end adapter coverage for the two reported cases (MAJ-006; Settings duplicated-arguments case added in grill round 2, R2-MIN-001)
**Traces to**: FR-092, FR-093 · **Category**: Integration
- **Given** a fake CLI process boundary producing (a) a duplicated-flag rejection via Additional CLI arguments — a process-exit/stderr failure — and (b) a nested `error.type: provider.no-route` stdout envelope, plus a direct start failure
- **When** each drives the real parser → dispatcher → error assembly → persistence → chat renderer; **case (a) additionally drives the real Settings handler** (asserted in the serialized test response) alongside case (b)'s Settings drive, each **paired with a corrected-arguments success control** through the same handler
- **Then** the helper chat shows the W1/W2 sentences with `{cli}`'s display name; the Settings result shows **W1's neutral form for the duplicated-arguments case** — failed-test status, no flag or value text in the serialized result (R2-MIN-001) — and W2's neutral form for the model-unavailable case; each success control passes; the typed code survives to the persisted record and replays identically; a mutation that drops nested metadata, leaves Settings on the old translator, or loses the typed code on the completion route fails this scenario

### Feature: connection logging

#### S-80: Outline — failure class in the log
**Traces to**: US-9 AC-1, AC-2 · **Category**: Happy Path
- **Given** a dispatched attempt that fails with `<observation>`
- **When** the attempt ends
- **Then** exactly one WARN line "provider attempt failed" has `failure_class=<class>` and `reason=<reason>`

(Examples: dataset DS-NET.)

#### S-81: Browser outage report
**Traces to**: US-9 AC-3 · **Category**: Happy Path
- **Given** the browser last heard from the server at 10:00:00, noticed the loss at 10:00:01 and reconnected at 10:00:41
- **When** it reports the outage
- **Then** the server log has one WARN "client connection outage" with `client_outage_kind=server_unreachable`, `observed_duration_ms` 40000 and `max_possible_duration_ms` 41000

#### S-82: Invalid outage report
**Traces to**: US-9 AC-3 · **Category**: Error Path
- **Given** a report whose end precedes its start
- **When** it is posted
- **Then** the server answers 400 `invalid_outage_report` and logs nothing about an outage

#### S-83: Outage report flood
**Traces to**: US-9 AC-3 · **Category**: Boundary
- **Given** 10 reports in the last minute from one user
- **When** an 11th is posted
- **Then** the server answers 429

#### S-84: Incident recorded after the chat line left
**Traces to**: US-9 AC-4 · **Category**: Happy Path
- **Given** S-52 at Normal
- **When** the operator reads the log
- **Then** "provider incident started" and "provider incident ended" lines for P exist with duration, attempts and affected chat count

#### S-85: Retry decisions unchanged
**Traces to**: US-9 AC-1 · **Category**: Regression
- **Given** the existing fallback and retry datasets
- **When** they run with the new labelling
- **Then** every retry/fallback/cooldown outcome equals the pre-change outcome

#### S-86: Non-requests are not observed
**Traces to**: US-9 AC-1 · **Category**: Edge Case
- **Given** `<case>`
- **When** the turn proceeds
- **Then** no "provider attempt failed" line is written and no incident opens

| case |
|---|
| the user presses Stop during an attempt |
| a candidate is skipped by cooldown |
| the route cannot be built before dispatch |
| Omnipus's own rate limiter refuses the call |
| attaching an inspection image fails before the call |
| a context-window check refuses the request before the call |

#### S-87: Exactly one line per dispatched attempt across nested retries
**Traces to**: US-9 AC-1 · **Category**: Edge Case
- **Given** a fallback chain of two candidates where the first is retried twice by the inner retry loop and then falls back
- **When** all attempts fail
- **Then** there are exactly 4 lines with `attempt_seq` 1…4 and the right `candidate` and `retry_round` values

#### S-88: Re-failure within 10 minutes re-opens without a pop-up
**Traces to**: US-1 AC-4 · **Category**: Edge Case
- **Given** a `provider_unreachable` incident for P resolved 9 min ago
- **When** P fails again
- **Then** the same entry is unread and open again, no new pop-up appears, and the restored entry is still listed; after 10 min a new failure creates a new entry with a pop-up

#### S-89: Restart keeps an open incident open
**Traces to**: US-1 AC-4 · **Category**: Edge Case
- **Given** an unresolved `provider_unreachable` entry for P and a gateway restart
- **When** the first attempt at P after the restart succeeds
- **Then** the entry is resolved and a restored entry appears, with no duplicate unreachable entry created

#### S-108: Success with another model at the same provider resolves
**Traces to**: US-2 AC-3 · **Category**: Alternate Path
- **Given** an open `provider_error` incident for P raised by model M1
- **When** an attempt at P with model M2 succeeds
- **Then** the incident resolves

#### S-109: Disk full opens and the next write resolves
**Traces to**: US-1 AC-4 · **Category**: Happy Path
- **Given** the data folder's disk is full
- **When** a session write fails for lack of space, then a notification-store write fails for lack of space, and later a session write succeeds
- **Then** one `resources_low` (disk) entry opens — delivered as not saved, because the store cannot write either — and is resolved by the later session write
- **And** the notification-store failure neither opens a second entry nor resolves the first, a repeated no-space failure only updates the entry, and no write blocks or waits on the notification lock

#### S-110: Unmeasurable memory shows a permanent line, no notification
**Traces to**: US-4 AC-1 · **Category**: Alternate Path
- **Given** a host whose memory cannot be measured
- **When** the user opens Settings → Performance
- **Then** it shows "Memory can't be measured on this host, so at most 2 agents run at once." and no `resources_low` or `concurrency_reduced` entry exists

#### S-111: Connections failing opens at two providers
**Traces to**: US-8 AC-5 · **Category**: Boundary
- **Given** one provider unreachable
- **When** a second provider becomes unreachable, and later one of them recovers
- **Then** `provider_connections_failing` opens at the second, and resolves when only one remains

#### S-123: Model-specific errors stay in the chat
**Traces to**: US-1 AC-4 · **Category**: Alternate Path
- **Given** model M at P answers `model_retired`
- **When** the reply fails
- **Then** the chat shows the Error line and no `provider_error` entry exists

#### S-112: Report accepted but the answer was lost
**Traces to**: US-9 AC-3 · **Category**: Edge Case
- **Given** an outage report the server accepted whose 204 never reached the browser
- **When** the browser retries the same `report_id`
- **Then** the server answers 204 and the log still has exactly one outage line
- **And** after more than 1,000 newer report ids within 24 h, a repeat of the old id is logged again — the stated duplicate bound (DS-OUT)

#### S-113: Laptop slept during the outage
**Traces to**: US-9 AC-3 · **Category**: Edge Case
- **Given** the last confirmed contact at 22:00, the loss noticed at 07:00 after sleep, and reconnection at 07:00:10
- **When** the report is logged
- **Then** `observed_duration_ms` is 10000 and `max_possible_duration_ms` is about 32,410,000 (9 h 0 min 10 s)

#### S-114: Reload during the outage still reports once
**Traces to**: US-9 AC-3 · **Category**: Edge Case
- **Given** an outage in progress
- **When** the user reloads the page and the server comes back
- **Then** one report with `page_reloaded: true` is logged
- **And** a reload after reconnecting but before the 204 re-sends the same `report_id` and the log still has one line
- **And** with five reports already pending, a sixth drops the oldest with a console line (DS-OUT-CLIENT rows 6, 10)

#### S-115: Rate-limited report waits for Retry-After
**Traces to**: US-9 AC-3 · **Category**: Error Path
- **Given** the server answers a report with 429 and `Retry-After: 30`
- **When** the browser retries
- **Then** the next attempt is no sooner than 30 s later

### Feature: security

#### S-90: Outline — masking on every surface
**Traces to**: US-10 AC-1 · **Category**: Error Path
- **Given** a provider message `<echo>`
- **When** it is shown on `<surface>`
- **Then** the row's own expected outcome holds on that surface

(Examples: dataset DS-SEC — every row × surfaces main line, details, centre body, log line, fallback note, replayed history. Outcomes are row-specific: rows 1–12 and 15 masked (`<secret>` never appears and `[redacted]` does; row 15 is the non-JSON split value, driven through the real path by T-51, D81); row 13 is the documented limit (D82): output equals input; row 14 dropped — the no-quote sentence, no provider text.)

#### S-91: Prefix-anchored partial echo is masked
**Traces to**: US-10 AC-1 · **Category**: Edge Case
- **Given** a provider message `Incorrect API key provided: sk-…wxyz`
- **When** it is shown on the main line
- **Then** it reads `Incorrect API key provided: [redacted]`

#### S-92: A registered value split by a line break is masked
**Traces to**: US-10 AC-1 · **Category**: Edge Case
- **Given** a registered credential value echoed with a line break in its middle, inside a JSON message field
- **When** it is extracted and normalised
- **Then** neither half appears on any surface
- **And** for a `text/plain` body whose first line ends with the first half and whose second line starts with the second half, driven through the real extraction-to-display path, no fragment of the value (no substring of 4 or more of its characters) appears on any of the six surfaces, because the whole response is masked before the first line is selected (D81; DS-EXTRACT row 17, DS-SEC row 15, T-51)

#### S-93: Usernames differing only in case get different files
**Traces to**: US-1 AC-1 · **Category**: Edge Case
- **Given** accounts `Dana` and `dana`
- **When** each receives a notification
- **Then** two different files exist and each user sees only their own entries (DS-NAME)

#### S-94: Masking failure drops the text
**Traces to**: US-10 AC-1 · **Category**: Error Path
- **Given** provider text that the masker cannot process
- **When** the failure is shown
- **Then** the no-quote sentence is used, the text appears nowhere, and one ERROR is logged without it

### Feature: removals

#### S-95: Removal only where the page shows the outcome
**Traces to**: US-11 AC-1, AC-2 · **Category**: Happy Path
- **Given** each audited call site with recommendation "remove + add page indicator (with Retry)"
- **When** its action fails
- **Then** the page shows the indicator (with Retry where specified) and no pop-up appears

#### S-96: Undo has a stable page home
**Traces to**: US-11 AC-3 · **Category**: Happy Path
- **Given** a calendar event moved by drag
- **When** the user then navigates the calendar so the event is out of view
- **Then** the result area at the top of the Calendar page still offers **Undo** until 10 s have passed, and no pop-up holds it

#### S-97: No approval modal
**Traces to**: US-11 AC-4 · **Category**: Happy Path
- **Given** the built app
- **When** searching the bundle and DOM for the approval modal
- **Then** it is absent; approvals render only inline

#### S-98: Panel already open in another tab
**Traces to**: US-11 AC-1 · **Category**: Happy Path
- **Given** the Library panel open in tab A
- **When** the user opens it in tab B
- **Then** tab B shows a banner "Library is open in another tab." with **Switch**, and no pop-up

#### S-99: Preview link exposure stays visible
**Traces to**: US-11 AC-1 · **Category**: Happy Path
- **Given** a web preview with **Copy link**
- **When** the user copies the link
- **Then** the helper text under the button says who can open the link and until when, before and after copying, and no pop-up appears

#### S-116: Failed save, then Retry
**Traces to**: US-11 AC-2 · **Category**: Error Path
- **Given** Settings → Performance and the server unreachable
- **When** the user changes a value
- **Then** the page shows "Not saved — Retry"; after the server is back, **Retry** shows "Saved"; no pop-up at any step

### Feature: accessibility and narrow screens

#### S-118: Keyboard-only centre journey
**Traces to**: US-12 AC-1 · **Category**: Happy Path
- **Given** the keyboard only
- **When** the user opens the centre, filters by Needs you, opens an entry, deletes another and clears all
- **Then** each step is reachable in the order of §16A, and Escape returns focus to the account-menu button

#### S-119: Focus repair when an item disappears
**Traces to**: US-12 AC-2 · **Category**: Edge Case
- **Given** keyboard focus on a centre row
- **When** the item is resolved and deleted in another tab
- **Then** focus moves to the next row (or previous, or the filter)

#### S-120: Retry announced once per attempt
**Traces to**: US-12 AC-3 · **Category**: Happy Path
- **Given** a screen reader and a retry line counting down from 8 s over two attempts
- **When** the retries run
- **Then** exactly two announcements are made, and reloading the chat announces nothing

#### S-121: 320 px and 400 % zoom
**Traces to**: US-12 AC-4 · **Category**: Edge Case
- **Given** a 320 px wide window
- **When** a system line with a 300-character provider quote and an action, the centre, a pop-up and the preferences screen are shown
- **Then** none scrolls horizontally and every action is visible and operable

---

## 19. Test-driven development plan and gates

Tests are written **before** code by the one qa-lead RED author; CHECK by a different instance. Local runs follow root `CLAUDE.md` (one narrow tagged Go test at a time through the slot runner; one vitest file at a time; Playwright one spec at a time; CI is the authority on the integration candidate).

### 19.1 Test implementation order

| # | Test name | Level | Traces to | What it proves |
|---|---|---|---|---|
| T-48 | contract fixtures: one positive and one negative fixture per frame variant and per new schema, validated by the generated Go types and the generated Zod schemas, incl. 1,000 vs 1,001 ids, 10 causes + `causes_omitted` vs 11 causes, `notification_changed` with `popup` or `change: created`, retry and fallback frames without `text`, `LLMError` with the removed `provider_message` (`make verify-contracts` plus a fixture test in `pkg/api/generated` and `src/lib/api/generated`) | Unit | S-103, S-126, S-127 | strict frames, DS-FRAME (WP-0, first) |
| T-01 | `pkg/notifications::TestRouterRejectsUnknownKind` | Unit | S-06 | closed enum |
| T-02 | `pkg/notifications::TestRouterMintsIDWhenStoreFails` + `TestVolatileLifecycle` (create → update → resolve keep one id with rising `live_seq`; mark read, delete and Clear all of a volatile item answer 204; a failed update of a saved item keeps its saved revision as base; volatile items are listed by `GET /notifications`; folding into the file at the next successful write; a failed delete returns 500 and pushes nothing) | Unit | S-07, S-125 | #645 Defect 1 closed; `persisted: false`, revision 0, R2-MAJ-003 |
| T-39 | `pkg/notifications::TestRecipientRules` (R1 set / empty / unreadable, R2 every account, R3, attribution fields ignored, no accounts) | Unit | S-08, S-15, S-16 | MAJ-005 |
| T-03 | `pkg/notifications::TestRouterAppliesPreferences` (table: global, workspace per field, inherit, system-wide global only, three locked kinds, notify-off) | Unit | S-02, S-31, S-32, S-36, S-37 | resolution order |
| T-04 | `pkg/notifications::TestRetentionSweepAgeBoundary` (incl. waiting entries and quarantine files; a sweep of more than 1,000 entries is one store change in batches) | Unit | S-20, S-25, S-26, S-126 | 30 days, D80 |
| T-05 | `pkg/notifications::TestCapNeverEvictsUnresolvedNeedsYou` (incl. > 50 protected) | Unit | S-21, S-27 | eviction rule |
| T-06 | `pkg/notifications::TestDeleteAndClear` (waiting entries deletable; request untouched; idempotent clear; revision per change; Clear all of 1,001 entries → batches of 1,000 + 1 with one revision; failed delete → 500, nothing pushed) | Unit | S-22, S-23, S-24, S-28, S-102, S-126 | D80, real deletion |
| T-07 | `pkg/notifications::TestResolveLifecycle` (created/updated/read/resolved, coalesce on unresolved key) | Unit | S-13, S-04 | lifecycle |
| T-08 | `pkg/notifications::TestRecipientFileNames` (DS-NAME) | Unit | S-93 | SHA-256 names, stored-username check |
| T-09 | `pkg/notifications::TestPreferencesLoadStates` (missing → defaults, corrupt → unreadable, never overwritten without replace flag) | Unit | S-35, S-38 | degraded mode |
| T-29a | `pkg/notifications::TestSidecarLockAroundEveryMutation` (concurrent create/clear/sweep in both lock orders, client state equal to the following GET; Windows build vetted with `GOOS=windows go vet`) | Unit | S-29 | MIN-005 |
| T-10 | `pkg/providers::TestFailureClassDataset` (DS-NET) | Unit | S-80, S-86 | classification table |
| T-11 | `pkg/providers::TestRetryDecisionsUnchangedByClass` (existing fallback datasets re-run, outcome equality incl. cancellation and partial output) | Unit | S-85 | FR-063 |
| T-12 | `pkg/agent::TestProviderSaysExtraction` (DS-EXTRACT; row 17: the whole response is masked before the first line is selected, D81) | Unit | S-50, S-56, S-92 | extraction and order: mask whole response → select → mask → normalise → mask → cap |
| T-13 | `pkg/agent::TestMaskingContract` (DS-SEC, M1–M6, row-specific outcomes: masked / documented limit, row 13 (D82) / dropped) | Unit | S-90, S-91, S-92, S-94 | masking |
| T-45 | `pkg/agent::TestPerCodeFactsAndVariants` (DS-FACT: every code × no-facts / provider / quoted / retrying / final; `message_assembled` set only by the assembly, for every code; an actual assembled payload driven through `getLLMErrorDisplay` and the replay accessor live and after reload, incl. `judge_conclusion_failed` with its reason and rejection of unflagged raw text; `provider_stalled` N2 final with and without host; no status code at Normal; fallback note saved per turn and model pair) | Unit | S-56, S-57, S-58, S-72, S-107, S-132 | MAJ-008 |
| T-14 | `pkg/agent::TestCatalogueMatchesWordingBaseline` (in-chat and outside-chat forms; N2 lines; negative fixtures: a template with an unknown slot and a `{fix}` value outside its list both fail generation; copy-rule tests in `pkg/agent/translate_error_test.go` and `src/lib/llm-error.test.ts`) | Unit | S-72, S-70, S-77, S-78 | baseline, both voices |
| T-15 | `pkg/agent::TestNetworkSplitCodes` (`network` vs `provider_error`) | Unit | S-51, S-80 | R-EV-2 |
| T-16 | `scripts/check-system-message-voice.sh` self-check: one planted fixture per check and surface fails (incl. "please" outside the D48 sentence); the clean fixture passes | Unit | S-71, S-73, S-74 | the guard can fail |
| T-47 | `pkg/notifications::TestWordingManifestComplete` (every id has a disposition; survivors have text and a self-check receipt; no unlisted string in a scanned source; independent whole-tree inventory of emitter call sites reconciled to the manifest's source list; a planted bad string in an unlisted source fails) | Unit | S-75 | MAJ-018 |
| T-37 | `pkg/agent::TestTaskRunClassification` + `TestTaskOutcomeIndependentOfRunHistory` (Calendar scheduled, automatic, manual, user-stopped, dispatch failure, plan origin; origin captured at `runTask`; outcome only from an applied `completeTaskWithResult`; run open failure and run close failure still notify once; trigger edited mid-run keeps the dispatch class; duplicate close raises nothing; every terminal path enumerated) | Unit | S-01, S-09, S-17, S-18, S-129 | MAJ-004, R2-MAJ-009 |
| T-38 | `pkg/agent::TestProviderAttemptObservedOncePerDispatch` (nested retries, fallback, exclusions; the call-started flag: an inspection-image attachment failure and a context-window refusal are not observed, a dispatched unclassified failure writes one WARN with `unknown` and raises no incident, an adapter pre-send failure counts as `unknown`; lists every caller of the provider chat interface) | Unit | S-86, S-87, S-57 | MAJ-009, R2-MAJ-008 |
| T-46 | `pkg/gateway::TestIncidentStateMachine` (DS-INC: open, update, resolve by any model, 10-min re-open, restart seed, connections-failing threshold and its wording, 11 providers → 10 causes + 1 omitted, another chat's success leaves a still-retrying turn's line alone, disk through the non-blocking observer — notification-store writes excluded both ways, repeated no-space, recovery, queue overflow, no deadlock under the store lock — memory, model-specific excluded) | Unit | S-76, S-88, S-89, S-108, S-109, S-110, S-111, S-123, S-127, S-130 | MAJ-010, R2-MAJ-004/005/010 |
| T-17 | `src/lib/detailLevel.test.ts` (every L1–L11 × level, DS-DL, literal L6/L8) | Unit | S-60, S-65, S-66 | one rule |
| T-18 | `src/components/chat/SystemLine.test.tsx` (5 types with accessible prefix, in-progress, reduced motion, action, details by level and kept across level changes, no empty quotes, plain-text provider message, retry clause filled from typed facts while the server text stays unchanged) | Unit | S-50, S-54, S-56, S-62, S-63, S-131 | renderer |
| T-19 | `src/lib/systemLineAdapters.test.ts` (one row per §11.2 carrier) | Unit | S-50…S-55, S-107, S-117 | mapping |
| T-20 | `src/store/notifications.test.ts` (frame types, badge counts, volatile items: "Not saved" text, kept across refetch while the server runs, mark read / delete / clear) | Unit | S-11, S-14, S-22, S-125 | store |
| T-40 | `src/store/notifications.reconcile.test.ts` (DS-RECON rows 1–16: the one ordering rule — tombstones, snapshot floors, stale snapshots, late upserts and late deletes, volatile order, volatile → saved, refetch on re-auth) | Unit | S-14, S-100, S-101, S-102, S-125 | MAJ-003, R2-MAJ-002/003 |
| T-41 | `src/lib/outageReport.test.ts` (DS-OUT-CLIENT: record, reload, pending queue kept in session storage and re-sent after a reload before the 204, queue ≤ 5 with the sixth dropping the oldest, backoff, 204/400/429, 24 h drop) | Unit | S-112, S-113, S-114, S-115 | MAJ-011 client |
| T-43 | `src/lib/notificationTargets.test.ts` (DS-TARGET: every screen and action with its required ids; missing target text; knowledge drift through the real `KnowledgeLifecycle` holders: one mount, several mounts, mount removed, no mount) | Unit | S-05, S-59, S-128 | MAJ-013 |
| T-21 | `src/components/ui/popupHost.test.tsx` (DS-PU: durations, hover and focus pause with remaining time, cap, "+N more" opens the centre, queued full duration, resolve removes, once-only announcement) | Unit | S-40, S-41, S-42, S-47, S-48 | pop-up rules |
| T-22 | `pkg/gateway::TestNotificationRESTDeleteClearPrefs` (204/404, no 409 on delete, preference codes 400/409) | Integration | S-22…S-24, S-33, S-34, S-38, S-39, S-104 | REST contract codes |
| T-42 | `pkg/gateway::TestPreferencesRevisionAndInheritance` (DS-PREF) | Integration | S-37, S-38, S-39 | MAJ-012 |
| T-23 | `pkg/gateway::TestNotificationChangePushedToAllRecipientConnections` (all four frame types, batches, recipient-only) | Integration | S-10, S-11, S-22 | cross-tab push |
| T-24 | `pkg/gateway::TestEveryKindHasLiveProducer` (one subtest per Part-1 kind driving the **real** producer of §8.1 — Calendar trigger, gateway runner, run lifecycle, plan engine, approval and card paths, attempt observation, memory hook, the disk hook driven by real writes failing with no space (a fault injected below `WriteFileAtomic`, through a session write and through the notification store's own write), the knowledge-drift producer through the real mount registry — reading `GET /notifications`) | Integration | S-01, S-05, S-18, S-19, S-109, S-128, S-129 | reachability (19 kinds) |
| T-25 | `pkg/gateway::TestApprovalResolvedResolvesNotification` / `TestAskTerminalResolvesNotification` | Integration | S-10, S-12, S-13 | D19 |
| T-26 | `pkg/gateway::TestProviderIncidentOneEntryAffectedChats` (incl. two chats: B recovers while A still retries or then fails — only the incident and B's line resolve) | Integration | S-04, S-130 | R-EV-3/4 |
| T-27 | `pkg/gateway::TestOutageReportValidationIdempotencyRateLimit` (DS-OUT incl. the LRU eviction bound) | Integration | S-81, S-82, S-83, S-112 | R-LOG-3 server |
| T-28 | `pkg/gateway::TestSystemLineHistoryContract` (persist, replay with `history_origin`, prepared page byte weight, REST `Message.system_line`, live/history semantic parity on persisted fields, live-only fields absent from replayed and paged copies, dedupe by `line_id`) | Integration | S-55, S-106 | MAJ-002 |
| T-29 | `pkg/agent::TestPerAttemptWarnLine` + `TestIncidentStartEndLogged` (log capture at level `warn`) | Integration | S-80, S-84, S-87 | R-LOG-1/2/4 |
| T-30 | `pkg/gateway::TestScheduleFailedBodyHasNoRawError` | Integration | S-01, S-90 | masking + translation |
| T-51 | `pkg/gateway::TestSplitRegisteredValueMaskedThroughRealPath` (DS-SEC row 15: a fake provider HTTP server — the only stub, at the network boundary — returns the DS-EXTRACT row 17 `text/plain` error body; the real provider client, error capture, extraction, masking, transcript write, notification router, logger and replay run unchanged; each of the six surfaces is reached through its real producer — a chat turn failing finally (main line, technical details, log line), the same body on a fallback hop answered by a second fake model (fallback note), a Calendar-scheduled task failing (centre body, §15.4), and a reload (replayed history); asserts that no substring of 4 or more characters of the registered value appears on any surface and that `[redacted]` does; mutation check: selecting the first line before masking must make it fail with `AbC123x` visible) | Integration | S-90, S-92 | D81, R2-MAJ-001 |
| T-44 | `pkg/gateway::TestPart1NoNewOutboundSend` (the router never publishes outbound) + `TestPublishChannelAlertUnchanged` (characterisation of today's alert through the real `scheduledRunner` failure path) | Integration | FR-090 | D62 boundary |
| T-52 | `pkg/agent::TestTurnFailureKindClassification` (#1857; RED first: the §13.8.2 fixture set — every C1–C7/C9 CLI shape returns its proposed code from the **CLI-only classifier** and never `unknown` or a provider/network code, while `classifyByMessage` stays byte-identical (its existing provider fixtures classify unchanged); the A4/A5 narrowed residuals return `provider_error`/`provider_request_rejected` at the terminal presentation only; both media gates keep their actual inputs — `pkg/agent/media_downgrade.go::outcomeFallbackEligible` consumes the recovery classification (`ProviderError` + code), `pkg/agent/turn_transcript.go::outcomeRelabelApplies` consumes the **presentation code after `TranslateTurnError`** at the persist/emit point and keeps accepting the residual class exactly as it accepts `unknown`/`provider_request_rejected` today — with the promotion ordering explicit so a promoted `provider_error` never reaches the relabel predicate early and suppress the media label; the strip-retry-then-residual sequence and a distinct-later-typed-failure control are asserted on **live and persisted** outcomes, not on unchanged function bodies; retry decisions and incident eligibility unchanged; a pre-send/no-status attempt is NOT promoted (R-4); the reserved case of S-135 still returns `unknown`; dispatched/no-status and pre-send controls asserted separately) | Unit | S-133, S-135, S-139 | FR-092, OQ-1857-4 |
| T-53 | `pkg/agent::TestSanitizeRunnerErrorSafeFacts` (#1857, agent-level unit layer only — the wire/persistence/parent assertions live in T-56; a CLI stderr tail **and a stdout error envelope** with a path, a flag-with-value and a key-shaped string: the sanitized output carries the safe facts and the matched kind, no raw text, gateway.log keeps the raw text; the §13.8.3 lines pass the voice guard and the copy-rule constraints, both voices, all resolved variants and no-fact forms, closed CLI display names, per-code attribution fixtures) | Unit | S-134, S-136, S-137 | FR-093 |
| T-54 | `pkg/agent::TestCodeUnknownPayloadGuard` (#1857, the corrected F-1 guard — MAJ-004; RED first: three separate assertions. (1) **Untrusted text is never displayed**: an attached message on a `CodeUnknown` frame never renders through `WireLLMError` → `getLLMErrorDisplay`. (2) **Payloads are catalogue-clean**: every `CodeUnknown` payload in the T-54 allow-list — **exactly two named groups** (final list per the post-check): A4/A5's non-promoted dispatched-provider residuals (genuinely unclassified), and the six **B24** record-save refusals — a known class of cause (a record could not be saved) that #1857 deliberately does not re-key; each entry names its site (`loop_run_turn.go` ×2, `cancel.go`, `started_tool_outcome.go`, `loop_run_turn_tools.go`, `loop_window.go`) and its reason for staying generic, so no emitter hides. The B10 duplicate site is removed from the list (it carries `tool_assembly_duplicate` with two MCP-keyed variants). Every listed payload contains the catalogue fallback copy, never authored text. (3) **Mutations fail**: a new unlisted emitter fails, a regression inside an allowed emitter that swaps its catalogue fallback for authored text fails, and a mutation that restores a known-cause site (the B10 builtin-only duplicate) to the generic result fails) | Unit | S-138 | FR-092 |
| T-55 | `pkg/agent::TestKnownCauseKindsProduceTypedOutcomes` (#1857; RED first, the positive oracle MAJ-004/MAJ-005 demand, tightened in grill round 2 to the real callers and re-bound to the post-check's corrected bindings: every §13.8.2 kind this amendment moves off the generic line — B3 (founder E2), B5–B7 (both sites), B10–B17, B19, B21, B22, B23, B25, B26 (the sec2 rows: their fixed sentences are WP-6 manifest copy like every other row, but the no-raw-text assertion — no substring of the raw error on any emitted or persisted surface — is copy-independent and does not wait for the E4 review), C1–C9, C8 — is driven against its triggering fact from its matrix §13.8.5 row and produces its row's code, attribution and sentence **live and after transcript reload**; per-code variant and no-fact forms render their own agreed text; **both B10 fixtures are positive** — MCP-involved and no-MCP-involved, the only source fact the detector records — and a mutation restoring either to the generic result fails; the save-stage rows assert the execution outcome at the real caller: B19's tool keeps running **and the done frame reports failed** (`markTurnFailed`), B23 stops the turn with `TurnEndStatusError` and no new tool round admitted, B11's W15 is asserted **only with the non-empty-response fact present** (a WP-0 emitter requirement) and never on the direct `Sessions.Save` failure, B21 is driven on both producers — the direct Save from a stopped-silent turn and the `recordIterationLimitNotice` failure — asserting W15 never shows, B20's done frame asserts `TurnFailed`, B22's fixture runs through `prepareToolSurface` and asserts the refusal happens before any provider call, B7's steering fixture runs through the steering admission site and asserts no "not sent/nothing ran" claim) | Unit | S-139, S-140 | FR-092 |
| T-56 | `pkg/agent`+`pkg/gateway`+`src` integration: `TestCLIFailureAdaptersEndToEnd` (#1857; MAJ-006; RED first: from a **fake CLI process/output boundary** through the real parser → dispatcher → error assembly → persistence → renderer: (a) duplicated-flag rejection via Additional CLI arguments → helper chat W1 **and the real Settings handler → W1's neutral form with a failed-test status and no flag/value text in the serialized result, paired with a corrected-arguments success control** (Settings coverage added in grill round 2, R2-MIN-001); (b) nested `error.type: provider.no-route` stdout envelope → helper chat W2 **and** the Settings handler → W2's neutral form (not the old translator); (c) a direct start failure → W7; (d) the actual parent completion route (`steer_completion.go::completionMessage`) carries the typed cause; (e) task/centre neutral copy; (f) live/reload parity on serialized responses and persisted records; (g) the nonfatal-diagnostic successful run shows no terminal sentence **and no emitted/persisted diagnostic line** (the removal asserted), while the Settings drain keeps its **retained** any-error-fails rule (`ok: false`) with the truthful pinned copy — never claiming the diagnostic proves a failed CLI run (R2-MAJ-005); (h) stderr/stdout raw text absent from every serialized/persisted surface, gateway.log keeps it; mutations that discard nested metadata, leave Settings on the old translator, re-add the diagnostic line to a successful run, or lose the typed code on the completion route fail) | Integration | S-141, S-142, S-143 | FR-092, FR-093 |
| T-31 | `tests/e2e/notifications-centre.spec.ts` (filters, navigate, states, delete, clear all, two tabs, offline delete reconcile) | E2E | S-03, S-05, S-11, S-22, S-24, S-43, S-100 | user path |
| T-32 | `tests/e2e/notifications-needs-you.spec.ts` (approval in another tab, resolve everywhere, D70 rule, no modal, delete keeps request) | E2E | S-10, S-23, S-45, S-46, S-97 | D19, D30, D80 |
| T-33 | `tests/e2e/notifications-preferences.spec.ts` (switches, locks, workspace tri-state, unreadable, conflict) | E2E | S-30, S-31, S-32, S-34, S-37, S-39 | preferences UI |
| T-34 | `tests/e2e/chat-system-lines.spec.ts` (fake provider: refused → recover; final failure with host; 401 echo with auth remedy and `provider_error` entry; fallback quote incl. a later reply with the same pair and another cause; two chats, one recovering; browser-only connection loss vs gateway stop; levels) | E2E | S-50, S-51, S-52, S-53, S-55, S-58, S-76, S-107, S-124, S-130 | system lines, D84 |
| T-35 | `tests/e2e/chat-detail-levels.spec.ts` | E2E | S-60, S-61, S-62, S-63, S-64 | detail levels |
| T-36 | `tests/e2e/own-action-results.spec.ts` (sampled sites per WP-5 category, journeys of §16 incl. Team Undo with the added row out of view) | E2E | S-44, S-95, S-96, S-116 | removals |
| T-49 | `tests/e2e/notifications-a11y.spec.ts` (keyboard-only journey, focus repair, announcements, 320 px and 400 % zoom) | E2E | S-118, S-119, S-120, S-121, S-47 | MAJ-017 |
| T-50 | `tests/e2e/page-indicators-and-homes.spec.ts` (new-version banner, panel Switch banner and its lifetime, preview helper text, unmeasurable-memory line) | E2E | S-49, S-98, S-99, S-110 | D77, D78 |

### 19.2 RED evidence and gates (round-1 MAJ-020)

- **RED proof.** Lane branches do not trigger full CI (root `CLAUDE.md`, "CI scope"), so a tests-only CI run is not available before the candidate. Every new test is shown red on the pre-change code by **one saved narrow local run** through the slot runner (`slotrun.sh <lane> <absolute-log> <command>`), with its exit code captured directly (`cmd > log 2>&1; echo "exit=$?"`); vitest one file at a time; Playwright one spec at a time. The log path goes in the test author's evidence table. Absence tests (deleted modal, deleted switch) are shown red by running them before the deletion.
- **Test the instrument.** Each guard and each masking test includes a planted failing fixture (T-16, T-13, T-47); each E2E spec asserts one element known to be present before trusting an absence.
- **Gates before the candidate**, each with a saved log: `npm run typecheck` (slot runner; never bare `tsc`), the design-system lock scripts in the order the `omnipus-design-system` skill gives, `make verify-contracts`, `make lint-guards` (includes the new voice guard), `make lint-budgets`, `gofmt` and `go vet` on touched packages, and `GOOS=windows go vet ./pkg/fileutil/... ./pkg/notifications/...` for the Windows lock path.
- **Candidate**: one full CI run on the frozen cut (D63), failed-job re-runs only; hands-on UAT (§24) on the exact candidate commit.

---

## 20. Test datasets and regression

### 20.1 Test datasets

#### DS-NET — failure classification (T-10, T-29, S-80, S-86)

| # | Input (attempt outcome) | Boundary | Expected class / reason | Traces to |
|---|---|---|---|---|
| 1 | `dial tcp: lookup api.example.invalid: no such host` | network, DNS | network / dns_failure | S-80 |
| 2 | `dial tcp 127.0.0.1:9: connect: connection refused` | network | network / connection_refused | S-80 |
| 3 | `connect: network is unreachable` | network, local | network / network_unreachable | S-80 |
| 4 | `tls: handshake failure` | network, TLS | network / tls_failure | S-80 |
| 5 | `net/http: timeout awaiting response headers` | timeout before response | network / timed_out | S-80 |
| 6 | `read: connection reset by peer` before headers | network | network / connection_dropped | S-80 |
| 7 | 200 then `unexpected EOF` mid-stream | D72 | network / connection_dropped | S-80 |
| 8 | 200 then 300 s silence (`provider_stalled`) | D72 | network / timed_out | S-80 |
| 9 | HTTP 408 | API 4xx timeout | api / http_status | S-80 |
| 10 | HTTP 429 `rate limit exceeded` | API | api / http_status (code rate_limited) | S-80 |
| 11 | HTTP 402 `Insufficient balance` | API | api / http_status (quota_billing) | S-80 |
| 12 | HTTP 500 empty body | API, no message | api / http_status, provider_says absent | S-56 |
| 13 | HTTP 503 `{"error":{"message":"overloaded"}}` | API | api / http_status, provider_says "overloaded" | S-80 |
| 14 | HTTP 529 | API, non-standard | api / http_status | S-80 |
| 15 | 200 + stream error event `{"type":"error","error":{"message":"Overloaded"}}` | API in-stream | api / http_status | S-80 |
| 16 | HTTP 400 `context_length_exceeded` | request-specific API | api / http_status; chat-only, no incident (D71) | S-123 |
| 17 | 200 with a body that is not the expected response | parse failure | api / invalid_response | S-80 |
| 18 | a dispatched request failing with an error matching no row above | unclassified | unknown / unknown; no incident | S-57 |
| 19 | Omnipus own rate limiter denial | not dispatched | not observed | S-86 |
| 20 | context cancelled by Stop during the attempt | not a failure | not observed | S-86 |
| 21 | candidate skipped by cooldown | not dispatched | not observed | S-86 |
| 22 | route build error before dispatch | not dispatched | not observed | S-86 |
| 23 | inspection-image attachment fails in `runProviderAttempt` before `Chat` | call not started (R2-MAJ-008) | not observed | S-86 |
| 24 | context-window check in `callProviderOnce` refuses the request before `p.Chat` | call not started | not observed | S-86 |
| 25 | the provider adapter fails to encode the request after the call started, before any byte is sent | started, nothing on the wire | unknown / unknown; WARN, no incident (stated limit, §14.2) | S-57 |

#### DS-EXTRACT — provider message extraction (T-12)

| # | Body | Expected `provider_says` | Traces to |
|---|---|---|---|
| 1 | `{"error":{"message":"Insufficient balance"}}` | `Insufficient balance` | S-50 |
| 2 | `{"error":{"error":{"message":"nested"}}}` | `nested` | S-50 |
| 3 | `{"message":"Model not found"}` | `Model not found` | S-50 |
| 4 | `{"error":"invalid_request"}` | `invalid_request` | S-50 |
| 5 | `{"detail":"Not Found"}` | `Not Found` | S-50 |
| 6 | `upstream connect error\nreset reason: overflow` (text/plain) | `upstream connect error` | S-50 |
| 7 | `` (empty) | absent | S-56 |
| 8 | `{"error":{"message":""}}` | absent | S-56 |
| 9 | 5,000-char ASCII message | stored exactly 2,000 bytes; main line 300 code points + "…" | S-50 |
| 10 | message with `\u0000\u001b[31m` control chars | control chars removed | S-50 |
| 11 | `<b>bold</b>` | `<b>bold</b>`, rendered as plain text | S-50 |
| 12 | `残高が不足しています` repeated past 2,000 bytes (3-byte code points) | cut at 1,998 bytes (the last whole code point), no broken character | S-50 |
| 13 | `{"error":{"message":"x"},"message":"y"}` | `x` (precedence) | S-50 |
| 14 | `{"status":"failed","code":17}` (valid JSON, no eligible field) | absent | S-56 |
| 15 | a registered key split as `sk-or-v1-0123` + newline + `456789abcdef` inside a JSON message field | `[redacted]` — the second mask after normalisation catches it | S-92 |
| 16 | `"Bad key"` with surrounding quotes | `"Bad key"` kept as text; the sentence's own quotes are not doubled | S-50 |
| 17 | `text/plain` body `upstream rejected key AbC123x` + newline + `yz987Qwe for this account`, where `AbC123xyz987Qwe` is a registered value with no known prefix (R2-MAJ-001) | `upstream rejected key [redacted] for this account` — the whole response is masked first (M2 matches across the line break), then the first line is selected; no substring of 4 or more characters of the value remains (D81) | S-92 |

#### DS-SEC — masking (T-13; row 15 also T-51; S-90…S-94)

| # | Echo | Rule | Must not appear | Traces to |
|---|---|---|---|---|
| 1 | `Incorrect API key provided: sk-proj-abcdEFGH1234` | M4/M5 | `sk-proj-abcdEFGH1234` | S-90 |
| 2 | `Incorrect API key provided: sk-or-v1-0123456789abcdef` | M4/M5 | `sk-or-v1-0123456789abcdef` | S-90 |
| 3 | a registered credential value, verbatim | M1 | that value | S-90 |
| 4 | `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc123` | M3/M4 | the token | S-90 |
| 5 | `key sk-ab rejected` (short, prefix-anchored) | M5 | `sk-ab` | S-91 |
| 6 | `Incorrect API key provided: sk-…wxyz` | M5 | `sk-…wxyz` | S-91 |
| 7 | `key sk-****abcd invalid` | M5 | `sk-****abcd` | S-91 |
| 8 | registered value split by a line break inside a JSON field (non-JSON first-line variant: row 15 and DS-EXTRACT row 17, D81) | M2 | either half | S-92 |
| 9 | `{"api_key":"AbC123xyz","model":"m"}` | M3 | `AbC123xyz` | S-90 |
| 10 | `token=Zx81Lk2Pq9` in a URL query | M3 | `Zx81Lk2Pq9` | S-90 |
| 11 | `ref 9f8e7d6c5b4a39281706f5e4d3c2b1a0` (32 hex, letters and digits) | M6 | the run | S-90 |
| 12 | `ghp_` + 36 chars | M5 | the token | S-90 |
| 13 | `…a1b2c3` (short, no known prefix) | documented limit (D82) | nothing — output equals input on every surface (the accepted, documented limit); the row exists so any change to it is visible | S-90 |
| 14 | text the masker cannot process | failure path | the text, on every surface — dropped; the no-quote sentence is used | S-94 |
| 15 | the DS-EXTRACT row 17 body: `text/plain`, registered value `AbC123xyz987Qwe` (no known prefix, shorter than M6's 24) split as `AbC123x` at the end of line 1 and `yz987Qwe` at the start of line 2; driven through the real path by T-51 (R2-MAJ-001) | M1/M2 on the whole response before selection (D81) | any fragment of the value: no substring of 4 or more characters of `AbC123xyz987Qwe` on any surface; `[redacted]` appears | S-92 |

Surfaces per row: main line, technical details, centre body, log line, fallback note, replayed history (6 surfaces). Expected outcomes are row-specific (round-2 R2-MAJ-013): masked for rows 1–12 and 15, the documented limit for row 13 (D82: output equals input), dropped for row 14. Row 15 runs through the real extraction-to-display path (T-51), not only the masker (T-13).

#### DS-PU — pop-up timing and cap (T-21)

| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | info at 4,999 ms | max−1 | visible | S-40 |
| 2 | info at 5,000 ms | max | gone | S-40 |
| 3 | warning at 9,999 ms | max−1 | visible | S-40 |
| 4 | warning at 10,000 ms | max | gone | S-40 |
| 5 | error at 3,600,000 ms | sticky | visible | S-40 |
| 6 | 0 pop-ups, 1 arrives | min | 1 visible | S-42 |
| 7 | 3 visible, 1 arrives | max+1 | 3 visible + "+1 more" | S-42 |
| 8 | 3 visible + 2 queued, 1 resolved | release | 3 visible + "+1 more"; the promoted one gets its full duration | S-48 |
| 9 | hover 60 s on an info pop-up 4,000 ms old | pause | visible during hover; gone 1,000 ms after leaving | S-41 |
| 10 | focus 10 s on an info pop-up 3,000 ms old | pause | visible while focused; gone 2,000 ms after leaving | S-47 |
| 11 | hover, leave, hover again | repeated pause | remaining time keeps decreasing only while not hovered | S-41 |
| 12 | `notification_cleared` for a showing pop-up | remote removal | gone in every tab | S-42 |

#### DS-DL — detail levels (T-17, S-60)

All 33 cells of §12.2 (11 rows × 3 levels), one test case each; expected value = the table cell, with L6 and L8 literal (D75). Plus two transition cases: Normal → Verbose after a live failure shows its details (S-63); Verbose → Normal after the reply ends hides the fallback note (S-66).

#### DS-OUT — outage report endpoint (T-27)

| # | Report | Expected | Traces to |
|---|---|---|---|
| 1 | 40 s, server_unreachable, last confirmed 1 s before start | 204, one WARN with observed 40,000 and max-possible 41,000 | S-81 |
| 2 | 0 ms (start = end) | 204 (boundary), WARN with 0 | S-81 |
| 3 | end < start | 400 `invalid_outage_report` | S-82 |
| 4 | last_confirmed_at after started_at | 400 | S-82 |
| 5 | duration 7 d + 1 ms | 400 | S-82 |
| 6 | end = server now + 6 min | 400 | S-82 |
| 7 | body 4 KiB + 1 byte | 400 | S-82 |
| 8 | 11th report within 60 s | 429 with `Retry-After` | S-83 |
| 9 | unauthenticated | 401 | S-82 |
| 10 | unknown field `note` | 400 (additionalProperties false) | S-82 |
| 11 | missing `report_id` | 400 | S-82 |
| 12 | same `report_id` twice | 204 both; one WARN line | S-112 |
| 13 | `report_id` X accepted, then 1,000 newer ids within 24 h, then X again | 204; X logged a second time — the stated duplicate bound (§14.4) | S-112 |

#### DS-OUT-CLIENT — browser report state machine (T-41)

| # | Sequence | Expected | Traces to |
|---|---|---|---|
| 1 | close → reconnect → POST 204 | record cleared, one POST | S-81 |
| 2 | close → reload → reconnect | one POST with `page_reloaded: true` | S-114 |
| 3 | POST times out after server accepted | retried with the same `report_id` after 5 s | S-112 |
| 4 | POST 429 `Retry-After: 30` | next try ≥ 30 s later | S-115 |
| 5 | POST 400 | dropped, console line, no retry | S-82 |
| 6 | 6 outages queued while POSTs fail | queue keeps the newest 5; a console line names the dropped one | S-114 |
| 7 | record older than 24 h | dropped | S-114 |
| 8 | page unload (user closes the tab) | no record created | S-81 |
| 9 | `navigator.onLine` false at close | `kind: offline` | S-81 |
| 10 | reconnect → POST sent → page reloaded before the 204 | the pending queue survives in session storage; the same `report_id` is re-sent; one server log line | S-114 |

#### DS-NAME — recipient file names (T-08)

| # | Username | Expected | Traces to |
|---|---|---|---|
| 1 | `dana` and `Dana` | two different files | S-93 |
| 2 | `ünïcödé` | one file `u_<64 hex>.json` | S-93 |
| 3 | `CON`, `nul` (Windows reserved) | ordinary hashed names | S-93 |
| 4 | 300-character name | 66-character hashed name | S-93 |
| 5 | `../etc` | hashed name inside the folder | S-93 |
| 6 | file whose stored username differs from the requester | treated as unreadable, quarantined, ERROR | S-93 |
| 7 | empty username | rejected (no file), ERROR | S-07 |

#### DS-RECON — browser reconciliation (T-40)

Notation: rev = `revision`, seq = `live_seq`; a snapshot is (R, L). Rows 8–16 are round-2 additions (R2-MAJ-002, R2-MAJ-003).

| # | Local state | Arrives | Expected | Traces to |
|---|---|---|---|---|
| 1 | E rev 5 | snapshot R = 9 without E | E removed | S-100 |
| 2 | F rev 11 (pushed) | snapshot R = 10 with F rev 8 | F stays at rev 11 | S-101 |
| 3 | tombstone G @ rev 7 | `notification_changed` G rev 6 | ignored | S-101 |
| 4 | tombstone G @ rev 7 | `notification_created` G rev 8 (re-created) | G shown | S-101 |
| 5 | volatile H (rev 0, seq 10) | snapshot (R = 9, L = 9) without H — read before H existed | H kept, "Not saved" | S-14 |
| 6 | cleared {A, B} applied | the same `notification_cleared` again | no change | S-102 |
| 7 | — | WebSocket re-auth | one refetch | S-100 |
| 8 | tombstone E @ rev 7 | delayed snapshot R = 6 containing E rev 5 | E stays removed | S-100 |
| 9 | snapshot R = 8 without E applied, so tombstone E @ 7 retired | late `notification_changed` E rev 6 | ignored (rev ≤ F_r) | S-101 |
| 10 | E rev 8 (re-created) | late `notification_deleted` E rev 7 | E stays at rev 8 | S-102 |
| 11 | E rev 5 | `notification_changed` E rev 9 | applied (known-positive control) | S-101 |
| 12 | snapshot R = 10 applied | snapshot R = 9 arrives late | ignored entirely | S-101 |
| 13 | volatile H (rev 0, seq 10) | `notification_changed` H resolved (rev 0, seq 11), then read (rev 0, seq 12) | both applied, in order | S-125 |
| 14 | saved E rev 5 | volatile E (rev 5, seq 13) from a failed update, then saved E rev 6 | the overlay shows "Not saved"; then rev 6 replaces it | S-07, S-125 |
| 15 | volatile H (rev 0, seq 10) | `notification_deleted` H (rev R, seq 14) after the user's delete | H removed in every tab; no 404 | S-125 |
| 16 | volatile H (rev 0, seq 10) | snapshot (R = 9, L = 12) without H — the server restarted | H removed | S-14 |

#### DS-FRAME — contract fixtures (T-48)

Each fixture is validated by the generated Go types **and** the generated Zod schemas; both must give the expected result.

| # | Fixture | Expected | Traces to |
|---|---|---|---|
| 1 | `notification_created` with `popup: true` | valid | S-103 |
| 2 | `notification_changed` `read` carrying a `popup` field | invalid (no such field) | S-103 |
| 3 | `notification_changed` `resolved` | valid | S-103 |
| 4 | `notification_created` without the `notification` object | invalid | S-103 |
| 5 | `notification_deleted` with id, `revision` and `live_seq` | valid | S-103 |
| 6 | `notification_deleted` with a `notification` object | invalid | S-103 |
| 7 | `notification_cleared` with 0 ids | invalid | S-103 |
| 8 | `notification_cleared` with 1,001 ids | invalid | S-103 |
| 9 | `Notification` with top-level `session_id` | invalid (moved into target) | S-103 |
| 10 | `Notification` target screen `none` | invalid | S-05 |
| 11 | `SystemLineFrame` historical copy without `history_origin` | invalid | S-106 |
| 12 | `NotificationPreferencesUpdate` without `base_revision` | invalid | S-39 |
| 13 | `notification_cleared` with 1,000 ids | valid | S-126 |
| 14 | `notification_changed` with `change: created` | invalid | S-103 |
| 15 | `Notification` with 11 `causes` | invalid | S-127 |
| 16 | `Notification` with 10 `causes` and `causes_omitted: 1` | valid | S-127 |
| 17 | `ProviderRetryFrame` without `text` | invalid | S-131 |
| 18 | `ProviderFallbackFrame` without `text` | invalid | S-107 |
| 19 | `LLMError` carrying the removed `provider_message` field | invalid | S-132 |

#### DS-INC — incident tracker (T-46)

| # | Sequence | Expected | Traces to |
|---|---|---|---|
| 1 | P fails (network) | `provider_unreachable` opens, pop-up | S-04 |
| 2 | P fails again in another chat | updated, count 2, no pop-up | S-04 |
| 3 | P succeeds with another model | resolved + restored entry | S-108 |
| 4 | P fails 9 min 59 s after resolve | same entry re-opened, no pop-up | S-88 |
| 5 | P fails 10 min 1 s after resolve | new entry, pop-up | S-88 |
| 6 | restart with the entry open, then P succeeds | resolved, no duplicate | S-89 |
| 7 | P1 unreachable, P2 unreachable | connections-failing opens with 2 causes | S-111 |
| 8 | then P1 recovers | connections-failing resolves | S-111 |
| 9 | P answers `model_retired` | no incident | S-123 |
| 10 | P answers 503 | `provider_error` opens | S-80 |
| 11 | P answers 402 | `provider_quota_exhausted` opens | S-80 |
| 12 | Omnipus limiter refuses | no incident | S-86 |
| 13 | a session write fails with no space; a later session write succeeds | disk opens; resolves | S-109 |
| 14 | memory measurement unavailable | no memory kinds ever | S-110 |
| 15 | recipient turned Notify off while open; P succeeds | entry still resolved | S-89 |
| 16 | P1…P11 unreachable | one connections-failing entry: 10 causes, earliest first, `causes_omitted` 1 | S-127 |
| 17 | chats A and B waiting on P; B succeeds while A is at attempt 3 | incident resolved; A's line unchanged; A's later final failure → its Error line, no record | S-130 |
| 18 | a notification-store write fails with no space (incident closed, then open) | no change to the disk incident either way | S-109 |
| 19 | two no-space session writes | one entry, updated once | S-109 |
| 20 | observer queue full | the event is dropped and counted; the next observed write sets the state | S-109 |

#### DS-PREF — preferences protocol (T-42)

| # | Stored | Update | Expected | Traces to |
|---|---|---|---|---|
| 1 | rev 4 | base 4, W.task_failed.popup off | 200, rev 5 | S-37 |
| 2 | rev 5 | base 4, anything | 409 `preferences_changed`, body rev 5 | S-39 |
| 3 | unreadable | base 0, no replace flag | 409 `preferences_unreadable` | S-38 |
| 4 | unreadable | base 0, replace true | 200, `load_state` ok | S-38 |
| 5 | W.task_failed.popup off | base n, popup inherit | 200, W dropped | S-37 |
| 6 | any | workspace override for `provider_unreachable` | 400 `system_wide_kind_global_only` | S-33 |
| 7 | any | `goal_paused.notify` off | 400 `notification_switch_locked` | S-34 |
| 8 | any | kind `foo` | 400 `unknown_notification_kind` | S-34 |
| 9 | any | unknown workspace id | 400 `unknown_workspace` | S-104 |
| 10 | override for a deleted workspace | next accepted update | override dropped | S-37 |

#### DS-FACT — per-code variants (T-45)

Every code of §13.7 × variants {no facts, provider/model known, `provider_says` present}, plus `network` and `provider_error` × {retrying with cause, retrying without cause, final}; expected value = the §13.2 / WORDING §1 sentence with slots filled; plus `provider_stalled` × {host known, host unknown}; assertion: no empty slot, no empty quotes, no status code on the main line, `message_assembled` true for every assembled variant (any code) and absent for unassembled text (the own-limiter row, raw provider text).

#### DS-TARGET — targets and actions (T-43)

One row per `screen` and per action `kind` of §7.6: required ids present → navigates to that object; one required id missing → contract invalid (DS-FRAME); object deleted → the "no longer exists" text; `retry_reply` for a turn that is no longer the latest → "This reply can no longer be retried." Knowledge drift (round-2 R2-MAJ-012), driven from a real `DriftReport.Root`: one mount → that mount; two mounts in `ws_b` and `ws_a` → `ws_a`'s; mount removed after the entry → "This knowledge base is no longer available here."; no mount at report time → no entry, WARN (S-128).

### 20.2 Regression requirements

| Existing behaviour | Existing test(s) | New regression test |
|---|---|---|
| Coalescing by `CoalesceKey` | `pkg/notifications/store_test.go` | extended by T-07 |
| Unreadable session owner never broadcasts (goal pause) | goal-pause notifier tests in `pkg/agent`, `pkg/gateway` | T-39 |
| Knowledge-drift notification content and recipients | drift notifier tests in `pkg/gateway` | none new; must stay green unchanged except kind/category fields |
| Retry / fallback / cooldown outcomes | `pkg/providers/fallback_*_test.go`, `error_classifier_test.go` | T-11 asserts outcome equality |
| `TranslateLLMError` classification of codes | `pkg/agent/translate_error_test.go`, `translate_error_billing_test.go` | updated for `provider_error`; every other code unchanged; T-45 |
| `WireLLMError` explicit-code override and the trust flag (renamed `message_assembled`, §11.4; the own-limiter row stays untrusted) | `pkg/agent` wire tests, `src/lib/llm-error.test.ts` | T-45 |
| Prompt-cache accounting at `accountProviderAttempt` | `pkg/agent/prompt_cache_accounting_test.go` (existing) | unchanged; T-38 adds the observation beside it |
| Provider retry countdown, once-per-attempt announcement, fallback note live behaviour | provider-messages tests | T-19, T-21 |
| Inline approval card and its resolution | `ToolApprovalPanel` tests, `tool_approval_resolved` gateway tests | T-32 |
| Session replay / history paging of system entries | `pkg/gateway/replay*` tests, chat-history-paging tests | T-28 |
| Stop behaviour (scope, 3 s window) | Stop tests | unchanged; only the notice surface moves (D68) |
| Scheduled-task channel alert (`publishChannelAlert`) | none today | T-44 characterises it so Part 1 cannot change it by accident |

---

## 21. Functional requirements

| FR | Requirement | From |
|---|---|---|
| FR-001 | The system MUST accept only the §6 kind ids; a producer using another kind MUST fail without storing or pushing anything and MUST log an ERROR. | R-EV-1 |
| FR-002 | Every Part-1 kind (19) MUST have its real producer of §8.1 wired at boot, proven by one integration subtest per kind that drives that producer and reads the entry through the REST list. | R-EV-1, DoD |
| FR-003 | The system MUST create a centre entry only when the recipient's effective **Notify me** for the kind is on. | R-NC-1, R-PREF-1 |
| FR-004 | Every entry MUST carry a category; the centre MUST filter by All / Needs you / Tasks & plans / System-wide, with counts. | R-NC-2 |
| FR-005 | All producers MUST go through one router that rejects unknown kinds, resolves recipients by §8.1 R1–R3, mints a non-empty id before any branch, never broadcasts when a session owner is unreadable, and, when storage fails, keeps the change as a volatile item with a full lifecycle per §8.4.2 (listed, updated, resolved, marked read, deleted and cleared without a 404, saved at the next successful write). | R-NC-5, #645, R2-MAJ-003 |
| FR-005a | Recipients MUST NOT be derived from task, plan or schedule attribution fields that may hold an agent's name. | R-NC-5, MAJ-005 |
| FR-006 | Every entry MUST carry a `target` with the ids its screen requires (§7.6); activating the entry or its pop-up action MUST navigate there, and a missing object MUST be stated, not acted on. | R-NC-6, R-PU-4 |
| FR-007 | Every lifecycle change (created, updated, read, resolved, deleted, cleared) MUST reach every connection of the recipient within 2 s through the four strict frame types of §7.2, removals of more than 1,000 ids in batches; every §6 resolve trigger MUST resolve the entry. | R-NC-3 |
| FR-008 | Entries whose age is ≥ 30 days MUST be deleted from storage at start-up, every 6 h and on writes — waiting Needs-you entries included — and quarantined corrupt files older than 30 days MUST be deleted. | R-NC-4, R-SEC-2, D80 |
| FR-009 | The count cap MUST never evict an unresolved Needs-you entry; delete and Clear all MUST work for every entry, waiting ones included, and MUST NOT approve, deny or cancel the underlying request. | R-NC-4, D80 |
| FR-010 | The browser MUST reconcile pushes, removals and snapshots with the one ordering rule of §8.4.3 (two counters, tombstones, snapshot floors), so no delayed snapshot, late upsert or late delete can resurrect, roll back or remove the wrong state, and MUST refetch on every WebSocket re-authentication. | R-NC-3, #645 |
| FR-011 | Provider failures MUST raise `provider_unreachable` for network-class failures and `provider_error` only for the provider- or account-level API conditions of D71, as distinct kinds; model-specific conditions MUST stay chat-only; the incident kind MUST be decided separately from the chat error code, so an authentication rejection keeps `provider_auth_failed` and its remedy in the chat. | R-EV-2, D71 |
| FR-012 | A chat MUST get a provider/connection line only if one of its turns was hit by the event. | R-EV-3 |
| FR-013 | A system-wide condition MUST produce one coalesced entry and at most one pop-up per episode, following the §14.3 state machine (10-min re-open without pop-up; restart keeps open entries open). | R-EV-4 |
| FR-014 | Preferences MUST be stored per user with global values and per-workspace, per-field overrides; resolution is workspace field → global → default; System-wide kinds MUST be global only (400 otherwise). | R-PREF-1, R-PREF-2 |
| FR-015 | Both switches of the three Needs-you kinds MUST be locked on; attempts to turn either off MUST return 400 `notification_switch_locked`. | R-PREF-3, R-PU-2, D67, D69 |
| FR-016 | A missing preferences file MUST mean the §6 defaults (`load_state: defaults`); an unreadable one MUST apply defaults, log ERROR, report `load_state: unreadable` and MUST NOT be overwritten without `replace_unreadable: true`. | R-PREF-4 |
| FR-017 | Settings MUST have a Notifications tab and the workspace Settings tab MUST have a per-field override section (Use global · On · Off), both saving with on-page results and the states of §9.3. | R-PREF-1, R-PREF-2 |
| FR-018 | Pop-ups MUST last 5,000 ms (info/success), 10,000 ms (warning), until dismissed or handled (error, needs_you); hover or keyboard focus MUST pause the countdown and leaving MUST resume the remaining time. | R-PU-1 |
| FR-019 | Needs-you pop-ups MUST always show, except in a tab where that chat is open and the tab is visible. | R-PU-2, D70 |
| FR-020 | At most 3 pop-ups MUST be visible; more MUST collapse into "+N more — open notifications", which opens the centre where every hidden pop-up is listed. | R-PU-3 |
| FR-021 | A pop-up MUST offer at most one main action, which navigates, plus close. | R-PU-4 |
| FR-022 | Results of the user's own actions MUST NOT raise a pop-up or a centre entry. | R-PU-5 |
| FR-023 | No event MUST open a modal; `ToolApprovalModal` and `INLINE_TOOL_APPROVALS_ENABLED` MUST be deleted. | R-PU-6, R-RM-2 |
| FR-024 | Browser-local conditions (the ten audited sites) MUST appear as persistent page or app indicators with their action, never as pop-ups or centre entries. | R-PU-5, D77 |
| FR-030 | One `SystemLine` renderer MUST render every in-chat system message, through one carrier → line adapter module. | R-SL-1 |
| FR-031 | Lines MUST be compact message units with the five types' icon, colour token, weight and accessible prefix per §11.1, wrapping without horizontal scroll. | R-SL-2, R-SL-3 |
| FR-032 | Any line type MUST support an animated in-progress state that honours reduced motion and is announced once per attempt, never per countdown tick. | R-SL-4 |
| FR-033 | A connection-problem line MUST show at every level while it lasts, naming the cause as soon as observed, the countdown and the attempt counter; on the turn's own recovery it MUST disappear at Normal and leave one record line at Verbose/Debug with that turn's retry count; a provider-wide recovery MUST NOT resolve or record another turn's line. | R-SL-5, D64 |
| FR-034 | Browser↔server loss MUST show one app-wide indicator and one line in the open chat. When the server is unreachable both MUST read "Can't reach the server right now. This page reconnects on its own when it's back." (D84). Neither is persisted, and neither MUST claim what the server or the agents are doing. After reconnecting, the user MUST see what happened (refreshed history of finished work, or the existing Interrupted notice if the gateway restarted). | R-SL-6, D84 |
| FR-035 | A line MUST carry at most one action with its required ids; a stale action MUST say so instead of acting. | R-SL-7 |
| FR-036 | New server-authored lines MUST persist as `Message.system_line` entries and pass every history stage of §7.5 (replay with `history_origin`, prepared page, REST history), rendering with semantic parity and de-duplicated by `line_id`. | R-SL-1, R-SL-5 |
| FR-040 | A Normal/Verbose/Debug detail level MUST replace the Verbose switch; one rule function MUST decide every row × level per §12.2 with L6 and L8 literal; changing the level MUST NOT discard details already received. | R-DL-1, D75 |
| FR-041 | Technical details MUST render only at Verbose (collapsed) and Debug (open); the masked failure payload is the only raw payload shown. | R-DL-2 |
| FR-042 | Every user MUST be able to select Debug; Debug MUST show the internal ids, request ids and full masked failure details that exist, and MUST NOT require a capture of successful provider traffic. | R-DL-3, D66 |
| FR-050 | The generated catalogues MUST equal the WORDING §1 sentences, with the `network` / `provider_error` lines of §13.2. | R-WD-1, R-WD-3, R-WD-9 |
| FR-051 | The provider's own message MUST be extracted per §13.3 (the whole response is masked before the message is selected, D81), masked, persisted, and quoted on the main line and in the fallback note; a sentence MUST never render an empty slot or empty quotes. | R-WD-2, R-WD-3 |
| FR-052 | A CI guard MUST fail on "Omnipus" as actor (except D48), on "we/our/us" in chat, on first person outside a chat, and on "!", emoji, "sorry", "oops", and "please" outside the D48 sentence, for every source in the wording manifest, and MUST be shown able to fail per check. | R-WD-5, R-WD-6, R-WD-7 |
| FR-053 | Messages outside a chat MUST be neutral; a message used both inside and outside MUST have both forms. | R-WD-6, R-WD-8 |
| FR-054 | Every new or surviving system message MUST be written with the `omnipus-voice` skill and carry a self-check receipt in the wording manifest; every one of the 392 review ids MUST have a disposition, and an independent inventory of emitter call sites MUST reconcile to the manifest's source list. | R-WD-4, R-WD-9, D65 |
| FR-055 | Final network-failure lines MUST name host, observed cause and one Fix; no message MUST claim the internet is down; several failing providers MUST be reported as "Connections to AI providers are failing" with the observed causes. | R-WD-1, D64 |
| FR-056 | Slot values MUST be taken from the facts at the emit site per §13.7; the server MUST assemble the finished sentence (retry frames: cause sentence in `text`, the one fixed retry clause filled by the browser from typed facts only), and only the assembly MUST set `message_assembled`, which the browser trusts for every code (§11.4). | R-WD-2, MAJ-008, R2-MAJ-006/007 |
| FR-060 | Every failed dispatched provider attempt MUST log exactly one WARN line at the `accountProviderAttempt` seam with `failure_class`, `reason`, provider, model, host, status (API), masked provider message, `attempt_seq`, `candidate`, `retry_round`, session, turn and request ids; attempts whose provider call never started (the call-started fact of §14.2) or that were cancelled MUST NOT be logged as failures. | R-LOG-1, R-LOG-2 |
| FR-061 | Failure class MUST follow the §14.1 table, computed independently of the retry classifier. | R-LOG-2, R-EV-2, D72 |
| FR-062 | The browser MUST report each server outage per §14.4 (report id, retries until accepted, 24 h limit, pending queue ≤ 5 kept in session storage); the server MUST validate, rate-limit, de-duplicate by report id within the stated LRU and restart bounds and log both observed and longest possible duration at WARN; the server's unexpected-close line MUST be WARN. | R-LOG-3 |
| FR-063 | Retry, cooldown and failover decisions MUST be identical before and after this change. | safety |
| FR-064 | Incidents MUST log start and end (duration, attempts, affected chats) at WARN. | R-LOG-1, R-LOG-4 |
| FR-065 | `provider_connections_failing`, `resources_low` and `concurrency_reduced` MUST be produced per §14.3 (no probe traffic; disk on no-space write failure through a non-blocking observer that never re-enters a caller's lock and ignores the notification and preference stores' own writes; memory only on measured hosts; more than 10 failing providers counted in `causes_omitted`), and an unmeasurable host MUST show the permanent Settings → Performance line instead. | R-EV-1, D64, D73, D74 |
| FR-066 | Removing a notification MUST NOT change any request; resolving MUST still work for an entry seeded from storage after a restart, whatever the recipient's current Notify switch. | R-NC-3, D80 |
| FR-067 | Task and schedule notifications MUST be published from the dispatch origin captured at execution entry and the applied completion write, independent of best-effort run-history writes, exactly once per execution, without inventing a `run_id` (§8.1). | R-NC-1, R-EV-1, R2-MAJ-009 |
| FR-068 | The fallback note MUST be saved once per (turn, unavailable model, answering model), so every reply changed by a fallback replays its own cause (§11.4). | R-WD-2, R-SL-1, R2-MAJ-011 |
| FR-069 | A `knowledge_drift` entry MUST target a real mount resolved from the drift report's root through the mount registry, chosen deterministically among several mounts, and MUST NOT be created when no mount holds the root (§7.6). | R-NC-6, R2-MAJ-012 |
| FR-070 | One masking function implementing M1–M6 MUST be applied before persistence and before the wire to every provider-derived text, on every surface and level, and to the whole provider response before the message is selected (D81); when masking fails the text MUST be dropped. Masking recognises known key shapes only; the one accepted limit (a short fragment with no known prefix, D82) MUST be stated on the security page. | R-SEC-1, D81, D82 |
| FR-071 | Deletion MUST remove data from storage. | R-SEC-2 |
| FR-072 | Every per-user file MUST be named by the SHA-256 of the exact username and MUST verify the stored username on read. | R-NC-5 |
| FR-073 | The outage endpoint MUST be authenticated, accept only enumerated fields, ≤ 4 KiB, ≤ 10/min/user with `Retry-After`. | R-LOG-3, security |
| FR-074 | Every mutation of a recipient's files MUST hold the process mutex and the sidecar lock; the Windows in-process-only limitation MUST be documented. | R-SEC-2, #645 |
| FR-080 | Each audited own-action pop-up MUST be handled per its recommendation; removal only where the page shows the outcome (indicator first, with Retry for failed saves); sites added since the audit MUST be audited first. | R-RM-1 |
| FR-081 | Undo (Calendar move, team add), the preview-link exposure text, the cross-tab Switch, the assignee warning and the status-frame message MUST have the page homes of §16 before their pop-ups are removed. | R-RM-1a, D78 |
| FR-082 | No badge MUST appear in the top-right corner, at any width. | R-RM-3 |
| FR-083 | Every new or changed surface MUST meet §16A (keyboard order, focus trap and repair, single announcements, reduced motion, 320 px reflow). | R-NC-2, R-PU-1…4, R-SL-2/4/7 |
| FR-090 | Part 1 MUST NOT add any outbound send; `publishChannelAlert` stays unchanged until Part 2 replaces it, pinned by a characterisation test. | D62, R-EXT-12 |
| FR-091 | Every behaviour change that a user sees MUST have the matching user-facing documentation update of §25 in the same cut, audited by docs-verifier. | DoD |
| FR-092 | **`unknown` never carries a non-catalogue message** — every Omnipus-written failure sentence travels either on its own typed catalogue code (with attribution) or through the assembly function with `message_assembled: true`; only the assembly function may set `message_assembled`, never a producer attaching free text. The guard is **three assertions, not one** (T-54): untrusted attached text is never displayed; residual `CodeUnknown` payloads contain the permitted catalogue fallback rather than authored text (the allow-list holds **exactly two named groups with a reason each** — A4/A5's non-promoted dispatched-provider residuals, and the six B24 record-save refusals that #1857 deliberately leaves generic with their sites named so none hides; the sites this amendment converts, the B10 duplicate included, are removed from it); and the mutations — a new unlisted emitter, a regression inside an allowed emitter, and a mutation that restores a converted site to the generic result — fail the build. Every known-cause kind of §13.8.2 MUST produce its matrix §13.8.5 outcome **live and after reload** (T-55). A **known safe reason** is: a typed sentinel matched, a structured fact present, or a pinned matcher matched — anything else stays in the reserved generic case (S-135). The CLI path classifies in its own CLI-only function (`classifyByMessage` byte-identical) and never yields provider or network codes (R-2); classification alone does not declare run termination (C11). Media-strip-retry preservation, stated at the gates' actual inputs: `pkg/agent/media_downgrade.go::outcomeFallbackEligible` keeps consuming the recovery classification and `pkg/agent/turn_transcript.go::outcomeRelabelApplies` keeps consuming the presentation code after `TranslateTurnError` at the persist/emit point, accepting the residual class exactly as today (`unknown`, empty, `provider_request_rejected`); the terminal promotion of A4/A5 is **ordered** so a promoted `provider_error` never reaches the relabel predicate early and suppress the media label, and retry decisions and incident eligibility stay untouched — asserted on live and persisted outcomes, including the strip-retry-then-residual sequence (T-52). *(#1857 amendment, corrected in grill round 1; exposure answers applied 2026-11-18 — founder E1 B/E2 A/E4 A, E3 A by chief ruling; the wording rows remain proposed under E4, §13.8.3)* | #1857, ADR-051 |
| FR-093 | **No CLI stderr on the wire — the status quo stays (R-7).** Raw CLI stderr — and raw stdout error-envelope text — MUST NOT cross the gateway/SPA boundary in any form: not on the main line, not quoted, not masked, not in technical details, not in the parent's tool result, not in the parent's completion message, not in any persisted or replayed record. The main line carries the fixed §13.8.3 sentence with safe facts (CLI display name, model, counts); the Verbose/Debug technical details carry the CLI's display name, the exit status and the matched kind; gateway.log keeps the raw text (`SanitizedRunnerError.LogText`), exactly as today. No file path, no command-line text and no secret anywhere user-visible; asserted at the serialization boundary on live frames, Settings responses, parent surfaces and stored records (T-56). **Decided 2026-11-18:** stderr stays off the wire at every level — the founder's answer E1 B (OQ-1857-1) keeps the outside program's own error text out of messages entirely, and the technical-details question (OQ-1857-7) was **not answered by the founder's form; the chief ruled "build A"** because it must fit E1 B: technical details carry only the facts — program name, exit status, kind of failure — never the program's text. The same fail-closed copy rule fixes the sec2 raw-text cases B25/B26. *(#1857 amendment, restated in correction pass 1, boundary coverage extended in grill round 1; decided as above)* | #1857, ADR-051 §RD5, R-SEC-1 |

---

## 22. Success criteria

| SC | Criterion |
|---|---|
| SC-001 | T-24 passes with one subtest per Part-1 kind (19 kinds), each through its §8.1 producer; 0 kinds without a producer. |
| SC-002 | In the two-tab E2E (T-32), a handled approval disappears from the second tab in ≤ 2 s in 20/20 runs. |
| SC-003 | Grep over runtime code only — `src/`, `pkg/`, `cmd/`, `internal/` (production and test files), with `docs/` and specs out of scope because they name these symbols on purpose: 0 definitions, imports or references of `ToolApprovalModal`, `INLINE_TOOL_APPROVALS_ENABLED`, `verboseChatEnabled` and `internet_lost`; the grep first finds a known-present symbol in the same scope (instrument check). |
| SC-004 | Own-action pop-up call sites after WP-5: only the 2 dev-only sites; every removed site has a matching page-indicator test or a "page already shows it" citation; the 10 local conditions each have an indicator test (T-50). |
| SC-005 | The voice guard passes on the real sources and fails on each planted fixture (exit code non-zero, 6 fixtures incl. "please"); T-47's inventory fails on the bad string planted in an unlisted source. |
| SC-006 | DS-SEC: 0 occurrences of any secret across rows 1–12, 14 and 15 × 6 surfaces (84 checks; for row 15, driven through the real path by T-51, no substring of 4 or more characters of the split value on any surface — D81); row 13 equals its input on all 6 surfaces (the documented limit, D82). |
| SC-007 | DS-NET: 22/22 rows as the table says; T-11 retry/fallback outcomes 100 % equal to pre-change. |
| SC-008 | With the default log level `warn`, a forced unreachable provider with 5 attempts produces exactly 5 "provider attempt failed" lines with `failure_class=network`, one incident start line and no end line while it keeps failing; a subsequent forced successful attempt then produces exactly one end line. |
| SC-009 | After a 30-day sweep, `notifications/` holds no entry and no quarantine file whose age is ≥ 30 days. |
| SC-010 | Every hands-on UAT row in §24 is PASS by the tester and upheld by the independent validator on the exact candidate commit. |
| SC-011 | The wording manifest lists 392/392 ids with a disposition; every surviving id has final text and a self-check receipt (T-47). |
| SC-012 | DS-RECON 16/16 and DS-OUT-CLIENT 10/10 rows pass; an offline-deleted entry is gone within 2 s of reconnect in T-31. |
| SC-013 | docs-verifier marks every claim on every §25 page (DOC-01…DOC-16) TRUE, with 0 missing pages and 0 missing TODOs. |

---

## 23. Traceability matrix — requirement → FR → scenario → test → documentation

| R-… / decision | FR | Scenario(s) | Test(s) | Docs (§25) |
|---|---|---|---|---|
| R-NC-1 | FR-003, FR-067 | S-01, S-02, S-09, S-17, S-129 | T-03, T-24, T-37 | DOC-01, DOC-05, DOC-06 |
| R-NC-2 | FR-004, FR-083 | S-03, S-118 | T-31, T-49 | DOC-01 |
| R-NC-3 | FR-007, FR-010, FR-066 | S-10, S-11, S-12, S-13, S-14, S-100, S-101, S-102, S-103, S-125 | T-07, T-20, T-23, T-25, T-32, T-40, T-48 | DOC-01, DOC-14 |
| R-NC-4 | FR-008, FR-009 | S-20, S-21, S-22, S-23, S-24, S-25, S-26, S-27, S-28, S-29, S-126 | T-04, T-05, T-06, T-22, T-29a, T-31 | DOC-01 |
| R-NC-5 | FR-005, FR-005a, FR-072 | S-07, S-08, S-15, S-16, S-93, S-125 | T-02, T-08, T-24, T-39, T-40 | DOC-01 (who receives what) |
| R-NC-6 | FR-006, FR-069 | S-05, S-18, S-19, S-43, S-128 | T-24, T-31, T-43 | DOC-01 |
| R-EV-1 | FR-001, FR-002, FR-065, FR-067 | S-06, S-05, S-01, S-109, S-110, S-111, S-129 | T-01, T-24, T-37, T-46 | DOC-01, DOC-03 |
| R-EV-2 | FR-011, FR-061 | S-50, S-51, S-80, S-123 | T-10, T-15, T-46 | DOC-08, DOC-09 |
| R-EV-3 | FR-012 | S-04 | T-26 | DOC-08 |
| R-EV-4 | FR-013 | S-04, S-13, S-88, S-89, S-108, S-127, S-130 | T-07, T-26, T-46 | DOC-01, DOC-08 |
| R-PREF-1 | FR-003, FR-014, FR-017 | S-30, S-31, S-36, S-110 | T-03, T-33, T-50 | DOC-01, DOC-03 |
| R-PREF-2 | FR-014, FR-017 | S-32, S-33, S-37, S-104 | T-03, T-22, T-33, T-42 | DOC-01, DOC-04 |
| R-PREF-3 | FR-015 | S-34 | T-03, T-22, T-33 | DOC-01, DOC-03 |
| R-PREF-4 | FR-016 | S-30, S-35, S-38, S-39 | T-09, T-33, T-42 | DOC-03 |
| R-PU-1 | FR-018, FR-083 | S-40, S-41, S-47 | T-21, T-49 | DOC-01 |
| R-PU-2 | FR-015, FR-019 | S-34, S-46 | T-32 | DOC-01 |
| R-PU-3 | FR-020 | S-42, S-48 | T-21 | DOC-01 |
| R-PU-4 | FR-006, FR-021 | S-43 | T-21, T-31 | DOC-01 |
| R-PU-5 | FR-022, FR-024 | S-44, S-49, S-116 | T-36, T-50 | DOC-01, DOC-02 |
| R-PU-6 | FR-023 | S-45, S-97 | T-32, T-36 | DOC-02 |
| R-SL-1 | FR-030, FR-036, FR-068 | S-50, S-55, S-106, S-107, S-117, S-132 | T-18, T-19, T-28, T-45 | DOC-02, DOC-14 |
| R-SL-2 | FR-031 | S-50, S-121 | T-18, T-49 | DOC-02 |
| R-SL-3 | FR-031 | S-50, S-51, S-52 | T-18 | DOC-02 |
| R-SL-4 | FR-032 | S-51, S-120 | T-18, T-49 | DOC-02 |
| R-SL-5 | FR-033, FR-036 | S-51, S-52, S-57, S-130, S-131 | T-18, T-26, T-28, T-34 | DOC-02, DOC-08 |
| R-SL-6 | FR-034 | S-53, S-124 | T-34 | DOC-02, DOC-09 |
| R-SL-7 | FR-035 | S-54, S-59 | T-18, T-34, T-43 | DOC-02 |
| R-DL-1 | FR-040 | S-60, S-61, S-63, S-65, S-66 | T-17, T-35 | DOC-02, DOC-03, DOC-09, DOC-10, DOC-13 |
| R-DL-2 | FR-041 | S-60, S-62 | T-17, T-18 | DOC-02 |
| R-DL-3 | FR-042 | S-62, S-64 | T-35 | DOC-02, DOC-11 |
| R-WD-1 | FR-050, FR-055 | S-58, S-72, S-76, S-127 | T-14, T-45, T-46 | DOC-08, DOC-09 |
| R-WD-2 | FR-051, FR-056 | S-50, S-56, S-107, S-132 | T-12, T-18, T-45 | DOC-02, DOC-08 |
| R-WD-3 | FR-050, FR-051 | S-50, S-72, S-77 | T-12, T-14 | DOC-08 |
| R-WD-4 | FR-054 | S-75 | T-47 (manifest receipts) | — (no user-visible effect; process) |
| R-WD-5 | FR-052 | S-70, S-71 | T-14, T-16 | — (wording rule; covered by every page's examples) |
| R-WD-6 | FR-052, FR-053 | S-70, S-73, S-78 | T-14, T-16 | — (as R-WD-5) |
| R-WD-7 | FR-052 | S-71, S-74 | T-16 | — (as R-WD-5) |
| R-WD-8 | FR-053 | S-70, S-78 | T-14 | — (as R-WD-5) |
| R-WD-9 | FR-050, FR-054 | S-72, S-75 | T-14, T-47 | — (process) |
| R-LOG-1 | FR-060, FR-064 | S-80, S-84, S-87 | T-29, T-38 | DOC-10 |
| R-LOG-2 | FR-060, FR-061 | S-80, S-86 | T-10, T-29, T-38 | DOC-10 |
| R-LOG-3 | FR-062, FR-073 | S-81, S-82, S-83, S-112, S-113, S-114, S-115 | T-27, T-41 | DOC-09, DOC-10 |
| R-LOG-4 | FR-064 | S-84 | T-29 | DOC-10 |
| R-SEC-1 | FR-070 | S-50, S-62, S-90, S-91, S-92, S-94 | T-12, T-13, T-30, T-51 | DOC-12 |
| R-SEC-2 | FR-008, FR-071, FR-074 | S-20, S-22, S-25, S-29 | T-04, T-06, T-29a | DOC-01, DOC-12 |
| R-SEC-3 | Part 2 (§29) | P2-S-03 | Part 2 | Part 2 |
| R-SEC-4 | Part 2 (§29) | P2-S-06 | Part 2 | Part 2 |
| R-EXT-1 | Part 2 (§29) | P2-S-01 | Part 2 | Part 2 |
| R-EXT-2 | Part 2 (§29) | P2-S-02 | Part 2 | Part 2 |
| R-EXT-3 | Part 2 (§29) | P2-S-05 | Part 2 | Part 2 |
| R-EXT-4 | Part 2 (§29) | P2-S-04 | Part 2 | Part 2 |
| R-EXT-5 | Part 2 (§29) | P2-S-01 | Part 2 | Part 2 |
| R-EXT-6 | Part 2 (§29) | P2-S-10 | Part 2 | Part 2 |
| R-EXT-7 | Part 2 (§29) | P2-S-03 | Part 2 | Part 2 |
| R-EXT-8 | Part 2 (§29) | P2-S-07 | Part 2 | Part 2 |
| R-EXT-9 | Part 2 (later requirement) | — | — | — |
| R-EXT-10 | Part 2 (§29) | P2-S-08 | Part 2 | Part 2 |
| R-EXT-11 | Part 2 (§29) | P2-S-06 | Part 2 | Part 2 |
| R-EXT-12 | Part 2 (§29); Part 1 FR-090 | P2-S-09 | T-44 (Part-1 boundary) | — (no Part-1 user-visible change) |
| R-RM-1 | FR-080 | S-44, S-95, S-116 | T-36 | DOC-01, DOC-02 |
| R-RM-1a | FR-081 | S-96, S-98, S-99 | T-36, T-50 | DOC-02, DOC-04, DOC-05, DOC-06, DOC-16 |
| R-RM-2 | FR-023 | S-45, S-97 | T-36 | DOC-02 |
| R-RM-3 | FR-082 | (UAT U14) | U14 | — (no change: no badge exists) |
| (safety) | FR-063 | S-85 | T-11 | — |
| D62 | FR-090 | — (negative: no outbound send) | T-44 | — |
| D63 (one cut) | — (delivery shape) | — | candidate CI + UAT receipts on the cut commit | — |
| D64 | FR-033, FR-055, FR-065 | S-51, S-57, S-58, S-76, S-111 | T-45, T-46, T-34 | DOC-01, DOC-08, DOC-09 |
| D65 | FR-054 | S-75 | T-47 | — |
| D66 | FR-042 | S-64 | T-35 | DOC-02, DOC-11 |
| D67, D69 | FR-015 | S-34, S-13 | T-03, T-22 | DOC-01, DOC-03, DOC-07 |
| D68 | FR-030 | S-117 | T-19 | DOC-02 |
| D70 | FR-019 | S-46 | T-32 | DOC-01 |
| D71 | FR-011 | S-50, S-123 | T-46 | DOC-08 |
| D72 | FR-061 | S-80 (DS-NET rows 7, 8) | T-10 | DOC-09 |
| D73, D74 | FR-065 | S-109, S-110 | T-24, T-46, T-50 | DOC-01, DOC-03 |
| D75 | FR-040 | S-60, S-65, S-66 | T-17 | DOC-02 |
| D76 | FR-009 | S-21, S-27 | T-05 | DOC-01 |
| D77 | FR-024 | S-49 | T-50 | DOC-01, DOC-02 |
| D78 | FR-081 | S-96, S-98, S-99 | T-36, T-50 | DOC-02, DOC-04, DOC-05, DOC-06, DOC-16 |
| D79 | FR-030 | S-50…S-55 | T-19 | — |
| D80 | FR-009, FR-066 | S-23, S-24, S-26, S-28 | T-06, T-32 | DOC-01 |
| D81 | FR-051, FR-070 | S-90, S-92 (DS-EXTRACT row 17, DS-SEC row 15) | T-12, T-13, T-51 | DOC-12 |
| D82 | FR-070 | S-90 (DS-SEC row 13) | T-13 | DOC-12 |
| D83 | FR-005, FR-005a | S-15, S-16 | T-39 | DOC-01 |
| D84 | FR-034 | S-53, S-124 | T-34 | DOC-02, DOC-09 |
| FR-092, FR-093 (#1857 amendment) | FR-092, FR-093 | S-133, S-134, S-135, S-136, S-137, S-138, S-139, S-140, S-141, S-142, S-143 | T-52, T-53, T-54, T-55, T-56 | DOC-02, DOC-08, DOC-12 (TODOs pending the founder's wording review, §13.8.3) |
| D85 (exposure decisions applied 2026-11-18 — E1 B, E2 A, E4 A; E3 A by chief ruling; wording rows still E4-pending) | FR-092, FR-093 | S-133…S-143 | T-52…T-56 | DOC-02, DOC-08, DOC-12 |
| (accessibility) | FR-083 | S-118, S-119, S-120, S-121 | T-49 | DOC-01 |
| (documentation) | FR-091 | every scenario mapped in this table's Docs column | docs-verifier audit | DOC-01…DOC-15 (DOC-15: index entries) |

Completeness check (scripted at round 1, §33 evidence): every FR of §21 appears in at least one row; every S-xx of §18 appears in at least one row; every user-visible behaviour scenario maps to a DOC-xx through its requirement row; rows marked "—" in the Docs column have no user-visible effect and say why.

---

## 24. Hands-on UAT rows (spec time; macOS; tester + independent validator)

Setup for every row: the exact candidate commit built and installed per the standing per-lane procedure; provider `openrouter` + model `deepseek/deepseek-v4.1-flash` (founder-set UAT model); tester and validator each on their own instance, account and private browser; screenshots show the workspace and the badge. The validator re-drives every PASS on its own instance before ruling and never confers with the tester first. Setup and version receipts are recorded separately from product PASS.

| Row | Steps | Expected | Evidence |
|---|---|---|---|
| U1 | Agent in chat A runs a command that needs approval (`bash` on Ask) while the user is on Settings in tab 1 and chat B in tab 2 | Needs-you pop-up in both tabs, sticky, one **Review** action; centre entry under Needs you; no modal | 2 screenshots + centre screenshot |
| U2 | Approve from chat A in tab 2 | Pop-up gone and entry "Handled" in tab 1 within ~2 s | screenshot pair with timestamps |
| U3 | Agent asks a question (ask-user card) in chat C; answer it from the centre's **Open** | Card answered, entry handled, pop-up gone | screenshots |
| U4 | Calendar task due in the next minute that succeeds; then a Calendar task whose agent has an invalid model; then **Run now** on the failing one | `schedule_finished` pop-up (5 s) and entry; `schedule_failed` sticky pop-up and entry; **Open** opens the task on its run; body has no raw error text; Run now gives a `task_failed` entry, not a schedule entry | screenshots + centre |
| U5 | Add a custom OpenAI-compatible provider with base URL `http://127.0.0.1:9/v1`; send a message with it | Animated amber line "I can't reach …: connection refused. Retrying in … s (attempt 2 of …)…"; one `provider_unreachable` entry + pop-up; after retries an Error line naming the host `127.0.0.1` and "Fix: check your network or proxy." with **Switch model** | screenshots; gateway log lines with `failure_class=network`, one per attempt |
| U6 | Edit that same custom provider's base URL to OpenRouter's API address with a valid key; send again in the same chat | "reachable again" entry for that provider (pop-up 5 s); the unreachable entry shows Handled; at Normal the old retry line is gone; at Verbose one resolved record line | screenshots at Normal and Verbose |
| U7 | Copy the OpenRouter provider with an invalid key `sk-or-v1-0000000000000000invalid`; send | Error line `OpenRouter rejected the API key: "…"` with the key masked; **Open settings** (chat code `provider_auth_failed`); one `provider_error` centre entry for OpenRouter (the account-level incident kind, §14.1); Debug shows ids and details with the key masked | screenshots at Normal/Verbose/Debug; log line `failure_class=api` with masked text |
| U8 | Settings → Notifications: turn **Task or plan failed** Notify off; make a task fail | No entry, no pop-up; the task chat still shows the failure | screenshots |
| U9 | Workspace Settings → set **Task or plan started** Notify to On; start a task there and in another workspace; then set it back to **Use global** | Entry only for the overridden workspace; System-wide kinds not offered; after Use global, no entry | screenshots |
| U10 | Try to switch off either switch of **Agent needs your approval** and of **Goal paused** | Switches locked with explanation | screenshots |
| U11 | Trigger 5 info events quickly (e.g. 5 short Calendar tasks finishing) | 3 visible + "+2 more — open notifications"; it opens the centre listing all 5; hover pauses; non-hovered vanish after 5 s | screen recording or timed screenshots |
| U12 | Stop the gateway for 20 s, then start it | App-wide indicator + chat line during the outage, reading "Can't reach the server right now. This page reconnects on its own when it's back." — no claim that agents keep working (D84); a reply that was in progress shows **Interrupted** after the restart, so the user sees what happened; both gone after; reload: not in history; gateway log has one "client connection outage" with `observed_duration_ms` about 20,000 | screenshots + log excerpt |
| U13 | Turn macOS Wi-Fi off for 15 s, then on | Offline line + indicator; outage logged with `client_outage_kind=offline` | screenshots + log |
| U14 | Visual sweep of every main screen (chat, workspace tabs, settings, library, mail), at full width and at 320 px | No badge in the top-right corner; unread count only in the sidebar account menu | screenshots |
| U15 | Save settings on 4 server-backed screens (Performance, Profile, Providers, workspace Settings), once succeeding, once failing (stop the server mid-save), then Retry. Chat → Detail level is saved per device in this browser with no server call, so it is not part of this row | No pop-up; on-page "Saved" / "Not saved — Retry" / "Saved" | screenshots |
| U16 | Delete one centre entry, then Clear all, in two tabs | Entries gone in both tabs and after reload | screenshots |
| U17 | Detail level Normal/Verbose/Debug on one chat with tool calls, a delegation, a backup-model reply and a failed reply | Rows per §12.2 (validator checks cell by cell for the rows present), including the fallback note hidden at Normal after the reply | 3 screenshots |
| U18 | Read the chat lines of U5/U7 and the centre entries | Chat lines say "I …"; centre/pop-ups neutral; no "we/our/us"; no "Omnipus" as actor; no "internet" claim | screenshots |
| U19 | Calendar: drag an event to another week, then navigate away from that week | Undo still available in the page's result area for 10 s; pressing it moves the event back; no pop-up | screenshots |
| U20 | Keyboard only: open the centre, filter Needs you, open an entry, delete another, Clear all, Escape | Every step reachable; focus returns to the account-menu button | screen recording |
| U21 | 320 px wide window: a chat with a long quoted provider error and an action, the centre, a pop-up, Settings → Notifications | No horizontal scrolling; actions visible and usable | screenshots |
| U22 | With an approval waiting, delete its centre entry, then approve in the chat | Entry deleted; card still pending until approved; approval proceeds; no entry reappears | screenshots |
| U23 | Stop the gateway, close the laptop lid for 2 min, start the gateway, open the lid | One outage line with `observed_duration_ms` and a larger `max_possible_duration_ms` | log excerpt |
| U24 | Two custom providers pointing at closed local ports, each used by one chat | One "Connections to AI providers are failing" entry listing both causes; no internet claim | centre screenshot |
| U25 | Open the Library panel in tab A, then in tab B; press **Switch** in tab B | Banner "Library is open in another tab." in tab B's panel area, staying until Switch succeeds; Switch focuses tab A (or the manual sentence shows); no pop-up | screenshots |
| U26 | Workspace team page: add an existing agent, scroll the added row out of view, press **Undo** in the result area | Result area "Added … to …." with Undo for 10 s; Undo removes the agent; no pop-up | screenshots |

Rows U1–U3, U5–U7, U12, U22 touch `pkg/agent` / gateway WebSocket handling and are mandatory before landing (root `CLAUDE.md`). Stable rows become E2E tests over time (T-31…T-36, T-49, T-50 cover U1–U3, U8–U11, U15–U17, U19–U22, U25, U26 in automated form).

#1857 amendment rows (run on the exact cut commit; MAJ-006):

| Row | Steps | Expected | Evidence |
|---|---|---|---|
| U27 | Give a CLI worker a duplicated flag via Worker profile → Runtime → **Additional CLI arguments**; run a delegation from a parent agent; open the delegation timeline; press **Open**; then run Settings → the CLI worker → **Send a test message** with the same duplicated-flag profile, and once with corrected arguments; reload the page | Helper chat shows the W1 line with the CLI's display name, no flag text; the parent timeline keeps "{agent} stopped without finishing" with **Open**; **Open** leads to the child's typed error line; the Settings result shows W1's neutral form as a failed test, no flag text (R2-MIN-001); the corrected-arguments run passes; no stderr text at Normal/Verbose/Debug; after reload the same lines replay | Screenshots: helper chat, timeline, child chat, Settings failed + success results, details at 3 levels; reload screenshot |
| U28 | Give a CLI worker a model its CLI cannot route; run a task in a chat; then Settings → the CLI worker → **Send a test message** | Chat shows the W2 line with the model name; the Settings result shows W2's neutral form (not the provider-overloaded copy); no raw CLI text on either surface | Screenshots: chat + Settings result |
| U29 | Keyboard/accessibility sampling of the new error variants (U27's and U28's lines) on the §16A carriers: tab to the error line's disclosure, open/close it, trigger the one-time announcement, check reduced-motion and wrapping at 320 px | Every step keyboard-reachable; focus visible and returns; the error line announces once, not on history scroll; no horizontal scrolling at 320 px | Screen recording + 320 px screenshots |

---

## 25. User-facing documentation (first-class deliverable)

The implementing leads draft every page below in the same cut (FR-091); `docs-verifier` audits each against the built behaviour before the gate; **a missing page or a missing TODO is a gate finding** (Important). The backend implementer drafts DOC-08…DOC-12; the frontend implementer drafts the rest (DOC-16 included); both review DOC-01 and DOC-14. The founder's answers NS1, OQ-1 and OQ-2 are applied (D81–D83); no DOC item waits on the founder.

| Id | Page | New / changed | TODOs |
|---|---|---|---|
| DOC-01 | `docs/notifications.md` | **new** | (1) The centre: where it is, the three categories and counts, "Handled", delete, Clear all, 30-day retention, what deleting does **not** do (a waiting approval keeps waiting). (2) Every kind with its default Notify / pop-up switches, the three always-on Needs-you kinds and why. (3) When pop-ups appear (durations, three at a time, "+N more", one action) and when they **never** do: results of your own actions, browser-only conditions (shown as banners or page notices instead), Needs-you in the chat you are looking at. (4) Who receives what with several accounts: every account for tasks, plans, Calendar schedules, sessions no signed-in person started, and system-wide events, until per-person access exists; a session a signed-in person started notifies only that person (D83). (5) Provider incidents: one entry per provider, re-opening within 10 minutes, "Connections to AI providers are failing", and that Omnipus never claims the internet is down. (6) Resources: memory and disk entries; the Performance line on hosts where memory can't be measured. (7) Keyboard use of the centre and pop-ups **Round-2 additions (R2-MAJ-014):** (a) "Not saved — lost if the server restarts": what it means, that the entry still works in every tab and survives a page reload while the server runs, and that a later successful save removes the mark (§8.4.2); (b) "Could not refresh — showing the last loaded list.": the list may be stale until Retry succeeds; (c) a failed delete or Clear all: the entry comes back with "Could not delete — Retry", and Retry repeats the action; a read state that could not be saved is shown as not saved, not as an error; (d) who receives task, plan and owner-less-session notifications — every configured account until per-person access exists (D83) |
| DOC-02 | `docs/using-omnipus-ui.md` | changed | Link to DOC-01. System lines in a chat: the five types, in-progress lines, actions, technical details. Detail levels Normal / Verbose / Debug in plain words, replacing every "Verbose chat" mention. Correct the approval paragraph ("Where an approval came from") that says pending requests appear "one at a time over the app" — approvals are inline only. Provider failures: the provider's own message is shown (masked); remove the "provider response details are live-only" caveat where `provider_says` now persists. The Stop outcome is a chat line. Browser-only notices (new version, out-of-sync chat, live-browser input) as page notices **Round-2 additions:** (a) in the existing "Panels beside chat" section: the banner "{panel} is open in another tab." — where it appears (the panel area of the tab you opened it in), that it stays until **Switch** succeeds or you close the panel in that tab, and the manual-switch sentence when the other tab cannot be focused (§16); keep the approval-origin correction above; (b) browser↔server loss: the exact line and banner text of §11.3 (D84), that they do not say whether your agents are still working, and how to tell afterwards (refreshed history shows finished work; a restart shows **Interrupted**, linking the existing "If the gateway restarts during a reply" section) |
| DOC-03 | `docs/settings.md` | changed | New **Notifications** tab (switches, locks, defaults table, "settings could not be read" state, "changed in another tab"); Chat → **Detail level** replacing Verbose chat (line ~119); Performance: the unmeasurable-memory line **Round-2 addition:** for an unreadable notification settings file, state that **Retry** reads the file again, and that the first save asks "Replace the saved settings?" and replaces the file only after you confirm (§9.3); never claim the unreadable file was recovered |
| DOC-04 | `docs/workspaces.md` | changed | Workspace Settings → "Notifications for this workspace": Use global / On / Off per switch; System-wide kinds are global only **Round-2 additions:** (a) the same Retry-versus-confirmed-replacement wording as DOC-03 for the workspace overrides, without claiming recovery; (b) in the existing "Adding agents" text (today "Added … · Undo"): the result area at the top of the team page, "Added {agent} to {team}." with **Undo** for 10 s, also when the added row is no longer in view (§16) |
| DOC-05 | `docs/calendar.md` | changed | Which Calendar events notify by default; Run now counts as a task result; the Undo result area after moving an event **Round-2 addition:** the result area at the top of the Calendar page keeps **Undo** for 10 s after a move, also when the moved event is out of view or you went to another week |
| DOC-06 | `docs/tasks.md` | changed | Which task and plan events notify; your own Board changes never notify; the assignee warning on the task card **Round-2 addition:** the assignee warning also shows in the task detail, and stays until the assignment changes |
| DOC-07 | `docs/goals.md` | changed | Goal paused is a Needs-you notification, always on (lines ~123/129 mention the bell); goal reached / given up are off by default |
| DOC-08 | `docs/providers-and-models.md` | changed | How provider failures read: network ("can't reach", with the cause, countdown and attempt counter; final line with host and one fix) vs API ("answered with an error"); quota and rate limit; which conditions raise a centre entry and which stay in the chat (model-specific); the backup-model note quoting the first model's error **Round-2 additions:** align the examples with the corrected remedies — a rejected key reads "rejected the API key" with **Open settings** (it also opens a provider-wide entry), 408/5xx/529 read "answered with an error" with **Fix: retry in a moment; if it repeats, switch model.**; no status codes in the Normal line (they are in Technical details); the stalled final line with host and Fix |
| DOC-09 | `docs/troubleshooting.md` | changed | "Provider unreachable vs provider error" decision help, with the cause words (connection refused, address could not be found …) and the matching fix; "Connections to AI providers are failing" (check network, proxy, DNS — the internet may still work); browser↔server loss lines; replace "Verbose chat" mentions (~84, 98, 192) **Round-2 additions:** the same auth-specific remedy and stalled examples as DOC-08; the truthful server-loss wording of §11.3 (D84) — the page cannot tell a dropped connection from a stopped server |
| DOC-10 | `docs/operations/observability.md` | changed | Log lines "provider attempt failed" (fields, one per dispatched attempt), "provider incident started/ended", "client connection outage" (observed vs longest possible duration), raised "connection closed unexpectedly" — level `warn`; how to grep network vs API; replace the "Verbose chat" mention (~26) **Round-2 additions (R2-MIN-003, R2-MAJ-011):** outage reporting is bounded, best-effort: at most 5 reports wait in a tab, the oldest dropped beyond that; a waiting report is kept across a reload and dropped after 24 h or when the tab closes; the server's duplicate check remembers the last 1,000 report ids for 24 h, so a repeat can be logged twice after a restart or after 1,000 newer reports; two tabs produce two reports; replace today's sentence "written once per session and model pair" (rows `provider_fallback` and the paragraph on the saved note) with "once per reply and model pair"; the disk-full entry ignores the notification store's own writes |
| DOC-11 | `docs/operations/debug.md` | changed | The chat's **Debug** detail level (ids, request ids, full masked failure details; **not** raw successful provider traffic — tracked issue) vs the gateway's `--debug` start mode; neither shows unmasked secrets — link the tracked issue #1546 (proposed target 2026-12-15) where the page says raw successful traffic is not captured |
| DOC-12 | `docs/security.md` | changed | What is masked in messages, details, the centre and logs (keys, partial echoes with a known prefix, bearer tokens, long opaque values); that a key stored in Omnipus is masked even when the provider's reply splits it across lines (D81 — this is not a limit and must not be described as one); a user-facing **limits** passage (D82): masking recognises known key shapes only, so a short fragment with no known prefix (for example the last few characters of a key, such as `…a1b2c3`) is not caught and is shown as the provider sent it; notifications deleted for real after 30 days or on delete |
| DOC-13 | `docs/agents.md` | changed | Replace the "Verbose chat" mention (~229) with Detail level; agents report in their own chats, notifications are a user surface |
| DOC-14 | `docs/protocol/websocket-protocol.md` | changed | The four notification frames and their fields (`change`, `popup`, `revision`, `live_seq`, `persisted`, `target`, `category`, `causes`), `system_line` frame with `history_origin`, `provider_retry` new fields, `LLMError.provider_says` / `failure_class` / `facts.host`, `provider_error` code **Round-2 additions (R2-MAJ-006, R2-MAJ-004, R2-MAJ-003):** the four notification frame types with `live_seq` and the batching of more than 1,000 removed ids; the ordering rule in one paragraph; `provider_retry.text` and the client-filled retry clause (countdown from `retry_at`/`sent_at`); `provider_fallback.text` and `replay_provider_fallback` with `provider_says` / `failure_class`; `message_assembled` replacing `provider_message`; which fields are persisted (`message`, `message_assembled`, `provider_says`, `failure_class`, `facts.provider`/`model`/`host`) and which are live-only (`detail`, `facts.http_status`, `facts.request_id`); `causes_omitted` |
| DOC-15 | `docs/README.md` and `docs/operations/README.md` | changed | Index entries for DOC-01 and the updated DOC-11; check that the existing index entry for `docs/previews.md` (DOC-16) still describes its section, and add entries for any page this cut creates |
| DOC-16 | `docs/previews.md` | changed | (round-2 R2-MAJ-014) The copy-link steps (today step 4, "Use the copy button on the card…"): the permanent helper text under **Copy link** saying who can open the link and until when (the link is the credential), and that the button's label reads "Copied" for 2 s instead of a pop-up (§16, S-99) |

No user-visible effect, so no page: the router refactor, file locking, the recipient file-name hash, the wording manifest, the voice guard.

**#1857 amendment (draft, §13.8):** DOC-02 (chat system lines) and DOC-08 (providers and models) gain the new turn-failure lines of §13.8.3 (chat and neutral forms), the `external_cli_*` codes' copy, the `{cli}` display-name mapping and the reserved generic-line case. The TODO detail also covers the corrected journeys: the Settings test result's new per-kind sentences (W1's neutral form for duplicated arguments — R2-MIN-001 — and W2's neutral form for model unavailability) and the parent **Open** journey (summary → **Open** → child error line, §13.8.4) — documentation for both is drafted with the code, not assumed from the tool-result fix. **Work outcome versus saved record (grill round 2, R2-MAJ-006):** the DOC-02/DOC-08 wording for save failures applies the same distinction `docs/tools.md::Background results` already states — a failed save or report is not proof that repeating the work is safe; the documented recovery for a completed-but-unsaved reply is to check the delivered result before repeating anything, never "run it again to restore history". DOC-12 gains nothing new (the fail-closed rule is unchanged) and keeps its masking passage accurate — masking continues to apply to provider messages (§13.3), while CLI stderr never crosses to a user surface at all (FR-093 as restated). These TODOs are fixed when the founder approves the wording — the amendment lands its documentation rows with the code, per FR-091; no documentation is claimed shipped at spec stage.

---

## 26. Integration boundaries

### AI providers (via the existing provider layer)
- **Data in/out**: requests out; responses, HTTP status, error bodies, stream error events in.
- **Contract**: unchanged; this feature only reads failures.
- **On failure**: classified network/API (§14.1); retries and fallback unchanged (FR-063).
- **Development**: a local fake OpenAI-compatible server for tests (refuse, 401 echo, 402, 429, 503, mid-stream drop, stall); real OpenRouter only in UAT.

### Browser (SPA) ↔ gateway
- **Data in/out**: REST (notifications, preferences, outage report), WS frames (`notification_created`, `notification_changed`, `notification_deleted`, `notification_cleared`, `system_line`, `provider_retry`, `provider_fallback`, `error`).
- **On failure**: SPA keeps live state; snapshots reconcile by revision and tombstones (§8.4); outage reports are retried with the same report id until accepted, rejected (400) or 24 h old (§14.4).
- **Development**: real gateway in integration/E2E tests.

### Operating system (memory, disk, network signals)
- **Data in**: memory pressure via `config.MemoryPressureHigh`; no-space errors from `fileutil.WriteFileAtomic` (D73); `network is unreachable` errors (a reason word only — never an "internet down" claim, D64).
- **On failure**: unmeasurable memory = no memory notification, a permanent Settings → Performance line instead (D74).
- **Development**: existing test hooks (`MemoryPressureHighFromSignalsForTest`).

### Messaging channels / email — Part 2 only (§29)

---

## 27. Evaluation scenarios (holdout — not referenced by the TDD plan or the matrix)

| # | Category | Setup | Action | Expected outcome |
|---|---|---|---|---|
| H1 | Happy | Fresh install, one workspace, two agents | Run a day of normal use (two scheduled tasks, one approval, one goal) | Centre holds only: the approval (handled), two schedule results, nothing else; no pop-up for any save |
| H2 | Happy | Provider outage of 3 min across 4 active chats | Restore the provider | Exactly one outage entry, one restored entry, two pop-ups total; 4 chats each had one line; at Normal no line remains |
| H3 | Happy | Detail level Debug on a long chat | Scroll and inspect any technical details | Internal ids and request ids visible; no secret anywhere in the DOM |
| H4 | Error | Notifications folder made read-only | Trigger a failing scheduled task | Live pop-up appears; after a reload it is gone; gateway log explains |
| H5 | Error | Provider returns a key echo in a non-JSON body | Fail a reply | Main line quotes the message with the key masked |
| H6 | Edge | Laptop sleeps 2 h during a running reply, wakes | Open the app | One outage report logged whose observed duration is short (the loss is only noticed after waking) and whose longest possible duration is about 2 h; reply state truthful; no stale pop-ups (corrected at round 1: the browser cannot observe time asleep) |
| H7 | Edge | Two browsers on the same account, one on Debug, one on Normal | Same failing chat | Each shows its own level; handling in one resolves in the other |


---

## 28. ADR-051 amendment (proposed text — D36 settles it; ADR-051 itself is not edited by this spec)

Target file: `docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md` (**ADR-051 — Provider-Capability-Aware Media Handling and User-Facing Error Translation**). To be appended by the architect as a dated correction when this spec is approved; the text below is exact. Revised at round 1: masking contract (MAJ-006), the live `detail` claim (MIN-004), the scope of `provider_error` (D71), the N2 lines (D64), the fallback note (MAJ-008).

> ### Amendment 2026-10-31 — the provider's own message on the main line (founder decisions D28, D36, D64, D71; notifications spec)
>
> **Status:** Accepted by founder decision D36 (2026-10-22), refined by D64 and D71 (2026-10-31) and by D81 and D82 (2026-11-05); takes effect with `docs/internal/specs/notifications-spec.md`. Supersedes RD5's sentence "Raw provider errors never persist and never cross to the SPA in production" **for one bounded field only**, RD7's display rule, and the operator decision Q2 of 2026-07-21 for that field. Also supersedes founder decisions D1 and D5 of `docs/internal/specs/provider-messages-spec.md` for the same field (D5's accepted residual fragments are replaced by the masking contract below). RD1–RD4, RD6 and the two-choke-point architecture are unchanged.
>
> **Context.** The founder's message review (2026-10-22) found that hiding what the provider said makes failures harder to act on: a provider's own words ("Insufficient balance", "Incorrect API key provided") are often the most useful part of the message (D26). ADR-051's fixed sentences were written to keep raw provider text off the wire after an incident where a full raw JSON body reached the chat. The incident's harm was the *raw body*, not the provider's *message*. (Correction of an earlier summary: the live `detail` field already carries a bounded, masked preview of the provider's text to the browser; it is not persisted.)
>
> **Decision.**
> 1. **`provider_says`.** The error shape, the replayed error and the fallback note gain one optional field: the provider's own error message, *extracted* from the response (a fixed list of JSON message locations, else a text body's first line, else a stream error event's message) **only after the whole response has been masked**, so a value split across lines is masked whole (D81) — never the raw body. The extracted message is masked, normalised, masked again and capped at 2,000 bytes on a character boundary, **persisted** in the transcript at the write choke point (`appendErrorTranscript`) and **replayed**, so a reloaded chat reads the same as the live one.
> 2. **Masking contract.** One masker applies before persistence and the wire, to every surface: registered credential values (also when split by whitespace), authentication headers and key-named fields, key-shaped tokens and JWTs, partial echoes anchored on a known key prefix, and long opaque letter-and-digit runs. Text the masker cannot process is dropped. Masking recognises known key shapes only; the one stated residual, accepted and documented for users by founder decision D82, is a short fragment with no known prefix.
> 3. **Main line.** The user-facing sentence quotes `provider_says` on the main line at every detail level, next to a short sentence of ours (what happened · why · what to do). When no provider message exists, the sentence omits the quote; it never shows empty quotes and never invents provider text. The server assembles the finished sentence from the facts at the emit site; the browser never composes wording, except the one fixed retry clause whose numbers (countdown, attempt, maximum) it fills from typed frame facts. Only the server's assembly marks a sentence as user-facing (`message_assembled`, replacing the provider-message flag), for any code; unassembled text is never shown as the main line.
> 4. **Catalogue.** ADR-051's fixed sentences are replaced by the founder-agreed wording baseline (`docs/internal/specs/notifications-and-chat-system-messages-wording-2026-10-22.md` §1), written and checked with the `omnipus-voice` skill. A qualified remedy ("switch to a model with a larger limit") stays allowed; unqualified model-shopping in a product or configuration message does not. Each catalogue entry carries explicit variants: no facts; provider and model known; provider message known; and for connection failures retrying and final. The single-source catalogue and both generators remain the only place copy lives.
> 5. **Network vs API.** The `network` code is split: `network` now means the provider could not be reached at all (DNS, refused, unreachable, TLS, timeout, a connection dropped before or during the answer); the new code `provider_error` means the provider answered with a provider-side error that has no more specific code (408, 5xx, 529). An authentication rejection keeps `provider_auth_failed` and its remedy; as an account-level condition it raises the provider-wide `provider_error` incident in the notifications centre — the chat code and the incident kind are decided separately. Model-specific conditions keep their own codes. The error shape gains `failure_class` (`network` · `api`) and the fact `host`. Retry and fallback decisions do not read either. Connection lines follow the N2 pattern: cause named as soon as observed, countdown and attempt counter on every retry line, final line naming host, cause and one fix; no message claims the internet is down.
> 6. **`detail` unchanged in kind.** The raw `detail` (status and bounded, masked body preview) stays live-only and is still never persisted. It renders only at the Verbose (collapsed) and Debug (open) detail levels, which replace the Verbose-chat switch.
> 7. **Voice.** In a chat, the product never speaks of itself as the actor: "I could not …" (the agent's perspective); outside a chat, neutral wording (D47, D50). The copy rule "no message tells the user to contact support" is relaxed by exactly one sentence: "please report it to the Omnipus.ai team" (D48).
>
> **Consequences.**
> - *Positive:* failures say what the provider said; reload parity for the message; the network/API distinction is visible to the user and in logs; connection failures say what to check.
> - *Negative:* provider-authored text (masked) is now stored in transcripts. A content-policy refusal may echo part of the user's own prompt — the transcript already holds that prompt, so the sensitivity class does not change. External notifications never carry it (notifications spec R-SEC-3). The masker may hide a long opaque id quoted inside a provider message.
> - *Neutral:* the masker becomes a shared function used on more surfaces; RD5's "raw never lands on disk" still holds for the raw body.
>
> **Alternatives considered.** (a) Keep provider text Verbose-only (provider-messages spec D1) — rejected by D28: the useful part is hidden from most users. (b) Persist the raw body masked — rejected: the 2026-07-21 incident class (a full JSON blob in the chat) returns. (c) Classify every provider message into a fixed sentence — rejected by D28 ("not force-classified"). (d) Keep the residual-fragment exception of provider-messages D5 for the Verbose view only — rejected: the field is now on the main line and persisted, so one contract must cover every surface.
>
> **Affected components.** `contracts/components/schemas/LLMError.yaml` (and its replay and fallback shapes), `pkg/agent/translate_error.go`, `pkg/agent/translate_error_provider_messages.go`, `pkg/providers/error_classifier.go`, `src/lib/llm-error.ts`, the chat error rendering.

---

## 29. Part 2 — external delivery (second step; not in this build, D62)

Described to the depth Part 1 needs; Part 2 gets its own grill round before its build. Requirements R-EXT-1…12, R-SEC-3, R-SEC-4 apply verbatim (REQ §5.10, §5.9).

### 29.1 Design outline

| Topic | Outline |
|---|---|
| Notification channel (R-EXT-1, R-EXT-3, D58, D61) | A channel configuration entry with a "notifications" purpose. The channel screens show its owner as **Notification System** (workspace) / **Notification Bot** (agent) — presentation only: no workspace, no agent, no sidebar entry, no model run |
| Recipients (R-EXT-4, D56) | The channel's existing Allow From list (`allow_from`) — mandatory and non-empty for a notification channel; enabling with an empty list is refused. Telegram-style "write to the bot first" hints in setup |
| Channel types (R-EXT-2, D58) | Every existing channel type, plus **email as a channel type** for notifications with its own sender settings (mail server, sender address) — not an agent mailbox (F13) |
| Third preference switch (R-PREF-1) | **Also send externally** joins the per-kind preference record (global `{notify, popup}`, workspace tri-state fields) as `external` (reserved name; Part 2 contract change); default per §6 "Ext." column, all **off until the user opts in** (R-SEC-4, R-EXT-11) |
| Router step 8 | After step 7 (§8.2), if `external` is on: enqueue for every enabled notification channel, each Allow-From recipient (R-EXT-5, D55) |
| Content (R-EXT-7, R-SEC-3, D27, D53) | Neutral title only, plus a link `{gateway.public_url}/…target` when `gateway.public_url` is set (`pkg/config/config.go::GatewayConfig.PublicURL`), else the title alone. Never chat content, file names, technical details or error payloads |
| Rate limit and batching (R-SEC-4, R-EXT-11) | Per channel: batch window and maximum messages per minute (numbers decided in the Part 2 grill); offline → dropped, the in-app entry is enough (D38) |
| Inbound to a notification channel (R-EXT-8, D59) | One fixed reply, no AI: "This chat only sends notifications. Open the app to act: {link}" |
| Acting from a channel (R-EXT-9, D52) | Later, with its own security review |
| Delivery failure (R-EXT-10, D60) | After the channel's retries: one grouped, self-resolving `notification_delivery_failed` entry per channel "Could not deliver notifications to {channel}: {reason}"; every failure logged |
| Old scheduled-task alert (R-EXT-12, D54, F11) | `scheduledRunner.publishChannelAlert` is replaced: its trigger (`onFailure`) and outbound send path (`msgBus.PublishOutbound`) are reused; `resolveAgentDefaultChannel` and the full-error text are removed |
| Same bot or number (R-EXT-6, D57, F12) | Must be verified per channel type before the Part 2 build. Inferred, not tested: the Telegram channel receives updates by long polling (`pkg/channels/telegram/telegram.go`, `UpdatesViaLongPolling`), and a bot token is expected to support only one poller at a time — so a second channel on the same bot could not also receive (affects R-EXT-8). WhatsApp linked-device limits: Unknown |

### 29.2 Part 2 acceptance placeholders (for traceability; expanded in the Part 2 grill)

| Id | Scenario | Requirements |
|---|---|---|
| P2-S-01 | An event with "Also send externally" on reaches every enabled notification channel, each Allow-From recipient | R-EXT-1, R-EXT-5 |
| P2-S-02 | An email notification channel sends with its own sender settings, no agent involved | R-EXT-2 |
| P2-S-03 | The external message contains only the title (and the link when `public_url` is set) | R-EXT-7, R-SEC-3 |
| P2-S-04 | Enabling a notification channel with an empty Allow From list is refused | R-EXT-4 |
| P2-S-05 | The channel screens show Notification System / Notification Bot; no workspace or agent exists | R-EXT-3 |
| P2-S-06 | A burst of 50 events produces batched, rate-limited sends; offline events are dropped | R-SEC-4, R-EXT-11 |
| P2-S-07 | A message sent to the notification channel gets the fixed reply, no model call | R-EXT-8 |
| P2-S-08 | Repeated delivery failure produces one grouped entry that resolves after a successful send | R-EXT-10 |
| P2-S-09 | A failed scheduled task notifies through notification channels; the old binding lookup is gone | R-EXT-12 |
| P2-S-10 | Same-bot second configuration works or is refused with a clear reason, per channel type | R-EXT-6 |
| — | Acting from a channel | R-EXT-9 (later requirement, no scenario now) |

### 29.3 Part-1 boundary and Part-2 obligations found at round 1

| Topic | Rule |
|---|---|
| No new send in Part 1 (FR-090, round-1 MAJ-020) | Part 1 adds no outbound send of any kind. The existing `publishChannelAlert` keeps its exact behaviour until Part 2 replaces it; T-44 characterises it through the real `scheduledRunner` failure path. Part 1 completion never claims R-EXT-12 |
| Mobile request #177 (round-1 OBS-001) | Part 2 is not automatically a superset of #177: device-level filters and chat-message pushes (`agent_dms`) are outside this kind catalogue. The Part 2 grill states the mapping of §9.5 and whether device filters are in scope |
| Channel-tool parity (round-1 OBS-002) | Part 1 adds no agent-managed property. Part 2's notification-channel settings, if agents can manage channels, need a parity table (UI action ↔ agent tool ↔ policy entry) under Hard Constraint #6 |
| `notification_delivery_failed` | Reserved; added to the kind enum by the Part 2 contract change, not now |

---

## 30. Founder questions

### 30.1 Answered (founder interview 2026-10-31)

| Question | Answer | Recorded as | Where in this spec |
|---|---|---|---|
| FQ-1 / Q1 — one cut for all of Part 1 | A: ordered work packages, one frozen cut, one gate, one landing | D63 | §1.3, §1.4 |
| FQ-2 — goal paused | A: third Needs-you kind | D67 | §6, §9.1, S-34 |
| FQ-3 — Stop outcome | A: chat Warning line; `stop_notice` removed | D68 | §6, §11.2, S-117 |
| FQ-4 — lock Notify too | A: both switches locked for the Needs-you kinds | D69 | §9.1, S-34 |
| FQ-5 — Needs-you in the open chat | A: no pop-up in the visible tab with that chat open | D70 | §10.1, S-46 |
| FQ-6 / Q3 — provider-wide API errors | A (review-revised): provider- or account-level conditions only; model-specific stay in the chat | D71 | §14.1, S-123 |
| FQ-7 — mid-answer drop | A: network | D72 | §14.1, DS-NET 7–8 |
| FQ-8 / Q4 — internet loss | N2 (Claude Code's pattern): no probe; "connections to AI providers are failing" with observed causes; never claim internet loss | D64 | §6, §13.2, §14.3, S-76 |
| FQ-9 — disk | A: on a no-space write failure | D73 | §14.3, S-109 |
| FQ-10 — unmeasurable memory | A: measured changes only; permanent Performance line otherwise | D74 | §14.3, S-110 |
| FQ-11 / Q5 — detail-level readings | Q5a A (literal L6), Q5b A (literal L8) | D75 | §12.2, S-65, S-66 |
| FQ-12 — count limit | A: keep 50 as a safety bound, never evicting a waiting entry | D76 | §8.5 |
| FQ-13 — remaining wording | N3: everything written with the `omnipus-voice` skill; founder reviews the pattern, not each message; the 163 are rewritten by the leads; no rating page | D65 | §13.1, §13.5 |
| FQ-14 / Q6 — browser-local conditions | A (review-revised = FQ-14 C): persistent page/app indicators | D77 | §10.2, S-49 |
| FQ-15 / Q7 — homes for pop-up-only functions | A (review-revised): stable page-level result area for Undo; the other homes as proposed | D78 | §16, S-96, S-98, S-99 |
| FQ-16 — one carrier or migrate | A: keep existing frames, new carrier only where none exists | D79 | §11.2 |
| FQ-17 — Debug L11 | N4 = A: Debug shows what exists; raw successful exchange deferred to a tracked issue with a target date — #1546 (proposed target 2026-12-15) | D66 | §12.2, S-64 |
| Q2 — delete while waiting | A: deleting or clearing never changes the waiting request; 30-day rule applies | D80 | §8.3, §8.5, S-23, S-24, S-26, S-28 |

### 30.2 Answered 2026-11-05 — NS1 (round 2), OQ-1 and OQ-2 (round 1)

Founder answers given 2026-11-01 / 2026-11-05, put by team-lead and recorded here 2026-11-05.

| Question | Answer | Recorded as | Where in this spec |
|---|---|---|---|
| NS1 — masking before first-line extraction (R2-MAJ-001, a security item put by team-lead) | A: mask secrets in the **whole** provider response first, then take the first line; add a test row with a key split across two lines through the real path (oracle: no fragment of the key visible) | D81 | §13.3, §15.1, §15.5, §28, S-90, S-92, DS-EXTRACT row 17, DS-SEC rows 8 and 15, T-12, T-13, T-51, FR-051, FR-070, SC-006, DOC-12 |
| OQ-1 — short key fragments without a known prefix | A: accept and document — masking recognises known key shapes only; short fragments with no known prefix are not caught; stated in the security section and the user-facing limits | D82 | §15.1, §15.5, §28, S-90, DS-SEC row 13, T-13, FR-070, SC-006, DOC-12 |
| OQ-2 — recipients for workspace objects and owner-less sessions | A: owner-less tasks, plans and schedules (and sessions no signed-in person started) notify every configured account until per-person access exists (#1421) | D83 | §3 N17, §8.1 R1 and R2, S-15, S-16, T-39, DOC-01 |

The questions as they were put (kept for the record):

**OQ-1 — Short key fragments without a known prefix** — answered A (D82).

Context and impact: the new masking contract (§15.1) catches full keys, partial echoes that start with a known key prefix (`sk-…xyz`), bearer tokens and long opaque values. It cannot recognise a short fragment that has no prefix, such as `…a1b2c3`, without also masking ordinary words and numbers. R-SEC-1 asks for partial key echoes to be masked; this is the one case the design cannot promise.

| Option | Decision |
|---|---|
| **A — Recommended, chosen** | Accept this residual and document it (security page DOC-12); the dataset keeps a row that makes any change visible |
| B | Hide every provider message that contains any 6+ character letter-and-digit run, showing only our sentence — safer, but many useful provider messages (model names, ids) disappear from the main line |

**OQ-2 — Who receives notifications for workspace objects and owner-less sessions** — answered A (D83).

Context and impact: a task's or plan's "owner" field can hold an agent's name as well as a person's, so it cannot safely pick a recipient (§8.1). Omnipus also has no per-account access control for workspaces today (#1421: every account can see every workspace). With several accounts, someone has to receive task, plan and Calendar notifications, and approvals in sessions that no signed-in person started (for example a heartbeat).

| Option | Decision |
|---|---|
| **A — Recommended, chosen** | Every configured account receives them, until a per-account access model exists (#1421); sessions started by a signed-in person still go only to that person |
| B | Add a new person-only "notify" field to tasks, plans and schedules, filled from the signed-in user who created them, and notify only that person (needs a contract change and a rule for objects created by agents) |

Process receipts (team-lead, 2026-11-01): N4's tracked issue is #1546 (proposed target 2026-12-15). The notifications Epic waits on the founder (E1). Still for team-lead and the chief, not founder questions: confirmation of the riders (#645 items, #1513, the Windows lock residual, AF-4).

### 30.3 Answered 2026-11-05 — W1

Founder answer given 2026-11-05, put by team-lead and recorded here the same day. Spec text only.

| Question | Answer | Recorded as | Where in this spec |
|---|---|---|---|
| W1 — the banner and chat line shown when the page loses the server | A: drop the sentence "Your agents keep working." New text, on both surfaces, when the server is unreachable: "Can't reach the server right now. This page reconnects on its own when it's back." After reconnecting, the user sees what happened | D84 | §11.2 catalogue row, §11.3, §13.1 VS-5, §13.5 FE-076, S-53, S-124, U12, FR-034, DOC-02, DOC-09 |

### 30.4 Open and decided for #1857 (owners per the architect read's Rulings table; the founder's answers of 2026-11-18 are applied)

The #1857 amendment's decisions. The architect's read ruled the contract shape (R-2, R-3, R-4, plus R-5's shape); those rows record the ruling, not an open question. The founder answered the grill round-1 questions on 2026-11-18 (team-lead's form, answers **E1–E6**): **E1 = OQ-1857-1 B** (fixed safe sentences only — no outside program's own error text in messages), **E2 = OQ-1857-5 A** (the own-rate-limiter correction B3/W11 is in scope), **E4 = OQ-1857-8 A** (the wording pattern is kept; W1–W16 remain proposed until the founder reviews the corrected example messages against it), and **E5/E6** amend the R-3/R-6 notes below. **E3 — OQ-1857-7 — was not answered by the founder's form; the chief ruled "build A" because it must fit E1 B** (a Verbose stderr surface would contradict E1's fixed-sentences-only decision); that row is marked accordingly, not as a founder answer. Q1's B and Q3's A are consistent — both keep stderr off the client. The founder's sec2 answer also folds the two raw-error-text failure cases into this amendment as rows **B25/B26** (§13.8.2), under the fail-closed copy rule: raw error text must not reach messages or chat history — the fixed safe sentence goes to the user, the raw text to the server log only.

| # | Question | Options | Status, recommendation |
|---|---|---|---|
| OQ-1857-1 *(Q1 — founder)* | May CLI error output be quoted on the main line the way a provider's message is? CLI error output can contain paths, arguments and credential material; the proposed fixed sentences solve #1857 without transferring that output to the client | **A**: permit masked/extracted CLI stderr quotations on the main line — requires an explicitly revised exposure design and security review · **B**: fixed catalogue sentences using approved safe facts only; no stderr quotation | **Decided — founder 2026-11-18 (E1): B** — an outside program's own error text never reaches the chat; fixed, safe sentences only. Consistent with OQ-1857-7 A |
| OQ-1857-2 | New wire codes for the CLI kinds, or map onto existing ones? | **R-2 (architect-decided)**: six codes (`external_cli_invalid_args`, `external_cli_model_unavailable`, `external_cli_needs_signin`, `external_cli_failed`, `external_cli_turn_limit`, `external_cli_upstream_refused`), a CLI-only classifier (never sharing `classifyByMessage`), the attribution map of §13.8.2 (`config`/`user`/**`ambiguous`** — upstream-refused is `ambiguous`, restored in grill round 1), and the Hard-Constraint-#8 atomic order. Drafter-proposed extras ride this row: `external_cli_not_found` (C8), `external_cli_invalid_config` (B17) and the non-CLI codes of matrix §13.8.5 | **Decided — architect** (contract shape). The founder still reviews the resulting catalogue *wording* with W1–W16 (Q4) |
| OQ-1857-3 | Is the *name* of the offending CLI flag safe to show (e.g. "the --model option was given twice")? | **R-3 (architect-decided)**: never name the flag — "an option was invalid or given twice". W1 may name the **setting**, not the flag, its value, or a reconstructed command. The argsafety-style duplicate-flag prevention at profile save is a separate work item, **filed as issue #1880** ("duplicate model flag in Additional CLI arguments", owner: Mac team) — founder E5, 2026-11-18 — and not part of this amendment | **Decided — architect; E5 names the follow-up issue** |
| OQ-1857-4 | Does a dispatched-but-unclassified provider attempt become `provider_error` at the terminal presentation (A4/A5), keeping `unknown` only for genuinely nothing-known failures — without touching the media strip-retry gates? | **R-4 (architect-decided)**: promote only when an HTTP status ≥ 400 was received and matched nothing, or a 200 stream ended unparseable; quoted form exactly when `provider_says` is present; no incident; pre-send/no-status attempts not promoted; both media gates preserved at their actual inputs with explicit promotion ordering (FR-092, T-52) | **Decided — architect** |
| OQ-1857-5 *(Q2 — founder)* | The own rate limiter (SEC-26) currently shows the provider-overloaded `rate_limited` copy (B3, W11) — it misidentifies an agent's own request limit. Its separate code and config attribution are already decided; only inclusion is open | **A**: include the limiter correction in this amendment, with its real Settings destination and test oracle completed · **B**: defer through the named WP-6 disposition and tracked scope, without claiming it fixed by #1857 | **Decided — founder 2026-11-18 (E2): A** — in scope. Matrix row §13.8.5 B3, code `agent_request_limit`, oracle and the verified operator-path state (W11 note) completed in this pass |
| OQ-1857-6 *(team-lead)* | `delegated_task_limit` has no producer (B9). Give the delegated-task limit path its emitter in this Epic, or mark the code reserved? | **R-6 (agreed), amended 2026-11-18 (founder E6)**: option B — mark reserved (the single §13.7 row); **no issue is filed** — the earlier filing promise is withdrawn, and the code stays reserved until the delegated-task limit path enters an Epic's scope; no emitter is added in this amendment | **Decided — amended by founder E6** |
| OQ-1857-7 *(Q3 — founder, security-lead consulted first)* | Should CLI stderr ever become visible in the Verbose/Debug technical details? Showing a masked tail there creates a client data surface the status quo does not have; a detail-level switch is not a confidentiality boundary | **A**: keep stderr off the wire at every level; expose only approved structured facts (CLI display name, exit status, classified kind) · **B**: permit a masked tail only after security-lead reviews the new exposure and the specification is explicitly revised | **A — by chief ruling, not a founder answer** (the founder's 2026-11-18 form did not answer E3): technical details never carry the outside program's text either — only the facts: program name, exit status, kind of failure. Ruled "build A" because it must fit E1 B; flagged as such in §32 D85 |
| OQ-1857-8 *(Q4 — founder)* | The wording pattern: the grill's factual corrections apply whichever pattern is chosen; the open product choice is whether the proposed short, cause-specific pattern and its per-surface voices are the desired direction | **A**: keep the pattern — observed fact only, specific safe cause where known, one supported recovery path, chat and neutral forms; review the corrected examples (W1–W16) against it rather than approving every original sentence · **B**: request a different wording pattern and identify the example that should guide it | **Decided — founder 2026-11-18 (E4): A** — the pattern is kept. W1–W16 stay **proposed** until the founder reviews the corrected example messages against the pattern (§13.8.3 status note); exact-copy wording-manifest tests wait for that review, classification/no-raw-text/code tests do not |

The Mac team's error-case comment on #1857 (posted 2026-11-15) is **incorporated**: its duplicated-flag case is C1/W1 (the setting's real label, **Additional CLI arguments**, now named); its model-unavailable case is C2 (classified from the structured `error.type`) and also surfaces in Settings → "Send a test message" (B18, W2's neutral form — corrected in grill round 1, MIN-001). The parent-side "stopped without finishing" line is the **lifecycle summary**, a surface distinct from the tool result and the completion message; §13.8.4 lists all three and S-141/U27 cover the summary → **Open** → child error journey (MAJ-003). The comment's suggested wordings were treated as evidence, not as decided text — the founder's wording review rules the pattern (D65).

---

## 31. Ambiguity warnings, assumptions, accidental findings

### 31.1 Ambiguity warnings

| # | Ambiguous | Likely agent assumption | Resolved by |
|---|---|---|---|
| A1 | "Handled" for informational entries (e.g. task finished) | Reading it = handled | §8.3: read ≠ resolved; informational entries never need resolving; "Handled" applies to Needs-you and condition entries |
| A2 | "Affected chat" for a provider event | Every chat using that provider | §6 rule + FR-012 (only chats whose turn was hit) |
| A3 | Whether pop-up preference applies without Notify | Independent switches | §9.1 ("also") |
| A4 | Where detail level is stored | Server-side per user | §12.1: per device, like today |
| A5 | Restored notification for a short blip | Always notify | §14.3: 10-min re-open without a new pop-up; the restored entry stays |
| A7 | Kind of a Calendar task's run | Both task and schedule kinds, or the gateway runner as the source | §8.1: classified at run close; schedule kinds only for Calendar-dispatched runs; Run now = task kinds |
| A8 | Recipients with several accounts | First account, or the task's creator field | §8.1 R1–R3; D83 (OQ-2 = A) |
| A9 | L11 raw successful traffic | Build a capture | D66: not in Part 1 |
| A10 | `/loop` fires | Treat as scheduled tasks | §8.1: not a producer (unchanged) |
| A11 | Provider calls outside an agent turn (if any) | Observe them too, ad hoc | §14.2: not observed in Part 1; T-38 names them |
| A12 | Existing notification files after the file-name change | Migrate them | §15.2: greenfield, not migrated; old entries are not carried over |
| A13 | "One line" for a system line | No wrapping allowed | §11.1: one compact message unit that may wrap |

### 31.2 Assumptions and defaults chosen by the author (grill targets)

- Retention sweep cadence 6 h; incident re-open window 10 min; provider message cut 300 code points on the line, 2,000 bytes stored; outage reports 10/min/user, ≤ 4 KiB, ≤ 7 days.
- `provider_connections_failing` threshold: 2 distinct providers with open network incidents.
- Disk recovery signal: the next successful `WriteFileAtomic` (no free-space polling, D73).
- Outage report client: queue ≤ 5, retries 5 s / 30 s / 2 min / every 10 min, dropped after 24 h; server de-duplication LRU of 1,000 ids for 24 h per process.
- Masking: M5 prefix list and M6 run length 24 as in §15.1.
- Undo result area: 10 s, at the top of the page; preview helper text permanent.
- The app-wide browser connection indicator reuses the `AppShell` banner area.
- Preferences and notifications stay file-based per user (no new storage technology; Hard Constraint #1).
- Detail level is per device; Debug is not persisted per user.
- Kind ids are English snake_case and never shown to users.
- Round 2: volatile table ≤ 200 items per recipient, lost on server restart; `live_seq` = max(previous + 1, Unix microseconds); removal batches ≤ 1,000 ids; `causes` ≤ 10 plus `causes_omitted`; disk observer queue 256 events with counted drops; several drift mounts → first by (`workspace_id`, mount name); the Switch banner stays until Switch succeeds or the panel is closed in that tab.

### 31.3 Accidental findings (notes to team-lead — not fixed here)

| # | Finding | Evidence | Certainty |
|---|---|---|---|
| AF-1 | A test file marked "SCRATCH DRAFT ONLY" is committed in `pkg/notifications` (`goal_pause_b1_store_contract_test.go`) — #1513 | file read at `4cad412bf` | Verified |
| AF-2 | `system_overload` frame has a consumer pop-up but no producer anywhere | `pkg/gateway/websocket_forward.go` comment ("COULD NOT DETERMINE; not guessed"); grep of `pkg/` non-test, non-generated files at `e77af6cc9` | Verified (no producer found) |
| AF-3 | `docs/using-omnipus-ui.md` says pending approvals "appear one at a time over the app", while inline approvals are on and the app-wide modal is not mounted | doc text; `src/lib/featureSwitches.ts` | Inferred (UI not driven) |
| AF-4 | Notifications list is empty for `_dev_bypass`, `_env_token`, `cli` identities (#645) | #645 text; not re-run | Unknown (not re-verified) |
| AF-5 | Notification REST handlers live in `pkg/gateway/schedules.go`, not in a notifications file | file read | Verified (structure note) |
| AF-6 | The legacy scheduled-task channel alert (`scheduledRunner.publishChannelAlert`) is handed the raw, unmasked run error as its body and sends it outside the app when a channel binding resolves — a pre-existing exposure path; Part 1 must not change it (FR-090), Part 2 replaces it | `pkg/gateway/schedules.go::scheduledRunner.RunScheduled` (`onFailure` builds the body from the run error) and `publishChannelAlert(job, cfg, body)` | Verified (code read); delivery not exercised |

### 31.4 Clarifications log

- 2026-10-30 — Q: does the external-delivery part make one spec unreviewable? → A (author): only if built together; Part 2 is described, not built (§1.3).
- 2026-10-30 — Q: which ADR-051? → A: "Provider-Capability-Aware Media Handling and User-Facing Error Translation" (the ADR-051 number has three files).
- 2026-10-31 — Founder interview: N1–N4 and the 14 recommendations accepted (§30.1, §32).
- 2026-10-31 — Correction during the fix round: the brief said the cause stays generic on the first attempts; the founder-approved voice skill (landed at `e77af6cc9`) names the cause as soon as it is observed and puts the attempt counter on every retry line. This spec follows the skill; generic wording is used only when no cause is known (§13.2).

- 2026-11-01 — Round-2 correction (the one final fix round, no third grill): R2-MAJ-002…014 and R2-MIN-001…004 corrected (§34); R2-MAJ-001 held as NS1 and OQ-1/OQ-2 held with the founder, marked in place with no provisional answer.
- 2026-11-05 — Founder answers applied (spec text only): NS1 = A → D81 (the whole response is masked before the message is selected; DS-SEC row 15 and T-51 drive a value split across two lines through the real path, oracle: no fragment of the value visible); OQ-1 = A → D82 (masking recognises known key shapes only; short fragments with no known prefix are the documented limit, §15.1 and DOC-12); OQ-2 = A → D83 (owner-less tasks, plans, schedules and sessions notify every configured account until per-person access exists). Every held-for-the-founder marker removed.
- 2026-11-05 — Founder answer W1 = A → D84 (spec text only): the banner and the chat line shown when the page loses the server drop the sentence "Your agents keep working."; when the server is unreachable both read "Can't reach the server right now. This page reconnects on its own when it's back."; after reconnecting the user sees what happened (§11.3, VS-5, S-53, S-124, U12, FR-034).
- 2026-11-15 — #1857 amendment drafted (spec text only, no code): the failure-kind list and proposed wording are §13.8, swept read-only from release head `5d20e539f` (the notifications branch is ~1,100 commits behind the release, so the sweep did not use this branch's tree); FR-092/FR-093, S-133…S-137, T-52/T-53 added; open questions in §30.4; D85 placeholder "pending founder review" in §32. The Mac team's error-case comment on #1857 was not yet posted at draft time and is incorporated before the grill. The catalogue count in §13.2's premise (29 codes) is 31 at the release head — noted in §13.8.1, no §13.2 text changed by this amendment.
- 2026-11-15 — **#1857 correction pass 1** after the architect's NOT READY read of the first draft (verdict and items in the read log kept beside this spec, not in the repo): §13.8 reworked — the SPA trust rule (F-1) makes every curated sentence riding `unknown` render as the generic line, so B5/B6/B7/C8 are generic today and the M-kind rows (M1–M8, M10) were added; FR-092 restated ("`unknown` never carries a non-catalogue message", guard test T-54); FR-093 restated to the no-stderr-on-the-wire status quo (R-7) with OQ-1857-7; A4/A5 narrowed per R-4 with both strip-retry gates pinned; CLI classification pinned to a CLI-only function (R-2); C6 no longer reuses `network` (F-7); C9 added (F-8); wording W1–W15 with neutral twins, the copy-rule and voice-guard violations fixed (F-9/F-10), the Mac team comment folded in (F-13); §13.8.4 per-surface table added (F-2); §30.4 owners set per the Rulings table; one security-relevant accidental finding from the read is reported to team-lead separately and is deliberately not described in this spec. Founder questions stay open; nothing in §13.8 is decided by this pass.
- 2026-11-15 — **#1857 grill round 1** (verdict REVISE: 8 MAJOR, 3 MINOR; report `notifications-spec-1857-review.md`, commit `a2e268220`): all 11 findings fixed — dispositions in §35. Headline changes: the save-failure disposition is split by observed stage (admission refused / in-progress record / completed-output persistence / gateway late notice — MAJ-001); resume-unavailable and workspace-changed are separate cause forms (MAJ-002); the parent side is three distinct surfaces with the approved lifecycle summary kept (MAJ-003); the `unknown` guard is three assertions with a corrected allow-list and a positive kind-coverage oracle T-55 (MAJ-004); the §13.8.5 disposition matrix gives every newly promised cause a complete code/attribution/copy/slot/oracle mapping (MAJ-005); integration tests start at the real adapters and UAT rows U27–U29 were added (MAJ-006); `external_cli_upstream_refused` attribution restored to `ambiguous` per R-2 (MAJ-007); nonfatal CLI diagnostics get their explicit non-terminal state (MAJ-008); MIN-001…003 applied (W2's Settings cross-reference, one reserved §13.7 row, per-input/per-surface current-state qualifiers with recounted totals). §30.4 now holds the grill's Q1–Q4 for the founder with a Q1↔Q3 consistency rule; until the answers the spec text follows the status quo. The security-relevant side finding stays with team-lead only, not in this spec.
- 2026-11-15 — **#1857 grill round 2 (final; no third grill)** (verdict REVISE: 6 MAJOR, 1 MINOR, 0 critical; report `notifications-spec-1857-review-round2.md`; round-1 audit: 7 addressed, 4 partial): all 7 active findings fixed — dispositions in §36. Every save/goal disposition is now bound to the exact failed operation and its caller's outcome, not to the shared reporting helper (R2-MAJ-001): B19 restricted to the continuing receipt loss (`beginStartedTool` returns `true`), the terminal continuation-stop failure is the new B23 (`recordToolCalls` → `dispatchToolCalls`, `TurnEndStatusError`, no new tool round admitted), finalization-without-an-answer is the new B21 (no "answered" claim), B11 is bound to the completed-answer branches including the direct `Sessions.Save` emission, and B20's late gateway save failure is marked turn-failing (`TurnFailed`). The goal-state rows are split (R2-MAJ-002): B14 keeps only the background paths; the new B22 is the active-turn refusal via `refuseUnreadGoalState`/`prepareToolSurface`, terminal before the provider call. The builtin-duplicate exemption is removed (R2-MAJ-003): one code `tool_assembly_duplicate`, attribution `ambiguous`, two truthful catalogue variants, the T-54 allow-list reduced to the genuinely unclassified residuals. B16 is completed to four classes with the new `delegation_start_failed` storage-refusal code, W14's `{cli}` slots fixed and absent-fact fallbacks defined (R2-MAJ-004). C11's baseline is corrected — today a nonfatal diagnostic emits and persists an error line before the `Fatal` test — and the Settings disposition is pinned with its retained failed-test behaviour and truthful copy (R2-MAJ-005). W15's re-run advice is replaced with check-the-existing-result, in the spec and in the DOC TODOs (R2-MAJ-006). The duplicated-arguments Settings acceptance case is added to S-143/T-56/U27 (R2-MIN-001). Recounted: 41 kinds, 27 can end generic today. Founder questions Q1–Q4 stay open; the status quo holds until answered.
- 2026-11-15 — **#1857 narrow correction after the independent check** (chief ruling A; not a grill round, not an open edit; report kept beside this spec at `/workspace/logs/1857/fix3-verify.md`: of 18 execution-state claims, 9 supported, 5 wrong, 4 not decided by the code, 2 uncited): every corrected claim is re-bound to the caller that decides it, and where the code does not decide a fact the claim is removed or worded "unknown to the server". B11 is re-gated on a non-empty model response the emitter must carry (the `appendFinalModelResponse` branch runs for empty and default-response turns too — `loop_run_turn.go:86-91`); the direct `Sessions.Save` failure moves to B21 with no answer-state fact (shared by stopped-silent, truncated-empty, default-response and answered turns; emits via `TranslateLLMError`, not the reporter — `loop_run_turn.go:92-107`); the stopped-silent-calls-the-reporter claim is withdrawn (the branch calls nothing, :85-86). B14 is re-scoped to the non-refusing paths — the mid-turn rubric-budget note (`goal_loop_forcing.go::goalRubricNoteForBudget`) reports inside an active turn and the turn continues. B7 is bound to the root admission, with the mid-turn steering-admission producer (`wake_input.go:252` via `loop_run_turn.go:1381-1392`) given its own truthful variant (no "nothing ran" claim). B16's classes are re-partitioned by error identity (`errors.Is` on the sentinels; the resume sentinel is returned from `reserveExternalConversation`, `beginExternalRun` and `driverForRun`, not `startAndDrain`). B10's variants are re-keyed to MCP-involved vs no-MCP-involved — the only source fact `checkToolDedupInvariant` records — and the "operator-configured"/"internal defect" claims are withdrawn as not decided by code. B19's "non-terminal" is qualified by the shared reporter's `markTurnFailed` flag. B21/B22/B23 proposed copy no longer invites a blind re-run after earlier tool rounds. The six remaining `messageNotSavedError` producers get the named residual row B24 in the T-54 allow-list with per-site reasons. `agent_request_limit` is removed from the matrix-code list until OQ-1857-5 decides its scope. Recounted: 42 kinds, 28 can end generic today. Q1–Q4 stay open; the security-relevant side finding stays with team-lead only, not in this spec.
- 2026-11-18 — **#1857 founder answers applied (spec only, pass 5)**: the founder answered the grill's questions (team-lead's form, E1–E6) and the answers are applied — **E1 = OQ-1857-1 B** (an outside program's own error text never reaches the chat; fixed, safe sentences only), **E2 = OQ-1857-5 A** (the own-rate-limiter correction is in scope: `agent_request_limit` restored to the contract paragraph — superseding this log's 2026-11-15 removal — matrix row §13.8.5 B3 with oracle, W11 completed), **E4 = OQ-1857-8 A** (the pattern is kept; W1–W16 marked proposed — founder review pending; exact-copy manifest tests wait for that review, classification/no-raw-text/code tests do not), **E3 = OQ-1857-7 A by chief ruling** (not answered by the founder's form; ruled "build A" because it must fit E1 B — FR-093 and the §13.8.4 rows record the ruling as such), **E5** (the duplicate-flag prevention follow-up is filed as #1880 — the OQ-1857-3 note names it), **E6** (no issue is filed for the reserved `delegated_task_limit`; the filing promises in §13.7, B9 and OQ-1857-6 are withdrawn). The founder's **sec2** answer folds the security note's two raw-error-text failure cases into this amendment as **B25** (`pkg/agent/execution_disposition.go::reportOrdinarySettlementFailure` — raw `err.Error()` appended to the message today; re-keyed to `transcript_save_failed`) and **B26** (`pkg/agent/goal_record_error.go::reportGoalReadError` — raw `%v` persisted into chat history via `writeGoalSystemTranscript`; the refusal carrier `refuseUnreadGoalState` sets the failed turn's final content to the same message; mapped to `goal_state_unavailable`), under the fail-closed copy rule: raw error text must not reach messages or chat history — the fixed safe sentence goes to the user, the raw text to the server log only; the security note itself stays in the coordination repo and the spec describes no security impact beyond that rule. The sibling producers `reportStopSettlementFailure` (`stop_pending`) and `deferSecretResume` (`secret_resume_pending`) are named as already carrying fixed sentences with no raw text. Completing W11 surfaced **gap G7 (§37)**: the Settings field saves `security.rate_limits.*`, which no server code reads (`SecurityConfig` carries only `platform_auth`); the enforced `sandbox.rate_limits.*` is written only by PUT `/api/v1/security/rate-limits`, which the SPA never calls. Recounted: 44 kinds, 30 can end generic today (25 always + 3 conditional + 2 partial).
---

## 32. Decisions Log (continues REQ D1–D62)

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D63 | Landing shape (FQ-1 / Q1) | All of Part 1 — core and pop-up clean-up — ships as ordered work packages frozen into one cut, with one review gate and one landing; no later-train option | A gate cannot approve code absent from its cut. Founder: "N1 = A" | Interview | 2026-10-31 |
| D64 | Connection wording and internet loss (FQ-8 / Q4, N2) | Claude Code's pattern via the voice skill section "The N2 pattern": every retry line carries cause (named as soon as observed), countdown and attempt counter; the final line names host, observed cause and one Fix; never claim "internet lost". `internet_lost` is replaced by "Connections to AI providers are failing" with the observed causes; no active probe | Failed requests prove only that those connections failed. Founder: "N2 = Claude Code's pattern"; skill refinement at `e77af6cc9` | Interview | 2026-10-31 |
| D65 | Wording process (FQ-13, N3) | Every new or surviving message is written with the `omnipus-voice` skill and checked against its self-check; the founder reviews the pattern, not each message; the 163 unproposed messages are rewritten by the implementing leads once the clean-up list is fixed; no founder rating page. Amends R-WD-4 and R-WD-9 | One reviewed pattern scales; per-message rating does not | Interview | 2026-10-31 |
| D66 | Debug L11 (FQ-17, N4) | Debug shows what exists today: internal ids, request ids, full masked failure details. The raw successful provider exchange goes to a tracked issue with a target date | A capture of all provider traffic is a sizeable new data path | Interview | 2026-10-31 |
| D67 | Goal paused (FQ-2) | A third Needs-you kind, severity needs_you, sticky, both switches locked | An agent is waiting for the user | Interview | 2026-10-31 |
| D68 | Stop outcome (FQ-3) | Becomes a Warning line in the chat where Stop was pressed; `stop_notice` leaves the centre | Own-action results never enter the centre (D31) | Interview | 2026-10-31 |
| D69 | Needs-you Notify lock (FQ-4) | Both switches locked on for the Needs-you kinds | Otherwise a pop-up could exist without a centre entry | Interview | 2026-10-31 |
| D70 | Needs-you in the open chat (FQ-5) | D30 replaces the 2026-10-18 ruling; no pop-up in a visible tab with that chat open; every other tab pops up | The inline card is the surface there | Interview | 2026-10-31 |
| D71 | Provider-wide API errors (FQ-6 / Q3) | Only provider- or account-level conditions (401/403 account-level, 408, 5xx, 529) raise `provider_error`; model-specific conditions stay in the chat | A retired model is not a provider outage | Interview | 2026-10-31 |
| D72 | Mid-answer drop (FQ-7) | Network class (`connection_dropped` / `timed_out`) | The connection, not the provider's answer, failed | Interview | 2026-10-31 |
| D73 | Disk low (FQ-9) | Raised when a data-folder write fails for lack of space; resolved by the next successful write; no polling | No new monitoring code | Interview | 2026-10-31 |
| D74 | Concurrency on unmeasurable hosts (FQ-10) | Notify only on a measured change; unmeasurable hosts show one permanent Settings → Performance line, no notification | Avoids a permanent notification | Interview | 2026-10-31 |
| D75 | Detail-level readings (FQ-11 / Q5) | Q5a A: every other tool call gets its compact line at Normal; Q5b A: the fallback note hides at Normal after the reply ends | Literal L6 and L8 | Interview | 2026-10-31 |
| D76 | Count limit (FQ-12) | Keep 50 as a safety bound; never evict a waiting Needs-you entry | Bounded files without losing waiting requests | Interview | 2026-10-31 |
| D77 | Browser-local conditions (FQ-14 / Q6) | Persistent page or app indicators with their actions; the overflow link only for centre-backed events | A vanishing pop-up with no recoverable home loses the information | Interview | 2026-10-31 |
| D78 | Homes for pop-up-only functions (FQ-15 / Q7) | Undo in a stable page-level result area (Calendar, team page); preview-link text under the Copy link button; cross-tab Switch banner; assignee warning on the task card; status-frame message as a Verbose line | Functions are relocated, not lost | Interview | 2026-10-31 |
| D79 | Chat-line carriers (FQ-16) | Keep the existing typed frames, mapped to one renderer; the new `system_line` carrier only where no carrier exists | Less churn in replay and history | Interview | 2026-10-31 |
| D80 | Deleting while a request waits (Q2) | Any entry may be deleted or cleared; this never approves, denies or cancels the request; the 30-day rule applies to waiting entries too; the cap never evicts them | Matches R-NC-4; the request's own surfaces stay | Interview | 2026-10-31 |
| D81 | Masking before first-line extraction (NS1, round-2 R2-MAJ-001) | The masker (M1–M6) runs over the whole provider response first; only then is the provider's message selected (JSON field, a text body's first line, or a stream error message), followed by the existing mask → normalise → mask → cap. A registered value split across lines is masked whole. A key split across two lines is tested through the real path (DS-SEC row 15, T-51; oracle: no fragment of the key visible) | Selecting a line first can cut a registered value in two before the masker sees it, leaving an unmaskable half on screen, in storage and in logs | Interview | 2026-11-05 |
| D82 | Short key fragments without a known prefix (OQ-1) | Accept and document: masking recognises known key shapes only; a short fragment with no known prefix (under 24 characters, e.g. `…a1b2c3`) is not caught. Stated in the security section (§15.1) and in the user-facing limits on the security page (DOC-12); DS-SEC row 13 pins it | Such a fragment cannot be told apart from ordinary text; hiding every provider message with a 6+ character letter-and-digit run (option B) would remove many useful messages | Interview | 2026-11-05 |
| D83 | Recipients for workspace objects and owner-less sessions (OQ-2) | Owner-less tasks, plans and Calendar schedules, and sessions with a readable but empty owner, notify every configured account until per-person access exists (#1421); a session started by a signed-in person still notifies only that person; attribution fields are never recipients | No per-account access gate exists today, so every account can already see and act on these objects; option B needs a new contract field and a rule for objects created by agents | Interview | 2026-11-05 |
| D84 | Browser↔server banner and chat line (W1, VS-5) | The sentence "Your agents keep working." is dropped from the app-wide banner and the open-chat line. When the server is unreachable both read "Can't reach the server right now. This page reconnects on its own when it's back." After reconnecting, the user sees what happened: finished work in the refreshed history, or the existing Interrupted notice if the gateway restarted | The browser cannot tell a dropped connection from a stopped gateway, so neither surface may claim the agents are still working. Founder: "W1 = A" | Interview | 2026-11-05 |
| D85 | Turn-failure kinds and wording (#1857) | **Exposure decisions applied 2026-11-18** — founder answers: **E1 = OQ-1857-1 B** (an outside program's own error text never reaches the chat; fixed, safe sentences only), **E2 = OQ-1857-5 A** (the own-rate-limiter correction B3/W11 is in scope; matrix row, `agent_request_limit` restored to the contract list, operator-path truth in W11), **E4 = OQ-1857-8 A** (the wording pattern is kept; W1–W16 remain proposed until the founder reviews the corrected example messages against it — exact-copy manifest tests wait for that review, classification/no-raw-text/code tests do not), **E3 = OQ-1857-7 A by chief ruling** (the founder's form did not answer it; technical details carry only program name, exit status and failure kind, because it must fit E1 B — recorded as a chief ruling, not a founder answer), **E5** (the duplicate-flag prevention follow-up is issue #1880), **E6** (no issue is filed for the reserved `delegated_task_limit`; the filing promise is withdrawn), and **sec2 A** (the two raw-error-text failure cases folded in as B25/B26 under the fail-closed copy rule — raw text in the server log only). Failure-kind list §13.8.2 (44 kinds; 30 can end generic today), matrix §13.8.5, wording §13.8.3 (proposed, E4 review pending) | The founder calls the generic line the worst possible message (#1857, 2026-11-15); the wording pattern is his review, per D65 — kept by E4 A, examples still his review | Issue #1857; sweep at release head `5d20e539f`; correction pass per the architect read; grill round 1 (REVISE) fixed in §35; grill round 2 (final, REVISE) fixed in §36; narrow post-check correction in §36.1; founder answers applied 2026-11-18 (§30.4, §31.4) | 2026-11-18 |

---

## 33. Round-1 finding dispositions

| Finding | Disposition | Where |
|---|---|---|
| MAJ-001 Lifecycle frames incompatible | Fixed: three strict frame types, no union; `popup` only on `created`; idempotent clear (round 2: four types, every invariant in the schema — §34) | §7.2, DS-FRAME, S-103, T-48 |
| MAJ-002 System-line history contract | Fixed: content vs transport split; all four history stages named | §7.5, S-55, S-106, T-28 |
| MAJ-003 Merge-on-refresh | Fixed: store revision, tombstones, snapshot rules, refetch on re-auth | §8.4, DS-RECON, S-100…S-102, T-40 |
| MAJ-004 Calendar producer | Fixed: source map with the Calendar trigger path and run-close classification | §8.1, S-01, S-09, S-18, T-37, T-24 |
| MAJ-005 Owner not a safe recipient | Fixed by rules R1–R3 (attribution never used); recipients for workspace objects put to the founder | §8.1, S-15, S-16, T-39; **OQ-2 → D83** |
| MAJ-006 Masking contract | Fixed: M1–M6, order of operations, failure drops text, DS-SEC rewritten; residual put to the founder | §15.1, §13.3, DS-SEC, S-90…S-94; **OQ-1 → D82** |
| MAJ-007 File names | Fixed: SHA-256 for every username, stored-username check | §15.2, DS-NAME, S-93 |
| MAJ-008 Facts and quotes live-to-history | Fixed: server-assembled sentences, per-code fact table, persisted fields, fallback quote | §11.4, §13.7, §7.4, DS-FACT, S-107, T-45 |
| MAJ-009 Exactly-once logging | Fixed: one seam (`accountProviderAttempt`), pure classifier, exclusions, `attempt_seq` | §14.1, §14.2, S-86, S-87, T-38 |
| MAJ-010 Incident state machine | Fixed: one table; 60-s retrospective rule removed; 10-min re-open; restart seed | §14.3, DS-INC, S-88, S-89, S-108, T-46 |
| MAJ-011 Outage reports | Fixed: report id, retry queue, idempotency, observed vs longest possible duration; H6 corrected | §14.4, DS-OUT, DS-OUT-CLIENT, S-112…S-115, §27 H6 |
| MAJ-012 Preferences inheritance and degraded states | Fixed: tri-state fields, `base_revision`, `load_state`, replace confirmation, error codes | §7.3, §9, DS-PREF, S-37…S-39, S-104 |
| MAJ-013 Navigation and action payloads | Fixed: required ids per screen and action; no `none`; stale handling | §7.6, DS-TARGET, S-05, S-59 |
| MAJ-014 Waiting-item deletion | Fixed by founder decision D80 | §8.3, §8.5, S-23, S-24, S-26, S-28 |
| MAJ-015 Local overflow to the centre | Fixed by D77: local conditions are indicators; every pop-up is centre-backed | §10, S-42, S-49 |
| MAJ-016 States and removal journeys | Fixed: centre and preferences state tables; homes and journeys incl. Undo out of view | §8.6, §9.3, §16, S-96, S-116 |
| MAJ-017 Keyboard, focus, announcements, narrow screens | Fixed: §16A, US-12 | §16A, S-118…S-121, T-49, U20, U21 |
| MAJ-018 Wording worklist and guard | Fixed: 392-id manifest with dispositions; guard over manifest sources with per-check fixtures | §13.4, §13.5, S-73…S-75, T-16, T-47 |
| MAJ-019 One cut vs later train | Fixed by D63; Epic and riders listed for team-lead | §1.3, §1.5 |
| MAJ-020 False-positive oracles, RED route | Fixed: oracles revised (DS-SEC, S-23/S-24, T-24 real producers), RED by saved narrow local runs, named gates, FR-090 pinned by a characterisation test | §19.2, T-44 |
| MAJ-021 Internet-loss claim | Fixed by D64 | §6, §13.2, §14.3, S-76, S-111 |
| MIN-001 Detail-level bundled choices | Fixed by D75 | §12.2, S-65, S-66 |
| MIN-002 Third Needs-you kind | Fixed by D67/D69 and the eviction rule | §6, §8.5, §9.1, S-27, S-34 |
| MIN-003 Contract-first order, catalogue-first components | Fixed: §7 before hooks (§8.1); component plan per surface | §6–§8, §8.6, §9.3, §11.1 |
| MIN-004 Wire and copy-rule claims too broad | Fixed: C14 corrected; copy rule narrowed to unqualified model-shopping | §3 C14, §13.2, S-77 |
| MIN-005 Lock contract | Fixed: sidecar lock on every mutation; Windows limitation stated as a rider | §15.3, S-29, T-29a |
| OBS-001 #177 not a superset | Accepted: stated, Part 2 must map | §9.5, §29.3 |
| OBS-002 Tool parity | Accepted: none in Part 1; Part 2 parity table required | §17, §29.3 |
| Test-coverage assessment, dataset gaps, high-impact symbols | Addressed: new tests T-37…T-50, T-29a; datasets DS-NET, DS-EXTRACT, DS-SEC, DS-PU, DS-DL, DS-OUT extended; DS-OUT-CLIENT, DS-NAME, DS-RECON, DS-FRAME, DS-INC, DS-PREF, DS-FACT, DS-TARGET added; regression rows for `TranslateLLMError`, `WireLLMError`, `ClassifyError`, `accountProviderAttempt` | §19, §20 |
| STRIDE summary | Addressed: per-threat design answer and test | §15.5 |
| Reachability check | Addressed: real producers per kind, action destinations, docs reachable | §8.1, §7.6, §17, §25 |
| Unasked questions A1–A8 | A1 §7.2/§8.4; A2 §8.1; A3 §11.4/§13.7; A4 §14.3; A5 §8.6/§9.3/§16A; A6 §13.5; A7 §1.5 (team-lead); A8 FR-090, §29.3 | as listed |

---

## 34. Round-2 finding dispositions, author clarifications, process receipts

Round 2 was the fixed, final grill (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/notifications-spec-20261030/notifications-spec-review-round2.md`: REVISE, 14 MAJOR, 4 MINOR, no new founder questions). This is the one correction round; there is no third grill. Any blocking finding still open after it goes to the founder through team-lead. N1–N4 and D63–D80 are not reopened and the scope is not redesigned.

### 34.1 Dispositions

| Finding | Disposition | Where |
|---|---|---|
| R2-MAJ-001 Masking after a lossy extraction step | Held for the founder at round 2 (security item); **answered NS1 = A → D81 (2026-11-05)**: the whole response is masked first, then the message is selected; a key split across two lines is tested through the real path (DS-SEC row 15, T-51) | §13.3, §15.1, §28, §30.2, S-92, DS-EXTRACT row 17, DS-SEC row 8, SC-006, DOC-12 |
| R2-MAJ-002 Snapshot and tombstone resurrection | Corrected: one ordering rule for upserts, removals and snapshots; snapshot floors retire tombstones safely; tombstone checked on snapshot items; stale removals ignored; stale snapshots ignored; three interleavings plus a known-positive update added | §8.4.3, DS-RECON rows 8–12, S-100…S-102, T-40, FR-010 |
| R2-MAJ-003 Revision-zero lifecycle | Corrected: volatile table with its own counter `live_seq`; volatile items listed, updated, resolved, marked read, deleted and cleared without a 404; failed update of a saved item keeps its base; folded into the file at the next successful write; failed delete/clear stays a visible failure | §7.1, §7.3, §8.4.1–8.4.2, §8.6, S-07, S-14, S-125, DS-RECON rows 13–16, T-02, T-20, T-40, FR-005 |
| R2-MAJ-004 Bounded payloads vs legal states | Corrected: removals of more than 1,000 ids are one store change sent as independent batches; `causes` ≤ 10 plus `causes_omitted`, ordered | §7.1, §7.2, §8.5, §13.2, §13.6, §14.3, S-126, S-127, DS-FRAME rows 8, 13, 15, 16, DS-INC row 16, T-04, T-06, T-46, T-48 |
| R2-MAJ-005 Disk observer re-enters the store lock | Corrected: non-blocking bounded queue drained outside every lock; data-folder writes only; notification and preference store writes excluded both ways; no polling | §14.3, S-109, DS-INC rows 13, 18–20, T-24, T-46, FR-065 |
| R2-MAJ-006 Retry/fallback text missing from the wire | Corrected: `ProviderRetryFrame.text` (cause sentence) plus one fixed retry clause filled from typed facts — one countdown composer; `ProviderFallbackFrame.text` = persisted note bytes; replay fields threaded through `ProviderFallbackNote.yaml`, the hand-synced inline copies in `contracts/asyncapi.yaml` and `Message.yaml` | §7.4, §11.2, §11.4, §13.2, S-51, S-131, DS-FRAME rows 17–18, T-18, FR-056 |
| R2-MAJ-007 Trust gate drops assembled facts | Corrected: `provider_message` flag replaced by `message_assembled`, set only by the one assembly for any code; `judge_conclusion_failed` exclusion removed; own-limiter and raw text stay untrusted; tested through the display and replay accessors | §7.4, §11.4, §28, S-132, DS-FACT, DS-FRAME row 19, T-45, FR-056 |
| R2-MAJ-008 Accounting hook does not prove dispatch | Corrected: explicit call-started fact set by both callers immediately before the provider call; image-attach and request-build refusals not observed; adapter pre-send failure stated as `unknown`; `ClassifyError` untouched | §3 N18, §14.1, §14.2, S-86, DS-NET rows 23–25, T-38, FR-060 |
| R2-MAJ-009 Calendar results depend on run history | Corrected: immutable dispatch origin captured in `runTask`; outcome from the applied `completeTaskWithResult`; no invented `run_id`; trigger edits cannot reclassify a launched execution | §2.1, §2.2, §8.1 "Task outcome source", S-129, T-37, T-24, FR-067 |
| R2-MAJ-010 Provider-wide success vs per-chat record | Corrected: each turn resolves only its own line and writes its own record; the incident still resolves on any success, as decided | §11.3, §14.3, S-130, DS-INC row 17, T-26, T-46, FR-033 |
| R2-MAJ-011 Session-wide fallback dedupe | Corrected: one saved note per (turn, unavailable model, answering model); within-reply dedupe kept; DOC-10 sentence to change | §11.4 item 5, S-107, T-45, FR-068, DOC-10 |
| R2-MAJ-012 Knowledge-drift target without a mapping | Corrected: target `workspace_id` + `root_path` + `collection_id` resolved from `DriftReport.Root` through `KnowledgeLifecycle` holders; deterministic choice; no entry without a mount; honest "no longer available" destination | §7.6, §8.1, S-128, DS-TARGET, T-43, T-24, FR-069 |
| R2-MAJ-013 Contradictory oracles | Corrected one by one: S-55 (persisted fields vs live-only fields); S-90 / DS-SEC (row-specific outcomes; NS1 and OQ-1 since answered, D81 and D82); SC-008 (explicit later success); SC-003 (runtime scope, instrument check); S-103 / T-48 (four frame types — every invariant encodable; no server-only assertion); U15 (Chat detail level removed from the server-save row); S-50 / U7 (chat code vs incident kind); S-53 / §11.3 (truthful text; browser-only loss S-53 vs gateway stop S-124); `provider_stalled` on the N2 final pattern; S-29 (both lock orders, convergence to the following GET); S-76 assigned to T-46 and T-34 | §7.2, §11.3, §13.2, §13.7, §14.1, §18, §19, §22, §24 |
| R2-MAJ-014 Documentation TODOs | Corrected: concrete TODOs added to DOC-01, 02, 03, 04, 05, 06, 08, 09, 10, 11 (#1546 link), 12 (written from D81 and D82), 14, 15; new DOC-16 `docs/previews.md` | §25, §23 |
| R2-MIN-001 Unregistered `{fix}` slot | Corrected: `{fix}` added as a closed slot with its value list; unknown-slot and out-of-list negative fixtures | §13.2, T-14 |
| R2-MIN-002 Voice authority vs examples | Corrected by the rule "the hard rule wins, the conflicting example or baseline entry changes": VS-1…VS-6; skill edits (VS-2…VS-5) are the voice owner's, before WP-6 receipts; no failing self-check is certified | §13.1, §13.4, §13.5 |
| R2-MIN-003 Outage delivery bounds | Corrected: all bounds stated (LRU eviction, queue overflow, tab closure, restart); pending queue kept in session storage across reloads; tests for eviction, sixth report, reload before the 204 | §14.4, S-112, S-114, DS-OUT row 13, DS-OUT-CLIENT rows 6, 10, T-27, T-41, FR-062, DOC-10 |
| R2-MIN-004 Manifest scan cannot find an unlisted source | Corrected: independent whole-tree inventory of emitter call sites reconciled to the manifest; planted bad string in an unlisted source | §13.5, T-47, SC-005, FR-054 |
| Round-2 review §8, voice points 1, 2, 4 and SAMPLES §2/§14 | Points 1 → VS-1, 2 → VS-2, 4 → VS-3; SAMPLES §2/§14 → VS-4; point 5 truthfulness → VS-5 and S-53/S-124, confirmed by founder answer W1 = A → D84 (2026-11-05); points 3, 6–10 judged not defects by the review and left as they are | §13.1, §32 |

### 34.2 Author clarifications (round-2 review §10)

| Topic | Answer | Where |
|---|---|---|
| Ordering | One rule: an input applies to an id only if it is newer on the counter that governs it (`revision` for saved state, `live_seq` for volatile state) than both the id's known value (item or tombstone) and that counter's snapshot floor. Stale snapshots are ignored whole; tombstones retire when a floor covers them; removals older than a re-created item do nothing; volatile updates order by `live_seq` | §8.4.3 |
| Wire content | Retry: `ProviderRetryFrame.text` holds the server's cause sentence; the browser appends one fixed manifest clause and fills only its numbers from `retry_at` / `sent_at` / `attempt` / `max_attempts` — the countdown is owned by the existing indicator. Fallback: `ProviderFallbackFrame.text` live = persisted note bytes = `ProviderFallbackNote.message` on replay. Replay facts live in `ProviderFallbackNote.yaml`, the inline copies in `contracts/asyncapi.yaml`, and `Message.yaml` | §7.4, §11.4 |
| Observation | Dispatch is proven by a call-started flag each caller sets immediately before the provider call; disk events go through a non-blocking queue drained outside every lock and ignore the notification and preference stores' own writes | §14.2, §14.3 |
| Outcome identity | A turn's execution owns its retry line and recovery record; a fallback cause is saved per (turn, model pair); a task run is classified by the immutable dispatch origin captured in `runTask`, and its outcome comes from the applied `completeTaskWithResult` | §11.3, §11.4, §8.1 |
| Navigation | `DriftReport.Root` → `KnowledgeLifecycle` holders → `(workspace_id, root_path)` plus `collection_id`; first by (`workspace_id`, mount name) when several; no entry when none; the destination re-checks through `getKnowledgeBaseInfo` | §7.6 |
| Acceptance and docs | Contradictory oracles corrected individually (§34.1, R2-MAJ-013); every concrete documentation TODO of the review's §7 added | §18–§25 |

### 34.3 Process receipts (recorded as received from team-lead, 2026-11-01)

| Receipt | Record |
|---|---|
| N4 deferral | N4's tracked issue is #1546 (proposed target 2026-12-15). |
| Notifications Epic | The notifications Epic waits on the founder (E1). |

## 35. #1857 amendment — grill round-1 dispositions (2026-11-15)

Grill round 1 of 2 reviewed the amendment at `0a511d4e94` (verdict **REVISE**; 8 MAJOR, 3 MINOR; report: `notifications-spec-1857-review.md`, commit `a2e268220`). Every finding is applied with the reviewer's recommendation unless stated; the ruling-level decisions (R-2…R-6, the fail-closed boundary, contract-first ordering) were preserved throughout, and one security-relevant side finding stays with team-lead only — it is not described in this spec.

| Finding | Disposition | Where |
|---|---|---|
| 1857-R1-MAJ-001 — save-failure wording asserts unproven execution states | **Applied, split by observed stage**: admission refusal (B7/W9 — "wasn't sent" valid only here), in-progress record failure (new B19/W16, non-terminal, tool keeps running — `beginStartedTool`/`recordToolCalls` bound as producers), completed-output persistence (B11 rewritten to the finalize call site — "answered" established only there; W15 reworded with the evidenced action), gateway late save (new B20 — `publishSaveFailureNotice`, untyped frame, own manifest row `M-1857-B20`). Stage-specific live/reload coverage in S-140/T-55 | §13.8.2 B7/B11/B19/B20; §13.8.3 W9/W15/W16; §13.8.4 gateway row; S-140, T-55 |
| 1857-R1-MAJ-002 — resume-unavailable is not evidence the workspace changed | **Applied**: W14 split into two cause forms bound to their typed sentinels (`errExternalResumeUnavailable` / `errExternalWorkspaceChanged`); one code `delegation_not_resumable` with two catalogue variants; unchanged-workspace control added | §13.8.2 B16; §13.8.3 W14; matrix §13.8.5 B16a/B16b; S-141, T-56 |
| 1857-R1-MAJ-003 — the parent lifecycle line is not the tool-result text | **Applied**: §13.8.4 lists three parent surfaces — lifecycle summary (`spanTerminalEvent`/`delegationEventLineText`, **unchanged** by this amendment; journey summary → **Open** → child error line), parent completion message (`steer_completion.go::completionMessage`), tool result (`ForLLM` built in `drainExternalRun` — producer symbol corrected). The §30.4 Mac-comment paragraph corrected. S-141/U27 specify and test the journey | §13.8.4; §30.4 closing paragraph; S-141; T-56(d); U27 |
| 1857-R1-MAJ-004 — T-54/S-138 can pass while known-cause failures stay generic | **Applied**: FR-092's guard restated as three assertions; T-54 rewritten (allow-list restricted to justified residual cases; payload-cleanliness asserted on the payload; both mutations fail); new positive oracle T-55/S-139 drives every known-cause kind to its typed outcome live and after reload; assembly-boundary rule stated | FR-092; §13.8.5 rules; S-138/S-139; T-54/T-55 |
| 1857-R1-MAJ-005 — newly promised kinds lack a complete acceptance oracle | **Applied**: new matrix §13.8.5 gives every newly promised cause its trigger, terminality (producer-evidenced), code, per-code attribution, copy source (chat/neutral or named manifest row), slots with no-fact forms, surfaces and oracle; B17's "config variant" error fixed (invalid cap gets `external_cli_invalid_config`, `config`); B6's mechanism pinned; B10's attribution resolved by splitting the non-actionable instance to the reserved case; attribution is per code, never per instance | §13.8.5; §13.8.2 B6/B10/B17; contract paragraph of §13.8.2 |
| 1857-R1-MAJ-006 — tests bypass adapters that lose the required facts | **Applied**: integration scenario S-143 and test T-56 start at a fake CLI process/output boundary through the real parser → dispatcher → Settings handler → assembly → persistence → renderer; stdout error envelopes included; serialized responses and persisted records inspected; exact-cut UAT rows U27–U29 added (helper chat, Settings, parent **Open**, reload, detail levels, keyboard sampling); T-53 scoped to its unit layer, boundary claims moved to T-56 | S-133/S-136/S-141–S-143; T-53/T-56; §24 U27–U29 |
| 1857-R1-MAJ-007 — upstream-refused attribution contradicts R-2 | **Applied as a transcription correction** (R-2 not reopened): `external_cli_upstream_refused` is `ambiguous` everywhere — C9 row, contract paragraph, W13 note, OQ-1857-2 | §13.8.2 C9 + contract paragraph; §13.8.3 W13; §30.4 OQ-1857-2 |
| 1857-R1-MAJ-008 — nonfatal diagnostics would receive "stopped" wording | **Applied**: §13.8.1 state rule (classification ≠ termination; drivers carry fatality; `drainExternalRun` emits before testing `Fatal`); C preamble states it; W4 marked terminal-only; new control row C11 (no terminal sentence, no retry invite, no code); S-142's malformed-line → success control; the Settings drain's current any-error-fails divergence asserted as current behaviour in T-56(g) | §13.8.1; §13.8.2 C preamble + C11; §13.8.3 W4; matrix C11 row; S-142; T-56 |
| 1857-R1-MIN-001 — Settings row points at W13 for model-unavailable | **Applied**: §13.8.4's Settings row now references W2's neutral form, bound to the C2/B18 fixture | §13.8.4 |
| 1857-R1-MIN-002 — the reserved code has two inventory rows | **Applied**: the unqualified duplicate §13.7 row removed; one reserved row remains; team-lead's issue-filing action retained; no emitter added | §13.7 |
| 1857-R1-MIN-003 — "generic today" overstated; totals unreliable | **Applied**: C6 qualified (transport-phrase causes classify `network` today — `unexpected eof`/`broken pipe` verified in `IsTransportFailure`); B18 marked conditional with its dedicated paths named; the §13.8.1 typed-frame mechanism scoped to typed frames (untyped gateway notice and authored replies excepted); totals recomputed per exact input and surface: **38 kinds, 24 can end generic today (19 always + 3 conditional + 2 partial)** | §13.8.1; §13.8.2 C6/B18 + totals; §32 D85 |

Also applied from the review's accepted-decision checks (not numbered findings): the media-gate precision correction — FR-092/T-52 now state each gate's actual input (`outcomeFallbackEligible` consumes the recovery classification; `outcomeRelabelApplies` consumes the presentation code after `TranslateTurnError`) with explicit promotion ordering and live/persisted assertions; §25's TODO detail extended to the corrected Settings and parent-**Open** journeys; §1.5's rider-confirmation Unknown is **unchanged** — obtaining the current same-file rider confirmation is team-lead's action before final design readiness.

## 36. #1857 amendment — grill round-2 dispositions (final round; 2026-11-15)

Grill round 2 of 2 (the last grill — no third) reviewed the round-1 fix at `5616f4b4ef` (verdict **REVISE**; 6 MAJOR, 1 MINOR, 0 critical; report: `notifications-spec-1857-review-round2.md`). Its round-1 audit found 7 of 11 findings fully addressed and 4 partial. Every active finding below is applied with the reviewer's recommendation; the accepted rulings (R-2…R-6) and the open founder questions (Q1–Q4, §30.4) are untouched, and no production code was changed to match an oracle.

| Finding | Disposition | Where |
|---|---|---|
| 1857-R2-MAJ-001 — save-stage matrix maps stopping failures to continuing/completed work | **Applied, bound to the exact failed operation and caller**: B19 restricted to the continuing receipt loss (`beginStartedTool` reports, returns `true` — the tool keeps running); the intermediate-assistant failure is the new row **B23**, terminal — `recordToolCalls` wraps the failed flush, `dispatchToolCalls` sets `TurnEndStatusError` and returns **before `executeToolCalls`**, no new tool round is admitted (regression proof `TestWU22FlushContinuationArchiveFailureStopsToolRound`); **B21** (new) covers finalization without an answer — the stopped-silent and failed-`recordIterationLimitNotice` branches of `finalizeTurn`, where "answered" is false; **B11** is bound to the completed-answer branches **including the direct `Sessions.Save` emission that bypasses the shared reporter**; **B20** corrected — the late gateway save failure **fails the turn** (`Finalize` returns the error, `publishDone` sets `TurnFailed`), never a post-success annotation. Matrix rule 4 now forbids inferring state from the shared helper. S-140/T-55 rewritten to drive the six stages at their real callers and assert the execution outcome (text **and** `TurnEndStatusError`/no-new-tool-round/`TurnFailed`) | §13.8.2 B11/B19/B20/B21/B23; matrix §13.8.5 rules + rows; §13.8.3 W16 note; S-140; T-55 |
| 1857-R2-MAJ-002 — goal-state failures treated as uniformly background | **Applied, split by caller**: **B14** restricted to the background goal-loop paths (reminder admission; `reportGoalReadError`'s background reminder path) — the non-terminality claim is now scoped to those paths only; the new row **B22** is the active-turn provider-request refusal: `refuseUnreadGoalState` (sets `TurnEndStatusError`, returns an error result) called from `prepareToolSurface`, so request preparation ends **before the provider call**. S-139/T-55 add a caller-driven fixture through `prepareToolSurface` asserting no provider call, alongside the separate background control; the unsupported non-terminality claim is withdrawn | §13.8.2 B14/B22; matrix §13.8.5 B14/B22; S-139; T-55 |
| 1857-R2-MAJ-003 — the guard still blesses a known cause as unknown | **Applied**: the builtin-only duplicate exemption is **removed** — one code `tool_assembly_duplicate`, attribution `ambiguous` (per code, never per instance; not mislabelled `config`), **two truthful catalogue variants** written in the matrix (operator-MCP-involved: the worker's tool/MCP setup remedy; builtin-only: no exposed tool names, an internal-defect clause closing with the D48 report line). T-54/S-138's allow-list is reduced to the genuinely unclassified residuals (A4/A5's non-promoted dispatched-provider residuals) and gains a third mutation — restoring a known-cause site to the generic result fails; S-139/T-55 count both B10 fixtures as positive known-cause coverage. The generic line keeps only the genuinely unknown case (S-135) | §13.8.2 B10; matrix §13.8.5 both B10 rows; S-137's fixture set, S-138/S-139; T-54/T-55; FR-092 |
| 1857-R2-MAJ-004 — promised causes still lack executable oracles | **Applied**: **B7** got its matrix row (`reply_not_saved`, variant "not sent", W9, admission-stage terminal); **B16** is completed to four classes — config gap → `external_cli_invalid_config` (config-gap variant, shared with B17b), storage/preflight refusals → the new proposed code `delegation_start_failed` (`ambiguous`, one truthful outcome for the session-record reads, `beginExternalRun` and the start-marker write, all propagating from `runExternalCLISubTurn` before the run starts — verified order :362/:368/:424/:428), plus the two sentinel variants B16a/B16b. The catalogue's `agent_not_configured` is explicitly **not** reused (its meaning is a missing workspace membership — B1). W14's slots corrected: **both forms require `{cli}`**, with the absent-fact fallback defined (the no-fact form drops the CLI clause). The contract paragraph's code list and the S-139/T-55 coverage sets are reconciled with the completed matrix | §13.8.2 B7/B16; §13.8.3 W14; matrix §13.8.5 B7/B16-config/B16-storage/B16a/B16b; contract paragraph; S-139; T-55/T-56 |
| 1857-R2-MAJ-005 — C11 mistakes an unchanged outcome for an unchanged display; Settings unresolved | **Applied**: the baseline is corrected in §13.8.1 and the C11 rows — today a nonfatal diagnostic **does** emit and persist an error line (the event is emitted before the `Fatal` test), and the amendment **removes** those user-facing diagnostic emissions on the successful normal-run path; the "no line exists today" claim is withdrawn. The **Settings disposition is pinned now**, not left to WP-0: the drain's stricter rule is retained unchanged (`drainSmokeTestRun` → `ok: false` on every error event; `buildResponse` publishes it — changing the rule would be a separate deliberate decision), with truthful test-level copy (manifest `M-1857-C11-settings`, proposed text in the matrix) that never claims the diagnostic proves a failed CLI run. S-142/T-56(g) assert the final status, the exact allowed text, the absence of live/persisted diagnostic lines on the successful run, and the Settings result; the classifier ownership (R-2) is untouched | §13.8.1 state-rule bullet; §13.8.2 C11; §13.8.4 Settings row; matrix §13.8.5 C11; S-142; T-56(g) |
| 1857-R2-MAJ-006 — W15 tells users to rerun completed work | **Applied**: the re-run advice is **removed** from both voices. W15 (chat) now reads "check the answer above before asking me to repeat anything"; the neutral twin reads "check the delivered result before repeating any part of the task" — the work's outcome is distinguished from its saved record, on the principle `docs/tools.md::Background results` already states (a failed report is not proof it is safe to rerun). No invented persistence retry, no durability promise. S-140's completed-side-effect-plus-save-failure fixture rejects copy advising a re-run solely to save history; §25's DOC TODOs apply the same distinction in the planned user documentation | §13.8.3 W15 + neutral twin; matrix §13.8.5 B11; S-140; §25 |
| 1857-R2-MIN-001 — duplicated arguments lack the Settings-path acceptance case | **Applied**: the duplicated-argument process-exit fixture now drives the **real Settings handler** (S-143, T-56(a)) asserting W1's neutral form, the failed-test status and the absence of flag/value text in the serialized result, **paired with a corrected-arguments success control**; U27's steps and evidence gained the Settings drive and both results. An extension of the existing rows — no new framework, no additional campaign | §13.8.4 Settings row; S-143; T-56(a); §24 U27 |

Also applied from the round-2 review's structural notes (not numbered findings): the totals recount (**41 kinds, 27 can end generic today** — 22 always + 3 conditional + 2 partial) propagated to §13.8.2, §32 D85 and the §14.1 note (B1–B23); the round-2 report's author questions are answered by the caller bindings above (question 1 → matrix rule 4 and the B19/B21/B22/B23/B11/B20 rows; question 2 → the B7/B16-config/B16-storage rows and `delegation_start_failed`; question 3 → the pinned C11 Settings disposition and copy). No escalation to the founder is carried: every R2-MAJ finding is applied in full, and the remaining open items are exactly the already-open Q1–Q4.

### 36.1 Narrow correction after the independent check (chief ruling A; 2026-11-15)

The independent checker read the round-2 fix (`f28360d034`) against release head `5d20e539f` (report kept beside this spec, not in the repo; of 18 execution-state claims: 9 supported, 5 wrong, 4 not decided by the code, 2 uncited). This subsection applies **only** the failing claims and the checker's new issues — rows the checker marked SUPPORTED are untouched. Binding rule (chief): every "turn stops / continues / completed / answer exists" claim cites the caller that decides it; where the code does not decide it, the claim is removed or worded "unknown to the server".

| Item | Disposition | Caller evidence (`file::symbol`, at `5d20e539f`) |
|---|---|---|
| B11/B21 "answered" binding | **Re-bound**: the direct `Sessions.Save` failure moved from B11 to **B21** — it is shared by stopped-silent, truncated-empty, default-response and answered turns and emits via `TranslateLLMError(nil, err.Error())` (:97), not the shared reporter and not a `messageNotSavedError`, so it carries **no answer-state fact**: no "answered" sentence may show on it (unknown to the server). B11 is now the `appendFinalModelResponse` failure branch **gated on a non-empty model response — a fact the emitter must carry** (WP-0 stated); until it does, B21's form shows (fail-closed). The round-2 claims withdrawn: the stopped-silent branch calls no reporter; the branch alone does not establish "answered"; W15's spec note and S-140/T-55 re-worded to the gate | `pkg/agent/loop_run_turn.go::finalizeTurn` — :77/:84-86 (`stoppedSilent` appends nothing, calls nothing), :86-91 (`appendFinalModelResponse` failure → reporter + `TurnEndStatusError`), :92-107 (direct `Sessions.Save` failure → `TranslateLLMError` + own emit), :137-144 (`recordIterationLimitNotice` failure → reporter + `TurnEndStatusError`); `pkg/agent/turn_exit.go::reportSessionSaveFailure` |
| B14 "no active turn" | **Re-scoped**: B14 = the **non-refusing** goal-state paths — background reminder paths **and** the mid-turn rubric-budget note, which reports **inside an active turn** and continues (the old "no active turn is running or aborted" claim withdrawn); the refusal path stays B22 | `pkg/agent/goal_loop_forcing.go::goalRubricNoteForBudget` (:344-352 — reports, sets `holds = true`, returns a note; the turn continues); B22's refusal unchanged (`goal_record_error.go::refuseUnreadGoalState` — checker-supported) |
| B7 "before any work ran" | **Split into two stage variants**: root admission (the only site where "wasn't sent / nothing ran" holds) and mid-turn steering admission — the same `messageNotSavedError{user}` from `admitSteeringInput`'s window append, terminal but **after earlier work ran**, so W9 must never show there; a second catalogue variant claims only "your message could not be added to this conversation" (matrix row + manifest `M-1857-B7steer`) | `pkg/agent/window_runtime.go::appendWindowMessage` via `pkg/agent/loop_run_turn.go:825-826` (root) and `pkg/agent/wake_input.go:252` via `pkg/agent/loop_run_turn.go:1381-1392` (steering; `contextWindowTurnExit`, `TurnEndStatusError`) |
| B16 class split overlaps | **Re-partitioned by error identity, not call site**: the resume sentinel is returned from `reserveExternalConversation` (:366/:371), `beginExternalRun` (:444) and `driverForRun` (:469) — **not from `startAndDrain`** (round-2 claim corrected); classes select by `errors.Is` on the sentinels: config = pre-checks (:207-213) + `newExternalDriver` failure when not the sentinel; storage = the non-sentinel lifecycle errors; resume/workspace = the two sentinels | `pkg/agent/external_run_session.go::reserveExternalConversation` (:366/:371), `::beginExternalRun` (:425-433 storage reads, :444 sentinel), `::driverForRun` (:469 sentinel, :471-473 `newExternalDriver`); `pkg/agent/external_dispatch.go:207-213` (pre-checks), `::runExternalCLISubTurn` (:362/:368/:424/:428 order — checker-supported, unchanged) |
| B10 builtin-only "internal defect" | **Re-keyed to the only fact the code carries — MCP-involved vs no-MCP-involved**: `checkToolDedupInvariant` tags `CategoryMCP` vs everything else (`loop_policy.go:153-156`); "operator-configured" and "internal defect" are **not decided by the code** and are withdrawn — the no-MCP variant now states the collision and that the why is **not recorded**, closing with the D48 sentence. WP-0 must add the structured MCP/non-MCP fact at the emit site (hard-coded `CodeUnknown` today); until then the no-MCP variant is the fail-closed fallback — the exemption stays removed | `pkg/agent/loop_policy.go::checkToolDedupInvariant` (:148-157); the user-facing emit `pkg/agent/loop_run_turn.go:963-975` (`CodeUnknown` hard-coded); `pkg/tools/result.go::ToolAssemblyDuplicateCode` (payload discriminator, not an `LLMError` code) |
| B21/B22/B23 copy "Retry the task when you're ready" | **Reworded — no blind re-run**: B21 "Check what already ran before deciding to repeat anything."; B22 "Check what this turn already did before trying again."; B23 "Earlier steps did run; check what was done before deciding to repeat anything." — B23's failure follows a prior tool round, B21's iteration-limit producer means tools ran, B22's refusal can follow earlier iterations; matrix rule 5 holds | The matrix proposed texts (§13.8.5 B21/B22/B23); rule 5 cites `docs/tools.md::Background results` (cited by analogy, not as a code decision — checker issue 8 noted) |
| B19 "non-terminal" vs `markTurnFailed` | **Qualified**: the tool's execution continues (`beginStartedTool` returns `true`; its caller proceeds) **and** the shared reporter sets the turn-failed flag, so the done frame reports failed; W16's note and the matrix row state both facts and hand the "work continues" vs failed-flag reconciliation to the WP-6 wording review; the round-2 "only continuing caller" enumeration withdrawn as not enumerated | `pkg/agent/started_tool_outcome.go::beginStartedTool` (:59-61, checker-supported), its caller `pkg/agent/loop_run_turn_tools.go:1467`; `pkg/agent/turn_exit.go::reportSessionSaveFailure` (:57-58 `ts.markTurnFailed()`); cancel-path reporter callers `started_tool_outcome.go:91`, `cancel.go:699` read |
| Six `messageNotSavedError` producers with no row | **Named, not hidden**: new kind row **B24** lists all six (final streamer ensure, dedup synthetic-deny save, cancel marker, cancelled tool receipt, sub-turn result, wake-input placement) as the residual emitters of the T-54 allow-list — **no new code promised in this amendment**; they stay generic today with per-site reasons in S-138/T-54/FR-092 (which no longer say "no known-cause site remains" but "exactly two named groups"); totals recounted | The six sites read at `5d20e539f`: `loop_run_turn.go:125-130`, `loop_run_turn.go:944-948`, `cancel.go:698-699`, `started_tool_outcome.go:90-92`, `loop_run_turn_tools.go:2254-2257`, `loop_window.go:73` |
| `agent_request_limit` with no matrix row | **Removed from the contract paragraph's matrix-code list**: it has wording (W11) but no matrix row, and its row is written only if OQ-1857-5 (founder) answers "include" — naming it as a matrix code promised an oracle that does not exist (checker issue 5, pre-existing) | Contract paragraph of §13.8.2; §30.4 OQ-1857-5 (unchanged, open) |

Also applied from the checker's new-issue list: the direct-Save producer shape is stated where it matters (B21's row notes that keying the finalization variant on that path requires the producer to carry the stage/answer fact — WP-0 work, flagged for the RED author); `docs/tools.md::Background results` is cited **by analogy**, not as a code decision (B21's matrix note). The counts are consistent: 42 = 7 + 24 + 11; 28 = 23 always + 3 conditional + 2 partial; the matrix now has 25 rows (B10 and B7 counted twice each). No row the checker marked SUPPORTED was changed: B23's caller binding, B20, B22's terminality, the B16 order, the lifecycle-read NEW-3 rule, and both C11 rows stand as committed.

## 37. #1857 amendment — known gaps for WP-6 (chief ruling, 2026-11-15 23:14Z)

The spec is accepted at `69c02dd5ff`. The spec-review cap was reached, so these gaps are not fixed by another spec pass. The independent check after the narrow correction (team-lead's Sonnet checker against the release tree `5d20e539f`) found that the lines below still claim more than the server knows.

**Rule.** Copy says only what the server knows. A claim about the answer state, or about earlier work, needs a caller citation (`file::symbol`) or is removed.

**Binding.** The lines below MUST NOT go into RED tests or into the build as written. They are part of the wording the founder reviews with E4 (the wording pattern), after his form answers. RED tests for these rows assert the code and attribution only, never these sentences.

| # | Line | Gap |
|---|---|---|
| G1 | B21 proposed copy ("…ended without a final reply") | The direct `Sessions.Save` failure (`pkg/agent/loop_run_turn.go` :92-107) also fires for answered turns, and the B11 fail-closed fallback routes here. So the copy asserts an answer state the server does not know. |
| G2 | W16 "The work continues" | The shared reporter (`pkg/agent/turn_exit.go::reportSessionSaveFailure`) marks the turn failed, so the done frame reports failed. "Continues" is not established for the turn. |
| G3 | B7 mid-turn steering copy ("what ran earlier … is unaffected") | The code does not decide this. `pkg/agent/wake_input.go:252` → `contextWindowTurnExit` establishes only that the save failed and the turn ended. |
| G4 | B16 route via `reserveExternalConversation` | It is called from the revive path (`pkg/agent/steering.go:760`), not from `runExternalCLISubTurn`. The B16 row does not cite this route. |
| G5 | B14 general line "no turn stops and no request is refused" (minor) | It is broader than the listed paths. A reminder-admission failure is a turn that never started. Scope the line to the listed paths. |
| G6 | W9 stale "B19/W16 mid-run" note (minor) | The mid-run user-role producer is now B7 steering, not B19. |
| G7 | W11's "raise the limit in Settings" operator path (founder E2 added the row; gap found while completing it, 2026-11-18) | The Settings screen field saves `security.rate_limits.max_agent_llm_calls_per_hour` (`src/components/settings/SecuritySection.tsx`, via `src/lib/api/config.ts::updateConfig`), which no server code reads — `pkg/config/platform_auth.go::SecurityConfig` carries only `platform_auth`. The enforced key is `sandbox.rate_limits.max_agent_llm_calls_per_hour` (`pkg/config/sandbox.go::OmnipusRateLimitsConfig`; consumed at `pkg/agent/loop_run_turn.go::beginIteration`), written only by PUT `/api/v1/security/rate-limits` (`pkg/gateway/rest_rate_limits.go::putRateLimits`, hot-reloaded), an endpoint no SPA code calls. The build either wires the Settings save to the enforced path or W11's clause is reworded at the founder's copy review — the copy is not bound until then |
