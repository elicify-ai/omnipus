# ADR-091 Implementation Specification — Shell permission: `ask` + Auto-approve, one rule format

- **Spec status:** Draft for lead review (plan-spec output; not committed — lead commits)
- **ADR implemented:** [ADR-091 — Shell permission: `ask` + Auto-approve, not a third mode](../architecture/ADR-091-shell-permission-modes.md) (revised 2026-09-23 for the founder's permission-model and "what safe means" decisions — see the ADR's Revision note)
- **Evidence baseline:** `release/v0.1.1` @ `838d8092c` (read-only). Contract shapes verified against branch `adr091/contracts` @ `e6a5cfd4e` (worktree `wt-adr091-contracts`).
- **Depends on:** ADR-036, ADR-063, ADR-077, ADR-090
- **Repo rule honoured throughout:** greenfield — no migration, no shims. `file::symbol` citations, never line numbers.
- **Revision note:** this pass replaces every "three modes" framing with the founder's two 2026-09-23 decisions: (1) `bash` keeps the ordinary three-valued tool policy every tool has; Auto-approve is a setting meaningful only on `ask`, applying to every `ask`-resolved tool, not `bash` alone; (2) "safe" (what Auto-approve may clear without a prompt) means staying inside the kernel sandbox, judged per call. Both are already implemented in the contracts named above (`SandboxConfig.auto_approve`, `Agent.auto_approve_disabled`, `SandboxStatus.kernel_sandbox_active`/`auto_approve_effective`, `SessionModeUpdateFrame`/`SessionModeUpdatedFrame`) — this pass brings the FR/BDD/test-data/traceability text into agreement with them, including two mechanical corrections D9 forces: D8's network pre-flight rendering is keyed to "`bash` resolves to `ask`," not "under Auto only," and `guardCommand`'s disposition is two-way (prompt machinery available or not), not three-named-mode. Everything from the earlier grill-driven revision (14 confirmed blockers, `PathGrants` data model, resolve-and-verify, the global-write step-up) stands unchanged and is not re-narrated here.

---

## 1. Purpose and scope

Testable requirements, BDD scenarios, test data, contract-first work, removal tasks, an 8-lane delivery plan, and acceptance criteria for ADR-091.

**In scope:** the three-valued tool policy plus Auto-approve and their resolution across three scopes, one of which may loosen (D1); D9's per-call "stays inside the sandbox" classification, including its honest scoping to in-process tools; the filesystem pre-flight (D7) **and its data model** (`FSPolicy.PathGrants`, bash-scoped); the network pre-flight (D8, corrected to key off `ask` rather than "Auto"); the unified rule format, decision order, and **grant-consultation reachability** (corrected — D3); resolve-and-verify binary binding (corrected from "rewrite argv0" — D3); the approval dialog redesign (`cancel` retained in the wire enum); God Mode; the platform predicate; audit events (routed via `emitAudit`, not `logDecision`); the status contract fields (`SandboxStatus.kernel_sandbox_active`/`auto_approve_effective`, already built, not a new schema); the global-default write's password step-up; and the full removal inventory including four previously-missed live surfaces plus the `bash` ceiling-default flip.

**Out of scope:** per-website network approvals; a grants management surface; network egress changes to tools other than `bash`'s own child, beyond the honest app-layer judgement D9 requires; a shared network-classifier helper for in-process tools (open question 3, ADR); Windows kernel sandbox (still none — D9's existing Auto-approve→Ask fallback is the mitigation, not new coverage). Full list in §11.

---

## 2. Existing code context

| Symbol (file) | Role | Fate |
|---|---|---|
| `ResolveTurnFSPolicy` (`resolvepath.go`) | single authored per-turn `FSPolicy` | **Extended**: gains an optional grant-overlay parameter, populated only by bash's own pre-flight/exec call sites (FR-036) |
| `fspolicy.FSPolicy` (`fspolicy/policy.go`) | five fields today: `WorkDir`, `Scope`, `CarveOuts`, `AllowedRoots`, `ReadConfined` | **Additively extended** — gains `PathGrants []PathGrant` (FR-036). `AllowedRoots` is a subtree **write** grant only; there is no existing per-file or read-grant field, which is why this is additive, not "Unchanged" as the first draft claimed |
| `DeriveKernelPolicy` (`derive_from_fspolicy.go`) | authored policy → kernel policy | **Additively extended** — renders each `PathGrant` as one more `PathRule`; the anti-drift lock test's oracle |
| `resolveEffectivePolicyWith` (`compositor.go`) | strictest-wins global×agent merge; `if cfg.GodMode { g = allow }` | **Unchanged** — `bash` resolves through it exactly like every tool; Auto-approve is a setting layered on top of whatever this merge returns, not new merge logic (FR-001) |
| `CheckGrantOrRequestApproval` (`loop_policy.go`) | grant-store consultation | **Reachability extended** — today reachable ONLY from `resolveAskPolicy`, itself gated on `toctouPolicy == "ask"` (verified: `loop_run_turn_tools.go::resolveAskPolicy`); an `"allow"` verdict never calls it. FR-039 adds two more call sites: a D3 `ask`-rule verdict, and a pre-flight escalation (D7/D8) |
| `sandbox.DefaultConnectPorts` = `{53,80,443}` (`sandbox.go`) | boot-default outbound port allow-list, seeded unconditionally into every child's `ConnectPortRules` | **Unchanged for the boot profile**; `bash`'s own per-turn rendering starts empty instead whenever `bash` resolves to `ask` (corrected from "under Auto only" — D9, FR-042) |
| `defaultDenyPatterns`/`secretGuardPatterns`/`buildSecretGuardPatterns` (`shell.go`) | ~38-regex block list + secrets backstop | **Deleted** (D2) |
| `applyDenyPatterns`/`compileDenyPatterns`/`denyPatternMessage` (`shell_guard.go`) | block-list matcher | **Deleted** (D2); `lowerASCII` survives, moves to `shell_subst_guard.go` |
| `guardCommand`'s absolute-path scan / `checkPathSegment` (`shell_path_guard.go`) | tokenized path-containment scan (three ordered layers total — **not** "step 4"; the surviving scan is layer 3, corrected citation throughout this spec) | **Survives, mode-sensitive** (FR-041) |
| `pathUseVerdict`/`newPathUseClassifier` (`shell_path_guard.go`) | existing `{readOnly, exec, reason}` three-way verdict | **Reused** as the base for the FR-038 operation classifier, extended to the `{read, write, read+write}` shape FR-016 needs |
| `splitShellSegments`/`shellCommandHead`/`shellCommandHeadDetailed` (`shell_subst_guard.go`) | tokenizer; the `Detailed` variant's third return flags a normalised (unreliable) head | **Survives**; D3's matcher MUST use `shellCommandHeadDetailed` and refuse to satisfy an `allow` rule from a normalised head (FR-040) — POSIX operator set only, does not model PowerShell (FR-041) |
| `Evaluator.EvaluateExec`/`MatchGlob`/`FirstToken` (`policy/evaluator.go`) | opt-in exec allowlist | **Deleted, folded into D3** |
| `PolicyAuditor.logDecision` (`policy/auditor.go`) | unexported, one caller, 4-string signature, `sessionID` fixed at construction | **Survives for D3's own retarget only**; the four NEW events (FR-046) route via `ExecTool.emitAudit`'s `audit.Entry.Details map[string]any` instead — `logDecision` has no field for mode/level/scope |
| `ExecApprovalManager` et al. (`security/execapproval.go`) | dead manager behind issue #83 headline | **Deleted** (D5) |
| `ApprovalGrantStore` (`security/approvalgrants.go`) | session-scoped exact-fingerprint store | **Survives, extended** — prefix, path-widening (`PathGrants`), and network-widening (`ConnectPortRules`) record kinds |
| `ToolApprovalModal` / `ToolApprovalModal.resolution.test.tsx` | 4-button dialog; `cancel` and `deny` verified to behave **differently** on purpose (network-failure vs 404 dismissal) | **Redesigned** to `[Deny][Allow once][Allow]` + scope radio; `cancel` **stays in the wire enum**, no button (FR-023) |
| `GodModeActiveBanner` (`settings/GodModeControl.tsx`) | already exists, already red, already has a fetch-failure-safe `isError` variant, rendered today at `GatewaySection.tsx:298` | **Relocated** to `AppShell` app-wide — a move, not a new build (FR-034) |
| `SandboxStatus.yaml` | existing schema: `backend`, `available`, `kernel_level`, `policy_applied`, `abi_version` | **Extended, already built on `adr091/contracts`** with `kernel_sandbox_active` (D1's platform predicate) and `auto_approve_effective` (the global default, mirrored for the badge) — **not** `effective_mode`, which the first draft proposed and this revision drops; the badge composes both fields plus per-agent/per-chat context client-side (FR-033) |
| `applyShellPolicy` (`sysagent/tools/agent_apply_args.go`); `rest_agents_update.go`; `rest_agents_create.go`; `openclaw/openclaw_config.go`(+test) | four live surfaces referencing `AgentShellPolicy`/`ShellDenyPatterns` missed by the first removal pass | **Removal tasks added** (R-1a…R-1d, §7.1) |
| `pkg/config/defaults.go:190` | `"bash": "allow"` — today's shipped ceiling default | **Changed to `"ask"`** (D1's consequence, removal item 5, FR-001) — the global `auto_approve` default (`true`) is what keeps a fresh install feeling the way the first draft's "Auto" description did |
| `WebFetchTool`/`webFetchParseArgs`/`webFetchDialContext` (`web_fetch.go`); `SendFileTool.Execute` (`send_file.go`); `browser/tools.go`, `tools_interact.go` | in-process tools — verified no `os/exec` in any of the three files, unlike `shell.go`/`shell_bg.go`/`shell_process_unix.go`/`shell_process_windows.go`/`environment_setup_runner.go`, which do call it | **D9's Consequence 1 applies**: no kernel enforces these calls. `send_file.go:116`, `browser/tools.go:805`, `browser/tools_interact.go:789` call `ResolveTurnFSPolicy` in-process (never `DeriveKernelPolicy`/a spawned child) for filesystem access; `web_fetch.go` has no filesystem policy call at all — its own guard is `SSRFChecker`, a different mechanism for a different resource, with no pre-flight-escalation or grant-consultation step today (FR-051/FR-052, new) |

