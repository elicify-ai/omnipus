// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// newTestRestAPIWithAuditLog creates a restAPI where the agent loop has a real
// audit logger wired (Sandbox.AuditLog = true). The workspace is placed at
// tmpDir/workspace so that the agent loop calculates homePath = tmpDir and
// writes audit entries to tmpDir/system/.
func newTestRestAPIWithAuditLog(t *testing.T) (*restAPI, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o700))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspaceDir,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
	}

	minimalCfg := []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"sandbox":{"audit_log":true}}`)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "config.json"), minimalCfg, 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{
		agentLoop:     al,
		allowedOrigin: "http://localhost:3000",
		onboardingMgr: onboarding.NewManager(tmpDir),
		homePath:      tmpDir,
		taskStore:     task.New(filepath.Join(tmpDir, "tasks")),
	}
	return api, tmpDir
}

// adminCtx returns a context with an authenticated user set — used for PUT
// tests. Under the single-user model there is no separate role to inject;
// the handlers under test here are gated purely by RequireNotBypass at
// route-registration time (not exercised by these direct-handler-call
// tests) and read the actor identity from ctxkey.UserContextKey for audit
// attribution.
func adminCtx() context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.UserContextKey{}, &config.UserConfig{Username: "admin"})
	return ctx
}

// TestHandleSandboxAuditLog_PUTPersists verifies that PUT {enabled:true}
// writes sandbox.audit_log=true to config.json on disk.
func TestHandleSandboxAuditLog_PUTPersists(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := strings.NewReader(`{"enabled":true}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	raw, err := os.ReadFile(api.homePath + "/config.json")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))
	sandbox, _ := m["sandbox"].(map[string]any)
	require.NotNil(t, sandbox, "sandbox key must exist in config.json after PUT")
	assert.Equal(t, true, sandbox["audit_log"], "audit_log must be persisted as true")
}

