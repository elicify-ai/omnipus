// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package library

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// --- CleanRelPath: structural (lexical) path-safety adversarial cases ---

func TestCleanRelPath_Adversarial(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
		wantRel string
	}{
		{"empty is root", "", false, ""},
		{"dot is root", ".", false, ""},
		{"plain relative", "notes/report.md", false, "notes/report.md"},
		{"deeply nested", "a/b/c/d/e/f/g.txt", false, "a/b/c/d/e/f/g.txt"},
		{"trailing slash cleaned", "notes/", false, "notes"},
		{"redundant dot segments", "notes/./report.md", false, "notes/report.md"},
		{"leading dotdot", "../etc/passwd", true, ""},
		{"embedded dotdot", "notes/../../etc/passwd", true, ""},
		{"bare dotdot", "..", true, ""},
		{"absolute unix", "/etc/passwd", true, ""},
		{"absolute with subpath", "/notes/report.md", true, ""},
		{"backslash windows-style", "notes\\report.md", true, ""},
		{"embedded NUL", "notes/report\x00.md", true, ""},
		{"double slash collapses", "notes//report.md", false, "notes/report.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := CleanRelPath(tc.raw)
			if tc.wantErr {
				require.Error(t, err, "raw=%q", tc.raw)
				assert.ErrorIs(t, err, ErrInvalidPath)
				return
			}
			require.NoError(t, err, "raw=%q", tc.raw)
			assert.Equal(t, tc.wantRel, rel)
		})
	}
}

// buildTestRoot opens a *Root at a fresh temp directory standing in for a
// workspace's work/ tree (bypassing workspace.SafeWorkDir's ID validation,
// which is irrelevant to what these tests exercise).
func buildTestRoot(t *testing.T) (*Root, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { root.Close() })
	return &Root{dir: dir, root: root}, dir
}

// --- Symlink escape: the adversarial case CleanRelPath CANNOT catch (it is
// lexically fine) — only os.Root's own runtime containment check can. ---

func TestRoot_SymlinkEscape_AbsoluteTarget_Rejected(t *testing.T) {
	r, dir := buildTestRoot(t)

	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.txt")
	require.NoError(t, os.WriteFile(secretPath, []byte("top secret"), 0o600))

	// A symlink INSIDE the root pointing to an ABSOLUTE path OUTSIDE it.
	linkPath := filepath.Join(dir, "escape")
	require.NoError(t, os.Symlink(outsideDir, linkPath))

	_, err := r.StatDir("escape")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot,
		"absolute-target symlink escape must be rejected as ErrOutsideRoot, got %v", err)

	_, err = r.ReadContent("escape/secret.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot)

	_, err = r.List("escape", false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot)
}

func TestRoot_SymlinkEscape_RelativeTarget_Rejected(t *testing.T) {
	r, dir := buildTestRoot(t)

	// A relative-target symlink that walks OUT of the root via "..".
	outsideDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("shh"), 0o600))

	rel, err := filepath.Rel(dir, outsideDir)
	require.NoError(t, err)
	linkPath := filepath.Join(dir, "escape2")
	require.NoError(t, os.Symlink(rel, linkPath))

	_, err = r.StatDir("escape2")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot, "relative-target symlink escape must be rejected, got %v", err)
}

func TestRoot_SymlinkEscape_ToSensitiveSiblingWorkspace_Rejected(t *testing.T) {
	// Simulates the highest-value real attack: a symlink inside workspace
	// A's work tree pointing at workspace B's directory (or the Omnipus home
	// itself, where master.key/credentials.json live).
	home := t.TempDir()
	workDirA := filepath.Join(home, "workspaces", "wsA", "work")
	require.NoError(t, os.MkdirAll(workDirA, 0o700))
	sensitiveDir := filepath.Join(home, "workspaces", "wsB")
	require.NoError(t, os.MkdirAll(sensitiveDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sensitiveDir, "AGENT.md"), []byte("wsB secrets"), 0o600))

	root, err := os.OpenRoot(workDirA)
	require.NoError(t, err)
	defer root.Close()
	r := &Root{dir: workDirA, root: root}

	require.NoError(t, os.Symlink(sensitiveDir, filepath.Join(workDirA, "peek")))

	_, err = r.List("peek", true)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrOutsideRoot)
}

