// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCriterion_Validate exercises the "Dataset: Criterion validation" rows
// from the Planning & Goals spec (Part A §C, SD-A9) via the store's normal
// Create path. Rows 14/15 (all-check + assignee bash policy deny/ask) are the
// agent-tool-path tier (ADR D2 rule 5, FR-017) — that check depends on caller
// identity/tool-policy the store does not have access to, so it is NOT
// exercised here; it belongs to the tool/gateway layer.
func TestCriterion_Validate(t *testing.T) {
	longText := strings.Repeat("x", 1001)
	cases := []struct {
		name    string
		crit    AcceptanceCriterion
		wantErr bool
	}{
		{
			name: "1 prose valid",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "looks good",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: false,
		},
		{
			name: "2 check valid",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "tests pass",
				Check:  &CriterionCheck{Command: "go test", ExpectedExitCode: 0},
				Author: CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"},
			},
			wantErr: false,
		},
		{
			name: "3 invalid kind",
			crit: AcceptanceCriterion{
				Kind: "bogus", Text: "x",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "4 empty text",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "5 text over 1000 runes",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: longText,
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "6 check with no check object",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "7 exit code min 0",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: 0},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: false,
		},
		{
			name: "8 exit code max 255",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: 255},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: false,
		},
		{
			name: "9 exit code 256 over max",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: 256},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "10 exit code -1 under min",
			crit: AcceptanceCriterion{
				Kind: KindCheck, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: -1},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "11 prose with check object is mixed shape",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "x",
				Check:  &CriterionCheck{Command: "x", ExpectedExitCode: 0},
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
			},
			wantErr: true,
		},
		{
			name: "12 author absent",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "x",
			},
			wantErr: true,
		},
		{
			name: "13 author id empty",
			crit: AcceptanceCriterion{
				Kind: KindProse, Text: "x",
				Author: CriterionAuthor{Kind: AuthorKindUser, ID: ""},
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			tk := mkTask("crit-task", "ws-1")
			tk.Criteria = []AcceptanceCriterion{tc.crit}
			err := s.Create(tk)
			if tc.wantErr {
				require.Error(t, err, "case %q must be rejected", tc.name)
				assert.True(t, errors.Is(err, ErrValidation),
					"case %q must wrap ErrValidation, got: %v", tc.name, err)
				return
			}
			require.NoError(t, err, "case %q must be accepted", tc.name)
			require.Len(t, tk.Criteria, 1)
			// Server-set fields: ID generated, Status defaulted to pending.
			assert.NotEmpty(t, tk.Criteria[0].ID, "criterion ID must be server-set")
			assert.Equal(t, CritPending, tk.Criteria[0].Status, "criterion status must default to pending")
		})
	}
}

// TestCriterion_AuthorRecorded verifies that a criterion's author identity
// (ADR D2 rule 3) is recorded exactly as supplied and survives a round trip
// through create + reload, for both agent and user authors — a
// differentiation check that the field is not dropped or hardcoded.
func TestCriterion_AuthorRecorded(t *testing.T) {
	s := newStore(t)

	agentAuthored := mkTask("agent-authored", "ws-1")
	agentAuthored.Criteria = []AcceptanceCriterion{{
		Kind: KindCheck, Text: "tests pass",
		Check:  &CriterionCheck{Command: "go test ./...", ExpectedExitCode: 0},
		Author: CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"},
	}}
	require.NoError(t, s.Create(agentAuthored))

	userAuthored := mkTask("user-authored", "ws-1")
	userAuthored.Criteria = []AcceptanceCriterion{{
		Kind: KindProse, Text: "reads well",
		Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
	}}
	require.NoError(t, s.Create(userAuthored))

	got1, err := s.Get(agentAuthored.ID)
	require.NoError(t, err)
	require.Len(t, got1.Criteria, 1)
	assert.Equal(t, AuthorKindAgent, got1.Criteria[0].Author.Kind)
	assert.Equal(t, "jim", got1.Criteria[0].Author.ID)

	got2, err := s.Get(userAuthored.ID)
	require.NoError(t, err)
	require.Len(t, got2.Criteria, 1)
	assert.Equal(t, AuthorKindUser, got2.Criteria[0].Author.Kind)
	assert.Equal(t, "alice", got2.Criteria[0].Author.ID)

	// Differentiation: the two tasks' recorded authors must differ.
	assert.NotEqual(t, got1.Criteria[0].Author, got2.Criteria[0].Author)
}
