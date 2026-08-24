package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const MaxReadFileSize = 64 * 1024 // 64KB limit to avoid context overflow

// isAllowedPath reports whether path matches one of the operator-configured
// AllowRead/WritePaths regex patterns — a DIFFERENT feature from
// filesystem_scope (ADR-046 does not change it; ResolvePathAllowingPatterns
// in resolvepath.go is its new caller for the generic file tools below).
func isAllowedPath(path string, patterns []*regexp.Regexp) bool {
	if len(patterns) == 0 {
		return false
	}

	cleaned := filepath.Clean(path)
	if !filepath.IsAbs(cleaned) {
		return false
	}
	if !matchesAllowedPath(cleaned, patterns) {
		return false
	}

	resolved, err := resolvePathAgainstExistingAncestor(cleaned)
	if err != nil {
		return false
	}

	return matchesAllowedPath(resolved, patterns)
}

func matchesAllowedPath(path string, patterns []*regexp.Regexp) bool {
	cleaned := filepath.Clean(path)
	for _, pattern := range patterns {
		if pattern.MatchString(cleaned) {
			return true
		}
		if root, ok := extractAllowedPathRoot(pattern); ok && isWithinAllowedRoot(cleaned, root) {
			return true
		}
	}
	return false
}

func extractAllowedPathRoot(pattern *regexp.Regexp) (string, bool) {
	raw := pattern.String()
	if !strings.HasPrefix(raw, "^") {
		return "", false
	}

	literal := strings.TrimPrefix(raw, "^")

	// Recognize the common "directory prefix" form: ^<literal>(?:/|$)
	literal = strings.TrimSuffix(literal, "(?:/|$)")
	literal = strings.TrimSuffix(literal, `(?:\\|$)`)

	// Reject patterns that still contain regex operators after removing the
	// optional anchored-directory suffix. That keeps arbitrary regex behavior
	// unchanged and only enables normalized prefix matching for literal paths.
	if containsUnescapedRegexMeta(literal) {
		return "", false
	}

	unescaped, ok := unescapeRegexLiteral(literal)
	if !ok || unescaped == "" {
		return "", false
	}

	return filepath.Clean(unescaped), filepath.IsAbs(unescaped)
}

func appendUniquePath(paths []string, path string) []string {
	for _, existing := range paths {
		if existing == path {
			return paths
		}
	}
	return append(paths, path)
}

func containsUnescapedRegexMeta(s string) bool {
	escaped := false
	for _, r := range s {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		switch r {
		case '.', '+', '*', '?', '(', ')', '[', ']', '{', '}', '|':
			return true
		}
	}
	return escaped
}

func unescapeRegexLiteral(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))

	escaped := false
	for _, r := range s {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}

	if escaped {
		return "", false
	}

	return b.String(), true
}

func isWithinAllowedRoot(path, root string) bool {
	candidate := filepath.Clean(path)
	allowedVariants := []string{filepath.Clean(root)}

	if resolvedRoot, err := resolvePathAgainstExistingAncestor(root); err == nil {
		allowedVariants = appendUniquePath(allowedVariants, filepath.Clean(resolvedRoot))
	}

	for _, allowedRoot := range allowedVariants {
		if isWithinWorkspace(candidate, allowedRoot) {
			return true
		}
	}

	return false
}

func resolvePathAgainstExistingAncestor(path string) (string, error) {
	cleaned := filepath.Clean(path)
	for current := cleaned; ; current = filepath.Dir(current) {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			suffix, relErr := filepath.Rel(current, cleaned)
			if relErr != nil {
				return "", relErr
			}
			if suffix == "." {
				return filepath.Clean(resolved), nil
			}
			return filepath.Clean(filepath.Join(resolved, suffix)), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", os.ErrNotExist
		}
	}
}

