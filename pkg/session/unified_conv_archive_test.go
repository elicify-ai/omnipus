// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the faithful CONV model-content conversion
// (unified_conv_archive.go), per CONV-P A and N2=B. Oracles are hand-written
// legacy bytes and the architect's D2/D3/D5 rules, never the code under test.

package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// convSeedContext writes a legacy `.context/<base>.jsonl` + `.meta.json`.
func convSeedContext(t *testing.T, baseDir, base, raw string, metaJSON string) {
	t.Helper()
	dir := filepath.Join(baseDir, convContextDir)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, base+".jsonl"), []byte(raw), 0o600))
	if metaJSON != "" {
		require.NoError(t, os.WriteFile(filepath.Join(dir, base+".meta.json"), []byte(metaJSON), 0o600))
	}
}

func convLoadPayloads(t *testing.T, baseDir, id string) []payloadLine {
	t.Helper()
	b := newArchiveBackend(baseDir)
	lines, err := b.payloadLines(id)
	require.NoError(t, err)
	return lines
}

func convMeta(key string) string {
	return `{"key":"` + key + `","skip":0,"count":0}`
}

// O1/CONV-P A D2: a converted assistant line preserves every saved field; the
// tool NAME is mirrored from function.name; the parsed arguments map and the
// top-level thought_signature stay ABSENT; model_origin is conv_archive.
func TestConvArchive_ConvertedAssistantPreservesSavedFields(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-c1", "chat-c1"
	raw := `{"role":"assistant","content":"calling","reasoning_content":"REASON","ts":5,` +
		`"tool_calls":[{"id":"call_0","type":"function",` +
		`"function":{"name":"read","arguments":"{ \"path\": \"a\\u00e9\" }","thought_signature":"FSIG"},` +
		`"extra_content":{"google":{"thought_signature":"GSIG"}}}]}` + "\n"
	convSeedContext(t, baseDir, base, raw, convMeta(id))

	require.NoError(t, CutoverSavedChatsAtBoot(baseDir))

	lines := convLoadPayloads(t, baseDir, id)
	require.Len(t, lines, 1)
	mp := lines[0].rec.ModelMessage
	require.NotNil(t, mp)
	require.Equal(t, ModelOriginConvArchive, lines[0].rec.ModelOrigin)
	require.Equal(t, "assistant", mp.Role)
	require.Equal(t, "REASON", mp.ReasoningContent)
	require.Len(t, mp.ToolCalls, 1)
	tc := mp.ToolCalls[0]
	require.Equal(t, "call_0", tc.ID)
	require.Equal(t, "read", tc.Name, "name mirrors function.name")
	require.NotNil(t, tc.Function)
	require.Equal(t, "{ \"path\": \"a\\u00e9\" }", tc.Function.Arguments,
		"raw argument string is byte-exact, preserving the literal unicode escape")
	require.Equal(t, "FSIG", tc.Function.ThoughtSignature)
	require.Empty(t, tc.ThoughtSignature, "top-level thought_signature must stay absent")
	require.Empty(t, tc.Arguments, "parsed arguments map must stay absent")
	require.NotNil(t, tc.ExtraContent)
	require.Equal(t, "GSIG", tc.ExtraContent.Google.ThoughtSignature)

	// The legacy source is retired only after the converted copy exists.
	_, err := os.Stat(filepath.Join(baseDir, convContextDir, base+".jsonl"))
	require.True(t, errors.Is(err, os.ErrNotExist), "the legacy source is retired after publication")
}

// O4: a source archive flagged hydrated converts every record as conv_rebuilt.
func TestConvArchive_HydratedSourceMarksRebuilt(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-h", "chat-h"
	raw := `{"role":"user","content":"hi","ts":1}` + "\n"
	convSeedContext(t, baseDir, base, raw, `{"key":"`+id+`","skip":0,"count":1,"hydrated":true}`)
	require.NoError(t, CutoverSavedChatsAtBoot(baseDir))
	lines := convLoadPayloads(t, baseDir, id)
	require.Len(t, lines, 1)
	require.Equal(t, ModelOriginConvRebuilt, lines[0].rec.ModelOrigin)
}

// O5: a repeated call_0 in two turns joins each tool result to its OWN assistant.
func TestConvArchive_RepeatedCallIDJoinsPerOccurrence(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-r", "chat-r"
	raw := `{"role":"assistant","content":"a1","ts":1,"tool_calls":[{"id":"call_0","function":{"name":"read","arguments":"{}"}}]}` + "\n" +
		`{"role":"tool","content":"r1","tool_call_id":"call_0","ts":2}` + "\n" +
		`{"role":"assistant","content":"a2","ts":3,"tool_calls":[{"id":"call_0","function":{"name":"write","arguments":"{}"}}]}` + "\n" +
		`{"role":"tool","content":"r2","tool_call_id":"call_0","ts":4}` + "\n"
	convSeedContext(t, baseDir, base, raw, convMeta(id))
	require.NoError(t, CutoverSavedChatsAtBoot(baseDir))

	lines := convLoadPayloads(t, baseDir, id)
	require.Len(t, lines, 4)
	require.NotEqual(t, lines[0].rec.ID, lines[2].rec.ID, "the two assistants are distinct occurrences")
	require.Equal(t, lines[0].rec.ID, lines[1].rec.ToolResultFor.AssistantEntryID, "first result joins the first assistant")
	require.Equal(t, lines[2].rec.ID, lines[3].rec.ToolResultFor.AssistantEntryID,
		"second result joins the second assistant, not the first")
}

