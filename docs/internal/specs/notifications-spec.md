# Feature Specification: Notifications and chat system messages

**Created**: 2026-10-30
**Status**: Draft — awaiting grill round 1 (founder questions FQ-1…FQ-17 open; each is written into the spec with its recommended option marked **pending founder**)
**Author**: architect (spec author), for team-lead (Mac squad)
**Branch / base**: `landing/notifications`, created from `release/v0.1.1` at `4cad412bf`; requirements merged at `e21febf4f`
**Change size**: feature (structural, cross-tree: `contracts/`, `pkg/notifications`, `pkg/gateway`, `pkg/agent`, `pkg/providers`, `src/`). Flow: two `grill-spec` rounds → RED/GREEN/CHECK → one 5-reviewer gate → candidate CI → hands-on UAT on the exact commit (touches `pkg/agent` and gateway WebSocket handlers) → founder's yes.
**Input (founder-confirmed)**:
- Requirements, stable `R-…` ids, Decisions Log D1–D62, build order: `docs/internal/specs/notifications-and-chat-system-messages-requirements-2026-10-22.md` (cited below as **REQ**)
- Agreed wording baseline: `docs/internal/specs/notifications-and-chat-system-messages-wording-2026-10-22.md` (cited as **WORDING**)
- Founder review data (read only): `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/review.json`, `messages-review.json`, `revised/revised.json`, `toast-audit/result-w1..w4.jsonl`
- Issues: #645 (superseded open question; its hardening list is folded in here), #177 (mobile notification preferences — compatibility only)
- ADR changed by decision D36: **ADR-051 — Provider-Capability-Aware Media Handling and User-Facing Error Translation** (`docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md`; not the two other ADR-051 files). The proposed amendment text is §28; ADR-051 itself is not edited by this spec.
- Prior approved spec this one partly supersedes: `docs/internal/specs/provider-messages-spec.md` (#711, Status Approved) — its founder decision D1 ("raw provider text is sent but displayed only under Verbose chat") is replaced by REQ D28 for the provider's own message (see §3.2 row C9).

---

## 0. Reading guide and bottom line

**Bottom line.** Omnipus gets one model for telling the user about events: a fixed **event catalogue** (§6) whose entries go to the **notification centre**, optionally a **pop-up**, and a **line in the affected chat**; one **system-line** renderer for every system message inside a chat; one **detail-level** setting (Normal / Verbose / Debug) replacing the Verbose-chat switch; honest wording that shows the provider's own (masked) message; and connection logging that tells **network failures** (provider not reachable) apart from **API failures** (provider answered with an error). The build scope is **Part 1 (in-app)**. **Part 2 (external delivery, REQ §5.10)** is specified here as the second step (§29) and is not built now (D62).

| Section | What it holds |
|---|---|
| §1 | Scope, build order, work packages, size verdict |
| §2 | Existing codebase context (GitNexus status, symbols, impact) |
| §3 | Re-verified current state at `4cad412bf`, with every difference from the requirements |
| §4–§5 | User stories, behavioural contract, edge cases, non-behaviours (behavioural language only) |
| §6 | Event catalogue (kinds, defaults, producers, owners, resolve triggers) |
| §7 | Wire contract changes — schemas named, shapes described, not written |
| §8–§16 | Design per area: centre, preferences, pop-ups, system lines, detail levels, wording, connections and logging, security, removals |
| §17 | Reachability — which screen or agent path reaches each part |
| §18–§20 | BDD scenarios, TDD plan, test datasets, regression |
| §21–§23 | Functional requirements, success criteria, traceability R-… → FR → scenario → test |
| §24–§25 | Hands-on UAT rows (macOS), user-facing documentation TODOs |
| §26–§27 | Integration boundaries, holdout evaluation |
| §28 | ADR-051 amendment — exact proposed text |
| §29 | Part 2 — external delivery (second step) |
| §30 | Founder questions FQ-1…FQ-17 |
| §31 | Ambiguity warnings, assumptions, accidental findings |

Phases §4–§5 follow plan-spec's no-implementation-detail rule. Every later section (§6 onward) is design detail and cites `file::symbol`.

---

## 1. Scope, build order and size

### 1.1 In scope (Part 1 — the build)

| Area | Requirements |
|---|---|
| Notification centre: categories, owner, resolve-everywhere, 30-day retention, delete and clear, navigation | R-NC-1…6, R-SEC-2 |
| Event catalogue with a real producer per kind; network vs API as separate kinds | R-EV-1…4 |
| Preferences per kind (Notify me, Also pop up), global + per-workspace override | R-PREF-1…4 |
| Pop-up rules (duration, cap, one action, no own-action pop-ups, no event modals) | R-PU-1…6 |
| One in-chat system-line mechanism, five types, in-progress state, connection lines | R-SL-1…7 |
| Detail levels Normal / Verbose / Debug replacing the Verbose-chat switch | R-DL-1…3 |
| Wording rules and the agreed baseline, voice rule | R-WD-1…9 |
| Connection logging at the default level; network vs API; browser outage report | R-LOG-1…4 |
| Secret masking on every surface | R-SEC-1 |
| Removals: own-action pop-ups (audited sites), event-raised modals, top-right badges | R-RM-1, R-RM-1a, R-RM-2, R-RM-3 |
| #645 hardening folded in: one shared producer path that always mints an ID, merge-not-replace hydrate, delete API, per-type eviction protection | REQ F4; #645 |

### 1.2 Second step (Part 2 — described, not built now)

External delivery through notification channels, R-EXT-1…12, R-SEC-3, R-SEC-4 (D62). §29 describes it to the depth needed to keep Part 1's contracts forward-compatible: the reserved third preference switch, the `notification_delivery_failed` kind, and the replacement of the dead scheduled-task channel alert (R-EXT-12). Part 2 gets its own grill before its build.

### 1.3 Size verdict

Part 1 plus Part 2 in one **build** would not be reviewable; Part 1 plus a **described** Part 2 is, provided the pop-up triage (R-RM-1: 328 call sites, §16) runs as its own work package. Covering Part 2 at design depth here costs about 10 % of the document and protects Part 1's contracts, so this spec does **not** split Part 2 out. Whether the pop-up triage stays in this spec's single gate is **FQ-1** (recommended: same spec, own work package and owner, allowed to ride a later train — pending founder).

### 1.4 Work packages and order (D62, REQ dependency graph)

```
 WP-0 contracts (backend-lead) ─────────────────────────────────────────────┐
   │                                                                         │
   ├─> WP-1 backend: notification router, kinds, prefs, retention,           │
   │        resolve-everywhere, delete (pkg/notifications, pkg/gateway)      │
   ├─> WP-2 backend: network-vs-API split, provider episodes, logging,       │
   │        provider message on main line, browser outage endpoint           │
   │        (pkg/providers, pkg/agent, pkg/gateway)                          │
   ├─> WP-3 frontend: system-line renderer, detail levels (src/components/   │
   │        chat, src/store, src/lib)                                         │
   │                                                                         │
   ├─(after WP-1)─> WP-4 frontend: centre filters/delete, preferences UI,    │
   │                     pop-up host rules (src/components/layout,           │
   │                     src/components/settings, src/store/ui.ts)           │
   ├─(any time after FQ-1/FQ-14/FQ-15 answers)─> WP-5 frontend: own-action  │
   │                     pop-up triage, 328 sites (R-RM-1/1a)                │
   ├─(parallel)─> WP-6 wording: catalogue + baseline + remaining rewrites    │
   │              (LLMError.yaml x-user-messages, Go/TS literals) — FQ-13    │
   └─> WP-7 docs (implementing leads draft, docs-verifier audits)  ──────────┘
                                 v
               one 5-reviewer gate on the combined branch, candidate CI,
               hands-on UAT (§24) on the exact commit, founder's yes
```

Ownership follows root `CLAUDE.md` "Feature ownership": one backend implementer owns WP-1 + WP-2 (overlapping `pkg/gateway` files — never split between two writers), one frontend implementer owns WP-3 + WP-4; WP-5 is a separate frontend owner only if FQ-1 = A, working in files WP-4 does not touch, otherwise it joins WP-4's owner. One test author (qa-lead RED) for the whole feature; CHECK is a different instance.

---

## 2. Existing codebase context

**GitNexus status.** The GitNexus MCP tools are not connected in this session, and `gitnexus list` shows no index for this checkout (only `omnipus-websearch/integ` at `c298c3b` and `omnipus-wt/crash-recovery` at `e0ab58a`). Per `omnipus-shared-rules` rule 9 the exploration below is Read/Grep at `4cad412bf`, and every impact row is labelled **Inferred**. Re-indexing this worktree was not done (heavy, not needed for a text deliverable).

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

HIGH rows: the change to `TranslateLLMError`/`WireLLMError`/`ClassifyError` is flagged to team-lead before GREEN; the design keeps retry and failover *decisions* byte-identical and only adds labels, masked text and log lines (FR-063).

### 2.3 Relevant flows and cluster placement

| Flow | Relevance |
|---|---|
| Turn → provider call → fallback chain → classify → retry/fallback/terminal error → transcript + live frame | Network vs API label, provider message on the main line, per-attempt logs, provider episodes |
| Scheduled run → `scheduledRunner` → success/failure | `schedule_finished` (new) and `schedule_failed` (existing) |
| Tool approval park → broadcast → decision from any tab → `tool_approval_resolved` | Needs-you notification and resolve-everywhere |
| Ask-user park → broadcast → answered/cancelled | Needs-you notification and resolve-everywhere |
| Browser WebSocket lifecycle → reconnect phases → catch-up | System line + app-wide indicator + outage report |

The feature spans the **notifications/gateway** cluster, the **agent turn loop / provider** cluster and the **SPA chat + layout + settings** clusters. Cross-cluster coupling is confined to: the contract (§7), one producer path in `pkg/notifications` (§8.1), and the provider-episode tracker (§14.3).

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
| C3 | Notification types `schedule_failed`, `knowledge_drift` only | **Four**: also `goal_paused` and `stop_notice` | **changed** → FQ-2, FQ-3 | `contracts/components/schemas/Notification.yaml` (`type` enum); `pkg/notifications/store.go` constants |
| C4 | Two producers | **Four**: + `pkg/gateway/goal_pause_notify.go::goalPausedNotifier` (commit `f24785784`), + `pkg/gateway/stop_notice.go::WSHandler.publishStopNotice` (commit `94408e56b`) | **changed** | files read |
| C5 | Retention newest 50 per user, no age limit | Same | same | `pkg/notifications/store.go::notificationCap`, `retain` |
| C6 | Stored per recipient, one file each | Same | same | `pkg/notifications/store.go::Store.userFile` |
| C7 | Sidebar attention cue | Same | same | `src/components/layout/sidebar/attentionCue.ts::attentionCountLabel` |
| C8 | Browser offline / server unreachable copy | Same text | same | `src/components/chat/ConnectionStatus.tsx::ChatConnectionStatusLine` |
| C9 | Browser↔server unexpected close logged at debug only | Same | same | `pkg/gateway/websocket.go` (`slog.Debug("ws: connection closed unexpectedly", …)`) |
| C10 | No client-side event reporting endpoint | Same | same | no matching path in `contracts/openapi.yaml` |
| C11 | `FailoverReason` set; transport failures matched as transient | Same set; **`bad_model` is now retriable** (falls back to the next candidate, founder Q70, 2026-10-24) and `ProviderFallbackFrame.unavailable_code` gained `model_not_found` | **changed** (no question — affects only the `bad_model` wording context) | `pkg/providers/types.go::FailoverError.IsRetriable`; `contracts/components/schemas/ProviderFallbackFrame.yaml` |
| C12 | No reason value separates network from API | Confirmed (was Inferred) | same | `pkg/providers/types.go::FailoverReason` has no such value; `pkg/providers/error_classifier.go::connectionDropPatterns` mixes never-reached (`no such host`, `connection refused`, `tls handshake`, `dial tcp`) with mid-stream drops (`connection reset by peer`, `unexpected eof`, `http2: server sent goaway`) → FQ-7 |
| C13 | Provider retry logging partial | Confirmed; and the **default log level is `warn`**, so any Info line is invisible on a default install | same + **new** | `pkg/agent/loop_run_turn_response.go` (Warn on timeout retry and stream reset; Error `"LLM call failed"` only after retries); `pkg/providers/fallback.go::FallbackChain.Execute` (no per-attempt log); `pkg/config/defaults.go` (`LogLevel: "warn"`) |
| C14 | ADR-051: raw provider text never reaches the wire; 29 sentences, 7 with a provider template | Same (`provider_auth_failed`, `rate_limited`, `quota_billing`, `model_retired`, `bad_model`, `provider_request_rejected`, `model_route_unavailable` carry `provider_message`) | same | `contracts/components/schemas/LLMError.yaml` (`x-user-messages`); `pkg/agent/translate_error.go` header |
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
| N3 | Founder ruling **2026-10-18**: *no "waiting in another chat" notice*; the sidebar attention cue is the signal elsewhere. D30 (2026-10-22) makes Needs-you pop up always | Later decision wins, but the conflict is surfaced → FQ-5 | `src/components/agents/ToolApprovalPanel.tsx` header comment |
| N4 | `system_overload` frame has a consumer (pop-up) but **no producer** | Not used for `concurrency_reduced`; its consumer pop-up is removed with WP-5 (dead path) | `pkg/gateway/websocket_forward.go` comment block; grep |
| N5 | Memory pressure already lowers concurrency (to 2) — also **permanently on hosts where memory cannot be measured** (Windows, some containers) | Producer for `concurrency_reduced` exists in logic but emits nothing → FQ-10 | `pkg/agent/admission.go::memoryAdmissionCap`, `applyMemoryCap` |
| N6 | No disk-space monitor and no internet-loss detector exist | Producers must be built → FQ-8, FQ-9 | grep `Statfs`/free-space/connectivity: none outside unrelated code |
| N7 | Schedule-failure notification body is the raw run error (`runErr.Error()`), unmasked and untranslated; on the no-recipient and store-failure paths **no ID is minted**, so the frame is rejected by the SPA (`id` minLength 1) — #645 Defect 1 still present | Fixed by the one producer path (§8.1, FR-005) | `pkg/gateway/schedules.go::scheduledRunner.onFailure`, `pushNotification` |
| N8 | `hydrate` replaces the whole list; `AppShell` refetches with `staleTime: 60_000` and default focus refetch — #645 Defect 2 still present | Merge by id (FR-010) | `src/store/notifications.ts::hydrate`; `src/components/layout/AppShell.tsx` |
| N9 | The only unread badge is inside the sidebar (account menu) — #645 Defect 3 | Pop-ups (R-PU) become the always-visible signal; badge placement unchanged (D33 keeps the sidebar as is) | `src/components/layout/Sidebar.tsx` (`sidebar-notification-badge`) |
| N10 | Marking read is not pushed to the user's other tabs | Resolve-everywhere needs a push (FR-007) | `pkg/gateway/schedules.go` REST switch (`MarkRead` then 204, no emit) |
| N11 | A timeout retry posts the text "Retrying — please wait..." as an outbound progress notice; `EventKindLLMRetry` is forwarded only as a context-window notice | Network retries get a structured line (FR-040) | `pkg/agent/loop_run_turn_response.go`; `pkg/gateway/websocket_forward_hub.go` |
| N12 | The provider-messages spec (Approved, #711) decided raw provider text is sent but **displayed only under Verbose**, and `LLMError.detail` is **live-only, never persisted** | REQ D28 replaces this for the provider's own message: a masked, bounded *provider message* is persisted and shown on the main line; the raw `detail` stays Verbose-only and live-only. Recorded in the ADR-051 amendment (§28) | `docs/internal/specs/provider-messages-spec.md` (D1); `contracts/components/schemas/LLMError.yaml` (`detail` description) |
| N13 | Stop outcomes (`stop_notice`) are results of the user's own Stop action, yet are centre notifications | Conflicts with D31 (own-action results never in the centre) → FQ-3 | `pkg/gateway/stop_notice.go`; `pkg/gateway/websocket_cancel.go` (4 call sites), `approval_stop.go` (1) |
| N14 | The 2026-10-24 bad-model fallback means a wrong model with a backup configured now produces a fallback note, not an error | Wording only; no question | C11 |

---

## 4. User stories and acceptance criteria

> Behavioural language only (plan-spec Phases 1–2.5). Requirement ids in brackets.

### US-1 — See only the events I opted into, in one place (P0)

A person running agents wants one notification centre that collects the events they chose — an agent waiting for them, a task that failed overnight, a provider that is down — without it becoming a log. Today only four event types reach the centre and two of them are by-products; most of what an agent or the system does is invisible unless the right chat is open. [R-NC-1, R-NC-2, R-NC-5, R-NC-6, R-EV-1, R-EV-4]

**Why this priority**: the centre is the anchor every other surface (pop-up, preferences, external delivery) hangs from.
**Independent test**: fire each catalogue event against a running instance and check the centre lists exactly the defaults-on kinds, filterable by category, each with a working "open" target.

1. **Given** fresh-install defaults, **When** a scheduled task fails, **Then** the centre shows one entry under **Tasks & plans** naming the task, and opening it shows the run.
2. **Given** fresh-install defaults, **When** a non-scheduled task starts, **Then** no centre entry appears.
3. **Given** entries in all three categories, **When** the user filters by **Needs you**, **Then** only Needs-you entries remain visible.
4. **Given** a provider becomes unreachable while three chats are replying, **When** the condition is detected, **Then** the centre shows one entry for the provider (not three), and each of the three chats shows its own line.
5. **Given** any centre entry, **When** the user activates it, **Then** the app navigates to the screen where the item can be handled.

### US-2 — Handle it once, cleared everywhere (P0)

A person who answers an agent's question in the chat does not want the same item still lit up in the centre, in a pop-up, or in another open tab. [R-NC-3]

**Why this priority**: stale badges teach people to ignore notifications.
**Independent test**: two tabs open; resolve in one; observe the other.

1. **Given** an approval request shown as pop-up and centre entry in two tabs, **When** the user approves it in the chat in tab A, **Then** within 2 s the pop-up disappears in both tabs and the centre entry shows as handled in both.
2. **Given** an unread entry in two tabs, **When** the user opens it from tab A, **Then** tab B shows it read and the badge count drops.
3. **Given** an approval that times out on the server, **When** it expires, **Then** its entry is handled everywhere without user action.

### US-3 — Old notifications go away, and I can delete them (P1)

[R-NC-4, R-SEC-2]

1. **Given** an entry older than 30 days, **When** the retention sweep runs, **Then** the entry is gone from the centre and from storage.
2. **Given** 60 entries including one unhandled Needs-you entry older than all others, **When** a new entry arrives, **Then** the Needs-you entry is still there.
3. **Given** a centre entry, **When** the user deletes it, **Then** it disappears from every open tab and from storage.
4. **Given** several entries, **When** the user chooses Clear all and confirms, **Then** the centre is empty in every open tab, except unhandled Needs-you entries, which stay.

### US-4 — Choose per event kind what notifies me (P1)

[R-PREF-1…4]

1. **Given** the preferences screen, **When** it opens, **Then** every catalogue kind is listed under its category with **Notify me** and **Also pop up** switches set to the fresh-install defaults.
2. **Given** **Task or plan failed** set to Notify off globally, **When** a task fails, **Then** no centre entry and no pop-up appear, and the task's own chat still shows the failure.
3. **Given** a workspace override turning **Task or plan started** on for workspace W, **When** a task starts in W, **Then** an entry appears; **When** a task starts in workspace V, **Then** none appears.
4. **Given** the preferences screen, **When** the user looks at a System-wide kind inside a workspace override, **Then** it is not offered (global only).
5. **Given** the two Needs-you kinds, **When** the user tries to switch their pop-up off, **Then** the switch is locked on with an explanation.

### US-5 — Pop-ups that interrupt only when worth it (P0)

[R-PU-1…6]

1. **Given** an info event with pop-up on, **When** it arrives, **Then** a pop-up shows bottom-right and disappears after 5 s unless the pointer hovers on it.
2. **Given** a warning event, **When** it arrives, **Then** the pop-up stays 10 s.
3. **Given** an error or Needs-you event, **When** it arrives, **Then** the pop-up stays until dismissed or the item is handled.
4. **Given** three pop-ups visible, **When** a fourth event arrives, **Then** three stay visible and a line "+1 more — open notifications" appears and opens the centre.
5. **Given** any pop-up, **When** it shows, **Then** it offers at most one main action that navigates to where the item is handled, plus close.
6. **Given** the user saves a setting, **When** the save succeeds or fails, **Then** no pop-up appears and nothing enters the centre; the page shows the result.
7. **Given** any event, **When** it arrives, **Then** no full-screen modal opens by itself.

### US-6 — One consistent line for system messages in a chat (P0)

[R-SL-1…7]

1. **Given** a reply that fails because the provider rejected the key, **When** the failure is shown, **Then** it appears as one Error line with icon, colour and weight of the Error type, our sentence and the provider's own message quoted.
2. **Given** a provider that cannot be reached while a reply is waiting, **When** the system retries, **Then** an animated amber Warning line says it cannot reach the provider, why, and when it retries next.
3. **Given** that retry later succeeds, **When** the reply continues, **Then** at Normal the line is gone; at Verbose and Debug one line remains recording the connection problem.
4. **Given** the browser loses its connection to the server, **When** the loss lasts, **Then** an app-wide indicator and one line in the open chat show it; after reconnecting neither appears in the chat history.
5. **Given** a line that offers an action (e.g. Switch model), **When** the user activates it, **Then** the action happens or the right screen opens.

### US-7 — Pick how much detail the chat shows (P1)

[R-DL-1…3]

1. **Given** Settings → Chat, **When** it opens, **Then** a three-way **Detail level** (Normal, Verbose, Debug) replaces the Verbose-chat on/off switch.
2. **Given** each level, **When** the same chat with tool calls, a delegation, a retried provider failure and a final failure is shown, **Then** each row type shows or hides exactly as the normative table in §12.
3. **Given** any user, **When** they select Debug, **Then** it is allowed and secrets are still masked.

### US-8 — Messages that say what happened, why, and what to do (P1)

[R-WD-1…9]

1. **Given** a provider balance error, **When** the failure is shown, **Then** the main line names the provider, quotes its message ("Insufficient balance") and says what the user can do.
2. **Given** a system line inside a chat, **When** the product would have been the actor, **Then** the line says "I" (agent's perspective) and never "Omnipus", "we", "our" or "us".
3. **Given** the same event outside a chat (centre, pop-up), **When** it is shown, **Then** the wording is neutral.

### US-9 — Connection problems are diagnosable afterwards (P1)

[R-LOG-1…4, R-EV-2]

1. **Given** a provider that cannot be reached, **When** an attempt fails, **Then** the log at the default level records a network failure naming provider, model and reason.
2. **Given** a provider that answers 503, **When** an attempt fails, **Then** the log records an API failure with the status and the provider's (masked) message.
3. **Given** the browser lost the server for 40 s, **When** it reconnects, **Then** the server log records the outage with start, end and duration.
4. **Given** a connection line that left the chat at Normal, **When** an operator reads the log, **Then** the same episode is there.

### US-10 — Secrets never leak through a message (P0)

[R-SEC-1]

1. **Given** a provider message that echoes part of an API key, **When** it is shown on the main line, in technical details, in the centre or in the log, **Then** the key fragment is replaced by a mask on every surface and every level.

### US-11 — Fewer, honest pop-ups for my own actions (P1)

[R-RM-1, R-RM-1a, R-RM-2, R-RM-3]

1. **Given** an action whose page already shows the outcome, **When** the action completes, **Then** no pop-up appears.
2. **Given** a failed save whose page showed nothing before, **When** the save fails, **Then** the page shows a "not saved" indicator with Retry.
3. **Given** a moved calendar event, **When** the move completes, **Then** Undo is available on the page (not only in a disappearing pop-up).
4. **Given** any tool approval, **When** it arrives, **Then** no app-wide modal opens; the request is handled in its chat.

---

## 5. Behavioural contract, edge cases and non-behaviours

### 5.1 Behavioural contract

Primary flows:
- When an event of an opted-in kind occurs, the system creates one centre entry for the event's owner and, if the kind's pop-up preference is on, shows one pop-up in every open tab of that owner.
- When a recurring condition (a provider outage, low memory) is still unresolved, the system updates the existing entry instead of creating a new one, and raises no second pop-up.
- When an item is handled anywhere, the system marks it handled in storage and on every open tab within 2 s.
- When a reply is affected by a provider problem, the system shows one system line in that chat; idle chats get none.
- When a provider failure ends a reply, the main line shows our sentence plus the provider's own message, masked.
- When the detail level changes, every chat component re-renders by the one table in §12, without reloading.
- When a connection problem starts, ends or repeats, the system writes a log line at the default level saying network or API.

Error flows:
- When the notification store cannot be written, the system still delivers the live entry and pop-up to open tabs and logs an error.
- When the browser cannot reach the server, the system shows the app-wide indicator and a chat line, keeps retrying, and reports the outage when back.
- When the preferences file is unreadable, the system uses the fresh-install defaults, keeps working and shows a "Preferences could not be read" indicator on the preferences screen.
- When a provider message cannot be extracted from a response, the main line shows our sentence alone (never a blank quote).

Boundary conditions:
- When more than three pop-ups are due, the system shows three and a "+N more — open notifications" line.
- When an entry is exactly 30 days old, the next retention sweep deletes it.
- When a provider message is longer than 300 characters, the main line shows the first 300 and an ellipsis; the full masked text is in technical details.

### 5.2 Edge cases

| Situation | Expected behaviour |
|---|---|
| Two producers report the same provider outage within one second | One centre entry, one pop-up |
| Provider recovers and fails again within 60 s | The earlier entry re-opens; no second pop-up |
| A Needs-you item is handled while the user's browser is offline | On reconnect the entry shows handled and no pop-up appears |
| User deletes an unhandled Needs-you entry | Refused with "This request is still waiting for you — handle it first" (it cannot be deleted while waiting) |
| Owner of a session cannot be read | No notification is broadcast to every account; the failure is logged (keeps today's goal-pause rule) |
| No user account exists (first start before onboarding) | Live pop-up only, with a minted id; nothing persisted; log warning |
| Detail level changed while a retry line is in progress | The in-progress line stays at every level (R-SL-5) |
| Page reloaded during a browser↔server outage | The outage is still reported once after reconnect (start kept across the reload in the tab) |
| Provider message contains HTML or Markdown | Shown as plain text |
| Provider message is not English | Shown as received (masked), our sentence stays English |
| A workspace with an override is deleted | Its override is ignored and removed on the next preferences save |
| Debug selected on a shared screen | Secrets are still masked; no raw credential value is ever rendered |

### 5.3 Non-behaviours and safeguards

Qualitative prohibitions:
- The system must not log or store raw credential values in any message, log line or entry, because providers echo partial keys.
- The system must not create a centre entry or pop-up for the result of the user's own action, because the screen where they acted shows it (D31).
- The system must not open a full-screen modal because of an event (D17, D37).
- The system must not put a system line into chats that were not affected (D14).
- The system must not hide the provider's own error message behind a generic sentence (D28), and must not invent one when none was received.
- The system must not send anything outside the app in Part 1 (D62); the existing scheduled-task channel alert stays untouched until Part 2 replaces it (R-EXT-12).
- The system must not change *whether* a provider call is retried or falls back; only labels, wording and logs change.
- The system must not introduce raw cron text, a Command Center screen, a JPEG screencast, a default agent or any other retired surface (root `CLAUDE.md`).
- The system must not use "Omnipus", "we", "our" or "us" as the actor in any system message (except the one bug-report line, D48).
- The system must not add new badges in the top-right corner (D34).
- An implementing agent must not "helpfully" add a notification sound, a browser (OS) notification, an email, or a mobile push in Part 1.

Machine-verifiable constraints:

| Category | Constraint |
|---|---|
| REST errors | Deleting an unhandled Needs-you entry → HTTP 409 with `ErrorResponse.code = "notification_waiting"`. Deleting an unknown id → 404. Preferences update with an unknown kind → 400, `code` `"unknown_notification_kind"`; locked switch set off → 400, `code` `"notification_switch_locked"`; workspace override containing a System-wide kind → 400, `code` `"system_wide_kind_global_only"`. Outage report with `ended_at` before `started_at`, duration > 7 days, or `ended_at` more than 5 min in the future → 400, `code` `"invalid_outage_report"`; more than 10 reports per minute per user → 429 |
| Timing | A handled item shows handled in another open tab ≤ 2 s after the handling request returns (local instance) |
| Retention | An entry with `created_at` ≥ 30 × 24 h old is absent after the next sweep; the sweep runs at start-up and every 6 h |
| Size | Provider message on the main line ≤ 300 code points (+ "…"); stored provider message ≤ 2,000 bytes; outage report body ≤ 4 KiB |
| Pop-ups | ≤ 3 visible; info/success 5 s, warning 10 s, error and Needs-you until dismissed or handled; hover pauses the countdown |
| Logging | Every failed provider attempt and every browser outage report produces exactly one line at level WARN or higher with a `failure_class` field ∈ {`network`, `api`} (provider) or `client_outage_kind` ∈ {`offline`, `server_unreachable`} (browser) |
| Voice | A guard fails CI if any catalogue sentence shown in a chat contains `Omnipus` as a word (except the D48 sentence) or the words `we`, `our`, `us` (§13.4) |

---

## 6. Event catalogue (R-EV-1…4, R-PREF-4, D15, D40)

> From here on: design detail (plan-spec Phase 4+), implementation names allowed.

Kind ids are stable strings — the one vocabulary for the centre, preferences, Part 2 external delivery and a future mobile client (#177, §9.4). Existing ids `schedule_failed` and `knowledge_drift` are kept unchanged.

| Kind id | Category | Event | Notify | Pop-up | Severity | Chat line | Ext. (P2) | Producer (hook point) | Owner | Coalesce key / resolve trigger | Open target |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `approval_requested` | needs_you | Agent needs your approval | On (locked, FQ-4) | Always (locked) | needs_you | unchanged (inline card) | Yes | `WSHandler.broadcastToolApprovalRequired` call site in `pkg/gateway/policy_approver.go` | owner of `entry.SessionID`'s root session (`UnifiedMeta.Owner`) | key `approval:<approval_id>`; resolved by every path that emits `tool_approval_resolved` (decision from any tab, timeout, Stop, agent deletion, shutdown) | chat of the request |
| `question_asked` | needs_you | Agent asked a question and is waiting | On (locked, FQ-4) | Always (locked) | needs_you | unchanged (inline card) | Yes | `WSHandler.broadcastAskUserCard` when the card is `pending` | owner of `card.session_id` | key `ask:<card_id>`; resolved on the card's terminal transition (answered, cancelled, auto-resolved) | chat of the card |
| `task_started` | tasks_plans | Task or plan started | Off | Off | info | its own session | No | `EmitTaskStatusChanged` → `in_progress`; `EmitPlanStatusChanged` → `running` (first time) | task/plan owner | none; info only | task or plan |
| `task_finished` | tasks_plans | Task or plan finished (not scheduled) | Off | Off | success | its own session | No | status → `done` (task) / `done` (plan), not schedule-triggered | task/plan owner | key `task:<id>` | task or plan |
| `task_failed` | tasks_plans | Task or plan failed | On | On | error | its own session | Yes | status → `failed`, not schedule-triggered | task/plan owner | key `task:<id>`; resolved when the same task later reaches `done` | task or plan |
| `schedule_finished` | tasks_plans | Scheduled task finished | On | On | success | its own session | Yes | `scheduledRunner` success path (new hook beside `onFailure`) | `job.CreatedBy` (today's `resolveRecipients` rule) | key `schedule:<job_id>` (one live item per schedule) | the run's session |
| `schedule_failed` | tasks_plans | Scheduled task failed | On | On | error | its own session | Yes | `scheduledRunner.onFailure` (existing) | `job.CreatedBy` | key `schedule:<job_id>`; resolved by the next successful run | the run's session |
| `goal_reached` | tasks_plans | Goal reached | Off | Off | success | its own session | No | goal ending `met` (`EventKindGoalOutcome`) | session owner | key `goal:<goal_id>` | chat |
| `goal_given_up` | tasks_plans | Goal given up | Off | Off | warning | its own session | No | goal ending `rounds_exhausted` or `other` (not `stopped_by_user` — own action) | session owner | key `goal:<goal_id>` | chat |
| `goal_paused` | **needs_you (pending founder, FQ-2)** | Goal paused — waiting for you or blocked | On | On (Always if in Needs you) | warning | its own session (existing) | Yes | `goalPausedNotifier` (existing) | session owner (existing rule) | key `goal_paused:<goal_id>` (existing); resolved when the goal resumes or ends | chat |
| `provider_unreachable` | system_wide | Provider unreachable (network failure) | On | On | error | affected chats only | Yes | provider-episode tracker (§14.3) on the first network-class failure | every account (global) | key `provider_unreachable:<provider>`; resolved by the next successful call to that provider | Settings → Providers |
| `provider_error` | system_wide | Provider returned an error (API failure) | On | On | error | affected chats only | Yes | episode tracker, API-class failures in scope per FQ-6 | every account | key `provider_error:<provider>`; resolved by the next success | Settings → Providers |
| `provider_quota_exhausted` | system_wide | Provider balance or quota exhausted | On | On | error | affected chats only | Yes | episode tracker, `quota_billing` code | every account | key `provider_quota:<provider>`; resolved by the next success | Settings → Providers |
| `provider_rate_limited` | system_wide | Provider rate limit hit | On | On | warning | affected chats only | No | episode tracker, `rate_limited` code from a provider (not Omnipus's own limiter) | every account | key `provider_rate:<provider>`; resolved by the next success | Settings → Providers |
| `internet_lost` | system_wide | Omnipus lost its internet connection | On | On | error | affected chats only | No | episode tracker, detection rule per FQ-8 | every account | key `internet`; resolved by any successful provider call | — (no action) |
| `connection_restored` | system_wide | Provider or internet connection restored | On | On | success | affected chats only (as the resolved line) | No | episode end | every account | key `restored:<provider or internet>`; info, auto-handled on creation | Settings → Providers |
| `resources_low` | system_wide | System resources low (memory, disk) | On | On | warning | — | Yes | memory: `config.MemoryPressureHigh` transition false→true (measured only); disk: per FQ-9 | every account | key `resources:<memory or disk>`; resolved on transition back | Settings → Performance |
| `concurrency_reduced` | system_wide | Concurrency limit reduced because resources are low | On | On | warning | — | No | `applyMemoryCap` transition to memory-binding on a measured host (FQ-10) | every account | key `concurrency`; resolved when memory stops binding | Settings → Performance |
| `knowledge_drift` | system_wide | Knowledge base search index rebuilt after drift (existing) | On | Off | warning | — | No | `knowledgeDriftNotifier` (existing) | every account (existing) | existing key | knowledge base |
| `stop_notice` | **removed from the centre (pending founder, FQ-3)** | Stop outcome | — | — | — | Warning line in the chat where Stop was pressed | No | `publishStopNotice` → becomes a system line | — | — | — |
| `notification_delivery_failed` | system_wide | Could not deliver notifications to a channel | **Part 2** | Part 2 | error | — | — | Part 2 (§29) | every account | key `delivery:<channel>` | Channels |

Rules:
- **R-EV-1.** The catalogue is closed: the kind enum in the contract (§7.1) is the only list; a producer with a kind not in it fails a unit test that walks every producer call site's kind against the generated enum (FR-001).
- **R-EV-3 / D14.** "Affected chat" = a chat whose turn made a provider call that failed, retried, waited or fell back because of the event. The line in that chat is the turn's own retry/error/fallback line (§11); no line is broadcast to idle chats.
- **R-EV-4 / D12.** A system-wide event appears once in the centre (coalesce key) and once as a pop-up per episode, plus one line in each affected chat.
- **Scheduled vs not scheduled.** A task run triggered by a schedule produces `schedule_finished` / `schedule_failed` only, never also `task_finished` / `task_failed` (no double entry).
- **Episode flapping.** A failure for the same key within 60 s after its resolution re-opens the resolved entry (no second pop-up); the `connection_restored` entry for that short gap is not created.

---

## 7. Wire contract changes (Hard Constraint #8)

> Shapes decided here (architect owns contract shapes); `backend-lead` writes the YAML under `contracts/`, regenerates with `scripts/gen-contracts.sh`, and commits spec + generated artifacts atomically **before any Go/TS code** (WP-0). Field names below are the wire names. No YAML in this spec.

### 7.1 Changed: `Notification` (`contracts/components/schemas/Notification.yaml`) and `NotificationList`

| Field | Change | Meaning |
|---|---|---|
| `type` | enum replaced by the §6 kind ids (keeps `schedule_failed`, `knowledge_drift`; `goal_paused` per FQ-2; `stop_notice` removed per FQ-3) | the event kind |
| `category` | **new, required**, enum `needs_you` · `tasks_plans` · `system_wide` | filter (R-NC-2) |
| `severity` | enum extended: `info` · `success` · `warning` · `error` · `needs_you` | pop-up duration and colour (R-PU-1) |
| `resolved_at_ms` | **new, optional** int64 | set when handled anywhere / condition cleared (R-NC-3) |
| `workspace_id` | **new, optional** | per-workspace preference and navigation |
| `target` | **new, required** object: `screen` enum (`chat` · `task` · `plan` · `schedule_run` · `providers_settings` · `performance_settings` · `knowledge_base` · `none`) plus optional `session_id`, `task_id`, `plan_id`, `knowledge_base_id` | navigation (R-NC-6, R-PU-4, D39) |
| `provider` | **new, optional** | provider display name for System-wide provider kinds |
| `affected_chat_count` | **new, optional** int | updated on coalesce for provider episodes |
| `schedule_id`, `session_id`, `agent_id` | unchanged | routing data |

`NotificationList` gains nothing new beyond the item shape; `unread_count` stays and counts unread **and unresolved-or-informational** items (handled items are read by definition).

### 7.2 Changed: `NotificationFrame` (`contracts/asyncapi.yaml`, inline component)

Same field changes as §7.1 plus:

| Field | Meaning |
|---|---|
| `popup` | **new, required** bool — the server's verdict from preferences (R-PREF-1/3). The browser never re-derives it |
| `change` | **new, required** enum `created` · `updated` · `read` · `resolved` · `deleted` · `cleared` — one frame type carries every lifecycle change to every connection of the recipient (R-NC-3). `deleted` carries only `id`; `cleared` carries no item (all handled-or-informational items removed) |

Delivery stays recipient-scoped (today's forwarder filter on the user). The frame is not session-sequenced (it is not a session frame).

### 7.3 New REST operations (`contracts/openapi.yaml`, tag `notifications`)

| Operation id | Method + path | Request | Responses |
|---|---|---|---|
| `deleteNotification` | DELETE `/notifications/{id}` | — | 204; 404 unknown; 409 `notification_waiting` (unresolved Needs-you) |
| `clearNotifications` | DELETE `/notifications` | — | 204 (unresolved Needs-you items stay) |
| `getNotificationPreferences` | GET `/notification-preferences` | — | 200 `NotificationPreferences` |
| `updateNotificationPreferences` | PUT `/notification-preferences` | `NotificationPreferencesUpdate` | 200 `NotificationPreferences`; 400 with codes in §5.3 |
| `reportConnectionOutage` | POST `/client-events/connection-outage` | `ConnectionOutageReport` | 204; 400 `invalid_outage_report`; 429 |

Existing `markNotificationRead` / `markAllNotificationsRead` keep their paths; their behaviour gains the cross-tab push (FR-007).

New schema files (`contracts/components/schemas/`):

| Schema | Shape |
|---|---|
| `NotificationKind` | the closed enum of §6 kind ids (referenced by `Notification.type`, `NotificationFrame.notification_type`, preferences) |
| `NotificationKindPreference` | `{ notify: bool, popup: bool, notify_locked: bool (read-only), popup_locked: bool (read-only) }` — the Part 2 third switch `external` is **not** added now; it is reserved by name (§29) |
| `NotificationPreferences` | `{ global: map NotificationKind → NotificationKindPreference (every kind present), workspaces: map workspace_id → map NotificationKind → { notify, popup } (only non-system-wide kinds; only the kinds overridden), categories: map NotificationKind → category (read-only, for the UI) }` |
| `NotificationPreferencesUpdate` | `{ global?: map kind → {notify?, popup?}, workspaces?: map workspace_id → (map kind → {notify?, popup?} or null to delete the override) }` — partial update |
| `ConnectionOutageReport` | `{ kind: offline · server_unreachable, started_at: date-time, ended_at: date-time, reconnect_attempts: int ≥ 0, page_reloaded: bool }` |
| `SystemLine` | see §7.5 |

The `discriminator`/`oneOf` exception (ADR-034) does not apply — none of these is a union.

### 7.4 Changed: provider error and retry shapes

| Schema | Change |
|---|---|
| `LLMError` (`LLMError.yaml`) | **new optional `provider_says`** (string ≤ 2,000 bytes): the provider's own error message, extracted (§13.3), masked (§15), **persisted and replayed** (amends ADR-051, §28). **New optional `failure_class`** enum `network` · `api` (absent for non-provider codes). `facts` gains optional `http_status` (int) — Verbose/Debug only (L9). Enum: **new code `provider_error`** (API failure: 408/5xx/529 and other provider-side errors, FQ-6) — `network` keeps only network-class failures. `x-user-messages` replaced by the WORDING §1 catalogue (§13.2); every `{provider_says}` slot added to the closed slot vocabulary |
| `LLMErrorReplay`, `ReplayErrorPayload` | carry the same `provider_says` and `failure_class` so a reloaded chat renders identically |
| `ProviderRetryFrame` | `error_code` may now be `network` or `provider_error` (today always `rate_limited`); **new optional `reason`** enum `dns_failure` · `connection_refused` · `network_unreachable` · `tls_failure` · `timed_out` · `connection_dropped` · `http_status`; **new optional `http_status`** int; **new optional `provider_says`** (masked, API class only) |
| `WsFrameType` | adds `system_line` |

### 7.5 New: `SystemLine` (shared shape), `SystemLineFrame` (live) and `Message.system_line` (history)

A server-authored, persisted chat line for messages that **have no structured carrier today** (FQ-16 = A: existing typed frames stay and are mapped to the renderer; only new lines use this carrier).

| Field | Meaning |
|---|---|
| `type` | const `system_line` (frame) |
| `session_id`, `seq` | session-scoped, hub-sequenced like every session frame |
| `line_id` | stable id; a later line with `replaces_line_id` updates it in place |
| `line_type` | `info` · `warning` · `error` · `resolved` · `needs_you` (R-SL-3) |
| `in_progress` | bool (R-SL-4) — animated state |
| `text` | final wording, server-authored, already voice-ruled |
| `provider_says` | optional, masked |
| `action` | optional `{ kind: switch_model · open_provider_settings · open_task · open_performance_settings · retry_reply · review, label }` — at most one (R-SL-7) |
| `details` | optional masked technical text — rendered only at Verbose (collapsed) / Debug (open) (L9) |
| `min_level` | `normal` · `verbose` — the lowest detail level that shows the line (record lines are `verbose`, R-SL-5) |
| `replaces_line_id` | optional |
| `created_at` | date-time |

Persistence follows the stamped-subtype convention of `contracts/components/schemas/Message.yaml` (`system_subtype`, as `goal_outcome` and `helper_event` do): the entry is `type: system`, `Message.system_subtype` gains the value `system_line`, and `Message` gains an optional `system_line` field referencing the shared `SystemLine` schema. `pkg/gateway/replay.go` discriminates on the stamped subtype and re-emits a `SystemLineFrame`, so live, reload and paged history (`SessionHistoryResponse` → `Message`) render the same line. Lines that must leave history (browser↔server loss, R-SL-6) are **client-only** and never use this carrier.

---

## 8. Notification centre (R-NC-1…6, R-SEC-2)

### 8.1 One producer path

All producers (existing four, new ones) call one function in `pkg/notifications` — the *router* — never `Store.Create` directly:

```
producer ──> router.Notify(kind, owner-resolution input, title, body, target, coalesce key)
              │ 1. resolve recipients (session owner / job creator / every account for system_wide)
              │ 2. read the recipient's preferences (global, then workspace override)
              │ 3. notify off  → stop (log at debug only; the event itself is in logs/transcripts)
              │ 4. mint id at construction (never empty), mask body (§15)
              │ 5. persist (coalesce on unresolved key); store failure → log ERROR, continue live
              │ 6. emit NotificationFrame{change, popup} to the recipient's connections
              v
             store file  +  live frame  ──> SPA notifications store ──> centre / pop-up
```

- Owner (R-NC-5, D24): never inferred from a missing record. Unreadable owner → no broadcast, log ERROR (keeps `AgentLoop.notifyGoalPaused`'s rule). No accounts at all → live-only with a minted id, log WARN (today's sentinel, now with an id — closes #645 Defect 1).
- The router's recipient list is computed per call; nothing assumes one user (D24).
- Concurrency: the store keeps its process mutex and adds a file lock around read-modify-write per recipient file, matching the file-store family (`pkg/fileutil::WithFlock`) — closes #645's "no flock" point.

### 8.2 Lifecycle

| State | Set by | Effect |
|---|---|---|
| created (unread) | router | badge +1; pop-up if `popup` |
| updated | router coalesce | content refreshed; no new pop-up; unread stays unread |
| read | user opens it or marks read | badge −1 |
| resolved | condition cleared (§6 resolve trigger) **or** user handled it through its action | also read; pop-up removed everywhere; shown greyed with "Handled" |
| deleted / cleared | user | gone from storage (real deletion, R-SEC-2) |

"Handled anywhere = resolved everywhere" (D19): approval decided in the chat → the approval path already emits `tool_approval_resolved`; the router resolves `approval:<id>` in the same step and emits `change: resolved`. Every tab removes the pop-up and greys the entry.

### 8.3 Retention (R-NC-4, D23)

- Age: entries with `created_at_ms` older than 30 days are deleted by a sweep at start-up and every 6 hours, and opportunistically on every write to that recipient's file.
- Count: the existing cap of 50 stays as a safety bound (FQ-12), but eviction never removes an **unresolved Needs-you** entry; it removes the oldest handled-or-informational entries first.
- Delete one / Clear all: real removal from the file. Unresolved Needs-you entries cannot be deleted (409) and survive Clear all — they leave only by being handled.

### 8.4 Centre UI

- Same place (account menu → **Notifications**, right-hand sheet; D33 keeps the sidebar).
- Filter chips: **All · Needs you · Tasks & plans · System-wide** with counts.
- Each entry: type icon and colour by severity, title, short body, relative time, "Handled" state, one action (the `target`), delete control.
- Header: **Mark all read**, **Clear all** (confirm dialog — a dialog the user opens is allowed, D17).
- Merge on hydrate: REST list merged by id, REST wins on conflict; live-only items (never persisted) survive a refetch (#645 Defect 2).

---

## 9. Preferences (R-PREF-1…4, D3, D20, D22, D30)

### 9.1 Model

- Per user, stored server-side next to the notification store (one file per recipient, same sanitising, atomic write, file lock). Server-side because the router (§8.1) must apply it when no browser is open, and Part 2 / mobile need the same record.
- Missing file = fresh-install defaults from §6. Unreadable file = defaults in effect, log ERROR, preferences screen shows "Preferences could not be read — saving will replace them" with Retry.
- Resolution order per event: workspace override for the event's `workspace_id` (if any, non-system-wide kinds only) → global → catalogue default.
- Locked switches: the two Needs-you kinds — `popup` locked on (R-PREF-3); `notify` locked on too (FQ-4, pending founder). The server rejects an attempt to turn a locked switch off (400) and reports `*_locked: true` so the UI can show it.
- **Also pop up** is effective only when **Notify me** is on ("also"); the UI disables it when Notify is off and the server ignores it.

### 9.2 UI

- **Settings → Notifications** (new tab, beside Chat): three category groups, one row per kind with two switches and a one-line description; Needs-you rows show the lock and "Always on — an agent is waiting for you".
- **Workspace → Settings tab** (`src/components/workspaces/WorkspaceSettingsTab.tsx`): "Notifications for this workspace" — Tasks & plans and Needs-you kinds (Needs-you shown locked), each with "Use global / On / Off"; System-wide kinds are not listed (R-PREF-2).
- Save behaviour follows the own-action rule: on-page saved indicator / "Not saved — Retry", no pop-up (R-PU-5).

### 9.3 What preferences do not govern

Chat lines (always shown per detail level), logs, transcripts, the sidebar attention cue.

### 9.4 Compatibility with #177 (REQ F5)

#177 asks for per-**device** push categories (`tool_approvals`, `agent_dms`, `task_completion`) for a future mobile channel. This spec's per-**user**, per-**kind** model is the superset: the mobile categories map to kind groups (`tool_approvals` → `approval_requested` + `question_asked`; `task_completion` → `task_finished`, `task_failed`, `schedule_finished`, `schedule_failed`; `agent_dms` has no kind here — it is chat content, which external delivery never carries, R-SEC-3). A device-level filter, if built, applies **after** the user's "Also send externally" switch in Part 2. Nothing in Part 1 blocks #177.

---

## 10. Pop-ups (R-PU-1…6, D21, D30, D31, D39)

| Rule | Design |
|---|---|
| Source | Only (a) `NotificationFrame` with `popup: true` and `change: created` (or a re-opened episode, §6), and (b) the fixed list of browser-local event pop-ups (FQ-14). Never an own-action result |
| Host | The existing bottom-right `ToastContainer`, reworked into the pop-up host; `addToast` stays only for the browser-local list until WP-5 removes the rest |
| Duration | info / success 5 s; warning 10 s; error and needs_you until dismissed or handled; pointer hover or keyboard focus pauses the countdown; leaving resumes it |
| Cap | ≤ 3 visible, newest at the bottom; further ones collapse into one line "+N more — open notifications" that opens the centre; a resolved pop-up frees its slot |
| Action | ≤ 1 main action, from `target` (labels: **Open**, **Review**, **Open settings**), plus close (×). The action navigates (D39); it never approves or denies in place |
| Resolve | `change: resolved / deleted / cleared` removes a showing pop-up in every tab |
| Needs-you in the open chat | Pending founder FQ-5: no pop-up in the tab where that chat is open and visible (the inline card is the surface); other tabs still pop up |
| No modals | No event opens a modal; `ToolApprovalModal` and its feature switch are deleted (R-RM-2) |
| Accessibility | Pop-up host is a polite live region; needs_you/error use `role="alert"`; the close control has a name; durations do not apply while focus is inside |

---

## 11. In-chat system lines (R-SL-1…7, D5, D6, D45)

### 11.1 One renderer

One frontend component, **`SystemLine`**, renders every system message in a chat, live and replayed, server- or browser-originated (R-SL-1). It is a single line of text, not a card (R-SL-2). The frontend owner loads the `omnipus-design-system` skill before building it and uses catalogued primitives (icon, button, disclosure) only.

| Line type | Icon (Phosphor) | Colour token | Text weight | Example |
|---|---|---|---|---|
| info | `Info` | `--color-info` | regular | "Working — reading the CRM export" (in progress) |
| warning | `Warning` | `--color-warning` | medium | "I can't reach OpenRouter: connection refused. Retrying in 8 s…" (in progress) |
| error | `XCircle` | `--color-error` | semibold | "OpenRouter says your account is out of credit: "Insufficient balance". Top up your OpenRouter account, or switch to another provider." |
| resolved | `CheckCircle` | `--color-success` | regular | "Connection to OpenRouter restored after 2 retries (14 s)." (Verbose record line) |
| needs_you | `HandPalm` | `--color-accent` | semibold | "Waiting for your approval to run `git push`." |

- **In progress** (R-SL-4): any type; the icon pulses and the line ends with an animated ellipsis; `prefers-reduced-motion` turns the animation into a static "…".
- **Provider message**: shown on the main line in quotes, plain text (no Markdown/HTML), cut at 300 code points with "…" (§13.3).
- **Action** (R-SL-7): at most one, as a small text button at the end of the line; it navigates or acts directly (Switch model opens the chat's model picker; Open settings navigates; Retry re-sends the failed reply through the existing regenerate path).
- **Technical details** (L9): a disclosure "Technical details" under the line — rendered at Verbose (collapsed) and Debug (open), never at Normal.

### 11.2 Carrier → line mapping (FQ-16 = A, pending founder)

Existing typed frames and transcript entries stay; each maps through **one adapter module** (one table, unit-tested row by row) into `SystemLine` props. New lines with no carrier use `SystemLineFrame` (§7.5).

| Carrier (today) | Renderer today | Line type | In progress | Min level | Action |
|---|---|---|---|---|---|
| `ErrorFrame` / `ReplayErrorFrame` with `LLMError` (turn ended in failure) | inline error + `ErrorDetailDisclosure` | error (`turn_canceled` → info "You stopped this reply.") | no | normal | by code: Switch model (`provider_rejected`, `rate_limited`, `model_retired`, `bad_model`, `media_unsupported`, `request_too_large`, `context_too_long`, `content_policy`, `network`, `provider_error`, `unknown`); Open provider settings (`provider_auth_failed`, `quota_billing`, `needs_provider`, `model_route_unavailable`, `context_window_unknown`); Open settings (`judge_time_limit`); Open task (`delegated_task_limit`); none otherwise |
| `ProviderRetryFrame` | `ProviderRetryIndicator` | warning | yes (countdown) | normal while the turn runs | — |
| `ProviderFallbackFrame` / persisted fallback note | `ProviderFallbackNoteLine` | info | no | normal while the turn runs; **verbose after it ends** (D41 L8 — see FQ-11 b) | Pick a new model when `model_retired` |
| `RateLimitFrame` (Omnipus's own limiter, SEC-26) | `RateLimitIndicator` | warning | yes until `retry_after` | normal | — |
| `ContextWindowNoticeFrame` | system pill | info | yes during compaction | normal while happening; verbose after | — |
| `CancelStageFrame` | stop-button state | info | yes | normal | — |
| Goal outcome entry (`system_subtype: goal_outcome`) | `GoalOutcomeRow` | resolved (`met`) / warning (`rounds_exhausted`, `other`) / info (`stopped_by_user`) | no | normal (L2) | — |
| Helper event entry (`helper_event`) | `HelperEventRow` | info / warning by event | per event | normal | — |
| Browser handover notice, goal acknowledgement, clear-context divider | system pill | info | no | normal | — |
| Delegation line | `DelegationEventLine` | info | yes while running | normal (L7 summary) | — |
| Restart-interrupted notice | `RestartInterruptedNotice` | warning | no | normal | Retry |
| Unavailable linked chat | `UnavailableChatNotice` | error | no | normal | Retry |
| Browser↔server loss (client-only) | `ChatConnectionStatusLine` | warning ("Can't reach the server right now…" / offline) → resolved ("Up to date", then removed) | yes | normal; never persisted (R-SL-6) | Try again |
| **New** network/API retry outcome record | — | resolved (recovered) | no | **verbose** (R-SL-5 record line) | — |
| **New** Stop outcome (FQ-3) | (centre today) | warning | no | normal | — |
| **New** "working on …" step line | `AgentStatusIndicator` | info | yes | normal (L4) | — |

### 11.3 Connection lines (R-SL-5, R-SL-6, D10, D18, D42)

- Omnipus↔provider/internet problem while a reply waits: in-progress Warning at every level (driven by `ProviderRetryFrame` with network or API class). On success: at Normal the line disappears; the server appends one `SystemLine` record (`line_type: resolved`, `min_level: verbose`) "Connection to {provider} restored after {n} retries ({seconds} s)." On final failure: the terminal Error line (L3) replaces it at every level.
- Browser↔server loss: one app-wide indicator (the `AppShell` banner area, now shown in every reconnecting phase, not only on terminal errors) plus the chat line above; both client-only, neither enters history; the outage is logged via the browser's report (§14.4).

---

## 12. Detail levels (R-DL-1…3, D8, D16, D25, D41, D43)

### 12.1 Setting

- **Settings → Chat → Detail level**: segmented control **Normal · Verbose · Debug** replacing the Verbose-chat switch in `src/components/settings/ChatSection.tsx`. Per device, like today's switch (§3.2 N1). Default Normal. Every user may pick Debug (R-DL-3). Greenfield: the stored boolean is not migrated (an old `verboseChatEnabled: true` reads as Normal).
- One rule function in `src/lib/` decides visibility for every row kind and level; all 27 current consumers of `verboseChatEnabled` call it and nothing else (R-DL-1, D8). A test enumerates every row kind × level against the table below.

### 12.2 Normative table (REQ §5.6, verbatim rows) with implementation mapping

| # | What | Normal | Verbose | Debug | Implementation mapping |
|---|---|---|---|---|---|
| L1 | Your messages and the agent's replies | Shown | Shown | Shown | message rows |
| L2 | Task and goal results; "agent needs you" requests | Shown | Shown | Shown | goal outcome, helper results, approval and ask cards |
| L3 | A failure that stopped or changed a reply (our sentence + provider's message) | Shown | Shown + Technical details incl. masked payload (collapsed) | Shown + Technical details open | Error `SystemLine`; details = masked `LLMError.detail` (live) and `http_status`, `request_id` |
| L4 | One "working…" line naming the current step | Shown | Shown | Shown | `AgentStatusIndicator` as info in-progress line |
| L5 | Tool calls with visible output (file, web page, image) | Shown | Shown | Shown | cards of `FileReadPreview`, `FileWriteConfirm`, `FileTreeView`, `WebFetchPreview`, `WebServeUI`, `BrowserTool`/`BrowserNavigate` (image), `DraftLink`, `WebSearchResult` |
| L6 | Every other tool call | Compact line (name only) | Line + result | Line + result + raw | `GenericToolCall`, `BashOutput`, `ToolCallBadge`; FQ-11 a for calls hidden today |
| L7 | Delegation — which agent was asked what | Summary only | Every step | Every step | `DelegationEventLine` vs `delegate` badges |
| L8 | Automatic retries, backup-model switch, connection problems | While happening only | Shown, record line stays | Shown, record line stays | §11.3; fallback note per FQ-11 b |
| L9 | Technical details on a system line (status code, request ID) | — | Collapsed link | Open by default | `SystemLine` details disclosure |
| L10 | Timings, token counts, cost per reply | — | Shown | Shown | `DoneStats` (`tokens`, `cost`, `duration_ms`) |
| L11 | Raw provider request/response of non-failing calls, internal IDs | — | — | Shown | internal IDs: session, turn, tool-call, approval, request ids (on the wire today); raw non-failing payloads per FQ-17 |

Rules: technical details only at Verbose and Debug (R-DL-2, D25); the masked failure payload is the only raw payload at Verbose (D43); secrets masked at every level (R-SEC-1, D16); the connection-problem line shows at every level while it lasts (R-SL-5).

---

## 13. Wording (R-WD-1…9, D26, D28, D35, D36, D47–D50)

### 13.1 Rules (checklist applied to every system message)

1. What happened, in plain words · why, if known · what the user can do, or what is already being done (R-WD-1).
2. Dynamic parts are real values: provider, model, tool, agent, status, seconds, path (R-WD-2).
3. The provider's own message is quoted on the main line when one was received — never hidden, never force-replaced (R-WD-2, D28).
4. Our own sentence accompanies it; ADR-051's fixed texts remain only where they add an action (R-WD-3).
5. In a chat (web chat or channel chat): the agent speaks as "I"; "Omnipus", "we", "our", "us" never act (R-WD-5, R-WD-7). Lines about the user's own action stay as they are ("You stopped this reply.").
6. Everywhere else (pop-ups, banners, centre, settings): neutral (R-WD-6, R-WD-8); a message used in both places has two forms.
7. Third parties may be the actor ("OpenRouter rejected the API key"). The only product-name use: "please report it to the Omnipus.ai team" (D48).

### 13.2 Catalogue (ADR-051 codes)

- `contracts/components/schemas/LLMError.yaml` `x-user-messages` takes the WORDING §1 final sentences for all 29 codes, plus `provider_error` (new, from the `network` split). Each entry carries up to three explicit sentences — no clause logic in code:
  - `message`: no facts (no provider/model known);
  - `provider_message`: provider/model known, no provider message received;
  - `provider_message_quoted`: provider/model known and `provider_says` present.
  The generators (`scripts/gen-asyncapi-go`, `scripts/_gen-asyncapi-types.mjs`) validate slots against the extended closed vocabulary: `{provider}` `{model}` `{provider_says}` `{reason}` `{status}` `{n}` `{seconds}` `{minutes}` `{tool}` `{agent}` `{path}` `{model_requested}` `{model_used}` `{limit_name}` `{value}` `{task}` `{attempt}` `{max}` `{countdown}` `{answered_model}` `{unavailable_model}`. A sentence never renders an empty slot or empty quotes.
- `x-user-message-attributions` stays.
- The copy-rule tests (`pkg/agent/translate_error_test.go`, `src/lib/llm-error.test.ts`) are updated to the new rules: "a `product` or `config` message never tells the user to switch models" still holds; "no message tells the user to contact support" is relaxed by exactly the D48 sentence.
- `network` split (WORDING §1, row `network`):

| Situation | In chat | Outside chat (centre / pop-up title) |
|---|---|---|
| network, retrying | I can't reach {provider}: {reason}. Retrying in {n} s… | {provider} can't be reached: {reason} |
| network, retries exhausted (**new, proposed under R-WD-1…8**) | I couldn't reach {provider}: {reason}. Retry, or switch model if it repeats. | {provider} can't be reached: {reason} |
| API, retrying (**new, proposed**) | {provider} answered with an error ({status}): "{provider_says}". Retrying in {n} s… | {provider} returned an error ({status}) |
| API, final | {provider} answered with an error ({status}): "{provider_says}". Retry, or switch model if it repeats. | {provider} returned an error ({status}) |

- `{reason}` words (fixed; new texts, reviewed with FQ-13 — the baseline gave only examples): `dns_failure` → "its address could not be found"; `connection_refused` → "the connection was refused"; `network_unreachable` → "the network is unreachable"; `tls_failure` → "the secure connection failed"; `timed_out` → "it did not answer in time"; `connection_dropped` → "the connection dropped".

### 13.3 Provider message extraction (`provider_says`)

From the failing attempt's response, in order: JSON body → first non-empty string at `error.message`, `error.error.message`, `message`, `error` (when a string), `detail`, `error_description`; otherwise a non-JSON body's first line; otherwise a stream error event's message. Then: strip control characters, collapse whitespace, mask (§15), cap 2,000 bytes stored; the main line shows ≤ 300 code points + "…". Never the whole raw body. No match → absent (the no-quote sentence is used).

### 13.4 Voice guard

New CI guard `scripts/check-system-message-voice.sh` (picked up by `scripts/guards.sh`): scans the `LLMError.yaml` catalogue and the server-side system-line text table for `\bOmnipus\b` (allowed only inside the exact D48 sentence) and, for in-chat sentences, `\b(we|our|us)\b` (case-insensitive); exits non-zero with the offending entry. Self-check: the script's own fixture with a planted "we" must fail.

### 13.5 Remaining messages (R-WD-4, R-WD-9)

- In WP-6 scope: the 29 catalogue codes + `provider_error`, the 44 voice-rule and 6 founder-edited messages in WORDING §2, and the new texts in this spec (§13.2 table, §6 centre titles, §11 record line, §16 indicators).
- The 163 messages marked "no proposal yet" in `revised/revised.json`: **FQ-13** (recommended: rewritten through the existing review page as a wording appendix the founder approves before GREEN — pending founder).
- WORDING §2 entries whose surface disappears in this feature (pop-ups removed by WP-5, the deleted `ToolApprovalModal`) are dropped, not rewritten.

### 13.6 Proposed neutral centre titles (new texts, R-WD-6; reviewed with FQ-13)

| Kind | Title | Body |
|---|---|---|
| `approval_requested` | {agent} is waiting for your approval | {tool}: {short summary} in {chat} |
| `question_asked` | {agent} asked you a question | in {chat} |
| `task_started` / `task_finished` / `task_failed` | Task "{task}" started / finished / failed | failed: {cause sentence, neutral} |
| `schedule_finished` / `schedule_failed` | Scheduled task "{name}" finished / failed | failed: {cause sentence, neutral}; never the raw run error |
| `goal_reached` / `goal_given_up` | Goal reached / Goal given up in {chat} | {goal text, ≤160 chars} |
| `provider_quota_exhausted` | {provider}: account out of credit | "{provider_says}" |
| `provider_rate_limited` | {provider} is limiting requests | {affected_chat_count} chats waiting |
| `internet_lost` | The server has no internet connection | Replies wait until it is back |
| `connection_restored` | {provider} reachable again / Internet connection restored | after {duration} |
| `resources_low` | Memory is running low / Disk space is running low | Fewer agents run at once until it recovers (memory) |
| `concurrency_reduced` | Fewer agents run at once | Limited to {n} because memory is low |

---

## 14. Connection handling and logging (R-LOG-1…4, R-EV-2, D13, D32, D44)

### 14.1 Network vs API classification

| Observation on a provider attempt | Class | `reason` |
|---|---|---|
| DNS failure (`no such host`) | network | `dns_failure` |
| `connection refused` | network | `connection_refused` |
| `network is unreachable` | network | `network_unreachable` |
| TLS handshake failure | network | `tls_failure` |
| dial or response-header timeout (`i/o timeout`, `dial tcp … timeout`, our turn deadline before any response) | network | `timed_out` |
| connection dropped **before** response headers (`connection reset by peer`, `broken pipe`, `unexpected eof`) | network | `connection_dropped` |
| connection dropped **after** a 200 started streaming (reset, EOF, `http2: server sent goaway`, `stream error:`) and silence-limit stall (`provider_stalled`) | **network (pending founder, FQ-7)** | `connection_dropped` / `timed_out` |
| any HTTP status ≥ 400 received (incl. 408, 429, 5xx, 529) | api | `http_status` |
| provider error event inside a 200 stream (`common.StreamProviderError`) | api | `http_status` (status of the event when given) |

The class is computed once, at the provider boundary, from the same inputs `providers.ClassifyError` already sees, and travels on the failure (`FailoverError` gains a class label; `LLMError.failure_class`; `ProviderRetryFrame.reason`). Retry, cooldown and failover decisions do not read it (FR-063).

### 14.2 Per-attempt log line

Every failed provider attempt (inside `FallbackChain.Execute`, the agent's own retry loops, and stream-level failures) writes **one WARN line** "provider attempt failed" with fields: `failure_class`, `reason`, `provider`, `model`, `http_status` (api), `provider_says` (masked, ≤ 500 bytes), `attempt`, `max_attempts`, `next` (`retry` · `fallback` · `give_up`), `retry_in_ms`, `session_id`, `turn_id`, `request_id`. The existing ERROR "LLM call failed" line stays and gains `failure_class`. WARN because the shipped default level is `warn` (§3.1 C13) — Info would be invisible (R-LOG-1).

### 14.3 Provider-episode tracker (system-wide kinds)

In-memory, one per gateway process, keyed by (class or code, provider). Opens on the first failure, counts affected sessions, closes on the first success to that provider. Logs **episode start** and **episode end** (duration, attempts, affected chat count) at WARN. Drives the router: open → `provider_unreachable` / `provider_error` / `provider_quota_exhausted` / `provider_rate_limited` (coalesced); close → resolve + `connection_restored`. `internet_lost`: per FQ-8 (recommended: `network_unreachable` from the OS, or network-class failures to ≥ 2 distinct provider hosts within 60 s with no success in between; no probe traffic). Episodes are not persisted; a restart starts clean (the centre entry stays until the next success resolves it or it ages out).

### 14.4 Browser↔server

- **Client**: the WebSocket client records the outage start (time, kind `offline` when `navigator.onLine` is false, else `server_unreachable`) in tab-scoped session storage on the first unexpected close (not on page unload); on the next successful authentication it POSTs one `ConnectionOutageReport` and clears the record; a failed POST is retried once on the next reconnect, then dropped (logged to the browser console).
- **Server**: logs WARN "client connection outage" with `user`, `kind`, `started_at`, `ended_at`, `duration_ms`, `reconnect_attempts`, `page_reloaded`. No free-text field exists, so nothing user-typed reaches the log. The existing `ws: connection closed unexpectedly` line moves from Debug to WARN with user and connection age (O4: server-side connection issues are mandatory to log).
- **R-LOG-4**: every line that leaves the chat (R-SL-5 Normal, R-SL-6) has its episode in the log (14.2, 14.3, this subsection).

---

## 15. Security and privacy (R-SEC-1, R-SEC-2; D16, D23, D28)

| Topic | Rule |
|---|---|
| One masking function | `scrubDetailText` (credential replacer + key-shaped token regex) becomes the shared masker, exported from `pkg/agent` (or moved to `pkg/logger`, owner's choice) and applied to: `provider_says`, technical details, every notification title/body built from external text, every log field carrying provider text. Applied **before** persistence and before the wire |
| Main-line provider message | Masked like details (providers echo partial keys, e.g. "Incorrect API key provided: sk-…xyz") — dataset DS-SEC |
| Debug | Available to every user (D16); shows nothing unmasked |
| Schedule-failure body | No longer the raw run error: the translated sentence plus masked `provider_says`; the raw error stays in the log only |
| Retention | 30-day age deletion; Delete / Clear all remove from storage; quarantined corrupt files (`<recipient>.json.corrupt-*`) are deleted by the same sweep after 30 days |
| Recipient file name | Collision-free for distinct usernames: when sanitising changed the name, a short hash of the exact username is appended (#645 non-injective sanitise) — no migration, greenfield |
| Outage endpoint | Authenticated (bearer), rate-limited (10/min/user → 429), body ≤ 4 KiB, enumerated fields only |
| Preferences endpoint | Authenticated; a user reads and writes only their own record |
| Notification frames | Recipient-scoped delivery unchanged |
| Out of scope (recorded) | The `_dev_bypass` / `_env_token` / `cli` identities are never recipients (#645) — unchanged in Part 1; listed in §31.3 |

security-lead reviews `pkg/notifications` (storage, deletion), the masking change and the new endpoints in the 5-reviewer gate (focus area: gateway auth and rate limiting).

---

## 16. Removals (R-RM-1, R-RM-1a, R-RM-2, R-RM-3; D31, D34, D37, D46)

| Item | Action |
|---|---|
| Own-action pop-ups (WP-5) | Work list = the audit records `toast-audit/result-w1..w4.jsonl` (322 rows) **plus the 6 unaudited sites** (§3.1 C2), re-audited with the same fields before editing. Per recommendation: *remove (page already shows it)* 98 → delete the call; *remove + add page indicator* 107 → add the indicator first (reuse `AutoSaveIndicator`, the agent-profile "Save not confirmed + Retry" pattern, error cards with Retry), then delete; *… with Retry* 87 → same with Retry; *move to chat system line* 8 → `SystemLine` (those citing `ToolApprovalModal` vanish with it); *keep as event pop-up* 10 → browser-local list (FQ-14); *delete* 3; *dev-only* 2 → unchanged; *needs founder decision* 7 → FQ-15. Removal only where the page shows the outcome (D46) |
| Pop-ups holding the only copy of a function (R-RM-1a) | Undo (moved calendar event, added card), the shared-preview-link exposure warning, the cross-tab Switch: not removed until their page home exists — homes per FQ-15 |
| Event-raised modals (R-RM-2) | Delete `src/components/agents/ToolApprovalModal.tsx`, its tests and stories, and `INLINE_TOOL_APPROVALS_ENABLED` (always true). A test asserts no component opens a dialog from a WS frame handler |
| Top-right badges (R-RM-3) | None exist at `4cad412bf`: the only badge is the sidebar unread count; the God Mode corner dot is top-left (`GodModeIndicators.tsx`, `absolute left-0`). A UAT row confirms visually (§24 U14) |
| Superseded copy of "Verbose chat" | The switch, its store field and every "Verbose chat" mention in UI copy are replaced by "Detail level" (no alias, no deprecation comment — delete superseded code) |

---

## 17. Reachability (Definition of Done)

| Part | Who reaches it | Path |
|---|---|---|
| Notification centre + filters + delete | user | sidebar → account menu → **Notifications** (existing entry) |
| Pop-ups | user | automatic, bottom-right, every screen (`AppShell` host) |
| Preferences (global) | user | **Settings → Notifications** (new tab in `SettingsScreen`) |
| Preferences (workspace) | user | workspace → **Settings** tab → "Notifications for this workspace" |
| Detail level | user | **Settings → Chat → Detail level** |
| System lines | user | any chat, live and after reload / history paging |
| Browser outage indicator | user | app-wide banner in `AppShell` + open chat |
| Network/API logs | operator | gateway log at the default level (`warn`) |
| Outage report | browser → server | automatic on reconnect; visible in the gateway log |
| Each event kind | producer → centre | §6 producer column; reachability test: one integration test per kind drives the real producer and reads `GET /notifications` (FR-002) |
| Agents | — | No new agent tool: notifications are a user surface. Agents keep reporting in their own chats by their own decision (D51); nothing here is an agent capability that needs a builtin-catalogue entry (Hard Constraint #6 untouched) |

---

## 18. BDD scenarios

> Ids `S-xx` are used by the TDD plan and the traceability matrix. One action per When.

### Feature: notification centre and routing

#### S-01: Opted-in kind creates one centre entry
**Traces to**: US-1 AC-1 · **Category**: Happy Path
- **Given** fresh-install defaults and user `dana` owns schedule "Nightly report"
- **When** the scheduled run fails with a provider 402 "Insufficient balance"
- **Then** `dana`'s centre has one `schedule_failed` entry, category Tasks & plans, title `Scheduled task "Nightly report" failed`
- **And** its body contains no raw error text and the provider message appears masked

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

#### S-05: Every entry navigates
**Traces to**: US-1 AC-5 · **Category**: Happy Path
- **Given** entries of every kind in §6
- **When** the user activates each entry
- **Then** the app opens the screen named by its `target.screen`

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
| schedule_failed | the next run of the schedule succeeds |
| task_failed | the same task reaches done |
| provider_unreachable | the next call to that provider succeeds |
| resources_low | memory pressure ends |
| goal_paused (FQ-2) | the goal resumes |

#### S-14: Refetch keeps live-only entries
**Traces to**: US-2 AC-2 · **Category**: Edge Case
- **Given** a live-only entry (no account existed, nothing persisted) shown in the centre
- **When** the browser refetches the list on window focus
- **Then** the entry is still shown and the badge count is unchanged

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

#### S-23: Unresolved Needs-you cannot be deleted
**Traces to**: US-3 AC-3 · **Category**: Error Path
- **Given** an unresolved `question_asked` entry
- **When** the client calls delete for it
- **Then** the server answers 409 `notification_waiting` and the entry stays

#### S-24: Clear all keeps waiting requests
**Traces to**: US-3 AC-4 · **Category**: Happy Path
- **Given** 5 handled entries and 1 unresolved approval
- **When** the user confirms Clear all
- **Then** only the approval entry remains, in every tab

#### S-25: Quarantined corrupt file is deleted after 30 days
**Traces to**: US-3 AC-1 · **Category**: Edge Case
- **Given** a `<recipient>.json.corrupt-<n>` file 31 days old
- **When** the sweep runs
- **Then** the file is gone

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

#### S-34: Locked Needs-you switches
**Traces to**: US-4 AC-5 · **Category**: Error Path
- **Given** a preferences update turning `approval_requested` popup off
- **When** it is saved
- **Then** the server answers 400 `notification_switch_locked`, and the UI shows the switch locked with its explanation

#### S-35: Unreadable preferences file
**Traces to**: US-4 AC-1 · **Category**: Error Path
- **Given** a corrupt preferences file
- **When** an event occurs and the user opens the preferences screen
- **Then** defaults apply, an ERROR is logged, and the screen shows "Preferences could not be read" with Retry

#### S-36: Pop-up switch without Notify has no effect
**Traces to**: US-4 AC-2 · **Category**: Edge Case
- **Given** a kind with notify off and popup on
- **When** the event occurs
- **Then** neither entry nor pop-up appears, and the UI shows the pop-up switch disabled

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

#### S-42: Cap of three
**Traces to**: US-5 AC-4 · **Category**: Boundary
- **Given** three visible pop-ups
- **When** a fourth and a fifth arrive
- **Then** three remain visible and a line reads "+2 more — open notifications", which opens the centre

#### S-43: One action, navigates
**Traces to**: US-5 AC-5 · **Category**: Happy Path
- **Given** a `schedule_failed` pop-up
- **When** the user activates **Open**
- **Then** the run's session opens and the pop-up closes

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

#### S-46: Needs-you in the open chat (FQ-5)
**Traces to**: US-5 AC-3 · **Category**: Alternate Path
- **Given** chat X open and visible in T1, T2 on another screen
- **When** an approval for chat X arrives
- **Then** T1 shows the inline card and no pop-up; T2 shows the pop-up

### Feature: system lines

#### S-50: Provider rejection as one Error line with the provider's words
**Traces to**: US-6 AC-1, US-8 AC-1 · **Category**: Happy Path
- **Given** provider OpenRouter answers 401 `{"error":{"message":"Incorrect API key provided: sk-or-v1-0123456789abcdef"}}`
- **When** the reply fails
- **Then** one Error line reads `OpenRouter rejected the API key: "Incorrect API key provided: [redacted]". Update the key in Settings → Providers.` with an **Open settings** action

#### S-51: Network retry line
**Traces to**: US-6 AC-2 · **Category**: Happy Path
- **Given** provider P refuses connections
- **When** the agent retries
- **Then** an animated Warning line reads `I can't reach P: the connection was refused. Retrying in <n> s…` at every detail level

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

#### S-55: Replay parity
**Traces to**: US-6 AC-1 · **Category**: Edge Case
- **Given** a chat with an Error line carrying a provider message and a Verbose record line
- **When** the page is reloaded and the history is paged back
- **Then** both lines render identically to the live render, including the quoted provider message

#### S-56: Provider message absent
**Traces to**: US-8 AC-1 · **Category**: Edge Case
- **Given** a provider 503 with an empty body
- **When** the reply fails
- **Then** the line uses the no-quote sentence and contains no empty quotes

### Feature: detail levels

#### S-60: Outline — row visibility
**Traces to**: US-7 AC-2 · **Category**: Happy Path
- **Given** a chat with each row kind L1…L11
- **When** the detail level is `<level>`
- **Then** row `<row>` is `<shown>`

(Examples: every cell of the §12.2 table — 33 rows, dataset DS-DL.)

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

#### S-72: Catalogue equals the agreed baseline
**Traces to**: US-8 AC-1 · **Category**: Happy Path
- **Given** WORDING §1
- **When** the generated catalogues are compared with it
- **Then** every code's sentence matches exactly (placeholders included)

### Feature: connection logging

#### S-80: Outline — failure class in the log
**Traces to**: US-9 AC-1, AC-2 · **Category**: Happy Path
- **Given** an attempt that fails with `<observation>`
- **When** the attempt ends
- **Then** one WARN line "provider attempt failed" has `failure_class=<class>` and `reason=<reason>`

(Examples: dataset DS-NET.)

#### S-81: Browser outage report
**Traces to**: US-9 AC-3 · **Category**: Happy Path
- **Given** the browser lost the server from 10:00:00 to 10:00:40
- **When** it reconnects
- **Then** the server log has one WARN "client connection outage" with `duration_ms` 40000 and `kind=server_unreachable`

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

#### S-84: Episode recorded after the chat line left
**Traces to**: US-9 AC-4 · **Category**: Happy Path
- **Given** S-52 at Normal
- **When** the operator reads the log
- **Then** episode start and end lines for P exist with duration and affected chat count

#### S-85: Retry decisions unchanged
**Traces to**: US-9 AC-1 · **Category**: Regression
- **Given** the existing fallback and retry datasets
- **When** they run with the new labelling
- **Then** every retry/fallback/cooldown outcome equals the pre-change outcome

### Feature: security

#### S-90: Outline — masking on every surface
**Traces to**: US-10 AC-1 · **Category**: Error Path
- **Given** a provider message `<echo>`
- **When** it is shown on `<surface>`
- **Then** `<secret>` never appears and `[redacted]` does

(Examples: dataset DS-SEC × surfaces main line, details, centre body, log line, replay.)

### Feature: removals

#### S-95: Removal only where the page shows the outcome
**Traces to**: US-11 AC-1, AC-2 · **Category**: Happy Path
- **Given** each audited call site with recommendation "remove + add page indicator (with Retry)"
- **When** its action fails
- **Then** the page shows the indicator (with Retry where specified) and no pop-up appears

#### S-96: Undo has a page home
**Traces to**: US-11 AC-3 · **Category**: Happy Path
- **Given** a calendar event moved by drag
- **When** the move completes
- **Then** an Undo control is available on the page for at least 10 s (FQ-15 home) and no pop-up holds it

#### S-97: No approval modal
**Traces to**: US-11 AC-4 · **Category**: Happy Path
- **Given** the built app
- **When** searching the bundle and DOM for the approval modal
- **Then** it is absent; approvals render only inline

---

## 19. Test-driven development plan

Tests are written **before** code by the one qa-lead RED author; CHECK by a different instance. Local runs follow root `CLAUDE.md` (one narrow tagged Go test at a time; one vitest file at a time; Playwright one spec at a time; CI is the authority).

### 19.1 Test implementation order

| # | Test name | Level | Traces to | What it proves |
|---|---|---|---|---|
| T-01 | `pkg/notifications::TestRouterRejectsUnknownKind` | Unit | S-06 | closed enum |
| T-02 | `pkg/notifications::TestRouterMintsIDWhenStoreFails` | Unit | S-07 | #645 Defect 1 closed |
| T-03 | `pkg/notifications::TestRouterAppliesPreferences` (table: global, workspace override, system-wide global only, locked, notify-off) | Unit | S-02, S-31, S-32, S-36 | resolution order |
| T-04 | `pkg/notifications::TestRetentionSweepAgeBoundary` | Unit | S-20, S-25 | 30 days incl. quarantine files |
| T-05 | `pkg/notifications::TestCapNeverEvictsUnresolvedNeedsYou` | Unit | S-21 | eviction rule |
| T-06 | `pkg/notifications::TestDeleteAndClear` (waiting → 409 equivalent error; clear keeps waiting) | Unit | S-22, S-23, S-24 | real deletion |
| T-07 | `pkg/notifications::TestResolveLifecycle` (created/updated/read/resolved, coalesce on unresolved, re-open within 60 s) | Unit | S-13, S-04 | lifecycle |
| T-08 | `pkg/notifications::TestRecipientFileNameCollisionFree` | Unit | S-08 | sanitise |
| T-09 | `pkg/notifications::TestPreferencesFileCorruptUsesDefaults` | Unit | S-35 | degraded mode |
| T-10 | `pkg/providers::TestFailureClassDataset` (DS-NET) | Unit | S-80 | network vs API table |
| T-11 | `pkg/providers::TestRetryDecisionsUnchangedByClass` (existing fallback datasets re-run, outcome equality) | Unit | S-85 | FR-063 |
| T-12 | `pkg/agent::TestProviderSaysExtraction` (DS-EXTRACT) | Unit | S-50, S-56 | extraction rule |
| T-13 | `pkg/agent::TestProviderSaysMasked` (DS-SEC) | Unit | S-90 | masking |
| T-14 | `pkg/agent::TestCatalogueMatchesWordingBaseline` (in-chat and outside-chat forms) | Unit | S-72, S-70 | baseline and both voices |
| T-15 | `pkg/agent::TestNetworkSplitCodes` (`network` vs `provider_error`) | Unit | S-51, S-80 | R-EV-2 |
| T-16 | `scripts/check-system-message-voice.sh` self-check fixture | Unit | S-71 | voice guard can fail |
| T-17 | `src/lib/detailLevel.test.ts` (every L1–L11 × level, DS-DL) | Unit | S-60 | one rule |
| T-18 | `src/components/chat/SystemLine.test.tsx` (5 types, in-progress, reduced motion, action, details by level, no empty quotes) | Unit | S-50, S-54, S-56, S-62 | renderer |
| T-19 | `src/lib/systemLineAdapters.test.ts` (one row per §11.2 carrier) | Unit | S-50…S-55 | mapping |
| T-20 | `src/store/notifications.test.ts` (merge-on-hydrate, change kinds, badge counts) | Unit | S-11, S-14, S-22 | store |
| T-21 | `src/components/ui/popupHost.test.tsx` (DS-PU durations, hover, cap, +N more, resolve removes) | Unit | S-40, S-41, S-42 | pop-up rules |
| T-22 | `pkg/gateway::TestNotificationRESTDeleteClearPrefs` | Integration | S-22…S-24, S-33, S-34 | REST contract codes |
| T-23 | `pkg/gateway::TestNotificationChangePushedToAllRecipientConnections` | Integration | S-10, S-11, S-22 | cross-tab push |
| T-24 | `pkg/gateway::TestEveryKindHasLiveProducer` (one subtest per Part-1 kind driving the real producer, reading `GET /notifications`) | Integration | S-05, S-01 | reachability |
| T-25 | `pkg/gateway::TestApprovalResolvedResolvesNotification` / `TestAskTerminalResolvesNotification` | Integration | S-10, S-12, S-13 | D19 |
| T-26 | `pkg/gateway::TestProviderEpisodeOneEntryAffectedChats` | Integration | S-04 | R-EV-3/4 |
| T-27 | `pkg/gateway::TestOutageReportValidationAndRateLimit` | Integration | S-81, S-82, S-83 | R-LOG-3 |
| T-28 | `pkg/gateway::TestSystemLineReplayParity` (live frame = replayed frame = history `Message.system_line`) | Integration | S-55 | persistence |
| T-29 | `pkg/agent::TestPerAttemptWarnLine` + `TestEpisodeStartEndLogged` (log capture at level `warn`) | Integration | S-80, S-84 | R-LOG-1/2/4 |
| T-30 | `pkg/gateway::TestScheduleFailedBodyHasNoRawError` | Integration | S-01, S-90 | masking + translation |
| T-31 | `tests/e2e/notifications-centre.spec.ts` (filters, navigate, pop-up action, delete, clear all, two tabs) | E2E | S-03, S-05, S-11, S-22, S-24, S-43 | user path |
| T-32 | `tests/e2e/notifications-needs-you.spec.ts` (approval in other tab, resolve everywhere, FQ-5 rule, no modal) | E2E | S-10, S-45, S-46, S-97 | D19, D30 |
| T-33 | `tests/e2e/notifications-preferences.spec.ts` | E2E | S-30, S-31, S-32, S-34 | preferences UI |
| T-34 | `tests/e2e/chat-system-lines.spec.ts` (fake provider: refused → recover; 401 echo; levels) | E2E | S-50, S-51, S-52, S-53, S-55 | system lines |
| T-35 | `tests/e2e/chat-detail-levels.spec.ts` | E2E | S-60, S-61, S-62 | detail levels |
| T-36 | `tests/e2e/own-action-results.spec.ts` (sampled sites per WP-5 category, Undo home) | E2E | S-44, S-95, S-96 | removals |

Every test must be shown red on the pre-change code (tests-only commit on CI, or one narrow local run) before GREEN — except the deletions (T-36's "no modal" part) which are shown red by asserting absence before removal.

---

## 20. Test datasets and regression

### 20.1 Test datasets

#### DS-NET — failure classification (T-10, T-29, S-80)

| # | Input (attempt outcome) | Boundary | Expected class / reason | Traces to |
|---|---|---|---|---|
| 1 | `dial tcp: lookup api.example.invalid: no such host` | network, DNS | network / dns_failure | S-80 |
| 2 | `dial tcp 127.0.0.1:9: connect: connection refused` | network | network / connection_refused | S-80 |
| 3 | `connect: network is unreachable` | network, local | network / network_unreachable (also internet signal, FQ-8) | S-80 |
| 4 | `tls: handshake failure` | network, TLS | network / tls_failure | S-80 |
| 5 | `net/http: timeout awaiting response headers` | network, timeout before response | network / timed_out | S-80 |
| 6 | `read: connection reset by peer` before headers | network | network / connection_dropped | S-80 |
| 7 | 200 then `unexpected EOF` mid-stream | FQ-7 boundary | network / connection_dropped (pending founder) | S-80 |
| 8 | 200 then 300 s silence (`provider_stalled`) | FQ-7 boundary | network / timed_out (pending founder) | S-80 |
| 9 | HTTP 408 | API min status in the 4xx timeout | api / http_status 408 | S-80 |
| 10 | HTTP 429 `rate limit exceeded` | API | api / http_status 429 (code rate_limited) | S-80 |
| 11 | HTTP 402 `Insufficient balance` | API | api / 402 (quota_billing) | S-80 |
| 12 | HTTP 500 empty body | API, no message | api / 500, provider_says absent | S-56 |
| 13 | HTTP 503 `{"error":{"message":"overloaded"}}` | API | api / 503, provider_says "overloaded" | S-80 |
| 14 | HTTP 529 | API, non-standard | api / 529 | S-80 |
| 15 | 200 + stream error event `{"type":"error","error":{"message":"Overloaded"}}` | API in-stream | api | S-80 |
| 16 | HTTP 400 `context_length_exceeded` | request-specific API | api (chat-only, no system-wide entry, FQ-6) | S-80 |
| 17 | Omnipus own rate limiter denial | not a provider attempt | no class, no provider log line | S-80 |

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
| 9 | 5,000-char message | stored ≤ 2,000 bytes, main line 300 code points + "…" | S-50 |
| 10 | message with `\u0000\u001b[31m` control chars | control chars removed | S-50 |
| 11 | `<b>bold</b>` | `<b>bold</b>` rendered as plain text | S-50 |
| 12 | `残高が不足しています` (non-English, multibyte) | unchanged, cut on a code-point boundary | S-50 |
| 13 | `{"error":{"message":"x"},"message":"y"}` | `x` (order of precedence) | S-50 |

#### DS-SEC — masking (T-13, S-90)

| # | Echo | Must not appear | Traces to |
|---|---|---|---|
| 1 | `Incorrect API key provided: sk-proj-abcdEFGH1234` | `sk-proj-abcdEFGH1234` | S-90 |
| 2 | `Incorrect API key provided: sk-or-v1-0123456789abcdef` | `sk-or-v1-0123456789abcdef` | S-90 |
| 3 | a registered credential value from the credential store, verbatim | that value | S-90 |
| 4 | `Authorization: Bearer eyJhbGciOi...` | the bearer token | S-90 |
| 5 | `sk-` + 4 chars (below the key-shape minimum) | (stays — boundary, documented) | S-90 |
| 6 | key split across a line break | both halves masked when registered | S-90 |

Surfaces per row: main line, technical details, centre body, log line, replayed history.

#### DS-PU — pop-up timing and cap (T-21)

| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | info at 4,999 ms | max−1 | visible | S-40 |
| 2 | info at 5,001 ms | max+1 | gone | S-40 |
| 3 | warning at 10,001 ms | max+1 | gone | S-40 |
| 4 | error at 3,600,000 ms | sticky | visible | S-40 |
| 5 | 0 pop-ups, 1 arrives | min | 1 visible | S-42 |
| 6 | 3 visible, 1 arrives | max+1 | 3 visible + "+1 more" | S-42 |
| 7 | 3 visible + 2 overflow, 1 resolved | release | 3 visible + "+1 more" | S-42 |
| 8 | hover 60 s on info | pause | visible during hover | S-41 |

#### DS-DL — detail levels (T-17, S-60)

All 33 cells of §12.2 (11 rows × 3 levels), each one test case; expected value = the table cell.

#### DS-OUT — outage reports (T-27)

| # | Report | Expected | Traces to |
|---|---|---|---|
| 1 | 40 s, server_unreachable | 204, one WARN | S-81 |
| 2 | 0 ms (start = end) | 204 (boundary), WARN with 0 | S-81 |
| 3 | end < start | 400 | S-82 |
| 4 | duration 7 d + 1 ms | 400 | S-82 |
| 5 | end = now + 6 min | 400 | S-82 |
| 6 | body 4 KiB + 1 byte | 400 `invalid_outage_report` | S-82 |
| 7 | 11th report within 60 s | 429 | S-83 |
| 8 | unauthenticated | 401 | S-82 |
| 9 | unknown field `note` | 400 (additionalProperties false) | S-82 |

### 20.2 Regression requirements

| Existing behaviour | Existing test(s) | New regression test |
|---|---|---|
| Coalescing by `CoalesceKey` | `pkg/notifications/store_test.go` | extended by T-07 |
| Knowledge-drift notification content and recipients | drift notifier tests in `pkg/gateway` | none new; must stay green unchanged except kind/category fields |
| Retry / fallback / cooldown outcomes | `pkg/providers/fallback_*_test.go`, `error_classifier_test.go` | T-11 asserts outcome equality |
| ADR-051 classification of codes | `pkg/agent/translate_error_test.go`, `translate_error_billing_test.go` | updated for `provider_error`; every other code's classification unchanged |
| Provider retry countdown, fallback note live behaviour | provider-messages tests | T-19 rows |
| Inline approval card and its resolution | `ToolApprovalPanel` tests, `tool_approval_resolved` gateway tests | T-32 |
| Session replay / history paging of system entries | `pkg/gateway/replay*` tests, chat-history-paging tests | T-28 |
| Stop behaviour (scope, 3 s window) | Stop tests | unchanged; only the notice surface moves (FQ-3) |

---

## 21. Functional requirements

| FR | Requirement | From |
|---|---|---|
| FR-001 | The system MUST accept only the §6 kind ids; a producer using another kind MUST fail without storing or pushing anything and MUST log an ERROR. | R-EV-1 |
| FR-002 | Every Part-1 kind MUST have a real producer wired at boot, proven by one integration subtest per kind that drives the producer and reads the entry through the REST list. | R-EV-1, DoD |
| FR-003 | The system MUST create a centre entry only when the owner's effective **Notify me** for the kind is on. | R-NC-1, R-PREF-1 |
| FR-004 | Every entry MUST carry a category; the centre MUST filter by All / Needs you / Tasks & plans / System-wide. | R-NC-2 |
| FR-005 | All producers MUST go through one router that resolves the owner (session owner, schedule creator, every account for system-wide), mints a non-empty id before any branch, never broadcasts when an owner is unreadable, and delivers live when storage fails. | R-NC-5, #645 |
| FR-006 | Every entry MUST carry a `target`; activating the entry or its pop-up action MUST navigate there. | R-NC-6, R-PU-4 |
| FR-007 | Every lifecycle change (created, updated, read, resolved, deleted, cleared) MUST reach every connection of the recipient within 2 s; every §6 resolve trigger MUST resolve the entry. | R-NC-3 |
| FR-008 | Entries older than 30 days MUST be deleted from storage at start-up and every 6 h, including quarantined corrupt files. | R-NC-4, R-SEC-2 |
| FR-009 | The count cap MUST never evict an unresolved Needs-you entry; deleting one MUST return 409 `notification_waiting`; Clear all MUST keep them. | R-NC-4 |
| FR-010 | The browser MUST merge a refetched list into its store by id, keeping live-only entries. | #645 |
| FR-011 | Provider failures MUST raise `provider_unreachable` for network-class and `provider_error` for in-scope API-class failures (FQ-6), as distinct kinds. | R-EV-2 |
| FR-012 | A chat MUST get a provider/connection line only if one of its turns was hit by the event. | R-EV-3 |
| FR-013 | A system-wide event MUST produce one coalesced entry and at most one pop-up per episode; a failure within 60 s of resolution re-opens the entry without a pop-up. | R-EV-4 |
| FR-014 | Preferences MUST be stored per user with global values and per-workspace overrides; resolution is workspace → global → default; System-wide kinds MUST be global only (400 otherwise). | R-PREF-1, R-PREF-2 |
| FR-015 | The two Needs-you kinds MUST have their pop-up switch locked on (and Notify locked on per FQ-4); attempts to turn them off MUST return 400 `notification_switch_locked`. | R-PREF-3, R-PU-2 |
| FR-016 | A missing preferences file MUST mean the §6 defaults; an unreadable one MUST apply defaults, log ERROR and show Retry on the preferences screen. | R-PREF-4 |
| FR-017 | Settings MUST have a Notifications tab and the workspace Settings tab MUST have a workspace override section, both saving with on-page results. | R-PREF-1, R-PREF-2 |
| FR-018 | Pop-ups MUST last 5 s (info/success), 10 s (warning), until dismissed or handled (error, needs_you); hover or focus MUST pause the countdown. | R-PU-1 |
| FR-019 | Needs-you pop-ups MUST always show, except in the tab where that chat is open and visible (FQ-5). | R-PU-2 |
| FR-020 | At most 3 pop-ups MUST be visible; more MUST collapse into "+N more — open notifications". | R-PU-3 |
| FR-021 | A pop-up MUST offer at most one main action, which navigates, plus close. | R-PU-4 |
| FR-022 | Results of the user's own actions MUST NOT raise a pop-up or a centre entry. | R-PU-5 |
| FR-023 | No event MUST open a modal; `ToolApprovalModal` and `INLINE_TOOL_APPROVALS_ENABLED` MUST be deleted. | R-PU-6, R-RM-2 |
| FR-030 | One `SystemLine` renderer MUST render every in-chat system message, through one carrier→line adapter module. | R-SL-1 |
| FR-031 | Lines MUST be single text lines with the five types' icon, colour token and weight per §11.1. | R-SL-2, R-SL-3 |
| FR-032 | Any line type MUST support an animated in-progress state that honours reduced motion. | R-SL-4 |
| FR-033 | A connection-problem line MUST show at every level while it lasts; on recovery it MUST disappear at Normal and leave one record line at Verbose/Debug. | R-SL-5 |
| FR-034 | Browser↔server loss MUST show one app-wide indicator and one line in the open chat, neither persisted. | R-SL-6 |
| FR-035 | A line MUST carry at most one action, which navigates or acts directly. | R-SL-7 |
| FR-036 | New server-authored lines MUST persist as `Message.system_line` entries and replay identically live, on reload and in paged history. | R-SL-1, R-SL-5 |
| FR-040 | A Normal/Verbose/Debug detail level MUST replace the Verbose switch; one rule function MUST decide every row × level per §12.2. | R-DL-1 |
| FR-041 | Technical details MUST render only at Verbose (collapsed) and Debug (open); the masked failure payload is the only raw payload at Verbose. | R-DL-2 |
| FR-042 | Every user MUST be able to select Debug. | R-DL-3 |
| FR-050 | The generated catalogues MUST equal the WORDING §1 sentences (with the `network` split and `provider_error`). | R-WD-1, R-WD-3, R-WD-9 |
| FR-051 | The provider's own message MUST be extracted per §13.3, masked, persisted, and quoted on the main line; a sentence MUST never render an empty slot or empty quotes. | R-WD-2, R-WD-3 |
| FR-052 | In-chat sentences MUST NOT use "Omnipus" (except D48), "we", "our", "us"; a CI guard MUST enforce it and be shown able to fail. | R-WD-5, R-WD-7 |
| FR-053 | Messages outside a chat MUST be neutral; a message used both inside and outside MUST have both forms. | R-WD-6, R-WD-8 |
| FR-054 | All remaining system-message wording MUST go through the founder review page before GREEN (FQ-13). | R-WD-4, R-WD-9 |
| FR-060 | Every failed provider attempt MUST log one WARN line with `failure_class`, `reason`, provider, model, status (API), masked provider message, attempt, next step. | R-LOG-1, R-LOG-2 |
| FR-061 | Failure class MUST follow the §14.1 table. | R-LOG-2, R-EV-2 |
| FR-062 | The browser MUST report each server outage on reconnect; the server MUST validate, rate-limit and log it at WARN; the server's unexpected-close line MUST be WARN. | R-LOG-3 |
| FR-063 | Retry, cooldown and failover decisions MUST be identical before and after this change. | safety |
| FR-064 | Provider episodes MUST log start and end (duration, attempts, affected chats) at WARN. | R-LOG-1, R-LOG-4 |
| FR-065 | `internet_lost`, `resources_low` and `concurrency_reduced` MUST be produced per FQ-8, FQ-9, FQ-10. | R-EV-1 |
| FR-070 | One masking function MUST be applied before persistence and before the wire to every provider-derived text, on every surface and level. | R-SEC-1 |
| FR-071 | Deletion MUST remove data from storage. | R-SEC-2 |
| FR-072 | Distinct usernames MUST map to distinct notification and preference files. | R-NC-5 |
| FR-073 | The outage endpoint MUST be authenticated, accept only enumerated fields, ≤ 4 KiB, ≤ 10/min/user. | R-LOG-3, security |
| FR-080 | Each audited own-action pop-up MUST be handled per its recommendation; removal only where the page shows the outcome (adding the indicator first, with Retry for failed saves). | R-RM-1 |
| FR-081 | Undo, the preview-link exposure warning and the cross-tab Switch MUST have page homes before their pop-ups are removed. | R-RM-1a |
| FR-082 | No badge MUST appear in the top-right corner. | R-RM-3 |
| FR-090 | Part 1 MUST NOT send anything outside the app; `publishChannelAlert` stays as is until Part 2 replaces it. | D62, R-EXT-12 |

---

## 22. Success criteria

| SC | Criterion |
|---|---|
| SC-001 | T-24 passes with one subtest per Part-1 kind (18 kinds, or 19 with `goal_paused` per FQ-2); 0 kinds without a producer. |
| SC-002 | In the two-tab E2E (T-32), a handled approval disappears from the second tab in ≤ 2 s in 20/20 runs. |
| SC-003 | Repo grep: 0 imports of `ToolApprovalModal`; 0 references to `INLINE_TOOL_APPROVALS_ENABLED`; 0 occurrences of `verboseChatEnabled`. |
| SC-004 | Own-action pop-up call sites after WP-5: ≤ 10 browser-local event sites (FQ-14) + 2 dev-only; every removed site has a matching page-indicator test or a "page already shows it" citation. |
| SC-005 | The voice guard passes on the real catalogues and fails on its planted fixture (exit code non-zero). |
| SC-006 | DS-SEC: 0 occurrences of any secret across the 6 rows × 5 surfaces (30 checks). |
| SC-007 | DS-NET: 17/17 rows classified as the table says; T-11 retry/fallback outcomes 100 % equal to pre-change. |
| SC-008 | With the default log level `warn`, a forced unreachable provider produces ≥ 1 "provider attempt failed" line with `failure_class=network` and one episode start and end line. |
| SC-009 | After a 30-day sweep, `notifications/` holds no entry and no quarantine file older than 30 days. |
| SC-010 | Every hands-on UAT row in §24 is PASS by the tester and upheld by the independent validator on the exact candidate commit. |

---

## 23. Traceability matrix — requirement → FR → scenario → test

| R-… | FR | Scenario(s) | Test(s) |
|---|---|---|---|
| R-NC-1 | FR-003 | S-01, S-02 | T-03, T-24 |
| R-NC-2 | FR-004 | S-03 | T-31 |
| R-NC-3 | FR-007 | S-10, S-11, S-12, S-13 | T-07, T-23, T-25, T-32 |
| R-NC-4 | FR-008, FR-009 | S-20, S-21, S-22, S-23, S-24, S-25 | T-04, T-05, T-06, T-22, T-31 |
| R-NC-5 | FR-005, FR-072 | S-07, S-08 | T-02, T-08, T-24 |
| R-NC-6 | FR-006 | S-05, S-43 | T-24, T-31 |
| (#645 Defect 2) | FR-010 | S-14 | T-20 |
| R-EV-1 | FR-001, FR-002, FR-065 | S-06, S-05, S-01 | T-01, T-24 |
| R-EV-2 | FR-011, FR-061 | S-51, S-80 | T-10, T-15 |
| R-EV-3 | FR-012 | S-04 | T-26 |
| R-EV-4 | FR-013 | S-04, S-13 | T-07, T-26 |
| R-PREF-1 | FR-003, FR-014, FR-017 | S-30, S-31, S-36 | T-03, T-33 |
| R-PREF-2 | FR-014, FR-017 | S-32, S-33 | T-03, T-22, T-33 |
| R-PREF-3 | FR-015 | S-34 | T-03, T-22, T-33 |
| R-PREF-4 | FR-016 | S-30, S-35 | T-09, T-33 |
| R-PU-1 | FR-018 | S-40, S-41 | T-21 |
| R-PU-2 | FR-015, FR-019 | S-34, S-46 | T-32 |
| R-PU-3 | FR-020 | S-42 | T-21 |
| R-PU-4 | FR-006, FR-021 | S-43 | T-21, T-31 |
| R-PU-5 | FR-022 | S-44 | T-36 |
| R-PU-6 | FR-023 | S-45, S-97 | T-32, T-36 |
| R-SL-1 | FR-030, FR-036 | S-50, S-55 | T-18, T-19, T-28 |
| R-SL-2 | FR-031 | S-50 | T-18 |
| R-SL-3 | FR-031 | S-50, S-51, S-52 | T-18 |
| R-SL-4 | FR-032 | S-51 | T-18 |
| R-SL-5 | FR-033, FR-036 | S-51, S-52 | T-28, T-34 |
| R-SL-6 | FR-034 | S-53 | T-34 |
| R-SL-7 | FR-035 | S-54 | T-18, T-34 |
| R-DL-1 | FR-040 | S-60, S-61 | T-17, T-35 |
| R-DL-2 | FR-041 | S-60, S-62 | T-17, T-18 |
| R-DL-3 | FR-042 | S-62 | T-35 |
| R-WD-1 | FR-050 | S-72 | T-14 |
| R-WD-2 | FR-051 | S-50, S-56 | T-12, T-18 |
| R-WD-3 | FR-050, FR-051 | S-50, S-72 | T-12, T-14 |
| R-WD-4 | FR-054 | (process: review page sign-off) | founder sign-off receipt before GREEN |
| R-WD-5 | FR-052 | S-70, S-71 | T-14, T-16 |
| R-WD-6 | FR-053 | S-70 | T-14 |
| R-WD-7 | FR-052 | S-71 | T-16 |
| R-WD-8 | FR-053 | S-70 | T-14 |
| R-WD-9 | FR-050, FR-054 | S-72 | T-14 |
| R-LOG-1 | FR-060, FR-064 | S-80, S-84 | T-29 |
| R-LOG-2 | FR-060, FR-061 | S-80 | T-10, T-29 |
| R-LOG-3 | FR-062, FR-073 | S-81, S-82, S-83 | T-27 |
| R-LOG-4 | FR-064 | S-84 | T-29 |
| R-SEC-1 | FR-070 | S-50, S-62, S-90 | T-13, T-30 |
| R-SEC-2 | FR-008, FR-071 | S-20, S-22, S-25 | T-04, T-06 |
| R-SEC-3 | Part 2 (§29) | P2-S-03 | Part 2 |
| R-SEC-4 | Part 2 (§29) | P2-S-06 | Part 2 |
| R-EXT-1 | Part 2 (§29) | P2-S-01 | Part 2 |
| R-EXT-2 | Part 2 (§29) | P2-S-02 | Part 2 |
| R-EXT-3 | Part 2 (§29) | P2-S-05 | Part 2 |
| R-EXT-4 | Part 2 (§29) | P2-S-04 | Part 2 |
| R-EXT-5 | Part 2 (§29) | P2-S-01 | Part 2 |
| R-EXT-6 | Part 2 (§29) | P2-S-10 | Part 2 |
| R-EXT-7 | Part 2 (§29) | P2-S-03 | Part 2 |
| R-EXT-8 | Part 2 (§29) | P2-S-07 | Part 2 |
| R-EXT-9 | Part 2 (later requirement) | — | — |
| R-EXT-10 | Part 2 (§29) | P2-S-08 | Part 2 |
| R-EXT-11 | Part 2 (§29) | P2-S-06 | Part 2 |
| R-EXT-12 | Part 2 (§29); Part 1 FR-090 | P2-S-09 | Part 2 |
| R-RM-1 | FR-080 | S-44, S-95 | T-36 |
| R-RM-1a | FR-081 | S-96 | T-36 |
| R-RM-2 | FR-023 | S-45, S-97 | T-36 |
| R-RM-3 | FR-082 | (UAT U14) | U14 |
| (safety) | FR-063 | S-85 | T-11 |
| D62 | FR-090 | — (negative: no outbound send) | T-24 asserts no `PublishOutbound` from the router |

Completeness check: every FR above appears in at least one row; every S-xx appears in at least one row (S-01…S-08, S-10…S-14, S-20…S-25, S-30…S-36, S-40…S-46, S-50…S-56, S-60…S-62, S-70…S-72, S-80…S-85, S-90, S-95…S-97).

---

## 24. Hands-on UAT rows (spec time; macOS; tester + independent validator)

Setup for every row: the exact candidate commit built and installed per the standing per-lane procedure; provider `openrouter` + model `deepseek/deepseek-v4.1-flash` (founder-set UAT model); tester and validator each on their own instance, account and private browser; screenshots show the workspace and the badge. The validator re-drives every PASS on its own instance before ruling and never confers with the tester first. Setup and version receipts are recorded separately from product PASS.

| Row | Steps | Expected | Evidence |
|---|---|---|---|
| U1 | Agent in chat A runs a command that needs approval (`bash` on Ask) while the user is on Settings in tab 1 and chat B in tab 2 | Needs-you pop-up in both tabs, sticky, one **Review** action; centre entry under Needs you; no modal | 2 screenshots + centre screenshot |
| U2 | Approve from chat A in tab 2 | Pop-up gone and entry "Handled" in tab 1 within ~2 s | screenshot pair with timestamps |
| U3 | Agent asks a question (ask-user card) in chat C; answer it from the centre's **Open** | Card answered, entry handled, pop-up gone | screenshots |
| U4 | Scheduled task (calendar, next minute) that succeeds; then one that fails (agent with an invalid model) | `schedule_finished` pop-up (5 s) and entry; `schedule_failed` sticky pop-up and entry; **Open** opens the run; body has no raw error text | screenshots + centre |
| U5 | Add a custom OpenAI-compatible provider with base URL `http://127.0.0.1:9/v1`; send a message with it | Animated amber line "I can't reach … : the connection was refused. Retrying in … s…"; one `provider_unreachable` entry + pop-up; after retries an Error line with **Switch model** | screenshots; gateway log lines with `failure_class=network` |
| U6 | Point the same chat back to OpenRouter (Switch model) and send | "reachable again" entry (pop-up 5 s); at Normal the old retry line is gone; switch to Verbose: one resolved record line | screenshots at Normal and Verbose |
| U7 | Copy the OpenRouter provider with an invalid key `sk-or-v1-0000000000000000invalid`; send | Error line `OpenRouter rejected the API key: "…"` with the key masked; **Open settings**; Debug view shows details with the key masked | screenshots at Normal/Verbose/Debug; log line `failure_class=api` with masked text |
| U8 | Settings → Notifications: turn **Task or plan failed** Notify off; make a task fail | No entry, no pop-up; the task chat still shows the failure | screenshots |
| U9 | Workspace Settings → override **Task or plan started** On; start a task there and in another workspace | Entry only for the overridden workspace; System-wide kinds not offered in the override | screenshots |
| U10 | Try to switch off the pop-up of **Agent needs your approval** | Switch locked with explanation | screenshot |
| U11 | Trigger 5 info events quickly (e.g. 5 short scheduled tasks finishing) | 3 visible + "+2 more — open notifications"; hover pauses; non-hovered vanish after 5 s | screen recording or timed screenshots |
| U12 | Stop the gateway for 20 s (`launchctl`/process stop), then start it | App-wide indicator + chat line during the outage; both gone after; reload: not in history; gateway log has "client connection outage" with ~20,000 ms | screenshots + log excerpt |
| U13 | Turn macOS Wi-Fi off for 15 s, then on | "No internet connection. Your agents keep working." line + indicator; outage logged with `kind=offline` | screenshots + log |
| U14 | Visual sweep of every main screen (chat, workspace tabs, settings, library, mail) | No badge in the top-right corner; unread count only in the sidebar account menu | screenshots |
| U15 | Save settings on 5 screens (Performance, Chat, Profile, Providers, workspace Settings), once succeeding, once failing (disconnect the server mid-save) | No pop-up; on-page "Saved" / "Not saved — Retry" | screenshots |
| U16 | Delete one centre entry, then Clear all, with an approval still waiting | Entry gone in both tabs; Clear all keeps the waiting approval | screenshots |
| U17 | Detail level Normal/Verbose/Debug on one chat with tool calls, a delegation and a failed reply | Rows per §12.2 (validator checks the table cell by cell for the rows present) | 3 screenshots |
| U18 | Read the chat lines of U5/U7 and the centre entries | Chat lines say "I …"; centre/pop-ups neutral; no "we/our/us"; no "Omnipus" as actor | screenshots |
| U19 | Calendar: drag an event to a new time | Undo available on the page (FQ-15 home), no pop-up | screenshot |

Rows U1–U3, U5–U7, U12 touch `pkg/agent` / gateway WebSocket handling and are mandatory before landing (root `CLAUDE.md`). Stable rows become E2E tests over time (T-31…T-36 already cover U1–U3, U8–U11, U15–U17 in automated form).

---

## 25. User-facing documentation TODOs (root `CLAUDE.md`, Definition of Done)

Drafted by the implementing leads in the same change; audited by `docs-verifier` against the built behaviour.

| Page | TODO |
|---|---|
| `docs/using-omnipus-ui.md` | New section "Notifications": centre location, categories, Handled state, delete/Clear all, 30-day retention; pop-up rules (durations, three at a time, one action); system lines (five types, in-progress, actions, technical details); browser connection indicator. Replace every "Verbose chat" mention (today around the approval, tool-activity and failure paragraphs) with "Detail level" and the §12 table in plain words. Correct the approval paragraph that still says pending requests appear "one at a time over the app" (inline approvals are the only surface). Provider failures: the provider's own message is shown, masked; details at Verbose/Debug; remove the "provider response details are live-only" caveat where `provider_says` now persists |
| `docs/settings.md` | New "Notifications" tab (switches, locks, defaults table); Chat → **Detail level** replacing Verbose chat; workspace notification override |
| `docs/workspaces.md` | Workspace Settings → "Notifications for this workspace" |
| `docs/providers-and-models.md` | How provider failures read now: network ("can't reach") vs API ("answered with an error"), quota, rate limit; what the centre shows for a provider outage |
| `docs/troubleshooting.md` | "Provider unreachable vs provider error" decision help; where the log lines are; browser outage lines |
| `docs/operations/observability.md` | Log lines "provider attempt failed", episode start/end, "client connection outage", raised "connection closed unexpectedly" — fields, level `warn`, how to grep network vs API |
| `docs/protocol/websocket-protocol.md` | `notification` frame new fields (`category`, `change`, `popup`, `target`, `resolved_at_ms` …), `system_line` frame, `provider_retry` new fields, `LLMError.provider_says` / `failure_class` |
| `docs/goals.md`, `docs/tasks.md` | Which goal/task events notify by default and how to change it |

No user-visible effect, so no page: the router refactor, file locking, the recipient file-name hash.

---

## 26. Integration boundaries

### AI providers (via the existing provider layer)
- **Data in/out**: requests out; responses, HTTP status, error bodies, stream error events in.
- **Contract**: unchanged; this feature only reads failures.
- **On failure**: classified network/API (§14.1); retries and fallback unchanged (FR-063).
- **Development**: a local fake OpenAI-compatible server for tests (refuse, 401 echo, 402, 429, 503, mid-stream drop, stall); real OpenRouter only in UAT.

### Browser (SPA) ↔ gateway
- **Data in/out**: REST (notifications, preferences, outage report), WS frames (`notification`, `system_line`, `provider_retry`, `error`).
- **On failure**: SPA keeps live state; refetch merges; outage report retried once then dropped.
- **Development**: real gateway in integration/E2E tests.

### Operating system (memory, network signals)
- **Data in**: memory pressure via `config.MemoryPressureHigh`; `network is unreachable` errors.
- **On failure**: unmeasurable memory = no `resources_low` notification (FQ-10).
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
| H6 | Edge | Laptop sleeps 2 h during a running reply, wakes | Open the app | One outage report logged with ~2 h duration; reply state truthful; no stale pop-ups |
| H7 | Edge | Two browsers on the same account, one on Debug, one on Normal | Same failing chat | Each shows its own level; handling in one resolves in the other |


---

## 28. ADR-051 amendment (proposed text — D36 settles it; ADR-051 itself is not edited by this spec)

Target file: `docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md` (**ADR-051 — Provider-Capability-Aware Media Handling and User-Facing Error Translation**). To be appended by the architect as a dated correction when this spec is approved; the text below is exact.

> ### Amendment 2026-10-30 — the provider's own message on the main line (founder decisions D28, D36; notifications spec)
>
> **Status:** Accepted by founder decision D36 (2026-10-22); takes effect with `docs/internal/specs/notifications-spec.md`. Supersedes RD5's sentence "Raw provider errors never persist and never cross to the SPA in production" **for one bounded field only**, RD7's display rule, and the operator decision Q2 of 2026-07-21 for that field. Also supersedes founder decision D1 of `docs/internal/specs/provider-messages-spec.md` for the same field. RD1–RD4, RD6 and the two-choke-point architecture are unchanged.
>
> **Context.** The founder's message review (2026-10-22) found that hiding what the provider said makes failures harder to act on: a provider's own words ("Insufficient balance", "Incorrect API key provided") are often the most useful part of the message (D26). ADR-051's fixed sentences were written to keep raw provider text off the wire after an incident where a full raw JSON body reached the chat. The incident's harm was the *raw body*, not the provider's *message*.
>
> **Decision.**
> 1. **`provider_says`.** The error shape gains one optional field: the provider's own error message, *extracted* from the response (a fixed list of JSON message locations, else a text body's first line, else a stream error event's message) — never the raw body. It is stripped of control characters, **masked** with the same credential masker as `detail` (registered credentials and key-shaped tokens), capped at 2,000 bytes, **persisted** in the transcript at the write choke point (`appendErrorTranscript`) and **replayed**, so a reloaded chat reads the same as the live one.
> 2. **Main line.** The user-facing sentence quotes `provider_says` on the main line at every detail level, next to a short sentence of ours (what happened · why · what to do). When no provider message exists, the sentence omits the quote; it never shows empty quotes and never invents provider text.
> 3. **Catalogue.** ADR-051's fixed sentences are replaced by the founder-agreed wording baseline (`docs/internal/specs/notifications-and-chat-system-messages-wording-2026-10-22.md` §1). A fixed sentence of ours stays only where it adds an action (for example "switch model", "update the key in Settings → Providers"). Each catalogue entry carries up to three explicit sentences: no facts; provider and model known; provider, model and provider message known. The single-source catalogue and both generators remain the only place copy lives.
> 4. **Network vs API.** The `network` code is split: `network` now means the provider could not be reached at all (DNS, refused, unreachable, TLS, timeout before a response); the new code `provider_error` means the provider answered with an error (408, 5xx, 529 and other provider-side faults). The error shape gains `failure_class` (`network` · `api`). Retry and fallback decisions do not read either.
> 5. **`detail` unchanged in kind.** The raw `detail` (status and bounded, masked body preview) stays live-only and is still never persisted. It renders only at the Verbose (collapsed) and Debug (open) detail levels, which replace the Verbose-chat switch.
> 6. **Voice.** In a chat, the product never speaks of itself as the actor: "I could not …" (the agent's perspective); outside a chat, neutral wording (D47, D50). The copy rule "no message tells the user to contact support" is relaxed by exactly one sentence: "please report it to the Omnipus.ai team" (D48).
>
> **Consequences.**
> - *Positive:* failures say what the provider said; reload parity for the message; the network/API distinction is visible to the user and in logs.
> - *Negative:* provider-authored text (masked) is now stored in transcripts. A content-policy refusal may echo part of the user's own prompt — the transcript already holds that prompt, so the sensitivity class does not change. External notifications never carry it (notifications spec R-SEC-3).
> - *Neutral:* the masker becomes a shared function used on more surfaces; RD5's "raw never lands on disk" still holds for the raw body.
>
> **Alternatives considered.** (a) Keep provider text Verbose-only (provider-messages spec D1) — rejected by D28: the useful part is hidden from most users. (b) Persist the raw body masked — rejected: the 2026-07-21 incident class (a full JSON blob in the chat) returns. (c) Classify every provider message into a fixed sentence — rejected by D28 ("not force-classified").
>
> **Affected components.** `contracts/components/schemas/LLMError.yaml` (and its replay shapes), `pkg/agent/translate_error.go`, `pkg/agent/translate_error_provider_messages.go`, `pkg/providers/error_classifier.go`, `src/lib/llm-error.ts`, the chat error rendering.

---

## 29. Part 2 — external delivery (second step; not in this build, D62)

Described to the depth Part 1 needs; Part 2 gets its own grill round before its build. Requirements R-EXT-1…12, R-SEC-3, R-SEC-4 apply verbatim (REQ §5.10, §5.9).

### 29.1 Design outline

| Topic | Outline |
|---|---|
| Notification channel (R-EXT-1, R-EXT-3, D58, D61) | A channel configuration entry with a "notifications" purpose. The channel screens show its owner as **Notification System** (workspace) / **Notification Bot** (agent) — presentation only: no workspace, no agent, no sidebar entry, no model run |
| Recipients (R-EXT-4, D56) | The channel's existing Allow From list (`allow_from`) — mandatory and non-empty for a notification channel; enabling with an empty list is refused. Telegram-style "write to the bot first" hints in setup |
| Channel types (R-EXT-2, D58) | Every existing channel type, plus **email as a channel type** for notifications with its own sender settings (mail server, sender address) — not an agent mailbox (F13) |
| Third preference switch (R-PREF-1) | **Also send externally** joins `NotificationKindPreference` as `external` (reserved name); default per §6 "Ext." column, all **off until the user opts in** (R-SEC-4, R-EXT-11) |
| Router step 7 | After step 6, if `external` is on: enqueue for every enabled notification channel, each Allow-From recipient (R-EXT-5, D55) |
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

---

## 30. Founder questions

Each is written into the spec with the recommended option marked **pending founder**; the grill can run before the answers. Answer as "FQ-1 A, FQ-2 B, …".

**FQ-1 — Keep the pop-up triage (328 call sites) in this spec's single gate?**
Context: removing own-action pop-ups (R-RM-1) touches 101 files and needs a page indicator first for about 200 of them. It shares only the rule with the rest of the feature. Impact: review size and landing speed of the core feature.
- **A (recommended)**: same spec and gate, but its own work package and owner, allowed to ride a later train than the core.
- B: separate spec and gate after the core lands.
- C: everything in one work package and one landing.

**FQ-2 — Where does the existing "goal paused" notification belong?** (landed 2026-10-25 in `f24785784`, after the requirements were captured)
Context: it fires when a goal waits for the user or is blocked. Impact: whether it pops up always and how it can be switched off.
- **A (recommended)**: a third Needs-you kind ("Goal paused — waiting for you or blocked"); pop-up always.
- B: Tasks & plans kind, default Notify On / Pop-up On, switchable.
- C: remove it from the centre; the chat line is enough.

**FQ-3 — The existing Stop-outcome notice in the centre conflicts with D31.**
Context: "Stop was incomplete / could not finish / some requests were already approved" are results of the user's own Stop. Impact: whether the centre shows own-action results.
- **A (recommended)**: move it to a Warning line in the chat where Stop was pressed; remove the `stop_notice` kind.
- B: keep it in the centre as an exception.

**FQ-4 — Lock "Notify me" too for the two Needs-you kinds?**
Context: R-PREF-3 locks only the pop-up. With Notify off, a pop-up would exist without a centre entry and "+N more — open notifications" would lead nowhere. Impact: one more locked switch.
- **A (recommended)**: lock both switches on for Needs-you kinds.
- B: lock only the pop-up; Notify may be turned off.

**FQ-5 — Needs-you pop-up while that very chat is open, and the 2026-10-18 ruling.**
Context: on 2026-10-18 you ruled "no 'waiting in another chat' notice; the sidebar cue is the signal". D30 (2026-10-22) makes Needs-you always pop up. Impact: whether D30 replaces that ruling, and whether a pop-up shows on top of the inline card.
- **A (recommended)**: D30 replaces the 2026-10-18 ruling; no pop-up in the tab where that chat is open and visible (the inline card is the surface); every other tab pops up.
- B: always pop up, even over the open chat.
- C: keep the 2026-10-18 ruling; no Needs-you pop-ups at all.

**FQ-6 — Which API failures count as the system-wide "Provider returned an error"?**
Context: many API errors are about one request (too long, content policy, bad tool input), not about the provider's health. Impact: how noisy the centre is.
- **A (recommended)**: only provider-side faults that affect every chat: 401/403 (key rejected), 408, 5xx/529, retired model; request-specific errors stay chat-only.
- B: every API error.

**FQ-7 — A connection that drops after the provider started answering, or a provider that goes silent: network or API?**
Context: the definitions cover "never reached" (network) and "answered with an error" (API); a mid-answer drop is neither. Impact: wording and which centre kind fires.
- **A (recommended)**: network ("connection dropped" / "did not answer in time").
- B: API.
- C: a third class "interrupted".

**FQ-8 — How to detect "Omnipus lost its internet connection" without extra traffic?**
Context: no detector exists. Impact: false alarms vs an outbound check.
- **A (recommended)**: the operating system reports "network unreachable", or two different provider hosts fail with network errors within 60 s with no success in between; no probe traffic. A single-provider install will usually see "provider unreachable" instead.
- B: an active check against a configurable address.
- C: drop the kind; "provider unreachable" covers it.

**FQ-9 — "System resources low": disk.**
Context: memory has a shared check; disk has none. Impact: new monitoring code.
- **A (recommended)**: memory from the existing check (on the change to "high"); disk only when a write to the data folder fails for lack of space.
- B: add a periodic free-space check (for example below 1 GB or 5 %).
- C: memory only.

**FQ-10 — "Concurrency reduced" where memory cannot be measured (Windows, some containers).**
Context: there the limit of 2 is always on. Impact: a permanent notification on those hosts.
- **A (recommended)**: notify only on a measured change; on unmeasurable hosts show one permanent line in Settings → Performance, no notification.
- B: notify once at each start.

**FQ-11 — Two readings of the detail-level table.**
Context: (a) today some background calls (delegate status polls, tool loading) are hidden at non-Verbose; L6 says "every other tool call: compact line" at Normal. (b) the fallback note ("answered by X because Y was unavailable") is persisted and always visible today (provider-messages PM-4); L8 says "while happening only" at Normal. Impact: noise at Normal vs the earlier decision.
- **A (recommended)**: (a) keep today's hidden set hidden at Normal, compact line for the rest; (b) the fallback note hides at Normal after the reply ends and stays at Verbose/Debug (literal L8).
- B: (a) literally every call as a compact line; (b) keep the fallback note always visible.

**FQ-12 — Notification count limit.**
Context: today the newest 50 are kept; R-NC-4 adds 30 days and protects waiting requests. Impact: whether a busy user loses older handled entries before 30 days.
- **A (recommended)**: keep the 50-entry limit as a safety bound, never evicting a waiting Needs-you entry.
- B: remove the count limit; 30 days only.

**FQ-13 — The 163 messages without a wording proposal (R-WD-9 says "rewritten during the spec").**
Context: writing 163 rewrites inside the spec without the review page loses your rating step; many are pop-ups that WP-5 removes anyway. Impact: spec size and your review effort.
- **A (recommended)**: after the pop-up triage list is fixed, rewrite the surviving ones on the existing review page; you approve them before GREEN; the spec keeps the rules and the voice guard.
- B: the spec author writes all 163 into a spec appendix now.

**FQ-14 — Browser-only event pop-ups (the audit's 10 "keep" sites: new version available, page cannot read server data, unknown server events, live-input actions lost, large history loading …).**
Context: they are not server events, so they are not in the catalogue. Impact: whether they appear in the centre and preferences.
- **A (recommended)**: stay pop-ups under the pop-up rules; not in the centre; no preference; a fixed list.
- B: become catalogue kinds (centre + preferences).
- C: become page indicators only.

**FQ-15 — Homes for functions that today live only in a pop-up, and the audit's other founder-decision rows.**
Context: R-RM-1a. Impact: UI changes on four screens.
- **A (recommended)**: Undo on a moved calendar event / added card → an inline "Moved · Undo" on the item for 10 s; preview-link exposure warning → permanent text under the Copy link button; cross-tab Switch → a banner on the source page with **Switch**; assignee warning after saving a task → a persistent warning on the task card; replay-warning operator diagnostic → a Verbose-level system line.
- B: keep these as pop-ups (named exceptions to R-PU-5).

**FQ-16 — One new chat-line carrier, or migrate all system messages to it?**
Context: about a dozen existing frame types already carry structured system messages (retry, fallback, rate limit, errors, goal outcomes …). Impact: churn in replay and history paging vs one uniform wire format.
- **A (recommended)**: keep the existing frames and map them all to the one renderer; use the new `system_line` carrier only for lines that have none today.
- B: migrate every system message to `system_line`.

**FQ-17 — Debug row L11: raw provider request/response of calls that did not fail.**
Context: nothing like this is sent to the browser today (only per-reply totals and the failure details). Building it means capturing every provider request and response, masking it and sending it to the browser at Debug. Impact: a sizeable new data path; a deferral needs your approval with a tracked issue (Hard Constraint #7).
- **A (recommended)**: Debug shows everything already on the wire (internal ids, request ids, failure payloads open); raw non-failing payloads are deferred to a tracked issue.
- B: build the capture now, in this feature.

---

## 31. Ambiguity warnings, assumptions, accidental findings

### 31.1 Ambiguity warnings

| # | Ambiguous | Likely agent assumption | Resolved by |
|---|---|---|---|
| A1 | "Handled" for informational entries (e.g. task finished) | Reading it = handled | §8.2: read ≠ resolved; informational entries never need resolving; "Handled" applies to Needs-you and condition entries |
| A2 | "Affected chat" for a provider event | Every chat using that provider | §6 rule + FR-012 (only chats whose turn was hit) |
| A3 | Whether pop-up preference applies without Notify | Independent switches | §9.1 ("also") |
| A4 | Where detail level is stored | Server-side per user | §12.1: per device, like today |
| A5 | Restored notification for a 5-second blip | Always notify | §6 60-s re-open rule |
| A7 | Kind of a scheduled task's run | Both task and schedule kinds | §6: schedule kinds only |
| A8 | System-wide recipients with multiple accounts | First account | every account (D24-ready); single user today |
| A9 | **L11 "raw provider request/response of non-failing calls"** at Debug — no such data is on the wire today (only `DoneStats` and failure `detail`) | Build a capture | FQ-17 |

### 31.2 Assumptions and defaults chosen by the author (grill targets)

- Retention sweep cadence 6 h; re-open window 60 s; provider message cut 300 code points on the line, 2,000 bytes stored; outage reports 10/min/user, ≤ 4 KiB, ≤ 7 days.
- Delete of an unresolved Needs-you entry is refused (409) — it leaves only by being handled.
- The app-wide browser connection indicator reuses the `AppShell` banner area.
- Preferences and notifications stay file-based per user (no new storage technology; Hard Constraint #1).
- Detail level is per device; Debug is not persisted per user.
- Kind ids are English snake_case and never shown to users.

### 31.3 Accidental findings (notes to team-lead — not fixed here)

| # | Finding | Evidence | Certainty |
|---|---|---|---|
| AF-1 | A test file marked "SCRATCH DRAFT ONLY" is committed in `pkg/notifications` (`goal_pause_b1_store_contract_test.go`, header "SCRATCH DRAFT ONLY. Intended primary: …") | file read at `4cad412bf` | Verified |
| AF-2 | `system_overload` frame has a consumer pop-up but no producer anywhere | `pkg/gateway/websocket_forward.go` comment; grep | Verified (no producer) |
| AF-3 | `docs/using-omnipus-ui.md` says pending approvals "appear one at a time over the app", while inline approvals are on and the app-wide modal is not mounted | doc text; `src/lib/featureSwitches.ts` | Inferred (UI not driven) |
| AF-4 | Notifications list is empty for `_dev_bypass`, `_env_token`, `cli` identities (#645) | #645 text; not re-run | Unknown (not re-verified) |
| AF-5 | Notification REST handlers live in `pkg/gateway/schedules.go`, not in a notifications file | file read | Verified (structure note) |

### 31.4 Clarifications log

- 2026-10-30 — Q: does the external-delivery part make one spec unreviewable? → A (author): only if built together; Part 2 is described, not built (§1.3). FQ-1 covers the pop-up triage split.
- 2026-10-30 — Q: which ADR-051? → A: "Provider-Capability-Aware Media Handling and User-Facing Error Translation" (the ADR-051 number has three files).

