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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type bootDownloadTransport func(*http.Request) (*http.Response, error)

func (f bootDownloadTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// bootDownloadStream is the held-open archive body: one real chunk reaches
// the installer's .part file, the second read blocks on the request context
// (cancelled by the boot drain) with a finite 25 ms acknowledgement, and
// Close records the moment the HTTP client actually gave up on the body.
type bootDownloadStream struct {
	ctx                           context.Context
	ready, release, wrote, closed chan struct{}
	step                          int
	once                          sync.Once

	// closedAt is WHEN Close ran, stamped under closedMu before the closed
	// channel fires, so the join oracle can compare event times instead of
	// racing two ready channels (CHECK 2, 2026-11-08, defect 2).
	closedMu sync.Mutex
	closedAt time.Time
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
		s.closedAt = time.Now()
		s.closedMu.Unlock()
		close(s.closed)
	})
	return nil
}

// bodyClosedAt returns when Close ran, or the zero time if it has not run
// yet. The stamp is taken at the event site (inside Close), so an observer
// that learns of the event later still sees the event's own time.
func (s *bootDownloadStream) bodyClosedAt() time.Time {
	s.closedMu.Lock()
	defer s.closedMu.Unlock()
	return s.closedAt
}

// bootDownloadInFlight boots the real loadConfigAndProvider +
// initializeAgentLoop with managed-browser provisioning forced, holds the
// preprovision archive download open mid-first-chunk, and returns the
// run context, the stream and the .part path. The download is provably
// pending: one chunk is on disk and the second read is blocked.
func bootDownloadInFlight(t *testing.T) (*runContextWithOptions, *bootDownloadStream, string) {
	t.Helper()
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

// awaitBodyClosedBefore fails unless the download's HTTP body is closed
// BEFORE `caller` returns. This is the JOIN property in its strict form: the
// download goroutine closes its body as the last act before its WaitGroup
// Done, so a drain that joins it must come back only after the body is
// closed.
//
// The verdict is decided by comparing WHEN the two events happened, never by
// racing their channels: the stream stamps its Close at the event site
// (bootDownloadStream.Close), and the caller stamps its return at its own
// event site and hands the stamp over as callerAt (read only after
// callerReturned fires, so the write is ordered before the read). A select
// between two ready channels picks arbitrarily — the previous shape here
// rejected 18 of 32 legal already-finished schedules (CHECK 2, 2026-11-08,
// defect 2), a false alarm against a correct drain. Only a PROVEN inversion
// (caller stamped strictly before the body close) fails; schedules whose
// order cannot be proven, including both events already complete, are
// accepted. The two waits below are hang guards for genuine breakage (a
// body never closed, a caller never returning), not tolerances: there is no
// sleep and no elapsed-time threshold anywhere in the verdict.
// callerReturnedBeforeBodyClosed is the oracle's verdict as a pure predicate
// over the two event stamps: true only when the caller's stamp is strictly
// before the body's close stamp — a proven inversion. Unprovable orders (a
// zero stamp, or equal stamps from the same clock tick) are not inversions
// and return false. It is a function so TestAwaitBodyClosedBefore_OracleOrdering
// can pin every verdict positively — a rejection pinned by a nested failing
// probe would fail its parent test by construction, not by verdict.
func callerReturnedBeforeBodyClosed(callerAt, bodyClosedAt time.Time) bool {
	return !callerAt.IsZero() && !bodyClosedAt.IsZero() && callerAt.Before(bodyClosedAt)
}

func awaitBodyClosedBefore(t *testing.T, stream *bootDownloadStream, callerReturned <-chan struct{}, callerAt func() time.Time, what string) {
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
	if callerReturnedBeforeBodyClosed(callerAt(), stream.bodyClosedAt()) {
		t.Fatalf("%s returned before the managed download's body was closed: the download was cancelled but NOT joined by the drain", what)
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
	var drainReturnedAt time.Time
	go func() {
		rc.stopBootBackgroundWriters(stopCtx)
		drainReturnedAt = time.Now() // stamped at the event site, before the signal
		close(drainReturned)
	}()

	awaitBodyClosedBefore(t, stream, drainReturned, func() time.Time { return drainReturnedAt }, "boot drain")
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
	var abortReturnedAt time.Time
	var err error
	var stop bool
	go func() {
		err, stop = rc.validateAndApplySandbox()
		abortReturnedAt = time.Now() // stamped at the event site, before the signal
		close(abortReturned)
	}()

	awaitBodyClosedBefore(t, stream, abortReturned, func() time.Time { return abortReturnedAt }, "sandbox validation abort")
	<-abortReturned // the goroutine writes err/stop; read them only after it returned

	require.True(t, stop, "instrument: the real sandbox-validation abort branch was reached")
	require.Error(t, err)
	var bootErr *SandboxBootError
	require.True(t, errors.As(err, &bootErr), "expected a sandbox boot error, got %v", err)
}

// TestAwaitBodyClosedBefore_OracleOrdering pins the join oracle itself, so
// its verdicts are facts about schedules and not coin flips (CHECK 2,
// 2026-11-08, defect 2: the previous select-based oracle rejected 18 of 32
// legal already-finished schedules). The verdict table asserts every verdict
// positively on the pure predicate — the one illegal schedule (caller
// provably returned before the body closed) must be rejected, every legal
// and unprovable schedule accepted — and the full helper is then driven on
// real schedules: 32 repeats of the already-finished case the old select
// got wrong, plus a live schedule whose events land while the oracle is
// already waiting. Deterministic throughout: stamps are constructed from
// one clock reading, no concurrency, no sleeps beyond the live case's one
// finite hand-off, and the whole table runs under -count=100.
func TestAwaitBodyClosedBefore_OracleOrdering(t *testing.T) {
	newStream := func() *bootDownloadStream {
		return &bootDownloadStream{ready: make(chan struct{}), release: make(chan struct{}), wrote: make(chan struct{}), closed: make(chan struct{})}
	}
	t.Run("verdict", func(t *testing.T) {
		t0 := time.Now()
		cases := []struct {
			name               string
			callerAt, bodyAt   time.Time
			wantCallerProvenLt bool
		}{
			{"illegal caller before body", t0, t0.Add(time.Second), true},
			{"legal body before caller", t0.Add(time.Second), t0, false},
			{"tie accepted", t0, t0, false},
			{"zero caller stamp accepted", time.Time{}, t0, false},
			{"zero body stamp accepted", t0, time.Time{}, false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.Equal(t, tc.wantCallerProvenLt, callerReturnedBeforeBodyClosed(tc.callerAt, tc.bodyAt))
			})
		}
	})
	t.Run("legal body-closed-then-caller-returned, both already complete", func(t *testing.T) {
		for i := 0; i < 32; i++ {
			stream := newStream()
			returned := make(chan struct{})
			stream.Close() // body's event first
			returnedAt := time.Now()
			close(returned)
			awaitBodyClosedBefore(t, stream, returned, func() time.Time { return returnedAt }, "correctly completed caller")
		}
	})
	t.Run("legal live schedule accepted", func(t *testing.T) {
		stream := newStream()
		returned := make(chan struct{})
		var returnedAt time.Time
		go func() {
			time.Sleep(25 * time.Millisecond) // both events land while the oracle is already waiting on them
			stream.Close()
			returnedAt = time.Now()
			close(returned)
		}()
		awaitBodyClosedBefore(t, stream, returned, func() time.Time { return returnedAt }, "joined drain")
	})
	t.Run("unstamped caller accepted when order cannot be proven", func(t *testing.T) {
		stream := newStream()
		returned := make(chan struct{})
		close(returned)
		stream.Close()
		awaitBodyClosedBefore(t, stream, returned, func() time.Time { return time.Time{} }, "unstamped caller")
	})
}
