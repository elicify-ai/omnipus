package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// m9cLauncher wraps the real launcher so a test can hold the first Launch open
// (deterministically, via channels) or force a refusal.
type m9cLauncher struct {
	inner       steer.SessionLauncher
	launches    atomic.Int32
	entered     chan struct{} // closed when the first Launch is inside
	release     chan struct{} // the first Launch waits on this
	once        sync.Once
	launchErr   error
	dispatchErr error
}

func (l *m9cLauncher) Launch(ctx context.Context, req steer.LaunchRequest) (steer.LaunchResult, error) {
	n := l.launches.Add(1)
	if l.launchErr != nil {
		return steer.LaunchResult{}, l.launchErr
	}
	if n == 1 && l.entered != nil {
		l.once.Do(func() { close(l.entered) })
		<-l.release
	}
	return l.inner.Launch(ctx, req)
}

func (l *m9cLauncher) Dispatch(ctx context.Context, id string, gen int) (steer.DispatchResult, error) {
	if l.dispatchErr != nil {
		return steer.DispatchResult{}, l.dispatchErr
	}
	return l.inner.Dispatch(ctx, id, gen)
}

// One task gets at most one running launch: while a first start is inside its
// launch, a second concurrent start is refused (or returns the same session)
// and never launches a second session.
func TestStartTaskNow_ConcurrentStartsLaunchOnce_RealLauncher(t *testing.T) {
	al, _ := newRunTaskPolicyLoop(t, nil)
	tk := createRunnableTask(t, al, "t-m9c-conc", rtAssignee)
	wl := &m9cLauncher{inner: NewSteerLauncher(al), entered: make(chan struct{}), release: make(chan struct{})}
	al.taskExecutor.SetSessionLauncher(wl)

	type result struct {
		id  string
		err error
	}
	first := make(chan result, 1)
	go func() {
		id, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
		first <- result{id, err}
	}()
	<-wl.entered // the first start holds the task inside Launch

	secondID, secondErr := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
	close(wl.release)
	r1 := <-first
	joinGoalFixtureRuns(t, al)

	require.NoError(t, r1.err, "the first start must succeed")
	require.NotEmpty(t, r1.id)
	require.EqualValues(t, 1, wl.launches.Load(), "a task must never get a second launch while one is starting or running")
	if secondErr == nil {
		require.Equal(t, r1.id, secondID, "a second start that succeeds must return the same session")
	} else {
		require.Empty(t, secondID)
	}
}

// The BARRIER variant of the concurrency shape: both callers are released at
// the same instant, so the interleaving belongs to the scheduler, not to the
// test. The deleted TestStartTaskNow_NoConcurrentDoubleLaunch was the only
// test of this shape (R1 delta T3): the sequential variant above holds the
// first caller inside Launch and starts the second afterwards, which cannot
// exercise a genuine race for the launch slot — by then the first caller has
// already bound the task, so the second takes the bound-session branch and
// never reaches Launch at all.
func TestStartTaskNow_BarrierReleasedStartsLaunchOnce_RealLauncher(t *testing.T) {
	const callers = 2
	al, _ := newRunTaskPolicyLoop(t, nil)
	tk := createRunnableTask(t, al, "t-m9c-barrier", rtAssignee)
	wl := &m9cLauncher{inner: NewSteerLauncher(al)}
	al.taskExecutor.SetSessionLauncher(wl)

	// A barrier, not a sleep: every caller is inside StartTaskNow before any of
	// them may proceed.
	var barrier sync.WaitGroup
	barrier.Add(callers)
	ids := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			barrier.Done()
			barrier.Wait()
			ids[i], errs[i] = al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
		}(i)
	}
	wg.Wait()
	joinGoalFixtureRuns(t, al)

	// Whatever the interleaving, one task may hold at most ONE session.
	seen := map[string]bool{}
	for i := range callers {
		if errs[i] != nil {
			require.Empty(t, ids[i], "caller %d was refused but reported a session", i)
			continue
		}
		require.NotEmpty(t, ids[i], "caller %d succeeded but reported no session", i)
		seen[ids[i]] = true
	}
	require.LessOrEqual(t, len(seen), 1,
		"every successful caller must name the SAME session, got %v", seen)
	got, gerr := GetTaskStore(al).Get(tk.ID)
	require.NoError(t, gerr)
	require.NotEmpty(t, got.SessionID, "the task must end bound to exactly one session")
	require.EqualValues(t, 1, wl.launches.Load(),
		"two callers released on a barrier must still produce exactly ONE launch")
}

// A launch the launcher refuses must leave the task Failed with the reason,
// never stuck in_progress.
func TestStartTaskNow_RefusedLaunchFailsTheTask_RealLauncher(t *testing.T) {
	for name, set := range map[string]func(*m9cLauncher){
		"launch refused":   func(l *m9cLauncher) { l.launchErr = errors.New("launch boom") },
		"dispatch refused": func(l *m9cLauncher) { l.dispatchErr = errors.New("dispatch boom") },
	} {
		t.Run(name, func(t *testing.T) {
			al, _ := newRunTaskPolicyLoop(t, nil)
			tk := createRunnableTask(t, al, "t-m9c-refused", rtAssignee)
			wl := &m9cLauncher{inner: NewSteerLauncher(al)}
			set(wl)
			al.taskExecutor.SetSessionLauncher(wl)

			id, err := al.taskExecutor.StartTaskNow(context.Background(), tk.ID)
			require.Error(t, err)
			require.Empty(t, id)
			got, gerr := GetTaskStore(al).Get(tk.ID)
			require.NoError(t, gerr)
			require.Equal(t, task.StatusFailed, got.Status, "a refused launch must not leave the task %q", got.Status)
			require.Contains(t, got.Result, "boom", "the Failed task must carry the refusal reason")
		})
	}
}
