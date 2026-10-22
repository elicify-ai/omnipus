# Notifications and chat system messages — requirements

**Status:** Requirements (Draft) — not yet a spec, not an ADR (decision D9)
**Date:** 2026-10-22
**Author:** team-lead, from a founder interview and a founder review page
**Captured at commit:** `c90310f1f` (`release/v0.1.1`)
**Sources:** interview rounds 1–7 (2026-10-22); founder review page
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/review.json` (saved 2026-10-22 08:50Z); founder message review
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/messages-review.json` (saved 2026-10-22 11:34Z)
**Related issues:** #645 (notification centre hardening: unify with toasts) — this document supersedes its open question; #177 (notification-prefs for omnipus-mobile) — R-PREF requirements must stay compatible.

Requirement IDs (`R-…`) are stable; decision IDs (`D…`) refer to the Decisions Log at the end.

---

## 1. Problem

Omnipus tells the user about events through several unrelated mechanisms that grew one at a time — the founder calls it patchwork:

| Surface today | Where | What feeds it | Problem |
|---|---|---|---|
| Pop-ups (toasts) | bottom-right corner | 322 direct call sites in 101 frontend files, each choosing its own wording, duration and actions | no shared rule for what deserves a pop-up, how long it stays, or what it offers |
| Notification centre | opened from the user menu | only two event types exist: `schedule_failed`, `knowledge_drift` | looks unused; misses everything an agent or the system does |
| Sidebar attention cue | sidebar, per workspace / main chat | `attentionCue.ts` (“N main chats need your attention”) | works; kept as is (D33) |
| Chat system messages | inside the chat | connection status, provider errors (ADR-051 translations), stop/rate-limit frames — rendered by separate components | inconsistent look; some wording poor; some messages say “something happened” without saying what |
| Verbose chat | one on/off setting | 18 chat components each check the switch themselves | no single rule for what each level shows |

Goal of this work: **one coherent model** for (a) which event notifies the user and where, and (b) how any system-generated message looks and reads inside the chat, at three detail levels.

## 2. Current state (verified at `c90310f1f`)

| Fact | Evidence |
|---|---|
| Pop-up container is fixed bottom-right | `src/components/ui/toast-container.tsx::ToastContainer` |
| 322 non-test pop-up call sites (`addToast`/`showToast`/`pushToast`) in 101 files | grep over `src/`, excluding tests and stories |
| Notification types: `schedule_failed`, `knowledge_drift` only | `contracts/components/schemas/Notification.yaml` (`type` enum) |
| Producers: scheduled-run failure, knowledge-index drift | `pkg/gateway/schedules.go` (`r.notifs.Create`), `pkg/gateway/knowledge_drift_notify.go::knowledgeDriftNotifier` |
| Retention: newest 50 per user, no age limit | `pkg/notifications/store.go::notificationCap` |
| Notifications are stored per recipient (username) | `pkg/notifications/store.go` (`Recipient` field, one file per recipient) |
| Sidebar attention cue exists (“N main chats need your attention”) | `src/components/layout/sidebar/attentionCue.ts::attentionCountLabel` |
| Browser offline / server unreachable copy exists | `src/components/chat/ConnectionStatus.tsx` (“No internet connection. Your agents keep working.”) |
| Browser↔server unexpected close is logged only at debug level | `pkg/gateway/websocket.go` — `slog.Debug("ws: connection closed unexpectedly", …)` |
| The browser has no way to report client-side events to the server log | no such endpoint in `contracts/openapi.yaml` |
| Provider failures are classified for fallback (`auth`, `rate_limit`, `billing`, `timeout`, `format`, `bad_model`, `context_overflow`, `overloaded`, `unknown`); transport failures (`connection refused`, `no such host`, `dial tcp`, `tls handshake` …) are matched as transient | `pkg/providers/types.go::FailoverReason`, `pkg/providers/error_classifier.go` |
| No reason value separates “provider unreachable (network)” from “provider answered with an error (API)” | same — 🔶 inferred (medium): transport failures fold into a transient/timeout class |
| Provider retries/refusals logging | 🔶 partial: several retry paths log at warning level (`pkg/agent/loop_run_turn_response.go` — stream reset, timeout, transport drop; `translate_error.go::defaultUserMessage` — unrecognised code), but there is no consistent per-attempt record and no network-vs-API field |
| ADR-051's founding rule is “raw provider text never reaches the wire” | `pkg/agent/translate_error.go` file header; catalogue of 29 fixed sentences in `contracts/components/schemas/LLMError.yaml` (`x-user-messages`), only 7 with a provider/model template |
| One code (`network`) covers both unreachable provider and HTTP 408/5xx | `translate_error.go` — `CodeNetwork` comment “408 / 5xx / timeout / connection drop” |