// --- Non-adversarial CRUD smoke coverage for the primitives the REST layer
// builds on (deeper HTTP-level coverage lives in
// pkg/gateway/rest_library_test.go). ---

func TestRoot_WriteReadContent_RoundTrip(t *testing.T) {
	r, _ := buildTestRoot(t)

	// WriteContent's contract requires the parent directory to already exist
	// (LibraryContentRequest's documented "parent directory must already
	// exist" — this package has no mkdir-a-new-directory operation of its
	// own, matching the contract's actual operation set).
	require.NoError(t, r.root.MkdirAll("notes", 0o700))

	fi, err := r.WriteContent("notes/report.md", []byte("# Report\n"))
	require.NoError(t, err)
	assert.False(t, fi.IsDir())

	result, err := r.ReadContent("notes/report.md")
	require.NoError(t, err)
	assert.True(t, result.IsText)
	assert.False(t, result.TooLarge)
	assert.Equal(t, "# Report\n", result.Content)
}

func TestRoot_WriteContent_MissingParent_NotFound(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("nope/report.md", []byte("x"))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRoot_ReadContent_Binary_NoNulLie(t *testing.T) {
	r, _ := buildTestRoot(t)
	require.NoError(t, r.root.WriteFile("blob.bin", []byte{0x00, 0x01, 0x02, 0xff, 0xfe}, 0o600))

	result, err := r.ReadContent("blob.bin")
	require.NoError(t, err)
	assert.False(t, result.IsText)
	assert.Equal(t, "", result.Content)
}

func TestRoot_Delete_DirectoryRemovesContents(t *testing.T) {
	r, dir := buildTestRoot(t)
	require.NoError(t, r.root.MkdirAll("sub", 0o700))
	_, err := r.WriteContent("sub/a.txt", []byte("a"))
	require.NoError(t, err)
	_, err = r.WriteContent("sub/b.txt", []byte("b"))
	require.NoError(t, err)

	require.NoError(t, r.Delete("sub"))
	_, statErr := os.Stat(filepath.Join(dir, "sub"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestRoot_Rename_NoOpSameFromTo(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("a.txt", []byte("a"))
	require.NoError(t, err)
	fi, err := r.Rename("a.txt", "a.txt")
	require.NoError(t, err)
	assert.Equal(t, "a.txt", fi.Name())
}

func TestRoot_Rename_DestinationExists_Conflict(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("a.txt", []byte("a"))
	require.NoError(t, err)
	_, err = r.WriteContent("b.txt", []byte("b"))
	require.NoError(t, err)
	_, err = r.Rename("a.txt", "b.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

func TestCopyMoveInto_CrossRoot(t *testing.T) {
	src, _ := buildTestRoot(t)
	dst, _ := buildTestRoot(t)
	_, err := src.WriteContent("doc.md", []byte("hello"))
	require.NoError(t, err)

	// Copy: source remains.
	_, err = CopyInto(src, dst, "doc.md", "doc-copy.md")
	require.NoError(t, err)
	result, err := dst.ReadContent("doc-copy.md")
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Content)
	_, err = src.StatFile("doc.md")
	require.NoError(t, err, "copy must leave the source in place")

	// Move: source is gone afterward.
	_, err = MoveInto(src, dst, "doc.md", "doc-moved.md")
	require.NoError(t, err)
	_, err = src.StatFile("doc.md")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound, "move must remove the source")
	result, err = dst.ReadContent("doc-moved.md")
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Content)
}

func TestMoveInto_SameRoot_UsesRename(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("a.txt", []byte("a"))
	require.NoError(t, err)
	_, err = MoveInto(r, r, "a.txt", "b.txt")
	require.NoError(t, err)
	_, err = r.StatFile("a.txt")
	assert.True(t, errors.Is(err, ErrNotFound))
	_, err = r.StatFile("b.txt")
	require.NoError(t, err)
}

func TestCopyInto_DirectoryRecursive(t *testing.T) {
	src, _ := buildTestRoot(t)
	dst, _ := buildTestRoot(t)
	require.NoError(t, src.root.MkdirAll("proj/nested", 0o700))
	_, err := src.WriteContent("proj/a.txt", []byte("a"))
	require.NoError(t, err)
	_, err = src.WriteContent("proj/nested/b.txt", []byte("b"))
	require.NoError(t, err)

	_, err = CopyInto(src, dst, "proj", "proj-copy")
	require.NoError(t, err)

	ra, err := dst.ReadContent("proj-copy/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "a", ra.Content)
	rb, err := dst.ReadContent("proj-copy/nested/b.txt")
	require.NoError(t, err)
	assert.Equal(t, "b", rb.Content)
}

func TestList_HiddenFiltering(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("visible.txt", []byte("v"))
	require.NoError(t, err)
	_, err = r.WriteContent(".hidden.txt", []byte("h"))
	require.NoError(t, err)

	entries, err := r.List("", false)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "visible.txt", entries[0].Name)

	entries, err = r.List("", true)
	require.NoError(t, err)
	require.Len(t, entries, 2)
	var sawHidden bool
	for _, e := range entries {
		if e.Name == ".hidden.txt" {
			sawHidden = true
			assert.True(t, e.IsHidden)
		}
	}
	assert.True(t, sawHidden)
}

func TestCountVisibleRootEntries_AbsentWorkTree(t *testing.T) {
	home := t.TempDir()
	// No workspaces/<id>/work/ directory created at all.
	count, err := CountVisibleRootEntries(home, "ws-none")
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	// Confirm it did NOT create the directory as a side effect.
	_, statErr := os.Stat(filepath.Join(home, "workspaces", "ws-none", "work"))
	assert.True(t, os.IsNotExist(statErr), "CountVisibleRootEntries must not mkdir the work tree")
}

// --- ADR-067 Stage 0: addressing safety vs. name shape -------------------
//
// CleanRelPath used to do two unrelated jobs in one loop. Stage 0 splits
// them: ADDRESSING safety (traversal, control characters, dot segments,
// os.Root confinement) stays here, unconditional on every platform and in
// every build; NAME SHAPE (Windows characters, reserved device names,
// trailing dot/space, the two MAX_PATH length caps) moves to
// (*Root).ValidateCreateName, which runs only when Omnipus creates a name
// and only outside a mounted folder.
//
// The tests below are written so that each one names, in its own doc
// comment, the single mutation it is there to catch.

// stage0MountedRoot builds a Root with one mount named "repo", the same way
// the multi-root tests do, but locally so these Stage 0 assertions do not
// depend on a helper another change might reshape. Returns the root and the
// mount's real target directory on the host.
func stage0MountedRoot(t *testing.T) (*Root, string) {
	t.Helper()

	workDir := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.Symlink(target, filepath.Join(workDir, "repo")))

	wr, err := os.OpenRoot(workDir)
	require.NoError(t, err)
	mr, err := os.OpenRoot(target)
	require.NoError(t, err)
	t.Cleanup(func() { _ = wr.Close(); _ = mr.Close() })

	return &Root{
		dir:    workDir,
		root:   wr,
		mounts: map[string]*mountRoot{"repo": {root: mr, name: "repo", target: target}},
	}, target
}

// TestCleanRelPath_AddressingSafetyIsUnconditional is the guard that must
// not regress (ADR-067 FR-0002, AS-6). Stage 0 deletes pathsafe calls from
// this function; nothing here may weaken as a side effect.
//
// Mutation it dies on: deleting the `strings.HasPrefix(seg, "..")` loop at
// the end of CleanRelPath — the "..-prefixed name" cases below are the ones
// fs.ValidPath does NOT catch (it rejects a segment that IS exactly "..",
// not one that merely starts with it).
//
// Second mutation it dies on: deleting the firstControlRune check — the
// NUL/CR/LF cases. Those used to be caught only by
// pathsafe.ValidateComponent's fused illegal-rune predicate, so removing
// the pathsafe call without re-homing the control-character check here
// would have silently opened a header-injection vector on the read path.
func TestCleanRelPath_AddressingSafetyIsUnconditional(t *testing.T) {
	cases := []struct{ name, raw string }{
		{"bare dotdot", ".."},
		{"leading dotdot", "../etc/passwd"},
		{"embedded dotdot", "notes/../../etc/passwd"},
		{"dotdot prefixed leaf", "..sneaky.txt"},
		{"dotdot prefixed nested leaf", "notes/..sneaky.txt"},
		{"dotdot prefixed directory", "..config/report.md"},
		{"percent-encoded traversal leaf", "..%2fdana-pwned-encoded.txt"},
		{"absolute unix", "/etc/passwd"},
		{"backslash windows-style", `notes\report.md`},
		{"embedded NUL", "notes/report\x00.md"},
		{"embedded CR", "notes/report\r.md"},
		{"embedded LF", "notes/report\n.md"},
		{"embedded ESC", "notes/report\x1b.md"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := CleanRelPath(tc.raw)
			require.Error(t, err, "raw=%q must be refused — this is addressing safety, never conditional", tc.raw)
			assert.ErrorIs(t, err, ErrInvalidPath)
			assert.Equal(t, "", rel)
		})
	}
}

// TestCleanRelPath_NameShapeNoLongerAppliedOnRead is the core FR-0001
// assertion: a file already on disk is addressable whatever it is called.
// Every name here is legal on ext4 and APFS, and every one of them was
// refused by CleanRelPath before Stage 0.
//
// Mutation it dies on: putting `pathsafe.ValidateComponent(seg)` back into
// CleanRelPath's segment loop (or `pathsafe.ValidateRelPathLength(cleaned)`
// back after it) — i.e. exactly the change Stage 0 makes, reverted.
func TestCleanRelPath_NameShapeNoLongerAppliedOnRead(t *testing.T) {
	longLatin := strings.Repeat("a", 103) + ".md"       // 106 runes: the reference vault's longest note
	longCJK := strings.Repeat("測", 93)                  // 93 runes / 279 bytes
	deepPath := strings.Repeat("dir/", 60) + "file.txt" // 248 runes, every segment short
	longComponent := strings.Repeat("b", 300) + ".md"   // 303 runes / 303 bytes

	cases := []struct{ name, raw string }{
		{"windows-illegal colon", "Meeting: 2026-01-01.md"},
		{"windows-illegal question mark", "Why?.md"},
		{"windows-illegal quote", `He said "no".md`},
		{"windows-illegal pipe", "a|b.md"},
		{"windows-illegal asterisk", "draft*.md"},
		{"windows-illegal angle brackets", "<draft>.md"},
		{"reserved device name", "CON"},
		{"reserved device name with extension", "nul.txt"},
		{"reserved device name as a directory", "notes/con/report.txt"},
		{"trailing dot", "report."},
		{"trailing space", "report "},
		{"106-rune latin basename", longLatin},
		{"93-rune 279-byte CJK basename", longCJK},
		{"300-rune component", longComponent},
		{"248-rune whole path", deepPath},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := CleanRelPath(tc.raw)
			require.NoError(t, err,
				"raw=%q is a legal name on this filesystem and names a file the operator owns; "+
					"the read path must not apply name-shape rules to it (FR-0001)", tc.raw)
			assert.Equal(t, tc.raw, rel, "a valid path must survive cleaning byte-identical")
		})
	}
}

