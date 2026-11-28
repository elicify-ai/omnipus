// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package component_test

// T-122 (office-preview spec §3.8 BR-2.1/BR-2.3; FR-065; founder Q100 A,
// 2026-11-20) — the startup grant list for the confined Linux boot set.
//
// CONTRACT the developer implements to (BR-2.2 lets backend-lead rename the
// helper; these tests bind the name below and test-side reconciliation is
// the same qa-lead author's):
//
//	// ConfinementStartupGrants lists the paths the confined Linux startup
//	// rules grant read+execute on, in BR-2.1's order: for LibreOffice the
//	// registered root (if any) then the packaged slots in
//	// Resolver.PackagedCandidates order, the same for the PDF renderer, then
//	// the resolved omnipus program file. Paths are real (symlinks resolved).
//	// Packaged slots that do not exist are skipped, whether or not they hold
//	// a manifest; a registered manifest that fails ParseManifest or whose
//	// root does not exist adds nothing. notes carries one line per skipped
//	// entry, for the gateway's sandbox.component_grants log.
//	func ConfinementStartupGrants(r *Resolver) (grants []string, notes []string)
//
// RED on 24f2f912c: the helper does not exist, so this file does not compile
// (spec §13: the helper is introduced as a stub first, converting this into
// a failing-assertion red before GREEN).
//
// Oracles come from the spec and the layout the test builds, never from the
// helper or PackagedCandidates: every expected path is computed from R
// directly, then EvalSymlinks'd.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// t122ManifestFileListSHA256 computes FileListSHA256 by the documented
// formula ("fingerprints the sorted \"<path> <sha256>\\n\" lines",
// component.go) — written out here so the fixture does not call the code
// under test's helper to build its own oracle.
func t122ManifestFileListSHA256(files map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines strings.Builder
	for _, name := range names {
		lines.WriteString(name + " " + files[name] + "\n")
	}
	sum := sha256.Sum256([]byte(lines.String()))
	return hex.EncodeToString(sum[:])
}

func t122SHA256File(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// t122WriteRegisteredManifest writes a registered manifest for kind naming
// root. With valid = false the manifest is corrupted after writing (trailing
// data), so ParseManifest must reject it. The root itself must exist: the
// executable it names is fingerprinted for real.
func t122WriteRegisteredManifest(t *testing.T, home string, kind component.Kind, root string, valid bool) {
	t.Helper()
	dir := component.RegisteredDir(home, kind)
	require.NoError(t, os.MkdirAll(dir, 0700))
	exeRel := "program/soffice.bin"
	if kind == component.KindPDFRenderer {
		exeRel = "omnipus-pdfium"
	}
	exePath := filepath.Join(root, filepath.FromSlash(exeRel))
	if _, statErr := os.Stat(exePath); statErr != nil {
		t122Exe(t, exePath) // the registered root must hold the executable the manifest fingerprints
	}
	sum := t122SHA256File(t, exePath)
	files := map[string]string{exeRel: sum}
	m := map[string]any{
		"schema":           component.ManifestSchema,
		"component":        string(kind),
		"version":          "26.8.1.1",
		"platform":         "linux-amd64",
		"executable":       exeRel,
		"files":            files,
		"file_list_sha256": t122ManifestFileListSHA256(files),
		"trust":            component.TrustOperatorRegistered,
		"root":             root,
		"registered_by":    "t122",
		"registered_at":    "2026-11-20T00:00:00Z",
	}
	if kind == component.KindPDFRenderer {
		m["build_id"] = "t122-build"
		m["protocol_version"] = 1
	}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	if !valid {
		data = append(data, ' ', '{', '}')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), data, 0600))
}

