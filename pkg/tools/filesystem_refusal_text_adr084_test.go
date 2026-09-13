// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — ADR-084 JUDGE-FR-064: a not-found and a refusal must be
// distinguishable from the tool result alone.
//
// This is not cosmetic, and the FR says so: it is "the one requirement in §J
// with a real engine oracle", and the mapping step that consumes it depends
// on the distinction being true IN CODE. The consequence of getting it wrong
// is specific and bad — a path the Judge was REFUSED reads to the mapper as
// a path that was ABSENT, so "I could not look" becomes "the work is not
// done", and the criterion is failed for a reason that has nothing to do
// with the work. JUDGE-FR-060's new confinement makes refusals strictly more
// common on exactly the paths a verifier reaches for, so this gets harder to
// ignore, not easier.
//
// What "stable" means here, and why the marker constants are spelled out
// rather than read back from the code: a downstream consumer keys on these
// substrings. If someone rewords ErrOutsideScope or wrapFSErr, this test is
// what goes red — instead of the discriminator silently classifying every
// refusal as a not-found and nobody noticing until a verdict is wrong.
// Deriving the expected strings from the production constants would make the
// test pass through any rewording, which is the opposite of what is wanted.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// The stable markers JUDGE-FR-064 requires. Hand-written on purpose — see
// the package comment above.
const (
	// notFoundMarkerADR084 is what wrapFSErr produces for an ENOENT.
	notFoundMarkerADR084 = "file not found"
	// refusalMarkerOutsideScopeADR084 is ErrOutsideScope's own text — the
	// class a JUDGE-FR-060 confinement refusal carries.
	refusalMarkerOutsideScopeADR084 = "path is outside the effective filesystem scope"
	// refusalMarkerCarveOutADR084 is ErrCarveOut's own text — the other
	// refusal class a verifier can hit, on the secret set.
	refusalMarkerCarveOutADR084 = "path is a protected carve-out"
	// refusalStructuredCodeADR084 is the machine-readable discriminator on
	// the structured refusal payload. A consumer that keys on this rather
	// than on prose is better off still, so it is pinned too.
	refusalStructuredCodeADR084 = PermissionDeniedCode
)

// refusalTextTree lays out a work dir, a file outside it, and the install's
// master.key, so all three outcomes (not found / confinement refusal /
// carve-out refusal) can be produced through the real ReadFileTool.
type refusalTextTree struct {
	home        string
	workDir     string
	missingPath string // inside the work dir, deliberately never created
	outsidePath string // exists, outside the work dir
	masterKey   string // a carve-out
}

func newRefusalTextTree(t *testing.T) refusalTextTree {
	t.Helper()
	raw := t.TempDir()
	workDir := filepath.Join(raw, "agents", "judge")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir workDir: %v", err)
	}
	outside := filepath.Join(raw, "sessions", "other", "transcript.jsonl")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.WriteFile(outside, []byte(`{"role":"user","content":"hello"}`), 0o600); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
	masterKey := filepath.Join(raw, "master.key")
	if err := os.WriteFile(masterKey, []byte("key-material"), 0o600); err != nil {
		t.Fatalf("write master.key: %v", err)
	}

	resolved, err := filepath.EvalSymlinks(raw)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", raw, err)
	}

	return refusalTextTree{
		home:        resolved,
		workDir:     filepath.Join(resolved, "agents", "judge"),
		missingPath: filepath.Join(resolved, "agents", "judge", "never-created.txt"),
		outsidePath: filepath.Join(resolved, "sessions", "other", "transcript.jsonl"),
		masterKey:   filepath.Join(resolved, "master.key"),
	}
}

