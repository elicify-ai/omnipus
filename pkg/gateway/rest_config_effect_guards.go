// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_config_effect_guards.go — the generic PUT /api/v1/config write is
// refused, by EFFECT, when its merged result would change a value that has
// its own dedicated write path (Settings redesign #1519):
//
//   - retention (C-3 / FR-044): only PUT /api/v1/settings/retention;
//   - Performance values (C-7 / #936 / FR-100): only PUT /api/v1/performance;
//   - the three retired tool-loading keys (Skills & Tools C-22 row 3, C-21):
//     tools.manifest.compressed, tools.manifest.preview_all_lazy and
//     tools.mcp.servers.<id>.deferred have no write path at all. The refusal
//     is by name (team-lead ruling 2026-11-28, N83): a body that names one of
//     them with a value other than the stored one is refused. A body that
//     repeats the stored value (a whole-config round trip) is accepted, and so
//     is one that only drops a stored value without naming it.
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
	// retiredToolLoadingRefusal is C-22 row 3's text, character for character.
	retiredToolLoadingRefusal = "This setting was replaced by levels in Skills & Tools."
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
}

// retiredToolLoadingEffect is the stored state of the three retired
// tool-loading keys. A server's deferred is kept as its tri-state value
// ("" unset, "true", "false"): unset and false are different stored values.
type retiredToolLoadingEffect struct {
	manifestCompressed     bool
	manifestPreviewAllLazy bool
	serverDeferred         map[string]string
}

// retiredToolLoadingNamed is the value a PUT /config body gives each retired
// key it names, in the stored effect's form: "true"/"false" for a boolean, ""
// for a JSON null (which leaves a manifest switch as it is and unsets a
// server's deferred), and anything else as its JSON text, which no stored
// value equals.
type retiredToolLoadingNamed struct {
	manifestCompressed     *string
	manifestPreviewAllLazy *string
	serverDeferred         map[string]string
}

// namedRetiredToolLoadingKeys walks body the way encoding/json binds it
// (field names case-folded with jsonFoldKey, server ids exact) and returns
// the value it gives each retired key it names. The body is read on its own,
// not through the merge: a case variant of a section key is stored as its
// own top-level key and would take effect later, so it counts as named.
func namedRetiredToolLoadingKeys(body map[string]any) retiredToolLoadingNamed {
	named := retiredToolLoadingNamed{serverDeferred: map[string]string{}}
	for _, tools := range foldedObjects(body, "tools") {
		for _, manifest := range foldedObjects(tools, "manifest") {
			for k, v := range manifest {
				switch jsonFoldKey(k) {
				case jsonFoldKey("compressed"):
					named.manifestCompressed = namedValue(v)
				case jsonFoldKey("preview_all_lazy"):
					named.manifestPreviewAllLazy = namedValue(v)
				}
			}
		}
		for _, mcp := range foldedObjects(tools, "mcp") {
			for _, servers := range foldedObjects(mcp, "servers") {
				for id, v := range servers {
					server, _ := v.(map[string]any)
					for k, dv := range server {
						if jsonFoldKey(k) == jsonFoldKey("deferred") {
							named.serverDeferred[id] = *namedValue(dv)
						}
					}
				}
			}
		}
	}
	return named
}

// foldedObjects returns every object value of m whose key folds to name.
func foldedObjects(m map[string]any, name string) []map[string]any {
	var out []map[string]any
	for k, v := range m {
		if jsonFoldKey(k) != jsonFoldKey(name) {
			continue
		}
		if obj, ok := v.(map[string]any); ok {
			out = append(out, obj)
		}
	}
	return out
}

// namedValue renders a body value in the stored effect's form.
func namedValue(v any) *string {
	var s string
	switch x := v.(type) {
	case nil:
		s = ""
	case bool:
		s = fmt.Sprint(x)
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			s = fmt.Sprintf("%#v", x)
		} else {
			s = string(raw)
		}
	}
	return &s
}

// changesNamed reports whether the body gives a retired key it names a value
// other than the stored one. A JSON null leaves a manifest switch as it is.
func (n retiredToolLoadingNamed) changesNamed(before retiredToolLoadingEffect) bool {
	if n.manifestCompressed != nil && *n.manifestCompressed != "" &&
		*n.manifestCompressed != fmt.Sprint(before.manifestCompressed) {
		return true
	}
	if n.manifestPreviewAllLazy != nil && *n.manifestPreviewAllLazy != "" &&
		*n.manifestPreviewAllLazy != fmt.Sprint(before.manifestPreviewAllLazy) {
		return true
	}
	for id, v := range n.serverDeferred {
		if v != before.serverDeferred[id] {
			return true
		}
	}
	return false
}

// configEffect is what the guards compare before and after a merge.
type configEffect struct {
	retention   retentionEffect
	performance performanceEffect
	retired     retiredToolLoadingEffect
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
	deferred := make(map[string]string, len(cfg.Tools.MCP.Servers))
	for id, server := range cfg.Tools.MCP.Servers {
		deferred[id] = ""
		if server.Deferred != nil {
			deferred[id] = fmt.Sprint(*server.Deferred)
		}
	}
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
		},
		retired: retiredToolLoadingEffect{
			manifestCompressed:     cfg.Tools.Manifest.Compressed,
			manifestPreviewAllLazy: cfg.Tools.Manifest.PreviewAllLazy,
			serverDeferred:         deferred,
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
// that does not decode, that changes an effective retention or Performance
// value, or that changes a retired tool-loading key the body names.
func checkConfigEffectUnchanged(before configEffect, merged, body map[string]any) error {
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
	if namedRetiredToolLoadingKeys(body).changesNamed(before.retired) {
		return &requestRefusalError{msg: retiredToolLoadingRefusal}
	}
	return nil
}
