// Package chromeintegrity is the shared chrome.sha256 integrity helper for
// the browser package and the omnipus doctor command. Centralizing
// ParseChromeSHA256Manifest and VerifyChromeSHA256 here eliminates the
// CORR-005 duplication (the doctor command had its own copy) and gives
// every caller — the runtime installer, the runtime resolver, the
// capability classifier, and the doctor WARN-BROWSER-006 check — the
// exact same parser + verifier behavior.
//
// Both functions preserve the SEC-ADR052-004 hardening contract
// (tolerance for UTF-8 BOM / CRLF / sha256: prefix / sha256sum two-field
// format, rejection of uppercase hex, NUL bytes inside the digest, or
// digest length != 64 chars) and the SEC-ADR052-001 fail-closed contract
// (ErrSHA256ManifestMissing when shaPath is empty or unreadable).
package chromeintegrity

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrSHA256ManifestMissing is the sentinel returned by VerifyChromeSHA256
// when shaPath is empty OR the manifest cannot be read. Per
// SEC-ADR052-001, the package Chrome path (resolver step 3, capability
// classifier) treats this as a refusal and falls through to the managed
// download path; the managed install path (installer.go's
// findInstalledBuild) treats this as permissive back-compat for the
// runtime-download path that doesn't ship a manifest by default.
var ErrSHA256ManifestMissing = errors.New("chrome.sha256 integrity manifest missing or unreadable")

var errNoSHA256Line = errors.New("manifest contains no SHA-256 digest line")

// ParseChromeSHA256Manifest extracts the canonical 64-char lowercase hex
// SHA-256 digest from raw. Tolerant of the well-formed-but-noisy shapes
// CfT / goreleaser / sha256sum pipelines emit; refuses everything else as
// an explicit parse error. See SEC-ADR052-004 for the full grammar.
//
// Recognized shapes:
//
//   - "<64-hex>\n"
//   - "<64-hex>  chrome\n"                       — sha256sum(1) two-field form
//   - "  <64-hex>  chrome\n"                      — leading whitespace
//   - "# comment\n<64-hex>\n"                     — BusyBox-style comment prefix
//   - "sha256:<64-hex>\n" or "SHA-256:<64-hex>\n" — algo-prefixed
//   - "\xEF\xBB\xBF<64-hex>\n"                    — UTF-8 BOM at start of file
//   - "<64-hex>\r\n"                              — CRLF line endings
//
// Rejected shapes (return explicit errors, never silent-coerce):
//
//   - uppercase hex digits                         — toolchain mismatch
//   - digest length != 64 chars                    — corrupt / partial
//   - NUL bytes or CR-only separators INSIDE digest
//   - non-hex characters outside a comment line
//   - empty manifest                               — no digest
//   - embedded binary garbage after the digest line
func ParseChromeSHA256Manifest(raw []byte) (string, error) {
	// Strip a leading UTF-8 BOM (3 bytes) — present in some Windows-port
	// emitters and harmless once removed.
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		raw = raw[3:]
	}
	// Normalize CRLF and lone CR to LF before line-splitting.
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	raw = bytes.ReplaceAll(raw, []byte("\r"), []byte("\n"))

	var digest string
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		if line[0] == '#' {
			continue // comment line
		}
		// Tolerate a leading "sha256:" / "SHA-256:" prefix.
		trimmed := line
		if bytes.HasPrefix(trimmed, []byte("sha256:")) {
			trimmed = trimmed[len("sha256:"):]
		} else if bytes.HasPrefix(trimmed, []byte("SHA-256:")) {
			trimmed = trimmed[len("SHA-256:"):]
		}
		// Tolerate the sha256sum(1) two-field "<digest>  <filename>" format.
		// A single whitespace boundary between two whitespace-separated
		// fields is treated as the separator; if the right field is the
		// binary name and the left is a 64-hex digest, take the left.
		fields := bytes.Fields(trimmed)
		if len(fields) == 2 {
			if isLowerHex(fields[0]) && !isLowerHex(fields[1]) {
				trimmed = fields[0]
			} else {
				return "", fmt.Errorf("two-field line is not a <hex>  <name> pair: %q", line)
			}
		} else if len(fields) != 1 {
			return "", fmt.Errorf("expected a single digest field, got %d: %q", len(fields), line)
		} else {
			trimmed = fields[0]
		}
		candidate := string(trimmed)
		if len(candidate) != 64 {
			return "", fmt.Errorf("digest length %d != 64: %q", len(candidate), candidate)
		}
		// SEC-ADR052-004: refuse uppercase hex — toolchain mismatch.
		if !isLowerHex(trimmed) {
			return "", fmt.Errorf("digest is not lowercase hex (toolchain mismatch): %q", candidate)
		}
		if digest != "" {
			return "", fmt.Errorf("manifest declares multiple digests: %q and %q", digest, candidate)
		}
		digest = candidate
	}
	if digest == "" {
		return "", errNoSHA256Line
	}
	return digest, nil
}

