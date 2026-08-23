// Omnipus — ADR-067 stage 3 (US-12, US-14, US-15): creating and editing notes.
//
// The oracle for every assertion here is the specification, never the
// implementation:
//
//   - FR-105 "MUST preserve all frontmatter apart from the rewritten value" is
//     asserted as a WHOLE-FILE byte comparison against a literal expected
//     string written out by hand. A test that re-derived the expectation by
//     calling the code could not distinguish "preserved" from "reordered
//     consistently".
//   - FR-106 / FR-107 (version token, mtime insufficiency) are asserted by
//     restoring the modification time after an external change and REQUIRING
//     the fixture's restoration to have worked before the real assertion runs.
//     Without that step a passing test may only mean the fixture failed.
//   - FR-0001 / FR-0001a / FR-0001b (name shape on create only, skipped inside
//     a mount) are asserted with both verdicts reachable on one runner, by
//     passing rule sets as values rather than depending on GOOS.
//   - FR-090 (every mutation AND every refusal audited) is asserted on a
//     recording sink, for both outcomes.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

var a1Seq atomic.Uint64

// a1Home returns a fresh $OMNIPUS_HOME.
func a1Home(t *testing.T) string {
	t.Helper()
	return a1Real(t, t.TempDir())
}

// a1Workspace seeds a minimal valid workspace record and returns its id.
func a1Workspace(t *testing.T, home string) string {
	t.Helper()
	id := "a1ws-" + strconv.FormatUint(a1Seq.Add(1), 10)
	now := time.Now().UTC().Format(time.RFC3339)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: id, Name: id, Status: "active", CreatedAt: now, UpdatedAt: now,
	}))
	return id
}

// a1Note writes a note directly on disk, bypassing the authoring path, so a
// test can set up a starting state the code under test did not produce.
func a1Note(t *testing.T, root, rel, body string, mode os.FileMode) string {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
	require.NoError(t, os.WriteFile(full, []byte(body), mode))
	require.NoError(t, os.Chmod(full, mode))
	return full
}

// a1Recorder is a recording audit sink.
type a1Recorder struct{ records []AuthorAuditRecord }

func (r *a1Recorder) RecordKnowledgeWrite(rec AuthorAuditRecord) { r.records = append(r.records, rec) }

func (r *a1Recorder) last() AuthorAuditRecord { return r.records[len(r.records)-1] }

// ---------------------------------------------------------------------------
// US-12 AS-1, AS-2 — spec test 40: a new note starts from the template
// ---------------------------------------------------------------------------

// TestNewNote_FromTemplateValidates is spec test 40.
//
// "Validates against the collection's schema" (AS-2) has no schema mechanism
// anywhere in ADR-067 — there is no schema file, no validator and no
// requirement defining one. The testable core of the requirement is what the
// scenario actually depends on: the created note carries the TEMPLATE's
// frontmatter, structurally intact and with the documented placeholders
// substituted, rather than arriving blank. A collection with a schema is
// satisfied by exactly that, because the schema is expressed in the operator's
// own template.
func TestNewNote_FromTemplateValidates(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	a1WriteTemplate(t, c, "Meeting.md",
		"---\ntitle: {{title}}\ntype: meeting\ncreated: {{datetime}}\ntags:\n  - meeting\n---\n\n# {{title}}\n\n## Attendees\n\n## Notes\n")

	rec := &a1Recorder{}
	got, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
		RelPath:   "meetings/2026-08-23 standup.md",
		Template:  "Meeting.md",
		Now:       a1Clock(t),
		NameShape: OperatorNameShape,
		Audit:     rec,
		Actor:     AuthorActor{AgentID: "ava", WorkspaceID: "ws-1"},
	})
	require.NoError(t, err)

	const want = "---\ntitle: 2026-08-23 standup\ntype: meeting\ncreated: 2026-08-23T14:05:09Z\n" +
		"tags:\n  - meeting\n---\n\n# 2026-08-23 standup\n\n## Attendees\n\n## Notes\n"

	onDisk, err := os.ReadFile(got.AbsPath)
	require.NoError(t, err)
	assert.Equal(t, want, string(onDisk),
		"the created note is the template with the documented placeholders substituted "+
			"and every other byte copied through (FR-100, FR-102)")

	// The note is where it was asked for, and the version token describes what
	// was actually written — a caller can edit it with no intervening read.
	assert.Equal(t, "meetings/2026-08-23 standup.md", got.RelPath)
	assert.Equal(t, filepath.Join(root, "meetings", "2026-08-23 standup.md"), got.AbsPath)
	assert.Equal(t, NoteContentVersion([]byte(want)), got.Version)
	assert.Equal(t, len(want), got.Bytes)

	// Its frontmatter is a real, terminated block that this package can parse
	// and edit — the property an operator's schema depends on.
	block, err := fmParse(onDisk)
	require.NoError(t, err)
	require.True(t, block.present, "a note created from a frontmatter template must have frontmatter")
	assert.Contains(t, string(onDisk[block.innerStart:block.innerEnd]), "type: meeting")
}

