# Implementation Specification: Mail live access — W5 integration, privacy and the enabling deployment changes

**Created**: 2026-10-02
**Status**: Draft — awaiting grill (exactly one grill round, then one founder interview, then one correction)
**Design authority**: ADR-20261001 — *Mail live access: pooled connections, folder discovery and a bounded cache* (`docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`), including its applied correction round (grill findings I-01–I-06, M-01–M-02; founder answers Q1=A, Q2=B, Q3=A, Q4=A, Q5=A)
**Review input**: `adr-grill-report.md` (PASS WITH FINDINGS — 0 Critical, 6 Important, 2 Minor) and the auto-commit trace receipt (`receipts/autocommit-trace.md`), both read in full for this spec
**Package**: this spec covers the ADR's **W4 — integration/privacy** work package (one backend-lead owner); the spec file is numbered W5 in the implementation-spec sequence. Where this document says "this package" it means that owner.
**Sibling specs**: W0 (contracts), W1 (read runtime: pool/lease/budget/watcher), W2 (discovery, folder-file encryption, memory caches), W3 (panel/settings SPA) — authored in parallel; none may edit another's files. The Publishes/Consumes section (§7) is the parallel-work contract.
**Certainty labels**: **Verified** = read in this checkout (`wt-adr-mail` @ `fc4c8bf6dcdfa257e3540f602f042dfa19882ce1`, branch `docs/adr-mail-live-access`) in this task; **Inferred** = reasoned from verified evidence, not executed; **Unknown** = genuine gap, named as one. GitNexus MCP tools were not exposed in this session; impact assessment is grep-based and labelled Inferred accordingly. No test, build or benchmark was run.

---

## 1. Summary and scope

**Bottom line.** This package is the integration spine of the mail live-access design: it makes **one** connection pool, **one** operation budget and **one** bounded cache serve the Mail panel, the background watcher and the agent tools — so no code path can quietly build a second pool and break the founder's 2-per-mailbox / 8-global socket ceilings. It owns the privacy envelope around that shared cache: where the encrypted files live on disk, what must exclude them from the machine's Git auto-backup and the app's tar backup before the first byte is written (a **hard gate** — disk cache stays blocked until the deployment change is proven in place), and every removal path (mailbox, agent, workspace, credential change, key rotation) with a cleanup result that tells the truth when a file delete fails. It also converts today's 15-minute HTML preview tokens — which currently hold whole message bodies in memory — into metadata-only grants whose every serve request fetches and sanitizes live through the same shared pool, and it closes the logging holes that let raw server text reach state files and logs.

