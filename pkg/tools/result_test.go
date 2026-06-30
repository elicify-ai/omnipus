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

// TestContentForLLM_GuidanceBranch covers the Guidance appending logic in
// ContentForLLM: empty content, content + guidance, and the idempotent dedup.
func TestContentForLLM_GuidanceBranch(t *testing.T) {
	t.Parallel()

	t.Run("empty content + guidance returns guidance only", func(t *testing.T) {
		t.Parallel()
		tr := &ToolResult{Guidance: "use fetch_url instead"}
		got := tr.ContentForLLM()
		if got != "Guidance: use fetch_url instead" {
			t.Errorf("expected guidance-only content, got %q", got)
		}
	})

	t.Run("content + guidance appended with Guidance: prefix", func(t *testing.T) {
		t.Parallel()
		tr := &ToolResult{
			ForLLM:   "tool failed",
			Guidance: "do not retry",
		}
		got := tr.ContentForLLM()
		if !strings.Contains(got, "tool failed") {
			t.Errorf("expected original content in result, got %q", got)
		}
		if !strings.Contains(got, "Guidance: do not retry") {
			t.Errorf("expected 'Guidance: do not retry' appended, got %q", got)
		}
		// Guidance must appear AFTER the primary content.
		contentIdx := strings.Index(got, "tool failed")
		guidanceIdx := strings.Index(got, "Guidance:")
		if guidanceIdx < contentIdx {
			t.Errorf("Guidance appeared before content; got %q", got)
		}
	})

	t.Run("idempotent dedup: calling ContentForLLM twice does not duplicate guidance", func(t *testing.T) {
		t.Parallel()
		tr := &ToolResult{
			ForLLM:   "tool failed",
			Guidance: "do not retry",
		}
		first := tr.ContentForLLM()
		second := tr.ContentForLLM()
		if first != second {
			t.Errorf("ContentForLLM is not idempotent: first=%q second=%q", first, second)
		}
		// Verify "Guidance: do not retry" appears exactly once in the output.
		count := strings.Count(first, "Guidance: do not retry")
		if count != 1 {
			t.Errorf("expected guidance to appear exactly once, appeared %d times in %q", count, first)
		}
	})

	t.Run("nil ToolResult returns empty string", func(t *testing.T) {
		t.Parallel()
		var tr *ToolResult
		got := tr.ContentForLLM()
		if got != "" {
			t.Errorf("expected empty string for nil ToolResult, got %q", got)
		}
	})
}

// TestErrorResultWithGuidance verifies that ErrorResultWithGuidance sets both
// ForLLM and Guidance correctly, and that ContentForLLM appends Guidance after
// the error message.
func TestErrorResultWithGuidance(t *testing.T) {
	t.Parallel()

	msg := "capability unavailable in this deployment"
	guidance := "do NOT try to install a browser; use fetch_url to read the page instead"

	r := ErrorResultWithGuidance(msg, guidance)

	if !r.IsError {
		t.Errorf("expected IsError=true, got false")
	}
	if r.ForLLM != msg {
		t.Errorf("expected ForLLM=%q, got %q", msg, r.ForLLM)
	}
	if r.Guidance != guidance {
		t.Errorf("expected Guidance=%q, got %q", guidance, r.Guidance)
	}

	// ContentForLLM must include both the error message and the guidance.
	content := r.ContentForLLM()
	if !strings.Contains(content, msg) {
		t.Errorf("ContentForLLM missing error message; got %q", content)
	}
	if !strings.Contains(content, "Guidance: "+guidance) {
		t.Errorf("ContentForLLM missing guidance; got %q", content)
	}

	// Guidance already present → calling ContentForLLM again must not duplicate.
	content2 := r.ContentForLLM()
	if strings.Count(content2, "Guidance: "+guidance) != 1 {
		t.Errorf("guidance duplicated on second call; got %q", content2)
	}
}
