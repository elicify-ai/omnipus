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
	"os"
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
	"github.com/dapicom-ai/omnipus/pkg/tools"
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
	// onProcess, when set, is invoked with the run context before returning. It
	// lets a test simulate a tool spawning a child during the run (e.g. by
	// calling tools.TrackProcess(ctx, pid)) so the FR-011 tracker wiring is
	// exercised end-to-end.
	onProcess func(ctx context.Context)
}

type fakeCall struct {
	owner     string
	sessionID string
	content   string
}

func (f *fakeExecutor) ProcessScheduled(ctx context.Context, owner, sessionID, content, _, _ string) (string, error) {
	f.calls = append(f.calls, fakeCall{owner: owner, sessionID: sessionID, content: content})
	if f.onProcess != nil {
		f.onProcess(ctx)
	}
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

	cs := cron.NewCronService(filepath.Join(t.TempDir(), "jobs.json"))
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

// TestRunner_SessionMode_Continue_PrunedRecreates covers the FR-004 "continue
// pruned" edge. A continue job carries a stale SessionID whose on-disk session
// no longer exists. pickSession calls GetOrCreateScheduledSession, which for a
// VALID id RE-CREATES the missing session rather than erroring — so the run
// proceeds with a usable session id (the recreate path).
//
// NOTE on reachability: the explicit error-fallback branch in pickSession
// (GetOrCreateScheduledSession error → NewScheduledSession) is NOT reachable for
// a valid-but-pruned id, because GetOrCreateScheduledSession recreates it
// instead of erroring. That branch only fires when the id is INVALID (fails
// validateSessionID) — covered by the sibling subtest below, which asserts the
// fallback yields a fresh, different, usable scheduled session.
func TestRunner_SessionMode_Continue_PrunedRecreates(t *testing.T) {
	t.Run("valid pruned id is recreated and usable", func(t *testing.T) {
		cfg := baseConfig()
		r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

		// A stale but structurally-valid session id that was never created on disk.
		const staleID = "session_PRUNEDBUTVALID0000000000"
		job := &cron.CronJob{
			ID: "jcp", AgentID: "mia", SessionMode: cron.SessionModeContinue,
			SessionID: staleID,
			Payload:   cron.CronPayload{Message: "standup"},
		}

		sid, err := r.RunScheduled(context.Background(), job)
		require.NoError(t, err)
		require.NotEmpty(t, sid)
		// GetOrCreateScheduledSession recreates the exact id → run uses it.
		assert.Equal(t, staleID, sid, "valid pruned continue id must be recreated, not replaced")

		// The recreated session is real and scheduled-type (usable).
		m, err := exec.store.GetMeta(sid)
		require.NoError(t, err)
		assert.Equal(t, session.SessionTypeScheduled, m.Type)
	})

	t.Run("invalid pruned id falls back to a fresh session", func(t *testing.T) {
		cfg := baseConfig()
		r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

		// An invalid session id (path-escape) makes GetOrCreateScheduledSession
		// return an error → the runner's fallback mints a fresh isolated session.
		const badID = "../escape"
		job := &cron.CronJob{
			ID: "jcb", AgentID: "mia", SessionMode: cron.SessionModeContinue,
			SessionID: badID,
			Payload:   cron.CronPayload{Message: "standup"},
		}

		sid, err := r.RunScheduled(context.Background(), job)
		require.NoError(t, err, "invalid continue id must fall back, not fail the run")
		require.NotEmpty(t, sid)
		assert.NotEqual(t, badID, sid, "fallback must yield a different, fresh session id")

		// The fresh session is real and usable.
		m, err := exec.store.GetMeta(sid)
		require.NoError(t, err)
		assert.Equal(t, session.SessionTypeScheduled, m.Type)
	})
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

// TestRunner_Failure_AdminBroadcast_PersistsPerAdmin asserts M4: with multiple
// admins and no createdBy/owner-user, each admin gets their OWN persisted
// notification (readable after restart), not a single _admin_.json sentinel row.
func TestRunner_Failure_AdminBroadcast_PersistsPerAdmin(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.List = []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCustom}} // no owner-user
	cfg.Gateway.Users = []config.UserConfig{
		{Username: "root", Role: config.UserRoleAdmin},
		{Username: "ops", Role: config.UserRoleAdmin},
		{Username: "viewer", Role: config.UserRoleUser}, // non-admin: must NOT be notified
	}
	r, exec, mb, notifs := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	exec.err = fmt.Errorf("boom")
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	job := &cron.CronJob{ID: "j9", AgentID: "mia", Payload: cron.CronPayload{Message: "x"}} // no created_by
	_, _ = r.RunScheduled(context.Background(), job)

	rootList, _ := notifs.ListForUser("root")
	assert.Len(t, rootList, 1, "admin root must have a persisted notification")
	opsList, _ := notifs.ListForUser("ops")
	assert.Len(t, opsList, 1, "admin ops must have a persisted notification")
	viewerList, _ := notifs.ListForUser("viewer")
	assert.Empty(t, viewerList, "non-admin must not be notified")

	// No sentinel file persisted: ListForUser of the sentinel returns empty.
	sentinelList, _ := notifs.ListForUser(agent.NotificationAdminBroadcast)
	assert.Empty(t, sentinelList, "the admin-broadcast sentinel must not be persisted")
}

