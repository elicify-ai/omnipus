package gateway

// The managed-Chromium preprovision download's lifecycle under the boot
// drains (TempDir-cleanup family #1644/#1695, strengthened by CHECK 1
// findings F1/F5, 2026-11-08).
//
// Only the EXTERNAL HTTP edge is replaced: the manifest and the archive
// bytes are served from this process, and the installer's real code writes
// the real browser/chromium/<version>/<archive>.part-* file. The stream's
// second read blocks until its request context is cancelled and then takes
// a finite 25 ms to acknowledge it — long enough that a drain that returns
// without having JOINED the download fails the oracle deterministically,
// while the oracle itself only ever checks "the body is closed / the partial
// is gone", never an elapsed-time threshold. Every request this test's boot
// makes is answered by this transport; the default transport is restored in
// cleanup, so no request can reach the network.
import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type bootDownloadTransport func(*http.Request) (*http.Response, error)

func (f bootDownloadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// bootDownloadStream is the held-open archive body: one real chunk reaches
// the installer's .part file, the second read blocks on the request context
// (cancelled by the boot drain) with a finite 25 ms acknowledgement, and
// Close records the close event's position in the process-wide event order.
type bootDownloadStream struct {
	ctx                           context.Context
	ready, release, wrote, closed chan struct{}
	step                          int
	once                          sync.Once

	// closedStamp is the close event's position in that order, taken under
	// closedMu before the closed channel fires, so the join oracle compares
	// event positions instead of racing two ready channels (CHECK 2,
	// 2026-11-08, defect 2) and still orders two events inside one clock
	// tick (CHECK 3 finding R2: tied wall-clock stamps used to bless an
	// illegal order).
	closedMu    sync.Mutex
	closedStamp bootEventStamp
}

func (s *bootDownloadStream) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	s.step++
	switch s.step {
	case 1:
		return copy(p, "before"), nil
	case 2:
		close(s.ready) // io.Copy already wrote the first chunk into the .part file.
		select {
		case <-s.ctx.Done():
			time.Sleep(25 * time.Millisecond) // finite cancellation acknowledgement at the network edge
			return 0, s.ctx.Err()
		case <-s.release:
			if err := s.ctx.Err(); err != nil {
				return 0, err
			}
			return copy(p, "after"), nil
		}
	default:
		close(s.wrote) // io.Copy already wrote the second chunk into the .part file.
		<-s.ctx.Done()
		return 0, s.ctx.Err()
	}
}

func (s *bootDownloadStream) Close() error {
	s.once.Do(func() {
		s.closedMu.Lock()
		s.closedStamp = bootStampEvent()
		s.closedMu.Unlock()
		close(s.closed)
	})
	return nil
}

// bodyClosedStamp returns the close event's position in the event order, or
// the unset stamp if Close has not run yet. The stamp is taken at the event
// site (inside Close), so an observer that learns of the event later still
// sees the event's own position.
func (s *bootDownloadStream) bodyClosedStamp() bootEventStamp {
	s.closedMu.Lock()
	defer s.closedMu.Unlock()
	return s.closedStamp
}

