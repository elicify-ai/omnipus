// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRenderSeatbeltProfile is the table-driven driver covering directive
// presence, network default-deny, chrome/OMNIPUS_HOME allows, and path
// validation. It runs on Linux because renderSeatbeltProfile is pure string
// generation with no platform dependency.
func TestRenderSeatbeltProfile(t *testing.T) {
	// A representative workspace policy: $OMNIPUS_HOME RWX, /tmp RWX, the
	// bundled-Chrome path (R-only; lives under /opt on Linux and
	// /Applications on macOS — the renderer is path-agnostic), plus the
	// default connect ports (DNS/HTTP/HTTPS).
	omnipusHome := "/Users/op/.omnipus"
	chromePath := "/Applications/Chromium.app/Contents/MacOS/Chromium"

	t.Run("full workspace policy emits expected directives", func(t *testing.T) {
		policy := SandboxPolicy{
			FilesystemRules: []PathRule{
				{Path: omnipusHome, Access: AccessRead | AccessWrite | AccessExecute},
				{Path: "/tmp", Access: AccessRead | AccessWrite | AccessExecute},
				{Path: chromePath, Access: AccessRead},
			},
			ConnectPortRules: []NetPortRule{{Port: 53}, {Port: 80}, {Port: 443}},
			BindPortRules:    []NetPortRule{{Port: 5000}},
		}

		out, err := renderSeatbeltProfile(policy)
		require.NoError(t, err)

		// Version + default-deny posture.
		assert.Contains(t, out, "(version 1)")
		assert.Contains(t, out, "(deny default)")

		// $OMNIPUS_HOME RWX → exec + read + write, all subpath-scoped.
		assert.Contains(t, out, `(allow process-exec (subpath "`+omnipusHome+`"))`)
		assert.Contains(t, out, `(allow file-read* (subpath "`+omnipusHome+`"))`)
		assert.Contains(t, out, `(allow file-write* (subpath "`+omnipusHome+`"))`)

		// /tmp RWX.
		assert.Contains(t, out, `(allow process-exec (subpath "/tmp"))`)

		// Chrome path R-only: read present, no write/exec.
		assert.Contains(t, out, `(allow file-read* (subpath "`+chromePath+`"))`)
		assert.NotContains(t, out, `(allow file-write* (subpath "`+chromePath+`"))`)
		assert.NotContains(t, out, `(allow process-exec (subpath "`+chromePath+`"))`)

		// Network default-deny + explicit allow-list.
		assert.Contains(t, out, "(deny network*)")
		assert.Contains(t, out, `(allow network-bind (local tcp "*:5000"))`)
		assert.Contains(t, out, `(allow network-outbound (remote tcp "*:53"))`)
		assert.Contains(t, out, `(allow network-outbound (remote tcp "*:80"))`)
		assert.Contains(t, out, `(allow network-outbound (remote tcp "*:443"))`)
	})

	t.Run("read-only rule emits only file-read", func(t *testing.T) {
		out, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: "/etc/ssl", Access: AccessRead}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, `(allow file-read* (subpath "/etc/ssl"))`)
		assert.NotContains(t, out, "file-write")
		assert.NotContains(t, out, "process-exec")
	})

	t.Run("write-only rule emits only file-write", func(t *testing.T) {
		out, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: "/var/log/omnipus", Access: AccessWrite}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, `(allow file-write* (subpath "/var/log/omnipus"))`)
		assert.NotContains(t, out, "file-read")
	})

	t.Run("execute implies read (cannot mmap without read)", func(t *testing.T) {
		out, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: "/usr/local/bin/helper", Access: AccessExecute}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, `(allow process-exec (subpath "/usr/local/bin/helper"))`)
		assert.Contains(t, out, `(allow file-read* (subpath "/usr/local/bin/helper"))`)
	})

	t.Run("execute plus write emits exec, read, and write", func(t *testing.T) {
		out, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: "/opt/tool", Access: AccessExecute | AccessWrite}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, `(allow process-exec (subpath "/opt/tool"))`)
		assert.Contains(t, out, `(allow file-read* (subpath "/opt/tool"))`)
		assert.Contains(t, out, `(allow file-write* (subpath "/opt/tool"))`)
	})

	t.Run("denies network by default when no port rules", func(t *testing.T) {
		out, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: "/tmp", Access: AccessRead}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, "(deny network*)")
		assert.NotContains(t, out, "network-outbound")
		assert.NotContains(t, out, "network-bind")
	})

	t.Run("bind and connect ports both expand", func(t *testing.T) {
		out, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules:  []PathRule{{Path: "/tmp", Access: AccessRead}},
			BindPortRules:    []NetPortRule{{Port: 8080}, {Port: 8081}},
			ConnectPortRules: []NetPortRule{{Port: 443}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, `(allow network-bind (local tcp "*:8080"))`)
		assert.Contains(t, out, `(allow network-bind (local tcp "*:8081"))`)
		assert.Contains(t, out, `(allow network-outbound (remote tcp "*:443"))`)
	})

	// TestRenderSeatbeltProfile_Rejects* covers the "rejects empty/nil policy
	// cleanly" acceptance criterion plus path-safety validation.

	t.Run("rejects empty policy (zero allows would brick child)", func(t *testing.T) {
		_, err := renderSeatbeltProfile(SandboxPolicy{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zero allows")
	})

	t.Run("rejects filesystem rule with no access flags", func(t *testing.T) {
		_, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: "/tmp"}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no access flags")
	})

	t.Run("rejects relative path", func(t *testing.T) {
		_, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: "relative/path", Access: AccessRead}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must be absolute")
	})

	t.Run("rejects quote-injection path", func(t *testing.T) {
		// An attacker-controlled path containing a `"` could break out of the
		// (subpath "...") filter and inject arbitrary profile directives.
		injected := `/tmp/evil");(allow network*) ; (subpath "x`
		_, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: injected, Access: AccessRead}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "forbidden character")
	})
}

