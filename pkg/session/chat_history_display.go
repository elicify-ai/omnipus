// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// HistoryResultDisplay is the non-wire record of one tool result that a
// bounded history page shows as a preview (chat-history paging spec,
// "Amendment 2026-10-27 (founder, #1452 D2)", A2.1). Only the gateway
// projector maps it to the generated HistoryResultPreview type. The facts the
// history projection consumes from a result are derived from the FULL reduced
// value before it is replaced, so tool media and child-session
// classification rebuild exactly as they did from the whole result.
type HistoryResultDisplay struct {
	// Preview is the first at most ChatHistoryResultPreviewBytes bytes of
	// the result's JSON encoding, cut back to a UTF-8 character boundary.
	Preview string
	// OriginalSizeBytes is the exact size of that JSON encoding.
	OriginalSizeBytes int
	// Media is the full value's "media" member, present when HasMedia.
	Media    any
	HasMedia bool
	// ChildSessionID is the full value's top-level "session_id" string,
	// verbatim (possibly blank); empty when absent or not a string.
	ChildSessionID string
	// TextNamesSessionID records that the full value's "text" string member
	// contains the quoted key "session_id" (a delegate run's JSON text).
	TextNamesSessionID bool
}

// HistoryDisplayPreview reports the display preview that replaced this call's
// Result on a bounded history page, if any.
func (tc ToolCall) HistoryDisplayPreview() (HistoryResultDisplay, bool) {
	if tc.historyPreview == nil {
		return HistoryResultDisplay{}, false
	}
	return *tc.historyPreview, true
}

// DisplayContentOriginalSizeBytes reports the exact UTF-8 length of the saved
// Content when a bounded history page shortened it for display, else 0.
func (e TranscriptEntry) DisplayContentOriginalSizeBytes() int {
	return e.displayContentOriginalSize
}

// historyUTF8Cut returns the length of the longest prefix of b that is at
// most limit bytes long and does not end inside a UTF-8 character.
func historyUTF8Cut[T string | []byte](b T, limit int) int {
	if len(b) <= limit {
		return len(b)
	}
	cut := limit
	for back := 0; cut > 0 && back < utf8.UTFMax && !utf8.RuneStart(b[cut]); back++ {
		cut--
	}
	return cut
}

// historyUTF8Prefix returns a copy of the longest prefix of s that is at most
// limit bytes long and does not end inside a UTF-8 character. A shortened
// prefix is copied so the bounded display value never keeps the complete
// source allocation reachable (#1452 security gate SEC-1).
func historyUTF8Prefix(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return strings.Clone(s[:historyUTF8Cut(s, limit)])
}

// historyOrdinaryMessage is the existing semantic default: an empty/omitted
// type means message (TranscriptEntry.Type, Message.yaml `type`).
func historyOrdinaryMessage(e TranscriptEntry) bool {
	return (e.Type == "" || e.Type == EntryTypeMessage) && (e.Role == "user" || e.Role == "assistant")
}

// historyShortenForDisplay applies A2.1 to one fully reduced history entry.
// It runs after effects, cancellation and disposition, and only on the
// bounded display copy: canonical bytes, positions and model context are
// untouched. The input's ToolCalls backing array is never written.
func historyShortenForDisplay(e TranscriptEntry) (TranscriptEntry, error) {
	if historyOrdinaryMessage(e) && len(e.Content) > ChatHistoryTextDisplayMaxBytes {
		e.displayContentOriginalSize = len(e.Content)
		e.Content = historyUTF8Prefix(e.Content, ChatHistoryTextDisplayMaxBytes)
	}
	var calls []ToolCall
	for i, tc := range e.ToolCalls {
		display, shortened, err := historyResultDisplay(tc.Result)
		if err != nil {
			return e, err
		}
		if !shortened {
			continue
		}
		if calls == nil {
			calls = make([]ToolCall, len(e.ToolCalls))
			copy(calls, e.ToolCalls)
		}
		calls[i].Result = nil
		calls[i].historyPreview = &display
	}
	if calls != nil {
		e.ToolCalls = calls
	}
	return e, nil
}

// historyResultDisplay measures one result's JSON encoding and, above the
// inline threshold, derives its preview and consumed facts from the full value.
// An already persisted `_ref` result passes through unchanged.
func historyResultDisplay(result map[string]any) (HistoryResultDisplay, bool, error) {
	if result == nil {
		return HistoryResultDisplay{}, false, nil
	}
	if _, isRef := result["_ref"]; isRef {
		return HistoryResultDisplay{}, false, nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return HistoryResultDisplay{}, false, err
	}
	if len(encoded) <= ChatHistoryInlineResultMaxBytes {
		return HistoryResultDisplay{}, false, nil
	}
	// Convert only the bounded prefix: string(encoded) would copy the whole
	// encoding and the preview would keep that copy alive (SEC-1).
	d := HistoryResultDisplay{Preview: string(encoded[:historyUTF8Cut(encoded, ChatHistoryResultPreviewBytes)]), OriginalSizeBytes: len(encoded)}
	d.Media, d.HasMedia = result["media"]
	if sid, ok := result["session_id"].(string); ok {
		d.ChildSessionID = sid
	}
	if text, ok := result["text"].(string); ok {
		d.TextNamesSessionID = strings.Contains(text, `"session_id"`)
	}
	return d, true, nil
}
