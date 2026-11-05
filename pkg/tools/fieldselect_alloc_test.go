// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"runtime"
	"testing"
)

// projectBytesPerCall is the average heap bytes one Project call allocates
// for this selection (the selection itself is built before measuring).
func projectBytesPerCall(t *testing.T, full map[string]any, spec FieldSpec, selected []string) uint64 {
	t.Helper()
	const runs = 50
	if _, refusal := Project(full, spec, selected); refusal != nil { // warm-up
		t.Fatalf("Project refused: %s", refusal.ForLLM)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < runs; i++ {
		if _, refusal := Project(full, spec, selected); refusal != nil {
			t.Fatalf("Project refused: %s", refusal.ForLLM)
		}
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / runs
}

// CK-13: the result map's capacity must not follow the caller's selection.
// 10,000 repeats of one valid name must allocate about what one name does;
// sizing the map from len(selected) costs hundreds of kilobytes per call.
func TestProject_RepeatedSelection_AllocationIndependentOfSelectionLength(t *testing.T) {
	spec := FieldSpec{Object: "task", Identity: []string{"id"}, Valid: []string{"title", "status"}}
	full := map[string]any{"id": "t1", "title": "Write", "status": "next"}
	repeated := make([]string, 10000)
	for i := range repeated {
		repeated[i] = "title"
	}
	few := projectBytesPerCall(t, full, spec, []string{"title"})
	many := projectBytesPerCall(t, full, spec, repeated)
	t.Logf("bytes per call: one selection %d, 10,000 repeated %d", few, many)
	const slack = 32 << 10 // far below one oversized map, far above GC noise
	if many > few+slack {
		t.Fatalf("10,000 repeated selections allocate %d bytes per call, one selection %d: "+
			"the capacity follows the caller's selection again", many, few)
	}
}
