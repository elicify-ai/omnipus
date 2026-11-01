package systools

// agent_effort.go — create_agent / update_agent effort (effort-levels spec
// "Agent-tool parity", FR-048, FR-060, S-58, S-63, S-64, S-66, S-77): the
// agent primary's reasoning_effort and each fallback's own, checked against
// the catalog's served capability, mapped on a model change that carries no
// effort, refused on external-CLI workers.

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/effortdialect"
)

// effortErr is a refusal of an effort field whose message is the complete
// spec sentence (S-58, S-64), not the generic "fields ...: reason" form.
func effortErr(field, sentence string) error {
	return &agentmutation.FieldError{
		Code: agentmutation.InvalidInput, Fields: []string{field}, Reason: sentence, Message: sentence,
	}
}

// effortParameter is the tool schema of a stored effort value. The 32-character
// limit is checked in code so the refusal carries the spec sentence.
func effortParameter() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": `Reasoning effort for this model: "" (Not set), "provider_default", a level the model offers, "on" or "off". At most 32 characters.`,
	}
}

// agentEffortPair is the (provider, model) pair the agent primary uses.
type agentEffortPair struct{ provider, model string }

func primaryEffortPair(a *config.AgentConfig, defaults *config.AgentDefaults) agentEffortPair {
	if a.Model != nil && strings.TrimSpace(a.Model.Primary) != "" {
		return agentEffortPair{provider: strings.TrimSpace(a.Model.Provider), model: strings.TrimSpace(a.Model.Primary)}
	}
	if defaults != nil {
		return agentEffortPair{provider: defaults.DefaultModel.Provider, model: defaults.DefaultModel.Model}
	}
	return agentEffortPair{}
}

// servedEffort returns the served capability of a pair and whether the
// catalog knows the pair at all.
func servedEffort(p agentEffortPair) (effortdialect.Target, bool) {
	cat := providers.ProviderCatalog()
	if cat == nil {
		return effortdialect.Target{}, false
	}
	h := cat.Resolve(p.provider, p.model)
	if !h.Found() {
		return effortdialect.Target{}, false
	}
	// reasons is false for a known model that does not reason: it then
	// offers provider_default only.
	levels, sw, reasons := h.ServedReasoning()
	return effortdialect.Target{Levels: levels, Switch: sw, Reasons: reasons}, true
}

// effortOptions lists the values a pair offers, in order: provider_default,
// the levels as published, then the switch words.
func effortOptions(t effortdialect.Target) []string {
	opts := []string{effortdialect.ValueProviderDefault}
	opts = append(opts, t.Levels...)
	if t.Switch {
		opts = append(opts, effortdialect.ValueOn, effortdialect.ValueOff)
	}
	return opts
}

func effortOffered(v string, t effortdialect.Target) bool {
	switch v {
	case "", effortdialect.ValueProviderDefault:
		return true
	case effortdialect.ValueOff, effortdialect.ValueNone:
		return slices.Contains(t.Levels, effortdialect.ValueNone) || t.Switch
	case effortdialect.ValueOn:
		return t.Switch
	}
	return slices.Contains(t.Levels, v)
}

// checkToolEffort validates one stored effort value for a pair: a string of
// at most 32 characters, offered by the pair when the catalog knows it.
func checkToolEffort(field string, raw any, p agentEffortPair) (string, error) {
	v, ok := raw.(string)
	if !ok {
		return "", effortErr(field, effortNullMessage)
	}
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > effortMaxChars {
		return "", effortErr(field, effortLengthMessage)
	}
	if t, known := servedEffort(p); known && !effortOffered(v, t) {
		return "", effortErr(field, fmt.Sprintf("%s doesn't offer %s. Options: %s.", p.model, v, strings.Join(effortOptions(t), ", ")))
	}
	return v, nil
}

// applyPrimaryEffortArg sets the agent primary's effort from args, or maps the
// stored value when the pair changed without one (S-58).
func applyPrimaryEffortArg(a *config.AgentConfig, args map[string]any, before agentEffortPair, defaults *config.AgentDefaults) error {
	raw, present := args["reasoning_effort"]
	if present && a.IsExternalCLIWorker() {
		return effortErr("reasoning_effort", agentmutation.ExternalCLIEffortRefusal)
	}
	after := primaryEffortPair(a, defaults)
	if present {
		v, err := checkToolEffort("reasoning_effort", raw, after)
		if err != nil {
			return err
		}
		a.ReasoningEffort = v
		return nil
	}
	if after == before || a.ReasoningEffort == "" {
		return nil
	}
	mapped, err := mapStoredEffort(a.ReasoningEffort, after)
	if err != nil {
		return err
	}
	a.ReasoningEffort = mapped
	return nil
}

// mapStoredEffort maps a stored effort onto a new pair (DE-5): a blocked
// mapping is refused with the options to send instead; Off stays stored as
// written when the target offers it; an unknown pair keeps the value.
func mapStoredEffort(stored string, p agentEffortPair) (string, error) {
	t, known := servedEffort(p)
	if !known {
		return stored, nil
	}
	m := effortdialect.MapEffort(stored, t)
	if m.Blocked {
		opts := append([]string{effortdialect.ValueProviderDefault}, t.Levels...)
		return "", effortErr("reasoning_effort", fmt.Sprintf(
			"%s can't turn reasoning off. Send reasoning_effort with one of: %s.", p.model, strings.Join(opts, ", ")))
	}
	if effortdialect.IsOff(stored) && effortdialect.IsOff(m.Value) {
		return stored, nil
	}
	return m.Value, nil
}

// storedEffortView is the get_agent read of the primary effort: the stored
// value, or null when it is Not set or the agent is an external-CLI worker
// (S-77, S-94).
func storedEffortView(a config.AgentConfig) any {
	if a.IsExternalCLIWorker() || a.ReasoningEffort == "" {
		return nil
	}
	return a.ReasoningEffort
}

// effortResultFields lists the update_agent arguments that can change a stored
// effort: the effort fields themselves and a primary model change, which maps
// the stored value (S-58).
var effortResultFields = []string{"reasoning_effort", "fallback_models", "model", "provider"}

// addStoredEffort puts the saved agent's effort fields on a successful
// update_agent result when the update touched one of them (S-63: "the result
// shows the stored agent with every effort field"). A failed read-back of
// the just-saved record is returned, never dropped.
func addStoredEffort(store *agentstore.Store, id string, fields []string, body map[string]any) error {
	touched := false
	for _, f := range effortResultFields {
		if slices.Contains(fields, f) {
			touched = true
			break
		}
	}
	if !touched {
		return nil
	}
	state, err := store.ReadState(id)
	if err != nil {
		return fmt.Errorf("read back the saved effort fields: %w", err)
	}
	body["reasoning_effort"] = storedEffortView(*state.Agent)
	fallbacks := make([]map[string]any, 0, len(state.Agent.FallbackModels))
	for _, fb := range state.Agent.FallbackModels {
		entry := map[string]any{"provider": fb.Provider, "model": fb.Model, "reasoning_effort": nil}
		if fb.ReasoningEffort != "" && !state.Agent.IsExternalCLIWorker() {
			entry["reasoning_effort"] = fb.ReasoningEffort
		}
		fallbacks = append(fallbacks, entry)
	}
	body["fallback_models"] = fallbacks
	return nil
}
