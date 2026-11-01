package systools_test

// r15 §5.2/§5.11/§9.1, D-7, S-5.10/11/12/15; CHECK-04/12.
// All manifests/blobs below are produced by ApplyChange; tests only change
// created_at to supply the retention clock. Read/paging/restore are real.
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var readVersionID = regexp.MustCompile(`saved as skill version ([A-Za-z0-9-]+);`)

func projectionCommit(t *testing.T, root string, req skills.ChangeRequest) (skills.PublishOutcome, string) {
	t.Helper()
	out, err := skills.ApplyChange(root, req)
	if err != nil {
		t.Fatalf("BLOCKED: version-producing skill pipeline not implemented or refused valid fixture — required by r15 §7.1: %v", err)
	}
	match := readVersionID.FindStringSubmatch(out.Message)
	require.Len(t, match, 2, "actual committed version is named")
	return out, match[1]
}

func projectionHistory(t *testing.T, n int, deleted bool) (string, []string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "skills")
	ids := []string{}
	rev := ""
	if n == 0 {
		validSkillFixture(t, root, "foo", "paging fixture")
		return root, ids, rev
	}
	contentCount := n
	if deleted {
		contentCount--
	}
	for i := 0; i < contentCount; i++ {
		req := skills.ChangeRequest{Kind: skills.KindEdit, Slug: "foo", Path: "notes.md", Content: []byte(fmt.Sprintf("state %d\n", i)), Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "person", Tool: "rest-install"}}
		if i == 0 {
			req.Kind = skills.KindCreate
			req.Path = ""
			req.Content = []byte("---\nname: foo\ndescription: paging fixture\n---\nBody.\n")
			req.Expect = skills.Expectation{Absent: true}
		}
		out, id := projectionCommit(t, root, req)
		rev = out.Revision
		ids = append(ids, id)
	}
	if deleted {
		_, id := projectionCommit(t, root, skills.ChangeRequest{Kind: skills.KindDelete, Slug: "foo", Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "person", Tool: "REST DELETE"}})
		ids = append(ids, id)
		rev = ""
	}
	return root, ids, rev
}

func versionPage(t *testing.T, tool tools.Tool, ctxAgent string, args map[string]any) map[string]any {
	t.Helper()
	res := tool.Execute(readCtx(ctxAgent), args)
	m := parseToolJSON(t, res.ForLLM)
	require.NotContains(t, m, "error", "%s", res.ForLLM)
	page, ok := m["versions"].(map[string]any)
	require.True(t, ok)
	assert.Len(t, page, 3, "only items,total,next_before")
	return page
}

func pageIDs(t *testing.T, page map[string]any) []string {
	t.Helper()
	rows, ok := page["items"].([]any)
	require.True(t, ok)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		m, ok := row.(map[string]any)
		require.True(t, ok)
		id, ok := m["id"].(string)
		require.True(t, ok)
		ids = append(ids, id)
	}
	return ids
}

func TestGetSkill_VersionPaging_D7(t *testing.T) {
	for _, count := range []int{0, 1, 50, 51, 250} {
		t.Run(fmt.Sprintf("retained-%d", count), func(t *testing.T) {
			root, ascending, _ := projectionHistory(t, count, false)
			deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
			tool := systools.NewGetSkillTool(deps)
			newest := make([]string, len(ascending))
			for i, id := range ascending {
				newest[len(ascending)-1-i] = id
			}
			// D-7's full count × page-size × cursor matrix.
			for _, limit := range []int{-1, 1, 100, 0, 101} {
				cursors := []string{""}
				if count > 0 {
					cursors = append(cursors, newest[0], newest[len(newest)-1])
				}
				cursors = append(cursors, "v999999-20260101T000000Z")
				for _, cursor := range cursors {
					t.Run(fmt.Sprintf("limit-%d-before-%s", limit, cursor), func(t *testing.T) {
						args := map[string]any{"id": "foo", "fields": []any{"versions"}}
						if limit != -1 {
							args["versions_limit"] = limit
						}
						if cursor != "" {
							args["versions_before"] = cursor
						}
						if limit == 0 || limit == 101 || cursor == "v999999-20260101T000000Z" {
							code, msg := errorBlock(t, tool.Execute(readCtx("editor"), args))
							assert.Equal(t, "INVALID_INPUT", code)
							if limit != 0 && limit != 101 {
								assert.Equal(t, `unknown version "v999999-20260101T000000Z" for skill "foo"`, msg)
							}
							return
						}
						start := 0
						for i, id := range newest {
							if cursor == id {
								start = i + 1
							}
						}
						size := limit
						if size == -1 {
							size = 50
						}
						end := min(start+size, len(newest))
						want := newest[start:end]
						page := versionPage(t, tool, "editor", args)
						assert.Equal(t, float64(count), page["total"])
						assert.Equal(t, want, pageIDs(t, page))
						if end < len(newest) {
							require.NotEmpty(t, want)
							assert.Equal(t, want[len(want)-1], page["next_before"])
						} else {
							assert.Nil(t, page["next_before"])
						}
					})
				}
			}
			if count == 250 {
				got := []string{}
				cursor := ""
				for i := 0; i < 5; i++ {
					args := map[string]any{"id": "foo", "fields": []any{"versions"}}
					if cursor != "" {
						args["versions_before"] = cursor
					}
					page := versionPage(t, tool, "editor", args)
					assert.Equal(t, float64(250), page["total"])
					rows := pageIDs(t, page)
					require.Len(t, rows, 50)
					got = append(got, rows...)
					if i < 4 {
						var ok bool
						cursor, ok = page["next_before"].(string)
						require.True(t, ok)
					} else {
						assert.Nil(t, page["next_before"])
					}
				}
				assert.Equal(t, newest, got, "all 250 exactly once, in order")
				rev, err := skills.ContentRevision(filepath.Join(root, "foo"))
				require.NoError(t, err)
				_, err = skills.ApplyChange(root, skills.ChangeRequest{Kind: skills.KindRestore, Slug: "foo", Version: ascending[0], Expect: skills.Expectation{Revision: rev}, Actor: skills.ChangeActor{Kind: "person"}})
				require.NoError(t, err, "oldest returned version is actually restorable")
				raw, err := os.ReadFile(filepath.Join(root, "foo", "SKILL.md"))
				require.NoError(t, err)
				assert.Equal(t, "---\nname: foo\ndescription: paging fixture\n---\nBody.\n", string(raw))
				_, err = os.Stat(filepath.Join(root, "foo", "notes.md"))
				assert.True(t, os.IsNotExist(err))
			}
		})
	}
}

