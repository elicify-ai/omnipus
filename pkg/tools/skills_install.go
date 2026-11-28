package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// InstallSkillTool allows the LLM agent to install skills from registries.
// It shares the same RegistryManager that FindSkillsTool uses,
// so all registries configured in config are available for installation.
type InstallSkillTool struct {
	BaseTool
	registryMgr *skills.RegistryManager
	// globalSkillsDir is the fixed, install-wide skills directory
	// ($OMNIPUS_HOME/skills) — NOT a per-agent/per-turn workspace path.
	// ADR-046 FR-009: install_skill targets this GLOBAL registry
	// unconditionally (every agent's own SkillsLoader searches it too, via
	// pkg/agent.globalSkillsDir — see pkg/skills/loader.go's SkillRoots),
	// so a skill installed by one agent is discoverable by every other
	// agent. This is NOT routed through ResolvePath: ResolvePath resolves a
	// caller-supplied path relative to the turn's effective WORKING
	// directory, whereas this is always the same fixed global directory
	// regardless of which agent or turn is calling.
	globalSkillsDir string
	// skillTrust reads the live trust setting for unverified skills
	// (SetSkillTrust); nil refuses every install (fail closed).
	skillTrust func() config.SkillTrustLevel
	mu         sync.Mutex
}

// publishInstalledSkill is a test-only override of the publication step,
// kept for the result-rendering tests that inject a fixed outcome. Nil in
// production: install_skill then publishes through skills.ApplyChange
// (skills spec r15 §5.6), the one change pipeline.
var publishInstalledSkill func(skillsDir, slug, stageDir, revision string) (skills.PublishOutcome, error)

// NewInstallSkillTool creates a new InstallSkillTool.
// registryMgr is the shared registry manager (same instance as FindSkillsTool).
// globalSkillsDir is the fixed, install-wide skills directory
// ($OMNIPUS_HOME/skills, see pkg/agent.globalSkillsDir); skills install to
// {globalSkillsDir}/{slug}/.
func NewInstallSkillTool(registryMgr *skills.RegistryManager, globalSkillsDir string) *InstallSkillTool {
	// Boot-time sweep: remove any staging leftovers from a previous install
	// that never completed (crash, OOM-kill, forced restart) so they don't
	// accumulate forever. This constructor runs once per agent at process
	// startup (registerSharedTools, pkg/agent/loop.go) and again only on a
	// full config/registry reload — never on the per-turn hot path — so the
	// extra ReadDir here (typically of an empty or nonexistent directory) is
	// negligible. Best-effort: a sweep failure must never block tool
	// construction or gateway boot.
	if globalSkillsDir != "" {
		sweepStaleStaging(globalSkillsDir)
	}
	return &InstallSkillTool{
		registryMgr:     registryMgr,
		globalSkillsDir: globalSkillsDir,
		mu:              sync.Mutex{},
	}
}

// SetSkillTrust gives the tool the live trust setting for unverified skills
// (Skills & Tools C-3). Under Deny an unverified skill is refused before
// publication and before any approval card, with the same text as the
// screen and REST install. Without a trust source the tool fails closed:
// every install is refused before anything is downloaded (team-lead ruling
// R2, 2026-11-28).
func (t *InstallSkillTool) SetSkillTrust(trust func() config.SkillTrustLevel) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.skillTrust = trust
}

// installTrustUnavailableText is the refusal when the tool has no trust
// setting to apply: it cannot tell whether an unverified skill is allowed, so
// it installs nothing (fail closed).
const installTrustUnavailableText = "install_skill cannot check the trust setting for unverified skills here, so it installs nothing. Nothing was installed."

// stagingDirName is the dedicated, non-scanned subdirectory of the global
// skills directory that install_skill stages downloads into. It lives one
// level below skillsDir (not skillsDir itself) for two reasons: (1)
// pkg/skills/loader.go's ListSkills scans skillsDir's direct children for a
// SKILL.md and, prior to this fix, had no dot-prefix filter — a staging
// directory created directly inside skillsDir (even dot-prefixed) was one
// missing check away from becoming a phantom entry in every agent's skill
// list; keeping all staging under one always-dot-prefixed, SKILL.md-less
// directory removes that surface entirely, and pkg/skills/loader.go now also
// skips any dot-prefixed entry defensively. (2) it stays on the SAME
// filesystem as skillsDir, so the final os.Rename into targetDir remains an
// atomic, cheap rename rather than a cross-filesystem copy.
const stagingDirName = ".staging"

