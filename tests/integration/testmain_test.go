// Package integration contains outcome-level regression tests for bugs surfaced
// in manual E2E testing. These tests MUST fail before the corresponding fixes
// land and MUST pass after.
//
// The package uses the same patterns as tests/perf:
//   - startPerfGateway from tests/perf is not reused directly (different package),
//     instead we mirror the approach using testutil.StartTestGateway + WithAPIBase.
//   - RegisterGatewayRunner is called here so StartTestGateway works.
//   - All tests gate on the mock OpenRouter — no real API key required.
package integration

import (
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/gateway"
)

func TestMain(m *testing.M) {
	testutil.RegisterGatewayRunner(gateway.RunContext)
	os.Exit(m.Run())
}
