// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"fmt"
	"strings"
)

// Answer limits (#1449 D24, D27, TD-3). The group text limit in force is
// min(steer_body, 65,536) and is applied by the group itself; the tool
// refuses only what can never be posted.
const (
	answerMaxTextBytes = 65536
	answerMaxMedia     = 16
)

// AnswerRequestTool is how an agent answers a request it was sent (a peer's
// ask, a connector message in its main). Answering is an explicit tool call so
// the contract is unambiguous: a plain reply in the conversation reaches
// nobody. It is a thin front for the SAME reply admission send_message's
// reply_to form uses (MessageTool.executeReplyForm), so there is exactly one
// reply path, one authorization and one round-send tracker.
type AnswerRequestTool struct {
	msg   *MessageTool
	files AnswerFileResolver
}

// AnswerFileResolver resolves an answer file the agent's own chat could
// already send (a path in its reach, or a media reference of its own chat),
// with the same file-access checks send_file applies (#1449 FR-020).
type AnswerFileResolver interface {
	ResolveAnswerFile(ctx context.Context, source string) (ReplyMedia, error)
}

// NewAnswerRequestTool returns the tool over msg's reply seam. msg must be the
// agent's own registered MessageTool so the accepted reply counts as that
// round's send (no second, automatic reply from the same turn).
func NewAnswerRequestTool(msg *MessageTool) *AnswerRequestTool {
	return &AnswerRequestTool{msg: msg}
}

// SetFileResolver installs the resolver for answer files that are not
// workspace references. Nil leaves only workspace references usable.
func (t *AnswerRequestTool) SetFileResolver(r AnswerFileResolver) { t.files = r }

func (t *AnswerRequestTool) Name() string { return "answer_request" }

func (t *AnswerRequestTool) Description() string {
	return "Answer a request you were sent. Use the request id shown in the request. " +
		"A plain reply in this conversation does not reach the sender. " +
		"An answer to a group chat request can carry up to 16 files in media."
}

func (t *AnswerRequestTool) Scope() ToolScope       { return ScopeGeneral }
func (t *AnswerRequestTool) Category() ToolCategory { return CategoryCommunication }

func (t *AnswerRequestTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"request_id": map[string]any{
				"type":        "string",
				"description": "The id of the request you are answering.",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Your answer.",
			},
			"media": map[string]any{
				"type":     "array",
				"maxItems": answerMaxMedia,
				"items":    map[string]any{"type": "string"},
				"description": "Optional files for an answer to a group chat request, at most 16: a " +
					"media://workspace/<this workspace>/<id> reference, or a file path or media reference " +
					"you could send to your own chat. The files appear with your answer in the group and in your chat.",
			},
		},
		"required": []string{"request_id", "text"},
	}
}

func (t *AnswerRequestTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	id, idOK := args["request_id"].(string)
	text, textOK := args["text"].(string)
	if !idOK || !textOK {
		return &ToolResult{ForLLM: "request_id and text are required strings", IsError: true}
	}
	if len(text) > answerMaxTextBytes {
		return addrRefuseWith(ErrReplyForm, "your answer is %d bytes; an answer can be at most %d bytes. "+
			"Shorten it and call answer_request again; nothing was sent", len(text), answerMaxTextBytes)
	}
	sources, err := answerMediaArg(args)
	if err != nil {
		return addrRefuseWith(ErrReplyForm, "%v; nothing was sent", err)
	}
	if t.msg == nil {
		return addrRefuseWith(ErrReplyForm, "answering a request is not configured here; nothing was sent")
	}
	if denied := t.msg.denySteeredAddressing(ctx); denied != nil {
		return denied
	}
	media, refused := t.resolveMedia(ctx, sources)
	if refused != nil {
		return refused
	}
	res := t.msg.executeReplyFormWithMedia(ctx, text, id, false, false, media)
	if !res.IsError && !strings.Contains(res.ForLLM, "accepted:") {
		res.ForLLM = fmt.Sprintf("Answer to request %s accepted for sending.", id)
	}
	return res
}

// answerMediaArg reads the optional media array (at most 16 strings).
func answerMediaArg(args map[string]any) ([]string, error) {
	raw, present := args["media"]
	if !present || raw == nil {
		return nil, nil
	}
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case []string:
		for _, s := range v {
			items = append(items, s)
		}
	default:
		return nil, fmt.Errorf("media must be an array of strings")
	}
	if len(items) > answerMaxMedia {
		return nil, fmt.Errorf("an answer carries at most %d files in media; this one has %d", answerMaxMedia, len(items))
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok || strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("media entry %d must be a non-empty string", i+1)
		}
		out = append(out, strings.TrimSpace(s))
	}
	return out, nil
}

// resolveMedia turns each media entry into a ReplyMedia. A workspace
// reference must name this turn's workspace; anything else goes through the
// agent's own file access (send_file's checks). Any failure refuses the
// whole answer before anything is passed on (S-31, S-34).
func (t *AnswerRequestTool) resolveMedia(ctx context.Context, sources []string) ([]ReplyMedia, *ToolResult) {
	if len(sources) == 0 {
		return nil, nil
	}
	ws := ToolWorkspaceID(ctx)
	out := make([]ReplyMedia, 0, len(sources))
	for i, src := range sources {
		n := i + 1
		if rest, ok := strings.CutPrefix(src, "media://workspace/"); ok {
			refWS, _, _ := strings.Cut(rest, "/")
			if ws == "" || refWS != ws {
				return nil, addrRefuseWith(ErrReplyForm, "media entry %d is a file of another workspace; only files of "+
					"this workspace can be attached. Nothing was sent", n)
			}
			out = append(out, ReplyMedia{Ref: src, Identity: src})
			continue
		}
		if t.files == nil {
			return nil, addrRefuseWith(ErrReplyForm, "media entry %d cannot be attached here: only "+
				"media://workspace/%s/<id> references are accepted. Nothing was sent", n, ws)
		}
		m, err := t.files.ResolveAnswerFile(ctx, src)
		if err != nil {
			return nil, addrRefuseWith(ErrReplyForm, "media entry %d (%s) cannot be attached: %v. Nothing was sent", n, src, err)
		}
		out = append(out, m)
	}
	return out, nil
}
