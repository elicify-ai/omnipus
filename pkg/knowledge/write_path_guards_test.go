// Omnipus — the guards the WRITE path must apply, tested on the write path.
//
// Every case here exists because a guard that this package already owned was
// wired into the read path and omitted from the write path. The read side's
// coverage is not evidence for the write side, and two tests that looked like
// evidence were not:
//
//   - vault_edit_test.go's TestVaultEdit_NeverTouchesAFileTheAgentDidNotName
//     states a blast-radius property in its NAME and then exercises `op:
//     create` only — the one op that had the reserved-location guard. It
//     passed with all four edit ops writing freely into .obsidian/, .git/,
//     .trash/ and .omnipus-vault/. So every table here runs EVERY write op.
//   - author_test.go's TestEditNote_RefusesWhatIsNotAnEditableNote has a case
//     named "a symlink out of the collection" whose comment claims a symlink
//     guard. It passes because of CONTAINMENT: the link's target is outside
//     the root, so the refusal would stand with no symlink rule at all. The
//     case that was NOT refused — a symlink whose target is INSIDE the
//     collection — had no test. It has one below, and that test REQUIRES
//     containment to accept the path before asserting the refusal, so it can
//     only pass because of the symlink rule.
//
// Oracles come from the specification and from the read path's own behaviour,
// never from what the write path currently does: FR-111 (an evicted note is
// never treated as empty), FR-043/FR-044 (real containment, and no symlink
// traversed at all), and the reserved-location rule whose authority is
// scanSkippedDirNames.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// FINDING 1 — FR-111 on the write path: a cloud-evicted note must never be
// read as empty and written back as a stub.
// ---------------------------------------------------------------------------

// TestEditNote_RefusesAnEvictedNoteInsteadOfWritingBackAStub is the write-path
// half of FR-111, and the half that destroys data rather than merely
// misreporting it.
//
// The fake is lifecycle_test.go's a4DatalessFS, whose own comment says "the
// clean EOF is the dangerous one and is what this fake reproduces" — it was
// pointed at the read path only. Here it is pointed at a write.
//
// The measured behaviour before the fix, on a 62-byte note:
//
//	ReadNoteContent: REFUSED — stat reports 62 bytes but the read returned none
//	EditNote round 1: conflict, actual=v1:af5570f5a1810b7af78caf4bc70a660f
//	EditNote round 2 (using that token): err=<nil> changed=true
//	on disk after: 22 bytes, "---\nstatus: final\n---\n"
//
// af5570f5… is the hash of EMPTY content. The refusal handed the caller the
// key to the clobber, and the retry protocol vault_edit's description teaches
// walks a model straight through it. That is why this test does not stop at
// "the first attempt is refused": it walks the retry too.
func TestEditNote_RefusesAnEvictedNoteInsteadOfWritingBackAStub(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)

	const original = "---\nstatus: draft\n---\n# Q3 Board Deck\n\n" +
		"Eight hundred words of the operator's own prose, none of which Omnipus wrote.\n"
	abs := a1Note(t, root, "Deck.md", original, 0o600)

	fi, err := os.Stat(abs)
	require.NoError(t, err)
	require.Positive(t, fi.Size(), "fixture check: the note must have a non-zero size, "+
		"or the size disagreement FR-111 detects does not exist")

	fsys := a4NewDatalessFS()
	fsys.dataless[abs] = fi.Size()

	// Fixture check, and the oracle: this is precisely the shape the READ path
	// already refuses. Anything the write path does short of refusing it is a
	// disagreement between the two sides about the same file.
	_, readErr := ReadNoteContent(fsys, abs)
	require.ErrorIs(t, readErr, ErrNoteEvicted,
		"fixture check: the fake must present the dataless shape the read path refuses")

	rec := &a1Recorder{}
	edit := func(expect string) (EditNoteResult, error) {
		return EditNote(fsys, c, EditNoteRequest{
			RelPath:       "Deck.md",
			Edits:         []NoteEdit{SetProperty("status", "final")},
			ExpectVersion: expect,
			Audit:         rec,
		})
	}

	_, err = edit(NoteContentVersion([]byte(original)))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoteEvicted,
		"an edit must refuse an evicted note by the same rule the read path uses")

	// The refusal must NOT be a version conflict. A conflict carries the
	// note's "current" token, and for an evicted note that token is the hash
	// of empty content — handing it back is handing over the key.
	var conflict *ConflictError
	assert.False(t, errors.As(err, &conflict),
		"an evicted note must not be reported as a version conflict: the token such a "+
			"refusal carries is the hash of EMPTY content, which is exactly what makes "+
			"the documented retry complete the clobber (got %v)", err)

	// The retry protocol, walked end to end. Every token a caller could
	// plausibly arrive at must be refused, including the empty-content hash
	// the old conflict handed out and the empty token.
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"the empty-content hash the old conflict handed back", NoteContentVersion(nil)},
		{"no token at all", ""},
		{"the note's real token", NoteContentVersion([]byte(original))},
	} {
		t.Run("retry with "+tc.name, func(t *testing.T) {
			res, rerr := edit(tc.token)
			require.Error(t, rerr, "no token may unlock a write against an evicted note")
			assert.ErrorIs(t, rerr, ErrNoteEvicted)
			assert.False(t, res.Changed)
		})
	}

	onDisk, err := os.ReadFile(abs)
	require.NoError(t, err)
	assert.Equal(t, original, string(onDisk),
		"the operator's note must be byte-identical: this is the assertion that fails "+
			"when an evicted read is spliced onto nothing and written back")

	require.NotEmpty(t, rec.records, "FR-090: every refusal is audited")
	for i, r := range rec.records {
		assert.Equal(t, AuthorOutcomeRefused, r.Outcome, "record %d", i)
	}
}

