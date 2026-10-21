package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func canonicalLocation(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", refusal("unsafe_path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		if !samePath(path, resolved) {
			return "", refusal("unsafe_path")
		}
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", integrity("io_error", err)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", integrity("io_error", err)
	}
	base, err := canonicalLocation(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(base, filepath.Base(path)), nil
}
func samePath(a, b string) bool {
	// EqualFold also rejects case aliases on Windows without changing the
	// spelling subsequently bound in the native evidence.
	return filepath.Clean(a) == filepath.Clean(b) || nativeCaseInsensitive() && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
func containsPath(root, path string) bool {
	if samePath(root, path) {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func outsideRoots(path string, roots ...string) error {
	for _, root := range roots {
		if root != "" && (containsPath(root, path) || containsPath(path, root)) {
			return refusal("unsafe_path")
		}
	}
	return nil
}
func normalizeRepairPaths(a *repairArgs) error {
	root, err := canonicalLocation(a.root)
	if err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		return integrity("io_error", err)
	}
	if !info.IsDir() {
		return refusal("unsafe_path")
	}
	a.root = root
	if a.backup != "" {
		a.backup, err = canonicalLocation(a.backup)
		if err != nil {
			return err
		}
		if err = outsideRoots(a.backup, a.root); err != nil {
			return err
		}
		info, err = os.Stat(a.backup)
		if errors.Is(err, os.ErrNotExist) {
			return refusal("backup_missing")
		}
		if err != nil {
			return integrity("io_error", err)
		}
		if !info.IsDir() {
			return refusal("unsafe_path")
		}
	}
	for _, p := range []*string{&a.output, &a.planOutput, &a.scratch, &a.manifest, &a.binding, &a.receipt} {
		if *p == "" {
			continue
		}
		*p, err = canonicalLocation(*p)
		if err != nil {
			return err
		}
		if err = outsideRoots(*p, a.root, a.backup); err != nil {
			return err
		}
	}
	if a.executable != "" {
		a.executable, err = canonicalLocation(a.executable)
		if err != nil {
			return err
		}
	}
	if a.output != "" && (samePath(a.output, a.binding) || samePath(a.output, a.receipt) || samePath(a.output, a.manifest)) {
		return refusal("unsafe_path")
	}
	return nil
}
