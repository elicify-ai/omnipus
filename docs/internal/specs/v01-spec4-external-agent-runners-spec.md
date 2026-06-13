# Spec-4 — External-Agent Runners & the Executor Tier (v0.1.0 Foundation)

- **Spec:** 4 of 6 (v0.1.0 Foundation)
- **Source ADR:** [ADR-019](../architecture/ADR-019-v01-workspaces-foundation.md) — FR-4 + FR-5
- **Status:** Draft → pending `/grill-spec` (GATE C)
- **Cross-spec (Phase 3.5):** the sub-agent roster/policy is Spec-3 (the `executor` lives on the sub-agent config Spec-3 references; the unified `to` gates *which* sub-agent runs); the `remote-a2a` executor kind shares the agent-reference shape (Spec-2/3); the Orchestrator (Spec-3) dispatches runner fan-out within the Max-parallel gate (Spec-3 FR-6.6).
- **Lessons pre-applied:** ground hard (no "at impl"); contract-first for the executor enum; CI-authority; new deps = ADR decision; greenfield; compiler/test gate for completeness.

## 1. Overview

Give sub-agent workers an **`executor`** (`native | external-cli{claude-code, opencode, codex} | reserved remote-a2a`); define the **`ExternalAgentRunner`** interface — **bidirectional** (events-out: output·tool-calls·diffs·permission-requests + control-in: permission-decisions·cancel·input), **consent-routed** (permission-requests → the policy/consent layer), **resumable** (stable run/session id); drive external agents over the **universal CLI + JSON-streaming transport** (`claude -p --output-format stream-json` · `codex exec` JSON · `opencode run --format json`); run each as its **own process, kernel-confined** (Landlock/seccomp via the existing hardened-exec) and **git-worktree-isolated**; and add a per-runner **connection test** (health: spawn/auth/handshake).

**In scope:** the `executor` field on the sub-agent config (contract); the `ExternalAgentRunner` Go interface (bidirectional/consent-routed/resumable); the **CLI+JSON-streaming drivers for Claude Code + Codex** (built on the existing `claude_cli`/`codex_cli` providers) and **opencode** (net-new); own-process kernel-confined exec (Landlock/seccomp via `hardened_exec`); git-worktree isolation per run; the connection test; consent-routing of permission-requests; bounded runs (timeouts/turn-cap).
**Out of scope:** the ACP *protocol* driver (later — the bidirectional interface is the v0.1.0 *shape*); the A2A protocol/`remote-a2a` *resolution* (reserved kind only); the Orchestrator/fan-out concurrency (Spec-3); skill/tool execution inside the runner (the external agent owns its own tools).

## 2. Existing Codebase Context (grounded)

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `SubagentsConfig` (`config.go:584`, `AllowAgents`) / the sub-agent config | **add `executor`** (`{kind, cli?, …}`) | no executor today — NEW additive field |
| `claude_cli_provider.go`, `codex_cli_provider.go` | **partial** reuse | spawn the CLIs but are **one-shot buffered** (`cmd.Run`, `--dangerously-skip-permissions`) — prove spawn+auth, NOT streaming (C-2); runner drivers are new |
| `pkg/agent/hook_process.go` (JSON-RPC/stdio) | reuse **correlation only** | Omnipus is the *client* there; the runner is the **inverse** (child emits, Omnipus answers) — direction new (M-1) |
| `pkg/sandbox/hardened_exec.go` + the `workspace.shell` sandbox | **reuse for NATIVE subs** | native sub-agents ride the existing Omnipus sandbox; external CLIs use their OWN sandbox — **no new confiner** (operator decision) |
| `pkg/gateway/ws_approval.go` (`ToolApprovalRequest`→`ApprovalDecision`) | consent-routing target | tool-call-shaped — define the external-agent permission mapping (M-2) |
| **opencode driver** + **git-worktree isolation** + **consent-routing** | NEW | opencode net-new; worktree + consent-routing are new; **NO new confiner** — use the CLIs' own sandboxes (drop `--dangerously-skip`) |