// sweepStaleStaging removes any leftover contents of skillsDir/.staging left
// behind by an install_skill run that was interrupted before it could clean
// up after itself (process crash, OOM-kill, forced restart — the defer in
// Execute only runs on a normal return). It is best-effort: skillsDir may not
// exist yet on a fresh install, and any error here is logged, never
// propagated, since a failed sweep must not block tool construction or
// gateway boot. A future install_skill run will retry the sweep on its own
// next construction, and stale entries are otherwise harmless (never
// surfaced by ListSkills, per the dot-prefix skip in pkg/skills/loader.go).
func sweepStaleStaging(skillsDir string) {
	stagingRoot := filepath.Join(skillsDir, stagingDirName)
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			logger.WarnCF("tool", "install_skill: failed to read staging directory during startup sweep",
				map[string]any{
					"tool":  "install_skill",
					"dir":   stagingRoot,
					"error": err.Error(),
				})
		}
		return
	}
	for _, entry := range entries {
		stalePath := filepath.Join(stagingRoot, entry.Name())
		if err := os.RemoveAll(stalePath); err != nil {
			logger.WarnCF("tool", "install_skill: failed to remove stale staging entry during startup sweep",
				map[string]any{
					"tool":  "install_skill",
					"path":  stalePath,
					"error": err.Error(),
				})
			continue
		}
		logger.InfoCF("tool", "install_skill: removed stale staging entry left by an interrupted install",
			map[string]any{
				"tool": "install_skill",
				"path": stalePath,
			})
	}
}

func (t *InstallSkillTool) Name() string {
	return "install_skill"
}

func (t *InstallSkillTool) Description() string {
	return "Install a skill from a registry by slug into the global skills directory. Installation does not assign it to agents; execution still requires an applicable skill assignment. Registries of type github are " + githubNotSupportedText + ". Use find_skills for registry discovery and list_skills with scope=management to inspect installed packages and their revisions. " +
		"Replacing an installed package requires force=true and its exact reviewed revision. Replacement affects every agent assigned that shared package. The verified replacement is staged before publication; validation or download failures preserve the existing install. " +
		"Malicious packages are refused. Read persistence, activation and warning fields in the result; after publication or cleanup errors, inspect installed state before retrying. A revision conflict requires a fresh read and renewed confirmation for material changes."
}

// githubNotSupportedText is the stable wording of the github refusal; the tool
// description and the refusal both use it.
const githubNotSupportedText = "not supported by install_skill"

// isGitHubRegistry reports whether name selects a github registry: the
// well-known name "github" (any case), or any configured registry that is a
// GitHub registry under a custom marketplace name.
func (t *InstallSkillTool) isGitHubRegistry(name string) bool {
	if strings.EqualFold(strings.TrimSpace(name), "github") {
		return true
	}
	if t.registryMgr == nil {
		return false
	}
	marker, ok := t.registryMgr.GetRegistry(name).(interface{ IsGitHubRegistry() bool })
	return ok && marker.IsGitHubRegistry()
}

func (t *InstallSkillTool) Scope() ToolScope       { return ScopeGeneral }
func (t *InstallSkillTool) Category() ToolCategory { return CategorySkills }

func (t *InstallSkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"slug": map[string]any{
				"type":        "string",
				"description": "The unique slug of the skill to install (e.g., 'github', 'docker-compose')",
			},
			"version": map[string]any{
				"type":        "string",
				"description": "Specific version to install (optional, defaults to latest)",
			},
			"registry": map[string]any{
				"type":        "string",
				"description": "Registry to install from (required, e.g., 'clawhub')",
			},
			"force": map[string]any{
				"type":        "boolean",
				"description": "Marks an explicitly requested reinstall; it never replaces an existing skill without its current revision.",
			},
			"revision": map[string]any{
				"type":        "string",
				"description": "Required current revision when explicitly replacing an installed skill; omit only for a first install.",
			},
			"replace_local_changes": map[string]any{
				"type":        "boolean",
				"description": "Confirms replacing an installed skill that has local changes (edits made after it was installed). The local changes stay in the version history.",
			},
			"category": map[string]any{
				"type":        "string",
				"enum":        skills.SkillCategories(),
				"description": "Category the skill is listed under. Optional; defaults to the category in the skill file, else uncategorised.",
			},
			"ownerHandle": map[string]any{
				"type": "string",
				"description": "Disambiguates slug when it is published by more than one owner on the " +
					"registry (an ambiguous-match install error names the candidate owner handles to " +
					"retry with). Not needed for an unambiguous slug.",
			},
		},
		"required": []string{"slug", "registry"},
	}
}

