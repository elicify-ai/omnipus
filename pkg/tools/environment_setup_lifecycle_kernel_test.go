//go:build !windows

package tools

// environment_setup_lifecycle_kernel_test.go — the REAL-kernel confinement
// fixture for the environment_setup runner (CRIT-1 regression guard). A real
// child of THIS tool runs under the ACTUAL per-turn kernel policy
// (non-god-mode harness): darwin wraps via the installed Seatbelt backend +
// per-turn ApplyToCmd; Linux applies the derived Landlock domain on the
// launching thread (StartLockedWithPolicy — the CRIT-1 call site itself).
//
// RegisterTurnPolicyBase and the Seatbelt backend are PROCESS-GLOBAL, and
// other pkg/tools tests spawn non-god-mode children — so the fixture re-execs
// the test binary with a sentinel env (the inner process installs the global
// state; the pollution dies with it). The inner writes a JSON probe report to
// a path passed through the environment; the outer asserts per-platform.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

const (
	kernelFixtureSentinel  = "OMNIPUS_ENV_SETUP_KERNEL_FIXTURE"
	kernelFixtureReportEnv = "OMNIPUS_ENV_SETUP_KERNEL_REPORT"
)

// kernelProbeReport carries the inner process's REAL probe outcomes to the
// outer process. Values are "ok", "denied", or "error:<text>".
type kernelProbeReport struct {
	Supported        bool   `json:"supported"`
	SkipReason       string `json:"skip_reason,omitempty"`
	WriteInside      string `json:"write_inside"`
	WriteSibling     string `json:"write_sibling"`
	WriteOperator    string `json:"write_operator"`
	ReadCredentials  string `json:"read_credentials"`
	ReadSibling      string `json:"read_sibling"`
	ReadOwnManifest  string `json:"read_own_manifest"`
	ReadWorkSource   string `json:"read_work_source"`
	ReadInsidePrefix string `json:"read_inside_prefix"`
	Detail           string `json:"detail,omitempty"`
}

func TestEnvironmentSetup_Confinement_RealKernel(t *testing.T) {
	if os.Getenv(kernelFixtureSentinel) == "1" {
		kernelFixtureInner(t)
		return
	}

	// Outer process: re-exec self so the inner can install process-global
	// sandbox state without leaking it into this process's other tests.
	reportPath := filepath.Join(t.TempDir(), "probe-report.json")
	cmd := exec.Command(os.Args[0], "-test.run", "^TestEnvironmentSetup_Confinement_RealKernel$")
	cmd.Env = append(os.Environ(),
		kernelFixtureSentinel+"=1",
		kernelFixtureReportEnv+"="+reportPath)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "inner fixture process failed:\n%s", out)

	raw, rerr := os.ReadFile(reportPath)
	require.NoError(t, rerr, "inner wrote no probe report; output:\n%s", out)
	var rep kernelProbeReport
	require.NoError(t, json.Unmarshal(raw, &rep), "bad probe report %s: %s", raw, out)

	require.True(t, rep.Supported,
		"kernel confinement could not be exercised on this host: %s %s", rep.SkipReason, rep.Detail)

	// Positive controls (both enforcing platforms): a sandbox that broke
	// everything cannot false-green the negatives below.
	assert.Equal(t, "ok", rep.WriteInside,
		"the child must be able to write its own installation prefix")
	assert.Equal(t, "ok", rep.ReadInsidePrefix,
		"the child must be able to read its own installation prefix")
	assert.Equal(t, "ok", rep.ReadWorkSource,
		"the child must be able to read an authorized source file under the target work root")

	// Negative control (both platforms): the per-turn kernel policy must deny
	// a write to a sibling workspace. THIS is the CRIT-1 regression guard —
	// under the old StartLocked call the child fell back to the boot-global
	// posture and this write leaked.
	assert.Equal(t, "denied", rep.WriteSibling,
		"the per-turn kernel policy must deny writes to a sibling workspace (CRIT-1)")
	assert.Equal(t, "denied", rep.WriteOperator,
		"setup must discard inherited operator write grants outside prefix/cache/tmp")

	if runtime.GOOS == "darwin" {
		// Seatbelt can express read denial; the fspolicy secret set makes
		// gateway secrets, sibling workspaces, AND the own workspace's
		// records outside the work dir read-denied — the own-tree exception
		// is anchored on the work dir (the prefix), not the workspace
		// (pkg/fspolicy/secretset_kernel.go documents this exact case).
		assert.Equal(t, "denied", rep.ReadCredentials,
			"credentials.json must be read-denied under the real per-turn policy")
		assert.Equal(t, "denied", rep.ReadSibling,
			"sibling-workspace files must be read-denied under the real per-turn policy")
		assert.Equal(t, "denied", rep.ReadOwnManifest,
			"the own workspace's record outside the prefix is read-denied by design (work-dir anchoring)")
	} else {
		// Linux: Landlock is write/exec-only — read denial is not
		// expressible, reads are open by the documented model (ADR-062).
		// Record the outcomes as evidence without asserting a denial the
		// platform cannot enforce.
		t.Logf("linux read probes are informational (Landlock cannot deny reads): "+
			"credentials=%s sibling=%s own-manifest=%s inside-prefix=%s",
			rep.ReadCredentials, rep.ReadSibling, rep.ReadOwnManifest, rep.ReadInsidePrefix)
	}
}

