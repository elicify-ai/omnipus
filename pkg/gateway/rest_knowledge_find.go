// Omnipus — POST /api/v1/library/{workspace_id}/knowledge/find: the HUMAN
// vault search (library-b-c-design-2026-09-07 §C1).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS
//
// The agent has knowledge_find; a person had no way to search the vault. This
// endpoint is that surface, and it runs over the SAME engine — vaultprops.
// OpenFindEnv + knowledgefind.Find, exactly as the view-result endpoint does
// (rest_knowledge_view.go) — so it inherits the engine's prefix-matching,
// coverage and freshness behaviour rather than growing a second search path.
//
// A human query is FREE TEXT, so it is answered three ways at once:
//
//   - NOTES: `words` over kind=note. The engine decides what matches and in
//     what order; the snippet is re-read from the note on disk at query time
//     (never stored, never fabricated), mirroring the plain-search endpoint's
//     excerpt honesty.
//   - RECORDS: `words` over kind=record — the notes that DECLARE a record type
//     and match the query. Their typed property values ride along as cells so a
//     reader can see which values matched without a second read.
//   - VIEWS: the saved views in scope whose name or label matches the query,
//     taken straight from the loaded ViewSet (env.Views) the same evaluation
//     opened — no second load.
//
// HONEST STATES, same rules as its neighbours:
//   - An empty result is EMPTY, not an error (200, empty arrays).
//   - An out-of-scope collection_id answers with the empty-but-complete shape,
//     resolved BEFORE the rate limiter so it cannot be told apart by timing a
//     429 either (US-9 / FR-053).
//   - An index that is not ready (never built, or still catching up with disk)
//     sets complete=false with the engine's own freshness sentence in
//     complete_reason — so the UI says "still indexing", not "no results".
// ---------------------------------------------------------------------------

const (
	// vaultSearchDefaultLimit is the per-kind hit count when the caller names
	// none. Matches the contract's own default.
	vaultSearchDefaultLimit = 20
	// vaultSearchMaxLimit clamps an over-large request rather than rejecting it,
	// per the request schema. It is the engine's own row cap (MaxLimit).
	vaultSearchMaxLimit = knowledgefind.MaxLimit
	// vaultSearchSnippetScanBytes bounds how much of a note is read to build one
	// snippet. A snippet is a short window around a match; a note larger than
	// this still yields one from its head, and the read never grows with note
	// size.
	vaultSearchSnippetScanBytes = 128 << 10
	// vaultSearchSnippetRadius is how many bytes of context to keep on each side
	// of the matched term in a snippet.
	vaultSearchSnippetRadius = 90
	// vaultSearchMaxQueryLength is the contract's query maxLength bound
	// (contracts/components/schemas/VaultSearchRequest.yaml). Enforced
	// unconditionally in the handler — see the I3 comment at the point of
	// use, in handleKnowledgeVaultSearch.
	vaultSearchMaxQueryLength = 1024
)