// TestValidateCreateName_AppliesNameShapeInWorkspaceStorage proves the rules
// did not simply vanish: outside a mount, ValidateCreateName still runs
// pkg/pathsafe over every segment and over the whole path.
//
// Mutation it dies on: making ValidateCreateName `return nil` unconditionally
// (or dropping either of its two pathsafe calls).
//
// The first assertion is written against a name refused under BOTH rule sets
// — 303 runes is over the Windows rune budget and 303 bytes is over the POSIX
// 255-byte component limit — so it stays meaningful once pkg/pathsafe's
// GOOS-selected rule sets land (FR-0001c, FR-0004). The parity table then
// asserts the ROUTING, which is this method's actual job: whatever the active
// rule set says about a component, ValidateCreateName says the same. It
// deliberately does not re-assert which characters are illegal — that belongs
// to pkg/pathsafe's own tests, and duplicating it here would just pin this
// package to one platform's rule set.
func TestValidateCreateName_AppliesNameShapeInWorkspaceStorage(t *testing.T) {
	r, _ := buildTestRoot(t)

	overlongEveryRuleSet := strings.Repeat("b", 300) + ".md"
	require.Equal(t, 303, utf8.RuneCountInString(overlongEveryRuleSet))
	require.Equal(t, 303, len(overlongEveryRuleSet))

	err := r.ValidateCreateName(overlongEveryRuleSet)
	require.Error(t, err, "a 303-rune / 303-byte component exceeds every rule set's component budget")
	assert.ErrorIs(t, err, ErrInvalidPath, "must map to a 400 at the REST layer")
	assert.ErrorIs(t, err, pathsafe.ErrNameTooLong, "and must say WHY, not just that it failed")

	// The same name one directory down: an intermediate segment is checked
	// too, so a single mkdir cannot smuggle one in.
	err = r.ValidateCreateName(overlongEveryRuleSet + "/report.md")
	require.Error(t, err, "an intermediate segment must be checked, not just the leaf")
	assert.ErrorIs(t, err, ErrInvalidPath)

	parity := []string{
		"report.md",
		"Meeting: 2026-01-01.md",
		"Why?.md",
		"CON",
		"nul.txt",
		"report.",
		"report ",
		strings.Repeat("a", 103) + ".md",
		strings.Repeat("測", 93),
		"Copy of My Deck.pptx",
		"My Report (final) — résumé 测试 🎉.txt",
	}
	for _, name := range parity {
		t.Run("parity/"+name, func(t *testing.T) {
			wantErr := pathsafe.ValidateComponent(name) != nil
			gotErr := r.ValidateCreateName(name) != nil
			assert.Equal(t, wantErr, gotErr,
				"ValidateCreateName must apply the ACTIVE pathsafe rule set to %q, no more and no less", name)
		})
	}

	// Whole-path parity: many short segments summing past the path cap.
	deep := strings.Repeat("dir/", 60) + "file.txt"
	assert.Equal(t,
		pathsafe.ValidateRelPathLength(deep) != nil,
		r.ValidateCreateName(deep) != nil,
		"the whole-path cap must be applied on create, in step with the active rule set")
}

