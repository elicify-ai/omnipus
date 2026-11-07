// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// maxLinkHops bounds the symlink chain (r17 §3.5).
const maxLinkHops = 40

// ValidateProgramPath resolves enteredPath and applies every check of r17
// §3.5. It returns the fully resolved final file, which is the file the
// caller must exec; err is a *CheckError.
//
// Agent-writable locations are $OMNIPUS_HOME (every workspace root lives
// under it) plus agentWritable, which callers build with AgentWritableRoots.
// A leading "~" in an entry is expanded. File ownership and permission bits
// are not checked (founder Q2b, Q13): protecting the program is its owner's
// job. Callers holding AgentWritableRoots' error use CheckProgramPath.
func ValidateProgramPath(cli, enteredPath string, agentWritable ...string) (resolvedPath string, err error) {
	return checkProgramPath(cli, enteredPath, agentWritable, nil)
}

// CheckProgramPath is ValidateProgramPath over a list read with
// AgentWritableRoots together with its error. A non-nil rootsErr means the
// list is incomplete, so after the name checks the path is refused as
// CheckLocationUnverifiable and the cause is logged: an unchecked location is
// never accepted, and it is not reported as an agent-writable one.
func CheckProgramPath(cli, enteredPath string, agentWritable []string, rootsErr error) (resolvedPath string, err error) {
	return checkProgramPath(cli, enteredPath, agentWritable, rootsErr)
}

func checkProgramPath(cli, enteredPath string, agentWritable []string, rootsErr error) (resolvedPath string, err error) {
	fail := func(check, at string) (string, error) {
		return "", &CheckError{CLI: cli, Check: check, At: at}
	}
	if !isAbsolutePath(enteredPath) {
		return fail(CheckNotAbsolute, enteredPath)
	}
	if check := windowsNameCheck(enteredPath); check != "" {
		return fail(check, enteredPath)
	}
	if rootsErr != nil {
		slog.Error("third-party program check: the location cannot be checked, so the program is refused",
			"cli", cli, "path", enteredPath, "error", rootsErr)
		return fail(CheckLocationUnverifiable, enteredPath)
	}
	roots := writableRoots(agentWritable)
	final, hops, cerr := resolveChain(cli, enteredPath, roots)
	if cerr != nil {
		return "", cerr
	}
	fi, statErr := os.Lstat(final)
	if statErr != nil {
		return fail(CheckNotFound, final)
	}
	check := ""
	if hops > 0 {
		check = windowsNameCheck(final)
	}
	if check == "" {
		check = finalFileCheck(final, fi, roots)
	}
	if check != "" {
		return fail(check, final)
	}
	return final, nil
}

// resolveChain unfolds every symlink on the way to enteredPath's final file —
// directory components as well as the last one — and returns that file's
// path with no symlink left in it, plus the number of links followed. Each
// link's containing folder (already resolved) gets the link-folder rule of
// r17 §3.5 before the link is followed; relative targets resolve against that
// folder; at most maxLinkHops links. A missing element is not_found at the
// element, or at the last link followed when that link's target is missing.
func resolveChain(cli, enteredPath string, roots []string) (string, int, error) {
	fail := func(check, at string) (string, int, error) {
		return "", 0, &CheckError{CLI: cli, Check: check, At: at}
	}
	resolved, pending := splitPath(enteredPath)
	// fromLink counts the pending components that came from link targets,
	// so a missing one is reported at the link that named it.
	hops, lastLink, fromLink := 0, "", 0
	for len(pending) > 0 {
		comp := pending[0]
		pending = pending[1:]
		inTarget := fromLink > 0
		if inTarget {
			fromLink--
		}
		if comp == ".." {
			resolved = filepath.Dir(resolved)
			continue
		}
		cur := filepath.Join(resolved, comp)
		fi, err := os.Lstat(cur)
		if err != nil {
			if inTarget {
				return fail(CheckNotFound, lastLink)
			}
			return fail(CheckNotFound, cur)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			resolved = cur
			continue
		}
		if insideAny(resolved, roots) {
			return fail(CheckAgentWritableLocation, cur)
		}
		if hops >= maxLinkHops {
			return fail(CheckNotFound, cur)
		}
		target, err := os.Readlink(cur)
		if err != nil {
			return fail(CheckNotFound, cur)
		}
		hops++
		lastLink = cur
		targetRoot, targetParts := splitPath(target)
		if targetRoot != "" {
			// Absolute, or (Windows) rooted on the current drive.
			// A drive-relative target ("D:foo") names no fixed file.
			if filepath.VolumeName(targetRoot) == "" {
				targetRoot = filepath.VolumeName(resolved) + targetRoot
			} else if !filepath.IsAbs(targetRoot) {
				return fail(CheckNotFound, cur)
			}
			resolved = targetRoot
		}
		fromLink += len(targetParts)
		pending = append(targetParts, pending...)
	}
	return resolved, hops, nil
}

// isSeparator reports a path separator rune (only ASCII runes can be one).
func isSeparator(r rune) bool { return r < utf8.RuneSelf && os.IsPathSeparator(uint8(r)) }

// splitPath returns p's root (volume plus separator, for an absolute path)
// and its components in order, without "" and "." entries. ".." stays a
// component: it is applied only after the component before it is resolved.
func splitPath(p string) (root string, parts []string) {
	vol := filepath.VolumeName(p)
	rest := p[len(vol):]
	if rest != "" && os.IsPathSeparator(rest[0]) {
		root = vol + string(filepath.Separator)
	} else {
		root = vol
	}
	for _, part := range strings.FieldsFunc(rest, isSeparator) {
		if part != "." {
			parts = append(parts, part)
		}
	}
	return root, parts
}

// finalFileCheck applies the final-file rows of r17 §3.5 in their stated order
// and returns the failing check name or "".
func finalFileCheck(path string, fi os.FileInfo, roots []string) string {
	switch {
	case !fi.Mode().IsRegular():
		return CheckNotRegularFile
	case canExecute(path) != nil:
		return CheckNotExecutable
	case insideAny(path, roots):
		return CheckAgentWritableLocation
	}
	return ""
}

// writableRoots returns $OMNIPUS_HOME plus the supplied agent-writable
// locations, each in its lexical and symlink-resolved form.
func writableRoots(extra []string) []string {
	candidates := append([]string{config.OmnipusHomeDir()}, extra...)
	home, _ := os.UserHomeDir()
	out := make([]string, 0, 2*len(candidates))
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if home != "" && (c == "~" || strings.HasPrefix(c, "~/")) {
			c = filepath.Join(home, strings.TrimPrefix(c, "~"))
		}
		if !filepath.IsAbs(c) {
			continue
		}
		out = append(out, filepath.Clean(c))
		if resolved, err := filepath.EvalSymlinks(c); err == nil {
			out = append(out, resolved)
		}
	}
	return out
}

// insideAny reports whether path (lexical or with its folders' symlinks
// resolved) is one of roots or below one.
func insideAny(path string, roots []string) bool {
	forms := []string{filepath.Clean(path)}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		forms = append(forms, resolved)
	} else if dir, derr := filepath.EvalSymlinks(filepath.Dir(path)); derr == nil {
		forms = append(forms, filepath.Join(dir, filepath.Base(path)))
	}
	for _, f := range forms {
		for _, r := range roots {
			if within(f, r) {
				return true
			}
		}
	}
	return false
}

// within reports whether p equals root or lies below it; case-insensitive on
// Windows.
func within(p, root string) bool {
	if runtime.GOOS == "windows" {
		p, root = strings.ToLower(p), strings.ToLower(root)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
