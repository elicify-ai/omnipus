// rest_uploads.go: File uploads and media serving

package gateway

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/media/library"
	wkspace "github.com/elicify-ai/omnipus/pkg/workspace"
)

// --- File upload ---

const (
	// maxUploadFileSize is the per-file limit enforced via io.LimitReader.
	maxUploadFileSize int64 = 100 << 20 // 100 MB
)

// withUploadAuth is like withAuth but applies a 1 GB total body limit instead of
// the default 1 MB limit so that multi-file uploads can proceed. The per-file
// limit (100 MB) is enforced separately via io.LimitReader inside HandleUpload.
func (a *restAPI) withUploadAuth(handler http.HandlerFunc) http.HandlerFunc {
	return a.withAuthAndBodyLimit(handler, maxUploadFileSize*10)
}

// UploadedFile is the named type gen.UploadedFile, generated from the UploadedFile
// component schema in contracts/openapi.yaml (components/schemas/UploadedFile).
// oapi-codegen v2 generates it as a named struct that is referenced as the element
// type within gen.UploadFilesResponse.Files. Use gen.UploadedFile directly for
// struct literals.

// restAPIHandleUpload carries the shared state of HandleUpload across its stages.
type restAPIHandleUpload struct {
	a            *restAPI
	w            http.ResponseWriter
	sessionID    string
	workspaceID  string
	resp         gen.UploadFilesResponse
	workspaceLib *library.Library
}

// restAPIHandleUploadFlow reports how a block stage of restAPIHandleUpload wants the conductor to proceed.
type restAPIHandleUploadFlow int

const (
	restAPIHandleUploadNext restAPIHandleUploadFlow = iota
	restAPIHandleUploadReturn
	restAPIHandleUploadContinue
	restAPIHandleUploadBreak
)

