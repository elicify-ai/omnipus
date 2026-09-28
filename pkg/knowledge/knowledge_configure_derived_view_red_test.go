// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 48 and 68.
//
// Oracle: FD-6 ("`write_view` on a view whose current `ViewSet` entry carries
// `derived_from` refuses the write, naming the managing `.base` file")
// resolving R2-MAJ-003, and FR-VA-008e; plus US-2 AS-6 / R2-MIN-007 ("every
// one of those surfaces [knowledge_describe, knowledge_find,
// knowledge_configure] states that the view is derived and names its source
// `.base` file — never silently presenting it as an ordinary,
// independently-owned file").
//
// Today's execWriteView (pkg/knowledge/knowledge_configure.go) has no
// concept of a derived view at all: it parses the caller's `definition` into
// a fresh ViewDef with no merge of an existing file's fields
// (marshalDefinition(defMap)-style rebuild), so there is nothing that could
// even READ a "derived_from" marker to refuse against — confirmed by
// grep -rn "derived_from\|DerivedFrom" pkg/knowledge --include='*.go'
// returning zero matches for the concept (only coincidental substring hits
// in unrelated test names, e.g.
// TestKnowledgeConfigure_KindDescriptionIsDerivedFromViewKinds). Both tests
// below are BLOCKED per the qa-lead RED protocol.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestWriteView_RefusesDerivedView$' ./pkg/knowledge/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestKnowledgeTools_StateDerivedViewSourceOnAllThreeSurfaces$' ./pkg/knowledge/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import "testing"

// TestWriteView_RefusesDerivedView is TDD Plan test 48 (FD-6, R2-MAJ-003,
// FR-VA-008e, Dataset F-9): write_view on a view whose CURRENT ViewSet entry
// carries `derived_from` must refuse the write and name the managing .base
// file, never silently drop the marker and land the edit.
//
// BLOCKED: neither the ViewDef.derived_from contract field nor a read-only
// refusal path exists anywhere in execWriteView — there is no `derived_from`
// for a fixture view to carry, and no code path that could refuse against
// it.
func TestWriteView_RefusesDerivedView(t *testing.T) {
	t.Fatal("BLOCKED: ViewDef.derived_from (contract FR-VA-009a) and write_view's read-only refusal " +
		"for a derived view (FD-6, FR-VA-008e) are not implemented in " +
		"pkg/knowledge/knowledge_configure.go::execWriteView — required before a write_view call " +
		"against a derived view can be shown to be refused, naming the managing .base, per " +
		"Dataset F-9 / TDD test 48.")
}

// TestKnowledgeTools_StateDerivedViewSourceOnAllThreeSurfaces is TDD Plan
// test 68 (R2-MIN-007, US-2 AS-6): knowledge_describe, knowledge_find and
// knowledge_configure must each state, in their OWN output, that a view is
// derived and name its source .base file for the same fixture — the
// traceability matrix names this obligation but the original spec draft
// left it as a test nobody wrote (R2-MIN-007's own finding).
//
// BLOCKED: same root cause — with no `derived_from` field or membership
// record, none of renderViews (knowledge_describe), ViewFindLoader
// (knowledge_find) or knowledge_configure's write-result rendering has
// anything to report a "derived" status FROM.
func TestKnowledgeTools_StateDerivedViewSourceOnAllThreeSurfaces(t *testing.T) {
	t.Fatal("BLOCKED: ViewDef.derived_from and the pipeline-owned membership record (D-PROVENANCE) " +
		"are not implemented — required before knowledge_describe, knowledge_find and " +
		"knowledge_configure can each be shown to state a derived view's status and source .base " +
		"in their own output, per US-2 AS-6 / R2-MIN-007 / TDD test 68.")
}