// t122WriteRegisteredManifestWithoutRoot writes a registered manifest whose
// root does not exist: nothing under root is created, and the fingerprint is
// a synthetic SHA-256 (the manifest passes ParseManifest's format checks but
// names a root that is missing, which is exactly the BR-2.1 skip case).
func t122WriteRegisteredManifestWithoutRoot(t *testing.T, home string, kind component.Kind, root string) {
	t.Helper()
	dir := component.RegisteredDir(home, kind)
	require.NoError(t, os.MkdirAll(dir, 0700))
	exeRel := "program/soffice.bin"
	if kind == component.KindPDFRenderer {
		exeRel = "omnipus-pdfium"
	}
	files := map[string]string{exeRel: strings.Repeat("ab", 32)}
	m := map[string]any{
		"schema":           component.ManifestSchema,
		"component":        string(kind),
		"version":          "26.8.1.1",
		"platform":         "linux-amd64",
		"executable":       exeRel,
		"files":            files,
		"file_list_sha256": t122ManifestFileListSHA256(files),
		"trust":            component.TrustOperatorRegistered,
		"root":             root,
		"registered_by":    "t122",
		"registered_at":    "2026-11-20T00:00:00Z",
	}
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), data, 0600))
}

// t122Slot creates an existing packaged slot (content is irrelevant: BR-2.1
// grants a slot by its location, manifest or not).
func t122Slot(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(path, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(path, "marker"), []byte("slot"), 0644))
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}

func t122Exe(t *testing.T, path string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755))
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return resolved
}

// t122AssertExact fails with a diff-shaped message when the grant list is
// not exactly want, in order.
func t122AssertExact(t *testing.T, got, want []string) {
	t.Helper()
	require.Equal(t, want, got,
		"T-122: the startup grant list must equal the layout-derived expectation, in BR-2.1 order")
}

func TestT122_DebLayoutGrantsShareSlotsAndProgramFile(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "usr")
	exe := t122Exe(t, filepath.Join(root, "bin", "omnipus"))
	office := t122Slot(t, filepath.Join(root, "share", "omnipus", "office"))
	pdfium := t122Slot(t, filepath.Join(root, "share", "omnipus", "pdfium"))

	grants, _ := component.ConfinementStartupGrants(&component.Resolver{
		Home:       filepath.Join(r, "home"),
		Executable: filepath.Join(root, "bin", "omnipus"),
		GOOS:       "linux",
	})
	t122AssertExact(t, grants, []string{office, pdfium, exe})
}

func TestT122_InstallerLayoutGrantsLocalShareSlotsAndProgramFile(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "usr", "local")
	exe := t122Exe(t, filepath.Join(root, "bin", "omnipus"))
	office := t122Slot(t, filepath.Join(root, "share", "omnipus", "office"))
	pdfium := t122Slot(t, filepath.Join(root, "share", "omnipus", "pdfium"))

	grants, _ := component.ConfinementStartupGrants(&component.Resolver{
		Home:       filepath.Join(r, "home"),
		Executable: filepath.Join(root, "bin", "omnipus"),
		GOOS:       "linux",
	})
	t122AssertExact(t, grants, []string{office, pdfium, exe})
}

func TestT122_MissingSlotIsSkipped(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "usr")
	exe := t122Exe(t, filepath.Join(root, "bin", "omnipus"))
	office := t122Slot(t, filepath.Join(root, "share", "omnipus", "office"))
	// pdfium slot deliberately absent.

	grants, _ := component.ConfinementStartupGrants(&component.Resolver{
		Home:       filepath.Join(r, "home"),
		Executable: filepath.Join(root, "bin", "omnipus"),
		GOOS:       "linux",
	})
	t122AssertExact(t, grants, []string{office, exe})
}

func TestT122_FirstLinuxSlotIsListedWhenItExists(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "usr")
	exe := t122Exe(t, filepath.Join(root, "bin", "omnipus"))
	adjacent := t122Slot(t, filepath.Join(root, "office")) // <exe>/../office
	share := t122Slot(t, filepath.Join(root, "share", "omnipus", "office"))
	pdfium := t122Slot(t, filepath.Join(root, "share", "omnipus", "pdfium"))

	grants, _ := component.ConfinementStartupGrants(&component.Resolver{
		Home:       filepath.Join(r, "home"),
		Executable: filepath.Join(root, "bin", "omnipus"),
		GOOS:       "linux",
	})
	t122AssertExact(t, grants, []string{adjacent, share, pdfium, exe})
}

