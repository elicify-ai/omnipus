package email

// T30 — spec §7 row 30: TestWatcher_NeverMutatesFlags + TestWatcher_KeyedOnUIDNotUnseen.
// Oracles (expected values derive from the spec text, never from the code):
//   - MC-18 / D20/D27: a watcher cycle never mutates flags — no STORE, no
//     other mutating IMAP command — and its state file carries UIDs, counts
//     and error metadata only, never message content.
//   - MAJ-019: "a message that another session marks \Seen before the cycle
//     still advances the UID state (the watcher must key on UID, not UNSEEN)".
//
// The harness (startCaptureIMAP) is package-internal to email; the specapi
// stub of TestWatcher_NeverMutatesFlags stays the NewWatcher-validation half.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

func watcherMsgRaw(from, marker string) []byte {
	return []byte("From: " + from + "\r\nTo: mailbox@test.local\r\n" +
		"Subject: t\r\nDate: Mon, 02 Jan 2006 15:04:05 +0000\r\n" +
		"Message-ID: <" + marker + "@example.test>\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n\r\n" + marker + "\r\n")
}

// watcherStateKeyAllowed pins the state-file key set (MC-18: "state file
// UIDs/counts only"). Characterization of the persisted metadata shape; the
// spec-derived part is the content sweep in the tests below.
func watcherStateKeyAllowed(k string) bool {
	switch k {
	case "agent_id", "workspace_id", "uidvalidity", "last_seen_uid",
		"unseen_total", "watcher_state", "last_error_class", "last_error_text",
		"last_success_at", "next_attempt_at", "attempt":
		return true
	}
	return false
}

// watcherMutatingVerbs are the IMAP commands that change server state. A
// watcher cycle must issue none of them (MC-18 covers STORE; the rest is the
// same invariant against flag/mailbox mutation).
var watcherMutatingVerbs = []string{
	"STORE", "APPEND", "EXPUNGE", "DELETE", "CREATE", "RENAME", "SUBSCRIBE",
	"COPY", "MOVE", "SETACL", "SETMETADATA", "SETQUOTA", "UNSUBSCRIBE",
}

func TestWatcher_NeverMutatesFlags(t *testing.T) {
	const marker = "watcher-body-marker-77f3c1"
	cap := startCaptureIMAP(t, [][]byte{watcherMsgRaw("m1@example.test", marker)}, nil, false)
	// The harness seeds INBOX through its own client on the same DebugWriter
	// before the watcher exists; only the transcript after seeding is the
	// watcher's.
	seedLen := len(cap.log.String())
	dir := t.TempDir()
	const agentID, wsID = "agent-a", "ws-a"
	w, err := NewWatcher(WatcherConfig{AgentID: agentID, WorkspaceID: wsID, Transport: cap.cl, StateDir: dir})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx := context.Background()
	if err := w.Cycle(ctx); err != nil {
		t.Fatalf("watcher cycle 1: %v", err)
	}
	if err := w.Cycle(ctx); err != nil {
		t.Fatalf("watcher cycle 2: %v", err)
	}

	// Instrument check: the cycle actually reached the harness. Without a
	// read command in the transcript the no-STORE assertion below would be
	// vacuously green.
	full := cap.log.String()
	watcherLog := ""
	if len(full) >= seedLen {
		watcherLog = full[seedLen:]
	}
	sawRead := false
	for _, line := range clientCommands(watcherLog) {
		up := strings.ToUpper(line)
		if strings.Contains(up, " STATUS ") || strings.Contains(up, " FETCH ") {
			sawRead = true
		}
	}
	if !sawRead {
		t.Fatalf("instrument: transcript shows no STATUS/FETCH from the watcher cycle — commands seen: %q", clientCommands(watcherLog))
	}

	if stores := storeCommands(watcherLog); len(stores) != 0 {
		t.Fatalf("MC-18: watcher issued %d STORE command(s) across two cycles: %q — the watcher must never mutate flags", len(stores), stores)
	}
	for _, line := range clientCommands(watcherLog) {
		for _, verb := range watcherMutatingVerbs {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			second, third := strings.ToUpper(fields[1]), ""
			if len(fields) >= 3 {
				third = strings.ToUpper(fields[2])
			}
			if second == verb || (second == "UID" && third == verb) {
				t.Fatalf("MC-18: watcher issued a mutating %s command: %q", verb, line)
			}
		}
	}

	// MC-18/D6: the state file carries metadata only — the message body's
	// marker must not appear in it, and the key set must stay metadata.
	b, err := os.ReadFile(filepath.Join(dir, "email-watch", keyFor(agentID, wsID)+".json"))
	if err != nil {
		t.Fatalf("state file after cycles: %v", err)
	}
	if strings.Contains(string(b), marker) {
		t.Fatalf("MC-18/D6: watcher state file contains message content (marker %q): %s", marker, b)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b, &keys); err != nil {
		t.Fatalf("state file JSON: %v", err)
	}
	for k := range keys {
		if !watcherStateKeyAllowed(k) {
			t.Fatalf("MC-18: unexpected state-file key %q — state file carries UIDs/counts/error metadata only", k)
		}
	}
}

