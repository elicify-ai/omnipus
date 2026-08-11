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
// property of the type system rather than of a convention someone has to
// remember while editing a shared loop. If a caller ever needs a writable
// toolchain directory, they must reach for allowedPaths and be seen doing it.
//
// Four classes of entry are dropped, each with a warning rather than an error,
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
//  4. Entries that overlap allowedPaths. allowedPaths grants read+WRITE, so the
//     union of the two lists would be a directory that is writable AND
//     executable — precisely the "drop a binary and run it" shape this design
//     exists to avoid. The exec grant loses; the operator keeps the write
//     access they asked for, minus the ability to execute from it.
func buildExecPathRules(
	allowedExecPaths []string,
	allowedPaths []string,
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

	writable := make([]string, 0, len(allowedPaths))
	for _, raw := range allowedPaths {
		if raw == "" {
			continue
		}
		if clean, ok := expandExecPath(raw); ok {
			writable = append(writable, clean)
		}
	}

	rules := make([]PathRule, 0, len(allowedExecPaths))
	for _, raw := range allowedExecPaths {
		if raw == "" {
			continue
		}

		clean, ok := expandExecPath(raw)
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
			warn("Sandbox exec path overlaps a writable allowed_paths entry; execute access denied (a writable+executable directory would let an agent run code it just wrote).", clean)
			continue
		}

		rules = append(rules, PathRule{
			Path:   clean,
			Access: AccessRead | AccessExecute,
		})
	}
	return rules
}

// expandExecPath expands a leading ~ and cleans the result. It reports false
// when the value cannot be turned into an absolute path, so the caller can skip
// it rather than emit a rule the profile renderer will reject.
func expandExecPath(raw string) (string, bool) {
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
func overlapsAny(path string, others []string) bool {
	for _, other := range others {
		if path == other {
			return true
		}
		if strings.HasPrefix(path, other+string(filepath.Separator)) {
			return true
		}
		if strings.HasPrefix(other, path+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
