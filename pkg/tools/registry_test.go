package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// --- mock types ---

type mockRegistryTool struct {
	name   string
	desc   string
	params map[string]any
	result *ToolResult
}

func (m *mockRegistryTool) Name() string               { return m.name }
func (m *mockRegistryTool) Description() string        { return m.desc }
func (m *mockRegistryTool) Parameters() map[string]any { return m.params }
func (m *mockRegistryTool) Scope() ToolScope           { return ScopeGeneral }
func (m *mockRegistryTool) Category() ToolCategory     { return CategoryCore }
func (m *mockRegistryTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return m.result
}

type mockContextAwareTool struct {
	mockRegistryTool
	lastCtx context.Context
}

func (m *mockContextAwareTool) Execute(ctx context.Context, _ map[string]any) *ToolResult {
	m.lastCtx = ctx
	return m.result
}

type mockAsyncRegistryTool struct {
	mockRegistryTool
	lastCB AsyncCallback
}

func (m *mockAsyncRegistryTool) ExecuteAsync(_ context.Context, args map[string]any, cb AsyncCallback) *ToolResult {
	m.lastCB = cb
	return m.result
}

type mockMediaStoreAwareTool struct {
	mockRegistryTool
	store media.MediaStore
}

func (m *mockMediaStoreAwareTool) SetMediaStore(store media.MediaStore) {
	m.store = store
}

// --- helpers ---

func newMockTool(name, desc string) *mockRegistryTool {
	return &mockRegistryTool{
		name:   name,
		desc:   desc,
		params: map[string]any{"type": "object"},
		result: SilentResult("ok"),
	}
}

// --- tests ---

func TestNewToolRegistry(t *testing.T) {
	r := NewToolRegistry()
	if r.Count() != 0 {
		t.Errorf("expected empty registry, got count %d", r.Count())
	}
	if len(r.List()) != 0 {
		t.Errorf("expected empty list, got %v", r.List())
	}
}

func TestToolRegistry_RegisterAndGet(t *testing.T) {
	r := NewToolRegistry()
	tool := newMockTool("echo", "echoes input")
	r.Register(tool)

	got, ok := r.Get("echo")
	if !ok {
		t.Fatal("expected to find registered tool")
	}
	if got.Name() != "echo" {
		t.Errorf("expected name 'echo', got %q", got.Name())
	}
}

func TestToolRegistry_Get_NotFound(t *testing.T) {
	r := NewToolRegistry()
	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected ok=false for unregistered tool")
	}
}

// TestToolRegistry_RegisterOverwrite originally pinned the PRE-#278
// behaviour: a second Register() call for an already-registered name
// silently replaced the first. Issue #278 (registry hijack) deliberately
// reverses that contract in registerToolLocked (registry.go): Register/
// RegisterHidden now reject a same-name collision outright and keep the
// FIRST (incumbent) registration, because a caller of these void-returning
// methods has no other way to detect or survive a hostile same-name
// registration (see pkg/tools/mcp_registration_guard_test.go's
// TestRegistry_Register_MustNotSilentlyOverwriteOnCollision, the #278
// requirement test this change satisfies).
//
// This is NOT protecting a legitimate re-registration path: every
// currently-existing caller that legitimately re-registers a tool under a
// name already in the registry either (a) builds a brand-new ToolRegistry
// per agent instance (pkg/agent/instance.go's NewAgentInstance — no
// pre-existing entries to collide with), (b) checks
// GetIncludingHidden(name) before calling Register/RegisterHidden and skips
// the call entirely when the name is already present (loop_mcp.go's
// registerServerTools, the MCP reconcile path — verified by reading
// loop_mcp.go directly: an unchanged server's tools never reach
// Register/RegisterHidden a second time), or (c) uses RegisterReplacing
// instead of Register specifically because it IS an intentional
// replacement (wirePlanToolsForAgent re-wiring the plan/task tool surface).
// So the old silent-overwrite behaviour this test pinned had no real
// caller depending on it, and the test is updated here to assert the new,
// correct contract instead of being weakened or deleted.
func TestToolRegistry_RegisterOverwrite(t *testing.T) {
	r := NewToolRegistry()
	r.Register(newMockTool("dup", "first"))
	r.Register(newMockTool("dup", "second"))

	if r.Count() != 1 {
		t.Errorf("expected count 1 after a rejected collision, got %d", r.Count())
	}
	tool, ok := r.Get("dup")
	if !ok {
		t.Fatalf("expected %q to remain registered after a rejected collision, got not-found", "dup")
	}
	if tool.Description() != "first" {
		t.Errorf("#278: a same-name collision must NOT overwrite the incumbent registration; "+
			"expected the original description %q to be preserved, got %q", "first", tool.Description())
	}
}

