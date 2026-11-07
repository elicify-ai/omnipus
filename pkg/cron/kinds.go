package cron

// kinds.go — the registered job kinds and the kind→handler dispatcher of the
// one timer engine (T10 spec r6 FR-01, FR-02, FR-14, FR-22, FR-25).
//
// Exactly three kinds exist: heartbeat, loop and task. A job of any other
// kind — empty, unknown, or the retired agent_turn (whose only producer was
// the removed schedules API) — is refused at creation, dropped once and
// loudly at load, and refused at dispatch as a last defence.

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// The registered job kinds. Every producer sets its kind in the creating write.
const (
	KindHeartbeat = "heartbeat"
	KindLoop      = "loop"
	KindTask      = "task"
)

// registeredKinds is the closed set of kinds the engine keeps and dispatches.
var registeredKinds = map[string]bool{KindHeartbeat: true, KindLoop: true, KindTask: true}

// IsRegisteredKind reports whether kind is one of heartbeat, loop or task.
func IsRegisteredKind(kind string) bool { return registeredKinds[kind] }

// ErrUnregisteredKind is returned (wrapped) for a job whose kind is not one of
// the registered kinds.
var ErrUnregisteredKind = errors.New("cron: job kind is not registered (registered kinds: heartbeat, loop, task)")

func unregisteredKindError(kind string) error {
	return fmt.Errorf("%w: kind %q", ErrUnregisteredKind, kind)
}

// RegisterKindHandler installs the handler that runs every job of kind. The
// handler is called outside the engine lock (FR-22), so it may call back into
// the engine. Registering a kind twice replaces the previous handler; a kind
// with no handler falls back to the runner installed by SetRunner.
func (cs *CronService) RegisterKindHandler(kind string, handler ScheduledRunner) error {
	if !IsRegisteredKind(kind) {
		return unregisteredKindError(kind)
	}
	if handler == nil {
		return fmt.Errorf("cron: nil handler for kind %q", kind)
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.kindHandlers == nil {
		cs.kindHandlers = make(map[string]ScheduledRunner, len(registeredKinds))
	}
	cs.kindHandlers[kind] = handler
	return nil
}

// handlerForUnsafe resolves the handler for kind: the registered kind handler
// when one is installed, otherwise the SetRunner fallback. Caller holds cs.mu.
func (cs *CronService) handlerForUnsafe(kind string) ScheduledRunner {
	if h, ok := cs.kindHandlers[kind]; ok {
		return h
	}
	return cs.runner
}

// dropUnregisteredKindsUnsafe removes every loaded job whose kind is not
// registered, logging one WARN record per dropped job (FR-14, FR-25), and
// returns how many it dropped. Caller holds cs.mu or owns cs exclusively.
func (cs *CronService) dropUnregisteredKindsUnsafe() int {
	kept := cs.store.Jobs[:0]
	dropped := 0
	for _, job := range cs.store.Jobs {
		if IsRegisteredKind(job.Payload.Kind) {
			kept = append(kept, job)
			continue
		}
		dropped++
		slog.Warn("cron: dropped a stored job of a non-registered kind",
			"job_id", job.ID, "job", job.Name, "kind", job.Payload.Kind,
			"registered_kinds", strings.Join([]string{KindHeartbeat, KindLoop, KindTask}, ","),
			"store", cs.storePath)
	}
	cs.store.Jobs = kept
	return dropped
}
