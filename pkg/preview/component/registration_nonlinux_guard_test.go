// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component_test

// T-131(g) guard — office-preview spec §3.9 (gate round 2, TA-04): on the
// platforms where the own-folder rule does not apply, a valid readable
// operator registration made outside any Linux OWN location still RESOLVES
// ready, for both component kinds. The Linux-only resolver tests pin the
// selection rule by simulating GOOS on Linux; this file is UNTAGGED, so it
// compiles and runs wherever the package does — a future resolver change
// that applies the Linux own-folder refusal on darwin or Windows fails here
// (on Windows, registration is the LibreOffice route, so this guard is the
// one that protects it). GOOS simulation tests the platform-selection rule;
// running this file on a darwin host additionally exercises the darwin path
// natively, which is a smoke, not platform certification.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// t131gOutside asserts the precondition that path lies outside the given
// own folder by whole path components (as written).
func t131gOutside(t *testing.T, path, own, what string) {
	t.Helper()
	inside := path == own || (len(path) > len(own) && path[:len(own)] == own && path[len(own)] == filepath.Separator)
	require.False(t, inside, "T-131(g) precondition: %s (%s) must lie outside the Linux own folder %s, so a wrongly-applied own-folder refusal would bite", what, path, own)
}

// t131gDarwinOffice lays out the LibreOffice .app shape the darwin
// registration expects (the same shape the T-130(h) exemption row uses).
func t131gDarwinOffice(t *testing.T, root string) {
	t.Helper()
	program := filepath.Join(root, "Contents", "MacOS")
	require.NoError(t, os.MkdirAll(program, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("fixture program, never executed"), 0o755))
	product := `<?xml version="1.0"?><oor:data xmlns:oor="http://openoffice.org/2001/registry"><oor:component-data oor:name="Setup" oor:package="org.openoffice"><node oor:name="Product"><prop oor:name="ooSetupVersionAboutBox"><value>26.8.1.1</value></prop></node></oor:component-data></oor:data>`
	require.NoError(t, os.WriteFile(filepath.Join(root, "Contents", "Info.plist"),
		[]byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>26.8.1.1</string></dict></plist>`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Contents", "Resources", "services"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Contents", "Resources", "services", "services.rdb"),
		[]byte(`<?xml version="1.0"?>
<components xmlns="http://openoffice.org/2010/uno-components"><component loader="com.sun.star.loader.SharedLibrary" environment="gcc3" uri="vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.so"><implementation name="com.sun.star.chart2.ExponentialScaling" constructor="com_sun_star_chart2_ExponentialScaling_get_implementation"><service name="com.sun.star.chart2.ExponentialScaling"/>
</implementation></component></components>`), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Contents", "Resources", "registry"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Contents", "Resources", "registry", "main.xcd"), []byte(product), 0o600))
}

// t131gWindowsOffice lays out the LibreOffice program-folder shape the
// windows registration expects.
func t131gWindowsOffice(t *testing.T, root string) {
	t.Helper()
	program := filepath.Join(root, "program")
	require.NoError(t, os.MkdirAll(program, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.exe"), []byte("fixture program, never executed"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("fixture program, never executed"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "version.ini"),
		[]byte("[Bootstrap]\nbuildid=fixture\nProductVersion=26.8.1.1\n"), 0o600))
}

// t131gRendererFixture lays out a valid unpacked renderer release (shipped
// packaged manifest over a fingerprinted launcher).
func t131gRendererFixture(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "omnipus-pdfium"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	sum, _, err := component.HashFile(filepath.Join(root, "omnipus-pdfium"))
	require.NoError(t, err)
	files := map[string]string{"omnipus-pdfium": sum}
	m := component.Manifest{
		Schema: component.ManifestSchema, Component: component.KindPDFRenderer,
		Version: "26.8.0.3", BuildID: "t131g-renderer", ProtocolVersion: 1,
		Platform: component.Platform(), Executable: "omnipus-pdfium",
		Files: files, FileListSHA256: component.FileListDigest(files),
		Trust: component.TrustPackaged,
	}
	raw, marshalErr := json.Marshal(m)
	require.NoError(t, marshalErr)
	require.NoError(t, os.WriteFile(filepath.Join(root, component.ManifestFile), raw, 0o600))
}

func TestT131G_NonLinuxRegisteredRootsResolveReady(t *testing.T) {
	// The hypothetical Linux own folder of the running program path: the
	// guard fixtures stage OUTSIDE it, so a resolver that wrongly applied
	// the Linux rule would refuse them and this row would fail.
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	linuxOwn := component.OwnFolder(resolved, "linux")

	// The staging folder is the package work directory: outside OWN and
	// outside every agent-writable root (the T-130(h) pattern).
	work, workErr := os.Getwd()
	require.NoError(t, workErr)
	staging, stageErr := os.MkdirTemp(work, "t131g-")
	require.NoError(t, stageErr)
	t.Cleanup(func() { _ = os.RemoveAll(staging) })

	for _, goos := range []string{"darwin", "windows"} {
		for _, kind := range []component.Kind{component.KindOffice, component.KindPDFRenderer} {
			t.Run(goos+"/"+string(kind), func(t *testing.T) {
				fixture := filepath.Join(staging, goos+"-"+string(kind))
				homeDir := t.TempDir()
				var manifest *component.Manifest
				var regErr error
				switch {
				case kind == component.KindOffice && goos == "darwin":
					t131gDarwinOffice(t, fixture)
					manifest, regErr = component.RegisterOffice(component.RegisterOptions{Home: homeDir, Folder: fixture, Operator: "t131g", GOOS: goos})
				case kind == component.KindOffice && goos == "windows":
					t131gWindowsOffice(t, fixture)
					manifest, regErr = component.RegisterOffice(component.RegisterOptions{Home: homeDir, Folder: fixture, Operator: "t131g", GOOS: goos})
				default:
					t131gRendererFixture(t, fixture)
					manifest, regErr = component.RegisterRenderer(component.RegisterOptions{Home: homeDir, Folder: fixture, Operator: "t131g", GOOS: goos})
				}
				t131gOutside(t, fixture, linuxOwn, "the "+goos+" "+string(kind)+" fixture")
				require.NoError(t, regErr, "T-131(g): the %s %s registration outside any own folder is unchanged", goos, kind)
				require.NotNil(t, manifest)
				require.FileExists(t, filepath.Join(component.RegisteredDir(homeDir, kind), "manifest.json"),
					"T-131(g): the registration wrote its manifest")

				// The guard: the registered component RESOLVES ready — no
				// own-folder refusal, no damage, no packaged substitution.
				resolver := &component.Resolver{Home: homeDir, Executable: resolved, GOOS: goos}
				got, problem := resolver.Resolve(kind)
				require.Nil(t, problem, "T-131(g)/TA-04: the %s %s registration must resolve ready without the Linux own-folder refusal", goos, kind)
				require.NotNil(t, got)
				require.Equal(t, component.TrustOperatorRegistered, got.Manifest.Trust,
					"T-131(g): the resolved component is the operator registration, not a packaged substitute")
				require.Contains(t, got.Source, "registered:", "T-131(g): the registered source is what resolved")
				require.Equal(t, filepath.Clean(fixture), filepath.Clean(got.Manifest.Root),
					"T-131(g): the recorded root is the registered folder")
			})
		}
	}
}