func (a *restAPI) handleKnowledgeVaultSearch(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var req gen.VaultSearchRequest
	if !decodeAndValidate(w, r, "VaultSearchRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		jsonErr(w, http.StatusBadRequest, "query is required")
		return
	}
	// I3 (2026-09-09 code review): the contract's query maxLength:1024
	// (contracts/components/schemas/VaultSearchRequest.yaml) is enforced
	// HERE, unconditionally — decodeAndValidate's schema pass above only
	// runs when gateway.validate_inbound is true (default false), so in a
	// default install the declared bound was purely decorative. Left
	// unenforced, an over-long query reaches
	// knowledgefind.Find->Index.SearchFiltered->prefixSearchTokens, which
	// tokenises with NO cap, builds a huge conjunction across every prose
	// field, and on zero hits falls back to one bleve PrefixQuery PER TOKEN
	// PER prose field — repeated once per declared record type plus notes
	// plus attachments (vaultSearchRecords/vaultSearchAttachments), and each
	// returned note then re-scans with vaultSearchQueryTerms's strings.Index
	// for every one of those tokens. Unlike the sibling file-search endpoint
	// (rest_library_files_search.go, MV-11), this endpoint takes no walk
	// semaphore at all — the request-body size cap and the 60 req/min
	// per-workspace rate limiter are the only other bounds — so this length
	// check is the sole defence against that cost. Follows the sibling's own
	// fix for the identical gap (rest_library_files_search.go's
	// fileSearchMaxQueryLength check, F4).
	if len(query) > vaultSearchMaxQueryLength {
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("query must not exceed %d characters", vaultSearchMaxQueryLength))
		return
	}
	if strings.TrimSpace(req.CollectionId) == "" {
		jsonErr(w, http.StatusBadRequest, "collection_id is required")
		return
	}

	// FR-037's principle: a limit above the cap is CLAMPED, never rejected —
	// and the clamp is DISCLOSED (MV-9's limit_clamped/limit_requested), never
	// silent.
	requestedLimit := vaultSearchDefaultLimit
	if req.Limit != nil && *req.Limit > 0 {
		requestedLimit = *req.Limit
	}
	limit := requestedLimit
	limitClamped := false
	if limit > vaultSearchMaxLimit {
		limit = vaultSearchMaxLimit
		limitClamped = true
	}

	// F6 (2026-09-08 code review): the rate limiter is consulted BEFORE scope
	// is resolved, and for EVERY request regardless of outcome — an
	// out-of-scope collection_id must consume/observe the SAME
	// workspace-keyed limiter an in-scope one does. The limiter used to sit
	// AFTER the out-of-scope short-circuit below, which created exactly the
	// probe oracle FR-053 exists to close: drain the limiter, then probe a
	// candidate collection_id — a 429 meant "in scope" (it reached the
	// limiter), a 200 with the empty-but-complete body meant "out of scope"
	// (it never touched the limiter at all). FR-053's body invariant alone
	// cannot fix this — the STATUS CODE was the leak. Now both cases pass
	// through allowKnowledgeRetrieval first, so a drained limiter answers 429
	// for either, and an available one lets both continue to their
	// respective (body-indistinguishable-when-empty) responses.
	if !a.allowKnowledgeRetrieval(w, workspaceID) {
		return
	}

	// US-9/FR-053: an out-of-scope collection is an EMPTY, complete answer —
	// never a permission error, so the error channel cannot be used to probe
	// for another workspace's collections either.
	col, inScope := a.resolveScopedCollection(workspaceID, req.CollectionId)
	if !inScope {
		jsonOK(w, outOfScopeVaultSearchResponse(req.CollectionId, requestedLimit, limitClamped))
		return
	}

	env, closeEnv, err := vaultprops.OpenFindEnv(r.Context(), a.homePath, col)
	defer closeEnv()
	if err != nil {
		logger.ErrorCF("rest", "knowledge: open vault-search environment",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	jsonOK(w, buildVaultSearchResult(r.Context(), env, col.Root, req.CollectionId, query, limit, requestedLimit, limitClamped))
}

// vaultSearchCompleteStatement is the render-ready sentence for a search that
// covered the whole vault with nothing to disclose — the out-of-scope/empty
// base case, and the fallback when nothing more specific applies. It is
// deliberately the SAME sentence a genuinely empty, fully-indexed vault gets
// (US-9/FR-053: an out-of-scope collection_id must stay indistinguishable
// from an empty one, and that includes the prose, not only the hit arrays).
const vaultSearchCompleteStatement = "Searched the whole of this knowledge base; its index was complete at query time."

// emptyVaultSearchResponse is the empty-but-honest base every answer builds on:
// every hit array is a real empty slice (never nil, which marshals to null),
// and the verdict is complete — the empty set IS the whole of what this scope
// can see. Attachments is sent as [] too (MV-9: the handler always sends it;
// it is wire-optional only for additive-compat with older clients).
func emptyVaultSearchResponse(collectionID string) gen.VaultSearchResponse {
	statement := vaultSearchCompleteStatement
	return gen.VaultSearchResponse{
		CollectionId: collectionID,
		Complete:     true,
		Notes:        []gen.VaultSearchNoteHit{},
		Records:      []gen.VaultSearchRecordHit{},
		Views:        []gen.VaultSearchViewHit{},
		Attachments:  &[]gen.VaultSearchAttachmentHit{},
		Statement:    &statement,
	}
}

// outOfScopeVaultSearchResponse is the handler's answer for a collection_id
// this workspace cannot address (US-9/FR-053). It builds on
// emptyVaultSearchResponse and layers in ONLY the limit-clamp disclosure —
// never the notes_searched/notes_total_known coverage numbers, and never a
// statement composed from them. Those numbers describe a REAL collection's
// index state; an out-of-scope collection has none this workspace may read,
// so the only way to avoid either fabricating them or leaking another
// workspace's real ones is to omit them, exactly as buildVaultSearchResult
// itself omits them for an in-scope collection with nothing to disclose (see
// its own honesty-fields comment) — that is what keeps the two cases
// byte-indistinguishable. The limit clamp, by contrast, is safe to disclose
// unconditionally: it is derived only from the caller's own requested limit
// and the public server cap (vaultSearchMaxLimit), never from collection
// state, so withholding it here would gain no privacy and would cost a real
// caller the disclosure FR-037 requires.
func outOfScopeVaultSearchResponse(collectionID string, requestedLimit int, limitClamped bool) gen.VaultSearchResponse {
	out := emptyVaultSearchResponse(collectionID)
	applyVaultSearchLimitDisclosure(&out, limitClamped, requestedLimit)
	statement := vaultSearchStatement(out)
	out.Statement = &statement
	return out
}

// applyVaultSearchLimitDisclosure sets limit_clamped/limit_requested (MV-9/
// FR-037) when the caller's requested limit was clamped down to the server
// cap. Shared by buildVaultSearchResult and outOfScopeVaultSearchResponse so
// the two paths can never drift on how — or whether — this disclosure is
// composed.
func applyVaultSearchLimitDisclosure(out *gen.VaultSearchResponse, limitClamped bool, requestedLimit int) {
	if !limitClamped {
		return
	}
	clamped := true
	out.LimitClamped = &clamped
	requested := requestedLimit
	out.LimitRequested = &requested
}

// vaultSearchHasAnyHit reports whether out carries at least one real hit in
// any group (notes, records, views or attachments).
func vaultSearchHasAnyHit(out gen.VaultSearchResponse) bool {
	return len(out.Notes) > 0 || len(out.Records) > 0 || len(out.Views) > 0 ||
		(out.Attachments != nil && len(*out.Attachments) > 0)
}

// buildVaultSearchResult runs the four searches (records, notes, views,
// attachments) over one opened environment and merges their completeness into
// one verdict, then attaches the MV-9 honesty fields: the notes
// searched/total-known coverage pair, the notes-capped-at-limit flag, the
// limit-clamp disclosure and the handler-authored statement.
func buildVaultSearchResult(ctx context.Context, env vaultprops.FindEnv, collectionRoot, collectionID, query string, limit, requestedLimit int, limitClamped bool) gen.VaultSearchResponse {
	out := emptyVaultSearchResponse(collectionID)

	// RECORDS FIRST — words scoped to each declared record type in turn, so the
	// engine renders that type's declared properties as cells. A typeless
	// kind=record query cannot: with no type it does not know which schema's
	// columns to render, so it returns rows with EMPTY cells — and cells (the
	// typed property values that matched) are the whole point of a record hit.
	//
	// kind=record is a STRICT SUBSET of kind=note (a record note is a note that
	// declares a record type), so a record note also matches the note query. The
	// record hit is the more specific, more useful one, so it OWNS the file: its
	// paths are collected here and excluded from NOTES below, so the same file is
	// never shown — or counted — in both groups.
	recHits, recComplete, recReason := vaultSearchRecords(ctx, env, query, limit)
	out.Records = append(out.Records, recHits...)
	if !recComplete {
		out.Complete = false
		if out.CompleteReason == nil && recReason != "" {
			out.CompleteReason = &recReason
		}
	}
	recordPaths := make(map[string]bool, len(recHits))
	for i := range recHits {
		recordPaths[recHits[i].Path] = true
	}

	// NOTES — words over kind=note, minus any path already returned as a record.
	noteResp, noteReady := runVaultSearchFind(ctx, env, query, "", gen.VaultFindRequestKindNote, limit)
	if noteReady {
		for i := range noteResp.Rows {
			if recordPaths[noteResp.Rows[i].Path] {
				continue
			}
			out.Notes = append(out.Notes, vaultSearchNoteHit(collectionRoot, &noteResp.Rows[i], query))
		}
		// A present NextCursor is the engine's own "there is more beyond this
		// page" signal (VaultFindResponse.next_cursor: absent means last page).
		// This call always asks with offset 0 (RenderRows, no cursor), so its
		// presence means the note group was cut at `limit` — the count is a
		// lower bound, and the UI renders "N+" rather than an exact total.
		if noteResp.NextCursor != nil {
			capped := true
			out.NotesCappedAtLimit = &capped
		}
	}
	mergeVaultSearchCompleteness(&out, noteResp, noteReady)

	// VIEWS — name/label match over the loaded view set. No index needed, so it
	// is answered whatever the text index's state.
	viewHits, viewsComplete, viewReason := vaultSearchViewHits(env, query, limit)
	out.Views = append(out.Views, viewHits...)
	if !viewsComplete {
		out.Complete = false
		if out.CompleteReason == nil && viewReason != "" {
			out.CompleteReason = &viewReason
		}
	}

	// ATTACHMENTS (ADR-081 CRIT-001 parity) — words over kind=attachment,
	// matched by filename. This is the kind textOnlyServable never covers
	// (find.go:1066-1077 gates it to kind=note only), so it always routes
	// through the properties index: on a build without one (records_no_sqlite,
	// mipsle, netbsd, freebsd/arm) the engine refuses by name rather than
	// silently answering empty, and that refusal is surfaced here as
	// complete:false with the engine's own reason — never a bare empty group
	// (MV-9's platform carve-out).
	attHits, attComplete, attReason := vaultSearchAttachments(ctx, env, query, limit)
	out.Attachments = &attHits
	if !attComplete {
		out.Complete = false
		if out.CompleteReason == nil && attReason != "" {
			out.CompleteReason = &attReason
		}
	}

	// HONESTY (MV-9): notes_searched / notes_total_known, sourced from the
	// SAME text-index freshness surface complete_reason's own coverage
	// warnings draw from (knowledgefind.TextFreshnessReporter.IndexFreshness,
	// find.go:683-720/772-808) — never a number this handler invents. Both
	// values arrive from ONE walk together, so they are set together; when the
	// walk could not be trusted (ScannedFiles == 0, the same threshold the
	// engine's own coverage checks use) the denominator is OMITTED rather than
	// reported as zero.
	//
	// WITHHELD (FR-053) when the search is complete AND every group came back
	// empty: that state is the one an out-of-scope collection_id ALSO
	// produces (outOfScopeVaultSearchResponse), and US-9/FR-053 requires the
	// two to be indistinguishable — including the prose, not only the hit
	// arrays. A real notes_searched/notes_total_known pair here would be
	// exactly the signal a caller probing for another workspace's collection
	// ids needs: present only for a collection that genuinely exists. Once
	// there is at least one real hit, or the search is itself incomplete, the
	// response already carries collection-specific content beyond "nothing
	// matched" — at that point the coverage numbers add honesty without
	// opening a new channel, so they are disclosed as before.
	if fr, ok := env.Deps.Text.(knowledgefind.TextFreshnessReporter); ok {
		fresh, ferr := fr.IndexFreshness(ctx)
		if ferr != nil {
			// F5 (2026-09-08 code review): a failed freshness probe must never
			// yield a MORE confident answer than a successful one. Before this
			// fix, ferr was discarded and unlogged, and — because nothing here
			// touched out.Complete — vaultSearchStatement fell straight to its
			// `case out.Complete:` branch and rendered the fully-confident
			// "Searched the whole of this knowledge base; its index was
			// complete at query time." A SUCCESSFUL probe that instead found
			// the index behind only ever produces the hedged "Searched X of Y"
			// sentence below — failure must be at least as cautious as that,
			// never more confident. complete's own contract
			// (VaultSearchResponse.yaml) says true means "the index was
			// built, CURRENT, and no kind's result was clamped or refused";
			// a probe failure means currency could not be verified at all, so
			// asserting complete here would assert something never checked.
			logger.WarnCF("rest", "knowledge: vault search freshness probe failed",
				map[string]any{"collection_id": collectionID, "error": ferr.Error()})
			if hasHits := vaultSearchHasAnyHit(out); hasHits || !out.Complete {
				out.Complete = false
				if out.CompleteReason == nil {
					reason := "the index's freshness could not be verified for this search, " +
						"so it may not reflect the very latest changes"
					out.CompleteReason = &reason
				}
			}
		} else if fresh.ScannedFiles > 0 {
			if hasHits := vaultSearchHasAnyHit(out); hasHits || !out.Complete {
				searched := fresh.IndexedFiles
				total := fresh.ScannedFiles
				out.NotesSearched = &searched
				out.NotesTotalKnown = &total
			}
		}
	}

	// LIMIT CLAMP DISCLOSURE (MV-9/FR-037): the handler clamps a request above
	// knowledgefind.MaxLimit to that cap; the clamp is reported, never silent.
	// Not scope-sensitive (see applyVaultSearchLimitDisclosure) — shared with
	// outOfScopeVaultSearchResponse so both paths disclose it identically.
	applyVaultSearchLimitDisclosure(&out, limitClamped, requestedLimit)

	statement := vaultSearchStatement(out)
	out.Statement = &statement

	return out
}

// vaultSearchAttachments runs the attachment-kind Find and builds the
// Attachments group. It returns the hits, whether the search covered the
// whole collection, and — when not — the engine's own reason.
func vaultSearchAttachments(ctx context.Context, env vaultprops.FindEnv, query string, limit int) ([]gen.VaultSearchAttachmentHit, bool, string) {
	out := []gen.VaultSearchAttachmentHit{}
	resp, ready := runVaultSearchFind(ctx, env, query, "", gen.VaultFindRequestKindAttachment, limit)
	if !ready {
		return out, false, vaultSearchIncompleteReason(resp, ready)
	}
	for i := range resp.Rows {
		out = append(out, vaultSearchAttachmentHit(&resp.Rows[i]))
	}
	if !resp.Complete {
		return out, false, vaultSearchIncompleteReason(resp, ready)
	}
	return out, true, ""
}

// vaultSearchAttachmentHit builds one attachment hit. name is the basename of
// the (forward-slash, collection-relative) path — what the query matched
// against — computed rather than re-read, since attachment bytes are never
// opened by this surface (pkg/knowledge/index.go::indexAttachment records
// filename and path only).
func vaultSearchAttachmentHit(row *gen.VaultFindRow) gen.VaultSearchAttachmentHit {
	return gen.VaultSearchAttachmentHit{Path: row.Path, Name: path.Base(row.Path)}
}

// vaultSearchStatement composes the server-authored, render-ready coverage
// sentence (MV-9's `statement`), mirroring the retired surface's
// knowledgeStatement (rest_knowledge.go, deleted by US-5): a coverage clause
// first, then any clamp disclosure appended.
//
// The coverage clause prefers the notes_searched/notes_total_known numbers
// when known — "Searched X of Y notes known to the index." or, when the total
// itself is not known, "Searched X notes so far." (never inventing a
// denominator, FR-036) — and otherwise falls back to the merged
// complete/complete_reason verdict.
func vaultSearchStatement(out gen.VaultSearchResponse) string {
	parts := make([]string, 0, 3)

	switch {
	case out.NotesSearched != nil && out.NotesTotalKnown != nil:
		parts = append(parts, fmt.Sprintf("Searched %d of %d notes known to the index.",
			*out.NotesSearched, *out.NotesTotalKnown))
	case out.NotesSearched != nil:
		parts = append(parts, fmt.Sprintf("Searched %d notes so far.", *out.NotesSearched))
	case out.Complete:
		parts = append(parts, vaultSearchCompleteStatement)
	}

	if !out.Complete {
		if out.CompleteReason != nil && strings.TrimSpace(*out.CompleteReason) != "" {
			parts = append(parts, strings.TrimSpace(*out.CompleteReason))
		} else if len(parts) == 0 {
			parts = append(parts, "This knowledge base's index is not fully ready, so these results may be incomplete.")
		}
	}

	if out.LimitClamped != nil && *out.LimitClamped {
		requested := 0
		if out.LimitRequested != nil {
			requested = *out.LimitRequested
		}
		parts = append(parts, fmt.Sprintf(
			"The requested result count of %d was clamped to the maximum of %d.",
			requested, vaultSearchMaxLimit))
	}

	if len(parts) == 0 {
		parts = append(parts, vaultSearchCompleteStatement)
	}
	return strings.Join(parts, " ")
}

// runVaultSearchFind runs one Find and separates "the engine could not search
// yet" from "the engine searched and found rows/none".
//
// ready=false means the query was refused because the index is not available
// yet (never built, or still catching up). That is NOT a server error and NOT
// an empty answer — it is the freshness state the caller must be told about, so
// mergeVaultSearchCompleteness records the reason and the caller carries no rows
// for this kind.
//
// recordType is optional: when non-empty the words query is scoped to that
// declared record type (so the engine renders that type's columns as cells);
// when empty it runs over kind=note.
func runVaultSearchFind(ctx context.Context, env vaultprops.FindEnv, query, recordType string, kind gen.VaultFindRequestKind, limit int) (gen.VaultFindResponse, bool) {
	words := query
	k := kind
	l := limit
	req := gen.VaultFindRequest{Words: &words, Kind: &k, Limit: &l}
	if recordType != "" {
		rt := recordType
		req.Type = &rt
	}
	// RenderRows lets this in-process caller take the whole page in one call
	// under its own bound, skipping the language-model byte budget — the same
	// reason the view-result endpoint sets it.
	deps := env.Deps
	deps.RenderRows = limit

	resp, err := knowledgefind.Find(ctx, deps, req)
	if err != nil {
		// F10 (2026-09-08 code review): the CALLER already gets an honest
		// answer via the ready=false → CompleteReason path
		// (vaultSearchIncompleteReason), but a genuine engine error used to be
		// discarded here with no log line at all — collapsed onto the exact
		// same ready=false outcome as resp.Refused's ordinary, expected
		// "index not ready yet" state. An OPERATOR watching for a recurring
		// index fault (a corrupted index, a read failure) would see nothing
		// distinguishing a real error from routine "still indexing", request
		// after request. resp.Refused alone is not logged here — it is the
		// engine's own deliberate, self-explanatory signal, already visible
		// in the response; err != nil is the case with no other trail.
		logger.WarnCF("rest", "knowledge: vault search find failed",
			map[string]any{"kind": string(kind), "record_type": recordType, "error": err.Error()})
		return resp, false
	}
	if resp.Refused {
		return resp, false
	}
	return resp, true
}

// vaultSearchRecords runs the query once PER declared record type and merges the
// hits, deduplicated by path and capped at limit. Scoping by type is what makes
// the engine render that type's declared properties as cells — the typed values
// a record hit exists to show. The record type is therefore KNOWN for each row
// (it is the query's own scope), so it is stamped directly rather than re-read.
func vaultSearchRecords(ctx context.Context, env vaultprops.FindEnv, query string, limit int) ([]gen.VaultSearchRecordHit, bool, string) {
	out := []gen.VaultSearchRecordHit{}
	complete := true
	reason := ""

	// I6 (2026-09-09 code review): env.SchemaReport — the record-type schema
	// FILES that failed to even parse — used to be discarded one layer down
	// (vaultprops.OpenFindEnv threw away records.LoadSchemas' *SchemaLoadReport
	// entirely), so a rejected schema could never surface here at all.
	// env.Schemas.Types() below names only the record types that DID load; a
	// type whose schema file is broken is invisible to that loop — exactly the
	// twin defect vaultSearchViewHits had for a broken view file. The fix is
	// the same: any schema load rejection makes the records group, and
	// therefore the whole answer, incomplete UNCONDITIONALLY — not only when
	// the broken file's declared type happens to match this query, because a
	// file this endpoint could not even read is one it cannot truthfully say
	// would not have matched.
	if env.SchemaReport != nil && !env.SchemaReport.OK() {
		complete = false
		if rejTypes := env.SchemaReport.RejectedTypes(); len(rejTypes) > 0 {
			reason = fmt.Sprintf(
				"records: %d record-type schema file(s) failed to load and could not be searched (%s)",
				len(env.SchemaReport.Rejections), strings.Join(rejTypes, ", "))
		} else {
			reason = fmt.Sprintf(
				"records: %d record-type schema file(s) failed to load and could not be searched",
				len(env.SchemaReport.Rejections))
		}
	}

	if env.Schemas == nil {
		return out, complete, reason
	}
	types := env.Schemas.Types()
	sort.Strings(types)

	seen := map[string]bool{}
	for idx, rt := range types {
		if len(out) >= limit {
			// F1 (2026-09-08 code review): the merge cap was reached before
			// every declared record type could even be QUERIED — types[idx:]
			// were never asked about at all, which is a real coverage gap, not
			// merely a page boundary (contrast NOTES: one single Find call
			// covers the whole note corpus and only PAGINATES its own already-
			// complete answer via NextCursor/notes_capped_at_limit). Nothing
			// below this point in the loop ever ran, so `complete` must not
			// stay true — the caller was about to render "its index was
			// complete at query time" over record types it never searched.
			complete = false
			if reason == "" {
				reason = fmt.Sprintf(
					"records: the result limit was reached before every declared record type "+
						"could be searched (%d of %d types)", idx, len(types))
			}
			break
		}
		resp, ready := runVaultSearchFind(ctx, env, query, rt, gen.VaultFindRequestKindRecord, limit)
		if !ready || !resp.Complete {
			complete = false
			if reason == "" {
				reason = vaultSearchIncompleteReason(resp, ready)
			}
		}
		if !ready {
			continue
		}
		for i := range resp.Rows {
			if len(out) >= limit {
				// The cap was hit partway through THIS type's own (otherwise
				// complete) result set — some of its matching rows are
				// dropped from the merged answer, so the verdict cannot claim
				// completeness either, for the same reason as above.
				complete = false
				if reason == "" {
					reason = fmt.Sprintf(
						"records: more %q record hits exist beyond the result limit", rt)
				}
				break
			}
			row := &resp.Rows[i]
			if seen[row.Path] {
				continue
			}
			seen[row.Path] = true
			out = append(out, vaultSearchRecordHit(rt, row))
		}
	}
	return out, complete, reason
}

// mergeVaultSearchCompleteness folds one kind's verdict into the overall one.
// The result is complete only when every kind that ran was complete; the first
// reason encountered is the one shown.
func mergeVaultSearchCompleteness(out *gen.VaultSearchResponse, resp gen.VaultFindResponse, ready bool) {
	if ready && resp.Complete {
		return
	}
	out.Complete = false
	if out.CompleteReason != nil {
		return
	}
	reason := vaultSearchIncompleteReason(resp, ready)
	if reason != "" {
		out.CompleteReason = &reason
	}
}

// vaultSearchIncompleteReason names why a verdict is not complete, preferring
// the engine's own sentence so the UI never has to invent one.
//
// Problems[0].Reason is checked BEFORE CompleteReason: finishVerdict
// (pkg/records/knowledgefind/assemble.go) writes CompleteReason as a terse
// summary — "N problem(s) reported" — while the actual, informative sentence
// (the platform-naming refusal, or the "reflects N of M files" coverage
// warning) lives in the problem itself. Preferring the summary over the
// sentence it summarizes would surface the less useful of the two.
func vaultSearchIncompleteReason(resp gen.VaultFindResponse, ready bool) string {
	if len(resp.Problems) > 0 && strings.TrimSpace(resp.Problems[0].Reason) != "" {
		return resp.Problems[0].Reason
	}
	if resp.CompleteReason != nil && strings.TrimSpace(*resp.CompleteReason) != "" {
		return *resp.CompleteReason
	}
	if !ready {
		return "the knowledge base index is not ready yet, so these results may be incomplete"
	}
	return ""
}

// vaultSearchNoteHit builds one note hit, attaching a re-read snippet when the
// query can be located in the note as it is on disk now.
//
// When it cannot — the match moved, or the file could not be re-read —
// ExcerptUnavailable is set true (MV-9's deliberate reduction of the retired
// surface's 5-reason enum to one boolean, R2-MIN-010: the find path cannot
// attribute the old re-read reasons, so it reports only that an excerpt could
// not be produced). The hit still renders by title and path either way.
func vaultSearchNoteHit(collectionRoot string, row *gen.VaultFindRow, query string) gen.VaultSearchNoteHit {
	hit := gen.VaultSearchNoteHit{Path: row.Path, Title: row.Title}
	if snip := vaultSearchSnippet(collectionRoot, row.Path, query); snip != "" {
		hit.Snippet = &snip
		return hit
	}
	unavailable := true
	hit.ExcerptUnavailable = &unavailable
	return hit
}

// vaultSearchRecordHit builds one record hit, carrying the engine's rendered
// typed cells so the reader sees which values matched. recordType is the query's
// own scope, so it is known rather than inferred.
func vaultSearchRecordHit(recordType string, row *gen.VaultFindRow) gen.VaultSearchRecordHit {
	cells := row.Cells
	if cells == nil {
		cells = []gen.VaultFindCell{}
	}
	hit := gen.VaultSearchRecordHit{Path: row.Path, Title: row.Title, Cells: cells}
	if row.Id != nil {
		hit.Id = row.Id
	}
	if recordType != "" {
		rt := recordType
		hit.RecordType = &rt
	}
	return hit
}

// vaultSearchViewHits returns the saved views whose name or label matches the
// query, case-insensitively, ordered by name and capped at limit, plus
// whether every view in scope was actually CHECKED against the query, and —
// when not — why.
//
// F1 (2026-09-08 code review): the scan used to break at `limit` with no
// completeness signal at all — views sorted after the cap point were never
// even compared against the query, yet the caller rendered "its index was
// complete at query time" regardless. Unlike NOTES (one Find call whose own
// NextCursor already means "the corpus was fully covered, just paginated"),
// this is a local, unpaginated scan: stopping partway through it is a real
// coverage gap over the view set, not a page boundary.
//
// I6 (2026-09-09 code review): env.ViewReport — the set of view FILES that
// failed to even PARSE — was never consulted here. env.Views.Views() below is
// only the successfully-loaded set (records.LoadViews already separates the
// two); a views/broken.yaml that fails to parse was silently absent from both
// the hit list AND the completeness verdict, so a vault with one broken view
// file still got a bare "its index was complete at query time." A file this
// endpoint could not even read is a file it cannot truthfully say does not
// match the query, so any load rejection makes the views group — and
// therefore the whole answer — incomplete UNCONDITIONALLY, not only when the
// broken file's own (possibly unreadable) name happens to match: the whole
// point of the finding is that a caller has no way to know whether it would
// have. rest_knowledge_view.go:261-262 already reads this same ViewReport for
// exactly this purpose (a rejected-but-named view); this is the search
// surface's analogue.
func vaultSearchViewHits(env vaultprops.FindEnv, query string, limit int) ([]gen.VaultSearchViewHit, bool, string) {
	out := []gen.VaultSearchViewHit{}
	complete := true
	reason := ""
	if env.ViewReport != nil && !env.ViewReport.OK() {
		complete = false
		if names := env.ViewReport.RejectedNames(); len(names) > 0 {
			reason = fmt.Sprintf(
				"views: %d saved view file(s) failed to load and could not be checked against the query (%s)",
				len(env.ViewReport.Rejections), strings.Join(names, ", "))
		} else {
			reason = fmt.Sprintf(
				"views: %d saved view file(s) failed to load and could not be checked against the query",
				len(env.ViewReport.Rejections))
		}
	}
	if env.Views == nil {
		return out, complete, reason
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return out, complete, reason
	}
	views := env.Views.Views()
	sort.Slice(views, func(i, j int) bool { return views[i].Name() < views[j].Name() })
	for _, v := range views {
		if len(out) >= limit {
			complete = false
			if reason == "" {
				reason = "views: the result limit was reached before every saved view could be checked"
			}
			return out, false, reason
		}
		name := v.Name()
		label := v.DisplayLabel()
		if !strings.Contains(strings.ToLower(name), needle) && !strings.Contains(strings.ToLower(label), needle) {
			continue
		}
		hit := gen.VaultSearchViewHit{View: name, Label: label}
		if v.Def.Kind != nil {
			k := string(*v.Def.Kind)
			hit.Kind = &k
		}
		if v.Def.Type != nil {
			t := *v.Def.Type
			hit.Type = &t
		}
		out = append(out, hit)
	}
	return out, complete, reason
}

// ---------------------------------------------------------------------------
// Snippet — a render-time excerpt, NOT a second search engine
//
// The MATCH decision (which notes, in what order, with the engine's prefix and
// coverage rules) belongs to knowledgefind. This only renders a window of the
// note around the first query term for a reader's eye, from the file exactly as
// it is on disk. When no term can be located — the match moved, or the file
// cannot be read — it returns "" and the hit is shown by title and path alone,
// never with a fabricated excerpt.
// ---------------------------------------------------------------------------

// vaultSearchSnippet reads the note at collectionRoot/relPath and returns a
// short excerpt around the first query term found in it, or "" when none is.
func vaultSearchSnippet(collectionRoot, relPath, query string) string {
	body, ok := vaultSearchReadNoteHead(collectionRoot, relPath)
	if !ok {
		return ""
	}
	lowerBody := strings.ToLower(body)
	lowerPos := -1
	for _, term := range vaultSearchQueryTerms(query) {
		if i := strings.Index(lowerBody, term); i >= 0 {
			lowerPos = i
			break
		}
	}
	if lowerPos < 0 {
		return ""
	}
	// strings.ToLower can change byte length (e.g. U+0130 → "i̇"), so a byte
	// offset into lowerBody is NOT a valid offset into body. Map it back to the
	// original bytes; otherwise the window is misaligned and, when folding
	// expands text before the match, pos can exceed len(body) and panic.
	return vaultSearchWindow(body, vaultSearchOrigOffset(body, lowerPos))
}

// vaultSearchReadNoteHead reads up to the scan cap of the note, refusing any
// path that resolves outside the collection root (defence in depth — the engine
// already returns only contained, collection-relative paths).
func vaultSearchReadNoteHead(collectionRoot, relPath string) (string, bool) {
	clean := filepath.Clean(filepath.FromSlash(relPath))
	if clean == "" || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", false
	}
	full := filepath.Join(collectionRoot, clean)
	if full != collectionRoot && !strings.HasPrefix(full, collectionRoot+string(filepath.Separator)) {
		return "", false
	}
	f, err := os.Open(full)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, vaultSearchSnippetScanBytes)
	// F8 (2026-09-08 code review): a single f.Read may legally return fewer
	// bytes than len(buf) WITHOUT io.EOF — a network/FUSE-backed root can
	// return a short read mid-stream. The old single-Read call trusted
	// whatever n it got back as "the whole scan window", so a term sitting
	// past that short read was invisible to vaultSearchSnippet, which then
	// reported excerpt_unavailable: true — telling the reader the excerpt
	// could not be produced when the file was simply never fully read.
	// io.ReadFull loops until the buffer is full or a genuine EOF/error is
	// reached, tolerating io.EOF (file smaller than the scan window, read
	// nothing more) and io.ErrUnexpectedEOF (file smaller than the scan
	// window, read something) — both legitimate "that is the whole file"
	// outcomes, not failures.
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", false
	}
	if n <= 0 {
		return "", false
	}
	return string(buf[:n]), true
}

