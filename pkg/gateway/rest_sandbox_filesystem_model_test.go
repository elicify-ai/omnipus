// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/stretchr/testify/assert"
)

// TestValidFilesystemModels is the regression for DEFECT-UAT-001: the ADR-062
// filesystem model had no operator control at all. It could be read from
// sandbox-status but not SET, so the only way to change it was to hand-edit
// config.json — which meant the `confined` half of the model was untestable
// through the product, and a UAT pass in that mode was blocked outright.
//
// The value is restart-gated like `mode`, because the kernel profile is built
// from it once at boot and not rebuilt in place; persisting it live would leave
// configuration and enforcement disagreeing.
func TestValidFilesystemModels(t *testing.T) {
	assert.True(t, validFilesystemModels["confined"])
	assert.True(t, validFilesystemModels["open"])

	// ADR-062 defines exactly two. Anything else is a caller error, not a value
	// to coerce into a default — a silent coercion here would apply a posture
	// the operator did not choose.
	for _, bad := range []string{"", "Open", "OPEN", "unrestricted", "off", "enforce"} {
		assert.False(t, validFilesystemModels[bad],
			"%q must be rejected, not coerced", bad)
	}
	assert.Len(t, validFilesystemModels, 2, "exactly two models exist")
}

// TestFilesystemModelWireEnum pins the generated enum to the same two values, so
// the contract and the handler cannot drift apart into a state where the API
// advertises a model the handler refuses.
func TestFilesystemModelWireEnum(t *testing.T) {
	assert.Equal(t, "confined", string(gen.SandboxConfigUpdateFilesystemModelConfined))
	assert.Equal(t, "open", string(gen.SandboxConfigUpdateFilesystemModelOpen))

	assert.True(t, validFilesystemModels[string(gen.SandboxConfigUpdateFilesystemModelConfined)],
		"a value the wire contract accepts must be one the handler accepts")
	assert.True(t, validFilesystemModels[string(gen.SandboxConfigUpdateFilesystemModelOpen)],
		"a value the wire contract accepts must be one the handler accepts")
}
