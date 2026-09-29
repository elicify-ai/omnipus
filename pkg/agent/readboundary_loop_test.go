// readboundary_loop_test.go: #920 RED pack — tests 13 and 14 of
// docs/internal/specs/read-boundary-consistency-spec.md, at the LOOP level.
//
// MIN-004 (ADR-092 correction note) requires the D8 behaviour to be proved
// through the real agent loop's own pin path — pkg/agent/
// loop_run_turn_tools.go attaches tools.WithAutoApproved(execCtx,
// tools.AutoPinForVerdict(...)) to every Auto-run call on an Ask policy —
// never through a hand-built pin. These tests therefore drive real turns
// (NewAgentLoop via mustNewAgentLoop, a scripted provider, the agent's REAL
// read_file / list_directory / grep instances registered by the production
// wiring) and observe only what a user could: the approval cards (one
// RequestApproval call on the recording approver = one card), the tool
// results the model received, and the tool.auto_approved audit rows.
//
// Oracles: DS-2 (every row), decisions D2, D3, D8, FR-017..FR-019. Never the
// current code's output.

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// rbReadTools are the three reading tools every DS-2 row applies to.
var rbReadTools = []string{"read_file", "list_directory", "grep"}

type rbLocation string

const (
	rbInside rbLocation = "inside"
	rbOutEXT rbLocation = "ext"
	rbSecret rbLocation = "secret"
)

// rbLoopEnv is one real turn's world: $OMNIPUS_HOME (with master.key),
// mia's work dir (notes/a.md) and <EXT> (ext/notes.txt).
type rbLoopEnv struct {
	home, work, ext string
}

// rbArgs returns the tool arguments for a location and the text only a
// SUCCESSFUL call would put in the tool result.
func (e rbLoopEnv) rbArgs(tool string, loc rbLocation) (map[string]any, string) {
	switch loc {
	case rbInside:
		switch tool {
		case "read_file":
			return map[string]any{"path": "notes/a.md"}, "needle-inside-ws"
		case "list_directory":
			return map[string]any{"path": "notes"}, "a.md"
		default:
			return map[string]any{"pattern": "needle", "path": "notes"}, "notes/a.md:1:"
		}
	case rbOutEXT:
		switch tool {
		case "read_file":
			return map[string]any{"path": filepath.Join(e.ext, "ext", "notes.txt")}, "needle outside"
		case "list_directory":
			return map[string]any{"path": filepath.Join(e.ext, "ext")}, "notes.txt"
		default:
			return map[string]any{"pattern": "needle", "path": e.ext},
				filepath.ToSlash(filepath.Join(e.ext, "ext", "notes.txt")) + ":1:"
		}
	default:
		key := filepath.Join(e.home, "master.key")
		if tool == "grep" {
			return map[string]any{"pattern": "needle", "path": key}, "MASTERKEY-needle"
		}
		return map[string]any{"path": key}, "MASTERKEY-needle"
	}
}

func rbCall(t *testing.T, id, tool string, args map[string]any) providers.ToolCall {
	t.Helper()
	raw, err := json.Marshal(args)
	require.NoError(t, err)
	return autoToolCall(id, tool, string(raw))
}

// rbLoopTurn is one prepared turn.
type rbLoopTurn struct {
	env       rbLoopEnv
	al        *AgentLoop
	provider  *testutil.ScenarioProvider
	approver  *autoRecordingApprover
	readAudit func() []map[string]any
}

