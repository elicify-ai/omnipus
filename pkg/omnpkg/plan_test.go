package omnpkg_test

// RED B1 — the export review ("plan") unit tests (spec TDD order 1–4, issue
// #1967).
//
// Spec: docs/internal/specs/export-import-b1-spec.md revision 5 — the
// "left_out rules for one agent" table, "disclosures", Dataset D1 rows 1–18,
// Dataset D6, FR-002 (same refusals as export), FR-003 (fields, 30-minute
// TTL, size within 256 bytes, same file name), FR-004 (rows, order, messages,
// items, no values), SC-003/SC-004.
//
// Oracle independence: every expected row, message, order and boundary below
// is transcribed from that specification, never from implementation output.
// The bundle used for the wording and size comparisons is built and decoded by
// the test itself (test-owned zstd/tar), never by reading a handler back.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

// b1PlanTTL is the spec's review lifetime (Scope Boundaries: exactly 30
// minutes from created_at; held while now < expires_at). A literal, not the
// engine constant, so the oracle stays independent of the implementation.
const b1PlanTTL = 30 * time.Minute

// planAt runs BuildExportPlan for one agent and returns the plan.
func planAt(t *testing.T, src *fakeSource, id string, now time.Time) *omnpkg.ExportPlan {
	t.Helper()
	plan, err := omnpkg.BuildExportPlan(src, omnpkg.Selection{AgentIDs: []string{id}}, now)
	require.NoError(t, err, "the review must be produced for %s", id)
	require.NotNil(t, plan)
	return plan
}

// wantLeftOutRow asserts one left_out row by position: type, reason, exact
// message and items (spec left_out rules table; items absent means the row
// carries none).
func wantLeftOutRow(t *testing.T, rows []omnpkg.LeftOutItem, idx int, typ, reason, message string, items []string) {
	t.Helper()
	require.Less(t, idx, len(rows), "expected a left_out row %d (%s), got %d rows: %+v", idx, typ, len(rows), rows)
	row := rows[idx]
	require.Equal(t, typ, row.Type, "row %d type", idx)
	require.Equal(t, reason, row.Reason, "row %d (%s) reason", idx, typ)
	require.Equal(t, message, row.Message, "row %d (%s) message must match the spec text exactly", idx, typ)
	if items == nil {
		require.Empty(t, row.Items, "row %d (%s) carries no items", idx, typ)
	} else {
		require.Equal(t, items, row.Items, "row %d (%s) items", idx, typ)
	}
}

// b1FixedLeftOut asserts the four rows every agent's review carries, in the
// spec's order.
func b1FixedLeftOut(t *testing.T, rows []omnpkg.LeftOutItem) {
	t.Helper()
	wantLeftOutRow(t, rows, 0, "agent_memory", "type_not_exportable",
		"This agent's memory is not included in this version.", nil)
	wantLeftOutRow(t, rows, 1, "session", "type_not_exportable",
		"This agent's chats are not included.", nil)
	wantLeftOutRow(t, rows, 2, "work_files", "type_not_exportable",
		"Workspace files are not included.", nil)
	wantLeftOutRow(t, rows, 3, "team", "not_carried",
		"Team membership and delegation lines are not included. On import the agent joins the workspace you choose, with no delegation lines.", nil)
}

// b1FreeTextDisclosure asserts the one disclosure every review carries (ADR
// D-E4 layer 3), with the exact spec text.
func b1FreeTextDisclosure(t *testing.T, disclosures []omnpkg.PlanDisclosure) {
	t.Helper()
	require.Len(t, disclosures, 1, "B1 always carries exactly one disclosure")
	require.Equal(t, "free_text_copied", disclosures[0].Code)
	require.Equal(t, "Text you wrote (prompts and instructions) is copied exactly as it is. If you pasted a password or key into it, it goes with the bundle.",
		disclosures[0].Message, "the free-text disclosure text is exact")
}