// ---------------------------------------------------------------------------
// FINDING 3 — the reserved-location guard, on EVERY write op.
// ---------------------------------------------------------------------------

// veWriteOp is one vault_edit op plus the arguments that make it do real work.
// `create` is the only one that must NOT find a file already there, so the
// table carries that difference rather than a second table.
type veWriteOp struct {
	op       string
	creates  bool
	extraFor func(version string) map[string]any
}

// veWriteOps enumerates every op vault_edit can mutate a collection with.
//
// It is derived from vaultEditOps — the tool's own list — rather than
// hand-written, so an op added to the tool and not to this table fails here
// instead of shipping untested. That is the same reasoning reserved_setwide_test.go
// applies to scanSkippedDirNames, and the failure it prevents is exactly the
// one that produced these findings: a guard proven for one op and absent from
// the other four.
func veWriteOps(t *testing.T) []veWriteOp {
	t.Helper()
	all := []veWriteOp{
		{op: opCreate, creates: true, extraFor: func(string) map[string]any {
			return map[string]any{"body": "planted by an agent\n"}
		}},
		{op: opSetProperty, extraFor: func(v string) map[string]any {
			return map[string]any{"property": "status", "value": "active", "expect_version": v}
		}},
		{op: opAppendSection, extraFor: func(v string) map[string]any {
			return map[string]any{"heading": "Injected", "body": "planted", "expect_version": v}
		}},
		{op: opLink, extraFor: func(v string) map[string]any {
			return map[string]any{"target": "Somewhere Else", "expect_version": v}
		}},
		{op: opReplaceBody, extraFor: func(v string) map[string]any {
			return map[string]any{"body": "planted by an agent\n", "expect_version": v}
		}},
	}
	covered := make([]string, 0, len(all))
	for _, o := range all {
		covered = append(covered, o.op)
	}
	sort.Strings(covered)
	declared := append([]string(nil), vaultEditOps...)
	sort.Strings(declared)
	require.Equal(t, declared, covered,
		"every op vault_edit can write with must be in this table; an op the tool "+
			"accepts and this table omits is an op no blast-radius test covers")
	return all
}