// TestValidateCreateName_SkipsNameShapeInsideAMount is FR-0001b. A mounted
// folder is the operator's own disk; Omnipus does not get to decide what
// their files are called, even when it is the one writing.
//
// Mutation it dies on: deleting the `if r.mountFor(rel) != nil { return nil }`
// early return.
//
// The name used is the one the previous test proves is refused in workspace
// storage under every rule set, so this cannot pass by the rules having
// silently disappeared everywhere — the identical name at the work root is
// asserted to still fail, in the same test.
func TestValidateCreateName_SkipsNameShapeInsideAMount(t *testing.T) {
	r, _ := stage0MountedRoot(t)

	overlong := strings.Repeat("b", 300) + ".md"

	require.Error(t, r.ValidateCreateName(overlong),
		"control: the same name in WORKSPACE storage must still be refused")

	assert.NoError(t, r.ValidateCreateName("repo/"+overlong),
		"inside a mount, name shape is the host filesystem's business, not ours")
	assert.NoError(t, r.ValidateCreateName("repo/Meeting: 2026-01-01.md"),
		"a Windows-illegal character inside a mount must not be refused on any platform "+
			"(load-bearing only on a Windows build, where the rule is on; kept because that "+
			"build is the one the operator would otherwise be locked out of)")
	assert.NoError(t, r.ValidateCreateName("repo/sub/CON"),
		"the mount is decided by the FIRST segment, exactly as resolve decides it")

	// A path whose first segment merely resembles a mount name is NOT in the
	// mount — otherwise "repo-backup/..." would inherit a relaxation it was
	// never granted.
	assert.Error(t, r.ValidateCreateName("repo-backup/"+overlong),
		"only an exact first-segment match is inside the mount")
}