### Impact Assessment
| Modified | Risk | Direct (d=1) | Indirect (d=2) |
|---|---|---|---|
| `executor` on sub-agent config (contract) | **HIGH** (contract) | generated types + the Agents/sub-agent UI + the dispatch site | Spec-3 sub-agent dispatch |
| `ExternalAgentRunner` interface (Go) | MEDIUM | the dispatch site, the 3 drivers | the agent loop |
| own-process kernel-confined exec | **HIGH** (security) | `hardened_exec` spawn path | sandbox policy |
| git-worktree isolation | MEDIUM | run setup/teardown | disk |

## 3. User Stories

**US-1 — Executor field (P0).** 1. **Given** a sub-agent, **When** I set `executor=native`, **Then** it runs in Omnipus's loop; `executor=external-cli{cli:claude-code}` → it drives Claude Code headless; `remote-a2a` is accepted in the schema but **not resolvable** in v0.1.0 (reserved). 2. **Given** the schema, **When** `make verify-contracts` runs, **Then** exit 0.

**US-2 — Bidirectional, consent-routed runner (P0).** 1. **Given** a running external agent, **When** it requests a permission mid-run, **Then** the request surfaces to the consent layer and the decision is sent back (control-in); **When** it emits a diff/tool-call/output, **Then** those events stream out. 2. **Given** a run, **When** I cancel it, **Then** the cancel reaches the process and it stops.

**US-3 — Resumable runs (P0).** 1. **Given** a run with a stable id, **When** it is interrupted and resumed, **Then** it continues from its session (Claude `--resume`).

**US-4 — Universal CLI+JSON transport (P0).** 1. **Given** `executor=external-cli`, **When** dispatched, **Then** the driver invokes the CLI with JSON streaming (`claude -p --output-format stream-json` / `codex exec` JSON / `opencode run --format json`) and parses the structured events.

**US-5 — Own-process, kernel-confined, worktree-isolated (P0, security).** 1. **Given** an external run, **When** it starts, **Then** it is its own process **under the external CLI's OWN sandbox** (Codex = Landlock/seccomp on Linux + Seatbelt on macOS; Claude Code = its permission model — Omnipus does NOT disable it via `--dangerously-skip`), in a **git worktree** (or isolated temp dir) isolated from the main tree. **Native** sub-agents run under the **existing Omnipus `workspace.shell` sandbox**. 2. **Given** a hostile agent, **Then** the tool's own sandbox + the worktree confine it (no new Omnipus confiner needed).

**US-6 — Connection test (P0).** 1. **Given** an external-cli runner, **When** I click "test connection", **Then** it validates the binary is present, authed, and the JSON handshake works — without running real work. 2. **Given** a missing/un-authed binary, **Then** the test fails with a clear reason.

**US-7 — Bounded runs (P0, safety).** 1. **Given** a run, **When** it exceeds the per-run timeout or turn-cap, **Then** it is terminated and reported (the Gemini-RCE-class blast-radius bound).

### Edge Cases
- Missing CLI binary → connection test + run fail cleanly (not a crash). · `remote-a2a` executor → schema-valid, dispatch returns "not available in v0.1.0". · Permission-request with no consent handler → deny-by-default. · Worktree cleanup on crash → no orphaned worktrees. · CLI emits malformed JSON → driver records a parse error, run fails non-fatally. · Codex headless quirks (no stable stream) → best-effort + documented.

## 4. Behavioral Contract · Non-Behaviors · Integration Boundaries

**Contract:** sub-agent carries an `executor`; external runs go over CLI+JSON streaming; runs are own-process, Landlock/seccomp-confined, worktree-isolated, resumable, bounded; permission-requests route to consent (deny-by-default); a connection test validates a runner.

