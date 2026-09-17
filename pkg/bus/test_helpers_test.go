package bus

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// jsonUnmarshal is a test helper wrapping json.Unmarshal.
func jsonUnmarshal(data []byte, v any) error {
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("jsonUnmarshal: %w", err)
	}
	return nil
}

// jsonMarshal marshals v and fails the test on error.
func jsonMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return b
}

// jsonContains reports whether the JSON bytes contain a substring.
func jsonContains(b []byte, substr string) (bool, string) {
	s := string(b)
	return strings.Contains(s, substr), s
}
