package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

func historyOptionalPosition(p session.HistoryPosition) *string {
	if p == "" {
		return nil
	}
	v := string(p)
	return &v
}
func historyAppendZero[T any](values *[]T) *T {
	var zero T
	*values = append(*values, zero)
	return &(*values)[len(*values)-1]
}
func historyEmptySlice[T any](_ []T) []T { return make([]T, 0) }
func historyNewField[T any](p **T) *T    { *p = new(T); return *p }
func historyFactOrigin(e *session.ChatHistoryEntry) *generated.HistoryEntryOrigin {
	o := &generated.HistoryEntryOrigin{Position: string(e.Position)}
	if e.Entry.ID != "" {
		id := e.Entry.ID
		o.EntryId = &id
	}
	return o
}
func historyWirePageInfo(info session.ChatHistoryPageInfo, sid string) (generated.SessionHistoryPageInfo, error) {
	out := generated.SessionHistoryPageInfo{Mode: info.Mode, Limit: info.Limit, MinRows: info.MinRows, RowCount: info.RowCount, SourceGroupCount: info.SourceGroupCount, SoftByteBudget: info.SoftByteBudget, ByteCeiling: info.ByteCeiling, ProjectedBytes: info.ProjectedBytes, ByteBudgetExceeded: info.ByteBudgetExceeded, StopReason: info.StopReason, RetentionGeneration: info.RetentionGeneration, FirstPosition: historyOptionalPosition(info.FirstPosition), LastPosition: historyOptionalPosition(info.LastPosition)}
	if info.BudgetOverride != "" {
		v := info.BudgetOverride
		out.BudgetOverride = &v
	}
	if info.StoppedAtClear {
		v := true
		out.StoppedAtClear = &v
	}
	if info.LatestClear != nil {
		v := historyNewField(&out.LatestClear)
		v.EntryId = info.LatestClear.EntryID
		v.HistoryPosition = string(info.LatestClear.Position)
	}
	out.Context.Spans = historyEmptySlice(out.Context.Spans)
	out.Context.DuplicateIds = historyEmptySlice(out.Context.DuplicateIds)
	for _, d := range info.Context.DuplicateIDs {
		v := historyAppendZero(&out.Context.DuplicateIds)
		v.Kind = d.Kind
		v.Id = d.ID
		v.CanonicalPosition = string(d.CanonicalPosition)
		v.OccurrenceCount = d.OccurrenceCount
	}
	for _, span := range info.Context.Spans {
		v := historyAppendZero(&out.Context.Spans)
		v.SpanId = span.SpanID
		v.HasRun = span.HasRun
		if span.Start != nil && span.Start.Entry.SubagentStart != nil {
			f := *span.Start.Entry.SubagentStart
			f.SessionId = sid
			f.HistoryOrigin = historyFactOrigin(span.Start)
			f.SpanId = canonicalReplaySpanID(span.Start.Entry.ID, f.SpanId, f.ParentCallId)
			v.Start = &f
		}
		if span.State != nil && span.State.Entry.SubagentState != nil {
			f := *span.State.Entry.SubagentState
			f.SessionId = sid
			f.HistoryOrigin = historyFactOrigin(span.State)
			v.State = &f
		}
		if span.Message != nil && span.Message.Entry.SubagentMessage != nil {
			f := *span.Message.Entry.SubagentMessage
			f.SessionId = sid
			f.HistoryOrigin = historyFactOrigin(span.Message)
			v.Message = &f
		}
		if span.End != nil && span.End.Entry.SubagentEnd != nil {
			f := replayedSubagentEnd(span.End.Entry)
			f.SessionId = sid
			f.HistoryOrigin = historyFactOrigin(span.End)
			v.End = &f
		}
	}
	return out, nil
}

// Only generated envelopes determine field names/order/omission. Replacing the
// generated empty entries array splices already prepared generated Message JSON
// into that envelope, rather than re-encoding large selected groups per prefix.
func historyEncodeResponse(response generated.SessionHistoryResponse, memo map[session.HistoryPosition]historyPreparedGroup) ([]byte, error) {
	entries := response.Entries
	response.Entries = []generated.Message{}
	envelope, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	marker := []byte(`"entries":[]`)
	at := bytes.Index(envelope, marker)
	if at < 0 {
		return nil, errors.New("missing generated history entries field")
	}
	prefix := envelope[:at+len(`"entries":`)]
	suffix := envelope[at+len(marker):]
	size := int64(len(prefix) + len(suffix) + 2)
	for _, entry := range entries {
		if entry.HistoryPosition == nil {
			return nil, errHistoryPageProtocol
		}
		g, ok := memo[session.HistoryPosition(*entry.HistoryPosition)]
		if !ok {
			return nil, errHistoryPageProtocol
		}
		if err = historyAddBytes(&size, int64(len(g.encoded)+1)); err != nil {
			return nil, err
		}
	}
	if size > int64(math.MaxInt) {
		return nil, errors.New("history carrier size overflow")
	}
	out := make([]byte, 0, int(size))
	out = append(out, prefix...)
	out = append(out, '[')
	for i, entry := range entries {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, memo[session.HistoryPosition(*entry.HistoryPosition)].encoded...)
	}
	out = append(out, ']')
	out = append(out, suffix...)
	return out, nil
}
func historyAddBytes(total *int64, n int64) error {
	if n < 0 || *total > math.MaxInt64-n {
		return errors.New("history carrier size overflow")
	}
	*total += n
	return nil
}

