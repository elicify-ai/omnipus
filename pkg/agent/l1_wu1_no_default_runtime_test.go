//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func l1WU1Runtime(t *testing.T) (*AgentLoop, *testutil.ScenarioProvider) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCore}, {ID: "jim", Type: config.AgentTypeCore},
		{ID: "worker", Type: config.AgentTypeWorker},
	}
	path := filepath.Join(home, "workspaces", "sales.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"id":"sales","name":"Sales","status":"active","core_team":["mia"]}`), 0o600))
	provider := testutil.NewScenario().WithText("named owner control")
	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	require.NoError(t, err)
	t.Cleanup(func() { al.Close() })
	return al, provider
}

func TestL1WU1InboundChannel_UnboundAndInvalidOwnerRefuseLoudlyWithoutAnyTurn(t *testing.T) {
	// FR-121 / D3 / BDD-13.1/13.2/13.12. Real processMessage dispatch.
	for _, row := range []struct {
		name, workspace, agent, reason string
		valid                          bool
	}{
		{"bound_positive_control", "sales", "mia", "", true},
		{"unbound_empty", "", "", "bind this channel to an agent in a workspace", false},
		{"unbound_legacy_named", "", "mia", "bind this channel to an agent in a workspace", false},
		{"deleted_owner", "sales", "deleted", "bound agent deleted not found", false},
		{"worker_owner", "sales", "worker", "not a chat target", false},
		{"nonmember_owner", "sales", "jim", "jim is not in Sales", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			al, provider := l1WU1Runtime(t)
			inst := config.ChannelInstanceConfig{Type: "telegram", Enabled: true, WorkspaceID: row.workspace}
			if row.agent != "" {
				inst.Identity = &config.ChannelIdentity{Kind: "agent", ID: row.agent}
			}
			al.GetConfig().Channels = map[string]config.ChannelInstanceConfig{"telegram.red": inst}
			readAudit := swapAuditLogger(t, al)
			require.NoError(t, al.auditLogger.Log(&audit.Entry{Event: audit.EventChannelRoutingChanged, AgentID: "instrument-control"}))
			logPath := filepath.Join(t.TempDir(), "routing-log.jsonl")
			require.NoError(t, logger.EnableFileLogging(logPath))
			t.Cleanup(logger.DisableFileLogging)
			logger.ErrorC("l1-test-instrument", "known-log-control")
			msg := bus.InboundMessage{Channel: "telegram", InstanceID: "telegram.red", ChatID: "person-red", Content: "named owner control", UserInitiated: true}
			answer, owner, err := al.processMessage(context.Background(), msg)
			if row.valid {
				require.NoError(t, err, "bound control must actually execute")
				require.NotNil(t, owner)
				assert.Equal(t, "mia", owner.ID)
				assert.Equal(t, "named owner control", answer)
				assert.NotEmpty(t, provider.AllRequests(), "bound control reached real turn/provider edge")
				readAudit() // close this real audit sink after its control turn
			} else {
				assert.Error(t, err, "FR-121: unroutable traffic must not execute: answer=%q owner=%v", answer, owner)
				if err != nil {
					assert.Contains(t, err.Error(), row.reason, "loud drop must name the actual binding defect")
				}
				assert.Nil(t, owner, "no substitute owner may be returned")
				assert.Empty(t, provider.AllRequests(), "no agent may see the dropped message")
				rows := readAudit()
				controls, drops := 0, 0
				for _, record := range rows {
					if record["agent_id"] == "instrument-control" {
						controls++
						continue
					}
					encoded, encodeErr := json.Marshal(record)
					require.NoError(t, encodeErr)
					if strings.Contains(string(encoded), row.reason) {
						drops++
					}
				}
				assert.Equal(t, 1, controls, "the audit capture must see its known write")
				assert.Equal(t, 1, drops, "FR-121: exactly one audit entry must name this dropped input's reason")
				logger.DisableFileLogging()
				logs, readErr := os.ReadFile(logPath)
				require.NoError(t, readErr)
				assert.Contains(t, string(logs), "known-log-control", "the log capture must be active")
				foundError := false
				for _, line := range strings.Split(string(logs), "\n") {
					if strings.TrimSpace(line) == "" {
						continue
					}
					var event map[string]any
					require.NoError(t, json.Unmarshal([]byte(line), &event))
					if event["level"] == "error" && strings.Contains(line, row.reason) {
						foundError = true
					}
				}
				assert.True(t, foundError, "FR-121: drop reason needs an ERROR-level log, not only a warning/return value")
			}
		})
	}
}

