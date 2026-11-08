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

func (s *bootDownloadStream) Close() error { s.once.Do(func() { close(s.closed) }); return nil }

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
// closed. A wait window here would gut the oracle — a drain that returned
// WITHOUT the join comes back milliseconds early, and a body that closes on
// its own 25 ms later (the cancellation acknowledgement) would still land
// inside any generous window; the ordering race has no such slack.
func awaitBodyClosedBefore(t *testing.T, stream *bootDownloadStream, callerReturned <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-stream.closed:
	case <-callerReturned:
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
	go func() {
		rc.stopBootBackgroundWriters(stopCtx)
		close(drainReturned)
	}()

	awaitBodyClosedBefore(t, stream, drainReturned, "boot drain")
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
	var err error
	var stop bool
	go func() {
		err, stop = rc.validateAndApplySandbox()
		close(abortReturned)
	}()

	awaitBodyClosedBefore(t, stream, abortReturned, "sandbox validation abort")
	<-abortReturned // the goroutine writes err/stop; read them only after it returned

	require.True(t, stop, "instrument: the real sandbox-validation abort branch was reached")
	require.Error(t, err)
	var bootErr *SandboxBootError
	require.True(t, errors.As(err, &bootErr), "expected a sandbox boot error, got %v", err)
}
