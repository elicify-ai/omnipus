// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package component_test

// Office installer packaging P2 (#1713) — BEHAVIOURAL contract for the
// packaged LibreOffice manifest writer. This file is gated behind the
// //go:build omnipus_packaged_office tag because Go cannot compile calls to
// the not-yet-existing symbol (see office_packaged_manifest_red_test.go for
// the contract note and the RED BLOCKED probe).
//
// DEVELOPER STEP AT GREEN: delete the //go:build line above in the same
// commit that adds component.WritePackagedOfficeManifest /
// component.PackagedOfficeOptions. Every test below then runs in the normal
// suite; none may be weakened or skipped.
//
// Oracles come from the spec, never from the code:
//   - §10 darwin row: official app at <install dir>/../office/LibreOffice.app;
//     the packaged manifest and the filtered service list sit BESIDE the .app
//     inside the component folder, never inside the signed bundle (P1, plan).
//   - §10 linux row: component root carries program/soffice.bin; version from
//     metadata, pinned to the official 26.8.x series.
//   - AD-3: the macOS spell-checker registration is excised from
//     services.rdb; the other registries ship unfiltered.
//   - Resolver.Resolve (component.go doc, AD-4): a packaged slot resolves only
//     when manifest.json carries trust "packaged"; verification failures are
//     reported damaged, never silently accepted (B-17).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

