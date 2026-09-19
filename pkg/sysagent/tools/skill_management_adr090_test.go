package systools_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func writeManagementSkill(t *testing.T, root, id, body string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + id + "\ndescription: managed " + id + "\nauthor: public-author\n---\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func allowManagement(deps *systools.Deps, verdicts map[string]string) {
	deps.ResolveToolPolicy = func(agentID, toolName string) (string, bool) {
		if agentID != "ava" {
			return "deny", false
		}
		return verdicts[toolName], true
	}
}

func TestSkillListTool_ManagementRequiresComposedReadAndWriterPolicies(t *testing.T) {
	root := t.TempDir()
	writeManagementSkill(t, root, "release", "# Release\nSafe body.\n")
	deps, _ := newTestDeps()
	deps.SkillsLoader = skills.NewSkillsLoader("", root, "")
	tool := systools.NewSkillListTool(deps)
	ctx := tools.WithAgentID(context.Background(), "ava")

	cases := []struct {
		name     string
		policies map[string]string
		allowed  bool
	}{
		{"ask read and ask writer permits discovery", map[string]string{"list_skills": "ask", "edit_skill": "ask"}, true},
		{"denied read refuses", map[string]string{"list_skills": "deny", "edit_skill": "allow"}, false},
		{"malformed read verdict refuses", map[string]string{"list_skills": "", "edit_skill": "allow"}, false},
		{"all writers denied refuses", map[string]string{"list_skills": "allow", "create_agent": "deny", "update_agent": "deny", "create_skill": "deny", "edit_skill": "deny"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allowManagement(deps, tc.policies)
			result := tool.Execute(ctx, map[string]any{"scope": "management"})
			if tc.allowed && result.IsError {
				t.Fatalf("expected management read, got %s", result.ForLLM)
			}
			if !tc.allowed && !result.IsError {
				t.Fatalf("expected refusal, got %s", result.ForLLM)
			}
		})
	}

	allowManagement(deps, map[string]string{"list_skills": "allow", "edit_skill": "allow"})
	if result := tool.Execute(context.Background(), map[string]any{"scope": "management"}); !result.IsError {
		t.Fatalf("missing actor must fail closed: %s", result.ForLLM)
	}
}

func TestSkillListTool_ManagementNamedInspectionIsSanitizedAndUnfiltered(t *testing.T) {
	root := t.TempDir()
	writeManagementSkill(t, root, "release", "# Release\nSafe body.\n")
	deps, cfg := newTestDeps()
	deps.SkillsLoader = skills.NewSkillsLoader("", root, "")
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{ID: "ava", Skills: []string{}})
	allowManagement(deps, map[string]string{"list_skills": "allow", "edit_skill": "ask"})

	result := systools.NewSkillListTool(deps).Execute(
		tools.WithAgentID(context.Background(), "ava"),
		map[string]any{"scope": "management", "name": "release"},
	)
	if result.IsError {
		t.Fatal(result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Safe body.") || strings.Contains(result.ForLLM, "description: managed release") {
		t.Fatalf("expected body with frontmatter stripped: %s", result.ForLLM)
	}
	for _, forbidden := range []string{root, filepath.Join(root, "release"), "\"path\""} {
		if strings.Contains(result.ForLLM, forbidden) {
			t.Fatalf("response disclosed filesystem data %q: %s", forbidden, result.ForLLM)
		}
	}
	payload := parseSuccess(t, result.ForLLM)
	entries, entriesOK := payload["skills"].([]any)
	if !entriesOK {
		t.Fatalf("skills=%T %v, want array", payload["skills"], payload["skills"])
	}
	entry, entryOK := entries[0].(map[string]any)
	if !entryOK {
		t.Fatalf("skills[0]=%T %v, want object", entries[0], entries[0])
	}
	if entry["origin"] != "user" || entry["shared_impact"] != "all_agents" || entry["revision"] == "" {
		t.Fatalf("response lacks management metadata: %#v", entry)
	}
}

func TestSkillListTool_ManagementRejectsTraversalUnknownAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("---\nname: escaped\ndescription: outside\n---\nSECRET_TOKEN=do-not-return"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "escaped")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	deps, _ := newTestDeps()
	deps.SkillsLoader = skills.NewSkillsLoader("", root, "")
	allowManagement(deps, map[string]string{"list_skills": "allow", "create_skill": "allow"})
	tool := systools.NewSkillListTool(deps)
	ctx := tools.WithAgentID(context.Background(), "ava")

	for _, args := range []map[string]any{
		{"scope": "management", "name": "../secret"},
		{"scope": "management", "unexpected": true},
		{"scope": 7},
	} {
		if got := tool.Execute(ctx, args); !got.IsError {
			t.Fatalf("invalid arguments accepted: %#v => %s", args, got.ForLLM)
		}
	}
	got := tool.Execute(ctx, map[string]any{"scope": "management", "name": "escaped"})
	if !got.IsError || strings.Contains(got.ForLLM, "SECRET_TOKEN") || strings.Contains(got.ForLLM, outside) {
		t.Fatalf("symlink escape was not safely refused: %s", got.ForLLM)
	}
}

