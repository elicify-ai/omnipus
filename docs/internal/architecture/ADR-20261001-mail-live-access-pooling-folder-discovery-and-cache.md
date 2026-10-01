# ADR-20261001 — Mail live access: pooled connections, folder discovery and a bounded cache

- **Status:** Proposed; founder's core decisions settled in the 2026-10-02 brief. Draft checkpoint — incomplete, not implementation approval.
- **Date:** 2026-10-02 (founder decision date). The filename uses the naming helper's UTC date, 2026-10-01.
- **Deciders:** Daniel Piatkowski (founder, core decisions); architect (design record and recommendations).
- **Evidence baseline:** `docs/adr-mail-live-access` at `ee936a3858c172c19623af6c18bd2f286bcf648f`, before this document.
- **Scope:** Design only. No production code, contract changes, tests or push.

## Bottom line

Keep Mail live: first reuse plain IMAP connections — IMAP is the protocol used to read mail on the server — discover the server's real folders, and keep only bounded, short-lived message headers in memory. The founder has settled the limits: two connections per mailbox, eight overall, no panel-owned connections while the panel is closed, encrypted folder metadata on disk, and no stored bodies or attachments. Measure this first, without JMAP — a newer mail protocol that batches operations — then add JMAP where supported and an explicitly scoped amendment permitting an encrypted header cache. Source: founder-approved brief, 2026-10-02.

## Context — problem and evidence

### What is wrong today

Mail makes the user wait for repeated connection setup, and assumes folder names that some servers do not have. Reusing a connection removes repeated setup, not the first connection's server delay. Discovering folders fixes the assumption; it must not turn a network failure into a false empty folder. These are design conclusions from the transport and folder code below, not measured improvements.

