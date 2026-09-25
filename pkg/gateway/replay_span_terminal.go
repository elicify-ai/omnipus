// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"log/slog"
	"strconv"
	"strings"

	agent "github.com/elicify-ai/omnipus/pkg/agent"
	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// spanReplayTerminal is what one generation of a delegate span should show
// when a chat is reloaded. classifyToolCall and emitSpawnParentToolCall both
// ask this, so "still running" and the synthetic closing frame cannot drift.
type spanReplayTerminal int

const (
	// spanReplayUseToolCall is a legacy span with no persisted start. The
	// tool call's own record is the only terminal evidence that exists.
	spanReplayUseToolCall spanReplayTerminal = iota
	// spanReplayPersisted already has a saved end. Do not synthesize another.
	spanReplayPersisted
	// spanReplayOpen is the latest generation that started, with no saved
	// end and no recorded stop. The outer call is still running.
	spanReplayOpen
	// spanReplayCancelled has a recorded stop whose generation is this one.
	spanReplayCancelled
	// spanReplaySilent has no saved end, a later generation has started, and
	// no recorded stop for this generation. Show nothing.
	spanReplaySilent
)

// spanReplayTerminal reports how generation of callID should close on replay.
//
// A stop is one field on the child record, not one per generation.
// SteerCanceller.Revive keeps the old marker and only bumps Generation, so
// Stop.Generation == N still names the generation that was stopped. A later
// stampStop overwrites that field, and a same-generation completion clears
// it. When the marker for N is gone, this does not guess: an open generation
// that a later one has superseded is left unstated.
func (sr *streamReplayState) spanReplayTerminal(callID string, generation int) spanReplayTerminal {
	if generation < 1 || callID == "" {
		return spanReplayUseToolCall
	}
	spanID := agent.SubagentSpanID(callID, generation)
	if sr.persistedSubagentEndSpans[spanID] {
		return spanReplayPersisted
	}
	if !sr.persistedSubagentStartSpans[spanID] {
		return spanReplayUseToolCall
	}
	if sr.recordedStopMatches(callID, generation) {
		return spanReplayCancelled
	}
	latest, ok := sr.latestStartedGeneration(callID)
	if !ok || latest == generation {
		return spanReplayOpen
	}
	return spanReplaySilent
}

// delegateCallOpensSpawnSpan reports whether replaying this tool call should
// bracket a child span. A poll does not. A launch that was refused or failed
// before any child existed does not. spawn and create_task are launch tools.
// On delegate, only action "run" launches; an omitted action is run, which is
// the tool's own default. follow_up is not one of these: the tool names that
// action "follow_up", and its chat line is the persisted later generation on
// the original run's call, not a second span built from the follow_up call.
func (sr *streamReplayState) delegateCallOpensSpawnSpan(tc session.ToolCall) bool {
	switch tc.Tool {
	case "spawn", "create_task":
		return !sr.launchRefusedWithoutChild(tc)
	case "delegate":
		if !delegateActionLaunchesChild(tc) {
			return false
		}
		return !sr.launchRefusedWithoutChild(tc)
	default:
		return false
	}
}

// delegateActionLaunchesChild is true for a delegate call whose action starts
// a child. The tool's execute treats a missing or blank action as "run".
func delegateActionLaunchesChild(tc session.ToolCall) bool {
	if tc.Parameters == nil {
		return true
	}
	raw, present := tc.Parameters["action"]
	if !present || raw == nil {
		return true
	}
	action, ok := raw.(string)
	if !ok {
		return false
	}
	switch strings.TrimSpace(action) {
	case "", "run":
		return true
	default:
		return false
	}
}

// launchRefusedWithoutChild is an error or denial that never created a child.
// A persisted start, a nested child tool call, or a session id in the result
// means a child existed, so a legacy launch that failed after starting still
// brackets. Success, interrupted, and parked are not refusals: a legacy
// launch with no persisted start has only the tool call as its record.
func (sr *streamReplayState) launchRefusedWithoutChild(tc session.ToolCall) bool {
	switch tc.Status {
	case "error", "denied":
	default:
		return false
	}
	callID := string(tc.ID)
	if callID != "" && sr.persistedSubagentStartSpans[agent.SubagentSpanID(callID, 1)] {
		return false
	}
	if callID != "" && sr.spawnIDsWithChildren[callID] {
		return false
	}
	return !toolCallNamesChildSession(tc)
}

// toolCallNamesChildSession is the child id a successful delegate run stores.
// Production puts it in the result text as JSON; a top-level session_id key
// is accepted too.
func toolCallNamesChildSession(tc session.ToolCall) bool {
	if tc.Result == nil {
		return false
	}
	if sid, ok := tc.Result["session_id"].(string); ok && strings.TrimSpace(sid) != "" {
		return true
	}
	text, _ := tc.Result["text"].(string)
	return strings.Contains(text, `"session_id"`)
}

// isSteeringConsumedMarker is the wake bookmark processSteeredSystemWake and
// writeSteeringConsumedMarker append once a woken turn is certain to run.
// The text is "consumed" plus one message id. Other code reads that line from
// the transcript, so replay leaves the entry in place and simply does not
// emit it. A system line with more than one word after "consumed" is not this
// bookmark.
func isSteeringConsumedMarker(entry session.TranscriptEntry) bool {
	if entry.Type != session.EntryTypeSystem && entry.Role != "system" {
		return false
	}
	rest, ok := strings.CutPrefix(entry.Content, "consumed ")
	if !ok || rest == "" || strings.ContainsAny(rest, " \t\n\r") {
		return false
	}
	return true
}

