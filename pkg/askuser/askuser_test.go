// Omnipus — AskUserQuestion validation-table tests (spec Test 1)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package askuser

import (
	"strings"
	"testing"
)

// validQuestion returns a minimal valid question, mutable per test case.
func validQuestion() Question {
	return Question{
		Header:   "Scope",
		Question: "Which scope should this change cover?",
		Options: []Option{
			{Label: "Backend only"},
			{Label: "Full stack", Description: "SPA + backend"},
		},
	}
}

func TestValidateQuestions_Table(t *testing.T) {
	long := func(n int) string { return strings.Repeat("x", n) }

	cases := []struct {
		name    string
		mutate  func() []Question
		wantErr string // substring; "" = valid
	}{
		{"valid minimal", func() []Question { return []Question{validQuestion()} }, ""},
		{"valid with recommended and default_safe", func() []Question {
			q := validQuestion()
			q.Recommended = "Backend only"
			q.DefaultSafe = true
			return []Question{q}
		}, ""},
		{"zero questions", func() []Question { return nil }, "at least 1"},
		{"eleven questions", func() []Question {
			out := make([]Question, 11)
			for i := range out {
				q := validQuestion()
				q.Header = q.Header + string(rune('A'+i))
				out[i] = q
			}
			return out
		}, "at most 10"},
		{"empty header", func() []Question {
			q := validQuestion()
			q.Header = "  "
			return []Question{q}
		}, "header must not be empty"},
		{"header over 16 chars", func() []Question {
			q := validQuestion()
			q.Header = long(17)
			return []Question{q}
		}, "header exceeds 16"},
		{"header exactly 16 chars is valid", func() []Question {
			q := validQuestion()
			q.Header = long(16)
			return []Question{q}
		}, ""},
		{"duplicate headers", func() []Question {
			a, b := validQuestion(), validQuestion()
			return []Question{a, b}
		}, "duplicate header"},
		{"empty question text", func() []Question {
			q := validQuestion()
			q.Question = ""
			return []Question{q}
		}, "question text must not be empty"},
		{"question text over 500", func() []Question {
			q := validQuestion()
			q.Question = long(501)
			return []Question{q}
		}, "question text exceeds 500"},
		{"context over 4000", func() []Question {
			q := validQuestion()
			q.Context = long(4001)
			return []Question{q}
		}, "context exceeds 4000"},
		{"one option", func() []Question {
			q := validQuestion()
			q.Options = q.Options[:1]
			return []Question{q}
		}, "at least 2 options"},
		{"seven options", func() []Question {
			q := validQuestion()
			q.Options = nil
			for i := 0; i < 7; i++ {
				q.Options = append(q.Options, Option{Label: "opt" + string(rune('A'+i))})
			}
			return []Question{q}
		}, "at most 6 options"},
		{"empty option label", func() []Question {
			q := validQuestion()
			q.Options[1].Label = ""
			return []Question{q}
		}, "label must not be empty"},
		{"option label over 80", func() []Question {
			q := validQuestion()
			q.Options[1].Label = long(81)
			return []Question{q}
		}, "label exceeds 80"},
		{"option description over 200", func() []Question {
			q := validQuestion()
			q.Options[1].Description = long(201)
			return []Question{q}
		}, "description exceeds 200"},
		{"duplicate option labels within a question", func() []Question {
			q := validQuestion()
			q.Options[1].Label = q.Options[0].Label
			return []Question{q}
		}, "duplicate option label"},
		{"recommended names no option", func() []Question {
			q := validQuestion()
			q.Recommended = "Nonexistent"
			return []Question{q}
		}, "names no option"},
		{"default_safe without recommended", func() []Question {
			q := validQuestion()
			q.DefaultSafe = true
			return []Question{q}
		}, "default_safe requires recommended"},
		{"multi_select with recommended is valid (one-element auto-default)", func() []Question {
			q := validQuestion()
			q.MultiSelect = true
			q.Recommended = "Full stack"
			q.DefaultSafe = true
			return []Question{q}
		}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateQuestions(tc.mutate())
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected valid, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