// installRequest is one validated install_skill call.
type installRequest struct {
	slug, registryName, version, revision, ownerHandle, category string
	force, replaceLocal, alreadyInstalled                        bool
	onDisk, targetDir                                            string
	registry                                                     skills.SkillRegistry
}

// stagedInstall is a downloaded, unpublished skill in the staging area: the
// pin of an approval card (C-12 rows 6 and 8). The approved call publishes
// exactly this folder; a denied, timed-out or refused card discards it.
type stagedInstall struct {
	slug, registryName, dir, revision string
	result                            *skills.InstallResult
}

// validateInstall runs every check that needs no download. Caller holds t.mu.
func (t *InstallSkillTool) validateInstall(ctx context.Context, args map[string]any) (*installRequest, *ToolResult) {
	// A github registry is refused up front (#1574): it takes an owner/repo
	// reference, which the slug rule below can never accept, and its installer
	// bypasses the staging, scan and publish steps this tool relies on. Say so
	// before the slug check, whose "invalid slug" message hides the real reason.
	if name, _ := args["registry"].(string); t.isGitHubRegistry(name) {
		return nil, ErrorResult(fmt.Sprintf(
			"registry %q is a github registry: installing from github is %s; install from a clawhub registry instead",
			name, githubNotSupportedText,
		))
	}
	req := &installRequest{}
	req.slug, _ = args["slug"].(string)
	if err := utils.ValidateSkillIdentifier(req.slug); err != nil {
		return nil, ErrorResult(fmt.Sprintf("invalid slug %q: error: %s", req.slug, err.Error()))
	}
	req.registryName, _ = args["registry"].(string)
	if err := utils.ValidateSkillIdentifier(req.registryName); err != nil {
		return nil, ErrorResult(fmt.Sprintf("invalid registry %q: error: %s", req.registryName, err.Error()))
	}
	req.category, _ = args["category"].(string)
	if req.category != "" && !skills.IsSkillCategory(req.category) {
		return nil, ErrorResult(skills.UnknownCategoryError(req.category).Error() + ". Nothing was installed.")
	}
	req.version, _ = args["version"].(string)
	req.force, _ = args["force"].(bool)
	req.revision, _ = args["revision"].(string)
	if req.force && req.revision == "" {
		return nil, ErrorResult("revision is required when replacing an installed skill")
	}
	// ownerHandle disambiguates a slug published by more than one owner.
	req.ownerHandle, _ = args["ownerHandle"].(string)
	if req.ownerHandle != "" {
		if err := utils.ValidateSkillIdentifier(req.ownerHandle); err != nil {
			return nil, ErrorResult(fmt.Sprintf("invalid ownerHandle %q: error: %s", req.ownerHandle, err.Error()))
		}
	}
	// skillsDir is the fixed GLOBAL skills directory (ADR-046 FR-009), so an
	// install from one agent is discoverable by every other agent. A target
	// that matches an installed skill only by letter case is a replacement
	// of that skill (§5.6). A built-in name is reserved for every install:
	// refuse it before the "already installed" check, which a built-in
	// folder would trip.
	skillsDir := t.globalSkillsDir
	if refusal := skills.RefuseBuiltinTarget(skills.ChangeRequest{
		Kind: skills.KindInstall, Slug: req.slug,
		Actor: skills.ChangeActor{
			Kind: "agent", AgentID: ToolAgentID(ctx), SessionID: ToolTranscriptSessionID(ctx),
			WorkspaceID: ToolWorkspaceID(ctx), Tool: t.Name(),
		},
	}); refusal != nil {
		return nil, ErrorResult(refusal.Error())
	}
	onDisk, alreadyInstalled, err := skills.ResolveInstalledSlug(skillsDir, req.slug)
	if err != nil {
		return nil, ErrorResult(fmt.Sprintf("could not read the skills directory: %v", err))
	}
	req.onDisk, req.alreadyInstalled = onDisk, alreadyInstalled
	req.targetDir = filepath.Join(skillsDir, onDisk)
	if alreadyInstalled && req.revision == "" {
		return nil, ErrorResult(
			fmt.Sprintf("skill %q already installed at %s. Read it and provide its revision to replace it.", onDisk, req.targetDir),
		)
	}
	req.replaceLocal, _ = args["replace_local_changes"].(bool)
	// Resolve the registry BEFORE touching anything on disk: a typo'd name
	// must fail closed with an existing install left exactly as it was.
	req.registry = t.registryMgr.GetRegistry(req.registryName)
	if req.registry == nil {
		return nil, ErrorResult(fmt.Sprintf("registry %q not found", req.registryName))
	}
	return req, nil
}

