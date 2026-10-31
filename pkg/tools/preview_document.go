// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// PreviewDocumentTool is preview_document (office-preview spec AD-1, AD-5,
// AD-13): it shows a .docx, .xlsx, .pptx or .pdf to the model in the best
// form the active model reads, and page thumbnails to the person.
//
// The source is read through exactly read_file's authorization (policy,
// metadata guard, auto-approve pin, audit); refusals match read_file's
// result. The conversion and rendering run out of process through
// pkg/preview; the model forms travel live-turn only in
// ToolResult.DocumentPreview, the thumbnails are stored like browser
// screenshots and never enter the model-media path.
type PreviewDocumentTool struct {
	BaseTool
	agentHome     string
	restrict      bool
	patterns      []*regexp.Regexp
	allowPathsLen int
	service       *preview.Service
	mediaStore    media.MediaStore
	auditLogger   *audit.Logger
}

// PreviewDocumentOption configures the tool at construction.
type PreviewDocumentOption func(*PreviewDocumentTool)

// WithPreviewService wires a preview service. Without it the tool uses the
// gateway's shared service (preview.Shared); with neither, every call that
// passes the input checks is refused as unavailable.
func WithPreviewService(s *preview.Service) PreviewDocumentOption {
	return func(t *PreviewDocumentTool) { t.service = s }
}

// WithPreviewAllowPaths applies the operator's read allow-list patterns,
// exactly as read_file receives them.
func WithPreviewAllowPaths(patterns []*regexp.Regexp) PreviewDocumentOption {
	return func(t *PreviewDocumentTool) {
		t.patterns = patterns
		t.allowPathsLen = len(patterns)
	}
}

// NewPreviewDocumentTool builds the tool with the read_file constructor
// convention (agent home, restrict-to-workspace flag).
func NewPreviewDocumentTool(workspace string, restrict bool, opts ...PreviewDocumentOption) *PreviewDocumentTool {
	t := &PreviewDocumentTool{agentHome: workspace, restrict: restrict}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

// SetMediaStore satisfies the registry's mediaStoreAware contract
// (thumbnail storage).
func (t *PreviewDocumentTool) SetMediaStore(store media.MediaStore) { t.mediaStore = store }

// SetAuditLogger satisfies the registry's auditLoggerAware contract.
func (t *PreviewDocumentTool) SetAuditLogger(l *audit.Logger) {
	if t != nil {
		t.auditLogger = l
	}
}

func (t *PreviewDocumentTool) Name() string { return "preview_document" }

// Description is placeholder text owned by prometheus-prompt-engineer, who
// writes the final tool description (spec §18 W4).
func (t *PreviewDocumentTool) Description() string {
	return "Show a .docx, .xlsx, .pptx or .pdf file from the workspace so you can check how it looks. " +
		"Pages come in batches: pass pages as \"N\" or \"N-M\" (at most 20 pages per call); " +
		"without pages you get the first 10."
}

func (t *PreviewDocumentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Workspace path of the .docx, .xlsx, .pptx or .pdf file.",
			},
			"pages": map[string]any{
				"type":        "string",
				"description": "Optional page range: \"N\" or \"N-M\", at most 20 pages. Default: the first 10 pages.",
			},
		},
		"required": []string{"file"},
	}
}

func (t *PreviewDocumentTool) Scope() ToolScope { return ScopeGeneral }

func (t *PreviewDocumentTool) Category() ToolCategory { return CategoryFilesystem }

// MV-1 messages (office-preview spec §9).
const (
	previewPrefix          = "preview_document: "
	msgPreviewUnsupported  = previewPrefix + "unsupported file type; supported: .docx, .xlsx, .pptx, .pdf"
	msgPreviewTooLarge     = previewPrefix + "file exceeds the 50 MiB preview limit"
	msgPreviewPages        = previewPrefix + `pages must be "N" or "N-M" with 1 <= N <= M <= 9999 and at most 20 pages`
	msgPreviewEncrypted    = previewPrefix + "password-protected documents cannot be previewed"
	msgPreviewUnavailable  = previewPrefix + "document preview is not available in this process"
	readFileToolName       = "read_file"
	previewMaxPagesPerCall = 20
	previewMaxPageNumber   = 9999
)

