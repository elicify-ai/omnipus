//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Go's %q is not SBPL's escape vocabulary. See validateSeatbeltPath's doc
// comment for the full contract; this file is the executable proof of it,
// against real children under /usr/bin/sandbox-exec.
//
// The class is NOT profile injection, which is why the existing injection
// suite never reached it: strconv.Quote never emits a bare double quote, so
// TestSeatbeltAdversarial_ProfileInjectionRejected stays valid and
// TestSeatbeltAdversarial_MetacharacterPathsStillEnforce keeps passing — it
// only uses ASCII-printable metacharacters, which %q passes through untouched.
// The failure here is an ENCODING mismatch that fails silently OPEN.

// nbspSecretDir builds a directory whose name carries a raw U+00A0 and drops a
// secret file inside it, returning the directory and the secret's path.
//
// The rune is spelled with an explicit \u00a0 escape rather than pasted, so the
// fixture cannot be silently normalised to an ordinary space by an editor,
// a copy-paste, or a tool — which would turn this whole file into a test that
// passes while proving nothing.
//
// The returned dir is SYMLINK-RESOLVED. On macOS t.TempDir() sits under
// /var/folders/..., and /var is a symlink to /private/var -- Seatbelt matches
// the RESOLVED path, so a deny naming the unresolved spelling matches nothing
// for a reason that has nothing to do with escaping. Resolving here keeps the
// escaping the ONLY variable between the two runs below; without it the
// raw-byte control fails and the bug case would pass for the wrong reason. The
// production renderer resolves the same way, via resolveSeatbeltPath.
func nbspSecretDir(t *testing.T) (dir, secret string) {
	t.Helper()

	base, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	dir = filepath.Join(base, "home\u00a0x")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.Contains(t, dir, "\u00a0", "fixture lost its U+00A0; the test would prove nothing")

	secret = filepath.Join(dir, "master.key")
	require.NoError(t, os.WriteFile(secret, []byte("TOPSECRET-NBSP"), 0o600))
	return dir, secret
}

// denyProfileFor builds a profile with the EXACT shape renderSeatbeltProfile
// produces under the ADR-062 open-read model — system preamble, blanket
// (allow file-read*), then the secret-set deny last — with the deny path
// emitted verbatim as supplied.
//
// Callers pass either the %q-escaped spelling (the bug) or the raw path (the
// control), so the only variable between the two runs is the escaping.
func denyProfileFor(denyPathAsEmitted string) string {
	var b strings.Builder
	b.WriteString("(version 1)\n(deny default)\n")
	b.WriteString(seatbeltSystemPreamble)
	b.WriteString("(allow file-read*)\n")
	b.WriteString("(allow process-exec)\n")
	b.WriteString("(deny file-read* (subpath " + denyPathAsEmitted + "))\n")
	b.WriteString("(deny file-write* (subpath " + denyPathAsEmitted + "))\n")
	return b.String()
}

