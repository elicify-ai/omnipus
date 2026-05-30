package commands

import (
	"context"
	"errors"
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// ErrNoActiveTurn is returned by CancelActiveTurn when InterruptSession reports
// that no turn is currently running for the given session. Callers should reply
// with an informational "Nothing to cancel" message rather than treating it as
// a failure.
var ErrNoActiveTurn = errors.New("no active turn")

// AgentLoopInterface is a minimal interface for the agent-loop methods needed by
// the commands runtime. Using an interface here avoids a hard import cycle
// between pkg/commands and pkg/agent.
type AgentLoopInterface interface {
	// InterruptSession requests a graceful interrupt for the turn associated with
	// the given session ID, attaching hint to the audit trail. Returns the list
	// of canceled turn IDs (parent + sub-turns) which the gateway uses for the
	// turn_canceled audit entry; the commands runtime discards it.
	InterruptSession(sessionID, hint string) ([]string, error)
	// InterruptByChannelChat requests a graceful interrupt for all active turns
	// whose channel and chatID match the supplied values. Used by Tier B
	// (text-parsing) channels where inbound messages carry no explicit SessionID.
	InterruptByChannelChat(channel, chatID, hint string) error
	// RequestCancelForSession runs the full cancel state machine (audit, transcript,
	// abuse-detection, approval auto-deny, 2-stage timer) for the given session.
	// All parameters are primitive types so this interface can be defined without
	// importing pkg/agent (which would create a circular dependency).
	//
	// Returns (fired bool, err error): fired is true when an active turn was
	// successfully claimed; err is non-nil only for validation failures.
	RequestCancelForSession(ctx context.Context, sessionID, userID, channel string) (fired bool, err error)
}

// Canceller identifies who/what issued a cancel — populated for audit attribution.
type Canceller struct {
	UserID  string // user-side identity, e.g., "@bob" or "user_abc123"
	Channel string // factory ID, e.g., "telegram" | "slack" | "web" | "cli"
}

// Runtime provides runtime dependencies to command handlers. It is constructed
// per-request by the agent loop so that per-request state (like session scope)
// can coexist with long-lived callbacks (like GetModelInfo).
type Runtime struct {
	Config             *config.Config
	GetModelInfo       func() (name, provider string)
	ListAgentIDs       func() []string
	ListDefinitions    func() []Definition
	ListSkillNames     func() []string
	GetEnabledChannels func() []string
	GetActiveTurn      func() any // Returning any to avoid circular dependency with agent package
	SwitchModel        func(value string) (oldModel string, err error)
	SwitchChannel      func(value string) error
	ClearHistory       func() error
	ReloadConfig       func() error
	// SessionID returns the session key for the current request context. Used by
	// handlers that need to address a specific session (e.g., /cancel).
	SessionID func() string
	// agentLoop is the agent loop implementation used by CancelActiveTurn.
	// Populated by the agent loop via buildCommandsRuntime.
	agentLoop AgentLoopInterface
}

// CancelActiveTurn runs the full cancel state machine (audit, transcript,
// abuse-detection, 2-stage timer) for the given session via the centralized
// RequestCancelForSession entry point. The canceller fields are used for audit
// attribution.
//
// Return values:
//   - nil           — cancel fired (or no active turn — informational).
//   - ErrNoActiveTurn — no running turn found for sessionID.
//   - other error   — a real failure that the caller must surface.
func (rt *Runtime) CancelActiveTurn(ctx context.Context, sessionID string, canceller Canceller) error {
	if rt == nil || rt.agentLoop == nil {
		// No agent loop wired — treat as "nothing to cancel".
		return ErrNoActiveTurn
	}
	fired, err := rt.agentLoop.RequestCancelForSession(ctx, sessionID, canceller.UserID, canceller.Channel)
	if err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	if !fired {
		return ErrNoActiveTurn
	}
	return nil
}

// WithAgentLoop returns a shallow copy of rt with agentLoop set. Used by the
// agent loop's buildCommandsRuntime to inject the loop reference without
// exporting the field directly.
func (rt *Runtime) WithAgentLoop(al AgentLoopInterface) *Runtime {
	clone := *rt
	clone.agentLoop = al
	return &clone
}