// TestCreateNote_TemplatelessBodyIsNeverExpanded: a body supplied by a caller
// is CONTENT, not a template. Expanding it would rewrite text a user typed.
func TestCreateNote_TemplatelessBodyIsNeverExpanded(t *testing.T) {
	c := a1Collection(t, a1Vault(t, "Research", nil))
	got, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
		RelPath:   "raw.md",
		Body:      []byte("Literally {{title}} and {{date}}.\n"),
		Title:     "Something Else",
		Now:       a1Clock(t),
		NameShape: OperatorNameShape,
	})
	require.NoError(t, err)
	onDisk, err := os.ReadFile(got.AbsPath)
	require.NoError(t, err)
	assert.Equal(t, "Literally {{title}} and {{date}}.\n", string(onDisk))
}

// TestCreateNote_MissingExtensionBecomesMarkdown: a note with no extension is
// classified as an attachment by scan.go and never indexed as prose, so a
// create that quietly produced one would make the note unsearchable.
func TestCreateNote_MissingExtensionBecomesMarkdown(t *testing.T) {
	c := a1Collection(t, a1Vault(t, "Research", nil))
	got, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
		RelPath: "inbox/quick", NameShape: OperatorNameShape,
	})
	require.NoError(t, err)
	assert.Equal(t, "inbox/quick.md", got.RelPath)
	require.FileExists(t, got.AbsPath)
}

// ---------------------------------------------------------------------------
// Containment and clobbering (US-10, US-14)
// ---------------------------------------------------------------------------

// TestCreateNote_RefusesEscapeClobberAndToolState covers every way a create can
// write somewhere it must not.
//
// The symlink case is the one a lexical-only containment check passes and a
// real one fails: "link" is a directory symlink pointing out of the
// collection, so "link/note.md" is lexically inside and physically outside.
func TestCreateNote_RefusesEscapeClobberAndToolState(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)

	outside := a1Real(t, t.TempDir())
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "link")))
	a1Note(t, root, "taken.md", "original\n", 0o600)

	cases := []struct {
		name   string
		rel    string
		errIs  error
		unseen string // a path that must NOT exist afterwards
	}{
		{name: "traversal", rel: "../escaped.md", errIs: ErrOutsideCollection,
			unseen: filepath.Join(filepath.Dir(root), "escaped.md")},
		{name: "absolute", rel: filepath.Join(outside, "escaped.md"), errIs: ErrOutsideCollection,
			unseen: filepath.Join(outside, "escaped.md")},
		{name: "through a directory symlink", rel: "link/escaped.md", errIs: ErrOutsideCollection,
			unseen: filepath.Join(outside, "escaped.md")},
		{name: "already exists", rel: "taken.md", errIs: ErrNoteExists},
		{name: "inside the Omnipus marker", rel: MarkerDirName + "/vault.json", errIs: ErrReservedLocation},
		{name: "inside Obsidian's directory", rel: ObsidianMarkerDirName + "/app.json", errIs: ErrReservedLocation},
		{name: "nested tool state", rel: "projects/" + ObsidianMarkerDirName + "/plugins/x.md", errIs: ErrReservedLocation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
				RelPath: tc.rel, Body: []byte("payload\n"), NameShape: OperatorNameShape,
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.errIs)
			if tc.unseen != "" {
				assert.NoFileExists(t, tc.unseen, "a refused create must write nothing anywhere")
			}
		})
	}

	// The existing note is byte-for-byte what it was: a refused create never
	// truncates its target.
	onDisk, err := os.ReadFile(filepath.Join(root, "taken.md"))
	require.NoError(t, err)
	assert.Equal(t, "original\n", string(onDisk))
}

// ---------------------------------------------------------------------------
// FR-0001 / FR-0001a / FR-0001b — name shape on CREATE only, population-aware
// ---------------------------------------------------------------------------

