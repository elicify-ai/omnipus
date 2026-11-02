// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// set_channel_outbound (channels-spec-r8 FR-030–FR-035, FD-10, FD-11, FD-14):
// the only agent path that changes a connection's starting-messages setting.
// It calls the one gateway writer (applyOutboundChange, shared with PUT
// /api/v1/channels/{id}/outbound) through Deps.ApplyChannelOutbound; it never
// writes configuration itself.

// Request-shape bounds, the same as the REST request schema
// (ChannelOutboundUpdateRequest: at most 64 raw entries of at most 256
// characters). The stored-list limit of 20 distinct targets is the writer's.
const (
	outboundToolMaxRawTargets   = 64
	outboundToolMaxRawTargetLen = 256
)

// Refusal texts of FR-031 and the agent-tool parity table.
const (
	outboundOmnipusOnlyText = "only Omnipus can change a connection's posting list; ask the operator or Omnipus"
	outboundOwnConnection   = "a connection's own agent cannot change its posting list; ask the operator"
)

// ChannelOutboundChange is one set_channel_outbound request handed to the
// writer. Targets is the whole list, raw (the writer canonicalizes it).
type ChannelOutboundChange struct {
	ID               string
	Policy           string
	Targets          []string
	ExpectedRevision string
	// Actor names the caller for the writer's logs; it grants nothing.
	Actor string
}

// ChannelOutboundSaved is the writer's success: the stored outbound value,
// the channel's new FR-088 revision and, in the T-6b saved-but-not-refreshed
// case, the warning text.
type ChannelOutboundSaved struct {
	Policy   string
	Targets  []string
	Revision string
	Warning  string
}

// ChannelOutboundRefusal is the writer's refusal before anything was
// written. Reason is the writer's rejection code (ChannelOutboundRejection
// code: stale_revision, saved_live_mismatch, invalid_targets,
// too_many_targets, platform_cannot_start, platform_cannot_message_people,
// connect_first, lookup_unavailable); Message its safe text; TargetErrors and
// OmittedCount its per-target detail when present.
type ChannelOutboundRefusal struct {
	Reason       string
	Message      string
	TargetErrors any
	OmittedCount int
}

func (e *ChannelOutboundRefusal) Error() string { return e.Reason + ": " + e.Message }

// ErrChannelOutboundNotConfigured is the writer's answer for an instance with
// no configuration entry.
var ErrChannelOutboundNotConfigured = errors.New("channel is not configured")

// outboundReasonCodes maps the writer's rejection reasons to the tool's
// FR-030 error codes (r9, Q7-Q2 A: too_many_targets and
// platform_cannot_message_people translate to existing codes; no new codes).
var outboundReasonCodes = map[string]string{
	"stale_revision":                 "CONFLICT",
	"saved_live_mismatch":            "CONFLICT",
	"invalid_targets":                "INVALID_TARGETS",
	"too_many_targets":               "INVALID_TARGETS",
	"platform_cannot_start":          "PLATFORM_CANNOT_START",
	"platform_cannot_message_people": "PLATFORM_CANNOT_START",
	"connect_first":                  "CONNECT_FIRST",
	"lookup_unavailable":             "LOOKUP_UNAVAILABLE",
}

// ChannelOutboundTool is set_channel_outbound.
type ChannelOutboundTool struct {
	deps *Deps
}

func NewChannelOutboundTool(d *Deps) *ChannelOutboundTool { return &ChannelOutboundTool{deps: d} }

func (t *ChannelOutboundTool) Name() string                 { return "set_channel_outbound" }
func (t *ChannelOutboundTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ChannelOutboundTool) Category() tools.ToolCategory { return tools.CategoryChannels }
func (t *ChannelOutboundTool) ReportsChangeOutcome()        {}

// Description is the normative FR-035 text.
func (t *ChannelOutboundTool) Description() string {
	return "Change which people and groups an agent may start messages to on one chat connection, or switch the connection back to only answering. Supply id, policy (reply_only = Only answer; agent_may_start = Answer and also start messages), the whole targets list (exact ids as the allowed-senders list writes them: a person's user id for a direct message, a group's chat id; no @names, no *; at most 20) and expected_revision from list_channels. Only Discord and Telegram can start messages; on Discord, a channel id must be a text channel, announcement channel or thread. Refused for a connection bound to you. Only Omnipus holds this tool. It always shows an approval card, even in God Mode, and every approval applies to that one request only — it is never remembered."
}

func (t *ChannelOutboundTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{"type": "string", "description": "Channel instance key, as list_channels reports it"},
			"policy": map[string]any{
				"type": "string", "enum": []string{"reply_only", "agent_may_start"},
				"description": "reply_only = Only answer; agent_may_start = Answer and also start messages",
			},
			"targets": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
				"description": "The whole list of people and groups the agent may start messages to; replaces the stored list",
			},
			expectedRevisionParam: channelRevisionProperty(),
		},
		"required":             []string{"id", "policy", "targets", expectedRevisionParam},
		"additionalProperties": false,
	}
}

