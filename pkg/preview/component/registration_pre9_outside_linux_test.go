//go:build linux

package component_test

// T-131 (a), (d), (e), (f) — office-preview spec §3.9 (2026-11-21, founder
// decision OF; FR-068): a registration made before §3.9 whose recorded root
// lies outside Omnipus's own folder is refused at every use. The oracles are
// the spec's OF-4 texts and note format, never the code. The roots are
// staged OUTSIDE OWN on purpose (that is the pre-§3.9 state), their mode is
// 000 so any attempt to open anything inside them fails with a different
// error than the refusal, and OWN (P/share/omnipus for the resolver program
// path P/bin/omnipus) is created so the location check has a real folder to
// compare against.
//
// RED on the code before §3.9: Resolve accepts the planted manifests (the
// files hash-verify), so every refusal assertion fails on a non-nil
// component; ConfinementStartupGrants grants both roots, so the note
// assertions fail on the missing notes.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/stretchr/testify/require"
)

// t131StageOwn stages the resolver program file P/bin/omnipus and creates
// OWN = P/share/omnipus, returning the layout. The program file itself is a
// tiny marker: nothing executes it in these rows.
func t131StageOwn(t *testing.T, base string) (bin, own string) {
	t.Helper()
	bin = filepath.Join(base, "bin", "omnipus")
	own = filepath.Join(base, "share", "omnipus")
	require.NoError(t, os.MkdirAll(filepath.Dir(bin), 0o700))
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, os.MkdirAll(own, 0o755))
	return bin, own
}

// t131PlantedOperatorManifest writes an operator-registered manifest into
// the home's RegisteredDir whose root is rootDir, with real file hashes over
// the two files the root carries. The root must exist with its files before
// this is called (the hashes are computed from them).
func t131PlantedOperatorManifest(t *testing.T, home string, kind component.Kind, rootDir, exeName string, files []string) {
	t.Helper()
	// D13 (team-lead 2026-11-21): a manifest whose executable is not among
	// the fingerprinted files fails to PARSE before the OF-4 check, so the
	// row would observe the wrong refusal. The precondition makes that
	// impossible to reintroduce silently.
	require.Contains(t, files, exeName,
		"t131 precondition: the manifest's executable must be among the fingerprinted files, or the manifest fails to parse before the own-folder check")
	hashes := make(map[string]string, len(files))
	for _, name := range files {
		sum, _, err := component.HashFile(filepath.Join(rootDir, name))
		require.NoError(t, err)
		hashes[name] = sum
	}
	list := component.FileListDigest(hashes)
	m := component.Manifest{
		Schema:          component.ManifestSchema,
		Component:       kind,
		Version:         "26.8.0.3",
		BuildID:         "t131-pre9",
		ProtocolVersion: 1,
		Platform:        component.Platform(),
		Executable:      exeName,
		Files:           hashes,
		FileListSHA256:  list,
		Trust:           component.TrustOperatorRegistered,
		Root:            rootDir,
		RegisteredBy:    "t131-pre9-operator",
	}
	dir := component.RegisteredDir(home, kind)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, component.ManifestFile), raw, 0o600))
}

// t131LockedRoot makes rootDir and its files inaccessible to a non-root
// user, asserting the precondition per file (as root this precondition
// cannot hold — the test fails, never skips, per the T-131 row).
func t131LockedRoot(t *testing.T, rootDir string, files []string) {
	t.Helper()
	if os.Getuid() == 0 {
		t.Fatalf("BLOCKED: T-131 runs as a non-root user — required by office-preview spec §3.9 T-131; as root the mode-000 root cannot prove that nothing inside it was opened")
	}
	require.NoError(t, os.Chmod(rootDir, 0o000))
	t.Cleanup(func() { _ = os.Chmod(rootDir, 0o700) })
	for _, name := range files {
		_, err := os.Open(filepath.Join(rootDir, name))
		require.ErrorIs(t, err, os.ErrPermission,
			"T-131 precondition: the locked root's %s must be unreadable, or the refusal cannot be distinguished from an open", name)
	}
}

