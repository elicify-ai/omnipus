package tools

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ADR-036 / docs/internal/specs/agent-delegation-spec.md — `delegate` is the
// single, unified delegation tool. It replaces the formerly-separate `spawn`
// (async/background), `run_subagent` (sync/await), and `check_spawn_status`
// tools with one tool, one schema, and one piece of task-status state.
//
// FR-D2 (the bug this merge exists to fix): before this merge, `spawn` called
// SubTurnSpawner.SpawnSubTurn directly in a goroutine, entirely bypassing the
// legacy SubagentManager.tasks map that `check_spawn_status` read from —
// checking on a spawn-created task always reported "no subagents have been
// spawned yet." DelegateTool's own `tasks` map is now the SINGLE state store
// both the async path writes to and `action: "status"` reads from — no
// second, disconnected data structure exists.

// SubTurnSpawner is an interface for spawning sub-turns.
// This avoids circular dependency between tools and agent packages.
type SubTurnSpawner interface {
	SpawnSubTurn(ctx context.Context, cfg SubTurnConfig) (*ToolResult, error)
}

// SubTurnConfig holds configuration for spawning a sub-turn. This is the
// shared underlying primitive DelegateTool's async and sync paths both call
// (Async is the only differentiator) — unchanged in shape from the
// pre-merge spawn/run_subagent split, since pkg/agent/subturn.go converts
// this field-by-field into its own agent.SubTurnConfig.
type SubTurnConfig struct {
	Model              string
	Tools              []Tool
	SystemPrompt       string
	MaxTokens          int
	Temperature        float64
	Async              bool          // true for background delegation, false for await (blocking) delegation
	Critical           bool          // continue running after parent finishes gracefully
	Timeout            time.Duration // 0 = use default (5 minutes)
	MaxContextRunes    int           // 0 = auto, -1 = no limit, >0 = explicit limit
	ActualSystemPrompt string
	// TargetAgentID, when non-empty, is the configured agent the sub-turn is
	// delegating TO (e.g., a worker). When set, subturn.go resolves the
	// delegate's soul (AgentConfig.Soul or, for seeded base agents, the
	// compiled coreagent.GetPrompt) and uses it as the ActualSystemPrompt so
	// the child turn runs with system=soul + user=task, uniformly across the
	// native and external-cli executors. Empty means "delegate the parent's
	// own agent" — the parent's own soul applies.
	TargetAgentID      string
	InitialMessages    []providers.Message
	InitialTokenBudget *atomic.Int64 // Shared token budget for team members; nil if no budget
	// TaskLabel is the optional human-readable label for the sub-turn task (FR-H-004).
	// Populated from delegate's "label" argument. Used in the subagent_start WS frame.
	TaskLabel string
	// ResolvedMaxDepth, when non-nil, is the effective onward-delegation depth
	// cap the delegation-policy gate already authorized this specific call
	// against (the tighter of a matched delegation-graph edge's own Depth and
	// the global SubTurn.MaxDepth ceiling). When set, the spawn-time depth
	// check uses this value instead of independently re-deriving a possibly
	// different default, so an explicit per-edge Depth is never silently
	// overridden (#477). nil means "no override — use the spawner's own
	// default depth resolution."
	ResolvedMaxDepth *int
}

// DelegateTaskState is the single source of truth for a background
// (async=true) delegated task's status — written by DelegateTool's own async
// path and read by action:"status" (FR-D2). It replaces the legacy,
// disconnected SubagentTask/SubagentManager.tasks pair.
type DelegateTaskState struct {
	ID            string
	Task          string
	Label         string
	AgentID       string
	OriginChannel string
	OriginChatID  string
	Status        string // running | completed | failed | canceled
	Result        string
	Created       int64

	// SessionID is the transcript session ID a spawned child sub-turn writes
	// its own intermediate narration and final-turn text into — the SAME
	// session ID the delegating PARENT turn itself uses (delegated children
	// share their parent's transcript; see pkg/agent/subturn.go's
	// TranscriptSessionID: parentTS.transcriptSessionID). Captured at
	// task-creation time from ToolTranscriptSessionID(ctx) — the identical
	// value the child will be spawned with. Used by action:"status" (W2) to
	// read back a running native task's recent activity. Empty when no
	// transcript session context was available at creation time (e.g. a
	// direct programmatic Execute call, as in most of this file's tests).
	SessionID string
	// SpawnCallID is this delegate tool call's own ID — the value a spawned
	// child sub-turn's transcript entries carry back as
	// session.TranscriptEntry.ParentSpawnCallID (see that field's doc
	// comment and pkg/agent/subturn.go's parentSpawnCallID). Captured at
	// task-creation time from ToolCallID(ctx). Used to filter SessionID's
	// transcript down to just this task's own activity.
	SpawnCallID string
	// Is3P is true when this task's target agent dispatches via an external
	// CLI runner (subagent_3p: claude-code/codex/opencode — see
	// runner.DispatchKindExternalCLI) rather than natively inside the
	// Omnipus agent loop. Resolved ONCE at task-creation time via
	// DelegateAgentRegistry.IsExternalCLI, so a registry/config change
	// mid-flight cannot flip a task's own snapshot eligibility
	// inconsistently. By design (operator-confirmed scope for W2),
	// external-CLI dispatch is treated as batch/report-on-completion for
	// action:"status" purposes even though runExternalCLISubTurn's own
	// narration DOES land in the same ParentSpawnCallID-tagged transcript
	// entries a native task's does (see recordExternalToolCall /
	// pkg/agent/external_dispatch.go's appendIntermediateAssistantTranscript
	// calls) — a running Is3P task's action:"status" never attempts a live
	// transcript snapshot regardless, and instead renders a fixed
	// no-live-progress note.
	Is3P bool
}

