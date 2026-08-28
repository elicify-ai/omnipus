// Omnipus — the candidate stream: one record visited once, with its own values.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build !records_no_sqlite && !mipsle && !netbsd && !(freebsd && arm)

package propindex

import (
	"context"
	"fmt"
	"testing"
)

// TestAssembly_OneRecordIsVisitedOnceWithItsOwnValues.
//
// This is the store's half of SC-002a. A record carrying two matching values of
// a `many` property joins TWICE at the row level — the finding that made
// `COUNT(*)` return 2 and `SUM` return 200 where the truth was 1 and 100,
// silently, by a factor that varied per record. The store's contract is that the
// comparator never sees that fan-out: it sees one Candidate per record, holding
// that record's values and no other record's.
//
// The fixture varies the arity per record deliberately. A corpus where every
// record has the same number of elements cannot detect a stream that assigns
// values to the wrong record — every candidate would look plausible.
func TestAssembly_OneRecordIsVisitedOnceWithItsOwnValues(t *testing.T) {
	sc := plantSchema(t)
	store, _ := openIndex(t, Options{})

	// Record i carries i labels, so a misassignment changes a count.
	const records = 12
	for i := 1; i <= records; i++ {
		labels := ""
		for j := range i {
			labels += fmt.Sprintf("  - label-%02d-%02d\n", i, j)
		}
		src := fmt.Sprintf("---\ntype: plant\nid: PL-%04d\nspecies: Sedum %d\nlabels:\n%s---\n", i, i, labels)
		mustUpsert(t, store, note(t, fmt.Sprintf("garden/p-%04d.md", i), sc, src))
	}

	seen := map[string]int{}
	err := store.Candidates(context.Background(), Selector{RecordType: "plant"}, func(c Candidate) (Verdict, error) {
		seen[c.Path]++

		sp, ok := c.Prop("labels")
		if !ok {
			t.Errorf("%s carries no labels", c.Path)
			return Rejected, nil
		}
		var want int
		if _, err := fmt.Sscanf(c.RecordID, "PL-%04d", &want); err != nil {
			t.Errorf("%s has an unreadable identifier %q", c.Path, c.RecordID)
			return Rejected, nil
		}
		if len(sp.Elems) != want {
			t.Errorf("%s (%s) carries %d labels, want %d — the join fan-out was not collapsed per record",
				c.Path, c.RecordID, len(sp.Elems), want)
		}
		for _, e := range sp.Elems {
			var owner, idx int
			if _, err := fmt.Sscanf(e.Text, "label-%02d-%02d", &owner, &idx); err != nil {
				t.Errorf("%s holds an unreadable label %q", c.Path, e.Text)
				continue
			}
			if owner != want {
				t.Errorf("%s holds %q, which belongs to record %d — values crossed between records",
					c.Path, e.Text, owner)
			}
		}
		return Accepted, nil
	})
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}

	if len(seen) != records {
		t.Errorf("visited %d records, want %d", len(seen), records)
	}
	for path, n := range seen {
		if n != 1 {
			t.Errorf("%s was visited %d times; one record must reach the comparator once", path, n)
		}
	}
}

// TestAssembly_TheCountMatchesTheStream.
//
// B1 counts, then the stream retrieves. If the two disagree, the bound is
// bounding a different population from the one that gets evaluated — and the
// direction that matters is the silent one: a count LOWER than the stream lets a
// query through the bound and then does more work than the bound allowed.
func TestAssembly_TheCountMatchesTheStream(t *testing.T) {
	sc := plantSchema(t)
	store, _ := openIndex(t, Options{})
	for i := 1; i <= 7; i++ {
		mustUpsert(t, store, plantNote(t, i, "growing"))
	}
	// Two notes with no schema and no properties at all: they are candidates
	// under an unfiltered selector, and a LEFT JOIN is the reason they still
	// appear. An INNER JOIN here would drop them from the stream while the count
	// still included them.
	mustUpsert(t,
		store,
		note(t, "garden/plain-a.md", nil, "# A\n"),
		note(t, "garden/plain-b.md", nil, "# B\n"),
	)
	// And one record of the declared type whose every property is absent.
	mustUpsert(t, store, note(t, "garden/empty-record.md", sc, "---\ntype: plant\nid: PL-0800\n---\n"))

	for _, sel := range []Selector{
		{},
		{RecordType: "plant"},
		{Kind: KindNote},
		{PathPrefix: "garden/"},
		{RecordType: "plant", PathPrefix: "garden/plain"},
	} {
		want, err := store.CountCandidates(context.Background(), sel)
		if err != nil {
			t.Fatalf("CountCandidates(%+v): %v", sel, err)
		}
		got := len(collect(t, store, sel))
		if got != want {
			t.Errorf("selector %+v: counted %d candidates, streamed %d", sel, want, got)
		}
	}
}
