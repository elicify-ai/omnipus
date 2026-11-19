//go:build linux

// Package sandbox_test — issue #1909 RED: the gateway's boot order applies
// the Landlock bind-port allow-list (gateway.go::applySandbox) BEFORE the
// egress proxy takes its loopback listener (gateway_boot.go::
// wireInteractiveServices → buildEgressProxyOrAbort → sandbox.NewEgressProxy,
// which binds 127.0.0.1:0 — egress_proxy.go::NewEgressProxy). Port 0 is not
// on the bind list, so the listen fails with EACCES whenever the Go runtime
// schedules it on an OS thread created after the restriction (per-thread
// Landlock inheritance); on a pre-restriction thread it succeeds — boot
// survives by thread luck.
//
// The spec (elicify-ai/omnipus#1909) requires:
//
//	AC-1  the proxy's listener must be usable under the bind restriction
//	      regardless of which OS thread executes the listen — a failed
//	      proxy listen must not be reachable merely by thread luck;
//	AC-2  the fix must bind the listener before the restriction (or to a
//	      port already allowed) — explicitly NOT a port-0 allow rule,
//	      which would let every child bind any ephemeral port;
//	AC-3  the bind restriction itself must keep denying port 0 and
//	      ephemeral-range binds for sandboxed code.
//
// Each test re-execs the test binary (Landlock restrict_self is an
// irreversible per-process/per-thread ratchet — see
// backend_linux_subprocess_test.go, whose conventions this file follows:
// child exit 42 = scenario expectation met, 1 = spec violation, 2 =
// unexpected error, 77 = Landlock net ABI unavailable → parent skips).
//
// Determinism: LockOSThread is taken BEFORE Apply and held through the
// listen/bind, so the listen runs on the restricted thread itself — the
// deterministic worst case of the production thread lottery, and the exact
// repro shape the issue's evidence used ("a listen kept on the restricted
// thread"). The bind-only policy (no ConnectPortRules) is deliberate: the
// defect surface is the BIND list, and connect rules would make the dial
// assertions thread-dependent — injecting the very nondeterminism under
// test into the controls. The accept proof therefore dials from a SECOND
// OS thread pinned BEFORE the restriction (handshake-ordered, so it is
// deterministically outside the Landlock domain): on ABI v4+ computeRights
// declares NET_CONNECT_TCP too (sandbox_linux.go), so a dial from inside
// the domain is denied unless the port is in ConnectPortRules — and the
// proxy's port cannot be pre-authorized there, because it resolves from
// 127.0.0.1:0 during Apply, after the policy is already fixed. Only the
// accept proof moves outside the domain; the listen under test stays on
// the restricted thread.

package sandbox_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"golang.org/x/sys/unix"
)

// issue1909BindPorts is the boot-shape bind allow-list the scenarios enforce:
// the ends and middle of the default DevServerPortRange {18000,18999}
// (pkg/config/validator.go::applyDefaults) plus a gateway port
// (pkg/gateway/sandbox_apply.go::sandboxExtraPorts expands the dev range and
// adds the gateway port). It deliberately contains neither port 0 nor any
// port in the ephemeral range — the kernel assigns 127.0.0.1:0 binds from
// net.ipv4.ip_local_port_range (32768..60999 on this host), exactly the
// ports the list must NOT cover.
var issue1909BindPorts = []uint16{18000, 18001, 18999, 5000}

// issue1909AllowList is the shipped default egress allow-list
// (pkg/config/validator.go::applyDefaults — "default 7-host egress
// allow-list" per the issue). Contents do not affect the bind under test;
// they are the real boot input so the scenario mirrors boot faithfully.
var issue1909AllowList = []string{
	"registry.npmjs.org",
	"*.npmjs.org",
	"*.npmjs.com",
	"github.com",
	"raw.githubusercontent.com",
	"objects.githubusercontent.com",
	"nodejs.org",
}

