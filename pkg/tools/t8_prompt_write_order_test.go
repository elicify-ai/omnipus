package tools

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// r17 scenario 1.13: A persists Alpha and pauses before goal synchronisation;
// B is demonstrably queued, then A is released and both complete. The production
// SyncGoalDefinition callback is the test-owned barrier, not a replaced writer,
// store or synchroniser. Queue observation never supplies the expected prompt.
func testT8PromptWriteOrder(t *testing.T) {
	t.Helper()
	for _, tc := range []struct{ name, beta, wantGoal string }{
		{"short", "Beta", "Beta"},
		{"ascii_over_4000", "Beta" + strings.Repeat("B", 5000), "Beta" + strings.Repeat("B", 3996)},
		// r17 TK-03 cuts characters, not UTF-8 bytes, at 4,000 characters.
		{"unicode_over_4000", "Beta" + strings.Repeat("界", 5000), "Beta" + strings.Repeat("界", 3996)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, gs := parityStorePair(t)
			tk := parityTask(t, store, "agent-a", "agent-b", "ws-a", task.StatusInbox, true)
			originalGoal := parityGoalPrompt(t, gs, tk.ID)
			releaseA := make(chan struct{})
			aPersisted := make(chan *task.Task, 1)
			aDone, bDone := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			var aResult, bResult t8PromptWriteResult
			bStarted := false
			t.Cleanup(func() {
				releaseOnce.Do(func() { close(releaseA) })
				t8JoinPromptWriter(t, aDone, "A cleanup")
				if bStarted {
					t8JoinPromptWriter(t, bDone, "B cleanup")
				}
			})
			go func() {
				alpha := "Alpha"
				aResult.out, aResult.err = WriteTaskReconciled(store, tk.ID, TaskWriteRequest{
					Patch: task.Patch{Prompt: &alpha},
					SyncGoalDefinition: func(updated *task.Task) error {
						aPersisted <- updated
						<-releaseA
						return nil
					},
				})
				close(aDone)
			}()
			select {
			case updated := <-aPersisted:
				require.Equal(t, "Alpha", updated.Prompt, "barrier is after the actual task write")
			case <-aDone:
				t.Fatalf("A did not reach the in-lock post-persistence barrier: %v", aResult.err)
			case <-time.After(10 * time.Second):
				t.Fatal("A did not reach the in-lock post-persistence barrier")
			}
			persistedA, err := store.Get(tk.ID)
			require.NoError(t, err)
			require.Equal(t, "Alpha", persistedA.Prompt)
			require.Equal(t, originalGoal, parityGoalPrompt(t, gs, tk.ID), "A is before goal synchronisation")
			bStarted = true
			go func() {
				bResult = t8QueuedPromptWriterB(store, tk.ID, tc.beta)
				close(bDone)
			}()
			// A stays held until the actual B goroutine is parked on the shared
			// coordination lock. A launch signal or a sleep cannot prove this.
			require.Eventually(t, func() bool {
				return t8PromptWriterBIsQueued(t8AllGoroutineStacks())
			}, 10*time.Second, 5*time.Millisecond, "B must be queued before A is released")
			select {
			case <-bDone:
				t.Fatal("B completed while A still held the post-persistence barrier")
			default:
			}
			stillA, err := store.Get(tk.ID)
			require.NoError(t, err)
			require.Equal(t, "Alpha", stillA.Prompt, "queued B has not written yet")
			require.Equal(t, originalGoal, parityGoalPrompt(t, gs, tk.ID))
			releaseOnce.Do(func() { close(releaseA) })
			t8JoinPromptWriter(t, aDone, "A")
			t8JoinPromptWriter(t, bDone, "B")
			require.NoError(t, aResult.err)
			require.NoError(t, bResult.err)
			require.NoError(t, aResult.out.GoalDefinitionErr)
			require.NoError(t, aResult.out.GoalPromptErr)
			require.NoError(t, bResult.out.GoalPromptErr)
			final, err := store.Get(tk.ID)
			require.NoError(t, err)
			assert.Equal(t, tc.beta, final.Prompt, "current task is B, never A's stale snapshot")
			assert.Equal(t, tc.wantGoal, parityGoalPrompt(t, gs, tk.ID), "paired goal is B cut to the first 4,000 characters")
		})
	}
}

type t8PromptWriteResult struct {
	out TaskWriteOutcome
	err error
}

func t8QueuedPromptWriterB(store *task.Store, id, prompt string) t8PromptWriteResult {
	out, err := WriteTaskReconciled(store, id, TaskWriteRequest{Patch: task.Patch{Prompt: &prompt}})
	return t8PromptWriteResult{out: out, err: err}
}

func t8JoinPromptWriter(t *testing.T, done <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s writer did not settle after releasing the barrier", label)
	}
}

func t8AllGoroutineStacks() string {
	for size := 64 * 1024; ; size *= 2 {
		buf := make([]byte, size)
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return string(buf[:n])
		}
	}
}

func t8PromptWriterBIsQueued(stacks string) bool {
	for block := range strings.SplitSeq(stacks, "\n\n") {
		if strings.Contains(block, ".t8QueuedPromptWriterB(") &&
			strings.Contains(block, ".LockTaskWriteOrder(") &&
			strings.Contains(block, "runtime_SemacquireMutex(") {
			return true
		}
	}
	return false
}

func TestT8PromptQueueInstrument_RejectsUnrelatedOrUnqueuedGoroutines(t *testing.T) {
	// A synthetic stack is only the observation instrument's negative control,
	// never the ordering fixture or a production-code mutation claim.
	const queued = "goroutine 2 [sync.Mutex.Lock]:\nsync.runtime_SemacquireMutex(\nexample/task.LockTaskWriteOrder(\nexample/tools.t8QueuedPromptWriterB(\n"
	assert.True(t, t8PromptWriterBIsQueued(queued))
	assert.False(t, t8PromptWriterBIsQueued(strings.ReplaceAll(queued, "t8QueuedPromptWriterB", "unrelatedWriter")))
	assert.False(t, t8PromptWriterBIsQueued(strings.ReplaceAll(queued, "LockTaskWriteOrder", "unrelatedLock")))
	assert.False(t, t8PromptWriterBIsQueued(strings.ReplaceAll(queued, "runtime_SemacquireMutex", "running")))
	assert.False(t, t8PromptWriterBIsQueued(""))
}
