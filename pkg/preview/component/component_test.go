package component

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Independent file-list oracle: AD-4's manifest identifies every file and
// fingerprints the list. Do not call FileListDigest to manufacture its oracle.
func componentFixture(t *testing.T, root string, kind Kind, trust string) Manifest {
	t.Helper()
	require.NoError(t, os.MkdirAll(root, 0700))
	name := "omnipus-pdfium"
	if kind == KindOffice {
		name = "soffice"
	}
	data := []byte("fixture-program-not-launched")
	require.NoError(t, os.WriteFile(filepath.Join(root, name), data, 0700))
	sum := sha256.Sum256(data)
	files := map[string]string{name: hex.EncodeToString(sum[:])}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var lines strings.Builder
	for _, n := range names {
		lines.WriteString(n + " " + files[n] + "\n")
	}
	listSum := sha256.Sum256([]byte(lines.String()))
	m := Manifest{Schema: 1, Component: kind, Version: "26.8.1", BuildID: "fixture-build", ProtocolVersion: 1, Platform: runtime.GOOS + "-" + runtime.GOARCH, Executable: name, Files: files, FileListSHA256: hex.EncodeToString(listSum[:]), Trust: trust}
	if trust == TrustOperatorRegistered {
		resolvedRoot, err := filepath.EvalSymlinks(root)
		require.NoError(t, err)
		m.Root = resolvedRoot
		m.RegisteredBy = "test-operator"
		m.RegisteredAt = "2026-10-31T00:00:00Z"
	}
	return m
}

func writeComponentManifest(t *testing.T, dir string, m Manifest) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0700))
	data, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600))
}

// T-04 / AD-4: packaged slots and explicit protected registration, not PATH.
func TestComponentResolver_PackagedRegisteredAndPATHIsolation(t *testing.T) {
	base := t.TempDir()
	resolver := &Resolver{Home: filepath.Join(base, "home"), Executable: filepath.Join(base, "install", "bin", "omnipus"), GOOS: "linux"}
	require.Equal(t, []string{filepath.Join(base, "install", "pdfium"), filepath.Join(base, "install", "share", "omnipus", "pdfium")}, resolver.PackagedCandidates(KindPDFRenderer))
	fakePATH := filepath.Join(base, "workspace")
	fixture := componentFixture(t, fakePATH, KindPDFRenderer, TrustPackaged)
	writeComponentManifest(t, fakePATH, fixture)
	t.Setenv("PATH", fakePATH)
	// No packaged/registered manifest: even a valid PATH component is ignored.
	got, problem := resolver.Resolve(KindPDFRenderer)
	require.Nil(t, got)
	require.NotNil(t, problem)
	require.Equal(t, StatusMissing, problem.Status)
	packaged := resolver.PackagedCandidates(KindPDFRenderer)[0]
	m := componentFixture(t, packaged, KindPDFRenderer, TrustPackaged)
	writeComponentManifest(t, packaged, m)
	got, problem = resolver.Resolve(KindPDFRenderer)
	require.Nil(t, problem)
	require.NotNil(t, got)
	resolvedRoot, err := filepath.EvalSymlinks(packaged)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(resolvedRoot, "omnipus-pdfium"), got.Executable)
	registered := filepath.Join(base, "operator-component")
	m = componentFixture(t, registered, KindPDFRenderer, TrustOperatorRegistered)
	writeComponentManifest(t, RegisteredDir(resolver.Home, KindPDFRenderer), m)
	got, problem = resolver.Resolve(KindPDFRenderer)
	require.Nil(t, problem)
	require.NotNil(t, got)
	require.Equal(t, m.Root, got.Root, "explicit registration wins over a packaged component")
	// A damaged explicit registration cannot silently fall back to packaged.
	require.NoError(t, os.WriteFile(filepath.Join(registered, m.Executable), []byte("replacement"), 0700))
	got, problem = resolver.Resolve(KindPDFRenderer)
	require.Nil(t, got)
	require.NotNil(t, problem)
	require.Equal(t, StatusDamaged, problem.Status)
	require.Contains(t, problem.Reason, "fingerprint mismatch")
}

func TestComponentResolver_ExecutableSymlinkEscapeRefused(t *testing.T) {
	base := t.TempDir()
	resolver := &Resolver{Home: filepath.Join(base, "home"), Executable: filepath.Join(base, "install", "bin", "omnipus"), GOOS: "linux"}
	root := resolver.PackagedCandidates(KindPDFRenderer)[0]
	m := componentFixture(t, root, KindPDFRenderer, TrustPackaged)
	writeComponentManifest(t, root, m)
	outside := filepath.Join(base, "outside")
	require.NoError(t, os.WriteFile(outside, []byte("fixture-program-not-launched"), 0700))
	require.NoError(t, os.Remove(filepath.Join(root, m.Executable)))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, m.Executable)))
	got, problem := resolver.Resolve(KindPDFRenderer)
	require.Nil(t, got)
	require.NotNil(t, problem)
	require.Equal(t, StatusDamaged, problem.Status)
	require.Contains(t, problem.Reason, "outside the component folder")
}

// T-05: resolve verifies first; later size/mtime changes cause a re-hash.
func TestComponentOffice_RehashesChangedExecutable(t *testing.T) {
	for _, change := range []string{"size", "mtime"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			m := componentFixture(t, root, KindOffice, TrustPackaged)
			c, problem := load(KindOffice, &m, root, "", "fixture")
			require.Nil(t, problem)
			require.NotNil(t, c)
			require.Nil(t, c.VerifyIfChanged())
			exe := filepath.Join(root, m.Executable)
			info, err := os.Stat(exe)
			require.NoError(t, err)
			data := []byte("fixture-program-not-launched")
			if change == "size" {
				data = append(data, '!')
			} else {
				data[0] = 'X'
			}
			require.NoError(t, os.WriteFile(exe, data, 0700))
			require.NoError(t, os.Chtimes(exe, info.ModTime().Add(2*time.Second), info.ModTime().Add(2*time.Second)))
			problem = c.VerifyIfChanged()
			require.NotNil(t, problem)
			require.Equal(t, StatusDamaged, problem.Status)
			require.Equal(t, "soffice fingerprint mismatch", problem.Reason)
		})
	}
}

func TestComponentWriterExclusion_AncestorDescendantSiblingAndAlias(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "component")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "inside"), 0700))
	alias := filepath.Join(base, "alias")
	require.NoError(t, os.Symlink(root, alias))
	cases := []struct {
		name, path string
		overlap    bool
	}{
		{"same", root, true}, {"ancestor", base, true}, {"descendant", filepath.Join(root, "inside"), true},
		{"alias", alias, true}, {"sibling", filepath.Join(base, "component-other"), false}, {"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FirstOverlap(root, []WriteRoot{{Path: tc.path, Grant: "another agent workspace"}})
			if tc.overlap {
				require.Equal(t, &WriteRoot{Path: tc.path, Grant: "another agent workspace"}, got)
			} else {
				require.Nil(t, got)
			}
		})
	}
}