**Non-behaviors:** must **not** run external agents in-process or unsandboxed; must **not** auto-approve a permission-request (deny-by-default); must **not** resolve `remote-a2a` in v0.1.0 (reserved); must **not** let a run escape its worktree/egress allow-list; must **not** add the ACP protocol driver (interface shape only); must **not** run the full Go suite locally (CI authority); greenfield.

**Integration boundaries:**
- **External agent CLIs (Claude Code · Codex · opencode):** spawned as own-process children; driven via CLI flags + JSON-streaming stdout; control-in via stdin/flags; auth via each CLI's own credentials (managed in the credential store). Failure (missing binary / auth fail / malformed JSON) = non-fatal, surfaced via the connection test + run report. opencode + git-worktree are net-new (opencode dep is the user's installed binary, not a Go dep).
- **The consent/policy layer (Spec-1/3):** permission-requests routed in; decisions routed back.

## 5. BDD Scenarios

```gherkin
Scenario: Executor schema regenerates clean
  Traces to: US-1 / AC-2
  Category: Happy Path
  Given the sub-agent config carries executor {native|external-cli|remote-a2a}
  When make verify-contracts runs
  Then exit 0

Scenario: External run streams events and routes a permission request
  Traces to: US-2 / AC-1
  Category: Happy Path
  Given an external-cli run (claude-code)
  When the agent requests a permission mid-run
  Then the request surfaces to the consent layer
  And the decision is sent back to the process
  And output/tool-call/diff events stream out

Scenario: Run is own-process, sandboxed, worktree-isolated
  Traces to: US-5 / AC-1
  Category: Happy Path
  Given an external run
  When it starts
  Then it is a child process under Landlock/seccomp with an egress allow-list
  And it executes inside a dedicated git worktree
  And it cannot read outside its allow-list

Scenario: Permission request with no handler denies by default
  Traces to: US-2 (edge)
  Category: Error Path
  Given no consent handler is wired
  When the agent requests a permission
  Then it is denied by default

Scenario: Connection test validates without running work
  Traces to: US-6 / AC-1
  Category: Happy Path
  Given an external-cli runner
  When I run the connection test
  Then it checks binary presence + auth + JSON handshake
  And does not execute a real task

Scenario: remote-a2a is reserved, not resolvable
  Traces to: US-1 / AC-1
  Category: Alternate Path
  Given executor=remote-a2a
  When dispatched
  Then the schema accepts it
  And dispatch reports "not available in v0.1.0"

Scenario: Run exceeding the timeout is terminated
  Traces to: US-7 / AC-1
  Category: Error Path
  Given a per-run timeout
  When a run exceeds it
  Then the process is terminated and reported
```

## 6. TDD Plan

| Order | Test | Level | Traces | Description |
|---|---|---|---|---|
| 1 | `TestExecutorField_SchemaRoundTrip` | Unit | "schema regen" | native/external-cli/remote-a2a |
| 2 | `TestRunner_BidirectionalEvents_ConsentRouted` | Integration | "streams events…" | events-out + control-in (a fake CLI driver) |
| 3 | `TestRunner_PermissionNoHandler_DeniesByDefault` | Unit | "denies by default" | consent default |
| 4 | `TestRunner_OwnProcess_Sandboxed_Worktree` | Integration | "own-process, sandboxed" | hardened_exec + worktree (Linux) |
| 5 | `TestRunner_ConnectionTest_NoWork` | Integration | "connection test" | health check |
| 6 | `TestRunner_RemoteA2A_ReservedNotResolvable` | Unit | "remote-a2a reserved" | dispatch guard |
| 7 | `TestRunner_TimeoutTerminates` | Integration | "exceeding timeout" | bound |
| 8 | `TestRunner_ClaudeCodeDriver_ParsesStreamJSON` | Integration | US-4 | stream-json parse (recorded fixture) |
| 9 | `verify-contracts` (CI) | CI | "schema regen" | drift = fail |
| 10 | `e2e: configure a runner, connection test, run a small task` | E2E | US-6/US-2 | SPA + a real CLI (gated) |

