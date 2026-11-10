package gateway

import (
	"errors"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// historyPageFaultEvent is private deterministic instrumentation of the actual
// prepared values. It has no configuration, wire or environment exposure.
type historyPageFaultEvent struct {
	SessionID string
	Point     string
	Weight    *session.ChatHistoryWeight
	Prepared  *preparedHistoryPage
}

// historyPageFaultHook is nil in production. Call sites belong to the shared
// preparation pipeline; tests may alter a value, never bypass its validator.
var historyPageFaultHook func(historyPageFaultEvent)

var errHistoryPageProtocol = errors.New("history page protocol violation")
