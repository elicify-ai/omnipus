package gateway

// Approved POST-API formatter baselines; no original-source RED is claimed.
// Inputs/wants come ONLY from the sealed pending oracle file, not from output.
import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoalPauseB1PostAPIFormatter(t *testing.T) {
	cases := b1PendingReasonCases()
	require.Len(t, cases, 2, "exactly the two required pending formatter records")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, goalPausedReason(tc.input), "actual private formatter must preserve the exact frozen raw input, including short trailing whitespace")
		})
	}
}
