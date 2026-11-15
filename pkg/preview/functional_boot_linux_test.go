//go:build linux

package preview

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// T-114 / §3.6 O3: unlike primeKernelSandbox's deliberately broad
// converter-only bootstrap, use the REAL confined gateway boot policy.
// A denied account-list read is a visible failure, never a skipped run or a
// claim that the converter-only test proves production boot readiness.
func TestPreviewOffice_T114_ConfinedGatewayBootAccountLookup(t *testing.T) {
	functionalPinDataOutsideTmp(t)
	const marker = "OMNIPUS_TEST_OFFICE_REAL_BOOT_CHILD"
	if os.Getenv(marker) != t.Name() {
		self, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
		cmd.Env = append(os.Environ(), marker+"="+t.Name())
		output, runErr := cmd.CombinedOutput()
		t.Logf("isolated actual boot-policy lookup:\n%s", output)
		require.NoError(t, runErr, "T-114/O3: account lookup under actual boot must be visible; broad test bootstrap cannot settle it")
		require.Contains(t, string(output), "--- PASS: "+t.Name()+" (", "instrument: exact child test must run")
		return
	}
	home := t.TempDir()
	outside := functionalRunProbe(t, nil, "read", "/etc/passwd", home)
	require.Equal(t, "ok", outside.Errno, "O3 instrument: SAME account list is readable before the boot domain")
	require.NotEmpty(t, outside.Text)
	backend, ok := sandbox.NewLinuxBackend()
	require.True(t, ok, "BLOCKED: enforcing Linux Landlock unavailable — required by T-114/O3")
	self, err := os.Executable()
	require.NoError(t, err)
	// The test helper's executable folder is an operator exec-path input,
	// not an added system-file grant. Otherwise the probe cannot even start.
	policy := sandbox.DefaultPolicyForModel(sandbox.FilesystemModelConfined, home, nil, []string{filepath.Dir(self)}, nil, nil)
	require.NoError(t, backend.Apply(policy))
	require.True(t, backend.EnforcingChildren(), "T-114/O3: no unconfined acceptance")
	hooks := spawnHooks{start: func(cmd *exec.Cmd) (func(), error) {
		return sandbox.StartLockedWithPolicyHeld(cmd, &policy)
	}}
	inside := functionalRunProbe(t, &hooks, "read", "/etc/passwd", home)
	t.Logf("T-114/O3: uid=%d; actual confined boot account-list read=%s", os.Getuid(), inside.Errno)
	require.Equal(t, "ok", inside.Errno, "T-114/O3: /etc/passwd must be readable under actual confined gateway boot for numeric-UID socket naming; converter-only grants cannot overcome an inherited denial")
	require.Equal(t, outside.Text, inside.Text, "O3: read the real account list, not a substitute")
}
