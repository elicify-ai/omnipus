//go:build darwin

package gateway

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

func t11cSameFolder(t *testing.T, a, b string) {
	t.Helper()
	one, err := os.Stat(a)
	require.NoError(t, err, "instrument: configured mount exists")
	two, err := os.Stat(b)
	require.NoError(t, err, "fixture requires this volume to resolve both spellings")
	require.True(t, os.SameFile(one, two), "instrument: two byte-distinct spellings name the SAME physical folder")
	t.Logf("physical identity verified: %q and %q", a, b)
}

func t11cMountedAlternateSave(t *testing.T, configuredName, typedName string) {
	t.Helper()
	f := t8SettingsAPI(t)
	base := t11cCanonicalDir(t)
	host, alt := filepath.Join(base, configuredName), filepath.Join(base, typedName)
	require.NotEqual(t, host, alt, "instrument: input spellings must be byte-distinct")
	require.NoError(t, os.MkdirAll(host, 0o700))
	t11cSameFolder(t, host, alt)
	source := t11bWriteExe(t, "never-launched")
	outside := t11cProgramAt(t, source, t11cCanonicalDir(t))
	t11cSaveAllowed(t, f, outside)
	program := t11cProgramAt(t, source, host)
	typedProgram := filepath.Join(alt, filepath.Base(program))
	t11cSameFolder(t, program, typedProgram)
	ws := seedLibraryWorkspace(t, f.api, "Combined physical mount identity")
	mount, _, err := workspace.CreateMount(f.api.homePath, ws, "physical", host)
	require.NoError(t, err)
	t11cSameFolder(t, mount.HostPath, alt)
	t11cSaveRefused(t, f, typedProgram, "agent_writable_location", typedProgram)
}

func TestCombinedProgramLocation_CaseSpelledMount_Save_R4(t *testing.T) {
	t11cMountedAlternateSave(t, "MountCase", "mountcase")
}

func TestCombinedProgramLocation_UnicodeSpelledMount_Save_R5(t *testing.T) {
	// Explicit escapes retain the independently chosen NFC/NFD byte forms.
	t11cMountedAlternateSave(t, "Caf\u00e9", "Cafe\u0301")
}