func TestToolRegistry_Execute_Success(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockRegistryTool{
		name:   "greet",
		desc:   "says hello",
		params: map[string]any{},
		result: SilentResult("hello"),
	})

	result := r.Execute(context.Background(), "greet", nil)
	if result.IsError {
		t.Errorf("expected success, got error: %s", result.ForLLM)
	}
	if result.ForLLM != "hello" {
		t.Errorf("expected ForLLM 'hello', got %q", result.ForLLM)
	}
}

func TestToolRegistry_Execute_NotFound(t *testing.T) {
	r := NewToolRegistry()
	result := r.Execute(context.Background(), "missing", nil)
	if !result.IsError {
		t.Error("expected error for missing tool")
	}
	if !strings.Contains(result.ForLLM, "not found") {
		t.Errorf("expected 'not found' in error, got %q", result.ForLLM)
	}
	if result.Err == nil {
		t.Error("expected Err to be set via WithError")
	}
}

// TestToolRegistry_Execute_RetiredNameGetsActionableError reproduces the
// live UAT finding that calling the pre-ADR-071-D1 name "load_tool" is
// rejected outright: it must still fail (no dispatch-time alias — ADR-071
// §8.D explicitly rejected keeping a retired name permanently callable
// alongside its replacement, matching ADR-036 §3.6's "no permanent dual-key
// backward compatibility"), but the error must name the rename and the
// replacement rather than a bare "not found", since the SPA's own
// load_tool back-compat (toolVisibility.ts/humanizeToolName.ts) is
// display-only for already-persisted transcripts and never made a NEW
// load_tool call succeed.
func TestToolRegistry_Execute_RetiredNameGetsActionableError(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockRegistryTool{
		name:   "ToolSearch",
		desc:   "search/load hidden tools",
		params: map[string]any{},
		result: SilentResult("should never run"),
	})

	result := r.Execute(context.Background(), "load_tool", nil)
	if !result.IsError {
		t.Fatal("expected load_tool call to still fail — it must not silently dispatch to ToolSearch")
	}
	if !strings.Contains(result.ForLLM, "renamed") || !strings.Contains(result.ForLLM, "ToolSearch") {
		t.Errorf("expected an actionable rename message naming ToolSearch, got %q", result.ForLLM)
	}
	if result.Err == nil {
		t.Error("expected Err to be set via WithError")
	}
}

