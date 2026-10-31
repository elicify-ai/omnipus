package tools

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r6OwnCachedTool is a process-edge tool that returns one reused result object.
// The registry under test must not mark or alias that object.
type r6OwnCachedTool struct {
	BaseTool
	name    string
	panicOn bool
	cache   *ToolResult
	entered atomic.Int64
}

func (t *r6OwnCachedTool) Name() string { return t.name }
func (t *r6OwnCachedTool) Description() string {
	return "reused result fixture for registry invocation ownership"
}
func (t *r6OwnCachedTool) Scope() ToolScope { return ScopeGeneral }
func (t *r6OwnCachedTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"payload": map[string]any{"type": "string"},
		},
		"required": []string{"payload"},
	}
}
func (t *r6OwnCachedTool) Execute(context.Context, map[string]any) *ToolResult {
	t.entered.Add(1)
	if t.panicOn {
		panic("r6 ownership fixture panicked after entry")
	}
	return t.cache
}
func (t *r6OwnCachedTool) ExecuteAsync(context.Context, map[string]any, AsyncCallback) *ToolResult {
	t.entered.Add(1)
	return t.cache
}

func r6OwnTool(name string, cache *ToolResult) *r6OwnCachedTool {
	return &r6OwnCachedTool{name: name, cache: cache}
}

func r6OwnRegister(tool *r6OwnCachedTool) *ToolRegistry {
	registry := NewToolRegistry()
	registry.Register(tool)
	return registry
}

func r6OwnPayload() map[string]any {
	return map[string]any{"payload": "real reused-result fixture input"}
}

// r6OwnAssertSplit checks one returned call result against the tool-owned cache.
// Nested Deferred or Media pointers may still be shared; that is not ownership.
func r6OwnAssertSplit(t *testing.T, got, cache *ToolResult, wantLLM string, wantInvoked bool) {
	t.Helper()
	require.NotNil(t, got)
	require.NotSame(t, cache, got, "registry must not return the tool-owned object")
	assert.Equal(t, wantInvoked, got.WasInvoked(), "invocation fact belongs on the owned call result")
	assert.False(t, cache.WasInvoked(), "tool-owned cache must stay free of the live invocation fact")
	assert.Equal(t, wantLLM, got.ForLLM)
	assert.Equal(t, wantLLM, cache.ForLLM)
}

func r6OwnAssertNoWireFact(t *testing.T, got *ToolResult) {
	t.Helper()
	raw, err := json.Marshal(got)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &fields))
	assert.NotContains(t, fields, "invoked", "execution fact is live only, never a persisted or wire field")
}

