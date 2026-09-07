// Omnipus — FR-064's two bounds, FR-066a, FR-066b.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// bulkCorpus writes n minimal records of the fixture type. Minimal on purpose:
// these tests are about COUNTING records, and a rich fixture would make the
// bound tests slow enough that someone would be tempted to skip them.
func bulkCorpus(t *testing.T, store Store, n int) {
	t.Helper()
	sc := plantSchema(t)
	const batchSize = 2000
	batch := make([]NoteRows, 0, batchSize)
	for i := range n {
		src := fmt.Sprintf("---\ntype: plant\nid: PL-%06d\nspecies: Sedum\n---\n", i)
		batch = append(batch, note(t, fmt.Sprintf("garden/bulk/p-%06d.md", i), sc, src))
		if len(batch) == batchSize {
			if err := store.(*Index).UpsertNotes(context.Background(), batch); err != nil {
				t.Fatalf("UpsertNotes: %v", err)
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		if err := store.(*Index).UpsertNotes(context.Background(), batch); err != nil {
			t.Fatalf("UpsertNotes: %v", err)
		}
	}
}

// TestBound_B1IsCountedBeforeAnyCandidateIsRetrieved.
//
// B1's whole claim is that it is a HARD PRECONDITION, taken before the work it
// bounds. A bound enforced after the work bounds nothing — which is what
// revision 5's version did, because it counted rows surviving a compiled WHERE
// clause that ruling R-A deletes.
//
// So the assertion is not merely "it refuses". It is that the refusal arrives
// with the visit function never called once.
func TestBound_B1IsCountedBeforeAnyCandidateIsRetrieved(t *testing.T) {
	store, _ := openIndex(t, Options{})
	bulkCorpus(t, store, BoundNarrowedCandidates+1)

	visited := 0
	err := store.Candidates(context.Background(), Selector{RecordType: "plant"},
		func(Candidate) (Verdict, error) {
			visited++
			return Rejected, nil
		})
	if err == nil {
		t.Fatal("FR-064 B1: a query narrowing to more than 50,000 records must be refused")
	}
	if visited != 0 {
		t.Errorf("B1 is specified as a precondition counted BEFORE retrieval, but %d candidates were retrieved first; "+
			"a bound enforced after the work it bounds bounds nothing", visited)
	}

	var be *BoundError
	if !errors.As(err, &be) {
		t.Fatalf("the refusal is not a BoundError: %v", err)
	}
	if be.Bound != "B1" || be.Count != BoundNarrowedCandidates+1 || be.Limit != BoundNarrowedCandidates {
		t.Errorf("the refusal must name the measured count and the bound, got %+v", be)
	}
	// FR-064: "the refusal must therefore NOT say 'add a filter on status'",
	// because under B1 adding a filter does not change the number that fired. A
	// refusal whose remedy does not work is the failure class this whole
	// document exists to remove.
	if strings.Contains(be.Error(), "filter") {
		t.Errorf("B1's refusal offers a remedy that does not reduce the number that fired: %q", be.Error())
	}
	for _, want := range []string{"scope", "kind"} {
		if !strings.Contains(be.Error(), want) {
			t.Errorf("B1's refusal must name %q — the dimensions the count actually ranges over: %q", want, be.Error())
		}
	}
}

// TestBound_B1AdmitsExactlyTheBound — an off-by-one here is a product decision
// nobody made.
func TestBound_B1AdmitsExactlyTheBound(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes 50,000 records")
	}
	store, _ := openIndex(t, Options{})
	bulkCorpus(t, store, BoundNarrowedCandidates)

	seen := 0
	if err := store.Candidates(context.Background(), Selector{RecordType: "plant"},
		func(Candidate) (Verdict, error) {
			seen++
			return Rejected, nil
		}); err != nil {
		t.Fatalf("exactly %d candidates must be admitted, not refused: %v", BoundNarrowedCandidates, err)
	}
	if seen != BoundNarrowedCandidates {
		t.Errorf("evaluated %d candidates, want %d", seen, BoundNarrowedCandidates)
	}
}

// TestBound_B2AbortsOnSurvivorsNotOnCandidates.
//
// B2 counts what the COMPARATOR ACCEPTED. The distinction is the entire point:
// a query that evaluates 40,000 candidates and keeps nine returns nine, and a
// query that keeps 10,001 is refused. An implementation that counted candidates
// instead would refuse the first and pass the second, which is backwards on both
// counts.
func TestBound_B2AbortsOnSurvivorsNotOnCandidates(t *testing.T) {
	store, _ := openIndex(t, Options{})
	const corpus = BoundSurvivors + 500
	bulkCorpus(t, store, corpus)

	// First: reject everything. Far more candidates than B2, no refusal.
	evaluated := 0
	if err := store.Candidates(context.Background(), Selector{RecordType: "plant"},
		func(Candidate) (Verdict, error) {
			evaluated++
			return Rejected, nil
		}); err != nil {
		t.Fatalf("B2 must bound SURVIVORS: evaluating %d candidates and accepting none must not be refused, got %v", corpus, err)
	}
	if evaluated != corpus {
		t.Fatalf("evaluated %d candidates, want %d", evaluated, corpus)
	}

	// Then: accept everything. The abort must fire, DURING evaluation.
	accepted := 0
	err := store.Candidates(context.Background(), Selector{RecordType: "plant"},
		func(Candidate) (Verdict, error) {
			accepted++
			return Accepted, nil
		})
	if err == nil {
		t.Fatal("FR-064 B2: accepting more than 10,000 records must abort the query")
	}
	var be *BoundError
	if !errors.As(err, &be) {
		t.Fatalf("the abort is not a BoundError: %v", err)
	}
	if be.Bound != "B2" {
		t.Errorf("bound %q, want B2", be.Bound)
	}
	// It is a STREAMING abort: it stops as soon as the survivor above the bound
	// is accepted, and does not run the corpus out.
	if accepted != BoundSurvivors+1 {
		t.Errorf("the abort fired after %d acceptances, want %d — B2 is a streaming abort, not a check at the end",
			accepted, BoundSurvivors+1)
	}
	if !strings.Contains(be.Error(), "filter") {
		t.Errorf("B2's refusal must name a remedy that reduces survivors: %q", be.Error())
	}
}

// TestBound_TheComparatorsOwnErrorIsNotSwallowed.
//
// The visit callback is the comparator. If it fails, the query fails — it does
// not quietly return the records evaluated so far, which would be a partial
// answer presented as a whole one.
func TestBound_TheComparatorsOwnErrorIsNotSwallowed(t *testing.T) {
	store, _ := openIndex(t, Options{})
	for i := 1; i <= 3; i++ {
		mustUpsert(t, store, plantNote(t, i, "growing"))
	}
	sentinel := errors.New("the comparator gave up")
	err := store.Candidates(context.Background(), Selector{}, func(Candidate) (Verdict, error) {
		return Rejected, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("the comparator's error was lost on the way out: %v", err)
	}
	if IsBoundExceeded(err) {
		t.Error("a comparator failure was reported as a bound refusal")
	}
}

// TestBound_ContextCancellationStopsTheStream.
func TestBound_ContextCancellationStopsTheStream(t *testing.T) {
	store, _ := openIndex(t, Options{})
	bulkCorpus(t, store, 200)

	ctx, cancel := context.WithCancel(context.Background())
	seen := 0
	err := store.Candidates(ctx, Selector{}, func(Candidate) (Verdict, error) {
		seen++
		if seen == 10 {
			cancel()
		}
		return Rejected, nil
	})
	cancel()
	if err == nil {
		t.Error("a cancelled query reported success")
	}
	if seen > 100 {
		t.Errorf("the stream ran on for %d records after cancellation", seen)
	}
}