// TestCreateNote_NameShapeAppliesOnCreateAndNotOnRead is the ADR-067 stage-0
// line, tested at the authoring boundary.
//
// The fixture name is the one the spec measured in a real collection: a colon
// and non-ASCII characters. Windows refuses it; POSIX does not; the operator
// typed it. So:
//
//   - CREATING it inside a mounted collection succeeds (FR-0001b — the host
//     filesystem is the authority there).
//   - CREATING it under a Windows rule set is refused (FR-0001a, FR-0001c).
//     Both verdicts are produced on ONE runner by passing the rule set as a
//     value, which is precisely why pkg/pathsafe exposes RuleSet as data.
//   - EDITING it, once it exists, is never name-checked at all (FR-0001).
func TestCreateNote_NameShapeAppliesOnCreateAndNotOnRead(t *testing.T) {
	const awkward = "Meeting: 2026-01-01 — Résumé ✅.md"

	// The name really is one a Windows rule set refuses — without this the
	// "refused under Windows rules" case below could be passing for some
	// unrelated reason.
	require.Error(t, pathsafe.WindowsRules.ValidateComponent(awkward),
		"fixture check: the awkward name must be Windows-illegal, or this test proves nothing")
	require.NoError(t, pathsafe.POSIXRules.ValidateComponent(awkward),
		"fixture check: the awkward name must be POSIX-legal")

	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)

	t.Run("created inside a mount, colon and unicode intact", func(t *testing.T) {
		got, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
			RelPath: awkward, Body: []byte("# Notes\n"), NameShape: OperatorNameShape,
		})
		require.NoError(t, err, "FR-0001b: no name-shape rule applies inside a mount")
		assert.Equal(t, awkward, got.RelPath)
		require.FileExists(t, got.AbsPath)

		// And the name survives on disk exactly — not sanitised into
		// something else, which would be a silent rename of the operator's
		// note.
		entries, rerr := os.ReadDir(root)
		require.NoError(t, rerr)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		assert.Contains(t, names, awkward)
	})

	t.Run("refused under a Windows rule set", func(t *testing.T) {
		windows := func(rel string) error {
			for _, seg := range strings.Split(rel, "/") {
				if err := pathsafe.WindowsRules.ValidateComponent(seg); err != nil {
					return err
				}
			}
			return nil
		}
		_, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
			RelPath: "sub/" + awkward, Body: []byte("x\n"), NameShape: windows,
		})
		require.Error(t, err, "FR-0001a: the create path applies the destination's rule set")
		assert.NoFileExists(t, filepath.Join(root, "sub", awkward))
	})

	t.Run("editing an awkward name is never name-checked", func(t *testing.T) {
		current, rerr := os.ReadFile(filepath.Join(root, awkward))
		require.NoError(t, rerr)
		res, err := EditNote(OSLinkFS(), c, EditNoteRequest{
			RelPath:       awkward,
			Edits:         []NoteEdit{AppendSection("Follow-up", "Nothing yet.")},
			ExpectVersion: NoteContentVersion(current),
		})
		require.NoError(t, err,
			"FR-0001: reading and editing an existing file must never apply name-shape rules")
		assert.True(t, res.Changed)
	})

	t.Run("an unset check fails closed", func(t *testing.T) {
		// 300 bytes: past POSIX NAME_MAX (255), so the refusal is the same on
		// every build and never reaches the filesystem.
		long := strings.Repeat("n", 300) + ".md"
		_, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{RelPath: long, Body: []byte("x")})
		require.Error(t, err, "a nil NameShape must default to the strict check, never to no check")
		assert.ErrorIs(t, err, ErrNoteNameRefused)
		assert.NoFileExists(t, filepath.Join(root, long))
	})
}

// TestLibraryNameShape_UsesTheLibraryPopulationRule proves the create-time rule
// is routed through library.Root.ValidateCreateName — the single enforcement
// point FR-0001a names — rather than re-decided here.
//
// The two verdicts differ only in WHERE the destination is: the same name is
// refused in workspace storage (FR-0001c, Omnipus names those files) and
// allowed inside a mount (FR-0001b, the operator names those).
func TestLibraryNameShape_UsesTheLibraryPopulationRule(t *testing.T) {
	home := a1Home(t)
	ws := a1Workspace(t, home)
	vault := a1Vault(t, "Research", nil)
	_, _, err := workspace.CreateMount(home, ws, "notes", vault)
	require.NoError(t, err)

	r, err := library.OpenRoot(home, ws)
	require.NoError(t, err)
	defer func() { _ = r.Close() }()

	long := strings.Repeat("n", 300) + ".md"

	assert.Error(t, LibraryNameShape(r, "")(long),
		"FR-0001c: workspace storage is named by Omnipus and stays portable")
	assert.NoError(t, LibraryNameShape(r, "notes")(long),
		"FR-0001b: a create inside a mount carries no name-shape rule")
}

