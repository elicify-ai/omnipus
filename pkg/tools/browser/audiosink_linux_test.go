//go:build linux

package browser

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- A minimal, independent PulseAudio native-protocol mock server ---
//
// This deliberately hand-decodes wire bytes with encoding/binary rather than
// importing pkg/audio/pulse's (unexported) internals, so a framing bug in
// the real client can't hide behind a mock built from the same code.

type fakeModuleCall struct {
	name string
	args string
}

// fakePulseFixture spawns a fresh mock PulseAudio listener each time its
// spawn method is invoked (once per pulseSidecar (re)start), always on the
// SAME socket/cookie paths taken from AudioConfig — proving Test 31's
// "restart reuses the same socket path" property structurally, not just by
// assertion.
type fakePulseFixture struct {
	mu         sync.Mutex
	calls      []fakeModuleCall
	socketUsed []string
}

func (f *fakePulseFixture) recordPaths(cfg AudioConfig) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.socketUsed = append(f.socketUsed, socketPathFor(cfg))
}

func (f *fakePulseFixture) recordCall(c fakeModuleCall) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakePulseFixture) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakePulseFixture) snapshotCalls() []fakeModuleCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeModuleCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// spawn implements the `spawner` seam (audiosink_linux.go): instead of
// exec'ing a real `pulseaudio` binary, it starts an in-process mock speaking
// just enough of the native protocol (AUTH, SET_CLIENT_NAME, LOAD_MODULE)
// and returns a fakeProcess standing in for the real *exec.Cmd-backed one.
// Mirrors real PulseAudio's own stale-socket cleanup (a fresh daemon start
// unlinks a leftover socket file before binding) so a restart can rebind the
// identical path.
func (f *fakePulseFixture) spawn(cfg AudioConfig) (process, error) {
	f.recordPaths(cfg)

	_ = os.Remove(socketPathFor(cfg)) // stale-socket cleanup, mirrors real pulseaudio
	ln, err := net.Listen("unix", socketPathFor(cfg))
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(cookiePathFor(cfg), []byte("fixture-cookie"), 0o600); err != nil {
		ln.Close()
		return nil, err
	}

	proc := newFakeProcess(func() { ln.Close() })
	go f.acceptLoop(ln, proc)
	return proc, nil
}

func (f *fakePulseFixture) acceptLoop(ln net.Listener, proc *fakeProcess) {
	conn, err := ln.Accept()
	if err != nil {
		return // listener closed (Terminate, or test cleanup)
	}
	defer conn.Close()

	moduleIdx := uint32(0)
	for {
		hdr := make([]byte, 20)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			return
		}
		n := binary.BigEndian.Uint32(hdr[0:4])
		payload := make([]byte, n)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		br := bytes.NewReader(payload)
		_, cmdVal := readTaggedU32(br)
		_, seqVal := readTaggedU32(br)

		switch cmdVal {
		case 8: // AUTH: version + arbitrary cookie
			readTaggedU32(br) // version, unchecked
			readArbitrary(br) // cookie, unchecked (fixture accepts anything)
			writeFramed(conn, replyPayload(seqVal, func(w *[]byte) { appendU32(w, 32) }))
		case 9: // SET_CLIENT_NAME: proplist body, unchecked
			writeFramed(conn, replyPayload(seqVal, func(w *[]byte) { appendU32(w, 0) }))
		case 51: // LOAD_MODULE: name, args
			name := readString(br)
			args := readString(br)
			f.recordCall(fakeModuleCall{name: name, args: args})
			moduleIdx++
			idx := moduleIdx
			writeFramed(conn, replyPayload(seqVal, func(w *[]byte) { appendU32(w, idx) }))
		default:
			return
		}
	}
}

func readTaggedU32(r *bytes.Reader) (tag byte, val uint32) {
	t, _ := r.ReadByte()
	var b [4]byte
	io.ReadFull(r, b[:])
	return t, binary.BigEndian.Uint32(b[:])
}