// isWithinWorkspace reports whether candidate is workspace itself or lives
// strictly under it. Used by ResolvePath (resolvepath.go) as well as the
// legacy validators below — the single "is this contained" primitive both
// generations of path resolution share.
func isWithinWorkspace(candidate, workspace string) bool {
	rel, err := filepath.Rel(filepath.Clean(workspace), filepath.Clean(candidate))
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// resolveAbsPath returns the absolute path of rawPath relative to workspace,
// with symlinks resolved (best-effort) so the metadata guard cannot be bypassed
// by pointing a decoy filename at a canonical metadata file.
//
// Resolution steps:
//  1. Make rawPath absolute (relative to workspace) and Clean it. This collapses
//     "../" reentry such as agents/<id>/sub/../SOUL.md to agents/<id>/SOUL.md.
//  2. Apply filepath.EvalSymlinks so a symlink (e.g. decoy.md -> SOUL.md) is
//     followed to its real target before the basename match. For not-yet-created
//     files EvalSymlinks fails (the leaf does not exist), so we fall back to
//     resolving the deepest existing ancestor directory and re-joining the
//     remaining components — this defeats the "symlinked directory" vector
//     while still resolving brand-new files.
//
// An error is returned only when workspace itself cannot be made absolute (very
// rare). This helper is used by the metadata guard to resolve a tool path
// argument before metadataFileMatch can determine whether it targets a
// metadata file.
func resolveAbsPath(rawPath, workspace string) (string, error) {
	var abs string
	if filepath.IsAbs(rawPath) {
		abs = filepath.Clean(rawPath)
	} else {
		absWS, err := filepath.Abs(workspace)
		if err != nil {
			return "", err
		}
		abs = filepath.Clean(filepath.Join(absWS, rawPath))
	}

	// Fast path: the full path exists and resolves cleanly through symlinks.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	}

	// Slow path: the leaf (and possibly some parents) does not exist yet.
	// Resolve the deepest existing ancestor through symlinks, then re-attach
	// the not-yet-existing remainder. This still follows a symlinked directory
	// component to its real location.
	dir := filepath.Dir(abs)
	remainder := filepath.Base(abs)
	for dir != filepath.Dir(dir) { // stop at filesystem root
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
		dir = filepath.Dir(dir)
	}

	// Nothing along the chain exists (or symlink resolution failed entirely):
	// fall back to the lexically cleaned absolute path so the basename match
	// still fires. This is the conservative, fail-closed choice.
	return abs, nil
}

// guardMetadataPath enforces the metadata fail-closed guard for a generic file
// tool. It resolves path against workspace (following symlinks and collapsing
// "../" reentry) and, if the result is one of the four canonical metadata files
// under agents/<id>/, returns a structured USE_METADATA_TOOL error result.
//
// Returns nil when the path is allowed to proceed to the real I/O.
//
// workspace here is the caller's fspolicy.FSPolicy.WorkDir — the SAME
// effective working directory ResolvePath confines to (the per-turn
// workspace re-root when set, else the agent's own home) — so the guard
// always matches what the actual I/O will resolve against.
//
// Fail-closed semantics:
//   - workspace == "" is a programming error: every constructor that wires the
//     guard passes a non-empty workspace, and the call sites only invoke this
//     helper when their workspace is set. We log at warn and DENY rather than
//     silently skipping the guard, in case a future caller forgets the gate.
//   - a resolveAbsPath error means we could not establish the canonical path;
//     we log at warn and DENY (the path *might* be a metadata file).
//
// op must be "read" or "write" and is used only to pick the suggested
// replacement tool in the error message.
func guardMetadataPath(workspace, path, op string) *ToolResult {
	if workspace == "" {
		logger.WarnCF("filesystem", "metadata guard invoked with empty workspace; denying (fail-closed)",
			map[string]any{"path": path, "op": op})
		return ErrorResult(metadataGuardError(path, op))
	}
	absPath, err := resolveAbsPath(path, workspace)
	if err != nil {
		logger.WarnCF("filesystem", "metadata guard could not resolve path; denying (fail-closed)",
			map[string]any{"path": path, "op": op, "error": err.Error()})
		return ErrorResult(metadataGuardError(path, op))
	}
	if _, _, matched := metadataFileMatch(absPath); matched {
		return ErrorResult(metadataGuardError(absPath, op))
	}
	return nil
}

type ReadFileTool struct {
	BaseTool
	// agentHome is the agent's fixed home directory (the pre-ADR-046-rename
	// "workspace" constructor parameter) — fspolicy.EffectiveFSPolicy's
	// agentHome argument. When the turn carries a TurnWorkspaceDir (the
	// agent is a member of that turn's Workspace), EffectiveFSPolicy prefers
	// that re-rooted directory instead.
	agentHome string
	// restrict maps to fspolicy.FSScopeConfined (true) / FSScopeUnrestricted
	// (false) — the P1 equivalent of today's restrict flag.
	restrict bool
	maxSize  int64
	// patterns is the operator-configured AllowRead/WritePaths regex axis —
	// a feature orthogonal to filesystem_scope, bridged onto ResolvePath via
	// ResolvePathAllowingPatterns.
	patterns      []*regexp.Regexp
	allowPathsLen int
	// auditLogger receives path.access_denied events on ResolvePath
	// rejections. Nil means audit logging is disabled (best-effort).
	auditLogger *audit.Logger
}

func NewReadFileTool(
	workspace string,
	restrict bool,
	maxReadFileSize int,
	allowPaths ...[]*regexp.Regexp,
) *ReadFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}

	maxSize := int64(maxReadFileSize)
	if maxSize <= 0 {
		maxSize = MaxReadFileSize
	}

	return &ReadFileTool{
		agentHome:     workspace,
		restrict:      restrict,
		maxSize:       maxSize,
		patterns:      patterns,
		allowPathsLen: len(patterns),
	}
}

