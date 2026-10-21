// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
	workspacepkg "github.com/elicify-ai/omnipus/pkg/workspace"
)

// ChannelConnectTool connects a channel in one approved call (FR-080,
// US-8): save its configuration with stored references, bind its main agent
// in a workspace, enable it and live-test it. The result separates the facts
// a real platform start proved from everything not tested (BDD-8.1–8.3).
type ChannelConnectTool struct {
	deps *Deps
	secretSelectionAudit
}

func NewChannelConnectTool(d *Deps) *ChannelConnectTool { return &ChannelConnectTool{deps: d} }

func (t *ChannelConnectTool) Name() string                 { return "connect_channel" }
func (t *ChannelConnectTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *ChannelConnectTool) Category() tools.ToolCategory { return tools.CategoryChannels }
func (t *ChannelConnectTool) ReportsChangeOutcome()        {}

// Description is prometheus's connect_channel.md text.
func (t *ChannelConnectTool) Description() string {
	return "Connect a channel in one approved call: save its configuration, bind the instance's main agent in a workspace, enable it and live-test it. Supply id (the instance key: a bare type such as telegram for the default instance, or a namespaced key such as telegram.eu for an additional one), workspace_id, and agent_id for the main agent, who must be a member of that workspace — a valid token with a non-member target fails and nothing is saved. Credentials travel as stored references only, never plaintext: first request_secret for the instance's fields, wait for the person's card, then pass the returned references through the matching reference parameters; each must match that exact instance and field. WhatsApp needs no credentials here — the call starts QR pairing, the QR code appears in this chat while pairing runs (and on Connectors > WhatsApp), and Stop in this chat ends the pairing; a pairing timeout is reported as saved but not working. The result lists the facts proven by real platform calls and everything not tested; a channel that does not start is reported as saved but not working, with the reason — the checks cannot distinguish a rejected credential from an unreachable platform — and failed: nothing saved when validation stopped the save; never working. The shipped policy shows an approval card before it runs; Omnipus skips approvals in God Mode."
}

func (t *ChannelConnectTool) Parameters() map[string]any {
	properties := map[string]any{
		"id":                  map[string]any{"type": "string", "description": "Channel instance key to connect: the bare type for the default instance, or type.suffix for an additional connection, as list_channels reports them"},
		"workspace_id":        map[string]any{"type": "string", "description": "Workspace to bind the connection to"},
		"agent_id":            map[string]any{"type": "string", "description": "Main agent for the instance; must already be a member of the target workspace, otherwise the connect fails and nothing is saved"},
		expectedRevisionParam: map[string]any{"type": "string", "description": "The channel's revision from list_channels; \"absent\" when the instance is not configured yet"},
	}
	for _, binding := range channelReferenceBindings {
		properties[binding.parameter] = map[string]any{
			"type": "string",
			"description": "Stored reference for field=" + binding.field + "; first request_secret with purpose and the instance recipient, then wait for the person's secure-card Submit. " +
				"Must match that exact instance and field; otherwise request a new card for that recipient. Never pass a plaintext value",
		}
	}
	return map[string]any{
		"type": "object", "properties": properties,
		"required":             []string{"id", "workspace_id", "agent_id", expectedRevisionParam},
		"additionalProperties": false,
	}
}

// whatsappChannel reports a QR-paired channel type: no credentials, pairing
// progress renders in the requesting chat (BDD-8.4).
func whatsappChannel(chType string) bool {
	return chType == "whatsapp" || chType == "whatsapp_native"
}

