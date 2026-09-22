package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// TestListJobs_SubagentLastActivityAdvancesWithTranscript proves the production
// list_jobs wiring reads a delegated child's actual transcript activity rather
// than freezing at the lifecycle transition that started the job.
func TestListJobs_SubagentLastActivityAdvancesWithTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         filepath.Join(home, "agents"),
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Mia", Home: filepath.Join(home, "agents", "mia")},
				{ID: "worker", Name: "Worker", Home: filepath.Join(home, "agents", "worker")},
			},
		},
	}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(func() { al.Close() })

	lifecycle := session.NewLifecycleStore(filepath.Join(home, "lifecycle"))
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "inbox")), lifecycle)

	transcripts := al.GetSessionStore()
	require.NotNil(t, transcripts)
	parent, err := transcripts.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	const childID = "delegate-active-child"
	_, err = transcripts.CreateSessionWithID(childID, parent.ID, session.SessionTypeDelegate, "web", "worker")
	require.NoError(t, err)

	rec := &session.LifecycleRecord{
		SessionID:      childID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    "workspace-1",
		AgentID:        "worker",
		ParentAgentID:  "mia",
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parent.ID},
	}
	require.NoError(t, lifecycle.Persist(rec))
	lifecycleAt := rec.UpdatedAt

	activityAt := lifecycleAt.Add(2 * time.Minute)
	require.NoError(t, transcripts.AppendTranscriptStrict(childID, session.TranscriptEntry{
		ID:        "tool-result-1",
		Role:      "assistant",
		Content:   "completed another tool step",
		Timestamp: activityAt,
		AgentID:   "worker",
	}))
	meta, err := transcripts.GetMeta(childID)
	require.NoError(t, err)
	require.Equal(t, activityAt, meta.UpdatedAt, "fixture must prove the child transcript advanced")

	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	result := inst.Tools.Execute(
		tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "mia"), "workspace-1"),
		"list_jobs",
		map[string]any{"kind": "subagent"},
	)
	require.NotNil(t, result)
	require.False(t, result.IsError, result.ForLLM)

	type listJobsPayload struct {
		Rows []struct {
			ID             string `json:"id"`
			LastActivityAt string `json:"last_activity_at"`
		} `json:"rows"`
		Notes *struct {
			Errors []struct {
				Kind    string `json:"kind"`
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"notes"`
	}
	var payload listJobsPayload
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &payload))
	require.Len(t, payload.Rows, 1)
	require.Equal(t, childID, payload.Rows[0].ID)
	require.Equal(t, activityAt.Format(time.RFC3339), payload.Rows[0].LastActivityAt,
		"list_jobs must advance to the child's latest transcript record, not remain at lifecycle start")

	// A nonterminal lifecycle can outlive or lack its session metadata.
	// Keep the fallback row, but never present its lifecycle timestamp as if
	// live transcript activity had been read successfully.
	missing := &session.LifecycleRecord{
		SessionID:      "delegate-missing-session",
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    "workspace-1",
		AgentID:        "worker",
		ParentAgentID:  "mia",
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parent.ID},
	}
	require.NoError(t, lifecycle.Persist(missing))
	const olderActivityID = "delegate-older-session-activity"
	_, err = transcripts.CreateSessionWithID(
		olderActivityID,
		parent.ID,
		session.SessionTypeDelegate,
		"web",
		"worker",
	)
	require.NoError(t, err)
	olderActivityAt := lifecycleAt.Add(-time.Minute)
	require.NoError(t, transcripts.AppendTranscriptStrict(olderActivityID, session.TranscriptEntry{
		ID:        "older-activity",
		Role:      "assistant",
		Content:   "must not move telemetry backward",
		Timestamp: olderActivityAt,
		AgentID:   "worker",
	}))
	olderLifecycle := &session.LifecycleRecord{
		SessionID:      olderActivityID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    "workspace-1",
		AgentID:        "worker",
		ParentAgentID:  "mia",
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parent.ID},
	}
	require.NoError(t, lifecycle.Persist(olderLifecycle))
	const mismatchedID = "delegate-wrong-owner"
	_, err = transcripts.CreateSessionWithID(
		mismatchedID,
		parent.ID,
		session.SessionTypeDelegate,
		"web",
		"mia",
	)
	require.NoError(t, err)
	require.NoError(t, transcripts.AppendTranscriptStrict(mismatchedID, session.TranscriptEntry{
		ID:        "wrong-owner-activity",
		Role:      "assistant",
		Content:   "must not become worker telemetry",
		Timestamp: activityAt.Add(time.Minute),
		AgentID:   "mia",
	}))
	mismatchedLifecycle := &session.LifecycleRecord{
		SessionID:      mismatchedID,
		Generation:     1,
		State:          session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID:    "workspace-1",
		AgentID:        "worker",
		ParentAgentID:  "mia",
		SteeredBy:      &session.SteeredBy{SteeringSessionID: parent.ID},
	}
	require.NoError(t, lifecycle.Persist(mismatchedLifecycle))

	result = inst.Tools.Execute(
		tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "mia"), "workspace-1"),
		"list_jobs",
		map[string]any{"kind": "subagent"},
	)
	require.NotNil(t, result)
	require.False(t, result.IsError, result.ForLLM)
	payload = listJobsPayload{}
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &payload))
	require.Len(t, payload.Rows, 4, "degraded nonterminal rows must remain visible")
	require.NotNil(t, payload.Notes, "activity-read degradation must not look nominal")
	require.Len(t, payload.Notes.Errors, 1)
	require.Equal(t, "subagent", payload.Notes.Errors[0].Kind)
	require.Contains(t, payload.Notes.Errors[0].Message, "activity unavailable for 2 nonterminal job")
	activityByID := make(map[string]string, len(payload.Rows))
	for _, row := range payload.Rows {
		activityByID[row.ID] = row.LastActivityAt
	}
	require.Equal(t, missing.UpdatedAt.Format(time.RFC3339), activityByID[missing.SessionID])
	require.Equal(t, mismatchedLifecycle.UpdatedAt.Format(time.RFC3339), activityByID[mismatchedID])
	require.NotEqual(t, activityAt.Add(time.Minute).Format(time.RFC3339), activityByID[mismatchedID],
		"another agent's session timestamp must not enter this lifecycle row")
	require.Equal(t, olderLifecycle.UpdatedAt.Format(time.RFC3339), activityByID[olderActivityID],
		"session recency older than the lifecycle transition must not move telemetry backward")
}
