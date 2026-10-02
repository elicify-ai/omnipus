# Implementation Specification: Mail live access — W5 integration and privacy (shared runtime wiring, product-owned cache exclusion, truthful removal)

**Created**: 2026-10-02
**Status**: Correction round applied (2026-10-02) — the one prescribed grill round (grill report + `adr-grill-3.md` naming this spec: C-1, C-2, I-1…I-5, M-1, M-2, M-3, M-7, M-8; `adr-grill-4.md` naming this spec: C1, C2, C3, m4) and the founder interview answers (`mail-feature-decisions.md`, 2026-10-02) are applied in this revision, together with the ownership assignments of `docs/internal/specs/mail-live-access-landing-order.md` §2 (the interface register). No founder decision is reopened.
**Design authority**: ADR-20261001 — *Mail live access: pooled connections, folder discovery and a bounded cache* (`docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`), including its applied correction round (grill findings I-01–I-06, M-01–M-02; founder answers Q1=A, Q2=B, Q3=A, Q4=A, Q5=A)
**Review input**: `adr-grill-report.md` (PASS WITH FINDINGS — 0 Critical, 6 Important, 2 Minor), `adr-grill-3.md` (W4-features + W5-integration grill, BLOCK) and `adr-grill-4.md` (W6 + cross-spec grill, BLOCK), plus `docs/internal/specs/mail-live-access-landing-order.md` (the landing-order and interface-ownership register) and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md` (founder decisions, 2026-10-02) — all read in full for this correction
**Package-number mapping** (cite this line whenever a package letter is ambiguous): spec file **w1** = ADR-W1 (read runtime) · **w2** = ADR-W2 (discovery/cache) · **w3** = ADR-W3 (panel) · **w4** = ADR-W7–W10 (attachment features) · **w5 = ADR-W4 (integration/privacy) — this file** · **w6** = ADR-W5 (proof, tests only). There is **no W0 spec file**: the contracts wave is owned by backend-lead in the ADR's **W0 role** (landing-order register §2 row 1 and §5), not by a sibling document.
**Package**: this spec covers the ADR's **W4 — integration/privacy** work package (one backend-lead owner); the spec file is numbered w5 in the implementation-spec sequence (mapping line above). Where this document says "this package" it means that owner.
**Sibling specs**: w1 (read runtime: pool/lease/budget/watcher), w2 (discovery, folder-file encryption, memory caches, part reader + MIME classifier, server-side search/cursor issuance), w3 (panel/settings SPA), w4 (attachment features), w6 (proof — tests only) — authored in parallel; none may edit another's files. The Publishes/Consumes section (§7) is the parallel-work contract; the landing-order register §2 is the first authority on who owns any interface.
**Certainty labels**: **Verified** = read in this checkout (`wt-adr-mail` @ `fc4c8bf6dcdfa257e3540f602f042dfa19882ce1`, branch `docs/adr-mail-live-access`) in this task; **Inferred** = reasoned from verified evidence, not executed; **Unknown** = genuine gap, named as one. GitNexus MCP tools were not exposed in this session; impact assessment is grep-based and labelled Inferred accordingly. No test, build or benchmark was run.

---

## 1. Summary and scope

**Bottom line.** This package is the integration spine of the mail live-access design: it makes **one** connection pool, **one** operation budget and **one** bounded cache serve the Mail panel, the background watcher and the agent tools — so no code path can quietly build a second pool and break the founder's 2-per-mailbox / 8-global socket ceilings. It owns the privacy envelope around that shared cache: where the encrypted files live on disk, what must exclude them from the machine's Git auto-backup and the app's tar backup before the first byte is written (a **hard gate** — disk cache stays blocked until the deployment change is proven in place), and every removal path (mailbox, agent, workspace, credential change, key rotation) with a cleanup result that tells the truth when a file delete fails. It also converts today's 15-minute HTML preview tokens — which currently hold whole message bodies in memory — into metadata-only grants whose every serve request fetches and sanitizes live through the same shared pool, and it closes the logging holes that let raw server text reach state files and logs.

**In scope (this package's exclusive production files):** the gateway Mail read/budget/summary/settings/removal/preview files under `pkg/gateway/` (the routes, token store, isolation and inline-serving seams), the boot/reload/REST/socket wiring that injects the shared runtime, `pkg/agent/email_tools.go` and `pkg/config/config.go` (tool-side injection). This package is the **single gateway Mail-route writer** during this feature wave (landing-order register §4, Wave C: w4's former gateway rows are consume-rows against this package's files; adr-grill-3 I-1 resolved in this package's favour). Beyond production files, this package owns these register-assigned obligations: the exclusion-gate decision and the product-owned exclusion guarantee (US-4), the generation/pair-ID construction (US-8), `message_ref` issuance in the gateway handlers (US-9), the request-scoped instrument envelope (US-7), and the Windows CI workflow extension that lets the cache-permission evidence actually run (§7.1).

**Out of scope (owned elsewhere, consumed through frozen interfaces — §7):** the pool/lease manager implementation (w1), folder discovery, the encrypted-file envelope, the part reader and the MIME classifier (w2), all SPA work (w3), every contract/generated edit (**backend-lead in the ADR's W0 role** — no W0 spec file exists; register §2 row 1), the attachment Transfer service and Library storage (w4's ADR-W7), the agent attachment tools and policy catalog (ADR-W10), all tests (w6/qa-lead — this spec only names them), and the Phase 2 JMAP transport (ADR-W6, later wave).

| Deliverable | One-line outcome |
|---|---|
| Shared runtime wiring | Panel, watcher and tools resolve one pool/budget/cache instance; a second acquire of the same account slot is structurally impossible |
| Presence lifecycle | Mail panel observer identity rides the authenticated WebSocket and the REST `observer_id` param; teardown on close frame, socket death, logout and workspace exit |
| Cache placement + gate | `mail-cache/` under the data dir, 0700/0600, agent-unreachable; the product itself guarantees exclusion from its own backups/archives on every install and refuses disk writes unless staging exclusion is proven — by its own checks, never by depending on any personal machine setup |
| Backup/restore exclusions | Tar backup and restore never carry or resurrect cache files — unconditional product code, on every install |
| Removal cascades | Five triggers purge leases, presence, cache **and the pair's watcher state file**, with truthful `removed` / `removed_cleanup_pending` outcomes and late-completion protection |
| Metadata-only preview grants | No HTML/body/inline bytes in any token store; every preview fetch is a live, budgeted, sanitized request; existing token security controls preserved |
| Interface obligations | Publishes the exclusion-gate decision, the generation/pair-ID construction, `message_ref` issuance and the instrument envelope (§7.1); every other interface is consumed from its register-named single publisher (§7.2) |
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

This package sits in the **gateway / HTTP shell** cluster and the **agent runtime** cluster's tool-wiring edge. It owns no transport or discovery logic (w1/w2 clusters) and no UI (w3). It is the only package allowed to edit the gateway Mail routes during this feature wave, per the ADR's one-writer-per-file rule — restated by the landing-order register (§4, Wave C: "w5-integration as the single gateway Mail-route writer"; adr-grill-3 I-1 resolved in this package's favour, w4's former "W4-owned gateway work" rows being consume-rows against this package's files).

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

---

## 5. BDD scenarios

### Feature: One shared mail runtime

#### Scenario B-1: All three paths resolve one runtime
**Happy** · Traces to: US-1.1, MC-1
**Given** a booted gateway with a configured mailbox,
**When** the panel handler, an agent tool and a watcher cycle each resolve their mail runtime handles,
**Then** all three hold the same pool instance, the same budget instance and the same cache-manager instance.

#### Scenario B-2: Two pairs, one account, no shared flight data
**Alternate** · Traces to: US-1.2, MC-3
**Given** pairs P1 and P2 configured on the same `host:port|username` account with different Sent folder mappings,
**When** both issue the same folder-list request concurrently against a fake server whose responses are marked per pair,
**Then** P1 receives only P1's mapping result and P2 only P2's, while the fake server never sees more than two concurrent dials for the account.

#### Scenario B-3: Ninth socket demand is refused visibly
**Error** · Traces to: US-1.3, MC-2
**Given** eight global sockets active on the fake server,
**When** a ninth read arrives and waits out the 5-second acquisition window,
**Then** the caller receives the typed busy refusal carrying `pool_busy`, and the fake server's accepted-connection count never exceeds eight.

#### Scenario B-4: No unmanaged client construction
**Edge** · Traces to: US-1.4, MC-1
**Given** any REST route, tool registration or watcher cycle,
**When** it constructs its mail client,
**Then** the client carries the injected shared handles, and a client built without them is a construction error surfaced in tests — never a silently unmanaged dialer.

### Feature: Panel presence lifecycle

#### Scenario B-5: Open frame grants retention; close frame revokes one observer
**Happy** · Traces to: US-2.1, US-2.2, MC-4
**Given** an authenticated WebSocket with an acknowledged mail observer,
**When** a panel list request retains its socket past request end and then the same connection sends the observer's close frame,
**Then** the retained socket closes and the server's socket count returns to baseline, while a second tab's observer on another connection keeps its own retained socket.

#### Scenario B-6: Abrupt socket death revokes observers
**Error** · Traces to: US-2.3, MC-4
**Given** a live observer whose client crashes without sending close,
**When** the pong-wait read deadline expires and teardown runs,
**Then** the observer is revoked by the teardown hook, idle panel-owned sockets close, and a shared in-flight read completes only for its remaining real owners — with no polling or timer introduced.

#### Scenario B-7: Requests without presence degrade, never fail
**Alternate** · Traces to: US-2.4
**Given** a mailbox request carrying an unknown or stale observer id (or none at all),
**When** the handler executes it,
**Then** the request performs live request-scoped work (or serves cache per its mode), retains no socket, and returns success or its own genuine failure — presence absence is never itself an error.

#### Scenario B-8: Presence never grants access
**Edge** · Traces to: US-2.5
**Given** an authenticated connection with an open observer for workspace W1,
**When** a mailbox request addresses a pair in workspace W2 the connection is not authorized for,
**Then** the existing pair authorization refuses it exactly as it would without any presence — observer association adds retention, never access.

### Feature: Encrypted cache placement and the write gate

#### Scenario B-9: First cache write lands correctly
**Happy** · Traces to: US-3.1, US-3.2, MC-5
**Given** a temp data directory whose ignore file matches the cache directory,
**When** a pair's folder metadata is written,
**Then** the file exists at `<data-root>/mail-cache/<opaque-pair-id>/folders.enc`, the directory is 0700, the file 0600, the content is ciphertext (no plaintext marker survives), and a second pair's path differs.

#### Scenario B-10: Unexcluded directory blocks writes
**Error** · Traces to: US-3.3, MC-6
**Given** a temp data directory whose ignore files do NOT match the cache directory,
**When** the first cache write is attempted,
**Then** no file or directory is created, the pair serves live-only, and the surfaced cache state carries the `cache_unavailable` notice code.

#### Scenario B-11: Rule present in either location allows writes
**Alternate** · Traces to: US-3.3, MC-6
**Given** the rule present in `.git/info/exclude` but not `.gitignore` (and the mirror case),
**When** a cache write is attempted,
**Then** the write proceeds — either location satisfies the conservative gate.

#### Scenario B-12: Symlink at the cache path is refused
**Error** · Traces to: US-3.4
**Given** a symlink placed at the cache directory path pointing outside the data root,
**When** the manager resolves or writes,
**Then** the operation is refused with a safe class and nothing is written through the link.

#### Scenario B-13: File tools cannot reach the cache
**Error** · Traces to: US-3.5, MC-16
**Given** an agent holding normal workspace file authority,
**When** it resolves a path under `mail-cache/` for read or write,
**Then** the filesystem policy refuses the path — the cache is never agent-reachable regardless of workspace grants.

### Feature: Backup, restore and the deployment exclusion

#### Scenario B-14: Tar backup skips the cache, keeps the control
**Happy** · Traces to: US-4.2, MC-7
**Given** a data directory containing `mail-cache/x.enc` and an ordinary allowed state file,
**When** a backup archive is created,
**Then** the member listing contains the control file and no `mail-cache` member.

#### Scenario B-15: Restore never resurrects cache members
**Alternate** · Traces to: US-4.2, MC-7
**Given** a hand-built archive that (like an old pre-fix archive could) contains a `mail-cache` member,
**When** it is restored into a scratch directory,
**Then** no cache file materializes, while ordinary members extract normally.

#### Scenario B-16: The exclusion gate matches the deployed deny-list semantics
**Edge** · Traces to: US-4.1, US-4.4
**Given** the live provisioning-script ignore template with the `mail-cache/` rule added,
**When** the product gate evaluates exclusion,
**Then** the gate's rule-match semantics agree with `git check-ignore` on the deployed rules — the gate is conservative (refuses on doubt), and E1–E5 of US-4 all pass on the deployment receipt.

### Feature: Removal cascades

#### Scenario B-17: Mailbox removal with successful cleanup
**Happy** · Traces to: US-5.2
**Given** a configured pair with a cache subtree,
**When** the mailbox is deleted,
**Then** the response is full `removed`, the config entry and credentials are gone, and the pair's cache subtree no longer exists on disk.

#### Scenario B-18: Failed unlink is cleanup-pending, never success
**Error** · Traces to: US-5.2, US-5.3, MC-8
**Given** a pair whose cache directory is made unwritable (unlink will fail),
**When** the mailbox is deleted,
**Then** the response is `removed_cleanup_pending` with a safe cleanup code, the pair remains tombstoned, no success is claimed, and the Retry-cleanup operation — invoked after the config row is gone — completes the purge and reports success truthfully.

#### Scenario B-19: Late cache write cannot resurrect a removed pair
**Error** · Traces to: US-5.4, MC-9
**Given** a cache write paused mid-flight when the mailbox is deleted,
**When** the paused write resumes and completes,
**Then** the tombstone/generation check discards it: no file exists, no cache state advances, and the deletion outcome is unchanged.

#### Scenario B-20: Agent deletion cascades all its pairs
**Alternate** · Traces to: US-5.5
**Given** an agent with mailboxes in three workspaces and cache subtrees for each,
**When** the agent is deleted,
**Then** all three subtrees are purged (best-effort, truthful outcome), and any failed purge surfaces the pending-cleanup state rather than a clean success.

#### Scenario B-21: Workspace deletion purges pairs it cascades
**Alternate** · Traces to: US-5.6
**Given** a workspace with two mailbox pairs and cache subtrees,
**When** the workspace is deleted,
**Then** each cascaded pair's subtree is purged during `releaseRuntimeResources`, and the wholesale workspace-directory wipe's success is not mistaken for cache cleanup (the cache lives outside `workspaces/<id>/`).

#### Scenario B-22: Credential change invalidates and rebuilds
**Error** · Traces to: US-5.7, US-8.3, MC-10
**Given** a pair with a valid encrypted snapshot,
**When** its password is changed in config (offline, then process restart),
**Then** the old snapshot is rejected by fingerprint mismatch, deleted without plaintext salvage, leases from the old identity are closed, and the pair rebuilds live on next open.

#### Scenario B-23: Key lock is distinct from cache miss
**Edge** · Traces to: US-8.4
**Given** a locked credential store,
**When** a cache read is attempted,
**Then** the manager reports the store-locked state distinctly (not a cache miss, not corruption), serves live-only per the budget, and shows the safe cache warning — and never creates or regenerates a key over existing data.

### Feature: Metadata-only preview grants

#### Scenario B-24: Mint stores metadata only
**Happy** · Traces to: US-6.1, MC-11
**Given** an authenticated session and a valid message reference,
**When** the preview mint completes,
**Then** the grant in the token store contains the pair/folder/ref/load-remote identity, session binding and expiry — and zero HTML string, zero inline part bytes, zero attachment data (white-box inspection).

#### Scenario B-25: Serve fetches live through the budget
**Happy** · Traces to: US-6.2, MC-12
**Given** a minted grant,
**When** the SPA serves the preview HTML and one inline part,
**Then** each serve request performs its own budget-gated live fetch (fake-server command counter increments per request), the response carries `Cache-Control: no-store`, and no bytes persist after the request ends.

#### Scenario B-26: Mint dials nothing; failures surface at serve
**Alternate** · Traces to: US-6.3
**Given** a mint request naming a nonexistent message reference,
**When** the mint completes (no dial — counter unchanged),
**Then** the first serve attempt fetches, receives not-found, and surfaces the same safe 404 class the eager mint used to produce — the failure moved, it did not disappear.

#### Scenario B-27: Backoff blocks preview fetches with the typed refusal
**Error** · Traces to: US-6.2, MC-12
**Given** a pair inside its watcher backoff window,
**When** a preview serve request arrives,
**Then** no dial occurs and the serve path returns the typed budget refusal (the same class mapping the panel GETs use) — the preview shows its error state, never a stale cached body.

#### Scenario B-28: Every existing preview control still holds
**Edge** · Traces to: US-6.4, MC-13
**Given** the metadata-only grant shape,
**When** each legacy control is exercised — a ninth concurrent message mint (429, no eviction), logout (all session tokens revoked), an unknown/expired/revoked token (one 404 answer), a second signature mint (replaces the first, outside the message cap), and a Load-images consent (only grant-recorded URLs, token-scoped proxy) —
**Then** every control behaves exactly as it does today.

#### Scenario B-29: The preview byte endpoint and the download endpoint stay distinct
**Alternate** · Traces to: US-6.6, MC-14
**Given** an attachment part whose real decoded size exceeds 25 MiB while its reported metadata claims less,
**When** the preview-purpose byte endpoint serves it and, separately, the download endpoint streams it,
**Then** the preview endpoint aborts with the typed over-cap error before any success state exists, and the download endpoint completes the stream with attachment disposition and its existing headers — one capped preview path, one unlimited download path, never shared.

### Feature: Redaction

#### Scenario B-30: Leak markers never reach durable state
**Error** · Traces to: US-7.1, US-7.2, MC-15
**Given** a fake IMAP server whose failure responses embed synthetic markers (a distinctive subject, address and folder name),
**When** failures flow through watcher recording, budget refusals, cache warnings and gateway logs,
**Then** a scan of logs, watcher state, audit entries and cache envelopes finds zero markers and finds the safe class strings — with the scan's positive control (a deliberately recorded class string) proving it could have seen a leak.

#### Scenario B-31: Watcher state stops carrying raw error text
**Alternate** · Traces to: US-7.3
**Given** a watcher cycle failing against a raw-talking fake server,
**When** the state file is written,
**Then** the persisted error text is empty or class-derived, while the wire summary response is byte-identical in shape (it already carried only the class).

---

## 6. TDD plan (tests designed before implementation)

All tests are written and owned by the proof package (qa-lead); this spec names them and derives each from the design above — never from the implementation. Go tests use the repo's pure-Go/tag configuration; the fake IMAP harness is the existing `pkg/email/imapserver_test.go::startMemIMAP` (global dial seam — no blind parallelism). Backend tests live beside the code under `pkg/gateway/` and `pkg/agent/`; the deployment-evidence checks (E1–E5) are a runbook script under `scripts/` or `deploy/`, executed on the target machine, not a Go unit test. Existing preview/budget/deletion test families are re-derived from this spec where the behaviour legitimately changes; none may be weakened to pass.

| Order | Test name (indicative) | Level | Traces to | What it proves |
|---|---|---|---|---|
| 1 | `TestMailRuntime_SingleInstanceAcrossPaths` | Unit (wiring) | B-1, MC-1 | The six wiring sites resolve pointer-identical pool/budget/cache instances; boot-before-NewAgentLoop ordering holds |
| 2 | `TestMailRuntime_WatcherSetSharesHandles` | Unit | B-1 | The watcher set's constructor receives the same instances (no second `New*`) |
| 3 | `TestMailPairClient_CarriesInjectedHandles` | Integration | B-4 | A REST-resolved client holds the shared handles; an uninjected construction fails loudly |
| 4 | `TestAccountPairs_NoCrossPairFlightSharing` | `startMemIMAP` (marked responses) | B-2 | Two pairs on one account get pair-correct results; account dial cap still 2 (grill I-01 at the wiring layer) |
| 5 | `TestGlobalSocketCap_NinthDemandBusyWithinWindow` | `startMemIMAP` (counters, fake clock) | B-3, MC-2 | Eight active → ninth demand refuses with `pool_busy` inside the acquisition window; connecting reservations counted |
| 6 | `TestPresence_OpenRetains_CloseRevokesOneObserver` | Integration (WS + fake server) | B-5, MC-4 | Open→retention; close→that socket closes, the other tab's observer unaffected |
| 7 | `TestPresence_AbruptDisconnectRevokesViaTeardown` | Integration (kill socket) | B-6 | Deadline expiry → teardown hook revokes; idle panel sockets close; shared flight finishes for remaining owners; no timers introduced |
| 8 | `TestPresence_UnknownObserverDegradesToRequestScoped` | Integration | B-7 | Unknown/stale/absent observer id → normal live/cache service, no retention, no presence-derived error |
| 9 | `TestPresence_NeverGrantsAccess` | Integration | B-8 | Observer open on W1 does not authorize a W2 pair — pair authorization unchanged |
| 10 | `TestMailCache_PathModesAndPairDistinction` | Unit (temp dir) | B-9, MC-5 | Path shape from the configured root; 0700/0600; two pairs distinct; stable across a simulated restart |
| 11 | `TestMailCache_WriteGate_RefusesWithoutExclusionRule` | Unit (temp dir) | B-10, B-11, MC-6 | Rule absent → no file + `cache_unavailable`; rule in either ignore location → write proceeds |
| 12 | `TestMailCache_SymlinkAtPathsRefused` | Unit (temp dir + symlink) | B-12 | Symlinked dir/file components refused, nothing written through the link |
| 13 | `TestFSPolicy_RefusesCacheDirectory` | Unit (policy resolution) | B-13, MC-16 | Agent file tools cannot read/write/resolve any `mail-cache/` path |
| 14 | `TestCreateBackupArchive_ExcludesMailCache_KeepsControl` | Integration (httptest) | B-14, MC-7 | Member listing: control present, no `mail-cache` |
| 15 | `TestRestore_SkipsMailCacheMembers` | Integration (httptest) | B-15, MC-7 | Hand-built archive with a cache member → restore writes nothing there |
| 16 | `TestDeploymentExclusionReceipt` | Runbook script (machine) | B-16 | E1–E5 executed fresh: check-ignore matches, nothing tracked-ignored, archive + restore clean, staging shows no cache paths — with positive controls |
| 17 | `TestDeleteMailbox_RemovedPurge` | Integration | B-17 | Full removal → `removed`, subtree gone |
| 18 | `TestDeleteMailbox_FailedUnlinkIsCleanupPending` | Integration (blocked dir) | B-18, MC-8 | Unlink failure → `removed_cleanup_pending` + safe code; Retry-cleanup works after config row gone; success never falsely claimed |
| 19 | `TestRemoval_LateWriteCannotResurrect` | Integration (paused write) | B-19, MC-9 | Write completes after deletion → disk unchanged, deterministically (no sleeps) |
| 20 | `TestAgentDelete_CascadesCacheSubtrees` / `TestWorkspaceDelete_CascadesCacheSubtrees` | Integration | B-20, B-21 | Both deletion paths purge every owned pair's subtree with truthful outcomes |
| 21 | `TestConfigChange_GenerationInvalidatesSnapshot` | Unit + restart simulation | B-22, MC-10 | Offline password change → fingerprint mismatch → snapshot rejected+deleted+rebuilt live; no-change restart → still valid |
| 22 | `TestStoreLocked_DistinctFromCacheMiss` | Unit | B-23 | Locked store reported distinctly; live-only service; no key regeneration |
| 23 | `TestPreviewGrant_StoresMetadataOnly` | Unit (white-box) | B-24, MC-11 | Grant fields: identity/session/expiry only; zero bytes of any kind; URL list permitted |
| 24 | `TestPreviewServe_LiveBudgetedFetch` | Integration (counters) | B-25, MC-12 | Per-serve gated fetch counted; `no-store` present; nothing persists post-request |
| 25 | `TestPreviewMint_DialsNothing` | Integration (counters) | B-26 | Mint zero dials; missing-ref failure surfaces at first serve with the same safe 404 class |
| 26 | `TestPreviewServe_BackoffTypedRefusal` | Integration | B-27 | Backoff pair → zero dials + typed budget refusal on the serve path |
| 27 | `TestPreviewControls_Preserved` (family) | Integration | B-28, MC-13 | Cap-8-refuses 429; logout revocation; 404 parity; signature replace-on-mint @2 min outside the cap; consented proxy serves grant-recorded URLs only |
| 28 | `TestPreviewByteEndpoint_CapAndDisposition_DownloadStreams` | Integration (false metadata) | B-29, MC-14 | Over-cap actual bytes with lying metadata → preview aborts pre-success; same part downloads fully with attachment disposition |
| 29 | `TestMailDiagnostics_NoLeakMarkers` | `startMemIMAP` (marker server) + log capture | B-30, MC-15 | Zero markers in logs/state/audit/envelopes; class strings present; positive control proves the scan works |
| 30 | `TestWatcherState_NoRawErrorText` | Unit (scripted failure) | B-31 | Persisted error text empty/class-derived; summary wire shape unchanged |
| 31 | `TestRemovalMutation_*` (see counterexamples) | Unit | §6 counterexamples | The careless-implementation killers below |

### Test datasets

| Dataset | Rows / shape | Exercises | Traces to |
|---|---|---|---|
| DS-1 pairs | 3 pairs: two on one account (different Sent overrides), one unique | Cross-pair isolation, account contention | B-2, B-9 |
| DS-2 exclusion rules | rule absent / in `.gitignore` / in `.git/info/exclude` / in both / stale name mismatch | Gate decision table, all four cells + mismatch | B-10, B-11, B-16 |
| DS-3 permissions | 0700/0600 created; pre-existing 0755 dir (must be tightened); read-only dir (unlink fails); symlink at dir and at file | Creation, tightening, failure, escape | B-9, B-12, B-18 |
| DS-4 archives | fresh archive (no cache member); hand-built old-style archive (with cache member); archive with control file only | Backup skip, restore skip, positive control | B-14, B-15 |
| DS-5 removal states | pair live / tombstoned / config-row-gone; write in-flight paused at pre-publish and pre-write points | Cascade outcomes, late-completion guard at both pause points | B-17–B-21 |
| DS-6 generation | same identity restart; password change; host change; port change; username change; override change; locked store | Fingerprint stability and change rejection, store-state distinctness | B-22, B-23 |
| DS-7 previews | valid ref; missing ref; N-inline-part message (N=0,1,5); signature grant; 8 live grants (cap edge); 9th mint; expired token; revoked-by-logout token; over-cap part with lying metadata; backoff-state pair | Mint/serve lifecycle, cap edge, failure parity, byte-path split | B-24–B-29 |
| DS-8 markers | fake server errors embedding `SUBJECT-MARKER-7f3a`, `addr-marker@example.invalid`, `FolderName-Marker` in greeting/SELECT/BYE responses | Redaction scan with guaranteed-detectable leaks | B-30 |
| DS-9 boundaries | TTL at exactly `ExpiresAt` (refused); cap at 8th (allowed) and 9th (refused); idle retention at 2:00 vs 2:01; 24-hour mapping age ±1 min; 5-minute stale ±1 s | Time-boundary edges the constants name | B-5, B-28 |

### Counterexamples a careless implementation would survive (mandatory)

| # | The mutation (what a careless implementer does) | The test that kills it |
|---|---|---|
| X-1 | The write gate defaults to "allowed" when the ignore files are unreadable — silently permissive instead of conservative | `TestMailCache_WriteGate_*` DS-2 row: unreadable ignore file → must refuse (the gate's failure direction is refuse, never allow) |
| X-2 | Removal reports `removed` without attempting the unlink (or logs the failure and returns success — today's `deleteAgentMailbox` shape) | `TestDeleteMailbox_FailedUnlinkIsCleanupPending` with the read-only-dir dataset: asserts the pending outcome exists and success is absent |
| X-3 | The tombstone check happens at removal time only, not at write-publication time — late writes still land | `TestRemoval_LateWriteCannotResurrect` pauses the write **after** removal completes: only a publication-time check survives |
| X-4 | The grant drops the HTML from the *serve* path but keeps storing it at mint ("we don't serve from it, so it's fine") | `TestPreviewGrant_StoresMetadataOnly` is white-box on the store, not the serve path — storage alone fails it |
| X-5 | Presence teardown handles only explicit close frames; a crashed tab leaks retained sockets until process restart | `TestPresence_AbruptDisconnectRevokesViaTeardown` kills the socket without a close frame |
| X-6 | The gate checks only `.gitignore`, missing `.git/info/exclude` (or vice versa) — false refusals that operators "fix" by disabling the cache | DS-2's either-location rows pass only with both checked |
| X-7 | The exclusion scan greps for the literal directory name but not as a gitignore pattern (`mail-cache-backup/` would match a naive `strings.Contains`) | DS-2's stale-name-mismatch row + B-16's `git check-ignore` semantics-agreement assertion |
| X-8 | Pool wiring done at four of the six sites; the watcher set keeps constructing its own handles (the easiest site to miss — it lives in a boot file far from the mail routes) | `TestMailRuntime_WatcherSetSharesHandles` exists precisely for that site |
| X-9 | Redaction implemented as "log the error's `Error()` but truncated" — markers survive in the first N chars | DS-8 markers are placed at the *start* of server messages; the scan requires zero occurrences anywhere |
| X-10 | The preview byte endpoint enforces the cap on *reported* size only — lying metadata admits an over-cap preview | `TestPreviewByteEndpoint_*` uses the false-metadata dataset and asserts abort before success, per grill I-05 |

### Regression obligations (existing behaviour that must survive)

| Existing test family | Why it must keep passing | Note |
|---|---|---|
| `pkg/gateway/mail_preview_*_test.go` families | Cap/revocation/404-parity/CSP controls are preserved by design | Re-derived where the grant shape changes (mint no longer 400s on size — the check moves to serve); never weakened |
| `pkg/gateway/mail_budget_red_test.go` | The budget's backoff/busy/coalescing contract is unchanged at its own layer | Pool wiring sits beneath it |
| `pkg/gateway/no_local_mail_store_test.go` | The no-mail-content-on-disk guarantee | Must keep passing with the cache directory present (folder metadata is not mail content) |
| `pkg/gateway/rest_mailbox_test.go` | Config persistence semantics (E-Overrides) | Disable cascade adds behaviour; changes nothing about field handling |
| `pkg/agent/email_tools_test.go`, `pkg/tools/email_no_retry_param_test.go` | Tool registration and the no-retry rule | Setter extension must not alter registration shape |
| `pkg/email/view_missing_folder_test.go` | The count-side missing-folder hotfix | Named in the ADR's test strategy as preserved |

---

## 7. Publishes / Consumes (the parallel-work contract)

**Correction, 2026-10-02 — ownership is binding, not a fallback plan.** The single-publisher register is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/mail-live-access-landing-order.md::2. The interface register`. It supersedes this spec's former double publication of presence, unspecified generation construction, ignore-only gate and missing interface rows. **W0 means backend-lead's contracts role, not a spec file.** This package is **w5-integration = ADR-W4**; w4-features = ADR-W7–W10; w6-proof = ADR-W5. Each wire type is published by W0, each internal service by the owner below. A consumer creates no stub, parallel type, second MIME walker, second registry or second emitter definition.

