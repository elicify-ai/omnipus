package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const MaxReadFileSize = 64 * 1024 // 64KB limit to avoid context overflow

func validatePathWithAllowPaths(path, workspace string, restrict bool, patterns []*regexp.Regexp) (string, error) {
	if workspace == "" {
		return path, fmt.Errorf("workspace is not defined")
	}

	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("failed to resolve workspace path: %w", err)
	}

	var absPath string
	if filepath.IsAbs(path) {
		absPath = filepath.Clean(path)
	} else {
		absPath, err = filepath.Abs(filepath.Join(absWorkspace, path))
		if err != nil {
			return "", fmt.Errorf("failed to resolve file path: %w", err)
		}
	}

	if restrict {
		if isAllowedPath(absPath, patterns) {
			return absPath, nil
		}

		if !isWithinWorkspace(absPath, absWorkspace) {
			return "", fmt.Errorf("access denied: path is outside the workspace")
		}

		var resolved string
		workspaceReal := absWorkspace
		if resolved, err = filepath.EvalSymlinks(absWorkspace); err == nil {
			workspaceReal = resolved
		}

		if resolved, err = filepath.EvalSymlinks(absPath); err == nil {
			if !isWithinWorkspace(resolved, workspaceReal) {
				return "", fmt.Errorf("access denied: symlink resolves outside workspace")
			}
		} else if os.IsNotExist(err) {
			var parentResolved string
			if parentResolved, err = resolveExistingAncestor(filepath.Dir(absPath)); err == nil {
				if !isWithinWorkspace(parentResolved, workspaceReal) {
					return "", fmt.Errorf("access denied: symlink resolves outside workspace")
				}
			} else if !os.IsNotExist(err) {
				return "", fmt.Errorf("failed to resolve path: %w", err)
			}
		} else {
			return "", fmt.Errorf("failed to resolve path: %w", err)
		}
	}

	// FR-017: deny access to another agent's workspace even when restrict=false.
	// Derives the agents directory from the current workspace (its parent) and
	// blocks any path that lives under a sibling agent directory.
	if workspace != "" && isCrossAgentPath(absPath, absWorkspace) {
		return "", fmt.Errorf("access denied: path is inside another agent's workspace")
	}

	return absPath, nil
}

// isCrossAgentPath returns true when absPath is under a sibling agent workspace.
// It assumes the layout: <home>/agents/<agent-id>/ so the agents root is the
// grandparent of the current workspace.
// Example: workspace=~/.omnipus/agents/agent-A, absPath=~/.omnipus/agents/agent-B/x
// → agentsRoot = ~/.omnipus/agents, under agentsRoot but not under workspace → true
//
// The workspace root itself (absPath == absWorkspace) is always allowed; this
// handles the common case of an agent serving "." which resolves to its own
// workspace root without a trailing separator.
func isCrossAgentPath(absPath, absWorkspace string) bool {
	cleanWorkspace := filepath.Clean(absWorkspace)
	cleanPath := filepath.Clean(absPath)

	// The workspace root itself is never a cross-agent path.
	if cleanPath == cleanWorkspace {
		return false
	}

	agentsRoot := filepath.Dir(cleanWorkspace)
	if agentsRoot == cleanWorkspace || agentsRoot == "." {
		return false // can't derive agents root
	}
	agentsRootSlash := agentsRoot + string(filepath.Separator)
	workspaceSlash := cleanWorkspace + string(filepath.Separator)
	return strings.HasPrefix(cleanPath, agentsRootSlash) &&
		!strings.HasPrefix(cleanPath, workspaceSlash)
}

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

func resolveExistingAncestor(path string) (string, error) {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", os.ErrNotExist
		}
	}
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

// rerootable holds the construction parameters a file tool needs to rebuild its
// confined fileSystem against a per-turn workspace root. It is embedded by
// every generic file tool so the re-root logic lives in one place.
//
// When a turn carries no workspace dir (the turn's agent is not a member of
// any Workspace's CoreTeam — see workspace.FindForAgent and the agent loop's
// runTurn), effectiveFs returns the pre-built fixed-root fs and
// effectiveWorkspace returns the fixed agent workspace unchanged. When a turn
// DOES carry a workspace dir, both helpers rebuild against that dir using the
// SAME restrict + allow-path-pattern config the fixed root was built with, so
// every existing guard (os.Root confinement, metadata guard, cross-agent
// guard, allow-paths) applies relative to the re-rooted dir.
type rerootable struct {
	restrict bool
	patterns []*regexp.Regexp
}

