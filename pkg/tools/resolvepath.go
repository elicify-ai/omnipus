// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-046 P1 resolver core. ResolvePath is the single,
// mandatory chokepoint every path-taking tool MUST route through (FR-003,
// FR-034): it roots relative paths at the turn's effective working directory
// (FR-004), gates absolute/escaping paths by the effective filesystem_scope
// (FR-005), resolves symlinks and anchors confinement on the realpath
// (FR-006), and — critically — never hands a resolved path back as a bare
// string for a tool to os.Open independently. Instead it returns a
// *PathHandle backed by a Go 1.24 os.Root, so every subsequent I/O operation
// is enforced at the syscall boundary on every call, closing the CWE-357
// TOCTOU gap that validatePathWithAllowPaths' "resolve-then-return-a-string"
// shape left open (BLOCK #1 in the ADR-046 grill).
//
// P1 scope: this file implements confined-vs-unrestricted resolution only
// (fspolicy.FSScopeConfined / fspolicy.FSScopeUnrestricted). The ask/allow
// tri-state (fspolicy.FSScopeAsk / FSScopeAllow) is P2 — toolName/callID/op
// are threaded through the signature for that future ask-flow + audit
// dimension but are NOT consulted for any decision here (Constraint #6: no
// invented default, no early branch on a scope this package doesn't yet
// implement).
package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// FSOp classifies the filesystem operation a ResolvePath caller is about to
// perform. P1 does not branch on it (no read/write policy split — FR-032
// requires a single symmetric tri-state); it is threaded through so the P2
// ask-flow and the FR-035 audit dimension can key off it later without a
// signature change.
type FSOp string

const (
	FSOpRead  FSOp = "read"
	FSOpWrite FSOp = "write"
	FSOpList  FSOp = "list"
	FSOpExec  FSOp = "exec"
	FSOpServe FSOp = "serve"
)

// PathHandle is the sanctioned I/O handle ResolvePath returns. root is nil
// exactly when the resolved path was granted under fspolicy.FSScopeUnrestricted
// via the legacy host-fs back-compat path (an absolute, or escaping, path that
// the effective scope permits); in that case abs is the resolved absolute
// path and every method operates on it directly via the plain os package,
// matching the pre-ADR-046 unrestricted ("hostFs") behavior. When root is
// non-nil, every method resolves rel underneath it via os.Root, so an
// attacker who swaps a path component between ResolvePath's own realpath
// check and the actual I/O call cannot escape the root — the kernel/runtime
// re-resolves and re-enforces containment at the moment of the syscall, not
// merely at a prior string check (FR-006's TOCTOU-hardness).
type PathHandle struct {
	root *os.Root
	rel  string
	abs  string
}

// ReadFile reads the handle's target in full.
func (h *PathHandle) ReadFile() ([]byte, error) {
	if h.root == nil {
		content, err := os.ReadFile(h.abs)
		if err != nil {
			return nil, wrapReadErr(err)
		}
		return content, nil
	}
	content, err := h.root.ReadFile(h.rel)
	if err != nil {
		return nil, wrapReadErr(err)
	}
	return content, nil
}

// WriteFile writes data to the handle's target atomically (temp file +
// fsync + rename), mirroring the exact atomic-write body the pre-ADR-046
// sandboxFs.WriteFile used for the confined (root-backed) case, and
// fileutil.WriteFileAtomic's contract for the unrestricted (host) case.
func (h *PathHandle) WriteFile(data []byte) error {
	if h.root == nil {
		return writeFileAtomicHost(h.abs, data)
	}
	return writeFileAtomicRoot(h.root, h.rel, data)
}

// ReadDir lists the handle's target directory.
func (h *PathHandle) ReadDir() ([]os.DirEntry, error) {
	if h.root == nil {
		entries, err := os.ReadDir(h.abs)
		if err != nil {
			return nil, fmt.Errorf("failed to read directory: %w", err)
		}
		return entries, nil
	}
	entries, err := fs.ReadDir(h.root.FS(), h.rel)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}
	return entries, nil
}

