//go:build !cgo

package gateway

// TestMain registers the real gateway.RunContext into pkg/agent/testutil so
// that StartTestGateway can boot the full gateway without creating an import
// cycle (testutil does not import pkg/gateway).

import (
	"os"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
)

func TestMain(m *testing.M) {
	testutil.RegisterGatewayRunner(RunContext)
	os.Exit(m.Run())
}
