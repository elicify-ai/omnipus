# ADR-092 addendum — Auto-approve for tools other than `bash` (design, founder-ruled)

- **Status:** Founder-ruled 2026-09-23 (revision 3). Every open point is decided; nothing is left for the founder. Becomes ADR-092 **D9**.
- **Superseding decision (2026-09-24, founder, made with the risk stated to him):** ruling **J13 below is reversed**. Auto no longer requires an enforcing kernel sandbox for any tool, `bash` included — it now applies on Windows, when a sandbox failed to start, or in permissive mode, the same as everywhere else. Reason: the founder judged the old fallback a design flaw that made Auto effectively unusable on Windows and on any host without an enforcing sandbox. Accepted risk: without a kernel sandbox, `bash` under Auto is checked only by the D7/D8 pre-flights and the text-based guards — a disguised command can slip past a text-based check. See `docs/internal/architecture/ADR-092-shell-permission-modes.md`'s 2026-09-24 revision note for the full statement; every inline reference to J13 below is marked `[superseded 2026-09-24]` rather than rewritten in place, so this document's historical founder-ruling record stays intact.
- **Date:** 2026-09-23
- **Author:** architect
- **Baseline read:** `feat/adr-092-shell-permissions` @ `b7dc66acf`, plus `origin/adr092/fix-backend` @ `6bf3602b1` (the security-fix lane now editing `pkg/agent/loop_policy.go` and `pkg/agent/loop_run_turn_tools.go`). Every citation of those two files refers to the fix-backend version.
- **Authoritative per-tool list:** `/Users/danielpiatkowski/Desktop/auto-approve-choices.json` (saved 2026-09-23T14:20:18Z). It has 111 entries: 109 catalog tools and 2 MCP rules. Each entry's `choice` is either `runs` or `asks` (82 runs, 29 asks). §3 matches the file one-for-one.
- **No code changed.** This document is a design only.

### What changed from revision 1

| Revision 1 | Revision 2 (founder ruling) |
|---|---|
| Ask unless the tool is on a safe list (27 always-safe) | **Under Auto, every tool on Ask runs, except an explicit ask-list (28 catalog tools plus MCP tools the server marks destructive)**. The founder benchmarked this against Codex and Claude Code |
| Reads were open outside the secret set, the same as `bash` | File reads **and** writes run only inside the workspace or a mount. Reads outside ask. `bash` is unchanged |
| MCP tools asked by default, with an operator opt-in list | MCP tools **run unless the server marks them destructive** |
| `send_message` and `send_file` asked | Both **run** (founder change) |

---

## 1. Where things stand today

| Fact | Evidence |
|---|---|
| Auto only covers `bash`. Every other tool on Ask prompts, even with Auto on | `pkg/agent/loop_policy.go::bashShellModeFor` returns `""` for every tool except `bash`. `loop_run_turn_tools.go::resolveAskPolicy` skips the prompt only when `shellModePin == tools.ShellModeAuto` |
| The user docs already promise Auto for every tool, so that claim is false today | `docs/security.md` step 3; `docs/tools.md` |
| The whole "is Auto on?" check is inside `bash`'s mode resolver | `ShellPermissionGate.liveMode`: God Mode, then the ask policy, then `ResolveAutoApprove`, then `sandbox.TurnPolicyBaseInstalled()` |
| Scheduled runs deny every call on Ask before Auto is looked at, including a `bash` call Auto would allow | `resolveAskPolicy`: the `AutoDenyAsk` block runs before the `shellModePin` check. This contradicts the lead decision in `pkg/tools/shell_permission_mode.go::headlessShellDenyReason`. §5.4 fixes it (ruling J1) |

**Tools that ship on Ask on a new install.** These are the only calls this change affects until an operator switches more tools to Ask.
- Global: `bash`, `request_mount`, `environment_setup`, `delete_task`, `browser_upload_file`, `delete_workspace`, `remove_mcp_server`, `delete_task_in_workspace`.
- Per-agent: `send_email`, `reply`, `disable_channel`.
- For Mia, Jim and General Purpose: `knowledge_edit`, `knowledge_restructure`, `knowledge_configure`, `knowledge_base_create` (`pkg/coreagent/role_policies_adr090.go`).

**Effect on a new install with Auto on.** The four knowledge-base writing tools stop prompting. Everything else on that list still asks, because each is on the ask-list.

---

## 2. The rule

A call to any tool except `bash` **runs without a prompt**, and no approval grant is recorded, when all of the following hold. The check happens once per call, before the tool is dispatched.

1. The effective policy at execution time is `ask` (`resolveToolPolicyAtExec`). Auto never changes an `allow` or a `deny` result.
2. **Auto is active.** This is the same shared check `bash` uses:
   - God Mode is off, and
   - `ResolveAutoApprove(cfg, agentID, chatModifier)` is true.
   - *[superseded 2026-09-24, founder decision]* `sandbox.TurnPolicyBaseInstalled()` is **no longer** a third condition here — ruling J13 below is reversed; Auto no longer requires an enforcing kernel sandbox.
