# ADR-004 — Credential Boot Contract

**Status:** Accepted
**Date:** 2026-04-11
**Deciders:** backend-lead, security-lead

---

## Context

The previous configuration format (v0) stored plaintext secrets (API keys, bot tokens, channel passwords) directly in `config.json`. The `.security.yml` approach used in PicoClaw has been removed. All secrets must now flow through the encrypted credential store (`credentials.json`, AES-256-GCM, Argon2id KDF).

Without a formal contract, callers were using `LoadConfig` (store=nil), which silently dropped plaintext secrets during v0→v1 migration and logged inject failures at `Warn` level, allowing the gateway to start with broken channels.

---

## Decision

### Boot Order Contract

Every production caller must follow this exact sequence, implemented in the shared `bootCredentials` helper (`pkg/gateway/gateway.go`):

```
NewStore → Unlock → LoadConfigWithStore → InjectFromConfig →
ResolveBundle → cfg.RegisterSensitiveValues(plaintexts) →
NewAgentLoop → WireSystemTools → NewManager(cfg, bundle, bus) → Start
```

1. **`credentials.NewStore(path)`** — construct the store (does not read disk, safe before any I/O)
2. **`credentials.Unlock(store)`** — decrypt the store using the master key (see Provisioning below). If this fails, boot aborts with a fatal error.
3. **`config.LoadConfigWithStore(path, store)`** — load config, migrating v0 configs by moving plaintext secrets into the store and returning a v1 config with `*Ref` fields. If the v0 config contains plaintext secrets and the store is nil or locked, the load fails with an actionable error.
4. **`credentials.InjectFromConfig(cfg, store)`** — resolve each provider `APIKeyRef` from the store and inject into the process environment so LLM SDK clients can read them. Any error is fatal at boot; reject the reload on hot-reload.
5. **`credentials.ResolveBundle(cfg, store)`** — resolve all channel `*Ref` fields into a `SecretBundle`. Channels receive secrets via the bundle — no `os.Setenv` for channel credentials. `ErrNotFound` for an **enabled** channel is fatal. `ErrNotFound` for a **disabled** channel is logged at Info and skipped.
6. **`cfg.RegisterSensitiveValues(plaintexts)`** — register all resolved plaintext values with the config's sensitive-data replacer so they are scrubbed from LLM output and audit logs. Semantics are "replace not append" — every call must supply the complete current set so that rotated or removed secrets are evicted.
7. **`agent.NewAgentLoop(cfg, msgBus, provider)`** — constructs the agent loop with the loaded config. Does NOT wire system tools (gateway-level concerns stay out of the constructor).
8. **`agentLoop.WireSystemTools(sysToolDeps, navCb)`** — registers the 35 `system.*` tools as `GuardedTool`-wrapped entries on the system agent. Each `GuardedTool.Execute` routes through `SystemToolHandler.Handle`, which enforces RBAC (SEC-19), rate limiting, confirmation (with a single-user bypass when `callerRole == RoleSingleUser` and `args.confirm == true`), and audit logging (SEC-15). The audit entry's `Details` map includes a `confirmation_source` discriminator (`"not_required"` / `"llm_arg_single_user"` / `"ui_button"`) so forensic review can distinguish LLM-self-approved destructive ops from user-approved ones. `sysToolDeps.MutateConfig` is wired to `agentLoop.MutateConfig` — sysagent writes acquire the single `al.mu` write lock (see Concurrency Contract below). `sysToolDeps.GetCfg` is wired to `agentLoop.GetConfig` so hot-reloaded configs are visible without re-wiring. `sysToolDeps.SaveConfigLocked(cfg)` persists to disk assuming `al.mu` is already held by the caller (i.e. by the enclosing `MutateConfig` callback). This step is fatal: if the system agent is not found, boot aborts.
9. **`channels.NewManager(cfg, bundle, bus, mediaStore)`** — channel constructors receive secrets via the `SecretBundle` parameter. If construction fails for an enabled channel, boot aborts.
10. **`manager.Start()`** — begin receiving messages.

**Hot-reload note:** `refreshConfigAndRewireServices` (called during `POST /reload` or hot-file-reload) calls `agentLoop.SwapConfig(newCfg)` but does NOT re-call `WireSystemTools`. This is correct because `sysToolDeps.GetCfg` delegates to `agentLoop.GetConfig()`, so system tools automatically see the new config after `SwapConfig` without requiring re-wiring.

### Concurrency Contract

