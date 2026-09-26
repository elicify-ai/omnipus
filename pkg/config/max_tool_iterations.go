// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Tool iteration limit — one global ceiling, per-agent tightening (#904,
// docs/internal/specs/tool-iteration-limit-spec.md).
//
// This file is the ONE place the "max tool calls per turn" rule lives:
//
//   - EffectiveGlobalMaxToolIterations turns the saved global
//     (agents.defaults.max_tool_iterations) into the value in force, with the
//     D13/D17 in-memory correction (missing or < 1 → the shipped default,
//     > 1000 → 1000) and a saved-state for the Settings warning. It never
//     rewrites config.json.
//   - ResolveMaxToolIterations combines the in-force global with an agent's
//     own stored value: effective = min(global, own), own value <= 0 = none
//     (D17), an own value above the global is kept but ignored and flagged
//     (D1).
//   - ValidateMaxToolIterationsBound / ValidateAgentMaxToolIterations are the
//     write-side checks (FR-006, FR-007) with the exact spec messages.
//   - applyMaxToolIterationsOnLoad runs from loadConfigInternal: records
//     whether the key was present, performs the one-time import of the
//     retired OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS env var (D5-D7,
//     D17) and logs the saved-state WARN (D13).
//   - WarnCappedMaxToolIterationAgents emits the single D19 startup line.
//
// DefaultMaxToolIterations is the only literal for this limit anywhere in
// the Go code (FR-004). pkg/agent, pkg/gateway and pkg/sysagent/tools call
// these functions; none re-implements the rule.

package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const (
	// DefaultMaxToolIterations is the shipped global limit and the value a
	// missing or below-range saved global runs as (D3, D13, D17). The single
	// shipped-default constant FR-004 allows.
	DefaultMaxToolIterations = 200
	// MinMaxToolIterations and MaxMaxToolIterations bound every writable
	// limit field, global and per-agent (D2, FR-006).
	MinMaxToolIterations = 1
	MaxMaxToolIterations = 1000

	// MaxToolIterationsEnvVar is the retired environment variable. It is no
	// longer a setting source: loaded once into config.json, then ignored
	// (D5, D6).
	MaxToolIterationsEnvVar = "OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS"
)

// MaxToolIterationsSource says where an agent's effective limit comes from.
// Values match the wire enum MaxToolIterationsSource.
type MaxToolIterationsSource string

const (
	// MaxToolIterationsSourceGlobal: no own value, or the own value is above
	// the global and ignored.
	MaxToolIterationsSourceGlobal MaxToolIterationsSource = "global"
	// MaxToolIterationsSourceAgent: the agent's own lower-or-equal value
	// applies.
	MaxToolIterationsSourceAgent MaxToolIterationsSource = "agent"
)

// MaxToolIterationsSavedState classifies the global as saved in config.json.
// Values match the wire enum MaxToolIterationsSavedState.
type MaxToolIterationsSavedState string

const (
	MaxToolIterationsSavedOK       MaxToolIterationsSavedState = "ok"
	MaxToolIterationsSavedMissing  MaxToolIterationsSavedState = "missing"
	MaxToolIterationsSavedBelowMin MaxToolIterationsSavedState = "below_min"
	MaxToolIterationsSavedAboveMax MaxToolIterationsSavedState = "above_max"
)

// GlobalMaxToolIterations is the global limit in force plus how the saved
// value was classified (D13).
type GlobalMaxToolIterations struct {
	// Value is the global limit in force, always within 1..1000.
	Value int
	// SavedState classifies the saved value.
	SavedState MaxToolIterationsSavedState
	// SavedRaw is the value found in config.json; meaningful only when
	// SavedState is below_min or above_max (HasSavedRaw reports that).
	SavedRaw    int
	HasSavedRaw bool
}

// ResolvedMaxToolIterations is one agent's limit as every surface reports it.
type ResolvedMaxToolIterations struct {
	// Effective is the limit the agent runs with: min(global, own value), or
	// the global when there is no own value. Always within 1..1000.
	Effective int
	// Source is where Effective comes from.
	Source MaxToolIterationsSource
	// Override is the agent's own stored value; meaningful only when
	// HasOverride. Not bounded above — a hand-edited stored value is reported
	// truthfully.
	Override    int
	HasOverride bool
	// OverrideIgnored is true when the own value is above the global (D1).
	OverrideIgnored bool
	// Global is the global limit in force the resolution used.
	Global GlobalMaxToolIterations
}