func TestL1WU1CanonicalOwner_GoalAndSteeringNeverUseSurvivingParticipant(t *testing.T) {
	// FR-123 / BDD-13.4: Jim participates, but that does not make him owner.
	al, _ := l1WU1Runtime(t)
	t.Run("named_goal_owner_positive_control", func(t *testing.T) {
		inst := resolveGoalAgent(al, &session.UnifiedMeta{SessionMeta: session.SessionMeta{AgentID: "mia", AgentIDs: []string{"jim"}}})
		require.NotNil(t, inst)
		assert.Equal(t, "mia", inst.ID)
	})
	for _, owner := range []string{"deleted", ""} {
		t.Run("goal_"+owner, func(t *testing.T) {
			inst := resolveGoalAgent(al, &session.UnifiedMeta{SessionMeta: session.SessionMeta{AgentID: owner, AgentIDs: []string{"jim"}}})
			assert.Nil(t, inst, "FR-123: missing canonical owner cannot become participant Jim/default")
		})
	}
	t.Run("steering_missing_owner", func(t *testing.T) {
		assert.Nil(t, al.agentForSession("agent:deleted:session:stale-owner"), "FR-123: stale steering key cannot resolve to a default agent")
	})
	t.Run("steering_named_owner_positive_control", func(t *testing.T) {
		inst := al.agentForSession("agent:mia:session:named-owner")
		require.NotNil(t, inst)
		assert.Equal(t, "mia", inst.ID)
	})
	t.Run("task_missing_owner_pre_dispatch_failure", func(t *testing.T) {
		loop, provider := l1WU1Runtime(t)
		meta, err := loop.GetSessionStore().NewSession(session.SessionTypeTask, "webchat", "deleted")
		require.NoError(t, err)
		_, err = loop.processTaskDirect(context.Background(), "deleted", "never hand this to Jim", "agent:deleted:session:"+meta.ID, meta.ID)
		assert.ErrorIs(t, err, ErrTaskRunNotDispatched, "FR-123: missing canonical task owner is a pre-dispatch failure, not another agent's turn error")
		if err != nil {
			assert.Contains(t, err.Error(), "deleted", "visible failure must name the missing owner")
		}
		assert.Empty(t, provider.AllRequests(), "zero model calls before missing-owner refusal")
	})
}

