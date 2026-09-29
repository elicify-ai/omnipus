// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 45 (SECURITY), 44, 46, 47 and 65.
//
// SECURITY CONTEXT (D-PROVENANCE, round 2, resolves R2-CRIT-001/R2-CRIT-002):
// the spec requires a PIPELINE-OWNED MEMBERSHIP RECORD, not the `derived_from`
// field alone, to decide which files re-derivation may rewrite or delete.
//
// UNBLOCKED (2026-09-29, va-qa2 dispatch): generated.ViewDef.DerivedFrom now
// exists (contract commit 562acdc32, merged from origin/feat/library-views-anywhere)
// — verified: `grep -n 'DerivedFrom \*string' pkg/api/generated/openapi_types.gen.go`
// hits at line ~25016, and `records.ParseView` decodes into `generated.ViewDef`
// via `json.Decoder.DisallowUnknownFields` (pkg/records/view.go), so a
// `derived_from:` key now parses cleanly instead of being rejected as
// RejectViewUnknownKey — verified empirically, see receipts row44-red.log /
// row45-red.log / row46-dedup-check.log. Tests 44/45/46 are rewritten below as
// real, executable assertions against RederiveBase, the real entry point.
//
// STILL MISSING (verified: zero hits for "ViewMembership"/"membership record"
// outside comments, and zero hits for ".DerivedFrom" outside generated code
// and this test file — `grep -rln '\.DerivedFrom\b' pkg/ --include='*.go' |
// grep -v _test.go | grep -v pkg/api/generated` is empty): there is still NO
// pipeline-owned membership record, and NOTHING in production code consults
// `Def.DerivedFrom` for any decision. RederiveBase's ENTIRE "mine" computation
// (fileTranslatedBase, rederive.go) remains exactly `v.DeclaredSource() ==
// baseRelPath` — a plain `source:` string match, unconditioned on
// `derived_from` at all. That is the live vulnerability these tests exercise
// TODAY: `source` is exactly as attacker-controlled as `derived_from` would
// be, and is the ONLY of the two fields the current write/delete decision
// actually trusts.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_IgnoresHandAddedDerivedFromOnForeignFile$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_IgnoresCopiedDerivedFromField$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_RefusesOccupiedPathNotOwnRecord$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_TwoFilesClaimingSameDerivedFromAndName$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_TakesLockBeforeWriteOrDelete$' ./pkg/vaultimport/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestRederive_IgnoresHandAddedDerivedFromOnForeignFile is TDD Plan test 45 —
// SECURITY-CRITICAL (R2-CRIT-001, FR-VA-008a, Dataset F-9). A `derived_from`
// value hand-added directly to a file the pipeline never wrote, naming the
// `.base` under test, must not let that `.base`'s next re-derivation rewrite
// or delete the file — only a pipeline-owned membership record may decide
// "mine," never a field the attacker fully controls by editing a text file.
//
// REAL, EXECUTABLE, no longer BLOCKED: `derived_from` now parses (see file
// header). The attacker file below sets BOTH `derived_from` (the field the
// spec says must never decide) AND `source` (the field that ACTUALLY decides
// today) to `Projects.base`, under a name ("attacker-claimed") the base's own
// translation never produces. This is the real R2-CRIT-001 scenario, not a
// vacuous one: today's `mine` computation sweeps this file in via the
// `source` match alone — `derived_from` gives it no protection at all — and
// the file is DELETED because its name is absent from `producedSlugs`.
func TestRederive_IgnoresHandAddedDerivedFromOnForeignFile(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))
	foreignPath := filepath.Join(records.ViewsDir(root), "attacker-claimed.yaml")
	require.NoError(t, os.WriteFile(foreignPath,
		[]byte("name: attacker-claimed\nlabel: Looks derived\nsource: Projects.base\nderived_from: Projects.base\n"), 0o600))

	// Plumbing fact, asserted first: the real loader now populates
	// DerivedFrom from the hand-added key rather than rejecting the file.
	schemaSet, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	loaded, report, err := records.LoadViews(root, schemaSet)
	require.NoError(t, err)
	require.Empty(t, report.Rejections, "the file must load cleanly now that derived_from parses: %+v", report.Rejections)
	sv, ok := loaded.Get("attacker-claimed")
	require.True(t, ok, "the hand-added file must be a loaded view")
	require.NotNil(t, sv.Def.DerivedFrom, "DerivedFrom must be populated from the hand-added key")
	require.Equal(t, "Projects.base", *sv.Def.DerivedFrom)

	// The security assertion: re-deriving the base this file's derived_from
	// (and source) NAME must not touch it.
	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)

	if _, statErr := os.Stat(foreignPath); statErr != nil {
		t.Fatalf(
			"R2-CRIT-001/FR-VA-008a/Dataset F-9: a hand-added derived_from (naming this exact .base) gave "+
				"this file NO protection — it was deleted by this run (stat error: %v). Deleted=%v. Today's "+
				"mine-decision (rederive.go::fileTranslatedBase) is a bare `source` string match with no "+
				"membership-record check at all — exactly the field-trusts-attacker-input failure D-PROVENANCE "+
				"exists to close.",
			statErr, res.Deleted,
		)
	}
	for _, slug := range res.Deleted {
		if slug == "attacker-claimed" {
			t.Fatalf("attacker-claimed was reported Deleted=%v — a derived_from/source claim the pipeline "+
				"never made must never license a delete", res.Deleted)
		}
	}
}

