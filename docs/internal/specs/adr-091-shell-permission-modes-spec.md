# ADR-091 Implementation Specification — Shell permission modes (Ask / Auto / God Mode)

- **Spec status:** Draft for lead review (plan-spec output; not committed — lead commits)
- **ADR implemented:** [ADR-091 — Shell permission modes](../architecture/ADR-091-shell-permission-modes.md) (Proposed, founder-approved decisions incl. UI, 2026-09-23)
- **Evidence baseline:** `release/v0.1.1` @ `838d8092c` (read-only)
- **Depends on:** ADR-036 (one `bash` tool, one approval flow), ADR-063 (one authored `fspolicy.FSPolicy` + `DeriveKernelPolicy`), ADR-077 (two-layer tool policy), ADR-090 (built-in agents; Jim carries an explicit `bash: deny`)
- **Repo rule honoured throughout:** greenfield — no migration, no deprecation shims, no read-and-ignore of retired keys. `file::symbol` citations, never line numbers.

---

## 1. Purpose and scope

This spec turns ADR-091 into testable requirements, BDD scenarios, test data, contract-first work, removal tasks, a parallel delivery plan, and acceptance criteria. ADR-091 replaces six uncoordinated shell-guard mechanisms with one story: a three-way mode (Ask / Auto / God Mode) that selects how the `bash` tool resolves, one operator-authored rule format, a redesigned approval dialog, and a pre-flight evaluation that reuses the existing single-source-of-truth filesystem policy rather than inventing a parallel one.

**In scope:** the three modes and their three-level resolution; the pre-flight evaluator and its anti-drift lock test; the unified rule format and decision order; the approval dialog redesign and the suggested-prefix algorithm; binary resolve-and-bind; God Mode behaviour; the platform predicate; four audit events; the status/badge contract field; and every item in the ADR's removal inventory.

**Out of scope:** per-website network approvals; a session-grants management surface; headless-turn special-casing; network egress changes (explicitly unchanged); Windows kernel sandbox (still none). Full out-of-scope list in §11.

---

## 2. Existing code context

Manual reading of the evidence files confirms the ADR's cited call chains. No GitNexus index is available for this worktree; the caller analysis below is grep-verified in the ADR and re-confirmed by reading the named symbols directly.

