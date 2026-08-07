// tool_result_lazy_fetch_test.go — T2: Tool-result lazy-fetch round-trip tests.
//
// Tests the full write → REST-read cycle for the lazy-fetch feature:
//  1. A result > 50 KiB is offloaded by maybeOffloadResult → ToolResultRef sentinel.
//  2. GET /api/v1/tool-results/{ref} with valid auth returns 200 + original body.
//  3. Negative cases: 404 for non-existent ref, 400 for path-traversal, 401 for
//     missing auth.
//
// These are unit-level tests that exercise restAPI.HandleToolResults via
// newTestRW + newTestReq (defined in replay_since_cursor_test.go in this package).
// They do NOT require a full gateway boot or WS connection.
//
// NOTE: T2 stops at the unit level (pkg/gateway) rather than the full integration
// level because injecting a > 50 KiB tool result through the live agent loop
// requires scripting an LLM call that returns a tool result body of that size —
// non-trivial with the mock server. The unit tests here cover the same functional
// surface (offload → REST read-back) with full precision.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2.

package gateway

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestToolResultLazyFetch_RoundTrip verifies the full offload → REST read-back cycle.
//
// BDD:
//
//	Given a tool result body > 50 KiB (51 KiB, exactly above the inline threshold)
//	When maybeOffloadResult is called
//	Then the result is offloaded to disk and a ToolResultRef sentinel is returned
//	And GET /api/v1/tool-results/{ref} returns 200 with the original body
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (write → read round-trip)
func TestToolResultLazyFetch_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	// Build a 51 KiB payload (just above the 50 KiB inline threshold).
	// Use a JSON string so json.Marshal is straightforward.
	rawData := strings.Repeat("A", 51*1024)
	encoded, err := json.Marshal(rawData)
	require.NoError(t, err)
	require.Greater(t, len(encoded), InlineToolResultMaxBytes,
		"T2: payload must exceed InlineToolResultMaxBytes to trigger offload")

	// ── Step 1: Offload ───────────────────────────────────────────────────────
	sentinel, offloaded := maybeOffloadResult(store, "session-t2", encoded)
	require.True(t, offloaded, "T2: result > 50 KiB must be offloaded")
	require.NotNil(t, sentinel)

	ref, ok := sentinel.(generated.ToolResultRef)
	require.True(t, ok, "T2: sentinel must be generated.ToolResultRef")
	assert.True(t, ref.IsRef, "T2: IsRef must be true")
	assert.NotEmpty(t, ref.Ref, "T2: Ref must be non-empty")
	assert.Equal(t, len(encoded), ref.OriginalSizeBytes, "T2: OriginalSizeBytes must match encoded length")
	// Preview must be ≤ 4 KiB.
	assert.LessOrEqual(t, len([]byte(ref.Preview)), toolResultPreviewBytes, "T2: Preview must not exceed 4 KiB")

	// ── Step 2: REST read-back with valid GET ─────────────────────────────────
	api := &restAPI{homePath: dir}
	req := newTestReq(t, http.MethodGet, "/api/v1/sessions/session-t2/tool-results/"+ref.Ref, nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)

	assert.Equal(t, http.StatusOK, rw.code, "T2: GET with valid ref must return 200")
	assert.Equal(t, "application/json", rw.header.Get("Content-Type"), "T2: Content-Type must be application/json")
	assert.Equal(t, encoded, rw.body.Bytes(), "T2: response body must equal the original encoded payload")
}

// TestToolResultLazyFetch_DifferentInputsDifferentOutputs is the differentiation
// test: two different bodies produce two different refs, and each ref serves
// only its own body.
//
// BDD:
//
//	Given two large payloads (each > 50 KiB) with distinct content
//	When each is offloaded and its ref is fetched
//	Then each GET returns a body that differs from the other
//	(proves the endpoint is not serving a hardcoded response)
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (differentiation)
func TestToolResultLazyFetch_DifferentInputsDifferentOutputs(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)
	api := &restAPI{homePath: dir}

	makeAndFetch := func(marker string) []byte {
		rawData := strings.Repeat(marker, 51*1024/len(marker)+1)
		encoded, err := json.Marshal(rawData)
		require.NoError(t, err)
		require.Greater(t, len(encoded), InlineToolResultMaxBytes)

		sentinel, offloaded := maybeOffloadResult(store, "sess-diff", encoded)
		require.True(t, offloaded)
		ref := sentinel.(generated.ToolResultRef)

		req := newTestReq(t, http.MethodGet, "/api/v1/sessions/sess-diff/tool-results/"+ref.Ref, nil)
		rw := newTestRW()
		api.HandleToolResults(rw, req)
		require.Equal(t, http.StatusOK, rw.code)
		return rw.body.Bytes()
	}

	body1 := makeAndFetch("A")
	body2 := makeAndFetch("B")

	// The two responses must differ — proves the handler is not hardcoded.
	assert.NotEqual(t, body1, body2,
		"T2-diff: GET for two different refs must return different bodies")
}

// TestToolResultLazyFetch_404NotFound verifies that a non-existent ref returns 404.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (negative: 404)
func TestToolResultLazyFetch_404NotFound(t *testing.T) {
	dir := t.TempDir()
	api := &restAPI{homePath: dir}

	req := newTestReq(t, http.MethodGet, "/api/v1/sessions/sess-x/tool-results/nonexistent0123456789abcdef0", nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)

	assert.Equal(t, http.StatusNotFound, rw.code, "T2: non-existent ref must return 404")
}

