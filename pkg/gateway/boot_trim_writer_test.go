package gateway

// The boot cache-trim sweep's WaitGroup membership (TempDir-cleanup family
// #1644/#1695, CHECK 1 finding F5). The trim goroutine's exit after its
// cancel is near-instant, so the only observable of a MISSING join is the
// sweep still WALKING profiles when the drain has already returned: the
// sweep is ctx-unaware (TrimAllEligible runs to completion), and per
// profile it CREATES a sibling .chrome.lock file and REMOVES the allow-listed
// cache directories. This test plants enough real profiles that the sweep
// outlasts the drain, waits for the first profile to be touched (the sweep
// is provably in flight), drains, and then requires the sweep to have
// finished every planted profile and the home to be completely unchanged —
// additions, removals and resizes all fail. Post-fix the drain waits out the
// whole sweep inside the join; on the mutant that dropped the trim goroutine
// from the WaitGroup the drain returns early and the remaining profiles are
// trimmed after it did.
//
// The drain's budget is controlled by this test, not a wall clock (CHECK 4
// finding R3, 2026-11-08). The test used to hand the drain a real
// bootBackgroundStopTimeout (10 s) deadline: under a loaded -race run the
// 1,200-profile sweep outlasted it, the drain returned by design (a budgeted
// stop continues without a writer that is still running) and the join
// assertion failed although nothing was wrong with the join. This test proves
// the JOIN; whether a drain that runs out of budget continues is a separate
// contract that the real budget keeps in production. So the drain's context
// is cancelled only by the test, and only on its liveness guard — the go test
// deadline, the same bound that would otherwise kill a hung test — which
// fails the test as a hang, never passes it.
import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// trimKillProfiles is the planted-profile count: enough that the ctx-unaware
// boot sweep is still mid-walk when the drain starts, whatever the machine.
const trimKillProfiles = 1200

// firstProfileTouch waits until the boot sweep has touched its FIRST profile
// — a sibling .chrome.lock appearing, or a planted cache directory going
// away. It returns the moment the sweep is provably in flight.
func firstProfileTouch(t *testing.T, profileRoot string, plantedDirs []string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		locks, _ := filepath.Glob(filepath.Join(profileRoot, ".*.chrome.lock"))
		if len(locks) > 0 {
			return
		}
		for _, dir := range plantedDirs {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("instrument: the boot cache-trim sweep never touched any of the %d planted profiles (looked for %v and .*.chrome.lock siblings)",
		trimKillProfiles, plantedDirs[:min(3, len(plantedDirs))])
}

// TestTrimSweep_JoinedBeforeHomeRemoval kills the writer mutant that drops
// the cache-trim goroutine from the boot WaitGroup, with the REAL pool and
// the REAL sweep: the drain must return only after the in-flight sweep has
// finished walking the profiles, so the home is unchanged afterwards.
func TestTrimSweep_JoinedBeforeHomeRemoval(t *testing.T) {
	home, _ := bootFixture(t)
	bootEditedPlan(t, home)
	// Hermetic: this test needs the REAL pool and the REAL trim sweep, but
	// not the managed-Chromium fetch the same boot kicks off — on a networked
	// machine that would start a real download into the very home the
	// assertions below watch. The swap is this test's only transport effect
	// and is restored on cleanup.
	oldTransport := http.DefaultTransport
	http.DefaultTransport = bootDownloadTransport(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("trim-sweep instrument forbids external request: " + r.URL.Host)
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })
	gate := installTrimWorkerGate(t)
	profileRoot := filepath.Join(home, "browser", "profiles")
	var plantedDirs []string
	for i := 0; i < trimKillProfiles; i++ {
		profile := filepath.Join(profileRoot, "ws-tw"+fmt.Sprintf("%04d", i))
		cache := filepath.Join(profile, "Default", "Cache")
		require.NoError(t, os.MkdirAll(cache, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(cache, "blob"), []byte("cache-bytes"), 0o600))
		plantedDirs = append(plantedDirs, cache)
	}

	rc := &runContextWithOptions{ctx: context.Background(), homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	require.NoError(t, err)
	require.False(t, stop)
	rc.provider = &restMockProvider{}
	err, stop = rc.initializeAgentLoop()
	require.NoError(t, err)
	require.False(t, stop)
	stopBootWritersOnCleanup(t, rc)
	// Runs BEFORE the drain cleanup above (LIFO): a failure while the trim
	// goroutine is held must release it so the cleanup drain can join it.
	t.Cleanup(gate.release)

	// The sweep is provably mid-flight here: at least one profile has been
	// locked or already trimmed, and the sweep never re-checks its context
	// until the whole walk is done.
	firstProfileTouch(t, profileRoot, plantedDirs)

	// Deterministic in-flight gate (CHECK 5 re-audit R1/R2). The REAL boot
	// trim goroutine is held, by the test, at the point where its real sweep
	// has finished and it logs the result: it cannot exit, and so cannot
	// call its deferred WaitGroup Done, until the test releases it. Whatever
	// the machine's speed, the trim goroutine is therefore provably still a
	// live writer when the drain runs, and a drain that does not wait for
	// it has to return while it is held.
	<-gate.entered(t)

	// The drain's budget is test-controlled (CHECK 4 R3, see the file
	// comment): no wall-clock deadline races the sweep's real workload.
	stopCtx, stopCancel := context.WithCancel(context.Background())
	defer stopCancel()
	// A drain that returns because its budget expired is not a join (CHECK 4
	// R3): the budget handed to it here must carry no wall-clock deadline.
	if _, hasDeadline := stopCtx.Deadline(); hasDeadline {
		t.Fatal("boot background writers were not joined by the teardown drain: it was handed a wall-clock deadline, so an early return would be a budget expiry, not a join")
	}
	var seq atomic.Int64
	var returnSeq int64
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		rc.stopBootBackgroundWriters(stopCtx)
		returnSeq = seq.Add(1)
	}()
	// While the trim goroutine is held, a correct drain is blocked and
	// cannot return; the wait below ends on that state, never on a timer.
	if awaitDrainBlockedOrReturned(t, drained, gate, stopCancel) {
		gate.release()
		<-drained
		t.Fatal("boot background writers were not joined by the teardown drain: it returned while the boot cache-trim goroutine was still held")
	}
	releaseSeq := seq.Add(1)
	gate.release()
	awaitTrimDrainReturn(t, drained, stopCancel, profileRoot, plantedDirs)
	// Return order, recorded by sequence number before any filesystem
	// inventory: the release of the held trim goroutine precedes the
	// drain's return.
	if returnSeq < releaseSeq {
		t.Fatalf("teardown drain returned (seq %d) before the held boot cache-trim goroutine was released (seq %d)", returnSeq, releaseSeq)
	}

	select {
	case <-rc.bootBackgroundDone:
	default:
		t.Fatal("boot background writers were not joined by the teardown drain")
	}
	// The join covers the WHOLE boot sweep: when the drain returns, every
	// planted profile has been walked and its allow-listed cache removed
	// (FR-072 trigger 2 — none of them has a live Chrome or a held lock). A
	// drain that returned while the sweep was still walking leaves planted
	// caches behind and fails here, before the write-window check below.
	if remaining := plantedCachesRemaining(plantedDirs); len(remaining) > 0 {
		t.Fatalf("boot cache-trim sweep still had %d of %d planted profiles untrimmed when the teardown drain returned (first: %v)",
			len(remaining), trimKillProfiles, remaining[:min(3, len(remaining))])
	}
	// Post-drain the home must be EXACTLY as the drain left it. On the
	// unjoined mutant the remaining profiles' locks and cache removals land
	// here and fail this line.
	watchHomeQuiescent(t, home, homeTree(t, home))
}

