package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// Expected values in this file come from the ADR-092 D9 design
// (docs/internal/specs/adr-092-auto-for-other-tools-design.md §2, §3, §9)
// and the founder file /Users/danielpiatkowski/Desktop/auto-approve-choices.json
// (saved 2026-09-23T14:20:18Z), never from the implementation. The two
// lists below are that file's catalog entries, split by "choice".

var founderRunsNames = []string{
	"read_file",
	"list_directory",
	"write_file",
	"edit_file",
	"append_file",
	"grep",
	"library_list",
	"library_read",
	"list_mounts",
	"search_web",
	"fetch_url",
	"find_skills",
	"send_message",
	"send_file",
	"read_inbox",
	"search_email",
	"read_message",
	"delegate",
	"switch_agent",
	"message_parent",
	"list_agents",
	"create_plan",
	"execute_plan",
	"plan_correct",
	"stop_plan",
	"run_task",
	"create_task",
	"update_task",
	"list_tasks",
	"list_jobs",
	"inspect_session",
	"set_goal",
	"goal_claim",
	"set_todos",
	"AskUserQuestion",
	"Skill",
	"ToolSearch",
	"remember",
	"run_retrospective",
	"recall_memory",
	"recall_conversation",
	"knowledge_list",
	"knowledge_describe",
	"knowledge_find",
	"knowledge_read",
	"knowledge_edit",
	"knowledge_base_create",
	"knowledge_restructure",
	"knowledge_configure",
	"browser_navigate",
	"browser_open_tab",
	"browser_click",
	"browser_type",
	"browser_select_option",
	"browser_press_key",
	"browser_hover",
	"browser_handle_dialog",
	"browser_wait",
	"browser_get_text",
	"browser_snapshot",
	"browser_list_tabs",
	"browser_screenshot",
	"browser_switch_tab",
	"browser_close_tab",
	"browser_handover",
	"get_config",
	"get_usage",
	"list_providers",
	"list_models",
	"list_channels",
	"list_mcp_servers",
	"get_agent",
	"get_agent_tools",
	"read_agent_metadata",
	"list_workspaces",
	"get_workspace",
	"create_workspace",
	"list_tasks_in_workspace",
	"create_task_in_workspace",
	"update_task_in_workspace",
	"list_skills",
}

var founderAsksNames = []string{
	"request_mount",
	"install_skill",
	"environment_setup",
	"serve_web",
	"send_email",
	"reply",
	"delete_task",
	"browser_evaluate",
	"browser_upload_file",
	"set_config",
	"run_doctor",
	"configure_provider",
	"test_provider",
	"enable_channel",
	"disable_channel",
	"configure_channel",
	"test_channel",
	"add_mcp_server",
	"remove_mcp_server",
	"create_agent",
	"update_agent",
	"delete_agent",
	"update_workspace",
	"delete_workspace",
	"delete_task_in_workspace",
	"create_skill",
	"edit_skill",
	"remove_skill",
}

// founderRunsIfArgsNames are the §3 "runs" entries that carry the J2
// workspace path condition on their file argument (§3.8). browser_screenshot
// is in the table as runs-if; its classifier is lane L2's.
var founderRunsIfArgsNames = []string{
	"read_file", "list_directory", "write_file", "edit_file", "append_file", "send_file", "browser_screenshot",
}

func TestAutoApprove_ClassTableMatchesFounderFile(t *testing.T) {
	if len(founderRunsNames) != 81 || len(founderAsksNames) != 28 {
		t.Fatalf("founder lists: %d runs, %d asks; spec §3.8 says 81 and 28", len(founderRunsNames), len(founderAsksNames))
	}
	runsIf := map[string]bool{}
	for _, n := range founderRunsIfArgsNames {
		runsIf[n] = true
	}
	for _, n := range founderRunsNames {
		want := AutoRuns
		if runsIf[n] {
			want = AutoRunsIfArgs
		}
		if got := AutoApproveClassOf(n); got != want {
			t.Errorf("AutoApproveClassOf(%q) = %s, want %s", n, got, want)
		}
	}
	for _, n := range founderAsksNames {
		if got := AutoApproveClassOf(n); got != AutoAsks {
			t.Errorf("AutoApproveClassOf(%q) = %s, want asks", n, got)
		}
	}
	table := AutoApproveClassTable()
	// 109 catalog tools plus bash (§3.8 catalog total 110).
	if len(table) != 110 {
		t.Errorf("table has %d entries, want 110 (109 founder-file tools plus bash)", len(table))
	}
	if got := table["bash"]; got != AutoShellMode {
		t.Errorf("bash class = %s, want shell_mode (bash keeps its own mechanism, §3.7)", got)
	}
	for name := range table {
		if strings.HasPrefix(name, "mcp_") {
			t.Errorf("table key %q: MCP tools must never be listed (§4)", name)
		}
	}
	var zero AutoApproveClass
	if zero != AutoAsks {
		t.Error("the zero AutoApproveClass must be AutoAsks (§5.1)")
	}
	if got := AutoApproveClassOf("no_such_tool"); got != AutoAsks {
		t.Errorf("unknown tool class = %s, want asks", got)
	}
	table["delete_task"] = AutoRuns
	if AutoApproveClassOf("delete_task") != AutoAsks {
		t.Error("mutating the AutoApproveClassTable copy changed the live table")
	}
}