// ---------------------------------------------------------------------------
// FR-105 — a property edit changes ONE line and nothing else
// ---------------------------------------------------------------------------

// a1Frontmatter is a deliberately awkward but entirely legal frontmatter
// block: a comment, an anchor, nested mappings, block sequences, a quoted key,
// a wikilink value, an empty value, and a trailing inline comment. US-13 AS-3
// names comments, anchors and nested structures explicitly.
const a1Frontmatter = "---\n" +
	"# what this note is for\n" +
	"title: \"Old: notes\"\n" +
	"aliases:\n" +
	"  - Old\n" +
	"  - &hist Historical\n" +
	"tags:\n" +
	"  - project/alpha\n" +
	"  - \"weird: tag\"\n" +
	"related: \"[[Old Note]]\"\n" +
	"meta:\n" +
	"  nested:\n" +
	"    deep: value    # trailing comment\n" +
	"empty:\n" +
	"---\n"

const a1Body = "\n# Old notes\n\nSome prose with a [[Old Note]] link.\n\n```yaml\ntitle: not frontmatter\n```\n"

// TestSetProperty_PreservesEverythingElseByteForByte is the FR-105 assertion.
//
// Each case states the ENTIRE expected file as a literal. That is the only
// oracle that can distinguish "preserved" from "re-serialised into an
// equivalent document": a YAML round trip would drop the comment, resolve the
// anchor, re-quote "Old: notes" and reorder nothing visibly wrong — and would
// pass any assertion weaker than this one.
func TestSetProperty_PreservesEverythingElseByteForByte(t *testing.T) {
	src := []byte(a1Frontmatter + a1Body)

	t.Run("existing single-line key: only that line differs", func(t *testing.T) {
		out, err := SetProperty("title", "New notes")(src)
		require.NoError(t, err)

		want := strings.Replace(a1Frontmatter,
			"title: \"Old: notes\"\n", "title: New notes\n", 1) + a1Body
		assert.Equal(t, want, string(out))
	})

	t.Run("absent key: appended at the end of the block, order preserved", func(t *testing.T) {
		out, err := SetProperty("status", "draft")(src)
		require.NoError(t, err)

		want := strings.Replace(a1Frontmatter, "empty:\n---\n", "empty:\nstatus: draft\n---\n", 1) + a1Body
		assert.Equal(t, want, string(out))
	})

	t.Run("a value that YAML would re-read is quoted", func(t *testing.T) {
		// "[[Old Note]]" unquoted is a nested flow sequence, "no" is a
		// boolean, "1.20" is a number, ": " starts a mapping. Each one
		// unquoted silently changes the value's TYPE, which is a lost write
		// with none of the symptoms of one.
		for _, tc := range []struct{ value, want string }{
			{"[[Other Note]]", `"[[Other Note]]"`},
			{"no", `"no"`},
			{"1.20", `"1.20"`},
			{"a: b", `"a: b"`},
			{"", `""`},
			{"plain words", "plain words"},
		} {
			out, err := SetProperty("status", tc.value)(src)
			require.NoError(t, err, "value %q", tc.value)
			assert.Contains(t, string(out), "status: "+tc.want+"\n", "value %q", tc.value)
		}
	})

	t.Run("nested keys are not matched", func(t *testing.T) {
		// "deep" only exists indented, under meta.nested. Setting a top-level
		// "deep" must NOT rewrite that line — editing a nested value the
		// caller never named is exactly the destruction FR-105 forbids.
		out, err := SetProperty("deep", "surface")(src)
		require.NoError(t, err)
		assert.Contains(t, string(out), "    deep: value    # trailing comment\n",
			"an indented key belongs to another mapping and must be left alone")
		assert.Contains(t, string(out), "empty:\ndeep: surface\n---\n")
	})

	t.Run("no frontmatter at all: a block is added, content untouched", func(t *testing.T) {
		body := []byte("# Just a heading\n\nAnd a paragraph.\n")
		out, err := SetProperty("status", "draft")(body)
		require.NoError(t, err)
		assert.Equal(t, "---\nstatus: draft\n---\n\n# Just a heading\n\nAnd a paragraph.\n", string(out))
		assert.True(t, bytes.HasSuffix(out, body),
			"the original content must survive as an untouched suffix")
	})

	t.Run("CRLF endings are kept", func(t *testing.T) {
		crlf := []byte("---\r\ntitle: Old\r\n---\r\n\r\nBody\r\n")
		out, err := SetProperty("status", "draft")(crlf)
		require.NoError(t, err)
		assert.Equal(t, "---\r\ntitle: Old\r\nstatus: draft\r\n---\r\n\r\nBody\r\n", string(out))
	})
}

