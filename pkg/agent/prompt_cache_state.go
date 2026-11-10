package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// SessionToolList is the authorized advertised view of one session. The real
// registry owns executable tools; the manifest's loaded set owns membership.
// This snapshot is replaced only on ordinary requests, never by goal narrowing.
type SessionToolList struct {
	sessionID string
	defs      map[string]providers.ToolDefinition
}

func NewSessionToolList(sessionID string) *SessionToolList {
	return &SessionToolList{sessionID: sessionID, defs: make(map[string]providers.ToolDefinition)}
}

func (s *SessionToolList) Add(def providers.ToolDefinition) {
	if s.defs == nil {
		s.defs = make(map[string]providers.ToolDefinition)
	}
	s.defs[def.Function.Name] = def
}
func (s *SessionToolList) Remove(name string) { delete(s.defs, name) }
func (s *SessionToolList) Definitions() []providers.ToolDefinition {
	defs := make([]providers.ToolDefinition, 0, len(s.defs))
	for _, def := range s.defs {
		defs = append(defs, def)
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Function.Name < defs[j].Function.Name })
	return defs
}

func CanonicalToolListBytes(defs []providers.ToolDefinition) []byte {
	ordered := slices.Clone(defs)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Function.Name < ordered[j].Function.Name })
	data, err := json.Marshal(ordered)
	if err != nil {
		return nil
	}
	return data
}

func GoalNarrowedTools(session *SessionToolList, narrow bool, goal []providers.ToolDefinition) []providers.ToolDefinition {
	if narrow {
		return slices.Clone(goal)
	}
	return session.Definitions()
}

type FrozenSkillList struct{ names []string }

func FreezeSkillList(_ string, names []string) *FrozenSkillList {
	return &FrozenSkillList{names: slices.Clone(names)}
}
func (f *FrozenSkillList) Names() []string { return slices.Clone(f.names) }

// TokenSizeRecord is an estimate, not a provider tokenizer count. Err prevents
// serialization failures from being recorded as a misleading zero estimate.
type TokenSizeRecord struct {
	ToolListTokens  int
	SkillTextTokens int
	Estimator       string
	Err             error
}

func RecordTokenSizes(tools []providers.ToolDefinition, skillText string) TokenSizeRecord {
	data := CanonicalToolListBytes(tools)
	if data == nil && len(tools) > 0 {
		return TokenSizeRecord{Err: fmt.Errorf("tool schemas cannot be serialized for token estimation")}
	}
	toolTokens := 0
	if len(tools) > 0 {
		toolTokens = utf8.RuneCount(data) * 2 / 5
	}
	return TokenSizeRecord{
		ToolListTokens:  toolTokens,
		SkillTextTokens: utf8.RuneCountInString(skillText) * 2 / 5,
		Estimator:       "omnipus-json-runes/2.5",
	}
}