**Test Datasets**: executor enum {native,external-cli:claude-code/codex/opencode,remote-a2a}; permission-request {handler→decision, no-handler→deny}; sandbox {inside allow-list ok, outside denied}; timeout {under→ok, over→terminate}; malformed-JSON→parse-error-nonfatal; missing-binary→connection-test-fail.

**Regression:** new capability + reuses existing pieces. (1) The `claude_cli`/`codex_cli` LLM-provider paths still work (the runner is a sibling use); (2) `hardened_exec` existing behaviour preserved; (3) NEW: the executor field, the runner interface + 3 drivers, the connection test, worktree isolation. **No regression to native sub-agents** (executor=native is the default/existing path). **CI authority; local scoped only** (the sandbox/worktree tests are Linux-gated).

## 7. Functional Requirements & Success Criteria

- **FR-4.1 (C-3):** MUST add an `executor` field (`native | external-cli{cli} | remote-a2a`) to the **agent/sub-agent CONTRACT** (not only `SubagentsConfig`, which is **config-only today** — absent from `contracts/`) so it crosses the gateway/SPA boundary for the runner UI; regenerate; `verify-contracts` exits 0; `native` = default (existing behaviour).
- **FR-4.2:** MUST add a per-runner **connection test** (binary present + authed + JSON handshake) that runs no real work.
- **FR-5.1 (M-1, M-2):** MUST define `ExternalAgentRunner` as **bidirectional** (events-out: output/tool-calls/diffs/permission-requests; control-in: permission-decisions/cancel/input), **consent-routed** (permission-requests → the existing `ws_approval` layer — `ToolApprovalRequest`→`ApprovalDecision`, with a defined external-agent→approval mapping; **deny-by-default**), **resumable** (stable run/session id). The interface is the **inverse direction** of `hook_process` (child emits unsolicited events; Omnipus answers) — correlation reusable, direction new.
- **FR-5.2 (C-2):** MUST drive external agents via **NEW streaming CLI+JSON drivers** — Claude Code (`claude -p --output-format stream-json`), Codex (`codex exec` JSON), opencode (`opencode run --format json`). The existing `claude_cli`/`codex_cli` providers are **one-shot buffered** (`cmd.Run()`, `--dangerously-skip-permissions`) — they prove CLI-spawn+auth but **do NOT stream**; the runner drivers are new and **drop the permission-skip flag** (permissions route to consent).
- **FR-5.3 (operator decision — leverage the tools' own sandboxes, NO new confiner):** the external CLIs **self-sandbox** (Codex = Landlock/seccomp on Linux + Seatbelt on macOS; Claude Code = permission model + bash sandbox). v0.1.0 MUST therefore: (1) **run external CLIs WITH their own sandbox ENABLED** — i.e. **DROP the `--dangerously-skip-permissions` / `--dangerously-bypass-approvals-and-sandbox` flags** the existing one-shot providers use (those *disable* the tool's sandbox); (2) **git-worktree-isolate** each run (isolated temp dir if not a repo) — Omnipus's cheap FS boundary; (3) **route the CLI's permission prompts to Omnipus consent** (deny-by-default). **Native sub-agents** (no external sandbox) ride the **EXISTING Omnipus `workspace.shell` sandbox** — no new primitive. A **startup reaper GCs orphaned run dirs** (M-4). The CLI's **credentials are passed via the env-allowlist**. **There is NO new re-exec confiner primitive** — confinement comes from the tool's own sandbox (external) + the existing sandbox (native).
- **FR-5.6 (M-3, M-5):** the driver MUST **detect/pin the external CLI version** (the JSON stream schema drifts across versions) and degrade gracefully on an unknown version; every run MUST emit **observable events** (start/permission/tool-call/diff/end/error) to the run log + the SPA.
- **FR-5.4:** MUST bound runs (per-run timeout + turn-cap); termination is reported.
- **FR-5.5:** MUST accept but **not resolve** `remote-a2a` in v0.1.0 (reserved; shares the agent-reference shape).

