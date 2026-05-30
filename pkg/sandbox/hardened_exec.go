// Package sandbox — hardened-exec child wrapper for Tier 2 (build_static)
// and Tier 3 (web_serve dev mode + workspace.shell_bg) tools.
//
// Cross-platform contract:
//
// - Linux: Setpgid + Pdeathsig=SIGTERM for clean parent-death cleanup,
// plus prlimit on RLIMIT_AS (memory) and RLIMIT_CPU (cpu seconds).
// Children inherit gateway Landlock + seccomp unchanged (,
// no narrowing in v4).
// - macOS: prlimit-equivalent via Setrlimit (RLIMIT_AS unsupported on
// darwin; uses RLIMIT_DATA which approximates heap+stack). No isolation
// primitive beyond OS perms.
// - Windows: Job Object with JOBOBJECT_EXTENDED_LIMIT_INFORMATION
// (process-memory limit + KILL_ON_JOB_CLOSE so the child dies if the
// gateway exits). No DACL/Restricted Token/AppContainer in v4.
//
// All platforms inject HTTP_PROXY/HTTPS_PROXY when EgressProxyAddr is non-
// empty and npm_config_cache when WorkspaceDir is non-empty ( — per-
// agent npm cache so concurrent builds don't fight over a shared cache).
//
// Threat note: Tier 2 and Tier 3 are documented as trusted-prompt features
//. The child has the gateway's full filesystem reach;
// raw TCP egress is unblocked. Operator awareness is the primary control.

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/audit"
)

// restrictAuditHook is the package-level audit emitter for B1.2(d)
// per-thread restrict failures. Set by SetRestrictAuditHook at gateway
// boot; nil until then. Stored atomically so the read path on the
// hardened-exec hot loop is lock-free.
//
// Threat note: a per-thread restrict failure means the OS thread we
// locked could not have its Landlock domain re-applied, so the spawn
// MUST abort (the caller surfaces an error). Audit emission is a loud
// signal that the gateway's kernel sandbox is in a degraded state — an
// operator who sees this entry should investigate kernel ABI / namespace
// drift immediately.
var restrictAuditHook atomic.Pointer[func(*audit.Entry)]

// SetRestrictAuditHook installs the audit emitter for per-thread restrict
// failures. Pass nil to clear (used in tests). Safe to call from any
// goroutine. The function is invoked from within Run / StartLocked
// goroutines that have runtime.LockOSThread'd, so implementations MUST
// be cheap and non-blocking.
//
// B1.2(d): the gateway wires this at boot to agent.AgentLoop.AuditLogger().Log
// so a Landlock / seccomp re-apply failure on a worker thread surfaces in the
// audit JSONL rather than only in slog.
func SetRestrictAuditHook(fn func(*audit.Entry)) {
	if fn == nil {
		restrictAuditHook.Store(nil)
		return
	}
	restrictAuditHook.Store(&fn)
}

// emitRestrictFailure dispatches to the registered hook, falling back to
// slog.Error when no hook is wired. Called from Run and StartLocked when
// restrictCurrentThreadIfNeeded returns a non-nil error so the operator
// sees the failure in audit even though the spawn itself aborts.
func emitRestrictFailure(callsite string, err error) {
	if err == nil {
		return
	}
	hookPtr := restrictAuditHook.Load()
	if hookPtr == nil {
		slog.Error("hardened_exec: per-thread restrict failed (no audit hook wired)",
			"callsite", callsite, "error", err)
		return
	}
	(*hookPtr)(&audit.Entry{
		Event:    "sandbox_restrict_failed",
		Decision: audit.DecisionError,
		Details: map[string]any{
			"callsite": callsite,
			"error":    err.Error(),
			"phase":    "per_thread_restrict",
		},
	})
}

