// Package tools implements the Tool interface, the central ToolRegistry, and
// the full catalog of builtin tools available to Omnipus agents — the
// unified bash tool (ADR-036), delegate/hand_off (agent-to-agent delegation),
// filesystem, session, web, memory, messaging, skills, and MCP-backed tools.
// ToolRegistry (this file) is the single registration/dispatch point every
// agent's tool loop calls through; Tool (base.go) is the interface every
// tool implements; the compositor (compositor.go) applies per-agent
// scope/policy filtering on top of the registry's raw tool set.
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

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
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

// registerToolLocked saves a tool entry into the registry. Must be called
// with r.mu held. warnOnOverwrite distinguishes a genuinely unexpected
// name collision (Register/RegisterHidden — logged at WARN so an operator
// notices) from an intentional, expected replacement (RegisterReplacing —
// logged at DEBUG only): both still overwrite the entry identically, only
// the log level differs.
func (r *ToolRegistry) registerToolLocked(tool Tool, isCore bool, warnPrefix, debugLabel string, warnOnOverwrite bool) {
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		if warnOnOverwrite {
			logger.WarnCF("tools", warnPrefix+" overwrites existing tool",
				map[string]any{"name": name})
		} else {
			logger.DebugCF("tools", warnPrefix+" replaced existing tool (expected)",
				map[string]any{"name": name})
		}
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
	r.registerToolLocked(tool, true, "Tool registration", "Registered core tool", true)
}

// RegisterHidden saves hidden tools (visible only via TTL)
func (r *ToolRegistry) RegisterHidden(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registerToolLocked(tool, false, "Hidden tool registration", "Registered hidden tool", true)
}

