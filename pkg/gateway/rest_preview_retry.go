// rest_preview_retry.go: POST /api/v1/sessions/{session_id}/previews/{tool_call_id}/retry
// (office-preview spec §5.2, FR-038; contract operationId retrySessionPreview;
// architect ruling 2026-11-01: the source is authorized through the session
// workspace's Library).

package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

const (
	previewRetryPrefix = "previews/"
	previewRetrySuffix = "/retry"
	previewToolName    = "preview_document"
	previewWebChannel  = "webchat"

	previewRetryCodeNotInLibrary = "preview_source_not_in_library"
	previewRetryCodeChanged      = "preview_source_changed"
	previewRetryCodeUnavailable  = "preview_renderer_unavailable"

	previewRetryMsgNotInLibrary = "the preview's source is not a file of this session's workspace Library"
	previewRetryMsgChanged      = "the source file changed since the preview"
	previewRetryMsgUnavailable  = "the PDF renderer is not available"
)

// previewRetryToolCallID returns the tool call id of a
// "previews/{tool_call_id}/retry" sub-path, and whether subPath names the
// retry sub-resource at all.
func previewRetryToolCallID(subPath string) (string, bool) {
	if !strings.HasPrefix(subPath, previewRetryPrefix) || !strings.HasSuffix(subPath, previewRetrySuffix) {
		return "", false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(subPath, previewRetryPrefix), previewRetrySuffix)
	return id, true
}

// recordedPreview is what a preview_document call recorded: the file the
// agent named, and the version and pages of its preview record.
type recordedPreview struct {
	file          string
	sourceName    string
	sourceVersion string
	first, last   int
}

// previewRetrySource is a Retry's authorized source: the bytes read once
// through the session workspace's Library, which the version was checked
// on and which are rendered (no second open by path).
type previewRetrySource struct {
	rec      recordedPreview
	ext      string
	data     []byte
	hostPath string
	policy   fspolicy.FSPolicy
}

// handlePreviewRetry serves the retry route. Order: session (404, exactly as
// getSession: no access is indistinguishable from unknown), preview (404),
// file type (403), renderer (503), admission (AD-6: before any source read),
// source through the Library (403 / 409); then 202, the render runs in the
// background under that admission, and its result reaches the person as a
// new MediaFrame with the same tool_call_id; a successful one also settles
// the original call's chat record. Nothing is sent to a model.
func (a *restAPI) handlePreviewRetry(w http.ResponseWriter, r *http.Request, sessionID, toolCallID string) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if toolCallID == "" || len(toolCallID) > 255 || strings.Contains(toolCallID, "/") {
		jsonErr(w, http.StatusNotFound, "preview not found")
		return
	}
	meta, ok := a.previewRetrySession(w, r, sessionID)
	if !ok {
		return
	}
	store := a.resolveSessionStore(sessionID)
	entries, err := store.ReadTranscript(sessionID)
	if err != nil {
		jsonSessionStorageError(w, "read", sessionID, err)
		return
	}
	call, found := findPreviewToolCall(entries, toolCallID)
	if !found {
		jsonErr(w, http.StatusNotFound, "preview not found")
		return
	}
	rec, ok := previewRecordOf(call)
	if !ok {
		jsonErr(w, http.StatusNotFound, "preview not found")
		return
	}
	ext := strings.ToLower(filepath.Ext(rec.file))
	loc, status, code := a.locatePreviewRetrySource(meta.WorkspaceID, rec, ext)
	if status != 0 {
		writePreviewRetryRefusal(w, sessionID, toolCallID, status, code)
		return
	}
	defer loc.close()
	svc := preview.Shared()
	if svc == nil {
		jsonErrCode(w, http.StatusServiceUnavailable, previewRetryMsgUnavailable, previewRetryCodeUnavailable)
		return
	}
	// Admission before the source is read (AD-6, T-39), as the tool takes it:
	// the 120 s call deadline covers the wait for a free slot, and 202 means
	// admitted and started.
	deadline := time.Now().Add(preview.CallTimeout)
	admitCtx, cancelAdmit := context.WithDeadline(r.Context(), deadline)
	adm, err := svc.Admit(admitCtx, preview.IsOffice(ext))
	cancelAdmit()
	if err != nil {
		slog.Warn("rest: preview retry not admitted", "session_id", sessionID, "tool_call_id", toolCallID, "error", err)
		jsonErr(w, http.StatusInternalServerError, "the preview retry could not start: "+err.Error())
		return
	}
	src, status, code := loc.read(rec, ext)
	if status == 0 && svc.ComponentStatus().Renderer != "ready" {
		status, code = http.StatusServiceUnavailable, previewRetryCodeUnavailable
	}
	if status != 0 {
		adm.Release()
		writePreviewRetryRefusal(w, sessionID, toolCallID, status, code)
		return
	}
	go a.runPreviewRetry(svc, adm, deadline, sessionID, toolCallID, src)
	writeJSON(w, http.StatusAccepted, generated.PreviewRetryResponse{Status: generated.PreviewRetryResponseStatusStarted})
}