**Impact assessment** (grep-verified caller counts):

| Symbol removed/modified | Risk | Callers that MUST be updated |
|---|---|---|
| `defaultDenyPatterns`/`secretGuardPatterns` | MEDIUM | `NewExecToolWithConfig`; `applyDenyPatterns` (2 callers, `shell_path_guard.go`) |
| `Evaluator.EvaluateExec`/`MatchGlob`/`FirstToken` | LOW (contained; `EvaluateExec` ignores its own `agentID` param) | `PolicyAuditor.EvaluateExec`→`shell.go`; `loop_construct.go`; `rest_exec.go` |
| `ExecApprovalManager` | LOW (dead) | none |
| `AgentShellPolicy`/`ShellDenyPatterns`/`ExecConfig.AllowedBinaries` | MEDIUM–HIGH | `loop_wire.go`, `sandbox_config_validation.go`, `rest_sandbox_config.go`, `rest_exec.go`, `rest_agents_update.go`, `rest_agents_create.go`, `agent_apply_args.go`, `openclaw_config.go`, config load |

---

## 3. Functional requirements

### 3.1 Tool policy, Auto-approve, and authorization [founder decision 1, 2026-09-23 — rewritten, was "Modes, storage, and authorization"]

**FR-001 — Three-valued tool policy, unchanged; Auto-approve is a setting on `ask`, not a fourth value.** `bash` resolves through the unchanged ADR-077 two-layer ceiling/override merge to `allow`/`deny`/`ask`, identical meaning to every other tool. **`allow`** runs unprompted, none of this ADR's machinery. **`deny`** makes the tool invisible; Auto-approve has no effect. **`ask`** is where this ADR's machinery — approval dialog (D4), pre-flight escalation (D7/D8), grants (D3/D4), and D9's per-call classification — applies. Auto-approve (`SandboxConfig.auto_approve`, boolean, fresh-install default `true`) is a **separate setting**, meaningful only for a tool currently resolved to `ask`, and applies to **every** such tool, not `bash` alone. `config.ReconcileToolPolicyCeiling`/`resolveEffectivePolicyWith` need no change — this is layered on top of what they already return. `pkg/config/defaults.go:190` changes `bash`'s ceiling default from `"allow"` to `"ask"` (removal item 5) — the behavioural consequence the first draft's "Auto" framing tried to achieve by leaving the value at `"allow"`, which conflated "no machinery" with "machinery that mostly doesn't prompt." An agent may still carry a stricter explicit per-agent `bash` override (e.g. ADR-090's Jim, `bash: deny`); nothing about it changes.

**FR-002 — Three Auto-approve scopes, tighten-only except one.** Global (`SandboxConfig.auto_approve`) → per-agent (`Agent.auto_approve_disabled`, off-only by construction — no value means "on") → per-chat (`SessionModeUpdateFrame.auto_approve`, nullable `true`/`false`/`null`). Global and per-agent are tighten-only relative to the scope above; **per-chat is the one deliberate exception that MAY loosen** (turn Auto-approve on for that chat even when the resolved agent × global default has it off), because a human is present in that session. `null` clears the chat's own modifier back to the agent × global resolution rather than pinning a stale value.

**FR-003 — Server-side loosening rejection, one validator, four writers (global/per-agent only).** The write handler re-reads the global default and rejects a loosening write with 4xx. **Per-agent config has four writers, all four must call the same tighten-only validator**: `rest_agents_update.go` (field merge), `rest_agents_create.go` (create-path decode), `pkg/sysagent/tools/agent_apply_args.go` (agent-callable), and any other per-agent write surface. The sysagent path is the one that matters most: an agent reaching an Auto-approve write surface is bounded by this check, not by the tool being absent. This FR does **not** apply to the per-chat scope, which is allowed to loosen by design (FR-002).