// DelegateAgentRegistry is a minimal interface for resolving whether a
// delegation target dispatches natively (inside the Omnipus agent loop) or
// via an external CLI runner (subagent_3p: claude-code/codex/opencode).
// DelegateTool consults this at task-creation time (W2) to decide whether a
// background task is eligible for a live in-flight transcript snapshot
// under action:"status" — see DelegateTaskState.Is3P's doc comment.
//
// Satisfied by *agent.AgentRegistry; defined as an interface here (mirroring
// AgentRegistryReader in handoff.go) to avoid an import cycle
// (tools -> agent -> tools).
type DelegateAgentRegistry interface {
	// IsExternalCLI reports whether agentID resolves to dispatch kind
	// "external-cli" (subagent_3p). Returns false (native) for an unknown or
	// empty agentID.
	IsExternalCLI(agentID string) bool
}

// DelegateSessionStore is the subset of *session.UnifiedStore DelegateTool
// needs to read back a running native task's own transcript entries for
// action:"status" (W2). Defined as an interface (mirroring
// HandoffSessionStore in handoff.go) to decouple from the concrete store
// type.
type DelegateSessionStore interface {
	// ReadTranscript returns all transcript entries for the session.
	ReadTranscript(sessionID string) ([]session.TranscriptEntry, error)
}

// DelegateTool is the unified delegation tool (FR-D1). Any agent — including
// the main/orchestrating agent — uses this exact tool; access is governed
// solely by the delegation-policy gate (trust set, modes, depth), never by
// tool-registration role restriction (FR-D4).
type DelegateTool struct {
	BaseTool

	spawner      SubTurnSpawner
	defaultModel string
	maxTokens    int
	temperature  float64

	// getAgentRegistry, when set, resolves the live agent registry used to
	// classify a delegation target as native or external-CLI at
	// task-creation time (W2). Called at task-creation time (not
	// construction time) so hot reloads are reflected automatically,
	// mirroring NewHandoffTool's getRegistry closure pattern. A nil/unset
	// resolver leaves every task's Is3P at its zero value (false — treated
	// as native), matching the pre-W2 behavior for anyone who doesn't wire
	// it (e.g. this file's existing unit tests).
	getAgentRegistry func() DelegateAgentRegistry
	// sessionStore, when set, is read from by action:"status" (W2) to build
	// a running native task's recent-activity snapshot. A nil/unset store
	// degrades gracefully — status falls back to the prompt-only summary it
	// already returned before this feature.
	sessionStore DelegateSessionStore

	mu     sync.Mutex
	tasks  map[string]*DelegateTaskState
	nextID int

	// delegationDenyBackground applies the full delegation-policy gate
	// (FR-6.2: trust set + mode("background") + depth) for async=true calls.
	// This is the ONLY gate for the background mode (ADR-037 retired the
	// legacy trust-only allowlistCheck fallback — it was only ever consulted
	// when this was nil, which never happens in production wiring).
	delegationDenyBackground func(ctx context.Context, targetAgentID string) *DelegationDenial
	// delegationDenyAwait applies the full delegation-policy gate (FR-6.2:
	// trust set + mode("await") + depth) for async=false calls. This is the
	// ONLY gate for the await mode (ADR-037 retired the legacy trust-only
	// delegateChecker fallback — same reasoning as delegationDenyBackground).
	delegationDenyAwait func(ctx context.Context, targetAgentID string) *DelegationDenial

	// delegationDepthResolver, when non-nil, resolves the effective onward-
	// delegation depth cap for a specific target — the SAME cap the deny
	// checker above already authorized this call against. Returns nil for "no
	// override" (fall back to the spawner's own default depth resolution) or
	// a pointer to the resolved cap. Threaded into SubTurnConfig.ResolvedMaxDepth
	// so the spawn-time depth check never independently re-derives a different
	// number than the one this gate already authorized (#477). Field name and
	// setter name are pinned — do not rename (relied on by pkg/agent/loop.go).
	delegationDepthResolver func(ctx context.Context, targetAgentID string) *int
}

