//go:build linux

package launcher_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/preview/launcher"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func init() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case launcher.ModeArg:
		os.Exit(launcher.Main(os.Args[2:]))
	case "__office-go-allocation-bomb":
		fmt.Println("GO_TARGET_READY")
		var held [][]byte
		for {
			block := make([]byte, 8<<20)
			for i := 0; i < len(block); i += 4096 {
				block[i] = 1
			}
			held = append(held, block)
			fmt.Printf("GO_ALLOCATED=%d\n", len(held)*(8<<20))
			if len(held) > 256 {
				os.Exit(83) // test guard: 2 GiB exceeded the specified data budget
			}
		}
	}
}

type officeLimits struct {
	DataSoft  uint64 `json:"data_soft"`
	DataHard  uint64 `json:"data_hard"`
	StackSoft uint64 `json:"stack_soft"`
	StackHard uint64 `json:"stack_hard"`
	ASSoft    uint64 `json:"as_soft"`
	ASHard    uint64 `json:"as_hard"`
	Death     int    `json:"parent_death"`
}

func officeProcLimits(pid int) (map[string][2]uint64, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/limits", pid))
	if err != nil {
		return nil, err
	}
	result := map[string][2]uint64{}
	for _, line := range strings.Split(string(raw), "\n") {
		for _, name := range []string{"Max data size", "Max stack size", "Max address space"} {
			if !strings.HasPrefix(line, name) {
				continue
			}
			values := strings.Fields(strings.TrimPrefix(line, name))
			if len(values) < 2 {
				return nil, fmt.Errorf("unreadable limits row %q", line)
			}
			var pair [2]uint64
			for i := range pair {
				if values[i] == "unlimited" {
					pair[i] = ^uint64(0)
				} else {
					pair[i], err = strconv.ParseUint(values[i], 10, 64)
					if err != nil {
						return nil, err
					}
				}
			}
			result[name] = pair
		}
	}
	if len(result) != 3 {
		return nil, fmt.Errorf("expected three named /proc limits, found %v", result)
	}
	return result, nil
}

func officeNativeLimitProbe(t *testing.T) string {
	t.Helper()
	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Fatal("BLOCKED: native C compiler unavailable — required by office-preview T-76 malloc/mmap/stack probes")
	}
	source, err := filepath.Abs("../../../tests/officepreview/native/limit_probe.c")
	require.NoError(t, err)
	target := filepath.Join(t.TempDir(), "limit-probe")
	output, err := exec.Command(cc, "-O0", "-fno-optimize-sibling-calls", source, "-o", target).CombinedOutput()
	require.NoError(t, err, "compile the test-only instrument: %s", output)
	return target
}

func officeStartLimitProbe(t *testing.T, target string, args []string, budget uint64, inheritedStack uint64) (*launcher.Process, *bytes.Buffer) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	output := &bytes.Buffer{}
	spec := launcher.Spec{Self: self, Target: target, Args: args, Limits: launcher.Limits{MemoryBytes: budget}, Stdout: output}
	if inheritedStack != 0 {
		prlimit, lookupErr := exec.LookPath("prlimit")
		if lookupErr != nil {
			t.Fatal("BLOCKED: prlimit unavailable for inherited-stack instrument — required by office-preview T-76")
		}
		spec.Prepare = func(cmd *exec.Cmd) error {
			cmd.Path = prlimit
			cmd.Args = append([]string{prlimit, fmt.Sprintf("--stack=%d:%d", inheritedStack, inheritedStack), "--"}, cmd.Args...)
			return nil
		}
	}
	proc, err := launcher.Start(spec)
	require.NoError(t, err)
	go func() { _ = proc.Wait() }()
	t.Cleanup(func() { proc.Kill("test cleanup"); _ = proc.Stdin.Close(); _ = proc.Wait() })
	return proc, output
}

func officeWaitLimitProbe(t *testing.T, proc *launcher.Process) error {
	t.Helper()
	select {
	case <-proc.Done():
		return proc.Wait()
	case <-time.After(15 * time.Second):
		proc.Kill("test wall deadline")
		_ = proc.Wait()
		t.Fatal("T-76: target reached the test wall deadline instead of its data/stack budget")
		return nil
	}
}