// TestRunner_Failure_PushesEvenWhenCreateFails asserts H3: when notification
// persistence fails, the live WS push still fires for each recipient (so
// connected users see the alert) instead of being silently dropped.
func TestRunner_Failure_PushesEvenWhenCreateFails(t *testing.T) {
	cfg := baseConfig()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err)
	exec := &fakeExecutor{store: store, cfg: cfg, err: fmt.Errorf("boom")}
	mb := bus.NewMessageBus()
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	// Root the notifications store at a path that is a FILE, so saveLocked's
	// MkdirAll fails and Create errors deterministically.
	badPath := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(badPath, []byte("x"), 0o600))
	notifs := notifications.NewStore(badPath)

	r := newScheduledRunner(exec, fakeChecker{registered: map[string]bool{"mia": true}}, mb, notifs, exec.GetConfig)

	job := &cron.CronJob{
		ID: "jcf", Name: "report", AgentID: "mia", CreatedBy: "bob",
		SessionMode: cron.SessionModeIsolated, Payload: cron.CronPayload{Message: "do it"},
	}
	_, runErr := r.RunScheduled(context.Background(), job)
	require.Error(t, runErr)

	// Persistence failed (Create errored), but the live push fired for both
	// recipients (creator "bob" + owner-user "alice").
	require.GreaterOrEqual(t, len(exec.emitted), 2, "live push must fire even when persistence fails")
	recipients := map[string]bool{}
	for _, p := range exec.emitted {
		recipients[p.Recipient] = true
	}
	assert.True(t, recipients["bob"], "creator must get a live push")
	assert.True(t, recipients["alice"], "owner-user must get a live push")
}

// fakeChannelChecker stands in for the channel registry in M2 tests.
type fakeChannelChecker struct{ registered map[string]bool }

func (c fakeChannelChecker) ChannelRegistered(name string) bool { return c.registered[name] }

