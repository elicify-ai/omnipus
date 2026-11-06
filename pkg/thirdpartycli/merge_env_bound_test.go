// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

import (
	"fmt"
	"strings"
	"testing"
)

// Round 10 (CodeQL allocation-size alert): an oversized env_overrides set is
// caller input and must be refused before the merge sizes anything, with the
// set's own entry count in the refusal and nothing merged.
func TestMergeEnvOverrides_OversizedSetRefusedBeforeMerge(t *testing.T) {
	set := make(map[string]string, maxEnvEntries+1)
	for i := 0; i <= maxEnvEntries; i++ {
		set[fmt.Sprintf("NAME_%d", i)] = "v"
	}
	merged, out, err := MergeEnvOverrides(map[string]string{"KEEP": "1"}, set, nil)
	if err == nil {
		t.Fatalf("a set of %d entries must be refused", len(set))
	}
	want := fmt.Sprintf("env_overrides has %d entries; at most %d are allowed", maxEnvEntries+1, maxEnvEntries)
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("refusal = %q, want it to contain %q", err.Error(), want)
	}
	if merged != nil {
		t.Fatalf("nothing may be merged on refusal, got %d entries", len(merged))
	}
	if len(out.Added)+len(out.Replaced)+len(out.Removed)+len(out.AbsentRemoved) != 0 {
		t.Fatalf("no outcome may be reported on refusal, got %+v", out)
	}
}

// Control: a set within the limit still merges over the stored map, and a
// stored-plus-set total over the limit is still refused on the merged whole.
func TestMergeEnvOverrides_WithinLimitMerges_MergedTotalStillBounded(t *testing.T) {
	merged, out, err := MergeEnvOverrides(map[string]string{"A": "1"}, map[string]string{"A": "2", "B": "3"}, nil)
	if err != nil {
		t.Fatalf("valid merge refused: %v", err)
	}
	if merged["A"] != "2" || merged["B"] != "3" || len(merged) != 2 {
		t.Fatalf("merged = %v, want A=2 B=3", merged)
	}
	if len(out.Replaced) != 1 || out.Replaced[0] != "A" || len(out.Added) != 1 || out.Added[0] != "B" {
		t.Fatalf("outcome = %+v, want Replaced [A], Added [B]", out)
	}

	stored := make(map[string]string, maxEnvEntries)
	for i := 0; i < maxEnvEntries; i++ {
		stored[fmt.Sprintf("S_%d", i)] = "v"
	}
	_, _, err = MergeEnvOverrides(stored, map[string]string{"EXTRA": "v"}, nil)
	want := fmt.Sprintf("env_overrides has %d entries; at most %d are allowed", maxEnvEntries+1, maxEnvEntries)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("merged total over the limit: err = %v, want %q", err, want)
	}
}