func (t *ChannelOutboundTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	// FR-031 (r9 Q7-Q1 A): authority first, before any argument or
	// connection is looked at.
	caller := tools.ToolAgentID(ctx)
	if caller != string(coreagent.IDOmnipus) {
		return tools.ErrorResult(errorJSON("OMNIPUS_ONLY", outboundOmnipusOnlyText, ""))
	}
	change, invalid := outboundChangeArgs(args)
	if invalid != "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", invalid+"; nothing was saved", "Supply id, policy, the whole targets list and expected_revision from list_channels"))
	}
	change.Actor = caller
	if inst, ok := t.deps.GetCfg().Channels[change.ID]; ok && inst.Identity != nil &&
		strings.TrimSpace(inst.Identity.ID) == caller {
		return tools.ErrorResult(errorJSON("OWN_CONNECTION", outboundOwnConnection, ""))
	}
	if t.deps.ApplyChannelOutbound == nil {
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED", "the outbound writer is not wired; nothing was saved", ""))
	}
	saved, err := t.deps.ApplyChannelOutbound(ctx, change)
	if err != nil {
		return outboundErrorResult(change.ID, err)
	}
	fields := map[string]any{
		"status":   "saved",
		"outbound": map[string]any{"policy": saved.Policy, "targets": append([]string{}, saved.Targets...)},
		"revision": saved.Revision,
	}
	outcome := tools.ChangeWorking("the setting was saved and is in force for starting messages",
		"no message was sent; delivery to the listed targets is not tested")
	if saved.Warning != "" {
		fields["warning"] = saved.Warning
		outcome.NotTested = append(outcome.NotTested, "other settings of this connection were not refreshed")
	}
	return tools.ChangeSuccessResult(fields, outcome)
}

// outboundChangeArgs validates the request shape (FR-030: every shape error
// is INVALID_INPUT). It returns the reason text when invalid.
func outboundChangeArgs(args map[string]any) (ChannelOutboundChange, string) {
	var change ChannelOutboundChange
	for name := range args {
		switch name {
		case "id", "policy", "targets", expectedRevisionParam:
		default:
			return change, fmt.Sprintf("unknown parameter %q", name)
		}
	}
	var ok bool
	if change.ID, ok = args["id"].(string); !ok || strings.TrimSpace(change.ID) == "" {
		return change, "id is required and must be a non-empty string"
	}
	change.ID = strings.TrimSpace(change.ID)
	if change.Policy, ok = args["policy"].(string); !ok || (change.Policy != "reply_only" && change.Policy != "agent_may_start") {
		return change, "policy must be reply_only or agent_may_start"
	}
	if change.ExpectedRevision, ok = args[expectedRevisionParam].(string); !ok || strings.TrimSpace(change.ExpectedRevision) == "" {
		return change, expectedRevisionParam + " is required; read it with list_channels"
	}
	change.ExpectedRevision = strings.TrimSpace(change.ExpectedRevision)
	targets, reason := outboundTargetsArg(args["targets"])
	if reason != "" {
		return change, reason
	}
	change.Targets = targets
	return change, ""
}

func outboundTargetsArg(raw any) ([]string, string) {
	var items []any
	switch v := raw.(type) {
	case []any:
		items = v
	case []string:
		items = make([]any, len(v))
		for i, s := range v {
			items[i] = s
		}
	default:
		return nil, "targets is required and must be an array of strings (use [] for none)"
	}
	if len(items) > outboundToolMaxRawTargets {
		return nil, fmt.Sprintf("targets: at most %d entries", outboundToolMaxRawTargets)
	}
	targets := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			return nil, "targets must contain only strings"
		}
		if len([]rune(s)) > outboundToolMaxRawTargetLen {
			return nil, fmt.Sprintf("targets: an entry is longer than %d characters", outboundToolMaxRawTargetLen)
		}
		targets = append(targets, s)
	}
	return targets, ""
}

// outboundErrorResult renders a writer error with its FR-030 code. The
// writer's own reason is kept in the detail (r9 Q7-Q2 A), so the two CONFLICT
// causes and the translated codes stay distinguishable.
func outboundErrorResult(id string, err error) *tools.ToolResult {
	var refusal *ChannelOutboundRefusal
	switch {
	case errors.As(err, &refusal):
		code, known := outboundReasonCodes[refusal.Reason]
		if !known {
			// A writer reason this table does not know is still a refusal
			// before the write; never present it as a save failure.
			code = "INVALID_INPUT"
		}
		detail := map[string]any{"code": code, "message": refusal.Message, "reason": refusal.Reason}
		if refusal.TargetErrors != nil {
			detail["target_errors"] = refusal.TargetErrors
		}
		if refusal.OmittedCount > 0 {
			detail["omitted_count"] = refusal.OmittedCount
		}
		if code == "CONFLICT" {
			detail["suggestion"] = "Read the channel again with list_channels, revise the plan and confirm it again before retrying"
		}
		return tools.ErrorResult(outboundJSON(map[string]any{"success": false, "error": detail}))
	case errors.Is(err, ErrChannelOutboundNotConfigured):
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND",
			fmt.Sprintf("channel %q is not configured; nothing was saved", id), "Read list_channels for valid ids"))
	default:
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED",
			"could not save the setting; nothing was changed: "+err.Error(), ""))
	}
}

func outboundJSON(m map[string]any) string {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return errorJSON("ENCODE_FAILED", "the tool result could not be encoded", "")
	}
	return string(b)
}