// d1Body builds a printable body of exactly n bytes (deterministic content).
func d1Body(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

// d1Soul2k / d1AgentMD500 are the D1-1 fixture bodies: 2,000 and 500 bytes.
var (
	d1Soul2k    = d1Body(2000)
	d1AgentMD50 = d1Body(500)
)

// d1MainRecord is a Main ("custom") record the export rules accept.
func d1MainRecord(id, name string, extra map[string]any) map[string]any {
	rec := map[string]any{
		"id":          id,
		"name":        name,
		"description": "Drafts and edits prose",
		"type":        "custom",
		"model":       map[string]any{"primary": "m-1", "provider": "openrouter"},
	}
	for k, v := range extra {
		rec[k] = v
	}
	return rec
}

// d1WorkerRecord is a native worker record with a description.
func d1WorkerRecord(id, name string, extra map[string]any) map[string]any {
	rec := map[string]any{
		"id":          id,
		"name":        name,
		"description": "codes",
		"type":        "worker",
		"model":       map[string]any{"primary": "m-1", "provider": "openrouter"},
	}
	for k, v := range extra {
		rec[k] = v
	}
	return rec
}

// TestPlanExport_LeftOutRulesPerKind — spec order 1 ("Review contents per
// agent kind", Dataset D1 rows 1–4): kind, files, left_out rows in the exact
// table order with the exact messages and items, needs_secrets, the one
// disclosure, and the 30-minute TTL.
func TestPlanExport_LeftOutRulesPerKind(t *testing.T) {
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

	t.Run("D1-1 Main with AGENT.md: three files, four fixed rows, no secrets", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, d1MainRecord(testSourceID, "Writer", nil), d1Soul2k, d1AgentMD50)
		plan := planAt(t, src, testSourceID, now)

		require.Equal(t, "agents", plan.Scope, "scope echoes the request")
		require.Len(t, plan.Elements, 1, "B1 reviews exactly one element")
		el := plan.Elements[0]
		require.Equal(t, "agent", el.Type)
		require.Equal(t, testSourceID, el.SourceID)
		require.Equal(t, "Writer", el.Name)
		require.Equal(t, "main", el.Kind, "a custom Main reviews as kind main")

		paths := make([]string, 0, len(el.Files))
		for _, f := range el.Files {
			paths = append(paths, f.Path)
		}
		require.Equal(t, []string{recordPath(testSourceID), soulPath(testSourceID), instructionsPath(testSourceID)},
			paths, "files are agent.json, SOUL.md, AGENT.md")
		require.Equal(t, int64(2000), el.Files[1].Size, "SOUL.md size is the reviewed byte size")
		require.Equal(t, int64(500), el.Files[2].Size, "AGENT.md size is the reviewed byte size")
		require.Positive(t, el.Files[0].Size, "the settings record carries a size")

		var total int64
		for _, f := range el.Files {
			total += f.Size
		}
		require.Equal(t, total, el.Bytes, "element bytes is the sum of its file sizes")

		require.Len(t, plan.LeftOut, 4, "only the four fixed rows for an agent with no skills/connectors")
		b1FixedLeftOut(t, plan.LeftOut)
		require.Empty(t, plan.NeedsSecrets, "no secrets to enter again for D1-1")
		b1FreeTextDisclosure(t, plan.Disclosures)
		require.Equal(t, now.Add(b1PlanTTL), plan.ExpiresAt, "expires_at is created_at + 30 minutes")
		require.WithinDuration(t, now, plan.CreatedAt, time.Second)
	})

	t.Run("D1-2 skills and connector: two extra rows with items, in table order", func(t *testing.T) {
		src := newFakeSource()
		rec := d1MainRecord(testSourceID, "Writer", map[string]any{
			"skills": []string{"research", "Summarize"},
			"tools":  map[string]any{"mcp": map[string]any{"servers": []any{map[string]any{"id": "github"}}}},
		})
		src.withAgent(t, testSourceID, rec, d1Soul2k, nil)
		plan := planAt(t, src, testSourceID, now)

		paths := make([]string, 0, 2)
		for _, f := range plan.Elements[0].Files {
			paths = append(paths, f.Path)
		}
		require.Equal(t, []string{recordPath(testSourceID), soulPath(testSourceID)},
			paths, "no AGENT.md when none is stored")

		require.Len(t, plan.LeftOut, 6, "four fixed rows + skill + connector")
		b1FixedLeftOut(t, plan.LeftOut)
		wantLeftOutRow(t, plan.LeftOut, 4, "skill", "type_not_exportable",
			"Skills are not included. The agent keeps their names; the other install uses a skill with the same name if it has one.",
			[]string{"research", "Summarize"})
		wantLeftOutRow(t, plan.LeftOut, 5, "connector", "not_restored",
			"Connector names travel inside the agent's settings, but no connector settings or secrets are included, and the assignments are not restored on import. Assign connectors again after import.",
			[]string{"github"})
		require.Empty(t, plan.NeedsSecrets, "connector names are not needs_secrets")
	})

	t.Run("D1-3 native worker: kind worker, SOUL.md packaged", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID2, d1WorkerRecord(testSourceID2, "Coder", nil), d1Soul2k, nil)
		plan := planAt(t, src, testSourceID2, now)
		require.Equal(t, "worker", plan.Elements[0].Kind)
		paths := make([]string, 0, 2)
		for _, f := range plan.Elements[0].Files {
			paths = append(paths, f.Path)
		}
		require.Equal(t, []string{recordPath(testSourceID2), soulPath(testSourceID2)}, paths)
		require.Len(t, plan.LeftOut, 4, "only the fixed rows")
	})

	t.Run("D1-4 external worker: no SOUL.md, runtime rows, executor secrets", func(t *testing.T) {
		src := newFakeSource()
		rec := d1WorkerRecord(testSourceID, "Coder", map[string]any{
			"subagents": map[string]any{"executor": map[string]any{
				"kind":          "external-cli",
				"cli":           "codex",
				"cli_path":      "/opt/x/CANARY_CLI_PATH/claude",
				"cli_args":      "--token=CANARY_ARG --verbose",
				"env_overrides": map[string]string{"API_TOKEN": "CANARY_ENV", "REGION": "eu"},
			}},
		})
		src.withAgent(t, testSourceID, rec, nil, []byte(testAgentBody))
		plan := planAt(t, src, testSourceID, now)

		require.Equal(t, "external_worker", plan.Elements[0].Kind)
		paths := make([]string, 0, 2)
		for _, f := range plan.Elements[0].Files {
			paths = append(paths, f.Path)
		}
		require.Equal(t, []string{recordPath(testSourceID), instructionsPath(testSourceID)},
			paths, "an external worker packages agent.json (+ AGENT.md) and never SOUL.md")

		// The spec's external-worker left-out list: the four fixed rows plus
		// exactly three runtime rows — program path, env override values, and
		// extra command-line arguments (CHECK F03: the count was 8 in the RED
		// pack; the spec enumerates 4 + 3 = 7, so the count follows the spec).
		require.Len(t, plan.LeftOut, 7, "four fixed rows + program path + env override values + extra CLI arguments")
		b1FixedLeftOut(t, plan.LeftOut)
		wantLeftOutRow(t, plan.LeftOut, 4, "agent_setting", "host_path",
			"The program path is not included. The other install uses its own setting for this tool.", nil)
		wantLeftOutRow(t, plan.LeftOut, 5, "agent_setting", "secret_value",
			"Environment override values are not included. Enter them again after import.",
			[]string{"API_TOKEN", "REGION"})
		wantLeftOutRow(t, plan.LeftOut, 6, "agent_setting", "secret_value",
			"Extra command-line arguments are not included. Enter them again after import.", nil)

		require.Len(t, plan.NeedsSecrets, 2, "both removed environment names are needs_secrets")
		require.Equal(t, "API_TOKEN", plan.NeedsSecrets[0].Name)
		require.Equal(t, "REGION", plan.NeedsSecrets[1].Name)
		require.Equal(t, omnpkg.NeedsSecretKindExecutorEnv, plan.NeedsSecrets[0].Kind)
		require.Equal(t, omnpkg.NeedsSecretKindExecutorEnv, plan.NeedsSecrets[1].Kind)
		require.Equal(t, testSourceID, plan.NeedsSecrets[0].UsedBy)
	})
}

