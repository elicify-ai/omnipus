//go:build darwin || linux

package agent

// Oracle: founder decision 2026-10-06 Q13. Plain Stop ends the current
// session turn but leaves its background shell work running; tree-scope
// Stop all / cancel kills that work, including after the turn already ended.
// The provider is the only fake boundary. StopSession, the registered bash
// tool, its background supervisor, the shared registry and OS PIDs are real.
// Windows needs a separate Job Object liveness probe, not a runtime skip.

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Five seconds is a harness completion/reaping budget, not an elapsed-time
// oracle. The fixture's sleep lifetime is much longer than all these waits.
const q13CompletionBudget = 5 * time.Second

// q13BackgroundProvider asks the real registered bash tool to start a job,
// then holds the next provider request until Stop cancels that request.
// release exists only for cleanup of a failed setup; no tested Stop calls it.
type q13BackgroundProvider struct {
	*testutil.ScenarioProvider
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *q13BackgroundProvider) open() { p.once.Do(func() { close(p.release) }) }

func (p *q13BackgroundProvider) Chat(ctx context.Context, messages []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any) (*providers.LLMResponse, error) {
	call := p.calls.Add(1)
	if call == 1 {
		return p.ScenarioProvider.Chat(ctx, messages, defs, model, opts)
	}
	if call == 2 {
		close(p.entered)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.release:
		return &providers.LLMResponse{Content: "fixture cleanup"}, nil
	}
}

type q13StopBackgroundFixture struct {
	al      *AgentLoop
	owner   string
	process *tools.ProcessSession
	pid     int // snapshot before Stop; never re-read as the expected PID
	handle  *turnState
	done    chan struct{}
	turnErr error
}

func q13WaitForProcessDeath(t *testing.T, pid int) {
	t.Helper()
	require.Eventually(t, func() bool {
		return syscall.Kill(pid, 0) == syscall.ESRCH && syscall.Kill(-pid, 0) == syscall.ESRCH
	}, q13CompletionBudget, 10*time.Millisecond,
		"Q13: real background PID %d and its process group must cease to exist (ESRCH), not just be relabeled", pid)
}

