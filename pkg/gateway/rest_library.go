// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
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
		http.NotFound(w, r)
		return
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

// checkCreateName applies the DESTINATION root's name-shape rules to rel and
// writes the 400 itself when they refuse, returning ok=false (ADR-067
// FR-0001a). Every create/rename handler calls it on its destination path,
// after the root is open.
//
// Why a helper rather than the method inline five times: FR-0001a names
// exactly five handlers that create or rename — content-put, upload, mkdir,
// rename, transfer — and observes that "the one that forgot would silently
// accept what the other four refuse". A one-line call is the smallest thing a
// sixth handler's author can copy correctly.
//
// Two properties this signature is deliberately shaped for:
//
//   - root is the DESTINATION's root, not the caller's convenient one. For a
//     cross-workspace move or copy that is toRoot, because population
//     (workspace storage vs. mount) is a property of where the file lands.
//   - The error goes through mapLibraryErr, not a bespoke jsonErr. Root's
//     ValidateCreateName wraps ErrInvalidPath precisely so the existing 400
//     mapping covers it with no new branch; routing it anywhere else would
//     re-invent that mapping and let the two drift.
//
// Nothing on the READ path may call this. Listing, opening, downloading and
// deleting an existing file are reads of the operator's disk, and FR-0001
// removes name shape from them entirely: a file already on disk is, by
// construction, inside its own filesystem's limits, and Omnipus did not name
// it.
func checkCreateName(w http.ResponseWriter, root *library.Root, rel, op, workspaceID string) bool {
	if err := root.ValidateCreateName(rel); err != nil {
		mapLibraryErr(w, op, workspaceID, err)
		return false
	}
	return true
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
	jsonOK(w, entries)
}

func (a *restAPI) handleLibraryEntryDelete(w http.ResponseWriter, r *http.Request, workspaceID string) {
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

	if err := root.Delete(rel); err != nil {
		mapLibraryErr(w, "delete entry", workspaceID, err)
		return
	}
	// ADR-067 FR-003d: the granted path is gone, so any preview token naming it
	// — or naming something beneath it — must stop working now rather than in
	// fifteen minutes. InvalidatePath covers the beneath-it half: deleting the
	// directory "reports" also kills a bundle token scoped to "reports/q3".
	a.revokePreviewTokensForPath(workspaceID, rel)
	a.logLibraryAudit(r, "library.delete", workspaceID, map[string]any{"path": rel})
	w.WriteHeader(http.StatusNoContent)
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
	jsonOK(w, resp)
}

func (a *restAPI) handleLibraryContentPut(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.LibraryContentRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryContentRequest", &req, validateEnabled) {
		return
	}
	if len(req.Content) > library.MaxContentBytes {
		jsonErr(w, http.StatusBadRequest, "content exceeds the 10 MB limit")
		return
	}
	rel, err := library.CleanRelPath(req.Path)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	if !checkCreateName(w, root, rel, "put content", workspaceID) {
		return
	}

	fi, err := root.WriteContent(rel, []byte(req.Content))
	if err != nil {
		mapLibraryErr(w, "put content", workspaceID, err)
		return
	}
	jsonOK(w, library.EntryFromInfo(rel, fi))
}

// maxLibraryBinaryContentBytes is the decoded-byte cap for PUT
// .../content-binary (LibraryBinaryContentRequest.content_base64) — 25 MB,
// matching the size a filled PDF or other binary attachment realistically
// needs and the cap the schema documents. It intentionally does NOT reuse
// library.MaxContentBytes (10 MB): that constant also gates GET .../content's
// inline-render threshold for TEXT files, and binary attachments are never
// rendered inline through that path.
const maxLibraryBinaryContentBytes = 25 * 1024 * 1024

