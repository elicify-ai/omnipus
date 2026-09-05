// Omnipus — ADR-067 stage 2: the knowledge-base READ surface over REST.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS
//
// pkg/knowledge was complete, tested and reachable by NOTHING: the binary did
// not import it at all. This file is one of the two seams that connect it —
// the operator's four read endpoints:
//
//	GET  /api/v1/library/{workspace_id}/knowledge          detection + identity
//	POST /api/v1/library/{workspace_id}/knowledge/search    relevance search
//	GET  /api/v1/library/{workspace_id}/knowledge/graph     links/backlinks/…
//	GET  /api/v1/library/{workspace_id}/knowledge/outline   heading outline
//	GET  /api/v1/library/{workspace_id}/knowledge/view      saved-view result (rest_knowledge_view.go)
//	GET  /api/v1/library/{workspace_id}/knowledge/base-views a .base file's imported views (rest_knowledge_base_views.go)
//
// Every one of them CALLS pkg/knowledge. None of them reimplements it: link
// resolution, containment, the index, the incompleteness report and the
// query-time excerpt all live there, and a second copy of any of them here
// would be a second thing to keep honest.
//
// THE CONFINEMENT CHAIN IS THE EXISTING ONE, NOT A SECOND ONE. Path-addressed
// endpoints (detect, outline) resolve exactly as every rest_library.go handler
// does — workspace.Exists → library.CleanRelPath → library.OpenRoot →
// Root.Stat*, which enforces containment through os.Root at the syscall
// boundary — and only then hand the resulting host path to pkg/knowledge.
// Collection-addressed endpoints (search, graph) resolve through
// knowledge.ResolveScope, which is the security-reviewed accessor for "what may
// this workspace address": workspace → workspace.AllowedMountRoots → the
// knowledge bases within those roots, plus the workspace's own work tree.
//
// US-9 (P0) IS A PROPERTY OF THAT SECOND CHAIN. A collection_id that names a
// knowledge base mounted into a DIFFERENT workspace matches nothing in this
// workspace's scope, and the answer is an EMPTY RESULT SET — never 403, never
// 404 (FR-052, FR-053). A permission error would confirm the collection exists,
// which is itself the disclosure the requirement forbids.
//
// NO INDEX COUNTS ON THE DETECT ENDPOINT. KnowledgeBaseInfo deliberately
// carries none: index progress is a streaming state delivered as the
// knowledge_index_progress WS frame (FR-080). A REST field would invite the
// polling loop that decision exists to prevent, so do not add one here however
// convenient it looks.
// ---------------------------------------------------------------------------

// Rate limiting for the two expensive retrieval endpoints (FR-055's principle,
// applied to the operator surface).
//
// TWO INSTANCES, ONE POLICY, AND THE SPLIT IS DELIBERATE. The inner limiter is
// the one pkg/knowledge's SearchTool checks for itself — that check is the
// package's own stated guarantee and must not be disabled or bypassed. The
// outer limiter is the one THIS layer checks, because only this layer can turn
// a refusal into the 429 (with Retry-After) that contracts/openapi.yaml
// documents; the tool answers with prose, which would land as a 500.
//
// They carry identical policy and see the same call stream keyed the same way,
// so the outer one always reaches the ceiling first and the inner one is a
// backstop that never fires in normal operation. Sharing ONE instance between
// them would consume two admissions per request and silently halve the
// configured rate — a limiter that lies about its own limit.
var (
	knowledgeRESTLimiter = knowledge.NewRetrievalRateLimiter(knowledge.RetrievalRateLimitConfig{})
	knowledgeToolLimiter = knowledge.NewRetrievalRateLimiter(knowledge.RetrievalRateLimitConfig{})
)

// knowledgeRateKey is the bucket one caller shares. It is the WORKSPACE, not
// the process: a runaway Library tab in one workspace must not rate-limit
// another workspace's operator out of their own notes.
func knowledgeRateKey(workspaceID string) string { return "library-ui:" + workspaceID }

// HandleLibraryTree is the /api/v1/library/ subtree entry point.
//
// WHY A SHIM RATHER THAN A ROUTE. The knowledge paths carry the workspace id in
// the MIDDLE of the pattern (/library/{id}/knowledge/...), and the gateway's mux
// (pkg/channels/dynamic_mux.go) matches exact paths and trailing-slash subtrees
// only — it has no path wildcards, so "/api/v1/library/{id}/knowledge" cannot be
// registered at all. The subtree registration therefore enters here, this
// function peels off the knowledge sub-paths, and everything else is handed to
// HandleLibrary byte-for-byte unchanged.
func (a *restAPI) HandleLibraryTree(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/v1/library")
	trimmed = strings.TrimPrefix(trimmed, "/")
	segs := strings.Split(trimmed, "/")
	if len(segs) >= 2 && segs[1] == "knowledge" {
		a.handleKnowledge(w, r, segs[0], segs[2:])
		return
	}
	a.HandleLibrary(w, r)
}

// handleKnowledge dispatches the four knowledge endpoints of one workspace.
func (a *restAPI) handleKnowledge(w http.ResponseWriter, r *http.Request, workspaceID string, rest []string) {
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}
	// The WORKSPACE is the addressed resource, so an unknown one is the 404.
	// A folder that does not exist inside a real workspace is NOT — it is
	// described in the body, through detection_error (E-9).
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	sub := ""
	if len(rest) == 1 {
		sub = rest[0]
	} else if len(rest) > 1 {
		http.NotFound(w, r)
		return
	}

	switch sub {
	case "":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleKnowledgeInfo(w, r, workspaceID)
	case "search":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleKnowledgeSearch(w, r, workspaceID)
	case "graph":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleKnowledgeGraph(w, r, workspaceID)
	case "outline":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleKnowledgeOutline(w, r, workspaceID)
	case "view":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleKnowledgeViewResult(w, r, workspaceID)
	case "base-views":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleKnowledgeBaseViews(w, r, workspaceID)
	default:
		http.NotFound(w, r)
	}
}

