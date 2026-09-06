// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// goal_status_criteria_test.go — ADR-074 D5.2 / FR-011 (test 19 wire half):
// setGoalStatusCriteria maps task.AcceptanceCriterion onto the generated
// GoalStatusFrame.criteria items: per-kind payloads become pointers (absent,
// not zero-valued, when the kind carries none), commands cross verbatim, and
// an empty input leaves the optional wire field absent entirely.
package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func baseGoalFrame() generated.GoalStatusFrame {
	return generated.GoalStatusFrame{
		Type:      string(generated.WsFrameTypeGoalStatus),
		SessionId: "s1",
		Condition: "ship it",
		Round:     0,
		MaxRounds: 20,
		Cap:       16,
		State:     "queued",
	}
}

func TestSetGoalStatusCriteria_MapsAllThreeKinds(t *testing.T) {
	minZero := 0
	maxThree := 3
	in := []task.AcceptanceCriterion{
		{
			ID: "c1", Kind: task.KindProse, Text: "the post is written",
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "mia"},
			Status: task.CritPending,
		},
		{
			ID: "c2", Kind: task.KindCheck, Text: "tests pass",
			Check:  &task.CriterionCheck{Command: "go test ./...", ExpectedExitCode: 0},
			Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "daniel"},
			Status: task.CritPending,
		},
		{
			ID: "c3", Kind: task.KindBehavior, Text: "researched for real",
			Behavior: &task.CriterionBehavior{
				Tool: "search_web", MinCount: &minZero, MaxCount: &maxThree,
				Scope: task.BehaviorScopeTaskSession,
			},
			Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "ray"},
			Status: task.CritPending,
		},
	}

	f := baseGoalFrame()
	setGoalStatusCriteria(&f, in)
	require.Len(t, f.Criteria, 3)

	// Prose: NO payload pointers — absent on the wire, never zero-valued
	// (the pointer-typed optional-object contract the generator fix bought).
	assert.Nil(t, f.Criteria[0].Check)
	assert.Nil(t, f.Criteria[0].Behavior)
	assert.Equal(t, "prose", f.Criteria[0].Kind)
	assert.Equal(t, "the post is written", f.Criteria[0].Text)
	require.NotNil(t, f.Criteria[0].Id)
	assert.Equal(t, "c1", *f.Criteria[0].Id)
	assert.Equal(t, "agent", f.Criteria[0].Author.Kind)
	assert.Equal(t, "mia", f.Criteria[0].Author.Id)
	assert.Equal(t, "pending", f.Criteria[0].Status)

	// Check: command VERBATIM (FR-113 substance).
	require.NotNil(t, f.Criteria[1].Check)
	assert.Equal(t, "go test ./...", f.Criteria[1].Check.Command)
	assert.Equal(t, 0, f.Criteria[1].Check.ExpectedExitCode)
	assert.Nil(t, f.Criteria[1].Behavior)

	// Behavior: pointer counts preserved (explicit zero ≠ absent).
	require.NotNil(t, f.Criteria[2].Behavior)
	assert.Equal(t, "search_web", f.Criteria[2].Behavior.Tool)
	require.NotNil(t, f.Criteria[2].Behavior.MinCount)
	assert.Equal(t, 0, *f.Criteria[2].Behavior.MinCount)
	require.NotNil(t, f.Criteria[2].Behavior.MaxCount)
	assert.Equal(t, 3, *f.Criteria[2].Behavior.MaxCount)
	require.NotNil(t, f.Criteria[2].Behavior.Scope)
	assert.Equal(t, "task_session", *f.Criteria[2].Behavior.Scope)
}

func TestSetGoalStatusCriteria_EmptyInputLeavesFieldAbsent(t *testing.T) {
	f := baseGoalFrame()
	setGoalStatusCriteria(&f, nil)
	assert.Nil(t, f.Criteria)

	data, err := json.Marshal(f)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(data), `"criteria"`),
		"empty breakdown must stay absent from the wire (omitempty)")
}

// A prose criterion's marshalled item must NOT carry zero-valued check/
// behavior objects — that is exactly the strict-zod frame-drop failure the
// generator's optional-inline-object pointer fix closed.
func TestSetGoalStatusCriteria_ProseItemMarshalsWithoutPayloadKeys(t *testing.T) {
	f := baseGoalFrame()
	setGoalStatusCriteria(&f, []task.AcceptanceCriterion{{
		ID: "c1", Kind: task.KindProse, Text: "done means done",
		Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "mia"},
		Status: task.CritPending,
	}})
	data, err := json.Marshal(f)
	require.NoError(t, err)
	s := string(data)
	assert.False(t, strings.Contains(s, `"check"`), "prose item leaked a zero-valued check object: %s", s)
	assert.False(t, strings.Contains(s, `"behavior"`), "prose item leaked a zero-valued behavior object: %s", s)
}