**FR-004 — Session-scoped modifier, not a third merge layer.** The per-chat `session_mode_update` frame is applied **after** the ADR-077 merge resolves the tool's policy value; no `chat_id` key on either policy map (Hard Constraint #6); new, separate, session-keyed state (`pkg/agent/sessionmode.go`), acknowledged via `session_mode_updated.auto_approve_effective`.

**FR-005 — Delegation inheritance.** The per-chat modifier inherits to a delegate via `ApprovalGrantStore.InheritFrom`; delegate resolves the tightest of (parent modifier, own override).

**FR-006 — Mid-turn and restart semantics.** A call is resolved against the tool-policy value and Auto-approve state in force when `resolveToolPolicyAtExec` (the **existing** TOCTOU re-check — unchanged, and this FR does not duplicate or bypass it) last evaluated it; a tighten landing after does not retroactively change that verdict. The per-chat modifier clears on restart with the session.

**FR-007 — Platform predicate, one derivation, now a contract field.** `SandboxStatus.kernel_sandbox_active` (built, `adr091/contracts`): Linux — Landlock enforce, not degraded. macOS — Seatbelt backend active/enabled, **reads Seatbelt's own state directly, not `SandboxStatus.policy_applied`**, which reports gateway self-confinement and is documented false on macOS by design. Windows — never.

**FR-008 — No kernel sandbox ⇒ Auto-approve resolves as Ask (D9 Consequence 2).** `kernel_sandbox_active=false` means D9's classifier has nothing to positively clear a call against, so every `ask`-resolved call prompts regardless of `auto_approve_effective`. Badge: "Auto → Ask" (`auto_approve_effective=true`, `kernel_sandbox_active=false`).

**FR-045 — Global Auto-approve write requires the password step-up.** `SandboxConfig.auto_approve`'s write MUST go through the existing `rest_sandbox_config.go::putSandboxConfig` → `authenticateAndDecode` → `requireReAuth` chain (verified, `pkg/gateway/rest_sandbox_config.go:170,344,355`) — the same step-up God Mode and credential writes already use. No new auth mechanism; the requirement is routing through the existing gated handler. It is hot-reloaded (no `requires_restart`), unlike `mode`/`allowed_paths` on the same endpoint. Per-agent/per-chat writes need no step-up. The agent-facing `set_config` tool already blocks the whole `sandbox.*` subtree by ancestor closure (`pkg/sysagent/tools/config.go::blockedConfigKeys`, verified) — `sandbox.auto_approve` inherits this coverage automatically since it lives under `sandbox.*`; no new `blockedConfigKeys` entry is required, but implementation confirms the field's storage path stays under that prefix. **Accepted, pre-existing, not fixed here:** on Windows, `config.json` is not kernel-denied to a spawned child, so an agent's own `bash` tool can write it directly, bypassing both `set_config` and the step-up — documented, Linux/macOS-only guarantee.

**FR-051 — Turning Auto-approve on for a chat is human-only, structurally (new — founder decision 1).** The per-chat `session_mode_update` frame is the only place `auto_approve` may resolve to `true` against a stricter agent/global default, and it is a session-scoped WS write, not a `config.json` path — `set_config` cannot reach it by construction, not merely by policy check. `SessionModeUpdateFrame.yaml`'s own description states this as a requirement. No FR-003-style validator is needed here precisely because there is no code path from `set_config` into this frame to reject.

**FR-052 — D9's classification, stated as a requirement (new — founder decision 2).** For a call on a tool resolved to `ask`, Auto-approve MAY auto-clear it only if the call stays within what the kernel sandbox already permits for that turn (the agent's workspace files and anything else the effective policy allows); it MUST prompt for anything outside the workspace, any network access, secrets/credential files, or a settings change — evaluated **per call**, not per tool. `bash`'s D7/D8 pre-flight is this classification's kernel-backed implementation. A call needing a widening grant always prompts, Auto-approve on or off (FR-048 restates this for the denial case).

**FR-053 — In-process tools get the same classification, app-layer, honestly weaker (new — founder decision 2, D9 Consequence 1).** `web_fetch.go`, `send_file.go`, and the browser tools (`pkg/tools/browser/tools.go`, `tools_interact.go`) run in-process — verified: none calls `os/exec`, unlike `shell.go`/`shell_bg.go`/`shell_process_unix.go`/`shell_process_windows.go`/`environment_setup_runner.go`. For the filesystem-touching ones, the same effective policy the kernel ruleset is derived from is evaluated app-layer: `send_file.go:116`, `browser/tools.go:805`, `browser/tools_interact.go:789` all call `ResolveTurnFSPolicy` directly and never reach `DeriveKernelPolicy` or a spawned child. This is an app-layer judgement, not a kernel one — a bug in the evaluating code is not caught by a kernel refusing the syscall the way a `bash` call's mistake would be. Any such tool an operator tightens to `ask` inherits FR-052's classification through this app-layer path only.

**FR-054 — `web_fetch` has no classification path today; tightening it to `ask` needs one (new — founder decision 2, honest gap).** `web_fetch.go` calls no `ResolveTurnFSPolicy`/`DeriveKernelPolicy` at all — its only guard is `SSRFChecker` (`webFetchParseArgs`/`webFetchDialContext`), a private/public-host check, not a workspace-vs-network classification, and it carries no pre-flight-escalation or grant-consultation step. `fetch_url` ships `allow` by default (`pkg/config/defaults.go:230`), so FR-052 is dormant for it out of the box. If an operator tightens `fetch_url` to `ask`, network access (every `fetch_url` call, by definition) MUST fall in the "prompt" bucket under FR-052 — implementation adds the minimal classification needed to honor that, not a full D8-style grant system (open question 3, ADR).

### 3.2 Filesystem pre-flight (D7) — data model, scope, and honest gaps

**FR-009 — Pre-flight, never start-then-interrupt.** Unchanged from the first draft.

**FR-010 — One policy value, not two implementations.** Unchanged.

**FR-011 — Widening re-resolves the single value, bash-scoped.** When approved, the widened value is re-resolved and both the app-layer guard and `DeriveKernelPolicy` are rebuilt from it before spawn. **The widening applies only to the bash tool's own resolution** (FR-036) — it does not propagate to `edit`, `write_file`, `grep`, `send_file`, `web_serve`, `request_mount`, or the browser tools' independent `ResolveTurnFSPolicy` calls.

**FR-012 — Anti-drift lock test, two levels.** Level 1 (unchanged): a `{path, operation}` matrix vs. `DeriveKernelPolicy`'s rendering, including the widened-value half (non-empty `PathGrants`), cross-platform Go-level. **Level 2 (new — resolves the "tests only the rendering, not the risky half" gap):** a separate, second test asserts that the pre-flight's own command-text→`{path, operation}` extraction (the FR-038 classifier) agrees with what a human-authored oracle expects for the §5.2/§5.4 test-data rows — the rendering-agreement test alone does not establish agreement with what Landlock actually enforces (`SandboxPolicy.DeniedPaths` is honoured differently on macOS vs. Linux, per `pkg/sandbox/sandbox.go`), so SC-002's claim is scoped to what each level actually proves.