// newRBLoopTurn builds the world, then the loop, with the three reading
// tools (plus extraAsk) set to policy on the GLOBAL ceiling — the shipped
// configuration surface (TestAutoApprove_Reachability_ProductionWiring's
// precedent) — and Auto-approve as asked. script receives the env so tool
// arguments can name real paths.
func newRBLoopTurn(
	t *testing.T,
	policy string,
	autoApprove bool,
	mutate func(*config.Config),
	extraAsk []string,
	script func(env rbLoopEnv) *testutil.ScenarioProvider,
) *rbLoopTurn {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	ext := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(ext, "ext"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ext, "ext", "notes.txt"), []byte("needle outside\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(home, "master.key"), []byte("MASTERKEY-needle\n"), 0o600))
	extReal, err := filepath.EvalSymlinks(ext)
	require.NoError(t, err)
	homeReal, err := filepath.EvalSymlinks(home)
	require.NoError(t, err)

	cfg, _ := baseLoopDenialTestConfig(t)
	cfg.Sandbox.AutoApprove = autoApprove
	cfg.Sandbox.ToolPolicies = map[string]string{}
	for _, name := range rbReadTools {
		cfg.Sandbox.ToolPolicies[name] = policy
	}
	for _, name := range extraAsk {
		cfg.Sandbox.ToolPolicies[name] = "ask"
	}
	if mutate != nil {
		mutate(cfg)
	}

	env := rbLoopEnv{home: homeReal, ext: extReal}
	provider := script(env)
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(func() { al.Close() })

	// mia's turn work dir: the harness workspace mustNewAgentLoop made her a
	// CoreTeam member of. DS-2 row 1 (allow/off/inside) is the control that
	// proves this is really where her relative paths land.
	work, err := workspace.EnsureWorkDir(home, testHarnessWorkspaceMembershipID)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(work, "notes"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "notes", "a.md"), []byte("needle-inside-ws\n"), 0o600))
	env.work = work

	inst, ok := al.GetRegistry().GetAgent("mia")
	require.True(t, ok, "agent mia must be registered")
	for _, name := range rbReadTools {
		_, registered := inst.Tools.Get(name)
		require.True(t, registered, "the production wiring must register %s for mia", name)
		require.Equal(t, policy, al.ResolveApprovalToolPolicy("mia", name),
			"precondition: %s must resolve to %q from the configured ceiling", name, policy)
	}

	approver := &autoRecordingApprover{approve: true}
	al.SetToolApprover(approver)
	return &rbLoopTurn{env: env, al: al, provider: provider, approver: approver, readAudit: swapAuditLogger(t, al)}
}

func (lt *rbLoopTurn) run(t *testing.T, key string) {
	t.Helper()
	_, err := lt.al.ProcessDirect(context.Background(), "read, list and search", key)
	require.NoError(t, err)
}

// rbThreeCalls scripts one model response calling read_file, list_directory
// and grep on loc (ids "<prefix>-<tool>").
func rbThreeCalls(t *testing.T, env rbLoopEnv, loc rbLocation, prefix string) []providers.ToolCall {
	calls := make([]providers.ToolCall, 0, 3)
	for _, tool := range rbReadTools {
		args, _ := env.rbArgs(tool, loc)
		calls = append(calls, rbCall(t, prefix+"-"+tool, tool, args))
	}
	return calls
}

type rbOutcome int

const (
	rbRunsNoCard rbOutcome = iota
	rbRunsNoCardAutoRow
	rbCardThenRuns
	rbCardThenRefused
	rbRefusedNoCard // secret set: refused, no card (DS-2 is silent on a tool.auto_approved row)
	rbDeniedNoCard  // Deny policy: refused, no card, no tool.auto_approved row (S-3.8)
)

// rbAssertOutcome checks one tool's outcome in one turn.
func rbAssertOutcome(t *testing.T, lt *rbLoopTurn, rows []map[string]any, tool string, loc rbLocation, id string, want rbOutcome, row string) {
	t.Helper()
	_, success := lt.env.rbArgs(tool, loc)
	cards := lt.approver.countFor(tool)
	result := toolResultText(t, lt.provider, id)
	autoRows := len(auditRowsFor(rows, audit.EventToolAutoApproved, tool))
	switch want {
	case rbRunsNoCard, rbRunsNoCardAutoRow:
		if cards != 0 {
			t.Errorf("%s %s: %d approval card(s), want none (runs, no card)", row, tool, cards)
		}
		if !strings.Contains(result, success) {
			t.Errorf("%s %s: must run and return %q; the model got:\n%s", row, tool, success, result)
		}
		if want == rbRunsNoCardAutoRow && autoRows != 1 {
			t.Errorf("%s %s: want exactly one %s row, got %d", row, tool, audit.EventToolAutoApproved, autoRows)
		}
	case rbCardThenRuns:
		if cards != 1 {
			t.Errorf("%s %s: %d approval card(s), want exactly 1", row, tool, cards)
		}
		if !strings.Contains(result, success) {
			t.Errorf("%s %s: after the approval the call must run and return %q; got:\n%s", row, tool, success, result)
		}
		if autoRows != 0 {
			t.Errorf("%s %s: a prompted call must write no %s row, got %d", row, tool, audit.EventToolAutoApproved, autoRows)
		}
	case rbCardThenRefused:
		if cards != 1 {
			t.Errorf("%s %s: %d approval card(s), want exactly 1 (card first, US-3 AS-2)", row, tool, cards)
		}
		if strings.Contains(result, success) || strings.Contains(result, "MASTERKEY-needle") {
			t.Errorf("%s %s: once approved the secret must still be refused; got:\n%s", row, tool, result)
		}
	case rbRefusedNoCard, rbDeniedNoCard:
		if cards != 0 {
			t.Errorf("%s %s: %d approval card(s), want none (refused, no card)", row, tool, cards)
		}
		if strings.Contains(result, success) || strings.Contains(result, "MASTERKEY-needle") {
			t.Errorf("%s %s: must be refused, but the model got the content:\n%s", row, tool, result)
		}
		if want == rbDeniedNoCard && autoRows != 0 {
			t.Errorf("%s %s: a denied call must write no %s row, got %d (S-3.8)", row, tool, audit.EventToolAutoApproved, autoRows)
		}
	}
}

