// Omnipus — regression coverage for F4: three places a degraded diagnostic
// signal was silently dropped instead of being reported as a caveat, so the
// response lost the one signal that would have told the caller its answer
// might not be trustworthy.
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

// TestF4a_NearestTermsErrorIsReportedNotSwallowed reproduces
// responses.go's zero-hit path silently absorbing a NearestTerms failure.
// Before the fix, `if t, err := d.Text.NearestTerms(...); err == nil { terms
// = t }` simply left `terms` nil on error and moved on — the response had
// no NearestTerms block (expected: NearestTerms is genuinely optional) AND
// no Problems entry saying the lookup itself failed, so a reader could not
// tell "the vocabulary check found nothing near your spelling either" from
// "the vocabulary check itself broke".
func TestF4a_NearestTermsErrorIsReportedNotSwallowed(t *testing.T) {
	f := newFixture(t)
	f.text.only = nil // nothing indexed matches — genuine zero hits
	f.text.termsErr = errors.New("bleve: dictionary reader closed")

	resp, err := Find(context.Background(), f.deps(), req(withWords("nomatch-f4a")))
	if err != nil {
		t.Fatalf("Find: unexpected refusal: %v", err)
	}
	if !resp.Complete {
		t.Fatalf("a genuine zero-hit answer over a populated index must stay Complete:true (AC-F4) "+
			"even when the best-effort NearestTerms suggestion failed\nfull response:\n%s", Render(resp))
	}
	if resp.NearestTerms != nil {
		t.Errorf("NearestTerms should be unset when the lookup failed, not populated from a partial " +
			"or stale result")
	}
	found := false
	for _, p := range resp.Problems {
		if p.Code == generated.IndexUnavailable {
			found = true
		}
	}
	if !found {
		t.Errorf("F4a: the NearestTerms lookup failed and NOTHING in the response says so — "+
			"the response lost the one signal that would tell the caller \"your spelling found "+
			"nothing near it either\" is not actually known\nfull response:\n%s", Render(resp))
	}
}

// TestF4b_FreshnessCheckErrorIsReportedNotSwallowed reproduces find.go's
// non-zero-hit `words` path (the A2(d) under-report caveat) dropping a
// freshness-read ERROR on the floor. checkTextIndexPopulated's OWN zero-hit
// fallback (a few lines below in the same file) already treats a freshness
// error as worth reporting; this path did not.
func TestF4b_FreshnessCheckErrorIsReportedNotSwallowed(t *testing.T) {
	f := gardenCorpus(t)
	f.text.only = []string{"garden/plants/PL-0002.md"} // "Fern" — one real hit
	f.text.freshErr = errors.New("bleve: manifest read failed")

	resp := mustFind(t, f.deps(), req(withType("plant"), withWords("Fern")))

	if len(resp.Rows) != 1 {
		t.Fatalf("the word search itself must still succeed despite the freshness check failing; "+
			"got %d rows", len(resp.Rows))
	}
	found := false
	for _, p := range resp.Problems {
		if p.Code == generated.IndexUnavailable {
			found = true
		}
	}
	if !found {
		t.Errorf("F4b: IndexFreshness returned an error and NOTHING in the response says the "+
			"under-report check could not run — the caller lost the entire \"this words result "+
			"may under-report\" warning exactly when the coverage check itself could not be "+
			"trusted\nfull response:\n%s", Render(resp))
	}
	if resp.Complete {
		t.Errorf("a response carrying a new IndexUnavailable problem must not also claim "+
			"Complete:true\nfull response:\n%s", Render(resp))
	}
}

// TestF4c_TextHashReadErrorIsDistinctFromNoHash reproduces assemble.go's
// per-row freshness comparison collapsing a FAILED SourceHash lookup into
// the same path as an honest "the text index holds no hash for this
// record" miss. Both used to render CompareFreshness's FreshnessUnknown
// reason verbatim — "one of the two indexes holds no content hash for it"
// — which is a false, specific claim about index CONTENT when the truth is
// that the read itself failed.
func TestF4c_TextHashReadErrorIsDistinctFromNoHash(t *testing.T) {
	f := newFixture(t)
	f.plant(1, "growing", "12") // garden/plant-0001.md, type=plant
	f.text.only = nil           // irrelevant: this is a TYPED-ONLY query (no words)
	f.text.sourceHashErr = errors.New("bleve: segment read failed")

	resp := mustFind(t, f.deps(), req(withType("plant")))

	if len(resp.Rows) != 1 {
		t.Fatalf("expected the one plant record back; got %d rows", len(resp.Rows))
	}
	if resp.Rows[0].Stale == nil || !*resp.Rows[0].Stale {
		t.Fatalf("a row whose text-hash lookup FAILED must still be flagged stale (unknown freshness "+
			"is never assumed fresh)\nfull response:\n%s", Render(resp))
	}
	var got *generated.RecordProblem
	for i := range resp.Problems {
		if resp.Problems[i].Code == generated.IndexUnavailable {
			got = &resp.Problems[i]
		}
	}
	if got == nil {
		t.Fatalf("F4c: expected an IndexUnavailable problem naming the FAILED read; got none — "+
			"full response:\n%s", Render(resp))
	}
	for i := range resp.Problems {
		if resp.Problems[i].Code == generated.StaleRecord {
			t.Errorf("F4c: got a StaleRecord problem (%q) for a row whose text-hash lookup FAILED — "+
				"that is the wrong diagnosis: CompareFreshness's \"one of the two indexes holds no "+
				"content hash for it\" is a claim about index CONTENT, not about a failing read, and "+
				"points the caller at check_integrity for the wrong reason", resp.Problems[i].Reason)
		}
	}
}