**FR-013 — Missed-denial behaviour, corrected claim.** A kernel denial the pre-flight missed fails the command with **the child's own OS error** (e.g. a bare permission denial). Omnipus does **not** claim to diagnose it as specifically a sandbox denial — a Landlock/Seatbelt denial is not a structured, observable event to the gateway (the same reason runtime-catch was rejected, ADR Alternatives). Never silently allowed, never silently retried unconstrained.

**FR-014 — No-sandbox platforms resolve Auto-approve as Ask.** Restates FR-008/D9 Consequence 2 for the pre-flight specifically: no `kernel_sandbox_active` ⇒ the pre-flight has no confinement to compare a call against ⇒ every `ask`-resolved call prompts, Auto-approve on or off.

**FR-015 — Network is NOT out of scope; D8 covers it.** *(Corrects the first draft, which declared network out of scope entirely — that left nine command categories with no compensating control, B-11.)* Filesystem pre-flight (D7) and network pre-flight (D8, §3.9) are two independent evaluators over two independent resources; neither substitutes for the other. A command needing only network (no filesystem escalation) is evaluated by D8 alone.

**FR-016 — Path-widening grant shape, secret-set excluded.** `{path, operation}`, `operation ∈ {read, write, read+write}`, exactly the access class found missing — **and never a path in `CarveOuts`/the secret set (FR-037)**.

**FR-017 — Session-scoped, recorded.** Unchanged.

**FR-036 — `PathGrants` data model (resolves B-10).** `fspolicy.FSPolicy` gains `PathGrants []PathGrant{Path string; Access uint64}`, reusing `sandbox.AccessRead|AccessWrite|AccessExecute` — not a new vocabulary. `DeriveKernelPolicy` renders each as one additional `PathRule{Path, Access}`, additive to the `WorkDir`/`AllowedRoots` rendering. `guardCommand`'s scan checks `PathGrants` the same way it checks `AllowedRoots`. **Tool scope:** `ResolveTurnFSPolicy` gains an optional grant-overlay parameter populated ONLY by the bash pre-flight/exec call sites from `ApprovalGrantStore`; the other eleven callers (`edit.go`, `filesystem.go` ×3, `grep.go`, `send_file.go`, `web_serve.go` ×2, `request_mount.go`, `browser/tools.go`, `browser/tools_interact.go`) pass none.

**FR-037 — Secret set is never widenable.** An escalation naming a `CarveOuts`/secret-set path is **refused outright, never prompted**. This is asserted as a requirement, not left to `CarveOuts`' existing "always denied" behaviour to happen to also cover it.

**FR-038 — Operation classifier.** The pre-flight's read/write/read+write classification reuses `pathUseVerdict`/`newPathUseClassifier`, extended from its current `{readOnly, exec, reason}` three-way to the `{read, write, read+write}` shape FR-016 needs. Redirections (`>`, `>>`), `tee`, `dd of=`, `mv`, `cp` (source read, dest write) are explicit classifier cases (§5.4 dataset).

**FR-047 — Concurrency.** A path/network widening approved while another command is in flight applies to subsequent spawns only — an already-spawned child's kernel ruleset cannot be widened retroactively. A mode tighten landing mid-turn does not retroactively change an in-flight command's resolved verdict (restates FR-006 for the concurrent case). Duplicate approval submissions for the same pending call are idempotent (second submission is a no-op, not a second grant).

**FR-048 — Denied escalation refuses outright.** Denying a filesystem or network pre-flight escalation refuses the command; it does not run un-widened and does not retry — true whether Auto-approve is on or off, since a widening decision is never Auto-approve's to make (D9, FR-052).

### 3.3 (renumbered from original 3.4) Unified rule format and decision order

**FR-018 — One rule shape, config-file-only.** Unchanged.

**FR-019 — Decision order, corrected reachability (resolves B-1).** Deny beats ask beats allow, specificity-blind, regardless of tool-policy value or Auto-approve state. **Grant consultation is reachable from three sites, not one:** (1) the existing `ask`-policy path (`resolveAskPolicy`, unchanged); (2) a D3 `ask`-rule verdict (new caller); (3) a D7/D8 pre-flight escalation (new caller). An `allow` ceiling verdict with no D3 `ask` rule and no pre-flight escalation still never touches the grant store — that is unchanged, correct behaviour, not a regression. Today's Ask-mode "Always Allow" (exact-fingerprint grant suppressing a repeat prompt) is **preserved** via site (1); it is not removed by this ADR.

**FR-039 — `CheckGrantOrRequestApproval` gains two callers.** The D3 `ask`-rule path and the pre-flight-escalation path both consult the grant store (via the same function or a shared helper) **before** showing a dialog, exactly as the existing `ask`-policy path does. A matching grant suppresses the prompt on all three paths identically.

**FR-020 — Per-segment matching, blind spots route to `ask`.** Chained commands split via `splitShellSegments`/`shellCommandHead` — no new parser. Each segment must independently clear or the whole call asks. **The splitter's known blind spots — a quote-blind over-split (`echo "a;b"`), no redirection-as-separator handling, no brace-expansion descent — each route the affected segment to `ask`, never to a resolved head** (§5.3 rows R12–R14).

**FR-040 — Resolve-and-verify, not rewrite (resolves B-3/B-12).** `buildShellArgv` execs `["sh","-c",command]` (POSIX) / `["powershell","-NoProfile","-NonInteractive","-Command",command]` (Windows) — `argv[0]` is always `sh`/`powershell`; there is no user-binary argv slot to rewrite, and rewriting the `-c` string is attacker-influenced text surgery. Instead: **the matcher resolves a segment's leading token to its actual executable against the child's effective PATH/env, matches rules against the resolved absolute path, and re-verifies that resolution immediately before spawn. Command text is never rewritten.** The matcher MUST use `shellCommandHeadDetailed`, not `shellCommandHead` — a head whose `normalised` flag is true (e.g. `./cat`, `CAT`, a stripped `/usr/bin/` prefix) cannot satisfy an `allow` rule; rule matching is case-sensitive on the resolved path. The TOCTOU window between resolve and exec is the kernel sandbox's boundary, stated explicitly, not the rule engine's.

**FR-041 — Windows: exact-command grants only.** `splitShellSegments` is a POSIX operator set (`| ; & \n \r`) that does not model PowerShell grammar, and there is no `argv[0]` slot on Windows either. On Windows, D3/D4 grants and rules are **exact-command match only** — no prefix option, no chained-segment splitting. Documented platform limitation, not partial POSIX behaviour.

**FR-022 — Exec allowlist folded in.** Unchanged; `logDecision` retarget is D3's own audit write and is distinct from the FR-046 event routing.

### 3.9 Network pre-flight (D8, corrected — resolves B-4/B-11 and keys off `ask`, not "Auto")

