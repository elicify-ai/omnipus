Status: In review

# Implementation Specification: Mail live access — W1 read runtime (pooled connections, shared operation budget, watcher)

- **Status history:** Draft for the plan-spec grill — derived from the approved design record **Mail live access: pooled connections, folder discovery and a bounded cache** (`docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`, grill report `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-report.md` applied; founder answers Q1–Q5 recorded). This spec is the **W1 work package only**: the pooled IMAP read runtime, the shared operation budget's identity/ordering corrections, and the watcher's relationship to both. **Correction round applied 2026-10-02** per `docs/internal/specs/mail-live-access-landing-order.md` (the interface-ownership register) and the W1+W2 grill `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-1.md` — one prescribed round; every ownership statement below names the register's single publisher. **Final grill round applied 2026-10-02** per `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill2-w1.md` (Round 2 — PASS WITH FINDINGS: two Important, four Minor, no Critical; every finding applied in the text below). **The W0 contracts wave has landed on this branch** (commit `5f23ae8a0`; `verify-contracts` green, generated artifacts regenerated; branch unmerged — the five gateway consumer sites are w5-integration's Wave-D adaptation): every wire shape named below is cited to its landed schema, not described as pending.
- **Date:** 2026-10-02
- **Package mapping (register §2 row 24 — ADR letter ↔ spec file):** **w1** = ADR-W1 read runtime (this file) · **w2** = ADR-W2 discovery/cache (`mail-live-access-w2-discovery-and-cache-spec.md`) · **w3** = ADR-W3 panel (`mail-live-access-w3-panel-and-settings-spec.md`) · **w4** = ADR-W7–W10 features (`mail-live-access-w4-attachments-and-rendering-spec.md`) · **w5** = ADR-W4 integration/privacy (`mail-live-access-w5-integration-and-privacy-spec.md`) · **w6** = ADR-W5 proof, qa-lead (`mail-live-access-w6-proof-spec.md`). **W0 is not a spec file**: it is the ADR's contracts work package — a backend-lead role that owns `contracts/` and the generated artifacts in the first production wave (register §2 row 1, §5).
- **Work package:** W1 (read runtime) of the ADR's work-package table. Exclusive W1 production files: `pkg/email/transport.go`, `pkg/email/mail_budget.go`, `pkg/email/watcher.go`, `pkg/email/watcher_set.go`, proposed `pkg/email/pool.go`.
- **Companion specs:** w2 (discovery/cache — owns all `pkg/email/view.go` changes), w3 (panel), w4 (features), w5-integration (gateway/agent wiring), and w6-proof (qa-lead's test package) are separate specifications; the contracts are **not** a sibling spec — they are the W0 wave owned by backend-lead (register §5). Interfaces W1 publishes are frozen in §3 so the packages can be built in parallel without editing each other's files; the register is the first authority on any ownership disagreement (register §3 R-2).
- **Every code claim below was verified by reading the file in this checkout** (`wt-adr-mail` at `fc4c8bf6d`, branch `docs/adr-mail-live-access`) unless labelled otherwise. GitNexus was not available in this worktree (no `.gitnexus/` index) — impact rows are first-hand Read/Grep sweeps labelled **Inferred**, per `omnipus-shared-rules` rule 9. No test was run and no measurement was taken by this spec.

---

## 1. Summary and scope

**Bottom line:** today every mail read dials the server from scratch — TLS handshake, login, SELECT INBOX, command, close — on every click, for the panel, the agent tools and the watcher alike (`pkg/email/transport.go::Client` is documented "connectionless between calls"). This work package puts **one application-owned connection manager** below that client facade, so repeated reads reuse a healthy authenticated session instead of repeating setup; it caps the pool at **two sockets per mailbox and eight open globally** (counting connecting reservations, not just established sockets); it gives every operation an **exclusive lease** on its socket so one request's selected folder can never leak into another's; it bounds every read to **45 seconds total** with a **5-second** maximum wait for pool capacity and the existing **30-second** dial ceiling subordinate; and it re-worked the shared two-per-account operation budget so that identical reads **coalesce only when they truly mean the same thing** (same agent/workspace pair, same configuration generation, same operation, same normalized arguments, same live-versus-cache purpose — grill finding I-01) and a read that was superseded by a newer **publication revision** publishes nothing anywhere (grill finding I-02). The watcher keeps its independence and its cadence but gains bounded fair scheduling under the same caps. **Phase 1 is plain IMAP only; JMAP is out of scope for W1 entirely.**

In scope (all in `pkg/email/` plus its injected seams):

- **The pool manager** (new `pkg/email/pool.go`): socket ceilings, reservation accounting, exclusive mailbox leases, idle expiry and LRU eviction, poisoned-connection retirement, panel-observer presence hooks, and the publication-revision capture seam.
- **The client facade rewiring** (`pkg/email/transport.go`): the client stops dialing per call and borrows sessions from the injected manager instead. A REST-created or tool-registered client **cannot** construct a private pool — the manager is a process-singleton injected at the three production construction sites.
- **The budget's identity and ordering corrections** (`pkg/email/mail_budget.go`): the read-coalescing identity (I-01), the total-read-deadline plumbing, and the revision capture — the two-slot per-account semaphore, backoff gate and singleflight structure stay.
- **The watcher's relationship to the pool** (`pkg/email/watcher.go`, `pkg/email/watcher_set.go`): bounded fair scheduling (replacing the sequential loop), at most one cycle in flight per mailbox, non-blocking acquisition so watcher work never displaces foreground reads, no socket retention and no panel-cache fill while no panel is open, and a skipped cycle leaving the last-checked time unchanged.
- **Wiring seams consumed by w5-integration** (gateway REST, agent tool registration, watcher provider): W1 publishes narrow interfaces; w5-integration injects the shared instances. W1 does not edit `pkg/gateway/` or `pkg/agent/` (w5-integration's files).

Out of scope for this spec (owned elsewhere, interfaces frozen here):

- Folder discovery, the folder-mapping cache, header cache, and **all** changes to `pkg/email/view.go` (W2). W1 publishes the lease/session interface W2's file will consume; W2 never edits `pkg/email/transport.go`.
- Wire contracts (`contracts/`), generated types, the presence WebSocket frames, and the typed 503 `reason` field — **landed on this branch in the W0 contracts wave owned by backend-lead** (commit `5f23ae8a0`; register §2 row 1, §5); w5-integration consumes/maps them. W1 supplies the distinct typed error values only (§3.1).
- The gateway/agent injection call sites themselves, boot/reload wiring, removal cascades (w5-integration's files; register rows 5–7, 16, 19).
- All test files (w6-proof — qa-lead — owns them; §8 names them and what each must prove).
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
| `pkg/gateway/rest_mail_budget.go::mailBudgetWrap` | **consumed (w5-integration)** | The REST gating wrapper: builds `MailBudgetRequest{Account: client.AccountKey(), AgentID, WorkspaceID, Operation, Params, Retry: mailRetryParam(r)}` and calls `email.CallValue`. Passes `AgentID`/`WorkspaceID` that `flightKey` ignores today — after I-01 they participate. `mailBudgetErr` maps `*MailBackoffError` → 503 `code=backoff` and `ErrMailBusy` → 503 `code=busy`. The typed `reason` extension has **landed**: `contracts/components/schemas/MailUnavailableError.yaml::reason` = `pool_busy\|account_busy\|server_connection_limit\|backoff` (register row 7, commit `5f23ae8a0`); W1 supplies the distinct error values; **w5-integration** implements the gateway mapping. |
| `pkg/gateway/rest_mail_read.go::handleMailFolders`, `::handleMailList` | **consumed (w5-integration)** | Verified dial closures: `client.FolderCounts(c)` under op `listMailFolders` params `{scope:folders}`; `client.ReadFolderPage(c, folder, limit, beforeUID)` under op `listMailMessages` params `{folder, limit, before_uid}`. These are the two reads whose flight identity I-01's tests exercise. |
| `pkg/gateway/rest_mail.go::mailPairClient` | **consumed (w5-integration)** | Verifies pair + resolves password from the credential store, then `email.NewClient(...)` **per request**. This is the "REST-created client" the design forbids from building a private pool: w5-integration injects the shared session source here; the client itself never constructs a manager (§4.1). |
| `pkg/gateway/rest_mail_read.go::handleMailSeen` | **hazard replaced by the lease** | Verified: calls `client.ResolveRef(...)` (one session), discards the returned epoch, then `client.MarkSeenIn(...)` opens **another** session — the resolve-then-mutate split the grill's source-integration note flags. Under the lease, ref validation happens **on the same selected lease** that performs the mutation (W2 implements; W1's lease API makes the same-lease check possible). |
| `pkg/gateway/gateway_boot.go` watcher wiring (and the same shape in `pkg/gateway/gateway_reload.go`) | **consumed (w5-integration)** | `email.MailboxProviderFunc(func() []email.Mailbox { return buildMailboxes(...) })` → `email.NewMailboxWatcherSet(provider, homePath, email.SharedMailBudget(homePath))`, wrapped in `heartbeat.NewMailWatchService(..., 0)`. The provider is rebuilt from live config each cycle. w5-integration re-injects the pool here. |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent`, `::SetSharedMailBudget` | **consumed (w5-integration)** | Builds `tools.EmailTransports` (workspace → `*email.Client`) per agent with env-resolved passwords; injects the budget via the optional `SetMailBudget(b, agentID)` setter. w5-integration adds the pool injection the same way (optional setter; unit-test toolsets without it keep compiling). |
| `pkg/tools/email.go::gateMailDial`, per-tool `::SetMailBudget` | **consumed (w5-integration injection; w4-features/W10 adapter)** | The tool-side outer wrapper — the second of the three budget owners. **One budget owner per operation** (§4.11) means this wrapper keeps owning the account gate and the injected client must not acquire a second slot for the same dial. |
| `pkg/email/watcher.go::Watcher` / `::Cycle` / `::probe` | **modifies** | One mailbox's cycle: `cycleIfDue` (skip while `NextAttemptAt` in the future) → `probe` (budget `TryCall`; `MailboxStatuser` one-STATUS path, else unseen-only `ReadInbox`) → `recordSuccess`/`recordFailure`. `recordFailure` accepts the raw error text today (verified) — **W1 owns the fix** (grill-1 C-1; register row 19): after correction it persists only the safe classified representation (`classifyMailError`'s closed class) plus safe metadata, never the raw provider text (§4.12, FR-W1-23). `classifyMailError` maps to the closed class enum; its text-contains `folder`→`folder_missing` rule is broad — absence still requires the structural `[NONEXISTENT]` response (`pkg/email/view.go::isNonexistentFolder`), never this classifier. |
| `pkg/email/watcher.go::WatcherBackoff` | **unchanged** | 60 s base, doubling loop to the 15-min nominal cap, `auth_failed` straight to cap, ±20% jitter (factor 0.8–1.2 — the effective cap window is 12–18 min). Preserve exactly; the pool must not add a parallel reconnect loop. |
| `pkg/email/watcher.go::WatcherInitialOffset` | **kept** | First-cycle stagger: index × 1-minute cycle interval (`watcherCycleInterval`). |
| `pkg/email/watcher_set.go::MailboxWatcherSet.CycleAll` | **modifies** | Verified **sequential** `for` loop over `provider.Mailboxes()` with stagger + `cycleIfDue`; the ADR correctly notes the loop's shape is not proof a stalled account cannot delay others — a stalled mailbox's cycle blocks the rest of the pass today. §4.10 replaces it with bounded fair scheduling. |
| `pkg/email/view.go::FolderCounts` / `::ReadFolderPage` / `::ReadView` / `::MarkSeenIn` / `::DeleteDraftStatus` / `::ResolveRef` | **W2's file — consumed via the lease** | All dial via `c.dialIMAP(ctx)` + `defer client.Close()`; per-folder SELECT via `selectFolder`; fetches are `BODY.PEEK` (no flag writes on reads). `DeleteDraftStatus` sets `\Deleted` and expunges the exact UID where UIDPLUS exists — deferred delete semantics the pool's release path must respect (**a release/close must never issue a folder-CLOSE that would expunge pending `\Deleted` messages**; §4.6/MC-13). W2 owns rewiring these onto W1's lease; W1 never edits `view.go`. |
| `pkg/email/imapserver_test.go::startMemIMAP` | **test harness** | In-memory `go-imap/v2` server on loopback TCP; appends real messages through a raw client; swaps the `imapDial` seam (restored via `t.Cleanup`); returns a wired `*Client`. Real protocol, no new dependency. w6-proof's (qa-lead's) new tests extend this harness with dial/LOGIN/SELECT counters and controlled stalls (§8). |
| `pkg/email/view_missing_folder_test.go` | **regression to preserve** | `TestFolderCounts_MissingSentFolderStillOpensMailbox`, `TestFolderCounts_MissingDraftsFolderStillOpensMailbox`, `TestFolderCounts_CancelledRequestStillFails` — must keep passing unchanged through the pool rewrite. |
| `pkg/email/mail_budget_red_test.go` | **regression to preserve** | `TestMailBudget_BackoffRefusalSymmetric`, `TestMailBudget_SemaphoreCapTwoHoldsUnderRetry`, `TestMailBudget_SingleflightOneDial`, `TestMailBudget_SingleflightKeyIncludesParams`, `TestMailBudget_WatcherTryAcquireSkipsNonBlocking`, `TestMailBudget_WatcherBackoffAndCallersAgree`. The singleflight-key test pins `flightKey`'s inclusion of params — it is updated by w6-proof only to the new identity shape, never weakened. |
| `pkg/email/watcher_budget_red_test.go`, `::watcher_backoff_red_test.go`, `::watcher_cycle_if_due_red_test.go`, `::append_timeout_red_test.go`, `::dial_timeout_classification_test.go` | **regression to preserve** | Budget-skip-is-not-failure, cycle-through-budget, backoff ladder bounds, `cycleIfDue` never dials in backoff, append returns promptly on deadline, dial-error classification. |

### 2.2 Impact assessment (Inferred — no GitNexus index in this worktree; first-hand Read/Grep sweep)

| Symbol modified | Risk | d=1 (will break / must adapt) | d=2 (likely affected) |
|---|---|---|---|
| `transport.go::dialIMAP` (split into establish-session + lease-select) | **HIGH** — every mail read on every path flows through it | All `pkg/email` methods that call `dialIMAP` (W2 rewires `view.go`; `transport.go`'s own `ReadInbox`/`Search`/`ReadMessage`/`MailboxStatus` are W1's); `startMemIMAP` harness (keeps working — same `imapDial` seam) | `pkg/tools/email.go` tools (via `Transport` interface — unchanged signatures), gateway handlers (via `mailPairClient`) |
| `mail_budget.go::MailBudgetRequest` (+ pair/generation/purpose fields) | **MEDIUM** | `mailBudgetWrap` (w5-integration), `gateMailDial` (w4-features/W10), `watcher.go::probe` (W1) — all construct the struct literally; new fields need values or explicit defaults | `pkg/tools/email_no_retry_param_test.go` and the budget RED tests (w6-proof updates to the new identity) |
| `mail_budget.go::flightKey` (new identity) | **MEDIUM** | `TestMailBudget_SingleflightKeyIncludesParams` (w6-proof revises); every coalescing behavior test | Panel N-tab coalescing behavior (same visible outcome, new key) |
| `watcher_set.go::CycleAll` (sequential → bounded fair) | **MEDIUM** | `heartbeat.NewMailWatchService` consumer (call shape unchanged), `specapi/watcher_dial_red_test.go` | Badge freshness timing under many mailboxes |
| `transport.go::Client` (+ injected session source) | **MEDIUM** | `NewClient` call sites: `mailPairClient`, `registerEmailToolsForAgent`, `buildMailboxes` (w5-integration injects); test constructors (nil source = legacy path, §4.1) | None beyond — `Transport` interface signatures unchanged |

No CRITICAL blast radius: the `Transport` interface method set is unchanged (implementations adapt internally); the three production `NewClient` sites are all named and all owned by w5-integration's injection wave; no wire type changes in W1.

### 2.3 Execution flows touched

| Flow | Today | After W1 |
|---|---|---|
| Panel folder counts (`handleMailFolders`) | `mailPairClient` → `mailBudgetWrap` (backoff→coalesce→2-slot) → `FolderCounts` → `dialIMAP` (dial+TLS+login+SELECT INBOX) → 3× STATUS → close | Same wrapper chain; the dial becomes a pool lease (reuse healthy session or bounded establish), exclusive per operation, retained only while a panel observer remains |
| Agent tool read (`read_inbox`) | `gateMailDial` → `ReadInbox` → `dialIMAP` → close | Same wrapper owns the budget; the dial borrows from the same pool — no private pool, no second account slot |
| Watcher cycle (`CycleAll` tick) | Sequential loop; `cycleIfDue` → `TryCall` (non-blocking) → `MailboxStatus` → dial+close | Bounded fair scheduling, ≤1 cycle in flight per mailbox; non-blocking pool reservation; socket released (not retained) after the cycle; skip leaves last-checked unchanged |
| Two pairs, one account, different Sent mapping, same list request | **One shared flight** (account+op+params key) — the first pair's result serves the second (I-01) | Two separate flights under the full coalescing identity; the account's 2-slot gate still bounds total concurrent dials |

### 2.4 Cluster placement

This work stays inside the **email transport/tools** cluster (`pkg/email`), with narrow consumed seams in the gateway/agent integration points w5-integration owns. No new product area, no wire change, no SPA file.

---

## 3. Published and consumed interfaces (parallel-work freeze)

W2 (discovery/cache), w5-integration (integration/privacy) and w6-proof (qa-lead's test package) build against the shapes below **without editing each other's files**. Ownership follows the landing-order register (`mail-live-access-landing-order.md` §2): where a register row names W1 as the single publisher, the freeze below is that publisher; where it names another package, W1 only states its consumption contract. Interface names are indicative Go names; the *shape and semantics* are what is frozen — a rename during implementation is fine, a semantic change goes back through the architect.

### 3.1 W1 publishes

| Interface (indicative shape) | Consumed by | Semantics frozen here |
|---|---|---|
| `MailSessions` — the application-owned connection manager, one process-wide instance per data dir (the pool analogue of `email.SharedMailBudget(stateDir)`) — **single publisher: W1 (register row 9)** | W2 (`view.go` rewiring — against this freeze only), w5-integration (injection at `mailPairClient`, `registerEmailToolsForAgent`, watcher provider) | `Acquire(ctx, LeaseRequest) (Lease, error)` and `Lease.Release()`. Never constructed by a `Client`. `LeaseRequest` carries the operation's pool identity (§4.2), the requested folder (or none, for STATUS-style probes), and whether the caller may retain (`eligible` — panel-observer work) or never retains (tool/watcher work). |
| `Lease` — exclusive borrower handle — **single publisher: W1 (register row 9)** | W2 | Guarantees: sole ownership of the socket until `Release`; the requested folder SELECTed (or re-SELECTed) and validated before the first borrower command; SELECT data (`UIDValidity`, `NumMessages`) returned to the borrower for the epoch check; PEEK discipline on reads; release never expunges. A `Lease.SelectError` class distinguishes structural folder-absence from transport failure (W2's M-01 work depends on it). **Same-lease reference validation capability (register row 16, grill correction I-03):** the lease exposes the selected folder's live epoch (`UIDValidity`) and the pair's generation to the borrower *before* its first command, so a gateway-issued `message_ref` can be validated **on the same lease** that performs the fetch or mutation. W1 provides the capability only — the validation logic is W2's (`view.go`), the reference's issuance is w5-integration's (gateway handlers); W1 never sees or parses a `message_ref` (§4.3 item 4, FR-W1-24). |
| Session-source injection on the facade — `Client` gains an optional session source (option/setter; nil = legacy per-call dial, tests only) | w5-integration | The three production construction sites pass the shared manager. A production dial with a nil source in a process where the manager exists is a **typed, visible error** naming the missing wiring — never a silent uncounted dial (§4.1, MC-W1-26; the manager's existence begins with w5-integration's Wave-D injection, register rows 9/12). |
| Extended `MailBudgetRequest` — `PairKey` (or reuse of existing `AgentID`+`WorkspaceID`), `Generation string`, `Purpose` (`read_live` \| `read_cache` \| `mutation` \| `watcher`), `TotalReadDeadline` | w5-integration (`mailBudgetWrap`), w4-features/W10 (`gateMailDial`), W1's own watcher | The coalescing identity (§4.8) and the 45 s total-read-deadline plumbing (§4.4). `Retry` keeps its exact semantics: bypasses the backoff check only. |
| Revision capture seam — `RevisionSource` (read current revision for a pair/folder; compare captured-vs-current) — **single publisher: W1 (register row 10, resolving grill-1 I-3's double freeze)** | W2 (cache layers; its `HeaderCache.Put`/`FolderSnapshotStore.Save` signatures take the captured value per W2's correction), w5-integration (owns the counter and the advancing events) | W1 defines the accessor the read path calls to capture the revision **before server work** and to test freshness **before publishing**; w5-integration owns the counter and the events that advance it (successful mutations, invalidations). W1 does not store the revision (§4.9). W1's capture hands the captured value to the flight; the transport of that value to W2's comparison points rides the same seam — no second capture point exists. |
| Presence registry — `PanelPresence`: `Bind(connID, observerID, workspaceID)`, `Unbind(connID, observerID)`, `UnbindAll(connID)`, `Count(workspaceID)` — **single publisher: W1, transport-agnostic, in `pool.go` (register row 11, resolving grill-3 I-3's double publication)** | w5-integration (consumes it; binds gateway socket teardown and the presence-frame handlers to it) | Observers bind to the **authenticated gateway connection's opaque ID issued by the gateway** — never a caller-supplied identity from REST bodies or query params. A REST read may *associate* itself with an observer already bound on an authenticated connection via the validated `observer_id` query param (§3.2); it can never *create* retention authority. Until w5-integration registers the presence frame handlers, retention is structurally disabled (`RetentionEnabled() == false`) and every socket closes after its operation (§4.7). |
| Watcher dirty-mark signal — "folder version changed while closed": `MarkPanelMetadataDirty(pair, folder)` (or a per-pair checkable dirty flag; exact shape fixed at implementation against this row) — **added to the freeze per register row 13** (the obligation existed in MC-W1-23/T21 but not here) | W2 (panel-cache invalidation), w5-integration (gateway-side consumption on the next eligible panel event) | Transport-agnostic, carries no mail data (no folder contents, no counts, no subjects). Set when the watcher observes a folder-version change while no eligible panel event consumes it; consumed-once semantics at the next panel-open/folder-switch event; never a cache write and never a refresh trigger by itself (§4.10). |
| Pool instrument sub-fields (register row 17, split ownership) | w6-proof (freezes the whole record shape in its §6.1 and consumes the joined records), w5-integration (owns the request-scoped envelope around W1's sub-fields) | For every pooled operation W1 populates **its** sub-fields of the instrument record — `acquire_wait_ms`, `socket_count`, and the pool outcome **as w6-proof's frozen shape carries it**: bounded-wait pool exhaustion is recorded as `outcome=pool_busy` (a safe class in w6 §6.1's `outcome` domain, mirroring the landed `MailUnavailableError.reason=pool_busy` wire value), success as `outcome=ok`. The frozen shape has **no per-record established-versus-reused discriminator, and W1 adds none** (R-1/R-4: one shape, one publisher — w6-proof; w6's `hit` field is the *cache*-hit flag, not a pool-reuse flag, so it cannot carry this). Reuse-versus-establish evidence lives where it is already observable: W1's own server-side accept-counter suite (T1, T2, B-W1-1) and MC-P4's record-sum-vs-accept-delta comparison — not in a per-record field; if the measurement campaign later needs the discriminator, that is a w6-proof shape amendment through the register (its R-3 path), never a W1-local field (R2-I-2 resolution). **No second instrument definition exists**: the shape has exactly one publisher (w6-proof). W1's coalescing layer applies the mechanical joiner rule: a coalesced joiner records `socket_count=0` plus the shared-flight marker, so the sum stays comparable to the server's connection counter. The emitter obligation is binding, not optional: it must exist before the proof wave starts (register §4, Wave E gate). |
| Typed pool errors — pool-busy and pool-identity error values | W0 contracts wave — **landed**: the 503 `MailUnavailableError` `reason` enum exists with exactly `pool_busy\|account_busy\|server_connection_limit\|backoff` (`contracts/components/schemas/MailUnavailableError.yaml::reason`, register row 7, commit `5f23ae8a0`); w5-integration (gateway mapping) | Distinct from `ErrMailBusy` (account-slot busy): a pool-capacity exhaustion that survived its bounded wait (§4.6). The landed `reason` enum carries both; w5-integration implements the mapping; W1 supplies the distinct error values and nothing crossing the wire. |

### 3.2 W1 consumes

| Interface | Provided by (register's single publisher) | Use |
|---|---|---|
| Folder name resolution and per-folder SELECT semantics | W2 (`view.go` — `folderNameFor`, `selectFolder` remain the folder authority) | The lease takes a resolved folder name (or none); W1 never interprets slugs, overrides, or discovery. W1 only guarantees exclusive selected-state per lease. |
| Generation values and pair ID | **w5-integration implements both (register row 12, conflict resolved)** — `GenerationSource`, durable and restart-stable | Construction is decided, one for all three consumers: the generation is a non-secret fingerprint of the canonical pair identity **plus the persisted config epoch** (this spec's former OQ-1 option C — catches endpoint/credential/override changes and invisible re-saves); the pair ID is a randomly minted 128-bit value stored beside the pair config. W1 treats both as opaque strings participating in the coalescing/pool identity; W1 never derives or stores them. The credential-material fingerprint (w5's former MC-17) was rejected by the register. |
| Revision counter and advancing events | w5-integration | W1 only captures and compares via its own `RevisionSource` seam (§4.9). |
| Presence frames on the gateway WebSocket (`type=mail_panel_observer`, `action=open\|close`) | **Landed** in the W0 contracts wave (`contracts/asyncapi.yaml`: `MailPanelObserverFrame`, `MailPanelObserverAckFrame`, `MailPanelObserverErrorFrame` + their `WsFrameType` entries; register row 5, commit `5f23ae8a0`); **w5-integration implements the gateway handlers** | W1's registry is transport-agnostic; the WS integration is w5-integration's. |
| REST `observer_id` query param on panel folder/list reads | **Landed** in the W0 contracts wave (`contracts/openapi.yaml` carries `observer_id` on the folders and messages reads); w5-integration requested it and validates it at the gateway (register row 6, commit `5f23ae8a0`) | Consumption contract: a panel read may carry the gateway-issued `observer_id` of an observer already bound on an authenticated connection, so its result rides that observer's retention class instead of staying request-scoped — this is what makes the founder's warm-pool behaviour reachable. Validation is the gateway's, never W1's: a foreign, stale or self-asserted observer ID confers no retention and no authority (§4.7, CX-11). W1's registry only ever consults observers bound on authenticated connections. |
| Existing seams kept: `imapDial` (tests), `dialResolver`, `LoadWatcherState`/`EffectiveState`, `fileutil.WriteFileAtomic` | in-tree | No new dial stack, no second backoff derivation, no second state file. |

**Negative consumption (stated so no package waits on W1 for these):** W1 consumes **none** of — the paging/search/stale-cursor shapes (`next_cursor`, `has_more`, `view_limit_reached`, the `search` param, the typed 409): the W0 shapes **have landed** (`contracts/components/schemas/MailMessagePage.yaml`, `MailStaleReferenceError.yaml`, commit `5f23ae8a0`) and **W2 implements the server-side search + cursor issuance in `view.go`** (register row 4); the targeted single-part reader and the MIME classifier behind the landed `has_attachments` field: **W2's**, in `attachment_parts.go`/`view.go` (register rows 14–15) — W1 publishes only the lease PEEK primitives that reader rides; `message_ref` **issuance**: w5-integration mints it in the gateway (register row 16) — W1 provides the same-lease validation capability, not the reference. W1 creates no stub, parallel type or second implementation for any of these (register §5.3, Hard Constraint #8).

### 3.3 Files new and changed

| File | Status | Owner wave | Contents |
|---|---|---|---|
| `pkg/email/pool.go` | **new** | W1 | The manager: identities, ceilings, reservations, leases, idle/LRU, retirement, presence registry (the single one — register row 11), revision-capture seam, watcher dirty-mark signal, pool instrument sub-fields, typed errors. |
| `pkg/email/transport.go` | **changed** | W1 | `dialIMAP` split into session-establishment (dial+login, no INBOX SELECT) and lease-time folder selection; `Client` gains the optional injected session source; `AccountKey` untouched. |
| `pkg/email/mail_budget.go` | **changed** | W1 | I-01 identity fields + new `flightKey`; total-read-deadline plumbing; revision capture hook; coalesced-joiner instrument marker. Semaphore, backoff gate, `TryCall` skip semantics, `Retry` bypass unchanged. |
| `pkg/email/watcher.go` | **changed** | W1 | Probe path gains non-blocking pool acquisition (a pool-busy reservation attempt is a skip, like a slot skip); no retention; no cache interaction beyond the dirty-mark signal; **`recordFailure` redaction** (§4.12, register row 19). Backoff/offset/state machine untouched. |
| `pkg/email/watcher_set.go` | **changed** | W1 | Bounded fair scheduling replacing the sequential loop (§4.10). |
| `pkg/email/view.go` | **changed — NOT by W1** | W2 | All lease call-site changes (`FolderCounts`, `ReadFolderPage`, `ReadView`, `MarkSeenIn`, `DeleteDraftStatus`, `ResolveRef` onto `MailSessions`), server-side search + cursor issuance (register row 4), same-lease `message_ref` validation (row 16). W1+W2 freeze the §3.1 lease shape first. |
| `pkg/gateway/rest_mail*.go`, `rest_mailbox.go`, boot/reload wiring, `pkg/agent/email_tools.go` | **changed — NOT by W1** | w5-integration (ADR-W4 — the single gateway/agent Mail-route writer) | Inject the shared manager + generation/revision sources; presence frame handlers + teardown binding; `observer_id` param; typed-error mapping; `message_ref` issuance; removal cascades; `mailErr502` redaction (register rows 5–7, 16, 19). |
| `contracts/*` + generated artifacts | **changed — NOT by W1** | W0 contracts wave (backend-lead; register row 1) — **landed on this branch** (commit `5f23ae8a0`; `verify-contracts` green) | 503 `reason` values, presence frames, `observer_id` param, `mode`/`MailReadMetadata` fields. Nothing in W1 crosses the wire by itself. |
| test files (`pkg/email/pool_test.go`, `pool_poison_test.go`, `mail_budget_identity_test.go`, `watcher_fair_test.go`, …) | **new** | w6-proof (qa-lead; ADR-W5) | §8 names them. Production owners never edit test files. |

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
| `pkg/gateway/rest_mail.go::mailPairClient` (per-request client) | w5-integration | shared `MailSessions` |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent` (per-agent toolset clients) | w5-integration | shared `MailSessions` |
| `pkg/gateway/gateway_boot.go` watcher provider (`buildMailboxes`) and the reload twin | w5-integration | shared `MailSessions` |

**Rules.**

1. A `Client` never constructs a manager. `NewClient` gains an optional injection; the client delegates session establishment to it.
2. **A production dial with no injected source is a typed, visible error naming the missing wiring — in a process where the manager exists.** It must not silently fall back to an uncounted per-call dial once the manager exists, because a missed injection would otherwise become exactly the private dial path the design forbids. **Which rule stands (Round-2 finding R2-I-1, resolved per register R-2):** the qualified rule governs. The manager's existence in a production process **begins with w5-integration's Wave-D injection** at the three construction sites (register rows 9/12; register §4 Wave D) — so during the Wave C→D window the legacy per-call dial remains the production behavior *by design* (the blast-radius note below states this window; it is not an exception to FR-W1-2), and from the moment the injection lands, a nil-source dial is the typed wiring error — never a fallback. The nil-source path exists only for tests (same status as the `imapDial` reassignment seam: "production code never reassigns it").
3. The budget counts **work** (account slots); the manager counts **sockets** (reservations + established). Neither replaces or subsumes the other.
4. There is no per-path pool: panel, watcher and tools share one instance, so the two-per-mailbox and eight-global ceilings are honest process-wide counts.

**Blast-radius note (Inferred):** the three injection sites are all in w5-integration's ownership columns per the ADR's work-package table (ADR-W4; register row 9's mapping); W1 ships the manager and the injection seam, w5-integration wires it. Until that integration wave lands, W1's code compiles and the legacy per-call dial remains the behavior in production — the switch is w5-integration's single integration wave, not a flag flip scattered across packages. **Rule of precedence (R2-I-1):** FR-W1-2's qualified rule — typed error in a process where the manager exists — stands over this transition note; the note describes the bounded Wave C→D window only. The qualification is deliberate, not a hedge: a categorical error would hard-fail every production mail read (panel, tool, watcher) for the whole window, and an unqualified fallback would let a post-injection missed wiring dial silently and uncounted forever — the private-dial path this design exists to forbid. Qualifying by manager existence gives both properties: legacy behavior only while the manager is genuinely absent, a visible wiring error the moment it is not.

### 4.2 Socket ceilings and reservation accounting

| Ceiling | Value | Counts |
|---|---|---|
| Per mailbox identity | **2** | Reservations (dial/login in progress) **plus** established sockets, idle or active |
| Global | **8** | Same — all mailboxes, all paths (panel, watcher, tools) |
| Per account (existing, unchanged) | **2 work slots** | Concurrent operations via `mailBudgetSlotsPerAccount` — the A8 gate, not sockets |

**Mailbox identity** (the pool key) binds, per the ADR's identity row: the agent/workspace **pair**, the configured endpoint (host:port) + TLS identity, and the **non-secret configuration/credential generation** (construction decided, one for all consumers, register row 12: fingerprint of the canonical pair identity plus the persisted config epoch, implemented by w5-integration — §3.2). It never contains the password text. Two pairs that share `host:port|username` therefore hold **separate** pool identities and **never share a socket** — cross-pair socket, header or presence sharing is prohibited (ADR risk table, "Cross-pair reuse"); the account key keeps only its contention role. A generation change (reconfiguration, credential/endpoint change, removal) orphans the old identity's sockets: they are closed, and late completions cannot re-enter the new identity's pool.

**Reservation discipline.**

- The reservation is taken **atomically** against both ceilings before any dial begins (verified requirement, ADR P1.2 "Reserve global capacity atomically before dialing"). A test can only ever observe: reserved-but-connecting, established, or released — never an uncounted dial.
- A **failed dial releases every reservation it holds** (per-mailbox and global) before the error propagates. No leak on the error path.
- Connecting reservations count toward both ceilings: while a dial is in flight, that mailbox is at 1-of-2 and the process is 1-of-8, and subsequent acquisitions account for it.

### 4.3 Exclusive mailbox leases

**Decision.** One borrower owns a socket until its entire operation finishes. SELECT/EXAMINE state belongs to the lease, never to a simultaneous request.

1. **Re-select and validate before use.** On every lease acquisition — reused or freshly established — the manager (re-)SELECTs the requested folder and returns the SELECT data (`UIDValidity`, message count) to the borrower for its epoch check. A reused socket carries the *previous* borrower's selected state; skipping the re-select would fetch one folder's messages under another folder's name. `dialIMAP`'s current unconditional SELECT INBOX (verified) disappears from session establishment: establishment is dial+login only; folder selection is lease work. STATUS-style probes (`watcher.go::probe` via `MailboxStatus`) take a no-folder lease — STATUS does not require a selected folder — which also removes today's wasteful SELECT INBOX inside the watcher's one-command probe.
2. **PEEK preserves flags.** Every read on a lease fetches `BODY.PEEK` — already the invariant of `pkg/email/view.go`'s fetches ("the fetches behind them are all BODY.PEEK, so no flag is ever written", verified comment on `selectFolder`). The lease makes it structural: read commands on a lease must not write flags; only explicit mutation operations (`MarkSeenIn`, `DeleteDraftStatus`, draft APPEND, send-APPEND) write, and each goes through the budget as a mutation purpose (§4.8) with **no coalescing and no silent replay** (§4.5).
3. **Structural folder absence is a select outcome, not a transport failure.** A SELECT answered with the server's structural not-found (`[NONEXISTENT]`, the class `pkg/email/view.go::isNonexistentFolder` detects) surfaces as a typed select-error on the lease; the socket stays healthy and returns to the idle pool. Network/timeout/auth failures during SELECT poison the socket (§4.5). W2's unknown-vs-absent classification (M-01) consumes this distinction.
4. **Same-lease validation seam.** The lease exposes the selected folder's live epoch to the borrower *before* commands run, so W2 can compare a reference's embedded `uidvalidity` and generation on the same lease that performs the fetch or mutation — replacing the verified resolve-on-one-session/mutate-on-another split in `pkg/gateway/rest_mail_read.go::handleMailSeen`. W1 provides the capability (frozen in §3.1's `Lease` row, FR-W1-24); W2's spec owns rewiring the callers and the validation logic; the gateway-issued `message_ref` itself is minted by w5-integration (register row 16) — W1 never parses or stores a reference.

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

**Failure mapping.** 5 s pool-wait exhaustion → typed pool-busy (distinct value; the landed 503 `reason=pool_busy` — `contracts/components/schemas/MailUnavailableError.yaml::reason`, register row 7 — which w5-integration maps). Total-deadline exhaustion while queued on the account slot → the existing `ErrMailBusy` shape (`reason=account_busy`). Deadline exhausted after dial → the existing timeout class. A server connection-limit refusal maps to the structurally-recognized `server_connection_limit` class — the wire value is landed in the same enum; only the capacity-reduction depth remains open (OQ-3). Cache timestamps never advance on any failure (verified requirement, ADR failure table).

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

**Decision.** Panel-requested sockets may be retained only while at least one authenticated Mail panel observer for that workspace is open. Presence is a W1-published registry (§3.1 — the register's single publisher, row 11); the WebSocket integration is w5-integration's.

1. **Bound to the authenticated connection, never a caller-supplied identity.** The registry keys observers by the **gateway-issued opaque connection ID** of the authenticated WebSocket + an opaque per-panel observer ID (one per tab). Observer *association* happens only through the authenticated socket's own frames, validated against that connection's session and workspace authorization. A REST folder/list read may **carry** the gateway-issued `observer_id` query param (landed in the W0 contracts wave, `contracts/openapi.yaml`, register row 6, commit `5f23ae8a0` — without it every panel read stays request-scoped and the founder's warm-pool behaviour never activates) — but only to *associate* the read with an observer already bound on an authenticated connection; the gateway validates it, never the caller's claim. A foreign, stale or self-asserted observer ID confers no retention and no authority — presenting someone else's observer ID gains nothing (CX-11). This is the verified ADR rule ("Bind observers to the authenticated connection, not a caller-supplied session identity"; "REST cannot claim ownership using another socket/user's ID").
2. **Per-tab observers.** Two tabs = two observers. Closing one tab unbinds only its observer and cancels only its subscriptions; a shared read finishes for the remaining owners. The shared operation is cancelled only when **no** request/observer still depends on it.
3. **What removes an observer:** explicit close frame; socket disconnect (the gateway's existing liveness teardown reaps lost connections — no new mail-data polling timer); logout; workspace exit. Any of these unbinds that connection's observers; when the last observer for a workspace goes, that workspace's **idle panel sockets close immediately** and active ones close when their operation completes (independent watcher/tool work is not interrupted).
4. **Conservative default until acknowledged.** Until w5-integration registers the presence frame handlers, `RetentionEnabled()` is false and **every** socket closes after its operation — request-scoped connections are the default, exactly as the ADR prescribes ("Until presence is acknowledged, use conservative request-scoped connections"). Retention cannot exist before the contract does; there is no "assume the panel is open" mode.
5. **No cache coupling in the registry.** Presence decides socket retention only. It does not refresh caches, does not extend the 30-minute header retention clock, and does not trigger discovery (W2/W3 own those, event-driven).

### 4.8 Read-coalescing identity (grill correction I-01)

**Decision.** The retained A8 singleflight layer shares results **only** between requests with the same **full coalescing identity**:

| Component | Source | Role |
|---|---|---|
| Pair | `AgentID` + `WorkspaceID` (already on the request; **ignored by today's key** — verified) | Result sharing never crosses pairs |
| Configuration/folder-mapping generation | w5-integration's `GenerationSource` (opaque, restart-stable, changes on endpoint/credential/override change — construction decided, register row 12) | An old generation never joins or receives a new generation's flight |
| Operation | existing `Operation` field | as today |
| Normalized arguments | existing `Params` (JSON-marshaled; empty map = coalescing opt-out, preserved) | as today |
| Live-versus-cache purpose | new `Purpose` (`read_live` / `read_cache` / `mutation` / `watcher`) | a `mode=cache_first` read and a `mode=live` refresh for the same folder are **different flights** — a live refresh can never be answered by joining a cache-shaped flight and reporting it as fresh (verified ADR "visible cached refresh shape" rule) |

The new `flightKey` is the concatenation of all five. The **account key (`host:port|username`) keeps only its contention role**: it keys the two-slot semaphore (`gateFor(req.Account)`) and nothing else — it never decides result sharing. Mutations (`Purpose=mutation`) never coalesce at all, regardless of identity equality. Watcher cycles keep `TryCall`'s never-coalesce rule.

**Cancellation and publication use the same identity.** A flight's completion can neither cancel nor publish for a different identity's request: joiners are matched by the full key (singleflight semantics on the new key give this), and the flight's captured revision (§4.9) travels with the same identity. Today's verified joiner rules are preserved: a joiner whose own context ends stops waiting without failing the flight; the flight runs on the detached `flightContext` so one tab's cancel cannot fail the others.

### 4.9 Folder publication revision (grill correction I-02)

**Decision.** Every mailbox/folder carries a local, monotonically advancing **publication revision**, owned by the gateway/cache service (w5-integration advances it; W1 captures and compares it; W2's caches obey it).

1. **Capture before server work.** A read captures the revision current at operation start — in the budget layer, before any dial — and carries it with the flight.
2. **Advance on the listed events only.** Successful mutations (own mark-read/send/draft changes) and every invalidation event the ADR enumerates (P1.3/P2.3 tables) advance the revision. No polling timer, no time-based advance.
3. **A superseded read publishes nothing.** If the captured revision is no longer current when the read finishes, the result is discarded: no rows into any memory cache, no disk snapshot write, no last-validated/fetched timestamp advance, no frontend state or count update. The data is simply dropped; the already-scheduled post-mutation refresh supplies truth. This applies equally to memory cache, encrypted disk snapshots, and delayed frontend responses (the response's opaque `publication_revision` field — **landed** as nullable `publication_revision` on `contracts/components/schemas/MailReadMetadata.yaml`, register rows 2/8, commit `5f23ae8a0` — lets the SPA drop an out-of-order response).
4. **A post-mutation refresh never joins a superseded flight.** Because the generation participates in the coalescing identity and the revision is captured at flight start, a refresh issued after a revision-advancing event cannot share a flight that started under the older revision — I-01 and I-02 compose structurally, not by convention.
5. **W1's obligation is the seam, not the store.** W1 defines `RevisionSource` (capture/compare, §3.1 — the register's single publisher for the seam, row 10) and wires capture into the read path. w5-integration implements the counter and the advancing events. W2's caches and W3's UI consume the response metadata. W1 never stores or advances the revision itself.

### 4.10 The watcher under the same caps

**Decision.** The watcher keeps its independence, cadence and budget (verified: `watcherCycleInterval` 1 min, `WatcherBackoff` 60 s→15-min nominal cap ±20% jitter, `auth_failed` straight to cap; closed-panel new-mail notice preserved; no mail-triggered turn or task) and gains three changes:

1. **Bounded fair scheduling replaces the sequential loop.** `CycleAll` runs due mailboxes with bounded parallelism — at most **one cycle in flight per mailbox** (a mailbox whose previous cycle is still running is not started again), and a global bound consistent with the account and pool ceilings (watcher cycles are non-blocking throughout, so the effective concurrency is capped by availability, not by a large worker pool). One stalled mailbox no longer delays the others' due checks — the verified gap in today's `for` loop. The first-cycle stagger (`WatcherInitialOffset`) and the `cycleIfDue` backoff gate are preserved exactly.
2. **Foreground ahead of watcher, without starving due cycles.** Watcher work is optional and takes **only** what is free: it keeps `TryCall`'s non-blocking account acquisition, and its pool reservation attempt is likewise non-blocking — a watcher cycle that cannot get a slot or a reservation **skips** (recorded as a skip, never a failure: last-success time unchanged, backoff not advanced — verified `ErrMailSkipped` semantics). Foreground requests, which may wait up to 5 s, therefore always outrank watcher work under contention. Due watcher cycles are not starved *by design*: any cycle that finds capacity runs; sustained saturation shows up as honest skipped-and-dated state, which is the accepted posture (ADR failure table "Watcher has no free slot").
3. **No socket retention, no cache fill.** A watcher cycle reuses a healthy **eligible** idle socket while a panel is open (sharing is fine; it is bounded by the same lease rules), but never causes retention: with no panel observer the cycle's socket is closed after the cycle, and 13 mailboxes can never hold 13 watcher sockets. Watcher activity never populates or refreshes panel folder/header/count caches and never prolongs their retention clock; a folder/version change observed by the watcher only marks panel metadata dirty through the dirty-mark signal W1 publishes (§3.1, register row 13) for the next eligible panel event (W2 and w5-integration consume that mark). A skipped cycle leaves the prior last-checked time unchanged — the badge cannot claim a fresh check that did not happen.
4. **The watcher's persisted state carries no raw error text.** `recordFailure` is W1's file and W1's obligation — §4.12 below. The `email-watch/` directory itself is excluded from data-directory version-control staging and from the application's own backups/archives, and is purged on mailbox removal — product-owned guarantees (founder rulings Q-A/Q-B, 2026-10-02; the gate decision is register row 18: w5-integration publishes it, W2 enforces the first-write refusal). Redaction is defence in depth: the state file's content stays safe-class even where the gate holds.

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

### 4.12 Watcher error text is classified before it persists (grill-1 C-1; register row 19)

**Decision.** `pkg/email/watcher.go::recordFailure` — W1's exclusive file — persists only the **safe classified error representation**: the closed class from `classifyMailError`, plus safe metadata (next-attempt time, consecutive-failure count). The raw provider error string is never written to `email-watch/<pair>.json` or to any log line W1 emits. Raw provider text can embed folder names, server hostnames and account prefixes; persisting it turns a transient failure into a durable disclosure.

1. **Ownership, settled by the register (row 19).** The watcher half is **W1's** — this file is W1-exclusive and this spec's two earlier refusal lines ("sanitizing it is W4's logging policy, not W1's") are corrected by this section. The gateway's `mailErr502` raw-error log is the other half and belongs to **w5-integration** (its file; register row 19). No second redaction helper is created in W2's or w5-integration's files, and W1 does not touch `mailErr502`.
2. **Same classes, both directions.** The safe classes `recordFailure` persists are the same closed classes W1 supplies for the typed 503 `reason` mapping (§4.4, §3.1) — one classification, two consumers, no second enum.
3. **Defence in depth, not the only layer.** The product separately guarantees the `email-watch/` directory is excluded from version-control staging and from application backups/archives on every install, and purged on mailbox removal (founder rulings Q-A/Q-B, 2026-10-02; enforced by register row 18's gate — w5-integration publishes the decision, W2 enforces at first write). Redaction bounds what the file can contain even where that gate holds.
4. **Testable, W1-observably.** The persisted state after a scripted raw failure contains only class-plus-metadata (MC-W1-27, B-W1-35, T29, CX-13). The end-to-end marker-scan across the whole state directory is w6-proof's test (register R-4: W1 asserts its seam, the proof package asserts the disk).

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
5. **Given** a client constructed without its injected session source in a process where the manager exists, **When** a read is attempted through it, **Then** it fails immediately with the typed missing-wiring error and zero server connections are attempted — an uncounted dial path is unreachable. (Anchors B-W1-34; added in the correction round, grill-1 M-6.)

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
6. **Given** a mailbox in watcher backoff and all pool capacity held, **When** the human Retry fires, **Then** the backoff gate is bypassed but the request still waits and ends in the typed busy outcome — the semaphore and the pool ceilings are never bypassed. (Anchors B-W1-33; added in the correction round, grill-1 M-6.)

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
4. **Given** a read paused after collecting its server data, **When** a successful mutation completes and advances the publication revision before the paused read resumes, **Then** the paused read publishes nothing at W1's seam — its publish step is suppressed and the response carries the superseded determination (the memory/disk/timestamp observables are asserted end-to-end by W2's V-2 and w6-proof's T15, register R-4) — and the post-mutation refresh's data stands. (R2-M-3 wording aligned with MC-W1-17/B-W1-22/T16.)
5. **Given** a refresh requested after a mutation, **When** it starts, **Then** it does not join any flight started before the mutation (its identity differs by revision and purpose).

### US-7 — Sockets stay only while a panel actually watches (P1)

Panel-requested connections may be kept warm only while at least one authenticated Mail panel for that workspace is open. Presence is bound to the authenticated gateway connection — an ID the gateway issues per socket — with one observer per tab; closing a tab, closing the browser, logging out, leaving the workspace, or dying connection each remove that connection's observers, and the last observer's departure closes that workspace's idle panel sockets immediately (active ones when they finish). Until the presence channel exists, nothing is retained.

**Why this priority:** the founder's "no panel-owned connections while the panel is closed" rule; P1 because it lands with w5-integration's integration wave.

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
6. **Given** a probe failure whose raw error text carries server or folder detail, **When** the failure is recorded, **Then** the persisted watcher state holds only the safe classified error representation — the raw text appears nowhere in the state file. (Added in the correction round: grill-1 C-1, register row 19; §4.12.)

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
11. **…retain presence on the strength of a caller-supplied ID.** Observer authority comes from the authenticated gateway connection only; REST cannot declare a panel open. A REST read's `observer_id` query param (§4.7) only associates the read with an observer the gateway has already bound and validates — it never creates retention.
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
| MC-W1-17 | Superseded read publishes nothing **at W1's seam**: after a revision advance, the read's publish step is suppressed — the publish/commit path W1 invokes on the fresh path is not invoked, and the response carries the superseded determination. (The cache-row/disk/timestamp observables are W2-owned; their end-to-end oracle is W2's V-2 and w6-proof's T15 — register R-4, grill-1 I-4. W1's suite asserts the seam, not another package's files.) | Paused-read → mutate → resume ordering (§8 DS-4), observed at W1's publish seam |
| MC-W1-18 | Post-mutation refresh never joins a pre-mutation flight | Refresh result reflects post-mutation server state |
| MC-W1-19 | Presence: retention exactly while ≥1 observer; last-observer departure closes idle panel sockets immediately, active at completion; observer removal on close/disconnect/logout/workspace exit | Observer lifecycle matrix (§8 DS-5) with socket counters |
| MC-W1-20 | Until presence is wired: zero retained sockets after any operation | All scenarios with retention disabled; post-operation counter at baseline |
| MC-W1-21 | Watcher: bounded parallel due-cycle progress with one stalled mailbox (others complete within the pass); ≤1 cycle in flight per mailbox | 13-mailbox scripted stall; completion set + per-mailbox in-flight guard |
| MC-W1-22 | Watcher skip: no slot/reservation → skip recorded, last-success unchanged, backoff unchanged, no dial | Saturated capacity; state file before/after |
| MC-W1-23 | Watcher closed-panel, **W1-observably**: zero retained sockets (pool counter), and the watcher's only panel-metadata effect is the dirty-mark signal of §3.1 — no cache call exists on the watcher's path. (Server-side cache-file assertions are W2's and w6-proof's — register R-4.) | Panel closed through ≥3 cycle intervals; pool counter + dirty-mark observations at W1's seam |
| MC-W1-24 | One budget owner: under N concurrent operations per account, account-slot acquisitions per operation = 1 (no wrapper+client double take). The instrument is a **test-owned** acquisition seam of the existing `imapDial` pattern — a package var the tests replace and production never assigns (production calls it on every dial; the invariant is never-*assign*, exactly as §8 T23 and DoD 5 state it — R2-M-2 wording correction) (resolves grill-1 M-9 within DoD 5's no-production-test-hook rule; the structural half is that `Acquire`'s signature carries no budget parameter, so a double acquire is unwritable) | Test-owned acquisition seam at the budget; total in-flight ≤2 |
| MC-W1-25 | `retry=true` bypasses backoff only: during pool/account saturation a retried request still waits/buses; it never bypasses ceilings | Backing-off + saturated scenario; retry outcome |
| MC-W1-26 | Nil session source **in a process where the manager exists** (the post-injection production shape) → typed visible error naming the missing wiring; no dial attempted. Pre-injection window (manager absent): the legacy per-call dial remains, by design (§4.1, R2-I-1) | Construct client without source with the manager present; operation fails with the wiring error; zero dials |
| MC-W1-27 | `recordFailure` persists only the safe classified representation: after a scripted failure whose raw text carries folder/hostname/account detail, the state file's error field contains the closed class plus safe metadata — the raw string appears nowhere in the file (§4.12, FR-W1-23) | Scripted raw provider error; persisted state JSON inspected for the class and for the raw substring |
| MC-W1-28 | Every pooled operation's instrument record carries W1's pool sub-fields — `acquire_wait_ms`, `socket_count`, and the pool outcome mapped into w6-proof §6.1's frozen `outcome` domain (`outcome=pool_busy` on bounded-wait exhaustion, `outcome=ok` otherwise; the frozen shape has no established/reused field and W1 adds none — R2-I-2) — populated to w6-proof §6.1's frozen shape; a coalesced joiner records `socket_count=0` plus the shared-flight marker (register row 17; the record shape's single publisher is w6-proof) | W1's emitter seam observed per operation across DS-1/DS-3 scenarios; joiner rule checked in the coalescing matrix |

### 6.4 Integration boundaries

| External system / neighbor | Data flow | Contract | On failure |
|---|---|---|---|
| IMAP server (the only network peer W1 touches) | Dial/TLS/login/SELECT/STATUS/FETCH/STORE/APPEND via go-imap/v2 | Existing command semantics; structural `[NONEXISTENT]` for absence; session termination via logout-class close | Timeout/dial/protocol classes as today (`classifyMailError` stays the mapper); a server connection-limit response is recognized structurally (the landed `server_connection_limit` value of `MailUnavailableError.reason`, register row 7), the refused session retired, and the effective per-mailbox capacity for that server reduced in-process (see OQ-3) |
| Gateway REST handlers (w5-integration) | Typed outcomes (success / pool-busy / account-busy / backoff / transport class) + response revision metadata; the validated `observer_id` query param on panel reads | §3.1 error values; the 503 `reason` enum is **landed** (`MailUnavailableError.yaml`, register row 7, commit `5f23ae8a0`); w5-integration maps | Handlers render visible errors; cached rows stay, labelled (W2/W3) |
| Gateway WebSocket (w5-integration) | Observer bind/unbind events (transport-agnostic registry API) | §4.7 — authenticated-connection binding only; the frames are **landed** (the W0 wave's, commit `5f23ae8a0`), the handlers w5-integration's | A dead connection's observers are reaped by the gateway's existing liveness teardown; the registry applies the same removal rules |
| Agent tools (w5-integration injection; w4-features/W10 adapters) | Same session source injection; same typed outcomes | §4.11 — the tool wrapper keeps owning the budget | Tool result text unchanged in shape; transport failures visible per existing contract |
| Watcher state file (`email-watch/`) | W1 writes the safe classified representation only (§4.12); the skip/pool interplay otherwise unchanged | `LoadWatcherState`/`EffectiveState` remain the one derivation point; the directory is excluded from staging/backups and purged on mailbox removal — product-owned guarantees (founder Q-A/Q-B; register row 18's gate: w5-integration decides, W2 enforces first write) | Unreadable state fails open with visible WARN (unchanged, verified) |
| Credentials | Password resolution stays entirely outside W1 (callers pass `Account` as today) | ADR-004 boot contract unchanged | Unresolved password → the construction site's existing skip/404 behavior (verified) |

---

## 7. BDD scenarios

Scenario keys map as `B-W1-n`. Every scenario traces to its user story's acceptance scenario (`US-n/AS-m`).

#### Scenario B-W1-1: Warm reuse skips connection setup
**Traces to**: US-1/AS-1, US-1/AS-3 · **Category**: Happy Path
- **Given** a configured mailbox on a counting fake IMAP server and one completed folder read
- **When** a second read for the same mailbox runs within the idle window
- **Then** the server's accepted-connection count is unchanged by the second read
- **And** the second read's rows match the server's current folder state

#### Scenario B-W1-2: A different folder on a reused session returns that folder's data
**Traces to**: US-1/AS-2, US-3/AS-1 · **Category**: Happy Path
- **Given** a pooled session whose last completed operation selected Sent
- **When** a new borrower asks the same session's mailbox for Inbox
- **Then** the returned rows are Inbox's
- **And** a SELECT for Inbox was issued on that session before the fetch

#### Scenario B-W1-3: No work, no sockets — closed panel stays dark
**Traces to**: US-1/AS-4 · **Category**: Edge Case
- **Given** the panel closed and the idle window elapsed
- **When** several sweep intervals pass
- **Then** zero mail connections are open and zero mail commands are issued

#### Scenario B-W1-4: Third concurrent read for a capped mailbox waits, then busies
**Traces to**: US-2/AS-1 · **Category**: Error Path
- **Given** two operations in flight on one mailbox (both holding sessions)
- **When** a third read for that mailbox starts
- **Then** within the bounded acquisition window it receives the typed pool-busy outcome
- **But** the server never accepted a third connection for that mailbox

#### Scenario B-W1-5: Ninth global demand never creates a ninth socket
**Traces to**: US-2/AS-2 · **Category**: Error Path
- **Given** eight operations in flight across mailboxes
- **When** a ninth demand arrives from any path (panel, tool, watcher)
- **Then** it waits its bounded window and receives the typed busy outcome
- **And** the process-wide accepted-connection maximum observed is exactly 8

#### Scenario B-W1-6: A connecting reservation counts against the ceilings
**Traces to**: US-2/AS-3 · **Category**: Edge Case
- **Given** one dial for a mailbox blocked at the server greeting (reservation held, no established socket)
- **When** a second read for that mailbox arrives
- **Then** it waits rather than dialing a second socket, and on expiry receives the typed busy outcome
- **And** the connection counter shows exactly one accepted connection

#### Scenario B-W1-7: Failed dial releases its reservations
**Traces to**: US-2/AS-4, US-4/AS-1 · **Category**: Error Path
- **Given** a server that refuses connections
- **When** a read attempt fails
- **Then** the per-mailbox and global reservations return to baseline before the error reaches the caller
- **And** an immediately following read can acquire normally

#### Scenario B-W1-8: Concurrent borrowers never see each other's selected state
**Traces to**: US-3/AS-3 · **Category**: Alternate Path
- **Given** two borrowers on different sessions of one mailbox, one paused mid-command by the server
- **When** both complete
- **Then** each returned exactly its own requested folder's rows
- **And** neither borrower's rows contain the other folder's messages

#### Scenario B-W1-9: Reads preserve flags
**Traces to**: US-3/AS-2 · **Category**: Happy Path
- **Given** a folder with unread messages
- **When** pooled reads list and open-detail-fetch that folder
- **Then** every message's unread state is unchanged server-side
- **And** no non-PEEK body fetch appears in the captured command trace

#### Scenario B-W1-10: Structural folder absence is not a transport failure
**Traces to**: US-3/AS-4 · **Category**: Alternate Path
- **Given** a mailbox whose Sent folder does not exist and whose server answers the structural not-found
- **When** a read targets the sent role
- **Then** the caller receives the structural-absence outcome, not a transport error
- **And** the session stays healthy — the next read on it succeeds

#### Scenario B-W1-11: Pool-busy arrives within the acquisition bound
**Traces to**: US-4/AS-1 · **Category**: Error Path
- **Given** all of a mailbox's pool capacity held by slow operations
- **When** a new read's acquisition wait elapses
- **Then** the pool-busy outcome arrives within the 5-second acquisition bound (assert < 7 s)
- **And** it is the pool-busy class, not the timeout class

#### Scenario B-W1-12: Stalled server ends within the total deadline, socket retired
**Traces to**: US-4/AS-2, US-5/AS-1 · **Category**: Error Path
- **Given** a server that accepts and then never responds
- **When** a read runs against it
- **Then** the read ends by the 45-second total deadline with the timeout class
- **And** the socket it used is retired — the next borrow does not reuse it

#### Scenario B-W1-13: Handshake stall ends within the dial ceiling
**Traces to**: US-4/AS-3 · **Category**: Error Path
- **Given** a server that stalls during TLS/login
- **When** establishment is attempted
- **Then** it ends within the 30-second dial ceiling, inside the total read deadline

#### Scenario B-W1-14: Queue-expiry never dials
**Traces to**: US-4/AS-4 · **Category**: Error Path
- **Given** both of an account's work slots held and a caller whose remaining deadline is short
- **When** the deadline expires while queued
- **Then** the caller receives the existing busy outcome and the server observes zero new commands

#### Scenario B-W1-15: BYE retires; reader termination observed before counting the socket gone
**Traces to**: US-5/AS-2 · **Category**: Error Path
- **Given** a server that sends BYE mid-session
- **When** the affected operation ends
- **Then** the caller sees a visible transport-class error, the socket is closed and retired
- **And** a subsequent borrow uses a different session — no interleaved or stale response is possible

#### Scenario B-W1-16: One dead-idle replacement per read
**Traces to**: US-5/AS-3 · **Category**: Alternate Path
- **Given** an idle pooled session the server has silently closed
- **When** a read borrows it and the first command fails with a connection-class error
- **Then** the read replaces the dead session exactly once and completes inside the original deadline
- **But** if the replacement also proves dead, the read fails visibly — never a second replacement

#### Scenario B-W1-17: A mutation on a dead session fails visibly, once
**Traces to**: US-5/AS-4 · **Category**: Error Path
- **Given** a mark-read (or draft save, or send-copy) whose session proves dead mid-command
- **When** the failure surfaces
- **Then** the caller receives a retryable transport error
- **And** no automatic second attempt occurs — the server shows no duplicate effect

#### Scenario B-W1-18: One joiner's cancellation never fails the others
**Traces to**: US-5/AS-5 · **Category**: Edge Case
- **Given** three requests joined on one shared read flight
- **When** one joiner's context is cancelled mid-flight
- **Then** that joiner's wait ends with its own cancellation outcome
- **And** the flight completes normally for the other two joiners

#### Scenario B-W1-19: Two pairs, one account, different Sent mappings — separate results
**Traces to**: US-6/AS-1 · **Category**: Error Path
- **Given** two agent/workspace pairs configured on the same server account (same `host:port|username`) with different Sent folder mappings
- **When** both issue the same concurrent Sent list request
- **Then** each pair receives the result of its own mapping — real fake-server per-identity markers prove no result crossing
- **And** the account's two-slot work gate still bounds their combined concurrent dials

#### Scenario B-W1-20: A new generation never joins an old flight
**Traces to**: US-6/AS-2 · **Category**: Error Path
- **Given** an in-flight read under an older configuration/folder-mapping generation
- **When** the configuration changes and the identical read is issued under the new generation
- **Then** the new request does not join, receive, or cancel the old flight
- **And** each caller receives its own generation's answer

#### Scenario B-W1-21: A live refresh is never answered by a cache-shaped flight
**Traces to**: US-6/AS-3 · **Category**: Error Path
- **Given** a cache-shaped read and a live refresh for the same folder running concurrently
- **When** both complete
- **Then** the live response carries live server data and its live label
- **And** the cache-shaped flight's data never satisfies the live request

#### Scenario B-W1-22: Superseded read publishes nothing
**Traces to**: US-6/AS-4 · **Category**: Error Path
- **Given** a read paused after collecting its server data, its captured revision now superseded
- **When** a successful mutation and the refresh it triggers complete, and only then the paused read resumes
- **Then** the read's publish step is suppressed at W1's revision seam — the publish/commit path is not invoked and the response carries the superseded determination
- **And** the cache-row, disk-write and timestamp observables are asserted by W2's and w6-proof's end-to-end tests (register R-4: W1's suite asserts its seam; the cache-visible oracle lives with the publisher's package)
- **And** the post-mutation refresh's data stands everywhere
- **And** the refresh never joined the superseded flight

#### Scenario B-W1-23: Observer lifecycle governs retention
**Traces to**: US-7/AS-1, US-7/AS-2, US-7/AS-3 · **Category**: Happy Path
- **Given** two panel tabs observing one workspace with completed reads (sessions warm)
- **When** one tab closes
- **Then** retention continues for the remaining tab and only the closed tab's subscriptions stop
- **And when** the second tab closes too
- **Then** the workspace's idle panel sockets close immediately, and no panel-owned connection survives

#### Scenario B-W1-24: Last observer's departure during a shared read
**Traces to**: US-7/AS-4 · **Category**: Edge Case
- **Given** a shared panel read in flight when the last observer for its workspace departs
- **When** the read completes
- **Then** its socket closes instead of returning to retention
- **And** an independent tool read and a watcher cycle running at that moment complete normally

#### Scenario B-W1-25: Dead browser connection is reaped
**Traces to**: US-7/AS-5 · **Category**: Error Path
- **Given** a panel tab whose browser vanishes without a close message
- **When** the gateway notices the dead connection
- **Then** that connection's observers are removed and the standard last-observer close rules apply

#### Scenario B-W1-26: Without the presence channel, nothing is retained
**Traces to**: US-7/AS-6 · **Category**: Edge Case
- **Given** the presence frame handlers not registered (retention structurally disabled)
- **When** any read completes — panel-originated or not
- **Then** its socket closes; the open-connection count returns to baseline after every operation

#### Scenario B-W1-27: One stalled mailbox cannot stall the pass
**Traces to**: US-8/AS-1 · **Category**: Alternate Path
- **Given** thirteen due mailboxes of which one stalls at the server
- **When** the cycle pass runs under the bounded scheduler
- **Then** the other twelve complete their checks within the pass
- **And** the stalled mailbox's cycle does not block a second cycle for any other mailbox

#### Scenario B-W1-28: No second cycle while one is in flight
**Traces to**: US-8/AS-2 · **Category**: Edge Case
- **Given** a mailbox whose current cycle is still running at its next due moment
- **When** the scheduler considers it
- **Then** no second cycle for that mailbox starts

#### Scenario B-W1-29: Watcher skips honestly under saturation
**Traces to**: US-8/AS-3 · **Category**: Error Path
- **Given** the account's work slots and pool capacity fully occupied by foreground reads
- **When** a watcher cycle becomes due
- **Then** it is recorded as a skip: no dial occurs, the last successful-check time is unchanged, and backoff is not advanced

#### Scenario B-W1-30: Closed panel — watcher retains nothing and fills nothing
**Traces to**: US-8/AS-4 · **Category**: Edge Case
- **Given** the panel closed through at least three watcher cycle intervals
- **When** cycles run
- **Then** each cycle's socket closes after the cycle (the pool's open-connection counter returns to baseline), W1-observably
- **And** the watcher's only panel-metadata effect is the dirty-mark signal of §3.1 — no cache call exists on the watcher's path; the server-side cache-file assertions are W2's and w6-proof's (register R-4)

#### Scenario B-W1-31: Watcher never mutates
**Traces to**: US-8/AS-5 · **Category**: Happy Path
- **Given** a mailbox with new mail and a running watcher cycle
- **When** the cycle completes
- **Then** no flag was stored, no agent turn started, no task created — only the state file advanced

#### Scenario B-W1-32: One budget owner per operation
**Traces to**: US-4/AS-5 · **Category**: Edge Case
- **Given** the outer wrapper gating an operation and the pool injected below the client
- **When** the operation runs to completion
- **Then** exactly one account slot was acquired for it (instrumented at the budget)
- **And** under two concurrent operations per account, no third ever runs — no double acquisition is reachable from any path

#### Scenario B-W1-33: Retry bypasses backoff only
**Traces to**: US-4/AS-6 · **Category**: Alternate Path
- **Given** a mailbox in watcher backoff and all pool capacity held
- **When** the human Retry click fires
- **Then** the backoff gate is bypassed but the request still waits and busies on capacity — ceilings are never bypassed

#### Scenario B-W1-34: A client without its injection fails visibly
**Traces to**: US-2/AS-5 · **Category**: Error Path
- **Given** a client constructed without a session source in a process where the manager exists
- **When** a read is attempted through it
- **Then** the operation fails immediately with the typed missing-wiring error
- **And** zero dials occurred

#### Scenario B-W1-35: Watcher failure text is classified before it persists
**Traces to**: US-8/AS-6 · **Category**: Error Path
- **Given** a watcher cycle whose probe fails with a raw provider error embedding a folder name, a hostname and an account prefix
- **When** the failure is recorded through `recordFailure`
- **Then** the persisted watcher state contains only the safe classified error representation plus safe metadata
- **And** the raw provider text appears nowhere in the state file (grill-1 C-1; §4.12, MC-W1-27)

---

## 8. TDD plan (tests designed before implementation)

**Harness rule (carried from the ADR's test strategy):** all pooled-runtime tests run against the real in-tree fake IMAP server — `pkg/email/imapserver_test.go::startMemIMAP` (real `go-imap/v2` in-memory server on loopback TCP; the `imapDial` seam swapped per test and restored via cleanup). w6-proof (qa-lead, ADR-W5) extends the harness **in test code only** with: connection/LOGIN/SELECT/STATUS/FETCH command counters (wrapping the `imapDial` seam client-side and counting accepts server-side), controlled stalls at greeting/TLS/login/select/fetch, scripted BYE and idle-close, per-session identity markers, and a controllable clock for idle/revision timing. Tests must not run blindly in parallel (global seam). **No test hooks, flags or globals are ever set from production code**; the only test seams are test-owned package vars of the existing `imapDial` pattern, which production never assigns (production calls them on every dial — the invariant is never-*assign*, R2-M-2 wording correction). Expected values are derived from this design, never read off the implementation (oracle independence). Where a test's oracle lives in another package's files, the register's R-4 rule applies: W1's tests assert the W1-observable seam; the end-to-end oracle lives with the publishing package (W2's V-2, w6-proof's T15 and T1–T6).

| Order | Test name (indicative) | Level | Traces to BDD | What it proves |
|---|---|---|---|---|
| 1 | `TestPool_CeilingsHoldUnderConcurrency` | Unit (`startMemIMAP` + counters) | B-W1-4, B-W1-5 | Max accepted connections ≤ 2 per mailbox / ≤ 8 global across scripted panel+tool+watcher concurrency; every over-cap demand ends typed-busy; no ninth dial |
| 2 | `TestPool_ReservationsCountWhileConnecting` | Unit (greeting-stall server) | B-W1-6 | A dial blocked at greeting holds countable capacity; second borrower waits/busies; accept count 1 |
| 3 | `TestPool_FailedDialReleasesReservation` | Unit (refusing server) | B-W1-7 | After a failed dial, immediate re-acquire succeeds; counters at baseline |
| 4 | `TestLease_ReSelectBeforeUse` | Unit (command capture) | B-W1-2, MC-W1-6 | Borrow after a Sent user sees Inbox rows with a captured SELECT Inbox first |
| 5 | `TestLease_ConcurrentBorrowersIsolated` | Unit (mid-command pause) | B-W1-8 | Two borrowers, one paused: both return their own folder's rows |
| 6 | `TestLease_ReadsPreserveFlags_PeekOnly` | Unit (command capture + server flags) | B-W1-9 | Zero non-PEEK fetches on reads; unread states unchanged server-side |
| 7 | `TestLease_StructuralFolderAbsent_HealthySession` | Unit (`[NONEXISTENT]` scripted) | B-W1-10 | Absence outcome ≠ transport error; session survives for the next read |
| 8 | `TestPool_PoisonOnTimeout_Cancel_Bye_Protocol` (table) | Unit (stall/BYE/malformed scripted) | B-W1-12, B-W1-15 | Each poison cause closes and retires; no subsequent borrow reuses the session; reader termination observed before the socket is counted gone |
| 9 | `TestPool_DeadIdleReplacementOnce` | Unit (server kills idle session) | B-W1-16 | Read replaces a dead idle session exactly once, inside the original deadline; second dead replacement fails visibly |
| 10 | `TestPool_MutationNeverReplayed` | Unit | B-W1-17 | Mutation on dead session → single visible retryable failure; zero duplicate server effects; identical mutations never coalesce |
| 11 | `TestPool_JoinerCancelIsolation` | Unit | B-W1-18 | Cancelling one of three joiners fails only that waiter |
| 12 | `TestPool_IdleReleaseAndLRU` | Unit (fake clock) | MC-W1-11, MC-W1-12 | ~2-min idle close from last completed use; eviction picks least-recently-completed idle, never active |
| 13 | `TestPool_AllEightActiveTypedBusy` | Unit (held leases) | B-W1-11, MC-W1-13 | 9th demand: bounded wait (< 7 s) then the pool-busy class; accept count 8 |
| 14 | `TestPool_ReleaseNeverExpunges` | Unit (server-side state) | MC-W1-14 | `\Deleted`-flagged message survives release/eviction/idle-close; re-select still sees it |
| 15 | `TestBudget_CoalescingIdentityMatrix` | Unit | B-W1-19, B-W1-20, B-W1-21 | DS-3 matrix: same identity → one dial; each differing component → separate dials with correct per-identity results; account gate still bounds combined dials |
| 16 | `TestBudget_RevisionSupersededReadPublishesNothing` | Unit (paused read + mutation ordering) | B-W1-22, MC-W1-17 | DS-4 ordering, **asserted at W1's publish seam** (register R-4 / grill-1 I-4 re-scope): the superseded read's publish step is suppressed and the response carries the superseded determination; the refresh stands; the refresh never joined the old flight. The cache-row/disk/timestamp end-to-end oracle is W2's V-2 and w6-proof's T15 — not asserted here against W2-owned files |
| 17 | `TestPresence_RetentionLifecycle` | Unit (registry + fake clock) | B-W1-23, B-W1-24, B-W1-25 | DS-5 matrix: retention exactly while ≥1 observer; last-observer close immediate (idle) / at completion (active); disconnect/logout/workspace-exit removal; independent tool/watcher work untouched |
| 18 | `TestPresence_DisabledUntilWired` | Unit | B-W1-26, MC-W1-20 | With retention disabled: every operation's socket closes; count returns to baseline after each |
| 19 | `TestWatcherSet_BoundedFairProgress` | Unit (13 mailboxes, one stalled) | B-W1-27, B-W1-28 | Other due mailboxes complete within the pass; ≤1 cycle in flight per mailbox; stagger + due gate preserved |
| 20 | `TestWatcher_SkipLeavesLastCheckedUnchanged` | Unit (saturated capacity) | B-W1-29, MC-W1-22 | Skip recorded; state file's last-success/attempt unchanged; zero dials |
| 21 | `TestWatcher_ClosedPanelNoRetentionNoCacheFill` | Unit (closed panel, ≥3 intervals) | B-W1-30, MC-W1-23 | **W1-observable scope** (register R-4 / grill-1 I-4 re-scope): zero retained sockets on the pool counter; the watcher's only panel-metadata effect is the dirty-mark signal — no cache call exists on its path. Server-side cache-file assertions are W2's and w6-proof's, not asserted here against W2-owned files |
| 22 | `TestWatcher_NeverMutates` (regression, exists in shape) | Unit (`startMemIMAP`) | B-W1-31 | No STORE, no turn, no task — preserved through the rewrite |
| 23 | `TestBudget_OneOwnerPerOperation` | Unit (test-owned acquisition seam, `imapDial` pattern) | B-W1-32, MC-W1-24 | Exactly one account-slot acquisition per gated operation across all three paths; ≤2 concurrent per account ever; the seam is replaced by tests only, never assigned by production |
| 24 | `TestBudget_RetryBypassesBackoffOnly` | Unit | B-W1-33, MC-W1-25 | Retry bypasses the backoff gate; still bounded by slot and pool under saturation |
| 25 | `TestClient_MissingSourceFailsVisibly` | Unit | B-W1-34, MC-W1-26 | Nil session source in a process where the manager exists → typed wiring error, zero dials (the pre-injection window's legacy dial is the preserved regression suite's existing behavior, not this test's) |
| 26 | `TestPool_DeadlineBounds` (table: greeting/TLS/login/select/fetch stalls) | Unit (fake clock + stalls) | B-W1-12, B-W1-13, MC-W1-4 | Every outcome inside 45 s total; dial ≤ 30 s; command bound subordinate to remaining total |
| 27 | `TestMailFolders_MissingSentRegressionPool` (preserve + extend) | Unit (`startViewIMAP`) | B-W1-10 | The three `view_missing_folder_test.go` tests pass unchanged through the pooled path; page-path Sent/Drafts absence now succeeds where the count path already did |
| 28 | Existing budget/watcher RED pack (preserve) | Unit | — | `mail_budget_red_test.go`, `watcher_budget_red_test.go`, `watcher_backoff_red_test.go`, `watcher_cycle_if_due_red_test.go`, `append_timeout_red_test.go`, `dial_timeout_classification_test.go` pass unchanged (key-shape test updated to the new identity, never weakened) |
| 29 | `TestWatcher_RecordFailurePersistsOnlySafeClass` | Unit (scripted raw failure) | B-W1-35, MC-W1-27 | After a failure whose raw text carries folder/hostname/account detail, the persisted state holds only the classified representation; the raw substring appears nowhere in the file (grill-1 C-1 correction) |
| 30 | `TestPool_InstrumentSubFields` | Unit (W1's emitter seam) | MC-W1-28 | Every pooled operation's record carries populated `acquire_wait_ms`/`socket_count`, `outcome=pool_busy` on bounded-wait exhaustion and `outcome=ok` otherwise, to w6-proof §6.1's frozen shape; the frozen shape has no established/reused discriminator and W1 adds none — reuse evidence is the accept-counter suite's (T1/T2), not a record field; a coalesced joiner records `socket_count=0` + the shared-flight marker (register row 17; the record shape's single publisher is w6-proof, whose T1–T6 are the end-to-end oracle) |

### 8.1 Test datasets

| Dataset | Rows (boundary → edge → error → happy) | Traces to |
|---|---|---|
| DS-1 Socket pressure | 1 mailbox 1 op (baseline), 2 concurrent same-mailbox (at cap), 3rd same-mailbox (refused), 8 global concurrent (at cap), 9th global (refused), 13 mailboxes × light load (turnover + LRU), 2 pairs on 1 account (shared account gate), dial-in-flight + demand (reservation counting) | B-W1-4/5/6, B-W1-19 |
| DS-2 Poison causes | command timeout, context cancellation, scripted BYE, malformed/protocol-failure response, silently-closed idle session (dead-idle, read → one replacement), dead session during a **mutation** (no replay), poisoned socket offered for reuse (must be refused), `[NONEXISTENT]` select (healthy — contrast row) | B-W1-12/15/16/17, B-W1-10 |
| DS-3 Coalescing identity | same pair+generation+op+args+purpose (share); differs by pair (2 pairs/1 account, different Sent mappings); differs by generation (reconfig mid-flight); differs by purpose (cache vs live); differs by args (different folder/limit); differs by operation; empty params (opt-out, preserved); joiner-cancel mid-flight; mutation pair (never coalesce) | B-W1-18/19/20/21 |
| DS-4 Revision ordering | read paused → mutation completes → read resumes (publishes nothing); read completes → mutation (publishes, normal); mutation → refresh (never joins old flight); UIDVALIDITY change mid-read (supersedes); two delayed responses out of order (newest revision wins at the consumer); cache-shaped vs live same folder | B-W1-21/22 |
| DS-5 Presence | 1 observer warm reuse; 2 tabs → close 1 (retain); close last (idle close immediate); active op during last close (close at completion); socket drop without close frame; logout; workspace exit; detached flight finishes after last observer (no retention); retention disabled entirely (every op closes); REST presenting a foreign observer ID (no authority) | B-W1-23/24/25/26 |
| DS-6 Watcher | 13 due, 1 stalled (12 progress); mailbox mid-cycle at due moment (no second cycle); all capacity foreground-held (skip; state unchanged); panel closed ≥3 intervals (no retention/no fill/dirty-mark only); backoff window (no dial, unchanged); new mail (state advances once); UIDVALIDITY reset (baseline rule preserved) | B-W1-27/28/29/30/31 |
| DS-7 Deadlines | greeting stall, TLS stall, login stall, select stall, fetch stall, queue expiry under account saturation, acquisition-window expiry (5 s), total-deadline boundary (success at 44 s, failure by 45 s + slack), failed dial reservation release | B-W1-7/11/12/13/14 |

### 8.2 Regression impact

**Preserved unchanged (must keep passing through the rewrite):**

- `pkg/email/view_missing_folder_test.go` — all three named tests (§2.1). The pooled path must not soften anything they pin.
- `pkg/email/mail_budget_red_test.go` — all six named tests; `TestMailBudget_SingleflightKeyIncludesParams` is updated by w6-proof to the new identity shape **without weakening** (it must still pin that params participate and that paramless ops opt out).
- `pkg/email/watcher_budget_red_test.go` (skip-is-not-failure; cycle-through-budget), `watcher_backoff_red_test.go` (ladder bounds + jitter window), `watcher_cycle_if_due_red_test.go` (no dial in backoff), `append_timeout_red_test.go`, `dial_timeout_classification_test.go`.
- The existing visible-failure contract: 503 `backoff`/`busy` envelopes (`mailBudgetErr`), Retry-bypasses-backoff-only, no dial during backoff.

**New regression tests required:**

- The pooled path serving `FolderCounts`/`ReadFolderPage`/`ReadView` must reproduce today's success shapes byte-for-shape (same wire types, same nil-vs-empty slice handling — `mailNonNilSlice` contract) so W2's rewiring cannot silently change responses.
- The `Transport` interface method set and `AccountKey` derivation (`host:port|username`) are pinned by existing tests; the spec adds the explicit assertion that `AccountKey` is unchanged after the rewrite (it is the contention key everything else keys from).

---

## 9. Counterexamples — what a careless implementation would survive

Each row is a test that must **fail** against the named careless implementation. The first is the dispatch-mandated mutation case.

| # | Careless implementation (the mutation) | Counterexample that kills it | Traces to |
|---|---|---|---|
| CX-1 | **Count only established sockets, not connecting reservations.** The ceiling check happens at borrow-of-established-socket time; a dial in progress is invisible to the counters. | B-W1-6: hold one dial at a greeting stall (reserved, not established); the second same-mailbox demand must wait/busy with exactly 1 accepted connection. The careless version dials a second socket — the accept counter and the waiter's outcome both expose it. | US-2/AS-3 |
| CX-2 | **Skip re-select on lease reuse** (assume the last borrower left the right folder selected). | B-W1-2: after a Sent operation, borrow for Inbox; assert Inbox rows *and* a captured SELECT Inbox before the fetch. The careless version returns Sent rows labeled Inbox. | US-3/AS-1 |
| CX-3 | **Return a timed-out socket to the idle pool** (close raced, poison flag missed on the cancellation path). | B-W1-12: after a total-deadline timeout mid-command, the next borrow must not observe that session; server-side session identity must differ. The careless version interleaves a stale reader's response into the next borrower's stream. | US-5/AS-1 |
| CX-4 | **Double-acquire the account slot** (outer wrapper gates; the injected client "helpfully" gates again internally). | B-W1-32: instrument acquisitions at the budget; one panel read must show exactly 1 acquisition; two concurrent per account must admit exactly 2. The careless version shows 2 acquisitions for one operation and deadlocks or starves the second account user. | US-4/AS-5 |
| CX-5 | **Coalesce on the old key** (account+operation+params) while carrying the new identity fields as unused metadata. | B-W1-19: two pairs/one account/different Sent mappings, concurrent identical requests — careless version serves the second pair the first pair's Sent rows (real per-identity server markers catch it). B-W1-21 catches the purpose dimension the old key cannot see. | US-6/AS-1, US-6/AS-3 |
| CX-6 | **Let a superseded read publish** (validate only at request time, not at publication time). | B-W1-22: pause the read post-fetch; mutate + refresh; resume. The careless version invokes W1's publish path for the superseded read — the seam-level assertion (publish step not invoked; superseded determination on the response) kills it. The cache-file/timestamp consequences are additionally killed end-to-end by W2's V-2 and w6-proof's T15 (register R-4). | US-6/AS-4 |
| CX-7 | **Issue folder-CLOSE on release** (natural teardown habit) with `\Deleted` pending. | MC-W1-14: set `\Deleted` without expunge (non-UIDPLUS shape), release, re-select: the message must still exist. The careless version expunged it silently. | US-3 |
| CX-8 | **Auto-retry a mutation on a fresh socket** after a mid-command session death (reconnection convenience). | B-W1-17: mutation on a session killed mid-command; careless version produces two server-side effects; the test asserts exactly one visible failure and no duplicate. | US-5/AS-4 |
| CX-9 | **Watcher retains its socket "for efficiency"** when a panel happens to be open in another workspace, or fills the folder cache during a cycle. | B-W1-30 + B-W1-24: closed-panel intervals show zero retained sockets and zero cache writes; the independent-work scenario shows watcher completion never extends retention. | US-8/AS-4 |
| CX-10 | **Sequential watcher loop kept** (bounded-fairness claimed in a comment, loop unchanged). | B-W1-27: 13 due mailboxes, one stalled server-side; careless version completes ≤2 within the pass window; the test requires the other 12 to progress. | US-8/AS-1 |
| CX-11 | **Retention keyed by a caller-supplied observer ID** (REST param trusted). | DS-5 last row: a REST request presenting a foreign observer ID gains no retention and no authority; only the authenticated connection's own frames bind observers. | US-7 |
| CX-12 | **Nil session source silently falls back to per-call dialing** once the manager exists (back-compatibility convenience; the post-injection shape — R2-I-1). | B-W1-34: with the manager present, the operation must fail with the typed wiring error and zero dials; the careless version dials uncounted — the accept counter catches the dial the ceilings never saw. | US-2/AS-5 |
| CX-13 | **Persist the raw provider error at `recordFailure`** ("log it all — it is only a state file"; the exact defect grill-1 C-1 found unowned). | B-W1-35 / MC-W1-27: a scripted failure whose raw text embeds a folder name, a hostname and an account prefix; the persisted state must contain only the classified representation — the raw substring appears nowhere in the file. The careless version fails the substring scan. | US-8/AS-6 |

---

## 10. Functional requirements, success criteria, traceability

### 10.1 Functional requirements

| ID | Requirement |
|---|---|
| FR-W1-1 | The system MUST provide exactly one application-owned IMAP connection manager per data dir, injected into every client-producing path (panel, agent tools, watcher); a client MUST NOT construct a private manager. (§4.1) |
| FR-W1-2 | A production dial through a client with no injected manager MUST fail with a typed, visible wiring error and MUST NOT fall back to an uncounted dial, **in a process where the manager exists** — the manager's existence begins with w5-integration's Wave-D injection (register rows 9/12); before that injection lands, the legacy per-call dial remains the production behavior by design (§4.1 blast-radius note). The nil-source path exists for tests only. (§4.1, MC-W1-26, R2-I-1) |
| FR-W1-3 | The pool MUST cap concurrent sockets+reservations at 2 per mailbox identity and 8 globally, on every path and interleaving, counting connecting reservations. (§4.2, MC-W1-1/2) |
| FR-W1-4 | The pool identity MUST bind pair + endpoint/TLS identity + non-secret generation, MUST NOT contain password text, and MUST NOT share sockets across pairs. (§4.2) |
| FR-W1-5 | A failed dial MUST release every reservation it held before its error propagates. (§4.2, MC-W1-5) |
| FR-W1-6 | Each operation MUST hold an exclusive lease for its socket's full duration; the requested folder MUST be (re-)selected and validated before the borrower's first command; SELECT data MUST be returned to the borrower for epoch checking. (§4.3, MC-W1-6) |
| FR-W1-7 | Read commands on a lease MUST NOT write flags (PEEK discipline); only explicit mutation operations write, and they MUST NOT be coalesced or automatically replayed. (§4.3/§4.5, MC-W1-7/10) |
| FR-W1-8 | A structural folder-absent SELECT answer MUST surface as a typed absence outcome with the session kept healthy; transport-class select failures MUST poison. (§4.3) |
| FR-W1-9 | The acquisition order MUST be: backoff → coalesce → account slot → pool reservation → establish/reuse; no manager lock during network I/O; no borrower waits for a second account slot. (§4.4) |
| FR-W1-10 | Every pooled read MUST carry a 45 s total deadline (queue+dial+commands; a shorter caller deadline wins), a ≤5 s pool-acquisition cap ending in the typed pool-busy outcome, and the 30 s dial ceiling as a subordinate bound. (§4.4, MC-W1-3/4) |
| FR-W1-11 | Timeout, cancellation, server BYE and protocol failure MUST close and retire the socket; reader termination MUST be observed before reuse decisions. (§4.5, MC-W1-8) |
| FR-W1-12 | A dead idle socket MAY be replaced exactly once per read, inside the original deadline and backoff rules; mutations MUST NOT be auto-retried. (§4.5, MC-W1-9/10) |
| FR-W1-13 | Sockets idle past ~2 minutes since last completed use MUST be closed; eviction MUST select the least-recently-completed idle socket and MUST NOT evict active work; all-eight-active MUST end in bounded wait then typed busy, never a ninth socket. (§4.6, MC-W1-11/12/13) |
| FR-W1-14 | Release, idle-close and eviction MUST NOT issue a folder-CLOSE; pending `\Deleted` messages MUST survive. (§4.6, MC-W1-14) |
| FR-W1-15 | Panel sockets MAY be retained only while ≥1 authenticated-connection-bound observer for the workspace is open; per-tab observers; removal on close/disconnect/logout/workspace exit; detached flights close instead of retaining after the last observer. (§4.7, MC-W1-19) |
| FR-W1-16 | Until the presence channel is registered, retention MUST be structurally disabled (request-scoped sockets everywhere). (§4.7, MC-W1-20) |
| FR-W1-17 | Read coalescing MUST key on pair + generation + operation + normalized arguments + purpose; the account key MUST retain only its contention role; mutations and watcher cycles MUST NOT coalesce. (§4.8, MC-W1-15/16) |
| FR-W1-18 | A read MUST capture the folder publication revision before server work; a read whose captured revision is superseded at completion MUST publish nothing (memory, disk, timestamps, response state); a post-mutation refresh MUST NOT join a superseded flight. (§4.9, MC-W1-17/18) |
| FR-W1-19 | The watcher MUST run due cycles with bounded fairness (≤1 cycle in flight per mailbox; one stalled mailbox MUST NOT delay others), MUST acquire non-blockingly (skip on no capacity, last-checked unchanged, backoff not advanced), MUST NOT retain sockets or fill panel caches while no panel is open, and MUST keep its existing cadence/backoff/stagger/state machine. (§4.10, MC-W1-21/22/23) |
| FR-W1-20 | Each path's outer wrapper MUST remain the single budget owner for its operations; the session source MUST NOT acquire account slots; `retry=true` MUST bypass only the backoff gate. (§4.11, MC-W1-24/25) |
| FR-W1-21 | The watcher cycle's STATUS probe and reads MUST ride the same pool and lease rules as every other path (no uncounted dial path may exist). (§4.1/§4.10) |
| FR-W1-22 | The pool MUST introduce no timer except socket/idle expiry, and no mail-data request of any kind while no request is in flight. (§4.6/§4.7) |
| FR-W1-23 | `recordFailure` MUST persist only the safe classified error representation (the closed class plus safe metadata) and MUST NOT persist raw server error text; the same closed classes are what W1 supplies for the typed 503 `reason` mapping. The gateway `mailErr502` half of the redaction obligation belongs to w5-integration (register row 19). (§4.12, MC-W1-27) |
| FR-W1-24 | The lease MUST expose the selected folder's live epoch (`UIDValidity`) and the pair's generation to the borrower before its first command, enabling same-lease validation of a gateway-issued `message_ref`; W1 provides the capability only — validation is W2's (`view.go`), issuance is w5-integration's. (§3.1/§4.3, register row 16) |
| FR-W1-25 | The pool MUST populate its sub-fields (`acquire_wait_ms`, `socket_count`) of the instrument record whose shape w6-proof §6.1 freezes, for every pooled operation, and MUST record pool exhaustion as `outcome=pool_busy` (a safe class in the frozen `outcome` domain) and success as `outcome=ok`; the frozen shape has no established/reused discriminator and W1 MUST NOT add one (one shape, one publisher — w6-proof, register row 17); the coalesced-joiner rule (`socket_count=0` + shared-flight marker) applies; W1 MUST NOT define a second record shape. (§3.1, MC-W1-28, R2-I-2) |
| FR-W1-26 | The watcher MUST signal panel-metadata dirtiness only through the published dirty-mark interface (§3.1, register row 13) and MUST make no other panel-cache interaction; the signal MUST be transport-agnostic and carry no mail data. (§4.10, MC-W1-23) |

### 10.2 Success criteria

| ID | Criterion |
|---|---|
| SC-W1-1 | Every test in §8's plan passes on CI (`go`, `race` tiers) with red-before-green receipts for the new tests; the preserved regression set (§8.2) passes unchanged. |
| SC-W1-2 | Under the DS-1 pressure matrix, the fake server's maximum observed accepted-connection count never exceeds 2 per mailbox / 8 global, with every refusal typed. |
| SC-W1-3 | Under DS-7, no read outcome (success or failure) exceeds the 45 s total bound + scheduling slack; pool-busy arrives < 7 s. |
| SC-W1-4 | The DS-3 identity matrix shows exactly one dial per identical identity and never a cross-pair/cross-generation/cross-purpose result. |
| SC-W1-5 | The DS-4 ordering cases show zero superseded publications at **W1's seam** — the superseded read's publish step is suppressed and the response carries the superseded determination (asserted by T16); the memory/disk/response end-to-end oracle is W2's V-2 plus w6-proof's T15 (register R-4). W1's suite asserts the seam; it writes no cache-file assertion of its own. (R2-M-3 wording aligned with MC-W1-17/B-W1-22/T16.) |
| SC-W1-6 | With the panel closed through ≥3 watcher intervals: zero open sockets, zero cache writes, watcher badge state honestly dated. |
| SC-W1-7 | A user browsing folders/lists in the Mail panel completes every operation with correct data through the pooled path (UAT lane evidence); a second visit within the idle window issues measurably fewer server connections than operations (counter receipt). |
| SC-W1-8 | No new footprint claim is made without measurement — the criterion fails without both numbers (R2-M-4): **(a) goroutine overhead** — a runtime goroutine-count snapshot at steady state under the DS-1 pressure matrix on the pooled build versus the same harness pre-change (the harness is w6-proof's test code; the W1 implementer runs both counts and records the two numbers with their log paths and exit codes in the delivery receipt); **(b) retained-socket ceiling arithmetic** — 8 sockets × the per-connection buffer the go-imap/v2 client allocates, with the buffer size cited from the library source (not recalled) in the receipt; **(c) the check** — the feature gate's code-reviewer fails the footprint claim if either number is absent, a log path is missing, or the sum exceeds the <10 MB security-feature envelope. A receipt stating "overhead acceptable" without both measured numbers does not satisfy this criterion. |

### 10.3 Traceability matrix

| Requirement | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-W1-1 | US-1 | B-W1-34 (US-1's warm-reuse premise needs the injected manager; the wiring-error scenario itself anchors US-2/AS-5) | T25, T2, T3 |
| FR-W1-2 | US-2 | B-W1-34 | T25; CX-12 |
| FR-W1-3, FR-W1-4, FR-W1-5 | US-2 | B-W1-4, B-W1-5, B-W1-6, B-W1-7 | T1, T2, T3; CX-1 |
| FR-W1-6, FR-W1-7, FR-W1-8 | US-3 | B-W1-2, B-W1-8, B-W1-9, B-W1-10 | T4, T5, T6, T7; CX-2, CX-7 |
| FR-W1-9, FR-W1-10 | US-4 | B-W1-11, B-W1-12, B-W1-13, B-W1-14, B-W1-32, B-W1-33 | T26, T23, T24; CX-4 |
| FR-W1-11, FR-W1-12 | US-5 | B-W1-12, B-W1-15, B-W1-16, B-W1-17, B-W1-18 | T8, T9, T10, T11; CX-3, CX-8 |
| FR-W1-17, FR-W1-18 | US-6 | B-W1-18, B-W1-19, B-W1-20, B-W1-21, B-W1-22 | T15, T16; CX-5, CX-6 |
| FR-W1-15, FR-W1-16 | US-7 | B-W1-23, B-W1-24, B-W1-25, B-W1-26 | T17, T18; CX-11 |
| FR-W1-19, FR-W1-21, FR-W1-26 | US-8 | B-W1-27, B-W1-28, B-W1-29, B-W1-30, B-W1-31 | T19, T20, T21, T22; CX-9, CX-10 |
| FR-W1-13, FR-W1-14 | US-1, US-2 | B-W1-5, B-W1-11 | T12, T13, T14 |
| FR-W1-20 | US-4, US-5 | B-W1-32, B-W1-33 | T23, T24; CX-4 |
| FR-W1-22 | US-1, US-8 | B-W1-3, B-W1-30 | T3, T21 |
| FR-W1-23 | US-8 | B-W1-35 | T29; CX-13 |
| FR-W1-24 | US-3, US-6 | B-W1-2, B-W1-19 (the lease rows the validation rides) | T4, T15 (capability half; the validation FRs themselves are W2's, register row 16) |
| FR-W1-25 | US-2, US-4 | (cross-spec oracle: w6-proof T1–T6; W1's half has no separate BDD — the record's shape is w6-proof's published interface) | T30 |
| FR-W1-26 | US-8 | B-W1-30 | T21 |

(Test numbers reference §8's order column, e.g. T15 = `TestBudget_CoalescingIdentityMatrix`.)

---

## 11. Non-goals

| Outside this work package | Boundary / reason |
|---|---|
| Folder discovery, overrides, unknown-vs-absent classification, the folder-mapping file, header cache, and every `pkg/email/view.go` edit | W2's package (§3.3). W1 consumes resolved folder names and publishes the lease; W2 never edits `transport.go`. |
| Wire contracts, generated types, the presence WebSocket frames, the 503 `reason` enum, `mode=cache_first|live`, `publication_revision` response metadata | **Landed on this branch** in the W0 contracts wave (backend-lead; commit `5f23ae8a0`; register rows 1/2/5/7); w5-integration's handlers consume/map. W1 produces the typed error values and revision-capture seam only. |
| Gateway/agent injection call sites, boot/reload wiring, removal cascades, watcher-provider re-injection | w5-integration's package (ADR-W4). W1 ships the seams; §4.1's switch is w5-integration's integration wave. |
| Data-directory and backup exclusion enforcement | **Product-owned, no operator-machine dependency** (founder ruling Q-A, 2026-10-02): the product itself guarantees its Mail cache directory and its `email-watch/` state directory are excluded from data-directory version-control staging and from the application's own backups/archives on every install, by its own means — with watcher state files also purged on mailbox removal (founder ruling Q-B). Ownership per register row 18: w5-integration publishes the gate decision; W2 enforces the first-write refusal; the gate fails closed to live-only with the visible `cache_unavailable` notice. This design depends on no operator-side provisioning to hold: the guarantee is the product's, checked and refused by the product when it cannot prove exclusion. W1's own contribution is the `recordFailure` redaction (§4.12). |
| All test files | w6-proof's package (qa-lead; ADR-W5). This spec names them (§8); production owners never edit tests. |
| JMAP transport selection, encrypted disk header cache, persisted JMAP state, Phase 2 invalidation events | W6/later waves after separate Phase 1 evidence. Plain IMAP only here. |
| SMTP pooling or any send-path change | SMTP stays request-scoped outside the read pool (verified ADR boundary). |
| IDLE / server-push subscriptions | Deferred by the ADR (13 rotating IDLE subscriptions exceed the 8-socket ceiling; confounds the plain-IMAP benchmark). |
| Mail-driven agent turns, tasks, or a drainer reintroduction | Retired surfaces; the watcher stays metadata-only. |
| Performance tuning to hit the accepted latency bars | The bars (Q3=A) are *targets* judged by the separate measurement plan; W1 delivers correct bounded mechanics, not a benchmark result. |
| ~~Sanitizing watcher error text~~ — **no longer a non-goal** (correction round) | The watcher half is **W1-owned** (§4.12, FR-W1-23): `recordFailure` persists only the safe classified representation. The earlier refusal ("W4's logging policy, not W1's task") is corrected per grill-1 C-1 and register row 19. The gateway's `mailErr502` raw-error log remains outside W1 — it is w5-integration's file and its obligation. |

---

## 12. Definition of Done

**Code correct and tested** — evidenced by all of:

1. **RED before green:** every new test in §8 first ran and failed against the pre-change code — proven by CI on a tests-only commit or by the one dispatcher-owned narrow local run (`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^<Name>$' -p 1 ./pkg/email/`, one at a time) — then passed on the implementation branch, with receipts (log path + exit code) per claim.
2. **CI green on the full gate set** for the branch: `gofmt go-build go-vet lint go-test go-race` (Go tier) — race is mandatory: this package is concurrency-first. No pre-existing failure is waved through ("ours to fix", Hard Constraint #7).
3. **Preserved regressions:** the §8.2 set passes unchanged; `TestMailBudget_SingleflightKeyIncludesParams` updated only to the new identity shape, never weakened; w6-proof's CHECK audit (mutation check + test-integrity-audit) returns PASS on the new suite.
4. **Counterexamples demonstrably kill:** at minimum CX-1 (reservation counting), CX-6 (superseded publication) and CX-13 (raw watcher error text) shown failing against a deliberately mutated implementation in w6-proof's CHECK receipt.
5. **No test hook in production code:** grep-verifiable — no pool/budget/revision flag, global or setter exists that production sets for tests. The only permitted test seams are test-owned package vars of the existing `imapDial` pattern (`pkg/email/transport.go::imapDial`: tests reassign, production never does), which resolves grill-1 M-9's tension with MC-W1-24's acquisition counter without weakening this rule.

**Reachable by a user/agent** — evidenced by all of:

1. **Panel path:** a real user opens the Mail panel on a configured mailbox and reads folders/lists/messages served through the pooled path — UAT lane evidence (executed, not written), with the counter receipt showing fewer server connections than operations on the warm pass (SC-W1-7).
2. **Agent path:** `read_inbox` / `search_email` / `read_message` — the existing registered tools — run through the same pool; registration is unchanged by W1 (no new tool; the Hard Constraint #6 check — `grep -rl '"read_inbox"' pkg/coreagent/ pkg/config/ pkg/tools/` non-empty — passes as today, re-run and recorded).
3. **Watcher path:** the badge advances on real mail with the panel closed, the state file's last-checked honestly reflects skips, and a forced failure leaves only the safe classified error in the state file — no raw provider text (executed watcher scenario, §4.12).
4. **No dead-end configuration:** every new behavior (busy outcomes, retention) is observable through existing surfaces — the busy outcome renders through the panel's existing 503 handling; retention needs no user action.
5. **User-facing documentation updated in the same change** (§13) and audited by `docs-verifier` against actual behavior.

Both lines are stated separately in the delivery report and neither is merged into the other.

---

## 13. User-facing documentation TODOs

W1's user-visible outcomes are narrow (speed feel, busy states, honest last-checked). Drafting owners follow the ADR's docs table; `docs-verifier` audits each against actual behavior before landing.

| Page | Owner | What must be said (W1-scoped additions) |
|---|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/mail.md` | W3 (frontend-lead) drafts | In the existing "If something goes wrong" section: the "Mail is busy. Try again." state — that it appears when too many mail requests run at once (many tabs/mailboxes), that Retry still respects the limits, and that this is not an error in their mailbox. A short note that repeatedly opening folders/messages no longer reconnects every time (faster repeat visits), with no promised numbers. That closing Mail stops its background activity. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/troubleshooting.md` | w5-integration (backend-lead; ADR-W4) drafts | New Mail guidance distinguishing: **busy** (too many simultaneous requests — wait and retry), **backoff** (the mailbox failed repeatedly; automatic retries paused until the shown time; Retry overrides the pause once), and a **server connection limit** (the provider refuses more connections; Omnipus adapts, configured maxima are ceilings not promises). That agent tasks and the new-mail watcher are not interrupted by closing Mail. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/connectors.md` | w5-integration (backend-lead; ADR-W4) | One sentence under the mailbox settings: Omnipus keeps at most two live connections per mailbox and eight overall, and reuses healthy ones — operators sizing self-hosted servers can plan with those numbers. |

(Nothing in `docs/library.md` or `docs/security.md` changes from W1 alone; their pending updates belong to the attachment/encryption work packages.)

---

## 14. Open questions

| Q | Question | Status | Resolution / remaining options |
|---|---|---|---|
| OQ-1 | **Generation source shape** (who implements; W1 consumes an opaque string). | **DECIDED** (correction round — register row 12, conflict resolved across w1 OQ-1 / w2 OQ-2 / w5 MC-17) | One construction for all three consumers: the generation is a **non-secret fingerprint of the canonical pair identity plus the persisted config epoch** (this table's former option C — catches endpoint/credential/override changes and invisible re-saves; matches the ADR's "reconfiguration/removal increments the generation"); the pair ID is a **randomly minted 128-bit value stored beside the pair config**. The credential-material fingerprint (w5's former MC-17) was rejected: it orphans caches on rotation and derives a linkable value from key material. **w5-integration implements both; W1 and W2 treat them as opaque.** Lands in the integration wave. |
| OQ-2 | **Does the 45 s total read deadline bound agent-tool reads too** (`read_inbox`, `search_email`, `read_message`), or only panel reads? | **OPEN** — owner: architect, decided before w4-features' Wave C dispatch (it shapes the tool wrapper's deadline plumbing, not W1's mechanics) | **A** — panel reads only; tools keep today's looser turn-context bounds. **B** — all pooled reads including tools; watcher STATUS exempt. **Recommendation B stands**: a tool read is the same operation shape; a hung tool dial is exactly what the original `dialTimeout` existed to bound (verified comment), and equal treatment keeps the "every read finishes inside its stated bounds" story honest. Watcher stays on cadence+backoff+per-command bounds either way. |
| OQ-3 | **Server connection-limit handling:** when a server refuses connections beyond its own lower limit, how far does W1 go in Phase 1? | **OPEN** — owner: architect (the wire value is settled: `server_connection_limit` landed in `MailUnavailableError.reason`, register row 7, commit `5f23ae8a0`; only the in-process capacity-reduction depth is open); decided before Wave C | **A** — recognize + retire + typed error only; capacity reduction deferred. **B** — also reduce the effective per-mailbox reservation ceiling for that server identity in-process (down to 1 if the response establishes it), reset on process restart; persisted adjustment deferred. **Recommendation B stands**, with the recognition strictly structural (the response must establish the limit — the ADR forbids promoting unknown text to a precise cause). In-process only; no persistence, no reconnect fighting. |
| OQ-4 | **Watcher parallelism bound:** exact maximum concurrent watcher cycles? | **OPEN** — owner: W1 implementer (an internal tunable constant, not founder-set; fixed in code review of `watcher_set.go`) | **A** — fixed small constant (e.g. 3). **B** — unbounded goroutine-per-due-mailbox (safety from non-blocking acquisition alone). **C** — derived: min(configured mailbox count, small constant). **Recommendation A with 3 stands**: enough to keep 13 mailboxes' cadence honest under normal latency, small enough that a pathological server cannot park many goroutines; per-mailbox single-flight guard does the rest. |

---

## 15. Holdout evaluation scenarios (post-implementation; NOT in the traceability matrix)

Evaluated outside the codebase, by a human or independent validator, on a real configured mailbox:

1. **Warm-repeat feel (happy):** open a folder, go back, open it again within a couple of minutes — the second open is noticeably quicker and shows correct current data; open a different folder — its data is its own.
2. **Two-client honesty (happy):** with the panel open in two browser tabs, mark a message read in one; the other tab reflects reality after its next eligible refresh, and neither shows stale rows labeled fresh.
3. **Closed-panel quiet (edge):** close Mail completely, watch the mail server's session list (or a self-hosted server's logs) for two-plus minutes — no Omnipus sessions linger beyond the idle window, while the new-mail badge still updates within its cadence.
4. **Cap refusal (error):** scripted/atypical load — many mailboxes opened in rapid succession across tabs — produces at most a clear "Mail is busy" message on the excess, never an error storm, and recovery is immediate once load drops.
5. **Broken-server behavior (error):** point a mailbox at a port that accepts and hangs; every panel action ends with a visible named failure with Retry inside a minute — no spinner survives the bound.
6. **Server restart survival (edge):** restart the mail server between two panel actions; the first action after the restart either succeeds (one in-bounds reconnect) or fails visibly with Retry — never wrong data.
7. **Recovery after failure (happy):** after a deliberate failure (server stopped and restarted), press Retry once — the action completes with correct data, the error clears, and no duplicate side effect occurs (a marked-read message is marked once).

---

## 16. Grounding and verification

Every code claim in this spec was checked first-hand in this checkout; commands ran read-only; nothing was built, tested or measured. Certainty: **Verified** = read/run in this task; **Inferred** = architectural consequence, reasoned not tested; **Unknown** = genuine gap owned elsewhere.

| Claim | Evidence | Certainty |
|---|---|---|
| Client is connectionless per call; every read dials+logs in+SELECTs INBOX and closes | `pkg/email/transport.go::Client` (doc comment + struct), `::dialIMAP` (verified: dial → login → `client.Select("INBOX", nil)`), `::runIMAP` (per-command `commandTimeout` bound, buffered result channel) | Verified |
| Account key is `host:port|username` (contention only after I-01); mail_budget.go header comment is stale | `pkg/email/transport.go::AccountKey` (comment records the port-collision defect); `pkg/email/mail_budget.go` file header vs code | Verified (discrepancy noted) |
| Flight key omits pair/generation (I-01's exact defect); params JSON-encoded; empty params opt out | `pkg/email/mail_budget.go::MailBudgetRequest.flightKey` (`Account + "\x00" + Operation + "\x00" + json(Params)`); `::call` (DoChan, detached `flightContext`, joiner select) | Verified |
| Budget = backoff → singleflight → 2-slot semaphore; `TryCall` non-blocking, never coalesces; skip ≠ failure | `pkg/email/mail_budget.go::backoffRefusal`, `::TryCall`, `runDialValue`, `mailBudgetSlotsPerAccount = 2`; `pkg/email/watcher.go::Cycle` (ErrMailSkipped branch) | Verified |
| Watcher: 60 s cadence, 60 s→15 min cap ±20% jitter, auth→cap; sequential `CycleAll`; stagger i×1 min; `cycleIfDue` due gate; STATUS probe; `recordFailure` takes raw text | `pkg/email/watcher.go::WatcherBackoff`, `::WatcherInitialOffset`, `::probe`, `::recordFailure`, `::cycleIfDue`; `pkg/email/watcher_set.go::CycleAll` (sequential `for` verified); `pkg/email/agent_read.go::MailboxStatus` | Verified |
| REST path: per-request `NewClient`; budget wrapper passes pair fields the key ignores; dial closures named | `pkg/gateway/rest_mail.go::mailPairClient`; `pkg/gateway/rest_mail_budget.go::mailBudgetWrap`, `::mailBudgetErr`; `pkg/gateway/rest_mail_read.go::handleMailFolders`, `::handleMailList` | Verified |
| Resolve-then-mutate split discards the epoch between sessions (lease must replace it) | `pkg/gateway/rest_mail_read.go::handleMailSeen` (ResolveRef epoch unused → MarkSeenIn separate session) | Verified |
| Reads are PEEK-only today; non-UIDPLUS draft delete leaves `\Deleted` pending (release must not CLOSE) | `pkg/email/view.go::selectFolder` (comment), `::DeleteDraftStatus` (symbol verified at its definition; deferral semantics per the ADR and the existing draft tests) | Verified (symbol + fetch discipline); CLOSE-expunge behavior reasoned from IMAP semantics — **Inferred, high confidence**, proven by MC-W1-14's server-side test |
| Tool path gates via outer wrapper + injected budget setter | `pkg/tools/email.go::gateMailDial`, per-tool `::SetMailBudget` (3 tools verified); `pkg/agent/email_tools.go::registerEmailToolsForAgent`, `::SetSharedMailBudget` | Verified |
| Watcher provider wiring sites exist as described | `pkg/gateway/gateway_boot.go` (MailboxProviderFunc → NewMailboxWatcherSet → heartbeat service), same shape in `pkg/gateway/gateway_reload.go` | Verified |
| Test harness + named regression tests exist | `pkg/email/imapserver_test.go::startMemIMAP` (seam swap + cleanup verified); `pkg/email/view_missing_folder_test.go` (3 named tests read in full); `pkg/email/mail_budget_red_test.go` + watcher RED files (test names listed via grep) | Verified |
| No GitNexus index in this worktree; impact rows are Inferred | `ls .gitnexus` → absent; per `omnipus-shared-rules` rule 9 the fallback sweep replaced graph analysis | Verified (absence) |
| Pool manager, presence registry, revision seam, new identity do not exist yet — they are this spec's deliverables | `ls pkg/email/pool.go` → absent; grep for `RevisionSource`/`PanelPresence` → absent | Verified (absence) |
| Blast radius of `dialIMAP` split and `MailBudgetRequest` extension | §2.2 table — first-hand grep of all `dialIMAP` callers (within `pkg/email` only) and all `MailBudgetRequest` literals (`mailBudgetWrap`, `gateMailDial`, `probe`) | Inferred (medium-high confidence — no graph run) |
| Ceiling/deadline/jitter numbers are founder-accepted targets, not measurements | ADR P1.1 table + recorded Q3=A (cited in the ADR's founder-decisions record); no benchmark was run by this spec | Verified (as recorded decisions); measured attainment **Unknown** |

**Correction-round evidence (2026-10-02, this task):**

| Claim | Evidence | Certainty |
|---|---|---|
| The register assigns W1 the single-publisher role for rows 9/10/10-half/11/13-half/16-half/17-half/19-half and consumption for rows 6/7/12 | `docs/internal/specs/mail-live-access-landing-order.md` §2 rows 1–24 read in full this task; §3 R-1–R-4; §4 waves; §5 deadlock ruling | Verified |
| Every grill-1 finding that names W1 is addressed; findings naming only W2/w3/w5/w6 are not applied here | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill-1.md` read in full (C-1, I-1…I-6, M-1…M-10 checked one by one); cross-checks against grill-2 F-9/Q2, grill-3 I-3/I-4/M-1/M-3, grill-4 C1/C3/I2/I6/Q-P1 via the register's per-row "Raised by" citations | Verified |
| The ADR's work-package table and correction-round ownership paragraph support every ownership statement made here | ADR-20261001 §"Affected components and parallel work packages" table (W0–W6 rows) + "Correction-round ownership" paragraph + feature-extension-owners row, read this task; P1.1/P1.2 read | Verified |
| No founder decision is reopened; the four correction-round rulings are applied | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md` read in full: Q-A (product-owned exclusion), Q-B (watcher state exclude+purge), Q-C (stale-gating stands), Q-D (search fields), Q-E (saved file = ordinary workspace file) — Q-C/D/E touch W2/w3/w5 surfaces, not W1's; Q-A/Q-B applied in §4.10/§4.12/§6.4/§11 | Verified |
| No operator-machine-provisioning dependency remains anywhere in this spec | The spec's exclusion requirement is stated and enforced purely in product terms (§4.10, §4.12, §6.4, §11): the product guarantees its own exclusions on every install and refuses when it cannot prove them; no operator-side setup artifact is named, referenced or assumed anywhere in this file (checked by reading the corrected text end-to-end this task) | Verified |
| The spec file set matches the header's mapping line | `ls docs/internal/specs/` — six `mail-live-access-w1…w6-*.md` files + the landing-order register, read this task | Verified |
| Residual ambiguous package labels eliminated | Post-edit grep for bare `W4`/`W5`/`W10` outside `ADR-W…`/file-label contexts → only mapped forms remain (re-run after final edit) | Verified |
| register R-4 re-scopes of T16/T21/CX-6 match the grill's required correction | adr-grill-1 I-4 text + register §3 R-4 ("W1's T16/T21 assert W1-observable callbacks; the cache-visible consequences are W2's V-2 and W6's T15") — both read | Verified |
| T29/T30, MC-W1-27/28, B-W1-35, CX-13, FR-W1-23…26 are new plan rows, not executed results | No test was run and no measurement was taken in this correction (spec-only dispatch); all new rows are obligations for the RED/CHECK waves | Verified (as plan rows) |

**Final-round evidence (2026-10-02, this task — Round 2 findings + the landed contracts wave):**

| Claim | Evidence | Certainty |
|---|---|---|
| R2-I-1: the nil-source rule's four sites (§4.1 rule 2, blast-radius note, FR-W1-2, MC-W1-26) now carry the same qualified condition as US-2/AS-5 and B-W1-34, with the Wave C→D window stated as a transition, not an exception | Post-edit re-read of all six sites; `grep -c "in a process where the manager exists"` → 8 consistent occurrences; the window's attribution (register rows 9/12, §4 Wave D) re-checked in `docs/internal/specs/mail-live-access-landing-order.md` §2/§4 this task | Verified |
| R2-I-2: w6-proof's frozen record shape has no established/reused field; `hit` is the *cache*-hit flag; `outcome` is `ok` or a safe class — so W1's outcome promise maps onto `outcome=pool_busy`/`ok` and adds no field | `docs/internal/specs/mail-live-access-w6-proof-spec.md` MC-P1 (full field list), MC-P3 (`hit=true, socket_count=0` cache-hit vs `hit=false, source=live, socket_count≥1` live), §6.1 field table (`hit`, `socket_count`, `outcome`, `shared_flight` rows) — read this task | Verified |
| R2-M-1: §4.10→§4.11→§4.12 now appear in numeric order | `grep -n "^### 4\.1[0-2]"` → 276 / 287 / 301; `git diff --stat` after the move = 9 insertions / 9 deletions (pure repositioning, no content lost) | Verified |
| R2-M-2: the never-*assign* invariant worded correctly at both sites | `grep "never assigns or reads"` → 0 matches post-edit (was 2: MC-W1-24 and the §8 harness rule); §8 T23 and DoD 5 already stated it correctly and are unchanged | Verified |
| R2-M-3: US-6/AS-4 and SC-W1-5 re-scoped to W1's seam; the end-to-end oracle named as W2's V-2 + w6-proof's T15 | Post-edit re-read of both rows; MC-W1-17/B-W1-22/T16 confirmed already seam-scoped (correction round) | Verified |
| R2-M-4: SC-W1-8 now names both measurements, the receipt contents, and the failing reviewer | Post-edit re-read of SC-W1-8; no number is invented — the receipt must cite the go-imap/v2 buffer size from library source | Verified |
| The W0 contracts wave's shapes are landed and match every shape this spec names | First-hand this task: `contracts/components/schemas/MailUnavailableError.yaml::reason` (enum exactly `pool_busy/account_busy/server_connection_limit/backoff`), `MailReadMetadata.yaml::publication_revision`, `MailMessagePage.yaml`, `MailStaleReferenceError.yaml` present; `grep -c observer_id contracts/openapi.yaml` → 2 (folders + messages reads); `MailPanelObserverFrame`/`MailPanelObserverAckFrame`/`MailPanelObserverErrorFrame` in `contracts/asyncapi.yaml`; `git log --oneline -1 5f23ae8a0` → the W0 wave commit on this branch. Independent confirmation: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/contracts-wave-check.md` (verify-contracts exit 0, no widened enum) | Verified |
| No statement that a W0 shape is still pending remains in this spec | `grep -n "is published by the W0\|publishes the frames\|wave schemas it\|the W0 contracts wave publishes\|is still missing"` → only line 532's landed-attribution phrasing matches the loose pattern (correct as written); all other W0 mentions are ownership/attribution statements | Verified |
| The one Critical in the Round-2 batch (w2's CRIT-1, cursor/search scoping) is not this file's finding | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill2-w1.md` read in full: verdict for w1 is PASS WITH FINDINGS — two Important, four Minor, no Critical; "Questions for the founder: None required" | Verified |
| No founder decision reopened; every §14 question decided or open with a named owner | §14 re-read post-edit: OQ-1 decided (register row 12), OQ-2 owner architect, OQ-3 owner architect (now narrowed to capacity depth only — the wire value landed), OQ-4 owner W1 implementer | Verified |

**Self-check (correction round):** re-read the corrected spec end-to-end against the dispatch's six numbered requirements — (1) every interface W1 consumes names the register's single publisher (§3.2 table + negative-consumption paragraph), and no text claims W1 or a nonexistent W0 spec publishes a shared shape: the header now states W0 is a backend-lead role, not a spec (grill-1 I-2); W1's four publisher freezes carry explicit single-publisher statements (§3.1, register rows 9/10/11); the shared items state publisher + consumption contract with no stub and no second implementation (§3.2). (2) Every grill-1 Critical/Important naming W1 is applied in text with its required test added: C-1 → §4.12 + MC-W1-27 + B-W1-35 + T29 + CX-13 + both refusal lines corrected; I-1 → header mapping line; I-2 → header + §1 + §3.3; I-3 → §3.1/§4.9 single-publisher statements; I-4 → MC-W1-17/23, B-W1-22/30, T16/T21 re-scoped per register R-4; I-5/I-6 name only W2 — not applied here, recorded in the delivery report. Minors naming W1 applied: M-1 → §3.1 dirty-mark row; M-2 → §3.2/§14 OQ-1 decided; M-6 → US-2/AS-5 and US-4/AS-6 added, B-W1-33/34 re-anchored, matrix fixed; M-9 → MC-W1-24 + DoD 5 seam rule. A finding believed misdirected is answered in place, none silently dropped. (3) Founder rulings applied: Q-A/Q-B restated purely in product terms (§4.10 item 4, §4.12 item 3, §6.4, §11) — no operator-side provisioning artifact is named, referenced or assumed anywhere in the file; Q-C/D/E touch no W1 normative text. (4) §3/§14/§16 updated to match; OQ-1 marked decided with the register as authority, OQ-2/3/4 open with named owners. (5) Commit discipline: only this file staged per commit, authored as the human, no co-author trailer, committed per section group. **Forbidden moves not committed: no production code, no contract-file edit, no test executed, no push, no weakened test (T16/T21 are re-scoped to stronger, correctly-owned oracles — nothing deleted), no widened limit (every founder number 2/8/5 s/45 s/30 s/2 min/25-50 stands untouched), no stub type (the negative-consumption paragraph states what W1 does not build instead), no silent scope change (§1 scope is unchanged; additions are register-mandated rows).**

**Self-check (final round, 2026-10-02):** re-read the spec end-to-end after the last edit against the final-round dispatch's six ordered instructions — (1) Critical findings: none name this file (the Round-2 batch's one Critical, w2's CRIT-1, is `mail-live-access-w2-discovery-and-cache-spec.md`'s; w1's Round-2 verdict is PASS WITH FINDINGS — checked in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/adr-grill2-w1.md` this task). (2) Importants: R2-I-1 → the qualified nil-source rule (typed error in a process where the manager exists) applied at §4.1 rule 2, the blast-radius note (with the rule-of-precedence statement and its why), FR-W1-2, MC-W1-26, T25 and CX-12; the "MC-1" citation slip fixed to MC-W1-26; R2-I-2 → the pool outcome mapped into w6-proof §6.1's frozen `outcome` domain (`pool_busy` as a safe class, `ok` otherwise) at §3.1, MC-W1-28, T30, FR-W1-25, with no established/reused field added (one shape, one publisher — w6's `hit` verified to be the cache-hit flag before choosing the mapping). (3) Minors: R2-M-1 → §4.11/§4.12 blocks reordered (numbers stay with content; every cross-reference re-checked valid); R2-M-2 → never-*assign* wording at MC-W1-24 and the §8 harness rule (the second site is the same defect found and fixed in the same pass, reported here); R2-M-3 → US-6/AS-4 and SC-W1-5 aligned to the seam scope; R2-M-4 → SC-W1-8 given both measurements, receipt contents and the failing reviewer. (4) Contract-check findings naming this spec: none in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/contracts-wave-check.md` (F1–F5 name the contracts files and the gateway/library consumers); instruction-5 shape verification applied instead — every wire shape this spec names is now cited to its landed schema (`MailUnavailableError.reason`, `MailPanelObserver*` frames, `observer_id` param, `MailReadMetadata.publication_revision`, `MailMessagePage`/`MailStaleReferenceError`), each verified present in this worktree before citing, and the landed wave is attributed to commit `5f23ae8a0` with its unmerged branch state stated. (5) §3 tables, §8 TDD plan (T25/T30), §10 (FR-W1-2/25, SC-W1-5/8), §14 (OQ-3 narrowed to its genuinely open half; OQ-1 decided, OQ-2/4 open with owners), §16 evidence table and both self-checks updated to match the corrected text. (6) Commit discipline: only this file staged per commit, authored and committed solely as Daniel Piatkowski's required no-reply identity, no `Co-Authored-By` trailer (verified per commit), committed after each corrected section group. **Forbidden moves not committed in this round: no production code, no contract-file edit (the landed shapes are cited, never edited), no test executed, no push, no weakened test (T25/T30 gained precision, lost nothing), no widened limit (2/8 sockets, 5 s, 45 s, 30 s, 2 min all stand), no stub type (R2-I-2 resolved by shrinking W1's promise to the publisher's frozen shape, not by inventing a field), no silent scope change (§1 scope untouched; every edit traces to a named Round-2 finding or the landed-contracts instruction).**

skills: omnipus-shared-rules, plan-spec, grill-spec
