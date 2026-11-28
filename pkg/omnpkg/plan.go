package omnpkg

// plan.go — the export review ("plan", epic #1094 slice B1; spec
// docs/internal/specs/export-import-b1-spec.md "PackageExportPlan fields",
// "left_out rules for one agent", "disclosures").
//
// The review runs the same capture, cleaning and refusals as Export
// (collectAgentFiles, cleanRecordForExport, buildPackage), builds the bundle
// in memory only to measure it and drops the bytes. It writes nothing and
// never carries a removed value: names only.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// PlanTTL is how long a review stays readable: exactly 30 minutes from
// created_at (spec Scope Boundaries; held while now < expires_at).
const PlanTTL = 30 * time.Minute

// PlanDisclosureFreeText is the one disclosure B1 always carries (ADR D-E4
// layer 3): free text travels as written.
const (
	PlanDisclosureFreeText     = "free_text_copied"
	PlanDisclosureFreeTextMsg  = "Text you wrote (prompts and instructions) is copied exactly as it is. If you pasted a password or key into it, it goes with the bundle."
	PlanScopeAgents            = "agents"
	PlanElementAgent           = "agent"
	PlanKindMain               = "main"
	PlanKindWorker             = "worker"
	PlanKindExternalWorker     = "external_worker"
	LeftOutTypeAgentMemory     = "agent_memory"
	LeftOutTypeSession         = "session"
	LeftOutTypeWorkFiles       = "work_files"
	LeftOutTypeTeam            = "team"
	LeftOutTypeSkill           = "skill"
	LeftOutTypeConnector       = "connector"
	LeftOutTypeAgentSetting    = "agent_setting"
	LeftOutReasonTypeNotExport = "type_not_exportable"
	LeftOutReasonHostPath      = "host_path"
	LeftOutReasonSecretValue   = "secret_value"
	LeftOutReasonNotCarried    = "not_carried"
	LeftOutReasonNotRestored   = "not_restored"
)

// Left-out messages (spec "left_out rules for one agent"; exact text).
const (
	leftOutMsgAgentMemory = "This agent's memory is not included in this version."
	leftOutMsgSession     = "This agent's chats are not included."
	leftOutMsgWorkFiles   = "Workspace files are not included."
	leftOutMsgTeam        = "Team membership and delegation lines are not included. On import the agent joins the workspace you choose, with no delegation lines."
	leftOutMsgSkill       = "Skills are not included. The agent keeps their names; the other install uses a skill with the same name if it has one."
	leftOutMsgConnector   = "Connector names travel inside the agent's settings, but no connector settings or secrets are included, and the assignments are not restored on import. Assign connectors again after import."
	leftOutMsgHostPath    = "The program path is not included. The other install uses its own setting for this tool."
	leftOutMsgEnvValues   = "Environment override values are not included. Enter them again after import."
	leftOutMsgCLIArgs     = "Extra command-line arguments are not included. Enter them again after import."
)

// LeftOutItem is one "left out" row: what does not travel and why. Values are
// never carried — names only (spec left_out rules table).
type LeftOutItem struct {
	Type    string   `json:"type"`
	Reason  string   `json:"reason"`
	Message string   `json:"message"`
	Items   []string `json:"items,omitempty"`
}

// PlanDisclosure is one disclosure row.
type PlanDisclosure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ExportPlanElement is the one reviewed element (B1: exactly one agent).
type ExportPlanElement struct {
	Type     string          `json:"type"`
	SourceID string          `json:"source_id"`
	Name     string          `json:"name"`
	Kind     string          `json:"kind"`
	Files    []PlanFileEntry `json:"files"`
	Bytes    int64           `json:"bytes"`
}

// PlanFileEntry is one reviewed file with its size.
type PlanFileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ExportPlan is the review the plan routes store and return (spec field
// table). It holds only what its response shows: no bundle bytes. The
// gateway maps it onto the generated wire type gen.PackageExportPlan.
type ExportPlan struct {
	PlanID       string              `json:"plan_id"`
	CreatedAt    time.Time           `json:"created_at"`
	ExpiresAt    time.Time           `json:"expires_at"`
	Scope        string              `json:"scope"`
	Elements     []ExportPlanElement `json:"elements"`
	LeftOut      []LeftOutItem       `json:"left_out"`
	NeedsSecrets []NeedsSecret       `json:"needs_secrets"`
	Disclosures  []PlanDisclosure    `json:"disclosures"`
	SizeBytes    int64               `json:"size_bytes"`
	FileName     string              `json:"file_name"`
}

// PlanCanarySeam is nil in production. A test sets it to plant a value into a
// built plan, proving the canary scanner would see a leak (positive control).
// BuildExportPlan applies it to the built plan right before returning it.
var PlanCanarySeam func(plan *ExportPlan)

// BuildExportPlan runs the same capture, cleaning and refusals as Export for
// the selected agent and returns the review (spec US-6, FR-001…FR-004): the
// same refusal codes in the same order. The bundle is built in memory only to
// measure size_bytes and is then dropped, so the returned plan holds only
// what its response shows. Nothing is written. now fixes created_at (the
// gateway passes its package clock; tests pass a fake one).
func BuildExportPlan(src ExportSource, sel Selection, now time.Time) (*ExportPlan, error) {
	if len(sel.AgentIDs) != 1 {
		return nil, newErr(CodeExportSelectionInvalid, "reason", "exactly one agent must be selected")
	}
	id := sel.AgentIDs[0]
	if src.IsCoreAgent(id) {
		return nil, newErr(CodeCoreAgentNotExportable, "agent_id", id)
	}
	agent, err := collectAgentFiles(src, id)
	if err != nil {
		return nil, err
	}
	secrets := agent.needsSecrets(id)
	created := now.UTC()
	bundle, err := buildPackage(id, agent.name, agent.files, secrets, created.Truncate(time.Second))
	if err != nil {
		return nil, err
	}
	leftOut, err := planLeftOut(agent)
	if err != nil {
		return nil, fmt.Errorf("omnpkg: agent %s: %w", id, err)
	}
	planID, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("omnpkg: generate plan ID: %w", err)
	}
	el := ExportPlanElement{
		Type:     PlanElementAgent,
		SourceID: id,
		Name:     agent.name,
		Kind:     agent.kind,
		Files:    make([]PlanFileEntry, 0, len(agent.files)),
	}
	for _, f := range agent.files {
		size := int64(len(f.body))
		el.Files = append(el.Files, PlanFileEntry{Path: f.path, Size: size})
		el.Bytes += size
	}
	plan := &ExportPlan{
		PlanID:       planID.String(),
		CreatedAt:    created,
		ExpiresAt:    created.Add(PlanTTL),
		Scope:        PlanScopeAgents,
		Elements:     []ExportPlanElement{el},
		LeftOut:      leftOut,
		NeedsSecrets: secrets,
		Disclosures:  []PlanDisclosure{{Code: PlanDisclosureFreeText, Message: PlanDisclosureFreeTextMsg}},
		SizeBytes:    int64(len(bundle)),
		FileName:     DownloadFileName(agent.name),
	}
	if PlanCanarySeam != nil {
		PlanCanarySeam(plan)
	}
	return plan, nil
}

