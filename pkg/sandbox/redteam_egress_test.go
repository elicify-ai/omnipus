//go:build linux

// Package sandbox_test — insider-LLM red-team coverage for raw TCP egress.
//
// Threat C4 (raw TCP egress) from the insider-pentest report: the egress
// proxy only intercepts HTTP/HTTPS through HTTP_PROXY env vars. A raw
// socket() + connect() syscall sequence — easily crafted from any
// scripting language with sockets — would bypass the proxy entirely
// unless the kernel intervenes.
//
// Status: CLOSED in v0.2 (#155 item 4). pkg/sandbox/sandbox_linux.go
// computeRights now declares NET_CONNECT_TCP in handledAccessNet on
// Landlock ABI v4+, and DefaultPolicy seeds ConnectPortRules with
// {53, 80, 443} (DefaultConnectPorts). The gateway boot path additionally
// extends the allow-list with cfg.Sandbox.DevServerPortRange. Any
// connect(2) from the gateway or any of its forked children to a port
// outside that union — e.g. the redteam target 127.0.0.1:1 — returns
// EACCES at the kernel layer.
//
// Limitation acknowledged: this is port-level filtering. A child can
// still dial RFC1918 IPs on allowed ports (192.168.1.1:443 etc.). CIDR-
// level filtering for compiled binaries would require eBPF cgroup, which
// is deferred. cfg.Sandbox.EgressAllowCIDRs is the operator escape hatch
// for the Go-side SSRFChecker layer that filters by IP/CIDR for the HTTP
// clients the gateway controls (web_search, MCP, skills installer).
//
// Test mechanics:
//
//   - The child applies the production sandbox policy to its own thread.
//   - It then attempts a raw socket() + connect() to a sentinel address.
//     We use 127.0.0.1:1 (port 1, which no benign service binds and which
//     is a closed loopback port). This avoids ANY external network round
//     trip — the connect either fails locally with ECONNREFUSED (no
//     enforcement) or with EACCES/EPERM (kernel block). We never touch
//     1.1.1.1 or any public IP.
//   - ECONNREFUSED indicates the kernel let the syscall through and the
//     remote port refused; that's the "no enforcement" outcome.
//   - EACCES/EPERM indicates the kernel blocked the syscall; that's the
//     "enforcement present" outcome.

package sandbox_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// rawTCPConnect issues connect(2) directly on a fresh AF_INET TCP socket,
// bypassing Go's net.Dial layer. Landlock NET_CONNECT_TCP, when handled,
// enforces at the connect(2) syscall — net.Dial has fallback paths that
// can mask the rule on some Go versions, so for a deterministic
// kernel-level test we go through the syscall directly. This mirrors
// rawTCPBind in backend_linux_subprocess_test.go.
//
// We connect to 127.0.0.1:1 — guaranteed-closed loopback — so even on a
// system with no kernel enforcement the syscall returns ECONNREFUSED
// quickly without any external network traffic.
func rawTCPConnect(ip [4]byte, port uint16) error {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	defer syscall.Close(fd)
	sa := &syscall.SockaddrInet4{Addr: ip, Port: int(port)}
	return syscall.Connect(fd, sa)
}