// maxLibraryBinaryContentBodyBytes bounds the raw JSON request body read for
// PUT .../content-binary. It is NOT decodeAndValidate's usual 1 MB cap:
// standard base64 inflates the payload to ~4/3 of the decoded size, so a
// legal 25 MB attachment needs room for its ~33.3 MB encoded form plus the
// JSON envelope and the "path" field. The +4096 is slack for that envelope,
// not part of the size budget being enforced.
const maxLibraryBinaryContentBodyBytes = (maxLibraryBinaryContentBytes/3+1)*4 + 4096

// handleLibraryContentBinaryPut is the binary-capable sibling of
// handleLibraryContentPut: PUT .../content carries UTF-8 text as a JSON
// string, which corrupts arbitrary bytes, so this route instead carries the
// content as standard base64 (LibraryBinaryContentRequest.content_base64) and
// writes the decoded bytes verbatim. It cannot go through decodeAndValidate
// unmodified because that helper hard-caps the body read at 1 MB regardless
// of schema — far too small for a base64-encoded PDF — so this handler reads
// and validates the body itself, at a size ceiling sized for the 25 MB
// decoded cap, before decoding into the generated type.
func (a *restAPI) handleLibraryContentBinaryPut(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	lr := io.LimitReader(r.Body, maxLibraryBinaryContentBodyBytes+1)
	raw, err := io.ReadAll(lr)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if int64(len(raw)) > maxLibraryBinaryContentBodyBytes {
		jsonErr(w, http.StatusBadRequest, "content exceeds the 25 MB limit")
		return
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		jsonErr(w, http.StatusBadRequest, "request body is required")
		return
	}

	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if validateEnabled {
		if errMsg, serverErr := validateBodyAgainstSchema("LibraryBinaryContentRequest", raw); errMsg != "" {
			if serverErr {
				jsonErr(w, http.StatusInternalServerError, "inbound schema unavailable")
			} else {
				jsonErr(w, http.StatusBadRequest,
					fmt.Sprintf("request body does not match schema LibraryBinaryContentRequest: %s", errMsg))
			}
			return
		}
	}

	var req gen.LibraryBinaryContentRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	rel, err := library.CleanRelPath(req.Path)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	decoded, err := base64.StdEncoding.DecodeString(req.ContentBase64)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "content_base64 is not valid base64")
		return
	}
	if len(decoded) > maxLibraryBinaryContentBytes {
		jsonErr(w, http.StatusBadRequest, "content exceeds the 25 MB limit")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	if !checkCreateName(w, root, rel, "put content", workspaceID) {
		return
	}

	fi, err := root.WriteContent(rel, decoded)
	if err != nil {
		mapLibraryErr(w, "put content", workspaceID, err)
		return
	}
	jsonOK(w, library.EntryFromInfo(rel, fi))
}

// --- POST /library/{workspace_id}/upload ---