func TestT122_RegisteredRootLeadsPackagedSlots(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "usr")
	home := filepath.Join(r, "home")
	exe := t122Exe(t, filepath.Join(root, "bin", "omnipus"))
	registered := t122Slot(t, filepath.Join(root, "share", "omnipus", "registered-office"))
	office := t122Slot(t, filepath.Join(root, "share", "omnipus", "office"))
	pdfium := t122Slot(t, filepath.Join(root, "share", "omnipus", "pdfium"))
	t122WriteRegisteredManifest(t, home, component.KindOffice, registered, true)

	grants, _ := component.ConfinementStartupGrants(&component.Resolver{
		Home:       home,
		Executable: filepath.Join(root, "bin", "omnipus"),
		GOOS:       "linux",
	})
	t122AssertExact(t, grants, []string{registered, office, pdfium, exe})
}

func TestT122_InvalidOrMissingRegisteredManifestAddsNothing(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "usr")
	home := filepath.Join(r, "home")
	exe := t122Exe(t, filepath.Join(root, "bin", "omnipus"))
	office := t122Slot(t, filepath.Join(root, "share", "omnipus", "office"))
	pdfium := t122Slot(t, filepath.Join(root, "share", "omnipus", "pdfium"))

	// (1) valid manifest whose root does not exist: nothing under that root
	// is created, so the root is genuinely missing.
	t122WriteRegisteredManifestWithoutRoot(t, home, component.KindOffice, filepath.Join(r, "gone", "office"))
	require.NoDirExists(t, filepath.Join(r, "gone"), "instrument: the missing-root case must not plant the root")
	grants, _ := component.ConfinementStartupGrants(&component.Resolver{
		Home: home, Executable: filepath.Join(root, "bin", "omnipus"), GOOS: "linux"})
	t122AssertExact(t, grants, []string{office, pdfium, exe})

	// (2) manifest that fails ParseManifest.
	t122WriteRegisteredManifest(t, home, component.KindOffice, filepath.Join(r, "opt", "broken"), false)
	grants, _ = component.ConfinementStartupGrants(&component.Resolver{
		Home: home, Executable: filepath.Join(root, "bin", "omnipus"), GOOS: "linux"})
	t122AssertExact(t, grants, []string{office, pdfium, exe})
}

func TestT122_SymlinkedSlotListsItsTarget(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "usr")
	exe := t122Exe(t, filepath.Join(root, "bin", "omnipus"))
	target := t122Slot(t, filepath.Join(r, "real-office"))
	link := filepath.Join(root, "share", "omnipus", "office")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0755))
	require.NoError(t, os.Symlink(target, link))
	pdfium := t122Slot(t, filepath.Join(root, "share", "omnipus", "pdfium"))

	grants, _ := component.ConfinementStartupGrants(&component.Resolver{
		Home:       filepath.Join(r, "home"),
		Executable: filepath.Join(root, "bin", "omnipus"),
		GOOS:       "linux",
	})
	t122AssertExact(t, grants, []string{target, pdfium, exe})
}

func TestT122_NeverGrantsParentFoldersOrBin(t *testing.T) {
	r := t.TempDir()
	root := filepath.Join(r, "usr", "local")
	exe := t122Exe(t, filepath.Join(root, "bin", "omnipus"))
	office := t122Slot(t, filepath.Join(root, "share", "omnipus", "office"))
	pdfium := t122Slot(t, filepath.Join(root, "share", "omnipus", "pdfium"))

	grants, _ := component.ConfinementStartupGrants(&component.Resolver{
		Home:       filepath.Join(r, "home"),
		Executable: filepath.Join(root, "bin", "omnipus"),
		GOOS:       "linux",
	})
	// The whole list is exactly the two slots and the program file: no
	// parent folder and no bin folder can hide in it.
	t122AssertExact(t, grants, []string{office, pdfium, exe})
	banned := []string{
		filepath.Join(root, "share", "omnipus"),
		filepath.Join(root, "share"),
		filepath.Join(r, "usr"),
		filepath.Join(root, "bin"),
		root,
	}
	for _, got := range grants {
		for _, bad := range banned {
			require.NotEqual(t, bad, got, "T-122(g): no parent folder or bin folder is granted")
		}
	}
}