// HandleUpload handles POST /api/v1/upload — streams multipart file uploads to disk.
// Files are stored at ~/.omnipus/uploads/{session_id}/{sanitized_filename}.
// Max file size per part: 100 MB. Data is streamed directly to disk; the full
// file is never buffered in memory.
//
// ADR-051 Rev 4 (FR-001): when the request carries a workspace_id (query param
// or form field before the file parts), files are routed to the workspace's
// persistent media library (workspaces/<ws>/media/) via library.Upload instead
// of the legacy session-scoped uploads dir. When no workspace_id is present,
// the legacy path is used unchanged (backward compat).
func (a *restAPI) HandleUpload(w http.ResponseWriter, r *http.Request) {
	ru := &restAPIHandleUpload{a: a, w: w}

	if r.Method != http.MethodPost {
		jsonErr(ru.w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// session_id may come from either a query parameter or a form field that
	// appears before any file parts. We prefer the query param for simplicity.
	ru.sessionID = r.URL.Query().Get("session_id")
	// workspace_id (ADR-051 Rev 4 FR-001) follows the same pattern: query
	// param first, form field as fallback before the file parts.
	ru.workspaceID = r.URL.Query().Get("workspace_id")

	// Parse the multipart stream without buffering file content in memory.
	reader, err := r.MultipartReader()
	if err != nil {
		slog.Warn("rest: upload: multipart reader failed", "error", err)
		jsonErr(ru.w, http.StatusBadRequest, fmt.Sprintf("invalid multipart request: %v", err))
		return
	}

	// workspaceLib is resolved lazily on the first file part when workspace_id
	// is set. A nil workspaceLib means workspace routing is unavailable, so the
	// handler falls back to the legacy session-scoped path (graceful
	// degradation — the file still uploads, just not to the library).

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Warn("rest: upload: read part failed", "error", err)
			jsonErr(ru.w, http.StatusBadRequest, fmt.Sprintf("multipart read error: %v", err))
			return
		}

		formName := part.FormName()
		fileName := part.FileName()

		// Non-file field — check for session_id / workspace_id overrides.
		if fileName == "" {
			if formName == "session_id" && ru.sessionID == "" {
				buf, readErr := io.ReadAll(io.LimitReader(part, 256))
				part.Close()
				if readErr != nil {
					slog.Warn("rest: upload: read session_id field", "error", readErr)
					jsonErr(ru.w, http.StatusBadRequest, "could not read session_id field")
					return
				}
				ru.sessionID = strings.TrimSpace(string(buf))
			} else if formName == "workspace_id" && ru.workspaceID == "" {
				buf, readErr := io.ReadAll(io.LimitReader(part, 256))
				part.Close()
				if readErr != nil {
					slog.Warn("rest: upload: read workspace_id field", "error", readErr)
					jsonErr(ru.w, http.StatusBadRequest, "could not read workspace_id field")
					return
				}
				ru.workspaceID = strings.TrimSpace(string(buf))
			} else {
				// Discard unrecognized non-file fields.
				if _, discardErr := io.Copy(io.Discard, part); discardErr != nil {
					slog.Warn("rest: upload: discard field failed", "field", formName, "error", discardErr)
				}
				part.Close()
			}
			continue
		}

		// --- Workspace media library path (ADR-051 Rev 4, FR-001) ---
		//
		// When a workspace_id is present and the workspace library can be
		// resolved, stream the file directly into the workspace's persistent
		// media library (workspaces/<ws>/media/) via library.Upload instead
		// of the legacy session-scoped uploads dir. The library handles
		// filename normalization, MIME sniffing, sha256, the per-file size
		// limit (100 MB), and an atomic write+manifest commit — so this path
		// does not need to replicate any of that.
		if ru.workspaceID != "" {
			if err := validateEntityID(ru.workspaceID); err != nil {
				part.Close()
				jsonErr(ru.w, http.StatusBadRequest, "invalid workspace_id")
				return
			}
			if ru.workspaceLib == nil && ru.a.agentLoop != nil {
				ru.workspaceLib = ru.a.agentLoop.GetWorkspaceLibrary(ru.workspaceID)
				if ru.workspaceLib == nil {
					// GetWorkspaceLibrary (pkg/agent/media_present.go) collapses
					// two very different conditions into the same nil signal:
					// "workspace library genuinely not configured" and "library
					// exists but failed to load" (corrupt manifest, permission
					// error, disk I/O). Silently falling back to the legacy
					// session-scoped path here would mask a real failure behind
					// a plausible 201 — the file would never appear in
					// GET /workspaces/{id}/media, get no refcount/orphan-GC/
					// cascade-delete coverage, and never resolve via
					// media://workspace/... . Re-derive the real cause the same
					// way rest_workspace_media.go's openLibraryForWorkspace
					// does: call library.New directly. It NEVER returns
					// (nil, nil) — every call either succeeds with a valid
					// *Library or fails with a concrete error (verified by
					// reading library.New's full body) — so this
					// deterministically separates the two cases without
					// needing to change GetWorkspaceLibrary's signature (out
					// of scope here: pkg/agent/).
					lib, libErr := library.New(ru.a.homePath, ru.workspaceID)
					if libErr != nil {
						part.Close()
						// Re-review FIX 1: was a bare slog.Error, invisible on a
						// backgrounded gateway (slog.SetDefault is never called
						// anywhere in this repo, so log/slog.Default() never
						// reaches $OMNIPUS_HOME/logs/gateway.log). Route through
						// pkg/logger instead.
						logger.ErrorCF("rest", "upload: workspace library load failed",
							map[string]any{"workspace_id": ru.workspaceID, "error": libErr})
						jsonErr(ru.w, http.StatusInternalServerError,
							fmt.Sprintf("workspace media library unavailable: %v", libErr))
						return
					}
					ru.workspaceLib = lib
				}
			}
			if ru.workspaceLib != nil {
				ref, projection, uploadErr := ru.workspaceLib.Upload(fileName, gen.MediaLibraryEntrySourceUserUpload, part)
				part.Close()

				switch ru.finishWorkspaceUpload(fileName, ref, projection, uploadErr) {
				case restAPIHandleUploadReturn:
					return
				}

				continue
			}
			// workspace_id set but library unavailable → fall through to the
			// legacy session-scoped path (graceful degradation).
			slog.Warn("rest: upload: workspace library unavailable, falling back to session-scoped path",
				"workspace_id", ru.workspaceID)
		}

		// --- Legacy session-scoped path ---

		// Validate session_id before the first file write.
		if ru.sessionID == "" {
			part.Close()
			jsonErr(ru.w, http.StatusBadRequest, "session_id is required (query param or form field before files)")
			return
		}
		if err := validateEntityID(ru.sessionID); err != nil {
			part.Close()
			jsonErr(ru.w, http.StatusBadRequest, "invalid session_id")
			return
		}

		// Sanitize the filename: strip directory components, reject empty result.
		sanitized := filepath.Base(filepath.Clean("/" + fileName))
		if sanitized == "" || sanitized == "." || sanitized == "/" {
			part.Close()
			jsonErr(ru.w, http.StatusBadRequest, fmt.Sprintf("invalid filename: %q", fileName))
			return
		}
		// Additional safety: reject null bytes.
		if strings.ContainsRune(sanitized, 0) {
			part.Close()
			jsonErr(ru.w, http.StatusBadRequest, "filename contains null byte")
			return
		}

		uploadDir := filepath.Join(ru.a.homePath, "uploads", ru.sessionID)
		if mkErr := os.MkdirAll(uploadDir, 0o700); mkErr != nil {
			part.Close()
			slog.Error("rest: upload: mkdir failed", "dir", uploadDir, "error", mkErr)
			jsonErr(ru.w, http.StatusInternalServerError, fmt.Sprintf("could not create upload directory: %v", mkErr))
			return
		}

		// Use O_CREATE|O_EXCL to atomically create the destination file.
		// If another concurrent upload or replay already created a file with
		// the same name (TOCTOU-safe: Stat+Create would race), retry once
		// with a nanosecond suffix to guarantee uniqueness.
		destPath := filepath.Join(uploadDir, sanitized)
		f, createErr := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil && errors.Is(createErr, os.ErrExist) {
			ext := filepath.Ext(sanitized)
			base := strings.TrimSuffix(sanitized, ext)
			sanitized = fmt.Sprintf("%s_%d%s", base, time.Now().UnixNano(), ext)
			destPath = filepath.Join(uploadDir, sanitized)
			f, createErr = os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		}

		// Read Content-Type before closing the part.
		contentType := part.Header.Get("Content-Type")
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		// cleanupUploaded removes all previously uploaded files on error.
		cleanupUploaded := func() {
			for _, prev := range ru.resp.Files {
				os.Remove(filepath.Join(ru.a.homePath, prev.Path))
			}
		}

		if createErr != nil {
			part.Close()
			slog.Error("rest: upload: create file failed", "path", destPath, "error", createErr)
			cleanupUploaded()
			jsonErr(ru.w, http.StatusInternalServerError, fmt.Sprintf("could not create file: %v", createErr))
			return
		}

		// Enforce per-file size limit. If the limit is exceeded, io.Copy returns
		// an error because LimitReader returns 0 bytes after the limit and the
		// copy stops, but to make the violation explicit we detect it below.
		limitedPart := io.LimitReader(part, maxUploadFileSize+1)
		written, copyErr := io.Copy(f, limitedPart)
		f.Close()
		part.Close()

		switch ru.finishLegacyUpload(sanitized, destPath, contentType, cleanupUploaded, written, copyErr) {
		case restAPIHandleUploadReturn:
			return
		}
	}

	if len(ru.resp.Files) == 0 {
		jsonErr(ru.w, http.StatusBadRequest, "no files found in upload")
		return
	}

	jsonCreated(ru.w, ru.resp)
}

