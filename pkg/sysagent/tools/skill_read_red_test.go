package systools_test

// RED tests — landing/skills (T9): the new `get_skill` reader (§5.2) and
// `list_skills`' pure-list change (§5.3). Scenarios S-8.1–S-8.10, E-9,
// S-8.7 (name removal); dataset D-5 (fields). Full paging is in the sibling
// skills_versions_projection_red_test.go; adapter seen-state in
// skills_seen_surface_red_test.go.
// Traces to: skills-spec-r15.md §5.2–§5.3, §16 US-8, §17.1 rows 2/3/35.
//
// RED expectation: GetSkillTool is a scaffold that refuses everything, and
// list_skills still accepts `name` — every behavioural assertion below fails
// on the baseline.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	bindings := map[string]string{}
	for _, slug := range opts.grants {
		dir := filepath.Join(globalSkillsDir, strings.ToLower(slug))
		if _, err := os.Stat(dir); err == nil {
			const generation = "11111111-2222-3333-4444-555555555555"
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-identity"), []byte(generation+"\n"), 0o644))
			bindings[strings.ToLower(slug)] = generation
		}
	}
	cfg.Agents.List = []config.AgentConfig{{
		ID:     opts.agentID,
		Skills: opts.grants,
	}}
	raw, err := json.Marshal(cfg.Agents.List[0])
	require.NoError(t, err)
	var record map[string]any
	require.NoError(t, json.Unmarshal(raw, &record))
	record["skill_grants"] = bindings
	raw, err = json.Marshal(record)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &cfg.Agents.List[0]))
	deps, _ := newTestDeps(t)
	deps.Home = filepath.Dir(globalSkillsDir)
	installLiveSessionStore(t, deps, deps.Home)
	deps.SkillWriter = skills.NewSkillWriter(globalSkillsDir)
	deps.SkillsLoader = skills.NewSkillsLoader("", globalSkillsDir, "")
	deps.GetCfg = func() *config.Config { return cfg }
	deps.ResolveToolPolicy = func(agentID, toolName string) (string, bool) {
		if agentID != opts.agentID {
			return "", false
		}
		p, ok := opts.policies[toolName]
		if !ok {
			return "deny", true
		} // registered, explicitly denied; not an unregistered caller
		return p, true
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
var readSessionSequence atomic.Uint64

func readCtx(agentID string) context.Context {
	return tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), agentID), fmt.Sprintf("read-session-%s-%d", agentID, readSessionSequence.Add(1)))
}

// TestGetSkill_FieldsShape (S-8.1). BDD: a granted skill read with
// fields:["description","files"] returns exactly id, revision, description,
// files; files = {items, total}.
// Traces to: skills-spec-r15.md §5.2, S-8.1.
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
	if _, hasErr := m["error"].(map[string]any); hasErr {
		t.Fatalf("RED: the scaffold refuses the read, got: %s", res.ForLLM)
	}
	for _, key := range []string{"id", "revision", "description", "files"} {
		require.Contains(t, m, key, "result key %s present", key)
	}
	for _, extra := range []string{"instructions", "metadata", "versions", "name", "folder", "state", "shelf", "compatibility", "allowed_tools", "needs", "last_change", "has_local_changes", "absent_fields", "truncated_fields"} {
		assert.NotContains(t, m, extra, "unrequested key %s must be absent", extra)
	}
	// Positive content oracle: the fixture's own values are served.
	assert.Equal(t, "pdf-tools", m["id"])
	assert.Equal(t, "read PDFs", m["description"], "the fixture description is served")
	rev, _ := m["revision"].(string)
	require.NotEmpty(t, rev, "revision is served")
	live, err := skills.ContentRevision(filepath.Join(global, "pdf-tools"))
	require.NoError(t, err, "the fixture folder's content revision is computable")
	assert.Equal(t, live, rev, "the served revision equals the folder's content revision (same observation)")
	files, _ := m["files"].(map[string]any)
	require.NotNil(t, files, "files is the {items,total} object")
	assert.Contains(t, files, "items")
	assert.Contains(t, files, "total")
	assert.Equal(t, float64(1), files["total"], "the fixture has exactly one file")
	items, _ := files["items"].([]any)
	require.Len(t, items, 1, "the items page lists the fixture's one file")
	first, _ := items[0].(map[string]any)
	assert.Equal(t, "SKILL.md", first["path"], "the listed path is the fixture's file")
}

