//go:build goolm && stdjson

package gateway

// Order 51 / CHECK F18. C-24, S-112/S-114: real model -> Skill ->
// approver -> pending card -> HTTP -> durable trust -> allowance -> result.
// No test inserts the approval, supplies a verdict or returns stored effects.
// Missing native effect-boundary inputs fail BLOCKED; they are not a second
// approval system. Durable trust is read by a fresh native inventory process,
// not by guessing the protected file's container from public record schemas.
// GREEN and implementation mutation/proof-of-failability defer to CHECK.
import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const notOfferedText = "This choice is not available for this request."

// Input-only hooks at the three absent native C-24 effect boundaries. They
// may pause or fail an operation; they NEVER provide a card/record/decision.
// A binding must call the original native operation after each successful
// callback. Failure injection is scoped to this loop and restored before retry.
type nativeSkillEffectInputs struct {
	beforeTrustWrite      func() error
	beforeAllowanceRecord func() error
	beforeWaitingRelease  func()
}

// Flatten input callbacks so this can be assigned a direct production method
// expression, not a test closure that fabricates the sequence of effects.
var bindNativeSkillEffectInputs func(*agent.AgentLoop, func() error, func() error, func()) func()

func nativeSkillInputs(t *testing.T, h *nativeSkillHarness, in nativeSkillEffectInputs) func() {
	t.Helper()
	nativebinding.Require(t, "pkg/agent", bindNativeSkillEffectInputs,
		"F18/S-114/C-24 Order of effects: actual trust-write, allowance-record and waiting-call release inputs, not effects supplied by tests")
	restore := bindNativeSkillEffectInputs(h.api.agentLoop, in.beforeTrustWrite, in.beforeAllowanceRecord, in.beforeWaitingRelease)
	require.NotNil(t, restore)
	var once sync.Once
	reset := func() { once.Do(restore) }
	t.Cleanup(reset)
	return reset
}

func TestSkillApprovalActions_Handler(t *testing.T) {
	if slices.Contains(os.Args, "--f18-native-trust-reader") {
		nativeTrustReadChild(t)
		return
	}
	t.Run("instrument actual permitted Skill load reaches the captured model request", func(t *testing.T) {
		h := newNativeSkillHarness(t, "allow", "allow")
		run := h.begin(t)
		h.finish(t, run, true)
		assert.Empty(t, h.reg.pendingApprovals())
	})
	t.Run("instrument ordinary Ask raises a native card in the same rooted chat", func(t *testing.T) {
		h := newNativeSkillHarness(t, "allow", "allow")
		inst, ok := h.api.agentLoop.GetRegistry().GetAgent("test-agent")
		require.True(t, ok)
		tool, ok := inst.Tools.Get("delete_task")
		require.True(t, ok, "instrument uses the actual builtin")
		ceiling := make(map[string]config.ToolPolicy)
		for name, value := range h.api.agentLoop.GetConfig().Sandbox.ToolPolicies {
			ceiling[name] = config.ToolPolicy(value)
		}
		ceiling["delete_task"] = "ask"
		inst.StoreToolPolicy(&tools.ToolPolicyCfg{GlobalPolicies: ceiling})
		require.Equal(t, "ask", tools.EffectiveToolPolicy(inst.LoadToolPolicy(), tool.Scope(), inst.AgentType, "delete_task"))
		h.script.tool, h.script.args = "delete_task", map[string]any{"task_id": "not-deleted"}
		run := h.begin(t)
		entry := h.pending(t, run)
		assert.Equal(t, "test-agent", entry.AgentID)
		assert.Equal(t, "delete_task", entry.ToolName)
		assert.Equal(t, map[string]any{"task_id": "not-deleted"}, entry.Args)
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"deny"}`)
		require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
		h.finish(t, run, false)
	})
	t.Run("instrument fresh native inventory sees exact installed key with no approval", func(t *testing.T) {
		// Empty is accepted by the legacy decoder and means Ask (C-3).
		// This is a reader control only; the next case retains literal ask.
		h := newNativeSkillHarness(t, "", "ask")
		assertFreshSkillTrust(t, h.api.homePath, "notes-helper", false)
	})
	t.Run("C-24 actual trust route persists a revision visible to a fresh native reader", func(t *testing.T) {
		h := newNativeSkillHarness(t, "ask", "ask")
		assertFreshSkillTrust(t, h.api.homePath, "notes-helper", false)
		approveSkillViaPersonRoute(t, h, "notes-helper")
		assertFreshSkillTrust(t, h.api.homePath, "notes-helper", true)
	})
	t.Run("C-24 actual combined card answer releases real instructions and persists trust", func(t *testing.T) {
		h := newNativeSkillHarness(t, "ask", "ask")
		run := h.begin(t)
		entry := h.pending(t, run)
		h.combined(t, entry)
		configBefore := readNativeConfig(t, h.api.homePath)
		w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"3h"}`)
		response := assertNativeSkillAnswer(t, w, entry, true)
		h.finish(t, run, true)
		assertFreshSkillTrust(t, h.api.homePath, "notes-helper", true)
		assert.Equal(t, configBefore, readNativeConfig(t, h.api.homePath), "a card writes no stored permission")
		h.unattended(t, "test-agent", true)
		h.unattended(t, "peer-agent", false)
		assertNativeSkillAudit(t, h, entry, "3h", response.ExpiresAt)
	})
	t.Run("C-24 durable trust finishes before allowance and allowance before waiting execution", testNativeSkillEffectOrder)
	t.Run("S-114 trust write failure preserves all prior records and the same card accepts retry", testNativeSkillTrustFailure)
	t.Run("S-114 allowance failure really stores trust and releases only this call", testNativeSkillAllowanceFailure)
	t.Run("S-112 changed revision refuses waiting call without changing prior trust then replay is 404", testNativeSkillStale)
	t.Run("C-24 unoffered action leaves the real pending card and prior records unchanged", testNativeSkillUnoffered)
	for _, policy := range []string{"ask", "allow"} {
		for _, answer := range []string{"approve", "deny", "timeout"} {
			t.Run("S-118 real install one card policy="+policy+" answer="+answer, func(t *testing.T) { testNativeSkillInstallCard(t, policy, answer) })
		}
	}
}

