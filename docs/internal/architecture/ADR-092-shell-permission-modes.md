# ADR-092 — Shell permission modes: Ask / Auto / God Mode; drop the block list; one rule format

> Numbering: this ADR was drafted as ADR-091 and renumbered to ADR-092 on 2026-09-23 (ADR-091 is "A sub-agent is a session steered by another session"). Commits pushed before the rename still say ADR-091.

- **Status:** Proposed (founder-approved decisions incl. UI, 2026-09-23; revised 2026-09-23 after a two-pass spec grill returned BLOCK — see Revision note)
- **Date:** 2026-09-23
- **Deciders:** Daniel Piatkowski (founder, ratifying decision); architect (design, drafting)
- **Supersedes-in-part:** [ADR-036 — Consolidate shell and subagent tools](./ADR-036-consolidate-shell-and-subagent-tools.md) §3.1's deny-pattern axis and its "one approval flow" decision (still valid; this ADR adds the mode selector in front of it).
- **Extends:** [ADR-077 — Two-layer tool-policy model](./ADR-077-two-layer-tool-policy-remove-fail-closed-backfill.md) — the three modes select which value the `bash` tool policy resolves to at Layer 1; the two-layer ceiling/override model itself is untouched.
- **Evidence baseline:** `release/v0.1.1` @ `838d8092c` (worktree `wt-release-session`), read-only.
- **Revision note:** two independent grills of the implementation spec (49 + 14 confirmed findings, verdict BLOCK) found four architecture-level defects in this ADR's first draft: D7's `{path, operation}` widening had no data model on `fspolicy.FSPolicy` (B-10); D2 dropped nine command categories the kernel boundary cannot express, with no compensating control named (B-11, now D8); D3's "rewrite argv[0]" cannot be built against `sh -c`/PowerShell (B-3/B-12); and the global mode write had no write-authorization requirement (B-13). All four are resolved below by founder decision. A fifth, code-level defect — `CheckGrantOrRequestApproval` is reachable only from the `ask`-policy path, so Auto's `allow` ceiling never touches the grant store as D3 originally described (B-1) — is also resolved below; it does not change a Decision letter, it corrects D3's description of where grant consultation actually happens.

## Context

### The problem: six mechanisms, no single story

A `bash` call today passes through, in fixed order: (1) a hardcoded ~38-regex deny list (`defaultDenyPatterns`, `pkg/tools/shell.go`) that cannot be disabled and is unconditional even under God Mode; (2) a structural command-substitution guard (`pkg/tools/shell_subst_guard.go::substitutionGuard`); (3) operator-configured deny patterns, global and per-agent, both opt-in and off by default; (4) a command-text path-containment scan with an ADR-068 read/write carve-out (`pkg/tools/shell_path_guard.go::guardCommand`); (5) a per-binary exec allowlist, a no-op unless configured (`pkg/policy/evaluator.go::EvaluateExec`); and (6) the ADR-077 tool-policy verdict for `"bash"` itself (default `allow`).

None of these six is redundant by design; they accumulated independently across several ADRs and nothing coordinates between them or explains, to an operator, what actually stops a destructive command in each mode. Mechanisms 1 and 3 are regexes over lowercased raw command text — the code's own comment: "a list of phrasings, not a judgement about the act." They are defeated by mid-token quoting (`r'm' -rf /`), `$IFS` substitution, or variable indirection (`X=rm; $X -rf /`). Where a kernel sandbox is active, it is the real boundary for *where* files can be touched; the deny-pattern layer is the only thing standing between an agent and a destructive *phrasing* against files the kernel would otherwise allow it to touch — and that is the bypassable layer.

### GitHub issue #83, and its correction

Issue #83's headline claim traces to `pkg/security/execapproval.go::ExecApprovalManager` — confirmed still buggy in that file, but the manager has **zero production callers**; ADR-036 §3.4 deleted its frontend and relocated live grant consultation to `pkg/security/approvalgrants.go::ApprovalGrantStore`, which does not share the defect. Issue #83's *second*, "Additional" claim is different and **still live**: the opt-in exec allowlist (`pkg/policy/evaluator.go::EvaluateExec`) matches raw command strings with no `exec.LookPath`/`filepath.Abs`/symlink resolution — an allowed pattern like `"git *"` says yes to `git evil-script` without checking which file named `git` is about to run. D3 closes this directly.

### Why simplify now

Six independently-evolved mechanisms is not a security posture, it is an audit liability. Claude Code and Codex CLI — surveyed for this ADR, and again for this revision (see the network-containment addendum, D8) — ship no curated "dangerous command" deny list. Claude Code: "Bash permission patterns that try to constrain command arguments are fragile"; the OS sandbox is the real boundary. Codex intercepts `execve(2)` at the syscall level and ships no default `execpolicy` rules. Both give `deny` an unconditional trump card over `allow`. This ADR adopts that posture — **and** both tools' actual replacement for the deleted block list, which the first draft of this ADR omitted: neither ships network access on by default in their sandboxed mode (D8).