// effectiveFs returns the fileSystem to use for this call: the fixed fs when no
// per-turn workspace dir is set, or a freshly-built fs rooted at the per-turn
// workspace dir when one is. fixedFs and fixedWorkspace are the tool's
// construction-time values.
func (r rerootable) effectiveFs(ctx context.Context, fixedFs fileSystem) fileSystem {
	if dir := TurnWorkspaceDir(ctx); dir != "" {
		return buildFs(dir, r.restrict, r.patterns)
	}
	return fixedFs
}

// effectiveWorkspace returns the workspace root the metadata/cross-agent guards
// should resolve against: the per-turn workspace dir when set, else the fixed
// agent workspace.
func (r rerootable) effectiveWorkspace(ctx context.Context, fixedWorkspace string) string {
	if dir := TurnWorkspaceDir(ctx); dir != "" {
		return dir
	}
	return fixedWorkspace
}

type ReadFileTool struct {
	BaseTool
	rerootable
	fs            fileSystem
	maxSize       int64
	allowPathsLen int
	// workspace is the agent's workspace root used for metadata-guard path
	// resolution. Empty means no guard is applied (e.g. static read-only tools
	// that have no agent workspace concept).
	workspace string
	// auditLogger receives path.access_denied events on workspace-guard
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
		rerootable:    rerootable{restrict: restrict, patterns: patterns},
		fs:            buildFs(workspace, restrict, patterns),
		maxSize:       maxSize,
		allowPathsLen: len(patterns),
		workspace:     workspace,
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

	// Resolve the effective root for this call. When the turn re-roots to a
	// workspace dir (the agent is a member of that Workspace's CoreTeam),
	// effFs is a fresh fs confined to that dir and effWorkspace is that dir;
	// otherwise they are the fixed agent fs/workspace, unchanged.
	effFs := t.effectiveFs(ctx, t.fs)
	effWorkspace := t.effectiveWorkspace(ctx, t.workspace)

	// Metadata guard: reject reads of agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md
	// via generic file tools — callers must use agent.read_metadata instead.
	// Skipped only for static tools that have no agent workspace concept.
	// When re-rooted to a workspace dir the guard resolves against that dir; a
	// workspace dir has no AGENT metadata files, so the guard is a safe no-op
	// there (it only ever matches the four canonical agents/<id>/ files).
	if effWorkspace != "" {
		if denied := guardMetadataPath(effWorkspace, path, "read"); denied != nil {
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

	file, err := effFs.Open(path)
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
	sniffN, _ := file.Read(sniff)

	// Reject binary files: null bytes are a reliable binary indicator.
	if bytes.Contains(sniff[:sniffN], []byte{0}) {
		// Before rejecting, check whether this opaque binary is actually a
		// readable document (Word/PowerPoint/Excel/PDF). If so, return its
		// extracted plain text instead. Extraction reads through the SAME
		// confined filesystem this call resolved (effFs — the re-rooted
		// workspace fs when the turn carries a workspace dir, else t.fs), never
		// a raw os.Open, so it cannot escape the boundary effFs.Open enforced.
		if docextract.IsExtractable("", filepath.Base(path)) {
			return t.extractDocument(ctx, effFs, path, offset, length)
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

// extractDocument reads a document file through the sandboxed filesystem and
// returns its extracted plain text, paginated by character offset/length so the
// caller can page through long documents the same way it pages raw files.
//
// Sandbox safety: the bytes are read via effFs.Open — the same confined handle
// the calling read_file resolved for this turn (re-rooted to the workspace dir
// when the agent is a CoreTeam member, else the fixed agent fs) — then
// extraction runs purely in memory via docextract.ExtractBytes, which opens no
// paths of its own, so it cannot read outside the workspace even for archive
// formats that re-open by path.
//
// offset and length are interpreted as character (rune) positions into the
// extracted text, not byte positions into the binary file (byte positions are
// meaningless once the document is decoded).
func (t *ReadFileTool) extractDocument(
	ctx context.Context,
	effFs fileSystem,
	path string,
	offset, length int64,
) *ToolResult {
	file, err := effFs.Open(path)
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
	rerootable
	fs        fileSystem
	workspace string
}

func NewWriteFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *WriteFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &WriteFileTool{
		rerootable: rerootable{restrict: restrict, patterns: patterns},
		fs:         buildFs(workspace, restrict, patterns),
		workspace:  workspace,
	}
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

	effFs := t.effectiveFs(ctx, t.fs)
	effWorkspace := t.effectiveWorkspace(ctx, t.workspace)

	// Metadata guard: reject writes to agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md
	// via generic file tools — callers must use agent.write_metadata instead.
	// Skipped only for static tools that have no agent workspace concept.
	if effWorkspace != "" {
		if denied := guardMetadataPath(effWorkspace, path, "write"); denied != nil {
			return denied
		}
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	overwrite, _ := args["overwrite"].(bool)

	if !overwrite {
		if f, err := effFs.Open(path); err == nil {
			f.Close()
			return ErrorResult(fmt.Sprintf("file: %s already exists. Set overwrite=true to replace.", path))
		}
	}

	if err := effFs.WriteFile(path, []byte(content)); err != nil {
		return ErrorResult(err.Error())
	}

	return SilentResult(fmt.Sprintf("File written: %s", path))
}

type ListDirTool struct {
	BaseTool
	rerootable
	fs fileSystem
}

func NewListDirTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *ListDirTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &ListDirTool{
		rerootable: rerootable{restrict: restrict, patterns: patterns},
		fs:         buildFs(workspace, restrict, patterns),
	}
}

func (t *ListDirTool) Name() string {
	return "list_directory"
}

func (t *ListDirTool) Description() string {
	return "List files and directories in a path"
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
		},
		"required": []string{"path"},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}

	entries, err := t.effectiveFs(ctx, t.fs).ReadDir(path)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read directory: %v", err))
	}
	return formatDirEntries(entries)
}

func formatDirEntries(entries []os.DirEntry) *ToolResult {
	var result strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			result.WriteString("DIR:  " + entry.Name() + "\n")
		} else {
			result.WriteString("FILE: " + entry.Name() + "\n")
		}
	}
	return NewToolResult(result.String())
}

