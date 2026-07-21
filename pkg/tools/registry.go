package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/media"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

type ToolEntry struct {
	Tool   Tool
	IsCore bool
	TTL    int
}

type ToolRegistry struct {
	tools             map[string]*ToolEntry
	mu                sync.RWMutex
	version           atomic.Uint64 // incremented on Register/RegisterHidden for cache invalidation
	mediaStore        media.MediaStore
	auditLogger       *audit.Logger      // SEC-15: structured audit logging for tool executions
	memoryRateLimiter *MemoryRateLimiter // v0.2 #155 item 6: rate-limit memory writes
}

type mediaStoreAware interface {
	SetMediaStore(store media.MediaStore)
}

// auditLoggerAware is implemented by tools that need direct access to the
// audit logger for emitting their own specialised audit events (e.g. memory
// tools that must log content_sha256 without relying on the registry's generic
// tool_call entry). The registry propagates the logger on SetAuditLogger.
type auditLoggerAware interface {
	SetAuditLogger(logger *audit.Logger)
}

// memoryRateLimiterAware is implemented by tools that participate in the
// memory-write rate-limit gate (v0.2 #155 item 6). The registry propagates a
// shared limiter on SetMemoryRateLimiter; tools that do not implement this
// interface are unaffected.
type memoryRateLimiterAware interface {
	SetMemoryRateLimiter(limiter *MemoryRateLimiter)
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*ToolEntry),
	}
}

// registerToolLocked saves a tool entry into the registry. Must be called with r.mu held.
func (r *ToolRegistry) registerToolLocked(tool Tool, isCore bool, warnPrefix, debugLabel string) {
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		logger.WarnCF("tools", warnPrefix+" overwrites existing tool",
			map[string]any{"name": name})
	}
	r.tools[name] = &ToolEntry{
		Tool:   tool,
		IsCore: isCore,
		TTL:    0,
	}
	if aware, ok := tool.(mediaStoreAware); ok && r.mediaStore != nil {
		aware.SetMediaStore(r.mediaStore)
	}
	if aware, ok := tool.(auditLoggerAware); ok && r.auditLogger != nil {
		aware.SetAuditLogger(r.auditLogger)
	}
	if aware, ok := tool.(memoryRateLimiterAware); ok && r.memoryRateLimiter != nil {
		aware.SetMemoryRateLimiter(r.memoryRateLimiter)
	}
	r.version.Add(1)
	logger.DebugCF("tools", debugLabel, map[string]any{"name": name})
}

func (r *ToolRegistry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registerToolLocked(tool, true, "Tool registration", "Registered core tool")
}

// RegisterHidden saves hidden tools (visible only via TTL)
func (r *ToolRegistry) RegisterHidden(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registerToolLocked(tool, false, "Hidden tool registration", "Registered hidden tool")
}

// SetMediaStore injects a MediaStore into all registered tools that can
// consume it, and remembers it for future registrations.
func (r *ToolRegistry) SetMediaStore(store media.MediaStore) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.mediaStore = store
	for _, entry := range r.tools {
		if aware, ok := entry.Tool.(mediaStoreAware); ok {
			aware.SetMediaStore(store)
		}
	}
}

// SetAuditLogger injects an audit Logger into the registry for tool execution
// audit logging (SEC-15). Following the SetMediaStore pattern for dependency injection.
// Also propagates the logger to any registered tools that implement auditLoggerAware
// so per-tool structured audit events (e.g. memory content_sha256) work immediately.
func (r *ToolRegistry) SetAuditLogger(logger *audit.Logger) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auditLogger = logger
	for _, entry := range r.tools {
		if aware, ok := entry.Tool.(auditLoggerAware); ok {
			aware.SetAuditLogger(logger)
		}
	}
}

// SetMemoryRateLimiter injects a MemoryRateLimiter (v0.2 #155 item 6) into
// the registry. The limiter is propagated to any registered tools that
// implement memoryRateLimiterAware (currently RememberTool and
// RetrospectiveTool) so their writes are gated.
//
// Pass nil to clear (used by tests and by environments that explicitly
// disable rate limiting). A nil limiter at the tool boundary becomes a
// no-op: Allow returns true unconditionally.
//
// Following the SetMediaStore / SetAuditLogger pattern for dependency
// injection across the registry.
func (r *ToolRegistry) SetMemoryRateLimiter(limiter *MemoryRateLimiter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.memoryRateLimiter = limiter
	for _, entry := range r.tools {
		if aware, ok := entry.Tool.(memoryRateLimiterAware); ok {
			aware.SetMemoryRateLimiter(limiter)
		}
	}
}

