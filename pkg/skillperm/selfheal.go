// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skillperm

// Skills & Tools C-2 self-heal of the two skill maps, the skills twin of
// ReconcileToolPolicyCeiling. It runs on every config load once the
// one-time upgrade (Run, C-19) is done, again at every start after the
// built-in sync, and at a turn start that finds a project skill with no
// stored key (C-19: the key is created when Omnipus first reads the
// workspace's project shelf). It also removes the project keys of a deleted
// workspace or a removed project folder (project_keys_gone.go, FR-080). It
// is otherwise additive and never overwrites: a skill key with no
// stored permission gets ask (ST-Q3 B), except a built-in skill shipped by
// a newer release, which gets allow, its shipped default (ST-Q10 A); a key
// with no stored level gets name_and_description.
//
// "Shipped by a newer release" needs a memory of the built-ins this home
// already had. It is kept in system/known-builtin-skills.json. The first
// heal that finds no record writes it from the built-ins that already hold
// a stored permission (the upgrade or an earlier heal gave them one),
// before config.json is touched. The record grows to the installed
// built-ins only after config.json holds the healed maps, so a failed
// config write is healed the same way at the next load or start, and a
// built-in the sync has not installed yet is still new when it arrives.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

const knownBuiltinsName = "known-builtin-skills.json"

type knownBuiltinsRecord struct {
	Keys []string `json:"keys"`
}

func init() {
	config.RegisterSkillMapHeal(HealIfUpgraded)
}

// HealIfUpgraded is the heal run by every config load and by the turn
// start that first reads a project shelf (C-19). The config.json at
// configPath lives in the home it heals. Before the C-19 upgrade is done
// it does nothing: the upgrade runs first and gives the existing skills
// their keys.
func HealIfUpgraded(configPath string) (policies, visibility map[string]string, written []byte, err error) {
	return HealIfUpgradedIn(filepath.Dir(configPath), configPath, skills.DefaultSkillNames())
}

// HealIfUpgradedIn is HealIfUpgraded for a home and built-in list named by
// the caller (the gateway start). Before the C-19 upgrade is done it returns
// nil maps and writes nothing (spec line 154: the heal runs after the
// one-time migrations).
func HealIfUpgradedIn(home, configPath string, builtins []string) (policies, visibility map[string]string, written []byte, err error) {
	raw, err := readJSONMap(configPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("skillperm: self-heal: %w", err)
	}
	if childMap(childMap(raw, "sandbox"), "migrations")[MigrationName] != stateDone {
		return nil, nil, nil, nil
	}
	return SelfHeal(home, configPath, builtins)
}

// SelfHeal runs the C-2 self-heal over the skills of home and writes the
// added keys into the config.json at configPath. builtins are the current
// built-in skill names. It returns the stored skill_policies and
// skill_visibility maps after the heal, for the caller's in-memory config,
// and the bytes written to config.json (nil when nothing was added).
func SelfHeal(home, configPath string, builtins []string) (policies, visibility map[string]string, written []byte, err error) {
	cfg, err := readJSONMap(configPath)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("skillperm: self-heal: %w", err)
	}
	sandbox := ensureMap(cfg, "sandbox")
	rawPolicies := ensureMap(sandbox, "skill_policies")
	rawVisibility := ensureMap(sandbox, "skill_visibility")

	recordPath := filepath.Join(home, "system", knownBuiltinsName)
	known, err := loadOrSeedKnownBuiltins(recordPath, builtins, rawPolicies)
	if err != nil {
		return nil, nil, nil, err
	}
	current := keySet(builtins)
	installed := map[string]bool{}
	installedBuiltins := map[string]struct{}{}
	for _, k := range installedSkills(home) {
		_, isBuiltin := current[k]
		_, wasKnown := known[k]
		installed[k] = isBuiltin && !wasKnown
		if isBuiltin {
			installedBuiltins[k] = struct{}{}
		}
	}
	for _, keys := range projectSkillsByWorkspace(home) {
		for _, k := range keys {
			installed[k] = false
		}
	}

	removed, err := removeGoneProjectKeys(home, rawPolicies, rawVisibility)
	if err != nil {
		return nil, nil, nil, err
	}
	healedPolicies, healedVisibility := config.ReconcileSkillPolicyMaps(
		stringValues(rawPolicies), stringValues(rawVisibility), installed)
	added := addMissing(rawPolicies, healedPolicies)
	added = addMissing(rawVisibility, healedVisibility) || added
	if added || removed {
		written, err = json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			return nil, nil, nil, fmt.Errorf("skillperm: self-heal: encode %s: %w", filepath.Base(configPath), err)
		}
		if err := fileutil.WriteFileAtomic(configPath, written, 0o600); err != nil {
			return nil, nil, nil, fmt.Errorf("skillperm: self-heal: write %s: %w", filepath.Base(configPath), err)
		}
	}

	if err := growKnownBuiltins(recordPath, known, installedBuiltins); err != nil {
		return nil, nil, nil, err
	}
	return healedPolicies, healedVisibility, written, nil
}

// loadOrSeedKnownBuiltins reads the record, or writes the first one from the
// built-ins that already hold a stored permission.
func loadOrSeedKnownBuiltins(path string, builtins []string, storedPolicies map[string]any) (map[string]struct{}, error) {
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
	known := map[string]struct{}{}
	for k := range keySet(builtins) {
		if _, stored := storedPolicies[k]; stored {
			known[k] = struct{}{}
		}
	}
	if err := writeKnownBuiltins(path, known); err != nil {
		return nil, err
	}
	return known, nil
}

// growKnownBuiltins adds every installed built-in to the record.
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
