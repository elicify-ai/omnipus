// ADR-092 D9 — Auto-approve for tools other than bash
// (docs/internal/specs/adr-092-auto-for-other-tools-design.md, §2/§3/§5).
//
// # What this file decides
//
// Under Auto, a call whose effective policy is "ask" runs without a prompt
// when this file's classifier says it may. Everything else keeps today's
// flow (grant store, then a human prompt, or the unattended auto-deny).
// This file never decides whether Auto is ACTIVE (God Mode off,
// ResolveAutoApprove true, an enforcing kernel sandbox) — that is the agent
// loop's shared check — and it never turns an allow or deny into anything
// else. It only answers: "given Auto is on and this call would ask, may it
// run?"
//
// # API for the agent loop (lane L3)
//
//	ClassifyAutoApprove(ctx, toolName, tool, args) AutoVerdict
//
// The one per-call entry point. ctx MUST be the same turn context the tool's
// Execute will receive (it carries the agent id, workspace id, work folder
// re-root and ReadConfined facts that ResolveTurnFSPolicy reads), tool is
// the agent's own registered instance (ts.agent.Tools.Get(toolName)) or nil,
// args are the call's arguments. It returns Run=false ("asks") for:
//
//   - bash (it has its own ADR-092 mechanism; class AutoShellMode),
//   - every ask-list tool (class AutoAsks),
//   - any name not in the table unless it is an MCP tool whose own
//     classifier approves it (the default for an unknown tool is ASKS),
//   - a RUNS-IF tool whose arguments fail its condition, whose instance is
//     nil or does not implement AutoApproveClassifier, or whose classifier
//     panics.
//
// It returns Run=true for every unconditional RUNS tool, and for a RUNS-IF
// or MCP tool whose own classifier approves this call's arguments.
//
//	WithAutoApproved(ctx, AutoPin) context.Context
//	AutoPinFrom(ctx) (AutoPin, bool)
//	AutoPinForVerdict(toolName, AutoVerdict) AutoPin
//
// When the loop dispatches a call because Auto ran it, it pins the decision
// on the tool's execution context with WithAutoApproved(execCtx,
// AutoPinForVerdict(toolName, verdict)) — the same pattern as
// withPinnedShellMode. The pin is what makes the decision once-per-call:
// each RUNS-IF file tool re-checks its FINAL resolved path against the pin
// (RecheckAutoPin) and refuses — without prompting again — if the path no
// longer satisfies the workspace rule (for example a symlink swapped between
// classification and use). A call dispatched without a pin (human-approved,
// or policy allow) is never affected by the re-check.
//
// # The workspace path rule (§2, founder ruling J2)
//
// AutoWorkspacePath is the rule, written once: the tool's OWN
// ResolveTurnFSPolicy (no grant overlay, so a bash path grant never widens
// it), then the tool's OWN ResolvePathAllowingPatterns (same op, same
// patterns), then: not in the secret set (fspolicy.IsCarveOut) AND within
// policy.WorkDir or one of policy.AllowedRoots (mounts), tested with
// fspolicy.CoversForGrant. Reads and writes alike: a read outside the
// workspace and its mounts asks, even though the tool itself would permit
// it once a human approves. Because the classifier resolves through the
// exact functions the tool uses, the two can never disagree about which
// file a path names.
package tools

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// AutoApproveClass is a tool's Auto-approve classification (§3). The zero
// value is AutoAsks, so a tool nobody classified still asks.
type AutoApproveClass uint8

const (
	// AutoAsks: always asks under Auto; auto-denied in an unattended run.
	AutoAsks AutoApproveClass = iota
	// AutoRuns: runs under Auto whatever the arguments.
	AutoRuns
	// AutoRunsIfArgs: runs only when this call's arguments meet the tool's
	// condition (the J2 workspace path rule for every current member). The
	// tool's live instance must implement AutoApproveClassifier.
	AutoRunsIfArgs
	// AutoShellMode: bash only. Decided by bash's own ADR-092 shell mode
	// (D3 rules, D7 filesystem, D8 network), never by this classifier. It
	// is a distinct class so the table can list bash explicitly without
	// putting it on the 28-name ask-list.
	AutoShellMode
)

// String returns the stable wire/audit spelling of the class.
func (c AutoApproveClass) String() string {
	switch c {
	case AutoRuns:
		return "runs"
	case AutoRunsIfArgs:
		return "runs_if_args"
	case AutoShellMode:
		return "shell_mode"
	default:
		return "asks"
	}
}