// TestRunner_DeliverTrue_UnregisteredChannel_Fails asserts M2: a deliver=true
// run whose target channel is not registered/active is recorded as a failure
// (and never publishes), instead of a silent success.
func TestRunner_DeliverTrue_UnregisteredChannel_Fails(t *testing.T) {
	cfg := baseConfig()
	r, exec, mb, notifs := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	// Channel registry reports "telegram" as NOT registered.
	r.setChannelChecker(func() channelChecker {
		return fakeChannelChecker{registered: map[string]bool{}}
	})

	job := &cron.CronJob{
		ID: "jdu", Name: "ping", AgentID: "mia", CreatedBy: "alice",
		Payload: cron.CronPayload{Message: "hi", Deliver: true, Channel: "telegram", To: "c1"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.Error(t, err, "delivery to an unregistered channel must be a failure")
	assert.Empty(t, sid)
	assert.Empty(t, exec.calls, "deliver=true must not run an agent turn")
	assert.Contains(t, err.Error(), "not registered")

	// The bus is buffered, so by the time RunScheduled returns any outbound is
	// already enqueued. Drain it non-blockingly: the original delivery payload
	// "hi" must NOT appear (only the failure ALERT may, which onFailure sends to
	// the owner's default channel).
	for {
		select {
		case m := <-mb.OutboundChan():
			assert.NotEqual(t, "hi", m.Content, "the original delivery payload must NOT be published to a dead channel")
			continue
		default:
		}
		break
	}

	// The failure raised a notification for the creator.
	aliceList, _ := notifs.ListForUser("alice")
	assert.Len(t, aliceList, 1, "an unregistered-channel delivery failure must raise a notification")
}

// TestRunner_DeliverTrue_RegisteredChannel_Succeeds asserts the M2 check passes
// through when the target channel IS registered.
func TestRunner_DeliverTrue_RegisteredChannel_Succeeds(t *testing.T) {
	cfg := baseConfig()
	r, _, mb, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	r.setChannelChecker(func() channelChecker {
		return fakeChannelChecker{registered: map[string]bool{"telegram": true}}
	})

	job := &cron.CronJob{
		ID: "jdr", Name: "ping", AgentID: "mia",
		Payload: cron.CronPayload{Message: "hi", Deliver: true, Channel: "telegram", To: "c1"},
	}
	_, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	m := drainOutbound(t, mb)
	assert.Equal(t, "telegram", m.Channel)
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

// TestRunner_ChildProcessCleanup_OnError asserts the FR-011 cleanup hook fires
// even when the run FAILS (not just on success). A failed run can leave behind
// spawned children just as a successful one can, so cleanup must run on the
// error path too.
func TestRunner_ChildProcessCleanup_OnError(t *testing.T) {
	cfg := baseConfig()
	r, exec, mb, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	exec.err = errors.New("boom") // run fails
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	var cleaned int32
	var cleanedSession atomic.Value
	r.setProcessCleanup(func(sessionID string) {
		atomic.AddInt32(&cleaned, 1)
		cleanedSession.Store(sessionID)
	})

	job := &cron.CronJob{
		ID: "jce", AgentID: "mia", CreatedBy: "alice", SessionMode: cron.SessionModeIsolated,
		Payload: cron.CronPayload{Message: "spawn"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.Error(t, err)
	require.NotEmpty(t, sid)

	assert.Equal(t, int32(1), atomic.LoadInt32(&cleaned), "process cleanup must fire on the error path")
	assert.Equal(t, sid, cleanedSession.Load(), "cleanup must be scoped to the run's session id")
}

// TestRunner_ChildProcessCleanup_OnTimeout asserts the FR-011 cleanup hook fires
// on the deadline/timeout path. A run that overran its deadline is exactly when
// orphaned children are most likely, so cleanup must run there too.
func TestRunner_ChildProcessCleanup_OnTimeout(t *testing.T) {
	cfg := baseConfig()
	cfg.Schedules.RunTimeoutSeconds = 0
	r, exec, mb, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})
	exec.hangUntilCtxDone = true
	r.canceller = &fakeCanceller{}
	go func() {
		for range mb.OutboundChan() {
		}
	}()

	var cleaned int32
	var cleanedSession atomic.Value
	r.setProcessCleanup(func(sessionID string) {
		atomic.AddInt32(&cleaned, 1)
		cleanedSession.Store(sessionID)
	})

	job := &cron.CronJob{
		ID: "jct", AgentID: "mia", CreatedBy: "alice",
		SessionMode: cron.SessionModeIsolated, TimeoutSeconds: 1,
		Payload: cron.CronPayload{Message: "work"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded))
	require.NotEmpty(t, sid)

	assert.Equal(t, int32(1), atomic.LoadInt32(&cleaned), "process cleanup must fire on the timeout path")
	assert.Equal(t, sid, cleanedSession.Load(), "cleanup must be scoped to the run's session id")
}

// TestRunner_ProcessTracker_TrackedThenCleaned proves the FR-011 tracker the
// runner installs on the ProcessScheduled context is actually reachable by the
// run: a PID reported through tools.TrackProcess(ctx, pid) during the run lands
// in the per-session registry and is terminated on completion. This is the
// end-to-end wiring proof (Track is NOT inert).
func TestRunner_ProcessTracker_TrackedThenCleaned(t *testing.T) {
	cfg := baseConfig()
	r, exec, _, _ := newRunnerHarness(t, cfg, map[string]bool{"mia": true})

	reg := newScheduledProcRegistry()
	var killed []int
	var mu sync.Mutex
	reg.kill = func(pid int) error {
		mu.Lock()
		killed = append(killed, pid)
		mu.Unlock()
		return nil
	}
	r.setProcessTracker(reg.Track)
	r.setProcessCleanup(reg.Cleanup)

	// Simulate a tool spawning a child during the run: read the tracker the
	// runner installed on the context and report a PID through it.
	exec.onProcess = func(ctx context.Context) {
		tools.TrackProcess(ctx, 4242)
	}

	job := &cron.CronJob{
		ID: "jpt", AgentID: "mia", SessionMode: cron.SessionModeIsolated,
		Payload: cron.CronPayload{Message: "spawn"},
	}
	sid, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	require.NotEmpty(t, sid)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []int{4242}, killed, "the tracked child PID must be terminated on completion")
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
