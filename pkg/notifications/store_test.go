package notifications

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoad_CorruptFileSelfHeals asserts that a corrupt history file does NOT
// permanently swallow all future alerts (B4): ListForUser returns empty, the bad
// file is quarantined, and the next Create rewrites a clean file.
func TestLoad_CorruptFileSelfHeals(t *testing.T) {
	s := newTestStore(t)

	// Pre-create the dir and write garbage to alice's file.
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := s.userFile("alice")
	if err := os.WriteFile(path, []byte("{not valid json at all"), 0o600); err != nil {
		t.Fatal(err)
	}

	// ListForUser must return empty (not error) and quarantine the bad file.
	list, err := s.ListForUser("alice")
	if err != nil {
		t.Fatalf("ListForUser on corrupt file must not error, got %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list from corrupt file, got %d", len(list))
	}

	// A quarantine file <path>.corrupt-<nano> must now exist.
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	foundQuarantine := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "alice.json.corrupt-") {
			foundQuarantine = true
		}
	}
	if !foundQuarantine {
		t.Fatal("expected a quarantine file alice.json.corrupt-<nano>")
	}

	// The original corrupt file must no longer block: the next Create succeeds
	// and writes a clean, readable history.
	_, err = s.Create(Notification{
		Recipient: "alice", Type: TypeScheduleFailed, Title: "fresh",
		Severity: SeverityError, ScheduleID: "s1",
	})
	if err != nil {
		t.Fatalf("Create after corrupt self-heal must succeed, got %v", err)
	}
	got, err := s.ListForUser("alice")
	if err != nil {
		t.Fatalf("ListForUser after self-heal must succeed, got %v", err)
	}
	if len(got) != 1 || got[0].Title != "fresh" {
		t.Fatalf("expected the fresh notification after self-heal, got %+v", got)
	}

	// The rewritten file must parse as a clean JSON array.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(data)), "[") {
		t.Fatalf("rewritten store should be a JSON array, got %q", string(data))
	}
	_ = filepath.Base(path)
}

