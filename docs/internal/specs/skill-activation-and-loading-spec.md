# Spec — Skill activation and loading: the `Skill` tool, per-shelf grants, and project skills (ADR-072)

**Created**: 2026-09-01
**Status**: Draft (rev 5) — revised against the adversarial review (`…-spec-review.md`, REVISE: 3 CRITICAL, 5 MAJOR, 4 MINOR, 2 OBSERVATION). Three findings were design changes and landed in **ADR-072 r4** (D10.2, D6.1.1, D1.2).
**Source**: [ADR-072 r6](../architecture/ADR-072-skill-activation-and-loading.md) (Accepted), answering
[elicify-ai/omnipus#663](https://github.com/elicify-ai/omnipus/issues/663)
**Phase**: v0.3 (CLAUDE.md routing rule — skills/workspaces/plugins → v0.3, issue #156)
**Verified against**: `release/v0.1.1` @ `f101a9b4`

---

## 1. Overview

### 1.1 Discovery basis — synthesized from ADR-072, not re-interviewed

Phase 1's discovery questions are answered by ADR-072 itself: its §1 Problem, its twelve decision
points (D1–D10.2), its §3 Consequences, its §5/§6 resolutions including the founder's six direct
answers, and its §9 required-tests floor. This section records that synthesis rather than restating
the ADR. **Where the ADR is genuinely silent, §11 records it as a gap** — nothing is invented.

| Discovery dimension | Answer (source) |
|---|---|
| **Actors** | The agent (loads/searches skills); the operator (grants skills, creates mounts, authors skills); a delegating parent agent (D9); a delegated child agent (D9); the human typing `/<slug>` (ADR-026) |
| **Problem** | Omnipus has no agent-invoked way to activate a skill. The per-agent list meant as a *permission* list is used as an *always-load* list, injecting every granted skill's full body into every turn (ADR-072 §1.2) |
| **In scope** | On-demand activation; the grant as the real gate; project skills + instructions via mounts; delegation-with-skill; read gating; workspace-aware prompt cache |
| **Out of scope** | `create_skill`/`edit_skill`/`remove_skill` governance (tool policy, not the grant list — ADR-072 N2); `find_skills`' marketplace role (F3); moving grants to workspace scope (§6.5, deferred) |
| **Constraints** | CLAUDE.md Hard Constraints #1 (single binary), #2 (pure Go), #5 (ecosystem compat), #6 (no default-policy fallback), #8 (contract-first) |
| **Integration** | `pkg/skills`, `pkg/agent` (context + loop + subturn), `pkg/coreagent`, `pkg/sysagent/tools`, `pkg/fspolicy`, `pkg/sandbox`, `pkg/tools`, `pkg/workspace`, SPA `src/lib/toolVisibility.ts` |
| **Priority** | v0.3 foundation. D8 (workspace-aware cache) and D5.1 (`SeedConfig` gating) are prerequisites of correctness, not enhancements |
| **Primary walkthrough** | ADR-072 §"What a real turn looks like" (draft §05), reproduced as US-1/US-5 acceptance scenarios |
| **Non-behaviors** | ADR-072 §3 Negative + D1's "delete the force-load" + D4's out-of-scope carve-out. Consolidated in §5.2 |
| **Failure modes** | Skill silently never fires (§3 Negative, biggest risk); grant restored on reboot (D5.1); stale menu after workspace change (D8); slug shadowing (D4.2) |
| **Human evaluation** | ADR-072 §3's falsifiable target: zero uncached skill tokens on a no-skill turn, measured via `static_chars`/`total_chars` already logged |
| **Performance envelope** | Menu **uncapped** (ADR D1.1), with a mount-add-time threshold warning (D1.2); per-turn reminder ≤240 bytes; instructions ≤262144 bytes total; search inherits `MaxSearchResults`=5 |

**Confidence note (Phase 1 GATE):** every row above is traceable to ADR-072 text. The one place I
am *not* fully confident the ADR settles the matter is the interaction between project skills and
the menu cap, and between project skills and the authoring verbs — both were raised as founder
questions and are now answered (§14.2); the ADR carries them as D1.1 and D6.1.

### 1.2 What changes, in one table

| # | Change | ADR ref |
|---|---|---|
| 1 | New `Skill` tool: load-by-slug and search-by-query | D1 |
| 2 | Stop force-loading the grant list into every turn | D1 |
| 3 | Skill descriptions become trigger conditions, enforced at authoring | D2 |
| 4 | Per-request ≤30-token menu reminder; call hidden from chat thread | D3 |
| 5 | Every `Skill` call audited; per-skill `last_invoked` in UI | D3.1 |
| 6 | Grant gate at five doors (adds `list_skills`, `Skill` load, `Skill` search) | D4 |
| 7 | Per-shelf grant model: mount grants project skills | D4.1 |
| 8 | Collision rules: no shadowing granted slugs; mount-name order | D4.2 |
| 9 | Empty/absent grant list means *no* skills | D5 |
| 10 | `SeedConfig`'s skill re-seed gated behind `isFreshInstall` | D5.1 |
| 11 | Project skills discovered from mounts, re-rooting shelf 1 | D6 |
| 12 | Project `CLAUDE.md`/`AGENTS.md` as a second instructions source | D7 |
| 13 | Prompt cache keyed by (agent × workspace), bounded, invalidated | D8 |
| 14 | `delegate` gains `requested_skill`, gated by the receiver | D9 |
| 15 | Skill file reads routed through the tool (app + kernel layers) | D10 |

---

## 2. Existing codebase context

### 2.1 GitNexus status — STALE, fell back to direct verification

**The GitNexus index for this checkout is stale and was not used.** Recorded with evidence rather
than silently skipped, per the skill's own instruction ("note this in the spec and fall back to
manual codebase exploration; do not block on a stale index"):

| Check | Value |
|---|---|
| Registry entry for this checkout | `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2` |
| Indexed at | 2026-08-23, commit `e269e52c`, branch `fix/uat-defects-2026-08-22` |
| Current HEAD | `f101a9b4`, branch `release/v0.1.1` |
| Commits since index | **315** |
| Commits touching this feature's files since index | **23** |
| Feature files changed since index | `pkg/agent/context.go`, `pkg/agent/subturn.go`, `pkg/coreagent/core.go`, `pkg/fspolicy/secretset.go`, `pkg/fspolicy/secretset_kernel.go`, `pkg/skills/loader.go`, `pkg/skills/registry.go`, `pkg/skills/installer.go`, `pkg/sysagent/tools/skill.go` |

**Every single file this spec depends on has changed since the index was built.** A graph query
would answer from a snapshot predating the work. Two further checkouts are registered under the same
name `omnipus` (`wt-library-improvements`, `wt-context-budget`), so a name-resolving query could
also read a different branch's graph entirely — the exact hazard CLAUDE.md warns about. All symbol
facts below were therefore read directly from source at `f101a9b4`.

### 2.2 Symbols involved

| Symbol | Role | Verified behaviour today |
|---|---|---|
| `pkg/agent/context.go::skillAllowed` | **modifies** | Flat, shelf-agnostic case-folded map lookup. Returns `true` when the map is nil (unrestricted) |
| `pkg/agent/context.go::WithSkillAllowlist` | **modifies** | nil → no allowlist; non-nil empty → deny-all |
| `pkg/agent/context.go::BuildSystemPrompt` | **modifies** | Takes **no** workspace id. Emits the `# Skills` menu; text instructs `read_file` |
| `pkg/agent/context.go::BuildSystemPromptWithCache` | **modifies** | Per-`ContextBuilder` cache, mtime-invalidated via `skillRoots`/`sourcePaths` |
| `pkg/agent/context.go::buildActiveSkillsContext` | **calls** | Renders `# Active Skills`; kept, trigger changes |
| `pkg/agent/context.go::ResolveSkillName` | **modifies** | Resolves slug or display name → slug; applies `skillAllowed`; must become shelf-aware |
| `pkg/agent/context.go::ListSkillNames` | **calls** | Allowlist-filtered slug list |
| `pkg/agent/loop.go::activeSkillNames` | **modifies** | Unions `AgentInstance.SkillsFilter` + `opts.ForcedSkills`. **The union with `SkillsFilter` is deleted** |
| `pkg/agent/loop.go::applyExplicitSkillCommand` | **modifies** | `/<slug>` one-shot → `opts.ForcedSkills` |
| `pkg/skills/loader.go::BuildSkillsSummaryFunc` | **modifies** | Menu; `maxSkillsInSummary = 20`; emits `<name>/<display_name>/<description>/<location>/<source>`; footer names `find_skills` |
| `pkg/skills/loader.go::ListSkills`, `::LoadSkill`, `::SkillRoots` | **modifies** | Three shelves: workspace → global → builtin; dedup by slug |
| `pkg/skills/loader.go::LoadSkillsForContext` | **calls** | Reads full `SKILL.md` bodies. Kept |
| `pkg/sysagent/tools/skill.go::SkillListTool.Execute` | **modifies** | Returns **every** installed skill incl. `path`, **no** allowlist filter |
| `pkg/coreagent/core.go::SeedConfig` | **modifies** | Ungated per-boot loop re-seeds `a.Skills` when empty |
| `pkg/agent/subturn.go::spawnSubTurn` | **modifies** | `cfg.SystemPrompt` → child's first user message; `snapshot` appended to it |
| `pkg/tools/delegate.go::DelegateTool.Parameters` | **modifies** | Gains `requested_skill` |
| `pkg/fspolicy/secretset.go::SecretEntriesAlways` | **modifies** | 8 entries; feeds kernel + app denies **and** `pkg/tools/shell.go`'s text guard (§13) |
| `pkg/sandbox/derive_from_fspolicy.go::DeriveKernelPolicy` | **modifies** | Single authored-policy → kernel-policy seam; has the turn's mounts via `FSPolicy.AllowedRoots` |
| `src/lib/toolVisibility.ts` | **modifies** | Hide-by-default set; `ToolSearch` forced visible on error |

### 2.3 Impact assessment (measured by direct reference count)

| Symbol modified | Risk | Prod refs | Test refs | Notes |
|---|---|---|---|---|
| `SeedConfig` | **HIGH** | 54 | 260 | Boot path for every install; 260 test references means broad blast radius on any behaviour change |
| `BuildSystemPrompt` | **HIGH** | 17 | 77 | Signature change (workspace id) touches every caller |
| `BuildSystemPromptWithCache` | **HIGH** | 6 | 37 | Cache key change; hottest path in the prompt build |
| `SecretEntriesAlways` | **HIGH** | 28 | 20 | Two independent consumers with different semantics — see §13 |
| `activeSkillNames` | MEDIUM | 6 | 0 | **Zero test references today** — behaviour change is currently unguarded |
| `skillAllowed` | MEDIUM | 5 | 0 | **Zero test references today** — the flipped default is currently unguarded |
| `ResolveSkillName` | MEDIUM | 5 | 5 | Shared by `/<slug>` and D9 |
| `ForcedSkills` | LOW | 4 | 14 | Reused unchanged by D9 |
| `WithSkillAllowlist` | LOW | 4 | 5 | Single construction site |

**Flagged:** `skillAllowed` and `activeSkillNames` — the two symbols carrying this ADR's central
security semantics — have **zero** test references today. That is why §9's T1/T2 groups are
mandatory rather than nice-to-have.

### 2.4 Relevant execution flows

| Flow | Relevance |
|---|---|
| Turn assembly (`BuildMessages` → `BuildSystemPromptWithCache` + `buildDynamicContext` + `buildActiveSkillsContext`) | Where the menu, the reminder, and any active skill land |
| Boot seeding (`SeedConfig` → per-agent config) | D5.1 |
| Sub-turn dispatch (`delegate` → `spawnSubTurn` → child `processOptions`) | D9 |
| Per-turn FS policy (`ResolveTurnFSPolicy` → `DeriveKernelPolicy` → sandbox backend) | D10 |
| Slash-command dispatch (`applyExplicitSkillCommand`) | D4 door 4 |

### 2.5 Cluster placement

Spans four areas — **skills** (`pkg/skills`), **agent context/loop** (`pkg/agent`), **sandbox/policy**
(`pkg/fspolicy`, `pkg/sandbox`, `pkg/tools`), and **SPA chat rendering** (`src/lib`). The
sandbox/policy leg is the one that crosses an accepted-ADR boundary (ADR-062) and carries the
platform-specific risk.

### 2.5a Glossary — "shelf" (OBS-002)

Used throughout with one meaning: **the source a slug resolves from**. The closed set of values,
which is what `shelf-aware` (FR-065) and the audit field (FR-071a) both refer to:

| Shelf value | Location | Grant instrument |
|---|---|---|
| `project:<mount-name>` | a mount's `.omnipus/skills/` or `.claude/skills/` | the mount (D4.1) |
| `global` | `$OMNIPUS_HOME/skills` | the per-agent grant list |
| `builtin` | the 4 embedded skills | the per-agent grant list |

Precedence for resolution is `project` → `global` → `builtin`, **except** where the agent's own
grant list already names a same-slug `global`/`builtin` skill, in which case **that registry skill
wins over the project skill** (D4.2/FR-028) — the one case the general rule inverts. A *dangling*
grant (naming a skill no longer installed) does not trigger the inversion and the project skill
resolves normally (FR-028a). The loader's
existing three roots call the first one `workspace`; **`project:<mount>` is that root re-pointed**
(D6), and the spec uses the new name throughout to avoid colliding with the Omnipus *workspace*
concept, which is a different thing entirely.

### 2.6 Reference patterns

`docs/reference/go-implementation/` **does not exist in this repository** — the plan-spec skill's
reference library is from a different project. N/A. The in-repo equivalent is ADR-071's shipped
`ToolSearch` mechanism, which this feature deliberately mirrors (D1).

---

## 3. User stories & acceptance criteria

### US-1 — An agent picks up a skill when the task needs it (P0)

An agent carrying a menu of one-line descriptions recognises that the current task matches one, and
loads that skill's full instructions for this turn only. Today it carries every granted skill's full
text into every message, whether relevant or not, paying for all of them on every request.

**Why this priority**: This is the feature. Every other story either enables it or protects it.

**Independent test**: Grant an agent three skills; send an unrelated message; confirm no skill body
is in the request. Send a matching message; confirm exactly one skill body arrives after a `Skill`
call.

**Acceptance scenarios**:
1. **Given** an agent granted three skills and a message unrelated to any of them, **When** the turn
   is assembled, **Then** no skill instructions are present and the menu lists all three.
2. **Given** the same agent, **When** it calls the skill tool naming a granted slug, **Then** that
   skill's instructions become available for the current turn and no other skill's do.
3. **Given** the same agent, **When** the next message arrives, **Then** the previously loaded skill
   is no longer present unless activated again.
4. **Given** an agent granted more skills than the old 20-entry cap, **When** a turn is assembled,
   **Then** every granted skill is listed — the menu is not truncated and carries no footer.

### US-2 — The operator's grant decides what an agent may use (P0)

The per-agent grant list stops being an always-load list and becomes the permission gate it was
always meant to be, enforced everywhere a skill can be reached or named.

**Why this priority**: Issue #663's stated defect; also the precondition for grants being free.

**Independent test**: Grant agent A skill X only. Confirm A cannot load, search-discover, list, or
slash-activate skill Y through any surface.

**Acceptance scenarios**:
1. **Given** an agent not granted a skill, **When** it names that skill to the skill tool, **Then**
   the request is refused with a message identifying the skill and the reason.
2. **Given** the same agent, **When** it searches with a query matching that skill, **Then** the
   skill's name and description do not appear in the results.
3. **Given** the same agent, **When** a human types the slash command for that skill, **Then** it is
   not activated.
4. **Given** the same agent, **When** it lists installed skills, **Then** the ungranted skill is
   absent and no filesystem location is returned for any entry.

### US-3 — An agent with no grants has no skills (P0)

Absence of a grant list means none, matching the published wire contract, and that choice survives a
restart.

**Why this priority**: The contract already says this; the code says the opposite, and a second
piece of code silently reverts an operator who chooses it.

**Independent test**: Set an agent's grant list to empty, restart, confirm it is still empty and the
agent can load nothing.

**Acceptance scenarios**:
1. **Given** an agent whose configuration carries no grant list, **When** it attempts to load any
   skill, **Then** the request is refused.
2. **Given** an agent whose grant list is an empty list, **When** it attempts to load any skill,
   **Then** the request is refused — identically to the absent case.
3. **Given** a core-roster agent whose grant list the operator has deliberately emptied, **When** the
   gateway restarts, **Then** the list is still empty and no grants have been restored.
4. **Given** a first-ever boot with no agents configured, **When** seeding runs, **Then** the core
   roster receives its default grants.

### US-4 — A mounted code project brings its own skills (P1)

An operator points Omnipus at a repository checkout; the agents working in that workspace can use
that project's own skills, with no per-slug configuration and no setting to discover.

**Why this priority**: Founder requirement three; high value, but US-1/2/3 must land first.

**Independent test**: Mount a folder containing a project skill; confirm an agent in that workspace
sees and can load it, and the same agent in another workspace cannot.

**Acceptance scenarios**:
1. **Given** a workspace with a mount containing a recognised skills directory, **When** an agent in
   that workspace assembles a turn, **Then** the project's skills appear in its menu regardless of
   its grant list.
2. **Given** the same agent acting in a different workspace, **When** it assembles a turn, **Then**
   none of those project skills appear.
3. **Given** a workspace whose mounts contain no skills directory, **When** a turn is assembled,
   **Then** nothing changes and no warning is produced.
4. **Given** a project skill whose slug matches a skill the agent is granted from the registry,
   **When** the agent loads that slug, **Then** the granted registry skill is used and the collision
   is recorded.
5. **Given** two mounts carrying the same project-skill slug, **When** the menu is built, **Then**
   one wins deterministically by mount name and the collision is recorded.
6. **Given** an agent in that workspace, **When** it edits or removes a project skill, **Then** the
   project's own file changes and no copy is made in the central library.
7. **Given** the same edit, **When** it completes, **Then** the write is recorded with its shelf and
   resolved path.

### US-5 — A mounted project's instructions apply while working in it (P1)

A repository's `CLAUDE.md` or `AGENTS.md` reaches the agent as instructions for the whole turn,
alongside the workspace's own Project Instructions.

**Why this priority**: Completes founder requirement three; independent of the skills half.

**Independent test**: Mount a repo with a root `CLAUDE.md`; confirm its content is present in every
turn in that workspace, labelled and ordered after the workspace's own instructions.

**Acceptance scenarios**:
1. **Given** a mount with a root instruction file, **When** any turn in that workspace is assembled,
   **Then** its content is present, labelled with the mount name, after the workspace's own.
2. **Given** a mount with both file names present, **When** instructions are composed, **Then** only
   one is used, deterministically.
3. **Given** mounts whose combined instructions exceed the size budget, **When** composed, **Then**
   the content is truncated at the budget and the truncation is visibly marked.
4. **Given** a mount with a skills directory but no instruction file, **When** a turn is assembled,
   **Then** skills contribute and no instruction block is added.

### US-6 — A parent can delegate work that reliably runs a specific skill (P1)

A delegating agent can request that the child run a named skill, and either the child starts with it
loaded or the delegation is refused — never silently ignored.

**Why this priority**: Founder's explicit follow-up; the "hopeful" alternative already exists.

**Independent test**: Delegate with a requested skill to a child that holds it (loaded), and to one
that does not (refused, named).

**Acceptance scenarios**:
1. **Given** a child granted the requested skill, **When** the parent delegates requesting it,
   **Then** the child's first turn begins with that skill's instructions loaded.
2. **Given** a child not granted it, **When** the parent delegates requesting it, **Then** the
   delegation fails immediately with an error naming the child and the skill.
3. **Given** a slug that resolves to nothing, **When** the parent delegates requesting it, **Then**
   the delegation fails with an error distinguishable from the refusal case.
4. **Given** a parent that does not itself hold the requested skill but whose child does, **When**
   it delegates requesting it, **Then** the outcome is the same as if the parent held it.

### US-7 — A grant is a real boundary on POSIX, best-effort on Windows (P1)

Skill file content is reachable only through the skill tool, so an agent that knows a slug cannot
simply read the file. **The strength of that boundary is platform-dependent and the difference is
stated, not glossed** (ADR D10.2): on Linux and macOS it holds against a spawned shell child too; on
Windows there is no sandbox backend at all — `FallbackBackend.ApplyToCmd` appends two environment
variables and restricts nothing — so the in-process file tools are gated and a `bash` child is not.
This mirrors how `pkg/entity`'s cross-process guarantee is already documented as POSIX-only.

**Why this priority**: The founder chose the strongest option; it is what makes US-2 mean something.

**Independent test**: With a registry skill on disk, confirm the file tool refuses to read it while
the skill tool still loads it.

**Acceptance scenarios**:
1. **Given** a registry skill on disk, **When** an agent reads its path with the file tool, **Then**
   the read is refused.
2. **Given** the same skill, **When** the agent loads it with the skill tool and holds the grant,
   **Then** the content is delivered.
3. **Given** a mount containing both a project skill and ordinary source files, **When** the agent
   reads an ordinary file, **Then** it succeeds; **When** it reads under the skills directory,
   **Then** it is refused.
4. **Given** a mount with a root instruction file, **When** the agent reads that file directly,
   **Then** it succeeds — instruction files are not gated.

### US-8 — The menu is correct for the workspace the agent is working in (P0)

An agent moving between workspaces sees the right menu in each, and the cache backing it neither
grows without bound nor serves a stale membership.

**Why this priority**: US-4 is incorrect without it; it is a stated ADR prerequisite.

**Independent test**: Same agent, two workspaces with different mounts; confirm each turn shows that
workspace's menu.

**Acceptance scenarios**:
1. **Given** an agent on two workspaces with different project skills, **When** it acts in each,
   **Then** each turn's menu reflects that workspace only.
2. **Given** an agent removed from a workspace, **When** it next acts, **Then** that workspace's
   menu is not served from cache.
3. **Given** a mount deleted from a workspace, **When** a turn is next assembled there, **Then** its
   project skills are gone from the menu.
4. **Given** an agent acting across more workspaces than the cache bound, **When** it continues,
   **Then** memory stays bounded and every turn still shows a correct menu.

### US-9 — Skill descriptions are written so the model can match them (P2)

Descriptions state when to use a skill, in matching terms, because that line is the only thing the
model sees before choosing.

**Why this priority**: The largest determinant of real-world reliability, but it is convention plus
validation, not mechanism.

**Independent test**: Attempt to author a skill whose description merely restates its name; confirm
it is rejected with guidance.

**Acceptance scenarios**:
1. **Given** an authoring request with an empty description, **When** submitted, **Then** it is
   rejected naming the description.
2. **Given** a description that only restates the skill's name, **When** submitted, **Then** it is
   rejected with guidance on trigger phrasing.
3. **Given** a description over the length limit, **When** submitted, **Then** it is rejected stating
   the limit.

### US-10 — Checking the menu is a silent habit, not narration (P1)

The agent is reminded per request to consider its skills, and neither the reminder nor a successful
load becomes visible chatter.

**Why this priority**: Directly determines whether D1 fires in practice, and whether it is annoying.

**Independent test**: Observe a chat turn where a skill is loaded; confirm no "let me check my
skills" narration and no visible tool card, but the call is in the transcript.

**Acceptance scenarios**:
1. **Given** any assembled turn, **When** the per-request context is built, **Then** a reminder
   within the token budget is present.
2. **Given** a successful skill load, **When** the thread renders, **Then** the call is hidden.
3. **Given** a refused skill load, **When** the thread renders, **Then** the failure is shown.
4. **Given** verbose chat enabled, **When** the thread renders, **Then** all skill calls are shown.

### US-11 — An operator can tell whether skills are actually being used (P1)

Every skill call is recorded, and the Skills view shows when each skill was last used, so a granted
but never-firing skill is visible.

**Why this priority**: The ADR names "a skill silently never fires" as its biggest accepted risk;
without this it is undetectable.

**Independent test**: Load one skill, deny another; confirm both appear in the audit trail with
outcomes, and the loaded one's last-used timestamp updates.

**Acceptance scenarios**:
1. **Given** a successful load, **When** it completes, **Then** an audit record carries the slug,
   mode, outcome, shelf, agent and workspace.
2. **Given** a refused load, **When** it completes, **Then** an audit record with a denial outcome
   exists even though the call is hidden from the thread.
3. **Given** a granted skill never invoked, **When** the operator views the Skills screen, **Then**
   it shows no last-used time.

### 3.1 Edge cases

- **Slug that exists in two shelves, agent granted neither** → not offered, not loadable (US-2).
- **Mount added mid-conversation** → next turn's menu includes it; the current turn is unaffected.
- **Mount removed while a skill from it is loaded in the current turn** → the turn completes; the
  next turn no longer offers it.
- **Workspace with no mounts at all** → identical behaviour to today plus the menu/reminder.
- **Agent granted a slug that is no longer installed** → menu omits it; a direct load returns
  not-found, distinct from refused.
- **Project skill directory exists but contains no valid skill** → contributes nothing, no warning.
- **A mount contributing more skills than the old cap** → every skill is still listed, granted and
  project alike; the per-message menu grows accordingly (ADR D1.1, accepted).
- **Two mounts, same slug, same content** → still deterministic by mount name; still recorded.
- **Skill body larger than the context budget** → existing context-paging applies unchanged.
- **Delegation to self with a requested skill** → the delegating agent's own grant is the receiver's
  grant; behaves like any other receiver.
- **Unicode / mixed-case slug in a grant list** → matched case-insensitively, as today.
- **Instruction file that is exactly at the byte budget** → included whole, not marked truncated.

---

## 4. Behavioral contract

**Primary flows**
- When a turn is assembled, the system presents a menu of the skills the acting agent may use in the
  current workspace, and no skill instructions.
- When an agent names a skill it may use, the system makes that skill's instructions available for
  the current turn only.
- When an agent searches by description text, the system returns only skills it may use.
- When a workspace has a mount containing a recognised skills directory, the system offers that
  project's skills to agents acting in that workspace.
- When a workspace has a mount containing a root instruction file, the system includes its content
  in every turn in that workspace, labelled and ordered after the workspace's own instructions.
- When a parent delegates requesting a skill the child may use, the child's first turn begins with
  that skill loaded.

**Error flows**
- When an agent names a skill it may not use, the system refuses and says which skill and why.
- When an agent names a slug that resolves to nothing, the system reports not-found, distinguishably
  from a refusal.
- When a delegation requests a skill the child may not use, the system refuses the delegation before
  the child starts, naming the child and the skill.
- When an agent reads a skill file directly, the system refuses the read.

**Boundary conditions**
- When an agent has no grant list, the system treats it as granting nothing.
- When the available catalogue is large, the system lists all of it; no truncation and no footer.
- When mounted instructions exceed the size budget, the system truncates at the budget and marks it.
- When two sources offer the same slug, the system resolves deterministically and records it.
- When an agent acts across more workspaces than the cache bound, the system evicts least-recently
  used entries and still serves a correct menu.

---

## 5. Explicit non-behaviors & safeguards

### 5.1 Qualitative prohibitions

- The system must not load any skill into a turn that was not explicitly activated for **that turn**,
  because the whole defect being fixed is unconditional loading (#663 AC-2).
- The system must not add an "always-on skill" marker, because an always-on skill is an instruction
  and would rebuild the problem being fixed (ADR-072 OQ3).
- The system must not let a parent agent supply a skill's **content** to a child, only its name,
  because agent-level settings come from the target (ADR-032).
- The system must not consult the parent's grant list when resolving a delegated skill request.
- The system must not silently ignore a delegation's skill request under any circumstance.
- The system must not build a unified "is this a project folder" heuristic — in particular must not
  test for a version-control directory — because the two mechanisms trigger independently (D6).
- The system must not sniff file contents to decide whether a mounted file is a skill; location is
  the sole criterion (D6).
- The system must not let a project skill replace a slug the agent holds from the registry (D4.2).
- The system must not narrate skill consideration to the user, because it is a habit, not a step.
- The system must not restore a grant list the operator has deliberately emptied (D5.1).
- The system must not return filesystem locations for skills from any agent-facing surface, because
  the location is what makes a direct read possible.
- The system must not gate reads of a mounted project's instruction file, because its content is
  already injected every turn and the file is ordinary project material (D10).
- The system must not extend the grant list to the authoring verbs; those stay governed by tool
  policy (ADR-072 N2).
- The system must not present a mounted project's skills as reviewed, sandboxed or vetted content.
  **What an agent trusts when a project skill loads, stated plainly** (MAJ-004): a `SKILL.md` is not
  passive data — once loaded it is *literal instructions in the agent's context*. A registry skill
  earns that position by an operator granting its slug individually. A project skill earns it by
  directory presence alone: the instant a mount contains a recognised skills directory, every
  `SKILL.md` beneath it is loadable and menu-listed for every agent in that workspace, with no
  per-skill review step. So mounting a folder — including, per Evaluation Scenario H2, a cloned
  repository nobody in the organisation wrote — hands every contributor to that repository, and
  anything that reached it through a dependency or a merged pull request, **a standing channel to
  inject instructions into every agent working in that workspace**. This is accepted (ADR §6.3:
  mounting *is* the trust decision, and it already grants write access, which is strictly more) but
  it is accepted **explicitly**, on the same terms as the Windows read gate and the enumeration
  oracle: every other accepted risk in this spec earns that status by being written down, and this
  one now does too.
- The system must not conceal, at the load and delegate doors, whether an unusable slug is *unknown*
  or merely *ungranted* — **and this is an accepted disclosure, stated rather than overlooked**
  (MIN-003). It lets an ungranted agent probe slugs and learn which are installed, which FR-022
  deliberately prevents at the *search* door. The asymmetry is intentional: search is a broad sweep
  where leaking a catalogue costs much and helps nobody, whereas a direct load or delegation names
  one slug the caller already had in mind, and the operator needs "install it" and "grant it" to be
  distinguishable to act on either. Debuggability wins at the doors where one name is already known.
- The system must not introduce a migration path for existing installs, because v0.3 is greenfield.

### 5.2 Machine-verifiable constraints

**Sizes and counts**
- The menu MUST list every currently-available skill, with no entry cap (D1.1, rev 3 — the
  original 20-entry cap named here was reversed by the founder's explicit decision and this line
  was stale; see FR-005). A mount contributing an implausible number of skills is handled at
  mount-add time by D1.2's threshold warning, not by a menu cap.
- The per-request reminder MUST be **≤240 bytes** (`len()` over the emitted string).
- Composed mounted instructions MUST be **≤262144 bytes** total across all mounts.
- A skill description MUST be non-empty and **≤1024 characters**.
- Search MUST return at most **5** results (inherited; no new limit introduced).
- The per-agent prompt-cache variant count MUST be bounded by a fixed cap with LRU eviction.

**Outcomes**
- A load of a slug the agent may not use MUST fail with a structured failure carrying a
  permission-denied classification and naming the slug.
- A load of an unknown slug MUST fail with a **distinct** not-found classification.
- A delegation naming a skill the child may not use MUST fail **before** the child's first LLM call.
- A no-skill-active turn MUST contribute **zero** skill-body tokens outside the cached prefix.
- Every skill call MUST produce exactly one audit record carrying slug, mode, outcome, shelf, agent
  id and workspace id.

**Scope boundaries**
- A project skill MUST be visible only to agents acting in the workspace carrying its mount.
- A **registry skill's instruction file** MUST be refused through the in-process file tools on every platform. Bundled sibling files MUST NOT be refused, on any shelf.
- A registry skill's **instruction file** read by a **spawned shell child** MUST be refused on Linux and macOS, and MUST NOT be claimed as refused on Windows.
- Instruction files MUST NOT be read-gated.

### 5.3 Conservative type design

No new nominal type unless it carries invariants the built-in cannot. Specifically: the requested
skill on a delegation is a plain slug string validated by the existing identifier validator; the
skill shelf is an existing string-valued source field; only the new fspolicy category (§13) warrants
its own named collection, because it carries a distinct enforcement semantic.

---

## 6. Prerequisites, stack, deployment

**Prerequisites**
- Hardware / OS: development on macOS arm64 or Linux x86_64. The shell-child read gate splits by
  platform: **T5c-linux needs a Linux host** (the child-only Landlock ruleset — the ADR §6.8 spike),
  **T5c-macos needs a macOS host** (the Seatbelt leg), and **T5c-windows** documents the known gap
  (D10.2). No single host satisfies all three; CI must run at least Linux and macOS.
- Required runtimes: Go per `go.mod`; Node for the SPA.
- Required services: none.
- Network: none for this feature (marketplace search is out of scope).
- Accounts: none.

**Development setup**
1. `cd` to the checkout.
2. `go mod download`
3. `npm install`
4. `make test` (injects `-tags goolm,stdjson`; a bare `go test ./...` will not compile)
5. Smoke: `make build`

**Expected first run**: gateway boots; core roster seeded on a fresh `$OMNIPUS_HOME`.
**Common first-run failure**: missing build tags → `build constraints exclude all Go files in
.../pkg/channels/matrix`. That is a missing tag, not a bug.

**Tech stack**

| Category | Choice | Pin | Source |
|---|---|---|---|
| Language | Go | per `go.mod` | CLAUDE.md |
| Build tags | `goolm,stdjson` | — | Makefile `GO_BUILD_TAGS` |
| Frontend | TypeScript / React 19 / Vite 6 | — | CLAUDE.md |
| Storage | File-based JSON/JSONL | — | CLAUDE.md |
| Test frameworks | `go test`, testify; vitest | — | CLAUDE.md |
| Sandbox backends | Landlock+seccomp (Linux), Seatbelt (macOS), fallback (Windows) | — | ADR-062, CLAUDE.md |

**Deployment / runtime**
- Target: single Go binary, all three variants.
- Offline: fully offline-capable.
- Resource limits: security-feature overhead <10MB (CLAUDE.md Hard Constraint #3). **This is a
  demonstration, not a citation** — rev 3 asserted it by pointing at D8, which bounds variant
  *count*, a different quantity. A menu entry costs ~302 bytes typical / ~1206 bytes at the 1024-char
  description cap, so one cached variant of a 5000-skill workspace (the case D1.2 exists for) is
  ~1.44 MB typical and ~5.75 MB worst; **eight such variants for a single agent is ~11.5 MB —
  already past the whole budget.** Constraint #3 therefore holds only because ADR **D8.1** makes the
  cache byte-aware (FR-046a/b): eviction on count *or* aggregate bytes, whichever binds first, with
  a 4 MB per-agent budget leaving headroom for every other security feature.
- Health: existing `run_doctor`.
- Logs / telemetry: existing audit trail (`pkg/audit`) plus existing structured logging.

---

## 7. Integration boundaries

### Filesystem policy engine (`pkg/fspolicy` → `pkg/sandbox`)
- **Data in**: the turn's work dir and mount list; the secret-set definitions.
- **Data out**: denied path sets for the app resolver and the kernel ruleset.
- **Contract**: whole-directory containment tests resolved by filesystem identity. **No glob or
  pattern facility exists.**
- **On failure**: a deny that cannot be expressed fails closed (skills stop loading) rather than
  silently opening.
- **Development**: real, with a temp `$OMNIPUS_HOME`; the kernel leg needs a real host per platform.
- **⚠️ Second consumer with different semantics** — see §13.

### Mount store (`pkg/workspace`)
- **Data in**: workspace id.
- **Data out**: named, realpath-resolved host paths.
- **Contract**: mounts live in the protected entity store, out of a sandboxed child's reach; names
  are unique within a workspace.
- **On failure**: a missing/unreadable mount contributes nothing; never fails the turn.
- **Development**: real, temp directories.

### Audit trail (`pkg/audit`)
- **Data in**: one record per skill call.
- **Data out**: append-only JSONL under the (denied) system directory.
- **On failure**: logging failure must not fail the turn, but must itself be logged.
- **Development**: real.

### Anthropic prompt caching (via `pkg/providers`)
- **Data in**: content blocks, one marked as the cache breakpoint.
- **Data out**: cache hit/miss economics.
- **Contract**: content after the breakpoint is not cached. The menu goes inside; the reminder
  outside, deliberately.
- **On failure**: no functional impact, cost only.
- **Development**: measured via existing `static_chars`/`total_chars` debug logging.

---

## 8. BDD scenarios

### Feature: On-demand skill activation

#### Background
- **Given** a fresh installation with the default core roster
- **And** an agent whose grant list is explicitly configured

#### Scenario: Agent carries no skill instructions on an unrelated turn
**Traces to**: US-1, AS-1 · **Category**: Happy Path
- **Given** an agent granted three installed skills
- **When** a message unrelated to any of them is processed
- **Then** the assembled request contains no skill instructions
- **And** the menu lists all three with their descriptions

#### Scenario: Agent loads exactly the skill it names
**Traces to**: US-1, AS-2 · **Category**: Happy Path
- **Given** an agent granted three installed skills
- **When** it invokes the skill tool naming one granted slug
- **Then** that skill's instructions are available for the current turn
- **But** the other two skills' instructions are not

#### Scenario: A loaded skill does not persist into the next message
**Traces to**: US-1, AS-3 · **Category**: Happy Path
- **Given** an agent that loaded a skill during the previous message
- **When** a new message is processed
- **Then** no skill instructions are present

#### Scenario: Large catalogue fully listed
**Traces to**: US-1, AS-4 · **Category**: Alternate Path
- **Given** an agent granted 25 installed skills
- **When** a turn is assembled
- **Then** all 25 appear in the menu
- **But** no truncation footer is present

#### Scenario: Granted skill survives a large mount
**Traces to**: US-1, AS-4 · **Category**: Edge Case
- **Given** an agent granted 3 registry skills
- **And** a mount in the same workspace contributing 30 project skills
- **When** a turn is assembled
- **Then** all 3 granted registry skills appear in the menu alongside all 30 project skills

#### Scenario: Search returns at most the inherited result bound
**Traces to**: US-1, AS-4 · **Category**: Edge Case
- **Given** an agent granted 25 installed skills whose descriptions all match a query
- **When** it searches with that query
- **Then** at most 5 results are returned

### Feature: Grant enforcement

#### Scenario: Loading an ungranted skill is refused by name
**Traces to**: US-2, AS-1 · **Category**: Error Path
- **Given** an agent granted only skill X while skill Y is installed
- **When** it invokes the skill tool naming Y
- **Then** the call fails with a permission-denied classification naming Y

#### Scenario: Search results do not disclose an ungranted skill
**Traces to**: US-2, AS-2 · **Category**: Error Path
- **Given** an agent granted only skill X while skill Y is installed
- **When** it searches with a query strongly matching Y's description
- **Then** neither Y's name nor its description appears in the results

#### Scenario: A human cannot slash-activate an ungranted skill
**Traces to**: US-2, AS-3 · **Category**: Error Path
- **Given** an agent granted only skill X while skill Y is installed
- **When** a user submits the slash command for Y
- **Then** Y is not activated for the turn

#### Scenario: Listing installed skills is grant-filtered and location-free
**Traces to**: US-2, AS-4 · **Category**: Error Path
- **Given** an agent granted only skill X while skill Y is installed
- **When** it lists installed skills
- **Then** Y is absent
- **And** no entry carries a filesystem location

#### Scenario Outline: Every door refuses an ungranted slug
**Traces to**: US-2, AS-1 · **Category**: Error Path
- **Given** an agent not granted `<slug>`
- **When** the slug is presented at `<door>`
- **Then** the outcome is `<result>`

**Examples**:

| door | slug | result |
|---|---|---|
| skill tool load | ungranted-a | refused, named |
| skill tool search | ungranted-a | absent from results |
| menu build | ungranted-a | absent from menu |
| slash command | ungranted-a | not activated |
| list installed | ungranted-a | absent from listing |

### Feature: Default-none grants

#### Scenario: An agent with no grant list can load nothing
**Traces to**: US-3, AS-1 · **Category**: Error Path
- **Given** an agent whose configuration carries no grant list
- **When** it invokes the skill tool naming any installed skill
- **Then** the call is refused

#### Scenario: An empty grant list behaves identically to an absent one
**Traces to**: US-3, AS-2 · **Category**: Edge Case
- **Given** an agent whose grant list is an empty list
- **When** it invokes the skill tool naming any installed skill
- **Then** the call is refused with the same classification as the absent-list case

#### Scenario: A deliberately emptied grant list survives a restart
**Traces to**: US-3, AS-3 · **Category**: Error Path
- **Given** a core-roster agent whose grant list the operator has emptied
- **When** the gateway restarts and seeding runs
- **Then** the agent's grant list is still empty

#### Scenario: A first-ever boot seeds the core roster's grants
**Traces to**: US-3, AS-4 · **Category**: Happy Path
- **Given** an installation with no agents configured
- **When** seeding runs
- **Then** each core agent receives its default grants

### Feature: Project skills from mounts

#### Scenario: A mounted project's skills appear without any grant
**Traces to**: US-4, AS-1 · **Category**: Happy Path
- **Given** a workspace with a mount containing a recognised skills directory holding one skill
- **And** an agent in that workspace with an empty grant list
- **When** a turn is assembled
- **Then** that project skill appears in the menu
- **And** the agent can load it

#### Scenario: Project skills do not follow the agent to another workspace
**Traces to**: US-4, AS-2 · **Category**: Edge Case
- **Given** an agent that can use a project skill in workspace A
- **When** the same agent assembles a turn in workspace B, which has no such mount
- **Then** that project skill does not appear and cannot be loaded

#### Scenario: A mount with no skills directory changes nothing
**Traces to**: US-4, AS-3 · **Category**: Happy Path
- **Given** a workspace with a mount containing only ordinary source files
- **When** a turn is assembled
- **Then** the menu is exactly what the grant list alone would produce
- **And** no warning is emitted

#### Scenario: A dangling registry grant does not shadow a present project skill
**Traces to**: US-4, AS-4 · **Category**: Edge Case
- **Given** an agent whose grant list names registry slug `deploy`
- **And** `deploy` has since been uninstalled from the central library
- **And** a mount in the agent's workspace carries a project skill also called `deploy`
- **When** the agent loads `deploy`
- **Then** the project skill's content is delivered
- **But** no not-found error is returned

#### Scenario: A project skill cannot shadow a granted registry slug
**Traces to**: US-4, AS-4 · **Category**: Error Path
- **Given** an agent granted registry skill `release-notes`
- **And** a mount containing a project skill with the same slug and different content
- **When** the agent loads that slug
- **Then** the registry skill's content is delivered
- **And** the collision is recorded with both locations

#### Scenario: Two mounts with the same slug resolve by mount name
**Traces to**: US-4, AS-5 · **Category**: Edge Case
- **Given** a workspace with mounts `alpha` and `beta` each carrying project skill `deploy`
- **When** the menu is assembled
- **Then** the entry from `alpha` is used
- **And** the collision is recorded naming both mounts

#### Scenario: A mount's first skills directory discloses what it grants
**Traces to**: US-4, AS-1 · **Category**: Happy Path
- **Given** a folder containing a recognised skills directory with three skills
- **When** the operator creates a mount for it
- **Then** the disclosure states that those skills become auto-loadable agent instructions
- **And** it appears even though the count is far below the warning threshold

#### Scenario: A mount contributing an implausible number of skills warns at creation
**Traces to**: US-4, AS-1 · **Category**: Edge Case
- **Given** a folder whose recognised skills directory holds more skills than the configured threshold
- **When** the operator creates a mount for it
- **Then** a warning states the count and its per-turn consequence
- **And** the mount is still created
- **And** the resulting menu lists every one of those skills, untruncated

#### Scenario: Editing a project skill writes into the project
**Traces to**: US-4, AS-6 · **Category**: Happy Path
- **Given** a workspace with a mount containing project skill `deploy`
- **When** an agent in that workspace edits `deploy` through the authoring tools
- **Then** the project's own skill file carries the new content
- **But** no copy of `deploy` is created in the central library

#### Scenario: Removing a project skill deletes the project's file
**Traces to**: US-4, AS-6 · **Category**: Alternate Path
- **Given** a workspace with a mount containing project skill `deploy`
- **When** an agent in that workspace removes `deploy`
- **Then** the project's own skill file is gone
- **And** the removal is confined to that mount

#### Scenario: A project-skill write is audited
**Traces to**: US-4, AS-7 · **Category**: Happy Path
- **Given** an agent editing a project skill
- **When** the write completes
- **Then** an audit record carries the shelf and the resolved path

#### Scenario: An agent can read back through the tool what it just wrote
**Traces to**: US-4, AS-6 · **Category**: Edge Case
- **Given** an agent that has just written a project skill in its workspace
- **When** it loads that skill with the skill tool
- **Then** the content it wrote is returned
- **But** reading the same file with the file tool is still refused

#### Scenario Outline: Project-skill discovery triggers only on the recognised directories
**Traces to**: US-4, AS-1 · **Category**: Edge Case
- **Given** a mount whose layout is `<layout>`
- **When** project skills are discovered
- **Then** the result is `<result>`

**Examples**:

| layout | result |
|---|---|
| `.claude/skills/x/SKILL.md` | x discovered |
| `.omnipus/skills/x/SKILL.md` | x discovered |
| `SKILL.md` at repo root | nothing discovered |
| `docs/SKILL.md` | nothing discovered |
| `.git/` present, no skills dir | nothing discovered |
| `.claude/skills/` present but empty | nothing discovered |

### Feature: Project instructions

#### Scenario: A mounted project's instructions reach every turn
**Traces to**: US-5, AS-1 · **Category**: Happy Path
- **Given** a workspace with mount `acme` whose root carries an instruction file
- **When** any turn in that workspace is assembled
- **Then** its content is present, labelled with `acme`, after the workspace's own instructions

#### Scenario: Only one instruction file per mount is used
**Traces to**: US-5, AS-2 · **Category**: Edge Case
- **Given** a mount whose root carries both recognised instruction file names
- **When** instructions are composed
- **Then** exactly one file's content is present, chosen deterministically

#### Scenario: Oversized composed instructions truncate visibly
**Traces to**: US-5, AS-3 · **Category**: Error Path
- **Given** mounts whose combined instruction content exceeds the byte budget
- **When** instructions are composed
- **Then** the content is cut at the budget
- **And** a visible marker states that truncation occurred

#### Scenario: Skills without instructions contribute independently
**Traces to**: US-5, AS-4 · **Category**: Alternate Path
- **Given** a mount with a skills directory and no instruction file
- **When** a turn is assembled
- **Then** its project skills appear in the menu
- **And** no instruction block is added for that mount

### Feature: Delegating with a requested skill

#### Scenario: A permitted requested skill loads in the child's first turn
**Traces to**: US-6, AS-1 · **Category**: Happy Path
- **Given** a child agent granted skill `release-notes`
- **When** the parent delegates a task requesting `release-notes`
- **Then** the child's first turn begins with that skill's instructions loaded
- **And** the delegation result names the skill that was loaded

#### Scenario: A requested skill the child may not use refuses the delegation
**Traces to**: US-6, AS-2 · **Category**: Error Path
- **Given** a child agent not granted skill `release-notes`
- **When** the parent delegates a task requesting `release-notes`
- **Then** the delegation fails before the child's first model call
- **And** the error names both the child and the skill

#### Scenario: An unresolvable requested slug is distinguishable from a refusal
**Traces to**: US-6, AS-3 · **Category**: Error Path
- **Given** a slug that names no installed skill
- **When** the parent delegates requesting it
- **Then** the delegation fails with a not-found classification distinct from permission-denied

#### Scenario: The parent's own grant is irrelevant to the outcome
**Traces to**: US-6, AS-4 · **Category**: Alternate Path
- **Given** a parent not granted `release-notes` and a child that is
- **When** the parent delegates requesting `release-notes`
- **Then** the child's first turn begins with that skill loaded

#### Scenario: Naming a skill in the task text alone guarantees nothing
**Traces to**: US-6, AS-1 · **Category**: Edge Case
- **Given** a child agent granted `release-notes`
- **When** the parent delegates a task whose text mentions the skill but does not request it
- **Then** the child's first turn begins with no skill loaded

### Feature: Gated skill reads

#### Scenario: A registry skill file cannot be read with the file tool
**Traces to**: US-7, AS-1 · **Category**: Error Path
- **Given** an installed registry skill on disk
- **When** an agent reads its path with the file tool
- **Then** the read is refused

#### Scenario: The skill tool still delivers the same content
**Traces to**: US-7, AS-2 · **Category**: Happy Path
- **Given** an agent granted that skill
- **When** it loads the skill with the skill tool
- **Then** the instructions are delivered in full

#### Scenario: Ordinary files in a mount stay readable
**Traces to**: US-7, AS-3 · **Category**: Happy Path
- **Given** a mount containing both a project skill and ordinary source files
- **When** the agent reads an ordinary source file
- **Then** the read succeeds

#### Scenario: Project skill files inside a mount stay readable
**Traces to**: US-7, AS-3 · **Category**: Alternate Path
- **Given** a mount containing a project skill with a bundled reference file
- **When** the agent reads either the skill's instruction file or its bundled sibling
- **Then** both reads succeed
- **And** no gate applies, because the mount already grants the skill

#### Scenario: A skill's bundled helper files stay reachable
**Traces to**: US-7, AS-2 · **Category**: Happy Path
- **Given** a registry skill whose directory holds a bundled script and a reference document
- **And** whose instructions tell the agent to read the reference and run the script
- **When** the agent follows those instructions
- **Then** it can read the reference document
- **And** it can execute the bundled script
- **But** reading that skill's own instruction file with the file tool is still refused

#### Scenario: A mounted instruction file remains readable
**Traces to**: US-7, AS-4 · **Category**: Alternate Path
- **Given** a mount whose root carries an instruction file
- **When** the agent reads that file directly
- **Then** the read succeeds

#### Scenario: Windows refuses the file tool but does not confine a shell child
**Traces to**: US-7, AS-1 · **Category**: Edge Case
- **Given** a Windows installation with the read gate active
- **When** an agent reads a registry skill file with the file tool
- **Then** the read is refused
- **But** a shell child reading the same path succeeds, and this is a known documented limitation
- **And** the boot log carries an operator-visible warning naming it

#### Scenario: A symlinked skill file pointing outside its mount is refused
**Traces to**: US-4, AS-1 · **Category**: Edge Case
- **Given** a mount whose skills directory is legitimate and in-mount
- **And** a `SKILL.md` inside it that is a symlink to a path outside the mount
- **When** project skills are discovered
- **Then** that skill is not discovered
- **And** reading it through the skill tool is refused

#### Scenario: A shell child cannot read a skill file where a sandbox backend exists
**Traces to**: US-7, AS-1 · **Category**: Edge Case
- **Given** a platform with a kernel sandbox backend
- **When** a shell child attempts to read a registry skill file
- **Then** the read is refused
- **But** the skill tool continues to load skills successfully in the same installation

### Feature: Workspace-correct menu

#### Scenario: The menu differs per workspace for the same agent
**Traces to**: US-8, AS-1 · **Category**: Happy Path
- **Given** an agent on workspaces A and B with different mounted project skills
- **When** it assembles a turn in each
- **Then** each turn's menu contains only that workspace's project skills

#### Scenario: Removing an agent from a workspace invalidates its menu
**Traces to**: US-8, AS-2 · **Category**: Error Path
- **Given** an agent with a cached menu for workspace A
- **When** the agent is removed from workspace A and then acts
- **Then** the cached menu for A is not served

#### Scenario: Deleting a mount removes its skills from the menu
**Traces to**: US-8, AS-3 · **Category**: Error Path
- **Given** a workspace whose cached menu includes a mount's project skills
- **When** that mount is deleted and a turn is assembled
- **Then** those skills are absent from the menu

#### Scenario: Cache stays within its byte budget for a very large workspace
**Traces to**: US-8, AS-4 · **Category**: Edge Case
- **Given** an agent acting across several workspaces, one carrying a mount with thousands of skills
- **When** it acts in each in turn
- **Then** the aggregate retained cache for that agent stays within the byte budget
- **And** every turn's menu still lists every skill available in its workspace

#### Scenario: An oversized single variant is rebuilt rather than cached
**Traces to**: US-8, AS-4 · **Category**: Edge Case
- **Given** a workspace whose assembled menu alone exceeds the whole per-agent byte budget
- **When** the agent acts there and then in another workspace and back
- **Then** the oversized variant is never retained
- **And** the other workspace's cached variant is still present

#### Scenario: Cache stays bounded across many workspaces
**Traces to**: US-8, AS-4 · **Category**: Edge Case
- **Given** an agent acting across more workspaces than the cache bound
- **When** it continues to act in each in turn
- **Then** the number of retained menu variants never exceeds the bound
- **And** each turn's menu is correct for its workspace

### Feature: Description authoring

#### Scenario Outline: Descriptions are validated at authoring time
**Traces to**: US-9, AS-1 · **Category**: Error Path
- **Given** an authoring request whose description is `<description>`
- **When** it is submitted
- **Then** the outcome is `<result>`

**Examples**:

| description | result |
|---|---|
| (empty) | rejected, names the description |
| whitespace only | rejected, names the description |
| exactly the skill's name | rejected, trigger guidance |
| 1024 characters | accepted |
| 1025 characters | rejected, states the limit |
| "Use when the user asks to cut a release or publish notes" | accepted |

### Feature: Silent habit and observability

#### Scenario: The per-request reminder is present and within budget
**Traces to**: US-10, AS-1 · **Category**: Happy Path
- **Given** any agent with at least one usable skill
- **When** a turn is assembled
- **Then** the per-request context carries a menu reminder within the token budget

#### Scenario: A successful skill call is hidden from the thread
**Traces to**: US-10, AS-2 · **Category**: Happy Path
- **Given** verbose chat disabled
- **When** the agent successfully loads a skill
- **Then** no tool card for that call renders in the thread

#### Scenario: A refused skill call is shown in the thread
**Traces to**: US-10, AS-3 · **Category**: Error Path
- **Given** verbose chat disabled
- **When** a skill call is refused
- **Then** the failure renders in the thread

#### Scenario: Verbose chat reveals every skill call
**Traces to**: US-10, AS-4 · **Category**: Alternate Path
- **Given** verbose chat enabled
- **When** the agent successfully loads a skill
- **Then** the call renders in the thread

#### Scenario: Every skill call produces an audit record
**Traces to**: US-11, AS-1 · **Category**: Happy Path
- **Given** an agent that loads a granted skill
- **When** the call completes
- **Then** an audit record carries the slug, mode, outcome, shelf, agent id and workspace id

#### Scenario: A hidden denial is still audited
**Traces to**: US-11, AS-2 · **Category**: Edge Case
- **Given** verbose chat disabled
- **When** a skill call is refused
- **Then** an audit record with a denial outcome exists

#### Scenario: A write through any tool is audited
**Traces to**: US-11, AS-1 · **Category**: Edge Case
- **Given** a workspace with a mount containing a project skill
- **When** an agent modifies that skill's file with the generic file-writing tool rather than the authoring tool
- **Then** an audit record for the write exists
- **And** it names the tool that performed it

#### Scenario: A never-invoked granted skill is visibly unused
**Traces to**: US-11, AS-3 · **Category**: Happy Path
- **Given** a skill granted to an agent and never invoked
- **When** the operator views the Skills screen
- **Then** the skill shows no last-used time

**Scenario count**: 61 total — 19 Happy Path, 7 Alternate Path, 16 Error Path, 19 Edge Case.
(rev 5: +1 net for ADR r6's D10.3 — a bundled-helper-files scenario added, and the project-skill
"refused" scenario re-cast as "stay readable". Counted mechanically from the `**Category**` lines.)

---

## 9. Test-driven development plan

### 9.1 Relationship to ADR-072 §9

ADR-072 §9 names 13 required tests (T1a–T6) as a **floor**. This plan keeps those IDs and expands
beneath them. Where a spec test corresponds to an ADR test, the ADR id is cited in the ID column.

### 9.2 Test hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Single package: grant predicate, menu builder, shelf resolver, seeding, fspolicy sets | Logic in isolation, no filesystem beyond temp dirs |
| Integration | Turn assembly, sub-turn dispatch, tool → policy → filesystem | Components together on a real temp `$OMNIPUS_HOME` |
| E2E | Gateway with embedded SPA; per-platform sandbox | The whole path a user or agent actually exercises |

### 9.3 Implementation order

Unit first, then integration, then E2E; within a level, foundations before dependents.

| # | Test | Level | ADR id | Traces to scenario | Verifies |
|---|---|---|---|---|---|
| 1 | `TestSkillAllowed_NilAllowlistDeniesEverything` | Unit | T1a | No grant list can load nothing | Flipped nil semantics |
| 2 | `TestSkillAllowed_EmptySliceMatchesNilSemantics` | Unit | T1b | Empty identical to absent | Absence ≡ empty |
| 3 | `TestSkillAllowed_GrantedSlugCaseInsensitive` | Unit | — | (edge: mixed case) | Existing case-folding preserved |
| 4 | `TestBuildSkillsSummary_FiltersByGrant` | Unit | T2 | Outline: every door (menu row) | Menu door |
| 5 | `TestBuildSkillsSummary_NoCapNoTruncation` | Unit | **T7a** | Large catalogue fully listed | Cap removal |
| 6 | `TestBuildSkillsSummary_GrantedSurvivesLargeMount` | Unit | **T7b** | Granted skill survives a large mount | The defect that removed the cap |
| 7 | `TestBuildSkillsSummary_OmitsLocationField` | Unit | T2 | Listing grant-filtered location-free | Location removal |
| 8 | `TestActiveSkillNames_DoesNotUnionGrantList` | Unit | — | Agent carries no skill instructions | The force-load deletion |
| 9 | `TestActiveSkillNames_ForcedSkillsStillHonored` | Unit | — | Permitted loads in child's first turn | `/<slug>` + D9 path preserved |
| 10 | `TestResolveSkillName_DeniesUngrantedRegistrySlug` | Unit | T2 | Loading ungranted refused | Resolution door |
| 11 | `TestResolveSkillName_AllowsProjectSlugWithoutGrant` | Unit | T3a | Mounted project skills appear without grant | Per-shelf model |
| 12 | `TestShelfResolution_ProjectCannotShadowGrantedRegistrySlug` | Unit | T3b | Cannot shadow granted registry slug | D4.2 carve-out |
| 13 | `TestShelfResolution_MountNameOrderWinsOnCollision` | Unit | T3c | Two mounts same slug | D4.2 ordering |
| 14 | `TestShelfResolution_CollisionIsRecorded` | Unit | — | Two mounts same slug | Collision observability |
| 15 | `TestProjectSkillDiscovery_RecognisedDirectoriesOnly` | Unit | — | Outline: discovery triggers | No heuristic, no `.git` |
| 16 | `TestProjectInstructions_SingleFilePerMountDeterministic` | Unit | — | Only one file per mount | Name precedence |
| 17 | `TestProjectInstructions_TruncationIsMarked` | Unit | — | Oversized truncate visibly | Budget boundary |
| 18 | `TestSeedConfig_FreshInstallSeedsCoreGrants` | Unit | T1c | First boot seeds | Seed still works |
| 19 | `TestSeedConfig_TwiceWithEmptiedListStaysEmpty` | Unit | **T1c** | Emptied survives restart | **CRIT-002 regression** |
| 20 | `TestSeedConfig_StillReEnforcesIdentityFields` | Unit | — | (regression) | Gating didn't disable tamper protection |
| 21 | `TestSecretEntries_SkillsDeniedForPathsNotTextGuard` | Unit | — | Registry skill cannot be read | **§13 split** |
| 22 | `TestSecretGuardPatterns_OrdinaryCommandsMentioningSkills` | Unit | — | (regression) | **§13 false-positive guard** |
| 23 | `TestSkillDescription_RejectsEmptyAndNameEcho` | Unit | — | Outline: descriptions validated | D2 enforcement |
| 24 | `TestSkillDescription_LengthBoundary` | Unit | — | Outline: descriptions validated | 1024/1025 |
| 25 | `TestReminder_WithinByteBudget` | Unit | — | Reminder present within budget | **≤240 bytes** — renamed from `…TokenBudget`; MAJ-001, no tokenizer exists |
| 26 | `TestSkillSearch_MatchListPolicyFilteredAfterRanking` | Unit | T2 | Search doesn't disclose | ADR-071 §3.2.2 shape |
| 27 | `TestSkillSearch_InheritsResultBound` | Unit | — | Search returns at most bound | MIN-003 |
| 28 | `TestSkillListTool_FiltersByGrantAndOmitsPath` | Unit | T2 | Listing grant-filtered location-free | `list_skills` door |
| 29 | `TestSlashCommand_DeniesUngrantedSlug` | Unit | T2 | Human cannot slash-activate | Slash door |
| 30 | `TestSlashCommand_AllowsProjectSlugInWorkspace` | Unit | — | (gap G3, §11) | Slash + per-shelf |
| 30a | `TestSkillWriter_ResolvesProjectSlugToItsOwnShelf` | Unit | **T7c** | Editing a project skill writes into the project | D6.1 shelf-aware authoring |
| 30b | `TestSkillWriter_ProjectWriteConfinedToMount` | Unit | **T7c** | Editing a project skill writes into the project | Traversal confinement |
| 30c | `TestRemoveSkill_ProjectSlugDeletesProjectFile` | Unit | **T7d** | Removing a project skill | D6.1 |
| 30d | `TestSecretGuard_SkillsPathDeniedNotTextGuarded` | Unit | **T7f** | (regression) | D10.1 split |
| 30e | `TestDiscovery_SymlinkedSkillFileOutsideMountRefused` | Unit | **T8d** | A symlinked skill file is refused | **MAJ-005** — file-level, distinct from dir-level |
| 30f | `TestDescription_NearMissNameRestatement` | Unit | — | Descriptions are validated at authoring time | MAJ-002 — the named comparison |
| 31 | `TestToolVisibility_SkillHiddenOnSuccessShownOnError` | Unit (vitest) | — | Successful hidden / refused shown | D3 rendering |
| 32 | `TestToolVisibility_VerboseChatRevealsSkill` | Unit (vitest) | — | Verbose reveals | Override |
| 33 | `TestTurnAssembly_NoSkillBodiesWhenNoneActive` | Integration | — | Agent carries no skill instructions | The headline claim |
| 34 | `TestTurnAssembly_LoadedSkillPresentThisTurnOnly` | Integration | — | Loads exactly / does not persist | Turn scoping |
| 35 | `TestTurnAssembly_MenuInsideCacheBoundaryReminderOutside` | Integration | — | Reminder present within budget | Cache placement |
| 36 | `TestTurnAssembly_MenuDiffersPerWorkspace` | Integration | T6 | Menu differs per workspace | D8 key |
| 37 | `TestPromptCache_EvictsOnWorkspaceMembershipChange` | Integration | T6 | Removing agent invalidates | D8 trigger 2 |
| 38 | `TestPromptCache_EvictsOnMountDeletion` | Integration | T6 | Deleting mount removes | D8 trigger 3 |
| 39 | `TestPromptCache_BoundedUnderWorkspaceChurn` | Integration | T6 | Cache stays bounded | LRU cap |
| 40 | `TestDelegate_RequestedSkillLoadsInChildFirstTurn` | Integration | T4 | Permitted loads in child's first turn | D9 granted |
| 41 | `TestDelegate_RequestedSkillDeniedByReceiverGrant` | Integration | **T4** | Not-granted refuses delegation | **D9 denied** |
| 42 | `TestDelegate_RequestedSkillUnresolvableIsDistinct` | Integration | **T4** | Unresolvable distinguishable | **D9 not-found** |
| 43 | `TestDelegate_ParentGrantDoesNotAffectOutcome` | Integration | T4 | Parent's grant irrelevant | ADR-032 conformance |
| 44 | `TestDelegate_TaskTextMentionGuaranteesNothing` | Integration | — | Naming in task text | Encourage ≠ request |
| 45 | `TestReadGate_RegistrySkillRefusedViaFileTool` | Integration | T5a | Registry skill cannot be read | D10 app layer |
| 46 | `TestReadGate_SkillToolStillLoads` | Integration | T5a | Skill tool still delivers | Loader below the boundary |
| 47 | `TestReadGate_MountOrdinaryFileReadable` | Integration | T5b | Ordinary files stay readable | Scope precision |
| 48 | `TestReadGate_MountSkillsDirNotGated` | Integration | **T9b** | Project skill files stay readable | **D10.3(a)** — Part B removed |
| 48a | `TestReadGate_BundledSiblingReadableInstructionFileRefused` | Integration | **T9a** | Bundled helper files stay reachable | **D10.3(b)** — asserted as a pair |
| 48b | `TestReadGate_BundledExecutableRunnable` | E2E | **T9c** | Bundled helper files stay reachable | Tool-output-only could never satisfy this |
| 49 | `TestReadGate_MountInstructionFileReadable` | Integration | — | Mounted instruction file readable | Deliberate non-gate |
| 50 | `TestAudit_EverySkillCallRecorded` | Integration | — | Every call audited | D3.1 |
| 51 | `TestAudit_HiddenDenialStillRecorded` | Integration | — | Hidden denial audited | Render ≠ audit |
| 51a | `TestAudit_ProjectSkillWriteRecorded` | Integration | **T7e** | A project-skill write is audited | D6.1 |
| 51b | `TestProjectSkill_WrittenThenLoadableViaTool` | Integration | — | Read back through the tool | D6.1 × D10 interaction |
| 51c | `TestAudit_WriteFileToSkillPathIsAudited` | Integration | **T8a** | A write through any tool is audited | **CRIT-002** — the `write_file` route |
| 51d | `TestAudit_RecordNamesPerformingTool` | Integration | **T8a** | A write through any tool is audited | FR-071a |
| 51e | `TestMountAdd_ThresholdWarnsAndStillCreates` | Integration | **T8c** | A mount contributing an implausible number of skills warns at creation | D1.2; menu stays uncapped |
| 51f | `TestMountAdd_FirstSkillsDirDisclosesInstructionGrant` | Integration | — | A mount's first skills directory discloses what it grants | **MAJ-004** — independent of the count threshold |
| 51g | `TestResolve_DanglingRegistryGrantDoesNotShadowProjectSkill` | Unit | — | A dangling registry grant does not shadow a present project skill | **MAJ-003** — FR-028a |
| 51h | `TestPromptCache_ByteBudgetEvicts` | Integration | — | Cache stays within its byte budget for a very large workspace | **CRIT-002** — FR-046a |
| 51i | `TestPromptCache_OversizedVariantNotCached` | Integration | — | An oversized single variant is rebuilt rather than cached | FR-046b |
| 51j | `TestAudit_ModeAndOutcomeClosedSet` | Unit | — | Every skill call produces an audit record | **MAJ-002** — asserts the closed set, not "N distinct" |
| 52 | `TestReadGate_ShellChildRefusedOnLinux` | E2E (Linux) | **T5c-linux** | Shell child cannot read | **The §6.8 spike criterion**; Linux-only |
| 53 | `TestReadGate_ShellChildRefusedOnMacOS` | E2E (macOS) | **T5c-macos** | Shell child cannot read | Seatbelt leg; macOS-only |
| 53a | `TestReadGate_WindowsFileToolRefusedShellChildNotConfined` | E2E (Windows) | **T5c-windows / T8b** | Windows: file tool refused, shell child is not | Documents the D10.2 gap; asserts the app layer holds and records that the child layer does not |
| 54 | `TestSkillsScreen_LastInvokedSurfaced` | E2E | — | Never-invoked visibly unused | D3.1 UI |

### 9.4 Test datasets

#### Dataset A — Grant list values

| # | Grant list | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| 1 | absent (field omitted) | Null | Denies every slug | No grant list can load nothing |
| 2 | `[]` | Empty | Denies every slug, same classification as #1 | Empty identical to absent |
| 3 | `["summarize"]` | Single item | Allows `summarize` only | Loading ungranted refused |
| 4 | `["SUMMARIZE"]` | Case variant | Allows `summarize` | Granted slug case-insensitive |
| 5 | `["summarize", "summarize"]` | Duplicates | Allows `summarize`; no error | Loading ungranted refused |
| 6 | `["  summarize  "]` | Whitespace padding | Allows `summarize` | Granted slug case-insensitive |
| 7 | `["", "summarize"]` | Empty element | Empty ignored; `summarize` allowed | Loading ungranted refused |
| 8 | `["not-installed"]` | Dangling reference | Menu omits; direct load → not-found | Outline: every door |
| 9 | 25 valid slugs | Past the old cap | All 25 listed; no footer | Large catalogue fully listed |
| 10 | grant names a slug that was uninstalled, and a mount carries a project skill of that slug | **Dangling × collision** | Project skill resolves; no not-found | A dangling registry grant does not shadow a present project skill |
| 11 | grant names 5000 slugs across a mounted monorepo | Pathological scale | All listed; cache evicts by byte budget | Cache stays within its byte budget for a very large workspace |

#### Dataset B — Skill slug input to the tool

| # | Input | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| 1 | `""` | Empty | Rejected, names the parameter | Loading ungranted refused |
| 2 | `"   "` | Whitespace only | Rejected, names the parameter | Loading ungranted refused |
| 3 | `"summarize"` | Happy path | Loaded | Agent loads exactly the skill it names |
| 4 | `"Summarize"` | Case variant | Loaded | Agent loads exactly the skill it names |
| 5 | `"no-such-skill"` | Not found | Not-found classification | Unresolvable distinguishable |
| 6 | `"ungranted-installed"` | Denied | Permission-denied classification | Loading ungranted refused |
| 7 | `"../../etc/passwd"` | Traversal | Rejected by identifier validation | Outline: every door |
| 8 | `"a"` × 65 | Over name length | Rejected | Outline: every door |
| 9 | `"café-résumé"` | Unicode | Rejected by identifier validation | Outline: every door |

#### Dataset C — Mount layouts for project-skill discovery

| # | Layout | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| 1 | `.claude/skills/x/SKILL.md` | Happy path | `x` discovered | Outline: discovery triggers |
| 2 | `.omnipus/skills/x/SKILL.md` | Alternate name | `x` discovered | Outline: discovery triggers |
| 3 | both, same slug | Cross-dir collision | Omnipus name wins; recorded | Outline: discovery triggers |
| 4 | `SKILL.md` at root | Wrong location | Nothing discovered | Outline: discovery triggers |
| 5 | `docs/SKILL.md` | Wrong location | Nothing discovered | Outline: discovery triggers |
| 6 | `.claude/skills/` empty | Empty dir | Nothing discovered, no warning | Mount with no skills dir |
| 7 | `.claude/skills/x/` with no `SKILL.md` | Incomplete | Nothing discovered | Mount with no skills dir |
| 8 | `.git/` present, no skills dir | Decoy | Nothing discovered | Outline: discovery triggers |
| 9 | skills dir is a symlink outside the mount | Symlink | Not followed outside the mount | Project skill files refused |
| 10 | two mounts, same slug | Cross-mount collision | Mount-name order; recorded | Two mounts same slug |
| 11 | skills dir real and in-mount, but `x/SKILL.md` is a symlink outside the mount | **Symlink, file-level** | Not discovered; read refused | A symlinked skill file is refused |
| 12 | `x/SKILL.md` is a symlink to another file *inside* the same mount | Symlink, in-bounds | Discovered normally | A symlinked skill file is refused |
| 12a | `x/SKILL.md` plus `x/reference.md` and `x/scripts/run.sh` | **Bundled siblings** | All three readable in a mount; in the registry the siblings read and the SKILL.md is refused | A skill's bundled helper files stay reachable |
| 12b | `x/scripts/run.sh` marked executable, in the registry | Bundled executable | Runnable | A skill's bundled helper files stay reachable |
| 13 | mount contributing 5000 discovered skills | Pathological scale | Mount created; operator warning surfaced; menu uncapped | A mount contributing an implausible number of skills warns at creation |
| 14 | `x/SKILL.md` being written while another agent loads the same slug | **Concurrency: torn read** | **Unspecified — locking is gap G9**; recorded so the gap is visible in the dataset, not only in prose | A mount contributing an implausible number of skills warns at creation |

#### Dataset D — Composed instruction sizes

| # | Content | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| 1 | no instruction file | Empty | No instruction block | Skills without instructions |
| 2 | 1 byte | Min | Included whole | Instructions reach every turn |
| 3 | exactly 262144 bytes | Max | Included whole, not marked | Oversized truncate visibly |
| 4 | 262145 bytes | Max+1 | Truncated at budget, marked | Oversized truncate visibly |
| 5 | three mounts summing over budget | Cumulative overflow | Cut at budget, marked | Oversized truncate visibly |
| 6 | both file names present | Duplicate | One used deterministically | Only one file per mount |
| 7 | file present but unreadable | Permission error | Contributes nothing; turn proceeds | Skills without instructions |

#### Dataset E — Delegation `requested_skill`

| # | Child grant | Requested | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|
| 1 | holds it | `release-notes` | Happy path | Loaded in child's first turn | Permitted loads |
| 2 | does not hold it | `release-notes` | Denied | Dispatch fails, names child + skill | Not-granted refuses |
| 3 | n/a | `no-such-skill` | Not found | Dispatch fails, distinct classification | Unresolvable distinguishable |
| 4 | holds it; parent does not | `release-notes` | Asymmetric | Loaded | Parent's grant irrelevant |
| 5 | project skill in shared workspace | project slug | Per-shelf | Loaded | Permitted loads |
| 6 | omitted | (absent) | Null | Ordinary delegation, no skill | Naming in task text |
| 7 | `""` | Empty | Rejected as malformed | Not-granted refuses |

#### Dataset F — Skill description authoring

| # | Description | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| 1 | `""` | Empty | Rejected, names the field | Outline: descriptions validated |
| 2 | `"   "` | Whitespace | Rejected, names the field | Outline: descriptions validated |
| 3 | skill's own name verbatim | Name echo | Rejected with trigger guidance | Outline: descriptions validated |
| 4 | `"a"` × 1024 | Max | Accepted | Outline: descriptions validated |
| 5 | `"a"` × 1025 | Max+1 | Rejected, states the limit | Outline: descriptions validated |
| 6 | `"Use when the user asks to cut a release…"` | Happy path | Accepted | Outline: descriptions validated |
| 7 | emoji + RTL text within limit | Unicode | Accepted | Outline: descriptions validated |
| 8 | `"Release Notes"` for slug `release-notes` | Near-miss: case + separator | Rejected | Descriptions are validated at authoring time |
| 9 | `"release notes."` for slug `release-notes` | Near-miss: punctuation | Rejected | Descriptions are validated at authoring time |
| 10 | `"Handles release notes"` for slug `release-notes` | Near-miss: extra words | **Accepted** — not equal after normalisation; the rule is restatement, not similarity | Descriptions are validated at authoring time |

### 9.5 Regression requirements

This feature **modifies existing functionality**. Behaviours that must be preserved:

| Existing behaviour | Existing coverage | New regression test | Why |
|---|---|---|---|
| `/<slug>` one-shot activation for a **granted** skill | `ForcedSkills` tests (14 refs) | No — reuse | Path unchanged; only the gate widens |
| `SeedConfig` re-enforces `Locked`/`Name`/`Type` every boot | 260 refs | **Yes** — #20 | Gating the skills block must not disable its neighbours |
| Menu precedence workspace > global > builtin | `summary_cap_test.go` | No — extend | Existing test still valid |
| Menu cap of 20 by precedence, not mtime | `summary_cap_test.go` | **Yes** — rewrite | ADR r3 D1.1 removes the cap; the existing cap tests assert behaviour that is being deleted |
| Ordinary bash commands are not blocked by the secret guard | `TestSecretGuardPatterns_OrdinaryCommandsUnaffected` | **Yes** — #22 | §13; existing test's 5 fixed commands do not contain "skills" |
| `SecretEntriesAlways` coupling to the text guard | `TestSecretGuardPatterns_CoverEverySecretEntryAlways` | **Yes** — #21 | §13 splits the set; the coupling test's premise changes |
| Existing kernel denies (`entities`, `system`, …) still enforced | `secretset_kernel_test.go` | No — must still pass | Regression signal for §13's split |
| `buildActiveSkillsContext` rendering | none found | Covered by #34 | Trigger changes, renderer does not |

**Regression dataset — existing behaviour that must not change**

| # | Input | Previous behaviour | Must still produce | Traces to |
|---|---|---|---|---|
| 1 | `cat master.key` | Blocked by text guard | Blocked | Regression: secret guard |
| 2 | `cat credentials.json` | Blocked | Blocked | Regression: secret guard |
| 3 | `cat notes.txt` | Allowed | Allowed | Regression: secret guard |
| 4 | `grep -r "skills" .` | Allowed | **Allowed** | Regression: §13 false positive |
| 5 | `ls ~/projects/skills-demo` | Allowed | **Allowed** | Regression: §13 false positive |
| 6 | `/summarize` by a granted agent | Activates for the turn | Activates | Regression: slash path |
| 7 | Core agent boot with customised non-empty grants | Preserved | Preserved | Regression: seeding |

---

## 10. Functional requirements

**Activation (D1)**
- **FR-001**: System MUST provide a single agent-invocable skill tool supporting load-by-slug and search-by-query.
- **FR-001a**: The tool's own description MUST disambiguate it from its two neighbours in trigger terms — `Skill` uses a skill you already have; `list_skills` enumerates what you have; `find_skills` searches the marketplace for skills you do **not** have — applying to this tool the same trigger-condition discipline D2 requires of skill authors.
- **FR-002**: System MUST NOT inject any skill's instructions into a turn unless activated for that turn.
- **FR-003**: System MUST make an activated skill's instructions available for the current turn only.
- **FR-004**: System MUST retain the existing menu of slug, display name and description in the cached prefix.
- **FR-005**: System MUST list every skill available to the acting agent in the current workspace, with no count limit, no truncation and no truncation footer.
- **FR-006**: System MUST NOT emit a filesystem location in the menu or any agent-facing skill listing.
- **FR-007**: System MUST offer a search mode over the same catalogue the menu is drawn from.
- **FR-008**: System MUST NOT introduce a new result or rate bound for search; it inherits the existing one.
- **FR-009**: System MUST correct any prompt text that directs the agent to read a skill file or to use the marketplace search for installed skills.

**Descriptions and habit (D2, D3)**
- **FR-010**: System MUST reject an authored skill description that is empty or whitespace-only.
- **FR-011**: System MUST reject an authored description that restates the skill's name under this exact comparison: case-fold both, strip all whitespace and punctuation, then test equality. No fuzzy matching, no edit distance.
- **FR-012**: System MUST reject an authored description exceeding 1024 characters.
- **FR-013**: System MUST emit a per-request reminder to consult the menu of at most **240 bytes**, measured as `len()` over the emitted string — the same unit `static_chars`/`total_chars` already use. (~30 tokens is the design intent behind the number; it is a review-time judgement, not an assertion, because no tokenizer exists in this codebase.)
- **FR-014**: System MUST place that reminder outside the cached prefix and the menu inside it.
- **FR-015**: System MUST NOT instruct the agent to narrate skill consideration.
- **FR-016**: System MUST hide a successful skill call from the chat thread by default and show a failed one.
- **FR-017**: System MUST show every skill call when verbose chat is enabled.

**Observability (D3.1)**
- **FR-018**: System MUST record one audit entry per skill call carrying slug, mode, outcome, shelf, agent id and workspace id.
- **FR-018a**: `mode` MUST be one of the closed set **`load` | `search`**, and `outcome` one of **`loaded` | `denied` | `not_found`** (ADR-072 D3.1). No other values are permitted; a test MUST assert against this closed set rather than against "N distinct values".
- **FR-018b**: A D9 delegate preload MUST audit as **`mode: load`** — it is a load performed on the child's behalf, not a fourth mode.
- **FR-018c**: FR-018's call records and FR-071a's write records are **two distinct record shapes under one audit event kind**, distinguished by their own field sets; neither is a superset of the other and neither uses optional fields to impersonate the other.
- **FR-019**: System MUST audit denials even when they are hidden from the thread.
- **FR-020**: System MUST surface a per-skill last-invoked time in the Skills view.

**Grant enforcement (D4, D4.1, D4.2)**
- **FR-021**: System MUST refuse a load of a skill the acting agent may not use, naming the skill, reusing the existing `PermissionDeniedCode = "permission_denied"` constant (`pkg/tools/result.go`) rather than minting a parallel string.
- **FR-022**: System MUST exclude skills the acting agent may not use from search results, filtered after ranking.
- **FR-023**: System MUST exclude them from the menu.
- **FR-024**: System MUST refuse slash-command activation of them.
- **FR-025**: System MUST exclude them from the installed-skill listing.
- **FR-026**: System MUST treat a mount as the grant instrument for skills discovered within it.
- **FR-027**: System MUST scope project skills to agents acting in the workspace carrying that mount.
- **FR-028**: System MUST resolve a registry slug the agent holds in preference to a project skill of the same slug.
- **FR-028a**: A **dangling** grant MUST NOT compete. When the grant names a registry slug that is no longer installed and a mount carries a project skill under that slug, the project skill MUST resolve normally. FR-028 protects a granted registry skill from displacement; it does not reserve the name.
- **FR-029**: System MUST resolve a cross-mount slug collision by **byte-wise ascending order of the raw UTF-8 mount name** (Go's default `sort.Strings`), first winning — not locale-aware, not case-folded, not Unicode-normalised.
- **FR-030**: System MUST record every slug collision with the competing locations.
- **FR-031**: System MUST NOT extend the grant list to the skill-authoring verbs.

**Default-none and seeding (D5, D5.1)**
- **FR-032**: System MUST treat an absent grant list as granting no skills.
- **FR-033**: System MUST treat an empty grant list identically to an absent one.
- **FR-034**: System MUST apply the core-roster skill seed only on a fresh install.
- **FR-035**: System MUST continue to re-enforce non-skill core-agent identity fields on every boot.

**Project skills and instructions (D6, D7)**
- **FR-036**: System MUST discover project skills solely from the recognised skills directories of a mount.
- **FR-037**: System MUST NOT apply any other project-detection heuristic, including version-control presence.
- **FR-038**: System MUST NOT inspect file contents to classify a file as a skill.
- **FR-039**: System MUST take no action and emit no warning when no mount carries a recognised skills directory.
- **FR-040**: System MUST include a mount's root instruction file content in every turn in that workspace.
- **FR-041**: System MUST label each mounted instruction block with its mount name and order it after the workspace's own instructions.
- **FR-042**: System MUST use at most one instruction file per mount, chosen deterministically.
- **FR-043**: System MUST cap composed instructions at 262144 bytes and visibly mark truncation.
- **FR-044**: System MUST order mounted instruction contributions by the same byte-wise ascending mount-name order as FR-029.

**Cache (D8)**
- **FR-045**: System MUST key the assembled prompt cache by acting agent and workspace.
- **FR-046**: System MUST bound the per-agent cached variant count and evict least-recently-used, exposed as an operator-tunable config value **shipping with a default of 8**, seeded in `pkg/config/defaults.go` like every other tunable (the `MaxSearchResults: 5` precedent).
- **FR-046a**: System MUST additionally bound the cache by **aggregate retained bytes per agent, defaulting to 4 MB**, and evict least-recently-used on whichever of the count or byte bound binds first. A count-only bound does not bound memory when variants differ by three orders of magnitude (ADR D8.1).
- **FR-046b**: System MUST NOT cache a single variant whose assembled size exceeds the byte budget; it MUST be rebuilt per turn instead, so one oversized workspace cannot evict every other entry on each insertion.
- **FR-046c**: The byte bound MUST be measured on the assembled string actually retained, not estimated from skill count.
- **FR-047**: System MUST invalidate a cached variant when the agent's membership of that workspace changes.
- **FR-048**: System MUST invalidate affected variants when a mount or workspace is deleted.
- **FR-049**: System MUST NOT scan a mounted directory tree on every turn to detect staleness.

**Delegation (D9)**
- **FR-050**: System MUST accept an optional requested-skill slug on a delegation run.
- **FR-051**: System MUST resolve that slug against the receiving agent's own grant, never the caller's.
- **FR-052**: System MUST begin the child's first turn with the requested skill loaded when permitted.
- **FR-053**: System MUST fail the delegation before the child's first model call when not permitted, naming the child and the skill, reusing the existing `DelegationDeniedCode = "delegation_denied"` constant.
- **FR-054**: System MUST distinguish an unresolvable slug from a refused one, via a **new** constant `SkillNotFoundCode = "skill_not_found"` — no not-found equivalent exists in `pkg/tools/result.go` today, so this spec mints and names it here rather than leaving it to be invented during implementation.
- **FR-055**: System MUST NOT transmit skill content from caller to child.
- **FR-056**: System MUST report the loaded skill in the delegation result.

**Read gating (D10)**
- **FR-057**: System MUST refuse file-tool reads of a registry skill's **instruction file** (`SKILL.md`, or a legacy `AGENT.md`/`AGENTS.md` inside a skill directory) on every platform (in-process file tools; see FR-062 for spawned children).
- **FR-058**: System MUST NOT gate reads of anything under a mount's recognised skills directories — instruction files included (ADR **D10.3(a)**). D4.1 already lets every agent in the workspace load every skill in that mount, so a read gate there restricts nothing and only breaks bundled files.
- **FR-059**: System MUST permit reads of all other files within a mount.
- **FR-060**: System MUST NOT gate reads of mounted instruction files.
- **FR-061**: System MUST continue to load skills through the skill tool while the read gate is in force.
- **FR-061a**: System MUST leave a skill's **bundled sibling files** — helper scripts, templates, reference documents, anything in the skill directory that is not an instruction file — readable by the ordinary file tools on every shelf.
- **FR-061b**: System MUST leave bundled executables runnable; a skill whose instructions say "run the script next to me" MUST work as authored.
- **FR-061c**: The kernel-layer deny (POSIX) MUST enumerate instruction files per turn from the loader's existing skill listing, not deny whole skill directories. Its cost scales with installed skill count and MUST be measured against D1.2's threshold.
- **FR-062**: System MUST extend the **instruction-file** deny to spawned children on POSIX platforms (Linux, macOS) only. On Windows the deny MUST NOT be claimed for spawned children: `FallbackBackend.ApplyToCmd` appends two environment variables and restricts nothing, so a shell child reads any skill file.
- **FR-062a**: System MUST emit an operator-visible warning at boot on Windows when the read gate is active, naming the spawned-child limitation.
- **FR-063**: System MUST NOT extend the shell literal-text guard to the skills entry (§13).
- **FR-064**: System SHOULD ship the non-kernel portion of the read gate even if the kernel leg is deferred on a platform.

**Project-skill authoring (D6.1, r3)**
- **FR-065**: System MUST resolve a skill slug to its own shelf when authoring, and write there.
- **FR-066**: System MUST write an edit to a mounted project's skill into that project's own file.
- **FR-067**: System MUST NOT create a central-library copy as a side effect of editing a project skill.
- **FR-068**: System MUST confine a project-skill write to the mount that owns it.
- **FR-069**: System MUST delete the project's own file when a project skill is removed.
- **FR-070**: System MUST NOT gate authoring writes behind the read gate or the grant list.
- **FR-071**: System MUST audit every write whose resolved path lands under a recognised skills directory, **regardless of which tool performed it** — `write_file`, `edit_file`, the authoring verbs, or any tool added later. The hook belongs at the shared path resolver, not in the authoring tool.
- **FR-071a**: The audit record MUST carry the shelf, the resolved path, the acting agent, the workspace, and **the tool that performed the write**, so the sanctioned and generic routes are distinguishable.
- **FR-071b**: System MUST NOT claim the write trail is complete for shell-mediated writes. `bash` can write these paths on every platform and is outside the resolver; the trail is complete for tool-mediated writes only.
- **FR-071c**: The write and its audit append are **not transactional**. A write that succeeds and whose audit append then fails is still performed; the audit failure MUST itself be logged per §7 and MUST NOT fail the turn. Stated locally because FR-071 carries the weight of the previous review's CRIT-002 and should be verifiable without first knowing §7 covers it.

**Mount-add threshold (D1.2, r4)**
- **FR-074**: System MUST surface an operator-visible warning at mount-creation time when the mount would contribute more skills than a configured threshold, **defaulting to 500**, stating the count and its per-turn consequence.
- **FR-074a**: The **first** time a mount is found to contain a recognised skills directory, the system MUST additionally disclose *what that grants* — auto-loadable agent instructions, not merely files — independently of the count threshold. A mount with three skills carries the same instruction-injection surface as one with five hundred; only the per-turn cost differs.
- **FR-075**: System MUST still create the mount — the threshold is information, not a refusal.
- **FR-076**: System MUST NOT truncate, cap or otherwise reduce the menu as a result of the threshold; the menu stays uncapped per FR-005.

**Symlink confinement (MAJ-005)**
- **FR-077**: System MUST resolve a candidate `SKILL.md` to its real path during discovery and refuse it when that real path lies outside the mount, including when the skills directory itself is legitimate and in-mount.
- **FR-078**: Discovery MUST apply the real-path check so a symlinked instruction file targeting outside the mount is not discovered. (After D10.3(a) the project shelf has no read gate, so discovery is the enforcement point there.)

**Shell guard separation (D10.1, r3)**
- **FR-072**: System MUST deny skill paths at the filesystem-policy layer without adding the skills entry to the shell literal-text guard.
- **FR-073**: System MUST continue to permit shell commands whose text merely mentions the word "skills".

---

## 11. Success criteria

- **SC-001**: On a turn where no skill is active, the number of skill-body tokens outside the cached prefix is **0**.
- **SC-002**: For a workspace offering M skills, the menu contributes exactly **M** one-line entries, all inside the cached prefix, with no truncation footer at any M.
- **SC-003**: The per-request reminder measures **≤240 bytes**.
- **SC-004**: All 73 tests in §9.3 pass; the ADR-072 §9 floor tests (T1–T9) are among them.
- **SC-005**: Running the seeding path twice with an emptied grant list in between leaves the list empty in **100%** of runs.
- **SC-006**: All five grant doors refuse an ungranted slug — **5 of 5** rows of the Dataset A/B door matrix pass.
- **SC-007**: An agent with an absent grant list and an agent with an empty one produce byte-identical refusal classifications.
- **SC-008**: A project skill is loadable by an agent with an empty grant list in its own workspace, and unloadable by the same agent in another — both observed.
- **SC-009**: Cross-mount and cross-shelf slug collisions each produce exactly **1** recorded collision entry naming both locations.
- **SC-010**: Composed instructions never exceed **262144 bytes**, and every truncation carries a visible marker.
- **SC-011**: All three delegation outcomes are distinguishable by classification, asserted against the **named closed set** (`permission_denied` / `delegation_denied` / `skill_not_found`), not merely as "3 distinct values".
- **SC-012**: Reading a registry skill's **instruction file** with the file tool fails on macOS, Linux and Windows — **3 of 3** platforms — while a **bundled sibling file in the same directory succeeds on all 3**. Both halves are required; the second is the D10.3 regression.
- **SC-013**: On Linux and macOS, a shell child cannot read a registry skill file while the skill tool still loads it in the same installation.
- **SC-014**: `grep -r "skills" .` and `ls ~/projects/skills-demo` remain permitted — **0** false positives from the shell guard.
- **SC-015**: Per-agent cached prompt variants never exceed **either** the configured count bound (default 8) **or** the configured byte budget (default 4 MB) under a 3×-bound workspace-churn loop, including one workspace whose menu alone exceeds the budget.
- **SC-016**: Every skill call in a 20-call mixed sample produces exactly one audit record — **20 of 20**.
- **SC-017**: `gofmt -l` = 0; `golangci-lint run --build-tags=goolm,stdjson` exit 0; `make test` exit 0; `npm run typecheck` exit 0; `npx vitest run` exit 0; `make verify-contracts` exit 0.

---

## 12. Traceability matrix

| FR | User story | BDD scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 | US-1 | Agent loads exactly the skill it names | 34, 46 |
| FR-002 | US-1 | Agent carries no skill instructions | 8, 33 |
| FR-003 | US-1 | A loaded skill does not persist | 34 |
| FR-004 | US-1 | Agent carries no skill instructions | 4, 35 |
| FR-005 | US-1 | Large catalogue fully listed | 5 |
| FR-006 | US-2 | Listing grant-filtered location-free | 7, 28 |
| FR-007 | US-1 | Search returns at most bound | 26 |
| FR-008 | US-1 | Search returns at most bound | 27 |
| FR-009 | US-1 | Granted skill survives a large mount | 6 |
| FR-010 | US-9 | Outline: descriptions validated | 23 |
| FR-011 | US-9 | Outline: descriptions validated | 23 |
| FR-012 | US-9 | Outline: descriptions validated | 24 |
| FR-013 | US-10 | Reminder present within budget | 25 |
| FR-014 | US-10 | Reminder present within budget | 35 |
| FR-015 | US-10 | Reminder present within budget | 25 |
| FR-016 | US-10 | Successful hidden / refused shown | 31 |
| FR-017 | US-10 | Verbose reveals | 32 |
| FR-018 | US-11 | Every call audited | 50 |
| FR-019 | US-11 | Hidden denial audited | 51 |
| FR-020 | US-11 | Never-invoked visibly unused | 54 |
| FR-021 | US-2 | Loading ungranted refused | 10 |
| FR-022 | US-2 | Search doesn't disclose | 26 |
| FR-023 | US-2 | Outline: every door (menu) | 4 |
| FR-024 | US-2 | Human cannot slash-activate | 29 |
| FR-025 | US-2 | Listing grant-filtered location-free | 28 |
| FR-026 | US-4 | Mounted project skills appear without grant | 11 |
| FR-027 | US-4 | Don't follow to another workspace | 11, 36 |
| FR-028 | US-4 | Cannot shadow granted registry slug | 12 |
| FR-029 | US-4 | Two mounts same slug | 13 |
| FR-030 | US-4 | Two mounts same slug | 14 |
| FR-031 | US-2 | Outline: every door | 28 |
| FR-032 | US-3 | No grant list can load nothing | 1 |
| FR-033 | US-3 | Empty identical to absent | 2 |
| FR-034 | US-3 | First boot seeds / Emptied survives restart | 18, 19 |
| FR-035 | US-3 | (regression) | 20 |
| FR-036 | US-4 | Outline: discovery triggers | 15 |
| FR-037 | US-4 | Outline: discovery triggers | 15 |
| FR-038 | US-4 | Outline: discovery triggers | 15 |
| FR-039 | US-4 | Mount with no skills dir changes nothing | 15 |
| FR-040 | US-5 | Instructions reach every turn | 16 |
| FR-041 | US-5 | Instructions reach every turn | 16 |
| FR-042 | US-5 | Only one file per mount | 16 |
| FR-043 | US-5 | Oversized truncate visibly | 17 |
| FR-044 | US-5 | Instructions reach every turn | 16 |
| FR-045 | US-8 | Menu differs per workspace | 36 |
| FR-046 | US-8 | Cache stays bounded | 39 |
| FR-047 | US-8 | Removing agent invalidates | 37 |
| FR-048 | US-8 | Deleting mount removes | 38 |
| FR-049 | US-8 | Menu differs per workspace | 36 |
| FR-050 | US-6 | Permitted loads in child's first turn | 40 |
| FR-051 | US-6 | Parent's grant irrelevant | 43 |
| FR-052 | US-6 | Permitted loads in child's first turn | 40 |
| FR-053 | US-6 | Not-granted refuses delegation | 41 |
| FR-054 | US-6 | Unresolvable distinguishable | 42 |
| FR-055 | US-6 | Naming in task text | 44 |
| FR-056 | US-6 | Permitted loads in child's first turn | 40 |
| FR-057 | US-7 | Registry skill cannot be read | 45 |
| FR-058 | US-7 | Project skill files stay readable | 48 |
| FR-061a | US-7 | Bundled helper files stay reachable | 48a |
| FR-061b | US-7 | Bundled helper files stay reachable | 48b |
| FR-061c | US-7 | Bundled helper files stay reachable | 48a |
| FR-059 | US-7 | Ordinary files stay readable | 47 |
| FR-060 | US-7 | Mounted instruction file readable | 49 |
| FR-061 | US-7 | Skill tool still delivers | 46 |
| FR-062 | US-7 | Shell child cannot read | 52, 53 |
| FR-063 | US-7 | (regression) | 21, 22 |
| FR-064 | US-7 | Shell child cannot read | 52 |
| FR-065 | US-4 | Editing a project skill writes into the project | 30a |
| FR-066 | US-4 | Editing a project skill writes into the project | 30a |
| FR-067 | US-4 | Editing a project skill writes into the project | 30a |
| FR-068 | US-4 | Removing a project skill | 30b |
| FR-069 | US-4 | Removing a project skill | 30c |
| FR-070 | US-4 | Read back through the tool | 51b |
| FR-071 | US-4 | A project-skill write is audited | 51a |
| FR-072 | US-7 | (regression) | 21, 30d |
| FR-073 | US-7 | (regression) | 22, 30d |
| FR-062a | US-7 | Windows refuses the file tool but does not confine a shell child | 53a |
| FR-071a | US-11 | A write through any tool is audited | 51d |
| FR-071b | US-11 | Windows refuses the file tool but does not confine a shell child | 53a |
| FR-074 | US-4 | A mount contributing an implausible number of skills warns at creation | 51e |
| FR-075 | US-4 | A mount contributing an implausible number of skills warns at creation | 51e |
| FR-076 | US-4 | A mount contributing an implausible number of skills warns at creation | 51e |
| FR-077 | US-4 | A symlinked skill file pointing outside its mount is refused | 30e |
| FR-078 | US-7 | A symlinked skill file pointing outside its mount is refused | 30e |
| FR-001a | US-1 | Agent loads exactly the skill it names | 34 |
| FR-018a | US-11 | Every skill call produces an audit record | 51j |
| FR-018b | US-6 | A permitted requested skill loads in the child's first turn | 51j |
| FR-018c | US-11 | A write through any tool is audited | 51j |
| FR-028a | US-4 | A dangling registry grant does not shadow a present project skill | 51g |
| FR-046a | US-8 | Cache stays within its byte budget for a very large workspace | 51h |
| FR-046b | US-8 | An oversized single variant is rebuilt rather than cached | 51i |
| FR-046c | US-8 | Cache stays within its byte budget for a very large workspace | 51h |
| FR-071c | US-11 | A write through any tool is audited | 51c |
| FR-074a | US-4 | A mount's first skills directory discloses what it grants | 51f |

**Completeness check — rev 5, re-derived by script on 2026-09-01 after ADR r6's D10.3 changes.**

Numbers below are script output, not a recount. The validator parses §8, §9.3, §10 and §12
independently and cross-checks them:

- **FRs: 94.** Every `- **FR-nnn**` definition diffed against every `| FR-nnn |` row in §12, in both
  directions — **0 defined-but-unmatrixed, 0 matrixed-but-undefined.**
- **Scenarios: 61** (19 Happy Path, 7 Alternate Path, 16 Error Path, 19 Edge Case), counted from the
  `**Category**` lines in §8. Every title appears in §12.1 by exact string match, and every §12.1 row
  corresponds to a real scenario — **0 in either gap.** No duplicate titles.
- **Tests: 73** in §9.3. Every test id referenced anywhere in §12 or §12.1 exists there — **0
  dangling references.**
- **§12.1's Test(s) column is DERIVED from §12**, recomputed and diffed against what is written:
  **61 rows, 0 mismatches, 0 empty cells.**

> **Why the numbers moved again.** Rev 4 read 91 FRs / 60 scenarios. ADR **r6/D10.3** then removed
> the project-shelf read gate and added the bundled-file requirements (FR-061a/b/c), taking it to
> 94 / 61. That is the point of deriving rather than asserting: the block is regenerated from the
> document each time, so a design change cannot leave it quietly stale — which is what happened at
> rev 2 and rev 3, and what the derivation exists to stop.

### 12.1 Scenario coverage index (exact titles)

| # | Scenario | FR(s) | Test(s) |
|---|---|---|---|
| 1 | Agent carries no skill instructions on an unrelated turn | FR-002,004 | 8, 33 |
| 2 | Agent loads exactly the skill it names | FR-001,003 | 34, 46 |
| 3 | A loaded skill does not persist into the next message | FR-003 | 34 |
| 4 | Large catalogue fully listed | FR-005,007 | 5 |
| 5 | Granted skill survives a large mount | FR-005,009 | 5 |
| 6 | Search returns at most the inherited result bound | FR-008 | 27 |
| 7 | Loading an ungranted skill is refused by name | FR-021 | 10 |
| 8 | Search results do not disclose an ungranted skill | FR-022 | 26 |
| 9 | A human cannot slash-activate an ungranted skill | FR-024 | 29 |
| 10 | Listing installed skills is grant-filtered and location-free | FR-006,025,031 | 7, 28 |
| 11 | Every door refuses an ungranted slug | FR-021..025 | 4, 10, 26, 28, 29 |
| 12 | An agent with no grant list can load nothing | FR-032 | 1 |
| 13 | An empty grant list behaves identically to an absent one | FR-033 | 2 |
| 14 | A deliberately emptied grant list survives a restart | FR-034 | 18, 19 |
| 15 | A first-ever boot seeds the core roster's grants | FR-034 | 18, 19 |
| 16 | A mounted project's skills appear without any grant | FR-026 | 11 |
| 17 | Project skills do not follow the agent to another workspace | FR-027,045 | 11, 36 |
| 18 | A mount with no skills directory changes nothing | FR-039 | 15 |
| 19 | A project skill cannot shadow a granted registry slug | FR-028 | 12 |
| 20 | Two mounts with the same slug resolve by mount name | FR-029,030 | 13 |
| 21 | A mount contributing an implausible number of skills warns at creation | FR-074,075,076 | 51e |
| 22 | Editing a project skill writes into the project | FR-065,066,067 | 30a |
| 23 | Removing a project skill deletes the project's file | FR-068,069 | 30b |
| 24 | A project-skill write is audited | FR-071 | 51a |
| 25 | An agent can read back through the tool what it just wrote | FR-070 | 51b |
| 26 | Project-skill discovery triggers only on the recognised directories | FR-036,037,038 | 15 |
| 27 | A mounted project's instructions reach every turn | FR-040,041,044 | 16 |
| 28 | Only one instruction file per mount is used | FR-042 | 16 |
| 29 | Oversized composed instructions truncate visibly | FR-043 | 17 |
| 30 | Skills without instructions contribute independently | FR-039,040 | 15 |
| 31 | A permitted requested skill loads in the child's first turn | FR-050,052,056 | 40 |
| 32 | A requested skill the child may not use refuses the delegation | FR-053 | 41 |
| 33 | An unresolvable requested slug is distinguishable from a refusal | FR-054 | 42 |
| 34 | The parent's own grant is irrelevant to the outcome | FR-051 | 43 |
| 35 | Naming a skill in the task text alone guarantees nothing | FR-055 | 44 |
| 36 | A registry skill file cannot be read with the file tool | FR-057 | 45 |
| 37 | The skill tool still delivers the same content | FR-061 | 46 |
| 38 | Ordinary files in a mount stay readable | FR-059 | 47 |
| 39 | Project skill files inside a mount stay readable | FR-058 | 48 |
| 61 | A skill's bundled helper files stay reachable | FR-061a, FR-061b, FR-061c | 48a, 48b |
| 40 | A mounted instruction file remains readable | FR-060 | 49 |
| 41 | Windows refuses the file tool but does not confine a shell child | FR-062,062a,071b | 52, 53 |
| 42 | A symlinked skill file pointing outside its mount is refused | FR-077,078 | 30e |
| 43 | A shell child cannot read a skill file where a sandbox backend exists | FR-062 | 52, 53 |
| 44 | The menu differs per workspace for the same agent | FR-045,049 | 36 |
| 45 | Removing an agent from a workspace invalidates its menu | FR-047 | 37 |
| 46 | Deleting a mount removes its skills from the menu | FR-048 | 38 |
| 47 | Cache stays bounded across many workspaces | FR-046 | 39 |
| 48 | Descriptions are validated at authoring time | FR-010,011,012 | 23 |
| 49 | The per-request reminder is present and within budget | FR-013,014,015 | 25 |
| 50 | A successful skill call is hidden from the thread | FR-016 | 31 |
| 51 | A refused skill call is shown in the thread | FR-016 | 31 |
| 52 | Verbose chat reveals every skill call | FR-017 | 32 |
| 53 | Every skill call produces an audit record | FR-018 | 50 |
| 54 | A hidden denial is still audited | FR-019 | 51 |
| 55 | A write through any tool is audited | FR-071,071a | 51a |
| 56 | A never-invoked granted skill is visibly unused | FR-020 | 54 |
| 57 | A dangling registry grant does not shadow a present project skill | FR-028a | 51g |
| 58 | Cache stays within its byte budget for a very large workspace | FR-046a, FR-046c | 51h |
| 59 | An oversized single variant is rebuilt rather than cached | FR-046b | 51i |
| 60 | A mount's first skills directory discloses what it grants | FR-074a | 51f |

---## 13. ⚠️ Finding: D10 Part A's stated mechanism breaks the shell guard

**This is a spec-phase finding that contradicts ADR-072 D10 Part A as written, and it requires an
ADR amendment. It was not visible at ADR time because it lives in a second consumer of the same
data structure.**

**What the ADR says.** D10 Part A: *"add `skills` to `SecretEntriesAlways`, and amend ADR-062 §4.0's
table."* The reasoning was sound as far as it went — `skills/` is a whole directory under
`$OMNIPUS_HOME`, structurally identical to `entities` and `system`, and `fspolicy` has no pattern
facility, so directory-shaped is the only shape available.

**What was missed.** `SecretEntriesAlways` has **two** consumers with different semantics:

1. **Path denial** — `DeniedPathsFor` / `KernelDeniedPathsFor` / `SecretPathsAlways`, feeding the app
   resolver and `DeriveKernelPolicy`. This is what D10 wants.
2. **A literal-text guard on shell commands** — `pkg/tools/shell.go::buildSecretGuardPatterns`
   compiles **one word-boundary regex per `SecretEntriesAlways` entry** and appends them to
   `defaultDenyPatterns`. This blocks any `bash` command whose *text* mentions the name.

Adding `skills` therefore auto-generates `\bskills\b` into the shell deny set, blocking **any command
containing the word "skills"** — `grep -r "skills" .`, `ls ~/projects/skills-demo`, `git commit -m
"add skills"`. A large, permanent false-positive surface on the project's primary shell tool.

**And the coupling is enforced, so this cannot be quietly skipped.**
`TestSecretGuardPatterns_CoverEverySecretEntryAlways` iterates every `SecretEntriesAlways` entry,
runs `"cat " + name`, and **fails if it is not blocked**. Adding `skills` to the set makes the test
demand exactly the over-broad behaviour.

**The code already documents the criterion this violates,** in `shell.go`'s own comment explaining
why `agents`/`workspaces` are deliberately excluded from the guard:

> "the per-turn half (`agents/`, `workspaces/`) is made of **ordinary English words an agent
> legitimately types constantly** ("list the workspaces", "check the agents dir")… **The five ALWAYS
> names are never legitimate in ANY turn**, which is what makes a context-free literal match safe for
> them and not for the rest."

`skills` is unambiguously an ordinary English word an agent types constantly. By the code's own
stated test for membership, it does not belong in `SecretEntriesAlways`.

**Resolution adopted by this spec (FR-063).** Introduce a **third category** — a set of entries that
are **path-denied but not text-guarded** — feeding `DeniedPathsFor`, `KernelDeniedPathsFor` and
`SecretPathsAlways`, but explicitly **not** `buildSecretGuardPatterns`. `skills` becomes its only
member. Rationale:

- It is the only option consistent with the criterion the code itself documents.
- It preserves the drift-regression property: the coupling test continues to assert full coverage of
  the text-guarded set, and a parallel test asserts the path-denied set is fully covered by the path
  denies. Neither set can silently fall behind.
- The alternatives are worse: excluding `skills` by name inside `buildSecretGuardPatterns` is a
  special case that breaks the coupling test's premise and invites the exact hand-copied drift that
  generation was introduced to eliminate; and dropping the fspolicy leg entirely would abandon D10's
  kernel enforcement, which is most of its value.

**Amendment made** — this is now **ADR-072 D10.1** (r3): Part A's "add `skills` to
`SecretEntriesAlways`" is superseded by the three-category split, recorded with this reasoning. The ADR-062 §4.0 table amendment (including the `system/`
row correction, MAJ-003) is unaffected.

**Tests**: #21 (skills path-denied but not text-guarded), #22 (ordinary commands mentioning "skills"
stay permitted), plus regression dataset rows 4 and 5.

---

## 14. Ambiguity self-audit (Phase 5.5)

### 14.1 Resolved during this spec — no founder input needed

| # | Ambiguity | Likely agent assumption | Resolution and grounding |
|---|---|---|---|
| A1 | Does `skills` really belong in `SecretEntriesAlways`? | Add it, as the ADR says | **No** — §13. Third category. Grounded in `shell.go`'s own documented membership criterion |
| A2 | Does the search mode cover project skills? | Registry only | **Yes, all shelves** — search runs over the same catalogue the menu is drawn from; a shelf-specific search would contradict D4.1's uniform model |
| A3 | Can a human slash-activate a project skill? | Denied, because `ResolveSkillName` applies the grant | **Yes** — `ResolveSkillName` must become shelf-aware (test #30). A human having *less* access than the agent would be incoherent, and D4.1 makes the mount the grant for both |
| A4 | Is `remove_skill` able to delete a project skill? | Silently attempts it | **Yes — REVISED by the founder's Q1 answer.** The authoring verbs become shelf-aware and write to the skill's own shelf, confined to the mount (ADR r3 **D6.1**). The earlier "refuse" resolution assumed the global-root limitation the decision removes |
| A5 | Which recognised skills directory wins within one mount? | Undefined | **`.omnipus/skills/` over `.claude/skills/`** — mirrors D6's stated "ours winning a slug clash" |
| A6 | Does D9's requested skill work for self-delegation? | Undefined | **Yes** — `execSource` is the parent for self-delegation, so the receiver's grant is the parent's own. No special case |
| A7 | Do heartbeat/cron turns get the menu and reminder? | Undefined | **Yes, unchanged** — ADR-072 §8 states they flow through the same assembly path |
| A8 | What is the cache bound's value? | Arbitrary | **A fixed cap with LRU**, value tunable; specified as a bound rather than a number so it can be set from measurement (FR-046, SC-015) |
| A9 | Does the read gate apply to the operator-facing Skills UI? | Might break the UI | **No** — the UI reads through the gateway in-process, below the tool boundary, exactly as `SkillsLoader` does |
| A10 | Are already-loaded skills removed from a transcript when a mount is deleted? | Might rewrite history | **No** — transcripts are history; only future turns change |
| A11 | Does the reminder reuse ADR-071's ToolSearch reminder plumbing? | Assume one exists and extend it | **There is no such plumbing to reuse — the review's premise is wrong.** Verified: ADR-071 contains no reminder or nudge mechanism, and `pkg/agent/tool_manifest.go::injectManifestNote` injects the *manifest block* (the tool index), not a per-request nudge. The skill reminder is therefore new — but it uses the **analogous injection point**, the per-request system message that `injectManifestNote` and `injectWorkspaceInstructions` already share, so it is one more note on an existing rail rather than a parallel mechanism (MIN-004) |
| A12 | What should an operator DO about a granted skill that never fires? | Nothing documented | **Runbook, three ordered checks** (OBS-001): (1) is the description written as a *trigger* ("use when…") rather than a summary — the single most common cause, and D2's whole premise; (2) is the skill actually granted for the workspace the agent is acting in — D4.1 makes that per-workspace for project skills; (3) if both hold, it is model behaviour, not configuration — reword the description toward the task's own vocabulary. Ordered cheapest-first and tied to the FR-020 last-invoked signal that surfaces the problem |

### 14.2 Answered by the founder (2026-09-01)

Both were relayed and answered. **The founder rejected the recommended option in both cases**, and
in both the recommendation was the more timid one. Both are now decisions in ADR-072 r3, not
spec-local notes.

**Q1 — Can an agent edit a mounted project's skills? → Write directly into the project** (option C,
against the recommendation to refuse). Recorded as **ADR-072 D6.1**.

The recommendation to refuse rested on a conflict with the read gate that **does not actually
exist**, and tracing it was the substance of implementing this answer. D4.1 already makes the mount
the grant instrument, so *every agent acting in that workspace may load every skill in that mount*
via the skill tool. D10 refuses `read_file` on that subtree — it redirects the access path, it does
not deny access. **There is therefore no project skill an agent can write but cannot read**, and the
"blind overwrite" hazard the recommendation guarded against is not reachable.

So the narrow decision this settles is that **authoring writes are not gated by the read gate or the
grant list** (FR-070), which is acceptable because: §6.4 already accepted that a mounted folder is
agent-writable; `write_file` can already reach the same path, so refusing in the authoring tools
blocks nothing; and D4.2 still prevents the dangerous case (a written project skill cannot hijack a
granted registry slug — the granted one still wins on resolution). Writes are audited (FR-071) so
the §6.4-accepted behaviour leaves a trail.

Real work this creates: the authoring verbs must become **shelf-aware**. `SkillWriter` is rooted at
the global skills directory today and cannot address a project slug at all, so this is new
capability, not a flag. It also **reverses this spec's earlier A4 resolution** on `remove_skill`.

**Q2 — Should a mount's skills push granted ones off the menu? → Remove the cap entirely**
(none of the three options offered; stronger than all of them). Recorded as **ADR-072 D1.1**,
reversing the ADR's own OQ13.

The question asked about crowding; the answer deletes the mechanism that causes it. Consequences
implemented here: FR-005 becomes "list everything, no truncation, no footer"; SC-002 is restated per
M rather than capped at 20; the truncation footer disappears, which **deletes F3's wrong signpost
rather than correcting it**; and the existing `summary_cap_test.go` cap assertions become a required
rewrite rather than "unchanged" in the regression table.

**No substitute bound was introduced** — not a token budget, which would be the same decision in a
different unit and was explicitly out of bounds. The honest consequence is recorded instead: the
per-message menu is now proportional to how many skills the workspace offers, unbounded for a large
mounted collection, and the mount's contribution is the one number **nobody chose** — a `git clone`
decides it. What was added is visibility, not a bound: the mount view states how many skills each
mount contributes per message.

## 14a. Review traceability

### rev 4 → rev 5 (test-integrity audit of the UAT plan)

| Finding | Resolution |
|---|---|
| **Bundled helper files unreachable** — D10's whole-directory denies break "run the script next to me", the commonest real skill shape | **ADR r6 / D10.3.** Confirmed with first-hand evidence: the `plan-spec` skill used to build *this spec* bundles four sibling files its `SKILL.md` instructs reading. **(a)** Part B's project-shelf gate **removed** — D4.1 already lets any agent in the workspace load any of that mount's skills, so it protected nothing and only broke bundling. **(b)** Part A narrowed from directory to **instruction file**; siblings read and execute normally. FR-057/058/061a/061b/061c/062/078, §5.2, scenarios 39 + 61, tests 48/48a/48b, Dataset C 12a/12b. Missed originally because all four embedded skills are lone `SKILL.md` files — the shipped set is the unrepresentative case |
| **§12 completeness block stale again** (claimed 81 FRs / 56 scenarios) | Re-derived by the rev-4 validator: real numbers are **94 FRs / 61 scenarios / 73 tests / 61 index rows, 0 orphans in every direction**. Block relabelled rev 5 with the method and an explanation of why the numbers moved |
| **§5.2's "menu MUST contain at most 20 entries"** contradicted D1.1/FR-005 | Fixed by the coordinator before this pass; **verified consistent** with FR-005, D1.1 and D1.2's mount-add threshold. Not redone |

### rev 3 → rev 4 (second grill-spec pass, verdict BLOCK)

| Finding | Resolution |
|---|---|
| **CRIT-001** §12.1 has wrong test refs in rows 53–56 | **Worse than reported, and fixed structurally.** A validator script found the one-row shift begins at **row 5, not 53 — 43 of 56 rows were wrong**, because rev 3 built the table from three hand-aligned parallel lists and one had silently lost an entry. §12.1's Test column is now **derived** from §12 (union of each row's FRs' tests), so it has one source of truth and cannot drift. The "regenerated mechanically" wording is replaced with the method and the failure is recorded in-place |
| **CRIT-002** Uncapped menu × count-only cache vs Constraint #3 | **Real, and forced ADR r5 (D8.1).** Arithmetic: ~302 B/entry typical, ~1206 B at the description cap → one 5000-skill variant is 1.44 MB typical / 5.75 MB worst; **8 variants for one agent = 11.5 MB, past the entire 10 MB budget**. Cache becomes byte-aware (FR-046a/b/c): evict on count *or* bytes, oversized variants never cached. §6's line now demonstrates rather than cites. Does **not** re-open D1.1 — bounding retained copies is not bounding the menu; a miss costs a rebuild |
| **MAJ-001** No default for the cache bound | **Count 8, bytes 4 MB, mount threshold 500** — stated in FR-046/046a/074 and destined for `pkg/config/defaults.go`, matching the `MaxSearchResults: 5` precedent |
| **MAJ-002** Audit vocabulary undefined | **FR-018a/b/c** — closed sets copied from ADR D3.1 (`mode`: `load`\|`search`; `outcome`: `loaded`\|`denied`\|`not_found`), D9 preload audits as `mode: load`, and FR-018 vs FR-071a declared **two distinct shapes under one event kind**. Test 51j asserts the closed set instead of "N distinct" |
| **MAJ-003** Dangling grant × same-slug project skill | **FR-028a** (and ADR D4.2) — a dangling grant does not compete; the project skill resolves. FR-028 protects a granted skill from displacement, it does not reserve the name. Scenario 57, test 51g, Dataset A row 10 |
| **MAJ-004** Project-skill content trust unstated | **§5.1** now states what an agent trusts when a project skill loads — instructions, not data; directory presence, not per-slug review; a standing injection channel for every repository contributor. **FR-074a** extends the mount disclosure to name *what a skills directory grants*, independent of the count threshold, because three skills carry the same surface as five hundred |
| **MIN-001** Shelf precedence reads as inverted | **§2.5a** restated with the exception inline, plus the FR-028a dangling case |
| **MIN-002** Three discovery tools undisambiguated | **FR-001a** — the tool description must distinguish `Skill` / `list_skills` / `find_skills` in trigger terms, the same discipline D2 demands of skill authors |
| **MIN-003** Classification strings not tied to wire vocabulary | **FR-021/053** reuse the existing `PermissionDeniedCode` / `DelegationDeniedCode`; **FR-054** mints and names `SkillNotFoundCode = "skill_not_found"`, since no not-found constant exists today |
| **OBS-001** Audit-append guarantee only inherited from §7 | **FR-071c** states locally that write and audit are not transactional |
| **OBS-002** G9 silent on read-during-write | **G9** now names both failure modes, and calls the torn read the more dangerous. Dataset C row 14 records it |

### rev 2 → rev 3 (first grill-spec pass, verdict REVISE)
All 14 findings from `skill-activation-and-loading-spec-review.md`. Three needed ADR changes
(→ **ADR-072 r4**); one had a false premise and is corrected rather than adopted.

| Finding | Resolution |
|---|---|
| **CRIT-001** Windows has no read-gate enforcement | **ADR r4 D10.2** — scoped POSIX-only, in the same explicit form CLAUDE.md uses for `pkg/entity`. FR-057 qualified, FR-062 rewritten, FR-062a adds a boot warning, US-7 retitled, scenario 41 + test 53a document the gap. A path-scoped text guard was **considered and rejected** (decorative, trivially bypassed, re-litigates D10.1) |
| **CRIT-002** Audit bypassable via `write_file` | **ADR r4 D6.1.1** — hook moved from the authoring tool to `tools.ResolvePath`, the shared seam every file tool passes and the same layer that already classifies skills paths for the read gate. FR-071 rewritten tool-agnostically, FR-071a adds the performing tool, **FR-071b states plainly that `bash` remains outside it**. Tests 51c/51d |
| **CRIT-003** Traceability self-check stale | **§12 regenerated mechanically.** 81 FRs and 56 scenarios diffed by script in both directions: 0 orphans either way. Root cause was deeper than stale numbers — the scenario column used abbreviated labels matching no title, so the claim was uncheckable in principle. Replaced with **§12.1, an index of exact titles** |
| **MAJ-001** "≤30 tokens" untestable | **Restated as ≤240 bytes** (FR-013, SC-003, §1.1, §5.2). Verified there is no tokenizer anywhere in `pkg/`, and that `static_chars`/`total_chars` are `len()` **byte** counts. ~30 tokens kept as design intent, explicitly not an assertion |
| **MAJ-002** "Restates the name" only tested verbatim | **FR-011 names the exact comparison** — case-fold, strip whitespace and punctuation, equality. Dataset F rows 8–10 add the near-miss cases, including one that is **accepted** ("Handles release notes") to fix the rule's boundary in both directions. Test 30f |
| **MAJ-003** T5c contradiction | **Split into T5c-linux / T5c-macos / T5c-windows**, matching the ADR's own T1a/T1b/T1c convention. §6 rewritten to name which host each needs; tests 52, 53, 53a re-tagged |
| **MAJ-004** Uncapped menu has no safety valve | **ADR r4 D1.2** — a threshold at **mount-add time**, with a table showing why it is not the cap D1.1 removed (different moment, visible, no effect on the menu). FR-074–076, scenario 21, test 51e, Dataset C row 13 |
| **MAJ-005** Symlink only tested at directory level | **FR-077/078** require real-path resolution at **both** discovery and read, and name both responsible layers. Dataset C rows 11 (out-of-mount, refused) and 12 (in-mount, fine). Scenario 42, test 30e |
| **MIN-001** Mount-name sort undefined | **FR-029/044** now specify byte-wise ascending over the raw UTF-8 name (`sort.Strings`) — not locale-aware, case-folded or normalised |
| **MIN-002** Cache bound configurability | **FR-046** — operator-tunable config value, default deferred to measurement |
| **MIN-003** Enumeration oracle unnamed | **Stated as an accepted trade-off** in §5.1, with the reason the search door is treated differently: a broad sweep leaking a catalogue costs much and helps nobody; a direct load names one slug already in mind, where "install it" vs "grant it" must be distinguishable |
| **MIN-004** Reminder reuse unstated | **Premise corrected, not adopted** (A11). Verified ADR-071 defines **no** reminder or nudge, and `injectManifestNote` injects the manifest block. There is nothing to reuse; the reminder is new but rides the same per-request system-message rail as `injectManifestNote` and `injectWorkspaceInstructions` |
| **OBS-001** No runbook for the biggest risk | **A12** — three ordered checks, cheapest first, tied to the FR-020 last-invoked signal |
| **OBS-002** "Shelf" ambiguous | **§2.5a glossary** — closed value set `{project:<mount>, global, builtin}`, with a note on why `project:` replaces the loader's `workspace` naming |

---

## 15. Gaps carried forward

| # | Gap | Status |
|---|---|---|
| G1 | The Linux child-only kernel deny (ADR §6.8) is unverified | Spike required; test #52 is its acceptance criterion. Fallback per OBS-001: ship without the kernel leg on Linux |
| G2 | §13's category split needs an ADR-072 amendment | **CLOSED** — now ADR-072 **D10.1** (r3) |
| G3 | ADR-072 does not state whether `/<slug>` reaches project skills | Resolved here as A3; test #30. Worth folding back into the ADR |
| G4 | Cache bound value is unspecified | Deliberate — set from measurement (A8) |
| G5 | Q1/Q2 unanswered | **CLOSED** — answered 2026-09-01; ADR-072 **D6.1** and **D1.1** (r3). See §14.2 |
| G6 | Shelf-aware authoring is new capability, not a flag | Open work item — `SkillWriter` is global-rooted today; the largest single piece r3 adds |
| G7 | Windows shell-child read gate is absent and stays absent | **Accepted, documented** (ADR D10.2). Boot warning + test 53a records it rather than asserting it closed |
| G8 | `bash`-mediated writes to skill paths are unaudited on every platform | **Accepted, stated** (FR-071b). The resolver hook covers tool-mediated writes only |
| G9 | Concurrent access to one project skill through a shared mount — **writer-vs-writer AND reader-vs-writer** | Open. No locking requirement is stated for `SkillWriter`'s project path. Two distinct failure modes: a lost update between two writers, and a **torn read** where a `Skill` load observes a partially-written `SKILL.md` and injects malformed instructions. The second is the more dangerous and was implicit in rev 3; both are named now. The codebase's striped-lock conventions would apply, but this spec does not specify them |

---

## 16. Assumptions

- v0.3 is greenfield; no migration path is designed or tested (ADR §6.2).
- Grants remain global on the agent for this work (ADR §6.5).
- `find_skills` keeps its marketplace role and is not repurposed.
- Mount names remain unique within a workspace, as enforced today.
- The audit trail is an acceptable home for per-call skill records; no new metrics system.
- Windows keeps app-layer-only enforcement; no Windows sandbox backend is built here.

---

## 17. Evaluation scenarios (holdout)

> **Post-implementation only.** Not referenced in the TDD plan or traceability matrix. To be run by
> a separate evaluator against a built binary.

### H1 — A long conversation still reaches for the right skill
- **Setup**: Agent granted 6 skills with trigger-shaped descriptions. Hold a 30-turn conversation on
  unrelated topics.
- **Action**: At turn 30, ask for something one skill plainly covers.
- **Expected**: The agent uses that skill, without being told it exists.
- **Category**: Happy Path

### H2 — Mounting a real repository just works
- **Setup**: Clone a public repository that genuinely carries `.claude/skills/`. Mount it.
- **Action**: Ask an agent in that workspace to do something the project's own skill covers.
- **Expected**: It uses the project's skill; no configuration was needed at any point.
- **Category**: Happy Path

### H3 — Cost actually fell
- **Setup**: Two builds, before and after, same agent granted 8 skills, same first message.
- **Action**: Compare logged prompt sizes.
- **Expected**: The after build carries no skill bodies; the difference is roughly the total size of
  the 8 skill files.
- **Category**: Happy Path

### H4 — A deliberate "no skills" choice sticks
- **Setup**: Empty a core agent's skill list in the UI. Restart the gateway three times.
- **Action**: Inspect the agent, then ask it to use a skill it previously had.
- **Expected**: Still empty after every restart; the request is refused.
- **Category**: Error

### H5 — A refusal explains itself
- **Setup**: Agent granted nothing. Mount nothing.
- **Action**: Ask it to use a named installed skill.
- **Expected**: It reports being unable to, identifying the skill; the operator can tell from the
  message that the fix is a grant, not an install.
- **Category**: Error

### H6 — The word "skills" is still usable in a shell
- **Setup**: A workspace with a mount containing a directory named `skills-demo`.
- **Action**: Ask the agent to list that directory and to search the repo for the word "skills".
- **Expected**: Both succeed.
- **Category**: Edge Case

### H7 — Two repositories, same skill name
- **Setup**: Two mounts on one workspace, both carrying a project skill called `deploy` with
  visibly different content.
- **Action**: Ask the agent to deploy, twice, in separate conversations.
- **Expected**: The same one is used both times, and which one is discoverable by the operator.
- **Category**: Edge Case

---

## 18. Clarifications

### 2026-09-01

- Q: Should Phase 1 discovery be re-run as an interview? → A: No — synthesized from ADR-072 (§1.1),
  which already answers it. Genuine silences recorded as gaps (§15), not invented.
- Q: Use GitNexus for codebase context? → A: No — index is 315 commits stale and every feature file
  has changed since; two other checkouts share the name. Direct verification used instead (§2.1).
- Q: Are the ADR's 13 required tests the full plan? → A: No — treated as a floor; expanded to 54
  (§9.3), with the ADR ids preserved.
- Q: Does `skills` go into `SecretEntriesAlways` as the ADR says? → A: No — a second consumer makes
  that break the shell tool; three-category split adopted (§13). ADR amended to r3 as D10.1.
- Q: Can an agent edit a mounted project's skills? → A: Yes, writing into the project itself
  (ADR r3 D6.1). The read-gate conflict was a false premise — see §14.2.
- Q: Should the 20-entry menu cap be reserved, raised or split? → A: None — removed entirely
  (ADR r3 D1.1). No substitute bound; consequence accepted and made visible.
- Q: Is the read gate real on Windows? → A: No. Verified `FallbackBackend.ApplyToCmd` appends two
  env vars and restricts nothing. Scoped POSIX-only (ADR r4 D10.2), not narrowed silently.
- Q: Is FR-071's write trail complete? → A: Not as r3 wrote it — `write_file` bypassed it. Hook
  moved to `tools.ResolvePath` (ADR r4 D6.1.1); still incomplete for `bash`, and FR-071b says so.
- Q: Does the reminder reuse ToolSearch's plumbing? → A: **The premise was wrong** — ADR-071 has no
  reminder mechanism; `injectManifestNote` injects the manifest block. See A11.
- Q: What unit is the reminder budget in? → A: **Bytes (240)**, not tokens — no tokenizer exists in
  this codebase; `static_chars`/`total_chars` are `len()` byte counts.
- Q: Does the uncapped menu fit Hard Constraint #3? → A: **Not with a count-only cache bound.** 8
  variants × 5000 skills ≈ 11.5 MB vs a 10 MB budget. Cache made byte-aware (ADR r5 D8.1).
- Q: Does a dangling registry grant block a same-slug project skill? → A: No — FR-028a.
- Q: Can a skill reach its own bundled helper files? → A: **Not under D10 as written** — the whole
  skills subtree was denied. Fixed in ADR r6 D10.3: the gate covers the instruction file only, and
  the project shelf is ungated entirely. Caught before any gate code was written.
- Q: Was §12.1 really only wrong in rows 53–56? → A: **No — 43 of 56 rows.** The shift began at row
  5; the review spot-checked the tail. Column is now derived from §12, not hand-maintained.
