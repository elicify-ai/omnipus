// rest_skills_changes.go: the skill mutations a person makes over REST
// (skills spec r15 §5.8) — install publication, delete and copy — all
// through skills.ApplyChange with a person actor, so locking, history,
// audit and freshness are identical to the agent path. Refusals answer with
// their §5.10 code; storage failures answer with the reused
// ConfigurationMutationFailureState (C-9).

package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// readListedSkillLocalChanges is GET /skills' read-time has_local_changes
// (§9.2): an observation under the mutation lock that never writes history.
var readListedSkillLocalChanges = func(root, id string) (bool, error) {
	obs, err := skills.ObserveSkill(root, id, skills.VersionQuery{})
	if err != nil {
		return false, err
	}
	return obs.HasLocalChanges, nil
}

// personActor is the manifest/audit actor of a REST change.
func personActor(tool string) skills.ChangeActor {
	return skills.ChangeActor{Kind: "person", Tool: tool}
}

// publishInstall publishes a staged install (§5.6): onto an absent name it is
// an install expecting absence; onto an installed name (any case — the
// installed folder's name is kept) it is a replacement that requires the
// caller's revision and, when the skill has local changes, the explicit
// confirmation. It returns the on-disk slug the change published to.
func publishInstall(root, slug, stageDir, expected string, replaceLocal bool) (string, skills.PublishOutcome, error) {
	onDisk, present, err := skills.ResolveInstalledSlug(root, slug)
	if err != nil {
		return slug, skills.PublishOutcome{}, err
	}
	req := skills.ChangeRequest{
		Kind: skills.KindInstall, Slug: slug, StagedDir: stageDir,
		Expect: skills.Expectation{Absent: true}, ReplaceLocalChanges: replaceLocal,
		Actor: personActor("rest-install"),
	}
	switch {
	case present && expected == "":
		return onDisk, skills.PublishOutcome{}, &skills.ChangeError{Code: skills.ErrInvalidInput,
			Text: fmt.Sprintf("'%s' is already installed; revision is required to replace it (read GET /api/v1/skills for the current revision)", onDisk)}
	case present:
		req.Kind, req.Slug, req.Expect = skills.KindInstallReplace, onDisk, skills.Expectation{Revision: expected}
	case expected != "":
		// A revision for a name that is not installed: the pipeline refuses
		// the stale expectation (SKILL_CHANGED).
		req.Kind, req.Expect = skills.KindInstallReplace, skills.Expectation{Revision: expected}
	}
	out, err := skills.ApplyChange(root, req)
	return req.Slug, out, err
}

// deleteSkill handles DELETE /api/v1/skills/{name}?revision= (§5.8): the
// query revision is the expected revision; no session seen record is
// involved. Built-ins are refused 403; grant removal is best effort (Q-S3).
func (a *restAPI) deleteSkill(w http.ResponseWriter, r *http.Request, name string) {
	if err := validateEntityID(name); err != nil || !skills.ValidSlug(name) {
		jsonErr(w, http.StatusBadRequest, "invalid skill name")
		return
	}
	if skills.IsBuiltin(name) {
		ce := skills.BuiltinReadOnlyError(name)
		jsonErrCode(w, http.StatusForbidden, ce.Text, ce.Code.Error())
		return
	}
	revision := strings.TrimSpace(r.URL.Query().Get("revision"))
	if revision == "" {
		jsonErr(w, http.StatusBadRequest, "revision is required")
		return
	}
	root := filepath.Join(a.homePath, "skills")
	if _, present, err := skills.ResolveInstalledSlug(root, name); err != nil {
		jsonServerFailure(w, http.StatusInternalServerError, "could not read installed skill state", err)
		return
	} else if !present {
		jsonErrCode(w, http.StatusNotFound, fmt.Sprintf("skill %q is not installed", name), "NOT_FOUND")
		return
	}
	out, err := skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindDelete, Slug: name, Expect: skills.Expectation{Revision: revision},
		Actor:  personActor("rest-delete"),
		Grants: newSkillGrantEditor(agentstore.New(a.homePath), nil),
	})
	if err != nil {
		writeSkillChangeFailure(w, name, out, err)
		return
	}
	body := map[string]any{
		"persistence_status": out.PersistenceStatus,
		"activation_status":  out.ActivationStatus,
		"revision":           revision,
		"changed_fields":     nonNilFields(out.ChangedFields),
	}
	if msg := strings.TrimSpace(strings.Join(nonEmpty(out.Message, out.Warning), " ")); msg != "" {
		body["message"] = msg
	}
	jsonOK(w, body)
}