// TestSetProperty_RefusesWhatItCannotWriteSafely: every refusal here exists
// because writing it anyway corrupts the whole block, which loses every
// property in the note rather than just the one being set.
func TestSetProperty_RefusesWhatItCannotWriteSafely(t *testing.T) {
	src := []byte(a1Frontmatter + a1Body)

	cases := []struct {
		name       string
		key, value string
		errIs      error
	}{
		{"newline in the value", "title", "one\ntwo", ErrInvalidProperty},
		{"carriage return in the value", "title", "one\rtwo", ErrInvalidProperty},
		{"colon in the key", "ti:tle", "x", ErrInvalidProperty},
		{"empty key", "", "x", ErrInvalidProperty},
		{"padded key", " title ", "x", ErrInvalidProperty},
		{"control character in the value", "title", "a\x00b", ErrInvalidProperty},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := SetProperty(tc.key, tc.value)(src)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.errIs)
		})
	}

	t.Run("unterminated frontmatter is refused, not repaired", func(t *testing.T) {
		_, err := SetProperty("title", "x")([]byte("---\ntitle: Old\n\nbody with no closing fence\n"))
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrFrontmatterUnterminated)
	})
}

// ---------------------------------------------------------------------------
// Appending a section
// ---------------------------------------------------------------------------

// TestAppendSection_OnlyEverAppends asserts the operation's whole safety
// property: the original file is a literal byte prefix of the result. Nothing
// before the append point can have been altered, whatever the note contained.
func TestAppendSection_OnlyEverAppends(t *testing.T) {
	sources := map[string]string{
		"frontmatter and prose": a1Frontmatter + a1Body,
		"no trailing newline":   "# Title\n\nlast line without a newline",
		"frontmatter only":      a1Frontmatter,
		"empty file":            "",
		"crlf":                  "---\r\ntitle: x\r\n---\r\n\r\nBody\r\n",
		"open code fence":       "# T\n\n```go\nfunc main() {}\n```\n",
	}
	for name, src := range sources {
		t.Run(name, func(t *testing.T) {
			out, err := AppendSection("Follow-up", "- [ ] send the notes")([]byte(src))
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(out, []byte(src)),
				"an append must never modify a preceding byte")
			assert.Contains(t, string(out), "## Follow-up")
			assert.Contains(t, string(out), "- [ ] send the notes")
			assert.True(t, strings.HasSuffix(string(out), "\n"),
				"the note must end with a newline so the next append starts on its own line")
		})
	}

	t.Run("body is inserted verbatim, never interpreted", func(t *testing.T) {
		body := "<% tp.file.title %> {{date}} $(id) ${HOME}"
		out, err := AppendSection("Raw", body)([]byte("# T\n"))
		require.NoError(t, err)
		assert.Contains(t, string(out), body)
	})

	t.Run("heading level", func(t *testing.T) {
		out, err := AppendSectionAt(4, "Deep", "")([]byte("# T\n"))
		require.NoError(t, err)
		assert.Contains(t, string(out), "#### Deep\n")

		for _, bad := range []int{0, 7, -1} {
			_, err := AppendSectionAt(bad, "Deep", "")([]byte("# T\n"))
			assert.ErrorIs(t, err, ErrInvalidSection, "level %d", bad)
		}
	})

	t.Run("empty and multi-line headings are refused", func(t *testing.T) {
		_, err := AppendSection("   ", "x")([]byte("# T\n"))
		assert.ErrorIs(t, err, ErrInvalidSection)
		_, err = AppendSection("two\nlines", "x")([]byte("# T\n"))
		assert.ErrorIs(t, err, ErrInvalidSection)
	})

	t.Run("repeated appends do not accumulate blank lines", func(t *testing.T) {
		out, err := AppendSection("One", "a")([]byte("# T\n"))
		require.NoError(t, err)
		out, err = AppendSection("Two", "b")(out)
		require.NoError(t, err)
		assert.NotContains(t, string(out), "\n\n\n",
			"each append separates itself with exactly one blank line")
	})
}

// ---------------------------------------------------------------------------
// US-14 — the version token (FR-106) and mtime insufficiency (FR-107)
// ---------------------------------------------------------------------------

