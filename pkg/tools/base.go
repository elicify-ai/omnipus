package tools

import "context"

// ToolScope classifies a tool's privilege level for per-agent visibility filtering.
type ToolScope string

const (
	// ScopeCore tools are available to core agents by default, and to custom
	// agents only when per-agent policy explicitly grants them.
	// Examples: exec, browser.*, write_file, edit_file, spawn, subagent,
	// and all system.* tools (which return this scope via Scope()).
	ScopeCore ToolScope = "core"
	// ScopeGeneral tools are available to all agent types by default.
	// Examples: read_file, list_directory, search_web, fetch_url, message, create_task, list_tasks.
	ScopeGeneral ToolScope = "general"
)

// ToolCategory groups tools by function for the UI tool picker and for the
// Category() method on the Tool interface.
// These values align with the existing CatalogEntry categories.
type ToolCategory string

const (
	// CategoryCore is a dumping-ground value (legacy). BaseTool's default is CategoryCore, but no
	// SHIPPED tool returns CategoryCore or CategorySystem after the §7 rename —
	// every tool returns a domain category below. Kept only as the BaseTool
	// fallback and for back-compat; not used by any shipped tool.
	CategoryCore   ToolCategory = "core"   // default for BaseTool
	CategorySystem ToolCategory = "system" // legacy sysagent dumping ground

	// CategoryFilesystem and the other domain categories (§7 tool-rename-map-2026-06) are
	// returned by every shipped builtin tool.
	CategoryFilesystem    ToolCategory = "filesystem"
	CategoryShell         ToolCategory = "shell"
	CategoryWeb           ToolCategory = "web"
	CategoryBrowser       ToolCategory = "browser"
	CategoryCommunication ToolCategory = "communication"
	CategoryDelegation    ToolCategory = "delegation"
	CategoryMemory        ToolCategory = "memory"
	CategoryTasks         ToolCategory = "tasks"
	CategorySkills        ToolCategory = "skills"
	CategoryToolDiscovery ToolCategory = "tool_discovery"
	CategoryAgents        ToolCategory = "agents"
	CategoryWorkspaces    ToolCategory = "workspaces"
	CategoryChannels      ToolCategory = "channels"
	CategoryProviders     ToolCategory = "providers"
	CategoryPlatform      ToolCategory = "platform"
	// CategoryMCP is behavior-bearing: it drives server_id population in
	// rest_tool_registry.go. Reserved for dynamic MCP-server tools and the
	// mcp-management builtins (add/remove/list_mcp_servers).
	CategoryMCP ToolCategory = "mcp"
)

// Tool is the interface that all tools must implement.
//
// Category has a default implementation via BaseTool. Embed BaseTool in your
// tool struct to inherit the default without needing to implement it explicitly:
//
//	type MyTool struct {
//	    tools.BaseTool
//	    ...
//	}
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any
	Execute(ctx context.Context, args map[string]any) *ToolResult
	// Scope returns the privilege level of this tool.
	Scope() ToolScope
	// Category returns the functional category for the tool picker UI.
	// Default (via BaseTool): CategoryCore.
	Category() ToolCategory
}

// BaseTool provides a zero-value default implementation of Category so that
// existing tool structs only need to embed BaseTool to satisfy the full Tool
// interface without mass-modifying every file.
//
// Usage:
//
//	type MyTool struct {
//	    tools.BaseTool
//	    // ... other fields
//	}
//
// MyTool then inherits:
//   - Category() ToolCategory  → CategoryCore
//
// Override the method on the embedding struct as needed.
type BaseTool struct{}

// Category returns CategoryCore as the default. Override for more specific
// categorisation (file, web, browser, etc.).
func (BaseTool) Category() ToolCategory { return CategoryCore }

// --- Request-scoped tool context (channel / chatID) ---
//
// Carried via context.Value so that concurrent tool calls each receive
// their own immutable copy — no mutable state on singleton tool instances.
//
// Keys are unexported pointer-typed vars — guaranteed collision-free,
// and only accessible through the helper functions below.