// VerifyChromeSHA256 uses subtle.ConstantTimeCompare for the integrity comparison. The constant-time
// requirement is enforced by code review and static lint convention rather
// than a timing-sensitive unit test.
//
// SEC-ADR052-001 fail-closed contract: returns ErrSHA256ManifestMissing
// when shaPath is empty OR the manifest cannot be read for any reason
// (os.IsNotExist, permission denied, EIO, ...). The caller decides what to
// do with that sentinel — for the package Chrome path (Phase 1 floor),
// the resolver falls through to the managed download path; for the
// managed install path, the verifier is only invoked when chrome.sha256
// is present.
//
// SEC-ADR052-005 TOCTOU + symlink defense: the binary and the manifest
// are both checked with leaf symlink rejection via os.Lstat (refuses a
// symlink at the leaf). A TOCTOU swap between Lstat and Open is bounded
// by the install-root-mode check at the call sites — those refuse to
// operate on a world-writable install root, so the attacker who could
// race the open needs write access to the install root's contents,
// which the mode check disallows.
func VerifyChromeSHA256(binaryPath, shaPath string) error {
	if shaPath == "" {
		return ErrSHA256ManifestMissing
	}

	// SEC-ADR052-005: refuse a symlinked manifest at the leaf.
	info, lerr := os.Lstat(shaPath)
	if lerr != nil {
		// Any error here — IsNotExist, EACCES, EIO, ... — is a refusal.
		// Per SEC-ADR052-001, a manifest we can't read is the same as a
		// manifest we can't trust.
		return fmt.Errorf("%w: %s: %v", ErrSHA256ManifestMissing, shaPath, lerr)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symlink", ErrSHA256ManifestMissing, shaPath)
	}
	if info.IsDir() {
		return fmt.Errorf("%w: %s is a directory, not a file", ErrSHA256ManifestMissing, shaPath)
	}

	raw, readErr := os.ReadFile(shaPath)
	if readErr != nil {
		return fmt.Errorf("%w: read %s: %v", ErrSHA256ManifestMissing, shaPath, readErr)
	}

	want, parseErr := ParseChromeSHA256Manifest(raw)
	if parseErr != nil {
		return fmt.Errorf("parse sha256 manifest %s: %w", shaPath, parseErr)
	}

	// SEC-ADR052-005: refuse a symlinked binary at the leaf.
	binInfo, lerr := os.Lstat(binaryPath)
	if lerr != nil {
		return fmt.Errorf("lstat binary %s: %w", binaryPath, lerr)
	}
	if binInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to verify a symlinked binary at %s", binaryPath)
	}
	if binInfo.IsDir() {
		return fmt.Errorf("binary path %s is a directory", binaryPath)
	}

	f, openErr := os.Open(binaryPath)
	if openErr != nil {
		return fmt.Errorf("open binary %s: %w", binaryPath, openErr)
	}
	defer f.Close()
	hasher := sha256.New()
	if _, copyErr := io.Copy(hasher, f); copyErr != nil {
		return fmt.Errorf("hash binary %s: %w", binaryPath, copyErr)
	}
	got := hex.EncodeToString(hasher.Sum(nil))

	// SEC-ADR052-004: constant-time comparison. Both sides are 64 hex chars
	// by construction (ParseChromeSHA256Manifest enforces length), so this
	// is a fixed-size compare — ConstantTimeCompare returns 1 iff equal
	// and 0 otherwise, with no early-exit on first-mismatch byte.
	if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
		return fmt.Errorf(
			"sha256 mismatch: binary hashes to %s… but manifest %s declares %s…",
			got[:16],
			shaPath,
			want[:16],
		)
	}
	return nil
}

// isLowerHex reports whether b is non-empty and contains only lowercase
// hex digits ([0-9a-f]). Used by ParseChromeSHA256Manifest — uppercase
// is an explicit error (SEC-ADR052-004 — toolchain mismatch, surfaced
// rather than silently lowercased).
func isLowerHex(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
