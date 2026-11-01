package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestCapRED_CapNotice_ConveyedEpisodeDoesNotSuppressLater(t *testing.T) {
	high, ok := false, true
	capRedMemory(t, &high, &ok)
	al, _ := newSteerAL(t)
	installTestSessionLauncher(t, al)
	wireSteerCompletionDeps(t, al)
	_, release := installParkedProvider(t, al)
	defer release()
	al.GetConfig().Performance.MaxParallelAgents = 8
	holders := capRedFillSteer(t, al.steerAdmission(), 8)
	parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
	ctx := tools.WithAgentID(tools.WithWorkspaceID(tools.WithTranscriptSessionID(context.Background(), parent), testHarnessWorkspaceMembershipID), testDefaultAgentID)
	high = true
	result := delegateToolFor(t, al).Execute(ctx, map[string]any{"action": "run", "agent_id": testDefaultAgentID, "task": "hold", "label": "same-parent"})
	if result == nil || result.IsError {
		t.Fatalf("real delegate failed: %+v", result)
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(result.ForLLM, "\n", 2)[0]), &obj); err != nil {
		t.Fatal(err)
	}
	sid := obj["session_id"].(string)
	capRedAssert(t, "S51 first conveyed episode suppressed", len(capRedInbox(t, al, parent, sid)), 0)
	high, ok = false, false
	al.drainSteerQueue(holders[0])
	msgs := capRedInbox(t, al, parent, sid)
	if len(msgs) != 1 {
		t.Fatalf("S51 later cause-change episode must reach original same-parent caller; got %d", len(msgs))
	}
	p := capRedNotice(t, msgs[0])
	if !strings.HasSuffix(p.MessageId, ":1:2") {
		t.Errorf("second episode identity missing: %s", p.MessageId)
	}
	at := capRedStoredQueueTime(t, al.steerAdmission(), sid)
	want := fmt.Sprintf("Cap notice: at %s your delegated child %q had a turn waiting to start. The lane allowed 2 delegated turns at a time then (7 in use): the machine could not report its available memory.", at.UTC().Format(time.RFC3339), "same-parent")
	capRedAssert(t, "later episode exact M18", p.Text, want)
}

