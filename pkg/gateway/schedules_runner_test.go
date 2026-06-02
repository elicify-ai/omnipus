//go:build !cgo

// Scheduled-runner integration tests (#264): owner pinning (no default
// fallback), deliver=true direct send, per-mode session selection, and the
// failure → coalesced notification + channel alert path. Deterministic: a fake
// executor stands in for the agent loop; an in-memory bus captures outbound.

package gateway

import (
	"context"
	"fmt"
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
}

type fakeCall struct {
	owner     string
	sessionID string
	content   string
}

func (f *fakeExecutor) ProcessScheduled(_ context.Context, owner, sessionID, content, _, _ string) (string, error) {
	f.calls = append(f.calls, fakeCall{owner: owner, sessionID: sessionID, content: content})
	return f.reply, f.err
}
func (f *fakeExecutor) GetSessionStore() *session.UnifiedStore { return f.store }
func (f *fakeExecutor) GetConfig() *config.Config              { return f.cfg }
func (f *fakeExecutor) EmitNotification(p agent.NotificationPayload) {
	f.emitted = append(f.emitted, p)
}

type fakeChecker struct{ registered map[string]bool }

func (c fakeChecker) IsRegistered(id string) bool { return c.registered[id] }

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

// TestRunner_SessionMode_Continue reuses one session id across runs and persists
// it onto the job (W-2).
func TestRunner_SessionMode_Continue(t *testing.T) {
	cfg := baseConfig()
	r := newRunnerOnly(t, cfg, map[string]bool{"mia": true})

	job := &cron.CronJob{
		ID: "j4", AgentID: "mia", SessionMode: cron.SessionModeContinue,
		Payload: cron.CronPayload{Message: "standup"},
	}
	sid1, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	assert.Equal(t, sid1, job.SessionID, "continue must persist its session id on the job")

	sid2, err := r.RunScheduled(context.Background(), job)
	require.NoError(t, err)
	assert.Equal(t, sid1, sid2, "continue must reuse the same session id")
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