// stage downloads the skill into a fresh folder under skillsDir/.staging (a
// non-scanned subdirectory on the same filesystem, so the publish rename
// stays atomic and a concurrent listing never sees a half-written skill).
// The existing install is never touched. On failure nothing is left behind.
func (t *InstallSkillTool) stage(ctx context.Context, req *installRequest) (*stagedInstall, *ToolResult) {
	if t.skillTrust == nil {
		return nil, ErrorResult(installTrustUnavailableText)
	}
	skillsDir := t.globalSkillsDir
	if mkErr := os.MkdirAll(skillsDir, 0o755); mkErr != nil {
		return nil, ErrorResult(fmt.Sprintf("failed to create skills directory: %v", mkErr))
	}
	stagingRoot := filepath.Join(skillsDir, stagingDirName)
	if mkErr := os.MkdirAll(stagingRoot, 0o755); mkErr != nil {
		return nil, ErrorResult(fmt.Sprintf("failed to create the staging directory for %q: %v", req.slug, mkErr))
	}
	stageDir, err := os.MkdirTemp(stagingRoot, req.slug+".install-")
	if err != nil {
		return nil, ErrorResult(fmt.Sprintf("failed to create a staging directory for %q: %v", req.slug, err))
	}
	fail := func(r *ToolResult) (*stagedInstall, *ToolResult) {
		if rmErr := os.RemoveAll(stageDir); rmErr != nil {
			logger.WarnCF("tool", "install_skill: staged copy not removed",
				map[string]any{"skill": req.slug, "dir": stageDir, "error": rmErr.Error()})
		}
		return nil, r
	}
	var result *skills.InstallResult
	if req.ownerHandle != "" {
		scoped, ok := req.registry.(skills.OwnerScopedRegistry)
		if !ok {
			return fail(ErrorResult(fmt.Sprintf(
				"registry %q does not support ownerHandle-scoped installs", req.registryName,
			)))
		}
		result, err = scoped.DownloadAndInstallForOwner(ctx, req.slug, req.ownerHandle, req.version, stageDir)
	} else {
		result, err = req.registry.DownloadAndInstall(ctx, req.slug, req.version, stageDir)
	}
	if err != nil {
		return fail(ErrorResult(fmt.Sprintf("failed to install %q: %v", req.slug, err)))
	}
	if result.IsMalwareBlocked {
		return fail(ErrorResult(fmt.Sprintf("skill %q is flagged as malicious and cannot be installed", req.slug)))
	}
	if !result.Verified && t.skillTrust != nil && t.skillTrust() == config.SkillTrustDeny {
		return fail(ErrorResult(skills.InstallTrustDenyText))
	}
	revision, err := skills.ContentRevision(stageDir)
	if err != nil {
		return fail(ErrorResult(fmt.Sprintf("could not read the downloaded skill %q: %v", req.slug, err)))
	}
	return &stagedInstall{slug: req.slug, registryName: req.registryName, dir: stageDir, revision: revision, result: result}, nil
}

// PrepareForApproval stages the skill before its approval card (C-12 rows 6
// and 8) and returns the facts the card shows: the skill, its staged
// revision and the verification result. The pin is the staged copy.
func (t *InstallSkillTool) PrepareForApproval(ctx context.Context, args map[string]any) (map[string]any, any, *ToolResult) {
	t.mu.Lock()
	defer t.mu.Unlock()
	req, refusal := t.validateInstall(ctx, args)
	if refusal != nil {
		return nil, nil, refusal
	}
	staged, refusal := t.stage(ctx, req)
	if refusal != nil {
		return nil, nil, refusal
	}
	verification := "no_registry_hash"
	if staged.result.Verified {
		verification = "matched"
	}
	facts := map[string]any{
		"skill":        req.slug,
		"revision":     staged.revision,
		"unverified":   !staged.result.Verified,
		"verification": verification,
		"version":      staged.result.Version,
	}
	return facts, staged, nil
}