const (
	packagedRDBFiltered   = `<components xmlns="http://openoffice.org/2010/uno-components"><component loader="com.sun.star.loader.SharedLibrary" uri="vnd.sun.star.expand:$LO_LIB_DIR/libmergedlo.dylib"><implementation name="org.openoffice.lingu.MacOSXSpellChecker" constructor="lingucomponent_MacSpellChecker_get_implementation" single-instance="true"><service name="com.sun.star.linguistic2.SpellChecker"/></implementation><implementation name="org.openoffice.lingu.MySpellSpellChecker" constructor="lingucomponent_MySpellChecker_get_implementation"><service name="com.sun.star.linguistic2.SpellChecker"/></implementation></component></components>`
	packagedRDBUnfiltered = `<components xmlns="http://openoffice.org/2010/uno-components"><component loader="com.sun.star.loader.SharedLibrary" uri="vnd.sun.star.expand:$LO_BASE_DIR/program/offapi.rdb"><implementation name="org.openoffice.Office.DataAccess" constructor="offapi_DataAccess_get_implementation"><service name="com.sun.star.sdb.DataAccess"/></implementation></component></components>`

	packagedDarwinPlist = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>26.8.1</string></dict></plist>`

	packagedFixtureProgram = "fixture program, never executed"
)

// packagedDarwinFixture builds the staged darwin component root: the folder
// holding LibreOffice.app (app bundle untouched, services registry inside).
func packagedDarwinFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	contents := filepath.Join(dir, "LibreOffice.app", "Contents")
	require.NoError(t, os.MkdirAll(filepath.Join(contents, "MacOS"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(contents, "Resources", "services"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contents, "MacOS", "soffice"), []byte(packagedFixtureProgram), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(packagedDarwinPlist), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(contents, "Resources", "services", "services.rdb"), []byte(packagedRDBFiltered), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(contents, "Resources", "services", "offapi.rdb"), []byte(packagedRDBUnfiltered), 0o644))
	return dir
}

// packagedLinuxFixture builds the staged linux component root.
func packagedLinuxFixture(t *testing.T, versionrc string) string {
	t.Helper()
	dir := t.TempDir()
	program := filepath.Join(dir, "program")
	require.NoError(t, os.MkdirAll(program, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice"), []byte(packagedFixtureProgram), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(program, "soffice.bin"), []byte(packagedFixtureProgram), 0o755))
	if versionrc != "" {
		require.NoError(t, os.WriteFile(filepath.Join(program, "versionrc"), []byte(versionrc), 0o644))
	}
	return dir
}

// packagedResolver returns a resolver whose first packaged slot is dir.
func packagedResolver(t *testing.T, dir, goos string) *component.Resolver {
	t.Helper()
	base := filepath.Dir(filepath.Dir(dir)) // dir = <base>/install/office
	return &component.Resolver{
		Home:       filepath.Join(base, "home"),
		Executable: filepath.Join(base, "install", "bin", "omnipus"),
		GOOS:       goos,
	}
}

// TestOfficePackagedManifest_DarwinFolderIsResolvedAsPackaged (P2 §10 darwin
// row): the writer fingerprints the staged folder, records the executable
// relative to the component ROOT (not the .app), writes the filtered services
// BESIDE the .app, never inside the signed bundle, and Resolver.Resolve
// accepts the folder with trust "packaged".
func TestOfficePackagedManifest_DarwinFolderIsResolvedAsPackaged(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "install", "office")
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	require.NoError(t, os.Rename(packagedDarwinFixture(t), dir)) // keep dir path stable for the resolver

	plistBefore, err := os.ReadFile(filepath.Join(dir, "LibreOffice.app", "Contents", "Info.plist"))
	require.NoError(t, err)

	require.NoError(t, component.WritePackagedOfficeManifest(dir, component.PackagedOfficeOptions{GOOS: "darwin", GOARCH: "arm64"}))

	data, readErr := os.ReadFile(filepath.Join(dir, "manifest.json"))
	require.NoError(t, readErr, "the packaged manifest is written into the component root, beside the .app")
	m, parseErr := component.ParseManifest(data, component.KindOffice)
	require.NoError(t, parseErr)
	require.Equal(t, component.TrustPackaged, m.Trust, "a packaged manifest carries trust \"packaged\" (§10, Resolver.Resolve)")
	require.Equal(t, "26.8.1", m.Version, "version comes from the bundle metadata (§10 pinned official 26.8.x)")
	require.Equal(t, "LibreOffice.app/Contents/MacOS/soffice", m.Executable, "executable is relative to the component root, through the .app")
	require.Equal(t, "darwin-arm64", m.Platform, "platform records the build target")
	require.Equal(t, "services", m.Registry, "darwin packaged manifest records the filtered registry (AD-3)")
	require.NotEmpty(t, m.RegistrySHA256, "the filtered registry is fingerprinted")
	require.FileExists(t, filepath.Join(dir, "services", "services.rdb"), "the filtered service list sits inside the component folder")
	require.FileExists(t, filepath.Join(dir, "services", "offapi.rdb"), "unfiltered registries ship alongside")
	require.NoFileExists(t, filepath.Join(dir, "LibreOffice.app", "services"),
		"nothing is written inside the signed .app bundle (P1: never inside the bundle)")
	plistAfter, plistErr := os.ReadFile(filepath.Join(dir, "LibreOffice.app", "Contents", "Info.plist"))
	require.NoError(t, plistErr)
	require.Equal(t, string(plistBefore), string(plistAfter), "the .app bundle is left byte-identical (signature preservation)")

	filtered, filterErr := os.ReadFile(filepath.Join(dir, "services", "services.rdb"))
	require.NoError(t, filterErr)
	require.NotContains(t, string(filtered), "MacOSXSpellChecker", "the AD-3 macOS spell-checker registration is excised")
	require.Contains(t, string(filtered), "MySpellSpellChecker", "the HunSpell spell checker survives the filter")

	resolver := packagedResolver(t, dir, "darwin")
	got, problem := resolver.Resolve(component.KindOffice)
	require.Nil(t, problem, "Resolve accepts the writer's folder: %v", problem)
	require.NotNil(t, got)
	require.Equal(t, component.TrustPackaged, got.Manifest.Trust)
	require.True(t, strings.HasPrefix(got.Source, "packaged:"), "resolved from a packaged slot, source=%q", got.Source)
}

// TestOfficePackagedManifest_DarwinTamperedProgramReportsDamaged (P2, B-17):
// replacing a fingerprinted program makes Resolve report the component
// damaged, never usable.
func TestOfficePackagedManifest_DarwinTamperedProgramReportsDamaged(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "install", "office")
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	require.NoError(t, os.Rename(packagedDarwinFixture(t), dir))
	require.NoError(t, component.WritePackagedOfficeManifest(dir, component.PackagedOfficeOptions{GOOS: "darwin", GOARCH: "arm64"}))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "LibreOffice.app", "Contents", "MacOS", "soffice"), []byte("replaced program"), 0o755))

	got, problem := packagedResolver(t, dir, "darwin").Resolve(component.KindOffice)
	require.Nil(t, got)
	require.NotNil(t, problem)
	require.Equal(t, component.StatusDamaged, problem.Status)
}

// TestOfficePackagedManifest_LinuxFolderIsResolvedAsPackaged (P2 §10 linux
// row): same contract for the linux layout, no registry folder.
func TestOfficePackagedManifest_LinuxFolderIsResolvedAsPackaged(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "install", "office")
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	require.NoError(t, os.Rename(packagedLinuxFixture(t, "ProductVersion=26.8.1.1\n"), dir))

	require.NoError(t, component.WritePackagedOfficeManifest(dir, component.PackagedOfficeOptions{GOOS: "linux", GOARCH: "amd64"}))

	data, readErr := os.ReadFile(filepath.Join(dir, "manifest.json"))
	require.NoError(t, readErr)
	m, parseErr := component.ParseManifest(data, component.KindOffice)
	require.NoError(t, parseErr)
	require.Equal(t, component.TrustPackaged, m.Trust)
	require.Equal(t, "26.8.1.1", m.Version)
	require.Equal(t, "program/soffice.bin", m.Executable)
	require.Equal(t, "linux-amd64", m.Platform)
	require.Empty(t, m.Registry, "linux packaged components carry no services registry")

	got, problem := packagedResolver(t, dir, "linux").Resolve(component.KindOffice)
	require.Nil(t, problem, "Resolve accepts the writer's folder: %v", problem)
	require.NotNil(t, got)
	require.Equal(t, component.TrustPackaged, got.Manifest.Trust)
	require.True(t, strings.HasPrefix(got.Source, "packaged:"), "resolved from a packaged slot, source=%q", got.Source)
}

// TestOfficePackagedManifest_LinuxTamperedProgramReportsDamaged (P2, B-17).
func TestOfficePackagedManifest_LinuxTamperedProgramReportsDamaged(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "install", "office")
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	require.NoError(t, os.Rename(packagedLinuxFixture(t, "ProductVersion=26.8.1.1\n"), dir))
	require.NoError(t, component.WritePackagedOfficeManifest(dir, component.PackagedOfficeOptions{GOOS: "linux"}))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "program", "soffice.bin"), []byte("replaced program"), 0o755))

	got, problem := packagedResolver(t, dir, "linux").Resolve(component.KindOffice)
	require.Nil(t, got)
	require.NotNil(t, problem)
	require.Equal(t, component.StatusDamaged, problem.Status)
}

// TestOfficePackagedManifest_RefusesNonLibreOfficeFolder (negative): a folder
// that is not a LibreOffice install is refused and no manifest is written.
func TestOfficePackagedManifest_RefusesNonLibreOfficeFolder(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notepad.exe"), []byte("not an office"), 0o755))

	err := component.WritePackagedOfficeManifest(dir, component.PackagedOfficeOptions{GOOS: "linux"})
	require.Error(t, err, "a non-LibreOffice folder must be refused")
	require.NoFileExists(t, filepath.Join(dir, "manifest.json"), "no manifest is left behind for a refused folder")
}

// TestOfficePackagedManifest_RefusesUnsupportedSeries (negative, §10 pinned
// 26.8.x): a folder whose metadata reports another series is refused.
func TestOfficePackagedManifest_RefusesUnsupportedSeries(t *testing.T) {
	dir := packagedLinuxFixture(t, "ProductVersion=25.2.0.3\n")

	err := component.WritePackagedOfficeManifest(dir, component.PackagedOfficeOptions{GOOS: "linux"})
	require.Error(t, err, "LibreOffice outside the 26.8.x series must be refused (§10)")
	require.Contains(t, err.Error(), "26.8", "the refusal names the supported series")
	require.NoFileExists(t, filepath.Join(dir, "manifest.json"))
}