// vaultSearchQueryTerms lowercases and splits the query into terms, longest
// first, so the most specific term anchors the snippet. Terms are delimited by
// any character that is not a letter, digit, '_' or '-' — using the full Unicode
// letter/digit classes, so a CJK, Cyrillic or accented query still yields terms
// (an ASCII-only class silently dropped them, leaving those hits snippet-less).
func vaultSearchQueryTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// vaultSearchOrigOffset maps a byte offset in strings.ToLower(body) back to the
// corresponding byte offset in body. Case-folding is rune→runes and can change
// byte length, so the two strings do not share offsets; this walks body once,
// accumulating each rune's folded length until it reaches lowerPos. The result
// is always a valid index into body (≤ len(body)), so the caller's window can
// never slice out of range.
//
// F9 (2026-09-08 code review, performance): strings.ToLower can only ever
// change a rune's BYTE LENGTH for a non-ASCII rune — every ASCII byte folds
// to exactly one ASCII byte (upper or not; ASCII case folding is always
// 1-byte-to-1-byte). Before this fix every rune, ASCII included, paid for a
// fresh string(r) allocation plus a strings.ToLower(...) call just to learn
// what is already known for free — up to ~256k allocations walking one 128
// KiB note, times up to `limit` hits per interactive search. The non-ASCII
// path is UNCHANGED — same strings.ToLower(string(r)) computation as before,
// so U+0130's documented 2-byte→3-byte expansion (the reason this function
// exists at all) is still handled exactly as it was.
func vaultSearchOrigOffset(body string, lowerPos int) int {
	if lowerPos <= 0 {
		return 0
	}
	lo := 0
	for i, r := range body {
		if lo >= lowerPos {
			return i
		}
		if r < utf8.RuneSelf {
			lo++
			continue
		}
		lo += len(strings.ToLower(string(r)))
	}
	return len(body)
}

// vaultSearchWindow cuts a whitespace-collapsed window of context around pos,
// with an ellipsis on each side that was actually clipped.
func vaultSearchWindow(body string, pos int) string {
	start := pos - vaultSearchSnippetRadius
	if start < 0 {
		start = 0
	}
	end := pos + vaultSearchSnippetRadius
	if end > len(body) {
		end = len(body)
	}
	// Snap to rune boundaries so a multi-byte character is never split.
	for start > 0 && !utf8RuneStart(body[start]) {
		start--
	}
	for end < len(body) && !utf8RuneStart(body[end]) {
		end++
	}
	window := strings.TrimSpace(collapseWhitespace(body[start:end]))
	if window == "" {
		return ""
	}
	if start > 0 {
		window = "…" + window
	}
	if end < len(body) {
		window = window + "…"
	}
	return window
}

// utf8RuneStart reports whether b is the first byte of a UTF-8 rune (i.e. not a
// continuation byte 0x80–0xBF).
func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// collapseWhitespace turns every run of whitespace into a single space, so a
// snippet that spans newlines and indentation renders as one line.
func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}
