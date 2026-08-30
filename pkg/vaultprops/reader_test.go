// Omnipus — tests for the propindex -> knowledge adapter.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultprops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/records/propindex"
)

// TestReader_AnswersTheTwoQuestionsOverARealStore.
//
// The integrity sweep is tested against a fake because the questions it asks
// are two and a database in the way would make every bound test a database
// test. THIS is the test that proves the two questions are answered correctly
// over the real store — without it, the fake would be the only thing either
// side had ever agreed with.
func TestReader_AnswersTheTwoQuestionsOverARealStore(t *testing.T) {
	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the refusal is covered by TestOpen_RefusesOnSQLiteLessBuild")
	}
	ctx := context.Background()
	store, err := propindex.Open(ctx, filepath.Join(t.TempDir(), "properties.db"), propindex.Options{})
	if err != nil {
		t.Fatalf("propindex.Open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := store.Close(); cerr != nil {
			t.Errorf("closing the store: %v", cerr)
		}
	})

	rows := []propindex.NoteRows{
		{
			Path: "Widgets/Gear.md", Kind: propindex.KindNote,
			RecordType: "widget", RecordID: "WI-0001", SourceHash: "aaaa",
			Relations: []propindex.RelationRow{
				{Prop: "maker", Elem: 0, Target: "Acme Ltd", Raw: "[[Acme Ltd]]"},
			},
		},
		{
			Path: "Foundries/Acme Ltd.md", Kind: propindex.KindNote,
			RecordType: "foundry", RecordID: "FO-0001", SourceHash: "bbbb",
		},
		{
			Path: "Notes/loose.md", Kind: propindex.KindNote, SourceHash: "cccc",
		},
	}
	for _, r := range rows {
		if err := store.UpsertNote(ctx, r); err != nil {
			t.Fatalf("UpsertNote(%s): %v", r.Path, err)
		}
	}

	reader := NewReader(store)

	t.Run("ScanRecords narrows by declared type", func(t *testing.T) {
		var got []knowledge.IndexedRecord
		if err := reader.ScanRecords(ctx, "widget", func(r knowledge.IndexedRecord) error {
			got = append(got, r)
			return nil
		}); err != nil {
			t.Fatalf("ScanRecords: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected one widget, got %+v", got)
		}
		if got[0].Path != "Widgets/Gear.md" || got[0].RecordType != "widget" || got[0].RecordID != "WI-0001" {
			t.Errorf("the three facts the sweep needs did not survive the adapter: %+v", got[0])
		}
	})

	t.Run("an empty record type means every record", func(t *testing.T) {
		n := 0
		if err := reader.ScanRecords(ctx, "", func(knowledge.IndexedRecord) error {
			n++
			return nil
		}); err != nil {
			t.Fatalf("ScanRecords: %v", err)
		}
		// Three, INCLUDING the note that declares no type. FR-005: an
		// ordinary note is the majority of every real vault and is not an
		// error; the orphan-row check has to see it.
		if n != 3 {
			t.Errorf("an empty record type must select every record including untyped notes; got %d", n)
		}
	})

	t.Run("ScanRelations carries the edge", func(t *testing.T) {
		var got []knowledge.IndexedRelation
		if err := reader.ScanRelations(ctx, "widget", func(e knowledge.IndexedRelation) error {
			got = append(got, e)
			return nil
		}); err != nil {
			t.Fatalf("ScanRelations: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected one relation edge, got %+v", got)
		}
		want := knowledge.IndexedRelation{
			Path: "Widgets/Gear.md", RecordID: "WI-0001", Property: "maker", Target: "Acme Ltd",
		}
		if got[0] != want {
			t.Errorf("the edge did not survive the adapter: got %+v, want %+v", got[0], want)
		}
	})

	t.Run("a visitor error stops the scan and reaches the caller", func(t *testing.T) {
		boom := errors.New("the sweep gave up")
		err := reader.ScanRecords(ctx, "", func(knowledge.IndexedRecord) error { return boom })
		if !errors.Is(err, boom) {
			t.Errorf("a visitor's error must reach the caller unchanged, got %v", err)
		}
	})
}

// TestReader_SatisfiesThePropertyIndexReaderInterface — a compile-time
// assertion, stated rather than implied.
//
// The whole cycle break rests on this being structurally true; if the
// interface gains a method and this adapter does not, the failure should be
// here rather than at a wiring site in another package.
func TestReader_SatisfiesThePropertyIndexReaderInterface(t *testing.T) {
	var _ knowledge.PropertyIndexReader = (*Reader)(nil)
}