3. The tool's classification (§3) is **RUNS**, or it is **RUNS-IF** and this call's arguments meet the condition. For MCP tools, the tool is not marked destructive (§4).
4. The classifier ran without error. Any error (an unresolvable path, a bad argument, a nil dependency) counts as "asks". The classifier never refuses a call itself.

If any condition fails, the flow is what it is today:
- the session grant store is checked (`ApprovalGrants().IsAllowed`),
- then a human is prompted,
- or, in a scheduled or unattended run, the call is auto-denied with a clear error (J1).

**Workspace path rule for file arguments (ruling J2).** A path counts as inside when:
- the tool resolves it through **its own** `ResolveTurnFSPolicy(turnCtx, agentHome, restrict)` followed by `ResolvePath` or `ResolvePathAllowingPatterns` (same operation, same patterns), and
- the resolved real path is not in the secret set (`fspolicy.IsCarveOut`), and
- the path lies within `policy.WorkDir` or one of `policy.AllowedRoots` (mounts), tested with the same `fspolicy.CoversForGrant` primitive the D7 pre-flight uses.

This applies to **reads and writes alike**. No grant overlay is passed, so a path grant approved for `bash` (FR-036) never widens this rule.

**This differs deliberately from `bash`.**
- Under Auto, `bash` treats a read outside the secret set as contained (ADR-063 D2, `preflight.go::EvaluateFSPreflight`). `cat /etc/hosts` runs, while `read_file /etc/hosts` asks.
- The founder ruled that `bash` is **not** changed. This is recorded as an accepted asymmetry.
- The reason it cannot reuse `EvaluateFSPreflight` with `readConfined=true`: that function treats mounts as write-only grants, so a read inside a mount would ask. The non-bash rule counts mounts for reads too.

**Per call, not per tool.** Examples with an agent on Ask and Auto on:

| Call | Result |
|---|---|
| `write_file{path:"notes/a.md"}` | Inside the work folder, so it runs |
| `read_file{path:"work/<mount>/x.csv"}` | Inside a mount, so it runs |
| `read_file{path:"/etc/hosts"}` or `{path:"~/.ssh/id_rsa"}` | Outside, so it asks (J2) |
| `write_file{path:"/Users/x/Desktop/a.md"}` | Outside, so it asks |
| `send_file{path:"report.pdf"}` on Telegram | Inside, so it runs, and the file leaves the machine (founder decision, §3.2) |
| `delete_task{...}` | Ask-list, so it always asks |

---

## 3. Classification table (matches the founder file one-for-one)

**Verdicts:**
- **RUNS:** runs under Auto.
- **RUNS-IF:** runs only when the argument condition holds; otherwise it asks.
- **ASKS:** always asks under Auto. In an unattended run it is auto-denied.

**WS-PATH** means every path argument satisfies the §2 workspace path rule.

### 3.1 Files (10)

| Tool | Verdict | Condition / reason |
|---|---|---|
| `read_file` | RUNS-IF | WS-PATH (read) |
| `list_directory` | RUNS-IF | WS-PATH (read). The default path `.` is the work folder |
| `write_file` | RUNS-IF | WS-PATH (write) |
| `edit_file` | RUNS-IF | WS-PATH (read and write) |
| `append_file` | RUNS-IF | WS-PATH (write) |
| `grep` | RUNS | Its scope is always the work folder plus mounts |
| `library_list` | RUNS | `work/.library`, inside the work folder |
| `library_read` | RUNS | Same |
| `list_mounts` | RUNS | Read-only list of the caller's own mounts |
| `request_mount` | **ASKS** | Widens the sandbox |

### 3.2 Web and sending out (13)

| Tool | Verdict | Condition / reason |
|---|---|---|
| `search_web` | RUNS | Founder choice |
| `fetch_url` | RUNS | Founder choice. The existing private-address refusal in the tool is unchanged |
| `find_skills` | RUNS | Founder choice |
| `install_skill` | **ASKS** | Writes the skills folder for the whole install |
| `environment_setup` | **ASKS** | Arbitrary install script. ADR-090 requires the exact command in the approval |
| `serve_web` | **ASKS** | Dev mode runs a command without `bash`'s checks; static mode publishes files |
| `send_message` | RUNS | **Founder decision (changed from asks).** Messages go out on external channels (Telegram, Slack and others) with no prompt |
| `send_file` | RUNS-IF | **Founder decision (changed from asks).** Condition: WS-PATH (read), the same file-read rule as J2 (**founder decision 2026-09-23:** the condition stays; outside the workspace or a mount it asks). The consequence to state in the ADR: a file from the workspace leaves the machine to a third-party channel with no prompt |
| `send_email` | **ASKS** | External mail |
| `reply` | **ASKS** | External mail |
| `read_inbox` | RUNS | Founder choice |
| `search_email` | RUNS | Founder choice |
| `read_message` | RUNS | Founder choice. Side effect: the message is marked as read on the mail server |