// plantedCachesRemaining returns the planted cache directories that still
// exist. A stat error other than not-exist counts as remaining: the
// instrument must not read an unreadable path as "trimmed".
func plantedCachesRemaining(plantedDirs []string) []string {
	var remaining []string
	for _, dir := range plantedDirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			remaining = append(remaining, dir)
		}
	}
	return remaining
}

// awaitTrimDrainReturn waits for the teardown drain to return. Its only
// clock is a liveness guard: the go test deadline (minus a margin to report
// before the harness kills the binary), or none when the run has no
// deadline. The guard can only FAIL the test — it cancels the drain's
// context, waits for the drain to return, and reports a hang with the
// sweep's progress — so no load level can turn a slow sweep into a pass,
// and none can turn a correct join into the R3 false "not joined" failure.
func awaitTrimDrainReturn(t *testing.T, drained <-chan struct{}, cancel context.CancelFunc, profileRoot string, plantedDirs []string) {
	t.Helper()
	var guard <-chan time.Time
	if deadline, ok := t.Deadline(); ok {
		margin := 30 * time.Second
		timer := time.NewTimer(max(time.Until(deadline)-margin, time.Second))
		defer timer.Stop()
		guard = timer.C
	}
	select {
	case <-drained:
	case <-guard:
		cancel()
		<-drained
		t.Fatalf("teardown drain did not return before the test deadline: a hang, not a budget expiry; %d of %d planted profiles under %s still untrimmed",
			len(plantedCachesRemaining(plantedDirs)), trimKillProfiles, profileRoot)
	}
}

// trimWorkerGate holds the real boot trim goroutine at its post-sweep log
// line (success or recovered panic), through a slog default handler that
// forwards every record to a text handler on stderr.
type trimWorkerGate struct {
	reached     chan struct{}
	released    chan struct{}
	reachedOnce sync.Once
	releaseOnce sync.Once
}

func (g *trimWorkerGate) entered(t *testing.T) <-chan struct{} {
	t.Helper()
	return g.reached
}

func (g *trimWorkerGate) release() { g.releaseOnce.Do(func() { close(g.released) }) }

type trimWorkerGateHandler struct {
	slog.Handler
	gate *trimWorkerGate
}

