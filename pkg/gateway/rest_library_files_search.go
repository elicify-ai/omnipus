// Omnipus — POST /api/v1/library/{workspace_id}/files/search: the bounded,
// index-free file search over a plain folder or mount
// (docs/internal/specs/unified-search-and-grep-spec.md workstream B/C, US-2,
// FR-003/014/016/017/018/021, MV-1/3/3a/5/11, BDD B).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/filegrep"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS
//
// pkg/filegrep (frozen reference engine, ADR-081 phase 0) searches file
// names/paths always and text-file content under hard bounds, across one or
// more confined roots handed to it as fs.FS — "confinement is the CALLER's
// job" per that package's own doc. This file is that caller for the human
// surface: it resolves a workspace's Library scope (the work tree, or one of
// its mounts, or a subdirectory of either) into the []filegrep.Root list,
// maps the wire request/response, and applies the two governance layers the
// spec requires (MV-11's shared walk semaphore, the knowledge-retrieval-class
// rate limiter) before ever touching disk.
//
// Pattern followed throughout: rest_knowledge_find.go's handler shape, and
// the Library taxonomy (mapLibraryErr, library.CleanRelPath,
// a.openLibraryRoot) already established across rest_library.go — this file
// does not invent a second error-mapping convention.
// ---------------------------------------------------------------------------

// filesSearchWalkSlotWait is how long the REST path waits for a free walk
// slot before answering 429 (MV-11's "brief try"). Short and fixed: the SPA
// already auto-retries once after 500ms on a 429 (spec MV-11), so this only
// needs to smooth over a slot freeing up a moment later, not to queue a
// caller for any meaningful time.
const filesSearchWalkSlotWait = 150 * time.Millisecond

// fileSearchMaxGlobItems / fileSearchMaxGlobLength are the contract's
// include_globs/exclude_globs maxItems/maxLength bounds
// (contracts/components/schemas/FileSearchRequest.yaml). Neither filegrep nor
// Options.Limits.Normalize enforces them — see validateFileSearchGlobs — so
// the handler is the only place these are checked; they must stay in sync
// with the contract by hand (not generated).
const (
	fileSearchMaxGlobItems  = 32
	fileSearchMaxGlobLength = 512
)

// filegrepSearchFn is a swappable seam over filegrep.Search, mirroring this
// codebase's established swappable-seam convention for test observability
// (e.g. pkg/session's sessionLockAcquireFn/sessionLockReleaseFn — see
// CLAUDE.md's session-locking section). Production never reassigns it; the
// ConcurrencyCapAndCancel test substitutes a controllable stand-in so it can
// prove client-disconnect cancellation (r.Context() propagating into the
// engine call) deterministically, without depending on real walk timing.
var filegrepSearchFn = filegrep.Search

