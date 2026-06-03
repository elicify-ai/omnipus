//go:build !cgo

// Scheduled-runner integration tests (#264): owner pinning (no default
// fallback), deliver=true direct send, per-mode session selection, and the
// failure → coalesced notification + channel alert path. Deterministic: a fake
// executor stands in for the agent loop; an in-memory bus captures outbound.

package gateway

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/cron"
	"github.com/dapicom-ai/omnipus/pkg/notifications"
	"github.com/dapicom-ai/omnipus/pkg/session"
)

// fakeExecutor implements scheduledExecutor for the runner tests.
type fakeExecutor struct {
	store   *session.UnifiedStore
	cfg     *config.Config
	calls   []fakeCall
	reply   string
	err     error
	emitted []agent.NotificationPayload
	// hangUntilCtxDone makes ProcessScheduled block until ctx is canceled,
	// returning ctx.Err(). Used by the deadline/timeout test.
	hangUntilCtxDone bool
}

type fakeCall struct {
	owner     string
	sessionID string
	content   string
}

func (f *fakeExecutor) ProcessScheduled(ctx context.Context, owner, sessionID, content, _, _ string) (string, error) {
	f.calls = append(f.calls, fakeCall{owner: owner, sessionID: sessionID, content: content})
	if f.hangUntilCtxDone {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return f.reply, f.err
}
func (f *fakeExecutor) GetSessionStore() *session.UnifiedStore { return f.store }
func (f *fakeExecutor) GetConfig() *config.Config              { return f.cfg }
func (f *fakeExecutor) EmitNotification(p agent.NotificationPayload) {
	f.emitted = append(f.emitted, p)
}

type fakeChecker struct{ registered map[string]bool }

func (c fakeChecker) IsRegistered(id string) bool { return c.registered[id] }

// fakeCanceller records RequestCancel calls (for the timeout/force-abort test).
type fakeCanceller struct {
	mu       sync.Mutex
	sessions []string
}

func (c *fakeCanceller) RequestCancel(
	_ context.Context,
	scope agent.CancelScope,
	_ agent.CancelCanceller,
	_ agent.CancelHooks,
) (agent.CancelOutcome, error) {
	c.mu.Lock()
	c.sessions = append(c.sessions, scope.SessionID)
	c.mu.Unlock()
	return agent.CancelOutcome{Fired: true}, nil
}

func (c *fakeCanceller) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.sessions))
	copy(out, c.sessions)
	return out
}

func newRunnerHarness(
	t *testing.T,
	cfg *config.Config,
	registered map[string]bool,
) (*scheduledRunner, *fakeExecutor, *bus.MessageBus, *notifications.Store) {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	exec := &fakeExecutor{store: store, cfg: cfg, reply: "ok"}
	msgBus := bus.NewMessageBus()
	notifs := notifications.NewStore(t.TempDir())
	r := newScheduledRunner(exec, fakeChecker{registered: registered}, msgBus, notifs, exec.GetConfig)
	return r, exec, msgBus, notifs
}

// newRunnerOnly returns just the runner for tests that don't inspect the
// executor/bus/store (avoids the triple-blank-identifier lint).
func newRunnerOnly(t *testing.T, cfg *config.Config, registered map[string]bool) *scheduledRunner {
	t.Helper()
	r, _, _, _ := newRunnerHarness(t, cfg, registered) //nolint:dogsled // single accessor for the runner
	return r
}

func baseConfig() *config.Config {
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Type: config.AgentTypeCustom, OwnerUsername: "alice"},
	}
	return cfg
}

// drainOutbound reads one outbound message or fails.
func drainOutbound(t *testing.T, mb *bus.MessageBus) bus.OutboundMessage {
	t.Helper()
	select {
	case m := <-mb.OutboundChan():
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for outbound message")
		return bus.OutboundMessage{}
	}
}

