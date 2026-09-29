// Omnipus — trusted retry receipts for incomplete view-membership moves.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

const viewMoveRetryLifetime = 7 * 24 * time.Hour

var (
	ErrViewMoveRetryExpired   = errors.New("knowledge: view move retry expired")
	ErrViewMoveRetryNotFound  = errors.New("knowledge: view move retry not found")
	ErrViewMoveRetryPreflight = errors.New("knowledge: view move retry preflight failed")
)

// PendingViewMove is stored in the private collection index in the SAME JSON
// atomic write that revokes its members. It contains the original verified
// plan; Retry must never reconstitute authority by reading view markers.
type PendingViewMove struct {
	ID             string                  `json:"id"`
	CollectionRoot string                  `json:"collection_root"`
	From           string                  `json:"from"`
	To             string                  `json:"to"`
	Folder         bool                    `json:"folder"`
	AllowAmbiguity bool                    `json:"allow_ambiguity,omitempty"`
	Members        []PendingViewMoveMember `json:"members"`
	StartedAt      time.Time               `json:"started_at"`
}

// PendingViewMoveMember records one revoked identity and its planned paths.
type PendingViewMoveMember struct {
	OldBase string `json:"old_base"`
	NewBase string `json:"new_base"`
	Name    string `json:"name"`
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}

// CompletedViewMove is an inert idempotence receipt. It never authorizes a
// view write: only an unexpired PendingViewMove with verified members can do
// that. A repeated Retry can return a true no-op rather than false success
// for an arbitrary unknown ID.
type CompletedViewMove struct {
	From        string    `json:"from"`
	To          string    `json:"to"`
	CompletedAt time.Time `json:"completed_at"`
}

func (m *ViewMembership) stagePendingMove(req RenameRequest, members []viewMemberRename) string {
	id := rand.Text()
	pending := PendingViewMove{
		ID: id, CollectionRoot: m.Root,
		From: normalizeRel(req.From), To: normalizeRel(req.To),
		Folder: req.Folder, AllowAmbiguity: req.AllowAmbiguity,
		Members:   make([]PendingViewMoveMember, 0, len(members)),
		StartedAt: time.Now().UTC(),
	}
	for _, member := range members {
		pending.Members = append(pending.Members, PendingViewMoveMember{
			OldBase: member.oldBase, NewBase: member.newBase, Name: member.name,
			OldPath: member.oldRel, NewPath: member.newRel,
		})
	}
	if m.PendingMoves == nil {
		m.PendingMoves = make(map[string]PendingViewMove)
	}
	m.PendingMoves[id] = pending
	return id
}

func (p PendingViewMove) affectedMembers() []viewMemberRename {
	members := make([]viewMemberRename, 0, len(p.Members))
	for _, member := range p.Members {
		members = append(members, viewMemberRename{
			oldBase: member.OldBase, newBase: member.NewBase,
			name: member.Name, oldRel: member.OldPath, newRel: member.NewPath,
		})
	}
	return members
}

func (p PendingViewMove) validate(root, id string) error {
	if id == "" || p.ID != id || p.CollectionRoot != root || p.StartedAt.IsZero() ||
		p.From == "" || p.To == "" || len(p.Members) == 0 {
		return fmt.Errorf("knowledge: invalid pending view move %q", id)
	}
	for _, member := range p.Members {
		if strings.TrimSpace(member.Name) == "" ||
			!validMembershipPath(member.OldBase, ".base") ||
			!validMembershipPath(member.NewBase, ".base") ||
			!validMembershipPath(member.OldPath, ".view") ||
			!validMembershipPath(member.NewPath, ".view") {
			return fmt.Errorf("knowledge: invalid pending member for view move %q", id)
		}
	}
	return nil
}

func (m *ViewMembership) finishPendingMove(id, from, to string) {
	delete(m.PendingMoves, id)
	if m.CompletedMoves == nil {
		m.CompletedMoves = make(map[string]CompletedViewMove)
	}
	m.CompletedMoves[id] = CompletedViewMove{From: from, To: to, CompletedAt: time.Now().UTC()}
}

func (m *ViewMembership) pendingMove(id string) (PendingViewMove, bool, error) {
	if p, ok := m.PendingMoves[id]; ok {
		if !time.Now().Before(p.StartedAt.Add(viewMoveRetryLifetime)) {
			return PendingViewMove{}, false, fmt.Errorf("%w: %s", ErrViewMoveRetryExpired, id)
		}
		return p, false, nil
	}
	if done, ok := m.CompletedMoves[id]; ok {
		if !time.Now().Before(done.CompletedAt.Add(viewMoveRetryLifetime)) {
			return PendingViewMove{}, false, fmt.Errorf("%w: %s", ErrViewMoveRetryExpired, id)
		}
		return PendingViewMove{ID: id, From: done.From, To: done.To}, true, nil
	}
	return PendingViewMove{}, false, fmt.Errorf("%w: %s", ErrViewMoveRetryNotFound, id)
}