// issue1909BindPolicy builds the enforce-mode policy the scenarios apply:
// filesystem rules in the production shape (workspace RW + loader dirs,
// mirroring runLandlockBindBlockedChild) plus the boot-shape bind list.
// ConnectPortRules are deliberately omitted — see the file comment.
func issue1909BindPolicy(workspace string) sandbox.SandboxPolicy {
	rules := make([]sandbox.NetPortRule, 0, len(issue1909BindPorts))
	for _, p := range issue1909BindPorts {
		rules = append(rules, sandbox.NetPortRule{Port: p})
	}
	return sandbox.SandboxPolicy{
		FilesystemRules: []sandbox.PathRule{
			{Path: workspace, Access: sandbox.AccessRead | sandbox.AccessWrite},
			{Path: "/lib", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/lib64", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/usr/lib", Access: sandbox.AccessRead | sandbox.AccessExecute},
			{Path: "/usr/lib64", Access: sandbox.AccessRead | sandbox.AccessExecute},
		},
		BindPortRules: rules,
	}
}

// rawTCPBindLoopbackPort issues bind(2) directly on a fresh AF_INET TCP
// socket bound to 127.0.0.1:port — the same syscall NewEgressProxy's
// net.Listen("tcp", "127.0.0.1:0") performs, bypassing net.Listen's
// dual-stack fallback paths (same rationale as rawTCPBind in
// backend_linux_subprocess_test.go, which binds 0.0.0.0; the proxy binds
// loopback).
func rawTCPBindLoopbackPort(port uint16) error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	defer func() {
		if closeErr := syscall.Close(fd); closeErr != nil {
			_ = closeErr // test fixture cleanup only
		}
	}()
	sa := &syscall.SockaddrInet4{Port: int(port), Addr: [4]byte{127, 0, 0, 1}}
	return syscall.Bind(fd, sa)
}

// logIssue1909Kernel records which kernel and Landlock ABI actually execute
// the test body, so a saved log can never be confused between a runner
// kernel and a local one (the brief: report which environment ran the body).
func logIssue1909Kernel(t *testing.T) {
	t.Helper()
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		release := uts.Release
		// NUL-terminated char array → Go string.
		end := 0
		for end < len(release) && release[end] != 0 {
			end++
		}
		t.Logf("kernel: %s (Landlock ABI v%d)", string(release[:end]), sandbox.ProbeLandlockABI())
	} else {
		t.Logf("kernel: uname unavailable (%v) — Landlock ABI v%d", err, sandbox.ProbeLandlockABI())
	}
}

// runIssue1909Child re-execs the test binary for one scenario and maps the
// child exit code onto the verdict. wantCode 42 = expectation met; 77 is
// always "kernel unavailable" (skip); anything else fails with the child's
// stderr inline. Hard timeout: the child is killed at 60 s.
func runIssue1909Child(t *testing.T, envVar, testRunName, successMsg string, wantCode int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	// gosec rationale (out of gosec scope; kept as documentation): intentional test-binary self-exec
	cmd := exec.CommandContext(ctx, os.Args[0],
		"-test.run="+testRunName,
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		envVar+"=1",
		"OMNIPUS_LANDLOCK_SANDBOX_DIR="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("child failed to run: %v\n%s", err, out)
		}
	}
	if ctx.Err() != nil {
		t.Fatalf("child exceeded the 60s hard timeout (killed); exit %d:\n%s", exitCode, out)
	}
	switch exitCode {
	case wantCode:
		t.Logf("%s (child exited %d)", successMsg, wantCode)
	case 77:
		t.Skipf("Landlock net ABI v4+ unavailable in child (exit 77):\n%s", out)
	default:
		t.Fatalf("child exit %d (expected %d):\n%s", exitCode, wantCode, out)
	}
}

// childLandlockOrExit selects the Landlock backend and applies policy in
// enforce mode on the CALLING thread (the caller must hold LockOSThread).
// Exits 77 when the kernel cannot run the scenario at all — the parent
// skips on 77, never fails.
func childLandlockOrExit(policy sandbox.SandboxPolicy) {
	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		fmt.Fprintf(os.Stderr, "Landlock backend not available (backend=%q)\n", name)
		os.Exit(77)
	}
	if sandbox.ProbeLandlockABI() < 4 {
		fmt.Fprintf(os.Stderr, "Landlock ABI v%d < v4 — net port rules unsupported\n", sandbox.ProbeLandlockABI())
		os.Exit(77)
	}
	if err := backend.Apply(policy); err != nil {
		fmt.Fprintf(os.Stderr, "Apply failed (treating as skip): %v\n", err)
		os.Exit(77)
	}
}

