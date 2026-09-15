// Package notifications implements a file-based, per-user notification store
// for the scheduled-agent-autonomy feature (#264). Each notification belongs to
// a single recipient (a username). On failure of a scheduled run, the gateway
// creates a notification for the schedule's creator and the owning agent's
// owner, coalescing repeated failures of the same schedule into one item so a
// flapping schedule does not spam the bell.
//
// Storage layout: one JSON file per recipient under
// $OMNIPUS_HOME/notifications/<recipient>.json, written atomically via
// fileutil.WriteFileAtomic (temp + rename). Retention keeps the most recent
// notificationCap items per user.
package notifications

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/pathsafe"
)

// Severity values mirror the Notification.severity contract enum.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Type values mirror the Notification.type contract enum
// (contracts/components/schemas/Notification.yaml). A value not in that enum is
// rejected by the SPA's generated zod guard and the notification disappears
// without a trace, so this list and the contract's must be changed together.
const (
	TypeScheduleFailed = "schedule_failed"
	// TypeKnowledgeDrift — ADR-067 FR-038a. The automatic drift check found a
	// knowledge base's search index out of step with its folder, and the index
	// is being rebuilt. Raised only when something is actually wrong.
	TypeKnowledgeDrift = "knowledge_drift"
)

// notificationCap is the per-user retention bound (keep the most recent N).
const notificationCap = 50

// validSeverity reports whether s is one of the known wire-enum severity values.
func validSeverity(s string) bool {
	switch s {
	case SeverityInfo, SeverityWarning, SeverityError:
		return true
	default:
		return false
	}
}

// Notification is the internal record. It carries every wire field plus the
// Recipient (username) that scopes per-user storage and the live WS push.
type Notification struct {
	Recipient   string `json:"recipient"`
	ID          string `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Body        string `json:"body,omitempty"`
	Severity    string `json:"severity"`
	Read        bool   `json:"read"`
	CreatedAtMs int64  `json:"created_at_ms"`
	UpdatedAtMs int64  `json:"updated_at_ms,omitempty"`
	// CoalesceKey is the stable identity of a RECURRING event: a Create whose
	// key matches an existing UNREAD notification updates that one in place
	// instead of adding a row. Empty means "always a new row".
	//
	// This is the ONLY coalescing mechanism. Create used to key on ScheduleID,
	// which quietly made coalescing a privilege of the scheduler: the drift
	// checker sets no ScheduleID, so its every-six-hours report appended
	// forever. Some drift cannot be repaired by re-indexing — one unreadable
	// file, a stale rename journal — so a single bad file produced a new item
	// every six hours indefinitely and evicted everything else from the
	// 50-item cap within a fortnight. Any producer that repeats MUST set this;
	// ScheduleID is now routing data (the SPA's click target) and nothing more.
	CoalesceKey string `json:"coalesce_key,omitempty"`
	ScheduleID  string `json:"schedule_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	AgentID     string `json:"agent_id,omitempty"`
}

// Store is a per-user file-backed notification store.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore returns a store rooted at dir (e.g. $OMNIPUS_HOME/notifications). The
// directory is created lazily on the first write.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// userFile returns the JSON path for a recipient. The recipient is sanitized to
// a filesystem-safe token so a malicious username cannot path-escape.
func (s *Store) userFile(recipient string) string {
	return filepath.Join(s.dir, sanitize(recipient)+".json")
}

// sanitize maps a username to a filesystem-safe token, replacing any character
// outside [A-Za-z0-9._-] with '_'. Empty input maps to "_".
//
// This allowlist pass alone already closes the traversal/separator risk (a
// malicious recipient path-escaping the notifications directory), but it
// does not know about Windows reserved device names: a recipient literally
// named "con" or "com1" would pass through unchanged, and userFile's
// ".json" suffix would then try to address "con.json" — which on Windows
// names the CON device, not a regular file, not an ordinary username. This
// is one of the call sites that cannot reject (a username is already
// authenticated; there is no request to 400 here), so it routes the
// allowlisted result through pkg/pathsafe.SanitizeComponent — the same
// shared, cross-platform-safe rewriter every other filename-accepting
// surface in Omnipus uses — for reserved-name defusal and a conservative
// length cap. Deliberately does NOT case-fold the token: two users whose
// names differ only in case (e.g. "Alice" and "alice") are distinct
// accounts with their own notification histories, not a filename-collision
// concern this package should silently merge.
func sanitize(name string) string {
	if name == "" {
		return "_"
	}
	b := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
			b = append(b, c)
		default:
			b = append(b, '_')
		}
	}
	allowlisted := string(b)
	safe, changed := pathsafe.SanitizeComponent(allowlisted)
	if changed {
		logger.WarnCF("notifications", "sanitized unsafe recipient token for storage",
			map[string]any{"recipient": name, "token": safe})
	}
	return safe
}

