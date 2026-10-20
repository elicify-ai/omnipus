// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// transcript_view.go: the chat-view read of a session transcript. /clear clears
// what is on screen and moves the context forward; the archive keeps every
// entry and recall can still reach it. So the chat views (REST detail, REST
// messages, the attach snapshot) start at the last /clear marker, while
// ReadTranscript stays the full transcript for every other reader (goal and task
// loops, repair, boot recovery, verifiers, ...).

package session

import "slices"

// ClearMarkerText is the content of the chat-view marker a successful /clear
// appends (type system, role system, view membership chat). The marker is
// identified by that type plus this exact content.
const ClearMarkerText = "Conversation context cleared"

// TranscriptView is a session's chat-view transcript: the entries the chat
// shows, and whether earlier entries exist before them.
type TranscriptView struct {
	Entries []TranscriptEntry
	// HasEarlier is true when Entries starts at the last /clear marker and the
	// transcript holds entries before that point.
	HasEarlier bool
}

// IsClearMarker reports whether e is the marker a successful /clear appends.
func IsClearMarker(e TranscriptEntry) bool {
	return e.Type == EntryTypeSystem && e.Role == "system" && e.Content == ClearMarkerText
}

// sinceLastClear cuts entries at the last clear marker. The marker stays the
// first entry (it renders as the divider). The person's /clear command row and
// whatever the stopped turn wrote after it sit before the marker, in the cleared
// part: a Stop can land its own entries between the command and the marker, so
// "the row just before the marker" is not a reliable link and none is kept.
func sinceLastClear(entries []TranscriptEntry) TranscriptView {
	last := -1
	for i := len(entries) - 1; i >= 0; i-- {
		if IsClearMarker(entries[i]) {
			last = i
			break
		}
	}
	if last < 0 {
		return TranscriptView{Entries: entries}
	}
	return TranscriptView{Entries: entries[last:], HasEarlier: last > 0}
}

// ReadTranscriptView is the chat-view read: the transcript since the last /clear
// marker with input-disposition labels applied and private saved inputs excluded,
// plus whether earlier entries exist. ReadTranscript retains those inputs for
// consumption and recovery; the independently saved event row remains visible.
func (us *UnifiedStore) ReadTranscriptView(sessionID string) (TranscriptView, error) {
	entries, err := us.ReadTranscriptWithDispositions(sessionID)
	if err != nil {
		return TranscriptView{}, err
	}
	view := sinceLastClear(entries)
	view.Entries = slices.DeleteFunc(view.Entries, isSavedChatInput)
	return view, nil
}