// The size meter excludes its diagnostic accounting from the generated page
// metadata. It still includes actual source ownership, continuation and context
// plus historical batch overhead exactly once, not transport/live controls.
func historyMeterEnvelope(value any) (int64, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	var envelope map[string]json.RawMessage
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return 0, err
	}
	var info map[string]json.RawMessage
	if err = json.Unmarshal(envelope["page_info"], &info); err != nil {
		return 0, err
	}
	for _, key := range []string{"projected_bytes", "soft_byte_budget", "byte_ceiling", "byte_budget_exceeded", "budget_override", "stop_reason"} {
		delete(info, key)
	}
	envelope["page_info"], err = json.Marshal(info)
	if err != nil {
		return 0, err
	}
	delete(envelope, "seq")
	delete(envelope, "boot_id")
	delete(envelope, "reason")
	delete(envelope, "secret_requests")
	raw, err = json.Marshal(envelope)
	return int64(len(raw)), err
}
func historyCarrierWeight(prepared preparedHistoryPage, memo map[session.HistoryPosition]historyPreparedGroup) (session.ChatHistoryWeight, error) {
	response := prepared.Response
	response.Entries = []generated.Message{}
	rest, err := historyMeterEnvelope(response)
	if err != nil {
		return session.ChatHistoryWeight{}, err
	}
	for i, e := range prepared.Response.Entries {
		if e.HistoryPosition == nil {
			return session.ChatHistoryWeight{}, errHistoryPageProtocol
		}
		g, ok := memo[session.HistoryPosition(*e.HistoryPosition)]
		if !ok {
			return session.ChatHistoryWeight{}, errHistoryPageProtocol
		}
		if err = historyAddBytes(&rest, int64(len(g.encoded))); err != nil {
			return session.ChatHistoryWeight{}, err
		}
		if i > 0 {
			if err = historyAddBytes(&rest, 1); err != nil {
				return session.ChatHistoryWeight{}, err
			}
		}
	}
	snapshot := generated.SessionSnapshotFrame{Type: "session_snapshot", SessionId: response.SessionId, HasEarlier: response.HasEarlier, HasLater: response.HasLater, NextBefore: response.NextBefore, NextAfter: response.NextAfter, PageInfo: response.PageInfo}
	ws, err := historyMeterEnvelope(snapshot)
	if err != nil {
		return session.ChatHistoryWeight{}, err
	}
	for _, raw := range prepared.ReplayFrames {
		if err = historyAddBytes(&ws, int64(len(raw))); err != nil {
			return session.ChatHistoryWeight{}, err
		}
	}
	return session.ChatHistoryWeight{RESTBytes: rest, WSBytes: ws, ProjectedBytes: max(rest, ws)}, nil
}
func validatePreparedHistoryPage(ctx context.Context, prepared preparedHistoryPage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, r := prepared.Page, prepared.Response
	i := r.PageInfo
	if r.SessionId != p.SessionID || len(r.Entries) != len(p.Entries) || len(r.Entries) > 100 || i.SourceGroupCount != len(r.Entries) || i.Limit < 20 || i.Limit > 100 || i.MinRows < 1 || i.MinRows > i.Limit || i.RowCount < 0 || i.SoftByteBudget <= 0 || i.ProjectedBytes < 0 || i.RetentionGeneration == "" {
		return errHistoryPageProtocol
	}
	if r.HasEarlier != (r.NextBefore != nil) || r.HasLater != (r.NextAfter != nil) || i.ByteBudgetExceeded != (i.ProjectedBytes > i.SoftByteBudget) || i.ByteBudgetExceeded != (i.BudgetOverride != nil) {
		return errHistoryPageProtocol
	}
	expected, err := historyWirePageInfo(p.PageInfo, p.SessionID)
	if err != nil {
		return errHistoryPageProtocol
	}
	if !reflect.DeepEqual(expected, i) || r.HasEarlier != p.HasEarlier || r.HasLater != p.HasLater || !reflect.DeepEqual(r.NextBefore, historyOptionalPosition(p.NextBefore)) || !reflect.DeepEqual(r.NextAfter, historyOptionalPosition(p.NextAfter)) {
		return errHistoryPageProtocol
	}
	if len(r.Entries) == 0 {
		if r.HasEarlier || r.HasLater || i.FirstPosition != nil || i.LastPosition != nil {
			return errHistoryPageProtocol
		}
	} else {
		if i.FirstPosition == nil || i.LastPosition == nil {
			return errHistoryPageProtocol
		}
		for n, entry := range r.Entries {
			if entry.HistoryPosition == nil || *entry.HistoryPosition != string(p.Entries[n].Position) {
				return errHistoryPageProtocol
			}
		}
		if *i.FirstPosition != *r.Entries[0].HistoryPosition || *i.LastPosition != *r.Entries[len(r.Entries)-1].HistoryPosition {
			return errHistoryPageProtocol
		}
	}
	// Only the soft budget may not underfill; a byte_ceiling stop may (A2.3).
	if i.StopReason == "byte_budget" && i.RowCount < i.MinRows {
		return errHistoryPageProtocol
	}
	// A page stays within the ceiling unless it is exactly one whole group.
	if i.ByteCeiling < 1 || i.ProjectedBytes > i.ByteCeiling && i.SourceGroupCount != 1 {
		return errHistoryPageProtocol
	}
	raw, err := json.Marshal(i)
	if err != nil {
		return errHistoryPageProtocol
	}
	if msg, _ := validateBodyAgainstSchema("SessionHistoryPageInfo", raw); msg != "" {
		return errHistoryPageProtocol
	}
	return nil
}