// fileSystem abstracts reading, writing, and listing files, allowing both
// unrestricted (host filesystem) and sandbox (os.Root) implementations to share the same polymorphic interface.
type fileSystem interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	ReadDir(path string) ([]os.DirEntry, error)
	Open(path string) (fs.File, error)
}

// hostFs is an unrestricted fileReadWriter that operates directly on the host filesystem.
type hostFs struct{}

func (h *hostFs) ReadFile(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read file: file not found: %w", err)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("failed to read file: access denied: %w", err)
		}
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	return content, nil
}

func (h *hostFs) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

func (h *hostFs) WriteFile(path string, data []byte) error {
	// Use unified atomic write utility with explicit sync for flash storage reliability.
	// Using 0o600 (owner read/write only) for secure default permissions.
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

func (h *hostFs) Open(path string) (fs.File, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to open file: file not found: %w", err)
		}
		if os.IsPermission(err) {
			return nil, fmt.Errorf("failed to open file: access denied: %w", err)
		}
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return f, nil
}

// sandboxFs is a sandboxed fileSystem that operates within a strictly defined workspace using os.Root.
type sandboxFs struct {
	workspace string
}

func (r *sandboxFs) execute(path string, fn func(root *os.Root, relPath string) error) error {
	if r.workspace == "" {
		return fmt.Errorf("workspace is not defined")
	}

	root, err := os.OpenRoot(r.workspace)
	if err != nil {
		return fmt.Errorf("failed to open workspace: %w", err)
	}
	defer root.Close()

	relPath, err := getSafeRelPath(r.workspace, path)
	if err != nil {
		return err
	}

	return fn(root, relPath)
}

func (r *sandboxFs) ReadFile(path string) ([]byte, error) {
	var content []byte
	err := r.execute(path, func(root *os.Root, relPath string) error {
		fileContent, err := root.ReadFile(relPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("failed to read file: file not found: %w", err)
			}
			// os.Root returns "escapes from parent" for paths outside the root
			if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
				strings.Contains(err.Error(), "permission denied") {
				return fmt.Errorf("failed to read file: access denied: %w", err)
			}
			return fmt.Errorf("failed to read file: %w", err)
		}
		content = fileContent
		return nil
	})
	return content, err
}

