//go:build darwin || linux

package gateway

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

type officeSecurityAuto struct{}

type officeSecurityApproval struct{ kinds []string }

// Only the independent no-kernel command approval may be accepted. A path
// widening request is DENIED, so an absent/broken stored path grant cannot
// be hidden by the UI edge granting a new one during the write control.
func (a *officeSecurityApproval) RequestShellApproval(_ context.Context, _, _, _, _, _ string, args map[string]any) (bool, string, bool) {
	kind, _ := args["adr092_kind"].(string)
	a.kinds = append(a.kinds, kind)
	return kind == "no_sandbox_ask", "path widening denied by fixture", false
}

func (officeSecurityAuto) ResolveShellMode(context.Context, string, string) tools.ShellMode {
	return tools.ShellModeAuto
}

// The commissioned RED/GREEN revisions changed ONLY this startup signature
// from cfg to AgentLoop. Invoke the real startup on either version; never
// replace its configuration/grant source with a test-computed approximation.
func officeSecurityBoot(t *testing.T, api *restAPI) *preview.Service {
	t.Helper()
	previous := preview.Shared()
	t.Cleanup(func() { preview.SetShared(previous) })
	boot := reflect.ValueOf(startDocumentPreview)
	argument := reflect.ValueOf(api.agentLoop)
	if boot.Type().In(1) == reflect.TypeOf((*config.Config)(nil)) {
		argument = reflect.ValueOf(api.agentLoop.GetConfig())
	}
	boot.Call([]reflect.Value{reflect.ValueOf(api.homePath), argument})
	svc := preview.Shared()
	require.NotNil(t, svc)
	return svc
}

func TestOfficeWriterExclusion_GatewaySeesOtherSessionGrant(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	t.Setenv("OMNIPUS_HOME", api.homePath)
	root := filepath.Join(t.TempDir(), "office")
	target := officepreview.TrustedOffice(t, api.homePath, root)
	svc := officeSecurityBoot(t, api)
	require.Equal(t, "ready", svc.ComponentStatus().Office)
	grants := api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants)
	work := t.TempDir()
	approver := &officeSecurityApproval{}
	tool, err := tools.NewExecToolWithDeps(work, true, api.agentLoop.GetConfig(), tools.ExecToolDeps{ApprovalGrants: grants, ShellMode: officeSecurityAuto{}, ApprovalRequester: approver})
	require.NoError(t, err)
	ctx := tools.WithTurnID(tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "mia"), "other-session"), "write-control")
	// Quote the argument, not the shell program. This is a harmless real
	// bash write through the actual injected approval store, not God Mode.
	command := "printf 'approved authority wrote' > '" + target + "'"
	denied := tool.Execute(ctx, map[string]any{"command": command, "timeout_seconds": 5})
	resolvedTarget, evalErr := filepath.EvalSymlinks(target)
	require.NoError(t, evalErr)
	resolvedRoot, evalErr := filepath.EvalSymlinks(root)
	require.NoError(t, evalErr)
	// §3.2: the write is refused where the access is created — after the
	// containment check and BEFORE requestPreflightApproval — so the
	// fs_preflight approval card must never be offered, with either answer.
	require.True(t, denied.IsError, "negative control: no stored path authority must deny the write")
	require.Equal(t, officepreview.ProgramShellRefusal(resolvedTarget, "LibreOffice", resolvedRoot), denied.ForLLM,
		"§3.2: the result is the fixed refusal sentence, not an approval denial")
	require.Empty(t, approver.kinds, "§3.2: no approval card for a program-folder write — the old expectation of an offered fs_preflight card is the defect this assertion guards against")
	original, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "original resource", string(original))
	grant := fspolicy.PathGrant{Path: target, Access: fspolicy.PathGrantAccessWrite}
	require.True(t, grants.RecordPathGrant("other-session", "mia", grant))
	require.Empty(t, grants.PathGrantsFor("preview-session", "jim"), "preview caller must not borrow the writer's grant")
	result := tool.Execute(ctx, map[string]any{"command": command, "timeout_seconds": 5})
	require.False(t, result.IsError, "actual bash grant must permit the write: %s", result.ForLLM)
	// The stored grant already contains this write, so §3.2 does not refuse
	// it and no path-widening card is needed: the ONLY card across both
	// executes is the independent no-kernel approval.
	require.Equal(t, []string{"no_sandbox_ask"}, approver.kinds, "the positive write must not get a fresh path-widening approval")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "approved authority wrote", string(data))
	officepreview.RequireWriterRefusal(t, svc)
}

func TestOfficeWriterExclusion_GatewaySeesConfigAddedAfterBoot(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	t.Cleanup(cleanup)
	t.Setenv("OMNIPUS_HOME", api.homePath)
	root := filepath.Join(t.TempDir(), "office")
	target := officepreview.TrustedOffice(t, api.homePath, root)
	svc := officeSecurityBoot(t, api)
	require.Equal(t, "ready", svc.ComponentStatus().Office)
	before := api.agentLoop.GetConfig()
	next, err := before.Clone()
	require.NoError(t, err)
	pattern := "^" + regexp.QuoteMeta(root) + "(?:/|$)"
	next.Tools.AllowWritePaths = []string{pattern}
	api.agentLoop.SwapConfig(next)
	require.NotSame(t, before, api.agentLoop.GetConfig(), "real reload seam replaces the pointer; mutating the boot pointer would hide this defect")
	require.Empty(t, before.Tools.AllowWritePaths, "old configuration must stay unchanged")
	writer := tools.NewWriteFileTool(t.TempDir(), true, []*regexp.Regexp{regexp.MustCompile(pattern)})
	result := writer.Execute(tools.WithAgentID(context.Background(), "mia"), map[string]any{"path": target, "content": "new config authority wrote", "overwrite": true})
	require.False(t, result.IsError, "new config actually authorizes a real file-tool write: %s", result.ForLLM)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "new config authority wrote", string(data))
	officepreview.RequireWriterRefusal(t, svc)
}
