// Omnipus — Skill: on-demand skill activation (ADR-072 D1)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — SkillTool is the load-by-slug / search-by-query tool that
// replaces force-loaded skill instructions. It is this codebase's second
// instance of the "index in context, content on demand" pattern ADR-071
// established for tools: `ToolSearch` (tools_tool.go) fetches a tool's full
// schema only when the agent asks for it, and this tool does the identical
// thing one layer up for skills — the system prompt's `# Skills` menu
// (pkg/skills.SkillsLoader.BuildSkillsSummaryFunc) carries every GRANTED
// skill's slug/name/description inside the cached prefix, and `Skill` is the
// only way a skill's body ever enters a turn (D1's "delete the force-load").
//
// See docs/internal/architecture/ADR-072-skill-activation-and-loading.md D1,
// D1.1, D1.2, D4 and
// docs/internal/specs/skill-activation-and-loading-spec.md FR-001..FR-009,
// FR-021, FR-022.
//
// Wiring note: this file defines the tool's shape and its self-contained BM25
// search — exactly like tools_tool.go's ToolsTool, the actual per-agent
// resolver (which shelf a slug lives on, whether THIS agent is granted it) is
// injected via SetResolver by a later integration phase that has access to
// the agent loop's per-agent ContextBuilder / shelf-resolution wiring
// (pkg/skills.ResolveSkillName from the prior phase). This file, deliberately,
// does not register Skill into GeneralBuiltinMetadata()
// (general_builtin_catalog.go): doing so would pull in Constraint #6's
// boot-time tool-policy coverage validation (an explicit policy entry for
// EVERY agent), which requires seeding pkg/coreagent's per-agent defaults and
// pkg/config's global ceiling in lockstep — cross-cutting work outside this
// phase's assigned scope (menu/summary + the tool itself), left to the
// integration phase that also wires SetResolver.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/skills"
	"github.com/elicify-ai/omnipus/pkg/utils"
)

// SkillLoadStatus is the closed set of outcomes SkillTool's load path can
// report. Deliberately distinguishes "the agent may not use this slug"
// (SkillLoadDenied) from "no such slug exists on any shelf visible to the
// agent" (SkillLoadNotFound) — ADR-072 D4/FR-054: "denies cleanly if
// ungranted, distinguishes not-found from denied."
type SkillLoadStatus int

const (
	// SkillLoadNotFound is the zero value deliberately: an unwired or
	// misbehaving resolver fails toward "nothing exists", never toward
	// "granted" or "installed-but-denied" (which would leak existence of an
	// otherwise-invisible slug through the error path itself).
	SkillLoadNotFound SkillLoadStatus = iota
	SkillLoadDenied
	SkillLoadLoaded
)

// SkillLoadOutcome is what a SkillTool's injected load resolver hands back
// for one slug, resolved strictly against the CALLING agent (read from ctx
// inside the resolver) — never a caller-supplied identity, and never the
// agent that dispatched a delegation (ADR-072 D9 is the same principle one
// layer up: the receiver's own grant, always).
type SkillLoadOutcome struct {
	Status  SkillLoadStatus
	Content string
	Shelf   skills.SkillShelf
	// CanonicalSlug is the resolved slug (case-canonicalised to how it is
	// actually stored), populated only when Status == SkillLoadLoaded.
	CanonicalSlug string
}

// SkillSearchDoc is one entry of the corpus SkillTool's search mode ranks
// over: every installed skill's slug and description, across every shelf,
// UNFILTERED by any agent's grant. ADR-071 §3.2.2, applied to skills exactly
// as ADR-072 D1 directs: ranking must run over the WHOLE corpus — filtering
// it down first would leak an ungranted skill's relative ranking/existence
// through score ordering, make the ranking uncacheable per-agent, and change
// BM25's own scores (different corpus ⇒ different IDF/avgDocLen). Only the
// ranked MATCH LIST is filtered afterward, in execSearch below.
type SkillSearchDoc struct {
	Slug        string
	Description string
}

