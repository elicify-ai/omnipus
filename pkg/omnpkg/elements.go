package omnpkg

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// recordView is the part of a stored agent record the engine reads to decide
// type rules. JSON names are the stored names (pkg/config AgentConfig tags).
type recordView struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Type        config.AgentType `json:"type"`
	Subagents   *struct {
		Executor *struct {
			Kind config.ExecutorKind `json:"kind"`
		} `json:"executor"`
	} `json:"subagents"`
	ModelParams map[string]json.RawMessage `json:"model_params"`
}

// asAgentConfig carries the type-deciding fields into config.AgentConfig, so
// the type predicates are the config package's own.
func (v recordView) asAgentConfig() config.AgentConfig {
	ac := config.AgentConfig{Type: v.Type}
	if v.Subagents != nil && v.Subagents.Executor != nil {
		ac.Subagents = &config.SubagentsConfig{
			Executor: &config.ExecutorConfig{Kind: v.Subagents.Executor.Kind},
		}
	}
	return ac
}

// isCoreType reports the built-in record types that never travel (P-Q1 B).
func (v recordView) isCoreType() bool {
	return v.Type == config.AgentTypeCore || v.Type == config.AgentTypeSystem
}

// parseRecordView decodes the type-deciding view of a record.
func parseRecordView(raw []byte) (recordView, error) {
	var v recordView
	err := json.Unmarshal(raw, &v)
	return v, err
}

// allowedModelParams are the model_params keys the create rule accepts
// (Field mapping row model_params; architect sign-off C-9: the package record
// decoder refuses other keys).
var allowedModelParams = map[string]bool{"temperature": true, "max_tokens": true}

// ValidateElements checks every element's record for the upload-time element
// rules: record JSON well-formedness, forbidden types (core/system), the S1a
// unknown-element refusal, the S1a external-worker refusal and the strict
// model_params keys (FR-006, FR-011, FR-020; D1 rows 20, 22, 30, 31). The
// create-rule dry run itself is the shared create function's (T-3).
func ValidateElements(m *Manifest, payload map[string][]byte) error {
	for _, el := range m.Elements {
		if el.Type != ElementTypeAgent {
			return newErr(CodeElementTypeNotSupported, "element", el.SourceID, "type", el.Type)
		}
	}
	for _, el := range m.Elements {
		if err := validateAgentElement(el, payload); err != nil {
			return err
		}
	}
	return nil
}

func validateAgentElement(el ManifestElement, payload map[string][]byte) error {
	raw := payload[agentRecordPath(el.SourceID)]
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return newErr(CodeInvalidElement, "element", el.SourceID, "reason", "agent.json is not a JSON object")
	}
	v, err := parseRecordView(raw)
	if err != nil {
		return newErr(CodeInvalidElement, "element", el.SourceID, "reason", "agent.json has a field of the wrong type")
	}
	if v.isCoreType() {
		return newErr(CodeInvalidElement, "element", el.SourceID, "reason", "built-in agent types cannot be imported")
	}
	if v.asAgentConfig().IsExternalCLIWorker() {
		return newErr(CodeExternalWorkerNotSupported, "element", el.SourceID)
	}
	keys := make([]string, 0, len(v.ModelParams))
	for k := range v.ModelParams {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowedModelParams[k] {
			return fieldErr(CodeElementNotCreatable, "model_params", "unsupported key "+k)
		}
	}
	return nil
}

// blank reports whether s is empty after trimming surrounding whitespace.
func blank(s string) bool { return strings.TrimSpace(s) == "" }
