// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-074 D7 / judgment-first spec US-5, test 18 (backend half): the
// evidence_quote becomes real — parsed from the judge's declared JSON
// contract {"id","evidence_quote","met","reason"}, truncated rune-safe to
// 500 code points AT THE PARSER, and absent from every fail-closed verdict
// constructor (empty-quote verdicts render no line downstream).
package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/task"
)

func TestParseJudgeResponse_EvidenceQuoteParsed(t *testing.T) {
	raw := `{"met": true, "summary": "ok", "criteria": [
		{"id": "c1", "evidence_quote": "--- PASS: TestX (0.01s)", "met": true, "reason": "test passed"},
		{"id": "c2", "evidence_quote": "", "met": false, "reason": "nothing to quote"},
		{"id": "c3", "met": false, "reason": "old-soul entry without the field"}
	]}`
	out, err := parseJudgeResponse(raw)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if got := out.Criteria[0].EvidenceQuote; got != "--- PASS: TestX (0.01s)" {
		t.Errorf("c1 quote = %q, want the verbatim quote", got)
	}
	if got := out.Criteria[1].EvidenceQuote; got != "" {
		t.Errorf("c2 quote = %q, want empty", got)
	}
	// EC-6: a pre-D7 soul's JSON without the field parses fine, quote empty.
	if got := out.Criteria[2].EvidenceQuote; got != "" {
		t.Errorf("c3 (field absent) quote = %q, want empty", got)
	}
}

// DS-5: quote {"", 500cp, 501cp multi-byte boundary} → no line / intact /
// rune-safe 500.
func TestParseJudgeResponse_EvidenceQuoteTruncation(t *testing.T) {
	// Multi-byte fixture: U+20AC EURO SIGN is 3 bytes per code point, so any
	// byte-based 500-truncation would split a rune here.
	exactly500 := strings.Repeat("€", 500)
	over501 := strings.Repeat("€", 501)

	mk := func(quote string) string {
		b, err := json.Marshal(quote)
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		return `{"met": true, "criteria": [{"id": "c1", "evidence_quote": ` + string(b) + `, "met": true, "reason": "r"}], "summary": "s"}`
	}

	t.Run("exactly_500_code_points_intact", func(t *testing.T) {
		out, err := parseJudgeResponse(mk(exactly500))
		if err != nil {
			t.Fatalf("parseJudgeResponse: %v", err)
		}
		if got := out.Criteria[0].EvidenceQuote; got != exactly500 {
			t.Errorf("500cp quote was modified (len %d runes)", len([]rune(got)))
		}
	})

	t.Run("501_code_points_truncated_rune_safe", func(t *testing.T) {
		out, err := parseJudgeResponse(mk(over501))
		if err != nil {
			t.Fatalf("parseJudgeResponse: %v", err)
		}
		got := out.Criteria[0].EvidenceQuote
		if n := len([]rune(got)); n != 500 {
			t.Errorf("truncated quote has %d code points, want 500", n)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncation split a rune: result is not valid UTF-8")
		}
		if got != exactly500 {
			t.Errorf("truncation did not preserve the first 500 code points")
		}
	})
}

func TestTruncateEvidenceQuote(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"", 500, ""},
		{"abc", 5, "abc"},
		{"abcdef", 3, "abc"},
		{"a€b", 2, "a€"},
		{"abc", 0, ""},
		{"abc", -1, ""},
	}
	for _, c := range cases {
		if got := truncateEvidenceQuote(c.in, c.max); got != c.want {
			t.Errorf("truncateEvidenceQuote(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}

// Fail-closed constructors never fabricate a quote (D5.3: the quote line
// renders only when non-empty — it is empty on every fail-closed verdict).
func TestFailClosedProseVerdicts_NoEvidenceQuote(t *testing.T) {
	criteria := []task.AcceptanceCriterion{
		{ID: "c1", Kind: task.KindProse, Text: "works"},
		{ID: "c2", Kind: task.KindProse, Text: "documented"},
	}
	for _, v := range failClosedProseVerdicts(criteria, "judge unavailable") {
		if v.EvidenceQuote != "" {
			t.Errorf("fail-closed verdict for %s carries a quote %q, want empty", v.CriterionID, v.EvidenceQuote)
		}
		if v.Met {
			t.Errorf("fail-closed verdict for %s is met", v.CriterionID)
		}
	}
}

// Persisted-JSON posture: an empty quote is ABSENT from the marshalled
// verdict (omitempty), a populated one round-trips verbatim.
func TestCriterionVerdict_EvidenceQuoteJSONRoundTrip(t *testing.T) {
	empty, err := json.Marshal(task.CriterionVerdict{CriterionID: "c1", Met: false, Reason: "r"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(empty), "evidence_quote") {
		t.Errorf("empty quote must be omitted from JSON, got %s", empty)
	}

	quoted, err := json.Marshal(task.CriterionVerdict{CriterionID: "c1", Met: true, Reason: "r", EvidenceQuote: "PASS line"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back task.CriterionVerdict
	if err := json.Unmarshal(quoted, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.EvidenceQuote != "PASS line" {
		t.Errorf("round-trip quote = %q, want %q", back.EvidenceQuote, "PASS line")
	}
}
