// gateway_boot_roster.go: Load souls and seed the agent roster at boot, persisting what the seed wrote

package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	_ "github.com/elicify-ai/omnipus/pkg/channels/dingtalk"
	_ "github.com/elicify-ai/omnipus/pkg/channels/discord"
	_ "github.com/elicify-ai/omnipus/pkg/channels/feishu"
	_ "github.com/elicify-ai/omnipus/pkg/channels/googlechat"
	_ "github.com/elicify-ai/omnipus/pkg/channels/irc"
	_ "github.com/elicify-ai/omnipus/pkg/channels/line"
	_ "github.com/elicify-ai/omnipus/pkg/channels/qq"
	_ "github.com/elicify-ai/omnipus/pkg/channels/slack"
	_ "github.com/elicify-ai/omnipus/pkg/channels/telegram"
	_ "github.com/elicify-ai/omnipus/pkg/channels/wecom"
	_ "github.com/elicify-ai/omnipus/pkg/channels/weixin"
	_ "github.com/elicify-ai/omnipus/pkg/channels/whatsapp_native"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// seedSystemAgentEagerSouls eagerly backfills EVERY seeded System Agent's
// SOUL.md with its compiled default soul (coreagent.SystemAgentDefaultSoul —
// JudgeDefaultRubric for the Judge, PlanSupervisorDefaultRubric for the
// PlanSupervisor) at gateway boot, right after coreagent.SeedConfig has
// ensured their AgentConfig entries exist.
//
// It iterates coreagent.SystemAgents() rather than naming ids, so adding a
// System Agent with a default soul needs no edit here — a previous version
// looped for the Judge alone, which is precisely how the PlanSupervisor's
// rubric ended up existing only as a Go constant that never reached disk.
//
// FOR THE JUDGE this fixes an operator-reported UX gap: its soul used to
// materialize ONLY lazily, on its first real verifier dispatch (pkg/agent's
// ensureVerifierSoul) — but the soul is now operator-editable in the SPA
// (judge_soul_editable_test.go), so a fresh install's Judge profile must show
// the default standards immediately, not stay blank until the operator has
// already triggered a judgment.
//
// FOR THE PLANSUPERVISOR this is not a UX nicety but the ONLY seed path
// (plan-supervisor-spec FR-005 rev 2 deliberately adds no lazy backstop: the
// Judge's backstop is Judge-gated and sits on the verifier-dispatch path,
// which a bus-woken PlanSupervisor never reaches). If this call does not
// fire, the adjudicator wakes with an EMPTY prompt.
//
// This call site — pkg/gateway's boot sequence — was chosen over folding
// the write into coreagent.SeedConfig/seedSystemAgents themselves for two
// independent reasons, both already true of the pre-existing lazy seed
// (see ensureVerifierSoul's doc comment, verifier_adjudication.go):
//
//  1. coreagent.SeedConfig is documented, and relied on by its own test
//     suite (none of which sets OMNIPUS_HOME), as a PURE config-struct
//     mutation with zero filesystem side effects. Adding a disk write there
//     would start silently touching the real machine's home directory on
//     every `go test ./pkg/coreagent/...` run.
//  2. pkg/coreagent cannot cleanly resolve a System Agent's REAL workspace
//     path itself — that resolution (OMNIPUS_HOME lookup, ID sanitization,
//     traversal guarding) lives in agent.ResolveAgentHome, and
//     pkg/coreagent cannot import pkg/agent
//     (pkg/agent already imports pkg/coreagent — that direction would be a
//     cycle). Reimplementing the resolution a second time in pkg/coreagent
//     would be a second source of truth that could silently drift from the
//     path the agent's real AgentInstance.Home resolves to at runtime.
//
// pkg/gateway already imports both pkg/agent and pkg/coreagent, so it is
// the cleanest place that can call the real, single-source-of-truth
// agent.ResolveAgentHome and land each seed at EXACTLY the directory that
// agent's own AgentInstance will later use — then delegates the actual
// write (mkdir + backfill-only-when-missing/empty + atomic write) to
// agent.SeedSystemAgentSoulFile, the same helper ensureVerifierSoul uses, so
// the call sites can never diverge on write semantics. In particular the
// "never overwrite existing non-empty content" rule lives THERE, which is
// what keeps this safe to run on every boot: an operator's edited soul
// survives a restart untouched, exactly like the identity/type/locked/
// tool-policy re-enforcement in seedSystemAgents leaves Model/Provider alone.
//
// Non-fatal per agent: a failure is logged at WARN and boot continues, and
// one agent's failure never skips the rest — an empty soul degrades that
// agent, it is not a boot-blocking condition.
func seedSystemAgentEagerSouls(cfg *config.Config) {
	for _, sa := range coreagent.SystemAgents() {
		if strings.TrimSpace(coreagent.SystemAgentDefaultSoul(sa.ID)) == "" {
			// A System Agent with no compiled default soul has nothing to
			// backfill (its prompt comes from elsewhere). Skipping here keeps
			// SeedSystemAgentSoulFile's "no default soul" error a real,
			// loud misconfiguration signal for other callers instead of a
			// WARN this loop would emit on every boot forever.
			continue
		}
		idx := -1
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == string(sa.ID) {
				idx = i
				break
			}
		}
		if idx < 0 {
			// coreagent.SeedConfig runs immediately before this and seeds
			// every System Agent, so a missing entry means the roster and the
			// System-Agents list have diverged — loud, because for the
			// PlanSupervisor this loop is the ONLY path that gives it a prompt.
			slog.Warn("gateway: System Agent missing from roster; default soul not seeded",
				"agent_id", string(sa.ID))
			continue
		}
		home := agent.ResolveAgentHome(&cfg.Agents.List[idx], &cfg.Agents.Defaults)
		if err := agent.SeedSystemAgentSoulFile(home, sa.ID); err != nil {
			slog.Warn("gateway: could not eagerly seed System Agent default soul",
				"error", err, "agent_id", string(sa.ID), "workspace", home)
		}
	}
}

