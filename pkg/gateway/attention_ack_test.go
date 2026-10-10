// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

type fakeApprovalSource struct{ entries []*approvalEntry }

func (f fakeApprovalSource) pendingApprovals() []*approvalEntry { return f.entries }

func metaOf(m map[string]*session.UnifiedMeta) func(string) (*session.UnifiedMeta, error) {
	return func(id string) (*session.UnifiedMeta, error) {
		if v, ok := m[id]; ok {
			return v, nil
		}
		return nil, fmt.Errorf("no meta %q", id)
	}
}

// Oracle: architect U11 Q1 — a pending approval on the main, or on a helper
// below it, counts for that main; an unreadable meta mid-walk makes the answer
// unknown (never false) unless something else already says true.
func TestAttention_PendingApprovalWalksHelperUpToMain(t *testing.T) {
	mainMeta := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "main-1"}, Type: session.SessionTypeMain}
	helper := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "helper-1", ParentSessionID: "main-1"}, Type: session.SessionTypeDelegate}
	grandchild := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "helper-2", ParentSessionID: "helper-1"}, Type: session.SessionTypeDelegate}
	other := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "main-2"}, Type: session.SessionTypeMain}
	metas := map[string]*session.UnifiedMeta{"main-1": mainMeta, "helper-1": helper, "helper-2": grandchild, "main-2": other}

	got := pendingApprovalMainsFor(fakeApprovalSource{[]*approvalEntry{{SessionID: "helper-2"}}}, metaOf(metas))
	require.False(t, got.unknown)
	require.Contains(t, got.mains, "main-1", "a grandchild helper's approval blocks its main")
	require.NotContains(t, got.mains, "main-2")

	require.True(t, *mainNeedsAttention(mainMeta, got), "pending approval on a helper lights the main")
	require.False(t, *mainNeedsAttention(other, got), "an unrelated main stays off")

	broken := pendingApprovalMainsFor(fakeApprovalSource{[]*approvalEntry{{SessionID: "ghost"}}}, metaOf(metas))
	require.True(t, broken.unknown)
	require.Nil(t, mainNeedsAttention(other, broken), "an unreadable source leaves the field out, never false")
	require.True(t, *mainNeedsAttention(&session.UnifiedMeta{
		SessionMeta: session.SessionMeta{ID: "main-3", Attention: session.AttentionMark{OutcomeOrder: 5}},
		Type:        session.SessionTypeMain,
	}, broken), "a known true survives an unreadable source")
}

// Oracle: C-ATTENTION — an outcome counts only while unseen.
func TestAttention_SeenOutcomeIsOff(t *testing.T) {
	m := &session.UnifiedMeta{
		SessionMeta: session.SessionMeta{ID: "m", Attention: session.AttentionMark{OutcomeOrder: 7, SeenOrder: 7}},
		Type:        session.SessionTypeMain,
	}
	require.False(t, *mainNeedsAttention(m, pendingApprovalMains{mains: map[string]struct{}{}}))
	m.Attention.OutcomeOrder = 8
	require.True(t, *mainNeedsAttention(m, pendingApprovalMains{mains: map[string]struct{}{}}))
}

// Oracle: founder rule — a missing approval registry is a wiring fault and the
// answer is unknown (field left out), never a false "nothing to see".
func TestAttention_NilApprovalRegistryIsUnknownNotFalse(t *testing.T) {
	got := pendingApprovalMainsFor(nil, metaOf(nil))
	require.True(t, got.unknown)
	require.Nil(t, mainNeedsAttention(&session.UnifiedMeta{
		SessionMeta: session.SessionMeta{ID: "m"}, Type: session.SessionTypeMain,
	}, got))
}
