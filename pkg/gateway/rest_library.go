// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// HandleLibrary dispatches all /api/v1/library* requests (library-spec.md).
// The Library is a file explorer over the FULL workspace work tree
// (workspaces/<id>/work/ — see pkg/library's package doc), distinct from the
// UUID-keyed workspace media library (pkg/media/library,
// rest_workspace_media.go). Registration (rest.go is owned by another
// in-flight change and is intentionally not touched here):
//
//	cm.RegisterHTTPHandler("/api/v1/library", a.withUploadAuth(withRateLimit(configLimiter, a.HandleLibrary)))
//	cm.RegisterHTTPHandler("/api/v1/library/", a.withUploadAuth(withRateLimit(configLimiter, a.HandleLibrary)))
//
// withUploadAuth (not the plain 1 MB withAuth) is required because
// POST .../upload streams a multipart body directly through this same
// dispatcher; every JSON-bodied route here is independently capped at 1 MB
// by decodeAndValidate regardless of the outer limit, so the larger ceiling
// only actually matters for the upload route.
func (a *restAPI) HandleLibrary(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/v1/library")
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		http.NotFound(w, r)
		return
	}
	segs := strings.Split(trimmed, "/")

	if len(segs) == 1 && segs[0] == "workspaces" {
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryWorkspaces(w, r)
		return
	}
	if len(segs) == 1 && segs[0] == "move" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryTransfer(w, r, transferModeMove)
		return
	}
	if len(segs) == 1 && segs[0] == "copy" {
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryTransfer(w, r, transferModeCopy)
		return
	}
	if len(segs) != 2 {
		// The ONE exception to the flat {workspace_id}/{sub} shape: files/search
		// (rest_library_files_search.go) carries a literal two-segment sub-path.
		// Every other route stays exactly two segments, so this is a narrow,
		// exact-match carve-out rather than a general depth relaxation.
		if len(segs) != 3 || segs[1] != "files" || segs[2] != "search" {
			http.NotFound(w, r)
			return
		}
	}

	workspaceID, sub := segs[0], segs[1]
	if err := validateEntityID(workspaceID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid workspace ID")
		return
	}

	switch sub {
	case "entries":
		switch r.Method {
		case http.MethodGet:
			a.handleLibraryEntriesList(w, r, workspaceID)
		case http.MethodDelete:
			a.handleLibraryEntryDelete(w, r, workspaceID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "content":
		switch r.Method {
		case http.MethodGet:
			a.handleLibraryContentGet(w, r, workspaceID)
		case http.MethodPut:
			a.handleLibraryContentPut(w, r, workspaceID)
		default:
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "content-binary":
		if r.Method != http.MethodPut {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryContentBinaryPut(w, r, workspaceID)
	case "upload":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryUpload(w, r, workspaceID)
	case "mkdir":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryMkdir(w, r, workspaceID)
	case "vaults":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryCreateVault(w, r, workspaceID)
	case "rename":
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryRename(w, r, workspaceID)
	case "download":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryDownload(w, r, workspaceID)
	case "inline-disposition":
		if r.Method != http.MethodGet {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryInlineDisposition(w, r, workspaceID)
	case "files":
		// Only the exact files/search sub-path reaches here (guarded above);
		// a bare /library/{workspace_id}/files (len(segs)==2) falls through to
		// this same case with sub=="files" and is rejected below.
		if len(segs) != 3 {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		a.handleLibraryFilesSearch(w, r, workspaceID)
	default:
		http.NotFound(w, r)
	}
}

// mapLibraryErr writes the appropriate HTTP error response for an error
// returned by pkg/library, per library-spec.md's per-operation status
// table: ErrInvalidPath/ErrNotDir → 400, ErrOutsideRoot → 403,
// *DestinationParentNotFoundError → 404 with the missing directory named
// (checked BEFORE the generic ErrNotFound case, since it wraps ErrNotFound
// and would otherwise also match that arm — UAT Issue 4: a bare "not found"
// gave no way to tell "source is missing" from "destination folder doesn't
// exist yet", nor any path to success), ErrNotFound/ErrIsDir → 404 (the
// contract pairs "path does not exist" and "path names a directory" under
// the same 404 for every file-scoped operation), ErrAlreadyExists → 409,
// anything else → 500 (logged).
func mapLibraryErr(w http.ResponseWriter, op, workspaceID string, err error) {
	var destErr *library.DestinationParentNotFoundError
	switch {
	case errors.Is(err, library.ErrInvalidPath):
		jsonErr(w, http.StatusBadRequest, "invalid path")
	case errors.Is(err, library.ErrNotDir):
		jsonErr(w, http.StatusBadRequest, "path is not a directory")
	case errors.Is(err, library.ErrOutsideRoot):
		jsonErr(w, http.StatusForbidden, "path resolves outside the workspace work tree")
	case errors.As(err, &destErr):
		jsonErr(w, http.StatusNotFound, fmt.Sprintf(
			"destination directory %q does not exist — create it first with POST /library/{workspace_id}/mkdir",
			destErr.Parent))
	case errors.Is(err, library.ErrNotFound), errors.Is(err, library.ErrIsDir):
		jsonErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, library.ErrAlreadyExists):
		jsonErr(w, http.StatusConflict, "an entry already exists at the destination path")
	case errors.Is(err, library.ErrIsMountRoot):
		// 409, not 500. The engine refuses this because performing it would act
		// on the operator's real folder — deleting a mount's own entry would
		// empty their actual files. Without this case the guard still HELD but
		// reported "internal server error", which reads as a bug in Omnipus
		// rather than as a boundary, and gives the caller nothing to do next.
		jsonErr(w, http.StatusConflict,
			"that is a mounted folder — remove the mount instead "+
				"(DELETE /workspaces/{id}/mounts/{name}), which revokes access without deleting your files")
	case errors.Is(err, library.ErrCrossRootTransfer):
		jsonErr(w, http.StatusBadRequest,
			"cannot rename directly between the work tree and a mounted folder — use move or copy, "+
				"which transfer the contents rather than relinking them")
	default:
		logger.ErrorCF("rest", "library: "+op+" failed",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
	}
}

// openLibraryRoot opens workspaceID's Library root (mkdir-on-demand — see
// library.OpenRoot's doc for why that side effect is deliberate here),
// writing a 500 response and returning ok=false on an already-logged
// failure. label distinguishes the log line for the one call site juggling
// two roots at once (handleLibraryTransfer's from/to).
func (a *restAPI) openLibraryRoot(w http.ResponseWriter, workspaceID, label string) (*library.Root, bool) {
	root, err := library.OpenRoot(a.homePath, workspaceID)
	if err != nil {
		logger.ErrorCF("rest", "library: open "+label+" failed",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return nil, false
	}
	return root, true
}

// --- GET /library/workspaces ---

func (a *restAPI) handleLibraryWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces, err := listWorkspaceFiles(a.homePath)
	if err != nil {
		logger.ErrorCF("rest", "library: list workspaces failed", map[string]any{"error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	nodes := make([]gen.LibraryWorkspaceNode, 0, len(workspaces))
	for _, ws := range workspaces {
		count, countErr := library.CountVisibleRootEntries(a.homePath, ws.ID)
		if countErr != nil {
			logger.WarnCF("rest", "library: count root entries failed",
				map[string]any{"workspace_id": ws.ID, "error": countErr.Error()})
			count = 0
		}
		nodes = append(nodes, gen.LibraryWorkspaceNode{
			Id:         ws.ID,
			Name:       ws.Name,
			EntryCount: int32(count),
		})
	}
	sort.Slice(nodes, func(i, j int) bool {
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	jsonOK(w, nodes)
}

// --- GET/DELETE /library/{workspace_id}/entries ---

func (a *restAPI) handleLibraryEntriesList(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	rel, err := library.CleanRelPath(r.URL.Query().Get("path"))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	includeHidden := false
	if raw := r.URL.Query().Get("include_hidden"); raw != "" {
		parsed, perr := strconv.ParseBool(raw)
		if perr != nil {
			jsonErr(w, http.StatusBadRequest, "include_hidden must be a boolean")
			return
		}
		includeHidden = parsed
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	entries, err := root.List(rel, includeHidden)
	if err != nil {
		mapLibraryErr(w, "list entries", workspaceID, err)
		return
	}
	if entries == nil {
		entries = []gen.LibraryEntry{}
	}
	annotateKnowledgeBaseEntries(root, entries)
	jsonOK(w, entries)
}

// annotateKnowledgeBaseEntries sets IsKnowledgeBase on every directory entry
// in place, so vault-ness is a fact the listing STATES rather than something
// the SPA infers from whichever folders it happened to have queried this
// session (a folder never opened yet, or a session whose react-query cache
// was evicted, used to render as a plain folder even though it was a real
// knowledge base — reproduced with a vault named "UAT Vault" showing the
// plain-folder icon right after creation).
//
// A detection failure (unreadable target, a broken mount, a row that no longer
// resolves inside the root) leaves the field absent for that one entry rather
// than failing the whole listing — the folder still renders, just without the
// vault fact this request could not establish, mirroring root.List's own
// per-entry tolerance for a raced concurrent delete.
func annotateKnowledgeBaseEntries(root *library.Root, entries []gen.LibraryEntry) {
	for i := range entries {
		if !entries[i].IsDir {
			continue
		}
		isKB, established := detectKnowledgeBaseInRoot(root, entries[i].Path)
		if !established {
			continue
		}
		entries[i].IsKnowledgeBase = &isKB
	}
}

// detectKnowledgeBaseInRoot answers knowledge.Detection's question about one
// workspace-relative directory, through the CONFINED root the listing handler
// already holds. The second return reports whether the question could be
// answered at all.
//
// # Why not knowledge.IsKnowledgeBase(root.HostPath(rel))
//
// That was the first implementation, and it was wrong twice.
//
// CONFINEMENT. HostPath is a string join that "grants nothing and opens
// nothing" (its own doc comment); handing its result to knowledge.Detect ran
// os.ReadDir on a RAW HOST PATH, outside the os.Root every other operation in
// this handler goes through. A row reported is_dir that is actually a symlink
// — which library.Root.List does emit, forcing is_dir true on a mount's own
// symlink entry, and which any ordinary directory row becomes if it is swapped
// for a symlink between the listing read and this annotation — was then
// followed straight out of the Library. Routing through root.StatDir means
// os.Root re-walks and re-checks the path at the syscall level, so an escaping
// row is refused ("path escapes from parent") instead of read.
//
// COST. Detect reads and sorts the ENTIRE target directory, and this runs once
// per listed row: 200 subfolders holding 10,000 notes each is ~2,000,000
// dirents for one interactive GET .../entries, and listing the workspace root
// re-listed every mount target — walking a slow or network volume on every
// listing. Detection does not need the listing; it needs to know whether two
// specific marker entries exist at the folder's root, which is a stat.
//
// # One detection rule
//
// The marker NAMES and the VERDICT both stay in pkg/knowledge — this supplies
// only the confined stat that answers "is this marker here?". A folder is a
// knowledge base when either marker directory is present (FR-020), and
// knowledge.Detection.IsKnowledgeBase is what decides that, here as in Detect.
//
// # Known divergence from knowledge.Detect
//
// Detect requires each marker to be a real DIRECTORY and never follows a
// symlink (FR-044), because a folder must not be able to claim knowledge-base
// status by pointing at another folder's config. StatDir resolves through
// os.Root.Stat, which DOES follow a symlink that stays inside the root, so a
// relative in-root symlink named .obsidian/ or .omnipus-vault/ pointing at a
// directory is counted here and not by Detect. An escaping symlink is refused
// either way. Closing this needs a confined LSTAT, which library.Root does not
// currently expose (its StatDir/StatFile both follow); a one-method
// (*library.Root).Lstat, or a stat-shaped detection seam in pkg/knowledge
// alongside DetectUsing, would close it with no change to the rule itself.
// The divergence over-detects and never under-detects, and it is pinned by a
// test so it cannot drift further unnoticed.
func detectKnowledgeBaseInRoot(root *library.Root, rel string) (isKB, established bool) {
	d := knowledge.Detection{Root: rel}
	for _, marker := range []struct {
		name    string
		present *bool
	}{
		{knowledge.MarkerDirName, &d.HasOmnipusMarker},
		{knowledge.ObsidianMarkerDirName, &d.HasObsidianMarker},
	} {
		markerRel := marker.name
		if rel != "" {
			markerRel = rel + "/" + marker.name
		}
		_, err := root.StatDir(markerRel)
		switch {
		case err == nil:
			*marker.present = true
		case errors.Is(err, library.ErrNotFound), errors.Is(err, library.ErrNotDir):
			// Definitively absent, or present but not a directory — which
			// FR-020 says is not a marker. Either way: no marker, and the
			// question IS answered.
		default:
			// Escapes the root, unreadable, a broken mount: the question could
			// not be answered for this row, so the listing states nothing —
			// annotateKnowledgeBaseEntries leaves IsKnowledgeBase absent for
			// established=false, exactly LibraryEntry.yaml's documented
			// tri-state ("file, OR detection could not complete for this
			// row"). F7 (2026-09-08 code review): that silent omission had NO
			// observability at all — unlike KnowledgeBaseInfo's single-entity
			// endpoint, which carries a companion detection_error field for
			// this exact failure and fails LOUDLY, a listing row's detection
			// failure left no trail anywhere, for either the caller or the
			// operator. A real knowledge base whose marker became unreadable
			// would render as a plain folder with zero record of why. Logged
			// here rather than surfaced on the wire (a per-entry
			// detection_error would be a contract change affecting every
			// listing row) so an operator can at least see the failure
			// happening.
			logger.WarnCF("rest", "library: knowledge-base marker detection could not complete",
				map[string]any{"path": markerRel, "marker": marker.name, "error": err.Error()})
			return false, false
		}
	}
	return d.IsKnowledgeBase(), true
}

// libraryContentGetRaceHook is a TEST SEAM: nil in production. When set, it
// runs once inside handleLibraryContentGet, immediately after
// root.ReadContent returns and before the version token is derived (M11) —
// the exact gap that fix closes. A test using this hook to mutate the file
// in that window and asserting the response's ETag still matches the
// response's own Content proves the token comes from the SAME read as the
// content, not a second independent one; that same test fails against a
// handler that re-reads the file for its token instead of deriving it from
// the bytes already in hand.
//
//nolint:gochecknoglobals // a test seam, nil in production.
var libraryContentGetRaceHook func()

// libraryETagValue renders a knowledge version token as the RFC 7232
// quoted-strong ETag EMB-007b requires: `ETag: "v1:…"`. A weak (W/) form is
// never emitted — it is also the only form Go's http.ServeContent
// (scanETag) and every conforming client actually parses.
func libraryETagValue(token knowledge.VersionToken) string {
	return `"` + string(token) + `"`
}

// --- GET/PUT /library/{workspace_id}/content ---

func (a *restAPI) handleLibraryContentGet(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		jsonErr(w, http.StatusBadRequest, "path is required")
		return
	}
	rel, err := library.CleanRelPath(rawPath)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	result, err := root.ReadContent(rel)
	if err != nil {
		mapLibraryErr(w, "get content", workspaceID, err)
		return
	}
	if libraryContentGetRaceHook != nil {
		libraryContentGetRaceHook()
	}

	// M11 fix — read once, derive both. The old code unconditionally called
	// knowledge.ReadFileVersion for the token, which re-opens and re-reads
	// the file from scratch AFTER root.ReadContent above already returned —
	// two independent reads with a window between them for a concurrent
	// writer's atomic rename to land, so Content and the ETag could come
	// from two different on-disk versions of the file. B1
	// (useLibraryFileEditor.ts) is the concrete failure this produces: the
	// text-editor save path pairs whatever this endpoint returns as Content
	// with this endpoint's own ETag, so those two must always come from the
	// SAME read.
	//
	// For a text, non-too-large file — the only case where Content is ever
	// populated below, and therefore the only case where a caller can pair
	// it with the token at all — ReadContent already holds the file's full
	// bytes (result.Content). knowledge.ComputeVersionToken hashes byte-
	// identically to ReadFileVersion (both bottom out in the same
	// sha256-plus-length-suffix digest — see ComputeVersionToken's own doc
	// comment), so deriving the token from those SAME bytes yields the
	// token a fresh read would have returned, without a second read to
	// desynchronize from the first.
	//
	// Binary and too-large files never populate Content (ContentResult's own
	// doc comment — ReadContent deliberately reads only a bounded sniff
	// prefix for those, not the full file, to avoid paying for a multi-MB
	// hash on every listing), so there are no bytes here to hash from; the
	// original ReadFileVersion re-read is kept for exactly those two cases,
	// where there is no Content field for a caller to mismatch it against in
	// the first place.
	var token knowledge.VersionToken
	if result.IsText && !result.TooLarge {
		token = knowledge.ComputeVersionToken([]byte(result.Content))
	} else {
		version, verErr := knowledge.ReadFileVersion(root.HostPath(rel))
		if verErr != nil {
			logger.ErrorCF("rest", "library: read version failed",
				map[string]any{"workspace_id": workspaceID, "path": rel, "error": verErr.Error()})
			jsonErr(w, http.StatusInternalServerError, "internal server error")
			return
		}
		token = version.Token
	}

	resp := gen.LibraryContentResponse{
		Path:     rel,
		IsText:   result.IsText,
		Size:     result.Size,
		TooLarge: result.TooLarge,
	}
	if result.Mime != "" {
		m := result.Mime
		resp.Mime = &m
	}
	if result.IsText && !result.TooLarge {
		c := result.Content
		resp.Content = &c
	}
	// Set BEFORE jsonOK/writeJSON: once WriteHeader is called the header map
	// is flushed and a later Set is silently ignored.
	w.Header().Set("ETag", libraryETagValue(token))
	jsonOK(w, resp)
}

// rfc5987AttrChars is the punctuation RFC 5987 §3.2.1 lets an ext-value carry
// unencoded, alongside ALPHA and DIGIT. Everything else — space, "%", "(",
// every non-ASCII byte — is percent-encoded. Kept as an explicit allow-list
// rather than a "deny these" test so a character nobody thought about is
// encoded, not emitted.
const rfc5987AttrChars = "!#$&+-.^_`|~"

// percentEncodeRFC5987 percent-encodes s (already UTF-8, as every Go string
// from a filesystem name is) into an RFC 5987 ext-value body — the part after
// the charset-and-language prefix of a filename* parameter. Byte-wise, not
// rune-wise: the encoding is defined over the octets of the charset, so a
// multi-byte rune becomes several %XX escapes.
func percentEncodeRFC5987(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			strings.IndexByte(rfc5987AttrChars, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// asciiFallbackFilename reduces name to something safe inside an HTTP
// quoted-string: printable US-ASCII only, with `"` and `\` backslash-escaped.
// Any other byte — non-ASCII, DEL, or a control character that somehow got
// this far — becomes "_".
//
// This is the RFC 6266 §4.3 fallback, read only by a client too old to
// understand filename*. It is allowed to be lossy; the exact name travels in
// filename*.
func asciiFallbackFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r >= 0x20 && r < 0x7F:
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "download"
	}
	return b.String()
}

// contentDispositionAttachment builds an RFC 6266 Content-Disposition value
// for a download of filename (ADR-067 FR-0003).
//
// What was wrong with the previous fmt.Sprintf("attachment; filename=%q", …),
// and what was not: header injection was NOT the problem. %q escapes CR, LF,
// NUL and the double quote, and CleanRelPath refuses control characters
// upstream besides — verified by running it, and the injection cases are kept
// as regression controls in the tests rather than presented as new coverage.
//
// The real defect is non-ASCII. %q leaves a rune like "ü" as its raw UTF-8
// bytes inside a quoted-string, and a quoted-string carries no charset
// declaration, so a client is free to read those bytes as Latin-1 and save
// "Ãœnï…". Stage 0 makes non-ASCII names strictly more common — that is the
// point of it — so the fix ships with it.
//
// The output for a pure-ASCII name is byte-identical to the old construction,
// which is deliberate: the overwhelming majority of downloads must not change
// their headers because of this.
//
// mime.FormatMediaType is not used. It emits filename* ALONE with no ASCII
// fallback (FR-0003 requires both), and it returns the empty string on failure
// — which, written into a header unchecked, produces a bare
// "Content-Disposition:" and a browser that renders the file inline instead of
// downloading it. A silent downgrade from attachment to inline is exactly the
// class of failure this handler must not have.
// contentDispositionDisposition builds an RFC 6266 Content-Disposition value
// for either disposition, sharing one encoder so the two can never disagree
// about how a name is escaped.
//
// The inline form KEEPS the filename. Dropping it (a bare "inline") is not a
// hardening measure — the type comes from the extension table plus nosniff,
// never from the name — and it silently changes what the browser offers when
// the reader saves from an inline view. Unifying the two routes on the shared
// helper did exactly that to the media route, and only the integration test
// noticed.
func contentDispositionWith(kind, filename string) string {
	ascii := asciiFallbackFilename(filename)
	needsExtended := false
	for i := 0; i < len(filename); i++ {
		if filename[i] >= 0x80 {
			needsExtended = true
			break
		}
	}
	if !needsExtended {
		return kind + `; filename="` + ascii + `"`
	}
	// filename first, filename* second: RFC 6266 §4.3 says a recipient that
	// understands both MUST prefer filename*, and Go's own mime.ParseMediaType
	// does, so ordering is a courtesy to lenient parsers rather than a
	// correctness requirement — but it costs nothing to put the fallback where
	// a strictly-first-wins parser finds the safe one.
	return kind + `; filename="` + ascii + `"; filename*=UTF-8''` + percentEncodeRFC5987(filename)
}

// contentDispositionInline is the inline half. An empty name yields a bare
// "inline", which is the correct value when there is no name to offer.
func contentDispositionInline(filename string) string {
	if filename == "" {
		return "inline"
	}
	return contentDispositionWith("inline", filename)
}

func contentDispositionAttachment(filename string) string {
	return contentDispositionWith("attachment", filename)
}

// --- GET /library/{workspace_id}/download ---

func (a *restAPI) handleLibraryDownload(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		jsonErr(w, http.StatusBadRequest, "path is required")
		return
	}
	rel, err := library.CleanRelPath(rawPath)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	f, fi, err := root.OpenFileForDownload(rel)
	if err != nil {
		mapLibraryErr(w, "download", workspaceID, err)
		return
	}
	defer f.Close()

	// ADR-083 EMB-007/EMB-007a/EMB-007b. This is the ONLY read on the
	// annotated-PDF save path (LibraryPdfPreview's loader uses a raw fetch
	// of this endpoint, never GET .../content), so it must carry a version
	// token too — byte-identical to GET .../content's for the same path
	// (test 121), via the SAME knowledge.ReadFileVersion digest.
	//
	// Set HERE, in handleLibraryDownload, and NEVER inside
	// serveLibraryContent/applyLibraryByteHeaders: those are shared by
	// serveLibraryPath too, and http.ServeContent's conditional-GET /
	// If-Range handling keys off a response's ETag — setting one in the
	// shared helper would turn on 304s and change If-Range's validator for
	// every caller, including the audio/video Range-request path. See the
	// header comment on serveLibraryContent (inline_serving.go), which this
	// file must not become a second copy of.
	version, verErr := knowledge.ReadFileVersion(root.HostPath(rel))
	if verErr != nil {
		logger.ErrorCF("rest", "library: read version failed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "error": verErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("ETag", libraryETagValue(version.Token))

	// ADR-067 FR-015a/FR-003g. This used to call http.ServeContent with the
	// filename and no Content-Type, so the type came from the HOST MIME
	// registry and, failing that, from sniffing the first 512 bytes — both
	// forbidden by FR-015, and the registry half means the same binary answers
	// differently on a developer Mac and in a scratch container.
	//
	// forceAttachment is true and must stay true: FR-003g keeps the
	// authenticated Library path serving attachments unchanged, so this
	// response also carries no isolation policy (MV-13's second half). Inline
	// serving belongs to the preview-token path, which is the only URL whose
	// credential is scoped to a single file.
	serveLibraryContent(w, r, f, fi.ModTime(), path.Base(rel), true)
}

// --- GET /library/{workspace_id}/inline-disposition ---

// handleLibraryInlineDisposition answers, for ONE file, whether the Library may
// serve it inline, as what Content-Type, which SPA surface should draw it, and
// whether drawing it makes the browser execute it (ADR-067 D15, FR-080).
//
// WHY THIS ENDPOINT EXISTS RATHER THAN THE SPA WORKING IT OUT. The §10.4
// allow-list and the extension→type table are compiled into the binary and are
// the single source of truth (FR-015a, FR-015b). A second copy in TypeScript is
// a second answer, and the two disagree the first time an extension is added to
// one of them — at which point the SPA mounts a surface for bytes the server
// will not serve that way, which is exactly the type confusion FR-015 exists to
// prevent, arriving from the inside.
//
// IT ANSWERS ABOUT THE FILE, NOT ABOUT A GRANT. Nothing here mints anything;
// fetching the bytes inline still requires a preview token. What it does owe the
// caller is the same containment the rest of the Library owes: the path is
// shape-checked by library.CleanRelPath and then resolved through an
// os.Root-confined Stat, so an out-of-root symlink is a 403 here rather than a
// confident answer about a file the caller may not read.
//
// The file must EXIST. An answer for a path that is not there would be a
// perfectly plausible, entirely fictional classification — and the SPA would
// mount a renderer for it before discovering the 404.
func (a *restAPI) handleLibraryInlineDisposition(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		jsonErr(w, http.StatusBadRequest, "path is required")
		return
	}
	rel, err := library.CleanRelPath(rawPath)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	// StatFile, not Stat: a directory has no disposition, and mapLibraryErr
	// turns library.ErrIsDir into the 404 the contract specifies for it.
	if _, statErr := root.StatFile(rel); statErr != nil {
		mapLibraryErr(w, "inline disposition", workspaceID, statErr)
		return
	}

	jsonOK(w, libraryInlineDispositionFor(rel))
}

// --- POST /library/move, POST /library/copy ---

type libraryTransferMode string

const (
	transferModeMove libraryTransferMode = "move"
	transferModeCopy libraryTransferMode = "copy"
)