// TestPlanExport_RefusalsEqualExport — spec order 2: every refusal the export
// gives, the review gives with the same code (FR-002, SC-004; Dataset D1 rows
// 5–12) — plus the size boundary on the safe side (row 5 reviews fine).
func TestPlanExport_RefusalsEqualExport(t *testing.T) {
	// refusals pairs each fixture with the code the spec assigns the export
	// refusal ("Review: export refusal → the export's code").
	tests := []struct {
		name       string
		record     map[string]any
		soul       []byte
		agentMD    []byte
		coreID     bool // the source reports the ID as a built-in
		wantCode   string
		wantDetail map[string]string
	}{
		{
			name:     "D1-11 mia (built-in id)",
			record:   d1MainRecord("mia", "Mia", nil),
			coreID:   true,
			wantCode: omnpkg.CodeCoreAgentNotExportable,
		},
		{
			name:     "D1-12 planner (locked shipped subagent id)",
			record:   d1MainRecord("planner", "Planner", nil),
			coreID:   true,
			wantCode: omnpkg.CodeCoreAgentNotExportable,
		},
		{
			name:     "D1-7 blank SOUL.md",
			record:   d1MainRecord(testSourceID, "Writer", nil),
			soul:     []byte(""),
			wantCode: omnpkg.CodeAgentPromptMissing,
		},
		{
			name:     "D1-8 whitespace-only SOUL.md",
			record:   d1MainRecord(testSourceID, "Writer", nil),
			soul:     []byte(" \n\t \n"),
			wantCode: omnpkg.CodeAgentPromptMissing,
		},
		{
			name:       "D1-6 SOUL.md one byte over 1 MiB",
			record:     d1MainRecord(testSourceID, "Writer", nil),
			soul:       bytes.Repeat([]byte("a"), 1048577),
			wantCode:   omnpkg.CodeAgentFileUnreadable,
			wantDetail: map[string]string{"file": omnpkg.AgentSoulName, "check": omnpkg.CheckTooLarge},
		},
		{
			name:       "D1-9 SOUL.md is a symlink",
			record:     d1MainRecord(testSourceID, "Writer", nil),
			wantCode:   omnpkg.CodeAgentFileUnreadable,
			wantDetail: map[string]string{"file": omnpkg.AgentSoulName, "check": omnpkg.CheckLink},
		},
		{
			name:       "D1-10 worker with empty description",
			record:     d1WorkerRecord(testSourceID, "Helper", map[string]any{"description": ""}),
			soul:       d1Soul2k,
			wantCode:   omnpkg.CodeElementNotCreatable,
			wantDetail: map[string]string{"field:reason": "description"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := newFakeSource()
			home := src.withAgent(t, testSourceID, tc.record, tc.soul, tc.agentMD)
			if tc.coreID {
				src.coreIDs[testSourceID] = true
			}
			if tc.name == "D1-9 SOUL.md is a symlink" {
				require.NoError(t, os.Remove(filepath.Join(home, omnpkg.AgentSoulName)))
				require.NoError(t, os.Symlink(filepath.Join(home, "credentials-like.json"), filepath.Join(home, omnpkg.AgentSoulName)))
			}

			// The oracle is the export's own refusal for the same agent: the
			// review must answer the same code and status class (FR-002).
			var exportBuf bytes.Buffer
			exportErr := omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &exportBuf)
			require.Error(t, exportErr, "the fixture must refuse at export (spec precondition)")

			_, planErr := omnpkg.BuildExportPlan(src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, time.Now().UTC())
			require.Error(t, planErr, "the review must refuse where the export refuses")
			require.Equal(t, omnpkg.CodeOf(exportErr), omnpkg.CodeOf(planErr),
				"the review and the export answer the same code for the same agent state")
			require.Equal(t, tc.wantCode, omnpkg.CodeOf(planErr), "and the code is the spec's code for this row")
			var pe *omnpkg.PackageError
			require.ErrorAs(t, planErr, &pe)
			for k, v := range tc.wantDetail {
				if k == "field:reason" {
					require.Equal(t, v, pe.Field, "the refusal names the field")
					continue
				}
				require.Equal(t, v, pe.Detail[k], "detail %s", k)
			}
			require.Empty(t, exportBuf.Bytes(), "a refused export writes no bytes; the review writes nothing either")
		})
	}

	t.Run("D1-5 SOUL.md of exactly 1,048,576 bytes reviews fine", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, d1MainRecord(testSourceID, "Writer", nil),
			bytes.Repeat([]byte("a"), 1048576), nil)
		plan := planAt(t, src, testSourceID, time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC))
		require.Equal(t, int64(1048576), plan.Elements[0].Files[1].Size)
	})
}