**In scope (this package's exclusive production files):** the gateway Mail read/budget/summary/settings/removal/preview files under `pkg/gateway/` (the routes, token store, isolation and inline-serving seams), the boot/reload/REST/socket wiring that injects the shared runtime, `pkg/agent/email_tools.go` and `pkg/config/config.go` (tool-side injection), plus the deployment-side change outside this repo (the data-directory Git-ignore rule) that unblocks disk cache writes.

**Out of scope (owned elsewhere, consumed through frozen interfaces — §7):** the pool/lease manager implementation (W1), folder discovery and the encrypted-file envelope (W2), all SPA work (W3), every contract/generated edit (W0), the attachment Transfer service and Library storage (W7), the agent attachment tools and policy catalog (W10), all tests (W5-test/qa-lead — this spec only names them), and the Phase 2 JMAP transport (W6).

| Deliverable | One-line outcome |
|---|---|
| Shared runtime wiring | Panel, watcher and tools resolve one pool/budget/cache instance; a second acquire of the same account slot is structurally impossible |
| Presence lifecycle | Mail panel observer identity rides the authenticated WebSocket; teardown on close frame, socket death, logout and workspace exit |
| Cache placement + gate | `mail-cache/` under the data dir, 0700/0600, agent-unreachable, and refuse-to-write unless exclusion from Git staging is proven |
| Backup/restore exclusions | Tar backup and restore never carry or resurrect cache files |
| Removal cascades | Five triggers purge leases, presence and cache with truthful `removed` / `removed_cleanup_pending` outcomes and late-completion protection |
| Metadata-only preview grants | No HTML/body/inline bytes in any token store; every preview fetch is a live, budgeted, sanitized request; existing token security controls preserved |
| Redaction | Pool/cache/probe diagnostics carry opaque identifiers, safe classes and counts — never subjects, addresses, folder names or raw upstream text |

---

## 2. Existing Codebase Context

### 2.1 Symbols involved

| Symbol | Role | Certainty |
|---|---|---|
| `pkg/gateway/rest.go::restAPI` (`homePath`, `mailBudget`, `mailBudgetOnce`) | The REST shell struct; `homePath` is `~/.omnipus` (the data-dir root). The shared budget is injected here today; the pool/cache manager fields land beside it | Verified |
| `pkg/gateway/rest_mail.go::mailPairClient` | Resolves one (agent, workspace) pair to an authenticated client **per request** — `email.NewClient` on every call. This is the panel's client construction site and the place a private pool could form | Verified |
| `pkg/gateway/rest_mail.go::mailErr502` | Maps upstream failures to the 502 class envelope; passes the **raw error** into `logsafeError` — a named redaction seam | Verified |
| `pkg/gateway/rest_mail_budget.go::mailBudgetFor`, `mailBudgetWrap`, `mailBudgetErr`, `mailRetryParam` | The A8 budget at the REST surface: lazy process-wide resolution (`email.SharedMailBudget(a.homePath)`), typed 503 backoff/busy mapping, human-Retry param. `mailBudgetWrap` passes `AgentID`/`WorkspaceID` into the request, which the flight key ignores (grill I-01) | Verified |
| `pkg/gateway/rest_mail_read.go::handleMailFolders`, `handleMailList`, `handleMailFolderMessage`, `handleMailAttachment`, `mailPartByStableIndex` | The four budget-wrapped panel GET routes + the attachment byte route. `handleMailAttachment` fetches the **whole message** (`ReadView`), selects the part, returns 413 for `DataUnavailable`, serves with attachment disposition — today the single shared preview/download byte path (grill I-05) | Verified |
| `pkg/gateway/rest_mail_read.go::handleMailSeen` | Resolve-then-mutate split: discards `ResolveRef`'s epoch (`_`), then `MarkSeenIn` opens its own session — the current-code hazard the grill flagged; this package's routes must adopt the same-lease validation W1/W2 provide | Verified |
| `pkg/gateway/rest_mail_summary.go::handleMailSummary` | Saved-state summary; serves `LastErrorClass` only — **never** `LastErrorText` (redaction change has no wire impact) | Verified |
| `pkg/gateway/mail_preview_token.go::mailPreviewGrant`, `MailPreviewTokenTTL`, `MailPreviewMaxLiveTokensPerSession`, `mint`, `lookup`, `invalidateSession`, `purgeLocked` | The preview token store. **The grant carries the already-fetched payload today**: `HTML string`, `Inline []mailPreviewInline{ContentType, Data []byte}`, `RemoteURLs []string`, TTL 15 minutes, cap 8 per session (refuses, never evicts), logout revocation, unknown/expired/revoked all 404, signature-kind tokens replace-on-mint with a 2-minute TTL | Verified |
| `pkg/gateway/rest_mail_preview.go::handleMint`, `mailSanitizePreviewHTML`, `mailExtractRemoteImageURLs`, `fetchRemoteImage` | The mint: session-authenticated (`PreviewSessionKey`), fetches the whole view — **outside the A8 budget** (a POST on `/mail-preview/`, not one of the four wrapped GETs) — sanitizes, stores HTML + inline bytes + remote-URL list in the grant. The URL list is metadata and may stay in a metadata-only grant; the bytes may not | Verified |
| `pkg/gateway/mail_isolation_policy.go::mailIsolationPolicy` | The Mail preview CSP (`script-src 'none'`, `style-src 'unsafe-inline'`, restricted sources) — preserved unchanged by this package | Verified |
| `pkg/gateway/inline_serving.go::applyMailByteHeaders`, `mailContentDisposition` | The one disposition-policy write site in the package (FR-008c source gate): attachment disposition, nosniff, no CSP. The new preview-purpose byte endpoint must not reuse the attachment disposition | Verified |
| `pkg/gateway/rest_mailbox.go::restAPISetAgentMailbox.persistConfig` | The mailbox save path. Already preserves omitted fields, trims nonempty overrides, deletes explicitly empty ones (the E-Overrides correction). Captures `wasEnabled` before overwrite — the disabled→enabled transition signal already exists; **enabled→disabled** is the hook for the disable cascade | Verified |
| `pkg/gateway/rest_mailbox.go::deleteAgentMailbox` | Removes the config entry (`safeUpdateConfigJSON`), deletes per-pair + legacy credentials (`removeStoredCredential`, `logsafeError` + continue), waits out the reload (`triggerReloadAndWaitOutcome`), then returns unconditional `gen.OperationResult{Success: true}` — **no filesystem cleanup, no pool/cache eviction, no generation bump, and success even when cleanup failed** | Verified |
| `pkg/gateway/rest_agents.go::deleteAgent` | Agent deletion: `agentstore.New(a.homePath).DeleteState(id, revision)` (entity + SOUL records only), then reload. No mail-cache cleanup | Verified |
| `pkg/gateway/rest_workspaces.go::restAPIHandleWorkspaceDelete.releaseRuntimeResources` → `removeMailboxesForWorkspace` | The workspace cascade's mail step: best-effort config+credential removal per bound pair. The wholesale `removeAllFn(wsDir)` wipe does **not** touch `mail-cache/` (it lives outside `workspaces/<id>/`) | Verified |
| `pkg/gateway/rest_settings.go::createTarGz`, `extractTarGz`, `HandleCreateBackup`, `HandleRestore`, `maxRestoreFileSize` | The app tar backup: skips only top-level `logs` and `backups`; restore skips only `config.json`. A new `mail-cache/` directory is archived **and re-materialized on restore** today | Verified |
| `pkg/gateway/websocket.go::WSHandler.ServeHTTP`, `authenticateWS`, `readLoop`, `wsHandlerReadLoop.dispatchFrame`, `wsFrameSchemaName`, `pingPump`, `writePump`, `unbindConnHubLocked` | The gateway WebSocket lifecycle — fully traced in §2.3. This is the presence substrate the ADR left to this package | Verified |
| `pkg/gateway/gateway.go::initializeAgentLoop` | Boot: `agent.SetSharedMailBudget(email.SharedMailBudget(rc.homePath))` **before** `NewAgentLoop` — the ordering rule ("must run before NewAgentLoop") the pool/cache injection must follow | Verified |
| `pkg/gateway/gateway_boot.go` (watcher construction site) | `heartbeat.NewMailWatchService(email.NewMailboxWatcherSet(provider, stg.homePath, email.SharedMailBudget(stg.homePath)), 0)` — the watcher resolves the same state-dir-keyed budget instance | Verified |
| `pkg/gateway/rest_auth.go::triggerReloadAndWait`, `triggerReloadAndWaitOutcome` | The config-reload seam: mailbox save/delete and agent delete wait out the async reload that re-runs `registerSharedTools` | Verified |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent`, `SetSharedMailBudget`, `sharedMailBudget` | Tool-side wiring: per-pair `email.NewClient` construction at registration (called from `pkg/agent/loop_wire.go` on construction **and** every reload), budget injection via the optional `SetMailBudget` setter pattern, `RegisterReplacing` on reload | Verified |
| `pkg/email/mail_budget.go::MailBudget`, `SharedMailBudget`, `MailBudgetRequest.flightKey`, `call`, `flightContext`, `TryCall`, `runDialValue` | The existing shared gate: process-wide instance keyed by state dir; flight key = `account + "\x00" + operation + "\x00" + json(params)` — **no pair, no generation** (I-01); shared flights detached from the first caller (`context.WithoutCancel`) — the I-02 ordering hole's substrate; watcher non-blocking entry | Verified |
| `pkg/email/watcher.go::Watcher.statePath` (`<StateDir>/email-watch/<pair>.json`), `recordFailure(errClass, errText)`, `keyFor` | Watcher state is written to an **un-ignored, never-deleted** data-dir path (the orphan precedent); `recordFailure` persists **raw `errText`** into the state file — the named redaction seam (owned by W1, required by this package); `keyFor` is the lossy lowercase-alnum sanitizer the cache path must **not** reuse for pair identity | Verified |
| `pkg/email/view.go::Client.ReadView`, `ResolveRef`, `MarkSeenIn`, `ReadFolderPage`, `folderNameFor`, `maxViewPartBytes` (25<<20), `SanitizeAttachmentName` | The read paths this package's routes call; the 25 MiB decoded cap; the name sanitizer F2 reuses | Verified |
| `pkg/config/config.go::MailboxConfig` | Pair configuration: `Enabled`, `WorkspaceID`, IMAP/SMTP host/port, `Username`, `PasswordRef`, `SignatureHTML`, `SentFolderName`, `DraftsFolderName` — the canonical identity fields the cache generation fingerprint binds | Verified |
| `pkg/credentials/store.go::Store.DeriveSubkey`, `Store.Close` | The sanctioned purpose-keyed derivation seam (32-byte keys, `ErrStoreLocked` when locked, no master-key export); `Close` cannot guarantee erasure of handed-out copies — the honesty constraint for rotation cleanup | Verified |
| `pkg/fileutil/file.go::WriteFileAtomic` | The existing atomic-write primitive (used by watcher state) — the ciphertext-only replacement path for cache files | Verified |
| `pkg/security/ssrf.go::SSRFChecker.SafeDialContext`, `SafeTransport` | Dial-time restricted-IP resolution — consumed by W1/W6 transports, not re-implemented here; named because preview/JMAP URL handling must keep using it | Verified |
| `contracts/openapi.yaml::getMailAttachment` (operationId), `contracts/asyncapi.yaml::channels.chat`, `WsFrameType` enum | The contract anchors: the single attachment byte operation today, and the type-discriminated WS protocol the presence frames extend | Verified |

### 2.2 The wiring inventory — every site where a second pool could form

The A8 budget is shared today because every construction site resolves the same state-dir-keyed instance (`SharedMailBudget`). The pool and cache must follow the identical pattern, and this spec pins every construction site so parallel writers cannot fork it:

| # | Wiring site | Today | Required state after this package |
|---|---|---|---|
| 1 | `pkg/gateway/gateway.go::initializeAgentLoop` | `agent.SetSharedMailBudget(email.SharedMailBudget(rc.homePath))` before `NewAgentLoop` | The pool manager + cache manager + presence registry are constructed once here (or resolved from the same state-dir accessors) and injected **before** `NewAgentLoop`, same ordering rule |
| 2 | `pkg/gateway/rest.go::restAPI` fields | `mailBudget` + `mailBudgetOnce` | `mailPool` / `mailCacheManager` / `mailPresence` fields beside them, with `mailBudgetFor`-style lazy accessors that resolve the **same** process-wide instances |
| 3 | `pkg/gateway/gateway_boot.go` (watcher site) | `NewMailboxWatcherSet(provider, stg.homePath, SharedMailBudget(stg.homePath))` | The watcher set receives the same pool/cache handles (constructor extended by W1) — never a second `New*` call with fresh state |
| 4 | `pkg/agent/email_tools.go::registerEmailToolsForAgent` | Per-pair `email.NewClient` at registration; budget via optional setter | The client facade is constructed with the injected pool/cache handles (W1's constructor change); the setter pattern extends to the new handles. Re-runs on every reload replace, never stack |
| 5 | `pkg/gateway/rest_mail.go::mailPairClient` | Fresh `email.NewClient` per request | The facade construction passes the shared handles; REST never constructs an unmanaged client. This is the highest-risk fork site: it runs on every request |
| 6 | `pkg/email/mail_budget.go::SharedMailBudget` | Process-wide map keyed by state dir | The pool/cache accessors mirror this exact mechanism (same key, same lazy-create-once semantics) |

**Ownership rules that stop a second acquire of the same account slot** (ADR P1.2, binding on W1's implementation and this package's wiring):

1. **One budget owner per operation.** The budget slot is acquired exactly once per operation: either by the outer wrapper that exists today (REST `mailBudgetWrap`, the tools' `SetMailBudget` gate, the watcher's `TryCall`) or by the pooled client facade internally — never both. When the facade performs acquisition internally (the target state), the outer wrappers keep only their backoff-check and coalescing roles and must not take a second slot.
2. **Contention and ownership are different keys.** `host:port|username` (transport `AccountKey`) remains the account-contention key only. The agent/workspace pair, endpoint/TLS identity and non-secret config generation decide socket ownership, cache isolation and read-flight sharing. The two pairs on one account case (grill I-01) is closed by W1's coalescing-identity rewrite; this package's wiring must never let a handler resolve a client without the pair identity attached.
3. **Counting reservations.** Connecting reservations count against both the 2-per-mailbox and 8-global ceilings from reservation time, not dial success (W1 enforces; this package's wiring must not create a path that bypasses the manager).
4. **No pool construction below the facade.** A REST-created or tool-created client must not build a private pool (the ADR's "a REST-created `Client` must not construct a private pool"). The gate test in §6 pins this at site 5.

### 2.3 Presence and lifecycle trace — the hooks the design left to this package

The ADR (P1.2, panel-presence row) requires this package to trace the gateway's actual socket lifecycle before parallel implementation starts. Traced; the hooks exist and are named:

| Lifecycle stage | Named seam | What it does today | What this package adds |
|---|---|---|---|
| **Authorization** | `pkg/gateway/websocket.go::WSHandler.authenticateWS` | Session cookie first (`middleware.ResolveUserFromCookie`), else the first-frame `generated.AuthFrame` bearer token (`resolveBearerIdentity`); sets `wc.userID`; refusal paths are friendly-constant-closed (`wsAuthErr*`) | Nothing to the auth itself. The mail observer registry accepts observers **only** for connections that passed this gate, and binds each observer to the connection object (`wc`), never to a caller-supplied session string |
| **Connection registration** | `ServeHTTP`: `chatID := "webchat:" + uuid`; `h.sessions[chatID] = wc`; event subscription (`SubscribeEvents`) | Every connection gets a fresh server-generated chat id; per-connection state lives in handler maps | The observer registry is keyed the same way (per-connection), so multiple tabs are distinct observers by construction |
| **Inbound frame handling** | `wsHandlerReadLoop.dispatchFrame` — type switch on `generated.WsFrameType*`; per-frame JSON-Schema validation via `wsFrameSchemaName` + `ValidateInboundFrameJSON`, gated by `gateway.validate_inbound`; oversized frames killed by `SetReadLimit` | Each frame type has: an asyncapi schema, a `WsFrameType` enum entry, a `dispatchFrame` case, and a schema-name mapping | The `mail_panel_observer` frame (open/close, opaque `observer_id`, `workspace_id`) gets exactly those four things, in the contract-first order of §8. Unknown frame types already fall through to a visible error frame — no new failure mode |
| **Liveness** | `pingPump` (`wsPingPeriod` = 30 s) + pong handler and `readLoop` both re-arming `wsPongWait` = 60 s read deadline | A dead client (crash, network loss) stops answering pings; the read deadline expires; `readLoop` exits | **No new mail-data polling or heartbeat.** The ADR is explicit: existing socket liveness reaps lost connections; the registry just needs the expiry callback (next row) |
| **Teardown** | The deferred block in `ServeHTTP`: `UnsubscribeEvents`, task-map cleanup, `delete(h.sessions, chatID)` / `delete(h.sessionIDs, chatID)`, `unbindConnHubLocked(wc)`, `wc.close()` — runs on **every** exit path: explicit close, read error, deadline expiry, abnormal closure | This is the single choke point every disconnected socket passes through | **The presence-revocation hook lands here**: one call (e.g. `a.mailPresence.DropConnection(wc)`) that cancels that connection's observers, closes idle panel-owned sockets, and lets shared flights finish only for remaining real owners. Late additions to the deferred block must keep its ordering: revocation before `wc.close()` |
| **Rest-event teardown (same seam)** | Logout (session invalidation closes sockets), workspace exit (SPA sends close), browser quit (TCP death → ping failure) | All converge on the same deferred block | No extra handling needed — which is the point of binding observers to the connection rather than to UI promises |

Conservative default (ADR, binding): until a mailbox request's observer association is **acknowledged**, requests execute live request-scoped work — they may not start detached panel refreshes or retain sockets. Absence of presence must degrade to today's behaviour, never to an error.

### 2.4 Impact assessment (Inferred — no GitNexus in this session; grep-based)

| Symbol this package modifies | Risk | d=1 (will break, must update) | d=2 (should test) |
|---|---|---|---|
| `rest_mailbox.go::deleteAgentMailbox` | MEDIUM | SPA mailbox-removal flow (W3 consumes the changed result shape); W2's cache manager (new purge call) | E2E mailbox removal; the malformed-entry 500 path |
| `rest_settings.go::createTarGz` / `extractTarGz` | LOW | Backup/restore round-trip tests | `no_local_mail_store_test.go` (must keep passing) |
| `rest_mail_preview.go::handleMint` + `mail_preview_token.go` grant shape | HIGH | `mail_preview_red_test.go`, `mail_preview_no_redirect_test.go`, `mail_preview_serve_limit_test.go`, `mail_preview_logout_red_test.go`, `mail_preview_part_unavailable_red_test.go` (all pin today's payload-carrying behaviour and must be re-derived from this spec, not weakened); W3's preview consumer | `mail_preview_ssrf_gap_test.go`; the signature-preview path (unchanged) |
| `rest_mail_read.go::handleMailAttachment` | HIGH | `fetchMailAttachment` consumer (W3); the 413 contract tests | Draft attachment paths sharing `mailPartByStableIndex` |
| `rest.go::restAPI` (new fields) | LOW | Every `restAPI` literal constructor in tests | — |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent` | MEDIUM | `pkg/agent/email_tools_test.go`; `pkg/tools/email_no_retry_param_test.go` (must keep passing) | Reload paths (`loop_wire.go` callers) |
| `pkg/gateway/websocket.go::ServeHTTP` deferred block + `dispatchFrame` | MEDIUM | `websocket_*` test families (teardown order, frame validation) | `ws_hub_*` invariants |
| `pkg/config/config.go` (pair identity/generation support) | LOW | Config load/save round-trips | `mailbox_policy_fill_red_test.go` |

No HIGH-or-CRITICAL risk *widenings* are introduced by wiring itself; the two HIGH rows are re-derivations of existing pinned behaviour, which is why §6 requires their tests re-derived from this spec with red-before-green receipts.

### 2.5 Cluster placement

This package sits in the **gateway / HTTP shell** cluster and the **agent runtime** cluster's tool-wiring edge. It owns no transport or discovery logic (W1/W2 clusters) and no UI (W3). It is the only package allowed to edit the gateway Mail routes during this feature wave, per the ADR's one-writer-per-file rule.

---

## 3. User stories and acceptance criteria

### US-1 — One shared pool, budget and cache instance for panel, watcher and tools (P0)

The founder set hard ceilings — two connections per mailbox, eight open globally — that only hold if every mail path draws from the same pool. Today the budget is already shared (every site resolves `email.SharedMailBudget`), but each path builds its own connectionless client, and the pooling design introduces real state (leases, caches) that would silently fork if any construction site built a second manager. This story pins the wiring so that no code path can exist that doesn't share.

**Why this priority**: every other story (presence, cache, preview, removal) calls through this shared runtime; if the wiring forks, the ceilings and the cache bounds are unenforceable.

**Independent test**: with a fake IMAP server counting accepted sockets, drive a panel request, an agent-tool request and a watcher cycle against the same pair concurrently; assert all three resolved the same manager instance (pointer identity at the wiring sites) and the server never saw more than the configured caps.

**Acceptance scenarios**:

1. **Given** the gateway booted normally, **When** the panel, a tool and the watcher each resolve their mail runtime handles, **Then** all three hold the same pool, budget and cache instances (one process-wide set, keyed by the data dir).
2. **Given** a pair whose account is also configured on a second (agent, workspace) pair, **When** both pairs run identical reads concurrently, **Then** the two results never share flight data with each other (separate coalescing identities per grill I-01) while their combined concurrent dials still respect the shared 2-slot account gate.
3. **Given** a request that arrives while all eight global sockets are active, **When** it waits past the founder-accepted 5-second acquisition window, **Then** it receives the typed busy refusal (503 with `reason=pool_busy`) and no ninth socket is ever dialed.
4. **Given** any code path that constructs a mail client (REST route, tool registration, watcher cycle), **When** it builds its client, **Then** the client carries the injected shared handles — a construction without the injected handles fails loudly in tests rather than silently dialing unmanaged.

### US-2 — Mail panel presence on the authenticated WebSocket (P0)

Panel-owned connections may be retained only while at least one authenticated Mail panel observer is open (founder close rule). The ADR proposed presence frames over the gateway WebSocket and left the lifecycle integration to this package; §2.3 traced the hooks. This story defines the observable behaviour of open, close and death.

**Why this priority**: without presence, the pool cannot know when to release panel sockets — either it leaks them after the panel closes or it closes them under an active user. Both violate founder-set rules.

**Independent test**: open a real authenticated WebSocket, send the observer-open frame, verify a panel-scoped request retains its socket past request end; send close (then separately: kill the socket abruptly), verify the retained socket closes and a subsequent request is request-scoped.

**Acceptance scenarios**:

1. **Given** an authenticated WebSocket connection, **When** the SPA sends `mail_panel_observer` open with a fresh opaque observer id and its workspace, **Then** the server acknowledges binding that observer to *this connection* (not to any caller-supplied session string) and panel requests presenting that observer id may retain sockets while it lives.
2. **Given** a live observer, **When** the same connection sends close for that observer id, **Then** only that observer is dropped; another tab's observer on a different connection is unaffected.
3. **Given** a live observer and its connection dies abruptly (no close frame — read deadline expires), **When** teardown runs, **Then** the observer is revoked by the teardown hook and idle panel-owned sockets close; a shared read in flight finishes only for its remaining real owners.
4. **Given** a request whose observer id is unknown, stale (its connection died), or owned by a different connection, **When** the request executes, **Then** it performs live request-scoped work (or serves from cache per its mode) — it never retains a socket and never fails *because* presence is missing.
5. **Given** a mailbox request on a connection, **When** the pair is authorized (existing `mailPairClient` checks), **Then** authorization remains independent of presence: presence only ever *extends* what may be retained, never *grants* access to mail data.

### US-3 — Encrypted cache location, permissions and the write gate (P0)

The encrypted folder-metadata file (Phase 1) and later the header snapshot (Phase 2) live under the data directory. Folder names can reveal sensitive projects and people, so the ADR requires encryption, restrictive permissions, and exclusion from every capture path *before the first write*. This story defines the on-disk contract and the product-side gate that refuses to write when exclusion cannot be confirmed.

**Why this priority**: the first cache write on an unexcluded install commits mail-derived metadata to an off-machine Git backup within 15 minutes (the trace's verified mechanism), and Git history retains it regardless of later cleanup.

**Independent test**: with the exclusion rule present in a temp data dir, write a cache file and assert its path, permissions and ciphertext-only content; remove the rule and assert the write is refused with a visible cache-unavailable outcome and no file created.

**Acceptance scenarios**:

1. **Given** a configured data directory, **When** the cache writes a pair's folder metadata, **Then** the file lands at `<data-dir>/mail-cache/<opaque-pair-id>/folders.enc` (path derived from the configured data root, never hard-coded), inside a `0700` directory as a `0600` file, replaced atomically as complete ciphertext via the existing atomic-write primitive.
2. **Given** two distinct (agent, workspace) pairs, **When** both write cache files, **Then** each pair's path is unambiguous and stable across restarts — the identifier is an opaque stable derivation of the pair identity, never the watcher's lossy filename sanitizer (`keyFor`) and never an email address or folder name.
3. **Given** an install whose data-directory `.gitignore` (or `.git/info/exclude`) does not match the cache directory, **When** the first cache write is attempted, **Then** the write is refused before any file is created, the pair runs live-only, and the surfaced cache state carries the safe `cache_unavailable` notice — never a silent skip and never a plaintext fallback.
4. **Given** a cache path where any component is (or becomes) a symlink, **When** the manager resolves or writes, **Then** it refuses the operation rather than following the link out of the cache directory.
5. **Given** the agent filesystem/shell policy surface, **When** a file tool resolves any path under the cache directory, **Then** it is refused — a generic file tool must never become a decryption route.
6. **Given** Windows, **When** the cache directory is created, **Then** access is restricted to the current user by the platform's own mechanism (ACL), not by a chmod call that has no effect there.

### US-4 — The data-folder exclusion deployment change (P0 — blocks US-3's disk writes)

The auto-commit job is machine-level deployment, not product code: a launchd agent runs a generated bash script that stages with bare `git add -A` in the data directory, gated by a gitleaks pre-commit, and pushes to an off-machine backup remote. Exclusion is decided **solely** by the deny-list `.gitignore` that an external provisioning script writes; the product writes no ignore file today (all verified in the trace receipt). The tar backup has the same hole with a different seam. This story specifies the exact deployment change and the evidence that proves it, and makes disk-cache activation **blocked** until that evidence exists.

**Why this priority**: the ignore rule must land before the first cache write ships *on any install* — ignoring a file after it is tracked removes nothing, and the backup remote's history keeps every prior blob.

**Independent test**: on a machine with the deployed job's staging rules, create a synthetic `mail-cache/` file plus one ordinary allowed state file, run the job's staging (or its exact `git add -A` semantics), and assert the cache file is untracked while the control file is staged; separately list the members of a fresh tar backup and assert the same split.

**Acceptance scenarios**:

1. **Given** the provisioning script's ignore-file template, **When** the deployment change is applied, **Then** the template carries a `mail-cache/` deny rule (adjusted to the final directory name) and the script's idempotent refresh has been re-run on this machine, so the live `<data-dir>/.gitignore` matches the rule.
2. **Given** the tar backup path, **When** `createTarGz` walks the data directory, **Then** top-level `mail-cache` is skipped exactly as `logs` and `backups` are — and the same skip makes fresh archives safe to restore, since restore only materializes what archives contain.
3. **Given** an install where a cache file has already been committed (tracked), **When** the ignore rule lands, **Then** a documented remediation runbook exists (`git rm -r --cached mail-cache/` + commit per install), and the runbook names the history question (whether the backup remote's history is rewritten) as an explicit founder decision — never silently skipped.
4. **Given** the whole gate, **When** the product decides whether disk cache writes are enabled, **Then** the gate is the product-side check of US-3 scenario 3 **plus** the deployment evidence below — deployment change without the product gate, or the gate without the deployment change, does not unblock.
5. **Given** the trace's live-state caveats (job currently unloaded; last run blocked by 30 gitleaks findings; 22,239 dirty entries pending), **When** this feature claims the exclusion is real, **Then** the claim cites fresh evidence gathered after the job is reloaded and unblocked — never the trace's snapshot alone.

**Evidence that proves the exclusion is real** (all five required; this is the US-3/US-4 unblock receipt):

| # | Evidence | Passes when |
|---|---|---|
| E1 | `git -C <data-dir> check-ignore -v mail-cache/ mail-cache/x.enc` | Both paths match a rule naming the cache directory |
| E2 | `git -C <data-dir> ls-files -i -c --exclude-standard` | Empty — nothing cache-related is already tracked |
| E3 | `POST /api/v1/backup` archive member listing | No `mail-cache` member; a positive-control ordinary state file **is** present (proves the listing instrument sees members) |
| E4 | Restore of that archive into a scratch dir | No cache file materializes |
| E5 | Staging run with the deployed job's rules after a real cache write | `git status --porcelain` shows no cache paths staged; the positive control appears |

### US-5 — Removal cascades with truthful cleanup outcomes (P0)

Five events must end a pair's cached identity: mailbox disable, mailbox removal, agent deletion, workspace deletion, and credential/endpoint change — plus key rotation and store lock from the credential side. Today none of them touch the filesystem (verified §2.1), and the one handler that comes closest (`deleteAgentMailbox`) reports unconditional success even when its best-effort steps fail. This story defines the cascade and its honesty rule.

**Why this priority**: resurrection is the failure mode — a late cache write or a failed unlink that still reports success leaves mail-derived files (or retained sockets, or live presence) behind a removed pair, and the operator has no way to know.

**Independent test**: for each trigger, remove a pair with a cache subtree present and a paused in-flight cache write; assert leases close, presence revokes, files disappear (or the outcome says cleanup-pending), and the paused write cannot recreate anything.

**Acceptance scenarios**:

1. **Given** a mailbox save that flips `enabled` true→false, **When** the save commits, **Then** the pair's cache subtree is purged, its leases are closed and its presence is revoked — the same transition detection the existing `wasEnabled` capture already provides, mirrored for the disable direction.
2. **Given** mailbox removal (`deleteAgentMailbox`), **When** config and credentials are removed, **Then** the cache purge runs best-effort, and the response distinguishes full `removed` from `removed_cleanup_pending` (with a safe cleanup code) instead of unconditional success; the separately authorized Retry-cleanup operation works **after** the config row is gone, keyed by the pair's opaque cleanup intent.
3. **Given** an unlink that fails (permissions, AV hold, platform lock), **When** the removal handler responds, **Then** it never reports a successful purge: the pair stays tombstoned, the cleanup-pending outcome is visible, and no plaintext copy of anything is created as a workaround.
4. **Given** a cache write that was in flight when removal happened, **When** the write completes, **Then** a generation/tombstone check at publication time discards it — no file is (re)created after removal, on any trigger, without polling or sleeps.
5. **Given** agent deletion, **When** the agent's records are removed, **Then** every pair the agent owned is cascaded through the same cache purge (best-effort, same truthful-outcome rule) — the deletion response carries the same pending-cleanup discrimination when any purge fails.
6. **Given** workspace deletion, **When** `releaseRuntimeResources` cascades its mailboxes, **Then** each removed pair's cache subtree is purged in the same step (the wholesale directory wipe does not cover the cache, which lives outside `workspaces/<id>/`).
7. **Given** a credential, host, port, username or folder-override change on an existing pair, **When** the save commits, **Then** the pair's generation advances before any older completion can publish, old disposable caches are deleted (never migrated under an uncertain key), and the pair's leases are closed.
8. **Given** credential-store lock or key rotation, **When** the cache manager notices, **Then** in-memory derived keys and cached plaintext are cleared, disposable files are removed, and rebuild happens only after normal credential resolution succeeds — a missing/wrong master key is never "fixed" by creating a new one over existing data.
9. **Given** a gateway restart with orphan cache files (pair no longer configured), **When** boot reconciliation runs, **Then** orphans whose pair no longer exists are deleted best-effort, with failures logged as safe classes — never rendered as usable state.

### US-6 — Metadata-only preview grants (P0)

Today a mail HTML preview grant carries the entire fetched payload — sanitized HTML, every inline part's bytes, the remote-image URL list — for fifteen minutes (`mailPreviewGrant`, `MailPreviewTokenTTL`). That is a body cache by another name and breaks the request-only body rule. This story converts grants to authorization/reference metadata: every preview serve request fetches and sanitizes within its own request, through the shared budget and pool, and holds bytes only for that request. The ADR explicitly permits keeping the remote-image **URL list** in the grant (source-URL metadata is not bytes).

**Why this priority**: it is the one place the current code durably holds message bodies outside a request, and the design marks it a violation to be removed as part of this package — not a Phase 2 item.

**Independent test**: mint a preview, inspect the token store (no HTML/inline bytes present), serve the preview and count IMAP commands (serve fetches live), then assert every existing token control still holds: session cap, logout revocation, 404 parity, signature-token discipline, remote-image consent.

**Acceptance scenarios**:

1. **Given** a minted message-preview grant, **When** the token store is inspected, **Then** it contains authorization/reference metadata only — no HTML string, no inline part bytes, no attachment data — and expiry, session binding and the pair/folder/ref/load-remote identity as today.
2. **Given** a minted grant, **When** the SPA serves the preview, **Then** the serve path performs a live, budget-gated fetch (the four-GET budget wrapper's rules apply to preview fetches too), sanitizes within the request, serves with `Cache-Control: no-store`, and holds bytes only for that request's lifetime.
3. **Given** the mint request itself, **When** it is processed, **Then** it dials nothing: mint authorizes the pair and issues the ref-bound token; the first serve performs the fetch and surfaces 404 (missing), 400 (bad ref), or the size/decode failure — a mint that would have failed under today's eager fetch now fails at serve with the same safe classes.
4. **Given** every existing preview security control, **When** the grant shape changes, **Then** all of them still hold: per-session cap 8 refuses (429) with no eviction, logout revocation via `invalidateSession`, unknown/expired/revoked all one 404 answer, signature tokens replace-on-mint at their own 2-minute TTL and never counted against the message cap, remote images only from the grant-recorded URL list through the token-scoped bounded proxy, and the Mail CSP unchanged on the isolated document.
5. **Given** a message with N inline images, **When** the SPA renders the preview, **Then** the part requests each fetch through the shared pool — the added live fetches are counted and reported (the ADR's measurement obligation), never hidden; the design accepts the cost and this package surfaces it to the measurement plan.
6. **Given** the new preview-purpose byte endpoint (grill I-05's wire split, defined by W0), **When** it serves an attachment part for Open, **Then** it enforces inline disposition, `no-store`, the unchanged 25 MiB actual-decoded-byte cap checked before success, and the grant's ownership lifecycle (view exit, expiry, revoke, panel close all end it) — while the existing attachment endpoint keeps its browser-Download role and disposition via the unchanged `applyMailByteHeaders`.

### US-7 — Redaction: what pool, cache and probe diagnostics may record (P1)

New pool/cache/probe diagnostics are mandatory for the measurement plan, and they sit next to two existing seams that pass raw upstream text into durable state (`recordFailure` persists `LastErrorText`) or logs (`mailErr502` logs the raw error). This story draws the line.

**Why this priority**: the watcher state file is captured by the Git backup today (un-ignored, per the trace) — raw server error text landing there is a real disclosure path, not a style concern. The wire is already safe: the summary serves only the class, so removing raw text has no UI impact (verified §2.1).

**Independent test**: drive a fake IMAP server whose error strings contain distinctive synthetic markers (a fake subject, address and folder name); assert the markers appear nowhere in logs, watcher state, audit entries or cache envelopes, while the safe class strings do appear.

**Acceptance scenarios**:

1. **Given** any pool, cache, probe or watcher diagnostic write, **When** it records, **Then** it records only: opaque pair identifiers, generation identifiers, operation labels, safe error classes, durations, and counts (dials, commands, sockets, hit/miss, refusals).
2. **Given** the same writes, **When** inspected, **Then** they never contain: subjects, addresses, Message-IDs, server folder names (role slugs `inbox`/`sent`/`drafts` are fine), credential values, full JMAP URLs, raw protocol transcripts, response bodies, or any raw upstream error string passed through as if it were safe.
3. **Given** `recordFailure(errClass, errText)` (W1's file, this package's requirement), **When** a failure is recorded, **Then** the raw `errText` no longer enters the persisted state file — the field receives either empty or a class-derived safe message; the summary wire contract is unchanged (it already serves only the class).
4. **Given** `mailErr502` and the mail-path `logsafeError`/`logsafeWarn` sites, **When** they log, **Then** they log the class plus safe fields — not the raw error value.
5. **Given** an error that leaves its trust boundary (into a 5xx detail, a cleanup-pending code, a cache notice), **When** it is rendered, **Then** it is a closed safe class; URL-bearing values are redacted before crossing.

### US-8 — Durable generation across restarts (P0)

The generation that invalidates caches and blocks late completions must survive restarts: a process that restarts after an offline endpoint/password edit must not reconstruct a generation that makes old encrypted snapshots look applicable to the new account (the ADR's "stable generation incorrectly rebuilt" risk). This package decides the mechanism (W0 contracts the wire-visible parts; W1/W2 consume it).

**Why this priority**: without durable generation, every other invalidation rule can be silently undone by a restart — the tombstone holds but the snapshot still decrypts.

**Independent test**: configure a pair, write a snapshot, stop the process, change the pair's password in config offline, restart, and present the old snapshot — assert it is rejected without plaintext salvage and the pair rebuilds live.

**Acceptance scenarios**:

1. **Given** a pair's canonical identity (endpoint host/port, username, credential reference binding) resolved at runtime, **When** the cache writes or reads a snapshot, **Then** the snapshot's envelope carries a purpose-keyed, non-secret fingerprint of that identity (derived via the sanctioned `DeriveSubkey` seam over the canonical identity and the resolved credential material — never the password bytes themselves in key names, files or logs), recomputed and compared on every load.
2. **Given** a restart without any credential change, **When** the cache loads a snapshot, **Then** the fingerprint matches and the snapshot is usable — pair identity is stable across normal restarts.
3. **Given** any relevant identity change (password, host, port, username, folder-override), **When** the next load compares fingerprints, **Then** the old snapshot is rejected as foreign — deleted, rebuilt live, never salvaged, never displayed.
4. **Given** the locked-store case, **When** a cache read is attempted, **Then** the manager reports the store state distinctly (locked ≠ cache miss ≠ corrupt) and serves live-only with the safe warning — a broken credential store is never misreported as a cache problem.

---

## 4. Behavioral contract (quick reference)

### 4.1 When/Then summary

| When | Then |
|---|---|
| Any mail path (panel, watcher, tool) resolves its runtime | It gets the same process-wide pool, budget and cache instances |
| Two pairs share one account and run identical reads | Results never cross pairs; combined dials still respect the 2-slot account gate |
| The last panel observer disappears (frame, socket death, logout) | Panel-owned idle sockets close; shared flights finish only for remaining real owners; nothing polls |
| A cache write is attempted without proven Git-staging exclusion | The write is refused before file creation; the pair runs live-only with a visible `cache_unavailable` notice |
| A cache file is written | It is ciphertext-only, atomically replaced, 0600 in a 0700 dir, at a stable per-pair path derived from the data root |
| A tar backup is created or restored | `mail-cache` is skipped on archive and on restore — old archives included |
| Any of the five removal triggers fires | Leases close, presence revokes, generation advances, cache purges; a failed purge yields `removed_cleanup_pending`, never silent success |
| A cache write completes after its pair was removed | It is discarded by the tombstone/generation check — no resurrection, no polling |
| A credential/endpoint/override change commits | The generation fingerprint changes; old snapshots are rejected and deleted, never migrated |
| A preview is minted | No HTML/body/inline bytes enter any store; the grant is authorization/reference metadata only |
| A preview or inline part is served | It live-fetches through the shared budget/pool, sanitizes in-request, serves `no-store`, drops bytes at request end |
| A preview token is unknown, expired or revoked | The same 404 answer as today; cap-8 still refuses with 429; logout still revokes everything |
| Any mail diagnostic is written | Only opaque ids, safe classes, durations and counts — never subjects, addresses, folder names or raw upstream text |

### 4.2 Explicit non-behaviors

| The system must not… | Because |
|---|---|
| …construct a second pool/budget/cache instance on any path | The founder's 2/mailbox and 8/global ceilings are unenforceable against a forked pool (ADR P1.2) |
| …let the account key (`host:port|username`) decide result sharing or cache isolation | Two pairs on one account would read each other's data (grill I-01); the account key is contention-only |
| …write any cache file before exclusion from Git staging is proven | The first unexcluded write is committed off-machine within 15 minutes and history retains it (trace §5) |
| …fall back to a plaintext cache file, or to an unencrypted temp/journal file, when encryption or exclusion fails | A degraded cache is worse than no cache; the failure must be visible (ADR security section) |
| …reuse the watcher's lossy `keyFor` sanitizer as the cache pair identifier | Two distinct pairs can produce the same sanitized name; the cache must be unambiguous |
| …report a successful purge when an unlink failed | A false success orphans mail-derived files with no operator-visible trail (ADR cleanup-failure row) |
| …poll, sleep or retry-loop to win the late-completion race | The generation/tombstone check is a correctness rule, not a timing heuristic (ADR: no new timers) |
| …store HTML, body, attachment or inline bytes in any token grant, query cache or persistent store | The request-only body rule has no exceptions (founder brief; F1 "no storage" row) |
| …let the preview mint dial IMAP | Mint is authorization; the serve request is the fetch — a validating mint doubles fetches and reintroduces eager work |
| …loosen any existing preview control to make metadata-only grants easier | Every control (cap, revocation, 404 parity, proxy scoping, CSP) is preserved verbatim (US-6.4) |
| …serve a restored cache snapshot from an old backup archive | Stale snapshots bypass every freshness rule (trace §3, restore row) |
| …route agent file tools through the cache directory | A generic file tool must never become a decryption route (ADR filesystem row) |
| …introduce a mail-data polling timer, panel-refresh heartbeat or IDLE subscription | Retired/forbidden surfaces (ADR P1.1, non-goals) |
| …log or persist subjects, addresses, Message-IDs, server folder names, credentials or raw upstream error strings | Watcher state is Git-captured today; logs are durable; raw text is not a safe class (US-7) |

### 4.3 Machine-verifiable constraints

| ID | Constraint | Test observable |
|---|---|---|
| MC-1 | Exactly one pool, one budget, one cache manager per process; all six wiring sites (§2.2) resolve pointer-identical instances | Pointer identity assertions at each site; second-construction returns the same instance |
| MC-2 | Global concurrent IMAP sockets ≤ 8; per-mailbox ≤ 2; connecting reservations counted; a 9th demand gets the typed busy refusal within the 5 s acquisition window | Fake-server accepted/max counters; refusal latency ≤ acquisition bound |
| MC-3 | Identical reads on two pairs of one account produce pair-correct results (no cross-pair flight sharing); combined dials ≤ 2 | Per-pair result markers on the fake server; dial counter |
| MC-4 | Observer open → panel request may retain a socket; observer close, socket death, or logout → no panel-retained socket survives (server socket count returns to baseline) | Fake-server socket count before/after; independent tool request unaffected |
| MC-5 | Cache path = `<data-root>/mail-cache/<opaque-pair-id>/…`; dir mode 0700, file mode 0600 (Unix); Windows current-user ACL; stable across restart; distinct for distinct pairs | Path/mode assertions; two-pair distinctness; restart stability |
| MC-6 | Cache write refused with `cache_unavailable` when the exclusion rule is absent from both `<data-dir>/.gitignore` and `.git/info/exclude`; allowed when present in either; no file created on refusal | Flip the rule in a temp data dir; assert file existence and notice code both ways |
| MC-7 | `createTarGz` skips top-level `mail-cache`; `extractTarGz` skips `mail-cache` members regardless of archive age | Archive member listing; restore-into-scratch assertion; positive control file present |
| MC-8 | Removal of a pair with a failing unlink → response `removed_cleanup_pending` + safe code + Retry-cleanup succeeds later, including after the config row is gone; success path → subtree gone | Permission-blocked dir; retry assertions |
| MC-9 | A cache write completing after removal writes nothing (tombstone/generation guard), deterministically, with the write paused mid-flight | Paused-write harness; disk assertion after release |
| MC-10 | Credential/host/port/username/override change → old snapshot rejected (fingerprint mismatch), files deleted, leases closed; restart without change → snapshot still valid | Offline-edit-then-restart dataset; both directions asserted |
| MC-11 | Minted preview grant contains zero bytes: no HTML string, no `Inline[].Data`, no attachment data; URL list permitted; expiry/session/identity fields as today | White-box store inspection + grant-size bound |
| MC-12 | Preview serve fetches live through the budget: during backoff → typed refusal, zero dials; otherwise one gated fetch per serve request (counted) | Fake-server command counter; backoff-state serve attempt |
| MC-13 | All existing preview controls hold: cap-8-refuses (429, no eviction), logout revocation, unknown/expired/revoked = one 404, signature kind replace-on-mint @ 2 min TTL and outside the message cap, remote images only from the grant-recorded list via the token-scoped proxy | The existing preview test families re-derived against the new grant shape — none weakened |
| MC-14 | Preview-purpose responses carry `Cache-Control: no-store`; the new preview byte endpoint enforces the 25 MiB actual-decoded cap before success and inline disposition; the download endpoint keeps `applyMailByteHeaders` attachment disposition | Header assertions on both endpoints; over-cap refusal with false metadata |
| MC-15 | Diagnostics contain zero leak markers (synthetic subject/address/folder-name markers) and do contain the safe class strings; `recordFailure` persists no raw text; `mailErr502` logs class + safe fields only | Marker-scan test with positive control |
| MC-16 | Agent file/shell policy refuses any path under the cache directory | `ResolveTurnFSPolicy`/`ResolvePath`-level refusal test |
| MC-17 | Pair identity is stable across restart and changes with endpoint/credential identity, via a purpose-keyed non-secret fingerprint over canonical identity + resolved credential material (DeriveSubkey seam); no password bytes in filenames, keys or logs | Restart-stability + change-rejection dataset; fingerprint never equals a password hash of the raw password alone |

### 4.4 Integration boundaries

| External system / package | Data in and out | Contract | Failure behaviour |
|---|---|---|---|
| **W0 (contracts)** | Schema/enum additions this package needs (§8) | This package never edits `contracts/` or generated artifacts; it requests, W0 defines and regenerates | A missing generated type blocks this package's handler work — declared, not improvised |
| **W1 (read runtime)** | Pool/lease manager, budget internals, watcher scheduling; presence hooks and generation hooks it exposes | Frozen internal interfaces agreed with architect + W0 before parallel writers start (ADR sequencing) | Pool unavailable → requests take the typed busy/failure path, never bypass |
| **W2 (discovery/cache service)** | Folder-mapping and header-cache services; the encrypted envelope writer | This package decides *where* files live, *whether* writes are gated, and *when* they are purged; W2 owns the envelope format and crypto | Cache service error → safe cache-health warning; live work continues (ADR failure table) |
| **W7 (attachment service)** | Transfer service for Save mode | This package wires preview/download endpoints to it; it never edits Library files | Service refusal → typed error on the wire, no partial file |
| **W3 (SPA)** | Generated REST/WS types only | Presence frames, metadata fields, cleanup-pending result — all via W0's generated types; no ad-hoc JSON | SPA missing → server behaviour unchanged (server never depends on UI) |
| **Deployment (omnipus-agent-os, outside repo)** | The `mail-cache/` deny rule in the provisioning script's ignore template + idempotent re-run | Owned by team-lead/founder ops; this package specifies it (US-4) and consumes its evidence | Rule absent → the product-side gate keeps disk writes blocked (defence in depth) |
| **launchd auto-commit job** | Reads whatever the deny-list allows | Not controllable by product code; the ignore rule is the only lever | Job blocked/unloaded (today's live state) does NOT weaken the gate — the gate is the rule + product check, not the job's health |