// lastNonEmptyRosters remembers, per home directory, the most recently
// observed NON-EMPTY agent roster loaded by
// populateAgentsListFromEntityStoreStrict. It backs that function's
// regression guard: a fresh entity-store List() that comes back EMPTY for a
// home directory that previously yielded a real roster is treated as a hard
// failure rather than silently wiping the in-memory roster — see that
// function's doc comment for the full rationale (an empty roster does not
// merely mean "nothing to route to"; it promotes ALL traffic to an
// unrestricted fallback agent).
//
// Keyed by homePath (never a single global) so multiple *config.Config
// instances/tests rooted at different homes cannot cross-contaminate each
// other's remembered roster. Process-lifetime only, in-memory — a
// genuinely fresh process/home combination has no entry yet, so its first
// (legitimately empty, pre-SeedConfig) population never trips the guard.
var (
	lastNonEmptyRostersMu sync.Mutex
	lastNonEmptyRosters   = map[string][]config.AgentConfig{}
)

// forgetRosterBaseline drops the remembered non-empty roster for homePath so
// the next populateAgentsListFromEntityStoreStrict call will not treat a
// legitimately-shrunk roster as a regression.
//
// WHY THIS IS NEEDED, and why the guard alone is not enough: the regression
// guard cannot distinguish "the store broke and handed back nothing" from
// "the operator deleted the last agent" — both look like non-empty -> empty,
// and an on-disk file count does not separate them either (a homePath that
// resolves to the WRONG directory also reports zero records, which is the
// precise failure the guard exists to catch). The authority on an INTENTIONAL
// shrink is the mutation path, so deleteAgent tells the guard rather than the
// guard trying to infer it.
//
// Without this, deleting the LAST agent wedged the running gateway: the entity
// record was removed from disk, the post-delete reload was rejected by the
// guard, and the in-memory roster kept serving the deleted agent until a
// restart — permanent divergence between disk and memory. Regression coverage:
// TestHandleAgentsDelete_OK (rest_clidetect_test.go) deletes the only agent and
// asserts the subsequent GET is 404.
//
// The narrow trade-off is deliberate: if the store ALSO fails during the very
// next reload after an intentional delete, that one reload accepts an empty
// roster instead of rejecting it. The baseline re-establishes on the following
// successful load, and an operator-initiated delete is a far weaker signal of
// compromise than an unexplained disappearance.
func forgetRosterBaseline(homePath string) {
	lastNonEmptyRostersMu.Lock()
	delete(lastNonEmptyRosters, homePath)
	lastNonEmptyRostersMu.Unlock()
}

