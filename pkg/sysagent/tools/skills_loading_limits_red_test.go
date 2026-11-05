package systools_test

// r15 §5.2/§9.5, S-5.22/23 and S-8.3/9; CHECK-12.
// Fixtures are external input. Real metadata scan/get_skill are exercised;
// probe positive controls distinguish no reads from missing instrumentation.
import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSkill_FileCap201AndInstructionTruncation(t *testing.T) {
	// r15 §5.2 caps at 200; S-8.9 specifically supplies 250. Test both
	// neighboring boundaries and that scenario, counting SKILL.md as content.
	for _, count := range []int{199, 200, 201, 250} {
		t.Run(fmt.Sprintf("displayable-files-%d", count), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "skills")
			dir := validSkillFixture(t, root, "foo", "bounded projection")
			for i := 0; i < count-1; i++ {
				require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%03d.txt", i)), []byte("x"), 0o644))
			}
			require.NoError(t, os.MkdirAll(filepath.Join(dir, ".private"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".private", "hidden.md"), []byte("hidden"), 0o644))
			// >48,000 characters triggers the 8,000-character instruction cut;
			// instructions remain under MaxSkillMarkdownBytes; arrays stay uncut.
			require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: foo\ndescription: bounded projection\n---\n"+strings.Repeat("Z", 60000)), 0o644))
			deps := newSkillDeps(t, root, skillDepsOpts{agentID: "reader", grants: []string{"foo"}, policies: map[string]string{"get_skill": "allow"}})
			res := systools.NewGetSkillTool(deps).Execute(readCtx("reader"), map[string]any{"id": "foo", "fields": []any{"instructions", "files"}})
			m := parseToolJSON(t, res.ForLLM)
			require.NotContains(t, m, "error")
			files, ok := m["files"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, float64(count), files["total"], "dot content is hidden only from display")
			rows, ok := files["items"].([]any)
			require.True(t, ok)
			require.Len(t, rows, min(count, 200))
			for i, row := range rows {
				entry, ok := row.(map[string]any)
				require.True(t, ok)
				want := "SKILL.md"
				if i > 0 {
					want = fmt.Sprintf("file-%03d.txt", i-1)
				}
				assert.Equal(t, want, entry["path"])
				assert.Len(t, entry, 3, "path,size,exec only")
			}
			assert.Equal(t, []any{"instructions"}, m["truncated_fields"], "files is not silently shortened by result sizing")
			assert.Equal(t, strings.Repeat("Z", 8000)+"…[cut: 8,000 of 60,000 characters]", m["instructions"], "exact shared cut, no extra bytes between prefix and marker")
			assert.LessOrEqual(t, len([]rune(res.ForLLM)), 48000, "shared budget counts characters")
		})
	}
}