// Verdict class strings recorded on AutoVerdict.Class (and so in the
// tool.auto_approved audit row).
const (
	AutoVerdictClassRuns              = "runs"
	AutoVerdictClassRunsIfArgs        = "runs_if_args"
	AutoVerdictClassMCPNotDestructive = "mcp_not_destructive"
	AutoVerdictClassAsks              = "asks"
)

// autoApproveClasses is the §3 table, transcribed one-for-one from the
// founder file /Users/danielpiatkowski/Desktop/auto-approve-choices.json
// (saved 2026-09-23T14:20:18Z): every catalog tool listed explicitly, plus
// bash. "runs" entries become AutoRuns, except the seven whose file
// argument carries the J2 path rule (AutoRunsIfArgs); "asks" entries
// become AutoAsks. MCP tools are never listed here (§4) — they are
// classified by their own annotations.
var autoApproveClasses = map[string]AutoApproveClass{

	// Files
	"read_file":      AutoRunsIfArgs,
	"list_directory": AutoRunsIfArgs,
	"write_file":     AutoRunsIfArgs,
	"edit_file":      AutoRunsIfArgs,
	"append_file":    AutoRunsIfArgs,
	"grep":           AutoRuns,
	"library_list":   AutoRuns,
	"library_read":   AutoRuns,
	"list_mounts":    AutoRuns,
	"request_mount":  AutoAsks,

	// Web
	"search_web":        AutoRuns,
	"fetch_url":         AutoRuns,
	"find_skills":       AutoRuns,
	"install_skill":     AutoAsks,
	"environment_setup": AutoAsks,
	"serve_web":         AutoAsks,

	// Sending out
	"send_message": AutoRuns,
	"send_file":    AutoRunsIfArgs,
	"send_email":   AutoAsks,
	"reply":        AutoAsks,
	// create_email_draft only appends to the mailbox's own Drafts folder and
	// never sends, so it runs under Auto like the other non-sending email
	// tools (founder ruling 2026-09-26, decision D45); D33: attachments add
	// NO separate touch point, this entry governs the whole call.
	"create_email_draft": AutoRuns,
	"read_inbox":         AutoRuns,
	"search_email":       AutoRuns,
	"read_message":       AutoRuns,

	// Agents & tasks
	"delegate":        AutoRuns,
	"switch_agent":    AutoRuns,
	"message_parent":  AutoRuns,
	"list_agents":     AutoRuns,
	"create_plan":     AutoRuns,
	"execute_plan":    AutoRuns,
	"plan_correct":    AutoRuns,
	"stop_plan":       AutoRuns,
	"run_task":        AutoRuns,
	"create_task":     AutoRuns,
	"update_task":     AutoRuns,
	"delete_task":     AutoAsks,
	"list_tasks":      AutoRuns,
	"list_jobs":       AutoRuns,
	"inspect_session": AutoRuns,
	"set_goal":        AutoRuns,
	"goal_claim":      AutoRuns,
	"set_todos":       AutoRuns,
	"AskUserQuestion": AutoRuns,
	"Skill":           AutoRuns,
	"ToolSearch":      AutoRuns,

	// Memory & knowledge
	"remember":              AutoRuns,
	"run_retrospective":     AutoRuns,
	"recall_memory":         AutoRuns,
	"recall_conversation":   AutoRuns,
	"knowledge_list":        AutoRuns,
	"knowledge_describe":    AutoRuns,
	"knowledge_find":        AutoRuns,
	"knowledge_read":        AutoRuns,
	"knowledge_edit":        AutoRuns,
	"knowledge_base_create": AutoRuns,
	"knowledge_restructure": AutoRuns,
	"knowledge_configure":   AutoRuns,

	// Browser
	"browser_navigate":      AutoRuns,
	"browser_open_tab":      AutoRuns,
	"browser_click":         AutoRuns,
	"browser_type":          AutoRuns,
	"browser_select_option": AutoRuns,
	"browser_press_key":     AutoRuns,
	"browser_hover":         AutoRuns,
	"browser_handle_dialog": AutoRuns,
	"browser_wait":          AutoRuns,
	"browser_get_text":      AutoRuns,
	"browser_snapshot":      AutoRuns,
	"browser_list_tabs":     AutoRuns,
	"browser_screenshot":    AutoRunsIfArgs,
	"browser_switch_tab":    AutoRuns,
	"browser_close_tab":     AutoRuns,
	"browser_handover":      AutoRuns,
	"browser_evaluate":      AutoAsks,
	"browser_upload_file":   AutoAsks,

	// System (settings)
	"get_config": AutoRuns,
	"set_config": AutoAsks,
	"run_doctor": AutoAsks,
	"get_usage":  AutoRuns,

	// System (providers)
	"list_providers":     AutoRuns,
	"list_models":        AutoRuns,
	"configure_provider": AutoAsks,
	"test_provider":      AutoAsks,

	// System (channels)
	"list_channels":     AutoRuns,
	"enable_channel":    AutoAsks,
	"disable_channel":   AutoAsks,
	"configure_channel": AutoAsks,
	"test_channel":      AutoAsks,

	// System (MCP)
	"list_mcp_servers":  AutoRuns,
	"add_mcp_server":    AutoAsks,
	"remove_mcp_server": AutoAsks,

	// System (agents)
	"get_agent":           AutoRuns,
	"get_agent_tools":     AutoRuns,
	"read_agent_metadata": AutoRuns,
	"create_agent":        AutoAsks,
	"update_agent":        AutoAsks,
	"delete_agent":        AutoAsks,

	// System (workspaces)
	"list_workspaces":          AutoRuns,
	"get_workspace":            AutoRuns,
	"create_workspace":         AutoRuns,
	"update_workspace":         AutoAsks,
	"delete_workspace":         AutoAsks,
	"list_tasks_in_workspace":  AutoRuns,
	"create_task_in_workspace": AutoRuns,
	"update_task_in_workspace": AutoRuns,
	"delete_task_in_workspace": AutoAsks,

	// System (skills)
	"list_skills":  AutoRuns,
	"create_skill": AutoAsks,
	"edit_skill":   AutoAsks,
	"remove_skill": AutoAsks,

	// Shell (§3.7)
	"bash": AutoShellMode,
}

