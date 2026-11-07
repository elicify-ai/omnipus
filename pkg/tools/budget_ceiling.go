// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

// budget_ceiling.go — the agent-caller budget ceiling (tool-parity spec r17
// §7.2 TK-05, §7.3 PL-08, §7.11, dataset D-3). For an agent caller every
// resulting retry limit or plan bound must be at or below its EFFECTIVE
// global value, read live at call time (never captured at wiring): the
// ceiling wins over "lowering" (MIN-003). Clearing to inherit is always
// accepted. People are uncapped; they never reach these tools.

import (
	"fmt"
	"math"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/plan"
)

// PlanningConfigFn returns the live planning configuration. A nil function
// resolves every limit to its shipped default (config.PlanningConfig's zero
// value), which is what a tool constructed without wiring sees.
type PlanningConfigFn func() config.PlanningConfig

func livePlanning(fn PlanningConfigFn) config.PlanningConfig {
	if fn == nil {
		return config.PlanningConfig{}
	}
	return fn()
}

// aboveGlobalLimitText is the §7.11 refusal, byte for byte, for max_attempts
// and each plan bound.
func aboveGlobalLimitText(field string, value, global int) string {
	return fmt.Sprintf("%s %d is above the global limit of %d. Use %d or less.", field, value, global, global)
}

// wholeNumberArg reads a JSON number argument that must be a whole number of
// at least 1. present is false when the key is absent or null.
func wholeNumberArg(args map[string]any, key string) (value int, present bool, refusal string) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return 0, false, ""
	}
	var f float64
	switch n := raw.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	default:
		return 0, true, fmt.Sprintf("%s must be a whole number of at least 1", key)
	}
	if f != math.Trunc(f) || f < 1 || f > math.MaxInt32 {
		return 0, true, fmt.Sprintf("%s must be a whole number of at least 1", key)
	}
	return int(f), true, ""
}

// planBoundFields lists the four plan bounds with their live global and the
// field they set, in a fixed order so a refusal is deterministic.
type planBoundField struct {
	name   string
	global func(config.PlanningConfig) int
	slot   func(*plan.PlanBounds) **int
}

var planBoundFields = []planBoundField{
	{"plan_judge_max_rounds", func(c config.PlanningConfig) int { return c.EffectivePlanJudgeMaxRounds(nil) },
		func(b *plan.PlanBounds) **int { return &b.PlanJudgeMaxRounds }},
	{"idle_expiry_days", func(c config.PlanningConfig) int { return c.EffectiveIdleExpiryDays(nil) },
		func(b *plan.PlanBounds) **int { return &b.IdleExpiryDays }},
	{"supervision_turn_timeout_seconds", func(c config.PlanningConfig) int { return c.EffectiveSupervisionTurnTimeoutSeconds(nil) },
		func(b *plan.PlanBounds) **int { return &b.SupervisionTurnTimeoutSeconds }},
	{"supervision_max_attempts", func(c config.PlanningConfig) int { return c.EffectiveSupervisionMaxAttempts(nil) },
		func(b *plan.PlanBounds) **int { return &b.SupervisionMaxAttempts }},
}

// planBoundsParamSchema is the shared `bounds` parameter of create_plan and
// update_plan.
func planBoundsParamSchema() map[string]any {
	props := make(map[string]any, len(planBoundFields))
	for _, f := range planBoundFields {
		props[f.name] = map[string]any{"type": "integer", "minimum": 1}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
		"description": "Per-plan limits, merged field by field into the stored bounds (an omitted field keeps " +
			"its value). Each value must be at least 1 and at most its global limit in Settings; one value " +
			"above its global limit refuses the whole call.",
	}
}

// mergePlanBounds overlays the supplied bounds argument onto existing (which
// is not modified) and checks each value the call supplies against its live
// global; one value above its global refuses the whole call.
func mergePlanBounds(existing *plan.PlanBounds, raw any, planning config.PlanningConfig) (*plan.PlanBounds, string) {
	arg, ok := raw.(map[string]any)
	if !ok {
		return nil, "bounds must be an object"
	}
	merged := &plan.PlanBounds{}
	if existing != nil {
		*merged = *existing
	}
	known := make(map[string]bool, len(planBoundFields))
	for _, f := range planBoundFields {
		known[f.name] = true
		value, present, refusal := wholeNumberArg(arg, f.name)
		if refusal != "" {
			return nil, "bounds." + refusal
		}
		if !present {
			continue
		}
		// Only the values this call sets are checked: a person's earlier,
		// higher value on another field is theirs and stays (people are
		// uncapped); the agent's own value must sit at or below the global.
		if g := f.global(planning); value > g {
			return nil, aboveGlobalLimitText(f.name, value, g)
		}
		v := value
		*f.slot(merged) = &v
	}
	for key := range arg {
		if !known[key] {
			return nil, fmt.Sprintf("bounds has an unknown field %q", key)
		}
	}
	return merged, ""
}