// TestPlanExport_SizeMatchesExport — spec order 3 (FR-003, SC-003; D1 rows
// 17–18): size_bytes within 256 bytes of the exported bundle and the same
// download file name.
func TestPlanExport_SizeMatchesExport(t *testing.T) {
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	t.Run("D1-1 size within 256 bytes of the export", func(t *testing.T) {
		src := newFakeSource()
		src.withAgent(t, testSourceID, d1MainRecord(testSourceID, "Writer", nil), d1Soul2k, d1AgentMD50)
		plan := planAt(t, src, testSourceID, now)
		var out bytes.Buffer
		require.NoError(t, omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out))
		delta := plan.SizeBytes - int64(out.Len())
		if delta < 0 {
			delta = -delta
		}
		require.LessOrEqual(t, delta, int64(256), "|plan size_bytes − exported length| ≤ 256 (SC-003)")
	})

	t.Run("D1-17 and D1-18 download names follow the export rule", func(t *testing.T) {
		cases := []struct {
			agentName string
			wantFile  string
		}{
			{"Écrivain ✍", "_crivain _.omnpkg"}, // D1-17: non-ASCII/emoji → underscores
			{"..", "agent.omnpkg"},              // D1-18: trim leaves empty → "agent"
			{"Writer", "Writer.omnpkg"},
		}
		for _, tc := range cases {
			src := newFakeSource()
			src.withAgent(t, testSourceID, d1MainRecord(testSourceID, tc.agentName, nil), d1Soul2k, nil)
			plan := planAt(t, src, testSourceID, now)
			require.Equal(t, tc.wantFile, plan.FileName, "file_name for agent named %q", tc.agentName)
		}
	})
}

