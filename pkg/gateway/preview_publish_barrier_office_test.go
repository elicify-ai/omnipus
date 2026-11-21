//go:build darwin || linux

package gateway

// T-88 / §3.2 point 5 (FR-049, B-62, R4 closure): a refusal never depends on
// whether a preview runs. While a REAL preview.Service conversion is live,
// three overlapping write accesses are attempted at three deterministic
// barriers — after the component check (child parked before its first
// instruction; the owner record's "running" state is published before the
// go-byte, AD-8 step 3), after the first start ended 81 (the second start is
// parked at entry until released), and while the second target is
// demonstrably working — and each attempt must be refused with the §3.2
// sentence. The conversion itself must complete: the target runs, the PDF
// bytes reach the result, nothing is killed.
//
// Only the external converter is fake: it is this test binary at --headless
// (tests/officepreview's init already hosts launcher.ModeArg and
// extracttext.ModeArg here). The launcher, retry decision, scratch record,
// profile reseed, PDF reader, extract-text and the component check all run
// for real. Barrier timing is file-driven: every wait polls a file the child
// or the parent owns, with deadlines, never a fixed sleep against the parent.
//
// RED at a0ad39455: the conversion completes, and every refusal assertion
// fails — the approval is offered and the command runs, the mount is created,
// the config save succeeds — exactly the §3.2 defect this row exists to
// prevent. ("before the go-byte" is approximated by the earliest externally
// observable point of the live call: no production seam exists between the
// check and the go-byte, and adding one is production code, not a test.)

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	publishPlanFile  = "publish-plan.json"
	publishReleaseA  = "release-a"
	publishReleaseC  = "release-c"
	publishReleaseB  = "release-b"
	publishStart1    = "start-1"
	publishExiting81 = "exiting-81"
	publishStart2    = "start-2"
	publishWorking   = "working"
	publishDone      = "start-2-done"
)

// publishPDF is the fixture package's independently specified one-page PDF —
// the info stage really parses it, and the result must be exactly these bytes.
var publishPDF = officepreview.PDF(1)

// publishCoord is the test-owned coordination directory: all choreography
// markers and releases live there, NEVER in the production call dir — the
// gateway removes the call dir (AD-8) the moment Preview returns, before the
// test goroutine observes the result. Only out/document.pdf stays
// CWD-relative, because producing it is the converter's real contract.
var publishCoord = ""

func officePublishPath(name string) string { return filepath.Join(publishCoord, name) }

// The child polls for its plan: the parent writes it after the call dir
// exists, which can be before or after this process was spawned.
func officePublishChildProcess() int {
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, err := os.ReadFile(publishPlanFile)
		if err == nil {
			var plan struct {
				Coord string `json:"coord"`
			}
			if json.Unmarshal(raw, &plan) != nil || plan.Coord == "" {
				return 76
			}
			publishCoord = plan.Coord
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return 70
		}
		if time.Now().After(deadline) {
			return 71
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(officePublishPath(publishStart1)); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return 72
		}
		if os.WriteFile(officePublishPath(publishStart1), []byte("first start"), 0600) != nil {
			return 73
		}
		if officePublishWaitFor(publishReleaseA) != nil {
			officePublishTrace("start 1: release-a wait failed")
			return 74
		}
		if os.WriteFile(officePublishPath(publishExiting81), []byte("restart requested"), 0600) != nil {
			return 75
		}
		return 81
	}
	if os.WriteFile(officePublishPath(publishStart2), []byte("exit-81 retry start"), 0600) != nil {
		return 77
	}
	// Parked at entry until the parent's barrier-(c) attempts are done: the
	// second start cannot begin any work before release-c exists.
	if officePublishWaitFor(publishReleaseC) != nil {
		officePublishTrace("start 2: release-c wait failed")
		return 78
	}
	if os.WriteFile(officePublishPath(publishWorking), []byte("target is running"), 0600) != nil {
		return 79
	}
	if officePublishWaitFor(publishReleaseB) != nil {
		officePublishTrace("start 2: release-b wait failed")
		return 80
	}
	officePublishTrace("start 2: released, producing the PDF")
	if os.WriteFile(filepath.Join("out", "document.pdf"), publishPDF, 0600) != nil {
		officePublishTrace("write document.pdf failed")
		return 81
	}
	if os.WriteFile(officePublishPath(publishDone), []byte("target finished"), 0600) != nil {
		officePublishTrace("write start-2-done failed")
		return 82
	}
	return 0
}

