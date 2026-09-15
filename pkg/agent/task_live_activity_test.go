package agent

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// Founder decision 2026-09-14 (UAT E-15c): a running task's last-activity
// time must advance while its model is only reasoning. This drives the REAL
// OpenAI-compatible streaming provider against a server that sends nothing
// but reasoning deltas, into a registered task turn, and reads the value the
// REST surface stamps Task.last_activity_at from.
func TestTaskLiveLastActivity_AdvancesWhileOnlyReasoningArrives(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	const taskID = "task-reasoning-only"
	ts := &turnState{turnID: "turn-task-root", opts: processOptions{RunningTaskID: taskID}}
	al.activeTurnStates.Store("session-task-root", ts)
	t.Cleanup(func() { al.activeTurnStates.Delete("session-task-root") })

	if _, ok := al.TaskLiveLastActivity(taskID); ok {
		t.Fatal("before any delta the task must report no live activity")
	}

	step := make(chan struct{})
	stop := make(chan struct{})
	wait := func() bool {
		select {
		case <-step:
			return true
		case <-stop:
			return false
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		send := func(s string) {
			if _, err := w.Write([]byte(s)); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		send(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning_content\":%q}}]}\n\n", "weighing options"))
		if !wait() {
			return
		}
		send(fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"reasoning_content\":%q}}]}\n\n", " still thinking"))
		if !wait() {
			return
		}
		send(`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n")
		send("data: [DONE]\n\n")
	}))
	defer server.Close()
	defer close(stop)

	provider, err := providers.NewHTTPProviderWithMaxTokensFieldAndRequestTimeout("k", server.URL, "", "", 0, nil)
	if err != nil {
		t.Fatalf("NewHTTPProvider: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, streamErr := provider.ChatStream(t.Context(), []providers.Message{{Role: "user", Content: "hi"}},
			nil, "test-model", nil, nil, ts.recordToolCallProgress)
		done <- streamErr
	}()

	waitStamp := func(what string, after time.Time) time.Time {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if at, ok := al.TaskLiveLastActivity(taskID); ok && at.After(after) {
				return at
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("timed out waiting for %s", what)
		return time.Time{}
	}

	first := waitStamp("the first reasoning delta to stamp the task", time.Time{})
	time.Sleep(2 * time.Millisecond)
	step <- struct{}{}
	second := waitStamp("the second reasoning delta to advance the stamp", first)
	if !second.After(first) {
		t.Fatalf("stamp did not advance: first=%v second=%v", first, second)
	}

	step <- struct{}{}
	if streamErr := <-done; streamErr != nil {
		t.Fatalf("ChatStream() error = %v", streamErr)
	}
}

// A delegated child is typically the turn that streams while the task's own
// turn waits, so its progress must count for the task; another task's turn
// must not.
func TestTaskLiveLastActivity_IncludesDelegatesAndIgnoresOtherTasks(t *testing.T) {
	al, _, _, _, cleanup := newTestAgentLoop(t) //nolint:dogsled // only al+cleanup used here
	defer cleanup()

	root := &turnState{turnID: "turn-root", opts: processOptions{RunningTaskID: "task-a"}, childTurnIDs: []string{"turn-child"}}
	child := &turnState{turnID: "turn-child"}
	other := &turnState{turnID: "turn-other", opts: processOptions{RunningTaskID: "task-b"}}
	for key, ts := range map[string]*turnState{"s-root": root, "s-child": child, "s-other": other} {
		al.activeTurnStates.Store(key, ts)
		k := key
		t.Cleanup(func() { al.activeTurnStates.Delete(k) })
	}

	other.recordToolCallProgress(protocoltypes.ToolCallProgress{
		Index: protocoltypes.ReasoningProgressIndex, ReasoningBytes: 10,
	})
	if _, ok := al.TaskLiveLastActivity("task-a"); ok {
		t.Fatal("another task's progress must not count for task-a")
	}

	child.recordToolCallProgress(protocoltypes.ToolCallProgress{
		Index: protocoltypes.ReasoningProgressIndex, ReasoningBytes: 64,
	})
	at, ok := al.TaskLiveLastActivity("task-a")
	if !ok {
		t.Fatal("a delegated child's reasoning must count as the task's activity")
	}
	if want := child.ToolCallProgress().LastActivity; !at.Equal(want) {
		t.Fatalf("task-a stamp = %v, want the child's stamp %v", at, want)
	}

	// A finished child no longer counts.
	child.isFinished.Store(true)
	if _, ok := al.TaskLiveLastActivity("task-a"); ok {
		t.Fatal("a finished delegate's stale stamp must not keep the task looking active")
	}
}
