package session

import "errors"

// historyNestedDelegateParents resolves only the actual parent occurrences
// named by this group's nested calls. The existing latest-call index supplies
// bounded addresses; a page edge is never evidence that a parent is absent.
// Return IDs, not parent content or invented span/lifecycle state.
func historyNestedDelegateParents(t *chatHistoryIndexTree, dir string, cut ChatHistoryReadCut, entry TranscriptEntry) ([]ToolCallID, error) {
	var parents []ToolCallID
	seen := map[ToolCallID]bool{}
	for _, call := range entry.ToolCalls {
		id := call.ParentToolCallID
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		var ref historyDuplicateRef
		ok, err := t.get(historyIDKey("dup/tool/", string(id)), &ref)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue // Genuinely absent/expired parent keeps the orphan path.
		}
		meta, ok, err := historyReadRecordMeta(t, ref.Rank)
		if err != nil {
			return nil, err
		}
		if !ok || !meta.Eligible {
			return nil, errors.New("missing nested parent occurrence")
		}
		parent, err := historyReadArchive(t.ctx, dir, meta, cut, "page", "dependency")
		if err != nil {
			return nil, err
		}
		t.resolvedDependency("archive", ref.Rank)
		// As in replay's latest-by-ID map, the last matching call in the
		// canonical occurrence wins when a malformed row repeats an ID.
		found := false
		for i := len(parent.ToolCalls) - 1; i >= 0; i-- {
			candidate := parent.ToolCalls[i]
			if candidate.ID != id {
				continue
			}
			found = true
			switch candidate.Tool {
			case "spawn", "delegate", "create_task":
				parents = append(parents, id)
			}
			break
		}
		if !found {
			return nil, errors.New("nested parent call absent from indexed occurrence")
		}
	}
	return parents, nil
}
