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
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// TestTaskWire_EffectiveMaxAttemptsFollowsGoalTryLimit pins the task wire half
// of the founder decision of 2026-09-14: with the Settings -> Performance goal
// try limit at 5, a task created through the API has a paired goal record
// whose max_rounds is 5 and reports effective_max_attempts 5 (the "M" in the
// UI's "attempt N/M"); a task carrying its own max_attempts reports that
// instead. Before the fix there was no such field and the SPA fell back to a
// hardcoded 20.
func TestTaskWire_EffectiveMaxAttemptsFollowsGoalTryLimit(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	require.NoError(t, api.agentLoop.MutateConfig(func(cfg *config.Config) error {
		cfg.Planning.GoalMaxRounds = 5
		return nil
	}))
	wsID := ensureTestWorkspace(t, api)

	w := postTaskJSON(t, api, `{"title":"invoice report","action":"llm","workspace_id":"`+wsID+`",`+
		`"criteria":`+oneCriterionJSON("the report lists every open invoice")+`,`+
		`"dod":`+oneCriterionJSON("no credentials appear in the report")+`}`)
	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created gen.Task
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	require.NotNil(t, created.EffectiveMaxAttempts, "effective_max_attempts must always be present; body=%s", w.Body.String())
	assert.Equal(t, 5, *created.EffectiveMaxAttempts)

	rec, err := tools.GoalStoreForTasks(api.taskStore).GetByOwner(gen.GoalOwnerKindTask, created.Id)
	require.NoError(t, err, "a task created with criteria and dod has a paired goal record")
	assert.Equal(t, 5, rec.MaxRounds, "the paired goal record must carry the goal try limit")

	w2 := postTaskJSON(t, api, `{"title":"quick check","action":"llm","workspace_id":"`+wsID+`","max_attempts":2,`+
		`"criteria":`+oneCriterionJSON("the check passes")+`,`+
		`"dod":`+oneCriterionJSON("the check output is attached")+`}`)
	require.Equal(t, http.StatusCreated, w2.Code, "body=%s", w2.Body.String())
	var overridden gen.Task
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &overridden))
	require.NotNil(t, overridden.EffectiveMaxAttempts)
	assert.Equal(t, 2, *overridden.EffectiveMaxAttempts, "a per-task max_attempts wins over the goal try limit")
}
