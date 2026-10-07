package gateway

// Oracle: accepted I1 option B. Kill the process holding an actual first-human
// execution; graceful Close would land its own outcome and hide the crash gap.
import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// not-wire-format: subprocess fixture receipt, never sent through the gateway.
type i1CrashReceipt struct {
	Config                                    *config.Config
	Agents                                    []config.AgentConfig // Config omits its separately persisted agent list from JSON.
	Root, LifecycleDir, InboxDir, SessionsDir string
}

const i1CrashReceiptEnv = "OMNIPUS_I1_CRASH_RECEIPT"

func TestI1RootRestart(t *testing.T) {
	if receiptPath := os.Getenv(i1CrashReceiptEnv); receiptPath != "" {
		f, _ := newFirstHumanStopFixture(t)
		inboxDir := filepath.Join(f.al.GetConfig().Agents.Defaults.Home, "i1_inbox")
		f.al.SetSessionMessagingStores(session.NewMessageInboxStore(inboxDir), f.lifecycle)
		id := firstHumanRootIntoProvider(t, f, "i1-killed-first-human")
		rec, err := f.lifecycle.Load(id)
		require.NoError(t, err)
		require.Equal(t, session.LifecycleRunning, rec.State)
		require.NotNil(t, rec.ExecutionID, "instrument: genuine admission must be durable before SIGKILL")
		// Operator-configured second agent and trust edge survive the same
		// process receipt as the existing profile; delegation is not self-target.
		cfg := f.al.GetConfig()
		cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "i1-helper", Home: cfg.Agents.Defaults.Home})
		ensureTestWorkspaceMembership(t, cfg)
		require.NoError(t, workspace.SaveDelegation(cfg.Agents.Defaults.Home, rec.WorkspaceID, []workspace.DelegationEdge{{FromAgent: rec.AgentID, ToAgent: "i1-helper", Modes: []workspace.DelegationMode{workspace.ModeDirect}}}))
		raw, err := json.Marshal(i1CrashReceipt{Config: f.al.GetConfig(), Agents: f.al.GetConfig().Agents.List, Root: id, LifecycleDir: f.lifecycle.Dir(), InboxDir: inboxDir, SessionsDir: f.al.GetSessionStore().BaseDir()})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(receiptPath+".tmp", raw, 0600))
		require.NoError(t, os.Rename(receiptPath+".tmp", receiptPath))
		select {} // Only Process.Kill ends this subprocess; never run graceful Close.
	}
	for _, continuation := range []bool{false, true} {
		name := "boot_history_and_second_boot"
		if continuation {
			name = "human_continuation_and_delegation"
		}
		t.Run(name, func(t *testing.T) {
			m := i1KillAdmittedRoot(t)
			t.Setenv("OMNIPUS_HOME", m.Config.Agents.Defaults.Home)
			beforeStore := session.NewLifecycleStore(m.LifecycleDir)
			before, err := beforeStore.Load(m.Root)
			require.NoError(t, err)
			require.Equal(t, session.LifecycleRunning, before.State, "instrument: SIGKILL leaves the admitted execution running on disk")
			us, err := session.NewUnifiedStore(m.SessionsDir)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, us.Close()) })
			prior, err := us.ReadTranscript(m.Root)
			require.NoError(t, err)
			require.NotEmpty(t, prior, "the real first human message must be durably saved")
			fresh, stg, p, msgBus := i1ReopenAndBoot(t, m)
			after, err := stg.lifecycleStore.Load(m.Root)
			require.NoError(t, err)
			assert.Equal(t, session.LifecycleStopped, after.State, "I1: boot must settle the dead ordinary execution, not leave Working forever")
			assert.Empty(t, after.FailedReason, "a restart-cut conversation does not fail")
			assert.False(t, after.Terminal(), "restart Stop must keep the conversation resumable")
			assert.Equal(t, before.Generation, after.Generation, "boot must not mint a generation")
			assert.Equal(t, before.ExecutionID, after.ExecutionID, "boot records the interrupted execution, not a fabricated admission")
			assert.Equal(t, before.GoalRef, after.GoalRef, "boot must not clear the session goal")
			assert.Equal(t, before.LastActivityAt, after.LastActivityAt, "recovery is not real user/agent activity")
			assert.Nil(t, after.Stop)
			if assert.NotNil(t, after.StopNote, "I1: restart Stop must carry the durable reason") {
				assert.Equal(t, session.StopCauseRestart, after.StopNote.Cause)
				assert.Equal(t, session.StopActorRestart, after.StopNote.By)
				assert.Equal(t, stg.bootEpoch.Current(), after.StopNote.BootSeq, "note identifies the actual writing boot")
			}
			assert.Equal(t, session.LifecycleDisplayInterrupted, session.LifecycleRecordToDisplay(after))
			got, err := fresh.GetSessionStore().ReadTranscript(m.Root)
			require.NoError(t, err)
			assert.Equal(t, prior, got, "restart preserves exact history and writes no invented assistant answer")
			meta, err := fresh.GetSessionStore().GetMeta(m.Root)
			require.NoError(t, err)
			assert.Equal(t, session.StatusActive, meta.Status, "option B leaves the conversation active, not archived")
			transitions, err := stg.lifecycleStore.ListStoppedTransitions(m.Root)
			require.NoError(t, err)
			if assert.Len(t, transitions, 1, "restart Stop must land in the existing control ledger exactly once") {
				assert.Equal(t, session.StopCauseRestart, transitions[0].Cause)
				assert.Equal(t, before.Generation, transitions[0].Generation)
				assert.Empty(t, transitions[0].ControlID, "automatic restart fabricates no accepted human control")
				assert.Empty(t, transitions[0].ParentSessionID, "ordinary root has no parent notice")
			}
			assert.Zero(t, p.calls.Load(), "ZERO provider calls at boot: no automatic replay")
			if continuation {
				require.Equal(t, session.LifecycleStopped, after.State, "continuation must start from the recovered crash, not from seeded Stop state")
				i1ContinueHumanAndDelegate(t, fresh, stg, p, msgBus, m.Root, before, prior)
				return
			}
			journalPath := filepath.Join(m.LifecycleDir, m.Root+".jsonl")
			journal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			fresh.Close()
			_, second, secondProvider, _ := i1ReopenAndBoot(t, m)
			secondJournal, err := os.ReadFile(journalPath)
			require.NoError(t, err)
			assert.Equal(t, journal, secondJournal, "a second real boot must add no lifecycle transition")
			secondTransitions, err := second.lifecycleStore.ListStoppedTransitions(m.Root)
			require.NoError(t, err)
			assert.Equal(t, transitions, secondTransitions, "second boot must add no restart ledger transition")
			assert.Zero(t, secondProvider.calls.Load(), "second boot must not call the provider")
			t.Logf("I1: killed run boot=%d; writing boot=%d; second boot=%d; saved outcome=%s; provider calls=0", before.ExecutionID.BootSeq, stg.bootEpoch.Current(), second.bootEpoch.Current(), after.State)
		})
	}
}

