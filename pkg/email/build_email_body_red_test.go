package email

// Round-2 delta review, item 5 (silent-failure-hunter): a Compose failure on
// buildEmailBody's Markdown-structured path used to log a WARN and fall back
// to the plain shape (round 8, silent-failure-hunter F8) — logged, but still
// silent to the CALLER: the return value was indistinguishable from a normal
// plain-text send. This file's two tests used to pin that logged fallback;
// they now pin the stronger fix — the caller gets a visible error instead
// of a return value it cannot tell apart from success.

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildEmailBody_ComposeFailure_ReturnsErrorNotFallback(t *testing.T) {
	// "not-an-email" is not a parseable recipient, so parseRecipientList
	// drops it; buildEmailBody's own toList ends up empty, Compose gets zero
	// addresses, and Compose fails with "no recipients given".
	out, err := buildEmailBody("qa@x.test", "not-an-email", "Compose fallback probe",
		"# Heading\n\nmarkdown body — NOT plain-only", "")

	require.Error(t, err,
		"a Compose failure must be returned to the caller as a visible error, never silently swallowed into a degraded plain-text send")
	require.Empty(t, out,
		"no body may be produced on a Compose failure")
}

func TestBuildEmailBody_Control_ComposeSuccessReturnsNoError(t *testing.T) {
	out, err := buildEmailBody("qa@x.test", "to@x.test", "Compose success probe",
		"# Heading\n\nmarkdown body — NOT plain-only", "")

	require.NoError(t, err, "a successful Compose must not return an error")
	require.Contains(t, out, "boundary=", "the control must take the Compose (multipart) path")
}