func msgPreviewMismatch(ext string) string {
	return previewPrefix + "file content does not match its " + ext + " extension"
}

var previewPagesRe = regexp.MustCompile(`^([0-9]{1,5})(?:-([0-9]{1,5}))?$`)

// parsePreviewPages applies the AD-5 syntax rule. ok=false is the MV-1
// pages refusal; def reports "pages omitted" (default first 10).
func parsePreviewPages(args map[string]any) (first, last int, def, ok bool) {
	raw, present := args["pages"]
	if !present || raw == nil {
		return 1, preview.DefaultPages, true, true
	}
	s, isString := raw.(string)
	if !isString {
		return 0, 0, false, false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 1, preview.DefaultPages, true, true
	}
	m := previewPagesRe.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false, false
	}
	first, _ = strconv.Atoi(m[1])
	last = first
	if m[2] != "" {
		last, _ = strconv.Atoi(m[2])
	}
	if first < 1 || last < first || last > previewMaxPageNumber || last-first >= previewMaxPagesPerCall {
		return 0, 0, false, false
	}
	return first, last, false, true
}

func previewExt(path string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf", ".docx", ".xlsx", ".pptx":
		return ext, true
	}
	return ext, false
}

func (t *PreviewDocumentTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// AD-5 (1): pages syntax and file checks before any work.
	first, last, def, ok := parsePreviewPages(args)
	if !ok {
		return ErrorResult(msgPreviewPages)
	}
	path, _ := args["file"].(string)
	if strings.TrimSpace(path) == "" {
		return ErrorResult(previewPrefix + "file is required")
	}
	ext, supported := previewExt(path)
	if !supported {
		return ErrorResult(msgPreviewUnsupported)
	}

	src, denied := t.openSource(ctx, path)
	if denied != nil {
		return denied
	}
	defer src.close()
	if refusal := src.precheck(ext); refusal != nil {
		return refusal
	}
	svc := t.service
	if svc == nil {
		svc = preview.Shared()
	}
	if svc == nil {
		return ErrorResult(msgPreviewUnavailable)
	}

	callCtx, cancel := context.WithTimeout(ctx, preview.CallTimeout)
	defer cancel()
	// Admission before reading or copying the source (AD-6, T-39).
	adm, err := svc.Admit(callCtx, preview.IsOffice(ext))
	if err != nil {
		return ErrorResult(previewPrefix + err.Error())
	}
	defer adm.Release()

	data, refusal := src.readAll(ext)
	if refusal != nil {
		return refusal
	}
	readModel, err := sandbox.KernelPolicyForTurn(src.policy)
	if err != nil {
		return ErrorResult(previewPrefix + "the sandbox policy for this turn could not be built: " + err.Error())
	}
	res, err := svc.Preview(callCtx, adm, preview.Request{
		Name:             filepath.Base(path),
		Ext:              ext,
		Data:             data,
		First:            first,
		Last:             last,
		Default:          def,
		ReadModel:        readModel,
		CallerWriteRoots: preview.TurnWriteRoots(src.policy),
	})
	if err != nil {
		return ErrorResult(previewPrefix + err.Error())
	}
	return t.buildResult(ctx, filepath.Base(path), res, src.reauthorize)
}

// previewSource is the authorized, open source file of one call.
type previewSource struct {
	tool        *PreviewDocumentTool
	policy      fspolicy.FSPolicy
	handle      *PathHandle
	file        io.ReadCloser
	size        int64
	path        string
	reauthorize func(context.Context) error
}

func (s *previewSource) close() {
	if s.file != nil {
		_ = s.file.Close()
	}
	if s.handle != nil {
		_ = s.handle.Close()
	}
}