// Compile-time check: DelegateTool implements AsyncExecutor.
var _ AsyncExecutor = (*DelegateTool)(nil)

// NewDelegateTool constructs a DelegateTool. defaultModel/maxTokens/temperature
// mirror the values the retired SubagentManager used to carry for its callers
// (agent.Model / agent.MaxTokens / agent.Temperature at the call site).
func NewDelegateTool(defaultModel string, maxTokens int, temperature float64) *DelegateTool {
	return &DelegateTool{
		defaultModel: defaultModel,
		maxTokens:    maxTokens,
		temperature:  temperature,
		tasks:        make(map[string]*DelegateTaskState),
		nextID:       1,
	}
}

// SetSpawner sets the SubTurnSpawner used for both async and sync delegation.
func (t *DelegateTool) SetSpawner(spawner SubTurnSpawner) {
	t.spawner = spawner
}

// SetAgentRegistry installs the live agent-registry lookup (W2) DelegateTool
// uses at task-creation time to classify a delegation target as native or
// external-CLI (DelegateTaskState.Is3P). getRegistry is called at
// task-creation time, not construction time, so hot reloads are reflected
// automatically — see the getAgentRegistry field doc.
func (t *DelegateTool) SetAgentRegistry(getRegistry func() DelegateAgentRegistry) {
	t.getAgentRegistry = getRegistry
}

// SetSessionStore installs the transcript store DelegateTool reads from to
// build a running native task's recent-activity snapshot under
// action:"status" (W2). See the sessionStore field doc.
func (t *DelegateTool) SetSessionStore(store DelegateSessionStore) {
	t.sessionStore = store
}

// SetDelegationDenyCheckerBackground installs the full delegation-policy gate
// (FR-6.2: trust set + mode("background") + depth) applied when async=true.
// Mirrors the pre-merge SpawnTool.SetDelegationDenyChecker exactly.
func (t *DelegateTool) SetDelegationDenyCheckerBackground(
	check func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDenyBackground = check
}

// SetDelegationDenyCheckerAwait installs the full delegation-policy gate
// (FR-6.2: trust set + mode("await") + depth) applied when async=false.
// Mirrors the pre-merge SubagentTool.SetDelegationDenyChecker exactly.
func (t *DelegateTool) SetDelegationDenyCheckerAwait(
	check func(ctx context.Context, targetAgentID string) *DelegationDenial,
) {
	t.delegationDenyAwait = check
}

// SetDelegationDepthResolver installs the effective-depth-cap resolver (#477).
// See the delegationDepthResolver field doc. Name pinned — relied on by
// pkg/agent/loop.go's registration wiring.
func (t *DelegateTool) SetDelegationDepthResolver(resolve func(ctx context.Context, targetAgentID string) *int) {
	t.delegationDepthResolver = resolve
}

func (t *DelegateTool) Name() string {
	return "delegate"
}

func (t *DelegateTool) Description() string {
	return "Delegate a task to a subagent. By default this runs in the background " +
		"(async=true) and returns immediately with a task_id — poll progress with " +
		"action=\"status\" and that task_id. Set async=false to block this turn and " +
		"receive the delegated result inline instead. Optionally provide agent_id to " +
		"target a specific agent from your delegation allowlist; omit it to run a " +
		"generic subagent under your own agent. Status results are scoped to the " +
		"current conversation's channel and chat ID; all tasks are listed only when " +
		"no channel/chat context is injected (e.g. direct programmatic calls)."
}

func (t *DelegateTool) Scope() ToolScope       { return ScopeCore }
func (t *DelegateTool) Category() ToolCategory { return CategoryDelegation }

func (t *DelegateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task for the subagent to complete. Required when action is \"run\" (the default).",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
			"agent_id": map[string]any{
				"type": "string",
				"description": "Optional: the id of a specific agent to delegate to (must be in your " +
					"delegation allowlist). Omit to run a generic subagent under your own agent.",
			},
			"async": map[string]any{
				"type": "boolean",
				"description": "Whether to run in the background (true, the default) and return immediately " +
					"with a task_id, or block until the delegated turn completes (false) and return its " +
					"result inline.",
			},
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"run", "status"},
				"description": "\"run\" (the default) delegates a new task. \"status\" checks on a previously-delegated task.",
			},
			"task_id": map[string]any{
				"type": "string",
				"description": "The task_id to check (e.g. \"delegate-1\"), used with action=\"status\". " +
					"When omitted under action=\"status\", all visible tasks are listed instead.",
			},
		},
		// Nothing is unconditionally required at the schema level — requiredness
		// is action-dependent (task for action:"run", nothing hard-required for
		// action:"status", which falls back to listing all visible tasks) and is
		// enforced at runtime, mirroring ExecTool's action-dispatch pattern.
		"required": []string{},
	}
}

