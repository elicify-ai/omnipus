package cdppipe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
)

// defaultDialTimeout bounds the CDP liveness probe that confirms the pipe is
// actually carrying traffic before NewPipeAllocator returns.
const defaultDialTimeout = 20 * time.Second

// PipeOptions configures a --remote-debugging-pipe launch.
type PipeOptions struct {
	// ExecPath overrides the execPath argument to NewPipeAllocator when non-empty.
	ExecPath string

	// Args are additional Chrome command-line flags. The CALLER owns all
	// behavioral flags — headful vs --headless, --window-size, DISPLAY-driven
	// mode, the Puppeteer hardening set, proxy, etc. cdppipe adds only the
	// minimal flags required to launch cleanly over the pipe (see buildArgs) and
	// deliberately never forces a headless / window-size / display choice.
	Args []string

	// Env is appended to os.Environ for the child (e.g. DISPLAY=:99,
	// PULSE_SERVER=...). When empty the child inherits the parent environment.
	Env []string

	// ModifyCmd runs against the *exec.Cmd just before Start, for process
	// attributes (SysProcAttr) and for the caller to capture the *exec.Cmd for
	// PID / crash handling — the coordinator's existing chromedp.ModifyCmdFunc
	// pattern. cdppipe owns cmd.ExtraFiles / Stdout / Stderr; do NOT reassign
	// them from ModifyCmd (they are set after this hook runs and will win).
	ModifyCmd func(*exec.Cmd)

	// UserDataDir is the Chrome profile directory. When empty a temporary dir is
	// created and removed on teardown.
	UserDataDir string

	// DialTimeout bounds the post-launch CDP liveness probe. Defaults to 20s.
	DialTimeout time.Duration

	// Logf / Errf receive diagnostics (nil silences them). Debugf, if set, logs
	// every CDP frame in both directions (extremely verbose).
	Logf, Errf, Debugf func(string, ...any)
}

// NewPipeAllocator launches Chromium under --remote-debugging-pipe and returns a
// context ready for chromedp actions.
//
// The returned context behaves exactly like one from
// chromedp.NewExecAllocator + chromedp.NewContext — chromedp.Run(ctx, ...) drives
// the browser and chromedp.NewContext(ctx, ...) creates child tabs multiplexed
// over the single pipe — but with NO TCP port, NO /json HTTP surface, and NO
// ws:// endpoint (see doc.go / EC-3). Chrome is already launched and the CDP
// transport verified live before this returns, so err reports launch and
// connectivity failures directly (fail closed).
//
// The effective binary is opts.ExecPath when set, else execPath. Because
// chromedp did not spawn the process, chromedp.Browser.Process() returns nil
// under this allocator — obtain the PID / crash handle from the *exec.Cmd
// captured via opts.ModifyCmd. Crash detection works through the standard
// chromedp.Browser.LostConnection channel.
//
// The returned CancelFunc tears the browser down: it cancels the chromedp
// context, closes the pipe (which makes Chrome exit), stops the in-memory
// bridge, and reaps the process. It is idempotent.
func NewPipeAllocator(
	parent context.Context,
	execPath string,
	opts PipeOptions,
) (context.Context, context.CancelFunc, error) {
	if opts.ExecPath != "" {
		execPath = opts.ExecPath
	}
	if execPath == "" {
		return nil, nil, errors.New("cdppipe: exec path is empty")
	}
	installDialer()

	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = defaultDialTimeout
	}

	// The chromedp context the pipe *Browser will be bound onto. parent must not
	// already be a chromedp context (the managed-Chrome launcher passes context.Background()).
	lifetime, lifetimeCancel := context.WithCancelCause(parent)
	ctx, baseCancel := chromedp.NewContext(lifetime)

	l := &launch{opts: opts, execPath: execPath, onExit: lifetimeCancel}
	if err := l.start(ctx); err != nil {
		baseCancel()
		lifetimeCancel(context.Canceled)
		l.teardown()
		return nil, nil, err
	}

	browserOpts := []chromedp.BrowserOption{chromedp.WithDialTimeout(dialTimeout)}
	if opts.Logf != nil {
		browserOpts = append(browserOpts, chromedp.WithBrowserLogf(opts.Logf))
	}
	if opts.Errf != nil {
		browserOpts = append(browserOpts, chromedp.WithBrowserErrorf(opts.Errf))
	}
	if opts.Debugf != nil {
		browserOpts = append(browserOpts, chromedp.WithBrowserDebugf(opts.Debugf))
	}

	// chromedp dials the synthetic host, our NetDial hook returns the in-memory
	// client conn, and the bridge completes the websocket handshake over it.
	b, err := chromedp.NewBrowser(ctx, l.wsURL, browserOpts...)
	if err != nil {
		err = l.startupError("connect browser over pipe", err, parent.Err())
		baseCancel()
		lifetimeCancel(context.Canceled)
		l.teardown()
		return nil, nil, err
	}

	// Bind the Browser onto the chromedp context via the exported field, so
	// chromedp.Run / NewContext use it instead of allocating a second process.
	chromedp.FromContext(ctx).Browser = b

	// Liveness probe: prove CDP actually flows over the pipe end to end before
	// handing the context back (fail closed on a dead / mis-launched Chrome).
	// Target.getTargets is a cheap browser-level round trip over the pipe.
	probeCtx, probeCancel := context.WithTimeout(ctx, dialTimeout)
	_, err = target.GetTargets().Do(cdp.WithExecutor(probeCtx, b))
	probeCancel()
	if err != nil {
		err = l.startupError("CDP liveness probe failed over pipe", err, parent.Err())
		baseCancel()
		lifetimeCancel(context.Canceled)
		l.teardown()
		return nil, nil, err
	}

	var cancelOnce sync.Once
	cancel := func() {
		cancelOnce.Do(func() {
			baseCancel()
			lifetimeCancel(context.Canceled)
			l.teardown()
		})
	}

	// Cancel + reap if the browser connection drops (crash / exit). The
	// coordinator also watches LostConnection for its own recovery; this only
	// guarantees the process is reaped and resources freed.
	go func() {
		select {
		case <-b.LostConnection:
			cancel()
		case <-ctx.Done():
			cancel()
		}
	}()

	return ctx, cancel, nil
}