// delegateCallStillActive reports that the latest generation with a saved
// start is still open. A finished earlier generation does not clear it.
func (sr *streamReplayState) delegateCallStillActive(callID string) bool {
	gen, ok := sr.latestStartedGeneration(callID)
	if !ok {
		return false
	}
	return sr.spanReplayTerminal(callID, gen) == spanReplayOpen
}

// latestStartedGeneration is the highest generation of callID that has a
// saved subagent_start. Generation 1 uses the bare span id.
func (sr *streamReplayState) latestStartedGeneration(callID string) (int, bool) {
	if callID == "" {
		return 0, false
	}
	latest := 0
	if sr.persistedSubagentStartSpans[agent.SubagentSpanID(callID, 1)] {
		latest = 1
	}
	prefix := agent.SubagentSpanID(callID, 1) + "_g"
	for id := range sr.persistedSubagentStartSpans {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		n, err := strconv.Atoi(id[len(prefix):])
		if err != nil || n < 2 {
			continue
		}
		if n > latest {
			latest = n
		}
	}
	return latest, latest > 0
}

// recordedStopMatches reports that the child record still carries a stop
// for this generation. A missing store, a missing child id, or a stop for
// a different generation is not a match.
func (sr *streamReplayState) recordedStopMatches(callID string, generation int) bool {
	rec := sr.lifecycleRecord(sr.childSessionForGeneration(callID, generation))
	return rec != nil && rec.Stop != nil && rec.Stop.Generation == generation
}

// childSessionForGeneration reads the child session id stored on that
// generation's start frame. When this generation's frame omitted it, the
// id is used only if every start for the call names the same child — a
// native revive keeps one session. Disagreeing ids are missing evidence.
func (sr *streamReplayState) childSessionForGeneration(callID string, generation int) string {
	if sr.childSessionBySpan == nil || callID == "" || generation < 1 {
		return ""
	}
	want := agent.SubagentSpanID(callID, generation)
	if id := sr.childSessionBySpan[want]; id != "" {
		return id
	}
	prefix := agent.SubagentSpanID(callID, 1)
	var shared string
	saw := false
	for span, id := range sr.childSessionBySpan {
		if id == "" || !spanBelongsToCall(span, prefix) {
			continue
		}
		if !saw {
			shared = id
			saw = true
			continue
		}
		if id != shared {
			return ""
		}
	}
	if saw {
		return shared
	}
	return ""
}

func spanBelongsToCall(span, gen1Span string) bool {
	if span == gen1Span {
		return true
	}
	prefix := gen1Span + "_g"
	if !strings.HasPrefix(span, prefix) {
		return false
	}
	n, err := strconv.Atoi(span[len(prefix):])
	return err == nil && n >= 2
}

// lifecycleRecord loads the child record once per replay. A missing record
// is cached as no evidence. An unexpected read error is logged and treated
// the same way: replay does not invent a status.
func (sr *streamReplayState) lifecycleRecord(sessionID string) *session.LifecycleRecord {
	if sr.lifecycle == nil || sessionID == "" {
		return nil
	}
	if sr.lifecycleCache == nil {
		sr.lifecycleCache = make(map[string]*session.LifecycleRecord)
	}
	if rec, ok := sr.lifecycleCache[sessionID]; ok {
		return rec
	}
	rec, err := sr.lifecycle.Load(sessionID)
	if err != nil || rec == nil {
		sr.lifecycleCache[sessionID] = nil
		if err != nil && !errors.Is(err, session.ErrLifecycleNotFound) {
			slog.Warn("replay: lifecycle record unreadable — span end will not be guessed",
				"session_id", sr.sessionID, "child_session_id", sessionID, "error", err)
		}
		return nil
	}
	sr.lifecycleCache[sessionID] = rec
	return rec
}

func buildChildSessionBySpan(entries []session.TranscriptEntry) map[string]string {
	out := make(map[string]string)
	for _, entry := range entries {
		if entry.Type != session.EntryTypeSystem || entry.SystemSubtype != session.SystemSubtypeSubagentStart || entry.SubagentStart == nil {
			continue
		}
		start := entry.SubagentStart
		if start.ChildSessionId == nil || *start.ChildSessionId == "" || start.ParentCallId == "" {
			continue
		}
		id := canonicalReplaySpanID(entry.ID, start.SpanId, start.ParentCallId)
		if id == "" {
			continue
		}
		out[id] = *start.ChildSessionId
	}
	return out
}

// emitReplaySpanEnd emits generation 1's synthetic closing frame, or nothing.
// A saved end is emitted later from its own transcript entry.
func (sr *streamReplayState) emitReplaySpanEnd(tc session.ToolCall, term spanReplayTerminal, emitFrame func(any) error) error {
	switch term {
	case spanReplayCancelled:
		sr.buildCancelledSubagentEnd()
		return emitFrame(sr.subEnd)
	case spanReplayUseToolCall:
		if sr.persistedSubagentEndSpans[sr.spanID] {
			return nil
		}
		sr.buildSubagentEnd(tc)
		return emitFrame(sr.subEnd)
	default:
		return nil
	}
}

// buildCancelledSubagentEnd closes an open generation from a recorded stop.
// Duration is left unset: the tool call's duration is the placeholder
// acknowledgement, not this generation's runtime.
func (sr *streamReplayState) buildCancelledSubagentEnd() {
	sr.subEnd = generated.SubagentEndFrame{
		Type:      string(generated.WsFrameTypeSubagentEnd),
		SessionId: sr.sessionID,
		SpanId:    sr.spanID,
		Status:    "cancelled",
	}
	parent := sr.tcID
	if parent != "" {
		sr.subEnd.ParentCallId = &parent
	}
}
