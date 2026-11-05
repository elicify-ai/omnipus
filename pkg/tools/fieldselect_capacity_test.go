// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "testing"

// Round 10 (CodeQL allocation-size alert): Project sizes its result from the
// spec, not from the caller's selection. A selection that repeats a valid name
// many times projects exactly like the single name: identity plus that key,
// each once, nothing reported absent.
func TestProject_RepeatedSelection_ProjectsEachKeyOnce(t *testing.T) {
	spec := FieldSpec{Object: "task", Identity: []string{"id"}, Valid: []string{"title", "status"}}
	full := map[string]any{"id": "t1", "title": "Write", "status": "next"}
	selected := make([]string, 10000)
	for i := range selected {
		selected[i] = "title"
	}
	got, refusal := Project(full, spec, selected)
	if refusal != nil {
		t.Fatalf("Project refused: %s", refusal.ForLLM)
	}
	want := map[string]any{"id": "t1", "title": "Write"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got[%q] = %v, want %v (full result %v)", k, got[k], v, got)
		}
	}
}
