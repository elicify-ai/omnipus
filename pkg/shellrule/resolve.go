// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package shellrule

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// BinaryResolver resolves a command name — bare ("git") or already
// path-shaped ("./git", "/usr/bin/git") — to its actual, symlink-resolved,
// absolute executable path, searching the given PATH-style list for a bare
// name. Two independent PATH lists matter to ADR-092 D3's look-alike
// defence: the CHILD's effective (and potentially attacker-influenced) PATH
// used to resolve what a segment will actually execute, and a caller-chosen
// TRUSTED PATH used to resolve what an operator rule's Binary field means —
// see ResolveBinary and Verify.
type BinaryResolver func(name, pathList string) (resolved string, err error)

// ResolveBinary is the default BinaryResolver: it mirrors exec.LookPath's
// search order (path-shaped names are resolved directly, bare names are
// searched across pathList in order) but is parameterised on pathList so a
// caller can resolve the same bare name against two different lists and
// compare the results (the look-alike defence). On Windows it additionally
// tries the PATHEXT-style executable extensions for a bare name with no
// extension, matching how the shell itself would resolve it.
func ResolveBinary(name, pathList string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("shellrule: empty binary name")
	}
	if isPathShaped(name) {
		return resolveExecutableAt(name)
	}
	for _, dir := range filepath.SplitList(pathList) {
		if dir == "" {
			continue
		}
		for _, cand := range candidateNames(name) {
			full := filepath.Join(dir, cand)
			if resolved, ok := tryResolveExecutable(full); ok {
				return resolved, nil
			}
		}
	}
	return "", fmt.Errorf("shellrule: %q not found on PATH", name)
}

// isPathShaped reports whether name already names a location rather than a
// bare command to search PATH for.
func isPathShaped(name string) bool {
	if strings.ContainsRune(name, '/') {
		return true
	}
	if strings.HasPrefix(name, "~") {
		return true
	}
	if runtime.GOOS == "windows" && strings.ContainsRune(name, '\\') {
		return true
	}
	return filepath.IsAbs(name)
}

// candidateNames returns the filenames to probe for a bare command name:
// just name on POSIX, and name plus each PATHEXT-style extension on
// Windows when name has no extension of its own already.
func candidateNames(name string) []string {
	if runtime.GOOS != "windows" {
		return []string{name}
	}
	if ext := filepath.Ext(name); ext != "" {
		return []string{name}
	}
	exts := strings.Split(os.Getenv("PATHEXT"), string(filepath.ListSeparator))
	if len(exts) == 0 || (len(exts) == 1 && exts[0] == "") {
		exts = []string{".COM", ".EXE", ".BAT", ".CMD"}
	}
	out := make([]string, 0, len(exts)+1)
	for _, e := range exts {
		if e == "" {
			continue
		}
		out = append(out, name+e)
	}
	out = append(out, name)
	return out
}

// resolveExecutableAt resolves an already path-shaped name (relative or
// absolute) to its absolute, symlink-resolved form, verifying it exists and
// is executable.
func resolveExecutableAt(name string) (string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return "", fmt.Errorf("shellrule: resolving %q: %w", name, err)
	}
	if resolved, ok := tryResolveExecutable(abs); ok {
		return resolved, nil
	}
	return "", fmt.Errorf("shellrule: %q is not an executable file", name)
}

// tryResolveExecutable stats full, resolves symlinks, and confirms it is a
// regular, executable file. It never returns an error — a miss is reported
// via the boolean so ResolveBinary's PATH search loop can keep trying the
// next directory.
func tryResolveExecutable(full string) (string, bool) {
	info, err := os.Lstat(full)
	if err != nil {
		return "", false
	}
	resolved := full
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(full)
		if err != nil {
			return "", false
		}
		resolved = target
		info, err = os.Stat(resolved)
		if err != nil {
			return "", false
		}
	}
	if info.IsDir() {
		return "", false
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", false
	}
	return resolved, true
}

// Verify re-resolves head against pathList and reports whether it still
// resolves to expected — the immediate-before-spawn re-check ADR-092 D3
// requires ("re-verifies that resolution immediately before spawn"). A
// mismatch means the PATH/filesystem changed between the earlier decision
// and now (a TOCTOU window the kernel sandbox, not this package, is the
// actual boundary for); Verify only detects it.
func Verify(head, pathList, expected string, resolve BinaryResolver) (bool, string, error) {
	resolved, err := resolve(head, pathList)
	if err != nil {
		return false, "", err
	}
	return resolved == expected, resolved, nil
}
