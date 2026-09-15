// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestTaskWire_EffectiveMaxAttemptsIsTheTaskAttemptLimit pins the task wire half
// of the founder decision of 2026-09-14: goal tries and task attempts are
// separate limits. With Settings -> Performance tries per goal at 5, a task
// created through the API gets a goal record whose max_rounds is 5, while its
// effective_max_attempts reports the task attempt limit — the default of 3, or
// planning.task_max_attempts when set, or the task's own max_attempts.
func TestTaskWire_EffectiveMaxAttemptsIsTheTaskAttemptLimit(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Planning.GoalMaxRounds = 5
		cfg.Planning.TaskMaxAttempts = 0 // unset: the default of 3 applies
		return nil
	}))
	wsID := ensureTestWorkspace(t, api)

	created := createTaskForAttemptWire(t, api, `{"title":"invoice report","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON("the report lists every open invoice")+`,`+
		`"dod":`+oneCriterionJSON("no credentials appear in the report")+`}`)
	require.NotNil(t, created.EffectiveMaxAttempts, "effective_max_attempts must always be present")
	assert.Equal(t, 3, *created.EffectiveMaxAttempts, "the task attempt limit defaults to 3, not the goal try limit")

	rec, err := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, created.Id)
	require.NoError(t, err, "a task created with criteria and dod has a paired goal record")
	assert.Equal(t, 5, rec.MaxRounds, "the paired goal record carries the goal try limit")

	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Planning.TaskMaxAttempts = 4
		return nil
	}))
	configured := createTaskForAttemptWire(t, api, `{"title":"weekly digest","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON("the digest lists the week's merged changes")+`,`+
		`"dod":`+oneCriterionJSON("the digest links each change")+`}`)
	require.NotNil(t, configured.EffectiveMaxAttempts)
	assert.Equal(t, 4, *configured.EffectiveMaxAttempts, "planning.task_max_attempts sets the task attempt limit")

	overridden := createTaskForAttemptWire(t, api, `{"title":"quick check","action":"llm","workspace_id":"`+wsID+`","max_attempts":2,`+
		`"criteria":`+oneCriterionJSON("the check passes")+`,`+
		`"dod":`+oneCriterionJSON("the check output is attached")+`}`)
	require.NotNil(t, overridden.EffectiveMaxAttempts)
	assert.Equal(t, 2, *overridden.EffectiveMaxAttempts, "a per-task max_attempts wins")
}

// TestTaskWire_GoalTriesComeFromTheGoalRecord pins the other half of the two
// limits on the wire (issue #710): `judge_rounds` is the tries the goal's
// current run has used and `goal_max_rounds` the try limit that run started
// with, both read off the task's goal record — so the card can render
// "attempt N of M · try T of L" without mixing the two limits up.
func TestTaskWire_GoalTriesComeFromTheGoalRecord(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	wsID := ensureTestWorkspace(t, api)

	created := createTaskForAttemptWire(t, api, `{"title":"invoice report","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON("the report lists every open invoice")+`,`+
		`"dod":`+oneCriterionJSON("no credentials appear in the report")+`}`)
	assert.Nil(t, created.JudgeRounds, "a goal that has used no try yet reports no judge_rounds")

	gs := tools.GoalStoreForTasks(api.taskStore)
	rec, err := gs.GetByOwner(gen.GoalOwnerKindTask, created.Id)
	require.NoError(t, err, "a task created with criteria and dod has a paired goal record")
	// Distinct values for every limit so no assertion can pass by reading the
	// wrong one: 4 tries used, a 7-try limit on the record, and the task
	// attempt limit's default of 3.
	_, err = gs.Update(rec.GoalID, func(g *goal.Goal) error {
		g.Round = 4
		g.MaxRounds = 7
		return nil
	})
	require.NoError(t, err)

	w := patchTaskJSON(t, api, created.Id, `{"title":"invoice report (renamed)"}`)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.NotNil(t, got.JudgeRounds, "judge_rounds must be read off the goal record")
	assert.Equal(t, 4, *got.JudgeRounds)
	require.NotNil(t, got.GoalMaxRounds, "goal_max_rounds must be read off the goal record")
	assert.Equal(t, 7, *got.GoalMaxRounds)
	require.NotNil(t, got.EffectiveMaxAttempts)
	assert.Equal(t, 3, *got.EffectiveMaxAttempts, "the task attempt limit stays separate from the goal's try limit")
}

func createTaskForAttemptWire(t *testing.T, api *restAPI, body string) gen.Task {
	t.Helper()
	w := postTaskJSON(t, api, body)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var out gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}