### 3.3 Agents and tasks (21)

| Tool | Verdict | Condition / reason |
|---|---|---|
| `delegate` | RUNS | Founder choice. Each of the delegate's own calls is still checked under its own policy |
| `switch_agent` | RUNS | Founder choice |
| `message_parent` | RUNS | Founder choice |
| `list_agents` | RUNS | Read-only |
| `create_plan` | RUNS | Founder choice |
| `execute_plan` | RUNS | Founder choice |
| `plan_correct` | RUNS | Founder choice |
| `stop_plan` | RUNS | Containment |
| `run_task` | RUNS | Founder choice |
| `create_task` | RUNS | Founder choice. It still passes its own delegation-policy gate |
| `update_task` | RUNS | Founder choice |
| `delete_task` | **ASKS** | Irreversible delete |
| `list_tasks` | RUNS | Read-only |
| `list_jobs` | RUNS | Read-only |
| `inspect_session` | RUNS | Read-only; the tool limits it to the session under review |
| `set_goal` | RUNS | The session's own record |
| `goal_claim` | RUNS | Founder choice |
| `set_todos` | RUNS | The session's own scratchpad |
| `AskUserQuestion` | RUNS | It exists to ask the human |
| `Skill` | RUNS | Loads assigned skills |
| `ToolSearch` | RUNS | Moot: always resolved to allow (ManifestInfra) |

### 3.4 Memory and knowledge (12)

| Tool | Verdict | Condition / reason |
|---|---|---|
| `remember` | RUNS | Founder choice, both private and shared scope |
| `run_retrospective` | RUNS | Founder choice |
| `recall_memory` | RUNS | Read-only |
| `recall_conversation` | RUNS | Read-only, own session |
| `knowledge_list` | RUNS | Scope is always the work folder plus mounts (`knowledge/scope.go::ResolveScope`) |
| `knowledge_describe` | RUNS | Same |
| `knowledge_find` | RUNS | Same |
| `knowledge_read` | RUNS | Same |
| `knowledge_edit` | RUNS | Same. The ADR-090 Ask seed stops prompting under Auto |
| `knowledge_base_create` | RUNS | Same |
| `knowledge_restructure` | RUNS | Same |
| `knowledge_configure` | RUNS | Same |

### 3.5 Browser (18)

| Tool | Verdict | Condition / reason |
|---|---|---|
| `browser_navigate` | RUNS | Founder choice |
| `browser_open_tab` | RUNS | Founder choice |
| `browser_click` | RUNS | Founder choice |
| `browser_type` | RUNS | Founder choice |
| `browser_select_option` | RUNS | Founder choice |
| `browser_press_key` | RUNS | Founder choice |
| `browser_hover` | RUNS | Founder choice |
| `browser_handle_dialog` | RUNS | The tool still refuses `accept:true` itself in unattended runs (`WithAutoDenyAsk`) |
| `browser_wait` | RUNS | Observation only |
| `browser_get_text` | RUNS | Observation only |
| `browser_snapshot` | RUNS | Observation only |
| `browser_list_tabs` | RUNS | Observation only |
| `browser_screenshot` | RUNS-IF | Founder choice is `runs`, with the J2 write rule on its `filename` argument (it writes through `ResolvePath(FSOpWrite)`). **Founder decision 2026-09-23:** the condition stays; an outside filename asks |
| `browser_switch_tab` | RUNS | Founder choice |
| `browser_close_tab` | RUNS | Founder choice |
| `browser_handover` | RUNS | Brings the human in |
| `browser_evaluate` | **ASKS** | Arbitrary JavaScript |
| `browser_upload_file` | **ASKS** | Sends a local file to a website |

### 3.6 System / sysagent (35)

| Tool | Verdict | Tool | Verdict |
|---|---|---|---|
| `get_config` | RUNS | `set_config` | **ASKS** |
| `get_usage` | RUNS | `run_doctor` | **ASKS** |
| `list_providers` | RUNS | `configure_provider` | **ASKS** |
| `list_models` | RUNS | `test_provider` | **ASKS** |
| `list_channels` | RUNS | `enable_channel` | **ASKS** |
| `list_mcp_servers` | RUNS | `disable_channel` | **ASKS** |
| `get_agent` | RUNS | `configure_channel` | **ASKS** |
| `get_agent_tools` | RUNS | `test_channel` | **ASKS** |
| `read_agent_metadata` | RUNS | `add_mcp_server` | **ASKS** |
| `list_workspaces` | RUNS | `remove_mcp_server` | **ASKS** |
| `get_workspace` | RUNS | `create_agent` | **ASKS** |
| `create_workspace` | RUNS | `update_agent` | **ASKS** |
| `list_tasks_in_workspace` | RUNS | `delete_agent` | **ASKS** |
| `create_task_in_workspace` | RUNS | `update_workspace` | **ASKS** |
| `update_task_in_workspace` | RUNS | `delete_workspace` | **ASKS** |
| `list_skills` | RUNS | `delete_task_in_workspace` | **ASKS** |
| | | `create_skill` | **ASKS** |
| | | `edit_skill` | **ASKS** |
| | | `remove_skill` | **ASKS** |