// ---------------------------------------------------------------------------
// Collection identity
// ---------------------------------------------------------------------------

// knowledgeCollectionID derives the opaque collection_id from a collection
// root's RESOLVED REAL PATH (FR-031).
//
// The real path, not the spelling the caller used, is what makes two mounts of
// one folder — into one workspace or several — carry the same id and therefore
// share one index. It is also why the id is opaque: it is a hash, callers are
// told not to parse it, and nothing here or in the SPA may reconstruct a
// filesystem path from it. Deriving it from the same input as the index key
// (pkg/knowledge/index.go's indexKeyFor) keeps "same collection" meaning one
// thing across the feature.
func knowledgeCollectionID(realRoot string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(realRoot)))
	return "kb_" + hex.EncodeToString(sum[:])[:16]
}

// resolveScopedCollection maps a collection_id to the knowledge base it names,
// WITHIN this workspace's scope and nowhere else.
//
// The only candidates are knowledge.ResolveScope's own collections, which is
// what makes cross-workspace addressing impossible rather than merely refused
// (US-9 AS-2): an id naming another workspace's collection matches nothing here
// and is indistinguishable from an id that names nothing anywhere.
func (a *restAPI) resolveScopedCollection(workspaceID, collectionID string) (knowledge.ScopedCollection, bool) {
	collectionID = strings.TrimSpace(collectionID)
	if collectionID == "" {
		return knowledge.ScopedCollection{}, false
	}
	for _, c := range knowledge.ResolveScope(a.homePath, workspaceID).Collections() {
		if knowledgeCollectionID(c.Root) == collectionID {
			return c, true
		}
	}
	return knowledge.ScopedCollection{}, false
}

// collectionContaining reports which in-scope knowledge base holds absPath, if
// any. Used by the outline endpoint, which serves ANY markdown file (FR-062)
// and must say whether the client may additionally offer search and backlinks.
func (a *restAPI) collectionContaining(workspaceID, absPath string) (knowledge.ScopedCollection, bool) {
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		realPath = filepath.Clean(absPath)
	}
	for _, c := range knowledge.ResolveScope(a.homePath, workspaceID).Collections() {
		if realPath == c.Root || strings.HasPrefix(realPath, c.Root+string(filepath.Separator)) {
			return c, true
		}
	}
	return knowledge.ScopedCollection{}, false
}

// ---------------------------------------------------------------------------
// GET /library/{workspace_id}/knowledge — detection and identity
// ---------------------------------------------------------------------------

func (a *restAPI) handleKnowledgeInfo(w http.ResponseWriter, r *http.Request, workspaceID string) {
	rel, err := library.CleanRelPath(r.URL.Query().Get("path"))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	rootPath := rel
	if rootPath == "" {
		// The contract's root_path has minLength 1 and forbids an absolute or
		// dot-dot spelling; "." is the honest non-empty name for the work-tree
		// root, and is the same spelling the query parameter accepts for it.
		rootPath = "."
	}
	info := gen.KnowledgeBaseInfo{
		WorkspaceId:     workspaceID,
		RootPath:        rootPath,
		IsKnowledgeBase: false,
		Marker:          gen.KnowledgeBaseInfoMarkerNone,
	}

	if _, statErr := root.StatDir(rel); statErr != nil {
		switch {
		case errors.Is(statErr, library.ErrOutsideRoot):
			// The only way to reach this after CleanRelPath is a symlink that
			// leaves the work tree, which is a refusal, not a description.
			jsonErr(w, http.StatusForbidden, "path resolves outside the workspace work tree")
		case errors.Is(statErr, library.ErrNotFound):
			setKnowledgeDetectionError(&info, gen.RootMissing,
				fmt.Sprintf("no such folder in this workspace: %s", rootPath))
			jsonOK(w, info)
		case errors.Is(statErr, library.ErrNotDir):
			setKnowledgeDetectionError(&info, gen.NotADirectory,
				fmt.Sprintf("%s is a file, not a folder", rootPath))
			jsonOK(w, info)
		default:
			setKnowledgeDetectionError(&info, gen.RootUnreadable,
				fmt.Sprintf("cannot read %s: %v", rootPath, statErr))
			jsonOK(w, info)
		}
		return
	}

	abs := root.HostPath(rel)

	// FR-021: detection reads DIRECTORY ENTRIES ONLY. Nothing below opens a
	// note, and the marker document itself is read only AFTER the verdict is
	// already decided.
	det, detErr := knowledge.Detect(abs)
	if detErr != nil {
		setKnowledgeDetectionError(&info, gen.RootUnreadable,
			fmt.Sprintf("cannot read %s: %v", rootPath, detErr))
		jsonOK(w, info)
		return
	}

	info.IsKnowledgeBase = det.IsKnowledgeBase()
	switch {
	case det.HasOmnipusMarker:
		// Both markers present reports the Omnipus one, per the contract.
		info.Marker = gen.KnowledgeBaseInfoMarkerOmnipusVault
	case det.HasObsidianMarker:
		info.Marker = gen.KnowledgeBaseInfoMarkerObsidian
	default:
		info.Marker = gen.KnowledgeBaseInfoMarkerNone
	}
	if !info.IsKnowledgeBase {
		jsonOK(w, info)
		return
	}

	realRoot, realErr := knowledge.ResolveCollectionRoot(abs)
	if realErr != nil {
		setKnowledgeDetectionError(&info, gen.RootUnreadable,
			fmt.Sprintf("cannot resolve %s: %v", rootPath, realErr))
		jsonOK(w, info)
		return
	}
	id := knowledgeCollectionID(realRoot)
	info.CollectionId = &id

	// The marker document. ErrNoMarker is the ORDINARY case for a folder
	// detected through .obsidian/ alone — not an error, and not a downgrade of
	// is_knowledge_base. Anything else is reported loudly rather than defaulted
	// away (E-9): a corrupt marker that silently became {DisplayName: ""} would
	// rename the operator's collection to nothing and report success.
	m, markerErr := knowledge.ReadMarker(realRoot)
	switch {
	case markerErr == nil:
		if name := strings.TrimSpace(m.DisplayName); name != "" {
			info.DisplayName = &name
		}
		if tp, tpOK := knowledgeTemplatePath(realRoot, m); tpOK {
			info.TemplatePath = &tp
		}
	case errors.Is(markerErr, knowledge.ErrNoMarker):
		// Nothing to add; the SPA falls back to the folder's own name.
	default:
		setKnowledgeDetectionError(&info, gen.MarkerUnreadable,
			fmt.Sprintf("cannot read the marker in %s: %v", rootPath, markerErr))
	}

	jsonOK(w, info)
}