// finishWorkspaceUpload handles a workspace library upload result, stages agent access, and records the response.
func (ru *restAPIHandleUpload) finishWorkspaceUpload(fileName string, ref string, projection gen.MediaLibraryEntry, uploadErr error) restAPIHandleUploadFlow {
	if uploadErr != nil {
		slog.Error("rest: upload: workspace library store failed",
			"workspace_id", ru.workspaceID, "filename", fileName, "error", uploadErr)
		// Remove previously uploaded workspace files in this batch.
		ru.a.cleanupWorkspaceUploads(&ru.resp, ru.workspaceLib)
		switch {
		case errors.Is(uploadErr, library.ErrFileTooLarge):
			jsonErr(ru.w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("file %q exceeds 100 MB limit", fileName))
		case errors.Is(uploadErr, library.ErrInvalidFilename):
			jsonErr(ru.w, http.StatusBadRequest,
				fmt.Sprintf("invalid filename: %q", fileName))
		default:
			jsonErr(ru.w, http.StatusInternalServerError,
				fmt.Sprintf("workspace media store failed: %v", uploadErr))
		}
		return restAPIHandleUploadReturn
	}

	var size int64
	if projection.Size != nil {
		size = *projection.Size
	}
	mimeStr := ""
	if projection.Mime != nil {
		mimeStr = *projection.Mime
	}
	// Relative path for the response — informational; the SPA
	// serves workspace media via the media://workspace/ ref, not
	// the /api/v1/uploads/{session_id}/{filename} URL.
	_, mediaID, _ := media.ParseWorkspaceRef(ref)
	relativePath := filepath.Join("workspaces", ru.workspaceID, "media", mediaID)

	// D-1 (library-spec, 2026-07-29 UAT): the media-library blob
	// above lives in workspaces/<id>/media/ — a SIBLING of work/,
	// structurally unreachable by every agent file tool (they
	// open an os.Root at work/ and cannot escape it by
	// construction, ADR-046). Dual-write the SAME bytes as a
	// real, named file inside workspaces/<id>/work/.library/ so
	// library_read/read_file can actually find it — this is the
	// single change that makes the agent-visibility requirement
	// satisfiable. The media-library entry above remains the
	// metadata index (mime/size/sha256/refcount/source); this is
	// purely an additional copy, never a replacement. A failure
	// here means the upload as a whole does NOT satisfy "the
	// agent can read this file", so it is treated as a hard
	// upload failure — the just-created library entry is rolled
	// back rather than left as a half-satisfied promise.
	workRelPath, stageErr := ru.a.stageWorkspaceUploadCopy(ru.workspaceID, mediaID, fileName, ru.workspaceLib)
	if stageErr != nil {
		logger.ErrorCF("rest", "upload: could not stage workspace library copy for agent access",
			map[string]any{
				"workspace_id": ru.workspaceID, "media_id": mediaID,
				"filename": fileName, "error": stageErr.Error(),
			})
		if _, delErr := ru.workspaceLib.Delete(mediaID); delErr != nil {
			slog.Warn("rest: upload: rollback of media-library entry failed after stage failure",
				"media_id", mediaID, "error", delErr)
		}
		ru.a.cleanupWorkspaceUploads(&ru.resp, ru.workspaceLib)
		jsonErr(ru.w, http.StatusInternalServerError,
			fmt.Sprintf("could not stage uploaded file for agent access: %v", stageErr))
		return restAPIHandleUploadReturn
	}
	agent.RecordUploadWorkPath(ref, workRelPath)

	var refPtr *string
	if ref != "" {
		refCopy := ref
		refPtr = &refCopy
	}
	ru.resp.Files = append(ru.resp.Files, gen.UploadedFile{
		ContentType: mimeStr,
		Name:        projection.Filename,
		Path:        relativePath,
		Ref:         refPtr,
		Size:        size,
	})

	slog.Info(
		"rest: upload: file stored in workspace library",
		"workspace_id", ru.workspaceID,
		"filename", projection.Filename,
		"size", size,
		"content_type", mimeStr,
		"media_ref", ref,
		"work_path", workRelPath,
	)
	return restAPIHandleUploadNext
}

