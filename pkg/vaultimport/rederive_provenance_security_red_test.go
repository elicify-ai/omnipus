// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 45 (SECURITY), 44, 46, 47 and 65.
//
// SECURITY CONTEXT (D-PROVENANCE, round 2, resolves R2-CRIT-001/R2-CRIT-002):
// the spec requires a PIPELINE-OWNED MEMBERSHIP RECORD, not the `derived_from`
// field alone, to decide which files re-derivation may rewrite or delete —
// "the file most under attacker control (its own content) is exactly the
// input re-derivation's write/delete decision no longer trusts." Today
// neither the field nor the record exists at all:
//
//	$ grep -rn "derived_from\|DerivedFrom\|ViewMembership\|membership record" \
//	      pkg/vaultimport pkg/knowledge pkg/records --include='*.go'
//	(zero matches for the actual concept — only coincidental substring hits
//	 in unrelated identifiers, e.g. TestIntentionallyStopped_DerivedFromClosedEnums)
//
// generated.ViewDef (pkg/api/generated/openapi_types.gen.go) has no
// DerivedFrom field (verified: zero hits for "DerivedFrom"/"derived_from" in
// that generated file, and no contracts/components/schemas/ViewDef.yaml
// exists at all). records.ParseView decodes with
// json.Decoder.DisallowUnknownFields (pkg/records/view.go::ParseView), so a
// YAML file that even TRIES to carry a `derived_from:` key is rejected
// outright as RejectViewUnknownKey — verified empirically below by every one
// of tests 44/45/46, not merely asserted in a comment — before any
// provenance-authority decision could be made. There is no code path to
// drive R2-CRIT-001's actual scenario through yet, so these three tests
// remain BLOCKED per the qa-lead RED protocol, now with the blocker itself
// proven by a real, executable assertion rather than cited from memory: if
// the contract field lands and one of these empirical checks starts
// failing, that is the signal this BLOCKED test has gone stale and must be
// rewritten as the real scenario it names.
//
// Tests 47 and 65 need no new field — both are testable today directly
// against fileTranslatedBase's real, unconditional write/delete loop, so
// both are written as real, compiling, currently-failing assertions.
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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// TestRederive_IgnoresHandAddedDerivedFromOnForeignFile is TDD Plan test 45 —
// SECURITY-CRITICAL (R2-CRIT-001, FR-VA-008a, Dataset F-9). A `derived_from`
// value hand-added directly to a file the pipeline never wrote, naming an
// UNRELATED `.base`, must not let that `.base`'s next re-derivation rewrite
// or delete the file — only the pipeline's own membership record decides
// "mine," never a field a user or attacker fully controls by editing a text
// file. Per the spec's own words: "this MUST be impossible or ignored, not
// merely discouraged."
//
// BLOCKED, verified empirically, not merely asserted: neither
// generated.ViewDef.DerivedFrom nor a pipeline-owned membership record exists
// anywhere in the tree. This test drives the REAL entry point
// (records.LoadViews, the same loader RederiveBase itself calls) with a hand
// -added `derived_from` naming a foreign `.base`, and asserts what actually
// happens today: records.ParseView's DisallowUnknownFields rejects the file
// as RejectViewUnknownKey before re-derivation's authority question could
// even be asked. If this rejection-code assertion itself starts failing, the
// contract has changed and this BLOCKED test is stale.
func TestRederive_IgnoresHandAddedDerivedFromOnForeignFile(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))
	foreignPath := filepath.Join(records.ViewsDir(root), "foreign-hand-added.yaml")
	require.NoError(t, os.WriteFile(foreignPath,
		[]byte("name: foreign-hand-added\nlabel: Not mine\nderived_from: SomeoneElse.base\n"), 0o600))

	schemaSet, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	_, report, err := records.LoadViews(root, schemaSet)
	require.NoError(t, err)

	var gotCode records.ViewRejectionCode
	for _, rej := range report.Rejections {
		for _, p := range rej.Paths {
			if p == foreignPath {
				gotCode = rej.Code
			}
		}
	}
	if gotCode != records.RejectViewUnknownKey {
		t.Fatalf(
			"expected the real loader to reject the hand-added `derived_from` key as %q (the contract has "+
				"no such field yet) — got rejection code %q for %s, report: %+v. If a real code now exists, "+
				"this BLOCKED test is stale and must become the real R2-CRIT-001 scenario.",
			records.RejectViewUnknownKey, gotCode, foreignPath, report.Rejections,
		)
	}

	t.Fatal("BLOCKED: ViewDef.derived_from (contract FR-VA-009a) and the pipeline-owned membership " +
		"record (D-PROVENANCE, R2-CRIT-001) are not implemented anywhere in pkg/vaultimport, " +
		"pkg/knowledge or pkg/api/generated — verified above that the real loader (records.LoadViews) " +
		"rejects any derived_from key as RejectViewUnknownKey, so a hand-added derived_from on a foreign " +
		"file cannot even reach a ViewSet entry, let alone re-derivation's write/delete decision — " +
		"required before FR-VA-008a / Dataset F-9 / TDD test 45 can be exercised for real.")
}