func (a *restAPI) handleLibraryUpload(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}
	targetDir, err := library.CleanRelPath(r.URL.Query().Get("path"))
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	// Upload does not auto-create nested directories beyond the work-tree
	// root itself (OpenRoot already guarantees that one exists) — matches
	// putLibraryContent's "parent directory must already exist" contract.
	if targetDir != "" {
		if _, statErr := root.StatDir(targetDir); statErr != nil {
			mapLibraryErr(w, "upload", workspaceID, statErr)
			return
		}
	}

	reader, err := r.MultipartReader()
	if err != nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid multipart request: %v", err))
		return
	}

	var resp gen.LibraryUploadResponse
	var createdRelPaths []string
	rollback := func() {
		for _, rel := range createdRelPaths {
			if rmErr := root.Delete(rel); rmErr != nil {
				logger.WarnCF("rest", "library: upload rollback failed",
					map[string]any{"workspace_id": workspaceID, "path": rel, "error": rmErr.Error()})
			}
		}
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			rollback()
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("multipart read error: %v", err))
			return
		}
		fileName := part.FileName()
		if fileName == "" {
			// Non-file field — discard.
			if _, discardErr := io.Copy(io.Discard, part); discardErr != nil {
				logger.WarnCF("rest", "library: upload: discard field failed",
					map[string]any{"workspace_id": workspaceID, "error": discardErr.Error()})
			}
			part.Close()
			continue
		}

		sanitized, sanErr := agent.SanitizeUploadFilename(path.Base(fileName))
		if sanErr != nil {
			part.Close()
			rollback()
			jsonErr(w, http.StatusBadRequest, fmt.Sprintf("invalid filename: %v", sanErr))
			return
		}
		destRel := sanitized
		if targetDir != "" {
			destRel = targetDir + "/" + sanitized
		}

		// The full destination, not just the leaf. SanitizeUploadFilename above
		// already judged the leaf, so on a POSIX build this adds nothing a
		// caller can observe — every POSIX shape rule is a per-component byte
		// budget the host filesystem enforces anyway. What it adds on a Windows
		// build is the two rules the leaf check cannot see: targetDir's own
		// segments, and the whole-path MAX_PATH budget that a short filename in
		// a deep directory blows without any single component coming close.
		// FR-0001a names upload as one of the five for that reason.
		if nameErr := root.ValidateCreateName(destRel); nameErr != nil {
			part.Close()
			rollback()
			mapLibraryErr(w, "upload", workspaceID, nameErr)
			return
		}

		finalRel, f, createErr := root.CreateUnique(destRel)
		if createErr != nil {
			part.Close()
			rollback()
			logger.ErrorCF("rest", "library: upload: create file failed",
				map[string]any{"workspace_id": workspaceID, "path": destRel, "error": createErr.Error()})
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not create file: %v", createErr))
			return
		}

		limited := io.LimitReader(part, maxUploadFileSize+1)
		written, copyErr := io.Copy(f, limited)
		f.Close()
		part.Close()
		if copyErr != nil {
			if rmErr := root.Delete(finalRel); rmErr != nil {
				logger.WarnCF("rest", "library: upload: remove partial file failed",
					map[string]any{"workspace_id": workspaceID, "path": finalRel, "error": rmErr.Error()})
			}
			rollback()
			jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("file write failed: %v", copyErr))
			return
		}
		if written > maxUploadFileSize {
			if rmErr := root.Delete(finalRel); rmErr != nil {
				logger.WarnCF("rest", "library: upload: remove oversized file failed",
					map[string]any{"workspace_id": workspaceID, "path": finalRel, "error": rmErr.Error()})
			}
			rollback()
			jsonErr(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("file %q exceeds 100 MB limit", sanitized))
			return
		}

		createdRelPaths = append(createdRelPaths, finalRel)
		fi, statErr := root.StatFile(finalRel)
		if statErr != nil {
			rollback()
			logger.ErrorCF("rest", "library: upload: stat uploaded file failed",
				map[string]any{"workspace_id": workspaceID, "path": finalRel, "error": statErr.Error()})
			jsonErr(w, http.StatusInternalServerError, "internal server error")
			return
		}
		resp.Entries = append(resp.Entries, library.EntryFromInfo(finalRel, fi))
	}

	if len(resp.Entries) == 0 {
		jsonErr(w, http.StatusBadRequest, "no files found in upload")
		return
	}
	a.logLibraryAudit(r, "library.upload", workspaceID, map[string]any{
		"path": targetDir, "count": len(resp.Entries),
	})
	jsonCreated(w, resp)
}

// --- POST /library/{workspace_id}/mkdir ---