// SetAuditLogger injects an audit.Logger so that path.access_denied events are
// emitted on workspace-guard rejections. Satisfies the auditLoggerAware
// contract used by the ToolRegistry. Calling this on a nil ReadFileTool is a
// no-op.
func (t *ReadFileTool) SetAuditLogger(l *audit.Logger) {
	if t == nil {
		return
	}
	t.auditLogger = l
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	return "Read the contents of a file. Supports pagination via `offset` and `length`. " +
		"Word (.docx), PowerPoint (.pptx), Excel (.xlsx), and PDF (.pdf) documents are " +
		"automatically decoded to plain text; for these, `offset` and `length` count " +
		"characters of extracted text rather than raw bytes."
}

func (t *ReadFileTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ReadFileTool) Category() ToolCategory { return CategoryFilesystem }

func (t *ReadFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Byte offset to start reading from.",
				"default":     0,
			},
			"length": map[string]any{
				"type":        "integer",
				"description": "Maximum number of bytes to read.",
				"default":     t.maxSize,
			},
		},
		"required": []string{"path"},
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	// Resolve the single, authoritative filesystem policy for this turn
	// (FR-036): WorkDir prefers the per-turn workspace re-root
	// (TurnWorkspaceDir) when the agent is a Workspace CoreTeam member,
	// else falls back to the agent's own home.
	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to resolve filesystem policy: %v", err))
	}

	// Metadata guard: reject reads of agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md
	// via generic file tools — callers must use agent.read_metadata instead.
	// A re-rooted workspace turn has no AGENT metadata files, so the guard
	// is a safe no-op there (it only ever matches the four canonical
	// agents/<id>/ files).
	if policy.WorkDir != "" {
		if denied := guardMetadataPath(policy.WorkDir, path, "read"); denied != nil {
			return denied
		}
	}

	// offset (optional, default 0)
	offset, err := getInt64Arg(args, "offset", 0)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if offset < 0 {
		return ErrorResult("offset must be >= 0")
	}

	// length (optional, capped at MaxReadFileSize)
	length, err := getInt64Arg(args, "length", t.maxSize)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if length <= 0 {
		return ErrorResult("length must be > 0")
	}
	if length > t.maxSize {
		length = t.maxSize
	}

	handle, err := ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpRead, path, t.patterns)
	if err != nil {
		emitPathAccessDenied(ctx, t.auditLogger, t.Name(), path, err, t.allowPathsLen)
		return PermissionDeniedResult(t.Name(), err, err.Error())
	}
	defer handle.Close()

	file, err := handle.Open()
	if err != nil {
		// Emit a path.access_denied audit entry on workspace-guard rejections.
		// emitPathAccessDenied is a no-op when t.auditLogger is nil (best-effort).
		emitPathAccessDenied(ctx, t.auditLogger, t.Name(), path, err, t.allowPathsLen)
		return ErrorResult(err.Error())
	}
	defer file.Close()

	// measure total size
	totalSize := int64(-1) // -1 means unknown
	if info, statErr := file.Stat(); statErr == nil {
		totalSize = info.Size()
	} else {
		logger.WarnCF(
			"filesystem",
			"could not stat file for size",
			map[string]any{"path": path, "error": statErr.Error()},
		)
	}

	// sniff the first 512 bytes to detect binary content before loading
	// it into the LLM context. Seeking back to 0 afterwards restores state.
	sniff := make([]byte, 512)
	sniffN, sniffErr := file.Read(sniff)
	if sniffErr != nil && !errors.Is(sniffErr, io.EOF) {
		return ErrorResult(fmt.Sprintf("failed to sniff file content: %v", sniffErr))
	}

	// Reject binary files: null bytes are a reliable binary indicator.
	if bytes.Contains(sniff[:sniffN], []byte{0}) {
		// Before rejecting, check whether this opaque binary is actually a
		// readable document (Word/PowerPoint/Excel/PDF). If so, return its
		// extracted plain text instead. Extraction reads through the SAME
		// resolved handle this call resolved (handle — the re-rooted
		// workspace policy when the turn carries a workspace dir, else the
		// fixed agent policy), never a raw os.Open, so it cannot escape the
		// boundary ResolvePath enforced.
		if docextract.IsExtractableBytes(sniff[:sniffN], filepath.Base(path)) {
			return t.extractDocument(ctx, handle, path, offset, length)
		}
		return ErrorResult("binary file detected: use a dedicated tool to handle binary files")
	}

	// Reset read position to beginning before applying the caller's offset.
	if seeker, ok := file.(io.Seeker); ok {
		_, err = seeker.Seek(0, io.SeekStart)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to reset file position after sniff: %v", err))
		}
	} else {
		// Non-seekable: we consumed sniffN bytes above; account for them when
		// discarding to reach the requested offset below.
		// If offset < sniffN the data we already read covers it, which we
		// cannot replay on a non-seekable stream — return a clear error.
		// Limitation: for non-seekable streams with offset=0, the first sniffN
		// bytes are consumed by content-type detection and will be missing from the output.
		if offset < int64(sniffN) && offset > 0 {
			return ErrorResult(
				"non-seekable file: cannot seek to an offset within the first 512 bytes after binary detection",
			)
		}
	}

	// Seek to the requested offset.
	if seeker, ok := file.(io.Seeker); ok {
		_, err = seeker.Seek(offset, io.SeekStart)
		if err != nil {
			return ErrorResult(fmt.Sprintf("failed to seek to offset %d: %v", offset, err))
		}
	} else if offset > 0 {
		// Fallback for non-seekable streams: discard leading bytes.
		// sniffN bytes were already consumed above, so subtract them.
		remaining := offset - int64(sniffN)
		if remaining > 0 {
			_, err = io.CopyN(io.Discard, file, remaining)
			if err != nil {
				return ErrorResult(fmt.Sprintf("failed to advance to offset %d: %v", offset, err))
			}
		}
	}

	// read length+1 bytes to reliably detect whether more content exists
	// without relying on totalSize (which may be -1 for non-seekable streams).
	// This avoids the false-positive TRUNCATED message on the last page.
	probe := make([]byte, length+1)
	n, err := io.ReadFull(file, probe)
	// FIX: io.ReadFull returns io.ErrUnexpectedEOF for partial reads (0 < n < len),
	// and io.EOF only when n == 0. Both are normal terminal conditions — only
	// other errors are genuine failures.
	if err != nil && err != io.EOF && !errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrorResult(fmt.Sprintf("failed to read file content: %v", err))
	}

	// hasMore is true only when we actually got the extra probe byte.
	hasMore := int64(n) > length
	data := probe[:min(int64(n), length)]

	if len(data) == 0 {
		return NewToolResult("[END OF FILE - no content at this offset]")
	}

	// Build metadata header.
	// use filepath.Base(path) instead of the raw path to avoid leaking
	// internal filesystem structure into the LLM context.
	readEnd := offset + int64(len(data))
	// use ASCII hyphen-minus instead of en-dash (U+2013) to keep the
	// header parseable by downstream tools and log processors.
	readRange := fmt.Sprintf("bytes %d-%d", offset, readEnd-1)

	displayPath := filepath.Base(path)
	var header string
	if totalSize >= 0 {
		header = fmt.Sprintf(
			"[file: %s | total: %d bytes | read: %s]",
			displayPath, totalSize, readRange,
		)
	} else {
		header = fmt.Sprintf(
			"[file: %s | read: %s | total size unknown]",
			displayPath, readRange,
		)
	}

	if hasMore {
		header += fmt.Sprintf(
			"\n[TRUNCATED - file has more content. Call read_file again with offset=%d to continue.]",
			readEnd,
		)
	} else {
		header += "\n[END OF FILE - no further content.]"
	}

	logger.DebugCF("tool", "ReadFileTool execution completed successfully",
		map[string]any{
			"path":       path,
			"bytes_read": len(data),
			"has_more":   hasMore,
		})

	return NewToolResult(header + "\n\n" + string(data))
}

