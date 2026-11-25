// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skillperm

// Skills & Tools C-2 self-heal of the two skill maps, run at every start
// after the one-time upgrade (Run, C-19) and after the built-in sync. It is
// additive and never overwrites: a skill key with no stored permission gets
// ask (ST-Q3 B), except a built-in skill shipped by a newer release, which
// gets allow, its shipped default (ST-Q10 A); a key with no stored level gets
// name_and_description.
//
// "Shipped by a newer release" needs a memory of the built-ins this home
// already had. It is kept in system/known-builtin-skills.json. The first
// start that finds no record writes it from the built-ins that were on disk
// before this start's sync (the current built-ins minus those the sync
// installed), before config.json is touched. The record grows to every
// current built-in only after config.json holds the healed maps, so a
// failed config write is healed the same way at the next start.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

const knownBuiltinsName = "known-builtin-skills.json"

type knownBuiltinsRecord struct {
	Keys []string `json:"keys"`
}

// SelfHeal runs the C-2 self-heal over the skills of home and writes the
// added keys into the config.json at configPath. builtins are the current
// built-in skill names; installedThisStart are the built-ins this start's
// sync installed. It returns the stored skill_policies and skill_visibility
// maps after the heal, for the caller's in-memory config.
func SelfHeal(home, configPath string, builtins, installedThisStart []string) (policies, visibility map[string]string, err error) {
	recordPath := filepath.Join(home, "system", knownBuiltinsName)
	known, err := loadOrSeedKnownBuiltins(recordPath, builtins, installedThisStart)
	if err != nil {
		return nil, nil, err
	}
	current := keySet(builtins)
	installed := map[string]bool{}
	for _, k := range installedSkills(home) {
		_, isBuiltin := current[k]
		_, wasKnown := known[k]
		installed[k] = isBuiltin && !wasKnown
	}
	for _, keys := range projectSkillsByWorkspace(home) {
		for _, k := range keys {
			installed[k] = false
		}
	}

	cfg, err := readJSONMap(configPath)
	if err != nil {
		return nil, nil, fmt.Errorf("skillperm: self-heal: %w", err)
	}
	sandbox := ensureMap(cfg, "sandbox")
	rawPolicies := ensureMap(sandbox, "skill_policies")
	rawVisibility := ensureMap(sandbox, "skill_visibility")
	healedPolicies, healedVisibility := config.ReconcileSkillPolicyMaps(
		stringValues(rawPolicies), stringValues(rawVisibility), installed)
	added := addMissing(rawPolicies, healedPolicies)
	added = addMissing(rawVisibility, healedVisibility) || added
	if added {
		if err := writeJSONMap(configPath, cfg); err != nil {
			return nil, nil, fmt.Errorf("skillperm: self-heal: %w", err)
		}
	}

	if err := growKnownBuiltins(recordPath, known, current); err != nil {
		return nil, nil, err
	}
	return healedPolicies, healedVisibility, nil
}

// loadOrSeedKnownBuiltins reads the record, or writes the first one from the
// built-ins this home had before this start's sync.
func loadOrSeedKnownBuiltins(path string, builtins, installedThisStart []string) (map[string]struct{}, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		var rec knownBuiltinsRecord
		if parseErr := json.Unmarshal(raw, &rec); parseErr != nil {
			return nil, fmt.Errorf("skillperm: parse %s: %w", knownBuiltinsName, parseErr)
		}
		return keySet(rec.Keys), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("skillperm: read %s: %w", knownBuiltinsName, err)
	}
	known := keySet(builtins)
	for k := range keySet(installedThisStart) {
		delete(known, k)
	}
	if err := writeKnownBuiltins(path, known); err != nil {
		return nil, err
	}
	return known, nil
}

// growKnownBuiltins adds every current built-in to the record.
func growKnownBuiltins(path string, known, current map[string]struct{}) error {
	grown := false
	for k := range current {
		if _, ok := known[k]; !ok {
			known[k] = struct{}{}
			grown = true
		}
	}
	if !grown {
		return nil
	}
	return writeKnownBuiltins(path, known)
}

func writeKnownBuiltins(path string, known map[string]struct{}) error {
	keys := sortedKeys(known)
	raw, err := json.MarshalIndent(knownBuiltinsRecord{Keys: keys}, "", "  ")
	if err != nil {
		return fmt.Errorf("skillperm: encode %s: %w", knownBuiltinsName, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("skillperm: create %s: %w", filepath.Dir(path), err)
	}
	if err := fileutil.WriteFileAtomic(path, raw, 0o600); err != nil {
		return fmt.Errorf("skillperm: write %s: %w", knownBuiltinsName, err)
	}
	return nil
}

func keySet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		if k := config.NormalizeSkillKey(n); k != "" {
			out[k] = struct{}{}
		}
	}
	return out
}

// stringValues copies the string entries of a raw config map; a key whose
// value is not a string is kept with an empty value so the heal never
// replaces it.
func stringValues(m map[string]any) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		s, _ := v.(string)
		out[k] = s
	}
	return out
}

// addMissing writes into raw every healed key raw does not hold and reports
// whether it added one.
func addMissing(raw map[string]any, healed map[string]string) bool {
	keys := make([]string, 0, len(healed))
	for k := range healed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	added := false
	for _, k := range keys {
		if _, ok := raw[k]; !ok {
			raw[k] = healed[k]
			added = true
		}
	}
	return added
}
