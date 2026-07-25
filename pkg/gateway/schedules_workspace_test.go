//go:build !cgo

// schedules_workspace_test.go — regression coverage for the scheduled/
// heartbeat workspace-threading gap: ProcessScheduled (pkg/agent/loop.go)
// has always read transcriptStore.GetMeta(sessionID).WorkspaceID (its FIX-1
// comment), but nothing upstream of it ever wrote that field for a
// cron/heartbeat-fired session, so bus.OutboundMediaMessage.WorkspaceID
// stayed "" for every scheduled/heartbeat tool-media send even when the
// schedule's channel (or the heartbeat's workspace) was known.
//
// This file proves the write side: resolveScheduleWorkspaceID resolves the
// right workspace from either source, and scheduledRunner.pickSession
// (exercised through the public RunScheduled entry point) stamps it onto the
// session BEFORE the executor runs. The companion read-side proof — that a
// stamped session's WorkspaceID actually reaches
// bus.OutboundMediaMessage.WorkspaceID through a real tool-media delivery —
// lives in pkg/agent (TestProcessScheduled_MediaToolDelivery_StampsWorkspaceID),
// since the fixtures for a real tool + fake channel are private to that
// package. Together the two prove the gap is closed end-to-end.

package gateway

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// --- resolveScheduleWorkspaceID: pure-function coverage ---------------------

// TestResolveScheduleWorkspaceID_ChannelBinding asserts an operator-created
// schedule resolves its workspace from the channel instance its
// payload.Channel names, mirroring processMessage's
// cfg.Channels[instanceID].WorkspaceID lookup.
func TestResolveScheduleWorkspaceID_ChannelBinding(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram.sales": {Type: "telegram", Enabled: true, WorkspaceID: "sales"},
		},
	}
	job := cron.CronJob{
		Payload: cron.CronPayload{Channel: "telegram.sales", To: "chat-1"},
	}
	assert.Equal(t, "sales", resolveScheduleWorkspaceID(cfg, job))
}

// TestResolveScheduleWorkspaceID_ChannelBinding_CaseInsensitive asserts the
// channel lookup normalizes case/whitespace the same way inboundInstanceID
// does for the interactive path, so a schedule created with a
// differently-cased channel value still resolves.
func TestResolveScheduleWorkspaceID_ChannelBinding_CaseInsensitive(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram.sales": {Type: "telegram", Enabled: true, WorkspaceID: "sales"},
		},
	}
	job := cron.CronJob{
		Payload: cron.CronPayload{Channel: "  Telegram.Sales  "},
	}
	assert.Equal(t, "sales", resolveScheduleWorkspaceID(cfg, job))
}

// TestResolveScheduleWorkspaceID_UnboundChannel_ReturnsEmpty asserts a
// schedule targeting a channel instance with no workspace binding resolves
// to "" — never fabricated.
func TestResolveScheduleWorkspaceID_UnboundChannel_ReturnsEmpty(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {Type: "telegram", Enabled: true}, // no WorkspaceID
		},
	}
	job := cron.CronJob{Payload: cron.CronPayload{Channel: "telegram"}}
	assert.Equal(t, "", resolveScheduleWorkspaceID(cfg, job))
}

// TestResolveScheduleWorkspaceID_NoChannel_ReturnsEmpty asserts a schedule
// with no channel context at all (e.g. deliver=false, no outbound context)
// resolves to "" rather than guessing.
func TestResolveScheduleWorkspaceID_NoChannel_ReturnsEmpty(t *testing.T) {
	cfg := baseConfig()
	job := cron.CronJob{Payload: cron.CronPayload{Message: "do the thing"}}
	assert.Equal(t, "", resolveScheduleWorkspaceID(cfg, job))
}

// TestResolveScheduleWorkspaceID_HeartbeatJobName asserts a heartbeat job
// resolves its workspace from its deterministic name
// ("heartbeat:<workspaceID>:<agentID>") — the source that exists precisely
// because a heartbeat job IS a single (workspace, agent) pair by
// construction (ADR-027), unlike a plain schedule's AgentID which can be
// ambiguous across multiple workspaces.
func TestResolveScheduleWorkspaceID_HeartbeatJobName(t *testing.T) {
	job := cron.CronJob{
		Name:    heartbeatJobName("acme-corp", "mia"),
		AgentID: "mia",
		Payload: cron.CronPayload{Kind: heartbeatJobKind, Message: "heartbeat body"},
	}
	assert.Equal(t, "acme-corp", resolveScheduleWorkspaceID(nil, job))
}

// TestResolveScheduleWorkspaceID_HeartbeatJobName_AgentIDWithColons proves
// the parse is robust to a workspace id containing colons (not just an
// agent id) by relying on the KNOWN AgentID suffix rather than a positional
// split — an agent id that happens to embed a colon must not corrupt the
// extracted workspace id.
func TestResolveScheduleWorkspaceID_HeartbeatJobName_AgentIDWithColons(t *testing.T) {
	job := cron.CronJob{
		Name:    heartbeatJobName("team:sales:eu", "mia"),
		AgentID: "mia",
		Payload: cron.CronPayload{Kind: heartbeatJobKind},
	}
	assert.Equal(t, "team:sales:eu", resolveScheduleWorkspaceID(nil, job))
}

