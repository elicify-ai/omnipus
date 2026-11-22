package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SFH-R4-1 corrects the round-3 oracle: remove only the exposed latched save
// value, recursively through Join nodes. Keep every other wrapper WHOLE,
// including its nested save cause. The oracle is the agreed team-lead rule,
// not the current filter implementation.
func TestOpenCodeGateR3_SaveErrorFilterPreservesWrappedIndependentFailure(t *testing.T) {
	saveErr := errors.New("latched save")
	otherErr := errors.New("independent finalize")
	wrapped := fmt.Errorf("finalize: %w", errors.Join(saveErr, otherErr))
	for _, tc := range []struct {
		name        string
		err         error
		saveErr     error
		wantOther   bool
		keepWrapper bool
		passthrough bool
	}{
		{name: "wrapped_join_keeps_independent_failure", err: errors.Join(saveErr, wrapped), saveErr: saveErr, wantOther: true, keepWrapper: true},
		{name: "bare_save_only_is_removed", err: saveErr, saveErr: saveErr},
		{name: "flat_join_keeps_independent_failure", err: errors.Join(saveErr, otherErr), saveErr: saveErr, wantOther: true},
		{name: "nested_joins_remove_every_exposed_save", err: errors.Join(errors.Join(saveErr, saveErr), otherErr), saveErr: saveErr, wantOther: true},
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
			if tc.keepWrapper {
				assert.ErrorIs(t, got, wrapped, "the exact original non-save wrapper is retained, not rebuilt")
				assert.ErrorIs(t, got, saveErr, "a kept wrapper retains its nested save cause as well as the independent cause")
			} else if tc.saveErr != nil {
				assert.False(t, errors.Is(got, saveErr), "every exposed save occurrence is removed recursively through Join nodes")
				if tc.wantOther {
					assert.Equal(t, otherErr.Error(), got.Error(), "the exposed-only flat or nested Join contains only the independent failure afterward")
				}
			}
		})
	}
}
