package gateway

import "testing"

// D-B5 pins a positive advertised budget range, not a one-value enum. This is
// distinct from the real factory's4194304 default checked in behavioral tests.
func TestChatHistoryPage_AdvertisedBudgetSemanticBoundaries(t *testing.T) {
	for _, value := range []string{"2097152", "4194304", "8388608", "0", "-1", "null", "1.5"} {
		t.Run(value, func(t *testing.T) {
			t.Fatalf("BLOCKED: prepared page factory/semantic-validation seam not implemented — required by D4a/D4b/D10/T27/B23/B38/B46/D-B5; advertised soft_byte_budget=%s: consistent positive integers2097152/4194304/8388608 must be accepted,0/-1/null/fraction rejected, byte_budget below min_rows and override/flag/cursor contradictions rejected atomically; do not mistake Go JSON decoding alone for semantic validation", value)
		})
	}
}
