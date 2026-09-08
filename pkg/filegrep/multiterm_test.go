// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package filegrep

import "testing"

// TestFileGrep_MultiTermAcrossParagraphs_DefaultBehaviorFindsNothing pins
// KB-7b's REPRODUCTION step: today (Options.MatchAllWords unset, matching
// the agent grep tool's existing literal-substring contract), a query of
// two words that never sit on the same line matches nothing at all, even
// though the file plainly discusses both. This is the founder's own
// complaint ("A file containing both words in different paragraphs matches
// nothing") reproduced as a failing-would-be-wrong assertion: it documents
// the OLD behavior is still there for a caller that does not opt in, not
// that anything is broken.
func TestFileGrep_MultiTermAcrossParagraphs_DefaultBehaviorFindsNothing(t *testing.T) {
	fsys := buildFS(map[string]string{
		"q3.md": "Portfolio overview for the quarter.\n\nOur investment thesis remains unchanged.\n\nSeparately, the board report is attached below.\n",
	})
	res := mustSearch(t, oneRoot(fsys), Options{Query: "investment report"})
	if len(res.Hits) != 0 {
		t.Fatalf("MatchAllWords unset: expected zero hits (literal substring 'investment report' "+
			"never appears on one line), got %+v", res.Hits)
	}
}

// TestFileGrep_MatchAllWords_FindsTermsAcrossParagraphs is KB-7b's fix,
// proven against the SAME fixture the reproduction test used: with
// MatchAllWords set, the same query now finds the file, because both
// "investment" and "report" are present somewhere in it — just not on the
// same line.
func TestFileGrep_MatchAllWords_FindsTermsAcrossParagraphs(t *testing.T) {
	fsys := buildFS(map[string]string{
		"q3.md": "Portfolio overview for the quarter.\n\nOur investment thesis remains unchanged.\n\nSeparately, the board report is attached below.\n",
	})
	res := mustSearch(t, oneRoot(fsys), Options{Query: "investment report", MatchAllWords: true})
	if len(res.Hits) != 1 {
		t.Fatalf("MatchAllWords: expected exactly one collapsed hit for q3.md, got %d: %+v",
			len(res.Hits), res.Hits)
	}
	h := res.Hits[0]
	if h.Path != "q3.md" {
		t.Fatalf("hit path = %q, want q3.md", h.Path)
	}
	if h.Kind != KindContent {
		t.Fatalf("hit kind = %q, want content", h.Kind)
	}
}

// TestFileGrep_MatchAllWords_RequiresEveryWord is the negative half of the
// AND contract: a file containing only ONE of the two words must not match,
// exactly the case the founder's own measurement singled out (a note
// matching only the common word, in an unrelated sense, must not surface).
func TestFileGrep_MatchAllWords_RequiresEveryWord(t *testing.T) {
	fsys := buildFS(map[string]string{
		"only-report.md":     "Each one reports back with real evidence.\n",
		"only-investment.md": "Our investment thesis remains unchanged.\n",
		"both.md":            "The investment committee approved the report.\n",
	})
	res := mustSearch(t, oneRoot(fsys), Options{Query: "investment report", MatchAllWords: true})
	if len(res.Hits) != 1 {
		t.Fatalf("expected exactly one hit (both.md), got %d: %+v", len(res.Hits), res.Hits)
	}
	if res.Hits[0].Path != "both.md" {
		t.Fatalf("hit = %q, want both.md — a file matching only ONE query word must not be a hit",
			res.Hits[0].Path)
	}
}

// TestFileGrep_MatchAllWords_CollapsesManyMatchingLinesToOneHitWithCount
// pins KB-6a's per-document collapse: a file with many matching lines
// becomes ONE row with a match count, not one row per line.
func TestFileGrep_MatchAllWords_CollapsesManyMatchingLinesToOneHitWithCount(t *testing.T) {
	var body string
	for i := 0; i < 10; i++ {
		body += "The investment report is discussed again in this paragraph.\n"
	}
	fsys := buildFS(map[string]string{"repeated.md": body})

	res := mustSearch(t, oneRoot(fsys), Options{Query: "investment report", MatchAllWords: true})
	if len(res.Hits) != 1 {
		t.Fatalf("expected ONE collapsed hit regardless of line count, got %d: %+v",
			len(res.Hits), res.Hits)
	}
	if got := res.Hits[0].MatchCount; got != 10 {
		t.Errorf("MatchCount = %d, want 10 (every line matched both words)", got)
	}
}

// TestFileGrep_MatchAllWords_SingleWordUnaffected: KB-7b's flag has no
// effect on a single-word query — "all N words present" and "the one word
// present" are the same question, and per-line results (not a collapsed
// document) are still what a one-word search returns, matching ordinary
// mode exactly. This also proves MatchAllWords does not silently collapse
// results a caller never asked to have collapsed.
func TestFileGrep_MatchAllWords_SingleWordUnaffected(t *testing.T) {
	fsys := buildFS(map[string]string{
		"a.md": "alpha one\nalpha two\nalpha three\n",
	})
	withFlag := mustSearch(t, oneRoot(fsys), Options{Query: "alpha", MatchAllWords: true})
	without := mustSearch(t, oneRoot(fsys), Options{Query: "alpha", MatchAllWords: false})
	if len(withFlag.Hits) != len(without.Hits) {
		t.Fatalf("single-word MatchAllWords produced %d hits, plain search produced %d; "+
			"a one-word query must behave identically either way",
			len(withFlag.Hits), len(without.Hits))
	}
	if len(withFlag.Hits) != 3 {
		t.Fatalf("expected 3 per-line hits for a single-word query, got %d", len(withFlag.Hits))
	}
}

// TestFileGrep_MatchAllWords_IgnoredUnderRegex: a regex query is one
// expression the caller wrote on purpose; MatchAllWords must not split it on
// whitespace (which would mangle a pattern like "foo\s+bar" that contains a
// literal, meaningful space).
func TestFileGrep_MatchAllWords_IgnoredUnderRegex(t *testing.T) {
	fsys := buildFS(map[string]string{
		"a.md": "foo   bar\n",
	})
	res := mustSearch(t, oneRoot(fsys), Options{Query: `foo\s+bar`, Regex: true, MatchAllWords: true})
	if len(res.Hits) != 1 {
		t.Fatalf("regex mode with MatchAllWords set: expected the regex to still match as ONE "+
			"pattern (not split into words 'foo\\s+bar' pieces), got %d hits: %+v",
			len(res.Hits), res.Hits)
	}
}

// TestFileGrep_MatchAllWords_ContextAroundRepresentativeLine confirms the
// representative hit still carries context lines when requested — the
// per-document collapse must not silently drop the context feature.
func TestFileGrep_MatchAllWords_ContextAroundRepresentativeLine(t *testing.T) {
	fsys := buildFS(map[string]string{
		"a.md": "before line\ninvestment appears here\nmiddle line\nreport appears here\nafter line\n",
	})
	res := mustSearch(t, oneRoot(fsys), Options{
		Query: "investment report", MatchAllWords: true, ContextLines: 1,
	})
	if len(res.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(res.Hits))
	}
	h := res.Hits[0]
	if len(h.ContextBefore) != 1 || h.ContextBefore[0] != "before line" {
		t.Errorf("ContextBefore = %v, want [\"before line\"]", h.ContextBefore)
	}
	if len(h.ContextAfter) != 1 || h.ContextAfter[0] != "middle line" {
		t.Errorf("ContextAfter = %v, want [\"middle line\"]", h.ContextAfter)
	}
}