func (a *restAPI) handleLibraryFilesSearch(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.FileSearchRequest
	if !decodeAndValidate(w, r, "FileSearchRequest", &req, a.agentLoop.GetConfig().Gateway.ValidateInbound) {
		return
	}

	query := strings.TrimSpace(req.Query)
	if query == "" {
		jsonErr(w, http.StatusBadRequest, "query is required")
		return
	}
	req.Query = query

	// The contract's include_globs/exclude_globs maxItems/maxLength bounds
	// (contracts/components/schemas/FileSearchRequest.yaml) are enforced HERE,
	// unconditionally — decodeAndValidate's schema pass above only runs when
	// gateway.validate_inbound is true (default false), and neither
	// fileSearchOptionsFromRequest nor filegrep.Limits.Normalize clamps glob
	// count or length (Normalize only clamps files/bytes/matches/depth/
	// deadline/output). Left unenforced, an over-large glob list turns every
	// visited file into O(len(include)+len(exclude)) doublestar.Match calls
	// (filegrep's globAllowed), which can burn a shared MV-11 walk slot for
	// its full deadline.
	if err := validateFileSearchGlobs(req.IncludeGlobs, req.ExcludeGlobs); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}

	pathParam := ""
	if req.Path != nil {
		pathParam = *req.Path
	}
	rel, err := library.CleanRelPath(pathParam)
	if err != nil {
		mapLibraryErr(w, "files search", workspaceID, err)
		return
	}

	// Same class as the sibling knowledge-retrieval routes (MV-11, FR-017):
	// one workspace-keyed limiter shared by every Library-UI retrieval
	// endpoint, so a runaway search in one workspace never starves another.
	if !a.allowKnowledgeRetrieval(w, workspaceID) {
		return
	}

	opts := fileSearchOptionsFromRequest(req)

	release, ok := AcquireFilegrepWalkSlot(r.Context(), filesSearchWalkSlotWait)
	if !ok {
		w.Header().Set("Retry-After", "1")
		jsonErr(w, http.StatusTooManyRequests, "too many concurrent file searches — retry shortly")
		return
	}
	defer release()

	libRoot, ok := a.openLibraryRoot(w, workspaceID, "files search")
	if !ok {
		return
	}
	defer func() {
		if closeErr := libRoot.Close(); closeErr != nil {
			logger.WarnCF("rest", "files search: close library root failed",
				map[string]any{"workspace_id": workspaceID, "error": closeErr.Error()})
		}
	}()

	// StatDir both confirms rel exists as a directory AND exercises the SAME
	// os.Root confinement check every other Library operation relies on
	// (symlink escape ⇒ ErrOutsideRoot ⇒ 403 via mapLibraryErr) — the scope
	// is validated through the existing, tested taxonomy before this handler
	// ever opens its own roots below.
	if _, statErr := libRoot.StatDir(rel); statErr != nil {
		mapLibraryErr(w, "files search", workspaceID, statErr)
		return
	}

	roots, closeRoots, buildErr := buildFileSearchRoots(a.homePath, workspaceID, libRoot, rel)
	defer closeRoots()
	if buildErr != nil {
		// StatDir just confirmed rel resolves inside the confined root; a
		// failure constructing this handler's OWN fs.FS for it this soon
		// after can only be a root going away in the gap between the two
		// (a mount's target volume detaching, etc.) — FR-021: visible as
		// root_lost, never a quiet empty result.
		logger.WarnCF("rest", "files search: root unreachable building search roots",
			map[string]any{"workspace_id": workspaceID, "path": rel, "error": buildErr.Error()})
		jsonOK(w, emptyFileSearchResponse(opts))
		return
	}

	result, searchErr := filegrepSearchFn(r.Context(), roots, opts)
	if searchErr != nil {
		// filegrep.Search's error return is reserved for a bad pattern
		// (regex compile) — every other outcome, including every budget
		// stop and a lost root, is expressed in Result instead.
		jsonErr(w, http.StatusBadRequest, "invalid regex pattern: "+searchErr.Error())
		return
	}

	jsonOK(w, fileSearchResponseFromResult(result))
}

// validateFileSearchGlobs enforces the contract's include_globs/
// exclude_globs maxItems:fileSearchMaxGlobItems / maxLength:
// fileSearchMaxGlobLength bounds unconditionally — independent of
// gateway.validate_inbound (decodeAndValidate's schema pass is entirely
// opt-in, see its fast path). Unlike every bound in filegrep.Limits, glob
// count and per-glob length are never touched by Options.Limits.Normalize,
// so this is the only enforcement point for them.
func validateFileSearchGlobs(include, exclude *[]string) error {
	for _, group := range []struct {
		field string
		globs *[]string
	}{
		{"include_globs", include},
		{"exclude_globs", exclude},
	} {
		if group.globs == nil {
			continue
		}
		if len(*group.globs) > fileSearchMaxGlobItems {
			return fmt.Errorf("%s must not contain more than %d entries", group.field, fileSearchMaxGlobItems)
		}
		for _, g := range *group.globs {
			if len(g) > fileSearchMaxGlobLength {
				return fmt.Errorf("%s entries must not exceed %d characters", group.field, fileSearchMaxGlobLength)
			}
		}
	}
	return nil
}

