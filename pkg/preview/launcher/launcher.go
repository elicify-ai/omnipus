// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package launcher implements run-limited, the hidden mode of the omnipus
// binary that starts the out-of-process document programs (omnipus-pdfium,
// LibreOffice, extract-text) under resource limits — office-preview spec
// AD-2 "Launcher run-limited", AD-6 and AD-8.
//
// The gateway starts run-limited through the hardened spawn (the sandbox is
// applied by the caller's Start hook). run-limited applies the limits for its
// platform, then waits for exactly one go-byte on stdin before it starts the
// target, so untrusted input can only reach the target once the limits are in
// force and the owner record naming the launcher has been published (AD-8).
// If stdin closes before the go-byte, run-limited exits without starting
// anything.
//
//   - Linux: address-space (or data) / CPU / file-size / open-file limits and a
//     parent-death SIGKILL, then execve of the target. The parent starts the
//     launcher in its own process group, which execve keeps.
//   - macOS: CPU / file-size / open-file limits, then execve. macOS rejects
//     address-space, data and resident-size limits (S-1), so memory is best
//     effort: the parent samples the program's resident memory every 100 ms
//     and kills its process group above the budget (StartWatchdog). There is
//     no hard memory cap on macOS and nothing may claim one.
//   - Windows: a Job Object with memory, process-count, CPU-time and
//     kill-on-close limits; the launcher assigns itself, starts the target as
//     its child inside the job, passes its own stdio handles through and
//     relays the exit code. Killing the launcher, or the gateway's death,
//     closes the job and ends the whole tree.
package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ModeArg is the hidden argv[1] that selects run-limited.
const ModeArg = "__omnipus-run-limited"

// Exit codes of the launcher itself (the target's codes are its own).
const (
	ExitUsage        = 90
	ExitLimitFailed  = 91
	ExitNoGoByte     = 92
	ExitStartFailed  = 93
	ExitParentIsGone = 94
)

// Limits are the per-program limits (AD-6). Zero means "do not set".
type Limits struct {
	// MemoryBytes: Linux address space (or data segment, MemoryKind), Windows
	// job memory, macOS watchdog budget (best effort).
	MemoryBytes uint64
	// MemoryKind selects the Linux memory limit: "as" (default) or "data".
	MemoryKind string
	// CPUSeconds: RLIMIT_CPU on Unix, per-job user time on Windows.
	CPUSeconds uint64
	// FileSizeBytes: RLIMIT_FSIZE on Unix (Windows checks output after).
	FileSizeBytes uint64
	// OpenFiles: RLIMIT_NOFILE on Unix.
	OpenFiles uint64
	// Processes: active-process limit of the Windows job (launcher included).
	Processes uint32
}

// args encodes the limits as launcher flags.
func (l Limits) args() []string {
	var out []string
	add := func(name string, v uint64) {
		if v > 0 {
			out = append(out, "--"+name+"="+strconv.FormatUint(v, 10))
		}
	}
	add("mem", l.MemoryBytes)
	if l.MemoryKind != "" {
		out = append(out, "--mem-kind="+l.MemoryKind)
	}
	add("cpu", l.CPUSeconds)
	add("fsize", l.FileSizeBytes)
	add("nofile", l.OpenFiles)
	add("procs", uint64(l.Processes))
	return out
}

// parseArgs decodes launcher flags up to "--"; it returns the limits and the
// target argv.
func parseArgs(args []string) (Limits, []string, error) {
	var l Limits
	for i, a := range args {
		if a == "--" {
			target := args[i+1:]
			if len(target) == 0 || target[0] == "" {
				return l, nil, errors.New("run-limited: no target")
			}
			return l, target, nil
		}
		name, val, ok := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if !ok || !strings.HasPrefix(a, "--") {
			return l, nil, fmt.Errorf("run-limited: bad argument %q", a)
		}
		if name == "mem-kind" {
			if val != "as" && val != "data" {
				return l, nil, fmt.Errorf("run-limited: bad mem-kind %q", val)
			}
			l.MemoryKind = val
			continue
		}
		n, err := strconv.ParseUint(val, 10, 64)
		if err != nil || n == 0 {
			return l, nil, fmt.Errorf("run-limited: bad value for %s: %q", name, val)
		}
		switch name {
		case "mem":
			l.MemoryBytes = n
		case "cpu":
			l.CPUSeconds = n
		case "fsize":
			l.FileSizeBytes = n
		case "nofile":
			l.OpenFiles = n
		case "procs":
			if n > 1<<16 {
				return l, nil, fmt.Errorf("run-limited: procs %d too large", n)
			}
			l.Processes = uint32(n)
		default:
			return l, nil, fmt.Errorf("run-limited: unknown limit %q", name)
		}
	}
	return l, nil, errors.New("run-limited: missing -- before the target")
}

