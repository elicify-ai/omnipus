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

**Bottom line.** w5-integration is the **single gateway/agent Mail integration writer**, consuming W1's shared pool/budget/presence and W2's cache/search/part/reference services. It makes the founder's **2-per-mailbox / 8-global** socket limits hold across panel, watcher and tools. **The product itself establishes staging and application backup/archive exclusion for private Mail cache and watcher state on every install**, before sensitive writes; genuine failure is visible and cannot be excused by absent external setup. Removal purges both state targets honestly, blocks late resurrection and leaves user-saved Library files alone. This package also issues first-phase message references, owns durable pair/config identity and revision advancement, replaces payload-bearing message-preview grants with metadata-only live serving, fixes gateway raw-error logging and emits the **w6-frozen** request-scoped instrument. No production/test result or performance gain is claimed by this specification.

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

**Correction: this spec located the shared-budget setter inside `initializeAgentLoop`; wrong because the source places it in `loadConfigAndProvider`, which completes before `initializeAgentLoop` calls `NewAgentLoop`; correct is to extend the earlier injection site, preserving that order.** Dated 2026-10-02; evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/gateway/gateway.go::runContextWithOptions.loadConfigAndProvider`, `runContextWithOptions.initializeAgentLoop`, read in this correction. This corrects a source citation, not the design's boot-order rule.

Current-source rows below are not claims that the proposed runtime/cache/contract changes already work. Future internal publishers are §7; future tests/evidence are §6. General background was read in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/architecture/AS-IS-architecture.md::Method` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/docs/internal/architecture/plugin-extensibility-assessment.md::Method`; current Mail code wins over their older runtime descriptions. The optional Go-reference index required by the planning skill is absent from this checkout; existing source/harness and the sanctioned credential seam are the available patterns, not an invented reference library.

### 2.1 Symbols involved

| Symbol | Role | Certainty |
|---|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/gateway/rest.go::restAPI` (`homePath`, `mailBudget`, `mailBudgetOnce`) | REST shared-dependency shell. `homePath` is the configured data root (Mac default example `/Users/danielpiatkowski/.omnipus`), not a cache/workspace path. Existing budget handle/lazy resolution is the pattern for the proposed injected runtime | Verified source; new fields are proposed |
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
| `pkg/gateway/gateway.go::loadConfigAndProvider` | Boot: `agent.SetSharedMailBudget(email.SharedMailBudget(rc.homePath))` **before** `NewAgentLoop` — the ordering rule ("must run before NewAgentLoop") the pool/cache injection must follow | Verified |
| `pkg/gateway/gateway_boot.go` (watcher construction site) | `heartbeat.NewMailWatchService(email.NewMailboxWatcherSet(provider, stg.homePath, email.SharedMailBudget(stg.homePath)), 0)` — the watcher resolves the same state-dir-keyed budget instance | Verified |
| `pkg/gateway/rest_auth.go::triggerReloadAndWait`, `triggerReloadAndWaitOutcome` | The config-reload seam: mailbox save/delete and agent delete wait out the async reload that re-runs `registerSharedTools` | Verified |
| `pkg/agent/email_tools.go::registerEmailToolsForAgent`, `SetSharedMailBudget`, `sharedMailBudget` | Tool-side wiring: per-pair `email.NewClient` construction at registration (called from `pkg/agent/loop_wire.go` on construction **and** every reload), budget injection via the optional `SetMailBudget` setter pattern, `RegisterReplacing` on reload | Verified |
| `pkg/email/mail_budget.go::MailBudget`, `SharedMailBudget`, `MailBudgetRequest.flightKey`, `call`, `flightContext`, `TryCall`, `runDialValue` | The existing shared gate: process-wide instance keyed by state dir; flight key = `account + "\x00" + operation + "\x00" + json(params)` — **no pair, no generation** (I-01); shared flights detached from the first caller (`context.WithoutCancel`) — the I-02 ordering hole's substrate; watcher non-blocking entry | Verified |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/watcher.go::NewWatcher`, `Watcher.statePath`, `recordFailure` | The existing state file is under the configured data root's watcher-state namespace; `recordFailure` assigns raw `errText` to `LastErrorText`. **W1 owns its redaction/quiescence; w5 owns product exclusion and removal integration.** No assumption about external staging or backup configuration is evidence here | Verified source; new protections Unknown until implemented |
| `pkg/email/view.go::Client.ReadView`, `ResolveRef`, `MarkSeenIn`, `ReadFolderPage`, `folderNameFor`, `maxViewPartBytes` (25<<20), `SanitizeAttachmentName` | The read paths this package's routes call; the 25 MiB decoded cap; the name sanitizer F2 reuses | Verified |
| `pkg/config/config.go::MailboxConfig` | Pair configuration: `Enabled`, `WorkspaceID`, IMAP/SMTP host/port, `Username`, `PasswordRef`, `SignatureHTML`, `SentFolderName`, `DraftsFolderName` — the canonical identity fields the cache generation fingerprint binds | Verified |
| `pkg/credentials/store.go::Store.DeriveSubkey`, `Store.Close` | The sanctioned purpose-keyed derivation seam (32-byte keys, `ErrStoreLocked` when locked, no master-key export); `Close` cannot guarantee erasure of handed-out copies — the honesty constraint for rotation cleanup | Verified |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/fileutil/file.go::WriteFileAtomic` | Existing temp-write/rename primitive accepts caller-supplied bytes/mode, but its parent-directory creation uses 0755. The cache owner must **create/tighten the private 0700 directory first**, supply ciphertext only and prove native Windows access controls separately; the helper alone is not a privacy/ACL guarantee | Verified source; privacy proof remains Unknown |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/security/ssrf.go::SSRFChecker.SafeDialContext`, `SafeTransport` | Existing preview URL-protection seam; keep it rather than build a second dial-protection policy. **ADR-W6 is the later JMAP transport package, not w6-proof**; no JMAP code/compatibility result belongs to this correction | Inferred integration reuse from the design; no security execution claim |
| `contracts/openapi.yaml::getMailAttachment` (operationId), `contracts/asyncapi.yaml::channels.chat`, `WsFrameType` enum | The contract anchors: the single attachment byte operation today, and the type-discriminated WS protocol the presence frames extend | Verified |

### 2.2 The wiring inventory — every site where a second pool could form

The A8 budget is shared today because every construction site resolves the same state-dir-keyed instance (`SharedMailBudget`). The pool and cache must follow the identical pattern, and this spec pins every construction site so parallel writers cannot fork it:

| # | Wiring site | Today | Required state after this package |
|---|---|---|---|
| 1 | `pkg/gateway/gateway.go::loadConfigAndProvider` | `agent.SetSharedMailBudget(email.SharedMailBudget(rc.homePath))` before `NewAgentLoop` | The pool manager + cache manager + presence registry are constructed once here (or resolved from the same state-dir accessors) and injected **before** `NewAgentLoop`, same ordering rule |
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
| **Connection registration** | `WSHandler.ServeHTTP` issues the connection's opaque chat ID and registers `wc` in its existing session map | Fresh server connection identity after authentication | Pass that connection ID and W3's per-panel observer ID to **W1's one `PanelPresence` registry**. w5 adds an adapter, not a gateway-owned registry; W3 supplies distinct panel instances |
| **Inbound frame handling** | `wsHandlerReadLoop.dispatchFrame` — type switch on `generated.WsFrameType*`; per-frame JSON-Schema validation via `wsFrameSchemaName` + `ValidateInboundFrameJSON`, gated by `gateway.validate_inbound`; oversized frames killed by `SetReadLimit` | Each frame type has: an asyncapi schema, a `WsFrameType` enum entry, a `dispatchFrame` case, and a schema-name mapping | The generated `mail_panel_observer` frame needs its schema, enum entry, dispatch case and schema mapping before the adapter can acknowledge it (§8). **Correction:** the current generic unknown-frame branch only debug-logs/ignores; it does **not** produce a visible error. Mail's registered handler must own its generated ack/safe error responses; do not rely on the generic fallback or broaden unrelated frame behaviour |
| **Liveness** | `pingPump` (`wsPingPeriod` = 30 s) + pong handler and `readLoop` both re-arming `wsPongWait` = 60 s read deadline | A dead client (crash, network loss) stops answering pings; the read deadline expires; `readLoop` exits | **No new mail-data polling or heartbeat.** The ADR is explicit: existing socket liveness reaps lost connections; the registry just needs the expiry callback (next row) |
| **Teardown** | The deferred block in `ServeHTTP`: `UnsubscribeEvents`, task-map cleanup, `delete(h.sessions, chatID)` / `delete(h.sessionIDs, chatID)`, `unbindConnHubLocked(wc)`, `wc.close()` — runs on **every** exit path: explicit close, read error, deadline expiry, abnormal closure | This is the single choke point every disconnected socket passes through | **Bind this teardown to W1's published `PanelPresence.UnbindAll(connID)`**, not a new `DropConnection` registry or second presence implementation. W1 revokes that connection's observers and releases dependent panel sockets/work; remaining independent owners survive. Keep revocation before `wc.close()` and independently validate REST `observer_id` association (both folder and list reads; §8) |
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
| `pkg/config/config.go` (proposed persisted pair ID/config epoch support) | MEDIUM | Config load/save and every relevant credential/config save must preserve the one construction | Restart, identical re-save and rotation controls; no credential-material fingerprint |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/.github/workflows/cross-platform.yml` — proposed cache-test execution extension | MEDIUM | Existing Windows cross-compile, daemon and read-boundary jobs must remain intact; the new Mail permission tests must execute on Windows | Named-test/pass/no-skip receipts, not a compile-only green; signed Windows UAT fallback |

**Inferred planned impact, not a GitNexus verdict.** The preview grant/byte split rows are HIGH because they change already-pinned security and lifetime behaviour; the later implementing lead must trace callers and warn before production edits. This correction edits no production symbol. The Windows workflow was read: it currently has `windows-compile`, `windows-daemon-tests` and `windows-tools-tests`, **not Mail cache/ACL execution**. This package owns adding that missing evidence path (register row 23); qa-lead owns its tests/receipts. No current CI result is inferred from the workflow text.

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

The encrypted folder-metadata file (Phase 1; headers stay memory-only) and later Phase-2 snapshot belong to a private, disposable application cache under the configured data root. Encryption and permissions do not replace exclusion from capture. The product must establish both exclusions itself on every install before writing, with a visible refusal only for a genuine failure. Authority: *Mail live access: pooled connections, folder discovery and a bounded cache* and founder ruling Q-A in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/mail-feature-decisions.md::Founder answers to the spec-grill decisions`.