func TestSkillInstructionBound_MetadataAndGetSkill(t *testing.T) {
	const limit = 262144
	for _, n := range []int{limit - 1, limit, limit + 1} {
		t.Run(fmt.Sprintf("bytes-%d", n), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "skills")
			dir := validSkillFixture(t, root, "foo", "instruction bound")
			p := filepath.Join(dir, "SKILL.md")
			prefix := "---\nname: foo\ndescription: instruction bound\n---\n"
			require.NoError(t, os.WriteFile(p, []byte(prefix+strings.Repeat("X", n-len(prefix))), 0o644))
			deps := newSkillDeps(t, root, skillDepsOpts{agentID: "reader", grants: []string{"foo"}, policies: map[string]string{"get_skill": "allow"}})
			for _, surface := range []string{"metadata", "get_skill"} {
				t.Run(surface, func(t *testing.T) {
					var opens int
					var consumed int64
					oldOpen, oldRead := skills.CaptureOpenProbe, skills.CaptureConsumedProbe
					defer func() { skills.CaptureOpenProbe = oldOpen; skills.CaptureConsumedProbe = oldRead }()
					skills.CaptureOpenProbe = func(_, path string) {
						if path == p {
							opens++
						}
					}
					skills.CaptureConsumedProbe = func(path string, total int64) {
						if path == p && total > consumed {
							consumed = total
						}
					}
					if surface == "metadata" {
						rows := deps.SkillsLoader.ListSkills()
						found := false
						for _, row := range rows {
							if row.ID == "foo" {
								found = true
							}
						}
						assert.Equal(t, n <= limit, found, "menu discovery omits only oversized instructions")
					} else {
						res := systools.NewGetSkillTool(deps).Execute(readCtx("reader"), map[string]any{"id": "foo", "fields": []any{"instructions"}})
						if n > limit {
							code, msg := errorBlock(t, res)
							assert.Equal(t, "SKILL_INSTRUCTION_LIMIT", code)
							assert.Contains(t, msg, "262,144 bytes")
							assert.NotContains(t, res.ForLLM, strings.Repeat("X", 100))
						} else {
							m := parseToolJSON(t, res.ForLLM)
							assert.NotContains(t, m, "error")
						}
					}
					assert.Greater(t, opens, 0, "BLOCKED: real instruction-open instrumentation missing — r15 §9.5")
					assert.Greater(t, consumed, int64(0), "zero is not proof of a bounded real read")
					assert.LessOrEqual(t, consumed, int64(limit+65536), "bounded at limit plus at most one buffer")
				})
			}
		})
	}
}

// r15 S-5.22(b): positive read establishes real session state first; the
// next read grows at the actual open boundary and must clear that state.
func TestGetSkill_GrowingInstructionsBoundedAndSeenCleared(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	dir := validSkillFixture(t, root, "foo", "growth fixture")
	path := filepath.Join(dir, "SKILL.md")
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.md"), []byte("kept until recovery\n"), 0o644))
	deps := newSkillDeps(t, root, skillDepsOpts{agentID: "reader", grants: []string{"foo"}, policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
	ctx := skillSession(t, deps, "reader")
	get := systools.NewGetSkillTool(deps)
	read := func() *tools.ToolResult {
		return get.Execute(ctx, map[string]any{"id": "foo", "fields": []any{"instructions"}})
	}
	require.NotContains(t, parseToolJSON(t, read().ForLLM), "error", "positive read establishes real seen state")
	oldOpen, oldRead := skills.CaptureOpenProbe, skills.CaptureConsumedProbe
	defer func() { skills.CaptureOpenProbe, skills.CaptureConsumedProbe = oldOpen, oldRead }()
	fired := false
	var growErr error
	var consumed int64
	skills.CaptureOpenProbe = func(_, p string) {
		if p == path && !fired {
			fired = true
			growErr = os.WriteFile(path, append(append([]byte{}, original...), []byte(strings.Repeat("G", 262145))...), 0o644)
		}
	}
	skills.CaptureConsumedProbe = func(p string, n int64) {
		if p == path && n > consumed {
			consumed = n
		}
	}
	code, msg := errorBlock(t, read())
	assert.Equal(t, "SKILL_INSTRUCTION_LIMIT", code)
	assert.Contains(t, msg, "262,144 bytes")
	require.True(t, fired, "BLOCKED: real loading-path open probe not implemented — r15 §9.5")
	require.NoError(t, growErr)
	assert.Greater(t, consumed, int64(0), "real instruction read is observable")
	assert.LessOrEqual(t, consumed, int64(262144+65536), "all observed bounded-read counters, no unrestricted fallback")
	edit := systools.NewSkillEditTool(deps)
	res := edit.Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"})
	code, _ = errorBlock(t, res)
	assert.Equal(t, "SKILL_NOT_READ", code, "failed read clears the earlier session observation before mutation")
	assert.FileExists(t, filepath.Join(dir, "notes.md"))
	skills.CaptureOpenProbe, skills.CaptureConsumedProbe = oldOpen, oldRead
	require.NoError(t, os.WriteFile(path, original, 0o644))
	require.NotContains(t, parseToolJSON(t, read().ForLLM), "error", "repaired bounded content is readable again")
	res = edit.Execute(ctx, map[string]any{"action": "delete_file", "skill": "foo", "path": "notes.md"})
	assert.False(t, res.IsError, "successful recovery read establishes the new observation: %s", res.ForLLM)
}

// S-8.3's exact oversized fixture and all-fields projection, separate from
// the 201-file/selected-instructions cap case above. Arrays are never cut.
func TestGetSkill_AllFields200000CharacterInstructions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "skills")
	dir := validSkillFixture(t, root, "foo", "all-fields cut")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: foo\ndescription: all-fields cut\n---\n"+strings.Repeat("Z", 200000)), 0o644))
	deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
	res := systools.NewGetSkillTool(deps).Execute(readCtx("editor"), map[string]any{"id": "foo", "fields": []any{"all"}})
	m := parseToolJSON(t, res.ForLLM)
	require.NotContains(t, m, "error")
	assert.Equal(t, strings.Repeat("Z", 8000)+"…[cut: 8,000 of 200,000 characters]", m["instructions"])
	assert.Equal(t, []any{"instructions"}, m["truncated_fields"])
	files, ok := m["files"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1), files["total"])
	rows, ok := files["items"].([]any)
	require.True(t, ok)
	require.Len(t, rows, 1)
	versions, ok := m["versions"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"items": []any{}, "total": float64(0), "next_before": nil}, versions)
	assert.LessOrEqual(t, len([]rune(res.ForLLM)), 48000, "shared character bound, not a byte-count substitute")
}