// TestT131_ResolveRefusesRegisteredRootOutsideOwn — OF-4 (a): a registered
// manifest whose recorded root is outside OWN resolves to the new refusal
// status naming OWN, never to a packaged component, and nothing inside the
// root is opened (the mode-000 root makes any open fail with a permission
// error, which would surface as the damaged reason instead). A valid
// packaged component sits at OWN's slot first (R2-F5, gate round 2: office
// slot "office", renderer slot "pdfium") and positively resolves ready, so
// the refusal and the no-fallback assertion bite against something real.
func TestT131_ResolveRefusesRegisteredRootOutsideOwn(t *testing.T) {
	for _, tc := range []struct {
		kind    component.Kind
		exeName string
		files   []string
	}{
		{component.KindOffice, "soffice.bin", []string{"soffice.bin", "manifest-fixture.bin", "dependency.dat"}},
		{component.KindPDFRenderer, "omnipus-pdfium", []string{"omnipus-pdfium", "dependency.dat"}},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			base := t.TempDir()
			bin, own := t131StageOwn(t, base)
			home := filepath.Join(base, "home")
			require.NoError(t, os.MkdirAll(home, 0o700))

			// R2-F5: a valid packaged component at OWN's slot, and the
			// positive control that it really resolves ready before the
			// outside registration shadows it.
			slot := filepath.Join(own, "office")
			if tc.kind == component.KindOffice {
				require.NoError(t, os.MkdirAll(filepath.Join(slot, "program"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(slot, "program", "soffice.bin"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(slot, "program", "soffice"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(slot, "program", "versionrc"), []byte("ProductVersion=26.8.1.1\n"), 0o600))
				require.NoError(t, component.WritePackagedOfficeManifest(slot, component.PackagedOfficeOptions{GOOS: "linux", GOARCH: runtime.GOARCH}))
			} else {
				slot = filepath.Join(own, "pdfium")
				t131gRendererFixture(t, slot)
			}
			resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
			packaged, packagedProblem := resolver.Resolve(tc.kind)
			require.Nil(t, packagedProblem,
				"R2-F5 precondition: the packaged %s at OWN must resolve ready before the outside registration shadows it", tc.kind)
			require.NotNil(t, packaged)
			require.Contains(t, packaged.Source, "packaged:",
				"R2-F5 precondition: the ready component is the packaged one")

			// The pre-§3.9 root lives OUTSIDE OWN by construction (assert it).
			root := filepath.Join(base, "elsewhere", string(tc.kind))
			require.NoError(t, os.MkdirAll(root, 0o700))
			for _, name := range tc.files {
				require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("t131 fixture: "+name), 0o600))
			}
			require.NoError(t, os.Chmod(root, 0o755))
			t131NotInsideOwn(t, root, own, "the planted root")
			t131PlantedOperatorManifest(t, home, tc.kind, root, tc.exeName, tc.files)
			t131LockedRoot(t, root, tc.files)

			got, problem := resolver.Resolve(tc.kind)
			require.Nil(t, got, "T-131(a): the outside-own registration must not resolve to a component")
			require.NotNil(t, problem, "T-131(a): the refusal must be reported")
			require.NotEqual(t, component.StatusMissing, problem.Status, "T-131(a): the pre-§3.9 registration exists — not missing")
			require.NotEqual(t, component.StatusDamaged, problem.Status, "T-131(a): the root is refused for its location, not damaged — the mode-000 root proves nothing inside was opened")
			// R2-F5: the SPECIFIC outside-own refusal — the exact OF-4 reason,
			// naming both the recorded root and Omnipus's own folder.
			require.Equal(t, "registered root "+filepath.Clean(root)+" is outside Omnipus's own folder "+own, problem.Reason,
				"T-131(a)/R2-F5: the refusal must be the exact OF-4 outside reason; got %q", problem.Reason)

			// The packaged slot is still valid and present; the refusal must
			// never fall back to it (AD-4).
			got2, problem2 := resolver.Resolve(tc.kind)
			require.Nil(t, got2, "T-131(a): no fallback to the packaged slot after an outside-own registration (AD-4)")
			require.NotNil(t, problem2)
			require.Equal(t, problem.Reason, problem2.Reason, "T-131(a): the same refusal again")
		})
	}
}

// TestT131_ResolveRefusesRootThatBecameLinkOutsideOwn — OF-4 (e): a
// recorded root inside OWN that has become a link to a folder outside is
// refused (the "root with every link resolved now" clause).
func TestT131_ResolveRefusesRootThatBecameLinkOutsideOwn(t *testing.T) {
	base := t.TempDir()
	bin, own := t131StageOwn(t, base)
	home := filepath.Join(base, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	root := filepath.Join(own, "office-moved")
	require.NoError(t, os.MkdirAll(root, 0o755))
	files := []string{"soffice.bin", "manifest-fixture.bin", "dependency.dat"}
	for _, name := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("t131e fixture"), 0o600))
	}
	t131PlantedOperatorManifest(t, home, component.KindOffice, root, "soffice.bin", files)

	// The root becomes a link pointing OUTSIDE OWN; the manifest is
	// byte-identical and the target carries the same files, so only the
	// resolved-location check can refuse this.
	outside := filepath.Join(base, "elsewhere", "office-now")
	require.NoError(t, os.MkdirAll(filepath.Dir(outside), 0o755))
	require.NoError(t, os.Rename(root, outside))
	require.NoError(t, os.Symlink(outside, root))
	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	t131NotInsideOwn(t, resolved, own, "the link's target")

	resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
	got, problem := resolver.Resolve(component.KindOffice)
	require.Nil(t, got, "T-131(e): a recorded root that became a link out of OWN must not resolve")
	require.NotNil(t, problem, "T-131(e): the refusal must be reported")
	require.Contains(t, problem.Reason, own, "T-131(e): the refusal names Omnipus's own folder")
}