func q13StartBackgroundInLiveTurn(t *testing.T) *q13StopBackgroundFixture {
	t.Helper()
	t.Setenv("OMNIPUS_HOME", t.TempDir())
	// exec makes the harmless sleep the group leader, so the registry PID
	// identifies the actual job rather than an intermediate shell.
	script := testutil.NewScenario().WithToolCall("bash",
		`{"command":"exec sleep 300","run_in_background":true,"timeout_seconds":300}`)
	al, _, wrapper := newBashAsyncTestLoop(t, script)
	mintGenuineBootEpochForLoop(t, al)
	stores := t.TempDir()
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(stores, "inbox")),
		session.NewLifecycleStore(filepath.Join(stores, "lifecycle")))
	provider := &q13BackgroundProvider{
		ScenarioProvider: script, entered: make(chan struct{}), release: make(chan struct{}),
	}
	for _, id := range al.GetRegistry().ListAgentIDs() {
		instance, ok := al.GetRegistry().GetAgent(id)
		require.True(t, ok, "SETUP: registered agent %q must exist", id)
		instance.Provider = provider
	}
	f := &q13StopBackgroundFixture{al: al, done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	// Registered before starting either the turn or the shell. This runs
	// before the existing fixture's al.Close cleanup, even after a failure.
	t.Cleanup(func() {
		cancel()
		provider.open()
		select {
		case <-f.done:
		case <-time.After(q13CompletionBudget):
			t.Error("CLEANUP: real turn did not return")
		}
		if id := wrapper.capturedSessionID(); id != "" {
			process, err := tools.GetSharedSessionManager().Get(id)
			if err != nil {
				t.Errorf("CLEANUP: resolve owned background process %q: %v", id, err)
				return
			}
			if process.PID <= 0 {
				t.Errorf("CLEANUP: refusing to signal invalid PID %d", process.PID)
				return
			}
			if err := syscall.Kill(-process.PID, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
				t.Errorf("CLEANUP: kill owned process group %d: %v", process.PID, err)
			}
			q13WaitForProcessDeath(t, process.PID)
			t.Logf("CLEANUP: pid=%d and process group are absent (ESRCH)", process.PID)
		}
	})
	go func() {
		_, f.turnErr = al.ProcessDirectWithChannel(ctx, "start a background sleep and keep working",
			"q13-"+t.Name(), "cli", "q13-stop-background")
		close(f.done)
	}()
	select {
	case <-provider.entered:
	case <-f.done:
		t.Fatalf("SETUP: turn ended before starting the background job and reaching the held request: %v", f.turnErr)
	case <-time.After(q13CompletionBudget):
		t.Fatal("SETUP: turn never reached the provider request after its real background bash call")
	}
	id := wrapper.capturedSessionID()
	require.NotEmpty(t, id, "SETUP: real background bash response must identify the registered process")
	f.owner = wrapper.capturedOwnerTranscriptSessionID()
	require.NotEmpty(t, f.owner, "SETUP: real tool dispatch must stamp the owning session")
	var err error
	f.process, err = tools.GetSharedSessionManager().Get(id)
	require.NoError(t, err, "SETUP: real bash-created process must be in the shared registry")
	f.pid = f.process.PID
	require.True(t, f.process.Background, "SETUP: must exercise background mode, not foreground exec")
	require.Equal(t, f.owner, f.process.OwnerSessionID, "SETUP: Stop must target the job's own session")
	require.Greater(t, f.pid, 0, "SETUP: must use a real, valid OS PID")
	require.Equal(t, "running", f.process.GetStatus(), "SETUP: sleep must still be running")
	require.NoError(t, syscall.Kill(f.pid, 0), "SETUP: OS liveness control must see the real job")
	require.NoError(t, syscall.Kill(-f.pid, 0), "SETUP: real background process group must exist")
	f.handle, _ = al.activeTurnForCancel(f.owner, CancelScope{SessionID: f.owner, TurnOnly: true}).(*turnState)
	require.NotNil(t, f.handle, "SETUP: the same session must have a real active turn")
	require.True(t, f.handle.IsAlive(), "SETUP: Stop must interrupt a currently live turn")
	t.Logf("SETUP: real bash job id=%s owner=%s pid=%d status=running; OS PID/group probes succeeded; turn=%s active",
		id, f.owner, f.pid, f.handle.TurnID())
	return f
}

func q13StopSession(t *testing.T, f *q13StopBackgroundFixture, tree bool) StopResult {
	t.Helper()
	res, err := f.al.StopSession(context.Background(), StopRequest{
		SessionID: f.owner, By: steer.Principal{Kind: steer.PrincipalKindHuman, ID: "q13-human"},
		Channel: "web", Tree: tree,
		// Default hooks: the production background-kill path, not a fake.
	})
	require.NoError(t, err, "Q13: real StopSession must accept the requested scope")
	require.NoError(t, res.RootErr, "Q13: selected session's Stop must not fail")
	require.Empty(t, res.Report.Unreachable, "Q13: selected session must be reachable")
	return res
}

func q13RequireTurnEnded(t *testing.T, f *q13StopBackgroundFixture, res StopResult) {
	t.Helper()
	require.True(t, res.Root.Fired, "Q13: Stop must fire on the original live turn")
	require.Equal(t, f.handle.TurnID(), res.Root.TurnID, "Q13: Stop must target this session's exact turn")
	select {
	case <-f.done:
	case <-time.After(q13CompletionBudget):
		t.Fatal("Q13: actual turn processing did not return after Stop (cleanup has not released the provider)")
	}
	require.False(t, f.handle.IsAlive(), "Q13: original turn must genuinely finish, not merely report Fired")
	require.Nil(t, f.al.activeTurnForCancel(f.owner, CancelScope{SessionID: f.owner, TurnOnly: true}),
		"Q13: Stop must leave no current live turn for this session")
	t.Logf("Q13: turn=%s genuinely ended through StopSession; returned error=%v", f.handle.TurnID(), f.turnErr)
}