// TestIssue1909_EgressProxyListenUnderBindRestriction is the RED test.
//
// Scenario (AC-1): with the gateway's boot-shape bind list in force, the
// proxy constructor the gateway calls at boot (sandbox.NewEgressProxy) must
// yield a serving listener even when the listen executes on a restricted
// thread. Expected values derive from the issue: the fix must guarantee the
// listener regardless of thread; a nil-error construction whose Addr
// accepts a TCP connection is the only acceptable outcome. Today the child
// exits 1 with `egress_proxy: listen tcp 127.0.0.1:0: bind: permission
// denied` — the issue's exact defect — and the parent fails.
func TestIssue1909_EgressProxyListenUnderBindRestriction(t *testing.T) {
	if os.Getenv("OMNIPUS_1909_PROXY_LISTEN_CHILD") == "1" {
		runIssue1909ProxyListenChild()
		return // unreachable — the child ends in os.Exit
	}
	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock restrictions)")
	}
	logIssue1909Kernel(t)
	if sandbox.ProbeLandlockABI() < 4 {
		t.Skipf("Landlock ABI v4 required for net bind rules (have v%d)", sandbox.ProbeLandlockABI())
	}
	runIssue1909Child(t, "OMNIPUS_1909_PROXY_LISTEN_CHILD",
		"TestIssue1909_EgressProxyListenUnderBindRestriction",
		"egress proxy listened successfully under the bind restriction", 42)
}

func runIssue1909ProxyListenChild() {
	// Landlock restrict_self restricts the calling thread only; holding the
	// lock forces the listen below onto the restricted thread — the
	// deterministic worst case of the production thread lottery. Same
	// rationale as runLandlockBindBlockedChild.
	runtime.LockOSThread()

	// Accept-proof dialer: pinned to its own OS thread BEFORE the
	// restriction. The handshake below guarantees that thread exists and is
	// locked while no thread in this process is inside a Landlock domain, so
	// its dial is deterministically unrestricted. The dial cannot run on the
	// restricted thread itself: on ABI v4+ the ruleset handles
	// NET_CONNECT_TCP too (sandbox_linux.go::computeRights), so connect(2)
	// from inside the domain is denied without a matching ConnectPortRules
	// entry — and the proxy's port cannot be pre-authorized there, because
	// it resolves from 127.0.0.1:0 during Apply, after the policy is fixed.
	// accept(2) itself is not Landlock-gated; gating applies to the
	// bind/connect of the sockets that meet the listener, and this dial
	// comes from outside the domain — exactly as a client process's would.
	dialAddr := make(chan string, 1)
	dialErr := make(chan error, 1)
	dialerReady := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		close(dialerReady)
		addr := <-dialAddr
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			dialErr <- err
			return
		}
		if closeErr := conn.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "dial close failed: %v\n", closeErr)
		}
		dialErr <- nil
	}()
	<-dialerReady

	workspace := os.Getenv("OMNIPUS_LANDLOCK_SANDBOX_DIR")
	if workspace == "" {
		os.Exit(77)
	}
	childLandlockOrExit(issue1909BindPolicy(workspace))

	// The boot-time acquisition, in the boot order under test: restriction
	// already in force, NOW construct the proxy exactly as
	// gateway_boot.go::wireInteractiveServices does.
	p, err := sandbox.NewEgressProxy(issue1909AllowList, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "SPEC VIOLATION (AC-1): proxy listen failed under the bind restriction: %v\n", err)
		os.Exit(1)
	}
	if p.Addr() == "" {
		fmt.Fprintf(os.Stderr, "SPEC VIOLATION (AC-1): proxy constructed with empty Addr\n")
		os.Exit(2)
	}
	// The listener must be live, not merely allocated: dial it — from the
	// pinned unrestricted thread set up above (see its comment for why the
	// restricted thread itself cannot dial).
	dialAddr <- p.Addr()
	if dialFailure := <-dialErr; dialFailure != nil {
		fmt.Fprintf(os.Stderr, "SPEC VIOLATION (AC-1): proxy Addr %s refuses connections: %v\n", p.Addr(), dialFailure)
		os.Exit(1)
	}
	if closeErr := p.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "proxy close failed: %v\n", closeErr)
	}
	fmt.Fprintf(os.Stderr, "proxy listened on %s under the bind restriction\n", p.Addr())
	os.Exit(42)
}

