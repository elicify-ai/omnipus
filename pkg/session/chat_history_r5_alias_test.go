package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const historyR5Day = "2026-10-31"

// Ordered/duplicate JSON members cannot be produced by a Go map. Seed an EMPTY,
// closed temporary canonical session, then let the real offline builder derive
// every index fact. No accepted live record or product code is rewritten.
func historyR5CanonicalFixture(t *testing.T, records []string) (*UnifiedStore, string) {
	t.Helper()
	s, sid := newEffectsSession(t)
	require.NoError(t, s.Close())
	dir := filepath.Join(s.BaseDir(), sid)
	path := filepath.Join(dir, "transcript.jsonl")
	if old, err := os.ReadFile(path); !os.IsNotExist(err) {
		require.NoError(t, err)
		require.Empty(t, old, "fixture must not overwrite accepted canonical records")
	}
	for i, raw := range records {
		var rec ArchiveRecord
		require.NoError(t, json.Unmarshal([]byte(raw), &rec), "fixture record %d is complete JSON", i)
		require.NoError(t, rec.Validate(), "fixture record %d is an accepted envelope shape", i)
	}
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(records, "\n")+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "transcript.day"), []byte(historyR5Day+"\n"), 0o600))
	require.NoError(t, os.RemoveAll(filepath.Join(dir, ChatHistoryIndexDirectory)))
	build, err := PrepareChatHistoryIndex(context.Background(), dir, ChatHistoryIndexBuildOptions{})
	require.NoError(t, err, "real builder accepts the raw canonical fixture, including duplicate keys")
	require.NoError(t, build.Publish(context.Background()))
	require.NoError(t, build.Close())
	require.NoError(t, VerifyChatHistoryIndex(context.Background(), dir))
	reader, err := NewUnifiedStore(s.BaseDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	return reader, sid
}

func historyR5JSONString(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return string(b)
}

func historyR5Result(t *testing.T, text string, duplicate bool) string {
	t.Helper()
	member := `"text":` + historyR5JSONString(t, text)
	if duplicate {
		member = `"text":"superseded-public-value",` + member
	}
	return "{" + member + "}"
}

func historyR5Owner(t *testing.T, value string, privateFirst, duplicate bool, initial string) string {
	t.Helper()
	privateContent := `"content":` + historyR5JSONString(t, value)
	if duplicate {
		privateContent = `"content":"superseded-private-value",` + privateContent
	}
	private := `"model_message":{"role":"assistant",` + privateContent + `}`
	public := `"tool_calls":[{"id":"r5-call","tool":"read_file","status":"pending","parameters":{},"result":` + historyR5Result(t, initial, duplicate) + `}]`
	members := public + "," + private
	if privateFirst {
		members = private + "," + public
	}
	return `{"id":"r5-owner","type":"tool_call","role":"assistant","agent_id":"mia","view_membership":"both","timestamp":"2026-10-31T07:00:00Z","turn_id":"r5-turn",` + members + `}`
}

func historyR5Correction(t *testing.T, kind, value string, duplicate bool) string {
	t.Helper()
	target := historyR5JSONString(t, ArchiveAddress{PartitionKey: historyR5Day, ByteOffset: 0, EntryID: "r5-owner"})
	base := `{"id":"r5-correction","type":"tool_call_effect","view_membership":"chat","tool_call_effect":`
	switch kind {
	case "settle":
		return base + `{"settle":{"target":` + target + `,"if_status":"pending","tool_call":{"id":"r5-call","tool":"read_file","status":"success","parameters":{},"result":` + historyR5Result(t, value, duplicate) + `}}}}`
	case "projection":
		text := `"text":` + historyR5JSONString(t, value)
		if duplicate {
			text = `"text":"superseded-projection",` + text
		}
		return base + `{"projections":[{"target":` + target + `,"tool_call_id":"r5-call","content_state":"capped",` + text + `}]}}`
	default:
		t.Fatalf("unknown correction kind %q", kind)
		return ""
	}
}

// SEC-R5-1 / A2.8: equal private/public strings must not alias into a shorter
// private stand-in. The oracle is full decode/reduce THEN A2.1 shortening, also
// checked against independent encoding/json byte arithmetic (not marker shape).
func TestChatHistoryPage_R5PrivatePublicAliasesEqualFullProjection(t *testing.T) {
	// ASCII isolates the retention/aliasing defect from UTF-8 boundary cases.
	const size = 70000 // SEC-R5-1 counterexample; exceeds the 51,200-byte threshold.
	value := strings.Repeat("x", size)
	for _, kind := range []string{"same_record", "settle", "projection"} {
		for _, privateFirst := range []bool{true, false} {
			for _, duplicate := range []bool{false, true} {
				name := fmt.Sprintf("%s/private_first_%t/duplicate_%t", kind, privateFirst, duplicate)
				t.Run(name, func(t *testing.T) {
					initial := value
					if kind != "same_record" {
						initial = "pending-public-value" // Large public value exists ONLY in the dependency.
					}
					records := []string{historyR5Owner(t, value, privateFirst, duplicate, initial)}
					if kind != "same_record" {
						records = append(records, historyR5Correction(t, kind, value, duplicate))
					}
					historyR5AssertProjection(t, records, value)
				})
			}
		}
	}
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("public_owner_then_private_cancel_dependency/duplicate_%t", duplicate), func(t *testing.T) {
			// Reverse cross-record encounter: public owner first, identical private
			// value only in its real cancellation dependency. Neither an effect with
			// an invalid model payload nor an artificial reversed reducer is used.
			owner := strings.Replace(historyR5Owner(t, value, false, duplicate, value), `,"model_message":{"role":"assistant","content":`+historyR5JSONString(t, value)+`}`, "", 1)
			if duplicate {
				owner = strings.Replace(historyR5Owner(t, value, false, true, value), `,"model_message":{"role":"assistant","content":"superseded-private-value","content":`+historyR5JSONString(t, value)+`}`, "", 1)
			}
			cancel := `{"id":"r5-cancel","type":"turn_canceled","role":"system","agent_id":"mia","view_membership":"both","timestamp":"2026-10-31T07:00:01Z","turn_id":"r5-turn","cancel_method":"graceful","model_message":{"role":"assistant","content":` + historyR5JSONString(t, value) + `}}`
			historyR5AssertProjection(t, []string{owner, cancel}, value)
		})
	}
}

