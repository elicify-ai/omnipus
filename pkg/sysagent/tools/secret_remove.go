// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools

import (
	"context"
	"errors"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// RemoveSecretTool removes one stored secret version after the person has
// seen its consumers on the approval card (FR-077, BDD-15.1–15.6). With #1277
// landed an in-use removal proceeds: the live reload applies it and the next
// boot starts the affected integrations degraded.
type RemoveSecretTool struct {
	deps *Deps
	secretSelectionAudit
}

func NewRemoveSecretTool(d *Deps) *RemoveSecretTool { return &RemoveSecretTool{deps: d} }

func (t *RemoveSecretTool) Name() string                 { return "remove_secret" }
func (t *RemoveSecretTool) Scope() tools.ToolScope       { return tools.ScopeCore }
func (t *RemoveSecretTool) Category() tools.ToolCategory { return tools.CategoryPlatform }
func (t *RemoveSecretTool) ReportsChangeOutcome()        {}

// Description is prometheus's remove_secret.md text with its landed (#1277)
// in-use sentence, per the chief's ruling.
func (t *RemoveSecretTool) Description() string {
	return "Remove one stored secret reference after the person has seen who depends on it. Supply the full reference name as list_secrets reports it, version suffix included. The consumers are listed on the approval card before anything is deleted. Removal of an in-use reference proceeds behind the approval; the result names every consumer, reports the stale live value truthfully, and the next boot starts with the affected integrations degraded. Removing an older version that a replacement superseded leaves the integration unaffected and working. The result reports three separate facts and they must stay separate: whether the value is durably deleted, what the live effect on running connections is, and whether the configuration still starts on the next boot. Never summarise them into a single ok. Values are never an input or an output. Only Omnipus holds this tool — an agent that wants a reference removed asks Omnipus. The shipped policy shows an approval card before it runs; Omnipus skips approvals in God Mode."
}

func (t *RemoveSecretTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"reference_name": map[string]any{"type": "string", "description": "Full stored reference name as list_secrets reports it, including the version suffix; removing one version leaves other versions of the same base name untouched"},
		},
		"required":             []string{"reference_name"},
		"additionalProperties": false,
	}
}

// ApprovalContext shows the person every configured consumer of the
// reference BEFORE they approve its removal (FR-077, BDD-15.1).
func (t *RemoveSecretTool) ApprovalContext(_ context.Context, args map[string]any) map[string]any {
	ref := removalReference(args)
	consumers := t.consumers(ref)
	return map[string]any{"consumers": consumers, "in_use": len(consumers) > 0}
}

func (t *RemoveSecretTool) consumers(ref string) []string {
	if ref == "" || t.deps == nil || t.deps.GetCfg == nil {
		return []string{}
	}
	return append([]string{}, secretrequest.ReferenceConsumers(t.deps.GetCfg())[ref]...)
}

// removalReference reads reference_name (no aliases) EXACTLY as given:
// Settings can store distinct keys that differ only by surrounding spaces,
// and list_secrets shows them verbatim, so trimming could delete a different
// secret than the one approved (gate r1 M4, security T2-S6).
func removalReference(args map[string]any) string {
	ref, _ := args["reference_name"].(string)
	return ref
}

func (t *RemoveSecretTool) Execute(ctx context.Context, args map[string]any) (res *tools.ToolResult) {
	defer func() { res = finalizeChangeResult(t.Name(), res) }()
	ref := removalReference(args)
	if strings.TrimSpace(ref) == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "reference_name is required", "Read list_secrets for the stored names"))
	}
	if t.deps == nil || t.deps.CredStore == nil {
		return tools.ErrorResult(errorJSON("STORE_UNAVAILABLE", "the secret store is unavailable; nothing was removed", ""))
	}
	consumers := t.consumers(ref)
	removed, err := t.deps.CredStore.RemoveReference(ref)
	var notFound *credentials.NotFoundError
	switch {
	case errors.As(err, &notFound):
		return tools.ErrorResult(errorJSON("NOT_FOUND", "no stored reference "+ref+"; nothing was removed", "Read list_secrets for the stored names"))
	case err != nil:
		return tools.ErrorResult(errorJSON("REMOVE_FAILED", "the reference could not be removed; nothing was removed", "Check that the credential store is unlocked and writable, then try again"))
	}
	t.auditRemoval(ctx, ref, consumers)
	durable := "durable: " + ref + " was deleted from the encrypted store"
	if !removed.Plain {
		durable = "durable: " + ref + " and its recipient binding were deleted from the encrypted store"
	}
	if removed.Selected {
		durable += "; it was the selected version of private variable " + removed.Secret.Recipient.Field + ", which is now unset"
	}
	live, boot := t.liveAndBootFacts(consumers)
	return changeSuccess(map[string]any{
		"reference": ref, "consumers": consumers,
		"durable_deletion": durable, "live_effect": live, "boot_validity": boot,
	}, working("the encrypted store after removal; the live reload result; the saved configuration's boot validity", "each consumer's own connection behaviour"))
}

// liveAndBootFacts applies the removal live when configuration uses the
// reference and reports the live and boot facts separately (BDD-15.3).
func (t *RemoveSecretTool) liveAndBootFacts(consumers []string) (live, boot string) {
	if len(consumers) == 0 {
		return "live: no configured integration used it; nothing running changed",
			"boot: the configuration still starts on the next boot"
	}
	list := strings.Join(consumers, ", ")
	boot = "boot: the configuration still starts on the next boot; " + list + " start degraded because their saved reference is now missing"
	wait := t.deps.WaitForReloadFunc
	if wait == nil {
		wait = t.deps.ReloadAndWaitFallback
	}
	if wait == nil {
		return "live: not applied — no live reload is wired; running connections keep the old value until the next reload: " + list, boot
	}
	if err := wait(); err != nil {
		return "live: the reload after removal did not complete; running connections may still hold the old value until the next reload: " + list, boot
	}
	return "live: the configuration was reloaded; these consumers no longer have the credential and run degraded: " + list, boot
}

func (t *RemoveSecretTool) auditRemoval(ctx context.Context, ref string, consumers []string) {
	audit.Emit(ctx, t.logger.Load(), "secret.removed", audit.SeverityInfo, map[string]any{
		"reference": ref, "consumers": append([]string(nil), consumers...),
		"agent_id": tools.ToolAgentID(ctx), "session_id": tools.ToolTranscriptSessionID(ctx),
	})
}