**FR-042 — `bash` denies outbound network by default whenever it resolves to `ask` (corrected: was "under Auto only").** The rendering is keyed to the tool-policy value, not to Auto-approve — Auto-approve only ever decides whether an already-contained call still needs a click (D9); it has no say over what the kernel child is allowed to reach. For `bash`'s rendered per-turn policy whenever `bash` resolves to `ask` (not other tools, not `allow`, not God Mode — which floors the ceiling at `allow`, D1), `ConnectPortRules`/`BindPortRules` render **empty** instead of the boot-default `DefaultConnectPorts={53,80,443}`. On Linux, Landlock ABI≥4 installs `handledAccessNet` unconditionally (`sandbox_linux.go`, verified — not conditioned on the rule list being non-empty), so an empty list is a true kernel-enforced deny-all for that child's `connect(2)`. macOS: the Seatbelt profile renders `ConnectPortRules` identically (`seatbelt_profile.go`) — same mechanism. Windows: no kernel network control exists there in the first place; FR-008 already makes `ask` always prompt on Windows, so FR-042 adds nothing and removes nothing.

**FR-043 — Network-need classifier.** A pre-flight sibling to FR-038, reusing D3's resolved-binary step: flags a command via (a) a curated, operator-extendable set of network-capable binaries (`git`, `curl`, `wget`, `ssh`, `scp`, `rsync`, `npm`/`pnpm`/`yarn`, `pip`, `apt`/`yum`/`dnf`, `docker`, `gh`, cloud CLIs) or (b) a literal `http(s)://` token. A flagged command escalates before spawn, identically to FR-009's filesystem prompt.

**FR-044 — Network grant, widening, honest gap.** Approving widens the session's `ConnectPortRules` to `DefaultConnectPorts` — **port-level, not domain-level** (Landlock `NET_CONNECT_TCP` cannot filter by host; CIDR/host filtering for Omnipus's own HTTP clients remains the unchanged `SSRFChecker`/`ExecProxy`, which a bash-spawned binary can bypass by ignoring its proxy env vars — pre-existing, documented, not new here). New `network` grant kind in `ApprovalGrantStore`, same session/delegate/clear-on-close lifetime as `PathGrants`. **Honest gap, symmetric to FR-013:** a command the classifier misses but that opens a raw socket is denied by the kernel at `connect()` time (fails with a clear error), never silently allowed.

**FR-049 — What D8 does not cover.** `shutdown`/`reboot`/`poweroff`, `kill`/`pkill`/`killall`, the fork bomb, `sudo`, `chmod`/`chown`, `eval`, `source *.sh` are not network operations; D8 is silent on them, same as D2. This is the accepted residual risk named once in the ADR, not re-litigated per category here.

**FR-055 — In-process network access is never auto-approved (new — founder decision 2, D9's general rule applied beyond `bash`).** FR-052's classification puts "any network access" in the always-prompt bucket regardless of mechanism. For `bash`, D8's kernel-level empty-`ConnectPortRules` rendering enforces this. For an in-process tool tightened to `ask` (FR-054 — `fetch_url`; equivalently `browser_navigate`/similar, `pkg/config/defaults.go:306`, `allow` by default today), the same rule is enforced app-layer only: the tool's own handler MUST treat a call that would reach the network as outside Auto-approve's safe set and route it to the ordinary `ask` prompt, with no kernel backstop if that check is wrong (FR-053's honest weaker claim applies here specifically).

### 3.5 Approval dialog and suggested prefix

**FR-023 — Three buttons; `cancel` stays in the wire enum (resolves R-a).** `[Deny][Allow once][Allow]` shown; the UI never renders a Cancel button and Escape/overlay/X all resolve to `deny`. **`ToolApprovalActionRequest.action`'s enum keeps `cancel`** as a client-issued (not button-issued) resolution value — `ToolApprovalModal.resolution.test.tsx` verifies `deny` (network failure, leaves the approval unresolved so a later snapshot can restore it) and `cancel` (lost-server 404, resolves it locally) behave *differently on purpose*; the headless CLI path (`pkg/app/internal/run/run.go`) drives the same enum and would also break if `cancel` were removed. Enum becomes `deny | allow_once | allow | cancel` (four values; three surfaced as buttons, `cancel` reserved for the stuck-approval recovery path).

**FR-024 — Scope choice, token-boundary matched.** Unchanged from first draft (exact default; prefix ignores `cwd`; token-boundary — `npm run test` does not match `npm run testfoo`).

**FR-025 — Per-segment grants.** Unchanged.

**FR-026 — Suggested-prefix algorithm, pinned stop tokens (resolves R-e/M-20).** Stop at the first flag/path-arg/URL token; env-assignment stripped before deriving; known wrapper (`sudo`, `env`, `timeout`, `xargs`, `sh -c`) is the prefix's start, not stripped. **Pinned:** the prefix stops at the wrapper's first argument — `env X=1 cmd` → `env` (P7); `timeout 5 npm test` → `timeout` (P10); `VAR=value echo hi` → `echo hi` (assignment stripped, no further stop token, P14). This is the narrowest, most defensible reading; if the founder wants the looser (whole-wrapper-plus-args) reading later, it is a config default to change, not a re-spec.

**FR-027 — Per-segment dialog display.** Unchanged.

**FR-028 — No grants list; audit is the compensating visibility (closes m-5 loosely).** Session grants are not shown, listed, or revocable. The audit log (FR-046) is the deliberate compensating visibility for this non-feature — stated once here so it reads as a considered trade, not merely an absence.

### 3.6 God Mode

**FR-029, FR-030, FR-031 — Unchanged from first draft.**

**FR-050 — Path-guard disposition turns on whether prompt machinery exists, not on a named mode (resolves R-g; corrected — D9 Consequence 3).** `guardCommand`'s path-containment denial has a two-way disposition, not a three-named-mode claim: **`ask`-resolved** (Auto-approve on or off — irrelevant here, since a widening decision is never Auto-approve's) surfaces the denial as the FR-009 pre-flight escalation, because `ask` is exactly the state where prompt machinery exists. **`allow`-resolved or God Mode** (no prompt machinery active in either) hard-denies with no prompt — God Mode does not skip the guard, it just has nothing to escalate through.

### 3.7 Audit and status contract

