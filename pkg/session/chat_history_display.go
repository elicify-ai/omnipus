// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package session

import (
	"encoding/json"
	"sort"
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

// DisplayUnavailable reports that a bounded history page could not read this
// saved record for display (A2.8 fail-closed): the projector must show the
// D4 per-item placeholder in its place.
func (e TranscriptEntry) DisplayUnavailable() bool {
	return e.displayUnavailable
}

// historyShortenStreamed applies A2.1 to one fully reduced history entry.
// It runs after effects, cancellation and disposition, and only on the
// bounded display copy: canonical bytes, positions and model context are
// untouched. The input's ToolCalls backing array is never written.
//
// ins holds the stand-ins of the streamed display read (A2.8); nil for an
// entry decoded whole. A stand-in is resolved from its bounded prefixes
// exactly as the whole value would have been shortened; a value shown whole
// that is larger than its stand-in returns errHistoryStandInUnresolved.
func historyShortenStreamed(e TranscriptEntry, ins *historyStandIns) (TranscriptEntry, error) {
	if ins == nil || len(ins.values) == 0 {
		return historyShortenForDisplay(e)
	}
	if l, ok := ins.lookup(e.Content); ok {
		switch {
		case historyOrdinaryMessage(e) && l.decodedLen > ChatHistoryTextDisplayMaxBytes:
			e.displayContentOriginalSize = l.decodedLen
			e.Content = string(l.decodedPrefix[:historyUTF8Cut(l.decodedPrefix, ChatHistoryTextDisplayMaxBytes)])
		case l.whole():
			e.Content = string(l.decodedPrefix)
		default:
			return e, errHistoryStandInUnresolved
		}
	} else if historyOrdinaryMessage(e) && len(e.Content) > ChatHistoryTextDisplayMaxBytes {
		e.displayContentOriginalSize = len(e.Content)
		e.Content = historyUTF8Prefix(e.Content, ChatHistoryTextDisplayMaxBytes)
	}
	var err error
	if e.Summary, err = ins.resolveString(e.Summary); err != nil {
		return e, err
	}
	if len(e.ToolCalls) > 0 {
		calls := make([]ToolCall, len(e.ToolCalls))
		copy(calls, e.ToolCalls)
		for i := range calls {
			if err = historyShortenCall(&calls[i], ins); err != nil {
				return e, err
			}
		}
		e.ToolCalls = calls
	}
	encoded, err := json.Marshal(e)
	if err != nil {
		return e, err
	}
	if ins.unresolvedIn(encoded) {
		return e, errHistoryStandInUnresolved
	}
	return e, nil
}

func historyShortenCall(tc *ToolCall, ins *historyStandIns) error {
	var err error
	if tc.Error, err = ins.resolveString(tc.Error); err != nil {
		return err
	}
	if tc.Parameters, err = ins.resolveMap(tc.Parameters); err != nil {
		return err
	}
	_, isRef := tc.Result["_ref"]
	if isRef || !ins.inValue(tc.Result) {
		if !isRef {
			display, shortened, err := historyResultDisplay(tc.Result)
			if err != nil {
				return err
			}
			if shortened {
				tc.Result = nil
				tc.historyPreview = &display
			}
			return nil
		}
		tc.Result, err = ins.resolveMap(tc.Result)
		return err
	}
	display, err := historyResultDisplayStandIns(tc.Result, ins)
	if err != nil {
		return err
	}
	tc.Result = nil
	tc.historyPreview = &display
	return nil
}

// historyShortenForDisplay applies A2.1 to an entry decoded whole.
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

// resolveString turns a stand-in that still holds its whole value back into
// that value; a larger stand-in returns errHistoryStandInUnresolved.
func (s *historyStandIns) resolveString(v string) (string, error) {
	l, ok := s.lookup(v)
	if !ok {
		return v, nil
	}
	if !l.whole() {
		return v, errHistoryStandInUnresolved
	}
	return string(l.decodedPrefix), nil
}

// resolveValue is resolveString over a decoded JSON value. It copies only
// the containers it changes.
func (s *historyStandIns) resolveValue(v any) (any, error) {
	if !s.inValue(v) {
		return v, nil
	}
	switch x := v.(type) {
	case string:
		return s.resolveString(x)
	case map[string]any:
		return s.resolveMap(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			r, err := s.resolveValue(e)
			if err != nil {
				return v, err
			}
			out[i] = r
		}
		return out, nil
	}
	return v, nil
}

func (s *historyStandIns) resolveMap(m map[string]any) (map[string]any, error) {
	if !s.inValue(m) {
		return m, nil
	}
	out := make(map[string]any, len(m))
	for k, e := range m {
		r, err := s.resolveValue(e)
		if err != nil {
			return m, err
		}
		out[k] = r
	}
	return out, nil
}

// historyPrefixWriter keeps the first limit bytes written and counts all.
type historyPrefixWriter struct {
	buf   []byte
	limit int
	total int
}

func (w *historyPrefixWriter) write(b []byte) {
	w.total += len(b)
	if room := w.limit - len(w.buf); room > 0 {
		w.buf = append(w.buf, b[:min(room, len(b))]...)
	}
}

// historyEncodePrefix writes v's canonical encoding (encoding/json: sorted
// keys, HTML escaping) with each stand-in written from its bounded encoded
// prefix and counted at its exact encoded size. Without stand-ins the output
// equals json.Marshal(v).
func historyEncodePrefix(w *historyPrefixWriter, v any, ins *historyStandIns) error {
	switch x := v.(type) {
	case string:
		if l, ok := ins.lookup(x); ok {
			w.write([]byte{'"'})
			w.write(l.encodedPrefix)
			// The rest of the encoding is counted, not held.
			w.total += l.encodedLen - 1 - len(l.encodedPrefix)
			return nil
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		w.write([]byte{'{'})
		for i, k := range keys {
			if i > 0 {
				w.write([]byte{','})
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			w.write(kb)
			w.write([]byte{':'})
			if err = historyEncodePrefix(w, x[k], ins); err != nil {
				return err
			}
		}
		w.write([]byte{'}'})
		return nil
	case []any:
		w.write([]byte{'['})
		for i, e := range x {
			if i > 0 {
				w.write([]byte{','})
			}
			if err := historyEncodePrefix(w, e, ins); err != nil {
				return err
			}
		}
		w.write([]byte{']'})
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if ins.unresolvedIn(b) {
		return errHistoryStandInUnresolved
	}
	w.write(b)
	return nil
}

// historyResultDisplayStandIns is historyResultDisplay for a result holding
// stand-ins. A stand-in's encoding exceeds the inline threshold, so the
// result is always shown as a preview.
func historyResultDisplayStandIns(result map[string]any, ins *historyStandIns) (HistoryResultDisplay, error) {
	w := &historyPrefixWriter{limit: ChatHistoryResultPreviewBytes + utf8.UTFMax}
	if err := historyEncodePrefix(w, result, ins); err != nil {
		return HistoryResultDisplay{}, err
	}
	if w.total <= ChatHistoryInlineResultMaxBytes {
		return HistoryResultDisplay{}, errHistoryStandInUnresolved // cannot happen; never guess
	}
	d := HistoryResultDisplay{Preview: string(w.buf[:historyUTF8Cut(w.buf, ChatHistoryResultPreviewBytes)]), OriginalSizeBytes: w.total}
	if media, ok := result["media"]; ok {
		if ins.inValue(media) {
			return d, errHistoryStandInUnresolved // media is shown whole
		}
		d.Media, d.HasMedia = media, true
	}
	if sid, ok := result["session_id"].(string); ok {
		if ins.inValue(sid) {
			return d, errHistoryStandInUnresolved
		}
		d.ChildSessionID = sid
	}
	if text, ok := result["text"].(string); ok {
		if l, isStandIn := ins.lookup(text); isStandIn {
			d.TextNamesSessionID = l.namesSessionID
		} else {
			d.TextNamesSessionID = strings.Contains(text, `"session_id"`)
		}
	}
	return d, nil
}