// extractDocument re-resolves path through handle (the SAME PathHandle the
// calling read_file resolved for this turn — the confined os.Root-backed
// handle when the effective scope is confined, or the host-mode handle when
// unrestricted), returning its extracted plain text, paginated by character
// offset/length so the caller can page through long documents the same way
// it pages raw files.
//
// Sandbox safety: the bytes are read via handle.Open — the same resolved
// handle the calling read_file established — then extraction runs purely in
// memory via docextract.ExtractBytes, which opens no paths of its own, so it
// cannot read outside the boundary ResolvePath enforced even for archive
// formats that re-open by path.
//
// offset and length are interpreted as character (rune) positions into the
// extracted text, not byte positions into the binary file (byte positions are
// meaningless once the document is decoded).
func (t *ReadFileTool) extractDocument(
	ctx context.Context,
	handle *PathHandle,
	path string,
	offset, length int64,
) *ToolResult {
	file, err := handle.Open()
	if err != nil {
		emitPathAccessDenied(ctx, t.auditLogger, t.Name(), path, err, t.allowPathsLen)
		return ErrorResult(err.Error())
	}
	defer file.Close()

	// Read up to MaxDocBytes+1 so we can detect (and reject) oversized files
	// without buffering them in full.
	raw, err := io.ReadAll(io.LimitReader(file, docextract.MaxDocBytes+1))
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read document: %v", err))
	}
	if int64(len(raw)) > docextract.MaxDocBytes {
		return ErrorResult(fmt.Sprintf(
			"document too large to extract (limit %d bytes); use a dedicated tool",
			docextract.MaxDocBytes,
		))
	}

	text, ok, reason := docextract.ExtractBytes(raw, "", filepath.Base(path))
	if !ok {
		return ErrorResult(fmt.Sprintf(
			"binary file detected: could not extract document text (%s)", reason,
		))
	}

	// Paginate the extracted text by rune offset so multi-page documents can be
	// retrieved across multiple calls, mirroring the raw-read offset contract.
	runes := []rune(text)
	total := int64(len(runes))
	start := offset
	if start > total {
		start = total
	}
	end := start + length
	if end > total {
		end = total
	}
	page := string(runes[start:end])
	hasMore := end < total

	displayPath := filepath.Base(path)
	header := fmt.Sprintf(
		"[document: %s | extracted text | chars %d-%d of %d]",
		displayPath, start, end, total,
	)
	if start >= total {
		return NewToolResult(header + "\n[END OF DOCUMENT - no content at this offset.]")
	}
	if hasMore {
		header += fmt.Sprintf(
			"\n[TRUNCATED - document has more text. Call read_file again with offset=%d to continue.]",
			end,
		)
	} else {
		header += "\n[END OF DOCUMENT - no further content.]"
	}

	logger.DebugCF("tool", "ReadFileTool document extraction completed",
		map[string]any{
			"path":        path,
			"chars_total": total,
			"chars_read":  end - start,
			"has_more":    hasMore,
		})

	return NewToolResult(header + "\n\n" + page)
}

