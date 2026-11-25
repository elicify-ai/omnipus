// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// Skills & Tools C-14: a skill set to Full has its whole instructions in the
// agent's context from the first turn, only when its outcome for that agent
// is allowed (ask_pending and deny are never injected; Full is never
// consent). The instructions are charged once to the request budget as part
// of the pinned overhead; when the pinned overhead with every Full skill and
// the output reserve leaves no room for the tool definitions and the current
// message, the turn is not sent and the chat says why. The check runs every
// turn, so a model switch or a skill that grew is caught on the next turn.

import (
	"fmt"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// fullSkillsContext renders the instructions of every Full skill whose
// outcome for this agent is allowed, in menu order. "" when none.
func (cb *ContextBuilder) fullSkillsContext(workspaceID string) string {
	// An unreadable state is logged by skillMenuView and injects nothing
	// (fail closed).
	text, _ := cb.renderFullSkills(workspaceID, cb.effectiveProjectShelf(workspaceID))
	return text
}

// InstalledFullSkillsTokens is what this agent's next turn would charge for
// its Full skills among the installed ones (ruling spec-gaps-1, C-14 "Page
// warning"): the turn's own selection and estimator with no project shelf.
// The error reports stored skill levels that cannot be read.
func (cb *ContextBuilder) InstalledFullSkillsTokens() (int, error) {
	text, err := cb.renderFullSkills("", nil)
	if err != nil {
		return 0, err
	}
	return fullSkillsTokens(text), nil
}

// FullSkillBlockTokens is the estimate the turn charges for one skill's Full
// block ("### Skill: <id>" and its body).
func FullSkillBlockTokens(id, body string) int {
	return fullSkillsTokens(fullSkillBlock(id, body))
}

func fullSkillBlock(id, body string) string {
	return fmt.Sprintf("### Skill: %s\n\n%s", id, body)
}

// renderFullSkills renders the Full skills block over the installed skills
// and the given project shelf. The error reports unreadable stored levels.
func (cb *ContextBuilder) renderFullSkills(workspaceID string, shelf skills.ProjectShelf) (string, error) {
	if cb.skillsLoader == nil {
		return "", nil
	}
	view, err := cb.skillMenuView(workspaceID)
	if err != nil {
		return "", err
	}
	all := cb.skillsLoader.ListSkills()
	eligible, isProject := cb.skillsLoader.MenuCandidates(cb.grantedPredicate(all), shelf)
	var parts []string
	for _, s := range eligible {
		if !view.FullInjected(s, isProject[strings.ToLower(s.ID)]) {
			continue
		}
		body, ok := skills.LoadSkillFile(s.Path)
		if !ok {
			continue // LoadSkillFile logs a file it cannot serve
		}
		parts = append(parts, fullSkillBlock(s.ID, body))
	}
	if len(parts) == 0 {
		return "", nil
	}
	// The same header the Skill tool's active skills use (no new agent text).
	return fmt.Sprintf(`# Active Skills

The following skills are active for this request. Follow them when relevant.

%s`, strings.Join(parts, "\n\n---\n\n")), nil
}

// fullSkillsTokens is the estimated cost of the Full skills block.
func fullSkillsTokens(text string) int {
	if text == "" {
		return 0
	}
	return estimateMessageTokens(providers.Message{Role: "system", Content: text})
}

// fullSkillsBudgetInputs are the inputs of the C-14 send check: the model's
// context, the output reserve, the pinned core, the Full skills, the tool
// definitions sent and the current message, all in tokens.
type fullSkillsBudgetInputs struct {
	contextTokens, maxTokens, pinnedCore, fullSkills, toolDefs, message int
}

// fits reports whether toolDefs + message fit the budget B left after the
// pinned overhead including the Full skills (contextBudget).
func (in fullSkillsBudgetInputs) fits() bool {
	b := contextBudget(in.contextTokens, in.maxTokens, in.pinnedCore+in.fullSkills)
	return in.toolDefs+in.message <= b
}

// fixedTokenEstimators holds per-loop fixed estimator inputs for the C-14
// send check (D9 rows 7–10 fix them); absent means the real estimators.
var fixedTokenEstimators sync.Map // *AgentLoop → fullSkillsBudgetInputs

// setFixedTokenEstimators fixes the estimator inputs of the C-14 send check
// for al: every later turn uses these numbers in place of the measured
// window, output reserve, pinned core, Full skills, tool definitions and
// message.
func setFixedTokenEstimators(al *AgentLoop, contextTokens, maxTokens, pinnedCore, fullSkills, toolDefs, message int) {
	fixedTokenEstimators.Store(al, fullSkillsBudgetInputs{
		contextTokens: contextTokens, maxTokens: maxTokens, pinnedCore: pinnedCore,
		fullSkills: fullSkills, toolDefs: toolDefs, message: message,
	})
}

// fullSkillsBudgetFor measures the C-14 inputs of one turn (or returns the
// fixed ones). ok is false when the agent has no budget (exempt, or no
// resolved window: runTurn refuses that turn on its own).
func (al *AgentLoop) fullSkillsBudgetFor(ts *turnState) (fullSkillsBudgetInputs, bool) {
	if fixed, ok := fixedTokenEstimators.Load(al); ok {
		in, _ := fixed.(fullSkillsBudgetInputs)
		return in, true
	}
	agent := ts.agent
	window, exempt, _ := agent.windowSnapshot()
	if exempt || window <= 0 {
		return fullSkillsBudgetInputs{}, false
	}
	full := 0
	if agent.ContextBuilder != nil {
		full = fullSkillsTokens(agent.ContextBuilder.fullSkillsContext(ts.opts.WorkspaceID))
	}
	return fullSkillsBudgetInputs{
		contextTokens: window,
		maxTokens:     agent.MaxTokens,
		pinnedCore:    pinnedCoreOverheadTokens(agent),
		fullSkills:    full,
		toolDefs:      al.sentToolSurfaceTokens(agent, ts.opts.TranscriptSessionID, ts.sessionKey),
		message:       estimateMessageTokens(providers.Message{Role: "user", Content: ts.userMessage}),
	}, true
}

// fullSkillsRefusal returns the C-14 chat text when this turn's Full skills
// leave no room for the request; "" when it fits. It records the Full
// skills charge on the agent so every budget site of the turn subtracts it.
func (al *AgentLoop) fullSkillsRefusal(ts *turnState) string {
	if ts == nil || ts.agent == nil {
		return ""
	}
	in, ok := al.fullSkillsBudgetFor(ts)
	if !ok {
		ts.agent.fullSkillTokens.Store(0)
		return ""
	}
	ts.agent.fullSkillTokens.Store(int64(in.fullSkills))
	if in.fullSkills == 0 || in.fits() {
		// Without Full skills the ordinary window trim keeps the request
		// within the budget, as before.
		return ""
	}
	name := ts.agent.Name
	if name == "" {
		name = ts.agent.ID
	}
	_, model := ts.agent.primaryModelPair()
	return fmt.Sprintf("The skills set to Full for %s need about %d tokens, more than %s can take with this chat. "+
		"Set some of them to Name and short description in Skills & Tools, or choose a model with a larger context.",
		name, in.fullSkills, model)
}

// refuseOversizedFullSkills ends the turn before anything is sent when the
// Full skills do not fit (C-14); the chat shows the reason.
func (rp *agentLoopRunTurnPrepare) refuseOversizedFullSkills() (agentLoopRunTurnConductorFlow, bool) {
	rt := rp.rc.rx.rr.rq.ri.rf.rt
	text := rt.al.fullSkillsRefusal(rt.ts)
	if text == "" {
		return 0, false
	}
	logger.WarnCF("agent", "Full skills do not fit the model; the turn is not sent",
		map[string]any{"agent_id": rt.ts.agentID, "session_key": rt.ts.sessionKey})
	rt.al.emitTurnErrorFrame(rt.ts, rt.ts.eventMeta("runTurn", "turn.error"), "context_window", "context_window",
		LLMError{Code: CodeContextTooLong, Message: text})
	rp.rc.rx.rr.rq.ri.turnStatus = TurnEndStatusError
	rp.rc.ret0 = turnResult{status: TurnEndStatusError, finalContent: text}
	rp.rc.ret1 = nil
	return agentLoopRunTurnConductorReturn, true
}