func (r *sandboxFs) WriteFile(path string, data []byte) error {
	return r.execute(path, func(root *os.Root, relPath string) error {
		dir := filepath.Dir(relPath)
		if dir != "." && dir != "/" {
			if err := root.MkdirAll(dir, 0o755); err != nil {
				return fmt.Errorf("failed to create parent directories: %w", err)
			}
		}

		// Use atomic write pattern with explicit sync for flash storage reliability.
		// Using 0o600 (owner read/write only) for secure default permissions.
		tmpRelPath := fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())

		tmpFile, err := root.OpenFile(tmpRelPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to open temp file: %w", err)
		}

		if _, err := tmpFile.Write(data); err != nil {
			tmpFile.Close()
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to write temp file: %w", err)
		}

		// CRITICAL: Force sync to storage medium before rename.
		// This ensures data is physically written to disk, not just cached.
		if err := tmpFile.Sync(); err != nil {
			tmpFile.Close()
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to sync temp file: %w", err)
		}

		if err := tmpFile.Close(); err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to close temp file: %w", err)
		}

		if err := root.Rename(tmpRelPath, relPath); err != nil {
			root.Remove(tmpRelPath)
			return fmt.Errorf("failed to rename temp file over target: %w", err)
		}

		// Sync directory to ensure rename is durable
		if dirFile, err := root.Open("."); err == nil {
			if syncErr := dirFile.Sync(); syncErr != nil {
				logger.WarnCF(
					"filesystem",
					"directory sync failed after atomic write",
					map[string]any{"error": syncErr.Error()},
				)
			}
			dirFile.Close()
		}

		return nil
	})
}

func (r *sandboxFs) ReadDir(path string) ([]os.DirEntry, error) {
	var entries []os.DirEntry
	err := r.execute(path, func(root *os.Root, relPath string) error {
		dirEntries, err := fs.ReadDir(root.FS(), relPath)
		if err != nil {
			return err
		}
		entries = dirEntries
		return nil
	})
	return entries, err
}

func (r *sandboxFs) Open(path string) (fs.File, error) {
	var f fs.File
	err := r.execute(path, func(root *os.Root, relPath string) error {
		file, err := root.Open(relPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("failed to open file: file not found: %w", err)
			}
			if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
				strings.Contains(err.Error(), "permission denied") {
				return fmt.Errorf("failed to open file: access denied: %w", err)
			}
			return fmt.Errorf("failed to open file: %w", err)
		}
		f = file
		return nil
	})
	return f, err
}

// whitelistFs wraps a sandboxFs and allows access to specific paths outside
// the workspace when they match any of the provided patterns.
type whitelistFs struct {
	sandbox  *sandboxFs
	host     hostFs
	patterns []*regexp.Regexp
}

func (w *whitelistFs) matches(path string) bool {
	return isAllowedPath(path, w.patterns)
}

func (w *whitelistFs) ReadFile(path string) ([]byte, error) {
	if w.matches(path) {
		return w.host.ReadFile(path)
	}
	return w.sandbox.ReadFile(path)
}

func (w *whitelistFs) WriteFile(path string, data []byte) error {
	if w.matches(path) {
		return w.host.WriteFile(path, data)
	}
	return w.sandbox.WriteFile(path, data)
}

func (w *whitelistFs) ReadDir(path string) ([]os.DirEntry, error) {
	if w.matches(path) {
		return w.host.ReadDir(path)
	}
	return w.sandbox.ReadDir(path)
}

func (w *whitelistFs) Open(path string) (fs.File, error) {
	if w.matches(path) {
		return w.host.Open(path)
	}
	return w.sandbox.Open(path)
}

// buildFs returns the appropriate fileSystem implementation based on restriction
// settings and optional path whitelist patterns.
func buildFs(workspace string, restrict bool, patterns []*regexp.Regexp) fileSystem {
	if !restrict {
		return &hostFs{}
	}
	sandbox := &sandboxFs{workspace: workspace}
	if len(patterns) > 0 {
		return &whitelistFs{sandbox: sandbox, patterns: patterns}
	}
	return sandbox
}

// Helper to get a safe relative path for os.Root usage
func getSafeRelPath(workspace, path string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("workspace is not defined")
	}

	rel := filepath.Clean(path)
	if filepath.IsAbs(rel) {
		var err error
		rel, err = filepath.Rel(workspace, rel)
		if err != nil {
			return "", fmt.Errorf("failed to calculate relative path: %w", err)
		}
	}

	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("path escapes workspace: %s", path)
	}

	return rel, nil
}