// TestWatcherHarness_StoreDetectsCommands proves the instrument: a client
// STORE against the capture harness is visible to storeCommands, so the
// no-STORE assertions above could have seen a violation.
func TestWatcherHarness_StoreDetectsCommands(t *testing.T) {
	cap := startCaptureIMAP(t, [][]byte{watcherMsgRaw("m1@example.test", "instr")}, nil, false)
	before := len(storeCommands(cap.log.String()))
	cl, err := imapclient.DialInsecure(cap.addr, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()
	if err := cl.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := cl.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("select: %v", err)
	}
	setFlags := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}
	if err := cl.Store(imap.SeqSetNum(1), setFlags, nil).Close(); err != nil {
		t.Fatalf("store: %v", err)
	}
	after := storeCommands(cap.log.String())
	if len(after) != before+1 {
		t.Fatalf("instrument: planted STORE not detected (before=%d after=%d) — storeCommands cannot see client STOREs, so TestWatcher_NeverMutatesFlags would be vacuous", before, len(after))
	}
}

func TestWatcher_KeyedOnUIDNotUnseen(t *testing.T) {
	cap := startCaptureIMAP(t, [][]byte{watcherMsgRaw("m1@example.test", "k1")}, nil, false)
	dir := t.TempDir()
	const agentID, wsID = "agent-b", "ws-b"
	w, err := NewWatcher(WatcherConfig{AgentID: agentID, WorkspaceID: wsID, Transport: cap.cl, StateDir: dir})
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	ctx := context.Background()
	if err := w.Cycle(ctx); err != nil {
		t.Fatalf("watcher cycle 1: %v", err)
	}
	st, err := LoadWatcherState(dir, agentID, wsID)
	if err != nil {
		t.Fatalf("load state after cycle 1: %v", err)
	}
	if st.LastSeenUID != 1 {
		t.Fatalf("after cycle 1 (one message in INBOX, uidnext-1 = 1): last_seen_uid = %d, want 1", st.LastSeenUID)
	}
	if st.UnseenTotal != 1 {
		t.Fatalf("after cycle 1: unseen_total = %d, want 1", st.UnseenTotal)
	}

	// Another session marks message 1 \Seen; a new message arrives (uid 2).
	other, err := imapclient.DialInsecure(cap.addr, nil)
	if err != nil {
		t.Fatalf("dial other session: %v", err)
	}
	defer other.Close()
	if err := other.Login(testIMAPUser, testIMAPPass).Wait(); err != nil {
		t.Fatalf("login other session: %v", err)
	}
	if _, err := other.Select("INBOX", nil).Wait(); err != nil {
		t.Fatalf("select other session: %v", err)
	}
	setFlags := &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}}
	if err := other.Store(imap.SeqSetNum(1), setFlags, nil).Close(); err != nil {
		t.Fatalf("other session marks \\Seen: %v", err)
	}
	appendRaw(t, cap.addr, [][]byte{watcherMsgRaw("m2@example.test", "k2")}, nil)

	// MAJ-019: the cycle still advances the UID state even though the unseen
	// count is unchanged (1 before, 1 after) — keying on UNSEEN would see
	// "nothing new".
	if err := w.Cycle(ctx); err != nil {
		t.Fatalf("watcher cycle 2: %v", err)
	}
	st2, err := LoadWatcherState(dir, agentID, wsID)
	if err != nil {
		t.Fatalf("load state after cycle 2: %v", err)
	}
	if st2.LastSeenUID != 2 {
		t.Fatalf("MAJ-019: after another session marked msg 1 \\Seen and a new message arrived, last_seen_uid = %d, want 2 — the watcher must key on UID, not UNSEEN (unseen_total was 1 across both cycles)", st2.LastSeenUID)
	}
	if st2.UnseenTotal != 1 {
		t.Fatalf("unseen_total after cycle 2 = %d, want 1 (only the new message is unseen)", st2.UnseenTotal)
	}
	if st2.State != "ok" {
		t.Fatalf("watcher_state after clean cycle 2 = %q, want ok", st2.State)
	}

	// The watcher's cycles must not have touched any flags: msg 1 keeps
	// exactly \\Seen (set by the other session), msg 2 keeps none.
	f1 := inboxFlags(t, cap.addr, 1)
	if len(f1) != 1 || !flagPresent(f1, imap.FlagSeen) {
		t.Fatalf("msg 1 flags after two watcher cycles = %v, want exactly [\\Seen] — the watcher must not add or remove flags", f1)
	}
	f2 := inboxFlags(t, cap.addr, 2)
	if len(f2) != 0 {
		t.Fatalf("msg 2 flags after a watcher cycle = %v, want none", f2)
	}
}