// launch owns one Chrome process and its in-memory websocket bridge.
type launch struct {
	opts     PipeOptions
	execPath string

	cmd      *exec.Cmd
	waitDone chan struct{} // closed by the sole process waiter
	waitErr  error         // read only after waitDone closes
	onExit   context.CancelCauseFunc
	stderr   diagnosticTail
	pc       *PipeConn // parent-side NUL-framed pipe to Chrome (fd 3 out / fd 4 in)

	serverConn net.Conn // bridge side of the in-memory ws link
	clientConn net.Conn // chromedp side of the in-memory ws link (registered)
	addr       string   // registry key + synthetic ws host:port
	wsURL      string   // ws:// URL handed to chromedp.NewBrowser

	tempDir string // non-empty when we created the profile dir

	bridgeWG     sync.WaitGroup
	teardownOnce sync.Once
}

// start launches Chrome over the pipe and spins up the in-memory ws bridge.
func (l *launch) start(ctx context.Context) error {
	// fd 3: browser reads commands. Child gets the read end; parent writes.
	browserInR, browserInW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("cdppipe: create input pipe: %w", err)
	}
	// fd 4: browser writes events/results. Child gets the write end; parent reads.
	browserOutR, browserOutW, err := os.Pipe()
	if err != nil {
		browserInR.Close()
		browserInW.Close()
		return fmt.Errorf("cdppipe: create output pipe: %w", err)
	}

	l.pc = NewPipeConn(browserInW, browserOutR, l.opts.Debugf)

	args, tempDir, err := l.buildArgs()
	if err != nil {
		browserInR.Close()
		browserOutW.Close()
		l.pc.Close()
		return err
	}
	l.tempDir = tempDir

	cmd := exec.CommandContext(ctx, l.execPath, args...)
	if len(l.opts.Env) > 0 {
		cmd.Env = append(os.Environ(), l.opts.Env...)
	}
	// Caller hook first (process attrs / capture); then cdppipe asserts
	// Stderr and the pipe fds so they are always final regardless of what
	// ModifyCmd did — matching PipeOptions.ModifyCmd's doc ("cdppipe owns
	// cmd.ExtraFiles / Stdout / Stderr; do NOT reassign them from ModifyCmd
	// — they are set after this hook runs and will win"). Assigning Stderr
	// here (after ModifyCmd, not before) is what actually makes that true;
	// setting it before ModifyCmd would let the hook silently clobber it.
	if l.opts.ModifyCmd != nil {
		l.opts.ModifyCmd(cmd)
	}
	cmd.Stderr = &lineWriter{fn: l.opts.Errf, prefix: "cdppipe: chrome: ", tail: &l.stderr}
	// A descendant inheriting stderr must not prevent reaping after Chrome exits.
	cmd.WaitDelay = time.Second
	cmd.ExtraFiles = []*os.File{browserInR, browserOutW} // → child fd 3, fd 4

	if err := cmd.Start(); err != nil {
		browserInR.Close()
		browserOutW.Close()
		l.pc.Close()
		if l.tempDir != "" {
			os.RemoveAll(l.tempDir)
			l.tempDir = ""
		}
		return fmt.Errorf("cdppipe: start chrome: %w", err)
	}
	l.cmd = cmd
	l.waitDone = make(chan struct{})
	go func() {
		l.waitErr = cmd.Wait()
		if l.waitErr != nil && ctx.Err() == nil {
			l.errf("Chrome process exited unexpectedly: %v", l.waitErr)
		}
		close(l.waitDone)
		if l.onExit != nil {
			l.onExit(l.exitError())
		}
	}()
	// The child now owns its ends; the parent must not keep them open or it will
	// never observe the child's EOF.
	browserInR.Close()
	browserOutW.Close()

	// In-memory ws bridge between chromedp (clientConn) and Chrome (l.pc).
	l.serverConn, l.clientConn = net.Pipe()
	idb := make([]byte, 16)
	if _, err := rand.Read(idb); err != nil {
		return fmt.Errorf("cdppipe: generate launch id: %w", err)
	}
	l.addr = hex.EncodeToString(idb) + magicHostSuffix + ":1"
	l.wsURL = "ws://" + l.addr + "/devtools/browser/"
	registry.register(l.addr, l.clientConn)

	l.bridgeWG.Add(1)
	go l.runBridge()
	return nil
}

