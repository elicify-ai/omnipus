package secretrequest_test

import (
	"log/slog"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

// Instrument control, NOT a claim that production emitted a fault diagnosis.
// Both public logging front-ends must preserve Error, context, stage and cause
// into the exact sink and matcher used by the actual-boundary fault tests.
func TestWU4RegistryDiagnosticCapturePreservesContextFromBothPublicLoggers(t *testing.T) {
	c := newRegistryFaultCapture(t)
	logger.ErrorCF("registry-test", "Check storage permissions and retry", map[string]any{
		"request_id": "native-control-request", "session_id": "native-control-session",
		"stage": "instrument", "cause_category": "permission_denied",
	})
	slog.Error("Unlock the credential store and retry",
		"request_id", "structured-control-request", "session_id", "structured-control-session",
		"stage", "instrument", "cause_category", "store_locked")
	entries := c.logs(t)
	assertRegistryDiagnostic(t, entries, "native-control-request", "native-control-session", "permission_denied", "instrument")
	assertRegistryDiagnostic(t, entries, "structured-control-request", "structured-control-session", "store_locked", "instrument")
}
