package agent

import "testing"

// effortRequireType preserves the original strict type premise with a useful
// failure message. Some callers run in HTTP handlers: record the test failure
// and panic rather than invoking testing.T.FailNow from a non-test goroutine.
func effortRequireType[T any](t *testing.T, raw any) T {
	t.Helper()
	value, ok := raw.(T)
	if !ok {
		t.Errorf("effort test boundary: got %T, want %T", raw, value)
		panic("effort test boundary: type mismatch")
	}
	return value
}