func assertNativeSkillAnswer(t *testing.T, w *httptest.ResponseRecorder, entry *approvalEntry, timed bool) gen.ToolApprovalResponse {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, "C-24 actual HTTP answer: %s", w.Body.String())
	var response gen.ToolApprovalResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, entry.ApprovalID, response.ApprovalId)
	assert.Equal(t, gen.ToolApprovalResponseActionApproveVersionAllowFor, response.Action)
	require.Equal(t, &gen.ToolApprovalApplied{CallReleased: true, TrustApproved: true, TimedAllowance: timed}, response.Applied)
	if timed {
		require.NotNil(t, response.ExpiresAt)
		require.Equal(t, boolPtr(true), response.GrantRecorded)
	} else {
		assert.Nil(t, response.ExpiresAt)
		assert.Equal(t, boolPtr(false), response.GrantRecorded)
	}
	return response
}

// Pause inputs never return synthetic effect results. Main-goroutine
// observations at every phase make an early later effect independently fatal.
func testNativeSkillEffectOrder(t *testing.T) {
	h := newNativeSkillHarness(t, "ask", "ask")
	clock := controlledTimedCardClock(t, h.timedCardHarness)
	end := clock.wallNow().Add(3 * time.Hour)
	// Record the controlled input even when an absent native observer blocks
	// the effect phases; this is not a claim that an allowance was recorded.
	t.Logf("C-24 controlled-clock input: 3h allowance must expire at %s", end.Format(time.RFC3339Nano))
	type phase struct {
		entered, release chan struct{}
		once             sync.Once
	}
	newPhase := func() *phase { return &phase{entered: make(chan struct{}), release: make(chan struct{})} }
	trust, allowance, release := newPhase(), newPhase(), newPhase()
	unblock := func(p *phase) { p.once.Do(func() { close(p.release) }) }
	wait := func(p *phase) func() error { return func() error { close(p.entered); <-p.release; return nil } }
	nativeSkillInputs(t, h, nativeSkillEffectInputs{beforeTrustWrite: wait(trust), beforeAllowanceRecord: wait(allowance), beforeWaitingRelease: func() { close(release.entered); <-release.release }})
	t.Cleanup(func() { unblock(trust); unblock(allowance); unblock(release) })
	run := h.begin(t)
	entry := h.pending(t, run)
	h.combined(t, entry)
	answered := make(chan *httptest.ResponseRecorder, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		answered <- postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"3h"}`)
	}()
	t.Cleanup(func() {
		unblock(trust)
		unblock(allowance)
		unblock(release)
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("native answer did not join")
		}
	})
	awaitPhase := func(p *phase) {
		t.Helper()
		select {
		case <-p.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("native effect did not reach its boundary")
		}
	}
	assertHeld := func() {
		t.Helper()
		assert.Len(t, h.script.captured(run.prompt), 1, "waiting tool instructions have not reached a second real model request")
	}
	awaitPhase(trust)
	assert.NoFileExists(t, trustApprovalsPath(h.api))
	assertHeld()
	assertNativeSkillAllowance(t, h, false)
	unblock(trust)
	awaitPhase(allowance)
	raw, err := os.ReadFile(trustApprovalsPath(h.api))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))
	require.NotEqual(t, "{}", strings.TrimSpace(string(raw)), "durable file is not an empty success placeholder")
	assertFreshSkillTrust(t, h.api.homePath, "notes-helper", true)
	assertNativeSkillAllowance(t, h, false)
	assertHeld()
	unblock(allowance)
	awaitPhase(release)
	assertNativeSkillAllowance(t, h, true)
	assertHeld()
	unblock(release)
	var answer *httptest.ResponseRecorder
	select {
	case answer = <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("native answer did not finish after the real effect boundaries were released")
	}
	response := assertNativeSkillAnswer(t, answer, entry, true)
	assert.Equal(t, end, response.ExpiresAt.UTC())
	h.finish(t, run, true)
	assertNativeSkillAudit(t, h, entry, "3h", &end)
}

func testNativeSkillTrustFailure(t *testing.T) {
	h := newNativeSkillHarness(t, "ask", "ask")
	prior := seedNativePriorTrust(t, h)
	reset := nativeSkillInputs(t, h, nativeSkillEffectInputs{beforeTrustWrite: func() error { return errors.New("F18 trust device unavailable") }})
	defer reset()
	run := h.begin(t)
	entry := h.pending(t, run)
	h.combined(t, entry)
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"3h"}`)
	assertNativeSkillRefusal(t, w, http.StatusInternalServerError, "Could not save the approval. Nothing was changed.")
	assert.Equal(t, prior, readNativeTrustBytes(t, h))
	assertNativeSkillAllowance(t, h, false)
	h.reg.mu.Lock()
	state := entry.state
	h.reg.mu.Unlock()
	assert.Equal(t, ApprovalStatePending, state, "same approval is still answerable after trust-write failure")
	require.Same(t, entry, h.reg.get(entry.ApprovalID))
	assert.Len(t, h.script.captured(run.prompt), 1, "failure cannot release waiting instructions")
	reset()
	w = postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"3h"}`)
	assertNativeSkillAnswer(t, w, entry, true)
	h.finish(t, run, true)
	assertFreshSkillTrust(t, h.api.homePath, "notes-helper", true)
	assertFreshSkillTrust(t, h.api.homePath, "unrelated-helper", true)
}
func testNativeSkillAllowanceFailure(t *testing.T) {
	h := newNativeSkillHarness(t, "ask", "ask")
	const reason = "F18 allowance memory unavailable"
	nativeSkillInputs(t, h, nativeSkillEffectInputs{beforeAllowanceRecord: func() error { return errors.New(reason) }})
	run := h.begin(t)
	entry := h.pending(t, run)
	h.combined(t, entry)
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"3h"}`)
	response := assertNativeSkillAnswer(t, w, entry, false)
	require.Equal(t, strPtr("Approved for this request only. The time limit was not saved: "+reason+"."), response.NotAppliedReason)
	h.finish(t, run, true)
	assertFreshSkillTrust(t, h.api.homePath, "notes-helper", true)
	assertNativeSkillAllowance(t, h, false)
	h.unattended(t, "test-agent", false)
}
func testNativeSkillStale(t *testing.T) {
	h := newNativeSkillHarness(t, "ask", "ask")
	prior := seedNativePriorTrust(t, h)
	run := h.begin(t)
	entry := h.pending(t, run)
	h.combined(t, entry)
	require.NoError(t, os.WriteFile(filepath.Join(h.api.homePath, "skills", "notes-helper", "SKILL.md"), []byte(nativeSkillR2), 0o600))
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"3h"}`)
	assertNativeSkillRefusal(t, w, http.StatusConflict, "The skill changed after this request.")
	assert.Equal(t, prior, readNativeTrustBytes(t, h))
	assertNativeSkillAllowance(t, h, false)
	h.finish(t, run, false)
	requests := h.script.captured(run.prompt)
	var results []string
	for _, msg := range requests[1] {
		if msg.Role == "tool" {
			results = append(results, msg.Content)
		}
	}
	require.Len(t, results, 1)
	assert.Contains(t, results[0], "The skill changed after this request.", "same refusal reaches the actual waiting call")
	w = postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"3h"}`)
	assertNativeSkillRefusal(t, w, http.StatusNotFound, existingApprovalNotFoundText(entry.ApprovalID))
	assert.Equal(t, prior, readNativeTrustBytes(t, h))
	assertNativeSkillAllowance(t, h, false)
}

