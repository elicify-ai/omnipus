//go:build linux

package preview

// T6 (office preview, CI triage round 3): a CPU-limit kill of a preview
// program is the time-limit outcome, never "PDF renderer failed (protocol)".
//
// The launcher arms RLIMIT_CPU with soft = hard = programCPUSeconds before
// execve of the target (launcher/child_unix.go::applyCommonLimits), and on
// Linux the kernel kills at the hard limit with SIGKILL (posix-cpu-timers).
// The Go ProcessState text of that death is "signal: killed" — it never
// carries the "cputime limit exceeded" words macOS produces (SIGXCPU), so
// the pre-fix classification (a text match on those words) missed it on
// Linux and the death fell through to the protocol/EOF branch. The spec
// behaviour the fix (and this test) pin: a program ended by a signal after
// using at least programCPUSeconds of CPU is classified as the CPU time
// limit outcome; a signal death WITHOUT the CPU budget is not.
//
// This file is Linux-only because the defect it guards exists only where
// the death text lacks the cputime words: on macOS the same launcher
// mechanism kills with SIGXCPU and the pre-fix text check already matched.

import (
	"crypto/sha256"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
)

// Child modes of this test binary, hosted on distinct argv patterns (the
// launcher execve's the target in the shim's own process, so the process the
// parent Waits on IS this binary running the mode below).
var cpuSpinSink byte
var cpuSpinRounds uint64

func init() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case "__cpu-limit-spin":
		// Burn CPU until the hard RLIMIT_CPU (soft = hard,
		// programCPUSeconds) kills us. The hash loop is real work: a bare
		// counter risks being optimised, and nothing here sleeps, so every
		// wall second is a CPU second.
		for {
			sum := sha256.Sum256([]byte{byte(cpuSpinRounds)})
			cpuSpinSink ^= sum[0]
			cpuSpinRounds++
		}
	case "__cpu-limit-signal-death":
		// A signal death that uses no CPU budget: SIGKILL ourselves at once.
		// SIGKILL is not catchable, so the sleep below is unreachable and
		// keeps the runtime aware of a live goroutine.
		_ = unix.Kill(os.Getpid(), unix.SIGKILL)
		for {
			time.Sleep(time.Hour)
		}
	}
}

// TestProgramCPULimitKillClassifiesAsTheTimeLimitOutcome drives the
// production call site (protocolProgram.run's exchange failure path) with a
// real launcher start of this binary under the real program budgets
// (programMemory, programCPUSeconds, programFileSize, programOpenFile), the
// real go-byte, and the real protocol ReadResponse — then asserts the
// classification classifyProgramError gives that death.
func TestProgramCPULimitKillClassifiesAsTheTimeLimitOutcome(t *testing.T) {
	for _, row := range []struct {
		name string
		mode string
		want string
	}{
		{
			// The positive case: killed by a signal after using the full
			// CPU budget. The time-limit outcome names the CPU limit, is a
			// program failure (routing), and never claims a protocol
			// failure or the wall-clock limit.
			name: "cpu limit kill names the cpu time limit, not protocol",
			mode: "__cpu-limit-spin",
			want: "rendering stopped: CPU time exceeded",
		},
		{
			// The discriminator: a signal death without the CPU budget must
			// not be classified as either time-limit outcome — the CPU
			// usage is what separates the two, not signal-ness alone.
			name: "signal death without the cpu budget is not a time-limit outcome",
			mode: "__cpu-limit-signal-death",
			want: "",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			self, err := os.Executable()
			require.NoError(t, err)
			proc, err := launcher.Start(launcher.Spec{
				Self:   self,
				Target: self,
				Args:   []string{row.mode},
				Env:    []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir},
				Dir:    dir,
				Limits: launcher.Limits{MemoryBytes: programMemory, CPUSeconds: programCPUSeconds, FileSizeBytes: programFileSize, OpenFiles: programOpenFile, Processes: 2},
			})
			require.NoError(t, err)
			require.NoError(t, proc.Go())

			// A loud guard against a silently unenforced limit: if the
			// kernel's CPU limit never ends the program, the exchange read
			// would block forever and a watchdog kill would look exactly
			// like the real thing. Watchdog firings fail the row instead.
			watchdogFired := make(chan struct{}, 1)
			exchangeDone := make(chan struct{})
			go func() {
				select {
				case <-time.After(300 * time.Second):
					proc.Kill("test-watchdog")
					watchdogFired <- struct{}{}
				case <-exchangeDone:
				}
			}()

			_, readErr := protocol.ReadResponse(proc.Stdout, protocol.Request{}, nil)
			require.Error(t, readErr, "a program that never speaks the protocol must fail the exchange")
			if row.mode == "__cpu-limit-spin" {
				select {
				case <-watchdogFired:
					t.Fatal("the CPU limit did not end the program within 300 s — RLIMIT_CPU was not enforced; the outcome below would be from the test watchdog, not the kernel")
				default:
				}
			}
			close(exchangeDone)

			// The production call site's exact sequence when the exchange
			// fails (protocolProgram.run): Kill("protocol"), close stdin,
			// drain stdout, Wait, then classifyProgramError(readErr, proc).
			proc.Kill("protocol")
			_ = proc.Stdin.Close()
			_, _ = io.Copy(io.Discard, proc.Stdout)
			_ = proc.Wait()
			f := classifyProgramError(readErr, proc)
			require.NotNil(t, f)
			if row.want == "" {
				require.NotContains(t, f.Message, "CPU time exceeded",
					"a signal death without the CPU budget must not be the CPU limit outcome")
				require.NotContains(t, f.Message, "time limit exceeded",
					"a signal death without the CPU budget must not be the wall limit outcome either")
				return
			}
			require.Equal(t, row.want, f.Message,
				"a CPU-limit kill (signalled, at least programCPUSeconds of CPU) is the CPU time limit outcome")
			require.Equal(t, failProgram, f.Kind,
				"a renderer limit outcome routes as a program failure, not a document failure")
			require.NotContains(t, f.Message, "protocol",
				"a CPU-limit kill must never be reported as a protocol failure (the Linux misclassification)")
			require.NotContains(t, f.Message, "time limit exceeded",
				"the CPU limit outcome is its own sentence, not the wall-clock one")
		})
	}
}
