package gateway

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

type historyMediaResolution struct {
	path string
	err  error
}
type historyReadOnlyMedia struct {
	media.MediaStore
	paths   map[string]historyMediaResolution
	failure error
}

func (m *historyReadOnlyMedia) ResolveWithOpts(ref string, opts media.ResolveOpts) (string, error) {
	if v, ok := m.paths[ref]; ok {
		return v.path, v.err
	}
	path, err := m.MediaStore.ResolveWithOpts(ref, opts)
	m.paths[ref] = historyMediaResolution{path, err}
	if err != nil {
		m.failure = err
	}
	return path, err
}
func (m *historyReadOnlyMedia) Store(string, media.MediaMeta, string) (string, error) {
	m.failure = errors.New("history preparation cannot publish media")
	return "", m.failure
}
func (m *historyReadOnlyMedia) ReleaseAll(string) error {
	m.failure = errors.New("history preparation cannot release media")
	return m.failure
}

type historyReadOnlyLifecycle struct {
	source  lifecycleRecordLoader
	records map[string]*session.LifecycleRecord
	errs    map[string]error
}

func (l *historyReadOnlyLifecycle) Load(id string) (*session.LifecycleRecord, error) {
	if rec, ok := l.records[id]; ok {
		return rec, l.errs[id]
	}
	rec, err := l.source.Load(id)
	l.records[id] = rec
	l.errs[id] = err
	return rec, err
}

// A prepared page never invokes the durable offloader. Already persisted result
// references are checked read-only. Otherwise the admitted canonical value is
// prepared inline, using the existing 1-MiB preview protection for WS. REST
// keeps the whole owning public group, so this is not a smaller size estimate.
func (r *historyPageRequest) prepareResult(ctx context.Context, tc session.ToolCall) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tc.Result == nil {
		return tc.Result, nil
	}
	if isRef, ok := tc.Result["_ref"].(bool); ok && isRef {
		ref, ok := tc.Result["ref"].(string)
		if !ok || ref == "" {
			return nil, errors.New("invalid persisted result reference")
		}
		if r.deps.ToolStore == nil {
			return nil, errors.New("result reference store unavailable")
		}
		if _, err := r.deps.ToolStore.readByRef(r.sessionID, ref); err != nil {
			return nil, err
		}
		return tc.Result, nil
	}
	raw, err := json.Marshal(tc.Result)
	if err != nil {
		return nil, err
	}
	if len(raw) <= replayMaxResultBytes {
		return tc.Result, nil
	}
	return generated.TruncatedResult{Truncated: true, OriginalSizeBytes: len(raw), Preview: string(raw[:min(len(raw), replayResultPreviewBytes)])}, nil
}
func historyWireMessage(sid string, e session.ChatHistoryEntry) (generated.Message, []byte, error) {
	var message generated.Message
	// This existing verdict adapter is the same one the unpaged REST path used.
	// Decoding into the generated projection drops every private archive member.
	raw, err := json.Marshal(withWireJudgeVerdict(sid, e.Entry))
	if err != nil {
		return message, nil, err
	}
	if err = json.Unmarshal(raw, &message); err != nil {
		return message, nil, err
	}
	position := string(e.Position)
	message.HistoryPosition = &position
	raw, err = json.Marshal(message)
	if err != nil {
		return message, nil, err
	}
	if msg, serverErr := validateBodyAgainstSchema("Message", raw); msg != "" {
		if serverErr {
			return message, nil, errors.New("message projection schema unavailable")
		}
		// A bad individual public item keeps its source position and recorded ID.
		// It never gets a new persisted identity or replaces valid neighbors.
		message = generated.Message{Id: e.Entry.ID, AgentId: e.Entry.AgentID, Timestamp: e.Entry.Timestamp, HistoryPosition: &position}
		text := "This saved message could not be displayed."
		message.Content = &text
		role := generated.MessageRoleSystem
		message.Role = &role
		raw, err = json.Marshal(message)
	}
	return message, raw, err
}
func (r *historyPageRequest) prepareGroup(ctx context.Context, p session.ChatHistoryPage, e session.ChatHistoryEntry) (historyPreparedGroup, error) {
	var out historyPreparedGroup
	message, encoded, err := historyWireMessage(r.sessionID, e)
	if err != nil {
		return out, err
	}
	out.message = message
	out.encoded = encoded
	out.frames = [][]byte{}
	out.emitted = map[string]bool{}
	// Actual bounded context informs the shared emitter without adding its facts
	// as extra chronological source groups or replaying a child's transcript.
	entries := []session.TranscriptEntry{e.Entry}
	seen := map[session.HistoryPosition]bool{e.Position: true}
	for _, span := range p.PageInfo.Context.Spans {
		for _, fact := range []*session.ChatHistoryEntry{span.Start, span.State, span.Message, span.End} {
			if fact != nil && !seen[fact.Position] {
				seen[fact.Position] = true
				entries = append(entries, fact.Entry)
			}
		}
	}
	sr := &streamReplayState{sessionID: r.sessionID, entries: entries, toolStore: r.deps.ToolStore, terminalAsk: r.deps.TerminalAsk, preparedResults: map[session.ToolCallID]any{}}
	if r.lifecycle != nil {
		sr.lifecycle = r.lifecycle
	}
	sr.prepareReplay()
	for _, d := range p.PageInfo.Context.DuplicateIDs {
		if d.Kind == "tool" && d.CanonicalPosition != e.Position {
			sr.latestByID[d.ID] = tcAddr{entryIdx: -1, tcIdx: -1}
		}
	}
	position := string(e.Position)
	origin := generated.HistoryEntryOrigin{Position: position}
	if e.Entry.ID != "" {
		id := e.Entry.ID
		origin.EntryId = &id
	}
	sr.historyOrigin = &origin
	for _, tc := range e.Entry.ToolCalls {
		result, resultErr := r.prepareResult(ctx, tc)
		if resultErr != nil {
			return out, resultErr
		}
		sr.preparedResults[tc.ID] = result
	}
	var mediaStore media.MediaStore
	if r.media != nil {
		mediaStore = r.media
	}
	err = sr.replayEntry(ctx, 0, e.Entry, mediaStore, func(frame any) error {
		raw, frameErr := json.Marshal(frame)
		if frameErr != nil {
			return frameErr
		}
		if frameErr = validateHistorySourceFrame(raw); frameErr != nil {
			return frameErr
		}
		recordEmittedIDs(out.emitted, frame)
		out.frames = append(out.frames, raw)
		return nil
	})
	if err != nil {
		return out, err
	}
	if r.media != nil && r.media.failure != nil {
		return out, r.media.failure
	}
	out.terminalAskEmitted = sr.terminalAskEmitted
	return out, nil
}

