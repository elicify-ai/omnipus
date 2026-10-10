package agent

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/runner"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// FR-032 / FR-043 (U10b): a person's instruction to a LIVE external-CLI task run
// interrupts the CLI and the task loop resumes the SAME native conversation with
// it, inside the one task turn. No second run, no fresh conversation.

// taskSteerDriver models a CLI: Run blocks until its context is canceled (the
// interrupt), then its stream closes; Resume answers with a fixed text and
// records the instruction it was given.
type taskSteerDriver struct {
	mu          sync.Mutex
	runCalls    int
	resumeCalls int
	resumeInstr []string
	started     chan struct{}
}

func newTaskSteerDriver() *taskSteerDriver { return &taskSteerDriver{started: make(chan struct{})} }

func (d *taskSteerDriver) Run(ctx context.Context, _ runner.RunOptions) (<-chan runner.RunEvent, error) {
	d.mu.Lock()
	d.runCalls++
	d.mu.Unlock()
	ch := make(chan runner.RunEvent)
	close(d.started)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

func (d *taskSteerDriver) Resume(_ context.Context, _ string, instruction ...string) (<-chan runner.RunEvent, error) {
	d.mu.Lock()
	d.resumeCalls++
	d.resumeInstr = append(d.resumeInstr, instruction...)
	d.mu.Unlock()
	ch := make(chan runner.RunEvent, 2)
	ch <- runner.RunEvent{Kind: runner.EventKindOutput, Output: &runner.OutputEvent{Text: "resumed answer"}}
	close(ch)
	return ch, nil
}

func (d *taskSteerDriver) Decide(runner.PermissionDecision) {}
func (d *taskSteerDriver) Cancel()                          {}
func (d *taskSteerDriver) Input(string) error               { return nil }
func (d *taskSteerDriver) Test(context.Context) runner.ConnectionTestResult {
	return runner.ConnectionTestResult{OK: true}
}

func (d *taskSteerDriver) counts() (runs, resumes int, instr []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runCalls, d.resumeCalls, append([]string(nil), d.resumeInstr...)
}

const (
	u10bTaskChat  = "task-chat-u10b"
	u10bAgentID   = "ext-agent"
	u10bTaskInput = "do the task"
)

func u10bTaskFixture(t *testing.T) (*AgentLoop, *AgentInstance, *taskSteerDriver) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	al, _ := newExternalTestLoop(t, "claude-code", "")
	al.SetSessionMessagingStores(session.NewMessageInboxStore(t.TempDir()), session.NewLifecycleStore(t.TempDir()))
	t.Cleanup(func() { al.Close() })
	ws := t.TempDir()
	agent := &AgentInstance{
		ID: u10bAgentID, Name: "External Agent", Home: ws, Tools: tools.NewToolRegistry(),
		Subagents: &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: config.ExecutorKindExternalCLI, CLI: "claude-code"},
		},
	}
	seedTestWorkspaceMembershipForIDs(t, []string{agent.ID})
	require.NoError(t, al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: u10bTaskChat, Generation: 1, State: session.LifecycleRunning,
		OwnerScopeKind: session.OwnerScopeHuman,
		Origin:         &session.Origin{Kind: session.OriginKindTask, TaskID: "task-1"},
		AgentID:        u10bAgentID, Is3P: true,
	}))
	d := newTaskSteerDriver()
	prev := newExternalDriver
	newExternalDriver = func(string, runner.ConsentHandler) (runner.ExternalAgentRunner, error) { return d, nil }
	t.Cleanup(func() { newExternalDriver = prev })
	return al, agent, d
}

type taskRunResult struct {
	resp string
	err  error
}

func TestU10b_FR032_LiveExternalTaskRun_SteerResumesSameConversation(t *testing.T) {
	al, agent, d := u10bTaskFixture(t)
	done := make(chan taskRunResult, 1)
	go func() {
		resp, err := al.processTaskDirectExternalCLI(context.Background(), agent, u10bTaskInput, "task-key", u10bTaskChat, 0)
		done <- taskRunResult{resp, err}
	}()
	select {
	case <-d.started:
	case <-time.After(10 * time.Second):
		t.Fatal("the external task run never started")
	}

	_, err := al.DeliverExternalCLIInstruction(context.Background(), u10bTaskChat, u10bAgentID,
		providers.Message{Role: "user", Content: "focus on the failing test"}, "")
	require.NoError(t, err, "a live external task run must accept the instruction")

	select {
	case r := <-done:
		require.NoError(t, r.err)
		assert.Contains(t, r.resp, "resumed answer", "the task turn returns the resumed conversation's answer")
	case <-time.After(15 * time.Second):
		t.Fatal("the task loop never resumed after the steer interrupt")
	}
	runs, resumes, instr := d.counts()
	assert.Equal(t, 1, runs, "no second fresh Run: the conversation continues")
	assert.Equal(t, 1, resumes, "exactly one resume of the same native conversation")
	require.Len(t, instr, 1)
	assert.Contains(t, instr[0], "focus on the failing test", "the person's instruction reaches the CLI")
	assert.Zero(t, al.pendingSteeringCountForScope(u10bTaskChat), "the instruction is consumed, not stranded")
	assert.False(t, al.takeExternalSteerInterrupt(u10bTaskChat), "the interrupt mark is consumed exactly once")
}

func TestU10b_FR032_NoSteerMeansNoResume(t *testing.T) {
	al, agent, d := u10bTaskFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan taskRunResult, 1)
	go func() {
		resp, err := al.processTaskDirectExternalCLI(ctx, agent, u10bTaskInput, "task-key", u10bTaskChat, 0)
		done <- taskRunResult{resp, err}
	}()
	<-d.started
	cancel() // a stop of the whole task, not a steer
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("the canceled task run never returned")
	}
	runs, resumes, _ := d.counts()
	assert.Equal(t, 1, runs)
	assert.Zero(t, resumes, "a run that was not steered must never be resumed")
}

func TestU10b_FR032_HumanMessageIntoLiveExternalTaskChatSteers(t *testing.T) {
	al, _, _ := u10bTaskFixture(t)
	var canceled int
	sess := al.externalRunSession(u10bTaskChat)
	sess.mu.Lock()
	sess.started, sess.running = true, true
	sess.driver = runner.NewFakeRunner()
	sess.cancelRun = func() { canceled++ }
	sess.mu.Unlock()

	before := al.pendingSteeringCountForScope(u10bTaskChat)
	handled := al.deliverHumanHelperInput(bus.InboundMessage{
		Channel: "webchat", ChatID: "c", SessionID: u10bTaskChat, Content: "change approach",
	})
	require.True(t, handled)
	assert.Equal(t, before+1, al.pendingSteeringCountForScope(u10bTaskChat))
	assert.Equal(t, 1, canceled, "the live CLI run is interrupted")
	assert.True(t, al.takeExternalSteerInterrupt(u10bTaskChat), "and marked so the task loop resumes it")

	// A finished run (not running) is not intercepted: the ordinary path owns it.
	sess.mu.Lock()
	sess.running = false
	sess.mu.Unlock()
	assert.False(t, al.deliverHumanHelperInput(bus.InboundMessage{
		Channel: "webchat", ChatID: "c", SessionID: u10bTaskChat, Content: "again",
	}), "no live run, no interception")
}