// TestReadBoundary_AutoPolicyMatrix is test 14: DS-2 rows 1-12 and 14-21
// (the full 18-cell policy x Auto x location grid plus the two ask/on/<EXT>
// modifiers), every row for all three reading tools, each row one real
// turn; plus R2/R3 for the write and send tools. Row 13 (image) is in test
// 13; R1 and R4/R5 are pin-level (readboundary_autopin_test.go in
// pkg/tools).
//
// Traces: S-3.1, S-3.6, S-3.7, S-3.8; FR-017, FR-019.
func TestReadBoundary_AutoPolicyMatrix(t *testing.T) {
	rows := []struct {
		row    string
		policy string
		auto   bool
		loc    rbLocation
		want   rbOutcome
	}{
		{"DS-2 row 1", "allow", false, rbInside, rbRunsNoCard},
		{"DS-2 row 2", "allow", false, rbOutEXT, rbRunsNoCard},
		{"DS-2 row 3", "allow", true, rbOutEXT, rbRunsNoCard},
		{"DS-2 row 4", "ask", false, rbInside, rbCardThenRuns},
		{"DS-2 row 5", "ask", false, rbOutEXT, rbCardThenRuns},
		{"DS-2 row 6", "ask", true, rbInside, rbRunsNoCardAutoRow},
		{"DS-2 row 7", "ask", true, rbOutEXT, rbRunsNoCardAutoRow},
		{"DS-2 row 8", "ask", true, rbSecret, rbRefusedNoCard},
		{"DS-2 row 9", "deny", false, rbInside, rbDeniedNoCard},
		{"DS-2 row 10", "deny", true, rbOutEXT, rbDeniedNoCard},
		{"DS-2 row 14", "allow", true, rbInside, rbRunsNoCard},
		{"DS-2 row 15", "allow", false, rbSecret, rbRefusedNoCard},
		{"DS-2 row 16", "allow", true, rbSecret, rbRefusedNoCard},
		{"DS-2 row 17", "ask", false, rbSecret, rbCardThenRefused},
		{"DS-2 row 18", "deny", false, rbOutEXT, rbDeniedNoCard},
		{"DS-2 row 19 (S-3.8)", "deny", true, rbInside, rbDeniedNoCard},
		{"DS-2 row 20", "deny", false, rbSecret, rbDeniedNoCard},
		{"DS-2 row 21", "deny", true, rbSecret, rbDeniedNoCard},
	}
	for _, r := range rows {
		t.Run(r.row, func(t *testing.T) {
			lt := newRBLoopTurn(t, r.policy, r.auto, nil, nil, func(env rbLoopEnv) *testutil.ScenarioProvider {
				return testutil.NewScenario().WithToolCalls(rbThreeCalls(t, env, r.loc, "m")).WithText("done")
			})
			lt.run(t, "rb-matrix")
			audited := lt.readAudit()
			for _, tool := range rbReadTools {
				rbAssertOutcome(t, lt, audited, tool, r.loc, "m-"+tool, r.want, r.row)
			}
		})
	}

	t.Run("DS-2 row 11 agent marked Never auto-approve (S-3.7)", func(t *testing.T) {
		lt := newRBLoopTurn(t, "ask", true, func(c *config.Config) { c.Agents.List[0].AutoApproveDisabled = true }, nil,
			func(env rbLoopEnv) *testutil.ScenarioProvider {
				return testutil.NewScenario().WithToolCalls(rbThreeCalls(t, env, rbOutEXT, "n")).WithText("done")
			})
		lt.run(t, "rb-never-auto")
		audited := lt.readAudit()
		for _, tool := range rbReadTools {
			rbAssertOutcome(t, lt, audited, tool, rbOutEXT, "n-"+tool, rbCardThenRuns, "DS-2 row 11")
		}
	})

	t.Run("DS-2 row 12 chat Auto-approve toggled off (S-3.7)", func(t *testing.T) {
		// The per-chat toggle is set off at the first card of the turn (an
		// ask-list stub, delete_task) — ADR-092 rule C: the chat's choice
		// applies from the next tool-call dispatch — so the three reads that
		// follow run in a chat whose toggle is off while global Auto is on.
		lt := newRBLoopTurn(t, "ask", true, nil, []string{"delete_task"}, func(env rbLoopEnv) *testutil.ScenarioProvider {
			return testutil.NewScenario().
				WithToolCalls([]providers.ToolCall{autoToolCall("c-delete", "delete_task", `{}`)}).
				WithToolCalls(rbThreeCalls(t, env, rbOutEXT, "c")).
				WithText("done")
		})
		inst, ok := lt.al.GetRegistry().GetAgent("mia")
		require.True(t, ok)
		inst.Tools.Unregister("delete_task")
		inst.Tools.Register(&autoStubTool{name: "delete_task"})
		lt.approver.onRequest = func(req PolicyApprovalReq) {
			if req.ToolName == "delete_task" {
				lt.al.SessionModes().Set(req.SessionID, false)
			}
		}
		lt.run(t, "rb-chat-off")
		audited := lt.readAudit()
		require.Equal(t, 1, lt.approver.countFor("delete_task"), "precondition: the ask-list stub prompts once")
		for _, tool := range rbReadTools {
			rbAssertOutcome(t, lt, audited, tool, rbOutEXT, "c-"+tool, rbCardThenRuns, "DS-2 row 12")
		}
	})

	t.Run("DS-2 R2 write and send tools keep asking outside the workspace (S-3.6)", func(t *testing.T) {
		writers := []string{"write_file", "edit_file", "append_file", "send_file"}
		lt := newRBLoopTurn(t, "allow", true, nil, writers, func(env rbLoopEnv) *testutil.ScenarioProvider {
			target := filepath.Join(env.ext, "ext", "notes.txt")
			return testutil.NewScenario().WithToolCalls([]providers.ToolCall{
				rbCall(t, "w-write_file", "write_file", map[string]any{"path": filepath.Join(env.ext, "new.txt"), "content": "x"}),
				rbCall(t, "w-edit_file", "edit_file", map[string]any{"path": target, "old_text": "needle", "new_text": "x"}),
				rbCall(t, "w-append_file", "append_file", map[string]any{"path": target, "content": "x"}),
				rbCall(t, "w-send_file", "send_file", map[string]any{"path": target}),
			}).WithText("done")
		})
		lt.run(t, "rb-writers")
		for _, tool := range writers {
			if got := lt.approver.countFor(tool); got != 1 {
				t.Errorf("DS-2 R2: %s outside the workspace under Ask + Auto on must show exactly 1 approval card, got %d", tool, got)
			}
		}
	})

	t.Run("DS-2 R3 send_file inside the workspace runs without a card (control)", func(t *testing.T) {
		lt := newRBLoopTurn(t, "allow", true, nil, []string{"send_file"}, func(env rbLoopEnv) *testutil.ScenarioProvider {
			return testutil.NewScenario().WithToolCalls([]providers.ToolCall{
				rbCall(t, "s-send_file", "send_file", map[string]any{"path": "notes/a.md"}),
			}).WithText("done")
		})
		lt.run(t, "rb-send-inside")
		if got := lt.approver.countFor("send_file"); got != 0 {
			t.Errorf("DS-2 R3: send_file inside the workspace under Ask + Auto on must run without a card, got %d card(s)", got)
		}
	})
}

