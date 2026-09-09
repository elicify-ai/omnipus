# ADR-072 — Skill activation and loading: an on-demand `Skill` tool, the per-agent grant as the real gate, and a project's own skills and instructions

- **Status:** **Accepted** — **revision 5** (2026-09-01, HEAD `f101a9b4`; ratified by the founder —
  see §6 resolutions). All six founder-only questions are closed; §5 records the twenty-three
  resolvable from the code. Two of the six answers added mechanisms rather than merely selecting an
  option — **D9** (delegating with a requested skill) and **D10** (skill reads routed through the
  tool). §6.8 lists what is deliberately carried forward rather than closed.
- **Revision history:**
  - **r1** (2026-09-01) — initial draft plus the founder's six resolutions.
  - **r2** (2026-09-01) — revised in response to the adversarial review at
    [`ADR-072-skill-activation-and-loading-review.md`](ADR-072-skill-activation-and-loading-review.md)
    (verdict **REVISE**; 2 CRITICAL, 6 MAJOR, 4 MINOR, 3 OBSERVATION). That review re-derived r1's
    code citations independently — `skillAllowed`'s nil semantics, `ForcedSkills`,
    `SkillListTool.Execute`, the `delegate` snapshot channel, `Agent.yaml`'s contract text,
    `inherit_skills` — and every one held. Its yield came from two questions r1 never asked.
    **CRIT-001: r1 never said whether a mount-derived project skill is subject to the per-agent
    grant list**, and D4 and D6 answered it oppositely by implication. Resolved by **D4.1** — the
    gate is per-shelf, and each shelf has its own grant instrument. **CRIT-002: `coreagent.SeedConfig`
    silently re-seeds a core agent's skill grants on *every* boot whenever the list is empty** —
    which is exactly the state D5 makes meaningful. Re-verified against source for r2 (§ D5.1) and
    resolved by gating that block behind `isFreshInstall`, now a named implementation requirement.
    r2 also adds a required-tests section (**§9**, MAJ-005), an observability requirement
    (**D3.1**, MAJ-006), and states the D9 enumeration trade-off (MAJ-002) that r1 analysed for
    D10's file paths but never for D9's own failure mode. Per-finding map in **§10**. The review
    file is retained unmodified.
  - **r3** (2026-09-01) — two founder decisions taken during the spec round
    ([`docs/internal/specs/skill-activation-and-loading-spec.md`](../specs/skill-activation-and-loading-spec.md)),
    both **against** this ADR's own recommendation and both requiring design work rather than a
    wording change. **(1) Authoring writes go directly into the mounted project** (D6.1) — writing
    into the repo, not forking a shadow copy into the central library. **(2) The 20-entry menu cap
    is removed entirely** (D1.1) — not raised, not split, removed; OQ13's r1/r2 answer is reversed
    and §3's cost claim is re-stated to match what survives. r3 also records a spec-round finding
    that **contradicts r2's D10 Part A mechanism**: `SecretEntriesAlways` has a second consumer
    (`pkg/tools/shell.go`'s literal-text guard) that would block every `bash` command containing the
    word "skills" — corrected in **D10.1**.
  - **r4** (2026-09-01) — three corrections forced by the adversarial review of the derived spec
    ([`…-spec-review.md`](../specs/skill-activation-and-loading-spec-review.md), verdict REVISE).
    Each is a design change, not wording. **D10.2** scopes D10's "a grant is a real boundary" claim
    to **POSIX only** — `FallbackBackend.ApplyToCmd` merely appends two env vars, so on Windows a
    spawned shell child reads any skill file it likes; stated as explicitly as the `pkg/entity`
    POSIX-only precedent CLAUDE.md already sets. **D6.1.1** moves the project-skill write audit
    from the authoring tool down to `tools.ResolvePath`, because D6.1's own justification ("the
    write leaves a trail") was false for the `write_file` route it simultaneously conceded was
    always open. **D1.2** adds a mount-add-time threshold — not a menu cap, a different mechanism at
    a different moment — so D1.1's uncapped menu cannot be silently weaponised by one pathological
    mount.
  - **r6** (2026-09-01) — **D10.3**, a functional defect found by a test-integrity audit of the UAT
    plan: D10's whole-directory denies make a skill's own bundled helper files unreachable, so the
    commonest real skill shape ("run the script next to me") cannot work. Part B's project-skill gate
    is **removed** (D4.1 already lets any agent in the workspace load any of that mount's skills, so
    it protected nothing); Part A's registry gate **narrows from the directory to the instruction
    file**. Caught before implementation — `pkg/tools/resolvepath.go`, `pkg/fspolicy/` and
    `pkg/sandbox/derive_from_fspolicy.go` were all still unmodified when this landed.
  - **r5** (2026-09-01) — one correction forced by the **second** adversarial review of the spec
    (verdict BLOCK). **D8.1**: D1.1 (uncapped menu) and D8 (cache bounded by *count*) were each
    reasoned through in isolation and their product was never checked against CLAUDE.md Hard
    Constraint #3. It fails — arithmetic in D8.1 — because an LRU bound on the *number* of cached
    variants does not bound memory when individual variants vary by three orders of magnitude. The
    cache becomes **byte-aware**: evict on count or aggregate bytes, whichever binds first. This
    does **not** re-open D1.1: bounding how many assembled copies are *retained* is not bounding
    what the agent *sees*, and a cache miss costs a rebuild, never a truncated menu.
- **Date:** 2026-09-01
- **Deciders:** founder (Daniel Piatkowski) — decided §6.1–§6.6; architect — mechanism
- **Answers:** [elicify-ai/omnipus#663](https://github.com/elicify-ai/omnipus/issues/663)
  ("Skill activation model is wrong: SkillsFilter auto-loads every turn instead of gating an
  agent-invoked skill tool")
- **Phase:** v0.3 (per CLAUDE.md's routing rule — skills/workspaces/plugins → v0.3, issue #156)
- **Related:**
  [ADR-071](ADR-071-tool-manifest-tier-redesign.md) (`ToolSearch`; index-in-context, content
  on demand — **the precedent this ADR is built on**, especially D1, §3.2.2 and §4.1);
  [ADR-063](ADR-063-unified-file-access-engine-and-mounts.md) (mounts, D4/D7);
  [ADR-062](ADR-062-filesystem-read-exec-model-inversion.md) (reads default open — §4.0's
  secret set, which **explicitly keeps `skills/` agent-readable**);
  [ADR-032](ADR-032-external-agent-workspace-execution.md) (a delegated sub-turn runs as the
  target agent, never the parent);
  [ADR-037](ADR-037-remove-global-delegation-policy.md) (trust is workspace-scoped, not a
  global agent attribute — the precedent §6.5 asks about);
  [ADR-026](ADR-026-unified-slash-command-skill-menu.md) (the `/<skill>` one-shot path);
  `.preview-doc/` (v0.3 concept: `decisions.html` locked ledger, `spaces.html` §"Skills stay a
  global registry", `context.html` prompt-layer map)
- **Evidence level:** every claim about current behaviour below was read from source on
  `release/v0.1.1` at HEAD `f101a9b4` (2026-09-01) and is cited by `file::symbol` rather than
  `file:line` — issue #663's own anchors into `pkg/agent/loop.go` had already drifted by ~220
  lines when this ADR was written, which is the failure mode CLAUDE.md warns about.
- **Source material:** this ADR formalises the design draft at
  `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/docs/internal/design/skills-on-demand-draft.html`.
  §8 lists the four places where re-verification corrected or extended that draft.

---

## 1. Problem

### 1.1 What is wrong, in one paragraph

A **skill** is a written instruction sheet an agent can follow — "here is how we cut a release",
"here is our invoice process". Omnipus can search for them, install them, write them, edit them
and delete them. The one thing it has no way to do is let an agent **pick one up at the moment a
task turns out to need it**. In place of that there is a per-agent list of skills, meant to say
*which sheets this agent may pick up*, which the code reads as *which sheets this agent must always
be holding* — and pastes every one of them, in full, into the agent's instructions on every single
message.

### 1.2 Verified current behaviour

Six mechanisms, all confirmed by reading the source at `f101a9b4`:

| # | Mechanism | Where |
|---|---|---|
| 1 | The per-agent grant list `config.AgentConfig.Skills` becomes `AgentInstance.SkillsFilter` at instance construction | `pkg/agent/instance.go` (`skillsFilter` local → the `AgentInstance` literal's `SkillsFilter` field) |
| 2 | The **same** list is also installed as a resolution-time allowlist | `pkg/agent/instance.go` → `contextBuilder.WithSkillAllowlist(agentCfg.Skills)` |
| 3 | `SkillsFilter` is unioned with the one-shot `/<skill>` list into "active this turn" | `pkg/agent/loop.go::activeSkillNames` |
| 4 | That union is passed to `BuildMessages(..., activeSkills...)`, which calls `buildActiveSkillsContext`, which calls `LoadSkillsForContext` — reading **every** named skill's full `SKILL.md` off disk into the system context, **every turn** | `pkg/agent/context.go::BuildMessages`, `::buildActiveSkillsContext`; `pkg/skills/loader.go::LoadSkillsForContext` |
| 5 | A **menu** of the permitted skills (slug, display name, description, on-disk path, source) is already built and already filtered by the allowlist, capped at 20 | `pkg/skills/loader.go::BuildSkillsSummaryFunc`, `maxSkillsInSummary = 20`; consumed at `pkg/agent/context.go::BuildSystemPrompt` |
| 6 | The skill loader already searches **three shelves in priority order**: workspace → global → builtin | `pkg/skills/loader.go::SkillsLoader.ListSkills`, `::LoadSkill`, `::SkillRoots` |

So the menu exists, the permission filter on the menu exists, and the three-shelf loader exists.
**What is missing is the single verb that turns a menu entry into a loaded skill** — and the
deletion of mechanism 3/4, which serves every dish on the menu regardless.

Two consequences, both compounding:

- **It costs money on every message and crowds out the conversation.** `BuildMessages` marks the
  static system prompt as the Anthropic prompt-cache breakpoint
  (`CacheControl: &providers.CacheControl{Type: "ephemeral"}`) and then appends the active-skills
  block **after** it, as an uncached content block. So the force-loaded skill bodies are the one
  part of the prompt paid for in full on every single request.
- **It punishes exactly what v0.3 is built to encourage.** `.preview-doc/decisions.html` locks
  "skills are the editable arm of self-improvement" and has agents authoring their own skills over
  time. Under today's rule a growing skill library makes every conversation more expensive, so
  being generous with grants and being cheap to run are in direct conflict.

### 1.3 Four findings beyond what issue #663 reports

**F1 — the grant list is not a permission barrier; it leaks through at least three other
channels.** `skillAllowed` (`pkg/agent/context.go`) is consulted in exactly three places, all in
that one file: the menu (`BuildSkillsSummaryFunc`'s predicate), `ListSkillNames`, and
`ResolveSkillName`. Everything else that can reach a skill bypasses it:

  - **`list_skills`** (`pkg/sysagent/tools/skill.go::SkillListTool.Execute`) calls
    `SkillsLoader.ListSkills()` directly and returns **every installed skill's id, name,
    description *and on-disk path*** with no allowlist filter of any kind. This is the identical
    shape ADR-071 §3.2.2 recorded as **CRIT-201** on the tools side — the loader was gated, the
    *discovery* channel was not — reproduced verbatim on the skills side. It is in Tier 3 of
    ADR-071 §4.1, i.e. registered and callable for every agent.
  - **`read_file`** on the path the menu and `list_skills` both hand out. The system prompt
    currently *instructs* this: "To use a skill, read its `SKILL.md` file using the `read_file`
    tool" (`pkg/agent/context.go::BuildSystemPrompt`). And per **ADR-062 §4.0**, filesystem reads
    default to ALLOW with `skills/` named in the "**must stay agent-accessible**" column — so this
    is not an oversight to be quietly patched, it is an accepted decision this ADR must
    deliberately reckon with. See §6.6.
  - **`create_skill` / `edit_skill` / `remove_skill`** (`pkg/sysagent/tools/skill.go`,
    `pkg/skills/authoring.go`) are confined to the global skills directory but consult no
    allowlist — an agent can rewrite a skill it was never granted.

**F2 — an empty grant list means the opposite of what the shipped contract says, and the two
disagree in writing.** `contracts/components/schemas/Agent.yaml`'s `skills` property states:
"Absence of the field and an empty array are semantically identical (opt-in, default none)." The
code does the reverse: `WithSkillAllowlist(nil)` leaves `skillAllowlist == nil`, and `skillAllowed`
returns `true` unconditionally for a nil allowlist — **an agent with no list has access to
everything installed**, while `"skills": []` (a non-nil empty slice) is deny-all. Absence and empty
are not identical; they are opposites. This is a contract-vs-code divergence, not merely an
undocumented default, and it settles half of the migration question on its own (§5, OQ4).

**F3 — the escape hatch the prompt advertises does not exist.** Both the `# Skills` block and
`BuildSkillsSummaryFunc`'s truncation footer tell the agent to "call `find_skills` to search the
full installed catalog, including any not shown below." `find_skills`
(`pkg/tools/skills_search.go::FindSkillsTool`) searches **remote marketplaces for skills that are
not installed yet** — `RegistryManager.SearchAll` — and cannot see an installed skill at all. So
the 20-skill cap is a hard discovery ceiling with a **wrong** signpost on it (**resolved in r3 by
D1.1 removing the cap and the footer together**, rather than by correcting the footer): skills 21+ are
undiscoverable except by guessing their slug. (`list_skills` *can* see them — but it is Tier 3,
unfiltered, and never named in that footer.)

**F4 — the advertised skills path is wrong, and the first shelf is vestigial.**
`getWorkspaceAndRules` prints `Skills: <agent workspace>/skills/{skill-name}/SKILL.md`. Nothing
writes there: `install_skill` writes to the **global** dir (`pkg/agent/context.go::globalSkillsDir`,
shared with `NewSkillWriter(skillsGlobalDir)` in `pkg/gateway/gateway.go`), the first-boot seed of
the four embedded skills (`summarize`, `skill-authoring`, `plan`, `daily-briefing`) writes there
too, and the authoring tools deliberately write an override there rather than mutating a builtin.
The loader's first shelf (`<workspace>/skills`) therefore exists, is ranked first, and is empty on
every real install — **which is exactly what makes it available to re-point without disturbing
anything** (§2, D6).

### 1.4 The three requirements this ADR must satisfy

1. **On-demand activation.** A lightweight menu of one-line descriptions in context; full
   instructions only when a task matches.
2. **The per-agent permission model is the enforcement gate.** One central registry; each agent
   holds a card saying which of them it may borrow.
3. **A code project's own skills and its `CLAUDE.md`/`AGENTS.md` become loadable when an agent
   works on that project** — reconciled with, not bolted onto, the Omnipus **workspace** concept.

---

## 2. Decision

### D1 — Add one tool, `Skill`. This is `ToolSearch`'s pattern, not a borrowed idea

**Omnipus has already made this exact decision once, for tools, and shipped it.** ADR-071 §1.1
records the mechanism: a **manifest block** carries `name — description` for tools an agent cannot
yet call, and `ToolSearch` fetches a tool's full callable schema only when the agent asks for it.
ADR-071 §4.1 then goes further and makes 63 of the 89 tools **search-only** — registered,
policy-governed, fully loadable, and **invisible until the agent goes looking**.

Skills are the same problem one layer up: a `SKILL.md` body is to its one-line description exactly
what a tool schema is to its manifest line. The design below is therefore **not a new pattern being
introduced to this codebase** — it is the pattern this codebase already validated, applied to the
one place it was never wired up.

Concretely:

- **Keep the menu.** `BuildSkillsSummaryFunc` already produces it, already filtered by the grant
  list. It stays inside the cached static system prompt (the prompt-cache breakpoint), where its
  cost is paid once per prefix rather than once per message.
- **Add the missing verb.** One tool, named **`Skill`**, taking a skill's slug. It loads that skill
  and nothing else, for the current turn.
- **Delete the force-load.** `activeSkillNames` stops unioning `AgentInstance.SkillsFilter` into
  the turn's active set. `buildActiveSkillsContext` and `LoadSkillsForContext` are **kept** —
  they are the correct loading machinery, wired to the wrong trigger — and are driven only by an
  explicit activation for *that turn*: a `Skill` call, or the existing `/<skill>` command.

**On the name.** Every other Omnipus tool is a lowercase verb, which argues for `load_skill`.
ADR-071 **D1** overrode exactly that convention for exactly this class of tool, renaming
`load_tool` → `ToolSearch` "to match the equivalent tool in the harness this design was compared
against, for operator familiarity across the two systems." `Skill` is the same call for the same
reason, and consistency with the sibling mechanism outranks consistency with the general naming
convention here. See §5, OQ1.

**On search.** `Skill` also takes a `query` instead of a slug, running the same BM25 ranking
`ToolSearch` uses (`pkg/utils/bm25.go`) over the installed catalog. This is what F3's footer should
always have pointed at. **The match list is policy-filtered after ranking, never before** — ADR-071
§3.2.2 establishes both halves of that: filtering the *results* is required (an unfiltered match
list discloses the name and full description of a denied item), and filtering the *corpus* is wrong
(cross-agent leak, uncacheable key, changes BM25's own scores). That reasoning transfers unmodified.

### D1.1 — The menu has no cap (r3, founder decision, reversing OQ13)

`maxSkillsInSummary = 20` is **deleted**, not raised and not split. Every skill the acting agent may
use in the current workspace appears in the menu, always.

r1/r2 kept the cap and fixed its signpost instead (OQ13). The spec round put the question again,
because D4.1's per-shelf model created a consequence neither revision had traced: a mounted
repository carrying 20+ skills of its own would fill the entire menu by shelf precedence, and the
skills an operator had *deliberately granted* would silently stop being listed. Three options were
offered — reserve part of the cap, leave it, raise it. **The founder rejected all three: no cap at
all.**

**What this fixes, and it is more than the crowding problem it was asked about:**

- **An explicit grant can never be invisible.** That was the actual defect behind the question. A
  reserved-slots rule would have solved it with a rule nobody could predict the behaviour of;
  removing the cap solves it by construction.
- **F3 dissolves rather than gets corrected.** §1.3's F3 was that the truncation footer pointed at
  `find_skills`, which cannot see installed skills. With no truncation there is no footer and no
  wrong signpost — the bug is deleted rather than fixed. `BuildSkillsSummaryFunc` loses both the
  cap and the footer.
- **The menu's ordering becomes cosmetic.** Shelf precedence still decides *which skill a slug
  resolves to* (D4.2), but it no longer decides *which skills survive into the menu*, because none
  are cut. The deterministic sort stays for stable output, not for selection.
- **Search stops being load-bearing for discovery.** `Skill`'s search mode remains (D1) and is still
  useful for narrowing a large menu, but it is no longer the only way to reach a skill past a
  ceiling. Nothing about it changes.

**The consequence, stated plainly rather than mitigated.** The menu was the mechanism that replaced
force-loaded skill bodies *specifically to keep the per-message cost small*. Removing its bound
means the per-message menu is now proportional to however many skills are available, with no ceiling:

| Source | Bounded by | Who chose that number |
|---|---|---|
| Registry skills | the agent's grant list | **the operator, explicitly** |
| Builtin skills | 4 | the project |
| **Project skills (mounts)** | **whatever the repository happens to carry** | **nobody — a `git clone` decides it** |

The registry side is self-limiting: an operator who grants 200 skills chose to. The project side is
not — mounting a repository that carries 200 skills adds 200 menu lines to every message in that
workspace, and the operator chose the *mount*, not the count. **A very large mounted skill
collection therefore produces a correspondingly large per-message menu, and this ADR accepts that.**

**No substitute bound is introduced.** Not a token budget, not a per-shelf cap, not a per-mount cap.
A token budget is still a cap in substance and the decision was explicitly "no cap at all"; quietly
swapping the unit would be answering a different question than the one that was asked. If this turns
out to bite in practice, the honest fix is to go back to the founder with real numbers, not to
pre-empt them here.

**What is added instead is visibility, which is not a bound.** Consistent with D3.1's approach to
the other accepted risk: the workspace's mount view states how many skills each mount contributes to
every message. That lets an operator see the cost they have taken on and decide for themselves
(unmount, or trim the repo's skills directory) — it constrains nothing.

### D1.2 — A mount-add threshold, which is not the cap that was removed (r4)

D1.1 removed the menu cap and declined a substitute bound. The spec review pointed out that this
leaves **no failure mode at all** for a pathological mount — a monorepo with thousands of generated
or vendored directories matching `*/SKILL.md` would inflate every turn in that workspace, possibly
past the provider's own request limit, with no warning and no recovery path. §6.7.2's own words
concede the risk ("the mount's contribution is the one number nobody chose").

**This is a different mechanism at a different moment, and that is why it does not re-litigate
D1.1.** The founder rejected *a cap on the menu* — a silent, per-turn truncation of what the agent
can see. What is added here is **a threshold at mount-add time**:

| | Removed by D1.1 | Added by D1.2 |
|---|---|---|
| When | Every turn, forever | Once, when a mount is created |
| What it does | Silently drops skills from the menu | Warns the operator, visibly |
| Who sees it | Nobody | The person making the decision |
| Effect on the menu | Truncated | **None — still uncapped** |

Concretely: when a mount would contribute more than a threshold number of skills, **mount creation
surfaces an operator-visible warning stating the count and its per-turn consequence**. The mount is
still created — this is information, not a refusal — and the menu remains uncapped exactly as D1.1
requires. A count far above any plausible hand-authored collection is the right threshold, because
its purpose is catching *accidents and generated trees*, not disciplining real usage.

This satisfies the review's actual requirement — "some explicit, tested boundary rather than none" —
without reintroducing what was rejected. **It is not a token budget and not a menu limit**; nothing
about what the agent sees per turn changes. If a threshold at mount time turns out to be the wrong
moment, that is a question to take back to the founder, not to convert into a per-turn cap.

**Search introduces no new bound of its own** (MIN-003). `Skill`'s search mode inherits
`ToolSearch`'s existing result cap (`MaxSearchResults`, default 5) and its cached-engine behaviour
(`getOrBuildEngine`, keyed on registry version). No new rate limit, no new result-count limit, no
new size cap — deliberately, so there is one number to reason about rather than two that can drift.
Stated because this ADR is otherwise explicit about every cap it touches (262144-byte instructions,
1024-char description; the menu's own cap is **removed** by D1.1), and silence would read as an
oversight. Note the asymmetry this leaves, deliberately: search results stay bounded at 5 while the
menu is unbounded. They answer different questions — the menu is "everything you may use", search is
"the best few matches for this query" — and a ranked list is only useful when it is short.

### D2 — A skill's description is a trigger condition, and that is an authoring rule, not a hope

Once nothing loads automatically, **the one-line description is the only thing the model sees
before choosing.** Reliability of "did the agent use the right skill?" is therefore almost entirely
a property of how descriptions are written, and almost not at all a property of how insistently the
system prompt nags.

A description must state **when to use the skill**, in matching terms — *"Use when the user asks to
cut a release, tag a version, or publish release notes"* — not what the skill is about
(*"Release process documentation"*). The first pattern-matches against a task; the second does not.

This becomes a **stated authoring convention**, enforced where skills are authored rather than left
to author discipline:

- The `skill-authoring` embedded skill (`pkg/skills/embedded/skill-authoring/`) states the rule and
  gives the good/bad pair above.
- `create_skill` / `edit_skill` (`pkg/skills/authoring.go`) reject a description that is empty or
  merely restates the name, and their tool descriptions state the convention.
- The Skills UI shows the convention as helper text on the description field.

`SkillMetadata.Description` is capped at `MaxDescriptionLength = 1024` (`pkg/skills/loader.go`) —
ample for a trigger sentence, and no schema change is needed.

**This is the highest-leverage half of the "when does it fire?" problem.** D3 is the smaller half.

### D3 — The check-the-menu habit is reinforced per task, and it is silent

Three parts, deliberately separated because they live in different places:

1. **The menu itself stays in the cached static prompt** (`BuildSystemPrompt`) — it is stable
   per-agent and belongs inside the cache breakpoint, per ADR-071 D5's reasoning about what may sit
   inside a cached prefix.
2. **A one-line reminder is emitted per request**, in the dynamic context block
   (`pkg/agent/context.go::buildDynamicContext`), which is already rebuilt per turn and already
   sits *after* the cached prefix and therefore closer to the user's message. Wording of the effect
   rather than the mechanism: *when starting a new task or acting on a new request, check the
   `# Skills` list for one whose description matches, and call `Skill` before proceeding.* A single
   reminder at session start decays across a long conversation; recency is precisely what this
   placement buys.
   **Budget: ≤240 bytes, hard — and the unit matters** (MIN-001, corrected in r4). This text lands
   outside the prompt-cache breakpoint on every single turn, the position whose cost §1.2 complains
   about, so "one line" is not a budget. r2/r3 wrote the budget as "≤30 tokens", which **cannot be
   checked**: there is no tokenizer anywhere in this codebase (verified — no `TokenCount` /
   `EstimateTokens` / `CountTokens` / tiktoken), and the only measurement infrastructure this ADR
   itself cites (`static_chars`, `total_chars` in `pkg/agent/context.go`) is `len()` over a string,
   i.e. a **byte** count. The normative, testable budget is therefore **240 bytes**, in the unit the
   codebase actually measures. ~30 tokens remains the *design intent* behind the number and is a
   review-time judgement, not an assertion any test makes.
3. **It is never narrated.** The agent must not report "let me consider available skills…" — this
   is a silent habit, not a step to tell the user about. Two mechanisms, both existing:
   - The prompt rule says so explicitly.
   - `src/lib/toolVisibility.ts` — the closed, narrow hide-by-default set CLAUDE.md documents —
     gains `Skill`, alongside `ToolSearch`, `bash`'s background sub-cases and `delegate`'s
     `run`/`status` sub-cases. Mirroring `ToolSearch` exactly: **hidden on success, forced visible
     on error**, because a *denied* or *missing* skill load is a real outcome the reader needs.
     Persistence is unaffected — the call is still in the transcript; this is render-only.

### D3.1 — The design's biggest risk must be observable, or D2 is unfalsifiable (MAJ-006)

§3 names "a badly-described skill simply never fires" as this ADR's largest accepted cost, and D2
as the mitigation. r1 offered no way to tell, in production, whether that mitigation works — and a
skill silently never firing looks identical from outside to a skill nobody needed. That is not
acceptable for the one risk the document itself calls biggest, so observability is part of the
design rather than a follow-up:

- **Every `Skill` call is audited** with: the slug requested, the mode (`load` / `search`), the
  outcome (`loaded` / `denied` / `not_found`), the shelf it resolved from, and the acting agent and
  workspace. This rides the existing audit trail (`pkg/audit`) — a denial in particular is a
  security-relevant event and belongs there next to every other policy decision.
- **A per-skill `last_invoked` timestamp** is surfaced in the Skills UI. This is the cheapest
  possible answer to "is D2 working?": a granted skill with a stale or empty `last_invoked` is
  exactly the failure mode, and it is visible without any analysis pipeline.
- **The menu size is recorded alongside invocation counts**, so granted-vs-invoked can be compared
  later. Deliberately not a new metrics system — the audit records already carry what a query needs.

Denials must be audited even though D3 hides a *successful* `Skill` call from the chat thread (N6):
render-visibility and audit are different questions, and the hide is render-only.

### D4 — The grant list is the gate, and a gate must sit at every door

One library, many library cards. One central registry (`$OMNIPUS_HOME/skills`); each agent holds a
card listing what it may borrow. Being on the card costs nothing until the agent borrows.

`skillAllowed` must be consulted at **five** points, not the current three:

1. **`Skill`'s load path** — a slug the agent may not use is refused with a message that says so
   (per ADR-060's structured tool-failure family), never a silent empty result.
2. **`Skill`'s search path — the match list, filtered after ranking** (ADR-071 §3.2.2).
3. **The menu** — already correct (`BuildSkillsSummaryFunc(cb.skillAllowed)`).
4. **The `/<skill>` human shortcut** — already correct, via `ResolveSkillName`
   (`pkg/agent/loop.go::applyExplicitSkillCommand`). A person cannot push a skill onto an agent
   that is not permitted it.
5. **`list_skills`** — F1. Today it returns every installed skill *with its path* to every agent.
   It must take the same allowlist predicate the menu takes, and it must stop returning `path` at
   all (see §6.6 on why the path is the load-bearing field).

`create_skill` / `edit_skill` / `remove_skill` are **out of scope** for the grant list: they are
authoring verbs over the shared registry, governed by tool policy (Constraint #6) rather than by
which skills an agent may *run*. Recorded so it is not read as an omission.

### D4.1 — The gate is per-shelf: which grant instrument answers for which shelf (CRIT-001)

r1 left this implicit and the two halves of the design answered it oppositely: D4 said the grant
list gates every door, D6 promised a mounted repository's skills "come along… no author effort."
Both cannot be true of a single flat slug list. `skillAllowed` is shelf-agnostic — a plain
case-folded map lookup (`pkg/agent/context.go::skillAllowed`) with no notion of where a slug was
served from — so the reconciliation has to be stated, not inferred.

**Decision: every shelf is gated, but each shelf has its own grant instrument.**

| Shelf | Grant instrument | Scope of the grant |
|---|---|---|
| **1 — Project** (a mount's `.claude/skills/`, `.omnipus/skills/`) | **The mount itself.** Creating the mount grants its own skills to agents acting in that workspace. Not slug-listed anywhere. | That workspace only, and only that mount's own slugs |
| **2 — Registry** (`$OMNIPUS_HOME/skills`) | The per-agent grant list — `skillAllowed`, D5 semantics | The agent, everywhere (§6.5) |
| **3 — Builtin** (the 4 embedded) | Same as the registry — they are ordinary entries in `ListSkills` and are filtered by the same predicate | The agent, everywhere |

So D4's "a gate must sit at every door" stands **exactly as written** — no door is ungated. What r1
failed to say is that the *answer* at the project door comes from the mount, not from the slug list.
This is the same shape the project already uses elsewhere: ADR-037 made delegation trust a property
of the workspace team rather than a global agent attribute, for the same reason — the grant belongs
where the relationship lives.

**Why the mount, and not pre-granting by slug.** A repository's skill slugs are not knowable until
it is mounted, so a slug-list gate would mean the operator granting names they cannot yet see, or
editing every agent's grant list after every mount — a workflow no part of this design describes and
no UI offers. It would make D6's headline promise unreachable in practice. And §6.3 already settled
that mounting is the trust decision: mounting grants **write** access to the folder, which is
strictly more than "may run the skills in it."

**The security consequence, stated rather than buried.** A mount can hand an agent invokable skills
the operator never granted by slug. That is a real widening over r1's implied reading, and it is
bounded three ways:

1. **Workspace-scoped, not global.** Project skills are visible only to an agent acting in that
   workspace — which is what D8's (agent × workspace) cache key exists to make true. The same agent
   in another workspace sees none of them.
2. **A project skill may never replace a slug the agent already holds from the registry** — see
   D4.2, which closes MIN-004.
3. **Never auto-loaded.** Like every other skill under D1, a project skill enters a turn only when
   the agent calls `Skill` (or a human types `/<slug>`). Discovery is not execution.

### D4.2 — Slug collisions: project skills may add names, never replace granted ones (MIN-004, MAJ-001)

D6's general rule is shelf precedence — most specific wins, project over registry over builtin.
That rule is correct for the ordinary case and is why shelf 1 exists. It has exactly one carve-out
and one ordering rule:

**Carve-out (cross-shelf, security-relevant).** A project skill **may not shadow a registry slug
this agent has been granted.** On such a collision the granted registry skill wins and the collision
is logged with both paths. Rationale: under D5 an agent's registry skills are *exactly* its
explicitly granted slugs, so a grant is a deliberate operator statement about what that name means.
A file appearing in a repository — possibly written by an agent, since mounts are writable (§3) —
must not silently redefine it. Without this, MIN-004's path is open: an agent writes
`.claude/skills/<granted-slug>/SKILL.md` inside a mount and takes over a name it was granted but has
never been able to read, with no signal to anyone. Project skills carrying **new** slugs are
unaffected, which is the common case, so D6's promise survives intact.

**A dangling grant does not compete.** If the agent's grant names a registry slug that is no longer
installed, and a mount carries a project skill under that same slug, **the project skill resolves
normally**. The carve-out above protects a granted registry skill from being displaced; it is not a
reservation on the *name*. "The agent holds it" is meaningless for a skill that no longer exists to
be held, and the alternative — a stale grant entry silently shadowing a present, working project
skill with nothing behind it — is precisely the silent-never-fires failure §3 calls this design's
biggest accepted risk. Skill packages get removed from a shared library on a cadence that does not
track individual agents' grant lists, so this is an ordinary production sequence, not a corner case.

**Ordering rule (cross-mount).** When two mounts on the same workspace carry the same slug,
**mounts are ordered by mount name and the first wins** — the same deterministic rule D7 already
uses for instruction files, chosen for the same reason (mount names are stable, operator-chosen, and
already unique within a workspace per `ErrMountNameCollision`). The collision is logged with both
mount names and both paths. Silent, load-order-dependent shadowing between two repositories is the
outcome this exists to prevent.

Both collision cases are also visible where it matters: the menu already emits `<source>` and
`<location>` per entry (`BuildSkillsSummaryFunc`), so a reader can always see which shelf and which
file a listed slug resolves to.

**What this gate is, honestly.** These five doors govern *invocation and discovery*. On their own
they would leave the grant a **routing and curation gate** — which skills an agent is steered to and
permitted to invoke — rather than a confidentiality boundary, because ADR-062's reads-open model
lets an agent that already knows a slug read the file directly. The founder chose not to leave it
there (§6.6): **D10 closes the read path too**, with the precise, per-platform limits stated there.
Read D4 and D10 together — D4 is the gate, D10 is what stops the wall having a door beside it.

### D5 — Absence of a grant list means *no* skills, matching the shipped contract

F2's divergence resolves in the contract's favour: `Agent.yaml` already says "opt-in, default none",
that text is already published, and "no list ⇒ unrestricted" is the same
punish-the-specific-operator inversion this ADR exists to remove. `skillAllowed` returns `false`
when `skillAllowlist == nil`; `WithSkillAllowlist` no longer treats nil specially.

**There is no migration path, and none is designed** (§6.2). v0.3 is a fresh build with no
back-compat — CLAUDE.md's Release Strategy states it for the whole phase ("v0.3 / 1.0 — Workspaces
redesign … Fresh-build, no back-compat"), and the founder confirmed it for this ADR specifically.
So the "agents with no list silently lose access" concern does not arise: there is no existing
installation whose behaviour this preserves or breaks.

### D5.1 — `coreagent.SeedConfig` will silently undo D5 on every boot. Gate it (CRIT-002)

r1 asserted that "new installs are seeded correctly from the start, by the same
`coreagent.SeedConfig` path that seeds every other per-agent default." That sentence was true about
fresh installs and wrong about every boot after one. **Re-verified independently against source for
r2** (not taken from the review, per this project's reproduce-before-acting rule):

- `SeedConfig` computes `isFreshInstall := len(existing) == 0` and uses it to gate the AutoRecap
  seed and the `DefaultAgentID` singleton seed — the latter with an explicit comment that it is
  "a fresh-install-only seed, not a re-enforcement that would overwrite an operator's later choice
  on every subsequent boot."
- The block that follows — `// Re-enforce identity fields on existing core agents (tamper
  protection + rename)` — is **not** gated by `isFreshInstall`. It runs on every boot, for every
  core agent in config.
- Inside it: `if len(a.Skills) == 0 { if seedSkills := coreAgentSkills(ca.ID); len(seedSkills) > 0
  { a.Skills = seedSkills; modified = true } }`, and `coreAgentSkills` returns a non-empty list for
  the whole roster.

Under D5, `len(a.Skills) == 0` stops meaning "never configured" and starts meaning **"the operator
deliberately granted nothing"** — a state the shipped contract already calls valid. This code cannot
tell the two apart, so it would restore the seeded grants on the next restart: a control that
reports success and does not stick. That is the ADR-054 D6.4 failure mode this repository already
treated as a release blocker, and CLAUDE.md records it as such.

**Decision: gate the skills block behind `isFreshInstall`.** This is an implementation requirement
of this ADR, not an aside. Three reasons it is the correct fix rather than the easier one:

1. **The code's own comment says so.** Unlike the `Locked` / `Name` / `Description` / `Color` /
   `Icon` / `Type` re-enforcements it sits beside — which are unconditional, genuinely asserted
   identity for a locked agent — the skills block is guarded by `len(a.Skills) == 0` and its comment
   calls it an *"Idempotent skill-allowlist migration (FR-9.4) … Upgrades from a release that
   predated allowlists therefore gain the default matrix."* It was written as a **migration**, and
   it already intends to respect operator choice ("an operator who has customized the agent's skills
   keeps their choice"). D5 simply widens what counts as a choice; the guard cannot follow.
2. **Under §6.2's greenfield answer that migration has nothing left to do.** Its entire stated
   purpose is upgrading installs that predate allowlists. There are none. What remains legitimate is
   the fresh-install seed — which is exactly what `isFreshInstall` expresses.
3. **The alternative would break the feature.** Exempting the core roster from D5 as "asserted, not
   operator-editable" config would mean the Agents UI must stop offering the skill list as editable
   for locked core agents — and granting Mia a skill is the single most ordinary thing this whole
   design exists to enable. The wire contract (`Agent.yaml`) also presents `skills` as ordinary
   per-agent state with no locked-agent carve-out.

**Test obligation (§9, T1c):** run `SeedConfig` twice in sequence with an operator-set empty list
in between, and assert it stays empty. The review is right that nothing today would catch this.

### D6 — A project's skills ride the mount that is already there

**The tension, stated before it is resolved.** A coding project carries its own skills in its own
folder and its own instruction file explaining how to work on *this* codebase. That is a
**filesystem** idea — it lives in a directory, travels with the repository, appears the moment you
are working inside it. An Omnipus **workspace** is not that: it is a project in the business sense —
a team of agents, a board, a shared memory room, a set of conversations — which *might* point at a
repository and might be a purely conversational space with nothing on disk at all.

**The decision is to add no new concept.** ADR-063 D4 already answers "let agents work in my own
folders": a **mount** is a named write-grant on a real local folder, realpath-resolved at create
time, surfaced inside the workspace's `work/<name>`, and stored in the protected entity store
(`$OMNIPUS_HOME/entities/mounts/<workspaceID>.json`) deliberately out of a sandboxed child's reach.
ADR-063 **D7** deleted the old `Workspace.repository` field with no back-compat precisely because it
was a setting that did nothing; mounts replaced it and work.

> **The rule: when a folder mounted on a workspace looks like a code project, that project's skills
> and instructions come along. When nothing mounted looks like one, nothing happens — no setting,
> no warning, no opt-out.**

That last clause is what dissolves the tension: the mechanism is **silent when there is nothing to
find**, so a conversational workspace never encounters it and nobody has to understand it until it
is useful to them. Multi-repository work needs no new design, because a workspace can already carry
several mounts — the collision rule for that case is **D4.2**.

**"Looks like a code project" is prose, not a heuristic — do not build one** (MIN-002). There is no
unified project-detection test anywhere in this design, and specifically **no `.git` check**. There
are two entirely independent path-existence checks that can each be true without the other:

| Mechanism | Triggers on | Independent of |
|---|---|---|
| Project skills (D6) | a `.claude/skills/` or `.omnipus/skills/` directory existing in the mount | whether any instruction file exists |
| Project instructions (D7) | a root `CLAUDE.md` or `AGENTS.md` existing in the mount | whether any skills directory exists |

A mount with skills and no `CLAUDE.md` contributes skills only; a mount with `CLAUDE.md` and no
skills directory contributes instructions only; a mount with neither contributes nothing and costs
two `stat` calls. Reading "looks like a code project" as an instruction to write one
`isProjectFolder()` helper would couple two mechanisms that must stay separable.

**Are project skills subject to the per-agent grant list?** No — the mount is the grant. That is
**D4.1**, which states the full per-shelf model and the security consequence; D6 and D4 are
reconciled there rather than each implying a different answer.

**Three shelves, most specific wins** — and the loader already has exactly these three, in exactly
this order (`SkillsLoader.SkillRoots`, `ListSkills`, `LoadSkill`):

| Rank | Shelf | Today | Under this ADR |
|---|---|---|---|
| 1 | The project's own skills | `<agent workspace>/skills` — **vestigial, nothing writes to it** (F4) | the mounted project folder's skills dir |
| 2 | The installed registry | `$OMNIPUS_HOME/skills` — the shelf grants are about | unchanged |
| 3 | What Omnipus ships | the 4 embedded skills | unchanged |

So this is **a change of address for a shelf that already exists** and is already understood by
every consumer (menu, `LoadSkill`, cache-invalidation via `skillRoots`), not new machinery. And the
file format already matches: an Omnipus skill and a Claude Code skill are the same `SKILL.md` on
disk, so a repository's existing skills are readable as-is.

**Which folder inside the project** — read both `.omnipus/skills/` and `.claude/skills/`, ours
winning a slug clash, consistent with the existing shelf precedence. Grounded in CLAUDE.md Hard
Constraint #5 (ecosystem compatibility): refusing the folder name that actually exists in
repositories today buys nothing and costs day-one usefulness. See §5, OQ6.

**Trust: mounting the folder IS the trust decision** (§6.3). There is no separate "trust this
project's skills" switch, off by default, and no per-skill approval prompt. Creating a mount is
already a deliberate, explicit operator action with its own REST call and its own UI — ADR-063 D4
makes a mount a *write*-grant on a named real folder, realpath-resolved at create time, recorded in
the protected entity store out of any agent's reach. An operator who has granted agents write access
to a folder has already made a larger trust decision than "may read the skills in it"; a second
switch would be asking the same question twice and would train people to click through it. The
consequence is stated plainly rather than mitigated: **cloning a repository and mounting it is
enough to give your agents new instructions.** The mount UI copy should say so at the moment of
mounting, which is the moment the decision is actually being made.

This does not weaken the *installed*-registry path: `install_skill` keeps its hash verification and
`config.SkillTrustLevel` gate (SEC-09). Mount-sourced and registry-sourced skills reach the agent by
different routes and keep different checks, which is correct — one is a folder you chose, the other
is a download.

### D6.1 — Authoring a project skill writes into the project (r3, founder decision)

When an agent edits a skill that belongs to a mounted project, the write goes **into that project's
own file**, in the repository, where the skill actually lives. It does not fork a copy into the
central registry.

The spec round offered three options: refuse the edit; allow it but fork a shadowing copy into the
library; or write into the repo. **The founder chose to write into the repo**, against this ADR's
recommendation to refuse. That is the right call and the recommendation was over-cautious — a
forked copy would have created two skills with one name and a precedence rule nobody could hold in
their head, and refusing would have made project skills read-only for no defensible reason.

**The apparent conflict with D10 is a false premise, and this is the part worth being precise
about.** The objection raised at spec time was: *an agent could overwrite a project skill it is not
permitted to read, because D10 routes skill reads through the tool.* That is wrong, and it is worth
saying exactly why rather than adding a guard against a problem that does not exist:

> **D10 changes how a skill is read, not whether it can be.** D4.1 makes the mount the grant
> instrument for project skills, so **every agent acting in that workspace may load every skill in
> that mount** via `Skill`. D10 Part B refuses `read_file` on the mount's skills subtree — it
> redirects the access path, it does not deny access. There is therefore **no project skill an agent
> can write but cannot read**. The read/write asymmetry the objection assumed is not present.

**So the narrow new decision is: skill-authoring writes are not gated by the read gate, and no
load-first precondition is imposed.** Three reasons:

1. **§6.4 already accepted exactly this.** "A mounted project folder is writable by the agents
   working in it — that is why you mounted it," accepted and documented rather than mitigated. An
   authoring write into a mount is that accepted behaviour, not an extension of it.
2. **The capability already exists through a more general tool.** `write_file` can already write to
   any path in the mount, including the skills subtree — D10 gates *reads*, never writes. Refusing
   the same write in `edit_skill` would block nothing an agent could not do one tool call away. That
   is security theatre with a real usability cost, which is the pattern ADR-037 exists to warn about.
3. **A load-first precondition would buy nothing here.** It would only matter if writing were
   possible where reading is not, and per the box above it never is.

**What does change, and must be built:**

- The authoring verbs become **shelf-aware**. Today `SkillWriter` is rooted at the global skills
  directory (`pkg/skills/authoring.go`, `NewSkillWriter(skillsGlobalDir)`) and a project slug is not
  addressable through it at all. Resolving a slug to *its own shelf* and writing there is new work,
  and it is what makes this decision real rather than nominal.
- A write to a project skill is **confined to the mount** by the same path-safety rules every other
  mount write obeys. `SkillWriter`'s existing traversal confinement applies with the mount root as
  its root.
- **`remove_skill` follows the same rule** — deleting a project skill deletes the project's file.
  (This supersedes the spec's earlier A4 resolution, which assumed refusal on the same
  global-root grounds this decision removes.)
- **Writes are audited** with the shelf and resolved path, extending D3.1 to the authoring verbs.
  This is the one place the "accepted, documented" posture of §6.4 needs a record: an agent editing
  the instructions it will later follow should leave a trail, even though it is permitted.
- **D4.2 is unaffected and still protects the dangerous case.** An agent writing a project skill
  whose slug collides with a granted registry slug does not thereby hijack that slug — the granted
  registry skill still wins on resolution, and the collision is still recorded.

### D6.1.1 — The write audit belongs at the path resolver, not the authoring tool (r4)

D6.1 accepted ungated authoring writes partly on the grounds that *"writes are audited… so the
§6.4-accepted behaviour leaves a trail."* The spec review found that reasoning self-defeating, and
it is right: **the same paragraph concedes `write_file` reaches the same paths**, so an audit scoped
to the authoring tool covers the one route an agent has no reason to prefer. `pkg/tools/edit.go` has
no audit call today (verified). An agent editing a project skill via `write_file` would produce **no
record at all** — precisely the repudiation gap the trail was invoked to close.

**Decision: audit at `tools.ResolvePath`, not in the authoring tool.** Every file tool already routes
through it (`pkg/tools/resolvepath.go`), and it is the same layer that must already recognise "this
path is under a mount's recognised skills directory" to enforce D10 Part B's read gate. Putting the
hook there makes the guarantee tool-agnostic by construction rather than by enumeration:

- **Any write whose resolved path lands under a recognised skills directory is audited**, whichever
  tool performed it — `write_file`, `edit_file`, the authoring verbs, anything added later.
- **The record carries the same fields** as D3.1's (shelf, resolved path, agent, workspace) plus the
  tool that performed the write, so the sanctioned and generic routes are distinguishable.
- **It reuses the classification the read gate already needs**, so this is one predicate serving two
  purposes rather than a second path-matching implementation that could drift from the first.

This is the difference between "we audit the door we built" and "we audit the doorway." Only the
second survives someone walking round the side, and D6.1's acceptance of ungated writes is only
honest with the second.

**Still not closed, and stated rather than implied:** `bash` can write to the same paths on every
platform (writes are never text-guarded, and on Windows nothing confines a child at all — D10.2).
The resolver hook covers the in-process file tools, which is every tool the agent normally uses, and
it does not cover a shell child. So the trail is **complete for tool-mediated writes and incomplete
for shell-mediated ones** — a materially stronger claim than r3's, and still not a total one.

**`.preview-doc` is not being contradicted.** `spaces.html` states skills stay a global registry —
and, in the same paragraph, flags the overlay explicitly: *"Open option for the ADR: if the
per-agent skills overlay re-points at the Workspace as a side effect of re-rooting, 'skills
available in this Workspace' could become an opt-in — but that's a deliberate choice, not today's
behaviour."* This ADR takes that option. It does not make the registry per-workspace; grants still
address the global registry.

### D7 — A project's instruction file is a second source on the rail that already exists

Omnipus already injects exactly one instruction note per turn: the workspace's own **Project
Instructions** (`workspaces/<id>/AGENT.md`), read by
`pkg/agent/workspace_instructions.go::buildWorkspaceInstructionsNote` and inserted as a `system`
message by `::injectWorkspaceInstructions`. A repository's `CLAUDE.md` is the same kind of thing
written by a different tool for the same purpose.

So it becomes a **second source for the layer that already exists**, not a new layer with its own
rules: the workspace's own instructions first, then each mounted project's file, each labelled with
its mount name so the agent knows which is the operator's and which came with the code. The
ordering already makes the operator's intent outrank the repository's.

Mechanics, all reusing what is there:

- **Root file only**, both `CLAUDE.md` and `AGENTS.md` accepted, `CLAUDE.md` winning if both exist.
  Not per-subdirectory — an unconditional, always-injected block must be bounded and predictable.
- **The existing byte cap applies**, `workspace.maxInstructionsBytes` (262144), across the whole
  composed note rather than per file, with the same truncate-and-WARN behaviour `ReadInstructions`
  already implements — plus a visible marker in the note itself where truncation occurred, because
  silently truncating instructions is worse than not loading them.
- **Every mount contributes**, ordered by mount name (deterministic), under that one shared budget.
  Rejected the alternative of a "primary" mount: it adds a setting, and silently ignoring two of
  three mounted repos is a worse failure than a longer note the operator can see and trim.

**Skills are offered; instructions are imposed.** That asymmetry is the reason the two halves get
different rules, and it is worth stating: a skill nobody loads costs one line, so all mounts may
contribute freely; an instruction file is unconditional, so it is capped and ordered.

### D8 — Prerequisite: assembled context is cached per agent and is workspace-blind

`BuildSystemPrompt()` takes no workspace id. The assembled prompt — including the `# Skills` menu —
is cached per `ContextBuilder`, i.e. per agent, invalidated only by mtimes on its own tracked
sources (`buildCacheBaseline`, `sourcePaths`, `skillRoots`). Only `buildDynamicContext(workspaceID, …)`
and the instructions note are workspace-aware. Move the same agent between two workspaces and its
menu does not change.

**A per-workspace skill menu is not correct until this is fixed.** The workspace id must reach the
static-prompt build and become part of the cache key. This is a genuine prerequisite, not a detail,
and the fix must not degrade into scanning a mounted repository on every message to decide whether
the cache is stale — the existing `skillRoots` mtime check is the right shape (a handful of `stat`
calls on directory roots), and adding mount roots to it keeps it that shape.

**The cache must also be bounded, and membership changes must evict** (MAJ-004). Going from one
cached prompt per agent to one per (agent × workspace) turns a fixed cost into one that scales with
workspace membership — in the hottest cache in the prompt path, and §6.5 keeps grants global so an
agent sitting on many workspaces is the normal case, not the pathological one. Three requirements,
none of which r1 stated:

1. **A hard per-agent cap on cached variants, LRU by last-used**, evicting the least-recently-used
   workspace entry on overflow. A miss costs one prompt rebuild — the same work the mtime path
   already does — so a small cap is cheap to be wrong about in the safe direction.
2. **Removing an agent from a workspace evicts that entry.** The existing mtime check answers "did a
   tracked file change"; it cannot answer "is this agent still on this team", because nothing in the
   skill roots changes when membership does. That is a separate invalidation trigger on the
   workspace-team write path, not a variation of the staleness check.
3. **Deleting a mount, or a workspace, evicts every entry keyed to it.** Same reasoning: the mount
   record lives in the entity store, not under a tracked skill root, so its removal is invisible to
   an mtime sweep.

Getting (2) or (3) wrong is not a leak of stale text, it is a **stale menu offering skills the agent
should no longer see** — which makes it a correctness requirement of D4.1's workspace scoping, not a
performance nicety.

### D8.1 — The cache bound must be byte-aware, because D1.1 made variant size unbounded (r5)

r3's D1.1 removed the menu cap. r2's D8 bounded the cache by **variant count**. Each was reasoned
through carefully on its own; **their product was never checked against CLAUDE.md Hard Constraint #3
("security-feature RAM overhead < 10MB beyond baseline")**, and §6 of the derived spec asserted that
constraint was satisfied by citing D8 — which bounds a different quantity.

**The arithmetic, using this ADR's own anticipated worst case** (D1.2 exists because a 5000-skill
mount is considered realistic). A menu entry is the XML shape `BuildSkillsSummaryFunc` emits, minus
`<location>` (removed by D4/F1): ~127 bytes of scaffolding plus slug, display name, description and
source. That is ~302 bytes at a realistic 120-char description and ~1206 bytes at
`MaxDescriptionLength` (1024).

| Mount size | One variant (typical) | One variant (worst) | 8 variants, 1 agent (typical) | 8 variants × 4 agents (typical) |
|---|---|---|---|---|
| 5000 skills | **1.44 MB** | 5.75 MB | **11.5 MB** | 46.1 MB |
| 500 skills | 0.14 MB | 0.58 MB | 1.2 MB | 4.6 MB |

**A single agent holding eight cached variants of one 5000-skill workspace is 11.5 MB — already past
the entire budget for every security feature combined, before anything else is counted.** Even the
plausible 500-skill case reaches 4.6 MB typical and 18.4 MB worst across four agents. A count-only
LRU cannot prevent any of this, because it is indifferent to the three-orders-of-magnitude spread in
what a variant weighs.

**Decision: the cache evicts on count OR aggregate bytes, whichever binds first**, with two
qualifications that matter:

1. **A variant larger than the whole byte budget is never cached** — it is rebuilt each turn. Without
   this, one giant workspace evicts every other entry on every insertion and the cache degrades into
   a thrashing single-slot buffer, which is worse than not caching that variant at all.
2. **The byte budget is measured on the assembled string actually retained**, not estimated from
   skill count, so it stays correct if entry shape changes later.

**This does not re-open D1.1, and the distinction is the same one D1.2 already drew.** The founder
rejected *a cap on the menu* — a per-turn truncation of what the agent can see. This bounds *how many
assembled copies are retained in memory*. A cache miss costs a rebuild (the work the mtime path
already does); it never truncates a menu. The agent sees every skill it may use, always, exactly as
D1.1 requires.

**And it makes D1.2's warning honest.** D1.2 warns at mount-add time on skill *count*, which the
spec review correctly noted is not the metric that matters for memory. With a byte-aware cache the
count warning is a proxy for a cost that is now genuinely bounded downstream, rather than a proxy for
one that was not bounded at all.

### D9 — Delegating with a skill: two mechanisms, and only one of them is reliable

The founder's question (§6.1): *"one path is still missing — if the main agent wants to really
execute the skill in a subturn/subagent style, how would that work?"* Two distinct things are
wanted, they are not the same thing, and conflating them is what makes this look ADR-032-shaped
when it need not be.

**The rule this must not break.** ADR-032 says a delegated sub-turn runs as the TARGET agent's own
instance — identity, workspace, context, tools, tool policy, model quad, all sourced from the
target, none from the parent. Its own carve-out names the single legitimate parent-sourced channel:
`SubTurnConfig.SystemPrompt`, which despite its name is **not** a system prompt — it becomes the
child's **first user message** (`pkg/agent/subturn.go`'s `processOptions` literal:
`UserMessage: cfg.SystemPrompt, // Task description becomes the first user message`). The child's
real system prompt is resolved from the target's own `ContextBuilder` and is regression-tested as
such (`TestSpawnSubTurn_NativeDispatch_SystemPromptComesFromTargetContextBuilder`).

**Mechanism 1 — encourage. Already works; no new machinery.** The parent names the skill in plain
language inside `task`. It lands in the child's first user message. The child's own menu, own
judgement and own `Skill` call decide whether anything happens. Zero enforcement, zero guarantee —
this is the "hopeful" version, and it is the correct default for the ordinary case.

**Mechanism 2 — request. New, and it is deterministic.** A new `requested_skill` parameter on
`delegate` (`action: "run"` only), naming one skill slug.

> **Name it `requested_skill`, not `suggested_skill`.** It behaves as a hard request: either the
> child starts with that skill loaded, or the delegation is refused. A parameter named *suggested*
> that can fail the call would be lying about its own semantics.

The mechanism is three existing pieces wired together, and **nothing new crosses the parent→child
boundary except the slug**:

1. **Transport.** `requested_skill` rides the same discretionary, parent-authored channel the
   existing `snapshot` parameter already uses — `spawnSubTurn` appends snapshot text to
   `cfg.SystemPrompt` (the task prompt), never to the child's context builder. Same channel, same
   deny-by-default discipline. The parent contributes a *name*; it never contributes content.
2. **The gate is the receiver's, structurally — not by convention.** Resolution runs
   `execSource.ContextBuilder.ResolveSkillName(requested)` — a method on the **child's own**
   ContextBuilder, which applies the **child's own** `skillAllowed`. `execSource` is already the
   resolved delegate target (or the parent, for self-delegation), and is already what every
   agent-level field is read from. The parent's own grant list is never consulted and cannot be:
   there is no code path from the parent's ContextBuilder into this decision.
3. **Execution is reliable, via the mechanism that already does exactly this.**
   On success the slug is appended to the child's `processOptions.ForcedSkills` — the same one-shot,
   per-turn field the human `/<skill>` command already uses
   (`pkg/agent/loop.go::applyExplicitSkillCommand`), consumed by
   `activeSkillNames` → `buildActiveSkillsContext`. The child's first turn therefore *begins* with
   the skill's instructions loaded. Not "maybe picks it up from context" — loaded, deterministically,
   by the same path a slash-command activation takes.

Three outcomes, and the failing two are loud:

| Outcome | Behaviour |
|---|---|
| Child is granted the skill | Loaded into the child's first turn via `ForcedSkills`. The `delegate` result names which skill was loaded, so the parent knows it took effect. |
| **Child is not granted it** | **The delegation fails at dispatch** with a structured error (ADR-060 family) naming the target agent and the skill. Never silently ignored — the founder's explicit requirement. |
| Slug does not resolve | Same clean dispatch failure, distinguishable from the denial. |

**Why this does not violate ADR-032.** The parent supplies a slug on the task-prompt channel; the
child's own grant list answers it; the child's own one-shot activation field carries it. No
agent-level setting is parent-sourced — not the context builder, not the tool set, not the policy,
not the model quad. The comparison worth making: this is *less* parent influence than the existing
`snapshot` parameter, which puts parent-authored **text** in front of the child. `requested_skill`
puts a parent-authored **request** in front of the child's own gate.

**And it makes the gate strictly stronger, not weaker.** Under mechanism 1 a parent can name a skill
in prose and the child may load it — if the child is granted it. Under mechanism 2 the same
authority applies, but the outcome is visible and auditable instead of implicit. Neither mechanism
can put a skill in front of an agent that was not granted it.

**The loud failure is an enumeration oracle. Accepted, with the reason stated** (MAJ-002). The
founder's "never silently ignored" requirement means a refusal names the skill and the agent — so
any agent permitted to delegate to a target can iterate `requested_skill` values and read pass/fail
to enumerate that target's entire grant list. This capability does not exist today, and r1 ran
exactly this analysis for D10's file paths while never asking it of D9's own failure mode.

**Accepted, not mitigated**, for three reasons:

1. **Delegation is already the higher privilege.** An agent that may delegate to a target can make
   that target *run work* — a far larger capability than learning which skills it holds. The
   delegation edge is itself workspace-scoped and operator-configured (ADR-037), so this discloses
   an agent's skill posture only to peers already trusted to task it.
2. **The information is close to public already.** A delegating agent can observe the target's
   behaviour, read its outputs, and — post-D6 — sees the same project shelf in the same workspace.
   Skill grants are a capability profile, not a secret.
3. **The generic-refusal alternative costs exactly what the founder asked for.** Collapsing
   "not granted" and "no such skill" into one opaque error is the only real mitigation, and it
   destroys the debuggability that motivated the loud failure in the first place — an operator
   would be unable to tell a typo from a missing grant.

Recorded so the trade-off is a decision rather than an oversight. If it is ever revisited, the lever
is the failure message's granularity, not the mechanism.

**Not adopted:** synthesising a fake `Skill` tool call in the child's transcript to make the
activation "look" agent-initiated. `ForcedSkills` already renders an `# Active Skills` block and is
the honest representation — the skill was activated by dispatch, not chosen by the child, and the
transcript should say the true thing. The delegation span records the requested skill for
observability instead.

### D10 — Skill content is readable only through the tool

§6.6, decided the strong way: the grant is a real gate, not curation. `read_file` (and, where the
platform permits, `bash`) must not be a way around it. The mechanism splits cleanly in two, because
the two kinds of skill live in structurally different places.

**Part A — the installed registry (`$OMNIPUS_HOME/skills`). This fits the existing mechanism
exactly.** `pkg/fspolicy` already maintains the single definition of what no agent may reach:
`SecretEntriesAlways` (context-free: `master.key`, `credentials.json`, `config.json`, `cli.token`,
`entities`, `auth.json`, `backups`) and `SecretEntriesPerTurn` (`agents`, `workspaces` — denied
except the caller's own tree). These are **whole directories anchored at `$OMNIPUS_HOME`**, and
`skills` is a sibling of exactly that shape.

> **Before trusting ADR-062 §4.0's table, know that it is already wrong about a different row**
> (MAJ-003). Verified independently for r2: `SecretEntriesAlways` already contains `"system"`, with
> a long comment giving the v0.2 HMAC-chain rationale (the audit chain detects a child *modifying*
> an entry but not `rm system/audit.jsonl`). ADR-062 §4.0's table still lists `system/` in the
> "must stay agent-accessible" column, beside `logs/` and `browser/`. **The table and the code have
> silently diverged, with no recorded amendment.** That is precisely the failure this ADR's own
> "one authority" discipline is trying to prevent for `skills/` — so the `skills/` amendment must
> **also correct the `system/` row in the same edit**, or "one authority" is false the moment this
> ships. Flagged here rather than fixed silently, so nobody mistakes the pre-existing drift for
> something that was reviewed and accepted.

**Decision: add `skills` to `SecretEntriesAlways`, and amend ADR-062 §4.0's table** — moving
`skills/` out of the "must stay agent-accessible" column, **and moving `system/` with it**. That is the honest framing: this reverses
a specific line of an accepted ADR, and should be recorded as an amendment rather than smuggled in
as a path-scoped special case. A path-scoped deny would be the *less* consistent option here, because
`fspolicy` has no pattern or glob facility at all — `IsCarveOut`, `CoversForDeny` and `CoversForGrant`
are containment tests resolved by filesystem identity. Directory-shaped is the only shape the
mechanism speaks, and `skills/` already is one.

This buys both enforcement layers at once, which is the point: the app-layer resolver
(`tools.ResolvePath`) blocks the in-process file tools, and `sandbox.DeriveKernelPolicy` →
`fspolicy.KernelDeniedPathsFor` puts it in the kernel ruleset for spawned children. The precedent is
exact — `derive_from_fspolicy.go`'s own comment records that reading "another agent's SOUL.md …
succeeded from `bash`" and that `KernelDeniedPathsFor` is what closed it.

**The `Skill` tool needs no bypass, and this is the part that makes Part A cheap.**
`SkillsLoader.LoadSkill` reads with plain `os.ReadFile`, in-process, inside the gateway — it never
goes through `tools.ResolvePath`, so the app-layer deny does not apply to it. The deny applies to
the *tool* boundary, and the loader sits below it. No carve-out, no privileged read path, no new
seam.

> **One implementation-time question this ADR cannot close by design: Linux.** CLAUDE.md records
> that on Linux the Landlock ruleset confines *the gateway and its children*, because Landlock is
> per-thread and inherited (on macOS only children are confined). If `$OMNIPUS_HOME/skills` becomes
> a kernel deny on the gateway's own threads, `SkillsLoader` cannot read it either and the whole
> feature breaks. The deny must therefore be **child-only** on Linux — expressible, since the child
> ruleset is built separately at spawn (`pkg/sandbox/hardened_exec*.go`), but it is a real
> distinction that does not exist today and it must be verified on a Linux host, not reasoned about.
> **This needs a spike before implementation.** Getting it wrong fails closed and loudly (skills
> stop loading), not silently — which is the right failure direction, but it is still a blocker for
> the Linux build rather than a detail.

### D10.1 — Correction to Part A: `skills` must be path-denied but NOT text-guarded (r3)

**r2's Part A prescribed a change that would break the `bash` tool, and the codebase's own coupling
test would force the breakage rather than catch it.** Found during the spec round; recorded here
because Part A as written above is wrong without it.

`SecretEntriesAlways` has **two consumers with different semantics**:

1. **Path denial** — `DeniedPathsFor` / `KernelDeniedPathsFor` / `SecretPathsAlways`, feeding the app
   resolver and `DeriveKernelPolicy`. This is what Part A wants.
2. **A literal-text guard on shell command strings** — `pkg/tools/shell.go::buildSecretGuardPatterns`
   compiles **one word-boundary regex per entry** into `defaultDenyPatterns`, blocking any `bash`
   command whose *text* mentions the name, irrespective of paths.

Adding `skills` therefore auto-generates `\bskills\b` into the shell deny set, blocking **every
command containing the word "skills"** — `grep -r "skills" .`, `ls ~/projects/skills-demo`,
`git commit -m "add skills"`. And it cannot be quietly skipped:
`TestSecretGuardPatterns_CoverEverySecretEntryAlways` iterates every entry, runs `"cat " + name`, and
**fails if it is not blocked** — so adding `skills` makes the test *demand* the over-broad behaviour.
The existing false-positive guard checks five fixed commands, none containing "skills", so it stays
green.

**The code already documents the criterion this violates**, in the comment explaining why
`agents`/`workspaces` are deliberately excluded from the guard:

> "the per-turn half (`agents/`, `workspaces/`) is made of **ordinary English words an agent
> legitimately types constantly**… **The five ALWAYS names are never legitimate in ANY turn**, which
> is what makes a context-free literal match safe for them and not for the rest."

`skills` is unambiguously such a word. By the code's own membership test it does not belong in
`SecretEntriesAlways`.

**Decision: introduce a third category — path-denied but not text-guarded.** It feeds
`DeniedPathsFor`, `KernelDeniedPathsFor` and `SecretPathsAlways`, and explicitly **not**
`buildSecretGuardPatterns`. `skills` is its only member. This is the only option consistent with the
documented criterion, and it preserves the anti-drift property that motivated generating the guard
in the first place: two coupling tests, one per set, so neither can silently fall behind. Rejected:
excluding `skills` by name inside the builder (a special case that breaks the coupling test's premise
and reintroduces exactly the hand-copied drift generation was built to eliminate), and dropping the
fspolicy leg entirely (abandons the kernel enforcement that is most of D10's value).

> **⚠️ SUPERSEDED BY D10.3 (r6).** Part B below is retained for the reasoning it records, but the
> project-skill read gate it specifies **is removed**: D4.1 already lets every agent in the workspace
> load every skill in the mount, so the gate protected nothing and broke bundled files. Do not
> implement Part B. Part A survives, narrowed to instruction files.

**Part B — project skills inside a mount. Directory-shaped, per-turn, and narrower.** A mount is a
folder full of files the agent legitimately needs; the mount itself cannot be denied. But **D6
already defines project skills by *directory*, not by filename** — `<mount>/.claude/skills/` and
`<mount>/.omnipus/skills/` — and that is precisely what makes this enforceable:

> **How the system tells a project skill from an ordinary project file: by location, full stop.**
> A file is a project skill if and only if it lives under a recognised skills directory of a mount.
> No content sniffing, no `SKILL.md` filename matching anywhere in the tree, no heuristics. A
> `SKILL.md` sitting loose in a repo is an ordinary file and `read_file` reads it.

Those two subtrees per mount become **per-turn deny roots**, derived alongside the existing
`$OMNIPUS_HOME` carve-outs. The seam already exists and already has the mount list:
`DeriveKernelPolicy(authored fspolicy.FSPolicy, in TurnPolicyInput)` is the single function turning
an authored per-turn policy into a kernel policy, and the turn's mounts are already in
`FSPolicy.AllowedRoots` (populated from `workspace.AllowedMountRoots`). Everything else in the mount
stays ordinary.

**`CLAUDE.md` / `AGENTS.md` are deliberately NOT denied.** They are the *instruction* rail (D7), not
skills, and denying them would protect nothing while breaking real work:

- Their content is **already in the agent's context every turn, by design** — that is what D7 does.
  Denying the read hides nothing the agent does not already have.
- An agent asked to update a project's own `CLAUDE.md` is doing ordinary, wanted work.

So the founder's phrase "when used as skill-equivalents" resolves to: under D7 they are never
skill-equivalents. They are the always-injected instruction layer, and they keep ordinary file
access.

**What this actually closes — stated without overclaiming.** Three honest limits:

| | Registry skills | Project skills |
|---|---|---|
| `read_file` and the other in-process file tools | **Closed**, all platforms (app layer) | **Closed**, all platforms (app layer) |
| `bash` on macOS / Linux | **Closed** (kernel ruleset) — pending the Linux child-only spike above | **Closed** (kernel ruleset) |
| `bash` on **Windows** | **Not closed.** There is no Windows sandbox backend — `selectBackendPlatform` returns `FallbackBackend`, i.e. app-level only. A `bash` child reads any file. | **Not closed**, same reason |
| `git` object access inside a mount | n/a | **Not closed.** `git show HEAD:.claude/skills/x/SKILL.md` reads the same content out of `.git/`, which is not under a denied subtree. Denying `.git/` wholesale would break every legitimate git operation, so this stays open by choice. |
| Content after a *permitted* load | Open by construction, and correctly so — once a granted skill is loaded the agent has the text and can do anything with it. Nothing should close this. |

### D10.3 — The gate covers a skill's *instruction file*, not its *directory* (r6)

**D10 as written breaks the commonest real-world skill shape, and this is a functional defect, not a
security nuance.** Parts A and B both deny whole *directories* — `$OMNIPUS_HOME/skills` and each
mount's `.claude/skills/` / `.omnipus/skills/` subtree. But a skill directory routinely contains more
than its `SKILL.md`: helper scripts the instructions say to run, templates, reference files the
instructions say to read. Deny the subtree and a skill loads, tells the agent "run the script next to
me", and the agent cannot reach it. **Skills with bundled files simply do not work as authored.**

**First-hand evidence, not hypothetical.** The `plan-spec` skill used to produce this ADR's own
implementation spec bundles four sibling files — `spec-template.md`, `bdd-template.md`,
`test-dataset-template.md`, `examples/sample-output.md` — and its `SKILL.md` instructs the reader to
open them ("For the output document structure, see spec-template.md"). Producing that spec required
reading all of them. Under D10 as written, an Omnipus agent could not have.

**Why this was missed:** Omnipus's four embedded skills (`plan`, `summarize`, `daily-briefing`,
`skill-authoring`) are each a lone `SKILL.md` with no bundled files. The shipped set is the
unrepresentative case, so nothing on a fresh install would have surfaced it.

**Correction, in two parts.**

**(a) Part B's project-skill read gate is removed entirely — it protects nothing.** D4.1 makes the
mount the grant instrument: *every agent acting in that workspace may load every skill in that
mount.* D6.1 already relied on this ("there is therefore no project skill an agent can write but
cannot read"). A read gate over content the agent may load on demand anyway restricts nothing, and
its only observable effect is breaking bundled files. Part B was written symmetrically with Part A
without checking whether the symmetry was warranted; it was not. **A mount's skills directory reads
and executes like any other part of the mount.**

**(b) Part A's registry gate narrows from the directory to the instruction file.** What the founder
chose in §6.6 was that *using a skill* goes through the tool. Using a skill means following its
instructions — so the instruction file is what must be routed, and it is the only thing that must be:

| Path | Gated | Why |
|---|---|---|
| `<skill>/SKILL.md` (and a legacy `AGENT.md`/`AGENTS.md` inside a skill dir) | **yes** | This *is* the skill; reading it is using it |
| `<skill>/scripts/*`, `<skill>/reference.md`, templates, any sibling | **no** | Inert without the instructions that reference them |

Enforcement follows the layer's capability, which is the same discipline D10.1 established:

- **App layer, every platform** — `tools.ResolvePath` refuses a read whose basename is an instruction
  filename *and* whose parent chain sits under a skills root. Filename matching is trivial in Go;
  this is the layer that can express it.
- **Kernel layer, POSIX** — the per-turn policy enumerates the instruction files as deny paths (one
  per installed skill), computed from the `ListSkills()` the loader already performs. This is a
  bounded, per-turn enumeration over data already in hand, not the open-ended enumeration ADR-062
  rejected. **Its cost scales with the installed skill count and must be measured** — with D1.1's
  uncapped menu a very large registry means a correspondingly large ruleset, and D1.2's threshold is
  the existing signal for that. Dropping Part B removes the worst of this pressure, since mounts are
  where 5000-skill trees come from.

**What this costs, stated rather than glossed.** A determined agent can now read a bundled
`reference.md` belonging to a skill it was never granted, and an author could hide instructions there
to sidestep the gate. That is accepted: on the project shelf the agent may load the whole skill
anyway (D4.1), and on the registry shelf it means the gate covers the file that defines the skill
rather than every file near it. The alternative — keeping the subtree deny — buys that narrow case at
the price of every skill that bundles anything, which is most real ones.

### D10.2 — The boundary is POSIX-only, and Windows must be named, not implied (r4)

r2's D10 table already said `bash` on Windows was "not closed". The spec review showed that was too
soft in one direction and too vague in another: **US-7's headline — "a grant is a real boundary, not
a suggestion" — is simply false on Windows**, and r3 left it as a row in a matrix rather than a
scoped claim. Verified for r4:

- `pkg/sandbox/sandbox_other.go::selectBackendPlatform` (`//go:build !linux && !darwin`) returns
  `FallbackBackend`, with its own comment: *"this platform has no kernel confinement primitive at
  all… Enforcement here is application-level only."*
- `FallbackBackend.ApplyToCmd` (`pkg/sandbox/sandbox.go`) appends exactly two environment variables —
  `OMNIPUS_SANDBOX_MODE=fallback` and `OMNIPUS_SANDBOX_PATHS=…` — and returns. **It restricts
  nothing.** It is an advisory signal to a child that chooses to read it; `type C:\…\SKILL.md` does
  not.
- D10.1 deliberately keeps `skills` out of the shell literal-text guard, so that layer does not catch
  it either. All three layers are therefore absent on Windows for a spawned child.

**Decision: scope the guarantee explicitly rather than narrow it silently.** The read gate is
**enforced against spawned children on POSIX only (Linux, macOS)**; on Windows it is **app-layer
only** — the in-process file tools are gated, and a `bash` child is not. This is stated the way
CLAUDE.md already states the directly analogous case for `pkg/entity` ("the cross-process guarantee
for `pkg/entity` is POSIX-only — on Windows only the in-process striped mutex protects concurrent
entity writes"). That precedent is the house style for exactly this shape of platform gap, and this
ADR follows it rather than inventing a softer formulation.

Consequently:

- **US-7's claim carries a platform qualifier wherever it appears.** "A grant is a real boundary" is
  true on POSIX and best-effort on Windows.
- **An operator-visible warning** is emitted at boot on Windows when the read gate is active, naming
  the limitation — the same disclosure posture the project uses for the absent Windows sandbox
  generally.
- **A test documents the gap rather than pretending to close it** (spec T5c-windows): it asserts the
  in-process file tool *is* refused on Windows, and records that a shell child is not.

**A path-scoped text guard was considered and rejected.** It would look like enforcement while being
trivially defeated by ordinary shell quoting or variable expansion, and D10.1 has just finished
establishing that the literal-text guard is the wrong instrument for this entry. Re-adding a narrower
version of the mechanism that was rejected two sections earlier, to produce a protection that cannot
actually hold, is the "fallback nobody can detect" pattern ADR-061 exists to prevent. Better an
honest gap than a decorative guard.

**Verdict, plainly: strong on the registry, partial on project skills, absent on Windows.** The
grant becomes a genuine boundary against the file tools everywhere, and against `bash` on the two
platforms that have a sandbox backend. It does not become a boundary on Windows, and a determined
agent inside a mounted git repository can route around it via git plumbing. That is worth having —
it removes the *documented, one-call* bypass the system currently advertises in its own system
prompt — but it is not a wall, and the ADR should not be read as claiming one.

**One cost the §6.6 option text overstated, corrected here.** The original wording said skills
"stop being ordinary files an operator can open, edit or keep in git the normal way." That is wrong.
These are app-layer and kernel policies applied to *agent* processes; the operator's own shell,
editor and git are untouched. Skills remain plain files on disk in a plain directory. What changes
is only that the agent must go through `Skill` to read one.

---

## 3. Consequences

### Positive

- **Grants become free.** Granting an agent twenty skills costs nothing until one is used. Being
  generous and being cheap to run stop being in conflict — which is the precondition for the v0.3
  self-improvement loop where agents author their own skills.
- **The per-message cost of skills drops to the menu**, which sits inside the prompt-cache
  breakpoint rather than after it.
  **Falsifiable target (OBS-002, restated in r3 for the uncapped menu):** for an agent granted N
  skills, the per-turn uncached skill cost must fall from *the sum of N `SKILL.md` bodies* to
  **0 tokens** when no skill is active, and the menu's contribution must be **O(M) one-line entries
  inside the cached prefix**, where M is the number of skills available in that workspace.
  **What survives D1.1's cap removal, and what does not:**
  - **The headline guarantee is unchanged and still absolute.** It was always about skill *bodies*
    sitting *outside* the cache breakpoint. Zero is still zero, for any M — the cap never had
    anything to do with it.
  - **The secondary bound is gone.** r2 could also say the menu itself was bounded (≤20 entries).
    It no longer is: M is unbounded for a large mounted skill collection (D1.1). So the claim
    "skills cost little per message" now holds *unconditionally* for bodies and *conditionally* for
    the menu — conditional on how many skills the workspace actually offers.
  - **The measurement is unchanged**, and it is now the only way to know the real number rather than
    assume it. Concretely measurable before/after on a representative install by diffing
  `static_chars` and total prompt tokens — both of which `BuildMessages` already logs
  (`logger.DebugCF("agent", "System prompt built", …)` carries `static_chars`, `dynamic_chars`,
  `total_chars`) — for the same agent and same first message. If the uncached skill contribution is
  not zero on a no-skill turn, this ADR has not delivered its headline claim, and that is checkable
  rather than assertable.
- **Four leaks close** — `list_skills`' unfiltered full-catalog dump with paths, the empty-list
  inversion, the wrong `find_skills` signpost, and the wrong advertised skills path.
- **Project skills and `CLAUDE.md` work on day one** with no author effort, because the format and
  the shelf both already exist.
- **No new concept beside workspaces**, and no revival of the deleted `repository` field.

### Negative / accepted

- **Reliability moves from a deterministic mechanism to a model judgement** — for the ordinary case.
  Today a listed skill is *always* present. Afterwards it is present when the model recognises the
  match. D2 is the mitigation and it is a real one, but the honest statement is that this trades
  guaranteed presence for relevance, and a badly-described skill will simply never fire. D9's
  `requested_skill` is the deterministic escape hatch where a caller genuinely needs a guarantee.
- **An agent can rewrite the instructions it later follows. Accepted, not mitigated** (§6.4). A
  mounted folder is writable by the agents working in it — that is why it was mounted. So an agent
  can edit that project's skills and its `CLAUDE.md`, and then follow what it wrote. This is a
  deliberate exception to a principle Omnipus otherwise holds: `workspaces/<id>/AGENT.md` sits one
  level *above* the confined `work/` root (`pkg/workspace/instructions.go`'s `workDirName` comment,
  ADR-032's amendment), and the mount list itself lives in the protected entity store. **This is
  the first agent-followed instruction placed inside agent-writable space, and it stays there.**
  No fingerprinting, no approval-on-change, no read-only snapshot. The obligation this creates is
  documentary: **the mount UI and the docs must say this in plain words** — that agents working in
  a mounted folder can change that project's skills and instructions, and will then follow the
  changed version. An accepted risk that nobody is told about is just an undocumented one.
  Note the interaction with D10: skill *reads* are gated, skill *writes* inside a mount are not, so
  an agent that cannot read a project skill can still overwrite it. That asymmetry is a direct
  consequence of "mounts grant write" and is not closed here.
- **Mounting is trusting** (§6.3). No separate switch means no moment where someone is asked a
  second time. Cloning a repository and mounting it is sufficient to give your agents new skills.
- **The per-message menu is unbounded for a large mounted skill collection** (D1.1, r3). Registry
  and builtin contributions are operator-chosen and self-limiting; a mount's is not — a `git clone`
  decides it. Accepted deliberately, with no substitute bound, and made visible in the mount view
  rather than constrained.
- **An agent editing a project skill writes into the operator's repository** (D6.1, r3). This is
  §6.4's accepted posture applied to the authoring verbs rather than a new exposure — `write_file`
  could already reach the same path — but it is now something the authoring tools do *by design*,
  so it is audited rather than merely permitted.
- **The assembled-prompt cache is bounded in bytes as well as count** (D8.1, r5), because D1.1's
  uncapped menu made per-variant size unbounded and a count-only LRU does not bound memory. Without
  this the design violated Hard Constraint #3 by roughly 4× in its own anticipated worst case.
- **The read gate covers a skill's instruction file, not its directory** (D10.3, r6), so bundled
  helper scripts, templates and reference files stay readable and executable. The project-shelf gate
  is gone entirely. The residual cost is that a bundled file belonging to an ungranted registry skill
  is readable, and an author could hide instructions there.
- **The read gate is enforced against spawned children on POSIX only** (D10.2, r4). On Windows it
  is app-layer only: the in-process file tools are gated, a `bash` child is not, and US-7's "a grant
  is a real boundary" carries that qualifier wherever it appears. Disclosed at boot, tested as a
  documented gap rather than papered over.
- **The write trail is complete for tool-mediated writes and incomplete for shell-mediated ones**
  (D6.1.1, r4). Moving the hook to `tools.ResolvePath` closes the `write_file` route that made r3's
  "writes are audited" claim false; `bash` remains outside it on every platform.
- **A mount that would contribute an implausible number of skills warns at creation** (D1.2, r4).
  The menu itself stays uncapped — this is a different mechanism at a different moment, not the cap
  D1.1 removed.
- **`skills/` moves into the filesystem secret set, amending ADR-062 §4.0** (D10). Small, but it
  reverses a specific accepted line and carries a Linux-only prerequisite (the child-only ruleset
  spike) that could block that platform's build until resolved.
- **The gate is real but not total** (D10's table): closed against the file tools everywhere, closed
  against `bash` on macOS/Linux, **open on Windows** (no sandbox backend), and routable around via
  `git` plumbing inside a mounted repository.
- **D8 is real work that must land first**, and it touches the hottest cache in the prompt path.
- **Skill grants stay global on the agent** (§6.5), so the same agent carries the same grants into
  every workspace. Revisitable; ADR-037's workspace-scoped precedent still argues the other way.

### Neutral

- `buildActiveSkillsContext` / `LoadSkillsForContext` survive unchanged — only their trigger moves.
- **The 20-entry menu cap is removed** (D1.1, r3). The truncation footer goes with it, which deletes
  F3's wrong signpost rather than correcting it. Menu ordering becomes cosmetic rather than
  selective. See §5, OQ13 — reversed in r3.
- `find_skills` keeps its actual job (marketplace discovery) and its description is already correct;
  only the *prompt text that misdescribes it* changes.

---

## 4. Alternatives considered

**A. Keep force-loading, but only the "relevant" skills, chosen by a cheap classifier per turn.**
Rejected: it adds an LLM call or an embedding index to a project whose memory design is explicitly
no-embeddings, spends latency on every turn to save tokens on some, and is strictly worse than
letting the model — which has already read the task — pick from a menu it can see. ADR-071 reached
the same conclusion for tools.

**B. A separate mechanism for project skills, parallel to the installed registry.** Rejected: the
loader already has a workspace shelf, ranked first, that nothing writes to (F4). Building a second
path alongside a working, understood, currently-empty one is pure duplication — and every consumer
(menu, cache invalidation, `LoadSkill`) would need teaching about both.

**C. A `project` concept alongside `workspace`.** Rejected: two overlapping containers is precisely
the confusion the Workspaces redesign exists to end, and ADR-063 D7 already deleted the last
attempt at a repository-shaped field on a workspace.

**D. A new instruction layer for `CLAUDE.md`, separate from Project Instructions.** Rejected: same
kind of content, same injection point, same cap, same audience. One rail, two sources.

**E. Name the tool `load_skill`, matching Omnipus's lowercase-verb convention.** Rejected — ADR-071
D1 made the opposite call for the sibling tool, deliberately and with a stated reason. Two tools
that are the same idea should not be named by two different conventions. §5, OQ1.

**F. (D9) Have the parent seed the child's context with the skill's text directly**, e.g. via
`SubTurnConfig.ActualSystemPrompt` or by appending the `SKILL.md` body to the task prompt. Rejected
outright: this is precisely the ADR-032-violating shape — the parent supplying *content* that
becomes the child's instructions, bypassing the child's grant entirely. It would also let a parent
launder a skill it holds into an agent that does not. D9 sends a **name**, never a body, and the
child's own gate answers it.

**G. (D10) A filename-pattern deny (`**/SKILL.md`) instead of directory-scoped denies.** Rejected on
two independent grounds. Mechanically, `pkg/fspolicy` has no pattern facility at all — `IsCarveOut`,
`CoversForDeny` and `CoversForGrant` are containment tests resolved by filesystem identity, and
neither Landlock nor Seatbelt expresses filename globs. Semantically it would be wrong even if it
worked: it would deny an ordinary `SKILL.md` an agent is legitimately editing anywhere in a repo,
while D6 already defines project skills by *directory*. Location is the definition, so location is
the enforcement.

---

## 5. Resolutions — the draft's thirteen open questions, plus ten found here

Each resolution names what it is grounded in. The six that were the founder's to decide were put to
him and are recorded in **§6**; the two added in r2 (N9, N10) come from the adversarial review and
are resolved in **D4.1** and **D5.1**. Nothing in this table is still open.

| # | Question | Resolution | Grounded in |
|---|---|---|---|
| **OQ1** | Tool name; one tool or two? | **`Skill`, one tool.** Load-by-slug and search-by-query are modes, exactly as `ToolSearch` carries `names` and `query`. | ADR-071 D1 (the same convention override, same reason); ADR-071 §1.1 (`ToolsTool`'s two-parameter shape) |
| **OQ2** | Delegating a skill to a subagent — whose permission counts? | **The receiver's, alone — and D9 makes that structural rather than conventional.** Founder chose "receiver decides" plus two refinements, now built out as D9: *encourage* (name it in the task prompt, no machinery) and *request* (`requested_skill`, resolved through the child's own `ContextBuilder.ResolveSkillName`, loaded via the child's own `ForcedSkills`, refused cleanly if the child is not granted it). | §6.1; ADR-032's task-prompt carve-out; `pkg/agent/subturn.go`; `pkg/agent/loop.go::applyExplicitSkillCommand` |
| **OQ3** | Does anything stay always-on? | **No always-on marker.** An always-on skill is an instruction wearing a costume; SOUL.md and the workspace's Project Instructions already exist for that, and a marker would rebuild the problem being fixed. | #663 AC-2 verbatim ("No skill content is injected into a turn's context unless something for *that turn* explicitly activated it") |
| **OQ4** | Upgrade behaviour | **The *empty-list* half is decided by D5** (the shipped contract already says "opt-in, default none"; the code contradicts its own contract). **The migration half is N/A** — v0.3 is greenfield, no back-compat, so there is no installed base whose behaviour changes. | `contracts/components/schemas/Agent.yaml` vs. `pkg/agent/context.go::skillAllowed`; CLAUDE.md Release Strategy (v0.3 "fresh-build, no back-compat"); §6.2 |
| **OQ5** | Who trusts a project's skills, and how? | **Mounting the folder is the trust decision.** No switch, no per-skill approval. Creating a mount is already an explicit operator action granting *write* access, which is the larger decision; a second prompt asks the same question twice. Consequence documented at the mount UI, not mitigated. | §6.3; ADR-063 D4 (mounts are explicit, operator-created, stored out of agent reach) |
| **OQ6** | Which folder do project skills come from? | **Both `.omnipus/skills/` and `.claude/skills/`**, ours winning a slug clash (same rule the shelves already use for collisions). | CLAUDE.md Hard Constraint #5; `SkillsLoader.ListSkills`' existing dedup-by-ID |
| **OQ7** | Which instruction files, how much? | **Root only; `CLAUDE.md` and `AGENTS.md` accepted, `CLAUDE.md` wins; existing 262144-byte cap applied across the composed note; truncation marked visibly in the note.** | `pkg/workspace/instructions.go` (`maxInstructionsBytes`, `ReadInstructions`' truncate-and-WARN) |
| **OQ8** | Several mounts | **All mounts contribute skills** (offered, never forced), with slug collisions resolved by mount-name order and logged (**D4.2**). **All mounts contribute instructions**, ordered by mount name, labelled, under one shared cap — no "primary" setting. | D7's offered-vs-imposed asymmetry; ADR-063 D4 (mounts are named and few); MAJ-001 |
| **N9** | Are project skills grant-gated? | **No — the mount is the grant**, workspace-scoped, and it may not replace a granted registry slug. | **D4.1 / D4.2**; CRIT-001, MIN-004 |
| **N10** | Does `SeedConfig` respect an operator's empty grant list? | **Not today — it silently re-seeds on every boot.** Gate the block behind `isFreshInstall`. | **D5.1**; CRIT-002; `pkg/coreagent/core.go::SeedConfig` |
| **OQ9** | An agent could rewrite its own instructions | **Accepted and documented, not mitigated.** No fingerprinting, no re-approval on change, no read-only snapshot. The obligation is that the mount UI and docs say so plainly. See §3 Consequences for the D10 interaction (reads gated, writes not). | §6.4 |
| **OQ10** | The dead `inherit_skills` toggle | **Remove the control** — now unconditional, since §6.1 chose receiver-decides and D9 gives the parent a real channel that is not inheritance. The toggle never reaches the backend (`CreateAgentModal.test.tsx` asserts `expect(call).not.toHaveProperty('inherit_skills')`), and after D5 its effect flips from "unrestricted" to "no skills" — a switch labelled *inherit* that silently means *none*. | ADR-032; §6.1/D9; `src/components/agents/wizard/Step3Tools.tsx`, `CreateAgentWizard.tsx` |
| **OQ11** | `.preview-doc` says two different things | **The locked ledger wins, and the overlay option is taken.** `decisions.html` (locked): "Per-agent allowlist + progressive disclosure (anti-bloat)". `spaces.html`'s "available to all agents" is loose sidebar wording about the registry being global, not a statement about grants — and the same page explicitly offers the workspace overlay to this ADR. | `.preview-doc/decisions.html` §Skills; `.preview-doc/spaces.html` §"Skills stay a global registry" |
| **OQ12** | Grant on the agent, or on the agent's seat in a workspace? | **Stays on the agent, for now.** Deferred deliberately, not settled: ADR-037's workspace-scoped precedent still argues the other way, and the `Skill` tool's shape is unaffected either way, so moving it later costs a storage change (into the protected store) rather than a redesign. | §6.5; ADR-037 |
| **OQ13** | Menu length | ~~Keep the 20 cap; fix the escape hatch.~~ **REVERSED in r3 (§6.7): remove the cap entirely.** r1/r2 kept it and corrected its footer; the spec round showed D4.1 lets a mount's skills crowd out explicitly granted ones, and the founder removed the cap rather than reserving or raising. The footer and F3's wrong signpost are deleted with it. | **D1.1**; `pkg/skills/loader.go::maxSkillsInSummary` (deleted) |
| **N1** | `list_skills` is unfiltered and returns paths | **Filter it with the same predicate as the menu; stop returning `path`.** Identical to ADR-071's CRIT-201 fix on the tools side. | ADR-071 §3.2.2; `pkg/sysagent/tools/skill.go::SkillListTool.Execute` |
| **N2** | `create_skill`/`edit_skill`/`remove_skill` vs. the grant list | **Out of scope of the grant list** — authoring verbs over the shared registry, governed by tool policy. | CLAUDE.md Constraint #6 |
| **N3** | The `# Skills` block tells agents to `read_file` a skill | **Replace with "call `Skill`".** The instruction is the back door, documented. | `pkg/agent/context.go::BuildSystemPrompt` |
| **N4** | The advertised skills path is wrong | **Correct or drop it.** `getWorkspaceAndRules` prints the agent-workspace path; skills live in the global dir. | F4 |
| **N5** | Where the per-task reminder lives | **Dynamic context, one line, per request.** | D3; `pkg/agent/context.go::buildDynamicContext` |
| **N6** | Is the `Skill` call shown in chat? | **Hidden on success, forced visible on error** — exactly `ToolSearch`'s rule. | `src/lib/toolVisibility.ts`; CLAUDE.md's UI-rules exception list |
| **N7** | Descriptions as triggers — convention or enforcement? | **Both**: stated in `skill-authoring`, checked in `create_skill`/`edit_skill`, shown in the UI. | D2 |
| **N8** | Does the workspace overlay need the context cache fixed first? | **Yes, hard prerequisite.** | D8; `pkg/agent/context.go::BuildSystemPrompt` takes no workspace id |

---

## 6. Founder resolutions (2026-09-01)

All six were put to the founder and answered. Two of the answers rejected this ADR's own
recommendation, and both were the more demanding option; where the reasoning below is the founder's
own it is marked as such, and where it is reconstructed grounding it is marked as that.

**6.1 — Delegating a skill to a subagent. RESOLVED: the receiver decides, plus a real request
channel.** The founder took "receiver decides for itself" as the baseline and added two refinements
rather than accepting the recommendation to drop the mode entirely. His follow-up, verbatim: *"one
path is still missing, if the main agent wants to really execute the skill in a subturn/subagent
style how would that work?"* — i.e. a soft suggestion is not enough; there must be a way to delegate
such that the child *reliably* runs the skill, while the receiver's own grant remains the gate and
denies rather than silently ignores. **Designed as D9**, which separates the two things that were
being conflated: *encourage* (name it in the task prompt — already works, no machinery, no
guarantee) and *request* (the new `requested_skill` parameter — resolved through the child's own
`ContextBuilder.ResolveSkillName`, executed via the child's own `ForcedSkills`, refused at dispatch
with a structured error if the child is not granted it). The recommendation to drop the mode was
wrong, and D9 is a better answer than any of the three options offered: it is deterministic *and*
receiver-gated, which the options had treated as a trade-off.

**6.2 — Upgrade behaviour. RESOLVED: N/A, greenfield.** Founder: *"assume green field not backward
compatibility."* This is not option C ("ship it, note it in the release") — it is the question being
struck rather than answered, and the distinction matters for anyone reading this later. CLAUDE.md's
Release Strategy already frames the whole of v0.3 that way ("Fresh-build, no back-compat"), so
there is no installed base whose behaviour this changes and no migration to design. The
empty-list-semantics half of the old OQ4 is unaffected and is decided by **D5** on contract grounds.

**6.3 — Trusting a project's skills. RESOLVED: automatic on mount** (option C, against the
recommendation of a per-workspace switch). Grounding, reconstructed and checked against ADR-063 D4:
creating a mount is already a deliberate, explicit operator action that grants **write** access to a
named real folder — a strictly larger trust decision than "may read the skills in it". A second
switch asks the same question twice and trains people to click through it. Written into **D6**,
with the consequence stated at the mount UI rather than mitigated.

**6.4 — An agent rewriting the instructions it then follows. RESOLVED: accept and document**
(option B). No fingerprint-and-re-approve, no read-only snapshot. Recorded in §3 Consequences with
the explicit obligation that the mount UI and docs say so in plain words, and with the D10
interaction named (reads gated, writes not — an agent that cannot read a project skill can still
overwrite it).

**6.5 — Grant scope. RESOLVED: stays on the agent** (option A). Deferred, not settled — ADR-037's
workspace-scoped precedent still argues the other way and the tool's shape is unaffected either way.

**6.6 — What a grant promises. RESOLVED: route skill reads through the tool** (option C, the
strongest, against the recommendation of "steering, not secrecy"). **Designed as D10.** Two working
notes on how that answer changed under design:

- **One of option C's stated costs was wrong and is withdrawn.** The option text said skills would
  "stop being ordinary files an operator can open, edit or keep in git the normal way." They do not.
  These are app-layer and kernel policies on *agent* processes; the operator's own shell, editor and
  git are untouched. The real cost is smaller than advertised.
- **One thing D10 genuinely cannot close, flagged rather than papered over.** A `git show` inside a
  mounted repository reads a project skill out of `.git/`, which is not under any denied subtree, and
  denying `.git/` would break every legitimate git operation. Plus Windows has no sandbox backend at
  all, so `bash` is unconfined there. D10's table states exactly what is and is not closed, per
  platform and per path. **Strong on the registry, partial on project skills, absent on Windows** —
  worth having, not a wall.

### 6.7 Spec-round resolutions (r3, 2026-09-01)

Two further questions surfaced while deriving the implementation spec. Both fall out of **D4.1**'s
per-shelf model, which is the newest part of the design and had not had these consequences traced
through it. **The founder rejected this ADR's recommendation in both cases**, and in both cases the
recommendation was the more timid option.

**6.7.1 — Editing a project's skills → write into the project** (options were: refuse / fork a
shadowing copy / write into the repo). Chosen: **write into the repo**. Recorded as **D6.1**. The
recommendation to refuse rested on a conflict with D10 that turns out not to exist — D4.1 already
lets any agent in the workspace load any of that mount's skills, so D10 redirects the read path
rather than denying it, and there is no skill an agent can write but cannot read. The narrow
decision this settles is that **authoring writes are not gated by the read gate**, which is
§6.4's already-accepted posture rather than a new exposure, and which `write_file` could reach
anyway. Real work it creates: the authoring verbs must become shelf-aware (`SkillWriter` is rooted
at the global directory today and cannot address a project slug at all), and project-skill writes
are audited.

**6.7.2 — The menu cap when a mount brings many skills → remove the cap entirely** (options were:
reserve slots / leave it / raise it). Chosen: **none of them — no cap at all.** Recorded as
**D1.1**, reversing OQ13. The question asked about crowding; the answer removes the mechanism that
causes it. Consequence accepted explicitly: the per-message menu is now unbounded for a large
mounted skill collection, and **no substitute bound is introduced** — not a token budget, which
would be the same decision in a different unit. Visibility is added instead (the mount view states
how many skills each mount contributes per message), because that informs without constraining.
OBS-002's target is restated in §3: the zero-uncached-skill-body-tokens guarantee is untouched; the
secondary "menu is bounded" claim is gone and is now something to measure rather than assert.

### 6.8 Carried forward — not closed by this ADR

- **Linux child-only kernel deny (D10, Part A).** Landlock confines the gateway as well as its
  children, so denying `$OMNIPUS_HOME/skills` at the kernel would also blind `SkillsLoader` and
  break the feature. The deny must be child-only on Linux. That distinction does not exist in the
  code today and must be **verified on a Linux host, not reasoned about** — a spike before
  implementation. It fails closed and loudly (skills stop loading) rather than silently, which is
  the right failure direction.
  **Explicit fallback if the spike fails or slips (OBS-001): ship everything else.** "Blocker for
  the Linux build" means blocker for *that one kernel-layer closure*, not for this ADR. D1–D9 and
  D10's app-layer deny are independent of it and carry the large majority of the value; without the
  kernel leg, Linux simply lands in the same posture this ADR already accepts for Windows —
  file tools gated, `bash` not — which is strictly better than today and is documented in D10's
  table rather than being a surprise. Do not read the spike as gating the menu, the `Skill` tool,
  or D9.
- **ADR-062 amendment.** D10 Part A moves `skills/` out of ADR-062 §4.0's "must stay
  agent-accessible" column, **and corrects the pre-existing `system/` drift in the same edit**
  (MAJ-003). That amendment must be written into ADR-062 itself rather than left implied here, so
  the secret-set table has one authority — which it does not have today. Note the amendment must
  describe `skills/` as **path-denied but not text-guarded** (D10.1), not as a plain
  `SecretEntriesAlways` member.
- **Shelf-aware authoring (D6.1).** `SkillWriter` is rooted at the global skills directory and
  cannot address a project slug at all today. Making the authoring verbs resolve a slug to its own
  shelf, and confining a project write to its mount, is genuinely new work — the largest single
  piece r3 adds — and it is a prerequisite of D6.1 being real rather than nominal.

---

## 7. Contract impact (Constraint #8)

**Net: this ADR requires no new wire schema.** That is a deliberate outcome of the founder's
answers, not luck — §6.3's "mounting is trusting" removed the one new field the design would
otherwise have needed.

- **No new wire type for `Skill`, nor for `delegate`'s `requested_skill`** (D9) — tool names,
  parameters and results are LLM-facing tool schemas built in Go (`Tool.Parameters()`), not
  gateway/SPA wire types, so Constraint #8's contract-first process does not apply to them.
- **`contracts/components/schemas/Agent.yaml`'s `skills` description changes** — text only, no shape
  change. Today's "Absence of the field and an empty array are semantically identical (opt-in,
  default none)" becomes *true* under D5 rather than aspirational, and should gain a sentence saying
  a granted skill is loadable on demand, not always loaded.
- **No workspace "trust project skills" flag.** §6.3 chose trust-on-mount, so the field the
  recommended option would have needed does not exist. Nothing to add to the workspace schema.
- **`inherit_skills`** (OQ10) is SPA-local and never crosses the wire, so removing the control is a
  no-op for contracts.
- **D10 changes no contract**: `fspolicy`'s secret set and the kernel policy are internal, and
  ADR-062's table is documentation.

---

## 8. Scope notes

**Heartbeat and cron-triggered turns are unaffected** (OBS-003). Per-agent heartbeats and
`pkg/cron`-triggered task runs reach the LLM through the same `BuildMessages` /
`activeSkillNames` path as a chat turn, with the same `ContextBuilder` and the same grant list. So
they get the same menu, the same D3 reminder, and the same `Skill` tool, and they lose the same
force-loaded bodies. No special-casing, and no separate activation path — stated explicitly because
a scheduled turn has no human to notice a skill that failed to fire, which makes D3.1's audit trail
the only signal there.

**Out of scope, unchanged from r1:** `create_skill` / `edit_skill` / `remove_skill` remain governed
by tool policy rather than the grant list (N2); `find_skills` keeps its marketplace role (F3).

---

## 9. Required tests (MAJ-005)

r1 named zero test obligations for its own new behaviour, while relying on existing regression
tests for adjacent shipped mechanisms. Given that CRIT-002 is a live example of code already
fighting D5's semantics, the following are **required**, not suggested. This is a list of
obligations for the implementation spec to expand, not a finished test plan.

**T1 — Grant semantics (D5, D5.1).**
- **T1a** `WithSkillAllowlist(nil)` denies every slug — the flipped nil semantics, asserted
  directly rather than via a caller.
- **T1b** a non-nil empty slice also denies everything (absence and `[]` are identical, matching
  `Agent.yaml`).
- **T1c** **`SeedConfig` run twice in sequence, with an operator-set empty skill list written
  between the runs, leaves the list empty.** This is the CRIT-002 regression and nothing today
  covers it.

**T2 — The five gate points (D4).** One test per door, each asserting a denied slug is refused:
`Skill` load, `Skill` search (the *match list*, not just the loaded item — the ADR-071 §3.2.2
shape), the menu, `/<skill>`, and `list_skills`. The `list_skills` case must also assert `path` is
no longer returned at all.

**T3 — Shelf and collision rules (D4.1, D4.2).**
- **T3a** a project skill in a mount is invokable by an agent in that workspace with **no** slug in
  its grant list, and **not** invokable by the same agent acting in a different workspace.
- **T3b** a project skill whose slug collides with a **granted registry slug** does not win; the
  registry skill loads and the collision is logged.
- **T3c** two mounts carrying the same slug resolve by mount-name order, deterministically, with
  the collision logged.

**T4 — Delegation (D9).** All three outcomes explicitly: granted (child's turn opens with the skill
loaded via `ForcedSkills`), **denied** (dispatch fails with a structured error naming agent and
skill), and **unresolvable** (distinguishable from denied). Only the first is even implicitly
covered by existing `ForcedSkills` tests. Plus one negative: the parent's own grant list does not
affect any of the three outcomes.

**T5 — Read gating (D10).**
- **T5a** `read_file` on a registry skill path is refused (app layer, every platform).
- **T5b** `read_file` on a path under a mount's `.claude/skills/` is refused, while an ordinary
  file in the same mount still reads.
- **T5c** **Linux host integration test: the child-only kernel deny does not blind `SkillsLoader`
  in-process** — i.e. `Skill` still loads while a `bash` child cannot `cat` the same file. This is
  the §6.8 spike's acceptance criterion and cannot be satisfied on macOS.

**T6 — Cache correctness (D8).** Removing an agent from a workspace, and deleting a mount, each
evict the corresponding cached prompt; and the per-agent variant count stays within the cap under
repeated workspace switching.

**T7 — r3 additions.**
- **T7a** the menu lists **every** available skill with no truncation and no footer, for a catalogue
  well past the old cap (D1.1).
- **T7b** an explicitly granted registry skill still appears in the menu when a mount contributes
  more skills than the old cap — the defect that motivated removing it.
- **T7c** editing a project skill writes to the project's own file, and the registry copy (if any)
  is untouched (D6.1).
- **T7d** `remove_skill` on a project slug deletes the project's file, confined to the mount.
- **T7e** a project-skill write is audited with shelf and resolved path.
- **T7f** `skills` is denied by the path layer but **not** by the shell literal-text guard, and an
  ordinary command mentioning the word "skills" is still permitted (D10.1). This is the regression
  that stops the guard being silently widened.

**T8 — r4 additions.**
- **T8a** a write under a recognised skills path is audited **whichever tool performed it** —
  asserted for `write_file` specifically, not only the authoring verbs (D6.1.1).
- **T8b** on Windows the in-process file tool refuses a skill read, and the shell-child gap is
  recorded as a known documented limitation rather than asserted closed (D10.2).
- **T8c** creating a mount whose skills count exceeds the threshold surfaces the operator warning,
  and the resulting menu is still uncapped (D1.2).
- **T8d** a `SKILL.md` that is a symlink pointing outside its mount is neither discovered nor
  readable, distinct from the directory-level symlink case.

**T9 — r6 additions (D10.3).**
- **T9a** a bundled sibling file inside a registry skill directory (`<skill>/reference.md`,
  `<skill>/scripts/run.sh`) is **readable** by the ordinary file tool, while that skill's `SKILL.md`
  is refused. The bundled-file regression, stated as the pair.
- **T9b** a bundled sibling file inside a *mount's* skills directory is readable, and so is that
  skill's `SKILL.md` — the project shelf has no read gate at all after D10.3(a).
- **T9c** a bundled executable inside a skill directory can be run, which the tool-output-only
  alternative could never have satisfied.

---

## 10. Review traceability — r1 → r2

| Finding | Resolution |
|---|---|
| **CRIT-001** D4/D6 contradiction on grant-gating project skills | **D4.1** — the gate is per-shelf; the mount is the project shelf's grant instrument. Both sections now cross-reference it. Security consequence stated and bounded three ways. |
| **CRIT-002** `SeedConfig` re-seeds empty grant lists every boot | **D5.1** — independently re-verified against source, then resolved by gating the block behind `isFreshInstall`, as a named implementation requirement. Option (b) rejected with reasons. Test obligation T1c. |
| **MAJ-001** multi-mount slug collisions | **D4.2** — mount-name order, first wins, collision logged. Same rule D7 uses. Test T3c. |
| **MAJ-002** D9 enumeration oracle | **D9** — named explicitly and **accepted** with three stated reasons; the alternative (generic refusal) is named along with what it costs. |
| **MAJ-003** ADR-062 §4.0 stale on `system/` | **D10 Part A** — verified independently, flagged in a boxed note, and the `skills/` amendment is required to correct the `system/` row in the same edit. |
| **MAJ-004** (agent × workspace) cache unbounded | **D8** — LRU cap, plus two invalidation triggers r1 omitted (workspace-membership change, mount/workspace deletion), framed as correctness not performance. Test T6. |
| **MAJ-005** no test obligations | **§9** — six groups, thirteen named tests, covering all four categories the review listed. |
| **MAJ-006** no observability for the biggest risk | **D3.1** — audit every `Skill` call (slug, mode, outcome, shelf, agent, workspace); per-skill `last_invoked` in the UI. Denials audited even though successes are hidden from the thread. |
| **MIN-001** reminder budget unspecified | **D3** — hard ≤30-token ceiling, with the reason (it sits outside the cache breakpoint). |
| **MIN-002** implied unified project heuristic | **D6** — explicit table of two independent path checks; "no `.git` check" stated. |
| **MIN-003** search mode's bounds unstated | **D1** — inherits `ToolSearch`'s `MaxSearchResults` and cached engine; no new limit, stated deliberately. |
| **MIN-004** write-without-read + slug shadowing | **D4.2** carve-out — a project skill may never replace a granted registry slug. Test T3b. |
| **OBS-001** Linux spike fallback unstated | **§6.8** — explicit: ship everything else; Linux falls back to the Windows posture, which is documented in D10's table. |
| **OBS-002** no falsifiable savings target | **§3 Positive** — zero uncached skill tokens on a no-skill turn, measurable via the `static_chars`/`total_chars` already logged. |
| **OBS-003** heartbeat/cron unmentioned | **§8** — same path, unaffected; with the note that a scheduled turn has no human to notice a missed skill, so D3.1 is the only signal. |

---

## 11. Where re-verification changed the source draft

The draft (`docs/internal/design/skills-on-demand-draft.html`, 2026-09-01) was re-checked against
`f101a9b4`. Its central claims all hold: force-loading via `SkillsFilter`, the existing filtered
menu, the three-shelf loader, the empty-list inversion, the `read_file` back door, the vestigial
first shelf, mounts as the answer, the workspace-blind context cache. Four corrections and
extensions:

1. **The `read_file` back door is not merely undefended — it is an ADR-recorded grant.** The draft
   treated it as a gate left ajar. ADR-062 §4.0's table names `skills/` in the "must stay
   agent-accessible" column. Closing it means amending an accepted ADR, which is why it became
   §6.6 rather than a line item in D4.
2. **`list_skills` is a second, unfiltered disclosure channel — including full paths.** Not in the
   draft, and it is the exact shape ADR-071 §3.2.2 recorded as CRIT-201. F1 / N1.
3. **The truncation footer's escape hatch does not exist.** The draft treated the 20-cap as "the
   rest are behind search"; `find_skills` searches marketplaces for *uninstalled* skills and cannot
   see the truncated ones at all. This changes OQ13's answer from "raise the cap" to "fix the
   hatch". F3 / OQ13.
4. **The empty-list question is already settled in writing by the shipped contract**, which says
   the opposite of what the code does. The draft framed it as fully open; only its user-visible
   half is (§6.2). F2 / D5.

Two further small corrections: the advertised skills path in the system prompt points at a
directory nothing writes to (F4/N4), and the draft's "four skills built into the binary" is exactly
right — `summarize`, `skill-authoring`, `plan`, `daily-briefing` (`pkg/skills/embedded/`), seeded
into the **global** dir on first boot by `skills.SeedDefaults`, not served from the builtin shelf on
a normal install.