// TestRederive_IgnoresCopiedDerivedFromField is TDD Plan test 44 (R2-CRIT-001,
// FR-VA-008a, Dataset F-8): a copy of a derived view — with `derived_from`
// stripped by D-DUPLICATE before it can even reach re-derivation — must never
// be rewritten or deleted by the next re-derivation of the `.base` it used to
// name, and must be absent from that `.base`'s membership record.
//
// REAL, EXECUTABLE: the ORIGINAL carries both `source` and `derived_from`
// (simulating a genuinely pipeline-produced view); the COPY keeps `source`
// (D-DUPLICATE does not touch it) but has `derived_from` stripped, under an
// auto-suffixed name the base's translation never produces. Today's
// mine-computation sweeps the COPY in via `source` alone and deletes it,
// because D-DUPLICATE's whole point — the copy is no longer managed — is
// invisible to a mechanism that never looks at derived_from either way.
//
// PRIORITY 1 scenario 1 also requires this survival to hold across the
// .base's own DELETE, not only its re-derivation. That second half stays
// separately reported: grepping every pkg/knowledge/knowledge_restructure*.go
// file for the string ".base" returns zero hits — (*Trasher).Trash has no
// `.base`-aware branch at all today, so it is not yet a security-relevant
// fact to falsify (test 51, TestLibraryKnowledgeCascade_DeleteReleasesDerivedViews,
// covers the delete-release half separately, below).
func TestRederive_IgnoresCopiedDerivedFromField(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)
	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))

	originalPath := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
	require.NoError(t, os.WriteFile(originalPath,
		[]byte("name: projects--open\nlabel: Open\nsource: Projects.base\nderived_from: Projects.base\n"), 0o600))

	copyPath := filepath.Join(records.ViewsDir(root), "projects--open-2.yaml")
	require.NoError(t, os.WriteFile(copyPath,
		[]byte("name: projects--open-2\nlabel: Open (copy)\nsource: Projects.base\n"), 0o600))

	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)

	if _, statErr := os.Stat(copyPath); statErr != nil {
		t.Fatalf(
			"R2-CRIT-001/FR-VA-008a/Dataset F-8: the copy (derived_from stripped per D-DUPLICATE, source "+
				"retained) was deleted by this run (stat error: %v). Deleted=%v. A copy that lost its "+
				"derived_from must be treated as an ordinary hand-made file, not swept in by a leftover "+
				"`source` match.",
			statErr, res.Deleted,
		)
	}
	for _, slug := range res.Deleted {
		if slug == "projects--open-2" {
			t.Fatalf("the derived_from-stripped copy was reported Deleted=%v — D-DUPLICATE's strip must "+
				"actually remove it from re-derivation's authority, not just from the wire field", res.Deleted)
		}
	}
}