### 3.7 `bash` (1)

Not in the founder file, and not handled by this classifier. It keeps ADR-092's own per-command checks (D3 rules, D7 filesystem, D8 network).

### 3.8 Tally

| Verdict | Count |
|---|---|
| RUNS (unconditional) | 74 |
| RUNS-IF (argument condition) | 7: `read_file`, `list_directory`, `write_file`, `edit_file`, `append_file`, `send_file`, `browser_screenshot` |
| **ASKS** | 28 |
| `bash` (own mechanism) | 1 |
| **Catalog total** | **110** |
| MCP rules | 2: runs only when the server marks the tool read-only or explicitly not destructive; everything else, including unlabelled tools, asks (§4) |

Reconciliation with the founder file:
- The file's 82 `runs` = 81 catalog tools (74 + 7) plus the MCP "not destructive" rule.
- The file's 29 `asks` = 28 catalog tools plus the MCP "destructive" rule.

Two tools in the file say `runs` but get an argument condition here: `browser_screenshot` and `send_file`. That is the J2 file-path rule applied to their file argument. **Founder decision 2026-09-23:** keep the condition. Each runs only when the file resolves inside the workspace or a mount; otherwise it asks.

---

## 4. MCP tools (connected servers)

**Rule (founder decision 2026-09-23, following the MCP specification):**
- Under Auto, an MCP tool on Ask **runs only when its server marks it read-only (`readOnlyHint: true`) or explicitly not destructive (`destructiveHint: false`)**.
- Every other MCP tool **asks**. That includes a tool with **no annotations at all**: the specification defaults `destructiveHint` to true when it is absent, and the hint only counts when `readOnlyHint` is false.
- Most servers send no annotations today, so in practice most MCP tools will still ask.

**Mechanism:** `pkg/tools/mcp_tool.go::MCPTool` already holds the server's `*mcp.Tool`. Its classifier reads `tool.Annotations` (go-sdk v1.4.1, `mcp.ToolAnnotations`):
- run only when `Annotations != nil`, and either `ReadOnlyHint` is true or `DestructiveHint` is non-nil and false;
- anything else asks.

**Notes for the ADR:**
- The SDK warns that annotations from untrusted servers are hints only. Omnipus relies on them because an operator chose to add the server, and `add_mcp_server` is on the ask-list. State this.
- ADR-090 still applies. An MCP tool needs an explicit server-and-tool assignment before it can run; Auto only removes the prompt for an Ask policy.

**No operator override ships now (founder decision 2026-09-23).**
- *Possible future work only:* a config-file-only per-tool override keyed by exact `mcp_<server>_<tool>` name, forcing `runs` or `asks`, for servers whose labels turn out to be wrong.
- It is not built, not in the contract, and not in any lane below.

---

## 5. Mechanism

### 5.1 One classifier the loop consults

**New file `pkg/tools/auto_approve.go`:**

```go
type AutoApproveClass uint8 // AutoAsks (ZERO VALUE) | AutoRuns | AutoRunsIfArgs
var autoApproveClasses = map[string]AutoApproveClass{ /* one entry per §3 row, transcribed from the founder file */ }
func AutoApproveClassOf(name string) AutoApproveClass // lookup miss → AutoAsks

type AutoVerdict struct {
    Run    bool
    Class  string       // "runs" | "runs_if_args" | "mcp_not_destructive" (for audit)
    Reason string
    Paths  []PinnedPath // {Real string; Access uint64}, RUNS-IF file tools only
}
type AutoApproveClassifier interface { // implemented by every AutoRunsIfArgs tool and by MCPTool
    AutoApproveVerdict(ctx context.Context, args map[string]any) AutoVerdict
}
// The J2 rule, written once: the tool's own ResolveTurnFSPolicy (no overlay) →
// ResolvePath/ResolvePathAllowingPatterns (own op + patterns) → !IsCarveOut &&
// CoversForGrant(WorkDir || any AllowedRoots), for read and write alike.
func AutoWorkspacePath(ctx context.Context, agentHome string, restrict bool, toolName string,
    op FSOp, raw string, patterns []*regexp.Regexp, access uint64) (PinnedPath, bool, string)
```

**New file `pkg/agent/auto_approve_gate.go`:**
- `autoApproveActive(agentID, sessionID) bool` is taken out of `ShellPermissionGate.liveMode`, so `bash` and every other tool share one Auto check.
- `(al *AgentLoop) autoApproveFor(ts, toolName, args) AutoVerdict`:
  - returns "asks" for `bash`, or when Auto is inactive;
  - for `AutoRuns`, returns Run;
  - for `AutoRunsIfArgs` and MCP tools, fetches the agent's registered tool (`ts.agent.Tools.Get`) and calls its classifier with `turnCtx`, which already carries the agent, workspace, work folder and `ReadConfined` values the tool will read;
  - an `AutoRunsIfArgs` tool with no classifier counts as "asks", and the §6 test fails.