// finishLegacyUpload handles a legacy file copy result, registers media, and records the response.
func (ru *restAPIHandleUpload) finishLegacyUpload(sanitized string, destPath string, contentType string, cleanupUploaded func(), written int64, copyErr error) restAPIHandleUploadFlow {
	if copyErr != nil {
		slog.Error("rest: upload: copy failed", "path", destPath, "error", copyErr)
		if rmErr := os.Remove(destPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			slog.Warn("rest: upload: remove partial file failed", "path", destPath, "error", rmErr)
		}
		cleanupUploaded()
		jsonErr(ru.w, http.StatusInternalServerError, fmt.Sprintf("file write failed: %v", copyErr))
		return restAPIHandleUploadReturn
	}

	if written > maxUploadFileSize {
		if rmErr := os.Remove(destPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			slog.Warn("rest: upload: remove oversized file failed", "path", destPath, "error", rmErr)
		}
		cleanupUploaded()
		jsonErr(ru.w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file %q exceeds 100 MB limit", sanitized))
		return restAPIHandleUploadReturn
	}

	// Relative path for the response — callers use this to construct the
	// /api/v1/uploads/{session_id}/{filename} URL.
	relativePath := filepath.Join("uploads", ru.sessionID, sanitized)

	// #254: register the uploaded file in the media store so it gets a
	// media:// ref. The SPA echoes this ref back in the message frame's
	// "media" array; the agent loop then threads it into the LLM content
	// array as a multimodal content block so the agent can see the file.
	// CleanupPolicyForgetOnly: the uploads dir is operator-visible data —
	// the media store must never auto-delete the file. Registration failure
	// is non-fatal: the file is still downloadable via path, the agent just
	// won't see it inline.
	// #254 stale media store fix: always fetch the current store via the
	// agent loop so uploads survive a restartServices store swap. Fall back
	// to a.mediaStore only when the agent loop is not yet wired (e.g. tests
	// that construct a restAPI with a direct mediaStore but no agentLoop).
	var ref string
	store := ru.a.agentLoop.GetMediaStore()
	if store == nil {
		store = ru.a.mediaStore
	}
	if store != nil {
		var storeErr error
		ref, storeErr = store.Store(destPath, media.MediaMeta{
			Filename:      sanitized,
			ContentType:   contentType,
			Source:        "upload:webchat",
			CleanupPolicy: media.CleanupPolicyForgetOnly,
		}, "upload:"+ru.sessionID)
		if storeErr != nil {
			slog.Warn("rest: upload: media store registration failed",
				"path", destPath, "error", storeErr)
			ref = ""
		}
	}

	slog.Info(
		"rest: upload: file stored",
		"session_id", ru.sessionID,
		"filename", sanitized,
		"size", written,
		"content_type", contentType,
		"media_ref", ref,
	)

	var refPtr *string
	if ref != "" {
		refCopy := ref
		refPtr = &refCopy
	}
	ru.resp.Files = append(ru.resp.Files, gen.UploadedFile{
		ContentType: contentType,
		Name:        sanitized,
		Path:        relativePath,
		Ref:         refPtr,
		Size:        written,
	})
	return restAPIHandleUploadNext
}

// cleanupWorkspaceUploads removes previously uploaded workspace library files
// from this batch when a later file in the same request fails. The failing
// file's own cleanup is handled transactionally by library.Upload; this only
// removes files that already succeeded. Best-effort — errors are logged.
// stageWorkspaceUploadCopy performs the D-1 (library-spec) dual-write: it
// reads back the just-uploaded file's bytes from the workspace media
// library (lib.Read, which sha256-verifies on read) and writes them a
// SECOND time into the SAME workspace's work/.library/ directory — a real,
// named file inside the os.Root every agent file tool is confined to
// (ADR-046), unlike workspaces/<id>/media/ which is a sibling those tools
// cannot reach by construction. De-duplicates a filename collision with a
// human-readable " (N)" numeric suffix, mirroring the legacy session-scoped
// upload path's own collision handling further down in HandleUpload.
//
// Returns the workspace-relative announced path (".library/<name>",
// agent.LibraryDirName()-prefixed) on success. On any failure, the
// caller MUST treat the whole upload as failed (see HandleUpload's use)
// rather than leaving a media-library entry the agent still cannot read —
// stageWorkspaceUploadCopy itself removes any partially-written destination
// file before returning an error, so the caller does not need to.
func (a *restAPI) stageWorkspaceUploadCopy(
	workspaceID, mediaID, filename string,
	lib *library.Library,
) (string, error) {
	sanitized, err := agent.SanitizeUploadFilename(filename)
	if err != nil {
		return "", fmt.Errorf("sanitize filename: %w", err)
	}
	workDir, err := wkspace.SafeWorkDir(a.homePath, workspaceID)
	if err != nil {
		return "", fmt.Errorf("resolve workspace work dir: %w", err)
	}
	libraryDir := filepath.Join(workDir, agent.LibraryDirName())
	if mkErr := os.MkdirAll(libraryDir, 0o700); mkErr != nil {
		return "", fmt.Errorf("create workspace library dir: %w", mkErr)
	}

	// Read back the FULL, sha256-verified bytes rather than tee-ing the
	// original multipart reader — the multipart part is already fully
	// consumed by lib.Upload above by the time this is called, and
	// re-reading through the library's own integrity-checked Read keeps this
	// function's contract simple (one clear source of truth for "what did we
	// actually store") at the cost of one extra full read, bounded by the
	// same 100 MB library.MaxFileSize every upload is already capped at.
	data, _, err := lib.Read(mediaID)
	if err != nil {
		return "", fmt.Errorf("read back uploaded bytes: %w", err)
	}

	ext := filepath.Ext(sanitized)
	base := strings.TrimSuffix(sanitized, ext)
	const maxDedupAttempts = 1000

	destName := sanitized
	var destFile *os.File
	var destPath string
	for attempt := 1; ; attempt++ {
		destPath = filepath.Join(libraryDir, destName)
		f, openErr := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if openErr == nil {
			destFile = f
			break
		}
		if !errors.Is(openErr, os.ErrExist) {
			return "", fmt.Errorf("create workspace library file: %w", openErr)
		}
		if attempt > maxDedupAttempts {
			return "", fmt.Errorf("too many filename collisions for %q in workspace library", sanitized)
		}
		destName = fmt.Sprintf("%s (%d)%s", base, attempt, ext)
	}

	// keepFile flips to true only once the write AND close both succeed —
	// any earlier return leaves it false, so the deferred cleanup below
	// removes the just-created (possibly empty or partially-written) file
	// rather than stranding it.
	keepFile := false
	defer func() {
		if !keepFile {
			if rmErr := os.Remove(destPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				logger.WarnCF("rest", "upload: cleanup partial workspace library file failed",
					map[string]any{"path": destPath, "error": rmErr.Error()})
			}
		}
	}()

	if _, writeErr := destFile.Write(data); writeErr != nil {
		destFile.Close()
		return "", fmt.Errorf("write workspace library file: %w", writeErr)
	}
	if closeErr := destFile.Close(); closeErr != nil {
		return "", fmt.Errorf("close workspace library file: %w", closeErr)
	}
	keepFile = true

	return agent.LibraryDirName() + "/" + destName, nil
}

func (a *restAPI) cleanupWorkspaceUploads(resp *gen.UploadFilesResponse, lib *library.Library) {
	if lib == nil {
		return
	}
	for _, prev := range resp.Files {
		if prev.Ref == nil || !strings.HasPrefix(*prev.Ref, "media://workspace/") {
			continue
		}
		_, mediaID, ok := media.ParseWorkspaceRef(*prev.Ref)
		if !ok {
			continue
		}
		if _, err := lib.Delete(mediaID); err != nil {
			slog.Warn("rest: upload: cleanup workspace file failed",
				"media_id", mediaID, "error", err)
		}
	}
}

// HandleServeUpload serves uploaded files for display in chat.
// GET /api/v1/uploads/{session_id}/{filename}
// Registered with withAuth (issue #716): the route requires the same login as
// every other API route — an unauthenticated fetch is a 401 — while a signed-in
// session still loads image URLs through the omnipus-session cookie the browser
// auto-attaches on same-origin requests.
func (a *restAPI) HandleServeUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Extract session_id and filename from the URL path.
	// Pattern: /api/v1/uploads/{session_id}/{filename}
	trimmed := strings.TrimPrefix(r.URL.Path, "/api/v1/uploads/")
	trimmed = strings.TrimPrefix(trimmed, "/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		jsonErr(w, http.StatusBadRequest, "path must be /api/v1/uploads/{session_id}/{filename}")
		return
	}

	sessionID := parts[0]
	filename := parts[1]

	if err := validateEntityID(sessionID); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid session_id")
		return
	}

	// Sanitize filename — reject anything with path separators or "..".
	if strings.ContainsAny(filename, "/\\") || strings.Contains(filename, "..") || strings.ContainsRune(filename, 0) {
		jsonErr(w, http.StatusBadRequest, "invalid filename")
		return
	}

	filePath := filepath.Join(a.homePath, "uploads", sessionID, filename)

	// Defense-in-depth: resolve symlinks and confirm the real path is still inside
	// the uploads directory. EvalSymlinks also returns an error if the file does
	// not exist, which naturally produces the 404 case below.
	uploadsRoot, _ := filepath.EvalSymlinks(filepath.Join(a.homePath, "uploads"))
	resolved, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		jsonErr(w, http.StatusNotFound, "file not found")
		return
	}
	if !strings.HasPrefix(resolved, uploadsRoot+string(filepath.Separator)) {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	// ADR-067 FR-008b/FR-015: this route used to set a bare
	// "Content-Disposition: inline" and hand the file to http.ServeFile, which
	// types it from the host MIME registry and then sniffs the bytes. An
	// uploaded .html was therefore served as a real document on the gateway
	// origin with no policy. serveLibraryPath decides the type from the
	// extension, attaches anything off the §10.4 allow-list, and carries the
	// §10.3 isolation policy on whatever it does serve inline.
	if err := serveLibraryPath(w, r, resolved, filename); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errLibraryBytesNotAFile) {
			jsonErr(w, http.StatusNotFound, "file not found")
			return
		}
		slog.Error("rest: uploads: serve failed", "session_id", sessionID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not read file")
		return
	}
}