**Why this priority**: version-control history and archives can retain sensitive metadata after current files are deleted. This is a product privacy invariant, not a deployment precondition.

**Independent test**: initialize a clean synthetic data root using only the product, write ciphertext after both exclusions hold, then independently break staging exclusion and archive exclusion; each genuine failure must refuse the next cache write with the visible notice.

**Acceptance scenarios**:

1. **Given** a configured data directory, **When** folder metadata is written, **Then** it occupies the pair's private cache subtree, with `0700` directories and `0600` files on Unix, and only complete ciphertext is published atomically. The logical layout is data root → mail-cache → opaque pair ID → folders.enc; an example absolute location is `/Users/danielpiatkowski/.omnipus/mail-cache/<opaque-pair-id>/folders.enc`, never a hard-coded runtime root.
2. **Given** two distinct agent/workspace pairs, **When** their cache identities are assigned, **Then** each has a **randomly minted 128-bit ID stored beside its configuration**, stable across normal restart and credential rotation; no derived credential value, lossy name, email address or folder name identifies the subtree. This is the register's row-12 settlement (grill-3 M-2).
3. **Given** a genuine failure to establish/prove **either** effective staging exclusion **or** application backup/archive exclusion, **When** a cache write is attempted, **Then** no cache file is created, live access remains subject to its normal authorization/budget, and **`cache_unavailable`** is visible. A normal clean install is not left in this state merely because external setup is absent: the product owns establishing both exclusions (US-4).
4. **Given** a cache path whose component is or becomes a symlink, **When** the application resolves or writes it, **Then** it refuses safely rather than following the link out of its private subtree.
5. **Given** ordinary agent workspace-file authority, **When** a file or shell operation attempts the private cache subtree, **Then** the path is refused; generic file access never becomes a decryption route.
6. **Given** Windows, **When** the private cache directory is created or used, **Then** native access-control permissions restrict it to the current user. The acceptance evidence includes a real Windows execution and a denied-access control, not a Unix mode check or compilation claim (grill-3 M-7; register row 23).

### US-4 — Product-owned staging, backup and restore exclusion (P0)

**Correction, 2026-10-02 — Q-A/Q-B are decided.** The application, on **every install**, owns exclusion of its entire disposable Mail cache and watcher-state directories from data-directory version-control staging and from **all of its own backups/archives**. Startup/new-root initialization establishes the product's own exclusion before the first sensitive write; reinitialization is idempotent and preserves unrelated operator choices. A staging repository introduced later cannot enable sensitive writes without the same protection. The product also filters its archive/restore paths, including repository objects that could carry excluded data from history. Genuine I/O or conflicting/tracked-state failures are visible and block disk activation; **a refusal is not a substitute for shipping the product-owned exclusion**. No external repository, personal configuration or separately installed job is an interface or prerequisite of this feature.

**Why this priority**: an ignore rule alone neither removes tracked files nor stops an application archive from copying them. The gate has one owner (this package), two required conditions and one first-write consumer (W2), settled in register row 18.

**Independent test**: on clean synthetic Linux/macOS/Windows data roots, run product initialization with no external setup; create encrypted-cache and watcher-state fixtures plus an ordinary state-file positive control. Exercise effective staging and every product archive/restore path. Sensitive fixtures never stage/archive/restore; the ordinary control does. Then break each exclusion independently and prove refusal before a cache write.

**Acceptance scenarios**:

1. **Given** a fresh or upgraded install without any external exclusion setup, **When** the product initializes its data root, **Then** it establishes its own effective exclusion for cache **and watcher state** before either can be persisted. Repeating initialization preserves protection and unrelated settings. In a root without version control, no staging exists; introducing a repository later is handled by the same product check, not treated as permanent proof of exclusion.
2. **Given** ordinary state, cache files, watcher-state files and a synthetic repository history containing sensitive fixtures, **When** an application backup/archive is created or an old archive restored, **Then** excluded current, temporary and retired Mail files never appear or re-materialize, and repository metadata capable of carrying their historical copies is not included/restored as a bypass. Ordinary state and saved workspace files remain included under their normal rules. Arbitrary manual copies and forensic historical erasure are not promised.
3. **Given** evidence of already-tracked cache or watcher-state files, **When** rollout encounters the conflict, **Then** disk activation remains blocked and a **product remediation runbook**, drafted by this owner and audited by docs-verifier, explains index removal, verification and the separate authorization needed for any history rewrite/external-copy cleanup. Neither destructive history rewriting nor an assumption of harmless ciphertext happens silently. The runbook is required regardless of whether rollout finds a tracked file.
4. **Given** the one published gate decision, **When** **either** effective staging exclusion or archive exclusion is absent, unreadable, overridden or unproven, **Then** W2 refuses the cache write with `cache_unavailable`. Both conditions allow encrypted writing only after product establishment and proof; a later archive-skip regression must not leave writing enabled. Effective policy precedence/negations and tracked-state conflicts are considered, not a substring or two-filename check. The security path is self-evaluated in pure Go, never a spawned Git command.
5. **Given** a claim that the product privacy guarantee ships, **When** its delivery report is reviewed, **Then** it includes fresh **E1–E5** receipts from the candidate build on the supported platforms, with ordinary-file positive controls. No historical trace or written-only plan is execution evidence.

**E1–E5 product exclusion receipt** — future proof obligations, **not executed in this spec correction**:

| ID | Evidence instrument | Passes when |
|---|---|---|
| E1 | Product-only clean-install/upgrade initialization; independent effective-policy oracle over cache and watcher-state current/temp/retired fixtures | Product establishes exclusion without external setup; policy precedence/negation cases match an independent Git oracle. Git may be used by QA as an oracle, **never by the product security path**. |
| E2 | Actual tracked/index-path inspection plus an already-tracked synthetic conflict | No sensitive fixture is tracked or staged on the clean install; the tracked-conflict case is refused visibly, not declared safe because an ignore rule exists. |
| E3 | Member and content listing for **every application backup/archive** | Cache, watcher state and repository-object sensitive markers are absent; ordinary state and a saved workspace-file control are present. |
| E4 | Restore of fresh and hostile/old archives into a scratch root | No protected Mail file or repository-history bypass materializes; ordinary state and saved-file provenance controls restore normally. |
| E5 | Actual staging after product initialization and a real synthetic cache/watcher write, followed by independent staging/backup-coverage failure injection | The control is staged, sensitive files are not; breaking **either** coverage condition refuses the next cache write with a visible notice. |

The matched absence checks always include a control that the same instrument can see. Product runtime establishment is this package's obligation; the end-to-end gate tests and execution receipts belong to **qa-lead/w6**, not a competing gateway test owner.

### US-5 — Removal cascades with truthful cleanup outcomes (P0)

Mailbox disable/removal, agent/workspace deletion, pair moves and credential/endpoint changes invalidate a pair's disposable Mail identity; credential-store lock/key rotation invalidate its cache as well. **Watcher state is excluded and purged on mailbox removal** (founder Q-B=A), not left behind as an orphan. This package wires the one cascade; W1 owns runtime quiescence/redaction and W2 owns cache deletion. Saved Library files remain ordinary user data, outside this cascade.

**Why this priority**: a late write or false purge success can resurrect sensitive state after removal. The operator needs a visible, retryable cleanup outcome even after the mailbox row has gone.

**Independent test**: pause both a cache write and a watcher-state completion, run each removal trigger, and release them deterministically. Prove no resurrection, full purge or truthful cleanup-pending, and an unaffected saved Library-file control.

**Acceptance scenarios**:

1. **Given** a mailbox becoming disabled, **When** the save commits, **Then** its disposable cache and watcher state are purged, panel grants/presence revoked and leases closed, with the same truthful-outcome rule as removal.
2. **Given** mailbox removal, **When** config/credential removal completes, **Then** the cascade distinguishes full **`removed`** from **`removed_cleanup_pending`** with a safe cleanup code, accounting for cache **and watcher-state** deletion; separately authorized **Retry cleanup** works after config deletion through opaque cleanup intent.
3. **Given** a cache or watcher-state unlink failure, **When** removal responds, **Then** the pair remains disabled/tombstoned, pending cleanup is visible/retryable and no successful purge or plaintext workaround is claimed.
4. **Given** cache or watcher-state work in flight at removal, **When** its late completion is released, **Then** no removed-pair file is recreated and no cache/summary timestamp advances. The result is determined by identity/publication guards, not sleeps, polling or cancellation of an independent agent turn.
5. **Given** an agent with several mailbox pairs, **When** the agent is deleted, **Then** each pair undergoes the same cache/watcher purge and any failed cleanup is reflected in the mail portion of the response.
6. **Given** a workspace with several mailbox pairs, **When** the workspace is deleted, **Then** each pair's private state is purged explicitly; deleting only the workspace directory is not counted as private-cache cleanup.
7. **Given** a credential, endpoint, username or folder-override change, or a same-value re-save, **When** it commits, **Then** the persisted config epoch advances before older work can publish, old disposable cache is deleted and old leases close. The **random pair ID remains stable** for the same pair; a moved pair loses its former binding and state.
8. **Given** store lock or key rotation, **When** the runtime observes it, **Then** owned keys/plaintext caches are cleared best-effort, disposable files removed and rebuilding waits for normal credential resolution. No replacement master key is minted over existing encrypted data, and no forensic memory-erasure claim is made.
9. **Given** a restart containing orphan cache or watcher files for an absent pair, **When** boot reconciliation runs, **Then** none is usable/served; deletion is attempted and a failure retains safe opaque, visible/retryable cleanup intent rather than a log-only success. This criterion is traced explicitly in B-33/§6 (grill-3 M-7).
10. **Given** a previously saved attachment, including mail-derived HTML, **When** its mailbox is removed or cache expires, **Then** its ordinary workspace file, original bytes and required provenance/script preference are untouched; normal workspace backup/restore rules still apply (founder Q-E and Q5 final).

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
6. **Given** the dedicated preview-purpose byte endpoint (W0's atomic wire split), **When** it serves a selected attachment for Open, **Then** it uses inline disposition, `no-store`, the unchanged **25 MiB actual-decoded-byte cap before success**, and the grant's view-exit/expiry/revoke/close lifecycle. Browser Download remains a separate attachment-disposition stream, including larger files. An honestly known over-cap size disables Open/Save upfront; unknown or falsely low metadata cannot be pre-detected by fetching content and instead causes a visible abort during transfer if actual bytes exceed the cap.
7. **Given** an attachment preview/read/Save/Download, **When** its bytes are fetched, **Then** it consumes **W2's one targeted part reader** on the validated W1 lease: only the selected MIME leaf is read with PEEK, flags unchanged. Nested parts and the dedicated draft marker follow the common classifier; no whole-message PEEK, unrelated part, second MIME walker, disk spool or reusable byte cache is permitted. If W2's reader has not landed, the dependent endpoint waits/reports blocked, never ships a fallback (grill-3 I-5/grill-4 C3).
8. **Given** temporary Mail content followed by an explicit successful Save, **When** the real Library file is opened, **Then** temporary mail resource authority is disposed and **ordinary workspace resource rules apply**. Saved HTML keeps original bytes, its proven mail-derived marker, scripts off by default and the visible per-file checkbox. This does not extend temporary resource restrictions to saved non-HTML files (founder Q-E; Q5 final).

### US-7 — Safe diagnostics and the mandatory instrument (P0 emission; P1 redaction)

The measurement campaign needs a safe, complete per-operation record on successful work as well as failures. **w6-proof owns the sole record definition; this package owns the gateway request envelope/emitter; W1 supplies pool fields and fixes watcher raw-error persistence; W2 supplies cache fields** (register rows 17/19). This package fixes only its gateway error/log boundary. No measurement can claim success from missing records or free-form failure-only logging.

**Why this priority**: absent emitters strand the P0 campaign (grill-4 C1/grill-3 M-1), while raw provider errors can disclose sensitive content through otherwise ordinary state/log paths (grill-3 I-4). Exclusion and redaction are separate required protections.

**Independent test**: drive success, safe failure, cache hit, coalesced readers, preview mint/serve, summary and removal through real gateway seams, capture the one instrument sink and logs, and scan distinctive server/content markers. The valid record/class control must be visible; a deliberately absent sink invalidates the campaign.

**Acceptance scenarios**:

1. **Given** operational Mail diagnostics, **When** they are recorded, **Then** only opaque pair/generation IDs, closed operation/error labels, genuine timings and counts appear; role slugs are allowed, content-bearing names are not.
2. **Given** subjects, addresses, Message-IDs, folder names, credentials, full URLs or raw server responses in provider data, **When** diagnostics and durable failure state are inspected, **Then** none appears there. Class labels remain visible, not truncated raw text masquerading as redaction.
3. **Given** a watcher failure, **When** **W1's watcher implementation** records it, **Then** persisted text is empty or class-derived, never raw provider text. This package consumes that correction and preserves the already class-only summary wire; it does not patch or duplicate W1's watcher logic.
4. **Given** a gateway mail failure, **When** this package logs/maps it, **Then** only the safe class and approved fields enter the log/response, never the raw error value.
5. **Given** a cache notice, cleanup-pending result or upstream error crossing a user boundary, **When** displayed, **Then** it uses a safe closed class and never discloses a URL-bearing provider message.
6. **Given** a completed gateway Mail operation, **When** it succeeds or fails, **Then** exactly one complete record in **w6's frozen shape** reaches the injected sink and dedicated structured log, including the real outcome/duration and the supplied pool/cache fields. Preview mint, each serve request, summary and removal are covered, not just the four dialing GETs. Removal's missing enum member is the explicit publisher request §13 Q8; it cannot be invented locally.
7. **Given** coalesced reads, a cache hit or metadata-only mint, **When** their records are collected, **Then** a joiner reports zero socket count plus the shared-flight marker, and a cache hit/mint reports zero mail acquisition; pool/cache owners supply their fields truthfully. Joined records never double-count the shared flight or label stale memory headers as encrypted disk headers.
8. **Given** the production emitter is missing, or an exercised operation has no records, **When** the proof campaign attempts to judge a bar, **Then** the run is invalid/not judgeable and Wave E does not proceed — never a silent green, zero-latency sample or ad-hoc replacement instrument.

### US-8 — One durable identity construction across restarts (P0)

**Correction (grill-3 M-2/M-3; register row 12):** this package implements the **random persisted 128-bit pair ID** and the **non-secret generation fingerprint of canonical pair identity plus persisted config epoch**. W1/W2 consume them unchanged, not through independently derived fingerprints. Encryption keys still come from the existing credential-store seam; resolved credential material is **not** the generation input.

**Why this priority**: incompatible constructions make valid snapshots unusable across packages; an unchanged epoch after a credential replacement can make an old snapshot look applicable. Password rotation must invalidate cache, not orphan its randomly named subtree.

**Independent test**: record the pair ID/generation, restart unchanged, then replace credentials at the same reference through the supported save flow and separately change the endpoint while stopped. Compare the persisted epoch/canonical identity and reject old snapshots before display; prove a same-value re-save advances the epoch too. Config contains credential references, never plaintext passwords.

**Acceptance scenarios**:

1. **Given** a pair's canonical identity and persisted config epoch, **When** its current generation is supplied to pool/cache/flight consumers, **Then** all three receive the **same opaque non-secret fingerprint** of those values, with no credential material included. The pair ID is the separate randomly assigned persisted value.
2. **Given** an unchanged configuration and epoch after restart, **When** a valid snapshot is loaded, **Then** the ID/generation match and the snapshot is usable subject to freshness/key checks.
3. **Given** a relevant config/credential save or same-value re-save, **When** it commits, **Then** the epoch advances before publication, the generation changes, old work/snapshots are rejected and old disposable cache is deleted without salvage. A stopped-process endpoint change changes canonical identity; a supported offline credential replacement must also persist the epoch advance, not rely on secret hashing. Normal password rotation leaves the pair ID intact.
4. **Given** a locked/broken credential store, **When** cache or live access is attempted, **Then** store-locked/broken is distinct from cache miss/corruption. Cache use is unavailable; live work can occur only if the normal credential/authorization path genuinely permits it, otherwise it fails visibly. No fabricated authenticated fallback or replacement master key is allowed.

### US-9 — Issued references make the first attachment journey reachable (P0)

Every list/detail or attachment metadata result, including the agent's read result, returns the authorized opaque message reference that the next action needs. **w5-integration is the single issuer; W1 exposes same-lease epoch/generation evidence; W2 performs same-lease validation**. This is first-phase work, not a promise deferred to JMAP or to another issuer (grill-3 C-1; register row 16).

**Why this priority**: a message without Message-ID must still be openable/readable/savable from actual returned values. A remembered numeric UID is not authority after a folder is recreated.

**Independent test**: list/read a synthetic message without Message-ID, then list/read/save its attachment using only returned references/part indices. Separately replay a reference after a config/epoch change and into a wrong pair; prove rejection before content fetch or mutation.

**Acceptance scenarios**:

1. **Given** an authorized message lacking Message-ID, **When** panel list/detail and agent read/attachment metadata return, **Then** they include the same issuer's usable opaque reference, binding pair, config/folder generation and live folder epoch/UID. No model or UI reconstructs it.
2. **Given** a returned reference and stable attachment index, **When** the next permitted Open/read/Save/Download or seen action executes, **Then** W2 validates it on W1's **same selected lease** before fetch/mutation; the accepted reference addresses the original message only.
3. **Given** a foreign-pair, old-generation or old-epoch reference, **When** it is presented, **Then** independent mailbox authorization is unchanged and validation yields the typed stale-reference 409 before any content command or mutation. No wrong-message success or resolve-then-mutate race is allowed.

### US-10 — Gateway consumes bounded search, freshness and metadata honestly (P0)

W0 publishes the wire fields; W2 alone searches, issues cursors, classifies attachment metadata and decides freshness. This package makes those existing publishers reachable through its gateway routes instead of inventing substitute services (grill-3 C-2; grill-4 C2/C3/m4; founder Q-C/Q-D/Q-E).

**Why this priority**: the 200-row wall is a dead end without server search, missing REST observer association silently disables pooling, and false zero counts/source labels conceal failures.

**Independent test**: exercise generated folder/list requests with real observer association, 25/+25/200 browse/search bounds, subject/from/to-only matches, a stale cursor, unknown count and Phase-1 mapping/header cache hits. Fake-server counters and safe generated results prove the seam, not a second search implementation.

**Acceptance scenarios**:

1. **Given** more than 200 folder messages or search matches, **When** browsing or searching by subject/sender/recipient substring, **Then** server-side results arrive 25 at a time, the active view never exceeds 200, the next cursor disappears at the ceiling and the search affordance remains reachable. Older/search rows do not enlarge the newest-50 reusable cache.
2. **Given** a cursor from another pair/folder/query/config/epoch, **When** the next page is requested, **Then** the typed stale-cursor 409 is visible and permits one view reset, not silent reinterpretation or replay.
3. **Given** absent or **older-than-five-minute** headers/counts, **When** the panel opens or switches folder, **Then** one eligible live refresh occurs; fresh data (including exactly five minutes old) causes no live refresh. **Manual Refresh always** and confirmed own-action refresh remains unchanged. Mapping freshness has its separate >24-hour rule; no repeated panel timer or watcher cache fill is introduced.
4. **Given** unknown folder count or Phase-1 cached metadata, **When** responses are produced, **Then** unknown count is null, not zero; disk source labels describe folder mapping only, and cached headers/counts are memory-labelled. The same W2 classifier feeds paperclip/detail/agent attachment facts without body fetch.
5. **Given** a successful explicit attachment Save, **When** the result hands off to its real Library entry, **Then** ordinary workspace resource rules apply and the temporary source is disposed. Saved HTML alone retains its already-decided original bytes/provenance, scripts-off default and per-file checkbox; cache removal never deletes the saved file.

---

## 4. Behavioral contract (quick reference)

### 4.1 When/Then summary

| When | Then |
|---|---|
| Panel, watcher or tool resolves the Mail runtime | It gets the one shared pool/budget/cache; two pairs on one account remain result-isolated under the existing two-slot account gate |
| The last eligible observer closes/dies/logs out | Idle panel sockets close; shared work survives only for remaining independent owners; no panel-data polling continues |
| A fresh install initializes Mail persistence | The **product itself** establishes staging **and** application backup/archive exclusions for cache and watcher state before sensitive writes |
| Either exclusion genuinely fails/is unproven | No cache file is written; normal authorized live access remains available with visible `cache_unavailable` — this failure mode does not excuse missing product-owned exclusion |
| A private cache file is written | It is complete ciphertext, atomically published under the random persisted pair ID with restrictive platform permissions |
| Any application backup/archive or old-archive restore runs | Cache, watcher state and a repository-history bypass are excluded; ordinary state and saved workspace files/provenance remain eligible |
| A removal/disable/reconfiguration/key event invalidates the pair | Identity/publication guards advance first, work/grants revoke, private cache/watcher state purges; any failed unlink is visible/retryable cleanup-pending |
| Old work completes after removal or a revision advance | No file, cache row, count or validation timestamp is resurrected/published under the wrong identity |
| Relevant settings/credentials are saved, including identical re-save | Persisted epoch and generation advance; the random pair ID stays stable for the same pair |
| A list/detail or agent result names attachment metadata | It includes the one issuer's usable opaque reference, even without Message-ID; later actions validate on the same lease before content/mutation |
| Browse/search reaches the view boundary | Pages remain 25/+25, at most 200; server-side subject/from/to search is reachable; a stale cursor produces the typed 409 |
| Open/folder switch finds fresh headers/counts | No live refresh; absent or >5-minute data gets one eligible refresh. Manual Refresh always and confirmed own-action refresh remains unchanged |
| A message preview is minted | It authorizes metadata only, makes no mail-network request and stores no HTML/body/part bytes |
| A selected attachment is served | Only W2's targeted PEEK reader is used; preview is capped before success, Download remains a separate larger-file stream; no whole-message fallback |
| Preview token cap/expiry/revocation is exercised | Existing cap-8-refuses 429, 404 parity, logout and separate signature discipline remain unchanged |
| An operation completes or fails | One safe record in w6's single frozen shape is emitted; missing emission invalidates measurement, never a zero-duration green |
| A temporary attachment is explicitly saved | It becomes an ordinary workspace file, not Mail cache; only saved HTML retains the settled original-byte/provenance/scripts-off/per-file-checkbox rule |

### 4.2 Explicit non-behaviors

| The system must not… | Because / authority |
|---|---|
| …build a second pool, budget, registry, cursor issuer, MIME walker, part reader, instrument shape, recipient rule or Save service | The register's single-publisher rules R-1/R-4 and §7 forbid parallel implementations, not just parallel wire types |
| …create a stub or local wire type while W0 is missing | Contract-first Wave B is a named publisher boundary; missing fields are reported under R-3, never improvised |
| …use account contention identity as result/cache ownership | Distinct pairs/config generations must not share data (ADR correction I-01) |
| …make cache privacy conditional on external setup, or treat an ignore match alone as permission to write | **The product owns both staging and application archive exclusion on every install**; tracked state/history and archive paths are independent (founder Q-A; US-4) |
| …spawn Git or another command to make a security-critical exclusion decision | Pure-Go enforcement and the register's self-evaluated effective-policy rule; QA's independent oracle is not product runtime |
| …write plaintext fallback/temp/journal cache or use lossy/credential-derived path identity | Ciphertext-only private cache, random persisted pair ID and sanctioned encryption-key ownership (US-3/US-8) |
| …return successful purge for failed cache/watcher cleanup, or rely on sleeps to prevent resurrection | Tombstone/epoch/publication ordering and visible Retry cleanup are correctness requirements (US-5) |
| …retain reusable body/inline/attachment bytes, even in token grants or a recent-preview keep | Only request/current renderer buffers are allowed; explicit user Save is a separate ordinary file, not a byte-cache exception |
| …dial at preview mint, or fetch a whole message/unrelated part as an attachment fallback | Mint is metadata authorization; attachment bytes come only from W2's selected-part PEEK reader (US-6; grill-3 I-5) |
| …loosen preview cap, token revocation/404 parity, proxy consent, CSP or separate signature discipline | Existing security controls and the **25 MiB decoded** preview/read/Save limit stand; larger browser Download has its separate streaming role |
| …restore disposable Mail state from old archives/history, or purge the user's saved files with mailbox cache | Cache/watcher exclusion and lifecycle are separate from ordinary workspace Save/backup/provenance (founder Q-B/Q-E) |
| …claim unknown count is zero, Phase-1 memory headers came from disk, or a failed refresh validated data | W2's single metadata producer preserves nullability, source scoping and successful-validation timestamps (US-10) |
| …refresh fresh headers/counts unconditionally on open/switch, or add a repeating panel timer/IDLE fill | Stale-gated eligible events only; manual Refresh and own successful actions are the explicit exceptions (founder Q-C) |
| …invent/synthesize a message reference or validate it on a different lease from its action | First-phase gateway issuance and same-lease epoch/generation validation protect message identity (register row 16) |
| …carry temporary Mail resource restrictions onto an ordinary saved non-HTML file | Temporary source authority ends at Save; saved HTML's scripts default/checkbox is the already-settled exception, not a new global policy (founder Q-E/Q5) |
| …log raw provider text/content or call absent instrument records a valid campaign | Closed safe classes and real per-operation records are mandatory; emitters precede Wave E (US-7) |

### 4.3 Machine-verifiable constraints

Technical enforcement detail belongs here and in the TDD/interface sections; all results below are **future test observables**, not measurements from this correction.

| ID | Constraint | Test observable |
|---|---|---|
| MC-1 | One pool/budget/cache set per configured process data root; all six construction sites inject the same handles; no unmanaged production dial | Pointer identities and typed uninjected-client refusal; one account-slot acquire per operation |
| MC-2 | Global open sockets/reservations ≤ **8**, per-mailbox ≤ **2**, existing per-account work slots ≤ **2**; ninth demand is busy within **5 s** inside the **45 s** overall read budget | Fake-server active/max/accepted counters plus pool reservations and fake-clock queue expiry; no cap/deadline widening |
| MC-3 | Two pairs on the same account never share flight data, selected socket or cache identity | Pair-specific fake-server markers; combined account work remains bounded |
| MC-4 | Only acknowledged, authorized REST observer association permits retention through **W1's one registry**; explicit close/death/logout removes its bindings and closes idle panel sockets | Real WS + both folder/list GETs; foreign/stale/absent observer controls; other tab/tool work unaffected |
| MC-5 | Private cache layout under configured root/random persisted pair ID; Unix directory/file modes **0700/0600**; native Windows current-user access restriction | Distinct-root/path/mode tests; actual Windows ACL/denied-access execution, not compile-only proof |
| MC-6 | **w5 publishes one gate; W2 enforces it.** `allowed` requires proven effective staging exclusion **AND** application backup/archive exclusion; product establishes both on every install, including watcher-state protection; genuine failure → `cache_unavailable`, no cache file | Product-only initialization control; independently break each condition, unreadable/negated policy and tracked-state conflict; subsequent write cannot bypass a regressed backup skip |
| MC-7 | Every application backup/archive/restore skips cache/watcher current/temp/retired namespaces and repository objects that would reintroduce their history | Actual member/content/restore assertions with sensitive synthetic markers and ordinary-state/saved-file controls |
| MC-8 | Cache **or watcher-state** unlink failure → truthful `removed_cleanup_pending` + safe code/opaque authorized Retry intent; config-row-gone retry succeeds later | Independently fault each purge target; no successful-purge assertion on failure |
| MC-9 | Paused cache/watcher completions cannot recreate files after removal/disable/reconfiguration | Deterministic pre-publication/pre-write barriers, no sleeps or polling |
| MC-10 | Relevant config/credential save, same-value re-save or stopped-process canonical-identity change invalidates old-generation work/snapshots; unchanged restart remains valid | Stable ID/no-change restart control; epoch-advancing same-reference credential replacement and canonical change rejection |
| MC-11 | Message/attachment grant store has **no HTML/body/inline/attachment payload**; authorization/reference/URL metadata only, existing session/expiry/cap discipline | White-box grant inspection independent of served response; no invented size/performance measurement |
| MC-12 | Metadata mint has **zero mail dials**; each serve runs through the shared budget and emits its own live-work outcome; backoff refusal has zero dials | Real command/dial counters and recording sink; no uncapped/unbudgeted path |
| MC-13 | Existing token controls stand: message cap **8**, ninth mint **429/no eviction**, unknown/expired/revoked **404 parity**, logout revoke; signature replace-on-mint with **2-minute** TTL, outside message cap; consented remote resources token-scoped | Existing preview control assertions re-derived for metadata-only grants, **never weakened** |
| MC-14 | Dedicated preview bytes = inline + `no-store` + **25 MiB actual-decoded cap before success**; Browser Download = separate attachment-disposition stream, including larger files | Exact cap−1/cap/cap+1, honest/unknown/lying metadata, decode/disconnect controls; incomplete streams never count as success |
| MC-15 | Watcher persistence and gateway diagnostics contain **zero content/credential/raw-error markers** and visible safe classes | W1 `recordFailure` prerequisite plus w5 `mailErr502`/logging fix; real raw-failure propagation and positive detection controls |
| MC-16 | Ordinary agent file/shell authority cannot read/write the private cache subtree | Existing filesystem-policy resolution refuses the path; authorized ordinary workspace-file control succeeds |
| MC-17 | Pair ID = random persisted **128-bit** value, unchanged across restart/password rotation; generation = non-secret fingerprint of canonical pair identity + **persisted config epoch**, with one w5 implementation | W1/W2/gateway receive identical opaque values; same-value save advances epoch; no credential-material generation derivation or lossy path naming |
| MC-18 | One complete safe record per gateway logical operation on success/failure, using **w6 §6.1 only**; W1/W2 provide owned fields; joiner `socket_count=0`, `shared_flight=true`; missing records invalidate Wave E | Injected sink + dedicated log assertions for mint/serve/summary/removal and normal reads; removal-member publisher gap §13 Q8 must be closed before its row goes green |
| MC-19 | **w5 issues** usable opaque `message_ref` on list/detail/tool metadata without Message-ID; **W2 validates** pair/generation/epoch on the **same W1 selected lease before content/mutation** | Actual-output-only attachment journey; wrong pair, epoch and generation each typed stale-reference refusal; zero prohibited FETCH/STORE |
| MC-20 | W0 generated paging/search/stale shapes reach handlers; **W2** alone issues cursors/searches SUBJECT/FROM/TO; **25/+25/200** per browse/search view, no full-body/local-index search | 201+ matches, field-isolated queries, query/cursor mismatch, typed stale-cursor **409**, no second issuer/cache growth |
| MC-21 | Unknown count = **null**, confirmed absent optional role = **0**; mapping source has **five** values including `saved`; Phase-1 disk source = folder mapping only, headers/counts memory/live/none | `TestMailFolders_UnknownCountIsNull` and generated-schema positive/negative controls; no fabricated timestamps |
| MC-22 | Open/switch live refresh only for absent or **>5-minute** header/count state; exactly 5 minutes is fresh; manual Refresh always; confirmed own-action invalidation/refresh unchanged | Fake-clock 5 min−1 s / 5 min / 5 min+1 s, command counts, no repeating timer or watcher fill; separate 24-hour mapping controls |
| MC-23 | Watcher state is **excluded and purged**, orphan state cannot serve, saved workspace files survive mailbox cleanup; saved HTML preserves original bytes/provenance/scripts default/checkbox | Paused watcher-purge/restart tests plus saved-file/provenance controls; real Windows receipt for permission claims |
| MC-24 | W1's `RevisionSource` captures once before server work; w5 counter advances on specified events; W2 `Put`/`PutCounts`/`Save` compare the **captured** value | A pre-mutation read released after newer success publishes no row/count/file/timestamp; response metadata retains correct revision and no older-flight join |

### 4.4 Integration boundaries

| Boundary / single publisher | Data and consumption | Failure / gate |
|---|---|---|
| **Backend-lead as ADR-W0** | Publishes every §8 generated REST/WS/persisted-JSON shape in Wave B; there is no W0 spec file | Missing generated field blocks wire consumers under register R-3; no stub/parallel type |
| **W1 read runtime** | Publishes pool/lease, **one presence registry**, revision-capture capability, dirty marks and safe watcher failures; w5 injects/binds only | Typed capacity/transport/wiring refusal; no private dial, second account acquire or watcher-file edit by w5 |
| **W2 metadata** | Publishes discovery, snapshot/header/count service, **server SEARCH/cursor issuer**, **one part reader/MIME classifier**, same-lease validation and cache instrument fields | Visible unknown/stale/cache/fetch failure; no false zero/attachment flag or whole-message fallback |
| **w5-owned data-root privacy boundary** | Product establishes its own effective staging and archive/restore exclusions for cache/watcher state on every install; w5 publishes the **one** allowed/refused decision and W2 enforces it | Both coverage conditions required before write; genuine failure is visible `cache_unavailable`, not an external-setup dependency |
| **w4-features (ADR-W7–W10)** | Publishes Transfer/Save/receipt, Library provenance/script storage, CSS sanitizer and recipient/tool helpers; w5 is the **single Mail-route adapter writer**, never a Library-file writer | Safe typed service refusal, no partial saved file, no second service or extra approval mechanism |
| **W3 panel** | Publishes presence and eligible refresh events, consumes generated pages/409/refs/cleanup outcomes; temporary source handoff then real-file Save | No UI connection is required for independent agent work; missing/stale observer permits request-scoped authorized work only |
| **w6-proof** | Publishes the **sole internal instrument shape**, test/mutation plan and measurement oracle; W1/W2/w5 emit production records | Absent emitter/record/Windows execution path blocks proof; tests-only owner never implements a missing producer |
| **Existing credential-store owner** | Sanctioned derived keys and lock/rotation state; **Credential Boot Contract** still governs | Locked/broken credentials are distinct from cache miss; no invented live authentication or replacement key |

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

### Feature: Product-owned cache placement and the unified gate

#### Scenario B-9: Product-only initialization makes the first write private
**Happy Path** · Traces to: US-3.1, US-3.2, US-4.1, MC-5, MC-6, MC-17
**Given** a fresh synthetic configured data root with no external exclusion setup, two pairs and product-established staging **and** archive protection,
**When** a pair's folder metadata is written,
**Then** it is complete ciphertext in that random persisted pair-ID subtree, Unix permissions are 0700/0600, the second pair's subtree is distinct, and ordinary state remains eligible for staging/backup.

#### Scenario B-10: Genuine exclusion failure blocks cache writes visibly
**Error Path** · Traces to: US-3.3, US-4.4, MC-6
**Given** product establishment cannot prove an effective exclusion because its policy is unreadable, negated or conflicts with tracked sensitive state,
**When** a cache write is attempted,
**Then** no cache file is created, normal authorized live work is not bypassed, and `cache_unavailable` is visible — not a silent skip or plaintext fallback.

#### Scenario Outline B-11: Staging AND archive exclusion are both required
**Edge Case** · Traces to: US-3.3, US-4.4, MC-6
**Given** product-established coverage is independently faulted to the staging/archive state below,
**When** the next cache write is attempted,
**Then** the single gate returns the indicated outcome and only the allowed case publishes ciphertext.

| Effective staging exclusion | Application archive exclusion | Expected outcome |
|---|---|---|
| Proven | Proven | Allowed |
| Proven | Failed/unproven | Refused; `cache_unavailable`; no cache file |
| Failed/unproven | Proven | Refused; `cache_unavailable`; no cache file |
| Failed/unproven | Failed/unproven | Refused; `cache_unavailable`; no cache file |

#### Scenario B-12: Symlink at the private cache path is refused
**Error Path** · Traces to: US-3.4, MC-5
**Given** a synthetic cache-path component is a symlink out of the private root,
**When** the application attempts a cache write,
**Then** it refuses safely and writes nothing through the link.

#### Scenario B-13: Ordinary file authority cannot reach private cache
**Error Path** · Traces to: US-3.5, MC-16
**Given** an agent holding ordinary workspace-file authority and an authorized ordinary-file control,
**When** it attempts a file/shell operation under the private cache root,
**Then** that path is refused while the same policy instrument permits the ordinary-file control.

### Feature: Product backup, archive and restore exclusion

#### Scenario B-14: Every application archive excludes sensitive Mail state
**Happy Path** · Traces to: US-4.2, US-4.5, MC-7, MC-23
**Given** synthetic cache/watcher current/temp/retired files, repository objects carrying their historical markers, ordinary state and a saved workspace-file control,
**When** each product backup/archive path creates an archive,
**Then** member/content inspection finds no protected fixture or historical marker and does find the ordinary state/saved-file controls (E3); exclusion covers more than the current directory name.

#### Scenario B-15: Old/hostile archives cannot restore protected Mail state
**Alternate Path** · Traces to: US-4.2, US-4.5, MC-7, MC-23
**Given** old/hostile archives containing protected Mail files/history plus ordinary saved-file/provenance controls,
**When** the product restores them into a scratch root,
**Then** no cache/watcher/history bypass materializes and ordinary controls restore under their normal rules (E4).

#### Scenario B-16: Product exclusion agrees with the independent policy oracle
**Edge Case** · Traces to: US-4.1, US-4.4, US-4.5, MC-6
**Given** fresh/upgrade/no-repository/later-repository roots and effective-policy precedence/negation/tracked-state fixtures,
**When** the product-only exclusion campaign evaluates them,
**Then** self-evaluated product decisions agree with an independent Git staging/policy oracle, unreadable/doubtful coverage refuses, **no product security path spawns Git**, and fresh **E1–E5** receipts include sensitive-negative and ordinary-positive controls. Product establishment, not externally supplied configuration, is under test.

### Feature: Cache and watcher-state removal cascades

#### Scenario B-17: Mailbox removal purges both private state targets
**Happy Path** · Traces to: US-5.2, US-5.10, MC-8, MC-23
**Given** a configured pair with cache/watcher files and a saved Library-file/provenance control,
**When** the mailbox is deleted,
**Then** full `removed` means both private targets are gone, runtime/grants are revoked and the config/credentials no longer authorize it; the saved user-file control is untouched.

#### Scenario Outline B-18: Either unlink failure is visible pending cleanup
**Error Path** · Traces to: US-5.2, US-5.3, MC-8, MC-23
**Given** deletion of the target below will fail and the pair remains tombstoned after config removal,
**When** the user invokes the separately authorized Retry-cleanup operation after the failure is cleared,
**Then** the preceding removal outcome was `removed_cleanup_pending` with a safe code (never a successful purge), and retry now completes both purges truthfully.

| Failed target |
|---|
| Cache subtree |
| Watcher state file |

#### Scenario B-19: Late cache and watcher completion cannot resurrect removal
**Error Path** · Traces to: US-5.4, MC-9, MC-23
**Given** cache and watcher-state writes paused at deterministic pre-publication/pre-write points and removal already completed,
**When** those completions are released,
**Then** no removed-pair file, row, count or timestamp is recreated/advanced; an independent agent turn is not cancelled because of UI loss.

#### Scenario B-20: Agent deletion cascades every pair's private state
**Alternate Path** · Traces to: US-5.5, MC-8, MC-23
**Given** an agent with three mailbox pairs, both private state targets for each and a saved-file control,
**When** the agent is deleted,
**Then** all three pairs cascade; any cache/watcher purge failure is visible in the mail-cleanup result and saved files remain untouched.

#### Scenario B-21: Workspace deletion does not mistake its directory wipe for Mail purge
**Alternate Path** · Traces to: US-5.6, MC-8, MC-23
**Given** two mailbox pairs whose private state lives outside the workspace directory,
**When** that workspace is deleted,
**Then** each pair's cache and watcher state are explicitly purged, with truthful failure discrimination independent of the workspace-directory outcome.

#### Scenario Outline B-22: Reconfiguration advances the one persisted generation
**Error Path** · Traces to: US-5.7, US-8.3, MC-10, MC-17
**Given** a valid old-generation snapshot and paused old work,
**When** the relevant supported change below commits,
**Then** the epoch/canonical identity yields a new generation before publication, old work/snapshots are rejected/deleted without salvage and the same pair's random ID remains stable. No plaintext password is stored in config.

| Change |
|---|
| Credential replacement at the same credential reference, with persisted epoch advance |
| Identical configuration re-save, with persisted epoch advance |
| Endpoint/port/username/override change |
| Stopped-process endpoint change, detected from canonical identity on restart |

#### Scenario B-23: Store lock is not a cache miss or invented live authentication
**Edge Case** · Traces to: US-8.4, MC-10
**Given** a locked/broken credential store,
**When** cache/live access is attempted,
**Then** the store state is reported distinctly; cache is unavailable, live work follows only genuine credential/authorization readiness or its visible refusal, and no replacement key or fabricated successful dial is created.

### Feature: Metadata-only preview grants

#### Scenario B-24: Mint stores metadata only
**Happy** · Traces to: US-6.1, MC-11
**Given** an authenticated session and a valid message reference,
**When** the preview mint completes,
**Then** the grant in the token store contains the pair/folder/ref/load-remote identity, session binding and expiry — and zero HTML string, zero inline part bytes, zero attachment data (white-box inspection).

#### Scenario B-25: Serve fetches live through the budget
**Happy Path** · Traces to: US-6.2, US-6.5, MC-12, MC-18
**Given** a minted grant and a message with N inline resources (N=0,1,5),
**When** the SPA renders that preview through its HTML and inline serve requests,
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
**Error Path** · Traces to: US-7.1, US-7.2, US-7.4, US-7.5, MC-15
**Given** a fake IMAP server whose failures contain distinctive subject/address/folder/URL/credential markers, plus a separate deliberately marked synthetic scan-control fixture,
**When** failures flow through W1's watcher state and this package's gateway logs/notices/cleanup/error boundaries,
**Then** operational outputs and persisted failure state contain zero forbidden markers and do contain safe classes; the **same scan finds the marker in the separate control fixture** and in the provider input, proving it could detect a leak. An allowed class string alone is not a leak-detection control.

#### Scenario B-31: W1's watcher correction is consumed without a second redactor
**Alternate Path** · Traces to: US-7.3, MC-15
**Given** W1's watcher fails against a raw-talking synthetic server,
**When** its persisted failure is read through the gateway summary seam,
**Then** the persisted text is empty/class-derived, the generated summary remains class-only and w5 adds no watcher-file edit or substitute implementation.

### Feature: Missing proof obligations and frozen-interface integration

#### Scenario B-32: Native Windows permissions are executed, not inferred
**Edge Case** · Traces to: US-3.6, MC-5
**Given** a real Windows runner with the tagged pure-Go cache tests and current-user/denied-user controls,
**When** product cache creation/access is exercised,
**Then** native access-control assertions run and refuse the denied control; a signed named-test/exit-code receipt exists. A compile-only job or Unix mode result cannot satisfy this scenario.

#### Scenario Outline B-33: Boot rejects and reconciles orphan private state
**Error Path** · Traces to: US-5.9, MC-8, MC-9, MC-23
**Given** the target below belongs to a pair absent from live config and unlink is either successful or faulted,
**When** the gateway reconciles state at restart,
**Then** the orphan is never served, successful deletion removes it, and faulted deletion keeps safe visible/retryable cleanup intent without claiming purge success; no late writer recreates it.

| Orphan target |
|---|
| Cache subtree |
| Watcher state file |

#### Scenario B-34: The tracked-state remediation runbook exists at delivery
**Alternate Path** · Traces to: US-4.3, MC-6, MC-7
**Given** a synthetic already-tracked Mail-state conflict,
**When** the implementation's documentation/evidence pack is reviewed,
**Then** this owner's audited runbook explains blocked activation, removal from the current index, rechecking both exclusions and the separate founder authorization for any history/external-copy cleanup. No destructive remediation was silently executed and no personal setup is a prerequisite.

#### Scenario Outline B-35: One frozen record for every gateway outcome
**Happy Path / Error Path** · Traces to: US-7.6, US-7.7, US-7.8, MC-18
**Given** the operation below has the W1/W2 frozen dependencies and w6's complete operation enum,
**When** it completes successfully or with a scripted safe failure,
**Then** exactly one complete record reaches the sink and dedicated log, with real outcome/duration, supplied fields and no sensitive marker; cache hits/mints have zero acquisition and joiners have `socket_count=0` plus `shared_flight=true`.
**And** repeating the observation with an absent sink yields an invalid/not-judgeable campaign, never a zero-duration success. The **removal** row remains a publisher-boundary gate until **§13 Q8** is resolved by w6, not a skipped requirement.

| Gateway operation |
|---|
| Folder/list/open |
| Metadata-only preview mint |
| Message/attachment preview serve, including each inline request |
| Saved-state summary (zero mail commands) |
| Removal cascade, including cleanup-pending |

#### Scenario B-36: A no-Message-ID journey uses only actual issued references
**Happy Path** · Traces to: US-9.1, US-9.2, MC-19
**Given** an authorized nested-part message without Message-ID and genuine list/agent results,
**When** the permitted attachment list/read/Save journey uses only those returned references and indices,
**Then** w5's single issuer supplies every ref, W2 validates on the same W1 lease, only the selected part is fetched with PEEK and the shared Save service produces the real file/receipt.

#### Scenario B-37: Search and browse stay within the frozen 25/200 boundary
**Happy Path / Edge Case** · Traces to: US-10.1, MC-20
**Given** a folder with over 200 synthetic matches, including subject-only, sender-only and recipient-only matches older than the current browse view,
**When** the user follows the real browse/search flow,
**Then** server header search and pages use W2's single issuer, default/max page is 25, Load more adds 25, no view exceeds 200, no next cursor exists at the ceiling and search remains reachable without a body/local-index fetch or reusable-cache growth.

#### Scenario Outline B-38: Mismatched cursor/reference refuses before content work
**Error Path** · Traces to: US-9.3, US-10.2, MC-19, MC-20
**Given** an independently authorized route and the stale/foreign binding below,
**When** the next page/message action uses the issued cursor/reference,
**Then** the generated HTTP 409 carries the appropriate `stale_cursor`/`stale_reference` discriminator, no prohibited content FETCH/STORE occurs and the client can reset/re-read once, not loop or replay a mutation.

| Binding mismatch |
|---|
| Cursor pair/folder/query/config/epoch |
| Message reference from another authorized pair |
| Message reference from an old config/mapping generation |
| Same numeric UID after UIDVALIDITY/folder recreation |

#### Scenario Outline B-39: Generated metadata retains null and source truth
**Edge Case** · Traces to: US-10.4, MC-21
**Given** W2 supplies the state below through the generated schema,
**When** the gateway maps the result,
**Then** the expected value remains unchanged, with no second MIME walk or fabricated validation time.

| W2 state | Expected wire fact |
|---|---|
| Unknown count | `total=null`, not 0 |
| Confirmed absent Sent/Drafts | `total=0`, availability absent |
| Saved Phase-1 folder mapping | Mapping source `saved`; mapping metadata source `encrypted_disk` |
| Phase-1 cached headers/counts | Source `memory`, never `encrypted_disk` |
| Metadata-only classifier success/failure | Same list/detail/agent flag; visible failure instead of uncomputed false; zero attachment bytes |

#### Scenario Outline B-40: Eligible refresh keeps the founder's stale-gating
**Alternate Path / Edge Case** · Traces to: US-10.3, MC-22
**Given** W2 owns the header/count freshness decision and W3 supplies the real panel event below,
**When** that event occurs,
**Then** the resulting gateway calls obey the expected live-refresh count; separate mapping metadata remains on its 24-hour/invalidation rule and automatic work never carries human Retry.

| Event/data | Live refreshes |
|---|---|
| Open/switch, absent data | One |
| Open/switch, age 5 min−1 s | Zero |
| Open/switch, age exactly 5 min | Zero |
| Open/switch, age 5 min+1 s | One |
| Manual Refresh, fresh data | One |
| Confirmed own mutation, open panel | One after invalidation |
| Closed panel across several intervals | Zero panel-data refreshes |

#### Scenario B-41: Revision advancement rejects delayed publication everywhere
**Error Path** · Traces to: US-1.2, US-5.4, US-10.3, MC-24
**Given** a read captured W1's revision before server work and is paused after its old snapshot, then a confirmed mutation and newer refresh complete,
**When** the old read's completion is released,
**Then** W2 publishes no old row/count/snapshot/timestamp, w5 does not attach a new revision to old data and the newer refresh did not join the older flight. One capture seam, one counter and no new timer exist.

#### Scenario Outline B-42: Disable and key events use the same honest cascade
**Alternate Path** · Traces to: US-5.1, US-5.8, MC-8, MC-9, MC-23
**Given** private pair state, active owned work and a saved-file control,
**When** the trigger below invalidates the pair/cache,
**Then** relevant work/grants/keys and disposable state are invalidated before later publication; any deletion failure is visible/retryable, saved files remain unchanged and no replacement master key is created.

| Trigger |
|---|
| Mailbox enabled → disabled |
| Credential-store lock |
| Key rotation |

#### Scenario B-43: Random pair ID and the one generation survive unchanged restart
**Happy Path** · Traces to: US-3.2, US-8.1, US-8.2, MC-10, MC-17
**Given** the persisted random 128-bit pair ID, canonical identity/config epoch and an authenticated eligible snapshot,
**When** the application restarts with those values unchanged,
**Then** pool/cache/gateway consume the same opaque ID/generation and the snapshot remains eligible; password rotation later changes generation via the epoch, never the pair ID or a credential-material fingerprint.

#### Scenario B-44: Gateway endpoints consume only the selected-part reader
**Edge Case** · Traces to: US-6.7, US-9.2, MC-14, MC-19
**Given** an issued reference for a nested multipart message with two attachments, an inline body CID resource, a dedicated draft marker and a genuine user `message.md`,
**When** a preview/read/Save/Download adapter requests one stable attachment index,
**Then** the real protocol trace shows only the addressed part-specific PEEK and necessary metadata, no whole-message/unrelated body transfer and no Seen change; W2's classifier alone supplies attachment facts. Removing W2's reader yields a visible missing-dependency boundary, not a fallback.

#### Scenario B-45: Saving ends temporary resource authority without losing the HTML rule
**Alternate Path** · Traces to: US-6.8, US-5.10, US-10.5, MC-23
**Given** temporary mail markdown targeting a Library/API/embed/remote resource and mail HTML with a script-effect marker, plus ordinary stored-file controls,
**When** explicit Save succeeds and the real saved entries are opened,
**Then** the temporary source is disposed, saved markdown follows **ordinary workspace resource rules** (the control proves requests could be observed), and saved HTML keeps **original bytes/provenance and scripts off by default** until its visible per-file checkbox deliberately allows that file's scripts. Provenance survives move/copy/rename/restore; mailbox removal leaves the saved files alone. The stricter temporary resource policy is never silently extended to saved non-HTML files.

---

## 6. TDD plan (tests designed before implementation)

**Design of tests only — no test is written or executed by this correction.** **qa-lead/w6-proof owns every test/evidence execution**; production owners supply the frozen implementations. Foundations/unit checks precede integrated fake-server/HTTP/WS checks, then platform/E2E/privacy campaigns; the row numbers below are stable trace identifiers, not permission to run suites in parallel. Reuse `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/wt-adr-mail/pkg/email/imapserver_test.go::startMemIMAP` and real protocol/accepted-connection counters (global dial seam: no blind parallelism), not mocked final Mail responses. Contract-aware tests bind only after W0's regenerated artifacts merge; an absent publisher shape is a boundary stop, **never a stub**. E1–E5 exercise **product-only initialization, effective staging and actual application archives/restores** on the supported platforms, with positive controls. The Windows CI execution path is this package's production obligation (register row 23); evidence is qa-lead's. Each RED, GREEN and CHECK claim requires its saved receipt/exit code. Existing assertions may be re-derived only where this design legitimately changes behaviour, never weakened, deleted or skipped to obtain green. Publisher-owned service oracles remain with their test packs; the gateway tests below assert consumption/injection seams, not competing implementations.

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
| 10 | `TestMailCache_PathModesAndPairDistinction` | Unit (real temp root) | B-9, B-43, MC-5, MC-17 | Product-only initialization establishes both protections; complete ciphertext, configured root, Unix 0700/0600 and distinct stable random-ID subtrees; Windows proof is row 32 |
| 11 | `TestMailCache_WriteGate_RefusesWithoutExclusionRule` (unified-gate family) | Unit / Integration | B-10, B-11, MC-6 | All four staging/backup cells, unreadable/negated/tracked conflict; both required, no write on refusal; a regressed backup skip cannot pass an ignore-only check |
| 12 | `TestMailCache_SymlinkAtPathsRefused` | Unit (real temp root) | B-12, MC-5 | Directory/file symlink components refuse with no escape write |
| 13 | `TestFSPolicy_RefusesCacheDirectory` | Unit (policy resolution) | B-13, MC-16 | Private cache access refuses while the authorized ordinary-file control succeeds |
| 14 | `TestCreateBackupArchive_ExcludesMailCache_KeepsControl` | Integration (actual archives) | B-14, MC-7, MC-23 | Every application archive excludes cache/watcher current/temp/retired paths and repository-history markers; ordinary state/saved-file controls present |
| 15 | `TestRestore_SkipsMailCacheMembers` | Integration (actual restore) | B-15, MC-7, MC-23 | Old/hostile current/history fixtures do not materialize; ordinary saved files/provenance restore normally |
| 16 | `TestProductMailExclusionReceipt` | Platform integration campaign | B-9, B-16, MC-6, E1–E5 | Fresh/upgrade/later-repository roots protected by product-only initialization; independent Git policy/staging oracle, actual tracked/index paths, actual archives/restores, both failure directions and positive controls. Product security path never shells out |
| 17 | `TestDeleteMailbox_RemovedPurge` | Integration | B-17, MC-8, MC-23 | Full removal means cache **and watcher state** gone; saved files/provenance remain unchanged |
| 18 | `TestDeleteMailbox_FailedUnlinkIsCleanupPending` | Integration (fault each target) | B-18, MC-8, MC-23 | Either purge failure yields pending + safe code; after-config-row-gone authorized retry completes both purges, never false success |
| 19 | `TestRemoval_LateWriteCannotResurrect` | Integration (real writes/barriers) | B-19, MC-9, MC-23 | Paused cache and watcher completions cannot recreate files or advance state after deletion; no sleeps/polling |
| 20 | `TestAgentDelete_CascadesCacheSubtrees` / `TestWorkspaceDelete_CascadesCacheSubtrees` | Integration | B-20, B-21, MC-8, MC-23 | Both paths purge every pair's private cache/watcher state and surface mail-cleanup failure independently of unrelated directory deletion |
| 21 | `TestConfigChange_GenerationInvalidatesSnapshot` | Unit / restart integration | B-22, B-43, MC-10, MC-17 | Canonical identity + persisted epoch is the **one** construction; unchanged restart valid, relevant change and identical re-save invalid; same-reference credential replacement advances epoch; random pair ID stable |
| 22 | `TestStoreLocked_DistinctFromCacheMiss` | Unit | B-23, MC-10 | Locked/broken store is distinct and cannot fabricate live authentication or create a replacement key |
| 23 | `TestPreviewGrant_StoresMetadataOnly` | Unit (white-box) | B-24, MC-11 | Grant fields: identity/session/expiry only; zero bytes of any kind; URL list permitted |
| 24 | `TestPreviewServe_LiveBudgetedFetch` | Integration (counters) | B-25, MC-12 | Per-serve gated fetch counted; `no-store` present; nothing persists post-request |
| 25 | `TestPreviewMint_DialsNothing` | Integration (counters) | B-26 | Mint zero dials; missing-ref failure surfaces at first serve with the same safe 404 class |
| 26 | `TestPreviewServe_BackoffTypedRefusal` | Integration | B-27 | Backoff pair → zero dials + typed budget refusal on the serve path |
| 27 | `TestPreviewControls_Preserved` (family) | Integration | B-28, MC-13 | Cap-8-refuses 429; logout revocation; 404 parity; signature replace-on-mint @2 min outside the cap; consented proxy serves grant-recorded URLs only |
| 28 | `TestPreviewByteEndpoint_CapAndDisposition_DownloadStreams` | Integration (false metadata) | B-29, MC-14 | Over-cap actual bytes with lying metadata → preview aborts pre-success; same part downloads fully with attachment disposition |
| 29 | `TestMailDiagnostics_NoLeakMarkers` | Integration (marker server + capture) | B-30, MC-15, MC-18 | Zero forbidden markers in actual operational outputs/state; safe classes present; **the same scan detects a deliberately marked control fixture and provider input** |
| 30 | `TestWatcherState_NoRawErrorText` (W1 oracle consumed by gateway seam) | Unit / Integration | B-31, MC-15 | W1's real persisted failure is safe, summary class-only shape unchanged; no W5 watcher fix/second redactor |
| 31 | `TestRemovalMutation_*` and the named tests below | Independent CHECK mutation audit | Every X-1…X-18 below | All counterexamples are mandatory kills with restoration/receipts; this row is not permission to skip a missing named test |
| 32 | `TestMailCache_WindowsCurrentUserACL` | Unit executed on **Windows** | B-32, MC-5 | Real native permissions and denied-access control, named test actually executed; CI workflow extension/signed Windows receipt required |
| 33 | `TestMailBoot_ReconcilesOrphanCacheAndWatcherState` | Integration (restart + unlink faults) | B-33, MC-8, MC-9, MC-23 | Orphan cache/watcher state never serves, is removed or visible/retryable pending; valid configured-pair and saved-file controls unaffected |
| 34 | `TestMailTrackedState_RemediationRunbookAndRefusal` | Integration / independent docs audit | B-34, MC-6, MC-7 | Actual tracked fixture blocks activation; owner-drafted runbook covers index removal, both rechecks and separately authorized history/external-copy handling; required in DoD |
| 35 | `TestMailGatewayInstrument_EmitsExactlyOneSafeRecord` | Integration (real seams + sink/log) | B-35, MC-18 | Success **and** failure for reads/mint/each serve/summary/removal; closed W6 shape, real supplied fields, exactly one sink/log record; **Q8 must be resolved by the publisher for removal**, never a local enum |
| 36 | `TestMailIdentity_OneConstructionAcrossConsumers` | Unit / restart integration | B-22, B-43, MC-17 | Random persisted 128-bit ID, stable on rotation; all consumers get the same canonical+epoch generation; identical re-save and credential replacement advance epoch; no secret-based alternative |
| 37 | `TestMailGateway_IssuesReferencesWithoutMessageID` | Integration (actual returned values) | B-36, MC-19 | Panel list/detail and injected agent metadata yield usable issued refs; no model/test-fabricated identity or second issuer |
| 38 | `TestMailGateway_StaleReferenceBeforeFetchOnSameLease` | Integration (folder recreation/barrier) | B-38, MC-19 | Authorized wrong pair/old generation/new epoch each typed 409 before content FETCH/STORE; a recreated UID never addresses the replacement message |
| 39 | `TestMailGateway_PagingSearchConsumesW2_25And200` | Integration / E2E | B-37, MC-20 | Real server SUBJECT/FROM/TO matches, 25/+25/200 boundary, no body/local-index search or reusable-cache growth; gateway never issues its own cursor |
| 40 | `TestMailGateway_StaleCursorReturnsTyped409` | Integration / E2E | B-38, MC-20 | Pair/folder/query/generation/epoch mismatch returns generated `stale_cursor`, reset once, no silent reinterpretation or loop |
| 41 | `TestMailIntegration_StaleGatedRefreshEvents` | Unit / Integration / W3 E2E seam | B-40, MC-22 | **Real W2 freshness/W3 event path**, exact 5-minute boundaries, manual Refresh and own mutation exceptions, separate 24-hour mapping rule, closed-panel zero refresh — not a test-coded copy of the gate |
| 42 | `TestMailReadMetadata_Phase1SourceScopes` | Unit / Integration | B-39, MC-21 | Folder mapping may say encrypted disk; headers/counts never do in Phase 1; all five mapping sources and classifier failure propagate honestly |
| 43 | `TestMailGateway_RevisionCaptureAndPublicationOrdering` | Integration (paused actual read/mutation) | B-41, MC-24 | One W1 capture before work, w5 advancement, W2 compare-before-publish: delayed completion updates no row/count/file/time and cannot relabel itself as new |
| 44 | **`TestMailFolders_UnknownCountIsNull`** | Gateway integration + generated-schema assertion | B-39, MC-21 | Unknown → null, confirmed absent → 0; valid generated controls and deliberate unknown→0 counterexample. **This exact name owns W6 M-α10's previously dangling gateway mutation target** (grill-4 I3) |
| 45 | `TestMailIntegration_SaveHandoffUsesOrdinaryWorkspacePolicy` | E2E (publisher-owned Library suite + gateway seam) | B-45, MC-23 | Temporary resource authority ends; saved markdown ordinary resources, saved HTML original bytes/provenance/scripts-off/per-file checkbox; move/copy/rename/restore and mailbox-removal controls |
| 46 | `TestMailGateway_SelectedPartPEEK_NoWholeMessageFallback` | Integration (real protocol/byte/flag counters) | B-44, MC-14, MC-19 | Gateway/Transfer consume W2 reader/classifier; selected nested part only, no `BODY.PEEK[]`/unrelated bytes, Seen unchanged; draft marker versus user `message.md` |
| 47 | `TestMailPresence_RestObserverIdOnFoldersAndList` | Integration (real authenticated WS + REST) | B-5, B-7, B-8, MC-4 | **Both** generated folder/list `observer_id` params bind the acknowledged W1 registry; foreign/stale/absent association retains no socket, no presence-created mailbox access |
| 48 | `TestMailCascade_DisableAndKeyLifecycle` | Integration (real lifecycle + paused writes) | B-42, MC-8, MC-9, MC-23 | Disable, store lock and key rotation use the same guarded invalidation; owned keys clear best-effort, relevant disposable files purge or pending surfaces, late work cannot publish, saved files unaffected |

### Test datasets

| Dataset | Rows / shape | Exercises | Traces to |
|---|---|---|---|
| DS-1 pairs | Three pairs: two on one account with different overrides, one unique; another authorized pair for reference-replay control | Result isolation, shared two-slot contention, stable distinct pair IDs | B-2, B-9, B-36, B-38, B-43 |
| DS-2 product exclusion | Clean/upgrade/no-repository/later-repository root; product establishment repeated; four staging/backup coverage cells; unreadable/negated effective policy, tracked sensitive fixture, exact-root versus similarly named unrelated directory | Product self-sufficiency, AND gate, precedence, tracking and conservative genuine failure; independent Git oracle, no product shell-out | B-9, B-10, B-11, B-16, B-34 |
| DS-3 permissions | Unix 0700/0600, pre-existing 0755 (tighten), cache and watcher unlink faults, directory/file symlink; **real Windows native ACL current-user and denied-access controls** | Creation/tightening/escape, truthful cleanup and platform proof, no chmod-only Windows claim | B-9, B-12, B-18, B-32 |
| DS-4 archives | Every product archive, protected current/temp/retired cache/watcher fixtures, synthetic repository objects containing their history, old/hostile archive, ordinary state and saved-file/provenance controls | Member **and content** absence; restore refusal of protected namespaces/history; ordinary user files restore normally | B-14, B-15, B-45 |
| DS-5 lifecycle | Live/tombstoned/config-row-gone/orphan pairs; cache **and watcher** writes paused before publication/write; successful and faulted unlink; disable/agent/workspace/remove/move/key triggers; saved-file control | No resurrection, honest both-target cleanup and restart reconciliation, separate saved-file lifecycle | B-17, B-18, B-19, B-20, B-21, B-33, B-42, B-45 |
| DS-6 identity | Unchanged restart; random persisted pair ID; same-reference credential replacement with epoch advance; identical re-save; host/port/username/override change; stopped-process endpoint edit; locked/broken store | One canonical+epoch generation, stable random pair ID, stale snapshot rejection and no fabricated credentials | B-22, B-23, B-43 |
| DS-7 previews/parts | Valid/missing/stale refs; nested MIME, inline-only CID, dedicated draft marker versus real user `message.md`; inline N=0/1/5; 8/9 grants, signature replacement, exact expiry, logout, backoff; decoded cap−1/cap/cap+1 with honest/unknown/false-low metadata; decode/disconnect fault | No-dial metadata mint, per-serve record, selected PEEK only, cap before success, larger Download separately, no byte keep/spool | B-24, B-25, B-26, B-27, B-28, B-29, B-44 |
| DS-8 leak controls | Synthetic subject/address/folder/URL/credential markers at the **start** and later positions in greeting/SELECT/BYE errors; provider input and separately marked capture fixture; safe-class positive output | Scan can actually detect a leak; truncation/raw-error logging fail; W1 watcher and w5 gateway boundaries both covered | B-30, B-31, B-35 |
| DS-9 time boundaries | Token just before/exactly/after expiry; message cap 8th/9th; ~2-minute idle release; mapping age 24 h−1 min / 24 h / 24 h+1 min; header/count age 5 min−1 s / **5 min** / 5 min+1 s; absent/invalidated data; manual/own-action/closed-panel controls | Existing token hygiene, distinct mapping/count/header clocks, stale-gated events and zero panel polling | B-5, B-28, B-40 |
| DS-10 pages/search | 0/1/24/25/26/199/200/201 matches, controlled larger folder; subject-only/from-only/to-only matches outside newest browse view; unicode/special-character query; cursor wrong pair/folder/query/config/epoch | Page/view bounds, actual header SEARCH, stable issued cursor binding, typed 409 and no local-index/body/cache expansion | B-37, B-38 |
| DS-11 metadata/revision | Unknown/present/confirmed-absent roles; mapping sources override/special-use/fallback/saved/none; Phase-1 disk mapping versus memory headers/counts; failed refresh; pre-mutation read released after new revision | Null is not zero, source scoping, metadata-only indicator, one captured revision and no stale publication | B-39, B-41 |
| DS-12 instrument/handoff | Every gateway operation success/failure; cache hit/live work/coalesced joiner/no-dial mint/summary; absent sink; temporary markdown resource attack versus real saved/ordinary controls; original-byte HTML and per-file allowance through move/copy/rename/restore | Safe complete frozen records and invalid missing-record campaign; **Q8 removal member publisher gate**; temporary-only resource authority and saved HTML rule | B-35, B-45 |

### Counterexamples a careless implementation would survive (mandatory)

| # | The mutation (what a careless implementer does) | The test that kills it |
|---|---|---|
| X-1 | Unknown/unreadable product coverage defaults to allowed | Row 11/DS-2 requires refused + visible `cache_unavailable` and no file; a valid both-covered control must still write |
| X-2 | Either cache or watcher unlink fails but removal logs it and returns `removed` | Row 18 faults **each** target and requires pending; row 17 is the successful-purge control |
| X-3 | Identity guard runs only at removal, not at late cache/watcher publication | Row 19 pauses actual writers at both pre-publish/pre-write points; release after tombstone must not create state |
| X-4 | Grant still stores HTML/bytes although serving stopped reading them | Row 23 inspects the actual store, independent of response content; payload retention alone kills it |
| X-5 | Registry teardown handles explicit close only, not socket death | Row 7 kills the real WS without close and observes registry/server socket cleanup; other-owner control survives |
| X-6 | Gate checks staging only, uses OR, or allows writes after backup-skip regression | Row 11's **four-cell AND** dataset independently breaks archive coverage; row 16 proves product establishment without external setup |
| X-7 | Literal-name matching misses policy negation/precedence, tracking or history; product archives skip only current cache files | Rows 11/14/15/16 use real effective-policy/staging and member/content/history fixtures, including unrelated-name and ordinary-file controls |
| X-8 | One of six injection sites constructs its own runtime, or outer and inner code both acquire an account slot | Rows 1/2/3 assert every injected instance and acquisition count through real paths; socket/account controls prevent a private bypass |
| X-9 | Raw-error string is truncated rather than classified, or the scan never sees content markers | Row 29/DS-8 place markers at start and later positions and require the same scan to find the marked control/provider input; row 30 consumes W1's actual persisted result |
| X-10 | Preview trusts reported size, or an aborted stream counts as completed | Row 28/DS-7 uses cap−1/cap/cap+1 and false-low/unknown metadata; over-cap/decode/disconnect abort before success, while larger browser Download still works |
| X-11 | A whole-message `BODY.PEEK[]` or unrelated attachment is a temporary fallback | Row 46's real protocol/byte counters kill it; returned bytes alone are insufficient proof of selected-part access |
| X-12 | No reference is issued without Message-ID, or a test/model fabricates one | Row 37 and B-36 chain only actual gateway/agent results from a no-Message-ID message; missing issued ref makes the real journey fail |
| X-13 | Separate consumers derive different/credential-based generations, or password rotation changes the path ID | Rows 21/36 assert the **one canonical+persisted-epoch** construction, same-value epoch advance and random-ID stability across restart/rotation |
| X-14 | Unknown count becomes zero or Phase-1 cached list source is encrypted disk | **Row 44 `TestMailFolders_UnknownCountIsNull`** and row 42 require null versus absent-zero and memory-header versus disk-mapping controls; this kills W6 M-α10, not an unnamed future test |
| X-15 | Fresh open/switch always issues live refresh, or sender/recipient-only older matches are not searched | Rows 41/39 use real event/freshness and server header-search paths; exact-five-minute fresh control and FROM/TO-only fixtures fail the shortcuts |
| X-16 | Resolve on one lease then act on another, accept old UID after epoch reset, or silently reinterpret a stale cursor | Rows 38/40 recreate the folder/replay bindings and require typed 409 before content/mutation, with a valid-ref/cursor control |
| X-17 | Emitter logs failures only, skips mint/summary/removal, double-counts joiners or silently replaces missing records with zero timings | Row 35 plus w6 T1–T6 require success/failure complete records and invalid absent-sink campaign; **Q8 is a publisher gate**, never permission to omit removal |
| X-18 | Boot serves orphan watcher/cache state, or Save inherits temporary restrictions/deletes the user's file during mailbox purge | Rows 33/45 use orphan success/failure, ordinary saved-resource requests, original HTML bytes/provenance/checkbox and saved-file lifecycle controls |

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

| Outside this package | Single owner / boundary |
|---|---|
| Pool/lease implementation, presence registry, coalescing/capture, watcher scheduling and watcher raw-error redaction | **W1**; w5 binds/injects the published capabilities, never patches W1's files |
| Envelope crypto, discovery, memory cache/freshness, server search/cursor issuer, part reader/MIME classifier and same-lease validator | **W2**; w5 consumes the freeze and publishes the one gate/identity/revision/issuer halves assigned to it |
| SPA implementation | **W3** Mail; **w4-features/ADR-W8** Library viewer; this package defines consumption/route obligations only |
| Contract/generated edits | **Backend-lead as ADR-W0**, not a missing W0 spec; §8 requests and consumes the Wave B union |
| Attachment Transfer/Save/receipt, Library storage/provenance/script allowance, parsed CSS policy, recipient rule and tool catalog | **w4-features (ADR-W7–W10)**; gateway Mail adapters remain exclusively w5's; no second service, scanner or approval layer |
| Phase 2 JMAP, trusted-origin probes or encrypted disk headers | **ADR-W6 later transport wave**, not w6-proof. Phase-1 exclusions/cascades are compatible with it, but no Phase-2 capability is delivered here |
| Executing history rewrite, index remediation or deleting external historical copies | This owner **must draft the product runbook** (US-4.3); execution requires explicit authorization through team-lead/founder. No side cleanup or assumed harmless exposure |
| Tests, mutation/CHECK execution, measurements or UAT | **qa-lead/w6-proof** and independent UAT lanes. w5 implements the emitter/envelope and Windows evidence path; it does not redefine the record or claim runs happened |
| Unrelated credential algorithms, shell/tool policy or backup-model changes | Preserve **Credential Boot Contract** and the two-layer tool model. The **necessary product-owned Mail staging/archive/restore exclusions are in scope**, not an outside deployment task |

**Correction-task fence:** this round edits/commits only this specification. No production code, real contract/generated edit, test execution, measurement, push, merge, history repair or user-page edit is performed here. Future user-page updates are the same-change implementation obligations in §11.

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
| Budget is shared process-wide by state-dir key | `pkg/email/mail_budget.go::SharedMailBudget`; injection `pkg/gateway/gateway.go::loadConfigAndProvider` (`agent.SetSharedMailBudget(email.SharedMailBudget(rc.homePath))` before `NewAgentLoop`); watcher `pkg/gateway/gateway_boot.go` (`email.NewMailboxWatcherSet(provider, stg.homePath, email.SharedMailBudget(stg.homePath))`); tools `pkg/agent/email_tools.go::registerEmailToolsForAgent` + `SetSharedMailBudget` | Verified |
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