// TestRederive_RefusesOccupiedPathNotOwnRecord is TDD Plan test 47
// (R2-CRIT-002, FR-VA-008d, Dataset D10): re-derivation writing a
// NEWLY-declared view must never overwrite a path already occupied by a file
// that is not already in its own membership record for this .base — it must
// pick a free (suffixed) path instead.
//
// RE-TARGETED to the spec's real write location per va-qa2 dispatch (FR-VA-008:
// the sibling `<slug>.view` file beside the `.base`, not the legacy
// .omnipus-vault/views/ directory). The earlier pass found the naive version
// of this ("plant the occupant at the sibling path, assert it survives")
// PASSES VACUOUSLY today (receipt: row47-siblingpath-vacuous-check.log) —
// verified by reading view_translate.go::translateOneView, which hardcodes
// `records.ViewsDirName + "/" + slug + ".yaml"`, so nothing writes beside the
// `.base` at all yet (test 5, TestVaultImport_WritesViewBesideBaseFile, is
// independently RED for that same reason).
//
// This version closes that vacuous-pass gap: it asserts NOT ONLY that the
// occupant survives untouched, but that the "Open" view is written to SOME
// OTHER sibling path next to Projects.base (a suffixed name, since the exact
// occupied slug is taken) — a claim that CANNOT pass today, because no writer
// puts anything beside Projects.base at all. The test therefore fails on the
// second assertion for the right reason (missing sibling-path writer), and
// will fail differently (an actual overwrite) if a sibling-path writer lands
// without the occupied-path check — either way it stays a real proof of
// FR-VA-008d, not a test that can pass by construction.
func TestRederive_RefusesOccupiedPathNotOwnRecord(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	// The spec's real target: <base-dir>/projects--open.view, sibling to
	// Projects.base itself (root/Projects.base in this fixture).
	occupiedPath := filepath.Join(root, "projects--open.view")
	unrelatedContent := "name: someone-elses-file\n# not produced by this .base's translation\n"
	require.NoError(t, os.WriteFile(occupiedPath, []byte(unrelatedContent), 0o600))

	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)

	got, rerr := os.ReadFile(occupiedPath)
	require.NoError(t, rerr, "the occupied path must still exist")
	if string(got) != unrelatedContent {
		t.Fatalf(
			"FR-VA-008d/R2-CRIT-002/Dataset D10: re-derivation overwrote %s, which was occupied by a file "+
				"NOT in this .base's membership record\nwant (unchanged): %q\ngot: %q",
			occupiedPath, unrelatedContent, string(got),
		)
	}

	entries, rderr := os.ReadDir(root)
	require.NoError(t, rderr)
	var suffixedSibling string
	for _, e := range entries {
		name := e.Name()
		if name == "projects--open.view" || name == "Projects.base" {
			continue
		}
		if strings.HasPrefix(name, "projects--open") && strings.HasSuffix(name, ".view") {
			suffixedSibling = name
		}
	}
	if suffixedSibling == "" {
		t.Fatalf(
			"FR-VA-008d/R2-CRIT-002/Dataset D10: the occupied path was left alone (good), but the "+
				"\"Open\" view was never written to a suffixed sibling path beside Projects.base either — "+
				"root now contains only %v. FR-VA-008's sibling-`.view`-file writer has not landed, so the "+
				"view has nowhere free to go; that missing writer is the seam this test names.",
			direntNames(entries),
		)
	}
}

func direntNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// TestRederive_TwoFilesClaimingSameDerivedFromAndName is TDD Plan test 46
// (R2-CRIT-001, Dataset F-10): two files sharing BOTH `derived_from` and
// `name` (a sync-conflict shape) must be touched by NEITHER re-derivation
// run — no file may be picked as "the" managed one on ambiguous evidence.
//
// EMPIRICAL FINDING (real, not fabricated red): with a shared `name`, the
// PRE-EXISTING, UNRELATED dedup mechanism (records.loadViewPaths's
// RejectViewDuplicateName, keyed purely on `Def.Name`, verified by reading
// pkg/records/view.go) excludes BOTH files from `existing.Views()` before
// RederiveBase's mine-computation ever runs — so "neither is touched" is
// TRUE today, but for a reason that has NOTHING to do with derived_from or
// any membership record. This is reported rather than dressed up as
// CRIT-001 coverage: this test asserts the (currently true, worth
// protecting) top-level outcome AND separately proves — with a second pair
// of files sharing `derived_from` but NON-colliding names, so dedup cannot
// shield them — that the underlying ambiguity is unresolved by anything
// but that accident: both are swept into `mine` by a shared, spoofed
// `source` and BOTH get deleted, which is the real, falsifiable violation
// of "no file is picked as the managed one" (here, neither survives at
// all, which is equally a violation of Dataset F-10's expectation that
// re-derivation "touches NEITHER").
func TestRederive_TwoFilesClaimingSameDerivedFromAndName(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)
	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))

	// Part A — the literal Dataset F-10 shape: same derived_from, same name.
	// Verified true today via dedup rejection; asserted as a regression
	// guard, not claimed as CRIT-001 coverage (see doc comment).
	pathA := filepath.Join(records.ViewsDir(root), "sync-conflict-a.yaml")
	pathB := filepath.Join(records.ViewsDir(root), "sync-conflict-b.yaml")
	body := []byte("name: sync-conflict\nlabel: Sync conflict\nderived_from: Projects.base\n")
	require.NoError(t, os.WriteFile(pathA, body, 0o600))
	require.NoError(t, os.WriteFile(pathB, body, 0o600))

	// Part B — same derived_from, DIFFERENT (non-dedup-colliding) names, both
	// claiming `source` too (the field that actually decides today). This is
	// the real, currently-falsifiable half.
	pathC := filepath.Join(records.ViewsDir(root), "sync-conflict-c.yaml")
	pathD := filepath.Join(records.ViewsDir(root), "sync-conflict-d.yaml")
	require.NoError(t, os.WriteFile(pathC,
		[]byte("name: sync-conflict-c\nlabel: Claimant C\nsource: Projects.base\nderived_from: Projects.base\n"), 0o600))
	require.NoError(t, os.WriteFile(pathD,
		[]byte("name: sync-conflict-d\nlabel: Claimant D\nsource: Projects.base\nderived_from: Projects.base\n"), 0o600))

	res, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)

	for _, p := range []string{pathA, pathB} {
		if _, statErr := os.Stat(p); statErr != nil {
			t.Fatalf("Dataset F-10 part A (same name, dedup-shielded): %s was removed (stat error: %v) — "+
				"even the accidental protection regressed", p, statErr)
		}
	}

	survivedC := fileExists(pathC)
	survivedD := fileExists(pathD)
	if !survivedC || !survivedD {
		t.Fatalf(
			"R2-CRIT-001/Dataset F-10 part B: two files claiming the SAME derived_from via distinct names "+
				"must both survive re-derivation of the .base they name (\"no file is picked as the managed "+
				"one\" — here, neither should be touched at all). Today's source-based mine-computation has "+
				"no ambiguity check: survivedC=%v survivedD=%v, Deleted=%v",
			survivedC, survivedD, res.Deleted,
		)
	}
}

