//go:build goolm && stdjson

package agent

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Subprocess helper, not an acceptance row. A pipe keeps an actual child alive
// on all three supported platforms without an installed shell/sleep dependency.
func TestT10_TrackedChildProcessHelper(t *testing.T) {
	if os.Getenv("T10_TRACKED_CHILD") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

// Traces to: schedules-t10-spec-r6.md S-F.4, TDD 13.
func t10LoopChildCleanup(t *testing.T, failed bool) {
	f := t10LoopSetup(t, false)
	f.command(t, "/loop every 15m tracked child")
	executable, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(executable, "-test.run=^TestT10_TrackedChildProcessHelper$")
	cmd.Env = append(os.Environ(), "T10_TRACKED_CHILD=1")
	input, err := cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once sync.Once
	cleanup := func() { once.Do(func() { input.Close(); _ = cmd.Process.Kill() }) }
	defer cleanup()
	tracked := 0
	f.p.onChat = func(ctx context.Context) { tracked++; tools.TrackProcess(ctx, cmd.Process.Pid) }
	if failed {
		f.p.failure = errors.New("T10 tracked turn failed")
	}
	jobs := t10SessionLoopJobs(f.ls, f.sid)
	require.Len(t, jobs, 1)
	_, runErr := f.ls.RunScheduled(context.Background(), &jobs[0])
	if failed {
		require.Error(t, runErr)
		assert.Contains(t, runErr.Error(), "T10 tracked turn failed")
	} else {
		require.NoError(t, runErr)
	}
	assert.Equal(t, 1, tracked, "positive control: actual turn reported the live child PID")
	assert.Equal(t, int32(1), f.p.calls.Load())
	select {
	case <-done: // Any exit proves termination; cleanup is best effort, not a mandated exit signal.
	case <-time.After(time.Second):
		t.Error("loop callback returned without terminating its tracked child")
		cleanup()
		<-done
	}
}

// Traces to: schedules-t10-spec-r6.md S-F.5; real ask-denial audit, not a fake
// observer record. Deliberately DO NOT inject WithScheduledJobContext: the loop
// production runner must supply its own job identity.
func t10LoopAuditIdentity(t *testing.T) {
	al, home, auditPath := schedTestLoopWithAudit(t)
	p := testutil.NewScenario().WithToolCall("dangerous_tool", `{}`).WithText("loop continued without the denied tool")
	inst := registerAgent(t, al, home, "mia", p)
	stub := &dangerousStubTool{}
	inst.Tools.Register(stub)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"dangerous_tool": "ask"}})
	approver := &autoApproveApprover{}
	al.SetToolApprover(approver)
	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)
	ls := t10NewLoop(t, filepath.Join(t.TempDir(), "cron", "jobs.json"), al)
	ls.SetClock(newTriggerFakeClock())
	al.SetLoopScheduler(ls)
	matched, handled, reply := al.applyLoopCommandPrompt(context.Background(), bus.InboundMessage{Content: "/loop every 15m audit"}, inst, &processOptions{UserInitiated: true, TranscriptStore: al.GetSessionStore(), TranscriptSessionID: meta.ID})
	require.True(t, matched && handled, reply)
	jobs := t10SessionLoopJobs(ls, meta.ID)
	require.Len(t, jobs, 1)
	reply, err = ls.RunScheduled(context.Background(), &jobs[0])
	require.NoError(t, err)
	assert.Equal(t, "loop continued without the denied tool", reply)
	assert.False(t, stub.wasCalled.Load())
	assert.False(t, approver.consulted.Load())
	records, err := readScheduledAuditRecords(auditPath)
	require.NoError(t, err)
	found := 0
	for _, rec := range records {
		if rec["event"] != audit.EventToolPolicyDenyAttempted {
			continue
		}
		// S-F.5 / FR-10(e): the WithScheduledJobContext equivalent is an
		// audit.Entry. The denied tool is the top-level "tool" field.
		// Job identity stays in details. A name only under details["tool"]
		// or details["tool_name"] is not that entry.
		if rec["tool"] != "dangerous_tool" {
			continue
		}
		fields, ok := rec["details"].(map[string]any)
		require.True(t, ok)
		found++
		assert.Equal(t, jobs[0].ID, fields["schedule_job_id"])
		assert.Equal(t, jobs[0].Name, fields["schedule_job_name"])
	}
	assert.Equal(t, 1, found, "one real audit denial for the loop job, never an empty log green")
}