// TestResolveScheduleWorkspaceID_HeartbeatKindButNameMismatch_ReturnsEmpty
// asserts a defensive parse: if the job is heartbeat-kind but the name does
// not carry the AgentID suffix the reconciler would have stamped (e.g.
// hand-edited store data, or a job whose AgentID drifted after the name was
// set), the parse refuses to guess rather than returning a wrong workspace.
func TestResolveScheduleWorkspaceID_HeartbeatKindButNameMismatch_ReturnsEmpty(t *testing.T) {
	job := cron.CronJob{
		Name:    "heartbeat:acme-corp:mia",
		AgentID: "someone-else",
		Payload: cron.CronPayload{Kind: heartbeatJobKind},
	}
	assert.Equal(t, "", resolveScheduleWorkspaceID(nil, job))
}

// TestResolveScheduleWorkspaceID_HeartbeatKind_PrefersNameOverChannel
// asserts a heartbeat job's workspace comes from its name even when (in
// principle) a Channel field were also set — heartbeats never set
// Payload.Channel in production (heartbeat_schedule.go's buildHeartbeatMessage
// path), but the resolution order must not accidentally prefer a stale/
// irrelevant channel binding over the authoritative heartbeat identity.
func TestResolveScheduleWorkspaceID_HeartbeatKind_PrefersNameOverChannel(t *testing.T) {
	cfg := &config.Config{
		Channels: map[string]config.ChannelInstanceConfig{
			"telegram": {Type: "telegram", Enabled: true, WorkspaceID: "wrong-workspace"},
		},
	}
	job := cron.CronJob{
		Name:    heartbeatJobName("acme-corp", "mia"),
		AgentID: "mia",
		Payload: cron.CronPayload{Kind: heartbeatJobKind, Channel: "telegram"},
	}
	assert.Equal(t, "acme-corp", resolveScheduleWorkspaceID(cfg, job))
}

// --- RunScheduled end-to-end: the session actually gets stamped -------------

// TestRunScheduled_ChannelBoundSchedule_StampsSessionWorkspace drives a real
// RunScheduled call for an operator-created, channel-bound schedule and
// asserts the picked session's meta.WorkspaceID carries the bound workspace
// afterward — the precondition ProcessScheduled's existing GetMeta read
// depends on.
func TestRunScheduled_ChannelBoundSchedule_StampsSessionWorkspace(t *testing.T) {
	cfg := baseConfig()
	cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram.sales": {Type: "telegram", Enabled: true, WorkspaceID: "sales"},
	}
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "j-ws-1", Name: "daily report", AgentID: "mia", SessionMode: cron.SessionModeIsolated,
		Payload: cron.CronPayload{Message: "run", Channel: "telegram.sales", To: "chat-1"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	require.NotEmpty(t, sid)
	require.Len(t, exec.calls, 1)

	meta, err := exec.store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "sales", meta.WorkspaceID,
		"the session RunScheduled picked must carry the schedule's bound-channel workspace")
}

// TestRunScheduled_HeartbeatJob_StampsSessionWorkspace mirrors the above for
// a workspace-scoped heartbeat job (name-encoded workspace, no
// Payload.Channel) in continue mode — the mode every real heartbeat job uses
// (heartbeat_schedule.go always sets SessionMode: cron.SessionModeContinue).
func TestRunScheduled_HeartbeatJob_StampsSessionWorkspace(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "j-hb-1", Name: heartbeatJobName("acme-corp", "mia"), AgentID: "mia",
		SessionMode: cron.SessionModeContinue,
		Payload:     cron.CronPayload{Kind: heartbeatJobKind, Message: "heartbeat body"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	require.NotEmpty(t, sid)
	require.Len(t, exec.calls, 1)

	meta, err := exec.store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "acme-corp", meta.WorkspaceID,
		"the heartbeat's standing session must carry the workspace encoded in the job's name")

	// A second fire (continue mode reuses the same session id) must not
	// re-derive or clobber the tag — it stays stamped from the first run.
	sid2, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	assert.Equal(t, sid, sid2, "continue mode must reuse the same session id")
	meta2, err := exec.store.GetMeta(sid2)
	require.NoError(t, err)
	assert.Equal(t, "acme-corp", meta2.WorkspaceID)
}

// TestRunScheduled_UnresolvableWorkspace_LeavesSessionUnstamped is the
// negative case: a schedule with no channel and no heartbeat identity must
// not have any workspace fabricated onto its session.
func TestRunScheduled_UnresolvableWorkspace_LeavesSessionUnstamped(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "j-none-1", Name: "no-channel", AgentID: "mia", SessionMode: cron.SessionModeIsolated,
		Payload: cron.CronPayload{Message: "run"}, // no Channel, not a heartbeat
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	require.NotEmpty(t, sid)

	meta, err := exec.store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "", meta.WorkspaceID,
		"a schedule with no resolvable workspace context must not stamp a fabricated one")
}

// TestStampScheduledSessionWorkspace_DoesNotClobberExisting asserts the
// stamp helper never overwrites an already-tagged session, mirroring
// stampScheduledSessionOwner's identical non-clobber rule for Owner.
func TestStampScheduledSessionWorkspace_DoesNotClobberExisting(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	meta, err := exec.store.NewScheduledSession("mia")
	require.NoError(t, err)
	original := "original-workspace"
	require.NoError(t, exec.store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &original}))

	r.stampScheduledSessionWorkspace(exec.store, meta.ID, "different-workspace")

	got, err := exec.store.GetMeta(meta.ID)
	require.NoError(t, err)
	assert.Equal(t, "original-workspace", got.WorkspaceID,
		"an already-stamped session's workspace must not be overwritten by a later resolution")
}
