package gateway

// RED — w5-integration claim 6: the product's own backups and archives never
// carry or resurrect cache or watcher state, and a restore cannot bring one
// back.
//
// Oracles (derived from the spec BEFORE reading the implementation;
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-uat/receipts/w5-red-test-plan.md):
//   - w5 spec US-4.2/B-14/E3: "excluded current, temporary and retired Mail
//     files never appear"; E3 requires MEMBER AND CONTENT inspection —
//     "Cache, watcher state and repository-object sensitive markers are
//     absent; ordinary state and a saved workspace-file control are
//     present"; B-14: "exclusion covers more than the current directory
//     name".
//   - w5 spec US-4.2/B-15/E4/MC-7: a hostile/old archive restores NO
//     protected Mail file or repository-history bypass; ordinary controls
//     restore normally.
//   - Namespace names are the spec's own: the cache layout is
//     "data root → mail-cache → opaque pair ID → folders.enc" (US-3.1) and
//     the watcher-state namespace is `email-watch/` (w6-proof MC-P18,
//     founder Q-B=A).
//
// Mutations this pack must kill (check-integration-report.md §2):
//   - M9: the backup's exclusion case never matches — the archive carries
//     mail-cache/, email-watch/ and the data root's own .git again (the
//     existing backup test plants no sensitive fixtures, so nothing died).
//   - M10: restore re-materializes protected namespaces (skip disabled).

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	backupMarkerCache   = "MAILCACHE-SENSITIVE-MARKER-P4F"
	backupMarkerWatch   = "EMAILWATCH-SENSITIVE-MARKER-K8D"
	backupMarkerHistory = "GIT-HISTORY-BYPASS-MARKER-Z2R"
	backupControlFile   = "agents.json"
)

// plantBackupFixtures lays down the E3 fixture set: current/temp/retired
// cache state, watcher state, a repository-object history bypass, and the
// ordinary-state positive control. Returns the planted relative paths.
func plantBackupFixtures(t *testing.T, root string) []string {
	t.Helper()
	files := map[string]string{
		// Current + temp + retired cache state (US-4.2 covers all three).
		filepath.Join("mail-cache", "pairid-current", "folders.enc"):      backupMarkerCache,
		filepath.Join("mail-cache", "pairid-current", "folders.enc.tmp"):  backupMarkerCache,
		filepath.Join("mail-cache", "pairid-retired-0001", "folders.enc"): backupMarkerCache,
		// Watcher state: current and retired (founder Q-B=A).
		filepath.Join("email-watch", "agent-ws.json"):      backupMarkerWatch,
		filepath.Join("email-watch", "retired-agent.json"): backupMarkerWatch,
		// Repository metadata that could carry the cache's history (MC-7).
		filepath.Join(".git", "objects", "ab", "cdef0123"): backupMarkerHistory,
		// Ordinary state: the positive control the same instrument MUST find.
		backupControlFile: `{"agents":[]}`,
	}
	relPaths := make([]string, 0, len(files))
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o700))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o600))
		relPaths = append(relPaths, rel)
	}
	return relPaths
}

// archiveMembers walks a .tar.gz and returns every member's path plus its
// content concatenated — the E3 instrument (member AND content listing).
func archiveMembers(t *testing.T, archivePath string) (names []string, allContent string) {
	t.Helper()
	f, err := os.Open(archivePath)
	require.NoError(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	require.NoError(t, err)
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return names, allContent
		}
		require.NoError(t, err)
		names = append(names, hdr.Name)
		if hdr.Typeflag == tar.TypeReg {
			b, err := io.ReadAll(tr)
			require.NoError(t, err)
			allContent += string(b)
		}
	}
}

func assertNoProtectedMembers(t *testing.T, names []string, allContent string, where string) {
	t.Helper()
	for _, name := range names {
		top := strings.SplitN(name, "/", 2)[0]
		switch top {
		case "mail-cache", "email-watch", ".git":
			t.Fatalf("US-4.2/E3: %s carries protected Mail/version-control member %q", where, name)
		}
	}
	for _, marker := range []string{backupMarkerCache, backupMarkerWatch, backupMarkerHistory} {
		if strings.Contains(allContent, marker) {
			t.Fatalf("US-4.2/E3: %s carries a sensitive marker in member CONTENT (repository-object/history bypass or body text): %q", where, marker)
		}
	}
	foundControl := false
	for _, name := range names {
		if name == backupControlFile {
			foundControl = true
		}
	}
	if !foundControl {
		t.Fatalf("US-4.2/E3: %s lost the ordinary-state control %q — an exclusion that eats ordinary state is not the spec's exclusion", where, backupControlFile)
	}
}

func TestCreateBackupArchive_ExcludesMailNamespaces_KeepsControl(t *testing.T) {
	root := t.TempDir()
	plantBackupFixtures(t, root)

	dest := filepath.Join(t.TempDir(), "backup.tar.gz")
	require.NoError(t, createTarGz(root, dest))

	names, allContent := archiveMembers(t, dest)
	if len(names) == 0 {
		t.Fatal("instrument error: the archive is empty — the fixture was not planted or the walker failed")
	}
	assertNoProtectedMembers(t, names, allContent, "the application backup archive")
}

func TestRestore_NeverRematerializesProtectedNamespaces(t *testing.T) {
	// A hostile/old archive (E4): hand-crafted, because the product's own
	// backup refuses to carry these members — the threat model is an archive
	// from before the exclusion existed, or written by something else.
	src := t.TempDir()
	hostile := map[string]string{
		filepath.Join("mail-cache", "pairid-old", "folders.enc"): backupMarkerCache,
		filepath.Join("email-watch", "old-agent-ws.json"):        backupMarkerWatch,
		filepath.Join(".git", "objects", "cd", "beef4567"):       backupMarkerHistory,
		backupControlFile: `{"agents":[]}`,
	}
	for rel, content := range hostile {
		abs := filepath.Join(src, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o700))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o600))
	}
	archive := filepath.Join(t.TempDir(), "hostile.tar.gz")
	require.NoError(t, createTarGzRaw(src, archive))

	dest := t.TempDir()
	require.NoError(t, extractTarGz(archive, dest))

	for _, rel := range []string{
		filepath.Join("mail-cache", "pairid-old", "folders.enc"),
		filepath.Join("email-watch", "old-agent-ws.json"),
		filepath.Join(".git", "objects", "cd", "beef4567"),
	} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err == nil {
			t.Fatalf("US-4.2/B-15/E4: restoring a hostile archive re-materialized protected Mail state %q — restore must never resurrect cache, watcher or history-bypass files", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, backupControlFile)); err != nil {
		t.Fatalf("US-4.2/E4: the ordinary-state control must restore normally (stat err=%v)", err)
	}
}

// createTarGzRaw builds an archive with the standard library directly —
// the hostile fixture MUST contain the protected members, which the
// product's own walker would exclude.
func createTarGzRaw(srcDir, destPath string) error {
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	walkErr := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if hdrErr := tw.WriteHeader(hdr); hdrErr != nil {
			return hdrErr
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(tw, src)
		return err
	})
	if walkErr != nil {
		return walkErr
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return out.Close()
}
