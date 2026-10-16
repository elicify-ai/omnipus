package agent

import (
	"html"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

type sessionPromptCache struct {
	mu            sync.Mutex
	tools         *SessionToolList
	skills        *FrozenSkillList
	skillSection  string
	skillPosition int
}

func (al *AgentLoop) promptCacheSession(ts *turnState) *sessionPromptCache {
	key := ts.manifestBucket()
	initial := &sessionPromptCache{tools: NewSessionToolList(ts.cacheSessionID())}
	if key == "" {
		return initial
	}
	value, _ := al.promptCacheSessions.LoadOrStore(key, initial)
	state, ok := value.(*sessionPromptCache)
	if !ok {
		panic("invalid prompt-cache session state")
	}
	return state
}

func (al *AgentLoop) retainAdvertisedTools(ts *turnState, defs []providers.ToolDefinition) {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Function.Name)
	}
	ts.agent.Tools.RetainLoadedTools(names)
}

// restoreLoadedToolRetention re-stamps a replacement registry from the session's
// loaded names BEFORE GetAll can hide fresh deferred registrations. Registration,
// current policy and MCP assignment still decide which names may be restored.
func (al *AgentLoop) restoreLoadedToolRetention(ts *turnState) {
	if ts == nil || ts.agent == nil || ts.agent.Tools == nil {
		return
	}
	loaded := al.sessionLoadedTools(ts.manifestBucket())
	candidates := make([]tools.Tool, 0, len(loaded))
	for name := range loaded {
		if tool, ok := ts.agent.Tools.GetIncludingHidden(name); ok {
			candidates = append(candidates, tool)
		}
	}
	allowed, _ := tools.FilterToolsByPolicy(candidates, ts.agent.AgentType, ts.agent.LoadToolPolicy())
	names := make([]string, 0, len(allowed))
	for _, tool := range allowed {
		names = append(names, tool.Name())
	}
	ts.agent.Tools.RetainLoadedTools(names)
}

// pruneLoadedTools drops revoked memberships, even if a later turn grants them
// again. Time passing alone never removes a loaded definition.
func (al *AgentLoop) pruneLoadedTools(bucket string, allowed []tools.Tool) map[string]bool {
	present := make(map[string]bool, len(allowed))
	for _, t := range allowed {
		present[t.Name()] = true
	}
	al.loadedToolsMu.Lock()
	for name := range al.loadedTools[bucket] {
		if !present[name] {
			delete(al.loadedTools[bucket], name)
		}
	}
	al.loadedToolsMu.Unlock()
	return al.sessionLoadedTools(bucket)
}

func (rq *agentLoopRunTurnRequest) cacheToolSurface() {
	state := rq.ri.rf.rt.al.promptCacheSession(rq.ri.rf.rt.ts)
	state.mu.Lock()
	defer state.mu.Unlock()
	// Membership lives in the session's loaded-name set. The outgoing slice
	// owns this request's schemas; retaining another full copy per idle session
	// would keep every Parameters map alive until that session is forgotten.
	defer func() { state.tools.defs = nil }()
	if rq.goalForce.layer1 {
		rq.ri.rf.providerToolDefs = GoalNarrowedTools(state.tools, true, rq.ri.rf.providerToolDefs)
		return
	}
	current := make(map[string]bool, len(rq.ri.rf.providerToolDefs))
	for _, def := range rq.ri.rf.providerToolDefs {
		state.tools.Add(def)
		current[def.Function.Name] = true
	}
	for _, def := range state.tools.Definitions() {
		if !current[def.Function.Name] {
			state.tools.Remove(def.Function.Name)
		}
	}
	rq.ri.rf.providerToolDefs = GoalNarrowedTools(state.tools, false, nil)
}

// freezeSessionSkillMenu replaces only the advertised menu section. Instructions,
// memory, permissions, active skills and clock are not frozen by this function.
func (al *AgentLoop) freezeSessionSkillMenu(ts *turnState, messages []providers.Message) []providers.Message {
	if len(messages) == 0 || messages[0].Role != "system" {
		return messages
	}
	state := al.promptCacheSession(ts)
	state.mu.Lock()
	defer state.mu.Unlock()
	const separator = "\n\n---\n\n"
	parts := strings.Split(messages[0].Content, separator)
	index := -1
	for i, part := range parts {
		if strings.HasPrefix(part, "# Skills\n") {
			index = i
			break
		}
	}
	if state.skills == nil {
		state.skillPosition = index
		if index >= 0 {
			state.skillSection = parts[index]
		}
		names := []string{}
		remaining := state.skillSection
		for {
			_, rest, ok := strings.Cut(remaining, "<name>")
			if !ok {
				break
			}
			name, rest, ok := strings.Cut(rest, "</name>")
			if !ok {
				break
			}
			names = append(names, html.UnescapeString(name))
			remaining = rest
		}
		state.skills = FreezeSkillList(ts.cacheSessionID(), names)
	}
	section := state.skillSection
	if len(state.skills.Names()) == 0 {
		section = ""
	}
	messages = append([]providers.Message(nil), messages...)
	messages[0].Content = replaceSkillMenu(messages[0].Content, section, state.skillPosition)
	messages[0].SystemParts = append([]providers.ContentBlock(nil), messages[0].SystemParts...)
	if len(messages[0].SystemParts) > 0 {
		messages[0].SystemParts[0].Text = replaceSkillMenu(messages[0].SystemParts[0].Text, section, state.skillPosition)
	}

	return messages
}

func replaceSkillMenu(text, section string, position int) string {
	const separator = "\n\n---\n\n"
	parts := strings.Split(text, separator)
	index := -1
	for i, part := range parts {
		if strings.HasPrefix(part, "# Skills\n") {
			index = i
			break
		}
	}
	if index >= 0 {
		if section == "" {
			parts = append(parts[:index], parts[index+1:]...)
		} else {
			parts[index] = section
		}
	} else if section != "" {
		position = max(0, min(position, len(parts)))
		parts = append(parts, "")
		copy(parts[position+1:], parts[position:])
		parts[position] = section
	}
	return strings.Join(parts, separator)
}

func (rq *agentLoopRunTurnRequest) recordPromptTokenSizes() error {
	state := rq.ri.rf.rt.al.promptCacheSession(rq.ri.rf.rt.ts)
	state.mu.Lock()
	skillText := state.skillSection
	sessionID := state.tools.sessionID
	state.mu.Unlock()
	record := RecordTokenSizes(rq.ri.rf.providerToolDefs, skillText)
	if record.Err != nil {
		return record.Err
	}
	logger.InfoCF("agent", "prompt token estimates", map[string]any{
		"agent_id": rq.ri.rf.rt.ts.agent.ID, "session_id": sessionID,
		"tool_list_tokens": record.ToolListTokens, "skill_text_tokens": record.SkillTextTokens,
		"estimator": record.Estimator,
	})
	return nil
}