// TestEditNote_StaleVersionRefusedAndFileUnchanged is US-14 AS-1 at the
// primitive level: refused, typed, naming the path, and with the file on disk
// byte-identical to what the other writer left there.
//
// The "file unchanged" assertion is the one that matters. An implementation
// that returned the error AFTER writing would satisfy every other assertion
// here and would still have destroyed the operator's work.
func TestEditNote_StaleVersionRefusedAndFileUnchanged(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	abs := a1Note(t, root, "note.md", "---\ntitle: Old\n---\n\nMine.\n", 0o600)

	original, err := os.ReadFile(abs)
	require.NoError(t, err)
	staleToken := NoteContentVersion(original)

	// Another program (Obsidian, git, a sync agent) writes the file.
	const theirs = "---\ntitle: Old\n---\n\nTheirs — written by another program.\n"
	require.NoError(t, os.WriteFile(abs, []byte(theirs), 0o600))

	rec := &a1Recorder{}
	_, err = EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "note.md",
		Edits:         []NoteEdit{SetProperty("title", "Mine")},
		ExpectVersion: staleToken,
		Audit:         rec,
		Actor:         AuthorActor{AgentID: "ava", WorkspaceID: "ws-1"},
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoteVersionStale)
	assert.Contains(t, err.Error(), "note.md", "the typed conflict must name the path (US-14 AS-1)")

	after, err := os.ReadFile(abs)
	require.NoError(t, err)
	assert.Equal(t, theirs, string(after), "a refused write must leave the file exactly as it was")

	require.Len(t, rec.records, 1, "US-14 AS-5: a refused write is audited")
	assert.Equal(t, AuthorOutcomeRefused, rec.last().Outcome)
	assert.Equal(t, []string{"note.md"}, rec.last().Paths)
}

// TestEditNote_MtimeRestoredChangeStillDetected is US-14 AS-3 / FR-107.
//
// D14 is explicit that mtime alone is insufficient: Syncthing preserves source
// mtimes on replication and several filesystems have one-second granularity.
// The fixture reproduces exactly that — same length, same modification time,
// different bytes — and REQUIRES the restoration to have worked before
// asserting, because a fixture that silently failed to restore the timestamp
// would turn this into a test of nothing.
func TestEditNote_MtimeRestoredChangeStillDetected(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	abs := a1Note(t, root, "note.md", "AAAAAAAAAA\n", 0o600)

	before, err := os.Stat(abs)
	require.NoError(t, err)
	token := NoteContentVersion([]byte("AAAAAAAAAA\n"))

	require.NoError(t, os.WriteFile(abs, []byte("BBBBBBBBBB\n"), 0o600))
	require.NoError(t, os.Chtimes(abs, before.ModTime(), before.ModTime()))

	afterStat, err := os.Stat(abs)
	require.NoError(t, err)
	require.True(t, afterStat.ModTime().Equal(before.ModTime()),
		"fixture check: the modification time must really have been restored, "+
			"or this test would pass for the wrong reason")
	require.Equal(t, before.Size(), afterStat.Size(),
		"fixture check: the size must be unchanged too, so size cannot be the detector")

	_, err = EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "note.md",
		Edits:         []NoteEdit{AppendSection("New", "x")},
		ExpectVersion: token,
	})
	require.Error(t, err, "FR-107: the decision is the content hash, never the modification time")
	assert.ErrorIs(t, err, ErrNoteVersionStale)
}

// TestEditNote_MatchingVersionApplies is the positive control for the two
// refusal tests above: with the right token the edit lands. Without it, an
// implementation that refused EVERY write would pass both of them.
func TestEditNote_MatchingVersionApplies(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	abs := a1Note(t, root, "note.md", "---\ntitle: Old\n---\n\nBody.\n", 0o600)

	original, err := os.ReadFile(abs)
	require.NoError(t, err)

	rec := &a1Recorder{}
	res, err := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "note.md",
		Edits:         []NoteEdit{SetProperty("title", "New"), AppendSection("Log", "entry")},
		ExpectVersion: NoteContentVersion(original),
		Audit:         rec,
		Actor:         AuthorActor{AgentID: "ava", WorkspaceID: "ws-1"},
	})
	require.NoError(t, err)
	assert.True(t, res.Changed)
	assert.Equal(t, NoteContentVersion(original), res.PriorVersion)

	onDisk, err := os.ReadFile(abs)
	require.NoError(t, err)
	assert.Equal(t, "---\ntitle: New\n---\n\nBody.\n\n## Log\n\nentry\n", string(onDisk))
	assert.Equal(t, NoteContentVersion(onDisk), res.Version,
		"the returned token must describe what is on disk, so the next edit needs no re-read")

	require.Len(t, rec.records, 1)
	assert.Equal(t, AuthorOutcomeApplied, rec.last().Outcome)
}

