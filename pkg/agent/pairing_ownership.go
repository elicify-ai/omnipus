// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"errors"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/channels"
)

// ErrPairingRetryConflict refuses a chat Retry whose request/session pair is
// not the channel's eligible recorded owner, or that competes with another
// chat's claim (contract ChannelEnableRequest, 409 pairing_retry_conflict).
var ErrPairingRetryConflict = errors.New("this chat can no longer retry this pairing")

// pairingOwnerState is one channel's chat ownership of its QR pairing
// attempts (spec K10 / FR-081, BDD-8.8; Train 2 gate r2 T2-R2-S1, R2-A02,
// CR-T2-04). Ownership is per attempt:
//
//   - owner is the chat whose request stamps the pairing's frames and whose
//     Stop ends it. armed means the claim was made for the NEXT attempt and
//     has not been bound to one yet (connect_channel, or a validated chat
//     Retry through the enable request).
//   - last is the chat that owned the last attempt that ended (timeout, error
//     or stopped); only that exact chat may Retry it.
//
// A new attempt ("waiting") binds an armed owner; an unarmed owner left over
// from an attempt that ended without a terminal event (the channel was
// disabled) moves to last, so the new attempt — for example one started from
// Connectors — is unowned. Subscribing to pairing frames never changes any of
// this.
type pairingOwnerState struct {
	owner *pairingCorrelation
	armed bool
	last  *pairingCorrelation
}

// pairingOwnership holds every channel's pairingOwnerState under one lock, so
// each claim is a compare-and-set against the state it was checked against.
type pairingOwnership struct {
	mu        sync.Mutex
	byChannel map[string]*pairingOwnerState
}

func (p *pairingOwnership) stateLocked(channelID string) *pairingOwnerState {
	if p.byChannel == nil {
		p.byChannel = make(map[string]*pairingOwnerState)
	}
	st := p.byChannel[channelID]
	if st == nil {
		st = &pairingOwnerState{}
		p.byChannel[channelID] = st
	}
	return st
}

func sameCorrelation(a *pairingCorrelation, b pairingCorrelation) bool {
	return a != nil && *a == b
}

// claimNext records corr as the owner of the channel's next attempt for an
// approved connect_channel. It never overwrites another chat's claim that is
// still waiting for its attempt (the first claim wins); a stale owner moves
// to last. It reports whether corr now owns the channel.
func (p *pairingOwnership) claimNext(channelID string, corr pairingCorrelation) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.stateLocked(channelID)
	if st.owner != nil && st.armed && !sameCorrelation(st.owner, corr) {
		return false
	}
	if st.owner != nil && !sameCorrelation(st.owner, corr) {
		prev := *st.owner
		st.last = &prev
	}
	owned := corr
	st.owner, st.armed = &owned, true
	return true
}

// claimRetry is the atomic chat-Retry admission: corr must be the channel's
// last owner (or its own stale, unbound owner), and no other chat may hold a
// claim. On success corr owns the next attempt.
func (p *pairingOwnership) claimRetry(channelID string, corr pairingCorrelation) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.stateLocked(channelID)
	if st.owner != nil && !sameCorrelation(st.owner, corr) {
		return ErrPairingRetryConflict
	}
	if st.owner == nil && !sameCorrelation(st.last, corr) {
		return ErrPairingRetryConflict
	}
	owned := corr
	st.owner, st.armed, st.last = &owned, true, nil
	return nil
}

// releaseClaim undoes corr's claim when the start it was made for failed:
// corr becomes the last owner again so the chat can retry. A claim another
// attempt or chat replaced is left untouched.
func (p *pairingOwnership) releaseClaim(channelID string, corr pairingCorrelation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.stateLocked(channelID)
	if !sameCorrelation(st.owner, corr) || !st.armed {
		return
	}
	last := corr
	st.owner, st.armed, st.last = nil, false, &last
}

// settleClaim disarms corr's claim once its start finished without beginning
// a pairing attempt (for example the device was already linked), so a later,
// unrelated attempt cannot inherit it.
func (p *pairingOwnership) settleClaim(channelID string, corr pairingCorrelation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.stateLocked(channelID)
	if sameCorrelation(st.owner, corr) && st.armed {
		st.armed = false
	}
}

// observe applies a pairing event to the channel's ownership and returns the
// owner whose ids the event carries (nil for an unowned attempt).
func (p *pairingOwnership) observe(channelID string, status channels.PairingStatus) *pairingCorrelation {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.stateLocked(channelID)
	if status == channels.PairingStatusWaiting {
		if st.owner != nil && !st.armed {
			prev := *st.owner
			st.owner, st.last = nil, &prev
		}
		st.armed = false
	}
	var stamp *pairingCorrelation
	if st.owner != nil {
		owned := *st.owner
		stamp = &owned
	}
	switch status {
	case channels.PairingStatusLinked:
		st.owner, st.armed, st.last = nil, false, nil
	case channels.PairingStatusError, channels.PairingStatusTimeout, channels.PairingStatusStopped:
		if st.owner != nil {
			st.last = st.owner
		}
		st.owner, st.armed = nil, false
	}
	return stamp
}

// ownedBy lists the channels whose current owner is sessionID.
func (p *pairingOwnership) ownedBy(sessionID string) map[string]pairingCorrelation {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]pairingCorrelation)
	for channelID, st := range p.byChannel {
		if st.owner != nil && st.owner.sessionID == sessionID {
			out[channelID] = *st.owner
		}
	}
	return out
}

// endWithoutAttempt handles a Stop that found no pairing in flight: the owner
// is kept as the last owner (so that chat can still Retry) instead of being
// deleted, and no stale claim survives into a later attempt.
func (p *pairingOwnership) endWithoutAttempt(channelID string, corr pairingCorrelation) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.stateLocked(channelID)
	if !sameCorrelation(st.owner, corr) {
		return
	}
	last := corr
	st.owner, st.armed, st.last = nil, false, &last
}
