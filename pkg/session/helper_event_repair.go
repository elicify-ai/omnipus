package session

import (
	"fmt"
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const JournalKindHelperEventRepair = "helper_event_repair"

// HelperEventRepair is a chat-membership repair payload on the helper's
// existing lifecycle journal. Inbox acknowledgement/compaction never owns it.
// not-wire-format: private lifecycle journal envelope.
type HelperEventRepair struct {
	ID            string                `json:"id"`
	SourceID      string                `json:"source_id"`
	Destinations  []string              `json:"destinations"`
	Event         generated.HelperEvent `json:"event"`
	PendingNotice bool                  `json:"pending_notice,omitempty"`
	// GoalOutcome retains the goal record's exact terminal payload until both
	// transcript rows exist, including if a task later reuses that goal id.
	GoalOutcome *generated.GoalOutcome `json:"goal_outcome,omitempty"`
}

// not-wire-format: delivery-only envelope, never a lifecycle state transition.
type helperEventRepairEnvelope struct {
	Kind      string             `json:"kind"`
	SessionID string             `json:"session_id"`
	ID        string             `json:"id"`
	Payload   *HelperEventRepair `json:"payload,omitempty"`
	Retired   bool               `json:"retired,omitempty"`
}

func (s *LifecycleStore) SaveHelperEventRepair(sessionID string, repair HelperEventRepair) error {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return err
	}
	if !strings.HasPrefix(repair.ID, "repair-") || repair.SourceID == "" || len(repair.Destinations) == 0 {
		return fmt.Errorf("helper event repair: id, source and destinations are required")
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	if _, err := s.loadLocked(sessionID); err != nil {
		return err
	}
	pending, err := s.helperEventRepairsLocked(sessionID)
	if err != nil {
		return err
	}
	for _, old := range pending {
		if old.ID == repair.ID {
			return nil
		}
	}
	return fileutil.AppendJSONL(s.path(sessionID), helperEventRepairEnvelope{
		Kind: JournalKindHelperEventRepair, SessionID: sessionID, ID: repair.ID, Payload: &repair,
	})
}

func (s *LifecycleStore) PendingHelperEventRepairs(sessionID string) ([]HelperEventRepair, error) {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return nil, err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	return s.helperEventRepairsLocked(sessionID)
}

func (s *LifecycleStore) helperEventRepairsLocked(sessionID string) ([]HelperEventRepair, error) {
	lines, err := s.readJournal(sessionID)
	if err != nil {
		return nil, err
	}
	pending := make(map[string]HelperEventRepair)
	var order []string
	for _, line := range lines {
		env := line.repair
		if env == nil {
			continue
		}
		if env.SessionID != sessionID || env.ID == "" || (!env.Retired && env.Payload == nil) {
			return nil, fmt.Errorf("helper event repair: invalid envelope in %q", sessionID)
		}
		if env.Retired {
			delete(pending, env.ID)
		} else {
			if _, exists := pending[env.ID]; !exists {
				order = append(order, env.ID)
			}
			pending[env.ID] = *env.Payload
		}
	}
	var out []HelperEventRepair
	for _, id := range order {
		if payload, exists := pending[id]; exists {
			out = append(out, payload)
			delete(pending, id)
		}
	}
	return out, nil
}

// MarkHelperEventRepairPending retains the fact that a pending-save line was
// shown while the destination archive was unavailable. Repair saves that line
// as its own immutable row before retiring the payload.
func (s *LifecycleStore) MarkHelperEventRepairPending(sessionID, id string) error {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	repairs, err := s.helperEventRepairsLocked(sessionID)
	if err != nil {
		return err
	}
	for _, repair := range repairs {
		if repair.ID == id {
			if repair.PendingNotice {
				return nil
			}
			repair.PendingNotice = true
			return fileutil.AppendJSONL(s.path(sessionID), helperEventRepairEnvelope{
				Kind: JournalKindHelperEventRepair, SessionID: sessionID, ID: id, Payload: &repair,
			})
		}
	}
	return fmt.Errorf("helper event repair %q is not pending", id)
}

// RetireHelperEventRepair appends a body-free retirement only after the caller
// confirmed every required destination row. It does not alter terminal state.
func (s *LifecycleStore) RetireHelperEventRepair(sessionID, id string) error {
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return err
	}
	mu := s.Lock(sessionID)
	mu.Lock()
	defer mu.Unlock()
	return fileutil.AppendJSONL(s.path(sessionID), helperEventRepairEnvelope{
		Kind: JournalKindHelperEventRepair, SessionID: sessionID, ID: id, Retired: true,
	})
}