// TestReadFileTool_NotFoundAndRefusalTextsAreDistinct is JUDGE-FR-064's
// oracle, run through the real read_file tool rather than through
// ResolvePath, because the mapper consumes the TOOL RESULT and a
// distinction that exists at the error but is flattened by the result
// formatter would be no distinction at all.
//
// Both directions are asserted for every row: the expected marker is
// present AND the other class's marker is absent. One-directional assertions
// would pass against a result that said "file not found: access denied:
// path is outside…", which is exactly the ambiguity the FR forbids.
func TestReadFileTool_NotFoundAndRefusalTextsAreDistinct(t *testing.T) {
	tr := newRefusalTextTree(t)
	t.Setenv("OMNIPUS_HOME", tr.home)

	confinedCtx := WithReadConfined(context.Background(), true)
	tool := NewReadFileTool(tr.workDir, true, MaxReadFileSize)

	cases := []struct {
		name        string
		ctx         context.Context
		path        string
		wantMarkers []string
		denyMarkers []string
	}{
		{
			name: "a path that does not exist reads as not found",
			ctx:  confinedCtx,
			path: tr.missingPath,
			wantMarkers: []string{
				notFoundMarkerADR084,
			},
			denyMarkers: []string{
				refusalMarkerOutsideScopeADR084,
				refusalMarkerCarveOutADR084,
				refusalStructuredCodeADR084,
			},
		},
		{
			name: "a path outside a confined turn's reach reads as a refusal",
			ctx:  confinedCtx,
			path: tr.outsidePath,
			wantMarkers: []string{
				refusalMarkerOutsideScopeADR084,
				refusalStructuredCodeADR084,
			},
			denyMarkers: []string{
				notFoundMarkerADR084,
				refusalMarkerCarveOutADR084,
			},
		},
		{
			name: "a carve-out reads as a refusal, in an unconfined turn too",
			ctx:  context.Background(),
			path: tr.masterKey,
			wantMarkers: []string{
				refusalMarkerCarveOutADR084,
				refusalStructuredCodeADR084,
			},
			denyMarkers: []string{
				notFoundMarkerADR084,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Execute(tc.ctx, map[string]any{"path": tc.path})
			if res == nil {
				t.Fatal("tool returned a nil result")
			}
			// Every row here is an error outcome, so IsError cannot be the
			// discriminator — which is precisely why the TEXT has to be.
			if !res.IsError {
				t.Fatalf("expected an error result for %q, got:\n%s", tc.path, res.ForLLM)
			}
			for _, want := range tc.wantMarkers {
				if !strings.Contains(res.ForLLM, want) {
					t.Fatalf("JUDGE-FR-064: result for %q is missing the stable marker %q:\n%s", tc.path, want, res.ForLLM)
				}
			}
			for _, unwanted := range tc.denyMarkers {
				if strings.Contains(res.ForLLM, unwanted) {
					t.Fatalf("JUDGE-FR-064: result for %q also carries the other class's marker %q, so the two are not distinguishable:\n%s",
						tc.path, unwanted, res.ForLLM)
				}
			}
		})
	}

	// The pairwise property, stated directly: no two of the three outcomes
	// produce the same text. Written separately from the marker rows above
	// because "each carries its own marker" and "no two are identical" are
	// different claims, and the FR needs both.
	t.Run("the three outcomes produce three different texts", func(t *testing.T) {
		seen := map[string]string{}
		for _, row := range []struct {
			label string
			ctx   context.Context
			path  string
		}{
			{"not_found", confinedCtx, tr.missingPath},
			{"outside_scope", confinedCtx, tr.outsidePath},
			{"carve_out", context.Background(), tr.masterKey},
		} {
			res := tool.Execute(row.ctx, map[string]any{"path": row.path})
			if prev, dup := seen[res.ForLLM]; dup {
				t.Fatalf("%s and %s produce byte-identical tool results:\n%s", prev, row.label, res.ForLLM)
			}
			seen[res.ForLLM] = row.label
		}
	})
}

// ============================================================================
// ADR-084 JUDGE-FR-084 — the audit half
// ============================================================================
//
// The engine-side oracle for FR-084 lives in pkg/agent (it drives a real
// adjudication). This file owns the half that lives HERE: the emit sites in
// pkg/tools. Without these entries the pkg/agent test has nothing to find,
// so proving them where they are written is not duplication — it is the
// difference between "the Judge's reads are audited" and "the Judge's reads
// would be audited if something emitted them".
//
// Before ADR-084 a successful read was audited NOWHERE. Deleting
// emitFileReadAudit's call site leaves every other test in this package
// green; the rows below are what go red.

// auditRow is one decoded audit.jsonl line, reduced to the fields FR-084
// names.
type auditRow struct {
	Event     string         `json:"event"`
	Decision  string         `json:"decision"`
	AgentID   string         `json:"agent_id"`
	SessionID string         `json:"session_id"`
	Tool      string         `json:"tool"`
	Details   map[string]any `json:"details"`
}

// readAuditRows closes the logger (forcing a flush) and decodes every line
// of audit.jsonl. Closing first is not optional: reading the file while the
// logger still buffers is how an audit test reports "no entries" for entries
// that were in fact written.
func readAuditRows(t *testing.T, logger *audit.Logger, dir string) []auditRow {
	t.Helper()
	if err := logger.Close(); err != nil {
		t.Fatalf("close audit logger: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatalf("read audit.jsonl: %v", err)
	}
	var rows []auditRow
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row auditRow
		if jerr := json.Unmarshal([]byte(line), &row); jerr != nil {
			t.Fatalf("audit line is not JSON: %v\n%s", jerr, line)
		}
		rows = append(rows, row)
	}
	return rows
}

func (r auditRow) detail(key string) string {
	v, _ := r.Details[key].(string)
	return v
}