// HandleSkillCopy handles POST /api/v1/skills/{name}/copy (§5.8, C-3/C-4):
// a copy expecting the absence of new_id under the skill identity; 201 with
// the new skill.
func (a *restAPI) HandleSkillCopy(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(strings.TrimSuffix(r.URL.Path, "/"), "/api/v1/skills/")
	source := strings.TrimSuffix(sub, "/copy")
	if !skills.ValidSlug(source) {
		jsonErr(w, http.StatusBadRequest, "invalid skill name")
		return
	}
	var req gen.SkillCopyRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SkillCopyRequest", &req, validateEnabled) {
		return
	}
	to := strings.TrimSpace(req.NewId)
	if !skills.ValidSlug(to) {
		jsonErrCode(w, http.StatusBadRequest, fmt.Sprintf("'%s' is not a valid skill name: use letters, digits and single hyphens (at most 64 characters)", req.NewId), skills.ErrInvalidInput.Error())
		return
	}
	root := filepath.Join(a.homePath, "skills")
	out, err := skills.ApplyChange(root, skills.ChangeRequest{
		Kind: skills.KindCreate, Slug: source, To: to, Expect: skills.Expectation{Absent: true},
		Actor: personActor("rest-copy"),
	})
	if err != nil {
		writeSkillChangeFailure(w, to, out, err)
		return
	}
	skill := gen.Skill{Id: to, Name: to, Version: "0.0.0", Status: gen.SkillStatusActive, Revision: out.Revision, HasLocalChanges: true}
	persistence := gen.SkillPersistenceStatus(out.PersistenceStatus)
	activation := gen.SkillActivationStatus(out.ActivationStatus)
	changed := nonNilFields(out.ChangedFields)
	skill.PersistenceStatus, skill.ActivationStatus, skill.ChangedFields = &persistence, &activation, &changed
	if msg := strings.TrimSpace(strings.Join(nonEmpty(out.Message, out.Warning), " ")); msg != "" {
		skill.Message = &msg
	}
	if src, ok := skillSourceToWire("global"); ok {
		skill.Source = &src
	}
	writeJSON(w, http.StatusCreated, skill)
}

// writeSkillChangeFailure renders a refused or failed change: a refusal with
// its §5.10 code and text, a storage failure or partial save with the
// failure envelope (C-9) carrying the pipeline's own client text.
func writeSkillChangeFailure(w http.ResponseWriter, slug string, out skills.PublishOutcome, err error) {
	var ce *skills.ChangeError
	if errors.As(err, &ce) {
		if status, refusal := skillRefusalStatus(ce.Code); refusal {
			jsonErrCode(w, status, ce.Text, ce.Code.Error())
			return
		}
	} else if errors.Is(err, skills.ErrNotFound) {
		jsonErrCode(w, http.StatusNotFound, fmt.Sprintf("skill %q not found", slug), "NOT_FOUND")
		return
	}
	logsafeError("rest: skill change failed", "skill", slug, "error", err)
	if !out.PersistenceStatus.Valid() || !out.ActivationStatus.Valid() || out.ErrorStage == "" {
		jsonServerFailure(w, http.StatusInternalServerError, "could not change skill", err)
		return
	}
	message := out.Message
	if message == "" {
		message = "the skill change failed; read the installed skill state before retrying"
	}
	payload := gen.ConfigurationMutationFailureState{
		PersistenceStatus: gen.ConfigurationMutationFailureStatePersistenceStatus(out.PersistenceStatus),
		ActivationStatus:  gen.ConfigurationMutationFailureStateActivationStatus(out.ActivationStatus),
		ChangedFields:     nonNilFields(out.ChangedFields),
		ErrorStage:        string(out.ErrorStage),
		Message:           message,
	}
	if out.Revision != "" {
		payload.Revision = &out.Revision
	}
	writeJSON(w, http.StatusInternalServerError, payload)
}

// skillRefusalStatus maps a §5.10 refusal code to its HTTP status. Storage
// failures and partial saves are not refusals (false).
func skillRefusalStatus(code error) (int, bool) {
	switch {
	case errors.Is(code, skills.ErrNotFound), errors.Is(code, skills.ErrUnknownVersion):
		return http.StatusNotFound, true
	case errors.Is(code, skills.ErrSkillChanged), errors.Is(code, skills.ErrSkillExists),
		errors.Is(code, skills.ErrBuiltinNameReserved), errors.Is(code, skills.ErrLocalChangesConfirmationRequired),
		errors.Is(code, skills.ErrSkillNotRead):
		return http.StatusConflict, true
	case errors.Is(code, skills.ErrBuiltinReadOnly):
		return http.StatusForbidden, true
	case errors.Is(code, skills.ErrSecretCheckUnavailable):
		return http.StatusServiceUnavailable, true
	case errors.Is(code, skills.ErrStorage), errors.Is(code, skills.ErrSkillPartiallySaved):
		return 0, false
	default:
		// INVALID_INPUT, SAVE_CHECK_FAILED, SECRET_IN_SKILL, SKILL_MD_REQUIRED,
		// SKILL_INSTRUCTION_*: the request or the content cannot be accepted.
		return http.StatusBadRequest, true
	}
}

func nonNilFields(fields []string) []string {
	if fields == nil {
		return []string{}
	}
	return fields
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}