### 7.1 This package PUBLISHES

Names of future internal methods are indicative; the input/output shapes, ownership and behaviours below are frozen by this correction. These are specifications, not hand-written wire types or implemented services.

| Interface / obligation — single publisher: w5-integration | Consumers | Exact shape and semantics | Freeze point |
|---|---|---|---|
| **Runtime injection and shared accessors** — boot, REST, watcher construction and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/agent/email_tools.go::registerEmailToolsForAgent` | Existing mail tools and w4-features' adapters; this package's handlers | Accept the **existing publishers' handles**: W1's `MailSessions`, budget and `PanelPresence`; W2's discovery, snapshot store, header/count cache and part reader; this package's opaque identity, revision-counter implementation and gate decision; w6's instrument sink. Resolve one set per configured data root and inject before agent/watcher construction and on reload. Nil/uninjected production handles yield a typed visible wiring error, never an ungated production dial. Reload replaces dependencies, never stacks them. | Shape: Wave A, this row; dependency shapes: W1/W2 freezes below. Implementation: Wave D, before Wave E. |
| **Gateway presence lifecycle adapter — not a registry** (register rows 5, 6, 11) | W1's registry; W3 via W0 frames and REST params | Consume `PanelPresence.Bind(connID, observerID, workspaceID)`, `Unbind(connID, observerID)`, `UnbindAll(connID)`, `Count(workspaceID)`. `connID` is issued by the authenticated gateway; W3 supplies a fresh per-panel `observer_id`, which the gateway acknowledges and binds to that connection. Validate the REST association against authenticated user/session, acknowledged observer and authorized workspace/pair. Close/disconnect/logout invokes W1's unbind methods before connection teardown. No second presence store; no caller-supplied connection identity. Missing/foreign/stale presence gives request-scoped work, never mail authority or retention. | W1 registry freeze: its §3.1, Wave A. Wire freeze: Wave B. Adapter: Wave D. |
| **Opaque pair ID and generation construction** (register row 12) | W1 (pool/flight identity), W2 (cache key, authenticated envelope binding and rejection) | Pair ID = **randomly minted 128-bit value persisted beside the pair config**, unchanged on password rotation and normal restart; never derived from credentials or lossy names. Generation = **non-secret fingerprint of canonical pair identity plus the persisted config epoch**. Canonical identity includes the authorized pair, configured endpoint/TLS identity, username, credential-reference binding and folder overrides; relevant config/credential saves, including same-value re-saves, advance the epoch before old work can publish. W1/W2 consume both as opaque values. No fingerprint of resolved credential material. The sanctioned subkey seam remains for encryption only. | Construction: Wave A, this row/US-8; config serialization crossing the SPA boundary: W0, Wave B (§8); implementation/injection: Wave D. |
| **Publication-revision counter and advancement events** (register row 10) | W1's `RevisionSource`; W2's `Put`/`PutCounts`/`Save`; generated response metadata for W3 | Store one monotonically advancing local revision per pair/folder and implement **W1's** current-revision/compare accessor. W1 captures before server work; the captured value travels unchanged to W2's publication checks. Advance on confirmed own seen/send/draft mutation and each mapping/epoch/config/credential/key/removal invalidation; do not advance on cache access or failed refresh. A superseded completion publishes no rows, count, snapshot or validation timestamp; gateway attaches the applicable opaque `publication_revision`. A refresh never joins an older-revision flight. This package does not publish a competing capture interface. | Accessor: W1 §3.1, Wave A. Counter/events: this row, Wave A; implementation Wave D. Wire field: Wave B. |
| **Exclusion gate decision and product-owned policy** (register row 18) | W2 enforces the decision before cache writes; runtime startup establishes watcher-state exclusion before its first write; w6 verifies end to end | Internal result = `allowed` Boolean plus nullable safe `notice_code`; allowed **only if both** effective data-directory staging exclusion **AND** application backup/archive exclusion hold for the entire cache tree and its temporary/retired files. The same product-owned exclusion covers watcher state. The product establishes/repairs its own exclusion on every install; a missing external setup is never a prerequisite. Self-evaluate effective staging-policy precedence/negations and tracked-state conflicts; never spawn Git on this security path. Unknown/unreadable policy, already-tracked sensitive files or an unproven archive skip → refused + `cache_unavailable`, no cache write and no plaintext fallback. Re-evaluate when coverage changes; an archive-skip regression cannot leave writes enabled. | Unified condition/owner: Wave A, this row/US-4/MC-6. W2 enforcement: Wave C; product policy and archive/restore integration: Wave D, **before disk activation**. Proof: Wave E. |
| **Removal cascade and restart reconciliation** | W1 lease/flight/watcher lifecycle; W2 deletion service; W3 via generated outcomes | Input: independently authorized pair scope plus removal/disable/reconfiguration/key trigger. Tombstone/advance first, revoke grants/observers, quiesce pair work, close leases, remove cache subtree **and the pair's watcher state**, then report generated `removed` or `removed_cleanup_pending` with only a safe cleanup code and opaque authorized cleanup intent. Retry works after config deletion. Boot rejects orphan state before serving and retries deletion safely; unlink failure remains visible/retryable. No late cache or watcher write may recreate removed state. Saved Library files are not purge targets. | Internal policy: Wave A, this row/US-5. Wire result/retry: Wave B. Integration: Wave D; proof before Wave E closes. |
| **`message_ref` issuance — one gateway-owned issuer** (register row 16) | W3, w4-features' panel/tool adapters, W2's same-lease validator | Issue an opaque reference from authorized, validated metadata, binding **pair ID, config generation, folder/mapping generation, UIDVALIDITY and UID**. List/detail/attachment-metadata outputs and injected agent list/`read_message` outputs carry the same issuer's result even without Message-ID. A transport-neutral issuer/claims seam is injected into tool/data consumers: normalized claims contain those bindings, never a password or gateway import into `pkg/email`. Authenticity/decoding belongs to that one issuer capability; **W2**, using **W1's selected lease**, compares epoch/generation before fetch or mutation. This package never substitutes its own resolve-on-one-session/act-on-another validator. Wire string field and typed stale error are W0's, not a local type. | Claims/issuance obligation: Wave A, this row/US-9. Generated field/error: Wave B. W1/W2 capability/validation: Wave C. Issuance and tool injection: Wave D, before the first attachment journey. |
| **Request-scoped instrument envelope / emission obligation** (register row 17) | w6's T1–T6 and measurement campaign | Accept **w6 §6.1's single record and sink shape**, join W1 pool and W2 cache sub-fields, emit exactly one record per logical operation on success **and** safe failure, and mirror to the dedicated structured-log surface. Covers folder/list/open, preview mint/serve, summary and removal boundaries; no fabricated timings or content fields. Coalesced joiner: `socket_count=0`, `shared_flight=true`; owner carries the flight's socket accounting. Preview mint uses the frozen `open` category with zero mail acquisition; attachment preview reads use `attachment_read`. **Removal's operation member is absent from the current w6 freeze: §13 Q8 is an explicit publisher-owned boundary request, not permission to add a second enum or mislabel removal.** | Record shape: **w6 §6.1 only**, Wave A. W1/W2 sub-fields: Wave C. Envelope/emitters: Wave D, **must exist before Wave E**; removal cannot emit/go green before Q8 is resolved by w6. |
| **Gateway raw-error redaction** (register row 19) | W3 safe error responses; w6 marker scan | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/gateway/rest_mail.go::mailErr502` and owned Mail logging sites emit the existing closed safe class plus approved operational fields, never raw provider error text. Preserve summary's class-only wire. The watcher-file correction is **W1's**, consumed below. | Policy: Wave A, US-7/MC-15. Gateway fix: Wave D; watcher prerequisite: Wave C. |
| **Gateway feature adapters — single Mail-route writer** | W3 and w4-features | Consume W0 generated request/results, W2 metadata/search/part/reference services and w4-features' shared Transfer, sanitizer and recipient helper. Dedicated token-bound preview bytes: inline, `no-store`, **25 MiB actual-decoded cap before success**. Browser Download: separate attachment-disposition streaming endpoint, including larger files. Only explicit Save uses the shared service's workspace writer. No Library-file edit by this owner and no second service. | Wire/source/reader freezes: Waves A/B/C. This package's adapters: Wave D. Payload removal and preview endpoint split are atomic. |
| **Windows permissions evidence path** (register row 23) | w2 cache tests; w6 proof gate | This package owns the Windows CI workflow extension that **executes** the cache/ACL tests under the required pure-Go/build-tag settings and records named tests/exit codes, not compile-only evidence. Fallback: actual Windows UAT execution with a signed receipt. Native ACL assertions and a denied-access control are required; Unix modes never stand in for Windows proof. | Obligation: Wave A, this row/§6. Workflow integration: Wave D; executed evidence: Wave E. |