### 2.1 Pop-up audit (2026-10-22, all 322 call sites)

Source: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/toast-audit/result-w1..w4.jsonl` (one record per call site; counts re-verified by team-lead per file group; 8/8 sampled “page already shows it” claims confirmed — `teamlead-spotchecks.md`).

| Measure | Result |
|---|---|
| Origin | 291 own-action results · 16 connection · 11 system · 2 agent · 2 developer-only |
| Page already shows the same outcome | yes 98 · partial 50 · no 174 |
| Recommendation | remove (page shows it) 98 · remove + add page indicator 107 · remove + add indicator with Retry 87 · keep as event pop-up 10 · move to chat line 8 · founder decision 7 · delete 3 · developer-only 2 |
| Wording | raw error passthrough 57 · missing next step 34 · technical 22 · vague 12 · other 28 |
| Reusable page patterns | `src/components/ui/AutoSaveIndicator.tsx`; agent-profile “Save not confirmed + Retry”; error cards with Retry (`ChatImage`, `IframePreview::ErrorBlock`); `AppShell` connection banner with Retry |
| Pop-ups that hold the only copy of a function | Undo on a moved calendar event / added card; the shared-preview-link exposure warning; the cross-tab “Switch” button |

## 3. Goals and non-goals

**Goals**
- One routing rule: every event kind has a defined destination (centre, pop-up, chat line, sidebar, later external).
- One in-chat system-message mechanism and style for server- and browser-originated messages.
- One detail-level rule (Normal / Verbose / Debug) every chat component follows.
- Honest, specific wording that shows the provider's own message instead of hiding it.
- Complete logging of connection problems, with network and API failures told apart.

**Non-goals (this round)**
- Detailed design and delivery of external delivery (WhatsApp, email …) — requirements only (D11).
- Multi-user — Omnipus is single-user today; the model must not block bringing it back (D24).
- An ADR — follow-up only (D9, D36).
- Changes to the sidebar attention cue (D33).

## 4. Vocabulary

| Term | Meaning |
|---|---|
| **Event kind** | A named type of event (e.g. “Scheduled task failed”). Preferences and routing are set per kind. |
| **Notification** | An entry in the notification centre for an event kind the user opted into. Not a log. |
| **Pop-up** | A transient message in the bottom-right corner (today's “toast”). |
| **System line** | A one-line, system-generated message inside a chat (not a card). |
| **Detail level** | Normal, Verbose or Debug — decides which chat content is shown. |
| **Network failure** | Omnipus cannot reach the provider at all (DNS, refused, unreachable, TLS, timeout before a response). |
| **API failure** | The provider was reached and answered with an error (e.g. 402 insufficient balance, 429 rate limit). |

## 5. Requirements

### 5.1 Notification centre

| ID | Requirement | Decisions |
|---|---|---|
| R-NC-1 | The centre holds notifications only for event kinds the user opted into. It is not a log — logs and transcripts record everything. | D1 |
| R-NC-2 | Entries are filterable by category: **Needs you**, **Tasks & plans**, **System-wide**. | D1 |
| R-NC-3 | Handling an item anywhere (chat, centre, pop-up) resolves it everywhere: centre entry, unread badge, pop-up, on every open tab and device. | D19 |
| R-NC-4 | Notifications are deleted automatically after 30 days. An unhandled “Needs you” item is never pushed out by any count limit. The user can delete single entries and clear all. | D23 |
| R-NC-5 | Every notification has an owner (the owner of the session, task or schedule). Today that is the single user; storage and routing must not assume a single user. | D24 |
| R-NC-6 | Every centre entry offers navigation to the screen where it can be handled. | D39 |

### 5.2 Event catalogue and defaults

Defaults on a fresh install (D15, D40). “Notify” = centre entry; “Pop-up” = also show a pop-up; “Chat line” = a system line in a chat; “External” = eligible for later external delivery.

| Category | Event kind | Notify | Pop-up | Chat line | External |
|---|---|---|---|---|---|
| Needs you | Agent needs your approval for an action | On | **Always** (R-PU-2) | unchanged — the existing approval UI already covers it | Yes |
| Needs you | Agent asked a question and is waiting | On | **Always** (R-PU-2) | unchanged — already covered | Yes |
| Tasks & plans | Task or plan started | Off | Off | its own session | No |
| Tasks & plans | Task or plan finished (not scheduled) | Off | Off | its own session | No |
| Tasks & plans | Task or plan failed | On | On | its own session | Yes |
| Tasks & plans | Scheduled task finished | On | On | its own session | Yes |
| Tasks & plans | Scheduled task failed | On | On | its own session | Yes |
| Tasks & plans | Goal reached / goal given up | Off | Off | its own session | No |
| System-wide | Provider unreachable (network failure) | On | On | affected chats only | Yes |
| System-wide | Provider returned an error (API failure) | On | On | affected chats only | Yes |
| System-wide | Provider balance or quota exhausted | On | On | affected chats only | Yes |
| System-wide | Provider rate limit hit | On | On | affected chats only | No |
| System-wide | Omnipus lost its internet connection | On | On | affected chats only | No (cannot send while offline) |
| System-wide | Provider or internet connection restored | On | On | affected chats only | No |
| System-wide | System resources low (memory, disk) | On | On | — | Yes |
| System-wide | Concurrency limit reduced because resources are low | On | On | — | No |
| System-wide | Knowledge base search index rebuilt after drift (exists today) | On | Off | — | No |

| ID | Requirement | Decisions |
|---|---|---|
| R-EV-1 | The catalogue above is the initial set of event kinds; new kinds are added to it, never raised ad hoc. | D1, D40 |
| R-EV-2 | “Provider unreachable (network)” and “Provider returned an error (API)” are distinct event kinds. | D32 |
| R-EV-3 | “Affected chats” means chats actually hit — e.g. a reply failed, was retried or waited because of the event. Idle chats get no line. | D14 |
| R-EV-4 | A system-wide event appears once in the centre (and as a pop-up if enabled), plus a line in each affected chat. | D12 |

### 5.3 Preferences

| ID | Requirement | Decisions |
|---|---|---|
| R-PREF-1 | Per event kind, two switches: **Notify me** (centre) and **Also pop up**; a third, **Also send externally**, is reserved for later. | D3, D22 |
| R-PREF-2 | Preferences are global with a per-workspace override. System-wide kinds belong to no workspace and are global only. | D20 |
| R-PREF-3 | The two “Needs you” kinds always pop up; their pop-up switch cannot be turned off. | D30 |
| R-PREF-4 | Fresh-install defaults are the catalogue in 5.2. | D15, D40 |

### 5.4 Pop-ups

| ID | Requirement | Decisions |
|---|---|---|
| R-PU-1 | Duration by urgency: info/success 5 s; warning 10 s; error and Needs you stay until dismissed or handled. Hover pauses the timer. | D21 |
| R-PU-2 | Needs-you pop-ups always show (R-PREF-3). | D30 |
| R-PU-3 | At most 3 pop-ups visible; further ones collapse into “+N more — open notifications”. | D21 |
| R-PU-4 | At most one main action per pop-up (e.g. Open, Retry, Review) plus close; the action navigates to the screen where the item is handled. | D21, D39 |
| R-PU-5 | Results of the user's own actions (“Saved”, “Could not save”) do **not** raise a pop-up and do not enter the centre; the screen where the user acted shows the result. | D31 |
| R-PU-6 | No event opens a full-screen modal by itself, and no new ones are added. Dialogs the user opens deliberately are not events and stay. | D17, D37 |

### 5.5 In-chat system lines

| ID | Requirement | Decisions |
|---|---|---|
| R-SL-1 | One mechanism renders every system message in the chat, whether it originates in the server or the browser. | D6 |
| R-SL-2 | A system line is a simple line of text (Claude Code style), not a card. | D5 |
| R-SL-3 | Five types, each with its own icon, colour token and text weight: **Info**, **Warning**, **Error**, **Resolved**, **Needs you**. | D45 |
| R-SL-4 | Any type can be in an animated “in progress” state while something is still happening (e.g. a retry after a provider failure is an animated amber Warning; “Working — reading the CRM export” is an animated Info). | D45 |
| R-SL-5 | A connection-problem line shows at every level while the problem lasts and Omnipus is retrying. When the connection is back it disappears at Normal; at Verbose and Debug one line stays recording that there was a connection problem. | D10, D42 |
| R-SL-6 | When the browser loses its connection to the Omnipus server: one app-wide indicator plus a line in the open chat; neither enters history. | D18 |
| R-SL-7 | Lines may carry one action (e.g. Switch model, Review) that navigates or acts directly. | D28, D39 |

### 5.6 Detail levels

One rule for every chat component (D8, D41). “Shown” = visible; “—” = hidden.

| # | What | Normal | Verbose | Debug |
|---|---|---|---|---|
| L1 | Your messages and the agent's replies | Shown | Shown | Shown |
| L2 | Task and goal results; “agent needs you” requests | Shown | Shown | Shown |
| L3 | A failure that stopped or changed a reply (our sentence + the provider's message) | Shown | Shown + Technical details incl. masked payload (collapsed) | Shown + Technical details open |
| L4 | One “working…” line naming the current step | Shown | Shown | Shown |
| L5 | Tool calls with visible output (file, web page, image) | Shown | Shown | Shown |
| L6 | Every other tool call | Compact line (name only) | Line + result | Line + result + raw |
| L7 | Delegation — which agent was asked what | Summary only | Every step | Every step |
| L8 | Automatic retries, backup-model switch, connection problems | While happening only (R-SL-5) | Shown, and a record line stays | Shown, and a record line stays |
| L9 | Technical details on a system line (status code, request ID) | — | Collapsed link | Open by default |
| L10 | Timings, token counts, cost per reply | — | Shown | Shown |
| L11 | Raw provider request/response of non-failing calls, internal IDs | — | — | Shown |

| ID | Requirement | Decisions |
|---|---|---|
| R-DL-1 | Three levels: Normal, Verbose, Debug; the table above is normative. | D8, D41 |
| R-DL-2 | Technical details appear only at Verbose and Debug. For failures, Verbose includes the provider's raw response (masked), collapsed; Debug opens it by default. | D25, D43 |
| R-DL-3 | Every user may select Debug. | D16 |

### 5.7 Wording

| ID | Requirement | Decisions |
|---|---|---|
| R-WD-1 | High-level rule for every system message: what happened (plain words) · why, if known · what the user can do or what is already being done (for example, retrying). | D26, D47 |
| R-WD-2 | Messages contain dynamic parts. A provider's own error message (e.g. “Insufficient balance”) is shown on the main line, quoted and well presented — never hidden, and not force-classified into a custom text. | D26, D28 |
| R-WD-3 | Our own short sentence accompanies the provider's message; ADR-051's custom texts are kept only where they add a useful action (e.g. Switch model). The ADR-051 messages are the ones to revise. | D28, D36 |
| R-WD-4 | All system-message wording is reviewed through a dedicated review page (see 7. Follow-ups) listing every generated and hard-coded message with a rating and a suggestion field; the rewrite follows that review. | D7, D35 |
| R-WD-5 | **Voice in a chat.** A message shown inside a chat — the Omnipus chat or a channel chat such as WhatsApp — never names Omnipus as the actor. It speaks from the agent's perspective: “Omnipus could not …” becomes “I could not …”. Only those words change; the rest of the message stays as agreed. Lines about the user's own action stay as they are (“You stopped this reply.”). | D47 |
| R-WD-6 | **Voice everywhere else.** Pop-ups, banners, panels, settings screens, the notification centre and external notifications are neutral: “Image could not be sent”, not “Omnipus could not send the image”. | D47 |
| R-WD-7 | No “we”, “our” or “us”: in a chat they become “I”, “my”, “me” (agent's perspective); elsewhere the message is neutral. Third parties may be named as actors (“OpenRouter rejected the API key”). The only allowed use of the product name is the founder's own bug-report line (“please report it to the Omnipus.ai team”). | D48, D50 |
| R-WD-8 | A message that can appear both inside and outside a chat has a neutral form for the outside surface (for example, a task-failed notification: “{agent}'s working folder could not be opened”). | D47 |
| R-WD-9 | The agreed wording baseline is the founder's message review with R-WD-5…8 applied: `notifications-and-chat-system-messages-wording-2026-10-22.md` (ADR-051 catalogue in full, plus every message the voice rule or a founder edit changed). Unrated proposals count as agreed. Messages with no proposal yet are rewritten under R-WD-1…8 during the spec. | D49 |

### 5.8 Connection handling and logging

| ID | Requirement | Decisions |
|---|---|---|
| R-LOG-1 | Every connection problem between Omnipus and an AI provider, and between Omnipus and the internet, is written to the log at the default log level (not debug). | D32 |
| R-LOG-2 | The log and the event kind clearly distinguish **network failure** (provider not reachable at all) from **API failure** (provider answered with an error). | D32 |
| R-LOG-3 | A browser's lost connection to the Omnipus server is reported by the browser when it reconnects (start, end, duration) and logged by the server then. | D44 |
| R-LOG-4 | Lines removed from chat history (R-SL-5, R-SL-6) remain recoverable from the logs. | D10 |

### 5.9 Security and privacy

| ID | Requirement | Decisions |
|---|---|---|
| R-SEC-1 | Secrets (API keys, tokens, login headers, credential values) are always masked, at every detail level — including inside a provider's message shown on the main line (providers echo partial keys, e.g. “Incorrect API key provided: sk-…xyz”). | D16, D28 |
| R-SEC-2 | Notification retention as R-NC-4; deletion is real deletion from storage. | D23 |
| R-SEC-3 | External messages (later) carry only the notification title and a link into Omnipus — never chat content, file names, technical details or error payloads. | D27 |
| R-SEC-4 | External delivery (later) is opt-in per kind, rate-limited and batched. | D27 |

### 5.10 External delivery (requirements only — not designed or delivered now)

| ID | Requirement | Decisions |
|---|---|---|
| R-EXT-1 | Notifications can later be delivered via channels such as WhatsApp or email, reusing the existing channels where possible; the routing model reserves the “Also send externally” switch. | D4, D11 |
| R-EXT-2 | Content and rate rules: R-SEC-3, R-SEC-4. | D27 |
| R-EXT-3 | If Omnipus is offline, an external notification is dropped; the in-app notification is enough. | D38 |

### 5.11 Removals

| ID | Requirement | Decisions |
|---|---|---|
| R-RM-1 | Own-action pop-ups are removed and replaced by on-screen results (R-PU-5). A pop-up may only be removed where the page shows the same outcome; where it does not, the page first gains an indicator — with Retry for failed saves. Sized by the audit (2.1): 98 removable now, 194 need a new page indicator (87 with Retry). | D31, D46 |
| R-RM-1a | A pop-up that holds the only copy of a function (Undo, an exposure warning, a Switch button) is not removed until that function has a home on the page. | D46 |
| R-RM-2 | Any event-raised full-screen modal is removed (R-PU-6). | D17, D37 |
| R-RM-3 | Any badges in the top-right corner are removed. None were found in the code; the message review page sweep confirms. | D34 |

## 6. Definition of Done hooks

- **Reachability:** every event kind in 5.2 must have a real producer in the backend and render in the centre; preferences must be reachable in Settings; the detail-level selector must replace the current on/off “Verbose chat” switch (`src/components/settings/ChatSection.tsx`).
- **User-facing documentation TODOs:** `docs/using-omnipus-ui.md` (notification centre, pop-ups, detail levels, preferences); `docs/operations/observability.md` (connection logging, network vs API); `docs/protocol/websocket-protocol.md` (any new frames). `docs-verifier` audits each.
- **Contract-first:** new event kinds, preference shapes, the system-line frame and the browser outage report are added to `contracts/` before any code (Hard Constraint #8).
- **Hands-on UAT:** changes to `pkg/agent`, `pkg/session` or gateway WebSocket handlers require hands-on UAT on the exact commit being landed (root `CLAUDE.md`).

## 7. Follow-ups

| # | Item | Why |
|---|---|---|
| F1 | **Message review page** — every system-generated and hard-coded message (frontend and backend), with rating and suggestion fields | D35; input to R-WD-4 |
| F2 | ADR revising ADR-051 (provider error translation → provider message on the main line) | D36 |
| F3 | Provider-failure logging is partial (2.) — make it consistent per attempt with a network/API field | R-LOG-1, R-LOG-2 |
| F7 | Founder review of every system message: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/messages/` (ADR-051 catalogue with proposed rewrites; 363 analysed chat/app messages; 322 pop-ups) | D35, R-WD-4 |
| F9 | Withdrawn proposals: BE-097, BE-099, BE-101, BE-102, BE-104 are fragments inserted into the knowledge-drift notification (BE-096), not standalone messages; the review page's proposals for them were worker filler | correction, D49 |
| F10 | Check whether a user can reach an agent that is on no workspace team (`agent_not_configured`): the code path exists (`create_agent` outside a workspace makes a “metadata only” agent; `pkg/agent/workspace_reroot.go::resolveTurnWorkDirAndWorkspaceOrRefuse` refuses its turns), but whether the UI lets a chat or task reach it is not yet checked | founder question |
| F8 | Extend the pop-up category list with the audit's proposals (test result, mail sent/failed, sign-out, opened in another tab, placement failed, download/media failed, consequence warning, outside change) | D46 |
| F4 | Close or re-scope #645 against this document | superseded question |
| F5 | Check #177 (mobile notification-prefs) against R-PREF | compatibility |
| F6 | Spec (`plan-spec`) once the founder approves these requirements, then two `grill-spec` rounds | feature-size flow |

## Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Notification centre | Not a log; holds only opted-in event kinds; categories Needs you, Tasks & plans, System-wide | Logs and transcripts already record everything; centre must be useful, not noisy (founder corrected an earlier “log” reading) | Interview | 2026-10-22 |
| D2 | Pop-up alignment | One rule for when, how long, which actions; Needs you also pops up | Patchwork of ad-hoc pop-ups today | Interview | 2026-10-22 |
| D3 | Preferences | User chooses per event kind whether to be notified | e.g. “task started” is noise for many users | Interview | 2026-10-22 |
| D4 | External delivery | Considered (WhatsApp, email …) | Reach the user outside the app; refined by D11, D27, D38 | Interview | 2026-10-22 |
| D5 | System-line style | Simple line of text, Claude Code style, not a card; format by type; animation allowed | Founder rejected a card design | Interview | 2026-10-22 |
| D6 | One mechanism | One mechanism for every in-chat system message, server or browser | Multiple mechanisms today | Interview | 2026-10-22 |
| D7 | Wording | All system-message wording reviewed and rewritten | Some wording poor; vague messages unacceptable | Interview | 2026-10-22 |
| D8 | Detail levels | Three levels: Normal, Verbose, Debug | Founder choice over per-category toggles | Interview | 2026-10-22 |
| D9 | Output | Requirements document, no ADR yet | Founder choice | Interview | 2026-10-22 |
| D10 | Chat-line history | Depends on event type; reconnection line leaves history once resolved, kept in logs | Avoid clutter without losing evidence; refined by D42 | Interview | 2026-10-22 |
| D11 | External delivery scope | Requirements collected now; design and delivery later | Founder: collecting requirements ≠ delivering | Interview | 2026-10-22 |
| D12 | System-wide events | Affected chats + centre; pop-up allowed; never full-screen modal | Founder clarified pop-ups are welcome, modals are not | Interview | 2026-10-22 |
| D13 | Two connections | Omnipus↔internet/provider kept separate from browser↔server | Different causes, different display (team-lead had conflated them; corrected) | Interview | 2026-10-22 |
| D14 | Affected chats | Only chats actually hit | Avoid lines in idle chats | Interview | 2026-10-22 |
| D15 | Defaults | On: needs you, failures, scheduled finished/failed, system-wide; Off: started, non-scheduled finished | Sensible first impression | Interview | 2026-10-22 |
| D16 | Debug access | Every user, secrets always masked | Founder chose openness with masking | Interview | 2026-10-22 |
| D17 | Full-screen modals | No event raises one; user-opened dialogs stay | Founder: “only no full-screen modal pop-up” | Interview | 2026-10-22 |
| D18 | Browser lost server | App-wide indicator + line in the open chat | Server cannot deliver while disconnected | Interview | 2026-10-22 |
| D19 | Clearing | Handled anywhere = resolved everywhere | No stale badges | Interview | 2026-10-22 |
| D20 | Preference scope | Global with per-workspace override; system-wide global only | System events belong to no workspace | Interview | 2026-10-22 |
| D21 | Pop-up timing | 5 s / 10 s / sticky; hover pauses; max 3; one action | Founder accepted urgency-based rule | Interview | 2026-10-22 |
| D22 | Pop-up switch | Separate “Notify me” and “Also pop up” per kind | Centre entry without interruption | Interview | 2026-10-22 |
| D23 | Retention | 30 days; unhandled Needs you protected; user can clear | Security: retention needs a deletion rule; today newest 50, no age limit | Interview | 2026-10-22 |
| D24 | Recipient | Owner of session/task/schedule; single user now, multi-user-ready | Founder: multi-user may return | Interview | 2026-10-22 |
| D25 | Technical details | Verbose and Debug only | Founder choice | Interview | 2026-10-22 |
| D26 | Wording rule | What / why / what you can do, with dynamic parts; show provider messages | Provider messages are often meaningful (“balance exceeded”) | Interview | 2026-10-22 |
| D27 | External content | Title + link only; opt-in, rate-limited, batched | Messages leave Omnipus, may show on lock screens | Interview | 2026-10-22 |
| D28 | Provider errors | Our sentence + provider message on main line, masked; ADR-051 custom texts only where they add an action | Reconciles D25 with D26 | Interview | 2026-10-22 |
| D29 | Line types (superseded by D45) | Six types incl. separate “In progress” | Superseded after founder review | Interview | 2026-10-22 |
| D30 | Needs-you pop-up | Always; cannot be switched off | Resolves D2 vs D22 conflict | Interview | 2026-10-22 |
| D31 | Own-action results | No pop-up, not in centre; shown on screen where the user acted | Founder: handled in the UI anyway | Interview | 2026-10-22 |
| D32 | Connection logging | Mandatory for Omnipus-side and provider connection problems; network vs API clearly distinguished | Founder: diagnosis needs the difference | Interview | 2026-10-22 |
| D33 | Sidebar | Unchanged | Founder choice | Interview | 2026-10-22 |
| D34 | Top-right badges | Not found in code; any found are removed | Founder would remove them anyway | Interview | 2026-10-22 |
| D35 | Message review page | Separate page of all generated/hard-coded messages with rating and suggestions | Founder prefers reviewing real messages over examples | Interview | 2026-10-22 |
| D36 | ADR-051 | Its messages are the ones to change; follow-up ADR | Changes an existing decision | Interview | 2026-10-22 |
| D37 | Modals | No new event-raised modals | Founder: “correct, no new ones” | Interview | 2026-10-22 |
| D38 | Offline external | Dropped; in-app notification is enough | Founder choice | Interview | 2026-10-22 |
| D39 | Pop-up actions | Actions navigate to the right screen | Founder review comment | Interview | 2026-10-22 |
| D40 | Event defaults | As in 5.2 (founder edits: scheduled finished pops up; goal reached off; rate limit and restored pop up; needs-you chat unchanged) | Founder review page | Interview | 2026-10-22 |
| D41 | Detail-level table | As in 5.6 | Founder review page, reconciled by D42/D43 | Interview | 2026-10-22 |
| D42 | Retry lines | Shown while retrying at every level; gone at Normal when resolved; record line stays at Verbose/Debug | Reconciles table L8 with O3 | Interview | 2026-10-22 |
| D43 | Failure payload | Verbose shows masked failure payload (collapsed); other raw payloads Debug-only | Reconciles table L3 comment with L11 | Interview | 2026-10-22 |
| D44 | Browser outage logging | Browser reports the outage on reconnect; server logs it then | Server cannot log what it cannot see | Interview | 2026-10-22 |
| D45 | Line types | Five types; “in progress” is an animated state of any type | Founder: retry example should be an amber warning | Interview | 2026-10-22 |
| D46 | Own-action pop-up removal condition | Remove only where the page shows the outcome; otherwise add a page indicator first, with Retry for failed saves; audit every call site with a finer category list | Founder: “validate that there is another indicator in the page itself … maybe with a retry” | Interview | 2026-10-22 |
| D47 | Message voice | In a chat: replace “Omnipus” as the actor with “I” (agent's perspective), changing only those words. Elsewhere: neutral (“Image could not be sent”) | Founder: “if it is an in chat message it must be I could not …”; correction: “do not rephrase everything only where you put Omnipus” | Interview | 2026-10-22 |
| D48 | Bug-report exception | “Please report it to the Omnipus.ai team” is the one allowed product-name use | Founder's own edit of `provider_request_rejected`; confirmed by the founder (Q1 yes) | Interview | 2026-10-22 |
| D49 | Message review result | Founder ratings and edits adopted; unrated proposals count as agreed; voice rule applied over all of them | Founder: “in most cases I agreed to your proposal, assume that for the remaining ones” | Interview | 2026-10-22 |
| D50 | “We / our / us” | In a chat they become “I / my / me”; elsewhere neutral | Founder: “not we our us it must be I from the agent perspective when it is shown in a chat” (replaces team-lead's earlier blanket ban; confirmed by the founder (Q2 yes)) | Interview | 2026-10-22 |

## Dependency Graph & Implementation Order

```
        F1 message review page ───────────────┐ (parallel to everything)
                                               v
 [1] Contracts: event kinds, owner, resolved   [6] Wording rewrite (R-WD-*)
     state, preferences, system-line frame,        incl. ADR-051 revision (F2)
     browser outage report
        │
        ├──────────────────────┬────────────────────────┐
        v                      v                        v
 [2a] Backend: producers   [2b] Backend: provider   [3a] Frontend: system-line
      for every kind,          network-vs-API           component, 5 types,
      prefs store,             split + logging          animation, detail-level
      retention, resolve-      (R-LOG-*, R-EV-2)        rule (R-SL-*, R-DL-*)
      everywhere (R-NC-*)
        │                      │                        │
        └──────────┬───────────┘                        │
                   v                                    v
 [3b] Frontend: centre filters, prefs UI,   [3c] Frontend: own-action pop-up
      pop-up rules (R-PU-*)                      triage — 322 call sites
                   │                             (R-RM-1; independent, can start
                   │                              after the rule is agreed)
                   └──────────────┬──────────────┘
                                  v
                    [4] User-facing docs + docs-verifier
                                  v
                    [5] 5-reviewer gate, hands-on UAT, landing
```

Order: (1) before any code; (2a), (2b) and (3a) in parallel after (1); (3b) after (2a); (3c) and F1 in parallel from the start of the spec phase; (4) with each stream; (5) last. This is a feature-size change: it goes through `plan-spec`, two `grill-spec` rounds, and squads under their own `squad-lead`.