| Evidence | Verified current behaviour | Meaning for this decision |
|---|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/transport.go::Client`, `dialIMAP`, `AccountKey` | Clients are connectionless between calls. Calls dial, authenticate and select INBOX; the account budget key is **host:port\|username**, not host\|username. | Pool below the client facade, preserving account isolation and TLS hostname checks. Do not duplicate pools in each REST-created client. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/view.go::folderNameFor`, `FolderCounts`, `ReadFolderPage` | Sent/Drafts default to literal names. The hotfix returns zero counts for structurally absent Sent/Drafts, but the page path still fails when SELECT cannot find the folder. | Preserve the hotfix; add discovery and consistent confirmed-absence handling to reads. Missing INBOX is not a healthy empty account. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/mail_budget.go::SharedMailBudget`, `call`, `TryCall`, `flightContext` | Shared two-slot account budget, duplicate-call coalescing and watcher backoff already exist. Shared work can outlive its first caller. | Add the eight-socket ceiling beneath this budget; cancellation must retire unsafe sockets and respect other observers. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/watcher.go::Cycle`, `WatcherBackoff`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/watcher_set.go::CycleAll` | Watcher updates counts/state without starting turns or creating tasks. Its nominal one-minute cadence and exponential backoff are separate from the panel. | Keep closed-panel new-mail notice; do not restore the retired drainer. Pooling must not bypass its budget/backoff. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/results/lane-M.md::The 13 mailboxes (M2/M3)` | Supplied post-hotfix UAT: 13/13 folder calls succeed; folders take 2.5–8.1 s, Inbox lists 4.7–22.8 s, summary 17–25 s; all 13 Sent lists fail with `folder_missing`. | This is recorded evidence, not a new run. Folder/list timings share the mailbox-click clock origin. The Inbox samples contain only 0–8 messages. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/email-loading-rca.md::Evidence` | Supplied pre-hotfix analysis records fresh TLS/login work, slow server setup, and query retries prolonging failures. Its fail-all folder-count observation predates this checkout's hotfix. | Do not repeat the superseded count defect or assert a proven cause of slow server handshakes. Separate cached display time from live refresh time. |

The supplied reports do not contain a comparable message-open latency series, a large-folder benchmark, or a completed real connection-failure Retry exercise. Those baselines remain **Unknown** until measured. No performance, test or security result is produced by this ADR.

### Authority and explicit supersession

| Source | What stands | What this ADR changes or qualifies |
|---|---|---|
| Founder-approved task brief, 2026-10-02 | Phase 1 limits, encryption from the start, no panel timers, Phase 2 direction, and design-only scope. | Architect recommendations below are labelled separately; they are not invented founder approvals. |
| **Per-(Agent, Workspace) Email Mailboxes**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/architecture/ADR-033-per-pair-mailboxes.md::Decision` | One mailbox per agent/workspace pair; structural resolution and pair-keyed credentials. | Its historical inbound-drainer/Board-task paragraph is not authority for this work; the later Mail spec and current watcher supersede it. No side edit to that ADR is included here. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/email-mail-view-spec.md::D6`, `D21`, `D25`, `A8` | Live mail, separate watcher, coalescing, visible failure and two operations per account. | Phase 1 expressly permits short-lived **in-memory headers**, but no disk header store. Folder metadata is not mail content. Phase 2's narrow D6/D21 amendment is below. D25/A8's repeating 30-second panel refresh and one-new-session-per-request assumption are superseded by event-driven refresh and pooling. |
| **Credential Boot Contract**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/architecture/ADR-004-credential-boot-contract.md::Decision` | Existing credential key/unlock ownership; no replacement key over existing encrypted data. | Cache encryption must reuse the sanctioned key-derivation seam, not export the master key or invent a second password. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/CLAUDE.md::Hard Constraints`, `Definition of Done` | Single pure-Go binary, bounded footprint, contract-first generated boundary types, reachable UI/tools and matching user documentation. | No production code, actual contracts, tests, old ADRs or user pages are edited in this design task. Later implementation owns those changes. |

**Reading conventions:** evidence labels in the final table are references to actually read files/symbols, not new requirement IDs. **Verified** means source behaviour or supplied-report contents were checked; **Inferred** means an architectural consequence or recommendation, not a tested result; **Unknown** names a genuine evidence gap. GitNexus tools/resources were not exposed in this session; direct reads/searches ground the impact assessment. No production symbol is modified.

## Decision — Phase 1: plain IMAP

### P1. Settled limits and refresh rules

This phase uses **plain IMAP only**. JMAP must be disabled in the Phase 1 benchmark. The server remains the source of truth. The following numbers and triggers come from the founder-approved Phase 1 brief; they replace the older timer/session assumptions, not the existing safety budget.

| Area | Decided limit or behaviour | Refresh / release rule |
|---|---|---|
| IMAP sockets | Maximum **2 per mailbox** and **8 open globally**, including panel, watcher and agent-tool connections; count connecting reservations as well as established sockets. For pairs sharing one account, also enforce the existing two-slot **account** cap. | Open only for work, never preconnect all 13 mailboxes. Close after approximately **2 minutes since last completed use**, or sooner under idle eviction/panel close. |
| Eviction | Least-recently-used **idle** connection first. Never evict an active operation to warm another mailbox. | If all eight are active, wait only within the bounded request budget, then report busy. Do not create a ninth socket. |
| Panel ownership | Panel-requested sockets may be retained only while at least one authenticated Mail panel observer is open. No panel-owned connection remains when the last panel closes. | A close, workspace exit, logout or observer disconnect cancels that observer's work and closes idle panel sockets immediately. Active sockets close when the last dependent request is cancelled/completes; independent watcher/tool work is not interrupted. |
| Folder choices | Keep the three stable UI roles **Inbox, Sent, Drafts**. An explicit, visible per-mailbox Sent/Drafts name setting wins. Otherwise discover SPECIAL-USE roles using LIST-EXTENDED where supported, then try the configured/default name, then common names. | The discovery operation is bounded and coalesced; it does not fetch mail bodies. Do not claim current `folderNameFor` already performs discovery. |
| Truly absent folder | After successful discovery/probing confirms no Sent/Drafts folder, show that role as empty and explain that the server has no such folder. | Do not create a server folder during discovery. Do not convert auth, DNS, TLS, timeout or permission errors to empty. Missing INBOX is an account error. |
| Folder-list cache | **One small encrypted disk file per mailbox**, containing resolved names, roles and **UIDVALIDITY** — the server's folder identity generation — plus schema/config generation and last successful validation time. No header, subject or address data. | Load on demand. Refresh only on first mailbox open; panel opening with a saved list **older than 24 hours**; manual **Refresh**; or **one immediate rediscovery** after a missing-folder failure or observed folder-version change. No repeating timer; no folder-list refresh while panel is closed. |
| Counts | Panel folder counts are **memory only**. Unknown is distinct from zero. | Refresh on panel open if absent or **older than 5 minutes**; on a folder switch alongside its list; after this client's own successful action such as mark-read/send; and on manual **Refresh**. No repeating timer. |
| Headers | Reusable header cache is **memory only**, at most the **newest 50 per folder per mailbox**. It holds envelope fields and flags, not message text or attachment bytes. | Display immediately on folder open; if absent fetch once, or if **older than 5 minutes** refresh once in the background. No refresh while the panel is closed. Drop these memory entries **30 minutes after the panel was last open**; panel close starts that retention clock, reopening resets it. |
| Bodies and attachments | **Bodies and attachments are NEVER cached beyond the one open request.** | Fetch on demand. No server token payload store, reusable query cache, browser persistent store, disk file or retry cache of bodies/parts. The current open view may render its response; reopening fetches again. Release response/object-URL state on message/view exit. |
| Display pagination | **25 rows per page**. **Load more** adds **25**. Hard ceiling **200 rows per folder per view**. | Beyond 200, the user searches; never auto-load the mailbox or grow a reusable 50-header cache into a 200-header cache. Loaded older pages are only the active view's working set and are released on view exit. |
| Watcher | Remains independent of the panel so agents/users retain new-mail notice when it is closed. Shares both the same socket manager and **A8** account budget. | Keep the existing nominal **60-second** probe/backoff behaviour in Phase 1; watcher activity does not refresh folder-list/header caches or prolong their panel retention. No mail-triggered agent turn or Board task. |
| Summary | Reads watcher state, not live IMAP, and never waits for a panel's cache refresh. | Preserve honest never-checked/backoff/error states. Pooling is not a demonstrated remedy for its reported 17–25 s delay. |

Authority: founder brief; current seams are `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/view.go::FolderStat`, `ReadFolderPage`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/mail_budget.go::call`, `TryCall`; and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/gateway/rest_mail_summary.go::handleMailSummary`.

### P2. Connection ownership and acquisition

**Architect recommendation (Inferred, high confidence):** one application-owned manager below the existing mail client facade, injected into panel, watcher and agent-tool paths. A REST-created `Client` must not construct a private pool. The budget counts work; the pool counts actual sockets. Neither replaces the other.

```text
Panel / watcher / agent tool
             |
       authenticate + resolve pair and config generation
             |
       cache-only read? ---- yes ---> labelled cached result; no socket
             |
             no
             |
       A8: backoff gate -> coalesce identical READ -> 2-slot account gate
             |
       shared manager: exclusive mailbox lease -> 8-socket reservation
             |
       reuse healthy idle socket OR bounded TLS/login -> operation
             |
       release healthy socket if panel observer remains;
       otherwise close; timeout / cancellation / protocol failure -> retire