func (t *DelegateTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, nil)
}

// ExecuteAsync implements AsyncExecutor. The callback is passed through as a
// call parameter — never stored on the DelegateTool instance.
func (t *DelegateTool) ExecuteAsync(
	ctx context.Context,
	args map[string]any,
	cb AsyncCallback,
) *ToolResult {
	return t.execute(ctx, args, cb)
}

func (t *DelegateTool) execute(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	action, _ := args["action"].(string)
	if rawAction, present := args["action"]; present && rawAction != nil {
		if _, ok := rawAction.(string); !ok {
			return ErrorResult("action must be a string")
		}
	}
	if action == "" {
		action = "run"
	}

	switch action {
	case "run":
		return t.executeRun(ctx, args, cb)
	case "status":
		return t.executeStatus(ctx, args)
	default:
		return ErrorResult(fmt.Sprintf("invalid action %q: must be \"run\" or \"status\"", action))
	}
}

func (t *DelegateTool) executeRun(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	task, ok := args["task"].(string)
	if !ok || strings.TrimSpace(task) == "" {
		return ErrorResult("task is required and must be a non-empty string")
	}

	label, _ := args["label"].(string)
	agentID, _ := args["agent_id"].(string)

	async := true
	if rawAsync, present := args["async"]; present && rawAsync != nil {
		b, ok := rawAsync.(bool)
		if !ok {
			return ErrorResult("async must be a boolean")
		}
		async = b
	}

	// Delegation policy gate (FR-6.2): trust set + mode + depth, mode selected
	// by the async flag ("background" vs "await") — applied identically
	// regardless of async value (FR-D3). ADR-037: this is now the ONLY gate —
	// the legacy trust-only allowlistCheck/delegateChecker fallbacks (consulted
	// only when these were nil, which never happened in production) are
	// retired.
	//
	// FAIL CLOSED, not open, when no checker is wired: an unwired deny-checker
	// is a configuration error, never a permission grant. This is unreachable
	// in today's production wiring — pkg/agent/loop.go's registerSharedTools
	// unconditionally calls SetDelegationDenyCheckerBackground/Await for every
	// agent — but removing the legacy fallback (which was itself deny-by-
	// default: config.IsDelegationAllowed/CanSpawnSubagent both returned false
	// on an unset policy) must not also remove the safety net for the NEXT
	// wiring bug: a new agent-construction path, a v0.3 plugin-system entry
	// point, or a refactor slip that forgets to call the setter. Do NOT
	// "simplify" this back to fail-open — CLAUDE.md Hard Constraint #6 exists
	// precisely to forbid a silent runtime default here.
	if async {
		if t.delegationDenyBackground != nil {
			if denial := t.delegationDenyBackground(ctx, agentID); denial != nil {
				return DelegationDeniedResult("delegate", denial)
			}
		} else {
			slog.Error("delegate: no background delegation-deny checker installed — denying by default",
				"agent_id", agentID)
			return DelegationDeniedResult("delegate", &DelegationDenial{
				Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
				Policy:        DenyTrustSet,
				TargetAgentID: agentID,
			})
		}
	} else {
		if t.delegationDenyAwait != nil {
			if denial := t.delegationDenyAwait(ctx, agentID); denial != nil {
				return DelegationDeniedResult("delegate", denial)
			}
		} else {
			slog.Error("delegate: no await delegation-deny checker installed — denying by default",
				"agent_id", agentID)
			return DelegationDeniedResult("delegate", &DelegationDenial{
				Reason:        "delegation is not configured for this agent (no policy gate installed) — denying by default",
				Policy:        DenyTrustSet,
				TargetAgentID: agentID,
			})
		}
	}

	// #477: resolve the effective depth cap the gate above just authorized
	// this call against, so the spawner's own depth check does not
	// independently re-derive a different (possibly stricter) default.
	var resolvedMaxDepth *int
	if t.delegationDepthResolver != nil {
		resolvedMaxDepth = t.delegationDepthResolver(ctx, agentID)
	}

	if async {
		return t.executeAsync(ctx, task, label, agentID, resolvedMaxDepth, cb)
	}
	return t.executeSync(ctx, task, label, agentID, resolvedMaxDepth)
}