// AutoApproveClassOf returns name's classification. A name missing from the
// table is AutoAsks: in the flipped model a tool someone forgot to classify
// still asks.
func AutoApproveClassOf(name string) AutoApproveClass {
	return autoApproveClasses[name]
}

// AutoApproveClassTable returns a copy of the whole table, for the gateway's
// drift guard and the generated docs. Mutating the copy changes nothing.
func AutoApproveClassTable() map[string]AutoApproveClass {
	out := make(map[string]AutoApproveClass, len(autoApproveClasses))
	for name, class := range autoApproveClasses {
		out[name] = class
	}
	return out
}

// PinnedPath is one resolved path an approving classifier checked: Real is
// the resolved absolute path, Access the fspolicy.PathGrantAccessRead|
// PathGrantAccessWrite bitmask the call needs on it.
type PinnedPath struct {
	Real   string
	Access uint64
}

// AutoVerdict is the classifier's answer for one call. Run=false means
// "asks": the loop falls through to the grant store and the prompt (or the
// unattended auto-deny). Class and Reason are recorded in the audit row;
// Paths is set only by the RUNS-IF file tools, for the pin and the audit.
type AutoVerdict struct {
	Run    bool
	Class  string
	Reason string
	Paths  []PinnedPath
}

// AutoApproveClassifier is implemented by every AutoRunsIfArgs tool and by
// MCPTool. It must never refuse the call itself — an argument it cannot
// evaluate is an "asks" verdict, not an error.
type AutoApproveClassifier interface {
	AutoApproveVerdict(ctx context.Context, args map[string]any) AutoVerdict
}

// autoAsks builds an "asks" verdict with a reason.
func autoAsks(reason string) AutoVerdict {
	return AutoVerdict{Class: AutoVerdictClassAsks, Reason: reason}
}

// ClassifyAutoApprove is the per-call entry point for the agent loop; see
// the file header for the full contract. It assumes Auto is already active
// and the call's effective policy is "ask" — checking those is the caller's
// job.
func ClassifyAutoApprove(ctx context.Context, toolName string, tool Tool, args map[string]any) AutoVerdict {
	class, listed := autoApproveClasses[toolName]
	if !listed {
		if !strings.HasPrefix(toolName, "mcp_") {
			return autoAsks(fmt.Sprintf("%s has no Auto-approve classification; unclassified tools ask", toolName))
		}
		return classifyWithInstance(ctx, toolName, tool, args)
	}
	switch class {
	case AutoRuns:
		return AutoVerdict{Run: true, Class: AutoVerdictClassRuns, Reason: toolName + " runs under Auto"}
	case AutoRunsIfArgs:
		return classifyWithInstance(ctx, toolName, tool, args)
	case AutoShellMode:
		return autoAsks("bash is decided by its own shell permission mode, not the Auto-approve classifier")
	default:
		return autoAsks(toolName + " is on the Auto-approve ask-list")
	}
}

