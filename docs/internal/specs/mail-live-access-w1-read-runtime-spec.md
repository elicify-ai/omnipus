# Implementation Specification: Mail live access — W1 read runtime (pooled connections, shared operation budget, watcher)

- **Status:** Draft for the plan-spec grill — derived from the approved design record **Mail live access: pooled connections, folder discovery and a bounded cache** (`docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`, grill report `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` applied; founder answers Q1–Q5 recorded). This spec is the **W1 work package only**: the pooled IMAP read runtime, the shared operation budget's identity/ordering corrections, and the watcher's relationship to both.
- **Date:** 2026-10-02
- **Work package:** W1 (read runtime) of the ADR's work-package table. Exclusive W1 production files: `pkg/email/transport.go`, `pkg/email/mail_budget.go`, `pkg/email/watcher.go`, `pkg/email/watcher_set.go`, proposed `pkg/email/pool.go`.
- **Companion specs:** W0 (contracts), W2 (discovery/metadata — owns all `pkg/email/view.go` changes), W3 (panel), W4 (gateway/agent integration), W5 (tests) are separate specifications. Interfaces W1 publishes to them are frozen in §3 so the packages can be built in parallel without editing each other's files.
- **Every code claim below was verified by reading the file in this checkout** (`wt-adr-mail` at `fc4c8bf6d`, branch `docs/adr-mail-live-access`) unless labelled otherwise. GitNexus was not available in this worktree (no `.gitnexus/` index) — impact rows are first-hand Read/Grep sweeps labelled **Inferred**, per `omnipus-shared-rules` rule 9. No test was run and no measurement was taken by this spec.

---

## 1. Summary and scope

**Bottom line:** today every mail read dials the server from scratch — TLS handshake, login, SELECT INBOX, command, close — on every click, for the panel, the agent tools and the watcher alike (`pkg/email/transport.go::Client` is documented "connectionless between calls"). This work package puts **one application-owned connection manager** below that client facade, so repeated reads reuse a healthy authenticated session instead of repeating setup; it caps the pool at **two sockets per mailbox and eight open globally** (counting connecting reservations, not just established sockets); it gives every operation an **exclusive lease** on its socket so one request's selected folder can never leak into another's; it bounds every read to **45 seconds total** with a **5-second** maximum wait for pool capacity and the existing **30-second** dial ceiling subordinate; and it re-worked the shared two-per-account operation budget so that identical reads **coalesce only when they truly mean the same thing** (same agent/workspace pair, same configuration generation, same operation, same normalized arguments, same live-versus-cache purpose — grill finding I-01) and a read that was superseded by a newer **publication revision** publishes nothing anywhere (grill finding I-02). The watcher keeps its independence and its cadence but gains bounded fair scheduling under the same caps. **Phase 1 is plain IMAP only; JMAP is out of scope for W1 entirely.**

In scope (all in `pkg/email/` plus its injected seams):

