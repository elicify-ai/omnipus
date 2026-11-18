//go:build linux

package preview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// T-118: only the external converter is fake. officeConversion.run, its
// actual captured policy, launcher, confinement hooks and kernel are real.
func TestPreviewOffice_T118_CapturedVariantCPolicyAndOperations(t *testing.T) {
	functionalPinDataOutsideTmp(t)
	if !primeKernelSandbox(t) {
		return
	}
	home := t.TempDir()
	conv, call := fakeOfficeCall(t, home, "valid")
	defer call.finish()
	starts := 0
	testHookConverterStart = func(start converterStart) {
		starts++
		functionalAssertVariantCPolicy(t, start.policy, conv.root, filepath.Dir(conv.self), call.dir)
		functionalVariantCOperations(t, start, call.dir)
	}
	t.Cleanup(func() { testHookConverterStart = nil })
	_, unconfined, err := conv.run(context.Background(), []byte("T-118 process-edge fixture"), ".docx")
	require.NoError(t, err)
	require.Empty(t, unconfined)
	require.Equal(t, 1, starts, "instrument: capture the real start, not a hand-built policy")
	raw, err := os.ReadFile(filepath.Join(call.dir, "receipt.json"))
	require.NoError(t, err)
	var receipt fakeOfficeReceipt
	require.NoError(t, json.Unmarshal(raw, &receipt))
	for _, entry := range receipt.Env {
		assert.False(t, strings.HasPrefix(entry, "OSL_SOCKET_PATH="), "T-118: Linux environment must not receive deferred private-folder behavior")
	}
	require.ElementsMatch(t, []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8", "HOME=" + filepath.Join(call.dir, "home"), "TMPDIR=" + filepath.Join(call.dir, "tmp")}, receipt.Env, "T-118 / MV-2: no environment additions")
}

func functionalAssertVariantCPolicy(t *testing.T, policy sandbox.SandboxPolicy, office, launcher, work string) {
	t.Helper()
	var narrow uint64
	count := 0
	for _, rule := range policy.FilesystemRules {
		if rule.Path == "/tmp" {
			count++
			narrow = rule.Access
		}
	}
	assert.Equal(t, 1, count, "T-118: exactly one /tmp rule must carry the new narrow access")
	assert.NotZero(t, narrow, "T-118: missing make-socket/remove-file access — required by FR-061")
	assert.Zero(t, narrow&(sandbox.AccessRead|sandbox.AccessWrite|sandbox.AccessExecute), "T-118: /tmp must not gain read, broad write or execute")
	assert.Zero(t, narrow&(narrow-1), "T-118: one new abstract access value, not a combination")
	// The fixed system paths and access classes below are from spec §3.4
	// D8c + §3.6, NOT linuxSystemReadExec or officeFontFolders.
	wanted := []sandbox.PathRule{
		{Path: office, Access: sandbox.AccessRead | sandbox.AccessExecute},
		{Path: launcher, Access: sandbox.AccessRead | sandbox.AccessExecute},
		{Path: work, Access: sandbox.AccessRead | sandbox.AccessWrite},
		{Path: "/tmp", Access: narrow},
		{Path: "/dev/urandom", Access: sandbox.AccessRead},
		{Path: "/etc/passwd", Access: sandbox.AccessRead},
		{Path: "/etc/nsswitch.conf", Access: sandbox.AccessRead},
	}
	for _, path := range []string{"/lib", "/lib64", "/usr/lib", "/usr/lib64", "/etc/ld.so.cache"} {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue // D8c explicitly omits absent system folders; the test is NOT skipped.
		} else {
			require.NoError(t, err)
		}
		wanted = append(wanted, sandbox.PathRule{Path: path, Access: sandbox.AccessRead | sandbox.AccessExecute})
	}
	for _, path := range []string{"/usr/share/fonts", "/usr/local/share/fonts", "/etc/fonts"} {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else {
			require.NoError(t, err)
		}
		wanted = append(wanted, sandbox.PathRule{Path: path, Access: sandbox.AccessRead})
	}
	assert.ElementsMatch(t, wanted, policy.FilesystemRules, "T-118: full exact D8c + variant-C file list; no /var/tmp or other additions")
	assert.False(t, policy.ReadsOpen)
	assert.False(t, policy.ExecOpen)
	assert.False(t, policy.NetworkUnhandled)
	assert.True(t, policy.InheritToChildren)
	assert.Empty(t, policy.DeniedPaths)
	assert.Empty(t, policy.DeniedNodes)
	assert.Empty(t, policy.DeniedPathPrefixes)
	assert.Empty(t, policy.WriteDeniedPaths)
	assert.Empty(t, policy.AllowedBranches)
	assert.Empty(t, policy.UnixSocketRules, "T-118: Linux has no private socket-folder rules")
	assert.Equal(t, []sandbox.NetPortRule{{Port: 0}}, policy.BindPortRules, "T-118: existing port-0 rule is unchanged")
	assert.Equal(t, []sandbox.NetPortRule{{Port: 0}}, policy.ConnectPortRules, "T-118: existing port-0 rule is unchanged")
}

func functionalVariantCOperations(t *testing.T, start converterStart, work string) {
	t.Helper()
	// These are deliberately planted PROBE TARGETS beneath /tmp, not a data
	// home/owner record. All homes/work/control executables stay outside /tmp.
	targets, err := os.MkdirTemp("/tmp", "ofc-")
	require.NoError(t, err)
	defer func() { require.NoError(t, os.RemoveAll(targets)) }()
	planted := filepath.Join(targets, "planted")
	require.NoError(t, os.WriteFile(planted, []byte("independent planted text"), 0600))
	cases := []struct {
		operation string
		path      string
		allowed   bool
	}{
		{"socket", filepath.Join(targets, "socket"), true},
		{"file", filepath.Join(targets, "regular"), false},
		{"directory", filepath.Join(targets, "directory"), false},
		{"symlink", filepath.Join(targets, "symlink"), false},
		{"link", filepath.Join(targets, "link"), false},
		{"read", planted, false},
		{"list", targets, false},
		{"list", "/tmp", false},
		{"remove", filepath.Join(targets, "removable"), true},
	}
	for index, tc := range cases {
		t.Run(tc.operation+"-"+string(rune('a'+index)), func(t *testing.T) {
			if tc.operation == "link" {
				require.NoError(t, os.WriteFile(tc.path+"-source", []byte("hard-link source"), 0600))
			}
			if tc.operation == "remove" {
				require.NoError(t, os.WriteFile(tc.path, []byte("owned removable target"), 0600))
			}
			outside := functionalRunProbe(t, nil, tc.operation, tc.path, work)
			require.Equal(t, "ok", outside.Errno, "instrument: SAME target operation succeeds outside captured sandbox")
			switch tc.operation {
			case "file", "directory", "symlink", "link":
				require.NoError(t, os.Remove(tc.path), "reset only own positive-control entry before same creation")
			case "read":
				require.Equal(t, "independent planted text", outside.Text)
			case "remove":
				require.NoError(t, os.WriteFile(tc.path, []byte("owned removable target"), 0600))
			}
			inside := functionalRunProbe(t, &start.hooks, tc.operation, tc.path, work)
			if tc.allowed {
				assert.Equal(t, "ok", inside.Errno, "T-118: exactly granted /tmp operation must succeed through ACTUAL captured hooks")
			} else {
				assert.Contains(t, []string{"EACCES", "EPERM"}, inside.Errno, "T-118: kernel refusal, not a missing/unreadable target")
			}
		})
	}
}
