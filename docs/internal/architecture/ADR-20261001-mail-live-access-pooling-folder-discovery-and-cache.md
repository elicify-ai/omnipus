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

Grounding: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/view.go::folderNameFor`, `FolderCounts`, `ReadFolderPage`, `parseMailRef`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/email-mail-view-spec.md::A1`, `D29`, `FR-020`; founder refresh rules. No assumed provider-specific folder is created.

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

Phase 2 starts only after Phase 1 has a separately recorded plain-IMAP result. **Settled direction:** discover JMAP per mailbox, use the existing mailbox credentials, require the mail capability, remember the choice, fall back to IMAP, and add an encrypted disk cache of headers/flags/folder state. **The numerical Phase 2 bounds below are architect recommendations, not founder-approved constants.**

### P2.1 Transport selection and bounded probing

| Area | Proposed Phase 2 rule | Authority / status |
|---|---|---|
| Discovery | Check IMAP `JMAPACCESS` when advertised, otherwise probe HTTPS `/.well-known/jmap` on the explicitly configured mailbox service origin. Resolve its advertised session location only through the security rules below. Do not guess arbitrary web domains from a user's email address or scan ports. Exact `JMAPACCESS` URL acquisition/API support must be verified in the implementation spec; it was not established by this bounded source review. | Founder Phase 2 direction; **Unknown** capability-integration detail. JMAP session/discovery behaviour: [RFC 8620, §2 and §2.2](https://www.rfc-editor.org/rfc/rfc8620.html). |
| Capability/account | Require the session's mail capability **and** the chosen account's `accountCapabilities` entry for `urn:ietf:params:jmap:mail`. Use the mailbox's appropriate mail account; a session advertising core only is not mail support. If multiple mail accounts cannot be resolved unambiguously, fall back visibly rather than read another account. | Founder mail-capability requirement, strengthened by verified RFC 8620 §2 account selection. |
| Authentication | Use the same resolved credential reference, not a copied password/config secret. If the service does not accept those credentials, requires an unsupported authentication mechanism, or supplies untrusted endpoint origins, do not claim working JMAP; retain IMAP and show the reason. | Founder same-credential rule; existing `mailPairClient` credential seam. |
| Remembered choice | Persist encrypted, pair/account-generation-bound metadata: chosen transport, trusted session origin/account identifier, capability snapshot, successful probe time, last failure class and next eligible probe. Treat the record as a hint, not authority to bypass fresh TLS/URL checks. | Founder per-mailbox choice; architecture recommendation. |
| Periodic re-probe | Recommend **once every 7 days**, evaluated on the next panel-open or due watcher event, never by a new folder/header-refresh timer. With no event, the due marker waits. A remembered “unsupported” result is negative-cached for the same 7 days. | Recommended explicit meaning of “periodically”; not founder-approved interval. |
| Failure re-probe | In Auto, invalidate a failed JMAP choice, attempt one bounded trusted-session re-probe and fall back to IMAP for the current read if time/budget permits. Recommend a **15-minute minimum failed-probe interval**, subordinate to an existing later A8 backoff; no probe/login loop on every page. Auth/TLS/security failures are surfaced and not retried blindly. | Founder failure re-probe/fallback; existing watcher backoff. |
| Probe bounds | Recommend **5 s maximum** discovery/session work inside the same **45 s** total read budget; one discovery chain, maximum **3 trusted redirects**, response maximum **1 MiB**. A failed optional probe must not consume another full request timeout before IMAP starts. Respect the server's smaller advertised request/concurrency/object limits. | Architectural bounds (Inferred), RFC 8620 §2 limits. |
| Manual override in Connectors | Recommend **Auto / IMAP / JMAP** with current choice and fallback reason visible. Auto permits fallback; IMAP skips probes; explicit JMAP is strict and reports unavailable rather than silently using IMAP. Manual Retry may retry a trusted failed probe once, not bypass URL validation or capacity. | Founder visible override; strict-vs-fallback override semantics are an explicit recommendation for the founder-question section. |
| Shared capacity | Both transports pass the same A8 account work gate. Preserve the **8 open transport sockets globally / 2 per mailbox** ceiling; in Auto, simultaneous fallback attempts must not allocate an uncounted ninth HTTP socket. Bound HTTP keep-alive sockets and close panel-owned ones on last observer close. Unchanged SMTP remains request-scoped and outside this IMAP/JMAP read pool. | Founder shared-load intent; architectural recommendation extending the read cap, not SMTP pooling. |
| State tracking | IMAP uses folder UIDVALIDITY plus UID; JMAP uses opaque account/object IDs and state/queryState. Never fabricate IMAP UIDs/UIDVALIDITY for JMAP. On transport switch, invalidate transport-specific cursors and rebuild the bounded header snapshot. | Verified current numeric contracts; RFC 8620 §5 state/query methods. |
| Watcher | On a selected working JMAP mailbox, use a bounded metadata/changes probe under the same cadence, backoff and shared manager; do not enable push/IDLE/event-source sockets in this phase by accident. No message bodies are fetched for new-mail notice. | Founder separate watcher; bounded protocol abstraction recommendation. |

