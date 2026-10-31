package clitrust

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxLinkHops bounds link-chain resolution (the same order of magnitude as
// the kernel's own ELOOP limit) so a link cycle is refused, not followed.
const maxLinkHops = 40

// agentWritableKinds names what the forbidden roots are in every refusal, so
// a person can tell why a location is refused without a list of paths.
const agentWritableKinds = "a folder agents can write (the Omnipus home, a workspace folder or a workspace mount)"

// CheckProgramPath decides whether path may be started as a third-party CLI
// (T11 spec §3.2). It returns the resolved final file; callers launch exactly
// that file and never resolve path a second time.
//
// The path must be absolute. The link chain is resolved one link at a time.
// The only location rule is "could an agent have planted it": the candidate
// is refused when the final file, any link, or any link's folder lies inside
// one of the forbidden roots (normally AgentWritableRoots — the Omnipus home,
// agent homes, workspaces and mounts). It is also refused when the final file
// is not a regular file the gateway user can execute. Who owns the program or
// can write it is the user's responsibility, not Omnipus's (founder decision
// 2026-10-30, "Omnipus runs the command and worries not about where the CLI
// accesses anything"), so no owner, permission-bit or ACL check is made.
// Windows adds its path-shape rules (drive or UNC path, .exe/.cmd/.bat, no
// 8.3 short names, no alternate data streams) in check_windows.go.
//
// Every refusal is an error naming the failed check and the path concerned;
// no file content is ever read into it.
func CheckProgramPath(path string, forbidden []string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("no program path was given; enter the absolute path of the program")
	}
	if err := checkPathShape(path); err != nil {
		return "", err
	}
	roots, err := expandRoots(forbidden)
	if err != nil {
		return "", err
	}

	final, links, err := resolveLinkChain(path)
	if err != nil {
		return "", err
	}
	var root string
	var inside bool
	for _, link := range links {
		root, inside, err = insideAny(link, roots)
		if err != nil {
			return "", err
		}
		if inside {
			return "", fmt.Errorf("the link %q lies inside %q, %s", link, root, agentWritableKinds)
		}
		root, inside, err = insideAny(filepath.Dir(link), roots)
		if err != nil {
			return "", err
		}
		if inside {
			return "", fmt.Errorf("the folder of the link %q lies inside %q, %s", link, root, agentWritableKinds)
		}
	}
	root, inside, err = insideAny(final, roots)
	if err != nil {
		return "", err
	}
	if inside {
		if len(links) > 0 {
			return "", fmt.Errorf("%q is a link chain ending at %q, which lies inside %q, %s", path, final, root, agentWritableKinds)
		}
		return "", fmt.Errorf("%q lies inside %q, %s", final, root, agentWritableKinds)
	}
	if err := checkFinalFile(final); err != nil {
		return "", err
	}
	return final, nil
}

// forbiddenRoot is one spelling of a forbidden root plus, when the folder
// exists, the filesystem identities it names (the folder itself and, when
// the spelling is a link, the link).
type forbiddenRoot struct {
	path string
	ids  []os.FileInfo
}

// expandRoots returns each forbidden root in its cleaned form and, when it
// exists, its symlink-resolved form, so both spellings of a root (macOS
// /var and /private/var) are matched. A root that does not exist has no
// other spelling; any other resolution failure is an error, because the
// unresolved spelling alone may not match the real location. Each existing
// root also carries its filesystem identity (see insideAny).
func expandRoots(forbidden []string) ([]forbiddenRoot, error) {
	paths, err := expandRootPaths(forbidden)
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

// rootIdentities stats a root spelling: the folder it reaches and, when the
// spelling itself is a link, the link. A root that does not exist has no
// identity; any other failure is an error, because without the identity a
// differently spelt path to the same folder could not be recognised.
func rootIdentities(p string) ([]os.FileInfo, error) {
	var ids []os.FileInfo
	if fi, err := os.Stat(p); err == nil {
		ids = append(ids, fi)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the agent-writable folder %q could not be read, so the program location cannot be checked: %w", p, err)
	}
	if fi, err := os.Lstat(p); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			ids = append(ids, fi)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the agent-writable folder %q could not be read, so the program location cannot be checked: %w", p, err)
	}
	return ids, nil
}

