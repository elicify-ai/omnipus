// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// historyPageProjectionDeps is the private preparation dependency declaration.
// Nothing is sent, offloaded or mutated by these declaration-only stubs.
type historyPageProjectionDeps struct {
	MediaStore  media.MediaStore
	ToolStore   *toolResultStore
	TerminalAsk *askuser.PendingSet
	Lifecycle   lifecycleRecordLoader
}

type preparedHistoryPage struct {
	Page         session.ChatHistoryPage
	Response     generated.SessionHistoryResponse
	ReplayFrames [][]byte
}

// historyPageRequest reserves request-local preparation state. Its factory
// refuses until the shared read-only projector/weigher is implemented.
type historyPageRequest struct{}

func newHistoryPageRequest(
	sessionID string,
	opts session.ChatHistoryPageOptions,
	deps historyPageProjectionDeps,
) (*historyPageRequest, error) {
	return nil, session.ErrChatHistoryNotImplemented
}

func (r *historyPageRequest) Read(
	ctx context.Context,
	store *session.UnifiedStore,
) (preparedHistoryPage, error) {
	return preparedHistoryPage{}, session.ErrChatHistoryNotImplemented
}
