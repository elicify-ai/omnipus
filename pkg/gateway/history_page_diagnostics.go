package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

type historyPageAttempt struct {
	started       time.Time
	carrier, mode string
	opts          *session.ChatHistoryPageOptions
	requested     *int
	metrics       session.ChatHistoryReadMetrics
	page          *session.ChatHistoryPage
	err           error
}

func newHistoryPageAttempt(carrier string) *historyPageAttempt {
	return &historyPageAttempt{started: time.Now(), carrier: carrier, mode: "unparsed"}
}
func (a *historyPageAttempt) context(ctx context.Context) context.Context {
	return session.WithChatHistoryReadMetrics(ctx, &a.metrics)
}
func (a *historyPageAttempt) validated(opts session.ChatHistoryPageOptions, requested *int) {
	a.opts = &opts
	a.requested = requested
	a.mode = "latest"
	if opts.WindowStart != "" {
		a.mode = "window_refresh"
	} else if opts.Before != "" {
		a.mode = "older"
	} else if opts.After != "" {
		a.mode = "newer"
	}
}
func historyPageOutcome(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "abort"
	}
	var input *historyQueryError
	if errors.As(err, &input) || errors.Is(err, session.ErrInvalidHistoryPosition) || errors.Is(err, session.ErrInvalidHistoryPageRange) || errors.Is(err, session.ErrHistoryPositionNotFound) {
		return "invalid_request"
	}
	if errors.Is(err, errHistoryPageProtocol) {
		return "protocol"
	}
	if errors.Is(err, session.ErrHistoryProjection) {
		return "projection_error"
	}
	var unavailable *session.ChatHistoryUnavailableError
	if errors.As(err, &unavailable) {
		switch unavailable.Reason {
		case "index_missing", "index_inconsistent", "catchup_limit":
			return "index_unavailable"
		}
	}
	return "storage"
}
func (a *historyPageAttempt) finish() {
	m := a.metrics
	fields := []any{"event", "history_page", "carrier", a.carrier, "mode", a.mode, "outcome", historyPageOutcome(a.err), "duration_ms", float64(time.Since(a.started).Nanoseconds()) / 1e6, "soft_byte_budget", session.DefaultChatHistorySoftByteBudget,
		"selected_archive_records", m.SelectedArchiveRecords, "selected_archive_bytes", m.SelectedArchiveBytes,
		"lookahead_archive_records", m.LookaheadArchiveRecords, "lookahead_archive_bytes", m.LookaheadArchiveBytes,
		"dependency_archive_records", m.DependencyArchiveRecords, "dependency_archive_bytes", m.DependencyArchiveBytes,
		"recovery_archive_records", m.RecoveryArchiveRecords, "recovery_archive_bytes", m.RecoveryArchiveBytes,
		"discarded_archive_records", m.DiscardedArchiveRecords, "discarded_archive_bytes", m.DiscardedArchiveBytes,
		"archive_records_read", m.SelectedArchiveRecords + m.LookaheadArchiveRecords + m.DependencyArchiveRecords + m.RecoveryArchiveRecords + m.DiscardedArchiveRecords,
		"archive_bytes_read", m.SelectedArchiveBytes + m.LookaheadArchiveBytes + m.DependencyArchiveBytes + m.RecoveryArchiveBytes + m.DiscardedArchiveBytes,
		"disposition_records_read", m.DispositionRecordsRead, "disposition_bytes_read", m.DispositionBytesRead, "index_bytes_read", m.IndexBytesRead, "dependency_count", m.DependencyCount, "recovery_items", m.RecoveryItems, "recovery_anchor_records", m.RecoveryAnchorRecords, "recovery_overflow_records", m.RecoveryOverflowRecords, "retention_retries", m.RetentionRetries}
	if a.opts != nil {
		fields = append(fields, "limit", a.opts.Limit, "min_rows", a.opts.MinRows)
	}
	if a.requested != nil {
		fields = append(fields, "requested_limit", *a.requested)
	}
	if a.err == nil && a.page != nil {
		p := a.page
		info := p.PageInfo
		fields = append(fields, "source_group_count", info.SourceGroupCount, "row_count", info.RowCount, "projected_bytes", info.ProjectedBytes, "has_earlier", p.HasEarlier, "has_later", p.HasLater, "byte_budget_exceeded", info.ByteBudgetExceeded, "stop_reason", info.StopReason, "retention_generation", info.RetentionGeneration)
		if info.ByteBudgetExceeded {
			fields = append(fields, "budget_override", info.BudgetOverride)
		}
	}
	logsafeInfo("history_page", fields...)
}