// TestRederive_IgnoresCopiedDerivedFromField is TDD Plan test 44 (R2-CRIT-001,
// FR-VA-008a, Dataset F-8): a copy of a derived view — with `derived_from`
// stripped by D-DUPLICATE before it can even reach re-derivation — must never
// be rewritten or deleted by the next re-derivation of the .base it used to
// name, and must be absent from that .base's membership record.
//
// PRIORITY 1 scenario 1 also requires this survival to hold across the
// .base's own DELETE, not only its re-derivation. That second half is
// EMPIRICALLY VACUOUS against today's code for a different, independently
// verified reason (not the DisallowUnknownFields blocker below): grepping
// every pkg/knowledge/knowledge_restructure*.go file for the string ".base"
// returns zero hits — (*Trasher).Trash has no `.base`-aware branch at all
// today, so deleting a `.base` file touches exactly the one requested path
// and nothing else, by construction. Asserting "the copy survives a .base
// delete" against that code can never fail, today, for ANY file — it is not
// yet a security-relevant fact to falsify (FR-VA-008g's release step is test
// 51, `TestLibraryKnowledgeCascade_DeleteReleasesDerivedViews`, itself
// BLOCKED in pkg/gateway for the identical missing-field/missing-record
// reason as this test). Recorded here as a comment, not as a silently-added
// vacuous assertion, so CHECK does not have to rediscover it.
//
// BLOCKED, verified empirically: same root cause as the test above —
// `derived_from` does not exist on the wire type, so a "copy that kept
// derived_from" cannot be constructed at all; the nearest real approximation
// (a file carrying that key) is rejected the same way, confirmed below.
func TestRederive_IgnoresCopiedDerivedFromField(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	// The nearest real approximation of "a copy that kept derived_from
	// despite D-DUPLICATE's strip step" (pkg/records::RewriteCopiedViewIdentity
	// does not exist either — confirmed: no such symbol anywhere under
	// pkg/records, pkg/library or pkg/gateway): a file carrying the field.
	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))
	copyPath := filepath.Join(records.ViewsDir(root), "projects--open-2.yaml")
	require.NoError(t, os.WriteFile(copyPath,
		[]byte("name: projects--open-2\nlabel: Open (copy)\nderived_from: Projects.base\n"), 0o600))

	schemaSet, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	_, report, err := records.LoadViews(root, schemaSet)
	require.NoError(t, err)

	var gotCode records.ViewRejectionCode
	for _, rej := range report.Rejections {
		for _, p := range rej.Paths {
			if p == copyPath {
				gotCode = rej.Code
			}
		}
	}
	if gotCode != records.RejectViewUnknownKey {
		t.Fatalf(
			"expected the real loader to reject the copy's carried-over `derived_from` key as %q — got "+
				"%q for %s, report: %+v. If a real code now exists, this BLOCKED test is stale.",
			records.RejectViewUnknownKey, gotCode, copyPath, report.Rejections,
		)
	}

	t.Fatal("BLOCKED: ViewDef.derived_from, the copy-time strip step (D-DUPLICATE), and the " +
		"pipeline-owned membership record (D-PROVENANCE) are not implemented — verified above that the " +
		"real loader rejects a derived_from-carrying file as RejectViewUnknownKey before any membership " +
		"decision runs — required before a copy of a derived view can be shown to survive, and be absent " +
		"from the membership record, across the next re-derivation AND across a .base delete " +
		"(the delete half is separately vacuous today — see this test's doc comment), per FR-VA-008a / " +
		"Dataset F-8 / TDD test 44 / combined-brief Priority 1 scenario 1.")
}