// buildArgs assembles the Chrome command line. cdppipe contributes only the
// minimal, non-behavioral flags; all headful/display/window/hardening flags are
// the caller's via opts.Args.
func (l *launch) buildArgs() (args []string, tempDir string, err error) {
	// --remote-debugging-pipe (bare) selects NUL-delimited JSON on fd 3/4 with
	// NO TCP port. Never combine with --remote-debugging-port.
	args = append(args, "--remote-debugging-pipe")
	args = append(args, "--no-first-run", "--no-default-browser-check")

	dir := l.opts.UserDataDir
	if dir == "" {
		dir, err = os.MkdirTemp("", "omnipus-cdppipe-")
		if err != nil {
			return nil, "", fmt.Errorf("cdppipe: create profile dir: %w", err)
		}
		tempDir = dir
	}
	args = append(args, "--user-data-dir="+dir)

	// Running as root (containers) needs --no-sandbox unless the caller opted out.
	if runtime.GOOS != "windows" && os.Getuid() == 0 && !hasFlag(l.opts.Args, "no-sandbox") {
		args = append(args, "--no-sandbox")
	}

	args = append(args, l.opts.Args...)
	args = append(args, "about:blank") // force a blank first tab
	return args, tempDir, nil
}

// runBridge terminates the chromedp-side websocket over the in-memory pipe and
// relays payloads to/from the Chrome pipe. If either direction fails (chromedp
// disconnects OR Chrome dies) the whole bridge is stopped: serverConn is closed
// so chromedp observes the drop and closes Browser.LostConnection, and the pipe
// is closed so Chrome exits.
func (l *launch) runBridge() {
	defer l.bridgeWG.Done()

	if _, err := ws.Upgrade(l.serverConn); err != nil {
		l.errf("ws upgrade over in-memory pipe failed: %v", err)
		l.serverConn.Close()
		l.pc.Close()
		return
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			l.serverConn.Close()
			l.pc.Close()
		})
	}

	done := make(chan struct{}, 2)

	// Chrome (fd 4) -> chromedp: read NUL frames, write ws server text frames.
	go func() {
		for {
			payload, err := l.pc.readRaw()
			if err != nil {
				break
			}
			if err := wsutil.WriteServerText(l.serverConn, payload); err != nil {
				break
			}
		}
		stop()
		done <- struct{}{}
	}()

	// chromedp -> Chrome (fd 3): read ws client text frames, write NUL frames.
	go func() {
		for {
			payload, err := wsutil.ReadClientText(l.serverConn)
			if err != nil {
				break
			}
			if err := l.pc.writeRaw(payload); err != nil {
				break
			}
		}
		stop()
		done <- struct{}{}
	}()

	<-done
	<-done
}

