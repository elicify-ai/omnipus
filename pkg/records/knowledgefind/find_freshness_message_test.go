// A2(d) — the zero-hit refusal must tell a STALE index apart from a
// NEVER-BUILT one when the searcher can report its freshness. Before this, both
// produced the same "has never finished indexing this vault" message, which is
// a flat lie on a vault that WAS indexed and merely drifted — exactly the
// stale-vs-absent confusion the tester reported.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"context"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// freshnessStub implements TextSearcher (only the methods this path needs are
// non-trivial) plus the optional TextFreshnessReporter.
type freshnessStub struct {
	populated bool
	fresh     TextIndexFreshness
	reportErr error
}

func (s *freshnessStub) Search(context.Context, string, int) ([]TextHit, error) { return nil, nil }
func (s *freshnessStub) NearestTerms(context.Context, string, int) ([]generated.VaultTermCount, error) {
	return nil, nil
}
func (s *freshnessStub) SourceHash(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (s *freshnessStub) Populated(context.Context) (bool, error) { return s.populated, nil }
func (s *freshnessStub) IndexFreshness(context.Context) (TextIndexFreshness, error) {
	return s.fresh, s.reportErr
}

// plainUnpopulatedStub implements ONLY TextSearcher — no freshness reporter —
// to prove the fallback message still fires for a searcher that cannot report
// freshness.
type plainUnpopulatedStub struct{}

func (plainUnpopulatedStub) Search(context.Context, string, int) ([]TextHit, error) { return nil, nil }
func (plainUnpopulatedStub) NearestTerms(context.Context, string, int) ([]generated.VaultTermCount, error) {
	return nil, nil
}
func (plainUnpopulatedStub) SourceHash(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (plainUnpopulatedStub) Populated(context.Context) (bool, error) { return false, nil }

func TestCheckTextIndexPopulated_IncompleteIndexQuotesCoverageCounts(t *testing.T) {
	// An index that reflects 1 of 68 files must refuse a zero-hit words query
	// AND quote the coverage, so a caller can tell "the index is behind by 67
	// files" from "the content is genuinely absent" (a FRESH index returning a
	// true zero, which never reaches this refusal at all).
	stub := &freshnessStub{
		populated: false, // coverage check fails → not populated
		fresh: TextIndexFreshness{
			Built: true, Fresh: false,
			ScannedFiles: 68, IndexedFiles: 1, PendingFiles: 67,
		},
	}
	ref := checkTextIndexPopulated(context.Background(), stub)
	if ref == nil {
		t.Fatal("checkTextIndexPopulated returned nil for an incomplete index; want a refusal")
	}
	reason := ref.Problem.Reason
	// The concrete coverage is the freshness signal A2(d) adds.
	if !strings.Contains(reason, "68") || !strings.Contains(reason, "67") || !strings.Contains(reason, "1 of") {
		t.Errorf("incomplete-index refusal did not quote the coverage counts (1 of 68, 67 pending): %q", reason)
	}
	// The established wording is preserved so callers/tests keying on it hold.
	if !strings.Contains(reason, "never finished indexing") {
		t.Errorf("incomplete-index refusal lost the established wording: %q", reason)
	}
}

func TestCheckTextIndexPopulated_ReporterErrorFallsBackToPlainMessage(t *testing.T) {
	// If the freshness report itself fails, the refusal must still fire with
	// the plain wording — never swallowed into a false success.
	stub := &freshnessStub{populated: false, reportErr: context.DeadlineExceeded}
	ref := checkTextIndexPopulated(context.Background(), stub)
	if ref == nil {
		t.Fatal("want a refusal when the freshness report errors")
	}
	if !strings.Contains(ref.Problem.Reason, "never finished indexing") {
		t.Errorf("fallback refusal lost its wording: %q", ref.Problem.Reason)
	}
}

func TestCheckTextIndexPopulated_NoFreshnessReporterFallsBack(t *testing.T) {
	// A searcher with no freshness reporter degrades to the pre-existing
	// never-built message — no loss of correctness, only of specificity.
	ref := checkTextIndexPopulated(context.Background(), plainUnpopulatedStub{})
	if ref == nil {
		t.Fatal("want a refusal for an unpopulated index")
	}
	if !strings.Contains(ref.Problem.Reason, "never finished indexing") {
		t.Errorf("fallback refusal lost its wording: %q", ref.Problem.Reason)
	}
}