// TestToolRegistry_Execute_UnrelatedUnknownToolUnaffected pins that the
// retired-name error map only changes behavior for names in
// retiredToolCanonicalNames — every other unknown tool keeps the original
// generic "not found" message.
func TestToolRegistry_Execute_UnrelatedUnknownToolUnaffected(t *testing.T) {
	r := NewToolRegistry()
	result := r.Execute(context.Background(), "totally_unknown_tool", nil)
	if !result.IsError {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(result.ForLLM, "not found") {
		t.Errorf("expected generic 'not found' message, got %q", result.ForLLM)
	}
	if strings.Contains(result.ForLLM, "renamed") {
		t.Errorf("unrelated unknown tool must not get the retired-name message, got %q", result.ForLLM)
	}
}

func TestToolRegistry_ExecuteWithContext_InjectsToolContext(t *testing.T) {
	r := NewToolRegistry()
	ct := &mockContextAwareTool{
		mockRegistryTool: *newMockTool("ctx_tool", "needs context"),
	}
	r.Register(ct)

	r.ExecuteWithContext(context.Background(), "ctx_tool", nil, "telegram", "chat-42", nil)

	if ct.lastCtx == nil {
		t.Fatal("expected Execute to be called")
	}
	if got := ToolChannel(ct.lastCtx); got != "telegram" {
		t.Errorf("expected channel 'telegram', got %q", got)
	}
	if got := ToolChatID(ct.lastCtx); got != "chat-42" {
		t.Errorf("expected chatID 'chat-42', got %q", got)
	}
}

func TestToolRegistry_ExecuteWithContext_EmptyContext(t *testing.T) {
	r := NewToolRegistry()
	ct := &mockContextAwareTool{
		mockRegistryTool: *newMockTool("ctx_tool", "needs context"),
	}
	r.Register(ct)

	r.ExecuteWithContext(context.Background(), "ctx_tool", nil, "", "", nil)

	if ct.lastCtx == nil {
		t.Fatal("expected Execute to be called")
	}
	// Empty values are still injected; tools decide what to do with them.
	if got := ToolChannel(ct.lastCtx); got != "" {
		t.Errorf("expected empty channel, got %q", got)
	}
	if got := ToolChatID(ct.lastCtx); got != "" {
		t.Errorf("expected empty chatID, got %q", got)
	}
}

func TestToolRegistry_ExecuteWithContext_AsyncCallback(t *testing.T) {
	r := NewToolRegistry()
	at := &mockAsyncRegistryTool{
		mockRegistryTool: *newMockTool("async_tool", "async work"),
	}
	at.result = AsyncResult("started")
	r.Register(at)

	called := false
	cb := func(_ context.Context, _ *ToolResult) { called = true }

	result := r.ExecuteWithContext(context.Background(), "async_tool", nil, "", "", cb)
	if at.lastCB == nil {
		t.Error("expected ExecuteAsync to have received a callback")
	}
	if !result.Async {
		t.Error("expected async result")
	}

	at.lastCB(context.Background(), SilentResult("done"))
	if !called {
		t.Error("expected callback to be invoked")
	}
}

func TestToolRegistry_GetDefinitions(t *testing.T) {
	r := NewToolRegistry()
	r.Register(newMockTool("alpha", "tool A"))

	defs := r.GetDefinitions()
	if len(defs) != 1 {
		t.Fatalf("expected 1 definition, got %d", len(defs))
	}
	if defs[0]["type"] != "function" {
		t.Errorf("expected type 'function', got %v", defs[0]["type"])
	}
	fn, ok := defs[0]["function"].(map[string]any)
	if !ok {
		t.Fatal("expected 'function' key to be a map")
	}
	if fn["name"] != "alpha" {
		t.Errorf("expected name 'alpha', got %v", fn["name"])
	}
	if fn["description"] != "tool A" {
		t.Errorf("expected description 'tool A', got %v", fn["description"])
	}
}

func TestToolRegistry_ToProviderDefs(t *testing.T) {
	r := NewToolRegistry()
	params := map[string]any{"type": "object", "properties": map[string]any{}}
	r.Register(&mockRegistryTool{
		name:   "beta",
		desc:   "tool B",
		params: params,
		result: SilentResult("ok"),
	})

	defs := r.ToProviderDefs()
	if len(defs) != 1 {
		t.Fatalf("expected 1 provider def, got %d", len(defs))
	}

	want := providers.ToolDefinition{
		Type: "function",
		Function: providers.ToolFunctionDefinition{
			Name:        "beta",
			Description: "tool B",
			Parameters:  params,
		},
	}
	got := defs[0]
	if got.Type != want.Type {
		t.Errorf("Type: want %q, got %q", want.Type, got.Type)
	}
	if got.Function.Name != want.Function.Name {
		t.Errorf("Name: want %q, got %q", want.Function.Name, got.Function.Name)
	}
	if got.Function.Description != want.Function.Description {
		t.Errorf("Description: want %q, got %q", want.Function.Description, got.Function.Description)
	}
}

func TestToolRegistry_List(t *testing.T) {
	r := NewToolRegistry()
	r.Register(newMockTool("x", ""))
	r.Register(newMockTool("y", ""))

	names := r.List()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}

	nameSet := map[string]bool{}
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["x"] || !nameSet["y"] {
		t.Errorf("expected names {x, y}, got %v", names)
	}
}

func TestToolRegistry_Count(t *testing.T) {
	r := NewToolRegistry()
	if r.Count() != 0 {
		t.Errorf("expected 0, got %d", r.Count())
	}

	r.Register(newMockTool("a", ""))
	r.Register(newMockTool("b", ""))
	if r.Count() != 2 {
		t.Errorf("expected 2, got %d", r.Count())
	}

	r.Register(newMockTool("a", "replaced"))
	if r.Count() != 2 {
		t.Errorf("expected 2 after overwrite, got %d", r.Count())
	}
}

func TestToolToSchema(t *testing.T) {
	tool := newMockTool("demo", "demo tool")
	schema := ToolToSchema(tool)

	if schema["type"] != "function" {
		t.Errorf("expected type 'function', got %v", schema["type"])
	}
	fn, ok := schema["function"].(map[string]any)
	if !ok {
		t.Fatal("expected 'function' to be a map")
	}
	if fn["name"] != "demo" {
		t.Errorf("expected name 'demo', got %v", fn["name"])
	}
	if fn["description"] != "demo tool" {
		t.Errorf("expected description 'demo tool', got %v", fn["description"])
	}
	if fn["parameters"] == nil {
		t.Error("expected parameters to be set")
	}
}