**FR-032 — Four audit events. Renumbered routing (corrects the first draft's `logDecision` claim).**
(a) Auto-approve/tool-policy change — new value, scope (global/per-agent/per-chat), actor; (b) pre-flight escalation (filesystem **or** network, FR-009/FR-042) — rule/path or classifier match that tripped, operation/port requested; (c) grant recorded — scope (exact/prefix/path-widening/network-widening), lifetime (session), resolved binary; (d) approval decision — allow-once/allow-with-grant/deny, per call. **Actor taxonomy** for (a): `operator` (human via Settings), `agent` (a tool call — always refused at write time per FR-045/FR-003 for global/per-agent, structurally unreachable per FR-051 for per-chat, but the *attempt* is still audited), `system` (a config-load reconciliation, if any).

**FR-046 — Routed via `emitAudit`, not `logDecision`.** `PolicyAuditor.logDecision` is unexported, single-caller, and its `(event, agentID, tool, command string, d Decision)` signature has no room for scope/level/path. The four FR-032 events are written via `ExecTool.emitAudit`'s existing `audit.Entry.Details map[string]any` (already used for `cwd`/`god_mode` today) — new `Details` keys per event type, not a `logDecision` signature change. **Redaction posture:** unchanged and deliberate — `audit.Entry.Command` already logs the full command text; the new events add paths/prefixes/ports at the same fidelity, no new redaction requirement.

**FR-033 — Contract-defined status fields, extend `SandboxStatus` (resolves R-b/M-11; corrected — built as two fields, not `effective_mode`).** The badge reads `SandboxStatus.kernel_sandbox_active` (D1's platform predicate) and `auto_approve_effective` (the gateway-wide Auto-approve default, mirroring `SandboxConfig.auto_approve` so the badge doesn't need a second round-trip to Settings) — **both already built on `adr091/contracts` @ `e6a5cfd4e`**, not the `effective_mode` field the first draft proposed. Neither is a second `policy_applied`/`kernel_level` derivation — those describe this process's own sandboxing posture; `kernel_sandbox_active` specifically answers "does Auto-approve's pre-flight have anything to check a command against." The SPA composes the actual per-chat badge from these two fields plus the active agent's `auto_approve_disabled` (`GET /agents/{id}`) and the session's own `session_mode_updated.auto_approve_effective` once received — `auto_approve_effective` here is the global baseline only, carrying neither agent nor session context. On macOS, `kernel_sandbox_active` reads Seatbelt's own active/enabled state (FR-007), not `policy_applied` verbatim.

**FR-034 — God Mode banner is a relocation, not a build (resolves M-12).** `GodModeActiveBanner` (`GodModeControl.tsx`) already exists, already red, already has a fetch-failure-safe `god-mode-status-unknown-banner` variant, and is already rendered at `GatewaySection.tsx:298`. This FR is: **move it to `AppShell`, app-wide**; preserve the fetch-failure `isError` state exactly (a naive move risks dropping it); correct its body text, which today says the "shell guard" is disabled — stale even before D6, doubly stale after (D6/FR-031 already corrects the Go comment; this FR extends that correction to this UI string).

### 3.8 Removal

**FR-035 — Complete removal, no shims.** Unchanged; each removal carries a CI guard.

---

## 4. BDD scenarios

Grouped by FR; each carries `Traces to:`. Scenarios unchanged from the first draft are listed by ID only where their text is unchanged; changed/new scenarios are given in full.

### 4.1 Tool policy and Auto-approve scopes (S1–S10 unchanged in intent, reworded for the corrected model: fresh-install `bash` ceiling is `ask` with global `auto_approve=true`, global/per-agent tighten, per-chat may loosen, UI never offers a looser global/per-agent option, delegation ×2, per-chat modifier dies with restart)

**S43 — Sysagent tool cannot loosen an agent's Auto-approve** *(Error Path, resolves FR-003's fourth writer)*
- **Given** an agent calls `set_config`/`agent_apply_args` attempting to set its own `auto_approve_disabled` to `false` (loosen) while the global default is `false` (off), **When** the write is evaluated, **Then** it is rejected the same way the REST handler rejects it (one validator, four writers) — and separately, `false` structurally only ever means "inherit," never "force on," so no write of this field can loosen past the global default regardless of validation (FR-001/FR-003).
- *Traces to:* FR-003.

**S44 — Global Auto-approve write requires re-auth** *(Error Path)*
- **Given** an operator session with no fresh re-auth token, **When** the global `auto_approve` default is changed, **Then** the write is refused pending `requireReAuth`, identically to a God Mode toggle.
- *Traces to:* FR-045.

**S54 — Per-agent write cannot turn Auto-approve on** *(Error Path — mandatory negative case, founder decision 1)*
- **Given** the global `auto_approve` default is `false`, **When** an operator `PUT`s `Agent.auto_approve_disabled: false` for an agent (i.e. "not disabled"), **Then** that agent's `ask`-resolved tools still resolve to Ask, not Auto — `false` means "inherit the global default," which is itself `false`; there is no value on this field that means "on."
- *Traces to:* FR-001, FR-002.

**S55 — Per-chat modifier turns Auto-approve on against an agent that has it off** *(Happy Path — mandatory negative-of-the-rule case, founder decision 1)*
- **Given** an agent has `auto_approve_disabled: true` (forced off, global default irrelevant), **When** that chat's session sends `session_mode_update{auto_approve: true}`, **Then** the session resolves Auto-approve on for that one chat — the deliberate exception that may loosen — and `session_mode_updated.auto_approve_effective` reflects `true`; the agent's own stored setting is unchanged and a new chat with the same agent starts back at Ask.
- *Traces to:* FR-002.

**S56 — `set_config` cannot reach the per-chat loosening path** *(Error Path — mandatory negative case, founder decision 1)*
- **Given** an agent calls `set_config` with any key/value combination, **When** the call is evaluated, **Then** it cannot turn Auto-approve on for the current chat — `sandbox.*` (including `sandbox.auto_approve`) is refused by `blockedConfigKeys` ancestor closure, and the per-chat modifier is not a `config.json` path `set_config` can address at all.
- *Traces to:* FR-051.

**S57 — `deny` tool is unaffected by Auto-approve** *(Error Path — mandatory negative case, founder decision 1)*
- **Given** `bash` is explicitly `deny` for an agent, and the global `auto_approve` default is `true`, **When** the agent attempts to call `bash`, **Then** the tool remains invisible exactly as `deny` behaves without this ADR — Auto-approve never applies to a `deny`-resolved tool.
- *Traces to:* FR-001.

### 4.2 Platform predicate (S11–S14 unchanged, with S13 now citing FR-007's corrected macOS predicate wording)

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
- **Given** a pre-flight escalation for `{P, write}` on an `ask`-resolved `bash` call, **When** the user clicks Deny, **Then** the command does not run at all — not un-widened, not retried, regardless of the Auto-approve setting.
- *Traces to:* FR-048.

**S46 — Secret-set path is refused, never prompted** *(Error Path — mandatory adversarial case)*
- **Given** a command whose write target resolves into `CarveOuts` (e.g. `master.key`), **When** pre-flight evaluates it, **Then** the command is refused directly — no escalation prompt is ever shown for this path.
- *Traces to:* FR-037.

**S47 — Delegate inherits a path widening** *(Error Path — mandatory adversarial case)*
- **Given** a parent session was granted `{P, write}` for bash, **When** the parent delegates to a subagent, **Then** the subagent's bash calls also resolve the widened policy (same `InheritFrom` mechanism as command grants) — the user should understand `Allow` on a bash escalation extends to delegates, not just the visible chat.
- *Traces to:* FR-017, FR-005.