// DiscardStaged removes the staged copy of a card that was denied, timed
// out or refused. A pin that is not an install_skill staging folder is
// refused.
func (t *InstallSkillTool) DiscardStaged(pin any) error {
	staged, ok := pin.(*stagedInstall)
	if !ok || staged == nil {
		return fmt.Errorf("install_skill: not a staged install: %T", pin)
	}
	stagingRoot := filepath.Join(t.globalSkillsDir, stagingDirName)
	if filepath.Dir(staged.dir) != filepath.Clean(stagingRoot) {
		return fmt.Errorf("install_skill: staged copy %q is outside the staging area", staged.dir)
	}
	if err := os.RemoveAll(staged.dir); err != nil {
		return fmt.Errorf("install_skill: remove staged copy: %w", err)
	}
	return nil
}

// DiscardApprovalPin is the loop hook for a denied, timed-out or refused
// card (tools.ApprovalPinDiscarder).
func (t *InstallSkillTool) DiscardApprovalPin(pin any) error { return t.DiscardStaged(pin) }

func (t *InstallSkillTool) Execute(ctx context.Context, args map[string]any) (res *ToolResult) {
	// FR-030: every result carries the outcome fields; refusals before the
	// publish step changed nothing.
	defer func() { res = FinalizeChangeResult(t.Name(), res) }()
	// Install lock to prevent concurrent directory operations (one global
	// skills directory).
	t.mu.Lock()
	defer t.mu.Unlock()

	req, refusal := t.validateInstall(ctx, args)
	if refusal != nil {
		return refusal
	}
	slug, registry, skillsDir, targetDir := req.slug, req.registry, t.globalSkillsDir, req.targetDir
	onDisk, alreadyInstalled, revision, replaceLocal, category := req.onDisk, req.alreadyInstalled, req.revision, req.replaceLocal, req.category

	// An approved card publishes exactly the folder it reviewed (S-118);
	// otherwise the skill is downloaded now.
	staged, _ := ApprovalPinFrom(ctx).(*stagedInstall)
	if staged != nil && (staged.slug != slug || staged.registryName != req.registryName) {
		return ErrorResult(fmt.Sprintf("the approved install was for %q; this call names %q. Nothing was installed.", staged.slug, slug))
	}
	if staged == nil {
		if staged, refusal = t.stage(ctx, req); refusal != nil {
			return refusal
		}
	}
	// If we return before the publish rename, the staged copy is removed;
	// after a successful publish it no longer exists and this is a no-op.
	defer os.RemoveAll(staged.dir)
	stageDir, result := staged.dir, staged.result

	// Everything above succeeded: only now is the change pipeline asked to
	// publish the verified package (§5.6). The precondition is the request's
	// own expectation — absence for a new install, the given revision for a
	// replacement — never a session's seen record.
	installCategory, err := skills.InstallCategory(category, stageDir)
	if err != nil {
		return ErrorResult(err.Error() + ". Nothing was installed.")
	}
	publish, err := t.publish(ctx, skillsDir, onDisk, stageDir, revision, alreadyInstalled, replaceLocal)
	if err != nil {
		return installFailure(slug, publish, err)
	}
	// C-5: category and verification live in Omnipus data, never in the
	// skill folder. A failed write is reported, not hidden: the skill is
	// installed but listed as uncategorised and unverified.
	installRec := skills.InstallRecord{Category: installCategory}
	if result.Verified {
		installRec.VerifiedRevision = publish.Revision
	}
	if recErr := skills.WriteInstallRecord(skillsDir, onDisk, installRec); recErr != nil {
		logger.ErrorCF("tools", "install_skill: skill installed but its category and verification were not saved",
			map[string]any{"skill": onDisk, "error": recErr.Error()})
		publish.Warning = strings.TrimSpace(publish.Warning + " The category and verification were not saved: " + recErr.Error())
	}

	// Build result with moderation warning if suspicious.
	var output string
	if result.IsSuspicious {
		output = fmt.Sprintf("⚠️ Warning: skill %q is flagged as suspicious (may contain risky patterns).\n\n", slug)
	}
	output += fmt.Sprintf("Successfully installed skill %q v%s from %s registry.\nLocation: %s\n",
		slug, result.Version, registry.Name(), targetDir)

	if result.Summary != "" {
		output += fmt.Sprintf("Description: %s\n", result.Summary)
	}
	output += "\nThe skill is now available and can be loaded in the current session."
	output += fmt.Sprintf("\nRevision: %s\nPersistence: %s\nActivation: %s", publish.Revision, publish.PersistenceStatus, publish.ActivationStatus)
	if publish.Warning != "" {
		output += "\nWarning: " + publish.Warning
	}

	const checkScope = "registry moderation check passed and the skill was published into the skills directory"
	verdict := ChangeWorking(checkScope, "an agent loading and running the skill")
	if publish.PersistenceStatus != skills.PersistenceComplete || publish.ActivationStatus != skills.ActivationActive {
		verdict = ChangePartlySaved(fmt.Sprintf("publication reported persistence %s, activation %s",
			publish.PersistenceStatus, publish.ActivationStatus), checkScope, []string{"skill activation"})
	}
	payload, marshalErr := json.Marshal(verdict.Into(map[string]any{
		"success":            true,
		"name":               slug,
		"revision":           publish.Revision,
		"persistence_status": publish.PersistenceStatus,
		"activation_status":  publish.ActivationStatus,
		"changed_fields":     publish.ChangedFields,
		"message":            output,
		"warning":            publish.Warning,
	}))
	if marshalErr != nil {
		return ErrorResult(fmt.Sprintf("installed skill but failed to encode mutation state: %v", marshalErr))
	}
	return SilentResult(string(payload))
}

