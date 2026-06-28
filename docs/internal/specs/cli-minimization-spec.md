# Feature Specification: CLI Minimization — thin one-shot task-runner

**Created**: 2026-06-28
**Status**: Draft
**Input**: `docs/internal/architecture/ADR-024-cli-minimization.md` (Accepted-pending), red-teamed by `ADR-024-cli-minimization-review.md` (PASS-WITH-CONDITIONS). Operator decisions captured 2026-06-28.

> **Phasing (operator-decided).** **P0** = execute against a *running* gateway (clear error if down) + onboard menu/URL + start URL + credentials + FR-005 approval handling + FR-006 `cli` token + hard-removal of dead verbs. **P1 (fast-follow)** = auto-start (the agent-run command invoking `start`) + `omnipus stop` + shared `pkg/daemon`. P1 stories are included for completeness and marked **(P1)**.

> **Revision 2 (2026-06-28, post-grill — `cli-minimization-spec-review.md`).** Folded in: **CR-1** — approvals resolve via REST `POST /api/v1/tool-approvals/{approval_id}` (not the legacy `exec_approval_response` WS frame, which would hang 90 s); the run is **WS-to-observe + REST-to-resolve**. **CR-2** — `user=cli` attribution requires new plumbing, now IN scope (FR-017: add `audit.Entry.User` + thread `userID`). **Token lifecycle** — `start` mints create-if-absent; `--new-cli-token` rotates (FR-018); a stale token gets a distinct message (FR-019). **`--url`** out of P0 (FR-007). **Majors** — declared kept commands (`audit`/`doctor`/`version`); grep-guard regex now includes `model|skills` + path-prefixed forms; P1 health-poll probes real WS acceptance (not `/ready`); `gateway.lock` dropped (OS port-bind is the mutex); integration tests use a mock provider + `-p 1`; `Role` documented as unenforced at the tool layer.

---

## Available Reference Patterns

| Source | Pattern | Relevance |
|---|---|---|
| `cmd/omnipus/internal/onboard/onboard.go:54-66,338-431` | `wizardIO` scripted-stdin + `prompt()` | Reuse for the interactive numbered provider menu (US-8) and its tests. |
| `cmd/omnipus/internal/onboard/onboard.go:516-522,480-491,524-623` | `generateBearerToken` + bcrypt + `mutateConfigFile` + `fileutil.WriteFileAtomic(…,0o600)` | Extend to mint the `cli` principal + write `cli.token` (US-5/US-8). |
| `cmd/omnipus-launcher-tui/ui/gateway.go:34-160` | PID file (`~/.omnipus/gateway.pid`, 0600), `nohup` spawn, `kill`+verify, Windows `wmic`/`taskkill` | Extract into `pkg/daemon` for `omnipus stop`/auto-start (US-12/US-13, P1). |
| `pkg/credentials/keymgr.go:37-143` | `Unlock` priority (MASTER_KEY / KEY_FILE / default file / auto-gen / interactive) | Non-interactive precondition gate before auto-start (US-12, P1). |
| `contracts/asyncapi.yaml` | WS frames: `auth`, `message`, `session_started`, `token`, `tool_call_start/result`, `tool_approval_required`, `done`, `error` | The execute client OBSERVES the run over WS. |
| `contracts/openapi.yaml` + `pkg/gateway/rest_tool_registry.go:415` | `POST /api/v1/tool-approvals/{approval_id}` `{"action":"approve"\|"deny"}` (`ToolApprovalActionRequest`) | The execute client RESOLVES ask-policy approvals over REST (the live `approvalRegistryV2` gate; the `exec_approval_response` WS frame resolves only the *legacy* registry and would hang the run). No new wire types. |

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|---|---|---|
| `cmd/omnipus/main.go::NewOmnipusCommand` | modifies | Root command: add `<agent>` positional run path + no-args roster; drop removed subcommands. |
| `cmd/omnipus/internal/agent/*` | deletes | The broken in-process REPL — removed wholesale. |
| `cmd/omnipus/internal/{auth,status,cron,migrate,model,skills}` | deletes | Hard-removed (dead OAuth store / retired tool / OpenClaw / duplicated). |
| `cmd/omnipus/internal/gateway/command.go` | extends | `start` gains bind-aware URL block + ensures `cli.token`. Keep `gateway`/`g` aliases. |
| `cmd/omnipus/internal/onboard/onboard.go` | extends | Numbered provider menu + URL block + mint `cli` principal/token. |
| `cmd/omnipus/internal/credentials/command.go` | unchanged | Kept as-is (regression-protected). |
| `pkg/config/config.go::AgentConfig.IsChatTarget` (line 955) | calls | Filter roster to chat targets for the no-args listing and run validation. |
| `pkg/config/config.go::ModelConfig` / `Config.Agents.List` | calls | Roster + model read from local config.json (offline). |
| `pkg/gateway/websocket.go` auth (`~630-710`) + `handleApprovalResponse` (`~1716-1766`) | calls (over the wire) | The execute client authenticates and answers approvals against these. |
| `pkg/health/server.go` `/health` `/ready` | calls (over the wire) | `/health` 200 = "a gateway is up" (P0 reachability). NOTE: `/ready` flips green in `SetupHTTPServer` BEFORE the WS listener binds — auto-start (P1) MUST probe real WS acceptance, not `/ready`. |
| `cmd/omnipus-launcher-tui/ui/gateway.go` PID logic | extends → `pkg/daemon` | Reconciled into the shared daemon package (P1). |

### Impact Assessment

| Symbol Modified | Risk | d=1 Dependents | d=2 |
|---|---|---|---|
| `NewOmnipusCommand` (command tree) | **HIGH** | Docker entrypoints, CI workflows, CI worker, launcher, `install.sh`, docs | end users / scripts |
| `onboard.applyInput` / `mutateConfigFile` | MEDIUM | `onboard` tests; gateway boot reads the new `cli` user | `bootCredentials` user list |
| launcher PID logic → `pkg/daemon` | MEDIUM | `omnipus-launcher-tui` | `omnipus stop`/auto-start (P1) |
| removed verbs | **HIGH** | `docker/Dockerfile*`, `docker/entrypoint.sh`, `.github/workflows/*`, `deploy/ci-worker/runci.sh`, `scripts/install.sh`, `docs/using-omnipus-cli.md` | external scripts |

> **Constraint #7 deliverable:** every internal caller of a removed verb MUST be updated in the same PR, and a CI grep-guard MUST assert no infra references the removed verbs. The kept `gateway` alias back-stops `omnipus gateway` callers; prefer migrating them to `omnipus start`.

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| WS chat turn (`message` → `session_started` → `token*` → `tool_*` → `tool_approval_required?` → `done`) | The one-shot run consumes this; `done.stats` ends it. |
| Credential boot (`bootCredentials`: Unlock→Inject→Resolve) | Auto-start's spawned gateway runs it; needs non-interactive unlock (P1). |
| Default-agent / chat-target routing (`route.go:296`, `rest.go:893`) | `<agent>` must be a chat target; workers rejected. |

---

## User Stories & Acceptance Criteria

### User Story 1 — Run a one-shot task on a named agent (Priority: P0)