func readString(r *bytes.Reader) string {
	r.ReadByte() // 't'/'N' tag
	var sb bytes.Buffer
	for {
		c, err := r.ReadByte()
		if err != nil || c == 0 {
			break
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

func readArbitrary(r *bytes.Reader) []byte {
	r.ReadByte() // 'x' tag
	var lb [4]byte
	io.ReadFull(r, lb[:])
	n := binary.BigEndian.Uint32(lb[:])
	b := make([]byte, n)
	io.ReadFull(r, b)
	return b
}

func appendU32(w *[]byte, v uint32) {
	*w = append(*w, 'L')
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	*w = append(*w, b[:]...)
}

// replyPayload builds a REPLY (command=2) tagstruct echoing the request's
// tag, followed by whatever the caller appends.
func replyPayload(tag uint32, appendBody func(w *[]byte)) []byte {
	var w []byte
	appendU32(&w, 2) // commandReply
	appendU32(&w, tag)
	if appendBody != nil {
		appendBody(&w)
	}
	return w
}

func writeFramed(conn net.Conn, payload []byte) {
	var hdr [20]byte
	binary.BigEndian.PutUint32(hdr[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint32(hdr[4:8], 0xffffffff)
	conn.Write(hdr[:])
	conn.Write(payload)
}

// fakeProcess is the hermetic stand-in for a real *exec.Cmd-backed process
// (the `process` interface in audiosink_linux.go), letting tests simulate a
// crash (Test 31) without touching a real OS process.
type fakeProcess struct {
	mu      sync.Mutex
	done    chan struct{}
	exitErr error
	onStop  func()
}

func newFakeProcess(onStop func()) *fakeProcess {
	return &fakeProcess{done: make(chan struct{}), onStop: onStop}
}

func (f *fakeProcess) Wait() error {
	<-f.done
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exitErr
}

// simulateCrash marks the fake process exited, exactly once, as if it had
// crashed on its own (distinct from Terminate, which is what a supervisor-
// initiated Stop calls).
func (f *fakeProcess) simulateCrash(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	select {
	case <-f.done:
		return
	default:
	}
	f.exitErr = err
	close(f.done)
}

func (f *fakeProcess) Terminate() error {
	if f.onStop != nil {
		f.onStop()
	}
	f.simulateCrash(nil)
	return nil
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

func testAudioConfig(t *testing.T) AudioConfig {
	t.Helper()
	return AudioConfig{
		SocketDir:         t.TempDir(),
		SinkName:          "vspk",
		SourceName:        "vmic",
		StartTimeout:      5 * time.Second,
		RestartBackoff:    20 * time.Millisecond,
		MaxRestartBackoff: 100 * time.Millisecond,
	}
}

// TestPulseSidecar_SpawnLoadModules_StableSocket is Test 18
// (live-browser-video-streaming-spec.md): spawn on a stable socket + load
// module-null-sink/module-remap-source via the pure-Go native-protocol
// client, and — critically — Start must not block the caller (FR-022's
// "never blocks the Chrome launch").
func TestPulseSidecar_SpawnLoadModules_StableSocket(t *testing.T) {
	cfg := testAudioConfig(t)
	fixture := &fakePulseFixture{}

	sidecar := NewAudioSidecar(cfg).(*pulseSidecar)
	sidecar.spawnFn = fixture.spawn
	defer sidecar.Stop()

	startBegin := time.Now()
	if err := sidecar.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	startElapsed := time.Since(startBegin)

	// The non-blocking guarantee, proven directly: Start must return almost
	// immediately — bring-up (spawn + socket/cookie wait + PA handshake +
	// two LoadModule round trips) all happens in the background. A generous
	// bound (well under the 5s StartTimeout) still catches any regression
	// that makes Start synchronously wait on bring-up.
	if startElapsed > 200*time.Millisecond {
		t.Fatalf("Start blocked for %s — FR-022 requires Start to never block the Chrome launch", startElapsed)
	}

	if got, want := sidecar.PulseServer(), "unix:"+socketPathFor(cfg); got != want {
		t.Fatalf("PulseServer() = %q, want %q", got, want)
	}

	waitUntil(t, 2*time.Second, sidecar.Healthy)

	waitUntil(t, 2*time.Second, func() bool { return fixture.callCount() >= 2 })
	calls := fixture.snapshotCalls()
	if len(calls) != 2 {
		t.Fatalf("expected exactly 2 LoadModule calls, got %d: %+v", len(calls), calls)
	}
	if calls[0].name != "module-null-sink" || calls[0].args != "sink_name=vspk" {
		t.Fatalf("unexpected null-sink call: %+v", calls[0])
	}
	if calls[1].name != "module-remap-source" || calls[1].args != "master=vspk.monitor source_name=vmic" {
		t.Fatalf("unexpected remap-source call (Chrome hides raw .monitor sources, remap is required): %+v", calls[1])
	}
}

// TestPulseSidecar_RestartReEnumeratesDevice is Test 31: after a mid-run
// crash, the sidecar restarts on the exact same socket path and reloads the
// null-sink + remap-source modules, without ever changing PulseServer()'s
// value — the property the AC needs to let Chrome resume audio on the next
// stream (or, failing re-enumeration, the next Chrome launch — either way,
// this proves the sidecar side never causes a permanent audio loss).
func TestPulseSidecar_RestartReEnumeratesDevice(t *testing.T) {
	cfg := testAudioConfig(t)
	fixture := &fakePulseFixture{}

	// FR-019 (Test 28/O-4): a real crash+restart cycle must fire
	// IncPulseSidecarRestart exactly once via the registered recorder — the
	// end-to-end proof for the pulse_sidecar_restart_total metric (see
	// pkg/gateway/browser_metrics_test.go for the registry-level proof and
	// why this specific hook is verified here instead).
	metrics := &fakeSidecarMetricsRecorder{}
	swapMetricsRecorder(t, metrics)

	sidecar := NewAudioSidecar(cfg).(*pulseSidecar)
	sidecar.spawnFn = fixture.spawn
	defer sidecar.Stop()

	if err := sidecar.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, 2*time.Second, sidecar.Healthy)
	waitUntil(t, 2*time.Second, func() bool { return fixture.callCount() >= 2 })

	pulseServerBefore := sidecar.PulseServer()

	sidecar.mu.Lock()
	firstProc, ok := sidecar.proc.(*fakeProcess)
	sidecar.mu.Unlock()
	if !ok || firstProc == nil {
		t.Fatalf("expected a live fakeProcess after Start, got %#v", firstProc)
	}

	// Simulate a mid-run daemon crash (Test 31's "restarts on the same
	// socket path" scenario) — NOT a Stop()-initiated exit. The unhealthy
	// window between crash and restart-complete is real but can be too
	// short to reliably observe via polling against an in-memory fake with
	// no I/O latency, so this asserts on the durable post-restart state
	// (fresh module-load pair + healthy again), not the transient dip.
	firstProc.simulateCrash(errors.New("simulated pulseaudio crash"))

	waitUntil(t, 2*time.Second, func() bool { return fixture.callCount() >= 4 })
	waitUntil(t, 2*time.Second, sidecar.Healthy)

	if got := sidecar.PulseServer(); got != pulseServerBefore {
		t.Fatalf("PulseServer() changed across restart: before %q, after %q", pulseServerBefore, got)
	}

	fixture.mu.Lock()
	pathsUsed := append([]string(nil), fixture.socketUsed...)
	fixture.mu.Unlock()
	if len(pathsUsed) != 2 {
		t.Fatalf("expected exactly 2 spawns (initial + one restart), got %d: %v", len(pathsUsed), pathsUsed)
	}
	if pathsUsed[0] != pathsUsed[1] {
		t.Fatalf("restart used a different socket path: %q vs %q", pathsUsed[0], pathsUsed[1])
	}

	calls := fixture.snapshotCalls()
	if len(calls) != 4 {
		t.Fatalf("expected 4 LoadModule calls (2 before + 2 after restart), got %d: %+v", len(calls), calls)
	}
	// The post-restart pair must be the same module load sequence as the
	// initial bring-up — this is the "device re-enumeration" the AC needs.
	if calls[2].name != "module-null-sink" || calls[3].name != "module-remap-source" {
		t.Fatalf("post-restart calls were not a fresh null-sink+remap-source load: %+v", calls[2:])
	}

	if got := metrics.pulseRestart.Load(); got != 1 {
		t.Fatalf("expected exactly one FR-019 pulse sidecar restart metric increment, got %d", got)
	}
}

// TestPulseSidecar_Stop_TerminatesProcessAndIsIdempotent proves Stop cleanly
// tears down a healthy sidecar and is safe to call more than once.
func TestPulseSidecar_Stop_TerminatesProcessAndIsIdempotent(t *testing.T) {
	cfg := testAudioConfig(t)
	fixture := &fakePulseFixture{}

	sidecar := NewAudioSidecar(cfg).(*pulseSidecar)
	sidecar.spawnFn = fixture.spawn

	if err := sidecar.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, 2*time.Second, sidecar.Healthy)

	sidecar.Stop()
	if sidecar.Healthy() {
		t.Fatal("expected Healthy() == false after Stop")
	}
	// Idempotent: a second Stop must not panic or hang.
	sidecar.Stop()
}

// TestPulseSidecar_SpawnFailure_RetriesWithoutPanicking proves a persistently
// failing spawn (e.g. pulseaudio missing) degrades to "audio absent" forever
// rather than panicking or wedging Stop().
func TestPulseSidecar_SpawnFailure_RetriesWithoutPanicking(t *testing.T) {
	cfg := testAudioConfig(t)
	cfg.RestartBackoff = 5 * time.Millisecond
	cfg.MaxRestartBackoff = 20 * time.Millisecond

	var attempts int32
	var mu sync.Mutex
	sidecar := NewAudioSidecar(cfg).(*pulseSidecar)
	sidecar.spawnFn = func(AudioConfig) (process, error) {
		mu.Lock()
		attempts++
		mu.Unlock()
		return nil, errors.New("pulseaudio not found on PATH")
	}

	if err := sidecar.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitUntil(t, 1*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return attempts >= 3
	})
	if sidecar.Healthy() {
		t.Fatal("expected Healthy() == false when spawn always fails")
	}
	sidecar.Stop()
}

// TestPulseSidecar_BoundedRestartAttempts_GivesUp is the SF5 regression test:
// a permanently-failing spawn (e.g. `pulseaudio` never on PATH) must NOT
// retry forever — the supervisor gives up after pulseMaxRestartAttempts
// consecutive failures, leaving Healthy() false and never spawning again,
// mirroring the Xvfb sidecar's bounded-restart posture
// (TestXvfbSidecar_SpawnSuperviseRestart's "bounded restart attempts"
// subtest).
func TestPulseSidecar_BoundedRestartAttempts_GivesUp(t *testing.T) {
	cfg := testAudioConfig(t)
	cfg.RestartBackoff = 2 * time.Millisecond
	cfg.MaxRestartBackoff = 5 * time.Millisecond

	var attempts int32
	sidecar := NewAudioSidecar(cfg).(*pulseSidecar)
	sidecar.spawnFn = func(AudioConfig) (process, error) {
		atomic.AddInt32(&attempts, 1)
		return nil, errors.New("pulseaudio not found on PATH")
	}
	defer sidecar.Stop()

	if err := sidecar.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Eventually the supervisor exhausts pulseMaxRestartAttempts and gives
	// up; the spawn count converges to pulseMaxRestartAttempts and stays
	// there (proves the retry loop is bounded, not infinite).
	waitUntil(t, 2*time.Second, func() bool {
		return atomic.LoadInt32(&attempts) >= int32(pulseMaxRestartAttempts)
	})
	if sidecar.Healthy() {
		t.Fatal("expected Healthy() == false after exhausting restart attempts")
	}

	stalled := atomic.LoadInt32(&attempts)
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&attempts); got != stalled {
		t.Fatalf("expected spawn attempts to stop at %d once the bound is exceeded, got %d (retried forever)", stalled, got)
	}
	if sidecar.Healthy() {
		t.Fatal("expected Healthy() to stay false")
	}
}