// Open opens the handle's target for reading. The returned fs.File remains
// valid even after h.Close() runs (os.Root's documented guarantee, already
// relied on by the pre-ADR-046 sandboxFs.Open) — callers may safely
// `defer handle.Close()` alongside `defer file.Close()` in either order.
func (h *PathHandle) Open() (fs.File, error) {
	if h.root == nil {
		f, err := os.Open(h.abs)
		if err != nil {
			return nil, wrapOpenErr(err)
		}
		return f, nil
	}
	f, err := h.root.Open(h.rel)
	if err != nil {
		return nil, wrapOpenErr(err)
	}
	return f, nil
}

// MkdirAll creates the handle's target directory (and any missing parents).
func (h *PathHandle) MkdirAll(perm os.FileMode) error {
	if h.root == nil {
		if err := os.MkdirAll(h.abs, perm); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
		return nil
	}
	if err := h.root.MkdirAll(h.rel, perm); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return nil
}

// Stat returns the handle's target FileInfo.
func (h *PathHandle) Stat() (os.FileInfo, error) {
	if h.root == nil {
		info, err := os.Stat(h.abs)
		if err != nil {
			return nil, fmt.Errorf("failed to stat: %w", err)
		}
		return info, nil
	}
	info, err := h.root.Stat(h.rel)
	if err != nil {
		return nil, fmt.Errorf("failed to stat: %w", err)
	}
	return info, nil
}

// RealPath returns the resolved absolute path ResolvePath computed. This is
// the ONE documented exception to "never hand back a bare string" — it
// exists solely for the OS/library-boundary call sites that have no handle
// parameter to accept (exec.Cmd.Dir, and web_serve's http.Dir directory
// registration in the follow-up defects wave). Treat it as ADVISORY ONLY:
// nothing re-checks confinement between this call and the consumer's own use
// of the string, so it must never be treated as a TOCTOU-safe substitute for
// the handle's own I/O methods above. P3 (ADR-046) closes this gap for exec
// by feeding EffectiveFSPolicy into a per-child Landlock ruleset instead of
// relying on this string.
func (h *PathHandle) RealPath() (string, error) {
	if h == nil {
		return "", fmt.Errorf("resolvepath: nil path handle")
	}
	return h.abs, nil
}

// Close releases the handle's os.Root, when it holds one. Safe to call on a
// host-mode handle (root == nil) or a nil handle — both are no-ops.
func (h *PathHandle) Close() error {
	if h == nil || h.root == nil {
		return nil
	}
	return h.root.Close()
}

// ResolvePath is the single, mandatory path-resolution chokepoint (FR-003).
// rawPath is the caller-supplied (LLM/tool-argument) path; policy is the
// turn's single source-of-record FSPolicy (fspolicy.EffectiveFSPolicy).
// toolName, callID, and op are threaded through for the P2 ask-flow and the
// FR-035 audit dimension but drive NO decision in this P1 implementation.
//
// Resolution order:
//  1. Reject an embedded NUL byte or any hard (non-"not exist") resolution
//     failure with ErrPathInvalid — before any policy decision.
//  2. Check fspolicy.IsCarveOut on the resolved realpath, UNCONDITIONALLY —
//     even under fspolicy.FSScopeUnrestricted an agent must never reach
//     master.key/credentials.json/another agent's home/another workspace
//     (FR-017).
//  3. If the resolved realpath falls outside policy.WorkDir (a leading ".."
//     escape, a mid-string ".." reentry, an absolute path elsewhere, or a
//     symlink that resolves outside), the operation needs
//     fspolicy.FSScopeUnrestricted to proceed — in which case a legacy
//     host-fs PathHandle (root==nil) is returned; under any other scope it
//     is refused with ErrOutsideScope.
//  4. Otherwise (the realpath falls within policy.WorkDir — including an
//     absolute path that simply happens to resolve inside it, matching the
//     pre-ADR-046 sandboxFs/getSafeRelPath contract every existing
//     path-taking tool test relies on) the path is resolved through a fresh
//     os.Root opened at policy.WorkDir, and every subsequent I/O call on the
//     returned handle is enforced at the syscall boundary (FR-006).
func ResolvePath(
	ctx context.Context,
	policy fspolicy.FSPolicy,
	toolName, callID string,
	op FSOp,
	rawPath string,
) (*PathHandle, error) {
	// P2 seam: the ask-flow and audit dimension will consult ctx/toolName/
	// callID/op once filesystem_scope=ask lands. Referenced here only to
	// make the current no-op deliberate rather than silently unused.
	_ = ctx
	_ = toolName
	_ = callID
	_ = op

	realAbs, err := resolveRealpathUnderWorkDir(rawPath, policy.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathInvalid, err)
	}

	if fspolicy.IsCarveOut(realAbs, policy) {
		return nil, ErrCarveOut
	}

	if !isWithinWorkspace(realAbs, policy.WorkDir) {
		if policy.Scope == fspolicy.FSScopeUnrestricted {
			return &PathHandle{abs: realAbs}, nil
		}
		return nil, fmt.Errorf("%w: %q resolves to %q, outside the effective working directory %q",
			ErrOutsideScope, rawPath, realAbs, policy.WorkDir)
	}

	rel, err := safeRelPath(policy.WorkDir, rawPath)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(policy.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("resolvepath: open working directory root %q: %w", policy.WorkDir, err)
	}

	return &PathHandle{root: root, rel: rel, abs: realAbs}, nil
}

