//go:build goolm && stdjson

package gateway

// Fix round 4, part E item 4 (team-lead) — FR-056, the `copy` half:
// "`install_skill` and `edit_skill` `copy` MUST accept an optional `category`
// from `SkillCategory` and refuse other values naming the allowed ones"
// (skills-tools-redesign-spec.md, FR-056). The install_skill half is
// pkg/tools/skill_install_category_red_test.go::TestInstallSkillCategoryParity;
// until now nothing covered the edit_skill copy half (the backend's handoff-5,
// item 4, says so).
//
// Oracles, all from the spec and none from the implementation:
//   - C-21, the "skill category" row: `edit_skill` `copy` gains `category`.
//   - C-5: the category is stored in Omnipus's own data, outside the skill
//     folder (R32: never written into the skill file); the `Skill` listing
//     carries it (`GET /skills`, the read the person's screen uses, S-71/S-104).
//   - C-5's `SkillCategory` enum is the allowed set: tools, business,
//     development, testing_security, data_ai, devops, documentation,
//     content_media, research, lifestyle, databases, blockchain, uncategorised.
//   - S-72 / Dataset D5 row 5, the same refusal for install_skill: "refused
//     naming the allowed categories and nothing is installed".
//   - C-21 / S-104: a category can be chosen once, only while the stored
//     category is `uncategorised` (PUT /skills/{name}/category).
//
// Harness: the real edit_skill tool over the REST API's own skills folder; the
// read-back is the real `GET /skills`. Only the policy edge is a stub (the
// same one pkg/sysagent/tools' own copy tests use). No helper stores, lists or
// refuses a category on the implementation's behalf.
//
// What the spec does not say (reported, not pinned here): the category an
// `edit_skill` copy gets when `category` is omitted. "default: the source's"
// is written only on the REST copy row of C-21.
//
// RED-ness: NOT shown by a run. The backend already carried the parameter
// (3df34ca36) when this test was written, and every proof run (fix4e-*,
// fix4e3-*) started at or after it, so no failing run of these three tests
// exists. The expectation that a build without the parameter fails them is
// Inferred from the tool's earlier schema (r15 §5.4: five parameters, `category`
// refused as unknown, so the stored-category and the uncategorised cases would
// fail on that refusal; the unknown-category case would fail because a refusal
// of an unknown parameter does not name the allowed categories). Proof of
// failability (a mutant that drops the parameter, skips the enum check or
// writes the category into the skill file), GREEN and mutation probes are
// CHECK's.

import (
	"context"
	"net/http"
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

// copyCategoryAllowed is C-5's SkillCategory enum, in the spec's order.
var copyCategoryAllowed = []string{
	"tools", "business", "development", "testing_security", "data_ai", "devops",
	"documentation", "content_media", "research", "lifestyle", "databases",
	"blockchain", "uncategorised",
}

// seedCopyCategorySource installs the plain skill every case copies and returns
// its exact SKILL.md text.
func seedCopyCategorySource(t *testing.T, api *restAPI) string {
	t.Helper()
	root := filepath.Join(api.homePath, "skills")
	seedSkill(t, root, "source",
		"name: source\ndescription: A valid copy source with a long enough description to pass.",
		"# Source\n\nBody of the source.")
	raw, err := os.ReadFile(filepath.Join(root, "source", "SKILL.md"))
	require.NoError(t, err)
	return string(raw)
}

// copyCategoryCall runs the real edit_skill tool: copy `source` to `to`, with
// `category` only when it is non-empty (absent = the optional parameter left out).
func copyCategoryCall(t *testing.T, api *restAPI, to, category string) *tools.ToolResult {
	t.Helper()
	root := filepath.Join(api.homePath, "skills")
	deps := sysDepsFor(t, api)
	deps.SkillsLoader = skills.NewSkillsLoader("", root, os.Getenv(config.EnvBuiltinSkills))
	deps.SkillWriter = skills.NewSkillWriter(root)
	deps.ResolveToolPolicy = func(_, _ string) (string, bool) { return "allow", true }
	args := map[string]any{"action": "copy", "skill": "source", "to": to}
	if category != "" {
		args["category"] = category
	}
	ctx := tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "mia"), "fr056-copy-"+to)
	return systools.NewSkillEditTool(deps).Execute(ctx, args)
}