// fileSearchOptionsFromRequest maps the wire request onto filegrep.Options.
// Every field is a downward-only override (MV-3); Normalize (called inside
// filegrep.Search) fills in defaults and clamps over-asks, and the response's
// limits_applied echoes what was actually applied.
func fileSearchOptionsFromRequest(req gen.FileSearchRequest) filegrep.Options {
	opts := filegrep.Options{
		Query: req.Query,
		Case:  filegrep.CaseSmart,
	}
	if req.Regex != nil {
		opts.Regex = *req.Regex
	}
	if req.Case != nil {
		switch *req.Case {
		case gen.Sensitive:
			opts.Case = filegrep.CaseSensitive
		case gen.Insensitive:
			opts.Case = filegrep.CaseInsensitive
		default:
			opts.Case = filegrep.CaseSmart
		}
	}
	if req.IncludeHidden != nil {
		opts.IncludeHidden = *req.IncludeHidden
	}
	if req.MatchAllWords != nil {
		opts.MatchAllWords = *req.MatchAllWords
	}
	if req.IncludeGlobs != nil {
		opts.IncludeGlobs = *req.IncludeGlobs
	}
	if req.ExcludeGlobs != nil {
		opts.ExcludeGlobs = *req.ExcludeGlobs
	}
	if req.ContextLines != nil {
		opts.ContextLines = *req.ContextLines
	}
	if l := req.Limits; l != nil {
		if l.Files != nil {
			opts.Limits.Files = *l.Files
		}
		if l.Bytes != nil {
			opts.Limits.Bytes = int64(*l.Bytes)
		}
		if l.Matches != nil {
			opts.Limits.Matches = *l.Matches
		}
		if l.MatchesPerFile != nil {
			opts.Limits.MatchesPerFile = *l.MatchesPerFile
		}
		if l.Depth != nil {
			opts.Limits.Depth = *l.Depth
		}
		if l.DeadlineMs != nil {
			opts.Limits.Deadline = time.Duration(*l.DeadlineMs) * time.Millisecond
		}
		if l.OutputBytes != nil {
			opts.Limits.OutputBytes = *l.OutputBytes
		}
	}
	return opts
}

// fileSearchHitWire aliases gen.FileSearchResponse.Hits' element type — an
// oapi-codegen-inlined anonymous struct (the schema's `items: $ref` to
// FileSearchHit.yaml is generated inline here rather than reusing the
// separately generated gen.FileSearchHit) — so this file can build hits by
// name instead of respelling the literal at every call site. A type ALIAS
// (`=`), not a new type: it and the generated field remain the identical
// type, so it carries no separate wire-format identity of its own
// (Constraint #8). Mirrors browser_ws.go's browserTabWire /
// rest_executor_preview.go's executorPreviewDroppedArg convention.
type fileSearchHitWire = struct { // not-wire-format: type alias of the generated gen.FileSearchResponse.Hits element shape, not a new wire type
	ContextAfter  *[]string                           `json:"context_after,omitempty"`
	ContextBefore *[]string                           `json:"context_before,omitempty"`
	Excerpt       *string                             `json:"excerpt,omitempty"`
	IsDir         *bool                               `json:"is_dir,omitempty"`
	Line          *int                                `json:"line,omitempty"`
	MatchCount    *int                                `json:"match_count,omitempty"`
	MatchKind     gen.FileSearchResponseHitsMatchKind `json:"match_kind"`
	Path          string                              `json:"path"`
}

