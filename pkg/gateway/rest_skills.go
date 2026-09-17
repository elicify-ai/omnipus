// rest_skills.go: Installed skills and the skill marketplace

package gateway

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/skills"
)

// --- Skills ---

var readListedSkillRevision = func(root, id string) (string, error) {
	return skills.NewSkillWriter(root).SkillRevision(id)
}

var publishRESTSkill = skills.PublishStagedSkill

// HandleSkills handles GET /api/v1/skills and POST sub-paths (search, install).
func (a *restAPI) HandleSkills(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	sub := strings.TrimPrefix(path, "/api/v1/skills")
	sub = strings.TrimPrefix(sub, "/")

	switch {
	case r.Method == http.MethodGet && sub == "":
		a.listSkills(w)
	case r.Method == http.MethodGet && sub == "marketplace":
		a.skillMarketplaceStatus(w)
	case r.Method == http.MethodGet && sub == "search":
		a.searchSkills(w, r)
	case r.Method == http.MethodPost && sub == "install":
		a.installSkill(w, r)
	case r.Method == http.MethodDelete && sub != "":
		a.deleteSkill(w, r, sub)
	default:
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *restAPI) listSkills(w http.ResponseWriter) {
	// Per-skill metadata (name, source, description, author, version) sourced from
	// the default agent's skills loader — the single source of truth for installed
	// skills (same loader that feeds GetStartupInfo's aggregate summary).
	detailed := a.agentLoop.ListSkillsDetailed()
	if len(detailed) == 0 {
		jsonOK(w, []gen.Skill{})
		return
	}
	result := make([]gen.Skill, 0, len(detailed))
	for _, s := range detailed {
		// The skill's stable identifier is its slug (ID = directory name). The
		// display Name is the human-readable label (e.g. "Daily Briefing"). They
		// are kept separate so renaming the display label never changes the ID
		// used by DELETE, activation, or built-in detection.
		id := s.ID
		if id == "" {
			id = s.Name // defensive: older loaders may not populate ID
		}
		name := s.Name
		if name == "" {
			name = id
		}
		// A skill is "built-in" (system) when it ships embedded in the binary —
		// identified by its slug (ID), not the display name. DefaultSkillNames()
		// returns slugs. The embedded defaults are seeded into the global skills
		// dir on first boot, so the loader reports their source as "global"; we
		// override that here so they surface as system skills.
		isBuiltin := s.Source == "builtin" || isSystemSkill(id)

		skill := gen.Skill{
			Id:       id,
			Name:     name,
			Status:   gen.SkillStatusActive,
			Verified: isBuiltin, // built-in skills are Omnipus-team-verified.
		}
		revision, revisionErr := readListedSkillRevision(filepath.Dir(filepath.Dir(s.Path)), id)
		if revisionErr != nil {
			slog.Warn("rest: compute skill revision", "skill", id, "error", revisionErr)
			jsonErr(w, http.StatusInternalServerError, "could not read installed skill state")
			return
		}
		skill.Revision = revision

		// Version: SKILL.md frontmatter when present, else a neutral default.
		if s.Version != "" {
			skill.Version = s.Version
		} else {
			skill.Version = "0.0.0"
		}

		// Description (optional wire field): omit when empty.
		if s.Description != "" {
			desc := s.Description
			skill.Description = &desc
		}

		// Author (optional): built-ins are authored by Omnipus; otherwise use the
		// frontmatter author if present, omitting the field when absent.
		switch {
		case isBuiltin:
			author := "Omnipus"
			skill.Author = &author
		case s.Author != "":
			author := s.Author
			skill.Author = &author
		}

		// Source (optional): map the loader source onto the wire enum. Embedded
		// defaults report "builtin" regardless of the dir they were seeded into.
		effectiveSource := s.Source
		if isBuiltin {
			effectiveSource = "builtin"
		}
		if src, ok := skillSourceToWire(effectiveSource); ok {
			skill.Source = &src
		}

		// ArgumentHint (optional): surface the SKILL.md frontmatter argument-hint
		// in the wire response so the composer palette and ghost-text can use it
		// (FR-006/FR-014/R3). Omit when the skill declares no hint.
		if s.ArgumentHint != "" {
			hint := s.ArgumentHint
			skill.ArgumentHint = &hint
		}

		// LastInvoked (optional, ADR-072 D3.1): the most recent time this skill
		// was requested by name through the Skill tool's load path, sourced from
		// the real audit trail (pkg/audit.Logger.LastInvokedForSkill — both
		// "loaded" and "denied" load outcomes count, matching that function's
		// own documented contract). a.auditor is nil only in unit-test fixtures
		// that construct restAPI without an audit logger; found is false when
		// the skill has never been invoked by name or no audit history exists,
		// in which case the field is correctly left unset rather than guessed.
		if a.auditor != nil {
			if invokedAt, found := a.auditor.LastInvokedForSkill(id); found {
				ts := invokedAt
				skill.LastInvoked = &ts
			}
		}

		result = append(result, skill)
	}
	jsonOK(w, result)
}

// skillSourceToWire maps a skills-loader source string onto the generated
// SkillSource wire enum. Returns ok=false for unrecognized values so the caller
// can leave the optional field unset rather than emit an invalid enum.
func skillSourceToWire(source string) (gen.SkillSource, bool) {
	switch source {
	case "builtin":
		return gen.SkillSourceBuiltin, true
	case "global":
		return gen.SkillSourceGlobal, true
	case "workspace":
		return gen.SkillSourceWorkspace, true
	default:
		return "", false
	}
}

// skillSource returns the loader source ("builtin"/"global"/"workspace") for the
// skill addressed by id (the slug = directory name), or "" when the skill is not
// found. Used to enforce the built-in deletion guard (built-ins cannot be
// removed). The DELETE route param is the skill's Id (slug), so matching is keyed
// on the slug — never the human-readable display name.
func (a *restAPI) skillSource(id string) string {
	// Embedded defaults are system skills regardless of which dir they were
	// seeded into (the loader reports them as "global"). DefaultSkillNames()
	// returns slugs, so this check is keyed on the slug.
	if isSystemSkill(id) {
		return "builtin"
	}
	for _, s := range a.agentLoop.ListSkillsDetailed() {
		skillID := s.ID
		if skillID == "" {
			skillID = s.Name
		}
		if skillID == id {
			return s.Source
		}
	}
	return ""
}

// systemSkillNames is the set of embedded default skill names (the "built-in"
// system skills shipped inside the binary). They are seeded into the global
// skills dir on first boot, so they must be identified by NAME — not by the
// loader's directory-derived source — to be surfaced as built-in and protected
// from deletion.
var systemSkillNames = func() map[string]struct{} {
	names := skills.DefaultSkillNames()
	m := make(map[string]struct{}, len(names))
	for _, n := range names {
		m[n] = struct{}{}
	}
	return m
}()

// isSystemSkill reports whether name is one of the embedded default skills.
func isSystemSkill(name string) bool {
	_, ok := systemSkillNames[name]
	return ok
}

// installedSkillIDs returns the set of skill IDs currently known to the agent
// loop (same source as GET /api/v1/skills). An empty map is returned when no
// skills are installed, which lets the validation below produce a proper 400
// ("unknown skill id") rather than silently accepting any string.
func (a *restAPI) installedSkillIDs() map[string]struct{} {
	// Read the skills directories directly. This used to go through
	// GetStartupInfo, which sourced them from the DEFAULT AGENT's context
	// builder — so when no default agent existed the set came back empty, and
	// validateSkillIDs is documented to skip validation entirely on an empty
	// set. Unknown skill ids were then accepted, through three layers of
	// indirection, with nothing logged. Skills are install-wide; no agent is
	// needed to enumerate them.
	workspace := a.homePath
	if cfg := a.agentLoop.GetConfig(); cfg != nil && strings.TrimSpace(cfg.Agents.Defaults.Home) != "" {
		workspace = cfg.Agents.Defaults.Home
	}
	names := agent.InstalledSkillIDs(workspace)
	result := make(map[string]struct{}, len(names))
	for _, n := range names {
		result[n] = struct{}{}
	}
	return result
}

// validateSkillIDs returns an error string (for a 400 response) if any of the
// supplied skill IDs are not present in the installed-skills registry.
// Returns "" when all IDs are valid or when no skills are installed at all
// (to avoid false rejections in environments where the skills directory hasn't
// been populated yet — the agent loop's runtime filter is the final gate).
func (a *restAPI) validateSkillIDs(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	installed := a.installedSkillIDs()
	// Skip validation when the installed set is empty: the skills directory may
	// not exist yet (fresh install, test environment). Accept any id and let the
	// agent loop's skill filter gate unknown ids at runtime.
	if len(installed) == 0 {
		return ""
	}
	for _, id := range ids {
		if _, ok := installed[id]; !ok {
			return fmt.Sprintf("unknown skill id: %q", id)
		}
	}
	return ""
}

// marketplaceEnabled reports whether at least one skill marketplace registry is
// enabled, read live from the current config. When false the search and
// slug-install endpoints refuse with 409 and the SPA hides its skill-browse UI.
// A marketplace is available when any entry in the unified Marketplaces list
// (FR-10.1) is enabled. ClawHub is enabled by default, so the default behavior
// is "on".
func (a *restAPI) marketplaceEnabled() bool {
	cfg := a.agentLoop.GetConfig()
	if cfg == nil {
		return false
	}
	for _, m := range cfg.Tools.Skills.Marketplaces {
		if m.Enabled {
			return true
		}
	}
	return false
}

// skillMarketplaceStatus handles GET /api/v1/skills/marketplace. It reports
// whether any skill marketplace registry is enabled so the SPA can gate its
// skill-browse UI (search / install-by-slug) on marketplace availability.
func (a *restAPI) skillMarketplaceStatus(w http.ResponseWriter) {
	cfg := a.agentLoop.GetConfig()

	status := gen.SkillMarketplaceStatus{}
	if cfg != nil {
		for _, m := range cfg.Tools.Skills.Marketplaces {
			name := m.Name
			if name == "" {
				name = m.Type
			}
			if m.Enabled {
				status.Enabled = true
			}
			status.Registries = append(status.Registries, struct {
				Enabled bool   `json:"enabled"`
				Name    string `json:"name"`
			}{Enabled: m.Enabled, Name: name})
		}
	}

	jsonOK(w, status)
}

// searchSkillsDefaultLimit / searchSkillsMaxLimit bound the number of marketplace
// results returned by GET /api/v1/skills/search.
const (
	searchSkillsDefaultLimit = 20
	searchSkillsMaxLimit     = 50
)

// searchSkills handles GET /api/v1/skills/search?q=<query>&limit=<n>. It queries
// the configured skill marketplace (ClawHub) and maps each registry hit onto the
// SkillSearchResult wire type. An empty query is a 400; a registry/transport
// failure is a 502 (not a 500) so the SPA can surface "registry unavailable".
func (a *restAPI) searchSkills(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		jsonErr(w, http.StatusBadRequest, "query parameter 'q' is required")
		return
	}

	limit := searchSkillsDefaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			jsonErr(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if limit > searchSkillsMaxLimit {
		limit = searchSkillsMaxLimit
	}

	if !a.marketplaceEnabled() {
		jsonErr(w, http.StatusConflict, "no skill marketplace is enabled")
		return
	}

	if a.skillRegistry == nil {
		slog.Warn("rest: skill search requested but no registry configured")
		jsonErr(w, http.StatusBadGateway, "skill registry unavailable")
		return
	}

	results, err := a.skillRegistry.Search(r.Context(), q, limit)
	if err != nil {
		slog.Warn("rest: skill search failed", "query", q, "error", err)
		jsonErr(w, http.StatusBadGateway, "skill registry unavailable")
		return
	}

	out := make([]gen.SkillSearchResult, 0, len(results))
	for _, res := range results {
		item := gen.SkillSearchResult{Slug: res.Slug}
		if res.DisplayName != "" {
			dn := res.DisplayName
			item.DisplayName = &dn
		}
		if res.Summary != "" {
			sm := res.Summary
			item.Summary = &sm
		}
		if res.Version != "" {
			v := res.Version
			item.Version = &v
		}
		if res.Score != 0 {
			score := res.Score
			item.Score = &score
		}
		if res.RegistryName != "" {
			rn := res.RegistryName
			item.RegistryName = &rn
		}
		if res.OwnerHandle != "" {
			oh := res.OwnerHandle
			item.OwnerHandle = &oh
		}
		out = append(out, item)
	}
	jsonOK(w, out)
}

// installSkill handles POST /api/v1/skills/install. It installs a skill from the
// ClawHub marketplace by its slug, optionally pinning a version. The slug is
// path-validated; the install is SSRF-safe (the registry's HTTP client honors
// the SSRF policy). On success it returns the freshly installed skill as it now
// appears in the local inventory.
func (a *restAPI) installSkill(w http.ResponseWriter, r *http.Request) {
	var req gen.SkillInstallRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "SkillInstallRequest", &req, validateEnabled) {
		return
	}
	slug := ""
	if req.Slug != nil {
		slug = strings.TrimSpace(*req.Slug)
	}
	uploadID := ""
	if req.UploadId != nil {
		uploadID = strings.TrimSpace(*req.UploadId)
	}
	if (slug == "") == (uploadID == "") {
		jsonErr(w, http.StatusBadRequest, "exactly one of slug or upload_id is required")
		return
	}
	// Path-traversal / identity guard: the slug becomes a directory name.
	if slug != "" && validateEntityID(slug) != nil {
		jsonErr(w, http.StatusBadRequest, "invalid skill slug")
		return
	}
	if uploadID != "" && req.Version != nil {
		jsonErr(w, http.StatusBadRequest, "version applies only to slug installs")
		return
	}

	if slug != "" && !a.marketplaceEnabled() {
		jsonErr(w, http.StatusConflict, "no skill marketplace is enabled")
		return
	}

	if slug != "" && a.skillRegistry == nil {
		slog.Warn("rest: skill install requested but no registry configured", "slug", slug)
		jsonErr(w, http.StatusBadGateway, "skill registry unavailable")
		return
	}

	version := ""
	if req.Version != nil {
		version = strings.TrimSpace(*req.Version)
	}

	skillsRoot := filepath.Join(a.homePath, "skills")
	stagingRoot := filepath.Join(skillsRoot, ".staging")
	var result *skills.InstallResult
	var stageDir string
	var err error
	if slug != "" {
		err = os.MkdirAll(stagingRoot, 0o755)
		if err == nil {
			stageDir, err = os.MkdirTemp(stagingRoot, slug+".install-")
		}
		if err == nil {
			result, err = a.skillRegistry.DownloadAndInstall(r.Context(), slug, version, stageDir)
		}
	} else {
		store := a.agentLoop.GetMediaStore()
		if store == nil {
			store = a.mediaStore
		}
		if store == nil {
			jsonErr(w, http.StatusServiceUnavailable, "upload store unavailable")
			return
		}
		var uploadPath string
		var meta media.MediaMeta
		uploadPath, meta, err = store.ResolveWithMeta(uploadID)
		if err == nil && !strings.HasPrefix(meta.Source, "upload:") {
			err = fmt.Errorf("upload_id does not identify an authorized upload")
		}
		if err == nil {
			root := filepath.Join(a.homePath, "uploads")
			rel, relErr := filepath.Rel(root, uploadPath)
			if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				err = fmt.Errorf("upload resolves outside the authorized upload store")
			}
		}
		if err == nil {
			slug, stageDir, err = skills.StageUploadedSkill(uploadPath, stagingRoot)
		}
	}
	if stageDir != "" {
		defer os.RemoveAll(stageDir)
	}
	if err != nil {
		slog.Warn("rest: skill install failed", "slug", slug, "version", version, "error", err)
		jsonErr(w, http.StatusBadGateway, fmt.Sprintf("could not stage skill: %v", err))
		return
	}

	// Surface a moderation block as a hard failure: do not leave a malware-flagged
	// skill installed.
	if result != nil && result.IsMalwareBlocked {
		jsonErr(w, http.StatusForbidden, fmt.Sprintf("skill %q is blocked by registry moderation", slug))
		return
	}
	expected := ""
	if req.Revision != nil {
		expected = strings.TrimSpace(*req.Revision)
	}
	publish, err := publishRESTSkill(skillsRoot, slug, stageDir, expected)
	if err != nil {
		if errors.Is(err, skills.ErrRevisionConflict) {
			jsonErr(w, http.StatusConflict, err.Error())
		} else {
			slog.Error("rest: publish skill", "skill", slug, "error", err)
			if publish.PersistenceStatus.Valid() && publish.ActivationStatus.Valid() {
				payload := gen.ConfigurationMutationFailureState{
					PersistenceStatus: gen.ConfigurationMutationFailureStatePersistenceStatus(publish.PersistenceStatus),
					ActivationStatus:  gen.ConfigurationMutationFailureStateActivationStatus(publish.ActivationStatus),
					ChangedFields:     publish.ChangedFields,
					ErrorStage:        string(publish.ErrorStage),
					Message:           publish.Message,
				}
				if publish.Revision != "" {
					payload.Revision = &publish.Revision
				}
				writeJSON(w, http.StatusInternalServerError, payload)
			} else {
				jsonErr(w, http.StatusInternalServerError, "could not publish skill; inspect installed skill state before retrying")
			}
		}
		return
	}

	installedVersion := "0.0.0"
	if result != nil && result.Version != "" && result.Version != "latest" {
		installedVersion = result.Version
	}

	skill := gen.Skill{
		Id:       slug,
		Name:     slug,
		Version:  installedVersion,
		Status:   gen.SkillStatusActive,
		Verified: result != nil && result.Verified,
		Revision: publish.Revision,
	}
	persistence := gen.SkillPersistenceStatus(publish.PersistenceStatus)
	activation := gen.SkillActivationStatus(publish.ActivationStatus)
	changed := publish.ChangedFields
	skill.PersistenceStatus = &persistence
	skill.ActivationStatus = &activation
	skill.ChangedFields = &changed
	if publish.Warning != "" {
		skill.Message = &publish.Warning
	}
	if result != nil && result.Summary != "" {
		summary := result.Summary
		skill.Description = &summary
	}
	if src, ok := skillSourceToWire("global"); ok {
		skill.Source = &src
	}
	jsonOK(w, skill)
}

