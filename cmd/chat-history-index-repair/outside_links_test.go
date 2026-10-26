package main

import (
	"encoding/json"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// A real install keeps legitimate symbolic links outside sessions/ (a
// workspace's link to the user's Desktop, browser profile Singleton* links,
// toolchain .bin links). The repair command must record them as links — never
// follow or refuse them — while sessions/ keeps its strict refusal. Expected
// values below come from the fix brief (R1-R5), not from the implementation.

// repairOutsideLinks is the independent oracle: relative link path -> exact
// target string handed to os.Symlink.
type repairOutsideLinks map[string]string

// repairAddOutsideLinks plants links outside sessions/: a directory link and a
// file link pointing OUTSIDE the root, a relative directory link inside the
// root and a dangling Chrome-style lock link. Each directory behind a link holds
// a file that would appear in the inventory if the link were ever traversed.
func repairAddOutsideLinks(t *testing.T, root string) (repairOutsideLinks, string) {
	t.Helper()
	outside, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(outside, "behind-the-link.txt"), []byte("never inventoried"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "post.md"), []byte("user blog post"), 0o600))
	for _, dir := range []string{"workspaces/w1/work", "browser/profiles/p1", "sitefiles"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o700))
	}
	require.NoError(t, os.WriteFile(filepath.Join(root, "sitefiles", "index.html"), []byte("<p>site</p>"), 0o600))
	links := repairOutsideLinks{
		"workspaces/w1/work/Desktop":        outside,
		"workspaces/w1/work/blog":           filepath.Join(outside, "post.md"),
		"workspaces/w1/work/site":           filepath.Join("..", "..", "..", "sitefiles"),
		"browser/profiles/p1/SingletonLock": "omnipus-host-4242",
	}
	for rel, target := range links {
		require.NoError(t, os.Symlink(target, filepath.Join(root, filepath.FromSlash(rel))))
	}
	return links, outside
}

// repairMakeSocket creates a real AF_UNIX socket file at path. It binds in a
// short temporary directory (socket paths are length-limited) and renames the
// socket into place, keeping the file after the listener closes.
func repairMakeSocket(t *testing.T, path string) {
	t.Helper()
	short, err := os.MkdirTemp("", "hs")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(short)) })
	bound := filepath.Join(short, "s")
	l, err := net.Listen("unix", bound)
	require.NoError(t, err, "real AF_UNIX socket creation on %s", runtime.GOOS)
	unixListener, ok := l.(*net.UnixListener)
	require.True(t, ok)
	unixListener.SetUnlinkOnClose(false)
	require.NoError(t, os.Rename(bound, path))
	require.NoError(t, l.Close())
	info, err := os.Lstat(path)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&fs.ModeSocket, "instrument: fixture really is a socket")
}

// repairCopyPreservingLinks is a test-side full backup like `cp -a`: links are
// recreated with the same target string (never followed), special files are
// recreated with the same kind, and every entry's modification time is copied.
func repairCopyPreservingLinks(t *testing.T, src, dst string) {
	t.Helper()
	var dirs []string
	require.NoError(t, filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			dirs = append(dirs, rel)
			return os.MkdirAll(target, mode.Perm())
		case mode.IsRegular():
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err = os.WriteFile(target, b, mode.Perm()); err != nil {
				return err
			}
			return os.Chtimes(target, info.ModTime(), info.ModTime())
		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err = os.Symlink(link, target); err != nil {
				return err
			}
		case mode&fs.ModeSocket != 0:
			repairMakeSocket(t, target)
		case mode&fs.ModeNamedPipe != 0:
			repairMakeFIFO(t, target)
		default:
			t.Fatalf("fixture copy has no recipe for %s (%s)", rel, mode)
		}
		repairSetLinkTimes(t, target, info.ModTime())
		return nil
	}))
	for i := len(dirs) - 1; i >= 0; i-- {
		info, err := os.Stat(filepath.Join(src, dirs[i]))
		require.NoError(t, err)
		require.NoError(t, os.Chtimes(filepath.Join(dst, dirs[i]), info.ModTime(), info.ModTime()))
	}
}

func repairManifestEntries(t *testing.T, manifest string) (map[string]any, map[string]map[string]any) {
	t.Helper()
	b, err := os.ReadFile(manifest)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	raw, ok := m["entries"].([]any)
	require.True(t, ok)
	byPath := map[string]map[string]any{}
	for _, r := range raw {
		e, ok := r.(map[string]any)
		require.True(t, ok)
		p, ok := e["path"].(string)
		require.True(t, ok)
		byPath[p] = e
	}
	return m, byPath
}