// N2=B: a provably torn FINAL line (last, no closing newline, does not decode) is
// tolerated — earlier lines convert, the original is left byte-identical.
func TestConvArchive_TornFinalLineIsTolerated(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-torn", "chat-torn"
	good := `{"role":"user","content":"hello","ts":1}` + "\n"
	torn := `{"role":"assistant","content":"half` // no newline, does not decode
	convSeedContext(t, baseDir, base, good+torn, convMeta(id))
	path := filepath.Join(baseDir, convContextDir, base+".jsonl")
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	require.NoError(t, CutoverSavedChatsAtBoot(baseDir))

	lines := convLoadPayloads(t, baseDir, id)
	require.Len(t, lines, 1, "every earlier line converts")
	require.Equal(t, "hello", lines[0].rec.ModelMessage.Content)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "the original source is never rewritten")
}

// N2=B: a bad line that is NOT the last line refuses visibly.
func TestConvArchive_BadMiddleLineRefuses(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-bad", "chat-bad"
	raw := `{"role":"user","content":"ok","ts":1}` + "\n" +
		`{not json}` + "\n" +
		`{"role":"user","content":"more","ts":2}` + "\n"
	convSeedContext(t, baseDir, base, raw, convMeta(id))
	require.Error(t, CutoverSavedChatsAtBoot(baseDir), "a non-final bad line refuses")
	// Source retained.
	_, err := os.Stat(filepath.Join(baseDir, convContextDir, base+".jsonl"))
	require.NoError(t, err, "a refused conversion keeps the original source")
}

// N2=B: a last line WITH a closing newline that fails to decode refuses.
func TestConvArchive_BadLastLineWithNewlineRefuses(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-badnl", "chat-badnl"
	raw := `{"role":"user","content":"ok","ts":1}` + "\n" + `{broken}` + "\n"
	convSeedContext(t, baseDir, base, raw, convMeta(id))
	require.Error(t, CutoverSavedChatsAtBoot(baseDir))
}

// N2=B: a last line WITHOUT a newline that DOES decode converts normally.
func TestConvArchive_LastLineWithoutNewlineThatDecodesConverts(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-nonl", "chat-nonl"
	raw := `{"role":"user","content":"ok","ts":1}` + "\n" + `{"role":"assistant","content":"bye","ts":2}`
	convSeedContext(t, baseDir, base, raw, convMeta(id))
	require.NoError(t, CutoverSavedChatsAtBoot(baseDir))
	lines := convLoadPayloads(t, baseDir, id)
	require.Len(t, lines, 2)
	require.Equal(t, "bye", lines[1].rec.ModelMessage.Content)
}

// D3(2): an assistant call with no function.name refuses (nothing to mirror).
func TestConvArchive_UnnameableCallRefuses(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-noname", "chat-noname"
	raw := `{"role":"assistant","content":"a","ts":1,"tool_calls":[{"id":"call_0","function":{"arguments":"{}"}}]}` + "\n"
	convSeedContext(t, baseDir, base, raw, convMeta(id))
	require.Error(t, CutoverSavedChatsAtBoot(baseDir))
}

// D3(3): a role "tool" line with no provable occurrence join refuses.
func TestConvArchive_UnjoinableToolResultRefuses(t *testing.T) {
	baseDir := t.TempDir()
	const base, id = "chat-orphan", "chat-orphan"
	raw := `{"role":"tool","content":"r","tool_call_id":"call_0","ts":1}` + "\n"
	convSeedContext(t, baseDir, base, raw, convMeta(id))
	require.Error(t, CutoverSavedChatsAtBoot(baseDir))
}

// O7: Validate refuses every malformed conv_* provenance shape before a byte is
// written.
func TestConvArchive_ValidateRefusesBadProvenance(t *testing.T) {
	base := ArchiveRecord{
		TranscriptEntry: TranscriptEntry{ID: "x", ViewMembership: ViewMembershipModel},
		ModelOrigin:     ModelOriginConvArchive,
	}
	cases := map[string]func(*ArchiveRecord){
		"top-level thought_signature": func(r *ArchiveRecord) {
			r.ModelMessage = &ModelPayload{Role: "assistant", ToolCalls: []ModelToolCall{{ID: "c", Name: "f",
				ThoughtSignature: "TS", Function: &ModelFunctionCall{Name: "f"}}}}
		},
		"parsed arguments present": func(r *ArchiveRecord) {
			r.ModelMessage = &ModelPayload{Role: "assistant", ToolCalls: []ModelToolCall{{ID: "c", Name: "f",
				Arguments: []byte(`{}`), Function: &ModelFunctionCall{Name: "f"}}}}
		},
		"name != function.name": func(r *ArchiveRecord) {
			r.ModelMessage = &ModelPayload{Role: "assistant", ToolCalls: []ModelToolCall{{ID: "c", Name: "g",
				Function: &ModelFunctionCall{Name: "f"}}}}
		},
		"model_origin without model_message": func(r *ArchiveRecord) { r.ModelMessage = nil },
		"model_origin on model_ref": func(r *ArchiveRecord) {
			r.Type = EntryTypeModelRef
			r.ModelRef = &ModelRef{EntryID: "s", PartitionKey: "d"}
			r.ModelMessage = nil
		},
	}
	for name, mutate := range cases {
		rec := base
		mutate(&rec)
		require.Error(t, rec.Validate(), "%s must be refused", name)
	}
	// Positive control: a well-formed conv_* record passes.
	ok := base
	ok.ModelMessage = &ModelPayload{Role: "assistant", ToolCalls: []ModelToolCall{{ID: "c", Name: "f",
		Function: &ModelFunctionCall{Name: "f", Arguments: "{}"}}}}
	require.NoError(t, ok.Validate())
}
