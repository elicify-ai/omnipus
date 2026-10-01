package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// This RED helper observes real stores, not an invented future notice type.
// Oracle: corrected sub-agent control-plane ADR D6/D7. It deliberately does
// not choose an ID spelling for (parent, child, generation, stop_seq).
func assertU1StoppedChildNotice(t *testing.T, al *AgentLoop, parentID string, original *session.LifecycleRecord, wantCause, wantActor string) (string, string) {
	t.Helper()
	rec, err := al.GetSessionLifecycleStore().Load(original.SessionID)
	if err != nil {
		t.Fatalf("Load(stopped child): %v", err)
	}
	if rec.State != session.LifecycleStopped || rec.Terminal() {
		t.Errorf("child lifecycle = %q, terminal=%v; D6 requires stopped and non-terminal", rec.State, rec.Terminal())
	}
	if rec.Generation != original.Generation {
		t.Errorf("child generation = %d, want unchanged generation %d (D6)", rec.Generation, original.Generation)
	}

	entries, err := al.GetMessageInboxStore().Entries(parentID)
	if err != nil {
		t.Fatalf("Entries(direct parent): %v", err)
	}
	finalID := fmt.Sprintf("%s:%d:final", original.SessionID, original.Generation)
	var notices []map[string]any
	for _, entry := range entries {
		if entry.Kind != session.InboxEntryMessage {
			continue
		}
		if entry.Message == nil {
			t.Fatal("parent message entry has no message")
		}
		raw, merr := entry.Message.MarshalJSON()
		if merr != nil {
			t.Fatalf("MarshalJSON(parent message): %v", merr)
		}
		var fields map[string]any
		if uerr := json.Unmarshal(raw, &fields); uerr != nil {
			t.Fatalf("decode parent message: %v", uerr)
		}
		if fields["session_id"] != original.SessionID {
			continue
		}
		if fields["message_id"] == finalID || fields["fatal"] == true || (fields["kind"] == "handback" && fields["mode"] == "final") {
			t.Errorf("stopped child produced legacy terminal/fatal completion instead of D6 notice: %s", raw)
			continue
		}
		body := u1NoticeBody(fields)
		// D6 offers both actions. Other independent goal-status messages are
		// allowed; they do not count as stopped-child notices.
		if strings.Contains(body, "resume") && strings.Contains(body, "redirect") {
			notices = append(notices, fields)
		}
	}
	if len(notices) == 0 {
		t.Fatal("BLOCKED: independent stopped-child notice delivery not implemented — required by sub-agent control-plane ADR D6")
	}
	if len(notices) != 1 {
		t.Fatalf("direct-parent stopped-child notices = %d, want exactly 1 (D6 retry/replay deduplication)", len(notices))
	}
	notice := notices[0]
	id, ok := notice["message_id"].(string)
	if !ok || id == "" {
		t.Fatal("D6 stopped-child notice has no durable message_id")
	}
	if notice["parent_session_id"] != parentID || notice["direction"] != "child_to_parent" {
		t.Errorf("notice destination/direction = %v/%v, want %s/child_to_parent", notice["parent_session_id"], notice["direction"], parentID)
	}

	return id, assertU1StopNoticeMetadata(t, rec, notice, wantCause, wantActor)
}

func assertU1StopNoticeMetadata(t *testing.T, rec *session.LifecycleRecord, notice map[string]any, wantCause, wantActor string) string {
	t.Helper()
	// Compare persisted stop metadata with the notice as a consistency
	// property. Only cascade's cause/actor have exact values in this brief;
	// clear/idle-expiry do not invent new cause or actor vocabulary.
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal(child lifecycle): %v", err)
	}
	var lifecycleFields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &lifecycleFields); err != nil {
		t.Fatalf("decode child lifecycle: %v", err)
	}
	noteRaw := lifecycleFields["stop_note"]
	if len(noteRaw) == 0 || string(noteRaw) == "null" {
		t.Fatal("BLOCKED: persisted stop_note not implemented — required by sub-agent control-plane ADR D6")
	}
	var note map[string]json.RawMessage
	if err := json.Unmarshal(noteRaw, &note); err != nil {
		t.Fatalf("decode stop_note: %v", err)
	}
	var cause, actor string
	var at time.Time
	var seq uint64
	for key, out := range map[string]any{"cause": &cause, "by": &actor, "at": &at, "seq": &seq} {
		if err := json.Unmarshal(note[key], out); err != nil {
			t.Fatalf("decode stop_note.%s: %v", key, err)
		}
	}
	if actor == "" || at.IsZero() {
		t.Errorf("stop_note actor/time = %q/%v, want a recorded actor and time (D6; seq=%d)", actor, at, seq)
	}
	switch cause {
	case "stop", "redirect_pause", "cascade", "restart", "timeout": // D2/D6 closed cause vocabulary.
	default:
		t.Errorf("stop_note cause = %q, outside D2/D6's stop/redirect_pause/cascade/restart/timeout vocabulary", cause)
	}
	if wantCause != "" && cause != wantCause {
		t.Errorf("stop cause = %q, want %q (D7)", cause, wantCause)
	}
	if wantActor != "" && !strings.Contains(actor, wantActor) {
		t.Errorf("stop actor = %q, want human principal %q (D6/D7)", actor, wantActor)
	}
	body := u1NoticeBody(notice)
	for label, value := range map[string]string{"cause": cause, "actor": actor} {
		value = strings.ToLower(strings.ReplaceAll(value, "_", " "))
		if value == "" || !strings.Contains(body, value) {
			t.Errorf("D6 notice does not convey stop %s %q: %s", label, value, body)
		}
	}
	// Validate the existing envelope timestamp without inventing notice prose
	// or a UTC-only display format. Full presentation of the stop event time
	// remains a CHECK/UAT gap; created_at alone does not prove that claim.
	created, ok := notice["created_at"].(string)
	createdAt, terr := time.Parse(time.RFC3339Nano, created)
	if !ok || terr != nil || createdAt.IsZero() {
		t.Errorf("D6 notice created_at = %v, want a valid nonzero contract timestamp (parse error: %v)", notice["created_at"], terr)
	}
	u1RequireNoticeAction(t, body, "do the work", "do the work", "do work", "do it yourself", "work yourself")
	u1RequireNoticeAction(t, body, "report it open", "report it open", "report open", "report the goal open")
	if !strings.Contains(body, "clear") || !strings.Contains(body, "goal") {
		t.Errorf("D6 notice does not offer clearing the helper's goal: %s", body)
	}
	if wantActor != "" && (!strings.Contains(body, "owner") || (!strings.Contains(body, "ask") && !strings.Contains(body, "check with"))) {
		t.Errorf("human-stop notice lacks advice to consider asking the owner first (D6): %s", body)
	}
	return string(noteRaw)
}

