package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestSeededEdgeDepth_SelfPinAndNonSelfCopy is the unit oracle for ADR-090
// FR-006: "Fresh self-edges explicitly set max_depth 3 (or the lower
// configured ceiling)." Expected values come from that sentence, not from
// observed output. The helper must pin only Jim→Jim and Worker→Worker; every
// other pair copies the policy-wide depth (nil stays inherit).
func TestSeededEdgeDepth_SelfPinAndNonSelfCopy(t *testing.T) {
	policyTwo := 2
	cases := []struct {
		name       string
		from, to   string
		policy     *int
		ceiling    int
		wantNil    bool
		want       int
		keepPolicy bool // returned pointer must not alias policy
	}{
		{"jim self, unset ceiling pins 3", "jim", "jim", nil, 0, false, 3, false},
		{"jim self, ceiling 1 clamps to 1", "jim", "jim", nil, 1, false, 1, false},
		{"jim self, ceiling 2 clamps to 2", "jim", "jim", nil, 2, false, 2, false},
		{"jim self, ceiling 3 pins 3", "jim", "jim", nil, 3, false, 3, false},
		{"jim self, ceiling 5 still pins 3 (F3)", "jim", "jim", nil, 5, false, 3, false},
		{"worker self, ceiling 2 clamps to 2", "worker", "worker", nil, 2, false, 2, false},
		{"worker self, ceiling 5 still pins 3", "worker", "worker", nil, 5, false, 3, false},
		{"jim→ava nil stays inherit at ceiling 5", "jim", "ava", nil, 5, true, 0, false},
		{"jim→worker nil stays inherit at ceiling 2", "jim", "worker", nil, 2, true, 0, false},
		{"planner policy depth copies verbatim", "planner", "researcher", &policyTwo, 5, false, 2, true},
		{"non-permitted ava→ava is not pinned", "ava", "ava", nil, 3, true, 0, false},
		{"non-self with a policy depth is not replaced by the pin", "jim", "ava", &policyTwo, 5, false, 2, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := coreagent.SeededEdgeDepth(tc.from, tc.to, tc.policy, tc.ceiling)
			if tc.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.want, *got)
			if tc.keepPolicy {
				require.NotNil(t, tc.policy)
				assert.NotSame(t, tc.policy, got, "must copy the policy depth, not alias the seed pointer")
				*got = tc.want + 10
				assert.Equal(t, 2, *tc.policy, "mutating the returned pointer must not change the seed policy")
			}
		})
	}
}