// Unregister removes a tool from the registry. Used by fail-closed paths where
// we need to strip a tool that could not be securely wired.
func (r *ToolRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; !exists {
		return
	}
	delete(r.tools, name)
	r.version.Add(1)
	logger.DebugCF("tools", "Unregistered tool", map[string]any{"name": name})
}

// PromoteTools atomically sets the TTL for multiple non-core tools.
// This prevents a concurrent TickTTL from decrementing between promotions.
func (r *ToolRegistry) PromoteTools(names []string, ttl int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	promoted := 0
	for _, name := range names {
		if entry, exists := r.tools[name]; exists {
			if !entry.IsCore {
				entry.TTL = ttl
				promoted++
			}
		}
	}
	logger.DebugCF(
		"tools",
		"PromoteTools completed",
		map[string]any{"requested": len(names), "promoted": promoted, "ttl": ttl},
	)
}

// TickTTL decreases TTL only for non-core tools
func (r *ToolRegistry) TickTTL() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, entry := range r.tools {
		if !entry.IsCore && entry.TTL > 0 {
			entry.TTL--
		}
	}
}

// Version returns the current registry version (atomically).
func (r *ToolRegistry) Version() uint64 {
	return r.version.Load()
}

// HiddenToolSnapshot holds a consistent snapshot of hidden tools and the
// registry version at which it was taken. Used by BM25SearchTool cache.
type HiddenToolSnapshot struct {
	Docs    []HiddenToolDoc
	Version uint64
}

// HiddenToolDoc is a lightweight representation of a hidden tool for search indexing.
type HiddenToolDoc struct {
	Name        string
	Description string
}

// SnapshotHiddenTools returns all non-core tools and the current registry
// version under a single read-lock, guaranteeing consistency between the
// two values.
func (r *ToolRegistry) SnapshotHiddenTools() HiddenToolSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	docs := make([]HiddenToolDoc, 0, len(r.tools))
	for name, entry := range r.tools {
		if !entry.IsCore {
			docs = append(docs, HiddenToolDoc{
				Name:        name,
				Description: entry.Tool.Description(),
			})
		}
	}
	return HiddenToolSnapshot{
		Docs:    docs,
		Version: r.version.Load(),
	}
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.tools[name]
	if !ok {
		return nil, false
	}
	// Hidden tools with expired TTL are not callable.
	if !entry.IsCore && entry.TTL <= 0 {
		return nil, false
	}
	return entry.Tool, true
}

func (r *ToolRegistry) Execute(ctx context.Context, name string, args map[string]any) *ToolResult {
	return r.ExecuteWithContext(ctx, name, args, "", "", nil)
}