// kernelFixtureInner runs INSIDE the re-exec'd process: it installs the real
// per-turn kernel policy chain, runs a real child of the tool (non-god), and
// writes the probe report for the outer process to judge.
func kernelFixtureInner(t *testing.T) {
	reportPath := os.Getenv(kernelFixtureReportEnv)
	if reportPath == "" {
		t.Fatal("inner fixture: no report path env")
	}
	writeReport := func(rep kernelProbeReport) {
		data, err := json.Marshal(rep)
		if err != nil {
			t.Fatalf("marshal probe report: %v", err)
		}
		if werr := os.WriteFile(reportPath, append(data, '\n'), 0o600); werr != nil {
			t.Fatalf("write probe report: %v", werr)
		}
	}

	home := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(home, "credentials.json"), []byte("secret-sentinel"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces", "sib"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", "sib", "sentinel.txt"), []byte("sibling-sentinel"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces", "alpha", "work"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", "alpha", "manifest.json"), []byte("own-manifest"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", "alpha", "work", "source-manifest.txt"), []byte("authorized-source"), 0o600))
	operatorDir := filepath.Join(home, "operator-write-grant")
	require.NoError(t, os.MkdirAll(operatorDir, 0o755))
	agentDir := filepath.Join(home, "agents", "testagent")
	require.NoError(t, os.MkdirAll(agentDir, 0o755))
	wsAlpha := filepath.Join(home, "workspaces", "alpha", "work")

	prefix := filepath.Join(wsAlpha, ".omnipus", "env")
	require.NoError(t, os.MkdirAll(prefix, 0o755))
	target := &lifecycleTarget{
		prefix: prefix,
		cache:  filepath.Join(prefix, "cache"),
		tmp:    filepath.Join(prefix, "tmp"),
	}

	// The REAL per-turn chain, exactly as the gateway boots it: the base
	// registered once, and (darwin) the Seatbelt backend installed globally.
	sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{HomePath: home, Model: sandbox.FilesystemModelOpen, AllowedPaths: []string{operatorDir}})
	installKernelTestBackend(t, home)

	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{
		Home:         home,
		AgentWorkDir: agentDir,
		Admin:        false,
		GodMode:      false, // THE POINT: the real per-turn kernel policy
		Store:        lifecycleStore{target: target},
	})

	ctx := WithToolContext(t.Context(), "cli", "chat")
	ctx = WithAgentID(ctx, "mia")
	ctx = WithWorkspaceID(ctx, "alpha")
	// The turn workspace dir is what own-tree re-admission keys on — the
	// production loop always carries it, so the fixture must too, or the
	// policy would deny the fixture's OWN workspace reads.
	ctx = WithTurnWorkspaceDir(ctx, wsAlpha)
	ctx = WithTranscriptSessionID(ctx, "t-kernel-1")

	script := `echo ok > "$OMNIPUS_ENV_PREFIX/inside.txt" 2>/dev/null && echo "PROBE write_inside=ok" || echo "PROBE write_inside=denied"
echo leak > "` + home + `/workspaces/sib/leak.txt" 2>/dev/null && echo "PROBE write_sibling=ok" || echo "PROBE write_sibling=denied"
echo leak > "` + operatorDir + `/leak.txt" 2>/dev/null && echo "PROBE write_operator=ok" || echo "PROBE write_operator=denied"
cat "` + home + `/credentials.json" > "$OMNIPUS_ENV_PREFIX/cred-copy.txt" 2>/dev/null && echo "PROBE read_credentials=ok" || echo "PROBE read_credentials=denied"
cat "` + home + `/workspaces/sib/sentinel.txt" > "$OMNIPUS_ENV_PREFIX/sib-copy.txt" 2>/dev/null && echo "PROBE read_sibling=ok" || echo "PROBE read_sibling=denied"
cat "` + home + `/workspaces/alpha/manifest.json" > "$OMNIPUS_ENV_PREFIX/manifest-copy.txt" 2>/dev/null && echo "PROBE read_own_manifest=ok" || echo "PROBE read_own_manifest=denied"
cat "` + home + `/workspaces/alpha/work/source-manifest.txt" > "$OMNIPUS_ENV_PREFIX/source-copy.txt" 2>/dev/null && echo "PROBE read_work_source=ok" || echo "PROBE read_work_source=denied"
cat "$OMNIPUS_ENV_PREFIX/inside.txt" > "$OMNIPUS_ENV_PREFIX/inside-copy.txt" 2>/dev/null && echo "PROBE read_inside_prefix=ok" || echo "PROBE read_inside_prefix=denied"
exit 0
`

	done := make(chan *ToolResult, 1)
	start := tool.ExecuteAsync(ctx, map[string]any{
		"command":         script,
		"purpose":         "real-kernel confinement probes",
		"scope":           "shared",
		"timeout_seconds": float64(20),
	}, func(_ context.Context, result *ToolResult) { done <- result })
	if start == nil {
		writeReport(kernelProbeReport{SkipReason: "ExecuteAsync returned nil start"})
		t.FailNow()
	}
	if start.IsError {
		// A hardening/start refusal (e.g. kernel older than Landlock needs)
		// is a truthful platform limitation, not a green.
		writeReport(kernelProbeReport{SkipReason: "start refused", Detail: start.ForLLM})
		t.Skipf("inner fixture could not start: %s", start.ForLLM)
	}

	// Wait for the terminal completion, then parse the REAL probe outcomes
	// from the child's captured output (stdout — never a file inside the
	// sandbox, which the first negative probe could make unwritable).
	var completion *ToolResult
	select {
	case completion = <-done:
	case <-time.After(60 * time.Second):
		writeReport(kernelProbeReport{SkipReason: "completion callback never fired"})
		t.FailNow()
	}
	outText := ""
	if completion != nil {
		outText = completion.ForLLM
	}

	probe := func(name string) string {
		switch {
		case strings.Contains(outText, "PROBE "+name+"=ok"):
			return "ok"
		case strings.Contains(outText, "PROBE "+name+"=denied"):
			return "denied"
		default:
			return "error:probe line absent from child output"
		}
	}
	writeReport(kernelProbeReport{
		Supported:        true,
		WriteInside:      probe("write_inside"),
		WriteSibling:     probe("write_sibling"),
		WriteOperator:    probe("write_operator"),
		ReadCredentials:  probe("read_credentials"),
		ReadSibling:      probe("read_sibling"),
		ReadOwnManifest:  probe("read_own_manifest"),
		ReadWorkSource:   probe("read_work_source"),
		ReadInsidePrefix: probe("read_inside_prefix"),
		Detail:           completion.ForLLM,
	})
}
