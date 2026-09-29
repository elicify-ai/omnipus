// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), combined-brief
// Priority 1 scenarios 2 and 3 (va-qa-combined-brief.md).
//
// Both scenarios are questions about the pipeline-owned MEMBERSHIP RECORD
// (D-PROVENANCE §2, "The fix: a pipeline-owned membership record is the SOLE
// authority") — a mechanism §4 step 5b states precisely: "mine" is the
// INTERSECTION of (i) a `derived_from` field match AND (ii) presence in the
// record. Neither half of that intersection exists in the tree today:
//
//	$ grep -rn "derived_from\|DerivedFrom\|membership record\|ViewMembership" \
//	      pkg/vaultimport pkg/knowledge pkg/records --include='*.go'
//	(zero matches for the concept)
//
// Scenario 2's oracle (D-PROVENANCE §4 step 5b, intersection rule): a view
// at a TRACKED path (in the record) whose `derived_from` marker was removed
// fails half (i) of the intersection — it is therefore NOT "mine" and MUST
// NOT be rewritten or deleted. FD-6's read-only gate is a separate concern
// (write_view's own refusal), not re-derivation's; the spec is not silent on
// re-derivation's own behavior here — §4 step 5b answers it directly.
//
// Scenario 3's oracle (D-PROVENANCE §2, "A missing or unreadable record is
// treated as EMPTY, never rebuilt by trusting files' derived_from values ...
// the next re-derivation run starts writing views as if none were yet
// tracked, subject to the occupied-path rule").
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_IgnoresTrackedViewAfterUserRemovesDerivedFromMarker$' ./pkg/vaultimport/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestRederive_TreatsCorruptOrMissingMembershipRecordAsEmpty$' ./pkg/vaultimport/
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

// TestRederive_IgnoresTrackedViewAfterUserRemovesDerivedFromMarker is
// combined-brief Priority 1 scenario 2 (D-PROVENANCE §4 step 5b intersection
// rule, FD-6). A view at a path that WOULD be in the .base's membership
// record — the exact deterministic slug path a prior re-derivation wrote —
// has its `derived_from` marker manually removed by the user. Because "mine"
// requires BOTH a field match AND record presence, losing the field alone
// must be enough to take the file out of "mine": the next re-derivation must
// NOT rewrite or delete it.
//
// BLOCKED, verified empirically: neither half of the intersection exists.
// The nearest real approximation — a first re-derivation writes the view
// (today's mechanism: `source:` field match, not derived_from/record), then
// the file's `source:` key is removed by hand to model "the marker the user
// could remove was stripped" — demonstrates today's ACTUAL "mine" computation
// (RederiveBase's `v.DeclaredSource() == baseRelPath`) is a plain field
// match with no record at all, so it is not testable as an intersection: it
// either matches or it doesn't, there being no second, independent
// authority to fall through to. Removing the file's only "mine" marker
// under today's mechanism makes it invisible to re-derivation for exactly
// the reason the file matched in the first place, which incidentally
// produces the spec's REQUIRED external outcome (survives) — but via the
// single-field mechanism the spec is replacing, not via the intersection it
// requires. Asserting on that alone would be indistinguishable from a
// vacuous pass, so this stays BLOCKED and names the missing intersection
// precisely instead.
func TestRederive_IgnoresTrackedViewAfterUserRemovesDerivedFromMarker(t *testing.T) {
	root := buildRederiveVault(t, rederiveBaseWithViews, nil)

	first, err := RederiveBase(root, "Projects.base")
	require.NoError(t, err)
	require.NotEqual(t, OutcomeRefused, first.Status, "reason: %s", first.RefusedReason)
	require.Contains(t, first.Written, "projects--open")

	// The nearest real approximation of "the tracking marker was removed":
	// today's ONLY tracking marker is `source:`, so strip it — modeling the
	// user action the scenario describes against the mechanism that exists.
	viewPath := filepath.Join(records.ViewsDir(root), "projects--open.yaml")
	require.NoError(t, os.WriteFile(viewPath,
		[]byte("name: projects--open\nlabel: Open\n"), 0o600))

	schemaSet, _, err := records.LoadSchemas(root)
	require.NoError(t, err)
	_, vs, err := loadRederivedViewsForTest(t, root, schemaSet)
	require.NoError(t, err)
	v, ok := vs.Get("projects--open")
	require.True(t, ok, "the view must still load after the marker is removed")
	if v.DeclaredSource() == "Projects.base" {
		t.Fatalf(
			"expected removing the `source:` marker to make DeclaredSource() stop matching \"Projects.base\" "+
				"(today's only \"mine\" signal) — it still reports %q. If the mechanism changed, this BLOCKED "+
				"test is stale and the real intersection scenario should be written instead.",
			v.DeclaredSource(),
		)
	}

	t.Fatal("BLOCKED: ViewDef.derived_from and the pipeline-owned membership record (D-PROVENANCE §2, §4 " +
		"step 5b's intersection rule) are not implemented — today's ONLY \"mine\" signal is a single " +
		"`source:` field match with no independent record to fall through to, so removing that one " +
		"marker produces the spec's required external outcome by accident (no second signal to lose), " +
		"not by the intersection FR-VA-008a requires. Required before a view at a TRACKED path whose " +
		"derived_from marker was removed can be shown to be ignored BECAUSE of the record/field " +
		"intersection, per combined-brief Priority 1 scenario 2.")
}

// TestRederive_TreatsCorruptOrMissingMembershipRecordAsEmpty is
// combined-brief Priority 1 scenario 3 (D-PROVENANCE §2, "A missing or
// unreadable record is treated as EMPTY ... never rebuilt by trusting
// files' derived_from values"). A corrupt or missing membership record must
// read as EMPTY: the pipeline must never infer membership from a file's own
// `derived_from` marker, and nothing already on disk may be deleted as a
// result of the record being unreadable.
//
// BLOCKED: there is no membership record at all to corrupt, delete, or read
// as empty — verified by the same grep this file's header cites, and by
// pkg/records.manifest.go's own file family (the spec's own precedent for
// "a small, derived, disposable index ... beside the index, not inside the
// vault") having no view-membership counterpart:
func TestRederive_TreatsCorruptOrMissingMembershipRecordAsEmpty(t *testing.T) {
	t.Fatal("BLOCKED: the pipeline-owned membership record (D-PROVENANCE §2) does not exist anywhere " +
		"under pkg/records, pkg/vaultimport or pkg/knowledge (grep for \"membership record\"/\"ViewMembership\" " +
		"across all three returns zero matches for the concept) — there is no record to plant as corrupt " +
		"or missing, and no read-as-empty behavior to exercise, required before a corrupt or missing " +
		"membership record can be shown to read as EMPTY rather than fall back to trusting files' " +
		"derived_from values, per D-PROVENANCE §2 / combined-brief Priority 1 scenario 3.")
}

// loadRederivedViewsForTest mirrors rederive_test.go's loadRederivedViews
// but returns the error instead of calling require.NoError itself, so this
// file's own empirical check can report its own failure message rather than
// testify's generic one.
func loadRederivedViewsForTest(t *testing.T, root string, schemas *records.SchemaSet) (*records.SchemaSet, *records.ViewSet, error) {
	t.Helper()
	vs, report, err := records.LoadViews(root, schemas)
	if err != nil {
		return nil, nil, err
	}
	require.True(t, report.OK(), "fixture views must load cleanly: %+v", report.Rejections)
	return schemas, vs, nil
}
