// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
// Agent-writable locations are $OMNIPUS_HOME plus agentWritable, which
// callers build with AgentWritableRoots (that list already has "~" expanded
// and every entry made absolute; a relative entry given here is made
// absolute against the working directory). File ownership and permission
// bits are not checked (founder Q2b, Q13): protecting the program is its
// owner's job. Callers holding AgentWritableRoots' error use
// CheckProgramPath.
func ValidateProgramPath(cli, enteredPath string, agentWritable ...string) (resolvedPath string, err error) {
	return checkProgramPath(cli, enteredPath, agentWritable, nil)
}

// CheckProgramPath is ValidateProgramPath over a list read with
// AgentWritableRoots together with its error. A non-nil rootsErr means the
// list is incomplete, so after the name checks the path is refused as
// CheckLocationUnverifiable and the cause is logged: an unchecked location is
// never accepted, and it is not reported as an agent-writable one. A listed
// folder that exists but cannot be examined is refused the same way.
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
		return failUnverifiable(cli, enteredPath, rootsErr)
	}
	roots, rootErr := expandRoots(append([]string{config.OmnipusHomeDir()}, agentWritable...))
	if rootErr != nil {
		return failUnverifiable(cli, enteredPath, rootErr)
	}
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
		check = finalFileCheck(cli, final, fi, roots)
	}
	if check != "" {
		return fail(check, final)
	}
	return final, nil
}

// failUnverifiable refuses enteredPath as CheckLocationUnverifiable and logs
// the cause once: the list of folders agents can write to is incomplete, or
// one of them cannot be examined, so the location cannot be checked.
func failUnverifiable(cli, enteredPath string, cause error) (string, error) {
	slog.Error("third-party program check: the location cannot be checked, so the program is refused",
		"cli", cli, "path", enteredPath, "error", cause)
	return "", &CheckError{CLI: cli, Check: CheckLocationUnverifiable, At: enteredPath}
}

