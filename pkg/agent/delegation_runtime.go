package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultMaxSubTurnDepth = 3

	// defaultSubTurnTimeout is the built-in lifetime for a delegated/steered
	// child session when neither a per-call timeout_seconds nor
	// performance.delegation_timeout_minutes was configured (D9: 0 = default;
	// founder raised the default from 5 to 30 minutes, 2026-09-23). Its two
	// readers are both child-session paths — effectiveDelegationTimeout (the
	// delegation resolver, below) and the external-CLI runner's per-run
	// fallback (external_dispatch.go::prepareRunOptions, reached only from the
	// task executor, the runner's one remaining caller) — so changing it here
	// moves the default for both, and neither is a path that must stay at 5.
	defaultSubTurnTimeout = 30 * time.Minute
)

type requestedSkillOutcome int

const (
	requestedSkillUnresolvable requestedSkillOutcome = iota
	requestedSkillDenied
	requestedSkillGranted
)

type agentLoopKeyType struct{}

var agentLoopKey = agentLoopKeyType{}

// WithAgentLoop injects AgentLoop into a turn context for tools that need the
// currently executing loop. It is shared by ordinary, task, and steered turns.
func WithAgentLoop(ctx context.Context, al *AgentLoop) context.Context {
	return context.WithValue(ctx, agentLoopKey, al)
}

func (al *AgentLoop) effectiveDelegationTimeout() time.Duration {
	minutes, err := al.cfg.Performance.EffectiveDelegationTimeoutMinutes()
	if err != nil || minutes <= 0 {
		return defaultSubTurnTimeout
	}
	return time.Duration(minutes) * time.Minute
}

// ContextSnapshot is the discretionary, parent-authored portion of a
// delegation context: artifact references and notes, never inherited chat
// history or credentials.
type ContextSnapshot struct {
	References []string
	Notes      string
}

const (
	defaultSnapshotMaxBytes = 8 * 1024
	defaultSnapshotMaxRefs  = 50
)

var ErrSnapshotOverCap = errors.New("agent: curated context snapshot exceeds the discretionary cap")

func ValidateContextSnapshot(snapshot *ContextSnapshot, maxBytes, maxRefs int) error {
	if snapshot == nil {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = defaultSnapshotMaxBytes
	}
	if maxRefs <= 0 {
		maxRefs = defaultSnapshotMaxRefs
	}
	if len(snapshot.References) > maxRefs {
		return fmt.Errorf("%w: %d references exceeds snapshot_max_refs (%d) — narrow the snapshot",
			ErrSnapshotOverCap, len(snapshot.References), maxRefs)
	}
	total := len(snapshot.Notes)
	for _, reference := range snapshot.References {
		total += len(reference)
	}
	if total > maxBytes {
		return fmt.Errorf("%w: %d bytes exceeds snapshot_max_bytes (%d) — narrow the snapshot",
			ErrSnapshotOverCap, total, maxBytes)
	}
	return nil
}

func renderContextSnapshot(snapshot *ContextSnapshot) string {
	if snapshot == nil || (len(snapshot.References) == 0 && strings.TrimSpace(snapshot.Notes) == "") {
		return ""
	}
	var rendered strings.Builder
	if len(snapshot.References) > 0 {
		rendered.WriteString("References:\n")
		for _, reference := range snapshot.References {
			fmt.Fprintf(&rendered, "- %s\n", reference)
		}
	}
	if note := strings.TrimSpace(snapshot.Notes); note != "" {
		if rendered.Len() > 0 {
			rendered.WriteString("\n")
		}
		rendered.WriteString("Notes: ")
		rendered.WriteString(snapshot.Notes)
	}
	return rendered.String()
}