func i1KillAdmittedRoot(t *testing.T) i1CrashReceipt {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ready.json")
	log, err := os.Create(filepath.Join(dir, "killed-process.log"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, log.Close()) })
	cmd := exec.Command(os.Args[0], "-test.run=^TestI1RootRestart$", "-test.v")
	cmd.Env = append(os.Environ(), i1CrashReceiptEnv+"="+path, "TMPDIR="+dir, "TMP="+dir, "TEMP="+dir)
	cmd.Stdout, cmd.Stderr = log, log
	require.NoError(t, cmd.Start())
	joined := false
	t.Cleanup(func() {
		if !joined {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	deadline := time.NewTimer(cancelTestTurnStartDeadline)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var raw []byte
	for raw == nil {
		bytes, readErr := os.ReadFile(path)
		if readErr == nil {
			raw = bytes
			break
		}
		require.True(t, os.IsNotExist(readErr), "read actual subprocess admission receipt: %v", readErr)
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatalf("SIGKILL fixture never reached the external provider; process log: %s", log.Name())
		}
	}
	require.NoError(t, cmd.Process.Kill(), "real process death, not Stop/Close")
	waitErr := cmd.Wait()
	joined = true
	require.Error(t, waitErr, "instrument: the child must be killed before it finishes the turn")
	var m i1CrashReceipt
	require.NoError(t, json.Unmarshal(raw, &m))
	m.Config.Agents.List = m.Agents
	return m
}

func i1ReopenAndBoot(t *testing.T, m i1CrashReceipt) (*agent.AgentLoop, *setupAndStartServicesState, *uatD2bFinalProvider, *bus.MessageBus) {
	t.Helper()
	p := &uatD2bFinalProvider{entered: make(chan context.Context, 16), release: make(chan struct{})}
	msgBus := bus.NewMessageBus()
	al, err := agent.NewAgentLoop(m.Config, msgBus, p)
	require.NoError(t, err)
	t.Cleanup(func() { al.Close(); msgBus.Close(); gatewaySteerCancellers.Delete(al) })
	ls := session.NewLifecycleStore(m.LifecycleDir)
	al.SetSessionMessagingStores(session.NewMessageInboxStore(m.InboxDir), ls)
	stg := &setupAndStartServicesState{ctx: context.Background(), cfg: m.Config, homePath: m.Config.Agents.Defaults.Home, agentLoop: al, lifecycleStore: ls, runningServices: &services{}}
	require.NoError(t, stg.mintBootEpoch(), "real gateway physical-boot mint")
	stg.wireSteerDeps()
	require.NoError(t, stg.runningServices.SteerDeps.BootHook(context.Background()), "real gateway hook, including accepted-Stop finisher")
	return al, stg, p, msgBus
}