// setKnowledgeDetectionError attaches the typed detection failure. It never
// touches is_knowledge_base: E-9 requires the field to carry the last known
// answer rather than being silently downgraded to "ordinary folder".
func setKnowledgeDetectionError(info *gen.KnowledgeBaseInfo, code gen.KnowledgeBaseInfoDetectionErrorCode, msg string) {
	info.DetectionError = &struct {
		Code    gen.KnowledgeBaseInfoDetectionErrorCode `json:"code"`
		Message string                                  `json:"message"`
	}{Code: code, Message: msg}
}

// knowledgeTemplatePath returns the COLLECTION-relative templates folder, and
// whether the collection actually has one.
//
// Templates live inside the marker directory (.omnipus-vault/templates by
// default, ADR-067 D2/D12), so the collection-relative spelling is the marker
// directory plus the marker's own templates_dir. It is reported only when the
// folder is really there: a path to a directory that does not exist is worse
// than no path at all, and FR-101 is about reaching templates without enabling
// hidden files, not about advertising a location nothing has created yet.
func knowledgeTemplatePath(realRoot string, m knowledge.Marker) (string, bool) {
	abs := knowledge.TemplatesPath(realRoot, m)
	fi, err := os.Stat(abs)
	if err != nil || !fi.IsDir() {
		return "", false
	}
	rel, err := filepath.Rel(realRoot, abs)
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// ---------------------------------------------------------------------------
// POST /library/{workspace_id}/knowledge/search
// ---------------------------------------------------------------------------

// knowledgeToolSearchPayload decodes what knowledge.SearchTool.Execute produced.
//
// WHY THE SEARCH ENDPOINT GOES THROUGH THE TOOL AT ALL. FR-050 requires every
// hit to carry path, TITLE and a matched EXCERPT, and FR-050a requires that
// excerpt to be re-read from the file at query time under a shared budget, with
// a machine-readable reason whenever it could not be. All of that machinery —
// excerptAt, titleFor, the containment re-check on every hit path — is
// unexported inside pkg/knowledge and is reachable only through the tool. The
// alternative was a second copy of it here, which is exactly what this file
// says it will not do. One search implementation, one honesty layer, one
// excerpt budget, for the agent and the operator alike.
type knowledgeToolSearchPayload struct { // not-wire-format: decodes the in-process JSON of a tool result produced and consumed inside this one binary; it never crosses the gateway/SPA boundary — the response that does is the generated gen.KnowledgeSearchResponse built from it below
	Results []struct {
		Path               string  `json:"path"`
		Title              string  `json:"title"`
		Kind               string  `json:"kind"`
		Score              float64 `json:"score"`
		Excerpt            string  `json:"excerpt"`
		ExcerptUnavailable string  `json:"excerpt_unavailable"`
	} `json:"results"`
	Report struct {
		Complete      bool   `json:"complete"`
		Indeterminate bool   `json:"indeterminate"`
		Found         int    `json:"found"`
		Indexed       int    `json:"indexed"`
		Total         int    `json:"total"`
		Statement     string `json:"statement"`
	} `json:"report"`
	IndexState string `json:"index_state"`
}

func (a *restAPI) handleKnowledgeSearch(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var req gen.KnowledgeSearchRequest
	if !decodeAndValidate(w, r, "KnowledgeSearchRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
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

	// FR-037: a limit above the cap is CLAMPED and the clamp is REPORTED —
	// never rejected, never silently applied.
	requested := knowledge.SearchDefaultTopN
	if req.Limit != nil && *req.Limit > 0 {
		requested = *req.Limit
	}
	applied := requested
	clamped := false
	if applied > knowledge.SearchMaxTopN {
		applied = knowledge.SearchMaxTopN
		clamped = true
	}
	offset := 0
	if req.Offset != nil && *req.Offset > 0 {
		offset = *req.Offset
	}

	// US-9/FR-053: out of scope is an EMPTY ANSWER, not an error. Resolved
	// BEFORE the rate limiter so that probing for another workspace's
	// collections cannot be distinguished by timing a 429 either.
	col, inScope := a.resolveScopedCollection(workspaceID, req.CollectionId)
	if !inScope {
		jsonOK(w, knowledgeEmptySearchResponse(req.CollectionId, requested, applied, clamped))
		return
	}

	if !a.allowKnowledgeRetrieval(w, workspaceID) {
		return
	}

	// How many hits to ask the index for.
	//
	// Two REST-only concerns are folded in here, and neither may reach the
	// package's own clamp reporting: `offset` pages through an answer the
	// package has no notion of, and `kinds` filters one it produced. Both are
	// applied to a WIDER fetch so that a filtered or paged view is still the
	// real ranking rather than the leftovers of a narrower one — the same
	// reason SearchOptions.Folder is applied before the clamp and not after.
	// The fetch never exceeds the cap, so the package never reports a clamp of
	// its own and the clamp on the response is unambiguously the CALLER's.
	fetch := applied + offset
	if len(knowledgeRequestedKinds(req.Kinds)) > 0 {
		fetch = knowledge.SearchMaxTopN
	}
	if fetch > knowledge.SearchMaxTopN {
		fetch = knowledge.SearchMaxTopN
	}

	tool := knowledgeSearchTool(a.homePath)
	if tool == nil {
		logger.ErrorCF("rest", "knowledge: search tool not registered", map[string]any{"workspace_id": workspaceID})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	ctx := tools.WithAgentID(
		tools.WithWorkspaceID(r.Context(), workspaceID),
		knowledgeRateKey(workspaceID),
	)
	res := tool.Execute(ctx, map[string]any{
		// The collection is named by its RESOLVED ROOT, which Scope.Select
		// matches only against collections already in this workspace's scope.
		// The caller's own collection_id never reaches the tool.
		"collection": col.Root,
		"query":      query,
		"top_n":      fetch,
	})
	if res == nil || res.IsError {
		detail := "no result"
		if res != nil {
			detail = res.ForLLM
		}
		logger.ErrorCF("rest", "knowledge: search failed",
			map[string]any{"workspace_id": workspaceID, "error": detail})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var payload knowledgeToolSearchPayload
	if err := json.Unmarshal([]byte(res.ForLLM), &payload); err != nil {
		logger.ErrorCF("rest", "knowledge: decode search result",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	resp := knowledgeEmptySearchResponse(req.CollectionId, requested, applied, clamped)
	kinds := knowledgeRequestedKinds(req.Kinds)
	skipped := 0
	for _, hit := range payload.Results {
		kind := gen.KnowledgeSearchHitKindNote
		if hit.Kind == string(knowledge.ScanKindAttachment) {
			kind = gen.KnowledgeSearchHitKindAttachment
		}
		if len(kinds) > 0 {
			if _, want := kinds[kind]; !want {
				continue
			}
		}
		if skipped < offset {
			skipped++
			continue
		}
		if len(resp.Hits) >= applied {
			break
		}
		resp.Hits = append(resp.Hits, knowledgeSearchHit(hit.Path, hit.Title, hit.Excerpt, hit.ExcerptUnavailable, kind, hit.Score))
	}

	// FR-035: the statement travels in the SAME payload as the results, and it
	// is composed here rather than by the client so a partial answer can never
	// be phrased as a whole one.
	resp.Incompleteness.Complete = payload.Report.Complete && payload.IndexState == "ready"
	resp.Incompleteness.TotalKnown = !payload.Report.Indeterminate && payload.IndexState == "ready"
	switch {
	case payload.Report.Indeterminate:
		// FR-036: the tree is still being walked. Report the running count and
		// NO denominator — a ratio invented here is the confidently-wrong
		// answer the whole requirement exists to prevent.
		found := int64(payload.Report.Found)
		resp.Incompleteness.IndexedFiles = &found
	case !resp.Incompleteness.Complete && payload.Report.Total > 0:
		indexed := int64(payload.Report.Indexed)
		total := int64(payload.Report.Total)
		resp.Incompleteness.IndexedFiles = &indexed
		resp.Incompleteness.TotalFiles = &total
	}
	resp.Incompleteness.Statement = knowledgeStatement(payload, clamped, requested)

	jsonOK(w, resp)
}

// knowledgeSearchHit builds one wire hit.
//
// The excerpt and its absence are a matched pair, with NO carve-out: a hit
// carries an excerpt, or it carries the machine-readable reason there is none
// (FR-050a a). That invariant used to have a hole exactly where it was least
// defensible — an ATTACHMENT, whose contents are never opened for any reason
// (FR-039a) and which therefore has no body excerpt by construction. The
// contract's enum had no member for it, so the reason pkg/knowledge already
// emitted ("attachment_not_read") was dropped here and the hit reached the SPA
// with neither field: no quote, and no word about why. FR-050a(a)'s amendment
// added the member; this function no longer drops it.
func knowledgeSearchHit(relPath, title, excerpt, unavailable string, kind gen.KnowledgeSearchHitKind, score float64) gen.KnowledgeSearchHit {
	out := gen.KnowledgeSearchHit{Kind: kind, Path: relPath, Score: score, Title: title}
	if excerpt != "" {
		e := excerpt
		out.Excerpt = &e
		return out
	}
	if reason, ok := knowledgeExcerptReason(unavailable); ok {
		out.ExcerptUnavailable = &reason
	}
	return out
}

// knowledgeExcerptReason maps pkg/knowledge's reason onto the contract's enum.
//
// The two vocabularies are not identical and the differences are stated rather
// than papered over:
//
//   - match_not_found → match_moved. Same fact under two names: the note
//     matched when it was indexed, the term is not there now.
//   - path_not_contained → file_unreadable. The indexed path no longer resolves
//     inside the collection, so nothing was opened. "file_unreadable" is the
//     only value in the contract's enum that does not assert something untrue
//     about it; "file_missing" would, since the file is very much there.
//   - attachment_not_read → attachment_not_read. Passed through verbatim now
//     that the contract has the member (FR-050a a). It is NOT a failure and
//     must not be worded as one: nothing went wrong, nothing was even tried —
//     an attachment is matched on its name and path and its bytes are never
//     opened (FR-039a).
func knowledgeExcerptReason(reason string) (gen.KnowledgeSearchHitExcerptUnavailable, bool) {
	switch knowledge.ExcerptReason(reason) {
	case knowledge.ExcerptFileMissing:
		return gen.KnowledgeSearchHitExcerptUnavailableFileMissing, true
	case knowledge.ExcerptFileUnreadable, knowledge.ExcerptNotContained:
		return gen.KnowledgeSearchHitExcerptUnavailableFileUnreadable, true
	case knowledge.ExcerptMatchNotFound:
		return gen.KnowledgeSearchHitExcerptUnavailableMatchMoved, true
	case knowledge.ExcerptBudgetExhausted:
		return gen.KnowledgeSearchHitExcerptUnavailableBudgetExhausted, true
	case knowledge.ExcerptAttachment:
		return gen.KnowledgeSearchHitExcerptUnavailableAttachmentNotRead, true
	default:
		return "", false
	}
}

// knowledgeStatement writes the sentence that rides beside the results.
//
// It is never empty — the contract requires the field, and "absent" would be
// ambiguous between "complete" and "the server forgot". A finished index still
// says so in words; US-6 AS-4's "no incompleteness statement is shown" is the
// CLIENT's rendering decision, taken from incompleteness.complete.
func knowledgeStatement(payload knowledgeToolSearchPayload, clamped bool, requested int) string {
	parts := make([]string, 0, 2)
	switch {
	case payload.IndexState == "not_built":
		parts = append(parts,
			"This knowledge base has not been indexed yet, so these results cover none of it. "+
				"Indexing runs on mount and on a schedule.")
	case payload.Report.Statement != "":
		parts = append(parts, payload.Report.Statement)
	default:
		parts = append(parts, "Searched the whole of this knowledge base; its index was complete at query time.")
	}
	if clamped {
		parts = append(parts, fmt.Sprintf(
			"The requested result count of %d was clamped to the maximum of %d.",
			requested, knowledge.SearchMaxTopN))
	}
	return strings.Join(parts, " ")
}

// knowledgeEmptySearchResponse is the zero answer: no hits, and an
// incompleteness object that still says something true.
//
// It is what an out-of-scope collection_id returns (FR-053) — complete, because
// the empty set IS the whole of what this workspace may see — and the base
// every populated answer is built on, so a field can never be forgotten on one
// path and set on the other.
func knowledgeEmptySearchResponse(collectionID string, requested, applied int, clamped bool) gen.KnowledgeSearchResponse {
	resp := gen.KnowledgeSearchResponse{
		CollectionId: collectionID,
		LimitApplied: applied,
		LimitClamped: clamped,
	}
	// An EMPTY ARRAY, never null. The contract says "always present — an empty
	// array, never null — so a client may map over it without a nil check",
	// and a nil Go slice marshals to null, which is a different answer.
	resp.Hits = []gen.KnowledgeSearchHit{}
	resp.Incompleteness.Complete = true
	resp.Incompleteness.TotalKnown = true
	resp.Incompleteness.Statement = "No knowledge base with that identifier is available in this workspace."
	if clamped {
		resp.LimitRequested = &requested
	}
	return resp
}

// knowledgeRequestedKinds turns the optional kinds filter into a set.
func knowledgeRequestedKinds(kinds *[]gen.KnowledgeSearchRequestKinds) map[gen.KnowledgeSearchHitKind]struct{} {
	if kinds == nil || len(*kinds) == 0 {
		return nil
	}
	out := make(map[gen.KnowledgeSearchHitKind]struct{}, len(*kinds))
	for _, k := range *kinds {
		out[gen.KnowledgeSearchHitKind(k)] = struct{}{}
	}
	return out
}

// knowledgeSearchTool builds the retrieval tool this endpoint delegates to.
//
// It is looked up BY NAME out of knowledge.RetrievalTools rather than by
// position, so a future third tool inserted ahead of it cannot silently turn
// this endpoint into something else.
func knowledgeSearchTool(home string) tools.Tool {
	for _, t := range knowledge.RetrievalTools(knowledge.ToolDeps{Home: home, RateLimiter: knowledgeToolLimiter}) {
		if t.Name() == "knowledge_search" {
			return t
		}
	}
	return nil
}

// allowKnowledgeRetrieval admits one retrieval call, or writes the 429 itself.
func (a *restAPI) allowKnowledgeRetrieval(w http.ResponseWriter, workspaceID string) bool {
	d := knowledgeRESTLimiter.Allow(knowledgeRateKey(workspaceID))
	if d.Allowed {
		return true
	}
	retry := int(d.RetryAfter.Round(time.Second) / time.Second)
	if retry < 1 {
		retry = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	jsonErr(w, http.StatusTooManyRequests, fmt.Sprintf(
		"too many knowledge requests — at most %d per %s. Retry in %ds.",
		d.Limit, d.Window, retry))
	return false
}

// ---------------------------------------------------------------------------
// GET /library/{workspace_id}/knowledge/graph
// ---------------------------------------------------------------------------

func (a *restAPI) handleKnowledgeGraph(w http.ResponseWriter, r *http.Request, workspaceID string) {
	q := r.URL.Query()
	collectionID := strings.TrimSpace(q.Get("collection_id"))
	if collectionID == "" {
		jsonErr(w, http.StatusBadRequest, "collection_id is required")
		return
	}
	kind := gen.KnowledgeGraphResponseKind(strings.TrimSpace(q.Get("kind")))
	if !kind.Valid() {
		jsonErr(w, http.StatusBadRequest,
			"kind must be one of links, backlinks, unresolved, orphans, neighbourhood")
		return
	}
	notePath, pathErr := library.CleanRelPath(q.Get("path"))
	if pathErr != nil {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	needsPath := kind == gen.KnowledgeGraphResponseKindLinks ||
		kind == gen.KnowledgeGraphResponseKindBacklinks ||
		kind == gen.KnowledgeGraphResponseKindNeighbourhood
	if needsPath && notePath == "" {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("path is required for kind %q", kind))
		return
	}

	resp := gen.KnowledgeGraphResponse{
		CollectionId: collectionID,
		Kind:         kind,
		Nodes:        []gen.KnowledgeGraphNode{},
		Edges:        []gen.KnowledgeGraphEdge{},
		Skipped:      []gen.KnowledgeGraphSkip{},
	}
	if needsPath {
		p := notePath
		resp.SourcePath = &p
	}

	// US-9 AS-2, again as an empty answer rather than an error: another
	// workspace's knowledge base is not addressable, and saying so with a 403
	// would confirm it exists.
	col, inScope := a.resolveScopedCollection(workspaceID, collectionID)
	if !inScope {
		jsonOK(w, resp)
		return
	}

	if !a.allowKnowledgeRetrieval(w, workspaceID) {
		return
	}

	root, rootErr := knowledge.NewCollectionRoot(knowledge.OSLinkFS(), col.Root)
	if rootErr != nil {
		logger.ErrorCF("rest", "knowledge: open collection root",
			map[string]any{"workspace_id": workspaceID, "error": rootErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	g, gErr := knowledge.BuildLinkGraph(knowledge.OSLinkFS(), root)
	if gErr != nil {
		logger.ErrorCF("rest", "knowledge: build link graph",
			map[string]any{"workspace_id": workspaceID, "error": gErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// FR-044/FR-112: what the walk refused to follow is REPORTED, never
	// omitted. An empty array is the positive statement that nothing was.
	for _, s := range g.Skipped() {
		resp.Skipped = append(resp.Skipped, knowledgeSkip(s))
	}

	exists := make(map[string]struct{}, len(g.Files()))
	for _, f := range g.Files() {
		exists[f] = struct{}{}
	}
	nodes := newKnowledgeNodeSet(exists)

	switch kind {
	case gen.KnowledgeGraphResponseKindLinks:
		nodes.add(notePath)
		for _, l := range g.Links(notePath) {
			resp.Edges = append(resp.Edges, knowledgeEdge(l))
			nodes.add(knowledgeEdgeTarget(l))
		}
	case gen.KnowledgeGraphResponseKindBacklinks:
		nodes.add(notePath)
		for _, l := range g.Backlinks(notePath) {
			resp.Edges = append(resp.Edges, knowledgeEdge(l))
			nodes.add(l.From)
		}
	case gen.KnowledgeGraphResponseKindUnresolved:
		for _, l := range g.Unresolved() {
			resp.Edges = append(resp.Edges, knowledgeEdge(l))
			nodes.add(l.From)
			nodes.add(knowledgeEdgeTarget(l))
		}
	case gen.KnowledgeGraphResponseKindOrphans:
		for _, n := range g.Orphans() {
			nodes.add(n)
		}
	case gen.KnowledgeGraphResponseKindNeighbourhood:
		hops := knowledgeIntParam(q.Get("hops"), 1)
		maxNodes := knowledgeIntParam(q.Get("limit"), knowledge.MaxNeighborhoodNodes)
		n := g.Neighborhood(notePath, hops, maxNodes)
		inSet := make(map[string]struct{}, len(n.Nodes))
		for _, p := range n.Nodes {
			inSet[p] = struct{}{}
			nodes.add(p)
		}
		// Only the edges WITHIN the returned node set. An edge to a node the
		// bound cut off would describe a neighbourhood wider than the one
		// reported, which is the clipped-graph-read-as-whole failure FR-054's
		// truncation flag exists to prevent.
		for _, p := range n.Nodes {
			for _, l := range g.Links(p) {
				if _, ok := inSet[l.To]; ok && l.State == knowledge.ResolveResolved {
					resp.Edges = append(resp.Edges, knowledgeEdge(l))
				}
			}
		}
		hopLimit, nodeLimit := n.Hops, n.MaxNodes
		resp.HopLimitApplied = &hopLimit
		resp.NodeLimitApplied = &nodeLimit
		resp.Truncated = n.HopsClamped || n.NodesClamped
	}

	resp.Nodes = nodes.sorted()
	jsonOK(w, resp)
}

// knowledgeNodeSet collects the nodes one graph answer mentions, de-duplicated
// and with each one's existence taken from the walk rather than guessed.
type knowledgeNodeSet struct {
	exists map[string]struct{}
	seen   map[string]struct{}
	order  []string
}

func newKnowledgeNodeSet(exists map[string]struct{}) *knowledgeNodeSet {
	return &knowledgeNodeSet{exists: exists, seen: map[string]struct{}{}}
}

func (s *knowledgeNodeSet) add(p string) {
	if p == "" {
		return
	}
	if _, dup := s.seen[p]; dup {
		return
	}
	s.seen[p] = struct{}{}
	s.order = append(s.order, p)
}

func (s *knowledgeNodeSet) sorted() []gen.KnowledgeGraphNode {
	sort.Strings(s.order)
	out := make([]gen.KnowledgeGraphNode, 0, len(s.order))
	for _, p := range s.order {
		_, ok := s.exists[p]
		node := gen.KnowledgeGraphNode{Path: p, Exists: ok}
		if ok {
			// The display title is derived from the FILENAME. A graph answer
			// opens no note to build itself, and a node that does not exist
			// gets no title at all — FR-065's client must be able to tell a
			// real note from a link target that was never written.
			title := knowledgeStemTitle(p)
			node.Title = &title
		}
		out = append(out, node)
	}
	return out
}

// knowledgeEdgeTarget is the to_path an edge reports: the resolved target, or
// the normalised link text when there is none. Never empty — the contract's
// to_path has minLength 1, and an unresolved link still has to name what it
// tried to reach.
func knowledgeEdgeTarget(l knowledge.ResolvedLink) string {
	if l.State == knowledge.ResolveResolved && l.To != "" {
		return l.To
	}
	if t := strings.TrimSpace(l.Target); t != "" {
		return t
	}
	if raw := strings.TrimSpace(l.Raw); raw != "" {
		return raw
	}
	return "(empty link)"
}

func knowledgeEdge(l knowledge.ResolvedLink) gen.KnowledgeGraphEdge {
	e := gen.KnowledgeGraphEdge{
		FromPath:   l.From,
		ToPath:     knowledgeEdgeTarget(l),
		Resolution: knowledgeEdgeResolution(l),
		Ambiguous:  l.Ambiguous,
	}
	if t := strings.TrimSpace(l.Target); t != "" {
		e.LinkText = &t
	}
	if l.Alias != "" {
		alias := l.Alias
		e.Alias = &alias
	}
	if l.Heading != "" {
		heading := l.Heading
		e.Heading = &heading
	}
	if l.Embed {
		embed := true
		e.Embed = &embed
	}
	if l.Ambiguous && len(l.Candidates) > 0 {
		candidates := append([]string(nil), l.Candidates...)
		e.Candidates = &candidates
	}
	return e
}

// knowledgeEdgeResolution names WHICH rung of FR-040's ladder produced the
// target.
//
// pkg/knowledge resolves by that ladder but does not record which rung fired,
// so the rung is derived here from what it does record. The derivation reads
// the ladder backwards and touches no filesystem:
//
//   - Unresolved is unresolved.
//   - Only a BARE wikilink — no slash — is ever resolved by basename at all
//     (NoteIndex.Resolve applies the basename rungs to that shape and nothing
//     else). Every other link, including every markdown link, can only have
//     matched at rung 1.
//   - A bare wikilink whose target IS the resolved path (with or without the
//     .md extension) matched rung 1 too — that is a note at the collection
//     root, which the exact-path rung reaches first.
//   - Otherwise it came through the basename rungs: unique when nothing else
//     matched, and when something did, shortest-path when the winner is
//     strictly shorter than the runner-up, lexicographic when they tie.
//
// This is a mapping, not a second resolver: it never decides where a link
// points, only how to describe a decision already made.
func knowledgeEdgeResolution(l knowledge.ResolvedLink) gen.KnowledgeGraphEdgeResolution {
	if l.State != knowledge.ResolveResolved {
		return gen.KnowledgeGraphEdgeResolutionUnresolved
	}
	target := strings.TrimSpace(l.Target)
	if l.Kind != knowledge.LinkWikilink || strings.Contains(target, "/") {
		return gen.KnowledgeGraphEdgeResolutionExactPath
	}
	if l.To == target || l.To == target+".md" || l.To == target+".markdown" {
		return gen.KnowledgeGraphEdgeResolutionExactPath
	}
	if !l.Ambiguous || len(l.Candidates) < 2 {
		return gen.KnowledgeGraphEdgeResolutionUniqueBasename
	}
	if len(l.Candidates[0]) < len(l.Candidates[1]) {
		return gen.KnowledgeGraphEdgeResolutionShortestPath
	}
	return gen.KnowledgeGraphEdgeResolutionLexicographic
}

func knowledgeSkip(s knowledge.SkippedEntry) gen.KnowledgeGraphSkip {
	out := gen.KnowledgeGraphSkip{Path: s.RelPath, Reason: gen.KnowledgeGraphSkipReasonUnreadable}
	switch s.Reason {
	case knowledge.SkipSymlink:
		out.Reason = gen.KnowledgeGraphSkipReasonSymlink
	case knowledge.SkipOutsideRoot:
		out.Reason = gen.KnowledgeGraphSkipReasonOutsideRoot
	case knowledge.SkipIrregular:
		// Not a regular file and not a symlink — a device node, socket or FIFO.
		// "not_addressable" is the contract's value for a thing this system
		// cannot name as a note; reporting it as "unreadable" would suggest a
		// permissions problem the operator could fix.
		out.Reason = gen.KnowledgeGraphSkipReasonNotAddressable
	case knowledge.SkipUnreadable:
		out.Reason = gen.KnowledgeGraphSkipReasonUnreadable
	}
	if s.Detail != "" {
		detail := s.Detail
		out.Detail = &detail
	}
	return out
}

func knowledgeIntParam(raw string, fallback int) int {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// ---------------------------------------------------------------------------
// GET /library/{workspace_id}/knowledge/outline
// ---------------------------------------------------------------------------

// knowledgeFrontmatterScanBytes bounds how far the frontmatter check reads
// looking for the closing delimiter. Frontmatter is metadata at the head of a
// file; a block that has not closed within this much of it is not one.
const knowledgeFrontmatterScanBytes = 64 << 10

func (a *restAPI) handleKnowledgeOutline(w http.ResponseWriter, r *http.Request, workspaceID string) {
	raw := r.URL.Query().Get("path")
	if strings.TrimSpace(raw) == "" {
		jsonErr(w, http.StatusBadRequest, "path is required")
		return
	}
	rel, err := library.CleanRelPath(raw)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if !knowledge.IsMarkdownPath(rel) {
		jsonErr(w, http.StatusBadRequest, "path is not a markdown file")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	f, _, openErr := root.OpenFileForDownload(rel)
	if openErr != nil {
		mapLibraryErr(w, "knowledge outline", workspaceID, openErr)
		return
	}
	defer func() { _ = f.Close() }()

	malformed := knowledgeFrontmatterMalformed(f)
	if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		logger.ErrorCF("rest", "knowledge: rewind for outline",
			map[string]any{"workspace_id": workspaceID, "error": seekErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// ScanNote STREAMS. FR-034a refuses a note size cap anywhere in this
	// feature, so the outline of a 200 MB note is produced without ever holding
	// it in memory.
	scan, scanErr := knowledge.ScanNote(f)
	if scanErr != nil {
		logger.ErrorCF("rest", "knowledge: scan note for outline",
			map[string]any{"workspace_id": workspaceID, "error": scanErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	out := gen.KnowledgeOutline{
		Path:                 rel,
		IsKnowledgeBase:      false,
		FrontmatterMalformed: &malformed,
		// Empty, not null: "always an array, never null; empty for a file with
		// no headings" — a file with no headings is an ordinary file, and the
		// client must not have to nil-check it.
		Headings: []gen.KnowledgeOutlineHeading{},
	}
	if col, inKB := a.collectionContaining(workspaceID, root.HostPath(rel)); inKB {
		out.IsKnowledgeBase = true
		id := knowledgeCollectionID(col.Root)
		out.CollectionId = &id
	}

	used := map[string]int{}
	for _, h := range scan.Headings {
		line := h.Line
		offset := h.Offset
		entry := gen.KnowledgeOutlineHeading{
			Level: h.Level,
			Slug:  knowledgeHeadingSlug(h.Text, used),
			Text:  h.Text,
		}
		if line > 0 {
			entry.Line = &line
		}
		if offset >= 0 {
			entry.ByteOffset = &offset
		}
		out.Headings = append(out.Headings, entry)
	}

	jsonOK(w, out)
}

// knowledgeHeadingSlug builds the fragment that makes a heading addressable.
//
// Uniqueness within one outline is a contract requirement, so a repeated
// heading text gets a numeric suffix rather than two identical anchors — the
// second of which no client could ever scroll to. An empty heading ("###" with
// no text, which the contract explicitly allows) still needs a non-empty slug.
func knowledgeHeadingSlug(text string, used map[string]int) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "section"
	}
	n := used[slug]
	used[slug] = n + 1
	if n > 0 {
		slug = fmt.Sprintf("%s-%d", slug, n)
	}
	return slug
}

// knowledgeFrontmatterMalformed reports E-17: the file opens with a
// frontmatter block that is not valid YAML.
//
// It is a REPORT, never a refusal — the file is still outlined and still
// indexed for its body text either way. Three shapes count as malformed: a
// block that never closes within the head of the file, one that does not parse
// as YAML, and one that parses as something other than a mapping (frontmatter
// is a set of properties; a bare scalar is not one).
func knowledgeFrontmatterMalformed(r io.Reader) bool {
	head := make([]byte, knowledgeFrontmatterScanBytes)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	text := string(head[:n])
	rest, isFM := strings.CutPrefix(text, "---\n")
	if !isFM {
		if rest, isFM = strings.CutPrefix(text, "---\r\n"); !isFM {
			return false
		}
	}
	end := -1
	for _, delim := range []string{"\n---\n", "\n---\r\n", "\n...\n"} {
		if i := strings.Index(rest, delim); i >= 0 && (end < 0 || i < end) {
			end = i
		}
	}
	if end < 0 {
		if strings.HasSuffix(strings.TrimRight(rest, "\r\n"), "\n---") {
			end = len(strings.TrimRight(rest, "\r\n")) - len("\n---")
		} else {
			// Never closed within the head of the file: this opened as
			// frontmatter and is not a frontmatter block.
			return true
		}
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(rest[:end]), &doc); err != nil {
		return true
	}
	return false
}

// knowledgeStemTitle is the filename-derived display title: the basename with
// its markdown extension removed. It is the same fallback pkg/knowledge uses
// when a note carries neither a frontmatter title nor a heading.
func knowledgeStemTitle(relPath string) string {
	base := path.Base(relPath)
	ext := path.Ext(base)
	if strings.EqualFold(ext, ".md") || strings.EqualFold(ext, ".markdown") {
		return strings.TrimSuffix(base, ext)
	}
	return base
}
