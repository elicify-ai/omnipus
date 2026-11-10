// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"strings"
	"sync"
)

// TurnOrigin is the set of inputs a turn has consumed (omnipus-system-agent
// §6.18, FR-240): the web user, a channel person (connection, person) or an
// agent request (peer). A turn that consumed ANY input from a channel person
// is channel-triggered — in a per-person conversation and in an agent's shared
// main chat alike; the check is by origin, never by session. Inputs can be
// added while the turn runs (a channel message consumed mid-turn), so the
// value is shared by pointer and safe for concurrent use.
type TurnOrigin struct {
	mu            sync.Mutex
	channelPerson string
	connection    string
	peerAgent     string
}

// NewTurnOrigin returns an empty origin (a web-user turn until inputs are added).
func NewTurnOrigin() *TurnOrigin { return &TurnOrigin{} }

// AddChannelPerson records that the turn consumed a message from person on
// connection. The first channel person names the turn's label.
func (o *TurnOrigin) AddChannelPerson(person, connection string) {
	if o == nil {
		return
	}
	person, connection = strings.TrimSpace(person), strings.TrimSpace(connection)
	if person == "" {
		person = "a person"
	}
	if connection == "" {
		connection = "a connection"
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.channelPerson == "" {
		o.channelPerson, o.connection = person, connection
	}
}

// AddPeer records that the turn consumed a request from another agent.
func (o *TurnOrigin) AddPeer(agentName string) {
	if o == nil {
		return
	}
	agentName = strings.TrimSpace(agentName)
	if agentName == "" {
		agentName = "another agent"
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.peerAgent == "" {
		o.peerAgent = agentName
	}
}

// ChannelTriggered reports whether the turn consumed any input from a
// channel person (§6.18).
func (o *TurnOrigin) ChannelTriggered() bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.channelPerson != ""
}

// Label is the server-derived approval origin label (K7 origin_label):
// "while answering <person> on <connection>" for a channel-triggered turn,
// else "asked by <Agent>" for a peer request, else "" (a web turn has no label).
func (o *TurnOrigin) Label() string {
	if o == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	switch {
	case o.channelPerson != "":
		return "while answering " + o.channelPerson + " on " + o.connection
	case o.peerAgent != "":
		return "asked by " + o.peerAgent
	}
	return ""
}

type turnOriginKey struct{}

// WithTurnOrigin attaches the turn's origin to ctx.
func WithTurnOrigin(ctx context.Context, o *TurnOrigin) context.Context {
	if o == nil {
		return ctx
	}
	return context.WithValue(ctx, turnOriginKey{}, o)
}

// ToolTurnOrigin returns the turn's origin, or nil when none is attached.
func ToolTurnOrigin(ctx context.Context) *TurnOrigin {
	if ctx == nil {
		return nil
	}
	o, _ := ctx.Value(turnOriginKey{}).(*TurnOrigin)
	return o
}
