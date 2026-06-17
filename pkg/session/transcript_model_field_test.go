package session

import (
	"encoding/json"
	"testing"
)

// TestTranscriptEntry_ModelFieldRoundTrip verifies the per-turn `model` field
// (FR-013) survives the JSONL round-trip: a value set in Go decodes back to
// the same value. UI replay must be able to read each turn's producing model
// to render the "produced by {model}" badge (or "(model not recorded)" for
// legacy turns that lack the field — see FR-014).
//
// Traces to: spec §11 Dataset 5 / §12 TDD row 15 / FR-013.
func TestTranscriptEntry_ModelFieldRoundTrip(t *testing.T) {
	original := TranscriptEntry{
		ID:      "entry-1",
		Role:    "assistant",
		Content: "Hello world",
		Model:   "z-ai/glm-5.2",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded TranscriptEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Model != original.Model {
		t.Errorf("Model = %q after round-trip; want %q", decoded.Model, original.Model)
	}
}

// TestTranscriptEntry_ModelFieldAbsentIsOK verifies that legacy transcript
// entries — written before FR-013 — decode cleanly with an empty Model
// string. UI shows "(model not recorded)" for these (FR-014).
func TestTranscriptEntry_ModelFieldAbsentIsOK(t *testing.T) {
	jsonData := `{
		"id":"entry-1",
		"role":"assistant",
		"content":"Hi",
		"timestamp":"2026-06-17T12:00:00Z"
	}`

	var e TranscriptEntry
	if err := json.Unmarshal([]byte(jsonData), &e); err != nil {
		t.Fatalf("unmarshal legacy entry: %v", err)
	}
	if e.Model != "" {
		t.Errorf("legacy entry Model = %q, want empty", e.Model)
	}
	if e.Content != "Hi" {
		t.Errorf("Content lost during decode: %q", e.Content)
	}
}

// TestTranscriptEntry_ModelFieldIsOmitempty — Model is omitempty so legacy
// JSONL lines without the field stay identical to the pre-FR-013 format.
// (Regression: a non-omitempty field would change the byte content of every
// existing turn on disk and trigger spurious diffs in tests/users' setups.)
func TestTranscriptEntry_ModelFieldIsOmitempty(t *testing.T) {
	e := TranscriptEntry{ID: "x", Role: "assistant", Content: "x"}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(data); contains(got, `"model"`) {
		t.Errorf("empty Model must be omitted from JSON: %s", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (indexOf(s, substr) >= 0)
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