// EffectiveGlobalMaxToolIterations returns the global limit in force. It is
// a pure function of the in-memory config: the saved value is never
// rewritten (D13). A nil receiver resolves like a missing key.
//
// "missing" is reported only when the loader saw config.json without the key
// (MaxToolIterationsKeyMissing). A config built in memory with a zero value
// (tests, programmatic callers) classifies as below_min — both run as the
// shipped default.
func (d *AgentDefaults) EffectiveGlobalMaxToolIterations() GlobalMaxToolIterations {
	if d == nil || d.MaxToolIterationsKeyMissing {
		return GlobalMaxToolIterations{Value: DefaultMaxToolIterations, SavedState: MaxToolIterationsSavedMissing}
	}
	raw := d.MaxToolIterations
	switch {
	case raw < MinMaxToolIterations:
		return GlobalMaxToolIterations{
			Value: DefaultMaxToolIterations, SavedState: MaxToolIterationsSavedBelowMin,
			SavedRaw: raw, HasSavedRaw: true,
		}
	case raw > MaxMaxToolIterations:
		return GlobalMaxToolIterations{
			Value: MaxMaxToolIterations, SavedState: MaxToolIterationsSavedAboveMax,
			SavedRaw: raw, HasSavedRaw: true,
		}
	default:
		return GlobalMaxToolIterations{Value: raw, SavedState: MaxToolIterationsSavedOK}
	}
}

// ResolveMaxToolIterations is the single resolver (FR-002). agent may be nil
// (no own value). An own value <= 0 is "no own value" (D17).
func ResolveMaxToolIterations(defaults *AgentDefaults, agent *AgentConfig) ResolvedMaxToolIterations {
	global := defaults.EffectiveGlobalMaxToolIterations()
	out := ResolvedMaxToolIterations{
		Effective: global.Value,
		Source:    MaxToolIterationsSourceGlobal,
		Global:    global,
	}
	if agent == nil || agent.MaxToolIterations <= 0 {
		return out
	}
	out.Override = agent.MaxToolIterations
	out.HasOverride = true
	if agent.MaxToolIterations > global.Value {
		out.OverrideIgnored = true
		return out
	}
	out.Effective = agent.MaxToolIterations
	out.Source = MaxToolIterationsSourceAgent
	return out
}

// MaxToolIterationsBoundMessage is the refusal text for a value outside
// 1..1000 (spec, Machine-Verifiable Constraints).
const MaxToolIterationsBoundMessage = "max_tool_iterations must be between 1 and 1000"

// ValidateMaxToolIterationsBound refuses a value outside 1..1000 (FR-006).
func ValidateMaxToolIterationsBound(n int) error {
	if n < MinMaxToolIterations || n > MaxMaxToolIterations {
		return fmt.Errorf("%s", MaxToolIterationsBoundMessage)
	}
	return nil
}

// ValidateAgentMaxToolIterations checks a per-agent value about to be saved:
// the 1..1000 bound first, then that it is not above the global in force
// (FR-006, FR-007, D10). Callers handle an explicit clear (null) before
// calling this.
func ValidateAgentMaxToolIterations(n int, defaults *AgentDefaults) error {
	if err := ValidateMaxToolIterationsBound(n); err != nil {
		return err
	}
	global := defaults.EffectiveGlobalMaxToolIterations().Value
	if n > global {
		return fmt.Errorf("max_tool_iterations %d is above the global limit (%d); "+
			"lower it, or raise the global limit in Settings → Performance", n, global)
	}
	return nil
}

// CappedMaxToolIterationAgent is one agent whose stored own value is above
// the global and therefore ignored (D1).
type CappedMaxToolIterationAgent struct {
	ID     string
	Stored int
}

// CappedMaxToolIterationAgents lists, in roster order, the agents whose own
// value is above the global in force.
func CappedMaxToolIterationAgents(defaults *AgentDefaults, agents []AgentConfig) []CappedMaxToolIterationAgent {
	var out []CappedMaxToolIterationAgent
	for i := range agents {
		r := ResolveMaxToolIterations(defaults, &agents[i])
		if r.OverrideIgnored {
			out = append(out, CappedMaxToolIterationAgent{ID: agents[i].ID, Stored: r.Override})
		}
	}
	return out
}

