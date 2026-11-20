// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_packages_publish.go: what an apply does after the commit point (epic
// #1094 S1a T-5; spec revision 7 FR-036, FR-036a, FR-036b, FR-040, FR-041):
// P1 live publish by reload, P2 agent_created, P3 the main chat of a Main,
// P4 audit. Every failure here is a warning; the stored agent is kept and
// the answer never says "nothing imported".

package gateway

import (
	"log/slog"
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
)

const (
	packageActivationPendingMessage = "The agent is stored, but the gateway has not confirmed that it is live yet. " +
		"Restart the gateway, or wait and check the agent list, before chatting with it."
	packageWorkerDelegationMessage = "This agent is a worker: it is not a chat partner. It is reached by delegation " +
		"once you draw a delegation line to it from a Main agent in the Team graph."
)

// afterCommit runs P1–P4, finishes the journal and staging, and answers 200.
func (p *packageApplyRun) afterCommit(w http.ResponseWriter) {
	id := p.agentID()
	if err := p.journal(omnpkg.JournalCommitted); err != nil {
		// Start-up recovery finishes a committed import from the disk even
		// when the journal still says started.
		slog.Warn("packages: write committed journal", "staging_id", p.stagingID, "error", err)
	}
	if p.crashAt(w, "committed") || p.crashAt(w, "P1") {
		return
	}
	if p.notDurable {
		p.warnings = append(p.warnings, packageWarning{
			Code: packageCodePublishNotDurable, ElementSourceID: p.sourceID,
			Message: "The agent is imported, but the data folder could not confirm the write to disk; a power cut now could undo it.",
		})
	}
	res := gen.PackageImportResult{
		Activation: gen.PackageImportResultActivationActive,
		Mode:       gen.PackageImportResultMode(gen.PackageImportApplyRequestModeNew),
	}
	if p.publishLive() {
		p.a.emitAgentCreated(id) // P2, only after a confirmed reload
		p.ensureImportedMain()   // P3
	} else {
		res.Activation = gen.PackageImportResultActivationPending
		msg := packageActivationPendingMessage
		res.ActivationMessage = &msg
	}
	if !p.plan.Create.Agent.IsChatTarget() && res.ActivationMessage == nil {
		msg := packageWorkerDelegationMessage
		res.ActivationMessage = &msg
	}
	fields := map[string]string{
		"outcome": "success", "package_sha256": p.packageSHA, "staging_id": p.stagingID,
		"workspace_id": p.workspaceID, "source_id": p.sourceID, "activation": string(res.Activation),
	}
	if p.notDurable {
		fields["note"] = "published, directory sync failed"
	}
	if err := p.a.packageAudit("import", p.actor, id, fields); err != nil { // P4
		slog.Error("packages: audit of a committed import failed", "agent_id", id, "error", err)
		p.warnings = append(p.warnings, packageWarning{
			Code: packageCodeAuditFailed, ElementSourceID: p.sourceID,
			Message: "The agent is imported, but the audit log could not be written.",
		})
	}
	if err := p.journal(omnpkg.JournalDone); err != nil {
		slog.Warn("packages: write done journal", "staging_id", p.stagingID, "error", err)
	}
	if err := p.a.packageRegistry().FinishApply(p.stagingID); err != nil {
		slog.Warn("packages: delete finished staging", "staging_id", p.stagingID, "error", err)
	}
	p.a.forgetPreview(p.stagingID)
	p.fillResult(&res)
	writeJSON(w, http.StatusOK, res)
}

// publishLive is P1: the reload that lists the agent, confirmed or not.
func (p *packageApplyRun) publishLive() bool {
	var confirmed bool
	var err error
	if packageT5.Reload != nil {
		confirmed, err = packageT5.Reload()
	} else {
		confirmed, err = p.a.triggerReloadAndWaitOutcome()
	}
	if err != nil || !confirmed {
		slog.Warn("packages: live publish of the imported agent not confirmed",
			"agent_id", p.agentID(), "timeout", packageT5.ReloadTimeout, "error", err)
		return false
	}
	return true
}

// ensureImportedMain is P3 (FR-036a): the imported Main's main chat in its
// workspace only, when mainSessionPairEligible allows it. Eligibility is read
// from the stored plan agent and the workspace on disk. A failure is the
// warning main_chat_pending.
func (p *packageApplyRun) ensureImportedMain() {
	agent := p.plan.Create.Agent
	ws, err := readWorkspaceFile(p.a.homePath, p.workspaceID)
	if err != nil {
		p.mainChatPending(err)
		return
	}
	eligibility := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{agent}}}
	if !mainSessionPairEligible(eligibility, ws, agent.ID) {
		return
	}
	if packageT5.EnsureMain != nil {
		err = packageT5.EnsureMain(agent.ID, p.workspaceID)
	} else {
		_, err = p.a.ensureMainSession(p.workspaceID, agent.ID, p.actor)
	}
	if err != nil {
		p.mainChatPending(err)
	}
}

func (p *packageApplyRun) mainChatPending(err error) {
	slog.Warn("packages: main chat for the imported agent not created", "agent_id", p.agentID(), "error", err)
	p.warnings = append(p.warnings, packageWarning{
		Code: packageCodeMainChatPending, ElementSourceID: p.sourceID,
		Message: "The main chat is created when you open the agent.",
	})
}

// fillResult puts created[] and warnings[] on the generated result. S1a
// lists no secrets.
func (p *packageApplyRun) fillResult(res *gen.PackageImportResult) {
	res.NeedsSecrets = resized(res.NeedsSecrets, 0)
	res.Created = resized(res.Created, 1)
	res.Created[0].Type = omnpkg.ElementTypeAgent
	res.Created[0].SourceId = p.sourceID
	res.Created[0].NewId = p.agentID()
	res.Created[0].Name = p.plan.Name
	res.Created[0].WorkspaceId = p.workspaceID
	res.Warnings = resized(res.Warnings, len(p.warnings))
	for i, wn := range p.warnings {
		res.Warnings[i].Code, res.Warnings[i].Message = wn.Code, wn.Message
		if wn.ElementSourceID != "" {
			sid := wn.ElementSourceID
			res.Warnings[i].ElementSourceId = &sid
		}
		if len(wn.Items) > 0 {
			items := append([]string(nil), wn.Items...)
			res.Warnings[i].Items = &items
		}
	}
}