// publish runs the publication step through skills.ApplyChange (or the
// test override).
func (t *InstallSkillTool) publish(ctx context.Context, skillsDir, slug, stageDir, revision string, replace, replaceLocal bool) (skills.PublishOutcome, error) {
	if publishInstalledSkill != nil {
		return publishInstalledSkill(skillsDir, slug, stageDir, revision)
	}
	req := skills.ChangeRequest{
		Kind: skills.KindInstall, Slug: slug, StagedDir: stageDir,
		Expect:              skills.Expectation{Absent: true},
		ReplaceLocalChanges: replaceLocal,
		Actor: skills.ChangeActor{
			Kind: "agent", AgentID: ToolAgentID(ctx), SessionID: ToolTranscriptSessionID(ctx),
			WorkspaceID: ToolWorkspaceID(ctx), Tool: t.Name(),
		},
	}
	if replace {
		req.Kind, req.Expect = skills.KindInstallReplace, skills.Expectation{Revision: revision}
	}
	return skills.ApplyChange(skillsDir, req)
}

// installFailure renders a refused or partly saved publication. A pipeline
// refusal carries its code and §5.10 text; a partial save reports the
// persisted state (FR-030) without the private cause.
func installFailure(slug string, publish skills.PublishOutcome, err error) *ToolResult {
	if publish.PersistenceStatus.Valid() && publish.ActivationStatus.Valid() && publish.PersistenceStatus != skills.PersistenceNone {
		payload := map[string]any{
			"persistence_status": publish.PersistenceStatus,
			"activation_status":  publish.ActivationStatus,
			"changed_fields":     publish.ChangedFields,
			"error_stage":        publish.ErrorStage,
			"message":            publish.Message,
		}
		if publish.Revision != "" {
			payload["revision"] = publish.Revision
		}
		var ce *skills.ChangeError
		if errors.As(err, &ce) {
			payload["message"] = ce.Error()
		}
		return ChangeErrorResult(payload, ChangePartlySaved(
			fmt.Sprintf("the skill was partly published (stage %s)", publish.ErrorStage), "",
			[]string{"skill publication"}))
	}
	var ce *skills.ChangeError
	if errors.As(err, &ce) {
		return ErrorResult(ce.Error())
	}
	logger.ErrorCF("tool", "Failed to publish downloaded skill", map[string]any{"tool": "install_skill", "skill": slug, "error": err.Error()})
	return ErrorResult(fmt.Sprintf("downloaded %q but could not publish it; read installed skill state before retrying", slug))
}