// expandRootPaths is expandRoots' spelling half.
func expandRootPaths(forbidden []string) ([]string, error) {
	out := make([]string, 0, len(forbidden)*2)
	for _, r := range forbidden {
		if strings.TrimSpace(r) == "" {
			continue
		}
		// A relative root is made absolute against the working directory (the
		// runtime's base); a relative root never matches an absolute path.
		clean, err := filepath.Abs(r)
		if err != nil {
			return nil, fmt.Errorf("the agent-writable folder %q could not be made absolute, so the program location cannot be checked: %w", r, err)
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
			return nil, fmt.Errorf("the agent-writable folder %q could not be resolved, so the program location cannot be checked: %w", clean, err)
		}
	}
	return out, nil
}

// insideAny reports whether p equals or lies below one of roots, by text
// (pathWithin) or by filesystem identity: p, or any folder above it, is the
// same file as an existing root. Identity is what the kernel itself uses, so
// every spelling the volume accepts for a root — letter case, composed or
// decomposed Unicode (macOS treats "Café" written either way as one folder),
// or any other equivalence — is caught (T11 round 5, S8). The text check
// stays for roots that do not exist yet. A path component that cannot be
// read is an error, never "outside": the missing component could be exactly
// the one that matches a root.
func insideAny(p string, roots []forbiddenRoot) (string, bool, error) {
	for _, r := range roots {
		if pathWithin(r.path, p) {
			return r.path, true, nil
		}
	}
	for cur := filepath.Clean(p); ; {
		fi, err := os.Lstat(cur)
		if err != nil {
			return "", false, fmt.Errorf("%q could not be read, so the program location cannot be checked: %w", cur, err)
		}
		for _, r := range roots {
			for _, id := range r.ids {
				if os.SameFile(fi, id) {
					return r.path, true, nil
				}
			}
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false, nil
		}
		cur = parent
	}
}

// pathWithin reports whether p equals or lies below root. Both are folded by
// foldPathCase first: on macOS and Windows the usual volumes are
// case-insensitive, so /Users/x/OmniHome and /users/x/omnihome name the same
// folder and a different-case spelling must not escape the check (T11 round
// 4, R3-C1). Folding on a rare case-sensitive volume only refuses more,
// which is the safe side. A comparison that fails counts as inside.
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

// resolveLinkChain walks path one component at a time from its volume root,
// replacing every symbolic link by its target. It returns the final resolved
// file and every link met on the way (as resolved link locations).
func resolveLinkChain(path string) (string, []string, error) {
	vol := filepath.VolumeName(path)
	pending := splitComponents(path[len(vol):])
	cur := vol + string(filepath.Separator)
	var links []string
	hops := 0
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		if c == ".." {
			cur = filepath.Dir(cur)
			continue
		}
		next := filepath.Join(cur, c)
		fi, err := os.Lstat(next)
		if err != nil {
			return "", nil, fmt.Errorf("%q cannot be read: %w", path, err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			cur = next
			continue
		}
		hops++
		if hops > maxLinkHops {
			return "", nil, fmt.Errorf("%q has more than %d links in its chain", path, maxLinkHops)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", nil, fmt.Errorf("the link %q cannot be read: %w", next, err)
		}
		links = append(links, next)
		if filepath.IsAbs(target) {
			tvol := filepath.VolumeName(target)
			cur = tvol + string(filepath.Separator)
			pending = append(splitComponents(target[len(tvol):]), pending...)
		} else {
			pending = append(splitComponents(target), pending...)
		}
	}
	return cur, links, nil
}

func splitComponents(p string) []string {
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == filepath.Separator })
	out := parts[:0]
	for _, part := range parts {
		if part != "." && part != "" {
			out = append(out, part)
		}
	}
	return out
}
