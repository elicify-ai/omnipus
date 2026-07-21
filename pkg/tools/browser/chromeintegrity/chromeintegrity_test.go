package chromeintegrity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseSHA256Manifest_Table exercises every accepted and rejected
// shape per SEC-ADR052-004's grammar. Mirrors the in-package table that
// lived in installer.go before CORR-005 — moved here verbatim so the
// shared package still has full parser coverage.
func TestParseSHA256Manifest_Table(t *testing.T) {
	const validHex = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	type tc struct {
		name       string
		input      []byte
		wantDigest string
		wantErr    bool
		errSubstr  string
	}
	cases := []tc{
		{
			name:       "plain 64-char lowercase hex with trailing newline",
			input:      []byte(validHex + "\n"),
			wantDigest: validHex,
		},
		{
			name:       "sha256sum two-field format <hex>  chrome",
			input:      []byte(validHex + "  chrome\n"),
			wantDigest: validHex,
		},
		{
			name:       "leading sha256: prefix",
			input:      []byte("sha256:" + validHex + "\n"),
			wantDigest: validHex,
		},
		{
			name:       "leading SHA-256: prefix",
			input:      []byte("SHA-256:" + validHex + "\n"),
			wantDigest: validHex,
		},
		{
			name:       "leading comment line then digest",
			input:      []byte("# SHA256\n" + validHex + "\n"),
			wantDigest: validHex,
		},
		{
			name:       "leading UTF-8 BOM",
			input:      append([]byte{0xEF, 0xBB, 0xBF}, []byte(validHex+"\n")...),
			wantDigest: validHex,
		},
		{
			name:       "CRLF line endings",
			input:      []byte(validHex + "\r\n"),
			wantDigest: validHex,
		},
		{
			name:      "uppercase hex is REJECTED (toolchain mismatch, SEC-004)",
			input:     []byte("ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789\n"),
			wantErr:   true,
			errSubstr: "lowercase hex",
		},
		{
			name:      "wrong digest length is REJECTED",
			input:     []byte("abcdef0123456789abcdef0123456789abcdef0123456789abcdef01234567\n"), // 63 chars
			wantErr:   true,
			errSubstr: "digest length",
		},
		{
			name:      "64-char non-hex content is REJECTED (length matches, content wrong)",
			input:     []byte(strings.Repeat("_", 64) + "\n"),
			wantErr:   true,
			errSubstr: "lowercase hex",
		},
		{
			name:      "empty manifest is REJECTED",
			input:     []byte("\n"),
			wantErr:   true,
			errSubstr: "no SHA-256",
		},
		{
			name:      "non-hex garbage line is REJECTED",
			input:     []byte("not-a-hex-digest\n"),
			wantErr:   true,
			errSubstr: "digest length",
		},
		{
			name:      "two distinct digests is REJECTED",
			input:     []byte(validHex + "\n" + validHex + "\n"),
			wantErr:   true,
			errSubstr: "multiple digests",
		},
		// TEST-010: additional adversarial shapes:
		{
			name:      "NUL byte inside the 64-char digest is REJECTED",
			input:     []byte(validHex[:32] + "\x00" + validHex[33:] + "\n"),
			wantErr:   true,
			errSubstr: "lowercase hex",
		},
		{
			name:      "embedded binary garbage after the digest is REJECTED",
			input:     []byte(validHex + "\x01\x02\x03\x04\x05\x06\x07\n"),
			wantErr:   true,
			errSubstr: "digest length",
		},
		{
			name:      "CR-only mid-digest is REJECTED (CRs are normalized to LFs before line-splitting)",
			input:     []byte(validHex[:32] + "\r" + validHex[33:] + "\n"),
			wantErr:   true,
			errSubstr: "digest length",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseChromeSHA256Manifest(c.input)
			if c.wantErr {
				require.Error(t, err)
				if c.errSubstr != "" {
					assert.Contains(t, err.Error(), c.errSubstr)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.wantDigest, got)
		})
	}
}