// executeAsync runs the background (async=true) delegation path. It records
// the task's state in t.tasks BEFORE launching the sub-turn goroutine and
// updates that SAME record on completion — the fix for FR-D2: action:"status"
// reads from this exact map, so a real, live status is always available.
func (t *DelegateTool) executeAsync(
	ctx context.Context,
	task, label, agentID string,
	resolvedMaxDepth *int,
	cb AsyncCallback,
) *ToolResult {
	if t.spawner == nil {
		return ErrorResult("delegate: no sub-turn spawner configured")
	}

	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)
	// W2: capture, at task-creation time, the exact correlation anchors a
	// spawned child sub-turn's transcript entries will carry back —
	// SessionID mirrors TranscriptSessionID: parentTS.transcriptSessionID
	// (pkg/agent/subturn.go), and SpawnCallID mirrors the delegate tool
	// call's own ID (the value spawnToolCallIDFromContext captures on the
	// agent-package side to set childTS.parentSpawnCallID). Reading both
	// from THIS SAME ctx guarantees they match what the child will actually
	// use, without any agent-package coupling.
	sessionID := ToolTranscriptSessionID(ctx)
	spawnCallID := ToolCallID(ctx)
	is3P := false
	if agentID != "" && t.getAgentRegistry != nil {
		if reg := t.getAgentRegistry(); reg != nil {
			is3P = reg.IsExternalCLI(agentID)
		}
	}

	t.mu.Lock()
	taskID := fmt.Sprintf("delegate-%d", t.nextID)
	t.nextID++
	t.tasks[taskID] = &DelegateTaskState{
		ID:            taskID,
		Task:          task,
		Label:         label,
		AgentID:       agentID,
		OriginChannel: channel,
		OriginChatID:  chatID,
		Status:        "running",
		Created:       time.Now().UnixMilli(),
		SessionID:     sessionID,
		SpawnCallID:   spawnCallID,
		Is3P:          is3P,
	}
	t.mu.Unlock()

	// The task is the first USER message; the delegate's soul (worker /
	// configured agent) is resolved inside spawnSubTurn and used as the
	// system role. delegate does not pre-inject any persona — a configured
	// delegate exposes its own soul and a soul-less worker runs with an empty
	// system role (worker souls are OPTIONAL by design). The label, when set,
	// is preserved as the task label for the WS subTurn_start frame.
	//
	// Critical: true is REQUIRED here, not optional. Background delegation's
	// entire premise is "the parent moves on; tell me later" — the parent
	// turn routinely finishes (its own follow-up LLM call after receiving
	// this async ack, then Finish(false)) in well under the time it takes
	// the delegate to run even one tool call. Without Critical:true, the
	// child sub-turn's own loop (pkg/agent/loop.go's "Parent turn ended"
	// check, evaluated early in each iteration, before the next LLM call)
	// treats !ts.critical && ts.IsParentEnded() as a signal to exit
	// gracefully — silently discarding the delegate's real answer for any
	// task needing more than a single LLM turn (i.e.
	// any task that calls a tool before its final answer). The delegate's
	// pre-tool-call narration survives (persisted per-iteration), but the
	// synthesized final answer is never produced at all: spawnSubTurn's
	// result comes back with ForLLM/ForUser == "", and asyncCallback's
	// `content == "" { return }` guard (pkg/agent/loop.go) then silently
	// drops it — no error, no notification, nothing delivered to the user,
	// live or on reload. Critical:true lets the child keep running past the
	// parent's own finish (it still delivers as an "orphan" on the
	// now-moot pendingResults channel — see deliverSubTurnResult — but its
	// REAL delivery path, this same cb -> AsyncNotifier.Notify chain, is
	// unaffected by parent lifecycle and fires correctly once the child
	// actually finishes). See SubTurnConfig.Critical's doc comment.
	go func() {
		result, err := t.spawner.SpawnSubTurn(ctx, SubTurnConfig{
			Model:            t.defaultModel,
			Tools:            nil, // Will inherit from parent via context
			SystemPrompt:     task,
			TargetAgentID:    agentID,
			MaxTokens:        t.maxTokens,
			Temperature:      t.temperature,
			Async:            true,
			Critical:         true,
			TaskLabel:        label,
			ResolvedMaxDepth: resolvedMaxDepth,
		})

		t.mu.Lock()
		if state, ok := t.tasks[taskID]; ok {
			switch {
			case err != nil && ctx.Err() != nil:
				state.Status = "canceled"
				state.Result = "Task canceled during execution"
			case err != nil:
				state.Status = "failed"
				state.Result = fmt.Sprintf("Error: %v", err)
			default:
				state.Status = "completed"
				if result != nil {
					state.Result = result.ForLLM
				}
			}
		}
		t.mu.Unlock()

		switch {
		case err != nil:
			result = ErrorResult(fmt.Sprintf("Delegate failed: %v", err)).WithError(err)
		case result != nil:
			// Finding B (A-I4 round 4, live-verified): mirror executeSync's
			// own wrapping below — the raw spawner result's ForUser field
			// (spawnSubTurn / pkg/agent/subturn.go sets it to
			// turnRes.finalContent, the CHILD's own unwrapped, first-person
			// final text) must never reach pkg/agent/loop.go's asyncCallback
			// unmodified. asyncCallback unconditionally does
			// `if !result.Silent && result.ForUser != "" { PublishOutbound(...
			// Content: result.ForUser ...) }` — a DIRECT, immediate publish
			// with no relation to the wsStreamer/shadow-stream machinery at
			// all (confirmed via a live background delegation: the leaked
			// bubble appeared even with no cancellation involved, the moment
			// the child's own final answer happened to require no wrapping —
			// e.g. a policy-denied task explaining itself in its own voice).
			// That silently turns the delegate's own raw narration into a
			// second, unattributed top-level chat bubble the instant the
			// parent's own turn has already ended — the common case for
			// background delegation (Critical:true's own doc comment above:
			// "the parent turn routinely finishes ... in well under the time
			// it takes the delegate to run even one tool call"). This is
			// exactly the content class the design intends to keep hidden,
			// matching the already-correct sync/await case (executeSync
			// below never independently publishes anything — its result only
			// ever becomes a normal tool_call_result) and
			// pkg/gateway/replay.go's ParentSpawnCallID skip. The LLM-facing
			// AsyncNotifier continuation turn (still fed the wrapped ForLLM
			// content below) already informs the user, in the DELEGATOR's
			// own voice, that the delegation finished and what it found —
			// clearing ForUser here removes the duplicate, unattributed raw
			// dump without losing any user-facing information.
			labelStr := label
			if labelStr == "" {
				labelStr = "(unnamed)"
			}
			result = &ToolResult{
				ForLLM:  fmt.Sprintf("Subagent task completed:\nLabel: %s\nResult: %s", labelStr, result.ForLLM),
				IsError: result.IsError,
				Async:   true,
			}
		}

		// Call callback if provided
		if cb != nil {
			cb(ctx, result)
		} else if err != nil {
			slog.Error("delegate: subturn failed with no callback", "error", err)
		}
	}()

	msg := fmt.Sprintf("Delegated task for: %s (task_id: %s)", task, taskID)
	if label != "" {
		msg = fmt.Sprintf("Delegated task '%s' for: %s (task_id: %s)", label, task, taskID)
	}
	msg += fmt.Sprintf(" — running in background; check progress with delegate(action=\"status\", task_id=%q).", taskID)
	return AsyncResult(msg)
}