**S58 — No kernel sandbox: filesystem pre-flight cannot positively clear anything** *(Error Path — founder decision 2, D9 Consequence 2)*
- **Given** `kernel_sandbox_active=false` (Windows, or Landlock-degraded, or the app-level fallback) and the global `auto_approve` default `true`, **When** an `ask`-resolved `bash` call that would otherwise stay inside the workspace is evaluated, **Then** it still prompts — the pre-flight has no confinement to compare the call against, so nothing can be classified as "stays inside," matching the "Auto → Ask" badge state.
- *Traces to:* FR-008, FR-014.

**S59 — In-process filesystem call reaching network is app-layer judged, not kernel-enforced** *(Error Path — founder decision 2, D9 Consequence 1)*
- **Given** `send_file` is tightened to `ask` by an operator and the global `auto_approve` default is `true`, **When** the agent calls it to attach a file from inside the workspace, **Then** the call is evaluated by `ResolveTurnFSPolicy`'s in-process result (never `DeriveKernelPolicy`, never a spawned child) and, being workspace-contained, auto-clears — the test asserts this happens through the app-layer path, not a kernel one, so a future change to `ResolveTurnFSPolicy`'s in-process evaluation is the thing that could silently break this guarantee, unlike `bash`'s kernel-backed equivalent.
- *Traces to:* FR-053.

**S60 — In-process tool call reaching the network is prompted under Auto-approve** *(Error Path — mandatory negative case, founder decision 2)*
- **Given** an operator has tightened `fetch_url` from its `allow` default (`pkg/config/defaults.go:230`) to `ask`, and the global `auto_approve` default is `true`, **When** the agent calls `fetch_url` against an external host, **Then** the call prompts — network access is never in Auto-approve's safe set (FR-052/FR-055) — even though no kernel sandbox confines `fetch_url` at all (`SSRFChecker` is the only guard it has today, and it is not a workspace-vs-network classifier).
- *Traces to:* FR-054, FR-055.

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
- **Given** `bash` resolves to `ask` with an active kernel sandbox, **When** `curl https://example.com` is evaluated, **Then** the classifier flags it, an escalation prompt shows before any process spawns — regardless of the Auto-approve setting, since a widening decision is never Auto-approve's (D9).
- *Traces to:* FR-042, FR-043.

**S51 — Approval widens network for the session** *(Happy Path)*
- **Given** the user approves the S50 escalation, **When** the same command runs again in the session, **Then** it runs without re-prompting (network grant recorded).
- *Traces to:* FR-044.

**S52 — Unclassified binary opening a raw socket is kernel-denied** *(Error Path — mandatory adversarial case)*
- **Given** a custom binary not in the classifier's known set, that opens a raw TCP socket, **When** it runs under `bash` resolved to `ask` with no prior grant, **Then** the kernel denies the `connect()` (empty `ConnectPortRules`) and the command fails with a clear error — not silently allowed.
- *Traces to:* FR-044 (honest gap).

**S53 — `git push` prompts via D8, not a name check** *(Happy Path — regression for D2's removed pattern)*
- **Given** the block list is gone, **When** `git push` runs under `bash` resolved to `ask` with no network grant, **Then** it still prompts — because `git` is in the FR-043 classifier's known set, not because a `git push` regex survived.
- *Traces to:* FR-043.

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

### 6.1 New / changed wire types — already built on `adr091/contracts` @ `e6a5cfd4e`; merge, don't re-author

The Auto-approve wire types (this section's first six rows) are **already implemented** on branch `adr091/contracts` (worktree `wt-adr091-contracts`), commit `e6a5cfd4e` — verified by direct read, not proposed here. Implementation merges that branch (or regenerates an equivalent diff) rather than re-authoring these schemas from scratch.

| Schema file | Change |
|---|---|
| `SandboxConfig.yaml` | **built:** `auto_approve` (boolean, global default, hot-reloaded, no `requires_restart`) |
| `SandboxConfigUpdate.yaml` | **built:** `auto_approve` write field on the existing `PUT /security/sandbox-config` body, documented as the routing target specifically because it already gates behind `requireReAuth` |
| `Agent.yaml`, `AgentUpdateRequest.yaml`, `AgentCreateRequestMain.yaml`, `AgentCreateRequestSubagent.yaml` | **built:** `auto_approve_disabled` (boolean, off-only by construction — no value means "on") |
| `SessionModeUpdateFrame.yaml` (+ inline copy, `asyncapi.yaml`) | **built:** `auto_approve` (nullable boolean: `true` loosens for the chat, `false` tightens, `null` clears the modifier) — the one field in this contract allowed to loosen |
| `SessionModeUpdatedFrame.yaml` (+ inline copy, `asyncapi.yaml`) | **built:** `auto_approve_effective` (non-nullable) — the session's resolved state after the write |
| `SandboxStatus.yaml` | **built, corrected from the first draft's `effective_mode`:** `kernel_sandbox_active` (D1's platform predicate) + `auto_approve_effective` (gateway-wide default, mirrors `SandboxConfig.auto_approve`) — **not** a new schema, and not the single `effective_mode` field this spec's first draft proposed |
| `ToolApprovalActionRequest.yaml` | **not yet built — this ADR's own work, unaffected by the founder's two decisions.** `action` enum becomes `deny \| allow_once \| allow \| cancel` (four values — **`cancel` retained**, resolves R-a); add `scope` (`exact`\|`prefix`) when `action == allow` |
| `ToolApprovalResponse.yaml` | echo the new enum + `scope` |
| **`contracts/asyncapi.yaml`'s INLINE `ToolApprovalRequiredFrame` schema (line ~3385)** | **primary edit target (resolves M-9)** — verified: `openapi.yaml` never `$ref`s the standalone `ToolApprovalRequiredFrame.yaml`; `asyncapi.yaml` carries its own inline copy, which is what `scripts/gen-contracts.sh` actually turns into the SPA's Zod types. Add the per-segment command list (resolved binary, args, classification) and `suggested_prefix`/no-prefix-flag here. Keep the standalone file in sync or delete it as a duplicate |
| `AgentShellPolicy.yaml`, `ExecAllowlist.yaml` | deleted in full |
| `Agent.yaml:153`, `AgentCreateRequestMain.yaml:121`, `AgentCreateRequestSubagent.yaml:117` | **added to the removal list (resolves M-10)** — all three `$ref` `AgentShellPolicy.yaml`; missing them breaks `make gen-contracts`. `AgentUpdateRequest.shell_policy` is **inlined**, not a `$ref` — remove the inline property, not "the reference" |

### 6.2 Endpoints/frames — decided, not left open

1. **Status read:** `GET /api/v1/security/sandbox-status` gains `kernel_sandbox_active`/`auto_approve_effective` (§6.1, already built) — no new endpoint.
2. **Per-chat modifier write:** `session_mode_update`, a session-scoped WS frame (already built, `asyncapi.yaml`), never written into `config.json` (FR-004); acknowledged by `session_mode_updated`.
3. **Global Auto-approve write:** the existing `PUT /security/sandbox-config` endpoint (`rest_sandbox_config.go`), gaining `auto_approve` alongside its existing body (already built) — inherits `requireReAuth` for free (FR-045).
4. **Per-agent write:** the existing `PUT /agents/{id}` endpoint, gaining `auto_approve_disabled` (already built).

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
**R-15 — Operator documentation (resolves M-23).** `command_rules` is config-file-only with no UI (FR-018/§11) — per CLAUDE.md's Definition of Done, an undocumented config-only key is not reachable by an operator. Add a task: document `command_rules`'s shape, decision order, and resolve-and-verify semantics (FR-040), plus the tool-policy/Auto-approve model (FR-001/FR-002), in operator-facing docs. Owned by L7 (§8.3), added to its DoD.

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
| L3 | security-lead | Filesystem pre-flight verdict and kernel rendering agree (Level 1 **and** Level 2, FR-012); **network pre-flight**: `bash`'s `ConnectPortRules` render empty by default whenever it resolves to `ask` (not "under Auto," D9) and widen correctly on grant (FR-042–044) |
| L7 | qa-lead + backend-lead | New guards wired and self-checking (incl. the named third guard); `command_rules`/tool-policy-and-Auto-approve operator documentation written (R-15) |

(L0, L1, L2, L4, L5, L6 unchanged from first draft.)

### 8.4 Serialisation points (SP-1…SP-5 unchanged; SP-2 now also covers L3's network half; SP-4 explicitly names `approvalgrants.go` as L4-owned, matching §8.2)

### 8.5 Lane count — eight, rationale restated honestly per §8.1.

---

## 9. Acceptance criteria (SC-xxx)

**SC-001 through SC-011 as first draft**, with:
- **SC-002** reworded: the anti-drift claim is scoped to what Level 1 (rendering agreement) and Level 2 (classifier agreement) each actually prove (FR-012); it does not claim agreement with live Landlock enforcement beyond the Linux-only spawned-child leg.
- **SC-004** extended: end-to-end demonstration covers both the filesystem widening (existing) and the network widening (FR-044).

**SC-012 — Global Auto-approve write requires re-auth.** A global `auto_approve` write with no fresh re-auth token is refused, matching God Mode's existing step-up behaviour (FR-045).

**SC-015 — Per-chat loosening is unreachable from `set_config`.** No sequence of `set_config`/`agent_apply_args` calls can turn Auto-approve on for a chat whose agent × global resolution has it off; only the `session_mode_update` frame can (FR-051, S56).

**SC-016 — In-process tool network access never auto-clears.** For every in-process tool tightened to `ask` in the test matrix (`fetch_url`, `send_file`, a browser tool), a call reaching the network prompts under Auto-approve in every run; a call staying inside the workspace auto-clears (FR-052–055, S59, S60).

**SC-013 — Secret set is never widenable.** An adversarial escalation naming a `CarveOuts` path is refused outright in every test run, never surfaced as a prompt (FR-037).

**SC-014 — Stale-client PUT gets 400, not silent drop.** A client still sending `shell_policy` to `PUT /agents/{id}` after removal gets a hard 400 (FR-003/R-2), not a 200 with the field silently dropped.

---

## 10. Open questions

1. **Per-turn single-value threading** — `guardCommand`, `turnKernelPolicy`, and now two pre-flight evaluators (D7, D8) each call `ResolveTurnFSPolicy`/the network equivalent independently. Cache-and-reuse vs. provably-safe independent calls: implementation decides.
2. **`ExecConfig.Approval` reachability** — confirm dead before deleting alongside §7.3.
3. **In-process network tools beyond `bash` (new, FR-054/FR-055).** D8's mechanism is `bash`-specific (it renders a kernel child's `ConnectPortRules`). No shared implementation exists yet for applying "network always prompts" to `fetch_url`/browser navigation the way D3's resolved-binary step is shared for `bash`. Implementation decides whether a common helper ships now or each in-process tool gets an honest, narrow, per-tool check first.

