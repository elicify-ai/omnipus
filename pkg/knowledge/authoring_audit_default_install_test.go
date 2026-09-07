// Omnipus — ADR-067 FR-090 on a DEFAULT install.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 ./pkg/knowledge/
package knowledge

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The default install is the one nobody tests, and it is the one everybody has
// ---------------------------------------------------------------------------
//
// `sandbox.audit_log` is FALSE on a fresh gateway: pkg/config/sandbox.go
// declares `AuditLog bool json:"audit_log,omitempty"` and nothing in
// pkg/config/defaults.go seeds it true. So pkg/agent's al.auditLogger is nil,
// AgentLoop.wireMemoryAuditLoggerOn never runs, and ToolRegistry.SetAuditLogger
// is never called on any tool.
//
// That matters because AuthoringDeps.begin refuses outright on a nil Audit —
// correctly, because FR-090 admits no mutation without a record. The two facts
// together mean that if NewAuthorAuditLogger(nil) returned nil, the seven
// authoring tools would be registered, catalogued, seeded "allow"/"ask" — and
// would refuse EVERY call on the configuration almost every operator runs.
// A feature that ships dead, with a green suite, is the exact failure this
// wave exists to close, one layer up from where it was found.
//
// The rule these tests pin down: the audit GUARANTEE holds, the audit SINK
// degrades. There is always a sink; with an audit.Logger it is the real audit
// file, without one it is the structured log. FR-090 stays true either way.

// a4DefaultInstallDeps builds the tools EXACTLY as pkg/agent's
// registerKnowledgeTools does on a default install: home, and the no-logger
// sink. It deliberately does NOT use a4Deps' recording sink — the whole point
// is to exercise the production wiring rather than a test double.
func a4DefaultInstallDeps(home string) AuthoringDeps {
	return AuthoringDeps{
		Home:  home,
		Audit: NewAuthorAuditLogger(nil),
	}
}

// TestAuthoringTools_WorkOnADefaultInstallWithNoAuditLogger is the regression
// that keeps the fallback sink from being "simplified" away.
//
// DIES ON: NewAuthorAuditLogger(nil) returning nil (its previous behaviour).
// The create is then refused by the nil-Audit gate and this test fails on the
// first require.
func TestAuthoringTools_WorkOnADefaultInstallWithNoAuditLogger(t *testing.T) {
	home, ws, root := a4Fixture(t, "KB")
	deps := a4DefaultInstallDeps(home)

	res := a4Tool(t, deps, "knowledge_create").Execute(a4Ctx("ava", ws), map[string]any{
		"collection": "KB",
		"path":       "notes/default-install.md",
		"body":       "written on a gateway with sandbox.audit_log unset",
	})

	require.Falsef(t, res.IsError,
		"knowledge_create must succeed on a DEFAULT install (sandbox.audit_log unset, so "+
			"al.auditLogger is nil and SetAuditLogger is never called). It refused with %q. "+
			"A nil Audit is a fail-closed refusal by design; the fix is that a sink always "+
			"exists, never that the gate is removed", res.ForLLM)

	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash("notes/default-install.md")))
	require.NoError(t, err, "the note must actually be on disk, not merely reported as written")
	assert.Contains(t, string(body), "written on a gateway with sandbox.audit_log unset")
}

// TestAuthoringTools_DefaultInstallStillRecordsEveryMutationAndRefusal proves
// the second half: succeeding is not enough, FR-090 requires a RECORD.
//
// The oracle is the structured log, captured through slog's own handler rather
// than by inspecting the sink — a test that asserts "the sink was called" would
// pass against a sink that formats nothing and emits nothing.
//
// DIES ON: authorAuditSlog.RecordKnowledgeWrite becoming a no-op, and on
// NewAuthorAuditLogger(nil) returning a silent sink instead of the slog one.
func TestAuthoringTools_DefaultInstallStillRecordsEveryMutationAndRefusal(t *testing.T) {
	home, ws, _ := a4Fixture(t, "KB")
	deps := a4DefaultInstallDeps(home)

	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(restore) })

	// 1. An APPLIED mutation.
	applied := a4Tool(t, deps, "knowledge_create").Execute(a4Ctx("ava", ws), map[string]any{
		"collection": "KB",
		"path":       "notes/recorded.md",
		"body":       "hello",
	})
	require.False(t, applied.IsError, applied.ForLLM)

	// 2. A REFUSAL the lower layers never see — a collection outside the
	//    caller's workspace. FR-090's "and every refusal" half, and the one an
	//    operator most needs: an agent reaching for a knowledge base it may not
	//    address.
	refused := a4Tool(t, deps, "knowledge_create").Execute(a4Ctx("ava", ws), map[string]any{
		"collection": "SomeOtherWorkspacesVault",
		"path":       "notes/nope.md",
		"body":       "hello",
	})
	require.True(t, refused.IsError,
		"a create against a collection outside the caller's workspace must be refused (US-9)")

	records := a4SlogRecords(t, &buf)

	appliedRec := a4FindRecord(records, "knowledge.note.create", "applied")
	require.NotNilf(t, appliedRec,
		"no audit record for the APPLIED create. FR-090 requires one for every mutation, "+
			"and sandbox.audit_log being off is a configuration of WHERE the record goes, "+
			"never of WHETHER there is one. Captured log: %s", buf.String())
	assert.Equal(t, "ava", appliedRec["agent_id"], "US-15 AS-1: the record must name the agent")
	assert.Equal(t, ws, appliedRec["workspace_id"])
	assert.Contains(t, a4Strings(appliedRec["paths"]), "notes/recorded.md",
		"US-15 AS-1: the record must name the paths it touched")

	refusedRec := a4FindRecord(records, "knowledge.note.create", "refused")
	require.NotNilf(t, refusedRec,
		"no audit record for the REFUSED create. This is the half that is usually "+
			"forgotten and the one that matters most: a refusal that leaves no trace is how "+
			"a silent failure hides (pkg/knowledge/audit.go's header). Captured log: %s",
		buf.String())
	assert.Equal(t, "ava", refusedRec["agent_id"])
	assert.NotEmpty(t, refusedRec["reason"], "a refusal must record why")

	// The record must never carry note content — the audit log must not become
	// a second copy of the operator's notes.
	assert.NotContains(t, buf.String(), "hello",
		"the record must not carry the note body")
}

// a4SlogRecords parses the captured JSON log lines that came from the
// knowledge audit sink.
func a4SlogRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(buf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		if _, ok := rec["event"]; !ok {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// a4FindRecord returns the first record matching an event name and outcome.
func a4FindRecord(records []map[string]any, event, outcome string) map[string]any {
	for _, r := range records {
		if r["event"] == event && r["outcome"] == outcome {
			return r
		}
	}
	return nil
}

// a4Strings renders a decoded JSON array of strings.
func a4Strings(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