// TestReadBoundary_AutoAskRunsThroughLoop is test 13 (MIN-004): with the
// three reading tools on Ask and Auto-approve on, the REAL loop pins each
// call and dispatches the REAL tool. Inside and outside the workspace all
// six calls return content with no card and one tool.auto_approved row of
// class "runs" each (S-3.2, US-3 Independent Test); a PNG outside the
// workspace is inspected (S-3.3 / DS-2 row 13); master.key is refused.
// Without the D8 fix, the class move alone would make every one of these
// fail on the empty pin's re-check — exactly what this test would show.
//
// DS-2 R1 (write_file swap) cannot be placed deterministically between the
// loop's decision and its dispatch without a loop seam; it is driven through
// the same pin API in pkg/tools TestAutoPin_WritePinStillCatchesSwap.
//
// Traces: S-3.2, S-3.3; FR-017, FR-018.
func TestReadBoundary_AutoAskRunsThroughLoop(t *testing.T) {
	var pic string
	lt := newRBLoopTurn(t, "ask", true, nil, nil, func(env rbLoopEnv) *testutil.ScenarioProvider {
		pic = filepath.Join(env.ext, "pic.png")
		rbWritePNG(t, pic)
		calls := append(rbThreeCalls(t, env, rbInside, "in"), rbThreeCalls(t, env, rbOutEXT, "out")...)
		calls = append(calls,
			rbCall(t, "img-read_file", "read_file", map[string]any{"path": pic}),
			rbCall(t, "key-read_file", "read_file", map[string]any{"path": filepath.Join(env.home, "master.key")}),
		)
		return testutil.NewScenario().WithToolCalls(calls).WithText("done")
	})
	lt.run(t, "rb-auto-ask")
	rows := lt.readAudit()

	for _, loc := range []rbLocation{rbInside, rbOutEXT} {
		prefix := map[rbLocation]string{rbInside: "in", rbOutEXT: "out"}[loc]
		for _, tool := range rbReadTools {
			_, success := lt.env.rbArgs(tool, loc)
			result := toolResultText(t, lt.provider, prefix+"-"+tool)
			if !strings.Contains(result, success) {
				t.Errorf("S-3.2: %s %s under Ask + Auto on must return %q — not a moved/re-check refusal; the model got:\n%s",
					tool, loc, success, result)
			}
		}
	}
	for _, tool := range rbReadTools {
		if got := lt.approver.countFor(tool); got != 0 {
			t.Errorf("US-3 AS-1: %s showed %d approval card(s), want none", tool, got)
		}
	}
	for _, tool := range []string{"list_directory", "grep"} {
		got := auditRowsFor(rows, audit.EventToolAutoApproved, tool)
		if len(got) != 2 {
			t.Errorf("S-3.2: want 2 tool.auto_approved rows for %s (inside + outside), got %d", tool, len(got))
			continue
		}
		for _, r := range got {
			if details, _ := r["details"].(map[string]any); details["class"] != "runs" {
				t.Errorf("S-3.2: %s tool.auto_approved class = %v, want \"runs\" (D3)", tool, details["class"])
			}
		}
	}
	// read_file: inside, outside and the image each ran under Auto, so at
	// least those three rows exist (DS-2 is silent on whether the refused
	// master.key call writes one).
	readRows := auditRowsFor(rows, audit.EventToolAutoApproved, "read_file")
	if len(readRows) < 3 {
		t.Errorf("S-3.2: want at least 3 tool.auto_approved rows for read_file (inside, outside, image), got %d", len(readRows))
	}
	for _, r := range readRows {
		if details, _ := r["details"].(map[string]any); details["class"] != "runs" {
			t.Errorf("S-3.2: read_file tool.auto_approved class = %v, want \"runs\" (D3)", details["class"])
		}
	}

	img := toolResultText(t, lt.provider, "img-read_file")
	if !strings.Contains(img, "[image: ") || strings.Contains(img, "permission_denied") {
		t.Errorf("S-3.3: the PNG outside the workspace must be inspected under Ask + Auto on; the model got:\n%s", img)
	}
	key := toolResultText(t, lt.provider, "key-read_file")
	if strings.Contains(key, "MASTERKEY-needle") {
		t.Errorf("DS-2 row 8: master.key must be refused under Ask + Auto on; the model got its content")
	}
}

// rbWritePNG writes a 1x1 PNG (S-3.3's image).
func rbWritePNG(t *testing.T, p string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	require.NoError(t, os.WriteFile(p, buf.Bytes(), 0o600))
}
