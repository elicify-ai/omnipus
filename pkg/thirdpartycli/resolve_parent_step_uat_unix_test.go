//go:build darwin || linux

package thirdpartycli_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/thirdpartycli"
	"github.com/elicify-ai/omnipus/tests/fixtures/t11combined"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: T11 §3.2 checks the final file and every actual link/link folder.
// Visiting an ordinary directory and then stepping back out with .. must not
// make that abandoned directory part of the resolved file's ancestry. This
// covers the identity-pop branch added by the 28a355b20 resolver refactor.
func TestT11Resolver_ParentStepDropsAbandonedDirectory(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	base := t11combined.TrustedProgramDir(t)
	abandoned := filepath.Join(base, "abandoned")
	safe := filepath.Join(base, "safe")
	require.NoError(t, os.Mkdir(abandoned, 0o700))
	require.NoError(t, os.Mkdir(safe, 0o700))
	program := filepath.Join(safe, "codex")
	require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nexit 0\n"), 0o700))
	// Do not use filepath.Join here: it would erase the .. before the real
	// resolver sees it. No symlink is located in the abandoned directory.
	entered := abandoned + string(filepath.Separator) + ".." + string(filepath.Separator) + "safe" + string(filepath.Separator) + "codex"
	want, resolveErr := filepath.EvalSymlinks(entered)
	require.NoError(t, resolveErr, "instrument: the OS resolves this actual parent step")
	require.Equal(t, program, want)
	got, checkErr := thirdpartycli.CheckProgramPath("codex", entered, []string{abandoned}, nil)
	require.NoError(t, checkErr, "the resolved file and all its real ancestors are outside the abandoned forbidden root")
	assert.Equal(t, want, got, "the caller receives the physical checked target, not an unwalked spelling")

	blocked, blockedErr := thirdpartycli.CheckProgramPath("codex", entered, []string{safe}, nil)
	assert.Empty(t, blocked, "the same parent step cannot bypass a real forbidden final location")
	var failed *thirdpartycli.CheckError
	require.ErrorAs(t, blockedErr, &failed)
	assert.Equal(t, thirdpartycli.CheckAgentWritableLocation, failed.Check)
}