// TestRederive_RefusesOccupiedPathNotOwnRecord is TDD Plan test 47
// (R2-CRIT-002, FR-VA-008d, Dataset D10): re-derivation writing a
// NEWLY-declared view must never overwrite a path already occupied by a file
// that is not already in its own membership record for this .base — it must
// pick a free (suffixed) path instead.
//
// CONFLICT WITH THE va-redfix1/combined-brief INSTRUCTION TO USE THE SIBLING
// `.view` PATH, flagged rather than silently applied: moving occupiedPath to
// the spec's sibling `<slug>.view` location (filepath.Join(root,
// "projects--open.view")) makes this test PASS VACUOUSLY on today's code,
// verified by an isolated local run (receipt:
// row47-siblingpath-vacuous-check.log, exit=0, PASS) — because
// view_translate.go::translateOneView hardcodes
// `records.ViewsDirName + "/" + slug + ".yaml"` (verified by reading it) and
// rederive.go/run.go both only ever join that against records.ViewsDir —
// there is NO real entry point today that writes beside a `.base` file at
// all (FR-VA-008's location fix has not landed). Testing the occupied-path
// rule at a location nothing writes to yet cannot fail for the right reason.
// This test therefore stays targeted at the path the real write loop
// actually uses today (records.ViewsDir), which IS provably red (receipt:
// row47-baseline.log). Once FR-VA-008's sibling-path write lands, this test
// must be re-targeted at the sibling path in the SAME change that lands it —
// tracked as a note in the qa-lead accounting report, not done silently now.
func TestRederive_RefusesOccupiedPathNotOwnRecord(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	// "projects--open" is exactly the deterministic slug rederiveBaseWithViews'
	// "Open" view produces (base file stem "Projects" + view name "Open",
	// same derivation TestRederiveBase_WritesViewsAgainstCurrentOnDiskSchemas
	// already relies on). Pre-occupy that path with unrelated content BEFORE
	// this .base has ever been re-derived — so the occupant cannot possibly
	// be in this .base's own membership record; nothing has written to it yet.
	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))
	occupiedPath := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
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
				"NOT in this .base's membership record — the pipeline may only write a NEWLY-declared view "+
				"to a FREE (suffixed) path, never overwrite an unmanaged occupant.\nwant (unchanged): %q\ngot: %q",
			occupiedPath, unrelatedContent, string(got),
		)
	}
}

// TestRederive_TwoFilesClaimingSameDerivedFromAndName is TDD Plan test 46
// (R2-CRIT-001, Dataset F-10): two files sharing BOTH `derived_from` and
// `name` (a sync-conflict shape) must be touched by NEITHER re-derivation
// run — no file may be picked as "the" managed one on ambiguous evidence.
//
// BLOCKED, verified empirically: same root cause as tests 44/45 — a file
// even attempting to carry `derived_from` is rejected as RejectViewUnknownKey
// before ambiguity between two such files could ever be evaluated, so there
// is no way to construct the F-10 scenario at all yet.
func TestRederive_TwoFilesClaimingSameDerivedFromAndName(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	require.NoError(t, os.MkdirAll(records.ViewsDir(root), 0o755))
	pathA := filepath.Join(records.ViewsDir(root), "sync-conflict-a.yaml")
	pathB := filepath.Join(records.ViewsDir(root), "sync-conflict-b.yaml")
	body := []byte("name: projects--open\nlabel: Sync conflict\nderived_from: Projects.base\n")
	require.NoError(t, os.WriteFile(pathA, body, 0o600))
	require.NoError(t, os.WriteFile(pathB, body, 0o600))

	schemaSet, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	_, report, err := records.LoadViews(root, schemaSet)
	require.NoError(t, err)

	for _, p := range []string{pathA, pathB} {
		var gotCode records.ViewRejectionCode
		for _, rej := range report.Rejections {
			for _, rp := range rej.Paths {
				if rp == p {
					gotCode = rej.Code
				}
			}
		}
		if gotCode != records.RejectViewUnknownKey {
			t.Fatalf(
				"expected the real loader to reject %s's carried `derived_from` key as %q — got %q, "+
					"report: %+v. If a real code now exists, this BLOCKED test is stale.",
				p, records.RejectViewUnknownKey, gotCode, report.Rejections,
			)
		}
	}

	t.Fatal("BLOCKED: ViewDef.derived_from (contract FR-VA-009a) and the pipeline-owned membership " +
		"record (D-PROVENANCE, R2-CRIT-001) are not implemented — verified above that both files sharing " +
		"a hand-added derived_from are rejected at parse time (RejectViewUnknownKey) before ambiguity " +
		"between them could even be evaluated — required before two files sharing both derived_from and " +
		"name can be shown to be left untouched by re-derivation, per Dataset F-10 / TDD test 46.")
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