// --- Media ---

// HandleMedia serves a legacy global media file by its ref ID extracted from
// the URL path (e.g. /api/v1/media/abc123 resolves "media://abc123").
func (a *restAPI) HandleMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.setCORSHeaders(w, r)

	refID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/media/"), "/")
	if refID == "" || strings.ContainsAny(refID, "/\\") || strings.Contains(refID, "..") {
		jsonErr(w, http.StatusBadRequest, "invalid media ref")
		return
	}

	a.serveMedia(w, r, "media://"+refID, media.ResolveOpts{}, refID)
}

// HandleMediaByRef serves workspace-library media through the split path shape
// /api/v1/media/workspace/{workspace}/{id}; the split keeps each path segment
// independently validated while preserving the opaque media ref for resolution.
func (a *restAPI) HandleMediaByRef(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.setCORSHeaders(w, r)

	const prefix = "/api/v1/media/workspace/"
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) != 2 || validateEntityID(parts[0]) != nil || validateEntityID(parts[1]) != nil {
		jsonErr(w, http.StatusBadRequest, "invalid media ref")
		return
	}

	workspaceID, mediaID := parts[0], parts[1]
	ref := media.WorkspaceRefPrefix + workspaceID + "/" + mediaID
	a.serveMedia(w, r, ref, media.WithCallerWorkspace(workspaceID), ref)
}

