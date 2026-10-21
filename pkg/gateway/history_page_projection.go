package gateway

import (
	"context"
	"encoding/json"
	"errors"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type historyPageProjectionDeps struct {
	MediaStore  media.MediaStore
	ToolStore   *toolResultStore
	TerminalAsk *askuser.PendingSet
	Lifecycle   lifecycleRecordLoader
}
type preparedHistoryPage struct {
	Page               session.ChatHistoryPage
	Response           generated.SessionHistoryResponse
	ReplayFrames       [][]byte
	encodedResponse    []byte
	emitted            map[string]bool
	terminalAskEmitted bool
}
type historyPreparedGroup struct {
	message            generated.Message
	encoded            []byte
	frames             [][]byte
	media              map[int]*historyPreparedMedia
	emitted            map[string]bool
	terminalAskEmitted bool
}
type historyPageRequest struct {
	sessionID string
	opts      session.ChatHistoryPageOptions
	deps      historyPageProjectionDeps
	cut       string
	memo      map[session.HistoryPosition]historyPreparedGroup
	media     *historyReadOnlyMedia
	lifecycle *historyReadOnlyLifecycle
}

func newHistoryPageRequest(sessionID string, opts session.ChatHistoryPageOptions, deps historyPageProjectionDeps) (*historyPageRequest, error) {
	if err := validateEntityID(sessionID); err != nil {
		return nil, err
	}
	if opts.Limit == 0 {
		opts.Limit = session.DefaultChatHistoryLimit
	}
	if opts.MinRows == 0 {
		opts.MinRows = session.DefaultChatHistoryMinRows
	}
	if opts.Limit < 20 || opts.Limit > 100 || opts.MinRows < 1 || opts.MinRows > opts.Limit {
		return nil, session.ErrInvalidHistoryPageRange
	}
	opts.SoftByteBudget = session.DefaultChatHistorySoftByteBudget
	return &historyPageRequest{sessionID: sessionID, opts: opts, deps: deps}, nil
}
func historyProjectionError(cause error) error {
	// Preserve cancellation, but never expose dependency text in the public copy.
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return cause
	}
	return &historyPageProjectionError{cause: cause}
}

type historyPageProjectionError struct{ cause error }

func (e *historyPageProjectionError) Error() string { return session.ErrHistoryProjection.Error() }
func (e *historyPageProjectionError) Unwrap() error { return e.cause }
func (e *historyPageProjectionError) Is(target error) bool {
	return target == session.ErrHistoryProjection
}
func (r *historyPageRequest) reset(cut string) {
	r.cut = cut
	r.memo = map[session.HistoryPosition]historyPreparedGroup{}
	r.media = nil
	if r.deps.MediaStore != nil {
		r.media = &historyReadOnlyMedia{MediaStore: r.deps.MediaStore, paths: map[string]historyMediaResolution{}}
	}
	r.lifecycle = nil
	if r.deps.Lifecycle != nil {
		r.lifecycle = &historyReadOnlyLifecycle{source: r.deps.Lifecycle, records: map[string]*session.LifecycleRecord{}, errs: map[string]error{}}
	}
}
func (r *historyPageRequest) weigh(ctx context.Context, p session.ChatHistoryPage) (session.ChatHistoryWeight, error) {
	if err := ctx.Err(); err != nil {
		return session.ChatHistoryWeight{}, err
	}
	if r.cut != p.Cut.Generation() {
		r.reset(p.Cut.Generation())
	}
	prepared, err := r.prepare(ctx, p, false)
	if err != nil {
		return session.ChatHistoryWeight{}, historyProjectionError(err)
	}
	weight, err := historyCarrierWeight(prepared, r.memo)
	if err != nil {
		return weight, historyProjectionError(err)
	}
	if historyPageFaultHook != nil {
		historyPageFaultHook(historyPageFaultEvent{SessionID: r.sessionID, Point: "after_weigh", Weight: &weight})
	}
	return weight, nil
}
func (r *historyPageRequest) Read(ctx context.Context, store *session.UnifiedStore) (preparedHistoryPage, error) {
	if store == nil {
		return preparedHistoryPage{}, historyProjectionError(errors.New("history store unavailable"))
	}
	defer func() { r.memo = nil; r.media = nil; r.lifecycle = nil }()
	r.reset("")
	opts := r.opts
	opts.Weigh = r.weigh
	page, err := store.ReadChatHistoryPage(ctx, r.sessionID, opts)
	if err != nil {
		return preparedHistoryPage{}, err
	}
	prepared, err := r.prepare(ctx, page, true)
	if err != nil {
		return preparedHistoryPage{}, historyProjectionError(err)
	}
	if historyPageFaultHook != nil {
		historyPageFaultHook(historyPageFaultEvent{SessionID: r.sessionID, Point: "before_carrier_validate", Prepared: &prepared})
	}
	if err = validatePreparedHistoryPage(ctx, prepared); err != nil {
		return preparedHistoryPage{}, err
	}
	prepared.encodedResponse, err = historyEncodeResponse(prepared.Response, r.memo)
	if err != nil {
		return preparedHistoryPage{}, historyProjectionError(err)
	}
	if msg, _ := validateBodyAgainstSchema("SessionHistoryResponse", prepared.encodedResponse); msg != "" {
		return preparedHistoryPage{}, errHistoryPageProtocol
	}
	// An unselected lookahead and all request-local preparation are not retained
	// by the store or a global/session cache after this carrier is returned.
	r.memo = nil
	r.media = nil
	r.lifecycle = nil
	return prepared, nil
}
func (r *historyPageRequest) prepare(ctx context.Context, p session.ChatHistoryPage, final bool) (preparedHistoryPage, error) {
	out := preparedHistoryPage{Page: p, Response: generated.SessionHistoryResponse{SessionId: p.SessionID, Entries: []generated.Message{}, HasEarlier: p.HasEarlier, HasLater: p.HasLater}, ReplayFrames: [][]byte{}, emitted: map[string]bool{}}
	if p.NextBefore != "" {
		v := string(p.NextBefore)
		out.Response.NextBefore = &v
	}
	if p.NextAfter != "" {
		v := string(p.NextAfter)
		out.Response.NextAfter = &v
	}
	info, err := historyWirePageInfo(p.PageInfo, p.SessionID)
	if err != nil {
		return out, err
	}
	out.Response.PageInfo = info
	seenMedia := make(map[string]struct{})
	for _, entry := range p.Entries {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		group, ok := r.memo[entry.Position]
		if !ok {
			group, err = r.prepareGroup(ctx, p, entry)
			if err != nil {
				return out, err
			}
			r.memo[entry.Position] = group
		}
		out.Response.Entries = append(out.Response.Entries, group.message)
		out.ReplayFrames, err = group.appendFrames(out.ReplayFrames, seenMedia)
		if err != nil {
			return out, err
		}
		for id := range group.emitted {
			out.emitted[id] = true
		}
		out.terminalAskEmitted = out.terminalAskEmitted || group.terminalAskEmitted
	}
	// Historical completion overhead is emitted/metered once, not per owner.
	done := generated.DoneFrame{Type: string(generated.WsFrameTypeDone), SessionId: p.SessionID}
	b, err := json.Marshal(done)
	if err != nil {
		return out, err
	}
	out.ReplayFrames = append(out.ReplayFrames, b)
	_ = final
	return out, nil
}
