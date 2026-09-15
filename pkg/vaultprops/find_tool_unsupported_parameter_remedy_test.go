// Omnipus — Finding 8: the unknown-top-level-argument check
// (find_tool.go's Execute, run BEFORE the scope gate to stop the 24-retry
// loop TestFindTool_UnknownArgumentRefusedBeforeScope guards) must still
// hand back knowledgefind's own STRUCTURED refusal — Permitted list and a
// targeted remedy included — not a flat string that merely says the same
// thing in prose.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestFindTool_UnsupportedParameter' ./pkg/vaultprops/
package vaultprops

import (
	"context"
	"strings"
	"testing"
)

// TestFindTool_UnsupportedParameter_CarriesTheTargetedRemedy is the
// reproduction named in the finding: {"words":"x","order_by":"title"} used
// to come back with a remedy saying "use sort"; after the ordering fix that
// stopped the 24-retry loop, it degraded to a flat error string with no
// remedy at all. This must carry it again.
func TestFindTool_UnsupportedParameter_CarriesTheTargetedRemedy(t *testing.T) {
	res := NewFindTool(t.TempDir()).Execute(context.Background(), map[string]any{
		"words": "x", "order_by": "title",
	})
	if res == nil {
		t.Fatal("Execute returned nil")
	}
	got := res.ForLLM

	if !strings.Contains(got, `"order_by" is not an argument`) {
		t.Errorf("the refusal does not name the offending argument by its own spelling.\ngot: %s", got)
	}
	if !strings.Contains(got, "accepted:") || !strings.Contains(got, "words") {
		t.Errorf("the refusal does not list the accepted arguments.\ngot: %s", got)
	}
	// THE LOAD-BEARING ASSERTION. "use sort" is the targeted remedy
	// knowledgefind/tool.go's unknownParameterRemedy gives specifically for
	// order_by/orderby — a flat, unstructured error string does not carry
	// this, only the real generated.RecordProblem.Fix does.
	if !strings.Contains(got, "use sort") {
		t.Fatalf("STRUCTURED REFUSAL LOST: the targeted remedy for order_by ('use sort') is missing — "+
			"this reads as knowledgefind's rich refusal was replaced by a flat string.\ngot: %s", got)
	}
	// The refusal must still be a REFUSAL, not a partial success: same
	// contract knowledgefind's own Render() applies everywhere else.
	if !strings.Contains(got, "REFUSED") {
		t.Errorf("expected the response to read as refused; got:\n%s", got)
	}
	// And the ordering guarantee TestFindTool_UnknownArgumentRefusedBeforeScope
	// exists for must still hold: no scope refusal leaking through here.
	if strings.Contains(got, "no single knowledge base") {
		t.Fatalf("ORDERING REGRESSION: the scope gate ran before the argument check.\ngot: %s", got)
	}
}

// TestFindTool_UnsupportedParameter_UnmappedNameGetsTheGenericRemedy is the
// negative control: an unknown argument that ISN'T one of knowledgefind's
// recognised mistakes (order_by, where, having, ...) must still get SOME
// remedy — the generic "drop the argument, or call knowledge_describe" —
// not an empty Fix. Without this, TestFindTool_UnsupportedParameter_
// CarriesTheTargetedRemedy above could be satisfied by hard-coding "use
// sort" for every unknown argument, which would not actually be reusing
// knowledgefind's per-mistake mapping.
func TestFindTool_UnsupportedParameter_UnmappedNameGetsTheGenericRemedy(t *testing.T) {
	res := NewFindTool(t.TempDir()).Execute(context.Background(), map[string]any{
		"totally_unrecognised_key": true,
	})
	got := res.ForLLM
	if strings.Contains(got, "use sort") {
		t.Fatalf("an argument name unrelated to order_by must not get order_by's remedy.\ngot: %s", got)
	}
	if !strings.Contains(got, "drop the argument, or call knowledge_describe") {
		t.Fatalf("expected the generic remedy for an unrecognised argument name.\ngot: %s", got)
	}
}
