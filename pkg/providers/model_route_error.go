package providers

import "fmt"

// ModelRouteUnavailableError refuses a named route before a provider request.
// Cause is diagnostic-only: user-facing translation uses the fixed contract
// sentence, never a constructor error or a fabricated HTTP status.
type ModelRouteUnavailableError struct {
	Provider string
	Model    string
	Cause    error
}

func (e *ModelRouteUnavailableError) Error() string {
	return fmt.Sprintf("model route unavailable: provider=%s model=%s: %v", e.Provider, e.Model, e.Cause)
}
func (e *ModelRouteUnavailableError) Unwrap() error { return e.Cause }