// runEgressChild is the in-child phase of TestRedteam_RawTCP_Egress_Blocked.
// Exit codes:
//
//	42 — connect was BLOCKED at the kernel layer (EACCES/EPERM). PASS.
//	77 — Landlock unavailable in the child. Parent skips.
//	1  — connect attempt was let through by the kernel (succeeded or
//	     refused at the remote end). The control is missing — FAIL.
//	2  — unexpected errno (not nil, not EACCES/EPERM, not ECONNREFUSED).
//	     Test environment problem.
func runEgressChild() {
	runtime.LockOSThread()

	home := os.Getenv("OMNIPUS_REDTEAM_HOME")
	if home == "" {
		fmt.Fprintln(os.Stderr, "redteam_egress: missing OMNIPUS_REDTEAM_HOME")
		os.Exit(77)
	}

	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		os.Exit(77)
	}

	// Use the production policy, exactly. Includes the bind-port allow-list
	// for completeness even though we exercise CONNECT here, not BIND.
	policy := sandbox.DefaultPolicy(home, nil, nil, []uint16{18001})
	if err := backend.Apply(policy); err != nil {
		fmt.Fprintf(os.Stderr, "Apply failed (skip): %v\n", err)
		os.Exit(77)
	}

	// 127.0.0.1:1 — guaranteed-closed loopback. We never touch the
	// external network. The connect either:
	//   - returns EACCES/EPERM (kernel blocked it; rule applies)
	//   - returns ECONNREFUSED (kernel let it through; remote refused)
	//   - returns EADDRNOTAVAIL or similar (test environment problem)
	err := rawTCPConnect([4]byte{127, 0, 0, 1}, 1)
	if err == nil {
		// Connect SUCCEEDED to a closed port? That would be very weird;
		// treat as test environment problem.
		fmt.Fprintf(os.Stderr, "redteam_egress: connect to 127.0.0.1:1 unexpectedly SUCCEEDED\n")
		os.Exit(2)
	}
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		fmt.Fprintf(os.Stderr, "redteam_egress: kernel BLOCKED connect: %v\n", err)
		os.Exit(42)
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		// Kernel let the syscall reach the network stack; remote refused.
		// That's the "no enforcement" outcome — exactly the gap we document.
		fmt.Fprintf(os.Stderr, "redteam_egress: kernel ALLOWED connect; remote refused (gap confirmed): %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "redteam_egress: unexpected connect errno: %v\n", err)
	os.Exit(2)
}

// TestRedteam_RawTCP_Egress_Blocked documents C4. The child applies the
// production sandbox policy and tries a raw TCP connect to 127.0.0.1:1
// (closed loopback port). With NET_CONNECT_TCP enforcement, the kernel
// returns EACCES BEFORE the syscall reaches the network stack. Without it,
// the kernel forwards the syscall and the closed port returns ECONNREFUSED.
//
// Status: closed by v0.2 (#155 item 4). NET_CONNECT_TCP is now declared in
// handledAccessNet on ABI v4+ (sandbox_linux.go::computeRights), and
// DefaultPolicy seeds ConnectPortRules with {53, 80, 443}. Port 1 is not
// in the allow-list, so the kernel returns EACCES — the test exits 42.
//
// Loopback target rationale: we deliberately do NOT attempt to connect
// to a public IP. (1) The test must not generate external network
// traffic. (2) The control we're testing is "kernel blocks the connect
// syscall regardless of destination", so any reachable destination
// works. Loopback to a closed port gives us ECONNREFUSED quickly with
// zero external impact.
func TestRedteam_RawTCP_Egress_Blocked(t *testing.T) {
	t.Logf(
		"documents C4 (raw TCP egress) from insider-pentest report; closes when v0.2 #155 adds NET_CONNECT_TCP enforcement",
	)

	if os.Getenv("OMNIPUS_REDTEAM_EGRESS_CHILD") == "1" {
		runEgressChild()
		return
	}

	if os.Getuid() == 0 {
		t.Skip("must run as non-root (root bypasses Landlock)")
	}

	abi := sandbox.ProbeLandlockABI()
	if abi < 4 {
		t.Skipf("Landlock ABI v4+ required for any NET_* enforcement (have v%d)", abi)
	}

	backend, name := sandbox.SelectBackend()
	if !strings.HasPrefix(name, "landlock") {
		t.Skipf("Landlock backend not available (backend=%q)", name)
	}
	_ = backend

	home := t.TempDir()
	//nolint:gosec // intentional self-exec
	cmd := exec.Command(os.Args[0],
		"-test.run=TestRedteam_RawTCP_Egress_Blocked",
		"-test.count=1",
		"-test.v",
	)
	cmd.Env = append(os.Environ(),
		"OMNIPUS_REDTEAM_EGRESS_CHILD=1",
		"OMNIPUS_REDTEAM_HOME="+home,
	)
	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("child failed to spawn: %v\n%s", err, out)
		}
	}

	switch exitCode {
	case 42:
		t.Logf("C4 closed: kernel blocked raw TCP connect inside sandboxed child")
	case 77:
		t.Skipf("Landlock unavailable in child (exit 77):\n%s", out)
	case 1:
		t.Errorf(
			"C4 GAP CONFIRMED: sandboxed child completed raw TCP connect — NET_CONNECT_TCP enforcement is intentionally disabled (sandbox_linux.go:160). Fix in v0.2 (#155).\nchild stderr:\n%s",
			out,
		)
	default:
		t.Fatalf("child exit %d (unexpected — expected 1=gap, 42=fixed, 77=skip):\n%s", exitCode, out)
	}
}