// TestPlanExport_WordingMatchesDecodedBundle — spec order 3a ("Review wording
// matches the real bundle"): the review's "names travel" wording must be true
// of the real bundle — skills and the connector ID still in the decoded
// agent.json, and no connector definition fields.
func TestPlanExport_WordingMatchesDecodedBundle(t *testing.T) {
	src := newFakeSource()
	rec := d1MainRecord(testSourceID, "Writer", map[string]any{
		"skills": []string{"research", "Summarize"},
		"tools":  map[string]any{"mcp": map[string]any{"servers": []any{map[string]any{"id": "github"}}}},
	})
	src.withAgent(t, testSourceID, rec, d1Soul2k, nil)

	plan := planAt(t, src, testSourceID, time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC))
	var out bytes.Buffer
	require.NoError(t, omnpkg.Export(testCtx(t), src, omnpkg.Selection{AgentIDs: []string{testSourceID}}, &out))
	dec := decodePackagePlain(t, out.Bytes())

	raw, ok := dec.Payload[recordPath(testSourceID)]
	require.True(t, ok, "the bundle payload carries agent.json")
	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored))

	// Skills names travel (the review's skill row says so).
	skillsRaw, ok := stored["skills"]
	require.True(t, ok, "the decoded record still lists the agent's skills")
	var skills []string
	for _, s := range skillsRaw.([]any) {
		skills = append(skills, s.(string))
	}
	require.Equal(t, []string{"research", "Summarize"}, skills)

	// The connector ID travels; no connector definition fields do.
	tools, ok := stored["tools"].(map[string]any)
	require.True(t, ok, "the decoded record still carries the connector assignment under tools")
	mcp, ok := tools["mcp"].(map[string]any)
	require.True(t, ok)
	servers, ok := mcp["servers"].([]any)
	require.True(t, ok)
	require.Len(t, servers, 1, "the github assignment is still listed")
	binding, ok := servers[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "github", binding["id"], "the connector ID travels as a name")
	for _, defField := range []string{"command", "url", "env", "headers"} {
		require.NotContains(t, binding, defField,
			"the decoded record carries no connector definition field %q (the review's wording must stay true)", defField)
	}

	// The review rows that speak about skills and the connector must exist —
	// the wording the bundle was compared against.
	var hasSkill, hasConnector bool
	for _, row := range plan.LeftOut {
		if row.Type == "skill" {
			hasSkill = true
		}
		if row.Type == "connector" && row.Reason == "not_restored" {
			hasConnector = true
		}
	}
	require.True(t, hasSkill, "the review lists the skill row")
	require.True(t, hasConnector, "the review lists the connector row with reason not_restored")

	// Import-side coverage note (CHECK W02, spec row 3a's "matches the real
	// bundle" promise covers the export side; the import side reading this
	// same record shape is pinned where the import lives, and is NOT
	// duplicated here): element validation of the uploaded agent.json —
	// element_agent_test.go::TestAgentElement_S1aRefusals and the happy
	// mapping path field_mapping_import_test.go::TestFieldMapping_Import.
}

// b1D6Canaries are the Dataset D6 values that must never appear in any review
// answer (D6-1…9). D6-10/11 are the free-text positive controls and are
// deliberately not in this list.
var b1D6Canaries = []string{
	"CANARY_CRED_VAULT",            // D6-1 credential store value
	"CANARY_MASTER_KEY",            // D6-2 master key
	"CANARY_CLI_TOKEN",             // D6-3 CLI token
	"CANARY_AUTH_JSON",             // D6-4 auth.json content
	"CANARY_PROVIDER_KEY",          // D6-5 provider key
	"CANARY_CHANNEL_TOKEN",         // D6-6 channel token
	"CANARY_CLI_PATH",              // D6-7 executor program path value
	"--token=CANARY_ARG --verbose", // D6-8 executor extra arguments (a string)
	"CANARY_ENV",                   // D6-9 environment override value
}