### 7.2 This package CONSUMES

| Interface | Register's single publisher and freeze | Consumption contract — no local substitute |
|---|---|---|
| **All generated REST/WS/persisted-JSON boundary types** (rows 1–8, 22) | **Backend-lead as ADR-W0**, Wave B; queue = §8 plus sibling queues and the register. No W0 spec exists or is required. | Use generated Go/TS/Zod artifacts only after schema/regeneration merge and `verify-contracts` green. A missing field is a boundary stop/request under register R-3, never a typed stub or copied response. |
| **`MailReadMetadata` and role fields** (rows 2, 3) | **W0 defines**; **W2 is the sole value producer**. Wave B schemas; W2 §4.1 internal freeze. | Attach values without inventing freshness. Exactly three roles; availability `present|absent|unknown`; nullable version/count; mapping source **`override|special_use|fallback|saved|none`**. In Phase 1 `encrypted_disk` describes **folder mapping only**, never headers or counts; headers/counts use `live|memory|none`. Separate mapping/count/page metadata and timestamps. |
| **Paging/search/stale-409** (row 4) | **W0 publishes wire shapes; W2 implements SEARCH and cursor issuance in `view.go`**, frozen in its §4.1/§3.13, built Wave C. | §8 requests `search`, `cursor`, `next_cursor`, `has_more`, `view_limit_reached` and the typed stale-cursor/reference 409. Gateway forwards the generated inputs/results, never issues a second cursor or searches locally. Search is folder-scoped server-side **subject plus sender/recipient (SUBJECT/FROM/TO) substring**, same **25/+25/200** bounds. |
| **REST `observer_id` and presence frames** (rows 5, 6) | **W0 schemas them**, Wave B; **W3 publishes per-panel lifecycle hooks**, row 20. | Both folder and list GETs accept the optional opaque `observer_id`; gateway validates existing acknowledged presence independently of mailbox authorization. W3 opens/closes observers on mount/unmount, workspace exit, pagehide/logout and socket death; the server does not rely on UI promises. |
| **Pool/lease and session-source injection** (row 9) | **W1**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/mail-live-access-w1-read-runtime-spec.md::3.1 W1 publishes` | `MailSessions.Acquire(ctx, LeaseRequest) (Lease,error)` / `Lease.Release()`, exclusive selected state, returned epoch/generation, reservation counting and typed pool errors. Inject the one instance at every construction site; no private manager or duplicate account-slot acquire. |
| **Revision capture/compare accessor** (row 10) | **W1's `RevisionSource`**, W1 §3.1 freeze | Implement the counter behind that accessor; hand the one captured value to W2's `HeaderCache.Put`/`PutCounts` and `FolderSnapshotStore.Save`. Never recapture after server work or publish under the new revision an old read did not observe. |
| **Presence registry** (row 11) | **W1's `PanelPresence` in `pool.go`**, W1 §3.1 freeze | Bind authenticated connection lifecycle to its existing Bind/Unbind/UnbindAll/Count API; W1 decides retention. This package publishes the adapter only, not a registry W1 would consume. |
| **Discovery, encrypted snapshots and memory header/count cache** | **W2**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/mail-live-access-w2-discovery-and-cache-spec.md::4.1 PUBLISHED by W2` | Consume `Discovery.Resolve`, `FolderSnapshotStore.Load/Save/Delete`, `HeaderCache.Get/Put/GetCounts/PutCounts/InvalidateFolder/MarkPairRemoved/PanelOpened/PanelClosed`. Supply credential-derived keys, opaque identity, captured revision and the single gate decision; W2 owns envelope crypto and compare-before-publication. |
| **Watcher dirty mark** (row 13) | **W1**, W1 §3.1 | Deliver invalidation/dirty notification to W2 without refreshing closed-panel mappings, counts or headers. A mark is not a live read and does not advance last-success time. |
| **Targeted single-part reader** (row 14) | **W2**, W2 §4.1: `PartReader.Fetch(ctx, lease, ref, partIndex, sink) error` | Stable MIME leaf index, nested-part resolution, dedicated draft-body-marker exclusion, same validated lease and **part-specific PEEK only**. No unrelated attachment/body fetch, byte cache or whole-message fallback. Preview/read/Save/Download wait for this dependency. |
| **MIME attachment classifier** (row 15) | **W2**, W2 §4.1: `ClassifyStructure(structure) (descriptors, hasAttachments)` | One walker feeds list/detail/agent metadata. Inline-only body CID and dedicated draft marker do not count; genuine user `message.md` and unavailable attachment do. Metadata classification failure is visible, never fabricated `false`; gateway maps results without rewalking MIME. |
| **Same-lease reference validation** (row 16) | **W1 publishes lease epoch/generation capability; W2 publishes validation in `view.go`** | Pass this package's issued reference unchanged. W2 compares its bindings against the **same selected lease before fetch/mutation** and returns W0's typed stale result. Issuance remains this package's §7.1 obligation, not a consumed-but-unowned task. |
| **Instrument record shape and pool/cache sub-fields** (row 17) | **w6 alone freezes the record** in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/specs/mail-live-access-w6-proof-spec.md::6.1 The instrument, specified first`; **W1 supplies pool fields, W2 cache fields** | Consume `operation`, `pair_ref`, `source`, `hit`, `duration_ms`, `acquire_wait_ms`, `socket_count`, `outcome`, optional `rows`/`revision`/`shared_flight`, through its injected sink. This package joins/emits, not redefines. Closed-enum gap follows R-3/§13 Q8; no measurement green without the emitter. |
| **Watcher raw-error redaction** (row 19) | **W1**, its exclusive `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/watcher.go::recordFailure`; W1 §4.12, Wave C | Persisted text is empty or class-derived, never raw provider text. This package's US-7.3 and end-to-end marker scan bind to that implementation; neither W2 nor this owner patches W1's file or builds a second redaction helper. |
| **Counts/header refresh decision** (row 21) | **W2**, W2 §3.9/§4.1, Wave C; W3 supplies eligible events | Open/folder switch refreshes live only when data is absent, dirty or **older than five minutes**; fresh data produces no live refresh. Manual Refresh always; confirmed own-action refresh unchanged. No repeating panel timer, watcher-driven fill or unconditional reopen dial. Mapping uses its separate >24-hour/explicit invalidation rules. |
| **Transfer/Save/reconciliation, parsed CSS and recipient helper** (row 22 and ADR feature owners) | **w4-features**: ADR-W7 Transfer/receipt service, ADR-W9 sanitizer, ADR-W10 `BuildReplyRecipients`/agent adapters; W0 owns their boundary shapes | Wire gateway adapters to these single services. Same-token explicit Save retry uses the bounded prior receipt, never automatic replay. Temporary preview alone uses mail-restricted resource authority. Saved files use ordinary workspace rules; saved HTML retains original bytes, mail-derived provenance, scripts off by default and a visible per-file checkbox. No duplicate Transfer, CSS policy or recipient algorithm. |
| **Encryption/key lifecycle primitives** | Existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/credentials/store.go::Store.DeriveSubkey`, `Store.Close`; **Credential Boot Contract** | Supply purpose-separated keys to W2, reject locked/broken store distinctly, delete disposable cache on rotation without replacing an existing master key. No credential-material generation fingerprint or credential algorithm edit. |