// existingApprovalNotFoundText is the 404 text the approval handler already gives for an unknown,
// answered or expired approval id. Spec C-24 server rules, row "Replay and expiry": "the existing 404
// 'approval … not found'". The existing text quotes the id (pkg/gateway/rest_tool_registry.go::
// HandleToolApprovals, `approval %q not found`, unchanged since the round-4 base 456e35108), so a
// replay's expected text quotes it too; the first RED draft wrote the id without quotes, which was
// not "the existing 404" (fix round 4, part D item 3). Built by concatenation, not by calling any
// production helper, so the oracle stays the spec's wording.
func existingApprovalNotFoundText(approvalID string) string {
	return `approval "` + approvalID + `" not found`
}
func testNativeSkillUnoffered(t *testing.T) {
	h := newNativeSkillHarness(t, "ask", "ask")
	prior := seedNativePriorTrust(t, h)
	run := h.begin(t)
	entry := h.pending(t, run)
	h.combined(t, entry)
	w := postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"allow_for","duration":"3h"}`)
	assertNativeSkillRefusal(t, w, http.StatusConflict, notOfferedText)
	assert.Equal(t, prior, readNativeTrustBytes(t, h))
	assertNativeSkillAllowance(t, h, false)
	require.Same(t, entry, h.reg.get(entry.ApprovalID))
	h.reg.mu.Lock()
	state := entry.state
	h.reg.mu.Unlock()
	assert.Equal(t, ApprovalStatePending, state)
	assert.Len(t, h.script.captured(run.prompt), 1)
	w = postToolApprovalRaw(t, h.api, entry.ApprovalID, `{"action":"approve_version_allow_for","duration":"3h"}`)
	assertNativeSkillAnswer(t, w, entry, true)
	h.finish(t, run, true)
}

// assertNativeSkillAllowance reads the real timed-allowance store the loop owns (the one the card
// handler records into) for the skill-kind allowance of C-26 — Key = (agent id, kind, subject), the
// subject here being the skill key. It never supplies a boolean or a record: the answer under test
// is posted over HTTP and only the store's own readback is observed.
//
// Fix round 4, part D item 1: the first RED draft called t.Fatalf("BLOCKED: …") unconditionally
// because no skill-kind readback existed then, so every test that reached this helper failed here
// whatever the code did. The readback now exists (ApprovalGrantStore.IsSkillAllowed(agentID,
// skillKey)); the assertion is kept as the BLOCKED message stated it: notes-helper is allowed for the
// answering agent exactly when active, and no other agent and no other skill is ever covered by the
// card's answer. An ordinary whole-tool grant (IsAllowed with tool "Skill") is a different, wider
// key and is deliberately not what is read here.
func assertNativeSkillAllowance(t *testing.T, h *nativeSkillHarness, active bool) {
	t.Helper()
	grants := h.api.agentLoop.ApprovalGrants()
	require.NotNil(t, grants, "fixture: real store exists")
	assert.Equal(t, active, grants.IsSkillAllowed("test-agent", "notes-helper"),
		"C-24 effect order / C-26: the skill allowance of notes-helper for the answering agent must be active=%t at this point", active)
	assert.False(t, grants.IsSkillAllowed("peer-agent", "notes-helper"),
		"C-26: the allowance belongs to the answering agent; another agent holding the same skill is never covered")
	assert.False(t, grants.IsSkillAllowed("test-agent", "unrelated-helper"),
		"C-26: the subject is the one skill the card named; another skill is never covered")
}
func assertNativeSkillRefusal(t *testing.T, w *httptest.ResponseRecorder, status int, text string) {
	t.Helper()
	assert.Equal(t, status, w.Code, "%s", w.Body.String())
	var refusal gen.ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &refusal))
	assert.Equal(t, text, refusal.Error)
}
func readNativeConfig(t *testing.T, home string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	return raw
}
func readNativeTrustBytes(t *testing.T, h *nativeSkillHarness) []byte {
	t.Helper()
	raw, err := os.ReadFile(trustApprovalsPath(h.api))
	require.NoError(t, err)
	return raw
}

func approveSkillViaPersonRoute(t *testing.T, h *nativeSkillHarness, key string) gen.SkillTrustApproval {
	t.Helper()
	revision, err := skills.NewSkillWriter(filepath.Join(h.api.homePath, "skills")).SkillRevision(key)
	require.NoError(t, err)
	raw, err := json.Marshal(gen.SkillTrustApprovalRequest{Revision: revision})
	require.NoError(t, err)
	r := withAdminRole(httptest.NewRequest(http.MethodPost, "/api/v1/skills/"+key+"/trust-approval", strings.NewReader(string(raw))))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.api.HandleSkills(w, r)
	require.Equal(t, http.StatusOK, w.Code, "native C-24 trust route, not fabricated prior records: %s", w.Body.String())
	var got gen.SkillTrustApproval
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, key, got.Key)
	assert.Equal(t, revision, got.Revision)
	assert.Equal(t, "admin", got.ApprovedBy)
	assert.False(t, got.ApprovedAt.IsZero())
	return got
}
func seedNativePriorTrust(t *testing.T, h *nativeSkillHarness) []byte {
	t.Helper()
	path := filepath.Join(h.api.homePath, "skills", "notes-helper", "SKILL.md")
	require.NoError(t, os.WriteFile(path, []byte(strings.ReplaceAll(nativeSkillR1, "F18-NOTES-R1-INSTRUCTIONS-9Z", "F18-PRIOR-REVISION-3P")), 0o600))
	approveSkillViaPersonRoute(t, h, "notes-helper")
	other := filepath.Join(h.api.homePath, "skills", "unrelated-helper")
	require.NoError(t, os.MkdirAll(other, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(other, "SKILL.md"), []byte("---\nname: unrelated-helper\ndescription: Prior unrelated consent.\n---\n\nUnrelated instructions.\n"), 0o600))
	approveSkillViaPersonRoute(t, h, "unrelated-helper")
	require.NoError(t, os.WriteFile(path, []byte(nativeSkillR1), 0o600))
	assertFreshSkillTrust(t, h.api.homePath, "notes-helper", false)
	assertFreshSkillTrust(t, h.api.homePath, "unrelated-helper", true)
	return readNativeTrustBytes(t, h)
}
func assertNativeSkillAudit(t *testing.T, h *nativeSkillHarness, entry *approvalEntry, duration string, end *time.Time) {
	t.Helper()
	require.NotNil(t, end)
	revision, err := skills.NewSkillWriter(filepath.Join(h.api.homePath, "skills")).SkillRevision("notes-helper")
	require.NoError(t, err)
	trust, allowance := 0, 0
	for _, row := range readAllAuditEntries(t, filepath.Join(h.api.homePath, "system")) {
		fields, _ := row["fields"].(map[string]any)
		if row["event"] == "skill.trust_approved" && fields["key"] == "notes-helper" {
			trust++
			assert.Equal(t, revision, fields["revision"])
			assert.Equal(t, "admin", fields["actor"])
		}
		if row["event"] == audit.EventToolPolicyAskGranted && fields["approval_id"] == entry.ApprovalID {
			allowance++
			assert.Equal(t, entry.AgentID, fields["agent_id"])
			assert.Contains(t, nativeAuditScalarValues(fields), "notes-helper", "C-24/C-26 require the exact skill subject; no field name is invented for this absent carrier")
			assert.Equal(t, "admin", fields["approver_user_id"])
			assert.Equal(t, "approve_version_allow_for", fields["action"])
			assert.Equal(t, duration, fields["duration"])
			raw, ok := fields["expires_at"].(string)
			require.True(t, ok)
			parsed, err := time.Parse(time.RFC3339Nano, raw)
			require.NoError(t, err)
			assert.Equal(t, end.UTC(), parsed.UTC())
		}
	}
	assert.Equal(t, 1, trust, "exact trust event for current key/revision/person")
	assert.Equal(t, 1, allowance, "exact decision event, not any row with expiry")
}

// Shared HTTP helper signature intentionally preserved for other RED files.
func postToolApprovalRaw(t *testing.T, api *restAPI, approvalID, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+approvalID, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)
	w := httptest.NewRecorder()
	api.HandleToolApprovals(w, r)
	return w
}
func trustApprovalsPath(api *restAPI) string {
	return filepath.Join(api.homePath, "system", "skill-trust-approvals.json")
}