func validateHistorySourceFrame(raw []byte) error {
	var kind wsTypeOnly
	if err := json.Unmarshal(raw, &kind); err != nil {
		return err
	}
	names := map[string]string{"replay_message": "ReplayMessageFrame", "tool_call_start": "ToolCallStartFrame", "tool_call_result": "ToolCallResultFrame", "replay_error": "ReplayErrorFrame", "replay_provider_fallback": "ProviderFallbackNote", "context_window_notice": "ContextWindowNoticeFrame", "judge_verdict": "JudgeVerdictFrame", "browser_handover_notice": "BrowserHandoverNoticeFrame", "helper_event": "HelperEventFrame", "goal_outcome": "GoalOutcomeFrame", "subagent_start": "SubagentStartFrame", "subagent_state": "SubagentStateFrame", "subagent_message": "SubagentMessageFrame", "subagent_end": "SubagentEndFrame", "media": "MediaFrame", "ask_user_question": "AskUserQuestionFrame"}
	schema := names[kind.Type]
	if schema == "" {
		return errors.New("unclassified historical frame")
	}
	// MediaFrame is an inline AsyncAPI component rather than a standalone
	// inbound-schema file. Its generated type and typed ownership are checked
	// here; every standalone generated carrier is also schema-validated.
	if schema == "MediaFrame" {
		var f generated.MediaFrame
		if err := json.Unmarshal(raw, &f); err != nil {
			return err
		}
		if f.HistoryOrigin == nil || len(f.Parts) == 0 {
			return errors.New("invalid prepared media")
		}
		return nil
	}
	if msg, _ := validateBodyAgainstSchema(schema, raw); msg != "" {
		return errors.New("invalid prepared historical frame")
	}
	return nil
}