// classifyWithInstance delegates to the tool's own AutoApproveClassifier.
// A missing instance, a missing implementation or a panic is "asks".
func classifyWithInstance(ctx context.Context, toolName string, tool Tool, args map[string]any) (verdict AutoVerdict) {
	if tool == nil {
		return autoAsks(toolName + ": no registered instance to classify the call")
	}
	classifier, ok := tool.(AutoApproveClassifier)
	if !ok {
		return autoAsks(toolName + ": tool does not implement an Auto-approve classifier")
	}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("auto-approve: classifier panicked; the call asks", "tool", toolName, "panic", r)
			verdict = autoAsks(fmt.Sprintf("%s: classifier failed: %v", toolName, r))
		}
	}()
	verdict = classifier.AutoApproveVerdict(ctx, args)
	if !verdict.Run {
		verdict.Class = AutoVerdictClassAsks
		verdict.Paths = nil
	}
	return verdict
}

// AutoWorkspacePath applies the §2 workspace path rule (J2) to one path
// argument, resolving it exactly the way the calling tool will:
// ResolveTurnFSPolicy(ctx, agentHome, restrict) with no grant overlay, then
// ResolvePathAllowingPatterns with the tool's own op and patterns. It
// returns the pinned resolved path and true when the path is outside the
// secret set and within the work folder or a mount; otherwise false and the
// reason. Any resolution error is a false (asks), never a refusal.
func AutoWorkspacePath(
	ctx context.Context,
	agentHome string,
	restrict bool,
	toolName string,
	op FSOp,
	raw string,
	patterns []*regexp.Regexp,
	access uint64,
) (PinnedPath, bool, string) {
	policy, err := ResolveTurnFSPolicy(ctx, agentHome, restrict)
	if err != nil {
		return PinnedPath{}, false, fmt.Sprintf("filesystem policy could not be resolved: %v", err)
	}
	handle, err := ResolvePathAllowingPatterns(ctx, policy, toolName, "", op, raw, patterns)
	if err != nil {
		return PinnedPath{}, false, fmt.Sprintf("path %q could not be resolved: %v", raw, err)
	}
	realPath, realErr := handle.RealPath()
	if closeErr := handle.Close(); closeErr != nil {
		slog.Warn("auto-approve: closing classifier path handle failed", "tool", toolName, "error", closeErr)
	}
	if realErr != nil {
		return PinnedPath{}, false, fmt.Sprintf("path %q has no resolved location: %v", raw, realErr)
	}
	if ok, reason := autoPathCovered(policy, realPath); !ok {
		return PinnedPath{}, false, reason
	}
	return PinnedPath{Real: realPath, Access: access}, true, ""
}

