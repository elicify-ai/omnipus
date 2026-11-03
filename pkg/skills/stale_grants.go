// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

import (
	"errors"
	"path/filepath"
)

// StaleGrants returns, in the order given, the granted names whose skill is
// installed under root but whose binding (bindings: skill key → generation)
// does not match the folder's current generation — exactly the grants
// activation refuses with SKILL_GRANT_STALE (checkBinding, §5.10). A name
// whose skill is absent is not listed. The agent's REST view surfaces the
// list so the Agents screen can mark the stale rows (UAT-1 F-9).
func StaleGrants(root string, names []string, bindings map[string]string) ([]string, error) {
	if bindings == nil {
		bindings = map[string]string{} // an agent's table is strict (checkBinding)
	}
	var stale []string
	var errs []error
	for _, name := range names {
		if !ValidSlug(SkillKey(name)) {
			continue // no folder can carry an invalid name, so it is never installed
		}
		onDisk, present, err := ResolveInstalledSlug(root, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !present {
			continue
		}
		err = checkBinding(filepath.Join(root, onDisk), onDisk, bindings)
		switch {
		case err == nil:
		case errors.Is(err, ErrSkillGrantStale):
			stale = append(stale, name)
		default:
			errs = append(errs, err)
		}
	}
	return stale, errors.Join(errs...)
}
