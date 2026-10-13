// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// U5b follow-up (FR-043): the delivery resume carries the NEW instruction S to
// the SAME native CLI conversation, and preserves runtime/workspace/model caps.
//
// This is the driver-boundary proof the dispatch brief scopes ("assert the
// delivery path with the existing stub/instrument boundary"); the installed-CLI
// end-to-end lane is T19/T31 and is deferred to UAT/W4. The oracle is FR-043,
// not the implementation: a resumed invocation must (1) carry the captured
// native conversation id, (2) receive S on its stdin, (3) keep the run's model
// cap, and (4) run in the same workspace directory as the first run.

package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// deliveryModel is deliberately provider/model-shaped so opencode's
// looksLikeProviderModel guard passes it through (claude and codex accept it
// verbatim).
const deliveryModel = "openai/gpt-4o-mini"

// deliveryMaxTurns is the turn cap the first Run is given; FR-043 requires the
// instruction-bearing Resume to retain it (a delivery mutant that resets the cap
// to 1 must die here).
const deliveryMaxTurns = 37

// u5bDeliveryStub builds a POSIX stub CLI that logs its argv, delivers its
// stdin to stdinLog, and drops a marker file in its own working directory — but
// ONLY on a real run invocation (one carrying marker), so the `--version` probe
// (whose cwd is not the run's workspace) never pollutes the assertions.
func u5bDeliveryStub(t *testing.T, argsLog, stdinLog, marker, nativeLine, doneLine string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(`echo "$*" >> '` + argsLog + "'\n")
	b.WriteString(`case "$*" in` + "\n")
	b.WriteString("  *'" + marker + "'*)\n")
	b.WriteString("    cat >> '" + stdinLog + "' 2>/dev/null\n")
	b.WriteString("    printf '\\n' >> '" + stdinLog + "'\n")
	b.WriteString("    : > u5b-ran-$$.marker\n")
	b.WriteString("    ;;\n")
	b.WriteString("esac\n")
	if nativeLine != "" {
		b.WriteString("printf '%s\\n' '" + nativeLine + "'\n")
	}
	b.WriteString("printf '%s\\n' '" + doneLine + "'\n")
	b.WriteString("exit 0\n")
	return writeStubScript(t, b.String())
}

// countMarkers returns how many u5b-ran-*.marker files exist in dir.
func countMarkers(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "u5b-ran-*.marker"))
	if err != nil {
		t.Fatalf("glob markers: %v", err)
	}
	return len(matches)
}

