//go:build !cgo

// Package security_test — PR-D security test stack.
//
// TestMain registers the real gateway.RunContext into pkg/agent/testutil so
// that StartTestGateway can boot the full gateway without creating an import
// cycle.
//
// This file retains the //go:build !cgo tag because it imports pkg/gateway,
// which itself carries //go:build !cgo (the gateway uses modernc.org/sqlite and
// other pure-Go dependencies that conflict with CGO). The remaining test files in
// this package do NOT have the !cgo tag; they only test HTTP paths and do not
// import the gateway package directly. Under CGO_ENABLED=1, TestMain is excluded,
// so these tests must be run with CGO_ENABLED=0 or the goolm,stdjson tags.
//
// All tests in this package share this TestMain.
package security_test

import (
	"os"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/gateway"
)

func TestMain(m *testing.M) {
	testutil.RegisterGatewayRunner(gateway.RunContext)
	os.Exit(m.Run())
}