// TestRunner_MissingOwner_NoFallback asserts an unregistered owner is a failure
// and the executor is never invoked (FR-001).
func TestRunner_MissingOwner_NoFallback(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{}) // nobody registered

	job := &cron.CronJob{ID: "j1", Name: "daily", AgentID: "mia", CreatedBy: "alice"}
	sid, err := r.RunScheduled(context.Background(), job)
	assert.Error(t, err)
	assert.Empty(t, sid)
	assert.Empty(t, exec.calls, "executor must not run for a missing owner")
}

// TestRunner_DeliverTrue_DirectSend asserts deliver=true sends straight to the
// channel with no agent turn (FR-014).
func TestRunner_DeliverTrue_DirectSend(t *testing.T) {
	cfg := baseConfig()
	r, exec, mb, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "j2", Name: "ping", AgentID: "mia",
		Payload: cron.CronPayload{Message: "hello", Deliver: true, Channel: "telegram", To: "chat-1"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	assert.Empty(t, sid)
	assert.Empty(t, exec.calls, "deliver=true must not run an agent turn")

	m := drainOutbound(t, mb)
	assert.Equal(t, "telegram", m.Channel)
	assert.Equal(t, "chat-1", m.ChatID)
	assert.Equal(t, "hello", m.Content)
}

// TestRunner_SessionMode_Isolated mints a fresh scheduled session each run.
func TestRunner_SessionMode_Isolated(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "j3", AgentID: "mia", SessionMode: cron.SessionModeIsolated,
		Payload: cron.CronPayload{Message: "run"},
	}
	sid1, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	sid2, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)

	assert.NotEmpty(t, sid1)
	assert.NotEqual(t, sid1, sid2, "isolated mode must use a fresh session each run")
	require.Len(t, exec.calls, 2)
	assert.Equal(t, "mia", exec.calls[0].owner)

	// Both sessions exist and are scheduled-type.
	m, err := exec.store.GetMeta(sid1)
	require.NoError(t, err)
	assert.Equal(t, session.SessionTypeScheduled, m.Type)
}

