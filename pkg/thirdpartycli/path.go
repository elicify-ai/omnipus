// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package thirdpartycli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// maxLinkHops bounds the symlink chain (r17 §3.5).
const maxLinkHops = 40

// ValidateProgramPath resolves enteredPath and applies every check of r17
// §3.5. It returns the fully resolved final file, which is the file the
// caller must exec; err is a *CheckError.
//
// Agent-writable locations are $OMNIPUS_HOME (every workspace root lives
// under it) plus agentWritable: the caller passes the gateway's home folder
// and the sandbox's agent-writable grants (sandbox.allowed_paths). A leading
// "~" in an entry is expanded.
func ValidateProgramPath(cli, enteredPath string, agentWritable ...string) (resolvedPath string, err error) {
	fail := func(check, at string) (string, error) {
		return "", &CheckError{CLI: cli, Check: check, At: at}
	}
	if !isAbsolutePath(enteredPath) {
		return fail(CheckNotAbsolute, enteredPath)
	}
	roots := writableRoots(agentWritable)
	cur, lastLink := enteredPath, ""
	for hops := 0; ; hops++ {
		if hops == 0 {
			if check := windowsNameCheck(cur); check != "" {
				return fail(check, cur)
			}
		}
		fi, statErr := os.Lstat(cur)
		if statErr != nil {
			if lastLink != "" {
				return fail(CheckNotFound, lastLink)
			}
			return fail(CheckNotFound, cur)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			check := ""
			if hops > 0 {
				check = windowsNameCheck(cur)
			}
			if check == "" {
				check = finalFileCheck(cur, fi, roots)
			}
			if check != "" {
				return fail(check, cur)
			}
			return cur, nil
		}
		// A link's own mode bits are never checked (Linux reports 0777);
		// its containing folder is.
		if check := folderCheck(filepath.Dir(cur), roots); check != "" {
			return fail(check, cur)
		}
		if hops >= maxLinkHops {
			return fail(CheckNotFound, cur)
		}
		target, linkErr := os.Readlink(cur)
		if linkErr != nil {
			return fail(CheckNotFound, cur)
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		lastLink, cur = cur, target
	}
}

// finalFileCheck applies the final-file and containing-folder rows of r17
// §3.5 in their stated order and returns the failing check name or "".
func finalFileCheck(path string, fi os.FileInfo, roots []string) string {
	switch {
	case !fi.Mode().IsRegular():
		return CheckNotRegularFile
	case canExecute(path) != nil:
		return CheckNotExecutable
	case worldWritable(fi):
		return CheckWritableByOthers
	case insideAny(path, roots):
		return CheckAgentWritableLocation
	}
	if dir, err := os.Stat(filepath.Dir(path)); err == nil && worldWritable(dir) {
		return CheckWritableByOthers
	}
	return ""
}

// folderCheck applies the link-folder rule: not inside an agent-writable
// location, not world-writable.
func folderCheck(dir string, roots []string) string {
	if insideAny(dir, roots) {
		return CheckAgentWritableLocation
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return CheckNotFound
	}
	if worldWritable(fi) {
		return CheckWritableByOthers
	}
	return ""
}

// worldWritable is the Unix other-write bit (o+w). Group-write is never
// refused. Windows keeps no such bit (Go reports 0666 for every writable
// file there), so the check does not apply on Windows.
func worldWritable(fi os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	return fi.Mode().Perm()&0o002 != 0
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
