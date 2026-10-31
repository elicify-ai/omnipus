package systools_test

// RED tests — landing/skills (T9): the new `get_skill` reader (§5.2) and
// `list_skills`' pure-list change (§5.3). Scenarios S-8.1–S-8.10, E-9,
// S-8.7 (name removal); dataset D-5 (fields). The 250-version paging matrix
// itself lives with the history fixtures in pkg/skills; the tool-side
// contract (versions_before/limit validation) is asserted below.
// Traces to: skills-spec-r13.md §5.2–§5.3, §16 US-8, §17.1 rows 2/3/35.
//
// RED expectation: GetSkillTool is a scaffold that refuses everything, and
// list_skills still accepts `name` — every behavioural assertion below fails
// on the baseline.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validSkillFixture writes a skill folder with frontmatter and returns its dir.
func validSkillFixture(t *testing.T, root, slug, description string) string {
	t.Helper()
	dir := filepath.Join(root, slug)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	md := "---\nname: " + slug + "\ndescription: " + description + "\n---\n\nBody of " + slug + ".\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644))
	return dir
}

// skillDepsOpts configures newSkillDeps.
type skillDepsOpts struct {
	agentID  string
	grants   []string
	policies map[string]string // tool name → allow|ask|deny
}

// newSkillDeps builds Deps for get_skill/list_skills tests: one agent with
// grants and a policy resolver stub.
func newSkillDeps(t *testing.T, globalSkillsDir string, opts skillDepsOpts) *systools.Deps {
	t.Helper()
	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{{
		ID:     opts.agentID,
		Skills: opts.grants,
	}}
	deps, _ := newTestDeps(t)
	deps.SkillsLoader = skills.NewSkillsLoader("", globalSkillsDir, "")
	deps.GetCfg = func() *config.Config { return cfg }
	deps.ResolveToolPolicy = func(agentID, toolName string) (string, bool) {
		if agentID != opts.agentID {
			return "", false
		}
		p, ok := opts.policies[toolName]
		return p, ok
	}
	return deps
}

// errorBlock extracts the {code,message} error block from a result.
func errorBlock(t *testing.T, res *tools.ToolResult) (code, message string) {
	t.Helper()
	m := parseToolJSON(t, res.ForLLM)
	blk, _ := m["error"].(map[string]any)
	require.NotNil(t, blk, "expected an error block, got: %s", res.ForLLM)
	code, _ = blk["code"].(string)
	message, _ = blk["message"].(string)
	return code, message
}

// readCtx carries the acting agent id.
func readCtx(agentID string) context.Context {
	return tools.WithAgentID(context.Background(), agentID)
}

// TestGetSkill_FieldsShape (S-8.1). BDD: a granted skill read with
// fields:["description","files"] returns exactly id, revision, description,
// files; files = {items, total}.
// Traces to: skills-spec-r13.md §5.2, S-8.1.
func TestGetSkill_FieldsShape(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "pdf-tools", "read PDFs")
	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-a",
		grants:   []string{"pdf-tools"},
		policies: map[string]string{"get_skill": "allow"},
	})

	res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-a"), map[string]any{
		"id":     "pdf-tools",
		"fields": []any{"description", "files"},
	})
	m := parseToolJSON(t, res.ForLLM)
	for _, key := range []string{"id", "revision", "description", "files"} {
		require.Contains(t, m, key, "result key %s present (RED: scaffold refuses)", key)
	}
	for _, extra := range []string{"instructions", "metadata", "versions", "name", "folder", "state", "shelf", "compatibility", "allowed_tools", "needs", "last_change", "has_local_changes", "absent_fields", "truncated_fields"} {
		assert.NotContains(t, m, extra, "unrequested key %s must be absent", extra)
	}
	files, _ := m["files"].(map[string]any)
	require.NotNil(t, files, "files is the {items,total} object")
	assert.Contains(t, files, "items")
	assert.Contains(t, files, "total")
}