func (a *restAPI) deleteSkill(w http.ResponseWriter, r *http.Request, name string) {
	if err := validateEntityID(name); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid skill name")
		return
	}
	// Built-in (pre-installed/system) skills ship inside the binary's skills dir
	// and must never be removable through the API — the frontend disables the
	// button, but the backend is the enforcing gate. Reject with 403.
	if a.skillSource(name) == "builtin" {
		jsonErr(w, http.StatusForbidden, "built-in skills cannot be removed")
		return
	}
	revision := strings.TrimSpace(r.URL.Query().Get("revision"))
	if revision == "" {
		jsonErr(w, http.StatusBadRequest, "revision is required")
		return
	}
	// a.homePath (OMNIPUS_HOME) is the correct root here: ADR-046 FR-009 made
	// install_skill target the fixed, install-wide GLOBAL skills directory
	// ($OMNIPUS_HOME/skills, see pkg/agent.globalSkillsDir), not a per-agent
	// workspace — so this installer's root must be a.homePath (which resolves
	// to that same $OMNIPUS_HOME/skills once "skills" is joined on below),
	// not any individual agent's Workspace. Routing this through a specific
	// agent's workspace (as an earlier version of this handler did, before
	// ADR-046) would point the installer at a directory install_skill no
	// longer writes into, so every delete would 404 on a skill that
	// demonstrably exists.
	//
	// Inject the SSRF checker (SEC-24) so that any outbound HTTP calls made by
	// the installer (e.g. future hash verification against a registry) are
	// protected. a.ssrfChecker is nil when SSRF is disabled; the constructor
	// accepts nil and falls back to a plain HTTP client in that case.
	installer, err := skills.NewSkillInstallerWithSSRF(a.homePath, "", "", a.ssrfChecker)
	if err != nil {
		slog.Error("rest: create skill installer for delete", "error", err)
		jsonErr(w, http.StatusInternalServerError, "could not initialize skill installer")
		return
	}
	if err := installer.UninstallReviewed(name, revision); err != nil {
		if errors.Is(err, skills.ErrRevisionConflict) {
			jsonErr(w, http.StatusConflict, err.Error())
			return
		}
		if strings.Contains(err.Error(), "not found") {
			jsonErr(w, http.StatusNotFound, fmt.Sprintf("skill %q not found", name))
			return
		}
		slog.Error("rest: delete skill", "name", name, "error", err)
		jsonErr(w, http.StatusInternalServerError, fmt.Sprintf("could not remove skill: %v", err))
		return
	}
	jsonOK(w, map[string]any{"status": "removed", "name": name, "revision": revision,
		"persistence_status": "complete", "activation_status": "active", "changed_fields": []string{"installed"}})
}