// bootDownloadInFlight boots the real loadConfigAndProvider +
// initializeAgentLoop with managed-browser provisioning forced, holds the
// preprovision archive download open mid-first-chunk, and returns the
// run context, the stream and the .part path. The download is provably
// pending: one chunk is on disk and the second read is blocked.
func bootDownloadInFlight(t *testing.T) (*runContextWithOptions, *bootDownloadStream, string) {
	t.Helper()
	// Precondition, not a tolerance: on Linux the managed Chrome download
	// exists only for amd64. On any other Linux architecture production
	// refuses before a single byte is requested
	// (pkg/tools/browser/installer.go::cftPlatform: "chrome-for-testing has
	// no linux/<arch> build"), so there is no in-flight download for a boot
	// drain to join; the Cross-Platform ubuntu-24.04-arm leg proved it.
	if runtime.GOOS == "linux" && runtime.GOARCH != "amd64" {
		t.Skipf("no managed Chrome download exists on linux/%s (installer.go::cftPlatform refuses), so this instrument cannot hold one open", runtime.GOARCH)
	}
	home, _ := bootFixture(t)
	bootEditedPlan(t, home)
	t.Setenv("OMNIPUS_BROWSER_FORCE_MANAGED", "1")
	ctx, cancel := context.WithCancel(context.Background())
	stream := &bootDownloadStream{ready: make(chan struct{}), release: make(chan struct{}), wrote: make(chan struct{}), closed: make(chan struct{})}
	old := http.DefaultTransport
	http.DefaultTransport = bootDownloadTransport(func(r *http.Request) (*http.Response, error) {
		var body io.ReadCloser
		if strings.Contains(r.URL.Path, "last-known-good-versions-with-downloads.json") {
			manifest := `{"channels":{"Stable":{"version":"bootdl","downloads":{"chrome":[{"platform":"linux64","url":"https://bootdl.test/archive.zip"}],"chrome-headless-shell":[{"platform":"linux64","url":"https://bootdl.test/archive.zip"}]}}}}`
			body = io.NopCloser(strings.NewReader(manifest))
		} else if r.URL.Host == "bootdl.test" {
			stream.ctx = r.Context()
			body = stream
		} else {
			return nil, errorTransportRefusal(r)
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: body, Request: r}, nil
	})
	t.Cleanup(func() {
		cancel()
		close(stream.release) // never leak a blocked reader into a later test
		http.DefaultTransport = old
	})
	rc := &runContextWithOptions{ctx: ctx, homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	require.NoError(t, err)
	require.False(t, stop)
	rc.cfg.Tools.Browser.ExecPath = ""
	rc.cfg.Tools.Browser.CDPURL = ""
	rc.provider = &restMockProvider{}
	err, stop = rc.initializeAgentLoop()
	require.NoError(t, err)
	require.False(t, stop)

	select {
	case <-stream.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: managed download did not reach the blocked second read")
	}
	parts, globErr := filepath.Glob(filepath.Join(home, "browser", "chromium", "bootdl", "*.part-*"))
	require.NoError(t, globErr)
	require.Len(t, parts, 1, "instrument: one real partial download must exist before teardown")
	info, statErr := os.Stat(parts[0])
	require.NoError(t, statErr)
	require.EqualValues(t, len("before"), info.Size(), "instrument: the first chunk really reached disk")
	return rc, stream, parts[0]
}

func errorTransportRefusal(r *http.Request) error {
	return errors.New("boot download instrument forbids external request: " + r.URL.Host)
}

// bootEventStamp is one oracle event's position in the test's process-wide
// event order: the sequence number its event site took from bootStampEvent,
// with set=false when the site never ran. Sequence numbers — not clock
// readings — decide the verdict, so two events inside one clock tick are
// still ordered (CHECK 3 finding R2: tied wall-clock stamps used to bless an
// illegal order), and a missing stamp is missing evidence, never a pass.
type bootEventStamp struct {
	seq uint64
	set bool
}

// bootEventClock allocates the sequence numbers under one mutex: the same
// lock orders every stamp site, so the numbers form one monotonic event
// order no matter what the wall clock was doing.
var (
	bootEventClockMu  sync.Mutex
	bootEventClockSeq uint64
)

// bootStampEvent is the only way to mint a set stamp: call it at the event
// site, before the event's channel fires.
func bootStampEvent() bootEventStamp {
	bootEventClockMu.Lock()
	defer bootEventClockMu.Unlock()
	bootEventClockSeq++
	return bootEventStamp{seq: bootEventClockSeq, set: true}
}

// joinVerdict is the oracle's verdict over two event stamps.
type joinVerdict int

const (
	// joinLegal: the body's close is provably at or before the caller's
	// return — the join held.
	joinLegal joinVerdict = iota
	// joinInverted: the caller provably returned before the body closed —
	// the download was cancelled but NOT joined.
	joinInverted
	// joinUnprovable: a stamp is missing, so there is no order evidence at
	// all. Missing evidence fails the oracle; it is never accepted (CHECK 3
	// finding R2: the previous helper treated it as acceptance).
	joinUnprovable
)

// awaitBodyClosedBefore fails unless the download's HTTP body is closed
// BEFORE `caller` returns. This is the JOIN property in its strict form: the
// download goroutine closes its body as the last act before its WaitGroup
// Done, so a drain that joins it must come back only after the body is
// closed.
//
// The verdict is decided by comparing the two events' positions in the
// process-wide event order (bootStampEvent under one mutex), never by racing
// their channels and never by comparing clock readings: a select between two
// ready channels picks arbitrarily — the first shape here rejected 18 of 32
// legal already-finished schedules (CHECK 2, 2026-11-08, defect 2) — and a
// wall-clock comparison blessed an illegal order whenever the two stamps
// tied or one was missing (CHECK 3 finding R2, same day). Sequence numbers
// order same-tick events; missing evidence is joinUnprovable and fails. The
// two waits below are hang guards for genuine breakage (a body never
// closed, a caller never returning), not tolerances: there is no sleep and
// no elapsed-time threshold anywhere in the verdict.
//
// callerReturnedBeforeBodyClosed is that verdict as a pure predicate over
// the two stamps. It is a function so
// TestAwaitBodyClosedBefore_OracleOrdering can pin every verdict positively
// — a rejection pinned by a nested failing probe would fail its parent test
// by construction, not by verdict.
func callerReturnedBeforeBodyClosed(callerStamp, bodyStamp bootEventStamp) joinVerdict {
	if !callerStamp.set || !bodyStamp.set {
		return joinUnprovable
	}
	if callerStamp.seq < bodyStamp.seq {
		return joinInverted
	}
	return joinLegal
}

func awaitBodyClosedBefore(t *testing.T, stream *bootDownloadStream, callerReturned <-chan struct{}, callerStamp func() bootEventStamp, what string) {
	t.Helper()
	select {
	case <-stream.closed:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never closed the managed download's body: the drain neither joined it nor cancelled it", what)
	}
	select {
	case <-callerReturned:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never returned", what)
	}
	caller, body := callerStamp(), stream.bodyClosedStamp()
	switch callerReturnedBeforeBodyClosed(caller, body) {
	case joinLegal:
		// joined: the body's close provably did not follow the caller's return.
	case joinInverted:
		t.Fatalf("%s returned before the managed download's body was closed: the download was cancelled but NOT joined by the drain", what)
	default:
		t.Fatalf("%s: the join oracle is missing event-order evidence (caller stamped: %v, body stamped: %v); missing evidence fails, it is never accepted", what, caller.set, body.set)
	}
}

// TestPreprovisionDownload_CancelledAndJoinedByBootDrain kills the writer
// mutant that drops the preprovision download from the boot WaitGroup
// (CHECK 1 finding F5): the boot drain must return only after the download
// goroutine has exited — its body closed and its .part removed. An unjoined
// download is still inside its 25 ms cancellation acknowledgement when the
// drain returns, which is exactly what the ordering assertion catches.
func TestPreprovisionDownload_CancelledAndJoinedByBootDrain(t *testing.T) {
	rc, stream, partial := bootDownloadInFlight(t)
	// The same teardown every boot test registers, so the assertion below is
	// about the production drain shape, not a private helper.
	stopBootWritersOnCleanup(t, rc)
	stopCtx, stopCancel := context.WithTimeout(context.Background(), bootBackgroundStopTimeout)
	defer stopCancel()
	drainReturned := make(chan struct{})
	var drainReturnedStamp bootEventStamp
	go func() {
		rc.stopBootBackgroundWriters(stopCtx)
		drainReturnedStamp = bootStampEvent() // stamped at the event site, before the signal
		close(drainReturned)
	}()

	awaitBodyClosedBefore(t, stream, drainReturned, func() bootEventStamp { return drainReturnedStamp }, "boot drain")
	// Secondary witness, bounded: the partial is removed inside the joined
	// goroutine, which can legally finish its own cleanup just after the
	// transport's body close this oracle synced on.
	require.Eventually(t, func() bool {
		_, err := os.Stat(partial)
		return errors.Is(err, os.ErrNotExist)
	}, 2*time.Second, 10*time.Millisecond, "the cancelled partial must be gone once the download was joined")
}

// TestSandboxAbortDrainsStartedBootWriters is the regression for CHECK 1
// finding F1: every stop=true return of validateAndApplySandbox happens
// AFTER initializeAgentLoop started the boot-stage writers, so the abort
// itself must cancel and join them before returning — the caller (and every
// test harness) treats this return as "the gateway is gone" and removes
// homePath right after it. The real sandbox-apply abort branch is reached
// with an invalid mode; the download started above must be joined inside
// that call, body closed before the call comes back.
func TestSandboxAbortDrainsStartedBootWriters(t *testing.T) {
	rc, stream, _ := bootDownloadInFlight(t)
	rc.opts.SandboxMode = "bootdl-invalid"
	abortReturned := make(chan struct{})
	var abortReturnedStamp bootEventStamp
	var err error
	var stop bool
	go func() {
		err, stop = rc.validateAndApplySandbox()
		abortReturnedStamp = bootStampEvent() // stamped at the event site, before the signal
		close(abortReturned)
	}()

	awaitBodyClosedBefore(t, stream, abortReturned, func() bootEventStamp { return abortReturnedStamp }, "sandbox validation abort")
	<-abortReturned // the goroutine writes err/stop; read them only after it returned

	require.True(t, stop, "instrument: the real sandbox-validation abort branch was reached")
	require.Error(t, err)
	var bootErr *SandboxBootError
	require.True(t, errors.As(err, &bootErr), "expected a sandbox boot error, got %v", err)
}

// TestAwaitBodyClosedBefore_OracleOrdering pins the join oracle itself, so
// its verdicts are facts about schedules and not coin flips (CHECK 2,
// 2026-11-08, defect 2: the first select-based oracle rejected 18 of 32
// legal already-finished schedules; CHECK 3 finding R2, same day: the
// wall-clock replacement accepted illegal orders whenever the two stamps
// tied or one was missing, and its live case slept). The verdict table
// asserts every verdict positively on the pure predicate — legal schedules
// accepted; the illegal schedules (caller provably before the body close,
// distinct stamps or within one tick) and the missing-evidence schedules
// rejected — and the full helper is then driven on real schedules: 32
// repeats of the already-finished case the first select got wrong, a live
// schedule handed over by a channel (no sleep), and synctest bubbles where
// both events land in ONE clock tick in both orders — the schedules a
// wall-clock verdict cannot decide. Deterministic throughout: no sleeps, no
// elapsed-time thresholds, and the whole table runs under -count=100.
func TestAwaitBodyClosedBefore_OracleOrdering(t *testing.T) {
	newStream := func() *bootDownloadStream {
		return &bootDownloadStream{ready: make(chan struct{}), release: make(chan struct{}), wrote: make(chan struct{}), closed: make(chan struct{})}
	}
	t.Run("verdict", func(t *testing.T) {
		stamp := func(seq uint64) bootEventStamp { return bootEventStamp{seq: seq, set: true} }
		unset := bootEventStamp{}
		cases := []struct {
			name                   string
			callerStamp, bodyStamp bootEventStamp
			want                   joinVerdict
		}{
			{"legal body before caller, distinct stamps", stamp(2), stamp(1), joinLegal},
			{"legal body before caller, later positions", stamp(9), stamp(8), joinLegal},
			{"illegal caller before body, distinct stamps", stamp(1), stamp(2), joinInverted},
			{"missing caller stamp rejected", unset, stamp(1), joinUnprovable},
			{"missing body stamp rejected", stamp(1), unset, joinUnprovable},
			{"both stamps missing rejected", unset, unset, joinUnprovable},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, tc.want, callerReturnedBeforeBodyClosed(tc.callerStamp, tc.bodyStamp))
			})
		}
	})
	t.Run("same tick, legal order accepted", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			stream := newStream()
			returned := make(chan struct{})
			require.NoError(t, stream.Close()) // the body's event first
			bodyStamp, bodyAt := stream.bodyClosedStamp(), time.Now()
			returnedStamp, returnedAt := bootStampEvent(), time.Now() // the caller's event second
			close(returned)
			require.Equal(t, bodyAt, returnedAt, "SETUP: the bubble clock must hold both events in one tick for this to be a same-tick schedule")
			require.True(t, returnedStamp.seq > bodyStamp.seq, "SETUP: the sequencer must order what the clock cannot distinguish")
			require.Equal(t, joinLegal, callerReturnedBeforeBodyClosed(returnedStamp, bodyStamp), "a legal same-tick completion must be accepted, not rejected with every tie")
		})
	})
	t.Run("same tick, illegal order rejected", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			stream := newStream()
			returned := make(chan struct{})
			returnedStamp, returnedAt := bootStampEvent(), time.Now() // the caller's return FIRST
			close(returned)
			require.NoError(t, stream.Close()) // the body's close SECOND
			bodyStamp, bodyAt := stream.bodyClosedStamp(), time.Now()
			require.Equal(t, returnedAt, bodyAt, "SETUP: the bubble clock must hold both events in one tick for this to be a same-tick schedule")
			require.True(t, returnedStamp.seq < bodyStamp.seq, "SETUP: the sequencer must order what the clock cannot distinguish")
			require.Equal(t, joinInverted, callerReturnedBeforeBodyClosed(returnedStamp, bodyStamp), "an illegal same-tick return-before-close must be rejected, not blessed by a tied clock reading")
		})
	})
	t.Run("legal body-closed-then-caller-returned, both already complete", func(t *testing.T) {
		for i := 0; i < 32; i++ {
			stream := newStream()
			returned := make(chan struct{})
			stream.Close() // body's event first
			returnedStamp := bootStampEvent()
			close(returned)
			awaitBodyClosedBefore(t, stream, returned, func() bootEventStamp { return returnedStamp }, "correctly completed caller")
		}
	})
	t.Run("legal live schedule accepted", func(t *testing.T) {
		stream := newStream()
		returned := make(chan struct{})
		var returnedStamp bootEventStamp
		oracleEntering := make(chan struct{})
		go func() {
			<-oracleEntering // handoff: the events are released at the oracle's entry — no sleep
			stream.Close()
			returnedStamp = bootStampEvent()
			close(returned)
		}()
		close(oracleEntering)
		awaitBodyClosedBefore(t, stream, returned, func() bootEventStamp { return returnedStamp }, "joined drain")
	})
}