// TestIssue1909_PreboundProxyServesAfterBindRestriction is the control that
// must pass today: the CORRECT order — proxy listener taken BEFORE the
// restriction — leaves a fully serving proxy after the bind list is in
// force (Landlock gates bind(2), never the already-bound listener's
// accept/serve path). It validates the instrument: if the restriction in
// these scenarios did not actually enforce, or killed any listener, this
// control could not hold, and the RED test's verdict would mean nothing.
//
// Oracle for the serve proof: egress_proxy.go's package doc — "Hosts not on
// the list get a 403"; the request below uses a host outside
// issue1909AllowList, so a live proxy answers 403 without any upstream
// dial.
func TestIssue1909_PreboundProxyServesAfterBindRestriction(t *testing.T) {
	if os.Getenv("OMNIPUS_1909_PREBOUND_SERVES_CHILD") == "1" {
		runIssue1909PreboundServesChild()
		return // unreachable — the child ends in os.Exit
	}
	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock restrictions)")
	}
	logIssue1909Kernel(t)
	if sandbox.ProbeLandlockABI() < 4 {
		t.Skipf("Landlock ABI v4 required for net bind rules (have v%d)", sandbox.ProbeLandlockABI())
	}
	runIssue1909Child(t, "OMNIPUS_1909_PREBOUND_SERVES_CHILD",
		"TestIssue1909_PreboundProxyServesAfterBindRestriction",
		"pre-bound proxy kept serving after the bind restriction", 42)
}

func runIssue1909PreboundServesChild() {
	workspace := os.Getenv("OMNIPUS_LANDLOCK_SANDBOX_DIR")
	if workspace == "" {
		os.Exit(77)
	}

	// The correct order, part 1: listener BEFORE restriction (unrestricted
	// thread — the main goroutine's original M).
	p, err := sandbox.NewEgressProxy(issue1909AllowList, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "environment error: pre-restriction proxy listen failed: %v\n", err)
		os.Exit(2)
	}

	// The correct order, part 2: apply the bind restriction on a dedicated
	// OS thread that stays LOCKED and parked — its restricted M never
	// re-enters the scheduler pool, so no other goroutine can inherit it
	// and the serve/dial assertions below stay deterministic.
	restricted := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		childLandlockOrExit(issue1909BindPolicy(workspace))
		close(restricted)
		<-make(chan struct{}) // park forever; the process exits from elsewhere
	}()
	<-restricted

	// The pre-bound listener must still serve: TCP connects, HTTP answers.
	u, err := url.Parse("http://" + p.Addr())
	if err != nil {
		fmt.Fprintf(os.Stderr, "proxy addr parse failed: %v\n", err)
		os.Exit(2)
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(u),
		},
	}
	resp, err := client.Get("http://denied-not-listed.invalid/probe-1909")
	if err != nil {
		fmt.Fprintf(os.Stderr, "SPEC VIOLATION (control): pre-bound proxy stopped serving after restriction: %v\n", err)
		os.Exit(1)
	}
	if resp.StatusCode != http.StatusForbidden {
		fmt.Fprintf(os.Stderr, "SPEC VIOLATION (control): expected 403 for a non-listed host (package doc), got %d\n", resp.StatusCode)
		os.Exit(1)
	}
	// Checked close (lint: errcheck); every path below ends in os.Exit, which
	// never runs defers, so an explicit close is the only one that happens.
	if closeErr := resp.Body.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "response body close failed: %v\n", closeErr)
	}
	if closeErr := p.Close(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "proxy close failed: %v\n", closeErr)
	}
	fmt.Fprintf(os.Stderr, "pre-bound proxy on %s served 403 after the bind restriction\n", p.Addr())
	os.Exit(42)
}

// TestIssue1909_PortZeroBindDeniedUnderBindRestriction is the negative
// guard against the issue's named wrong fix ("NOT a port-0 allow rule — it
// would let every child bind any ephemeral port"): under the bind
// restriction, a bind to 127.0.0.1:0 — the exact syscall NewEgressProxy
// performs, which the kernel resolves from the ephemeral range — must fail
// with EACCES/EPERM (kernel net-bind semantics; the issue's observed error
// confirms it). If a fix adds port 0 to BindPortRules, the bind succeeds,
// the child exits 1, and this test fails — rejecting the fix.
func TestIssue1909_PortZeroBindDeniedUnderBindRestriction(t *testing.T) {
	if os.Getenv("OMNIPUS_1909_PORT0_CHILD") == "1" {
		runIssue1909PortZeroChild()
		return // unreachable — the child ends in os.Exit
	}
	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock restrictions)")
	}
	logIssue1909Kernel(t)
	if sandbox.ProbeLandlockABI() < 4 {
		t.Skipf("Landlock ABI v4 required for net bind rules (have v%d)", sandbox.ProbeLandlockABI())
	}
	runIssue1909Child(t, "OMNIPUS_1909_PORT0_CHILD",
		"TestIssue1909_PortZeroBindDeniedUnderBindRestriction",
		"bind(127.0.0.1:0) stayed denied under the bind restriction", 42)
}