func TestSessionCoreU5bFollowup_ResumeDeliversInstructionAndRetainsCaps(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Founder-accepted platform gap: issue #1256 (2026-10-09) accepts the
		// missing Windows equivalents for this landing and tracks them. The
		// fixture is a POSIX shell stub; the Windows replacement proof is owed.
		t.Skip("stub uses a POSIX shell script (Windows equivalent owed — founder-accepted gap, issue #1256)")
	}
	cases := []struct {
		name       string
		binVar     *string
		marker     string
		nativeLine string
		doneLine   string
		nativeID   string
		modelFlag  string
		// maxTurnsFlag is the child argv fragment carrying the turn cap, for the
		// driver that passes it on the command line (claude); empty otherwise.
		maxTurnsFlag string
		newD         func() ExternalAgentRunner
	}{
		{
			name:         "claude",
			binVar:       &claudeBinName,
			marker:       "stream-json",
			nativeLine:   `{"type":"system","subtype":"init","session_id":"native-claude-77"}`,
			doneLine:     `{"type":"result","subtype":"success","result":"ok"}`,
			nativeID:     "native-claude-77",
			modelFlag:    "--model " + deliveryModel,
			maxTurnsFlag: "--max-turns 37",
			newD:         func() ExternalAgentRunner { return NewClaudeDriver(nil) },
		},
		{
			name:       "codex",
			binVar:     &codexBinName,
			marker:     "exec",
			nativeLine: `{"type":"thread.started","thread_id":"native-codex-77"}`,
			doneLine:   `{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
			nativeID:   "native-codex-77",
			modelFlag:  "-m " + deliveryModel,
			newD:       func() ExternalAgentRunner { return NewCodexDriver(nil) },
		},
		{
			name:       "opencode",
			binVar:     &opencodeBinName,
			marker:     "run",
			nativeLine: `{"type":"session.start","session_id":"native-opencode-77"}`,
			doneLine:   `{"type":"session.complete"}`,
			nativeID:   "native-opencode-77",
			modelFlag:  "--model " + deliveryModel,
			newD:       func() ExternalAgentRunner { return NewOpencodeDriver(nil) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			argsLog := filepath.Join(dir, "args.log")
			stdinLog := filepath.Join(dir, "stdin.log")
			workDir := filepath.Join(dir, "work")
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				t.Fatalf("mkdir work: %v", err)
			}
			stub := u5bDeliveryStub(t, argsLog, stdinLog, tc.marker, tc.nativeLine, tc.doneLine)
			orig := *tc.binVar
			*tc.binVar = stub
			t.Cleanup(func() { *tc.binVar = orig })

			d := tc.newD()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			ch, err := d.Run(ctx, RunOptions{
				Input: "first instruction", MaxTurns: deliveryMaxTurns, Model: deliveryModel, WorkDir: workDir,
			})
			if err != nil {
				t.Fatalf("instrument: first Run error = %v", err)
			}
			drain(t, ch)

			// FR-043 delivery: resume the native conversation WITH the new
			// instruction S.
			ch2, err := d.Resume(ctx, "run-resume", "follow-up instruction S")
			if err != nil {
				t.Fatalf("FR-043: %s Resume with the new instruction = %v; want the native conversation resumed", tc.name, err)
			}
			drain(t, ch2)

			runs := u5bRunInvocations(t, argsLog, tc.marker)
			if len(runs) != 2 {
				t.Fatalf("instrument: want 2 run invocations (Run + Resume), got %d: %q", len(runs), runs)
			}
			if !strings.Contains(runs[1], tc.nativeID) {
				t.Fatalf("FR-043: resumed argv %q does not carry native conversation id %q", runs[1], tc.nativeID)
			}
			if !strings.Contains(runs[1], tc.modelFlag) {
				t.Fatalf("FR-043 caps: resumed argv %q does not retain the run's model (%q)", runs[1], tc.modelFlag)
			}
			// FR-043 caps: the instruction-bearing resume must retain the run's
			// TURN CAP. A delivery mutant that resets it (e.g. opts.MaxTurns = 1
			// in Resume) leaves the name/comment claiming cap retention while the
			// real cap is lost — this assertion is what makes that claim true.
			if got := recordedCap(t, d); got != deliveryMaxTurns {
				t.Fatalf("FR-043 caps: the instruction-bearing resume recorded turn cap %d, want the first Run's %d", got, deliveryMaxTurns)
			}
			if tc.maxTurnsFlag != "" && !strings.Contains(runs[1], tc.maxTurnsFlag) {
				t.Fatalf("FR-043 caps: resumed argv %q does not retain the run's turn cap (%q)", runs[1], tc.maxTurnsFlag)
			}

			stdin, err := os.ReadFile(stdinLog)
			if err != nil {
				t.Fatalf("instrument: stdin log unreadable: %v", err)
			}
			// Each CLI delivers its prompt its own way — claude -p and
			// `codex exec -` read it from stdin, opencode `run -- <prompt>`
			// takes it as a positional argument — so the delivery is observed
			// in the run's stdin OR its argv. What FR-043 requires is that the
			// text actually REACHES the CLI, whatever the channel.
			delivered := string(stdin) + "\n" + runs[1]
			if !strings.Contains(string(stdin)+"\n"+runs[0], "first instruction") {
				t.Fatalf("instrument: the FIRST instruction never reached a run; stdin=%q argv=%q", string(stdin), runs[0])
			}
			if !strings.Contains(delivered, "follow-up instruction S") {
				t.Fatalf("FR-043: the new instruction S did not reach the resumed conversation; stdin=%q argv=%q", string(stdin), runs[1])
			}

			if got := countMarkers(t, workDir); got != 2 {
				t.Fatalf("FR-043 workspace retention: %d run(s) executed in the workspace dir, want 2 (first run + resume)", got)
			}
		})
	}
}