// SkillTool is the ADR-072 D1 `Skill` tool: load a skill's instructions by
// slug for the current turn only, or search the installed catalogue by what
// it does. Both modes resolve strictly against the calling agent's own
// per-shelf grant (D4/D4.1) via a resolver set through SetResolver —
// mirroring ToolsTool.SetResolver's contract exactly (tools_tool.go), since
// this is the same "index in context, content on demand" pattern one layer
// up (D1).
type SkillTool struct {
	BaseTool
	// maxSearchResults bounds the search mode's match list. ADR-072
	// D1.2/MIN-003: deliberately the SAME bound the caller wires ToolSearch
	// with (MaxSearchResults) — this tool introduces no new limit of its own.
	maxSearchResults int

	// load resolves slug for the acting agent (read from ctx, typically via
	// tools.ToolAgentID) through the shelf-resolution model
	// (pkg/skills.ResolveSkillName) and loads its body. Nil until SetResolver
	// is called — Execute then fails closed with a configuration error,
	// never a silent empty result.
	load func(ctx context.Context, slug string) SkillLoadOutcome
	// canUse reports whether the acting agent may load slug — the SAME
	// per-shelf grant model `load` consults, exposed separately so the
	// search path can filter the ranked match list WITHOUT loading every
	// candidate's full body just to check permission.
	canUse func(ctx context.Context, slug string) bool
	// corpus returns every installed skill's slug+description across every
	// shelf visible to the acting agent's installation/workspace — NOT yet
	// grant-filtered (that happens after ranking, via canUse; see the
	// SkillSearchDoc doc comment for why). Nil ⇒ search always reports no
	// matches, rather than panicking or disclosing anything.
	corpus func(ctx context.Context) []SkillSearchDoc
}

// NewSkillTool constructs a Skill tool with no resolver wired — Name(),
// Description(), Category() and Parameters() are safe to call immediately
// (the metadata-catalog contract every other builtin tool constructor
// follows); Execute requires SetResolver first.
func NewSkillTool(maxSearchResults int) *SkillTool {
	return &SkillTool{maxSearchResults: maxSearchResults}
}

// SetResolver wires the per-installation callbacks the load and search paths
// need. Mirrors ToolsTool.SetResolver's contract: the tool is inert (Execute
// fails closed) until this is called.
func (t *SkillTool) SetResolver(
	load func(ctx context.Context, slug string) SkillLoadOutcome,
	canUse func(ctx context.Context, slug string) bool,
	corpus func(ctx context.Context) []SkillSearchDoc,
) {
	t.load = load
	t.canUse = canUse
	t.corpus = corpus
}

func (t *SkillTool) Name() string           { return "Skill" }
func (t *SkillTool) Scope() ToolScope       { return ScopeGeneral }
func (t *SkillTool) Category() ToolCategory { return CategorySkills }

// Description disambiguates Skill from its two neighbours in trigger terms
// (FR-001a — applying D2's own authoring rule to this tool's own
// description, per the ADR's explicit instruction): Skill USES a skill you
// already have; list_skills enumerates what you have, with no content;
// find_skills searches the marketplace for skills you do NOT have yet.
func (t *SkillTool) Description() string {
	return "Use a skill you already have — loads its full instructions for THIS turn only. " +
		"Pass 'name' with the skill's exact slug (as listed in the '# Skills' section of your " +
		"context) to load it; pass 'query' instead to search your installed skills by what they " +
		"do. This is not list_skills (which enumerates what you have, with no content) and not " +
		"find_skills (which searches the marketplace for skills you do NOT have installed)."
}

func (t *SkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Exact skill slug to load, from the '# Skills' section of your context. When present (non-empty), this mode runs and 'query' is ignored.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Describe what you need — searches your installed skills. Used only when 'name' is absent or empty.",
			},
		},
	}
}

// Execute dispatches on which parameter is present. Precedence mirrors
// ToolsTool exactly: a non-empty 'name' always runs the load path; 'query'
// runs search only when 'name' is absent or empty.
func (t *SkillTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	name, _ := args["name"].(string)
	name = strings.TrimSpace(name)
	if name != "" {
		return t.execLoad(ctx, name)
	}

	query, _ := args["query"].(string)
	if strings.TrimSpace(query) != "" {
		return t.execSearch(ctx, query)
	}

	return ErrorResult("Skill: provide either 'name' (to load a skill by its exact slug) or 'query' (to search your installed skills)")
}

// ── load path ────────────────────────────────────────────────────────────

func (t *SkillTool) execLoad(ctx context.Context, name string) *ToolResult {
	if !skills.ValidSlug(name) {
		return ErrorResult(fmt.Sprintf(
			"Skill: %q is not a valid skill identifier — expected alphanumeric segments joined by single hyphens, at most %d characters",
			name, skills.MaxNameLength,
		))
	}
	if t.load == nil {
		return ErrorResult("Skill(load): resolver not set — internal configuration error")
	}

	outcome := t.load(ctx, name)
	switch outcome.Status {
	case SkillLoadLoaded:
		return SilentResult(outcome.Content)
	case SkillLoadDenied:
		return skillPermissionDeniedResult(name)
	default: // SkillLoadNotFound
		return skillNotFoundResult(name)
	}
}

// ── search path ──────────────────────────────────────────────────────────

