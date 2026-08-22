// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// pathsafe_integration_test.go — regression tests for filename safety in
// the Library root, as ADR-067 Stage 0 leaves it.
//
// Stage 0 drew TWO independent lines through what used to be one blanket
// rule, and both are asserted below. Conflating them is the mistake this
// file previously encoded, so they are named separately:
//
//  1. READ vs CREATE. Name-SHAPE rules (reserved device names, characters
//     Windows forbids, a trailing dot or space, length caps) apply only to
//     a name Omnipus is about to CREATE. They never apply to reading,
//     listing, indexing or linking, because a mount holds files the
//     operator already has and never asked us to name.
//
//  2. POSIX vs WINDOWS. Those same Windows-shape rules are enforced only
//     where a Windows filesystem will actually see the file. On Linux and
//     macOS a file may legitimately be called "CON" or "a<b.txt"; refusing
//     it there would take away a naming freedom the host grants. Selection
//     is by GOOS inside pkg/pathsafe, so nothing here needs a build tag.
//
// Addressing safety ("..", absolute paths, NUL) is neither of those and did
// not move: it is refused everywhere, always.
//
// Separately, every collision-sensitive Root operation (Rename, CopyInto,
// CreateUnique, Mkdir, WriteContent) detects a colliding sibling
// case-INsensitively, so behaviour is identical whether the host
// filesystem is case-sensitive (ext4) or not (NTFS, default APFS/HFS+).

package library

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/pathsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Windows-shape rules: create-only, and Windows-only ---

// windowsHostileNames are single components that a Windows filesystem
// refuses and a POSIX one accepts. Each row is checked against all four
// corners of the Stage 0 contract, so a regression in any one of them
// fails here rather than surfacing as a file the operator cannot open.
var windowsHostileNames = []struct {
	name string
	want error // the pathsafe sentinel WindowsRules must produce
}{
	{"CON", pathsafe.ErrReservedName},
	{"con", pathsafe.ErrReservedName},
	{"nul.txt", pathsafe.ErrReservedName},
	{"COM1.log", pathsafe.ErrReservedName},
	{"LPT1", pathsafe.ErrReservedName},
	{"bad<name.txt", pathsafe.ErrIllegalChar},
	{"bad>name.txt", pathsafe.ErrIllegalChar},
	{"bad:name.txt", pathsafe.ErrIllegalChar},
	{`bad"name.txt`, pathsafe.ErrIllegalChar},
	{"bad|name.txt", pathsafe.ErrIllegalChar},
	{"bad?name.txt", pathsafe.ErrIllegalChar},
	{"bad*name.txt", pathsafe.ErrIllegalChar},
	{"report.", pathsafe.ErrTrailingDotOrSpace},
	{"report ", pathsafe.ErrTrailingDotOrSpace},
}