// executeSync runs the await (async=false) delegation path: it blocks until
// the delegated turn completes and returns the result inline.
func (t *DelegateTool) executeSync(
	ctx context.Context,
	task, label, agentID string,
	resolvedMaxDepth *int,
) *ToolResult {
	if t.spawner == nil {
		return ErrorResult("delegate: no sub-turn spawner configured").WithError(fmt.Errorf("spawner not set"))
	}

	result, err := t.spawner.SpawnSubTurn(ctx, SubTurnConfig{
		Model:            t.defaultModel,
		Tools:            nil, // Will inherit from parent via context
		SystemPrompt:     task,
		TargetAgentID:    agentID, // "" → parent's own soul; non-empty → named agent's soul
		TaskLabel:        label,
		MaxTokens:        t.maxTokens,
		Temperature:      t.temperature,
		Async:            false,
		ResolvedMaxDepth: resolvedMaxDepth,
	})
	// Finding F (A-I4 round 5): only take the generic "Delegate execution
	// failed" shortcut for a genuine dispatch failure — result == nil (e.g.
	// a panic spawnSubTurn's own recover() deliberately nils result for) or
	// a real, non-interrupted error. A parent-cancellation interruption
	// (result.Interrupted, set by spawnSubTurn's cleanup defer using the
	// SAME classification the live subagent_end frame already reports —
	// see ToolResult.Interrupted's doc comment) still returns a non-nil err
	// here (the child's context WAS canceled), but must fall through to the
	// normal formatting below so result.Interrupted survives onto the
	// result this function returns — which pkg/agent/loop.go's tool-call-
	// transcript persistence reads to decide whether a session reload shows
	// "interrupted" (matching live) or "failed" (the bug this closes).
	if result == nil || (err != nil && !result.Interrupted) {
		return ErrorResult(fmt.Sprintf("Delegate execution failed: %v", err)).WithError(err)
	}

	// Format result for display
	userContent := result.ForLLM
	if result.ForUser != "" {
		userContent = result.ForUser
	}
	maxUserLen := 500
	if len(userContent) > maxUserLen {
		userContent = userContent[:maxUserLen] + "..."
	}

	labelStr := label
	if labelStr == "" {
		labelStr = "(unnamed)"
	}
	llmContent := fmt.Sprintf("Subagent task completed:\nLabel: %s\nResult: %s",
		labelStr, result.ForLLM)

	return &ToolResult{
		ForLLM:      llmContent,
		ForUser:     userContent,
		Silent:      false,
		IsError:     result.IsError,
		Interrupted: result.Interrupted,
		Async:       false,
	}
}