// fakeAutoClassifier is a Tool implementing AutoApproveClassifier with a
// fixed verdict, standing in for an MCP tool (lane L2).
type fakeAutoClassifier struct {
	BaseTool
	name    string
	verdict AutoVerdict
	panics  bool
}

func (f *fakeAutoClassifier) Name() string               { return f.name }
func (f *fakeAutoClassifier) Description() string        { return "fake" }
func (f *fakeAutoClassifier) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (f *fakeAutoClassifier) Scope() ToolScope           { return ScopeGeneral }
func (f *fakeAutoClassifier) Execute(context.Context, map[string]any) *ToolResult {
	return SilentResult("ok")
}
func (f *fakeAutoClassifier) AutoApproveVerdict(context.Context, map[string]any) AutoVerdict {
	if f.panics {
		panic("classifier exploded")
	}
	return f.verdict
}

func TestClassifyAutoApprove_Dispatch(t *testing.T) {
	ctx := context.Background()
	approve := AutoVerdict{Run: true, Class: AutoVerdictClassMCPNotDestructive, Reason: "read-only"}
	cases := []struct {
		name    string
		tool    string
		inst    Tool
		wantRun bool
	}{
		{"unconditional runs tool", "delegate", nil, true},
		{"ask-list tool asks", "delete_task", nil, false},
		{"ask-list tool asks even if its instance would approve", "set_config", &fakeAutoClassifier{name: "set_config", verdict: approve}, false},
		{"bash asks here: its own mechanism", "bash", nil, false},
		{"unknown non-MCP tool asks", "made_up_tool", &fakeAutoClassifier{name: "made_up_tool", verdict: approve}, false},
		{"runs-if tool with no instance asks", "write_file", nil, false},
		{"runs-if tool without a classifier asks", "write_file", &plainAutoTool{name: "write_file"}, false},
		{"MCP tool approved by its classifier runs", "mcp_srv_read", &fakeAutoClassifier{name: "mcp_srv_read", verdict: approve}, true},
		{"MCP tool whose classifier asks asks", "mcp_srv_rm", &fakeAutoClassifier{name: "mcp_srv_rm", verdict: AutoVerdict{Reason: "destructive"}}, false},
		{"MCP tool with no classifier asks", "mcp_srv_x", &plainAutoTool{name: "mcp_srv_x"}, false},
		{"panicking classifier asks", "mcp_srv_boom", &fakeAutoClassifier{name: "mcp_srv_boom", verdict: approve, panics: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ClassifyAutoApprove(ctx, tc.tool, tc.inst, map[string]any{})
			if v.Run != tc.wantRun {
				t.Fatalf("Run = %v, want %v (reason %q)", v.Run, tc.wantRun, v.Reason)
			}
			if !v.Run && v.Class != AutoVerdictClassAsks {
				t.Errorf("an asks verdict must carry class %q, got %q", AutoVerdictClassAsks, v.Class)
			}
			if !v.Run && v.Reason == "" {
				t.Error("an asks verdict must say why")
			}
		})
	}
}

// plainAutoTool is a Tool with no AutoApproveClassifier.
type plainAutoTool struct {
	BaseTool
	name string
}

func (p *plainAutoTool) Name() string               { return p.name }
func (p *plainAutoTool) Description() string        { return "plain" }
func (p *plainAutoTool) Parameters() map[string]any { return map[string]any{"type": "object"} }
func (p *plainAutoTool) Scope() ToolScope           { return ScopeGeneral }
func (p *plainAutoTool) Execute(context.Context, map[string]any) *ToolResult {
	return SilentResult("ok")
}

