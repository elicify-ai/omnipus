package gateway

// TestMain registers the real gateway.RunContext into pkg/agent/testutil so
// that StartTestGateway can boot the full gateway without creating an import
// cycle (testutil does not import pkg/gateway).

import (
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"

	"github.com/elicify-ai/omnipus/pkg/logger"
)

func TestMain(m *testing.M) {
	testutil.RegisterGatewayRunner(RunContext)
	code := m.Run()
	// Hygiene guard (2026-09-14, after a CI debug session where the real
	// failure text never reached the log): a test that silences the console
	// via logger.DisableConsole and never runs its restore func leaves EVERY
	// later test in this binary unable to log. Fail the package loudly rather
	// than letting the next failure hide its own evidence.
	if code == 0 && logger.ConsoleDisabled() {
		println("gateway TestMain: console logging is still disabled after the test run — a test called logger.DisableConsole() without t.Cleanup on its restore func")
		os.Exit(1)
	}
	os.Exit(code)
}
