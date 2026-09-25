# ADR-092 Implementation Specification — Shell permission modes (Ask / Auto / God Mode)

- **Spec status:** Draft for lead review (plan-spec output; not committed — lead commits)
- **ADR implemented:** [ADR-092 — Shell permission modes](../architecture/ADR-092-shell-permission-modes.md) (revised 2026-09-23 after a two-pass grill returned BLOCK — see the ADR's Revision note)
- **Evidence baseline:** `release/v0.1.1` @ `838d8092c` (read-only)
- **Correction note (2026-09-23, docs lane L7):** FR-001, FR-023, FR-033, FR-043 and §6.1/§6.2 are corrected to match the built code (marked *[corrected 2026-09-23]*); §3.10 adds FR-051 to FR-062 for ADR-092 D9 (Auto for tools other than `bash`), with scenarios S54 to S64.
- **Depends on:** ADR-036, ADR-063, ADR-077, ADR-090
- **Repo rule honoured throughout:** greenfield — no migration, no shims. `file::symbol` citations, never line numbers.
- **Revision note:** this pass resolves 14 confirmed blockers from two independent grills (`/tmp/squads/grill-spec-opus.md`, 9 findings + 5 confirmed second-pass; and the untracked `adr-092-shell-permission-modes-spec-review.md`, 49 findings) plus the highest-value MAJOR/minor findings from both. Where the two disagreed, the architect's own code re-verification (cited inline) is followed. The untracked review file is scratch, not a deliverable, and is deleted at the end of this pass.

---

## 1. Purpose and scope

Testable requirements, BDD scenarios, test data, contract-first work, removal tasks, an 8-lane delivery plan, and acceptance criteria for ADR-092.

**In scope:** three presented states and their resolution (*[corrected 2026-09-23]* storage: a separate Auto-approve switch at global, per-agent and per-chat scope, see FR-001); Auto for tools other than `bash` (D9, §3.10); the filesystem pre-flight (D7) **and its data model** (`FSPolicy.PathGrants`, bash-scoped); the network pre-flight (D8, new); the unified rule format, decision order, and **grant-consultation reachability** (corrected — D3); resolve-and-verify binary binding (corrected from "rewrite argv0" — D3); the approval dialog redesign (`cancel` retained in the wire enum); God Mode; the platform predicate; audit events (routed via `emitAudit`, not `logDecision`); the status contract field (extends `SandboxStatus`, not a new schema); the global-mode write's password step-up; and the full removal inventory including four previously-missed live surfaces.

**Out of scope:** per-website network approvals; a grants management surface; network egress changes to tools other than `bash`'s own child (unchanged); Windows kernel sandbox (still none). *[corrected 2026-09-24, founder decision — the original text here said "D1's existing Auto→Ask fallback is the mitigation, not new coverage"]* There is no longer an Auto→Ask fallback: since 2026-09-24, Auto applies on Windows exactly as elsewhere, mitigated only by the D7/D8 pre-flights and the surviving text-based guards — see ADR-092's 2026-09-24 revision note. Full list in §11.

---

## 2. Existing code context

| Symbol (file) | Role | Fate |
|---|---|---|
| `ResolveTurnFSPolicy` (`resolvepath.go`) | single authored per-turn `FSPolicy` | **Extended**: gains an optional grant-overlay parameter, populated only by bash's own pre-flight/exec call sites (FR-036) |
| `fspolicy.FSPolicy` (`fspolicy/policy.go`) | five fields today: `WorkDir`, `Scope`, `CarveOuts`, `AllowedRoots`, `ReadConfined` | **Additively extended** — gains `PathGrants []PathGrant` (FR-036). `AllowedRoots` is a subtree **write** grant only; there is no existing per-file or read-grant field, which is why this is additive, not "Unchanged" as the first draft claimed |
| `DeriveKernelPolicy` (`derive_from_fspolicy.go`) | authored policy → kernel policy | **Additively extended** — renders each `PathGrant` as one more `PathRule`; the anti-drift lock test's oracle |
| `resolveEffectivePolicyWith` (`compositor.go`) | strictest-wins global×agent merge; `if cfg.GodMode { g = allow }` | **Unchanged** — global/per-agent mode is a presentation over this, not new storage (FR-001) |
| `CheckGrantOrRequestApproval` (`loop_policy.go`) | grant-store consultation | **Reachability extended** — today reachable ONLY from `resolveAskPolicy`, itself gated on `toctouPolicy == "ask"` (verified: `loop_run_turn_tools.go::resolveAskPolicy`); an `"allow"` verdict never calls it. FR-039 adds two more call sites: a D3 `ask`-rule verdict, and a pre-flight escalation (D7/D8) |
| `sandbox.DefaultConnectPorts` = `{53,80,443}` (`sandbox.go`) | boot-default outbound port allow-list, seeded unconditionally into every child's `ConnectPortRules` | **Unchanged for the boot profile**; bash's own per-turn Auto rendering starts empty instead (FR-042) |
| `defaultDenyPatterns`/`secretGuardPatterns`/`buildSecretGuardPatterns` (`shell.go`) | ~38-regex block list + secrets backstop | **Deleted** (D2) |
| `applyDenyPatterns`/`compileDenyPatterns`/`denyPatternMessage` (`shell_guard.go`) | block-list matcher | **Deleted** (D2); `lowerASCII` survives, moves to `shell_subst_guard.go` |
| `guardCommand`'s absolute-path scan / `checkPathSegment` (`shell_path_guard.go`) | tokenized path-containment scan (three ordered layers total — **not** "step 4"; the surviving scan is layer 3, corrected citation throughout this spec) | **Survives, mode-sensitive** (FR-041) |
| `pathUseVerdict`/`newPathUseClassifier` (`shell_path_guard.go`) | existing `{readOnly, exec, reason}` three-way verdict | **Reused** as the base for the FR-038 operation classifier, extended to the `{read, write, read+write}` shape FR-016 needs |
| `splitShellSegments`/`shellCommandHead`/`shellCommandHeadDetailed` (`shell_subst_guard.go`) | tokenizer; the `Detailed` variant's third return flags a normalised (unreliable) head | **Survives**; D3's matcher MUST use `shellCommandHeadDetailed` and refuse to satisfy an `allow` rule from a normalised head (FR-040) — POSIX operator set only, does not model PowerShell (FR-041) |
| `Evaluator.EvaluateExec`/`MatchGlob`/`FirstToken` (`policy/evaluator.go`) | opt-in exec allowlist | **Deleted, folded into D3** |
| `PolicyAuditor.logDecision` (`policy/auditor.go`) | unexported, one caller, 4-string signature, `sessionID` fixed at construction | **Survives for D3's own retarget only**; the four NEW events (FR-046) route via `ExecTool.emitAudit`'s `audit.Entry.Details map[string]any` instead — `logDecision` has no field for mode/level/scope |
| `ExecApprovalManager` et al. (`security/execapproval.go`) | dead manager behind issue #83 headline | **Deleted** (D5) |
| `ApprovalGrantStore` (`security/approvalgrants.go`) | session-scoped exact-fingerprint store | **Survives, extended** — prefix, path-widening (`PathGrants`), and network-widening (`ConnectPortRules`) record kinds |
| `ToolApprovalModal` / `ToolApprovalModal.resolution.test.tsx` | 4-button dialog; `cancel` and `deny` verified to behave **differently** on purpose (network-failure vs 404 dismissal) | *[corrected 2026-09-23]* Buttons relabelled Approve Once / Deny / Always Allow / Cancel, scope radio added for `bash`; `cancel` stays in the wire enum and the Cancel button is still rendered (FR-023) |
| `GodModeActiveBanner` (`settings/GodModeControl.tsx`) | already exists, already red, already has a fetch-failure-safe `isError` variant, rendered today at `GatewaySection.tsx:298` | **Relocated** to `AppShell` app-wide — a move, not a new build (FR-034). *[superseded 2026-09-25, founder decision — the banner is deleted; see FR-034's amendment]* |
| `SandboxStatus.yaml` | existing schema: `backend`, `available`, `kernel_level`, `policy_applied`, `abi_version` | **Extended** with `auto_approve_effective`, `kernel_sandbox_active`, `god_mode_active` (*[corrected 2026-09-23]*, no `effective_mode`) — the badge's reuse target, not a new schema (FR-033) |
| `applyShellPolicy` (`sysagent/tools/agent_apply_args.go`); `rest_agents_update.go`; `rest_agents_create.go`; `openclaw/openclaw_config.go`(+test) | four live surfaces referencing `AgentShellPolicy`/`ShellDenyPatterns` missed by the first removal pass | **Removal tasks added** (R-1a…R-1d, §7.1) |

**Impact assessment** (grep-verified caller counts):

| Symbol removed/modified | Risk | Callers that MUST be updated |
|---|---|---|
| `defaultDenyPatterns`/`secretGuardPatterns` | MEDIUM | `NewExecToolWithConfig`; `applyDenyPatterns` (2 callers, `shell_path_guard.go`) |
| `Evaluator.EvaluateExec`/`MatchGlob`/`FirstToken` | LOW (contained; `EvaluateExec` ignores its own `agentID` param) | `PolicyAuditor.EvaluateExec`→`shell.go`; `loop_construct.go`; `rest_exec.go` |
| `ExecApprovalManager` | LOW (dead) | none |
| `AgentShellPolicy`/`ShellDenyPatterns`/`ExecConfig.AllowedBinaries` | MEDIUM–HIGH | `loop_wire.go`, `sandbox_config_validation.go`, `rest_sandbox_config.go`, `rest_exec.go`, `rest_agents_update.go`, `rest_agents_create.go`, `agent_apply_args.go`, `openclaw_config.go`, config load |

---

## 3. Functional requirements

### 3.1 Modes, storage, and authorization

**FR-001 — Three presented states, Auto-approve as a separate switch** *[corrected 2026-09-23; further corrected 2026-09-24, founder decision]*. The chat is presented as Ask, Auto or God Mode; this is not a stored mode. `bash` keeps an ordinary tool policy, shipped `ask`. Auto-approve is a separate boolean at three scopes: `sandbox.auto_approve` (global, seeded `true`), `AgentConfig.AutoApproveDisabled` / wire `auto_approve_disabled` (per agent, off-only), and the session-keyed per-chat modifier (may turn Auto on or off for that chat). Resolved by `sessionmode.go::ResolveAutoApprove`: chat modifier if set, else the global default turned off by the agent's switch. A call is in Auto only when its tool resolved to `ask`, Auto-approve is on, and God Mode is off. **As of 2026-09-24, an enforcing kernel sandbox is no longer part of this predicate** — the original text here required one and presented "Auto → Ask" when it was absent; that fourth condition and that presented state are both gone (see ADR-092's 2026-09-24 revision note; supersedes FR-008 below). God Mode is the existing separate global flag. `config.ReconcileToolPolicyCeiling`/`resolveEffectivePolicyWith` unchanged.

**FR-002 — Three-level merge.** Global → per-agent → per-chat. *[corrected 2026-09-23]* The per-agent level can only turn Auto off. The per-chat level may loosen for its own chat (a human is present), but is never copied onto a delegate whose own `auto_approve_disabled` is set.

**FR-003 — Server-side loosening rejection, one validator, four writers.** The write handler re-reads the global default and rejects a loosening write with 4xx. **Per-agent config has four writers, all four must call the same tighten-only validator**, not just "the REST write handler": `rest_agents_update.go` (field merge), `rest_agents_create.go` (create-path decode), `pkg/sysagent/tools/agent_apply_args.go` (agent-callable), and the per-chat write path. The sysagent path is the one that matters most: an agent reaching a mode-write surface is bounded by this check, not by the tool being absent.

**FR-004 — Session-scoped modifier, not a third policy layer.** Per-chat quick switch applied after the ADR-077 merge; no `chat_id` key on either policy map (Hard Constraint #6); new, separate, session-keyed state (`pkg/agent/sessionmode.go`).

**FR-005 — Delegation inheritance.** Session modifier inherits to a delegate via `ApprovalGrantStore.InheritFrom`; delegate resolves the tightest of (parent modifier, own override).

**FR-006 — Mid-turn and restart semantics.** A command is resolved against the mode in force when `resolveToolPolicyAtExec` (the **existing** TOCTOU re-check — unchanged, and this FR does not duplicate or bypass it) last evaluated it; a tighten landing after does not retroactively change that verdict. Modifier clears on restart with the session.

**FR-007 — Platform predicate, one derivation.** Linux: Landlock enforce, not degraded. macOS: Seatbelt backend active/enabled — **reads Seatbelt's own state directly, not `SandboxStatus.policy_applied`**, which reports gateway self-confinement and is documented false on macOS by design. Windows: never.

**FR-008 — Auto→Ask fallback [SUPERSEDED 2026-09-24, founder decision].** Originally: no active kernel sandbox ⇒ Auto behaves identically to Ask. **This is no longer true.** Auto now applies with no active kernel sandbox exactly as it does with one; the D7 filesystem pre-flight, the D8 network pre-flight, and the surviving text-based guards (`guardCommand`, `substitutionGuard`, D3 rules) are the only checks in that case. See ADR-092's 2026-09-24 revision note for the accepted risk this ruling states plainly. This FR ID is retained (not renumbered) so cross-references and the removal-inventory grep stay stable; its content is the historical rule, corrected here.

**FR-045 — Global mode write requires the password step-up.** Because global mode lives inside `cfg.Sandbox.*` (FR-001), its write MUST go through the existing `rest_sandbox_config.go` PUT handler, inheriting its `requireReAuth` gate — the same step-up God Mode and credential writes already use. No new auth mechanism; the requirement is routing through the existing gated handler. Per-agent/per-chat writes need no step-up (tighten-only). The agent-facing `set_config` tool already blocks the whole `sandbox.*` subtree by ancestor closure (`pkg/sysagent/tools/config.go::blockedConfigKeys`) — confirm the new mode field lives under `sandbox.*` so this coverage is automatic; if implementation places it elsewhere, `blockedConfigKeys` needs an explicit new entry. **Accepted, pre-existing, not fixed here:** on Windows, `config.json` is not kernel-denied to a spawned child, so an agent's own `bash` tool can write it directly, bypassing both `set_config` and the step-up — documented, Linux/macOS-only guarantee.

### 3.2 Filesystem pre-flight (D7) — data model, scope, and honest gaps

**FR-009 — Pre-flight, never start-then-interrupt.** Unchanged from the first draft.

**FR-010 — One policy value, not two implementations.** Unchanged.

**FR-011 — Widening re-resolves the single value, bash-scoped.** When approved, the widened value is re-resolved and both the app-layer guard and `DeriveKernelPolicy` are rebuilt from it before spawn. **The widening applies only to the bash tool's own resolution** (FR-036) — it does not propagate to `edit`, `write_file`, `grep`, `send_file`, `web_serve`, `request_mount`, or the browser tools' independent `ResolveTurnFSPolicy` calls.

**FR-012 — Anti-drift lock test, two levels.** Level 1 (unchanged): a `{path, operation}` matrix vs. `DeriveKernelPolicy`'s rendering, including the widened-value half (non-empty `PathGrants`), cross-platform Go-level. **Level 2 (new — resolves the "tests only the rendering, not the risky half" gap):** a separate, second test asserts that the pre-flight's own command-text→`{path, operation}` extraction (the FR-038 classifier) agrees with what a human-authored oracle expects for the §5.2/§5.4 test-data rows — the rendering-agreement test alone does not establish agreement with what Landlock actually enforces (`SandboxPolicy.DeniedPaths` is honoured differently on macOS vs. Linux, per `pkg/sandbox/sandbox.go`), so SC-002's claim is scoped to what each level actually proves.

**FR-013 — Missed-denial behaviour, corrected claim.** A kernel denial the pre-flight missed fails the command with **the child's own OS error** (e.g. a bare permission denial). Omnipus does **not** claim to diagnose it as specifically a sandbox denial — a Landlock/Seatbelt denial is not a structured, observable event to the gateway (the same reason runtime-catch was rejected, ADR Alternatives). Never silently allowed, never silently retried unconstrained.

**FR-014 — No-sandbox platforms defer to Ask.** Unchanged.

**FR-015 — Network is NOT out of scope; D8 covers it.** *(Corrects the first draft, which declared network out of scope entirely — that left nine command categories with no compensating control, B-11.)* Filesystem pre-flight (D7) and network pre-flight (D8, §3.9) are two independent evaluators over two independent resources; neither substitutes for the other. A command needing only network (no filesystem escalation) is evaluated by D8 alone.

**FR-016 — Path-widening grant shape, secret-set excluded.** `{path, operation}`, `operation ∈ {read, write, read+write}`, exactly the access class found missing — **and never a path in `CarveOuts`/the secret set (FR-037)**.

**FR-017 — Session-scoped, recorded.** Unchanged.

**FR-036 — `PathGrants` data model (resolves B-10).** `fspolicy.FSPolicy` gains `PathGrants []PathGrant{Path string; Access uint64}`, reusing `sandbox.AccessRead|AccessWrite|AccessExecute` — not a new vocabulary. `DeriveKernelPolicy` renders each as one additional `PathRule{Path, Access}`, additive to the `WorkDir`/`AllowedRoots` rendering. `guardCommand`'s scan checks `PathGrants` the same way it checks `AllowedRoots`. **Tool scope:** `ResolveTurnFSPolicy` gains an optional grant-overlay parameter populated ONLY by the bash pre-flight/exec call sites from `ApprovalGrantStore`; the other eleven callers (`edit.go`, `filesystem.go` ×3, `grep.go`, `send_file.go`, `web_serve.go` ×2, `request_mount.go`, `browser/tools.go`, `browser/tools_interact.go`) pass none.

**FR-037 — Secret set is never widenable.** An escalation naming a `CarveOuts`/secret-set path is **refused outright, never prompted**. This is asserted as a requirement, not left to `CarveOuts`' existing "always denied" behaviour to happen to also cover it.

**FR-038 — Operation classifier.** The pre-flight's read/write/read+write classification reuses `pathUseVerdict`/`newPathUseClassifier`, extended from its current `{readOnly, exec, reason}` three-way to the `{read, write, read+write}` shape FR-016 needs. Redirections (`>`, `>>`), `tee`, `dd of=`, `mv`, `cp` (source read, dest write) are explicit classifier cases (§5.4 dataset).

**FR-047 — Concurrency.** A path/network widening approved while another command is in flight applies to subsequent spawns only — an already-spawned child's kernel ruleset cannot be widened retroactively. A mode tighten landing mid-turn does not retroactively change an in-flight command's resolved verdict (restates FR-006 for the concurrent case). Duplicate approval submissions for the same pending call are idempotent (second submission is a no-op, not a second grant).

**FR-048 — Denied escalation refuses outright.** Denying an Auto filesystem or network escalation refuses the command; it does not run un-widened and does not retry.

### 3.3 (renumbered from original 3.4) Unified rule format and decision order

**FR-018 — One rule shape, config-file-only.** Unchanged.

**FR-019 — Decision order, corrected reachability (resolves B-1).** Deny beats ask beats allow, specificity-blind, in every mode. **Grant consultation is reachable from three sites, not one:** (1) the existing `ask`-policy path (`resolveAskPolicy`, unchanged); (2) a D3 `ask`-rule verdict (new caller); (3) a D7/D8 pre-flight escalation (new caller). An `allow` ceiling verdict with no D3 `ask` rule and no pre-flight escalation still never touches the grant store — that is unchanged, correct behaviour, not a regression. Today's Ask-mode "Always Allow" (exact-fingerprint grant suppressing a repeat prompt) is **preserved** via site (1); it is not removed by this ADR.

**FR-039 — `CheckGrantOrRequestApproval` gains two callers.** The D3 `ask`-rule path and the pre-flight-escalation path both consult the grant store (via the same function or a shared helper) **before** showing a dialog, exactly as the existing `ask`-policy path does. A matching grant suppresses the prompt on all three paths identically.

**FR-020 — Per-segment matching, blind spots route to `ask`.** Chained commands split via `splitShellSegments`/`shellCommandHead` — no new parser. Each segment must independently clear or the whole call asks. **The splitter's known blind spots — a quote-blind over-split (`echo "a;b"`), no redirection-as-separator handling, no brace-expansion descent — each route the affected segment to `ask`, never to a resolved head** (§5.3 rows R12–R14).

**FR-040 — Resolve-and-verify, not rewrite (resolves B-3/B-12).** `buildShellArgv` execs `["sh","-c",command]` (POSIX) / `["powershell","-NoProfile","-NonInteractive","-Command",command]` (Windows) — `argv[0]` is always `sh`/`powershell`; there is no user-binary argv slot to rewrite, and rewriting the `-c` string is attacker-influenced text surgery. Instead: **the matcher resolves a segment's leading token to its actual executable against the child's effective PATH/env, matches rules against the resolved absolute path, and re-verifies that resolution immediately before spawn. Command text is never rewritten.** The matcher MUST use `shellCommandHeadDetailed`, not `shellCommandHead` — a head whose `normalised` flag is true (e.g. `./cat`, `CAT`, a stripped `/usr/bin/` prefix) cannot satisfy an `allow` rule; rule matching is case-sensitive on the resolved path. The TOCTOU window between resolve and exec is the kernel sandbox's boundary, stated explicitly, not the rule engine's.

**FR-041 — Windows: exact-command grants only.** `splitShellSegments` is a POSIX operator set (`| ; & \n \r`) that does not model PowerShell grammar, and there is no `argv[0]` slot on Windows either. On Windows, D3/D4 grants and rules are **exact-command match only** — no prefix option, no chained-segment splitting. Documented platform limitation, not partial POSIX behaviour.

**FR-022 — Exec allowlist folded in.** Unchanged; `logDecision` retarget is D3's own audit write and is distinct from the FR-046 event routing.

### 3.9 Network pre-flight (D8, new — resolves B-4/B-11)

**FR-042 — Auto denies bash's outbound network by default.** For the bash tool's rendered per-turn policy under Auto (not other tools, not God Mode), `ConnectPortRules`/`BindPortRules` render **empty** instead of the boot-default `DefaultConnectPorts={53,80,443}`. On Linux, Landlock ABI≥4 installs `handledAccessNet` unconditionally (`sandbox_linux.go`, verified — not conditioned on the rule list being non-empty), so an empty list is a true kernel-enforced deny-all for that child's `connect(2)`. macOS: the Seatbelt profile renders `ConnectPortRules` identically (`seatbelt_profile.go`) — same mechanism. Windows *[corrected 2026-09-25 — supersedes the original "D1 already makes Auto behave as Ask there; FR-042 adds nothing and removes nothing on Windows," stale since the 2026-09-24 founder decision]*: since Auto no longer requires an enforcing kernel sandbox, Windows now runs under Auto like every other platform, and D8's classifier is the only containment for outbound network on Windows — there is no kernel `connect()` deny-all sitting behind it the way Linux/macOS have.

**FR-043 — Network-need classifier.** A pre-flight sibling to FR-038, reusing D3's resolved-binary step: flags a command via (a) a curated set of network-capable binaries (*[corrected 2026-09-23]* fixed in `preflight.go::networkCapableBinaries`, no operator extension built) (`git`, `curl`, `wget`, `ssh`, `scp`, `rsync`, `npm`/`pnpm`/`yarn`, `pip`, `apt`/`yum`/`dnf`, `docker`, `gh`, cloud CLIs) or (b) a literal `http(s)://` token. A flagged command escalates before spawn, identically to FR-009's filesystem prompt.

**FR-044 — Network grant, widening, honest gap.** Approving widens the session's `ConnectPortRules` to `DefaultConnectPorts` — **port-level, not domain-level** (Landlock `NET_CONNECT_TCP` cannot filter by host; CIDR/host filtering for Omnipus's own HTTP clients remains the unchanged `SSRFChecker`/`ExecProxy`, which a bash-spawned binary can bypass by ignoring its proxy env vars — pre-existing, documented, not new here). New `network` grant kind in `ApprovalGrantStore`, same session/delegate/clear-on-close lifetime as `PathGrants`. **Honest gap, symmetric to FR-013:** a command the classifier misses but that opens a raw socket is denied by the kernel at `connect()` time (fails with a clear error), never silently allowed.

**FR-049 — What D8 does not cover.** `shutdown`/`reboot`/`poweroff`, `kill`/`pkill`/`killall`, the fork bomb, `sudo`, `chmod`/`chown`, `eval`, `source *.sh` are not network operations; D8 is silent on them, same as D2. This is the accepted residual risk named once in the ADR, not re-litigated per category here.

### 3.10 Auto-approve for tools other than `bash` (ADR-092 D9, founder-ruled 2026-09-23)

*Implemented; FR-051 to FR-062 and S54 to S64 checked against `feat/adr-092-shell-permissions` @ `5d6515d0c` (2026-09-24).*

Source: [`adr-092-auto-for-other-tools-design.md`](adr-092-auto-for-other-tools-design.md) (revision 3) and the founder's `auto-approve-choices.json` (2026-09-23T14:20:18Z). Expected values in tests come from those two documents, never from the implementation.

**FR-051 — One Auto predicate for every tool** *[corrected 2026-09-24, founder decision — supersedes J13]*. Auto is active for a call exactly when God Mode is off and `ResolveAutoApprove(cfg, agentID, chatModifier)` is true. `sandbox.TurnPolicyBaseInstalled()` is **no longer** part of this predicate — see ADR-092's 2026-09-24 revision note. `bash` and every other tool read the same predicate (`autoApproveActive`, extracted from `ShellPermissionGate.liveMode`). No Auto in God Mode; Auto now applies on Windows and without an enforcing kernel sandbox, governed by the D7/D8 pre-flights and the surviving text-based guards instead.

**FR-052 — Rule for non-bash tools.** A non-bash call whose effective execution-time policy is `ask` runs with no prompt and no grant when Auto is active (FR-051) and the classifier verdict is Run: the tool is RUNS; or RUNS-IF and its arguments meet the condition; or an MCP tool meeting FR-056. Any classifier error counts as "asks". Auto never changes an `allow` or `deny` result.

**FR-053 — Classification table.** One entry per catalog tool, transcribed from the founder file: 74 RUNS, 7 RUNS-IF (`read_file`, `list_directory`, `write_file`, `edit_file`, `append_file`, `send_file`, `browser_screenshot`), 28 ASKS, `bash` excluded (its own D1–D8 mechanism). The ASKS zero value is the lookup-miss default. The 28: `request_mount`, `install_skill`, `environment_setup`, `serve_web`, `send_email`, `reply`, `delete_task`, `browser_evaluate`, `browser_upload_file`, `set_config`, `run_doctor`, `configure_provider`, `test_provider`, `enable_channel`, `disable_channel`, `configure_channel`, `test_channel`, `add_mcp_server`, `remove_mcp_server`, `create_agent`, `update_agent`, `delete_agent`, `update_workspace`, `delete_workspace`, `delete_task_in_workspace`, `create_skill`, `edit_skill`, `remove_skill`.

**FR-054 — Workspace path rule (J2).** A RUNS-IF path argument is inside only when the tool's own `ResolveTurnFSPolicy` (no grant overlay) and `ResolvePath`/`ResolvePathAllowingPatterns` resolve it outside the secret set and within `WorkDir` or an `AllowedRoots` mount (`fspolicy.CoversForGrant`), for reads and writes alike. A `bash` path grant (FR-036) never widens it. `send_file` applies the read rule to the file it sends; `browser_screenshot` applies the write rule to its destination, a generated name in the work folder (*[corrected at implementation]* the tool has no `filename` argument).

**FR-055 — Pin and re-check.** The verdict is pinned on the call context (`WithAutoApproved`); each RUNS-IF file tool re-checks its resolved real path (`RecheckAutoPin`) and **refuses** — never re-prompts — when the path no longer passes FR-054. A mid-turn Auto toggle does not alter an already-decided call (as FR-006).

**FR-056 — MCP tools (J15).** Under Auto an MCP tool on `ask` runs only when `Annotations != nil` and (`ReadOnlyHint` is true, or `DestructiveHint` is non-nil and false). Every other case asks, including no annotations and a nil `DestructiveHint`. No operator override. ADR-090 server/tool assignment still applies. No classification-table key may start with `mcp_`.

**FR-057 — Unattended runs (J1).** The `AutoDenyAsk` block in `resolveAskPolicy` moves after the approval checks: a call Auto runs in a chat also runs unattended; anything needing a human (ASKS tool, failed RUNS-IF condition, unlabelled MCP tool, a `bash` escalation) is auto-denied with `autoDenyHeadlessReason` and its existing audit rows. This also makes `bash` under Auto run in scheduled runs.

**FR-058 — Grants.** Order: Auto verdict, then grant store, then prompt. An Auto-run call records and reads no grant. Approve Once records nothing; Always Allow records the exact-arguments grant. The prefix-scope radio is `bash`-only. Delegation: grants and the per-chat modifier inherit, but the modifier is never copied onto a delegate whose own `auto_approve_disabled` is set, and the shared predicate (`autoApproveActive`) refuses Auto outright for a delegated sub-turn whose agent carries it, for every tool.

**FR-059 — Audit.** New event `tool.auto_approved`, decision `allow`, one per Auto-run call, via `audit.EmitEntry`; agent and session are entry fields, details are `{tool, class, reason, paths}` with `class` one of `runs`, `runs_if_args`, `mcp_not_destructive`. Prompted or denied calls emit no `tool.auto_approved`.

**FR-060 — Per-tool verdict on the wire and in the UI (J14).** `ToolRegistryEntry.auto_approve: enum[runs, runs_if_args, asks]`, contract-first (Hard Constraint #8), filled from the classification table in `rest_tool_registry.go`; MCP entries report `runs` or `asks` from FR-056. A read-only marker appears only on rows whose effective policy is Ask, rendered by the shared `ToolPolicyEditor.tsx` (used by `ToolsAndPermissions.tsx` and the global tool-policy table), labelled exactly **Auto: runs**, **Auto: runs inside workspace**, **Auto: asks**; no marker for `bash` or an unclassified tool. The generated `docs/reference/built-in-tools.md` gains an "Under Auto" column from the same table.

**FR-061 — One dialog for a `bash` ask rule.** With `bash` resolved to plain `ask` and a genuine operator `{action: ask}` rule match, the D3 verdict is settled inside the single upfront prompt (request carries `adr092_kind: "rule_ask"` and a `note` naming the matched rule); `enforceShellPermissionMode` skips `requestRuleApproval` when `tools.WithRuleAskSettled` is pinned. D3 `deny` and D7/D8 still apply. Headless: auto-denied once. "Always Allow" on that prompt records an exact grant over the request arguments, so the identical command does not prompt again in that chat (found by `CheckGrantOrRequestApproval`); "Approve Once" records nothing.

**FR-062 — Drift guard.** `pkg/gateway/auto_approve_classification_test.go` against `buildCentralBuiltinRegistry`: every registry tool has an explicit entry; no stale keys (except `bash`); every global-ceiling key has an entry; every RUNS-IF tool implements `AutoApproveClassifier`; `AutoApproveClassOf("no_such_tool") == AutoAsks` and ASKS is the zero value; a golden copy of the 28-name ask-list equals the table's ASKS set; no `mcp_` keys. Each check carries a mutation self-check.

### 3.5 Approval dialog and suggested prefix

**FR-023 — Buttons; `cancel` stays in the wire enum (resolves R-a).** *[corrected 2026-09-23]* As built the dialog shows **Approve Once** (`allow_once`, no grant), **Deny** (`deny`), **Always Allow** (`allow`, session grant) and a rendered **Cancel** (`cancel`); the planned three-button layout without Cancel was not implemented. **`ToolApprovalActionRequest.action`'s enum keeps `cancel`** as a client-issued (not button-issued) resolution value — `ToolApprovalModal.resolution.test.tsx` verifies `deny` (network failure, leaves the approval unresolved so a later snapshot can restore it) and `cancel` (lost-server 404, resolves it locally) behave *differently on purpose*; the headless CLI path (`pkg/app/internal/run/run.go`) drives the same enum and would also break if `cancel` were removed. Enum becomes `deny | allow_once | allow | cancel` (four values; three surfaced as buttons, `cancel` reserved for the stuck-approval recovery path).

**FR-024 — Scope choice, token-boundary matched.** Unchanged from first draft (exact default; prefix ignores `cwd`; token-boundary — `npm run test` does not match `npm run testfoo`).

**FR-025 — Per-segment grants.** Unchanged.

**FR-026 — Suggested-prefix algorithm, pinned stop tokens (resolves R-e/M-20).** Stop at the first flag/path-arg/URL token; env-assignment stripped before deriving; known wrapper (`sudo`, `env`, `timeout`, `xargs`, `sh -c`) is the prefix's start, not stripped. **Pinned:** the prefix stops at the wrapper's first argument — `env X=1 cmd` → `env` (P7); `timeout 5 npm test` → `timeout` (P10); `VAR=value echo hi` → `echo hi` (assignment stripped, no further stop token, P14). This is the narrowest, most defensible reading; if the founder wants the looser (whole-wrapper-plus-args) reading later, it is a config default to change, not a re-spec.

**FR-027 — Per-segment dialog display.** Unchanged.

**FR-028 — No grants list; audit is the compensating visibility (closes m-5 loosely).** Session grants are not shown, listed, or revocable. The audit log (FR-046) is the deliberate compensating visibility for this non-feature — stated once here so it reads as a considered trade, not merely an absence.

### 3.6 God Mode

**FR-029, FR-030, FR-031 — Unchanged from first draft.**

**FR-050 — Mode-sensitive path-guard disposition, three branches (resolves R-g).** `guardCommand`'s path-containment denial has a three-branch disposition, not a single "survives" claim: **Ask** hard-denies exactly as today; **Auto** surfaces the denial as the FR-009 pre-flight escalation; **God Mode** hard-denies with no prompt (no approvals exist there — God Mode does not skip the guard, it just never offers to widen).

### 3.7 Audit and status contract

**FR-032 — Four audit events. Renumbered routing (corrects the first draft's `logDecision` claim).**
(a) mode change — new mode, level, actor; (b) pre-flight escalation (filesystem **or** network, FR-009/FR-042) — rule/path or classifier match that tripped, operation/port requested; (c) grant recorded — scope (exact/prefix/path-widening/network-widening), lifetime (session), resolved binary; (d) approval decision — allow-once/allow-with-grant/deny, per call. **Actor taxonomy** for (a): `operator` (human via Settings), `agent` (a tool call — always refused at write time per FR-045/FR-003, but the *attempt* is still audited), `system` (a config-load reconciliation, if any).

**FR-046 — Routed via `emitAudit`, not `logDecision`.** `PolicyAuditor.logDecision` is unexported, single-caller, and its `(event, agentID, tool, command string, d Decision)` signature has no room for mode/level/scope/path. The four FR-032 events are written via `ExecTool.emitAudit`'s existing `audit.Entry.Details map[string]any` (already used for `cwd`/`god_mode` today) — new `Details` keys per event type, not a `logDecision` signature change. **Redaction posture:** unchanged and deliberate — `audit.Entry.Command` already logs the full command text; the new events add paths/prefixes/ports at the same fidelity, no new redaction requirement.

**FR-033 — Contract-defined status fields, extend `SandboxStatus` (resolves R-b/M-11).** *[corrected 2026-09-23; further corrected 2026-09-24, founder decision]* There is no `effective_mode` field. `SandboxStatus` gained `auto_approve_effective` (the live global default only, not resolved per agent or chat), `kernel_sandbox_active` (`sandbox.TurnPolicyBaseInstalled()`; on macOS true when the Seatbelt boot profile is installed, not `policy_applied`) and `god_mode_active`. The badge folds these with the per-agent and per-chat layers client-side (`useResolvedAutoApprove`): God Mode, else Ask, else Auto. As of 2026-09-24, `kernel_sandbox_active` no longer changes whether the badge reads Ask or Auto — there is no more "Auto → Ask" state. When Auto is on and `kernel_sandbox_active` is false, the badge instead reads "Auto — no sandbox" with a warning tooltip (frontend lane owns the exact string).

**FR-034 — God Mode banner is a relocation, not a build (resolves M-12).** `GodModeActiveBanner` (`GodModeControl.tsx`) already exists, already red, already has a fetch-failure-safe `god-mode-status-unknown-banner` variant, and is already rendered at `GatewaySection.tsx:298`. This FR is: **move it to `AppShell`, app-wide**; preserve the fetch-failure `isError` state exactly (a naive move risks dropping it); correct its body text, which today says the "shell guard" is disabled — stale even before D6, doubly stale after (D6/FR-031 already corrects the Go comment; this FR extends that correction to this UI string). *[Amended 2026-09-25, founder decision: the app-wide banner is **removed**. In its place: a small red "God Mode" pill in the sidebar brand row (`layout/GodModeIndicators.tsx` — catalogued Badge, `error` variant, Phosphor `ShieldWarning`, click navigates to Settings → Gateway at the God Mode control), a red dot on the sidebar-open hamburger while the sidebar is hidden, and the existing chat-header `ChatModeBadge` and Settings → Gateway `GodModeControl`, both unchanged. The fetch-failure safety property survives: `useGodModeLiveStatus` keeps the unknown state (pill warning variant reading "God Mode ?", `sidebar-god-mode-unknown`); under `dev_mode_bypass` the pill renders nothing, as the banner did.]*

### 3.8 Removal

**FR-035 — Complete removal, no shims.** Unchanged; each removal carries a CI guard.

---

## 4. BDD scenarios

Grouped by FR; each carries `Traces to:`. Scenarios unchanged from the first draft are listed by ID only where their text is unchanged; changed/new scenarios are given in full.

### 4.1 Modes (S1–S10 unchanged: fresh-install default, global/per-agent/per-chat tighten, loosening-rejected ×2, UI never offers looser, delegation ×2, session dies with restart)

**S43 — Sysagent tool cannot loosen an agent's mode** *(Error Path, resolves FR-003's fourth writer)*
- **Given** an agent calls `set_config`/`agent_apply_args` attempting to set its own mode to Auto under global Ask, **When** the write is evaluated, **Then** it is rejected the same way the REST handler rejects it (one validator, four writers).
- *Traces to:* FR-003.

**S44 — Global mode write requires re-auth** *(Error Path)*
- **Given** an operator session with no fresh re-auth token, **When** the global mode is changed to a looser value, **Then** the write is refused pending `requireReAuth`, identically to a God Mode toggle.
- *Traces to:* FR-045.

### 4.2 Platform predicate (S11–S14 unchanged, with S13 now citing FR-007's corrected macOS predicate wording). *[flagged 2026-09-24, founder decision]* Any of S11–S14 that asserted "no active kernel sandbox ⇒ Auto behaves like Ask" (FR-008) now assert the opposite — see FR-008's 2026-09-24 correction above and S57/S57b for the D9-side equivalent; whoever next touches this section should locate and rewrite that scenario's assertion, not merely its citation.

### 4.3 Filesystem pre-flight

**S15, S16 unchanged.**

**S17 unchanged** (approval widens `{P, write}`, second identical command does not re-prompt).

**S18 — REPLACED (resolves M-6: the original precondition is unreachable).** *Reads are open outside the secret set for every non-`ReadConfined` agent (§5.2 W4, ADR-063 D2) — a `{P, read}` widening can never be produced for an ordinary agent, so a test that grants read then expects a later write to re-prompt could never fail.* New:
**S18 — Widening is access-exact for a `ReadConfined` agent** *(Error Path)*
- **Given** a `ReadConfined` agent (e.g. Judge/System) is granted `{P, read}`, **When** the same agent later needs `{P, write}`, **Then** it prompts again — the grant was read-only.
- *Traces to:* FR-016, FR-038.

**S19 unchanged.**

**S20 unchanged**, now explicitly cross-referencing FR-013's corrected "child's own OS error" claim.

**S21 unchanged** as Level-1 lock-test scenario; **S21b — new, Level 2 (resolves R-c/M-15):**
**S21b — Classifier self-check catches a wrong extraction**
- **Given** a command whose known-correct `{path, operation}` set is `{(/etc/foo, write)}`, **When** the FR-038 classifier is deliberately fed a mutated version that extracts `{(/etc/foo, read)}`, **Then** the Level-2 test fails — proving the classifier's own text→`{path,op}` step is checked, not only the rendering comparator.
- *Traces to:* FR-012, FR-038.

**S45 — Denied filesystem escalation refuses outright** *(Error Path)*
- **Given** an Auto escalation for `{P, write}`, **When** the user clicks Deny, **Then** the command does not run at all — not un-widened, not retried.
- *Traces to:* FR-048.

**S46 — Secret-set path is refused, never prompted** *(Error Path — mandatory adversarial case)*
- **Given** a command whose write target resolves into `CarveOuts` (e.g. `master.key`), **When** pre-flight evaluates it, **Then** the command is refused directly — no escalation prompt is ever shown for this path.
- *Traces to:* FR-037.

**S47 — Delegate inherits a path widening** *(Error Path — mandatory adversarial case)*
- **Given** a parent session was granted `{P, write}` for bash, **When** the parent delegates to a subagent, **Then** the subagent's bash calls also resolve the widened policy (same `InheritFrom` mechanism as command grants) — the user should understand `Allow` on a bash escalation extends to delegates, not just the visible chat.
- *Traces to:* FR-017, FR-005.

### 4.4 Rule format and decision order

**S22, S23, S25, S26 unchanged.**

**S24 — REPLACED wording (mechanism corrected, same intent).**
**S24 — Look-alike binary fails to auto-approve**
- **Given** an `allow` rule for `git`, and an attacker-placed executable named `git` earlier on `PATH`, **When** `git evil-script` is evaluated, **Then** the resolved absolute path is the look-alike, matched via `shellCommandHeadDetailed` (not the normalised head), and does not match the rule written against the real `git`.
- *Traces to:* FR-040.

**S27 unchanged**, its own comment corrected: this is the *preserved* behaviour FR-019 restates, not a change.

**S48 — D3 `ask` rule consults the grant store** *(Happy Path, resolves B-1)*
- **Given** a D3 `ask` rule on `git push` and a matching session grant already recorded for `git push`, **When** `git push` runs again, **Then** the grant suppresses the prompt (FR-039's second call site) — grants are prompt-keyed, not policy-verdict-keyed.
- *Traces to:* FR-019, FR-039.

**S49 — Windows: no prefix, no chained split** *(Alternate Path)*
- **Given** Windows, **When** a compound PowerShell command is evaluated, **Then** it is matched as one exact unit — no segment split, no prefix radio offered in the dialog.
- *Traces to:* FR-041.

### 4.5 Approval dialog (S28–S37 unchanged, S36 corrected to note `cancel` survives as a non-button wire value)

### 4.6 Audit and status (S38–S42 unchanged, S38/S39 now citing FR-046's `emitAudit` routing)

### 4.9 Network pre-flight (new)

**S50 — Network-flagged command escalates before running** *(Happy Path)*
- **Given** Auto with an active kernel sandbox, **When** `curl https://example.com` is evaluated, **Then** the classifier flags it, an escalation prompt shows before any process spawns.
- *Traces to:* FR-042, FR-043.

**S51 — Approval widens network for the session** *(Happy Path)*
- **Given** the user approves the S50 escalation, **When** the same command runs again in the session, **Then** it runs without re-prompting (network grant recorded).
- *Traces to:* FR-044.

**S52 — Unclassified binary opening a raw socket is kernel-denied when a kernel sandbox is enforcing** *(Error Path — mandatory adversarial case)*
- **Given** a custom binary not in the classifier's known set, that opens a raw TCP socket, and a kernel sandbox enforcing, **When** it runs in Auto with no prior grant, **Then** the kernel denies the `connect()` (empty `ConnectPortRules`) and the command fails with a clear error — not silently allowed.
- **Given** the same setup with NO kernel sandbox enforcing (Windows, or a sandbox that failed to start) *[added 2026-09-24, founder decision]*, **When** the same binary runs, **Then** there is no kernel `connect()` to deny it — the command's raw socket connects with no further check. This is the accepted risk ADR-092's 2026-09-24 revision note states explicitly, not a defect in this scenario's coverage.
- *Traces to:* FR-044 (honest gap).

**S53 — `git push` prompts via D8, not a name check** *(Happy Path — regression for D2's removed pattern)*
- **Given** the block list is gone, **When** `git push` runs in Auto with no network grant, **Then** it still prompts — because `git` is in the FR-043 classifier's known set, not because a `git push` regex survived.
- *Traces to:* FR-043.

### 4.10 Auto for tools other than `bash` (new, D9)

**S54 — In-workspace file write runs, outside asks** *(Happy + Error Path)*
- **Given** `write_file` on `ask`, Auto on, a kernel sandbox enforcing, **When** the agent writes `notes/a.md`, **Then** it runs with zero approver calls; **When** it writes a Desktop path, **Then** one prompt is shown.
- *Traces to:* FR-052, FR-054. (Design T1.)

**S55 — Read outside the workspace asks** *(Error Path)*
- **Given** the same setup, **When** `read_file /etc/hosts` is called, **Then** it prompts, while `bash` `cat /etc/hosts` under Auto runs (accepted asymmetry).
- *Traces to:* FR-054. (Design T2.)

**S56 — Every ask-list tool still prompts** *(Error Path, table-driven over the 28 golden names)*
- *Traces to:* FR-053. (Design T3.)

**S57 — No Auto when Auto is off or in God Mode** *(Alternate Path)* *[rewritten 2026-09-24, founder decision — the original title and scenario said "No Auto without a kernel sandbox"; that case is now the OPPOSITE outcome, see S57b]*
- **Given** `delegate`, `send_message`, `knowledge_edit` on `ask`, **When** Auto is off, or God Mode is on with an agent-level `ask`, **Then** each prompts.
- *Traces to:* FR-051. (Design T5, T14.)

**S57b — Auto on with no kernel sandbox still runs RUNS tools without a prompt** *(Happy Path)* *[added 2026-09-24, founder decision — replaces S57's old "no kernel sandbox ⇒ prompts" coverage]*
- **Given** `delegate`, `send_message`, `knowledge_edit` on `ask`, Auto on, and NO kernel sandbox enforcing (Windows, or a sandbox that failed to start), **When** each is called, **Then** each runs with zero approver calls, exactly as it would with a kernel sandbox enforcing — Auto no longer requires one.
- *Traces to:* FR-051 (as corrected 2026-09-24). (Design T5b.)

**S58 — Scheduled run follows the same rule** *(Happy + Error Path)*
- **Given** a headless run with Auto on, **Then** a RUNS tool executes, an ASKS tool is auto-denied with `autoDenyHeadlessReason`, and `bash` under Auto executes.
- *Traces to:* FR-057. (Design T8.)

**S59 — MCP annotations** *(Alternate Path)*
- `readOnlyHint:true` runs; `destructiveHint:false` runs; `destructiveHint:true` asks; no annotations asks; annotations present with `destructiveHint` nil and `readOnlyHint` false asks.
- *Traces to:* FR-056. (Design T9.)

**S60 — Symlink swap after the verdict is refused** *(Error Path — mandatory adversarial case)*
- **Given** the classifier approved `a.md`, **When** it is swapped to a symlink pointing outside before dispatch, **Then** the tool refuses and nothing is written.
- *Traces to:* FR-055. (Design T6.)

**S61 — A delegate's own off-switch wins** *(Error Path)*
- **Given** a parent chat with Auto on and a delegate with `auto_approve_disabled`, **When** the delegate calls `write_file` inside its workspace, **Then** it prompts.
- *Traces to:* FR-058. (Design T15.)

**S62 — One dialog for a `bash` ask rule** *(Happy Path)*
- **Given** `bash` on `ask`, mode Ask, a command matching an operator `ask` rule, **Then** the approver is called exactly once with `adr092_kind: "rule_ask"`.
- *Traces to:* FR-061. (Design T18.)

**S63 — Auto-run calls are audited and grant nothing** *(Happy Path)*
- **Then** exactly one `tool.auto_approved` row with the right `class`, and `IsAllowed` stays false afterwards.
- *Traces to:* FR-058, FR-059. (Design T11, T12.)

**S64 — Registry carries the verdict** *(Happy Path)*
- **Then** `GET /api/v1/tools` carries `auto_approve` for every entry, `make verify-contracts` is clean, and markers render only on rows set to Ask.
- *Traces to:* FR-060. (Design T16, T17.)

---

## 5. Test data sets

### 5.1 Suggested-prefix algorithm (P1–P14 as first draft; P7/P10/P14 pinned per FR-026)

| # | Command | Expected prefix | Traces to |
|---|---|---|---|
| P7 | `env X=1 cmd` | `env` | FR-026 |
| P10 | `timeout 5 npm test` | `timeout` | FR-026 |
| P14 | `VAR=value echo hi` | `echo hi` | FR-026 |

(P1–P6, P8, P9, P11–P13 unchanged from the first draft.)

### 5.2 Path widening (W1–W12 as first draft, W9/W10 corrected)

| # | Candidate | Expected verdict | Traces to |
|---|---|---|---|
| W9 | `master.key`/`credentials.json`/`config.json`/`cli.token` | **refused outright, never an escalation prompt** (corrected: not merely "always denied" — FR-037 makes non-widenability an explicit requirement) | FR-037 |
| W10a | Write to `/tmp` itself (the shared node) | denied by `narrowSharedTmpWrite`; escalation | FR-016, `derive_from_fspolicy.go::narrowSharedTmpWrite` |
| W10b | Write to `/tmp/foo` (a path *under* `/tmp`) | **contained — no prompt** (corrected: `narrowSharedTmpWrite` strips write only from the `/tmp` node itself, not paths beneath it; the first draft's single W10 row conflated the two) | FR-016, same symbol |

(W1–W8, W11, W12 unchanged.)

### 5.3 Rule matching (R1–R11 as first draft; R9/R10 reworded, R5 fixed, R12–R14 new)

| # | Input | Expected | Traces to |
|---|---|---|---|
| R5 | `` echo a \| grep b `` | split on `\|` (table cell fixed — the raw pipe broke the original markdown row) | FR-020 |
| R9 | `X=rm; $X -rf /` | **not resolvable to a literal head — routes to `ask`** (reworded: the first draft asserted only what the deleted block list used to do, which passes trivially forever; this asserts what the new engine actually does) | FR-020 (blind spot) |
| R10 | `rm${IFS}-rf /` | same as R9 | FR-020 |
| R12 | `echo "a;b"` (quoted separator) | not over-split; single segment | FR-020 (blind spot) |
| R13 | `> out` (redirection-only segment) | routes to `ask` (no resolvable head) | FR-020 (blind spot) |
| R14 | `{cat,/etc/passwd}` (brace expansion) | routes to `ask`, not resolved as a literal head | FR-020 (blind spot) |

(R1–R4, R6–R8, R11 unchanged.)

### 5.4 Operation classifier (new, FR-038)

| # | Command | Classification | Notes |
|---|---|---|---|
| C1 | `cat /etc/foo` | read | |
| C2 | `echo x > /etc/foo` | write | redirection |
| C3 | `echo x >> /etc/foo` | write | append |
| C4 | `tee /etc/foo` | write | |
| C5 | `cp /etc/foo /tmp/bar` | read `/etc/foo`, write `/tmp/bar` | two paths, two verdicts |
| C6 | `mv /etc/foo /tmp/bar` | read+write `/etc/foo` (removed), write `/tmp/bar` | |
| C7 | `dd if=/etc/foo of=/tmp/bar` | read `/etc/foo`, write `/tmp/bar` | |
| C8 | `rm /etc/foo` | write (delete is a write op on the containing dir + target) | |

### 5.5 Network pre-flight (new, FR-043/FR-044)

| # | Command | Expected | Traces to |
|---|---|---|---|
| N1 | `curl https://x.com` | flagged, escalation | FR-043 |
| N2 | `git push` | flagged, escalation | FR-043 |
| N3 | `ls -la` | not flagged, runs (no network) | FR-043 |
| N4 | `npm run build` (no network access inside) | flagged (binary-based, not behaviour-based — accepted over-flagging per FR-043's honest simplicity) | FR-043 |
| N5 | custom binary raw `socket()+connect()`, not in classifier set | not flagged; kernel denies at `connect()` | FR-044 honest gap |

---

## 6. Contract-first work (Hard Constraint #8)

### 6.1 New / changed wire types — 5-step order unchanged

| Schema file | Change |
|---|---|
| `SandboxStatus.yaml` | **extended** with `auto_approve_effective`, `kernel_sandbox_active`, `god_mode_active` (*[corrected 2026-09-23]* — no `effective_mode`; not a new schema) |
| `ToolApprovalActionRequest.yaml` | `action` enum becomes `deny \| allow_once \| allow \| cancel` (four values — **`cancel` retained**, resolves R-a); add `scope` (`exact`\|`prefix`) when `action == allow` |
| `ToolApprovalResponse.yaml` | echo the new enum + `scope` |
| **`contracts/asyncapi.yaml`'s INLINE `ToolApprovalRequiredFrame` schema (line ~3385)** | **primary edit target (resolves M-9)** — verified: `openapi.yaml` never `$ref`s the standalone `ToolApprovalRequiredFrame.yaml`; `asyncapi.yaml` carries its own inline copy, which is what `scripts/gen-contracts.sh` actually turns into the SPA's Zod types. Add the per-segment command list (resolved binary, args, classification) and `suggested_prefix`/no-prefix-flag here. Keep the standalone file in sync or delete it as a duplicate — a lane task asserts the generated TS type actually gained the fields, not just that the standalone YAML changed |
| `AgentShellPolicy.yaml`, `ExecAllowlist.yaml` | deleted in full |
| `Agent.yaml:153`, `AgentCreateRequestMain.yaml:121`, `AgentCreateRequestSubagent.yaml:117` | **added to the removal list (resolves M-10)** — all three `$ref` `AgentShellPolicy.yaml`; missing them breaks `make gen-contracts`. `AgentUpdateRequest.shell_policy` is **inlined**, not a `$ref` — remove the inline property, not "the reference" |

### 6.2 Endpoints/frames — decided, not left open

1. **Mode-status read:** extend the existing `GET /api/v1/security/sandbox-status` response with the three fields in §6.1 — no new endpoint.
2. **Per-chat modifier write:** a session-scoped WS frame (declared in `asyncapi.yaml`), never written into `config.json` (FR-004).
3. **Global mode write:** the existing `PUT` sandbox-config endpoint (`rest_sandbox_config.go`), gaining an `auto_approve` field (*[corrected 2026-09-23]*, not `mode`) — inherits `requireReAuth` for free (FR-045).

**Config-only, no wire schema:** `command_rules` (FR-018).

---

## 7. Removal work

### 7.1 Block list and deny-pattern config (D2)

**R-1 — Backend block list.** Unchanged from first draft (delete `defaultDenyPatterns` et al.; move `lowerASCII`; delete `shell_secret_guard_test.go`).

**R-1a — `applyShellPolicy` (resolves B-6).** Delete `pkg/sysagent/tools/agent_apply_args.go::applyShellPolicy` and its call site — an **agent-callable** write path. Deleting `AgentShellPolicy` without this is a compile break in an unowned package.
**R-1b — `rest_agents_update.go`.** Remove the `shell_policy` change-tracking (`add(req.ShellPolicy != nil, "shell_policy")`), the custom-pattern regex validation block, and the field-merge block.
**R-1c — `rest_agents_create.go`.** Remove `agentCreateShellPolicyInput`/`agentCreateShellPolicyFromWire` and their call sites.
**R-1d — OpenClaw importer.** Delete `openclaw_config.go`'s `ToStandardConfig` migration branch mapping legacy deny patterns onto `Sandbox.ShellDenyPatterns`/`AgentConfig.ShellPolicy` (both target fields are gone) and its test (`openclaw_config_test.go`'s `TestToStandardConfig_ExecDenyPatterns_*`). Greenfield — no replacement mapping.

**R-2 — Config fields, corrected acceptance check (resolves B-7).** Delete `ShellDenyPatterns`/`AgentShellPolicy`. **Acceptance check corrected:** a `release/v0.1.1`-shaped config file loads cleanly (file-half tolerance, unchanged). **The strict-client check is a `PUT` against `rest_agents_update.go`, not a `POST` against `rest_agents_create.go`** — `rest_agents_update.go`'s decode is non-strict by default (`validate_inbound` defaults false) and would otherwise silently drop a stale `shell_policy` with a 200. Add `shell_policy` to the existing retired-field raw-body sniff in `rest_agents_update.go` (matching the `sandbox_profile`/`delegation_policy` precedent already there); assert 400 on the PUT.

**R-3 through R-6 unchanged from first draft** (gateway validation, wiring, contracts — extended per §6.1's three `$ref` additions, SPA).

### 7.2, 7.3 unchanged from first draft (dead exec-approval manager; exec allowlist fold-in).

**R-9's guard, named (resolves R-j):** the "third check" is `scripts/check-no-exec-allowlist-rerun.sh`, with a required `.test.sh` self-check companion per `scripts/guards.sh`'s discovery rule (no guard may use the `guards-no-selftest.exempt` list for a *new* guard). R-14's acceptance check names this file explicitly. **`docs/` is deliberately outside the guards' scope** (`pkg/ cmd/ src/ contracts/` only) — `browser-agent-capability-spec.md` still naming `AgentShellPolicy` is R-13's job, not a guard failure.

### 7.4 `ExecConfig.Approval` (unchanged — R-11, investigation).

### 7.5 Docs, CLAUDE.md, and operator documentation

**R-12 unchanged** (GodMode comment).
**R-13 unchanged** (13-doc triage).
**R-14 unchanged**, now naming the third guard file (above).
**R-15 — Operator documentation (resolves M-23).** `command_rules` is config-file-only with no UI (FR-018/§11) — per CLAUDE.md's Definition of Done, an undocumented config-only key is not reachable by an operator. Add a task: document `command_rules`'s shape, decision order, and resolve-and-verify semantics (FR-040), plus the three modes, in operator-facing docs. Owned by L7 (§8.3), added to its DoD.

---

## 8. Parallel delivery plan

### 8.1 Dependency graph and honest critical path (resolves M-21/R-l)

The eight lanes are **not** eight-way parallel; the true shape is five serial stages with two lanes running genuinely wide:

```
L0 Contracts ──> {L1 Rule engine, L2 Mode resolution, L3 Pre-flight(fs+net)} ──> L4 Shell core ──> L5 Removal ──> L7 Guards
                                                                                    ↑
L6 SPA renders against L0 immediately, wires grants after L4 lands ───────────────┘
```
Critical path: **L0 → {L1,L2,L3} → L4 → L5 → L7**. The genuine parallel win is L6 (large, `src/**`, self-contained) running alongside the L1/L2/L3 middle band from the moment L0 lands — not eight independent lanes. Stated honestly per the founder's standing max-parallelism ruling: eight lanes is still the right shape (L6 is large enough to justify its own lane, and the two contested backend files are correctly collapsed into single-owner lanes L4/L5), but the coordination win is the middle band plus L6, not eight-way concurrency.

### 8.2 File ownership (resolves B-8 — every previously-unowned file now has one lane)

| File(s) | Owner |
|---|---|
| `contracts/**`, `pkg/api/generated/**`, `src/lib/api/generated/**` | L0 |
| `pkg/shellrule/**` (new) | L1 |
| `pkg/agent/loop_policy.go` (mode-resolution half only — see L4 split below), `pkg/agent/sessionmode.go` (new) | L2 |
| `pkg/tools/preflight.go` (new — **both** filesystem D7 and network D8 evaluators), `pkg/fspolicy/policy.go` (`PathGrants`), `pkg/sandbox/derive_from_fspolicy.go` (renders `PathGrants`), `pkg/sandbox/sandbox.go`/`sandbox_linux.go`/`seatbelt_profile.go` (bash-scoped empty-`ConnectPortRules` rendering), `pkg/sandbox/*_antidrift_test.go` (new, Level 1 + Level 2) | **L3** — resolves B-8's `resolvepath.go`/`derive_from_fspolicy.go`/`fspolicy/policy.go` ownership gap; D8's network mechanism lives here alongside D7's filesystem one (same reviewer, same single-source philosophy) |
| `pkg/tools/shell*.go`, `pkg/tools/resolvepath.go` (grant-overlay parameter, FR-036), **`pkg/agent/loop_policy.go`'s `CheckGrantOrRequestApproval`-reachability half (FR-039's two new call sites)** | **L4** — resolves B-8's split of `loop_policy.go`: L2 owns mode resolution in that file, L4 owns the grant-consultation wiring in the same file; the two edits are non-overlapping functions, stated explicitly so the lanes don't collide |
| `pkg/security/approvalgrants.go` (prefix/path-widening/network-widening record kinds) | **L4** — resolves B-8; SP-4 already assumed this, now the ownership table agrees |
| `pkg/config/*`, `pkg/agent/loop_wire.go`/`loop_construct.go`, `pkg/policy/*` (delete), `pkg/security/execapproval.go`(delete), `pkg/gateway/sandbox_config_validation.go`/`rest_sandbox_config.go`/`rest_exec.go`, **`pkg/gateway/rest_agents_update.go`/`rest_agents_create.go` (R-1b/R-1c/R-2), `pkg/sysagent/tools/agent_apply_args.go` (R-1a), `pkg/migrate/sources/openclaw/openclaw_config.go`+test (R-1d), `pkg/gateway/approvals.go`/`rest_tool_registry.go` (new action enum + scope handling), `pkg/app/internal/run/run.go` (headless CLI approval path consumes the same enum)** | **L5** — resolves B-8's five previously-unowned files |
| `src/**` **except** `src/lib/api/generated/**` (L0's — resolves R-l's overlap: L0 and L6 both named `src/**`-shaped paths in the first draft) | L6 |
| `scripts/check-no-shell-deny-patterns.sh`, `scripts/check-no-exec-approval-manager.sh`, `scripts/check-no-exec-allowlist-rerun.sh` (R-j), `scripts/guards.sh`, root `CLAUDE.md`, `docs/**` triage, operator docs (R-15) | L7 |

**Compile-order rule (resolves B-9 — "lane branches must compile" was previously unstated):** lane branches compile against the **integration branch**, not standalone — R-4-style cross-lane deletions (L4 deletes a field L5's file still assigns) are expected mid-flight and resolved by rebasing onto integration, not by each lane branch passing CI alone. The full CI green bar (SC-001) is asserted once, on the integrated branch, before the `→ main` PR — this is the serialisation mechanism, not a shared-file exception.

### 8.3 Lanes (agent/DoD/review-gate table — unchanged from first draft except L3's DoD, extended for D8, and L7's DoD, extended for R-15/operator docs)

| Lane | Agent | Extended DoD |
|---|---|---|
| L3 | security-lead | Filesystem pre-flight verdict and kernel rendering agree (Level 1 **and** Level 2, FR-012); **network pre-flight**: bash's Auto `ConnectPortRules` render empty by default and widen correctly on grant (FR-042–044) |
| L7 | qa-lead + backend-lead | New guards wired and self-checking (incl. the named third guard); `command_rules`/three-modes operator documentation written (R-15) |

(L0, L1, L2, L4, L5, L6 unchanged from first draft.)

### 8.4 Serialisation points (SP-1…SP-5 unchanged; SP-2 now also covers L3's network half; SP-4 explicitly names `approvalgrants.go` as L4-owned, matching §8.2)

### 8.5 Lane count — eight, rationale restated honestly per §8.1.

---

## 9. Acceptance criteria (SC-xxx)

**SC-001 through SC-011 as first draft**, with:
- **SC-002** reworded: the anti-drift claim is scoped to what Level 1 (rendering agreement) and Level 2 (classifier agreement) each actually prove (FR-012); it does not claim agreement with live Landlock enforcement beyond the Linux-only spawned-child leg.
- **SC-004** extended: end-to-end demonstration covers both the filesystem widening (existing) and the network widening (FR-044).

**SC-012 — Global mode write requires re-auth.** A global mode-loosening write with no fresh re-auth token is refused, matching God Mode's existing step-up behaviour (FR-045).

**SC-013 — Secret set is never widenable.** An adversarial escalation naming a `CarveOuts` path is refused outright in every test run, never surfaced as a prompt (FR-037).

**SC-014 — Stale-client PUT gets 400, not silent drop.** A client still sending `shell_policy` to `PUT /agents/{id}` after removal gets a hard 400 (FR-003/R-2), not a 200 with the field silently dropped.

---

## 10. Open questions

1. **Per-turn single-value threading** — `guardCommand`, `turnKernelPolicy`, and now two pre-flight evaluators (D7, D8) each call `ResolveTurnFSPolicy`/the network equivalent independently. Cache-and-reuse vs. provably-safe independent calls: implementation decides.
2. **`ExecConfig.Approval` reachability** — confirm dead before deleting alongside §7.3.

*(All other first-draft open questions are resolved by founder decisions and are recorded as FRs above: mode storage — FR-001; global-mode auth — FR-045; widening data model — FR-036; network coverage — D8/FR-042–044; Windows grant scope — FR-041.)*

---

## 11. Out of scope / future work

- Per-website network approvals; a session-grants management surface (FR-028); SPA display/editing of `command_rules`; the exec allowlist's per-domain semantics (retired, not re-homed); Windows kernel sandbox (still none).
- **Headless-turn stall under Auto is a real, new consequence of this ADR, not "unchanged" (resolves M-16/R-k):** today a headless turn under the shipped `allow` ceiling never prompts. Under Auto, a filesystem or network escalation (D7/D8) can fire with no operator attached, stalling to the existing `ToolApprovalTimeout`. Since mode can only tighten below the global default, an operator cannot exempt a headless agent short of setting the global default to Auto-with-no-escalations-expected or God Mode. **This is stated here as an accepted consequence the operator must plan for, not built around** — no headless-specific handling ships in this ADR. On Windows specifically, Auto already equals Ask (D1), so a headless Windows agent stalls on every shell call unless the global default is God Mode — worth a founder ruling before shipping if Windows headless agents are a real deployment target; flagged, not resolved here.

---

## 12. Traceability matrix

| Requirement | Representative scenario(s) | Test level |
|---|---|---|
| FR-001, FR-002 | S1, S2, S3 | Unit |
| FR-003, S43 | S5, S6, S7, S43 | Unit + E2E |
| FR-004, FR-006, FR-047 | S4, S10 | Unit |
| FR-005, S47 | S8, S9, S47 | Unit |
| FR-007, FR-008, FR-014 | S11–S14 | Unit |
| FR-009, FR-010 | S15, S16 | Integration |
| FR-011, FR-036 | S17, S18 | Integration |
| FR-012, FR-038 | S21, S21b | Unit + generated |
| FR-013 | S20 | Integration |
| FR-015 | S50 (network is not silently uncovered) | Integration |
| FR-016, FR-017, FR-037 | S17–S19, S46 | Integration |
| FR-018, FR-019, FR-039 | S22, S25, S27, S48 | Unit |
| FR-020 | S23, R12–R14 | Unit |
| FR-040 | S24 | Unit |
| FR-041 | S49 | Unit |
| FR-022 | R-8/R-9 acceptance | Unit |
| FR-023 | S28, S36 | Unit + E2E |
| FR-024–FR-027 | S29–S37 | Unit + E2E |
| FR-028 | (non-feature assertion) | E2E |
| FR-029, FR-050 | S26, (three-branch scenario set) | Integration |
| FR-030, FR-031 | (invariance regression) | Unit + audit |
| FR-032, FR-046 | S38, S39 | Unit |
| FR-033 | S40, S41 | E2E |
| FR-034 | S42 | E2E |
| FR-035 | §7 acceptance checks | Unit + guards |
| FR-042–044 | S50–S53 | Integration |
| FR-045 | S44 | E2E |
| FR-048 | S45 | Integration |
| FR-049 | (accepted-risk statement, no test) | — |
| FR-051 | S57, S57b | Unit |
| FR-052, FR-054 | S54, S55 | Unit + integration |
| FR-053, FR-062 | S56, drift-guard mutation checks | Unit |
| FR-055 | S60 | Unit |
| FR-056 | S59 | Unit |
| FR-057 | S58 | Integration |
| FR-058, FR-059 | S61, S63 | Unit |
| FR-060 | S64 | Unit + E2E |
| FR-061 | S62 | Unit |

Every FR appears above; FR-015 (corrected) and FR-018/FR-022/FR-028/FR-030/FR-031/FR-035 all now carry at least one scenario or acceptance-check reference, closing the first draft's matrix gap.