// TestValidateCreateName_BrokenMountIsStillAMount: a mount whose target
// cannot be opened (detached volume, renamed folder) keeps a map entry with a
// nil os.Root. Its files are still the operator's, so name shape still does
// not apply — the create will fail at I/O time for the honest reason.
//
// Mutation it dies on: narrowing the mount predicate to
// `m := r.mountFor(rel); m != nil && m.root != nil`.
//
// The probe is the 303-rune / 303-byte name again rather than a
// Windows-illegal character, for the reason the first version of this test
// got wrong: with the POSIX rule set active, "Meeting: 2026-01-01.md" is a
// perfectly legal component, so asserting it is accepted proves nothing on
// this platform and the mutation survived. A name over BOTH the rune and the
// byte budget is refused by every rule set, so only the mount skip can be
// letting it through.
func TestValidateCreateName_BrokenMountIsStillAMount(t *testing.T) {
	r, _ := buildTestRoot(t)
	r.mounts = map[string]*mountRoot{
		"gone": {root: nil, name: "gone", target: "/nonexistent/operator/folder"},
	}

	overlong := strings.Repeat("b", 300) + ".md"
	require.Error(t, r.ValidateCreateName(overlong),
		"control: the same name in workspace storage must still be refused")

	assert.NoError(t, r.ValidateCreateName("gone/"+overlong),
		"a broken mount is still the operator's folder; refusing on NAME would report the wrong problem")
}

