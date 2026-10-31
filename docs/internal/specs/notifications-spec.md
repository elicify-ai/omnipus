# Feature Specification: Notifications and chat system messages

**Created**: 2026-10-30 · **Revised**: 2026-10-31 (grill round-1 fix — the one fix round after grill 1)
**Status**: Draft — round-1 findings fixed; founder decisions of 2026-10-31 (N1–N4 and the 14 accepted recommendations) recorded as D63–D80 (§32); two questions still open for the founder (§30.2, OQ-1 and OQ-2); awaiting grill round 2
**Author**: architect (spec author), for team-lead (Mac squad)
**Branch / base**: `landing/notifications`, created from `release/v0.1.1` at `4cad412bf`; requirements merged at `e21febf4f`; round-0 spec `eafa2b1c5`; voice skill landed at `e77af6cc9`
**Change size**: feature (structural, cross-tree: `contracts/`, `pkg/notifications`, `pkg/gateway`, `pkg/agent`, `pkg/providers`, `src/`, user docs under `docs/`). Flow: grill round 2 (fixed, the last spec grill) → RED/GREEN/CHECK → **one** 5-reviewer gate on the one frozen Part-1 cut (D63) → candidate CI → hands-on UAT on the exact commit (touches `pkg/agent` and gateway WebSocket handlers) → founder's yes → one landing.
**Input (founder-confirmed)**:
- Requirements, stable `R-…` ids, Decisions Log D1–D62, build order: `docs/internal/specs/notifications-and-chat-system-messages-requirements-2026-10-22.md` (cited below as **REQ**)
- Agreed wording baseline: `docs/internal/specs/notifications-and-chat-system-messages-wording-2026-10-22.md` (cited as **WORDING**)
- The Omnipus voice skill: `.claude/skills/omnipus-voice/SKILL.md` and `knowledge/` (cited as **VOICE**; founder decision N3 = D65). Its section "The N2 pattern" is the pattern for every transient and connection message (N2 = D64). This spec references it and does not copy it.
- Founder review data (read only): `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/review.json`, `messages-review.json`, `revised/revised.json`, `toast-audit/result-w1..w4.jsonl`
- Round-1 review: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/notifications-spec-20261030/notifications-spec-review.md` (21 MAJOR, 5 MINOR, 2 OBSERVATION; dispositions in §33)
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
| §30 | Founder questions: answered (FQ-1…FQ-17, Q1–Q7) and still open (OQ-1, OQ-2) |
| §31 | Ambiguity warnings, assumptions, accidental findings, clarifications |
| §32 | Decisions Log D63–D80 (continues REQ D1–D62) |
| §33 | Round-1 finding dispositions |

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
   │   all §7 shapes, atomically with generated artifacts, before any Go/TS code
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
| Epic | **None exists** — `gh issue list --repo elicify-ai/omnipus --search "notifications OR \"system messages\" OR toast"` (2026-10-31) shows no notifications Epic. Team-lead creates or names one before RED; this spec does not create it |
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
| `pkg/agent/task_executor.go::TaskExecutor.ExecuteTask`, `StartOccurrenceRun`; `pkg/agent/task_executor_run.go::TaskExecutor.closeRun`, `emitRunStatus` | calls | Run lifecycle: automatic runs are `TaskRunKindScheduled`, Run now is `TaskRunKindManual`; `closeRun` → `emitRunStatus` carries task, run, occurrence, status |
| `pkg/task/task.go::Task` (`Owner` / `CreatedBy` NAMESPACE WARNING, `CancelReason`, trigger types) | calls | Attribution fields may hold a person's or an agent's name — never a recipient (round-1 MAJ-005) |
| `pkg/agent/loop_scheduler.go::LoopScheduler.RunScheduled` | — (not a producer) | `/loop` fires inside the user's chat; no notification today or in Part 1 |
| `pkg/agent/prompt_cache_accounting.go::agentLoopRunTurn.accountProviderAttempt` (called from `loop_run_turn_attempt.go::runProviderAttempt` and the `loop_run_turn.go` candidate closure) | modifies (additive) | The single per-attempt observation seam (round-1 MAJ-009) |
| `pkg/gateway/history_replay_entry.go::stampHistoryOrigin`; `pkg/gateway/history_page_prepare.go::validateHistorySourceFrame`, `prepareGroup`, `historyWireMessage` | modifies | Each rejects an unknown carrier today; all four gain `system_line` (round-1 MAJ-002) |
| `pkg/fileutil/sidecar_lock.go::SidecarLockPath`; `pkg/fileutil/flock_windows.go::WithFlock`; `pkg/fileutil/file.go::WriteFileAtomic` | calls / hook | Sidecar lock convention (in-process only on Windows); the disk-full hook point (D73) |
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
| `TaskExecutor.closeRun` | MEDIUM | every task run close | classification hook only; status semantics unchanged |

HIGH rows: the change to `TranslateLLMError`/`WireLLMError`/`ClassifyError` is flagged to team-lead before GREEN; the design keeps retry and failover *decisions* byte-identical and only adds labels, masked text and log lines (FR-063).

### 2.3 Relevant flows and cluster placement

| Flow | Relevance |
|---|---|
| Turn → provider call → fallback chain → classify → retry/fallback/terminal error → transcript + live frame | Network vs API label, provider message on the main line, per-attempt logs, provider incidents |
| Calendar trigger → `TaskTriggerScheduler.RunScheduled` → `SpawnTriggeredRun` → `ExecuteTask` → `closeRun` → `emitRunStatus` | Calendar `schedule_finished` / `schedule_failed` (§8.1) |
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
| N17 | No per-account access gate exists for workspace objects ("all post-gate role/multi-user checks were removed"); issue #1421 tracks extra accounts seeing every chat | Workspace-object recipients = every configured account until an access model exists (OQ-2) | `pkg/gateway/rest_workspaces.go` (caller comment); `pkg/gateway/schedules.go::resolveRecipients` ("Single-user product"); #1421 |
| N18 | `accountProviderAttempt` already owns per-attempt accounting at the real call boundary, before a retry or fallback replaces the failed candidate | The one place to observe every dispatched attempt exactly once (§14.2, round-1 MAJ-009) | `pkg/agent/prompt_cache_accounting.go` |
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
| Voice | A guard fails CI if any in-chat sentence in a scanned source contains `Omnipus` as a word (except the D48 sentence) or `we`, `our`, `us`; if any outside-chat sentence contains `I`, `my`, `me`, `we`, `our`, `us`; or if any scanned sentence contains `!`, an emoji, `sorry` or `oops` (§13.4) |
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

---

## 7. Wire contract changes (Hard Constraint #8)

> Shapes decided here (architect owns contract shapes); `backend-lead` writes the YAML under `contracts/`, regenerates with `scripts/gen-contracts.sh`, and commits spec + generated artifacts atomically **before any Go/TS code** (WP-0). Field names below are the wire names. No YAML in this spec. **No schema here is a discriminated union**: every lifecycle variant is its own strict frame type, so the ADR-034 inline-union rule is not needed.

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
| `revision` | yes, **new** | int64 ≥ 0: the recipient store's revision at this item's last change; 0 only when `persisted` is false (§8.4) |
| `persisted` | yes, **new** | bool: false when the store write failed or no account exists (live-only item) |
| `workspace_id` | no, **new** | per-workspace preferences and navigation |
| `target` | yes, **new** | `NotificationTarget`, §7.6 |
| `provider_id`, `provider` | no, **new** | provider id and display name for System-wide provider kinds |
| `affected_chat_count` | no, **new** | int ≥ 0, updated on coalesce for provider incidents |
| `causes` | no, **new** | array (≤ 10) of `{ provider: string, reason: FailureReason }` — only for `provider_connections_failing`: the observed causes (D64) |
| `agent_id` | no | display only |

`schedule_id` and `session_id` move into `target` (greenfield, removed from the top level).

`NotificationList` = `{ items: Notification[], unread_count: int, revision: int64 }` — `revision` is the store revision at the moment the snapshot was read (§8.4). `unread_count` = items with `read: false` (a resolved item is read).

### 7.2 Changed: the notification frames (`contracts/asyncapi.yaml`)

The single flat `NotificationFrame` is replaced by **three strict frame types**, each delivered only to the recipient's own connections (today's forwarder filter on the user), none session-sequenced:

| Frame `type` | Required fields | Forbidden | Meaning |
|---|---|---|---|
| `notification` | `change` ∈ {`created`, `updated`, `read`, `resolved`}, `notification` (full `Notification`), `popup` (bool) | — | upsert of one item. `popup: true` is valid **only** with `change: created` (the server's verdict from preferences; the browser never re-derives it). A schema `if/then` (or, if the AsyncAPI generators cannot express it, a server-side assertion plus a contract test) enforces `popup: false` for the other three changes |
| `notification_deleted` | `id` (non-empty), `revision` (int64 ≥ 1) | `notification`, `popup` | one item removed (user delete or sweep of one item) |
| `notification_cleared` | `cleared_ids` (array of non-empty ids, 1…1,000, unique), `revision` | `notification`, `popup` | several items removed at once (Clear all, or a sweep). Idempotent: removing an absent id is a no-op, so a replayed or duplicated frame changes nothing |

`WsFrameType` gains `notification_deleted`, `notification_cleared`. Positive and negative contract fixtures exist for every variant (DS-FRAME, §20). Live-only items (`persisted: false`) are delivered by `notification` frames with `revision: 0`.

### 7.3 New and changed REST operations (`contracts/openapi.yaml`, tag `notifications`)

| Operation id | Method + path | Request | Responses |
|---|---|---|---|
| `listNotifications` (existing) | GET `/notifications` | — | 200 `NotificationList` (now with `revision`) |
| `markNotificationRead` / `markAllNotificationsRead` (existing paths) | unchanged | — | 204; each change is pushed to every connection (FR-007) |
| `deleteNotification` | DELETE `/notifications/{id}` | — | 204; 404 unknown id. **No 409**: a waiting Needs-you entry may be deleted (D80) |
| `clearNotifications` | DELETE `/notifications` | — | 204; removes every entry of the caller, waiting ones included (D80) |
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

### 7.4 Changed: provider error, retry and fallback shapes (round-1 MAJ-008)

| Schema | Change |
|---|---|
| `LLMError` (`LLMError.yaml`) | **new optional `provider_says`** (string ≤ 2,000 bytes): the provider's own error message, extracted (§13.3), masked (§15.1), **persisted and replayed** (amends ADR-051, §28). **New optional `failure_class`** enum `network` · `api` (absent for non-provider codes and for `unknown` class). `facts` gains optional `http_status` (int) and `host` (string ≤ 253) — rendered only in technical details at Verbose/Debug, except `host`, which the final network/API failure sentence names (D64). Enum: **new code `provider_error`** (API failure, provider side: 401/403 account-level, 408, 5xx, 529 — D71) — `network` keeps only network-class failures. `message` stays the finished, server-assembled sentence and `provider_message` stays the trust flag (`src/lib/llm-error.ts::getLLMErrorDisplay`); `x-user-messages` is replaced per §13.2 |
| `ReplayErrorPayload`, `ReplayErrorFrame` | carry the same `provider_says`, `failure_class` and `facts.host`; the persisted transcript error entry stores them (§13.7); `facts.http_status` and `detail` stay live-only |
| `ProviderRetryFrame` | `error_code` may now be `network` or `provider_error` (today always `rate_limited`); **new optional `reason`** (`FailureReason`); **new optional `http_status`** int; **new optional `provider_says`** (masked, API class only). Existing `attempt` and `max_attempts` stay required and feed the attempt counter on every retry line (D64) |
| `ProviderFallbackFrame` (live) and `ProviderFallbackNote` (replay) | **new optional `provider_says`** (masked) and **`failure_class`**, so a reply changed by a fallback quotes why the first model failed (L3); persisted with the existing fallback note |
| `WsFrameType` | adds `system_line`, `notification_deleted`, `notification_cleared` |

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

`NotificationTarget` = `{ screen, workspace_id?, session_id?, task_id?, run_id?, plan_id?, agent_id?, knowledge_base_id?, provider_id? }`. Every entry has a usable screen (R-NC-6); there is no `none`.

| `screen` | Required ids | Used by | When the target is gone |
|---|---|---|---|
| `chat` | `session_id` | Needs-you kinds, goal kinds, gateway schedule run with a session | The centre/pop-up says "This chat no longer exists." and stays on the current screen |
| `task` | `workspace_id`, `task_id`; `run_id` + `session_id` when a run exists | task and Calendar schedule kinds | Opens the task without the run when the run is gone; "This task no longer exists." when the task is gone |
| `plan` | `workspace_id`, `plan_id` | plan kinds | "This plan no longer exists." |
| `agent` | `agent_id` | a gateway schedule job (heartbeat) that failed before any session existed (`scheduledRunner.RunScheduled` calls `onFailure` with an empty session on owner/setup failures) | "This agent no longer exists." |
| `providers_settings` | `provider_id` optional | provider kinds | Settings → Providers opens without a selection |
| `performance_settings` | — | `resources_low`, `concurrency_reduced` | — |
| `knowledge_base` | `workspace_id`, `knowledge_base_id` | `knowledge_drift` | "This knowledge base no longer exists." |

| Line action `kind` | Required ids | Behaviour |
|---|---|---|
| `switch_model` | `session_id` | opens that chat's model picker (existing) |
| `open_provider_settings` | `provider_id` optional | navigates |
| `open_task` | `workspace_id`, `task_id` | navigates |
| `open_performance_settings` | — | navigates |
| `retry_reply` | `session_id`, `turn_id` | re-sends through the existing regenerate path; if that turn is no longer the latest, the line says "This reply can no longer be retried." and does nothing |
| `review` | `session_id` | opens the chat with the pending card |

Navigation never trusts a carried id: the destination screen loads its object through the normal authenticated REST call and shows the "no longer exists" text on 404. Ids are never parsed from text and no session id is fabricated.

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
| `schedule_finished`, `schedule_failed` (Calendar task) | `TaskTriggerScheduler.RunScheduled` (`pkg/agent/task_trigger.go`) → `TaskExecutor.SpawnTriggeredRun` → `ExecuteTask` → `TaskExecutor.closeRun` → `emitRunStatus` (`pkg/agent/task_executor_run.go`) | at run close: run kind `TaskRunKindScheduled` **and** the task's trigger type is `once` or `recurring` → schedule kind; status `done` → finished, `failed` without `CancelReason` → failed; a run the user stopped (`Task.CancelReason` set) → nothing | R2 (workspace object) |
| `schedule_failed` (Calendar dispatch failure) | `TaskTriggerScheduler.RunScheduled` returning an error **before** a run opens (task missing, spawn refused) | one `schedule_failed`, target `task` without `run_id` | R2 |
| `schedule_finished`, `schedule_failed` (gateway job: heartbeat or API schedule) | `scheduledRunner.RunScheduled` (`pkg/gateway/schedules.go`) success path (new hook) and `onFailure` (existing) | coalesce key `schedule:<job_id>`; a failure before any session exists (owner unavailable, worker, session pick) gets target `agent` | R1 when the run has a session; otherwise R2 |
| `task_started`, `task_finished`, `task_failed` (task) | the same run lifecycle (`ExecuteTask` open, `closeRun` close) | **automatic runs** that are not Calendar-scheduled (queue drain, dependency unblock, plan dispatch: `TaskRunKindScheduled` with trigger `manual`): started, finished, failed. **Manual runs** (`TaskRunKindManual`: Run now, `StartOccurrenceRun`): finished and failed only — starting it is the user's own action. A user Stop (`CancelReason`) → nothing | R2 |
| `task_started`, `task_finished`, `task_failed` (plan) | `EmitPlanStatusChanged` (`pkg/agent/loop_events.go`) with a new internal, **non-wire** `origin` (`engine` · `rest`) on the plan-status hook | only `origin: engine`; `running` (first time) → started, `done` → finished, `failed` → failed; REST mutations are own actions → nothing | R2 |
| `provider_*`, `provider_connections_failing`, `connection_restored` | incident tracker (§14.3) fed by the per-attempt observation (§14.2) | §14.3 | R3 |
| `resources_low` (memory), `concurrency_reduced` | `config.MemoryPressureHigh` transitions; `applyMemoryCap` (`pkg/agent/admission.go`) becoming memory-binding on a measured host (D74) | §14.3 | R3 |
| `resources_low` (disk) | `fileutil.WriteFileAtomic` (`pkg/fileutil/file.go`) failing with no-space (Unix `ENOSPC`; Windows `ERROR_DISK_FULL`, `ERROR_HANDLE_DISK_FULL`) (D73) | §14.3 | R3 |
| `knowledge_drift` | `knowledgeDriftNotifier` (`pkg/gateway/knowledge_drift_notify.go`) — existing | existing | existing (`knowledgeDriftRecipients`: every account) |

Not a producer in Part 1 (unchanged behaviour): `/loop` fires (`LoopScheduler.RunScheduled`, `pkg/agent/loop_scheduler.go`) run inside the chat the user started the loop in and report there; they raise no notification today and none is added (§31.1). The Stop outcome (`publishStopNotice`) becomes a chat line (D68).

**Recipient rules.**
- **R1 — session-scoped.** The root session's `UnifiedMeta.Owner` (stamped by authentication when the session is created, `pkg/session/unified.go`). Readable and set → that user. Readable but empty (a session created by a schedule or channel with no authenticated user) → every configured account (OQ-2, recommended A). Unreadable → **no** recipient and one ERROR line (keeps `notifyGoalPaused`'s rule; D24).
- **R2 — workspace object** (task, plan, Calendar schedule). **Every configured account** (OQ-2, recommended A). Reason: no per-account access gate exists for workspace objects today (`pkg/gateway/rest_workspaces.go` caller comment: "all post-gate role/multi-user checks were removed"; issue #1421), so every account can already see and act on them; and `Task.Owner` / `Task.CreatedBy` / `Plan.Owner` / `job.CreatedBy` may hold either a person's or an agent's name (`pkg/task/task.go` NAMESPACE WARNING), so they are never used as recipients.
- **R3 — system-wide.** Every configured account.
- **No accounts at all** (first start before onboarding): live-only item with a minted id, `persisted: false`, `revision: 0`, log WARN (closes #645 Defect 1).
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
              │    write fails → log ERROR, deliver live with persisted:false, revision:0
              │ 7. emit `notification` frame {change, notification, popup} to that recipient
              v
             store file  +  live frame  ──> SPA store (reconciliation §8.4) ──> centre / pop-up
```

