package secretrequest_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Assertion-output recording, not a duplicated matcher or source-text check.
// The real shared guidance helper evaluates both strings unchanged.
type registryGuidanceRecorder struct{ failures []string }

func (*registryGuidanceRecorder) Helper() {}

func (r *registryGuidanceRecorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func TestWU4RegistryGuidanceOracleAcceptsProvideKeyAndRejectsNoRepairInstruction(t *testing.T) {
	positive := &registryGuidanceRecorder{}
	assertRegistryRepairGuidance(positive, "credentials: store is locked — provide master key via OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, or interactive passphrase")
	require.Empty(t, positive.failures, "oracle defect: valid English verb omitted; 'provide master key' IS an actionable repair instruction")
	negative := &registryGuidanceRecorder{}
	assertRegistryRepairGuidance(negative, "saved secret request state is unavailable")
	require.Len(t, negative.failures, 1, "no repair instruction must still be rejected by the REAL helper")
	require.Contains(t, negative.failures[0], "safe error must give an actionable next step")
}
