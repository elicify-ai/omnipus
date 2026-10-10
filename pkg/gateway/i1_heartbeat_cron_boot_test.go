package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/cron"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only the paid provider is controlled; cron, the gateway runner, boot recovery,
// session selection and ordinary admission all remain real.
type i1HeartbeatCronProvider struct {
	mu      sync.Mutex
	store   *session.LifecycleStore
	root    string
	records []*session.LifecycleRecord
}

func (p *i1HeartbeatCronProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	rec, err := p.store.Load(p.root)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.records = append(p.records, rec)
	p.mu.Unlock()
	return &providers.LLMResponse{Content: "The normal heartbeat continued."}, nil
}

func (*i1HeartbeatCronProvider) GetDefaultModel() string { return "i1-heartbeat-cron" }

func (p *i1HeartbeatCronProvider) observations() []*session.LifecycleRecord {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*session.LifecycleRecord(nil), p.records...)
}

func TestI1HeartbeatPhysicalBootRealCron(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{Home: home,
		DefaultModel: config.DefaultModel{Model: "i1-heartbeat-cron"}}, List: []config.AgentConfig{{ID: "mia", Home: home}}},
		Sandbox: config.OmnipusSandboxConfig{Mode: config.SandboxModeOff}}
	ensureTestWorkspaceMembership(t, cfg)
	ls := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	provider := &i1HeartbeatCronProvider{store: ls}
	msgBus := bus.NewMessageBus()
	al, err := agent.NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err)
	t.Cleanup(func() { al.Close(); msgBus.Close(); gatewaySteerCancellers.Delete(al) })
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")), ls)
	meta, err := al.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, "mia")
	require.NoError(t, err)
	provider.root = meta.ID
	priorBoot := session.NewBootEpochStore(home)
	oldEpoch, err := priorBoot.Mint()
	require.NoError(t, err)
	require.NoError(t, ls.Persist(&session.LifecycleRecord{SessionID: meta.ID, Generation: 2,
		State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		AgentID: "mia", WorkspaceID: testHarnessWorkspaceMembershipID,
		Origin:      &session.Origin{Kind: session.OriginKindHeartbeat},
		ExecutionID: &session.ExecutionIdentity{RunID: "i1-killed-heartbeat", BootSeq: oldEpoch}}))
	ws, err := readWorkspaceFile(home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	ws.MemberConfigs = buildMemberConfigs("mia", true, 5, "Continue the normal heartbeat.")
	require.NoError(t, writeWorkspaceFile(home, ws))
	interval, last := int64(300000), time.Now().Add(-10*time.Minute).UnixMilli()
	job := cron.CronJob{ID: "i1-crash-heartbeat-job", Name: heartbeatJobName(ws.ID, "mia"), Enabled: true,
		AgentID: "mia", SessionMode: cron.SessionModeMain,
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &interval},
		Payload:  cron.CronPayload{Kind: heartbeatJobKind, Message: "Continue the normal heartbeat."},
		State: cron.CronJobState{Running: true, LastRunAtMS: &last, LastStatus: "ok",
			History: []cron.CronRunRecord{{RanAtMs: last, Status: "ok", SessionID: meta.ID}}}}
	path := filepath.Join(home, "cron", "jobs.json")
	raw, err := json.Marshal(cron.CronStore{Version: 1, Jobs: []cron.CronJob{job}})
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	stg := &setupAndStartServicesState{ctx: context.Background(), cfg: cfg, homePath: home,
		agentLoop: al, msgBus: msgBus, lifecycleStore: ls}
	_, stop, startErr := stg.startSchedulers() // EXACT physical-boot caller, not a direct ProcessScheduled call.
	require.NoError(t, startErr)
	require.False(t, stop)
	services := stg.runningServices
	require.NotNil(t, services)
	t.Cleanup(func() { stopAndCleanupServices(services, 5*time.Second, true) })
	require.NoError(t, stg.mintBootEpoch())
	stg.wireSteerDeps()
	require.NoError(t, services.SteerDeps.BootHook(context.Background()))
	bootRecord, err := ls.Load(meta.ID)
	require.NoError(t, err)
	require.Equal(t, session.LifecycleStopped, bootRecord.State)
	require.Equal(t, session.StopCauseRestart, bootRecord.StopNote.Cause)
	require.Empty(t, provider.observations(), "physical boot must never dispatch/replay the cut turn")
	stored, found := services.CronService.GetJob(job.ID)
	require.True(t, found)
	assert.Equal(t, job.ID, stored.ID)
	assert.Equal(t, job.SessionID, stored.SessionID)
	assert.Equal(t, job.Schedule, stored.Schedule)
	assert.Equal(t, job.State.History, stored.State.History)
	require.NotNil(t, stored.State.NextRunAtMS)
	services.CronService.RunDueJobs(time.UnixMilli(*stored.State.NextRunAtMS))
	services.CronService.WaitForLane()
	observed := provider.observations()
	require.Len(t, observed, 1, "the real due cron tick must reach gateway RunScheduled and AgentLoop.ProcessScheduled")
	assert.Equal(t, meta.ID, observed[0].SessionID)
	assert.Equal(t, bootRecord.Generation, observed[0].Generation)
	require.NotNil(t, observed[0].ExecutionID)
	assert.NotEqual(t, bootRecord.ExecutionID.RunID, observed[0].ExecutionID.RunID)
	assert.Equal(t, stg.bootEpoch.Current(), observed[0].ExecutionID.BootSeq)
	after, found := services.CronService.GetJob(job.ID)
	require.True(t, found)
	assert.False(t, after.State.Running)
	assert.Equal(t, meta.ID, after.SessionID)
	assert.Equal(t, "ok", after.State.LastStatus)
	assert.Len(t, after.State.History, 2)
}
