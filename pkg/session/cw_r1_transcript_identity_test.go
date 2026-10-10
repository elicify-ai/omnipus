package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: ADR-066 §12 (the 2026-10-01 R1 GREEN blocker correction), session-core
// U2 effects design D5 and ARCHITECT-ANSWER-CUTOVER-SLICE4.md (the address
// replaces the line index). A tool-call correction names its target by the
// ArchiveAddress its append returned: offset 0 is a VALID address, and a missing
// or mismatched target is a visible error, never a silent fallback to another
// same-ID occurrence. The nil/"unknown → newest occurrence" contract the old
// ToolCallProjectionUpdate carried is deleted with the rewrite surface (DEL-12):
// there is no unknown-address path any more.
// GREEN and implementation mutation probes belong to a separate CHECK.

type cwSessionIdentityFixture struct {
	store *UnifiedStore
	id    string
	path  string
	addrs []ArchiveAddress // the address of each appended row, in order
}

func newCWSessionIdentityFixture(t *testing.T) cwSessionIdentityFixture {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, "sessions")
	store, err := NewUnifiedStoreWithHome(base, home)
	require.NoError(t, err)
	meta, err := store.NewSession(SessionTypeChat, "web", "identity")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	f := cwSessionIdentityFixture{store: store, id: meta.ID, path: filepath.Join(base, meta.ID, "transcript.jsonl")}
	entries := []TranscriptEntry{
		{ID: "older-row", Type: EntryTypeToolCall, AgentID: "identity", TurnID: "older", Timestamp: time.Now().UTC(), ToolCalls: []ToolCall{{
			ID: "call_0", Tool: "identity_tool", Status: "success", DurationMS: 11,
			Parameters: map[string]any{"occurrence": "older"}, Result: map[string]any{"text": "older full"},
		}}},
		{ID: "intervening-row", AgentID: "identity", Role: "user", Content: "between occurrences", Timestamp: time.Now().UTC()},
		{ID: "newer-row", Type: EntryTypeToolCall, AgentID: "identity", TurnID: "newer", Timestamp: time.Now().UTC(), ToolCalls: []ToolCall{{
			ID: "call_0", Tool: "identity_tool", Status: "success", DurationMS: 13,
			Parameters: map[string]any{"occurrence": "newer"}, Result: map[string]any{"text": "newer full"},
		}}},
	}
	for i, entry := range entries {
		addr, aerr := store.AppendTranscriptAddressed(meta.ID, entry)
		require.NoError(t, aerr)
		if i > 0 {
			require.Greater(t, addr.ByteOffset, f.addrs[i-1].ByteOffset, "row %d starts after row %d", i, i-1)
		}
		f.addrs = append(f.addrs, addr)
	}
	require.Len(t, f.rows(t), len(entries))
	return f
}

func (f cwSessionIdentityFixture) rows(t *testing.T) []TranscriptEntry {
	t.Helper()
	rows, err := f.store.ReadTranscript(f.id)
	require.NoError(t, err)
	return rows
}

func (f cwSessionIdentityFixture) raw(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(f.path)
	require.NoError(t, err)
	return data
}

func cwSessionIdentityAddressed(t *testing.T, id ToolCallID, addr ArchiveAddress, state, text string) ToolCallProjectionEdit {
	t.Helper()
	require.NotEmpty(t, addr.EntryID, "a projection edit needs a complete target address")
	return ToolCallProjectionEdit{ToolCallID: id, Target: addr, ContentState: state, Text: text}
}

func cwSessionIdentityProjectedRow(before []TranscriptEntry, row int, state, text string) TranscriptEntry {
	want := before[row]
	want.ToolCalls = append([]ToolCall(nil), before[row].ToolCalls...)
	want.ToolCalls[0].ContentState = state
	result := make(map[string]any, len(want.ToolCalls[0].Result)+1)
	for k, v := range want.ToolCalls[0].Result {
		result[k] = v
	}
	result["text"] = text
	want.ToolCalls[0].Result = result
	return want
}

