// Package perf contains Go benchmarks and SLO gate tests for Plan 3 PR-C.
//
// TestMain registers the real gateway.RunContext into pkg/agent/testutil so
// that StartTestGateway can boot the full gateway without creating an import
// cycle.
//
// This file carries no build tag: it compiles under both CGO_ENABLED=0 and
// CGO_ENABLED=1, so the package is race-testable. Build with the canonical
// -tags goolm,stdjson.
//
// All benchmark files in this package share this TestMain.
package perf

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