// cappedAgentsWarning builds the single D19 line; ok is false when no agent
// is capped.
func cappedAgentsWarning(defaults *AgentDefaults, agents []AgentConfig) (msg string, fields map[string]any, ok bool) {
	capped := CappedMaxToolIterationAgents(defaults, agents)
	if len(capped) == 0 {
		return "", nil, false
	}
	global := defaults.EffectiveGlobalMaxToolIterations().Value
	parts := make([]string, 0, len(capped))
	for _, c := range capped {
		parts = append(parts, fmt.Sprintf("%s (stored %d)", c.ID, c.Stored))
	}
	list := strings.Join(parts, ", ")
	msg = fmt.Sprintf("max_tool_iterations: %d agent(s) have an own limit above the global limit (%d); "+
		"they run at %d and their stored values are kept unchanged: %s",
		len(capped), global, global, list)
	return msg, map[string]any{"global": global, "agents": list, "count": len(capped)}, true
}

// WarnCappedMaxToolIterationAgents logs exactly one WARN line naming every
// agent whose stored own value is above the global, with its stored value and
// the global (D1, D19). Call once at startup, after the roster is loaded.
// Logs nothing when no agent is capped.
func WarnCappedMaxToolIterationAgents(defaults *AgentDefaults, agents []AgentConfig) {
	if msg, fields, ok := cappedAgentsWarning(defaults, agents); ok {
		logger.WarnF(msg, fields)
	}
}

// applyMaxToolIterationsOnLoad runs on every config.json load
// (loadConfigInternal): records whether the global key is present, performs
// the one-time env import, and logs a WARN when the saved global is outside
// 1..1000 or missing (D13). It never refuses the load (D7, D13).
func applyMaxToolIterationsOnLoad(cfg *Config, data []byte, cfgPath string, onSelfHeal SelfHealWriteHook) {
	cfg.Agents.Defaults.MaxToolIterationsKeyMissing = !maxToolIterationsKeyPresent(data)
	importMaxToolIterationsEnv(cfg, cfgPath, onSelfHeal)
	warnSavedGlobalMaxToolIterations(&cfg.Agents.Defaults)
}

// applyMaxToolIterationsEnvFreshInstall handles the env var when no
// config.json exists yet: there is no file to import into, so the clamped
// value is applied in memory for this boot; the one-time import into the file
// happens on the first load after config.json is written (no marker yet).
func applyMaxToolIterationsEnvFreshInstall(cfg *Config) {
	n, ok := parseMaxToolIterationsEnv()
	if !ok {
		return
	}
	cfg.Agents.Defaults.MaxToolIterations = n
	logger.InfoF("max_tool_iterations: "+MaxToolIterationsEnvVar+" applied for this first boot; "+
		"it is copied into config.json once that file exists and is not used after that",
		map[string]any{"value": n})
}