// TestEditNote_NoOpEditWritesNothing: an edit whose result is byte-identical
// must not touch the file. Rewriting identical bytes bumps the modification
// time, which wakes every sync tool watching the folder and makes the index's
// cheap freshness check report a change that did not happen.
func TestEditNote_NoOpEditWritesNothing(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	abs := a1Note(t, root, "note.md", "---\ntitle: Same\n---\n\nBody.\n", 0o600)

	before, err := os.Stat(abs)
	require.NoError(t, err)

	res, err := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       "note.md",
		Edits:         []NoteEdit{SetProperty("title", "Same")},
		ExpectVersion: NoteContentVersion([]byte("---\ntitle: Same\n---\n\nBody.\n")),
	})
	require.NoError(t, err)
	assert.False(t, res.Changed)

	after, err := os.Stat(abs)
	require.NoError(t, err)
	assert.True(t, after.ModTime().Equal(before.ModTime()),
		"a no-op edit must not rewrite the file")
}

// TestEditNote_RefusesWhatIsNotAnEditableNote covers the paths an edit must
// never follow or truncate.
func TestEditNote_RefusesWhatIsNotAnEditableNote(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)

	outside := a1Real(t, t.TempDir())
	secretPath := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secretPath, []byte("PRIVATE\n"), 0o600))
	require.NoError(t, os.Symlink(secretPath, filepath.Join(root, "shortcut.md")))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "folder.md"), 0o700))

	for _, tc := range []struct {
		name  string
		rel   string
		errIs error
	}{
		{"missing", "nope.md", ErrNoteNotFound},
		{"a directory", "folder.md", ErrNoteNotFound},
		{"traversal", "../outside.md", ErrOutsideCollection},
		{"a symlink out of the collection", "shortcut.md", ErrOutsideCollection},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EditNote(OSLinkFS(), c, EditNoteRequest{
				RelPath: tc.rel, Edits: []NoteEdit{AppendSection("X", "y")},
			})
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.errIs)
		})
	}

	onDisk, err := os.ReadFile(secretPath)
	require.NoError(t, err)
	assert.Equal(t, "PRIVATE\n", string(onDisk), "the symlink's target must never be written")
}

// ---------------------------------------------------------------------------
// Round trip: unusual frontmatter, a filename with ':' and unicode, and a body
// larger than one index segment
// ---------------------------------------------------------------------------

// TestEditNote_RoundTripsAwkwardNameLargeBodyAndOddFrontmatter is the scale and
// awkwardness case the plan calls for, in one note:
//
//   - a filename carrying ':' and non-ASCII characters (the reference
//     collection's own shape);
//   - a body LARGER than IndexSegmentSize, the unit the index cuts documents
//     at, so the note is one that the read path handles in several pieces;
//   - frontmatter with comments, anchors, nested mappings and quoted keys.
//
// After a property edit and an appended section, the assertions are exact:
// every frontmatter line except the edited one is byte-identical, the entire
// original body appears unchanged at the same offset, and the file's mode is
// the one the operator set — not the mode a fresh create would have used.
func TestEditNote_RoundTripsAwkwardNameLargeBodyAndOddFrontmatter(t *testing.T) {
	const awkward = "Meeting: 2026-01-01 — Résumé ✅.md"

	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)

	// One index segment is 512 KiB; this body is comfortably past it, so the
	// note is genuinely a multi-segment one and not merely a long line.
	var big strings.Builder
	for i := 0; big.Len() <= IndexSegmentSize+(64<<10); i++ {
		big.WriteString("Paragraph ")
		big.WriteString(strconv.Itoa(i))
		big.WriteString(" — with a [[Old Note]] link and some prose to make it realistic.\n\n")
	}
	body := "\n# Résumé\n\n" + big.String()
	require.Greater(t, len(body), IndexSegmentSize,
		"fixture check: the body must exceed one index segment, or this test is not the case it claims")

	original := a1Frontmatter + body
	abs := a1Note(t, root, awkward, original, 0o640)

	res, err := EditNote(OSLinkFS(), c, EditNoteRequest{
		RelPath:       awkward,
		Edits:         []NoteEdit{SetProperty("title", "Résumé — 2026"), AppendSection("Actions", "- [ ] file it")},
		ExpectVersion: NoteContentVersion([]byte(original)),
	})
	require.NoError(t, err)
	require.True(t, res.Changed)

	onDisk, err := os.ReadFile(abs)
	require.NoError(t, err)

	wantFrontmatter := strings.Replace(a1Frontmatter,
		"title: \"Old: notes\"\n", "title: Résumé — 2026\n", 1)
	// body already ends with a blank line, so the appended section follows
	// directly: the rule is exactly one blank line of separation, never an
	// accumulating run of them.
	want := wantFrontmatter + body + "## Actions\n\n- [ ] file it\n"
	assert.Equal(t, want, string(onDisk),
		"nothing outside the edited property line and the appended section may change")

	// Stated again as an independent property, because the whole-file
	// comparison above would also pass if BOTH sides were wrong in the same
	// way: the original body must still be present, contiguous, byte-identical.
	assert.True(t, bytes.Contains(onDisk, []byte(body)),
		"a body larger than one index segment must survive an edit intact")

	fi, err := os.Stat(abs)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), fi.Mode().Perm(),
		"an edit preserves the operator's own file mode; widening it silently would be a "+
			"security regression nobody would ever see")

	assert.Equal(t, NoteContentVersion(onDisk), res.Version)
}