// ResolvePathAllowingPatterns bridges the operator-configured AllowRead/
// WritePaths regex axis (isAllowedPath/matchesAllowedPath in filesystem.go —
// a feature orthogonal to filesystem_scope; ADR-046 does not touch it,
// "only rewire its callers") onto ResolvePath, which has no patterns
// parameter of its own by design (FR-003's signature is fixed).
//
// When rawPath (resolved the SAME lexical way validatePathWithAllowPaths did
// — a plain absolute join, deliberately NOT symlink-resolved, so a
// pattern anchored on a symlink alias like allowedDir still matches before
// resolution) matches one of patterns, this re-resolves through ResolvePath
// with the scope forced to fspolicy.FSScopeUnrestricted for this single
// call — mirroring the pre-ADR-046 whitelistFs's "allow-listed path bypasses
// workspace confinement" behavior. Critically, FR-017's carve-out check
// still runs unconditionally either way, because ResolvePath always checks
// it first regardless of scope — an improvement over whitelistFs, which
// bypassed it entirely.
func ResolvePathAllowingPatterns(
	ctx context.Context,
	policy fspolicy.FSPolicy,
	toolName, callID string,
	op FSOp,
	rawPath string,
	patterns []*regexp.Regexp,
) (*PathHandle, error) {
	if len(patterns) > 0 {
		lexicalAbs := rawPath
		if !filepath.IsAbs(lexicalAbs) {
			lexicalAbs = filepath.Join(policy.WorkDir, lexicalAbs)
		}
		lexicalAbs = filepath.Clean(lexicalAbs)
		if isAllowedPath(lexicalAbs, patterns) {
			unrestricted := policy
			unrestricted.Scope = fspolicy.FSScopeUnrestricted
			return ResolvePath(ctx, unrestricted, toolName, callID, op, rawPath)
		}
	}
	return ResolvePath(ctx, policy, toolName, callID, op, rawPath)
}

// resolveRealpathUnderWorkDir resolves rawPath to an absolute, symlink-
// resolved location, joining it under workDir first when rawPath is
// relative (FR-004). It mirrors fspolicy.realpath's fail-closed
// walk-up-until-found strategy (that function is unexported and
// pkg/fspolicy is a deliberate leaf package pkg/tools must not import back
// into for this) so a not-yet-existing leaf (the common write_file case)
// still resolves through any existing symlinked ancestor, while a genuine
// hard failure (a permission error, ENAMETOOLONG, ...) — as opposed to a
// benign "does not exist yet" — is surfaced rather than silently
// swallowed into a best-effort lexical fallback.
//
// Fails closed on an embedded NUL byte: Go's os/syscall layer would reject
// such a path at the first real syscall anyway (BytePtrFromString), but
// checking it here — before any I/O — makes the rejection deterministic
// and platform-independent rather than depending on exactly which syscall
// happens to touch the string first.
func resolveRealpathUnderWorkDir(rawPath, workDir string) (string, error) {
	if strings.IndexByte(rawPath, 0) != -1 {
		return "", fmt.Errorf("embedded NUL byte in path")
	}

	var abs string
	if filepath.IsAbs(rawPath) {
		abs = filepath.Clean(rawPath)
	} else {
		abs = filepath.Clean(filepath.Join(workDir, rawPath))
	}

	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("resolve symlinks for %q: %w", abs, err)
	}

	dir := filepath.Dir(abs)
	remainder := filepath.Base(abs)
	for {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder)), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve ancestor %q: %w", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no existing ancestor found for %q", abs)
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
		dir = parent
	}
}

