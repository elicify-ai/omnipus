// loop_slash.go: Slash, skill and memory commands

package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/commands"
)

func (al *AgentLoop) handleCommand(
	ctx context.Context,
	msg bus.InboundMessage,
	agent *AgentInstance,
	opts *processOptions,
) (string, bool) {
	if !commands.HasCommandPrefix(msg.Content) {
		return "", false
	}

	if matched, handled, reply := al.applyExplicitSkillCommand(msg.Content, agent, opts); matched {
		return reply, handled
	}

	// applyMemoryCommandPrompt runs after the skill hook and before dispatch
	// (same seam as applyExplicitSkillCommand above). Since /remember, /recall,
	// and /retrospective are registered builtins (pkg/commands/cmd_memory.go),
	// applyExplicitSkillCommand's own builtin-wins check already returns
	// matched=false for these three names, so this hook is what actually
	// rewrites their turn. See applyMemoryCommandPrompt's doc comment for why
	// a rewrite hook is used instead of a Handler.
	if matched, handled, reply := al.applyMemoryCommandPrompt(msg.Content, opts); matched {
		return reply, handled
	}

	// /goal and /loop (ADR-049 D6, Gap #8/r2 origin gating) — checked before
	// registered-command dispatch so a matched verb can answer synchronously
	// (status/clear/stop) or rewrite the turn (goal set) exactly like the
	// hooks above. A non-user-initiated turn's "/goal"/"/loop" text is NOT
	// matched by either hook and falls through to normal dispatch/passthrough.
	if matched, handled, reply := al.applyGoalCommandPrompt(ctx, msg, agent, opts); matched {
		return reply, handled
	}
	if matched, handled, reply := al.applyLoopCommandPrompt(ctx, msg, agent, opts); matched {
		return reply, handled
	}

	if al.cmdRegistry == nil {
		return "", false
	}

	rt := al.buildCommandsRuntime(agent, opts)
	executor := commands.NewExecutor(al.cmdRegistry, rt)

	var commandReply string
	result := executor.Execute(ctx, commands.Request{
		Channel:  msg.Channel,
		ChatID:   msg.ChatID,
		SenderID: msg.Sender.CanonicalID,
		Text:     msg.Content,
		Reply: func(text string) error {
			commandReply = text
			return nil
		},
	})

	switch result.Outcome {
	case commands.OutcomeHandled:
		if result.Err != nil {
			return mapCommandError(result), true
		}
		if commandReply != "" {
			return commandReply, true
		}
		return "", true
	default: // OutcomePassthrough — let the message fall through to LLM
		return "", false
	}
}