Resolution, read, delete and clear take the same lock and emit the same frames (§7.2). "Handled anywhere = resolved everywhere" (D19): the approval path already emits `tool_approval_resolved`; the router resolves `approval:<id>` in the same step and emits `change: resolved`.

### 8.3 Lifecycle

| State | Set by | Frame | Effect |
|---|---|---|---|
| created (unread) | router | `notification` `created` | badge +1; pop-up if `popup` |
| updated | router coalesce | `notification` `updated` | content refreshed (e.g. `affected_chat_count`, `causes`); no new pop-up; read state unchanged |
| read | user opens it or marks read | `notification` `read` | badge −1 |
| resolved | condition cleared (§6 resolve trigger), or handled through its action anywhere | `notification` `resolved` | also read; pop-up removed in every tab; entry greyed "Handled" |
| re-opened | incident re-fails within 10 min of resolving (§14.3) | `notification` `updated` (resolved_at_ms removed) | unread again; **no** new pop-up |
| deleted | user, or sweep of one item | `notification_deleted` | gone from storage (R-SEC-2) |
| cleared | user Clear all, or a sweep of several | `notification_cleared` | gone from storage |

Deleting or clearing never touches the underlying request (D80): an approval or question stays pending in its chat, its sidebar cue stays, and its resolve trigger simply finds nothing to resolve.

### 8.4 Reconciliation between tabs, pushes and refetches (round-1 MAJ-003)

The server keeps one monotonic **store revision** per recipient, persisted in the recipient's file; every persisted change (create, update, read, resolve, delete, clear, sweep) increments it once under the lock. Each persisted item carries the revision of its last change; each delete/clear frame carries the revision of the removal.