Omnipus has exactly ONE write lock for the in-memory `*config.Config`: the agent loop's `al.mu` (`sync.RWMutex`). All config-write paths must go through `AgentLoop.MutateConfig(fn)`, which acquires `al.mu.Lock()`, calls `fn(al.cfg)`, and releases. Reads go through `AgentLoop.GetConfig()` which acquires `al.mu.RLock()`.

Rules:

1. **Sysagent writes** acquire `al.mu` only. `Deps.WithConfig(fn)` routes through `MutateConfig` and calls `SaveConfigLocked(cfg)` from inside the callback; `SaveConfigLocked` MUST NOT acquire any additional mutex — it only writes to disk.
2. **REST writes** (`safeUpdateConfigJSON`) hold the REST-local `configMu` for read-modify-write cycles and then call through to `AgentLoop` methods that take `al.mu`. Lock order is always `configMu → al.mu`, never the reverse.
3. **Never acquire two mutexes across the agent/config boundary in different orders.** The round-2 design introduced a `gatewayConfigMu` as a shared mutex between REST and sysagent; that produced a classic AB-BA deadlock (REST took `gatewayConfigMu → al.mu`; sysagent took `al.mu → gatewayConfigMu`). Round 3 deleted `gatewayConfigMu` and consolidated on `al.mu` as the single source of truth. Do not reintroduce a second boundary mutex.
4. **`configMu` is REST-local.** It serializes concurrent REST handlers so two `PATCH /config` calls cannot trample each other's read-modify-write cycle. It is NOT shared with sysagent and sysagent code MUST NOT import or reference it.
5. **Rollback on error.** `Deps.WithConfig` JSON-snapshots the config before calling `fn`; on either `fn` error or `SaveConfigLocked` error, it calls `restoreConfig` which uses a reflection-based `clearMaps` walker to zero map fields before `json.Unmarshal`-ing the snapshot back into `cfg` (because Go's stdlib `json.Unmarshal` extends maps rather than replacing them).

Violations of rule 3 are invisible at compile time and often invisible in unit tests — they only manifest as production deadlocks under concurrent load. Reviewers should reject any PR that adds a second mutex on this boundary.

### Canonical Shared Helper

`bootCredentials(homePath, configPath string)` in `pkg/gateway/gateway.go` is the single implementation of the above sequence. Both `gateway.Run` and `pkg/gateway/boot_order_test.go` call this helper so that a refactor of one cannot silently drift from the other.

REST-initiated config writes (`safeUpdateConfigJSON`) also rewire sensitive-data scrubbing via `restAPI.refreshConfigAndRewireServices`, which runs steps 3–6 on the new config and atomically swaps it via `agentLoop.SwapConfig`.

### Master-Key Provisioning

The credential store is unlocked using the first available source (in priority order):

1. **`OMNIPUS_MASTER_KEY`** — 64-character hex-encoded 256-bit key set in the environment. Recommended for CI/CD and container deployments.
2. **`OMNIPUS_KEY_FILE`** — path to a file containing the hex key, mode 0600. Recommended for server deployments where env injection is impractical.
3. **Default key file** — `$OMNIPUS_HOME/master.key` (mode 0600). Loaded automatically when neither env variable is set. This is where auto-generated keys live across reboots.
4. **Auto-generate on fresh install** — when no env key is set, no default key file exists, **and** no `credentials.json` exists, the gateway mints a fresh 32-byte key via `crypto/rand`, writes it to `$OMNIPUS_HOME/master.key` with mode 0600 using `O_EXCL` (atomic against concurrent boots), logs a prominent backup warning to stderr, and continues boot. Auto-generate **never** fires when an existing `credentials.json` is present — doing so would strand the encrypted data.
5. **Interactive TTY prompt** — passphrase entered at the terminal. Only available when a TTY is attached (not suitable for headless/daemon mode).

The auto-generate path (mode 4) closes the headless first-run chicken-and-egg: a new user on a cloud VPS can start the gateway with zero configuration and still end up with a working encrypted credential store. Subsequent boots pick up the same key via mode 3. Losing `$OMNIPUS_HOME/master.key` makes every credential in `credentials.json` permanently inaccessible, so the first-boot stderr warning is non-optional.

To generate a key file manually (for operators who prefer explicit provisioning over auto-generate):

```bash
openssl rand -hex 32 > /path/to/omnipus.key
chmod 600 /path/to/omnipus.key
export OMNIPUS_KEY_FILE=/path/to/omnipus.key
```

Headless deployments that already have a `credentials.json` (e.g., because the key was lost or rotated out-of-band) **must** provide a valid key via mode 1, 2, or 3. If none of those resolve and no TTY is available, `credentials.Unlock` returns an error and boot aborts — auto-generate (mode 4) will refuse to clobber existing encrypted data.

### Failure Semantics

| Scenario | Boot behavior | Hot-reload / REST-write behavior |
|----------|--------------|----------------------------------|
| `Unlock` fails | Fatal — abort boot | Reject reload, keep old config |
| `LoadConfigWithStore` fails | Fatal — abort boot | Reject reload, keep old config |
| `InjectFromConfig` returns any error | Fatal — abort boot | Reject reload, keep old config |
| `ResolveBundle` returns `ErrNotFound` for **enabled** channel | Fatal — abort boot | Reject reload, keep old config |
| `ResolveBundle` returns `ErrNotFound` for **disabled** channel | Info log, continue | Info log, continue |
| `RegisterSensitiveValues` (never errors) | — | — |
| `channels.NewManager` fails (enabled channel) | Fatal | Reject reload |

### Legacy v0 Migration

When `LoadConfigWithStore` encounters a v0 config file:

1. Each non-empty plaintext secret field is written to the credential store under a canonical ref name (e.g. `TELEGRAM_TOKEN`, `DISCORD_TOKEN`).
2. The corresponding `*Ref` field in the output config is set to that ref name.
3. A v1 config is written back to disk, replacing the v0 file. A backup of the original is kept at `config.json.bak`.
4. If the store is nil or locked during migration and the v0 config contains secrets, `LoadConfigWithStore` returns an actionable error: the operator must set `OMNIPUS_MASTER_KEY` and retry.

### Canonical Loader

`config.LoadConfigWithStore` is the **only** sanctioned loader for production callers. `config.LoadConfig` (store=nil) is reserved for CLI sub-commands that do not perform migration (e.g., `omnipus auth`) and for unit tests that work exclusively with v1 configs containing no plaintext secrets.

---

## Failure Modes and Recovery

### Boot-time failures (fatal)

Any failure in the boot sequence (Unlock, LoadConfigWithStore, InjectFromConfig,
ResolveBundle, NewManager) aborts the process with a descriptive error. The
operator must fix the root cause and restart.

### Reload-time failures (rollback + degraded)

When `POST /reload` or the hot-reload watcher triggers `executeReload`, any
failure in `InjectFromConfig`, `ResolveBundle`, `handleConfigReload`, or
`restartServices` is caught:

1. `executeReload` snapshots the pre-reload `services` state via
   `snapshotServices` (captures bundle, ChannelManager, CronService,
   HeartbeatService, MediaStore, DeviceService).
2. On failure, `markDegraded` calls `restoreServices` to restore the snapshot
   and sets:
   - `services.reloadDegraded = true`
   - `services.reloadError = <wrapped error>`
3. `/health` returns HTTP 503 with:
   ```json
   {"status": "degraded", "reason": "config reload failed: <error>"}
   ```
4. Load balancers (k8s readiness probes, nginx, envoy) remove the pod from
   rotation automatically.
5. Old channels + services continue serving requests from the snapshotted
   state — no traffic interruption.

**Recovery:** fix `config.json`, POST `/reload` again. On success,
`clearDegraded` is called and `/health` returns 200.

**Partial rollback scope:** only the fields listed in `servicesSnapshot` are
restored (bundle, ChannelManager, CronService, HeartbeatService, MediaStore,
DeviceService). Cron task state, agent loop state, audit log, and other
side-effects that `stopAndCleanupServices` triggers are NOT rolled back.
Operators should prefer a full process restart for reloads that touch
cron/heartbeat/MCP state.

---

## Consequences

- Gateway always starts with a fully resolved environment or not at all.
- Operators get a clear error message when OMNIPUS_MASTER_KEY is missing.
- v0 → v1 migration is automatic and preserves all secrets.
- Hot-reload and REST config writes re-arm sensitive-data scrubbing so newly-added credentials are immediately scrubbed from LLM output and audit logs.
- Hot-reload failures roll back the in-memory service graph and set the process degraded. The old config continues serving while the operator fixes the new config.
- See also: `pkg/credentials/inject.go`, `pkg/config/config_old.go` (`MigrateWithStore`), `pkg/gateway/gateway.go` (`bootCredentials`, `Run`, `executeReload`, `snapshotServices`, `restoreServices`), `pkg/gateway/rest.go` (`refreshConfigAndRewireServices`, `safeUpdateConfigJSON`).