// TestGetSkill_FieldsValidation_D5 (S-8.2, D-5).
// BDD: missing / empty / string-"all" / ["all","name"] / ["name","name"] /
// ["nope"] / ["absent_fields"] / all 14 names — the T8 shared validation
// texts, byte-exact where the spec fixes them.
// Traces to: skills-spec-r13.md §5.2 parameters + errors, §17.2 D-5, S-8.2.
func TestGetSkill_FieldsValidation_D5(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "app", "fixture")
	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-a",
		grants:   []string{"app"},
		policies: map[string]string{"get_skill": "allow"},
	})
	tool := systools.NewGetSkillTool(deps)
	ctx := readCtx("agent-a")

	call := func(fields any) *tools.ToolResult {
		args := map[string]any{"id": "app"}
		if fields != nil {
			args["fields"] = fields
		}
		return tool.Execute(ctx, args)
	}

	t.Run("missing fields is refused", func(t *testing.T) {
		code, msg := errorBlock(t, call(nil))
		assert.Equal(t, "INVALID_INPUT", code)
		assert.Contains(t, msg, `fields is required for skill: pass ["all"] or one or more of:`,
			"byte-exact T8 shared required-fields text")
	})

	t.Run("empty array is refused", func(t *testing.T) {
		code, msg := errorBlock(t, call([]any{}))
		assert.Equal(t, "INVALID_INPUT", code)
		assert.Contains(t, msg, "fields is required for skill")
	})

	t.Run("all as a bare string is refused", func(t *testing.T) {
		code, _ := errorBlock(t, call("all"))
		assert.Equal(t, "INVALID_INPUT", code)
	})

	t.Run("all combined with other names is refused", func(t *testing.T) {
		code, msg := errorBlock(t, call([]any{"all", "name"}))
		assert.Equal(t, "INVALID_INPUT", code)
		assert.Contains(t, msg, `"all" cannot be combined with other field names`,
			"byte-exact T8 shared text")
	})

	t.Run("duplicate names are refused", func(t *testing.T) {
		code, _ := errorBlock(t, call([]any{"name", "name"}))
		assert.Equal(t, "INVALID_INPUT", code, "uniqueItems")
	})

	t.Run("unknown field names the valid set", func(t *testing.T) {
		code, msg := errorBlock(t, call([]any{"nope"}))
		assert.Equal(t, "INVALID_INPUT", code)
		assert.Contains(t, msg, `unknown field "nope" for skill. Valid: all,`,
			"byte-exact T8 shared unknown-field text")
	})

	t.Run("metadata keys are not selectable", func(t *testing.T) {
		code, msg := errorBlock(t, call([]any{"absent_fields"}))
		assert.Equal(t, "INVALID_INPUT", code)
		assert.Contains(t, msg, `unknown field "absent_fields"`,
			"T8 P-2: metadata keys are never selectable")
	})

	t.Run("all fourteen selectable names are valid", func(t *testing.T) {
		res := call([]any{"state", "name", "description", "shelf", "folder", "files", "metadata",
			"compatibility", "allowed_tools", "needs", "last_change", "has_local_changes", "versions", "instructions"})
		code, _ := errorBlock(t, res)
		assert.NotEqual(t, "INVALID_INPUT", code,
			"all 14 §5.2 field names must be accepted, got: %s", res.ForLLM)
	})
}

