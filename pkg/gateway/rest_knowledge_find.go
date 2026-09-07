// Omnipus — POST /api/v1/library/{workspace_id}/knowledge/find: the HUMAN
// vault search (library-b-c-design-2026-09-07 §C1).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

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
	if strings.TrimSpace(req.CollectionId) == "" {
		jsonErr(w, http.StatusBadRequest, "collection_id is required")
		return
	}

	// FR-037's principle: a limit above the cap is CLAMPED, never rejected.
	limit := vaultSearchDefaultLimit
	if req.Limit != nil && *req.Limit > 0 {
		limit = *req.Limit
	}
	if limit > vaultSearchMaxLimit {
		limit = vaultSearchMaxLimit
	}

	// US-9/FR-053: an out-of-scope collection is an EMPTY, complete answer, and
	// it is resolved BEFORE the rate limiter so probing for another workspace's
	// collections cannot be distinguished by timing a 429 either.
	col, inScope := a.resolveScopedCollection(workspaceID, req.CollectionId)
	if !inScope {
		jsonOK(w, emptyVaultSearchResponse(req.CollectionId))
		return
	}

	if !a.allowKnowledgeRetrieval(w, workspaceID) {
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

	jsonOK(w, buildVaultSearchResult(r.Context(), env, col.Root, req.CollectionId, query, limit))
}

// emptyVaultSearchResponse is the empty-but-honest base every answer builds on:
// every hit array is a real empty slice (never nil, which marshals to null),
// and the verdict is complete — the empty set IS the whole of what this scope
// can see.
func emptyVaultSearchResponse(collectionID string) gen.VaultSearchResponse {
	return gen.VaultSearchResponse{
		CollectionId: collectionID,
		Complete:     true,
		Notes:        []gen.VaultSearchNoteHit{},
		Records:      []gen.VaultSearchRecordHit{},
		Views:        []gen.VaultSearchViewHit{},
	}
}

// buildVaultSearchResult runs the three searches over one opened environment
// and merges their completeness into one verdict.
func buildVaultSearchResult(ctx context.Context, env vaultprops.FindEnv, collectionRoot, collectionID, query string, limit int) gen.VaultSearchResponse {
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
	}
	mergeVaultSearchCompleteness(&out, noteResp, noteReady)

	// VIEWS — name/label match over the loaded view set. No index needed, so it
	// is answered whatever the text index's state.
	out.Views = append(out.Views, vaultSearchViewHits(env, query, limit)...)

	return out
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
	if err != nil || resp.Refused {
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
	if env.Schemas == nil {
		return out, true, ""
	}
	types := env.Schemas.Types()
	sort.Strings(types)

	complete := true
	reason := ""
	seen := map[string]bool{}
	for _, rt := range types {
		if len(out) >= limit {
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
func vaultSearchIncompleteReason(resp gen.VaultFindResponse, ready bool) string {
	if resp.CompleteReason != nil && strings.TrimSpace(*resp.CompleteReason) != "" {
		return *resp.CompleteReason
	}
	if !ready && len(resp.Problems) > 0 && strings.TrimSpace(resp.Problems[0].Reason) != "" {
		return resp.Problems[0].Reason
	}
	if !ready {
		return "the vault index is not ready yet, so these results may be incomplete"
	}
	return ""
}

// vaultSearchNoteHit builds one note hit, attaching a re-read snippet when the
// query can be located in the note as it is on disk now.
func vaultSearchNoteHit(collectionRoot string, row *gen.VaultFindRow, query string) gen.VaultSearchNoteHit {
	hit := gen.VaultSearchNoteHit{Path: row.Path, Title: row.Title}
	if snip := vaultSearchSnippet(collectionRoot, row.Path, query); snip != "" {
		hit.Snippet = &snip
	}
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
// query, case-insensitively, ordered by name and capped at limit.
func vaultSearchViewHits(env vaultprops.FindEnv, query string, limit int) []gen.VaultSearchViewHit {
	out := []gen.VaultSearchViewHit{}
	if env.Views == nil {
		return out
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return out
	}
	views := env.Views.Views()
	sort.Slice(views, func(i, j int) bool { return views[i].Name() < views[j].Name() })
	for _, v := range views {
		if len(out) >= limit {
			break
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
	return out
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
	n, err := f.Read(buf)
	if n <= 0 {
		if err != nil {
			return "", false
		}
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
		return !(r == '_' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r))
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
func vaultSearchOrigOffset(body string, lowerPos int) int {
	if lowerPos <= 0 {
		return 0
	}
	lo := 0
	for i, r := range body {
		if lo >= lowerPos {
			return i
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
