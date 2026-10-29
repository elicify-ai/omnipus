// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import "errors"

var (
	ErrChatHistoryNotImplemented = errors.New("chat history: not implemented")
	ErrChatHistoryUnavailable    = errors.New("chat history unavailable")
	ErrHistoryPositionNotFound   = errors.New("history position not found")
	ErrInvalidHistoryPosition    = errors.New("invalid history position")
	ErrInvalidHistoryPageRange   = errors.New("invalid history page range")
	ErrHistoryProjection         = errors.New("history projection failed")
	// ErrHistoryEntryExpired: a same-session position proven expired by its
	// retention tombstone. Never inferred from a missing or corrupt index.
	ErrHistoryEntryExpired = errors.New("history entry expired")
)

// ChatHistoryUnavailableError carries a fixed reason separately from its
// private cause. Error deliberately exposes neither the cause nor its paths.
type ChatHistoryUnavailableError struct {
	Reason string
	cause  error
}

func (e *ChatHistoryUnavailableError) Error() string {
	return ErrChatHistoryUnavailable.Error()
}

func (e *ChatHistoryUnavailableError) Unwrap() error {
	return e.cause
}

func (e *ChatHistoryUnavailableError) Is(target error) bool {
	return target == ErrChatHistoryUnavailable ||
		(target == ErrHistoryProjection && e.Reason == "projection_error")
}

// historyIsStorageFailure reports an already typed storage_error.
func historyIsStorageFailure(err error) bool {
	var unavailable *ChatHistoryUnavailableError
	return errors.As(err, &unavailable) && unavailable.Reason == "storage_error"
}
