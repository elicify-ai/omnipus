//go:build darwin || linux

package preview

// Office review SFH-02: AD-2 requires a positively malformed frame to remain
// a protocol failure. AD-6's 60-second CPU limit must still classify a real
// CPU death whose EOF is otherwise indistinguishable from truncation.
// These tests use the real launcher, protocol reader, kill/wait sequence and
// classifier. They do not replace the launcher or change any production hook.

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

var officeReviewCPUSink byte
var officeReviewCPURounds uint64

func init() {
	if len(os.Args) == 3 && os.Args[1] == "__office-review-classification" {
		os.Exit(officeReviewClassificationChild(os.Args[2]))
	}
}

func officeReviewClassificationChild(mode string) int {
	if mode == "ordinary-exit" {
		return 7
	}
	if mode == "early-signal" {
		_ = unix.Kill(os.Getpid(), unix.SIGKILL)
		return 80 // Unexpected if SIGKILL worked; parent checks signalled status.
	}
	for {
		sum := sha256.Sum256([]byte{byte(officeReviewCPURounds)})
		officeReviewCPUSink ^= sum[0]
		officeReviewCPURounds++
		if officeReviewCPURounds%1024 != 0 {
			continue
		}
		var usage unix.Rusage
		if unix.Getrusage(unix.RUSAGE_SELF, &usage) != nil {
			return 81
		}
		used := time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
		if used < 59*time.Second+500*time.Millisecond {
			continue
		}
		// Stop spending CPU after emitting the violation. The parent must
		// kill this blocked peer, not observe an accidental ordinary exit.
		var payload []byte
		kind := byte(99) // Not one of the five types in the AD-2 table.
		if mode == "invalid-meta" {
			kind = protocol.FrameMeta
			payload = []byte(`{"protocol_version":0,"build_id":"office-review","page_count":1,"encrypted":false}`)
		} else if mode != "unknown-frame" {
			return 82
		}
		header := []byte{kind, 0, 0, 0, 0}
		binary.LittleEndian.PutUint32(header[1:], uint32(len(payload)))
		if _, err := os.Stdout.Write(append(header, payload...)); err != nil {
			return 83
		}
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 84 // The parent checks that its protocol kill, not this, ended us.
	}
}

func officeReviewClassifiedProcess(t *testing.T, mode string) (*launcher.Process, *exec.ExitError, error) {
	t.Helper()
	dir := t.TempDir()
	self, err := os.Executable()
	require.NoError(t, err)
	target, args := self, []string{"__office-review-classification", mode}
	if mode == "cpu-limit" {
		// A native peer has the OS signal disposition. The Go test runtime
		// catches SIGXCPU without exiting on macOS (os/signal docs, default
		// behavior), so a Go spin would test that runtime instead of a
		// renderer-like kernel limit death. This fixed script uses no file,
		// network, sleep or external command, and keeps stdout open until
		// the real kernel budget ends it.
		target, args = "/bin/sh", []string{"-c", "while :; do :; done"}
	}
	proc, err := launcher.Start(launcher.Spec{
		Self: self, Target: target, Args: args,
		Env: []string{"PATH=/usr/bin:/bin", "HOME=" + dir, "TMPDIR=" + dir}, Dir: dir,
		Limits: launcher.Limits{MemoryBytes: programMemory, CPUSeconds: programCPUSeconds, FileSizeBytes: programFileSize, OpenFiles: programOpenFile, Processes: 2},
	})
	require.NoError(t, err)
	t.Cleanup(func() { proc.Kill("test-cleanup"); _ = proc.Stdin.Close(); _ = proc.Wait() })
	var watchdogFired atomic.Bool
	watchdog := time.AfterFunc(300*time.Second, func() { watchdogFired.Store(true); proc.Kill("test-watchdog") })
	defer watchdog.Stop()
	require.NoError(t, proc.Go())
	_, readErr := protocol.ReadResponse(proc.Stdout, protocol.Request{}, nil)
	require.Error(t, readErr)
	// Exactly protocolProgram.run's response-error cleanup order. This
	// fixture isolates CPU accounting from the separate wall-clock watchdog.
	proc.Kill("protocol")
	_ = proc.Stdin.Close()
	_, _ = io.Copy(io.Discard, proc.Stdout)
	waitErr := proc.Wait()
	require.False(t, watchdogFired.Load(), "instrument: a test watchdog death is never CPU-limit evidence")
	require.Equal(t, "protocol", proc.KillCause())
	var exitErr *exec.ExitError
	require.ErrorAs(t, waitErr, &exitErr)
	require.NotNil(t, exitErr.ProcessState)
	t.Logf("mode=%s signalled=%t user=%s system=%s cause=%s read=%v", mode, !exitErr.Exited(), exitErr.UserTime(), exitErr.SystemTime(), proc.KillCause(), readErr)
	return proc, exitErr, readErr
}

