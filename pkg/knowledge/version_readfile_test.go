// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// version_readfile_test.go — ReadFileVersion (EMB-007a), the exported
// streaming sibling of ReadNoteVersion for a file that has no enclosing
// *Collection: a Library file outside every knowledge base (G1d), or a
// Library file read through the download/content doors rather than the
// collection API (G1a/G1b/G1c).
//
// Founder ruling (A-11, closing the only open ambiguity in revision 2 of
// docs/internal/specs/adr-083-embedded-content-spec.md): "ONE token, and it
// is the EXISTING one." Both doors — the knowledge-collection path and the
// plain-file Library path — MUST produce a byte-identical token for the same
// file. The test below is the direct proof of that: it is not "ReadFileVersion
// works", it is "ReadFileVersion and ReadNoteVersion agree", and it would
// fail immediately if a future edit gave ReadFileVersion its own hashing
// logic instead of delegating to readNoteVersionAbs.

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/library"
)

// ---------------------------------------------------------------------------
// The whole point: one identity, not two.
// ---------------------------------------------------------------------------

// TestReadFileVersion_MatchesReadNoteVersion_SameFileInsideCollectionAndAsPlainPath
// is EMB-007a's required equivalence check. The SAME on-disk file is read
// through both doors: as a collection-relative note (ReadNoteVersion) and as
// a plain absolute path (ReadFileVersion, with no *Collection in sight). If
// the two ever computed different tokens for the same bytes, a save through
// one door could never clear a conflict raised by the other — "an
// unclearable 409" the spec calls out by name (order-table row 121).
//
// Non-vacuity: this test would fail (Token mismatch) if ReadFileVersion were
// reimplemented with its own hashing instead of delegating to
// readNoteVersionAbs — e.g. a naive "hash the bytes plus the mtime" or a
// digest that dropped the length-suffix domain separation
// (finishVersionToken). It would also fail to compile at all if
// ReadFileVersion were deleted, which is the strongest possible non-vacuity
// for "this function must exist and be exported".
func TestReadFileVersion_MatchesReadNoteVersion_SameFileInsideCollectionAndAsPlainPath(t *testing.T) {
	t.Parallel()

	const rel = "projects/plan.md"
	const body = "# Plan\n\nsame bytes, read through two doors\n"
	f := newWriteFixture(t, map[string]string{rel: body})

	viaCollection, err := ReadNoteVersion(f.col, rel)
	if err != nil {
		t.Fatalf("ReadNoteVersion: %v", err)
	}

	abs := filepath.Join(f.col.Root(), filepath.FromSlash(rel))
	viaPlainPath, err := ReadFileVersion(abs)
	if err != nil {
		t.Fatalf("ReadFileVersion: %v", err)
	}

	// The load-bearing assertion: identical content yields an identical
	// token whichever door computed it.
	if viaCollection.Token != viaPlainPath.Token {
		t.Fatalf("two token definitions disagree on the same file: ReadNoteVersion=%q, ReadFileVersion=%q — "+
			"this is exactly the unclearable-409 failure EMB-007a exists to prevent",
			viaCollection.Token, viaPlainPath.Token)
	}
	if viaCollection.Token != ComputeVersionToken([]byte(body)) {
		t.Errorf("ReadNoteVersion token = %q, want the content hash %q", viaCollection.Token, ComputeVersionToken([]byte(body)))
	}
	if viaPlainPath.Token != ComputeVersionToken([]byte(body)) {
		t.Errorf("ReadFileVersion token = %q, want the content hash %q", viaPlainPath.Token, ComputeVersionToken([]byte(body)))
	}

	// Every other value-carrying field agrees too — Size and ModTime are
	// properties of the file, not of which door read it.
	if viaCollection.Exists != viaPlainPath.Exists {
		t.Errorf("Exists disagrees: ReadNoteVersion=%v, ReadFileVersion=%v", viaCollection.Exists, viaPlainPath.Exists)
	}
	if viaCollection.Size != viaPlainPath.Size {
		t.Errorf("Size disagrees: ReadNoteVersion=%d, ReadFileVersion=%d", viaCollection.Size, viaPlainPath.Size)
	}
	if !viaCollection.ModTime.Equal(viaPlainPath.ModTime) {
		t.Errorf("ModTime disagrees: ReadNoteVersion=%v, ReadFileVersion=%v", viaCollection.ModTime, viaPlainPath.ModTime)
	}

	// Path is the ONE field that legitimately differs — one door only ever
	// has a collection-relative path, the other only ever has an absolute
	// one. Pinned explicitly so the difference is a documented design
	// choice, not an unnoticed gap.
	if viaCollection.Path != rel {
		t.Errorf("ReadNoteVersion Path = %q, want the collection-relative %q", viaCollection.Path, rel)
	}
	if viaPlainPath.Path != abs {
		t.Errorf("ReadFileVersion Path = %q, want the absolute %q", viaPlainPath.Path, abs)
	}

	// A write through the Writer and a subsequent read through the plain-path
	// door must keep agreeing after the content changes, not just at one
	// snapshot in time.
	const edited = "# Plan\n\nedited through the Writer\n"
	res, err := f.writer.WriteNote(WriteRequest{
		Path:            rel,
		Content:         []byte(edited),
		ExpectedVersion: viaCollection.Token,
	})
	if err != nil {
		t.Fatalf("WriteNote: %v", err)
	}
	afterEdit, err := ReadFileVersion(abs)
	if err != nil {
		t.Fatalf("ReadFileVersion after edit: %v", err)
	}
	if afterEdit.Token != res.Version {
		t.Errorf("post-edit ReadFileVersion token = %q, want the Writer's own reported version %q", afterEdit.Token, res.Version)
	}
}

