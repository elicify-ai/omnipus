package launcher

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// T-76 / office-preview §3.1 D1: address-space is deleted, not retained as
// an alternative. This parser leg is portable; Linux enforcement is separate.
func TestRunLimitedRejectsMemoryKind(t *testing.T) {
	for _, kind := range []string{"as", "data", "anything"} {
		t.Run(kind, func(t *testing.T) {
			_, _, err := parseArgs([]string{"--mem-kind=" + kind, "--", "verified-target"})
			require.Error(t, err, "T-76: every --mem-kind value is an unknown option")
			require.Contains(t, err.Error(), "unknown")
			require.Contains(t, err.Error(), "mem-kind")
		})
	}
}
