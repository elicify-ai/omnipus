//go:build darwin && !nogodmode

package tools

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/stretchr/testify/require"
)

// Called only in the isolated inner process: active Seatbelt state is global.
func skillsInstallNativeBackend(t *testing.T, home string) {
	t.Helper()
	backend := sandbox.NewSeatbeltBackend()
	require.True(t, backend.Available(), "BLOCKED: Seatbelt unavailable — r15 §6.2 native child proof")
	require.NoError(t, backend.Apply(sandbox.DefaultPolicyForModel(sandbox.FilesystemModelOpen, home, nil, nil, nil, nil)), "real child-confiner backend, not a policy-only test")
}
