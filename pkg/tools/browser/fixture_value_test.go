package browser

import "fmt"

// fixtureValue requires the exact protocol type at the test executor boundary.
// A mismatched fake command remains a hard fixture failure, never a zero value.
func fixtureValue[T any](value any) T {
	typed, ok := value.(T)
	if !ok {
		panic(fmt.Sprintf("fixture value has type %T, expected %T", value, *new(T)))
	}
	return typed
}