func repairEntryPathsUnder(byPath map[string]map[string]any, prefix string) []string {
	out := []string{}
	for p := range byPath {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func repairLinkManifest(t *testing.T, binary, source string) (string, string) {
	t.Helper()
	backup, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repairCopyPreservingLinks(t, source, backup)
	manifest := filepath.Join(t.TempDir(), "backup-manifest.json")
	beforeSource, beforeBackup := repairTree(t, source), repairTree(t, backup)
	out := repairRun(t, binary, 0, "manifest", "--data-root", source, "--backup-root", backup, "--output", manifest)
	require.Contains(t, out, "verified=1")
	require.Equal(t, beforeSource, repairTree(t, source), "manifest writes nothing in the source")
	require.Equal(t, beforeBackup, repairTree(t, backup), "manifest writes nothing in the backup")
	return backup, manifest
}

// R8a / R1 / R5: links outside sessions/ are recorded with their exact target
// string, never followed (nothing behind them is inventoried) and never refused.
func TestHistoryRepairCommand_OutsideSessionLinksRecordedNotFollowed(t *testing.T) {
	binary := repairBinary(t)
	source := repairCanonicalFixture(t)
	links, _ := repairAddOutsideLinks(t, source)
	before := repairTree(t, source)
	out := repairRun(t, binary, 0, "dry-run", "--data-root", source)
	require.Contains(t, out, "mode=dry-run sessions=1 ")
	require.Contains(t, out, "groups=40")
	require.Contains(t, out, "refused=0")
	require.NotContains(t, out, "unsafe_kind")
	require.Equal(t, before, repairTree(t, source), "dry-run stays read-only with links present")

	backup, manifest := repairLinkManifest(t, binary, source)
	m, byPath := repairManifestEntries(t, manifest)
	require.EqualValues(t, 2, m["schema_version"], "format change (link/special kinds) bumps the manifest version")
	for rel, target := range links {
		e, ok := byPath[rel]
		require.True(t, ok, "link %s is inventoried", rel)
		info, err := os.Lstat(filepath.Join(source, filepath.FromSlash(rel)))
		require.NoError(t, err)
		require.Equal(t, "symlink", e["kind"], rel)
		require.Equal(t, target, e["link_target"], "exact link target string, never resolved: %s", rel)
		require.EqualValues(t, 0, e["size"], rel)
		require.NotContains(t, e, "sha256", "a link is never opened or hashed: %s", rel)
		require.NotContains(t, e, "special_type", rel)
		require.EqualValues(t, uint32(info.Mode().Perm()), e["mode"], rel)
		require.Equal(t, strconv.FormatInt(info.ModTime().UnixNano(), 10), e["mtime_unix_ns"], "lstat time of the link itself: %s", rel)
	}
	require.Equal(t, []string{"workspaces", "workspaces/w1", "workspaces/w1/work", "workspaces/w1/work/Desktop", "workspaces/w1/work/blog", "workspaces/w1/work/site"}, repairEntryPathsUnder(byPath, "workspaces"), "directory links are not walked into")
	require.Equal(t, []string{"browser", "browser/profiles", "browser/profiles/p1", "browser/profiles/p1/SingletonLock"}, repairEntryPathsUnder(byPath, "browser"))
	require.Equal(t, []string{"sitefiles", "sitefiles/index.html"}, repairEntryPathsUnder(byPath, "sitefiles"), "the real directory is inventoried once, under its own name")
	repairRun(t, binary, 0, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
}

// R8b / R2: a socket outside sessions/ is recorded as a special entry, not
// opened and not refused.
func TestHistoryRepairCommand_OutsideSessionSocketRecorded(t *testing.T) {
	binary := repairBinary(t)
	source := repairCanonicalFixture(t)
	require.NoError(t, os.MkdirAll(filepath.Join(source, "workspaces", "w1"), 0o700))
	repairMakeSocket(t, filepath.Join(source, "workspaces", "w1", "agent.sock"))
	out := repairRun(t, binary, 0, "dry-run", "--data-root", source)
	require.Contains(t, out, "refused=0")
	backup, manifest := repairLinkManifest(t, binary, source)
	_, byPath := repairManifestEntries(t, manifest)
	e, ok := byPath["workspaces/w1/agent.sock"]
	require.True(t, ok)
	require.Equal(t, "special", e["kind"])
	require.Equal(t, "socket", e["special_type"])
	require.EqualValues(t, 0, e["size"])
	require.NotContains(t, e, "sha256")
	require.NotContains(t, e, "link_target")
	repairRun(t, binary, 0, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
}

// R8c / R3: inside sessions/ the strict refusal is unchanged.
func TestHistoryRepairCommand_SessionsSubtreeLinksStillRefused(t *testing.T) {
	binary := repairBinary(t)
	for _, tc := range []struct{ name, rel string }{{"inside_session", "notes-link"}, {"session_alias", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			source, sid := repairMissingIndexFixture(t)
			outside, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			if tc.rel == "" {
				require.NoError(t, os.Symlink(filepath.Join(source, "sessions", sid), filepath.Join(source, "sessions", "alias")))
			} else {
				require.NoError(t, os.Symlink(outside, filepath.Join(source, "sessions", sid, tc.rel)))
			}
			before := repairTree(t, source)
			out := repairRun(t, binary, 2, "dry-run", "--data-root", source)
			require.Contains(t, out, "class=unsafe_kind")
			backup, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			manifest := filepath.Join(t.TempDir(), "backup-manifest.json")
			out = repairRun(t, binary, 2, "manifest", "--data-root", source, "--backup-root", backup, "--output", manifest)
			require.Contains(t, out, "mode=manifest class=unsafe_kind")
			repairNoEvidence(t, manifest)
			require.Equal(t, before, repairTree(t, source))
		})
	}
}

// R8d / R4: the backup must hold every link as a link with the identical
// target, and drift on either side or in the manifest is refused.
func TestHistoryRepairCommand_LinkBackupMismatchRefused(t *testing.T) {
	binary := repairBinary(t)
	const desktop = "workspaces/w1/work/Desktop"
	const blog = "workspaces/w1/work/blog"
	breakBackup := func(t *testing.T, backup, fault string) {
		t.Helper()
		link := filepath.Join(backup, filepath.FromSlash(desktop))
		info, err := os.Lstat(link)
		require.NoError(t, err)
		switch fault {
		case "link_missing":
			require.NoError(t, os.Remove(link))
		case "target_differs":
			require.NoError(t, os.Remove(link))
			require.NoError(t, os.Symlink(filepath.Join(backup, "elsewhere"), link))
			repairSetLinkTimes(t, link, info.ModTime())
		case "replaced_by_file":
			require.NoError(t, os.Remove(link))
			require.NoError(t, os.WriteFile(link, []byte("not a link"), 0o600))
			require.NoError(t, os.Chtimes(link, info.ModTime(), info.ModTime()))
		case "replaced_by_directory":
			require.NoError(t, os.Remove(link))
			require.NoError(t, os.Mkdir(link, 0o700))
		case "socket_replaced_by_file":
			sock := filepath.Join(backup, "workspaces", "w1", "agent.sock")
			info, err = os.Lstat(sock)
			require.NoError(t, err)
			require.NoError(t, os.Remove(sock))
			require.NoError(t, os.WriteFile(sock, nil, 0o600))
			require.NoError(t, os.Chtimes(sock, info.ModTime(), info.ModTime()))
		}
	}
	faults := []string{"link_missing", "target_differs", "replaced_by_file", "replaced_by_directory", "socket_replaced_by_file"}
	fixture := func(t *testing.T) string {
		source := repairCanonicalFixture(t)
		repairAddOutsideLinks(t, source)
		repairMakeSocket(t, filepath.Join(source, "workspaces", "w1", "agent.sock"))
		return source
	}
	for _, fault := range faults {
		t.Run("manifest_"+fault, func(t *testing.T) {
			source := fixture(t)
			backup, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			repairCopyPreservingLinks(t, source, backup)
			breakBackup(t, backup, fault)
			manifest := filepath.Join(t.TempDir(), "backup-manifest.json")
			out := repairRun(t, binary, 2, "manifest", "--data-root", source, "--backup-root", backup, "--output", manifest)
			require.Contains(t, out, "mode=manifest class=backup_incomplete")
			repairNoEvidence(t, manifest)
		})
		t.Run("verify_"+fault, func(t *testing.T) {
			source := fixture(t)
			backup, manifest := repairLinkManifest(t, binary, source)
			breakBackup(t, backup, fault)
			out := repairRun(t, binary, 2, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
			require.Contains(t, out, "class=backup_tampered")
		})
	}
	t.Run("verify_source_link_retargeted", func(t *testing.T) {
		source := fixture(t)
		backup, manifest := repairLinkManifest(t, binary, source)
		link := filepath.Join(source, filepath.FromSlash(blog))
		info, err := os.Lstat(link)
		require.NoError(t, err)
		require.NoError(t, os.Remove(link))
		require.NoError(t, os.Symlink(filepath.Join(source, "sitefiles", "index.html"), link))
		repairSetLinkTimes(t, link, info.ModTime())
		out := repairRun(t, binary, 2, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
		require.Contains(t, out, "class=source_changed")
	})
	t.Run("verify_manifest_target_edited", func(t *testing.T) {
		source := fixture(t)
		backup, manifest := repairLinkManifest(t, binary, source)
		b, err := os.ReadFile(manifest)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		edited := false
		for _, raw := range m["entries"].([]any) {
			e := raw.(map[string]any)
			if e["path"] == blog {
				e["link_target"] = "/somewhere/else"
				edited = true
			}
		}
		require.True(t, edited, "instrument: the link entry exists to edit")
		b, err = json.Marshal(m)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(manifest, b, 0o600))
		out := repairRun(t, binary, 2, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
		require.Contains(t, out, "class=backup_tampered", "an edited link target in the manifest is refused")
	})
}

// R8e / R6: with an identical link-preserving backup, the full guarded apply
// publishes the index and leaves every link (target, lstat mode and mtime) and
// everything behind it untouched.
func TestHistoryRepairCommand_GuardedApplyPreservesOutsideLinks(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	root, sid := repairMissingIndexFixture(t)
	links, outside := repairAddOutsideLinks(t, root)
	type linkFact struct { // not-wire-format: test-only lstat oracle.
		mode   fs.FileMode
		mtime  int64
		target string
	}
	lstatFacts := func(base string) map[string]linkFact {
		out := map[string]linkFact{}
		for rel := range links {
			info, err := os.Lstat(filepath.Join(base, filepath.FromSlash(rel)))
			require.NoError(t, err)
			target, err := os.Readlink(filepath.Join(base, filepath.FromSlash(rel)))
			require.NoError(t, err)
			require.Equal(t, links[rel], target)
			out[rel] = linkFact{mode: info.Mode(), mtime: info.ModTime().UnixNano(), target: target}
		}
		return out
	}
	child := repairStartNative(t, helper, root)
	binding, identity := repairCapture(t, tool, helper, root, child)
	repairStopNative(t, child, "stop", 0)
	backup, manifest := repairLinkManifest(t, tool, root)
	receipt := repairProduceReceipt(t, tool, root, binding, identity, "absent")
	sourceLinks, backupLinks, outsideBefore := lstatFacts(root), lstatFacts(backup), repairTree(t, outside)
	canonical, backupTree := repairCanonicalFacts(t, root), repairTree(t, backup)

	out := repairRun(t, tool, 0, "apply", "--data-root", root, "--backup-root", backup, "--backup-manifest", manifest, "--instance-binding", binding, "--stopped-receipt", receipt, "--confirm-stopped")
	repairAssertPublicationCounts(t, out, 1, 0, 0)
	require.NoError(t, session.VerifyChatHistoryIndex(t.Context(), filepath.Join(root, "sessions", sid)), "index really published")
	require.Equal(t, sourceLinks, lstatFacts(root), "every source link keeps its target, lstat mode and mtime")
	require.Equal(t, backupLinks, lstatFacts(backup))
	require.Equal(t, outsideBefore, repairTree(t, outside), "nothing behind a link is touched")
	require.Equal(t, canonical, repairCanonicalFacts(t, root), "only the derived index subtree is written")
	require.Equal(t, backupTree, repairTree(t, backup), "backup is immutable")
	repairRun(t, tool, 0, "verify", "--data-root", root, "--backup-root", backup, "--backup-manifest", manifest)
}

// R4: the canonical fingerprint covers a link's target and a special file's
// type. Frozen vectors computed independently with Python hashlib + struct
// ('>Q' length prefix per value): (path, kind, size, sha256, mtime) per entry,
// directory mtime empty, then link_target for "symlink" / special_type for
// "special".
func TestHistoryRepairCommand_FingerprintCoversLinkTargetAndSpecialType(t *testing.T) {
	entries := func(target string) []inventoryEntry {
		return []inventoryEntry{
			{Path: "a", Kind: "directory", Mtime: "999", Mode: 0o700},
			{Path: "a/l", Kind: "symlink", LinkTarget: target, Mtime: "123", Mode: 0o755},
			{Path: "s", Kind: "special", SpecialType: "socket", Mtime: "-5", Mode: 0o755},
		}
	}
	require.Equal(t, "d7e49ae673b2a45d8f66b1c0fe0249e238069244f7f55d5e3c71d83ddcd213c0", canonicalFingerprint(entries("../t")))
	require.Equal(t, "173142a5f2bf9c8a2363dd21f08082e0d86eefc71d28288fe814281328476380", canonicalFingerprint(entries("../u")), "a retargeted link changes the fingerprint")
}

// R4: the format change bumped the manifest version; a version-1 manifest is
// refused by verify (and therefore apply) rather than read with old semantics.
func TestHistoryRepairCommand_Version1ManifestRefused(t *testing.T) {
	binary := repairBinary(t)
	source := repairCanonicalFixture(t)
	backup, manifest := repairLinkManifest(t, binary, source)
	m, _ := repairManifestEntries(t, manifest)
	m["schema_version"] = 1
	b, err := json.Marshal(m)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(manifest, b, 0o600))
	out := repairRun(t, binary, 2, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
	require.Contains(t, out, "class=unsupported_manifest_version")
}
