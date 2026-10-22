package session

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sort"
)

func historyNormalizeOptions(o ChatHistoryPageOptions) (ChatHistoryPageOptions, error) {
	if o.Limit == 0 {
		o.Limit = DefaultChatHistoryLimit
	}
	if o.MinRows == 0 {
		o.MinRows = DefaultChatHistoryMinRows
	}
	if o.SoftByteBudget == 0 {
		o.SoftByteBudget = DefaultChatHistorySoftByteBudget
	}
	if o.Limit < 20 || o.Limit > 100 || o.MinRows < 1 || o.MinRows > o.Limit || o.SoftByteBudget < 1 {
		return o, ErrInvalidHistoryPageRange
	}
	for _, p := range []HistoryPosition{o.Before, o.After, o.WindowStart, o.WindowEnd} {
		if p != "" {
			if _, err := ParseHistoryPosition(string(p)); err != nil {
				return o, err
			}
		}
	}
	if o.Before != "" && o.After != "" || (o.WindowStart == "") != (o.WindowEnd == "") || o.WindowStart != "" && o.After != "" {
		return o, ErrInvalidHistoryPageRange
	}
	if o.Weigh == nil {
		return o, historyUnavailable("projection_error", ErrHistoryProjection)
	}
	return o, nil
}
func historyWeigh(ctx context.Context, o ChatHistoryPageOptions, p ChatHistoryPage) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	w, err := o.Weigh(ctx, p)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, historyUnavailable("projection_error", err)
	}
	if w.RESTBytes < 0 || w.WSBytes < 0 || w.ProjectedBytes < 0 || w.ProjectedBytes != max(w.RESTBytes, w.WSBytes) || uint64(w.RESTBytes) > uint64(math.MaxInt) || uint64(w.WSBytes) > uint64(math.MaxInt) {
		return 0, historyUnavailable("projection_error", ErrHistoryProjection)
	}
	return w.ProjectedBytes, nil
}
func historyMode(o ChatHistoryPageOptions) string {
	switch {
	case o.WindowStart != "":
		return "window_refresh"
	case o.Before != "":
		return "older"
	case o.After != "":
		return "newer"
	default:
		return "latest"
	}
}
func historyEmptyPage(sid string, o ChatHistoryPageOptions, cp historyCheckpoint) ChatHistoryPage {
	return ChatHistoryPage{SessionID: sid, Entries: []ChatHistoryEntry{}, Cut: ChatHistoryReadCut{sessionID: sid, generation: cp.Generation, retentionGeneration: cp.RetentionGeneration}, PageInfo: ChatHistoryPageInfo{Mode: historyMode(o), Limit: o.Limit, MinRows: o.MinRows, SoftByteBudget: o.SoftByteBudget, StopReason: "end", RetentionGeneration: cp.RetentionGeneration, Context: ChatHistoryContext{Spans: []ChatHistorySpan{}, DuplicateIDs: []ChatHistoryDuplicate{}}}}
}
func historyFindEligible(t *chatHistoryIndexTree, rank uint64, forward, inclusive bool) (uint64, bool, error) {
	n, ok, err := t.seek(historyRankKey("e/", rank), forward, inclusive)
	if err != nil || !ok || !historyPrefix(n, "e/") {
		return 0, false, err
	}
	var found uint64
	err = json.Unmarshal(n.Value, &found)
	return found, err == nil, err
}
func historyPageBounds(t *chatHistoryIndexTree, cp historyCheckpoint, o ChatHistoryPageOptions) (uint64, uint64, bool, error) {
	ranks := map[HistoryPosition]uint64{}
	// Resolve all well-formed selectors in this session before comparing order.
	for _, p := range []HistoryPosition{o.Before, o.After, o.WindowStart, o.WindowEnd} {
		if p != "" {
			rank, err := historyTokenRank(t, p)
			if err != nil {
				return 0, 0, false, err
			}
			ranks[p] = rank
		}
	}
	lo, hi := uint64(1), cp.Rank
	forward := o.After != ""
	if o.WindowStart != "" {
		lo, hi = ranks[o.WindowStart], ranks[o.WindowEnd]
		if lo > hi {
			return 0, 0, false, ErrInvalidHistoryPageRange
		}
		if o.Before != "" && (ranks[o.Before] < lo || ranks[o.Before] > hi) {
			return 0, 0, false, ErrInvalidHistoryPageRange
		}
	} else if o.Before == "" && o.After == "" {
		var clearRank uint64
		if _, err := t.get("last/clear", &clearRank); err != nil {
			return 0, 0, false, err
		}
		if clearRank > lo {
			lo = clearRank
		}
	}
	if o.Before != "" {
		hi = min(hi, ranks[o.Before]-1)
	}
	if o.After != "" {
		lo = ranks[o.After] + 1
	}
	return lo, hi, forward, nil
}
func historyPageMetadata(t *chatHistoryIndexTree, cp historyCheckpoint, o ChatHistoryPageOptions, p *ChatHistoryPage) error {
	p.PageInfo.SourceGroupCount = len(p.Entries)
	p.PageInfo.RowCount = 0
	p.HasEarlier = false
	p.HasLater = false
	p.NextBefore = ""
	p.NextAfter = ""
	p.PageInfo.FirstPosition = ""
	p.PageInfo.LastPosition = ""
	for _, e := range p.Entries {
		p.PageInfo.RowCount += e.RowCount
	}
	if len(p.Entries) == 0 {
		return nil
	}
	p.PageInfo.FirstPosition = p.Entries[0].Position
	p.PageInfo.LastPosition = p.Entries[len(p.Entries)-1].Position
	first, err := historyTokenRank(t, p.PageInfo.FirstPosition)
	if err != nil {
		return err
	}
	last, err := historyTokenRank(t, p.PageInfo.LastPosition)
	if err != nil {
		return err
	}
	lower, upper := uint64(1), cp.Rank
	if o.WindowStart != "" {
		lower, err = historyTokenRank(t, o.WindowStart)
		if err != nil {
			return err
		}
		upper, err = historyTokenRank(t, o.WindowEnd)
		if err != nil {
			return err
		}
	}
	before, found, err := historyFindEligible(t, first, false, false)
	if err != nil {
		return err
	}
	p.HasEarlier = found && before >= lower
	after, found, err := historyFindEligible(t, last, true, false)
	if err != nil {
		return err
	}
	p.HasLater = found && after <= upper
	if p.HasEarlier {
		p.NextBefore = p.PageInfo.FirstPosition
	}
	if p.HasLater {
		p.NextAfter = p.PageInfo.LastPosition
	}
	return nil
}
func historyPageContext(t *chatHistoryIndexTree, dir string, cp historyCheckpoint, p *ChatHistoryPage, cache map[uint64]ChatHistoryEntry) error {
	context := ChatHistoryContext{Spans: []ChatHistorySpan{}, DuplicateIDs: []ChatHistoryDuplicate{}}
	spans := map[string]bool{}
	duplicates := map[string]bool{}
	for _, e := range p.Entries {
		rank, err := historyTokenRank(t, e.Position)
		if err != nil {
			return err
		}
		r, _, err := historyReadRecordMeta(t, rank)
		if err != nil {
			return err
		}
		if r.Span != "" {
			spans[r.Span] = true
		}
		for _, id := range r.Calls {
			var ids []string
			if _, err = t.get(historyIDKey("q/", id), &ids); err != nil {
				return err
			}
			for _, span := range ids {
				spans[span] = true
			}
		}
		pairs := [][2]string{{"message", e.Entry.ID}}
		for _, call := range e.Entry.ToolCalls {
			pairs = append(pairs, [2]string{"tool", string(call.ID)})
		}
		for _, pair := range pairs {
			key := historyIDKey("dup/"+pair[0]+"/", pair[1])
			if pair[1] == "" || duplicates[key] {
				continue
			}
			duplicates[key] = true
			var d historyDuplicateRef
			if _, err = t.get(key, &d); err != nil {
				return err
			}
			if d.Count < 2 {
				continue
			}
			canonical, ok, err := historyReadRecordMeta(t, d.Rank)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("missing duplicate canonical occurrence")
			}
			context.DuplicateIDs = append(context.DuplicateIDs, ChatHistoryDuplicate{Kind: pair[0], ID: pair[1], CanonicalPosition: canonical.Position, OccurrenceCount: d.Count})
		}
	}
	ids := make([]string, 0, len(spans))
	for id := range spans {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		var refs historySpanRef
		if ok, err := t.get(historyIDKey("s/", id), &refs); err != nil {
			return err
		} else if !ok {
			return errors.New("missing span references")
		}
		span := ChatHistorySpan{SpanID: id, HasRun: refs.HasRun}
		for i, rank := range []uint64{refs.Start, refs.State, refs.Message, refs.End} {
			if rank == 0 {
				continue
			}
			r, ok, err := historyReadRecordMeta(t, rank)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("missing span occurrence")
			}
			e, err := historyProjectEntry(t, dir, cp, p.Cut, r, cache, "dependency")
			if err != nil {
				return err
			}
			switch i {
			case 0:
				span.Start = &e
			case 1:
				span.State = &e
			case 2:
				span.Message = &e
			case 3:
				span.End = &e
			}
			t.resolvedDependency("archive", rank)
		}
		context.Spans = append(context.Spans, span)
	}
	p.PageInfo.Context = context
	return nil
}
func historySelect(t *chatHistoryIndexTree, dir string, cp historyCheckpoint, o ChatHistoryPageOptions) (ChatHistoryPage, error) {
	p := historyEmptyPage(cp.SessionID, o, cp)
	lo, hi, forward, err := historyPageBounds(t, cp, o)
	if err != nil {
		return ChatHistoryPage{}, err
	}
	var latestClear uint64
	if ok, e := t.get("last/clear", &latestClear); e != nil {
		return p, e
	} else if ok {
		r, found, e := historyReadRecordMeta(t, latestClear)
		if e != nil {
			return p, e
		}
		if found {
			p.PageInfo.LatestClear = &ChatHistoryClear{EntryID: r.Address.EntryID, Position: r.Position}
		}
	}
	if o.WindowStart != "" {
		p.PageInfo.StopReason = "range_end"
	}
	if hi < lo {
		p.PageInfo.ProjectedBytes, err = historyWeigh(t.ctx, o, p)
		return p, err
	}
	rank := hi
	if forward {
		rank = lo
	}
	rank, found, err := historyFindEligible(t, rank, forward, true)
	if err != nil {
		return p, err
	}
	if !found || rank < lo || rank > hi {
		p.PageInfo.ProjectedBytes, err = historyWeigh(t.ctx, o, p)
		return p, err
	}
	cache := map[uint64]ChatHistoryEntry{}
	for found && rank >= lo && rank <= hi {
		r, ok, e := historyReadRecordMeta(t, rank)
		if e != nil {
			return p, e
		}
		if !ok || !r.Eligible {
			return p, errors.New("invalid eligible history reference")
		}
		candidate := p
		entry, e := historyProjectEntry(t, dir, cp, p.Cut, r, cache, "selected")
		if e != nil {
			return p, e
		}
		candidate.Entries = make([]ChatHistoryEntry, 0, len(p.Entries)+1)
		if !forward {
			candidate.Entries = append(candidate.Entries, entry)
		}
		candidate.Entries = append(candidate.Entries, p.Entries...)
		if forward {
			candidate.Entries = append(candidate.Entries, entry)
		}
		if e = historyPageMetadata(t, cp, o, &candidate); e != nil {
			return p, e
		}
		if e = historyPageContext(t, dir, cp, &candidate, cache); e != nil {
			return p, e
		}
		weight, e := historyWeigh(t.ctx, o, candidate)
		if e != nil {
			return p, e
		}
		if len(p.Entries) > 0 && p.PageInfo.RowCount >= o.MinRows && weight > o.SoftByteBudget {
			if m := historyMetrics(t.ctx); m != nil {
				m.SelectedArchiveRecords--
				m.SelectedArchiveBytes -= r.Length
				m.LookaheadArchiveRecords++
				m.LookaheadArchiveBytes += r.Length
			}
			p.PageInfo.StopReason = "byte_budget"
			break
		}
		p = candidate
		p.PageInfo.ProjectedBytes = weight
		if p.PageInfo.RowCount >= o.Limit {
			p.PageInfo.StopReason = "row_target"
			break
		}
		if len(p.Entries) >= 100 {
			p.PageInfo.StopReason = "source_cap"
			break
		}
		if r.Clear && o.WindowStart == "" {
			p.PageInfo.StopReason = "clear"
			p.PageInfo.StoppedAtClear = true
			break
		}
		next, exists, e := historyFindEligible(t, rank, forward, false)
		if e != nil {
			return p, e
		}
		if !exists || next < lo || next > hi {
			break
		}
		if weight > o.SoftByteBudget && p.PageInfo.RowCount >= o.MinRows {
			p.PageInfo.StopReason = "byte_budget"
			break
		}
		rank, found = next, exists
	}
	if p.PageInfo.ProjectedBytes > o.SoftByteBudget {
		p.PageInfo.ByteBudgetExceeded = true
		p.PageInfo.BudgetOverride = "height_floor"
		if len(p.Entries) == 1 {
			p.PageInfo.BudgetOverride = "atomic_group"
		}
	}
	return p, nil
}