// handleLibraryMkdir creates a directory in workspaceID's work tree,
// creating any missing intermediate directories along the way (UAT Issue 4:
// previously there was no way for a caller to create a folder at all, and a
// clean, non-malicious nested Move/Copy destination like "subfolder/test.txt"
// had no path to success because those operations deliberately do not
// auto-create missing parents — see requireParentDir's doc). Idempotent:
// creating a directory that already exists returns 200 with its existing
// entry rather than an error; 409 if a regular FILE already occupies path.
func (a *restAPI) handleLibraryMkdir(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.LibraryMkdirRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryMkdirRequest", &req, validateEnabled) {
		return
	}
	rel, err := library.CleanRelPath(req.Path)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	if !checkCreateName(w, root, rel, "mkdir", workspaceID) {
		return
	}

	fi, created, err := root.Mkdir(rel)
	if err != nil {
		mapLibraryErr(w, "mkdir", workspaceID, err)
		return
	}
	entry := library.EntryFromInfo(rel, fi)
	a.logLibraryAudit(r, "library.mkdir", workspaceID, map[string]any{"path": rel, "created": created})
	if created {
		jsonCreated(w, entry)
	} else {
		jsonOK(w, entry)
	}
}

// handleLibraryCreateVault creates a new Omnipus knowledge base ("vault") at
// parent_rel_path/name inside workspaceID's work tree.
//
// Unlike handleLibraryMkdir this is NOT idempotent and does NOT auto-create
// missing intermediate directories: it behaves like content-put/rename
// (requires the immediate parent to already exist, 404 otherwise) and rejects
// (409) ANY entry — file, plain directory, or existing vault — already at
// the target path, because adopting an existing folder into a vault or
// silently reusing one is never this endpoint's job (CreateVaultRequest's
// description).
//
// SEEDING DECISION: after knowledge.CreateInWorkspace writes the
// .omnipus-vault/ marker, this handler additionally creates empty
// records/ and views/ control-plane directories (records.SchemaDir,
// records.ViewsDir) so knowledge_configure has somewhere to write into
// immediately. It deliberately does NOT seed a starter saved view. A view
// is validated against the vault's schema set and must name an existing
// record TYPE (pkg/records/view.go's RejectViewMissingType /
// ValidateViewAgainstSchemas) — a brand-new vault has zero record types, so
// there is no type this handler could reference without inventing a schema
// shape, which view.go's own doc comment reserves to knowledge_configure's
// write path alone ("THERE IS NO WRITER [here], on purpose"). Empty
// records/ + views/ plus the marker is a valid, detectable, immediately
// usable vault; a fabricated view would not be.
func (a *restAPI) handleLibraryCreateVault(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.CreateVaultRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "CreateVaultRequest", &req, validateEnabled) {
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		jsonErr(w, http.StatusBadRequest, "invalid vault name")
		return
	}

	parentRel := ""
	if req.ParentRelPath != nil {
		cleaned, err := library.CleanRelPath(*req.ParentRelPath)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid parent_rel_path")
			return
		}
		parentRel = cleaned
	}
	joined := name
	if parentRel != "" {
		joined = parentRel + "/" + name
	}
	rel, err := library.CleanRelPath(joined)
	if err != nil || rel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	if !checkCreateName(w, root, rel, "create vault", workspaceID) {
		return
	}
	if parentRel != "" {
		if _, err := root.StatDir(parentRel); err != nil {
			mapLibraryErr(w, "create vault", workspaceID, err)
			return
		}
	}

	// ValidateCreateName only judges name SHAPE (FR-0001a), not collision —
	// check for an existing entry at rel ourselves so this route can refuse
	// with 409 rather than silently adopting or converting whatever is
	// already there.
	switch _, statErr := root.StatDir(rel); {
	case statErr == nil, errors.Is(statErr, library.ErrNotDir):
		jsonErr(w, http.StatusConflict, "an entry already exists at that path")
		return
	case errors.Is(statErr, library.ErrNotFound):
		// Expected: nothing there yet.
	default:
		mapLibraryErr(w, "create vault", workspaceID, statErr)
		return
	}

	collection, err := knowledge.CreateInWorkspace(a.homePath, workspaceID, rel, knowledge.Marker{DisplayName: name})
	if err != nil {
		switch {
		case errors.Is(err, knowledge.ErrAlreadyKnowledgeBase):
			jsonErr(w, http.StatusConflict, "an entry already exists at that path")
		case errors.Is(err, knowledge.ErrMarkerInvalid):
			jsonErr(w, http.StatusBadRequest, "invalid vault name")
		case errors.Is(err, knowledge.ErrOutsideCollection):
			jsonErr(w, http.StatusForbidden, "path resolves outside the workspace work tree")
		default:
			logger.ErrorCF("rest", "library: create vault failed",
				map[string]any{"workspace_id": workspaceID, "path": rel, "error": err.Error()})
			jsonErr(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	if mkErr := os.MkdirAll(records.SchemaDir(collection.Root()), 0o755); mkErr != nil {
		logger.ErrorCF("rest", "library: create vault: seed records dir failed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "error": mkErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if mkErr := os.MkdirAll(records.ViewsDir(collection.Root()), 0o755); mkErr != nil {
		logger.ErrorCF("rest", "library: create vault: seed views dir failed",
			map[string]any{"workspace_id": workspaceID, "path": rel, "error": mkErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	fi, err := root.StatDir(rel)
	if err != nil {
		mapLibraryErr(w, "create vault", workspaceID, err)
		return
	}
	entry := library.EntryFromInfo(rel, fi)
	a.logLibraryAudit(r, "library.create_vault", workspaceID, map[string]any{"path": rel})
	jsonCreated(w, entry)
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
			b.WriteString(fmt.Sprintf("%%%02X", c))
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

// --- POST /library/{workspace_id}/rename ---

func (a *restAPI) handleLibraryRename(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !workspace.Exists(a.homePath, workspaceID) {
		jsonErr(w, http.StatusNotFound, "workspace not found")
		return
	}

	var req gen.LibraryRenameRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryRenameRequest", &req, validateEnabled) {
		return
	}
	fromRel, err := library.CleanRelPath(req.From)
	if err != nil || fromRel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid from path")
		return
	}
	toRel, err := library.CleanRelPath(req.To)
	if err != nil || toRel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid to path")
		return
	}

	root, ok := a.openLibraryRoot(w, workspaceID, "root")
	if !ok {
		return
	}
	defer root.Close()

	// The DESTINATION only. fromRel names something that already exists —
	// judging its shape would be judging a name Omnipus is not creating, and
	// would make an operator's existing file un-renameable precisely because
	// its current name is the thing they want to fix.
	if !checkCreateName(w, root, toRel, "rename", workspaceID) {
		return
	}

	fi, err := root.Rename(fromRel, toRel)
	if err != nil {
		mapLibraryErr(w, "rename", workspaceID, err)
		return
	}
	// ADR-067 FR-003d: the granted path has MOVED, so every token naming it —
	// or naming something beneath it — must stop working now.
	//
	// The SOURCE only, and that is not an oversight. A token over the
	// DESTINATION cannot exist: minting requires the path to be readable at mint
	// time, and this handler refuses a destination that already exists (409,
	// root.Rename's ErrExists). If that ever stops being true — an overwrite
	// mode, a force flag — the destination becomes a live grant over bytes its
	// holder never saw, and this is the line that has to grow a second call.
	a.revokePreviewTokensForPath(workspaceID, fromRel)
	a.logLibraryAudit(r, "library.rename", workspaceID, map[string]any{"from": fromRel, "to": toRel})
	jsonOK(w, library.EntryFromInfo(toRel, fi))
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

func (a *restAPI) handleLibraryTransfer(w http.ResponseWriter, r *http.Request, mode libraryTransferMode) {
	var req gen.LibraryTransferRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "LibraryTransferRequest", &req, validateEnabled) {
		return
	}

	if err := validateEntityID(req.FromWorkspaceId); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid from_workspace_id")
		return
	}
	if err := validateEntityID(req.ToWorkspaceId); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid to_workspace_id")
		return
	}
	if !workspace.Exists(a.homePath, req.FromWorkspaceId) {
		jsonErr(w, http.StatusNotFound, "from_workspace_id not found")
		return
	}
	if !workspace.Exists(a.homePath, req.ToWorkspaceId) {
		jsonErr(w, http.StatusNotFound, "to_workspace_id not found")
		return
	}

	fromRel, err := library.CleanRelPath(req.FromPath)
	if err != nil || fromRel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid from_path")
		return
	}
	toRel, err := library.CleanRelPath(req.ToPath)
	if err != nil || toRel == "" {
		jsonErr(w, http.StatusBadRequest, "invalid to_path")
		return
	}

	sameWorkspace := req.FromWorkspaceId == req.ToWorkspaceId

	fromRoot, ok := a.openLibraryRoot(w, req.FromWorkspaceId, "from-root")
	if !ok {
		return
	}
	defer fromRoot.Close()

	toRoot := fromRoot
	if !sameWorkspace {
		toRoot, ok = a.openLibraryRoot(w, req.ToWorkspaceId, "to-root")
		if !ok {
			return
		}
		defer toRoot.Close()
	}

	// toRoot, never fromRoot: for a cross-workspace transfer the two are
	// different roots with different mount tables, and the question
	// ValidateCreateName answers — "is Omnipus about to create this name in
	// storage it owns?" — is a property of where the file LANDS. Asking
	// fromRoot would consult the wrong workspace's mounts and, for a copy out
	// of a mount into workspace storage, would skip the check entirely.
	if !checkCreateName(w, toRoot, toRel, string(mode), req.ToWorkspaceId) {
		return
	}

	var fi os.FileInfo
	var opErr error
	switch mode {
	case transferModeMove:
		fi, opErr = library.MoveInto(fromRoot, toRoot, fromRel, toRel)
	case transferModeCopy:
		fi, opErr = library.CopyInto(fromRoot, toRoot, fromRel, toRel)
	}
	if opErr != nil {
		mapLibraryErr(w, string(mode), req.FromWorkspaceId, opErr)
		return
	}

	// ADR-067 FR-003d. A MOVE vacates from_path, so every token over it dies —
	// in the SOURCE workspace, which for a cross-workspace transfer is not the
	// one the entry landed in. A COPY destroys nothing and moves nothing, so it
	// is not one of FR-003d's events and revokes nothing; the destination cannot
	// hold a live grant either, because both modes refuse an existing
	// destination (409) and a token can only be minted over a path that exists.
	if mode == transferModeMove {
		a.revokePreviewTokensForPath(req.FromWorkspaceId, fromRel)
	}

	entry := library.EntryFromInfo(toRel, fi)
	a.logLibraryAudit(r, "library."+string(mode), req.FromWorkspaceId, map[string]any{
		"from_workspace_id": req.FromWorkspaceId, "from_path": fromRel,
		"to_workspace_id": req.ToWorkspaceId, "to_path": toRel,
	})
	if mode == transferModeCopy {
		jsonCreated(w, entry)
	} else {
		jsonOK(w, entry)
	}
}

// logLibraryAudit emits a best-effort audit event for a mutating Library
// operation. Audit write failures are logged, never surfaced to the HTTP
// caller — an audit gap must not block an otherwise-successful operation,
// matching the convention rest_workspace_media.go's logMediaDeleteAudit and
// rest_workspaces.go's workspace.create/update events already establish.
func (a *restAPI) logLibraryAudit(r *http.Request, event, workspaceID string, details map[string]any) {
	if a.auditor == nil {
		return
	}
	details["actor"] = a.callerIdentity(r).Username
	details["workspace_id"] = workspaceID
	if err := a.auditor.Log(&audit.Entry{
		Event:    event,
		Decision: audit.DecisionAllow,
		Details:  details,
	}); err != nil {
		logger.WarnCF("rest", "library: audit write failed",
			map[string]any{"event": event, "workspace_id": workspaceID, "error": err.Error()})
	}
}