*(All other first-draft open questions are resolved by founder decisions and are recorded as FRs above: tool-policy/Auto-approve model — FR-001/FR-002; global-write auth — FR-045; widening data model — FR-036; network coverage — D8/FR-042–044; Windows grant scope — FR-041; what "safe" means — FR-052/FR-053.)*

---

## 11. Out of scope / future work

- Per-website network approvals; a session-grants management surface (FR-028); SPA display/editing of `command_rules`; the exec allowlist's per-domain semantics (retired, not re-homed); Windows kernel sandbox (still none); a shared network-classification helper for in-process tools beyond `bash` (open question 3).
- **Headless-turn stall under `ask` is a real, new consequence of this ADR, not "unchanged" (resolves M-16/R-k):** today a headless turn under the shipped `allow` ceiling never prompts. Under the new `ask` default (D1), a filesystem or network escalation (D7/D8) can fire with no operator attached, stalling to the existing `ToolApprovalTimeout` — Auto-approve being on does not prevent this, since a widening decision is never Auto-approve's to make (D9). Since Auto-approve can only tighten below the global default at agent/chat scope, an operator cannot exempt a headless agent from this short of God Mode. **This is stated here as an accepted consequence the operator must plan for, not built around** — no headless-specific handling ships in this ADR. On Windows specifically, `ask` always prompts regardless of Auto-approve (FR-008), so a headless Windows agent stalls on every shell call unless the global default is God Mode — worth a founder ruling before shipping if Windows headless agents are a real deployment target; flagged, not resolved here.

---

## 12. Traceability matrix

| Requirement | Representative scenario(s) | Test level |
|---|---|---|
| FR-001, FR-002 | S1, S2, S3, S54, S55, S57 | Unit |
| FR-003, S43 | S5, S6, S7, S43, S54 | Unit + E2E |
| FR-004, FR-006, FR-047 | S4, S10 | Unit |
| FR-005, S47 | S8, S9, S47 | Unit |
| FR-007, FR-008, FR-014 | S11–S14, S58 | Unit |
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
| FR-029, FR-050 | S26, (two-way disposition scenario set) | Integration |
| FR-030, FR-031 | (invariance regression) | Unit + audit |
| FR-032, FR-046 | S38, S39 | Unit |
| FR-033 | S40, S41 | E2E |
| FR-034 | S42 | E2E |
| FR-035 | §7 acceptance checks | Unit + guards |
| FR-042–044 | S50–S53 | Integration |
| FR-045 | S44 | E2E |
| FR-048 | S45 | Integration |
| FR-049 | (accepted-risk statement, no test) | — |
| FR-051 | S56 | Unit + E2E |
| FR-052 | S45, S50, S59, S60 | Integration |
| FR-053 | S59 | Integration |
| FR-054 | S60 | Integration |
| FR-055 | S60 | Integration |

Every FR appears above; FR-015 (corrected) and FR-018/FR-022/FR-028/FR-030/FR-031/FR-035 all now carry at least one scenario or acceptance-check reference, closing the first draft's matrix gap. FR-051–FR-055 (founder decisions 1 and 2, 2026-09-23) are new in this revision, each with its own scenario, including the mandatory negative cases (S54–S57, S59, S60).