// getInt64Arg extracts an integer argument from the args map, returning the
// provided default if the key is absent.
func getInt64Arg(args map[string]any, key string, defaultVal int64) (int64, error) {
	raw, exists := args[key]
	if !exists {
		return defaultVal, nil
	}

	switch v := raw.(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, fmt.Errorf("%s must be an integer, got float %v", key, v)
		}
		if v > math.MaxInt64 || v < math.MinInt64 {
			return 0, fmt.Errorf("%s value %v overflows int64", key, v)
		}
		return int64(v), nil
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case string:
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid integer format for %s parameter: %w", key, err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("unsupported type %T for %s parameter", raw, key)
	}
}

type WriteFileTool struct {
	BaseTool
	agentHome string
	restrict  bool
	patterns  []*regexp.Regexp
	// auditLogger receives file_op audit entries on successful writes, so
	// lastWriterForPath (path_audit.go) can attribute a CoreTeam-shared-dir
	// overwrite to the agent that last touched the path. Nil means audit
	// logging is disabled (best-effort, no attribution note — never blocking).
	auditLogger *audit.Logger
}

func NewWriteFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *WriteFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &WriteFileTool{
		agentHome: workspace,
		restrict:  restrict,
		patterns:  patterns,
	}
}

// SetAuditLogger injects an audit.Logger so successful writes are recorded
// (file_op events) and overwrite calls can look up the prior writer. Satisfies
// the auditLoggerAware contract used by the ToolRegistry. Calling this on a
// nil WriteFileTool is a no-op.
func (t *WriteFileTool) SetAuditLogger(l *audit.Logger) {
	if t == nil {
		return
	}
	t.auditLogger = l
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	return "Write content to a file. If the file already exists, you must set overwrite=true to replace it."
}