**Landing/freeze gates:** Wave A lands these corrections and the sibling freezes; **backend-lead's W0 role** runs Wave B schemas/regeneration next. Non-wire work may use the published internal freezes, but **no wire-consuming code or wire-asserting green precedes merged generated artifacts**. W1/W2 land in Wave C, then this single integration writer lands Wave D. Wave E starts only after the production emitter and real Windows evidence path exist. Missing publisher fields are reported to team-lead under register R-3; they are never worked around with a stub or a second implementation.

---

## 8. Contract-first requirement queue — W0 publishes, this package consumes

**Correction, 2026-10-02 (grill-3 C-2; grill-4 C2/m4).** This is the integration package's contribution to the **Wave B union queue**, not a contract-file ownership claim and not a request to a nonexistent W0 spec. **Backend-lead in the ADR-W0 role** is the single publisher/writer of every schema and generated artifact. Wave A freezes the queue below together with W3's queue, w4-features' queue and the ADR's wire tables; Wave B must cover their union before any dependent handler or wire-asserting green. The procedure is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/CLAUDE.md::Contract regeneration`: schema → reference → `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/scripts/gen-contracts.sh` → atomic schema/generated commit → generated-only consumer. **No contract file is edited by this spec correction.**

| Order | Shape requested — single publisher: W0 | Boundary and consumers | Freeze / implementation owner |
|---|---|---|---|
| 1 | **`MailReadMetadata`**: `source=live|memory|encrypted_disk|none`; nullable RFC-3339 `last_validated_at`; Boolean `stale`/`refresh_needed`; nullable closed safe `notice_code` including `cache_unavailable`; nullable opaque `publication_revision`. Mapping, counts and page headers have **separate** metadata. **Phase 1:** `encrypted_disk` is valid for folder mapping only; header/list and count metadata use `live|memory|none`. Disk headers remain Phase 2. Missing validation time means unknown/stale; reads/failures never advance it. | REST folder/list results: `MailFolder.mapping_metadata`/`count_metadata`, appropriate `MailFolderList` and `MailMessagePage` metadata; W3 labels, w6 hit/source proofs | Wave B. **W2 produces values**; w5-integration attaches the captured/current revision and notice without inventing a fresh timestamp. |
| 2 | **Both folder and list GETs** accept `mode=cache_first|live` (omitted = **live**) and optional opaque **`observer_id`**. Folder GET also accepts Boolean `refresh_mapping` (normal counts refresh does not force discovery). Existing human `retry=true` remains distinct; an automatic stale refresh never sets it. | REST params consumed by the two gateway handlers and W3's API adapter. Acknowledged, authorized observer permits panel retention; absent/unknown/foreign/stale observer degrades to request-scoped work. `mode=live` never returns a cache hit as a successful refresh. | Wave B, same freeze as presence frames; w5 validates association, W1 supplies retention capability, W3 supplies the observer lifecycle. |
| 3 | **Presence frames**: client `type=mail_panel_observer`, `action=open|close`, `observer_id`, `workspace_id`; generated server ack/error bound to the same action/observer/workspace and authenticated connection; corresponding `WsFrameType` entries and inbound schema names. No client connection/session identity can grant retention or mailbox access. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/contracts/asyncapi.yaml::channels.chat`; dispatch/schema mapping and teardown adapter; W3 acknowledgment gating | Wave B; w5 handles frames and consumes **W1's one registry**, not a second implementation. |
| 4 | **Folder role state**: exactly Inbox/Sent/Drafts; `availability=present|absent|unknown`; nullable resolved name/`uidvalidity`; nullable `total` (unknown = null, confirmed absent optional role = 0); nullable unread where not applicable; **`mapping_source=override|special_use|fallback|saved|none`**. The register's five values supersede the ADR's earlier four-value proposal. Required nullable effective `date` and `has_attachments` additions are row 9. | Generated `MailFolder`/`MailFolderList`; rail display and nullable-total schema oracle (`TestMailFolders_UnknownCountIsNull`) | Wave B; **W2 is the only role/count value producer**, w5 maps without converting unknown to zero. |
| 5 | **Paging/search request and page response**: list `limit` default/max **25**, optional opaque `cursor`, optional `search`; response `next_cursor` nullable opaque string, Boolean `has_more` and `view_limit_reached`, rows + metadata. **Load more adds 25; hard ceiling 200 per folder per browse/search view**; at the ceiling no next cursor and the visible search instruction. A new search starts its own bounded sequence. Matching is server-side **SUBJECT or FROM or TO substring** (subject plus sender/recipient); no body/offline/local-index search. Cursor binds pair/config generation, folder/UIDVALIDITY, normalized query and already-delivered row count. Replace the panel's old numeric `before_uid`/`next_before_uid` paging atomically with its generated consumer; do not silently redesign agent list limits. | REST list operation and `MailMessagePage`; W3 US-3, w6 25/200/search campaign | Wave B shapes; **W2 alone implements IMAP SEARCH and cursor issuance in `view.go`, Wave C**; w5 forwards only. |
| 6 | **Typed HTTP 409 stale result**: one generated closed error object with safe `error` string and **`code=stale_cursor|stale_reference`**. Cursor mismatch requests one view reset; reference mismatch reports **“This message changed or was deleted. Refresh the list.”** No silent reinterpretation, infinite reset loop or mutation replay. Wrong-pair requests still undergo independent authorization; no token authenticates a foreign mailbox. | List/detail/seen/attachment read/save/preview error responses; W3 branches on `code`, not text; w6 negative tests | Wave B; W2 returns the stale condition using W1's **same selected lease before fetch/mutation**; w5 maps it without a parallel type. |
| 7 | **`MailboxRemovalResult` / cleanup request**: distinguish `removed|removed_cleanup_pending`, nullable safe cleanup code and opaque cleanup intent for separately authorized **Retry cleanup**. It works after the mailbox config row is gone. The mailbox result and agent/workspace cascade's mail-cleanup result explicitly account for **cache and watcher-state purge**, without changing unrelated deletion semantics. | Mailbox disable/removal, agent/workspace deletion's mail portion and authorized cleanup endpoint; W3 truthful status | Wave B, consumed in Wave D. w5 owns cascade/intent; W1 quiesces runtime work; W2 deletes cache. |
| 8 | **Attachment preview + byte-path split**, atomically: generated `MailAttachmentPreviewRequest` carries authorized workspace/agent/folder, `message_ref`, stable `part_index`, optional observer association. Response: `kind=mail_attachment`, opaque `preview_id`, display subject, descriptor, `text_readable`, `read_only=true`, metadata-only `content_source` (`byte_url`, nullable `isolated_html_url`, token, expiry). `byte_url` names a dedicated grant/part-bound **preview-purpose** endpoint: inline disposition, `no-store`, **25 MiB actual-decoded cap before success**. Existing `getMailAttachment` stays the separate **browser-Download stream**, attachment disposition and original bytes, including larger files. Size/decode failures discovered in transfer never create a success/complete preview. Message HTML grant payload fields are removed; serve-time proxy URL metadata can remain, never bytes. | REST mint/revoke, preview bytes/isolated HTML and Download descriptions; temporary Library source consumes generated preview response, never a fake `LibraryEntry.path` | Wave B is **one atomic payload-removal/endpoint-split step**. w5 owns Mail endpoints/tokens/headers; **W2 supplies part-specific PEEK**; w4 supplies the service/rendering adapters. No whole-message fallback. |
| 9 | **Shared list/read metadata**: required opaque **`message_ref`** on list/detail and attachment-descriptor-bearing agent results, including messages without Message-ID; required Boolean **`has_attachments`** from the common classifier; **required nullable RFC-3339 `date`** with Date → server received/internal date → null; attachment descriptor `filename`, `content_type`, stable `part_index`, **nullable decoded `size_bytes`**, optional labelled `reported_size_bytes`. Unknown decoded size is never 0 or encoded octets disguised as decoded bytes. | `MailMessageSummary`, `MailMessage`, `MailAttachment` and generated tool results crossing the SPA boundary | Wave B; **w5 issues refs**, **W2 supplies the MIME classifier/metadata**, W1/W2 normalize dates, W3 displays paperclip/**No date**. Metadata is not permission to fetch bodies. |
| 10 | **Save and explicit-retry receipt**: generated `MailAttachmentSaveRequest` has optional observer association and opaque `save_operation_token`, no arbitrary path/overwrite/body upload. Source identity is route-bound. Response echoes the token and includes `saved=true`, workspace ID, **real `entry: LibraryEntry`**, workspace-relative `path`, resolved **`absolute_path`**, actual `size_bytes`, `audit_status=recorded|disabled|failed`, nullable safe `warning_code`. Same-token explicit retry resolves the prior receipt within the service's bounded window; restart/window loss is surfaced as a new attempt, never an invented prior receipt or a universal no-duplicate promise. | Save-to-Library subresource; w4's Save service and W3's unknown-result/retry/Open-in-Library flow | Wave B shapes; **w4-features (ADR-W7) implements Transfer and bounded receipt lookup**, w5 adapts the Mail route. Original saved bytes and 25 MiB limit unchanged. |
| 11 | **Reply context and reader/tool results**: generated reply request `mode=reply|reply_all`; response `to[]`, `cc[]`, `bcc[]` (empty by default), subject, quoted safe `body_markdown`, nullable `in_reply_to`. Attachment list/read/save tool-result schemas use the same refs/descriptors/receipt. Read representation is typed `text|image|document|unsupported`, with nullable text/transient reader representation and safe unsupported notice; its exact existing reader integration must be traced by w4/W0 before freezing that variant, never arbitrary JSON/base64-as-transcript. | Gateway reply-context operation and tool-result JSON read by the SPA | Wave B, **w4's shared `BuildReplyRecipients`/reader adapters**; w5 wires without a second recipient or reader implementation. |
| 12 | **Real saved-file profile and internal identity persistence**: `LibraryEntry` remains real-path-only; its generated preview profile/per-file scripts-allowance fields record the already-settled mail-HTML scripts default. Saved files are ordinary workspace files; **temporary mail resource authority is not applied to saved non-HTML files**. Saved HTML keeps original bytes, a non-byte mail-derived marker, scripts off by default and visible per-file checkbox, surviving supported move/copy/rename/restore. Persisted pair ID/config epoch from §7.1 are internal scalar identity fields; **any serialized copy read by the SPA is defined by W0 first**, not an ad-hoc config wire field. | Library response/persistence and any affected config boundary; this package consumes only its identity generation and w4's marker service | Wave B boundary freeze; **w4/ADR-W7–W8 owns Library marker/allowance storage and viewer policy**, w5 owns pair ID/config epoch persistence/injection. Marker/provenance proof cannot be assumed. |
| 13 | **`MailUnavailableError.reason`**: `pool_busy|account_busy|server_connection_limit|backoff`, preserving the existing busy/backoff 503 envelope, safe class and `next_attempt_at`. Retry bypasses **only** backoff, never capacity/security; an unrecognized server string is not fabricated into a precise connection-limit cause. | Folder/list/detail/attachment/preview 503 responses; W3 distinct safe status and explicit Retry, w6 refusal proofs | Wave B; W1 supplies distinct internal pool/account errors; w5 maps them through W0's generated type. |

All REST rows reference `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/contracts/openapi.yaml` and shared schemas under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/contracts/components/schemas/`; **W0 alone** edits/regenerates them. Generated Go artifacts live under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/api/generated/`, TypeScript/Zod under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/src/lib/api/generated/`. `make verify-contracts` detects drift; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/scripts/check-no-handwritten-wire-types.sh` detects hand-written parallels. These gates are future obligations, **not executed by this correction**.

**Instrument step — deliberately not a wire row:** w6 §6.1 publishes the sole internal record/sink shape. Wave C supplies W1/W2 fields; **this package's Wave D emitters** join them and cover gateway-only operations before Wave E. Nothing here asks W0 to invent an instrument schema or lets the tests-only proof package implement production emitters. The unresolved removal operation member follows §13 Q8/R-3.

**Tool scope and reachability:** this package adds no builtin tool names; w4-features owns the new list/read/download tools and their literal allow/allow/ask defaults, ordinary Auto behavior and registration proof. This package wires the existing and new permitted tools to the same runtime. Neither an empty registry grep nor generated types alone prove the resulting feature reachable: executed user/agent journeys are required (§10).

**Sequencing:** the full Wave B union is authoritative; no package treats rows 1–3 alone as the whole panel contract. W2's search/cursor, part reader and classifier land before dependent gateways/features go green. W3 consumes the generated params/409/page fields and validated presence; preview payload removal and endpoint split land together. If any field is missing, stop at the publisher boundary under register R-3 — never stub.

---

## 9. Explicit non-goals

| Not in this package | Reason / owner |
|---|---|
| Pool/lease internals, coalescing identity, watcher scheduling | W1's implementation; this package wires and consumes |
| Envelope crypto, folder discovery, header-cache internals | W2; security-lead reviews the crypto |
| Any SPA file | W3 exclusively |
| Every `contracts/` and generated-file edit | W0 exclusively; this package requests and consumes |
| Attachment Transfer service, Library storage, saved-HTML provenance | W7 |
| Agent attachment tools, policy catalog, `BuildReplyRecipients` | W10 |
| Phase 2 JMAP transport, trusted-origin probing, on-disk header snapshots | W6, later wave — this package's location/gate/cascade design is Phase-2-ready but nothing Phase 2 ships here |
| The data-dir ignore rule's *provisioning-script edit and machine re-run* | team-lead/founder ops (deployment); this package specifies, gates on, and verifies it |
| Fixing the blocked backup job (30 gitleaks findings, unloaded launchd service) | Reported as a note by the trace; a human-runbook task outside this feature |
| Remediation of already-tracked cache files (should any exist at rollout) | The runbook (US-4.3) documents it; execution is ops, history rewrite is a founder decision |
| Any measurement run | The measurement plan's runs belong to the UAT/proof lanes; this package only guarantees the counters exist (US-6.5, MC-12) |
| Credential-store, shell-policy or backup-model overhauls | ADR non-goal: only necessary Mail cache isolation lands here |

---

## 10. Definition of Done

**Code correct and tested** — requires ALL of:
- Every §6 test written from this spec (RED first), passing (GREEN), with red-before-green receipts on the implementation branch (tests-only CI proof, or the single dispatcher-owned narrow local run the root rules permit), then an independent CHECK/mutation audit (the X-1…X-10 counterexamples each demonstrated to kill their mutation).
- `make verify-contracts` green with the §8 artifacts regenerated in the same commits as their spec changes; no hand-edited generated files.
- `make lint-budgets`, `make lint-guards` and the full CI gate green (remote cluster per the root rules); the four fixed+architect+security 5-reviewer gate clean on the feature branch, and the whole-epic gate before `→ main`.
- The truthful-outcome rule proven: at least one test (B-18) drives a real unlink failure and asserts the non-success outcome; the removal paths never return unconditional success.

**Reachable by a user/agent** — requires ALL of:
- An executed UI journey: real Mail panel open produces the presence ack; panel close/socket death closes retained sockets (server-side counter evidence, not assertion-only); cached-state and `cache_unavailable` notice visible in the SPA when the gate refuses.
- An executed agent journey: an allowed agent's mail tool call resolves the shared runtime (same-instance evidence); a file-tool attempt on `mail-cache/` is refused (MC-16 executed, not just written).
- An executed privacy journey: mint → serve → logout on a real preview; token store inspection shows zero bytes; the marker-scan test executed against a marker-talking fake server.
- The deployment receipt: US-4's E1–E5 executed **fresh on the target machine** (after the job is reloaded and unblocked), cited by path and date in the delivery report — the trace's snapshot is motivation, never proof.
- Hard Constraint #6 honoured negatively (no new tools — §8 step 7) and positively for reachability: the feature is reachable through the existing registered mail surfaces (panel + existing tools), with the executed journeys above as evidence — registration/policy checks alone are not reachability.
- User-facing documentation updates below landed and audited by `docs-verifier` against the actual behaviour in the same change.

---

## 11. User-facing documentation TODOs

The ADR assigns these three pages to this package's owner (W4's drafting duty); `docs-verifier` audits each against actual behaviour before landing.

| Page | Exact sections and what must be said |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/connectors.md` | In the mailbox settings/removal guidance: the Sent/Drafts override and Automatic-clear semantics (omitted keeps, empty clears, non-empty overrides — the existing behaviour, now user-visible in discovery states); what happens on removal — the new **cleanup-pending** outcome and its **Retry cleanup** action, including that it works after the mailbox entry is gone; what a pair move does to cached data. Do not promise a disk cache the Phase 1 gate may refuse; state that mail metadata caching activates only when the installation's data-folder privacy exclusion is confirmed, and what the visible cache-unavailable notice means. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/security.md` | In the mail section: precisely what is stored on disk (encrypted folder names/roles/version per mailbox — no subjects, addresses, bodies or attachments), where (the private cache directory under the data dir), the key dependency (derived from the credential store; losing it loses only the cache, never server mail), expiry/removal (removed with the mailbox/agent/workspace; rebuilt live), and that the directory is excluded from the data-folder Git backup and from app backups/restores — with the honest limit that a *pre-exclusion* captured copy in Git history or old archives is not removable by the app. State that HTML previews keep no message content server-side between requests (metadata-only grants) and that preview tokens die at logout/close/expiry. Distinguish saved attachments (normal user files) from the disposable encrypted cache. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/troubleshooting.md` | New Mail entries: "Mail is busy" vs backoff vs server connection limit (and that Retry bypasses only backoff); cache unavailable / cache warning states (what `cache_unavailable` means, that live access continues); "removal left cleanup pending" — what it means and that Retry cleanup is safe to run; preview fails to load after mailbox changes (the metadata-only re-fetch behaviour); and a support-hygiene note: never paste logs or state files into support channels (they contain opaque identifiers, not message content, by design — say both halves truthfully). |

---

## 12. Traceability matrix

| Requirement (story) | BDD scenarios | Tests (§6) | Machine constraints |
|---|---|---|---|
| US-1 shared runtime | B-1…B-4 | 1–5 | MC-1, MC-2, MC-3 |
| US-2 presence | B-5…B-8 | 6–9 | MC-4 |
| US-3 cache placement + gate | B-9…B-13 | 10–13 | MC-5, MC-6, MC-16 |
| US-4 deployment exclusion | B-14…B-16 | 14–16 | MC-7 + E1–E5 receipt |
| US-5 removal cascades | B-17…B-21 | 17–20 | MC-8, MC-9 |
| US-8 durable generation | B-22, B-23 | 21, 22 | MC-10 (+ MC-17 design rule) |
| US-6 metadata-only previews | B-24…B-29 | 23–28 | MC-11…MC-14 |
| US-7 redaction | B-30, B-31 | 29, 30 | MC-15 |
| Cross-cutting (mutations) | — | 31 (X-1…X-10 embedded across) | — |

Every story traces to at least one scenario, every scenario to at least one test, every test family to at least one machine-verifiable constraint. No gap row remains.

---

## 13. Open questions (options and recommendation)

| # | Question | Options | Recommendation |
|---|---|---|---|
| Q1 | Who owns the data-dir ignore rule long-term — can a shipped feature depend on a separate machine-deployment repo for a correctness property? (trace §"decision or deployment change") | **(a)** deployment keeps ownership; this spec's prerequisite + product gate documented · **(b)** product writes/repairs an ignore fragment on boot · **(c)** move the cache outside the data dir (OS cache dir, or under `workspaces/<id>/` where deletion cascades) | **(a) + the product-side gate (US-3.3)** for v0.1.1. (b) makes the product fight the provisioning script's idempotent refresh over file ownership; (c) trades one problem for two (the workspace option still gets tar-captured — `createTarGz` does not skip `workspaces/*/work/` — and the OS-cache option breaks the co-located removal story). The gate makes (a) safe: without the rule the product simply refuses disk writes. Revisit (b) only if multi-machine drift proves real. |
| Q2 | Does `email-watch/` (watcher state: UIDs, counts, classes — and raw error text until W1's redaction lands) get the same exclusion + a deletion story? | **(a)** add `email-watch/` to the same ignore rule and give it a purge on pair removal · **(b)** leave it; document as accepted exposure | **(a)** — it is the identical structural hole the trace found (un-ignored, never deleted, Git-captured), the fix is one more ignore line plus a purge call beside the cache purge, and it currently carries *worse* content (raw error text) than the encrypted cache. Founder confirms because it changes what gets backed up. |
| Q3 | Should `extractTarGz` also skip `mail-cache` members defensively, even though fixed archives never contain them? | **(a)** skip defensively · **(b)** rely on archive hygiene alone | **(a)** — one string compare; it covers archives created before the exclusion lands (US-4.3's tracked-file window) and makes restore correct independent of archive provenance. |
| Q4 | If an install's cache files were already committed before the ignore rule landed, who executes remediation, and is the backup remote's history rewritten? | **(a)** runbook only (`git rm -r --cached` + commit per install); history untouched · **(b)** runbook + history rewrite/force-push of the backup remote | **(a)** for v0.1.1 with **(b) reserved for evidenced exposure**: the cache is ciphertext (high-entropy, key never in the repo), so exposure is limited; a history rewrite of a backup remote is disruptive and needs its own founder call. The runbook (US-4.3) must exist regardless. |
| Q5 | Should the preview mint dial once to validate the reference (fail fast at mint) or dial nothing? | **(a)** no-dial mint; failures surface at first serve · **(b)** validating mint (one metadata fetch at mint) | **(a)** — a validating mint doubles fetches for the common mint→serve path, reintroduces eager network work the design removed, and still cannot guarantee the serve-time fetch succeeds; the SPA already renders per-request states. Cost: the mint→error latency moves from mint to first serve — acceptable and measured (MC-12 counters). |
| Q6 | Wave-1 preview part fetches: W2's targeted part reader, or whole-message PEEK fallback? | **(a)** wait for W2's targeted reader · **(b)** ship whole-message PEEK fallback, switch when W2 lands, keep counters | **(b)** — the byte cap and budget bounds make the fallback safe, the design's fetch-count measurement exposes its cost honestly, and blocking preview work on W2's schedule buys nothing for privacy (both shapes are request-only). |
| Q7 | `releaseRuntimeResources` purge failures during workspace deletion: surface in the workspace-delete response, or log-only like today's cascade? | **(a)** extend the workspace delete response with the mail cleanup-pending discrimination · **(b)** best-effort log-only for the workspace path (truthful outcome only on the mailbox route) | **(a)** — the truthful-outcome rule (US-5.3) should not have a hole shaped "whenever it happened during a workspace delete"; the W0 shape (step 5) covers both routes at once. Flag: it touches a high-blast-radius handler — the 5-reviewer gate covers it. |

---

## 14. Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Preview grants carry payload bytes for 15 min | `pkg/gateway/mail_preview_token.go::mailPreviewGrant` (fields `HTML string`, `Inline []mailPreviewInline`, `RemoteURLs []string`), `MailPreviewTokenTTL = 15 * time.Minute`, `MailPreviewMaxLiveTokensPerSession = 8`, `mint`/`lookup`/`invalidateSession` read in this checkout | Verified |
| The mint fetches outside the A8 budget | `pkg/gateway/rest_mail_preview.go::handleMint` — `client.ReadView` directly; no `mailBudgetWrap` on the `/mail-preview/` POST (the wrapped four GETs are `rest_mail_budget.go`'s documented set) | Verified |
| The flight key lacks pair/generation; flights detach | `pkg/email/mail_budget.go::MailBudgetRequest.flightKey` (`Account + "\x00" + Operation + "\x00" + json(Params)`), `call` (`DoChan` + `flightContext`/`context.WithoutCancel`) | Verified |
| Budget is shared process-wide by state-dir key | `pkg/email/mail_budget.go::SharedMailBudget`; injection `pkg/gateway/gateway.go::initializeAgentLoop` (`agent.SetSharedMailBudget(email.SharedMailBudget(rc.homePath))` before `NewAgentLoop`); watcher `pkg/gateway/gateway_boot.go` (`email.NewMailboxWatcherSet(provider, stg.homePath, email.SharedMailBudget(stg.homePath))`); tools `pkg/agent/email_tools.go::registerEmailToolsForAgent` + `SetSharedMailBudget` | Verified |
| Every REST request builds a fresh client | `pkg/gateway/rest_mail.go::mailPairClient` — `email.NewClient(email.Account{…})` per call | Verified |
| Socket authorization / liveness / teardown hooks | `pkg/gateway/websocket.go::WSHandler.authenticateWS` (cookie via `middleware.ResolveUserFromCookie`, else `AuthFrame` via `resolveBearerIdentity`); `wsPingPeriod` 30 s / `wsPongWait` 60 s + pong-handler and readLoop re-arming; teardown = the deferred block in `WSHandler.ServeHTTP` (`UnsubscribeEvents`, session-map deletes, `unbindConnHubLocked`, `wc.close()`); frame dispatch `wsHandlerReadLoop.dispatchFrame` + `wsFrameSchemaName` + `ValidateInboundFrameJSON` (gated `Gateway.ValidateInbound`) | Verified |
| Tar backup/restore exclusion gaps | `pkg/gateway/rest_settings.go::createTarGz` (`topLevel == "logs" || topLevel == "backups"` → `filepath.SkipDir`); `extractTarGz` (skips only `config.json`; `maxRestoreFileSize` 256 MB) | Verified |
| Deletion paths touch no cache/pool state; success can be unconditional | `pkg/gateway/rest_mailbox.go::deleteAgentMailbox` (config entry + `removeStoredCredential` ×2 + `triggerReloadAndWaitOutcome`, failures `logsafeError`, then `jsonOK(gen.OperationResult{Success: true})`); `pkg/gateway/rest_agents.go::deleteAgent` (`DeleteState`, reload); `pkg/gateway/rest_workspaces.go::releaseRuntimeResources` → `removeMailboxesForWorkspace` (config+credential only); `removeAllFn(wsDir)` wipe cannot reach `mail-cache/` (outside `workspaces/<id>/`) | Verified |
| The disable→enable transition signal exists (mirror for disable) | `pkg/gateway/rest_mailbox.go::restAPISetAgentMailbox.persistConfig` (`wasEnabled` captured before overwrite; overrides kept-omitted/cleared-empty/nonempty) | Verified |
| Raw error text seams | `pkg/email/watcher.go::recordFailure(errClass, errText string)` persists `LastErrorText` (state at `<StateDir>/email-watch/<pair>.json`, written 0600 atomic, never deleted — no `Remove` calls in `watcher.go`/`watcher_set.go`); wire is class-only (`pkg/gateway/rest_mail_summary.go::handleMailSummary` carries `LastErrorClass`; `MailboxNewMailSummary.yaml` requires `last_error_class`, no text field); `pkg/gateway/rest_mail.go::mailErr502` logs the raw error | Verified |
| Atomic write primitive exists | `pkg/fileutil/file.go::WriteFileAtomic` | Verified |
| Derivation seam and its limits | `pkg/credentials/store.go::Store.DeriveSubkey` (32-byte purpose key; `ErrStoreLocked`), `Store.Close` (no erasure guarantee) | Verified |
| 25 MiB cap, part selection, seen split | `pkg/email/view.go::maxViewPartBytes = 25 << 20`, `SanitizeAttachmentName`; `pkg/gateway/rest_mail_read.go::handleMailAttachment` (whole `ReadView`, `mailPartByStableIndex`, 413 on `DataUnavailable`, `applyMailByteHeaders`); `handleMailSeen` (discards `ResolveRef`'s epoch, `MarkSeenIn` opens its own session) | Verified |
| MailboxConfig identity fields | `pkg/config/config.go::MailboxConfig` (Enabled, WorkspaceID, hosts/ports, Username, PasswordRef, SentFolderName, DraftsFolderName) | Verified |
| Contract anchors | `contracts/openapi.yaml` operationId `getMailAttachment`; `contracts/asyncapi.yaml` `channels.chat` + `WsFrameType` enum | Verified |
| Auto-commit job is external; staging is `git add -A`; ignore file is a deny-list; job unloaded/blocked; tracked-file mechanism | `receipts/autocommit-trace.md` (launchd `com.omnipus.state-autocommit`, runner `/Users/danielpiatkowski/.local/bin/omnipus-state-autocommit`, generator scripts 35/36 of omnipus-agent-os, live `.gitignore` contents, `check-ignore` probes, 30-findings block, 22,239 pending) | Verified as a receipt — re-proven fresh by US-4's E1–E5 before any disk write ships |
| Design authority and corrections applied | ADR-20261001 (read in full, 807 lines, this checkout @ `fc4c8bf6dcdfa257e3540f602f042dfa19882ce1`); `adr-grill-report.md` (read in full; I-01/I-02/I-05/I-06/M-01/M-02 dispositions carried into §3–§8; I-03/I-04 belong to W10/W8–W3 respectively) | Verified |
| Impact assessment basis | GitNexus MCP tools not exposed in this session; §2.4 built from direct reads and the grep sweeps shown above — labelled Inferred there | Inferred |
| **Self-check** | Re-read this spec end-to-end against the dispatch's nine mandatory contents (context ✓ stories ✓ BDD ✓ TDD ✓ non-goals ✓ DoD ✓ docs TODOs ✓ counterexamples incl. mutations X-1…X-10 ✓ open questions ✓) and the publishes/consumes requirement (§7) ✓. Every cited symbol was read in this checkout during this task; the one external-system claim (auto-commit job) rests on the named trace receipt and is explicitly gated on fresh evidence (US-4.5, E1–E5). No test, build or benchmark was run; no production, contract or test file was touched — `git diff` scope checked before each commit showed only this spec file. Commit series: one per major section, authored `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>`, no co-author trailers. | Verified artifact/scope check |

skills: omnipus-shared-rules, plan-spec
