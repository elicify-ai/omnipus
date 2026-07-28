package sandbox_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestGodModeAvailableDefault verifies that GodModeAvailable is true under the
// default build (no nogodmode tag). The SaaS build path (nogodmode) sets it to
// false; that path is exercised by the CI `go build -tags=nogodmode` step.
func TestGodModeAvailableDefault(t *testing.T) {
	t.Parallel()
	if !sandbox.GodModeAvailable {
		t.Error("GodModeAvailable must be true in the default (non-nogodmode) build")
	}
}
