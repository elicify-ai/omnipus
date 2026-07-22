// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package gitevidence

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// normalizeWriteSetPath cleans a caller-declared write-set entry (relative
// to a Repo's dir) and rejects anything that could escape the repo root —
// an absolute path, a leading "..", or a cleaned path that resolves outside
// the root. Returned paths always use "/" separators, matching git's
// internal path convention (object.ChangeEntry.Name, go-git Status keys).
func normalizeWriteSetPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("gitevidence: empty write-set path")
	}
	slashed := filepath.ToSlash(raw)
	if strings.HasPrefix(slashed, "/") {
		return "", fmt.Errorf("gitevidence: write-set path must be relative: %q", raw)
	}
	cleaned := cleanSlashPath(slashed)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("gitevidence: write-set path escapes repo root: %q", raw)
	}
	if cleaned == "." {
		return "", fmt.Errorf("gitevidence: write-set path resolves to the repo root itself: %q", raw)
	}
	if strings.HasPrefix(cleaned, ".git/") || cleaned == ".git" {
		return "", fmt.Errorf("gitevidence: write-set path targets the evidence repo's own .git: %q", raw)
	}
	return cleaned, nil
}

// expandedFile is one concrete file resolved from a declared write-set
// entry (which may itself have named a directory).
type expandedFile struct {
	// relPath is "/"-separated, relative to the repo root.
	relPath string
	// declaredAs records the original write-set entry that produced this
	// file, so callers can report contention/exclusion against what the
	// caller actually declared.
	declaredAs string
}

// expandWriteSet resolves a caller-declared write-set (files and/or
// directories, relative to root) into a flat, deduplicated, sorted list of
// concrete file paths. A directory entry expands to every regular file
// under it (skipping the evidence repo's own .git); a file entry — even
// one that no longer exists on disk (deleted as part of this attempt) —
// passes through unchanged, since Worktree.Add stages a deletion when the
// path is absent.
func expandWriteSet(root string, entries []string) ([]expandedFile, error) {
	seen := make(map[string]bool, len(entries))
	var out []expandedFile
	for _, raw := range entries {
		clean, err := normalizeWriteSetPath(raw)
		if err != nil {
			return nil, err
		}
		abs := filepath.Join(root, filepath.FromSlash(clean))
		fi, statErr := os.Lstat(abs)
		if statErr == nil && fi.IsDir() {
			walkErr := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() {
					if d.Name() == ".git" {
						return filepath.SkipDir
					}
					return nil
				}
				rel, relErr := filepath.Rel(root, p)
				if relErr != nil {
					return fmt.Errorf("gitevidence: relativize %s: %w", p, relErr)
				}
				relSlash := filepath.ToSlash(rel)
				if !seen[relSlash] {
					seen[relSlash] = true
					out = append(out, expandedFile{relPath: relSlash, declaredAs: raw})
				}
				return nil
			})
			if walkErr != nil {
				return nil, fmt.Errorf("gitevidence: walk write-set directory %q: %w", raw, walkErr)
			}
			continue
		}
		if statErr != nil && !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("gitevidence: stat write-set entry %q: %w", raw, statErr)
		}
		// Either a plain file, or a path that doesn't exist (a pending
		// deletion) — pass through as a single file entry either way.
		if !seen[clean] {
			seen[clean] = true
			out = append(out, expandedFile{relPath: clean, declaredAs: raw})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].relPath < out[j].relPath })
	return out, nil
}

// pathMatches reports whether name (a "/"-separated repo-relative path)
// falls within any of the given write-set path prefixes. An empty prefix
// list matches everything (an unscoped diff).
func pathMatches(name string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, raw := range prefixes {
		p, err := normalizeWriteSetPath(raw)
		if err != nil {
			continue
		}
		if name == p {
			return true
		}
		if strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	return false
}

// cleanSlashPath is filepath.Clean applied to a "/"-separated path,
// independent of GOOS path-separator conventions (git paths are always
// "/"-separated regardless of host OS).
func cleanSlashPath(p string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
}