// The rule still exists and still bites — asserted against the rule set as
// a VALUE, so this runs identically on a Mac, a Linux runner and a Windows
// runner. Without this test, deleting every Windows rule would still leave
// the suite green on the machines we develop on.
func TestWindowsRules_StillRejectHostileNames(t *testing.T) {
	for _, tc := range windowsHostileNames {
		t.Run(tc.name, func(t *testing.T) {
			err := pathsafe.WindowsRules.ValidateComponent(tc.name)
			require.Error(t, err, "WindowsRules must reject %q on every platform", tc.name)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

// The founder requirement, stated as a test: Linux and macOS support all
// filenames. If someone "helpfully" re-adds a Windows rule to POSIXRules,
// this fails — which is the only way that regression gets noticed on a
// non-Windows machine.
func TestPOSIXRules_AcceptWindowsHostileNames(t *testing.T) {
	for _, tc := range windowsHostileNames {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, pathsafe.POSIXRules.ValidateComponent(tc.name),
				"POSIXRules must accept %q — the host filesystem allows it", tc.name)
		})
	}
}

// The read path never applies shape rules, on ANY platform. A file already
// sitting in a mount must be addressable whatever it is called; refusing it
// would make the operator's own file invisible in their own library.
func TestCleanRelPath_AcceptsWindowsHostileNames(t *testing.T) {
	for _, tc := range windowsHostileNames {
		t.Run(tc.name, func(t *testing.T) {
			rel, err := CleanRelPath(tc.name)
			require.NoError(t, err, "reading must not apply name-shape rules")
			assert.Equal(t, tc.name, rel, "the name must survive unaltered")
		})
	}
	// Nested, too: the walk resolves every segment and must not trip on an
	// intermediate directory the operator named years ago.
	rel, err := CleanRelPath("notes/con/report.txt")
	require.NoError(t, err)
	assert.Equal(t, "notes/con/report.txt", rel)
}

// The create path enforces whatever the BUILD TARGET's filesystem enforces.
// Expectation is derived from ActiveRules() rather than hardcoded, because
// hardcoding either answer makes the test wrong on half the CI matrix — and
// a test that is wrong on Windows is a test nobody runs on Windows.
func TestValidateCreateName_FollowsActiveRules(t *testing.T) {
	root, _ := buildTestRoot(t)
	active := pathsafe.ActiveRules()

	for _, tc := range windowsHostileNames {
		t.Run(tc.name, func(t *testing.T) {
			err := root.ValidateCreateName(tc.name)
			if active.ValidateComponent(tc.name) != nil {
				require.Error(t, err, "the active rule set rejects %q, so create must too", tc.name)
				assert.ErrorIs(t, err, ErrInvalidPath, "must still map to a 400")
				assert.ErrorIs(t, err, tc.want, "the pathsafe sentinel must stay reachable through the wrap")
			} else {
				require.NoError(t, err, "the active rule set allows %q, so create must not refuse it", tc.name)
			}
		})
	}

	// Intermediate segments are checked, not just the leaf: creating
	// "CON/report.txt" on Windows would otherwise leave an unopenable
	// directory behind that no later per-leaf check ever revisits.
	err := root.ValidateCreateName("CON/report.txt")
	if active.ValidateComponent("CON") != nil {
		require.Error(t, err, "an illegal INTERMEDIATE segment must be caught")
	} else {
		require.NoError(t, err)
	}
}

// ADR-067 Stage 0 moved the LENGTH rules from the read path to the create
// path. These two tests previously asserted CleanRelPath rejects an over-long
// name; that was the pre-Stage-0 contract and it is now wrong in a specific,
// deliberate way.
//
// Why the contract changed: a component-rune cap and a whole-path-rune cap
// both exist for Windows MAX_PATH headroom. They are name-SHAPE rules, not
// addressing-safety rules, so under FR-0001 they must not refuse a file that
// is already on the operator's disk — a name Omnipus never chose. Under
// FR-0004d they still apply to what Omnipus CREATES.
//
// So each test below keeps its original assertion and gains its opposite:
// the name is refused on create, and accepted on read. Coverage grows; the
// guarantee that a long name cannot be created is unchanged.

func TestLengthRules_RefusedOnCreate_AcceptedOnRead(t *testing.T) {
	root, _ := buildTestRoot(t)

	t.Run("component over the rune cap", func(t *testing.T) {
		// A 210-rune filename is realistic (UAT precedent).
		name := strings.Repeat("a", 210) + ".txt"

		// Create: refused. This is the half that must never regress —
		// Stage 0 relaxes what Omnipus READS, never what it writes.
		// The rune cap is a Windows MAX_PATH budget, so like every other
		// shape rule it binds only where the active rule set says so.
		// POSIX keeps a 255-BYTE cap instead, which 211 ASCII bytes clears.
		if pathsafe.ActiveRules().MaxComponentRunes > 0 {
			require.Error(t, root.ValidateCreateName(name),
				"a 210-rune component must be refused when Omnipus creates it")
		}

		// Read: accepted. A file with this name can exist on a mounted
		// disk; refusing to address it makes the operator's own file
		// invisible, which is the defect Stage 0 exists to fix.
		_, err := CleanRelPath(name)
		require.NoError(t, err,
			"an existing file must be addressable regardless of its length")
	})

	t.Run("whole path over the rune cap", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 50; i++ {
			b.WriteString("dir/")
		}
		rel := b.String() + "file.txt"

		if pathsafe.ActiveRules().MaxRelPathRunes > 0 {
			require.Error(t, root.ValidateCreateName(rel),
				"a 200+ rune path must be refused on create")
		}

		_, err := CleanRelPath(rel)
		require.NoError(t, err,
			"a deep existing path must remain readable")
	})
}