func (t *SkillTool) execSearch(ctx context.Context, query string) *ToolResult {
	if t.corpus == nil {
		return SilentResult("No skills found matching the query.")
	}
	docs := t.corpus(ctx)
	if len(docs) == 0 {
		return SilentResult("No skills found matching the query.")
	}

	// ADR-072 D1 / ADR-071 §3.2.2, applied to skills: rank over the WHOLE
	// corpus (topK = len(docs), not maxSearchResults) so the policy filter
	// below can build a truthful loadable list without silently losing a
	// loadable candidate ranked below a denied one.
	engine := buildSkillBM25Engine(docs)
	ranked := engine.Search(query, len(docs))
	if len(ranked) == 0 {
		return SilentResult("No skills found matching the query.")
	}

	if t.canUse == nil {
		// ADR-071 §3.2.2 point 5, applied to skills: no resolver ⇒ fail
		// closed, no disclosure at all — not even a slug.
		return SilentResult("No skills found matching the query.")
	}

	// Filter-and-truncate in ONE pass, in rank order — the MATCH LIST is
	// filtered after ranking; the corpus above was never filtered at all
	// (ADR-072 D4/D1, ADR-071 §3.2.2: filtering the corpus would leak an
	// ungranted skill's existence/relative ranking; filtering only the
	// result the agent actually sees does not).
	matches := make([]ToolSearchResult, 0, t.maxSearchResults)
	for _, r := range ranked {
		if len(matches) >= t.maxSearchResults {
			break
		}
		if !t.canUse(ctx, r.Document.Slug) {
			continue
		}
		matches = append(matches, ToolSearchResult{Name: r.Document.Slug, Description: r.Document.Description})
	}

	if len(matches) == 0 {
		return SilentResult("No skills found matching the query.")
	}

	encoded, err := json.Marshal(map[string]any{"matches": matches})
	if err != nil {
		return ErrorResult(fmt.Sprintf("Skill(query): failed to encode result: %v", err))
	}

	return SilentResult(fmt.Sprintf(
		"Found %d skills. Call Skill again with the exact slug in 'name' to load one.\n%s",
		len(matches), string(encoded),
	))
}

// buildSkillBM25Engine builds a fresh BM25 engine over docs on every call.
// Unlike ToolsTool.getOrBuildEngine, this is deliberately NOT cached across
// calls: the installed-skill catalogue is small enough (a few dozen to a few
// hundred entries even under D1.1's now-uncapped menu) that a per-call
// rebuild is cheap, and caching would need a skills-specific analogue of the
// tool registry's version counter that this phase does not own (that
// invalidation signal is D8 cache work, a later phase). A future phase may
// add caching the way ToolsTool's getOrBuildEngine does, keyed on whatever
// signal D8 settles on.
func buildSkillBM25Engine(docs []SkillSearchDoc) *utils.BM25Engine[SkillSearchDoc] {
	return utils.NewBM25Engine(docs, func(d SkillSearchDoc) string {
		return d.Slug + " " + d.Description
	})
}

// skillPermissionDeniedResult builds the structured denial for a slug the
// acting agent is not granted (FR-021: "reusing the existing
// PermissionDeniedCode ... rather than minting a parallel string").
// PermissionDeniedPayload is the SAME generic wire-payload builder every
// other permission_denied producer in this codebase uses (result.go) — it is
// not filesystem-specific, so no new schema is introduced (ADR-072 §7: no
// new wire type required for Skill's results).
func skillPermissionDeniedResult(slug string) *ToolResult {
	message := fmt.Sprintf("You are not granted the %q skill.", slug)
	encoded, err := PermissionDeniedPayload("Skill", message, "skill not granted to this agent", true)
	if err != nil {
		warnStructuredFailureMarshalError("skillPermissionDeniedResult", "Skill", PermissionDeniedCode, err)
		return ErrorResult(message)
	}
	return &ToolResult{ForLLM: string(encoded), IsError: true}
}

// skillNotFoundResult builds the structured not-found response for a slug
// that does not exist on any shelf visible to the acting agent (FR-054's
// SkillNotFoundCode). Deliberately a plain map[string]any marshaled with
// encoding/json rather than a generated.*Failure struct — see
// SkillNotFoundCode's own doc comment (result.go) for why no such generated
// shape exists or is needed here.
func skillNotFoundResult(slug string) *ToolResult {
	message := fmt.Sprintf("No skill named %q is installed.", slug)
	payload := map[string]any{
		"error":   SkillNotFoundCode,
		"tool":    "Skill",
		"skill":   slug,
		"message": message,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ErrorResult(message)
	}
	return &ToolResult{ForLLM: string(encoded), IsError: true}
}