// officePublishTrace appends the child's step progress to a file in the
// coordination directory so a parent-side wait timeout can show exactly how
// far the converter got. Diagnostic only; never asserted on.
func officePublishTrace(step string) {
	f, err := os.OpenFile(officePublishPath("publish-child-trace"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(step + "\n")
}

func officePublishWaitFor(name string) error {
	deadline := time.Now().Add(30 * time.Second)
	path := officePublishPath(name)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if time.Now().After(deadline) {
			return os.ErrDeadlineExceeded
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func init() {
	if len(os.Args) >= 2 && os.Args[1] == "--headless" {
		os.Exit(officePublishChildProcess())
	}
}

// publishShellMode resolves Auto for every call — the T-88 shell attempt must
// reach the D7 pre-flight exactly as an agent's Auto-mode call would.
type publishShellMode struct{}

func (publishShellMode) ResolveShellMode(context.Context, string, string) tools.ShellMode {
	return tools.ShellModeAuto
}

// publishApprover counts every consultation: the §3.2 rule is that an
// overlapping write is NEVER offered for approval, with either answer.
type publishApprover struct {
	calls int
}

func (a *publishApprover) RequestShellApproval(_ context.Context, _, _, _, _, _ string, _ map[string]any) (bool, string, bool) {
	a.calls++
	return true, "", true // scripted "Allow"
}

func waitForPublishFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if !time.Now().Before(deadline) {
			if entries, readErr := os.ReadDir(filepath.Dir(path)); readErr == nil {
				names := make([]string, 0, len(entries))
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Logf("call dir at timeout of %s: %v", filepath.Base(path), names)
				if trace, traceErr := os.ReadFile(filepath.Join(filepath.Dir(path), "publish-child-trace")); traceErr == nil {
					t.Logf("child trace:\n%s", trace)
				}
			}
			require.FailNow(t, "barrier wait", "the converter never reached %s", filepath.Base(path))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestOfficePreviewRefusesOverlapsPublishedDuringPreview(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	t.Setenv(config.EnvHome, api.homePath)
	home, err := filepath.EvalSymlinks(api.homePath)
	require.NoError(t, err)
	self, err := os.Executable()
	require.NoError(t, err)

	// The component root lives OUTSIDE Omnipus's home: registered manifests
	// are discovered from home, but a root under home would be refused by the
	// pre-existing secret-set and data-directory guards instead of reaching
	// the §3.2 refusal under test (and would mask the red).
	root := officepreview.RegisteredOfficeExecutable(t, home, filepath.Join(t.TempDir(), "office"), self)
	resolver := &component.Resolver{Home: home, Executable: self}
	office, problem := resolver.Resolve(component.KindOffice)
	require.Nil(t, problem, "instrument: the registered fixture verifies cleanly before the preview")
	require.Equal(t, root, office.Root, "instrument: the protected folder is the resolved component root")

	svc, err := preview.New(preview.Config{Home: home, Self: self, Resolver: resolver})
	require.NoError(t, err)

	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen})
	t.Cleanup(func() { sandbox.RegisterTurnPolicyBase(nil) })
	workDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	approver := &publishApprover{}
	grants := security.NewApprovalGrantStore()
	tool, err := tools.NewExecToolWithDeps(workDir, true, nil, tools.ExecToolDeps{
		ShellMode:         publishShellMode{},
		ApprovalRequester: approver,
		ApprovalGrants:    grants,
	})
	require.NoError(t, err)
	ctx := tools.WithTurnID(tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "agent-t88"), "session-t88"), "turn-t88")

	protected := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(protected, 0700))
	entry := officeAllowancePattern(root)

	// attempts drives the three real refusal surfaces once, labelled by the
	// barrier it belongs to.
	attempts := func(barrier string) {
		t.Helper()
		// Shell approval: never offered, never recorded, never run.
		command := "touch '" + filepath.Join(protected, "attempt") + "'"
		before := grants.AllPathGrants()
		result := tool.Execute(ctx, map[string]any{"command": command})
		assert.Equal(t, 0, approver.calls, "%s: the overlapping write must not reach the approver", barrier)
		assert.Equal(t, before, grants.AllPathGrants(), "%s: no grant may be recorded", barrier)
		assert.NoFileExists(t, filepath.Join(protected, "attempt"), "%s: the command must not run", barrier)
		require.NotNil(t, result, "%s", barrier)
		assert.True(t, result.IsError, "%s: the command result must be the §3.2 refusal", barrier)
		assert.Equal(t, officepreview.ProgramShellRefusal(filepath.Join(protected, "attempt"), "LibreOffice", root), result.ForLLM,
			"%s: exact §3.2 shell text", barrier)
		// Mount: refused where it is created — by the §3.2 overlap, never by
		// the older data-directory refusal (the target is outside home).
		_, _, mountErr := workspace.CheckMountTarget(protected, home)
		assert.ErrorIs(t, mountErr, workspace.ErrMountRefused, "%s: the overlapping mount must be refused", barrier)
		if mountErr != nil {
			assert.NotContains(t, mountErr.Error(), "data directory",
				"%s: the refusal must be the program-folder one, not the Omnipus-data-directory guard", barrier)
		}
		// Configuration: refused before any byte is written. assert, not
		// require: every barrier's evidence must land even at RED.
		saveBefore, readErr := os.ReadFile(api.configPath())
		require.NoError(t, readErr, "%s", barrier)
		saveErr := api.safeUpdateConfigJSON(func(m map[string]any) error {
			raw, ok := m["tools"].(map[string]any)
			if !ok {
				raw = map[string]any{}
				m["tools"] = raw
			}
			list, _ := raw["allow_write_paths"].([]any)
			raw["allow_write_paths"] = append(list, entry)
			return nil
		})
		if assert.Error(t, saveErr, "%s: the overlapping config save must be refused", barrier) {
			assert.Equal(t, officepreview.ConfigEntryRefusal("tools.allow_write_paths", entry, "LibreOffice", root), saveErr.Error(),
				"%s: exact §3.2 config text", barrier)
		}
		saveAfter, readErr := os.ReadFile(api.configPath())
		require.NoError(t, readErr, "%s", barrier)
		assert.Equal(t, saveBefore, saveAfter, "%s: a refused save must not write config.json", barrier)
	}

	ctx2, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	adm, err := svc.Admit(ctx2, true)
	require.NoError(t, err)
	defer adm.Release()

	type publishOutcome struct {
		result *preview.Result
		err    error
	}
	outcome := make(chan publishOutcome, 1)
	go func() {
		result, previewErr := svc.Preview(ctx2, adm, preview.Request{Name: "barrier.docx", Ext: ".docx", Data: []byte("authorized harmless input"), Default: true})
		outcome <- publishOutcome{result, previewErr}
	}()

	// §3.4 D1: owner.json is tmp/preview-calls/<id>/owner.json. The converter's
	// working folder is <id>/work, so the plan the child reads from its cwd is
	// written there, not beside the record.
	callDir := ""
	deadline := time.Now().Add(30 * time.Second)
	for callDir == "" {
		entries, readErr := os.ReadDir(filepath.Join(home, "tmp", "preview-calls"))
		if readErr == nil {
			for _, e := range entries {
				idDir := filepath.Join(home, "tmp", "preview-calls", e.Name())
				// work is created after the record (§3.4 D2). Wait for both so the
				// plan is written into a folder the converter can already see.
				workInfo, workErr := os.Stat(filepath.Join(idDir, "work"))
				if _, statErr := os.Stat(filepath.Join(idDir, "owner.json")); statErr == nil && workErr == nil && workInfo.IsDir() {
					callDir = idDir
				}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			require.NoError(t, readErr)
		}
		require.Less(t, time.Now().UnixNano(), deadline.UnixNano(),
			"§3.4 D1: owner.json never appeared at tmp/preview-calls/<id>/owner.json, one level above work")
		time.Sleep(2 * time.Millisecond)
	}
	// Choreography lives in a test-owned coordination directory: the gateway
	// removes the call dir when Preview returns, before the result is
	// observed, so nothing asserted after the conversion may live there.
	publishCoord = filepath.Join(t.TempDir(), "coord")
	require.NoError(t, os.MkdirAll(publishCoord, 0700))
	plan, err := json.Marshal(map[string]string{"coord": publishCoord})
	require.NoError(t, err)
	scratchWork := filepath.Join(callDir, "work")
	require.NoError(t, os.WriteFile(filepath.Join(scratchWork, publishPlanFile), plan, 0600))

	// Barrier (a): after the component check. The owner record's "running"
	// state is published before the go-byte, so reaching it proves the check
	// (which precedes conv.run) is behind us; the child is parked before its
	// first instruction.
	waitForPublishFile(t, officePublishPath(publishStart1))
	require.Eventually(t, func() bool {
		raw, readErr := os.ReadFile(filepath.Join(callDir, "owner.json"))
		return readErr == nil && strings.Contains(string(raw), `"state":"running"`)
	}, 10*time.Second, 2*time.Millisecond, "the call must reach its running record (component check done)")
	attempts("barrier a (after the component check, target parked at entry)")
	require.NoError(t, os.WriteFile(officePublishPath(publishReleaseA), []byte("go"), 0600))

	// Barrier (c): the first start has ended with exit 81; the attempts land
	// before the second start performs any work (it parks at entry until
	// release-c exists).
	waitForPublishFile(t, officePublishPath(publishExiting81))
	attempts("barrier c (after the first start ended, before the second start's work)")
	require.NoError(t, os.WriteFile(officePublishPath(publishReleaseC), []byte("go"), 0600))

	// Barrier (b): while the second target runs.
	waitForPublishFile(t, officePublishPath(publishWorking))
	attempts("barrier b (while the target runs)")
	require.NoError(t, os.WriteFile(officePublishPath(publishReleaseB), []byte("go"), 0600))

	select {
	case got := <-outcome:
		require.NoError(t, got.err, "R4: the preview must complete untouched by the refused overlaps")
		require.Equal(t, publishPDF, got.result.PDF, "the converted PDF must be exactly the target's output")
	case <-time.After(60 * time.Second):
		t.Fatal("the conversion never finished after all barriers were released")
	}
	waitForPublishFile(t, officePublishPath(publishDone))
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(callDir)
		return errors.Is(statErr, os.ErrNotExist)
	}, 10*time.Second, 2*time.Millisecond, "AD-8: the call's scratch folder is removed at the end")
}
