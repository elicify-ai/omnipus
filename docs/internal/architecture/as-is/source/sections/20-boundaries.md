## Sign-in and who may do what

**One account, no roles.** The code has a single-account model. A search of `pkg/` and `cmd/` found no role or RBAC (role-based access control) model for people. `pkg/config/config.go::UserConfig` holds a username, password hash, token hashes and session data, and no role field. Other "role" hits are unrelated (chat message roles, browser accessibility roles). The names `adminWrap` and `requireAdminAuthz` are historical: the comment on `pkg/gateway/rest.go::adminWrap` says they gate by authentication plus the dev-mode-bypass guard, "rather than an admin-vs-user role check". Besides account users, the code builds synthetic identities: `cli` (the CLI token), `_dev_bypass` (bypass on, no Bearer header) and `_env_token` (the legacy environment token). They carry no role; no human role hierarchy follows from them.

**Sign-in type is fixed at build time.** `pkg/gateway/auth_mode.go::activeAuthMode` asks `pkg/config/edition.go::EditionAuthMode`. The core edition gets local mode (password login routes). Desktop and hosted get platform mode (a registered provider handles sign-in). An unknown edition gets no sign-in or onboarding routes. No config key switches modes.

**Three ways in.** `pkg/gateway/auth.go::checkBearerAuth` goes in this order:
1. No Bearer header and `dev_mode_bypass` on: allowed as `_dev_bypass`, with a warning.
2. No Bearer header otherwise: the `omnipus-session` cookie (the browser path), else 401.
3. Bearer header: `resolveBearerIdentity` tries account tokens and the CLI token, in an order that depends on token shape. No match while accounts exist gives 401.
4. No match and no accounts: the legacy `OMNIPUS_BEARER_TOKEN` variable, then bypass, then 401.

**Route classes.** `pkg/gateway/rest.go::registerCoreRoutes` does not require a signed-in session on every request.

| Class | Wrapper | Example |
|---|---|---|
| Public | none | `/metrics` |
| Optional sign-in | `withOptionalAuth` | `/api/v1/state` |
| Signed in | `withAuth` | `/api/v1/tasks` |
| Sensitive | `adminWrap` | `/api/v1/gateway/restart` |

`withOptionalAuth` does not reject an unmatched Bearer token. It tries the legacy environment token, then a valid session cookie (a planted-cookie check can still answer a typed 401), and only then lets the request through as anonymous (`pkg/gateway/rest_auth.go::withOptionalAuth`). `adminWrap` is `withAuth` then `middleware.RequireNotBypass`, which answers 503 while bypass is on or the config snapshot is missing. `requireAdminAuthz` applies only that guard inside a handler already signed in. CSRF (`pkg/gateway/middleware/csrf.go`) uses a double-submit cookie on state-changing methods and skips safe methods and Bearer requests.

## Wire contracts: REST and WebSocket

**Sources and generation.** `contracts/openapi.yaml` describes REST. `contracts/asyncapi.yaml` describes WebSocket frames at `/api/v1/chat/ws`. Shared schemas are in `contracts/components/schemas/`. `scripts/gen-contracts.sh` lints both specs, generates TypeScript types and Zod schemas (Zod is a TypeScript validation library), generates Go types, formats them, and copies the schemas into `pkg/gateway/inboundschemas/` for embedding. Outputs are `pkg/api/generated/` and `src/lib/api/generated/`. The script was read, not run.

**Server validation is not uniform.**
- **REST.** `pkg/gateway/rest_inbound_validate.go::decodeAndValidate` takes a `validateEnabled` flag. Off: it only decodes JSON (1 MB limit). On: it checks the named schema first and answers 400 on mismatch. `decodeAndValidateStrict` also refuses unknown fields when validation is off. The config comment says `gateway.validate_inbound` defaults to false. Nine places in `pkg/gateway` still call `json.NewDecoder(r.Body)` directly (count only, not classified).
- **WebSocket.** `pkg/gateway/websocket.go` calls `ValidateInboundFrameJSON` only when `validate_inbound` is on, and skips `attach_session` in that main loop. Attach is checked later, after the session access check: the history options are parsed and, when `validate_inbound` is on, the frame is validated against `AttachSessionFrame` (`pkg/gateway/history_page_websocket.go::validateHistoryAttach`). A bad frame is dropped, counted and answered with an error frame. Browser-input paths call it directly.
- **Boot.** `PreCompileAllInboundSchemas` aborts boot if a schema fails to compile.

**Browser side.** `src/lib/ws.ts::parseFrameSafe` checks every frame with `WsFrameSchema.safeParse`. Bad, unknown or wrong-direction frames are dropped and counted. REST responses are validated in `src/lib/api/http.ts` only when a schema is passed to `request()`.

**Text drift.** The OpenAPI server example uses port 5000. The AsyncAPI example uses `localhost:6060`. The default in `pkg/config/defaults.go` is 5000.