// TestHandleSandboxAuditLog_ResponseShape verifies the response JSON contains
// saved, requires_restart:true, and applied_enabled with correct types.
func TestHandleSandboxAuditLog_ResponseShape(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	body := strings.NewReader(`{"enabled":true}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	saved, hasSaved := resp["saved"]
	assert.True(t, hasSaved, "response must have 'saved' field")
	assert.Equal(t, true, saved, "'saved' must be true")

	rr, hasRR := resp["requires_restart"]
	assert.True(t, hasRR, "response must have 'requires_restart' field")
	assert.Equal(t, true, rr, "'requires_restart' must be true")

	_, hasAE := resp["applied_enabled"]
	assert.True(t, hasAE, "response must have 'applied_enabled' field")
}

// TestHandleSandboxAuditLog_MethodNotAllowed verifies that POST and DELETE
// receive 405 Method Not Allowed.
func TestHandleSandboxAuditLog_MethodNotAllowed(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			r := httptest.NewRequest(method, "/api/v1/security/audit-log", nil)
			w := httptest.NewRecorder()
			api.HandleSandboxAuditLog(w, r)
			assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
		})
	}
}

// TestHandleSandboxAuditLog_InvalidBody verifies that missing the 'enabled'
// field or sending malformed JSON yields 400 Bad Request.
func TestHandleSandboxAuditLog_InvalidBody(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	t.Run("missing enabled field", func(t *testing.T) {
		// Regression: gen.AuditLogToggleRequest.Enabled is bool (not *bool), so an
		// absent "enabled" key would decode as false and silently disable audit
		// logging. The pre-check in HandleSandboxAuditLog now rejects {} with 400
		// before decoding, preventing the silent-disable regression.
		r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(`{}`))
		r = r.WithContext(adminCtx())
		w := httptest.NewRecorder()
		api.HandleSandboxAuditLog(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Contains(t, resp["error"], "enabled")
	})

	t.Run("malformed JSON", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(`{not json}`))
		r = r.WithContext(adminCtx())
		w := httptest.NewRecorder()
		api.HandleSandboxAuditLog(w, r)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// TestHandleSandboxAuditLog_EmitsAuditEntry verifies that when audit logging is
// already enabled (precondition: sandbox.audit_log=true), a PUT {enabled:false}
// emits a security_setting_change JSONL record with the correct resource and
// actor fields.
func TestHandleSandboxAuditLog_EmitsAuditEntry(t *testing.T) {
	api, tmpDir := newTestRestAPIWithAuditLog(t)

	body := strings.NewReader(`{"enabled":false}`)
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", body)
	r = r.WithContext(adminCtx())
	w := httptest.NewRecorder()

	api.HandleSandboxAuditLog(w, r)

	require.Equal(t, http.StatusOK, w.Code)

	systemDir := filepath.Join(tmpDir, "system")
	entries, err := os.ReadDir(systemDir)
	require.NoError(t, err, "system dir must exist after audit emit")
	require.NotEmpty(t, entries, "at least one audit file must exist")

	var found bool
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		f, err := os.Open(filepath.Join(systemDir, entry.Name()))
		require.NoError(t, err)
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var record map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
				continue
			}
			if record["event"] != "security_setting_change" {
				continue
			}
			assert.Equal(t, "sandbox.audit_log", record["resource"], "resource must match")
			assert.Equal(t, "admin", record["actor"], "actor must be the admin username")
			found = true
		}
		require.NoError(t, scanner.Err())
	}

	assert.True(t, found, "security_setting_change record must appear in audit JSONL")
}

// TestAuditLog_RecordRetrievableAlongsideDottedLegacyEvent pins the READ
// surface FR-028 (browser-agent-capability-spec §10 order 23, US-18/AC4)
// actually rests on today, and it pins it as it really behaves — not as the
// spec's prose hopes.
//
// UPDATE — #667 IS FIXED. The analysis below is retained because it is the
// accurate record of what the bug was and how it was diagnosed, but read it
// in the PAST tense: AuditEntry.yaml's event pattern is now `^[a-z_.]+$`, so
// dotted names no longer fail the SPA's edge validation and the Audit Log
// screen no longer blanks. Assertion block (4) at the end of this test was
// flipped accordingly, and pkg/audit/event_name_contract_test.go is the
// standing guard that keeps the contract and the Go event names in sync.
//
// FR-028 says a metadata-only `browser_snapshot` audit event is "readable via
// GET /api/v1/audit-log and $OMNIPUS_HOME/system/audit.jsonl today, and via
// Settings → Security → Audit Log once #667 lands". This test covers the
// first half and DELIBERATELY DOES NOT claim the second.
//
// What issue #667 actually is, verified in this worktree rather than assumed:
//   - contracts/components/schemas/AuditEntry.yaml:17 pins `event` to
//     `^[a-z_]+$`, and the generated client schema carries that verbatim
//     (src/lib/api/generated/schemas.ts::AuditEntry, `z.string().regex(/^[a-z_]+$/)`).
//   - AuditLogResponse wraps it in `z.array(AuditEntry)`, so ONE non-matching
//     `event` fails the WHOLE array, and src/lib/api.ts::fetchAuditLog runs
//     that schema over the response — the viewer blanks entirely.
//   - Dotted event names are emitted from 19 non-test production sites today
//     (`pkg/tools/memory.go`'s `memory.remember`, `pkg/gateway/rest_workspaces.go`'s
//     `workspace.create`, `pkg/gateway/rest.go`'s `agent.delete`, …), PLUS seven
//     dotted named constants in `pkg/audit/audit.go:53-106` — `boot.abort`,
//     `channel.pairing`, `cli.validate`, `executor.smoke_test`,
//     `onboarding.admin_created`, `onboarding.refused`, `skill.call`.
//   - Two of those make the blank effectively certain rather than likely:
//     `onboarding.admin_created` is written once per install at admin
//     registration (`pkg/gateway/rest_onboarding.go:835`), and `skill.call` is
//     written on every `Skill` tool call (`pkg/audit/skill_call.go:155`,
//     ADR-072 D3.1). So on any real install the Audit Log screen is ALREADY
//     blank, for reasons that have nothing to do with this spec — FR-028's
//     "operator-inspectable" mitigation does not reach an operator today.
//
// The failure is therefore CLIENT-side, in zod. The Go handler
// (rest_settings.go::HandleAuditLog) passes each line through as an opaque
// json.RawMessage and validates nothing, so a dotted legacy record neither
// drops nor corrupts a well-named one sitting next to it. That is the
// property this test fixes: `browser_snapshot` stays retrievable from the
// endpoint even in a log that contains the very record that blanks the SPA.
//
// The last two assertions are the honest half. They state, at the contract's
// own regex, that `browser_snapshot` is innocent of #667 (it matches) and
// that the co-resident `channel.pairing` is guilty of it (it does not) — so
// nobody later reads this test's green as evidence that the Audit Log SCREEN
// shows a snapshot. It does not, and will not until #667 lands.
func TestAuditLog_RecordRetrievableAlongsideDottedLegacyEvent(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	systemDir := filepath.Join(api.homePath, "system")
	require.NoError(t, os.MkdirAll(systemDir, 0o700))

	// Line 1: a dotted LEGACY event — the exact constant at
	// pkg/audit/audit.go::EventChannelPairing. This is the #667 trigger.
	// Line 2: the metadata-only browser_snapshot record FR-028 specifies —
	// origin, node count, byte count, values-emitted flag, truncated flag,
	// and deliberately NOT the captured values themselves.
	lines := []string{
		`{"timestamp":"2026-09-02T10:00:00Z","event":"channel.pairing","decision":"allow","details":{"channel":"whatsapp_native"}}`,
		`{"timestamp":"2026-09-02T10:00:01Z","event":"browser_snapshot","decision":"allow","agent_id":"ray",` +
			`"details":{"page_origin":"https://example.com","node_count":412,"output_bytes":18234,` +
			`"value_nodes_emitted":7,"truncated":false}}`,
	}
	require.NoError(t, os.WriteFile(
		filepath.Join(systemDir, "audit.jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"),
		0o600,
	))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-log", nil)
	w := httptest.NewRecorder()
	api.HandleAuditLog(w, r)
	require.Equal(t, http.StatusOK, w.Code, "endpoint must not fail on a dotted legacy event name")

	var resp struct {
		Entries     []map[string]any `json:"entries"`
		ChainStatus string           `json:"chain_status"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Entries, 2, "both records must survive the read")

	byEvent := map[string]map[string]any{}
	for _, e := range resp.Entries {
		name, _ := e["event"].(string)
		byEvent[name] = e
	}

	// (1) The dotted legacy record is returned, not silently dropped. If the
	// handler ever started filtering by name this assertion would red, and
	// the next one would keep passing — which is why both are here.
	require.Contains(t, byEvent, "channel.pairing",
		"the handler must pass a dotted legacy record through untouched, not filter it")

	// (2) The browser_snapshot record is still retrievable ALONGSIDE it.
	snap, ok := byEvent["browser_snapshot"]
	require.True(t, ok, "browser_snapshot must be retrievable from GET /api/v1/audit-log")

	details, ok := snap["details"].(map[string]any)
	require.True(t, ok, "browser_snapshot record must carry a details object")
	assert.Equal(t, "https://example.com", details["page_origin"])
	assert.Equal(t, float64(412), details["node_count"])
	assert.Equal(t, float64(18234), details["output_bytes"])
	assert.Equal(t, false, details["truncated"])
	assert.Equal(t, "ray", snap["agent_id"])

	// (3) WAS the "page values never reach the audit log" assertion. REMOVED,
	// because it could not fail, and a test that cannot fail is worse than no
	// test: it occupies the space where the real one would go.
	//
	// What it did: marshal `snap` — the round-trip of the two-line JSONL
	// literal this test writes twenty lines above — and assert the bytes do
	// not contain "role=", "textbox", "password" or "4111". None of those
	// strings was ever in that literal. Nothing between the literal and the
	// assertion contributes any bytes either: HandleAuditLog reads lines and
	// passes them through as opaque json.RawMessage, so the only production
	// code in the path cannot add or remove a character.
	//
	// So it was a statement about a string constant in its own function, and
	// it would have stayed green with the entire redaction property deleted —
	// SnapshotTool.recordSnapshot is not called here, not imported here, and
	// not reachable from here (it is unexported in pkg/tools/browser).
	//
	// The real property lives with the code that has to hold it, which is the
	// EMITTER: recordSnapshot receives the whole snapshotRender — rendered
	// page text included, values and all — and must copy only counts and an
	// origin into Details. That is a structural omission, the kind a one-line
	// "add the text, it helps debugging" change removes silently. It is
	// asserted against a render genuinely carrying secrets in
	// pkg/tools/browser/snapshot_audit_redaction_test.go.
	//
	// What this file CAN say about it truthfully is the next test's job.

	// (4) #667 IS NOW FIXED, and this block pins the fix rather than the bug.
	// AuditEntry.yaml's event pattern was widened from `^[a-z_]+$` to
	// `^[a-z_.]+$` so the dot-separated names that every newer audit event
	// uses stop failing the SPA's edge validation. Both co-resident names
	// must now satisfy it: the flat `browser_snapshot` AND the dotted
	// `channel.pairing` that used to blank the whole screen.
	//
	// The pattern is READ OUT OF THE CONTRACT FILE, not restated here.
	//
	// It used to be restated — `regexp.MustCompile("^[a-z_.]+$")` written in
	// this function, then matched against the two literals "browser_snapshot"
	// and "channel.pairing", also written in this function. That could not
	// fail for the thing it claimed to check. Narrowing AuditEntry.yaml back
	// to `^[a-z_]+$` — reinstating #667 exactly, blanking the operator's whole
	// Audit Log screen the moment any dotted event lands next to a snapshot —
	// left it green, because nothing in the assertion had read the contract.
	// The comment even said so, and pointed at a sibling test as the real
	// guard, which is a fair description of a test that is not one.
	//
	// pkg/audit/event_name_contract_test.go remains the authoritative sweep:
	// it checks EVERY declared event name against this same pattern. What is
	// asserted here is narrower and local — that the two names co-resident in
	// THIS test's fixture both satisfy the contract as it actually reads
	// today, so a reader can trust the record above is one the SPA will
	// render rather than one that blanks the screen.
	pattern := auditEntryEventPatternFromContract(t)
	for _, name := range []string{"browser_snapshot", "channel.pairing"} {
		assert.Regexp(t, pattern, name,
			"%q does not satisfy AuditEntry.yaml's event pattern %q. AuditLogResponse validates "+
				"z.array(AuditEntry) at the SPA edge, so ONE non-matching event fails the WHOLE "+
				"array and Settings → Security → Audit Log blanks — this is #667, and the fix was "+
				"widening the pattern to admit the dotted names every newer event uses",
			name, pattern)
	}
}