func (t *WriteFileTool) Scope() ToolScope       { return ScopeCore }
func (t *WriteFileTool) Category() ToolCategory { return CategoryFilesystem }

func (t *WriteFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": "Must be set to true to overwrite an existing file.",
				"default":     false,
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to resolve filesystem policy: %v", err))
	}

	// Metadata guard: reject writes to agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md
	// via generic file tools — callers must use agent.write_metadata instead.
	if policy.WorkDir != "" {
		if denied := guardMetadataPath(policy.WorkDir, path, "write"); denied != nil {
			return denied
		}
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	overwrite, _ := args["overwrite"].(bool)

	handle, err := ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpWrite, path, t.patterns)
	if err != nil {
		return PermissionDeniedResult(t.Name(), err, err.Error())
	}
	defer handle.Close()

	// Canonical path for audit-log keying — the handle's own resolved
	// absolute path (advisory-only per PathHandle.RealPath's doc, but safe
	// here since it is used only as an audit-log dictionary key, never for
	// further I/O). Falls back to the raw arg path if RealPath somehow
	// yields nothing, matching the prior best-effort contract.
	canonicalPath := path
	if resolved, rpErr := handle.RealPath(); rpErr == nil && resolved != "" {
		canonicalPath = resolved
	}

	var clobberNote string
	if !overwrite {
		if _, statErr := handle.Stat(); statErr == nil {
			// A PRECONDITION refusal, not a genuine I/O failure: the file is
			// already there. The prose alone was the same shape as the real
			// write failure two branches below (ErrorResult(err.Error())), so
			// a caller could not tell "a sibling already wrote this, no action
			// needed" from "the write broke" without matching on wording.
			//
			// ADR-059 W5 carries a discriminator inside the text the CALLING
			// AGENT reads, per ADR-058's convention — not a Go struct field,
			// which no language model can see. An earlier attempt added a
			// ToolResult.Reason field here; W3 removed it for exactly that.
			// The sentence itself is unchanged and travels as the payload's
			// reason.
			return FileExistsRefusalResult(t.Name(), path,
				fmt.Sprintf("file: %s already exists. Set overwrite=true to replace.", path))
		}
	} else if _, statErr := handle.Stat(); statErr == nil {
		// We are about to replace a file that already exists. Look up who
		// last wrote it (per the audit log's write history) and, if it was a
		// DIFFERENT agent, surface that as a purely informational note — no
		// new blocking behavior, this write proceeds regardless.
		if writerID, found := lastWriterForPath(t.auditLogger, canonicalPath, ToolAgentID(ctx)); found {
			// Precisely scoped wording: only write_file calls are tracked
			// (edit_file/append_file are not), and only within
			// LastWriterForPath's current-file-plus-most-recent-rotated-file
			// window — "last written via write_file by" says exactly that,
			// rather than implying a complete modification history.
			clobberNote = fmt.Sprintf(
				" Note: you are replacing a file last written via write_file by agent %s.",
				writerID,
			)
		}
	}

	if err := handle.WriteFile([]byte(content)); err != nil {
		return ErrorResult(err.Error())
	}

	emitFileWriteAudit(ctx, t.auditLogger, t.Name(), canonicalPath)

	return SilentResult(fmt.Sprintf("File written: %s", path) + clobberNote)
}