// TestGetSkill_AuthorizationMatrix (S-8.4, S-8.5, S-8.6, NOT_FOUND).
// BDD: non-editor without management tools → PERMISSION_DENIED byte-exact;
// management holder (update_agent ask, edit_skill deny) → allowed;
// non-editor on a deleted skill (history exists) → PERMISSION_DENIED —
// authorization precedes existence; an authorised caller with neither folder
// nor history → NOT_FOUND.
// Traces to: skills-spec-r13.md §5.2 who-may-call table + §5.3 precedence
// rule (R2-MIN-001), S-8.4–S-8.6.
func TestGetSkill_AuthorizationMatrix(t *testing.T) {
	nonEditor := skillDepsOpts{
		agentID:  "agent-n",
		grants:   []string{},
		policies: map[string]string{"get_skill": "allow", "edit_skill": "deny"},
	}
	t.Run("S-8.4 non-editor, ungranted → PERMISSION_DENIED", func(t *testing.T) {
		global := t.TempDir()
		validSkillFixture(t, global, "secret-skill", "not for you")
		deps := newSkillDeps(t, global, nonEditor)

		res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-n"),
			map[string]any{"id": "secret-skill", "fields": []any{"description"}})
		code, msg := errorBlock(t, res)
		assert.Equal(t, "PERMISSION_DENIED", code)
		assert.Contains(t, msg, `skill "secret-skill" is not available to this agent`,
			"byte-exact §5.2 PERMISSION_DENIED text")
	})

	t.Run("S-8.5 management holder (update_agent ask, edit_skill deny) is allowed", func(t *testing.T) {
		global := t.TempDir()
		validSkillFixture(t, global, "ungranted", "details")
		deps := newSkillDeps(t, global, skillDepsOpts{
			agentID:  "agent-m",
			grants:   []string{},
			policies: map[string]string{"get_skill": "allow", "edit_skill": "deny", "update_agent": "ask"},
		})
		res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-m"),
			map[string]any{"id": "ungranted", "fields": []any{"instructions"}})
		code, _ := errorBlock(t, res)
		assert.NotEqual(t, "PERMISSION_DENIED", code,
			"management-read authority covers installed skills, got: %s", res.ForLLM)
	})

	t.Run("S-8.6 non-editor on a deleted skill → PERMISSION_DENIED, never NOT_FOUND", func(t *testing.T) {
		global := t.TempDir()
		validSkillFixture(t, global, "gone", "later deleted")
		// The live folder is gone; §9 history exists at the spec path.
		require.NoError(t, os.RemoveAll(filepath.Join(global, "gone")))
		home := t.TempDir()
		hist := filepath.Join(home, "system", "skill-history", "gone", "versions")
		require.NoError(t, os.MkdirAll(hist, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(hist, "v1.json"),
			[]byte(`{"id":"v1","created_at":"20260101T000000Z","slug":"gone","kind":"delete_skill","actor":"agent","files":[],"paths":[],"paths_total":0,"revision":""}`), 0o644))
		deps := newSkillDeps(t, global, nonEditor)
		deps.Home = home

		res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-n"),
			map[string]any{"id": "gone", "fields": []any{"state"}})
		code, msg := errorBlock(t, res)
		assert.Equal(t, "PERMISSION_DENIED", code,
			"authorization precedes existence (§5.3), got: %s", res.ForLLM)
		assert.Contains(t, msg, `skill "gone" is not available to this agent`)
	})

	t.Run("authorised caller, no folder and no history → NOT_FOUND", func(t *testing.T) {
		global := t.TempDir()
		validSkillFixture(t, global, "app", "fixture")
		deps := newSkillDeps(t, global, skillDepsOpts{
			agentID:  "agent-a",
			grants:   []string{"app"},
			policies: map[string]string{"get_skill": "allow"},
		})
		res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-a"),
			map[string]any{"id": "nope", "fields": []any{"state"}})
		code, msg := errorBlock(t, res)
		assert.Equal(t, "NOT_FOUND", code)
		assert.Contains(t, msg, `skill "nope" is not installed`, "byte-exact §5.2 NOT_FOUND text")
	})
}

// TestGetSkill_DeletedSkillShape (S-8.8).
// BDD: an editor reading a deleted skill with fields:["state","name","versions"]
// gets {id, revision: null, state: "deleted", versions: {…}, absent_fields:
// ["name"]} and no other key.
// Traces to: skills-spec-r13.md §5.2 deleted-skill row, S-8.8.
func TestGetSkill_DeletedSkillShape(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "gone", "later deleted")
	require.NoError(t, os.RemoveAll(filepath.Join(global, "gone")))
	home := t.TempDir()
	hist := filepath.Join(home, "system", "skill-history", "gone", "versions")
	require.NoError(t, os.MkdirAll(hist, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hist, "v1.json"),
		[]byte(`{"id":"v1","created_at":"20260101T000000Z","slug":"gone","kind":"delete_skill","actor":"agent","files":[],"paths":[],"paths_total":0,"revision":""}`), 0o644))

	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-e",
		grants:   []string{},
		policies: map[string]string{"get_skill": "allow", "edit_skill": "ask"},
	})
	deps.Home = home
	res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-e"),
		map[string]any{"id": "gone", "fields": []any{"state", "name", "versions"}})
	m := parseToolJSON(t, res.ForLLM)

	assert.Equal(t, "gone", m["id"])
	require.Contains(t, m, "revision")
	assert.Nil(t, m["revision"], "revision is null for a deleted skill (json null)")
	assert.Equal(t, "deleted", m["state"])
	versions, _ := m["versions"].(map[string]any)
	require.NotNil(t, versions, "versions keeps its normal value")
	assert.Contains(t, versions, "items")
	absent, _ := m["absent_fields"].([]any)
	assert.Contains(t, absent, "name", "unrequested properties are named in absent_fields (T8 P-2)")
	for _, extra := range []string{"description", "files", "instructions", "metadata", "folder"} {
		assert.NotContains(t, m, extra, "no invented nulls for %s", extra)
	}
}

