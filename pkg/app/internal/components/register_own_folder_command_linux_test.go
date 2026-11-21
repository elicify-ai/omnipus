//go:build linux

package components_test

// T-130 command layer — OF-3's person-visible error (spec line 972;
// behavior B-89). D12, team-lead 2026-11-21: the `<kind> not registered: `
// prefix belongs to the error the register command RETURNS, and
// pkg/preview/component may not import this internal package to drive the
// command (Go's internal-package rule), so the prefixed refusal is pinned
// here, where T-129 already drives the same commands. The component layer's
// unprefixed OF-3 sentences stay pinned in pkg/preview/component's T-130
// rows; this file adds the prefix half, word for word.
//
// RED on the code before §3.9: the registrations below succeed and print a
// Registered line, so EqualError on the prefixed refusal fails on a real
// observation.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// t130CmdWorld stages the command's default program world: base =
// dir(resolved test binary)/.., own = base/share/omnipus (the same default
// the command resolves).
func t130CmdWorld(t *testing.T) (base, own string) {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	base = filepath.Clean(filepath.Join(filepath.Dir(resolved), ".."))
	own = filepath.Join(base, "share", "omnipus")
	require.NoError(t, os.MkdirAll(own, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(own) })
	return base, own
}

// t130CmdOfficeFixture lays out a valid 26.8.1.1 LibreOffice (metadata-only
// version probe) at root — valid except for its location, so only the
// own-folder rule can refuse it.
func t130CmdOfficeFixture(t *testing.T, root string) {
	t.Helper()
	program := filepath.Join(root, "program")
	require.NoError(t, os.MkdirAll(program, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice"), []byte("t130 command fixture"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("t130 command fixture"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "versionrc"), []byte("ProductVersion=26.8.1.1\n"), 0o644))
}

// t130CmdRendererFixture lays out an unpacked renderer release (its own
// packaged manifest plus the fingerprinted program, AD-4) at root — valid
// except for its location.
func t130CmdRendererFixture(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "omnipus-pdfium"), []byte("t130 command fixture"), 0o755))
	raw, err := os.ReadFile(filepath.Join(root, "omnipus-pdfium"))
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	files := map[string]string{"omnipus-pdfium": hex.EncodeToString(sum[:])}
	listSum := sha256.Sum256([]byte("omnipus-pdfium " + files["omnipus-pdfium"] + "\n"))
	manifest := map[string]any{
		"schema": 1, "component": "pdf-renderer", "version": "129.0+t130",
		"build_id": "t130-build", "protocol_version": 1,
		"platform": runtime.GOOS + "-" + runtime.GOARCH, "executable": "omnipus-pdfium",
		"files": files, "file_list_sha256": hex.EncodeToString(listSum[:]),
		"trust": "packaged",
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), data, 0o600))
}

// TestT130_CommandReturnsThePrefixedOwnFolderRefusal — B-89: the operator's
// register command returns exactly the prefixed OF-3 text, exits non-zero,
// writes nothing and prints no Registered line and no restart line.
func TestT130_CommandReturnsThePrefixedOwnFolderRefusal(t *testing.T) {
	base, own := t130CmdWorld(t)
	home := t129Home(t, "confined")

	officeRoot := filepath.Join(base, "elsewhere", "opt", "libreoffice26.8")
	t130CmdOfficeFixture(t, officeRoot)
	want := "office not registered: LibreOffice folder " + filepath.Clean(officeRoot) +
		" is outside Omnipus's own folder (" + own +
		"); on Linux, Omnipus accepts only a LibreOffice inside that folder. Re-run the Omnipus installer, which puts LibreOffice in " +
		own + "/office, or put LibreOffice in a new folder inside " + own + " and register that folder"
	out, _, execErr := t129RunRegister(t, "office", officeRoot)
	require.EqualError(t, execErr, want,
		"T-130: the command's returned error must be the prefixed OF-3 text word for word; got %q", execErr)
	require.NotContains(t, out, "Registered ", "T-130: no Registered line after the refusal")
	require.NotContains(t, out, t129Hint, "T-130: no restart line after the refusal")
	require.NoFileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json"),
		"T-130: a refused registration writes nothing")

	rendererRoot := filepath.Join(base, "elsewhere", "usr", "lib", "pdfium")
	t130CmdRendererFixture(t, rendererRoot)
	want = "pdf-renderer not registered: PDF renderer folder " + filepath.Clean(rendererRoot) +
		" is outside Omnipus's own folder (" + own +
		"); on Linux, Omnipus accepts only a PDF renderer inside that folder. Re-run the Omnipus installer, which puts the renderer in " +
		own + "/pdfium, or unpack the renderer release in a new folder inside " + own + " and register that folder"
	out, _, execErr = t129RunRegister(t, "pdf-renderer", rendererRoot)
	require.EqualError(t, execErr, want,
		"T-130: the command's returned error must be the prefixed OF-3 text word for word; got %q", execErr)
	require.NotContains(t, out, "Registered ", "T-130: no Registered line after the refusal")
	require.NotContains(t, out, t129Hint, "T-130: no restart line after the refusal")
	require.NoFileExists(t, filepath.Join(component.RegisteredDir(home, component.KindPDFRenderer), "manifest.json"),
		"T-130: a refused registration writes nothing")
}