// executeStatus implements action:"status". It resolves against the exact
// same t.tasks map the async path writes to (FR-D2) and preserves
// check_spawn_status's channel/chatID scoping exactly: a lookup or listing is
// restricted to tasks that originated from the SAME conversation, and all
// tasks are listed only when no channel/chat context is injected at all
// (e.g. direct programmatic Execute calls).
func (t *DelegateTool) executeStatus(ctx context.Context, args map[string]any) *ToolResult {
	callerChannel := ToolChannel(ctx)
	callerChatID := ToolChatID(ctx)

	var taskID string
	if rawTaskID, ok := args["task_id"]; ok && rawTaskID != nil {
		taskIDStr, ok := rawTaskID.(string)
		if !ok {
			return ErrorResult("task_id must be a string")
		}
		taskID = strings.TrimSpace(taskIDStr)
	}

	if taskID != "" {
		taskCopy, ok := t.getTaskCopy(taskID)
		if !ok {
			return ErrorResult(fmt.Sprintf("No subagent found with task ID: %s", taskID))
		}

		// Restrict lookup to tasks that belong to this conversation.
		if callerChannel != "" && taskCopy.OriginChannel != "" && taskCopy.OriginChannel != callerChannel {
			return ErrorResult(fmt.Sprintf("No subagent found with task ID: %s", taskID))
		}
		if callerChatID != "" && taskCopy.OriginChatID != "" && taskCopy.OriginChatID != callerChatID {
			return ErrorResult(fmt.Sprintf("No subagent found with task ID: %s", taskID))
		}

		return NewToolResult(delegateFormatTask(&taskCopy, t.delegateStatusExtra(&taskCopy)))
	}

	origTasks := t.listTaskCopies()
	if len(origTasks) == 0 {
		return NewToolResult("No subagents have been spawned yet.")
	}

	taskList := make([]*DelegateTaskState, 0, len(origTasks))
	for i := range origTasks {
		cpy := &origTasks[i]

		// Filter to tasks that originate from the current conversation only.
		if callerChannel != "" && cpy.OriginChannel != "" && cpy.OriginChannel != callerChannel {
			continue
		}
		if callerChatID != "" && cpy.OriginChatID != "" && cpy.OriginChatID != callerChatID {
			continue
		}

		taskList = append(taskList, cpy)
	}

	if len(taskList) == 0 {
		return NewToolResult("No subagents found for this conversation.")
	}

	// Order by creation time (ascending) so spawning order is preserved.
	// Fall back to ID string for tasks created in the same millisecond.
	sort.Slice(taskList, func(i, j int) bool {
		if taskList[i].Created != taskList[j].Created {
			return taskList[i].Created < taskList[j].Created
		}
		return taskList[i].ID < taskList[j].ID
	})

	counts := map[string]int{}
	for _, task := range taskList {
		counts[task.Status]++
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Subagent status report (%d total):\n", len(taskList)))
	for _, status := range []string{"running", "completed", "failed", "canceled"} {
		if n := counts[status]; n > 0 {
			label := strings.ToUpper(status[:1]) + status[1:] + ":"
			sb.WriteString(fmt.Sprintf("  %-10s %d\n", label, n))
		}
	}
	sb.WriteString("\n")

	for _, task := range taskList {
		sb.WriteString(delegateFormatTask(task, t.delegateStatusExtra(task)))
		sb.WriteString("\n\n")
	}

	return NewToolResult(strings.TrimRight(sb.String(), "\n"))
}

// getTaskCopy returns a copy of the task with the given ID, taken under the
// lock, so the caller receives a consistent snapshot with no data race.
func (t *DelegateTool) getTaskCopy(taskID string) (DelegateTaskState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	task, ok := t.tasks[taskID]
	if !ok {
		return DelegateTaskState{}, false
	}
	return *task, true
}

// listTaskCopies returns value copies of all tasks, taken under the lock, so
// callers receive consistent snapshots with no data race.
func (t *DelegateTool) listTaskCopies() []DelegateTaskState {
	t.mu.Lock()
	defer t.mu.Unlock()

	copies := make([]DelegateTaskState, 0, len(t.tasks))
	for _, task := range t.tasks {
		copies = append(copies, *task)
	}
	return copies
}

// delegateFormatTask renders a single DelegateTaskState as a human-readable
// block. extra, when non-empty, is appended as a trailing "\n"+extra section
// — used by action:"status" (W2) to attach either a running native task's
// recent transcript activity or a running external-CLI task's
// no-live-progress note (see delegateStatusExtra). Pass "" for a task with
// nothing to add (a non-running task, or a running native task with no
// activity captured yet) — this keeps the function's output identical to
// its pre-W2 shape for those cases.
func delegateFormatTask(task *DelegateTaskState, extra string) string {
	var sb strings.Builder

	header := fmt.Sprintf("[%s] status=%s", task.ID, task.Status)
	if task.Label != "" {
		header += fmt.Sprintf("  label=%q", task.Label)
	}
	if task.AgentID != "" {
		header += fmt.Sprintf("  agent=%s", task.AgentID)
	}
	if task.Created > 0 {
		created := time.UnixMilli(task.Created).UTC().Format("2006-01-02 15:04:05 UTC")
		header += fmt.Sprintf("  created=%s", created)
	}
	sb.WriteString(header)

	if task.Task != "" {
		sb.WriteString(fmt.Sprintf("\n  task:   %s", task.Task))
	}
	if task.Result != "" {
		result := task.Result
		const maxResultLen = 300
		runes := []rune(result)
		if len(runes) > maxResultLen {
			result = string(runes[:maxResultLen]) + "…"
		}
		sb.WriteString(fmt.Sprintf("\n  result: %s", result))
	}
	if extra != "" {
		sb.WriteString("\n")
		sb.WriteString(extra)
	}

	return sb.String()
}

// maxStatusActivityLines caps how many of a running native task's most
// recent transcript entries action:"status" surfaces (W2). Fixed at ~5 per
// spec — enough to convey what the delegate is currently doing without
// flooding the calling LLM's context on every poll.
const maxStatusActivityLines = 5

// maxStatusActivityLineRunes caps each surfaced activity line's length (W2).
const maxStatusActivityLineRunes = 120

// delegate3PStatusNote is the fixed action:"status" annotation for a running
// external-CLI (subagent_3p) task — see DelegateTaskState.Is3P's doc comment
// for why no live snapshot is attempted for these.
const delegate3PStatusNote = "  note:   external agent — no live progress; results on completion"

// delegateStatusExtra computes the action:"status" trailing annotation for
// task (W2). Only a "running" task gets anything:
//   - a native task gets up to maxStatusActivityLines of its own recent
//     transcript activity (recentActivityLines), or "" if the child sub-turn
//     hasn't written anything yet;
//   - an external-CLI (Is3P) task gets the fixed delegate3PStatusNote instead
//     of any attempted snapshot (batch/report-on-completion by design).
//
// Every non-running task (completed/failed/canceled) returns "" — its
// Result field already carries the final answer, so nothing is added.
func (t *DelegateTool) delegateStatusExtra(task *DelegateTaskState) string {
	if task.Status != "running" {
		return ""
	}
	if task.Is3P {
		return delegate3PStatusNote
	}
	lines := t.recentActivityLines(task.SessionID, task.SpawnCallID, maxStatusActivityLines)
	if len(lines) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  recent activity:")
	for _, line := range lines {
		sb.WriteString("\n    - ")
		sb.WriteString(line)
	}
	return sb.String()
}

// recentActivityLines reads back up to max of the most recent transcript
// entries a running NATIVE delegated sub-turn has written into its shared
// parent session, filtered to just this task's own activity via
// session.TranscriptEntry.ParentSpawnCallID == spawnCallID — the delegate
// tool call's own ID, which subturn.go stamps onto every intermediate/final
// assistant-text entry the child sub-turn produces (see
// ParentSpawnCallID's doc comment). This is a pure READ of data the child
// sub-turn already persists as a side effect of running — no new storage or
// write path is introduced by W2.
//
// Returns nil (never an error) when no session store is wired, sessionID or
// spawnCallID is empty, the transcript can't be read, or nothing has been
// written yet — delegateStatusExtra treats all of these as "no snapshot
// available" and falls back to the prompt-only summary that predates this
// feature.
func (t *DelegateTool) recentActivityLines(sessionID, spawnCallID string, maxLines int) []string {
	if t.sessionStore == nil || sessionID == "" || spawnCallID == "" {
		return nil
	}
	entries, err := t.sessionStore.ReadTranscript(sessionID)
	if err != nil {
		slog.Warn("delegate: status snapshot: failed to read transcript",
			"session_id", sessionID, "error", err)
		return nil
	}

	var lines []string
	for _, e := range entries {
		if e.ParentSpawnCallID != spawnCallID {
			continue
		}
		content := strings.TrimSpace(e.Content)
		if content == "" {
			continue
		}
		runes := []rune(content)
		if len(runes) > maxStatusActivityLineRunes {
			content = string(runes[:maxStatusActivityLineRunes]) + "…"
		}
		lines = append(lines, content)
	}
	if len(lines) == 0 {
		return nil
	}
	// Entries are in chronological (append) order — keep only the most
	// recent `maxLines`, preserving chronological order within that window.
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}