// activeSkillNames returns the skills active for THIS turn only — never the
// agent's full grant list (ADR-072 D1/D3: skills are loaded on demand via the
// Skill tool, not force-injected into every turn's context).
//
// Before ADR-072, this unioned agent.SkillsFilter (the agent's ENTIRE
// per-agent grant list, agentCfg.Skills) with opts.ForcedSkills every single
// message — the exact force-load mechanism the on-demand Skill tool
// (pkg/tools/skill.go) replaces. A turn's active skills are now only what was
// explicitly loaded this turn: via opts.ForcedSkills, which the Skill tool's
// "load" outcome and the pre-existing /<slug> slash-command
// (applyExplicitSkillCommand) and, pre-ADR-091, delegate's requested_skill
// (D9, the deleted spawnSubTurn's ForcedSkills append) all populated
// one-shot, per turn — never via the agent's static grant list, which only
// gates WHICH skills may be loaded (skillAllowed/D5), not which ones are.
// See pkg/agent/subturn_identity.go::resolveRequestedSkillForChild's own doc
// comment for the ADR-091 fix lane RX-SUBTURN finding that requested_skill's
// grant/deny/unresolvable enforcement has no confirmed production caller
// today — this ForcedSkills append may be part of the same gap.
func activeSkillNames(agent *AgentInstance, opts processOptions) []string {
	if agent == nil {
		return nil
	}

	if len(opts.ForcedSkills) == 0 {
		return nil
	}

	var resolved []string
	seen := make(map[string]struct{}, len(opts.ForcedSkills))
	for _, name := range opts.ForcedSkills {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if agent.ContextBuilder != nil {
			if canonical, ok := agent.ContextBuilder.ResolveSkillName(name); ok {
				name = canonical
			}
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		resolved = append(resolved, name)
	}

	return resolved
}

// applyExplicitSkillCommand handles the "/<name>" one-shot skill activation
// introduced by the unified slash-command + skill menu (D2/D3/D4/R1).
//
// Resolution order (R1):
//  1. If <name> is any registered built-in command (visible or hidden) →
//     return matched=false so the normal command dispatcher handles it.
//     Registration is the sole authority — hidden/deprecated back-compat
//     aliases also win (D3: "built-ins win").
//  2. If <name> resolves to an installed skill (exact, case-insensitive) →
//     force the skill for THIS turn (one-shot, no arming/pending state).
//     If a message follows ("/<skill> the message"), set opts.UserMessage to
//     that message. If no message follows, opts.UserMessage retains the
//     literal "/<skill-id>" token as the user turn (the skill's injected
//     instructions from its SKILL.md body drive the LLM response, per R1).
//  3. Otherwise → return matched=false so the text is delivered as a normal
//     chat message (D4 — unknown /<x> is not an error).
//
// The old arm/pending path (setPendingSkills) and /use-token guard are removed.
func (al *AgentLoop) applyExplicitSkillCommand(
	raw string,
	agent *AgentInstance,
	opts *processOptions,
) (matched bool, handled bool, reply string) {
	cmdName, ok := commands.CommandName(raw)
	if !ok {
		// No leading "/" token at all — not our concern.
		return false, false, ""
	}

	// D3: if <name> is any registered built-in (visible or hidden/deprecated),
	// let normal dispatch run. Hidden commands are back-compat aliases that must
	// not be shadowed by a skill of the same name — registration is the sole
	// authority (D3: "built-ins win").
	if al.cmdRegistry != nil {
		if _, found := al.cmdRegistry.Lookup(cmdName); found {
			return false, false, ""
		}
	}

	// No skill context — cannot resolve skills; fall through to normal message.
	if agent == nil || agent.ContextBuilder == nil {
		return false, false, ""
	}

	// Attempt exact, case-insensitive slug resolution.
	skillName, ok := agent.ContextBuilder.ResolveSkillName(cmdName)
	if !ok {
		// Unknown /<x> → normal chat message (D4).
		return false, false, ""
	}

	// Skill matched — one-shot activation (R1).
	if opts != nil {
		opts.ForcedSkills = append(opts.ForcedSkills, skillName)

		// If a message follows the skill token, replace opts.UserMessage with
		// that message. If no message is provided, leave opts.UserMessage as
		// the literal "/<skill-id>" token — it becomes the user turn text and
		// the skill's injected instructions (from SKILL.md body) drive the LLM
		// response. The literal token is intentional; the SPA renders it as a
		// compact "skill: <name>" indicator (R2) so users never see the raw token.
		parts := strings.Fields(strings.TrimSpace(raw))
		if len(parts) >= 2 {
			message := strings.TrimSpace(strings.Join(parts[1:], " "))
			if message != "" {
				opts.UserMessage = message
			}
		}
	}

	// Return matched=true, handled=false so the turn continues to the LLM.
	return true, false, ""
}

// applyMemoryCommandPrompt rewrites opts.UserMessage into a steering prompt
// for the three memory slash commands (/remember, /recall, /retrospective)
// and lets the turn continue to the LLM afterward. Templates live in
// commands.MemoryCommandSteeringPrompt (pkg/commands/cmd_memory.go).
//
// Constraint — why a rewrite hook and not a commands.Definition.Handler:
// a Handler runs synchronously inside Executor.executeDefinition and replies
// immediately (pkg/commands/executor.go), which short-circuits the turn
// BEFORE the LLM ever sees it. These three commands need the model itself to
// invoke a real tool (remember / recall_memory / recall_conversation /
// run_retrospective) and shape its reply from the tool's actual output, so
// their Definitions are registered with Handler: nil (passthrough, per
// executor.go's OutcomePassthrough) and this hook does the rewrite instead —
// mirroring how applyExplicitSkillCommand rewrites opts.UserMessage for
// one-shot skill activation above.
//
// Every agent is given the remember / recall_memory / recall_conversation /
// run_retrospective tools now (pkg/agent/instance.go and this package's
// agent.Tools.Register calls register them unconditionally — the retired
// "main" sentinel used to be excluded by a hardcoded identity check, which
// went away with the sentinel). Whether an agent can actually use one is
// governed by its own tool policy like any other tool; the steering prompt
// still degrades gracefully if a policy denies it — the model simply reports
// it doesn't have that capability instead of the turn erroring.
func (al *AgentLoop) applyMemoryCommandPrompt(
	raw string,
	opts *processOptions,
) (matched bool, handled bool, reply string) {
	cmdName, ok := commands.CommandName(raw)
	if !ok {
		return false, false, ""
	}

	args := commands.CommandArgs(raw)
	prompt, ok := commands.MemoryCommandSteeringPrompt(cmdName, args)
	if !ok {
		return false, false, ""
	}

	if opts != nil {
		opts.UserMessage = prompt
	}

	// matched=true, handled=false: the turn continues to the LLM with the
	// rewritten steering prompt as its user message.
	return true, false, ""
}

func (al *AgentLoop) buildCommandsRuntime(agent *AgentInstance, opts *processOptions) *commands.Runtime {
	registry := al.GetRegistry()
	cfg := al.GetConfig()
	rt := &commands.Runtime{
		Config:          cfg,
		ListAgentIDs:    registry.ListAgentIDs,
		ListDefinitions: al.cmdRegistry.Definitions,
		GetEnabledChannels: func() []string {
			if cm := al.getChannelManager(); cm != nil {
				return cm.GetEnabledChannels()
			}
			return nil
		},
		GetActiveTurn: func() any {
			info := al.GetActiveTurn()
			if info == nil {
				return nil
			}
			return info
		},
		SwitchChannel: func(value string) error {
			cm := al.getChannelManager()
			if cm == nil {
				return fmt.Errorf("channel manager not initialized")
			}
			if _, exists := cm.GetChannel(value); !exists && value != "cli" {
				return fmt.Errorf("channel '%s' not found or not enabled", value)
			}
			return nil
		},
	}
	if agent != nil && agent.ContextBuilder != nil {
		rt.ListSkillNames = agent.ContextBuilder.ListSkillNames
	}
	rt.ReloadConfig = func() error {
		if al.reloadFunc == nil {
			return ErrReloadNotConfigured
		}
		return al.reloadFunc()
	}
	if agent != nil {
		if agent.ContextBuilder != nil {
			rt.ListSkillNames = agent.ContextBuilder.ListSkillNames
		}
		rt.GetModelInfo = func() (string, string) {
			agent.mu.RLock()
			m, c := agent.Model, agent.Candidates
			agent.mu.RUnlock()
			return m, resolvedCandidateProvider(c, cfg.Agents.Defaults.DefaultModel.Provider)
		}
		rt.SwitchModel = func(value string) (string, error) {
			// Shared in-place model switch (#73): same path as the
			// PUT /api/v1/agents/{id} model change.
			return al.ApplyAgentModel(agent.ID, value)
		}

		rt.ClearHistory = func() error {
			if opts == nil {
				return fmt.Errorf("process options not available")
			}
			if agent.Sessions == nil {
				return fmt.Errorf("sessions not initialized for agent")
			}

			return clearSessionWindow(agent.Sessions, opts.SessionKey)
		}
	}

	// Inject the session ID accessor so /cancel can target the current session.
	if opts != nil {
		sessionKey := opts.SessionKey
		rt.SessionID = func() string { return sessionKey }
	}

	// Inject the agent loop so CancelActiveTurn can call
	// RequestCancelForSession (ADR-057 FR-100/FR-041: InterruptSession, the
	// symbol this comment used to name, was retired by U8's collapse of the
	// four legacy interrupt entry points behind Interrupt/InterruptSessionHard
	// plus a mandatory InterruptScope; CancelActiveTurn's own call has always
	// gone through RequestCancelForSession, pkg/commands/runtime.go, never
	// direct to an Interrupt* function).
	rt = rt.WithAgentLoop(al)

	return rt
}

func mapCommandError(result commands.ExecuteResult) string {
	if result.Command == "" {
		return fmt.Sprintf("Failed to execute command: %v", result.Err)
	}
	return fmt.Sprintf("Failed to execute /%s: %v", result.Command, result.Err)
}