// allowedChildEnvKeys is the EXPLICIT ALLOWLIST of environment variable names
// that may be inherited by a hardened-exec child process. Any key not present
// here (and not matched by allowedChildEnvPrefixes) is stripped before the
// child sees the environment.
//
// Threat model (v0.2 #155 item 3): the previous implementation maintained a
// 3-key denylist (OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, OMNIPUS_BEARER_TOKEN).
// That model fails open: any newly-introduced sensitive env var (a future
// API key, an upstream provider token, a third-party secret loaded by a
// dependency) leaks to children until someone remembers to add it to the
// denylist. The allowlist inverts the default — unknown keys are stripped,
// new sensitive keys are safe by default.
//
// The allowlist covers exactly what a generic build/run child needs to find
// libraries, locate the user's home, render localized output, and write to
// a scratch directory:
//
//	PATH    — required to locate executables (npm, node, python, sh, ...)
//	HOME    — npm/pip/cargo write per-user caches under here; node uses it for
//	          ~/.npmrc; many tools blow up if HOME is unset
//	USER    — git, ssh, and many CLI tools query it for default user
//	LOGNAME — equivalent to USER on systemd-managed Linux distros
//	SHELL   — npm spawns scripts via $SHELL; subprocess shells use it
//	TZ      — controls log timestamp rendering and date-sensitive build steps
//	LANG    — UTF-8 locale required for non-ASCII filenames in builds
//	TMPDIR  — Go's ioutil.TempFile and many test runners honor this
//	TERM    — terminal capability detection; npm/yarn use it for color output
//
// The two prefix-allowlists cover broad families:
//
//	LC_*  — C locale family (LC_ALL, LC_CTYPE, LC_NUMERIC, ...) — required
//	        for correct sorting, formatting, and case-insensitive filename
//	        comparison in build tools.
//	XDG_* — XDG Base Directory spec (XDG_CONFIG_HOME, XDG_CACHE_HOME, ...) —
//	        per-user dirs that respectful CLI tools use instead of fixed
//	        ~/.config / ~/.cache; passing through keeps user dotfile setups
//	        working in builds.
//
// And one operator escape hatch:
//
//	OMNIPUS_CHILD_*  — narrow, namespaced pass-through for callers that
//	                   intentionally want to forward a value to a child.
//	                   Operators wanting to pass FOO=bar to a child must
//	                   rename it to OMNIPUS_CHILD_FOO=bar at the gateway
//	                   layer. The rename is the trust boundary — it's loud
//	                   and grep-able, so a future audit can find every
//	                   intentional pass-through with one search.
//
// Build with care: anything you add here is information that reaches every
// child process for the rest of the gateway's life. If a key contains a
// secret, do NOT add it; use the OMNIPUS_CHILD_* rename pattern instead.
var allowedChildEnvKeys = map[string]struct{}{
	"PATH":    {},
	"HOME":    {},
	"USER":    {},
	"LOGNAME": {},
	"SHELL":   {},
	"TZ":      {},
	"LANG":    {},
	"TMPDIR":  {},
	"TERM":    {},
	// Standard proxy variables. Forwarded so operator-set or
	// EgressProxy-injected proxies route child traffic correctly. Values
	// are operator-controlled (egress proxy address) or operator-inherited
	// from the parent, never secrets. Required by TestExecProxy_*.
	"HTTP_PROXY":  {},
	"HTTPS_PROXY": {},
	"NO_PROXY":    {},
	"http_proxy":  {},
	"https_proxy": {},
	"no_proxy":    {},
}

// allowedChildEnvPrefixes are key prefixes whose entire family is allowed.
// See allowedChildEnvKeys for the rationale on each prefix.
var allowedChildEnvPrefixes = []string{
	"LC_",
	"XDG_",
	"OMNIPUS_CHILD_",
}

