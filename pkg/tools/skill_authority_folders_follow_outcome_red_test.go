//go:build goolm && stdjson

package tools

// Order 33 — TestSkillAuthority_FoldersFollowOutcome (S-85, S-86, S-87).
// Oracle: skills-tools-redesign-spec.md S-85 ("A denied skill's files cannot
// be read or run by the agent … the kernel grant list for Mia's turn does not
// contain the notes-helper folder"), S-86 ("An Ask skill's files open only
// after approval"), S-87 ("An editor agent can read but not use a denied
// skill"), traced through CRIT-002: denied and unapproved skill folders leave
// the loadable set in the app guard and the kernel grant. C-26 points 2 and 6
// (an allowance is bound to its agent, and answers an Ask but never lifts a
// Deny) bound the allowance legs.
//
// Production path: pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority
// → skills.LoadableFolders — the file's own doc states ResolveTurnFSPolicy
// is the ONLY place HomeGuard.SkillFolders (app guard) and the kernel grant
// list are filled. The app guard reads resolveTurnSkillAuthority directly; the
// kernel grant list is turnSkillFolders (E6 "kernel grant via
// turnSkillFolders"), read here through a registered turn-policy home, so both
// legs the scenarios name are observed on their own production function.
//
// Part L (#1963): the earlier seam seedSkillOutcomeForAgent took a testing.TB,
// which no production function can have, so it could never be bound — and
// there is no single production function that "seeds an outcome". Every stored
// input is now written where the spec stores it, by this test, and nothing is
// computed here:
//   - the global value and the trust value: <home>/config.json,
//     sandbox.skill_policies.<key> and sandbox.skill_trust (C-2, C-3). Trust is
//     stored allow so the trust floor can never settle the outcome (D1#7); the
//     scenarios are about the permission part.
//   - the agent's own value: the agent's stored record, tools.skills.policies.<key>
//     (C-4), written as the record's own JSON so this file compiles on a tree
//     that has no typed skill-override field.
//   - an approval: a timed allowance in the agent-keyed in-memory grant store
//     (C-12 "Stored consent", C-26), the writer a skill card's "Allow for 1 hour"
//     answer uses, registered as the process store. Approve Once stores nothing
//     (C-12), so a timed allowance is the one stored approval a folder read can
//     follow; the spec's "in that chat" is therefore, in the shipped design,
//     "for that agent" (C-26 point 2) — a spec-wording observation, not a test
//     gap.
//
// RED on a tree that has no per-skill outcome: the loadable set filters by
// grant and binding only, so the denied and the unapproved folders stay
// loadable and S-85, S-86 and S-87 fail on those assertions. The allowance
// writers do not exist there, so the legs that need them fail BLOCKED where
// they run.
import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/tests/helpers/agentrecord"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// recordSkillAllowanceOf is the seam to the writer a skill card's time choice
// uses (C-12 "Stored consent", C-26), typed exactly as the production method
// expression (*security.ApprovalGrantStore).RecordSkillAllowance so it binds
// to it directly. skill_authority_folders_bindings_test.go binds it; nil keeps
// a tree without that writer loud (BLOCKED), never a skip.
var recordSkillAllowanceOf func(store *security.ApprovalGrantStore, agentID, skillKey string, d time.Duration) (time.Time, bool)

// registerSkillAllowanceStoreOf is the seam to the registration that makes the
// agent loop's grant store the process store the folder authority reads
// (SetSkillAllowanceStore), typed exactly as that production function.
var registerSkillAllowanceStoreOf func(store *security.ApprovalGrantStore)

// allowSkillForAgent answers a skill card with "Allow for 1 hour" for agentID
// in a fresh grant store and registers it as the process store.
func allowSkillForAgent(t *testing.T, agentID, skillKey string) {
	t.Helper()
	nativebinding.Require(t, "pkg/security", recordSkillAllowanceOf,
		"C-12 Stored consent/C-26: the writer a skill card's time choice uses (the binding is (*security.ApprovalGrantStore).RecordSkillAllowance); the actual folder authority is the observer")
	nativebinding.Require(t, "pkg/tools", registerSkillAllowanceStoreOf,
		"C-12 Stored consent/C-26: registers the grant store the folder authority reads (the binding is SetSkillAllowanceStore)")
	store := security.NewApprovalGrantStore()
	_, recorded := recordSkillAllowanceOf(store, agentID, skillKey, time.Hour)
	require.True(t, recorded, "the 1-hour allowance of %q for %q is recorded", skillKey, agentID)
	registerSkillAllowanceStoreOf(store)
	t.Cleanup(func() { registerSkillAllowanceStoreOf(nil) })
}