// teardown stops the bridge, closes the pipe (making Chrome exit), reaps the
// process (cdppipe owns the single cmd.Wait, like chromedp's ExecAllocator), and
// removes the temp profile dir. Idempotent.
func (l *launch) teardown() {
	l.teardownOnce.Do(func() {
		if l.addr != "" {
			registry.drop(l.addr)
		}
		if l.pc != nil {
			l.pc.Close() // closing fd 3 makes Chrome observe EOF and exit
		}
		if l.serverConn != nil {
			l.serverConn.Close()
		}
		if l.clientConn != nil {
			l.clientConn.Close()
		}

		if l.waitDone != nil {
			select {
			case <-l.waitDone:
			case <-time.After(5 * time.Second):
				_ = l.cmd.Process.Kill()
				<-l.waitDone
			}
		}

		l.bridgeWG.Wait()

		if l.tempDir != "" {
			os.RemoveAll(l.tempDir)
		}
	})
}

func (l *launch) errf(format string, args ...any) {
	if l.opts.Errf != nil {
		l.opts.Errf("cdppipe: "+format, args...)
	}
}

// hasFlag reports whether args contains --name or --name=... (name given
// without leading dashes).
func hasFlag(args []string, name string) bool {
	pfx := "--" + name
	for _, a := range args {
		if a == pfx || strings.HasPrefix(a, pfx+"=") {
			return true
		}
	}
	return false
}

// lineWriter forwards Chrome's stderr to a printf-style logger, one write chunk
// at a time (trailing newlines trimmed).
type lineWriter struct {
	fn     func(string, ...any)
	prefix string
	tail   *diagnosticTail
}

func (w *lineWriter) Write(p []byte) (int, error) {
	if w.tail != nil {
		w.tail.append(p)
	}
	line := strings.TrimRight(string(p), "\r\n")
	if line != "" && w.fn != nil {
		w.fn("%s%s", w.prefix, line)
	}
	return len(p), nil
}

// exitError is called only after the process waiter has published its result.
func (l *launch) exitError() error {
	if l.waitErr != nil {
		return fmt.Errorf("cdppipe: Chrome process exited: %w", l.waitErr)
	}
	return errors.New("cdppipe: Chrome process exited (exit status 0)")
}

func (l *launch) startupError(stage string, err, parentErr error) error {
	if parentErr != nil {
		return fmt.Errorf("cdppipe: %s: %w", stage, parentErr)
	}
	select {
	case <-l.waitDone:
		err = fmt.Errorf("Chrome exited before readiness: %w", l.exitError())
	default:
	}
	if hint := l.stderr.hint(); hint != "" {
		return fmt.Errorf("cdppipe: %s: %w (%s)", stage, err, hint)
	}
	return fmt.Errorf("cdppipe: %s: %w", stage, err)
}

// Keep diagnostics bounded in memory. Only fixed classifications enter returned
// errors: Chrome stderr can include profile paths, URLs, or other private data.
const maxStartupDiagnosticBytes = 4096

type diagnosticTail struct {
	mu   sync.Mutex
	data []byte
}

func (d *diagnosticTail) append(p []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(p) >= maxStartupDiagnosticBytes {
		d.data = append(d.data[:0], p[len(p)-maxStartupDiagnosticBytes:]...)
		return
	}
	excess := len(d.data) + len(p) - maxStartupDiagnosticBytes
	if excess > 0 {
		d.data = append(d.data[:0], d.data[excess:]...)
	}
	d.data = append(d.data, p...)
}
func (d *diagnosticTail) hint() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	text := string(d.data)
	switch {
	case strings.Contains(text, "ProcessSingleton"), strings.Contains(text, "profile appears to be in use"):
		return "Chrome could not acquire its profile; another browser may be using it"
	case strings.Contains(text, "MachPortRendezvousServer"), strings.Contains(text, "No rendezvous client"):
		return "a Chrome helper could not contact its parent process; inspect browser startup logs"
	case len(d.data) > 0:
		return "Chrome emitted startup diagnostics; inspect browser logs"
	default:
		return ""
	}
}