func TestSkillListTool_ManagementIncludesMountedProjectAndRefusesCollision(t *testing.T) {
	home := t.TempDir()
	wsID := "workspace-one"
	mountA, mountB := t.TempDir(), t.TempDir()
	for _, fixture := range []struct{ root, body string }{{mountA, "A"}, {mountB, "B"}} {
		skillDir := filepath.Join(fixture.root, ".omnipus", "skills", "deploy")
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: deploy\ndescription: project deploy\n---\n"+fixture.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, "workspaces"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "workspaces", wsID+".json"), []byte(`{"id":"workspace-one","is_default":false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "entities", "mounts"), 0o700); err != nil {
		t.Fatal(err)
	}
	record, _ := json.Marshal(testMountRecordForSkills{WorkspaceID: wsID, Mounts: []testMountForSkill{{Name: "a", HostPath: mountA}, {Name: "b", HostPath: mountB}}})
	if err := os.WriteFile(filepath.Join(home, "entities", "mounts", wsID+".json"), record, 0o600); err != nil {
		t.Fatal(err)
	}
	deps, _ := newTestDeps()
	deps.Home = home
	deps.SkillsLoader = skills.NewSkillsLoader("", t.TempDir(), "")
	allowManagement(deps, map[string]string{"list_skills": "allow", "update_agent": "allow"})
	ctx := tools.WithWorkspaceID(tools.WithAgentID(context.Background(), "ava"), wsID)
	tool := systools.NewSkillListTool(deps)

	listed := tool.Execute(ctx, map[string]any{"scope": "management"})
	if listed.IsError || !strings.Contains(listed.ForLLM, "\"origin\": \"project\"") || !strings.Contains(listed.ForLLM, "\"shared_impact\": \"workspace_members\"") {
		t.Fatalf("mounted project inventory missing: %s", listed.ForLLM)
	}
	inspected := tool.Execute(ctx, map[string]any{"scope": "management", "name": "deploy"})
	if !inspected.IsError || !strings.Contains(inspected.ForLLM, "AMBIGUOUS") || strings.Contains(inspected.ForLLM, mountA) || strings.Contains(inspected.ForLLM, mountB) {
		t.Fatalf("collision was not sanitized and refused: %s", inspected.ForLLM)
	}
}

func TestSkillListTool_ManagementContentAndRevisionShareMutationLock(t *testing.T) {
	root := t.TempDir()
	writeManagementSkill(t, root, "release", "old body\n")
	deps, _ := newTestDeps()
	deps.SkillsLoader = skills.NewSkillsLoader("", root, "")
	allowManagement(deps, map[string]string{"list_skills": "allow", "edit_skill": "allow"})
	tool := systools.NewSkillListTool(deps)
	ctx := tools.WithAgentID(context.Background(), "ava")

	locked := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = skills.WithMutationLock(root, func() error {
			if err := os.WriteFile(filepath.Join(root, "release", "SKILL.md"), []byte("---\nname: release\ndescription: managed release\n---\nnew body\n"), 0o644); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	resultCh := make(chan *tools.ToolResult, 1)
	go func() {
		resultCh <- tool.Execute(ctx, map[string]any{"scope": "management", "name": "release"})
	}()
	select {
	case result := <-resultCh:
		t.Fatalf("management read bypassed the skill mutation lock: %s", result.ForLLM)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	result := <-resultCh
	if result.IsError || !strings.Contains(result.ForLLM, "new body") || strings.Contains(result.ForLLM, "old body") {
		t.Fatalf("management read did not return the published snapshot: %s", result.ForLLM)
	}
	wantRevision, err := skills.NewSkillWriter(root).SkillRevision("release")
	if err != nil {
		t.Fatal(err)
	}
	payload := parseSuccess(t, result.ForLLM)
	entries, entriesOK := payload["skills"].([]any)
	if !entriesOK {
		t.Fatalf("skills=%T %v, want array", payload["skills"], payload["skills"])
	}
	entry, entryOK := entries[0].(map[string]any)
	if !entryOK {
		t.Fatalf("skills[0]=%T %v, want object", entries[0], entries[0])
	}
	if entry["revision"] != wantRevision {
		t.Fatalf("content/revision snapshot mismatch: got %v want %s", entry["revision"], wantRevision)
	}
}