// authorityHome is one fixture: a home whose config.json carries the global
// skill values and trust allow, one installed skill folder, and one agent
// record that is granted that skill.
type authorityHome struct {
	home, agentID, slug string
	folder              string // the skill folder's real path, as the authority returns it
}

// authoritySeed is every stored input of one authorityHome. The zero value of
// a field is the S-85..S-87 fixture's: trust allow (so the trust floor can never
// settle the outcome), God Mode off, the skill granted, edit_skill denied.
type authoritySeed struct {
	agentID, slug string
	global        string // the skill's stored global value ("" = no entry)
	trust         string // sandbox.skill_trust ("" = allow)
	godMode       bool   // sandbox.god_mode
	notGranted    bool   // the agent's record does not grant the skill
	editor        bool   // the agent's own edit_skill is allow (else deny)
}

// seedAuthorityHome writes the home. global is the skill's stored global value
// ("" = no entry), editor sets the agent's own edit_skill to allow (else deny:
// S-85 and S-86 name edit_skill denied for the agent; the read-all editor
// exception would otherwise let Mia read the folder whatever its outcome).
func seedAuthorityHome(t *testing.T, agentID, slug, global string, editor bool) authorityHome {
	t.Helper()
	return seedAuthorityHomeWith(t, authoritySeed{agentID: agentID, slug: slug, global: global, editor: editor})
}

// seedAuthorityHomeWith writes the home from every stored input: the global
// skill value, trust and God Mode in <home>/config.json (C-2, C-3, God Mode),
// the installed skill folder, and the agent's record (grant, edit_skill).
func seedAuthorityHomeWith(t *testing.T, seed authoritySeed) authorityHome {
	t.Helper()
	// The home's real path: the bash guard compares the home it reads from
	// OMNIPUS_HOME with the operands' real paths, so a symlinked temp root
	// (macOS /var) must not make a loadable folder look foreign.
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	skillValues := map[string]string{}
	if seed.global != "" {
		skillValues[seed.slug] = seed.global
	}
	trust := seed.trust
	if trust == "" {
		trust = "allow"
	}
	sandboxSection := map[string]any{
		"tool_policies":  map[string]string{"edit_skill": "allow"},
		"skill_policies": skillValues,
		"skill_trust":    trust,
	}
	if seed.godMode {
		sandboxSection["god_mode"] = true
	}
	// version: every config.json the product writes carries one, and a reader
	// that loads the whole file (config.LoadConfig) refuses a file without it.
	raw, err := json.Marshal(map[string]any{"version": 1, "sandbox": sandboxSection})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))

	dir := filepath.Join(home, "skills", seed.slug)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: "+seed.slug+"\ndescription: "+seed.slug+" fixture.\n---\n\nBody."), 0o600))
	realDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	editSkill := config.ToolPolicyDeny
	if seed.editor {
		editSkill = config.ToolPolicyAllow
	}
	granted := []string{seed.slug}
	if seed.notGranted {
		granted = nil
	}
	rec := &config.AgentConfig{
		ID:     seed.agentID,
		Skills: granted,
		Tools:  &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: map[string]config.ToolPolicy{"edit_skill": editSkill}}},
	}
	require.NoError(t, agentstore.New(home).Create(seed.agentID, rec))
	return authorityHome{home: home, agentID: seed.agentID, slug: seed.slug, folder: realDir}
}

// setAgentValue stores the agent's own value for the skill (C-4:
// tools.skills.policies.<key> of the agent's record), editing the record's
// own JSON.
func (h authorityHome) setAgentValue(t *testing.T, value string) {
	t.Helper()
	path := agentrecord.Path(h.home, h.agentID)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // keep every stored number exactly as written
	var record map[string]any
	require.NoError(t, dec.Decode(&record))
	node := record
	for _, key := range []string{"tools", "skills", "policies"} {
		child, _ := node[key].(map[string]any)
		if child == nil {
			child = map[string]any{}
			node[key] = child
		}
		node = child
	}
	node[h.slug] = value
	out, err := json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, out, 0o600))
}

// appGuard is the authority the app-level guard (HomeGuard.SkillFolders and
// SkillsReadAll) is filled from.
func (h authorityHome) appGuard() turnSkillAuthority {
	return resolveTurnSkillAuthority(h.home, h.agentID)
}

