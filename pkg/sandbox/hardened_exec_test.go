// Cross-platform tests for hardened_exec.Run. Platform-specific behavior
// (memory cap enforcement) lives in build-tagged test files.

package sandbox

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRun_EmptyArgvReturnsError exercises the input-validation guard so a
// caller cannot accidentally launch nothing and silently succeed.
func TestRun_EmptyArgvReturnsError(t *testing.T) {
	_, err := Run(context.Background(), nil, nil, Limits{})
	if err == nil {
		t.Fatal("expected error for empty argv, got nil")
	}
	if err != ErrEmptyArgv {
		t.Errorf("err = %v; want ErrEmptyArgv", err)
	}
}

// TestRun_ZeroExitCaptured verifies a successful child returns exit 0
// and its stdout is captured. Uses /bin/echo on unix, cmd /c echo on
// windows.
func TestRun_ZeroExitCaptured(t *testing.T) {
	argv := echoArgv("hello")
	res, err := Run(context.Background(), argv, nil, Limits{TimeoutSeconds: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(string(res.Stdout), "hello") {
		t.Errorf("stdout = %q, want to contain %q", res.Stdout, "hello")
	}
}

// TestRun_TimeoutKillsChild exercises wall-clock cancellation. Spawns a
// long-running sleep and checks that Run returns within the timeout
// window with TimedOut=true.
func TestRun_TimeoutKillsChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sleep semantics differ on windows")
	}
	start := time.Now()
	res, err := Run(context.Background(), []string{"sleep", "10"}, nil, Limits{TimeoutSeconds: 1})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("TimedOut = false; want true")
	}
	// Allow generous slack for slow CI runners.
	if elapsed > 8*time.Second {
		t.Errorf("elapsed = %v; expected ~1 s due to timeout", elapsed)
	}
}

// TestMergeEnv_InjectsProxyAndCache verifies the env-injection contract:
// HTTP_PROXY/HTTPS_PROXY (and lowercase variants) appear when the proxy
// addr is set, and npm_config_cache is set when WorkspaceDir is non-empty.
// Caller-supplied env appears first; injected vars come after so they
// take precedence on duplicate keys (POSIX exec semantics).
func TestMergeEnv_InjectsProxyAndCache(t *testing.T) {
	in := []string{"USER=alice", "HOME=/home/alice"}
	merged := mergeEnv(in, Limits{
		EgressProxyAddr: "127.0.0.1:54321",
		WorkspaceDir:    "/tmp/ws",
	})
	wantContains := []string{
		"USER=alice",
		"HOME=/home/alice",
		"HTTP_PROXY=http://127.0.0.1:54321",
		"HTTPS_PROXY=http://127.0.0.1:54321",
		"http_proxy=http://127.0.0.1:54321",
		"https_proxy=http://127.0.0.1:54321",
		"NO_PROXY=127.0.0.1,localhost,::1",
		"no_proxy=127.0.0.1,localhost,::1",
		"npm_config_cache=/tmp/ws/.npm-cache",
	}
	for _, want := range wantContains {
		found := false
		for _, e := range merged {
			if e == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("merged env missing %q; full list: %v", want, merged)
		}
	}

	// Caller env first, injected after — verify the proxy entry comes
	// AFTER the user-supplied entries.
	userIdx := indexOf(merged, "USER=alice")
	proxyIdx := indexOf(merged, "HTTP_PROXY=http://127.0.0.1:54321")
	if userIdx < 0 || proxyIdx < 0 {
		t.Fatalf("missing key in merged; userIdx=%d proxyIdx=%d", userIdx, proxyIdx)
	}
	if userIdx >= proxyIdx {
		t.Errorf("user env (idx=%d) should precede proxy env (idx=%d)", userIdx, proxyIdx)
	}
}

// TestMergeEnv_NoProxyWhenAddrEmpty confirms that callers who do not pass
// an EgressProxyAddr get a clean env with only the npm cache + their own
// vars — no proxy injection.
func TestMergeEnv_NoProxyWhenAddrEmpty(t *testing.T) {
	merged := mergeEnv([]string{"X=1"}, Limits{WorkspaceDir: "/tmp"})
	for _, e := range merged {
		if strings.HasPrefix(strings.ToUpper(e), "HTTP_PROXY=") ||
			strings.HasPrefix(strings.ToUpper(e), "HTTPS_PROXY=") {
			t.Errorf("unexpected proxy var without addr: %q", e)
		}
	}
}

// TestCappedBuffer_TruncatesWithNotice exercises the capped-output writer
// to make sure long stdout/stderr is bounded but reported as such.
func TestCappedBuffer_TruncatesWithNotice(t *testing.T) {
	cb := cappedBuffer{cap: 16}
	for i := 0; i < 5; i++ {
		_, _ = cb.Write([]byte("0123456789"))
	}
	out := cb.Bytes()
	if len(out) <= 16 {
		t.Errorf("expected output > cap (notice appended), got len=%d", len(out))
	}
	if !strings.Contains(string(out), "truncated") {
		t.Errorf("output missing truncation notice: %q", out)
	}
	// Untruncated buffer should NOT carry the notice.
	cb2 := cappedBuffer{cap: 100}
	_, _ = cb2.Write([]byte("short"))
	if strings.Contains(string(cb2.Bytes()), "truncated") {
		t.Errorf("untruncated buffer should not include notice: %q", cb2.Bytes())
	}
}

// TestStderrTail returns the last n bytes of stderr trimmed at a line
// boundary so error messages surface cleanly.
func TestStderrTail(t *testing.T) {
	res := Result{Stderr: []byte("line1\nline2\nline3 final\n")}
	tail := res.StderrTail(20)
	if !strings.Contains(tail, "line3 final") {
		t.Errorf("tail = %q; want to contain 'line3 final'", tail)
	}
}

// echoArgv returns the argv to print "msg" to stdout on the current OS.
func echoArgv(msg string) []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c", "echo", msg}
	}
	return []string{"echo", msg}
}

func indexOf(slice []string, target string) int {
	for i, s := range slice {
		if s == target {
			return i
		}
	}
	return -1
}
