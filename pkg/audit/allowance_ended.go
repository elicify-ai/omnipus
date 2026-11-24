// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EventApprovalAllowanceEnded records one timed allowance ended by a change
// of the stored permission it answered (Skills & Tools C-26 point 7).
const EventApprovalAllowanceEnded = "approval.allowance_ended"

// AllowanceEndedRecord is the audit line of one ended allowance: whose it
// was, what it covered, and the permission change that ended it.
type AllowanceEndedRecord struct {
	Timestamp string `json:"timestamp"`
	Event     string `json:"event"`
	Actor     string `json:"actor"`
	AgentID   string `json:"agent_id"`
	Kind      string `json:"kind"`
	Subject   string `json:"subject"`
	Key       string `json:"key"`
	Old       string `json:"old"`
	New       string `json:"new"`
}

// AllowanceEnded is one ended allowance and the change that ended it.
type AllowanceEnded struct {
	AgentID, Kind, Subject string
	Key, Old, New          string
}

// EmitAllowanceEnded writes one approval.allowance_ended line, synced to
// disk like the permission change it follows. The actor is the signed-in
// person of ctx; a system change (a config reload) names "system".
func EmitAllowanceEnded(ctx context.Context, logger *Logger, e AllowanceEnded) error {
	if logger == nil {
		return nil
	}
	actor := extractActor(ctx)
	if actor == "" {
		actor = "system"
	}
	data, err := json.Marshal(AllowanceEndedRecord{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Event:     EventApprovalAllowanceEnded,
		Actor:     actor,
		AgentID:   e.AgentID, Kind: e.Kind, Subject: e.Subject,
		Key: e.Key, Old: e.Old, New: e.New,
	})
	if err != nil {
		return fmt.Errorf("audit: marshal %s: %w", EventApprovalAllowanceEnded, err)
	}
	if err := logger.writeLine(data, true); err != nil {
		return fmt.Errorf("audit: write %s: %w", EventApprovalAllowanceEnded, err)
	}
	return nil
}