**Loop change in `loop_run_turn_tools.go::resolveAskPolicy`:**

```
ex.shellModePin = bashShellModeFor(...)                  // unchanged
ex.autoPin      = al.autoApproveFor(ts, toolName, args)   // new
if toctouPolicy == "ask" {
    approved := shellModePin == Auto || ex.autoPin.Run ||
                bashRulesSettlePrompt(...) || grants.IsAllowed(...)
    if !approved && AutoDenyAsk { <existing unattended-run deny block> }  // MOVED after the approval checks (J1)
    if !approved { <existing pending placeholder + CheckGrantOrRequestApproval> }
    ...
}
```

### 5.2 Kernel sandbox requirement (ruling J13) — SUPERSEDED 2026-09-24

*Original ruling (2026-09-23), kept for the record:* Auto requires an enforcing kernel sandbox for **every** tool. That gives one rule and one badge. There is no Auto on Windows, in God Mode, or wherever the sandbox is not enforcing.

To state honestly in the ADR: non-bash tools run **inside the gateway process**. For them, the real boundary is the app-level check (the J2 path rule plus `ResolvePath`'s `os.Root` rooting). The kernel sandbox is the precondition for Auto, not what confines `write_file`.

**2026-09-24, founder decision:** this ruling is reversed. Auto no longer requires an enforcing kernel sandbox for any tool. There is Auto on Windows now, and wherever the sandbox is not enforcing — God Mode is still the one thing that turns Auto off, because it has no Auto machinery of its own. For non-bash tools this changes nothing about what confines them: the kernel sandbox was never their boundary (the J2 app-level check always was), so removing it as a precondition removes a gate that was never doing containment work for them in the first place. For `bash`, the consequence is real: without a kernel sandbox, the D7/D8 pre-flights and the text-based guards are the only checks. See `docs/internal/architecture/ADR-092-shell-permission-modes.md`'s 2026-09-24 revision note for the full statement of the accepted risk.

### 5.3 Pinning (one decision per call)

- `guardAndDispatch` pins the verdict: `tools.WithAutoApproved(execCtx, AutoPin{Tool, Paths})`, the same pattern as `withPinnedShellMode`.
- Each RUNS-IF file tool, after its own `ResolvePath`, calls `tools.RecheckAutoPin(ctx, policy, handle.RealPath(), access)`.
- If the path now fails the J2 rule (for example, a symlink was swapped between check and use), the tool **refuses** and nothing is read or written. It does not prompt again.
- Changing the Auto toggle mid-turn does not alter a call that has already been decided (the same as FR-006 for `bash`).

### 5.4 Unattended runs (ruling J1)

- A call that would run under Auto in a chat also runs in a scheduled or unattended run.
- Anything that would need a human (ask-list tools, a RUNS-IF call whose condition fails, a destructive MCP tool) is auto-denied with a clear error, through the existing `autoDenyHeadlessReason` and its audit rows.
- The §5.1 reorder also fixes `bash` in unattended runs. Correct the paragraph in `docs/security.md` that says Auto is "not consulted".

### 5.5 Audit

New event `tool.auto_approved`:
- A typed constant in `pkg/audit`, added to the event set in `audit.go`.
- Details: `{tool, agent_id, session_id, class, reason, paths, kernel_sandbox}`. `kernel_sandbox` *[added 2026-09-24, founder decision]* records `sandbox.TurnPolicyBaseInstalled()` at the moment of this call — since Auto no longer requires an enforcing kernel sandbox (§5.2), this is how an operator finds every auto-approval that ran unconfined by the kernel.
- Emitted once for each call Auto ran without a prompt, through `audit.EmitEntry`, so a failed write shows up in the degraded count.
- Calls that were prompted or denied keep their existing `tool.policy.ask.*` rows.

### 5.6 How Allow once and Allow interact

- The order is: Auto verdict, then the grant store, then the prompt. A call Auto runs never records a grant and never reads one.
- **Allow once:** nothing is recorded.
- **Allow:** records today's exact-arguments grant (`approvalGrantRecorder`).
- The prefix-scope choice stays `bash`-only, so the dialog hides the scope radio for other tools.
- A grant never turns a later, different call into an Auto call.
- Delegation inheritance is unchanged: grants and the per-chat Auto modifier pass to the delegate, except when the delegate's own `auto_approve_disabled` is set (fix-backend finding #6). The shared predicate now applies this to every tool.

### 5.7 Known interaction: two dialogs for one `bash` call (fixed in L3)

**What happens after fix-backend lands:**
- The `bash` policy resolves to plain `ask` (Auto off, so the mode is Ask — *[superseded 2026-09-24]* "or no kernel sandbox" no longer applies; a missing kernel sandbox no longer forces Ask), and the command matches an operator D3 `{action: ask}` rule.
- The user sees **two approval dialogs for one call**:
  1. `resolveAskPolicy` shows the generic upfront prompt, because `bashRulesSettlePrompt` returns false for an ask verdict.
  2. After approval, the tool's own `pkg/tools/shell_permission_mode.go::enforceShellPermissionMode` sees `verdictHasGenuineAskRuleMatch` in a non-God mode and calls `requestRuleApproval`, which prompts again.

**Fix (L3, with a small consumer change in the `bash` tool, see §9):** settle the D3 ask verdict inside the **same** upfront prompt.
- `resolveAskPolicy` evaluates the agent's own `ExecTool.EvaluateCommandRules` once (the evaluator `bashRulesSettlePrompt` already uses).
- When the verdict has a genuine ask-rule match, the upfront approval request carries the D3 context (`adr092_kind: "rule_ask"` and the matched rule) so the single dialog explains why it is asking.
- A human approval of that one prompt is pinned on the tool context (`withRuleAskSettled(ctx)`, the same pattern as `withPinnedShellMode`).
- `enforceShellPermissionMode` skips `requestRuleApproval` when that pin is present. It still enforces D3 `deny` and runs D7/D8.
- A denial or timeout of the upfront prompt ends the call exactly as today.
- In unattended runs, nothing changes: the call is auto-denied once.

---

## 6. Drift guard

New test `pkg/gateway/auto_approve_classification_test.go`, run against the same `buildCentralBuiltinRegistry` the gateway calls at boot:

1. **Coverage.** Every name in `buildCentralBuiltinRegistry(nil)` has an **explicit** entry in the table, read through an exported `tools.AutoApproveClassTable()`. A missing entry fails the test with the tool's name and the message "add an explicit entry; the default is ASKS".
2. **No stale entries.** Every table key is in that registry, or is `bash`.
3. **Every key in the global policy ceiling** (`defaultToolPolicyCeiling`, through its exported accessor) has an entry.
4. **Classifiers exist.** Every `AutoRunsIfArgs` tool's live instance implements `tools.AutoApproveClassifier`.
5. **Safe default.** `AutoApproveClassOf("no_such_tool") == AutoAsks`, and `AutoAsks` is the zero value. In the flipped model, a tool someone forgot to classify still asks.
6. **Ask-list lock.** A literal copy of the 28 ask-list names, as a golden list with a comment citing the founder file, must equal the table's `AutoAsks` set. Moving a tool off the ask-list then needs a deliberate, reviewable edit in two places.
7. **No MCP names in the table.** No table key starts with `mcp_`; MCP tools always go through §4.

---

## 7. UI and docs

| Surface | Change |
|---|---|
| `SecuritySection.tsx::AutoApproveControl` confirmation text | *[superseded 2026-09-24, founder decision]* Original text (2026-09-23): "...Needs an active kernel sandbox." This sentence is no longer true — Auto no longer needs one. *[superseded again 2026-09-24, founder decision A — "like Claude Code default": a no-sandbox shell command now asks first unless read-only or operator-allowed, so "checked by reading the command text only" is itself stale]* The Security card and the turn-on confirmation dialog now say: "Without a kernel sandbox (for example on Windows), shell commands ask first, except read-only ones and commands an operator rule allows." <!-- verify-ui-string --> |
| `ChatModeBadge.tsx` "Auto — no sandbox" tooltip | *[superseded 2026-09-24, founder decision — the original row here was the "Auto → Ask" tooltip, "No enforcing kernel sandbox: every tool set to "ask" prompts", which is no longer true]* The badge no longer degrades to Ask; instead it keeps the "Auto — no sandbox" label. *[superseded again 2026-09-24, founder decision A]* Its tooltip no longer says shell commands are "checked by text only" — it now reads: "No kernel sandbox is enforcing. Safe tool calls still run without asking; shell commands ask first, except read-only ones and commands an operator rule allows." <!-- verify-ui-string --> |
| `ToolsAndPermissions.tsx` and the global tool-policy table | A small read-only "Under Auto: runs / runs if inside workspace / asks" marker on rows set to Ask. **Contract first:** `auto_approve: enum[runs, runs_if_args, asks]` on `contracts/components/schemas/ToolRegistryEntry.yaml`; regenerate; fill it from `tools.AutoApproveClassOf` in `rest_tool_registry.go`. MCP rows show `runs` or `asks` from the annotation (J14) |
| `ToolApprovalModal.tsx` | Hide the scope radio for tools other than `bash` |
| `docs/security.md` | Rewrite "What 'safe' means" as "What Auto skips and what still asks": the ask-list in plain groups; the file-path rule and its difference from shell commands (`cat /etc/hosts` runs in the shell, `read_file /etc/hosts` asks); **messages and files sent to chat channels go out with no prompt**; the MCP destructive rule; unattended runs follow the same rule (fix the paragraph that says otherwise); *[superseded 2026-09-24, founder decision — the original row here said "no Auto on Windows or without an enforcing sandbox"]* Auto now applies on Windows and without an enforcing sandbox too — state plainly what Auto checks in that case (D7/D8 pre-flights + text-based guards, no kernel boundary) and the concrete risk example (a disguised command can slip past a text-based check) |
| `docs/tools.md` | One paragraph that points to the per-tool column in the reference |
| `docs/reference/built-in-tools.md` (generated by `cmd/docsref`) | Add an "Under Auto" column generated from the table, so the docs cannot drift |
| ADR-092 | Add **D9** (this rule, table reference, mechanism, the founder decision on `send_message`/`send_file`, the J2 asymmetry with `bash`). Change D1's Auto sentence to cover every tool |

---

## 8. Decisions (all ruled 2026-09-23 unless marked)

| # | Question | Ruling |
|---|---|---|
| J1 | Does Auto apply to unattended runs? | **Yes, the same rule.** Anything that needs a human is auto-denied with a clear error. The loop reorder in §5.1 also fixes `bash` |
| J2 | File reads and writes | **Run only inside the workspace or a mount; everything else asks, for reads too.** Stricter than `bash`'s read rule; `bash` is not changed, and the asymmetry is documented |
| J3 | `environment_setup` | **Asks** |
| J4 | `serve_web` | **Asks** |
| J5 | `send_file` | **Runs (founder decision)**, subject to the J2 path rule on the file it reads. Files leave the machine on non-web channels with no prompt |
| — | `send_message` | **Runs (founder decision).** Messages go out on external channels with no prompt. `send_email` and `reply` still **ask** |
| J6 | `AskUserQuestion`, `browser_handover` | **Run** |
| J7 | `delegate`, `message_parent`, `switch_agent` | **Run** |
| J8 | Task, plan and goal tools | **Run**, except `delete_task`, which **asks** |
| J9 | Read-only listings (own and install-wide) | **Run**, including `list_agents`, `list_workspaces`, `get_config`, `get_usage`, and the `get_*`/`list_*` system reads |
| J10 | Memory | **Runs**, all scopes |
| J11 | Knowledge-writing tools | **Run** (the fresh-install Ask seeds for Mia, Jim and General Purpose stop prompting under Auto) |
| J12 | Browser | **Runs**, except `browser_evaluate` and `browser_upload_file`, which **ask** |
| J13 | Kernel sandbox required for every tool? | *[superseded 2026-09-24, founder decision]* Originally: **Yes.** One rule, one badge. No Auto on Windows or where the sandbox is not enforcing. **Now: No, as of 2026-09-24.** Auto applies on Windows and wherever the sandbox is not enforcing; only God Mode still turns it off |
| J14 | Show each tool's Auto verdict in the UI | **In (founder decision 2026-09-23).** Contract field `auto_approve` on `ToolRegistryEntry` (L5); a per-row marker in the settings screens (L6) |
| J15 | MCP | **Founder decision 2026-09-23: follow the MCP specification.** A tool runs only when marked read-only or explicitly not destructive; unlabelled tools count as destructive and ask. No operator override ships; it is future work only |
| — | `browser_screenshot`, `send_file` path condition | **Kept (founder decision 2026-09-23).** Each runs only when the file resolves inside the workspace or a mount; otherwise it asks |

---

## 9. Implementation plan

**Sequence:** L3 edits `loop_policy.go` and `loop_run_turn_tools.go`, so it **starts only after `origin/adr092/fix-backend` merges** into `feat/adr-092-shell-permissions`. All other lanes can start now. Each lane owns its files exclusively.

| Lane | Owner | Files (exclusive) | Work |
|---|---|---|---|
| **L1** Classifier core and file tools | backend-lead | `pkg/tools/auto_approve.go` (new) and its `_test.go`, `pkg/tools/filesystem.go`, `pkg/tools/edit.go`, `pkg/tools/send_file.go` | Table (from the founder file), types, `AutoWorkspacePath`, `WithAutoApproved`/`RecheckAutoPin`; classifiers and re-checks on `read_file`, `list_directory`, `write_file`, `append_file`, `edit_file`, `send_file` |
| **L2** MCP and browser classifiers | backend-lead (second writer) | `pkg/tools/mcp_tool.go`, `pkg/tools/browser/tools.go` | `MCPTool.AutoApproveVerdict` (annotation rule, §4); `browser_screenshot` classifier and re-check |
| **L3** Loop wiring | backend-lead | `pkg/agent/auto_approve_gate.go` (new) and its test, `pkg/agent/loop_policy.go`, `pkg/agent/loop_run_turn_tools.go`, `pkg/tools/shell_permission_mode.go` | Extract `autoApproveActive`; `autoApproveFor`; `resolveAskPolicy` reorder (J1); pin in `guardAndDispatch`; `autoPin` field; **§5.7 one-dialog fix**: the D3 ask verdict is settled in the upfront prompt and `withRuleAskSettled` is pinned. The matching skip in `pkg/tools/shell_permission_mode.go::enforceShellPermissionMode` is also L3's (that file is owned by no other lane here) |
| **L4** Audit | security-lead | `pkg/audit/tool_auto_approve_events.go` (new), `pkg/audit/audit.go` | `tool.auto_approved` event and emitter |
| **L5** Contract, gateway, guard, generated docs | backend-lead | `contracts/components/schemas/ToolRegistryEntry.yaml` plus regenerated `pkg/api/generated/`, `src/lib/api/generated/`; `pkg/gateway/rest_tool_registry.go`; `pkg/gateway/auto_approve_classification_test.go` (new); `cmd/docsref/*` | `auto_approve` field (J14, decided); the §6 drift guard; generated column |
| **L6** Frontend | frontend-lead (load `omnipus-design-system` first) | `src/components/settings/SecuritySection.tsx`, `src/components/chat/ChatModeBadge.tsx`, `src/components/agents/ToolsAndPermissions.tsx`, `src/components/chat/ToolApprovalModal.tsx` | Copy, per-row marker, scope radio for `bash` only. **Rebase onto `origin/adr092/fix-frontend` first**: it touches the badge |
| **L7** Docs | architect / docs | `docs/security.md`, `docs/tools.md`, `docs/internal/architecture/ADR-092-shell-permission-modes.md` (D9), `docs/internal/specs/adr-092-shell-permission-modes-spec.md` (new FRs) | §7 text; record the rulings |

### Tests

Expected values come from this document and the founder file, never from the implementation.

| # | Test | Lane |
|---|---|---|
| T1 | `write_file` on Ask, Auto on, kernel sandbox enforcing: a path in the work folder runs with no approver call; a path inside a mount runs; a Desktop path prompts once | L1+L3 |
| T2 | `read_file`: inside the work folder or a mount runs; `/etc/hosts` prompts (J2); a secret-set path prompts and the tool then refuses | L1 |
| T3 | Every ASKS tool on Ask with Auto on still prompts. Table-driven over the 28 golden names, with a recording approver | L3 |
| T4 | A sample of unconditional RUNS tools (`delegate`, `browser_navigate`, `send_message`, `knowledge_edit`, `get_config`) on Ask run with zero approver calls | L3 |
| T5 | Auto off: T4's tools prompt. *[superseded 2026-09-24, founder decision — the original row also covered "Auto on without a kernel sandbox: T4's tools prompt (J13)"; that is now T5b, with the OPPOSITE assertion]* | L3 |
| T5b | *[added 2026-09-24, founder decision]* Auto ON with no kernel sandbox: T4's tools run with zero approver calls, exactly as with a kernel sandbox enforcing | L3 |
| T6 | Pin re-check: the classifier approves `a.md`; before dispatch it is swapped to a symlink pointing outside; the tool refuses and nothing is written | L1 |
| T7 | A `bash` path grant does not make `write_file` to that path run | L1 |
| T8 | Unattended run: a RUNS tool executes; an ASKS tool is auto-denied with `autoDenyHeadlessReason` and its audit rows; **`bash` under Auto executes** (a J1 regression, which fails today by the §1 analysis) | L3 |
| T9 | MCP: `readOnlyHint:true` runs; `destructiveHint:false` runs; `destructiveHint:true` asks; **no annotations asks**; `Annotations` present with `DestructiveHint` nil and `ReadOnlyHint` false asks (founder decision, following the MCP specification) | L2 |
| T10 | `send_file` of a workspace file on a non-web channel runs with no prompt; a file outside the workspace asks | L1 |
| T11 | An auto-approved call writes exactly one `tool.auto_approved` row with the right `class`; a prompted call writes none | L4+L3 |
| T12 | An auto-approved call records no grant (`IsAllowed` stays false) | L3 |
| T13 | The drift guard's seven checks (§6), each with a mutation self-check: delete one entry, or move one tool off the golden ask-list, and the test must fail | L5 |
| T14 | God Mode: an agent-level Ask on `write_file` still prompts (no sandbox, so Auto is inactive) | L3 |
| T15 | A delegate whose own `auto_approve_disabled` is set: its `write_file` inside the workspace still prompts under a parent chat with Auto on | L3 |
| T16 | `GET /api/v1/tools` carries `auto_approve` for every entry; `make verify-contracts` is clean | L5 |
| T17 | Frontend: no scope radio for tools other than `bash`; new Security copy; markers only on rows set to Ask | L6 |
| T18 | One dialog (§5.7): `bash` policy `ask`, mode Ask, a command matching an operator `{action: ask}` rule. A recording approver is called **exactly once**, and that request carries `adr092_kind: "rule_ask"`. Approving it runs the command; denying it runs nothing. A matching D3 `deny` rule still refuses with zero approver calls. Headless: auto-denied with zero approver calls | L3 |

**Definition of done:** reachability, not just green tests. On a fresh install with the UAT provider and Auto on:
1. Mia's `knowledge_edit` and an in-workspace `write_file` run with **no approval card**.
2. `read_file /etc/hosts` and `delete_task` **show one**.
3. A scheduled run with an ASKS tool shows the auto-deny error.

Record the screenshots.