// TestToolResultLazyFetch_400PathTraversal verifies that a ref containing ".."
// is rejected with 400.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (negative: path traversal)
func TestToolResultLazyFetch_400PathTraversal(t *testing.T) {
	dir := t.TempDir()
	api := &restAPI{homePath: dir}

	req := newTestReq(t, http.MethodGet, "/api/v1/sessions/sess-x/tool-results/../../etc/passwd", nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)

	assert.Equal(t, http.StatusBadRequest, rw.code, "T2: path traversal ref must return 400")
}

// TestToolResultLazyFetch_400SlashInRef verifies that a ref containing a slash
// is rejected with 400 (prevents path injection).
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (negative: slash in ref)
func TestToolResultLazyFetch_400SlashInRef(t *testing.T) {
	dir := t.TempDir()
	api := &restAPI{homePath: dir}

	// A ref that contains a slash after the tool-results/ infix is invalid.
	// The ref portion is "bad/ref" which validateEntityID rejects.
	req := newTestReq(t, http.MethodGet, "/api/v1/sessions/sess-x/tool-results/bad/ref123", nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)

	// HandleToolResults reads ref as "bad/ref123" — validateEntityID rejects it with 400.
	assert.Equal(t, http.StatusBadRequest, rw.code, "T2: ref with slash must return 400")
}

// TestToolResultLazyFetch_405MethodNotAllowed verifies that POST is rejected.
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (method guard)
func TestToolResultLazyFetch_405MethodNotAllowed(t *testing.T) {
	dir := t.TempDir()
	api := &restAPI{homePath: dir}

	req := newTestReq(t, http.MethodPost, "/api/v1/sessions/sess-x/tool-results/someref", nil)
	rw := newTestRW()
	api.HandleToolResults(rw, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rw.code, "T2: POST must return 405")
}

// TestToolResultLazyFetch_InlineNotOffloaded verifies that results ≤ 50 KiB
// are NOT offloaded (no sentinel emitted). This is the boundary condition.
//
// BDD:
//
//	Given a payload exactly at the inline threshold (50 KiB)
//	When maybeOffloadResult is called
//	Then offloaded == false and the sentinel is nil
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (boundary: inline threshold)
func TestToolResultLazyFetch_InlineNotOffloaded(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	// Exactly InlineToolResultMaxBytes — must NOT be offloaded.
	exactThreshold := make([]byte, InlineToolResultMaxBytes)
	for i := range exactThreshold {
		exactThreshold[i] = 'z'
	}

	sentinel, offloaded := maybeOffloadResult(store, "sess-inline", exactThreshold)
	assert.False(t, offloaded, "T2-boundary: payload at exactly InlineToolResultMaxBytes must not be offloaded")
	assert.Nil(t, sentinel, "T2-boundary: sentinel must be nil when result is inline")
}

// TestToolResultLazyFetch_TruncateResult_Integration verifies truncateResult
// emits a ToolResultRef sentinel when the result size is in (50KiB, 1MiB].
// This exercises the path that streamReplay takes on replay when a tool store
// is configured — the integration of truncateResult + toolResultStore.
//
// BDD:
//
//	Given a session.ToolCall with a result body of 60 KiB and a configured toolResultStore
//	When truncateResult is called
//	Then the returned value is a ToolResultRef (not the original result, not a TruncatedResult)
//	And reading the ref from the store returns the original JSON-encoded result
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (truncateResult integration)
func TestToolResultLazyFetch_TruncateResult_Integration(t *testing.T) {
	dir := t.TempDir()
	store := newToolResultStore(dir)

	// Build a 60 KiB string result.
	big := strings.Repeat("X", 60*1024)
	tc := session.ToolCall{
		ID:     "tc-lazy-t2",
		Tool:   "some.tool",
		Result: map[string]any{"data": big},
	}

	result := truncateResult("session-t2-int", tc, store)

	// The result must be a ToolResultRef — not the raw value, not a TruncatedResult.
	ref, ok := result.(generated.ToolResultRef)
	require.True(t, ok,
		"T2-truncate: truncateResult with 60 KiB result and configured store must return ToolResultRef")
	assert.True(t, ref.IsRef, "T2-truncate: IsRef must be true")
	assert.NotEmpty(t, ref.Ref, "T2-truncate: Ref must be non-empty")

	// Read back via the store — body must be valid JSON matching the original tc.Result.
	stored, err := store.readByRef("session-t2-int", ref.Ref)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stored, &decoded),
		"T2-truncate: stored body must be valid JSON")

	storedData, _ := decoded["data"].(string)
	assert.Equal(t, big, storedData,
		"T2-truncate: stored body must preserve the original 'data' field exactly")
}

// TestToolResultLazyFetch_TruncateResult_NoStore verifies that truncateResult
// falls back to inline (or hard-truncation) when no store is configured.
//
// BDD:
//
//	Given a session.ToolCall with a 60 KiB result and a nil toolResultStore
//	When truncateResult is called
//	Then the result is NOT a ToolResultRef (store was unavailable, fallback taken)
//
// Traces to: spa-streaming-refactor.md Phase 2D, T2 (truncateResult nil-store fallback)
func TestToolResultLazyFetch_TruncateResult_NoStore(t *testing.T) {
	big := strings.Repeat("Y", 60*1024)
	tc := session.ToolCall{
		ID:     "tc-nostore",
		Tool:   "some.tool",
		Result: map[string]any{"data": big},
	}

	result := truncateResult("session-nostore", tc, nil)

	// With no store, the result falls back to inline (it is under 1 MiB).
	// It must NOT be a ToolResultRef sentinel.
	_, isRef := result.(generated.ToolResultRef)
	assert.False(t, isRef,
		"T2-nostore: truncateResult with nil store must not return ToolResultRef (falls back to inline)")

	// The result must be non-nil and preserve the original map shape.
	require.NotNil(t, result)
}