// openSource authorizes and opens path exactly as read_file does; a
// refusal is read_file's own result for the same path (MV-1, T-15).
func (t *PreviewDocumentTool) openSource(ctx context.Context, path string) (*previewSource, *ToolResult) {
	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return nil, ErrorResult(fmt.Sprintf("failed to resolve filesystem policy: %v", err))
	}
	if policy.WorkDir != "" {
		if denied := guardMetadataPath(policy.WorkDir, path, "read"); denied != nil {
			return nil, denied
		}
	}
	resolve := func(checkCtx context.Context) (*PathHandle, error) {
		handle, resolveErr := ResolvePathAllowingPatterns(checkCtx, policy, readFileToolName, "", FSOpRead, path, t.patterns)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if _, pinned := AutoPinFrom(checkCtx); pinned {
			realPath, rpErr := handle.RealPath()
			if rpErr == nil {
				rpErr = RecheckAutoPin(checkCtx, t.Name(), policy, realPath, fspolicy.PathGrantAccessRead)
			}
			if rpErr != nil {
				_ = handle.Close()
				return nil, rpErr
			}
		}
		return handle, nil
	}
	handle, err := resolve(ctx)
	if err != nil {
		emitPathAccessDeniedCorrelated(ctx, t.auditLogger, t.Name(), path, err, t.allowPathsLen)
		return nil, PermissionDeniedResult(readFileToolName, err, err.Error())
	}
	src := &previewSource{tool: t, policy: policy, handle: handle, path: path}
	src.reauthorize = func(checkCtx context.Context) error {
		fresh, checkErr := resolve(checkCtx)
		if checkErr != nil {
			return checkErr
		}
		return fresh.Close()
	}
	info, err := handle.Stat()
	if err != nil {
		src.close()
		return nil, ErrorResult(previewPrefix + "the file could not be read: " + err.Error())
	}
	if !info.Mode().IsRegular() {
		src.close()
		return nil, ErrorResult(previewPrefix + "the file is not a regular file")
	}
	src.size = info.Size()
	file, err := handle.OpenNonBlockingRead()
	if err != nil {
		emitPathAccessDeniedCorrelated(ctx, t.auditLogger, t.Name(), path, err, t.allowPathsLen)
		src.close()
		return nil, ErrorResult(err.Error())
	}
	src.file = file
	if resolved, realErr := handle.RealPath(); realErr == nil {
		emitFileReadAudit(ctx, t.auditLogger, t.Name(), resolved, "read")
	}
	return src, nil
}

// precheck refuses oversize and wrong-signature files before admission.
func (s *previewSource) precheck(ext string) *ToolResult {
	if s.size > preview.MaxInputBytes {
		return ErrorResult(msgPreviewTooLarge)
	}
	head := make([]byte, 8)
	n, err := io.ReadFull(s.file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return ErrorResult(previewPrefix + "the file could not be read: " + err.Error())
	}
	if !signatureMatches(ext, head[:n]) {
		return ErrorResult(msgPreviewMismatch(ext))
	}
	s.file = readCloser{io.MultiReader(bytes.NewReader(head[:n]), s.file), s.file}
	return nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

// readAll reads the source under the 50 MiB limit and checks the OOXML
// package type (AD-5: content matches the extension, not encrypted).
func (s *previewSource) readAll(ext string) ([]byte, *ToolResult) {
	data, err := io.ReadAll(io.LimitReader(s.file, preview.MaxInputBytes+1))
	if err != nil {
		return nil, ErrorResult(previewPrefix + "the file could not be read: " + err.Error())
	}
	if len(data) > preview.MaxInputBytes {
		return nil, ErrorResult(msgPreviewTooLarge)
	}
	if ext != ".pdf" {
		switch officeContentCheck(ext, data) {
		case officeEncrypted:
			return nil, ErrorResult(msgPreviewEncrypted)
		case officeMismatch:
			return nil, ErrorResult(msgPreviewMismatch(ext))
		}
	}
	return data, nil
}