// safeRelPath computes the os.Root-relative path for rawPath under workDir,
// the same contract the pre-ADR-046 getSafeRelPath (filesystem.go:1263,
// deleted) provided: an absolute rawPath is made relative to workDir; the
// result must be a "local" path (filepath.IsLocal) — no leading ".." escape
// — or the call is refused. Called only after ResolvePath has already
// established (via the realpath check) that the target lies within workDir,
// so in practice this is a second, lexical, defense-in-depth check on the
// ORIGINAL (not realpath-resolved) rawPath — os.Root re-resolves rel itself
// at I/O time, following any symlinks fresh at that moment (FR-006).
func safeRelPath(workDir, rawPath string) (string, error) {
	rel := filepath.Clean(rawPath)
	if filepath.IsAbs(rel) {
		var err error
		rel, err = filepath.Rel(workDir, rel)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrPathInvalid, err)
		}
	}
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w: %q escapes the working directory", ErrOutsideScope, rawPath)
	}
	return rel, nil
}

// wrapReadErr normalizes a ReadFile-family error the same way the
// pre-ADR-046 hostFs/sandboxFs implementations did, so existing callers'
// substring checks ("file not found", "access denied") keep matching.
func wrapReadErr(err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("failed to read file: file not found: %w", err)
	}
	if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
		strings.Contains(err.Error(), "permission denied") {
		return fmt.Errorf("failed to read file: access denied: %w", err)
	}
	return fmt.Errorf("failed to read file: %w", err)
}

// wrapOpenErr normalizes an Open-family error, mirroring wrapReadErr's
// classification for the "open" verb.
func wrapOpenErr(err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("failed to open file: file not found: %w", err)
	}
	if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
		strings.Contains(err.Error(), "permission denied") {
		return fmt.Errorf("failed to open file: access denied: %w", err)
	}
	return fmt.Errorf("failed to open file: %w", err)
}

// writeFileAtomicHost writes data to abs directly on the host filesystem
// (the fspolicy.FSScopeUnrestricted case), matching the pre-ADR-046 hostFs.
// WriteFile contract exactly (fileutil.WriteFileAtomic: temp file + fsync +
// rename, 0o600).
func writeFileAtomicHost(abs string, data []byte) error {
	return fileutil.WriteFileAtomic(abs, data, 0o600)
}

// writeFileAtomicRoot writes data to relPath underneath root atomically
// (temp file + fsync + rename + directory fsync), porting the EXACT body
// the pre-ADR-046 sandboxFs.WriteFile used — this is the confined,
// os.Root-backed write path FR-006 requires.
func writeFileAtomicRoot(root *os.Root, relPath string, data []byte) error {
	dir := filepath.Dir(relPath)
	if dir != "." && dir != "/" {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent directories: %w", err)
		}
	}

	// Use atomic write pattern with explicit sync for flash storage
	// reliability. Using 0o600 (owner read/write only) for secure default
	// permissions.
	tmpRelPath := fmt.Sprintf(".tmp-%d-%d", os.Getpid(), time.Now().UnixNano())

	tmpFile, err := root.OpenFile(tmpRelPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		if rmErr := root.Remove(tmpRelPath); rmErr != nil {
			logger.WarnCF("filesystem", "failed to remove temp file after write error",
				map[string]any{"error": rmErr.Error()})
		}
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// CRITICAL: Force sync to storage medium before rename. This ensures
	// data is physically written to disk, not just cached.
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		if rmErr := root.Remove(tmpRelPath); rmErr != nil {
			logger.WarnCF("filesystem", "failed to remove temp file after sync error",
				map[string]any{"error": rmErr.Error()})
		}
		return fmt.Errorf("failed to sync temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		if rmErr := root.Remove(tmpRelPath); rmErr != nil {
			logger.WarnCF("filesystem", "failed to remove temp file after close error",
				map[string]any{"error": rmErr.Error()})
		}
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := root.Rename(tmpRelPath, relPath); err != nil {
		if rmErr := root.Remove(tmpRelPath); rmErr != nil {
			logger.WarnCF("filesystem", "failed to remove temp file after rename error",
				map[string]any{"error": rmErr.Error()})
		}
		return fmt.Errorf("failed to rename temp file over target: %w", err)
	}

	// Sync directory to ensure rename is durable.
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
}
