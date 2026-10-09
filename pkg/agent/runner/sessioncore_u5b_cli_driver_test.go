// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack, session-core U5b (external CLI steering: interrupt + real
// native-conversation resume). Spec: docs/internal/specs/session-core-spec.md
// FR-043, BDD-05.5/05.6, DEL-20, T19/T31. ADR D4/D6/D8
// (ADR-20261006-session-core-with-an-agent-address-book.md).
//
// FR-043: "Three current CLI drivers MUST deliver live instruction by interrupt
// plus actual native-conversation resume with runtime/workspace/model/caps
// preserved. Missing native ID/driver/auth or failed/superseded delivery visibly
// refuses; no no-op/fabricated ID/fresh fallback."
// BDD-05.6: "absent/invalid native ID, absent CLI/bad auth, rejecting/crashing
// resume, selected execution superseded ⇒ Visible failure, no no-op
// success/fabricated ID/silent fresh conversation."
//
// The oracle here is the SPEC, not the current implementation: today
// `Input` is a documented no-op that returns nil and `Resume` starts a FRESH
// conversation (buildArgs omits every CLI's native-resume flag). Every
// expected value below is derived from FR-043, never read off the code.
//
// T31's installed-CLI lane is NOT exercised here (no real claude/codex/opencode
// binary in CI); these tests use a stub CLI only to observe the driver contract
// and the observable failure, as the dispatch brief scopes RED.

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

// u5bCase is one CLI driver's RED fixtures. nativeLine is the NDJSON line the
// stub CLI prints to announce its native conversation id ("" = the CLI never
// announces one, the BDD-05.6 "absent native ID" case). runMarker is an argv
// substring that appears only on a real Run invocation (never on the --version
// probe), so we can tell the invocations apart in the stub's argv log.
type u5bCase struct {
	name       string
	binVar     *string
	runMarker  string
	nativeLine string
	doneLine   string
	nativeID   string
	newD       func() ExternalAgentRunner
}

func u5bCases() []u5bCase {
	return []u5bCase{
		{
			name:      "claude",
			binVar:    &claudeBinName,
			runMarker: "stream-json",
			// claude announces its session id in the system/init line
			// (driver_claude.go parses session_id from it today).
			nativeLine: `{"type":"system","subtype":"init","session_id":"native-claude-42"}`,
			doneLine:   `{"type":"result","subtype":"success","result":"ok"}`,
			nativeID:   "native-claude-42",
			newD:       func() ExternalAgentRunner { return NewClaudeDriver(nil) },
		},
		{
			name:      "codex",
			binVar:    &codexBinName,
			runMarker: "exec",
			// codex carries its native thread id in the thread_id field
			// (declared today on codexStreamEvent, never read).
			nativeLine: `{"type":"thread.started","thread_id":"native-codex-42"}`,
			doneLine:   `{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}`,
			nativeID:   "native-codex-42",
			newD:       func() ExternalAgentRunner { return NewCodexDriver(nil) },
		},
		{
			name:      "opencode",
			binVar:    &opencodeBinName,
			runMarker: "run",
			// opencode announces its session id on session events.
			nativeLine: `{"type":"session.start","session_id":"native-opencode-42"}`,
			doneLine:   `{"type":"session.complete"}`,
			nativeID:   "native-opencode-42",
			newD:       func() ExternalAgentRunner { return NewOpencodeDriver(nil) },
		},
	}
}

// u5bStubScript builds a POSIX stub CLI that logs its argv to argsLog, prints
// nativeLine (when non-empty) then doneLine, and exits 0.
func u5bStubScript(t *testing.T, argsLog, nativeLine, doneLine string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(`echo "$*" >> '` + argsLog + "'\n")
	if nativeLine != "" {
		b.WriteString("printf '%s\\n' '" + nativeLine + "'\n")
	}
	b.WriteString("printf '%s\\n' '" + doneLine + "'\n")
	b.WriteString("exit 0\n")
	return writeStubScript(t, b.String())
}