| Symbol (file) | Role in ADR-091 | Fate |
|---|---|---|
| `ResolveTurnFSPolicy` (`pkg/tools/resolvepath.go`) | The single authored per-turn `fspolicy.FSPolicy` — consumer 1's and consumer 2's shared input | **Unchanged**; becomes consumer 3's (pre-flight) input |
| `DeriveKernelPolicy` (`pkg/sandbox/derive_from_fspolicy.go`) | "THE single function that turns an authored per-turn policy into a kernel policy" (ADR-063 D1/FR-1.3) | **Unchanged**; the anti-drift lock test compares the pre-flight verdict against its output |
| `resolveEffectivePolicyWith` (`pkg/tools/compositor.go`) | strictest-wins global×agent merge; `if cfg.GodMode { g = allow }` floors the *global* side | **Unchanged** — the modes select what the ceiling resolves to, they do not edit this merge |
| `defaultDenyPatterns` / `secretGuardPatterns` / `buildSecretGuardPatterns` (`pkg/tools/shell.go`) | the ~38-regex block list (+ secrets backstop) | **Deleted** (D2) |
| `applyDenyPatterns` / `compileDenyPatterns` / `denyPatternMessage` (`pkg/tools/shell_guard.go`) | the block-list matcher; callerless once D2 lands | **Deleted** (D2); `lowerASCII` survives (moves to `shell_subst_guard.go`) |
| `guardCommand` step 4 / `checkPathSegment` (`pkg/tools/shell_path_guard.go`) | tokenized absolute-path scan + ADR-068 read/write carve-out | **Survives, mode-sensitive** (denial disposition now depends on mode) |
| `splitShellSegments` / `shellCommandHead` (`pkg/tools/shell_subst_guard.go`) | existing shell tokenizer | **Survives**; gains a new caller (the D3 rule matcher) |
| `Evaluator.EvaluateExec` / `MatchGlob` / `FirstToken` (`pkg/policy/evaluator.go`) | opt-in exec allowlist, raw-string match (issue #83 "Additional") | **Deleted, folded into D3** |
| `PolicyAuditor.logDecision` (`pkg/policy/auditor.go`) | audit-log write | **Survives, retargeted** to log the D3 engine's verdict |
| `ExecApprovalManager` et al. (`pkg/security/execapproval.go`) | dead "always allow" manager behind issue #83 headline | **Deleted** (D5), zero production callers |
| `ApprovalGrantStore` (`pkg/security/approvalgrants.go`) | session-scoped `(session, agent, tool)` → exact-args fingerprint store | **Survives, extended** — prefix grants + path-widening record kinds |
| `ToolApprovalModal` (`src/components/agents/ToolApprovalModal.tsx`) | the one generic approval dialog; today four buttons (`approve`/`deny`/`always`/`cancel`) | **Redesigned** to `[Deny][Allow once][Allow]` + scope radio |

**Impact assessment (from the ADR's grep-verified caller counts, which are the authoritative churn signal for this change):**

| Symbol removed/modified | Risk | Callers that MUST be updated or removed |
|---|---|---|
| `defaultDenyPatterns` / `secretGuardPatterns` | MEDIUM | `NewExecToolWithConfig` wiring; `applyDenyPatterns` (2 callers in `shell_path_guard.go`) |
| `Evaluator.EvaluateExec` / `MatchGlob` / `FirstToken` | LOW (contained) | `PolicyAuditor.EvaluateExec` → `shell.go`; `loop_construct.go` (`NewEvaluator`); `rest_exec.go` |
| `ExecApprovalManager` | LOW (dead) | none — its own package tests only |
| `ShellDenyPatterns` / `AgentShellPolicy` / `ExecConfig.AllowedBinaries` | MEDIUM | `loop_wire.go`, `sandbox_config_validation.go`, `rest_sandbox_config.go`, `rest_exec.go`, config load |

---

## 3. Functional requirements

### 3.1 Modes and three-level resolution

**FR-001 — Three modes.** Every bash-capable context resolves to exactly one of `ask`, `auto`, or `god` mode. `auto` is the shipped default for a fresh install. God Mode is global-only.

**FR-002 — Three-level, tighten-only merge.** The effective mode is the product of three levels in order — global default → per-agent override → per-chat (session) modifier — where each lower level may only tighten (never loosen) the level above it. God Mode appears only at the global level.

**FR-003 — Server-side loosening rejection.** A per-agent or per-chat write that would loosen below the global default is rejected by the REST write handler with a 4xx response. The handler re-reads the global default at write time; it does not trust the client's claim and does not silently clamp. Hiding the looser option in the UI is defence-in-depth only, never the enforcement.

**FR-004 — Session-scoped modifier, not a third policy layer.** The per-chat quick switch is a session-scoped resolution modifier applied *after* the ADR-077 global×agent merge resolves the `bash` policy value. It must not be carried as a new key on `cfg.Sandbox.ToolPolicies` or `AgentConfig.Tools.Builtin.Policies` (Hard Constraint #6 — "two layers, no third" is non-negotiable). It is new, separate, session-keyed state, structurally like `ApprovalGrantStore`.

**FR-005 — Delegation inheritance.** The session modifier inherits to a delegated subagent session exactly as approval grants do (`ApprovalGrantStore.InheritFrom`, ADR-057). A delegated subagent resolves the tightest of (parent's session modifier, the delegate's own per-agent override), both on top of the global default. A chat tightened to Ask stays Ask under a delegate with no override.

**FR-006 — Mid-turn and restart semantics.** A command is resolved against the mode in force when it is *resolved*; a tighten that lands mid-turn does not retroactively change an already-resolved command. On gateway restart the modifier clears with the session and the chat resumes at its global/agent default.

**FR-007 — Platform predicate.** "Kernel sandbox active" is a single server-derived predicate: Linux = Landlock available **and** applied (enforce, not the app-level fallback); macOS = Seatbelt backend active; Windows = never. This predicate drives both the Auto→Ask fallback and the badge's status field, and both read exactly it (one value, not two derivations).

**FR-008 — Auto→Ask fallback.** Where the platform predicate says no kernel sandbox is active, Auto behaves identically to Ask: every command prompts, and the app-layer guards are never silently trusted as if they were a real kernel boundary.

### 3.2 Pre-flight evaluation and the single-source-of-truth rule

**FR-009 — Pre-flight, never start-then-interrupt.** In Auto, before a command is spawned, it is evaluated against the same filesystem rules the kernel will enforce. When the evaluation says the command needs more than the kernel would grant, the command is never started; the escalation prompt comes first.

**FR-010 — One policy value, not two implementations.** The pre-flight evaluator resolves the turn's one `fspolicy.FSPolicy` via `ResolveTurnFSPolicy` and asks the identical question `DeriveKernelPolicy` answers ("would the kernel grant this operation"), rather than re-deriving a parallel verdict from config. It is a third consumer of the same value, not a fourth opinion.

**FR-011 — Widening re-resolves the single value.** When an Auto escalation is approved, the widened policy is re-resolved as the single `fspolicy.FSPolicy`, and *both* the app-layer guard (consumer 1) and the kernel ruleset (consumer 2, `DeriveKernelPolicy`) are rebuilt from that widened value *before* the command spawns. The single-source-of-truth rule applies to the widened value, not only the pre-widening one.

**FR-012 — Anti-drift lock test.** A generated matrix of `{path, operation}` pairs evaluated against a fixed `fspolicy.FSPolicy` asserts, for every pair, that the pre-flight verdict and the rights `DeriveKernelPolicy` actually encodes agree. Any divergence fails the test. The test is pure Go (no live enforcement), runs on every CI OS, and its widened-value half covers the post-approval policy too.

**FR-013 — Missed-denial honest-gap behaviour.** A kernel denial the pre-flight missed (symlink resolved at access time, a path constructed at runtime inside the command, a program re-executing under another identity) still fails the command with a clear error. It is never silently allowed and never silently retried unconstrained.

**FR-014 — No-sandbox platforms defer to Ask.** On Windows, the app-level fallback, or a Landlock-degraded Linux host — where no kernel policy is derived from the authored value — Auto defers to Ask rather than guessing. `fspolicy.FSPolicy`/`ResolveTurnFSPolicy` remains platform-independent and still feeds the app-layer guard on those platforms.

**FR-015 — Network egress is outside pre-flight scope.** The pre-flight evaluation covers filesystem policy only. A filesystem-contained `curl evil.com` runs silently in Auto exactly as today; egress filtering is a separate layer, unchanged.

### 3.3 Approval widening

**FR-016 — Path-widening grant shape.** An approved Auto escalation widens the turn's filesystem policy for `{path, operation}` where `operation ∈ {read, write, read+write}` is exactly the access class the pre-flight verdict found the command needs on that path and the current policy denies — nothing broader.

**FR-017 — Session-scoped, recorded.** The widening is recorded as a session-scoped grant — a new record kind in `ApprovalGrantStore` (a `{path, operation}` record alongside the command grant), reusing its session keying, `InheritFrom` delegation, and clear-on-close lifetime. The next identical command in the same session resolves the widened policy directly and does not re-prompt.

### 3.4 Unified rule format and decision order

**FR-018 — One rule shape.** Operator-authored rules are a new `config.SandboxConfig.CommandRules` list (json `command_rules`), each entry `{action: allow|ask|deny, binary, arg_prefix?}`. They are config-file-only and do not cross the gateway/SPA boundary in this ADR (no wire schema).

**FR-019 — Decision order, deny-first, specificity-blind.** Rules resolve in the order deny beats ask beats allow, regardless of rule specificity. A broad `deny` on a binary is not defeated by a narrower `allow` elsewhere.

**FR-020 — Per-segment matching.** Chained commands are split before matching — `&&`, `||`, `;`, `|`, and newlines, using the existing `splitShellSegments`/`shellCommandHead` tokenizer (no new parser). Each resulting segment must independently be allowed, or the whole call asks. An `allow` on `npm test` must not silently allow `npm test && curl evil.com`.

**FR-021 — Resolve-and-bind.** Each segment's leading token is resolved to its actual executable against the child's effective environment (the same layered PATH/env `shell.go` applies to the sandboxed child — not the gateway process's PATH), then `argv[0]` is rewritten to the resolved absolute path so the matched binary is the executed binary. This closes the live PATH/look-alike-binary finding (issue #83 "Additional"). The residual TOCTOU window (swap between resolve and exec) is the kernel sandbox's boundary, not the rule engine's.

**FR-022 — Exec allowlist folded in.** The pre-existing operator exec allowlist (`Evaluator.EvaluateExec`/`MatchGlob`/`FirstToken`, keyed on `security.policy.exec.allowed_binaries`) is retired as a standalone decision path; its configured entries become `allow` rules in the D3 format. It is not run alongside the new engine. `PolicyAuditor.logDecision`'s audit write survives, retargeted to log the D3 engine's verdict.

### 3.5 Approval dialog and suggested prefix

**FR-023 — Three buttons.** The approval dialog shows `[Deny] [Allow once] [Allow]` — replacing today's `Approve`/`Deny`/`Always Allow`/`Cancel`. `Allow once` approves this one call and records no grant; `Allow` approves and records a session-scoped grant. There is no `Cancel` button; Escape, overlay-click, and the X close resolve to `Deny` (default-deny-on-dismiss is unchanged, and Deny remains the default keyboard-focus target).

**FR-024 — Scope choice above the buttons.** A scope radio appears above the three buttons, always visible and enabled only when `Allow` is selected: "(•) exactly this command" (default, pre-selected) or "( ) anything starting with `<suggested prefix>`". Exact scope keeps today's grant exactly — command text + working folder (`cwd`). Prefix scope records a `{binary, arg_prefix}` rule (D3 shape) scoped to the session, and **ignores `cwd`** (it covers the command family in every folder). `run_in_background` stays a match dimension for both scopes — a grant against a foreground command does not auto-approve a detached background run.

**FR-025 — Per-segment grants.** Choosing "Allow" (either scope) on a compound command records one grant per segment. A later call matching some but not all of a previously-granted compound's segments prompts only for the unmatched segment(s).

**FR-026 — Suggested-prefix algorithm.** The suggested prefix is the resolved binary plus its leading sub-command words, stopping at the first token that is a flag (starts `-`), a path-shaped argument, or a URL. A leading environment-variable assignment (`FOO=1 cmd …`) is stripped before deriving the prefix, and the grant matches under any assignment value. A known wrapper (`sudo`, `env`, `timeout`, `xargs`, `sh -c`) is **not** stripped — the prefix is derived from the wrapper itself (`sudo apt install …` → `sudo apt install`), never from what it executes. When the very next token is a URL (no useful prefix), the dialog offers the exact tier only and no prefix radio.

**FR-027 — Per-segment dialog display.** For a chained command, the dialog lists each part separately (as D3's segment split produces), rather than one raw compound string, so the user sees what each segment resolves to before choosing a scope. It shows the command and its working folder as today's `BashApprovalPreview` does.

**FR-028 — No grants list.** Session grants are not shown, listed, or revocable anywhere in the UI. This is an explicit non-feature (not an oversight to fill in later).

### 3.6 God Mode

**FR-029 — God Mode floors the ceiling, deny rules survive.** God Mode floors the global ceiling at `allow` for every tool (today's already-implemented `cfg.GodMode` behaviour), but operator-authored D3 `deny` rules are enforced *inside the shell tool, downstream of policy resolution*, so they survive the floor. A global D3 `deny` + God Mode → the command is still refused. Per-agent overrides continue to survive God Mode under strictest-wins, as today.

**FR-030 — Invariant guards stay on.** The audit log, the prompt-injection guard, and rate limiting remain on in all three modes, including God Mode (they defend against external threats, not agent freedom).

**FR-031 — GodMode comment corrected.** `pkg/config/sandbox.go`'s `GodMode` field doc comment is rewritten (D6) to state exactly what remains true: floors every agent's effective tool policy at `allow` (operator/per-agent D3 `deny` still applies), forces the kernel sandbox off, forces network egress open, and does **not** disable audit logging, the prompt-injection guard, or rate limiting. No behavioural change beyond D1–D3.

### 3.7 Audit and status contract

**FR-032 — Four audit events.** Written by the existing `emitAudit`/`logDecision` path: (a) **mode change** — new mode, the level it changed at (global/per-agent/per-chat), actor; (b) **pre-flight escalation** — that Auto routed a denial to the prompt, naming the rule/path that tripped and the operation requested; (c) **grant recorded** — scope (exact/prefix/path-widening), lifetime (session), resolved binary; (d) **approval decision** — allow-once/allow-with-grant/deny, per call.

**FR-033 — Contract-defined status field.** The chat-header badge's data — the resolved effective mode and whether a kernel sandbox is actually active — comes from a contract-defined status field (Hard Constraint #8), not inferred client-side from separately-fetched config plus platform detection. The badge reflects the resolved effective mode (after the three-level tighten-only merge), not the last level the user touched. When Auto is selected but no kernel sandbox is active, the badge reads "Auto → Ask" with the info tooltip.

**FR-034 — God Mode banner.** A persistent, red, app-wide banner (shown in `AppShell`, not scoped to one chat) appears for as long as God Mode is on, with a `[Turn off]` control that actually clears it.

### 3.8 Removal

**FR-035 — Complete removal, no shims.** Every mechanism ADR-091 supersedes is deleted in the same change set: no dead code, no deprecation shims, no compatibility aliases, no "kept for reference" comments. Old config keys are deleted, not read-and-ignored; an install carrying them is a fresh-install concern (greenfield). Each removal carries a CI guard script that fails the build if the retired symbol reappears.

---

## 4. BDD scenarios

Scenarios are grouped by the FR they trace to. Every scenario carries a `Traces to:` line.

### 4.1 Modes and three-level resolution

**S1 — Auto is the fresh-install default** *(Happy Path)*
- **Given** a fresh install with no operator mode setting, **When** a bash-capable agent resolves its mode, **Then** the effective mode is Auto and `bash` resolves `allow` at the ceiling.
- *Traces to:* FR-001, FR-002.

**S2 — Global Ask tightens all agents** *(Happy Path)*
- **Given** the global default is set to Ask, **When** any agent with no per-agent override runs a shell command, **Then** the command prompts (Ask floor).
- *Traces to:* FR-002, FR-008.

**S3 — Per-agent tighten below global Auto** *(Happy Path)*
- **Given** global default is Auto, **When** an agent's Tools & Permissions sets that agent to Ask, **Then** that agent prompts while a sibling agent with no override stays Auto.
- *Traces to:* FR-002.

**S4 — Per-chat tighten further** *(Happy Path)*
- **Given** global Auto and an agent with no override, **When** the user switches that one chat to Ask via the composer quick switch, **Then** only that chat prompts; a second chat with the same agent stays Auto.
- *Traces to:* FR-002, FR-004, FR-006.

**S5 — Loosening per-agent write rejected at the endpoint** *(Error Path — mandatory adversarial case)*
- **Given** the global default is Ask, **When** a client (API, stale SPA, or an agent) writes a per-agent mode of Auto for some agent, **Then** the write is rejected with a 4xx, the stored value is unchanged, and the agent still resolves Ask. The handler re-read the global default at write time (a client cannot win a race by asserting a stale global).
- *Traces to:* FR-003.

**S6 — Loosening per-chat write rejected the same way** *(Error Path)*
- **Given** global Ask, **When** a client writes a per-chat (session) modifier of Auto, **Then** the write is rejected with a 4xx and the session still resolves Ask.
- *Traces to:* FR-003.

**S7 — UI never offers a looser option** *(Alternate Path)*
- **Given** global Ask, **When** the Tools & Permissions screen and the composer quick switch render their mode selectors, **Then** neither presents Auto or God Mode as a choice (they present only equal-or-stricter options), before any submit.
- *Traces to:* FR-003 (defence-in-depth half).

**S8 — Delegated subagent under a tightened parent chat** *(Error Path — mandatory adversarial case)*
- **Given** a parent chat is tightened to Ask via the session modifier, **When** the parent delegates to a subagent that has no per-agent override (which would otherwise resolve global Auto), **Then** the delegate also resolves Ask — the session modifier inherited via the same mechanism as approval grants, and consent moves in one direction for both.
- *Traces to:* FR-005.

**S9 — Delegate's own override is strictest** *(Alternate Path)*
- **Given** the parent's session modifier is Auto and the delegate carries a per-agent `bash: deny`, **When** the delegate runs a shell command, **Then** it is refused (deny), because the delegate resolves the tightest of (parent modifier, own override).
- *Traces to:* FR-005.

**S10 — Session modifier dies with the session** *(Alternate Path)*
- **Given** a chat is tightened to Ask via the session modifier, **When** the gateway restarts and the chat resumes, **Then** the chat resolves its global/agent default (the modifier did not persist) — a fresh consent context.
- *Traces to:* FR-006.

### 4.2 Platform predicate and Auto→Ask fallback

**S11 — Linux Landlock enforce → Auto** *(Happy Path)*
- **Given** Linux 5.13+ with Landlock applied in enforce mode (not degraded), **When** Auto resolves, **Then** commands run without prompting while the kernel sandbox confines them.
- *Traces to:* FR-007, FR-008.

**S12 — Linux Landlock degraded → Ask** *(Error Path)*
- **Given** Linux where Landlock is unavailable and the sandbox degraded to the app-level fallback, **When** Auto resolves, **Then** it behaves as Ask (every command prompts).
- *Traces to:* FR-007, FR-008, FR-014.

**S13 — macOS Seatbelt active → Auto; Seatbelt off → Ask** *(Happy + Error Path)*
- **Given** macOS, **When** the Seatbelt backend is active, **Then** Auto runs (Seatbelt confines the child). **And** **When** Seatbelt is disabled or unavailable, **Then** Auto behaves as Ask.
- *Traces to:* FR-007, FR-008.

**S14 — Windows → always Ask** *(Happy Path)*
- **Given** Windows (no kernel sandbox backend), **When** Auto resolves, **Then** it behaves as Ask for every command.
- *Traces to:* FR-007, FR-008, FR-014.

### 4.3 Pre-flight evaluation and single source of truth

**S15 — Out-of-containment command asks before running** *(Happy Path)*
- **Given** Auto with an active kernel sandbox, **When** a command names a write target outside the turn's work dir and mounts, **Then** the pre-flight evaluator flags it, an escalation prompt is shown *before* any process spawns, and the command has not started.
- *Traces to:* FR-009, FR-010.

**S16 — Contained command runs with no prompt** *(Happy Path)*
- **Given** Auto with an active kernel sandbox, **When** a command touches only paths `DeriveKernelPolicy` would grant, **Then** it runs with no prompt.
- *Traces to:* FR-009, FR-010.

**S17 — Approval widens, then a second command reuses it** *(Happy Path — mandatory adversarial case)*
- **Given** Auto, a command needs write access to path `P` outside the current policy, **When** the user approves the escalation, **Then** the turn's policy widens by `{P, write}` only, the command runs, and a subsequent identical command in the same session resolves the widened policy and does **not** re-prompt.
- *Traces to:* FR-016, FR-017, FR-011.

**S18 — Widening is operation-exact, not broad** *(Error Path)*
- **Given** the user approved `{P, read}`, **When** a later command in the same session needs `{P, write}`, **Then** it prompts again (the grant was read-only on `P`; write was not granted).
- *Traces to:* FR-016.

**S19 — Widening does not leak past session close** *(Error Path)*
- **Given** a session-wide path widening for `{P, write}` was recorded, **When** the session ends, **Then** a new session does not inherit the widening and prompts for `P` again.
- *Traces to:* FR-017.

**S20 — Kernel denial the pre-flight missed** *(Error Path — mandatory adversarial case)*
- **Given** a command whose literal text is contained but whose actual syscall hits a symlink-to-outside at access time (the lexical-vs-realpath gap), **When** the command runs, **Then** the kernel denies the access and the command fails with a clear error — it is never silently allowed, never silently retried unconstrained, and any partial execution is confined to what ran before the denial.
- *Traces to:* FR-013.

**S21 — Anti-drift lock test catches divergence** *(Error Path)*
- **Given** a deliberately mismatched pair — a pre-flight verdict of "allowed" for a `{path, operation}` that `DeriveKernelPolicy` renders as denied, **When** the lock test runs that pair, **Then** it fails, proving the test can actually catch drift rather than merely pass.
- *Traces to:* FR-012.

### 4.4 Rule format and decision order

**S22 — Deny beats narrower allow** *(Happy Path)*
- **Given** a broad `deny` rule on binary `git` and a narrower `allow` rule on `git status`, **When** `git status` is evaluated, **Then** it is refused (deny-first, specificity-blind).
- *Traces to:* FR-019.

**S23 — Chained command: one part allowed, one not** *(Error Path — mandatory adversarial case)*
- **Given** an `allow` rule for `npm test` and no rule for `curl`, **When** `npm test && curl evil.com` is evaluated, **Then** the whole call asks (the `curl` segment is not allowed, so no silent auto-run of the compound).
- *Traces to:* FR-020.

**S24 — Look-alike binary fails to auto-approve** *(Error Path)*
- **Given** an `allow` rule for `git`, and an attacker-placed executable named `git` earlier on `PATH` than the trusted one (or a symlink over the allowed name), **When** `git evil-script` is evaluated, **Then** the resolved-and-bound binary is the look-alike, and it does **not** match the `allow` rule written against the real `git`.
- *Traces to:* FR-021 (regression against issue #83 "Additional").

**S25 — `ask` rule prompts in every mode** *(Happy Path)*
- **Given** an operator `ask` rule on `git push`, **When** `git push` is evaluated in Auto or God Mode, **Then** it prompts (an `ask` rule is not overridden by a permissive mode floor).
- *Traces to:* FR-019.

**S26 — `deny` rule refuses in God Mode** *(Error Path — mandatory adversarial case)*
- **Given** God Mode is on and a global D3 `deny` rule names binary `rm`, **When** `rm -rf /` is evaluated, **Then** it is refused — proving the D3 engine runs downstream of the policy floor and God Mode does not erase it.
- *Traces to:* FR-029.

**S27 — Session grants do not suppress Ask's floor** *(Error Path)*
- **Given** Ask mode and a session grant recorded for `npm test`, **When** the same `npm test` runs again, **Then** it still prompts (grants and `allow` rules auto-approve only in Auto/God Mode; Ask's floor is "always ask").
- *Traces to:* FR-019, FR-002.

### 4.5 Approval dialog and suggested prefix

**S28 — Allow once records no grant** *(Happy Path)*
- **Given** a pending approval, **When** the user clicks `Allow once`, **Then** this call runs and no grant is recorded; a second identical call re-prompts.
- *Traces to:* FR-023.

**S29 — Allow + exact behaves as today** *(Happy Path)*
- **Given** the exact scope is selected (default), **When** the user clicks `Allow` on `npm run test -- --watch` in folder `A`, **Then** a grant for exactly that command + `cwd` is recorded; `npm run test -- --coverage` (different args) still prompts.
- *Traces to:* FR-024.

**S30 — Allow + prefix matches the family** *(Happy Path)*
- **Given** the prefix scope is selected with suggested prefix `npm run test`, **When** the user clicks `Allow`, **Then** a `{binary: npm, arg_prefix: run test}` session grant is recorded; `npm run test -- --coverage` in a *different* folder does not re-prompt, while `npm install` (outside the prefix) does.
- *Traces to:* FR-024, FR-026.

**S31 — Background is a separate grant** *(Error Path)*
- **Given** a foreground grant recorded for `npm test`, **When** a `run_in_background` variant of the same command runs, **Then** it prompts again (the grant did not cover the background run).
- *Traces to:* FR-024.

**S32 — Compound grant is one rule per part; partial match prompts only the rest** *(Happy Path)*
- **Given** the user granted `Allow` on `npm test && curl https://a.com`, **When** a later call runs `npm test && curl https://b.com`, **Then** only the `curl` segment prompts (the `npm test` segment is already granted).
- *Traces to:* FR-025.

**S33 — Wrapper: no prefix through the wrapper** *(Error Path — mandatory adversarial case)*
- **Given** a command `sudo apt install foo`, **When** the dialog derives the suggested prefix, **Then** it is `sudo apt install` (derived from the wrapper, not from `apt install`), so a grant on it cannot silently cover `sudo rm -rf /`.
- *Traces to:* FR-026.

**S34 — URL argument yields exact-only** *(Alternate Path)*
- **Given** a command `curl https://x.com`, **When** the dialog derives the suggested prefix, **Then** the next token is a URL, so no prefix is offered — only the exact tier and no prefix radio.
- *Traces to:* FR-026.

**S35 — Leading env-var assignment stripped** *(Alternate Path)*
- **Given** `FOO=1 npm run test`, **When** the prefix is derived, **Then** the assignment is stripped (`npm run test`) and the resulting grant matches under any assignment value, not only `FOO=1`.
- *Traces to:* FR-026.

**S36 — Escape / overlay / X resolve to Deny** *(Alternate Path)*
- **Given** a pending approval, **When** the user presses Escape, clicks the overlay, or clicks X, **Then** the call is denied (default-deny-on-dismiss), and Deny remains the default keyboard-focus target. There is no Cancel button.
- *Traces to:* FR-023.

**S37 — Chained command lists each part** *(Happy Path)*
- **Given** a compound command pending approval, **When** the dialog renders, **Then** it lists each segment separately (not one raw compound string), each with its resolved form, before the scope choice.
- *Traces to:* FR-027.

### 4.6 Audit and status

**S38 — Mode change is audited with level + actor** *(Happy Path)*
- **Given** an operator changes the global mode to Ask, **When** the change lands, **Then** an audit event records the new mode, the level (global), and the actor.
- *Traces to:* FR-032.

**S39 — Pre-flight escalation is audited** *(Happy Path)*
- **Given** Auto routes a denial to the prompt, **When** the escalation fires, **Then** an audit event records the rule/path that tripped and the operation requested.
- *Traces to:* FR-032.

**S40 — Badge reflects resolved mode, not last-touched level** *(Happy Path)*
- **Given** global Auto, an agent override Auto, and a chat tightened to Ask, **When** the chat header renders, **Then** the badge reads Ask (the resolved effective mode), not the agent/global value the user last edited.
- *Traces to:* FR-033.

**S41 — Badge reads "Auto → Ask" only when Auto-without-sandbox** *(Alternate Path)*
- **Given** Auto is the resolved mode, **When** the platform predicate says no kernel sandbox is active, **Then** the badge reads "Auto → Ask" with the info tooltip — and not when a kernel sandbox is active.
- *Traces to:* FR-033, FR-007.

**S42 — God Mode banner appears app-wide and Turn off clears it** *(Happy Path)*
- **Given** God Mode turns on, **When** any screen renders, **Then** the red banner appears in `AppShell` (app-wide, not one chat), and its `[Turn off]` control actually clears it.
- *Traces to:* FR-034.

---

## 5. Test data sets

### 5.1 Suggested-prefix algorithm — boundary and edge cases

| # | Command | Expected prefix | Notes | Traces to |
|---|---|---|---|---|
| P1 | `npm run test -- --watch` | `npm run test` | stop at first `-`-flag token | S30, FR-026 |
| P2 | `git diff src/` | `git diff` | stop at path-shaped arg | FR-026 |
| P3 | `curl https://x.com` | *(none — exact only)* | next token is a URL | S34, FR-026 |
| P4 | `FOO=1 cmd --flag` | `cmd` | env-var assignment stripped | S35, FR-026 |
| P5 | `FOO=1 BAR=2 cmd` | `cmd` | multiple assignments stripped | FR-026 |
| P6 | `sudo apt install foo` | `sudo apt install` | wrapper not stripped | S33, FR-026 |
| P7 | `env X=1 cmd` | `env X=1 cmd`? | `env` is a wrapper → prefix from wrapper | FR-026 |
| P8 | `sh -c 'rm -rf X'` | `sh` (or `sh -c`) | wrapper; never `rm` | FR-026 |
| P9 | `xargs rm` | `xargs` | wrapper; never `rm` | FR-026 |
| P10 | `timeout 5 npm test` | `timeout 5 npm test`? | wrapper not stripped | FR-026 |
| P11 | `git -C /repo log` | `git` | stop at first flag `-C` | FR-026 |
| P12 | `git log --oneline` | `git log` | stop at flag | FR-026 |
| P13 | `echo "a b" && npm test` | *(per segment)* `echo` / `npm test` | split then derive per segment | FR-020, FR-026 |
| P14 | `VAR=value echo hi` | `echo hi`? or `echo` | assignment stripped, then stop at… `hi` is not flag/path/URL → `echo hi` | FR-026 |

> **Decision needed (flagged, not invented):** P7/P10 — whether `env X=1` / `timeout 5` are "leading words" retained in the prefix, or the prefix stops at the wrapper's first argument, is not pinned by the ADR. The ADR fixes only that the prefix starts at the wrapper (`env`/`timeout`) and never reaches through it. The spec states the question; implementation must pick the exact stop token and pin it with a test.

### 5.2 Path widening — boundary and edge cases

| # | Candidate | Expected verdict | Traces to |
|---|---|---|---|
| W1 | Write to path under the work dir | contained — no prompt | S16, FR-009 |
| W2 | Write to a mounted host folder | contained (mount = write grant) — no prompt | FR-009 |
| W3 | Write to an unmounted absolute path outside work dir | escalation prompt `{P, write}` | S15, FR-009 |
| W4 | Read of an outside path (open-read posture) | no prompt (reads are open minus secret set) | FR-009, ADR-063 D2 |
| W5 | Symlink inside work dir pointing outside, used for a write | escalation (realpath resolves outside) | FR-013, ADR-068 §9 |
| W6 | Relative path with `..` escaping the work dir | escalation, judged on realpath | FR-009 |
| W7 | Path under another agent's workspace (`workspaces/<other>/`) | denied/escalation (secret set / carve-out) | FR-009, ADR-063 D3 |
| W8 | Path under another agent's home (`agents/<other>/SOUL.md`) | denied (carve-out, per-turn root) | FR-009 |
| W9 | `master.key` / `credentials.json` / `config.json` / `cli.token` | always denied (secret set) — never widened by approval | FR-009, ADR-063 D3 |
| W10 | `/tmp` (shared) write | denied by kernel (narrowed shared-tmp write); escalation | FR-009, `derive_from_fspolicy.go::narrowSharedTmpWrite` |
| W11 | per-user `$TMPDIR` write | granted (scratch space) | FR-009 |
| W12 | Symlink swapped after pre-flight, before exec (TOCTOU) | kernel denies at runtime → clear error | S20, FR-013 |

### 5.3 Rule matching — boundary and edge cases

| # | Input | Expected | Traces to |
|---|---|---|---|
| R1 | `git push` with `deny: git` | refuse | S22, FR-019 |
| R2 | `git status` with `deny: git` + `allow: git status` | refuse (deny-first) | S22, FR-019 |
| R3 | `npm test && curl evil.com` with `allow: npm test` | whole call asks | S23, FR-020 |
| R4 | `echo a; echo b` | split into two segments on `;` | FR-020 |
| R5 | `echo a | grep b` | split on `\|` | FR-020 |
| R6 | newline-separated commands | split on newline | FR-020 |
| R7 | `git` resolved to a PATH-shadowing look-alike | fails to match real-`git` allow | S24, FR-021 |
| R8 | `/bin/rm` vs `rm` spelling | both resolve-and-bind to the same binary | FR-021 |
| R9 | `X=rm; $X -rf /` | not matched as `rm` by the block list (which is deleted); governed by kernel, not text | FR-035 (block list gone) |
| R10 | `rm${IFS}-rf /` | same as R9 — text bypass no longer a security claim | FR-035 |
| R11 | `$(...)` / backtick substitution | substitution guard (surviving) judges structure | ADR-091 D2 (guard survives) |

---

## 6. Contract-first work (Hard Constraint #8)

Wire types are generated from `contracts/openapi.yaml` (REST) and `contracts/asyncapi.yaml` (WS) plus `contracts/components/schemas/`. Generated artifacts — committed, never hand-edited — are `pkg/api/generated/` (Go) and `src/lib/api/generated/` (TS + Zod). Process: `make gen-contracts` / `scripts/gen-contracts.sh`, then `make verify-contracts` (fails on drift).

### 6.1 New / changed wire types — the 5-step order

For each new or changed wire type: **(1)** add/edit the schema file, **(2)** reference it from `openapi.yaml` and/or `asyncapi.yaml`, **(3)** run `scripts/gen-contracts.sh`, **(4)** commit the generated diff with the spec change (one atomic commit), **(5)** write the handler/consumer against the generated type only.

**New schemas (all under `contracts/components/schemas/`):**

| Schema file | Purpose | Carried by |
|---|---|---|
| `ShellPermissionMode.yaml` | The mode enum (`ask`/`auto`/`god`) — one source for every level's write shape and the status field | referenced by the three write shapes + `ShellModeStatus` |
| `ShellModeStatus.yaml` | The badge's data: `effective_mode` (resolved, post three-level merge) + `kernel_sandbox_active` (the D1 predicate, server-derived). This is the Hard-Constraint-#8 field — the SPA must read it, not re-derive it | GET on a security/status endpoint (see 6.2); consumed by the chat-header badge and the God Mode banner |
| `ShellModeSelector.yaml` (or fold per-level into the existing update schemas) | The mode-selector write shape at the global/per-agent level (`mode: ask\|auto\|god`, with God Mode global-only) | REST write endpoints (see 6.2) |
| (per-chat modifier) `ShellSessionMode.yaml` | The session-scoped modifier write (`mode: ask\|auto`, never `god`) | the composer quick switch's write path (WS frame or session REST — see 6.2 decision) |

**Changed schemas:**

| Schema file | Change |
|---|---|
| `ToolApprovalActionRequest.yaml` | `action` enum becomes `deny \| allow_once \| allow` (drop `approve`/`always`/`cancel`); add a `scope` field (`exact` \| `prefix`) present when `action == allow`, defaulting to `exact` |
| `ToolApprovalResponse.yaml` | echo the new `action` enum + `scope`; keep `grant_recorded` semantics for `allow` |
| `ToolApprovalRequiredFrame.yaml` | add the per-segment command list (each segment: resolved binary, args, path/URL/flag classification) and the server-derived `suggested_prefix` (or a flag that no prefix is offerable), so the dialog renders D3's segment split and D4's scope radio from one wire value |
| `SandboxConfig.yaml` | remove the `shell_deny_patterns` property |
| `SandboxConfigUpdate.yaml` | remove the `shell_deny_patterns` reference |
| `AgentUpdateRequest.yaml` | remove the `shell_policy` reference |
| `AgentShellPolicy.yaml` | **deleted in full** (only `enable_deny_patterns`/`custom_deny_patterns` — no other properties) |
| `ExecAllowlist.yaml` | **deleted in full** |

**`openapi.yaml` / `asyncapi.yaml` edits:**

- Remove the `/security/exec-allowlist` path from `openapi.yaml` (its REST surface is deleted with the exec allowlist — Removal inventory item 3).
- Add the mode-status endpoint reference (or fold into an existing status endpoint — decision below) and the mode-selector write endpoints.
- Any WS frame carrying the per-chat modifier or the mode status must be declared in `asyncapi.yaml` (frame type + Zod schema), generated, never hand-written.

### 6.2 Endpoints / frames — decisions the ADR does not pin

The ADR mandates *that* the badge data and the mode writes cross the boundary as contract-defined fields, but does not fix *which* endpoint/frame carries each. The spec states the decisions needed rather than inventing them:

1. **Mode-status read** — the badge needs `{effective_mode, kernel_sandbox_active}`. Candidate: a new `GET /api/v1/security/shell-mode-status` returning `ShellModeStatus`, or fold the two fields into the existing security-status/`SandboxStatus` response. **Decision needed:** which endpoint; prefer reuse of an existing status shape where the field is already server-derived.
2. **Per-chat modifier write** — session-scoped, dies with the session. Candidate: a WS frame (declared in `asyncapi.yaml`) or a session-scoped REST call. **Decision needed:** which; must be session-keyed, never written into `config.json` (FR-004).

**Config-only, no wire schema (explicit non-requirement):** the D3 `command_rules` list (FR-018) is config-file-only and does not cross the boundary, so it needs no schema. If the SPA ever displays or edits rules, that is future work and a schema comes first.

---

## 7. Removal work (first-class tasks)

Every item below is a task with its own acceptance check. Greenfield: no migration, no deprecation shim, no read-and-ignore. Old keys are **deleted**, not tolerated-then-ignored; an install carrying them is a fresh-install concern.

### 7.1 Remove the built-in block list and deny-pattern config (D2)

**R-1 — Backend block list.** Delete `defaultDenyPatterns` (the ~38-regex slice, which already includes `secretGuardPatterns`), `buildSecretGuardPatterns`, `applyDenyPatterns`, `compileDenyPatterns`, `denyPatternMessage`. Move `lowerASCII` into `pkg/tools/shell_subst_guard.go` (it has three surviving callers there). Delete `pkg/tools/shell_secret_guard_test.go` (tests only the now-removed `secretGuardPatterns`); keep `shell_readwrite_guard_test.go`/`shell_mount_guard_test.go`.
- **Files:** `pkg/tools/shell.go`, `pkg/tools/shell_guard.go`, `pkg/tools/shell_subst_guard.go`, `pkg/tools/shell_path_guard.go`.
- **Acceptance check:** `grep -rn 'defaultDenyPatterns\|secretGuardPatterns\|applyDenyPatterns\|compileDenyPatterns\|denyPatternMessage' pkg/` returns only `lowerASCII`-adjacent comments or nothing; surviving guards' test suites still pass.
- **Guard:** `scripts/check-no-shell-deny-patterns.sh`.

**R-2 — Config fields.** Delete `config.SandboxConfig.ShellDenyPatterns` (json `shell_deny_patterns`), `config.AgentShellPolicy.CustomDenyPatterns`/`EnableDenyPatterns` (json `custom_deny_patterns`/`enable_deny_patterns`), and the `AgentShellPolicy` type itself (its only two properties are the two removed fields).
- **Files:** `pkg/config/sandbox.go`, `pkg/config/config.go`.
- **Acceptance check:** a `release/v0.1.1`-shaped config carrying the removed keys loads cleanly (unknown-field tolerance, file half) with no error and no silent promise the old patterns still apply; the agent REST write path (`rest_agents_create.go`, `DisallowUnknownFields`) rejects a stale `shell_policy` with a hard 400 (strict half).
- **Guard:** `scripts/check-no-shell-deny-patterns.sh`.

**R-3 — Gateway validation.** Delete `validateShellDenyPatterns` (`pkg/gateway/sandbox_config_validation.go`) and its caller in `pkg/gateway/rest_sandbox_config.go`.
- **Acceptance check:** no remaining reference to the symbol; the sandbox-config write path no longer validates a field that no longer exists.
- **Guard:** `scripts/check-no-shell-deny-patterns.sh`.

**R-4 — Wiring.** Remove `ExecToolDeps.GlobalShellDenyPatterns`/`AgentShellPolicy` and their use in `NewExecToolWithConfig`; remove `pkg/agent/loop_wire.go`'s `agentShellPolicy`/`globalShellDenyPatterns` construction including the `agentShellPolicy = nil // drop per-agent deny patterns under god mode` carve-out (moot once the mechanism it carves out of is gone).
- **Files:** `pkg/tools/shell.go`, `pkg/agent/loop_wire.go`.
- **Acceptance check:** `grep -rn 'AgentShellPolicy\|GlobalShellDenyPatterns' pkg/` returns nothing but comments.
- **Guard:** `scripts/check-no-shell-deny-patterns.sh`.

**R-5 — Contracts.** Delete `AgentShellPolicy.yaml`; remove `SandboxConfig.yaml`'s `shell_deny_patterns` property and the `SandboxConfigUpdate.yaml`/`AgentUpdateRequest.yaml` references; regenerate.
- **Acceptance check:** `make verify-contracts` clean; generated diff committed with the schema edit.
- **Guard:** `scripts/check-no-shell-deny-patterns.sh` (covers `contracts/` too).

**R-6 — SPA.** Delete `src/components/agents/ShellDenyPatternsEditor.tsx` (+ `.test.tsx`); remove references from `CreateAgentWizard.tsx`, `wizard/Advanced.tsx`, `wizard/types.ts`, `agentDraft.ts` (+ `.test.ts`), `AgentProfile.tsx` (+ `.test.tsx`), `CreateAgentModal.test.tsx`; remove `globalDenyPatterns`/`extractShellDenyPatterns` state + the `ShellDenyPatternsEditor` import from `src/components/settings/SandboxSection.tsx` (+ `.test.tsx`); remove the `enableDenyPatterns` state and deny-pattern portion from `src/components/settings/SecuritySection.tsx` (+ `.test.tsx`) — the mode selector is added to this same file, but the deny-pattern section is deleted. `src/lib/api/agents.ts`/`config.ts`/`security.ts` lose references (surfaced as compile errors after regeneration — the correct signal).
- **Acceptance check:** `npx vitest run src/components/agents src/components/settings` green; `npx tsc -b --noEmit` (via `npm run typecheck`) clean after regeneration.
- **Guard:** `scripts/check-no-shell-deny-patterns.sh` (covers `src/`).

### 7.2 Delete the dead "always allow" manager (D5)

**R-7 — `pkg/security/execapproval.go` + `execapproval_test.go`.** Delete `ExecApprovalManager`, `NewExecApprovalManager`, `CheckApproval`, `PersistPattern`, `matchAllowlistPattern`, `MatchExecAllowlist`, `WithAllowlistFile` — definition and tests, no shim, no retirement comment beyond what CI guards require. Zero production callers (grep-verified).
- **Acceptance check:** the guard script is run once by hand against the pre-deletion tree (`838d8092c`) to confirm it *actually fires*, not just passes on the post-deletion tree.
- **Guard:** `scripts/check-no-exec-approval-manager.sh`.

### 7.3 Fold the exec allowlist into D3 (Removal inventory item 3)

**R-8 — `pkg/policy/evaluator.go` deleted in full** (`NewEvaluator`, `Evaluator.EvaluateExec`, `MatchGlob`, `FirstToken`); `PolicyAuditor.EvaluateExec` retired; `PolicyAuditor.logDecision` retargeted to log the D3 engine's verdict.
- **Files:** `pkg/policy/evaluator.go` (delete), `pkg/policy/auditor.go`, `pkg/agent/loop_construct.go` (drop `policy.NewEvaluator` wiring).
- **Acceptance check:** `grep -rn 'EvaluateExec\|MatchGlob\|FirstToken' pkg/` returns only `logDecision` (retargeted) and nothing else; the audit trail still logs a decision per exec call.
- **Guard:** the third check (fold into either guard or a new script).

**R-9 — `ExecConfig.AllowedBinaries` + `ExecAllowlist.yaml`.** Retire the config field (json `allowed_binaries`, env `OMNIPUS_TOOLS_EXEC_ALLOWED_BINARIES`) and the schema; no auto-conversion of existing entries to D3 rules (operator re-authors them).
- **Files:** `pkg/config/config.go`, `contracts/components/schemas/ExecAllowlist.yaml` (delete).
- **Guard:** the third check.

**R-10 — Exec allowlist REST + SPA surfaces.** Delete `pkg/gateway/rest_exec.go::HandleExecAllowlist` + `sanitiseAllowlist`, the `/api/v1/security/exec-allowlist` route in `pkg/gateway/rest.go`, the `/security/exec-allowlist` path in `contracts/openapi.yaml`, `src/components/settings/ExecAllowlistSection.tsx` (+ `.test.tsx`), its render in `SecuritySection.tsx`, and `fetchExecAllowlist`/`updateExecAllowlist` in `src/lib/api/security.ts`.
- **Acceptance check:** `grep -rn 'exec-allowlist\|ExecAllowlistSection\|fetchExecAllowlist'` returns nothing (comments excepted).
- **Guard:** the third check (including the literal path `exec-allowlist`).

### 7.4 Resolve the adjacent `ExecConfig.Approval` surface (Open Question 1)

**R-11 — Investigate, then act.** `config.ExecConfig.Approval` (`tools.exec.approval`, `"ask"`/`"off"`) and the untyped `SecuritySection` keys `config.security.policy_mode` / `config.security.exec_approval` are reachable-but-unenforced (per the ADR's grep: three references, none load-bearing). **Acceptance check:** confirm both are dead before deleting them alongside R-8, or fold them into D1's mode resolution if live — the spec does not assume which; the finding is recorded, not guessed.

### 7.5 Stale docs and CLAUDE.md

**R-12 — `GodMode` comment (D6).** Rewrite `pkg/config/sandbox.go`'s `GodMode` doc comment per FR-031.
- **Acceptance check:** the comment states the floor-at-allow + D3-deny-survives + sandbox/egress-off + audit/injection/rate-limiting-on posture; no behavioural change.

**R-13 — Docs triage.** `docs/` files that name the retired mechanisms (the ADR lists 13, e.g. `bash-tool-spec-review.md`, `policy-change-approval-spec.md`, `tool-consolidation-spec.md`, ADR-025's review) each get a pass to mark the reference historical or update it. **Acceptance check:** a decision recorded per file (historical vs updated); the spec does not assume which for each.

**R-14 — CLAUDE.md "Retired surfaces" + guard wiring.** Add both entries to the root `CLAUDE.md` retired-surfaces list; add `scripts/check-no-shell-deny-patterns.sh` and `scripts/check-no-exec-approval-manager.sh` (plus the third exec-allowlist check) to `scripts/guards.sh`; wire into `.github/workflows/pr.yml`, the CI worker `lint` gate, and `make lint-guards`.
- **Acceptance check:** `make lint-guards` runs the new guards and they pass on the post-removal tree and fail on a deliberately reintroduced symbol (self-check, per `guards.test.sh` convention).

---

## 8. Parallel delivery plan

### 8.1 Dependency graph

```
L0 Contracts ────────────────┐
   │                         │ (blocks every lane that consumes generated types)
   ├──> L1 Rule engine (D3) ─┼──> L5 (config/gateway/policy removal — evaluator delete waits on L1)
   │                         │
   ├──> L2 Mode resolution ──┼──> L4 (shell core — reads effective mode from L2)
   │                         │
   ├──> L3 Pre-flight (D7) ──┼──> L4 (shell core — wires the pre-flight hook + anti-drift test from L3)
   │                         │
   └──> L6 SPA ──────────────┘   (renders against L0 generated types; dialog wires to L4's grant store once L4 lands)
                              L7 CI guards + CLAUDE.md + docs triage  (runs last; verifies L4/L5 removals)
```

Ordering constraints, stated once:
- **Contracts first** — L0 is a hard blocker for L5, L6, and any lane touching generated types.
- **Single-source plumbing before the pre-flight evaluator** — L3 depends on nothing new (it reuses `ResolveTurnFSPolicy`/`DeriveKernelPolicy`, both unchanged), but its anti-drift test is what makes D7's "one source of truth" claim enforced; L4 wires it into `executeRun`.
- **Pre-flight before deny-pattern removal** — L4 does both in sequence within its own worktree (ADR step 6: Auto's pre-flight is the compensating control before the text-pattern layer disappears).
- **D3 engine before evaluator deletion** — L5's `evaluator.go` deletion waits on L1 (the engine that replaces its decision logic).
- **Grant-store extension before dialog wiring** — L6's `Allow`/prefix flow consumes L4's `ApprovalGrantStore` record kinds; the dialog's server contract (L0) can proceed independently of that wiring.

### 8.2 File ownership (no two lanes edit the same file)

The three contested backend files are assigned to exactly one lane each; every other lane produces new files or owns disjoint files.

| File(s) | Sole owner lane |
|---|---|
| `contracts/**`, `pkg/api/generated/**`, `src/lib/api/generated/**` | L0 |
| `pkg/shellrule/**` (new package: rule type, resolver, segment/binary matching) + its tests | L1 |
| `pkg/agent/loop_policy.go`, `pkg/agent/sessionmode.go` (new) + tests | L2 |
| `pkg/tools/preflight.go` (new), `pkg/sandbox/derive_from_fspolicy_antidrift_test.go` (new, or in `pkg/tools`) | L3 |
| `pkg/tools/shell.go`, `pkg/tools/shell_guard.go`, `pkg/tools/shell_path_guard.go`, `pkg/tools/shell_subst_guard.go`; delete `pkg/tools/shell_secret_guard_test.go` | L4 |
| `pkg/config/sandbox.go`, `pkg/config/config.go`, `pkg/agent/loop_wire.go`, `pkg/agent/loop_construct.go`, `pkg/policy/evaluator.go` (delete), `pkg/policy/auditor.go`, `pkg/security/execapproval.go`+`execapproval_test.go` (delete), `pkg/gateway/sandbox_config_validation.go`, `pkg/gateway/rest_sandbox_config.go`, `pkg/gateway/rest_exec.go`, `pkg/gateway/rest.go` | L5 |
| `src/**` (dialog, preview, SecuritySection, ToolsAndPermissions, composer, chat header, AppShell, deletions, `src/lib/api/*`) | L6 |
| `scripts/check-no-shell-deny-patterns.sh`, `scripts/check-no-exec-approval-manager.sh`, third check, `scripts/guards.sh`, root `CLAUDE.md`, `docs/**` triage | L7 |

Note: `pkg/config/config.go` and `pkg/tools/shell.go` appear in multiple ADR steps but are **each owned by a single lane** here (L5 and L4 respectively). The D3 `command_rules` config field is added by L5 (which owns `config.go`), consuming L1's type — an explicit serialisation point, not a shared edit.

### 8.3 Lanes

| Lane | Agent | Files | Tests | Definition of done | Review gate |
|---|---|---|---|---|---|
| **L0** | backend-lead | contracts (see §6.1, 7.1 R-5, 7.3 R-9/R-10) + generated | `make gen-contracts` idempotent; `make verify-contracts` clean | Generated diff committed with the spec edit; every new/changed type referenced and generated | `/code-review high` + Constraint #8 self-check (`scripts/check-no-handwritten-wire-types.sh` clean) |
| **L1** | backend-lead | `pkg/shellrule/**` (new) | unit tests for segment split, binary resolve-and-bind, deny-first order; the issue-#83 look-alike regression | Engine resolves rules per D3; look-alike binary fails to auto-approve | `/grill-code` |
| **L2** | backend-lead | `pkg/agent/loop_policy.go`, `pkg/agent/sessionmode.go` | three-level merge, tighten-only, delegation inheritance, session lifetime | Modes resolve per D1; modifier inherits to delegates; no `chat_id` key in the two-layer maps | `/grill-code` |
| **L3** | security-lead | `pkg/tools/preflight.go`, anti-drift lock test | `{path,op}` matrix vs `DeriveKernelPolicy`; widened-value half; deliberate-mismatch self-check | Pre-flight verdict and kernel rendering agree for every matrix pair; the test fails on a seeded mismatch | `/grill-code` |
| **L4** | security-lead | `pkg/tools/shell*.go` (single owner) | surviving-guard suites + new mode-sensitive denial tests + pre-flight hook wiring | D2 block-list gone; pre-flight fires before spawn; path guard's denial disposition is mode-sensitive; God Mode floors + D3 deny survives | `/grill-code` |
| **L5** | backend-lead | config + gateway + policy removal (see table) | config-load tolerance test; no-remaining-reference checks | All superseded config/wire surfaces deleted; evaluator + exec-approval + exec-allowlist gone; `logDecision` retargeted | `/grill-code` |
| **L6** | frontend-lead | `src/**` | vitest for dialog/scope/prefix; typecheck; design-system locks | Dialog is `[Deny][Allow once][Allow]` + scope radio; prefix algorithm per D4; badge/banner render per D1/D6; deny-pattern + allowlist editors deleted | design-system stage gates use `/code-review high` (per repo memory); `omnipus-design-system` skill loaded first |
| **L7** | qa-lead + backend-lead | guard scripts, `guards.sh`, `CLAUDE.md`, docs triage | guard self-tests (`*.test.sh` convention); `make lint-guards` | New guards wired and self-checking; retired-surfaces clause added; docs triage decisions recorded | `/code-review high` on CLAUDE.md/guard changes |

### 8.4 Serialisation points (lanes wait here)

1. **SP-1:** all lanes wait on **L0** (generated types are the cross-boundary contract).
2. **SP-2:** **L4** waits on **L2** (read the effective mode) and **L3** (call the pre-flight + anti-drift test exists before wiring it).
3. **SP-3:** **L5** waits on **L1** before deleting `evaluator.go` (the engine must exist to replace its decision logic); L5 also waits on **L4** so the pre-flight compensating control is in place before the deny-pattern config surface is removed in the same change set.
4. **SP-4:** **L6** renders against **L0** types immediately (parallel), but wires `Allow`/prefix grants only after **L4** lands the `ApprovalGrantStore` record kinds.
5. **SP-5:** **L7** runs last; its guards must be verified against the **L4/L5** post-removal tree, and once by hand against the pre-deletion tree (`838d8092c`) to confirm each guard actually fires.

### 8.5 Suggested lane count and briefs

**Eight lanes (L0–L7).** Rationale: contracts, the additive rule engine, the pre-flight evaluator, and the SPA are naturally independent and parallel; the two contested files (`shell.go`, `config.go`) are collapsed into single-owner lanes (L4, L5) rather than split, because overlapping files are this repo's main failure mode in parallel worktrees.

Each lane brief must include: the ADR decisions it implements (D1/D2/D3/D4/D5/D6/D7 as relevant), its exact file list from §8.2, its DoD and review gate from §8.3, the serialisation points it obeys (§8.4), and the instruction to cite `file::symbol` (never line numbers) and to author commits as the human (no `Co-Authored-By: ...@anthropic.com` — repo CLAUDE.md). L3 and L4 are `security-lead` (kernel-adjacent logic); L6 is `frontend-lead` and must load the `omnipus-design-system` skill before touching `src/components/`.

---

## 9. Acceptance criteria (SC-xxx)

**SC-001 — CI green.** Every CI gate passes: Go test/build (with `goolm,stdjson`, `CGO_ENABLED=0`), lint, vuln, race, vitest, `tsc -b`, Playwright, and `make lint-budgets` / `make lint-guards` — with no "pre-existing"/"not mine" closure paths (Hard Constraint #7).

**SC-002 — Anti-drift lock test passes and is proven live.** The `{path, operation}` matrix test passes on every CI OS, its widened-value half passes, and a deliberately mismatched pair fails it (proving the test detects drift, not merely runs).

**SC-003 — Guard scripts fail on reintroduction.** Each of the two (three) new guards fails the build when a retired symbol is reintroduced as a definition or non-comment reference; each is verified once by hand against the pre-deletion tree, not only on the post-deletion tree.

**SC-004 — Auto widening demonstrated end-to-end.** A real (or UAT) run shows: Auto + an out-of-containment command → escalation prompt → approve → command runs confined to the widened `{path, operation}` → the next identical command in the session does not re-prompt, and the widening does not leak past session close.

**SC-005 — Auto→Ask fallback demonstrated end-to-end.** On a host with no active kernel sandbox (Windows, app-level fallback, or Landlock-degraded), Auto behaves as Ask: every command prompts, and the badge reads "Auto → Ask" with the tooltip.

**SC-006 — Mode merge correctness.** The three-level tighten-only merge resolves per D1: a loosening write returns 4xx; a per-agent tighten and a per-chat tighten apply at their level and do not leak; delegation inherits the session modifier to the tightest.

**SC-007 — Removal completeness.** `grep` over `pkg/`, `cmd/`, `src/`, `contracts/` finds no surviving definition or non-comment reference to any retired symbol from the Removal inventory (§7); a `release/v0.1.1`-shaped config loads cleanly with the retired keys dropped.

**SC-008 — D3 rule engine correctness.** Deny-beats-allow (specificity-blind) holds; chained commands split per segment and a partially-allowed compound asks; the issue-#83 look-alike-binary case fails to auto-approve; the exec allowlist's decision logic is gone and its audit write is retargeted.

**SC-009 — Dialog and prefix correctness.** `Allow once` records no grant; `Allow`+exact behaves as today; `Allow`+prefix matches the family and respects `run_in_background` as a separate dimension; wrapper commands yield a wrapper-derived prefix; URL-headed commands offer exact-only; Escape/overlay/X deny.

**SC-010 — Invariance.** The audit log, prompt-injection guard, and rate limiting fire identically across all three modes including God Mode (regression against the as-is finding that they are not gated on `godMode`).

**SC-011 — Status field is contract-derived.** The badge's effective mode and kernel-sandbox-active flag come from the contract-defined status field (server-derived), not from client-side platform detection; the God Mode banner is app-wide and its `[Turn off]` clears it.

---

## 10. Open questions (decision needed — not invented)

1. **Per-turn single-value threading (ADR-091 OQ2).** Today `guardCommand` and `turnKernelPolicy` each call `ResolveTurnFSPolicy` independently; D7 adds a third call site (pre-flight). Under normal conditions all three resolve identically, but nothing structurally guarantees one `fspolicy.FSPolicy` instance backs the guard, the kernel policy, and the pre-flight verdict for one turn if state changes between calls. **Decision needed:** per-turn cached resolution (one call reused by all three) vs provably-safe independent calls.
2. **`ExecConfig.Approval` reachability (ADR-091 OQ1).** Whether `tools.exec.approval` and the untyped `policy_mode`/`exec_approval` keys are dead (delete with §7.3) or live (fold into D1) is unresolved in the ADR's evidence pass; §7.4 makes it an explicit investigation task.
3. **Suggested-prefix stop token for wrapper-leading words** (P7/P10 in §5.1): whether `env X=1 cmd`/`timeout 5 npm test` retain their leading words or stop at the wrapper's first argument is not pinned; the spec flags it for implementation to decide and pin with a test.
4. **Mode-status endpoint and per-chat modifier write path** (§6.2): which endpoint/frame carries each is not fixed by the ADR; reuse of an existing status shape is preferred where the field is already server-derived.

---

## 11. Out of scope / future work

- **Per-website network approvals** — a "always allow this domain" option for network tools is explicitly future work (founder's own note), not built here.
- **Session-grants management surface** — no list, chip, or revoke UI; grants are invisible and end with the chat (FR-028).
- **Headless-turn special-casing** — a headless Ask turn stalls to `ToolApprovalTimeout` exactly as today; no new handling (operators set the global default accordingly).
- **Network egress changes** — egress filtering posture is unchanged; Auto selects filesystem-confinement behaviour and approval posture only.
- **Windows kernel sandbox** — still none; Auto → Ask on Windows (unchanged).
- **SPA display/editing of `command_rules`** — D3 rules are config-file-only; a UI for them is future work and would need a schema first.
- **The exec allowlist's per-website/per-domain semantics** — retired with the allowlist, not re-homed.

---

## 12. Traceability matrix

| Requirement | Representative BDD scenario(s) | Test level |
|---|---|---|
| FR-001, FR-002 | S1, S2, S3 | Unit (mode resolver) |
| FR-003 | S5, S6, S7 | Unit (write handler) + E2E (UAT) |
| FR-004, FR-006 | S4, S10 | Unit (session modifier) |
| FR-005 | S8, S9 | Unit (inheritance) |
| FR-007, FR-008, FR-014 | S11, S12, S13, S14 | Unit (platform predicate) |
| FR-009, FR-010 | S15, S16 | Integration (pre-flight vs derived policy) |
| FR-011 | S17, S18 | Integration |
| FR-012 | S21 | Unit (lock test, cross-OS) |
| FR-013 | S20 | Integration (synthetic missed-denial) |
| FR-016, FR-017 | S17, S18, S19 | Integration (grant store) |
| FR-018, FR-019 | S22, S25, S27 | Unit (rule resolver) |
| FR-020 | S23 | Unit (segment split) |
| FR-021 | S24 | Unit (resolve-and-bind regression) |
| FR-022 | (R-8/R-9 acceptance) | Unit (fold-in) |
| FR-023 | S28, S36 | Unit + E2E (dialog) |
| FR-024 | S29, S30, S31 | Unit + E2E |
| FR-025 | S32 | Unit |
| FR-026 | S33, S34, S35 | Unit (prefix algorithm) |
| FR-027 | S37 | Unit + E2E |
| FR-028 | (non-feature — assert no UI exists) | E2E |
| FR-029 | S26 | Integration (God Mode + deny) |
| FR-030, FR-031 | (invariance regression, comment fix) | Unit + audit assertion |
| FR-032 | S38, S39 | Unit (audit events) |
| FR-033 | S40, S41 | E2E (badge) |
| FR-034 | S42 | E2E (banner) |
| FR-035 | §7 removal acceptance checks | Unit (guards self-check) + `make lint-guards` |

Every FR appears in the matrix; every BDD scenario traces to at least one FR.
