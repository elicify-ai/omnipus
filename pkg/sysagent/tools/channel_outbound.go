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

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// set_channel_outbound (channels-spec-r8 FR-030–FR-035, FD-10, FD-11, FD-14):
// the only agent path that changes a connection's starting-messages setting.
// It calls the one gateway writer (applyOutboundChange, shared with PUT
// /api/v1/channels/{id}/outbound) through Deps.ApplyChannelOutbound; it never
// writes configuration itself.
//
// Channels Q2-B (founder: "The tool must look into it, not the agent
// itself"): before the approval card, PrepareForApproval reads the current
// setting through Deps.PreviewChannelOutbound, runs every validity check the
// writer runs, shows the person current -> new and pins the revision it read.
// The pin rides the execution context; the model's arguments are never
// rewritten. expected_revision is optional.

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
	// outboundNoApprovedRevision is the fail-closed refusal of an execution
	// that carries neither an approval pin nor expected_revision (Q2-B §2).
	outboundNoApprovedRevision = "no approved revision; nothing was saved"
	// outboundChangedAfterCard is the only "approved, then refused" result
	// caused by settings state (Q2-B §2): another writer changed this
	// connection between the card and the click.
	outboundChangedAfterCard = "This connection's settings changed after the card was shown; nothing was saved. " +
		"Make the call again to see a new card with the current settings."
)

// Audit reasons of the tool's own refusals before the card (Q2-B §3).
const (
	outboundReasonOmnipusOnly   = "omnipus_only"
	outboundReasonInvalidInput  = "invalid_input"
	outboundReasonOwnConnection = "own_connection"
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
	// Pin is the state the person approved (Channels Q2-B), set by Execute
	// only when the call runs under the pin of its own approval card and
	// ExpectedRevision is the pinned revision. With the revision unchanged
	// the writer then skips the second Discord look-ups (founder Q1 B). Nil
	// for every other call: the writer re-checks everything.
	Pin *ChannelOutboundPin
}

// ChannelOutboundPin is the server-side pin PrepareForApproval returns: the
// revision the card was built from and the canonical Discord targets that
// were looked up and confirmed before the card. It is carried on the
// execution context (tools.WithApprovalPin), never in the arguments, and is
// never sent to the approver's client or to the agent.
type ChannelOutboundPin struct {
	Revision        string
	VerifiedTargets []string
}

// ChannelOutboundPreview is the read-only result of the writer's checks
// before the approval card (Deps.PreviewChannelOutbound): the current
// setting, the revision it was read at, the canonical new setting exactly as
// the writer would store it, the targets it adds and removes, and the
// Discord targets that were looked up and confirmed.
type ChannelOutboundPreview struct {
	Platform        string
	CurrentPolicy   string
	CurrentTargets  []string
	Revision        string
	NewPolicy       string
	NewTargets      []string
	Added           []string
	Removed         []string
	VerifiedTargets []string
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

// ChannelOutboundRefusalError is the writer's refusal before anything was
// written. Reason is the writer's rejection code (ChannelOutboundRejection
// code: stale_revision, saved_live_mismatch, invalid_targets,
// too_many_targets, platform_cannot_start, platform_cannot_message_people,
// connect_first, lookup_unavailable); Message its safe text; TargetErrors and
// OmittedCount its per-target detail when present.
type ChannelOutboundRefusalError struct {
	Reason       string
	Message      string
	TargetErrors any
	OmittedCount int
}

func (e *ChannelOutboundRefusalError) Error() string { return e.Reason + ": " + e.Message }

// ErrChannelOutboundNotConfigured is the writer's answer for an instance with
// no configuration entry.
var ErrChannelOutboundNotConfigured = errors.New("channel is not configured")

// ErrChannelOutboundPreviewFailed is the preview's answer when it could not
// run its checks (for example the saved settings could not be read; Q2-B
// A2.5). The cause is logged and audited by the gateway; the agent only gets
// outboundPreviewFailedText, never an internal path or error.
var ErrChannelOutboundPreviewFailed = errors.New("the outbound preview could not run")

// outboundPreviewFailedText is the fixed agent-facing text of
// ErrChannelOutboundPreviewFailed (Q2-B A2.5).
const outboundPreviewFailedText = "could not save; nothing was changed"

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

// ChannelOutboundToolName is set_channel_outbound's registered name.
const ChannelOutboundToolName = "set_channel_outbound"

// ChannelOutboundTool is set_channel_outbound.
type ChannelOutboundTool struct {
	deps *Deps
}

func NewChannelOutboundTool(d *Deps) *ChannelOutboundTool { return &ChannelOutboundTool{deps: d} }

func (t *ChannelOutboundTool) Name() string                 { return ChannelOutboundToolName }
func (t *ChannelOutboundTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ChannelOutboundTool) Category() tools.ToolCategory { return tools.CategoryChannels }
func (t *ChannelOutboundTool) ReportsChangeOutcome()        {}

// Description is the normative FR-035 text, as revised by Channels Q2-B
// (expected_revision is optional; the tool reads the settings itself).
func (t *ChannelOutboundTool) Description() string {
	return "Change which people and groups an agent may start messages to on one chat connection, or switch the connection back to only answering. Supply id, policy (reply_only = Only answer; agent_may_start = Answer and also start messages) and the whole targets list (exact ids as the allowed-senders list writes them: a person's user id for a direct message, a group's chat id; no @names, no *; at most 20). expected_revision is optional: leave it out, the tool reads the current settings itself and shows the person the current and the new list on the approval card. Only Discord and Telegram can start messages; on Discord, a channel id must be a text channel, announcement channel or thread. Refused for a connection bound to you. It always shows an approval card, even in God Mode, and every approval applies to that one request only — it is never remembered."
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
			expectedRevisionParam: map[string]any{
				"type":        "string",
				"description": "Leave it out; the tool reads the current settings itself. Pass it only if you read it with list_channels.",
			},
		},
		"required":             []string{"id", "policy", "targets"},
		"additionalProperties": false,
	}
}