// TestRunner_SessionMode_Continue_ThroughLane is the real BLOCKER-5 test: it
// drives the runner through cron's executeJobByID/RunNow (NOT a reused local
// pointer), proving the continue session id minted on the first run is persisted
// back onto the STORED job and reused on the second run. The previous test drove
// RunScheduled with a reused *job and so could never catch the jobCopy bug.
func TestRunner_SessionMode_Continue_ThroughLane(t *testing.T) {
	cfg := baseConfig()
	r := newRunnerOnly(t, cfg, map[string]bool{"mia": true})

	cs := cron.NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	cs.SetRunner(r)
	require.NoError(t, cs.Start())
	defer cs.Stop()

	job, err := cs.AddJobFull(cron.JobSpec{
		Name:        "standup",
		Schedule:    cron.CronSchedule{Kind: "every", EveryMS: i64Ptr(60000)},
		Message:     "standup",
		AgentID:     "mia",
		SessionMode: cron.SessionModeContinue,
	})
	require.NoError(t, err)

	// First run mints a continue session.
	status1, sid1, err := cs.RunNow(job.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", status1)
	require.NotEmpty(t, sid1)

	// The stored job must now carry the minted session id (BLOCKER 5).
	stored, ok := cs.GetJob(job.ID)
	require.True(t, ok)
	assert.Equal(t, sid1, stored.SessionID, "continue session id must persist onto the stored job")

	// Second run reuses the SAME session id (no fresh mint).
	status2, sid2, err := cs.RunNow(job.ID)
	require.NoError(t, err)
	require.Equal(t, "ok", status2)
	assert.Equal(t, sid1, sid2, "continue must reuse the same session id across runs")
}

// TestRunner_DisabledOwner_NoFallback asserts a registered-but-disabled owner is
// rejected as unavailable with no default fallback (HIGH). The checker reports
// availability = registered AND enabled; a disabled owner makes IsRegistered
// false.
func TestRunner_DisabledOwner_NoFallback(t *testing.T) {
	cfg := baseConfig()
	// "mia" is registered but the checker reports false (disabled).
	r, exec, mb, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": false})
	// Drain any outbound alert so PublishOutbound never blocks.
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	job := &cron.CronJob{ID: "jd", Name: "daily", AgentID: "mia", CreatedBy: "alice"}
	sid, err := r.RunScheduled(context.Background(), job)
	assert.Error(t, err)
	assert.Empty(t, sid)
	assert.Empty(t, exec.calls, "disabled owner must not run, and must not fall back")
}

// TestRunner_SessionMode_Main uses the reserved per-owner session id.
func TestRunner_SessionMode_Main(t *testing.T) {
	cfg := baseConfig()
	r := newRunnerOnly(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "j5", AgentID: "mia", SessionMode: cron.SessionModeMain,
		Payload: cron.CronPayload{Message: "remind"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	assert.Equal(t, "sched-main-mia", sid)
}

// TestRunner_Failure_NotifiesAndAlerts asserts a failed run creates a coalesced
// notification for created_by + owner AND publishes a channel alert to the
// owner's default channel (FR-013).
func TestRunner_Failure_NotifiesAndAlerts(t *testing.T) {
	cfg := baseConfig()
	// Owner "mia" is bound to telegram → that is the default channel for the alert.
	cfg.Bindings = []config.AgentBinding{
		{AgentID: "mia", Match: config.BindingMatch{Channel: "telegram"}},
	}
	r, exec, mb, notifs := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	exec.err = fmt.Errorf("provider exploded")

	job := &cron.CronJob{
		ID: "j6", Name: "report", AgentID: "mia", CreatedBy: "bob",
		SessionMode: cron.SessionModeIsolated, Payload: cron.CronPayload{Message: "do it"},
	}

	// Capture the channel alert.
	got := make(chan bus.OutboundMessage, 1)
	go func() { got <- drainOutbound(t, mb) }()

	sid, err := r.RunScheduled(context.Background(), job)
	assert.Error(t, err)
	assert.NotEmpty(t, sid)

	alert := <-got
	assert.Equal(t, "telegram", alert.Channel)
	assert.Contains(t, alert.Content, "report")
	assert.Contains(t, alert.Content, "provider exploded")

	// Notification for the creator "bob".
	bobList, _ := notifs.ListForUser("bob")
	require.Len(t, bobList, 1)
	assert.Equal(t, notifications.TypeScheduleFailed, bobList[0].Type)
	assert.Equal(t, "j6", bobList[0].ScheduleID)
	// Notification for the owner's owner-user "alice".
	aliceList, _ := notifs.ListForUser("alice")
	require.Len(t, aliceList, 1)

	// Live WS push emitted for each recipient.
	assert.GreaterOrEqual(t, len(exec.emitted), 2)
}

// TestRunner_Failure_Coalesces asserts a second failure for the same schedule
// updates the same unread notification rather than spamming (Ambiguity #6).
func TestRunner_Failure_Coalesces(t *testing.T) {
	cfg := baseConfig()
	r, exec, mb, notifs := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	exec.err = fmt.Errorf("boom")

	// Drain alerts in the background so PublishOutbound never blocks.
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	job := &cron.CronJob{
		ID: "j7", Name: "n", AgentID: "mia", CreatedBy: "bob",
		Payload: cron.CronPayload{Message: "x"},
	}
	_, _ = r.RunScheduled(context.Background(), job)
	_, _ = r.RunScheduled(context.Background(), job)

	bobList, _ := notifs.ListForUser("bob")
	assert.Len(t, bobList, 1, "repeated failures must coalesce into one notification")
}

// TestRunner_Failure_NoRecipients_FallsBackToAdmin asserts that when neither
// created_by nor the owner-user is resolvable, admins are notified.
func TestRunner_Failure_NoRecipients_FallsBackToAdmin(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCustom}} // no owner-user
	cfg.Gateway.Users = []config.UserConfig{{Username: "root", Role: config.UserRoleAdmin}}
	r, exec, mb, notifs := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	exec.err = fmt.Errorf("boom")
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	job := &cron.CronJob{ID: "j8", AgentID: "mia", Payload: cron.CronPayload{Message: "x"}} // no created_by
	_, _ = r.RunScheduled(context.Background(), job)

	rootList, _ := notifs.ListForUser("root")
	assert.Len(t, rootList, 1, "admin should be notified when no other recipient resolves")
}

// TestRunner_Timeout_ForceCancels asserts that a run exceeding its deadline is
// force-aborted via RequestCancel(CancelScope{SessionID}) and that the returned
// error is a context.DeadlineExceeded (so the cron lane records it as "timeout").
func TestRunner_Timeout_ForceCancels(t *testing.T) {
	cfg := baseConfig()
	cfg.Schedules.RunTimeoutSeconds = 0 // force per-schedule override below
	r, exec, mb, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	exec.hangUntilCtxDone = true
	canceller := &fakeCanceller{}
	r.canceller = canceller // inject the force-abort surface
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	job := &cron.CronJob{
		ID: "jt", Name: "slow", AgentID: "mia", CreatedBy: "alice",
		SessionMode: cron.SessionModeIsolated, TimeoutSeconds: 1, // 1s deadline
		Payload: cron.CronPayload{Message: "work"},
	}

	sid, err := r.RunScheduled(context.Background(), job)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "timeout error must be DeadlineExceeded, got %v", err)
	require.NotEmpty(t, sid)

	// RequestCancel was called for the run's session.
	calls := canceller.calls()
	require.NotEmpty(t, calls, "RequestCancel must be called on deadline")
	assert.Equal(t, sid, calls[0], "RequestCancel must target the run's session id")

	// A deadline must NOT be classified as a transient (retryable) error.
	assert.False(t, cron.IsTransient(err), "deadline must not be classified transient")
}

