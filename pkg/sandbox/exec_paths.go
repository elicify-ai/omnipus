// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// buildExecPathRules converts sandbox.allowed_exec_paths into read+execute
// PathRules, dropping any entry that would be unsafe or unusable.
//
// The access bits are hard-coded here and the function takes no access
// argument. That is deliberate: it makes "exec paths are never writable" a
// property of this function's signature rather than of a convention someone
// has to remember while editing a shared loop. If a caller ever needs a writable
// toolchain directory, they must reach for allowedPaths and be seen doing it.
//
// Empty strings are skipped silently. Four further classes are dropped, each
// with a warning rather than an error,
// because a bad entry in an install-time seed must never prevent the gateway
// from booting:
//
//  1. Entries that cannot be made absolute (an unexpanded ~ on a system with
//     no resolvable home). A relative path in a rendered Seatbelt profile is
//     interpreted against the sandbox-exec working directory, and the renderer
//     rejects it outright — which would abort boot with a policy render error.
//  2. The filesystem root. An exec grant on "/" would hand back everything the
//     rest of the policy is carefully withholding.
//  3. System-restricted paths (/etc, /proc, /sys, /dev, /boot, /root) — the
//     same set AllowedPaths refuses to make writable.
//  4. Entries that overlap a WRITABLE path. That includes the operator's
//     allowed_paths and the unconditional $OMNIPUS_HOME / /tmp / $TMPDIR
//     grants, so the union would be a directory that is writable AND
//     executable — precisely the "drop a binary and run it" shape this design
//     exists to avoid. The exec grant loses; the operator keeps the write
//     access they asked for, minus the ability to execute from it.
func buildExecPathRules(
	allowedExecPaths []string,
	writableRoots []string,
	warnFn func(msg string, path string),
) []PathRule {
	if len(allowedExecPaths) == 0 {
		return nil
	}

	warn := func(msg, path string) {
		if warnFn != nil {
			warnFn(msg, path)
		}
	}

	// writableRoots arrive as already-expanded, cleaned rule paths, so there is
	// no second expansion step here that could silently drop an entry and
	// weaken the overlap check.
	writable := writableRoots

	rules := make([]PathRule, 0, len(allowedExecPaths))
	for _, raw := range allowedExecPaths {
		if raw == "" {
			continue
		}

		clean, ok := expandUserPath(raw)
		if !ok {
			warn("Sandbox exec path is not absolute and could not be expanded; skipping.", raw)
			continue
		}
		if clean == string(filepath.Separator) {
			warn("Sandbox exec path resolves to the filesystem root; refusing to grant execute on /.", raw)
			continue
		}
		if isSystemRestricted(clean) {
			warn("Sandbox exec path is a restricted system path; execute access denied.", clean)
			continue
		}
		if overlapsAny(clean, writable) {
			warn("Sandbox exec path overlaps a writable allowed_paths entry; execute access denied (a writable+executable directory would let an agent run code it just wrote). Overlap is judged on the symlink-resolved path and ignores letter case, so two entries that look different can still be the same directory.", clean)
			continue
		}

		rules = append(rules, PathRule{
			Path:   clean,
			Access: AccessRead | AccessExecute,
		})
	}
	return rules
}

// expandUserPath expands a leading ~ and cleans the result. It reports false
// when the value cannot be turned into an absolute path, so the caller can skip
// it rather than emit a rule the profile renderer will reject (which on macOS
// fails the render and aborts boot).
//
// Used for BOTH allowed_paths and allowed_exec_paths so the two config lists
// cannot disagree about what "~/work" means.
func expandUserPath(raw string) (string, bool) {
	p := strings.TrimSpace(raw)
	if p == "" {
		return "", false
	}

	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return "", false
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}

	if !filepath.IsAbs(p) {
		return "", false
	}
	return filepath.Clean(p), true
}