## Decisions

### D1 — Three modes: Ask, Auto, God Mode

Every bash-capable context resolves to exactly one of three modes (global, per-agent, per-chat):

- **Ask** — every shell command shows the approval dialog.
- **Auto** (fresh-install default) — commands run while a kernel sandbox confines them (filesystem, D7; network, D8); anything needing more asks. Where no kernel sandbox is active, **Auto behaves like Ask**.
- **God Mode** — no approvals, no kernel sandbox, no network egress filter. The existing `sandbox.GodMode`/`GodModeAllowed` fields (`pkg/config/sandbox.go`), unchanged mechanism; this ADR renames the posture and clarifies what it turns off (D6).

**"Kernel sandbox active" predicate** (drives Auto/Ask fallback and the status badge; one server-derived value, both read it):

| Platform | Predicate | Auto behaviour |
|---|---|---|
| Linux | Landlock applied in enforce mode (`sandbox.mode = enforce`) | Auto |
| macOS | Seatbelt backend active — reads Seatbelt availability/enable state directly, **not** the existing `SandboxStatus.policy_applied` field, which reports the *gateway's own* confinement and is documented false on macOS by design (Seatbelt confines children only, `pkg/sandbox/CLAUDE.md`) | Auto |
| Windows | Never — no kernel sandbox backend | Ask |

