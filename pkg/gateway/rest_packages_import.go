// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// rest_packages_import.go: POST /api/v1/packages/import, the provisional
// preview (epic #1094 S1a T-5; spec revision 7 FR-006, FR-008, FR-020–FR-023,
// FR-033, FR-040, FR-044), plus the package-to-create-input mapping the
// upload dry run and the apply share (FR-034).

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// packageFileInStaging is the uploaded package's file name inside its staging
// folder. Apply re-reads (and so re-verifies) it.
const packageFileInStaging = "package.omnpkg"

// handlePackageImport is POST /api/v1/packages/import (FR-020, FR-021). The
// whole body is copied into the reserved staging folder first, then decoded
// and checked; nothing outside the staging folder and the audit log changes
// (FR-023).
func (a *restAPI) handlePackageImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	actor := a.packageActor(r)
	reg := a.packageRegistry()
	unfinished := reg.Sweep()
	stagingID, err := reg.Reserve()
	if err != nil {
		a.refuseUpload(w, actor, "", err)
		return
	}
	prev, err := a.stageUpload(r, stagingID, unfinished)
	if err != nil {
		if relErr := reg.Release(stagingID); relErr != nil {
			slog.Warn("packages: release refused upload's staging", "staging_id", stagingID, "error", relErr)
		}
		// The staging root itself goes when it is empty (a refused upload keeps
		// no staging folder). os.Remove refuses a non-empty folder.
		_ = os.Remove(a.packageStagingBase())
		a.refuseUpload(w, actor, stagingID, err)
		return
	}
	writeJSON(w, http.StatusCreated, prev)
}

// refuseUpload writes the refusal and its audit entry. An audit failure goes
// to the gateway log and never changes the refusal (FR-040).
func (a *restAPI) refuseUpload(w http.ResponseWriter, actor, stagingID string, err error) {
	var pe *omnpkg.PackageError
	if errors.As(err, &pe) {
		if auditErr := a.packageAudit("upload", actor, "", map[string]string{
			"outcome": "refused", "code": pe.Code, "staging_id": stagingID,
		}); auditErr != nil {
			slog.Error("packages: audit of an upload refusal failed", "code", pe.Code, "error", auditErr)
		}
	}
	writePackageErr(w, "upload", err)
}

// stageUpload copies the body into the staging folder, runs every format and
// element check and the create-rule dry run, marks the staging ready and
// builds the preview.
func (a *restAPI) stageUpload(r *http.Request, stagingID string, unfinished []omnpkg.UnfinishedImport) (*gen.PackageImportPreview, error) {
	reg := a.packageRegistry()
	dir, err := reg.Dir(stagingID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, packageFileInStaging)
	body := http.MaxBytesReader(nil, r.Body, packageUploadBodyLimit)
	if err = copyUploadToFile(body, path); err != nil {
		return nil, err
	}
	m, payload, err := readStagedPackage(path)
	if err != nil {
		return nil, err
	}
	el := m.Elements[0]
	plan, err := a.planPackageAgent(el, payload, a.liveAgentNames(), true)
	if err != nil {
		return nil, err
	}
	if err := reg.MarkReady(stagingID); err != nil {
		return nil, err
	}
	a.rememberPreview(stagingID, previewSnapshotOf(plan))
	return buildPackagePreview(m, stagingID, el, plan, unfinished)
}

// copyUploadToFile writes the whole body to path. A body over the route
// limit is 413 package_too_large (FR-008).
func copyUploadToFile(body io.Reader, path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create staged package: %w", err)
	}
	_, copyErr := io.Copy(f, body)
	closeErr := f.Close()
	var mbe *http.MaxBytesError
	if errors.As(copyErr, &mbe) {
		return packageErr(omnpkg.CodePackageTooLarge, "limit", "upload")
	}
	if copyErr != nil {
		return fmt.Errorf("read upload body: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close staged package: %w", closeErr)
	}
	return nil
}

// readStagedPackage runs every format check (omnpkg.ReadPackage) and the
// element checks (omnpkg.ValidateElements) on a staged package file.
func readStagedPackage(path string) (*omnpkg.Manifest, map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open staged package: %w", err)
	}
	defer f.Close()
	m, payload, err := omnpkg.ReadPackage(f)
	if err != nil {
		return nil, nil, err
	}
	if err := omnpkg.ValidateElements(m, payload); err != nil {
		return nil, nil, err
	}
	return m, payload, nil
}

// packageAgentPlan is one package agent mapped and built by the shared create
// rules, not yet persisted.
type packageAgentPlan struct {
	Name     string // the final (possibly suffixed) name
	Create   agentCreatePlan
	AgentMD  string
	Warnings []packageWarning
}