// ExecuteWithContext executes a tool with channel/chatID context and optional async callback.
// If the tool implements AsyncExecutor and a non-nil callback is provided,
// ExecuteAsync is called instead of Execute — the callback is a parameter,
// never stored as mutable state on the tool.
func (r *ToolRegistry) ExecuteWithContext(
	ctx context.Context,
	name string,
	args map[string]any,
	channel, chatID string,
	asyncCallback AsyncCallback,
) *ToolResult {
	logger.InfoCF("tool", "Tool execution started",
		map[string]any{
			"tool":      name,
			"arg_count": len(args),
		})

	// Capture auditLogger under lock to avoid a data race with SetAuditLogger.
	r.mu.RLock()
	auditLog := r.auditLogger
	r.mu.RUnlock()

	tool, ok := r.Get(name)
	if !ok {
		logger.ErrorCF("tool", "Tool not found",
			map[string]any{
				"tool": name,
			})
		return ErrorResult(fmt.Sprintf("tool %q not found", name)).WithError(fmt.Errorf("tool not found"))
	}

	// Validate arguments against the tool's declared schema.
	if err := validateToolArgs(tool.Parameters(), args); err != nil {
		logger.WarnCF("tool", "Tool argument validation failed",
			map[string]any{"tool": name, "error": err.Error()})
		return ErrorResult(fmt.Sprintf("invalid arguments for tool %q: %s", name, err)).
			WithError(fmt.Errorf("argument validation failed: %w", err))
	}

	// Inject channel/chatID into ctx so tools read them via ToolChannel(ctx)/ToolChatID(ctx).
	// Always inject — tools validate what they require.
	ctx = WithToolContext(ctx, channel, chatID)

	// If tool implements AsyncExecutor and callback is provided, use ExecuteAsync.
	// The callback is a call parameter, not mutable state on the tool instance.
	var result *ToolResult
	start := time.Now()

	// Use recover to catch any panics during tool execution
	// This prevents tool crashes from killing the entire agent
	func() {
		defer func() {
			if re := recover(); re != nil {
				errMsg := fmt.Sprintf("Tool '%s' crashed with panic: %v", name, re)
				logger.ErrorCF("tool", "Tool execution panic recovered",
					map[string]any{
						"tool":  name,
						"panic": fmt.Sprintf("%v", re),
					})
				result = &ToolResult{
					ForLLM:  errMsg,
					ForUser: errMsg,
					IsError: true,
					Err:     fmt.Errorf("panic: %v", re),
				}
			}
		}()

		if asyncExec, ok := tool.(AsyncExecutor); ok && asyncCallback != nil {
			logger.DebugCF("tool", "Executing async tool via ExecuteAsync",
				map[string]any{
					"tool": name,
				})
			result = asyncExec.ExecuteAsync(ctx, args, asyncCallback)
		} else {
			result = tool.Execute(ctx, args)
		}
	}()

	// Handle nil result (should not happen, but defensive)
	if result == nil {
		result = &ToolResult{
			ForLLM:  fmt.Sprintf("Tool '%s' returned nil result unexpectedly", name),
			ForUser: fmt.Sprintf("Tool '%s' returned nil result unexpectedly", name),
			IsError: true,
			Err:     fmt.Errorf("nil result from tool"),
		}
	}

	result = normalizeToolResult(result, name, r.mediaStore, channel, chatID)

	duration := time.Since(start)

	// Log based on result type
	if result.IsError {
		logger.ErrorCF("tool", "Tool execution failed",
			map[string]any{
				"tool":     name,
				"duration": duration.Milliseconds(),
				"error":    result.ForLLM,
			})
	} else if result.Async {
		logger.InfoCF("tool", "Tool started (async)",
			map[string]any{
				"tool":     name,
				"duration": duration.Milliseconds(),
			})
	} else {
		logger.InfoCF("tool", "Tool execution completed",
			map[string]any{
				"tool":          name,
				"duration_ms":   duration.Milliseconds(),
				"result_length": len(result.ContentForLLM()),
			})
	}

	// SEC-15: Write structured audit entry for every tool execution.
	// Audit logging is best-effort — errors are logged via slog but never
	// propagate to the caller. The audit logger handles its own degraded-mode
	// recovery internally.
	if auditLog != nil {
		agentID := ToolAgentID(ctx)
		decision := audit.DecisionAllow
		if result.IsError {
			decision = audit.DecisionError
		}
		if err := auditLog.Log(&audit.Entry{
			Event:    audit.EventToolCall,
			Decision: decision,
			AgentID:  agentID,
			Tool:     name,
			Details: map[string]any{
				"duration_ms": duration.Milliseconds(),
			},
		}); err != nil {
			logger.SlogError("SEC-15: audit log write failed for tool execution",
				"tool", name, "agent", agentID, "error", err)
		}
	}

	return result
}

// sortedToolNames returns tool names in sorted order for deterministic iteration.
// This is critical for KV cache stability: non-deterministic map iteration would
// produce different system prompts and tool definitions on each call, invalidating
// the LLM's prefix cache even when no tools have changed.
func (r *ToolRegistry) sortedToolNames() []string {
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *ToolRegistry) GetDefinitions() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sorted := r.sortedToolNames()
	definitions := make([]map[string]any, 0, len(sorted))
	for _, name := range sorted {
		entry := r.tools[name]

		if !entry.IsCore && entry.TTL <= 0 {
			continue
		}

		definitions = append(definitions, ToolToSchema(entry.Tool))
	}
	return definitions
}