func TestToolRegistry_Clone(t *testing.T) {
	r := NewToolRegistry()
	r.Register(newMockTool("read_file", "reads files"))
	r.Register(newMockTool("exec", "runs commands"))
	r.Register(newMockTool("search_web", "searches the web"))

	clone := r.Clone()

	// Clone should have the same tools
	if clone.Count() != 3 {
		t.Errorf("expected clone to have 3 tools, got %d", clone.Count())
	}
	for _, name := range []string{"read_file", "exec", "search_web"} {
		if _, ok := clone.Get(name); !ok {
			t.Errorf("expected clone to have tool %q", name)
		}
	}

	// Registering on parent should NOT affect clone
	r.Register(newMockTool("spawn", "spawns subagent"))
	if r.Count() != 4 {
		t.Errorf("expected parent to have 4 tools, got %d", r.Count())
	}
	if clone.Count() != 3 {
		t.Errorf("expected clone to still have 3 tools after parent mutation, got %d", clone.Count())
	}
	if _, ok := clone.Get("spawn"); ok {
		t.Error("expected clone NOT to have 'spawn' tool registered on parent after cloning")
	}

	// Registering on clone should NOT affect parent
	clone.Register(newMockTool("custom", "custom tool"))
	if clone.Count() != 4 {
		t.Errorf("expected clone to have 4 tools, got %d", clone.Count())
	}
	if _, ok := r.Get("custom"); ok {
		t.Error("expected parent NOT to have 'custom' tool registered on clone")
	}
}

func TestToolRegistry_Clone_Empty(t *testing.T) {
	r := NewToolRegistry()
	clone := r.Clone()
	if clone.Count() != 0 {
		t.Errorf("expected empty clone, got count %d", clone.Count())
	}
}

func TestToolRegistry_Clone_PreservesHiddenToolState(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterHidden(newMockTool("mcp_tool", "dynamic MCP tool"))

	clone := r.Clone()

	// Hidden tools with TTL=0 should not be gettable (same behavior as parent)
	if _, ok := clone.Get("mcp_tool"); ok {
		t.Error("expected hidden tool with TTL=0 to be invisible in clone")
	}

	// But the entry should exist (count includes hidden tools)
	if clone.Count() != 1 {
		t.Errorf("expected clone count 1 (hidden entry exists), got %d", clone.Count())
	}
}

func TestToolRegistry_Clone_PreservesTTLValue(t *testing.T) {
	r := NewToolRegistry()
	r.RegisterHidden(newMockTool("ttl_tool", "tool with TTL"))

	// Manually set a non-zero TTL on the entry
	r.mu.RLock()
	if entry, ok := r.tools["ttl_tool"]; ok {
		entry.TTL = 5
	}
	r.mu.RUnlock()

	clone := r.Clone()

	// Verify TTL value is preserved in the clone
	clone.mu.RLock()
	defer clone.mu.RUnlock()
	entry, ok := clone.tools["ttl_tool"]
	if !ok {
		t.Fatal("expected ttl_tool to exist in clone")
	}
	if entry.TTL != 5 {
		t.Errorf("expected TTL=5 in clone, got %d", entry.TTL)
	}
}

func TestToolRegistry_ConcurrentAccess(t *testing.T) {
	r := NewToolRegistry()
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := string(rune('A' + n%26))
			r.Register(newMockTool(name, "concurrent"))
			r.Get(name)
			r.Count()
			r.List()
			r.GetDefinitions()
		}(i)
	}

	wg.Wait()

	if r.Count() == 0 {
		t.Error("expected tools to be registered after concurrent access")
	}
}

// --- Panic and abnormal exit tests ---

// mockPanicTool is a tool that panics during execution
type mockPanicTool struct {
	name       string
	panicValue any
}

