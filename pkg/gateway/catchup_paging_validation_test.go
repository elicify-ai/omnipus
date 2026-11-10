package gateway

import (
	"encoding/json"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// not-wire-format: test instrument state, never sent over a product boundary.
type catchupPagingBatch struct {
	snapshot generated.SessionSnapshotFrame
	sources  []generated.ReplayMessageFrame
	bytes    int64
	done     bool
}

func catchupPagingSourceCount(scenario string) (int, error) {
	// Derived from the existing scenario inputs, not recorded output. F4 saves
	// both user and assistant inside bind/read; the other snapshots save one
	// plain user row. F1 is live-only traffic; F2/F8 are journal incrementals.
	switch scenario {
	case "F4":
		return 2, nil
	case "F3", "F5", "F6", "F7":
		return 1, nil
	case "F1", "F2", "F8":
		return 0, nil
	default:
		return 0, fmt.Errorf("catchup paging: missing scenario oracle %q", scenario)
	}
}

func catchupPagingSchema(name string, raw []byte) error {
	message, serverError := validateBodyAgainstSchema(name, raw)
	if serverError || message != "" {
		return fmt.Errorf("catchup paging: generated %s validation failed: server=%t %s", name, serverError, message)
	}
	return nil
}

func catchupPagingSnapshot(scenario string, raw []byte) (*catchupPagingBatch, error) {
	if err := catchupPagingSchema("SessionSnapshotFrame", raw); err != nil {
		return nil, err
	}
	var frame generated.SessionSnapshotFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return nil, fmt.Errorf("catchup paging: decode snapshot: %w", err)
	}
	count, err := catchupPagingSourceCount(scenario)
	if err != nil {
		return nil, err
	}
	p := frame.PageInfo
	if count == 0 || p.SourceGroupCount != count || p.RowCount != count {
		return nil, fmt.Errorf("catchup paging: exact scenario source/row counts must be %d", count)
	}
	if p.Mode != "latest" || p.Limit != 40 || p.MinRows != 20 || p.SoftByteBudget != 4194304 || p.StopReason != "end" {
		return nil, fmt.Errorf("catchup paging: default options and true retained end changed")
	}
	if frame.HasEarlier || frame.HasLater || frame.NextBefore != nil || frame.NextAfter != nil || p.LatestClear != nil || p.StoppedAtClear != nil {
		return nil, fmt.Errorf("catchup paging: tiny complete fixture has no continuation or clear")
	}
	if p.ByteBudgetExceeded || p.BudgetOverride != nil || p.ProjectedBytes <= 0 || p.ProjectedBytes > p.SoftByteBudget {
		return nil, fmt.Errorf("catchup paging: actual tiny-page byte accounting is invalid")
	}
	if p.RetentionGeneration == "" || p.FirstPosition == nil || p.LastPosition == nil ||
		p.Context.Spans == nil || p.Context.DuplicateIds == nil || len(p.Context.Spans) != 0 || len(p.Context.DuplicateIds) != 0 {
		return nil, fmt.Errorf("catchup paging: range/generation/empty dependency context missing or changed")
	}
	return &catchupPagingBatch{snapshot: frame}, nil
}

func (b *catchupPagingBatch) replay(raw []byte) error {
	if b.done {
		return fmt.Errorf("catchup paging: historical source follows replay done")
	}
	if err := catchupPagingSchema("ReplayMessageFrame", raw); err != nil {
		return err
	}
	var source generated.ReplayMessageFrame
	if err := json.Unmarshal(raw, &source); err != nil {
		return fmt.Errorf("catchup paging: decode source: %w", err)
	}
	if source.SessionId != b.snapshot.SessionId || source.Id == nil || source.HistoryOrigin == nil ||
		source.HistoryOrigin.EntryId == nil || *source.HistoryOrigin.EntryId != *source.Id {
		return fmt.Errorf("catchup paging: missing or foreign source origin/entry identity")
	}
	for _, previous := range b.sources {
		if previous.HistoryOrigin.Position == source.HistoryOrigin.Position {
			return fmt.Errorf("catchup paging: distinct source occurrences share a position")
		}
	}
	b.sources = append(b.sources, source)
	b.bytes += int64(len(raw))
	return nil
}