```

| Mechanism | Recommended design and failure prevented | Evidence / authority |
|---|---|---|
| Identity | Preserve `host:port\|username` for the A8 contention boundary. Pool/cache isolation additionally binds agent/workspace pair, configured endpoint/TLS identity and a non-secret credential/config generation; do not key by password text. Reconfiguration/removal increments the generation before an old completion may publish. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/transport.go::AccountKey`; **Per-(Agent, Workspace) Email Mailboxes**. |
| Exclusive lease | One borrower owns a socket until its entire operation finishes. SELECT/EXAMINE state belongs to the lease, never to another simultaneous request. Re-select/validate the requested folder before use; PEEK reads must preserve flags. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/view.go::ReadFolderPage`, `ReadView`, `MarkSeenIn`. |
| Deadlines and lock order | Coalesce before taking the account slot; acquire a socket only after the slot. Reserve global capacity atomically before dialing. No pool lock is held during network I/O, and no socket borrower waits to acquire a second account slot. Recommend **5 s maximum acquisition wait**, within a **45 s total read-work deadline** including queue, dial and commands; preserve the **30 s dial ceiling** as a subordinate bound. Release every reservation on failed dial. | Existing `commandTimeout`, `dialTimeout` and `flightContext`; tighter acquisition/overall bounds are recommendations, not measured tolerances. |
| Poisoned connection | Timeout, cancelled command, server BYE or protocol failure closes and retires the socket. Wait for command-reader termination before marking it reusable; buffered goroutine completion is not proof of a healthy protocol session. An idle socket that proves dead may be replaced **once for a read**, within the same deadline/backoff rules. Never silently replay a mutation. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/transport.go::runIMAP`, `dialIMAP`; current callers use Close to unblock commands. |
| Panel presence | Recommend contract-defined **open/close observer messages on the authenticated gateway WebSocket**, including an opaque per-panel observer ID, workspace and mailbox scope. Bind them to the authenticated connection, not a caller-supplied session identity. Explicit close and socket disconnect remove that observer. Existing socket liveness should reap lost connections; no new mail-data polling timer is introduced. Validate exact integration against the gateway's current socket lifecycle during implementation. | Founder close rule; `flightContext` can outlive the first request. This is a proposed contract/lifecycle shape, not a claim that Mail presence exists today. |
| Multiple tabs and shared flights | Retain sockets only while any eligible observer remains. Closing one tab cancels only its subscriptions; cancel the shared operation only when no request/observer still depends on it. A detached flight must not return a socket to retention after the last observer closes. Tool/watcher requests have their own owners and terminate normally. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/mail_budget.go::flightContext`, `call`; founder panel-close rule. |
| Idle expiry | Schedule only socket/resource expiry, not mail polling. Last-completed-use determines the two-minute idle deadline. Eviction and close never send an unsafe EXPUNGE or change message flags. | Founder pool rules; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/view.go::DeleteDraftStatus` protects exact UID expunge. |