// Scenario 4 (ported). An addressed edit updates exactly the record its address
// names, never the newest same-ID occurrence, and a retract restores it exactly.
func TestCWIdentity_KnownAddressIsNotTheLatestSameIDOccurrence(t *testing.T) {
	f := newCWSessionIdentityFixture(t)
	before := f.rows(t)

	effect, err := f.store.ProjectToolCalls(f.id, []ToolCallProjectionEdit{
		cwSessionIdentityAddressed(t, "call_0", f.addrs[0], "emptied", "zero addressed"),
	})
	require.NoError(t, err)

	rows := f.rows(t)
	require.Len(t, rows, 3)
	require.Equal(t, cwSessionIdentityProjectedRow(before, 0, "emptied", "zero addressed"), rows[0],
		"the addressed row is updated and every other field of it is preserved")
	require.Equal(t, before[2], rows[2],
		"the newer same-ID occurrence must be untouched when an address names the older one")
	require.Equal(t, before[1], rows[1], "an unrelated row is untouched")

	// The retract restores the addressed row exactly, without touching the newer duplicate.
	_, err = f.store.RetractToolCallEffects(f.id, []ArchiveAddress{effect})
	require.NoError(t, err)
	require.Equal(t, before, f.rows(t), "retract restores the addressed row without touching the newer duplicate")
}

// Scenario 7 (ported). An invalid known address must not fall back to another
// same-ID row, skip corruption, or turn a failed read into a successful no-op.
func TestCWIdentity_MissingOrUnreadableAddressReturnsVisibleError(t *testing.T) {
	for _, tc := range []struct {
		name string
	}{
		{"negative_offset"},
		{"addressed_non_tool_row"},
		{"one_past_end"},
		{"two_past_end"},
		{"wrong_id_at_address"},
		{"corrupt_addressed_row"},
		{"empty_transcript"},
		{"missing_transcript"},
		{"unreadable_transcript"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCWSessionIdentityFixture(t)
			before := f.raw(t)

			target := f.addrs[0]
			switch tc.name {
			case "negative_offset":
				target.ByteOffset = -1
			case "addressed_non_tool_row":
				target = f.addrs[1]
			case "one_past_end":
				target.ByteOffset = int64(len(before))
			case "two_past_end":
				target.ByteOffset = int64(len(before)) + 5
			case "wrong_id_at_address":
				lines := bytes.Split(before, []byte{'\n'})
				require.Len(t, lines, 4, "three explicit physical rows plus the terminating newline")
				var row TranscriptEntry
				require.NoError(t, json.Unmarshal(lines[0], &row))
				row.ToolCalls[0].ID = "other_id"
				var err error
				lines[0], err = json.Marshal(row)
				require.NoError(t, err)
				before = bytes.Join(lines, []byte{'\n'})
				require.NoError(t, os.WriteFile(f.path, before, 0o600))
			case "corrupt_addressed_row":
				lines := bytes.Split(before, []byte{'\n'})
				require.Len(t, lines, 4)
				lines[0] = []byte("{not-json")
				before = bytes.Join(lines, []byte{'\n'})
				require.NoError(t, os.WriteFile(f.path, before, 0o600))
			case "empty_transcript":
				before = []byte{}
				require.NoError(t, os.WriteFile(f.path, before, 0o600))
			case "missing_transcript":
				require.NoError(t, os.Remove(f.path))
			case "unreadable_transcript":
				cwSessionIdentityMakeUnreadable(t, f)
			}

			_, err := f.store.ProjectToolCalls(f.id, []ToolCallProjectionEdit{
				cwSessionIdentityAddressed(t, "call_0", target, "emptied", "must not reach another row"),
			})
			require.Error(t, err, "a known but missing/unreadable row must produce a visible error, never a fallback or successful no-op")

			switch tc.name {
			case "missing_transcript":
				_, statErr := os.Stat(f.path)
				require.ErrorIs(t, statErr, os.ErrNotExist, "a missing address must not create a transcript")
			case "unreadable_transcript":
				cwSessionIdentityAssertReadError(t, f, err)
			default:
				require.Equal(t, before, f.raw(t), "invalid addressing must leave all physical bytes, including other same-ID rows, untouched")
			}
		})
	}
}

func cwSessionIdentityMakeUnreadable(t *testing.T, f cwSessionIdentityFixture) {
	t.Helper()
	// A directory at the file path fails a real read even for a privileged
	// process; chmod-only fixtures can silently remain readable by root.
	require.NoError(t, os.Remove(f.path))
	require.NoError(t, os.Mkdir(f.path, 0o700))
	info, err := os.Stat(f.path)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "instrument control: the read target really is a directory")
}

func cwSessionIdentityAssertReadError(t *testing.T, f cwSessionIdentityFixture, err error) {
	t.Helper()
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "filesystem cause must reach the caller")
	assert.Equal(t, "read", pathErr.Op)
	assert.Equal(t, f.path, pathErr.Path)
	assert.ErrorIs(t, err, syscall.EISDIR, "the original read-directory cause must remain visible")
	children, readErr := os.ReadDir(f.path)
	require.NoError(t, readErr)
	require.Empty(t, children, "a failed projection must not write a replacement result inside the invalid target")
}