**Mode storage — no new field at the global or per-agent level [resolves B-2].** Global and per-agent mode are a *presentation*, not new state: **Ask** ↔ the existing `bash` tool-policy value `"ask"`; **Auto**/**God Mode** both ↔ the existing value `"allow"`, distinguished by the existing, separate `GodMode` flag (global-only). `config.ReconcileToolPolicyCeiling` and `resolveEffectivePolicyWith` (ADR-077) need no change. **Only the per-chat modifier is new, session-keyed state** (unchanged from the original draft): applied *after* the global×agent merge resolves the `bash` value, structurally like `ApprovalGrantStore` — not a `chat_id` key on either policy map (Hard Constraint #6). An agent may still carry an explicit stricter per-agent `bash` override (e.g. ADR-090's Jim, `bash: deny`) that is *not* one of the three named modes; the UI shows such an agent as its nearest named mode (Ask) with a "custom override" indicator, never forces it into a bucket the strictest-wins merge would silently loosen.

**Tighten-only below the global default, enforced server-side.** A per-agent or per-chat write that would loosen is rejected with 4xx by the write handler, which re-reads the global default at write time. UI hiding the looser option is defence-in-depth only.

**Global mode write requires the password step-up [resolves B-13].** Because global mode lives inside `cfg.Sandbox.*` (above), a global-mode write is a `sandbox.*` config mutation and goes through the **existing** `rest_sandbox_config.go` PUT handler, which already gates every sandbox-config write behind `requireReAuth` — the same re-type-your-password step-up God Mode and credential writes use. No new authentication mechanism is needed; the requirement is that the mode write use this handler, not a bespoke endpoint that bypasses it. The agent-facing `set_config` tool already blocks the whole `sandbox.*` subtree by ancestor closure (`pkg/sysagent/tools/config.go::blockedConfigKeys`), so an agent cannot reach global mode through that path either. Per-agent and per-chat writes need no step-up — they can only tighten. **Accepted, pre-existing gap named, not fixed here:** on Windows, `config.json` is not kernel-denied to a spawned child (`FallbackBackend`'s own doc comment: "cooperative processes only"), so an agent's own `bash` tool can write `config.json` directly, bypassing both `set_config` and the step-up gate — a Linux/macOS-only guarantee, documented as such, not new to this ADR.

**Delegation:** the session modifier inherits to delegated subagent sessions exactly as approval grants do (`ApprovalGrantStore.InheritFrom`); a delegate resolves the tightest of (parent's modifier, delegate's own override). **Mid-turn:** a command is resolved against the mode in force when `resolveToolPolicyAtExec` (the existing TOCTOU re-check, unchanged) last ran for it; a tighten landing after does not retroactively change that command's verdict.

**In all three modes**, the audit log, prompt-injection guard, and rate limiting stay on (verified: none conditioned on `godMode` in `executeRun`). **Operator `deny` rules (D3) stay in force in God Mode — new behaviour**: D3 rules are enforced inside the shell tool, downstream of `resolveEffectivePolicyWith`'s God Mode floor, so the floor cannot erase them (matches Claude Code's managed-deny precedent).

### D2 — Drop the built-in shell block list entirely

`defaultDenyPatterns` (~38 regexes, includes `secretGuardPatterns`) and the per-agent/global deny-pattern config are removed outright. **Rationale:** bypassable by construction; neither Claude Code nor Codex ships this; the kernel sandbox is the real boundary where one exists. The secrets regex is a backstop over a boundary (`~/.omnipus/`'s secret files) the kernel already denies independently of command text where a sandbox is active.

**What replaces the coverage, stated honestly [resolves B-4 — the first draft claimed the pre-flight was a "compensating control"; it is not, for anything outside filesystem containment]:** of the ~38 patterns, the filesystem-shaped ones (path writes, `rm` chains) are covered by the kernel boundary D7 already made explicit. The **network-shaped** ones — `git push`, `ssh …@`, `curl|wget … | sh`, `apt/yum/dnf/npm/pip install`, `docker run`/`exec` — have no filesystem expression at all; **D8 below is their compensating control**, not D7's pre-flight. The remaining categories — `shutdown`/`reboot`/`poweroff`, `kill`/`pkill`/`killall`, the fork bomb, `sudo`, `chmod`/`chown`, `eval`, `source *.sh` — are neither filesystem nor network operations, and **nothing in this ADR covers them**. This is the same accepted trade Claude Code and Codex ship and document (verified: neither addresses process limits, signals, or in-workspace destruction in their sandbox docs — see D8's sourcing). The trade is real, ratified, and stated once here rather than implied by D7's ordering.

**Accepted residual risk, precisely scoped:** on Windows (no kernel sandbox), in God Mode (no sandbox, no D8), and for the categories above on every platform, nothing in this ADR blocks a destructive command by phrasing. This is not new — the block list was the *only* thing providing even bypassable coverage there, and it is gone.

**What survives, per guard:** structural command-substitution guard (`shell_subst_guard.go::substitutionGuard`) — unchanged. Path-containment guard (`shell_path_guard.go::guardCommand`'s absolute-path scan) — survives, mode-sensitive disposition (Ask hard-denies; Auto surfaces the pre-flight escalation, D7; God Mode hard-denies, no prompt). Per-binary exec allowlist — retired, folded into D3. ADR-077 `bash` policy — survives, selected via modes. `ApprovalGrantStore` — survives, extended (D4/D7/D8).

### D3 — One rule format and one decision order

Operator rules (`config.SandboxConfig.CommandRules`, json `command_rules`, `{action: allow|ask|deny, binary, arg_prefix?}`, config-file-only, no wire schema) and user grants (D4) share one format and order: **deny beats ask beats allow, specificity-blind.** Chained commands (`&&`, `||`, `;`, `|`, newline) split via the **existing** `splitShellSegments`/`shellCommandHead` (`shell_subst_guard.go`) — no new parser; each segment must independently clear, or the whole call asks.

**Resolve-and-verify, not resolve-and-rewrite [resolves B-3/B-12 — "rewrite argv[0]" is not implementable].** `pkg/tools/shell.go::buildShellArgv` execs `["sh", "-c", command]` on POSIX and `["powershell", "-NoProfile", "-NonInteractive", "-Command", command]` on Windows — `argv[0]` is always `sh`/`powershell`, never the user's binary; there is no argv slot naming `git` to rewrite, and rewriting the `-c` string is attacker-influenced text surgery, the exact class D2 just rejected. Instead: **the D3 matcher resolves a segment's leading token to its actual executable against the child's effective PATH/env, matches operator rules against the resolved absolute path, and re-verifies that resolution immediately before spawn.** The command text itself is never rewritten. The residual window — a swap between resolve and exec (TOCTOU) — is explicitly the kernel sandbox's boundary, not the rule engine's; this narrows, and does not claim to close, the gap `EvaluateExec` left open (issue #83 "Additional"). **Windows [founder decision]:** `splitShellSegments` is a POSIX operator set (`| ; & \n \r`) that does not model PowerShell grammar, and there is no `argv[0]` there either. On Windows, D3/D4 grants and rules are **exact-command only** — no prefix option, no chained-segment splitting — a documented limitation, not a partial POSIX behaviour.

**Exec allowlist folded in, not run alongside** (`Evaluator.EvaluateExec`/`MatchGlob`/`FirstToken`, keyed `security.policy.exec.allowed_binaries`, retired; configured entries become D3 `allow` rules, operator re-authored — greenfield, no auto-conversion). `PolicyAuditor.logDecision`'s write survives, retargeted — but see D7's audit note: `logDecision` is unexported, single-caller, and its four-string signature carries no room for mode/level/scope, so the *new* D7/D8/D4 events route through `ExecTool.emitAudit`'s existing `audit.Entry.Details map[string]any` instead (D3's own `logDecision` retarget is unaffected — it keeps logging exec decisions the way it does today).

**Honest gap:** D3 rules bind to a segment's head and do not see through interpreters — `sh -c 'rm -rf X'`, `xargs rm`, `find … -exec rm`, `awk 'system(…)'` escape a `deny` on `rm`. Friction layer, not containment; the kernel is the boundary.

### D4 — Approval dialog: three buttons, a scope choice, chained commands per part

`ToolApprovalModal.tsx` is **[Deny] [Allow once] [Allow]**, replacing `Approve`/`Deny`/`Always Allow`/`Cancel`. **Allow** records a session grant with a scope choice: **exact** (default — command text + `cwd`, unchanged from today) or **prefix** (new — `{binary, arg_prefix}`, ignores `cwd`, token-boundary matched so `npm run test` does not match `npm run testfoo`). `run_in_background` is a separate match dimension for both scopes. Chained commands grant one rule per segment; a later partial match prompts only the unmatched segment(s).

**Suggested-prefix algorithm:** resolved binary plus leading sub-command words, stopping at the first flag/path-shaped-arg/URL token. A leading env-assignment is stripped before deriving; the grant matches under any assignment value. A known wrapper (`sudo`, `env`, `timeout`, `xargs`, `sh -c`) is **not** stripped — the prefix is derived starting from the wrapper (`sudo apt install …` → `sudo apt install`), never from what it executes.

**`cancel` stays in the wire enum, with no button [corrects an over-deletion the first draft's four-button-to-three collapse implied].** `ToolApprovalModal.resolution.test.tsx` verifies Escape→`deny` (leaves the approval unresolved on a network failure, so a later snapshot can still bring it back) and the old Cancel→`cancel` (resolves it locally on a lost-server 404) behave *differently on purpose*, and the headless CLI approval path (`pkg/app/internal/run/run.go`) drives the same enum. The three-button UI never shows a Cancel button and Escape/overlay/X all resolve to `deny` as stated — but `cancel` remains a valid, client-issued action value for the stuck-approval recovery path, not removed from the schema.

Session grants are **not shown anywhere** — no list, no chip, no revoke surface; they end with the chat. This is an explicit non-feature. Per-website network grants are future work, not built here.

### D5 — Delete the dead code

`pkg/security/execapproval.go` (`ExecApprovalManager` et al.) and its test file, deleted outright — zero production callers, answers a UI flow ADR-036 already deleted.

### D6 — Fix God Mode's code comment

`pkg/config/sandbox.go`'s `GodMode` comment is rewritten to state what remains true post-D1–D3: floors every agent's ceiling at `allow` (D3 `deny` still applies), forces sandbox off, forces egress open, does not disable audit/injection-guard/rate-limiting. Documentation-accuracy fix only.

### D7 — Auto's filesystem pre-flight: one source of truth, additively extended

Before a command runs, Auto evaluates it against the same rules the kernel will enforce; a command needing more than the sandbox allows asks *before* it starts, never after. **The single source, extended, not forked [resolves B-10 — the first draft claimed `fspolicy.FSPolicy`/`DeriveKernelPolicy` stay "Unchanged," which is false: there is nowhere for a `{path, operation}` grant to live on the current five-field struct]:**

- `fspolicy.FSPolicy` gains one new field: **`PathGrants []PathGrant`**, `PathGrant{Path string; Access uint64}`, using the *same* `sandbox.AccessRead|AccessWrite|AccessExecute` bitmask `PathRule.Access` already uses — not a new vocabulary. Unlike `AllowedRoots` (a subtree **write** grant from workspace mounts), a `PathGrant` is exactly one path and exactly the access class the pre-flight verdict found missing.
- `sandbox.DeriveKernelPolicy` renders each `PathGrant` as one additional `PathRule{Path, Access}` — additive to the existing `WorkDir`/`AllowedRoots` rendering, not a parallel code path.
- `guardCommand`'s path scan (consumer 1) checks `PathGrants` the same way it already checks `AllowedRoots`: contained if within `WorkDir`, `AllowedRoots`, or a `PathGrant` covering the needed access.
- **Tool scope, bash-only [resolves B-5 — an unscoped widening reaches `edit`, `write_file`, `grep`, `send_file`, `web_serve`, and the browser tools, none of which the user was shown]:** `PathGrants` is **not** part of the ambient value every `ResolveTurnFSPolicy` caller reads identically. `ResolveTurnFSPolicy` gains an optional grant-overlay parameter that **only the bash pre-flight/exec call sites** populate from `ApprovalGrantStore`; the other eleven callers (`edit.go`, `filesystem.go`, `grep.go`, `send_file.go`, `web_serve.go`, `request_mount.go`, the browser tools) pass none and are unaffected by a bash-approved widening. `DeriveKernelPolicy` is one function either way — it renders whatever `PathGrants` it is given; the scoping is at the call site, not a second derivation.
- **Secret set is never widenable [resolves C-5/B-14]:** an escalation naming a path in `CarveOuts`/the secret set is refused outright — never prompted, never grantable. This is a requirement, not an accident of `CarveOuts` happening to also apply.

**Consequence:** the next identical command in the session resolves the widened policy directly (no re-prompt); the widening does not leak past session close; a delegate inherits it via the same `InheritFrom` mechanism as command grants.

**Honest gaps, corrected claim [resolves C-6/B-14 — FR-013 in the first draft promised a "clear [sandbox-specific] error" a Landlock denial cannot produce]:** a pre-flight check cannot see symlink resolution at access time, a runtime-constructed path, or a re-exec under another identity. When the kernel denies something pre-flight missed, **the command fails with the child's own OS error (e.g. a bare permission denial); Omnipus does not attempt to diagnose it as specifically a sandbox denial** — Landlock's denial is not a structured, observable event to the gateway, the same reason runtime-catch was rejected as the primary design (Alternatives considered). It is never silently allowed and never silently retried unconstrained.

**No kernel sandbox** (Windows, app-level fallback, Landlock-degraded): Auto defers to Ask (D1) rather than guessing — `FSPolicy` is platform-independent and still feeds the app-layer guard, but there is no kernel rendering to check a pre-flight verdict against.

**Anti-drift lock test:** a generated `{path, operation}` matrix, evaluated against a fixed `FSPolicy` including a non-empty `PathGrants`, asserts the pre-flight verdict and `DeriveKernelPolicy`'s actual rendering agree; a deliberately mismatched pair must fail the test (proves the comparator catches drift, not merely runs). Pure Go, every CI OS; a second, Linux-only leg spawns a real Landlock-confined child.

### D8 — Auto denies outbound network by default [new, resolves B-11/B-13's network half; founder decision, 2026-09-23]

D2 deletes `git push`, `ssh …@`, `curl/wget … | sh`, `apt/yum/dnf/npm/pip install`, `docker run`/`exec` along with the rest of the block list — every one of these is a **network** operation, and D7's filesystem pre-flight does not cover any of them (D2's own table, corrected above). **This is their compensating control, mirroring what Claude Code and Codex actually ship** (research addendum: `dist/lanes/network-containment-and-mode-auth.md`) — both deny network by default in their most-permissive sandboxed mode and escalate to a prompt on first need; neither uses a command-name deny list for this.

**Mechanism, per platform, honestly scoped:**

- **Linux, Landlock ABI v4+ (kernel 5.19+ syscalls, 6.7+ net rights — `pkg/sandbox/sandbox.go`'s own version gate):** the kernel process-wide boot policy already seeds every child's `ConnectPortRules` with `DefaultConnectPorts = {53, 80, 443}` unconditionally (`sandbox.go::DefaultPolicy`) — allow-by-default for those three ports today. For **the bash tool's rendered per-turn policy under Auto only**, `ConnectPortRules`/`BindPortRules` render **empty** instead — confirmed from `sandbox_linux.go`: on ABI≥4, `handledAccessNet` is installed unconditionally (not conditioned on the rule list being non-empty), so an empty list is a true kernel-enforced deny-all for that child's `connect(2)`, not "no restriction." A network-need classifier — a sibling to D7's pre-flight, reusing D3's resolved-binary step — flags a command via (a) a curated, operator-extendable set of network-capable binaries (`git`, `curl`, `wget`, `ssh`, `scp`, `rsync`, `npm`/`pnpm`/`yarn`, `pip`, `apt`/`yum`/`dnf`, `docker`, `gh`, cloud CLIs) or (b) a literal `http(s)://` token. A flagged command escalates before spawn, exactly like D7's prompt; approving widens the session's rendered `ConnectPortRules` to `DefaultConnectPorts` (port-level, not domain-level — Landlock `NET_CONNECT_TCP` cannot filter by host, `sandbox.go`'s own documented limitation) via a new `network` grant kind in `ApprovalGrantStore`, same session/delegate/clear-on-close lifetime as D7's path grants.
- **macOS:** the Seatbelt profile already renders `ConnectPortRules` identically (`seatbelt_profile.go`); same empty-by-default / widen mechanism applies to the bash child.
- **Windows:** no kernel network control exists. D1 already makes Windows Auto behave as Ask, so every command already prompts — D8 adds nothing there and takes nothing away.

**Honest gap, symmetric to D7's:** a command the classifier misses but that opens a raw socket is denied by the kernel at `connect()` time on Linux/macOS (fails with a clear error, same honest-gap pattern as D7) — not silently allowed. CIDR/host-level filtering for traffic Omnipus's own HTTP clients make remains the existing `SSRFChecker`/`ExecProxy` (`pkg/security/execproxy.go`), unchanged; a bash-spawned binary that ignores its `HTTP_PROXY`/`HTTPS_PROXY` env vars bypasses `ExecProxy` entirely — a pre-existing, documented limitation (`egress_proxy.go`'s own header), not new here, and D8's port-level kernel deny is the actual containment for that case, not the proxy.

**What D8 does not cover, stated once [ties back to D2/D4]:** `shutdown`/`kill`/the fork bomb/`sudo`/`chmod`/`eval` are not network operations; D8 is silent on them by design, same as D2.

## Removal inventory (mandatory, same change set)

Every mechanism superseded is deleted completely — no shims, no dead code, no "kept for reference." Grep-verified against `838d8092c`.

**1. Block list (D2):** `defaultDenyPatterns`/`secretGuardPatterns`/`buildSecretGuardPatterns` (`shell.go`); `applyDenyPatterns`/`compileDenyPatterns`/`denyPatternMessage` (`shell_guard.go`, `lowerASCII` survives, moves to `shell_subst_guard.go`); `config.SandboxConfig.ShellDenyPatterns`, `config.AgentShellPolicy` (type deleted whole — its only two fields both go); `validateShellDenyPatterns` (`gateway/sandbox_config_validation.go`) + its caller; `ExecToolDeps.GlobalShellDenyPatterns`/`AgentShellPolicy` + `loop_wire.go`'s construction (incl. the now-moot god-mode carve-out); contracts (`AgentShellPolicy.yaml` deleted; `SandboxConfig.yaml`/`SandboxConfigUpdate.yaml`/`AgentUpdateRequest.yaml` edited); SPA (`ShellDenyPatternsEditor.tsx` + every listed reference site); `shell_secret_guard_test.go` deleted, `shell_readwrite_guard_test.go`/`shell_mount_guard_test.go` survive (different mechanism).
  - **Four additional live surfaces [resolves B-6 — missed in the first draft]:** `pkg/sysagent/tools/agent_apply_args.go::applyShellPolicy` — an **agent-callable** write path constructing `AgentShellPolicy` from tool args; deleting the type without this is a compile break in an unowned package. `pkg/gateway/rest_agents_update.go` — change-tracking (`add(req.ShellPolicy != nil, ...)`), pattern validation, and the field-merge block (lines ~88, ~244–249, ~1040–1059). `pkg/gateway/rest_agents_create.go` — create-path decode of `shell_policy` (`agentCreateShellPolicyFromWire` et al.). `pkg/migrate/sources/openclaw/openclaw_config.go` (+ its `_test.go`) — the OpenClaw importer maps legacy deny patterns onto `Sandbox.ShellDenyPatterns`/`AgentConfig.ShellPolicy`; both target fields are gone, so the migration branch (`ToStandardConfig`, ~lines 941–957) is deleted with them, greenfield, no replacement mapping.
  - **Stale-client PUT gets a hard 400, not a silent drop [resolves B-7 — the acceptance check named the wrong handler]:** `rest_agents_create.go`'s `DisallowUnknownFields` only guards the **create** path. A stale client still `PUT`ting `shell_policy` after deletion hits `rest_agents_update.go`, whose own comment documents that its decode is non-strict by default (`validate_inbound` defaults false) and a client-sent-but-now-unknown field is silently dropped with a 200. Add `shell_policy` to the existing retired-field raw-body sniff in `rest_agents_update.go`, matching the `sandbox_profile`/`delegation_policy` precedent already there — assert 400 on the **PUT**, not the POST.

**2. Dead "always allow" manager (D5):** `pkg/security/execapproval.go` + test, in full.

**3. Exec allowlist, folded into D3:** `pkg/policy/evaluator.go` in full; `PolicyAuditor.EvaluateExec` retired, `logDecision` retargeted; `ExecConfig.AllowedBinaries` + `ExecAllowlist.yaml`; REST/SPA surfaces (`HandleExecAllowlist`, `/security/exec-allowlist` route + openapi path, `ExecAllowlistSection.tsx`, `fetchExecAllowlist`/`updateExecAllowlist`).

**4. `ExecConfig.Approval`** (`tools.exec.approval`) and the untyped `policy_mode`/`exec_approval` `SecuritySection` keys — reachable but unenforced (three references, none load-bearing); confirm dead and delete alongside item 3, or fold into D1 if the implementation pass finds a live consumer this ADR's evidence missed.

**5. Docs/CLAUDE.md:** the `GodMode` comment (D6); the 13 docs naming retired mechanisms, triaged historical-vs-update; add both retired-surface entries to root `CLAUDE.md` with guards `scripts/check-no-shell-deny-patterns.sh` / `scripts/check-no-exec-approval-manager.sh` / a third exec-allowlist check, wired into `scripts/guards.sh`.

## Consequences

### Security
Removes a bypassable text layer; D8 is its network-shaped compensating control, stated as a decision, not implied by D7's ordering; closes issue #83's live "Additional" claim via D3's resolve-and-verify; makes #83's headline claim moot by deleting the dead code (D5). Accepted, precisely-scoped residual risk: Windows, God Mode, and the non-filesystem/non-network categories D2/D8 both leave untouched (D2) — the same trade Claude Code and Codex document, now documented here the same way instead of relying on a bypassable regex.

### Audit
Four new events — mode change, pre-flight escalation (filesystem or network), grant recorded (scope: exact/prefix/path-widening/network-widening), approval decision — written via `ExecTool.emitAudit`'s `Details` map (not `logDecision`, which cannot carry these fields — D3).

### UX
One selector replaces six mechanisms. Fewer prompts in Auto; every prompt that fires means the sandbox genuinely could not confine the action. Prefix grants close a real friction gap `ApprovalGrantStore`'s exact-fingerprint today cannot. **Accepted trade:** no grants list/revoke UI (D4) — a real gap relative to Claude Code's `/permissions`, accepted because Omnipus grants are session-only and self-expire at chat end. Headless turns under Ask stall to the existing `ToolApprovalTimeout`, unchanged; operators set the global default accordingly.

### Migration
**Greenfield, no upgrade path.** Retired keys are dropped on load (unknown-field tolerance, file half); the agent REST write path is strict — a stale client `PUT`ting `shell_policy` now gets a hard 400 from `rest_agents_update.go`'s extended raw-body sniff (corrected above; the first draft cited the create path, which a PUT never reaches).

### Interaction with ADR-077
The three modes select what the `bash` ceiling resolves to; the merge mechanism is untouched. God Mode floors the ceiling at `allow` (D1's storage note); D3 `deny` rules and an agent's own stricter per-agent override both continue to survive it under strictest-wins, as today.

## UI presentation

Three-level, tighten-only mode choice (Settings → Security; Tools & Permissions; composer quick switch) — God Mode global-only, keeps its step-up. Approval dialog: `[Deny][Allow once][Allow]` + scope radio, visible whenever `Allow` is selected regardless of input mode; chained commands listed per segment. No grants-list surface, ever (D4). Chat-header badge reads the resolved effective mode from the **existing** `SandboxStatus` schema, extended with `effective_mode` (reuse, not a second `kernel_sandbox_active` derivation — `policy_applied`/`kernel_level` already exist there) — "Auto → Ask" with a tooltip when Auto has no active kernel sandbox. God Mode banner: the **existing** `GodModeActiveBanner` (`src/components/settings/GodModeControl.tsx`, already rendered at `GatewaySection.tsx:298`, already has a fetch-failure-safe `isError` variant) is **relocated** to `AppShell` app-wide — a move, not a new build; its fetch-failure state and stale "shell guard" body text (D6) both need preserving/correcting in the move. Design system: load `.claude/skills/omnipus-design-system/SKILL.md` before touching any of the above.

## Implementation outline

1. **Schema first** (Hard Constraint #8): mode-status field (extend `SandboxStatus`), mode-selector write shapes, the redesigned approval action/frame (retain `cancel`), removal-inventory deletions/edits. Regenerate before any handler code.
2. Land the D3 rule shape (resolved-binary match, segment scope, exact/prefix, Windows exact-only) as a shared type; wire `splitShellSegments`/`shellCommandHead` as its POSIX segment source.
3. Extend `fspolicy.FSPolicy` with `PathGrants` and `DeriveKernelPolicy`'s rendering (D7); wire the bash-scoped `ResolveTurnFSPolicy` overlay parameter.
4. Wire the three modes into `bash` ceiling resolution (`loop_policy.go`) and the session modifier (`sessionmode.go`, new); extend `CheckGrantOrRequestApproval`'s reachability to fire from the D3 `ask`-rule and pre-flight-escalation paths, not only the existing `ask`-policy path (D3/D7's grant consultation fix).
5. Build D7's filesystem pre-flight + anti-drift lock test; build D8's network pre-flight (empty-`ConnectPortRules`-by-default rendering + classifier + `network` grant kind) alongside it — both before step 7 deletes the block list.
6. Build the UI surfaces against step 1's contract (mode selectors, dialog redesign, badge, relocated banner).
7. Delete the block list (D2) — after steps 3–5 land, so D7/D8 are the actual compensating controls in place, not merely ordered before an unrelated deletion.
8. Delete the exec allowlist (D5/removal item 3) — after step 2, which replaces its decision logic.
9. Resolve removal item 4 (`ExecConfig.Approval`); fix the `GodMode` comment (D6); wire the CI guards; triage docs.

## Test plan (summary — BDD detail lives in the implementation spec)

Mode resolution and tighten-only merge across all three levels; Auto pre-flight for both filesystem (D7) and network (D8), including the anti-drift lock test and its deliberate-mismatch self-check; resolve-and-verify against a look-alike PATH binary (issue #83 regression); God Mode + D3 `deny` still refuses; the four audit events fire with correct payloads in all three modes; deletion regressions (surviving guards' existing suites still pass); a `release/v0.1.1`-shaped config with every retired key loads cleanly; the stale-client `PUT` 400 (B-7); the secret-set non-widenability adversarial case (C-5); a denied escalation refuses the command outright, never runs un-widened. Full scenario-level detail: `docs/internal/specs/adr-092-shell-permission-modes-spec.md`.

## Open questions

1. **Per-turn single-value threading.** `guardCommand`, `turnKernelPolicy`, and the two pre-flight evaluators (D7, D8) each call `ResolveTurnFSPolicy`/the network equivalent independently; nothing structurally guarantees one instance backs all of them within a turn if state changes mid-call. Implementation decides: cache-and-reuse vs. provably-safe independent calls.
2. **`ExecConfig.Approval` reachability** (removal item 4) — confirm dead before deleting.

## Affected components

- **Backend:** `pkg/tools/shell.go`, `shell_guard.go`, `shell_path_guard.go` (survives, mode-sensitive), `shell_subst_guard.go` (new caller), `pkg/policy/evaluator.go` (deleted), `auditor.go` (retargeted), `pkg/security/approvalgrants.go` (prefix/path-widening/network-widening record kinds), `execapproval.go`+test (deleted), `pkg/fspolicy/policy.go` (`PathGrants`), `pkg/sandbox/derive_from_fspolicy.go` (renders them), `pkg/sandbox/sandbox.go`/`sandbox_linux.go`/`seatbelt_profile.go` (bash-scoped empty-`ConnectPortRules` rendering, D8), `pkg/config/sandbox.go`/`config.go`, `pkg/gateway/sandbox_config_validation.go`/`rest_sandbox_config.go`/`rest_exec.go`, `pkg/gateway/rest_agents_update.go`/`rest_agents_create.go` (B-6/B-7), `pkg/sysagent/tools/agent_apply_args.go` (B-6), `pkg/migrate/sources/openclaw/openclaw_config.go` (B-6), `pkg/agent/loop_wire.go`/`loop_construct.go`/`loop_policy.go`.
- **Frontend:** `ToolApprovalModal.tsx`, `BashApprovalPreview.tsx`, `ShellDenyPatternsEditor.tsx` (deleted), `ExecAllowlistSection.tsx` (deleted), `SecuritySection.tsx`, `GodModeControl.tsx` (banner relocated to `AppShell`), `ToolsAndPermissions.tsx`, chat composer/header.
- **Contracts:** deletions (`AgentShellPolicy.yaml`, `ExecAllowlist.yaml`), edits (`SandboxConfig.yaml`, `SandboxConfigUpdate.yaml`, `AgentUpdateRequest.yaml`, `SandboxStatus.yaml` extended), additions (mode-selector shapes, `ToolApprovalActionRequest`/`ToolApprovalRequiredFrame` — the **`asyncapi.yaml` inline copy**, not only the standalone schema file, since that inline copy is what generates the SPA's types).
- **Variants:** identical decision logic OSS/Desktop/SaaS; platform degradation asymmetry (Windows always Ask) is pre-existing and unchanged.

## Alternatives considered

**Keep the block list as an off-by-default layer.** Rejected — reintroduces the false-coverage problem; every vendor surveyed rejected this shape.

**Auto-migrate deny patterns into D3 rules.** Rejected under greenfield — a regex and a resolved-binary rule are not equivalent; a 1:1 translation would misrepresent coverage.

**Runtime-catch the kernel's actual denial, filesystem and network alike.** Considered, then rejected for D7 (a Landlock/Seatbelt denial is not a structured event the gateway can observe; a command may have real side effects before hitting it) — and rejected for the identical reason when D8 was added: a network denial has the same unobservability problem, so D8 uses the same pre-flight-classifier shape as D7 rather than a second, differently-designed detection mechanism.
