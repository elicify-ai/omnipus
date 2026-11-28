//go:build linux

package component_test

// T-130 (c) — office-preview spec §3.9 (2026-11-21, founder decision OF;
// FR-067, behavior B-90): EACH shipped own-folder shape is accepted when the
// registration's program path is set explicitly (the RegisterOptions
// program field, spec §3.9 OF-3): P = T/usr (the deb/rpm shape),
// T/usr/local (installer default and Docker) and T/home/u/.local (the
// per-user installer shape), with the fixtures at <OWN>/libreoffice-custom
// and <OWN>/pdfium-custom. The folder given as <OWN>/libreoffice-custom/
// program resolves to the root <OWN>/libreoffice-custom (the final-root
// clause), and a dotted name (<OWN>/./pdfium-custom) is accepted (as-given,
// cleaned).
//
// Oracles are the spec sentence above, never the code. RED status per the
// spec's T-130 note: (c) is a GUARD row — it passes before and after §3.9
// and guards against over-refusal; no false RED is claimed.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

func TestT130_EachOwnFolderShapeAccepted(t *testing.T) {
	t130 := t.TempDir()
	for _, shape := range []struct {
		name string
		bin  string
	}{
		{"deb", filepath.Join(t130, "usr", "bin", "omnipus")},
		{"installer-default-docker", filepath.Join(t130, "usr", "local", "bin", "omnipus")},
		{"per-user-installer", filepath.Join(t130, "home", "u", ".local", "bin", "omnipus")},
	} {
		t.Run(shape.name, func(t *testing.T) {
			// P/bin/omnipus must exist for dir(E) to resolve; a copy of the
			// test binary is never executed here, it only names the program
			// path.
			require.NoError(t, os.MkdirAll(filepath.Dir(shape.bin), 0o755))
			raw, rawErr := os.ReadFile(mustTestBinary(t))
			require.NoError(t, rawErr)
			require.NoError(t, os.WriteFile(shape.bin, raw, 0o755))
			own := filepath.Clean(filepath.Join(filepath.Dir(shape.bin), "..", "share", "omnipus"))
			require.NoError(t, os.MkdirAll(own, 0o755))
			t.Cleanup(func() { _ = os.RemoveAll(own) })
			home := t.TempDir()

			// LibreOffice at <OWN>/libreoffice-custom: accepted, and the
			// manifest's root is the real path.
			officeRoot := filepath.Join(own, "libreoffice-custom")
			t130OfficeFixture(t, officeRoot)
			manifest, err := component.RegisterOffice(component.RegisterOptions{
				Home: home, Folder: officeRoot, Operator: "t130c", GOOS: "linux", Program: shape.bin,
			})
			require.NoError(t, err, "T-130(c): the %s shape accepts a LibreOffice inside its own folder", shape.name)
			require.NotNil(t, manifest)
			require.Equal(t, filepath.Clean(officeRoot), filepath.Clean(manifest.Root),
				"T-130(c): the manifest root is the real folder")
			require.FileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), component.ManifestFile),
				"T-130(c): the registration wrote its manifest")

			// The same folder given as <OWN>/libreoffice-custom/program:
			// the final root is <OWN>/libreoffice-custom.
			programRoot := filepath.Join(own, "libreoffice-program")
			t130OfficeFixture(t, programRoot)
			manifest, err = component.RegisterOffice(component.RegisterOptions{
				Home: home, Folder: filepath.Join(programRoot, "program"), Operator: "t130c", GOOS: "linux", Program: shape.bin,
			})
			require.NoError(t, err, "T-130(c): the folder given as its program folder resolves to the component root")
			require.NotNil(t, manifest)
			require.Equal(t, filepath.Clean(programRoot), filepath.Clean(manifest.Root),
				"T-130(c): the recorded root is the component root, not the program folder")

			// Renderer at <OWN>/pdfium-custom: accepted.
			rendererRoot := filepath.Join(own, "pdfium-custom")
			t130RendererFixture(t, rendererRoot)
			manifest, err = component.RegisterRenderer(component.RegisterOptions{
				Home: home, Folder: rendererRoot, Operator: "t130c", GOOS: "linux", Program: shape.bin,
			})
			require.NoError(t, err, "T-130(c): the %s shape accepts a renderer inside its own folder", shape.name)
			require.NotNil(t, manifest)
			require.Equal(t, filepath.Clean(rendererRoot), filepath.Clean(manifest.Root))

			// The same shape of folder named with a dot: accepted as given,
			// cleaned.
			dottedRoot := filepath.Join(own, "pdfium-dotted")
			t130RendererFixture(t, dottedRoot)
			manifest, err = component.RegisterRenderer(component.RegisterOptions{
				Home: home, Folder: own + string(filepath.Separator) + "." + string(filepath.Separator) + "pdfium-dotted",
				Operator: "t130c", GOOS: "linux", Program: shape.bin,
			})
			require.NoError(t, err, "T-130(c): a dotted name for a folder inside own is accepted")
			require.NotNil(t, manifest)
			require.Equal(t, filepath.Clean(dottedRoot), filepath.Clean(manifest.Root),
				"T-130(c): the dotted name resolves to the same cleaned root")
		})
	}
}

// mustTestBinary returns the running test binary's resolved path.
func mustTestBinary(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	return resolved
}