// autoFixture is a workspace (CoreTeam member agent) with one mount named
// "extra", plus a directory outside both — the §2 "Desktop" stand-in.
type autoFixture struct {
	home, work, mount, outside string
	ctx                        context.Context
}

func newAutoFixture(t *testing.T) *autoFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	const wsID, agentID = "ws-auto", "agent-auto"
	work := seedGrepWorkspace(t, home, wsID, agentID)
	mount := t.TempDir()
	if _, _, err := workspace.CreateMount(home, wsID, "extra", mount); err != nil {
		t.Fatalf("create mount: %v", err)
	}
	return &autoFixture{
		home:    home,
		work:    work,
		mount:   mount,
		outside: t.TempDir(),
		ctx:     WithTurnWorkspaceDir(WithAgentID(context.Background(), agentID), work),
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func realDir(t *testing.T, dir string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("realpath %s: %v", dir, err)
	}
	return r
}

func requireRuns(t *testing.T, v AutoVerdict, wantReal string, wantAccess uint64) {
	t.Helper()
	if !v.Run {
		t.Fatalf("verdict asks (%q), want runs", v.Reason)
	}
	if v.Class != AutoVerdictClassRunsIfArgs {
		t.Errorf("class = %q, want %q", v.Class, AutoVerdictClassRunsIfArgs)
	}
	if len(v.Paths) != 1 || v.Paths[0].Real != wantReal || v.Paths[0].Access != wantAccess {
		t.Errorf("pinned paths = %+v, want [{%s %b}]", v.Paths, wantReal, wantAccess)
	}
}

func requireAsks(t *testing.T, v AutoVerdict) {
	t.Helper()
	if v.Run {
		t.Fatalf("verdict runs (%+v), want asks", v)
	}
	if v.Reason == "" || len(v.Paths) != 0 {
		t.Errorf("asks verdict must carry a reason and no pinned paths: %+v", v)
	}
}