// populateAgentsListFromEntityStore — the legacy void, log-and-continue
// bridge between the per-entity agent store (entities/agents/<id>.json) and
// cfg.Agents.List — was DELETED (RELEASE BLOCKER security-fix follow-up,
// 2026-07-26). It was kept only for pkg/gateway/rest.go's
// populateAgentsListFromStore and rest_pending_restart.go's
// HandlePendingRestart, which were out of this security-fix pass's original
// file-ownership scope; once those call sites were fixed to call the strict,
// fail-closed populateAgentsListFromEntityStoreStrict directly (same package,
// no export needed) and reject on error instead of silently proceeding with
// whatever roster the entity store handed back, nothing in the codebase
// called this lenient wrapper anymore (verified: `grep -rn
// "populateAgentsListFromEntityStore("` finds only the strict variant's own
// definition). See populateAgentsListFromEntityStoreStrict's doc comment
// immediately below for the full privilege-escalation rationale this
// wrapper's removal closes off entirely rather than leaving as a
// still-reachable, silently-permissive code path.

// populateAgentsListFromEntityStoreStrict is populateAgentsListFromEntityStore's
// fail-closed variant. It returns a non-nil error whenever the entity
// store's state cannot be trusted enough to safely (re)populate
// cfg.Agents.List, and on ANY error path it leaves cfg.Agents.List and
// cfg.SkippedAgentIDs COMPLETELY UNTOUCHED — callers own the decision of
// what "cannot trust this" means for them (boot aborts; a reload rejects the
// candidate config and marks the service degraded via
// (*services).markReloadDegraded rather than swapping it in).
//
// This distinction matters far more than it looks: an EMPTY cfg.Agents.List
// does not merely mean "no agent to route a message to". Verified
// 2026-07-26 as a real privilege-escalation chain, not a theoretical one.
//
// The chain's ENTRY POINT is now closed: NewAgentRegistry used to ALWAYS
// register an unrestricted "main" sentinel AgentConfig carrying no
// Tools/Policies at all, and that agent has been removed — the registry now
// contains only agents from cfg.Agents.List, each of which the coverage gate
// does validate. The MECHANISM it exploited is unchanged and still worth
// guarding: pkg/tools/compositor.go's global×agent policy merge
// (resolveEffectivePolicyWith) falls through to the GLOBAL floor for every
// tool an agent has no per-agent policy entry for — which was every tool,
// for that sentinel. pkg/config/defaults.go seeds that global floor "allow"
// for bash, write_file, edit_file, delegate, send_email, and more. So a
// wiped roster silently promotes ALL routed traffic (via
// AgentRegistry.GetDefaultAgent's fallback ladder) to an unrestricted
// agent — and repairAndValidateToolPolicyCoverage (this file), which walks
// cfg.Agents.List to find coverage gaps, finds ZERO agents to check and
// vacuously PASSES an empty roster, so the existing coverage gate does not
// catch this at all. Silently limping on with whatever (potentially empty)
// roster the entity store handed back — the historical behavior, preserved
// only in the legacy populateAgentsListFromEntityStore wrapper above — is
// therefore never acceptable from a fresh call site.
//
// Three independent failure classes are rejected here:
//
//  1. A genuine entity.Store.List() error (e.g. EMFILE/ENFILE under fd
//     pressure, EACCES after a restore with the wrong ownership, EIO,
//     entities/agents shadowed by a regular file) — propagated directly.
//     This is DIFFERENT from "the directory does not exist yet", which
//     entity.Store.List() maps to (nil, nil, nil): a genuine fresh-install
//     state, not an error.
//  2. Every on-disk agent record failed to parse (List() succeeds, but
//     every id it found landed in `skipped`, none in `agents`) — e.g. a
//     breaking schema change. total := len(agents)+len(skipped) is the true
//     on-disk record count (every id List() finds lands in exactly one of
//     the two); total > 0 with zero LOADED agents must never be treated as
//     "fresh install, nothing configured" (total == 0 is the genuine
//     fresh-install case and is unaffected).
//  3. A regression within this process's own lifetime: homePath previously
//     yielded a non-empty roster (tracked in lastNonEmptyRosters) and this
//     call now yields an empty one. A genuinely fresh process/home
//     combination never has a prior entry, so this cannot fire on a real
//     first boot — it only fires on a live process observing its own
//     roster apparently disappear, e.g. homePath momentarily/incorrectly
//     resolving to the wrong directory (see setupConfigWatcherPolling's
//     homePath-threading fix) or a transient store hiccup that happened to
//     return a clean empty list instead of a class-1 error.
//
// Also closes the ADR-054-era normalization gap: entity-loaded agents never
// pass through loadConfigInternal's own NormalizeFallbacks /
// migrateAgentPrimaryProvider passes (those only run against config.json's
// agents.list inside config.LoadConfig*, which is stripped to empty before
// this bridge ever runs) — config.NormalizeAgentRoster applies both to the
// roster on every successful load here so an agent whose FallbackModel/
// primary-model fields were written pre-split still resolves correctly.
func populateAgentsListFromEntityStoreStrict(cfg *config.Config, homePath string) error {
	agents, skipped, err := agentstore.New(homePath).List()
	if err != nil {
		logger.Errorf("gateway: agent entity store list failed at %q: %v", homePath, err)
		return fmt.Errorf("gateway: could not list agent entity records at %q: %w", homePath, err)
	}

	if total := len(agents) + len(skipped); total > 0 && len(agents) == 0 {
		logger.Errorf("gateway: agent entity store at %q has %d on-disk record(s), all %d "+
			"unparseable — refusing to treat this as a fresh install", homePath, total, len(skipped))
		return fmt.Errorf(
			"gateway: entity store at %q has %d on-disk agent record(s), all %d unparseable — "+
				"refusing to treat this as a fresh install with zero agents",
			homePath, total, len(skipped),
		)
	}

	if len(agents) == 0 {
		lastNonEmptyRostersMu.Lock()
		previous := lastNonEmptyRosters[homePath]
		lastNonEmptyRostersMu.Unlock()
		if len(previous) > 0 {
			logger.Errorf("gateway: agent entity store at %q returned an EMPTY roster where a "+
				"NON-EMPTY roster (%d agents) was previously loaded for this home — refusing to "+
				"overwrite the in-memory roster", homePath, len(previous))
			return fmt.Errorf(
				"gateway: entity store at %q returned an EMPTY roster where a NON-EMPTY roster "+
					"(%d agents) was previously loaded for this home", homePath, len(previous),
			)
		}
	}

	cfg.Agents.List = agents
	cfg.SkippedAgentIDs = skipped
	config.NormalizeAgentRoster(cfg)

	if len(agents) > 0 {
		rosterCopy := make([]config.AgentConfig, len(agents))
		copy(rosterCopy, agents)
		lastNonEmptyRostersMu.Lock()
		lastNonEmptyRosters[homePath] = rosterCopy
		lastNonEmptyRostersMu.Unlock()
	}
	return nil
}

