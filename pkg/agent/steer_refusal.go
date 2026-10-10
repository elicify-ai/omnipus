// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// #1214 (session-core U7, FR-023): a refused delegate steer/respond is answered
// to its caller with ONE curated sentence. The control was never accepted for
// delivery, so there is no receipt and the caller's answer is the whole outcome
// (architect ruling, U7 Q1). The raw cause - ledger paths, generations, storage
// errors - is logged and stays reachable through errors.Is/As, never shown.
package agent

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// steerRefusalError is a curated refusal: Error() is the publishable sentence,
// Unwrap keeps the cause for callers that branch with errors.Is.
type steerRefusalError struct {
	text  string
	cause error
}

func (e *steerRefusalError) Error() string { return e.text }
func (e *steerRefusalError) Unwrap() error { return e.cause }

// curateSteerRefusal maps the internal error of a refused steer/respond
// enqueue to its caller-facing refusal. A nil error stays nil.
func curateSteerRefusal(scope string, err error) error {
	if err == nil {
		return nil
	}
	var already *steerRefusalError
	if errors.As(err, &already) {
		return err
	}
	switch {
	case errors.Is(err, errSteeringScopeClosed):
		return &steerRefusalError{
			text:  "that helper has already finished; use action=\"resume\" to continue it",
			cause: err,
		}
	case errors.Is(err, errSteeringQueueFull):
		// Already a plain sentence; the joined error may also carry a ledger
		// write failure's text, so only the sentence is shown.
		return &steerRefusalError{text: errSteeringQueueFull.Error(), cause: err}
	case errors.Is(err, session.ErrLifecycleNotFound):
		return &steerRefusalError{text: "no such helper session", cause: err}
	}
	logger.ErrorCF("agent", "steer: instruction refused; caller sees the curated sentence only",
		map[string]any{"session_id": scope, "error": err.Error()})
	return &steerRefusalError{
		text:  "the instruction could not be recorded for that helper; retry. Details are in the server log.",
		cause: err,
	}
}
