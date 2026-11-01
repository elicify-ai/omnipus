package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func capRedCapNotices(t *testing.T, al *AgentLoop, parent, child string) []generated.SessionMessageProgress {
	t.Helper()
	var notices []generated.SessionMessageProgress
	for _, msg := range capRedInbox(t, al, parent, child) {
		p, err := msg.AsSessionMessageProgress()
		if err == nil && p.Kind == "progress" && strings.HasPrefix(p.MessageId, "cap-notice:") {
			notices = append(notices, p)
		}
	}
	return notices
}

func TestCapRED_CapNotice_RunTaskCallerDiffersFromSteeringParent(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, caller := newRunTaskPolicyLoop(t, []graphEdge{edge(rtCaller, rtAssignee, nil, nil)})
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 8
	capRedFillSteer(t, al.steerAdmission(), 8)
	parent, err := al.GetSessionStore().GetOrCreateMainSession(testWS, rtAssignee)
	if err != nil {
		t.Fatal(err)
	}
	callerSession := newTestSteeringSessionOwnedBy(t, al, testWS, rtCaller)
	te := GetTaskExecutor(al)
	te.SetMainSessionResolver(&u6FakeMainResolver{elig: map[[2]string]string{{testWS, rtAssignee}: parent.ID}})
	tk := createRunnableTask(t, al, "caller-is-not-parent", rtAssignee)
	high = true
	ctx := tools.WithAgentID(tools.WithWorkspaceID(tools.WithTranscriptSessionID(context.Background(), callerSession), testWS), rtCaller)
	result := capRedToolJSON(t, caller.Tools.Execute(ctx, "run_task", map[string]any{"task_id": tk.ID}))
	sid, _ := result["session_id"].(string)
	if sid == "" {
		t.Fatal("real caller result lacks run session")
	}
	rec, err := al.GetSessionLifecycleStore().Load(sid)
	if err != nil {
		t.Fatal(err)
	}
	if rec.SteeredBy == nil || rec.SteeredBy.SteeringSessionID != parent.ID || rec.InitiatedBy == nil || rec.InitiatedBy.SessionID != callerSession {
		t.Fatalf("real launch did not establish A-caller/B-parent identities: %+v", rec)
	}
	want := "task started and queued: this machine is low on memory, so new delegated turns are held at 2 at a time (8 in use, checked just now); its turn has not started and it is number 1 in line"
	capRedAssert(t, "S37b caller gets its own exact M7a", result["note"], want)
	notices := capRedCapNotices(t, al, parent.ID, sid)
	capRedAssert(t, "S37b one real notice addressed to B main", len(notices), 1)
	capRedAssert(t, "S37b no duplicate or misaddressed notice to caller A", len(capRedCapNotices(t, al, callerSession, sid)), 0)
	if len(notices) == 1 {
		at := capRedStoredQueueTime(t, al.steerAdmission(), sid)
		text := fmt.Sprintf("Cap notice: at %s your task run %q had a turn waiting to start. The lane allowed 2 delegated turns at a time then (8 in use): the machine was low on memory.", at.UTC().Format(time.RFC3339), tk.Title)
		capRedAssert(t, "S37b parent receives exact dated M18", notices[0].Text, text)
	}
}

func TestCapRED_CapNotice_StoppedParentRetainedNeverWoken(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 8
	capRedFillSteer(t, al.steerAdmission(), 8)
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "stopped-recipient")
	// Launch initializes the parent's real ordinary-root lifecycle. Stop only
	// that parent after the edge exists, never the child's subtree.
	c := NewSteerCanceller(al.GetSessionLifecycleStore(), nil)
	if _, err := c.StopTurns(context.Background(), parent, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "cap-test"}, false, nil); err != nil {
		t.Fatal(err)
	}
	var wakes atomic.Int32
	al.asyncNotifier.registerObserver(func(e AsyncNotifyEvent) {
		if strings.HasPrefix(e.SourceKind, "message_parent:") {
			wakes.Add(1)
		}
	})
	high = true
	d, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
	if err != nil {
		t.Fatal(err)
	}
	capRedAssert(t, "stopped recipient does not change queued outcome", d.State, steer.DispatchQueued)
	capRedAssert(t, "S37c one retained cap notice", len(capRedCapNotices(t, al, parent, child.SessionID)), 1)
	capRedAssert(t, "S38 stopped recipient never woken", wakes.Load(), int32(0))
	capRedAssert(t, "notice consumes no turn", al.steerAdmission().activeCount(), 8)
	r, err := al.GetSessionLifecycleStore().Load(parent)
	if err != nil {
		t.Fatal(err)
	}
	if r.Stop == nil || r.Stop.Generation != r.Generation {
		t.Error("notice cleared stopped recipient's own fence")
	}
	foreign := newTestSteeringSession(t, al, "different-workspace")
	capRedAssert(t, "S38 no broadcast", len(capRedCapNotices(t, al, foreign, child.SessionID)), 0)
}