func (t *ChannelOutboundTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	change, refusal, _ := t.admit(ctx, args)
	if refusal != nil {
		return refusal
	}
	// Q2-B §2: the revision is the supplied one, else the pin of this call's
	// own approval card. Execute never reads the current revision as a
	// substitute: a path that skipped the card gets nothing written.
	pin, _ := tools.ApprovalPinFrom(ctx).(*ChannelOutboundPin)
	if change.ExpectedRevision == "" {
		if pin == nil || pin.Revision == "" {
			return tools.ErrorResult(errorJSON("INVALID_INPUT", outboundNoApprovedRevision, ""))
		}
		change.ExpectedRevision = pin.Revision
	}
	if pin != nil && pin.Revision == change.ExpectedRevision {
		change.Pin = pin
	}
	if t.deps.ApplyChannelOutbound == nil {
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED", "the outbound writer is not wired; nothing was saved", ""))
	}
	saved, err := t.deps.ApplyChannelOutbound(ctx, change)
	if err != nil {
		return outboundErrorResult(change.ID, err, change.Pin != nil)
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

// PrepareForApproval implements tools.ApprovalPreparer (Channels Q2-B): the
// tool, not the agent, reads the current setting. One read through
// Deps.PreviewChannelOutbound runs every check the writer runs (shape,
// own connection, configured connection, revision when supplied, targets,
// platform, connection running, Discord look-ups, saved/live
// correspondence), yields the card facts and the pin. A refusal is the
// call's final result, audited as refused_before_approval, and no card is
// shown. The facts reach the approval request only: never the executed
// arguments, the transcript or the tool result.
func (t *ChannelOutboundTool) PrepareForApproval(ctx context.Context, args map[string]any) (map[string]any, any, *tools.ToolResult) {
	change, refusal, reason := t.admit(ctx, args)
	if refusal != nil {
		if t.deps.AuditChannelOutboundRefusal != nil {
			t.deps.AuditChannelOutboundRefusal(ctx, change, reason)
		}
		return nil, nil, refusal
	}
	if t.deps.PreviewChannelOutbound == nil {
		return nil, nil, finalizeChangeResult(t.Name(), tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED",
			"the outbound preview is not wired; nothing was saved", "")))
	}
	preview, err := t.deps.PreviewChannelOutbound(ctx, change)
	if err != nil {
		return nil, nil, finalizeChangeResult(t.Name(), outboundErrorResult(change.ID, err, false))
	}
	facts := map[string]any{
		"connection":       map[string]any{"id": change.ID, "platform": preview.Platform},
		"current_outbound": map[string]any{"policy": preview.CurrentPolicy, "targets": nonNilStrings(preview.CurrentTargets)},
		"new_outbound":     map[string]any{"policy": preview.NewPolicy, "targets": nonNilStrings(preview.NewTargets)},
		"added":            nonNilStrings(preview.Added),
		"removed":          nonNilStrings(preview.Removed),
		"revision":         preview.Revision,
	}
	pin := &ChannelOutboundPin{Revision: preview.Revision, VerifiedTargets: nonNilStrings(preview.VerifiedTargets)}
	return facts, pin, nil
}

// outboundCardFactKeys are the approval-card facts PrepareForApproval adds
// next to the model's arguments (Q2-B section 4).
var outboundCardFactKeys = []string{"connection", "current_outbound", "new_outbound", "added", "removed", "revision"}