// previewRetrySession resolves the session exactly as getSession does; any
// refusal is a 404 so the route never confirms that a session exists.
func (a *restAPI) previewRetrySession(w http.ResponseWriter, r *http.Request, sessionID string) (*session.UnifiedMeta, bool) {
	mainOK, mainErr := a.resolveMainSessionForRead(sessionID, requestUsername(r))
	if mainErr != nil {
		jsonErr(w, http.StatusInternalServerError, mainErr.Error())
		return nil, false
	}
	if !mainOK {
		jsonErr(w, http.StatusNotFound, "session not found")
		return nil, false
	}
	store := a.resolveSessionStore(sessionID)
	if store == nil {
		jsonErr(w, http.StatusNotFound, "session not found")
		return nil, false
	}
	meta, err := store.GetMeta(sessionID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			jsonErr(w, http.StatusNotFound, "session not found")
			return nil, false
		}
		jsonSessionStorageError(w, "read", sessionID, err)
		return nil, false
	}
	return meta, true
}

func writePreviewRetryRefusal(w http.ResponseWriter, sessionID, toolCallID string, status int, code string) {
	slog.Info("rest: preview retry refused", "session_id", sessionID, "tool_call_id", toolCallID, "status", status, "code", code)
	switch code {
	case previewRetryCodeNotInLibrary:
		jsonErrCode(w, status, previewRetryMsgNotInLibrary, code)
	case previewRetryCodeChanged:
		jsonErrCode(w, status, previewRetryMsgChanged, code)
	case previewRetryCodeUnavailable:
		jsonErrCode(w, status, previewRetryMsgUnavailable, code)
	default:
		jsonErr(w, status, "internal server error")
	}
}

// findPreviewToolCall returns the preview_document call with id in the
// transcript (the call whose MediaFrame group carries that tool_call_id).
func findPreviewToolCall(entries []session.TranscriptEntry, id string) (session.ToolCall, bool) {
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if string(call.ID) == id && call.Tool == previewToolName {
				return call, true
			}
		}
	}
	return session.ToolCall{}, false
}

// previewRecordOf reads the recorded file argument and preview record of a
// preview_document call; false when the call recorded no preview group.
func previewRecordOf(call session.ToolCall) (recordedPreview, bool) {
	file, _ := call.Parameters["file"].(string)
	group, _ := call.Result["preview"].(map[string]any)
	if strings.TrimSpace(file) == "" || group == nil {
		return recordedPreview{}, false
	}
	rec := recordedPreview{file: file}
	rec.sourceName, _ = group["source_name"].(string)
	rec.sourceVersion, _ = group["source_version"].(string)
	rec.first = recordedInt(group["first_page"])
	rec.last = recordedInt(group["last_page"])
	if rec.sourceVersion == "" || rec.first < 1 || rec.last < rec.first {
		return recordedPreview{}, false
	}
	if rec.sourceName == "" {
		rec.sourceName = filepath.Base(file)
	}
	return rec, true
}