// TestCreate_GatesInvalidSeverity asserts an unknown severity is coerced to the
// known "error" value so the SPA zod guard cannot silently drop it (M5b).
func TestCreate_GatesInvalidSeverity(t *testing.T) {
	s := newTestStore(t)
	n, err := s.Create(Notification{
		Recipient: "alice", Type: TypeScheduleFailed, Title: "x",
		Severity: "totally-bogus", ScheduleID: "s1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if n.Severity != SeverityError {
		t.Fatalf("invalid severity must be coerced to %q, got %q", SeverityError, n.Severity)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return NewStore(t.TempDir())
}

// TestCreate_CoalescesByScheduleID asserts a second UNREAD notification for the
// same schedule_id+recipient updates the existing item in place rather than
// appending a new row (Ambiguity #6).
func TestCreate_CoalescesByScheduleID(t *testing.T) {
	s := newTestStore(t)

	first, err := s.Create(Notification{
		Recipient: "alice", Type: TypeScheduleFailed, Title: "Schedule x failed",
		Body: "boom1", Severity: SeverityError, ScheduleID: "sched-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	second, err := s.Create(Notification{
		Recipient: "alice", Type: TypeScheduleFailed, Title: "Schedule x failed again",
		Body: "boom2", Severity: SeverityError, ScheduleID: "sched-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	if second.ID != first.ID {
		t.Fatalf("coalesce should reuse id: first=%s second=%s", first.ID, second.ID)
	}
	if second.Body != "boom2" {
		t.Fatalf("coalesced body not updated: %q", second.Body)
	}
	if second.UpdatedAtMs == 0 {
		t.Fatal("coalesced notification should bump updated_at_ms")
	}

	list, err := s.ListForUser("alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 coalesced notification, got %d", len(list))
	}
}

// TestCreate_NoCoalesceWhenRead asserts a read notification is not coalesced —
// a new failure after the user read the prior one creates a fresh item.
func TestCreate_NoCoalesceWhenRead(t *testing.T) {
	s := newTestStore(t)
	n, _ := s.Create(Notification{Recipient: "bob", ScheduleID: "s2", Title: "a"})
	if err := s.MarkRead("bob", n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(Notification{Recipient: "bob", ScheduleID: "s2", Title: "b"}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListForUser("bob")
	if len(list) != 2 {
		t.Fatalf("expected 2 notifications (read not coalesced), got %d", len(list))
	}
}

// TestListForUser_NewestFirst asserts ordering.
func TestListForUser_NewestFirst(t *testing.T) {
	s := newTestStore(t)
	// Distinct schedule ids so they are not coalesced; explicit created_at.
	_, _ = s.Create(Notification{Recipient: "c", ScheduleID: "a", Title: "old", CreatedAtMs: 100})
	_, _ = s.Create(Notification{Recipient: "c", ScheduleID: "b", Title: "new", CreatedAtMs: 200})
	list, _ := s.ListForUser("c")
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}
	if list[0].Title != "new" || list[1].Title != "old" {
		t.Fatalf("not newest-first: %q, %q", list[0].Title, list[1].Title)
	}
}

// TestUnreadCount tracks unread.
func TestUnreadCount(t *testing.T) {
	s := newTestStore(t)
	n1, _ := s.Create(Notification{Recipient: "d", ScheduleID: "a", Title: "1"})
	_, _ = s.Create(Notification{Recipient: "d", ScheduleID: "b", Title: "2"})
	if c, _ := s.UnreadCount("d"); c != 2 {
		t.Fatalf("unread = %d, want 2", c)
	}
	_ = s.MarkRead("d", n1.ID)
	if c, _ := s.UnreadCount("d"); c != 1 {
		t.Fatalf("unread after read = %d, want 1", c)
	}
}

// TestMarkRead_Idempotent asserts marking the same id twice (or a missing id) is
// a no-op that returns nil.
func TestMarkRead_Idempotent(t *testing.T) {
	s := newTestStore(t)
	n, _ := s.Create(Notification{Recipient: "e", ScheduleID: "a", Title: "1"})
	if err := s.MarkRead("e", n.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkRead("e", n.ID); err != nil {
		t.Fatalf("second MarkRead should be nil: %v", err)
	}
	if err := s.MarkRead("e", "missing"); err != nil {
		t.Fatalf("MarkRead of missing id should be nil: %v", err)
	}
	list, _ := s.ListForUser("e")
	if !list[0].Read {
		t.Fatal("notification should be read")
	}
}

// TestMarkAllRead marks everything read.
func TestMarkAllRead(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Create(Notification{Recipient: "f", ScheduleID: "a", Title: "1"})
	_, _ = s.Create(Notification{Recipient: "f", ScheduleID: "b", Title: "2"})
	if err := s.MarkAllRead("f"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.UnreadCount("f"); c != 0 {
		t.Fatalf("unread after mark-all = %d, want 0", c)
	}
	// Idempotent.
	if err := s.MarkAllRead("f"); err != nil {
		t.Fatalf("second MarkAllRead should be nil: %v", err)
	}
}

// TestRetentionCap keeps only the most recent notificationCap entries.
func TestRetentionCap(t *testing.T) {
	s := newTestStore(t)
	total := notificationCap + 20
	for i := 0; i < total; i++ {
		// distinct schedule ids so none coalesce, ascending created_at.
		_, err := s.Create(Notification{
			Recipient:   "g",
			ScheduleID:  string(rune('A'+i%26)) + itoa(i),
			Title:       "n",
			CreatedAtMs: int64(i + 1),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	list, _ := s.ListForUser("g")
	if len(list) != notificationCap {
		t.Fatalf("retention: want %d, got %d", notificationCap, len(list))
	}
	// The newest (highest created_at) must survive.
	if list[0].CreatedAtMs != int64(total) {
		t.Fatalf("newest not retained: got created_at %d, want %d", list[0].CreatedAtMs, total)
	}
}

// TestCreate_EmptyRecipient is an error.
func TestCreate_EmptyRecipient(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Create(Notification{Title: "x"}); err == nil {
		t.Fatal("expected error for empty recipient")
	}
}

// itoa is a tiny dependency-free int->string for the retention test ids.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
