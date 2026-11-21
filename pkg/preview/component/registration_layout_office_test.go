//go:build darwin || linux

package component_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

func officeMetadataLayout(t *testing.T) (root, exe string) {
	t.Helper()
	root = t.TempDir()
	program := filepath.Join(root, "program")
	require.NoError(t, os.MkdirAll(program, 0700))
	exe = filepath.Join(program, "soffice")
	require.NoError(t, os.WriteFile(exe, []byte("#!/bin/sh\nprintf executed > \"$0.marker\"\nexit 73\n"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte("not executed"), 0600))
	return root, exe
}

// Founder S1 follow-up: official Linux product metadata may exist only in
// main.xcd. Registration must succeed without executing a version probe.
func TestOfficeRegistration_MainXCDOnlyMetadataNeverExecutes(t *testing.T) {
	root, exe := officeMetadataLayout(t)
	registry := filepath.Join(root, "share", "registry")
	require.NoError(t, os.MkdirAll(registry, 0700))
	const product = `<?xml version="1.0"?><oor:data xmlns:oor="http://openoffice.org/2001/registry"><oor:component-data oor:name="Setup" oor:package="org.openoffice"><node oor:name="Product"><prop oor:name="ooSetupVersionAboutBox"><value>26.8.1.1</value></prop></node></oor:component-data></oor:data>`
	require.NoError(t, os.WriteFile(filepath.Join(registry, "main.xcd"), []byte(product), 0600))
	home := t.TempDir()
	manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: root, Operator: "test-operator", GOOS: "linux"})
	require.NoFileExists(t, exe+".marker", "registration must use metadata, never execute the candidate")
	require.NoError(t, err)
	require.Equal(t, "26.8.1.1", manifest.Version)
	require.Equal(t, component.TrustOperatorRegistered, manifest.Trust)
	require.Equal(t, "program/soffice.bin", manifest.Executable)
	require.FileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json"))
}

func TestOfficeRegistration_ExecutableSymlinkOutsideRefused(t *testing.T) {
	root, exe := officeMetadataLayout(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "program", "versionrc"), []byte("ProductVersion=26.8.1.1\n"), 0600))
	outside := filepath.Join(t.TempDir(), "foreign-program")
	require.NoError(t, os.WriteFile(outside, []byte("foreign harmless program"), 0700))
	bin := filepath.Join(root, "program", "soffice.bin")
	require.NoError(t, os.Remove(bin))
	require.NoError(t, os.Symlink(outside, bin))
	linked, err := os.Stat(bin)
	require.NoError(t, err)
	foreign, err := os.Stat(outside)
	require.NoError(t, err)
	require.True(t, os.SameFile(linked, foreign), "positive control: symlink really reaches outside")
	home := t.TempDir()
	manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Folder: root, Operator: "test-operator", GOOS: "linux"})
	require.Error(t, err)
	require.Nil(t, manifest)
	// Both refusals enforce AD-4: the old regular-file check, or the new
	// explicit containment check. Neither may accept a foreign executable.
	require.Regexp(t, `points outside|not a regular file`, err.Error())
	require.NoFileExists(t, exe+".marker")
	require.NoFileExists(t, filepath.Join(component.RegisteredDir(home, component.KindOffice), "manifest.json"))
}
