// Omnipus — KB-2b (defect-list-knowledge-base-ux-2026-09-08.md,
// founder-ratified 2026-09-08): pins list_directory's duplicated
// knowledge-base marker literals against pkg/knowledge's own constants.
//
// pkg/knowledge imports pkg/tools (for the knowledge_* agent tools), so
// filesystem.go cannot import pkg/knowledge back — confirmed empirically: an
// INTERNAL test file (package tools) importing pkg/knowledge fails go test
// with "import cycle not allowed in test", because the test-augmented
// package is still "tools" for cycle-detection purposes.
//
// This file is an EXTERNAL test package (tools_test) instead, which is not
// the same thing: it imports both pkg/tools and pkg/knowledge, and neither
// of those imports tools_test back, so there is no cycle — only an ordinary
// package depending on two others, one of which depends on the first.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools_test

import (
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestListDirectory_KnowledgeBaseMarkerNamesMatchPkgKnowledge fails loudly if
// either side ever renames a marker directory without updating the other —
// the one drift risk tools.KnowledgeBaseMarkerNames' own doc comment
// (filesystem.go) names as the cost of duplicating the literal instead of
// importing it.
func TestListDirectory_KnowledgeBaseMarkerNamesMatchPkgKnowledge(t *testing.T) {
	want := []string{knowledge.MarkerDirName, knowledge.ObsidianMarkerDirName}
	got := append([]string(nil), tools.KnowledgeBaseMarkerNames...)

	sort.Strings(want)
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("tools.KnowledgeBaseMarkerNames has %d entries, pkg/knowledge names %d marker "+
			"directories (MarkerDirName, ObsidianMarkerDirName) — got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("tools.KnowledgeBaseMarkerNames = %v, want %v (pkg/knowledge.MarkerDirName=%q, "+
				"pkg/knowledge.ObsidianMarkerDirName=%q) — list_directory's KB: detection has "+
				"drifted from pkg/knowledge's own marker rule", got, want,
				knowledge.MarkerDirName, knowledge.ObsidianMarkerDirName)
		}
	}
}