func TestL1WU1SystemMessage_NoAddressableOriginIsDiscardedNotDefaulted(t *testing.T) {
	// FR-131 / BDD-13.9 and the explicit-origin positive control.
	al, provider := l1WU1Runtime(t)
	t.Run("named_origin_positive_control", func(t *testing.T) {
		inst, dropped, err := al.resolveSystemMessageAgent(bus.InboundMessage{AsyncOriginAgentID: "mia"})
		require.NoError(t, err)
		assert.False(t, dropped)
		require.NotNil(t, inst)
		assert.Equal(t, "mia", inst.ID)
	})
	inst, discarded, err := al.resolveSystemMessageAgent(bus.InboundMessage{Channel: "system", Content: "must not reach another agent"})
	assert.Nil(t, inst, "FR-131: a system message without an origin cannot borrow the default")
	assert.True(t, discarded, "FR-131: missing origin is an explicit discard")
	if err != nil {
		assert.Contains(t, err.Error(), "system message without an addressable agent")
	}
	readAudit := swapAuditLogger(t, al)
	require.NoError(t, al.auditLogger.Log(&audit.Entry{Event: audit.EventChannelRoutingChanged, AgentID: "instrument-control"}))
	logPath := filepath.Join(t.TempDir(), "system-log.jsonl")
	require.NoError(t, logger.EnableFileLogging(logPath))
	t.Cleanup(logger.DisableFileLogging)
	logger.ErrorC("l1-test-instrument", "known-system-log-control")
	answer, dispatchErr := al.processSystemMessage(context.Background(), bus.InboundMessage{Channel: "system", ChatID: "webchat:operator", Content: "must be discarded without origin"})
	assert.Empty(t, answer, "discarded unaddressed system input must produce no agent answer")
	if dispatchErr != nil {
		assert.Contains(t, dispatchErr.Error(), "system message without an addressable agent", "any returned refusal must name the missing origin")
	}
	rows := readAudit()
	controls, missingOrigin := 0, 0
	for _, record := range rows {
		if record["agent_id"] == "instrument-control" {
			controls++
			continue
		}
		encoded, encodeErr := json.Marshal(record)
		require.NoError(t, encodeErr)
		if strings.Contains(string(encoded), "system message without an addressable agent") {
			missingOrigin++
		}
	}
	assert.Equal(t, 1, controls, "audit instrument positive control")
	assert.Equal(t, 1, missingOrigin, "FR-131: missing origin discard must be audited exactly once")
	logger.DisableFileLogging()
	logs, logErr := os.ReadFile(logPath)
	require.NoError(t, logErr)
	assert.Contains(t, string(logs), "known-system-log-control")
	foundError := false
	for _, line := range strings.Split(string(logs), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		if event["level"] == "error" && strings.Contains(line, "system message without an addressable agent") {
			foundError = true
		}
	}
	assert.True(t, foundError, "FR-131: missing origin must be logged at ERROR level")
	assert.Empty(t, provider.AllRequests(), "FR-131: no agent/model may receive the discarded system input")
}

func TestL1WU1InternalMCPRoot_InstallRootNotAnyAgentHome(t *testing.T) {
	// FR-124 / §6.4. The instance home differs from the install root.
	al, _ := l1WU1Runtime(t)
	want := al.GetConfig().AgentHomeBasePath()
	mia, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok)
	require.NotEqual(t, want, mia.Home, "instrument control: old default-agent selection must be distinguishable")
	assert.Equal(t, want, al.MCPWorkspacePath(), "FR-124: install-wide connector work directory is agent-independent")
}

func TestL1WU1NilAgentReply_StillPublishesWithoutBorrowingSentTracker(t *testing.T) {
	// FR-132 / BDD-13.10. Mark ALL agents already-sent using their REAL
	// send_message tool; nil-agent error publishing must still reach the bus.
	al, _ := l1WU1Runtime(t)
	for _, id := range []string{"mia", "jim", "worker"} {
		inst, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok)
		raw, ok := inst.Tools.Get("send_message")
		require.True(t, ok)
		message, ok := raw.(*tools.MessageTool)
		require.True(t, ok)
		message.SetSendCallback(func(string, string, string, tools.SendOrigin) error { return nil })
		ctx := tools.WithAgentID(context.Background(), id)
		ctx = tools.WithToolContext(ctx, "webchat", "positive-control")
		result := message.Execute(ctx, map[string]any{"content": "sent control"})
		require.False(t, result.IsError, "real message positive control: %s", result.ForLLM)
		require.True(t, message.HasSentInRound(), "send tracker must really be true")
	}
	al.publishResponseIfNeeded(context.Background(), nil, "webchat", "unrouted", "No agent is bound to this connection.", "unrouted-session")
	select {
	case got := <-al.bus.OutboundChan():
		assert.Equal(t, bus.OutboundMessage{Channel: "webchat", ChatID: "unrouted", Content: "No agent is bound to this connection.", SessionID: "unrouted-session"}, got)
	default:
		t.Fatal("FR-132: nil-agent reply was suppressed by another agent's already-sent tracker")
	}
}