// TestFilesystemAudit_ReadsAndDenialsAreAttributedAndCorrelated is the
// pkg/tools half of JUDGE-FR-084, shaped after the judge specification's own
// scenario: "an adjudication in which the Judge read two files and was
// refused a third ... the audit log holds two read entries and one
// path.access_denied entry ... all three are attributed to the Judge's agent
// id and carry the adjudication id".
func TestFilesystemAudit_ReadsAndDenialsAreAttributedAndCorrelated(t *testing.T) {
	tr := newRefusalTextTree(t)
	t.Setenv("OMNIPUS_HOME", tr.home)

	// Two readable artifacts inside the Judge's confined work dir.
	first := filepath.Join(tr.workDir, "first.txt")
	second := filepath.Join(tr.workDir, "second.txt")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, []byte("artifact content for "+filepath.Base(p)), 0o600); err != nil {
			t.Fatalf("write %q: %v", p, err)
		}
	}

	const judgeAgentID = "omnipus-judge"
	const adjudicationID = "adj-42"
	const sessionID = "sess-under-review"

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}

	tool := NewReadFileTool(tr.workDir, true, MaxReadFileSize)
	tool.SetAuditLogger(auditLogger)

	ctx := WithAgentID(context.Background(), judgeAgentID)
	ctx = WithTranscriptSessionID(ctx, sessionID)
	ctx = WithReadConfined(ctx, true)
	ctx = WithVerifierAdjudicationID(ctx, adjudicationID)

	for _, p := range []string{first, second} {
		if res := tool.Execute(ctx, map[string]any{"path": p}); res.IsError {
			t.Fatalf("reading %q failed: %s", p, res.ForLLM)
		}
	}
	// The third path is outside the confined work dir — refused by
	// JUDGE-FR-060, which is exactly the refusal FR-084 must make visible.
	if res := tool.Execute(ctx, map[string]any{"path": tr.outsidePath}); !res.IsError {
		t.Fatalf("expected the out-of-workdir read to be refused, got:\n%s", res.ForLLM)
	}

	rows := readAuditRows(t, auditLogger, auditDir)

	var reads, denials []auditRow
	for _, row := range rows {
		switch {
		case row.Event == audit.EventFileOp && row.detail("op") == "read":
			reads = append(reads, row)
		case row.Event == PathAccessDeniedEvent:
			denials = append(denials, row)
		}
	}

	if len(reads) != 2 {
		t.Fatalf("want 2 read entries, got %d (all rows: %+v)", len(reads), rows)
	}
	if len(denials) != 1 {
		t.Fatalf("want 1 path.access_denied entry, got %d (all rows: %+v)", len(denials), rows)
	}

	// Attribution and correlation, asserted on all three entries — the
	// scenario's "all three" is the point, so checking only the reads would
	// leave the denial (the entry an operator most wants) unproven.
	for _, row := range append(append([]auditRow{}, reads...), denials...) {
		if row.AgentID != judgeAgentID {
			t.Fatalf("entry %+v is attributed to %q, want the Judge's agent id %q", row, row.AgentID, judgeAgentID)
		}
		if row.detail("adjudication_id") != adjudicationID {
			t.Fatalf("entry %+v does not carry the adjudication id %q", row, adjudicationID)
		}
		if row.SessionID != sessionID {
			t.Fatalf("entry %+v carries session id %q, want %q", row, row.SessionID, sessionID)
		}
		if row.Tool != "read_file" {
			t.Fatalf("entry %+v names tool %q, want read_file", row, row.Tool)
		}
	}

	// The read entries must name WHICH files — an audit row that says a read
	// happened but not of what answers none of the question FR-084 poses.
	gotPaths := map[string]bool{}
	for _, row := range reads {
		gotPaths[row.detail("path")] = true
	}
	for _, want := range []string{first, second} {
		if !gotPaths[want] {
			t.Fatalf("no read entry names %q; got %v", want, gotPaths)
		}
	}
	if reads[0].Decision != audit.DecisionAllow {
		t.Fatalf("a successful read was recorded with decision %q, want allow", reads[0].Decision)
	}
	if denials[0].Decision != audit.DecisionDeny {
		t.Fatalf("a refusal was recorded with decision %q, want deny", denials[0].Decision)
	}
	if denials[0].detail("path") != tr.outsidePath {
		t.Fatalf("the denial names %q, want the refused target %q", denials[0].detail("path"), tr.outsidePath)
	}
}