// TestRegistryR6ResultOwnership is the reused-result discriminator for registry
// invocation ownership. WRITTEN / NOT EXECUTED in the authoring dispatch.
func TestRegistryR6ResultOwnership(t *testing.T) {
	t.Run("ReusedSuccessIsOwnedPerCall", func(t *testing.T) {
		const body = "r6 ownership fixture executed"
		cache := &ToolResult{ForLLM: body, ForUser: "r6 ownership user text", Media: []string{"r6-owned-media-label"}}
		tool := r6OwnTool("r6_result_ownership_success", cache)
		registry := r6OwnRegister(tool)

		first := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", nil)
		second := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", nil)

		require.Equal(t, int64(2), tool.entered.Load(), "both calls must enter the real tool body")
		r6OwnAssertSplit(t, first, cache, body, true)
		r6OwnAssertSplit(t, second, cache, body, true)
		require.NotSame(t, first, second, "two calls must not share one owned result")
		assert.Equal(t, []string{"r6-owned-media-label"}, first.Media)
		assert.Equal(t, "r6 ownership user text", first.ForUser)
		first.ForLLM = "caller rewrote only the first owned result"
		assert.Equal(t, body, cache.ForLLM, "rewriting an owned result must not rewrite the tool cache")
		assert.Equal(t, body, second.ForLLM, "rewriting one owned result must not rewrite the other call")
		assert.True(t, first.WasInvoked())
		assert.True(t, second.WasInvoked())
		assert.False(t, cache.WasInvoked())
		r6OwnAssertNoWireFact(t, first)
	})

	t.Run("ReusedErrorIsOwnedPerCall", func(t *testing.T) {
		const body = "r6 ownership fixture failed after a real call"
		cache := ErrorResult(body)
		tool := r6OwnTool("r6_result_ownership_error", cache)
		registry := r6OwnRegister(tool)

		first := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", nil)
		second := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", nil)

		require.Equal(t, int64(2), tool.entered.Load())
		for _, got := range []*ToolResult{first, second} {
			r6OwnAssertSplit(t, got, cache, body, true)
			assert.True(t, got.IsError)
		}
		require.NotSame(t, first, second)
		assert.True(t, cache.IsError, "ownership must not clear the tool's own error mark")
		assert.False(t, cache.WasInvoked())
	})

	t.Run("ReusedDeferredStaysDistinctFromInvocation", func(t *testing.T) {
		const body = "r6 ownership fixture deferred"
		cache := &ToolResult{
			ForLLM:   body,
			Deferred: &ToolDeferral{Gate: "browser_control", Reason: "fixture gate held"},
		}
		tool := r6OwnTool("r6_result_ownership_deferred", cache)
		registry := r6OwnRegister(tool)

		got := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", nil)

		require.Equal(t, int64(1), tool.entered.Load(), "deferral still enters Execute")
		r6OwnAssertSplit(t, got, cache, body, true)
		require.NotNil(t, got.Deferred, "structural deferral survives onto the owned result")
		assert.Equal(t, "browser_control", got.Deferred.Gate)
		require.NotNil(t, cache.Deferred, "ownership must not strip the tool-owned deferral")
		assert.Equal(t, "browser_control", cache.Deferred.Gate)
		assert.False(t, cache.WasInvoked(), "entered-but-deferred must not be stamped onto the cache")
	})

	t.Run("ValidationMissDoesNotTouchCachedResult", func(t *testing.T) {
		const body = "r6 ownership fixture must not run"
		cache := &ToolResult{ForLLM: body}
		tool := r6OwnTool("r6_result_ownership_validation", cache)
		registry := r6OwnRegister(tool)

		got := registry.ExecuteWithContext(context.Background(), tool.Name(), map[string]any{}, "", "", nil)

		require.NotNil(t, got)
		require.NotSame(t, cache, got)
		assert.True(t, got.IsError)
		assert.Contains(t, got.ForLLM, "invalid arguments")
		assert.Contains(t, got.ForLLM, "missing required property \"payload\"")
		assert.False(t, got.WasInvoked(), "argument validation is not tool invocation")
		assert.Equal(t, int64(0), tool.entered.Load(), "validation must return before the tool body")
		assert.False(t, cache.WasInvoked())
		assert.Equal(t, body, cache.ForLLM)
	})

	t.Run("LookupMissIsNotAnInvocation", func(t *testing.T) {
		cache := &ToolResult{ForLLM: "r6 ownership fixture not registered"}
		tool := r6OwnTool("r6_result_ownership_unregistered", cache)
		registry := NewToolRegistry()

		got := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", nil)

		require.NotNil(t, got)
		require.NotSame(t, cache, got)
		assert.True(t, got.IsError)
		assert.Contains(t, got.ForLLM, "not found")
		assert.False(t, got.WasInvoked(), "a missing tool is not an invocation")
		assert.Equal(t, int64(0), tool.entered.Load())
		assert.False(t, cache.WasInvoked())
	})

	t.Run("PanicResultIsNotTheCachedObject", func(t *testing.T) {
		const body = "r6 ownership fixture cache was not returned"
		cache := &ToolResult{ForLLM: body}
		tool := r6OwnTool("r6_result_ownership_panic", cache)
		tool.panicOn = true
		registry := r6OwnRegister(tool)

		got := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", nil)

		require.Equal(t, int64(1), tool.entered.Load(), "panic happens after the tool body is entered")
		require.NotNil(t, got)
		require.NotSame(t, cache, got, "panic recovery must not publish the tool cache")
		assert.True(t, got.IsError)
		assert.Contains(t, got.ForLLM, "crashed with panic")
		assert.True(t, got.WasInvoked(), "a panic after entry is still an invocation of that call")
		assert.False(t, cache.WasInvoked(), "the unreturned cache stays unmarked")
		assert.Equal(t, body, cache.ForLLM)
	})

	t.Run("ReusedAsyncResultIsOwnedPerCall", func(t *testing.T) {
		const body = "r6 ownership fixture async"
		cache := &ToolResult{ForLLM: body, Async: true}
		tool := r6OwnTool("r6_result_ownership_async", cache)
		registry := r6OwnRegister(tool)
		callback := func(context.Context, *ToolResult) {}

		first := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", callback)
		second := registry.ExecuteWithContext(context.Background(), tool.Name(), r6OwnPayload(), "", "", callback)

		require.Equal(t, int64(2), tool.entered.Load(), "a callback must take the async entry, twice")
		r6OwnAssertSplit(t, first, cache, body, true)
		r6OwnAssertSplit(t, second, cache, body, true)
		require.NotSame(t, first, second)
		assert.True(t, first.Async)
		assert.True(t, cache.Async)
		assert.False(t, cache.WasInvoked())
	})
}
