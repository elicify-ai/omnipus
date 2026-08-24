// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 FR-050a(a) — "reason present if and only if excerpt absent", with the
// hole closed.
//
// The requirement is one sentence: a hit that cannot carry an excerpt is still
// returned, with path and title and a MACHINE-READABLE REASON — "never a
// fabricated excerpt and never a silently dropped result", and the set of
// reasons "MUST cover the case where no re-read was ever attempted".
//
// An ATTACHMENT is exactly that case. FR-039a forbids opening its contents for
// any reason, so there is nothing to quote and nothing was even tried. Until
// the FR-050a(a) amendment the contract's enum had no member for it, so
// pkg/knowledge emitted "attachment_not_read", the gateway dropped it on the
// floor, and the hit reached the SPA with NEITHER an excerpt NOR a reason —
// the unexplained gap the requirement exists to prevent.
//
// These tests are written against that sentence, not against the mapper: they
// drive the real search endpoint over a real indexed collection and read the
// JSON the SPA would receive.

package gateway

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestKnowledgeSearch_AttachmentHitExplainsItsMissingExcerpt_FR050a — the
// invariant, end to end.
//
// DIES ON: removing the knowledge.ExcerptAttachment case from
// knowledgeExcerptReason (the hit then carries neither field again); removing
// `attachment_not_read` from KnowledgeSearchHit.excerpt_unavailable in the
// contract and regenerating (the constant stops existing).
func TestKnowledgeSearch_AttachmentHitExplainsItsMissingExcerpt_FR050a(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	// An attachment, findable by NAME only (FR-039a). Its bytes are shaped like
	// a note on purpose: an implementation that opened it would have something
	// quotable to put in the excerpt, and this test would catch that too.
	writeNote(t, vault, "img/diagram-v3.png", "# LEAKED\n\nzarquonsecret\n")
	indexKnowledgeBase(t, api.homePath, vault)

	w := knowledgeSearchPost(t, api, ws, map[string]any{
		"query": "diagram-v3", "collection_id": collectionIDOf(t, api, ws, "vault"),
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeSearchResponse](t, w)

	require.Len(t, resp.Hits, 1, "an attachment is findable by its filename (FR-039a)")
	hit := resp.Hits[0]
	require.Equal(t, "img/diagram-v3.png", hit.Path)
	assert.Equal(t, gen.KnowledgeSearchHitKindAttachment, hit.Kind)

	assert.Nil(t, hit.Excerpt,
		"an attachment's contents are never opened, so there is nothing to quote (FR-039a)")
	require.NotNil(t, hit.ExcerptUnavailable,
		"FR-050a(a): a hit with no excerpt MUST carry the reason. Neither field is the "+
			"unexplained gap the requirement exists to prevent — the result arrives with "+
			"no quote and no word about why")
	assert.Equal(t, gen.KnowledgeSearchHitExcerptUnavailableAttachmentNotRead, *hit.ExcerptUnavailable)

	// And nothing was read out of the attachment on the way past.
	assert.NotContains(t, w.Body.String(), "zarquonsecret")
	assert.NotContains(t, w.Body.String(), "LEAKED")
}

// TestKnowledgeSearch_ExcerptAndReasonAreNeverBothAbsent_FR050a states the
// invariant as a property over a mixed result set rather than over one hit, so
// it also fails for any FUTURE reason the mapper forgets to translate.
//
// A note hit carries an excerpt and no reason; an attachment hit carries a
// reason and no excerpt. No hit carries neither, and none carries both.
//
// DIES ON: any un-mapped knowledge.ExcerptReason reaching the wire as a dropped
// value; returning an excerpt for an attachment.
func TestKnowledgeSearch_ExcerptAndReasonAreNeverBothAbsent_FR050a(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	vault := filepath.Join(workDir(api, ws), "vault")
	makeKnowledgeBase(t, vault, "Vault")
	writeNote(t, vault, "notes/diagram-v3 notes.md", "# Diagram notes\n\nabout diagram-v3\n")
	writeNote(t, vault, "img/diagram-v3.png", "binary-ish bytes\n")
	indexKnowledgeBase(t, api.homePath, vault)

	w := knowledgeSearchPost(t, api, ws, map[string]any{
		"query": "diagram-v3", "collection_id": collectionIDOf(t, api, ws, "vault"),
	})
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	resp := decodeJSON[gen.KnowledgeSearchResponse](t, w)

	require.Len(t, resp.Hits, 2, "both the note and the attachment match by name")

	var sawNote, sawAttachment bool
	for _, hit := range resp.Hits {
		hasExcerpt := hit.Excerpt != nil && *hit.Excerpt != ""
		hasReason := hit.ExcerptUnavailable != nil

		assert.Truef(t, hasExcerpt || hasReason,
			"hit %q has neither an excerpt nor a reason for its absence (FR-050a a)", hit.Path)
		assert.Falsef(t, hasExcerpt && hasReason,
			"hit %q carries both an excerpt and a reason it has none (FR-050a a)", hit.Path)

		if hasReason {
			assert.Truef(t, hit.ExcerptUnavailable.Valid(),
				"hit %q carries %q, which is not a member of the contract enum and is "+
					"dropped by the SPA's zod guard", hit.Path, string(*hit.ExcerptUnavailable))
		}

		switch hit.Kind {
		case gen.KnowledgeSearchHitKindNote:
			sawNote = true
			assert.Truef(t, hasExcerpt, "an indexed, readable note must quote itself: %q", hit.Path)
		case gen.KnowledgeSearchHitKindAttachment:
			sawAttachment = true
			assert.Equal(t, gen.KnowledgeSearchHitExcerptUnavailableAttachmentNotRead,
				*hit.ExcerptUnavailable)
		}
	}
	assert.True(t, sawNote, "the fixture must produce a note hit — the positive control")
	assert.True(t, sawAttachment, "the fixture must produce an attachment hit")
}