func (t *ChannelConnectTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	if refusal := tools.ToolArgumentRefusal(t, args); refusal != nil {
		return refusal
	}
	id, _ := args["id"].(string)
	ws, _ := args["workspace_id"].(string)
	agentID, _ := args["agent_id"].(string)
	id, ws, agentID = strings.TrimSpace(id), strings.TrimSpace(ws), strings.TrimSpace(agentID)
	if id == "" || ws == "" || agentID == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "id, workspace_id and agent_id are required; nothing saved", ""))
	}
	if _, ok := findChannelInstance(id); !ok {
		return tools.ErrorResult(errorJSON("CHANNEL_NOT_FOUND", fmt.Sprintf("Unknown channel %q; nothing saved", id), "Read list_channels for valid ids"))
	}
	in, err := readChannelReferenceInput(args)
	if err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error()+"; nothing saved", "Use request_secret for channel credentials"))
	}
	expected, missing := expectedRevisionArg(args, "list_channels")
	if missing != nil {
		return missing
	}
	if refusal := t.validateBinding(ws, agentID); refusal != nil {
		return refusal
	}
	chType, _ := config.ParseInstanceKey(id)
	// A QR-paired channel reserves its next pairing attempt for this call
	// before anything is saved (gate r3 M5): a refusal leaves the winner's
	// attempt untouched and saves nothing.
	var attemptID string
	if whatsappChannel(chType) && t.deps.AdmitPairing != nil {
		admitted, err := t.deps.AdmitPairing(id, tools.ToolCallID(ctx), tools.ToolTranscriptSessionID(ctx))
		if errors.Is(err, channels.ErrPairingDeleting) {
			return tools.ErrorResult(errorJSON("CHANNEL_BEING_DELETED",
				"this WhatsApp connection is being deleted; nothing saved",
				"Add the connection again once the deletion has finished"))
		}
		if err != nil {
			return tools.ErrorResult(errorJSON("PAIRING_IN_PROGRESS",
				"another WhatsApp pairing of this connection is starting, running or stopping; nothing saved",
				"Wait for it to finish, or stop it from the chat that started it (or use its Retry there), then connect again"))
		}
		attemptID = admitted
	}
	committed := false
	defer func() {
		if attemptID != "" && !committed && t.deps.ReleasePairing != nil {
			t.deps.ReleasePairing(id, attemptID)
		}
	}()
	configure := &ChannelConfigureTool{deps: t.deps}
	var saved config.ChannelInstanceConfig
	var savedRevision string
	writeErr := t.deps.withSharedResourceWrite(sharedChannel, id, expected, func() error {
		return t.deps.WithConfig(func(cfg *config.Config) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			before, exists := cfg.Channels[id]
			if !exists {
				before = config.ChannelInstanceConfig{Type: chType}
			}
			saved = before
			in.apply(&saved)
			saved.WorkspaceID = ws
			saved.Identity = &config.ChannelIdentity{Kind: "agent", ID: agentID}
			saved.Enabled = true
			// References are validated against the FINAL saved entry, under
			// the canonical consent scope (founder ruling 2026-10-20, M5):
			// binding and enabled are routing and do not affect it.
			if err := configure.validateChannelReferences(id, before, saved, in); err != nil {
				return err
			}
			if cfg.Channels == nil {
				cfg.Channels = make(map[string]config.ChannelInstanceConfig)
			}
			cfg.Channels[id] = saved
			// The revision of the entry THIS call saved: the live result may
			// certify only this configuration (gate r1 SFH T2-SF-03).
			savedRevision = channelRevision(cfg, id)
			return nil
		})
	})
	if r, ok := revisionConflictResult(writeErr, "list_channels"); ok {
		return r
	}
	if r, ok := referenceSelectionRefusal(writeErr); ok {
		return r
	}
	if writeErr != nil {
		return tools.ErrorResult(errorJSON("CONFIG_SAVE_FAILED", "the channel could not be saved; nothing saved", "Stored credential values were not changed"))
	}
	refs := make([]string, 0, len(in.references))
	for _, binding := range channelReferenceBindings {
		if ref, supplied := in.references[binding.parameter]; supplied {
			refs = append(refs, ref)
		}
	}
	t.selected(ctx, t.Name(), id, refs)
	switch {
	case attemptID != "":
		committed = t.deps.CommitPairing != nil && t.deps.CommitPairing(id, attemptID)
		if !committed {
			fields := map[string]any{"id": id, "workspace_id": ws, "agent_id": agentID,
				"revision": savedRevision, "saved": "configuration, binding and enable saved"}
			return changeSuccess(fields, savedNotWorking("the pairing was stopped before it started; connect again to start a new one",
				"the channel's start after the configuration reload",
				"inbound message delivery and routing to the agent", "outbound replies"))
		}
	case t.deps.RequestChannelRestart != nil:
		// A channel stopped in place, or whose earlier start failed, keeps an
		// unchanged configuration: the reload below starts it (gate r3 M10).
		t.deps.RequestChannelRestart(id)
	}
	res, confirmed := t.liveResult(id, ws, agentID, chType, savedRevision, attemptID)
	if attemptID != "" && confirmed && t.deps.ReleasePairing != nil {
		// The reload completed: an admission no start consumed (the device was
		// already linked, or nothing started) must not pass to a later attempt.
		// One the start took is left to it. An unconfirmed reload keeps the
		// reservation, since that start can still happen (M10).
		t.deps.ReleasePairing(id, attemptID)
	}
	return res
}