type ListDirTool struct {
	BaseTool
	agentHome string
	restrict  bool
	patterns  []*regexp.Regexp
}

func NewListDirTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *ListDirTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &ListDirTool{
		agentHome: workspace,
		restrict:  restrict,
		patterns:  patterns,
	}
}

func (t *ListDirTool) Name() string {
	return "list_directory"
}

func (t *ListDirTool) Description() string {
	return "List files and directories in a path. Large directories page with offset/limit " +
		"(entries), the same way read_file pages a file with offset/length (bytes)."
}

func (t *ListDirTool) Scope() ToolScope       { return ScopeGeneral }
func (t *ListDirTool) Category() ToolCategory { return CategoryFilesystem }

func (t *ListDirTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to list",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Entry index to start listing from (0-based, must be >= 0).",
				"default":     0,
			},
			"limit": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf(
					"Maximum number of entries to return (must be >= 1, capped at %d).", maxListDirEntries),
				"default": maxListDirEntries,
			},
		},
		"required": []string{"path"},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}

	// Bounding parameters (ADR-066 §15 task 1, FR-039): entries, named as
	// read_file names its byte window. An out-of-range value is a tool
	// error, never a silent clamp — the model must learn its call was wrong.
	offset, err := getInt64Arg(args, "offset", 0)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if offset < 0 {
		return ErrorResult("offset must be >= 0")
	}
	limit, err := getInt64Arg(args, "limit", maxListDirEntries)
	if err != nil {
		return ErrorResult(err.Error())
	}
	if limit < 1 {
		return ErrorResult("limit must be >= 1")
	}
	if limit > maxListDirEntries {
		limit = maxListDirEntries
	}

	policy, err := ResolveTurnFSPolicy(ctx, t.agentHome, t.restrict)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to resolve filesystem policy: %v", err))
	}

	handle, err := ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpList, path, t.patterns)
	if err != nil {
		return PermissionDeniedResult(t.Name(), err, err.Error())
	}
	defer handle.Close()

	entries, err := handle.ReadDir()
	if err != nil {
		return ErrorResult(err.Error())
	}
	return formatDirEntries(entries, int(offset), int(limit))
}

// maxListDirEntries bounds one list_directory page (ADR-066 §15 task 1,
// FR-039). It is both the default and the ceiling for `limit`: a directory
// with more entries than this pages through offset instead of returning a
// result the D4 cap would have to truncate blindly mid-name.
const maxListDirEntries = 1000

// formatDirEntries renders one page of a directory listing. offset/limit are
// already validated by the caller (offset >= 0, 1 <= limit <= maxListDirEntries).
// A page that does not cover the whole directory carries a framing line
// stating the total and the next offset, so the model can page deliberately
// rather than guess whether it saw everything.
func formatDirEntries(entries []os.DirEntry, offset, limit int) *ToolResult {
	total := len(entries)
	start := min(offset, total)
	end := min(start+limit, total)

	var result strings.Builder
	for _, entry := range entries[start:end] {
		if entry.IsDir() {
			result.WriteString("DIR:  " + entry.Name() + "\n")
		} else {
			result.WriteString("FILE: " + entry.Name() + "\n")
		}
	}
	if start > 0 || end < total {
		next := "end"
		if end < total {
			next = strconv.Itoa(end)
		}
		fmt.Fprintf(&result,
			"[listing: entries %d-%d of %d, offset=%d, next_offset=%s]\n",
			start, end, total, start, next)
	}
	return NewToolResult(result.String())
}
