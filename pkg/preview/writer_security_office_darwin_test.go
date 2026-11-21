//go:build darwin

package preview_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

func TestOfficeWriterExclusion_DarwinCaseAndUnicodeIdentity(t *testing.T) {
	for _, name := range []string{"case", "NFC_to_NFD", "NFD_to_NFC"} {
		t.Run(name, func(t *testing.T) {
			home, work, base := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			rootName, aliasName := "OfficeAlias", "officealias"
			switch name {
			case "NFC_to_NFD":
				rootName, aliasName = "Café", "Café"
			case "NFD_to_NFC":
				rootName, aliasName = "Café", "Café"
			}
			root, alias := filepath.Join(base, rootName), filepath.Join(base, aliasName)
			target := officepreview.TrustedOffice(t, home, root)
			original, err := os.Stat(root)
			require.NoError(t, err)
			aliased, err := os.Stat(alias)
			require.NoError(t, err, "BLOCKED: host must demonstrate the requested filesystem alias; never silently skip")
			require.NotEqual(t, root, alias)
			require.True(t, os.SameFile(original, aliased), "positive control: spellings must name the same inode")
			cfg := &config.Config{}
			svc := securityWriterService(t, home, func() *config.Config { return cfg })
			require.Equal(t, "ready", svc.ComponentStatus().Office)
			pattern := "^" + regexp.QuoteMeta(alias) + "(?:/|$)"
			cfg.Tools.AllowWritePaths = []string{pattern}
			securityWrite(t, work, filepath.Join(alias, filepath.Base(target)), pattern)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "authority really wrote", string(data), "the original component dependency, not a lookalike, changed")
			officepreview.RequireWriterRefusal(t, svc)
		})
	}
}

func securitySeatbeltWrite(t *testing.T, policy sandbox.SandboxPolicy, path string) ([]byte, error) {
	t.Helper()
	backend := sandbox.NewSeatbeltBackend()
	require.True(t, backend.Available(), "BLOCKED: real Seatbelt is required for the AllowedPaths write proof")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", `printf 'kernel authority wrote' > "$1"`, "office-write-control", path)
	require.NoError(t, backend.ApplyToCmd(cmd, policy))
	return cmd.CombinedOutput()
}

// AllowedPaths is a kernel authority, not a write_file allowance. Use the
// actual derivation and Seatbelt spawn with AND WITHOUT the entry. The
// component lives outside TMPDIR; otherwise the universal scratch grant
// would make an unrelated write look like proof of AllowedPaths.
func TestOfficeWriterExclusion_DarwinSandboxAllowedPaths(t *testing.T) {
	for _, form := range []string{"absolute", "home_shorthand"} {
		t.Run(form, func(t *testing.T) {
			fixtureRoot := os.Getenv("OMNIPUS_TEST_SECURITY_ROOT")
			if fixtureRoot == "" {
				t.Fatal("BLOCKED: OMNIPUS_TEST_SECURITY_ROOT outside TMPDIR is required for the isolated real-kernel write control")
			}
			require.NoError(t, os.MkdirAll(fixtureRoot, 0700))
			base, err := os.MkdirTemp(fixtureRoot, "allowed-paths-")
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, os.RemoveAll(base)) })
			home, work := t.TempDir(), t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			t.Setenv("HOME", base)
			root := filepath.Join(base, "component")
			target := officepreview.TrustedOffice(t, home, root)
			entry := root
			if form == "home_shorthand" {
				entry = "~/component"
			}
			authored := fspolicy.FSPolicy{WorkDir: work}
			input := sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen}
			denied := sandbox.DeriveKernelPolicy(authored, input)
			output, writeErr := securitySeatbeltWrite(t, denied, target)
			require.Error(t, writeErr, "negative control: no authority means write denied: %s", output)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "original resource", string(data))
			input.AllowedPaths = []string{entry}
			allowed := sandbox.DeriveKernelPolicy(authored, input)
			output, writeErr = securitySeatbeltWrite(t, allowed, target)
			require.NoError(t, writeErr, "positive control: real configured authority must enable the write: %s", output)
			data, err = os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, "kernel authority wrote", string(data))
			cfg := &config.Config{}
			cfg.Sandbox.AllowedPaths = []string{entry}
			svc := securityWriterService(t, home, func() *config.Config { return cfg })
			officepreview.RequireWriterRefusal(t, svc)
		})
	}
}