// isAllowedChildEnvKey reports whether name passes the allowlist for the
// hardened-exec child environment. Used by filterChildEnv on every entry of
// os.Environ() and in tests that exercise the boundary directly.
func isAllowedChildEnvKey(name string) bool {
	if _, ok := allowedChildEnvKeys[name]; ok {
		return true
	}
	for _, prefix := range allowedChildEnvPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// ScrubGatewayEnv returns a copy of os.Environ() filtered through the child
// process allowlist (see allowedChildEnvKeys). It is exported so callers
// outside this package (e.g. pkg/tools workspace.shell) can apply the same
// filter regardless of whether the kernel sandbox is active.
//
// Naming: the public function name is preserved across the v0.2 #155 item-3
// rework (denylist → allowlist) so callers do not need to change. Internally
// the implementation is filterChildEnv — that name better reflects the new
// semantics.
//
// Closes pentest item 3 (#155).
func ScrubGatewayEnv() []string {
	return filterChildEnv()
}

// filterChildEnv returns os.Environ() filtered through the child process
// allowlist. Entries with empty values pass through (a present-but-empty
// key like LANG="" is part of legitimate user config). Malformed entries
// (no '=', or '=' at index 0) are dropped — those are kernel-injected
// curiosities like `BASH_FUNC_foo%%=...` that no child needs.
func filterChildEnv() []string {
	parent := os.Environ()
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		if !isAllowedChildEnvKey(kv[:eq]) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// Limits describes resource caps and environment hints for a hardened child.
//
// Zero values are interpreted as "no limit" for TimeoutSeconds and
// MemoryLimitBytes; callers SHOULD pass concrete defaults from config (e.g.
// cfg.Tools.BuildStatic.TimeoutSeconds = 300, MemoryLimitBytes = 512 MiB)
// rather than relying on unbounded execution.
type Limits struct {
	// TimeoutSeconds is the wall-clock hard-kill deadline. 0 disables the
	// timeout (the parent context governs cancellation instead). On Linux
	// this is enforced via context cancellation; we do NOT use RLIMIT_CPU
	// because cpu-time != wall-clock and a build that sleeps for I/O could
	// run forever.
	TimeoutSeconds int32

	// MemoryLimitBytes caps the child's address space (Linux RLIMIT_AS,
	// Windows JOB_OBJECT_LIMIT_PROCESS_MEMORY). 0 disables the cap. Values
	// below 64 MiB are not validated here — the BuildStatic boot validator
	// rejects them earlier in the call chain.
	MemoryLimitBytes uint64

	// WorkspaceDir is the agent's workspace root. When non-empty,
	// npm_config_cache is set to <WorkspaceDir>/.npm-cache.
	// This is also the cwd of the child process.
	WorkspaceDir string

	// EgressProxyAddr is the loopback address (e.g. "127.0.0.1:54321") of
	// the egress proxy started by the caller. When non-empty, HTTP_PROXY
	// and HTTPS_PROXY are injected into the child's environment so HTTP/
	// HTTPS traffic flows through the allow-listed proxy.
	//
	// acknowledgement: this is HTTP/HTTPS only. Raw TCP connect
	// is NOT covered. Documented as a trusted-prompt-feature limitation.
	EgressProxyAddr string
}

// Result captures the outcome of a hardened child execution.
type Result struct {
	// Stdout and Stderr are the captured byte streams. Both are bounded
	// to 4 MiB each; longer output is truncated with a notice appended.
	Stdout []byte
	Stderr []byte

	// ExitCode is the child's exit code. -1 indicates the process was
	// killed by a signal (e.g. SIGTERM on timeout); inspect TimedOut.
	ExitCode int

	// TimedOut is true when the wall-clock deadline elapsed before the
	// child exited on its own. The child receives SIGTERM, then SIGKILL
	// after a 5-second grace period.
	TimedOut bool

	// Duration is the elapsed wall-clock time of the child run.
	Duration time.Duration

	// MemoryLimitUnsupported is true when the caller requested a non-zero
	// MemoryLimitBytes but the platform-specific post-start hardener could
	// not enforce it (currently darwin only — RLIMIT_AS unsupported on XNU).
	// Linux and Windows always set this to false because both backends
	// enforce the cap or fail the spawn outright.
	//
	// Tooling SHOULD surface this to the agent via the result preamble so
	// operators of cross-platform builds know the cap is best-effort. See
	// HIGH-1 (silent-failure-hunter).
	MemoryLimitUnsupported bool
}

// outputCap bounds captured stdout/stderr per stream. 4 MiB is enough for
// build logs while preventing a runaway child from exhausting gateway memory.
const outputCap = 4 << 20

// ErrEmptyArgv is returned by Run when called with no command to execute.
var ErrEmptyArgv = errors.New("hardened_exec: argv is empty")

// Run launches argv as a hardened child process.
//
// The child:
// - Runs in WorkspaceDir as cwd (when set).
// - Has the merged env: caller-supplied env, then proxy + npm cache vars
// appended last so callers cannot accidentally override them.
// - Is bounded by Limits — wall-clock timeout, address-space cap.
// - Has captured stdout/stderr (each capped at 4 MiB).
//
// Returns ErrEmptyArgv if argv has zero length. Other returned errors
// indicate the child could not be started; once started, normal exit codes
// are reported via Result.ExitCode regardless of zero/non-zero.
func Run(ctx context.Context, argv []string, env []string, lim Limits) (Result, error) {
	if len(argv) == 0 {
		return Result{}, ErrEmptyArgv
	}

	// Linux Landlock enforces per-OS-thread, but Go's M:N scheduler routes
	// goroutines onto arbitrary worker threads. If we forked the child from
	// whichever worker thread happened to run this goroutine, it would
	// almost certainly be a thread that never had restrict_self called on
	// it, and the child would silently escape the gateway's kernel sandbox.
	// To close that gap we run the entire spawn-and-wait sequence inside a
	// dedicated goroutine that locks itself to a fresh OS thread, re-applies
	// the Landlock domain to that thread, and exits without unlocking — so
	// Go's runtime disposes of the (now-restricted) thread instead of
	// recycling it for unrelated work. On non-enforce modes and non-Linux
	// platforms restrictCurrentThreadIfNeeded is a no-op and the wrapper
	// only adds the overhead of one extra goroutine per spawn.
	type runResult struct {
		res Result
		err error
	}
	ch := make(chan runResult, 1)
	go func() {
		runtime.LockOSThread()
		// Intentionally NO UnlockOSThread — see comment above. The
		// goroutine returns at the end of this function, which causes the
		// runtime to terminate the locked thread.
		if err := restrictCurrentThreadIfNeeded(); err != nil {
			// B1.2(d): emit a sandbox_restrict_failed audit entry BEFORE
			// returning the error so operators see the kernel-level
			// degradation in the audit trail, not just in slog. The spawn
			// is aborted unconditionally — we never run an unrestricted
			// child even if audit emission itself failed.
			emitRestrictFailure("hardened_exec.Run", err)
			ch <- runResult{Result{}, fmt.Errorf("hardened_exec: per-thread restrict: %w", err)}
			return
		}
		res, err := runOnCurrentThread(ctx, argv, env, lim)
		ch <- runResult{res, err}
	}()
	r := <-ch
	return r.res, r.err
}

// StartLocked invokes cmd.Start on an OS thread that has the gateway's
// Landlock domain re-applied to it, so the forked child inherits the kernel
// sandbox even when Go's M:N scheduler would otherwise have routed the
// spawning goroutine onto an unrestricted worker thread. The launching
// goroutine then exits without UnlockOSThread, which causes Go's runtime to
// terminate the (permanently restricted) OS thread instead of recycling it
// for unrelated work.
//
// After this returns, the caller can safely cmd.Wait, cmd.Process.Kill,
// cmd.Process.Signal, etc. from any goroutine — those operations do not
// require the launching thread.
//
// IMPORTANT: PR_SET_PDEATHSIG is cleared on cmd before Start, because the
// launching OS thread dies as soon as the goroutine returns. If a caller
// (e.g. ApplyChildHardening) has set Pdeathsig=SIGTERM, the kernel would
// fire that signal at the child the moment Go retires the locked thread.
// StartLocked therefore unconditionally clears Pdeathsig — children launched
// through this helper survive the launching thread's death and are managed
// by their own lifecycle owner (DevServerRegistry, session manager, etc.).
//
// On non-Linux platforms or when the sandbox is disabled,
// restrictCurrentThreadIfNeeded is a no-op and this wrapper costs only one
// goroutine + channel hop per spawn.
func StartLocked(cmd *exec.Cmd) error {
	// Record that the correct spawn path (StartLocked) has been used in this
	// process. ApplyToCmd contract enforcement (B1.4-b) uses this marker in
	// debug assertions to confirm callers are not bypassing StartLocked.
	MarkStartLockedCalled()
	clearPdeathsigForBackground(cmd)
	ch := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		// Intentionally NO UnlockOSThread.
		if err := restrictCurrentThreadIfNeeded(); err != nil {
			// B1.2(d): same audit emission as Run() — every per-thread
			// restrict failure is a loud signal of sandbox degradation,
			// regardless of which spawn entry point fired it.
			emitRestrictFailure("hardened_exec.StartLocked", err)
			ch <- fmt.Errorf("hardened_exec: per-thread restrict: %w", err)
			return
		}
		ch <- cmd.Start()
	}()
	return <-ch
}

// runOnCurrentThread is the original Run body, kept private so Run can wrap
// it in a thread-locked goroutine without duplicating the spawn logic.
func runOnCurrentThread(ctx context.Context, argv []string, env []string, lim Limits) (Result, error) {
	// Set up the wall-clock timeout. We always derive a child context so
	// the caller's ctx can still cancel earlier than TimeoutSeconds.
	runCtx := ctx
	var cancel context.CancelFunc
	if lim.TimeoutSeconds > 0 {
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(lim.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	if lim.WorkspaceDir != "" {
		cmd.Dir = lim.WorkspaceDir
	}
	cmd.Env = mergeEnv(env, lim)

	// Bounded stdout/stderr capture. We use bytes.Buffer with manual length
	// checks rather than io.LimitWriter because we want a clear truncation
	// notice in the captured output rather than a silent cut.
	var stdoutBuf, stderrBuf cappedBuffer
	stdoutBuf.cap = outputCap
	stderrBuf.cap = outputCap
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	// Stdin: bind to an empty in-memory reader so os/exec does not open
	// /dev/null on our behalf. Landlock at the gateway level does not grant
	// access to /dev, and an empty reader yields the same EOF semantics as
	// /dev/null without traversing the device tree.
	cmd.Stdin = bytes.NewReader(nil)

	// Apply platform-specific hardening (Setpgid+Pdeathsig+prlimit on
	// Linux, rlimit on darwin, Job Object on windows). Failure to apply
	// hardening returns an error before Start so the caller can decide
	// whether to fall back or abort. We do NOT silently spawn an
	// unhardened child.
	if err := applyPlatformHardening(cmd, lim); err != nil {
		return Result{}, fmt.Errorf("hardened_exec: apply hardening: %w", err)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("hardened_exec: start %s: %w", argv[0], err)
	}

	// Apply post-start hardening (Windows Job Object assignment must run
	// after Start because we need the child's process Handle).
	if hardeningErr := applyPostStartHardening(cmd, lim); hardeningErr != nil {
		// H1-BK: surface kill errors — a loose partially-sandboxed child is a
		// security event that operators must see, not a silently discarded error.
		killErr := cmd.Process.Kill()
		if killErr != nil && !isProcessDoneError(killErr) {
			slog.Error(
				"hardened_exec: post-start hardening failed AND child kill failed; child PID may still be running",
				"pid",
				cmd.Process.Pid,
				"kill_err",
				killErr,
				"hardening_err",
				hardeningErr,
			)
			_, _ = cmd.Process.Wait()
			return Result{}, fmt.Errorf(
				"hardened_exec: post-start hardening: %w; kill also failed: %v",
				hardeningErr,
				killErr,
			)
		}
		_, _ = cmd.Process.Wait()
		return Result{}, fmt.Errorf("hardened_exec: post-start hardening: %w", hardeningErr)
	}

	waitErr := cmd.Wait()
	duration := time.Since(start)

	// Determine timeout vs natural exit. exec.CommandContext kills the
	// child when the context expires; we detect that by inspecting the
	// derived context's error, NOT the original ctx (which may still be
	// live if only the timeout fired).
	timedOut := false
	if runCtx.Err() == context.DeadlineExceeded {
		timedOut = true
	}

	exitCode := -1
	if waitErr == nil {
		exitCode = 0
	} else {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		}
	}

	// HIGH-1: surface platform limitations to the caller. Set
	// MemoryLimitUnsupported when the caller asked for a non-zero cap but
	// this platform cannot enforce it (currently darwin only). Tooling
	// that wraps Run uses this flag to include a warning in the result
	// preamble shown to the agent.
	memUnsupported := lim.MemoryLimitBytes > 0 && !memoryLimitSupported

	return Result{
		Stdout:                 stdoutBuf.Bytes(),
		Stderr:                 stderrBuf.Bytes(),
		ExitCode:               exitCode,
		TimedOut:               timedOut,
		Duration:               duration,
		MemoryLimitUnsupported: memUnsupported,
	}, nil
}

// mergeEnv merges the gateway's (scrubbed) environment, the caller's env,
// and platform-injected vars. Order: gateway env, caller env, injected
// proxy/cache vars. POSIX exec(3) gives later entries precedence on
// duplicate keys, so caller env can override gateway env, and injected
// proxy/cache vars override both — this is intentional.
//
// Gateway env is always inherited (with sensitive keys stripped) so children
// have working PATH/HOME/LANG without leaking secrets. Setting cmd.Env to a
// non-nil empty slice in os/exec means "no inheritance"; the previous
// implementation forced operators to plumb PATH through the tool's `env`
// arg, which silently broke commands like `npm run dev` that depend on
// node_modules/.bin being on PATH.
func mergeEnv(env []string, lim Limits) []string {
	gateway := filterChildEnv()
	// CodeQL go/allocation-size-overflow flags the implicit int sum
	// len(gateway)+len(env)+6 as potentially overflowing. Per-process env
	// count is bounded by the kernel (NCARGS = 128 KiB total on Linux), so
	// the sum is always small in practice — but bound it explicitly so the
	// allocation hint is safe even if filterChildEnv() ever misbehaves and
	// hands back something pathological.
	const maxMergedEnvHint = 1 << 16
	capHint := len(gateway) + len(env) + 6
	if capHint < 0 || capHint > maxMergedEnvHint {
		capHint = maxMergedEnvHint
	}
	merged := make([]string, 0, capHint)
	merged = append(merged, gateway...)
	merged = append(merged, env...)

	// Inject proxy variables. Both upper- and lower-case forms are widely
	// honored (npm/node use lowercase, curl uses uppercase, Go's
	// http.Transport uses both via httpproxy.FromEnvironment).
	if lim.EgressProxyAddr != "" {
		proxyURL := "http://" + lim.EgressProxyAddr
		merged = append(merged,
			"HTTP_PROXY="+proxyURL,
			"HTTPS_PROXY="+proxyURL,
			"http_proxy="+proxyURL,
			"https_proxy="+proxyURL,
			// NO_PROXY for loopback so node's own loopback connections
			// (e.g. dev-server bind probes) don't bounce off the proxy.
			"NO_PROXY=127.0.0.1,localhost,::1",
			"no_proxy=127.0.0.1,localhost,::1",
		)
	}

	// Per-agent npm cache. The boot validator ensures
	// WorkspaceDir is absolute, so the joined path is well-formed.
	if lim.WorkspaceDir != "" {
		// Use forward-slash join — npm normalises path separators on
		// Windows, and using filepath.Join here would import filepath
		// just for one call. Forward slashes work everywhere npm runs.
		merged = append(merged,
			"npm_config_cache="+lim.WorkspaceDir+"/.npm-cache",
		)
	}

	return merged
}

// cappedBuffer is a bytes.Buffer wrapper that enforces a hard ceiling on
// bytes written. Once the cap is reached further writes are accepted (so
// the underlying child does not block on a full pipe) but discarded; a
// truncation notice is appended to the captured bytes on Bytes.
type cappedBuffer struct {
	buf       bytes.Buffer
	cap       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	remaining := c.cap - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		// Lie about the byte count — the kernel's pipe-fill semantics
		// require us to report success or the child will see EPIPE.
		return len(p), nil
	}
	if len(p) > remaining {
		c.truncated = true
		c.buf.Write(p[:remaining])
		return len(p), nil
	}
	c.buf.Write(p)
	return len(p), nil
}

// Bytes returns the captured bytes, with a truncation notice appended when
// any data was discarded. The notice is human-readable so it shows up
// in error reports and stderr tails without further processing.
func (c *cappedBuffer) Bytes() []byte {
	if !c.truncated {
		return c.buf.Bytes()
	}
	// Append the notice without mutating the underlying buffer in case
	// the caller calls Bytes multiple times.
	notice := []byte("\n[hardened_exec: output truncated at " + formatBytes(c.cap) + "]\n")
	out := make([]byte, 0, c.buf.Len()+len(notice))
	out = append(out, c.buf.Bytes()...)
	out = append(out, notice...)
	return out
}

// formatBytes returns a short human-readable size string used only for the
// truncation notice. Avoids importing humanize for a single call.
func formatBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	case n >= 1<<10:
		return fmt.Sprintf("%d KiB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// ApplyChildHardening exposes the platform-specific pre-start hardening
// (Setpgid, Pdeathsig on Linux; Setpgid on darwin; no-op on windows) so
// callers that need a non-blocking spawn (e.g. web_serve dev-mode and
// workspace.shell_bg) can apply the same primitives without going through
// Run.
//
// Caller responsibility:
// - Build the *exec.Cmd themselves (cmd.Dir, cmd.Env, etc.).
// - Call ApplyChildHardening BEFORE cmd.Start.
// - Call ApplyChildPostStartHardening AFTER cmd.Start.
//
// This is the same sequence Run performs internally; exposing it lets
// the dev-server tool reuse the platform logic without copy-pasting the
// SysProcAttr setup.
func ApplyChildHardening(cmd *exec.Cmd, lim Limits) error {
	return applyPlatformHardening(cmd, lim)
}

// ApplyChildPostStartHardening exposes the post-Start step (prlimit on
// Linux, Job Object assignment on Windows, no-op on darwin). See
// ApplyChildHardening for the contract.
func ApplyChildPostStartHardening(cmd *exec.Cmd, lim Limits) error {
	return applyPostStartHardening(cmd, lim)
}

// StderrTail returns the last n bytes of stderr (or all of it when shorter).
// Useful for surfacing build failures to the agent without dumping the entire
// log into the LLM context.
func (r Result) StderrTail(n int) string {
	if len(r.Stderr) <= n {
		return string(r.Stderr)
	}
	tail := r.Stderr[len(r.Stderr)-n:]
	// Cut at the first newline so the tail starts at a line boundary —
	// avoids garbled mid-line breaks when surfaced in error messages.
	if idx := bytes.IndexByte(tail, '\n'); idx >= 0 && idx < len(tail)-1 {
		tail = tail[idx+1:]
	}
	return strings.TrimSpace(string(tail))
}