// TestGetSkill_FieldsValidation_D5 (S-8.2, D-5).
// BDD: missing / empty / string-"all" / ["all","name"] / ["name","name"] /
// ["nope"] / ["absent_fields"] / all 14 names — the T8 shared validation
// texts, byte-exact where the spec fixes them.
// Traces to: skills-spec-r15.md §5.2 parameters + errors, §17.2 D-5, S-8.2.
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
		m := parseToolJSON(t, res.ForLLM)
		_, hasErr := m["error"].(map[string]any)
		require.False(t, hasErr,
			"all 14 §5.2 field names must be accepted (this is a positive case: no error block may appear), got: %s", res.ForLLM)
		// Positive content oracle on the accepted call.
		assert.Equal(t, "app", m["id"])
		assert.Equal(t, "fixture", m["description"])
	})
}

// TestGetSkill_AuthorizationMatrix (S-8.4, S-8.5, S-8.6, NOT_FOUND).
// BDD: non-editor without management tools → PERMISSION_DENIED byte-exact;
// management holder (update_agent ask, edit_skill deny) → allowed;
// non-editor on a deleted skill (history exists) → PERMISSION_DENIED —
// authorization precedes existence; an authorised caller with neither folder
// nor history → NOT_FOUND.
// Traces to: skills-spec-r15.md §5.2 who-may-call table + §5.3 precedence
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
			policies: map[string]string{"get_skill": "allow", "list_skills": "allow", "edit_skill": "deny", "update_agent": "ask"},
		})
		res := systools.NewGetSkillTool(deps).Execute(readCtx("agent-m"),
			map[string]any{"id": "ungranted", "fields": []any{"instructions"}})
		m := parseToolJSON(t, res.ForLLM)
		_, hasErr := m["error"].(map[string]any)
		require.False(t, hasErr,
			"management-read authority covers installed skills — this positive case must not error, got: %s", res.ForLLM)
		instructions, _ := m["instructions"].(string)
		assert.Contains(t, instructions, "Body of ungranted",
			"the management read serves the real instructions (positive content oracle)")
	})

	t.Run("S-8.6 non-editor on a deleted skill → PERMISSION_DENIED, never NOT_FOUND", func(t *testing.T) {
		global := t.TempDir()
		validSkillFixture(t, global, "gone", "later deleted")
		// The live folder is gone; §9 history exists at the spec path.
		require.NoError(t, os.RemoveAll(filepath.Join(global, "gone")))
		home := filepath.Dir(global)
		hist := filepath.Join(home, "system", "skill-history", "gone", "versions")
		require.NoError(t, os.MkdirAll(hist, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(hist, "v000001-20260101T000000Z.json"),
			[]byte(`{"id":"v000001-20260101T000000Z","created_at":"20260101T000000Z","slug":"gone","kind":"delete_skill","actor":"agent","files":[],"paths":[],"paths_total":0,"revision":""}`), 0o644))
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
		// Explicit editor authority is independent of a live skill. A
		// name-only grant for an absent skill is unbound under SD-30.
		deps := newSkillDeps(t, global, skillDepsOpts{
			agentID:  "agent-a",
			grants:   []string{},
			policies: map[string]string{"get_skill": "allow", "list_skills": "allow", "edit_skill": "ask"},
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
// Traces to: skills-spec-r15.md §5.2 deleted-skill row, S-8.8.
func TestGetSkill_DeletedSkillShape(t *testing.T) {
	global := t.TempDir()
	validSkillFixture(t, global, "gone", "later deleted")
	require.NoError(t, os.RemoveAll(filepath.Join(global, "gone")))
	home := filepath.Dir(global)
	hist := filepath.Join(home, "system", "skill-history", "gone", "versions")
	require.NoError(t, os.MkdirAll(hist, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hist, "v000001-20260101T000000Z.json"),
		[]byte(`{"id":"v000001-20260101T000000Z","created_at":"20260101T000000Z","slug":"gone","kind":"delete_skill","actor":"agent","files":[],"paths":[],"paths_total":0,"revision":""}`), 0o644))

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
// Traces to: skills-spec-r15.md E-9, §5.2 built-ins row.
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
// Traces to: skills-spec-r15.md §5.10 unknown version, S-5.12.
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
// Traces to: skills-spec-r15.md §5.3, S-8.7.
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
	skillsArr, ok := m["skills"].([]any)
	require.True(t, ok, "successful management listing has rows")
	require.Len(t, skillsArr, 1, "the fixture is listed, not an empty fabricated success")
	{
		for _, row := range skillsArr {
			rm, _ := row.(map[string]any)
			assert.NotContains(t, rm, "content", "management rows never carry content")
		}
	}
}

// TestListSkills_UsableRowsGrantFiltered_Regression (§17.3 regression row 1).
// BDD: usable scope returns exactly the loadable set (unchanged predicate).
// This pair must PASS on the baseline and stay green through the migration.
// Traces to: skills-spec-r15.md §17.3 (ADR-072 D4/N1 regression).
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
		id, _ := rm["id"].(string)
		ids = append(ids, id)
	}
	assert.Equal(t, []string{"granted"}, ids, "usable rows are exactly the loadable set")
}

// TestGetSkill_LockedReadCoherence (S-8.10 legs 1–3, three-way oracle r8).
// BDD: while the read holds the registry mutation lock a managed writer stays
// blocked, and an outside (lock-bypassing) write is not blocked — the read
// still serves ONE captured observation: the instructions and the revision
// come from the same observation, never a revision recomputed from projected
// output (R6-MAJ-001).
// Traces to: skills-spec-r15.md §5.2 side effect (three-way oracle), S-8.10.
func TestGetSkill_LockedReadCoherence(t *testing.T) {
	t.Run("managed writer cannot cross a paused reader", func(t *testing.T) {
		global := t.TempDir()
		dir := validSkillFixture(t, global, "foo", "before")
		deps := newSkillDeps(t, global, skillDepsOpts{agentID: "reader", grants: []string{"foo"}, policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
		ctx := skillSession(t, deps, "reader")
		before, err := skills.ContentRevision(dir)
		require.NoError(t, err)
		entered, release := make(chan struct{}), make(chan struct{})
		readDone := make(chan *tools.ToolResult, 1)
		readExited := make(chan struct{})
		var hold, unhold sync.Once
		old := skills.CaptureOpenProbe
		skills.CaptureOpenProbe = func(op, path string) {
			if op == "capture" && path == filepath.Join(dir, "SKILL.md") {
				hold.Do(func() { close(entered); <-release })
			}
		}
		var writerExited chan struct{}
		// Every fatal path releases the hold and joins the launched work BEFORE
		// restoring global hooks or deleting fixture files. No double close.
		t.Cleanup(func() {
			unhold.Do(func() { close(release) })
			<-readExited
			if writerExited != nil {
				<-writerExited
			}
			skills.CaptureOpenProbe = old
		})
		go func() {
			defer close(readExited)
			readDone <- systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": "foo", "fields": []any{"instructions"}})
		}()
		select {
		case <-entered:
		case res := <-readDone:
			t.Fatalf("RED: get_skill did not reach a real captured read: %s", res.ForLLM)
		case <-time.After(10 * time.Second):
			t.Fatal("BLOCKED: real read capture boundary not implemented — r15 §5.2 / S-8.10")
		}
		done := make(chan error, 1)
		writerExited = make(chan struct{})
		go func() { defer close(writerExited); done <- coherenceManagedWrite(global, before) }()
		// Observe the writer actually WAITING in the registry mutex, rather than
		// assuming it ran because 200ms elapsed. A completed writer is decisive
		// failure. The deadline is a deadlock guard, not the correctness oracle.
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
	wait:
		for {
			select {
			case writeErr := <-done:
				t.Fatalf("writer crossed paused read: %v", writeErr)
			case <-deadline.C:
				t.Fatal("writer never reached registry-lock wait")
			default:
			}
			stacks := make([]byte, 1<<20)
			n := runtime.Stack(stacks, true)
			for _, g := range strings.Split(string(stacks[:n]), "\n\n") {
				if strings.Contains(g, "coherenceManagedWrite") && strings.Contains(g, "WithMutationLock") && strings.Contains(g, "Mutex") {
					break wait
				}
			}
			runtime.Gosched()
		}
		unhold.Do(func() { close(release) })
		res := <-readDone
		m := parseToolJSON(t, res.ForLLM)
		require.NotContains(t, m, "error")
		assert.Equal(t, before, m["revision"])
		assert.Contains(t, m["instructions"], "Body of foo.")
		require.NoError(t, <-done, "managed write commits only after the captured read releases")
		raw, err := os.ReadFile(filepath.Join(dir, "notes.md"))
		require.NoError(t, err)
		assert.Equal(t, "managed write\n", string(raw))
		code, _ := errorBlock(t, systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"}))
		assert.Equal(t, "SKILL_CHANGED", code, "the read recorded A, not managed B which committed after it")
	})
	t.Run("outside writer bypasses lock but projection and revision describe one observation", func(t *testing.T) {
		global := t.TempDir()
		dir := validSkillFixture(t, global, "foo", "before")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("seen probe\n"), 0o644))
		deps := newSkillDeps(t, global, skillDepsOpts{agentID: "reader", grants: []string{"foo"}, policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
		ctx := skillSession(t, deps, "reader")
		before, err := skills.ContentRevision(dir)
		require.NoError(t, err)
		var injected bool
		var injectionErr error
		old := skills.CaptureOpenProbe
		defer func() { skills.CaptureOpenProbe = old }()
		skills.CaptureOpenProbe = func(op, path string) {
			if op == "capture" && path == filepath.Join(dir, "SKILL.md") && !injected {
				injected = true
				injectionErr = os.WriteFile(path, []byte("---\nname: foo\ndescription: after\n---\n\nAFTER-BODY\n"), 0o644)
			}
		}
		res := systools.NewGetSkillTool(deps).Execute(ctx, map[string]any{"id": "foo", "fields": []any{"instructions"}})
		m := parseToolJSON(t, res.ForLLM)
		require.NotContains(t, m, "error")
		require.True(t, injected, "the real capture instrument fired")
		require.NoError(t, injectionErr)
		after, err := skills.ContentRevision(dir)
		require.NoError(t, err)
		require.NotEqual(t, before, after)
		instructions, _ := m["instructions"].(string)
		rev, _ := m["revision"].(string)
		assert.True(t, (strings.TrimSpace(instructions) == "Body of foo." && rev == before) || (strings.TrimSpace(instructions) == "AFTER-BODY" && rev == after), "projection and complete observation revision must agree")
		probe := systools.NewSkillEditTool(deps).Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"})
		if rev == after {
			require.False(t, probe.IsError, "recorded seen must equal returned B: %s", probe.ForLLM)
			_, statErr := os.Stat(filepath.Join(dir, "notes.md"))
			assert.True(t, os.IsNotExist(statErr))
		} else {
			code, _ := errorBlock(t, probe)
			assert.Equal(t, "SKILL_CHANGED", code, "returned A must record A, not a later live B")
			assert.FileExists(t, filepath.Join(dir, "notes.md"))
		}
	})
}

func coherenceManagedWrite(root, revision string) error {
	_, err := skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindEdit, Slug: "foo", Path: "notes.md", Content: []byte("managed write\n"), Expect: skills.Expectation{Revision: revision}, Actor: skills.ChangeActor{Kind: "agent", AgentID: "writer", Tool: "write_file"}})
	return err
}
