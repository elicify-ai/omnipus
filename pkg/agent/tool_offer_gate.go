// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// offeredToolSet is the exact set of tool names one provider request put in
// front of the model — the names of the tool definitions actually sent, after
// every narrowing step (ADR-081 D3's goal first-move door, ADR-071's
// compressed manifest, the native-search strip, the graceful-terminal
// clearing and a BeforeLLM hook's rewrite). Keys are the sanitized names the
// provider saw (tools.ToolsToProviderDefs sanitizes every name).
//
// Why the dispatch loop needs it: the loop used to resolve every tool call
// against the agent's WHOLE registry (ToolRegistry.ExecuteWithContext) and
// check it against the whole-turn policy map (resolveToolPolicyAtExec), never
// against what the request offered. UAT B-10 run 1: on the narrowed goal
// request that offered only set_goal and AskUserQuestion, a ToolSearch call
// still executed. Two documented rules forbid that:
//
//   - ADR-081 D3: the narrowed request offers "only {set_goal,
//     AskUserQuestion} ∩ policy-allowed" and "'exactly two' is exact".
//   - ADR-071 §1.1: a lazy tool is "callable only after load_tool [now
//     ToolSearch] promotes it"; until then it is listed by name in the "More
//     tools" block, not offered as a callable definition.
type offeredToolSet map[string]struct{}

// newOfferedToolSet records the names of the definitions a request sends.
func newOfferedToolSet(defs []providers.ToolDefinition) offeredToolSet {
	set := make(offeredToolSet, len(defs))
	for _, def := range defs {
		set[def.Function.Name] = struct{}{}
	}
	return set
}

// has reports whether the call named rawName (as the model sent it) or
// toolName (the registry name after UnsanitizeToolName) was offered.
func (o offeredToolSet) has(rawName, toolName string) bool {
	if _, ok := o[rawName]; ok {
		return true
	}
	_, ok := o[tools.SanitizeToolName(toolName)]
	return ok
}

// toolNotOfferedRefusal decides whether a tool call must be refused because
// the request that produced it did not offer the tool. It returns the refusal
// text for the model and true when the call must not run.
//
// It deliberately leaves two cases to the gates that already own them:
//
//   - A tool absent from filterTimePolicyMap (policy-denied at filter time, or
//     not registered at all) is NOT refused here: the exec-time policy re-check
//     further down the loop denies it with the ADR-058 permission_denied
//     payload, audit row and denial ledger, exactly as before. This gate only
//     covers a tool policy would have allowed or asked for.
//   - On a compressed, non-narrowed request, a lazy tool the session's loaded
//     set holds NOW but the request did not offer was loaded by a ToolSearch
//     call earlier in this same response (every tool loaded before the request
//     was built is in the sent definitions, see buildCompressedToolDefs).
//     ADR-071 makes a tool callable once ToolSearch promotes it, so that call
//     runs. On a narrowed request no such exemption applies: ToolSearch was not
//     offered there, and a tool loaded in an EARLIER request is exactly what
//     the narrowed door withholds.
func (al *AgentLoop) toolNotOfferedRefusal(
	ts *turnState,
	offered offeredToolSet,
	rawName, toolName string,
	filterTimePolicyMap map[string]string,
	goalForce goalForcingDecision,
	compressed bool,
) (string, bool) {
	if offered.has(rawName, toolName) {
		return "", false
	}
	if _, allowedAtFilter := filterTimePolicyMap[toolName]; !allowedAtFilter {
		return "", false
	}
	lazy := compressed && tools.ToolManifestTier(toolName) == tools.ManifestLazy
	if lazy && !goalForce.layer1 && al.sessionLoadedTools(ts.manifestBucket())[toolName] {
		return "", false
	}
	return toolNotOfferedMessage(toolName, goalForce, lazy), true
}

// toolNotOfferedMessage is the tool result a refused not-offered call gets.
func toolNotOfferedMessage(toolName string, goalForce goalForcingDecision, lazy bool) string {
	msg := fmt.Sprintf(
		"tool %q is not available in this request: it was not among the tools offered to you, so it was not run.",
		toolName)
	switch {
	case goalForce.layer1 && goalForce.askOffered:
		msg += " Right now only set_goal and AskUserQuestion can be called: register the goal with set_goal, " +
			"or ask your one question with AskUserQuestion. Your full tool set comes back once the goal record is registered."
	case goalForce.layer1:
		msg += " Right now only set_goal can be called: register the goal with set_goal first. " +
			"Your full tool set comes back once the goal record is registered."
	case lazy:
		msg += " It is listed under More tools: load it with ToolSearch first, then call it."
	}
	return msg
}