// veReservedNames returns the tool-state directory names, from the walker's
// own skip set. Deriving them means a name added to scanSkippedDirNames is
// covered here automatically, rather than opening a hole nothing notices.
func veReservedNames(t *testing.T) []string {
	t.Helper()
	names := make([]string, 0, len(scanSkippedDirNames))
	for n := range scanSkippedDirNames {
		names = append(names, n)
	}
	sort.Strings(names)
	require.GreaterOrEqual(t, len(names), 4,
		"precondition: scanSkippedDirNames must name at least .obsidian, .omnipus-vault, .git and .trash")
	return names
}

// TestVaultEdit_EveryWriteOpRefusesAReservedLocation is the end-to-end form of
// finding 3, through the tool an agent actually calls.
//
// Measured before the fix, with the audit saying outcome=applied each time:
//
//	.obsidian/app.json      MUTATED=true
//	.git/config             MUTATED=true
//	.trash/Deleted.md       MUTATED=true
//	.omnipus-vault/state.md MUTATED=true
//
// Those are the operator's Obsidian configuration, a real git repository's
// config, Obsidian's soft-delete folder and Omnipus's own tool state.
func TestVaultEdit_EveryWriteOpRefusesAReservedLocation(t *testing.T) {
	for _, dir := range veReservedNames(t) {
		for _, w := range veWriteOps(t) {
			t.Run(dir+"/"+w.op, func(t *testing.T) {
				home, ws, root := a4Fixture(t, "kb")
				deps, audit := a4Deps(home)
				tool := veTool(deps)

				rel := dir + "/victim.md"
				const before = "---\nstatus: draft\n---\nOperator content.\n"
				full := filepath.Join(root, filepath.FromSlash(rel))
				version := ""
				if w.creates {
					rel = dir + "/planted.md"
					full = filepath.Join(root, filepath.FromSlash(rel))
					require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o700))
				} else {
					a4Note(t, root, rel, before)
					version = a4Version(t, root, rel)
				}

				args := map[string]any{"collection": "kb", "op": w.op, "path": rel}
				for k, v := range w.extraFor(version) {
					args[k] = v
				}
				res := tool.Execute(a4Ctx("mia", ws), args)

				require.True(t, res.IsError,
					"%s into %s/ must be refused; got %q", w.op, dir, res.ForLLM)
				assert.Contains(t, strings.ToLower(res.ForLLM), "reserved location",
					"the refusal must name the rule that made it, so an agent can stop "+
						"retrying rather than reword the path")

				if w.creates {
					assert.NoFileExists(t, full,
						"a refused create must leave nothing behind anywhere")
				} else {
					after, rerr := os.ReadFile(full)
					require.NoError(t, rerr)
					assert.Equal(t, before, string(after),
						"%s must not have touched a byte of %s", w.op, rel)
				}
				assert.Empty(t, audit.applied(),
					"FR-090: a refused write must never be audited as applied")
			})
		}
	}
}

// ---------------------------------------------------------------------------
// FINDING 6 — a write must land on the file the caller named.
// ---------------------------------------------------------------------------

// veSymlinkFixture builds a collection holding a real Archive/ directory with
// one note in it, plus an Inbox symlink pointing at Archive. Everything stays
// INSIDE the collection, so containment alone accepts every path through it —
// which is what makes this the case the old tests missed.
func veSymlinkFixture(t *testing.T) (home, ws, root string) {
	t.Helper()
	home, ws, root = a4Fixture(t, "kb")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "Archive"), 0o700))
	require.NoError(t, os.Symlink(filepath.Join(root, "Archive"), filepath.Join(root, "Inbox")))
	return home, ws, root
}