// TestFilesystemAudit_OrdinaryTurnRowsAreUncorrelated pins the other half of
// the correlation rule: a turn that is NOT an adjudication produces rows
// with no adjudication_id key at all, not an empty one.
//
// This is the compatibility assertion. Every consumer of path.access_denied
// that exists today was written before ADR-084; adding a key to every row in
// the product would be a wire change nobody asked for. It also keeps the
// correlation meaningful — a field that is present-but-empty on most rows is
// a field a query has to defend against rather than one it can filter on.
func TestFilesystemAudit_OrdinaryTurnRowsAreUncorrelated(t *testing.T) {
	tr := newRefusalTextTree(t)
	t.Setenv("OMNIPUS_HOME", tr.home)

	artifact := filepath.Join(tr.workDir, "notes.txt")
	if err := os.WriteFile(artifact, []byte("ordinary agent content"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}

	tool := NewReadFileTool(tr.workDir, true, MaxReadFileSize)
	tool.SetAuditLogger(auditLogger)

	// No WithVerifierAdjudicationID, and no confinement — an ordinary agent.
	ctx := WithAgentID(context.Background(), "mia")
	ctx = WithTranscriptSessionID(ctx, "sess-chat")

	if res := tool.Execute(ctx, map[string]any{"path": artifact}); res.IsError {
		t.Fatalf("ordinary read failed: %s", res.ForLLM)
	}
	if res := tool.Execute(ctx, map[string]any{"path": tr.masterKey}); !res.IsError {
		t.Fatal("expected the carve-out read to be refused")
	}

	rows := readAuditRows(t, auditLogger, auditDir)
	if len(rows) == 0 {
		t.Fatal("no audit rows were written for an ordinary turn")
	}
	sawRead, sawDenial := false, false
	for _, row := range rows {
		if _, present := row.Details["adjudication_id"]; present {
			t.Fatalf("an ordinary turn's audit row carries adjudication_id: %+v", row)
		}
		if row.Event == audit.EventFileOp && row.detail("op") == "read" {
			sawRead = true
		}
		if row.Event == PathAccessDeniedEvent {
			sawDenial = true
		}
	}
	// Guard against the assertion above passing vacuously: it must have
	// examined the two row kinds this test is about.
	if !sawRead {
		t.Fatal("no read entry was written for an ordinary agent's successful read — FR-084's emit site is not on the general path")
	}
	if !sawDenial {
		t.Fatal("no path.access_denied entry was written for an ordinary agent's refused read")
	}
}

// TestListDirAudit_RefusalIsRecorded covers the operation JUDGE-FR-060
// confines that had NO audit logger at all before ADR-084.
//
// The Judge holds list_directory. An unaudited list_directory refusal means
// the single most likely confinement event a verifier produces — reaching
// for $OMNIPUS_HOME/sessions/ — leaves no trace anywhere. Reverting
// ListDirTool.SetAuditLogger makes this test red and nothing else in the
// package notice.
func TestListDirAudit_RefusalIsRecorded(t *testing.T) {
	tr := newRefusalTextTree(t)
	t.Setenv("OMNIPUS_HOME", tr.home)

	auditDir := t.TempDir()
	auditLogger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 90})
	if err != nil {
		t.Fatalf("audit.NewLogger: %v", err)
	}

	tool := NewListDirTool(tr.workDir, true)
	tool.SetAuditLogger(auditLogger)

	ctx := WithAgentID(context.Background(), "omnipus-judge")
	ctx = WithTranscriptSessionID(ctx, "sess-under-review")
	ctx = WithReadConfined(ctx, true)
	ctx = WithVerifierAdjudicationID(ctx, "adj-77")

	// Allowed: its own work dir.
	if res := tool.Execute(ctx, map[string]any{"path": tr.workDir}); res.IsError {
		t.Fatalf("listing the own work dir failed: %s", res.ForLLM)
	}
	// Refused: the install's sessions directory.
	sessionsDir := filepath.Dir(tr.outsidePath)
	if res := tool.Execute(ctx, map[string]any{"path": sessionsDir}); !res.IsError {
		t.Fatalf("expected the sessions listing to be refused, got:\n%s", res.ForLLM)
	}

	rows := readAuditRows(t, auditLogger, auditDir)
	var sawList, sawDenial bool
	for _, row := range rows {
		if row.Tool != "list_directory" {
			continue
		}
		if row.detail("adjudication_id") != "adj-77" {
			t.Fatalf("list_directory audit row is not correlated: %+v", row)
		}
		if row.Event == audit.EventFileOp && row.detail("op") == "list" {
			sawList = true
		}
		if row.Event == PathAccessDeniedEvent && row.detail("path") == sessionsDir {
			sawDenial = true
		}
	}
	if !sawList {
		t.Fatalf("no list entry recorded for an allowed listing; rows: %+v", rows)
	}
	if !sawDenial {
		t.Fatalf("no path.access_denied entry recorded for the refused listing; rows: %+v", rows)
	}
}
