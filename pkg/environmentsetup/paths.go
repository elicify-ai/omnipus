// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Reserved workspace layout (ADR-090 environment-setup spec, ES-FR-03).
// .omnipus is the reserved metadata dir; everything a setup writes in
// workspace scope lives under .omnipus/env and nowhere else, so the setup
// child's sandbox write rule can be exactly that subtree. The prefix lifetime
// lock sits directly in .omnipus — reserved metadata, but OUTSIDE the
// installer-writable prefix — so a generic installer that clears and
// recreates its prefix cannot unlink the lock while it runs.
const (
	workspaceMetaRelative = ".omnipus"
	workspaceEnvRelative  = workspaceMetaRelative + "/env"
)

// WorkspaceEnvRoot returns the reserved workspace env subtree for an
// authorized workspace root, resolving symlinks on the workspace root itself
// (a canonical root symlink is allowed) so grants and checks agree on the
// real location. The workspace root must be an existing directory.
func WorkspaceEnvRoot(workspaceRoot string) (string, error) {
	root, err := resolveExistingDir("workspace root", workspaceRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(workspaceEnvRelative)), nil
}

// WorkspaceEnvPaths returns the sandbox-relevant shape of the workspace env
// subtree: the whole subtree is the writable root; there are no agent-managed
// read/execute-only parts here (agents already hold workspace access).
func WorkspaceEnvPaths(workspaceRoot string) (Paths, error) {
	root, err := WorkspaceEnvRoot(workspaceRoot)
	if err != nil {
		return Paths{}, err
	}
	return Paths{Root: root, Writable: []string{root}}, nil
}

// WorkspaceCacheDir is the workspace env's writable cache directory. Setup
// children get it via EnvVarCache; ordinary turns get it as writable so
// installed tools can cache without touching anything else in the workspace.
func WorkspaceCacheDir(envRoot string) string { return filepath.Join(envRoot, "cache") }

// WorkspaceTmpDir is the workspace env's writable temp directory.
func WorkspaceTmpDir(envRoot string) string { return filepath.Join(envRoot, "tmp") }

// openConfinedRoot opens a root-confined os.Root handle on an existing,
// resolved directory. Every creation and read beneath an authorized root
// (workspace root, shared store) is rooted here, so a pre-existing symlink
// below the root is refused instead of being followed out of the authorized
// area.
func openConfinedRoot(what, dir string) (*os.Root, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", what, err)
	}
	return root, nil
}

// mkdirConfined creates rel (with parents) beneath root. os.Root refuses to
// traverse a symlink whose target lies outside root, so a planted
// <ws>/.omnipus -> /outside link cannot redirect creation.
func mkdirConfined(root *os.Root, what, rel string) error {
	if err := root.MkdirAll(rel, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", what, err)
	}
	return nil
}

// readFileLimited reads an open control-file handle size-capped. A file
// larger than the cap is an error, never a silent truncation: cap+1 bytes
// are attempted so oversize is detected, not lost (spec: reject oversize
// input without silently truncating it).
func readFileLimited(f *os.File) ([]byte, error) {
	const capBytes = 64 << 10
	data, err := io.ReadAll(io.LimitReader(f, capBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > capBytes {
		return nil, fmt.Errorf("control file exceeds %d byte limit (%d bytes)", capBytes, len(data))
	}
	return data, nil
}

// readControlFile opens rel beneath root and reads it size-capped. Like all
// root-confined control-file reads, a symlinked metadata file is refused
// rather than followed outside root, so a planted <store>/published.json ->
// /outside link cannot feed outside content into publication decisions.
func readControlFile(root *os.Root, rel string) ([]byte, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return readFileLimited(f)
}

// resolveExistingDir validates dir as a clean absolute path with no ".."
// segments and resolves it to its existing real location (following
// symlinks). Anything else is an error — destinations are resolved from
// authenticated context, never from caller-supplied host paths (ES-FR-02).
func resolveExistingDir(what, dir string) (string, error) {
	if err := validateCleanAbsPath(what, dir); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", what, err)
	}
	// Re-validate the resolved path so a symlink cannot smuggle in a
	// non-canonical form.
	if err := validateCleanAbsPath(what, real); err != nil {
		return "", fmt.Errorf("resolve %s: %w", what, err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("%s: %w", what, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", what)
	}
	return real, nil
}

// validateCleanAbsPath enforces the structural properties of an absolute
// destination: absolute, cleaned, no ".." segments. Authority was already
// checked by the tool lane; this prevents forged or malformed paths from
// reaching destination math.
func validateCleanAbsPath(what, path string) error {
	if path == "" {
		return fmt.Errorf("%s is required", what)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s %q must be an absolute path", what, path)
	}
	clean := filepath.Clean(path)
	if clean != path {
		return fmt.Errorf("%s %q is not a clean absolute path", what, path)
	}
	for seg := range strings.SplitSeq(clean, string(filepath.Separator)) {
		if seg == ".." {
			return fmt.Errorf("%s %q must not contain %q segments", what, path, "..")
		}
	}
	return nil
}

// fsErrorIsNotExist reports whether err is a filesystem "does not exist"
// condition — the only state the readers in this package treat as "absent".
func fsErrorIsNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
