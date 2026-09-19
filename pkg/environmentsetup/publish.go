// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package environmentsetup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// digestTree computes a canonical sha256 over the tree at root, excluding the
// named marker file (written after digesting). WalkDir visits entries in
// sorted order, so the digest does not depend on directory order. Regular
// files contribute path, size and bytes; symlinks contribute path and link
// target and are never followed (installed trees legitimately contain
// symlinks, e.g. venv interpreter links); directories contribute their path.
// Any other file type is rejected rather than published.
func digestTree(root, exclude string) (string, int, error) {
	h := sha256.New()
	content := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !pathWithin(root, path) {
			return fmt.Errorf("%s escapes %s", path, root)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch rel {
		case ".":
			return nil
		case exclude:
			return nil
		}
		fmt.Fprint(h, len(rel), ":", rel, ":")
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			content++
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read link %s: %w", path, err)
			}
			fmt.Fprint(h, "link:", len(target), ":", target)
		case d.IsDir():
			fmt.Fprint(h, "dir")
		case d.Type().IsRegular():
			content++
			info, err := d.Info()
			if err != nil {
				return err
			}
			fmt.Fprint(h, "file:", info.Size(), ":")
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("unsupported file type at %s: %s", path, d.Type())
		}
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), content, nil
}

// lockDownReadOnly makes a published tree read/execute-only: directories
// 0555, executables 0555, other files 0444. Symlinks are skipped entirely —
// chmod through a symlink would touch its target, which can live outside the
// tree. Windows silently ignores these bits (its ACL model differs); the
// sandbox rules derived from the read-side views remain the enforcement layer
// there.
func lockDownReadOnly(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if runtime.GOOS == "windows" || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Chmod(path, 0o555)
		case info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0:
			return os.Chmod(path, 0o555)
		case info.Mode().IsRegular():
			return os.Chmod(path, 0o444)
		}
		return nil
	})
}

// restoreOwnerWrites is the inverse of lockDownReadOnly: it makes an
// unpublished tree owner-writable again (directories 0755, executables 0755,
// files 0644) so Abort's RemoveAll can actually delete it — os.RemoveAll
// cannot unlink children of 0555 directories for an ordinary owner. Like
// lockDownReadOnly it never follows symlinks (WalkDir visits the link itself,
// never its target) and is skipped entirely on Windows.
func restoreOwnerWrites(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if runtime.GOOS == "windows" || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.Chmod(path, 0o755)
		case info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0:
			return os.Chmod(path, 0o755)
		case info.Mode().IsRegular():
			return os.Chmod(path, 0o644)
		}
		return nil
	})
}

// SharedPublished reads the published shared generations in PATH precedence
// order (newest first). Entries whose marker is corrupt, mismatched, absent
// or unreadable — including a marker symlink leading outside the store, which
// the root-confined read refuses — are returned in broken (truthful,
// non-fatal) instead of the valid list: their integrity cannot be verified,
// so they are never granted, but a broken optional shared installation must
// not fail unrelated turns.
func SharedPublished(dataRoot string) (valid []SharedGeneration, broken []string, err error) {
	store, absent, err := resolveStoreDir(dataRoot)
	if err != nil || absent {
		return nil, nil, err
	}
	set, ok, err := readPublishedSet(store)
	if err != nil || !ok {
		return nil, nil, err
	}
	root, err := openConfinedRoot("shared store", store)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	for _, id := range set.Generations {
		dir := filepath.Join(store, id)
		gen := SharedGeneration{ID: id, Dir: dir}
		data, readErr := readControlFile(root, id+"/"+markerFileName)
		if readErr != nil {
			broken = append(broken, id)
			continue
		}
		var marker installMarker
		if jsonErr := json.Unmarshal(data, &marker); jsonErr != nil {
			broken = append(broken, id)
			continue
		}
		if marker.Generation != id || marker.Digest == "" {
			broken = append(broken, id)
			continue
		}
		gen.Digest = marker.Digest
		gen.PublishedAt, _ = time.Parse(time.RFC3339, marker.PublishedAt)
		gen.Platform = marker.Platform
		valid = append(valid, gen)
	}
	return valid, broken, nil
}

