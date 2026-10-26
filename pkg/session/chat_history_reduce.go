package session

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
)

func historyReduceArchive(t *chatHistoryIndexTree, cp *historyCheckpoint, a ArchiveAddress, b []byte, rec ArchiveRecord) error {
	pos, err := historyPositionForAddress(cp.SessionID, a)
	if err != nil {
		return err
	}
	rank := t.knownRanks[pos]
	if rank == 0 {
		if cp.Rank == math.MaxUint64 {
			return errors.New("history rank overflow")
		}
		rank = cp.Rank + 1
	}
	cp.Rank = max(cp.Rank, rank)
	var bound uint64
	if ok, e := t.get("p/"+string(pos), &bound); e != nil {
		return e
	} else if ok {
		return errors.New("history position binding collision")
	}
	r := historyRecord{Position: pos, Rank: rank, Address: a, Length: int64(len(b)), Digest: historyDigest(b), Role: rec.Role, Turn: rec.TurnID}
	r.Eligible = rec.ViewMembership != ViewMembershipModel && rec.Type != EntryTypeModelRef && rec.Type != EntryTypeToolCallEffect && !isSavedChatInput(rec.TranscriptEntry)
	r.Clear = IsClearMarker(rec.TranscriptEntry)
	for _, call := range rec.ToolCalls {
		if call.ID != "" {
			r.Calls = append(r.Calls, string(call.ID))
		}
	}
	if rec.ToolCallEffect != nil {
		if err = historyReduceEffect(t, &r, rec.ToolCallEffect); err != nil {
			return err
		}
	}
	if r.Eligible {
		if r.Role == "assistant" {
			if err = t.set("last/assistant", r.Rank); err != nil {
				return err
			}
			if err = t.set(historyIDKey("last/turn/", r.Turn), r.Rank); err != nil {
				return err
			}
		}
		if rec.Type == EntryTypeTurnCancelled {
			key := "last/assistant"
			if rec.TurnID != "" {
				key = historyIDKey("last/turn/", rec.TurnID)
			}
			var target uint64
			if ok, e := t.get(key, &target); e != nil {
				return e
			} else if ok {
				if err = t.set(historyRankKey("c/", target), r.Rank); err != nil {
					return err
				}
			}
		}
		if r.Clear {
			if err = t.set("last/clear", r.Rank); err != nil {
				return err
			}
		}
		if err = historyReduceDuplicate(t, "message", rec.ID, r.Rank); err != nil {
			return err
		}
		for _, call := range r.Calls {
			if err = historyReduceDuplicate(t, "tool", call, r.Rank); err != nil {
				return err
			}
		}
		if err = historyReduceSpan(t, &r, rec.TranscriptEntry); err != nil {
			return err
		}
		if err = t.set(historyRankKey("e/", r.Rank), r.Rank); err != nil {
			return err
		}
	}
	for key, value := range map[string]any{"p/" + string(pos): r.Rank, historyAddressKey(a): r.Rank, historyRankKey("r/", r.Rank): r, historyRankKey("b/"+a.PartitionKey+"/", r.Rank): r.Rank} {
		if err = t.set(key, value); err != nil {
			return err
		}
	}
	cp.ArchiveEnds[a.PartitionKey] = a.ByteOffset + int64(len(b))
	cp.CurrentKey = a.PartitionKey
	cp.Anchor = &historyAnchor{Stream: "archive", Record: r}
	return nil
}
func historyReduceDuplicate(t *chatHistoryIndexTree, kind, id string, rank uint64) error {
	if id == "" {
		return nil
	}
	key := historyIDKey("dup/"+kind+"/", id)
	var d historyDuplicateRef
	_, err := t.get(key, &d)
	if err != nil {
		return err
	}
	if d.Count == math.MaxInt {
		return errors.New("history duplicate count overflow")
	}
	d.Count++
	d.Rank = rank
	return t.set(key, d)
}
func historyReduceEffect(t *chatHistoryIndexTree, r *historyRecord, e *ToolCallEffect) error {
	var targets []ArchiveAddress
	if e.Settle != nil {
		targets = append(targets, e.Settle.Target)
	}
	for _, p := range e.Projections {
		targets = append(targets, p.Target)
	}
	for _, a := range e.Retracts {
		var rank uint64
		ok, err := t.get(historyAddressKey(a), &rank)
		if err != nil {
			return err
		}
		if !ok {
			if t.expiredAddress != nil && t.expiredAddress(a) {
				continue
			}
			return historyBuildFailure("invalid_reference", errors.New("invalid projection retraction reference"))
		}
		prior, ok, err := historyReadRecordMeta(t, rank)
		if err != nil {
			return err
		}
		if !ok {
			return historyBuildFailure("invalid_reference", errors.New("missing projection effect metadata"))
		}
		r.Targets = append(r.Targets, prior.Targets...)
	}
	for _, a := range targets {
		var rank uint64
		ok, err := t.get(historyAddressKey(a), &rank)
		if err != nil {
			return err
		}
		if !ok {
			if t.expiredAddress != nil && t.expiredAddress(a) {
				continue
			}
			return historyBuildFailure("invalid_reference", errors.New("invalid tool effect target"))
		}
		r.Targets = append(r.Targets, rank)
	}
	sort.Slice(r.Targets, func(i, j int) bool { return r.Targets[i] < r.Targets[j] })
	unique := r.Targets[:0]
	for _, rank := range r.Targets {
		if len(unique) == 0 || rank != unique[len(unique)-1] {
			unique = append(unique, rank)
		}
	}
	r.Targets = unique
	for _, rank := range r.Targets {
		refs := make([]uint64, 0, 1)
		key := historyRankKey("d/", rank)
		_, err := t.get(key, &refs)
		if err != nil {
			return err
		}
		refs = append(refs, r.Rank)
		if err = t.set(key, refs); err != nil {
			return err
		}
	}
	return nil
}
func historyReduceDisposition(t *chatHistoryIndexTree, cp *historyCheckpoint, ref historyDispositionRef, rec InputDisposition) error {
	if rec.MessageID == "" || rec.State != InputStateDiscarded || rec.Reason != InputReasonStoppedBeforeDelivery {
		return errors.New("invalid history disposition")
	}
	if err := t.set(historyIDKey("i/", rec.MessageID), ref); err != nil {
		return err
	}
	cp.DispositionEnd = ref.Offset + ref.Length
	cp.Anchor = &historyAnchor{Stream: "disposition", Offset: ref.Offset, Length: ref.Length, Digest: ref.Digest}
	return nil
}
func historyReduceSpan(t *chatHistoryIndexTree, r *historyRecord, e TranscriptEntry) error {
	parent := ""
	switch {
	case e.SubagentStart != nil:
		r.Span = e.SubagentStart.SpanId
		r.SpanKind = "start"
		parent = e.SubagentStart.ParentCallId
	case e.SubagentState != nil:
		r.Span = e.SubagentState.SpanId
		r.SpanKind = "state"
	case e.SubagentMessage != nil:
		r.Span = e.SubagentMessage.SpanId
		r.SpanKind = "message"
	case e.SubagentEnd != nil:
		r.Span = e.SubagentEnd.SpanId
		r.SpanKind = "end"
		if e.SubagentEnd.ParentCallId != nil {
			parent = *e.SubagentEnd.ParentCallId
		}
	}
	if r.Span == "" {
		return nil
	}
	key := historyIDKey("s/", r.Span)
	var refs historySpanRef
	_, err := t.get(key, &refs)
	if err != nil {
		return err
	}
	switch r.SpanKind {
	case "start":
		refs.Start = r.Rank
	case "state":
		refs.State = r.Rank
	case "message":
		refs.Message = r.Rank
	case "end":
		refs.End = r.Rank
	}
	// A lifecycle state/message proves execution only where it records it.
	if e.SubagentState != nil && e.SubagentState.State == "running" {
		v := true
		refs.HasRun = &v
	}
	if err = t.set(key, refs); err != nil {
		return err
	}
	if parent != "" {
		var spans []string
		k := historyIDKey("q/", parent)
		_, err = t.get(k, &spans)
		if err != nil {
			return err
		}
		found := false
		for _, s := range spans {
			found = found || s == r.Span
		}
		if !found {
			spans = append(spans, r.Span)
		}
		return t.set(k, spans)
	}
	return nil
}

