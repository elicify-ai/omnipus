//go:build darwin || linux

package preview_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

func securityWriterService(t *testing.T, home string, current func() *config.Config) *preview.Service {
	t.Helper()
	resolver, err := component.NewResolver(home)
	require.NoError(t, err)
	svc, err := preview.New(preview.Config{Home: home, Self: resolver.Executable, Resolver: resolver, WriteRoots: func() []component.WriteRoot { return officepreview.WriterRoots(home, current()) }})
	require.NoError(t, err)
	return svc
}

func securityWrite(t *testing.T, work, target string, patterns ...string) {
	t.Helper()
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		compiled = append(compiled, regexp.MustCompile(pattern))
	}
	ctx := tools.WithAgentID(context.Background(), "other-writer")
	result := tools.NewWriteFileTool(work, true, compiled).Execute(ctx, map[string]any{"path": target, "content": "authority really wrote", "overwrite": true})
	require.False(t, result.IsError, "positive control must use the REAL file tool: %s", result.ForLLM)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "authority really wrote", string(data))
}

// W2-S02 / AD-4: every pattern is an actual file-tool authority. Plain
// prefix and quantified-last-character cases cover a sibling name, not just
// descendants of the unquantified text (the security-critical distinction).
func TestOfficeWriterExclusion_ActualRegexAuthorities(t *testing.T) {
	for _, name := range []string{"folder_form", "plain_prefix", "quantified_last_character", "alternation", "case_insensitive", "unanchored", "symlink"} {
		t.Run(name, func(t *testing.T) {
			home, base, work := t.TempDir(), t.TempDir(), t.TempDir()
			// These rows test regex semantics, not the host temporary alias.
			// The real write guard vets lexical AND resolved paths; construct
			// this fixture under its physical parent so both are authorized.
			base, err := filepath.EvalSymlinks(base)
			require.NoError(t, err)
			t.Setenv("OMNIPUS_HOME", home)
			root := filepath.Join(base, "office")
			switch name {
			case "plain_prefix":
				root = filepath.Join(base, "office-sibling")
			case "quantified_last_character":
				root = filepath.Join(base, "offic-sibling")
			}
			target := officepreview.TrustedOffice(t, home, root)
			cfg := &config.Config{}
			svc := securityWriterService(t, home, func() *config.Config { return cfg })
			require.Equal(t, "ready", svc.ComponentStatus().Office, "positive control: fixture is trusted before authority is granted")
			literal := regexp.QuoteMeta(root)
			pattern := "^" + literal + "(?:/|$)"
			switch name {
			case "plain_prefix":
				pattern = "^" + regexp.QuoteMeta(filepath.Join(base, "office"))
			case "quantified_last_character":
				// The trailing e is optional; office-sibling is writable.
				pattern = "^" + regexp.QuoteMeta(filepath.Join(base, "office")) + "?"
			case "alternation":
				pattern = "^(?:" + literal + "|" + regexp.QuoteMeta(filepath.Join(base, "other")) + ")(?:/|$)"
			case "case_insensitive":
				pattern = "(?i)^" + literal + "(?:/|$)"
			case "unanchored":
				pattern = literal
			case "symlink":
				alias := filepath.Join(base, "linked-office")
				require.NoError(t, os.Symlink(root, alias))
				pattern = "^" + regexp.QuoteMeta(alias) + "(?:/|$)"
				target = filepath.Join(alias, filepath.Base(target))
			}
			cfg.Tools.AllowWritePaths = []string{pattern}
			securityWrite(t, work, target, pattern)
			officepreview.RequireWriterRefusal(t, svc)
		})
	}
}

// The writer was active before its source became unreadable. A failure to
// enumerate that source is UNKNOWN authority, not evidence of no writers.
func TestOfficeWriterExclusion_UnreadableAuthorityFailsClosed(t *testing.T) {
	for _, source := range []string{"mount_list", "configuration"} {
		t.Run(source, func(t *testing.T) {
			home, work := t.TempDir(), t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			root := filepath.Join(t.TempDir(), "office")
			target := officepreview.TrustedOffice(t, home, root)
			cfg := &config.Config{}
			current := cfg
			svc := securityWriterService(t, home, func() *config.Config { return current })
			if source == "mount_list" {
				mountDir := workspace.MountStoreDir(home)
				require.NoError(t, os.MkdirAll(mountDir, 0700))
				mountPath, err := workspace.MountStorePath(home, "other-workspace")
				require.NoError(t, err)
				// Protected operator-store fixture; the REAL reader must load
				// it and the REAL file tool must use the resulting mount.
				record := map[string]any{"workspace_id": "other-workspace", "mounts": []workspace.Mount{{Name: "office", HostPath: root}}}
				raw, err := json.Marshal(record)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(mountPath, raw, 0600))
				roots := workspace.AllowedMountRoots(home, "other-workspace")
				require.Equal(t, []string{root}, roots)
				ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "other-writer"), "other-workspace")
				result := tools.NewWriteFileTool(work, true).Execute(ctx, map[string]any{"path": target, "content": "mount authority wrote", "overwrite": true})
				require.False(t, result.IsError, "real mount authority must permit the write: %s", result.ForLLM)
				data, err := os.ReadFile(target)
				require.NoError(t, err)
				require.Equal(t, "mount authority wrote", string(data))
				// ENOTDIR is deterministic even for a privileged test user.
				require.NoError(t, os.Rename(mountDir, mountDir+"-unavailable"))
				require.NoError(t, os.WriteFile(mountDir, []byte("unreadable as a directory"), 0600))
				_, err = os.ReadDir(mountDir)
				require.Error(t, err, "positive control: mount list really cannot be read")
			} else {
				pattern := "^" + regexp.QuoteMeta(root) + "(?:/|$)"
				cfg.Tools.AllowWritePaths = []string{pattern}
				securityWrite(t, work, target, pattern)
				current = nil // config source cannot supply the current configuration
			}
			officepreview.RequireWriterRefusal(t, svc)
		})
	}
}
