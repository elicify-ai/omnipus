package preview

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// G9 / SF-11. AD-4: the operator's current registration is what the next
// preview launches. A registration change must be visible without a restart.
func TestOfficePreview_RegistrationChangeDropsCachedSelection(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	home := t.TempDir()
	rootA := gatefixInstallOffice(t, "gatefix-office-a")
	rootB := gatefixInstallOffice(t, "gatefix-office-b")
	gatefixRegisterOffice(t, home, rootA)
	svc, err := New(Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)

	first, problem := svc.officeComponent(nil)
	require.Nil(t, problem)
	require.Equal(t, rootA, first.Root)

	removed, err := component.Unregister(home, component.KindOffice)
	require.NoError(t, err)
	require.True(t, removed, "instrument: the first registration must be gone before the second is written")
	gatefixRegisterOffice(t, home, rootB)

	second, problem := svc.officeComponent(nil)
	require.Nil(t, problem)
	require.Equal(t, rootB, second.Root, "AD-4: the next preview must use the new registration, not the selection cached at the first resolve")
}

// gatefixInstallOffice links this test binary into a component root under
// the name the manifest will launch. The link is never executed by the
// selection test; the cache test executes its own copies.
func gatefixInstallOffice(t *testing.T, exeName string) string {
	t.Helper()
	return gatefixInstallProgram(t, exeName)
}

func gatefixInstallProgram(t *testing.T, exeName string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "root")
	require.NoError(t, os.MkdirAll(root, 0o700))
	self, err := os.Executable()
	require.NoError(t, err)
	dest := filepath.Join(root, exeName)
	if linkErr := os.Link(self, dest); linkErr != nil {
		in, openErr := os.Open(self)
		require.NoError(t, openErr)
		defer in.Close()
		out, createErr := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		require.NoError(t, createErr)
		_, copyErr := io.Copy(out, in)
		require.NoError(t, copyErr)
		require.NoError(t, out.Close())
	}
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return resolved
}

func gatefixRegisterOffice(t *testing.T, home, root string) {
	t.Helper()
	gatefixRegisterComponent(t, home, root, "gatefix-office-a", component.KindOffice, "", 0)
}

func gatefixRegisterComponent(t *testing.T, home, root, exeName string, kind component.Kind, buildID string, protocolVersion int) {
	t.Helper()
	// The installed name is the file in the root; callers pass the name they
	// linked. Office uses gatefix-office-a or gatefix-office-b, which is the
	// directory's only executable.
	matches, err := filepath.Glob(filepath.Join(root, "gatefix-*"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "instrument: one program in %s", root)
	exeName = filepath.Base(matches[0])
	data, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	sum := sha256.Sum256(data)
	files := map[string]string{exeName: hex.EncodeToString(sum[:])}
	manifest := component.Manifest{
		Schema: component.ManifestSchema, Component: kind, Version: "gatefix",
		BuildID: buildID, ProtocolVersion: protocolVersion,
		Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: exeName,
		Files: files, FileListSHA256: component.FileListDigest(files),
		Trust: component.TrustOperatorRegistered, Root: root,
		RegisteredBy: "gatefix", RegisteredAt: "2026-11-19T00:00:00Z",
	}
	dir := component.RegisteredDir(home, kind)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), append(raw, '\n'), 0o600))
}
