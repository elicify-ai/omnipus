package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Oracle: ADR-066 §12, [2026-10-01 correction — R1 GREEN blocker,
// FR-019/FR-022], and the independent dispatcher clarification: the internal
// TranscriptLine field is *int; nil is unknown, while &0 is known row zero.
// No ToolCall wire field is introduced. Error wording/type for invalid row
// addresses is unspecified: assert a visible error AND unchanged bytes.
// Real filesystem failures additionally preserve their original path/cause.
// GREEN and implementation mutation probes belong to a separate CHECK.

type cwSessionIdentityFixture struct {
	store *UnifiedStore
	id    string
	path  string
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
		{ID: "older-row", Type: EntryTypeToolCall, AgentID: "identity", TurnID: "older", ToolCalls: []ToolCall{{
			ID: "call_0", Tool: "identity_tool", Status: "success", DurationMS: 11,
			Parameters: map[string]any{"occurrence": "older"}, Result: map[string]any{"text": "older full"},
		}}},
		{ID: "intervening-row", AgentID: "identity", Role: "user", Content: "between occurrences"},
		{ID: "newer-row", Type: EntryTypeToolCall, AgentID: "identity", TurnID: "newer", ToolCalls: []ToolCall{{
			ID: "call_0", Tool: "identity_tool", Status: "success", DurationMS: 13,
			Parameters: map[string]any{"occurrence": "newer"}, Result: map[string]any{"text": "newer full"},
		}}},
	}
	for i, entry := range entries {
		switch appendEntry := any(store.AppendTranscriptStrict).(type) {
		case func(string, TranscriptEntry) error:
			require.NoError(t, appendEntry(meta.ID, entry))
		case func(string, TranscriptEntry) (int, error):
			line, err := appendEntry(meta.ID, entry)
			require.NoError(t, err)
			require.Equal(t, i, line, "strict append returns its actual physical row")
		default:
			t.Fatal("BLOCKED: transcript append has an unsupported index-return signature — required by ADR-066 §12 correction 2026-10-01")
		}
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

func cwSessionIdentityUpdate(t *testing.T, id ToolCallID, line int, state string, result map[string]any) ToolCallProjectionUpdate {
	t.Helper()
	update := ToolCallProjectionUpdate{ToolCallID: id, ContentState: state, Result: result}
	field := reflect.ValueOf(&update).Elem().FieldByName("TranscriptLine")
	if !field.IsValid() {
		t.Fatal("BLOCKED: ToolCallProjectionUpdate.TranscriptLine not implemented — required by ADR-066 §12 correction 2026-10-01")
	}
	require.Equal(t, reflect.TypeOf((*int)(nil)), field.Type(), "known zero must be distinct from nil/unknown")
	field.Set(reflect.ValueOf(&line))
	return update
}

func cwSessionIdentityUnknownAddress(t *testing.T, update ToolCallProjectionUpdate) {
	t.Helper()
	// A zero-value projection update is the legitimate legacy no-address
	// caller. On the pre-change baseline there is no field; no fake address
	// is supplied. After implementation, the same update must carry nil.
	field := reflect.ValueOf(update).FieldByName("TranscriptLine")
	if field.IsValid() {
		require.Equal(t, reflect.TypeOf((*int)(nil)), field.Type())
		require.True(t, field.IsNil(), "the zero-value update must mean unknown, not known row zero")
	}
}

func cwSessionIdentityProjectedRows(before []TranscriptEntry, line int, state string, result map[string]any) []TranscriptEntry {
	want := append([]TranscriptEntry(nil), before...)
	want[line].ToolCalls = append([]ToolCall(nil), before[line].ToolCalls...)
	want[line].ToolCalls[0].ContentState = state
	want[line].ToolCalls[0].Result = result
	return want
}

// Scenario 4. Known index zero is not unknown. Only genuine no-address
// callers retain the ADR's explicit newest-occurrence fallback exception.
func TestCWIdentity_KnownZeroIsNotUnknownTranscriptAddress(t *testing.T) {
	t.Run("known_zero", func(t *testing.T) {
		f := newCWSessionIdentityFixture(t)
		before := f.rows(t)
		update := cwSessionIdentityUpdate(t, "call_0", 0, "emptied", map[string]any{"text": "zero addressed"})
		previous, err := f.store.UpdateToolCallProjections(f.id, []ToolCallProjectionUpdate{update})
		require.NoError(t, err)
		require.Equal(t, cwSessionIdentityProjectedRows(before, 0, "emptied", update.Result), f.rows(t),
			"&zero updates row zero, never the newest same-ID row")
		require.Equal(t, []ToolCallProjectionUpdate{cwSessionIdentityUpdate(t, "call_0", 0,
			before[0].ToolCalls[0].ContentState, before[0].ToolCalls[0].Result)}, previous,
			"undo includes the addressed row zero and its exact original state")
		_, err = f.store.UpdateToolCallProjections(f.id, previous)
		require.NoError(t, err)
		require.Equal(t, before, f.rows(t), "undo must restore row zero without touching the newer duplicate")
	})
	t.Run("nil_unknown_latest_control", func(t *testing.T) {
		f := newCWSessionIdentityFixture(t)
		before := f.rows(t)
		update := ToolCallProjectionUpdate{ToolCallID: "call_0", ContentState: "emptied", Result: map[string]any{"text": "legacy latest"}}
		cwSessionIdentityUnknownAddress(t, update)
		previous, err := f.store.UpdateToolCallProjections(f.id, []ToolCallProjectionUpdate{update})
		require.NoError(t, err)
		require.Equal(t, cwSessionIdentityProjectedRows(before, 2, "emptied", update.Result), f.rows(t),
			"nil/unknown must retain last-occurrence behavior")
		require.Len(t, previous, 1)
		// Independent architect clarification, 2026-10-01: unknown input
		// still returns a concrete undo address, so undo cannot re-resolve
		// to a different occurrence after a later same-ID append.
		field := reflect.ValueOf(previous[0]).FieldByName("TranscriptLine")
		if !field.IsValid() {
			t.Fatal("BLOCKED: projection undo does not carry its resolved TranscriptLine — required by ADR-066 §12 clarification 2026-10-01")
		}
		address, ok := field.Interface().(*int)
		require.True(t, ok, "undo uses the independent *int address contract")
		require.NotNil(t, address, "nil input must not produce an unknown undo address")
		require.Equal(t, 2, *address, "fallback resolved the newest fixture row, whose zero-based index is 2")
		require.Equal(t, "call_0", string(previous[0].ToolCallID))
		require.Equal(t, before[2].ToolCalls[0].ContentState, previous[0].ContentState)
		require.Equal(t, before[2].ToolCalls[0].Result, previous[0].Result)
		_, err = f.store.UpdateToolCallProjections(f.id, previous)
		require.NoError(t, err)
		require.Equal(t, before, f.rows(t))
	})
	t.Run("legacy_status_latest_control", func(t *testing.T) {
		f := newCWSessionIdentityFixture(t)
		before := f.rows(t)
		result := map[string]any{"text": "newest status result"}
		found, err := f.store.UpdateToolCallStatusAndResult(f.id, "call_0", "error", 19, result)
		require.NoError(t, err)
		require.True(t, found)
		want := cwSessionIdentityProjectedRows(before, 2, before[2].ToolCalls[0].ContentState, result)
		want[2].ToolCalls[0].Status = "error"
		want[2].ToolCalls[0].DurationMS = 19
		require.Equal(t, want, f.rows(t), "status callers without archive identity must still update only the newest occurrence")
		found, err = f.store.UpdateToolCallStatusAndResult(f.id, "call_0", "success", 23, nil)
		require.NoError(t, err)
		require.True(t, found)
		want[2].ToolCalls[0].Status = "success"
		want[2].ToolCalls[0].DurationMS = 23
		require.Equal(t, want, f.rows(t), "the status API's nil result still leaves its existing result intact")
	})
}

// Scenario 7. An invalid known address must not fall back to another same-ID
// row, skip corruption, or turn a failed read into a successful no-op.
func TestCWIdentity_MissingOrUnreadableAddressReturnsVisibleError(t *testing.T) {
	for _, tc := range []struct {
		name string
		line int
	}{
		// Three rows: valid tool rows 0 and 2; row 1 has no tool call.
		{"negative_known_line", -1},
		{"addressed_non_tool_row", 1},
		{"one_past_end", 3},
		{"two_past_end", 4},
		{"wrong_id_at_address", 0},
		{"corrupt_addressed_row", 0},
		{"empty_transcript", 0},
		{"missing_transcript", 0},
		{"unreadable_transcript", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCWSessionIdentityFixture(t)
			update := cwSessionIdentityUpdate(t, "call_0", tc.line, "emptied", map[string]any{"text": "must not reach another row"})
			before := f.raw(t)
			switch tc.name {
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

			previous, err := f.store.UpdateToolCallProjections(f.id, []ToolCallProjectionUpdate{update})
			require.Error(t, err, "a known but missing/unreadable row must produce a visible error, never a fallback or successful no-op")
			require.Empty(t, previous, "a failed address must not claim another occurrence as its undo record")
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
	t.Run("legacy_storage_error_control", func(t *testing.T) {
		f := newCWSessionIdentityFixture(t)
		cwSessionIdentityMakeUnreadable(t, f)
		previous, err := f.store.UpdateToolCallProjections(f.id, []ToolCallProjectionUpdate{{
			ToolCallID: "call_0", ContentState: "emptied", Result: map[string]any{"text": "not written"},
		}})
		require.Error(t, err, "real storage failures must remain visible even on the legitimate legacy path")
		require.Empty(t, previous)
		cwSessionIdentityAssertReadError(t, f, err)
	})
}

func cwSessionIdentityMakeUnreadable(t *testing.T, f cwSessionIdentityFixture) {
	t.Helper()
	// A directory at the file path fails a real ReadFile even for a privileged
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