// ---------------------------------------------------------------------------
// Value semantics parity with ReadNoteVersion
// ---------------------------------------------------------------------------

func TestReadFileVersion_AbsentFileHasAbsentToken(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	abs := filepath.Join(dir, "does-not-exist.md")

	v, err := ReadFileVersion(abs)
	if err != nil {
		t.Fatalf("ReadFileVersion on a missing file must not error (create is a CAS too): %v", err)
	}
	if v.Exists {
		t.Error("a missing file reported Exists=true")
	}
	if v.Token != TokenAbsent {
		t.Errorf("missing file token = %q, want %q", v.Token, TokenAbsent)
	}
}

func TestReadFileVersion_RefusesRelativePath(t *testing.T) {
	t.Parallel()

	for _, rel := range []string{"note.md", "./note.md", "sub/note.md", ""} {
		if _, err := ReadFileVersion(rel); !errors.Is(err, ErrNotAbsolutePath) {
			t.Errorf("ReadFileVersion(%q) error = %v, want ErrNotAbsolutePath", rel, err)
		}
	}
}

func TestReadFileVersion_RefusesDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if _, err := ReadFileVersion(dir); !errors.Is(err, ErrNotRegularFile) {
		t.Errorf("ReadFileVersion(directory) error = %v, want ErrNotRegularFile", err)
	}
}

func TestReadFileVersion_RefusesSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privilege on Windows")
	}
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "real.md")
	if err := os.WriteFile(target, []byte("body"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}

	if _, err := ReadFileVersion(link); !errors.Is(err, ErrNotRegularFile) {
		t.Errorf("ReadFileVersion(symlink) error = %v, want ErrNotRegularFile (parity with ReadNoteVersion/FR-044)", err)
	}
}

// ---------------------------------------------------------------------------
// G1a/G1b — a token is produced even when library.ContentResult has nothing
// to hash (binary and too_large files).
// ---------------------------------------------------------------------------