// RuntimeEnvPaths resolves the per-turn runtime view for one workspace.
// Existence-tolerant and cheap (one set-file read plus a few stats): a
// workspace without an env subtree and a store that was never published both
// yield an empty view, not an error. An empty workspaceRoot yields the
// shared-only view. There are no agent or role inputs — policy visibility is
// the caller's concern. A workspace env subtree that is a symlink leading
// outside the workspace is an error, never a grant.
func RuntimeEnvPaths(appDataRoot, workspaceRoot string) (RuntimeEnv, error) {
	env := RuntimeEnv{}
	if workspaceRoot != "" {
		if err := addWorkspaceView(&env, workspaceRoot); err != nil {
			return RuntimeEnv{}, err
		}
	}
	valid, broken, err := SharedPublished(appDataRoot)
	if err != nil {
		return RuntimeEnv{}, err
	}
	env.Broken = broken
	for _, gen := range valid {
		env.Shared = append(env.Shared, gen)
		if _, statErr := os.Stat(gen.Dir); statErr != nil {
			continue
		}
		env.ReadExec = append(env.ReadExec, gen.Dir)
		env.BinDirs = append(env.BinDirs, binDirsUnder(gen.Dir)...)
	}
	return env, nil
}

// addWorkspaceView fills the workspace half of a RuntimeEnv when the
// workspace has an env subtree. A missing workspace root (deleted between
// authorization and this call) is tolerated as "no env", not an error. The
// env subtree itself is inspected through a root-confined handle on the
// resolved workspace root: an env that is really a symlink out of the
// workspace fails with a clear error instead of yielding grants outside.
func addWorkspaceView(env *RuntimeEnv, workspaceRoot string) error {
	if err := validateCleanAbsPath("workspace root", workspaceRoot); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		if fsErrorIsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	root, err := openConfinedRoot("workspace root", resolved)
	if err != nil {
		if fsErrorIsNotExist(err) {
			return nil // raced away between resolve and open: no env
		}
		return fmt.Errorf("open workspace root: %w", err)
	}
	defer root.Close()
	envRoot := filepath.Join(resolved, filepath.FromSlash(workspaceEnvRelative))
	info, err := root.Stat(workspaceEnvRelative)
	if err != nil {
		if fsErrorIsNotExist(err) {
			return nil
		}
		return fmt.Errorf("workspace env subtree is not accessible inside the workspace: %w", err)
	}
	if !info.IsDir() {
		return nil
	}
	env.WorkspacePrefix = envRoot
	env.WorkspaceCache = WorkspaceCacheDir(envRoot)
	env.WorkspaceTmp = WorkspaceTmpDir(envRoot)
	env.ReadExec = append(env.ReadExec, envRoot)
	env.Writable = append(env.Writable, env.WorkspaceCache, env.WorkspaceTmp)
	env.BinDirs = append(env.BinDirs, binDirsUnder(envRoot)...)
	return nil
}

// binDirsUnder lists dir's existing conventional executable subdirectories in
// platform preference order. Only real directories qualify: a symlinked
// <prefix>/bin (its target may lie anywhere) must never become a PATH entry.
func binDirsUnder(dir string) []string {
	var out []string
	for _, name := range binSubdirs() {
		info, err := os.Lstat(filepath.Join(dir, name))
		if err == nil && info.IsDir() && info.Mode()&fs.ModeSymlink == 0 {
			out = append(out, filepath.Join(dir, name))
		}
	}
	return out
}

// pathWithin reports whether path is root itself or inside root (lexically).
func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveStoreDir checks — through a confined handle on the resolved data
// root — that the shared store lives INSIDE the authorized data root, so
// readers and writers agree on the same confined location and no read-side
// grant can reach an escaped store through a planted ancestor symlink. A
// not-yet-existing store (or data root) is `absent` (no error): optional
// shared environments must not fail unrelated turns. A store that exists but
// is NOT reachable inside the data root is an error, never a grant.
func resolveStoreDir(dataRoot string) (store string, absent bool, err error) {
	resolved, err := resolveExistingDir("data root", dataRoot)
	if err != nil {
		if fsErrorIsNotExist(err) {
			return "", true, nil // no data root yet: nothing published
		}
		return "", false, err
	}
	root, err := openConfinedRoot("data root", resolved)
	if err != nil {
		if fsErrorIsNotExist(err) {
			return "", true, nil
		}
		return "", false, err
	}
	defer root.Close()
	if _, err := root.Stat(storeRelative); err != nil {
		if fsErrorIsNotExist(err) {
			return "", true, nil // no store yet: nothing published
		}
		return "", false, fmt.Errorf("shared store is not accessible inside the data root: %w", err)
	}
	return filepath.Join(resolved, filepath.FromSlash(storeRelative)), false, nil
}