func u1NoticeBody(fields map[string]any) string {
	// Read all payload strings, so either text or structured action labels
	// can satisfy the observable notice contract. Routing fields are not
	// evidence that the notice tells its reader the stop cause/actor.
	parts := make([]string, 0, len(fields))
	for key, value := range fields {
		switch key {
		case "message_id", "session_id", "parent_session_id", "direction", "depth", "generation", "sender_identity", "created_at", "untrusted_origin", "kind":
			continue
		}
		parts = append(parts, u1NoticeStrings(value)...)
	}
	return strings.ToLower(strings.ReplaceAll(strings.Join(parts, " "), "_", " "))
}

func u1NoticeStrings(value any) []string {
	switch value := value.(type) {
	case string:
		return []string{value}
	case []any:
		var parts []string
		for _, item := range value {
			parts = append(parts, u1NoticeStrings(item)...)
		}
		return parts
	case map[string]any:
		var parts []string
		for _, item := range value {
			parts = append(parts, u1NoticeStrings(item)...)
		}
		return parts
	default:
		return nil
	}
}

func u1RequireNoticeAction(t *testing.T, body, action string, labels ...string) {
	t.Helper()
	for _, label := range labels {
		if strings.Contains(body, label) {
			return
		}
	}
	t.Errorf("D6 notice does not offer %s: %s", action, body)
}

func observeU1ParentNoticeWakes(t *testing.T, al *AgentLoop, parentID string) func(string) int {
	t.Helper()
	var mu sync.Mutex
	wakes := make(map[string]int)
	al.asyncNotifier.registerObserver(func(event AsyncNotifyEvent) {
		if event.TranscriptSessionID != parentID {
			return
		}
		id, ok := event.Metadata["steer_message_id"].(string)
		if !ok || id == "" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		wakes[id]++
	})
	return func(id string) int {
		mu.Lock()
		defer mu.Unlock()
		return wakes[id]
	}
}

func replayU1StoppedNotices(t *testing.T, al *AgentLoop, lifecycleDir, inboxDir string) {
	t.Helper()
	// New store instances prove on-disk delivery, not an in-memory cache.
	lifecycle := session.NewLifecycleStore(lifecycleDir)
	inbox := session.NewMessageInboxStore(inboxDir)
	al.SetSessionMessagingStores(inbox, lifecycle)
	wireSteerCompletionDeps(t, al)
	recovery := &SteerBootRecovery{
		Lifecycle: lifecycle, Sessions: al.GetSessionStore(), Inbox: inbox,
		Classifier:     NewSteerRecordClassifier(lifecycle, al.GetSessionStore()),
		Deliverer:      al.getUpwardDeliverer(),
		EndSessionGoal: al.EndSessionOwnedGoalOnTerminal,
		OperatorNotice: func(message string) {
			t.Errorf("boot recovery reported a delivery/recovery problem: %s", message)
		},
	}
	if err := recovery.Run(context.Background()); err != nil {
		t.Fatalf("SteerBootRecovery.Run: %v", err)
	}
}

func assertU1NoticeStable(t *testing.T, al *AgentLoop, parentID string, original *session.LifecycleRecord, cause, actor, noticeID, stopNote string, wakeCount func(string) int) {
	t.Helper()
	id, note := assertU1StoppedChildNotice(t, al, parentID, original, cause, actor)
	if id != noticeID || note != stopNote {
		t.Errorf("retry/replay changed notice identity or stop event: id %q -> %q; note %s -> %s (D6)", noticeID, id, stopNote, note)
	}
	if got := wakeCount(noticeID); got != 1 {
		t.Errorf("working-parent wakes for stopped-child notice = %d, want exactly 1 across retry/replay (D6)", got)
	}
}