// TestT131_ConfinementStartupGrantsSkipOutsideRoots — OF-4 (d): neither
// outside root is granted, and each refusal leaves exactly the note
// `<kind> registered root <root> skipped: outside Omnipus's own folder <own>`.
func TestT131_ConfinementStartupGrantsSkipOutsideRoots(t *testing.T) {
	base := t.TempDir()
	bin, own := t131StageOwn(t, base)
	home := filepath.Join(base, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	plant := func(kind component.Kind, exeName string) string {
		root := filepath.Join(base, "elsewhere", string(kind))
		require.NoError(t, os.MkdirAll(root, 0o755))
		// D13: the executable is among the fingerprinted files, so the
		// planted manifest parses and the OF-4 check is reached.
		files := []string{exeName, "manifest-fixture.bin", "dependency.dat"}
		for _, name := range files {
			require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("t131d fixture"), 0o600))
		}
		t131PlantedOperatorManifest(t, home, kind, root, exeName, files)
		return root
	}
	officeRoot := plant(component.KindOffice, "soffice.bin")
	rendererRoot := plant(component.KindPDFRenderer, "omnipus-pdfium")

	resolver := &component.Resolver{Home: home, Executable: bin}
	grants, notes := component.ConfinementStartupGrants(resolver)
	for _, forbidden := range []string{officeRoot, rendererRoot} {
		require.NotContains(t, grants, forbidden,
			"T-131(d): an outside-own registered root must not be a startup grant")
	}
	for _, want := range []string{
		"office registered root " + officeRoot + " skipped: outside Omnipus's own folder " + own,
		"pdf-renderer registered root " + rendererRoot + " skipped: outside Omnipus's own folder " + own,
	} {
		require.Contains(t, notes, want,
			"T-131(d): the startup grant list must note the skipped outside root exactly as §3.9 OF-4 writes it")
	}
}

// TestT131_UnregisterFallsBackToPackagedSlot — OF-4 (f): after Unregister
// the packaged component resolves StatusReady.
func TestT131_UnregisterFallsBackToPackagedSlot(t *testing.T) {
	base := t.TempDir()
	bin, own := t131StageOwn(t, base)
	home := filepath.Join(base, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	// A valid packaged slot at OWN/office, complete with its manifest.
	slot := filepath.Join(own, "office")
	require.NoError(t, os.MkdirAll(filepath.Join(slot, "program"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(slot, "program", "soffice.bin"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(slot, "program", "soffice"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(slot, "program", "versionrc"), []byte("ProductVersion=26.8.1.1\n"), 0o600))
	require.NoError(t, component.WritePackagedOfficeManifest(slot, component.PackagedOfficeOptions{GOOS: "linux", GOARCH: runtime.GOARCH}))
	// The pre-§3.9 registration outside OWN shadows it.
	root := filepath.Join(base, "elsewhere", "office")
	require.NoError(t, os.MkdirAll(root, 0o755))
	// D13 class (this site missed in the D13 round; the Fly run of the row
	// caught it): the planted manifest names soffice.bin, so soffice.bin
	// must be among the fingerprinted files or the manifest fails to parse
	// before the own-folder check.
	files := []string{"soffice.bin", "manifest-fixture.bin", "dependency.dat"}
	for _, name := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("t131f fixture"), 0o600))
	}
	t131PlantedOperatorManifest(t, home, component.KindOffice, root, "soffice.bin", files)

	resolver := &component.Resolver{Home: home, Executable: bin, GOOS: "linux"}
	_, problem := resolver.Resolve(component.KindOffice)
	require.NotNil(t, problem, "T-131(f) precondition: the pre-§3.9 registration must be the refusal under test")

	removed, err := component.Unregister(home, component.KindOffice)
	require.NoError(t, err)
	require.True(t, removed, "T-131(f): the registration existed and was removed")
	got, problemAfter := resolver.Resolve(component.KindOffice)
	require.Nil(t, problemAfter, "T-131(f): after unregistering, the packaged component resolves (StatusReady — no refusal)")
	require.NotNil(t, got)
	require.Contains(t, got.Source, "packaged:", "T-131(f): the packaged component from OWN's slot is what resolves")
}

// t131NotInsideOwn asserts the precondition that path is not inside own (a
// precondition on the FIXTURE, by whole path components, as the spec's
// "strictly inside" is defined).
func t131NotInsideOwn(t *testing.T, path, own, what string) {
	t.Helper()
	inside := path == own || hasPrefixComponent(path, own)
	require.False(t, inside, "T-131 precondition: %s (%s) must lie outside Omnipus's own folder %s for this row", what, path, own)
}

// hasPrefixComponent reports whether path lies strictly inside base by
// whole path components.
func hasPrefixComponent(path, base string) bool {
	if base == path {
		return false
	}
	return len(path) > len(base) && path[:len(base)] == base && path[len(base)] == filepath.Separator
}
