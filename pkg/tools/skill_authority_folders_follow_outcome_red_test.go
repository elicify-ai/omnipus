//go:build goolm && stdjson

package tools

// Order 33 — TestSkillAuthority_FoldersFollowOutcome (S-85, S-86, S-87).
// Oracle: skills-tools-redesign-spec.md S-85 ("A denied skill's files cannot
// be read or run by the agent … the kernel grant list for Mia's turn does
// not contain the notes-helper folder"), S-86 ("An Ask skill's files open
// only after approval"), S-87 ("An editor agent can read but not use a
// denied skill"), traced through CRIT-002: denied and unapproved skill
// folders leave the loadable set in the app guard and the kernel grant.
//
// Production path: pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority
// → skills.LoadableFolders — the file's own doc states ResolveTurnFSPolicy
// is the ONLY place HomeGuard.SkillFolders (app guard) and the kernel grant
// list are filled, so asserting the resolved folders covers both legs (E6/E7).
//
// RED today: the loadable set filters by grant and binding only —
// skills.LoadableFolders has no skill-outcome input, and the skill-outcome
// storage it must consult does not exist (config.AgentToolsCfg has no Skills
// member; pkg/config's SkillsToolsConfig carries no policy map; the C-4
// envelope's skills member is order 14's RED). The one seam below is that
// missing storage WRITE, not a parallel filter: GREEN wires it to the real
// skill-outcome storage and makes resolveTurnSkillAuthority consult it, and
// every assertion here runs against the real authority resolution.
import (
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// seedSkillOutcomeForAgent is the test-local seam for the C-2/C-4 skill
// outcome storage. GREEN must wire it to the production write path — the
// agent's stored skill policy (C-4: agent.tools.skills.policies) with the
// approval state of C-12/C-26 — and GREEN must make
// resolveTurnSkillAuthority consult that stored outcome. Nil keeps the RED
// loud (t.Fatal BLOCKED); it is never a skip.
var seedSkillOutcomeForAgent func(t testing.TB, home, agentID, skill, policy string, approved bool)

func requireOutcomeSeam(t *testing.T) {
	t.Helper()
	nativebinding.Require(t, "pkg/tools", seedSkillOutcomeForAgent,
		"C-2/C-4/C-12/C-26/S-85/S-86/S-87: actual stored consent input; actual folder authority is the observer")
}

// seedAuthorityHome writes a minimal readable config (the ceiling read
// fails closed without one), the skills root with one skill, and an agent
// record granting it. It returns the folder the authority must decide about.
func seedAuthorityHome(t *testing.T, agentID string, grants []string) (home, folder string) {
	t.Helper()
	home = t.TempDir()
	cfgDir := filepath.Join(home, "config.json")
	minimal := `{"sandbox":{"tool_policies":{"edit_skill":"allow"}}}`
	if err := os.WriteFile(cfgDir, []byte(minimal), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	folder = filepath.Join(home, "skills", "notes-helper")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(folder, "SKILL.md"), []byte("---\nname: notes-helper\ndescription: Notes helper.\n---\n\nBody."), 0o600); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	rec := &config.AgentConfig{ID: agentID, Skills: grants}
	if err := agentstore.New(home).Create(agentID, rec); err != nil {
		t.Fatalf("create agent record: %v", err)
	}
	return home, folder
}

func TestSkillAuthority_FoldersFollowOutcome(t *testing.T) {
	const agentID = "agent-mia"

	t.Run("control: a granted skill with no adverse outcome is loadable", func(t *testing.T) {
		home, folder := seedAuthorityHome(t, agentID, []string{"notes-helper"})
		auth := resolveTurnSkillAuthority(home, agentID)
		if len(auth.folders) != 1 || auth.folders[0] != folder {
			t.Fatalf("control: loadable set = %v, want exactly %v (harness must see the granted folder before the outcome legs mean anything)", auth.folders, folder)
		}
	})

	t.Run("S-85: a denied skill's folder leaves the loadable set (app guard and kernel grant)", func(t *testing.T) {
		home, folder := seedAuthorityHome(t, agentID, []string{"notes-helper"})
		requireOutcomeSeam(t)
		seedSkillOutcomeForAgent(t, home, agentID, "notes-helper", "deny", false)
		auth := resolveTurnSkillAuthority(home, agentID)
		for _, f := range auth.folders {
			if f == folder {
				t.Fatalf("S-85: denied skill's folder %q is still loadable — resolveTurnSkillAuthority must consult the stored outcome (kernel grant list derives from the same folders)", folder)
			}
		}
	})

	t.Run("S-86: an Ask skill's folder opens only after approval", func(t *testing.T) {
		home, folder := seedAuthorityHome(t, agentID, []string{"notes-helper"})
		requireOutcomeSeam(t)
		seedSkillOutcomeForAgent(t, home, agentID, "notes-helper", "ask", false)
		auth := resolveTurnSkillAuthority(home, agentID)
		for _, f := range auth.folders {
			if f == folder {
				t.Fatalf("S-86: unapproved Ask skill's folder %q is loadable before any approval", folder)
			}
		}
		seedSkillOutcomeForAgent(t, home, agentID, "notes-helper", "ask", true)
		auth = resolveTurnSkillAuthority(home, agentID)
		found := false
		for _, f := range auth.folders {
			if f == folder {
				found = true
			}
		}
		if !found {
			t.Fatalf("S-86: after approval the Ask skill's folder %q must be loadable in that chat", folder)
		}
	})

	t.Run("S-87: an editor agent reads (read-all) but a denied skill still does not load", func(t *testing.T) {
		home, folder := seedAuthorityHome(t, "agent-omnipus", []string{"notes-helper"})
		requireOutcomeSeam(t)
		// Omnipus allowed edit_skill (the editor) — the agent record carries
		// its own tool policy alongside the grant.
		if _, err := agentstore.New(home).Update("agent-omnipus", func(cfg *config.AgentConfig) error {
			cfg.Tools = &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"edit_skill": config.ToolPolicyAllow}}}
			return nil
		}); err != nil {
			t.Fatalf("seed editor policy: %v", err)
		}
		seedSkillOutcomeForAgent(t, home, "agent-omnipus", "notes-helper", "deny", false)
		auth := resolveTurnSkillAuthority(home, "agent-omnipus")
		if !auth.readAll {
			t.Fatalf("S-87: editor agent with edit_skill allow must keep the read-all authority (HomeGuard.SkillsReadAll)")
		}
		for _, f := range auth.folders {
			if f == folder {
				t.Fatalf("S-87: the denied skill's folder %q must not become loadable for the editor — read succeeds, load stays refused", folder)
			}
		}
	})
}
