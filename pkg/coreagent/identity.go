package coreagent

import (
	"strings"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// Defaults applied when a create omits identity, and when a stored value is
// missing or not in the closed set. A read only emits them; nothing persists
// them.
const (
	DefaultFigure = string(generated.AgentFigureOmnipus)
	DefaultRole   = string(generated.AgentRoleGeneral)
	DefaultColor  = string(generated.AgentColorHash8E9196) // Pewter
)

// CanonicalFigure reports whether raw is one of the five figure words, exact case.
func CanonicalFigure(raw string) (string, bool) {
	f := generated.AgentFigure(raw)
	if !f.Valid() {
		return "", false
	}
	return string(f), true
}

// CanonicalRole reports whether raw is one of the 31 role slugs, exact case.
func CanonicalRole(raw string) (string, bool) {
	r := generated.AgentRole(raw)
	if !r.Valid() {
		return "", false
	}
	return string(r), true
}

// PaletteColors lists the avatar palette (AgentColor.yaml) for refusal messages.
func PaletteColors() []string {
	return []string{
		string(generated.AgentColorHash4F8A80), string(generated.AgentColorHash5E6973),
		string(generated.AgentColorHash6E8BA8), string(generated.AgentColorHash7A8C9E),
		string(generated.AgentColorHash7B7290), string(generated.AgentColorHash8E9196),
		string(generated.AgentColorHash9C8A5B), string(generated.AgentColorHashA97142),
		string(generated.AgentColorHashB76E79), string(generated.AgentColorHashCDBA96),
	}
}

// CanonicalColor accepts any letter-case of a palette hex and returns the
// uppercase enum value. Anything else, including a well-formed hex outside the
// ten (and any colour of the removed previous palette), is not canonical. It is
// the one function both the write path and the read path use.
func CanonicalColor(raw string) (string, bool) {
	upper := strings.ToUpper(raw)
	if c := generated.AgentColor(upper); c.Valid() {
		return string(c), true
	}
	return "", false
}

// WireIdentity is the read backstop. A stored value in the enum is emitted
// (colour letter-case is normalized) and anything else emits the default. It
// never writes.
func WireIdentity(figure, role, color string) (generated.AgentFigure, generated.AgentRole, generated.AgentColor) {
	fig := generated.AgentFigure(DefaultFigure)
	if f := generated.AgentFigure(figure); f.Valid() {
		fig = f
	}
	outRole := generated.AgentRole(DefaultRole)
	if r := generated.AgentRole(role); r.Valid() {
		outRole = r
	}
	outColor := generated.AgentColor(DefaultColor)
	if canon, ok := CanonicalColor(color); ok {
		outColor = generated.AgentColor(canon)
	}
	return fig, outRole, outColor
}

// migrateUnenforcedAgentIdentity rewrites figure and role on agents SeedConfig
// does not enforce (not a compiled built-in, not a system agent). A value
// already in the enum is left alone. Colour is never written: an unknown stored
// colour reads as the default. Icon is never written. Returns whether any field
// changed.
func migrateUnenforcedAgentIdentity(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	changed := false
	for i := range cfg.Agents.List {
		a := &cfg.Agents.List[i]
		if identityEnforced(a.ID) {
			continue
		}
		if !generated.AgentRole(a.Role).Valid() {
			next := DefaultRole // no legacy icon to derive from: the field is removed
			if a.Role != next {
				a.Role = next
				changed = true
			}
		}
		if !generated.AgentFigure(a.Figure).Valid() {
			if a.Figure != DefaultFigure {
				a.Figure = DefaultFigure
				changed = true
			}
		}
	}
	return changed
}

func identityEnforced(id string) bool {
	if ByID(CoreAgentID(id)) != nil {
		return true
	}
	for _, sa := range SystemAgents() {
		if string(sa.ID) == id {
			return true
		}
	}
	return false
}
