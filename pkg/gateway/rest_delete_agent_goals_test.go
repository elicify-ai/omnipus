// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/goal"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// TestDeleteAgent_EndsTheDeletedAgentsActiveGoals is UAT E-3's wiring oracle:
// DELETE /api/v1/agents/{id} must end the active chat goals that agent was
// working — an honest `cleared` transition naming the agent, the record
// retained — instead of leaving them active for the keeper to push at an agent
// that no longer exists.
func TestDeleteAgent_EndsTheDeletedAgentsActiveGoals(t *testing.T) {
	api := buildExecutorTestAPI(t)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "harness must provide a session store")
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "test-agent")
	require.NoError(t, err)

	gs := goal.NewStore(config.OmnipusHomeDir())
	now := time.Now().UTC()
	g, err := goal.New(generated.GoalOwnerKindSession, meta.ID, generated.GoalSourceChatCompiled,
		"write e3-marker.txt with three made-up octopus facts", "", nil,
		[]task.AcceptanceCriterion{{
			Kind: task.KindProse, Judgment: task.JudgmentBoolean, Provenance: task.ProvenanceFloor,
			Text: "No secrets appear in the output.", Author: task.CriterionAuthor{Kind: task.AuthorKindAgent, ID: "test-agent"},
		}}, 20, now)
	require.NoError(t, err)
	require.NoError(t, gs.Create(g))
	_, err = gs.Update(g.GoalID, func(cur *goal.Goal) error { return cur.Activate(meta.ID, now) })
	require.NoError(t, err)

	w := httptest.NewRecorder()
	api.HandleAgents(w, httptest.NewRequest(http.MethodDelete, "/api/v1/agents/test-agent", nil))
	require.Equal(t, http.StatusNoContent, w.Code, "delete must succeed: %s", w.Body.String())

	after, err := gs.Get(g.GoalID)
	require.NoError(t, err, "the goal record must be retained, not erased")
	assert.Equal(t, generated.GoalStateCleared, after.State, "the deleted agent's goal must not stay active")
	assert.True(t, strings.Contains(after.TerminalReason, "test-agent") && strings.Contains(after.TerminalReason, "deleted"),
		"terminal reason %q must name the deleted agent", after.TerminalReason)
}