// loadLocked reads a recipient's notifications. A missing file is an empty list.
// Caller must hold s.mu.
func (s *Store) loadLocked(recipient string) ([]Notification, error) {
	data, err := os.ReadFile(s.userFile(recipient))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []Notification
	if err := json.Unmarshal(data, &list); err != nil {
		// B4 self-heal: a corrupt history file must NEVER permanently swallow all
		// future alerts. If we returned the error here, Create would abort before
		// reaching save and the bad file would never be repaired — every failure
		// alert for this recipient would be lost. Instead, quarantine the bad file
		// (rename to <recipient>.json.corrupt-<unixnano>) and return an empty list
		// so the next Create rewrites a clean file. The store already uses the wall
		// clock elsewhere (CreatedAtMs), so a UnixNano suffix is consistent.
		path := s.userFile(recipient)
		quarantine := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UnixNano())
		if renameErr := os.Rename(path, quarantine); renameErr != nil {
			logger.ErrorCF("notifications", "corrupt notification store could not be quarantined",
				map[string]any{
					"recipient":    recipient,
					"path":         path,
					"error":        err.Error(),
					"rename_error": renameErr.Error(),
				})
		} else {
			logger.ErrorCF("notifications", "corrupt notification store quarantined; starting a fresh history",
				map[string]any{
					"recipient":  recipient,
					"quarantine": quarantine,
					"error":      err.Error(),
				})
		}
		return nil, nil
	}
	return list, nil
}

// saveLocked writes a recipient's notifications atomically. Caller holds s.mu.
func (s *Store) saveLocked(recipient string, list []Notification) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("notifications: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(s.userFile(recipient), data, 0o600)
}

// Create adds a notification for n.Recipient. If an UNREAD notification for the
// same (ScheduleID, Recipient) already exists, it is updated in place (title /
// body / session / severity refreshed, UpdatedAtMs bumped) instead of appending
// a new row — this coalesces a flapping schedule into a single bell item
// (Ambiguity #6). The persisted record (new or coalesced) is returned so the
// caller can push it live over the WS.
func (s *Store) Create(n Notification) (Notification, error) {
	if n.Recipient == "" {
		return Notification{}, fmt.Errorf("notifications: empty recipient")
	}
	now := time.Now().UnixMilli()
	if n.CreatedAtMs == 0 {
		n.CreatedAtMs = now
	}
	// Gate severity to the known wire enum (M5b). An out-of-set value would be
	// dropped by the SPA's zod guard, silently losing the alert; coerce unknown
	// values to "error" so the notification still surfaces.
	if !validSeverity(n.Severity) {
		n.Severity = SeverityError
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	list, err := s.loadLocked(n.Recipient)
	if err != nil {
		return Notification{}, err
	}

	// Coalesce: find an existing UNREAD notification for the same recurring
	// event. See CoalesceKey's doc comment for why this is not keyed on
	// ScheduleID any more.
	if n.CoalesceKey != "" {
		for i := range list {
			if !list[i].Read && list[i].CoalesceKey == n.CoalesceKey {
				list[i].Title = n.Title
				list[i].Body = n.Body
				list[i].Severity = n.Severity
				list[i].Type = n.Type
				list[i].SessionID = n.SessionID
				list[i].AgentID = n.AgentID
				list[i].ScheduleID = n.ScheduleID
				list[i].UpdatedAtMs = now
				updated := list[i]
				if err := s.saveLocked(n.Recipient, list); err != nil {
					return Notification{}, err
				}
				return updated, nil
			}
		}
	}

	if n.ID == "" {
		n.ID = generateID()
	}
	list = append(list, n)
	list = retain(list)
	if err := s.saveLocked(n.Recipient, list); err != nil {
		return Notification{}, err
	}
	return n, nil
}

// ListForUser returns a recipient's notifications, newest first.
func (s *Store) ListForUser(username string) ([]Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked(username)
	if err != nil {
		return nil, err
	}
	sortNewestFirst(list)
	return list, nil
}

// UnreadCount returns the number of unread notifications for a recipient.
func (s *Store) UnreadCount(username string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked(username)
	if err != nil {
		return 0, err
	}
	n := 0
	for i := range list {
		if !list[i].Read {
			n++
		}
	}
	return n, nil
}

// MarkRead marks a single notification read. Idempotent: marking an already-read
// (or missing) id is a no-op that returns nil.
func (s *Store) MarkRead(username, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked(username)
	if err != nil {
		return err
	}
	changed := false
	for i := range list {
		if list[i].ID == id && !list[i].Read {
			list[i].Read = true
			list[i].UpdatedAtMs = time.Now().UnixMilli()
			changed = true
			break
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked(username, list)
}

// MarkAllRead marks every notification for a recipient read. Idempotent.
func (s *Store) MarkAllRead(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadLocked(username)
	if err != nil {
		return err
	}
	changed := false
	now := time.Now().UnixMilli()
	for i := range list {
		if !list[i].Read {
			list[i].Read = true
			list[i].UpdatedAtMs = now
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.saveLocked(username, list)
}

// retain trims to the most recent notificationCap entries by CreatedAtMs.
func retain(list []Notification) []Notification {
	if len(list) <= notificationCap {
		return list
	}
	sortNewestFirst(list)
	kept := make([]Notification, notificationCap)
	copy(kept, list[:notificationCap])
	return kept
}

// sortNewestFirst sorts in place by CreatedAtMs descending, breaking ties by ID
// so the order is deterministic.
func sortNewestFirst(list []Notification) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].CreatedAtMs != list[j].CreatedAtMs {
			return list[i].CreatedAtMs > list[j].CreatedAtMs
		}
		return list[i].ID > list[j].ID
	})
}

func generateID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("ntf_%d", time.Now().UnixNano())
	}
	return "ntf_" + hex.EncodeToString(b)
}
