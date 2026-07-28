// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// hostnameRE matches a single DNS hostname: must begin with alphanumeric,
// may contain alphanumerics, dots, and hyphens. Conservative by design —
// the SSRF checker performs the authoritative resolve at request time.
var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]*$`)

// validateAllowedPaths enforces allowed_paths validation rules:
//   - each entry must be non-empty
//   - absolute ("/" or "~/" prefix); no relative paths
//   - no ".." segments anywhere
//   - final component, when present on disk, must not be a symlink
//
// When a "~/" path is supplied, the home dir is expanded (via
// os.UserHomeDir) before the symlink check. A missing final component
// is accepted — operators may pre-configure paths that have not been
// created yet. Only an existing symlink is rejected.
//
// One bad entry fails the whole list (atomic semantics); the caller
// must persist nothing when this returns a non-nil error.
func validateAllowedPaths(entries []string) error {
	for _, entry := range entries {
		if entry == "" {
			return fmt.Errorf("allowed_paths entries must be non-empty")
		}

		absolute := strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, "~/")
		if !absolute {
			return fmt.Errorf("allowed_paths entry %q must be absolute — got relative path", entry)
		}

		// Reject ".." anywhere — including embedded segments like /var/x/../etc.
		// filepath.Clean would silently collapse them, so we check the raw
		// segments before any normalisation.
		for _, seg := range strings.Split(entry, "/") {
			if seg == ".." {
				return fmt.Errorf("allowed_paths entry %q must not contain '..' segments", entry)
			}
		}

		// Symlink check on the final component. Expand "~" for the lstat
		// call; we still keep the original entry string in the error so
		// the operator sees what they typed.
		resolved := entry
		if strings.HasPrefix(resolved, "~/") {
			home, err := os.UserHomeDir()
			if err == nil && home != "" {
				resolved = filepath.Join(home, strings.TrimPrefix(resolved, "~/"))
			}
		}
		fi, err := os.Lstat(resolved)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// Missing paths are accepted — operators can pre-configure
				// paths that do not exist yet. The sandbox layer handles
				// access failures at enforcement time.
				continue
			}
			// EACCES, EIO, ELOOP, or any other error means the path exists
			// but cannot be inspected. Surface as a 400 at save time rather
			// than silently permitting an un-inspectable entry.
			return fmt.Errorf("allowed_paths[%q]: %w", entry, err)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("allowed_paths entry %q must not end in a symlink", entry)
		}
	}
	return nil
}

// wildcardCIDRs are the two "allow everything" CIDR strings. Accepting
// either effectively disables SSRF internal-block protection; we still
// allow the save so that operators running closed networks can opt in,
// but we log a conspicuous warning for audit review.
var wildcardCIDRs = map[string]struct{}{
	"0.0.0.0/0": {},
	"::/0":      {},
}

// validateSSRFAllowInternal enforces ssrf.allow_internal validation rules
// without inventing any new config shape.
//
// Each entry must parse as one of:
//   - a CIDR via net.ParseCIDR
//   - an IP via net.ParseIP
//   - a hostname matching hostnameRE
//
// Empty string rejects. Entries equal to "0.0.0.0/0" or "::/0" validate
// successfully but are returned in the warnings slice so the caller can
// emit a slog.Warn with event=ssrf_wildcard_accepted.
func validateSSRFAllowInternal(entries []string) (warnings []string, err error) {
	for _, entry := range entries {
		if entry == "" {
			return nil, fmt.Errorf("ssrf.allow_internal entry %q must be a hostname, IP address, or CIDR range", entry)
		}

		if _, _, cidrErr := net.ParseCIDR(entry); cidrErr == nil {
			if _, isWildcard := wildcardCIDRs[entry]; isWildcard {
				warnings = append(warnings, entry)
			}
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			continue
		}
		if hostnameRE.MatchString(entry) {
			continue
		}

		return nil, fmt.Errorf("ssrf.allow_internal entry %q must be a hostname, IP address, or CIDR range", entry)
	}
	return warnings, nil
}

// validateShellDenyPatterns enforces that every entry in the global
// security.shell_deny_patterns list compiles as a valid Go regexp. Mirrors
// the per-agent shell_policy.custom_deny_patterns validation in rest.go's
// updateAgent handler (gen.AgentUpdateRequest.ShellPolicy.CustomDenyPatterns)
// — see the same regexp.Compile check there — so both the global and
// per-agent surfaces reject malformed patterns identically, matching the
// documented contract ("Must each be valid Go regexp patterns (400 on
// invalid regexp)", gen.SandboxConfig's ShellDenyPatterns doc comment).
//
// Without this, a malformed pattern would persist with 200 OK and be
// silently dropped at enforcement time — compileDenyPatterns
// (pkg/tools/shell_guard.go) logs a Warn and skips any pattern that fails to
// compile, so the operator would only discover the mistake by grepping
// gateway.log.
//
// One bad entry fails the whole list (atomic semantics, matching
// validateAllowedPaths/validateSSRFAllowInternal above); the caller must
// persist nothing when this returns a non-nil error.
func validateShellDenyPatterns(entries []string) error {
	for _, pattern := range entries {
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("shell_deny_patterns: invalid regexp %q: %w", pattern, err)
		}
	}
	return nil
}