// ---------------------------------------------------------------------------
// FR-090 / US-15 — the audit record, for both outcomes
// ---------------------------------------------------------------------------

// TestAuthorAudit_AppliedAndRefusedBothRecorded asserts every field US-15 AS-1
// enumerates — who, which collection, which paths, which operation, which
// outcome — and asserts the refusal is recorded AS a refusal (AS-3), which is
// the half an audit trail of successes silently omits.
func TestAuthorAudit_AppliedAndRefusedBothRecorded(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)
	rec := &a1Recorder{}
	actor := AuthorActor{AgentID: "ava", WorkspaceID: "ws-7"}
	now := a1Clock(t)

	_, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
		RelPath: "notes/first.md", Body: []byte("# First\n"),
		NameShape: OperatorNameShape, Audit: rec, Actor: actor, Now: now,
	})
	require.NoError(t, err)

	require.Len(t, rec.records, 1)
	applied := rec.records[0]
	assert.Equal(t, AuthorOpCreate, applied.Operation)
	assert.Equal(t, AuthorOutcomeApplied, applied.Outcome)
	assert.Equal(t, "ava", applied.AgentID)
	assert.Equal(t, "ws-7", applied.WorkspaceID)
	assert.Equal(t, "Research", applied.Collection)
	assert.Equal(t, root, applied.Root)
	assert.Equal(t, []string{"notes/first.md"}, applied.Paths)
	assert.Equal(t, now, applied.At)
	assert.Empty(t, applied.Reason)

	// The same create again: refused, and on the record as a refusal.
	_, err = CreateNote(OSLinkFS(), c, CreateNoteRequest{
		RelPath: "notes/first.md", Body: []byte("# Again\n"),
		NameShape: OperatorNameShape, Audit: rec, Actor: actor, Now: now,
	})
	require.Error(t, err)

	require.Len(t, rec.records, 2)
	refused := rec.records[1]
	assert.Equal(t, AuthorOpCreate, refused.Operation)
	assert.Equal(t, AuthorOutcomeRefused, refused.Outcome)
	assert.Equal(t, []string{"notes/first.md"}, refused.Paths)
	assert.Contains(t, refused.Reason, "already exists",
		"a refusal must record WHY, or the record cannot answer the only question it is read for")

	// Refusals that never touch the filesystem are audited too — a containment
	// refusal is the one an operator most wants to see.
	_, err = CreateNote(OSLinkFS(), c, CreateNoteRequest{
		RelPath: "../escaped.md", Body: []byte("x"),
		NameShape: OperatorNameShape, Audit: rec, Actor: actor, Now: now,
	})
	require.Error(t, err)
	require.Len(t, rec.records, 3)
	assert.Equal(t, AuthorOutcomeRefused, rec.last().Outcome)
	assert.Equal(t, []string{"../escaped.md"}, rec.last().Paths)
}

// TestAuthorAudit_UnsetClockStillStampsARecord: a record with a zero timestamp
// is unusable for the question an audit trail exists to answer, so the wall
// clock is the fallback.
func TestAuthorAudit_UnsetClockStillStampsARecord(t *testing.T) {
	c := a1Collection(t, a1Vault(t, "Research", nil))
	rec := &a1Recorder{}
	_, err := CreateNote(OSLinkFS(), c, CreateNoteRequest{
		RelPath: "n.md", Body: []byte("x\n"), NameShape: OperatorNameShape, Audit: rec,
	})
	require.NoError(t, err)
	require.Len(t, rec.records, 1)
	assert.False(t, rec.last().At.IsZero())
}