// The length caps themselves, asserted as values so they hold on every
// platform — the conditional assertions above would otherwise let both caps
// be deleted without a single red test on a Mac.
func TestWindowsRules_LengthCapsStillBite(t *testing.T) {
	long := strings.Repeat("a", 210) + ".txt"
	require.ErrorIs(t, pathsafe.WindowsRules.ValidateComponent(long), pathsafe.ErrNameTooLong)
	require.NoError(t, pathsafe.POSIXRules.ValidateComponent(long),
		"210 ASCII bytes is under the POSIX 255-byte cap")

	deep := strings.Repeat("dir/", 50) + "file.txt"
	require.ErrorIs(t, pathsafe.WindowsRules.ValidateRelPathLength(deep), pathsafe.ErrNameTooLong)
	require.NoError(t, pathsafe.POSIXRules.ValidateRelPathLength(deep),
		"POSIX sets no whole-path rune budget")
}

// Addressing safety is NOT length, and did not move. This is the guard that
// must hold on every platform and every build — if it ever goes green while
// the create-side length rules are disabled, the split has been mis-drawn.
func TestCleanRelPath_AddressingSafetyStillRejects(t *testing.T) {
	for _, raw := range []string{
		"../etc/passwd",
		"notes/../../etc/passwd",
		"..",
		"/etc/passwd",
		"notes/report\x00.md",
	} {
		t.Run(raw, func(t *testing.T) {
			_, err := CleanRelPath(raw)
			require.Error(t, err, "raw=%q must be refused on every platform", raw)
		})
	}

	// "." is deliberately NOT in that list: it addresses the work-tree root
	// itself and returns ("", nil) by design. ".." is traversal; "." is not.
	// Asserting it here would have been testing my own misreading.
	rel, err := CleanRelPath(".")
	require.NoError(t, err, `"." addresses the root itself and is valid`)
	require.Equal(t, "", rel)

	// Positive control: without it, a CleanRelPath that refused EVERYTHING
	// would pass the whole list above.
	_, err = CleanRelPath("notes/report.md")
	require.NoError(t, err, "an ordinary relative path must still be accepted")
}

func TestCleanRelPath_RealWorldUATNames_Allowed(t *testing.T) {
	cases := []string{"Copy of My Deck.pptx", "My Report (final) — résumé 测试 🎉.txt"}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			rel, err := CleanRelPath(raw)
			require.NoError(t, err, "raw=%q", raw)
			assert.Equal(t, raw, rel)
		})
	}
}

// --- Rename: case-insensitive collision ---

func TestRename_CaseInsensitiveCollision_DifferentFile_Rejected(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Report.txt", []byte("original"))
	require.NoError(t, err)
	_, err = r.WriteContent("draft.txt", []byte("draft"))
	require.NoError(t, err)

	// "report.txt" differs from "Report.txt" only in case — on a
	// case-sensitive host (this dev/CI box) an exact-case Stat would miss,
	// but this must still be rejected: on Windows/macOS this exact rename
	// would silently clobber Report.txt's content.
	_, err = r.Rename("draft.txt", "report.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)

	// The original file's content must be untouched.
	result, err := r.ReadContent("Report.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", result.Content)
}

func TestRename_CaseOnlyRelabelOfSameFile_Allowed(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Report.txt", []byte("hello"))
	require.NoError(t, err)

	// Renaming a file to a different case OF ITSELF is a legitimate
	// relabel, not a collision — it must be allowed on every platform.
	fi, err := r.Rename("Report.txt", "report.txt")
	require.NoError(t, err, "a pure case-only rename of the SAME entry must be allowed")
	assert.Equal(t, "report.txt", fi.Name())

	result, err := r.ReadContent("report.txt")
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Content)
}

