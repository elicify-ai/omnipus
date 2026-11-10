// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Pending-question record (ADR-20260928 D1.7). This is not LifecycleRecord
// and not NeedsInput. persistLocked clears NeedsInput outside needs_input;
// Stop and Stop all must leave this record, including its original deadline,
// where a fresh store can read it. not-wire-format: server-internal.

const (
	// QuestionAuthoritySelfOK is a question the steering parent may answer.
	QuestionAuthoritySelfOK = "self_ok"
	// QuestionAuthorityOwnerRequired is a question only the signed-in owner may answer.
	QuestionAuthorityOwnerRequired = "owner_required"

	// QuestionStatusOpen is the one unanswered question for this asker.
	QuestionStatusOpen = "open"
	// QuestionStatusApplied means the answer was delivered and the asker was resumed.
	QuestionStatusApplied = "applied"
	// QuestionStatusSuperseded means a withdrawal or a winning other answer closed it.
	QuestionStatusSuperseded = "superseded"
	// QuestionStatusAnswerPendingDelivery means the answer was reserved but not
	// finished. A later attempt must deliver this text, not take a second answer.
	QuestionStatusAnswerPendingDelivery = "answer_pending_delivery"
)

// ErrPendingQuestionNotFound is returned when a session has no question record.
var ErrPendingQuestionNotFound = errors.New("session: pending question: not found")

// QuestionRelayLink is one hop in the relay chain. Empty on a direct question.
type QuestionRelayLink struct {
	SessionID     string `json:"session_id"`
	CorrelationID string `json:"correlation_id"`
}

// PendingQuestion is one open question identity. The current line is the tail
// of <lifecycle-dir>/pending_questions/<session_id>.jsonl. It may name a
// control id owned by the W2 ledger; this store does not mint that seq.
type PendingQuestion struct {
	CorrelationID    string             `json:"correlation_id"`
	AskerSessionID   string             `json:"asker_session_id"`
	AskerGeneration  int                `json:"asker_generation"`
	Authority        string             `json:"authority"`
	OriginalDeadline time.Time          `json:"original_deadline"`
	ShownSessionID   string             `json:"shown_session_id,omitempty"`
	ShownOrdinal     int64              `json:"shown_ordinal,omitempty"`
	RelayOf          *QuestionRelayLink `json:"relay_of,omitempty"`
	Origin           *QuestionRelayLink `json:"origin,omitempty"`
	ControlID        string             `json:"control_id,omitempty"`
	Status           string             `json:"status"`
	AnswerText       string             `json:"answer_text,omitempty"`
}

// Expired reports whether now is at or past the original deadline. A zero
// deadline is not expired. Stop must not rewrite this instant.
func (q *PendingQuestion) Expired(now time.Time) bool {
	if q == nil || q.OriginalDeadline.IsZero() {
		return false
	}
	return !now.Before(q.OriginalDeadline)
}

// Answerable is true while a respond may still reserve this question.
func (q *PendingQuestion) Answerable() bool {
	if q == nil {
		return false
	}
	return q.Status == QuestionStatusOpen || q.Status == QuestionStatusAnswerPendingDelivery
}

// PendingQuestionDir is the directory beside a lifecycle store. Lifecycle
// List reads only the lifecycle directory itself, so these files are not
// session records.
func PendingQuestionDir(lifecycleDir string) string {
	return filepath.Join(lifecycleDir, "pending_questions")
}

// QuestionStore is the durable pending-question reader and writer. Methods
// do not take the lifecycle lock. A caller that must serialize with park,
// stop, or respond holds LifecycleStore.Lock for that session across the
// read-modify-append, and must not call LifecycleStore.Mutate while holding it.
type QuestionStore struct {
	dir string
}

// NewQuestionStore stores records under dir, usually PendingQuestionDir.
func NewQuestionStore(dir string) *QuestionStore {
	return &QuestionStore{dir: dir}
}

// Dir returns the store directory.
func (s *QuestionStore) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Load returns the current question for sessionID. The current line is the
// last valid JSON line, so a torn tail falls back to the previous record.
func (s *QuestionStore) Load(sessionID string) (*PendingQuestion, error) {
	if s == nil || s.dir == "" {
		return nil, fmt.Errorf("session: pending question: store is not configured")
	}
	if err := validateLifecycleSessionID(sessionID); err != nil {
		return nil, err
	}
	q, found, err := s.tail(sessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrPendingQuestionNotFound
	}
	return q, nil
}

// Append validates q and appends it as the new current record.
func (s *QuestionStore) Append(q PendingQuestion) error {
	if s == nil || s.dir == "" {
		return fmt.Errorf("session: pending question: store is not configured")
	}
	if err := validatePendingQuestion(q); err != nil {
		return err
	}
	q.OriginalDeadline = q.OriginalDeadline.UTC()
	return fileutil.AppendJSONL(s.path(q.AskerSessionID), q)
}

func (s *QuestionStore) path(sessionID string) string {
	return filepath.Join(s.dir, sessionID+".jsonl")
}

func (s *QuestionStore) tail(sessionID string) (*PendingQuestion, bool, error) {
	f, err := os.Open(s.path(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("session: pending question: open %q: %w", sessionID, err)
	}
	defer f.Close()

	var last *PendingQuestion
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var q PendingQuestion
		if err := json.Unmarshal([]byte(line), &q); err != nil {
			continue
		}
		copied := q
		last = &copied
	}
	if err := scanner.Err(); err != nil {
		return nil, false, fmt.Errorf("session: pending question: scan %q: %w", sessionID, err)
	}
	return last, last != nil, nil
}

func validatePendingQuestion(q PendingQuestion) error {
	if err := validateLifecycleSessionID(q.AskerSessionID); err != nil {
		return err
	}
	if strings.TrimSpace(q.CorrelationID) == "" {
		return fmt.Errorf("session: pending question: correlation_id is required")
	}
	if q.AskerGeneration < 1 {
		return fmt.Errorf("session: pending question: asker_generation must be >= 1")
	}
	switch q.Authority {
	case QuestionAuthoritySelfOK, QuestionAuthorityOwnerRequired:
	default:
		return fmt.Errorf("session: pending question: invalid authority %q", q.Authority)
	}
	switch q.Status {
	case QuestionStatusOpen, QuestionStatusApplied, QuestionStatusSuperseded, QuestionStatusAnswerPendingDelivery:
	default:
		return fmt.Errorf("session: pending question: invalid status %q", q.Status)
	}
	if q.OriginalDeadline.IsZero() {
		return fmt.Errorf("session: pending question: original_deadline is required")
	}
	return nil
}