### P3. Folder discovery and invalidation

**Architect recommendation (Inferred):** treat Inbox/Sent/Drafts as logical roles, not literal IMAP names. A visible explicit override outranks discovery; an old shipped default such as `Sent` is a fallback, not an operator override. The configuration model needs to distinguish those cases before implementation (proposed contract shape below).

| Situation | Required handling |
|---|---|
| First open / list older than 24 h / manual Refresh | Obtain the server's supported role attributes and folder names. Use LIST-EXTENDED when available; fall back to ordinary LIST/probing when it is not. Probe only resolved candidates for existence/UIDVALIDITY and obtain requested counts separately. Unsupported extension is not a whole-mailbox failure. |
| No special-use role | Try the configured/default name, then a deterministic small candidate set: Sent, Sent Items, Sent Messages, `[Gmail]/Sent Mail`; Drafts, Draft, `[Gmail]/Drafts`. These are fallback proposals, not proof of a provider's layout. Only a successful probe establishes a mapping. |
| Multiple role candidates | Prefer a still-valid saved mapping; otherwise show the ambiguity and ask the user to choose through the visible name setting. Do not send/draft into an arbitrarily selected folder. |
| Saved role now missing | Invalidate that mapping and its headers/counts; perform **one** immediate coalesced rediscovery while the panel is open, then either adopt a confirmed replacement, show confirmed absent Sent/Drafts, or return the actual failure. An explicit missing override remains an actionable settings warning, not a reason to ignore the override. |
| UIDVALIDITY changes | Discard affected header entries and pagination cursors before publishing new rows; refresh the saved folder version once. Never reuse an old UID against a new epoch. UIDVALIDITY may be unknown before validation and must be represented as unknown, not a fabricated version. |
| Panel closed when watcher detects a version change | Invalidate/mark dirty only. Defer folder-list/header refresh until the next eligible panel event. The watcher may update its own UID/count metadata. |
| Counts and own mutations | Cache counts with separate successful-fetch timestamps. A successful mark-read/send/draft action patches known affected flags where safe and invalidates affected counts/list pages; perform one refresh while open. Failed/ambiguous actions do not pretend counts changed and are never automatically repeated. |
| Stale cache | Display a last-checked/stale label and refresh indicator without replacing rows with a skeleton. If the single refresh fails, preserve labelled stale rows plus visible error/Retry; do not reset the timestamp or call them live. This rule also applies to external moved/deleted mail. |

Grounding: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/view.go::folderNameFor`, `FolderCounts`, `ReadFolderPage`, `parseUIDRef`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/email-mail-view-spec.md::A1`, `D29`, `FR-020`; founder refresh rules. No assumed provider-specific folder is created.

### P4. Watcher recommendation and compatibility decisions