// catUnderProfile runs /bin/cat on path inside profile and returns its combined
// output plus the error. A real child, not a string assertion.
func catUnderProfile(t *testing.T, profile, path string) (string, error) {
	t.Helper()

	if _, err := os.Stat(seatbeltExecPath); err != nil {
		t.Skipf("sandbox-exec unavailable on this host: %v", err)
	}
	cmd := exec.Command(seatbeltExecPath, "-p", profile, "--", "/bin/cat", path)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestSeatbelt_GoQuoteEscapeVoidsDeny_AgainstRealChild is the measurement that
// justifies the rejection rule: the %q spelling of a U+00A0 path enforces
// NOTHING, while the identical deny carrying raw UTF-8 bytes enforces fully.
//
// It asserts the PLATFORM's behaviour, not Omnipus's, so it deliberately builds
// both profiles by hand. If macOS ever starts decoding Go's \u escapes this
// test fails, and the rejection rule in validateSeatbeltPath can be revisited
// with evidence rather than guessed at.
func TestSeatbelt_GoQuoteEscapeVoidsDeny_AgainstRealChild(t *testing.T) {
	dir, secret := nbspSecretDir(t)

	// The spelling renderSeatbeltProfile used to emit, produced by the very
	// same expression the renderer uses.
	goQuoted := fmt.Sprintf("%q", dir)
	require.NotContains(t, goQuoted, "\u00a0",
		"precondition: %%q must have escaped the rune away (that is the bug)")
	require.Contains(t, goQuoted, `\u00a0`,
		"precondition: %%q must have produced a backslash-u escape")

	t.Run("go-quoted deny is silently void", func(t *testing.T) {
		out, err := catUnderProfile(t, denyProfileFor(goQuoted), secret)
		require.NoError(t, err,
			"MEASUREMENT: the %%q-escaped deny is expected to enforce nothing; output=%s", out)
		assert.Contains(t, out, "TOPSECRET-NBSP",
			"MEASUREMENT: the secret is expected to leak straight through the void deny")
	})

	t.Run("raw-byte deny of the same path enforces", func(t *testing.T) {
		// Only the escaping differs. sandbox-exec accepts BOTH profiles without
		// complaint, which is what makes the bug invisible in production.
		out, err := catUnderProfile(t, denyProfileFor(`"`+dir+`"`), secret)
		require.Error(t, err,
			"control: the raw-byte deny must enforce, proving the deny mechanism itself is sound; output=%s", out)
		assert.NotContains(t, out, "TOPSECRET-NBSP")
	})
}

// TestSeatbelt_NonRoundTrippablePathRefusedToRender is the FIX: given the
// measurement above, the renderer must never produce such a profile at all.
//
// A render error is the fail-closed outcome — SeatbeltBackend.Apply propagates
// it and the gateway aborts boot (pkg/gateway/sandbox_apply.go's
// "Seatbelt Apply failed"), and ApplyToCmd propagates it and the spawn aborts.
// Neither path can reach an unconfined child.
func TestSeatbelt_NonRoundTrippablePathRefusedToRender(t *testing.T) {
	// Every one of these is ordinary, not exotic: a non-breaking space is what
	// a path pasted from a web page carries; a zero-width joiner is in every
	// multi-person emoji; an ideographic space is unremarkable in CJK paths;
	// and 0xff is simply not valid UTF-8.
	cases := map[string]string{
		"U+00A0 non-breaking space": "/tmp/home\u00a0x",
		"U+200D zero-width joiner":  "/tmp/team\u200dspace",
		"U+3000 ideographic space":  "/tmp/\u3000cjk",
		"invalid UTF-8 byte":        "/tmp/bad\xffpath",
	}

	for name, bad := range cases {
		t.Run(name+"/denied path", func(t *testing.T) {
			profile, err := renderSeatbeltProfile(SandboxPolicy{
				FilesystemRules: []PathRule{{Path: "/tmp/ws", Access: AccessRead | AccessWrite}},
				DeniedPaths:     []string{bad},
			})
			require.Error(t, err, "a deny that cannot be expressed must not be rendered as one that reads correct")
			assert.Contains(t, err.Error(), "not representable in a Seatbelt filter")
			assert.Empty(t, profile, "no profile may be handed back alongside the error")
		})

		t.Run(name+"/filesystem rule", func(t *testing.T) {
			// The same refusal applies to allows. An allow that matches nothing
			// is a broken child rather than a silent hole, but it is the same
			// encoding defect and must fail at the same place.
			_, err := renderSeatbeltProfile(SandboxPolicy{
				FilesystemRules: []PathRule{{Path: bad, Access: AccessRead}},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "not representable in a Seatbelt filter")
		})
	}
}

// TestSeatbelt_RefusalIsReachedByTheRealBackend closes the loop end to end: the
// refusal is not merely a property of an internal function, it is what a real
// caller gets. Apply refuses to install the profile, and ApplyToCmd refuses to
// wrap a child — so there is no path from a non-representable secret path to a
// running, unprotected process.
func TestSeatbelt_RefusalIsReachedByTheRealBackend(t *testing.T) {
	dir, _ := nbspSecretDir(t)

	policy := SandboxPolicy{
		FilesystemRules: []PathRule{{Path: t.TempDir(), Access: AccessRead | AccessWrite | AccessExecute}},
		DeniedPaths:     []string{filepath.Join(dir, "master.key")},
	}

	backend := NewSeatbeltBackend()

	require.Error(t, backend.Apply(policy),
		"Apply must refuse; the gateway turns this into a boot abort rather than booting unprotected")
	assert.False(t, backend.PolicyApplied(), "no profile may be installed after a refused Apply")

	cmd := exec.Command("/bin/cat", "/etc/hosts")
	require.Error(t, backend.ApplyToCmd(cmd, policy),
		"ApplyToCmd must refuse, aborting the spawn (FR-4.2 fail-closed)")
	assert.NotEqual(t, seatbeltExecPath, cmd.Path,
		"a refused ApplyToCmd must leave cmd untouched, never half-wrapped")
}

// TestSeatbelt_OrdinaryUnicodePathsStillRender guards the blast radius of the
// new rule. Refusing to render aborts boot, so a rule that over-triggers would
// brick installs for reasons an operator cannot act on.
//
// Accented letters, CJK ideographs and ordinary emoji are all unicode.IsPrint,
// so %q round-trips them unchanged and they must keep working — enforced here
// against a real child, not just asserted over the profile text.
func TestSeatbelt_OrdinaryUnicodePathsStillRender(t *testing.T) {
	for _, name := range []string{"café", "日本語", "rocket🚀"} {
		t.Run(name, func(t *testing.T) {
			ws := filepath.Join(t.TempDir(), name)
			require.NoError(t, os.MkdirAll(ws, 0o700))
			readable := filepath.Join(ws, "ok.txt")
			require.NoError(t, os.WriteFile(readable, []byte("UNICODE-OK"), 0o600))

			policy := SandboxPolicy{
				FilesystemRules: []PathRule{{Path: ws, Access: AccessRead | AccessWrite | AccessExecute}},
			}
			profile, err := renderSeatbeltProfile(policy)
			require.NoError(t, err, "a printable Unicode path must still render")

			out, err := catUnderProfile(t, profile, readable)
			require.NoError(t, err, "a printable Unicode path must still be usable by a real child; output=%s", out)
			assert.Contains(t, out, "UNICODE-OK")
		})
	}
}