func TestRename_CaseInsensitiveCollision_DifferentDirectory_Rejected(t *testing.T) {
	r, _ := buildTestRoot(t)
	require.NoError(t, r.root.MkdirAll("archive", 0o700))
	require.NoError(t, r.root.MkdirAll("notes", 0o700))
	_, err := r.WriteContent("archive/Report.txt", []byte("archived"))
	require.NoError(t, err)
	_, err = r.WriteContent("notes/Report.txt", []byte("notes"))
	require.NoError(t, err)

	// Moving notes/Report.txt into archive/ where "report.txt" (different
	// case) already exists must be rejected — this is a genuinely
	// different file, not a case-only relabel of the archive entry.
	_, err = r.Rename("notes/Report.txt", "archive/report.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

// --- CopyInto: case-insensitive collision, including same-root ---

func TestCopyInto_CaseInsensitiveCollision_Rejected(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Report.txt", []byte("original"))
	require.NoError(t, err)
	_, err = r.WriteContent("draft.txt", []byte("draft"))
	require.NoError(t, err)

	_, err = CopyInto(r, r, "draft.txt", "report.txt")
	require.Error(t, err, "copy has no same-entry exception — case-different sibling is always a collision")
	assert.ErrorIs(t, err, ErrAlreadyExists)

	result, err := r.ReadContent("Report.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", result.Content, "the existing file must be untouched")
}

func TestCopyInto_CrossRoot_CaseInsensitiveCollision_Rejected(t *testing.T) {
	src, _ := buildTestRoot(t)
	dst, _ := buildTestRoot(t)
	_, err := src.WriteContent("doc.md", []byte("hello"))
	require.NoError(t, err)
	_, err = dst.WriteContent("Doc.md", []byte("existing"))
	require.NoError(t, err)

	_, err = CopyInto(src, dst, "doc.md", "doc.md")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
}

// --- CreateUnique: case-insensitive de-duplication numbering ---

func TestCreateUnique_CaseInsensitiveCollision_Deduplicates(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, f, err := r.CreateUnique("Report.txt")
	require.NoError(t, err)
	f.Close()

	// A different-case candidate must be treated as colliding — de-duplicated
	// (using the REQUESTED name's own casing for the numbered suffix, the
	// same convention an exact-case collision already followed) rather
	// than silently allowed to create a second file that would collide
	// with the first the moment this workspace opens on Windows or
	// default macOS.
	finalRel, f2, err := r.CreateUnique("report.txt")
	require.NoError(t, err)
	f2.Close()
	assert.Equal(t, "report (1).txt", finalRel)
}

func TestCreateUnique_SameCaseCollision_StillDeduplicates(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, f, err := r.CreateUnique("doc.txt")
	require.NoError(t, err)
	f.Close()

	finalRel, f2, err := r.CreateUnique("doc.txt")
	require.NoError(t, err)
	f2.Close()
	assert.Equal(t, "doc (1).txt", finalRel)
}

// --- Mkdir: case-insensitive idempotency / conflict ---

func TestMkdir_CaseInsensitiveExistingDirectory_Idempotent(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, created1, err := r.Mkdir("Folder")
	require.NoError(t, err)
	assert.True(t, created1)

	fi, created2, err := r.Mkdir("folder")
	require.NoError(t, err, "a case-different existing DIRECTORY must be treated as the same idempotent success")
	assert.False(t, created2)
	assert.True(t, fi.IsDir())
}

func TestMkdir_CaseInsensitiveExistingFile_Conflict(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Taken.txt", []byte("x"))
	require.NoError(t, err)

	_, created, err := r.Mkdir("taken.txt")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAlreadyExists)
	assert.False(t, created)
}

// --- WriteContent: case-insensitive collision on new-file PUT ---

func TestWriteContent_CaseInsensitiveCollision_Rejected(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("Report.txt", []byte("original"))
	require.NoError(t, err)

	_, err = r.WriteContent("report.txt", []byte("would-be duplicate"))
	require.Error(t, err, "PUT to a case-different sibling must not silently create a duplicate/overwrite")
	assert.ErrorIs(t, err, ErrAlreadyExists)

	result, err := r.ReadContent("Report.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", result.Content)
}

func TestWriteContent_ExactCaseMatch_StillOverwrites(t *testing.T) {
	r, _ := buildTestRoot(t)
	_, err := r.WriteContent("report.txt", []byte("v1"))
	require.NoError(t, err)

	_, err = r.WriteContent("report.txt", []byte("v2"))
	require.NoError(t, err, "an exact-case PUT to an existing file remains a normal in-place overwrite")

	result, err := r.ReadContent("report.txt")
	require.NoError(t, err)
	assert.Equal(t, "v2", result.Content)
}