// TestReadFileVersion_BinaryFile_ProducesNonDegenerateToken is spec dataset
// G1a. library.ContentResult.Content is empty by design for a binary file
// (content.go's ReadContent, "Binary content ... is reported with
// Content == "" and IsText == false"). A token derived from that empty
// string would be IDENTICAL for every binary file in the workspace — the
// exact failure EMB-007a names by name. ReadFileVersion must not go anywhere
// near ContentResult.Content; it reads the file's own bytes.
//
// Non-vacuity: the test proves this by computing what the wrong
// implementation WOULD produce (ComputeVersionToken of the empty
// ContentResult.Content) and asserting the real token is different — twice,
// against two distinct binary files that would otherwise collide.
func TestReadFileVersion_BinaryFile_ProducesNonDegenerateToken(t *testing.T) {
	t.Parallel()

	root := newTestLibraryRoot(t)

	binA := append([]byte{0x00, 0x01, 0x02, 0xFF, 0xFE}, []byte("payload A")...)
	binB := append([]byte{0x00, 0x01, 0x02, 0xFF, 0xFE}, []byte("payload B — different length too")...)

	writeLibraryFile(t, root, "images/a.bin", binA)
	writeLibraryFile(t, root, "images/b.bin", binB)

	resultA, err := root.ReadContent("images/a.bin")
	if err != nil {
		t.Fatalf("ReadContent(a.bin): %v", err)
	}
	if resultA.IsText || resultA.Content != "" {
		t.Fatalf("premise broken: a.bin was not read as binary (IsText=%v, Content=%q) — this test proves nothing about G1a",
			resultA.IsText, resultA.Content)
	}
	resultB, err := root.ReadContent("images/b.bin")
	if err != nil {
		t.Fatalf("ReadContent(b.bin): %v", err)
	}
	if resultB.IsText || resultB.Content != "" {
		t.Fatalf("premise broken: b.bin was not read as binary (IsText=%v, Content=%q)", resultB.IsText, resultB.Content)
	}

	tokenA, err := ReadFileVersion(root.HostPath("images/a.bin"))
	if err != nil {
		t.Fatalf("ReadFileVersion(a.bin): %v", err)
	}
	tokenB, err := ReadFileVersion(root.HostPath("images/b.bin"))
	if err != nil {
		t.Fatalf("ReadFileVersion(b.bin): %v", err)
	}

	if !tokenA.Exists || tokenA.Token == TokenAbsent {
		t.Fatalf("a.bin: ReadFileVersion reported Exists=%v Token=%q, want a real token", tokenA.Exists, tokenA.Token)
	}
	if !tokenB.Exists || tokenB.Token == TokenAbsent {
		t.Fatalf("b.bin: ReadFileVersion reported Exists=%v Token=%q, want a real token", tokenB.Exists, tokenB.Token)
	}

	// The canary: what a ContentResult.Content-derived token WOULD be —
	// identical for both files, because Content is "" for both.
	degenerate := ComputeVersionToken([]byte(resultA.Content))
	if degenerate != ComputeVersionToken([]byte(resultB.Content)) {
		t.Fatalf("test setup broken: the two ContentResult.Content values are not both empty, so the degenerate-token premise does not hold")
	}
	if tokenA.Token == degenerate {
		t.Error("a.bin's ReadFileVersion token equals the hash of an EMPTY string — it was computed from ContentResult.Content, not the file's real bytes")
	}
	if tokenB.Token == degenerate {
		t.Error("b.bin's ReadFileVersion token equals the hash of an EMPTY string — it was computed from ContentResult.Content, not the file's real bytes")
	}

	// The real correctness bar: matches the content hash, and two different
	// binaries get two different tokens.
	if tokenA.Token != ComputeVersionToken(binA) {
		t.Errorf("a.bin token = %q, want the content hash %q", tokenA.Token, ComputeVersionToken(binA))
	}
	if tokenB.Token != ComputeVersionToken(binB) {
		t.Errorf("b.bin token = %q, want the content hash %q", tokenB.Token, ComputeVersionToken(binB))
	}
	if tokenA.Token == tokenB.Token {
		t.Error("two different binary files produced the SAME token — the collision G1a warns about")
	}
}

// TestReadFileVersion_OversizedTextFile_ProducesToken is spec dataset G1b:
// the second producer of an empty ContentResult.Content. A text file over
// library.MaxContentBytes is reported TooLarge with Content omitted
// (content.go's ReadContent), so the same "nothing to hash" gap applies on
// the too_large path, not only the binary one.
func TestReadFileVersion_OversizedTextFile_ProducesToken(t *testing.T) {
	t.Parallel()

	root := newTestLibraryRoot(t)

	big := strings.Repeat("a", library.MaxContentBytes+1024)
	writeLibraryFile(t, root, "notes/huge.txt", []byte(big))

	result, err := root.ReadContent("notes/huge.txt")
	if err != nil {
		t.Fatalf("ReadContent(huge.txt): %v", err)
	}
	if !result.TooLarge || result.Content != "" {
		t.Fatalf("premise broken: huge.txt was not reported too_large with omitted content (TooLarge=%v, len(Content)=%d) — this test proves nothing about G1b",
			result.TooLarge, len(result.Content))
	}

	version, err := ReadFileVersion(root.HostPath("notes/huge.txt"))
	if err != nil {
		t.Fatalf("ReadFileVersion(huge.txt): %v", err)
	}
	if !version.Exists || version.Token == TokenAbsent {
		t.Fatalf("huge.txt: ReadFileVersion reported Exists=%v Token=%q, want a real token", version.Exists, version.Token)
	}
	wantToken := ComputeVersionToken([]byte(big))
	if version.Token != wantToken {
		t.Errorf("huge.txt token = %q, want the content hash %q", version.Token, wantToken)
	}
	if version.Token == ComputeVersionToken([]byte(result.Content)) {
		t.Error("huge.txt's ReadFileVersion token equals the hash of the (empty) ContentResult.Content, not the real file bytes")
	}
}

