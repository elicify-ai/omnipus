package email

// RED (round 8, silent-failure-hunter F8 — LOW). Oracle: FR-018/FR-036
// (failures never silent in the logs) and the helper's own doc contract
// ("The helper never fails: ... a Compose error falls back to the plain
// shape") — the documented degrade is UNLOGGED on HEAD: outgoing mail
// silently loses its multipart/HTML render. The fix: one slog.Warn naming
// the compose fallback when Compose errors on this path.
//
// Trigger (verified by construction probe — inputs only): buildEmailBody
// with an unparseable recipient (all addresses dropped by parseRecipientList)
// plus markdown text sends Compose an empty recipient list, so Compose errors
// ("no recipients given") and the plain-shape fallback runs.

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildEmailBody_ComposeFallbackIsLogged(t *testing.T) {
	var buf strings.Builder
	captureLogs(t, &buf)

	out := buildEmailBody("qa@x.test", "not-an-email", "Compose fallback probe",
		"# Heading\n\nmarkdown body — NOT plain-only", "")

	// The helper never fails — the plain shape is the output either way.
	require.NotEmpty(t, out, "the fallback plain shape must still be produced")
	require.NotContains(t, out, "multipart", "the probe must have taken the fallback (no multipart built)")

	require.Contains(t, buf.String(), "level=WARN",
		"FR-018/FR-036: the Compose-error fallback must log a WARN")
	require.Contains(t, strings.ToLower(buf.String()), "compose",
		"FR-018/FR-036: the WARN must name the compose fallback")
}

func TestBuildEmailBody_Control_ComposeSuccessLogsNoFallbackWarning(t *testing.T) {
	var buf strings.Builder
	captureLogs(t, &buf)

	out := buildEmailBody("qa@x.test", "to@x.test", "Compose success probe",
		"# Heading\n\nmarkdown body — NOT plain-only", "")

	require.Contains(t, out, "boundary=", "the control must take the Compose (multipart) path")
	require.NotContains(t, strings.ToLower(buf.String()), "compose fallback",
		"a successful Compose must not log a fallback warning (the fix must log only the fault)")
}