// maxToolIterationsKeyPresent reports whether agents.defaults carries a
// max_tool_iterations key (any value, including null). A probe error means
// the full unmarshal already accepted the bytes, so treat it as present
// rather than invent a "missing" warning.
func maxToolIterationsKeyPresent(data []byte) bool {
	var probe struct {
		Agents struct {
			Defaults map[string]json.RawMessage `json:"defaults"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return true
	}
	raw, ok := probe.Agents.Defaults["max_tool_iterations"]
	return ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// warnSavedGlobalMaxToolIterations logs the D13 correction; silent when ok.
func warnSavedGlobalMaxToolIterations(d *AgentDefaults) {
	g := d.EffectiveGlobalMaxToolIterations()
	switch g.SavedState {
	case MaxToolIterationsSavedOK:
		return
	case MaxToolIterationsSavedMissing:
		logger.WarnF("max_tool_iterations: agents.defaults.max_tool_iterations is missing from config.json; "+
			"running with the default limit (config.json is not changed)",
			map[string]any{"in_force": g.Value, "saved_state": string(g.SavedState)})
	default:
		logger.WarnF("max_tool_iterations: saved global limit is outside 1-1000; "+
			"running with the corrected value (config.json is not changed)",
			map[string]any{"saved": g.SavedRaw, "in_force": g.Value, "saved_state": string(g.SavedState)})
	}
}

// parseMaxToolIterationsEnv reads the retired env var. ok is false when the
// variable is unset or empty (silent) or not a whole number (WARN, D17).
// Otherwise it returns the value clamped to 1..1000, with a WARN naming the
// original when clamping changed it (D7).
func parseMaxToolIterationsEnv() (int, bool) {
	raw, set := os.LookupEnv(MaxToolIterationsEnvVar)
	raw = strings.TrimSpace(raw)
	if !set || raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		logger.WarnF("max_tool_iterations: "+MaxToolIterationsEnvVar+" is not a whole number; "+
			"it is not imported (config.json is not changed)", map[string]any{"value": raw})
		return 0, false
	}
	clamped := min(max(n, MinMaxToolIterations), MaxMaxToolIterations)
	if clamped != n {
		logger.WarnF("max_tool_iterations: "+MaxToolIterationsEnvVar+" is outside 1-1000; "+
			"using the nearest bound", map[string]any{"value": n, "clamped": clamped})
	}
	return clamped, true
}

// importMaxToolIterationsEnv performs the one-time env import (D5-D7, D17).
// With the marker already set the env var is never read as a value again —
// only a note that it is ignored is logged. Otherwise a whole-number value is
// clamped, applied in memory, and written to config.json together with the
// marker through a raw-map patch (precedent migrateCLITokenOutOfUsers). A
// write failure leaves the in-memory import in place and no marker on disk,
// so the import is retried on the next load.
func importMaxToolIterationsEnv(cfg *Config, cfgPath string, onSelfHeal SelfHealWriteHook) {
	d := &cfg.Agents.Defaults
	if d.MaxToolIterationsEnvImported {
		if v, set := os.LookupEnv(MaxToolIterationsEnvVar); set && strings.TrimSpace(v) != "" {
			logger.WarnF("max_tool_iterations: "+MaxToolIterationsEnvVar+" is ignored — it was imported "+
				"into config.json earlier; change the limit in Settings → Performance and unset the variable",
				map[string]any{"env_value": v, "in_force": d.EffectiveGlobalMaxToolIterations().Value})
		}
		return
	}
	n, ok := parseMaxToolIterationsEnv()
	if !ok {
		return
	}
	d.MaxToolIterations = n
	d.MaxToolIterationsEnvImported = true
	d.MaxToolIterationsKeyMissing = false

	written, err := importMaxToolIterationsEnvOnDisk(cfgPath, n)
	if err != nil {
		logger.WarnF("max_tool_iterations: could not save the imported "+MaxToolIterationsEnvVar+
			" value to config.json; it applies for this run and the import is retried on the next load",
			map[string]any{"path": cfgPath, "value": n, "error": err.Error()})
		return
	}
	logger.InfoF("max_tool_iterations: copied "+MaxToolIterationsEnvVar+" into config.json; "+
		"the environment variable is no longer used", map[string]any{"value": n})
	if written != nil && onSelfHeal != nil {
		onSelfHeal(written)
	}
}

// importMaxToolIterationsEnvOnDisk sets agents.defaults.max_tool_iterations
// and agents.defaults.max_tool_iterations_env_imported in config.json,
// leaving every other key untouched (numbers kept verbatim via UseNumber).
func importMaxToolIterationsEnvOnDisk(path string, value int) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config for env import: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if decErr := dec.Decode(&m); decErr != nil {
		return nil, fmt.Errorf("parse config for env import: %w", decErr)
	}
	agents, _ := m["agents"].(map[string]any)
	if agents == nil {
		agents = map[string]any{}
		m["agents"] = agents
	}
	defaults, _ := agents["defaults"].(map[string]any)
	if defaults == nil {
		defaults = map[string]any{}
		agents["defaults"] = defaults
	}
	defaults["max_tool_iterations"] = value
	defaults["max_tool_iterations_env_imported"] = true

	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("serialize config for env import: %w", err)
	}
	if writeErr := fileutil.WriteFileAtomic(path, out, 0o600); writeErr != nil {
		return nil, fmt.Errorf("write config for env import: %w", writeErr)
	}
	return out, nil
}