// ChannelOutboundAskAuditArgs projects an approval request of this tool for
// the ask audit preview (Q2-B A2.1, R-S3): the model's own arguments (the
// preview summarizes their target list to a count) plus only the readable
// non-target card facts — connection_id, platform, current_policy,
// new_policy, added_count, removed_count and revision. The card's target
// lists never reach an audit row. The card itself (requestArgs) is unchanged.
func ChannelOutboundAskAuditArgs(requestArgs map[string]any) map[string]any {
	out := make(map[string]any, len(requestArgs)+3)
	for k, v := range requestArgs {
		out[k] = v
	}
	for _, k := range outboundCardFactKeys {
		delete(out, k)
	}
	if conn, ok := requestArgs["connection"].(map[string]any); ok {
		if id, ok := conn["id"].(string); ok {
			out["connection_id"] = id
		}
		if platform, ok := conn["platform"].(string); ok {
			out["platform"] = platform
		}
	}
	for fact, key := range map[string]string{"current_outbound": "current_policy", "new_outbound": "new_policy"} {
		if outbound, ok := requestArgs[fact].(map[string]any); ok {
			if policy, ok := outbound["policy"].(string); ok {
				out[key] = policy
			}
		}
	}
	for fact, key := range map[string]string{"added": "added_count", "removed": "removed_count"} {
		if n, ok := listLen(requestArgs[fact]); ok {
			out[key] = n
		}
	}
	if revision, ok := requestArgs["revision"].(string); ok {
		out["revision"] = revision
	}
	return out
}

// listLen is the length of a card fact list, whether in-process ([]string)
// or decoded from JSON ([]any).
func listLen(v any) (int, bool) {
	switch list := v.(type) {
	case []string:
		return len(list), true
	case []any:
		return len(list), true
	}
	return 0, false
}

// admit applies the checks that need no writer: the caller's identity, the
// request shape (FR-030) and the own-connection refusal (FD-11). Founder Q4
// (2026-11-03) lets every identified chat-capable agent use this tool with a
// fresh approval; an unidentified caller keeps the OMNIPUS_ONLY refusal. On a
// refusal, reason is its audit reason and change carries whatever was
// readable (the id and the caller) for that audit record.
func (t *ChannelOutboundTool) admit(ctx context.Context, args map[string]any) (ChannelOutboundChange, *tools.ToolResult, string) {
	caller := strings.TrimSpace(tools.ToolAgentID(ctx))
	if caller == "" {
		var change ChannelOutboundChange
		if id, ok := args["id"].(string); ok {
			change.ID = strings.TrimSpace(id)
		}
		return change, finalizeChangeResult(t.Name(), tools.ErrorResult(errorJSON("OMNIPUS_ONLY", outboundOmnipusOnlyText, ""))),
			outboundReasonOmnipusOnly
	}
	change, invalid := outboundChangeArgs(args)
	change.Actor = caller
	if invalid != "" {
		return change, finalizeChangeResult(t.Name(), tools.ErrorResult(errorJSON("INVALID_INPUT", invalid+"; nothing was saved",
			"Supply id, policy and the whole targets list"))), outboundReasonInvalidInput
	}
	if inst, ok := t.deps.GetCfg().Channels[change.ID]; ok && inst.Identity != nil &&
		strings.TrimSpace(inst.Identity.ID) == caller {
		return change, finalizeChangeResult(t.Name(), tools.ErrorResult(errorJSON("OWN_CONNECTION", outboundOwnConnection, ""))),
			outboundReasonOwnConnection
	}
	return change, nil, ""
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
	// Q2-B §1: optional. Omitted, empty or blank means "use the revision the
	// tool read for the card"; a supplied value is honoured.
	if raw, present := args[expectedRevisionParam]; present {
		revision, isString := raw.(string)
		if !isString {
			return change, expectedRevisionParam + " must be a string"
		}
		change.ExpectedRevision = strings.TrimSpace(revision)
	}
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
//
// afterCard is true when the call runs under its own approval card's pin: a
// stale_revision is then the one legitimate "approved, then refused" case
// and says so (Q2-B §2).
func outboundErrorResult(id string, err error, afterCard bool) *tools.ToolResult {
	var refusal *ChannelOutboundRefusalError
	switch {
	case errors.As(err, &refusal):
		code, known := outboundReasonCodes[refusal.Reason]
		if !known {
			// A writer reason this table does not know is still a refusal
			// before the write; never present it as a save failure.
			code = "INVALID_INPUT"
		}
		message := refusal.Message
		if afterCard && refusal.Reason == "stale_revision" {
			message = outboundChangedAfterCard
		}
		detail := map[string]any{"code": code, "message": message, "reason": refusal.Reason}
		if refusal.TargetErrors != nil {
			detail["target_errors"] = refusal.TargetErrors
		}
		if refusal.OmittedCount > 0 {
			detail["omitted_count"] = refusal.OmittedCount
		}
		if refusal.Reason == "stale_revision" {
			// Q2-B §2: a chat agent may not read channels; it never needs to.
			detail["suggestion"] = "Make the call again; the tool reads the current settings itself and shows a new card"
		}
		return tools.ErrorResult(outboundJSON(map[string]any{"success": false, "error": detail}))
	case errors.Is(err, ErrChannelOutboundPreviewFailed):
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED", outboundPreviewFailedText, ""))
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
