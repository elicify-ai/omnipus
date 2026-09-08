//go:build !windows

package cdppipe

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// This child acknowledges receiving the real CDP probe, then deliberately
// leaves it unanswered. The parent cancels only after that protocol barrier.
func TestStartupContextProcessHelper(t *testing.T) {
	if os.Getenv("OMNIPUS_STARTUP_CONTEXT_HELPER") != "1" {
		return
	}
	input := bufio.NewReader(os.NewFile(3, "cdp-in"))
	if _, err := input.ReadBytes(0); err != nil {
		os.Exit(7)
	}
	if err := os.WriteFile(os.Getenv("OMNIPUS_STARTUP_CONTEXT_READY"), []byte("probe received"), 0600); err != nil {
		os.Exit(8)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestStartupContextCancellationReapsBlockedProcess(t *testing.T) {
	caller, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	opts := startupHelperOptions(t, "stall")
	ready := filepath.Join(t.TempDir(), "probe-ready")
	opts.Env = append(opts.Env, "OMNIPUS_STARTUP_CONTEXT_HELPER=1", "OMNIPUS_STARTUP_CONTEXT_READY="+ready)
	captured := make(chan *exec.Cmd, 1)
	opts.ModifyCmd = func(cmd *exec.Cmd) {
		cmd.Args = []string{os.Args[0], "-test.run=^TestStartupContextProcessHelper$"}
		captured <- cmd
	}
	type result struct {
		cancel context.CancelFunc
		err    error
	}
	done := make(chan result, 1)
	var started time.Time
	go func() {
		_, cancel, err := NewPipeAllocatorWithStartupContext(context.Background(), caller, os.Args[0], opts)
		done <- result{cancel, err}
	}()
	cmd := <-captured
	readyDeadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(readyDeadline) {
			cancelCaller()
			got := <-done
			if got.cancel != nil {
				got.cancel()
			}
			t.Fatal("child did not reach the blocked CDP probe")
		}
		time.Sleep(5 * time.Millisecond)
	}
	started = time.Now()
	cancelCaller()
	got := <-done
	if got.cancel != nil {
		got.cancel()
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Errorf("startup cancellation returned %v, want caller cancellation", got.err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("canceled startup waited %s for its probe deadline", elapsed)
	}
	if cmd.Process != nil && cmd.ProcessState == nil {
		t.Error("canceled startup returned before its child was reaped")
	}
}

func TestStartupContextPreCanceledDoesNotSpawn(t *testing.T) {
	caller, cancelCaller := context.WithCancel(context.Background())
	cancelCaller()
	opts := startupHelperOptions(t, "cdp")
	called := false
	original := opts.ModifyCmd
	opts.ModifyCmd = func(cmd *exec.Cmd) { called = true; original(cmd) }
	root, cancel, err := NewPipeAllocatorWithStartupContext(context.Background(), caller, os.Args[0], opts)
	if cancel != nil {
		cancel()
	}
	if called || root != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("pre-canceled startup called=%t root=%v err=%v", called, root, err)
	}
}

func TestStartupContextAcceptedBrowserOutlivesHandshakeCaller(t *testing.T) {
	caller, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	root, cancel, err := NewPipeAllocatorWithStartupContext(context.Background(), caller, os.Args[0], startupHelperOptions(t, "cdp"))
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	cancelCaller()
	operation, done := context.WithTimeout(root, time.Second)
	defer done()
	_, err = target.GetTargets().Do(cdp.WithExecutor(operation, chromedp.FromContext(root).Browser))
	if err != nil {
		t.Fatalf("accepted browser lost CDP after handshake caller canceled: %v", err)
	}
}
