//go:build darwin || linux

package preview_test

import (
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// W2-S02 R1 / AD-4: reduction must cover every path the actual permission
// engine accepts. Every row with a directory suffix reaches that shortcut,
// including escapes that are NOT equivalent to removing a backslash.
func TestWriteExclusionConservativelyCoversRegexMatches(t *testing.T) {
	cases := []struct{ name, folder, expression string }{
		{"digit_directory_suffix", "office7", `office\d(?:/|$)`},
		{"hex_directory_suffix", "office7", `office\x37(?:/|$)`},
		{"hex_braced_directory_suffix", "office7", `office\x{37}(?:/|$)`},
		{"octal_directory_suffix", "office7", `office\067(?:/|$)`},
		{"quoted_directory_suffix", "office7", `\Qoffice7\E(?:/|$)`},
		{"quoted_punctuation", "office.7", `\Qoffice.7\E(?:/|$)`},
		{"escaped_punctuation", "office.7", `office\.7(?:/|$)`},
		{"literal_directory_suffix", "office7", `office7(?:/|$)`},
		{"quantified_last_character", "offic-sibling", `office?`},
		{"alternation", "office7", `(?:office7|other)(?:/|$)`},
		{"bare_prefix", "office7-sibling", `office7`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, base, work := t.TempDir(), t.TempDir(), t.TempDir()
			// Isolate the specified regex escape from the host's temporary
			// prefix alias; both spellings must pass the real write guard.
			base, err := filepath.EvalSymlinks(base)
			require.NoError(t, err)
			t.Setenv("OMNIPUS_HOME", home)
			root := filepath.Join(base, tc.folder)
			target := officepreview.TrustedOffice(t, home, root)
			cfg := &config.Config{}
			svc := securityWriterService(t, home, func() *config.Config { return cfg })
			require.Equal(t, "ready", svc.ComponentStatus().Office)
			pattern := "^" + regexp.QuoteMeta(base+string(filepath.Separator)) + tc.expression
			cfg.Tools.AllowWritePaths = []string{pattern}
			securityWrite(t, work, target, pattern)
			officepreview.RequireWriterRefusal(t, svc)
		})
	}
	for _, flag := range []string{"(?m)", "(?i)"} {
		t.Run(flag, func(t *testing.T) {
			home, work, root := t.TempDir(), t.TempDir(), t.TempDir()
			root, err := filepath.EvalSymlinks(root)
			require.NoError(t, err)
			t.Setenv("OMNIPUS_HOME", home)
			target := officepreview.TrustedOffice(t, home, root)
			cfg := &config.Config{}
			pattern := flag + "^" + regexp.QuoteMeta(root) + "(?:/|$)"
			cfg.Tools.AllowWritePaths = []string{pattern}
			securityWrite(t, work, target, pattern)
			officepreview.RequireWriterRefusal(t, securityWriterService(t, home, func() *config.Config { return cfg }))
		})
	}
}

// R5 is a defensive service contract: nil means unknown, never known-empty.
// This does not assert that a normal gateway reload can return nil.
func TestWriteExclusionRejectsUnknownAuthority(t *testing.T) {
	for _, source := range []string{"nil_config", "nil_callback"} {
		t.Run(source, func(t *testing.T) {
			home, work, root := t.TempDir(), t.TempDir(), t.TempDir()
			t.Setenv("OMNIPUS_HOME", home)
			target := officepreview.TrustedOffice(t, home, root)
			pattern := "^" + regexp.QuoteMeta(root) + "(?:/|$)"
			securityWrite(t, work, target, pattern)
			auth := preview.WriteAuthority{}
			if source == "nil_config" {
				auth.Config = func() *config.Config { return nil }
			}
			previous := preview.Shared()
			t.Cleanup(func() { preview.SetShared(previous) })
			svc, _, err := preview.StartGateway(home, auth)
			require.NoError(t, err)
			officepreview.RequireWriterRefusal(t, svc)
		})
	}
	t.Run("existing_source_controls", TestOfficeWriterExclusion_UnreadableAuthorityFailsClosed)
	t.Run("identity_error", securityUnknownIdentity)
}

// R5 identity error: the authority genuinely reaches the component first.
// A symlink loop then makes identity unknowable (ELOOP, not mere ENOENT).
// The retained-kernel-authority variant has its own held-child proof.
func securityUnknownIdentity(t *testing.T) {
	home, work, base := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	root := filepath.Join(base, "office")
	target := officepreview.TrustedOffice(t, home, root)
	alias := filepath.Join(base, "writer-alias")
	require.NoError(t, os.Symlink(root, alias))
	pattern := "^" + regexp.QuoteMeta(alias) + "(?:/|$)"
	securityWrite(t, work, filepath.Join(alias, filepath.Base(target)), pattern)
	require.NoError(t, os.Remove(alias))
	require.NoError(t, os.Symlink(alias, alias))
	_, err := os.Stat(alias)
	require.ErrorIs(t, err, syscall.ELOOP, "positive control: identity lookup really fails, not a nonexistent path")
	// A textual miss must not be promoted to a proof of no writer.
	require.NotNil(t, component.FirstOverlap(root, []component.WriteRoot{{Path: alias, Grant: "unreadable identity"}}), "R5: identity errors are unknown authority, not permission to execute")
}