func TestOfficePreview_CPUClassificationPreservesConcreteProtocolFailures(t *testing.T) {
	for _, row := range []struct {
		mode string
		want string
	}{
		{"unknown-frame", "PDF renderer failed (protocol)"},
		{"invalid-meta", "PDF renderer failed (protocol)"},
		{"cpu-limit", "rendering stopped: CPU time exceeded"},
		{"early-signal", "PDF renderer failed (protocol)"},
		{"ordinary-exit", "PDF renderer failed (protocol)"},
	} {
		t.Run(row.mode, func(t *testing.T) {
			proc, exitErr, readErr := officeReviewClassifiedProcess(t, row.mode)
			used := exitErr.UserTime() + exitErr.SystemTime()
			require.True(t, protocol.IsProtocolError(readErr), "instrument: the actual protocol reader must reject this response")
			if row.mode == "ordinary-exit" {
				require.True(t, exitErr.Exited())
				require.Equal(t, 7, exitErr.ExitCode())
			} else {
				require.False(t, exitErr.Exited(), "instrument: this case requires a real signal death")
			}
			switch row.mode {
			case "unknown-frame", "invalid-meta":
				require.GreaterOrEqual(t, used, 59*time.Second+500*time.Millisecond)
				require.Less(t, used, 60*time.Second, "instrument: malformed peer was killed below the actual CPU budget")
				if row.mode == "unknown-frame" {
					require.ErrorContains(t, readErr, "unknown frame type 99")
				} else {
					require.ErrorContains(t, readErr, "META protocol_version 0")
				}
			case "cpu-limit":
				require.GreaterOrEqual(t, used, 59*time.Second, "instrument: genuine kernel CPU kill, not an early crash")
				require.ErrorContains(t, readErr, "stream ended before DONE", "original EOF case must keep the CPU fix")
			case "early-signal", "ordinary-exit":
				require.Less(t, used, time.Second, "instrument: negative control did not spend the CPU budget")
			}
			f := classifyProgramError(readErr, proc)
			require.NotNil(t, f)
			require.Equal(t, failProgram, f.Kind)
			require.Equal(t, row.want, f.Message, "SFH-02: concrete malformed output is not CPU exhaustion; genuine CPU EOF still is")
		})
	}
}

// Exact boundary inputs for the 6270e43ae accounting correction. A real
// signalled child supplies ProcessState; only the exported OS-usage value
// is set to controlled observations, and restored. This is a classifier
// unit test, NOT a claim that each synthetic duration was spent on CPU.
// The real full-budget/near-budget processes are tested separately above.
func TestOfficePreview_CPUAccountingBoundaryInputs(t *testing.T) {
	_, signalled, _ := officeReviewClassifiedProcess(t, "early-signal")
	require.False(t, signalled.Exited())
	usage, ok := signalled.SysUsage().(*syscall.Rusage)
	require.True(t, ok, "instrument: public Unix usage record is required; never edit launcher internals")
	original := *usage
	defer func() { *usage = original }()
	for _, row := range []struct {
		name string
		user time.Duration
		sys  time.Duration
		want bool
	}{
		{"zero", 0, 0, false},
		{"below-allowance", 58*time.Second + 999999*time.Microsecond, 0, false},
		{"allowance-edge", 59 * time.Second, 0, true},
		{"measured-short-59.960", 59*time.Second + 960*time.Millisecond, 0, true},
		{"measured-short-59.985", 59*time.Second + 985*time.Millisecond, 0, true},
		{"user-plus-system", 30 * time.Second, 29*time.Second + 970*time.Millisecond, true},
		{"just-below-budget", 60*time.Second - time.Microsecond, 0, true},
		{"exact-budget", 60 * time.Second, 0, true},
		{"above-budget", 60*time.Second + time.Microsecond, 0, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			usage.Utime = syscall.NsecToTimeval(int64(row.user))
			usage.Stime = syscall.NsecToTimeval(int64(row.sys))
			require.Equal(t, row.user+row.sys, signalled.UserTime()+signalled.SystemTime(), "instrument: classifier sees the exact public accounting input")
			require.Equal(t, row.want, cpuLimitKilled(signalled))
			require.Equal(t, row.want, cpuLimitKilled(fmt.Errorf("wait wrapper: %w", signalled)))
		})
	}
	_, ordinary, _ := officeReviewClassifiedProcess(t, "ordinary-exit")
	require.True(t, ordinary.Exited())
	ordinaryUsage, ok := ordinary.SysUsage().(*syscall.Rusage)
	require.True(t, ok)
	ordinaryUsage.Utime = syscall.NsecToTimeval(int64(60 * time.Second))
	ordinaryUsage.Stime = syscall.NsecToTimeval(0)
	require.Equal(t, 60*time.Second, ordinary.UserTime()+ordinary.SystemTime())
	require.False(t, cpuLimitKilled(ordinary), "ordinary nonzero exit is not a CPU-limit signal, even at the budget")
	require.False(t, cpuLimitKilled(nil))
	require.False(t, cpuLimitKilled(errors.New("ordinary wait error")))
	require.False(t, cpuLimitKilled(&exec.ExitError{}), "missing process state cannot prove a CPU kill")
}
