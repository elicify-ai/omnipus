// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 45 (SECURITY), 44 and 47.
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
// DerivedFrom field, and records.ParseView decodes with
// json.Decoder.DisallowUnknownFields (pkg/records/view.go), so a YAML file
// that even TRIES to carry a `derived_from:` key is rejected outright as
// RejectViewUnknownProperty before any provenance decision could be made —
// there is no code path to exercise here yet. Tests 44 and 45 are BLOCKED
// per the qa-lead RED protocol: they name the missing contract field and the
// missing membership-record mechanism precisely, so CHECK and the
// implementer see exactly what must exist before these can run for real.
//
// Test 47 (the occupied-path rule, FR-VA-008d) needs no new field — it is
// testable today directly against fileTranslatedBase's existing write loop,
// which is unconditional (fileutil.WriteFileAtomic with no prior-occupant
// check at all beyond a byte-identical short-circuit) — so it is written as
// a real, compiling, currently-failing assertion.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_IgnoresHandAddedDerivedFromOnForeignFile$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_IgnoresCopiedDerivedFromField$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_RefusesOccupiedPathNotOwnRecord$' ./pkg/vaultimport/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package vaultimport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

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
// BLOCKED: neither generated.ViewDef.DerivedFrom nor a pipeline-owned
// membership record exists anywhere in the tree (see file header grep).
// records.ParseView's DisallowUnknownFields means a file merely CONTAINING
// `derived_from: <foreign>.base` in its YAML is rejected as
// RejectViewUnknownProperty before re-derivation's authority question could
// even be asked — there is no code path to drive this scenario through yet.
func TestRederive_IgnoresHandAddedDerivedFromOnForeignFile(t *testing.T) {
	t.Fatal("BLOCKED: ViewDef.derived_from (contract FR-VA-009a) and the pipeline-owned membership " +
		"record (D-PROVENANCE, R2-CRIT-001) are not implemented anywhere in pkg/vaultimport, " +
		"pkg/knowledge or pkg/api/generated — required before a hand-added derived_from on a foreign " +
		"file can be shown to be ignored by that .base's re-derivation, per FR-VA-008a / Dataset F-9 / " +
		"TDD test 45.")
}

// TestRederive_IgnoresCopiedDerivedFromField is TDD Plan test 44 (R2-CRIT-001,
// FR-VA-008a, Dataset F-8): a copy of a derived view — with `derived_from`
// stripped by D-DUPLICATE before it can even reach re-derivation — must never
// be rewritten or deleted by the next re-derivation of the .base it used to
// name, and must be absent from that .base's membership record.
//
// BLOCKED: same root cause as the test above — `derived_from` does not
// exist on the wire type, D-DUPLICATE's per-file copy-time strip step
// (pkg/records::RewriteCopiedViewIdentity, spec §2 D-DUPLICATE) does not
// exist (confirmed: no such symbol anywhere under pkg/records, pkg/library
// or pkg/gateway), and there is no membership record to assert the copy's
// absence from.
func TestRederive_IgnoresCopiedDerivedFromField(t *testing.T) {
	t.Fatal("BLOCKED: ViewDef.derived_from, the copy-time strip step (D-DUPLICATE), and the " +
		"pipeline-owned membership record (D-PROVENANCE) are not implemented — required before a " +
		"copy of a derived view can be shown to survive, and be absent from the membership record, " +
		"across the next re-derivation, per FR-VA-008a / Dataset F-8 / TDD test 44.")
}

// TestRederive_RefusesOccupiedPathNotOwnRecord is TDD Plan test 47
// (R2-CRIT-002, FR-VA-008d, Dataset D10): re-derivation writing a
// NEWLY-declared view must never overwrite a path already occupied by a file
// that is not already in its own membership record for this .base — it must
// pick a free (suffixed) path instead.
//
// Today fileTranslatedBase's write loop (verified by reading it) is:
//
//	if current, rerr := os.ReadFile(path); rerr == nil && string(current) == string(pv.Bytes) {
//	        res.Unchanged = append(res.Unchanged, slug)
//	        continue
//	}
//	if werr := fileutil.WriteFileAtomic(path, pv.Bytes, generatedFilePerm); werr != nil { ... }
//	res.Written = append(res.Written, slug)
//
// — an unconditional overwrite of whatever is at the deterministic slug
// path, with no occupant check at all beyond the byte-identical
// short-circuit. This test plants an unrelated file with DIFFERENT bytes at
// the exact path a brand-new declared view would deterministically slug to,
// and asserts it survives a first-time RederiveBase run untouched.
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
