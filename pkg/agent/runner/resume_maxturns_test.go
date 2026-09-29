package runner

// resume_maxturns_test.go — #904 gate ROUND 2 gap: Resume after a successful
// Run reuses THAT Run's turn cap (D4: an external-CLI worker follows the
// resolved limit for the whole run, resumed or not; FR-004: no hidden
// default). Oracle: the cap passed to the first Run (resumeTestCap).
//
// Kills: Resume sends MaxTurns 0 (Run refuses it → Resume errors), and Resume
// sends any other cap (the claude child's --max-turns argument and the cap
// the resumed Run recorded both differ from resumeTestCap).
//
// The turn-cap fatal event is deliberately NOT the oracle here: the drivers
// call Cancel() before returning that event and streamParser's send races
// the cancelled context, so the event is dropped nondeterministically
// (reported as a separate finding). The command line and the recorded cap are
// deterministic.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const resumeTestCap = 37

// recordedCap returns the cap the driver's most recent Run recorded.
func recordedCap(t *testing.T, d ExternalAgentRunner) int {
	t.Helper()
	switch v := d.(type) {
	case *ClaudeDriver:
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.runMaxTurns
	case *CodexDriver:
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.runMaxTurns
	case *OpencodeDriver:
		v.mu.Lock()
		defer v.mu.Unlock()
		return v.runMaxTurns
	}
	t.Fatalf("unknown driver type %T", d)
	return 0
}

func TestResume_ReusesPriorRunTurnCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	cases := []struct {
		name     string
		binVar   *string
		doneLine string
		newD     func() ExternalAgentRunner
		argsCap  bool // the driver passes the cap on the child's command line
	}{
		{"claude", &claudeBinName, `{"type":"result","subtype":"success","result":"ok"}`,
			func() ExternalAgentRunner { return NewClaudeDriver(nil) }, true},
		{"codex", &codexBinName, `{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
			func() ExternalAgentRunner { return NewCodexDriver(nil) }, false},
		{"opencode", &opencodeBinName, `{"type":"session.complete","session_id":"s1"}`,
			func() ExternalAgentRunner { return NewOpencodeDriver(nil) }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argsLog := filepath.Join(t.TempDir(), "args.log")
			stub := writeStubScript(t, "#!/bin/sh\necho \"$*\" >> '"+argsLog+"'\nprintf '%s\\n' '"+tc.doneLine+"'\nexit 0\n")
			orig := *tc.binVar
			*tc.binVar = stub
			t.Cleanup(func() { *tc.binVar = orig })

			d := tc.newD()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			ch1, err := d.Run(ctx, RunOptions{Input: "first", MaxTurns: resumeTestCap})
			if err != nil {
				t.Fatalf("Run(MaxTurns=%d) error = %v", resumeTestCap, err)
			}
			drain(t, ch1)
			if got := recordedCap(t, d); got != resumeTestCap {
				t.Fatalf("instrument: the first Run recorded cap %d, want %d", got, resumeTestCap)
			}

			ch2, err := d.Resume(ctx, "run-resume")
			if err != nil {
				t.Fatalf("Resume after a successful Run(MaxTurns=%d) = %v; want the prior cap reused, not refused", resumeTestCap, err)
			}
			drain(t, ch2)
			if got := recordedCap(t, d); got != resumeTestCap {
				t.Fatalf("the resumed run used cap %d, want the prior Run's %d (D4)", got, resumeTestCap)
			}

			if !tc.argsCap {
				return
			}
			raw, err := os.ReadFile(argsLog)
			if err != nil {
				t.Fatalf("instrument: stub args log unreadable: %v", err)
			}
			var runs []string // invocations that carry a run (not the --version probe)
			for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
				if strings.Contains(l, "stream-json") {
					runs = append(runs, l)
				}
			}
			if len(runs) != 2 {
				t.Fatalf("instrument: want 2 run invocations (Run + Resume), got %d: %q", len(runs), runs)
			}
			wantArg := "--max-turns 37"
			if !strings.Contains(runs[0], wantArg) {
				t.Fatalf("instrument: first Run args %q lack %q", runs[0], wantArg)
			}
			// No --resume flag is expected: buildArgs deliberately omits it
			// (driver_claude.go::buildArgs doc comment).
			if !strings.Contains(runs[1], wantArg) {
				t.Fatalf("resumed child args = %q, want %q (the prior Run's cap)", runs[1], wantArg)
			}
		})
	}
}