// auditEntryEventPatternFromContract reads the `pattern:` constraint that
// contracts/components/schemas/AuditEntry.yaml puts on `event`.
//
// Reading it rather than restating it is the whole point: the generated Zod
// schema carries this regex verbatim, so the contract file is the only thing
// that decides whether a stored event name survives the SPA's edge
// validation. A copy in a test is one more thing that can drift from it, and
// a test asserting its own copy cannot notice the drift.
//
// Deliberately mirrors pkg/audit/event_name_contract_test.go's reader,
// including its "exactly one quoted pattern" check — the schema declares one
// today, and failing loudly on a second is better than silently picking the
// wrong one.
func auditEntryEventPatternFromContract(t *testing.T) *regexp.Regexp {
	t.Helper()

	schemaPath := filepath.Join("..", "..",
		"contracts", "components", "schemas", "AuditEntry.yaml")
	raw, err := os.ReadFile(schemaPath)
	require.NoError(t, err, "read AuditEntry.yaml (%s)", schemaPath)

	patternLine := regexp.MustCompile(`(?m)^\s*pattern:\s*'([^']*)'\s*$`)
	matches := patternLine.FindAllStringSubmatch(string(raw), -1)
	require.Len(t, matches, 1,
		"expected exactly 1 quoted `pattern:` in AuditEntry.yaml — scope this lookup to the "+
			"`event` property")

	re, err := regexp.Compile(matches[0][1])
	require.NoError(t, err, "AuditEntry.yaml's event pattern %q does not compile as a Go regexp",
		matches[0][1])
	return re
}