| Option | Decision and reason |
|---|---|
| Continue 13 independent once-minute dials in every state | Reject as the primary design: when the panel is open, this wastes reusable authenticated sessions and competes with foreground reads. Closed-panel cycles still need request-scoped connections; this ADR does **not** promise their elimination. |
| **Reuse the same bounded pool** | **Recommend for Phase 1.** Watcher uses nonblocking A8/pool acquisition, a short STATUS lease, then releases. Reuse healthy eligible sockets while panels are open. With no panel observer, close after the cycle; never retain 13 watcher sockets or warm folder/header caches. A skipped cycle leaves the prior last-checked time unchanged, so the badge cannot claim a fresh check. |
| IDLE — a server notification mode holding a connection | Defer. Holding one connection for each of 13 mailboxes exceeds the global eight-socket limit. Rotating IDLE subscriptions introduces fairness/reconnect complexity and confounds the requested plain-IMAP benchmark. It requires a later measured decision, not a hidden Phase 1 addition. |

The current watcher set loops over accounts sequentially; its comment is not proof that a stalled account cannot delay others. Recommend a bounded fair scheduler under the same caps, with at most one cycle in flight per mailbox and ready foreground work ahead of optional watcher work, but without starving due watcher cycles. Do not claim this concurrency exists today. Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/watcher_set.go::CycleAll`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/watcher.go::WatcherInitialOffset` (currently index × one minute), `probe`.

Two existing behaviours need explicit integration, not invented completion:

| Compatibility issue | Recommendation and implementation gate |
|---|---|
| HTML previews retain bodies/inline bytes for 15 minutes | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/gateway/mail_preview_token.go::mailPreviewGrant`, `MailPreviewTokenTTL`, `mint` violate the new request-only body rule. Recommend grants holding **authorization/reference metadata only**; each preview/inline response fetches and sanitizes within its own request, through the shared budget/pool, with no payload left in the token store. Preserve existing preview security controls. This can add live fetches; measure it. No new HTML styling or attachment-preview feature is designed here. |
| Beyond 200 rows the user must search | Existing list API/client expose UID pagination, not a panel search parameter (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/src/lib/api/mail.ts::fetchMailMessages`); the earlier spec excludes server-search UI. Recommend a **minimal folder-scoped server search** using the same 25/200 limits and A8/pool, not a local index. The revised spec must make it reachable in the panel; a dead-end “search instead” instruction is not completion. |

## Decision — Phase 2: JMAP and encrypted headers

Pending detailed decision tables and D6 amendment text.

## Alternatives considered

Pending comparison of A–E.

## Failure behaviour

Pending visible failure, Retry and budget rules.

## Security and privacy

Pending encryption-helper verification, storage boundaries and JMAP URL protection.

## Wire-format impact

Pending proposed contract shapes; no contract files are changed by this ADR.

## Affected components and parallel work packages

Pending file ownership and dependencies.

## Test strategy

Pending future proof obligations; no tests are run or written for this design task.

## Measurement plan

Pending cold/warm comparisons and recommended pass bars.

## Consequences and risks

Pending positive, negative and neutral consequences, each risk with a failure scenario.

## Non-goals

Pending explicit scope boundaries.

## Features pending founder decisions

Issue placeholders only: #1170, #1171, #1172, #1173, #1174, #1175. A separate later pass owns these features; this ADR makes no decision about them.

## Open questions for the founder

Pending evidence review; settled decisions will not be reopened.

## Evidence table

| Claim | File or command | Certainty |
|---|---|---|
| The assigned branch starts at the Mail hotfix baseline | `git branch --show-current` exit 0: `docs/adr-mail-live-access`; `git rev-parse HEAD` exit 0: `ee936a3858c172c19623af6c18bd2f286bcf648f` | Verified (high confidence) |
| The helper supplied this date-based ID | `scripts/new-adr-id.sh "Mail live access: pooling, folder discovery and cache"` exit 0: `ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache`; `date -u` exit 0: `UTC 2026-10-01 18:44:40` | Verified (high confidence) |
| Core design direction is settled; code evidence remains to be checked | Founder-approved task brief, 2026-10-02; headings above explicitly mark incomplete sections | Verified direction; Unknown implementation evidence |
| **Self-check** | Initial artifact checked against the robustness requirement: correct helper ID, founder's bottom line and all requested major section headings; still an incomplete draft checkpoint | Verified (high confidence); not a completed ADR |