func (h trimWorkerGateHandler) Handle(ctx context.Context, r slog.Record) error {
	if strings.HasPrefix(r.Message, "browser-trim: trimmed closed workspace browser caches at boot") ||
		strings.HasPrefix(r.Message, "browser-trim: cache trim panicked") {
		h.gate.reachedOnce.Do(func() { close(h.gate.reached) })
		<-h.gate.released
	}
	return h.Handler.Handle(ctx, r)
}

func (h trimWorkerGateHandler) WithAttrs(a []slog.Attr) slog.Handler {
	return trimWorkerGateHandler{h.Handler.WithAttrs(a), h.gate}
}

func (h trimWorkerGateHandler) WithGroup(n string) slog.Handler {
	return trimWorkerGateHandler{h.Handler.WithGroup(n), h.gate}
}

// installTrimWorkerGate installs the gate; the cleanup always releases the
// held goroutine and restores the previous default logger.
func installTrimWorkerGate(t *testing.T) *trimWorkerGate {
	t.Helper()
	g := &trimWorkerGate{reached: make(chan struct{}), released: make(chan struct{})}
	old := slog.Default()
	// A fresh text handler, not old.Handler(): the stock default handler
	// writes through the log package, which SetDefault re-points at the
	// new handler — a feedback loop that deadlocks on the log mutex.
	slog.SetDefault(slog.New(trimWorkerGateHandler{slog.NewTextHandler(os.Stderr, nil), g}))
	t.Cleanup(func() {
		g.release()
		slog.SetDefault(old)
	})
	return g
}

// awaitDrainBlockedOrReturned returns true if the drain returned, false once
// the drain is provably blocked behind the held trim goroutine. "Provably
// blocked" is read from one stop-the-world goroutine snapshot: the drain is
// parked in its select, the held trim goroutine is present, exactly ONE other
// goroutine the boot spawned is alive and it is the boot joiner (identified
// by being parked in WaitGroup.Wait called DIRECTLY by an initializeAgentLoop
// closure — a writer that waits on some other WaitGroup has a different
// caller and disqualifies the snapshot), and every other boot writer has
// exited (its Done has run). The joiner's counter can then only be held by
// the trim goroutine. No timer paces the loop: it ends on a state, and its
// only bound is the go test deadline — if the snapshot never qualifies (for
// example another boot writer never finishes) the wait fails there with a
// named message instead of passing.
func awaitDrainBlockedOrReturned(t *testing.T, drained <-chan struct{}, gate *trimWorkerGate, cancel context.CancelFunc) bool {
	t.Helper()
	var guard <-chan time.Time
	if deadline, ok := t.Deadline(); ok {
		timer := time.NewTimer(max(time.Until(deadline)-30*time.Second, time.Second))
		defer timer.Stop()
		guard = timer.C
	}
	buf := make([]byte, 1<<20)
	for {
		select {
		case <-drained:
			return true
		case <-guard:
			gate.release()
			cancel()
			<-drained
			t.Fatal("teardown drain was never provably blocked behind the held boot cache-trim goroutine before the test deadline: another boot writer is still alive, or the goroutine snapshot never matched; the drain's wait cannot be attributed to the trim worker")
		default:
		}
		var n int
		for { // a truncated dump could hide a live writer: grow until complete
			n = runtime.Stack(buf, true)
			if n < len(buf) {
				break
			}
			buf = make([]byte, 2*len(buf))
		}
		if drainBlockedBehindTrimWorker(string(buf[:n])) {
			select {
			case <-drained:
				return true
			default:
			}
			return false
		}
		runtime.Gosched()
	}
}

const bootJoinerCaller = "(*runContextWithOptions).initializeAgentLoop.func"

func drainBlockedBehindTrimWorker(dump string) bool {
	var drainParked, joiners, worker bool
	nJoiners := 0
	for _, g := range strings.Split(dump, "\n\n") {
		header, _, _ := strings.Cut(g, "\n")
		switch {
		case strings.Contains(g, "stopBootBackgroundWriters") && strings.Contains(header, "select"):
			drainParked = true
		case strings.Contains(g, "runBrowserCacheTrimSchedule"):
			worker = true
		case strings.Contains(g, "created by") && strings.Contains(g, "initializeAgentLoop"):
			if !isBootJoiner(g) || strings.Contains(header, "runnable") {
				return false // another boot writer is still alive
			}
			nJoiners++
		}
	}
	joiners = nJoiners == 1
	return drainParked && joiners && worker
}

// isBootJoiner reports whether the goroutine block is parked in
// sync.WaitGroup.Wait with an initializeAgentLoop closure as the direct
// caller of that Wait — the boot joiner's own frame, which no other boot
// writer (the trim worker, the preprovision, the primer) has.
func isBootJoiner(block string) bool {
	var frames []string
	for _, line := range strings.Split(block, "\n")[1:] {
		if line == "" || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "created by") {
			continue
		}
		frames = append(frames, line)
	}
	for i, f := range frames {
		if strings.HasPrefix(f, "sync.(*WaitGroup).Wait(") {
			return i+1 < len(frames) && strings.Contains(frames[i+1], bootJoinerCaller)
		}
	}
	return false
}