// recordedInt reads a page number from a transcript map (JSON numbers decode
// as float64; in-process records hold int).
func recordedInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// previewRetryLocation is a Retry source located in the session workspace's
// Library and opened, but not yet read: the open handle is read once, after
// admission (AD-6), so the checked version and the rendered bytes are the
// same bytes.
type previewRetryLocation struct {
	root     *library.Root
	file     *os.File
	hostPath string
	policy   fspolicy.FSPolicy
}

func (l *previewRetryLocation) close() {
	if l.file != nil {
		_ = l.file.Close()
	}
	_ = l.root.Close()
}

// locatePreviewRetrySource maps the recorded file to a Library path of the
// session's workspace and opens it through the Library root. status is 0
// on success; otherwise 403 (not a Library file, refused by the Library,
// protected, or missing) or 500. Nothing is read here.
func (a *restAPI) locatePreviewRetrySource(workspaceID string, rec recordedPreview, ext string) (*previewRetryLocation, int, string) {
	notInLibrary := func() (*previewRetryLocation, int, string) {
		return nil, http.StatusForbidden, previewRetryCodeNotInLibrary
	}
	if workspaceID == "" || !workspace.Exists(a.homePath, workspaceID) || !previewRetryExt(ext) {
		return notInLibrary()
	}
	workDir, err := workspace.SafeWorkDir(a.homePath, workspaceID)
	if err != nil {
		return notInLibrary()
	}
	mounts, _ := workspace.LoadMounts(a.homePath, workspaceID)
	rel, ok := libraryRelForPreviewSource(workDir, mounts, rec.file)
	if !ok {
		return notInLibrary()
	}
	// The Library's own protected-file rule (the carve-out guard its file
	// search applies): a protected file is refused like a file outside it.
	policy, err := fspolicy.EffectiveFSPolicy(context.Background(), workDir, "", true, a.homePath, "", workspaceID)
	if err != nil {
		slog.Error("rest: preview retry: library policy could not be built", "workspace_id", workspaceID, "error", err)
		return nil, http.StatusInternalServerError, ""
	}
	root, err := library.OpenRoot(a.homePath, workspaceID)
	if err != nil {
		slog.Error("rest: preview retry: open library root failed", "workspace_id", workspaceID, "error", err)
		return nil, http.StatusInternalServerError, ""
	}
	hostPath := root.HostPath(rel)
	if fspolicy.IsCarveOut(hostPath, policy) {
		_ = root.Close()
		return notInLibrary()
	}
	f, _, err := root.OpenFileForDownload(rel)
	if err != nil {
		// Missing, a directory, outside the root or otherwise refused by
		// the Library: never a 409 (ruling Part 1 rule 3).
		_ = root.Close()
		return notInLibrary()
	}
	return &previewRetryLocation{root: root, file: f, hostPath: hostPath, policy: policy}, 0, ""
}

// read reads the opened source once (bounded by the 50 MiB limit) and
// checks its version. status is 0 on success, 409 for a different version,
// or 500 for a read failure.
func (l *previewRetryLocation) read(rec recordedPreview, ext string) (*previewRetrySource, int, string) {
	data, err := io.ReadAll(io.LimitReader(l.file, preview.MaxInputBytes+1))
	if err != nil {
		slog.Error("rest: preview retry: source read failed", "error", err)
		return nil, http.StatusInternalServerError, ""
	}
	if len(data) > preview.MaxInputBytes || sourceVersion(data) != rec.sourceVersion {
		// The recorded preview fit the 50 MiB limit, so a larger file is a
		// changed file too.
		return nil, http.StatusConflict, previewRetryCodeChanged
	}
	return &previewRetrySource{rec: rec, ext: ext, data: data, hostPath: l.hostPath, policy: l.policy}, 0, ""
}

func previewRetryExt(ext string) bool {
	return ext == ".pdf" || preview.IsOffice(ext)
}

// sourceVersion is the preview record's version: the first 12 hex of the
// SHA-256 of the bytes (preview.Service.Preview computes the same).
func sourceVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:12]
}

