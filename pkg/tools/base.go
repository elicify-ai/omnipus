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
	// Examples: read_file, list_dir, web_search, web_fetch, message, task_*.
	ScopeGeneral ToolScope = "general"
)

// ToolCategory groups tools by function for the UI tool picker and for the
// Category() method on the Tool interface.
// These values align with the existing CatalogEntry categories.
type ToolCategory string

const (
	CategoryCore          ToolCategory = "core"   // default for BaseTool
	CategorySystem        ToolCategory = "system" // sysagent tools
	CategoryFile          ToolCategory = "file"
	CategoryCode          ToolCategory = "code"
	CategoryWeb           ToolCategory = "web"
	CategoryBrowser       ToolCategory = "browser"
	CategoryCommunication ToolCategory = "communication"
	CategoryTask          ToolCategory = "task"
	CategoryAutomation    ToolCategory = "automation"
	CategorySearch        ToolCategory = "search"
	CategorySkills        ToolCategory = "skills"
	CategoryHardware      ToolCategory = "hardware"
	CategoryWorkspace     ToolCategory = "workspace"
	CategoryMCP           ToolCategory = "mcp"
)

// Tool is the interface that all tools must implement.
//
// RequiresAdminAsk and Category have default implementations via BaseTool.
// Embed BaseTool in your tool struct to inherit the defaults without needing to
// implement these methods explicitly:
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
	// RequiresAdminAsk returns true when the tool must be approved by an admin
	// user before execution on a custom agent, regardless of the configured
	// policy. All tools in pkg/sysagent/tools/ return true.
	// Default (via BaseTool): false.
	RequiresAdminAsk() bool
	// Category returns the functional category for the tool picker UI.
	// Default (via BaseTool): CategoryCore.
	Category() ToolCategory
}

// BaseTool provides zero-value default implementations of RequiresAdminAsk and
// Category so that existing tool structs only need to embed BaseTool to satisfy
// the full Tool interface without mass-modifying every file.
//
// Usage:
//
//	type MyTool struct {
//	    tools.BaseTool
//	    // ... other fields
//	}
//
// MyTool then inherits:
//   - RequiresAdminAsk() bool  → false
//   - Category() ToolCategory  → CategoryCore
//
// Override either method on the embedding struct as needed.
type BaseTool struct{}

// RequiresAdminAsk returns false for generic tools. Override to return true on
// privileged tools (e.g., every tool in pkg/sysagent/tools/).
func (BaseTool) RequiresAdminAsk() bool { return false }

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
	ctxKeyChannel             = &toolCtxKey{"channel"}
	ctxKeyChatID              = &toolCtxKey{"chatID"}
	ctxKeyAgentID             = &toolCtxKey{"agentID"}
	ctxKeySessionKey          = &toolCtxKey{"sessionKey"}
	ctxKeyTranscriptSessionID = &toolCtxKey{"transcriptSessionID"}
)

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
//	func (t *SpawnTool) ExecuteAsync(ctx context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
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
