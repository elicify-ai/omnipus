package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Silent-failure round-3 steer: filtering a latched save error must discard
// NOTHING else, even when a single wrapper encloses a join. The error-membership
// oracle comes from that promise, not from the current filter implementation.
func TestOpenCodeGateR3_SaveErrorFilterPreservesWrappedIndependentFailure(t *testing.T) {
	saveErr := errors.New("latched save")
	otherErr := errors.New("independent finalize")
	wrapped := fmt.Errorf("finalize: %w", errors.Join(saveErr, otherErr))
	for _, tc := range []struct {
		name        string
		err         error
		saveErr     error
		wantOther   bool
		passthrough bool
	}{
		{name: "wrapped_join_keeps_independent_failure", err: errors.Join(saveErr, wrapped), saveErr: saveErr, wantOther: true},
		{name: "bare_save_only_is_removed", err: saveErr, saveErr: saveErr},
		{name: "flat_join_keeps_independent_failure", err: errors.Join(saveErr, otherErr), saveErr: saveErr, wantOther: true},
		{name: "nil_error_with_save_latch", saveErr: saveErr},
		{name: "nil_error_and_nil_latch"},
		{name: "nil_latch_preserves_wrapped_error", err: wrapped, wantOther: true, passthrough: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := withoutSaveFailure(tc.err, tc.saveErr)
			if tc.passthrough {
				assert.Same(t, tc.err, got, "without a save latch the original error is returned unchanged")
				assert.ErrorIs(t, got, saveErr, "nil latch does not filter a coincidentally nested save error")
			}
			if tc.wantOther {
				require.Error(t, got, "filter must never erase the independent finalize failure")
				assert.ErrorIs(t, got, otherErr, "independent finalize cause survives single wrappers as well as flat joins")
			} else {
				assert.NoError(t, got, "there is no remaining cause after removing the lone save error")
			}
			if tc.saveErr != nil {
				assert.False(t, errors.Is(got, saveErr), "the latched save cause itself is removed, not just hidden behind a new join")
			}
		})
	}
}