// planRecordView is the part of a stored record the left_out rules read:
// skill and connector names (which travel) and which executor runtime
// settings are set (which do not). Runtime values are decoded only to test
// for presence and never leave planLeftOut.
type planRecordView struct {
	Skills []string `json:"skills"`
	Tools  *struct {
		MCP *struct {
			Servers []struct {
				ID string `json:"id"`
			} `json:"servers"`
		} `json:"mcp"`
	} `json:"tools"`
	Subagents *struct {
		Executor *struct {
			CLIPath string          `json:"cli_path"`
			CLIArgs json.RawMessage `json:"cli_args"`
		} `json:"executor"`
	} `json:"subagents"`
}

// planLeftOut builds the left_out rows in the spec table's order. A row whose
// condition is false is absent; items carry names only.
func planLeftOut(agent *exportedAgent) ([]LeftOutItem, error) {
	var v planRecordView
	if err := json.Unmarshal(agent.stored, &v); err != nil {
		return nil, fmt.Errorf("stored record is not valid JSON: %w", err)
	}
	rows := []LeftOutItem{
		{Type: LeftOutTypeAgentMemory, Reason: LeftOutReasonTypeNotExport, Message: leftOutMsgAgentMemory},
		{Type: LeftOutTypeSession, Reason: LeftOutReasonTypeNotExport, Message: leftOutMsgSession},
		{Type: LeftOutTypeWorkFiles, Reason: LeftOutReasonTypeNotExport, Message: leftOutMsgWorkFiles},
		{Type: LeftOutTypeTeam, Reason: LeftOutReasonNotCarried, Message: leftOutMsgTeam},
	}
	if skills := nonBlank(v.Skills); len(skills) > 0 {
		rows = append(rows, LeftOutItem{Type: LeftOutTypeSkill, Reason: LeftOutReasonTypeNotExport,
			Message: leftOutMsgSkill, Items: skills})
	}
	var connectors []string
	if v.Tools != nil && v.Tools.MCP != nil {
		for _, s := range v.Tools.MCP.Servers {
			connectors = append(connectors, s.ID)
		}
	}
	if connectors = nonBlank(connectors); len(connectors) > 0 {
		rows = append(rows, LeftOutItem{Type: LeftOutTypeConnector, Reason: LeftOutReasonNotRestored,
			Message: leftOutMsgConnector, Items: connectors})
	}
	if agent.kind != PlanKindExternalWorker || v.Subagents == nil || v.Subagents.Executor == nil {
		return rows, nil
	}
	exec := v.Subagents.Executor
	if strings.TrimSpace(exec.CLIPath) != "" {
		rows = append(rows, LeftOutItem{Type: LeftOutTypeAgentSetting, Reason: LeftOutReasonHostPath,
			Message: leftOutMsgHostPath})
	}
	if len(agent.envNames) > 0 {
		rows = append(rows, LeftOutItem{Type: LeftOutTypeAgentSetting, Reason: LeftOutReasonSecretValue,
			Message: leftOutMsgEnvValues, Items: append([]string(nil), agent.envNames...)})
	}
	if cliArgsSet(exec.CLIArgs) {
		rows = append(rows, LeftOutItem{Type: LeftOutTypeAgentSetting, Reason: LeftOutReasonSecretValue,
			Message: leftOutMsgCLIArgs})
	}
	return rows, nil
}

// cliArgsSet reports a stored cli_args value that is present and not empty
// (a non-blank string, or any other non-null JSON value).
func cliArgsSet(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s) != ""
	}
	return true
}

// nonBlank returns names without blank entries, keeping their stored order.
func nonBlank(names []string) []string {
	var out []string
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			out = append(out, n)
		}
	}
	return out
}

// DownloadFileName is the safe download name of an agent's bundle (S1a
// FR-010; the export's Content-Disposition and the review's file_name share
// it): letters, digits, space, dot, dash and underscore are kept, anything
// else becomes an underscore; an empty result is "agent".
func DownloadFileName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	base := strings.Trim(b.String(), ". ")
	if base == "" {
		base = "agent"
	}
	return base + ".omnpkg"
}