// ToProviderDefs converts tool definitions to provider-compatible format.
// This is the format expected by LLM provider APIs.
func (r *ToolRegistry) ToProviderDefs() []providers.ToolDefinition {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sorted := r.sortedToolNames()
	definitions := make([]providers.ToolDefinition, 0, len(sorted))
	for _, name := range sorted {
		entry := r.tools[name]

		if !entry.IsCore && entry.TTL <= 0 {
			continue
		}

		schema := ToolToSchema(entry.Tool)

		// Safely extract nested values with type checks
		fn, ok := schema["function"].(map[string]any)
		if !ok {
			logger.WarnCF(
				"tools",
				"skipping malformed tool schema — missing or invalid \"function\" key",
				map[string]any{"tool": name},
			)
			continue
		}

		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]any)

		definitions = append(definitions, providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        SanitizeToolName(name),
				Description: desc,
				Parameters:  params,
			},
		})
	}
	return definitions
}

// ToolsToProviderDefs converts a slice of Tool to providers.ToolDefinition without
// requiring a ToolRegistry. Used by the LLM-call assembly path (FR-003, FR-041) to
// convert the policy-filtered tool list to the format expected by LLM providers.
func ToolsToProviderDefs(toolSlice []Tool) []providers.ToolDefinition {
	definitions := make([]providers.ToolDefinition, 0, len(toolSlice))
	for _, t := range toolSlice {
		schema := ToolToSchema(t)
		fn, ok := schema["function"].(map[string]any)
		if !ok {
			logger.WarnCF("tools", "skipping malformed tool schema in ToolsToProviderDefs",
				map[string]any{"tool": t.Name()})
			continue
		}
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]any)
		definitions = append(definitions, providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        SanitizeToolName(name),
				Description: desc,
				Parameters:  params,
			},
		})
	}
	return definitions
}

// SanitizeToolName replaces characters invalid for LLM APIs (dots, colons)
// with underscores. Anthropic/Azure require ^[a-zA-Z0-9_-]{1,128}$.
func SanitizeToolName(name string) string {
	return strings.ReplaceAll(name, ".", "_")
}

// UnsanitizeToolName reverses SanitizeToolName — maps LLM tool names back
// to internal names (e.g., "browser_navigate" → "browser.navigate").
// Only applies to known prefixes to avoid false positives.
func (r *ToolRegistry) UnsanitizeToolName(name string) string {
	// Try the name as-is first (most tools have no dots).
	if _, ok := r.tools[name]; ok {
		return name
	}
	// Try replacing underscores with dots for known prefixes.
	dotName := strings.ReplaceAll(name, "_", ".")
	if _, ok := r.tools[dotName]; ok {
		return dotName
	}
	// Try just the first underscore → dot (e.g., "browser_navigate" → "browser.navigate").
	if idx := strings.IndexByte(name, '_'); idx > 0 {
		candidate := name[:idx] + "." + name[idx+1:]
		if _, ok := r.tools[candidate]; ok {
			return candidate
		}
	}
	return name // no mapping found — return as-is
}

// List returns a list of all registered tool names.
func (r *ToolRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.sortedToolNames()
}

// cloneEntry returns a shallow copy of a ToolEntry.
// Shared by Clone and CloneExcept so the field list cannot drift between
// the two methods. When a new field is added to ToolEntry, update ONLY this
// function and both Clone + CloneExcept pick up the change automatically.
//
// IMPORTANT: keep this in sync with the ToolEntry struct definition above.
func cloneEntry(e *ToolEntry) *ToolEntry {
	return &ToolEntry{
		Tool:   e.Tool,
		IsCore: e.IsCore,
		TTL:    e.TTL,
	}
}

