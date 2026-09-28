// Omnipus — RED tests for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), §10 TDD Plan tests
// 10, 11, 12, 21, 29, 30, 39 and 52. All BLOCKED per the qa-lead RED
// protocol: every one depends on LibraryEntry.is_view/view (D-CONTRACT),
// which does not exist on the generated type at all — confirmed by direct
// read of both contracts/components/schemas/LibraryEntry.yaml and
// src/lib/api/generated/openapi-types.ts's LibraryEntry block (TDD test 58's
// own finding).
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryEntries_IsViewAndKind$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryEntries_ViewWithNoKind$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryEntries_MalformedViewStillListed$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryEntries_OutsideKnowledgeBaseIsPlainFile$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryUpload_AutoRenamesCollidingViewName$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestDuplicateView_VisibleOnBothEntriesAndInAgentOutput$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryContentPut_ViewSaveNeverRefusesButFlagsRejection$' ./pkg/gateway/
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestLibraryCopy_StripsDerivedFromOnCopy$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

func TestLibraryEntries_IsViewAndKind(t *testing.T) {
	t.Fatal("BLOCKED: LibraryEntry has no is_view/view field at all (contracts/components/schemas/" +
		"LibraryEntry.yaml, D-CONTRACT) — required before a directory listing can be shown to report " +
		"is_view/view.kind/view.label/view.name for a well-formed view, per US-4 AS-1 / TDD test 10.")
}

func TestLibraryEntries_ViewWithNoKind(t *testing.T) {
	t.Fatal("BLOCKED: same root cause as test 10 — LibraryEntry has no view.kind field to be absent " +
		"or present, per US-4 AS-2 / EC-3 / TDD test 11.")
}

func TestLibraryEntries_MalformedViewStillListed(t *testing.T) {
	t.Fatal("BLOCKED: same root cause — LibraryEntry has no view.rejection field, and .view is not " +
		"even registered in pkg/library/entries.go's content-type table (TDD test 40's own finding), " +
		"so a malformed .view file today lists exactly like any other unrecognized file, with no " +
		"view-specific signal at all, per US-4 AS-3 / EC-6 / TDD test 12.")
}

func TestLibraryEntries_OutsideKnowledgeBaseIsPlainFile(t *testing.T) {
	t.Fatal("BLOCKED: same root cause — there is no is_view/view field to be absent for an entry " +
		"outside a knowledge base to distinguish it from one inside; the assertion would be " +
		"vacuously true today for EVERY entry (none carries the field at all), which is not a " +
		"meaningful RED for FD-1/D-SCOPE's actual claim, per US-1 AS-7 / US-4 AS-4 / TDD test 21.")
}

func TestLibraryUpload_AutoRenamesCollidingViewName(t *testing.T) {
	t.Fatal("BLOCKED: handleLibraryUpload has no per-file view-identity rewrite step (same root " +
		"cause as TDD test 27's own finding for handleLibraryTransfer) — required before an uploaded " +
		"name-colliding .view file can be shown to auto-suffix, per FD-2 / TDD test 29.")
}

func TestDuplicateView_VisibleOnBothEntriesAndInAgentOutput(t *testing.T) {
	t.Fatal("BLOCKED: the LibraryEntry half is blocked (no view.rejection field — same root cause as " +
		"test 10); the agent-output half (knowledge_describe/knowledge_find naming both colliding " +
		"paths) cannot be verified in isolation from the LibraryEntry half this row requires " +
		"together, per US-4 AS-5 / US-5 AS-4 / US-6 AS-4 / CRIT-002 / TDD test 30. NOTE for the " +
		"implementer: the agent-side half may already partially hold, since F1's existing " +
		"RejectViewDuplicateName mechanism already names both paths in ViewRejection.Paths — verify " +
		"at GREEN time whether knowledge_describe's renderViews already surfaces that, or needs " +
		"wiring.")
}

func TestLibraryContentPut_ViewSaveNeverRefusesButFlagsRejection(t *testing.T) {
	t.Fatal("BLOCKED: handleLibraryContentPut's post-write validation branch only fires for " +
		"isLibraryBasePath (.base) and knowledge.IsMarkdownPath (.md) — there is no .view branch at " +
		"all, and no view.rejection field on LibraryEntry to flag even if there were, per " +
		"MAJ-010/D-VALIDATE / TDD test 39.")
}

func TestLibraryCopy_StripsDerivedFromOnCopy(t *testing.T) {
	t.Fatal("BLOCKED: ViewDef.derived_from does not exist, and library.CopyInto has no per-file " +
		"rewrite step of any kind (same root cause as TDD test 27) — required before a copy of a " +
		"derived view can be shown to strip derived_from while the original keeps it, per " +
		"R2-CRIT-001 / D-DUPLICATE / TDD test 52.")
}
