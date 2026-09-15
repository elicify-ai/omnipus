// loop_window_test.go: tests for sliding window and model switch

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- moved from loop.go tests 2026-09-15 ---

// =============================================================================
// W2-27 — decideSwitchCompressAction edge cases (empty / negative inputs)
// =============================================================================
//
// W2-27 (test-analyzer-A #14) flagged that the function is never tested
// with empty or negative inputs. The function is pure and must guard
// against these without panicking.
func TestDecideSwitchCompressAction_EmptyAndNegativeInputs(t *testing.T) {
	cases := []struct {
		name     string
		cur      int
		win      int
		expected SwitchAction
	}{
		{"zero current", 0, 8000, SwitchActionNoop},
		{"negative current", -1, 8000, SwitchActionNoop},
		{"zero window", 5000, 0, SwitchActionNoop},
		{"negative window", 5000, -100, SwitchActionNoop},
		{"both zero", 0, 0, SwitchActionNoop},
		{"both negative", -1, -1, SwitchActionNoop},
		// Sanity: a real call still works.
		{"real compress", 50000, 8000, SwitchActionCompress},
		{"real noop", 5000, 8000, SwitchActionNoop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideSwitchCompressAction(tc.cur, tc.win)
			assert.Equal(t, tc.expected, got,
				"cur=%d win=%d expected %s, got %s",
				tc.cur, tc.win, tc.expected, got)
		})
	}
}