// ---------------------------------------------------------------------------
// G1c/G1d harness — a plain library.Root, standing in for a Library file
// with no enclosing knowledge collection.
// ---------------------------------------------------------------------------

// newTestLibraryRoot opens a library.Root over a fresh temp directory,
// standing in for one workspace's work/ tree. pkg/knowledge already imports
// pkg/library (for library.CleanRelPath), so using the real, exported
// library.OpenRoot here — rather than reimplementing Library's own path
// safety — is the same direction of dependency this package already has.
func newTestLibraryRoot(t *testing.T) *library.Root {
	t.Helper()
	home := t.TempDir()
	root, err := library.OpenRoot(home, "ws-version-token-test")
	if err != nil {
		t.Fatalf("library.OpenRoot: %v", err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// writeLibraryFile creates rel's parent directories (WriteContent requires
// them to exist already) and writes content through the Library's own
// atomic-write path, so these tests exercise the exact bytes a real save
// would leave on disk.
func writeLibraryFile(t *testing.T, root *library.Root, rel string, content []byte) {
	t.Helper()
	abs := root.HostPath(rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		t.Fatalf("mkdir parent of %q: %v", rel, err)
	}
	if _, err := root.WriteContent(rel, content); err != nil {
		t.Fatalf("WriteContent(%q): %v", rel, err)
	}
}

// TestReadFileVersion_MatchesReadContentBytes_G1c is spec dataset G1c: the
// same file read through getLibraryContent's kind of door (ReadContent) and
// through downloadLibraryFile's kind of door (OpenFileForDownload) must
// yield byte-identical ETag values, and both must equal
// knowledge.ReadNoteVersion's Token for that note — i.e. one computation,
// reached three ways. This package cannot reach the REST handlers
// (pkg/gateway), but it can and must prove the shared primitive underneath
// both doors — ReadFileVersion — is deterministic over the SAME bytes
// however they were opened.
func TestReadFileVersion_MatchesReadContentBytes_G1c(t *testing.T) {
	t.Parallel()

	root := newTestLibraryRoot(t)
	const body = "# Report\n\ntext content readable through either door\n"
	writeLibraryFile(t, root, "reports/report.md", []byte(body))

	// Door 1: getLibraryContent's read (library.Root.ReadContent).
	contentResult, err := root.ReadContent("reports/report.md")
	if err != nil {
		t.Fatalf("ReadContent: %v", err)
	}
	if contentResult.Content != body {
		t.Fatalf("premise broken: ReadContent returned %q, want the file body", contentResult.Content)
	}

	// Door 2: downloadLibraryFile's read (library.Root.OpenFileForDownload),
	// which streams bytes rather than returning a string.
	f, fi, err := root.OpenFileForDownload("reports/report.md")
	if err != nil {
		t.Fatalf("OpenFileForDownload: %v", err)
	}
	downloaded := make([]byte, fi.Size())
	if _, err := f.Read(downloaded); err != nil {
		f.Close()
		t.Fatalf("read downloaded bytes: %v", err)
	}
	f.Close()
	if string(downloaded) != body {
		t.Fatalf("premise broken: OpenFileForDownload returned %q, want the file body", downloaded)
	}

	// Both doors' bytes must hash to the SAME token, and that token must be
	// exactly what ReadFileVersion (the shared, streamed computation)
	// reports for the file on disk.
	tokenFromContentDoor := ComputeVersionToken([]byte(contentResult.Content))
	tokenFromDownloadDoor := ComputeVersionToken(downloaded)
	if tokenFromContentDoor != tokenFromDownloadDoor {
		t.Fatalf("the two doors' bytes hash to different tokens: content=%q, download=%q", tokenFromContentDoor, tokenFromDownloadDoor)
	}

	streamed, err := ReadFileVersion(root.HostPath("reports/report.md"))
	if err != nil {
		t.Fatalf("ReadFileVersion: %v", err)
	}
	if streamed.Token != tokenFromContentDoor {
		t.Errorf("ReadFileVersion token = %q, want the doors' shared token %q", streamed.Token, tokenFromContentDoor)
	}
}