// FR-056, C-21, C-5: a copy made with a category is listed under it, the choice
// lives outside the skill file, and the source is left alone.
func TestEditSkillCopy_CategoryIsStoredOutsideTheSkillFile(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	original := seedCopyCategorySource(t, api)

	res := copyCategoryCall(t, api, "research-copy", "research")
	require.False(t, res.IsError,
		"FR-056/C-21: edit_skill copy accepts an optional category from SkillCategory; it refused: %s", res.ForLLM)

	require.Equal(t, "research", listedCategory(t, api, "research-copy"),
		"FR-056/C-21/C-5: GET /skills lists the copy under the category the agent chose")

	copied, err := os.ReadFile(filepath.Join(api.homePath, "skills", "research-copy", "SKILL.md"))
	require.NoError(t, err, "the copy exists")
	assert.Equal(t, strings.Replace(original, "name: source", "name: research-copy", 1), string(copied),
		"C-5/R32: the category is never written into the skill file; the copy differs from its source only by the name")

	kept, err := os.ReadFile(filepath.Join(api.homePath, "skills", "source", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, original, string(kept), "copying never changes the source skill")
	assert.NotEqual(t, "research", listedCategory(t, api, "source"),
		"the chosen category belongs to the copy, not to the source it was copied from")
}

// FR-056, S-72, Dataset D5 row 5 (the same refusal for install_skill): a value
// outside SkillCategory is refused, the refusal names every allowed category,
// and nothing is copied.
func TestEditSkillCopy_UnknownCategoryIsRefusedBeforeAnythingIsCopied(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	original := seedCopyCategorySource(t, api)

	res := copyCategoryCall(t, api, "games-copy", "games")
	require.True(t, res.IsError,
		"FR-056/S-72: category `games` is not in SkillCategory and must be refused; got success: %s", res.ForLLM)
	for _, allowed := range copyCategoryAllowed {
		assert.Contains(t, res.ForLLM, allowed,
			"FR-056/S-72: the refusal names every allowed category; %q is missing from: %s", allowed, res.ForLLM)
	}

	assert.NoDirExists(t, filepath.Join(api.homePath, "skills", "games-copy"),
		"FR-056: the refusal comes before anything is copied")
	assert.NotContains(t, listSkillsBody(t, api), "games-copy", "no copy is listed after the refusal")
	kept, err := os.ReadFile(filepath.Join(api.homePath, "skills", "source", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, original, string(kept), "a refused copy leaves the source as it was")
}

// FR-056 boundary: `uncategorised` is itself a member of SkillCategory (C-5), so
// it is accepted on copy. It is not a choice: C-21 lets a category be chosen
// once, only while the stored category is `uncategorised`, so the one choice is
// still open afterwards.
func TestEditSkillCopy_UncategorisedIsAcceptedAndLeavesTheChoiceOpen(t *testing.T) {
	api := newTestRestAPIWithSkillsDirs(t, t.TempDir())
	seedCopyCategorySource(t, api)

	res := copyCategoryCall(t, api, "open-copy", "uncategorised")
	require.False(t, res.IsError,
		"FR-056/C-5: `uncategorised` is in SkillCategory and is accepted on copy; it refused: %s", res.ForLLM)
	require.DirExists(t, filepath.Join(api.homePath, "skills", "open-copy"), "the copy was made")

	w := putCategory(t, api, "open-copy", "lifestyle")
	require.Equal(t, http.StatusOK, w.Code,
		"C-21/S-104: the copy is still Uncategorised, so its one category choice is open; got %d, body: %s",
		w.Code, w.Body.String())
	assert.Equal(t, "lifestyle", listedCategory(t, api, "open-copy"),
		"C-21: the one choice is saved")
}