func (b *catchupPagingBatch) replayDone(raw []byte) error {
	var frame generated.DoneFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return fmt.Errorf("catchup paging: decode replay done: %w", err)
	}
	if b.done || frame.SessionId != b.snapshot.SessionId || frame.Stats == nil || frame.Stats.FramesEmitted == nil ||
		*frame.Stats.FramesEmitted != float64(b.snapshot.PageInfo.SourceGroupCount) || frame.Stats.Tokens != nil {
		return fmt.Errorf("catchup paging: one real replay done with exact frames_emitted is required")
	}
	for _, counter := range []*float64{frame.Stats.DuplicateToolCallIdCount, frame.Stats.OrphanCount, frame.Stats.TruncatedResultCount} {
		if counter != nil && *counter != 0 {
			return fmt.Errorf("catchup paging: nonzero optional diagnostic in a no-tool fixture")
		}
	}
	b.bytes += int64(len(raw))
	b.done = true
	return nil
}

func (b *catchupPagingBatch) complete(raw []byte) error {
	var frame generated.CatchUpCompleteFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return fmt.Errorf("catchup paging: decode snapshot completion: %w", err)
	}
	count := b.snapshot.PageInfo.SourceGroupCount
	if frame.Mode != "snapshot" || frame.SessionId != b.snapshot.SessionId || frame.Seq != b.snapshot.Seq ||
		!b.done || len(b.sources) != count {
		return fmt.Errorf("catchup paging: snapshot completion lost sources, replay done or exact cut")
	}
	p := b.snapshot.PageInfo
	if *p.FirstPosition != b.sources[0].HistoryOrigin.Position || *p.LastPosition != b.sources[count-1].HistoryOrigin.Position {
		return fmt.Errorf("catchup paging: metadata range does not match ordered real source origins")
	}
	if p.ProjectedBytes < b.bytes {
		return fmt.Errorf("catchup paging: projected_bytes undercounts actual historical encoded bytes")
	}
	return nil
}

func catchupPagingState(raw []byte) error {
	if err := catchupPagingSchema("SessionStateFrame", raw); err != nil {
		return err
	}
	var frame generated.SessionStateFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return fmt.Errorf("catchup paging: decode state: %w", err)
	}
	if frame.SessionId != nil && (frame.HistoryState == nil || frame.HistoryState.Status != "available" ||
		frame.HistoryState.Reason != nil || frame.HistoryState.Message != nil) {
		return fmt.Errorf("catchup paging: scoped state must explicitly report available history")
	}
	return nil
}

func catchupPagingTab(scenario string, events []fixtureEvent) (int, error) {
	var batch *catchupPagingBatch
	snapshots := 0
	for _, event := range events {
		if event.Dir != "server→client" || event.Frame == nil {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(event.Frame, &fields); err != nil {
			return 0, fmt.Errorf("catchup paging: malformed actual frame: %w", err)
		}
		var typ string
		if err := json.Unmarshal(fields["type"], &typ); err != nil {
			return 0, fmt.Errorf("catchup paging: missing frame discriminator: %w", err)
		}
		var err error
		switch typ {
		case "session_state":
			err = catchupPagingState(event.Frame)
		case "session_snapshot":
			if batch != nil {
				return 0, fmt.Errorf("catchup paging: overlapping incomplete snapshots")
			}
			batch, err = catchupPagingSnapshot(scenario, event.Frame)
			snapshots++
		case "replay_message":
			if batch != nil {
				err = batch.replay(event.Frame)
			}
		case "done":
			if batch != nil {
				err = batch.replayDone(event.Frame)
			}
		case "catch_up_complete":
			if err = catchupPagingSchema("CatchUpCompleteFrame", event.Frame); err == nil {
				if batch != nil {
					err = batch.complete(event.Frame)
					batch = nil
				} else {
					var complete generated.CatchUpCompleteFrame
					if err = json.Unmarshal(event.Frame, &complete); err == nil && complete.Mode != "incremental" {
						err = fmt.Errorf("catchup paging: a non-incremental completion has no prepared snapshot")
					}
				}
			}
		case "error":
			return 0, fmt.Errorf("catchup paging: unexpected product error in a healthy fixture")
		}
		if err != nil {
			return 0, err
		}
	}
	if batch != nil {
		return 0, fmt.Errorf("catchup paging: actual snapshot never completed")
	}
	return snapshots, nil
}

func catchupPagingValidate(scenario string, tabs ...*fxTab) error {
	count, err := catchupPagingSourceCount(scenario)
	if err != nil {
		return err
	}
	snapshots := 0
	for _, tab := range tabs {
		n, tabErr := catchupPagingTab(scenario, tab.events)
		if tabErr != nil {
			return tabErr
		}
		snapshots += n
	}
	wantSnapshots := 0
	if count > 0 {
		wantSnapshots = 1
	}
	if snapshots != wantSnapshots {
		return fmt.Errorf("catchup paging: scenario requires exactly %d actual snapshots, got %d", wantSnapshots, snapshots)
	}
	return nil
}