// Spec describes one limited program start.
type Spec struct {
	// Self is the verified omnipus executable that hosts run-limited.
	Self string
	// Target is the verified absolute program path; Args are its arguments
	// (without argv[0]).
	Target string
	Args   []string
	// Env is the complete environment of the program, built from nothing by
	// the caller (AD-7). Nil means an empty environment.
	Env []string
	// Dir is the working directory (the call's scratch folder).
	Dir    string
	Limits Limits
	// Start starts the prepared command, applying the sandbox (hardened
	// spawn). The returned release runs after Wait (Linux keeps its restricted
	// launching thread, and so the parent-death link, until then). Nil means
	// cmd.Start.
	Start func(*exec.Cmd) (release func(), err error)
	// Prepare may rewrite the command before Start (macOS Seatbelt wraps it).
	Prepare func(*exec.Cmd) error
	// StderrLimit bounds the kept stderr tail (default 64 KiB, AD-2).
	StderrLimit int
	// Stdout, when set, receives the program's stdout directly (LibreOffice);
	// otherwise Process.Stdout is a pipe the caller reads (renderer).
	Stdout io.Writer
}

// DefaultStderrLimit is the AD-2 stderr budget.
const DefaultStderrLimit = 64 << 10

// Process is a started launcher waiting for, or past, its go-byte.
type Process struct {
	cmd     *exec.Cmd
	Stdin   io.WriteCloser
	Stdout  io.ReadCloser
	stderr  *tailBuffer
	PID     int
	Started time.Time

	mu        sync.Mutex
	goSent    bool
	killed    bool
	killCause string
	waitOnce  sync.Once
	waitErr   error
	waitDone  chan struct{}
	watchStop func()
	release   func()
}

// Command builds the launcher command for spec without starting it.
func Command(spec Spec) (*exec.Cmd, error) {
	if spec.Self == "" || spec.Target == "" {
		return nil, errors.New("run-limited: executable and target are required")
	}
	argv := append([]string{ModeArg}, spec.Limits.args()...)
	argv = append(argv, "--", spec.Target)
	argv = append(argv, spec.Args...)
	cmd := exec.Command(spec.Self, argv...)
	cmd.Env = spec.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.Dir = spec.Dir
	setProcAttr(cmd)
	return cmd, nil
}

// Start starts run-limited. The target has not started yet: it starts only
// after Go sends the go-byte.
func Start(spec Spec) (*Process, error) {
	cmd, err := Command(spec)
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	p := &Process{cmd: cmd, Stdin: stdin, waitDone: make(chan struct{})}
	if spec.Stdout != nil {
		cmd.Stdout = spec.Stdout
	} else {
		stdout, pipeErr := cmd.StdoutPipe()
		if pipeErr != nil {
			return nil, pipeErr
		}
		p.Stdout = stdout
	}
	limit := spec.StderrLimit
	if limit <= 0 {
		limit = DefaultStderrLimit
	}
	p.stderr = &tailBuffer{limit: limit}
	cmd.Stderr = p.stderr
	if spec.Prepare != nil {
		if prepErr := spec.Prepare(cmd); prepErr != nil {
			return nil, prepErr
		}
	}
	start := spec.Start
	if start == nil {
		start = func(c *exec.Cmd) (func(), error) { return func() {}, c.Start() }
	}
	release, err := start(cmd)
	if err != nil {
		return nil, err
	}
	if release == nil {
		release = func() {}
	}
	p.release = release
	p.PID = cmd.Process.Pid
	p.Started = time.Now()
	if spec.Limits.MemoryBytes > 0 {
		p.watchStop = startWatchdog(p, spec.Limits.MemoryBytes)
	}
	return p, nil
}

// Go sends the go-byte. Call it only after the owner record naming this
// launcher has been published (AD-8 step 3).
func (p *Process) Go() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.goSent {
		return errors.New("run-limited: go-byte already sent")
	}
	p.goSent = true
	_, err := p.Stdin.Write([]byte{1})
	return err
}

// Kill ends the program and everything it started (process group on Unix,
// launcher and therefore the job on Windows). cause is recorded once.
func (p *Process) Kill(cause string) {
	p.mu.Lock()
	if !p.killed {
		p.killed = true
		p.killCause = cause
	}
	p.mu.Unlock()
	killTree(p.cmd)
}

// KillCause reports why Kill was first called ("" when it was not).
func (p *Process) KillCause() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.killCause
}

// Wait waits for the launcher (and so the target) to exit. Safe to call more
// than once and from several goroutines.
func (p *Process) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		if p.watchStop != nil {
			p.watchStop()
		}
		// The group may still hold a grandchild that outlived the target.
		killTree(p.cmd)
		p.release()
		close(p.waitDone)
	})
	<-p.waitDone
	return p.waitErr
}

// Done is closed once Wait has returned.
func (p *Process) Done() <-chan struct{} { return p.waitDone }

// ExitCode is the exit code after Wait (-1 when killed by a signal).
func (p *Process) ExitCode() int {
	if p.cmd.ProcessState == nil {
		return -1
	}
	return p.cmd.ProcessState.ExitCode()
}

// Stderr returns the kept stderr tail.
func (p *Process) Stderr() string { return p.stderr.String() }

// tailBuffer keeps the last limit bytes written to it.
type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func (t *tailBuffer) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(b)
	if len(b) >= t.limit {
		t.buf.Reset()
		t.buf.Write(b[len(b)-t.limit:])
		return n, nil
	}
	t.buf.Write(b)
	if over := t.buf.Len() - t.limit; over > 0 {
		rest := append([]byte(nil), t.buf.Bytes()[over:]...)
		t.buf.Reset()
		t.buf.Write(rest)
	}
	return n, nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}