func historyR5AssertProjection(t *testing.T, records []string, value string) {
	t.Helper()
	s, sid := historyR5CanonicalFixture(t, records)
	canonical := filepath.Join(s.BaseDir(), sid, "transcript.jsonl")
	before, err := os.ReadFile(canonical)
	require.NoError(t, err)
	position := historyExpectedPosition(sid, ArchiveAddress{PartitionKey: historyR5Day, EntryID: "r5-owner"})
	full, err := s.ReadChatHistoryEntry(context.Background(), sid, position)
	require.NoError(t, err)
	require.Len(t, full.Entry.ToolCalls, 1)
	require.Equal(t, map[string]any{"text": value}, full.Entry.ToolCalls[0].Result, "full reduction uses the final duplicate key/correction, never private text")
	full.Entry, err = historyShortenForDisplay(full.Entry)
	require.NoError(t, err)
	want, ok := full.Entry.ToolCalls[0].HistoryDisplayPreview()
	require.True(t, ok, "positive control: the full reduced value exceeds the A2.1 threshold")
	encoded := []byte(historyR5JSONString(t, map[string]any{"text": value}))
	cut := min(4096, len(encoded)) // A2.1: 4,096 encoded bytes, not characters.
	for !utf8.Valid(encoded[:cut]) {
		cut--
	}
	require.Equal(t, string(encoded[:cut]), want.Preview, "independent full-decode preview oracle")
	require.Equal(t, len(encoded), want.OriginalSizeBytes, "independent full-decode exact-size oracle")
	page := historyRead(t, s, sid, historyOptions(20))
	var owner *ChatHistoryEntry
	for i := range page.Entries {
		if page.Entries[i].Position == position {
			owner = &page.Entries[i]
		}
	}
	require.NotNil(t, owner, "the real page contains the addressed owner")
	require.False(t, owner.Entry.DisplayUnavailable(), "readable alias fixture must not degrade")
	require.Len(t, owner.Entry.ToolCalls, 1)
	got, ok := owner.Entry.ToolCalls[0].HistoryDisplayPreview()
	require.True(t, ok, "real streamed reader created the preview")
	assert.Equal(t, want.OriginalSizeBytes, got.OriginalSizeBytes, "SEC-R5-1 exact encoded size")
	assert.Equal(t, want.Preview, got.Preview, "SEC-R5-1 exact preview bytes must equal full-decode-then-shorten")
	assert.Equal(t, full, *owner, "SEC-R5-1 entire reduced/shortened owner, including correction/cancellation metadata")
	after, err := os.ReadFile(canonical)
	require.NoError(t, err)
	require.Equal(t, before, after, "display and full-entry reads change no canonical bytes")
}

// SEC-R5-1 / A2.8's separate confidentiality/memory rule: private content is
// skipped without being kept, even when small enough not to need a stand-in.
func TestHistoryStreamScanner_R5PrivateModelContentIsNotRetained(t *testing.T) {
	for _, tc := range []struct {
		name, member, value string
		duplicate           bool
	}{
		{"small", "model_message", "PRIVATE_R5_SMALL", false},
		{"large", "model_message", "PRIVATE_R5_LARGE" + strings.Repeat("p", 70000), false},
		{"duplicate_case_alias", "Model_Message", "PRIVATE_R5_DUP" + strings.Repeat("d", 70000), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := `"content":` + historyR5JSONString(t, tc.value)
			if tc.duplicate {
				content = `"content":"PRIVATE_R5_SUPERSEDED",` + content
			}
			private := `{"role":"assistant",` + content + `,"thinking_blocks":["PRIVATE_R5_THINKING"]}`
			raw := `{` + historyR5JSONString(t, tc.member) + `:` + private + `,"public":{"kept":"PUBLIC_R5_CONTROL"}}` + "\n"
			ins := newHistoryStandIns()
			reduced, err := historyStreamRecord(strings.NewReader(raw), ins)
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, json.Unmarshal(reduced, &got))
			assert.Equal(t, map[string]any{"kept": "PUBLIC_R5_CONTROL"}, got["public"], "positive control: public sibling is not skipped")
			assert.Nil(t, got[tc.member], "SEC-R5-1 private subtree must be absent or null, never retained in scanner output")
			assert.NotContains(t, string(reduced), "PRIVATE_R5_", "SEC-R5-1 no small private content retained")
			assert.Equal(t, 0, len(ins.values), "SEC-R5-1 private-only values must not create retained stand-ins or digest keys")
			for _, retained := range ins.values {
				assert.NotContains(t, string(retained.decodedPrefix), "PRIVATE_R5_", "SEC-R5-1 private decoded prefix retained")
				assert.NotContains(t, string(retained.encodedPrefix), "PRIVATE_R5_", "SEC-R5-1 private encoded prefix retained")
			}
		})
	}
}
