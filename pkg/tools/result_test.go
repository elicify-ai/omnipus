package tools

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNewToolResult(t *testing.T) {
	result := NewToolResult("test content")

	if result.ForLLM != "test content" {
		t.Errorf("Expected ForLLM 'test content', got '%s'", result.ForLLM)
	}
	if result.Silent {
		t.Error("Expected Silent to be false")
	}
	if result.IsError {
		t.Error("Expected IsError to be false")
	}
	if result.Async {
		t.Error("Expected Async to be false")
	}
}

func TestSilentResult(t *testing.T) {
	result := SilentResult("silent operation")

	if result.ForLLM != "silent operation" {
		t.Errorf("Expected ForLLM 'silent operation', got '%s'", result.ForLLM)
	}
	if !result.Silent {
		t.Error("Expected Silent to be true")
	}
	if result.IsError {
		t.Error("Expected IsError to be false")
	}
	if result.Async {
		t.Error("Expected Async to be false")
	}
}

func TestAsyncResult(t *testing.T) {
	result := AsyncResult("async task started")

	if result.ForLLM != "async task started" {
		t.Errorf("Expected ForLLM 'async task started', got '%s'", result.ForLLM)
	}
	if result.Silent {
		t.Error("Expected Silent to be false")
	}
	if result.IsError {
		t.Error("Expected IsError to be false")
	}
	if !result.Async {
		t.Error("Expected Async to be true")
	}
}

func TestErrorResult(t *testing.T) {
	result := ErrorResult("operation failed")

	if result.ForLLM != "operation failed" {
		t.Errorf("Expected ForLLM 'operation failed', got '%s'", result.ForLLM)
	}
	if result.Silent {
		t.Error("Expected Silent to be false")
	}
	if !result.IsError {
		t.Error("Expected IsError to be true")
	}
	if result.Async {
		t.Error("Expected Async to be false")
	}
}

func TestUserResult(t *testing.T) {
	content := "user visible message"
	result := UserResult(content)

	if result.ForLLM != content {
		t.Errorf("Expected ForLLM '%s', got '%s'", content, result.ForLLM)
	}
	if result.ForUser != content {
		t.Errorf("Expected ForUser '%s', got '%s'", content, result.ForUser)
	}
	if result.Silent {
		t.Error("Expected Silent to be false")
	}
	if result.IsError {
		t.Error("Expected IsError to be false")
	}
	if result.Async {
		t.Error("Expected Async to be false")
	}
}

func TestToolResultJSONSerialization(t *testing.T) {
	tests := []struct {
		name   string
		result *ToolResult
	}{
		{
			name:   "basic result",
			result: NewToolResult("basic content"),
		},
		{
			name:   "silent result",
			result: SilentResult("silent content"),
		},
		{
			name:   "async result",
			result: AsyncResult("async content"),
		},
		{
			name:   "error result",
			result: ErrorResult("error content"),
		},
		{
			name:   "user result",
			result: UserResult("user content"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal to JSON
			data, err := json.Marshal(tt.result)
			if err != nil {
				t.Fatalf("Failed to marshal: %v", err)
			}

			// Unmarshal back
			var decoded ToolResult
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("Failed to unmarshal: %v", err)
			}

			// Verify fields match (Err should be excluded)
			if decoded.ForLLM != tt.result.ForLLM {
				t.Errorf("ForLLM mismatch: got '%s', want '%s'", decoded.ForLLM, tt.result.ForLLM)
			}
			if decoded.ForUser != tt.result.ForUser {
				t.Errorf("ForUser mismatch: got '%s', want '%s'", decoded.ForUser, tt.result.ForUser)
			}
			if decoded.Silent != tt.result.Silent {
				t.Errorf("Silent mismatch: got %v, want %v", decoded.Silent, tt.result.Silent)
			}
			if decoded.IsError != tt.result.IsError {
				t.Errorf("IsError mismatch: got %v, want %v", decoded.IsError, tt.result.IsError)
			}
			if decoded.Async != tt.result.Async {
				t.Errorf("Async mismatch: got %v, want %v", decoded.Async, tt.result.Async)
			}
		})
	}
}

func TestToolResultWithErrors(t *testing.T) {
	err := errors.New("underlying error")
	result := ErrorResult("error message").WithError(err)

	if result.Err == nil {
		t.Error("Expected Err to be set")
	}
	if result.Err.Error() != "underlying error" {
		t.Errorf("Expected Err message 'underlying error', got '%s'", result.Err.Error())
	}

	// Verify Err is not serialized
	data, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		t.Fatalf("Failed to marshal: %v", marshalErr)
	}

	var decoded ToolResult
	if unmarshalErr := json.Unmarshal(data, &decoded); unmarshalErr != nil {
		t.Fatalf("Failed to unmarshal: %v", unmarshalErr)
	}

	if decoded.Err != nil {
		t.Error("Expected Err to be nil after JSON round-trip (should not be serialized)")
	}
}