type toolCtxKey struct{ name string }

var (
	ctxKeyChannel              = &toolCtxKey{"channel"}
	ctxKeyChatID               = &toolCtxKey{"chatID"}
	ctxKeyAgentID              = &toolCtxKey{"agentID"}
	ctxKeySessionKey           = &toolCtxKey{"sessionKey"}
	ctxKeyTranscriptSessionID  = &toolCtxKey{"transcriptSessionID"}
	ctxKeyProcTracker          = &toolCtxKey{"procTracker"}
	ctxKeySessionOwner         = &toolCtxKey{"sessionOwner"}
	ctxKeyWorkspaceID          = &toolCtxKey{"workspaceID"}
	ctxKeyTurnWorkspaceDir     = &toolCtxKey{"turnWorkspaceDir"}
	ctxKeyCitationTracker      = &toolCtxKey{"citationTracker"}
	ctxKeyDelegationDepth      = &toolCtxKey{"delegationDepth"}
	ctxKeyToolCallID           = &toolCtxKey{"toolCallID"}
	ctxKeyRunningTaskID        = &toolCtxKey{"runningTaskID"}
	ctxKeyVerifierSessionScope = &toolCtxKey{"verifierSessionScope"}
	ctxKeySearchPromotion      = &toolCtxKey{"searchPromotion"}
	ctxKeyAutoDenyAsk          = &toolCtxKey{"autoDenyAsk"}
)

// WithAutoDenyAsk marks a tool context as belonging to a turn with NOBODY to
// approve anything — a scheduled run, a heartbeat, an unattended delegated
// sub-turn. It carries the turn loop's EXISTING AutoDenyAsk field, deliberately
// and only that: a second, independently-computed notion of "is anyone there"
// would drift from the first, and the two would eventually disagree about the
// same turn.
//
// The agent loop already uses that field to auto-deny every `ask`-policy tool
// call. This exposes the same fact to a tool that needs to refuse a specific
// ARGUMENT rather than a whole call — something a tool policy cannot express,
// because a policy cannot see arguments. browser_handle_dialog is the first:
// dismissing a JavaScript dialog is always allowed, accepting one is agreeing
// on the user's behalf to whatever the page asked.
func WithAutoDenyAsk(ctx context.Context, v bool) context.Context {
	return context.WithValue(ctx, ctxKeyAutoDenyAsk, v)
}

// ToolAutoDenyAsk reports whether this turn has nobody to approve anything.
// FALSE when unset — an unstamped context is an ordinary attended turn, and
// defaulting the other way would refuse arguments on every code path that has
// not been taught to stamp it yet.
func ToolAutoDenyAsk(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, _ := ctx.Value(ctxKeyAutoDenyAsk).(bool)
	return v
}

// WithSearchPromotion returns a child context marking the enclosing
// markLoaded call as originating from ToolSearch's query (by-description)
// path, as opposed to its exact-name `names` path. ToolsTool.execSearchAndLoad
// sets this immediately before invoking its markLoaded resolver so the agent
// loop (which has no other way to distinguish the two ToolSearch call shapes
// once they reach the shared markLoaded closure) can record a pending
// search-follow-up entry only for genuine discoveries (ADR-071 §4.3.1a,
// FR-038a) — an exact-name load is a deliberate choice, not a search result,
// and must never be counted toward the no-followup metric.
func WithSearchPromotion(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeySearchPromotion, true)
}

// IsSearchPromotion reports whether ctx was marked by WithSearchPromotion.
func IsSearchPromotion(ctx context.Context) bool {
	v, _ := ctx.Value(ctxKeySearchPromotion).(bool)
	return v
}

// ProcessTrackerFunc records a child PID spawned by a tool so a caller (e.g. the
// scheduled-run lane, FR-011) can terminate it when the run finishes. It is
// installed on the tool context by the caller via WithProcessTracker; tools call
// TrackProcess after spawning a child. A nil tracker (the default for
// interactive runs) makes TrackProcess a no-op, so normal chat sessions are
// unaffected.
type ProcessTrackerFunc func(pid int)