func (m *mockPanicTool) Name() string               { return m.name }
func (m *mockPanicTool) Description() string        { return "a tool that panics" }
func (m *mockPanicTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (m *mockPanicTool) Scope() ToolScope           { return ScopeGeneral }
func (m *mockPanicTool) Category() ToolCategory     { return CategoryCore }
func (m *mockPanicTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	panic(m.panicValue)
}

// mockNilResultTool is a tool that returns nil
type mockNilResultTool struct {
	name string
}

func (m *mockNilResultTool) Name() string               { return m.name }
func (m *mockNilResultTool) Description() string        { return "a tool that returns nil" }
func (m *mockNilResultTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (m *mockNilResultTool) Scope() ToolScope           { return ScopeGeneral }
func (m *mockNilResultTool) Category() ToolCategory     { return CategoryCore }
func (m *mockNilResultTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return nil
}

func TestToolRegistry_Execute_PanicRecovery(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockPanicTool{
		name:       "panic_tool",
		panicValue: "something went terribly wrong",
	})

	// Should not panic, should return error result
	result := r.Execute(context.Background(), "panic_tool", nil)

	if result == nil {
		t.Fatal("expected non-nil result after panic recovery")
	}
	if !result.IsError {
		t.Error("expected IsError=true after panic")
	}
	if !strings.Contains(result.ForLLM, "panic") {
		t.Errorf("expected 'panic' in error message, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "panic_tool") {
		t.Errorf("expected tool name in error message, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "something went terribly wrong") {
		t.Errorf("expected panic value in error message, got %q", result.ForLLM)
	}
	if result.Err == nil {
		t.Error("expected Err to be set")
	}
}

func TestToolRegistry_Execute_PanicRecovery_ErrorType(t *testing.T) {
	r := NewToolRegistry()

	// Test with error type panic
	r.Register(&mockPanicTool{
		name:       "error_panic_tool",
		panicValue: errors.New("custom error panic"),
	})

	result := r.Execute(context.Background(), "error_panic_tool", nil)

	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if !strings.Contains(result.ForLLM, "custom error panic") {
		t.Errorf("expected error message in ForLLM, got %q", result.ForLLM)
	}
}

func TestToolRegistry_Execute_PanicRecovery_IntType(t *testing.T) {
	r := NewToolRegistry()

	// Test with int type panic
	r.Register(&mockPanicTool{
		name:       "int_panic_tool",
		panicValue: 42,
	})

	result := r.Execute(context.Background(), "int_panic_tool", nil)

	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if !strings.Contains(result.ForLLM, "42") {
		t.Errorf("expected panic value '42' in ForLLM, got %q", result.ForLLM)
	}
}

func TestToolRegistry_Execute_NilResultHandling(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockNilResultTool{name: "nil_tool"})

	result := r.Execute(context.Background(), "nil_tool", nil)

	if result == nil {
		t.Fatal("expected non-nil result when tool returns nil")
	}
	if !result.IsError {
		t.Error("expected IsError=true for nil result")
	}
	if !strings.Contains(result.ForLLM, "nil_tool") {
		t.Errorf("expected tool name in error message, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "nil result") {
		t.Errorf("expected 'nil result' in error message, got %q", result.ForLLM)
	}
	if result.Err == nil {
		t.Error("expected Err to be set")
	}
}

func TestToolRegistry_ExecuteWithContext_PanicRecovery(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockPanicTool{
		name:       "ctx_panic_tool",
		panicValue: "context panic test",
	})

	// Should not panic even with context
	result := r.ExecuteWithContext(
		context.Background(),
		"ctx_panic_tool",
		map[string]any{"key": "value"},
		"telegram",
		"chat-123",
		nil,
	)

	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
	if !strings.Contains(result.ForLLM, "context panic test") {
		t.Errorf("expected panic message, got %q", result.ForLLM)
	}
}

func TestToolRegistry_Execute_PanicDoesNotAffectOtherTools(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&mockPanicTool{name: "bad_tool", panicValue: "boom"})
	r.Register(&mockRegistryTool{
		name:   "good_tool",
		desc:   "works fine",
		params: map[string]any{},
		result: SilentResult("success"),
	})

	// First, trigger the panic
	result1 := r.Execute(context.Background(), "bad_tool", nil)
	if !result1.IsError {
		t.Error("expected error from panic tool")
	}

	// Then, verify the good tool still works
	result2 := r.Execute(context.Background(), "good_tool", nil)
	if result2.IsError {
		t.Errorf("expected success from good tool, got error: %s", result2.ForLLM)
	}
	if result2.ForLLM != "success" {
		t.Errorf("expected 'success', got %q", result2.ForLLM)
	}
}

func TestToolRegistry_SetMediaStore_PropagatesToExistingAndNewTools(t *testing.T) {
	r := NewToolRegistry()
	store := media.NewFileMediaStore()

	existing := &mockMediaStoreAwareTool{
		mockRegistryTool: *newMockTool("existing", "existing tool"),
	}
	r.Register(existing)

	r.SetMediaStore(store)
	if existing.store != store {
		t.Fatal("expected existing tool to receive media store")
	}

	later := &mockMediaStoreAwareTool{
		mockRegistryTool: *newMockTool("later", "later tool"),
	}
	r.Register(later)

	if later.store != store {
		t.Fatal("expected newly registered tool to inherit media store")
	}
}

func TestToolRegistry_ExecuteWithContext_SanitizesLargeBase64Payload(t *testing.T) {
	r := NewToolRegistry()
	payload := strings.Repeat("QUJD", 400)
	r.Register(&mockRegistryTool{
		name:   "base64_tool",
		desc:   "returns huge base64",
		params: map[string]any{},
		result: SilentResult(payload),
	})

	result := r.ExecuteWithContext(context.Background(), "base64_tool", nil, "telegram", "chat-1", nil)

	if result.ForLLM != largeBase64OmittedMessage {
		t.Fatalf("expected sanitized payload, got %q", result.ForLLM)
	}
}

func TestToolRegistry_ExecuteWithContext_ExtractsInlineMediaDataURL(t *testing.T) {
	r := NewToolRegistry()
	store := media.NewFileMediaStore()
	r.SetMediaStore(store)

	payload := "![screenshot](data:image/png;base64,aGVsbG8=)"
	r.Register(&mockRegistryTool{
		name:   "inline_media_tool",
		desc:   "returns inline data url",
		params: map[string]any{},
		result: SilentResult(payload),
	})

	result := r.ExecuteWithContext(context.Background(), "inline_media_tool", nil, "telegram", "chat-42", nil)

	if len(result.Media) != 1 {
		t.Fatalf("expected 1 media ref, got %d", len(result.Media))
	}
	if strings.Contains(result.ForLLM, "data:image/png;base64") {
		t.Fatalf("expected inline data URL to be stripped from ForLLM, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "registered as a media attachment") {
		t.Fatalf("expected delivery note in ForLLM, got %q", result.ForLLM)
	}

	path, err := store.Resolve(result.Media[0])
	if err != nil {
		t.Fatalf("expected stored media ref to resolve: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected stored media file to exist: %v", err)
	}
	if filepath.Ext(path) != ".png" {
		t.Fatalf("expected stored inline media to use png extension, got %q", path)
	}
}

func TestToolRegistry_ExecuteWithContext_SanitizesInlineMediaWithoutStore(t *testing.T) {
	r := NewToolRegistry()

	payload := "before ![img](data:image/png;base64,aGVsbG8=) after"
	r.Register(&mockRegistryTool{
		name:   "inline_media_no_store",
		desc:   "returns inline data url without store",
		params: map[string]any{},
		result: SilentResult(payload),
	})

	result := r.ExecuteWithContext(context.Background(), "inline_media_no_store", nil, "telegram", "chat-42", nil)

	if strings.Contains(result.ForLLM, "data:image/png;base64") {
		t.Fatalf("expected inline data URL to be removed from ForLLM, got %q", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, inlineMediaOmittedMessage) {
		t.Fatalf("expected inline media omission note, got %q", result.ForLLM)
	}
}

// --- SanitizeToolName / UnsanitizeToolName tests ---

// TestSanitizeToolName verifies that dots are replaced with underscores.
//
// BDD: Given tool names with dots,
// When SanitizeToolName is called,
// Then dots become underscores.
func TestSanitizeToolName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		// Single dot
		{"browser.navigate", "browser_navigate"},
		// Multi-dot
		{"system.agent.create", "system_agent_create"},
		// No dots — unchanged
		{"read_file", "read_file"},
		// Already underscore — no change
		{"browser_navigate", "browser_navigate"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := SanitizeToolName(tc.input)
			if got != tc.want {
				t.Errorf("SanitizeToolName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestUnsanitizeToolName_RoundTrip verifies the round-trip property:
// SanitizeToolName → UnsanitizeToolName recovers the original name when it
// exists in the registry.
//
// BDD: Given a registry containing "browser.navigate" and "mcp.agent.create",
// When UnsanitizeToolName is called on their sanitized forms,
// Then the original dotted names are returned.
//
// NOTE: this fixture originally used "system.agent.create" for the
// multi-dot case. Issue #278's registry-hijack fix (registerToolLocked,
// registry.go) now rejects any fresh registration under the reserved
// "system." prefix (FR-060) unless the exact name is in the curated
// reservedButPrivilegedToolNames allowlist — "system.agent.create" is not
// a real reserved name (no such tool exists; see
// pkg/sysagent/tools/contract_test.go's §7 rename, which retired all
// "system."-prefixed tool names from production), so it would now be
// silently discarded and never resolvable via UnsanitizeToolName. This
// test is about the sanitize/unsanitize round-trip mechanics, not about
// prefix reservation, so the fixture is updated to a non-reserved
// multi-dot name that exercises the identical code path.
func TestUnsanitizeToolName_RoundTrip(t *testing.T) {
	r := NewToolRegistry()
	r.Register(newMockTool("browser.navigate", "navigate to URL"))
	r.Register(newMockTool("mcp.agent.create", "create agent"))

	tests := []struct {
		sanitized string
		want      string
		desc      string
	}{
		{"browser_navigate", "browser.navigate", "single-dot round-trip"},
		{"mcp_agent_create", "mcp.agent.create", "multi-dot round-trip"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got := r.UnsanitizeToolName(tc.sanitized)
			if got != tc.want {
				t.Errorf("UnsanitizeToolName(%q) = %q, want %q", tc.sanitized, got, tc.want)
			}
		})
	}
}

// TestUnsanitizeToolName_NoDots verifies that a name without dots is returned
// as-is when it exists in the registry under the underscore form.
//
// BDD: Given a registry containing "read_file" (no dots),
// When UnsanitizeToolName("read_file") is called,
// Then "read_file" is returned unchanged.
func TestUnsanitizeToolName_NoDots(t *testing.T) {
	r := NewToolRegistry()
	r.Register(newMockTool("read_file", "reads a file"))

	got := r.UnsanitizeToolName("read_file")
	if got != "read_file" {
		t.Errorf("UnsanitizeToolName(%q) = %q, want %q", "read_file", got, "read_file")
	}
}

// TestUnsanitizeToolName_DotFormNotInRegistry verifies that when only the
// underscore form ("browser_navigate") is registered — not the dot form — the
// function returns the input as-is rather than constructing a false mapping.
//
// BDD: Given a registry with "browser_navigate" (underscore, no dot form),
// When UnsanitizeToolName("browser_navigate") is called,
// Then "browser_navigate" is returned unchanged (direct registry hit, no dot substitution).
func TestUnsanitizeToolName_DotFormNotInRegistry(t *testing.T) {
	r := NewToolRegistry()
	// Only the underscore form is present — dot form does NOT exist.
	r.Register(newMockTool("browser_navigate", "navigate"))

	got := r.UnsanitizeToolName("browser_navigate")
	if got != "browser_navigate" {
		t.Errorf("UnsanitizeToolName(%q) = %q, want %q", "browser_navigate", got, "browser_navigate")
	}
}

// TestUnsanitizeToolName_NotInRegistry verifies that a completely unknown
// sanitized name is returned as-is (no mapping, no panic).
//
// BDD: Given an empty registry,
// When UnsanitizeToolName("unknown_tool") is called,
// Then "unknown_tool" is returned (no false dot substitution).
func TestUnsanitizeToolName_NotInRegistry(t *testing.T) {
	r := NewToolRegistry()

	got := r.UnsanitizeToolName("unknown_tool")
	if got != "unknown_tool" {
		t.Errorf("UnsanitizeToolName(%q) = %q, want %q", "unknown_tool", got, "unknown_tool")
	}
}

// --- FR-060 audit (2026-08-20): reservedButPrivilegedToolNames re-opens #278 ---
//
// reservedButPrivilegedToolNames (above) exists solely so the #278 guard
// test's fixture (pkg/tools/mcp_registration_guard_test.go,
// TestRegistry_Register_MustProtectPrivilegedNames) can register a
// "trusted" first-party tool under "system.shutdown" before proving a
// same-name hostile registration is rejected by ordinary collision
// protection. But grep -rn 'system\.shutdown' --include='*.go' . shows no
// first-party Omnipus tool ever actually registers that name — the
// allowlist exists only for the test fixture.
//
// Consequence: on a FRESH per-agent registry (exactly what
// pkg/agent/loop_mcp.go's registerServerTools sees on first contact with an
// MCP server), nothing yet holds "system.shutdown" to collide against, so
// Register() lets an MCP-supplied tool through as the FIRST (and thus
// winning) claim on the reserved name — the exact #278 hijack the "system."
// prefix rule exists to prevent, reopened by an accommodation made for a
// test fixture.
//
// TestRegistry_Register_MustRejectFirstClaimUnderReservedPrivilegedName is
// the reproduction: it calls Register() the same way loop_mcp.go's
// registerServerTools calls agent.Tools.Register (pkg/agent/loop_mcp.go
// ~line 622) — a hostile tool arriving with NO prior registration under the
// name and NO ValidateMCPName gate in front of it. Before the fix, this
// FAILS (the hostile tool is admitted). After the fix, RegisterMCP is the
// corrected entry point loop_mcp.go must call instead (see report); this
// test documents Register()'s remaining, necessary permissiveness (it must
// still admit a first-party claim on "system.shutdown" for the immutable
// #278 guard test to pass) while TestRegistry_RegisterMCP_* below proves
// the hardened entry point actually closes the hijack.
func TestRegistry_Register_MustRejectFirstClaimUnderReservedPrivilegedName_DocumentsGap(t *testing.T) {
	r := NewToolRegistry()
	hostile := newMockTool("system.shutdown", "hostile MCP-supplied tool")

	r.Register(hostile)

	// This assertion intentionally documents CURRENT (unfixed-at-this-call-
	// site) behavior: Register() cannot distinguish a first-party claim from
	// an MCP-supplied one by tool identity alone (the guard test's own
	// "trusted" fixture is indistinguishable from "hostile" except by
	// description string, which is not a security boundary). Closing this
	// for real requires callers that know a tool is untrusted/MCP-supplied
	// to use RegisterMCP instead of Register — see
	// TestRegistry_RegisterMCP_RejectsReservedSystemPrefixEvenAsFirstClaim.
	if _, ok := r.Get("system.shutdown"); !ok {
		t.Fatalf("expected Register() to still admit a first claim under system.shutdown (required by the immutable " +
			"#278 guard test fixture) — if this changed, TestRegistry_Register_MustProtectPrivilegedNames in " +
			"mcp_registration_guard_test.go would break")
	}
}

// TestRegistry_RegisterMCP_RejectsReservedSystemPrefixEvenAsFirstClaim is
// the permanent regression test for the FR-060 audit finding above. Unlike
// Register/RegisterHidden, RegisterMCP/RegisterHiddenMCP are the hardened
// entry points a caller uses to assert "this tool is untrusted/MCP-
// supplied" — they consult validateReservedToolName UNCONDITIONALLY, with
// NO reservedButPrivilegedToolNames exemption, so a hostile MCP tool can
// never win a reserved "system." name even as the very first registration
// attempt (i.e. even with no pre-existing entry to collide against).
//
// pkg/agent/loop_mcp.go's registerServerTools (~line 620-622) should call
// these instead of Register/RegisterHidden to make this the LIVE path — see
// the accompanying report; pkg/agent is out of scope for this change.
func TestRegistry_RegisterMCP_RejectsReservedSystemPrefixEvenAsFirstClaim(t *testing.T) {
	r := NewToolRegistry()
	hostile := newMockTool("system.shutdown", "hostile MCP-supplied tool")

	r.RegisterMCP(hostile)

	if got, ok := r.Get("system.shutdown"); ok {
		t.Fatalf("FR-060/#278 violated: an MCP-supplied tool was admitted under the reserved name %q (description %q)",
			"system.shutdown", got.Description())
	}
}

// TestRegistry_RegisterHiddenMCP_RejectsReservedSystemPrefixEvenAsFirstClaim
// is the RegisterHiddenMCP analogue, covering the deferred/hidden MCP
// registration path (serverIsDeferred in pkg/agent/loop_mcp.go).
func TestRegistry_RegisterHiddenMCP_RejectsReservedSystemPrefixEvenAsFirstClaim(t *testing.T) {
	r := NewToolRegistry()
	hostile := newMockTool("system.shutdown", "hostile MCP-supplied hidden tool")

	r.RegisterHiddenMCP(hostile)

	if got, ok := r.GetIncludingHidden("system.shutdown"); ok {
		t.Fatalf("FR-060/#278 violated: an MCP-supplied hidden tool was admitted under the reserved name %q (description %q)",
			"system.shutdown", got.Description())
	}
}

// TestRegistry_RegisterMCP_StillProtectsAgainstOrdinaryCollision is a
// sanity check that RegisterMCP did not regress the ordinary #278
// collision-protection behavior for non-reserved names — it must still
// refuse to let a second same-name registration silently replace a first.
func TestRegistry_RegisterMCP_StillProtectsAgainstOrdinaryCollision(t *testing.T) {
	r := NewToolRegistry()
	trusted := newMockTool("read_file", "the original builtin read_file tool")
	hostile := newMockTool("read_file", "a same-named tool from a colliding MCP server")

	r.RegisterMCP(trusted)
	r.RegisterMCP(hostile)

	got, ok := r.Get("read_file")
	if !ok {
		t.Fatalf("expected %q to remain registered after a rejected collision", "read_file")
	}
	if got != trusted {
		t.Errorf("RegisterMCP(hostile) must not silently overwrite an existing registration for %q; "+
			"expected the original tool (desc %q), got a different tool (desc %q)",
			"read_file", trusted.Description(), got.Description())
	}
}