// TestVerifyChromeSHA256_DefensiveGuards covers the inputs the per-file
// test harness used to exercise — shaPath is a directory, shaPath is a
// symlink, shaPath is unreadable, binary is a symlink, binary is a
// directory, binary does not exist. (TEST-007 in the review matrix.)
func TestVerifyChromeSHA256_DefensiveGuards(t *testing.T) {
	const validHex = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	t.Run("shaPath empty returns ErrSHA256ManifestMissing", func(t *testing.T) {
		err := VerifyChromeSHA256("/anywhere", "")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSHA256ManifestMissing))
	})

	t.Run("shaPath does not exist returns ErrSHA256ManifestMissing", func(t *testing.T) {
		err := VerifyChromeSHA256("/anywhere", filepath.Join(t.TempDir(), "nope"))
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSHA256ManifestMissing))
	})

	t.Run("shaPath is a directory is REJECTED", func(t *testing.T) {
		dir := t.TempDir()
		err := VerifyChromeSHA256("/anywhere", dir)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSHA256ManifestMissing))
	})

	t.Run("shaPath is unreadable (chmod 0) returns ErrSHA256ManifestMissing", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("running as root bypasses chmod 000 file mode")
		}
		shaPath := filepath.Join(t.TempDir(), "sha")
		require.NoError(t, os.WriteFile(shaPath, []byte(validHex+"\n"), 0o000))
		t.Cleanup(func() { _ = os.Chmod(shaPath, 0o644) })
		err := VerifyChromeSHA256("/anywhere", shaPath)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSHA256ManifestMissing))
	})

	t.Run("shaPath is a symlink is REJECTED", func(t *testing.T) {
		// os.Symlink requires elevated privileges on Windows.
		if os.Getenv("GOOS") == "windows" {
			t.Skip("os.Symlink requires elevated privileges on Windows")
		}
		real := filepath.Join(t.TempDir(), "real-sha")
		require.NoError(t, os.WriteFile(real, []byte(validHex+"\n"), 0o644))
		link := filepath.Join(t.TempDir(), "sha-link")
		require.NoError(t, os.Symlink(real, link))
		err := VerifyChromeSHA256("/anywhere", link)
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrSHA256ManifestMissing))
	})

	t.Run("binary path does not exist is REJECTED", func(t *testing.T) {
		shaPath := filepath.Join(t.TempDir(), "sha")
		require.NoError(t, os.WriteFile(shaPath, []byte(validHex+"\n"), 0o644))
		err := VerifyChromeSHA256(filepath.Join(t.TempDir(), "nope-binary"), shaPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "lstat binary")
	})

	t.Run("binary is a directory is REJECTED", func(t *testing.T) {
		shaPath := filepath.Join(t.TempDir(), "sha")
		require.NoError(t, os.WriteFile(shaPath, []byte(validHex+"\n"), 0o644))
		err := VerifyChromeSHA256(t.TempDir(), shaPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "directory")
	})

	t.Run("binary is a symlink is REJECTED", func(t *testing.T) {
		if os.Getenv("GOOS") == "windows" {
			t.Skip("os.Symlink requires elevated privileges on Windows")
		}
		shaPath := filepath.Join(t.TempDir(), "sha")
		require.NoError(t, os.WriteFile(shaPath, []byte(validHex+"\n"), 0o644))
		realBin := filepath.Join(t.TempDir(), "real-binary")
		require.NoError(t, os.WriteFile(realBin, []byte("#!/bin/sh\nexit 0\n"), 0o755))
		linkBin := filepath.Join(t.TempDir(), "binary-link")
		require.NoError(t, os.Symlink(realBin, linkBin))
		err := VerifyChromeSHA256(linkBin, shaPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "symlinked binary")
	})

	t.Run("digest mismatch is REJECTED with a sha256 mismatch error", func(t *testing.T) {
		binPath := filepath.Join(t.TempDir(), "bin")
		require.NoError(t, os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755))
		shaPath := filepath.Join(t.TempDir(), "sha")
		require.NoError(t, os.WriteFile(shaPath,
			[]byte("0000000000000000000000000000000000000000000000000000000000000000\n"), 0o644))
		err := VerifyChromeSHA256(binPath, shaPath)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "sha256 mismatch")
	})

	t.Run("matching digest is ACCEPTED", func(t *testing.T) {
		binPath := filepath.Join(t.TempDir(), "bin")
		contents := []byte("#!/bin/sh\nexit 0\n")
		require.NoError(t, os.WriteFile(binPath, contents, 0o755))
		sum := sha256.Sum256(contents)
		shaPath := filepath.Join(t.TempDir(), "sha")
		require.NoError(t, os.WriteFile(shaPath, []byte(hex.EncodeToString(sum[:])+"\n"), 0o644))
		assert.NoError(t, VerifyChromeSHA256(binPath, shaPath))
	})
}