// WithProcessTracker returns a child context carrying a child-process tracker.
// The scheduled runner installs one keyed to the run's session so spawned
// children can be cleaned up on completion (FR-011).
func WithProcessTracker(ctx context.Context, fn ProcessTrackerFunc) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyProcTracker, fn)
}

// TrackProcess reports a spawned child PID to the context's process tracker, if
// one is installed. No-op when no tracker is present (the common interactive
// case) or pid <= 0.
func TrackProcess(ctx context.Context, pid int) {
	if pid <= 0 {
		return
	}
	if fn, ok := ctx.Value(ctxKeyProcTracker).(ProcessTrackerFunc); ok && fn != nil {
		fn(pid)
	}
}

// WithToolContext returns a child context carrying channel and chatID.
func WithToolContext(ctx context.Context, channel, chatID string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyChannel, channel)
	ctx = context.WithValue(ctx, ctxKeyChatID, chatID)
	return ctx
}

// WithSessionKey returns a child context carrying the session key.
func WithSessionKey(ctx context.Context, sessionKey string) context.Context {
	return context.WithValue(ctx, ctxKeySessionKey, sessionKey)
}

// ToolSessionKey extracts the session key from ctx, or "" if unset.
func ToolSessionKey(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeySessionKey).(string)
	return v
}

// WithTranscriptSessionID returns a child context carrying the transcript session ID.
// This is the actual session directory ID (e.g., "session_01KP30THP63YFESKGECYYHYQWY"),
// different from the routing session key.
func WithTranscriptSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyTranscriptSessionID, id)
}

// ToolTranscriptSessionID extracts the transcript session ID, or "" if unset.
func ToolTranscriptSessionID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTranscriptSessionID).(string)
	return v
}

// ToolChannel extracts the channel from ctx, or "" if unset.
func ToolChannel(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyChannel).(string)
	return v
}

// ToolChatID extracts the chatID from ctx, or "" if unset.
func ToolChatID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyChatID).(string)
	return v
}

// WithAgentID returns a child context carrying the calling agent's ID.
func WithAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, ctxKeyAgentID, agentID)
}

// ToolAgentID extracts the agent ID from ctx, or "" if unset.
func ToolAgentID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyAgentID).(string)
	return v
}

// WithSessionOwner returns a child context carrying the session owner username.
// This is the authenticated user who created the session; empty string means
// unowned/shared (agent-created or scheduled runs with no human creator).
func WithSessionOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, ctxKeySessionOwner, owner)
}

// ToolSessionOwner extracts the session owner username from ctx, or "" if unset.
func ToolSessionOwner(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeySessionOwner).(string)
	return v
}

// WithWorkspaceID returns a child context carrying the workspace ID.
// The workspace ID identifies which per-workspace shared memory room to use (FR-7.1).
func WithWorkspaceID(ctx context.Context, workspaceID string) context.Context {
	return context.WithValue(ctx, ctxKeyWorkspaceID, workspaceID)
}

// ToolWorkspaceID extracts the workspace ID from ctx, or "" if unset.
// An empty workspace ID means the agent turn is not associated with a Spec-1
// Workspace; the memory store falls back to the private room only.
func ToolWorkspaceID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyWorkspaceID).(string)
	return v
}

// WithTurnWorkspaceDir returns a child context carrying the absolute filesystem
// directory that the current turn's file/exec tools should be rooted in. The
// agent loop sets this only when the turn's agent belongs to a Workspace's
// CoreTeam (workspace.FindForAgent); otherwise it leaves the context untouched
// so file/exec tools fall back to their fixed agent root.
//
// An empty dir is treated as "unset" (the loop never sets it to ""), so passing
// "" is a no-op rather than re-rooting to the process cwd.
func WithTurnWorkspaceDir(ctx context.Context, dir string) context.Context {
	if dir == "" {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyTurnWorkspaceDir, dir)
}

