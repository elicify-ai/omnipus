//go:build linux

package preview

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/assert"
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

// Q1 B: the same real confined boot must also read a file under
// /usr/share/fonts and list that directory. The decision names the
// directory; the file is whichever regular font file is there.
func TestPreviewOffice_Q1B_ConfinedBootFontReadAndList(t *testing.T) {
	functionalPinDataOutsideTmp(t)
	const marker = "OMNIPUS_TEST_OFFICE_Q1B_FONT_CHILD"
	if os.Getenv(marker) != t.Name() {
		self, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
		cmd.Env = append(os.Environ(), marker+"="+t.Name())
		output, runErr := cmd.CombinedOutput()
		t.Logf("isolated actual boot-policy font probe:\n%s", output)
		require.NoError(t, runErr, "Q1 B: font read and directory list under the actual confined boot must be visible")
		require.Contains(t, string(output), "--- PASS: "+t.Name()+" (", "instrument: exact child test must run")
		return
	}
	home := t.TempDir()
	font := q1bBootFontFile(t)
	outsideRead := functionalRunProbe(t, nil, "read", font, home)
	require.Equal(t, "ok", outsideRead.Errno, "Q1 B instrument: the same font file is readable before the boot domain")
	require.NotEmpty(t, outsideRead.Text)
	outsideList := functionalRunProbe(t, nil, "list", "/usr/share/fonts", home)
	require.Equal(t, "ok", outsideList.Errno, "Q1 B instrument: /usr/share/fonts lists before the boot domain")
	backend, ok := sandbox.NewLinuxBackend()
	require.True(t, ok, "BLOCKED: enforcing Linux Landlock unavailable — required by founder Q1 B")
	self, err := os.Executable()
	require.NoError(t, err)
	policy := sandbox.DefaultPolicyForModel(sandbox.FilesystemModelConfined, home, nil, []string{filepath.Dir(self)}, nil, nil)
	require.NoError(t, backend.Apply(policy))
	require.True(t, backend.EnforcingChildren(), "Q1 B: no unconfined acceptance")
	hooks := spawnHooks{start: func(cmd *exec.Cmd) (func(), error) {
		return sandbox.StartLockedWithPolicyHeld(cmd, &policy)
	}}
	insideRead := functionalRunProbe(t, &hooks, "read", font, home)
	t.Logf("Q1 B: font %s confined boot read=%s", font, insideRead.Errno)
	assert.Equal(t, "ok", insideRead.Errno, "Q1 B: a font file under /usr/share/fonts must be readable under the actual confined gateway boot; converter-only grants cannot overcome an inherited denial")
	if insideRead.Errno == "ok" {
		assert.Equal(t, outsideRead.Text, insideRead.Text, "Q1 B: read the real font file, not a substitute")
	}
	insideList := functionalRunProbe(t, &hooks, "list", "/usr/share/fonts", home)
	t.Logf("Q1 B: /usr/share/fonts confined boot list=%s", insideList.Errno)
	assert.Equal(t, "ok", insideList.Errno, "Q1 B: /usr/share/fonts must be listable (read-dir) under the actual confined gateway boot")
}

func q1bBootFontFile(t *testing.T) string {
	t.Helper()
	var found string
	var walk func(string)
	walk = func(dir string) {
		if found != "" {
			return
		}
		listed, readErr := os.ReadDir(dir)
		require.NoError(t, readErr, "BLOCKED: %s is not listable before confinement — required by founder Q1 B", dir)
		for _, entry := range listed {
			path := filepath.Join(dir, entry.Name())
			if entry.IsDir() {
				walk(path)
				continue
			}
			if entry.Type().IsRegular() {
				found = path
				return
			}
		}
	}
	walk("/usr/share/fonts")
	require.NotEmpty(t, found, "BLOCKED: /usr/share/fonts has no regular font file — required by founder Q1 B")
	return found
}