// TestRenderSeatbeltProfile_DeterministicOrder verifies that two calls with
// the same policy produce byte-identical output. Seatbelt profile ordering is
// deterministic (rules emitted in policy order), which matters for caching
// and for auditors diffing profiles.
func TestRenderSeatbeltProfile_DeterministicOrder(t *testing.T) {
	policy := SandboxPolicy{
		FilesystemRules: []PathRule{
			{Path: "/a", Access: AccessRead},
			{Path: "/b", Access: AccessWrite},
		},
		ConnectPortRules: []NetPortRule{{Port: 443}},
	}
	first, err := renderSeatbeltProfile(policy)
	require.NoError(t, err)
	second, err := renderSeatbeltProfile(policy)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

// TestRenderSeatbeltProfile_ProfileShape is a coarse-grained sanity check that
// the rendered profile begins with the expected header sequence and contains
// the allow/deny directives in the right top-level order.
func TestRenderSeatbeltProfile_ProfileShape(t *testing.T) {
	out, err := renderSeatbeltProfile(SandboxPolicy{
		FilesystemRules:  []PathRule{{Path: "/tmp", Access: AccessRead}},
		ConnectPortRules: []NetPortRule{{Port: 443}},
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(out, ";; Omnipus Seatbelt profile"), "profile must start with header comment")
	// (version 1) before (deny default) before filesystem before network.
	verIdx := strings.Index(out, "(version 1)")
	denyIdx := strings.Index(out, "(deny default)")
	fsIdx := strings.Index(out, ";; --- Filesystem")
	netIdx := strings.Index(out, ";; --- Network")
	require.Greater(t, verIdx, -1)
	require.Greater(t, denyIdx, verIdx, "(deny default) must come after (version 1)")
	require.Greater(t, fsIdx, denyIdx, "filesystem block must come after default-deny")
	require.Greater(t, netIdx, fsIdx, "network block must come after filesystem")
}