// resolveChain unfolds every symlink on the way to enteredPath's final file —
// directory components as well as the last one — and returns that file's
// path with no symlink left in it, plus the number of links followed. Before
// a link is followed, the link itself and its containing folder (already
// resolved) must lie outside every agent-writable root (r17 §3.5; a link that
// is itself a root is caught too); relative targets resolve against that
// folder; at most maxLinkHops links. A missing element is not_found at the
// element, or at the last link followed when that link's target is missing.
func resolveChain(cli, enteredPath string, roots []forbiddenRoot) (string, int, error) {
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
		if check := linkLocationCheck(cli, cur, resolved, roots); check != "" {
			return fail(check, cur)
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

// linkLocationCheck applies the link rows of r17 §3.5 to the link at link,
// whose containing folder is dir: neither may lie inside an agent-writable
// root. It returns the failing check name or "".
func linkLocationCheck(cli, link, dir string, roots []forbiddenRoot) string {
	for _, p := range []string{link, dir} {
		if check := locationCheck(cli, p, roots); check != "" {
			return check
		}
	}
	return ""
}

// locationCheck returns CheckAgentWritableLocation when p lies inside an
// agent-writable root, or "". A path element that disappears while it is
// examined is CheckNotFound, never "outside"; the cause is logged.
func locationCheck(cli, p string, roots []forbiddenRoot) string {
	inside, err := insideAny(p, roots)
	if err != nil {
		slog.Error("third-party program check: a path element could not be examined, so the program is refused",
			"cli", cli, "path", p, "error", err)
		return CheckNotFound
	}
	if inside {
		return CheckAgentWritableLocation
	}
	return ""
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
func finalFileCheck(cli, path string, fi os.FileInfo, roots []forbiddenRoot) string {
	switch {
	case !fi.Mode().IsRegular():
		return CheckNotRegularFile
	case canExecute(path) != nil:
		return CheckNotExecutable
	}
	return locationCheck(cli, path, roots)
}

// forbiddenRoot is one spelling of an agent-writable root plus, when the
// folder exists, the filesystem identities it names (the folder itself and,
// when the spelling is a link, the link).
type forbiddenRoot struct {
	path string
	ids  []os.FileInfo
}

// expandRoots returns each root in its absolute cleaned form and, when it
// exists, its symlink-resolved form, so both spellings of a root (macOS /var
// and /private/var) are matched. A root that does not exist has no other
// spelling; any other failure to examine a root is an error, because the
// unresolved spelling alone may not match the real location. Each existing
// root also carries its filesystem identity (see insideAny).
func expandRoots(writable []string) ([]forbiddenRoot, error) {
	paths, err := expandRootPaths(writable)
	if err != nil {
		return nil, err
	}
	out := make([]forbiddenRoot, 0, len(paths))
	for _, p := range paths {
		ids, idErr := rootIdentities(p)
		if idErr != nil {
			return nil, idErr
		}
		out = append(out, forbiddenRoot{path: p, ids: ids})
	}
	return out, nil
}

// expandRootPaths is expandRoots' spelling half. A relative root is made
// absolute against the working directory (the runtime's base): a relative
// root never matches an absolute path.
func expandRootPaths(writable []string) ([]string, error) {
	out := make([]string, 0, len(writable)*2)
	for _, r := range writable {
		if strings.TrimSpace(r) == "" {
			continue
		}
		clean, err := filepath.Abs(r)
		if err != nil {
			return nil, fmt.Errorf("the agent-writable folder %q could not be made absolute: %w", r, err)
		}
		out = append(out, clean)
		resolved, err := filepath.EvalSymlinks(clean)
		switch {
		case err == nil:
			if resolved != clean {
				out = append(out, resolved)
			}
		case errors.Is(err, os.ErrNotExist):
			// Nothing exists there yet; the cleaned spelling is the only one.
		default:
			return nil, fmt.Errorf("the agent-writable folder %q could not be resolved: %w", clean, err)
		}
	}
	return out, nil
}

// rootIdentities stats a root spelling: the folder it reaches and, when the
// spelling itself is a link, the link. A root that does not exist has no
// identity; any other failure is an error, because without the identity a
// differently spelled path to the same folder could not be recognised.
func rootIdentities(p string) ([]os.FileInfo, error) {
	var ids []os.FileInfo
	if fi, err := os.Stat(p); err == nil {
		ids = append(ids, fi)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the agent-writable folder %q could not be examined: %w", p, err)
	}
	if fi, err := os.Lstat(p); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			ids = append(ids, fi)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the agent-writable folder %q could not be examined: %w", p, err)
	}
	return ids, nil
}

// insideAny reports whether p equals or lies below one of roots, by text
// (pathWithin) or by filesystem identity: p, or any folder above it, is the
// same file as an existing root. Identity is what the kernel itself uses, so
// every spelling the volume accepts for a root — letter case, composed or
// decomposed Unicode (macOS treats "Café" written either way as one folder),
// or any other equivalence — is caught. The text check stays for roots that
// do not exist yet. A path element that cannot be read is an error, never
// "outside": the missing element could be exactly the one that matches a
// root.
func insideAny(p string, roots []forbiddenRoot) (bool, error) {
	for _, r := range roots {
		if pathWithin(r.path, p) {
			return true, nil
		}
	}
	for cur := filepath.Clean(p); ; {
		fi, err := os.Lstat(cur)
		if err != nil {
			return false, fmt.Errorf("%q could not be examined: %w", cur, err)
		}
		for _, r := range roots {
			for _, id := range r.ids {
				if os.SameFile(fi, id) {
					return true, nil
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return false, nil
		}
		cur = parent
	}
}

// pathWithin reports whether p equals or lies below root. Both are folded by
// foldPathCase first: on macOS and Windows the usual volumes are
// case-insensitive, so /Users/x/OmniHome and /users/x/omnihome name the same
// folder and a different-case spelling must not escape the check. Folding on
// a rare case-sensitive volume only refuses more, which is the safe side. A
// comparison that fails counts as inside.
func pathWithin(root, p string) bool {
	rel, err := filepath.Rel(foldPathCase(root), foldPathCase(p))
	if err != nil {
		return true
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
