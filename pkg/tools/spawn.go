package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

type SpawnTool struct {
	BaseTool
	spawner        SubTurnSpawner
	defaultModel   string
	maxTokens      int
	temperature    float64
	allowlistCheck func(targetAgentID string) bool
	// delegationDeny, when non-nil, gates the spawn (background-mode) delegation
	// against the full delegation policy (trust set + modes + depth — FR-6.2). It
	// receives the call ctx (for current delegation depth) and the resolved
	// target agent id ("" when no explicit target was given), and returns a
	// non-empty human-readable reason string to DENY, or "" to ALLOW. Takes
	// precedence over allowlistCheck so the LLM sees *why* a delegation was
	// rejected (mode forbidden / depth exceeded / target untrusted). Returns a
	// non-nil *DelegationDenial to DENY (carrying the structured reason + policy
	// axis) or nil to ALLOW.
	delegationDeny func(ctx context.Context, targetAgentID string) *DelegationDenial
}

// Compile-time check: SpawnTool implements AsyncExecutor.
var _ AsyncExecutor = (*SpawnTool)(nil)

func NewSpawnTool(manager *SubagentManager) *SpawnTool {
	if manager == nil {
		return &SpawnTool{}
	}
	return &SpawnTool{
		defaultModel: manager.defaultModel,
		maxTokens:    manager.maxTokens,
		temperature:  manager.temperature,
	}
}

// SetSpawner sets the SubTurnSpawner for direct sub-turn execution.
func (t *SpawnTool) SetSpawner(spawner SubTurnSpawner) {
	t.spawner = spawner
}

func (t *SpawnTool) Name() string {
	return "spawn"
}

func (t *SpawnTool) Description() string {
	return "Spawn a subagent to handle a task in the background. Use this for complex or time-consuming tasks that can run independently. The subagent will complete the task and report back when done."
}

func (t *SpawnTool) Scope() ToolScope       { return ScopeCore }
func (t *SpawnTool) Category() ToolCategory { return CategoryDelegation }

func (t *SpawnTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task for subagent to complete",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Optional target agent ID to delegate the task to",
			},
		},
		"required": []string{"task"},
	}
}

func (t *SpawnTool) SetAllowlistChecker(check func(targetAgentID string) bool) {
	t.allowlistCheck = check
}

// SetDelegationDenyChecker installs the full delegation-policy gate (FR-6.2):
// trust set + modes + depth. The checker returns a non-empty reason to DENY or
// "" to ALLOW. When set, it takes precedence over the allowlist checker so the
// rejected delegation surfaces a clear reason to the LLM instead of a generic
// "not allowed" message.
func (t *SpawnTool) SetDelegationDenyChecker(check func(ctx context.Context, targetAgentID string) *DelegationDenial) {
	t.delegationDeny = check
}

func (t *SpawnTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, nil)
}

// ExecuteAsync implements AsyncExecutor. The callback is passed through to the
// subagent manager as a call parameter — never stored on the SpawnTool instance.
func (t *SpawnTool) ExecuteAsync(
	ctx context.Context,
	args map[string]any,
	cb AsyncCallback,
) *ToolResult {
	return t.execute(ctx, args, cb)
}

func (t *SpawnTool) execute(
	ctx context.Context,
	args map[string]any,
	cb AsyncCallback,
) *ToolResult {
	task, ok := args["task"].(string)
	if !ok || strings.TrimSpace(task) == "" {
		return ErrorResult("task is required and must be a non-empty string")
	}

	label, _ := args["label"].(string)
	agentID, _ := args["agent_id"].(string)

	// Delegation policy gate (FR-6.2): trust set + modes + depth. The
	// full-policy checker takes precedence and is consulted on every spawn
	// (including untargeted ones, where mode/depth still apply) so the reason
	// for any denial is surfaced to the LLM.
	if t.delegationDeny != nil {
		if denial := t.delegationDeny(ctx, agentID); denial != nil {
			return DelegationDeniedResult("spawn", denial)
		}
	} else if agentID != "" && t.allowlistCheck != nil {
		// Backward-compat: legacy trust-only allowlist check.
		if !t.allowlistCheck(agentID) {
			return ErrorResult(fmt.Sprintf("not allowed to spawn agent '%s'", agentID))
		}
	}

	// Use spawner if available (direct SpawnSubTurn call)
	//
	// The task is the first USER message; the delegate's soul (worker / configured
	// agent) is resolved inside spawnSubTurn and used as the system role. The
	// legacy "You are a spawned subagent running in the background" wrapper is
	// REMOVED — the spawn tool does not pre-inject any persona, so a configured
	// delegate exposes its own soul and a soul-less worker runs with an empty
	// system role (worker souls are OPTIONAL by design). The label, when set,
	// is preserved as the task label for the WS subTurn_start frame.
	if t.spawner != nil {
		// Launch async sub-turn in goroutine
		go func() {
			result, err := t.spawner.SpawnSubTurn(ctx, SubTurnConfig{
				Model:         t.defaultModel,
				Tools:         nil, // Will inherit from parent via context
				SystemPrompt:  task,
				TargetAgentID: agentID,
				MaxTokens:     t.maxTokens,
				Temperature:   t.temperature,
				Async:         true,  // Async execution
				TaskLabel:     label, // FR-H-004: propagate to SubTurnSpawnPayload for WS frame
			})
			if err != nil {
				result = ErrorResult(fmt.Sprintf("Spawn failed: %v", err)).WithError(err)
			}

			// Call callback if provided
			if cb != nil {
				cb(ctx, result)
			} else if err != nil {
				slog.Error("spawn: subturn failed with no callback", "error", err)
			}
		}()

		// Return immediate acknowledgment
		if label != "" {
			return AsyncResult(fmt.Sprintf("Spawned subagent '%s' for task: %s", label, task))
		}
		return AsyncResult(fmt.Sprintf("Spawned subagent for task: %s", task))
	}

	// Fallback: spawner not configured
	return ErrorResult("Subagent manager not configured")
}
