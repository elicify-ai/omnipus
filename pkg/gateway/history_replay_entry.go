package gateway

import (
	"context"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// replayEntry is the one source-entry emitter for full replay and prepared
// pages. The caller supplies the immutable source origin, never an adjacent
// narration's identity. Session-only completion/card frames stay outside it.
func (sr *streamReplayState) replayEntry(ctx context.Context, ei int, entry session.TranscriptEntry, mediaStore media.MediaStore, emit func(any) error) error {
	emitEntry := func(f any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if sr.historyOrigin != nil {
			var err error
			f, err = stampHistoryOrigin(f, sr.historyOrigin)
			if err != nil {
				return err
			}
		}
		return emit(f)
	}
	flow, err := sr.dispatchSpecialEntry(entry, emitEntry)
	if flow == streamReplayStateReturn {
		return err
	}
	if flow == streamReplayStateContinue {
		return nil
	}
	truncatedEmptyAssistant := entry.Content == "" && entry.Truncated && entry.Role == "assistant"
	if entry.Content != "" || truncatedEmptyAssistant {
		if entry.Type == session.EntryTypeSystem && entry.Status == "error" {
			return emitEntry(buildReplayErrorFrame(sr.sessionID, entry))
		}
		sr.buildEntryMessage(entry)
		if err = emitEntry(sr.msgFrame); err != nil {
			return err
		}
	}
	for ti, tc := range entry.ToolCalls {
		if sr.classifyToolCall(ei, entry, ti, tc) == streamReplayStateContinue {
			continue
		}
		flow, err = sr.emitToolCallFrames(tc, mediaStore, sr.buildStartFrame, sr.buildResultFrame, emitEntry)
		if flow == streamReplayStateReturn {
			return err
		}
	}
	return nil
}

// The exhaustive typed switch deliberately fails when a new entry-derived
// frame is added without an ownership decision. Never inject fields into an
// untyped JSON map or stamp session-only output with a guessed source.
func stampHistoryOrigin(frame any, origin *generated.HistoryEntryOrigin) (any, error) {
	switch f := frame.(type) {
	case generated.ReplayMessageFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.ToolCallStartFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.ToolCallResultFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.ReplayErrorFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.ProviderFallbackNote:
		f.HistoryOrigin = origin
		return f, nil
	case generated.ContextWindowNoticeFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.JudgeVerdictFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.BrowserHandoverNoticeFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.HelperEventFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.GoalOutcomeFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.SubagentStartFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.SubagentStateFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.SubagentMessageFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.SubagentEndFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.MediaFrame:
		f.HistoryOrigin = origin
		return f, nil
	case generated.AskUserQuestionFrame:
		f.HistoryOrigin = origin
		return f, nil
	default:
		return nil, fmt.Errorf("unclassified historical frame %T", frame)
	}
}