func TestRunLimitedLinuxDataStackAndAddressSpace(t *testing.T) {
	probe := officeNativeLimitProbe(t)
	inherited, err := officeProcLimits(os.Getpid())
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		budget uint64
		stack  uint64
	}{{"renderer", 512 << 20, 0}, {"extract_text", 512 << 20, 0}, {"office", 1 << 30, 0}, {"inherited_4MiB", 512 << 20, 4 << 20}} {
		t.Run(tc.name, func(t *testing.T) {
			proc, output := officeStartLimitProbe(t, probe, []string{"limits"}, tc.budget, tc.stack)
			require.NoError(t, proc.Go())
			require.NoError(t, officeWaitLimitProbe(t, proc), "target stderr: %s", proc.Stderr())
			var got officeLimits
			require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &got))
			require.Equal(t, tc.budget, got.DataSoft, "T-76: actual target data limit")
			require.Equal(t, tc.budget, got.DataHard)
			inheritedSoft := inherited["Max stack size"][0]
			if tc.stack != 0 {
				inheritedSoft = tc.stack // prlimit deliberately supplies this fixture's inheritance
			}
			wantStack := min(uint64(8<<20), inheritedSoft)
			require.Equal(t, wantStack, got.StackSoft)
			require.Equal(t, wantStack, got.StackHard)
			require.Equal(t, inherited["Max address space"], [2]uint64{got.ASSoft, got.ASHard}, "T-76: launcher must leave RLIMIT_AS inherited")
			require.Equal(t, int(unix.SIGKILL), got.Death, "parent-death signal survives execve")
		})
	}
}

func TestRunLimitedLinuxAllocationAndStackBombs(t *testing.T) {
	probe := officeNativeLimitProbe(t)
	for _, mode := range []string{"malloc", "mmap", "stack", "go"} {
		t.Run(mode, func(t *testing.T) {
			target, args := probe, []string{mode}
			if mode == "go" {
				var executableErr error
				target, executableErr = os.Executable()
				require.NoError(t, executableErr)
				args = []string{"__office-go-allocation-bomb"}
			}
			proc, output := officeStartLimitProbe(t, target, args, 512<<20, 0)
			require.NoError(t, proc.Go())
			waitErr := officeWaitLimitProbe(t, proc)
			require.Error(t, waitErr)
			require.NotEqual(t, "test wall deadline", proc.KillCause())
			if mode == "go" {
				require.Contains(t, output.String(), "GO_TARGET_READY", "target, not launcher, must reach the allocation loop")
				require.Contains(t, proc.Stderr(), "out of memory")
				var allocated uint64
				for _, line := range strings.Split(output.String(), "\n") {
					if strings.HasPrefix(line, "GO_ALLOCATED=") {
						value, parseErr := strconv.ParseUint(strings.TrimPrefix(line, "GO_ALLOCATED="), 10, 64)
						require.NoError(t, parseErr)
						allocated = value
					}
				}
				require.Positive(t, allocated)
				require.LessOrEqual(t, allocated, uint64(512<<20))
				return
			}
			first, rest, found := strings.Cut(output.String(), "\n")
			require.True(t, found, "C target must execute before failing")
			var limits officeLimits
			require.NoError(t, json.Unmarshal([]byte(first), &limits))
			require.Equal(t, uint64(512<<20), limits.DataSoft)
			require.Equal(t, limits.DataSoft, limits.DataHard)
			if mode == "stack" {
				var exit *exec.ExitError
				require.ErrorAs(t, waitErr, &exit)
				status, ok := exit.Sys().(unix.WaitStatus)
				require.True(t, ok)
				require.Equal(t, unix.SIGSEGV, status.Signal())
				require.LessOrEqual(t, limits.StackSoft, uint64(8<<20))
				return
			}
			var allocated uint64
			var errno int
			_, scanErr := fmt.Sscanf(strings.TrimSpace(rest), "ALLOCATED=%d ERRNO=%d", &allocated, &errno)
			require.NoError(t, scanErr)
			require.Equal(t, 42, proc.ExitCode(), "native ENOMEM, not a launcher failure or wall kill")
			require.Equal(t, int(unix.ENOMEM), errno)
			require.Positive(t, allocated)
			require.LessOrEqual(t, allocated, uint64(512<<20))
		})
	}
}