func TestCapRED_CapNotice_AppendFailureIsVisibleAndBounded(t *testing.T) {
	for _, kind := range []string{"storage", "inbox_full", "no_edge"} {
		t.Run(kind, func(t *testing.T) {
			high, ok := false, true
			capRedMemory(t, &high, &ok)
			al, _ := newSteerAL(t)
			wireSteerCompletionDeps(t, al)
			_, release := installParkedProvider(t, al)
			defer release()
			al.GetConfig().Performance.MaxParallelAgents = 8
			holders := capRedFillSteer(t, al.steerAdmission(), 8)
			parent := newTestSteeringSession(t, al, testHarnessWorkspaceMembershipID)
			if kind == "no_edge" {
				parent = ""
			}
			child := capRedLaunchChild(t, al, parent, "failure-child")
			inbox := al.GetMessageInboxStore()
			if kind == "storage" {
				// A regular file where the inbox parent directory must be is a real
				// filesystem error on every platform, unlike chmod under a root process.
				blocked := filepath.Join(t.TempDir(), "not-a-directory")
				if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
					t.Fatal(err)
				}
				al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(blocked, "inbox")), al.GetSessionLifecycleStore())
			}
			if kind == "inbox_full" {
				inbox.InboxUnackedMax = 1
				generation := child.Generation
				var message generated.SessionMessage
				if err := message.FromSessionMessageProgress(generated.SessionMessageProgress{MessageId: "prefill", SessionId: child.SessionID, ParentSessionId: &parent, Generation: &generation, CreatedAt: time.Now().UTC(), Depth: 1, Direction: "child_to_parent", Kind: "progress", Text: "prefill", SenderIdentity: testDefaultAgentID}); err != nil {
					t.Fatal(err)
				}
				if _, err := al.getUpwardDeliverer().Deliver(context.Background(), steer.UpwardEvent{ChildSessionID: child.SessionID, Generation: child.Generation, Outcome: steer.OutcomeProgress, Message: message}); err != nil {
					t.Fatalf("SETUP real inbox prefill: %v", err)
				}
			}
			logs := capRedLogs(t)
			var wakes atomic.Int32
			al.asyncNotifier.registerObserver(func(e AsyncNotifyEvent) {
				if strings.HasPrefix(e.SourceKind, "message_parent:") {
					wakes.Add(1)
				}
			})
			high = true
			d, err := NewSteerLauncher(al).Dispatch(context.Background(), child.SessionID, child.Generation)
			if err != nil {
				t.Fatalf("notice failure must not change dispatch outcome: %v", err)
			}
			capRedAssert(t, "notice failure leaves queued decision", d.State, steer.DispatchQueued)
			countWarnings := func() int {
				count := 0
				for _, line := range logs() {
					// No WARN wording is specified. This isolated dispatch must
					// produce exactly one WARN, whatever sentence the owner uses.
					if line["level"] == "warn" {
						count++
					}
				}
				return count
			}
			capRedAssert(t, "S52 exactly one notice-owned warning", countWarnings(), 1)
			if kind != "no_edge" {
				for _, line := range logs() {
					if line["level"] != "warn" {
						continue
					}
					hasID, hasParent, hasCause := false, false, false
					for _, value := range line {
						if s, ok := value.(string); ok {
							hasID = hasID || strings.HasPrefix(s, "cap-notice:"+parent+":"+child.SessionID+":")
							hasParent = hasParent || s == parent
							hasCause = hasCause || s == "memory_pressure"
						}
					}
					capRedAssert(t, "WARN carries notice identity, recipient and decision cause", []bool{hasID, hasParent, hasCause}, []bool{true, true, true})
				}
			}
			if kind == "inbox_full" {
				rec, err := al.GetSessionLifecycleStore().Load(child.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				if rec.NotDelivered == nil {
					t.Error("S52 inherited NotDelivered summary absent")
				} else {
					capRedAssert(t, "one inherited cap-refusal count", rec.NotDelivered.Count, int64(1))
					capRedAssert(t, "refused kind and reason", []any{rec.NotDelivered.LastKind, rec.NotDelivered.LastReason}, []any{"progress", generated.DelegateNotDeliveredSummaryLastReasonUnackedCap})
				}
				entries, err := al.GetSessionStore().ReadTranscript(parent)
				if err != nil {
					t.Fatal(err)
				}
				frames := 0
				for _, entry := range entries {
					raw, err := json.Marshal(entry)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(raw), "not_delivered") {
						frames++
					}
				}
				capRedAssert(t, "one inherited not_delivered transcript frame", frames, 1)
			}
			// Same cause, same placement: a real release event reconsidering this wait
			// must not retry the failed append or write a second notice-owned warning.
			al.drainSteerQueue(holders[0])
			capRedAssert(t, "one attempt per episode, no warning storm", countWarnings(), 1)
			capRedAssert(t, "failed notice never wakes anyone", wakes.Load(), int32(0))
			if kind != "no_edge" {
				gate := al.steerAdmission()
				limit, occupancy, position := 0, 0, 0
				gate.mu.Lock()
				for i, entry := range gate.queue {
					if entry.sessionID != child.SessionID {
						continue
					}
					v := reflect.ValueOf(entry)
					if !v.FieldByName("Limit").IsValid() {
						gate.mu.Unlock()
						t.Fatal("S52 queue lacks stored refusal evidence; live fallback cannot be exercised")
					}
					limit, occupancy, position = int(v.FieldByName("Limit").Int()), int(v.FieldByName("Occupancy").Int()), i+1
				}
				gate.mu.Unlock()
				if position == 0 {
					t.Fatal("S52 memory-limited wait incorrectly disappeared on a release above the floor")
				}
				at := capRedStoredQueueTime(t, gate, child.SessionID)
				delegateToolFor(t, al).SetClock(func() time.Time { return at.Add(500 * time.Millisecond) })
				text := capRedStatus(t, al, parent, child.SessionID)
				want := fmt.Sprintf("Admission: this child has a turn waiting to start. At its last check (just now) the lane allowed %d delegated turns at a time (%d in use then): the machine was low on memory. Queue position %d. list_jobs shows current state.", limit, occupancy, position)
				start := strings.Index(text, "Admission:")
				if start < 0 {
					t.Errorf("S52 missing live fallback; want %q, got %q", want, text)
				} else {
					capRedAssert(t, "S52 full exact M24 fallback", strings.TrimSpace(text[start:]), want)
				}
			}
		})
	}
}