// b1ScanBody fails when any D6-1…9 canary appears in a serialised review body.
func b1ScanBody(t *testing.T, where string, body []byte) {
	t.Helper()
	for _, c := range b1D6Canaries {
		require.NotContains(t, string(body), c, "%s must not carry the D6 canary %s", where, c)
	}
}

// TestPlanExport_NoValuesInPlan — spec order 4 (FR-004, FR-005; Dataset D6):
// no removed value appears anywhere in the review, only the allowed names; and
// the control proves the scanner would see a planted value.
func TestPlanExport_NoValuesInPlan(t *testing.T) {
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	src := newFakeSource()
	rec := d1WorkerRecord(testSourceID, "Coder", map[string]any{
		"description": "codes with CANARY_CLI_PATH bits",
		"subagents": map[string]any{"executor": map[string]any{
			"kind":          "external-cli",
			"cli":           "codex",
			"cli_path":      "/opt/x/CANARY_CLI_PATH/claude",
			"cli_args":      "--token=CANARY_ARG --verbose",
			"env_overrides": map[string]string{"API_TOKEN": "CANARY_ENV", "REGION": "eu"},
		}},
	})
	home := src.withAgent(t, testSourceID, rec, nil, []byte(testAgentBody))
	// Plant the file-shaped canaries at their real store locations (D6-1…4,
	// with D6-5/6 riding inside credentials.json as the store's entries) so a
	// leak would have a source. Each seed is asserted present BEFORE the
	// review runs — an unseeded source proves nothing (CHECK F08).
	d6Seeds := []struct {
		name    string
		canary  string
		content []byte
	}{
		{"credentials.json", "CANARY_CRED_VAULT", []byte(`{"entries":{"inst":{"value":"CANARY_CRED_VAULT"}},` +
			`"providers":{"p1":{"api_key":"CANARY_PROVIDER_KEY"}},` +
			`"channels":{"c1":{"token":"CANARY_CHANNEL_TOKEN"}}}`)},
		{"master.key", "CANARY_MASTER_KEY", []byte("CANARY_MASTER_KEY")},
		{"cli.token", "CANARY_CLI_TOKEN", []byte("CANARY_CLI_TOKEN")},
		{"auth.json", "CANARY_AUTH_JSON", []byte(`"CANARY_AUTH_JSON"`)},
	}
	for _, seed := range d6Seeds {
		require.NoError(t, os.WriteFile(filepath.Join(home, seed.name), seed.content, 0o600))
	}
	for _, seed := range d6Seeds {
		seeded, err := os.ReadFile(filepath.Join(home, seed.name))
		require.NoError(t, err, "the D6 seed %s must exist before the review runs", seed.name)
		require.Contains(t, string(seeded), seed.canary,
			"the seed %s must carry its canary before the review runs", seed.name)
	}

	plan := planAt(t, src, testSourceID, now)
	marshalled, err := json.Marshal(plan)
	require.NoError(t, err)
	b1ScanBody(t, "the review body", marshalled)

	// Names are not values: the removed environment variable names must be
	// present while their values are not.
	require.Contains(t, string(marshalled), "API_TOKEN")
	require.Contains(t, string(marshalled), "REGION")
	require.NotContains(t, string(marshalled), "CANARY_ENV")

	// Control (spec: a planted value makes this scanner fail): build a
	// spec-shaped plan in the test, plant D6-9's value, and assert the same
	// scanner flags it. This pins the instrument, not the implementation.
	var planted bytes.Buffer
	plantedPlan := *plan
	plantedPlan.Elements = append([]omnpkg.ExportPlanElement{}, plan.Elements...)
	plantedPlan.Elements[0].Name = "Coder CANARY_ENV"
	rawPlanted, err := json.Marshal(&plantedPlan)
	require.NoError(t, err)
	planted.WriteString("plan review body: ")
	planted.Write(rawPlanted)
	require.True(t, strings.Contains(planted.String(), "CANARY_ENV"),
		"the control body must actually carry the planted canary")
	for _, c := range b1D6Canaries {
		if c == "CANARY_ENV" {
			require.Contains(t, planted.String(), c,
				"the scanner's target canary is planted in the control body")
			continue
		}
		require.NotContains(t, planted.String(), c)
	}
}