// TestAuditLog_ReadSurfaceRedactsNothing states, as a fixed oracle, the one
// thing the READ surface can honestly say about page secrets: it does not
// remove them.
//
// This matters because of what it rules out. A reader who saw the emitter's
// metadata-only design might assume the endpoint sanitises too, and conclude
// that a leak on the write side would be caught on the way out. It would not.
// GET /api/v1/audit-log is a faithful reader — HandleAuditLog copies each
// stored line through as an opaque json.RawMessage — so whatever
// SnapshotTool.recordSnapshot puts in the file is what an audit-log reader
// gets, character for character.
//
// Under ADR-072 that is a real disclosure surface rather than a hygiene
// point: every agent on a workspace drives the OPERATOR'S browser with the
// operator's live logins in it, and browser_snapshot renders field values
// unconditionally by operator ruling (FR-018). So the rendered text routinely
// holds their password, card number or session token. There is exactly ONE
// thing standing between that text and a file retained by design, and it is
// the emitter's structural omission — asserted in
// pkg/tools/browser/snapshot_audit_redaction_test.go, which drives the real
// recordSnapshot with a render that genuinely carries secrets.
//
// The fixture below is therefore a DELIBERATELY BAD record: a browser_snapshot
// entry that leaked the page text, i.e. what the file would contain if that
// one omission were ever undone. Asserting it comes back verbatim is what
// proves nothing downstream compensates. If this test ever goes red because
// the handler started filtering, that is a genuine behaviour change and the
// comment above needs rewriting — which is the point of pinning it.
func TestAuditLog_ReadSurfaceRedactsNothing(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	systemDir := filepath.Join(api.homePath, "system")
	require.NoError(t, os.MkdirAll(systemDir, 0o700))

	// Secret-shaped, so a leak is recognisable in failure output as the thing
	// it would be in a customer's audit file.
	const leakedPassword = "hunter2-Tr0ub4dor&3"
	const leakedPAN = "4111111111111111"

	leaked := map[string]any{
		"timestamp": "2026-09-02T10:00:02Z",
		"event":     "browser_snapshot",
		"decision":  "allow",
		"agent_id":  "ray",
		"details": map[string]any{
			"page_origin": "https://shop.example.com",
			"node_count":  4,
			// The regression this stands in for: the rendered outline, values
			// and all, copied into the record "for debugging".
			"text": `textbox "Password" value="` + leakedPassword + `"` + "\n" +
				`textbox "Card number" value="` + leakedPAN + `"`,
		},
	}
	line, err := json.Marshal(leaked)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(systemDir, "audit.jsonl"), append(line, '\n'), 0o600))

	r := httptest.NewRequest(http.MethodGet, "/api/v1/audit-log", nil)
	w := httptest.NewRecorder()
	api.HandleAuditLog(w, r)
	require.Equal(t, http.StatusOK, w.Code)

	// DECODED, not raw. encoding/json HTML-escapes "&" to & on the way
	// out, so a raw-substring check would have reported the password as
	// "removed" when it was merely escaped — which is the opposite of the
	// truth and would have sent the next reader looking for redaction that
	// does not exist. The question is what a CLIENT receives after parsing,
	// so parse it.
	var resp struct {
		Entries []struct {
			Event   string         `json:"event"`
			Details map[string]any `json:"details"`
		} `json:"entries"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Entries, 1, "the record must be returned at all")
	require.Equal(t, "browser_snapshot", resp.Entries[0].Event)

	got, _ := resp.Entries[0].Details["text"].(string)
	assert.Contains(t, got, leakedPassword,
		"the audit-log endpoint removed a stored password. That is a behaviour change, not a "+
			"pass: this endpoint is a faithful reader, and the ONLY defence against a page value "+
			"reaching the trail is SnapshotTool.recordSnapshot never writing one. If read-side "+
			"redaction has genuinely been added, say so here — and do not let it become the reason "+
			"the emitter's own guarantee stops being tested.")
	assert.Contains(t, got, leakedPAN,
		"same: the read surface neither redacts nor truncates stored details")
}
