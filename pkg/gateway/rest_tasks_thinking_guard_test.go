// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WP-C guard test for the task-transcript reader (spec section 8.1 row for
// pkg/gateway/rest_tasks.go: "Naturally excluded (no change) - the
// task-transcript reader emits judge_verdict entries only
// (if e.Type != session.EntryTypeJudgeVerdict { continue }); no gate
// plumbing is added here, keeping Boundary 3 at its two serializers").
//
// Guard: expected GREEN in RED. Pins the outcome MIN-002 requires: a
// thinking entry in a task's transcript never appears in the verdicts
// payload.

const wpcGatewayThinkingSentinel = "sk-live-abcd1234EFGH"

func TestHandleTaskVerdicts_ThinkingEntryNeverInVerdictsPayload(t *testing.T) {
	api := newTestRestAPIWithAgent(t)
	shared := api.agentLoop.GetSessionStore()
	require.NotNil(t, shared)

	meta, err := shared.NewSession(session.SessionTypeTask, "", "01JXTESTAGENTSTARTTEST001")
	require.NoError(t, err)
	taskID := seedTaskWithVerdict(t, api, shared, meta.ID, "01JXTESTAGENTSTARTTEST001")

	// A thinking entry rides the same transcript.
	require.NoError(t, shared.AppendTranscriptStrict(meta.ID, session.TranscriptEntry{
		ID:           "wpc-think-1",
		Type:         session.EntryTypeThinking,
		ThinkingText: "task-verdicts probe " + wpcGatewayThinkingSentinel,
		Timestamp:    time.Now().UTC(),
	}))

	rec := httptest.NewRecorder()
	api.handleTaskVerdicts(rec, taskID)
	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

	body := rec.Body.String()
	assert.Contains(t, body, "verdict-1", "instrument check: the verdict itself is returned")
	assert.NotContains(t, body, wpcGatewayThinkingSentinel,
		"the verdicts payload never carries thinking text (section 8.1 MIN-002)")
}