// TestOpen_RefusesOnSQLiteLessBuild — FR-020h at the wiring seam.
//
// Open is what knowledge_describe calls, and on a build with no SQLite it must
// return records.RequirePropertyIndex's error UNCHANGED so the message still
// names the platform. Wrapping it would replace the one sentence that says
// which capabilities are gone and which still work.
func TestOpen_RefusesOnSQLiteLessBuild(t *testing.T) {
	records.AssertRefusesWhenIndexUnavailable(t, records.CapabilityOpenIndex,
		func() (knowledge.PropertyIndexReader, error) {
			return Open(context.Background(), t.TempDir(), t.TempDir())
		})
}

// TestOpen_RefusesAnIndexThatWasNeverBuilt.
//
// A sweep over an empty index would report zero duplicate identifiers for a
// vault nobody has indexed — a confidently wrong all-clear, which is the exact
// failure mode this layer exists to remove. So Open refuses, and the refusal
// says what to do about it.
func TestOpen_RefusesAnIndexThatWasNeverBuilt(t *testing.T) {
	if !records.PropertyIndexAvailable {
		t.Skip("the platform refusal fires first on this build")
	}
	home := t.TempDir()
	root := t.TempDir()
	_, err := Open(context.Background(), home, root)
	if err == nil {
		t.Fatalf("an index that was never built must be refused, not swept as clean")
	}
	for _, want := range []string{"has not been built", "index the collection"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must name the remedy; %q missing from %q", want, err.Error())
		}
	}
}

// verdictSpy is a propindex.Store that records the verdict the adapter returns
// for each candidate. Only Candidates is meaningful; the rest satisfy the
// interface.
type verdictSpy struct {
	rows     []propindex.Candidate
	verdicts []propindex.Verdict
}

func (s *verdictSpy) Candidates(_ context.Context, _ propindex.Selector, visit func(propindex.Candidate) (propindex.Verdict, error)) error {
	for _, c := range s.rows {
		v, err := visit(c)
		s.verdicts = append(s.verdicts, v)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *verdictSpy) UpsertNote(context.Context, propindex.NoteRows) error { return nil }
func (s *verdictSpy) DeleteNote(context.Context, string) error             { return nil }
func (s *verdictSpy) CountCandidates(context.Context, propindex.Selector) (int, error) {
	return len(s.rows), nil
}
func (s *verdictSpy) Tasks(context.Context, propindex.Selector, func(propindex.TaskHit) error) error {
	return nil
}
func (s *verdictSpy) Relations(context.Context, propindex.Selector, func(propindex.RelationHit) error) error {
	return nil
}
func (s *verdictSpy) NeedsFullIndex() bool { return false }
func (s *verdictSpy) Close() error         { return nil }

// TestReader_EveryCandidateIsRejectedSoTheSweepCannotHitTheSurvivorBound.
//
// propindex aborts a stream once ACCEPTED candidates exceed BoundSurvivors
// (10,000) — a bound on MEMORY, counted on what the comparator kept. The
// integrity sweep keeps nothing: an identifier and a path per record, dropped
// immediately. If this adapter returned Accepted, a sweep over a vault with
// more than 10,000 records of one type would abort with a B2 refusal that has
// nothing to do with what the caller asked, and the operator would be told to
// "add or tighten a filter" on a call that takes no filter.
//
// That failure needs 10,001 records to observe end to end, which is a corpus
// nobody builds in a unit test — so the property is asserted directly instead
// of being left to a test that will never run.
//
// MUTATION: return propindex.Accepted from ScanRecords' visitor and this fails.
func TestReader_EveryCandidateIsRejectedSoTheSweepCannotHitTheSurvivorBound(t *testing.T) {
	spy := &verdictSpy{rows: []propindex.Candidate{
		{Path: "a.md", RecordType: "widget", RecordID: "WI-1"},
		{Path: "b.md", RecordType: "widget", RecordID: "WI-2"},
		{Path: "c.md", RecordType: "widget", RecordID: "WI-3"},
	}}
	n := 0
	if err := NewReader(spy).ScanRecords(context.Background(), "widget", func(knowledge.IndexedRecord) error {
		n++
		return nil
	}); err != nil {
		t.Fatalf("ScanRecords: %v", err)
	}
	if n != len(spy.rows) {
		t.Fatalf("every record must reach the visitor; got %d of %d", n, len(spy.rows))
	}
	if len(spy.verdicts) != len(spy.rows) {
		t.Fatalf("expected one verdict per record, got %d", len(spy.verdicts))
	}
	for i, v := range spy.verdicts {
		if v != propindex.Rejected {
			t.Errorf("record %d was reported as a SURVIVOR; the sweep materialises nothing, so every "+
				"candidate must be Rejected or a large vault trips propindex's B2 memory bound "+
				"with a refusal the caller cannot act on", i)
		}
	}
}
