//go:build windows

package thirdpartycli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/stretchr/testify/require"
)

// Spec §3.2 and founder C1 A: .com is refused, not silently added to the
// .exe/.cmd/.bat set. A Darwin cross-vet is NOT a Windows runtime RED receipt.
func TestSharedProgramPath_WindowsExtensionMatrix_C1(t *testing.T) {
	for _, ext := range []string{".exe", ".EXE", ".cmd", ".CMD", ".bat", ".BAT", ".com", ".COM"} {
		t.Run(ext, func(t *testing.T) {
			program := filepath.Join(t11LongDir(t), "combined-cli"+ext)
			require.NoError(t, os.WriteFile(program, []byte("MZ"), 0o755))
			final, err := filepath.EvalSymlinks(program)
			require.NoError(t, err, "instrument: real regular file under a long path")
			resolved, checkErr := thirdpartycli.ValidateProgramPath("claude-code", program)
			if ext == ".com" || ext == ".COM" {
				require.Error(t, checkErr, "C1 A: follow the three-extension spec; .com is not accepted")
				require.Empty(t, resolved)
				t11RequireCheck(t, checkErr, "windows_extension")
				return
			}
			require.NoError(t, checkErr, "C1 positive control: keep every approved extension")
			require.Equal(t, final, resolved, "callers receive the exact resolved file")
		})
	}
}
