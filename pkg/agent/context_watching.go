// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"sync"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// Chat-links D-watch: the one line a web chat turn's dynamic context carries
// about whether the user is watching THIS chat right now. Exact text, fixed
// by the spec ("What the agent sees").
const (
	userWatchingYes = "User watching: yes"
	userWatchingNo  = "User watching: no"

	// webchatChannelName is the SPA's session origin; only a web chat turn
	// carries the watching line (non-web channels have no UI to watch).
	webchatChannelName = "webchat"
)

// WithWatchingInjector installs the presence lookup behind the "User
// watching" line. fn(sessionID) reports whether some tab is watching that
// chat. nil means every web turn renders "User watching: no" — never yes.
func (cb *ContextBuilder) WithWatchingInjector(fn func(sessionID string) bool) *ContextBuilder {
	cb.watchingInjector = fn
	return cb
}

// userWatchingLine returns the exact watching line for a web chat turn of
// sessionID, or "" for any other channel. An unknown session, a missing
// lookup, or an expired claim all read "no" (D-presence: never default to
// yes). The line names no tab count, user or time.
func (cb *ContextBuilder) userWatchingLine(channel, sessionID string) string {
	if channel != webchatChannelName {
		return ""
	}
	if sessionID != "" && cb.watchingInjector != nil && cb.watchingInjector(sessionID) {
		return userWatchingYes
	}
	return userWatchingNo
}

// buildDynamicContext renders the dynamic context for a turn whose session is
// unknown to the caller; a web turn then reads "User watching: no".
func (cb *ContextBuilder) buildDynamicContext(delegationDepth int, workspaceID, channel, chatID, senderID, senderDisplayName string) string {
	return cb.buildDynamicContextFor("", delegationDepth, workspaceID, channel, chatID, senderID, senderDisplayName)
}

// BuildMessages assembles the provider message list for a single agent turn
// whose transcript session the caller does not name. See buildMessagesFor.
func (cb *ContextBuilder) BuildMessages(
	delegationDepth int,
	history []providers.Message,
	currentMessage string,
	media []string,
	workspaceID string,
	channel, chatID, senderID, senderDisplayName string,
	breadcrumb string,
	recallSpan []providers.Message,
	activeSkills ...string,
) []providers.Message {
	return cb.buildMessagesFor("", delegationDepth, history, currentMessage, media, workspaceID,
		channel, chatID, senderID, senderDisplayName, breadcrumb, recallSpan, activeSkills...)
}

// BuildMessagesForSession is BuildMessages for a turn of transcript session
// sessionID: the "User watching" line reports that session's own bit, never a
// parent's or a child's.
func (cb *ContextBuilder) BuildMessagesForSession(
	sessionID string,
	delegationDepth int,
	history []providers.Message,
	currentMessage string,
	media []string,
	workspaceID string,
	channel, chatID, senderID, senderDisplayName string,
	breadcrumb string,
	recallSpan []providers.Message,
	activeSkills ...string,
) []providers.Message {
	return cb.buildMessagesFor(sessionID, delegationDepth, history, currentMessage, media, workspaceID,
		channel, chatID, senderID, senderDisplayName, breadcrumb, recallSpan, activeSkills...)
}

// userWatchingByLoop holds each AgentLoop's gateway-injected presence lookup.
// It lives here rather than as an AgentLoop field because loop.go is not
// edited by this feature (chat-links U3). Keyed by the loop pointer; a loop
// that never had a lookup installed has no entry and reads "not watching".
var userWatchingByLoop sync.Map // *AgentLoop -> func(string) bool

// SetUserWatching installs the gateway's presence lookup (chat-links
// D-presence): fn(sessionID) is true while some browser tab is watching that
// chat. It feeds both the "User watching" context line and show_to_user's
// success text. nil removes it, which reads as "not watching".
func (al *AgentLoop) SetUserWatching(fn func(sessionID string) bool) {
	if fn == nil {
		userWatchingByLoop.Delete(al)
		return
	}
	userWatchingByLoop.Store(al, fn)
}

// isUserWatching is the nil-safe reader both consumers share.
func (al *AgentLoop) isUserWatching(sessionID string) bool {
	if al == nil || sessionID == "" {
		return false
	}
	v, ok := userWatchingByLoop.Load(al)
	if !ok {
		return false
	}
	fn, _ := v.(func(string) bool)
	return fn != nil && fn(sessionID)
}