// TestVaultEdit_EveryWriteOpRefusesAPathReachedThroughAnInCollectionSymlink is
// finding 6 through the tool, for every op.
//
// Measured before the fix:
//
//	vault_edit create path="Inbox/New.md"   (Inbox -> Archive/)
//	AUDIT: outcome=applied paths=[Inbox/New.md]   landed at Archive/New.md
//	vault_read "Inbox/New.md": REFUSED — reaches ".../Archive/New.md" only
//	                                     through a symbolic link
//
// So the FR-090 audit record named a path where no file existed, and the agent
// could not read back what it had just been told it wrote.
func TestVaultEdit_EveryWriteOpRefusesAPathReachedThroughAnInCollectionSymlink(t *testing.T) {
	for _, w := range veWriteOps(t) {
		t.Run(w.op, func(t *testing.T) {
			home, ws, root := veSymlinkFixture(t)
			deps, audit := a4Deps(home)
			tool := veTool(deps)

			const before = "---\nstatus: draft\n---\nOperator content.\n"
			realNote := filepath.Join(root, "Archive", "Target.md")
			rel := "Inbox/Target.md"
			version := ""
			if w.creates {
				rel = "Inbox/New.md"
			} else {
				a4Note(t, root, "Archive/Target.md", before)
				version = a4Version(t, root, "Archive/Target.md")
			}

			args := map[string]any{"collection": "kb", "op": w.op, "path": rel}
			for k, v := range w.extraFor(version) {
				args[k] = v
			}
			res := tool.Execute(a4Ctx("mia", ws), args)

			require.True(t, res.IsError,
				"%s through a symlinked folder must be refused; got %q", w.op, res.ForLLM)
			assert.Contains(t, res.ForLLM, "symbolic link",
				"the refusal must say a link was involved — the whole defect is that the "+
					"named path and the written path differ")

			if w.creates {
				assert.NoFileExists(t, filepath.Join(root, "Archive", "New.md"),
					"a create the caller addressed as Inbox/New.md must never appear in Archive/")
			} else {
				after, rerr := os.ReadFile(realNote)
				require.NoError(t, rerr)
				assert.Equal(t, before, string(after),
					"Archive/Target.md is a file the caller never named")
			}
			assert.Empty(t, audit.applied(),
				"FR-090: a refused write must never be audited as applied")
		})
	}
}

// TestAuthorWriteTarget_RefusesASymlinkWhoseTargetIsInsideTheCollection is the
// case author_test.go's "a symlink out of the collection" does not cover, and
// it is isolated from that one by construction.
//
// Every case here REQUIRES ResolveContained — plain containment, FR-043 — to
// ACCEPT the path before asserting that the write is refused. That
// precondition is what makes the test about FR-044 and nothing else: with the
// symlink rule removed, containment still passes and the assertion fails. The
// existing outside-the-collection case would keep passing with the symlink
// rule removed, which is why it is not evidence for this.
func TestAuthorWriteTarget_RefusesASymlinkWhoseTargetIsInsideTheCollection(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	c := a1Collection(t, root)

	const before = "---\nstatus: draft\n---\nOperator content.\n"
	a1Note(t, root, "Archive/Target.md", before, 0o600)
	a1Note(t, root, "Real.md", before, 0o600)
	require.NoError(t, os.Symlink(filepath.Join(root, "Archive"), filepath.Join(root, "Inbox")))
	require.NoError(t, os.Symlink(filepath.Join(root, "Real.md"), filepath.Join(root, "Alias.md")))

	cr, err := NewCollectionRoot(OSLinkFS(), root)
	require.NoError(t, err)

	for _, tc := range []struct {
		name string
		rel  string
		real string // the file that must remain untouched
	}{
		{"a leaf symlink to a note inside the collection", "Alias.md", "Real.md"},
		{"a directory symlink inside the collection", "Inbox/Target.md", "Archive/Target.md"},
		{"a create through a directory symlink inside the collection", "Inbox/New.md", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The precondition that isolates this case: containment ALONE
			// accepts it. Without this the test could be passing for the same
			// reason the outside-the-collection case passes.
			resolved, cErr := cr.ResolveContained(OSLinkFS(), tc.rel)
			require.NoError(t, cErr,
				"fixture check: FR-043 containment must ACCEPT %q, or this case is not "+
					"the one it claims to be", tc.rel)
			require.True(t, cr.Contains(resolved),
				"fixture check: %q must resolve to somewhere INSIDE the collection", tc.rel)
			require.NotEqual(t, filepath.Join(root, filepath.FromSlash(tc.rel)), resolved,
				"fixture check: the resolved path must differ from the named one, or no "+
					"symlink was traversed and there is nothing here to refuse")

			_, wErr := authorWriteTarget(OSLinkFS(), cr, tc.rel)
			require.Error(t, wErr, "the write path must refuse %q", tc.rel)
			assert.ErrorIs(t, wErr, ErrOutsideCollection)

			_, eErr := EditNote(OSLinkFS(), c, EditNoteRequest{
				RelPath:       tc.rel,
				Edits:         []NoteEdit{AppendSection("Injected", "planted")},
				ExpectVersion: NoteContentVersion([]byte(before)),
			})
			require.Error(t, eErr, "EditNote must refuse %q", tc.rel)
			assert.ErrorIs(t, eErr, ErrOutsideCollection)

			_, cnErr := CreateNote(OSLinkFS(), c, CreateNoteRequest{
				RelPath: tc.rel, Body: []byte("planted\n"), NameShape: OperatorNameShape,
			})
			require.Error(t, cnErr, "CreateNote must refuse %q", tc.rel)
			assert.ErrorIs(t, cnErr, ErrOutsideCollection)

			if tc.real != "" {
				after, rErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(tc.real)))
				require.NoError(t, rErr)
				assert.Equal(t, before, string(after),
					"%s is the file the caller never named", tc.real)
			} else {
				assert.NoFileExists(t, filepath.Join(root, "Archive", "New.md"))
			}
		})
	}
}

