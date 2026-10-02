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

<!-- W1-SPEC-CONTINUES -->