// persistSeededCoreAgents persists every agent SeedConfig added-or-touched
// via the agent store: Create for one with no existing record, Update (full-
// record replace) for one that already has one — matching SeedConfig's own
// "re-enforce identity fields on existing core agents" semantics. Extracted
// from RunContextWithOptions as its own function so the fix below (a single
// corrupt/unparseable entity record must degrade, never abort boot —
// ADR-054 D7 + §0 R3) is directly unit-testable without spinning up the
// full boot sequence (credentials, providers, agent loop).
//
// store.Get's error is explicitly classified rather than treated as a bare
// "absent" signal: gating solely on "any error means create" (the previous
// behavior) mis-handled a PARSE error (corrupt entities/agents/<id>.json)
// identically to "record does not exist yet" — store.Create then hit
// entity.ErrAlreadyExists (the file DOES exist, it just didn't parse) and
// that error was propagated as a hard boot-abort. One unparseable agent
// record made the entire gateway unbootable, inverting ADR-054's own D7
// ("unparseable record -> skip + ERROR + mark degraded") and §0 R3, which
// explicitly rejected fail-closed here because a single corrupt file
// dropping ALL inbound traffic has no in-product repair path. Only a true
// entity.ErrNotFound now takes the create path; anything else (a corrupt
// record, a permission error, etc.) is skipped with an ERROR log so boot
// continues — the entity's on-disk record is left exactly as it was rather
// than being clobbered by a Create attempt that would only fail anyway.
func persistSeededCoreAgents(homePath string, agents []config.AgentConfig) error {
	store := agentstore.New(homePath)
	for i := range agents {
		seeded := agents[i]
		_, getErr := store.Get(seeded.ID)
		switch {
		case getErr == nil:
			// Record exists and parsed fine — re-enforce identity fields.
			if _, updateErr := store.Update(seeded.ID, func(existing *config.AgentConfig) error {
				*existing = seeded
				return nil
			}); updateErr != nil {
				return fmt.Errorf("gateway: failed to persist seeded core agent %q: %w", seeded.ID, updateErr)
			}
		case errors.Is(getErr, entity.ErrNotFound):
			// No record on disk yet — create it.
			if createErr := store.Create(seeded.ID, &seeded); createErr != nil {
				return fmt.Errorf("gateway: failed to persist seeded core agent %q: %w", seeded.ID, createErr)
			}
		default:
			// Get failed for a reason OTHER than "not found" — most commonly a
			// corrupt/unparseable record. Skip re-seeding this one agent rather
			// than aborting the whole boot; see this function's doc comment.
			logger.Errorf("gateway: seeded core agent %q record exists but could not be read "+
				"(corrupt/unparseable?) — skipping re-seed for this agent; boot continues degraded "+
				"for this agent only: %v", seeded.ID, getErr)
		}
	}
	return nil
}