// pairingResult reports what this connect's own admitted pairing attempt
// actually did after the reload (gate r4 R4-A05): a running pairing needs the
// QR scanned; an already-linked device connected without pairing; a failed,
// stopped or interrupted attempt is saved but not working. It does not decide
// while the attempt has not started yet.
func (t *ChannelConnectTool) pairingResult(id, attemptID string, running bool, fields map[string]any,
	scope string, notTested []string,
) (*tools.ToolResult, bool) {
	rec, ok := t.deps.PairingAttemptState(id, attemptID)
	if !ok {
		return nil, false
	}
	switch {
	case rec.Phase == channels.PairingPhaseActive:
		fields["pairing"] = "QR pairing started; the connection works only after the QR code is scanned"
		return changeSuccess(fields, savedNotWorking("pairing not finished: scan the WhatsApp QR code", scope,
			append([]string{"device pairing"}, notTested...)...)), true
	case rec.Outcome == channels.PairingOutcomeNoPairing && running:
		fields["pairing"] = "the WhatsApp device was already linked; no QR code was needed"
		fields["proven"] = []string{"the channel connected after the reload with its existing linked device"}
		return changeSuccess(fields, working(scope, notTested...)), true
	case rec.Outcome == channels.PairingOutcomeStartFailed:
		var cause error
		if t.deps.LastStartError != nil {
			cause = t.deps.LastStartError(id)
		}
		if cause == nil {
			return changeSuccess(fields, savedNotWorking("the WhatsApp pairing could not start; the server log names the cause. "+
				"Use Retry in this chat to try again", scope, notTested...)), true
		}
		logger.ErrorCF("sysagent", "Approved channel start failed", map[string]any{
			"channel_id": id, "operation": "start_channel", "error": cause.Error(),
		})
		return changeSuccess(fields, savedNotWorking(startFailureReason(cause), scope, notTested...)), true
	case rec.Outcome == channels.PairingOutcomeStopped || rec.Outcome == channels.PairingOutcomeInterrupted:
		return changeSuccess(fields, savedNotWorking("the pairing was stopped or interrupted before it finished; "+
			"use Retry in this chat to get a new code", scope, notTested...)), true
	}
	return nil, false
}

// startFailureReason turns an explicit start failure into a value-free cause
// and its repair, never the platform's raw response.
func startFailureReason(err error) string {
	const prefix = "the channel could not be started after saving: "
	var netErr net.Error
	switch {
	case errors.Is(err, channels.ErrLocalState):
		return prefix + "its local data (session store) could not be opened. " +
			"Check that the Omnipus data directory is readable and writable; the server log names the cause. " +
			"Then connect again. Re-entering credentials will not fix this"
	case errors.As(err, &netErr), errors.Is(err, context.DeadlineExceeded):
		return prefix + "the platform could not be reached. Check the network connection, then connect again"
	default:
		return prefix + "the platform rejected the credentials or the start failed. " +
			"Check the stored credentials; the server log names the cause. Then connect again"
	}
}