// kernelGrant is the folder list the kernel grant for the agent's turn is built
// from (turnSkillFolders), read through a registered turn-policy home.
func (h authorityHome) kernelGrant(t *testing.T) []string {
	t.Helper()
	prev, had := sandbox.RegisteredTurnPolicyBase()
	t.Cleanup(func() {
		if had {
			sandbox.RegisterTurnPolicyBase(&prev)
			return
		}
		sandbox.RegisterTurnPolicyBase(nil)
	})
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: h.home})
	return turnSkillFolders(WithAgentID(context.Background(), h.agentID))
}

// requireLoadable asserts the folder is in both the app guard set and the
// kernel grant list.
func (h authorityHome) requireLoadable(t *testing.T, why string) {
	t.Helper()
	require.Contains(t, h.appGuard().folders, h.folder, "app guard: %s", why)
	require.Contains(t, h.kernelGrant(t), h.folder, "kernel grant list: %s", why)
}

// requireNotLoadable asserts the folder is in neither the app guard set nor
// the kernel grant list.
func (h authorityHome) requireNotLoadable(t *testing.T, why string) {
	t.Helper()
	require.False(t, slices.Contains(h.appGuard().folders, h.folder), "app guard: %s", why)
	require.False(t, slices.Contains(h.kernelGrant(t), h.folder), "kernel grant list: %s", why)
}

func TestSkillAuthority_FoldersFollowOutcome(t *testing.T) {
	const agentID = "agent-mia"

	t.Run("control: a granted skill whose outcome is allowed is loadable", func(t *testing.T) {
		h := seedAuthorityHome(t, agentID, "notes-helper", "allow", false)
		require.Equal(t, []string{h.folder}, h.appGuard().folders,
			"control: the granted folder is the whole loadable set (the harness must see it before the outcome legs mean anything)")
		require.False(t, h.appGuard().readAll, "control: edit_skill is denied for this agent, so there is no editor read exception")
		h.requireLoadable(t, "control: an allowed skill's folder is granted to the kernel as well")
	})

	t.Run("S-85: a denied skill's folder leaves the loadable set (app guard and kernel grant)", func(t *testing.T) {
		// Given Mia granted notes-helper, Mia's own value for it Deny, edit_skill denied for Mia.
		h := seedAuthorityHome(t, agentID, "notes-helper", "allow", false)
		h.requireLoadable(t, "S-85 positive control: before Mia's own value is Deny the folder is loadable, so the Deny is the one thing that changes")
		h.setAgentValue(t, "deny")
		require.False(t, h.appGuard().readAll, "S-85 Given: edit_skill is denied for Mia")
		h.requireNotLoadable(t, "S-85: a denied skill's folder is refused as an ungranted skill's is (and is absent from the kernel grant list)")
	})

	t.Run("S-86: an Ask skill's folder opens only after approval", func(t *testing.T) {
		// Given deep-research at Ask, granted to Mia, not yet approved.
		h := seedAuthorityHome(t, agentID, "deep-research", "ask", false)
		require.False(t, h.appGuard().readAll, "S-86 Given: Mia is not an editor, so the read is not covered by the editor exception")
		h.requireNotLoadable(t, "S-86: an unapproved Ask skill's folder is refused before any approval")
		// After the person approves the load with a time choice (C-26), the same folder opens.
		allowSkillForAgent(t, agentID, "deep-research")
		h.requireLoadable(t, "S-86: after the person approves the load, the folder opens")
	})

	t.Run("C-26 point 2: another agent's allowance does not open an Ask skill's folder", func(t *testing.T) {
		h := seedAuthorityHome(t, agentID, "deep-research", "ask", false)
		allowSkillForAgent(t, "agent-someone-else", "deep-research")
		h.requireNotLoadable(t, "C-26 point 2: an allowance is bound to the agent that got it; other agents are unaffected")
	})

	t.Run("C-26 point 6: a running allowance never lifts the agent's own Deny", func(t *testing.T) {
		h := seedAuthorityHome(t, agentID, "notes-helper", "allow", false)
		h.setAgentValue(t, "deny")
		allowSkillForAgent(t, agentID, "notes-helper")
		h.requireNotLoadable(t, "C-26 point 6: an allowance answers an Ask and never lifts a Deny")
	})

	t.Run("S-87: an editor agent reads (read-all) but a denied skill still does not load", func(t *testing.T) {
		// Given Omnipus allowed edit_skill and notes-helper Deny for Omnipus.
		h := seedAuthorityHome(t, "agent-omnipus", "notes-helper", "allow", true)
		h.setAgentValue(t, "deny")
		require.True(t, h.appGuard().readAll,
			"S-87: the editor agent keeps the read-all authority (HomeGuard.SkillsReadAll) — the read succeeds")
		h.requireNotLoadable(t, "S-87: the editor exception never adds the denied skill's folder to the loadable set — the load stays refused")
	})
}