// persistFreshInstallDefaultAgentID writes agents.defaults.default_agent_id
// into config.json's raw JSON map, preserving every other key exactly as-is —
// unlike config.SaveConfig, which round-trips the whole typed Config struct
// and can clobber SecureString-backed API keys (CLAUDE.md hard rule: "NEVER
// use config.SaveConfig() — it corrupts API keys"). Mirrors
// pkg/gateway/rest.go's updateConfigJSONLocked/ensureMap read-modify-write
// convention. Called exactly once, at boot, immediately after
// coreagent.SeedConfig sets this field in memory on a genuinely fresh
// install (SeedConfig itself performs no file I/O by design) — see the call
// site's doc comment for why this durability step cannot live inside
// SeedConfig or persistSeededCoreAgents.
func persistFreshInstallDefaultAgentID(configPath, agentID string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return fmt.Errorf("parse config: %w", unmarshalErr)
	}
	ensureMap(m, "agents", "defaults")["default_agent_id"] = agentID
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	if err := fileutil.WriteFileAtomic(configPath, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// persistSeededSkillGrants durably records config.seeded_skill_grants (the
// ADR-074 D4 one-shot migration markers coreagent.SeedConfig checks) into
// config.json's raw JSON map, preserving every other key exactly as-is — the
// same read-modify-write convention as persistFreshInstallDefaultAgentID
// above, and for the same reason: SeedConfig is a pure config-struct mutation
// with zero filesystem side effects, so without this step the marker lives
// only in THIS process's in-memory cfg and the migration would re-run on
// every boot (harmless in effect — it is additive and append-if-lacking — but
// it would defeat the marker's "run once, recorded" contract and rewrite
// config.json every boot).
//
// Idempotent at the byte level: when the on-disk key already equals the
// in-memory value the file is left completely untouched (no write, no mtime
// churn), making the second boot a byte-level no-op (judgment-first spec
// test 16).
func persistSeededSkillGrants(configPath string, markers []string) error {
	return persistConfigMarkerList(configPath, "seeded_skill_grants", markers)
}

// persistSeededToolPolicyUpdates durably records
// config.seeded_tool_policy_updates (the one-time seeded tool-policy update
// markers coreagent.SeedConfig checks, e.g. the Worker goal_claim update)
// into config.json. Same contract as persistSeededSkillGrants: raw-map
// read-modify-write that preserves every other key, and no write at all when
// the on-disk value already matches.
func persistSeededToolPolicyUpdates(configPath string, markers []string) error {
	return persistConfigMarkerList(configPath, "seeded_tool_policy_updates", markers)
}

// persistConfigMarkerList writes markers under the top-level config.json key
// key, preserving every other key exactly as-is, and skips the write entirely
// when the on-disk list already equals markers (byte-level no-op on a
// settled install).
func persistConfigMarkerList(configPath, key string, markers []string) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return fmt.Errorf("parse config: %w", unmarshalErr)
	}
	// Skip the write entirely when the on-disk value already matches.
	if existing, ok := m[key].([]any); ok && len(existing) == len(markers) {
		same := true
		for i := range markers {
			if s, isStr := existing[i].(string); !isStr || s != markers[i] {
				same = false
				break
			}
		}
		if same {
			return nil
		}
	}
	m[key] = markers
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize config: %w", err)
	}
	if err := fileutil.WriteFileAtomic(configPath, out, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

// deleteOrphanedDefineDoneDir deletes the orphaned define-done/ skill
// directory left behind by the ADR-080 D-SKILL define-goal rename — but
// ONLY when the replacement define-goal/ directory is verifiably present on
// disk (fix-wave finding #1, operator-ratified 2026-09-07 Q3). The
// migration marker (SkillsMigrationDefineGoalRename) alone is NOT
// sufficient evidence the rename actually landed: skills.SeedDefaults can
// fail (disk full, permissions, a corrupt embed) after the marker was
// already recorded in SeededSkillGrants, and deleting define-done/ on the
// marker's say-so alone would leave a fresh boot with NEITHER directory on
// disk — loadDefineGoalSkillContent silently returns "" in that state, and
// every `/goal` compile silently loses its quality bar with no observable
// signal at compile time.
//
// The safe order is: marker present -> define-goal/ verifiably present ->
// ONLY THEN delete define-done/. Any other combination fails SAFE (not
// open): define-done/ is left untouched and the reason is returned so the
// caller can WARN. Returns deleted=true only when define-done/ was actually
// removed by THIS call; a repeat call after a successful deletion (or when
// the marker is absent, or define-done/ was never there) is a clean, silent
// no-op (deleted=false, err=nil) — idempotent by the directories' own
// on-disk state, never a second marker.
func deleteOrphanedDefineDoneDir(skillsGlobalDir string, markers []string) (deleted bool, err error) {
	renamed := false
	for _, m := range markers {
		if m == coreagent.SkillsMigrationDefineGoalRename {
			renamed = true
			break
		}
	}
	if !renamed {
		// A pre-ADR-080 install that has not yet run the rename never
		// reaches this branch — its define-done/ stays untouched until its
		// own boot actually rewrites its allowlists.
		return false, nil
	}

	defineGoalDir := filepath.Join(skillsGlobalDir, "define-goal")
	if _, statErr := os.Stat(defineGoalDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return false, fmt.Errorf(
				"replacement define-goal/ skill directory not found at %s (SeedDefaults may have failed) "+
					"— preserving define-done/ rather than deleting it", defineGoalDir)
		}
		return false, fmt.Errorf("could not stat replacement define-goal skill directory %s: %w", defineGoalDir, statErr)
	}

	orphanedDir := filepath.Join(skillsGlobalDir, "define-done")
	if _, statErr := os.Stat(orphanedDir); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return false, nil // already deleted (or never existed) — clean no-op
		}
		return false, fmt.Errorf("could not stat orphaned define-done skill directory %s: %w", orphanedDir, statErr)
	}

	if rmErr := os.RemoveAll(orphanedDir); rmErr != nil {
		return false, fmt.Errorf("could not delete orphaned define-done skill directory %s: %w", orphanedDir, rmErr)
	}
	return true, nil
}