A CLI user runs `omnipus <agent> "<prompt>"`, the named agent executes the task against the live engine, the result prints, and the process exits. This is the entire purpose of the CLI.

**Why this priority**: It is the feature. Without it nothing else matters.
**Independent Test**: With a running gateway and a valid `cli.token`, `omnipus jim "say hello"` prints Jim's reply to stdout and exits 0.

**Acceptance Scenarios**:
1. **Given** a running gateway and a valid `cli.token`, **When** `omnipus jim "2+2?"`, **Then** the assistant's answer streams to stdout and the process exits 0 after the `done` frame.
2. **Given** the gateway is **not** running, **When** `omnipus jim "hi"`, **Then** stderr prints `Omnipus isn't running — start it with: omnipus start` and the process exits non-zero (no in-process engine is opened).
3. **Given** `<agent>` is omitted (`omnipus`), **Then** the roster + usage is printed (US-6), not an error.
4. **Given** `<agent>` names a worker or a non-existent agent, **When** run, **Then** stderr prints a clear "not a runnable agent / unknown agent" message and exits non-zero.

---

### User Story 2 — Per-run model override (Priority: P0)

`omnipus <agent> --model <slug> "<prompt>"` runs the agent with the model overridden for that turn only, falling back to the agent's configured model when absent.

**Why this priority**: Cheap, expected, and already supported by the wire (`metadata.model_name`).
**Independent Test**: `omnipus jim --model openrouter/glm-5.2 "hi"` causes the run to use that model (observable in `done.stats`/audit), and omitting it uses Jim's configured model.

**Acceptance Scenarios**:
1. **Given** `--model X`, **When** run, **Then** the `message` frame carries `metadata.model_name=X`.
2. **Given** no `--model`, **When** run, **Then** `metadata.model_name` is omitted and the agent's configured model is used.

---

### User Story 3 — Scriptable output (Priority: P0)

The agent's final result goes to **stdout**; progress, tool activity, and diagnostics go to **stderr**, so `omnipus jim "…" > out.txt` captures only the answer.

**Why this priority**: A CLI that isn't pipe-clean is not a usable execution mechanism.
**Independent Test**: `omnipus jim "echo: banana" 1>out 2>err` — `out` contains only the result; tool/progress lines are in `err`.

**Acceptance Scenarios**:
1. **Given** a run that streams tokens and calls tools, **When** redirected, **Then** stdout holds the assembled result text only; tool-call/progress lines are on stderr.
2. **Given** a run that errors mid-turn (`error` frame), **Then** the error message is on stderr and the exit code is non-zero.

---

### User Story 4 — Ask-policy tools on a one-shot run (Priority: P0)