// fileSearchResponseFromResult maps a filegrep.Result onto the wire
// FileSearchResponse. hits is always a non-nil, possibly-empty slice (MV-5:
// arrays are always [], never null).
func fileSearchResponseFromResult(result filegrep.Result) gen.FileSearchResponse {
	resp := gen.FileSearchResponse{
		Truncated: result.Truncated,
		Hits:      make([]fileSearchHitWire, 0, len(result.Hits)),
	}
	if result.Truncated {
		reason := gen.FileSearchResponseTruncatedReason(string(result.TruncatedReason))
		resp.TruncatedReason = &reason
		// Finding F-C: TruncatedRoot names the FIRST root that died, when
		// root_lost fired over more than one root — see filegrep.Result's
		// own doc comment. Empty for every other reason and for a
		// single-root search, so it stays omitted rather than sent as "".
		if result.TruncatedReason == filegrep.ReasonRootLost && result.TruncatedRoot != "" {
			truncatedRoot := result.TruncatedRoot
			resp.TruncatedRoot = &truncatedRoot
		}
	}
	for _, h := range result.Hits {
		resp.Hits = append(resp.Hits, fileSearchHitFromEngine(h))
	}

	applyFileSearchLimits(&resp, result.LimitsApplied)

	resp.Stats.FilesVisited = result.Stats.FilesVisited
	resp.Stats.BytesScanned = int(result.Stats.BytesScanned)
	resp.Stats.FilesSkippedProblems = result.Stats.FilesSkippedProblems
	resp.Stats.FilesPrunedIgnored = result.Stats.FilesPrunedIgnored
	resp.Stats.FilesSkippedPerFileCap = result.Stats.FilesSkippedFileCap
	resp.Stats.HitsCappedPerFile = result.Stats.HitsCappedPerFile
	dirsVisited := result.Stats.DirsVisited
	resp.Stats.DirsVisited = &dirsVisited
	filesFilteredGlob := result.Stats.FilesFilteredGlob
	resp.Stats.FilesFilteredGlob = &filesFilteredGlob
	// Findings F-A / F-E: surfaced the same optional, backward-compatible
	// way as dirs_visited/files_filtered_glob above.
	filesSkippedBinary := result.Stats.FilesSkippedBinary
	resp.Stats.FilesSkippedBinary = &filesSkippedBinary
	ignoreFilesUnreadable := result.Stats.IgnoreFilesUnreadable
	resp.Stats.IgnoreFilesUnreadable = &ignoreFilesUnreadable

	return resp
}

// fileSearchHitFromEngine converts one filegrep.Hit to its wire shape.
// match_kind converts by a direct string cast rather than a switch: both
// filegrep.MatchKind and gen.FileSearchResponseHitsMatchKind are documented
// to share the exact "name"/"content" wire values (MV-14), so a switch would
// only be a second place for the two enums to silently drift apart.
func fileSearchHitFromEngine(h filegrep.Hit) fileSearchHitWire {
	out := fileSearchHitWire{
		Path:      h.Path,
		MatchKind: gen.FileSearchResponseHitsMatchKind(string(h.Kind)),
	}
	if h.IsDir {
		isDir := true
		out.IsDir = &isDir
	}
	if h.Kind == filegrep.KindContent {
		line := h.Line
		out.Line = &line
	}
	if h.Excerpt != "" {
		excerpt := h.Excerpt
		out.Excerpt = &excerpt
	}
	if len(h.ContextBefore) > 0 {
		cb := append([]string(nil), h.ContextBefore...)
		out.ContextBefore = &cb
	}
	if len(h.ContextAfter) > 0 {
		ca := append([]string(nil), h.ContextAfter...)
		out.ContextAfter = &ca
	}
	// KB-7b/KB-6a: MatchCount is only ever set by the engine on a collapsed
	// match_all_words hit (h.MatchCount > 0 exactly then — see filegrep's
	// own Hit.MatchCount doc). Zero means "not applicable" on both sides of
	// this boundary, so it stays omitted rather than sent as 0.
	if h.MatchCount > 0 {
		matchCount := h.MatchCount
		out.MatchCount = &matchCount
	}
	return out
}

// applyFileSearchLimits copies a filegrep.Limits (already Normalize()d) onto
// the response's limits_applied echo (R2-MIN-007's clamp disclosure).
func applyFileSearchLimits(resp *gen.FileSearchResponse, lim filegrep.Limits) {
	resp.LimitsApplied.Files = lim.Files
	resp.LimitsApplied.Bytes = int(lim.Bytes)
	resp.LimitsApplied.Matches = lim.Matches
	resp.LimitsApplied.MatchesPerFile = lim.MatchesPerFile
	resp.LimitsApplied.Depth = lim.Depth
	resp.LimitsApplied.DeadlineMs = int(lim.Deadline / time.Millisecond)
	resp.LimitsApplied.OutputBytes = lim.OutputBytes
}

// emptyFileSearchResponse builds the honest root_lost answer for the rare
// race where StatDir confirms a scope resolves inside the confined root but
// this handler's own root construction fails moments later (FR-021: visible,
// never a quiet empty result).
func emptyFileSearchResponse(opts filegrep.Options) gen.FileSearchResponse {
	reason := gen.RootLost
	resp := gen.FileSearchResponse{
		Truncated:       true,
		TruncatedReason: &reason,
		Hits:            []fileSearchHitWire{},
	}
	applyFileSearchLimits(&resp, opts.Limits.Normalize())
	return resp
}