// TestRunner_ChildProcessCleanup_Invoked asserts the FR-011 best-effort cleanup
// hook is invoked for the run's session on completion, with a fake tracked
// process registry standing in for real child processes.
func TestRunner_ChildProcessCleanup_Invoked(t *testing.T) {
	cfg := baseConfig()
	r := newRunnerOnly(t, cfg, map[string]bool{"mia": true})

	var cleaned int32
	var cleanedSession atomic.Value
	r.setProcessCleanup(func(sessionID string) {
		atomic.AddInt32(&cleaned, 1)
		cleanedSession.Store(sessionID)
	})

	job := &cron.CronJob{
		ID: "jc", AgentID: "mia", SessionMode: cron.SessionModeIsolated,
		Payload: cron.CronPayload{Message: "spawn"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	require.NotEmpty(t, sid)

	assert.Equal(t, int32(1), atomic.LoadInt32(&cleaned), "process cleanup must be invoked once on completion")
	assert.Equal(t, sid, cleanedSession.Load(), "cleanup must be scoped to the run's session id")
}

// TestScheduledProcRegistry_TracksAndKills verifies the minimal per-session
// process registry tracks PIDs and best-effort terminates them on Cleanup
// (FR-011), using an injected fake kill.
func TestScheduledProcRegistry_TracksAndKills(t *testing.T) {
	reg := newScheduledProcRegistry()
	var killed []int
	reg.kill = func(pid int) error { killed = append(killed, pid); return nil }

	reg.Track("sess-1", 111)
	reg.Track("sess-1", 222)
	reg.Track("sess-2", 333)
	reg.Track("sess-1", 0) // ignored (invalid pid)
	reg.Track("", 444)     // ignored (no session)

	reg.Cleanup("sess-1")
	assert.ElementsMatch(t, []int{111, 222}, killed, "Cleanup must terminate all tracked pids for the session")

	// Cleanup is idempotent — a second call for the same session is a no-op.
	killed = nil
	reg.Cleanup("sess-1")
	assert.Empty(t, killed, "second Cleanup for the same session must be a no-op")
}
