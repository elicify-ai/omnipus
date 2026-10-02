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
