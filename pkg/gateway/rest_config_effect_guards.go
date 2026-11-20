// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_config_effect_guards.go — the generic PUT /api/v1/config write is
// refused, by EFFECT, when its merged result would change a value that has
// its own dedicated write path (Settings redesign #1519):
//
//   - retention (C-3 / FR-044): only PUT /api/v1/settings/retention;
//   - Performance values (C-7 / #936 / FR-100): only PUT /api/v1/performance.
//
// blockedPaths cannot do this job: collectPaths sees only the paths present in
// the body, so {"storage": null} or a replaced storage.retention object would
// slip through, and updateConfig's one-level merge replaces whole sub-objects.
// So the guard decodes the stored map and the merged map the way config.json
// loads (DefaultConfig, then encoding/json over the bytes json.Marshal writes
// with sorted keys — every key spelling the loader accepts is covered) and
// compares the effective values. Same pattern as the #904 tool-iteration
// guard (rest_config_max_tool_iterations.go).

import (
	"encoding/json"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

const (
	retentionGuardRefusal   = "retention settings can only be changed through PUT /api/v1/settings/retention"
	performanceGuardRefusal = "performance settings can only be changed through PUT /api/v1/performance"
)

// retentionEffect is the four effective retention values FR-044 protects.
type retentionEffect struct {
	sessionDays      int
	memoryRetrosDays int
	memoryForever    bool
	keepEverything   bool
}

// performanceEffect is the effective Performance values FR-100 protects
// (agents.defaults.max_tool_iterations has its own #904 guard).
type performanceEffect struct {
	maxParallelAgents         int
	goalMaxRounds             int
	goalReminderQuietSeconds  int
	goalStalledCheckinEnabled bool
	goalStalledCheckinMinutes int
	manifestCompressed        bool
}

// configEffect is what the guards compare before and after a merge.
type configEffect struct {
	retention   retentionEffect
	performance performanceEffect
}

// decodeConfigEffect decodes m the way config.json is loaded and returns the
// protected effective values.
func decodeConfigEffect(m map[string]any) (configEffect, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return configEffect{}, err
	}
	cfg := config.DefaultConfig()
	if err := json.Unmarshal(raw, cfg); err != nil {
		return configEffect{}, err
	}
	ret := cfg.Storage.Retention
	maxParallel := cfg.Performance.MaxParallelAgents
	if maxParallel < 0 {
		maxParallel = 0 // ≤ 0 means "not configured", like 0
	}
	return configEffect{
		retention: retentionEffect{
			sessionDays:      ret.RetentionSessionDays(),
			memoryRetrosDays: ret.RetentionMemoryRetrosDays(),
			memoryForever:    ret.MemoryRetrosForever,
			keepEverything:   ret.IsDisabled(),
		},
		performance: performanceEffect{
			maxParallelAgents:         maxParallel,
			goalMaxRounds:             cfg.Planning.EffectiveGoalMaxRounds(),
			goalReminderQuietSeconds:  cfg.Planning.EffectiveGoalReminderQuietSeconds(),
			goalStalledCheckinEnabled: cfg.Planning.EffectiveGoalStalledCheckinEnabled(),
			goalStalledCheckinMinutes: cfg.Planning.EffectiveGoalStalledCheckinMinutes(),
			manifestCompressed:        cfg.Tools.Manifest.Compressed,
		},
	}, nil
}

// storedConfigEffect takes the baseline from config.json's stored map before
// the merge. A stored file that no longer decodes has no baseline: the write
// fails (500) rather than guessing.
func storedConfigEffect(m map[string]any) (configEffect, error) {
	before, err := decodeConfigEffect(m)
	if err != nil {
		return configEffect{}, fmt.Errorf("stored config does not decode: %w", err)
	}
	return before, nil
}

// checkConfigEffectUnchanged refuses (400, nothing written) a merged result
// that does not decode, or that changes an effective retention or
// Performance value.
func checkConfigEffectUnchanged(before configEffect, merged map[string]any) error {
	after, err := decodeConfigEffect(merged)
	if err != nil {
		return &requestRefusalError{msg: "this write does not decode: " + err.Error()}
	}
	if after.retention != before.retention {
		return &requestRefusalError{msg: retentionGuardRefusal}
	}
	if after.performance != before.performance {
		return &requestRefusalError{msg: performanceGuardRefusal}
	}
	return nil
}