// overlapsAny reports whether path equals, contains, or is contained by any
// entry in others. Containment is checked in BOTH directions: granting execute
// on /usr/local/bin is unsafe if the operator made /usr/local writable, and
// equally unsafe if they made /usr/local/bin/sub writable.
//
// The comparison runs in the space the KERNEL enforces, not the space the
// operator typed (see comparisonForms and pathCoversFold). A purely textual
// compare was bypassable three ways, each demonstrated with a real
// sandbox-exec child that wrote AND executed in a supposedly read+exec
// directory:
//
//   - Case: allowed_paths ".../work" plus allowed_exec_paths ".../WORK" is one
//     directory on a case-insensitive volume (the APFS default), and Seatbelt
//     matches (subpath …) case-insensitively there.
//   - Symlinks: allowed_exec_paths ".../toolbin" where toolbin links into a
//     writable tree. The renderer resolves symlinks; the guard did not. The
//     shipped seed includes ~/.local/bin and ~/go/bin, which dotfile managers
//     routinely symlink.
//   - /private aliasing: "/private/tmp/x" is textually unlike the built-in
//     "/tmp" writable root, and "/private/var/folders/…" unlike $TMPDIR.
func overlapsAny(path string, others []string) bool {
	candidates := comparisonForms(path)
	for _, other := range others {
		for _, o := range comparisonForms(other) {
			for _, c := range candidates {
				if pathCoversFold(o, c) || pathCoversFold(c, o) {
					return true
				}
			}
		}
	}
	return false
}

// comparisonForms returns every form a path must be compared in: the cleaned
// declared form, plus the symlink-resolved form when it differs.
//
// Both forms are kept deliberately. Resolution is the form that matters (it is
// what Seatbelt filters on and what Landlock reaches by inode), but keeping the
// declared form too makes this check strictly ADDITIVE: no overlap the old
// textual compare caught can be lost, including the case where a path is a
// dangling symlink today and a real directory a second later.
//
// Resolution reuses resolveSeatbeltPath — the same function the profile
// renderer uses to decide what to emit — so the guard and the rendered profile
// can never disagree about which directory a rule names. It resolves the
// deepest existing ancestor and re-attaches the remainder, so entries that do
// not exist yet (common: a toolchain dir created after boot) still normalise.
//
// Relative paths are returned unresolved: EvalSymlinks would interpret them
// against the process CWD, making the answer depend on where the gateway
// happened to be started. Every production caller passes an absolute path
// (expandUserPath enforces it before this is reached).
func comparisonForms(p string) []string {
	clean := filepath.Clean(p)
	if !filepath.IsAbs(clean) {
		return []string{clean}
	}
	resolved, _ := resolveSeatbeltPath(clean)
	if resolved == "" || resolved == clean {
		return []string{clean}
	}
	return []string{clean, resolved}
}

// pathCoversFold reports whether parent equals child or is one of its
// ancestors, comparing WITHOUT regard to letter case.
//
// Case folding is unconditional — on every platform, not only the ones
// presumed to have a case-insensitive filesystem. Two reasons:
//
//  1. Case sensitivity is a property of the MOUNT, not of the OS. macOS is
//     case-insensitive by default but can be formatted case-sensitive; Linux
//     ext4 supports per-directory casefold, and vfat/exfat/ciopfs/SMB mounts
//     are case-insensitive; Windows NTFS can opt individual directories into
//     case sensitivity. Any per-platform guess is wrong somewhere, and a wrong
//     guess in the "assume case-sensitive" direction is precisely the
//     vulnerability being fixed here.
//  2. The two failure directions are not symmetric. A false MATCH drops an exec
//     grant (or strips a write bit) and emits a warning the operator can act
//     on — visible, bounded, recoverable. A false MISS silently produces a
//     directory that is writable AND executable, which is the one capability
//     this whole design exists to withhold. Fail-safe therefore means folding.
//
// The residual cost is a Linux operator with two directories differing only by
// case (say /srv/Tools exec, /srv/tools writable) losing the exec grant with a
// warning. That is the correct trade.
//
// Both sides are lowercased in full before the prefix test rather than slicing
// one by the other's byte length: Unicode case folding can change a string's
// byte length, and slicing on the unfolded length could split a rune and miss.
func pathCoversFold(parent, child string) bool {
	sep := string(filepath.Separator)
	p := strings.ToLower(strings.TrimSuffix(filepath.Clean(parent), sep))
	c := strings.ToLower(strings.TrimSuffix(filepath.Clean(child), sep))
	if p == "" {
		// Clean+Trim collapses the filesystem root to "": it covers everything.
		return true
	}
	return c == p || strings.HasPrefix(c, p+sep)
}