func (a *restAPI) serveMedia(
	w http.ResponseWriter,
	r *http.Request,
	ref string,
	opts media.ResolveOpts,
	logRef string,
) {
	store := a.agentLoop.GetMediaStore()
	if store == nil {
		store = a.mediaStore
	}
	if store == nil {
		jsonErr(w, http.StatusServiceUnavailable, "media store not available")
		return
	}

	localPath, meta, err := store.ResolveWithMetaOpts(ref, opts)
	if err != nil {
		if errors.Is(err, media.ErrCrossWorkspaceRef) || errors.Is(err, library.ErrWorkspaceMismatch) {
			slog.Warn("rest: media ref not found", "ref", logRef, "error", err.Error())
			jsonErr(w, http.StatusForbidden, "media access denied")
			return
		}
		if errors.Is(err, library.ErrEntryStranded) {
			// See rest_workspace_media.go's handleWorkspaceMediaGet/Delete for
			// the identical branch and its full rationale: ErrEntryStranded
			// means the manifest still claims the entry is present while its
			// bytes are actually quarantined at an internal path — a
			// server-side data-integrity failure, not a routine absent ref.
			// Folding it into the catch-all below would report it as 404
			// ("this ref never existed"), which is a lie; 500 with an
			// attributable message keeps this path coherent with the
			// workspace-media handlers' mapping for the same sentinel.
			//
			// library.ErrIntegrityCheckFailed (sha256 mismatch) deliberately
			// gets no analogous branch here: it is only ever returned by
			// Library.Read/ResolveWithWorkspace (the bytes-returning,
			// integrity-checked path), never by ResolvePathWithCaller (the
			// path-only resolver this handler's workspace-ref route reaches
			// through FileMediaStore.resolveWorkspaceRef) or by the legacy
			// registry lookup the non-workspace route uses — so it cannot
			// reach this catch in practice. Should the resolution path ever
			// change to route through the integrity-checked reader, this
			// error deserves the same non-404 treatment as ErrEntryStranded.
			slog.Error("rest: media: entry stranded (manifest/disk diverged)", "ref", logRef, "error", err.Error())
			jsonErr(w, http.StatusInternalServerError, "media entry is in an inconsistent state")
			return
		}
		if errors.Is(err, media.ErrNotFound) || errors.Is(err, library.ErrNotFound) {
			// Both sentinels mean the same thing at two different layers:
			// media.ErrNotFound is FileMediaStore's own "no provider/resolver
			// wired, or the ref is absent from the legacy global registry"
			// (see FileMediaStore.resolveWorkspaceRef/resolveLegacyWithMeta);
			// library.ErrNotFound is the owning workspace library reporting
			// its manifest has no such id (Library.ResolvePathWithCaller).
			// Either way this is a genuine, routine absent ref — 404 is
			// correct and matches the workspace-media handlers' own mapping
			// for the same library.ErrNotFound sentinel
			// (rest_workspace_media.go's handleWorkspaceMediaGet/Delete).
			slog.Warn("rest: media ref not found", "ref", logRef, "error", err.Error())
			jsonErr(w, http.StatusNotFound, "media not found")
			return
		}
		// Anything else is a genuine resolution FAILURE, not a routine
		// absent ref — most notably FileMediaStore.resolveWorkspaceRef's
		// "workspace library %q unavailable: %w" when a wired provider
		// itself errors (disk/library-open failure). Collapsing that into
		// the same 404 the block above returns would report "this media
		// never existed" for what is actually "the server could not check".
		// media.ErrNotFound is deliberately NEVER used to wrap that error —
		// see its doc comment — so this catch-all is unreachable for a
		// routine absent ref and only fires on a real backend fault.
		slog.Error("rest: media: resolve failed", "ref", logRef, "error", err.Error())
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// ADR-067 FR-008b, and this is the round-4 LIVE exposure, not a preview
	// feature: this route WAS registered withOptionalAuth and served
	// workspace-library bytes with a bare "inline" disposition, the media
	// entry's own recorded ContentType, and NO policy — so an .html entry
	// (pkg/library/entries.go types it text/html) rendered as a real document
	// on the gateway origin, same-origin with the session cookie. (The route is
	// registered with withAuth since issue #716; the header rules below are
	// unchanged.)
	//
	// meta.ContentType is deliberately no longer consulted. It is the type an
	// UPSTREAM claimed — a channel, an MCP server, an upload form — and
	// FR-015b makes the compiled-in extension table the only source. The
	// storage path is only a fallback for a legacy registry entry with no
	// recorded filename: a workspace-library entry lives at <libdir>/<mediaID>
	// with no extension, so it can never supply one.
	displayName := meta.Filename
	if libraryExtOf(displayName) == "" {
		displayName = filepath.Base(localPath)
	}
	if err := serveLibraryPath(w, r, localPath, displayName); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errLibraryBytesNotAFile) {
			slog.Warn("rest: media: resolved path is not a readable file", "ref", logRef, "error", err.Error())
			jsonErr(w, http.StatusNotFound, "media not found")
			return
		}
		slog.Error("rest: media: serve failed", "ref", logRef, "error", err.Error())
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
}