// Clone creates an independent copy of the registry containing the same tool
// entries (shallow copy of each ToolEntry). This is used to give subagents a
// snapshot of the parent agent's tools without sharing the same registry —
// tools registered on the parent after cloning (e.g. spawn, spawn_status)
// will NOT be visible to the clone, preventing recursive subagent spawning.
// The version counter is reset to 0 in the clone as it's a new independent registry.
func (r *ToolRegistry) Clone() *ToolRegistry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := &ToolRegistry{
		tools:       make(map[string]*ToolEntry, len(r.tools)),
		mediaStore:  r.mediaStore,
		auditLogger: r.auditLogger,
	}
	for name, entry := range r.tools {
		clone.tools[name] = cloneEntry(entry)
	}
	return clone
}

// ExcludedTool is the opaque identifier for a tool name to be excluded from a
// cloned registry. Using a named type prevents accidental mixing with arbitrary
// tool name strings at call sites — the compiler rejects mismatched types without
// an explicit conversion.
type ExcludedTool string

const (
	// ExcludedSpawn is the async delegation tool (SpawnTool). Excluded from child
	// sub-turn registries so grandchild spawning is impossible (FR-H-006).
	ExcludedSpawn ExcludedTool = "spawn"
	// ExcludedSubagent is the sync delegation tool (SubagentTool). Also excluded
	// from child registries — a subagent calling subagent would create a grandchild.
	ExcludedSubagent ExcludedTool = "subagent"
	// ExcludedHandoff is the agent-switch tool. Excluded from child registries to
	// prevent sub-turns from hijacking the active agent session (FR-H-006).
	ExcludedHandoff ExcludedTool = "handoff"
)

// CloneExcept creates an independent copy of the registry omitting the named tools.
// It is used to construct child sub-turn registries that must not have access to
// certain tools (FR-H-006). The canonical call site is
// CloneExcept(ExcludedSpawn, ExcludedSubagent, ExcludedHandoff): a child sub-turn
// must never be able to spawn grandchildren, create nested subagents, or hand off
// to another agent. The version counter is reset to 0 in the clone as it is a new
// independent registry.
//
// Existence check: each ExcludedTool name is validated against the base registry.
// If a named tool is absent, slog.Warn is emitted and processing continues — this
// is a production-safe guard that does not panic on typos. The check prevents
// silent no-ops (e.g., a renamed tool that should still be excluded).
//
// IMPORTANT: keep field list in sync with Clone() and ToolEntry. A new field on
// ToolEntry must also be copied here (via cloneEntry), or the child registry will
// silently forget it. Add the field to cloneEntry above — not inline here.
func (r *ToolRegistry) CloneExcept(tools ...ExcludedTool) *ToolRegistry {
	excluded := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		excluded[string(t)] = struct{}{}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Existence check: warn if any excluded name is not in the base registry.
	// Non-fatal so production never crashes on a typo.
	for name := range excluded {
		if _, exists := r.tools[name]; !exists {
			logger.SlogWarn("CloneExcept: tool not in base registry",
				"tool", name,
				"hint", "check for renamed or unregistered tool",
			)
		}
	}
	clone := &ToolRegistry{
		tools:       make(map[string]*ToolEntry, len(r.tools)),
		mediaStore:  r.mediaStore,
		auditLogger: r.auditLogger,
	}
	for name, entry := range r.tools {
		if _, skip := excluded[name]; skip {
			continue
		}
		clone.tools[name] = cloneEntry(entry)
	}
	return clone
}

// Count returns the number of registered tools.
func (r *ToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// GetSummaries returns human-readable summaries of all registered tools.
// Returns a slice of "name - description" strings.
func (r *ToolRegistry) GetSummaries() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sorted := r.sortedToolNames()
	summaries := make([]string, 0, len(sorted))
	for _, name := range sorted {
		entry := r.tools[name]

		if !entry.IsCore && entry.TTL <= 0 {
			continue
		}

		summaries = append(summaries, fmt.Sprintf("- `%s` - %s", entry.Tool.Name(), entry.Tool.Description()))
	}
	return summaries
}

// GetAll returns all registered tools (both core and non-core with TTL > 0).
// Used by SubTurn to inherit parent's tool set.
func (r *ToolRegistry) GetAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sorted := r.sortedToolNames()
	tools := make([]Tool, 0, len(sorted))
	for _, name := range sorted {
		entry := r.tools[name]

		// Include core tools and non-core tools with active TTL
		if entry.IsCore || entry.TTL > 0 {
			tools = append(tools, entry.Tool)
		}
	}
	return tools
}