### P2.2 Disk header cache: recommended complete limits

| Item | Recommended bound / retention | Freshness and release |
|---|---|---|
| Content | Only the envelope fields used by existing rows: subject, sender/recipients/display names, date, Message-ID if present, transport-specific message reference, flags and folder state. **No snippet, body, MIME part, inline image or attachment bytes.** | Successful server reads only. Server remains authoritative; cache is disposable and cannot complete an offline send/read-body. |
| Cardinality | Keep at most **newest 50 headers per each of the three roles**, therefore **150 per mailbox**. The **25-row page / 200-row active view** ceiling remains; older loaded/search rows are not persisted. | Drop older cache entries when a newer validated snapshot displaces them. Do not silently shorten the actual live list in order to fill the cache. |
| Time | Delete a header record **7 days after its last successful server validation**, not after its last cache read. This is cache age, not message date: a very old message can still be among the newest 50. | A read never extends retention. Local deletion housekeeping is permitted while closed; it must not trigger mail network refresh. Expired data is not served even if unlink fails. |
| Disk bytes | Recommend **1 MiB per mailbox** for headers/state and **32 MiB globally** for this disposable cache; folder-role metadata target/maximum **64 KiB per mailbox**. Evict least-recently-used disposable snapshots under the global bound, not mailbox config or server mail. | If one encoded row/file cannot fit, leave it live-only and show “cache unavailable for this data”; never truncate a subject/address or report a partial cache as a complete snapshot. These byte limits need footprint validation in the later spec. |
| Memory | Keep Phase 1's newest-50 working-cache bound and **30-minute** closed-panel retention. Recommend a **4 MiB global reusable metadata budget** within the product's <10 MiB overhead constraint; disk decryption should load only requested bounded snapshots, not all mailboxes at boot. | On memory pressure, evict reusable headers, not current user input or a live message response. No plaintext memory snapshot is written as fallback. |
| Storage | One encrypted header/state snapshot per pair, alongside the existing encrypted folder-role file in the proposed private Mail cache directory; atomically replace complete authenticated ciphertext. | Exclude the entire directory from data-git auto-commit and backups before the first file write. Purge on mailbox disable/removal, credential/endpoint change, workspace/agent deletion and key rotation. |
| Refresh | Show saved headers immediately, visibly dated/stale. Keep the Phase 1 **5-minute** stale threshold and **event-only** open/switch/manual/own-action refresh rules. | No header-sync timer while closed. Watcher can mark dirty from metadata; it does not fetch/populate disk headers. A live failure preserves eligible stale headers plus error/Retry. |

All bounds are proposals grounded in the founder's “bounded encrypted header cache” direction, Phase 1 limits and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/CLAUDE.md::Hard Constraints`. They require approval/specification before Phase 2 implementation; they are not an evidence-backed claim that current memory/disk usage meets them.

### P2.3 Invalidation and scoped D6/D21 amendment