func TestToolResultJSONStructure(t *testing.T) {
	result := UserResult("test content")

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Verify JSON structure
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Check expected keys exist
	if _, ok := parsed["for_llm"]; !ok {
		t.Error("Expected 'for_llm' key in JSON")
	}
	if _, ok := parsed["for_user"]; !ok {
		t.Error("Expected 'for_user' key in JSON")
	}
	if _, ok := parsed["silent"]; !ok {
		t.Error("Expected 'silent' key in JSON")
	}
	if _, ok := parsed["is_error"]; !ok {
		t.Error("Expected 'is_error' key in JSON")
	}
	if _, ok := parsed["async"]; !ok {
		t.Error("Expected 'async' key in JSON")
	}

	// Check that 'err' is NOT present (it should have json:"-" tag)
	if _, ok := parsed["err"]; ok {
		t.Error("Expected 'err' key to be excluded from JSON")
	}

	// Verify values
	if parsed["for_llm"] != "test content" {
		t.Errorf("Expected for_llm 'test content', got %v", parsed["for_llm"])
	}
	if parsed["silent"] != false {
		t.Errorf("Expected silent false, got %v", parsed["silent"])
	}
}

func TestToolResultContentForLLM_AppendsArtifactPaths(t *testing.T) {
	result := &ToolResult{
		ForLLM:       "Artifact created.",
		ArtifactTags: []string{"[file:/tmp/example.png]"},
	}

	content := result.ContentForLLM()
	if !strings.Contains(content, "Artifact created.") {
		t.Fatalf("expected original content in ContentForLLM, got %q", content)
	}
	if !strings.Contains(content, "Local artifact paths: [file:/tmp/example.png]") {
		t.Fatalf("expected artifact path note in ContentForLLM, got %q", content)
	}
	if !strings.Contains(content, artifactPathsLLMNote) {
		t.Fatalf("expected artifact guidance note in ContentForLLM, got %q", content)
	}
}

// isBrowserControlDeferral mirrors, exactly, the classification predicate
// ADR-085 FR-012a specifies the turn-engine ledger (FR-013) must use:
// "result.Deferred != nil && result.Deferred.Gate == \"browser_control\"".
// It is NOT a helper exported by pkg/tools — the spec places the real
// consumer in pkg/agent (FR-013's ledger) — it is inlined here, verbatim, so
// this test proves the STRUCT gives that exact predicate a correct answer
// without reaching into pkg/agent's write-set.
func isBrowserControlDeferral(r *ToolResult) bool {
	return r.Deferred != nil && r.Deferred.Gate == "browser_control"
}

// TestToolResult_DeferralIsStructuralNotProse proves the ADR-085 FR-012a
// contract end to end: a deferral is a STRUCT fact (Deferred != nil, with
// Gate == "browser_control"), never a fact about ForLLM's prose. Before
// FR-012a, the only way to know a result was a deferral was to parse
// ForLLM — exactly what the turn-loop ledger must never do, because a
// wording change would silently break the count.
func TestToolResult_DeferralIsStructuralNotProse(t *testing.T) {
	// A result whose ForLLM prose says "deferred" but carries no structural
	// marker at all MUST NOT be counted as a deferral — the ledger reads
	// Deferred, never ForLLM.
	proseOnly := NewToolResult("a human is currently controlling the browser; this call was deferred, please wait")
	if proseOnly.Deferred != nil {
		t.Fatalf("expected Deferred to be nil on a plain NewToolResult, got %+v", proseOnly.Deferred)
	}
	if isBrowserControlDeferral(proseOnly) {
		t.Fatal("a result with deferral-sounding prose but no structural marker must not classify as a deferral")
	}

	// A result carrying the structural marker MUST classify as a deferral
	// even when its ForLLM prose is entirely unrelated (i.e. wording alone
	// is not the signal).
	structural := NewToolResult("some unrelated tool output")
	structural.Deferred = &ToolDeferral{Gate: "browser_control", Reason: "human is currently controlling the browser"}
	if !isBrowserControlDeferral(structural) {
		t.Fatal("a result carrying Deferred{Gate: \"browser_control\"} must classify as a deferral regardless of ForLLM wording")
	}

	// A structural marker for a DIFFERENT gate must not be mistaken for a
	// browser-control deferral — Gate, not mere non-nilness, is the
	// discriminator FR-012a specifies.
	otherGate := NewToolResult("irrelevant")
	otherGate.Deferred = &ToolDeferral{Gate: "some_other_gate", Reason: "irrelevant"}
	if isBrowserControlDeferral(otherGate) {
		t.Fatal("a deferral for a different gate must not classify as a browser_control deferral")
	}
}

// TestToolResult_DeferredNeverCrossesTheWire proves ADR-085 FR-012a's
// Constraint #8 requirement: Deferred carries json:"-" and must never appear
// in the JSON this struct produces — the same guarantee the existing Err
// field already has, verified the same way (round-trip through the real
// custom MarshalJSON, plus a raw key-presence check).
func TestToolResult_DeferredNeverCrossesTheWire(t *testing.T) {
	result := NewToolResult("deferred")
	result.Deferred = &ToolDeferral{Gate: "browser_control", Reason: "human is currently controlling the browser"}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if _, ok := parsed["Deferred"]; ok {
		t.Error("expected 'Deferred' key to be excluded from JSON (json:\"-\")")
	}
	if _, ok := parsed["deferred"]; ok {
		t.Error("expected no 'deferred' key in JSON output at all")
	}

	var decoded ToolResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded.Deferred != nil {
		t.Error("expected Deferred to be nil after a JSON round-trip (json:\"-\" strips it on the way out, so nothing populates it on the way back in)")
	}
}