| Family | Path prefix | Handler file |
|---|---|---|
| Chat stream | `/api/v1/chat/ws` | `pkg/gateway/websocket.go` |
| Sessions | `/api/v1/sessions` | `pkg/gateway/rest_sessions.go` |
| Agents | `/api/v1/agents/` | `pkg/gateway/rest_agents.go` |
| Providers | `/api/v1/providers` | `pkg/gateway/rest_providers.go` |
| Channels | `/api/v1/channels` | `pkg/gateway/rest_channels.go` |
| Tasks | `/api/v1/tasks` | `pkg/gateway/rest_tasks.go` |
| Uploads, media | `/api/v1/uploads/`, `/media/` | `pkg/gateway/rest_uploads.go` |
| Credentials, audit log | `/api/v1/credentials`, `/audit-log` | `pkg/gateway/rest_settings.go` |
| Sandbox config | `/api/v1/security/sandbox-config` | `pkg/gateway/rest_sandbox_config.go` |

## Secrets and keys

**Order at boot.** `pkg/gateway/gateway_boot_credentials.go::bootCredentials` unlocks the credential store, loads config with it (`LoadConfigWithStore`), checks the edition, injects provider values, resolves the channel bundle, then checks store health.

**Master key sources.** `pkg/credentials/keymgr.go::Unlock` takes the first that applies:
1. File named by `OMNIPUS_MASTER_KEY_SOURCE` (`unlockFromDeliveredKey`): refused if a persisted `master.key` also exists, if the delivery path is a symlink, or if the key does not decrypt an existing store; those failures are fatal with no fall-through, and the file is left in place. After a successful unlock the file is deleted. If that deletion fails (for example a read-only mount) boot continues, a warning is logged and the key stays readable at that path. This mode does not persist the key, so it must be delivered again on each boot.
2. `OMNIPUS_MASTER_KEY`.
3. `OMNIPUS_KEY_FILE`.
4. `master.key` in the home folder.
5. A newly generated `master.key`, only if no store exists.
6. A typed passphrase; with no terminal, an error.

**The store.** `pkg/credentials/store.go::encrypt` seals each entry with AES-256-GCM and a random nonce. `aadFor` ties the ciphertext to the entry name, so a copy under another name fails to open (`decrypt`). Config holds only `*_ref` references.

**Two routes out.** `credentials.InjectFromConfig` puts provider keys into the process environment. `credentials.ResolveBundle` builds the `SecretBundle` for channels and tools. Resolved values are registered for redaction.

**Reload.** Keys are not only read at start-up. `pkg/gateway/gateway_reload.go::prepareReloadConfig` re-injects from the live store. `pkg/gateway/rest_config.go::refreshConfigAndRewireServices` reloads config, re-resolves and rechecks health.

**Failure scope.** A missing reference degrades only its provider, mailbox or channel (ERROR log). A damaged single entry waits for `HealthCheck()`: if another entry decrypts, only that consumer degrades; if none do, boot or reload fails. Locked store, unreadable document and unknown errors are fatal.

## Audit

**Who writes rows.** Rows are JSON lines in `system/audit.jsonl`, written by `pkg/audit/audit.go::Log` and `writeLine`. Tool registry: `pkg/tools/registry.go::ExecuteWithContext` writes `tool_call`. Policy and approval: `pkg/agent/loop_policy.go` and `pkg/audit/events.go` write deny and ask rows. Shell: `pkg/tools/shell.go` writes `exec`, and `pkg/audit/shell_permission_events.go` covers grants and decisions. The number of rows one call can write was not counted.

**Integrity.** `writeLine` adds an `hmac` field: an HMAC-SHA256 keyed checksum over the previous row's checksum plus this row's canonical JSON. The key is derived from the master key at boot (`pkg/gateway/gateway.go`) and kept in memory. If adding it fails, the row is written without it and the chain does not advance. `pkg/audit/verify.go` checks chains.

**Writing and rotation.** Each write is flushed. Denials, errors, `boot.abort` and two policy-deny events are also fsynced (forced to disk). Allow rows are not, so a hard kill can lose them. The file rotates at a new UTC day or 50 MB; the chain carries across files. Retention is 90 days.

**Failure.** A failed raw write or a failed rotation puts the logger in degraded mode, and later writes return errors. A failed flush or a failed fsync of a critical row returns an error for that call but does not set the degraded latch (`writeLine`). `EmitEntry` counts them as skipped. The registry only logs the error, so a successful tool call does not prove its row was saved. A nil logger is a silent no-op.

**On or off.** `pkg/agent/loop_construct.go::initializeAudit` builds the logger when `sandbox.audit_log` is true, which is the default seed. If construction fails and the config explicitly set it, boot aborts. If audit was only on by default, boot continues without audit, logs an ERROR and `/health` reads degraded.