func runIssue1909PortZeroChild() {
	runtime.LockOSThread()
	workspace := os.Getenv("OMNIPUS_LANDLOCK_SANDBOX_DIR")
	if workspace == "" {
		os.Exit(77)
	}
	childLandlockOrExit(issue1909BindPolicy(workspace))
	if err := rawTCPBindLoopbackPort(0); err == nil {
		fmt.Fprintf(os.Stderr, "SPEC VIOLATION (AC-3): bind(127.0.0.1:0) succeeded under the bind restriction — a port-0 allow is in force\n")
		os.Exit(1)
	} else if !isEACCES(err) {
		fmt.Fprintf(os.Stderr, "unexpected bind(0) error (not EACCES/EPERM): %v\n", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "bind(127.0.0.1:0) denied as expected\n")
	os.Exit(42)
}

// TestIssue1909_EphemeralRangeBindDeniedUnderBindRestriction is the second
// negative guard: a bind to a specific port inside net.ipv4.ip_local_port_range
// (where the kernel resolves 127.0.0.1:0 binds; 32768..60999 here) and
// outside the allow-list must stay denied. This rejects the broader wrong
// fix of allow-listing the ephemeral range (or a wide dev-range style span
// that swallows it). The port is read from the range at runtime with a
// documented fallback so the guard holds across hosts.
func TestIssue1909_EphemeralRangeBindDeniedUnderBindRestriction(t *testing.T) {
	if os.Getenv("OMNIPUS_1909_EPHEMERAL_CHILD") == "1" {
		runIssue1909EphemeralChild()
		return // unreachable — the child ends in os.Exit
	}
	if os.Getuid() == 0 {
		t.Skip("Landlock tests must run as non-root (root bypasses Landlock restrictions)")
	}
	logIssue1909Kernel(t)
	if sandbox.ProbeLandlockABI() < 4 {
		t.Skipf("Landlock ABI v4 required for net bind rules (have v%d)", sandbox.ProbeLandlockABI())
	}
	runIssue1909Child(t, "OMNIPUS_1909_EPHEMERAL_CHILD",
		"TestIssue1909_EphemeralRangeBindDeniedUnderBindRestriction",
		"ephemeral-range bind stayed denied under the bind restriction", 42)
}

// issue1909EphemeralPort returns a port inside net.ipv4.ip_local_port_range,
// the pool the kernel draws 127.0.0.1:0 assignments from. Fallback 49152 =
// the IANA dynamic-range floor, inside every common Linux default range.
func issue1909EphemeralPort() uint16 {
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return 49152
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		return 49152
	}
	lo, loErr := strconv.Atoi(fields[0])
	hi, hiErr := strconv.Atoi(fields[1])
	if loErr != nil || hiErr != nil || hi <= lo {
		return 49152
	}
	return uint16(lo + (hi-lo)/2) // middle of the ephemeral range
}

func runIssue1909EphemeralChild() {
	runtime.LockOSThread()
	workspace := os.Getenv("OMNIPUS_LANDLOCK_SANDBOX_DIR")
	if workspace == "" {
		os.Exit(77)
	}
	childLandlockOrExit(issue1909BindPolicy(workspace))
	port := issue1909EphemeralPort()
	if err := rawTCPBindLoopbackPort(port); err == nil {
		fmt.Fprintf(os.Stderr, "SPEC VIOLATION (AC-3): bind(127.0.0.1:%d) in the ephemeral range succeeded under the bind restriction\n", port)
		os.Exit(1)
	} else if !isEACCES(err) {
		fmt.Fprintf(os.Stderr, "unexpected bind(%d) error (not EACCES/EPERM): %v\n", port, err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "bind(127.0.0.1:%d) in the ephemeral range denied as expected\n", port)
	os.Exit(42)
}