// TurnWorkspaceDir extracts the per-turn workspace filesystem root from ctx, or
// "" if unset. "" means no re-rooting: file/exec tools use their fixed agent
// root exactly as before the flag existed.
func TurnWorkspaceDir(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTurnWorkspaceDir).(string)
	return v
}

// WithDelegationDepth returns a child context carrying the delegation-chain
// depth of the turn currently executing. The agent loop seeds this when it
// runs a task (from the task's stored DelegationDepth) so task_create can stamp
// a child task's generation as parent+1 and reject a create that would exceed
// the task-depth bound. A root chat/agent turn leaves it 0.
func WithDelegationDepth(ctx context.Context, depth int) context.Context {
	if depth < 0 {
		depth = 0
	}
	return context.WithValue(ctx, ctxKeyDelegationDepth, depth)
}

// ToolDelegationDepth extracts the delegation-chain depth from ctx, or 0 if
// unset (a root chat/agent turn, or any non-task invocation).
func ToolDelegationDepth(ctx context.Context) int {
	v, _ := ctx.Value(ctxKeyDelegationDepth).(int)
	return v
}

// WithToolCallID returns a child context carrying the ID of the tool call
// currently being dispatched. The agent loop injects this immediately before
// every tool execution (mirroring the channel/chatID/agentID injections
// above), so a tool can learn its OWN call ID. DelegateTool uses it (W2:
// delegate action:"status" live-progress snapshot) to record, at
// task-creation time, the exact ID a spawned child sub-turn's transcript
// entries will carry back as ParentSpawnCallID — see
// session.TranscriptEntry.ParentSpawnCallID's doc comment and
// pkg/agent/subturn.go's parentSpawnCallID.
func WithToolCallID(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, ctxKeyToolCallID, toolCallID)
}

// ToolCallID extracts the current tool call's own ID from ctx, or "" if unset.
func ToolCallID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyToolCallID).(string)
	return v
}

// WithRunningTaskID returns a child context carrying the ID of the task
// currently being executed by the task-run turn in progress (review r2,
// Chunk 1). TaskExecutor stamps this onto the turn's tool ctx at
// task-dispatch time (runTask / runTaskFromInProgress, task_executor.go) so a
// task-update tool call can tell whether IT is genuinely part of THAT task's
// own executor run — the only run whose completion (finishTaskRun) ever
// adjudicates a staged PendingJudgeClaim. An out-of-band call (this context
// carries no running task, or a different one) must never stage a claim:
// nothing would ever adjudicate it, stranding the task non-terminal forever.
// Empty ("") for every non-task-run turn — interactive chat, /goal, /loop,
// scheduled runs, and sub-turns all leave this unset.
func WithRunningTaskID(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, ctxKeyRunningTaskID, taskID)
}

// ToolRunningTaskID extracts the currently-executing task's ID from ctx, or
// "" if unset (no task run in progress for this turn). See WithRunningTaskID.
func ToolRunningTaskID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyRunningTaskID).(string)
	return v
}

// WithVerifierSessionScope returns a child context carrying the set of
// session IDs the CURRENT verifier turn is authorized to inspect via the
// inspect_session tool (ADR-052 FR-033, R3-10/R3-11). The engine sets this
// BEFORE dispatching a verifier turn — it is never client-suppliable and is
// scoped per adjudication level: task verification -> that task's own
// session; plan verification -> that plan's member sessions (there is no
// "plan session" concept, GS-04); chat `/goal` verification -> that chat
// session only. inspect_session refuses any session id outside this set,
// unconditionally — the lock is enforced here (an engine-set context value),
// NOT expressible via tool policy. Mirrors WithRunningTaskID's precedent
// exactly: an engine-owned fact injected onto the turn context that a tool
// reads but can never itself set.
//
// An empty/nil sessionIDs leaves ctx untouched (mirrors WithTurnWorkspaceDir's
// "empty is unset" convention) so a normal (non-verifier) turn — which never
// calls this — carries no scope at all; VerifierSessionScopeAllows then
// fail-closed refuses every session id for that turn.
func WithVerifierSessionScope(ctx context.Context, sessionIDs []string) context.Context {
	if len(sessionIDs) == 0 {
		return ctx
	}
	allowed := make(map[string]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		if id != "" {
			allowed[id] = true
		}
	}
	if len(allowed) == 0 {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyVerifierSessionScope, allowed)
}