// validateBinding refuses a target that is not a valid main agent of the
// workspace BEFORE anything is saved (BDD-8.7).
func (t *ChannelConnectTool) validateBinding(ws, agentID string) *tools.ToolResult {
	fail := func(msg string) *tools.ToolResult {
		return tools.ErrorResult(errorJSON("INVALID_BINDING", msg+"; failed: nothing saved", "Choose a workspace member as the main agent"))
	}
	if err := validateID(ws); err != nil {
		return fail("invalid workspace_id")
	}
	w, err := readWorkspaceFromDisk(t.deps.Home, ws)
	switch {
	case err != nil && strings.HasPrefix(err.Error(), "NOT_FOUND"):
		return fail("workspace " + ws + " was not found")
	case err != nil:
		return fail("workspace " + ws + " could not be read")
	case strings.EqualFold(strings.TrimSpace(w.Status), "archived"):
		return fail("workspace " + ws + " is archived")
	}
	var agent *config.AgentConfig
	if cfg := t.deps.GetCfg(); cfg != nil {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == agentID {
				agent = &cfg.Agents.List[i]
			}
		}
	}
	switch {
	case agent == nil:
		return fail("agent " + agentID + " was not found")
	case agent.IsSystem():
		return fail("system agents cannot be a channel's main agent")
	case agent.IsWorker():
		return fail("workers cannot be a channel's main agent")
	}
	if found, member := workspacepkg.FindForAgentPreferring(t.deps.Home, agentID, ws); !member || found != ws {
		return fail("agent " + agentID + " is not a member of workspace " + ws)
	}
	return nil
}

// liveResult reloads, then reports what the real start proved and what was
// not tested. A channel that did not start is never "working" (BDD-8.3).
func (t *ChannelConnectTool) liveResult(id, ws, agentID, chType, savedRevision, attemptID string) (*tools.ToolResult, bool) {
	fields := map[string]any{"id": id, "workspace_id": ws, "agent_id": agentID,
		"revision": savedRevision, "saved": "configuration, binding and enable saved"}
	notTested := []string{"inbound message delivery and routing to the agent", "outbound replies"}
	const scope = "the channel's start after the configuration reload"
	wait := t.deps.WaitForReloadFunc
	if wait == nil {
		wait = t.deps.ReloadAndWaitFallback
	}
	if wait == nil {
		return changeSuccess(fields, savedNotWorking("not live-tested: no live reload is wired", scope, notTested...)), false
	}
	if err := wait(); err != nil {
		return changeSuccess(fields, savedNotWorking("the reload after saving did not complete", scope, notTested...)), false
	}
	if t.deps.ChannelLiveStatus == nil {
		return changeSuccess(fields, savedNotWorking("not live-tested: channel status is unavailable", scope, notTested...)), true
	}
	running, failure, known := t.deps.ChannelLiveStatus(id)
	// Another save between this write and the reload replaced the entry: the
	// running channel is not proof for the configuration this call saved.
	if current := channelRevision(t.deps.GetCfg(), id); current != savedRevision {
		fields["revision"] = current
		return changeSuccess(fields, savedNotWorking(
			"the connection was changed by another save while it was being applied; re-read it with list_channels",
			scope, notTested...)), true
	}
	if whatsappChannel(chType) && attemptID != "" && t.deps.PairingAttemptState != nil {
		if res, decided := t.pairingResult(id, attemptID, running, fields, scope, notTested); decided {
			return res, true
		}
	}
	switch {
	case whatsappChannel(chType) && running:
		fields["pairing"] = "QR pairing started; the connection works only after the QR code is scanned"
		return changeSuccess(fields, savedNotWorking("pairing not finished: scan the WhatsApp QR code", scope, append([]string{"device pairing"}, notTested...)...)), true
	case running:
		fields["proven"] = []string{"the channel started after the reload with the saved configuration and credentials"}
		return changeSuccess(fields, working(scope, notTested...)), true
	case !known:
		return changeSuccess(fields, savedNotWorking("not live-tested: channel status is unavailable", scope, notTested...)), true
	}
	if t.deps.LastStartError != nil {
		if err := t.deps.LastStartError(id); err != nil {
			// The full cause goes to the operator log (registered credential
			// values are filtered there); the model gets a value-free cause
			// and the matching repair (gate r2 T2-SF-10).
			logger.ErrorCF("sysagent", "Approved channel start failed", map[string]any{
				"channel_id": id, "operation": "start_channel", "error": err.Error(),
			})
			return changeSuccess(fields, savedNotWorking(startFailureReason(err), scope, notTested...)), true
		}
	}
	if failure == "" {
		failure = "the channel did not start"
	}
	return changeSuccess(fields, savedNotWorking(failure, scope, notTested...)), true
}