// RegisterReplacing saves a core tool entry exactly like Register, except a
// same-name collision is treated as an EXPECTED, intentional replacement —
// logged at DEBUG, not WARN. Use this at a wiring site that legitimately
// re-registers the same tool name more than once as part of normal
// operation (e.g. pkg/agent's wirePlanToolsForAgent/SetPlanStore re-wiring
// the ADR-052 plan/task tool surface for every already-registered agent
// once the real plan.Store becomes available — previously this produced 4
// spurious "Tool registration overwrites existing tool" WARNs per agent on
// every such re-wire, 7-reviewer gate item 5). Register/RegisterHidden
// remain the right choice everywhere a same-name collision is NOT expected
// and should stay loud.
func (r *ToolRegistry) RegisterReplacing(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.registerToolLocked(tool, true, "Tool registration", "Registered core tool", false)
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
// registry version at which it was taken. Used by ToolsTool BM25 engine cache.
type HiddenToolSnapshot struct {
	Docs    []HiddenToolDoc
	Version uint64
}

// HiddenToolDoc is a lightweight representation of a hidden tool for search indexing.
type HiddenToolDoc struct {
	Name        string
	Description string
}

// SnapshotSearchableTools returns all tools that should be included in the
// BM25 search corpus, and the current registry version, under a single
// read-lock.
//
// The corpus includes:
//   - Non-core (hidden/MCP) tools — always loadable on demand.
//   - Core (visible) tools whose manifest tier is ManifestLazy — these are
//     visible in the manifest but may need to be loaded explicitly; they were
//     previously invisible to BM25 search, causing "exact name required" gaps.
//
// Full-tier and infra-tier tools are excluded: they are always callable without
// loading and do not need to be discoverable via search.
//
// Deduplication by name is guaranteed by the registry's map representation.
func (r *ToolRegistry) SnapshotSearchableTools() HiddenToolSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	docs := make([]HiddenToolDoc, 0, len(r.tools))
	for name, entry := range r.tools {
		if !entry.IsCore {
			// Non-core (hidden/MCP) tools are always included.
			docs = append(docs, HiddenToolDoc{
				Name:        name,
				Description: entry.Tool.Description(),
			})
		} else if ToolManifestTier(name) == ManifestLazy {
			// Core (visible) tools that are lazy-tier: the model must load them
			// explicitly, so they must be discoverable via BM25 search.
			docs = append(docs, HiddenToolDoc{
				Name:        name,
				Description: entry.Tool.Description(),
			})
		}
		// Full-tier and infra-tier core tools are always callable and excluded.
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

// GetIncludingHidden returns the registered tool for name regardless of whether
// it is hidden or its TTL has expired. Unlike Get, it does NOT gate on TTL — it
// exists for policy evaluation (canLoad), where a deferred/hidden MCP tool must
// be resolvable BEFORE it is promoted (the load path promotes it before fetching
// the schema). Returns false only when name is not registered at all.
func (r *ToolRegistry) GetIncludingHidden(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.tools[name]
	if !ok {
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
			"tool": name,
			"args": args,
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

	// Recover arguments corrupted by a model that leaked its tool-call
	// template control tokens into a string value (A1 — see argrepair.go).
	// Runs before validation so a recoverable call (e.g. op="create_view"
	// with `type` swallowed into op's value) is repaired into the call the
	// model meant, rather than rejected with an unusable enum error. A no-op
	// for every well-formed call: nothing fires unless a value carries a
	// literal <arg_key>/<arg_value> template tag.
	if repairLeakedToolArgs(args) {
		logger.WarnCF("tool", "repaired leaked tool-call template tokens in arguments",
			map[string]any{"tool": name})
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

	result = normalizeToolResult(ctx, result, name, r.mediaStore, channel, chatID)

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
			slog.Error("SEC-15: audit log write failed for tool execution",
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
// tools registered on the parent after cloning (e.g. delegate) will NOT be
// visible to the clone, preventing recursive subagent spawning.
// The version counter is reset to 0 in the clone as it's a new independent registry.
func (r *ToolRegistry) Clone() *ToolRegistry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := &ToolRegistry{
		tools:             make(map[string]*ToolEntry, len(r.tools)),
		mediaStore:        r.mediaStore,
		auditLogger:       r.auditLogger,
		memoryRateLimiter: r.memoryRateLimiter,
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
	// ExcludedDelegate is the unified delegation tool (DelegateTool — ADR-036
	// merge of the former spawn/run_subagent/check_spawn_status trio). Prior to
	// the merge this was two separate names, ExcludedSpawn ("spawn") and
	// ExcludedSubagent ("run_subagent") — both collapsed into this one entry
	// since both tools are now the same "delegate" registration.
	//
	// REVERSED (ADR-040, 2026-07-12): this constant is no longer passed at the
	// production call site (pkg/agent/subturn.go's spawnSubTurn now calls
	// CloneExcept(tools.ExcludedHandoff) only — see that call site's own
	// comment). FR-H-006's registry-level "one level only for general
	// subagents" block pre-empted the per-workspace delegation trust-graph
	// (ADR-037) from ever running for nested delegation, silently overriding
	// an operator's explicit, wired, unrestricted trust edge. Nested
	// delegation is now governed exclusively by that trust-graph/mode/depth
	// gate (DelegateTool.Execute's deny-checker, `resolveEffectiveDelegationDepth`),
	// which is fail-closed on its own and does not depend on this exclusion.
	// ExcludedDelegate itself is NOT deleted — it remains a valid CloneExcept
	// primitive, still exercised by tests, for any future caller that
	// legitimately needs to omit `delegate` from a cloned registry; it is
	// simply no longer applied unconditionally to every child sub-turn.
	ExcludedDelegate ExcludedTool = "delegate"
	// ExcludedHandoff is the agent-switch tool. Excluded from child registries to
	// prevent sub-turns from hijacking the active agent session (FR-H-006).
	ExcludedHandoff ExcludedTool = "hand_off"
)

// CloneExcept creates an independent copy of the registry omitting the named tools.
// It is used to construct child sub-turn registries that must not have access to
// certain tools. The version counter is reset to 0 in the clone as it is a new
// independent registry.
//
// The canonical production call site is now CloneExcept(ExcludedHandoff) only
// (pkg/agent/subturn.go's spawnSubTurn) — a child sub-turn must never be able
// to hijack the active agent session via hand_off, but CAN delegate onward to
// a grandchild, governed instead by the per-workspace delegation trust-graph's
// mode/depth gate. This reverses the prior "a child sub-turn must never be
// able to delegate to a grandchild" rule that used to live here: see
// ADR-040 (docs/internal/architecture/ADR-040-fr-h-006-nested-delegation-reversal.md)
// for the full root-cause and rationale. ExcludedDelegate is unaffected as a
// CloneExcept primitive — see its own doc comment above.
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
			slog.Warn("CloneExcept: tool not in base registry",
				"tool", name,
				"hint", "check for renamed or unregistered tool",
			)
		}
	}
	clone := &ToolRegistry{
		tools:             make(map[string]*ToolEntry, len(r.tools)),
		mediaStore:        r.mediaStore,
		auditLogger:       r.auditLogger,
		memoryRateLimiter: r.memoryRateLimiter,
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
