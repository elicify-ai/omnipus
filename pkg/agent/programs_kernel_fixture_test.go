package agent

// Original-author S26/S27 prerequisite, not a fake TurnPolicyBase registration.
// Real one-way kernel Apply runs only inside a fresh, exact-leaf subprocess.
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

func programsKernelReexec(t *testing.T) bool {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Fatal("BLOCKED: real enforcing kernel prerequisite fixture currently requires Linux; not native macOS/Windows certification — S26/S27 Background")
	}
	if os.Getenv("OMNIPUS_S1_KERNEL_LEAF") == "1" {
		return false
	}
	selector := "^" + strings.ReplaceAll(regexp.QuoteMeta(t.Name()), "/", "$/^") + "$"
	cmd := exec.Command(os.Args[0], "-test.run="+selector, "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), "OMNIPUS_S1_KERNEL_LEAF=1")
	out, err := cmd.CombinedOutput()
	t.Logf("exact-leaf kernel subprocess:\n%s", out)
	require.NoError(t, err, "real enforcing-prerequisite leaf must succeed, not skip or silently degrade")
	require.Contains(t, string(out), "--- PASS: "+t.Name(), "instrument: exact child leaf actually executed")
	return true
}

// A leaf may compare two independently constructed agents/homes. The boot
// policy grants scratch /tmp in both cases; each spawn still derives its own
// per-turn exclusions from the current home's registered base. Never re-Apply
// a second backend and pretend the one-shot ratchet installed a new policy.
var programsAppliedKernel sandbox.SandboxBackend

func programsEnforceKernel(t *testing.T, f *wu5Fixture) string {
	t.Helper()
	require.Equal(t, "1", os.Getenv("OMNIPUS_S1_KERNEL_LEAF"), "Apply must stay in the disposable exact-leaf subprocess")
	canary := filepath.Join(f.home, "master.key")
	require.NoError(t, os.WriteFile(canary, []byte("s1-kernel-canary\n"), 0600))
	out, err := exec.Command("/bin/cat", canary).CombinedOutput()
	require.NoError(t, err, "instrument: same existing file is readable before per-turn confinement")
	require.Equal(t, "s1-kernel-canary\n", string(out), "instrument: canary control cannot be an absent/unreadable file")
	if programsAppliedKernel == nil {
		backend, name := sandbox.SelectBackend()
		require.True(t, strings.HasPrefix(name, "landlock"), "BLOCKED: actual enforcing Linux backend required, got %q", name)
		// The locked OS thread is deliberately NOT returned to the scheduler:
		// Landlock is irreversible. The disposable test goroutine exits with it.
		runtime.LockOSThread()
		policy := sandbox.DefaultPolicyForModel(sandbox.FilesystemModelOpen, f.home, nil, nil, nil, nil)
		require.NoError(t, backend.Apply(policy), "BLOCKED: actual boot enforcing Apply failed; registration alone cannot satisfy Background")
		require.True(t, sandbox.DescribeBackend(backend).PolicyApplied, "actual backend completed Apply")
		programsAppliedKernel = backend
	}
	f.al.GetConfig().Sandbox.FilesystemModel = string(sandbox.FilesystemModelOpen)
	f.al.SetSandboxBackend(programsAppliedKernel)
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: f.home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	require.True(t, sandbox.TurnPolicyBaseInstalled(), "base registered only after actual enforcing Apply")
	return canary
}

// Invoked INSIDE the actual command/probe child. A literal kernel diagnostic,
// joined to a positive readable-before/after control, rules out ENOENT and a
// fake policy flag. Diagnostic files live in the policy's permitted /dev/shm
// scratch; no policy allowance is widened just to make instrumentation work.
func programsKernelReadAttempt(canary, report string) string {
	return fmt.Sprintf("/bin/cat %q > /dev/null 2> %q; probe_exit=$?; [ \"$probe_exit\" -ne 0 ] || exit 85\n", canary, report)
}

func programsAssertKernelDenied(t *testing.T, canary, report string) {
	t.Helper()
	raw, err := os.ReadFile(report)
	require.NoError(t, err, "actual child must record its protected-file attempt")
	require.Contains(t, string(raw), "Permission denied", "kernel denial, not an absent file, wrong command or mocked status")
	still, err := os.ReadFile(canary)
	require.NoError(t, err, "instrument: canary still exists and is readable by gateway after child")
	require.Equal(t, "s1-kernel-canary\n", string(still))
	t.Log("actual_boot_Apply=true actual_child_kernel_read_denied=true readable_gateway_control=true")
}
