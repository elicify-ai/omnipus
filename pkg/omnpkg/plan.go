package omnpkg

// plan.go — the export review ("plan", epic #1094 slice B1; spec
// docs/internal/specs/export-import-b1-spec.md "PackageExportPlan fields").
//
// RED STUB (issue #1967 B1, test author scaffold): this file declares the plan
// types the B1 tests bind to and a behaviour-free BuildExportPlan. GREEN
// (backend-lead) replaces the stub body with the real capture/clean/refuse
// flow reusing collectAgentFiles, buildPackage and cleanRecordForExport.
// Nothing here implements the feature: every call returns errPlanNotStubbed,
// so a RED test fails on its own assertion naming the missing feature.

import (
	"errors"
	"time"
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
// table). It holds only what its response shows: no bundle bytes.
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
// GREEN applies it to the built plan right before returning it.
var PlanCanarySeam func(plan *ExportPlan)

// errPlanNotStubbed is what the RED stub returns. GREEN removes it together
// with the stub body.
var errPlanNotStubbed = errors.New("omnpkg: export plan review not implemented (RED stub, issue #1967 slice B1)")

// BuildExportPlan runs the same capture, cleaning and refusals as Export for
// the selected agent and returns the review — without building or keeping any
// bundle bytes and without writing anything (spec US-6, FR-006). The clock
// argument fixes created_at (tests pass a fake clock; callers pass UTC).
//
// RED STUB: returns errPlanNotStubbed and no plan.
func BuildExportPlan(src ExportSource, sel Selection, now time.Time) (*ExportPlan, error) {
	_ = src
	_ = sel
	_ = now
	return nil, errPlanNotStubbed
}