func (us *UnifiedStore) readChatHistoryPage(ctx context.Context, sid string, opts ChatHistoryPageOptions) (ChatHistoryPage, error) {
	o, err := historyNormalizeOptions(opts)
	if err != nil {
		return ChatHistoryPage{}, err
	}
	if err = validateSessionID(sid); err != nil {
		return ChatHistoryPage{}, err
	}
	for {
		if _, err = us.reconcileChatHistoryIndex(ctx, sid); err != nil {
			return ChatHistoryPage{}, err
		}
		h := us.lockSession(sid)
		dir := filepath.Join(us.baseDir, sid)
		cp, err := historyLoadCheckpoint(ctx, dir, sid)
		if err != nil {
			h.Unlock()
			return ChatHistoryPage{}, err
		}
		t, err := historyOpenTree(ctx, dir, cp, false)
		if err != nil {
			h.Unlock()
			return ChatHistoryPage{}, historyUnavailable("index_inconsistent", err)
		}
		release := us.historyOwner(sid).retain(filepath.Join(historyIndexPath(dir), "generations", cp.File))
		h.Unlock()
		cut := ChatHistoryReadCut{sessionID: sid, generation: cp.Generation, retentionGeneration: cp.RetentionGeneration}
		start := ChatHistoryReadMetrics{}
		if m := historyMetrics(ctx); m != nil {
			start = *m
		}
		p, err := func() (page ChatHistoryPage, readErr error) {
			// Scope cleanup to this attempt, not the whole reselect loop. Close
			// the handle before releasing the generation, including on panic.
			defer release()
			defer func() {
				if closeErr := t.file.Close(); closeErr != nil {
					readErr = errors.Join(readErr, closeErr)
				}
			}()
			readErr = historyReadEvent(ctx, chatHistoryReadEvent{SessionID: sid, Scope: "page", Phase: "cut_captured", Cut: cut})
			if readErr == nil {
				page, readErr = historySelect(t, dir, cp, o)
			}
			return page, readErr
		}()
		if ctx.Err() != nil {
			return ChatHistoryPage{}, ctx.Err()
		}
		retentionGeneration, validationErr := us.chatHistoryRetentionGeneration(ctx, sid)
		var expired []string
		if validationErr == nil {
			h = us.lockSession(sid)
			_, _, expired, _, validationErr = historyCaptureStreams(dir, cp)
			h.Unlock()
		}
		if validationErr != nil {
			return ChatHistoryPage{}, historyUnavailable("storage_error", validationErr)
		}
		if retentionGeneration != cp.RetentionGeneration || len(expired) > 0 {
			if m := historyMetrics(ctx); m != nil {
				records := m.SelectedArchiveRecords - start.SelectedArchiveRecords + m.LookaheadArchiveRecords - start.LookaheadArchiveRecords + m.DependencyArchiveRecords - start.DependencyArchiveRecords
				count := m.SelectedArchiveBytes - start.SelectedArchiveBytes + m.LookaheadArchiveBytes - start.LookaheadArchiveBytes + m.DependencyArchiveBytes - start.DependencyArchiveBytes
				m.SelectedArchiveRecords = start.SelectedArchiveRecords
				m.SelectedArchiveBytes = start.SelectedArchiveBytes
				m.LookaheadArchiveRecords = start.LookaheadArchiveRecords
				m.LookaheadArchiveBytes = start.LookaheadArchiveBytes
				m.DependencyArchiveRecords = start.DependencyArchiveRecords
				m.DependencyArchiveBytes = start.DependencyArchiveBytes
				m.DiscardedArchiveRecords += records
				m.DiscardedArchiveBytes += count
				m.RetentionRetries++
			}
			continue
		}
		if err != nil {
			if errors.Is(err, ErrHistoryPositionNotFound) || errors.Is(err, ErrInvalidHistoryPosition) || errors.Is(err, ErrInvalidHistoryPageRange) || errors.Is(err, ErrHistoryProjection) {
				return ChatHistoryPage{}, err
			}
			return ChatHistoryPage{}, historyUnavailable("storage_error", err)
		}
		return p, nil
	}
}