// planPackageAgent maps the element's record for this install (FR-033) and
// builds it with the shared create function (FR-034), naming it against
// existing (FR-032). It writes nothing. Every create-rule refusal is 422
// element_not_creatable naming the field. At preview, exhausted name suffixes
// are not a refusal (the error table places 409 name_suffix_exhausted at
// apply): the preview proposes the source name and apply decides.
func (a *restAPI) planPackageAgent(el omnpkg.ManifestElement, payload map[string][]byte, existing []string, preview bool) (*packageAgentPlan, error) {
	raw := payload[packageAgentPath(el.SourceID, omnpkg.AgentRecordName)]
	// Sign-off C-2: the REST raw-body effort rule on the record bytes, so a
	// JSON null or an over-long value is refused, not silently dropped.
	if msg, field := effortBodyRefusal(raw); msg != "" {
		return nil, notCreatable(field, msg)
	}
	mapped, err := omnpkg.MapAgentForImport(raw, el.SourceID, packageImportTarget{a: a})
	if err != nil {
		return nil, err
	}
	var rec config.AgentConfig
	if err = json.Unmarshal(mapped.Record, &rec); err != nil {
		return nil, packageErr(omnpkg.CodeInvalidElement, "element", el.SourceID, "reason", "agent.json has a field of the wrong type")
	}
	name, err := omnpkg.ResolveImportName(rec.Name, existing)
	if preview && omnpkg.CodeOf(err) == omnpkg.CodeNameSuffixExhausted {
		name, err = strings.TrimSpace(rec.Name), nil
	}
	if err != nil {
		return nil, err
	}
	soul := string(payload[packageAgentPath(el.SourceID, omnpkg.AgentSoulName)])
	if strings.TrimSpace(soul) == "" {
		return nil, &omnpkg.PackageError{Code: omnpkg.CodeElementNotCreatable, Field: "soul",
			Detail: map[string]string{"reason": "the prompt is empty"}}
	}
	in := packageCreateInput(&rec, name, soul)
	built, refusal := a.buildAgentCreate(in)
	if refusal != nil {
		return nil, notCreatable(refusal.Field, refusal.Msg)
	}
	if err := applyImportToolFields(&built, rec.Voice, rec.MemoryEnabled, rec.ContextWindowOverride); err != nil {
		field := ""
		var fe *agentmutation.FieldError
		if errors.As(err, &fe) && len(fe.Fields) > 0 {
			field = fe.Fields[0]
		}
		return nil, notCreatable(field, err.Error())
	}
	return &packageAgentPlan{
		Name:     name,
		Create:   built,
		AgentMD:  string(payload[packageAgentPath(el.SourceID, omnpkg.AgentInstructionsNm)]),
		Warnings: warningsFromEngine(mapped.Warnings),
	}, nil
}

func notCreatable(field, reason string) *omnpkg.PackageError {
	return &omnpkg.PackageError{Code: omnpkg.CodeElementNotCreatable, Field: field,
		Detail: map[string]string{"reason": reason}}
}

func packageAgentPath(sourceID, file string) string {
	return omnpkg.AgentsPrefix + sourceID + "/" + file
}

// packageCreateInput maps a mapped package record onto the shared create
// input (Field mapping, Import column). Voice, memory_enabled and
// context_window_override are applied afterwards (FR-047).
func packageCreateInput(rec *config.AgentConfig, name, soul string) agentCreateInput {
	in := agentCreateInput{WireType: string(gen.AgentTypeMain), Name: name, Soul: soul}
	if rec.Type == config.AgentTypeWorker {
		in.WireType = string(gen.AgentTypeSubagent)
	}
	desc := rec.Description
	in.Description = &desc
	if rec.Model != nil && rec.Model.Primary != "" {
		primary, provider := rec.Model.Primary, rec.Model.Provider
		in.Model, in.Provider = &primary, &provider
	}
	if rec.Color != "" {
		c := gen.AgentColor(rec.Color)
		in.Color = &c
	}
	if rec.Figure != "" {
		f := gen.AgentFigure(rec.Figure)
		in.Figure = &f
	}
	if rec.Role != "" {
		r := gen.AgentRole(rec.Role)
		in.Role = &r
	}
	if len(rec.Skills) > 0 {
		skills := append([]string(nil), rec.Skills...)
		in.Skills = &skills
	}
	if len(rec.FallbackModels) > 0 {
		fbs := make([]gen.FallbackModel, 0, len(rec.FallbackModels))
		for _, fb := range rec.FallbackModels {
			entry := gen.FallbackModel{Model: fb.Model}
			if fb.Provider != "" {
				p := fb.Provider
				entry.Provider = &p
			}
			if fb.ReasoningEffort != "" {
				e := fb.ReasoningEffort
				entry.ReasoningEffort = &e
			}
			fbs = append(fbs, entry)
		}
		in.FallbackModels = &fbs
	}
	if rec.ReasoningEffort != "" {
		e := rec.ReasoningEffort
		in.ReasoningEffort = &e
	}
	if rec.ModelParams != nil {
		in.ModelParams = &agentModelParamsInput{Temperature: rec.ModelParams.Temperature, MaxTokens: rec.ModelParams.MaxTokens}
	}
	if rec.Tools != nil && len(rec.Tools.Builtin.Policies) > 0 {
		policies := make(map[string]string, len(rec.Tools.Builtin.Policies))
		for k, v := range rec.Tools.Builtin.Policies {
			policies[k] = string(v)
		}
		in.ToolsCfg = &agentCreateToolsCfgInput{BuiltinPolicies: policies}
	}
	if rec.MaxToolIterations > 0 {
		n := rec.MaxToolIterations
		in.MaxToolIterations = &n
	}
	return in
}