// buildFileSearchRoots resolves rel (already CleanRelPath-cleaned and
// StatDir-confirmed by the caller to exist as a directory inside libRoot)
// into the []filegrep.Root list filegrep.Search walks, plus a func that
// releases every filesystem handle this call opened.
//
// CONFINEMENT MECHANISM (FR-014): every fs.FS handed to filegrep is backed by
// its own, independently opened Go 1.24+ os.Root — the exact primitive
// library.OpenRoot itself uses (pkg/library/root.go: "Every operation goes
// through a Go 1.24+ os.Root ... os.Root refuses any path whose resolution —
// including through a symlink — would leave the root, at the syscall/path-
// walk level"). pkg/library does not export an accessor onto its internal
// os.Root or its mount map, so this function opens a SECOND, independently
// confined os.Root at the identical directory — workspace.SafeWorkDir for
// the work tree, or the mount's already realpath-resolved HostPath (read via
// libRoot.MountAt, so it is the SAME resolved path library.Root itself
// trusts) for a mount — and takes ITS .FS() (stdlib, Go 1.24+). Two live
// os.Root handles open on the same directory are ordinary and independently
// confined; opening a second one does not loosen what the first already
// enforces, and libRoot's own StatDir call (in the caller, above) has
// already exercised that confinement against rel before this function is
// reached at all. This is NEVER os.DirFS on a raw host path: os.DirFS
// performs no containment check whatsoever (a path resolving through a
// symlink escapes silently), which is exactly the CWE-357 TOCTOU class
// os.Root exists to close — see pkg/library/root.go's own package doc for
// the identical reasoning applied to this same confinement primitive.
//
// A path scoped inside a mount searches ONLY that mount (Root.Name == rel,
// so hits report the mount-prefixed workspace-relative path — US-2 AS-3,
// test 21). An unscoped request (rel == "") searches the plain work tree AND
// appends every mount as its own Root: mirroring library.openMountRoots'
// documented precedent, a mount whose target cannot currently be opened
// (detached volume, renamed folder) is SKIPPED rather than failing the whole
// search, so one broken mount never takes a whole-workspace search offline.
// An EXPLICITLY mount-scoped request to a broken mount instead fails earlier,
// at the caller's StatDir check: library.Root.resolve falls back to the
// (symlink-refusing) plain root for a mount with no open root, which os.Root
// reports as escaping/missing — mapLibraryErr turns that into a visible HTTP
// error rather than reaching this function at all.
//
// # The secret set is subtracted from every root
//
// An os.Root confines the walk to one host directory; it says nothing about
// WHICH files inside it a caller may see. Every root built here is therefore
// wrapped in tools.GuardCarveOuts — the SAME guard pkg/tools' `grep` applies
// to its own roots, delegating to the same fspolicy.IsCarveOut predicate
// ResolvePath consults, so the agent surface and the human search bar cannot
// disagree about what a secret is.
//
// Without it, a mount on an ANCESTOR of $OMNIPUS_HOME — warn-and-allow per
// workspace.CheckMountTarget, which hard-refuses only a target inside
// $OMNIPUS_HOME — makes credentials.json, master.key, cli.token, auth.json,
// the config backups, entities/ and system/audit.jsonl greppable through this
// endpoint, excerpt lines and all. That shape is a first-class supported
// installation ($OMNIPUS_HOME is an env var; /srv/omnipus with a mount on
// /srv is as valid as ~/.omnipus), and CheckMountTarget's warning promises the
// operator that "the installation's own secrets remain protected independently
// of this mount".
//
// The policy's WorkDir is this workspace's own work tree, so IsCarveOut's
// own-tree exception keeps the workspace's own files searchable while every
// OTHER workspace's work tree stays denied — the same posture a re-rooted
// workspace turn gets from pkg/tools.
func buildFileSearchRoots(homePath, workspaceID string, libRoot *library.Root, rel string) ([]filegrep.Root, func(), error) {
	var opened []*os.Root
	closeAll := func() {
		for _, r := range opened {
			if err := r.Close(); err != nil {
				logger.WarnCF("rest", "files search: close root failed",
					map[string]any{"workspace_id": workspaceID, "error": err.Error()})
			}
		}
	}

	workDirPath, err := workspace.SafeWorkDir(homePath, workspaceID)
	if err != nil {
		return nil, closeAll, fmt.Errorf("resolve work dir: %w", err)
	}

	// Built once, before any root, and shared by every one of them — a policy
	// that cannot be built is a hard stop, never a search that proceeds
	// unguarded. The caller renders this as an empty, root-lost answer rather
	// than as hits.
	policy, err := fspolicy.EffectiveFSPolicy(
		context.Background(), workDirPath, "", true, homePath, "", workspaceID)
	if err != nil {
		return nil, closeAll, fmt.Errorf("resolve filesystem policy: %w", err)
	}

	if _, target, _, ok := libRoot.MountAt(rel); ok {
		mr, mErr := os.OpenRoot(target)
		if mErr != nil {
			return nil, closeAll, fmt.Errorf("open mount root: %w", mErr)
		}
		opened = append(opened, mr)
		// Guarded at the mount's own root, then narrowed: fs.Sub prefixes
		// every name back onto the guard, so the carve-out still judges the
		// full host path no matter how deep the scope reaches — and the
		// .gitignore reads LoadAncestorIgnore performs go through it too.
		mfs := tools.GuardCarveOuts(target, mr.FS(), policy)
		_, rest, _ := strings.Cut(rel, "/")
		var ancestor []filegrep.AncestorIgnoreLayer
		if rest != "" {
			// The unreadable-ancestor-ignore-file count (finding F-E) is
			// discarded here, same as pkg/tools/grep.go's own call sites: an
			// ancestor .gitignore/.ignore that fails to read degrades to
			// "no additional rules from that file", identical to a missing
			// one, and filegrep's within-walk unreadable-ignore-file
			// counting (Stats.IgnoreFilesUnreadable) already covers the
			// common case for THIS request's own scope.
			ancestor, _ = filegrep.LoadAncestorIgnore(mfs, rest)
			sub, subErr := fs.Sub(mfs, rest)
			if subErr != nil {
				closeAll()
				return nil, func() {}, fmt.Errorf("scope mount to %q: %w", rest, subErr)
			}
			mfs = sub
		}
		return []filegrep.Root{{
			Name: rel, FS: mfs,
			ScopePrefix: rest, AncestorIgnore: ancestor,
		}}, closeAll, nil
	}

	wr, err := os.OpenRoot(workDirPath)
	if err != nil {
		return nil, closeAll, fmt.Errorf("open work root: %w", err)
	}
	opened = append(opened, wr)
	wfs := tools.GuardCarveOuts(workDirPath, wr.FS(), policy)
	name := rel
	var wsAncestor []filegrep.AncestorIgnoreLayer
	if rel != "" {
		wsAncestor, _ = filegrep.LoadAncestorIgnore(wfs, rel)
		sub, subErr := fs.Sub(wfs, rel)
		if subErr != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("scope work tree to %q: %w", rel, subErr)
		}
		wfs = sub
	}
	roots := []filegrep.Root{{
		Name: name, FS: wfs,
		ScopePrefix: rel, AncestorIgnore: wsAncestor,
	}}

	if rel == "" {
		mounts, ok := workspace.LoadMounts(homePath, workspaceID)
		if ok {
			for _, m := range mounts {
				if m.Name == "" || m.HostPath == "" {
					continue
				}
				mr, mErr := os.OpenRoot(m.HostPath)
				if mErr != nil {
					logger.WarnCF("rest", "files search: mount unreachable, skipping",
						map[string]any{"workspace_id": workspaceID, "mount": m.Name, "error": mErr.Error()})
					continue
				}
				opened = append(opened, mr)
				roots = append(roots, filegrep.Root{
					Name: m.Name,
					FS:   tools.GuardCarveOuts(m.HostPath, mr.FS(), policy),
				})
			}
		}
	}

	return roots, closeAll, nil
}