| Event | Required invalidation, for both memory and disk |
|---|---|
| IMAP UIDVALIDITY change | Discard every header/reference/cursor from the old folder epoch; keep only a freshly validated role mapping/version. A stale UID cannot be used for read/mark/send/delete. |
| JMAP state advances | Apply bounded changes to flags, mailbox membership and deleted IDs. If changes cannot be calculated or state is invalid, discard the affected snapshot/query cursor and rebuild only the bounded newest page. Paginate changes under server limits; no unlimited full-account sync. |
| Message moved or deleted by another client | On successful refreshed membership/flags, remove it from the old role and invalidate affected counts. If an open cached row is not found live, remove/invalidate it and show “changed or deleted”; do not serve a stored body or take a mutation against an unvalidated stale ref. |
| Own mark-read/send/draft mutation | Update known flags after confirmed success; invalidate affected folder/query states and counts. Refresh once while open. Invalidate uncertain state on partial failure without replaying the action. |
| Pair moved, disabled or removed; agent/workspace removed | Revoke observers/token references; prevent new acquisitions; cancel pair-owned flights; increment its generation; close its sockets; delete both memory and disk caches plus remembered transport choice. Late completions cannot recreate files. |
| Credential/host/port/override change or key rotation | Close/invalidate the old generation. Delete disposable caches rather than migrating encrypted mail data under an uncertain key. Preserve the credential boot contract; a missing/wrong master key is not a reason to create a new one. |
| Corrupt ciphertext, schema mismatch or expired snapshot | Reject without plaintext salvage; surface a cache warning and use a budgeted live path when available. Never render unauthenticated ciphertext as mail or advance “last checked” on failure. |

**Proposed amendment text — applies when Phase 2 is accepted and implemented:**

> D6/D21 continue to make the mail server the authoritative mailbox. Omnipus may retain a bounded, disposable **encrypted local cache of message headers, flags and folder state only**, under the retention and invalidation limits of this decision. It is not a mailbox mirror, body store or full-text index. Bodies, body snippets, attachments and inline-part bytes are not part of this exception and are never cached beyond their open request. Cached rows must state their age; server refresh, mutation and reference validation remain live. Folder-version/state changes, moved/deleted messages, mailbox removal/reconfiguration and retention expiry invalidate affected cached data. Normal transcript records and user-directed exports remain separate from this Mail cache; this amendment does not expand them.

**Phase 1 clarification, effective before this amendment:** the founder permits only short-lived **in-memory header/flag display state** and encrypted **folder metadata** on disk, not a persistent header cache. Folder names are not mail content, so D6's no-stored-mail-content promise stands for the folder-list file; names can still reveal sensitive projects or people, which is why it is encrypted and excluded from versioning/backups.

The staleness risk is deliberate and visible: for example, a message deleted in another client can remain as a labelled cached row until the next eligible refresh; opening it checks the server and may report that it is gone. “Instant display” is not “fresh server data.”

## Alternatives considered

| Option | Benefit | Concrete drawback | Decision |
|---|---|---|---|
| **A — Per-request IMAP connections** (today) | Simple isolation; every close naturally retires selected-folder/command state. | Every folders/list/read request repeats TLS/login/select; the supplied 13-mailbox timings remain poor. | Reject as the foreground default; retain request-scoped release when no panel observer exists. Verified baseline: **E-Transport**, `Client` and `dialIMAP` (final evidence table). |
| **B — Pooled IMAP connections** | Removes repeated setup on healthy reuse while retaining live server reads. | Does not remove first-dial latency, absent-folder assumptions, or repeated envelope fetches. Requires exclusive leasing and shutdown correctness. | Adopt as the Phase 1 foundation, but not sufficient alone. Founder pooling decision. |
| **C — Pooling plus bounded header/folder cache** | Immediate labelled warm display; fewer repeated reads; server still owns mail. | Stale rows and sensitive metadata require visible age, invalidation and strict bounds. Disk headers change D6. | **Chosen**: memory headers/encrypted disk folder metadata in Phase 1; encrypted bounded disk headers only in Phase 2. Founder cache direction. |
| **D — Full local mailbox store** | Can support offline bodies and a local full-text index. | Duplicates sensitive mail/attachments, needs unbounded sync/reconciliation and storage, and violates the brief's request-only bodies and D6 boundary. | Reject. No SQLite/mail mirror, offline bodies or background whole-mailbox importer. |
| **E — JMAP** | Can batch metadata operations and expose explicit change state on compatible accounts. | Availability, same-credential support and endpoint trust are not guaranteed; selection adds protocol/reference/security work and confounds Phase 1 measurement. | Add conditionally in Phase 2, never prerequisite to improving plain IMAP. Founder phase order; RFC 8620 session/state rules. |

These are architectural comparisons (**Inferred**), not measured speedups or a claim that every provider supports discovery/JMAP. No option creates an email conversational channel or changes per-pair ownership. Sources: founder brief; **Per-(Agent, Workspace) Email Mailboxes** and **Credential Boot Contract** (absolute paths in Context); [RFC 8620](https://www.rfc-editor.org/rfc/rfc8620.html).

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