func TestCapRED_CapNotice_ConcurrentRechecksAttemptOneCauseEpisode(t *testing.T) {
	var high, readable atomic.Bool
	readable.Store(true)
	restore := config.SetMemoryProviderForTest(func() (bool, bool) { return high.Load(), readable.Load() }, func() (uint64, bool) { return 1 << 30, readable.Load() })
	t.Cleanup(restore)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	_, release := installParkedProvider(t, al)
	defer func() { release(); al.drainSteeredTurns(3 * time.Second) }()
	al.GetConfig().Performance.MaxParallelAgents = 8
	holders := capRedFillSteer(t, al.steerAdmission(), 8)
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "concurrent-notices")
	high.Store(true)
	if _, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation); err != nil {
		t.Fatal(err)
	}
	capRedAssert(t, "initial pressure episode delivered", len(capRedCapNotices(t, al, parent, child.SessionID)), 1)
	high.Store(false)
	readable.Store(false)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; al.drainSteerQueue(holders[i]) }(i)
	}
	close(start)
	wg.Wait()
	notices := capRedCapNotices(t, al, parent, child.SessionID)
	capRedAssert(t, "concurrent cause transition attempts exactly once", len(notices), 2)
	if len(notices) == 2 {
		capRedAssert(t, "ordered full-claim episode ids", []bool{strings.HasSuffix(notices[0].MessageId, ":1:1"), strings.HasSuffix(notices[1].MessageId, ":1:2")}, []bool{true, true})
		if !strings.HasSuffix(notices[1].Text, "the machine could not report its available memory.") {
			t.Errorf("transition notice lost its own cause: %q", notices[1].Text)
		}
	}
	capRedAssert(t, "no recheck promotes above memory floor", []int{al.steerAdmission().activeCount(), al.steerAdmission().queueLen()}, []int{5, 1})
}

func TestCapRED_CapNotice_RevivedGenerationMintsNewIdentity(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	wireSteerCompletionDeps(t, al)
	al.GetConfig().Performance.MaxParallelAgents = 8
	capRedFillSteer(t, al.steerAdmission(), 8)
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	child := capRedLaunchChild(t, al, parent, "revived-notice")
	high = true
	launcher := NewSteerLauncher(al)
	if _, err := launcher.Dispatch(context.Background(), child.SessionID, child.Generation); err != nil {
		t.Fatal(err)
	}
	first := capRedCapNotices(t, al, parent, child.SessionID)
	capRedAssert(t, "first placement notice", len(first), 1)
	old := capRedClaim(t, al, child.SessionID)
	// A real queued cancellation removes that full claim. Landing a failed
	// execution is a real durable transition, and Revive is the real G+1 owner.
	c := NewSteerCanceller(al.GetSessionLifecycleStore(), al.SteerGenerationCancel)
	if _, err := c.StopTurns(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "cap-test"}, false, al.SteerGenerationCancel); err != nil {
		t.Fatal(err)
	}
	if err := al.GetSessionLifecycleStore().Mutate(child.SessionID, func(r *session.LifecycleRecord) error {
		r.State = session.LifecycleFailed
		r.FailedReason = "test queued execution ended"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	gen, err := c.Revive(context.Background(), child.SessionID, steer.Principal{Kind: steer.PrincipalKindHuman, ID: "cap-test"})
	if err != nil {
		t.Fatal(err)
	}
	capRedAssert(t, "real revival advances generation", gen, child.Generation+1)
	if _, err := launcher.Dispatch(context.Background(), child.SessionID, gen); err != nil {
		t.Fatal(err)
	}
	fresh := capRedClaim(t, al, child.SessionID)
	if fresh == old {
		t.Error("revived admission reused old full execution identity")
	}
	notices := capRedCapNotices(t, al, parent, child.SessionID)
	capRedAssert(t, "revival not deduplicated against ended placement", len(notices), 2)
	if len(notices) == 2 {
		if notices[0].MessageId == notices[1].MessageId {
			t.Error("revived notice reused old deterministic id")
		}
		prefix := fmt.Sprintf("cap-notice:%s:%s:%d:%s:%d:", parent, fresh.SessionID, fresh.Generation, fresh.RunID, fresh.BootSeq)
		if !strings.HasPrefix(notices[1].MessageId, prefix) {
			t.Errorf("revived notice lacks current full identity: %q, want prefix %q", notices[1].MessageId, prefix)
		}
		capRedAssert(t, "revived envelope generation", notices[1].Generation, &gen)
	}
}

func capRedWarnCount(lines []map[string]any) int {
	count := 0
	for _, line := range lines {
		if line["level"] == "warn" {
			count++
		}
	}
	return count
}

// Only the filesystem boundary is faulted; shared Deliver still executes.
func capRedBlockInbox(t *testing.T, al *AgentLoop) func() {
	t.Helper()
	saved := al.GetMessageInboxStore()
	blocked := filepath.Join(t.TempDir(), "inbox-parent-is-a-file")
	if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(blocked, "inbox")), al.GetSessionLifecycleStore())
	var once sync.Once
	restore := func() { once.Do(func() { al.SetSessionMessagingStores(saved, al.GetSessionLifecycleStore()) }) }
	t.Cleanup(restore)
	return restore
}
