//go:build !windows

package cdppipe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// The subprocess is the process boundary: launch, pipe bridge, CDP probe,
// deadlines and cleanup remain real. Expected outcomes come from the allocator
// contract: fail closed, preserve exit reason, and release owned resources.
func TestStartupProcessHelper(t *testing.T) {
	if os.Getenv("OMNIPUS_STARTUP_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "exit":
		fmt.Fprint(os.Stderr, "ProcessSingleton: private-profile-path https://private.example/?token=secret")
		os.Exit(37)
	case "exit-zero":
		os.Exit(0)
	case "stall":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "cdp":
		in := bufio.NewReader(os.NewFile(3, "cdp-in"))
		out := os.NewFile(4, "cdp-out")
		for {
			data, err := in.ReadBytes(0)
			if err != nil {
				os.Exit(0)
			}
			var req struct {
				ID int `json:"id"`
			}
			if json.Unmarshal(data[:len(data)-1], &req) != nil {
				os.Exit(9)
			}
			fmt.Fprintf(out, "{\"id\":%d,\"result\":{\"targetInfos\":[]}}\x00", req.ID)
		}
	}
	os.Exit(8)
}

func startupHelperOptions(t *testing.T, mode string) PipeOptions {
	t.Helper()
	return PipeOptions{UserDataDir: t.TempDir(), DialTimeout: 3 * time.Second, Env: []string{"OMNIPUS_STARTUP_HELPER=1", "GORACE=atexit_sleep_ms=0"}, ModifyCmd: func(cmd *exec.Cmd) {
		cmd.Args = []string{os.Args[0], "-test.run=^TestStartupProcessHelper$", "--", mode}
	}}
}

func TestStartupReportsEarlyExitStatusAndStderr(t *testing.T) {
	start := time.Now()
	_, _, err := NewPipeAllocator(context.Background(), os.Args[0], startupHelperOptions(t, "exit"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 37 {
		t.Fatalf("want subprocess exit status 37, got %v", err)
	}
	if !strings.Contains(err.Error(), "Chrome could not acquire its profile") {
		t.Fatalf("missing Chrome diagnostic: %v", err)
	}
	if strings.Contains(err.Error(), "private-profile-path") || strings.Contains(err.Error(), "token=secret") {
		t.Fatalf("private stderr leaked into returned error: %v", err)
	}
	// One second allows process scheduling while detecting the old three-second probe wait.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("dead process consumed probe deadline: %s", elapsed)
	}
}

func TestStartupRejectsCleanExitBeforeReadiness(t *testing.T) {
	_, _, err := NewPipeAllocator(context.Background(), os.Args[0], startupHelperOptions(t, "exit-zero"))
	if err == nil || !strings.Contains(err.Error(), "exited before") {
		t.Fatalf("want explicit exit-before-readiness error, got %v", err)
	}
}

func TestStartupPreservesProbeDeadline(t *testing.T) {
	opts := startupHelperOptions(t, "stall")
	opts.DialTimeout = 100 * time.Millisecond
	_, _, err := NewPipeAllocator(context.Background(), os.Args[0], opts)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded for live but silent process, got %v", err)
	}
}

func TestStartupParentCancellationReapsProcess(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	opts := startupHelperOptions(t, "cdp")
	opts.UserDataDir = ""
	var profile string
	original := opts.ModifyCmd
	opts.ModifyCmd = func(cmd *exec.Cmd) {
		for _, arg := range cmd.Args {
			if strings.HasPrefix(arg, "--user-data-dir=") {
				profile = strings.TrimPrefix(arg, "--user-data-dir=")
			}
		}
		original(cmd)
	}
	_, cancel, err := NewPipeAllocator(parent, os.Args[0], opts)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if profile == "" {
		t.Fatal("allocator did not create a profile")
	}
	cancelParent()
	deadline := time.Now().Add(time.Second)
	for {
		_, err := os.Stat(profile)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("parent cancellation left allocator resources behind: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Concurrent explicit cancellation must join the same cleanup safely.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(cancel)
	}
	wg.Wait()
}

func TestStartupDiagnosticTailBoundsAndPrivacy(t *testing.T) {
	for _, size := range []int{0, 1, 4095, 4096, 4097, 8192} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var tail diagnosticTail
			payload := strings.Repeat("x", size)
			tail.append([]byte(payload))
			want := min(size, 4096) // private startup memory budget specified as 4 KiB
			if len(tail.data) != want {
				t.Fatalf("retained %d bytes, want %d", len(tail.data), want)
			}
			hint := ""
			if size > 0 {
				hint = "Chrome emitted startup diagnostics; inspect browser logs"
			}
			if tail.hint() != hint {
				t.Fatalf("unexpected safe hint: %q", tail.hint())
			}
		})
	}
	var tail diagnosticTail
	tail.append([]byte(strings.Repeat("old", 2000)))
	tail.append([]byte("No rendezvous client private-data"))
	if len(tail.data) != 4096 || !strings.HasSuffix(string(tail.data), "No rendezvous client private-data") {
		t.Fatalf("tail did not retain newest diagnostic within budget: bytes=%d", len(tail.data))
	}
	if hint := tail.hint(); hint != "a Chrome helper could not contact its parent process; inspect browser startup logs" {
		t.Fatalf("unexpected safe classification: %q", hint)
	}
}

func TestStartupCancellationPreservesCallerProfile(t *testing.T) {
	opts := startupHelperOptions(t, "cdp")
	marker := opts.UserDataDir + "/caller-data"
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	_, cancel, err := NewPipeAllocator(context.Background(), os.Args[0], opts)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	cancel()
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("caller profile changed: data=%q error=%v", data, err)
	}
}
