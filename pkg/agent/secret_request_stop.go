// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/session"
)

type secretStopIntentContextKey struct{}
type secretStopIntentObserver func(string) error

// withSecretStopIntent adds a request-scoped side effect to the existing Stop
// traversal, including already-landed and late-child visits. The old lifecycle
// note cannot establish the cause of THIS request. A genuine restart interruption
// (or its finisher) therefore omits the observer explicitly instead of inferring
// intent from the target record. No registry lock is taken under a record lock.
func (al *AgentLoop) withSecretStopIntent(ctx context.Context, cause session.StopCause) context.Context {
	if cause == session.StopCauseRestart {
		return context.WithValue(ctx, secretStopIntentContextKey{}, secretStopIntentObserver(nil))
	}
	return context.WithValue(ctx, secretStopIntentContextKey{}, secretStopIntentObserver(al.persistSecretStopIntent))
}

func (al *AgentLoop) persistSecretStopIntent(sessionID string) error {
	al.secretRequests.mu.Lock()
	registry := al.secretRequests.registry
	al.secretRequests.mu.Unlock()
	if registry == nil {
		return nil
	}
	return registry.Stop(sessionID)
}

func observeSecretStopIntent(ctx context.Context, sessionID string) error {
	observer, _ := ctx.Value(secretStopIntentContextKey{}).(secretStopIntentObserver)
	if observer == nil {
		return nil
	}
	if err := observer(sessionID); err != nil {
		return fmt.Errorf("secret continuation Stop veto could not be saved: %w", err)
	}
	return nil
}