// u5bRunInvocations filters the stub argv log down to the real Run invocations
// (the ones carrying runMarker), in order.
func u5bRunInvocations(t *testing.T, argsLog, runMarker string) []string {
	t.Helper()
	raw, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("instrument: stub argv log unreadable: %v", err)
	}
	var runs []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.Contains(l, runMarker) {
			runs = append(runs, l)
		}
	}
	return runs
}

// BDD-05.6 / FR-043: a driver whose Input cannot be delivered to any live
// native conversation MUST refuse visibly — never the current documented
// no-op that silently discards the text and returns nil.
func TestSessionCoreU5b_InputWithoutLiveNativeConversationMustRefuseVisibly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	for _, tc := range u5bCases() {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.newD()
			err := d.Input("operator steer: switch to plan B")
			if err == nil {
				t.Fatalf(
					"FR-043/BDD-05.6: %s Input() returned nil with no live native conversation; a discarded instruction must be a VISIBLE failure, not a silent no-op",
					tc.name,
				)
			}
		})
	}
}

// BDD-05.6 / FR-043: Resume MUST NOT silently start a fresh conversation when
// the driver never captured a native conversation id. Today Resume re-runs from
// scratch and returns a nil error — the exact "silent fresh conversation" the
// spec forbids.
func TestSessionCoreU5b_ResumeWithoutCapturedNativeIDMustRefuseVisibly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	for _, tc := range u5bCases() {
		t.Run(tc.name, func(t *testing.T) {
			argsLog := filepath.Join(t.TempDir(), "args.log")
			// nativeLine empty: the CLI never announces a native conversation id.
			stub := u5bStubScript(t, argsLog, "", tc.doneLine)
			orig := *tc.binVar
			*tc.binVar = stub
			t.Cleanup(func() { *tc.binVar = orig })

			d := tc.newD()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			ch, err := d.Run(ctx, RunOptions{Input: "first", MaxTurns: 37})
			if err != nil {
				t.Fatalf("instrument: Run() error = %v (stub CLI should complete)", err)
			}
			drain(t, ch)

			ch2, err := d.Resume(ctx, "run-resume")
			if err == nil {
				drain(t, ch2)
				t.Fatalf(
					"FR-043/BDD-05.6: %s Resume() with no captured native conversation id returned nil — it silently started a FRESH conversation instead of visibly refusing",
					tc.name,
				)
			}
		})
	}
}

// BDD-05.5 / FR-043: after a run whose CLI announced a native conversation id,
// Resume MUST reference that same native id on the resumed command line — the
// essence of "actual native-conversation resume". Today the resumed invocation
// omits every CLI's native-resume flag, so the native id never appears.
func TestSessionCoreU5b_ResumeReferencesCapturedNativeConversationID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub uses a POSIX shell script")
	}
	for _, tc := range u5bCases() {
		t.Run(tc.name, func(t *testing.T) {
			argsLog := filepath.Join(t.TempDir(), "args.log")
			stub := u5bStubScript(t, argsLog, tc.nativeLine, tc.doneLine)
			orig := *tc.binVar
			*tc.binVar = stub
			t.Cleanup(func() { *tc.binVar = orig })

			d := tc.newD()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			ch, err := d.Run(ctx, RunOptions{Input: "first", MaxTurns: 37})
			if err != nil {
				t.Fatalf("instrument: Run() error = %v", err)
			}
			drain(t, ch)

			ch2, err := d.Resume(ctx, "run-resume")
			if err != nil {
				t.Fatalf("FR-043: %s Resume() after a run that announced native id %q = %v; want the same native conversation resumed", tc.name, tc.nativeID, err)
			}
			drain(t, ch2)

			runs := u5bRunInvocations(t, argsLog, tc.runMarker)
			if len(runs) != 2 {
				t.Fatalf("instrument: want 2 run invocations (Run + Resume), got %d: %q", len(runs), runs)
			}
			if !strings.Contains(runs[1], tc.nativeID) {
				t.Fatalf(
					"FR-043/BDD-05.5: %s resumed argv %q does not carry the captured native conversation id %q — Resume started a fresh conversation instead of resuming it",
					tc.name, runs[1], tc.nativeID,
				)
			}
		})
	}
}
