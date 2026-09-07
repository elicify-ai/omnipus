// Omnipus — R1 regression: knowledge_find must not answer complete:true over
// a text index that was never built. F-9
// (docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md):
// 11 notes on disk contained "Vorlex"; asked for `words="Vorlex"` the tool
// answered `COMPLETE: yes — 0 records matched`. See also the ruling in
// docs/internal/design/knowledge-tools-remediation.md §R1.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package knowledgefind

import (
	"context"
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

func falsePtr() *bool {
	b := false
	return &b
}

// TestFind_UnbuiltTextIndex_WordsQueryRefusesRatherThanClaimingComplete is
// the direct regression for F-9's own reproduction: a bare `words=` query
// over an unbuilt text index must refuse, never answer complete:true, 0
// rows.
func TestFind_UnbuiltTextIndex_WordsQueryRefusesRatherThanClaimingComplete(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25") // a real candidate the properties store holds

	// Simulate the unbuilt index: the bleve index was opened but never
	// built, so Search legitimately returns zero hits AND Populated reports
	// false — the exact state that produced F-9.
	f.text.only = nil
	f.text.populated = falsePtr()

	// PRECONDITION CHECK: this test proves nothing unless the fixture is
	// actually in the state it means to exercise.
	if pop, err := f.text.Populated(context.Background()); err != nil || pop {
		t.Fatalf("fixture precondition failed: Populated() = (%v, %v), want (false, nil) — "+
			"the rest of this test is meaningless unless the stub genuinely reports "+
			"an unbuilt index", pop, err)
	}

	words := "Vorlex"
	r := req(withType("plant"))
	r.Words = &words

	resp := mustRefuse(t, f.depsWithText(), r)

	if resp.Complete {
		t.Fatalf("Complete = true over an unbuilt text index — this is F-9 exactly: a " +
			"zero-hit answer over a corpus that was never searched, reported as if it was")
	}
	found := false
	for _, p := range resp.Problems {
		if p.Code == generated.IndexUnavailable {
			found = true
		}
	}
	if !found {
		t.Errorf("no index_unavailable problem in the response; problems = %+v", resp.Problems)
	}
}

// TestFind_UnbuiltTextIndex_TypedAndFilteredWordsQueryAlsoRefuses is the
// blast-radius check. The finding attributed F-9 to a BARE `words=` query
// falling through a nil properties store; the actual return in
// find.go::findRecords sits before the properties-store (d.Store) check
// entirely, so ANY query carrying `words` — typed, filtered, or bare —
// reaches it. This is the typed-and-filtered shape, not the bare one, and
// it must refuse identically.
func TestFind_UnbuiltTextIndex_TypedAndFilteredWordsQueryAlsoRefuses(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	f.text.only = nil
	f.text.populated = falsePtr()

	words := "Vorlex"
	r := req(withType("plant"), withFilter(leaf("condition", "=", "growing")))
	r.Words = &words

	resp := mustRefuse(t, f.depsWithText(), r)
	if resp.Complete {
		t.Fatal("Complete = true over an unbuilt text index for a typed+filtered words query — " +
			"the bug is not confined to bare `words=`")
	}
}

// TestFind_TextIndexBuildStateUnreadable_Refuses covers Populated() itself
// failing (the index cannot even be asked whether it was built). A zero-hit
// answer this layer cannot even confirm was searched is no more trustworthy
// than one it positively knows was not — both refuse the same way.
func TestFind_TextIndexBuildStateUnreadable_Refuses(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "41.25")
	f.text.only = nil
	f.text.populatedErr = errors.New("index corrupt")

	words := "Vorlex"
	r := req(withType("plant"))
	r.Words = &words

	resp := mustRefuse(t, f.depsWithText(), r)
	if resp.Complete {
		t.Fatal("Complete = true when the text index's build state could not even be read")
	}
}

// TestFind_GenuinelyEmptyVault_BuiltIndexStillReportsCompleteZero is the
// regression this fix must NOT introduce: a genuinely empty vault (no notes
// at all) whose text index HAS completed a build must still answer an
// honest complete:true, 0 rows — never a refusal. Gating on Populated()
// (build completion) rather than on DocCount()==0 (current document count)
// is exactly what keeps this case distinct from the unbuilt-index case
// above, even though both currently observe zero word hits.
func TestFind_GenuinelyEmptyVault_BuiltIndexStillReportsCompleteZero(t *testing.T) {
	f := newFixture(t) // no f.plant(...) calls: the properties store holds nothing
	f.text.only = nil
	// f.text.populated left nil (== true): an ordinary, built index — the
	// state a real empty-but-indexed vault leaves behind.

	// PRECONDITION CHECK, mirroring the unbuilt-index test above: this test
	// proves nothing unless the fixture genuinely reports a built index over
	// zero content.
	if pop, err := f.text.Populated(context.Background()); err != nil || !pop {
		t.Fatalf("fixture precondition failed: Populated() = (%v, %v), want (true, nil)", pop, err)
	}

	words := "anything"
	r := req()
	r.Words = &words

	resp := mustFind(t, f.depsWithText(), r)
	if !resp.Complete {
		t.Errorf("Complete = false over a genuinely empty, correctly-built vault: %q",
			deref(resp.CompleteReason))
	}
	if len(resp.Rows) != 0 {
		t.Errorf("rows = %d, want 0 over an empty vault", len(resp.Rows))
	}
}