// Apply the existing full-reader effect/cancel rules to just one addressed
// owner and its indexed corrections. No omitted chronological prefix is read.
func historyProjectEntry(t *chatHistoryIndexTree, dir string, cp historyCheckpoint, cut ChatHistoryReadCut, r historyRecord, cache map[uint64]ChatHistoryEntry, purpose string) (ChatHistoryEntry, error) {
	if v, ok := cache[r.Rank]; ok {
		return v, nil
	}
	rec, err := historyReadArchive(t.ctx, dir, r, cut, "page", purpose)
	if err != nil {
		return ChatHistoryEntry{}, err
	}
	entry := rec.TranscriptEntry
	var refs []uint64
	_, err = t.get(historyRankKey("d/", r.Rank), &refs)
	if err != nil {
		return ChatHistoryEntry{}, err
	}
	effects := make([]pendingEffect, 0, len(refs))
	parts := make([]partitionRef, 0, len(cp.ArchiveEnds))
	for _, key := range historyRetained(cp) {
		parts = append(parts, partitionRef{key: key})
	}
	for _, rank := range refs {
		meta, ok, e := historyReadRecordMeta(t, rank)
		if e != nil {
			return ChatHistoryEntry{}, e
		}
		if !ok {
			continue
		}
		effect, e := historyReadArchive(t.ctx, dir, meta, cut, "page", "dependency")
		if e != nil {
			return ChatHistoryEntry{}, e
		}
		if effect.ToolCallEffect == nil {
			return ChatHistoryEntry{}, errors.New("required correction is not an effect")
		}
		original := effect.ToolCallEffect
		filtered := *original
		if filtered.Settle != nil && filtered.Settle.Target != r.Address {
			filtered.Settle = nil
		}
		filtered.Projections = nil
		for _, p := range original.Projections {
			if p.Target == r.Address {
				filtered.Projections = append(filtered.Projections, p)
			}
		}
		effects = append(effects, pendingEffect{addr: meta.Address, effect: &filtered})
		t.resolvedDependency("archive", rank)
	}
	entries := []TranscriptEntry{entry}
	if err = applyToolCallEffects(entries, map[ArchiveAddress]int{r.Address: 0}, effects, parts); err != nil {
		return ChatHistoryEntry{}, err
	}
	var cancelRank uint64
	if ok, e := t.get(historyRankKey("c/", r.Rank), &cancelRank); e != nil {
		return ChatHistoryEntry{}, e
	} else if ok {
		meta, present, e := historyReadRecordMeta(t, cancelRank)
		if e != nil {
			return ChatHistoryEntry{}, e
		}
		if present {
			cancel, e := historyReadArchive(t.ctx, dir, meta, cut, "page", "dependency")
			if e != nil {
				return ChatHistoryEntry{}, e
			}
			entries = deriveCancelTruncation(append(entries, cancel.TranscriptEntry))
			t.resolvedDependency("archive", cancelRank)
		}
	}
	entry = entries[0]
	var disposition historyDispositionRef
	if ok, e := t.get(historyIDKey("i/", entry.ID), &disposition); e != nil {
		return ChatHistoryEntry{}, e
	} else if ok {
		b, e := historyReadBytes(t.ctx, filepathJoinDisposition(dir), disposition.Offset, disposition.Length, chatHistoryReadEvent{SessionID: cut.sessionID, Scope: "page", Purpose: "dependency", Cut: cut, Stream: "disposition"})
		if e != nil {
			return ChatHistoryEntry{}, e
		}
		if historyDigest(b) != disposition.Digest {
			return ChatHistoryEntry{}, errors.New("history disposition changed")
		}
		var d InputDisposition
		if e = json.Unmarshal(b, &d); e != nil {
			return ChatHistoryEntry{}, e
		}
		if d.ClientMessageID == "" {
			d.ClientMessageID = entry.ClientMessageID
		}
		entry.InputDisposition = &d
		t.resolvedDependency("disposition", uint64(disposition.Offset))
	}
	rows := len(entry.ToolCalls)
	if entry.Content != "" || rows == 0 {
		rows++
	}
	// Cancellation is retained replay metadata, not another drawn bubble.
	// Its source/position still participates in ordering and the source cap.
	if entry.Type == EntryTypeTurnCancelled {
		rows = 0
	}
	position, err := historyPositionForAddress(cp.SessionID, r.Address)
	if err != nil || position != r.Position {
		return ChatHistoryEntry{}, errors.New("history occurrence position disagrees with canonical address")
	}
	if t.positions == nil {
		t.positions = make(map[HistoryPosition]uint64)
	}
	t.positions[position] = r.Rank
	parents, err := historyNestedDelegateParents(t, dir, cut, entry)
	if err != nil {
		return ChatHistoryEntry{}, err
	}
	out := ChatHistoryEntry{Position: r.Position, Entry: entry, RowCount: rows, NestedDelegateParents: parents}
	cache[r.Rank] = out
	return out, nil
}