// TestGetSkill_BuiltinVersionsEmptyPage (E-9).
// BDD: get_skill on a built-in with fields:["versions"] → the empty page.
// Traces to: skills-spec-r13.md E-9, §5.2 built-ins row.
func TestGetSkill_BuiltinVersionsEmptyPage(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "plan", "built-in fixture")
	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-a",
		grants:   []string{"plan"},
		policies: map[string]string{"get_skill": "allow"},
	})
	res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-a"),
		map[string]any{"id": "plan", "fields": []any{"versions", "state", "has_local_changes"}})
	m := parseToolJSON(t, res.ForLLM)
	versions, _ := m["versions"].(map[string]any)
	require.NotNil(t, versions)
	items, _ := versions["items"].([]any)
	assert.Empty(t, items, "built-in versions page is empty")
	assert.Equal(t, float64(0), versions["total"])
	assert.Nil(t, versions["next_before"], "next_before: null")
	assert.Equal(t, false, m["has_local_changes"], "built-ins always false")
}

// TestGetSkill_UnknownVersion (§5.10 unknown-version text).
// BDD: versions_before "v999999-…" → INVALID_INPUT with the byte-exact text.
// Traces to: skills-spec-r13.md §5.10 unknown version, S-5.12.
func TestGetSkill_UnknownVersion(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "app", "fixture")
	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-a",
		grants:   []string{"app"},
		policies: map[string]string{"get_skill": "allow"},
	})
	res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-a"), map[string]any{
		"id":              "app",
		"fields":          []any{"versions"},
		"versions_before": "v999999-20260101T000000Z",
	})
	code, msg := errorBlock(t, res)
	assert.Equal(t, "INVALID_INPUT", code)
	assert.Contains(t, msg, `unknown version "v999999-20260101T000000Z" for skill "app"`,
		"byte-exact §5.10 unknown-version text")
}

// TestListSkills_NameRemoved (S-8.7).
// BDD: list_skills with name → INVALID_INPUT `name was removed from
// list_skills; use get_skill with id and fields`; management rows never
// carry content.
// Traces to: skills-spec-r13.md §5.3, S-8.7.
func TestListSkills_NameRemoved(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "app", "fixture")
	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-m",
		grants:   []string{},
		policies: map[string]string{"list_skills": "allow", "edit_skill": "ask", "update_agent": "ask"},
	})

	res := systools.NewSkillListTool(deps).Execute(readCtx("agent-m"),
		map[string]any{"scope": "management", "name": "app"})
	code, msg := errorBlock(t, res)
	assert.Equal(t, "INVALID_INPUT", code,
		"name was removed from list_skills (RED: today it still filters), got: %s", res.ForLLM)
	assert.Contains(t, msg, "name was removed from list_skills; use get_skill with id and fields",
		"byte-exact §5.3 removal text")

	// Management rows never carry content, with or without name.
	res = systools.NewSkillListTool(deps).Execute(readCtx("agent-m"),
		map[string]any{"scope": "management"})
	m := parseToolJSON(t, res.ForLLM)
	if skillsArr, ok := m["skills"].([]any); ok {
		for _, row := range skillsArr {
			rm, _ := row.(map[string]any)
			assert.NotContains(t, rm, "content", "management rows never carry content")
		}
	}
}

// TestListSkills_UsableRowsGrantFiltered_Regression (§17.3 regression row 1).
// BDD: usable scope returns exactly the loadable set (unchanged predicate).
// This pair must PASS on the baseline and stay green through the migration.
// Traces to: skills-spec-r13.md §17.3 (ADR-072 D4/N1 regression).
func TestListSkills_UsableRowsGrantFiltered_Regression(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "granted", "mine")
	validSkillFixture(t, global, "other", "not mine")
	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-a",
		grants:   []string{"granted"},
		policies: map[string]string{"list_skills": "allow"},
	})
	res := systools.NewSkillListTool(deps).Execute(readCtx("agent-a"), map[string]any{"scope": "usable"})
	m := parseToolJSON(t, res.ForLLM)
	skillsArr, _ := m["skills"].([]any)
	ids := make([]string, 0, len(skillsArr))
	for _, row := range skillsArr {
		rm, _ := row.(map[string]any)
		id, _ := rm["name"].(string)
		ids = append(ids, id)
	}
	assert.Contains(t, ids, "granted")
	assert.NotContains(t, ids, "other", "usable rows are exactly the loadable set")
}