// libraryRelForPreviewSource maps the recorded file to a Library path: a
// relative path is relative to the work folder; an absolute path must lie
// inside the work folder or a mount's target. Anything else is not a
// Library file. The result is validated by library.CleanRelPath.
func libraryRelForPreviewSource(workDir string, mounts []workspace.Mount, file string) (string, bool) {
	file = strings.TrimSpace(file)
	var rel string
	if !filepath.IsAbs(file) {
		rel = filepath.ToSlash(filepath.Clean(file))
	} else {
		var ok bool
		rel, ok = absToLibraryRel(workDir, mounts, filepath.Clean(file))
		if !ok {
			return "", false
		}
	}
	cleaned, err := library.CleanRelPath(rel)
	if err != nil || cleaned == "" {
		return "", false
	}
	return cleaned, true
}

func absToLibraryRel(workDir string, mounts []workspace.Mount, file string) (string, bool) {
	if rest, ok := withinDir(workDir, file); ok {
		return rest, true
	}
	for _, m := range mounts {
		if m.Name == "" || m.HostPath == "" {
			continue
		}
		if rest, ok := withinDir(m.HostPath, file); ok {
			if rest == "" {
				return "", false
			}
			return path.Join(m.Name, rest), true
		}
	}
	return "", false
}

// withinDir reports file relative to dir (slash-separated) when file lies
// strictly inside dir, comparing both the given and the symlink-resolved
// form of dir (macOS temp folders resolve /var to /private/var).
func withinDir(dir, file string) (string, bool) {
	candidates := []string{filepath.Clean(dir)}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != candidates[0] {
		candidates = append(candidates, resolved)
	}
	for _, d := range candidates {
		rel, err := filepath.Rel(d, file)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.ToSlash(rel), true
	}
	return "", false
}

// runPreviewRetry renders the authorized bytes under the tool's admission,
// limits and writer-exclusion gate and publishes the result to the person
// as a new thumbnail group with the same tool_call_id. A failure publishes
// an unavailable group, so the card comes back with Retry (§5.3).
func (a *restAPI) runPreviewRetry(svc *preview.Service, adm *preview.Admission, deadline time.Time, sessionID, toolCallID string, src *previewRetrySource) {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	ctx = tools.WithTranscriptSessionID(ctx, sessionID)
	thumbs, record, err := a.renderPreviewRetry(ctx, svc, adm, src)
	if err == nil && record.Outcome != tools.PreviewOutcomeUnavailable {
		// Only a successful retry persists (ruling follow-up 2026-11-01): a
		// reload then shows the new group; a failed one leaves the old card.
		a.settlePreviewRetry(sessionID, toolCallID, thumbs, record)
	}
	if err != nil {
		slog.Warn("rest: preview retry failed", "session_id", sessionID, "tool_call_id", toolCallID, "error", err)
		reason := preview.ReasonRendererFailed
		record = generated.MediaPreview{
			SourceName: src.rec.sourceName, SourceVersion: src.rec.sourceVersion,
			FirstPage: src.rec.first, LastPage: src.rec.last,
			Outcome: tools.PreviewOutcomeUnavailable, UnavailableReason: &reason,
		}
		thumbs = nil
	}
	parts := make([]bus.MediaPart, 0, len(thumbs))
	for _, th := range thumbs {
		parts = append(parts, bus.MediaPart{
			Type: "image", Ref: th.Ref, Caption: th.Caption, Filename: th.Filename,
			ContentType: th.ContentType, PageNumber: th.PageNumber,
		})
	}
	msg := bus.OutboundMediaMessage{
		Channel:      previewWebChannel,
		SessionID:    sessionID,
		Parts:        parts,
		ToolCallID:   toolCallID,
		Presentation: previewPresentationPageThumbnails,
		Preview:      &record,
	}
	// Straight to the web chat channel (thumbnails go to no other channel,
	// §4.3): there is no turn whose typing indicator or placeholder the
	// manager's send path would need to clear.
	if sendErr := a.sendPreviewRetryFrame(ctx, msg); sendErr != nil {
		slog.Error("rest: preview retry result not delivered", "session_id", sessionID, "tool_call_id", toolCallID, "error", sendErr)
	}
}