func TestInstructionResolution_GetSkillCapturedDomain(t *testing.T) {
	for _, target := range []string{"included.md", ".versions/excluded.md", ".omnipus-identity", "../outside.md", "missing.md"} {
		t.Run(target, func(t *testing.T) {
			home := t.TempDir()
			root := filepath.Join(home, "skills")
			dir := validSkillFixture(t, root, "foo", "linked instructions")
			md := "---\nname: foo\ndescription: captured target\n---\nCAPTURED-INSTRUCTIONS\n"
			destination := filepath.Join(dir, filepath.FromSlash(target))
			if target != "missing.md" {
				require.NoError(t, os.MkdirAll(filepath.Dir(destination), 0o755))
				require.NoError(t, os.WriteFile(destination, []byte(md), 0o644))
			}
			require.NoError(t, os.Remove(filepath.Join(dir, "SKILL.md")))
			require.NoError(t, os.Symlink(target, filepath.Join(dir, "SKILL.md")))
			deps := newSkillDeps(t, root, skillDepsOpts{agentID: "editor", policies: map[string]string{"get_skill": "allow", "edit_skill": "allow"}})
			targetOpens := 0
			old := skills.CaptureOpenProbe
			defer func() { skills.CaptureOpenProbe = old }()
			skills.CaptureOpenProbe = func(_, path string) {
				if filepath.Clean(path) == filepath.Clean(destination) {
					targetOpens++
				}
			}
			res := systools.NewGetSkillTool(deps).Execute(readCtx("editor"), map[string]any{"id": "foo", "fields": []any{"instructions"}})
			if target == "included.md" {
				m := parseToolJSON(t, res.ForLLM)
				require.NotContains(t, m, "error")
				assert.Equal(t, "CAPTURED-INSTRUCTIONS\n", m["instructions"])
				assert.Greater(t, targetOpens, 0, "positive control: included target belongs to the real walk")
			} else {
				code, msg := errorBlock(t, res)
				assert.Equal(t, "SKILL_INSTRUCTION_UNRESOLVED", code)
				assert.Contains(t, msg, target)
				assert.Equal(t, 0, targetOpens, "excluded/outside/dangling target is never opened")
				assert.NotContains(t, res.ForLLM, "CAPTURED-INSTRUCTIONS")
			}
		})
	}
}