// VerifierSessionScopeAllows reports whether sessionID is inside the
// engine-set verifier target scope carried on ctx (WithVerifierSessionScope).
// FAIL CLOSED: an unset scope (no WithVerifierSessionScope call on this
// turn's context at all — every non-verifier turn) authorizes NOTHING, and
// an empty sessionID is never authorized. This is deliberate: the
// target-session lock has no tool-policy-expressible equivalent, so the
// zero-value (unset context) must never accidentally mean "no restriction".
func VerifierSessionScopeAllows(ctx context.Context, sessionID string) bool {
	if sessionID == "" {
		return false
	}
	allowed, _ := ctx.Value(ctxKeyVerifierSessionScope).(map[string]bool)
	return allowed != nil && allowed[sessionID]
}

// CitationTracker is installed on the tool context by the agent loop so the
// recall_memory tool can report which memories it surfaced in the current turn.
// The agent loop later scans the LLM's response text for references to those
// memories' IDs/titles and emits the cited_in (op:cited) counter event
// (FR-7.5 / NFR-1). Implementations live in pkg/agent (which can reach the
// room counters.jsonl paths); pkg/tools only sees the interface to avoid an
// import cycle. A nil/absent tracker (tests, legacy stores) makes
// RecordRecalled a no-op — recall still works, just without citation tracking.
type CitationTracker interface {
	// RecordRecalled reports the memories surfaced by a recall_memory call in
	// the current turn. Entries without an ID are ignored.
	RecordRecalled(entries []MemoryEntry)
}

// WithCitationTracker returns a child context carrying a CitationTracker.
func WithCitationTracker(ctx context.Context, t CitationTracker) context.Context {
	if t == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKeyCitationTracker, t)
}

// CitationTrackerFromContext returns the CitationTracker on ctx, or nil.
func CitationTrackerFromContext(ctx context.Context) CitationTracker {
	v, _ := ctx.Value(ctxKeyCitationTracker).(CitationTracker)
	return v
}

// AsyncCallback is a function type that async tools use to notify completion.
// When an async tool finishes its work, it calls this callback with the result.
//
// The ctx parameter allows the callback to be canceled if the agent is shutting down.
// The result parameter contains the tool's execution result.
type AsyncCallback func(ctx context.Context, result *ToolResult)

// AsyncExecutor is an optional interface that tools can implement to support
// asynchronous execution with completion callbacks.
//
// Unlike the old AsyncTool pattern (SetCallback + Execute), AsyncExecutor
// receives the callback as a parameter of ExecuteAsync. This eliminates the
// data race where concurrent calls could overwrite each other's callbacks
// on a shared tool instance.
//
// This is useful for:
//   - Long-running operations that shouldn't block the agent loop
//   - Subagent spawns that complete independently
//   - Background tasks that need to report results later
//
// Example:
//
//	func (t *DelegateTool) ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
//	    go func() {
//	        result := t.runSubagent(ctx, args)
//	        if cb != nil { cb(ctx, result) }
//	    }()
//	    return AsyncResult("Subagent spawned, will report back")
//	}
type AsyncExecutor interface {
	Tool
	// ExecuteAsync runs the tool asynchronously. The callback cb will be
	// invoked (possibly from another goroutine) when the async operation
	// completes. cb is guaranteed to be non-nil by the caller (registry).
	ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult
}

func ToolToSchema(tool Tool) map[string]any {
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  tool.Parameters(),
		},
	}
}