// autoPathCovered is the J2 containment test on an already-resolved path:
// not a secret, and inside WorkDir or a mount. It uses the policy's own
// AllowedRoots — never an allow-pattern grant ResolvePathAllowingPatterns
// may add for a single call — so the operator's regex axis does not widen
// what Auto runs.
func autoPathCovered(policy fspolicy.FSPolicy, realPath string) (bool, string) {
	if fspolicy.IsCarveOut(realPath, policy) {
		return false, fmt.Sprintf("%q is in the protected secret set", realPath)
	}
	if policy.WorkDir != "" && fspolicy.CoversForGrant(policy.WorkDir, realPath) {
		return true, ""
	}
	for _, root := range policy.AllowedRoots {
		if fspolicy.CoversForGrant(root, realPath) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("%q is outside the workspace and its mounts", realPath)
}

// autoWorkspaceVerdict is the whole classifier for a single-path RUNS-IF
// file tool: the J2 rule on one argument.
func autoWorkspaceVerdict(
	ctx context.Context,
	agentHome string,
	restrict bool,
	toolName string,
	op FSOp,
	raw string,
	patterns []*regexp.Regexp,
	access uint64,
) AutoVerdict {
	pinned, ok, reason := AutoWorkspacePath(ctx, agentHome, restrict, toolName, op, raw, patterns, access)
	if !ok {
		return autoAsks(reason)
	}
	return AutoVerdict{
		Run:    true,
		Class:  AutoVerdictClassRunsIfArgs,
		Reason: toolName + " path is inside the workspace or a mount",
		Paths:  []PinnedPath{pinned},
	}
}

// AutoPin is the Auto decision pinned on one call's execution context.
type AutoPin struct {
	Tool  string
	Paths []PinnedPath
}

var ctxKeyAutoPin = &toolCtxKey{"autoApprovePin"}

// WithAutoApproved pins an Auto decision on ctx. The loop calls it only for
// a call Auto ran without a prompt.
func WithAutoApproved(ctx context.Context, pin AutoPin) context.Context {
	return context.WithValue(ctx, ctxKeyAutoPin, pin)
}

// AutoPinFrom returns the pinned Auto decision, if any.
func AutoPinFrom(ctx context.Context) (AutoPin, bool) {
	if ctx == nil {
		return AutoPin{}, false
	}
	pin, ok := ctx.Value(ctxKeyAutoPin).(AutoPin)
	return pin, ok
}

// AutoPinForVerdict builds the pin for a running verdict.
func AutoPinForVerdict(toolName string, verdict AutoVerdict) AutoPin {
	return AutoPin{Tool: toolName, Paths: append([]PinnedPath(nil), verdict.Paths...)}
}

// ErrAutoPinMoved is returned by RecheckAutoPin when an auto-approved call's
// final path no longer satisfies the rule it was approved under.
var ErrAutoPinMoved = errors.New("auto-approved path no longer inside the workspace or a mount")

// RecheckAutoPin re-applies the J2 rule to a tool's final resolved path. It
// is a no-op (nil) when ctx carries no pin for toolName — a human-approved
// or allow-policy call is never affected. With a pin, the path must still be
// outside the secret set and inside the work folder or a mount, and access
// must be covered by the access the classifier pinned; otherwise the call
// is refused with an error wrapping ErrOutsideScope and ErrAutoPinMoved.
func RecheckAutoPin(ctx context.Context, toolName string, policy fspolicy.FSPolicy, realPath string, access uint64) error {
	pin, ok := AutoPinFrom(ctx)
	if !ok || pin.Tool != toolName {
		return nil
	}
	var pinnedAccess uint64
	for _, p := range pin.Paths {
		pinnedAccess |= p.Access
	}
	if access&^pinnedAccess != 0 {
		return fmt.Errorf("%w: %w: %s needs access %b on %q but Auto approved only %b; nothing was read or written",
			ErrOutsideScope, ErrAutoPinMoved, toolName, access, realPath, pinnedAccess)
	}
	if covered, reason := autoPathCovered(policy, realPath); !covered {
		return fmt.Errorf("%w: %w: %s: %s; nothing was read or written",
			ErrOutsideScope, ErrAutoPinMoved, toolName, reason)
	}
	return nil
}

// resolveAutoCheckedPath is ResolvePathAllowingPatterns followed by
// RecheckAutoPin on the handle's resolved path: the one call every RUNS-IF
// file tool makes instead of resolving directly. On a re-check failure the
// handle is closed and the error returned; the caller refuses.
func resolveAutoCheckedPath(
	ctx context.Context,
	policy fspolicy.FSPolicy,
	toolName string,
	op FSOp,
	raw string,
	patterns []*regexp.Regexp,
	access uint64,
) (*PathHandle, error) {
	handle, err := ResolvePathAllowingPatterns(ctx, policy, toolName, "", op, raw, patterns)
	if err != nil {
		return nil, err
	}
	if _, pinned := AutoPinFrom(ctx); !pinned {
		return handle, nil
	}
	realPath, err := handle.RealPath()
	if err == nil {
		err = RecheckAutoPin(ctx, toolName, policy, realPath, access)
	}
	if err != nil {
		if closeErr := handle.Close(); closeErr != nil {
			slog.Warn("auto-approve: closing refused path handle failed", "tool", toolName, "error", closeErr)
		}
		return nil, err
	}
	return handle, nil
}

// Every AutoRunsIfArgs file tool in this package implements the classifier.
var (
	_ AutoApproveClassifier = (*ReadFileTool)(nil)
	_ AutoApproveClassifier = (*ListDirTool)(nil)
	_ AutoApproveClassifier = (*WriteFileTool)(nil)
	_ AutoApproveClassifier = (*EditFileTool)(nil)
	_ AutoApproveClassifier = (*AppendFileTool)(nil)
	_ AutoApproveClassifier = (*SendFileTool)(nil)
)
