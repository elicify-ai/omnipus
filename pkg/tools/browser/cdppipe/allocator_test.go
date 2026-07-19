package cdppipe

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
)

// TestLaunchStart_ModifyCmdCannotOverrideStderr is the SF7/TD7 regression
// test: PipeOptions.ModifyCmd's doc says cdppipe owns cmd.Stderr and that
// assigning it from ModifyCmd is disallowed because cdppipe's own assignment
// "will win". Before the fix, cmd.Stderr was actually set BEFORE ModifyCmd
// ran, so a ModifyCmd hook that touched Stderr silently won instead — this
// proves the doc is now true: a ModifyCmd that tries to clear/replace
// Stderr is overridden by cdppipe's lineWriter (wired from opts.Errf).
func TestLaunchStart_ModifyCmdCannotOverrideStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only fake binary (sh)")
	}

	modifyCmdRan := false
	l := &launch{
		execPath: "sh",
		opts: PipeOptions{
			Args: []string{"-c", "exit 0"},
			ModifyCmd: func(cmd *exec.Cmd) {
				modifyCmdRan = true
				// Attempt to suppress/replace cdppipe's stderr diagnostics —
				// per the doc, this must NOT stick.
				cmd.Stderr = nil
			},
			Errf: func(string, ...any) {},
		},
	}

	if err := l.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(l.teardown)

	if !modifyCmdRan {
		t.Fatal("expected ModifyCmd to have run")
	}
	lw, ok := l.cmd.Stderr.(*lineWriter)
	if !ok {
		t.Fatalf(
			"expected cmd.Stderr to be cdppipe's lineWriter (winning over ModifyCmd's nil assignment), got %#v",
			l.cmd.Stderr,
		)
	}
	if lw.prefix != "cdppipe: chrome: " {
		t.Fatalf("unexpected lineWriter prefix: %q", lw.prefix)
	}
}

// TestLaunchStart_NoErrf_StderrLeftNil proves the inverse: when the caller
// supplied no Errf, cdppipe intentionally leaves cmd.Stderr as whatever
// ModifyCmd set it to (or nil) — the "cdppipe owns Stderr" behavior only
// applies when Errf is configured.
func TestLaunchStart_NoErrf_StderrLeftNil(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix-only fake binary (sh)")
	}

	l := &launch{
		execPath: "sh",
		opts: PipeOptions{
			Args: []string{"-c", "exit 0"},
		},
	}

	if err := l.start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(l.teardown)

	if l.cmd.Stderr != nil {
		t.Fatalf("expected cmd.Stderr to stay nil with no Errf configured, got %#v", l.cmd.Stderr)
	}
}
