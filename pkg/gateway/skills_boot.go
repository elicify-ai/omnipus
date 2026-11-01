package gateway

// The skills start-up step (skills spec r15 §8.5, S-7.13–S-7.14): §9.3
// recovery of interrupted changes, then §8.2 SyncBuiltins (the shipped
// built-ins replace the old boot-time seeding), then §8.4 retirement — inside
// loadConfigAndProvider right after seedAndPersistAgentRoster, before any
// agent is built, so the first live instance never holds a retired grant.
// The facts are kept in skillsBootReport and replayed through the real audit
// logger once it exists (initializeAgentLoop, right after NewAgentLoop wires
// it) — replayBootSkillAudit.

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// skillsBootReport keeps this start's SyncReport so the audit replay at the
// wiring point can emit it (§8.5 audit-delivery rule). One-shot per start:
// the replay clears it.
var skillsBootReport skills.SyncReport

// bootSkills is the start-up step (§8.5). It never stops the gateway: the
// caller logs a returned error and the step is retried at the next start.
// The report carries everything that did happen, even when an error is
// returned.
func bootSkills(cfg *config.Config, homePath string) (skills.SyncReport, error) {
	root := filepath.Join(homePath, "skills")
	var errs []error
	if err := skills.RecoverInterrupted(root, skills.SkillHistoryRoot(root)); err != nil {
		errs = append(errs, fmt.Errorf("recover interrupted skill changes: %w", err))
	}
	editor := newSkillGrantEditor(agentstore.New(homePath), cfg)
	report, err := skills.SyncBuiltins(root, editor)
	if err != nil {
		errs = append(errs, fmt.Errorf("sync built-in skills: %w", err))
	}
	if err := skills.RetireBuiltins(root, editor, &report); err != nil {
		errs = append(errs, fmt.Errorf("retire built-in skills: %w", err))
	}
	for i := range report.Retired {
		report.Retired[i].AgentsChanged = editor.agentsChanged(report.Retired[i].Slug)
	}
	skillsBootReport = report
	return report, errors.Join(errs...)
}

// replayBootSkillAudit emits the boot report's facts (skill.builtin_updated,
// skill.builtin_retired, skill.builtin_retired_kept — §5.12) through the
// normal audit path. A nil logger means audit is disabled by the operator:
// nothing is emitted and nothing is counted. A failed write is counted on
// the audit-degraded counter (one per lost event) and returned; it never
// undoes what the start-up step did.
func replayBootSkillAudit(report skills.SyncReport, l *audit.Logger) error {
	if l == nil {
		return nil
	}
	now := time.Now().UTC()
	var entries []*audit.Entry
	for _, out := range report.Updated {
		entries = append(entries, &audit.Entry{
			Timestamp: now, Event: audit.EventSkillBuiltinUpdated, Decision: audit.DecisionAllow,
			Details: map[string]any{"slug": out.Slug, "old_revision": out.OldRev, "new_revision": out.NewRev},
		})
	}
	for _, out := range report.Retired {
		if out.Kept {
			entries = append(entries, &audit.Entry{
				Timestamp: now, Event: audit.EventSkillBuiltinRetiredKept, Decision: audit.DecisionAllow,
				Details: map[string]any{"slug": out.Slug},
			})
			continue
		}
		agents := out.AgentsChanged
		if agents == nil {
			agents = []string{}
		}
		entries = append(entries, &audit.Entry{
			Timestamp: now, Event: audit.EventSkillBuiltinRetired, Decision: audit.DecisionAllow,
			Details: map[string]any{"slug": out.Slug, "folder_deleted": out.FolderDeleted, "agents_changed": agents},
		})
	}
	var errs []error
	for _, entry := range entries {
		if err := l.Log(entry); err != nil {
			audit.IncSkipped("skills_boot", audit.DecisionAllow)
			slog.Error("gateway: skills start-up audit line lost", "event", entry.Event, "slug", entry.Details["slug"], "error", err)
			errs = append(errs, fmt.Errorf("%s %v: %w", entry.Event, entry.Details["slug"], err))
		}
	}
	return errors.Join(errs...)
}

// skillGrantEditor is the gateway's skills.GrantEditor (§8.2): it removes a
// name (by skill key) from every agent's grant list — in each stored record
// through agentstore.RemoveSkillGrant (which changes only that grant and
// takes no skills mutation lock, so it is safe under the lock the caller
// holds) and, when cfg is set, in the in-memory roster. It remembers which
// agents changed per name for the start-up report.
type skillGrantEditor struct {
	store *agentstore.Store
	cfg   *config.Config

	mu      sync.Mutex
	changed map[string]map[string]struct{}
}

func newSkillGrantEditor(store *agentstore.Store, cfg *config.Config) *skillGrantEditor {
	return &skillGrantEditor{store: store, cfg: cfg, changed: map[string]map[string]struct{}{}}
}

// RemoveGrant implements skills.GrantEditor. Idempotent.
func (e *skillGrantEditor) RemoveGrant(name string) error {
	key := skills.SkillKey(name)
	agents, skipped, err := e.store.List()
	if err != nil {
		return err
	}
	var errs []error
	for _, a := range agents {
		changed, err := e.store.RemoveSkillGrant(a.ID, name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if changed {
			e.record(key, a.ID)
		}
	}
	if len(skipped) > 0 {
		errs = append(errs, fmt.Errorf("agent records that could not be read keep their %q grant: %v", name, skipped))
	}
	if e.cfg != nil {
		for i := range e.cfg.Agents.List {
			if removeGrantInMemory(&e.cfg.Agents.List[i], key) {
				e.record(key, e.cfg.Agents.List[i].ID)
			}
		}
	}
	return errors.Join(errs...)
}

func (e *skillGrantEditor) record(key, agentID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.changed[key] == nil {
		e.changed[key] = map[string]struct{}{}
	}
	e.changed[key][agentID] = struct{}{}
}

// agentsChanged returns the sorted ids whose grant for name was removed.
func (e *skillGrantEditor) agentsChanged(name string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	ids := make([]string, 0, len(e.changed[skills.SkillKey(name)]))
	for id := range e.changed[skills.SkillKey(name)] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// removeGrantInMemory drops key from one in-memory agent config.
func removeGrantInMemory(a *config.AgentConfig, key string) bool {
	changed := false
	kept := a.Skills[:0:0]
	for _, granted := range a.Skills {
		if skills.SkillKey(granted) == key {
			changed = true
			continue
		}
		kept = append(kept, granted)
	}
	a.Skills = kept
	if _, bound := a.SkillGrants[key]; bound {
		changed = true
		delete(a.SkillGrants, key)
	}
	return changed
}
