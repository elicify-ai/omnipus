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

	"github.com/elicify-ai/omnipus/pkg/config"
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
//
// policy is carried on the handle so the root==nil (host-fs) branch of every
// I/O method below can re-verify FR-017's carve-out protection AT I/O TIME,
// not merely at the earlier ResolvePath resolve — see
// recheckUnrestrictedCarveOut (HIGH #3, ADR-046 P1 review). The root!=nil
// (confined) branch never consults it — os.Root's own re-resolution already
// closes that TOCTOU gap for the confined case, and IsCarveOut was already
// checked unconditionally by ResolvePath before the handle was constructed.
type PathHandle struct {
	root   *os.Root
	rel    string
	abs    string
	policy fspolicy.FSPolicy
}

// recheckUnrestrictedCarveOut re-resolves h.abs (following any symlink that
// may have been swapped in since ResolvePath first resolved it) and re-runs
// fspolicy.IsCarveOut against it, refusing with ErrCarveOut if the target now
// falls under a carve-out root. Only meaningful — and only called — on a
// host-fs (root==nil) handle: os.Root-backed (confined) I/O already
// re-resolves and re-enforces containment at the syscall boundary on every
// call, but a root==nil handle's methods do raw os.* I/O directly against
// h.abs, a string that was resolved once, at ResolvePath time, and never
// re-checked again before this fix (HIGH #3, ADR-046 P1 review) — exactly
// the CWE-357 TOCTOU shape ResolvePath's package doc otherwise claims to
// close. This does not close the general host-fs TOCTOU (a swap between
// this re-check and the os.* call immediately below it is still possible;
// that residual is inherent to string-based host filesystem I/O and is
// P3's job, via a per-child Landlock ruleset, to close for real) — it
// specifically re-verifies the ONE property FR-017 requires unconditionally
// regardless of scope: an agent must never reach master.key/
// credentials.json/another agent's home/another workspace, even under
// FSScopeUnrestricted.
func (h *PathHandle) recheckUnrestrictedCarveOut() error {
	current, err := resolveRealpathUnderWorkDir(h.abs, "")
	if err != nil {
		return fmt.Errorf("resolvepath: re-resolve %q at I/O time: %w", h.abs, err)
	}
	if fspolicy.IsCarveOut(current, h.policy) {
		return ErrCarveOut
	}
	return nil
}

// ReadFile reads the handle's target in full.
func (h *PathHandle) ReadFile() ([]byte, error) {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
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
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return err
		}
		return writeFileAtomicHost(h.abs, data)
	}
	return writeFileAtomicRoot(h.root, h.rel, data)
}