When the agent triggers a tool that requires approval, a non-interactive one-shot must not hang or silently misbehave: it **default-denies and continues**, noting the blocked tool on stderr. `--yes` opts into auto-approve for that run (still bounded by the agent's own policy).

**Why this priority**: Without this, any `ask`-policy tool (e.g. Jim's shell) makes the happy path hang ~90 s then fail — an undefined failure mode.
**Independent Test**: Run an agent whose task triggers an `ask` tool; without `--yes` the tool is denied (logged to stderr) and the run completes; with `--yes` it is allowed.

**Acceptance Scenarios**:
1. **Given** a run with no `--yes`, **When** a `tool_approval_required` WS frame arrives, **Then** the CLI reads its `approval_id` and immediately issues `POST /api/v1/tool-approvals/{approval_id}` `{"action":"deny"}` (bearer = `cli` token), prints `denied tool: <name>` to stderr, and the run continues to `done` (no 90 s wait).
2. **Given** `--yes`, **When** a `tool_approval_required` frame arrives, **Then** the CLI issues `{"action":"approve"}`, and the tool runs subject to the agent's policy.
3. **Given** `--yes` but the agent's own policy forbids the tool, **Then** the tool is still blocked by the engine (the flag never overrides server-side policy).

---

### User Story 5 — CLI authenticates as a dedicated `cli` principal (Priority: P0)

The CLI authenticates to the gateway with a token it owns, minted as a distinct `cli` user, so CLI actions are **audit-attributable as `cli`** and **revocable independently** of the human admin.

**Why this priority**: The gateway fails closed; without a CLI token the CLI cannot talk to its own engine. A dedicated principal is the secure, attributable answer (loopback-trust is forbidden).

**Audit-attribution plumbing (in scope, operator-decided):** today `audit.Entry` has no user field and the WS-authenticated `userID` is not threaded into agent-loop audit emissions, so `user=cli` is not deliverable as-is. This story therefore **adds `audit.Entry.User`** and **threads the authenticated `userID` (e.g. `wc.userID`) through the turn into audit emission** (see FR-017). Blast radius: `pkg/audit` + `pkg/agent/loop.go` audit call sites.

**Role caveat (MAJ-4):** `UserConfig.Role` is currently **not enforced at the tool/approval layer** (cosmetic), so an "admin-equivalent" `cli` principal has the full blast radius. Accepted for v1. **Revocation** = remove the `cli` user (or `omnipus start --new-cli-token` to rotate). A scoped CLI role is tracked as the real EoP mitigation.

**Independent Test**: After onboarding, `$OMNIPUS_HOME/cli.token` exists (0600), `config.Gateway.Users` contains a `cli` user, and a run's audit entries carry `User="cli"`.

**Acceptance Scenarios**:
1. **Given** a fresh onboard, **Then** a `cli` user is written to `Gateway.Users` (bcrypt `token_hash`, role admin) and the plaintext token is written to `$OMNIPUS_HOME/cli.token` (0600).
2. **Given** an existing install WITH a valid `cli.token`, **When** `omnipus start` runs, **Then** it leaves the token unchanged (create-if-absent); **Given** no token exists, **Then** it mints one.
3. **Given** `omnipus start --new-cli-token`, **Then** a fresh token replaces the old one (old token stops authenticating).
4. **Given** a run, **When** the WS opens, **Then** the first frame is `{type:"auth", token:<cli.token>}`, it authenticates without `dev_mode_bypass`, and audit entries carry `User="cli"`.
5. **Given** the gateway is running but `cli.token` is wrong/stale, **When** a run is attempted, **Then** the CLI prints `Your CLI key is invalid or out of date. Run: omnipus start` (distinct from the gateway-down message) and exits non-zero.
6. **Given** a tampered/rotated token, **When** auth is attempted, **Then** the gateway rejects it.
7. **Given** `--url`, **When** invoked in v1, **Then** the CLI errors `remote gateways are not supported yet` (the `http://`/local-token guardrails remain as defensive checks); remote is out of P0 scope.

---

### User Story 6 — Bare `omnipus` lists agents + usage offline (Priority: P0)

`omnipus` with no args prints the runnable agents and the command usage, reading the roster from local `config.json` so it works with no gateway running.

**Why this priority**: Discovery — a mandatory-agent CLI is unusable if you can't see the agent names.
**Independent Test**: With a config present and no gateway running, `omnipus` lists the chat-target agents (id + name) and the usage block; exit 0.

**Acceptance Scenarios**:
1. **Given** a config with seeded agents and no gateway, **When** `omnipus`, **Then** chat-target agents are listed (id, name) with the run-usage line and the command list; exit 0.
2. **Given** workers in the roster, **When** listed, **Then** workers are excluded.
3. **Given** no config yet (pre-onboard), **When** `omnipus`, **Then** it prints a "run `omnipus onboard` first" hint; exit non-zero.

---

### User Story 7 — `omnipus --help` curated reference (Priority: P0)

`omnipus --help`/`help` prints a curated reference explaining the few commands and the `<agent> "<prompt>"` execute form with examples.

**Why this priority**: Standard discoverability; the execute form isn't a subcommand so cobra won't document it automatically.
**Independent Test**: `omnipus --help` lists `onboard`, `start`, `credentials`, the execute usage, and examples; exit 0.

**Acceptance Scenarios**:
1. **Given** `omnipus --help`, **Then** output documents the execute form, `onboard`, `start`, `credentials`, and shows ≥2 examples; exit 0.
2. **Given** a removed verb in help, **Then** it does **not** appear.

---

### User Story 8 — Interactive `onboard` with numbered provider menu + URL (Priority: P0)

`onboard` presents a numbered provider menu (type a number), prompts for key/model/admin, mints the `cli` token, and prints the access URL with next-steps. Headless `--non-interactive` is preserved.

**Why this priority**: First-run convenience + the bootstrap of `cli.token` and the URL the operator needs.
**Independent Test**: Scripted stdin selecting provider "1" completes onboarding, writes config + `cli.token`, and the output includes the access URL and `omnipus start` instruction.

**Acceptance Scenarios**:
1. **Given** interactive `onboard`, **When** the user types a provider number, **Then** that provider is selected; an invalid number re-prompts.
2. **Given** completion, **Then** the output prints the access URL block (US-9 format) framed as next-step and confirms `cli.token` was written.
3. **Given** `--non-interactive` with flags, **Then** behavior is unchanged except it also mints `cli.token` and prints the URL.

---

### User Story 9 — `start` prints a bind-aware access URL (Priority: P0)

On boot, `start` prints where to open the dashboard: always `localhost`; the LAN IP when bound to `0.0.0.0`/empty; `public_url` when set; with a hint to set `gateway.host=0.0.0.0` when bound to loopback.

**Why this priority**: Operators currently get only `Gateway started on host:port`; they need a clickable URL + guidance.
**Independent Test**: With `host=0.0.0.0`, `start` prints both `http://localhost:<port>` and `http://<lan-ip>:<port>`; with `host=127.0.0.1`, only localhost + the hint.

**Acceptance Scenarios**:
1. **Given** `host` is `0.0.0.0`/empty, **When** `start`, **Then** localhost and the detected LAN IPv4 URLs are printed.
2. **Given** `host=127.0.0.1`, **When** `start`, **Then** only localhost is printed plus the `gateway.host=0.0.0.0` hint.
3. **Given** `gateway.public_url` set, **When** `start`, **Then** the public URL is shown as canonical.
4. **Given** no `cli.token` exists, **When** `start`, **Then** it mints one (US-5 AC-2).

---

### User Story 10 — `credentials` retained, including `rotate` (Priority: P0)

`omnipus credentials set/list/delete/rotate` is preserved unchanged for headless/pre-boot/recovery secret management.

**Why this priority**: Headless secret management has no other home; rotation is recovery-critical.
**Independent Test**: The existing `credentials` tests still pass; `list` shows names only; `rotate` re-encrypts.

**Acceptance Scenarios**:
1. **Given** the redesign, **When** the command tree is built, **Then** `credentials` and its four subcommands still exist and behave as before.

---

### User Story 11 — Hard-remove dead verbs + fix all internal callers (Priority: P0)

`agent`, `auth`, `status`, `cron`, `migrate`, and standalone `model`/`skills` are deleted. **Kept commands:** `onboard`, `start` (+ `gateway`/`g` alias), `credentials`, `audit`, `doctor`, `version`. All internal callers are updated and a CI guard prevents regressions. Reserved-name set (agent IDs may not shadow these): the full kept-command set above.

**Why this priority**: These read dead stores / retired features; leaving them is the drift this redesign exists to end. Constraint #7 forbids breaking our own pipeline.
**Independent Test**: `omnipus status` → prints `"status" was removed in the CLI redesign — run 'omnipus --help' for the current commands` (stderr) and exits non-zero; `grep -rE '(omnipus|\./omnipus|[A-Za-z0-9_/]*/omnipus|\$[A-Z_]+) (agent|auth|status|cron|migrate|model|skills)\b'` over Docker/CI/worker/launcher/install.sh is empty; `omnipus gateway` still maps to `start`.

**Note (implementation detail):** Because the root command uses `cobra.ArbitraryArgs`, removed verbs reach `RunE` rather than cobra's built-in "unknown command" handler. `RunE` checks `args[0]` against the `removedVerbs` map and prints a dedicated redesign-removal message before the agent-lookup so the user receives actionable guidance rather than the confusing "unknown agent" error.

**Acceptance Scenarios**:
1. **Given** the new tree, **When** a removed verb is invoked (e.g. `omnipus status`), **Then** stderr prints `"<verb>" was removed in the CLI redesign — run 'omnipus --help' for the current commands` and the process exits non-zero.
2. **Given** the same PR, **Then** no removed verb is referenced in: runtime callers (`docker/Dockerfile{,.full,.heavy,.goreleaser}`, `docker/entrypoint.sh`, `.github/workflows/cross-platform.yml`, `pr.yml`, `deploy/ci-worker/runci.sh`, `cmd/omnipus-launcher-tui/ui/gateway.go`, `scripts/install.sh` — all currently SAFE via the kept `gateway`/`credentials`, migrate `gateway`→`start` for consistency); docs (`docs/using-omnipus-cli.md`, `docs/ANTIGRAVITY_USAGE.md`, `docs/providers.md`, `docs/configuration.md`, `docs/channels.md`, `docs/channels/weixin.md`, `docs/channels/wecom.md`, `docs/skills.md`, `ROADMAP.md`, `docs/internal/architecture/ADR-004-credential-boot-contract.md`); and user-facing error strings (`pkg/providers/{claude,factory,codex,antigravity}_provider.go` "run omnipus auth login" → point at `omnipus credentials set` / onboarding).
3. **Given** `omnipus gateway`, **Then** it still runs `start` (alias kept).
4. **Given** CI, **Then** the grep-guard step (regex incl. `model|skills` and path-prefixed forms) fails if any removed verb reappears in infra, and is implemented as a shell/CI step (primary) — a Go mirror, if any, resolves repo root via go.mod and is `//go:build linux`.

---

### User Story 12 — Auto-start via `start` when the gateway is down **(P1)**

When no gateway is reachable, the agent-run command brings one up by invoking the same `start` path (persistent), polls health until ready, then runs the task — provided a non-interactive unlock mode is available.

**Why this priority**: The "just works" convenience; deferred to P1 because it carries the daemon/auth/unlock complexity.
**Independent Test**: With no gateway running and `OMNIPUS_MASTER_KEY` set, `omnipus jim "hi"` spawns a gateway, waits for health, returns the reply; a second concurrent invocation does not spawn a duplicate.

**Acceptance Scenarios**:
1. **Given** no gateway and a non-interactive unlock available, **When** `omnipus jim "hi"`, **Then** the CLI spawns `start`, polls for **actual WS acceptance** (TCP connect + WS upgrade + `auth` round-trip — NOT `/ready`, which flips green in `SetupHTTPServer` before the WS listener binds), runs the task, and leaves the gateway running.
2. **Given** no non-interactive unlock mode (no MASTER_KEY/KEY_FILE/master.key), **When** auto-start is needed, **Then** the CLI fails with guidance and does **not** attempt an interactive prompt.
3. **Given** two concurrent invocations with no gateway, **Then** the **OS port bind** is the mutex (only one process binds `Gateway.Port`); the loser observes the bind failure / the now-listening gateway and connects to it rather than spawning a duplicate. (No separate `gateway.lock` — flock is a no-op on Windows; the port bind is the real guarantee.)
4. **Given** the port is held by a foreign (non-omnipus) process, **Then** auto-start fails with a clear message.

---

### User Story 13 — `omnipus stop` + shared `pkg/daemon` **(P1)**

`omnipus stop` stops a CLI/launcher-started gateway via a shared `pkg/daemon` (one PID-file convention) reconciled with the desktop launcher.

**Why this priority**: Lifecycle control for the persistent auto-started gateway; depends on US-12.
**Independent Test**: After auto-start, `omnipus stop` terminates the gateway and removes the PID file; a stale PID file (dead/reused PID) is handled safely.

**Acceptance Scenarios**:
1. **Given** a running CLI-started gateway, **When** `omnipus stop`, **Then** it is terminated and the PID file removed.
2. **Given** a stale PID file (process dead), **When** `omnipus stop`, **Then** it reports "not running" and clears the file; it never kills an unrelated reused PID.
3. **Given** the launcher and CLI, **Then** both use `pkg/daemon` with the same PID path (no competing mechanisms).

---

## Behavioral Contract

Primary:
- When `omnipus <agent> "<prompt>"` runs against a reachable gateway, the system streams the agent's result to stdout and exits 0 after `done`.
- When `--model X` is given, the system overrides the model for that turn only.
- When `omnipus` is run with no args, the system lists chat-target agents + usage from local config.
- When `onboard`/`start` complete, the system prints a bind-aware access URL and ensures `cli.token`.

Error:
- When the gateway is unreachable (P0), the system prints `run omnipus start` to stderr and exits non-zero.
- When a `tool_approval_required` frame arrives without `--yes`, the system denies-and-continues and notes it on stderr.
- When `<agent>` is a worker/unknown, the system errors clearly and exits non-zero.
- When a removed verb is invoked, the system prints `"<verb>" was removed in the CLI redesign — run 'omnipus --help' for the current commands` (stderr) and exits non-zero.

Boundary:
- When bound to loopback, the system prints only localhost + a hint.
- When no non-interactive unlock is available (P1 auto-start), the system fails with guidance rather than prompting.

---

## Edge Cases

- Empty prompt (`omnipus jim ""`) → reject with usage; exit non-zero.
- Prompt that looks like a flag (`omnipus jim -- "--weird"`) → `--` terminates flag parsing.
- Agent name colliding with a reserved verb (`onboard`/`start`/`stop`/`credentials`) → reserved; agent creation must reject/warn (cobra shadows it). Document.
- Very large prompt (> wire max 5MB) → server rejects; CLI surfaces the error to stderr.
- WS drops mid-turn → CLI prints a connection error to stderr, exits non-zero (no partial-success exit 0).
- `done` never arrives within a timeout → CLI aborts with a timeout error.
- `cli.token` missing at run time → P0: instruct to run `start`/`onboard`; do not silently bypass auth.
- Unicode/emoji in prompt and result → preserved verbatim on stdout.

---

## Explicit Non-Behaviors

- The system must NOT open an in-process engine/agent-loop when a gateway is reachable, because that re-introduces drift and the two-writers-one-datadir corruption hazard (NFR-1).
- The system must NOT use loopback-trust ("localhost = admin") for auth, because it re-creates a `dev_mode_bypass` hole; it must use the `cli` token.
- The system must NOT let `--yes` override server-side agent policy, because the engine remains the security authority.
- The system must NOT print secrets (the `cli` token, API keys) to stdout/stderr or logs.
- The system must NOT send any token to a non-TLS `http://` remote, nor a local token to a `--url` remote.
- The system must NOT add a REPL, session continuity (`-s`), or a worker/specialist execute path (all out of scope).
- The system must NOT hang on the 90 s approval timeout on a one-shot run.

---

## Integration Boundaries

### Omnipus Gateway — WS chat API (`contracts/asyncapi.yaml`)
- **Data in (client→server, WS):** `{type:"auth", token}` first; then `{type:"message", content, agent_id, metadata.model_name?}`.
- **Data out (server→client, WS):** `session_started`, `token*`, `tool_call_start/result`, `tool_approval_required`, `done{stats}`, `error`.
- **Approval resolve (REST):** `POST /api/v1/tool-approvals/{approval_id}` `{"action":"deny"|"approve"}` — the run is **WS-to-observe + REST-to-resolve**. The `/chat` SSE endpoint is NOT used (cannot carry `agent_id`/`model`; legacy/no-stats — `SseChatRequest.yaml:7-14`, `sse.go`).
- **On failure:** unreachable → P0 error "run omnipus start"; mid-turn `error` → stderr + non-zero exit; drop → connection error.
- **Development:** real gateway (in-process `RunContextWithOptions` for integration tests; a scripted WS server twin for unit-level client tests).

### Credential store / key unlock (`pkg/credentials`)
- **Data in/out:** master-key unlock; the `cli` token is a gateway-user credential, not in the encrypted store.
- **On failure (P1 auto-start):** no non-interactive unlock → fail with guidance.
- **Development:** real store with `OMNIPUS_MASTER_KEY` in tests.

### Local `config.json`
- **Data in:** agent roster (`Agents.List` + seeded), `Gateway.Host/Port/PublicURL`, `Gateway.Users`.
- **On failure:** missing/pre-onboard → guide to `onboard`.
- **Development:** real file via a temp `OMNIPUS_HOME`.

---

## BDD Scenarios

### Feature: One-shot agent execution

#### Scenario: Run a named agent against a live gateway
**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a running gateway and a valid `cli.token`
- **When** the user runs `omnipus jim "2+2?"`
- **Then** the reply text streams to stdout
- **And** the process exits 0 after the `done` frame

#### Scenario: Gateway down (P0) errors with guidance
**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Error Path
- **Given** no gateway is listening on the configured port
- **When** the user runs `omnipus jim "hi"`
- **Then** stderr prints `Omnipus isn't running — start it with: omnipus start`
- **And** the exit code is non-zero
- **But** no in-process engine is opened

#### Scenario: Worker/unknown agent rejected
**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Error Path
- **Given** the roster contains a worker `worker` and no agent `zzz`
- **When** the user runs `omnipus <name> "hi"` with `<name>` in {`worker`,`zzz`}
- **Then** stderr prints a clear unrunnable/unknown-agent message
- **And** the exit code is non-zero

#### Scenario Outline: Model override on the wire
**Traces to**: User Story 2, Acceptance Scenario 1/2
**Category**: Happy Path
- **Given** a running gateway
- **When** the user runs `omnipus jim <flag> "hi"`
- **Then** the `message` frame `metadata.model_name` is `<sent>`

**Examples**:
| flag | sent |
|---|---|
| `--model openrouter/glm-5.2` | openrouter/glm-5.2 |
| (none) | (absent) |

#### Scenario: Stdout/stderr separation under redirection
**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a run that emits tool-call activity and tokens
- **When** the user runs `omnipus jim "…" 1>out 2>err`
- **Then** `out` contains only the assembled result text
- **And** tool-call/progress lines appear in `err`

### Feature: Approval handling on a one-shot

#### Scenario: Ask-policy tool denied-and-continue without --yes
**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Alternate Path
- **Given** a one-shot run with no `--yes`
- **When** a `tool_approval_required` frame arrives
- **Then** the CLI issues `POST /api/v1/tool-approvals/{approval_id}` `{"action":"deny"}` immediately
- **And** stderr notes `denied tool: <name>`
- **And** the run proceeds to `done` without a 90 s wait

#### Scenario: --yes auto-approves but server policy still wins
**Traces to**: User Story 4, Acceptance Scenario 2/3
**Category**: Alternate Path
- **Given** a one-shot run with `--yes` and an agent whose policy forbids the tool
- **When** a `tool_approval_required` frame arrives
- **Then** the CLI issues `{"action":"approve"}`
- **And** the engine still blocks the tool per the agent's policy (`deny`-policy tools never reach the ask gate)

### Feature: Auth (cli principal)

#### Scenario: onboard mints the cli principal and token file
**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path
- **Given** a fresh `OMNIPUS_HOME`
- **When** onboarding completes
- **Then** `Gateway.Users` contains a `cli` user with a bcrypt `token_hash`
- **And** `$OMNIPUS_HOME/cli.token` exists with mode 0600
- **And** the token plaintext is never printed to stdout

#### Scenario: WS auth uses the cli token, audited as cli
**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Happy Path
- **Given** a valid `cli.token`
- **When** a run opens the WS
- **Then** the first frame is `{type:"auth", token:<cli.token>}`
- **And** authentication succeeds without `dev_mode_bypass`
- **And** the run's audit entries carry `User="cli"`

#### Scenario: stale cli token gives a distinct message
**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Error Path
- **Given** the gateway is running but `cli.token` no longer matches a `Gateway.Users` entry
- **When** a run is attempted
- **Then** stderr prints `Your CLI key is invalid or out of date. Run: omnipus start`
- **And** the exit code is non-zero
- **But** it does NOT print the gateway-down message

#### Scenario: --url is rejected in v1
**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Error Path
- **Given** `--url https://remote:5000`
- **When** the CLI runs
- **Then** stderr prints `remote gateways are not supported yet`
- **And** the exit code is non-zero

### Feature: Discovery & help

#### Scenario: Bare omnipus lists chat-target agents offline
**Traces to**: User Story 6, Acceptance Scenario 1/2
**Category**: Happy Path
- **Given** a config with seeded agents and no gateway running
- **When** the user runs `omnipus`
- **Then** chat-target agents are listed with id + name and the usage block
- **And** workers are excluded
- **And** the exit code is 0

#### Scenario: Bare omnipus pre-onboard guides to onboard
**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Error Path
- **Given** no config exists yet
- **When** the user runs `omnipus`
- **Then** stderr suggests `omnipus onboard`
- **And** the exit code is non-zero

### Feature: Bootstrap UX

#### Scenario: onboard numbered provider menu selection
**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Happy Path
- **Given** interactive onboarding
- **When** the user types a valid provider number
- **Then** that provider is selected
- **But** an out-of-range number re-prompts

#### Scenario Outline: start bind-aware URL
**Traces to**: User Story 9, Acceptance Scenario 1/2/3
**Category**: Happy Path
- **Given** `gateway.host=<host>` and `public_url=<pub>`
- **When** `omnipus start` boots
- **Then** the printed URLs are `<urls>`

**Examples**:
| host | pub | urls |
|---|---|---|
| 0.0.0.0 | (empty) | localhost + LAN-IP |
| 127.0.0.1 | (empty) | localhost only + host hint |
| 0.0.0.0 | https://omni.example | https://omni.example (canonical) |

### Feature: Removals

#### Scenario: removed verb prints redesign message; gateway alias survives
**Traces to**: User Story 11, Acceptance Scenario 1/3
**Category**: Error Path
- **Given** the new command tree
- **When** the user runs `omnipus status`
- **Then** stderr prints `"status" was removed in the CLI redesign — run 'omnipus --help' for the current commands` and exits non-zero
- **But** `omnipus gateway` still runs `start`

**Implementation note:** `RunE` uses `cobra.ArbitraryArgs` and intercepts removed verbs via the `removedVerbs` map before the agent-lookup, so the message is the redesign-specific one rather than cobra's generic "unknown command".

#### Scenario: CI grep-guard blocks removed-verb regressions
**Traces to**: User Story 11, Acceptance Scenario 2/4
**Category**: Edge Case
- **Given** the redesign PR
- **When** the grep-guard scans Docker/CI/worker/launcher/install.sh
- **Then** no removed verb is referenced
- **And** the guard fails the build if one reappears

### Feature: Auto-start (P1)

#### Scenario: auto-start spawns a gateway then runs
**Traces to**: User Story 12, Acceptance Scenario 1
**Category**: Happy Path
- **Given** no gateway running and `OMNIPUS_MASTER_KEY` set
- **When** the user runs `omnipus jim "hi"`
- **Then** the CLI invokes `start`, polls for real WS acceptance (not `/ready`), runs the task, and leaves the gateway running

#### Scenario: auto-start refuses without a non-interactive unlock
**Traces to**: User Story 12, Acceptance Scenario 2
**Category**: Error Path
- **Given** no gateway and no `OMNIPUS_MASTER_KEY`/`OMNIPUS_KEY_FILE`/`master.key`
- **When** auto-start is needed
- **Then** the CLI fails with guidance and does not prompt interactively

#### Scenario: concurrent auto-start does not double-spawn
**Traces to**: User Story 12, Acceptance Scenario 3
**Category**: Edge Case
- **Given** no gateway and two concurrent `omnipus jim` invocations
- **When** both reach auto-start
- **Then** the OS port bind ensures exactly one gateway starts; the loser connects to it (no duplicate, no separate lock)

#### Scenario: omnipus stop handles a stale PID safely
**Traces to**: User Story 13, Acceptance Scenario 2
**Category**: Edge Case
- **Given** a PID file whose process is dead
- **When** `omnipus stop` runs
- **Then** it reports "not running", clears the file, and never kills an unrelated reused PID

---

## Test-Driven Development Plan

### Test Hierarchy
| Level | Scope | Purpose |
|---|---|---|
| Unit | flag parsing, roster filter, URL builder, approval-decision mapper, token-file mint, WS-frame codec | logic in isolation |
| Integration | CLI ↔ in-process gateway (`RunContextWithOptions`) over real WS; onboard→config→boot | components together |
| E2E | built binary against a seeded `OMNIPUS_HOME` | full user workflow |

### Test Implementation Order

> **Test environment (MAJ-5):** integration tests boot the gateway in-process via `gateway.RunContextWithOptions(ctx, …)` with a **mock provider** (mirror the existing `restMockProvider{}` pattern in `pkg/gateway` tests — **no real LLM, no API key, no network**) and a scripted-WS-twin (`httptest.NewServer` + `newTestWSHandler`) for unit-level client tests. Per CLAUDE.md, run scoped: `-tags goolm,stdjson -run '^TestName$' -p 1`; **never** the full `pkg/gateway` suite locally (OOM). CI is the authority for the full run.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestRosterListing_ExcludesWorkers` | Unit | Bare omnipus lists chat-target agents offline | Filter `Agents.List` by `IsChatTarget`. |
| 2 | `TestUsageBuilder_NoArgsAndHelp` | Unit | omnipus --help curated reference | Usage/help text content + removed verbs absent. |
| 3 | `TestStartURLBuilder_BindAware` | Unit | start bind-aware URL (outline) | host/public_url → URL set + LAN-IP enumeration (injected interface list). |
| 4 | `TestMintCliToken_WritesUserAndFile0600` | Unit | onboard mints the cli principal | Extends `mutateConfigFile`; asserts user + 0600 file + no stdout leak. |
| 5 | `TestApprovalDecision_DefaultDenyAndYesAllow` | Unit | approval denied-and-continue / --yes | Map flag→REST `action` (`deny`/`approve`); default `deny`; uses `approval_id` from the frame. |
| 6 | `TestWSFrameCodec_MessageAndAuth` | Unit | model override on the wire | Encode auth/message frames incl. `metadata.model_name`. |
| 7 | `TestProviderMenu_NumberSelectionReprompt` | Unit | onboard numbered provider menu | `wizardIO` scripted stdin; invalid → re-prompt. |
| 8 | `TestRun_AgainstGateway_StreamsToStdout` | Integration | Run a named agent against a live gateway | In-process gateway; assert stdout result + exit 0. |
| 9 | `TestRun_GatewayDown_GuidesToStart` | Integration | Gateway down errors with guidance | No listener → stderr message + non-zero + no engine. |
| 10 | `TestRun_StdoutStderrSeparation` | Integration | Stdout/stderr separation | Tool-emitting agent; assert stream split. |
| 11 | `TestApproval_OneShotDenyContinues` | Integration | Ask-policy denied-and-continue | Agent triggers `ask` tool; CLI POSTs `/tool-approvals/{id}` `deny`; run completes < 5 s (no 90 s wait). |
| 12 | `TestAuth_CliTokenAuthsAndAudits` | Integration | WS auth uses the cli token | Auth frame accepted; an `audit.Entry` has `User=="cli"` (FR-017 plumbing). |
| 13 | `TestWorkerAgent_Rejected` | Integration | Worker/unknown agent rejected | `omnipus worker "…"` → error. |
| 14 | `TestRemovals_UnknownVerb_AliasKept` | Integration | removed verb unknown; gateway alias survives | Command tree assertions. |
| 15 | `TestGrepGuard_NoRemovedVerbsInInfra` | Integration | CI grep-guard | Script asserts infra is clean (also wired into CI). |
| 16 | `TestE2E_OnboardThenRun` | E2E | end-to-end | Built binary: onboard (scripted) → start → `omnipus jim "hi"` → reply. |
| 17 | `TestAutoStart_SpawnPollRun` (P1) | Integration | auto-start spawns then runs | MASTER_KEY set; spawn→health→run. |
| 18 | `TestAutoStart_NoUnlock_Fails` (P1) | Integration | auto-start refuses without unlock | No key mode → guided failure. |
| 19 | `TestDaemon_StaleP​idSafe` (P1) | Unit | omnipus stop handles stale PID | `pkg/daemon` PID staleness + identity check. |
| 20 | `TestAutoStart_ConcurrentPortBind` (P1) | Integration | concurrent auto-start | OS port-bind single-spawn; loser connects. |
| 21 | `TestRun_EmptyPrompt_Rejected` | Unit | (Edge) empty prompt | `omnipus jim ""` → usage error, non-zero. (MIN-1) |
| 22 | `TestCliToken_OverPermissiveAndCorrupt` | Unit | (Edge) token 0644 / malformed | 0644 → warn/refuse; truncated token → clear guidance, no panic. (MIN-2) |
| 23 | `TestRun_PreOnboard_GuidesToOnboard` | Integration | Bare omnipus pre-onboard guides to onboard | No config → `omnipus onboard` hint, non-zero. (US-6-AC-3 gap) |
| 24 | `TestUrl_RejectedInV1` | Unit | --url is rejected in v1 | `--url …` → `remote not supported yet`; `http://`+token refused. (FR-007) |
| 25 | `TestRun_StaleToken_DistinctMessage` | Integration | stale cli token gives a distinct message | reachable gateway + bad token → key-invalid message, not gateway-down. (FR-019) |
| 26 | `TestStart_TokenCreateIfAbsentAndReset` | Integration | (US-5 AC-2/3) | existing token preserved; `--new-cli-token` rotates + old rejected. (FR-018) |
| 27 | `TestWSFrameCodec_MetadataExactKey` | Unit | model override (outline) | `metadata` is an open map — assert the exact key `model_name` is set (a typo silently no-ops). (MIN-4) |

### Test Datasets

#### Dataset: `<agent>` argument resolution
| # | Input | Boundary Type | Expected | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `jim` | happy | runs Jim | Run a named agent | chat target |
| 2 | `worker` | error | rejected | Worker/unknown rejected | worker not chat target |
| 3 | `zzz` | error | unknown agent | Worker/unknown rejected | absent |
| 4 | `onboard` | edge | runs subcommand (shadowed) | Edge Cases | reserved name |
| 5 | `""` (no agent) | boundary | roster+usage | Bare omnipus lists agents | no-args path |

#### Dataset: start URL bind-awareness
| # | host | public_url | Boundary | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | `0.0.0.0` | "" | happy | localhost + LAN IP | start bind-aware URL |
| 2 | `127.0.0.1` | "" | boundary | localhost only + hint | start bind-aware URL |
| 3 | `` (empty) | "" | boundary | localhost + LAN IP | start bind-aware URL |
| 4 | `0.0.0.0` | `https://x` | alternate | canonical public_url | start bind-aware URL |
| 5 | `0.0.0.0` | `http://x` (non-TLS) | edge | print but flag insecure | Non-Behaviors |

#### Dataset: approval decision mapping
| # | `--yes` | frame | Boundary | Expected `decision` | Traces to |
|---|---|---|---|---|---|
| 1 | absent | tool_approval_required | happy | `deny` | denied-and-continue |
| 2 | present | tool_approval_required | happy | `allow` | --yes auto-approve |
| 3 | present | (policy forbids) | edge | `allow` sent, engine blocks | server policy wins |

#### Dataset: cli.token file
| # | Input | Boundary | Expected | Traces to |
|---|---|---|---|---|
| 1 | fresh onboard | happy | user+0600 file | onboard mints cli principal |
| 2 | existing install, no token | alternate | minted on `start` | US-5 AC-2 |
| 3 | token file mode 0644 | edge | warn/refuse (too open) | Non-Behaviors (secrets) |

### Regression Test Requirements
**Modifies existing functionality:**
| Existing Behaviour | Existing Test | New Regression Test | Notes |
|---|---|---|---|
| `onboard` writes usable config | `onboard_test.go::TestRun_FreshInstall_WritesUsableConfig` | extend to assert `cli.token` + `cli` user added without breaking provider/admin writes | must stay green |
| `credentials` set/list/delete/rotate | `credentials/command_test.go` | unchanged; assert still registered | kept verbatim |
| `start` boots gateway | `gateway/command_test.go` | extend for URL block; assert boot path unchanged | additive |
| gateway boot reads users | `bootCredentials`/auth tests | assert the new `cli` user doesn't break multi-user auth | |

---

## Requirements & Success Criteria

### Functional Requirements
- **FR-001**: The CLI MUST run `omnipus <agent> "<prompt>"` as a one-shot over the WS chat API and exit after `done`. (US-1)
- **FR-002**: `<agent>` MUST be a mandatory positional resolving to a chat-target agent; workers/unknown MUST error. (US-1)
- **FR-003**: `--model <slug>` MUST set `metadata.model_name` for that turn; absence MUST fall back to the agent's model. (US-2)
- **FR-004**: The result MUST go to stdout and progress/tool activity to stderr. (US-3)
- **FR-005**: On a `tool_approval_required` WS frame without `--yes`, the CLI MUST resolve it via `POST /api/v1/tool-approvals/{approval_id}` `{"action":"deny"}` immediately and continue; `--yes` MUST send `{"action":"approve"}`, never overriding server policy. It MUST NOT use the legacy `exec_approval_response` WS frame (wrong registry → 90 s hang). (US-4) `[grounded: policy_approver.go:48, rest_tool_registry.go:415]`
- **FR-006**: The CLI MUST authenticate via a `cli`-owned token (`$OMNIPUS_HOME/cli.token`, 0600) matching a `cli` `Gateway.Users` principal; it MUST NOT use loopback-trust. (US-5)
- **FR-007**: `--url` (remote gateway) is OUT of P0 — the CLI MUST error `remote gateways are not supported yet`. The defensive guardrails MUST still hold: never send any token over `http://`, never a local token to a remote. (US-5)
- **FR-017**: A `User` field MUST be added to `audit.Entry`, and the authenticated `userID` MUST be threaded through the turn into agent-loop audit emissions, so CLI runs are attributable to `cli`. (US-5; blast radius `pkg/audit`, `pkg/agent/loop.go`)
- **FR-018**: `start`/`onboard` MUST mint `cli.token` **create-if-absent** (a valid existing token is left unchanged); `omnipus start --new-cli-token` MUST rotate it (old token stops authenticating). (US-5)
- **FR-019**: When the gateway is reachable but the `cli` token is rejected, the CLI MUST print `Your CLI key is invalid or out of date. Run: omnipus start` and exit non-zero — distinct from the gateway-down message. (US-5)
- **FR-008**: Bare `omnipus` MUST list chat-target agents + usage from local config without a gateway. (US-6)
- **FR-009**: `omnipus --help` MUST document the execute form, `onboard`, `start`, `credentials`, with examples, and exclude removed verbs. (US-7)
- **FR-010**: `onboard` MUST offer a numbered provider menu, mint the `cli` token, print the access URL, and preserve `--non-interactive`. (US-8)
- **FR-011**: `start` MUST print bind-aware access URL(s) and ensure `cli.token` exists. (US-9)
- **FR-012**: `credentials set/list/delete/rotate` MUST remain functionally unchanged. (US-10)
- **FR-013**: `agent`, `auth`, `status`, `cron`, `migrate`, standalone `model`/`skills` MUST be removed; the `gateway` alias MUST be kept; all internal callers MUST be updated and a CI grep-guard added. (US-11)
- **FR-014**: The CLI MUST NOT open an in-process engine when a gateway is reachable. (NFR-1)
- **FR-015 (P1)**: When no gateway is reachable, the agent-run command MUST bring one up via `start` (persistent), poll for **real WS acceptance** (TCP+upgrade+auth, not `/ready`), and require a non-interactive unlock mode (else fail with guidance). (US-12)
- **FR-016 (P1)**: Concurrent auto-start MUST be serialized by the OS port bind (no separate `gateway.lock`); the loser connects to the winner. `omnipus stop` MUST stop a CLI/launcher-started gateway via shared `pkg/daemon` with safe stale-PID handling (verify the PID is a live omnipus process before killing). (US-12/US-13)

### Success Criteria
- **SC-001**: `omnipus jim "hi"` against a running gateway returns the reply and exits 0 in a test, with stdout containing only the result. (FR-001/004)
- **SC-002**: With the gateway down (P0), the command exits non-zero and stderr contains `omnipus start`; a test asserts no engine boot occurs. (FR-001/014)
- **SC-003**: A run that triggers an `ask` tool without `--yes` completes in < 5 s (no 90 s hang) and logs the denied tool to stderr. (FR-005)
- **SC-004**: After onboard, `cli.token` is mode 0600, a `cli` user exists, and (with FR-017 plumbing) an audited run produces ≥1 `audit.Entry` with `User=="cli"`; the token never appears in stdout/stderr. (FR-006/FR-017)
- **SC-008**: `omnipus start` with a valid `cli.token` leaves it byte-identical; `omnipus start --new-cli-token` changes it and the prior token is rejected by the gateway. (FR-018)
- **SC-009**: A run against a reachable gateway with a wrong/stale token prints the `omnipus start` *key-invalid* guidance (not the gateway-down message) and exits non-zero. (FR-019)
- **SC-005**: `omnipus status` prints the redesign-removal message and exits non-zero; `grep -rE '(omnipus|\./omnipus|[A-Za-z0-9_/]*/omnipus|\$[A-Z_]+) (agent|auth|status|cron|migrate|model|skills)\b'` over Docker/CI/worker/launcher/install.sh returns empty in CI; `omnipus gateway` still starts the server. (FR-013)
- **SC-006**: Bare `omnipus` with no gateway lists ≥1 chat-target agent and 0 workers, exit 0. (FR-008)
- **SC-007 (P1)**: With `OMNIPUS_MASTER_KEY` set and no gateway, `omnipus jim "hi"` spawns exactly one gateway (verified under 2 concurrent invocations) and returns a reply. (FR-015/016)

### Traceability Matrix
| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | Run a named agent; Gateway down | TestRun_AgainstGateway_StreamsToStdout; TestRun_GatewayDown_GuidesToStart |
| FR-002 | US-1 | Worker/unknown rejected | TestWorkerAgent_Rejected |
| FR-003 | US-2 | Model override (outline) | TestWSFrameCodec_MessageAndAuth |
| FR-004 | US-3 | Stdout/stderr separation | TestRun_StdoutStderrSeparation |
| FR-005 | US-4 | Denied-and-continue; --yes wins-policy | TestApprovalDecision_…; TestApproval_OneShotDenyContinues |
| FR-006 | US-5 | onboard mints cli; WS auth audited | TestMintCliToken_…; TestAuth_CliTokenAuthsAndAudits |
| FR-007 | US-5 | --url is rejected in v1 | TestUrl_RejectedInV1 |
| FR-017 | US-5 | WS auth audited as cli | TestAuth_CliTokenAuthsAndAudits |
| FR-018 | US-5 | (US-5 AC-2/3) | TestStart_TokenCreateIfAbsentAndReset |
| FR-019 | US-5 | stale cli token gives a distinct message | TestRun_StaleToken_DistinctMessage |
| FR-008 | US-6 | Bare omnipus lists; pre-onboard guide | TestRosterListing_ExcludesWorkers |
| FR-009 | US-7 | help reference | TestUsageBuilder_NoArgsAndHelp |
| FR-010 | US-8 | provider menu; mint token | TestProviderMenu_…; TestMintCliToken_… |
| FR-011 | US-9 | start bind-aware URL (outline) | TestStartURLBuilder_BindAware |
| FR-012 | US-10 | (regression) | credentials/command_test.go |
| FR-013 | US-11 | removed verb unknown; grep-guard | TestRemovals_UnknownVerb_AliasKept; TestGrepGuard_… |
| FR-014 | US-1 | Gateway down (no engine) | TestRun_GatewayDown_GuidesToStart |
| FR-015 | US-12 | auto-start spawn; refuse no-unlock | TestAutoStart_SpawnPollRun; TestAutoStart_NoUnlock_Fails |
| FR-016 | US-12/13 | concurrent auto-start; stale PID | TestAutoStart_ConcurrentPortBind; TestDaemon_StalePidSafe |
| FR-008 (gap fix) | US-6 | pre-onboard guides to onboard | TestRun_PreOnboard_GuidesToOnboard |
| FR-001/002 (edge) | US-1 | empty prompt | TestRun_EmptyPrompt_Rejected |

---

## Ambiguity Self-Audit

| # | What's ambiguous | Likely agent assumption | Question to resolve |
|---|---|---|---|
| A1 | Result formatting. | **RESOLVED** — live-stream tokens to stdout; newline at `done`. | — |
| A2 | `done` timeout. | **RESOLVED** — 300 s default, overridable by `--timeout`; abort non-zero on expiry. | — |
| A3 | Reserved-name collision. | **ACCEPTED DEFAULT** — reject at agent creation (SPA/API) + CLI hint when shadowed. | — |
| A4 | LAN-IP with multiple NICs. | **RESOLVED** — first non-loopback IPv4 as primary; list any extras. | — |
| A5 | `cli` principal role. | **DECIDED** — admin-equivalent for v1; scoped role deferred. | — |
| A6 | `--yes` scope. | **RESOLVED** — per-invocation only; no persisted auto-approve. | — |

> **GATE CLEARED (2026-06-28):** all six items resolved/accepted by the operator. Decisions folded into Assumptions and FRs (live stream → FR-004; 300 s `--timeout` → FR-001; first-IPv4 → FR-011; per-call `--yes` → FR-005).

---

## Holdout Evaluation Scenarios (post-implementation only — NOT in traceability)

1. **H1 (happy):** Fresh machine → `omnipus onboard` (pick provider 1) → `omnipus start` → `omnipus jim "write a haiku about the sea"` → a haiku prints to stdout, exit 0.
2. **H2 (happy):** `omnipus mia "what agents do I have?"` returns a sensible answer; `omnipus` (no args) lists the same agents.
3. **H3 (happy):** `omnipus jim "summarize" < bigfile.txt` (piped) — result on stdout only; `… > out.txt` yields a clean file.
4. **H4 (error):** Stop the gateway; `omnipus jim "hi"` prints the `omnipus start` guidance and exits non-zero.
5. **H5 (error):** `omnipus status` → stderr: `"status" was removed in the CLI redesign — run 'omnipus --help' for the current commands`, exit non-zero; `omnipus gateway` still serves.
6. **H6 (edge):** A task that makes Jim attempt a shell command without `--yes` → completes quickly, stderr notes the denied tool; with `--yes` the command runs.
7. **H7 (edge):** Inspect `$OMNIPUS_HOME/cli.token` (0600) and the audit log shows `user=cli`; grep the terminal scrollback — the token never appears.

---

## Assumptions

- **Output:** the result live-streams to stdout (tokens as they arrive), newline at `done`; progress/tool activity on stderr. (A1)
- **Timeout:** a run with no `done` aborts after **300 s** (overridable via `--timeout`) with a non-zero exit. (A2)
- **Reserved names:** `onboard`/`start`/`stop`/`credentials` shadow same-named agents; agent creation rejects/warns on the collision. (A3)
- **LAN URL:** `start`/`onboard` print the first non-loopback IPv4 as primary and list any additional IPv4s (the primary may be a Docker/VPN NIC; listing extras lets the user pick the reachable one — OBS-1). (A4)
- **`--yes`:** per-invocation only; never persisted; server-side agent policy always wins. (A6)
- **A5 (decided):** the `cli` principal is admin-role for v1; `UserConfig.Role` is currently NOT enforced at the tool/approval layer, so admin ≈ user there; a scoped CLI role is a future EoP mitigation. Revocation = remove the `cli` user or `omnipus start --new-cli-token`.
- **Approvals:** observed over WS (`tool_approval_required`), resolved over REST (`POST /api/v1/tool-approvals/{approval_id}` `{"action":"deny"|"approve"}`). The legacy `exec_approval_response` WS frame is NOT used.
- **Audit attribution:** delivered by adding `audit.Entry.User` + threading the authenticated `userID` through the turn (FR-017), in scope for this epic.
- **Token lifecycle:** `cli.token` is create-if-absent on `start`/`onboard`; rotated only by `--new-cli-token`; a rejected token yields a distinct key-invalid message.
- **`--url`:** out of P0 — errors `remote gateways are not supported yet`; the http/local-token guardrails remain as defensive checks.
- **Auto-start concurrency:** the OS port bind is the mutex (no `gateway.lock`; flock is a Windows no-op).
- Execution streams over WS; `/chat` SSE is not used (cannot carry `agent_id`/`model`).
- Each run is a fresh ephemeral session; no `-s` continuity in scope.
- The worker/specialist execute path is out of scope (v-next, needs a non-session delegation endpoint).
- P1 (auto-start, `omnipus stop`, `pkg/daemon`) ships after P0; the persistent auto-started gateway is the user's normal gateway (no channel flapping).

---

## Handoff
- Resolve the Ambiguity Self-Audit (A1–A4, A6) — A5 decided.
- Then `/taskify` to decompose (P0 epic first, P1 epic fast-follow), implement in the wave pattern, `/grill-code` to verify. Security-lead owns FR-006/FR-007 (the `cli` token handshake).
