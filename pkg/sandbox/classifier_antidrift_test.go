// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-092 FR-012 Level 2 — the classifier self-check (S21b). Level 1
// (fspreflight_antidrift_test.go) proves the pre-flight VERDICT agrees with
// DeriveKernelPolicy's RENDERING, for paths and operations the test itself
// supplies directly. That alone does not prove the classifier's own
// command-text -> {path, operation} EXTRACTION step (FR-038,
// tools.ClassifyPathOperations) is correct — a classifier that always
// extracted the wrong operation could still pass Level 1 trivially, because
// Level 1 never calls it. This file is Level 2: it checks
// ClassifyPathOperations' output against a human-authored oracle (the BRD
// spec's own §5.4 C1-C8 dataset, transcribed from the spec text — not
// derived by reading the implementation), for both the filesystem
// classifier and its D8 network sibling (§5.5 N1-N5).
package sandbox_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestLevel2_OperationClassifier_MatchesSpecDataset is the §5.4 C1-C8
// dataset, transcribed verbatim from
// docs/internal/specs/adr-092-shell-permission-modes-spec.md.
func TestLevel2_OperationClassifier_MatchesSpecDataset(t *testing.T) {
	read := fspolicy.PathGrantAccessRead
	write := fspolicy.PathGrantAccessWrite

	cases := []struct {
		id   string
		cmd  string
		want []tools.PathOperation
	}{
		{"C1", "cat /etc/foo", []tools.PathOperation{
			{Path: "/etc/foo", Access: read},
		}},
		{"C2", "echo x > /etc/foo", []tools.PathOperation{
			{Path: "/etc/foo", Access: write},
		}},
		{"C3", "echo x >> /etc/foo", []tools.PathOperation{
			{Path: "/etc/foo", Access: write},
		}},
		{"C4", "tee /etc/foo", []tools.PathOperation{
			{Path: "/etc/foo", Access: write},
		}},
		{"C5", "cp /etc/foo /tmp/bar", []tools.PathOperation{
			{Path: "/etc/foo", Access: read},
			{Path: "/tmp/bar", Access: write},
		}},
		{"C6", "mv /etc/foo /tmp/bar", []tools.PathOperation{
			{Path: "/etc/foo", Access: read | write},
			{Path: "/tmp/bar", Access: write},
		}},
		{"C7", "dd if=/etc/foo of=/tmp/bar", []tools.PathOperation{
			{Path: "/etc/foo", Access: read},
			{Path: "/tmp/bar", Access: write},
		}},
		{"C8", "rm /etc/foo", []tools.PathOperation{
			{Path: "/etc/foo", Access: write},
		}},
	}

	for _, tc := range cases {
		t.Run(tc.id+"_"+tc.cmd, func(t *testing.T) {
			got, ok := tools.ClassifyPathOperations(tc.cmd)
			if !ok {
				t.Fatalf("%s %q: ClassifyPathOperations returned ok=false", tc.id, tc.cmd)
			}
			if !operationsEqual(got, tc.want) {
				t.Fatalf("%s %q: got %+v, want %+v", tc.id, tc.cmd, got, tc.want)
			}
		})
	}
}

func operationsEqual(a, b []tools.PathOperation) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestLevel2_SelfCheck_CatchesDeliberateMismatch is S21b: given a command
// whose known-correct {path, operation} is {(/etc/foo, write)}, feeding the
// oracle comparator a DELIBERATELY mutated extraction (read instead of
// write) must fail the comparison — proving the Level 2 check is sensitive
// to the classifier's actual extraction, not merely present in the suite.
func TestLevel2_SelfCheck_CatchesDeliberateMismatch(t *testing.T) {
	const cmd = "echo x > /etc/foo"
	want := []tools.PathOperation{{Path: "/etc/foo", Access: fspolicy.PathGrantAccessWrite}}

	got, ok := tools.ClassifyPathOperations(cmd)
	if !ok || !operationsEqual(got, want) {
		t.Fatalf("precondition failed: ClassifyPathOperations(%q) = %+v (ok=%v), want %+v", cmd, got, ok, want)
	}

	mutated := []tools.PathOperation{{Path: got[0].Path, Access: fspolicy.PathGrantAccessRead}}
	if operationsEqual(mutated, want) {
		t.Fatal("self-check comparator accepted a deliberately wrong extraction (read instead of write) — it cannot catch a real FR-038 classifier regression")
	}
	if !operationsEqual(got, want) {
		t.Fatal("self-check comparator rejected the actually-correct extraction")
	}
}

// TestLevel2_NetworkClassifier_MatchesSpecDataset is the §5.5 N1-N5
// dataset (FR-043), transcribed from the spec. N5 (a custom binary opening
// a raw socket) is not classifier-testable by definition — it documents
// FR-044's honest gap, not a ClassifyNetworkNeed input, so it is asserted
// separately by name rather than as a classifier row.
func TestLevel2_NetworkClassifier_MatchesSpecDataset(t *testing.T) {
	cases := []struct {
		id   string
		cmd  string
		want bool
	}{
		{"N1", "curl https://x.com", true},
		{"N2", "git push", true},
		{"N3", "ls -la", false},
		{"N4", "npm run build", true}, // binary-based, not behaviour-based — accepted over-flagging (FR-043)
	}
	for _, tc := range cases {
		t.Run(tc.id+"_"+tc.cmd, func(t *testing.T) {
			got := tools.ClassifyNetworkNeed(tc.cmd)
			if got != tc.want {
				t.Fatalf("%s %q: ClassifyNetworkNeed = %v, want %v", tc.id, tc.cmd, got, tc.want)
			}
		})
	}

	t.Run("N5_honest_gap_documented_not_classifier_testable", func(t *testing.T) {
		verdict := tools.EvaluateNetworkPreflight("./raw-socket-tool", false, nil)
		if verdict.NeedsEscalation() {
			t.Fatal("an unclassified binary must NOT be flagged by the pre-flight — FR-044's honest gap is the kernel's empty ConnectPortRules, not a prompt")
		}
	})
}