// ReadDir lists the handle's target directory.
func (h *PathHandle) ReadDir() ([]os.DirEntry, error) {
	if h.root == nil {
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
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
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
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
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return err
		}
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
		if err := h.recheckUnrestrictedCarveOut(); err != nil {
			return nil, err
		}
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
//  0. Validate policy's own structural invariants (BLOCK #1, ADR-046 P1
//     review) — a zero-value or otherwise malformed FSPolicy (e.g. an empty
//     WorkDir, or a WorkDir sitting at/above one of its own CarveOuts) is
//     refused with ErrPathInvalid before any access decision is made.
//  1. Reject an embedded NUL byte or any hard (non-"not exist") resolution
//     failure with ErrPathInvalid — before any policy decision.
//  2. Check fspolicy.IsCarveOut on the resolved realpath, UNCONDITIONALLY —
//     even under fspolicy.FSScopeUnrestricted an agent must never reach
//     master.key/credentials.json/another agent's home/another workspace
//     (FR-017).
//  3. If the resolved realpath falls outside policy.WorkDir (a leading ".."
//     escape, a mid-string ".." reentry, an absolute path elsewhere, or a
//     symlink that resolves outside), dispatch on the effective scope: under
//     fspolicy.FSScopeUnrestricted a legacy host-fs PathHandle (root==nil) is
//     returned; under fspolicy.FSScopeConfined it is refused with
//     ErrOutsideScope; fspolicy.FSScopeAsk/FSScopeAllow are P2 seams this
//     package does not implement yet, so they are refused with
//     ErrPathInvalid rather than silently falling through to some invented
//     default (Constraint #6).
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

	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathInvalid, err)
	}

	realAbs, err := resolveRealpathUnderWorkDir(rawPath, policy.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPathInvalid, err)
	}

	if fspolicy.IsCarveOut(realAbs, policy) {
		return nil, ErrCarveOut
	}

	if !isWithinWorkspace(realAbs, policy.WorkDir) {
		switch policy.Scope {
		case fspolicy.FSScopeConfined:
			return nil, fmt.Errorf("%w: %q resolves to %q, outside the effective working directory %q",
				ErrOutsideScope, rawPath, realAbs, policy.WorkDir)
		case fspolicy.FSScopeUnrestricted:
			return &PathHandle{abs: realAbs, policy: policy}, nil
		case fspolicy.FSScopeAsk, fspolicy.FSScopeAllow:
			return nil, fmt.Errorf("%w: filesystem_scope %q is not yet supported by ResolvePath (P2)",
				ErrPathInvalid, policy.Scope)
		default:
			return nil, fmt.Errorf("resolvepath: internal error: unknown filesystem scope %q", policy.Scope)
		}
	}

	rel, err := safeRelPath(policy.WorkDir, rawPath)
	if err != nil {
		return nil, err
	}

	root, err := os.OpenRoot(policy.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("resolvepath: open working directory root %q: %w", policy.WorkDir, err)
	}

	return &PathHandle{root: root, rel: rel, abs: realAbs, policy: policy}, nil
}

// ResolveTurnFSPolicy resolves the single, authoritative FSPolicy for a turn
// (FR-036), using the exact parameter shape every generic path-taking tool's
// Execute method previously assembled by hand (MEDIUM #7, ADR-046 P1
// review): WorkDir prefers the per-turn Workspace re-root (TurnWorkspaceDir)
// when the agent is a Workspace CoreTeam member, else falls back to
// agentHome; agent/workspace identity and $OMNIPUS_HOME come from ctx and
// config.OmnipusHomeDir() respectively. Centralizing this removes what were
// 9 hand-duplicated call sites (filesystem.go x3, edit.go x2, send_file.go,
// web_serve.go x2, browser/tools.go) — each a chance for the shape to drift.
func ResolveTurnFSPolicy(ctx context.Context, agentHome string, restrict bool) (fspolicy.FSPolicy, error) {
	return fspolicy.EffectiveFSPolicy(
		ctx, agentHome, TurnWorkspaceDir(ctx), restrict,
		config.OmnipusHomeDir(), ToolAgentID(ctx), ToolWorkspaceID(ctx),
	)
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

// wrapFSErr normalizes a ReadFile/Open-family error the same way the
// pre-ADR-046 hostFs/sandboxFs implementations did, so existing callers'
// substring checks ("file not found", "access denied") keep matching. verb
// is the human-readable action ("read" or "open") that appears in the
// message (MEDIUM #8, ADR-046 P1 review: wrapReadErr and wrapOpenErr were
// byte-identical apart from this one word — consolidated into a single
// implementation with the two original names kept as one-line delegators
// below so every existing call site's behavior is unchanged).
func wrapFSErr(verb string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("failed to %s file: file not found: %w", verb, err)
	}
	if os.IsPermission(err) || strings.Contains(err.Error(), "escapes from parent") ||
		strings.Contains(err.Error(), "permission denied") {
		return fmt.Errorf("failed to %s file: access denied: %w", verb, err)
	}
	return fmt.Errorf("failed to %s file: %w", verb, err)
}

// wrapReadErr normalizes a ReadFile-family error. Delegates to wrapFSErr.
func wrapReadErr(err error) error {
	return wrapFSErr("read", err)
}

// wrapOpenErr normalizes an Open-family error. Delegates to wrapFSErr.
func wrapOpenErr(err error) error {
	return wrapFSErr("open", err)
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
