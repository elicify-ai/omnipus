package session

// ToolCallProjectionUpdate is internal transcript mutation state, not a wire
// type. A known line targets that exact record; nil retains the legitimate
// latest-id behavior of callers with no archive identity. Result nil clears the
// result when Text is nil. Text instead replaces only projected text, preserving
// media/delegation fields. Previous values round-trip through this same type.
type ToolCallProjectionUpdate struct {
	ToolCallID     ToolCallID
	TranscriptLine *int
	ContentState   string
	Result         map[string]any
	Text           *string
	Error          *string
}

// UpdateToolCallProjections applies an ordered batch in one atomic rewrite.
// Exact-address misses fail visibly; unknown-address misses retain the existing
// no-op semantics. Multiple updates to one record remain ordered, including
// reverse-order rollback of repeated projections in a turn.
func (us *UnifiedStore) UpdateToolCallProjections(sessionID string, updates []ToolCallProjectionUpdate) ([]ToolCallProjectionUpdate, error) {
	var previous []ToolCallProjectionUpdate
	mutations := make([]transcriptMutation, 0, len(updates))
	for _, u := range updates {
		if u.ToolCallID == "" {
			continue
		}
		mutations = append(mutations, transcriptMutation{
			id: u.ToolCallID, line: u.TranscriptLine,
			mutate: func(tc *ToolCall, line int) {
				prev := ToolCallProjectionUpdate{
					ToolCallID: tc.ID, TranscriptLine: &line,
					ContentState: tc.ContentState, Result: tc.Result,
				}
				// An unchanged Error must stay an absent mutation in the undo.
				// Text projection can alter it for an error-bearing record.
				if u.Error != nil || u.Text != nil && (tc.Error != "" || tc.Result == nil && tc.Status == "error") {
					oldError := tc.Error
					prev.Error = &oldError
				}
				previous = append(previous, prev)
				tc.ContentState = u.ContentState
				if u.Text == nil {
					tc.Result = u.Result
				} else if tc.Result == nil && (tc.Error != "" || tc.Status == "error") {
					tc.Error = *u.Text
				} else {
					result := make(map[string]any, len(tc.Result)+1)
					for k, v := range tc.Result {
						result[k] = v
					}
					result["text"] = *u.Text
					tc.Result = result
					if tc.Error != "" {
						tc.Error = *u.Text
					}
				}
				if u.Error != nil {
					tc.Error = *u.Error
				}
			},
		})
	}
	if _, err := us.rewriteTranscriptToolCallsAt(sessionID, mutations, "update tool call projection"); err != nil {
		return nil, err
	}
	return previous, nil
}

// ReplacePendingToolCallIndexed settles the current turn's pending record under
// the same session shard as appends. It reports the actual existing row instead
// of guessing an archive/transcript occurrence pairing after the rewrite.
func (us *UnifiedStore) ReplacePendingToolCallIndexed(sessionID, turnID string, id ToolCallID, expectStatus string, replacement ToolCall) (int, bool, error) {
	line := -1
	n, err := us.rewriteTranscriptToolCallsAt(sessionID, []transcriptMutation{{
		id: id,
		match: func(entry TranscriptEntry, tc ToolCall) bool {
			return entry.Type == EntryTypeToolCall && entry.TurnID == turnID && tc.Status == expectStatus
		},
		mutate: func(tc *ToolCall, at int) {
			*tc = replacement
			line = at
		},
	}}, "settle pending tool call")
	return line, n > 0, err
}