// TestGetSkill_LockedReadCoherence (S-8.10 legs 1–3, three-way oracle r8).
// BDD: while the read holds the registry mutation lock a managed writer stays
// blocked, and an outside (lock-bypassing) write is not blocked — the read
// still serves ONE captured observation: the instructions and the revision
// come from the same observation, never a revision recomputed from projected
// output (R6-MAJ-001).
// Traces to: skills-spec-r13.md §5.2 side effect (three-way oracle), S-8.10.
func TestGetSkill_LockedReadCoherence(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "foo", "before")
	deps := newSkillDeps(t, global, skillDepsOpts{
		agentID:  "agent-a",
		grants:   []string{"foo"},
		policies: map[string]string{"get_skill": "allow"},
	})
	tool := systools.NewGetSkillTool(deps)

	t.Run("legs 1–2 managed writer blocked while the read holds the lock", func(t *testing.T) {
		// Hold the registry mutation lock over the skills root; the read must
		// not complete until it is released (one lock hold per read).
		release := make(chan struct{})
		acquired := make(chan struct{})
		go func() {
			defer close(acquired)
			_ = skills.WithMutationLock(global, func() error {
				close(acquired)
				<-release
				return nil
			})
		}()
		<-acquired

		readDone := make(chan *tools.ToolResult, 1)
		go func() {
			readDone <- tool.Execute(readCtx("agent-a"),
				map[string]any{"id": "foo", "fields": []any{"instructions"}})
		}()

		select {
		case res := <-readDone:
			t.Fatalf("the read did not wait for the registry mutation lock: %s", res.ForLLM)
		default:
			// Still blocked — correct.
		}
		close(release)
		res := <-readDone
		code, _ := errorBlock(t, res)
		assert.NotEqual(t, "PERMISSION_DENIED", code, "the read completes after the release")
	})

	t.Run("leg 3 outside writer bypasses the lock — one captured observation", func(t *testing.T) {
		// An outside write (holds no lock) lands while the read is capturing;
		// the served instructions and the served revision must come from the
		// SAME observation. The probe is the injection point: when the read's
		// capture first opens the live SKILL.md, the outside write lands.
		writer := skills.NewSkillWriter(global)
		beforeRev, err := writer.SkillRevision("foo")
		require.NoError(t, err)

		var injected bool
		prevProbe := skills.CaptureOpenProbe
		skills.CaptureOpenProbe = func(op, path string) {
			if op == "read" && strings.HasSuffix(path, filepath.Join("foo", "SKILL.md")) && !injected {
				injected = true
				// Outside writer: no lock, live bytes change mid-flight.
				_ = os.WriteFile(path,
					[]byte("---\nname: foo\ndescription: after\n---\n\nAFTER-BODY\n"), 0o644)
			}
		}
		defer func() { skills.CaptureOpenProbe = prevProbe }()

		res := tool.Execute(readCtx("agent-a"),
			map[string]any{"id": "foo", "fields": []any{"instructions"}})
		m := parseToolJSON(t, res.ForLLM)
		instructions, _ := m["instructions"].(string)
		rev, _ := m["revision"].(string)

		afterRev, revErr := writer.SkillRevision("foo")
		require.NoError(t, revErr)
		assert.NotEqual(t, beforeRev, afterRev, "the outside write landed mid-flight")

		// One captured observation: the pair (instructions, revision) is one
		// of the two complete states, never a mix.
		pairBefore := strings.Contains(instructions, "Body of foo") && rev == beforeRev
		pairAfter := strings.Contains(instructions, "AFTER-BODY") && rev == afterRev
		assert.True(t, pairBefore || pairAfter,
			"instructions and revision must come from the same observation "+
				"(got instructions=%q revision=%q; before=%s after=%s)",
			instructions, rev, beforeRev, afterRev)
	})
}