**Success Criteria**
- **SC-1:** `verify-contracts` exits 0 (CI). · **SC-2:** build + typecheck exit 0 (CI authority; local scoped/Linux-gated). · **SC-3:** an external run is its own process, Landlock/seccomp-confined, worktree-isolated (verified on Linux). · **SC-4:** a mid-run permission-request routes to consent; no-handler → deny. · **SC-5:** the connection test validates without running work. · **SC-6:** `remote-a2a` is schema-valid + dispatch-rejected. · **SC-7:** a run over the timeout is terminated. · **SC-8:** `executor=native` sub-agents are unchanged (no regression).

## 8. Traceability Matrix

| Req | US | BDD | Test |
|---|---|---|---|
| FR-4.1 | US-1 | "schema regen" / "remote-a2a reserved" | #1,#6,#9 |
| FR-4.2 | US-6 | "connection test" | #5 |
| FR-5.1 | US-2 | "streams events…" / "denies by default" | #2,#3 |
| FR-5.2 | US-4 | "ClaudeCode driver parses stream-json" | #8 |
| FR-5.3 | US-5 | "own-process, sandboxed" | #4 |
| FR-5.4 | US-7 | "exceeding timeout" | #7 |
| FR-5.5 | US-1 | "remote-a2a reserved" | #6 |

## 9. Ambiguity Warnings

| # | Ambiguous | Likely assumption | Resolution |
|---|---|---|---|
| 1 | executor field location | on the sub-agent config (Spec-3 owns the sub-agent struct) | Phase-3.5 cross-spec — coordinate the shared sub-agent schema |
| 2 | consent-routing mechanism | reuse Spec-1 consent + Spec-3 policy | wire permission-requests into the existing consent layer |
| 3 | worktree for non-repo runs | a run not against a git repo still gets an isolated temp dir (worktree only if a repo) | RESOLVED — worktree if repo, else isolated temp dir under sandbox |
| 4 | Codex headless stability | best-effort (Codex JSON stream is less stable — ADR/research) | RESOLVED — Claude Code first-class; Codex/opencode best-effort, surfaced |
| 5 | sandbox on non-Linux | Landlock/seccomp Linux-only; fallback app-level (existing pattern) | RESOLVED — graceful degradation per `hardened_exec` platform split |

## 10. Holdout Evaluation Scenarios *(post-impl; NOT in traceability)*
- H1: configure a Claude Code runner, connection-test it, run a small task → diffs/output stream; a permission prompt surfaces to you.
- H2: point a runner at a missing binary → connection test fails clearly.
- H3: a hostile prompt tries to read `/etc/passwd` from inside a run → blocked by the sandbox.
- H4: kill a run mid-flight → worktree cleaned, no orphans.
- H5: set executor=remote-a2a → accepted in config, dispatch says not-available.
- H6: a run that loops forever → terminated at the timeout.

## 11. Assumptions
- `claude_cli`/`codex_cli` providers + `hook_process` (JSON-RPC/stdio) + `hardened_exec` are the reuse base. `[FACT]`
- opencode + git-worktree isolation are net-new; the external CLIs are the user's installed binaries (not Go deps). `[ADR FR-2 grounding]`
- The sub-agent struct (where `executor` lives) is owned by Spec-3; coordinated at Phase-3.5. `[cross-spec]`
- ACP protocol driver + A2A resolution are later; v0.1.0 ships the bidirectional interface *shape* + the reserved `remote-a2a` kind. `[ADR FR-11]`
- Sandbox degrades gracefully off-Linux (existing `hardened_exec` pattern). `[FACT]`