// T1 (classifier half, §9): write_file on Ask with Auto on — a work-folder
// path runs, a mount path runs, a path outside both asks. A work-folder call
// dispatched with its pin actually writes.
func TestAutoApprove_T1_WriteFile(t *testing.T) {
	f := newAutoFixture(t)
	tool := NewWriteFileTool(f.work, true)
	w := fspolicy.PathGrantAccessWrite

	inside := tool.AutoApproveVerdict(f.ctx, map[string]any{"path": "notes/a.md", "content": "x"})
	requireRuns(t, inside, filepath.Join(realDir(t, f.work), "notes", "a.md"), w)

	inMount := ClassifyAutoApprove(f.ctx, "write_file", tool, map[string]any{"path": "extra/b.md", "content": "x"})
	requireRuns(t, inMount, filepath.Join(realDir(t, f.mount), "b.md"), w)

	requireAsks(t, ClassifyAutoApprove(f.ctx, "write_file", tool,
		map[string]any{"path": filepath.Join(f.outside, "a.md"), "content": "x"}))
	requireAsks(t, ClassifyAutoApprove(f.ctx, "write_file", tool, map[string]any{"content": "x"}))

	if err := os.MkdirAll(filepath.Join(f.work, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	pinned := WithAutoApproved(f.ctx, AutoPinForVerdict("write_file", inside))
	res := tool.Execute(pinned, map[string]any{"path": "notes/a.md", "content": "auto-written"})
	if res.IsError {
		t.Fatalf("pinned in-workspace write failed: %s", res.ForLLM)
	}
	if got := mustRead(t, filepath.Join(f.work, "notes", "a.md")); got != "auto-written" {
		t.Errorf("file content = %q, want %q", got, "auto-written")
	}
}

// T2 (§9): read_file inside the work folder or a mount runs; /etc/hosts and
// any other path outside both asks (J2); a secret-set path asks, and the
// tool then refuses it even once a human approves.
func TestAutoApprove_T2_ReadFile(t *testing.T) {
	f := newAutoFixture(t)
	tool := NewReadFileTool(f.work, true, 0)
	r := fspolicy.PathGrantAccessRead
	mustWrite(t, filepath.Join(f.work, "in.txt"), "inside")
	mustWrite(t, filepath.Join(f.mount, "m.csv"), "a,b")
	mustWrite(t, filepath.Join(f.outside, "o.txt"), "outside")
	secret := filepath.Join(f.home, "credentials.json")
	mustWrite(t, secret, "SECRET-CREDENTIALS")

	requireRuns(t, ClassifyAutoApprove(f.ctx, "read_file", tool, map[string]any{"path": "in.txt"}),
		filepath.Join(realDir(t, f.work), "in.txt"), r)
	requireRuns(t, ClassifyAutoApprove(f.ctx, "read_file", tool, map[string]any{"path": "extra/m.csv"}),
		filepath.Join(realDir(t, f.mount), "m.csv"), r)
	requireAsks(t, ClassifyAutoApprove(f.ctx, "read_file", tool, map[string]any{"path": filepath.Join(f.outside, "o.txt")}))
	if _, err := os.Stat("/etc/hosts"); err == nil {
		requireAsks(t, ClassifyAutoApprove(f.ctx, "read_file", tool, map[string]any{"path": "/etc/hosts"}))
	}
	requireAsks(t, ClassifyAutoApprove(f.ctx, "read_file", tool, map[string]any{"path": secret}))

	// Human-approved (no pin): the secret is refused by the tool itself.
	res := tool.Execute(f.ctx, map[string]any{"path": secret})
	if !res.IsError || strings.Contains(res.ForLLM, "SECRET-CREDENTIALS") {
		t.Fatalf("secret read must be refused, got IsError=%v: %s", res.IsError, res.ForLLM)
	}
	// Human-approved outside read still works: J2 narrows Auto, not the tool.
	if res := tool.Execute(f.ctx, map[string]any{"path": filepath.Join(f.outside, "o.txt")}); res.IsError ||
		!strings.Contains(res.ForLLM, "outside") {
		t.Fatalf("an approved read outside the workspace must still succeed: %s", res.ForLLM)
	}
}

// swapToSymlink replaces path with a symlink to target, skipping the test
// where the platform refuses symlinks.
func swapToSymlink(t *testing.T, path, target string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove %s: %v", path, err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// T6 (§5.3, §9): the classifier approves a.md; before dispatch a.md becomes
// a symlink to a file outside the workspace; the pinned tool refuses and
// nothing is read or written.
func TestAutoApprove_T6_PinRecheckRefusesSwappedPath(t *testing.T) {
	t.Run("read_file", func(t *testing.T) {
		f := newAutoFixture(t)
		tool := NewReadFileTool(f.work, true, 0)
		a := filepath.Join(f.work, "a.md")
		mustWrite(t, a, "inside")
		secret := filepath.Join(f.outside, "secret.txt")
		mustWrite(t, secret, "OUTSIDE-SECRET")

		v := ClassifyAutoApprove(f.ctx, "read_file", tool, map[string]any{"path": "a.md"})
		requireRuns(t, v, filepath.Join(realDir(t, f.work), "a.md"), fspolicy.PathGrantAccessRead)
		swapToSymlink(t, a, secret)

		pinned := WithAutoApproved(f.ctx, AutoPinForVerdict("read_file", v))
		res := tool.Execute(pinned, map[string]any{"path": "a.md"})
		if !res.IsError || strings.Contains(res.ForLLM, "OUTSIDE-SECRET") {
			t.Fatalf("pinned read of a swapped symlink must be refused, got IsError=%v: %s", res.IsError, res.ForLLM)
		}
		if !errors.Is(res.Err, ErrAutoPinMoved) {
			t.Errorf("refusal must come from the pin re-check, got err %v", res.Err)
		}
	})
	t.Run("write_file", func(t *testing.T) {
		f := newAutoFixture(t)
		// Writes have two layers: ResolvePath's own write confinement already
		// refuses a leaf that now resolves outside the work folder and every
		// mount, and the pin re-check sits behind it. The read_file case
		// above is the one only the pin catches; this case locks the
		// spec's "nothing is written" outcome for writes.
		tool := NewWriteFileTool(f.work, true)
		a := filepath.Join(f.work, "a.md")
		mustWrite(t, a, "inside")
		target := filepath.Join(f.outside, "target.md")
		mustWrite(t, target, "untouched")

		v := ClassifyAutoApprove(f.ctx, "write_file", tool, map[string]any{"path": "a.md", "content": "x"})
		requireRuns(t, v, filepath.Join(realDir(t, f.work), "a.md"), fspolicy.PathGrantAccessWrite)
		swapToSymlink(t, a, target)

		pinned := WithAutoApproved(f.ctx, AutoPinForVerdict("write_file", v))
		res := tool.Execute(pinned, map[string]any{"path": "a.md", "content": "PWNED", "overwrite": true})
		if !res.IsError || !errors.Is(res.Err, ErrOutsideScope) {
			t.Fatalf("pinned write through a swapped symlink must be refused as outside scope, got IsError=%v err=%v: %s",
				res.IsError, res.Err, res.ForLLM)
		}
		if got := mustRead(t, target); got != "untouched" {
			t.Fatalf("outside target was written: %q", got)
		}
	})
}

// T7 (§2, §9): a path grant approved for bash (FR-036 overlay) does not make
// write_file to that path run under Auto, and does not rescue a pinned call
// whose path moved there.
func TestAutoApprove_T7_BashPathGrantDoesNotWiden(t *testing.T) {
	f := newAutoFixture(t)
	const sessionID, agentID = "sess-auto", "agent-auto"
	realOutside := realDir(t, f.outside)
	store := security.NewApprovalGrantStore()
	if !store.RecordPathGrant(sessionID, agentID, fspolicy.PathGrant{
		Path:   realOutside,
		Access: fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite,
	}) {
		t.Fatal("recording the bash path grant failed")
	}
	ctx := WithTranscriptSessionID(f.ctx, sessionID)
	bashPolicy, err := ResolveTurnFSPolicy(ctx, f.work, true, store.PathGrantsFor(sessionID, agentID))
	if err != nil {
		t.Fatalf("resolve the policy bash would see: %v", err)
	}
	if len(bashPolicy.PathGrants) == 0 {
		t.Fatal("precondition: the bash policy must carry the path grant")
	}
	target := filepath.Join(realOutside, "a.md")

	tool := NewWriteFileTool(f.work, true)
	requireAsks(t, ClassifyAutoApprove(ctx, "write_file", tool, map[string]any{"path": target, "content": "x"}))

	if covered, _ := autoPathCovered(bashPolicy, target); covered {
		t.Error("the J2 rule must ignore bash path grants on the policy")
	}
	pinned := WithAutoApproved(ctx, AutoPin{Tool: "write_file", Paths: []PinnedPath{
		{Real: filepath.Join(realDir(t, f.work), "a.md"), Access: fspolicy.PathGrantAccessWrite},
	}})
	if err := RecheckAutoPin(pinned, "write_file", bashPolicy, target, fspolicy.PathGrantAccessWrite); !errors.Is(err, ErrAutoPinMoved) {
		t.Errorf("re-check with a bash-granted outside path = %v, want ErrAutoPinMoved", err)
	}
}

// T10 (§3.2, §9): send_file of a workspace file on a non-web channel runs
// with no prompt and is delivered; a file outside the workspace asks.
func TestAutoApprove_T10_SendFile(t *testing.T) {
	f := newAutoFixture(t)
	store := media.NewFileMediaStore()
	tool := NewSendFileTool(f.work, true, 0, store)
	ctx := WithToolContext(f.ctx, "telegram", "chat-1")
	mustWrite(t, filepath.Join(f.work, "report.pdf"), "%PDF-1.4 report")
	mustWrite(t, filepath.Join(f.mount, "shared.txt"), "shared")
	mustWrite(t, filepath.Join(f.outside, "private.txt"), "private")

	v := ClassifyAutoApprove(ctx, "send_file", tool, map[string]any{"path": "report.pdf"})
	requireRuns(t, v, filepath.Join(realDir(t, f.work), "report.pdf"), fspolicy.PathGrantAccessRead)
	requireRuns(t, ClassifyAutoApprove(ctx, "send_file", tool, map[string]any{"path": "extra/shared.txt"}),
		filepath.Join(realDir(t, f.mount), "shared.txt"), fspolicy.PathGrantAccessRead)
	requireAsks(t, ClassifyAutoApprove(ctx, "send_file", tool,
		map[string]any{"path": filepath.Join(f.outside, "private.txt")}))
	requireAsks(t, ClassifyAutoApprove(ctx, "send_file", tool, map[string]any{"path": "  "}))

	res := tool.Execute(WithAutoApproved(ctx, AutoPinForVerdict("send_file", v)), map[string]any{"path": "report.pdf"})
	if res.IsError || len(res.Media) != 1 {
		t.Fatalf("auto-approved send_file must deliver one media ref, got IsError=%v media=%v: %s",
			res.IsError, res.Media, res.ForLLM)
	}
}

// The remaining RUNS-IF file tools (§3.1): list_directory (default path is
// the work folder), edit_file and append_file (read and write access).
func TestAutoApprove_OtherFileToolClassifiers(t *testing.T) {
	f := newAutoFixture(t)
	mustWrite(t, filepath.Join(f.work, "e.md"), "old")
	rw := fspolicy.PathGrantAccessRead | fspolicy.PathGrantAccessWrite
	workReal := realDir(t, f.work)
	outsideFile := filepath.Join(f.outside, "e.md")

	list := NewListDirTool(f.work, true)
	requireRuns(t, ClassifyAutoApprove(f.ctx, "list_directory", list, map[string]any{}), workReal, fspolicy.PathGrantAccessRead)
	requireRuns(t, ClassifyAutoApprove(f.ctx, "list_directory", list, map[string]any{"path": "extra"}),
		realDir(t, f.mount), fspolicy.PathGrantAccessRead)
	requireAsks(t, ClassifyAutoApprove(f.ctx, "list_directory", list, map[string]any{"path": f.outside}))

	edit := NewEditFileTool(f.work, true)
	requireRuns(t, ClassifyAutoApprove(f.ctx, "edit_file", edit, map[string]any{"path": "e.md"}), filepath.Join(workReal, "e.md"), rw)
	requireAsks(t, ClassifyAutoApprove(f.ctx, "edit_file", edit, map[string]any{"path": outsideFile}))

	appendTool := NewAppendFileTool(f.work, true)
	requireRuns(t, ClassifyAutoApprove(f.ctx, "append_file", appendTool, map[string]any{"path": "log.md"}),
		filepath.Join(workReal, "log.md"), rw)
	requireAsks(t, ClassifyAutoApprove(f.ctx, "append_file", appendTool, map[string]any{"path": outsideFile}))

	pinned := WithAutoApproved(f.ctx, AutoPinForVerdict("edit_file",
		ClassifyAutoApprove(f.ctx, "edit_file", edit, map[string]any{"path": "e.md"})))
	if res := edit.Execute(pinned, map[string]any{"path": "e.md", "old_text": "old", "new_text": "new"}); res.IsError {
		t.Fatalf("pinned in-workspace edit failed: %s", res.ForLLM)
	}
	if got := mustRead(t, filepath.Join(f.work, "e.md")); got != "new" {
		t.Errorf("edited content = %q, want %q", got, "new")
	}
}

func TestRecheckAutoPin_ScopeOfThePin(t *testing.T) {
	f := newAutoFixture(t)
	policy, err := ResolveTurnFSPolicy(f.ctx, f.work, true)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(realDir(t, f.outside), "x.md")
	inside := filepath.Join(realDir(t, f.work), "x.md")
	w := fspolicy.PathGrantAccessWrite

	if err := RecheckAutoPin(f.ctx, "write_file", policy, outside, w); err != nil {
		t.Errorf("no pin: the re-check must be a no-op, got %v", err)
	}
	otherTool := WithAutoApproved(f.ctx, AutoPin{Tool: "read_file"})
	if err := RecheckAutoPin(otherTool, "write_file", policy, outside, w); err != nil {
		t.Errorf("a pin for another tool must not apply, got %v", err)
	}
	readOnly := WithAutoApproved(f.ctx, AutoPin{Tool: "write_file", Paths: []PinnedPath{{Real: inside, Access: fspolicy.PathGrantAccessRead}}})
	if err := RecheckAutoPin(readOnly, "write_file", policy, inside, w); !errors.Is(err, ErrAutoPinMoved) {
		t.Errorf("access beyond the pinned access must be refused, got %v", err)
	}
	full := WithAutoApproved(f.ctx, AutoPin{Tool: "write_file", Paths: []PinnedPath{{Real: inside, Access: w}}})
	if err := RecheckAutoPin(full, "write_file", policy, inside, w); err != nil {
		t.Errorf("an in-workspace path under a matching pin must pass, got %v", err)
	}
	err = RecheckAutoPin(full, "write_file", policy, outside, w)
	if !errors.Is(err, ErrAutoPinMoved) || !errors.Is(err, ErrOutsideScope) {
		t.Errorf("an outside path under a pin must be refused with ErrAutoPinMoved and ErrOutsideScope, got %v", err)
	}
}