Browser rules (one module, `src/store/notifications.ts`, replacing today's replace-on-hydrate):
1. **Upsert** a `notification` frame only if its item revision is greater than both the local copy's revision and any **tombstone** for that id.
2. **Delete/clear** frames remove the ids and record a tombstone (id → removal revision), kept until a snapshot with a higher revision arrives.
3. **Snapshot** (`GET /notifications`, list revision R): for persisted local items — remove those absent from the snapshot whose revision ≤ R (they were deleted while this tab missed the frame); keep those with revision > R (newer than the snapshot, already received live); replace the rest by the snapshot copy. **Live-only items** (`persisted: false`) are kept until the user deletes them or the page reloads.
4. **Refetch** on every successful WebSocket (re-)authentication and on window focus; the unread badge is recomputed from the merged list.

Result: a tab that was offline during a delete loses the entry on reconnect (US-2 AC-4), and a slow GET can never roll back a newer pushed change.

### 8.5 Retention (R-NC-4, R-SEC-2; D23, D80)

- **Age.** Every entry with `created_at_ms` ≥ 30 × 24 h old is deleted — waiting Needs-you entries included (D80; the request keeps waiting). Sweep at start-up, every 6 h, and on every write to that recipient's file; the sweep emits one `notification_cleared` frame per recipient when it removed anything. Quarantined corrupt files (`<file>.corrupt-<unixnano>`, written by `pkg/notifications/store.go`) older than 30 days are deleted by the same sweep.
- **Count.** The cap of 50 stays as a safety bound, but eviction removes only handled or informational entries, oldest first; unresolved Needs-you entries are **never** evicted, so a file may hold more than 50 entries when more than 50 requests are waiting (MIN-002). The cap is therefore not a hard bound; the age rule is.
- **Delete one / Clear all.** Real removal from the file, waiting entries included (D80); no 409.

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
| Item not saved | `persisted: false` | row shows "Not saved — shown on this screen only" |
| Mutation failed | delete / clear / mark read rejected or network error | the item is restored (no optimistic loss) and the row or header shows "Could not delete — Retry" |
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
| Source | Only a `notification` frame with `popup: true` and `change: created`. Never a re-open, an update or an own-action result. Browser-local conditions never pop up (§10.2, D77) |
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
| `ProviderRetryFrame` | `ProviderRetryIndicator` | warning | yes (countdown + attempt counter) | every level while the turn waits | — |
| `ProviderFallbackFrame` / persisted fallback note | `ProviderFallbackNoteLine` | info, quoting why the first model failed (`provider_says`) | no | normal while the turn runs; **verbose after the reply ends** (D75, L8 literal) | Pick a new model when `model_retired` |
| `RateLimitFrame` (Omnipus's own limiter, SEC-26) | `RateLimitIndicator` | warning | yes until `retry_after` | normal | — |
| `ContextWindowNoticeFrame` | system pill | info | yes during compaction | normal while happening; verbose after | — |
| `CancelStageFrame` | stop-button state | info | yes | normal | — |
| Goal outcome entry (`system_subtype: goal_outcome`) | `GoalOutcomeRow` | resolved (`met`) / warning (`rounds_exhausted`, `other`) / info (`stopped_by_user`) | no | normal (L2) | — |
| Helper event entry (`helper_event`) | `HelperEventRow` | info / warning by event | per event | normal | — |
| Browser handover notice, goal acknowledgement, clear-context divider | system pill | info | no | normal | — |
| Delegation line | `DelegationEventLine` | info | yes while running | normal (L7 summary) | — |
| Restart-interrupted notice | `RestartInterruptedNotice` | warning | no | normal | Retry reply |
| Unavailable linked chat | `UnavailableChatNotice` | error | no | normal | Retry |
| Browser↔server loss (client-only) | `ChatConnectionStatusLine` | warning ("Can't reach the server right now…" / offline) → resolved ("Up to date", then removed) | yes | normal; never persisted (R-SL-6) | Try again |
| **New** network/API retry outcome record | — (`SystemLineFrame`) | resolved | no | **verbose** (R-SL-5 record line) | — |
| **New** Stop outcome (D68; `publishStopNotice` today feeds the centre) | — (`SystemLineFrame`) | warning | no | normal | — |
| **New** `system_overload` (w4-087, if a producer exists — §10.2) | toast today | warning | no | normal | — |
| **New** "working on …" step line | `AgentStatusIndicator` | info | yes | normal (L4) | — |

### 11.3 Connection lines (R-SL-5, R-SL-6; D10, D18, D42, D64)

- **Omnipus↔provider problem while a reply waits**: in-progress Warning at every level, driven by `ProviderRetryFrame` with network or API class. Every retry line carries the observed cause (as soon as observed), the countdown and the attempt counter (§13.2). On success: at Normal the line disappears; the server appends one `SystemLine` record (`line_type: resolved`, `min_level: verbose`) "Connection to {provider} restored after {n} retries ({seconds} s)." On final failure: the terminal Error line (L3, N2 final form naming host, cause and one fix) replaces it at every level.
- **Browser↔server loss**: one app-wide indicator (the `AppShell` banner area, shown in every reconnecting phase, not only on terminal errors) plus the chat line; both client-only, neither enters history; the outage is reported to the server when back (§14.4). The chat line distinguishes "Can't reach the server right now. Your agents keep working." (server unreachable) from the offline form; after reconnect, "Up to date" shows briefly and the chat refetches history, so the user sees what the agents did meanwhile.

### 11.4 Sentence composition (round-1 MAJ-008)

The server assembles the finished sentence; the browser never composes wording from facts.
1. At the emit site (`TranslateLLMError` and the retry/fallback emitters), the server picks the sentence variant by the facts it actually has: *no facts* · *provider/model known, no provider message* · *provider/model known and `provider_says` present* · for `network`/`provider_error` additionally *retrying* vs *final* (§13.2).
2. It fills every slot from the facts at that site (§13.7) and never renders an empty slot or empty quotes.
3. It puts the finished text in `LLMError.message` (or the retry/fallback frame's text) and sets `provider_message` (the existing trust flag, `src/lib/llm-error.ts::getLLMErrorDisplay`) exactly as today, so a curated server sentence is trusted and an untranslated one is not.
4. It persists `message`, `provider_says`, `failure_class` and `facts.provider` / `facts.model` / `facts.host`; `detail`, `facts.http_status` and `facts.request_id` stay live-only (C14 corrected, §3). Replay therefore renders the same main line without re-composing.

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
| L11 | Internal IDs and full failure details | — | — | Shown | what exists today (D66): session, turn, tool-call, approval and request ids already on the wire, and the full masked failure detail. **Raw request/response of successful provider calls is not captured in Part 1** — tracked issue (§30, N4) |

Rules: technical details only at Verbose and Debug (R-DL-2, D25); the masked failure payload is the only raw payload shown (D43, D66); secrets masked at every level (R-SEC-1, D16); the connection-problem line shows at every level while it lasts (R-SL-5).

---

## 13. Wording (R-WD-1…9; D26, D28, D35, D36, D47–D50, D64, D65)

### 13.1 Rules — the voice skill is the authority (D65)

Every new or surviving system message in this feature is written with the **`omnipus-voice` skill** (`.claude/skills/omnipus-voice/SKILL.md`) and checked against its "Self-check before committing a message" list before it is committed. The founder reviews the pattern (the skill), not each message (D65). This spec does not restate the skill's rules; it fixes only what the code must enforce:

| Enforced here | Source |
|---|---|
| In a chat the agent speaks as "I"; "Omnipus", "we", "our", "us" never act; the only product-name use is the D48 bug-report sentence | skill hard rule 1; R-WD-5, R-WD-7 |
| Outside a chat (centre, pop-ups, banners, settings) neutral, no first person; a message used in both places has two forms | skill hard rule 1; R-WD-6, R-WD-8 |
| The provider's own message is quoted on the main line when received; never an empty slot or empty quotes | skill hard rules 3, 12; R-WD-2, D28 |
| Transient and connection failures follow **the N2 pattern** (skill section "The N2 pattern — transient and connection failures"): cause named as soon as observed, countdown, attempt counter on every retry line; final line names host, observed cause and one Fix; never claim the internet is down | D64 |
| No `!`, no emoji, no "sorry", no "oops" in any system message | skill hard rules 9, 11 |

Differences between the skill and the agreed baseline are listed in §13.4 and in the round-1 report; none is resolved silently.

### 13.2 Catalogue (ADR-051 codes) and the N2 lines

- `contracts/components/schemas/LLMError.yaml` `x-user-messages` takes the WORDING §1 final sentences for all 29 codes, plus `provider_error` (new, from the `network` split), with the N2 refinements below. Each entry carries explicit variants — no clause logic in code: `message` (no facts), `provider_message` (provider/model known, no provider message), `provider_message_quoted` (`provider_says` present), and for `network` / `provider_error` the `retrying` and `final` forms.
- The generators (`scripts/gen-asyncapi-go`, `scripts/_gen-asyncapi-types.mjs`) validate slots against the closed vocabulary of the skill ("Naming things": `{provider}` `{model}` `{provider_says}` `{reason}` `{status}` `{n}` `{seconds}` `{minutes}` `{tool}` `{agent}` `{path}` `{model_requested}` `{model_used}` `{limit_name}` `{value}` `{task}` `{attempt}` `{max}` `{countdown}` `{answered_model}` `{unavailable_model}`) **plus `{host}`** (new, required by N2's final line; the skill's list is to be extended — difference 4).
- `x-user-message-attributions` stays.
- The copy-rule tests (`pkg/agent/translate_error_test.go`, `src/lib/llm-error.test.ts`) change narrowly (MIN-004): the rule "no `product` or `config` message tells the user to switch models" is kept as a ban on **unqualified** model-shopping — a qualified remedy ("switch to a model with a larger limit", "switch to a model that reads it") stays allowed, as in the baseline; "no message tells the user to contact support" is relaxed by exactly the D48 sentence.

N2 lines (D64; replaces the round-0 network split table):

| Situation | In chat | Outside chat (centre / pop-up title) |
|---|---|---|
| network, retrying, cause observed | `I can't reach {provider}: {reason}. Retrying in {n} s (attempt {attempt} of {max})…` | `{provider} can't be reached: {reason}` |
| network or API, retrying, no cause known yet | `I can't get a reply from {provider} yet. Retrying in {n} s (attempt {attempt} of {max})…` | `{provider} isn't answering` |
| network, final | `I couldn't reach {provider} ({host}): {reason}. Fix: {fix}` | `{provider} can't be reached: {reason}` |
| API, retrying | `{provider} answered with an error ({status}): "{provider_says}". Retrying in {n} s (attempt {attempt} of {max})…` (no-quote form: `{provider} answered with an error ({status}). Retrying in …`) | `{provider} returned an error ({status})` |
| API, final | `{provider} ({host}) answered with an error ({status}): "{provider_says}". Fix: retry in a moment; if it repeats, switch model.` (no-quote form drops the quote) | `{provider} returned an error ({status})` |
| several providers failing (centre only) | — (each affected chat shows its own provider line) | title `Connections to AI providers are failing`; body lists each `{provider}: {reason}` from `causes` |

- `{reason}` words (fixed; aligned with the skill's examples): `dns_failure` → "its address could not be found"; `connection_refused` → "connection refused"; `network_unreachable` → "the network is unreachable"; `tls_failure` → "the secure connection failed"; `timed_out` → "it did not answer in time"; `connection_dropped` → "the connection dropped". `http_status`, `invalid_response` and `unknown` never fill `{reason}` (they use the API or no-cause forms).
- `{fix}` by reason: `dns_failure` → "check your network or the provider's address in Settings → Providers."; `tls_failure` → "check your proxy or certificate settings."; every other network reason → "check your network or proxy."
- `{n}` is whole seconds ("8 s"); the indicator may still show its live countdown, but the line text uses the "8 s" form.

### 13.3 Provider message extraction (`provider_says`)

From the failing attempt's response, in order: JSON body → first non-empty string at `error.message`, `error.error.message`, `message`, `error` (when a string), `detail`, `error_description`; otherwise a non-JSON body's first line; otherwise a stream error event's message. Then, in this order: **mask first** (§15.1, on the raw text), strip control characters, collapse whitespace, **mask again** (catches values joined by the normalisation), cap 2,000 bytes on a UTF-8 boundary (never splitting a code point). The main line shows ≤ 300 code points + "…". Never the whole raw body. Valid JSON with no eligible field and an empty body both → absent (the no-quote sentence is used).

### 13.4 Voice guard (round-1 MAJ-018)

New CI guard `scripts/check-system-message-voice.sh` (picked up by `scripts/guards.sh`). It scans the sources listed in the wording manifest (§13.5) — the `LLMError.yaml` catalogue, the server-side system-line text table, the centre title/body table, and every frontend string module the manifest names — with these checks:

| Check | Applies to | Rule |
|---|---|---|
| Product as actor | in-chat and outside | `\bOmnipus\b` fails except inside the exact D48 sentence |
| First person plural | in-chat | `\b(we|our|us)\b` (case-insensitive) fails |
| First person outside a chat | outside | `\b(I|my|me|we|our|us)\b` fails |
| Tone | all | `!`, any emoji code point, `\bsorry\b`, `\boops\b` fail |

"please" is **not** checked: the baseline keeps "Please restate it." (BE-008) and `schema`'s "please report it." (difference 1). Sentence length, sentence case and the unit format are self-check items of the skill, not guard checks (difference 6). Self-check of the guard: one planted fixture per check and per surface (in-chat "we", outside "I", a "!", an emoji, "sorry"); each must make the script fail, and a clean fixture must pass.

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

A unit test fails if any manifest id has no disposition, if a surviving id has no final text or no self-check receipt, or if a scanned source contains a system-message string whose id is not in the manifest.

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
| `provider_error` | {provider} returned an error ({status}) | "{provider_says}" |
| `provider_quota_exhausted` | {provider}: account out of credit | "{provider_says}" |
| `provider_rate_limited` | {provider} is limiting requests | {affected_chat_count} chats waiting |
| `provider_connections_failing` | Connections to AI providers are failing | one line per cause: {provider}: {reason} |
| `connection_restored` | {provider} reachable again / Connections to AI providers restored | after {duration} |
| `resources_low` | Memory is running low / Disk space is running low | memory: Fewer agents run at once until it recovers · disk: Saving files fails until space is freed |
| `concurrency_reduced` | Fewer agents run at once | Limited to {n} because memory is low |

### 13.7 Per-code facts and where they come from (round-1 MAJ-008)

Slot values are taken at the emit site and baked into the finished sentence; only the facts in the "Wire" column travel as fields.

| Code(s) | Slots beyond provider/model/provider_says | Fact source at the emit site | Wire / persisted |
|---|---|---|---|
| `network` | `{reason}`, `{host}`, `{n}`, `{attempt}`, `{max}` | per-attempt observation (§14.2): reason, host of the failing candidate's endpoint; retry policy for n/attempt/max | `failure_class`, `facts.host`, retry frame `reason`, `attempt`, `max_attempts` — persisted on the error entry |
| `provider_error` | `{status}`, `{host}`, `{n}`, `{attempt}`, `{max}` | observation: HTTP status | `failure_class`, `facts.host` persisted; `facts.http_status` live-only |
| `provider_stalled` | `{minutes}` | the provider's silence limit in effect | in `message` only |
| `turn_timed_out`, `judge_time_limit` | `{seconds}` | the turn deadline / the Judge time limit in effect | in `message` only |
| `tool_args`, `tool_call_truncated` | `{tool}`, `{reason}` | the failing tool call's name and validation reason | in `message` only |
| `workspace_unavailable` | `{path}`, `{reason}`; outside form `{agent}` | the agent's working folder and the OS error text (masked) | in `message` only |
| `judge_conclusion_failed`, `model_route_unavailable` | `{reason}` | the save/delivery error, the provider set-up error (masked) | in `message` only |
| `model_unavailable` | `{model_requested}`, `{model_used}` | model selection of the turn | in `message` only |
| `delegated_task_limit` | `{task}`, `{limit_name}`, `{value}` | the delegated task's title and the limit that stopped it | in `message` only |
| `agent_not_configured`, `model_unassigned` | `{agent}` | agent display name | in `message` only |
| every other code | provider/model/provider_says only | failing attempt | `facts.provider`, `facts.model` persisted; `provider_says` persisted |

When a slot's fact is unknown at the emit site, the variant without that slot is used (§11.4). Codes that are non-terminal notices today (e.g. `model_unavailable` reporting a substitution while the reply continues) stay non-terminal: only their wording changes. The exact emit symbol for each code is pinned by the WP-1 RED test for that code (T-45).

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

Not observed (no class, no log line, no incident): context cancellation (Stop, shutdown), a candidate skipped by cooldown, a route error before dispatch (`routeErr`), Omnipus's own rate limiter, and an image-attach or request-build failure before dispatch.

**Kind per class** (D71 — FQ-6 in review-revised form): network → `provider_unreachable`; api with code `quota_billing` → `provider_quota_exhausted`; api with a provider 429 → `provider_rate_limited`; api with 401/403 at account level, 408, 5xx or 529 → `provider_error`; model-specific API conditions (`model_retired`, `bad_model`, `context_too_long`, `content_policy`, `request_too_large`, `media_unsupported`, a 400 `context_length_exceeded`) stay chat-only and raise no incident. `unknown` raises no incident.

### 14.2 Per-attempt observation (round-1 MAJ-009)

The single owner is the existing seam **`agentLoopRunTurn.accountProviderAttempt`** (`pkg/agent/prompt_cache_accounting.go`: it "owns accounting at the real call boundary, before a retry/fallback can replace the failed candidate's identity"). It is reached from `runProviderAttempt` (`pkg/agent/loop_run_turn_attempt.go`, deferred) and from the candidate closure in `pkg/agent/loop_run_turn.go`. The observation is added there, so every dispatched request of a turn is seen exactly once, whatever retry layer issued it.

For every failed observed attempt it writes **one WARN line** "provider attempt failed" with: `failure_class`, `reason`, `provider`, `model`, `host`, `http_status` (api), `provider_says` (masked, ≤ 500 bytes), `attempt_seq` (1, 2, 3 … per turn across all layers), `candidate` (index in the fallback chain), `retry_round`, `session_id`, `turn_id`, `request_id`. There is **no** `next` field: what happened next is the next line (another attempt, a fallback) or the existing ERROR "LLM call failed", which gains `failure_class`. WARN because the shipped default level is `warn` (§3.1 C13). Successful attempts write nothing to the log but feed the incident tracker (resolution).

Provider calls made outside an agent turn (if any) are not observed in Part 1; the test author's T-38 enumerates the callers of the provider chat interface so any such path is named, not missed (§31.1).

### 14.3 Incident tracker — one state machine (round-1 MAJ-010)

In memory, one per gateway process, keyed by **(kind, provider)** for provider kinds and by **resource** for `resources_low` / `concurrency_reduced`.

| From | Event | To | Effects |
|---|---|---|---|
| closed | first failure of kind K at provider P (§14.1) | open | router creates the K entry (pop-up per preferences); WARN "provider incident started" (kind, provider, reason) |
| open | another failure at P (any session) | open | entry updated (`affected_chat_count`, last reason); no pop-up |
| open | any successful attempt at P (any model) | resolved | entry resolved; `connection_restored` entry created (info, auto-handled; pop-up per preferences); chats that waited get their Verbose record line; WARN "provider incident ended" (duration, attempts, affected chat count) |
| resolved | failure of kind K at P within 10 min of resolving | open (re-opened) | the **same** entry re-opened (`updated`, unread, no pop-up); the restored entry stays — nothing is retracted |
| resolved | failure of kind K at P after 10 min | open (new episode) | a new entry, pop-up per preferences |
| any | gateway restart | — | at start-up, every unresolved provider/resource entry in the store seeds the tracker as `open` (no pop-up, no new entry); it resolves on the next success. Resolution is emitted even if the recipient has since switched Notify off for that kind (the entry exists, so it must not stay open) |

`provider_connections_failing` is derived: open while ≥ 2 distinct providers have an open `provider_unreachable` incident (one entry; `causes` lists each provider and reason); resolved — with a `connection_restored` entry "Connections to AI providers restored" — when fewer than 2 remain. No probe traffic is ever sent (D64).

Resources (measured hosts only): memory opens on `config.MemoryPressureHigh` false→true and resolves on true→false; `concurrency_reduced` opens when `applyMemoryCap` becomes the binding limit and resolves when it stops binding (D74). Disk (D73): a no-space failure in `fileutil.WriteFileAtomic` opens `resources:disk`; the next successful `WriteFileAtomic` resolves it. Hosts without a memory measurement (some Windows and container hosts) never raise the memory kinds; instead **Settings → Performance** shows one permanent line "Memory can't be measured on this host, so at most 2 agents run at once." (the floor `unmeasurableHostAgentFloor` = 2 in `pkg/agent/admission.go`), and no notification (D74).

### 14.4 Browser↔server outage report (R-LOG-3; round-1 MAJ-011)

Client (`src/lib/ws.ts` area, one state machine):
1. On the first unexpected close (not a page unload), record `{report_id: new UUID, kind, started_at: now, last_confirmed_at: time of the last frame or pong received, reconnect_attempts: 0, page_reloaded: false}` in that tab's session storage. `kind` = `offline` when `navigator.onLine` is false at that moment, else `server_unreachable`.
2. Each reconnect attempt increments `reconnect_attempts`; a reload during the outage sets `page_reloaded: true` and keeps the record.
3. On the next successful authentication: set `ended_at: now`, move the record to a pending queue (≤ 5 records; the oldest is dropped beyond 5), and POST it.
4. Retry while connected: 5 s, 30 s, 2 min, then every 10 min. Drop on 204, on 400 (logged to the browser console), or when older than 24 h. On 429, wait `Retry-After` before the next try.

Server:
- Idempotent on (user, `report_id`): a repeated id returns 204 and writes nothing new (an LRU of the last 1,000 ids per process, kept 24 h; after a restart a repeat may be logged twice — stated loss boundary).
- Logs WARN "client connection outage" with `user`, `client_outage_kind`, `started_at`, `ended_at`, `observed_duration_ms` (= ended − started), `max_possible_duration_ms` (= ended − last_confirmed), `reconnect_attempts`, `page_reloaded`. No free-text field exists, so nothing user-typed reaches the log.
- Two tabs produce two reports (one per tab); that is expected and documented.
- The existing `ws: connection closed unexpectedly` line moves from Debug to WARN with user and connection age (O4).
- Loss boundary (stated, not hidden): an outage is not reported if the tab is closed before it reconnects, or if every retry fails for 24 h.

**R-LOG-4**: every line that leaves the chat (R-SL-5 at Normal, R-SL-6) has its episode in the log (§14.2, §14.3, this subsection).

---

## 15. Security and privacy (R-SEC-1, R-SEC-2; D16, D23, D28)

### 15.1 Masking contract (round-1 MAJ-006)

One masking function (today's `scrubDetailText` in `pkg/agent/translate_error_provider_messages.go`, extended and exported, or moved to `pkg/logger` — the backend owner's choice) applies, in this order, before persistence and before the wire, to: `provider_says`, technical details, every notification title/body built from external text, every log field carrying provider text, the fallback note and the retry frame text.

| Rule | Masks | Replacement |
|---|---|---|
| M1 | every registered credential value (today's `logger.ScrubSensitiveValues`) — on the raw text **and** on the normalised text (§13.3) | `[redacted]` |
| M2 | a registered value split by whitespace or a line break (the value is matched with any whitespace run between its characters) | `[redacted]` |
| M3 | authentication carriers: `Authorization:` / `Proxy-Authorization:` header values, `Bearer <token>`, `Basic <token>`, and the values of JSON or query keys named `api_key`, `apikey`, `key`, `token`, `access_token`, `refresh_token`, `secret`, `password`, `x-api-key` | the value → `[redacted]` |
| M4 | key-shaped tokens: the existing `sk-[A-Za-z0-9][A-Za-z0-9_-]{4,}` (`keyShapedToken`) plus JWT shape (`eyJ` + base64url `.` base64url `.` base64url) | `[redacted]` |
| M5 | **partial echoes anchored on a known key prefix**: one of `sk-`, `sk-ant-`, `sk-or-`, `sk-proj-`, `xai-`, `gsk_`, `AIza`, `ghp_`, `github_pat_`, `hf_` followed by **≥ 1** further character from `[A-Za-z0-9_*.…-]` (catches `sk-…xyz`, `sk-****abcd`, `sk-ab`) | the whole token → `[redacted]` |
| M6 | long opaque runs: ≥ 24 consecutive characters from `[A-Za-z0-9_-]` mixing letters and digits | `[redacted]` |

Residual (stated, not hidden): a short fragment with no known prefix and fewer than 24 characters (e.g. `…a1b2c3`) cannot be told apart from ordinary text and is not masked. This is the one exception to R-SEC-1's "partial key echoes" and is put to the founder as **OQ-1** (§30.2; recommended: accept). It replaces the prior provider-messages spec's D5 (which accepted residual fragments in the raw Verbose view): from this feature on, the same contract applies to every surface and level, and the residual is only the class above (§28).

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
| Provider text | I — partial key echo reaches screen, store or log | M1–M6 before persistence and wire (§15.1) | T-13, T-30 |
| Provider text | T — HTML/Markdown injection via provider message | plain text rendering, control characters stripped (§13.3) | T-12, T-18 |
| Lifecycle | T — a stale snapshot rolls back a deletion | revisions + tombstones (§8.4) | T-20, T-40 |
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
| w2-048 `src/components/workspaces/team/useTeamMembership.ts::useTeamMembership::write` | "Added {agent} to {team}" with Undo | the same result-area pattern at the top of the team page, with **Undo** for 10 s |
| w3-010 `src/components/chat/IframePreview.tsx::IframePreview::handleCopy` | "Link copied. Anyone with this link can view the preview until it expires." | permanent helper text under the **Copy link** button stating who can open the link and until when; the button's own label changes to "Copied" for 2 s |
| w4-001, w4-002, w4-003 `src/components/panel-shell/panelTabSwitch.ts` | "{panel} is already open in another tab — switch" | a banner in the panel area: "{panel} is open in another tab." with **Switch** (and the manual-switch sentence when focusing failed) |
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

> Ids `S-xx` are used by the TDD plan and the traceability matrix. One action per When. Round 1 keeps every round-0 id whose meaning holds and adds new ids (S-09, S-15…S-19, S-26…S-29, S-37…S-39, S-47…S-49, S-57…S-59, S-63…S-66, S-73…S-78, S-86…S-89, S-91…S-94, S-98…S-104, S-106…S-121, S-123; S-105 and S-122 are unused); S-23 and S-24 changed meaning with D80.

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
- **Then** the open tab receives the entry with a non-empty id and a pop-up
- **And** the log has one ERROR line naming the recipient and kind

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

#### S-14: Refetch keeps live-only entries
**Traces to**: US-2 AC-2 · **Category**: Edge Case
- **Given** a live-only entry (`persisted: false`, revision 0) shown in the centre
- **When** the browser refetches the list on window focus
- **Then** the entry is still shown, marked "Not saved — shown on this screen only", and the badge count is unchanged

#### S-15: Attribution text never chooses a recipient
**Traces to**: US-1 AC-1 · **Category**: Error Path
- **Given** accounts `dana` and `lee`, and an agent also named `dana` that created task "Sync" (the task's creator field reads `dana`)
- **When** the task's automatic run fails
- **Then** both `dana` and `lee` receive one `task_failed` entry (workspace object, every configured account)
- **And** the recipient list was not derived from the creator field (the same result with the creator field empty)

#### S-16: Session without an owner reaches every account
**Traces to**: US-1 AC-1 · **Category**: Alternate Path
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

#### S-101: A slow snapshot never rolls back a newer push
**Traces to**: US-2 AC-4 · **Category**: Edge Case
- **Given** a list request answered with revision 10 and, before it arrives, a pushed update of entry F with revision 11
- **When** the snapshot is applied
- **Then** F keeps its revision-11 content (DS-RECON)

#### S-102: A repeated clear frame changes nothing
**Traces to**: US-2 AC-2 · **Category**: Edge Case
- **Given** a `notification_cleared` frame already applied
- **When** the same frame arrives again
- **Then** the list and badge are unchanged

#### S-103: Frame variants are strict
**Traces to**: US-2 AC-1 · **Category**: Error Path
- **Given** contract fixtures for every frame variant
- **When** a `notification` frame with `change: read` and `popup: true`, or a `notification_deleted` frame carrying a `notification` object, is validated
- **Then** validation fails; every positive fixture passes (DS-FRAME)

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
- **When** both complete
- **Then** the file holds either the new entry alone or nothing with the new entry delivered after it, never a half-written file; the revision increased once per change

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
- **Then** one Error line reads `OpenRouter rejected the API key: "Incorrect API key provided: [redacted]". Update the key in Settings → Providers.` with an **Open settings** action

#### S-51: Network retry line names the cause, countdown and attempt
**Traces to**: US-6 AC-2 · **Category**: Happy Path
- **Given** provider P refuses connections and the retry policy allows 5 attempts
- **When** the agent retries after the first failed attempt
- **Then** an animated Warning line reads `I can't reach P: connection refused. Retrying in <n> s (attempt 2 of 5)…` at every detail level

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

#### S-53: Browser loses the server
**Traces to**: US-6 AC-4 · **Category**: Happy Path
- **Given** the gateway stops for 20 s
- **When** the browser notices the loss
- **Then** an app-wide indicator and one chat line show "Can't reach the server right now. Your agents keep working."
- **And** after the gateway is back both disappear and the reloaded chat history contains neither

#### S-54: Line action
**Traces to**: US-6 AC-5 · **Category**: Happy Path
- **Given** a `model_retired` Error line
- **When** the user activates **Switch model**
- **Then** the chat's model picker opens

#### S-55: Replay parity, live and history de-duplicated
**Traces to**: US-6 AC-6 · **Category**: Edge Case
- **Given** a chat with an Error line carrying a provider message and a Verbose record line
- **When** the page is reloaded and the history is paged back
- **Then** both lines render with the same text, type, action and details as live, including the quoted provider message
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

#### S-117: Stop outcome is a chat line, not a notification
**Traces to**: US-6 AC-1, US-1 AC-6 · **Category**: Happy Path
- **Given** a Stop that could not finish every request
- **When** the outcome is known
- **Then** one Warning line appears in that chat and no centre entry or pop-up exists

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
- **When** a write fails for lack of space, and later a write succeeds
- **Then** one `resources_low` (disk) entry opens and is then resolved

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
- **Then** `<secret>` never appears and `[redacted]` does

(Examples: dataset DS-SEC — every row × surfaces main line, details, centre body, log line, fallback note, replayed history.)

#### S-91: Prefix-anchored partial echo is masked
**Traces to**: US-10 AC-1 · **Category**: Edge Case
- **Given** a provider message `Incorrect API key provided: sk-…wxyz`
- **When** it is shown on the main line
- **Then** it reads `Incorrect API key provided: [redacted]`

#### S-92: A registered value split by a line break is masked
**Traces to**: US-10 AC-1 · **Category**: Edge Case
- **Given** a registered credential value echoed with a line break in its middle
- **When** it is extracted and normalised
- **Then** neither half appears on any surface

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
| T-48 | contract fixtures: one positive and one negative fixture per frame variant and per new schema, validated by the generated Go types and the generated Zod schemas (`make verify-contracts` plus a fixture test in `pkg/api/generated` and `src/lib/api/generated`) | Unit | S-103 | strict frames, DS-FRAME (WP-0, first) |
| T-01 | `pkg/notifications::TestRouterRejectsUnknownKind` | Unit | S-06 | closed enum |
| T-02 | `pkg/notifications::TestRouterMintsIDWhenStoreFails` | Unit | S-07 | #645 Defect 1 closed; `persisted: false`, revision 0 |
| T-39 | `pkg/notifications::TestRecipientRules` (R1 set / empty / unreadable, R2 every account, R3, attribution fields ignored, no accounts) | Unit | S-08, S-15, S-16 | MAJ-005 |
| T-03 | `pkg/notifications::TestRouterAppliesPreferences` (table: global, workspace per field, inherit, system-wide global only, three locked kinds, notify-off) | Unit | S-02, S-31, S-32, S-36, S-37 | resolution order |
| T-04 | `pkg/notifications::TestRetentionSweepAgeBoundary` (incl. waiting entries and quarantine files) | Unit | S-20, S-25, S-26 | 30 days, D80 |
| T-05 | `pkg/notifications::TestCapNeverEvictsUnresolvedNeedsYou` (incl. > 50 protected) | Unit | S-21, S-27 | eviction rule |
| T-06 | `pkg/notifications::TestDeleteAndClear` (waiting entries deletable; request untouched; idempotent clear; revision per change) | Unit | S-22, S-23, S-24, S-28, S-102 | D80, real deletion |
| T-07 | `pkg/notifications::TestResolveLifecycle` (created/updated/read/resolved, coalesce on unresolved key) | Unit | S-13, S-04 | lifecycle |
| T-08 | `pkg/notifications::TestRecipientFileNames` (DS-NAME) | Unit | S-93 | SHA-256 names, stored-username check |
| T-09 | `pkg/notifications::TestPreferencesLoadStates` (missing → defaults, corrupt → unreadable, never overwritten without replace flag) | Unit | S-35, S-38 | degraded mode |
| T-29a | `pkg/notifications::TestSidecarLockAroundEveryMutation` (concurrent create/clear/sweep; Windows build vetted with `GOOS=windows go vet`) | Unit | S-29 | MIN-005 |
| T-10 | `pkg/providers::TestFailureClassDataset` (DS-NET) | Unit | S-80, S-86 | classification table |
| T-11 | `pkg/providers::TestRetryDecisionsUnchangedByClass` (existing fallback datasets re-run, outcome equality incl. cancellation and partial output) | Unit | S-85 | FR-063 |
| T-12 | `pkg/agent::TestProviderSaysExtraction` (DS-EXTRACT) | Unit | S-50, S-56 | extraction and order mask → normalise → mask → cap |
| T-13 | `pkg/agent::TestMaskingContract` (DS-SEC, M1–M6, residual row) | Unit | S-90, S-91, S-92, S-94 | masking |
| T-45 | `pkg/agent::TestPerCodeFactsAndVariants` (DS-FACT: every code × no-facts / provider / quoted / retrying / final; trust flag unchanged) | Unit | S-56, S-57, S-58, S-72, S-107 | MAJ-008 |
| T-14 | `pkg/agent::TestCatalogueMatchesWordingBaseline` (in-chat and outside-chat forms; N2 lines; copy-rule tests in `pkg/agent/translate_error_test.go` and `src/lib/llm-error.test.ts`) | Unit | S-72, S-70, S-77, S-78 | baseline, both voices |
| T-15 | `pkg/agent::TestNetworkSplitCodes` (`network` vs `provider_error`) | Unit | S-51, S-80 | R-EV-2 |
| T-16 | `scripts/check-system-message-voice.sh` self-check: one planted fixture per check and surface fails; the clean fixture passes | Unit | S-71, S-73, S-74 | the guard can fail |
| T-47 | `pkg/notifications::TestWordingManifestComplete` (every id has a disposition; survivors have text and a self-check receipt; no unlisted string in a scanned source) | Unit | S-75 | MAJ-018 |
| T-37 | `pkg/agent::TestTaskRunClassification` (Calendar scheduled, automatic, manual, user-stopped, dispatch failure, plan origin) | Unit | S-01, S-09, S-17, S-18 | MAJ-004 |
| T-38 | `pkg/agent::TestProviderAttemptObservedOncePerDispatch` (nested retries, fallback, exclusions; lists every caller of the provider chat interface) | Unit | S-86, S-87 | MAJ-009 |
| T-46 | `pkg/gateway::TestIncidentStateMachine` (DS-INC: open, update, resolve by any model, 10-min re-open, restart seed, connections-failing threshold, disk, memory, model-specific excluded) | Unit | S-88, S-89, S-108, S-109, S-110, S-111, S-123 | MAJ-010 |
| T-17 | `src/lib/detailLevel.test.ts` (every L1–L11 × level, DS-DL, literal L6/L8) | Unit | S-60, S-65, S-66 | one rule |
| T-18 | `src/components/chat/SystemLine.test.tsx` (5 types with accessible prefix, in-progress, reduced motion, action, details by level and kept across level changes, no empty quotes, plain-text provider message) | Unit | S-50, S-54, S-56, S-62, S-63 | renderer |
| T-19 | `src/lib/systemLineAdapters.test.ts` (one row per §11.2 carrier) | Unit | S-50…S-55, S-107, S-117 | mapping |
| T-20 | `src/store/notifications.test.ts` (change kinds, badge counts, live-only kept) | Unit | S-11, S-14, S-22 | store |
| T-40 | `src/store/notifications.reconcile.test.ts` (DS-RECON: tombstones, snapshot revision rules, refetch on re-auth) | Unit | S-100, S-101, S-102 | MAJ-003 |
| T-41 | `src/lib/outageReport.test.ts` (DS-OUT-CLIENT: record, reload, queue ≤ 5, backoff, 204/400/429, 24 h drop) | Unit | S-112, S-113, S-114, S-115 | MAJ-011 client |
| T-43 | `src/lib/notificationTargets.test.ts` (DS-TARGET: every screen and action with its required ids; missing target text) | Unit | S-05, S-59 | MAJ-013 |
| T-21 | `src/components/ui/popupHost.test.tsx` (DS-PU: durations, hover and focus pause with remaining time, cap, "+N more" opens the centre, queued full duration, resolve removes, once-only announcement) | Unit | S-40, S-41, S-42, S-47, S-48 | pop-up rules |
| T-22 | `pkg/gateway::TestNotificationRESTDeleteClearPrefs` (204/404, no 409 on delete, preference codes 400/409) | Integration | S-22…S-24, S-33, S-34, S-38, S-39, S-104 | REST contract codes |
| T-42 | `pkg/gateway::TestPreferencesRevisionAndInheritance` (DS-PREF) | Integration | S-37, S-38, S-39 | MAJ-012 |
| T-23 | `pkg/gateway::TestNotificationChangePushedToAllRecipientConnections` (all three frame types, recipient-only) | Integration | S-10, S-11, S-22 | cross-tab push |
| T-24 | `pkg/gateway::TestEveryKindHasLiveProducer` (one subtest per Part-1 kind driving the **real** producer of §8.1 — Calendar trigger, gateway runner, run lifecycle, plan engine, approval and card paths, attempt observation, memory and disk hooks — reading `GET /notifications`) | Integration | S-01, S-05, S-18, S-19 | reachability (19 kinds) |
| T-25 | `pkg/gateway::TestApprovalResolvedResolvesNotification` / `TestAskTerminalResolvesNotification` | Integration | S-10, S-12, S-13 | D19 |
| T-26 | `pkg/gateway::TestProviderIncidentOneEntryAffectedChats` | Integration | S-04 | R-EV-3/4 |
| T-27 | `pkg/gateway::TestOutageReportValidationIdempotencyRateLimit` (DS-OUT) | Integration | S-81, S-82, S-83, S-112 | R-LOG-3 server |
| T-28 | `pkg/gateway::TestSystemLineHistoryContract` (persist, replay with `history_origin`, prepared page byte weight, REST `Message.system_line`, live/history semantic parity, dedupe by `line_id`) | Integration | S-55, S-106 | MAJ-002 |
| T-29 | `pkg/agent::TestPerAttemptWarnLine` + `TestIncidentStartEndLogged` (log capture at level `warn`) | Integration | S-80, S-84, S-87 | R-LOG-1/2/4 |
| T-30 | `pkg/gateway::TestScheduleFailedBodyHasNoRawError` | Integration | S-01, S-90 | masking + translation |
| T-44 | `pkg/gateway::TestPart1NoNewOutboundSend` (the router never publishes outbound) + `TestPublishChannelAlertUnchanged` (characterisation of today's alert through the real `scheduledRunner` failure path) | Integration | FR-090 | D62 boundary |
| T-31 | `tests/e2e/notifications-centre.spec.ts` (filters, navigate, states, delete, clear all, two tabs, offline delete reconcile) | E2E | S-03, S-05, S-11, S-22, S-24, S-43, S-100 | user path |
| T-32 | `tests/e2e/notifications-needs-you.spec.ts` (approval in another tab, resolve everywhere, D70 rule, no modal, delete keeps request) | E2E | S-10, S-23, S-45, S-46, S-97 | D19, D30, D80 |
| T-33 | `tests/e2e/notifications-preferences.spec.ts` (switches, locks, workspace tri-state, unreadable, conflict) | E2E | S-30, S-31, S-32, S-34, S-37, S-39 | preferences UI |
| T-34 | `tests/e2e/chat-system-lines.spec.ts` (fake provider: refused → recover; final failure with host; 401 echo; fallback quote; levels) | E2E | S-50, S-51, S-52, S-53, S-55, S-58, S-107 | system lines |
| T-35 | `tests/e2e/chat-detail-levels.spec.ts` | E2E | S-60, S-61, S-62, S-63, S-64 | detail levels |
| T-36 | `tests/e2e/own-action-results.spec.ts` (sampled sites per WP-5 category, journeys of §16) | E2E | S-44, S-95, S-96, S-116 | removals |
| T-49 | `tests/e2e/notifications-a11y.spec.ts` (keyboard-only journey, focus repair, announcements, 320 px and 400 % zoom) | E2E | S-118, S-119, S-120, S-121, S-47 | MAJ-017 |
| T-50 | `tests/e2e/page-indicators-and-homes.spec.ts` (new-version banner, panel Switch banner, preview helper text, unmeasurable-memory line) | E2E | S-49, S-98, S-99, S-110 | D77, D78 |

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
| 15 | a registered key split as `sk-or-v1-0123` + newline + `456789abcdef` | `[redacted]` — the second mask after normalisation catches it | S-92 |
| 16 | `"Bad key"` with surrounding quotes | `"Bad key"` kept as text; the sentence's own quotes are not doubled | S-50 |

#### DS-SEC — masking (T-13, S-90…S-94)

| # | Echo | Rule | Must not appear | Traces to |
|---|---|---|---|---|
| 1 | `Incorrect API key provided: sk-proj-abcdEFGH1234` | M4/M5 | `sk-proj-abcdEFGH1234` | S-90 |
| 2 | `Incorrect API key provided: sk-or-v1-0123456789abcdef` | M4/M5 | `sk-or-v1-0123456789abcdef` | S-90 |
| 3 | a registered credential value, verbatim | M1 | that value | S-90 |
| 4 | `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abc123` | M3/M4 | the token | S-90 |
| 5 | `key sk-ab rejected` (short, prefix-anchored) | M5 | `sk-ab` | S-91 |
| 6 | `Incorrect API key provided: sk-…wxyz` | M5 | `sk-…wxyz` | S-91 |
| 7 | `key sk-****abcd invalid` | M5 | `sk-****abcd` | S-91 |
| 8 | registered value split by a line break | M2 | either half | S-92 |
| 9 | `{"api_key":"AbC123xyz","model":"m"}` | M3 | `AbC123xyz` | S-90 |
| 10 | `token=Zx81Lk2Pq9` in a URL query | M3 | `Zx81Lk2Pq9` | S-90 |
| 11 | `ref 9f8e7d6c5b4a39281706f5e4d3c2b1a0` (32 hex, letters and digits) | M6 | the run | S-90 |
| 12 | `ghp_` + 36 chars | M5 | the token | S-90 |
| 13 | `…a1b2c3` (short, no known prefix) | residual (OQ-1) | — expected output equals input; the row exists so any change to the residual is visible | S-90 |
| 14 | text the masker cannot process | failure path | the text, on every surface | S-94 |

Surfaces per row: main line, technical details, centre body, log line, fallback note, replayed history (6 surfaces).

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

#### DS-OUT-CLIENT — browser report state machine (T-41)

| # | Sequence | Expected | Traces to |
|---|---|---|---|
| 1 | close → reconnect → POST 204 | record cleared, one POST | S-81 |
| 2 | close → reload → reconnect | one POST with `page_reloaded: true` | S-114 |
| 3 | POST times out after server accepted | retried with the same `report_id` after 5 s | S-112 |
| 4 | POST 429 `Retry-After: 30` | next try ≥ 30 s later | S-115 |
| 5 | POST 400 | dropped, console line, no retry | S-82 |
| 6 | 6 outages queued while POSTs fail | queue keeps the newest 5 | S-114 |
| 7 | record older than 24 h | dropped | S-114 |
| 8 | page unload (user closes the tab) | no record created | S-81 |
| 9 | `navigator.onLine` false at close | `kind: offline` | S-81 |

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

| # | Local state | Arrives | Expected | Traces to |
|---|---|---|---|---|
| 1 | E rev 5 | snapshot R = 9 without E | E removed | S-100 |
| 2 | F rev 11 (pushed) | snapshot R = 10 with F rev 8 | F stays at rev 11 | S-101 |
| 3 | tombstone G @ 7 | `notification` G rev 6 | ignored | S-101 |
| 4 | tombstone G @ 7 | `notification` G rev 8 (re-created) | G shown | S-101 |
| 5 | live-only H (rev 0) | snapshot without H | H kept | S-14 |
| 6 | cleared {A, B} applied | the same `notification_cleared` again | no change | S-102 |
| 7 | — | WebSocket re-auth | one refetch | S-100 |

#### DS-FRAME — contract fixtures (T-48)

| # | Fixture | Expected | Traces to |
|---|---|---|---|
| 1 | `notification` created, popup true | valid | S-103 |
| 2 | `notification` read, popup true | invalid | S-103 |
| 3 | `notification` resolved, popup false | valid | S-103 |
| 4 | `notification` without `notification` object | invalid | S-103 |
| 5 | `notification_deleted` with id and revision | valid | S-103 |
| 6 | `notification_deleted` with a `notification` object | invalid | S-103 |
| 7 | `notification_cleared` with 0 ids | invalid | S-103 |
| 8 | `notification_cleared` with 1,001 ids | invalid | S-103 |
| 9 | `Notification` with top-level `session_id` | invalid (moved into target) | S-103 |
| 10 | `Notification` target screen `none` | invalid | S-05 |
| 11 | `SystemLineFrame` historical copy without `history_origin` | invalid | S-106 |
| 12 | `NotificationPreferencesUpdate` without `base_revision` | invalid | S-39 |

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
| 13 | write fails with no space; next write succeeds | disk opens; resolves | S-109 |
| 14 | memory measurement unavailable | no memory kinds ever | S-110 |
| 15 | recipient turned Notify off while open; P succeeds | entry still resolved | S-89 |

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

Every code of §13.7 × variants {no facts, provider/model known, `provider_says` present}, plus `network` and `provider_error` × {retrying with cause, retrying without cause, final}; expected value = the §13.2 / WORDING §1 sentence with slots filled; assertion: no empty slot, no empty quotes, `provider_message` trust flag equal to today's for the same code.

#### DS-TARGET — targets and actions (T-43)

One row per `screen` and per action `kind` of §7.6: required ids present → navigates to that object; one required id missing → contract invalid (DS-FRAME); object deleted → the "no longer exists" text; `retry_reply` for a turn that is no longer the latest → "This reply can no longer be retried."

### 20.2 Regression requirements

| Existing behaviour | Existing test(s) | New regression test |
|---|---|---|
| Coalescing by `CoalesceKey` | `pkg/notifications/store_test.go` | extended by T-07 |
| Unreadable session owner never broadcasts (goal pause) | goal-pause notifier tests in `pkg/agent`, `pkg/gateway` | T-39 |
| Knowledge-drift notification content and recipients | drift notifier tests in `pkg/gateway` | none new; must stay green unchanged except kind/category fields |
| Retry / fallback / cooldown outcomes | `pkg/providers/fallback_*_test.go`, `error_classifier_test.go` | T-11 asserts outcome equality |
| `TranslateLLMError` classification of codes | `pkg/agent/translate_error_test.go`, `translate_error_billing_test.go` | updated for `provider_error`; every other code unchanged; T-45 |
| `WireLLMError` explicit-code override and the `provider_message` trust flag | `pkg/agent` wire tests, `src/lib/llm-error.test.ts` | T-45 |
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
| FR-005 | All producers MUST go through one router that rejects unknown kinds, resolves recipients by §8.1 R1–R3, mints a non-empty id before any branch, never broadcasts when a session owner is unreadable, and delivers live (`persisted: false`) when storage fails. | R-NC-5, #645 |
| FR-005a | Recipients MUST NOT be derived from task, plan or schedule attribution fields that may hold an agent's name. | R-NC-5, MAJ-005 |
| FR-006 | Every entry MUST carry a `target` with the ids its screen requires (§7.6); activating the entry or its pop-up action MUST navigate there, and a missing object MUST be stated, not acted on. | R-NC-6, R-PU-4 |
| FR-007 | Every lifecycle change (created, updated, read, resolved, deleted, cleared) MUST reach every connection of the recipient within 2 s through the three strict frame types of §7.2; every §6 resolve trigger MUST resolve the entry. | R-NC-3 |
| FR-008 | Entries whose age is ≥ 30 days MUST be deleted from storage at start-up, every 6 h and on writes — waiting Needs-you entries included — and quarantined corrupt files older than 30 days MUST be deleted. | R-NC-4, R-SEC-2, D80 |
| FR-009 | The count cap MUST never evict an unresolved Needs-you entry; delete and Clear all MUST work for every entry, waiting ones included, and MUST NOT approve, deny or cancel the underlying request. | R-NC-4, D80 |
| FR-010 | The browser MUST reconcile pushes and snapshots by store revision and tombstones per §8.4, refetching on every WebSocket re-authentication and keeping live-only entries. | R-NC-3, #645 |
| FR-011 | Provider failures MUST raise `provider_unreachable` for network-class failures and `provider_error` only for the provider- or account-level API conditions of D71, as distinct kinds; model-specific conditions MUST stay chat-only. | R-EV-2, D71 |
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
| FR-033 | A connection-problem line MUST show at every level while it lasts, naming the cause as soon as observed, the countdown and the attempt counter; on recovery it MUST disappear at Normal and leave one record line at Verbose/Debug. | R-SL-5, D64 |
| FR-034 | Browser↔server loss MUST show one app-wide indicator and one line in the open chat, neither persisted. | R-SL-6 |
| FR-035 | A line MUST carry at most one action with its required ids; a stale action MUST say so instead of acting. | R-SL-7 |
| FR-036 | New server-authored lines MUST persist as `Message.system_line` entries and pass every history stage of §7.5 (replay with `history_origin`, prepared page, REST history), rendering with semantic parity and de-duplicated by `line_id`. | R-SL-1, R-SL-5 |
| FR-040 | A Normal/Verbose/Debug detail level MUST replace the Verbose switch; one rule function MUST decide every row × level per §12.2 with L6 and L8 literal; changing the level MUST NOT discard details already received. | R-DL-1, D75 |
| FR-041 | Technical details MUST render only at Verbose (collapsed) and Debug (open); the masked failure payload is the only raw payload shown. | R-DL-2 |
| FR-042 | Every user MUST be able to select Debug; Debug MUST show the internal ids, request ids and full masked failure details that exist, and MUST NOT require a capture of successful provider traffic. | R-DL-3, D66 |
| FR-050 | The generated catalogues MUST equal the WORDING §1 sentences, with the `network` / `provider_error` lines of §13.2. | R-WD-1, R-WD-3, R-WD-9 |
| FR-051 | The provider's own message MUST be extracted per §13.3, masked, persisted, and quoted on the main line and in the fallback note; a sentence MUST never render an empty slot or empty quotes. | R-WD-2, R-WD-3 |
| FR-052 | A CI guard MUST fail on "Omnipus" as actor (except D48), on "we/our/us" in chat, on first person outside a chat, and on "!", emoji, "sorry", "oops", for every source in the wording manifest, and MUST be shown able to fail per check. | R-WD-5, R-WD-6, R-WD-7 |
| FR-053 | Messages outside a chat MUST be neutral; a message used both inside and outside MUST have both forms. | R-WD-6, R-WD-8 |
| FR-054 | Every new or surviving system message MUST be written with the `omnipus-voice` skill and carry a self-check receipt in the wording manifest; every one of the 392 review ids MUST have a disposition. | R-WD-4, R-WD-9, D65 |
| FR-055 | Final network-failure lines MUST name host, observed cause and one Fix; no message MUST claim the internet is down; several failing providers MUST be reported as "Connections to AI providers are failing" with the observed causes. | R-WD-1, D64 |
| FR-056 | Slot values MUST be taken from the facts at the emit site per §13.7; the server MUST assemble the finished sentence and keep today's `provider_message` trust flag behaviour. | R-WD-2, MAJ-008 |
| FR-060 | Every failed dispatched provider attempt MUST log exactly one WARN line at the `accountProviderAttempt` seam with `failure_class`, `reason`, provider, model, host, status (API), masked provider message, `attempt_seq`, `candidate`, `retry_round`, session, turn and request ids; non-dispatched or cancelled attempts MUST NOT be logged as failures. | R-LOG-1, R-LOG-2 |
| FR-061 | Failure class MUST follow the §14.1 table, computed independently of the retry classifier. | R-LOG-2, R-EV-2, D72 |
| FR-062 | The browser MUST report each server outage per §14.4 (report id, retries until accepted, 24 h limit); the server MUST validate, rate-limit, de-duplicate by report id and log both observed and longest possible duration at WARN; the server's unexpected-close line MUST be WARN. | R-LOG-3 |
| FR-063 | Retry, cooldown and failover decisions MUST be identical before and after this change. | safety |
| FR-064 | Incidents MUST log start and end (duration, attempts, affected chats) at WARN. | R-LOG-1, R-LOG-4 |
| FR-065 | `provider_connections_failing`, `resources_low` and `concurrency_reduced` MUST be produced per §14.3 (no probe traffic; disk on no-space write failure; memory only on measured hosts), and an unmeasurable host MUST show the permanent Settings → Performance line instead. | R-EV-1, D64, D73, D74 |
| FR-066 | Removing a notification MUST NOT change any request; resolving MUST still work for an entry seeded from storage after a restart, whatever the recipient's current Notify switch. | R-NC-3, D80 |
| FR-070 | One masking function implementing M1–M6 MUST be applied before persistence and before the wire to every provider-derived text, on every surface and level; when masking fails the text MUST be dropped. | R-SEC-1 |
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

---

## 22. Success criteria

| SC | Criterion |
|---|---|
| SC-001 | T-24 passes with one subtest per Part-1 kind (19 kinds), each through its §8.1 producer; 0 kinds without a producer. |
| SC-002 | In the two-tab E2E (T-32), a handled approval disappears from the second tab in ≤ 2 s in 20/20 runs. |
| SC-003 | Repo grep: 0 imports of `ToolApprovalModal`; 0 references to `INLINE_TOOL_APPROVALS_ENABLED`; 0 occurrences of `verboseChatEnabled`; 0 `internet_lost`. |
| SC-004 | Own-action pop-up call sites after WP-5: only the 2 dev-only sites; every removed site has a matching page-indicator test or a "page already shows it" citation; the 10 local conditions each have an indicator test (T-50). |
| SC-005 | The voice guard passes on the real sources and fails on each planted fixture (exit code non-zero, 5 fixtures). |
| SC-006 | DS-SEC: 0 occurrences of any secret across rows 1–12 and 14 × 6 surfaces (78 checks); row 13 output equals input (documented residual). |
| SC-007 | DS-NET: 22/22 rows as the table says; T-11 retry/fallback outcomes 100 % equal to pre-change. |
| SC-008 | With the default log level `warn`, a forced unreachable provider with 5 attempts produces exactly 5 "provider attempt failed" lines with `failure_class=network` and one incident start and end line. |
| SC-009 | After a 30-day sweep, `notifications/` holds no entry and no quarantine file whose age is ≥ 30 days. |
| SC-010 | Every hands-on UAT row in §24 is PASS by the tester and upheld by the independent validator on the exact candidate commit. |
| SC-011 | The wording manifest lists 392/392 ids with a disposition; every surviving id has final text and a self-check receipt (T-47). |
| SC-012 | DS-RECON 7/7 and DS-OUT-CLIENT 9/9 rows pass; an offline-deleted entry is gone within 2 s of reconnect in T-31. |
| SC-013 | docs-verifier marks every claim on every §25 page TRUE, with 0 missing pages. |

---

## 23. Traceability matrix — requirement → FR → scenario → test → documentation

| R-… / decision | FR | Scenario(s) | Test(s) | Docs (§25) |
|---|---|---|---|---|
| R-NC-1 | FR-003 | S-01, S-02, S-09, S-17 | T-03, T-24, T-37 | DOC-01, DOC-05, DOC-06 |
| R-NC-2 | FR-004, FR-083 | S-03, S-118 | T-31, T-49 | DOC-01 |
| R-NC-3 | FR-007, FR-010, FR-066 | S-10, S-11, S-12, S-13, S-14, S-100, S-101, S-102, S-103 | T-07, T-20, T-23, T-25, T-32, T-40, T-48 | DOC-01, DOC-14 |
| R-NC-4 | FR-008, FR-009 | S-20, S-21, S-22, S-23, S-24, S-25, S-26, S-27, S-28, S-29 | T-04, T-05, T-06, T-22, T-29a, T-31 | DOC-01 |
| R-NC-5 | FR-005, FR-005a, FR-072 | S-07, S-08, S-15, S-16, S-93 | T-02, T-08, T-24, T-39 | DOC-01 (who receives what) |
| R-NC-6 | FR-006 | S-05, S-18, S-19, S-43 | T-24, T-31, T-43 | DOC-01 |
| R-EV-1 | FR-001, FR-002, FR-065 | S-06, S-05, S-01, S-109, S-110, S-111 | T-01, T-24, T-46 | DOC-01, DOC-03 |
| R-EV-2 | FR-011, FR-061 | S-51, S-80, S-123 | T-10, T-15, T-46 | DOC-08, DOC-09 |
| R-EV-3 | FR-012 | S-04 | T-26 | DOC-08 |
| R-EV-4 | FR-013 | S-04, S-13, S-88, S-89, S-108 | T-07, T-26, T-46 | DOC-01, DOC-08 |
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
| R-SL-1 | FR-030, FR-036 | S-50, S-55, S-106, S-117 | T-18, T-19, T-28 | DOC-02, DOC-14 |
| R-SL-2 | FR-031 | S-50, S-121 | T-18, T-49 | DOC-02 |
| R-SL-3 | FR-031 | S-50, S-51, S-52 | T-18 | DOC-02 |
| R-SL-4 | FR-032 | S-51, S-120 | T-18, T-49 | DOC-02 |
| R-SL-5 | FR-033, FR-036 | S-51, S-52, S-57 | T-28, T-34 | DOC-02, DOC-08 |
| R-SL-6 | FR-034 | S-53 | T-34 | DOC-02, DOC-09 |
| R-SL-7 | FR-035 | S-54, S-59 | T-18, T-34, T-43 | DOC-02 |
| R-DL-1 | FR-040 | S-60, S-61, S-63, S-65, S-66 | T-17, T-35 | DOC-02, DOC-03, DOC-09, DOC-10, DOC-13 |
| R-DL-2 | FR-041 | S-60, S-62 | T-17, T-18 | DOC-02 |
| R-DL-3 | FR-042 | S-62, S-64 | T-35 | DOC-02, DOC-11 |
| R-WD-1 | FR-050, FR-055 | S-58, S-72, S-76 | T-14, T-45 | DOC-08, DOC-09 |
| R-WD-2 | FR-051, FR-056 | S-50, S-56, S-107 | T-12, T-18, T-45 | DOC-02, DOC-08 |
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
| R-SEC-1 | FR-070 | S-50, S-62, S-90, S-91, S-92, S-94 | T-13, T-30 | DOC-12 |
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
| R-RM-1a | FR-081 | S-96, S-98, S-99 | T-36, T-50 | DOC-02, DOC-05, DOC-06 |
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
| D71 | FR-011 | S-123 | T-46 | DOC-08 |
| D72 | FR-061 | S-80 (DS-NET rows 7, 8) | T-10 | DOC-09 |
| D73, D74 | FR-065 | S-109, S-110 | T-46, T-50 | DOC-01, DOC-03 |
| D75 | FR-040 | S-60, S-65, S-66 | T-17 | DOC-02 |
| D76 | FR-009 | S-21, S-27 | T-05 | DOC-01 |
| D77 | FR-024 | S-49 | T-50 | DOC-01, DOC-02 |
| D78 | FR-081 | S-96, S-98, S-99 | T-36, T-50 | DOC-02, DOC-05, DOC-06 |
| D79 | FR-030 | S-50…S-55 | T-19 | — |
| D80 | FR-009, FR-066 | S-23, S-24, S-26, S-28 | T-06, T-32 | DOC-01 |
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
| U7 | Copy the OpenRouter provider with an invalid key `sk-or-v1-0000000000000000invalid`; send | Error line `OpenRouter rejected the API key: "…"` with the key masked; **Open settings**; Debug shows ids and details with the key masked | screenshots at Normal/Verbose/Debug; log line `failure_class=api` with masked text |
| U8 | Settings → Notifications: turn **Task or plan failed** Notify off; make a task fail | No entry, no pop-up; the task chat still shows the failure | screenshots |
| U9 | Workspace Settings → set **Task or plan started** Notify to On; start a task there and in another workspace; then set it back to **Use global** | Entry only for the overridden workspace; System-wide kinds not offered; after Use global, no entry | screenshots |
| U10 | Try to switch off either switch of **Agent needs your approval** and of **Goal paused** | Switches locked with explanation | screenshots |
| U11 | Trigger 5 info events quickly (e.g. 5 short Calendar tasks finishing) | 3 visible + "+2 more — open notifications"; it opens the centre listing all 5; hover pauses; non-hovered vanish after 5 s | screen recording or timed screenshots |
| U12 | Stop the gateway for 20 s, then start it | App-wide indicator + chat line during the outage; both gone after; reload: not in history; gateway log has one "client connection outage" with `observed_duration_ms` about 20,000 | screenshots + log excerpt |
| U13 | Turn macOS Wi-Fi off for 15 s, then on | Offline line + indicator; outage logged with `client_outage_kind=offline` | screenshots + log |
| U14 | Visual sweep of every main screen (chat, workspace tabs, settings, library, mail), at full width and at 320 px | No badge in the top-right corner; unread count only in the sidebar account menu | screenshots |
| U15 | Save settings on 5 screens (Performance, Chat, Profile, Providers, workspace Settings), once succeeding, once failing (stop the server mid-save), then Retry | No pop-up; on-page "Saved" / "Not saved — Retry" / "Saved" | screenshots |
| U16 | Delete one centre entry, then Clear all, in two tabs | Entries gone in both tabs and after reload | screenshots |
| U17 | Detail level Normal/Verbose/Debug on one chat with tool calls, a delegation, a backup-model reply and a failed reply | Rows per §12.2 (validator checks cell by cell for the rows present), including the fallback note hidden at Normal after the reply | 3 screenshots |
| U18 | Read the chat lines of U5/U7 and the centre entries | Chat lines say "I …"; centre/pop-ups neutral; no "we/our/us"; no "Omnipus" as actor; no "internet" claim | screenshots |
| U19 | Calendar: drag an event to another week, then navigate away from that week | Undo still available in the page's result area for 10 s; pressing it moves the event back; no pop-up | screenshots |
| U20 | Keyboard only: open the centre, filter Needs you, open an entry, delete another, Clear all, Escape | Every step reachable; focus returns to the account-menu button | screen recording |
| U21 | 320 px wide window: a chat with a long quoted provider error and an action, the centre, a pop-up, Settings → Notifications | No horizontal scrolling; actions visible and usable | screenshots |
| U22 | With an approval waiting, delete its centre entry, then approve in the chat | Entry deleted; card still pending until approved; approval proceeds; no entry reappears | screenshots |
| U23 | Stop the gateway, close the laptop lid for 2 min, start the gateway, open the lid | One outage line with `observed_duration_ms` and a larger `max_possible_duration_ms` | log excerpt |
| U24 | Two custom providers pointing at closed local ports, each used by one chat | One "Connections to AI providers are failing" entry listing both causes; no internet claim | centre screenshot |

Rows U1–U3, U5–U7, U12, U22 touch `pkg/agent` / gateway WebSocket handling and are mandatory before landing (root `CLAUDE.md`). Stable rows become E2E tests over time (T-31…T-36, T-49, T-50 cover U1–U3, U8–U11, U15–U17, U19–U22 in automated form).

---

## 25. User-facing documentation (first-class deliverable)

The implementing leads draft every page below in the same cut (FR-091); `docs-verifier` audits each against the built behaviour before the gate; **a missing page or a missing TODO is a gate finding** (Important). The backend implementer drafts DOC-08…DOC-12; the frontend implementer drafts the rest; both review DOC-01.

| Id | Page | New / changed | TODOs |
|---|---|---|---|
| DOC-01 | `docs/notifications.md` | **new** | (1) The centre: where it is, the three categories and counts, "Handled", delete, Clear all, 30-day retention, what deleting does **not** do (a waiting approval keeps waiting). (2) Every kind with its default Notify / pop-up switches, the three always-on Needs-you kinds and why. (3) When pop-ups appear (durations, three at a time, "+N more", one action) and when they **never** do: results of your own actions, browser-only conditions (shown as banners or page notices instead), Needs-you in the chat you are looking at. (4) Who receives what with several accounts (every account for workspace objects and system-wide events, per OQ-2's answer). (5) Provider incidents: one entry per provider, re-opening within 10 minutes, "Connections to AI providers are failing", and that Omnipus never claims the internet is down. (6) Resources: memory and disk entries; the Performance line on hosts where memory can't be measured. (7) Keyboard use of the centre and pop-ups |
| DOC-02 | `docs/using-omnipus-ui.md` | changed | Link to DOC-01. System lines in a chat: the five types, in-progress lines, actions, technical details. Detail levels Normal / Verbose / Debug in plain words, replacing every "Verbose chat" mention. Correct the approval paragraph ("Where an approval came from") that says pending requests appear "one at a time over the app" — approvals are inline only. Provider failures: the provider's own message is shown (masked); remove the "provider response details are live-only" caveat where `provider_says` now persists. The Stop outcome is a chat line. Browser-only notices (new version, out-of-sync chat, live-browser input) as page notices |
| DOC-03 | `docs/settings.md` | changed | New **Notifications** tab (switches, locks, defaults table, "settings could not be read" state, "changed in another tab"); Chat → **Detail level** replacing Verbose chat (line ~119); Performance: the unmeasurable-memory line |
| DOC-04 | `docs/workspaces.md` | changed | Workspace Settings → "Notifications for this workspace": Use global / On / Off per switch; System-wide kinds are global only |
| DOC-05 | `docs/calendar.md` | changed | Which Calendar events notify by default; Run now counts as a task result; the Undo result area after moving an event |
| DOC-06 | `docs/tasks.md` | changed | Which task and plan events notify; your own Board changes never notify; the assignee warning on the task card |
| DOC-07 | `docs/goals.md` | changed | Goal paused is a Needs-you notification, always on (lines ~123/129 mention the bell); goal reached / given up are off by default |
| DOC-08 | `docs/providers-and-models.md` | changed | How provider failures read: network ("can't reach", with the cause, countdown and attempt counter; final line with host and one fix) vs API ("answered with an error"); quota and rate limit; which conditions raise a centre entry and which stay in the chat (model-specific); the backup-model note quoting the first model's error |
| DOC-09 | `docs/troubleshooting.md` | changed | "Provider unreachable vs provider error" decision help, with the cause words (connection refused, address could not be found …) and the matching fix; "Connections to AI providers are failing" (check network, proxy, DNS — the internet may still work); browser↔server loss lines; replace "Verbose chat" mentions (~84, 98, 192) |
| DOC-10 | `docs/operations/observability.md` | changed | Log lines "provider attempt failed" (fields, one per dispatched attempt), "provider incident started/ended", "client connection outage" (observed vs longest possible duration), raised "connection closed unexpectedly" — level `warn`; how to grep network vs API; replace the "Verbose chat" mention (~26) |
| DOC-11 | `docs/operations/debug.md` | changed | The chat's **Debug** detail level (ids, request ids, full masked failure details; **not** raw successful provider traffic — tracked issue) vs the gateway's `--debug` start mode; neither shows unmasked secrets |
| DOC-12 | `docs/security.md` | changed | What is masked in messages, details, the centre and logs (keys, partial echoes with a known prefix, bearer tokens, long opaque values) and the stated limit (short fragments without a known prefix, per OQ-1's answer); notifications deleted for real after 30 days or on delete |
| DOC-13 | `docs/agents.md` | changed | Replace the "Verbose chat" mention (~229) with Detail level; agents report in their own chats, notifications are a user surface |
| DOC-14 | `docs/protocol/websocket-protocol.md` | changed | The three notification frames and their fields (`change`, `popup`, `revision`, `persisted`, `target`, `category`, `causes`), `system_line` frame with `history_origin`, `provider_retry` new fields, `LLMError.provider_says` / `failure_class` / `facts.host`, `provider_error` code |
| DOC-15 | `docs/README.md` and `docs/operations/README.md` | changed | Index entries for DOC-01 and the updated DOC-11 |

No user-visible effect, so no page: the router refactor, file locking, the recipient file-name hash, the wording manifest, the voice guard.

---

## 26. Integration boundaries

### AI providers (via the existing provider layer)
- **Data in/out**: requests out; responses, HTTP status, error bodies, stream error events in.
- **Contract**: unchanged; this feature only reads failures.
- **On failure**: classified network/API (§14.1); retries and fallback unchanged (FR-063).
- **Development**: a local fake OpenAI-compatible server for tests (refuse, 401 echo, 402, 429, 503, mid-stream drop, stall); real OpenRouter only in UAT.

### Browser (SPA) ↔ gateway
- **Data in/out**: REST (notifications, preferences, outage report), WS frames (`notification`, `notification_deleted`, `notification_cleared`, `system_line`, `provider_retry`, `provider_fallback`, `error`).
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
> **Status:** Accepted by founder decision D36 (2026-10-22), refined by D64 and D71 (2026-10-31); takes effect with `docs/internal/specs/notifications-spec.md`. Supersedes RD5's sentence "Raw provider errors never persist and never cross to the SPA in production" **for one bounded field only**, RD7's display rule, and the operator decision Q2 of 2026-07-21 for that field. Also supersedes founder decisions D1 and D5 of `docs/internal/specs/provider-messages-spec.md` for the same field (D5's accepted residual fragments are replaced by the masking contract below). RD1–RD4, RD6 and the two-choke-point architecture are unchanged.
>
> **Context.** The founder's message review (2026-10-22) found that hiding what the provider said makes failures harder to act on: a provider's own words ("Insufficient balance", "Incorrect API key provided") are often the most useful part of the message (D26). ADR-051's fixed sentences were written to keep raw provider text off the wire after an incident where a full raw JSON body reached the chat. The incident's harm was the *raw body*, not the provider's *message*. (Correction of an earlier summary: the live `detail` field already carries a bounded, masked preview of the provider's text to the browser; it is not persisted.)
>
> **Decision.**
> 1. **`provider_says`.** The error shape, the replayed error and the fallback note gain one optional field: the provider's own error message, *extracted* from the response (a fixed list of JSON message locations, else a text body's first line, else a stream error event's message) — never the raw body. It is masked, normalised, masked again and capped at 2,000 bytes on a character boundary, **persisted** in the transcript at the write choke point (`appendErrorTranscript`) and **replayed**, so a reloaded chat reads the same as the live one.
> 2. **Masking contract.** One masker applies before persistence and the wire, to every surface: registered credential values (also when split by whitespace), authentication headers and key-named fields, key-shaped tokens and JWTs, partial echoes anchored on a known key prefix, and long opaque letter-and-digit runs. Text the masker cannot process is dropped. The one stated residual is a short fragment with no known prefix (founder question OQ-1 of the notifications spec).
> 3. **Main line.** The user-facing sentence quotes `provider_says` on the main line at every detail level, next to a short sentence of ours (what happened · why · what to do). When no provider message exists, the sentence omits the quote; it never shows empty quotes and never invents provider text. The server assembles the finished sentence from the facts at the emit site; the browser never composes wording.
> 4. **Catalogue.** ADR-051's fixed sentences are replaced by the founder-agreed wording baseline (`docs/internal/specs/notifications-and-chat-system-messages-wording-2026-10-22.md` §1), written and checked with the `omnipus-voice` skill. A qualified remedy ("switch to a model with a larger limit") stays allowed; unqualified model-shopping in a product or configuration message does not. Each catalogue entry carries explicit variants: no facts; provider and model known; provider message known; and for connection failures retrying and final. The single-source catalogue and both generators remain the only place copy lives.
> 5. **Network vs API.** The `network` code is split: `network` now means the provider could not be reached at all (DNS, refused, unreachable, TLS, timeout, a connection dropped before or during the answer); the new code `provider_error` means the provider answered with a provider- or account-level error (401/403 at account level, 408, 5xx, 529). Model-specific conditions keep their own codes. The error shape gains `failure_class` (`network` · `api`) and the fact `host`. Retry and fallback decisions do not read either. Connection lines follow the N2 pattern: cause named as soon as observed, countdown and attempt counter on every retry line, final line naming host, cause and one fix; no message claims the internet is down.
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
| FQ-17 — Debug L11 | N4 = A: Debug shows what exists; raw successful exchange deferred to a tracked issue with a target date (issue text with team-lead to file) | D66 | §12.2, S-64 |
| Q2 — delete while waiting | A: deleting or clearing never changes the waiting request; 30-day rule applies | D80 | §8.3, §8.5, S-23, S-24, S-26, S-28 |

### 30.2 Still open — new at round 1

**OQ-1 — Short key fragments without a known prefix** (answer "OQ-1 A" or "OQ-1 B")

Context and impact: the new masking contract (§15.1) catches full keys, partial echoes that start with a known key prefix (`sk-…xyz`), bearer tokens and long opaque values. It cannot recognise a short fragment that has no prefix, such as `…a1b2c3`, without also masking ordinary words and numbers. R-SEC-1 asks for partial key echoes to be masked; this is the one case the design cannot promise.

| Option | Decision |
|---|---|
| **A — Recommended** | Accept this residual and document it (security page DOC-12); the dataset keeps a row that makes any change visible |
| B | Hide every provider message that contains any 6+ character letter-and-digit run, showing only our sentence — safer, but many useful provider messages (model names, ids) disappear from the main line |

**OQ-2 — Who receives notifications for workspace objects and owner-less sessions** (answer "OQ-2 A" or "OQ-2 B")

Context and impact: a task's or plan's "owner" field can hold an agent's name as well as a person's, so it cannot safely pick a recipient (§8.1). Omnipus also has no per-account access control for workspaces today (#1421: every account can see every workspace). With several accounts, someone has to receive task, plan and Calendar notifications, and approvals in sessions that no signed-in person started (for example a heartbeat).

| Option | Decision |
|---|---|
| **A — Recommended** | Every configured account receives them, until a per-account access model exists (#1421); sessions started by a signed-in person still go only to that person |
| B | Add a new person-only "notify" field to tasks, plans and schedules, filled from the signed-in user who created them, and notify only that person (needs a contract change and a rule for objects created by agents) |

Not founder questions (for team-lead and the chief): the missing notifications Epic (§1.5), confirmation of the riders (#645 items, #1513, the Windows lock residual, AF-4), and filing the N4 issue.

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
| A8 | Recipients with several accounts | First account, or the task's creator field | §8.1 R1–R3; OQ-2 |
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

---

## 33. Round-1 finding dispositions

| Finding | Disposition | Where |
|---|---|---|
| MAJ-001 Lifecycle frames incompatible | Fixed: three strict frame types, no union; `popup` only on `created`; idempotent clear | §7.2, DS-FRAME, S-103, T-48 |
| MAJ-002 System-line history contract | Fixed: content vs transport split; all four history stages named | §7.5, S-55, S-106, T-28 |
| MAJ-003 Merge-on-refresh | Fixed: store revision, tombstones, snapshot rules, refetch on re-auth | §8.4, DS-RECON, S-100…S-102, T-40 |
| MAJ-004 Calendar producer | Fixed: source map with the Calendar trigger path and run-close classification | §8.1, S-01, S-09, S-18, T-37, T-24 |
| MAJ-005 Owner not a safe recipient | Fixed by rules R1–R3 (attribution never used); recipients for workspace objects put to the founder | §8.1, S-15, S-16, T-39; **OQ-2** |
| MAJ-006 Masking contract | Fixed: M1–M6, order of operations, failure drops text, DS-SEC rewritten; residual put to the founder | §15.1, §13.3, DS-SEC, S-90…S-94; **OQ-1** |
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