- **The pool manager** (new `pkg/email/pool.go`): socket ceilings, reservation accounting, exclusive mailbox leases, idle expiry and LRU eviction, poisoned-connection retirement, panel-observer presence hooks, and the publication-revision capture seam.
- **The client facade rewiring** (`pkg/email/transport.go`): the client stops dialing per call and borrows sessions from the injected manager instead. A REST-created or tool-registered client **cannot** construct a private pool — the manager is a process-singleton injected at the three production construction sites.
- **The budget's identity and ordering corrections** (`pkg/email/mail_budget.go`): the read-coalescing identity (I-01), the total-read-deadline plumbing, and the revision capture — the two-slot per-account semaphore, backoff gate and singleflight structure stay.
- **The watcher's relationship to the pool** (`pkg/email/watcher.go`, `pkg/email/watcher_set.go`): bounded fair scheduling (replacing the sequential loop), at most one cycle in flight per mailbox, non-blocking acquisition so watcher work never displaces foreground reads, no socket retention and no panel-cache fill while no panel is open, and a skipped cycle leaving the last-checked time unchanged.
- **Wiring seams consumed by W4** (gateway REST, agent tool registration, watcher provider): W1 publishes narrow interfaces; W4 injects the shared instances. W1 does not edit `pkg/gateway/` or `pkg/agent/` (W4's files).

Out of scope for this spec (owned elsewhere, interfaces frozen here):

- Folder discovery, the folder-mapping cache, header cache, and **all** changes to `pkg/email/view.go` (W2). W1 publishes the lease/session interface W2's file will consume; W2 never edits `pkg/email/transport.go`.
- Wire contracts (`contracts/`), generated types, the presence WebSocket frames, and the typed 503 `reason` field (W0 defines; W4 consumes).
- The gateway/agent injection call sites themselves, boot/reload wiring, removal cascades (W4).
- All test files (W5 owns them; §8 names them and what each must prove).
- SMTP — stays request-scoped, outside the read pool, unchanged.
- JMAP transport selection, encrypted disk header cache, Phase 2 anything (W6/later waves).

---

## 2. Existing Codebase Context

### 2.1 Symbols involved (all read first-hand in this checkout)

| Symbol | Role | Verified context |
|---|---|---|
| `pkg/email/transport.go::Client` | **modifies** | The production IMAP facade. Its own doc comment states it is "connectionless between calls: each operation dials, authenticates, performs the operation, and tears down." Holds only `acct Account`. Every read method (`ReadInbox`, `Search`, `ReadMessage`, `FolderCounts`, `ReadFolderPage`, `ReadView`, `MailboxStatus`) calls `c.dialIMAP(ctx)` and `defer client.Close()`. This per-call dial+login+SELECT-INBOX is the repeated-setup cost the pool removes. |
| `pkg/email/transport.go::dialIMAP` | **modifies (split)** | Dials (implicit TLS, plaintext for loopback test hosts), with `dialTimeout` (30 s) and bounded DNS retry (`dialResolver` seam, 3 lookups, 250/500 ms backoff), then LOGIN, then **always SELECTs INBOX** — even for a Sent/Drafts read, which then SELECTs its real folder again via `pkg/email/view.go::selectFolder`. The pool's session-establishment step is dial+login; the per-borrower folder SELECT becomes lease work. |
| `pkg/email/transport.go::runIMAP` | **calls** | Bounds **each** IMAP command with `context.WithTimeout(ctx, commandTimeout)`; on deadline the caller's deferred `client.Close()` unblocks the goroutine parked in `Wait/Collect`; the result channel is buffered so the goroutine cannot leak. Under the pool this per-command bound continues, subordinate to the new 45 s total read deadline. |
| `pkg/email/transport.go::AccountKey` | **unchanged** | `"host:port\|username"` — the port was added deliberately because port-less keys collided two mailboxes sharing one host onto one semaphore (the comment records the confirmed defect). **Note:** `pkg/email/mail_budget.go`'s file-header comment still says the key is `"host\|username"` — stale comment, code is authoritative. This key stays the **contention** key (A8 two-slot gate); after correction I-01 it never again decides result sharing. |
| `pkg/email/transport.go::imapDial` (package var) | **test seam** | The TLS dialer is a package-level var "only so tests can point dialIMAP at an in-memory server"; production never reassigns it. The pool's dial step reuses this seam, so the existing fake-server harness keeps working unchanged. Tests must not run blindly in parallel (global seam). |
| `pkg/email/transport.go` consts | **reference** | `dialTimeout = 30 s`, `commandTimeout = 45 s`, `defaultListLimit = 20`, `maxListLimit = 100`, `maxBodyBytes = 256 KiB`. The founder-accepted **45 s total read deadline** coincides numerically with today's per-command `commandTimeout`; the two are distinct clocks and the spec keeps both names separate. |
| `pkg/email/transport.go::ctxOrCommandDeadline` | **calls** | Raw-connection deadline = caller's deadline if set, else `now + commandTimeout`. The detached shared-flight context (`flightContext`) reuses the same rule. |
| `pkg/email/mail_budget.go::MailBudget` / `::SharedMailBudget` | **modifies** | The A8 gate: backoff check → singleflight coalescing → 2-per-account semaphore (`mailBudgetSlotsPerAccount = 2`). One instance per state dir, process-wide (`sharedBudgets` map), so REST, tools and watcher contend on one cap. |
| `pkg/email/mail_budget.go::MailBudgetRequest.flightKey` | **modifies (I-01)** | Verified: `Account + "\x00" + Operation + "\x00" + json(Params)`; empty `Params` map returns `""` = coalescing opt-out. `AgentID`/`WorkspaceID` ride the request but are **not in the key** — so two pairs on one account, or two generations, can share one flight today. This is grill I-01's exact defect; §4.8 replaces the key. |
| `pkg/email/mail_budget.go::call` / `::CallValue[T]` | **modifies** | `singleflight.Group.DoChan` on the flight key; the flight runs on `flightContext` (`context.WithoutCancel` + first caller's deadline, else `commandTimeout`) so one tab's cancel cannot fail other joiners; each joiner keeps its own bail-out (`ErrMailBusy` wrap). Joiners receive the executor's typed value — the generic entry exists precisely so a coalesced joiner cannot be handed an empty result. |
| `pkg/email/mail_budget.go::TryCall` | **modifies (pool-aware)** | The watcher's non-blocking entry: backoff → `ErrMailSkipped` (a skip is not a failure — no `recordFailure`); both slots held → `ErrMailSkipped` immediately; never coalesces. The watcher's pool acquisition must inherit this non-blocking shape (§4.10). |
| `pkg/email/mail_budget.go::backoffRefusal` | **unchanged** | Reads the same persisted watcher state (`LoadWatcherState` + `EffectiveState`) the watcher writes; `Retry=true` bypasses **only** this check; empty pair or unreadable state fails open with a visible WARN. |
| `pkg/gateway/rest_mail_budget.go::mailBudgetWrap` | **consumed (W4)** | The REST gating wrapper: builds `MailBudgetRequest{Account: client.AccountKey(), AgentID, WorkspaceID, Operation, Params, Retry: mailRetryParam(r)}` and calls `email.CallValue`. Passes `AgentID`/`WorkspaceID` that `flightKey` ignores today — after I-01 they participate. `mailBudgetErr` maps `*MailBackoffError` → 503 `code=backoff` and `ErrMailBusy` → 503 `code=busy`. The typed `reason=pool_busy\|account_busy\|…` extension is W0's contract change; W1 supplies the distinct error values. |
| `pkg/gateway/rest_mail_read.go::handleMailFolders`, `::handleMailList` | **consumed (W4)** | Verified dial closures: `client.FolderCounts(c)` under op `listMailFolders` params `{scope:folders}`; `client.ReadFolderPage(c, folder, limit, beforeUID)` under op `listMailMessages` params `{folder, limit, before_uid}`. These are the two reads whose flight identity I-01's tests exercise. |
| `pkg/gateway/rest_mail.go::mailPairClient` | **consumed (W4)** | Verifies pair + resolves password from the credential store, then `email.NewClient(...)` **per request**. This is the "REST-created client" the design forbids from building a private pool: W4 injects the shared session source here; the client itself never constructs a manager (§4.1). |
| `pkg/gateway/rest_mail_read.go::handleMailSeen` | **hazard replaced by the lease** | Verified: calls `client.ResolveRef(...)` (one session), discards the returned epoch, then `client.MarkSeenIn(...)` opens **another** session — the resolve-then-mutate split the grill's source-integration note flags. Under the lease, ref validation happens **on the same selected lease** that performs the mutation (W2 implements; W1's lease API makes the same-lease check possible). |
| `pkg/gateway/gateway_boot.go` watcher wiring (and the same shape in `pkg/gateway/gateway_reload.go`) | **consumed (W4)** | `email.MailboxProviderFunc(func() []email.Mailbox { return buildMailboxes(...) })` → `email.NewMailboxWatcherSet(provider, homePath, email.SharedMailBudget(homePath))`, wrapped in `heartbeat.NewMailWatchService(..., 0)`. The provider is rebuilt from live config each cycle. W4 re-injects the pool here. |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent`, `::SetSharedMailBudget` | **consumed (W4)** | Builds `tools.EmailTransports` (workspace → `*email.Client`) per agent with env-resolved passwords; injects the budget via the optional `SetMailBudget(b, agentID)` setter. W4 adds the pool injection the same way (optional setter; unit-test toolsets without it keep compiling). |
| `pkg/tools/email.go::gateMailDial`, per-tool `::SetMailBudget` | **consumed (W4/W10)** | The tool-side outer wrapper — the second of the three budget owners. **One budget owner per operation** (§4.11) means this wrapper keeps owning the account gate and the injected client must not acquire a second slot for the same dial. |
| `pkg/email/watcher.go::Watcher` / `::Cycle` / `::probe` | **modifies** | One mailbox's cycle: `cycleIfDue` (skip while `NextAttemptAt` in the future) → `probe` (budget `TryCall`; `MailboxStatuser` one-STATUS path, else unseen-only `ReadInbox`) → `recordSuccess`/`recordFailure`. `recordFailure` accepts the raw error text (the ADR's noted audit seam — sanitizing it is W4's logging policy, not W1's). `classifyMailError` maps to the closed class enum; its text-contains `folder`→`folder_missing` rule is broad — absence still requires the structural `[NONEXISTENT]` response (`pkg/email/view.go::isNonexistentFolder`), never this classifier. |
| `pkg/email/watcher.go::WatcherBackoff` | **unchanged** | 60 s base, doubling loop to the 15-min nominal cap, `auth_failed` straight to cap, ±20% jitter (factor 0.8–1.2 — the effective cap window is 12–18 min). Preserve exactly; the pool must not add a parallel reconnect loop. |
| `pkg/email/watcher.go::WatcherInitialOffset` | **kept** | First-cycle stagger: index × 1-minute cycle interval (`watcherCycleInterval`). |
| `pkg/email/watcher_set.go::MailboxWatcherSet.CycleAll` | **modifies** | Verified **sequential** `for` loop over `provider.Mailboxes()` with stagger + `cycleIfDue`; the ADR correctly notes the loop's shape is not proof a stalled account cannot delay others — a stalled mailbox's cycle blocks the rest of the pass today. §4.10 replaces it with bounded fair scheduling. |
| `pkg/email/view.go::FolderCounts` / `::ReadFolderPage` / `::ReadView` / `::MarkSeenIn` / `::DeleteDraftStatus` / `::ResolveRef` | **W2's file — consumed via the lease** | All dial via `c.dialIMAP(ctx)` + `defer client.Close()`; per-folder SELECT via `selectFolder`; fetches are `BODY.PEEK` (no flag writes on reads). `DeleteDraftStatus` sets `\Deleted` and expunges the exact UID where UIDPLUS exists — deferred delete semantics the pool's release path must respect (**a release/close must never issue a folder-CLOSE that would expunge pending `\Deleted` messages**; §4.6/MC-13). W2 owns rewiring these onto W1's lease; W1 never edits `view.go`. |
| `pkg/email/imapserver_test.go::startMemIMAP` | **test harness** | In-memory `go-imap/v2` server on loopback TCP; appends real messages through a raw client; swaps the `imapDial` seam (restored via `t.Cleanup`); returns a wired `*Client`. Real protocol, no new dependency. W5's new tests extend this harness with dial/LOGIN/SELECT counters and controlled stalls (§8). |
| `pkg/email/view_missing_folder_test.go` | **regression to preserve** | `TestFolderCounts_MissingSentFolderStillOpensMailbox`, `TestFolderCounts_MissingDraftsFolderStillOpensMailbox`, `TestFolderCounts_CancelledRequestStillFails` — must keep passing unchanged through the pool rewrite. |
| `pkg/email/mail_budget_red_test.go` | **regression to preserve** | `TestMailBudget_BackoffRefusalSymmetric`, `TestMailBudget_SemaphoreCapTwoHoldsUnderRetry`, `TestMailBudget_SingleflightOneDial`, `TestMailBudget_SingleflightKeyIncludesParams`, `TestMailBudget_WatcherTryAcquireSkipsNonBlocking`, `TestMailBudget_WatcherBackoffAndCallersAgree`. The singleflight-key test pins `flightKey`'s inclusion of params — it is updated by W5 only to the new identity shape, never weakened. |
| `pkg/email/watcher_budget_red_test.go`, `::watcher_backoff_red_test.go`, `::watcher_cycle_if_due_red_test.go`, `::append_timeout_red_test.go`, `::dial_timeout_classification_test.go` | **regression to preserve** | Budget-skip-is-not-failure, cycle-through-budget, backoff ladder bounds, `cycleIfDue` never dials in backoff, append returns promptly on deadline, dial-error classification. |

### 2.2 Impact assessment (Inferred — no GitNexus index in this worktree; first-hand Read/Grep sweep)

| Symbol modified | Risk | d=1 (will break / must adapt) | d=2 (likely affected) |
|---|---|---|---|
| `transport.go::dialIMAP` (split into establish-session + lease-select) | **HIGH** — every mail read on every path flows through it | All `pkg/email` methods that call `dialIMAP` (W2 rewires `view.go`; `transport.go`'s own `ReadInbox`/`Search`/`ReadMessage`/`MailboxStatus` are W1's); `startMemIMAP` harness (keeps working — same `imapDial` seam) | `pkg/tools/email.go` tools (via `Transport` interface — unchanged signatures), gateway handlers (via `mailPairClient`) |
| `mail_budget.go::MailBudgetRequest` (+ pair/generation/purpose fields) | **MEDIUM** | `mailBudgetWrap` (W4), `gateMailDial` (W4/W10), `watcher.go::probe` (W1) — all construct the struct literally; new fields need values or explicit defaults | `pkg/tools/email_no_retry_param_test.go` and the budget RED tests (W5 updates to the new identity) |
| `mail_budget.go::flightKey` (new identity) | **MEDIUM** | `TestMailBudget_SingleflightKeyIncludesParams` (W5 revises); every coalescing behavior test | Panel N-tab coalescing behavior (same visible outcome, new key) |
| `watcher_set.go::CycleAll` (sequential → bounded fair) | **MEDIUM** | `heartbeat.NewMailWatchService` consumer (call shape unchanged), `specapi/watcher_dial_red_test.go` | Badge freshness timing under many mailboxes |
| `transport.go::Client` (+ injected session source) | **MEDIUM** | `NewClient` call sites: `mailPairClient`, `registerEmailToolsForAgent`, `buildMailboxes` (W4 injects); test constructors (nil source = legacy path, §4.1) | None beyond — `Transport` interface signatures unchanged |

No CRITICAL blast radius: the `Transport` interface method set is unchanged (implementations adapt internally); the three production `NewClient` sites are all named and all owned by W4's injection wave; no wire type changes in W1.

### 2.3 Execution flows touched

| Flow | Today | After W1 |
|---|---|---|
| Panel folder counts (`handleMailFolders`) | `mailPairClient` → `mailBudgetWrap` (backoff→coalesce→2-slot) → `FolderCounts` → `dialIMAP` (dial+TLS+login+SELECT INBOX) → 3× STATUS → close | Same wrapper chain; the dial becomes a pool lease (reuse healthy session or bounded establish), exclusive per operation, retained only while a panel observer remains |
| Agent tool read (`read_inbox`) | `gateMailDial` → `ReadInbox` → `dialIMAP` → close | Same wrapper owns the budget; the dial borrows from the same pool — no private pool, no second account slot |
| Watcher cycle (`CycleAll` tick) | Sequential loop; `cycleIfDue` → `TryCall` (non-blocking) → `MailboxStatus` → dial+close | Bounded fair scheduling, ≤1 cycle in flight per mailbox; non-blocking pool reservation; socket released (not retained) after the cycle; skip leaves last-checked unchanged |
| Two pairs, one account, different Sent mapping, same list request | **One shared flight** (account+op+params key) — the first pair's result serves the second (I-01) | Two separate flights under the full coalescing identity; the account's 2-slot gate still bounds total concurrent dials |

### 2.4 Cluster placement

This work stays inside the **email transport/tools** cluster (`pkg/email`), with narrow consumed seams in the gateway/agent integration points W4 owns. No new product area, no wire change, no SPA file.

---

## 3. Published and consumed interfaces (parallel-work freeze)

W2 (discovery/metadata), W4 (gateway/agent integration) and W5 (tests) build against the shapes below **without editing each other's files**. Interface names are indicative Go names; the *shape and semantics* are what is frozen — a rename during implementation is fine, a semantic change goes back through the architect.

### 3.1 W1 publishes

| Interface (indicative shape) | Consumed by | Semantics frozen here |
|---|---|---|
| `MailSessions` — the application-owned connection manager, one process-wide instance per data dir (the pool analogue of `email.SharedMailBudget(stateDir)`) | W2 (`view.go` rewiring), W4 (injection at `mailPairClient`, `registerEmailToolsForAgent`, watcher provider) | `Acquire(ctx, LeaseRequest) (Lease, error)` and `Lease.Release()`. Never constructed by a `Client`. `LeaseRequest` carries the operation's pool identity (§4.2), the requested folder (or none, for STATUS-style probes), and whether the caller may retain (`eligible` — panel-observer work) or never retains (tool/watcher work). |
| `Lease` — exclusive borrower handle | W2 | Guarantees: sole ownership of the socket until `Release`; the requested folder SELECTed (or re-SELECTed) and validated before the first borrower command; SELECT data (`UIDValidity`, `NumMessages`) returned to the borrower for the epoch check; PEEK discipline on reads; release never expunges. A `Lease.SelectError` class distinguishes structural folder-absence from transport failure (W2's M-01 work depends on it). |
| Session-source injection on the facade — `Client` gains an optional session source (option/setter; nil = legacy per-call dial, tests only) | W4 | The three production construction sites pass the shared manager. A production dial with a nil source in a process where the manager exists is a **typed, visible error** naming the missing wiring — never a silent uncounted dial (§4.1, MC-1). |
| Extended `MailBudgetRequest` — `PairKey` (or reuse of existing `AgentID`+`WorkspaceID`), `Generation string`, `Purpose` (`read_live` \| `read_cache` \| `mutation` \| `watcher`), `TotalReadDeadline` | W4 (`mailBudgetWrap`), W10 (`gateMailDial`), W1's own watcher | The coalescing identity (§4.8) and the 45 s total-read-deadline plumbing (§4.4). `Retry` keeps its exact semantics: bypasses the backoff check only. |
| Revision capture seam — `RevisionSource` (read current revision for a pair/folder; compare captured-vs-current) | W2 (cache layers), W4 (implements the advancing events) | W1 defines the accessor the read path calls to capture the revision **before server work** and to test freshness **before publishing**; W4 owns the counter and the events that advance it (successful mutations, invalidations). W1 does not store the revision (§4.9). |
| Presence registry — `PanelPresence`: `Bind(connID, observerID, workspaceID)`, `Unbind(connID, observerID)`, `UnbindAll(connID)`, `Count(workspaceID)` | W4 (gateway socket handlers) | Observers bind to the **authenticated gateway connection's opaque ID issued by the gateway** — never a caller-supplied identity from REST bodies or query params. Until W4 registers the presence frame handlers, retention is structurally disabled (`RetentionEnabled() == false`) and every socket closes after its operation (§4.7). |
| Typed pool errors — pool-busy and pool-identity error values | W0 (contract reason enum), W4 (mapping) | Distinct from `ErrMailBusy` (account-slot busy): a pool-capacity exhaustion that survived its bounded wait (§4.6). W0 maps both onto the 503 `MailUnavailableError` `reason` extension. |

### 3.2 W1 consumes

| Interface | Provided by | Use |
|---|---|---|
| Folder name resolution and per-folder SELECT semantics | W2 (`view.go` — `folderNameFor`, `selectFolder` remain the folder authority) | The lease takes a resolved folder name (or none); W1 never interprets slugs, overrides, or discovery. W1 only guarantees exclusive selected-state per lease. |
| Generation values | W4 (`GenerationSource` — durable, restart-stable per §4.8) | W1 treats it as an opaque string participating in the coalescing identity; W1 never derives or stores it. |
| Revision counter and advancing events | W4 | W1 only captures and compares (§4.9). |
| Presence frames on the gateway WebSocket (`type=mail_panel_observer`, `action=open|close`) | W0 defines the contract; W4 implements | W1's registry is transport-agnostic; the WS integration is W4's. |
| Existing seams kept: `imapDial` (tests), `dialResolver`, `LoadWatcherState`/`EffectiveState`, `fileutil.WriteFileAtomic` | in-tree | No new dial stack, no second backoff derivation, no second state file. |

### 3.3 Files new and changed

| File | Status | Owner wave | Contents |
|---|---|---|---|
| `pkg/email/pool.go` | **new** | W1 | The manager: identities, ceilings, reservations, leases, idle/LRU, retirement, presence registry, revision-capture seam, typed errors. |
| `pkg/email/transport.go` | **changed** | W1 | `dialIMAP` split into session-establishment (dial+login, no INBOX SELECT) and lease-time folder selection; `Client` gains the optional injected session source; `AccountKey` untouched. |
| `pkg/email/mail_budget.go` | **changed** | W1 | I-01 identity fields + new `flightKey`; total-read-deadline plumbing; revision capture hook. Semaphore, backoff gate, `TryCall` skip semantics, `Retry` bypass unchanged. |
| `pkg/email/watcher.go` | **changed** | W1 | Probe path gains non-blocking pool acquisition (a pool-busy reservation attempt is a skip, like a slot skip); no retention; no cache interaction. Backoff/offset/state machine untouched. |
| `pkg/email/watcher_set.go` | **changed** | W1 | Bounded fair scheduling replacing the sequential loop (§4.10). |
| `pkg/email/view.go` | **changed — NOT by W1** | W2 | All lease call-site changes (`FolderCounts`, `ReadFolderPage`, `ReadView`, `MarkSeenIn`, `DeleteDraftStatus`, `ResolveRef` onto `MailSessions`). W1+W2 freeze the §3.1 lease shape first. |
| `pkg/gateway/rest_mail*.go`, `rest_mailbox.go`, boot/reload wiring, `pkg/agent/email_tools.go` | **changed — NOT by W1** | W4 | Inject the shared manager + generation/revision sources; presence frame handlers; typed-error mapping; removal cascades. |
| `contracts/*` + generated artifacts | **changed — NOT by W1** | W0 | 503 `reason` values, presence frames, `mode`/metadata fields. Nothing in W1 crosses the wire by itself. |
| test files (`pkg/email/pool_test.go`, `pool_poison_test.go`, `mail_budget_identity_test.go`, `watcher_fair_test.go`, …) | **new** | W5 | §8 names them. Production owners never edit test files. |

---

## 4. Design — the read runtime

The acquisition order below is normative and appears once here; every later section references it:

```text
operation start
   |
   |-- 1. backoff gate (unchanged: persisted watcher state; Retry bypasses ONLY this)
   |-- 2. coalescing join under the FULL identity (I-01, §4.8) — no slot held while waiting
   |-- 3. account slot: the existing 2-per-account semaphore (A8) — bounded by the total deadline
   |-- 4. pool reservation: ≤2 per mailbox identity, ≤8 global — counts connecting, atomic (§4.2)
   |       bounded wait ≤5 s; exhaustion → typed pool-busy, reservation released
   |-- 5. lease: reuse healthy idle session OR establish (dial ≤30 s; failed dial releases everything)
   |-- 6. exclusive selected state: re-SELECT/validate requested folder; borrower runs PEEK commands
   |-- 7. release: healthy + retention eligible → idle pool (2-min clock); else close (LOGOUT, never expunge)
   |       timeout / cancel / BYE / protocol failure → close AND retire, never reuse (§4.5)
```

### 4.1 One manager below the facade — no private pools

**Decision.** One application-owned connection manager (`MailSessions`) lives below the `Client` facade. It is a process-wide singleton resolved from the data dir — the same resolution discipline as `email.SharedMailBudget(stateDir)`, verified in `pkg/email/mail_budget.go::SharedMailBudget` — and it is **injected** into every client-producing path:

| Production construction site | Owner of the injection | Injects |
|---|---|---|
| `pkg/gateway/rest_mail.go::mailPairClient` (per-request client) | W4 | shared `MailSessions` |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent` (per-agent toolset clients) | W4 | shared `MailSessions` |
| `pkg/gateway/gateway_boot.go` watcher provider (`buildMailboxes`) and the reload twin | W4 | shared `MailSessions` |

**Rules.**

1. A `Client` never constructs a manager. `NewClient` gains an optional injection; the client delegates session establishment to it.
2. **A production dial with no injected source is a typed, visible error naming the missing wiring** — it must not silently fall back to an uncounted per-call dial, because a missed injection would otherwise become exactly the private dial path the design forbids. The nil-source path exists only for tests (same status as the `imapDial` reassignment seam: "production code never reassigns it").
3. The budget counts **work** (account slots); the manager counts **sockets** (reservations + established). Neither replaces or subsumes the other.
4. There is no per-path pool: panel, watcher and tools share one instance, so the two-per-mailbox and eight-global ceilings are honest process-wide counts.

**Blast-radius note (Inferred):** the three injection sites are all in W4's ownership columns per the ADR's work-package table; W1 ships the manager and the injection seam, W4 wires it. Until W4's wave lands, W1's code compiles and the legacy per-call dial remains the behavior in production — the switch is W4's single integration wave, not a flag flip scattered across packages.

### 4.2 Socket ceilings and reservation accounting

| Ceiling | Value | Counts |
|---|---|---|
| Per mailbox identity | **2** | Reservations (dial/login in progress) **plus** established sockets, idle or active |
| Global | **8** | Same — all mailboxes, all paths (panel, watcher, tools) |
| Per account (existing, unchanged) | **2 work slots** | Concurrent operations via `mailBudgetSlotsPerAccount` — the A8 gate, not sockets |

**Mailbox identity** (the pool key) binds, per the ADR's identity row: the agent/workspace **pair**, the configured endpoint (host:port) + TLS identity, and the **non-secret configuration/credential generation**. It never contains the password text. Two pairs that share `host:port|username` therefore hold **separate** pool identities and **never share a socket** — cross-pair socket, header or presence sharing is prohibited (ADR risk table, "Cross-pair reuse"); the account key keeps only its contention role. A generation change (reconfiguration, credential/endpoint change, removal) orphans the old identity's sockets: they are closed, and late completions cannot re-enter the new identity's pool.

**Reservation discipline.**

- The reservation is taken **atomically** against both ceilings before any dial begins (verified requirement, ADR P1.2 "Reserve global capacity atomically before dialing"). A test can only ever observe: reserved-but-connecting, established, or released — never an uncounted dial.
- A **failed dial releases every reservation it holds** (per-mailbox and global) before the error propagates. No leak on the error path.
- Connecting reservations count toward both ceilings: while a dial is in flight, that mailbox is at 1-of-2 and the process is 1-of-8, and subsequent acquisitions account for it.

### 4.3 Exclusive mailbox leases

**Decision.** One borrower owns a socket until its entire operation finishes. SELECT/EXAMINE state belongs to the lease, never to a simultaneous request.

1. **Re-select and validate before use.** On every lease acquisition — reused or freshly established — the manager (re-)SELECTs the requested folder and returns the SELECT data (`UIDValidity`, message count) to the borrower for its epoch check. A reused socket carries the *previous* borrower's selected state; skipping the re-select would fetch one folder's messages under another folder's name. `dialIMAP`'s current unconditional SELECT INBOX (verified) disappears from session establishment: establishment is dial+login only; folder selection is lease work. STATUS-style probes (`watcher.go::probe` via `MailboxStatus`) take a no-folder lease — STATUS does not require a selected folder — which also removes today's wasteful SELECT INBOX inside the watcher's one-command probe.
2. **PEEK preserves flags.** Every read on a lease fetches `BODY.PEEK` — already the invariant of `pkg/email/view.go`'s fetches ("the fetches behind them are all BODY.PEEK, so no flag is ever written", verified comment on `selectFolder`). The lease makes it structural: read commands on a lease must not write flags; only explicit mutation operations (`MarkSeenIn`, `DeleteDraftStatus`, draft APPEND, send-APPEND) write, and each goes through the budget as a mutation purpose (§4.8) with **no coalescing and no silent replay** (§4.5).
3. **Structural folder absence is a select outcome, not a transport failure.** A SELECT answered with the server's structural not-found (`[NONEXISTENT]`, the class `pkg/email/view.go::isNonexistentFolder` detects) surfaces as a typed select-error on the lease; the socket stays healthy and returns to the idle pool. Network/timeout/auth failures during SELECT poison the socket (§4.5). W2's unknown-vs-absent classification (M-01) consumes this distinction.
4. **Same-lease validation seam.** The lease exposes the selected folder's live epoch to the borrower *before* commands run, so W2 can compare a reference's embedded `uidvalidity` and generation on the same lease that performs the fetch or mutation — replacing the verified resolve-on-one-session/mutate-on-another split in `pkg/gateway/rest_mail_read.go::handleMailSeen`. W1 provides the capability; W2's spec owns rewiring the callers.

### 4.4 Deadlines and lock order

| Bound | Value | Applies to |
|---|---|---|
| Total read-work deadline | **45 s** | The whole pooled read: account-slot queue + pool wait + establish (if any) + all commands. Founder-accepted (recorded Q3=A). |
| Pool-acquisition wait | **5 s maximum** | Waiting for a reservation inside the ceilings. Founder-accepted. Exhaustion → typed **pool-busy**, not a timeout. |
| Dial ceiling (existing) | **30 s** (`dialTimeout`, verified) | Subordinate bound inside the total; the dial can never outlive the read it serves. |
| Per-command bound (existing) | **45 s** (`commandTimeout` via `runIMAP`, verified) | Retained per command, but now subordinate to the remaining total — a command starts only if meaningful time remains, and its effective bound is min(45 s, remaining total). |
| Watcher cycle | unchanged cadence/backoff; the STATUS probe rides the same per-command bound | The watcher is **not** a 45 s-deadline read; its bounds are cadence + backoff + per-command (§4.10). |

**Lock order (normative):** coalesce **before** the account slot (a joiner holds nothing while waiting); the account slot **before** the pool reservation; the reservation **before** any dial. No manager lock is held during network I/O, and no socket borrower ever waits to acquire a second account slot (both verified requirements, ADR P1.2). This order is deadlock-free by construction: every lock is acquired in one direction, and the only waits (slot, reservation) happen while holding nothing but the caller's context.

**Plumbing.** The budget layer stamps the total deadline on read-shaped operations (`TotalReadDeadline` on the request; REST's `r.Context()` carries no deadline today and an agent turn's context is the 30-minute turn — neither is a read deadline). A caller-supplied **shorter** deadline always wins; the 45 s is a ceiling, never an extension.

**Failure mapping.** 5 s pool-wait exhaustion → typed pool-busy (distinct value, W0 maps to 503 `reason=pool_busy`). Total-deadline exhaustion while queued on the account slot → the existing `ErrMailBusy` shape (`reason=account_busy`). Deadline exhausted after dial → the existing timeout class. Cache timestamps never advance on any failure (verified requirement, ADR failure table).

### 4.5 Poisoned connections

**Decision.** A socket that experienced any of — command timeout, cancellation, server BYE, protocol failure — is **closed and retired**: it can never return to the idle pool for reuse.

1. **Retire on the four causes.** Timeout and cancellation: the borrower's `runIMAP` deadline or context fires. Server BYE: the server ended the session. Protocol failure: an unexpected/malformed response, or a command that completes at the transport level while the session is no longer coherent. All four mark the socket poisoned; close is mandatory, reuse is forbidden.
2. **Wait for command-reader termination before any reuse decision.** The go-imap client runs a command reader; a buffered goroutine completion is not proof of a healthy session (verified ADR wording). The retirement path must observe reader termination (library close acknowledgment) before the socket is counted as gone. The *test* for this is behavioral: no subsequent borrow may observe interleaved or stale responses (§8, BDD-15).
3. **One dead-idle replacement for a read.** An idle socket that proves dead at borrow time (first command fails with a connection-class error) may be replaced **once** within the same read: retire the dead socket, acquire again under the same ceilings and the same remaining deadline — never a fresh 45 s — and retry the read. The retry budget is one replacement per read, inside the original total deadline and backoff rules (verified requirement, ADR failure table "Failed idle connection").
4. **Never silently replay a mutation.** The dead-idle replacement applies to **reads only**. A mutation (`MarkSeenIn`, draft save/delete, send-APPEND) that hits a dead or poisoned socket fails visibly with a retryable transport error; the caller (human Retry, agent turn, explicit re-invoke) decides whether to repeat it. There is no automatic second attempt for any mutation, and no coalescing/replay of mutations as reads (verified: ADR P1.2 "mutations must not be coalesced/replayed as reads").
5. **Poisoned ≠ folder-missing.** A `[NONEXISTENT]` SELECT answer (§4.3) does not poison: the session is healthy, the folder is absent. Only transport/protocol-level failure retires.

### 4.6 Idle release, LRU eviction, and the all-busy case

1. **Two-minute idle release.** A socket whose **last completed use** is older than ~2 minutes is closed. The clock is last-completed-use (the end of the last operation), not last-borrowed, so a long operation cannot see its socket expire underneath it. Founder rule; "approximately" — the check runs on the manager's expiry sweep, and the sweep is the only timer W1 introduces (a socket/resource timer, not a mail-data poll).
2. **LRU eviction, never active.** When a new reservation needs capacity and no socket is free, the manager evicts the least-recently-completed **idle** socket (global LRU across mailbox identities). An active socket — reservation, lease, or command in flight — is never evicted "to warm another mailbox" (verified founder rule).
3. **All eight active → bounded wait, then typed busy, never a ninth socket.** A request that cannot get a reservation waits up to 5 s (§4.4); if still nothing, it fails with typed pool-busy. No dial is ever attempted outside the ceilings, under any contention pattern.
4. **Release never expunges.** Release, idle close and eviction tear the session down with LOGOUT-class semantics only. An IMAP CLOSE on a folder with pending `\Deleted` flags would expunge those messages — and `pkg/email/view.go::DeleteDraftStatus` deliberately *leaves* `\Deleted` set on servers without UIDPLUS (the draft old-copy stays hidden from listings until a later exact expunge). A pool close that issued CLOSE would silently expunge mail the design keeps. **Release/close/eviction must therefore never send CLOSE on a selected folder.** (MC-13 pins this server-side.)
5. **Retention is observer-gated, never lease-gated.** Release logic: healthy socket + retention enabled + ≥1 eligible panel observer for the socket's workspace → return to idle pool; otherwise close immediately. A detached flight finishing after the last observer left closes its socket; it never returns a socket to retention (verified ADR rule).

### 4.7 Panel-observer presence

**Decision.** Panel-requested sockets may be retained only while at least one authenticated Mail panel observer for that workspace is open. Presence is a W1-published registry (§3.1); the WebSocket integration is W4's.

1. **Bound to the authenticated connection, never a caller-supplied identity.** The registry keys observers by the **gateway-issued opaque connection ID** of the authenticated WebSocket + an opaque per-panel observer ID (one per tab). A REST request **cannot** claim ownership by presenting someone else's observer ID: REST carries no observer authority at all — observer association happens only through the authenticated socket's own frames, validated against that connection's session and workspace authorization. This is the verified ADR rule ("Bind observers to the authenticated connection, not a caller-supplied session identity"; "REST cannot claim ownership using another socket/user's ID").
2. **Per-tab observers.** Two tabs = two observers. Closing one tab unbinds only its observer and cancels only its subscriptions; a shared read finishes for the remaining owners. The shared operation is cancelled only when **no** request/observer still depends on it.
3. **What removes an observer:** explicit close frame; socket disconnect (the gateway's existing liveness teardown reaps lost connections — no new mail-data polling timer); logout; workspace exit. Any of these unbinds that connection's observers; when the last observer for a workspace goes, that workspace's **idle panel sockets close immediately** and active ones close when their operation completes (independent watcher/tool work is not interrupted).
4. **Conservative default until acknowledged.** Until W4 registers the presence frame handlers, `RetentionEnabled()` is false and **every** socket closes after its operation — request-scoped connections are the default, exactly as the ADR prescribes ("Until presence is acknowledged, use conservative request-scoped connections"). Retention cannot exist before the contract does; there is no "assume the panel is open" mode.
5. **No cache coupling in the registry.** Presence decides socket retention only. It does not refresh caches, does not extend the 30-minute header retention clock, and does not trigger discovery (W2/W3 own those, event-driven).

### 4.8 Read-coalescing identity (grill correction I-01)

**Decision.** The retained A8 singleflight layer shares results **only** between requests with the same **full coalescing identity**:

| Component | Source | Role |
|---|---|---|
| Pair | `AgentID` + `WorkspaceID` (already on the request; **ignored by today's key** — verified) | Result sharing never crosses pairs |
| Configuration/folder-mapping generation | W4's `GenerationSource` (opaque, restart-stable, changes on endpoint/credential/override change) | An old generation never joins or receives a new generation's flight |
| Operation | existing `Operation` field | as today |
| Normalized arguments | existing `Params` (JSON-marshaled; empty map = coalescing opt-out, preserved) | as today |
| Live-versus-cache purpose | new `Purpose` (`read_live` / `read_cache` / `mutation` / `watcher`) | a `mode=cache_first` read and a `mode=live` refresh for the same folder are **different flights** — a live refresh can never be answered by joining a cache-shaped flight and reporting it as fresh (verified ADR "visible cached refresh shape" rule) |

The new `flightKey` is the concatenation of all five. The **account key (`host:port|username`) keeps only its contention role**: it keys the two-slot semaphore (`gateFor(req.Account)`) and nothing else — it never decides result sharing. Mutations (`Purpose=mutation`) never coalesce at all, regardless of identity equality. Watcher cycles keep `TryCall`'s never-coalesce rule.

**Cancellation and publication use the same identity.** A flight's completion can neither cancel nor publish for a different identity's request: joiners are matched by the full key (singleflight semantics on the new key give this), and the flight's captured revision (§4.9) travels with the same identity. Today's verified joiner rules are preserved: a joiner whose own context ends stops waiting without failing the flight; the flight runs on the detached `flightContext` so one tab's cancel cannot fail the others.

### 4.9 Folder publication revision (grill correction I-02)

**Decision.** Every mailbox/folder carries a local, monotonically advancing **publication revision**, owned by the gateway/cache service (W4 advances it; W1 captures and compares it; W2's caches obey it).

1. **Capture before server work.** A read captures the revision current at operation start — in the budget layer, before any dial — and carries it with the flight.
2. **Advance on the listed events only.** Successful mutations (own mark-read/send/draft changes) and every invalidation event the ADR enumerates (P1.3/P2.3 tables) advance the revision. No polling timer, no time-based advance.
3. **A superseded read publishes nothing.** If the captured revision is no longer current when the read finishes, the result is discarded: no rows into any memory cache, no disk snapshot write, no last-validated/fetched timestamp advance, no frontend state or count update. The data is simply dropped; the already-scheduled post-mutation refresh supplies truth. This applies equally to memory cache, encrypted disk snapshots, and delayed frontend responses (the response's opaque `publication_revision` field lets the SPA drop an out-of-order response — W0's contract).
4. **A post-mutation refresh never joins a superseded flight.** Because the generation participates in the coalescing identity and the revision is captured at flight start, a refresh issued after a revision-advancing event cannot share a flight that started under the older revision — I-01 and I-02 compose structurally, not by convention.
5. **W1's obligation is the seam, not the store.** W1 defines `RevisionSource` (capture/compare, §3.1) and wires capture into the read path. W4 implements the counter and the advancing events. W2's caches and W3's UI consume the response metadata. W1 never stores or advances the revision itself.

### 4.10 The watcher under the same caps

**Decision.** The watcher keeps its independence, cadence and budget (verified: `watcherCycleInterval` 1 min, `WatcherBackoff` 60 s→15-min nominal cap ±20% jitter, `auth_failed` straight to cap; closed-panel new-mail notice preserved; no mail-triggered turn or task) and gains three changes:

1. **Bounded fair scheduling replaces the sequential loop.** `CycleAll` runs due mailboxes with bounded parallelism — at most **one cycle in flight per mailbox** (a mailbox whose previous cycle is still running is not started again), and a global bound consistent with the account and pool ceilings (watcher cycles are non-blocking throughout, so the effective concurrency is capped by availability, not by a large worker pool). One stalled mailbox no longer delays the others' due checks — the verified gap in today's `for` loop. The first-cycle stagger (`WatcherInitialOffset`) and the `cycleIfDue` backoff gate are preserved exactly.
2. **Foreground ahead of watcher, without starving due cycles.** Watcher work is optional and takes **only** what is free: it keeps `TryCall`'s non-blocking account acquisition, and its pool reservation attempt is likewise non-blocking — a watcher cycle that cannot get a slot or a reservation **skips** (recorded as a skip, never a failure: last-success time unchanged, backoff not advanced — verified `ErrMailSkipped` semantics). Foreground requests, which may wait up to 5 s, therefore always outrank watcher work under contention. Due watcher cycles are not starved *by design*: any cycle that finds capacity runs; sustained saturation shows up as honest skipped-and-dated state, which is the accepted posture (ADR failure table "Watcher has no free slot").
3. **No socket retention, no cache fill.** A watcher cycle reuses a healthy **eligible** idle socket while a panel is open (sharing is fine; it is bounded by the same lease rules), but never causes retention: with no panel observer the cycle's socket is closed after the cycle, and 13 mailboxes can never hold 13 watcher sockets. Watcher activity never populates or refreshes panel folder/header/count caches and never prolongs their retention clock; a folder/version change observed by the watcher only marks panel metadata dirty for the next eligible panel event (W2/W4 consume that mark). A skipped cycle leaves the prior last-checked time unchanged — the badge cannot claim a fresh check that did not happen.

**Explicit agent work while the panel is closed** (verified ADR P1.4): a tool-invoked read/write runs live under A8/pool, validates only what that operation needs, releases its request-owned resources, and never populates/refreshes panel caches or extends their retention. Closing Mail must not stop an agent turn or make an already-permitted tool depend on an open UI.

### 4.11 Budget interplay — one owner per operation (no double acquisition)

**Decision.** The A8 gate's ownership does not move. The **outer wrapper** of each path remains the single budget owner for its operations; the injected client/pool never acquires a second account slot for a dial the wrapper already gated:

| Path | Budget owner (unchanged) | What the pool adds |
|---|---|---|
| REST panel reads | `pkg/gateway/rest_mail_budget.go::mailBudgetWrap` | Socket lease only, after the wrapper's account slot is held |
| Agent tool reads | `pkg/tools/email.go::gateMailDial` (+ per-tool `SetMailBudget`) | Socket lease only |
| Watcher cycle | `pkg/email/watcher.go::probe` via `Budget.TryCall` | Non-blocking reservation only |

**Why this matters (the double-acquire failure):** if an injected client *also* called the budget internally, every panel read would consume two of the account's two slots for one operation — one self-deadlocking wrapper plus one innocent-looking "safety" gate — halving capacity and, in the worst interleaving, deadlocking against itself. The rule is structural: the session source's `Acquire` has **no budget parameter and performs no account-slot acquisition**; the type system makes a double acquire unwritable rather than merely forbidden by comment. Verified grounding: today's wrappers pass `client.AccountKey()` into the request and the client has no budget field at all — W1 preserves exactly that separation.

**Retry semantics stay pinned:** `retry=true` bypasses only the backoff gate — never the semaphore, never the pool ceilings, never TLS validation, never mutation safeguards (verified in `backoffRefusal` and the ADR failure table). An automatic refresh never sets it. `Purpose=read_live` vs `read_cache` does not interact with `Retry` beyond that.

---

## 5. User stories and acceptance criteria

### US-1 — Repeated reads stop repeating connection setup (P0)

A user browsing their Mail panel clicks between folders and mailboxes. Today every click pays the full connection setup again — TLS handshake, login, folder select — before the first byte of data. After this work, a repeat read within the idle window reuses the still-healthy authenticated session, so the second and later reads skip the setup work; the *first* read still pays the server's own delay, and nothing is prewarmed or fetched while the panel is closed.

**Why this priority:** this is the core of the founder's pooling decision and the only change that can move the supplied 2.5–22.8 s folder/list timings.

**Independent test:** with one configured mailbox against the fake IMAP server (connection counters in test code), open folders, list, open a message, list again — assert the server accepted strictly fewer connections than operations and that every response is correct against server state.

**Acceptance scenarios:**

1. **Given** a configured mailbox and a completed first read, **When** a second read for the same mailbox runs within the idle window, **Then** it completes without the server accepting a new connection, and its results match server state.
2. **Given** a pooled session that was just used, **When** the next operation is for a *different* folder of the same mailbox, **Then** the result reflects that folder — never the previously selected one.
3. **Given** a first read that must establish a connection, **When** it completes, **Then** the server observed exactly one connection setup (dial + login) for that read, and later reads did not add more while reuse was possible.
4. **Given** the panel closed and the idle window elapsed, **When** no request arrives, **Then** no mail server connection remains, and no mail-data request of any kind is issued while closed.

### US-2 — The ceilings hold under any pressure (P0)

Mail never opens more than two connections per mailbox or eight process-wide — including connections still being established — no matter how many tabs, mailboxes, agent tools and watcher cycles demand service. When capacity is gone, the requester gets a typed busy outcome within its bounded wait; a ninth socket is never created.

**Why this priority:** the ceilings are founder-set resource limits (Hard-Constraint-adjacent: bounded footprint); exceeding them is a correctness failure, not a tuning issue.

**Independent test:** fake-server connection counters under scripted concurrency (13 mailboxes, two configured pairs sharing one server account, delayed dials): assert the maximum simultaneously accepted connections is never above the ceilings and every refusal is the typed busy outcome.

**Acceptance scenarios:**

1. **Given** two reads already in flight for one mailbox, **When** a third read for that mailbox starts, **Then** it waits at most the bounded acquisition window and then receives a typed busy outcome — and the server never sees a third connection for that mailbox.
2. **Given** eight operations in flight across mailboxes, **When** a ninth demand arrives anywhere (panel, tool, watcher), **Then** it waits its bounded window and then receives the typed busy outcome; total server connections never exceed eight.
3. **Given** a dial that is still connecting (reservation held, no socket yet), **When** other demands are counted against the ceilings, **Then** the connecting reservation is counted — the ceilings are not fooled by "not yet established".
4. **Given** a dial attempt that fails, **When** the failure is reported, **Then** every reservation that dial held has been released and later demands can acquire them.

### US-3 — One socket, one owner: selected state never leaks (P0)

Two simultaneous requests that borrow (different) sessions of the same mailbox — or the *same* session at different times — each see exactly the folder they asked for, with the read flags of messages untouched by reads. No request ever reads another's selected-folder state.

**Why this priority:** cross-folder leakage returns wrong data silently — the worst failure class this design can ship.

**Independent test:** two concurrent borrowers request different folders while the server pauses one command mid-flight; assert each borrower's rows come from its own folder and no flag changed on any fetched message.

**Acceptance scenarios:**

1. **Given** a session last used for Sent, **When** a new borrower leases it for Inbox, **Then** the Inbox rows returned are Inbox's, and the server was asked to select Inbox before the fetch.
2. **Given** a pooled read of any folder, **When** its messages are fetched, **Then** no message's read/unread state changes on the server because of the read.
3. **Given** two simultaneous borrowers on different sessions of one mailbox, **When** one is paused mid-command, **Then** the other completes with its own folder's correct data.
4. **Given** a folder the server structurally reports as nonexistent, **When** a read targets it, **Then** the caller receives the structural absence outcome (not a transport error), the session stays healthy, and later reads still succeed.

### US-4 — Every read finishes inside its stated bounds (P0)

A read either completes or fails with a named outcome within 45 seconds total — including queueing for shared capacity — with connection establishment capped at 30 seconds and the wait for pool capacity capped at 5 seconds. A failed establishment releases everything it reserved. Locks are acquired in one direction only; nothing waits on a lock while holding the network.

**Why this priority:** the founder accepted these numeric bounds (recorded Q3=A); "no silent multi-attempt query retry may turn a bounded failure into minutes of loading" is the surviving hotfix requirement.

**Independent test:** fake server with controlled stalls at greeting, TLS/login, select and fetch; assert every outcome (success, typed busy, timeout class) arrives within its bound, and reservation counters return to baseline after failures.

**Acceptance scenarios:**

1. **Given** all pool capacity busy, **When** a read waits 5 seconds without acquiring, **Then** it returns the typed pool-busy outcome — not a timeout, not an indefinite wait.
2. **Given** a server that accepts but never responds, **When** a read runs, **Then** it ends by the total deadline with the timeout class, and its socket is not reused afterwards.
3. **Given** a server that stalls the handshake, **When** the dial is attempted, **Then** establishment ends within the 30-second dial ceiling, inside the total.
4. **Given** an operation queued behind the account's two in-flight operations, **When** the total deadline expires first, **Then** the caller receives the existing busy outcome and never dialed.
5. **Given** any interleaving of coalescing joins, account-slot waits and pool waits, **When** the system runs under load, **Then** no request waits for one resource while holding another in the opposite order (no deadlock is reachable).

### US-5 — A poisoned connection poisons nothing else (P0)

Timeouts, cancellations, server goodbyes and protocol failures retire the socket they happened on. A retired socket is closed, never silently reused, and its replacement obeys the same bounds. An idle socket that turns out dead may be replaced once for a read. A mutation is never automatically replayed.

**Why this priority:** stale-session reuse produces mixed or wrong responses — the ADR's "dead socket reused" risk — and silent mutation replay can act twice.

**Independent test:** scripted server sending BYE / stalling commands / closing idle sessions; assert retired sockets are closed, readers terminate, one replacement maximum per read, and a mutation on a dead session fails visibly without a second attempt.

**Acceptance scenarios:**

1. **Given** a command that times out, **When** the socket is examined afterwards, **Then** it is closed and can never be borrowed again; the next borrow establishes or reuses a *different* healthy session.
2. **Given** the server sends BYE, **When** the affected operation ends, **Then** the outcome is a visible error of the right class and the socket is retired.
3. **Given** an idle pooled session the server has silently closed, **When** a read borrows it and the first command fails with a connection-class error, **Then** the read replaces the dead session exactly once — inside the original bounds — and succeeds or fails visibly; never two replacements.
4. **Given** a mutation (mark-read, draft save, send-copy) whose session proves dead mid-operation, **When** the failure surfaces, **Then** the mutation is reported as failed-and-retryable and no automatic second attempt is made.
5. **Given** a cancellation of one waiting requester, **When** its shared read still has other live joiners, **Then** the read completes for them; only the cancelled requester's wait ends.

### US-6 — Identical reads share; different-meaning reads never do (P0)

Two requests share one server round-trip **only** when they come from the same agent/workspace pair, the same configuration generation, the same operation, the same normalized arguments, and the same live-versus-cache purpose. A read that was started before a change it cannot see (a mutation, an invalidation) publishes nothing anywhere when it finishes. And a refresh issued after a mutation never receives the pre-mutation flight's answer.

**Why this priority:** these are grill corrections I-01 and I-02 — the two strongest findings against the approved design; shipping without them ships wrong-data-by-sharing and stale-data-by-publication.

**Independent test:** two pairs configured on one server account with different Sent folder mappings, plus a scripted reconfiguration mid-flight and a paused-read/mutation/refresh ordering — assert separate results per identity, no cross-pair or cross-generation sharing, and no superseded publication to memory, disk or response.

**Acceptance scenarios:**

1. **Given** two pairs sharing one server account but configured with different Sent folder mappings, **When** both issue the same concurrent folder list, **Then** each receives its own mapping's result — and the account's two-operation cap still bounds their combined dials.
2. **Given** an in-flight read under an older configuration generation, **When** the configuration changes (new generation) and the same read is issued again, **Then** the new read does not join, receive, or cancel the old flight, and both callers get their own generation's answer.
3. **Given** a cache-shaped read and a live refresh for the same folder running concurrently, **When** both complete, **Then** the live response is labeled and *is* live data — it was never satisfied by the cache-shaped flight.
4. **Given** a read paused after collecting its server data, **When** a successful mutation completes and advances the publication revision before the paused read resumes, **Then** the paused read publishes nothing — no rows, no timestamps, no state change anywhere — and the post-mutation refresh's data stands.
5. **Given** a refresh requested after a mutation, **When** it starts, **Then** it does not join any flight started before the mutation (its identity differs by revision and purpose).

### US-7 — Sockets stay only while a panel actually watches (P1)

Panel-requested connections may be kept warm only while at least one authenticated Mail panel for that workspace is open. Presence is bound to the authenticated gateway connection — an ID the gateway issues per socket — with one observer per tab; closing a tab, closing the browser, logging out, leaving the workspace, or dying connection each remove that connection's observers, and the last observer's departure closes that workspace's idle panel sockets immediately (active ones when they finish). Until the presence channel exists, nothing is retained.

**Why this priority:** the founder's "no panel-owned connections while the panel is closed" rule; P1 because it lands with the W4 integration wave.

**Independent test:** scripted observer lifecycle (two tabs, close one, close both, drop the socket, logout) with socket counters: assert retention tracks the last observer exactly and tool/watcher work is never cancelled by UI disappearance.

**Acceptance scenarios:**

1. **Given** one open panel observer and completed reads, **When** the idle window has not elapsed, **Then** the panel's session may remain open for reuse.
2. **Given** two tabs open and one closes, **When** the remaining tab still observes, **Then** retention continues and the closed tab's subscriptions alone are cancelled.
3. **Given** the last observer for a workspace closes, **When** its reads have finished, **Then** its idle sockets close at once and no panel-owned connection survives.
4. **Given** a shared read in flight when the last observer departs, **When** the read completes, **Then** its socket is closed, not retained; other owners' (tool/watcher) work is untouched.
5. **Given** a browser that vanished without a close message, **When** the gateway notices the dead connection, **Then** its observers are removed and the same close rules apply.
6. **Given** the presence channel not yet wired, **When** any read completes, **Then** its socket is closed — request-scoped behavior only, no retention anywhere.

### US-8 — The watcher shares the pool without abusing it (P0)

The new-mail watcher keeps its one-minute cadence, its backoff, and its closed-panel independence; it runs its due cycles with bounded fairness so one slow mailbox cannot stall the others; at most one cycle per mailbox runs at a time; under pressure it skips honestly (the last-checked time does not move); and it never keeps sockets without a panel, never fills panel caches, and never turns mail into an agent turn or task.

**Why this priority:** the watcher is the only mail surface that works while the panel is closed — its honesty (badge, last-checked) and its non-interference with foreground reads are both founder-set.

**Independent test:** 13 mailboxes with one stalled, scripted capacity saturation, and a closed panel through several intervals — assert other due mailboxes still progress, skips leave last-checked unchanged, no retained watcher sockets, no cache writes, and backoff/skip bookkeeping matches the existing rules.

**Acceptance scenarios:**

1. **Given** thirteen due mailboxes of which one stalls, **When** the cycle pass runs, **Then** the other twelve complete their checks within the pass — none is queued behind the stalled one.
2. **Given** a mailbox whose previous cycle is still running, **When** its next due moment arrives, **Then** no second cycle for that mailbox starts.
3. **Given** the account's slots and the pool are fully occupied by foreground work, **When** a watcher cycle becomes due, **Then** it skips without changing the last successful-check time and without advancing backoff.
4. **Given** the panel closed, **When** watcher cycles run, **Then** their sockets close after each cycle (none retained), no folder/header/count cache is written or refreshed, and a folder change the watcher notices only marks panel metadata dirty for the next panel-open event.
5. **Given** a watcher cycle, **When** it probes, **Then** it changes no message flags and starts no agent turn and creates no task.

---

## 6. Behavioral contract and boundaries

### 6.1 When/Then quick reference

| When | Then |
|---|---|
| A pooled read completes within the idle window and a same-mailbox read follows | The second read reuses the session; no new server connection is accepted |
| A third concurrent read targets a mailbox already at its two-socket ceiling | It waits ≤5 s, then fails with the typed pool-busy outcome; no third socket |
| Nine operations are in flight process-wide and a tenth demand arrives | It waits ≤5 s, then fails typed-busy; total connections never exceed 8 |
| A dial is connecting | Its reservation counts against both ceilings |
| A dial fails | All reservations it held are released before the error propagates |
| A session is borrowed | The requested folder is (re-)selected and validated before the borrower's first command |
| A read fetches messages | No flag changes server-side (PEEK discipline) |
| A command times out / is cancelled / the server says BYE / the protocol breaks | The socket closes and is retired — never borrowed again |
| An idle session proves dead on borrow (read) | Exactly one in-bounds replacement, inside the original 45 s |
| A mutation hits a dead/poisoned session | Visible retryable failure; no automatic second attempt |
| Total deadline (45 s) expires | Named outcome (busy if queued; timeout class if dialing/running); no cache timestamp advances |
| Two identical reads share one flight | Only when pair + generation + operation + normalized args + purpose all match |
| Two pairs on one account issue the same read | Two flights, two results; the account's two-slot cap still bounds combined dials |
| A read finishes after its captured revision was superseded | It publishes nothing anywhere (memory, disk, timestamps, response state) |
| A mutation or invalidation occurs | The folder's publication revision advances; no timer is involved |
| The last panel observer for a workspace departs | Idle panel sockets close immediately; active ones close when their operation ends |
| The presence channel is not wired | Every socket closes after its operation (request-scoped only) |
| A watcher cycle cannot get a slot or reservation | It skips; last-checked unchanged; backoff not advanced |
| A mailbox's previous cycle is still running | No second cycle for that mailbox starts |
| The panel is closed | Watcher cycles still run (independence), retain nothing, fill nothing |
| A socket is released, idled out, or evicted | It is torn down without a folder-CLOSE — pending `\Deleted` messages are never expunged by release |

### 6.2 Explicit non-behaviors and safeguards

The system must not:

1. **…construct a second pool.** A REST-created or tool-registered client must not build a private connection manager — one process-wide instance per data dir, injected at three named sites (§4.1), because N private pools would multiply the ceilings by N and make the founder's two/eight limits fiction.
2. **…count work as sockets or sockets as work.** The account slot bounds concurrent *operations*; the pool bounds *sockets*. Merging them (e.g., holding the account slot for the socket's whole retained life) would pin slots for idle sockets and starve other accounts' work.
3. **…coalesce or replay mutations.** Two identical mark-read requests are two operations; a failed send-copy is never re-attempted automatically — replaying a mutation can act twice on the user's mailbox.
4. **…share results across pairs, generations or purposes.** Sharing a Sent list configured for one pair with its sibling pair, or an old generation's answer with a new one, or a cache answer with a live refresh request, is wrong data delivered confidently (I-01's exact defect).
5. **…publish a superseded read.** After a mutation/invalidation advanced the revision, the older read's data must not reach memory, disk, timestamps or the user's screen — not even as "successful-looking" rows (I-02's exact defect).
6. **…evict or expire an active socket, or expire a socket mid-operation.** The idle clock runs from last completed use precisely so a long operation cannot lose its socket.
7. **…send a folder-CLOSE on release.** Pending `\Deleted` messages must survive release/eviction/idle-close (the non-UIDPLUS draft-delete design depends on it).
8. **…introduce a mail-data polling timer.** The only new timer is the socket/idle sweep. No folder/count/header refresh timer exists — not from the pool, not from the watcher, not from presence.
9. **…let watcher work displace or queue-block foreground reads.** Watcher acquisition is non-blocking everywhere; it takes only free capacity.
10. **…let a UI disappearance cancel independent work.** Closing a tab/panel/browser never cancels an agent turn, a tool read, or another tab's work; only work owned by the departing observer stops.
11. **…retain presence on the strength of a caller-supplied ID.** Observer authority comes from the authenticated gateway connection only; REST cannot declare a panel open.
12. **…preconnect or prewarm.** Sockets open only for work — never 13 mailboxes warmed at boot, never a speculative dial.
13. **…key any pool/cache/flight identity by password text.** Identity binds endpoint/TLS + pair + non-secret generation only.
14. **…add a parallel reconnect loop beside the watcher backoff.** The existing `WatcherBackoff` ladder is the only reconnect scheduler; the pool adds one in-bounds dead-idle replacement per read, nothing else.
15. **…change folder resolution semantics.** Slug→name resolution, overrides, discovery, unknown-vs-absent stay in W2's files; the pool carries resolved names only.

### 6.3 Machine-verifiable constraints

| ID | Constraint | How a test checks it |
|---|---|---|
| MC-W1-1 | Per-mailbox concurrent sockets+reservations ≤ 2; global ≤ 8, on every path and every interleaving | Fake-server accept counter sampled continuously during scripted concurrency; max observed ≤ ceilings |
| MC-W1-2 | A connecting reservation is counted: with one dial in flight (blocked at greeting), the next demand for that mailbox waits/busy rather than dialing a second socket | Blocked-greeting server; second borrower's outcome + accept counter |
| MC-W1-3 | Pool-acquisition wait ≤ 5 s (±scheduling slack, assert < 7 s); pool exhaustion → the distinct pool-busy typed outcome, never the timeout class | All-reservations-held scenario; outcome class + elapsed |
| MC-W1-4 | Total read deadline 45 s: every read outcome (success/failure) arrives ≤ 45 s + scheduling slack; dial ≤ 30 s subordinate | Stalled-server matrix (greeting/TLS/login/select/fetch); per-stage elapsed |
| MC-W1-5 | Failed dial releases its per-mailbox and global reservation before returning | Post-failure acquire succeeds immediately; counters at baseline |
| MC-W1-6 | Reuse requires re-select: after a borrower used folder A, the next borrower of the same session sees folder B's data when asking for B, with a SELECT for B observed before its fetch | Protocol command trace (test-side capture) |
| MC-W1-7 | No non-PEEK body fetch on any read path (existing invariant, now structural) | Command capture: zero non-peek FETCH on reads |
| MC-W1-8 | Poison causes (timeout, cancel, BYE, protocol failure) each retire: the socket is closed and a subsequent borrow never reuses it | Scripted cause per case; identity of sessions used (server-side session IDs) |
| MC-W1-9 | Dead-idle replacement: at most one per read; total elapsed stays inside the original 45 s; a second dead replacement fails visibly | Idle-session killed server-side; read outcome + replacement count |
| MC-W1-10 | Mutations are never auto-retried and never coalesced | Mutation on dead session → single visible failure; two identical mutations → two server-side effects |
| MC-W1-11 | Idle release after ~2 min of last completed use (assert closed within 2 min + sweep slack; never closed before 1.5 min while healthy-idle) | Fake clock across the threshold; socket state sampled |
| MC-W1-12 | Eviction picks the least-recently-completed idle socket and never an active one | Three idles with distinct last-use; fourth demand; evicted identity asserted |
| MC-W1-13 | All-eight-active → bounded wait → typed busy; no ninth dial under any scripted pressure | 8 held leases + 9th demand; outcome + accept counter |
| MC-W1-14 | Release/eviction/idle-close never issues folder-CLOSE; a `\Deleted`-flagged message survives release | Set `\Deleted` without expunge; release; re-select; message still present server-side |
| MC-W1-15 | Coalescing identity: same full identity → one dial; any component differs (pair, generation, purpose, args, operation) → separate dials, both served correctly | I-01 matrix (§8 DS-3) with per-identity result markers |
| MC-W1-16 | Joiner cancellation isolation: one joiner's cancel neither fails nor cancels the flight others share | Three joiners; cancel one mid-flight; two succeed |
| MC-W1-17 | Superseded read publishes nothing: no cache write, no timestamp advance, no response-embedded state change after a revision advance | Paused-read → mutate → resume ordering (§8 DS-4) |
| MC-W1-18 | Post-mutation refresh never joins a pre-mutation flight | Refresh result reflects post-mutation server state |
| MC-W1-19 | Presence: retention exactly while ≥1 observer; last-observer departure closes idle panel sockets immediately, active at completion; observer removal on close/disconnect/logout/workspace exit | Observer lifecycle matrix (§8 DS-5) with socket counters |
| MC-W1-20 | Until presence is wired: zero retained sockets after any operation | All scenarios with retention disabled; post-operation counter at baseline |
| MC-W1-21 | Watcher: bounded parallel due-cycle progress with one stalled mailbox (others complete within the pass); ≤1 cycle in flight per mailbox | 13-mailbox scripted stall; completion set + per-mailbox in-flight guard |
| MC-W1-22 | Watcher skip: no slot/reservation → skip recorded, last-success unchanged, backoff unchanged, no dial | Saturated capacity; state file before/after |
| MC-W1-23 | Watcher closed-panel: no retained sockets, no panel-cache writes/refreshes, dirty-mark only | Panel closed through ≥3 cycle intervals; cache/state assertions |
| MC-W1-24 | One budget owner: under N concurrent operations per account, account-slot acquisitions per operation = 1 (no wrapper+client double take) | Instrumented slot counter (test seam) at the budget; total in-flight ≤2 |
| MC-W1-25 | `retry=true` bypasses backoff only: during pool/account saturation a retried request still waits/buses; it never bypasses ceilings | Backing-off + saturated scenario; retry outcome |
| MC-W1-26 | Nil session source in production shape → typed visible error naming the missing wiring; no dial attempted | Construct client without source; operation fails with the wiring error; zero dials |

### 6.4 Integration boundaries

| External system / neighbor | Data flow | Contract | On failure |
|---|---|---|---|
| IMAP server (the only network peer W1 touches) | Dial/TLS/login/SELECT/STATUS/FETCH/STORE/APPEND via go-imap/v2 | Existing command semantics; structural `[NONEXISTENT]` for absence; session termination via logout-class close | Timeout/dial/protocol classes as today (`classifyMailError` stays the mapper); a server connection-limit response is recognized structurally (W0's typed class), the refused session retired, and the effective per-mailbox capacity for that server reduced in-process (see OQ-3) |
| Gateway REST handlers (W4) | Typed outcomes (success / pool-busy / account-busy / backoff / transport class) + response revision metadata | §3.1 error values; W0 maps to 503 `reason` | Handlers render visible errors; cached rows stay, labelled (W2/W3) |
| Gateway WebSocket (W4) | Observer bind/unbind events (transport-agnostic registry API) | §4.7 — authenticated-connection binding only | A dead connection's observers are reaped by the gateway's existing liveness teardown; the registry applies the same removal rules |
| Agent tools (W4/W10) | Same session source injection; same typed outcomes | §4.11 — the tool wrapper keeps owning the budget | Tool result text unchanged in shape; transport failures visible per existing contract |
| Watcher state file | Untouched by W1 beyond the skip/pool interplay | `LoadWatcherState`/`EffectiveState` remain the one derivation point | Unreadable state fails open with visible WARN (unchanged, verified) |
| Credentials | Password resolution stays entirely outside W1 (callers pass `Account` as today) | ADR-004 boot contract unchanged | Unresolved password → the construction site's existing skip/404 behavior (verified) |

<!-- W1-SPEC-CONTINUES -->