// TestRederive_TakesLockBeforeWriteOrDelete is TDD Plan test 65 (R2-MIN-002,
// FR-VA-024): re-derivation's write/delete step on a view file must take
// the SAME lock key a concurrent write_view/Library save on that file would
// take (D-LOCK), so the two paths always serialize through
// WithNoteWriteLock.
//
// REAL, EXECUTABLE, deterministic (no sleeps): this test holds the exact
// lock key write_view's own control-plane writer would take for the view
// path RederiveBase is about to write
// (knowledge.WithNoteWriteLock/pkg/knowledge/knowledge_configure.go::
// controlPlaneLockKey — unexported, so its computation is reproduced here
// byte-for-byte: verified by reading it, controlPlaneLockKey(root, abs) is
// exactly filepath.ToSlash(filepath.Rel(root, abs)), nothing more), in a
// goroutine that blocks until released. A channel proves the lock is held
// before RederiveBase is called, and the view file's existence is checked
// synchronously the instant RederiveBase returns — still inside the window
// where the lock-holder has NOT yet been released. rederive.go/run.go never
// call WithNoteWriteLock or controlPlaneLockKey at all (grep confirms zero
// matches), so RederiveBase writes the file immediately, before the lock is
// released — the assertion below is the real, currently-failing proof.
func TestRederive_TakesLockBeforeWriteOrDelete(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	viewAbs := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
	relKey, relErr := filepath.Rel(root, viewAbs)
	require.NoError(t, relErr)
	lockKey := filepath.ToSlash(relKey)

	lockAcquired := make(chan struct{})
	release := make(chan struct{})
	lockErrCh := make(chan error, 1)
	go func() {
		lockErrCh <- knowledge.WithNoteWriteLock(knowledge.NoteLockConfig{CollectionRoot: root}, lockKey, func() error {
			close(lockAcquired)
			<-release
			return nil
		})
	}()

	<-lockAcquired // deterministic barrier: the lock is provably held from here on

	res, err := RederiveBase(root, "Projects.base")
	// Captured WHILE the lock is still held — `release` has not been closed yet.
	_, statErr := os.Stat(viewAbs)
	wroteWhileLockHeld := statErr == nil

	close(release)
	require.NoError(t, <-lockErrCh)
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, res.Status, "reason: %s", res.RefusedReason)

	if wroteWhileLockHeld {
		t.Fatalf(
			"FR-VA-024/R2-MIN-002: RederiveBase wrote %s while the per-file rederive lock "+
				"(WithNoteWriteLock/controlPlaneLockKey, key %q) was held by a concurrent writer — "+
				"re-derivation's write/delete step must take the SAME lock key before touching the file, "+
				"so it serializes behind a concurrent write_view/Library save instead of racing it",
			viewAbs, lockKey,
		)
	}
}