func (a *restAPI) sendPreviewRetryFrame(ctx context.Context, msg bus.OutboundMediaMessage) error {
	cm := a.agentLoop.GetChannelManager()
	if cm == nil {
		return errors.New("no channel manager")
	}
	ch, ok := cm.GetChannel(previewWebChannel)
	if !ok {
		return errors.New("web chat channel not registered")
	}
	sender, ok := ch.(channels.MediaSender)
	if !ok {
		return errors.New("web chat channel cannot send media")
	}
	return sender.SendMedia(ctx, msg)
}

// renderPreviewRetry renders src under adm (taken by the handler before the
// source was read) and releases it.
func (a *restAPI) renderPreviewRetry(ctx context.Context, svc *preview.Service, adm *preview.Admission, src *previewRetrySource) ([]tools.DocumentPreviewThumbnail, generated.MediaPreview, error) {
	defer adm.Release()
	readModel, err := sandbox.KernelPolicyForTurn(src.policy)
	if err != nil {
		return nil, generated.MediaPreview{}, fmt.Errorf("sandbox policy: %w", err)
	}
	res, err := svc.Preview(ctx, adm, preview.Request{
		Name:             src.rec.sourceName,
		Ext:              src.ext,
		Data:             src.data,
		First:            src.rec.first,
		Last:             src.rec.last,
		ReadModel:        readModel,
		CallerWriteRoots: preview.TurnWriteRoots(src.policy),
	})
	if err != nil {
		return nil, generated.MediaPreview{}, err
	}
	thumbs, record := tools.PreviewRetryGroup(ctx, a.mediaStore, a.agentLoop.AuditLogger(), src.rec.sourceName, src.hostPath, res)
	return thumbs, record, nil
}

// settlePreviewRetry persists a successful retry: the original
// preview_document call's chat record is settled (chat view only,
// UnifiedStore.SettleToolCall) to the same call with the new display media
// and preview group, so replay rebuilds the new group. The model view and
// the call's result text are unchanged; no second record with the same
// tool_call_id is written (replay keeps the last occurrence of an id).
func (a *restAPI) settlePreviewRetry(sessionID, toolCallID string, thumbs []tools.DocumentPreviewThumbnail, record generated.MediaPreview) {
	store := a.resolveSessionStore(sessionID)
	if store == nil {
		slog.Error("rest: preview retry not persisted: session store unavailable", "session_id", sessionID, "tool_call_id", toolCallID)
		return
	}
	addr, call, found, err := store.LocateChatToolCall(sessionID, session.ToolCallID(toolCallID))
	if err != nil || !found {
		slog.Error("rest: preview retry not persisted: original call not found", "session_id", sessionID, "tool_call_id", toolCallID, "found", found, "error", err)
		return
	}
	call.Result = previewRetryResult(call.Result, thumbs, record)
	if _, err := store.SettleToolCall(sessionID, addr, "", call); err != nil {
		slog.Error("rest: preview retry not persisted", "session_id", sessionID, "tool_call_id", toolCallID, "error", err)
	}
}

// previewRetryResult is the call's recorded result with its display media
// and preview group replaced (the shape pkg/agent's previewToolRecord
// writes and replay.go::buildMediaFrame reads); every other key is kept.
func previewRetryResult(old map[string]any, thumbs []tools.DocumentPreviewThumbnail, record generated.MediaPreview) map[string]any {
	out := make(map[string]any, len(old)+3)
	for k, v := range old {
		out[k] = v
	}
	descs := make([]any, 0, len(thumbs))
	for _, th := range thumbs {
		descs = append(descs, map[string]any{
			"ref": th.Ref, "filename": th.Filename, "content_type": th.ContentType,
			"type": "image", "caption": th.Caption, "page_number": th.PageNumber,
		})
	}
	group := map[string]any{
		"source_name":    record.SourceName,
		"source_version": record.SourceVersion,
		"page_count":     record.PageCount,
		"first_page":     record.FirstPage,
		"last_page":      record.LastPage,
		"outcome":        record.Outcome,
	}
	if record.UnavailableReason != nil {
		group["unavailable_reason"] = *record.UnavailableReason
	}
	out["media"] = descs
	out["presentation"] = previewPresentationPageThumbnails
	out["preview"] = group
	return out
}