// TestWritePathAndReadPathAgreeOnWhatIsAddressable is the property the split
// guards violated: the two tools must give the same verdict on the same path.
//
// It is asserted as an equality between the two entry points rather than as
// two independent expectations, so a future change that loosens one side
// without the other fails here even if both sides remain individually
// defensible.
func TestWritePathAndReadPathAgreeOnWhatIsAddressable(t *testing.T) {
	root := a1Vault(t, "Research", nil)
	outside := a1Real(t, t.TempDir())
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("PRIVATE\n"), 0o600))

	a1Note(t, root, "Archive/Target.md", "x\n", 0o600)
	a1Note(t, root, "Real.md", "x\n", 0o600)
	require.NoError(t, os.Symlink(filepath.Join(root, "Archive"), filepath.Join(root, "Inbox")))
	require.NoError(t, os.Symlink(filepath.Join(root, "Real.md"), filepath.Join(root, "Alias.md")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "Escape.md")))

	cr, err := NewCollectionRoot(OSLinkFS(), root)
	require.NoError(t, err)

	for _, rel := range []string{
		"Real.md",            // an ordinary note: both must ACCEPT
		"Archive/Target.md",  // an ordinary note in a folder: both must ACCEPT
		"Notes/Brand New.md", // does not exist yet: both must ACCEPT
		"Alias.md",           // leaf symlink, target inside
		"Inbox/Target.md",    // directory symlink, target inside
		"Escape.md",          // leaf symlink, target outside
		"../escaped.md",      // traversal
	} {
		t.Run(rel, func(t *testing.T) {
			readAbs, readErr := retrievalPath(OSLinkFS(), cr, rel)
			writeAbs, writeErr := authorWriteTarget(OSLinkFS(), cr, rel)
			assert.Equal(t, readErr == nil, writeErr == nil,
				"vault_read and vault_edit must agree about whether %q is addressable; "+
					"read=%v write=%v", rel, readErr, writeErr)
			if readErr == nil && writeErr == nil {
				assert.Equal(t, readAbs, writeAbs,
					"when both accept %q they must name the same file", rel)
			}
		})
	}
}