func q13AssertSameBackgroundAlive(t *testing.T, f *q13StopBackgroundFixture) {
	t.Helper()
	got, err := tools.GetSharedSessionManager().Get(f.process.ID)
	require.NoError(t, err, "Q13: plain Stop must retain the registered background job")
	assert.Same(t, f.process, got, "Q13: plain Stop must not replace the background job")
	assert.Equal(t, f.pid, got.PID, "Q13: plain Stop must preserve the original PID")
	assert.Equal(t, f.owner, got.OwnerSessionID, "Q13: job must remain owned by the same session")
	assert.Equal(t, "running", got.GetStatus(), "Q13: plain Stop must LEAVE background work running")
	assert.False(t, got.IsDone(), "Q13: plain Stop must not mark the background job terminal")
	pidErr, groupErr := syscall.Kill(got.PID, 0), syscall.Kill(-got.PID, 0)
	assert.NoError(t, pidErr, "Q13: original OS PID must still be alive after plain Stop")
	assert.NoError(t, groupErr, "Q13: original OS process group must still be alive after plain Stop")
	t.Logf("Q13: after plain Stop id=%s pid=%d status=%s PID probe=%v group probe=%v", got.ID, got.PID, got.GetStatus(), pidErr, groupErr)
}

func q13RequireBackgroundKilled(t *testing.T, f *q13StopBackgroundFixture, res StopResult) {
	t.Helper()
	assert.Equal(t, 1, res.BackgroundKilled, "Q13: tree Stop must kill exactly the one owned background job")
	assert.Equal(t, 0, res.BackgroundFailed, "Q13: tree Stop must not fail a background kill")
	got, err := tools.GetSharedSessionManager().Get(f.process.ID)
	require.NoError(t, err, "Q13: canceled background job must remain inspectable in the registry")
	assert.Same(t, f.process, got, "Q13: tree Stop must kill the same original job")
	assert.Equal(t, "canceled", got.GetStatus(), "Q13: tree Stop must mark the owned background job canceled")
	assert.True(t, got.IsDone(), "Q13: canceled background job must be terminal")
	q13WaitForProcessDeath(t, f.pid)
	t.Logf("Q13: after tree Stop original pid=%d is absent (PID/group ESRCH); reported killed=%d failed=%d",
		f.pid, res.BackgroundKilled, res.BackgroundFailed)
}

func TestQ13StopSession_PlainStopEndsTurnLeavesRealBackgroundAlive(t *testing.T) {
	// Sequential: AgentLoop.Close reaps the shared background registry.
	f := q13StartBackgroundInLiveTurn(t)
	res := q13StopSession(t, f, false)
	q13RequireTurnEnded(t, f, res)
	q13AssertSameBackgroundAlive(t, f)
	assert.Equal(t, 0, res.BackgroundKilled, "Q13: plain Stop must kill no background jobs")
	assert.Equal(t, 0, res.BackgroundFailed, "Q13: plain Stop must attempt no background kills")
}

func TestQ13StopSession_StopAllKillsRealBackgroundProcess(t *testing.T) {
	f := q13StartBackgroundInLiveTurn(t)
	res := q13StopSession(t, f, true)
	q13RequireTurnEnded(t, f, res)
	q13RequireBackgroundKilled(t, f, res)
}

func TestQ13StopSession_StopAllAfterPlainStopKillsSameBackgroundProcess(t *testing.T) {
	f := q13StartBackgroundInLiveTurn(t)
	plain := q13StopSession(t, f, false)
	q13RequireTurnEnded(t, f, plain)
	// Non-fatal survival assertions allow the later real Stop all to run even
	// on the broken base, without weakening the first Stop's expectations.
	q13AssertSameBackgroundAlive(t, f)
	assert.Equal(t, 0, plain.BackgroundKilled, "Q13: first, plain Stop must leave the job running")
	assert.Equal(t, 0, plain.BackgroundFailed, "Q13: first, plain Stop must attempt no background kills")
	// No replacement turn or new shell is started: this must kill the same
	// original PID even though the owning session's current turn is gone.
	tree := q13StopSession(t, f, true)
	q13RequireBackgroundKilled(t, f, tree)
}
