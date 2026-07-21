# ADR-046: Unified filesystem & workspace model — single working directory, `filesystem_scope` policy, per-exec-child sandbox

- **Status:** Proposed (operator decisions locked; `/grill-spec` completed 2026-07-16 — claims verified, ADR revised per §10; ready for `/plan-spec`)
- **Date:** 2026-07-16
- **Deciders:** Daniel Piatkowski (operator)
- **Evidence level (highest used):** 1 (user-input: operator decided every fork in a design session) + 2 (direct code evidence: an 8-agent audit of all 78 builtin tools + non-tool path elements) + 5 (expert reasoning: the Landlock one-shot-per-process constraint, grounded in code)

> **Operator decisions (2026-07-16 design session) — these are the locked forks this ADR records:**
> 1. **"workspace" gets exactly one meaning** (the shared collaborative space). The per-agent directory is renamed to **"agent home"**.
> 2. **The working directory is always a workspace's `work/`** (the turn's workspace, else the agent's default personal workspace).
> 3. **A new `filesystem_scope` tool-policy — `allow`/`ask`/`deny`** — governs access *outside* the working directory (per-agent + global default, seeded explicitly). **Default `ask`; `ask` is per-path/session-remembered; unattended `ask` fails closed to `deny`; hard carve-outs (`master.key`, `credentials.json`, other agents' homes, other workspaces) are denied even under `allow`.**
> 4. **Enforcement = per-exec-child kernel sandbox.** Boot no longer applies the process-wide `$OMNIPUS_HOME` Landlock fence; each `bash`/exec spawns a child whose Landlock/seccomp ruleset is computed from the agent's scope. In-process file tools enforce via an app-layer resolver.
> 5. **One mandatory `ResolvePath` chokepoint** every path-taking tool must call. No tool may capture a frozen base directory again.
> 6. **Per-workspace `working_dir` override** — default is the internal `work/`; a workspace may point its working directory at any real directory on disk (Claude-Code / "open a folder" model). Config-pointer is canonical; a symlinked `work/` transparently works on POSIX.

## 1. Problem Understanding

The word **"workspace"** currently names two unrelated things in the code, and the collision is the root of a whole class of path bugs:

- **The per-agent directory** — `AgentInstance.Workspace` / `AgentConfig.Workspace`, resolved by `resolveAgentWorkspace` (`pkg/agent/instance.go:812-854`) to `$OMNIPUS_HOME/agents/<id>/` (identical to `datamodel.AgentWorkspacePath`, `pkg/datamodel/init.go:181`). Holds identity (SOUL/AGENT/HEARTBEAT.md), sessions, private memory. `[FACT]`
- **The shared workspace** — `pkg/workspace.Workspace` = `$OMNIPUS_HOME/workspaces/<wsid>/` (team, board, tasks, Project Instructions, shared memory room), with a `work/` subdirectory (`workspace.WorkDir`/`SafeWorkDir`, `pkg/workspace/instructions.go:87-102`) that is the real, confinement-safe working area. `[FACT]`

Per-turn, `AgentLoop.runTurn` (`pkg/agent/loop.go:5622-5692`) sets a context signal `tools.WithTurnWorkspaceDir(ctx, wsDir)` = `workspaces/<wsid>/work/` **when the acting agent is a CoreTeam member of a workspace** — keyed by agent identity, not by whether the chat is workspace-bound. `[FACT]` Only **two files read that signal** — `pkg/tools/filesystem.go` (read/write/edit/append/list) and `pkg/tools/shell.go` (`bash`/exec) — so only those tools re-root to `work/`; every other path-taking tool resolves against the frozen construction-time agent home. `[FACT]`

An 8-agent audit of **all 78 builtin tools** (32 general + 35 system + 11 browser) plus non-tool path elements produced a complete pass/fail map. **Four working-directory defects** result directly from the frozen-base pattern: `[FACT]`

| Tool | Location | Resolves to (wrong) | Should be |
|---|---|---|---|
| `serve_web` | `pkg/tools/web_serve.go:336,531` | `agents/<id>/` (frozen) | working dir |
| `send_file` | `pkg/tools/send_file.go:111` | `agents/<id>/` (frozen) | working dir |
| `browser_screenshot` | `pkg/tools/browser/tools.go:472` | `os.TempDir()` (`/tmp`) | working dir |
| `install_skill` | `pkg/tools/skills_install.go:97` | `agents/<id>/skills/` | global `$OMNIPUS_HOME/skills` (`~/.omnipus/skills`, see §10 F1) |

Everything else audited is correct or on a separate, intentional axis: the 35 `system.*` tools (legit entity/config admin), memory (ADR-027 rooms, its own `WorkspaceID` context key), media/uploads (`$OMNIPUS_HOME/uploads/<sid>/`, surfaced via `media://` refs), sessions (agent-private, deliberately never re-rooted), and the task store (global by design). `[FACT]`

A second, deeper defect: the agent needs to be able to work **outside** any Omnipus-managed directory (the user's real projects), under user control — but there is no such capability today, and the kernel sandbox actively prevents it. **Landlock is a one-way ratchet applied once, process-wide, at boot, over all of `$OMNIPUS_HOME`** (`pkg/sandbox/sandbox.go:305-423`, applied via `LinuxBackend.Apply` with a `processLandlockApplied` guard, `pkg/sandbox/sandbox_linux.go:203-249`). A child process inherits that ruleset and can only add *more* restriction — it can never reach *outside* `$OMNIPUS_HOME`. `[FACT]` This is the `.preview-doc` "critical blocker ①". So "let the agent access files the user can access" is impossible under the current sandbox model.

**Stakeholders:** the operator (wants agents that can work in real project directories, with a permission gate), the agent (every path-taking tool), and future hosted deployments.

**Blast radius:** every filesystem-touching tool, the sandbox/enforcement layer, the config schema + Agent/global-policy UI, and ~110 references to `AgentInstance.Workspace`.

## 2. Extracted Requirements

### Functional
- **FR-1:** "workspace" MUST denote exactly one concept (the shared collaborative space); the per-agent directory MUST be renamed. `[USER-INPUT]`
- **FR-2:** The agent working directory MUST be a workspace's `work/` for every turn (turn's workspace, else the agent's default personal workspace). `[USER-INPUT]`
- **FR-3:** A `filesystem_scope` policy (`allow`/`ask`/`deny`) MUST exist per-agent and as a global default, following the established tool-policy pattern and seeded explicitly (no code default — Constraint #6). `[USER-INPUT]`
- **FR-4:** `deny` MUST hard-confine the agent to its working directory; `allow` MUST permit any path the OS user can access (minus carve-outs); `ask` MUST have `allow`'s reach but prompt on each new outside path. `[USER-INPUT]`
- **FR-5:** `filesystem_scope` MUST be applied and enforced by **every** tool the agent can use — file tools, `bash`/exec, `serve_web`, `send_file`, `browser_screenshot`, and any future path-taking tool. `[USER-INPUT]`
- **FR-6:** Every path-taking tool MUST resolve paths through **one** mandatory resolver; relative paths root at the working directory, absolute paths are permitted subject to `filesystem_scope`. `[USER-INPUT]`
- **FR-7:** A workspace MUST be able to point its working directory at an arbitrary real directory on disk (default = internal `work/`). `[USER-INPUT]`
- **FR-8:** The four audited defects (`serve_web`, `send_file`, `browser_screenshot`, `install_skill`) MUST be resolved by FR-6 (they route through the resolver / correct bucket). `[INFERENCE]`

### Non-Functional
- **NFR-1 (Security):** `deny` MUST be enforced at the **kernel** level for `bash`/exec (a shell can read files directly, bypassing app-layer checks). `[INFERENCE]`
- **NFR-2 (Security):** Omnipus internals (`$OMNIPUS_HOME/master.key`, `credentials.json`) and cross-tenant private data (other agents' homes, other workspaces) MUST be denied even under `allow`. `[USER-INPUT]`
- **NFR-3 (Safety):** When no interactive approver is reachable, `ask` MUST fail closed to `deny` (no silent grant). `[USER-INPUT]`
- **NFR-4 (Cross-platform):** The working-directory override MUST work on every supported OS (no dependency on privileged symlinks). `[USER-INPUT]`
- **NFR-5 (Single binary, no CGo, graceful degradation):** unchanged hard constraints — the per-exec-child sandbox MUST degrade to app-level enforcement on non-Landlock kernels/OSes. `[FACT]` (CLAUDE.md #1–#4)

### Constraints
- Single Go binary, pure Go, no new runtime deps. `[FACT]`
- Constraint #6: every policy decision is explicit, seeded data — no code default/fallback for `filesystem_scope`. `[FACT]`
- v0.3 scope: fresh-build, **no back-compat** — the rename and the removal of the boot fence are clean breaks. `[FACT]` (Release Strategy)

## 3. Gaps, ambiguities, and their resolutions

| # | Question | Resolution | Confidence |
|---|---|---|---|
| 1 | Rename target for the per-agent dir? | **"agent home"** (`AgentHome` / `AgentHomePath`); "workspace" reserved for the shared space. | High `[USER-INPUT]` |
| 2 | Working dir when a turn has no bound workspace? | The resolver MUST guarantee a workspace for **every** agent/turn. **§10 F2 (grill):** the current CoreTeam-keyed machinery does NOT — `My Workspace` is a single *shared*, best-effort-at-boot, core-roster-only team, so a custom agent falls through to its agent home. New requirement: decouple working-dir resolution from CoreTeam membership; auto-ensure a default workspace per agent. Shared-`My Workspace`-for-all vs per-agent-personal-default is an open sub-decision (§10 F2; lean: per-agent personal default). | Revised — see §10 F2 |
| 3 | Default `filesystem_scope`? | **`ask`** (global + seeded per-agent). | High `[USER-INPUT]` |
| 4 | `ask` granularity? | **Per path (or subtree), remembered for the session**, via the existing approval WS flow. | High `[USER-INPUT]` |
| 5 | Unattended `ask` (channels/tasks/heartbeats/background)? | **Fail closed to `deny`.** Operator opts an unattended agent up to `allow` explicitly. | High `[USER-INPUT]` |
| 6 | Carve-outs under `allow`? | **Hard-deny** `master.key`, `credentials.json`, other agents' homes, other workspaces — always, regardless of scope. | High `[USER-INPUT]` |
| 7 | `serve_web` served dir under `allow`? | Follows the same scope gate (the preview URL is token-gated and reaches only the user, who can already access those files); a warning is surfaced when serving outside `work/`. | Medium `[INFERENCE]` — confirm in grill |
| 8 | Working-directory override mechanism? | **Config pointer** on the workspace (`working_dir`) is canonical/cross-platform; a symlinked `work/` transparently works on POSIX because the resolver anchors confinement on the realpath. | High `[USER-INPUT]` |
| 9 | Does dropping the boot Landlock weaken in-process protection? | Yes — in-process file tools are then guarded by the app-layer resolver only. Accepted: the resolver is the single chokepoint and becomes security-critical; a minimal always-deny carve-out set (NFR-2) may still be applied process-wide. | Medium `[INFERENCE]` — confirm exact residual main-process protection in grill |

## 4. Decision criteria

| Criterion | Weight | Notes |
|---|---|---|
| One unambiguous meaning for "workspace" | **Critical** | Root cause of the bug class |
| `allow` genuinely reaches the user's whole filesystem | **Critical** | Operator requirement; impossible under the boot Landlock fence |
| `deny` is real for `bash` (kernel-enforced) | **Critical** | App-layer alone can't confine a shell |
| Cross-platform working-dir override | **High** | No privileged-symlink dependency |
| Closes the frozen-base bug class, not just 4 instances | **High** | Structural fix via the mandatory resolver |
| Secrets + tenant isolation preserved under `allow` | **High** | NFR-2 |
| Single binary / pure Go / graceful degradation | **Hard constraint** | CLAUDE.md #1–#4 |
| Explicit seeded policy, no code default | **Hard constraint** | Constraint #6 |

## 5. Option analysis (per fork)

### 5.1 Enforcement model

- **Option A — App-layer only.** `ResolvePath` gate for file tools; `bash`/exec unconfined. Rejected: `deny` is advisory for a shell (`cat /etc/passwd` bypasses it). Fails NFR-1.
- **Option B — Keep the boot `$OMNIPUS_HOME` Landlock fence.** `allow` can reach anything under `$OMNIPUS_HOME` only, never the wider filesystem. Rejected: fails FR-4 ("all files the user can access").
- **Option C — Per-exec-child kernel sandbox (CHOSEN).** Boot drops the process-wide fence; each `bash`/exec spawns a child whose Landlock/seccomp ruleset is computed from scope (`deny`→working dir + libs + `/tmp`; `ask`→working dir + approved paths; `allow`→unfenced minus carve-outs). File tools enforce via the app-layer resolver. Only option that satisfies both "`allow` reaches everything" and "`deny` is kernel-real for `bash`". Cost: the main process is no longer kernel-fenced for in-process file ops (mitigated: single resolver chokepoint + NFR-2 carve-outs). Omnipus already spawns hardened children (`pkg/sandbox/hardened_exec.go`, `SpawnBackgroundChild`), so this extends existing machinery. `[USER-INPUT]`

### 5.2 Default scope
- `deny` (secure-confined) vs **`ask` (CHOSEN)**. Operator chose `ask` — agents may reach the user's files with a prompt, matching the "collaborative assistant across my machine" intent. Unattended contexts fail closed to `deny` (5.4), so the permissive default is bounded.

### 5.3 `ask` granularity
- Per-access (noisy) vs **per-path/session-remembered (CHOSEN)** vs per-turn (coarse). Chosen balances control and interruption; first touch of a path/subtree prompts, then it's cached for the session.

### 5.4 Unattended `ask`
- **Fail-closed to `deny` (CHOSEN)** vs route-approval-to-channel vs treat-as-`allow`. Chosen for safety; channel-routed approval is a possible later enhancement, not required now.

### 5.5 Carve-outs
- Literal all-user-files vs **hard-deny internals + cross-tenant (CHOSEN)**. Chosen to protect the credential vault (an `allow` agent reading `master.key` could decrypt every stored secret) and tenant isolation.

### 5.6 Working-directory override mechanism
- Pure symlink (rejected as primary: Windows symlinks need privilege / Developer Mode — fails NFR-4) vs **config pointer (CHOSEN)**. The config pointer is cross-platform and resolved centrally by the one resolver; a POSIX symlink of `work/` transparently works too because the resolver anchors on realpath.

## 6. Decision

Adopt the unified model:

1. **Terminology.** "workspace" = the shared space (`workspaces/<wsid>/`). Rename `AgentInstance.Workspace`/`AgentConfig.Workspace`/`resolveAgentWorkspace`/`datamodel.AgentWorkspacePath` and all ~110 references to **agent home** (`Home` / `AgentHomePath`, `agents/<id>/`) — identity + sessions + private memory only. Clean v0.3 break.

2. **Working directory.** Every turn resolves to one workspace (its bound workspace, else the agent's default workspace). The working directory is that workspace's **effective work dir** = its `working_dir` override if set, else `workspaces/<wsid>/work/`. No turn ever works out of the agent home. **Working-dir resolution is decoupled from CoreTeam membership** (the old ADR-032 mechanism, which left custom agents unrooted — §10 F2): the resolver MUST resolve every agent/turn to a workspace, auto-ensuring a default one when none exists.

3. **`filesystem_scope` policy.** New tri-state (`allow`/`ask`/`deny`), per-agent (`tools.filesystem_scope`) and global default (`sandbox.filesystem_scope`), seeded explicitly (Constraint #6), replacing the `restrict` bool. Semantics govern access **outside the effective working dir**:
   - `deny` → hard-confined to the working dir.
   - `allow` → any path the OS user can access, **minus** the carve-out deny-list.
   - `ask` → `allow`'s reach, prompting on each new outside path; approvals cached per session; **fails closed to `deny`** when no interactive approver is reachable.
   - **Carve-outs (always denied, even under `allow`):** `$OMNIPUS_HOME/master.key`, `$OMNIPUS_HOME/credentials.json`, other agents' homes (`agents/<other>/`), other workspaces (`workspaces/<other>/`).
   - **Default = `ask`.**

4. **Enforcement.** Boot no longer applies the process-wide `$OMNIPUS_HOME` Landlock. `bash`/exec spawn a per-call hardened child whose Landlock/seccomp FS rules are computed from the agent's `filesystem_scope` and effective working dir (grant the *realpath* of the working dir). File tools (in-process) enforce via the app-layer resolver. On non-Landlock platforms, degrade to app-level enforcement (NFR-5).

5. **`ResolvePath` chokepoint.** A single mandatory `ResolvePath(ctx, rawPath) → (absPath, error)`: relative → the turn's effective working dir; absolute → gated by `filesystem_scope`; symlinks resolved (anchor confinement on realpath) to prevent escape and to make an external/symlinked working dir transparent. **Every** path-taking tool must call it; capturing a frozen base at construction is forbidden. This resolves FR-8 (the four defects) for free.

6. **Per-workspace `working_dir` override.** Optional absolute target on the workspace; default = internal `work/`. Validated on set (real/creatable directory; no overlap with `$OMNIPUS_HOME` internals or another tenant's tree). `deny` then confines the agent to that real directory — the "open my project folder, stay in it" model. Config pointer canonical; POSIX symlink of `work/` also works.

7. **UI/config placement.** `filesystem_scope` appears in the Agent form's tool-permissions section and the global tool-policy screen (established allow/ask/deny pattern). `working_dir` appears in Workspace settings.

## 7. Consequences

### Positive
- One meaning for "workspace"; the frozen-base bug **class** is closed by construction, and the four audited defects disappear.
- Agents can work in the user's real project directories under an explicit, kernel-enforced permission gate — Omnipus workspaces become first-class "project folders".
- `deny` + an external `working_dir` gives a genuine IDE-style "confine this agent to this project" guarantee, kernel-enforced for `bash`.
- Secrets and tenant isolation are preserved even under `allow`.

### Negative
- The main gateway process is no longer kernel-fenced for in-process file ops; the `ResolvePath` resolver becomes security-critical (single point of enforcement for file tools). Mitigated by the chokepoint discipline + always-deny carve-outs, and to be pinned down in grill-spec (residual minimal main-process protection).
- Per-exec-child sandbox setup adds cost to every `bash`/exec (a fresh Landlock/seccomp apply per child) and complexity to the exec path.
- Large rename (~110 refs) and a v0.3 breaking change to the sandbox boot behavior.
- `ask`-as-default means interactive turns prompt; unattended turns silently confine (fail-closed) — operators must opt specific unattended agents up to `allow`.

### Neutral / out of scope (separate axes, untouched)
- Memory (ADR-027 rooms + its own `WorkspaceID` context key), media/uploads (`media://` refs), sessions/transcripts (agent-private), and the global task store are correct on their own models and are **not** changed by this ADR.
- Minor cleanup (not this ADR's core): `datamodel.InitAgentWorkspace` pre-creates dead `agents/<id>/memory/` + `memory/daily/` dirs (real memory uses `.omnipus/memories/`).

## 8. Open items for `/grill-spec`
- Exact residual main-process protection once the boot Landlock is dropped (gap #9) — is a minimal always-deny ruleset applied process-wide for the carve-outs, or is that purely per-child + app-layer?
- `serve_web` serving outside `work/` under `allow` over the token-gated preview URL (gap #7) — confirm the exposure model and warning.
- Per-exec-child sandbox mechanism feasibility/perf: confirm `hardened_exec`/`SpawnBackgroundChild` can apply a *per-call* Landlock ruleset (re-exec-with-preexec vs in-child apply) and the cost budget (< 10MB RAM overhead, Constraint #3).
- `working_dir` override validation rules and the cross-platform confinement story when the target is on a different mount / network path.
- Graceful degradation matrix (older kernels, non-Linux, Android/Termux) for the tri-state under app-level-only enforcement.

## 9. Superseded / related
- Supersedes the `experimental.workspace_rooted_filesystem` flag (removed `d9f1e231`, 2026-07-04) and the CoreTeam-membership re-root keying (ADR-032 amendments) with "the turn's workspace" + explicit `filesystem_scope`.
- Reconciles the `.preview-doc/spaces.html` "Filesystem · sandbox · prompt engine" to-be (which flagged the Landlock blocker and asked for exactly this ADR).
- Fixes the defects catalogued in the 2026-07-16 path-rooting audit (`serve_web`, `send_file`, `browser_screenshot`, `install_skill`).

## 10. Grill-spec review (2026-07-16)

Three parallel verification agents fact-checked every code claim against the tree. **Verdict: the decisions stand; no fork reversed.** The ADR is revised below to correct facts and turn two material gaps the grill exposed into explicit requirements.

**Confirmed accurate:** the four defect sites (`serve_web`/`send_file`/`browser_screenshot`/`install_skill`) and their file:lines; "only `filesystem.go` + `shell.go` read `TurnWorkspaceDir`"; `resolveAgentWorkspace` → `agents/<id>/` (per-agent branch of a 3-branch resolver); `WorkDir`/`SafeWorkDir` → `workspaces/<id>/work/`; `TurnWorkspaceDir` keyed on agent identity (CoreTeam), not turn binding; the `allow`/`ask`/`deny` tool-policy pattern (values + per-agent/global layering) and the interactive approval WS flow (`PolicyApprover.RequestApproval` → `tool_approval_required` frame → `POST /tool-approvals/{id}`, session-remembered via `ApprovalGrantStore`) both exist and are reusable **in shape**.

**Findings & revisions:**

- **F1 (fact, LOW).** The global skills dir is `$OMNIPUS_HOME/skills` (`~/.omnipus/skills`, via `getGlobalConfigDir()` / `SkillsLoader.GlobalSkillsDir()`), **not** `$HOME/skills/`. Corrected. `install_skill`'s fix targets this path.
- **F2 (design gap, HIGH — now an explicit requirement).** FR-2 ("working dir is always a workspace `work/`") is **not** deliverable by today's machinery: re-root fires only for CoreTeam members via `FindForAgentPreferring`; the default `My Workspace` is a *single shared* team (not personal), seeded *best-effort at gateway boot* (`ensureDefaultWorkspace`, non-fatal), whose CoreTeam is *core-roster only* (`defaultWorkspaceTeam` = `coreagent.All()` ∩ config) — so a **custom agent on no team falls through to its agent home**, violating FR-2. **Revision:** the new resolver MUST NOT reuse CoreTeam-membership keying; it MUST resolve every turn to a workspace and auto-ensure a default workspace per agent. **Open sub-decision:** one shared `My Workspace` every agent may use, vs a **per-agent personal default workspace** (lean: per-agent, so `deny` confinement is per-agent-isolated).
- **F3 (design gap, MEDIUM-HIGH).** `filesystem_scope` is a **scalar**, not an entry in the tool-`policies` map, so the Constraint-#6 coverage validator (`ValidateToolPolicyCoverage`, iterates knownTools×agents) does **not** cover it — a **new seed + validation path** is required (not a free ride on the existing map validation). **Global×agent resolution rule** (unspecified before): adopt the existing tool-policy semantics — **most-restrictive-wins** (a global `deny` caps every agent; an agent may be *more* restrictive than the global default, never more permissive).
- **F4 (scope, MEDIUM).** "Replaces the `restrict` bool" understates it: the current mechanism is a **pair** — `RestrictToWorkspace` + `AllowReadOutsideWorkspace` (a read-vs-write asymmetry: "read outside but deny write outside" was expressible), global-only, **env-only** (removed from the JSON schema, kept as an ops hatch). `filesystem_scope` consolidates that pair **plus** the removed `experimental.workspace_rooted_filesystem` flag and the CoreTeam re-root keying. **Open sub-decision:** keep `filesystem_scope` a single symmetric tri-state (simpler; drops the read/write asymmetry) or split into read-scope + write-scope (lean: single tri-state for v1; revisit if asymmetry is needed).
- **F5 (design gap, MEDIUM).** The approval flow is **keyed on tool** (`session+agent+toolName`; grant store `ApprovalGrantStore.IsAllowed(session, agent, tool)`). Per-path/session `ask` needs a **path/subtree dimension** added to the `tool_approval_required` frame payload and the grant key. Reusable in shape, not a drop-in. Requirement added.
- **F6 (design gap, HIGH — now an explicit requirement).** "Unattended `ask` → fail-closed to deny" is only realized for the **scheduled** path (`RunTurnOptions.AutoDenyAsk`, set solely in `ProcessScheduled`). **Channels, heartbeats, non-scheduled tasks, and background delegations do NOT set it** — they would `RequestApproval`, **broadcast the frame to any session-owner web client, and block up to the 300s timeout** before denying (a stall, plus a human-at-the-web-UI-approves-a-channel-turn leak) rather than a clean immediate fail-close. **Revision:** introduce a per-turn "interactive approver reachable?" predicate (extend `AutoDenyAsk` / a per-turn origin-surface field to *every* non-webchat origin); unattended fail-close MUST be **immediate**, not timeout-driven.
- **F7 (implementation hazard, MEDIUM).** The rename MUST be **identifier-scoped (per-symbol)**, never a text sweep: 297 `.Workspace` accesses exist repo-wide and ~79+ refer to the *other* concept (`pkg/workspace.Workspace`, generated API types); the codebase itself warns against conflation (`pkg/agent/instance.go:800-807`, `openapi_types.gen.go:5041-5073`). A blind textual rename would corrupt the shared-workspace feature.

**Net:** two HIGH findings (F2 working-dir guarantee, F6 unattended fail-close) become explicit requirements rather than glossed assumptions; F1/F3/F4/F5/F7 are corrections and clarifications. Every locked decision is unchanged; the ADR is ready for `/plan-spec` with the three open sub-decisions (F2 per-agent-vs-shared default workspace, F4 symmetric-vs-split scope, plus the §8 items) carried forward.

**Note (2026-07-21 amendment, cross-reference only — does not change this ADR's status or F2's text):** ADR-052 (autonomous agent plan execution) implementation made System Agents (`coreagent.IsSystemAgentID`) IMPLICIT members of every workspace (`pkg/workspace/find_for_agent.go`'s `isImplicitMember`, consulted by `FindForAgent`/`FindForAgentPreferring`) — an operator-directed fix, 2026-07-21. This is necessary because F2's CoreTeam-keyed membership gate (as implemented, `resolveTurnWorkDirOrRefuse`) is definitionally unsatisfiable for a System Agent: `validateCoreTeamMembers` (`pkg/gateway/rest_workspaces.go`) permanently bars any System Agent from ever being added to a workspace's literal `core_team` roster, so the Judge's own verifier turns were refused outright ("agent is not a member of any workspace") until this fix. The carve-out is scoped to System-Agent identity, layered on top of F2's CoreTeam-keyed resolver rather than replacing it — ordinary agents still resolve via literal `core_team` membership exactly as F2 describes. The multi-tenant / `filesystem_scope` carve-out reconciliation this ADR's Decision item 3 anticipates (other agents' homes / other workspaces always denied even under `allow`) is deferred until that feature ships; this note does not resolve it, only records that System-Agent membership is now unconditionally implicit rather than roster-driven. See `docs/internal/architecture/ADR-052-autonomous-agent-plan-execution.md` §10 and `docs/internal/specs/autonomous-agent-plan-execution-spec.md`'s Implementation note 5.
