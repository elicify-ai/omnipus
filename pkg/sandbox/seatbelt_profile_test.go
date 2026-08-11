// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyBlock returns only the policy-derived portion of a rendered profile —
// everything from the filesystem-allows marker onward.
//
// Assertions of the form "a read-only rule must not emit file-write" are about
// the rules the POLICY produced, not about the whole file. The macOS system
// preamble legitimately contains file-read*, process-exec and a
// network-outbound allow for the resolver socket, so asserting NotContains
// over the entire profile would fail for reasons that have nothing to do with
// the property under test. Scoping to the policy block keeps those assertions
// meaningful instead of forcing them to be watered down.
func policyBlock(t *testing.T, profile string) string {
	t.Helper()
	const marker = ";; --- Filesystem allows"
	idx := strings.Index(profile, marker)
	require.NotEqual(t, -1, idx, "rendered profile must contain the filesystem-allows marker")
	return profile[idx:]
}

// TestRenderSeatbeltProfile is the table-driven driver covering directive
// presence, network default-deny, chrome/OMNIPUS_HOME allows, and path
// validation.
//
// It runs on any platform, but note that renderSeatbeltProfile is NOT pure
// string generation any more: it resolves symlinks against the real
// filesystem, because Seatbelt matches resolved paths and emitting a declared
// path verbatim would produce filters that silently match nothing on macOS
// (see resolveSeatbeltPath). To stay deterministic across Linux and macOS,
// these cases use paths that do not exist on either — EvalSymlinks then fails
// and the declared path is emitted unchanged. Symlink resolution itself is
// covered separately by TestRenderSeatbeltProfile_ResolvesSymlinkedPaths,
// which builds real symlinks under t.TempDir().
func TestRenderSeatbeltProfile(t *testing.T) {
	// A representative workspace policy: $OMNIPUS_HOME RWX, a scratch dir RWX,
	// the bundled-Chrome path (R-only), plus the default connect ports
	// (DNS/HTTP/HTTPS). None of these paths exist on a test host, which is
	// what makes the expected output identical on Linux and macOS.
	omnipusHome := "/omnipus-test/op/.omnipus"
	scratchDir := "/omnipus-test/scratch"
	chromePath := "/omnipus-test/Chromium.app/Contents/MacOS/Chromium"

	t.Run("full workspace policy emits expected directives", func(t *testing.T) {
		policy := SandboxPolicy{
			FilesystemRules: []PathRule{
				{Path: omnipusHome, Access: AccessRead | AccessWrite | AccessExecute},
				{Path: scratchDir, Access: AccessRead | AccessWrite | AccessExecute},
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

		// Scratch dir RWX.
		assert.Contains(t, out, `(allow process-exec (subpath "`+scratchDir+`"))`)

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
			FilesystemRules: []PathRule{{Path: "/omnipus-test/ssl", Access: AccessRead}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, `(allow file-read* (subpath "/omnipus-test/ssl"))`)
		policy := policyBlock(t, out)
		assert.NotContains(t, policy, "file-write")
		assert.NotContains(t, policy, "process-exec")
	})

	t.Run("write-only rule emits only file-write", func(t *testing.T) {
		out, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules: []PathRule{{Path: "/omnipus-test/log", Access: AccessWrite}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, `(allow file-write* (subpath "/omnipus-test/log"))`)
		assert.NotContains(t, policyBlock(t, out), "file-read")
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
			FilesystemRules: []PathRule{{Path: "/omnipus-test/scratch", Access: AccessRead}},
		})
		require.NoError(t, err)
		assert.Contains(t, out, "(deny network*)")

		// Scoped to everything AFTER the deny: the preamble above it carries a
		// network-outbound allow for the mDNSResponder socket, without which no
		// name resolution works at all on macOS. The property under test is that
		// an empty port policy adds no PORT allows, not that the profile is
		// free of the word "network-outbound".
		idx := strings.Index(out, "(deny network*)")
		require.NotEqual(t, -1, idx)
		afterDeny := out[idx:]
		assert.NotContains(t, afterDeny, "network-outbound")
		assert.NotContains(t, afterDeny, "network-bind")
	})

	t.Run("bind and connect ports both expand", func(t *testing.T) {
		out, err := renderSeatbeltProfile(SandboxPolicy{
			FilesystemRules:  []PathRule{{Path: "/omnipus-test/scratch", Access: AccessRead}},
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

	// Belt-and-suspenders validator (validateSeatbeltPath) must reject control
	// bytes and backslash too, not just the bare quote/newline the original
	// check covered. The PRIMARY defense is strconv.Quote at the render site,
	// but the validator is a complete standalone defense so a path never reaches
	// the renderer with a byte that could be misread by a buggy consumer.
	t.Run("rejects backslash and control-byte paths", func(t *testing.T) {
		tests := []struct {
			name string
			path string
		}{
			{"trailing backslash", "/tmp/evil" + string(rune(0x5C))},
			{"embedded tab", "/tmp/evil\tname"},
			{"embedded newline", "/tmp/evil\nname"},
			{"embedded carriage return", "/tmp/evil\rname"},
			{"embedded NUL", "/tmp/evil" + string(rune(0x00)) + "x"},
			{"embedded DEL (0x7f)", "/tmp/evil" + string(rune(0x7F))},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				_, err := renderSeatbeltProfile(SandboxPolicy{
					FilesystemRules: []PathRule{{Path: tc.path, Access: AccessRead}},
				})
				require.Error(t, err, "path %q must be rejected", tc.path)
				assert.Contains(t, err.Error(), "forbidden character")
			})
		}
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

// TestRenderSeatbeltProfile_ResolvesSymlinkedPaths is the regression guard for
// the macOS defect class this renderer exists to avoid.
//
// Seatbelt matches on the RESOLVED path. On macOS /tmp, /etc and /var are
// symlinks into /private, so a policy granting RWX on /tmp rendered verbatim
// produces `(subpath "/tmp")` — a filter that matches nothing. The profile
// looks correct, the gateway logs a clean apply, and every write the policy
// explicitly permits is denied at runtime. Nothing fails loudly.
//
// Real symlinks under t.TempDir() reproduce that on Linux and macOS alike.
func TestRenderSeatbeltProfile_ResolvesSymlinkedPaths(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real-workspace")
	require.NoError(t, os.Mkdir(realDir, 0o755))

	linkDir := filepath.Join(root, "linked-workspace")
	require.NoError(t, os.Symlink(realDir, linkDir))

	out, err := renderSeatbeltProfile(SandboxPolicy{
		FilesystemRules: []PathRule{{Path: linkDir, Access: AccessRead | AccessWrite}},
	})
	require.NoError(t, err)

	// EvalSymlinks also resolves the ancestors of t.TempDir() itself (on macOS
	// it lives under /var/folders, and /var is a symlink), so compare against
	// the fully resolved target rather than a hand-built string.
	resolved, err := filepath.EvalSymlinks(linkDir)
	require.NoError(t, err)

	assert.Contains(t, out, `(allow file-read* (subpath "`+resolved+`"))`,
		"the allow must name the RESOLVED path or Seatbelt matches nothing")
	assert.Contains(t, out, `(allow file-write* (subpath "`+resolved+`"))`)

	// The declared symlink itself must remain readable, or the child cannot
	// traverse the path the policy actually named.
	assert.Contains(t, out, `(allow file-read* (literal "`+linkDir+`"))`,
		"the symlink must get a traversal read allow")

	// The unresolved path must never be emitted as the access-granting filter.
	assert.NotContains(t, out, `(allow file-write* (subpath "`+linkDir+`"))`,
		"emitting the unresolved path would grant nothing on macOS")
}

// TestSeatbeltSystemPreamble_MandatoryRootRead guards the single most
// load-bearing line in the preamble.
//
// Without `(allow file-read* (literal "/"))` every child dies on SIGABRT with
// completely empty stderr — dyld is killed before it can report anything. The
// symptom is indistinguishable from "Seatbelt is fundamentally broken", so a
// well-meaning cleanup that drops this line as redundant would be extremely
// expensive to diagnose. No subpath allow covers "/" itself.
func TestSeatbeltSystemPreamble_MandatoryRootRead(t *testing.T) {
	assert.Contains(t, seatbeltSystemPreamble, `(allow file-read* (literal "/"))`,
		"removing the root-directory read makes every sandboxed child abort with no diagnostic")

	// DNS on macOS goes through mDNSResponder over a UNIX socket, not UDP/53.
	// Drop either line and every port-scoped network allow silently resolves
	// nothing, which reads like a broken port filter rather than missing DNS.
	assert.Contains(t, seatbeltSystemPreamble, `(allow mach-lookup (global-name "com.apple.mDNSResponder"))`)
	assert.Contains(t, seatbeltSystemPreamble, `(allow network-outbound (literal "/private/var/run/mDNSResponder"))`)

	// The preamble must never grant write access beyond /dev/null, or it would
	// silently widen every policy rendered on top of it.
	for _, line := range strings.Split(seatbeltSystemPreamble, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), ";;") {
			continue
		}
		if strings.Contains(line, "file-write") {
			assert.Contains(t, line, "/dev/null",
				"preamble grants write outside /dev/null: %s", line)
		}
	}
}
