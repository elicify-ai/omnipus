// Omnipus — exact question-card identity for one Stop request (#1343)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

type pendingAskStopContextKey struct{}

type pendingAskStopTarget struct {
	cardID string
	done   chan struct{}
	err    error
}

// One fresh Stop binds each session once. Repeated callbacks for the same
// accepted Stop share the first result instead of consuming its successor.
// Continuations have no new card intent and must not touch a later head.
type pendingAskStopTargets struct {
	mu      sync.Mutex
	targets map[string]*pendingAskStopTarget
	skip    bool
}

func (al *AgentLoop) bindPendingAskStop(ctx context.Context, sessionID string, continuation bool) context.Context {
	targets := &pendingAskStopTargets{targets: make(map[string]*pendingAskStopTarget), skip: continuation}
	if !continuation {
		targets.capture(al, sessionID)
	}
	return context.WithValue(ctx, pendingAskStopContextKey{}, targets)
}

func (t *pendingAskStopTargets) capture(al *AgentLoop, sessionID string) *pendingAskStopTarget {
	t.mu.Lock()
	defer t.mu.Unlock()
	if target, exists := t.targets[sessionID]; exists {
		return target
	}
	target := &pendingAskStopTarget{}
	if registry := al.getAskUserRegistry(); registry != nil {
		if head, exists := registry.PendingForSession(sessionID); exists && head != nil {
			target.cardID = head.CardID
		}
	}
	t.targets[sessionID] = target
	return target
}

func (t *pendingAskStopTargets) cancel(al *AgentLoop, sessionID string) error {
	if t.skip || sessionID == "" {
		return nil
	}
	target := t.capture(al, sessionID)
	t.mu.Lock()
	if target.done != nil {
		done := target.done
		t.mu.Unlock()
		<-done
		return target.err
	}
	target.done = make(chan struct{})
	t.mu.Unlock()

	var err error
	registry := al.getAskUserRegistry()
	if registry != nil {
		_, err = registry.CancelOnSessionStopForCard(sessionID, target.cardID)
	} else if target.cardID != "" {
		err = fmt.Errorf("pending-question registry unavailable; the targeted card was not cancelled")
	}
	if err != nil {
		err = fmt.Errorf("cancel pending question for session %s: %w", sessionID, err)
	}
	t.mu.Lock()
	target.err = err
	close(target.done)
	t.mu.Unlock()
	return err
}

// The durable Stop may skip an already-landed root entirely. Only a fresh
// request for that still-stopped generation may retry its captured card.
func (al *AgentLoop) retryStoppedPendingAsk(ctx context.Context, sessionID string, generation int, report *steer.CancelReport) error {
	if slices.Contains(report.SkippedNewerGeneration, sessionID) || slices.Contains(report.Superseded, sessionID) {
		return nil
	}
	if al.getAskUserRegistry() == nil {
		return al.cancelPendingAskForScope(ctx, sessionID)
	}
	store := al.GetSessionLifecycleStore()
	if store == nil {
		return fmt.Errorf("cannot verify stopped session before pending-question retry")
	}
	record, err := store.Load(sessionID)
	if err != nil {
		return fmt.Errorf("read stopped session before pending-question retry: %w", err)
	}
	if record.Generation > generation {
		report.SkippedNewerGeneration = append(report.SkippedNewerGeneration, sessionID)
		return nil
	}
	if record.Generation != generation {
		return fmt.Errorf("session generation changed; no pending question was cancelled")
	}
	if record.State != session.LifecycleStopped {
		return nil
	}
	return al.cancelPendingAskForScope(ctx, sessionID)
}
