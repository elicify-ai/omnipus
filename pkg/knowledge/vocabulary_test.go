// Omnipus — FR-114/FR-115 zero-hit behaviour.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func vocabIndex(t *testing.T) *Index {
	t.Helper()
	corpus := t.TempDir()
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(corpus, filepath.FromSlash(rel)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("Pipeline.md", "# Pipeline\n\nprospect prospects prospecting were reviewed.\n")
	write("Other.md", "# Other\n\nunrelated gardening prose.\n")

	ix, err := OpenIndex(t.TempDir(), corpus)
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() {
		if err := ix.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	if _, err := ix.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	return ix
}

// TestSearch_ZeroHitsReportsVocabularyNotExpansion is §7 test 29.
//
// The query has no match. FR-115 requires the near-miss VOCABULARY, and FR-114
// forbids answering with the results a widened query would have found — so the
// assertion is two-sided: the terms must be there AND the notes must not.
func TestSearch_ZeroHitsReportsVocabularyNotExpansion(t *testing.T) {
	ix := vocabIndex(t)

	// A term the corpus does not hold, but whose neighbours it does.
	const q = "prospectus"

	hits, _, err := ix.SearchFiltered(q, 10, nil)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("the fixture is meant to produce zero hits for %q, got %v; "+
			"the zero-hit path is not being exercised", q, pathsOf(hits))
	}

	terms, err := ix.NearMissVocabulary(q)
	if err != nil {
		t.Fatalf("vocabulary: %v", err)
	}
	if len(terms) == 0 {
		t.Fatal("no near-miss vocabulary for a query whose neighbours are indexed; " +
			"FR-115 requires the nearest indexed terms to be surfaced")
	}
	for _, want := range []string{"prospect"} {
		found := false
		for _, got := range terms {
			if strings.HasPrefix(got, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("near-miss terms %v contain nothing starting %q", terms, want)
		}
	}

	stmt := ZeroHitStatement(terms)
	if !strings.HasPrefix(stmt, "No matches.") {
		t.Errorf("the statement must lead with the refusal, got %q", stmt)
	}
	// FR-114: the statement reports terms, never documents. A path leaking into
	// it would mean we had answered a question the caller did not ask.
	if strings.Contains(stmt, ".md") || strings.Contains(stmt, "/") {
		t.Errorf("the zero-hit statement names a DOCUMENT, which is silent expansion: %q", stmt)
	}
}

func TestZeroHitStatement_EmptyVocabularyIsStillHonest(t *testing.T) {
	stmt := ZeroHitStatement(nil)
	if stmt == "" {
		t.Fatal("an empty statement gives the caller nothing to distinguish " +
			"'no matches' from 'the search never ran'")
	}
	if strings.Contains(strings.ToLower(stmt), "try ") {
		t.Errorf("the statement coaches the caller toward a reformulation: %q", stmt)
	}
}

// TestNearMissVocabulary_DoesNotEchoTheQueryTerm pins the case that makes the
// suggestion misleading: a term that IS indexed but matched nothing under the
// caller's filters must not be handed back as if it were a spelling correction.
func TestNearMissVocabulary_DoesNotEchoTheQueryTerm(t *testing.T) {
	ix := vocabIndex(t)
	terms, err := ix.NearMissVocabulary("prospect")
	if err != nil {
		t.Fatalf("vocabulary: %v", err)
	}
	for _, got := range terms {
		if got == "prospect" {
			t.Errorf("the query term was returned as its own suggestion: %v", terms)
		}
	}
}