// liveAgentNames lists the names on the live roster (the name-clash set, D6).
func (a *restAPI) liveAgentNames() []string {
	list := a.agentLoop.GetConfig().Agents.List
	names := make([]string, 0, len(list))
	for _, ac := range list {
		names = append(names, ac.Name)
	}
	return names
}

// buildPackagePreview fills the generated preview (FR-021). S1a lists no
// secrets and skips no element.
func buildPackagePreview(m *omnpkg.Manifest, stagingID string, el omnpkg.ManifestElement, plan *packageAgentPlan,
	unfinished []omnpkg.UnfinishedImport,
) (*gen.PackageImportPreview, error) {
	prev := &gen.PackageImportPreview{StagingId: stagingID, ExpiresAt: packageNow().Add(omnpkg.StagingReadyTTL).UTC()}
	rawManifest, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	if err := json.Unmarshal(rawManifest, &prev.Manifest); err != nil {
		return nil, fmt.Errorf("decode manifest into the preview: %w", err)
	}
	if prev.Manifest.NeedsSecrets == nil {
		prev.Manifest.NeedsSecrets = resized(prev.Manifest.NeedsSecrets, 0)
	}
	prev.NeedsSecrets = resized(prev.NeedsSecrets, 0)
	prev.SkippedElements = resized(prev.SkippedElements, 0)
	prev.WillAdd = resized(prev.WillAdd, 1)
	prev.WillAdd[0].Type = omnpkg.ElementTypeAgent
	prev.WillAdd[0].SourceId = el.SourceID
	prev.WillAdd[0].SourceName = el.Name
	prev.WillAdd[0].ProposedName = plan.Name
	prev.WillAdd[0].Renamed = plan.Name != strings.TrimSpace(el.Name)
	warnings := append([]packageWarning(nil), plan.Warnings...)
	for _, u := range unfinished {
		warnings = append(warnings, packageWarning{
			Code:    packageCodeUnfinishedImport,
			Message: "An earlier import did not finish; restart the gateway to finish its clean-up.",
			Items:   []string{u.AgentID, u.StagingID},
		})
	}
	prev.Warnings = resized(prev.Warnings, len(warnings))
	for i, wn := range warnings {
		prev.Warnings[i].Code, prev.Warnings[i].Message = wn.Code, wn.Message
		if wn.ElementSourceID != "" {
			id := wn.ElementSourceID
			prev.Warnings[i].ElementSourceId = &id
		}
		if len(wn.Items) > 0 {
			items := append([]string(nil), wn.Items...)
			prev.Warnings[i].Items = &items
		}
	}
	return prev, nil
}

// packageImportTarget is the engine's lookup edge into this install (D5).
type packageImportTarget struct{ a *restAPI }

func (t packageImportTarget) InstalledSkill(slug string) bool {
	_, ok := t.a.installedSkillIDs()[slug]
	return ok
}

func (t packageImportTarget) KnownTool(name string) bool {
	_, ok := buildKnownBuiltinToolNames()[name]
	return ok
}

func (t packageImportTarget) AlwaysAskTool(name string) bool { return tools.IsAlwaysAsk(name) }

func (t packageImportTarget) ProviderConfigured(name string) bool {
	return providerConfigured(t.a.agentLoop.GetConfig(), name)
}

func (t packageImportTarget) GlobalMaxToolIterations() int {
	return t.a.agentLoop.GetConfig().Agents.Defaults.EffectiveGlobalMaxToolIterations().Value
}

// SeedToolPolicies is the create seed for the record's stored type: the Main
// seed, restricted for a worker (the same seed buildToolConfig starts from).
func (t packageImportTarget) SeedToolPolicies(agentType string) map[string]string {
	seed := coreagent.NewCustomAgentToolsCfg()
	if config.AgentType(agentType) == config.AgentTypeWorker {
		coreagent.RestrictForWorkerTier(seed)
	}
	out := make(map[string]string, len(seed.Builtin.Policies))
	for k, v := range seed.Builtin.Policies {
		out[k] = string(v)
	}
	return out
}
