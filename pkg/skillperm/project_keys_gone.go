// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skillperm

// Skills & Tools C-23 and FR-080: a deleted workspace or a removed project
// folder takes its project:<W>/<slug> keys out of sandbox.skill_policies,
// sandbox.skill_visibility and every agent's skills.policies at the next
// reconcile (the self-heal). A key is removed only on a complete answer: its
// workspace no longer exists, or every mount of the workspace was read in
// full and none holds the slug. A workspace whose mounts cannot be read in
// full keeps its keys.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// GoneProjectKeys returns the project keys among keys whose workspace or
// folder is gone, by the same complete-answer rule as the self-heal. The
// gateway uses it to drop those keys from the live agent copies in the step
// that heals the stored maps.
func GoneProjectKeys(home string, keys []string) (map[string]struct{}, error) {
	return goneProjectKeys(home, keys)
}

// goneProjectKeys returns the project keys among keys whose workspace or
// folder is gone. It fails when the workspace list cannot be read.
func goneProjectKeys(home string, keys []string) (map[string]struct{}, error) {
	byWorkspace := map[string][]string{}
	for _, k := range keys {
		if w, _, ok := config.ParseProjectSkillKey(k); ok {
			byWorkspace[w] = append(byWorkspace[w], k)
		}
	}
	gone := map[string]struct{}{}
	if len(byWorkspace) == 0 {
		return gone, nil
	}
	ids, err := workspace.ListWorkspaceIDs(home)
	if err != nil {
		return nil, fmt.Errorf("skillperm: list workspaces for project skill keys: %w", err)
	}
	exists := keySetRaw(ids)
	for w, wkeys := range byWorkspace {
		if _, ok := exists[w]; !ok {
			for _, k := range wkeys {
				gone[k] = struct{}{}
			}
			continue
		}
		present, complete := workspaceProjectKeys(home, w)
		if !complete {
			continue
		}
		for _, k := range wkeys {
			if _, ok := present[config.NormalizeSkillKey(k)]; !ok {
				gone[k] = struct{}{}
			}
		}
	}
	return gone, nil
}

// workspaceProjectKeys lists the project keys of workspace w's mounts.
// complete is false when a mount record or a mount could not be read in full.
func workspaceProjectKeys(home, w string) (map[string]struct{}, bool) {
	mounts, ok, dropped := workspace.LoadMountsWithDropStatus(home, w)
	if !ok || dropped {
		return nil, false
	}
	pm := make([]skills.ProjectMount, 0, len(mounts))
	for _, m := range mounts {
		pm = append(pm, skills.ProjectMount{Name: m.Name, Root: m.HostPath})
	}
	shelf, _, err := skills.MergeProjectSkillsChecked(pm)
	if err != nil {
		logger.WarnCF(logComponent, "project skill keys: a mount could not be read in full; its workspace keeps its keys",
			map[string]any{"workspace_id": w, "error": err.Error()})
		return nil, false
	}
	out := make(map[string]struct{}, len(shelf))
	for _, ps := range shelf {
		out[config.ProjectSkillKey(w, ps.ID)] = struct{}{}
	}
	return out, true
}

func keySetRaw(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// removeKeys deletes every key of gone from m and reports whether one was
// deleted.
func removeKeys(m map[string]any, gone map[string]struct{}) bool {
	removed := false
	for k := range m {
		if _, ok := gone[k]; ok {
			delete(m, k)
			removed = true
		}
	}
	return removed
}

// agentRecordFiles lists the agent record files of home, sorted.
func agentRecordFiles(home string) ([]string, error) {
	dir := filepath.Join(home, "entities", "agents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("skillperm: list agent records: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

// agentProjectKeys reads each agent record's skills.policies keys. A record
// that cannot be read or parsed is left out: the C-19 notice reports such
// records, and the heal changes nothing in them.
func agentProjectKeys(home string) (map[string]map[string]any, error) {
	files, err := agentRecordFiles(home)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, path := range files {
		rec, err := readJSONMap(path)
		if err != nil {
			logger.WarnCF(logComponent, "project skill keys: agent record unreadable; its keys are left as they are",
				map[string]any{"path": path, "error": err.Error()})
			continue
		}
		out[path] = rec
	}
	return out, nil
}

// agentPolicies returns the tools.skills.policies map of a raw agent record,
// or nil when it has none.
func agentPolicies(rec map[string]any) map[string]any {
	return childMap(childMap(childMap(rec, "tools"), "skills"), "policies")
}

// removeGoneProjectKeys removes the gone project keys from the two raw
// config maps and from every readable agent record (written at once). It
// reports whether a config map changed.
func removeGoneProjectKeys(home string, rawPolicies, rawVisibility map[string]any) (bool, error) {
	records, err := agentProjectKeys(home)
	if err != nil {
		return false, err
	}
	var keys []string
	for k := range rawPolicies {
		keys = append(keys, k)
	}
	for k := range rawVisibility {
		keys = append(keys, k)
	}
	for _, rec := range records {
		for k := range agentPolicies(rec) {
			keys = append(keys, k)
		}
	}
	gone, err := goneProjectKeys(home, keys)
	if err != nil || len(gone) == 0 {
		return false, err
	}
	paths := make([]string, 0, len(records))
	for p := range records {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		rec := records[path]
		if !removeKeys(agentPolicies(rec), gone) {
			continue
		}
		if err := writeJSONMap(path, rec); err != nil {
			return false, fmt.Errorf("skillperm: remove project skill keys from %s: %w", filepath.Base(path), err)
		}
	}
	changed := removeKeys(rawPolicies, gone)
	changed = removeKeys(rawVisibility, gone) || changed
	return changed, nil
}
