// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ws_hub_projection.go: the active-turn projection (#823 BE-DESIGN.md §4.4).
//
// A snapshot catch-up rebuilds a session from its persisted transcript, read
// AFTER the connection was bound to the hub. What the transcript cannot hold
// yet is everything published but not persisted: the answer text streaming
// right now, a tool call still running, a delegate span still open. The
// projection keeps exactly that, updated inside the hub's publish critical
// section, so "what the snapshot shows" plus "what arrives live after the
// bind" is always the whole session with no gap. Items the transcript read
// already covers are filtered out by id when the snapshot is sent, so nothing
// is shown twice.
package gateway

import (
	"encoding/json"
	"log/slog"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// hubFrameKind classifies a published frame for the projection. Frames of
// kind hubKindOther do not touch it.
type hubFrameKind uint8

const (
	hubKindOther hubFrameKind = iota
	hubKindToken
	hubKindDone
	hubKindToolStart
	hubKindToolResult
	hubKindSpanStart
	hubKindSpanEnd
	hubKindItem // media, turn-level error: shown until the turn's done
)

// hubFrameMeta is what publish needs to know about a frame to keep the
// projection current, without re-parsing the frame's JSON.
type hubFrameMeta struct {
	kind hubFrameKind
	// key: tool call id (tool kinds), span id (span kinds), or a unique
	// item key (hubKindItem).
	key string
	// token only.
	turnID    string
	messageID string
	agentID   string
	content   string
}

// Projection bounds (BE-DESIGN.md §3.1).
const (
	hubProjectionMaxBytes = 2 << 20 // 2 MiB of retained active-turn state
	// hubProjectionTokenChunk bounds one snapshot token frame's content:
	// TokenFrame.content has maxLength 65536 in the contract.
	hubProjectionTokenChunk = 64 * 1024
)

// projItem is one still-unfinished thing a snapshot must reproduce.
type projItem struct {
	kind hubFrameKind // hubKindToken, hubKindToolStart (a tool call), hubKindSpanStart (a span), hubKindItem
	key  string
	id   string // tool call id / span id (what the replay's emitted set holds)

	// Token items: the message text so far.
	turnID    string
	messageID string
	agentID   string
	text      []byte

	// Tool / span / item frames, unsequenced, in the order they must be sent.
	start []byte
	end   []byte

	spanOpen bool
}

// activeTurnProjection is guarded by sessionHub.mu.
type activeTurnProjection struct {
	order []string
	items map[string]*projItem
	bytes int
	// truncated: items were evicted this turn to stay within budget (the
	// warning is logged once per turn).
	truncated bool
}

// projSnapshotItem is a copied-out projection item, safe to read after
// sessionHub.mu is released.
type projSnapshotItem struct {
	kind      hubFrameKind
	key       string
	id        string
	turnID    string
	messageID string
	agentID   string
	text      string
	start     []byte
	end       []byte
}

// active reports whether any turn or span is still unfinished — the gate
// that keeps an idle-eviction sweep from dropping a hub mid-turn (§3.2).
func (p *activeTurnProjection) active() bool {
	return len(p.items) > 0
}

func (p *activeTurnProjection) item(key, id string, kind hubFrameKind) *projItem {
	if p.items == nil {
		p.items = make(map[string]*projItem)
	}
	it := p.items[key]
	if it == nil {
		it = &projItem{kind: kind, key: key, id: id}
		p.items[key] = it
		p.order = append(p.order, key)
	}
	return it
}

// update applies one published frame (unsequenced bytes) to the projection.
// Caller holds sessionHub.mu.
func (p *activeTurnProjection) update(meta hubFrameMeta, frame []byte, sessionID string) {
	var touched string
	switch meta.kind {
	case hubKindToken:
		key := "msg:" + meta.messageID
		if meta.messageID == "" {
			key = "turn:" + meta.turnID
		}
		it := p.item(key, meta.messageID, hubKindToken)
		it.turnID, it.messageID = meta.turnID, meta.messageID
		if meta.agentID != "" {
			it.agentID = meta.agentID
		}
		it.text = append(it.text, meta.content...)
		p.bytes += len(meta.content)
		touched = key
	case hubKindToolStart:
		it := p.item("tool:"+meta.key, meta.key, hubKindToolStart)
		p.bytes += len(frame) - len(it.start)
		it.start = frame
		touched = "tool:" + meta.key
	case hubKindToolResult:
		it := p.item("tool:"+meta.key, meta.key, hubKindToolStart)
		p.bytes += len(frame) - len(it.end)
		it.end = frame
		touched = "tool:" + meta.key
	case hubKindSpanStart:
		it := p.item("span:"+meta.key, meta.key, hubKindSpanStart)
		p.bytes += len(frame) - len(it.start)
		it.start = frame
		it.spanOpen = true
		touched = "span:" + meta.key
	case hubKindSpanEnd:
		it := p.item("span:"+meta.key, meta.key, hubKindSpanStart)
		p.bytes += len(frame) - len(it.end)
		it.end = frame
		it.spanOpen = false
		touched = "span:" + meta.key
	case hubKindItem:
		it := p.item("item:"+meta.key, meta.key, hubKindItem)
		p.bytes += len(frame) - len(it.start)
		it.start = frame
		touched = "item:" + meta.key
	case hubKindDone:
		// done(T) is published only after T's final answer was persisted
		// (persist-before-done, §4.3), and every earlier round and tool call
		// was persisted before it too — so everything but a still-open span
		// (an async delegate outlives its parent's turn) is covered by any
		// transcript read taken from here on.
		p.clearExceptOpenSpans()
		return
	default:
		return
	}
	if p.bytes > hubProjectionMaxBytes {
		evicted := p.evictOlder(touched)
		if evicted > 0 && !p.truncated {
			p.truncated = true
			slog.Warn("ws: active-turn projection exceeded its budget — evicting its oldest items; a snapshot now "+
				"shows the transcript plus the newest in-progress items",
				"event", "hub_projection_truncated", "session_id", sessionID, "bytes", p.bytes, "evicted", evicted)
		}
	}
}

// evictOlder drops the oldest items, never the one just updated (keep),
// until the projection is back within hubProjectionMaxBytes or keep is the
// only item left. The item being written — typically the answer still
// streaming — therefore always stays whole (#823 review finding 4: the first
// version froze every message's text at the cap instead). An evicted item
// was published but not yet persisted, so a snapshot taken meanwhile no
// longer shows it; that is the budget's accepted cost, logged once per turn.
func (p *activeTurnProjection) evictOlder(keep string) int {
	evicted := 0
	for p.bytes > hubProjectionMaxBytes {
		// Oldest first, sparing still-open delegate spans (small, and the
		// only record that a delegate is running) until nothing else is left.
		victim := -1
		for i, key := range p.order {
			if it := p.items[key]; key != keep && (it == nil || !it.spanOpen) {
				victim = i
				break
			}
		}
		if victim < 0 {
			for i, key := range p.order {
				if key != keep {
					victim = i
					break
				}
			}
		}
		if victim < 0 {
			break
		}
		key := p.order[victim]
		if it := p.items[key]; it != nil {
			p.bytes -= len(it.text) + len(it.start) + len(it.end)
		}
		delete(p.items, key)
		p.order = append(p.order[:victim], p.order[victim+1:]...)
		evicted++
	}
	return evicted
}

// forgetTurnText drops the streamed text of turnID's messages. Used only
// for a turn abandoned without a done (ADR-082 review CR4/F2): its text was
// never persisted and no done will ever close it, so keeping it would show
// every later snapshot a phantom, never-finishing message.
func (p *activeTurnProjection) forgetTurnText(turnID string) {
	if turnID == "" {
		return
	}
	keepOrder := p.order[:0]
	for _, key := range p.order {
		it := p.items[key]
		if it != nil && it.kind == hubKindToken && it.turnID == turnID {
			p.bytes -= len(it.text)
			delete(p.items, key)
			continue
		}
		keepOrder = append(keepOrder, key)
	}
	p.order = keepOrder
}

func (p *activeTurnProjection) clearExceptOpenSpans() {
	var keepOrder []string
	keep := make(map[string]*projItem)
	bytes := 0
	for _, key := range p.order {
		it := p.items[key]
		if it != nil && it.kind == hubKindSpanStart && it.spanOpen {
			keepOrder = append(keepOrder, key)
			keep[key] = it
			bytes += len(it.start) + len(it.end)
		}
	}
	p.order, p.items, p.bytes, p.truncated = keepOrder, keep, bytes, false
}

// snapshot copies the projection out for use after the hub lock is released.
func (p *activeTurnProjection) snapshot() []projSnapshotItem {
	out := make([]projSnapshotItem, 0, len(p.order))
	for _, key := range p.order {
		it := p.items[key]
		if it == nil {
			continue
		}
		out = append(out, projSnapshotItem{
			kind: it.kind, key: it.key, id: it.id, turnID: it.turnID, messageID: it.messageID,
			agentID: it.agentID, text: string(it.text), start: it.start, end: it.end,
		})
	}
	return out
}

// projectionFrames turns snapshot items the transcript replay did NOT
// already emit into the unsequenced frames a snapshot catch-up sends after
// the replay (BE-DESIGN.md §4.1 A6). emitted holds every message id, tool
// call id and span id the replay sent.
func projectionFrames(sessionID string, items []projSnapshotItem, emitted map[string]bool) [][]byte {
	var out [][]byte
	for _, it := range items {
		switch it.kind {
		case hubKindToken:
			if it.messageID != "" && emitted[it.messageID] {
				continue
			}
			out = append(out, projectionTokenFrames(sessionID, it)...)
		case hubKindToolStart:
			if emitted[it.id] {
				continue
			}
			out = appendNonEmpty(out, it.start, it.end)
		case hubKindSpanStart:
			if emitted[it.id] {
				continue
			}
			out = appendNonEmpty(out, it.start, it.end)
		case hubKindItem:
			out = appendNonEmpty(out, it.start)
		}
	}
	return out
}

func appendNonEmpty(out [][]byte, frames ...[]byte) [][]byte {
	for _, f := range frames {
		if len(f) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// projectionTokenFrames renders one message's text so far as token frames of
// at most hubProjectionTokenChunk bytes each (split on a rune boundary): the
// first with replace:true (the client replaces whatever the bubble holds),
// the rest appended.
func projectionTokenFrames(sessionID string, it projSnapshotItem) [][]byte {
	if it.text == "" {
		return nil
	}
	var out [][]byte
	rest := it.text
	first := true
	for rest != "" {
		cut := len(rest)
		if cut > hubProjectionTokenChunk {
			cut = hubProjectionTokenChunk
			for cut > 0 && !utf8.RuneStart(rest[cut]) {
				cut--
			}
		}
		frame := generated.TokenFrame{
			Type:      string(generated.WsFrameTypeToken),
			SessionId: sessionID,
			Content:   rest[:cut],
		}
		if first {
			replace := true
			frame.Replace = &replace
		}
		if it.turnID != "" {
			turnID := it.turnID
			frame.TurnId = &turnID
		}
		if it.messageID != "" {
			messageID := it.messageID
			frame.MessageId = &messageID
		}
		if it.agentID != "" {
			agentID := it.agentID
			frame.AgentId = &agentID
		}
		data, err := json.Marshal(frame)
		if err != nil {
			slog.Error("ws: marshal projection token failed", "session_id", sessionID, "error", err)
			return out
		}
		out = append(out, data)
		rest = rest[cut:]
		first = false
	}
	return out
}