// Integer/range input classes supplement D-7's explicit 0/101 rows.
func TestGetSkill_VersionsLimitRejectsWrongTypeAndNegative(t *testing.T) {
	root, _, _ := projectionHistory(t, 0, false)
	deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
	for _, value := range []any{-1, 1.5, "50", true} {
		t.Run(fmt.Sprintf("%T-%v", value, value), func(t *testing.T) {
			res := systools.NewGetSkillTool(deps).Execute(readCtx("editor"), map[string]any{"id": "foo", "fields": []any{"versions"}, "versions_limit": value})
			code, msg := errorBlock(t, res)
			assert.Equal(t, "INVALID_INPUT", code)
			assert.Contains(t, msg, "versions_limit")
		})
	}
}

func TestGetSkill_DeletedVersionPaging120(t *testing.T) {
	root, ascending, _ := projectionHistory(t, 120, true)
	deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
	tool := systools.NewGetSkillTool(deps)
	got := []string{}
	cursor := ""
	for i := 0; i < 2; i++ {
		args := map[string]any{"id": "foo", "fields": []any{"state", "versions"}, "versions_limit": 100}
		if cursor != "" {
			args["versions_before"] = cursor
		}
		res := tool.Execute(readCtx("editor"), args)
		m := parseToolJSON(t, res.ForLLM)
		require.NotContains(t, m, "error")
		assert.Equal(t, "deleted", m["state"])
		assert.Nil(t, m["revision"])
		page, ok := m["versions"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, float64(120), page["total"])
		rows := pageIDs(t, page)
		if i == 0 {
			require.Len(t, rows, 100)
			cursor = rows[99]
			assert.Equal(t, cursor, page["next_before"])
		} else {
			assert.Len(t, rows, 20)
			assert.Nil(t, page["next_before"])
		}
		got = append(got, rows...)
	}
	require.Len(t, got, 120, "both deleted-history pages contain all versions before indexing")
	for i, id := range ascending {
		assert.Equal(t, id, got[119-i])
	}
}

func TestGetSkill_LastChangePathsSurviveParentPruning(t *testing.T) {
	root, ids, _ := projectionHistory(t, 2, false)
	hist := skills.SkillHistoryRoot(root)
	now := time.Now().UTC()
	for i, id := range ids {
		p := filepath.Join(hist, "foo", "versions", id+".json")
		raw, err := os.ReadFile(p)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(raw, &m))
		m["created_at"] = now.AddDate(0, 0, -200+i*100).Format("20060102T150405Z")
		raw, err = json.Marshal(m)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, raw, 0o644))
	}
	before, err := os.ReadFile(filepath.Join(hist, "foo", "versions", ids[1]+".json"))
	require.NoError(t, err)
	require.NoError(t, skills.RetentionSweep(hist, 90, false, now))
	_, err = os.Stat(filepath.Join(hist, "foo", "versions", ids[0]+".json"))
	require.True(t, os.IsNotExist(err), "instrument: parent really pruned")
	after, err := os.ReadFile(filepath.Join(hist, "foo", "versions", ids[1]+".json"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "retained manifest is not rewritten")
	deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
	res := systools.NewGetSkillTool(deps).Execute(readCtx("editor"), map[string]any{"id": "foo", "fields": []any{"versions", "last_change"}})
	m := parseToolJSON(t, res.ForLLM)
	require.NotContains(t, m, "error")
	change, ok := m["last_change"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []any{"notes.md"}, change["paths"])
	assert.NotContains(t, change, "unknown")
	page, ok := m["versions"].(map[string]any)
	require.True(t, ok)
	rows, ok := page["items"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 1)
	row, ok := rows[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, ids[1], row["id"])
	assert.Equal(t, []any{"notes.md"}, row["paths"])
	assert.Equal(t, float64(1), row["paths_total"])
}
