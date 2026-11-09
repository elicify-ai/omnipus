//go:build windows

package gateway

// r17 D6.19: these are Windows integration cases, not Linux simulations.
// Execution evidence must come from the Windows CI leg, not a Linux PASS.
import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestThirdPartySettings_D6_WindowsPaths(t *testing.T) {
	// Founder C1 A / spec §3.2: retain D6's reason template, with only the
	// three admitted extensions. Both spellings of the former .COM allow fail.
	const extensionReason = "it must end in .exe, .cmd or .bat"
	for _, tc := range []struct{ name, check, reason string }{
		{"program.txt", "windows_extension", extensionReason},
		{"program.com", "windows_extension", extensionReason},
		{"program.COM", "windows_extension", extensionReason},
		{"PROGRA~1.exe", "windows_short_name", "short (8.3) names are not accepted; use the full name"},
		{"program.exe:alternate", "windows_stream", "alternate data streams are not accepted"},
	} {
		t.Run(tc.check+"_"+tc.name, func(t *testing.T) {
			f := t8SettingsAPI(t)
			path := filepath.Join(t11combined.TrustedProgramDir(t), tc.name)
			// Real on-disk file / NTFS stream: a missing-file rejection must not
			// stand in for any of the three Windows-specific checks.
			require.NoError(t, os.WriteFile(path, []byte("T8 path validator fixture"), 0o600))
			f.expectRejected(t, t8SettingsBody("codex", path, ""), "codex", "path", "cli_path_invalid", tc.check, path, "The program path for Codex was not saved: "+tc.reason+".")
		})
	}
	for _, ext := range []string{".exe", ".EXE", ".cmd", ".CMD", ".bat", ".BAT"} {
		t.Run("accepted-"+ext, func(t *testing.T) {
			f := t8SettingsAPI(t)
			path := filepath.Join(t11combined.TrustedProgramDir(t), "program"+ext)
			require.NoError(t, os.WriteFile(path, []byte("T8 not executed"), 0o600))
			w := f.call(t, http.MethodPut, t8SettingsRoute, t8SettingsBody("codex", path, ""), "person")
			require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
			assert.Equal(t, path, t8SettingsRow(t, t8SettingsRows(t, f.view(t)), "codex")["path"])
		})
	}
}