// TestValidateCreateName_RootItselfIsNotACreatedName: "" and "." address the
// work-tree root, which no caller-supplied name creates.
//
// Mutation it dies on: deleting the `rel == "" || rel == "."` early return —
// pathsafe.ValidateComponent("") returns ErrEmptyName, so every root-scoped
// create (an upload into the top of the work tree) would start 400ing.
func TestValidateCreateName_RootItselfIsNotACreatedName(t *testing.T) {
	r, _ := buildTestRoot(t)
	assert.NoError(t, r.ValidateCreateName(""))
	assert.NoError(t, r.ValidateCreateName("."))
}

// TestMountedFile_WindowsIllegalName_ListsAndOpens is US-0 AS-1/AS-2 end to
// end, through the real Root rather than through CleanRelPath alone: the
// operator's own file, named as they named it, must list, stat and open.
//
// Mutation it dies on: putting pathsafe.ValidateComponent back into
// CleanRelPath — the path never reaches the filesystem and the operator sees
// a 400 on a file they can see in the listing.
func TestMountedFile_WindowsIllegalName_ListsAndOpens(t *testing.T) {
	r, target := stage0MountedRoot(t)

	const name = "Meeting: 2026-01-01.md"
	require.NoError(t, os.WriteFile(filepath.Join(target, name), []byte("the operator's own note"), 0o600))

	rel, err := CleanRelPath("repo/" + name)
	require.NoError(t, err, "addressing the operator's own file must not be a 400")
	require.Equal(t, "repo/"+name, rel)

	entries, err := r.List("repo", false)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name)
	}
	assert.Contains(t, names, name, "the file must appear in the listing")

	fi, err := r.StatFile(rel)
	require.NoError(t, err)
	assert.Equal(t, name, fi.Name())

	got, err := r.ReadContent(rel)
	require.NoError(t, err)
	assert.Equal(t, "the operator's own note", got.Content)
}
