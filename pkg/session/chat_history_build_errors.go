package session

import "errors"

// ChatHistoryIndexBuildError classifies offline constructor/publication
// failures without requiring the operator command to inspect error text.
// It is not a gateway wire type. Unwrap preserves native I/O error identity.
type ChatHistoryIndexBuildError struct {
	Class string
	cause error
}

func (e *ChatHistoryIndexBuildError) Error() string { return "history index build: " + e.Class }
func (e *ChatHistoryIndexBuildError) Unwrap() error { return e.cause }

// HistoryIndexBuildClass lets independently based command builds consume the
// classification through an errors.As structural interface after integration.
func (e *ChatHistoryIndexBuildError) HistoryIndexBuildClass() string { return e.Class }
func historyBuildFailure(class string, cause error) error {
	if cause == nil {
		return nil
	}
	var classified *ChatHistoryIndexBuildError
	if errors.As(cause, &classified) {
		return cause
	}
	return &ChatHistoryIndexBuildError{Class: class, cause: cause}
}
