# Adversarial Review: ADR-072 — Skill activation and loading

**Spec reviewed**: docs/internal/architecture/ADR-072-skill-activation-and-loading.md
**Review date**: 2026-09-01
**Verdict**: REVISE

## Executive Summary

ADR-072 is unusually well-evidenced — every code claim spot-checked below (`skillAllowed` nil semantics, `ForcedSkills`, `SkillListTool.Execute`, the delegate `snapshot` channel, `Agent.yaml`'s contract text, `inherit_skills`) held up exactly as cited. But the review surfaced two CRITICAL gaps the ADR does not address anywhere: (1) D4's "the grant list is the gate… at every door" and D6's "project skills… come along, no author effort" are in direct, unresolved tension — the ADR never states whether a mount-derived project skill is subject to the per-agent allowlist, and each reading breaks a different part of the design; (2) an existing, unmentioned piece of code (`coreagent.SeedConfig`'s per-boot re-enforcement loop, `pkg/coreagent/core.go:1737-1746`) will silently re-populate a core agent's skill grants from `coreAgentSkills()` every time `len(a.Skills) == 0`, which is indistinguishable from an operator's deliberate "opt-in, default none" choice under D5 — directly undermining the contract semantics D5 claims to restore. 8 MAJOR findings and several MINOR/OBSERVATION items follow, including an unaddressed skill-grant enumeration oracle in D9 and a project-skill slug-shadowing risk in D6.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 6 |
| MINOR | 4 |
| OBSERVATION | 3 |
| **Total** | **15** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] D4 and D6 give contradictory answers to "is a mount-derived project skill grant-gated?"

- **Lens**: Inconsistency
- **Affected section**: D4 ("A gate must sit at every door… `skillAllowed` must be consulted at five points") vs. D6 ("when a folder mounted on a workspace looks like a code project, that project's skills… come along… no author effort") and §3 Positive ("Project skills… work on day one with no author effort, because the format and the shelf both already exist").
- **Description**: `skillAllowed(name string) bool` (`pkg/agent/context.go:222-228`) is a flat, shelf-agnostic string lookup against the agent's grant map — it has no notion of which of the three shelves (project / registry / builtin) ultimately serves a slug. D4 states the gate applies at all five invocation points ("Skill's load path," "the menu," etc.) without carving out an exception for project-sourced skills; taken literally, an operator would have to *already know* a not-yet-mounted repository's skill slugs and add them to the agent's grant list in advance (or edit the grant after every mount) for D6's promise to hold. D6 and §3 Positive instead promise automatic, zero-configuration availability the moment a folder is mounted — "no author effort," "come along." These cannot both be true as written, and the ADR never states which wins. Nothing in D1–D10 or the resolutions table (§5) resolves it; §6.3 ("mounting the folder IS the trust decision") argues for the D6 reading but never says the grant list is bypassed for project-sourced slugs, and D4's own text says the opposite.
- **Impact**: An implementer resolving this ambiguity in favor of D4 ships a feature that silently fails to deliver its own headline benefit (mounting a repo does nothing useful until every project skill's slug is separately granted per agent — a workflow never described anywhere, with no described UI for it). An implementer resolving it in favor of D6 ships a feature where D4's central claim ("the grant list is the gate," "a gate must sit at every door") is simply false for one of the three shelves — meaning a mount can hand an agent brand-new invokable skills the operator never explicitly granted by slug, which is a materially different security posture than D4 advertises and than §6.3's argument ("mounting is a *larger* trust decision than a skill grant") was defending.
- **Recommendation**: State explicitly, as its own decision point: "Project-sourced skills bypass the per-agent grant list entirely — mounting a folder is itself the grant, scoped to that mount's own skill slugs" (or the opposite, with an explicit UI/API path for granting newly-discovered mount slugs). Whichever is chosen, cross-reference it from both D4 and D6 so the two sections stop reading as contradictory.

---

#### [CRIT-002] `coreagent.SeedConfig`'s per-boot re-enforcement loop contradicts D5's "opt-in, default none" for the entire core roster, and the ADR never mentions it

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: D5 ("Absence of a grant list means *no* skills, matching the shipped contract… New installs are seeded correctly from the start, by the same `coreagent.SeedConfig` path that seeds every other per-agent default.")
- **Description**: Verified in `pkg/coreagent/core.go`. `SeedConfig` (line 1658) computes `isFreshInstall := len(existing) == 0` (line 1674), but the loop that re-enforces skills (line 1711, "Re-enforce identity fields on **existing** core agents") is **not** gated by `isFreshInstall` — it runs on every single boot, for every core agent already in config. Inside it: `if len(a.Skills) == 0 { if seedSkills := coreAgentSkills(ca.ID); len(seedSkills) > 0 { a.Skills = seedSkills; modified = true } }` (lines 1741-1746), with `coreAgentSkills` (line 1457) returning a non-empty default list for Mia, Ray, Jim, Ava, Planner, Explorer, Researcher. The comment even names the problem this ADR reintroduces: *"Apply the seeded allowlist only when the existing entry declares none — an operator who has customized the agent's skills keeps their choice."* Under D5, `len(a.Skills) == 0` is the operator's valid, deliberate "opt-in, default none" state (per `Agent.yaml`: "absence of the field and an empty array are semantically identical"). This code cannot tell that apart from "never configured," so on the very next boot it will silently overwrite an operator's explicit deny-all back to the seeded default — precisely the "punish-the-specific-operator inversion this ADR exists to remove" language D5 itself uses, reintroduced by code the ADR doesn't discuss at all.
- **Impact**: An operator who deliberately empties Mia's skill list (to stop `daily-briefing`/`summarize` from ever being reachable, say for a compliance reason) will find it silently restored on the next gateway restart, with no error, no log the operator would think to check, and no way to tell from the UI why it came back. This is exactly the class of bug ADR-054 D6.4's "release blocker" precedent (cited elsewhere in this repo's own CLAUDE.md) was written to prevent: a control that reports success but doesn't stick.
- **Recommendation**: Either (a) gate the skills re-enforcement block at line 1741 behind `isFreshInstall` (matching the `DefaultAgentID` seed a few lines above it), so it only ever runs once per install, or (b) if the intent is genuinely to keep re-asserting core-roster defaults on every boot (tamper-protection framing, matching the `Locked`/`Type` re-enforcement it sits beside), then D5 must explicitly carve out the core roster as **exempt** from "empty means none," and state that core-agent skill grants are asserted config, not operator-editable state — which would also mean the Agents UI must not present the field as editable for locked core agents if it is going to be clobbered anyway. Either way, this function must be named and addressed in the ADR; it currently is not.

---

### MAJOR Findings

#### [MAJ-001] Multi-mount project-skill slug collisions are unaddressed

- **Lens**: Incompleteness
- **Affected section**: D6 ("Multi-repository work needs no new design, because a workspace can already carry several mounts.")
- **Description**: D7 explicitly defines collision/ordering behavior for multiple mounts' *instruction* files ("ordered by mount name, labelled, under one shared budget"). D6 defines no equivalent for multiple mounts' *skill* directories. If mount A and mount B both carry `.claude/skills/release-process/SKILL.md` with the same slug but different content, nothing in D6 or the three-shelf table (§D6) says which wins, whether it's an error, or whether the agent even finds out there was a collision.
- **Impact**: Silent, non-deterministic (or load-order-dependent) shadowing between two mounted repositories' skills, with no error and no way for the operator to discover it happened.
- **Recommendation**: Extend D6 with the same "ordered by mount name, first wins" rule D7 already uses for instructions, and log (at minimum) when a slug collision across mounts occurs.

---

#### [MAJ-002] D9's loud dispatch-time failure is a skill-grant enumeration oracle

- **Lens**: Insecurity (STRIDE — Information Disclosure)
- **Affected section**: D9, outcome table: *"Child is not granted it → The delegation fails at dispatch with a structured error naming the target agent and the skill. Never silently ignored."*
- **Description**: Any agent permitted to delegate to a target (an ADR-037 workspace-scoped relationship, which can be considerably broader than "the parent already knows everything about the child") can now iterate `requested_skill` values across `delegate` calls and read the pass/fail (and the named skill in the failure) to fully enumerate the target agent's entire skill grant list — a capability that does not exist today. The ADR performs exactly this kind of STRIDE analysis for D10 (file-read paths) but never raises it for D9, despite D9 introducing a new, explicit, per-item oracle by the founder's own "never silently ignored" requirement.
- **Impact**: A workspace member with narrow delegation rights to another agent can build a complete picture of that agent's skill posture (and, by extension, some of its capabilities/specialization) without ever seeing its config. Whether this is acceptable is a judgment call the ADR should make explicitly, not leave unexamined.
- **Recommendation**: Either accept this explicitly (with a one-line rationale — e.g., "an agent already trusted enough to delegate to another is trusted to learn its skill list") or mitigate it (e.g., a generic "not permitted" failure that doesn't name which of {skill doesn't exist, skill exists but not granted} occurred, at the cost of debuggability the founder may not want to give up). Either way, name the trade-off.

---

#### [MAJ-003] ADR-062 §4.0's table is already stale on a row this ADR isn't touching, undermining "one authority"

- **Lens**: Inconsistency
- **Affected section**: D10 Part A / §6.7 ("**ADR-062 amendment.** … so the secret-set table has one authority.")
- **Description**: Verified: ADR-062 §4.0's table (`docs/internal/architecture/ADR-062-filesystem-read-exec-model-inversion.md:90-91`) still lists `system/` under "must stay agent-accessible" (alongside `logs/`, `browser/`). But `pkg/fspolicy/secretset.go:88-113` already has `"system"` in `SecretEntriesAlways`, with a comment explaining the v0.2 HMAC-chain rationale for denying it. The table and the code have already diverged on this row, silently, with no recorded amendment — exactly the failure mode this ADR's own D10 discipline ("should be written into ADR-062 itself… so the secret-set table has one authority") is trying to prevent going forward for `skills/`.
- **Impact**: An implementer trusting ADR-062's table as ground truth (as this ADR explicitly does — "grounded in… ADR-062 §4.0") is already working from a document that is wrong about `system/`. The `skills/` amendment this ADR proposes will land in the same table without fixing the pre-existing drift, so "one authority" will remain false immediately after this ADR ships.
- **Recommendation**: While already amending ADR-062 §4.0's table for `skills/`, also correct the `system/` row (move it to "must be unreachable," matching code), or at minimum add a note in this ADR (or a companion issue) flagging the pre-existing drift so it isn't mistaken for having been reviewed and accepted.

---

#### [MAJ-004] D8's workspace-keyed cache has no eviction/growth bound or membership-change invalidation

- **Lens**: Infeasibility / Incompleteness
- **Affected section**: D8 ("The workspace id must reach the static-prompt build and become part of the cache key… This is a genuine prerequisite, not a detail.")
- **Description**: D8 correctly identifies that `BuildSystemPrompt()`'s cache is currently per-agent and must become (agent × workspace)-keyed for a correct per-workspace menu, and correctly constrains the *staleness-detection* mechanism (mtime `stat` calls on tracked roots, not a repository scan). It says nothing about the cache's **growth**: an agent that sits on N workspaces (which the ADR itself notes is normal — "the same agent carries the same grants into every workspace," §3 Negative) now has up to N cached prompt variants instead of 1, each potentially carrying a different mount-derived skill menu. Nothing bounds this, evicts stale workspace entries, or specifies what invalidates a cache entry when the agent is *removed* from a workspace (as opposed to a file changing under a still-member workspace).
- **Impact**: Unbounded per-agent cache growth proportional to workspace membership, for what the ADR itself calls "the hottest cache in the prompt path" — exactly the kind of thing that looks fine in testing and degrades slowly in a long-running production install.
- **Recommendation**: Specify an eviction policy (LRU by workspace-last-used, or a hard cap with the oldest entry dropped) and state what removes a stale workspace's cache entry when workspace membership changes (not just when mount contents change).

---

#### [MAJ-005] No test/verification plan for any of the ten decisions

- **Lens**: Incompleteness (Phase 3 structural gap)
- **Affected section**: Whole document.
- **Description**: For a change touching a new tool, a new delegate parameter, a flipped security default (D5), an ADR-062 amendment with a platform-specific kernel-policy change (D10), and a cache-key change to the hottest prompt path (D8), the ADR names zero new tests. It cites existing regression tests for *adjacent, already-shipped* mechanisms (`subturn_target_identity_test.go`, `CreateAgentModal.test.tsx`) but not one test obligation for the behavior this ADR itself introduces: D4's five gate points, D5's flipped nil semantics, D9's three-outcome table, or D10's per-platform closure matrix.
- **Impact**: Nothing stops this ADR's central security claim (D5: default-deny) from silently regressing the way CRIT-002 shows a *pre-existing* piece of code already does, because there's no named test asserting `WithSkillAllowlist(nil)` denies everything post-fix, or asserting the core roster's seeded grants survive an operator's explicit empty-list choice.
- **Recommendation**: At minimum, name required tests for: (1) nil/empty allowlist semantics (D5), (2) each of the five `skillAllowed` gate points (D4), (3) all three D9 outcomes (granted/denied/unresolvable), (4) a Linux-host integration test proving the child-only kernel deny doesn't blind `SkillsLoader` itself (D10's flagged spike).

---

#### [MAJ-006] No observability for the design's single largest accepted trade-off

- **Lens**: Inoperability
- **Affected section**: §3 Negative ("Reliability moves from a deterministic mechanism to a model judgement… a badly-described skill will simply never fire.")
- **Description**: This is named as the ADR's biggest accepted cost, and D2 (better descriptions) is offered as "the mitigation." But nothing proposes a way to *detect*, in production, that a skill is failing to fire when it should — no metric on Skill-menu-shown-vs-Skill-called ratio, no audit signal correlating a task's content with an un-invoked matching skill, nothing an on-call engineer or the founder would check three months from now to answer "is D2 actually working?"
- **Impact**: The ADR's own stated biggest risk is invisible after shipping. A skill silently never firing looks identical, from the outside, to a skill nobody needed.
- **Recommendation**: At minimum, log/audit every `Skill` call (slug, success/deny/not-found) so a later analysis can compare invocation rates against the granted-skill menu size, and consider surfacing a per-skill "last invoked" timestamp in the Skills UI so an operator can spot a granted-but-never-used skill.

---

### MINOR Findings

#### [MIN-001] D3's per-turn reminder wording is unspecified beyond "one line"

- **Lens**: Ambiguity
- **Affected section**: D3, point 2.
- **Description**: Deliberately left as "wording of the effect rather than the mechanism," which is reasonable, but "one line" has no token/character budget, and this text lands in the dynamic context block on every single turn — its cost compounds the same way the force-loaded skill bodies did (§1.2's own complaint).
- **Recommendation**: State an explicit token ceiling for the reminder (e.g., "≤30 tokens"), not just "one line."

---

#### [MIN-002] "Looks like a code project" implies a unified heuristic that doesn't actually exist

- **Lens**: Ambiguity
- **Affected section**: D6 ("when a folder mounted on a workspace looks like a code project, that project's skills and instructions come along.")
- **Description**: In the actual mechanism, there is no single "is this a project" test — there are two independent existence checks (a `.claude/skills/`or `.omnipus/skills/` directory for D6, a root `CLAUDE.md`/`AGENTS.md` for D7), which can each be true or false independently of the other. The prose's "looks like a code project" phrasing invites an implementer to build one unified heuristic (e.g., presence of `.git`) instead.
- **Recommendation**: State explicitly that there is no unified project-detection heuristic — each of the two mechanisms triggers solely off its own path's existence, independently.

---

#### [MIN-003] `Skill`'s search mode states no rate/size bound of its own

- **Lens**: Incompleteness
- **Affected section**: D1 ("`Skill` also takes a `query`… running the same BM25 ranking `ToolSearch` uses.")
- **Description**: Implied to inherit whatever bound `ToolSearch` has, but never stated. Given this ADR is otherwise careful to state caps explicitly (20-entry menu, 262144-byte instructions, 1024-char description), the search mode's silence on this reads as an oversight rather than a deliberate "same as ToolSearch, nothing new" statement.
- **Recommendation**: One sentence: "`Skill`'s search mode inherits `ToolSearch`'s existing [rate/result-count] bound; no new limit is introduced."

---

#### [MIN-004] The write-without-read asymmetry (§3 Negative) compounds with CRIT-001's slug question

- **Lens**: Insecurity
- **Affected section**: §3 Negative ("an agent that cannot read a project skill can still overwrite it").
- **Description**: If CRIT-001 resolves toward "project skills bypass the per-agent grant list" (the D6 reading), this asymmetry gets materially worse: an agent could write a `.claude/skills/<slug>/SKILL.md` inside a mount using a slug that collides with an already-granted **registry** skill it has never read, and have that content silently take over the slug via shelf priority — without the write ever being flagged as skill-relevant (D6's own rule: "a file is a project skill if and only if it lives under a recognised skills directory… no content sniffing").
- **Recommendation**: Resolve CRIT-001 first; if the D6 reading is chosen, revisit whether shelf-priority collision with an *already-granted, different-shelf* slug should be flagged or blocked rather than silently shadowed (see MAJ-001, same underlying mechanism, cross-shelf instead of cross-mount).

---

### Observations

#### [OBS-001] D10 Part A's Linux spike has no owner, timeline, or explicit fallback statement

- **Lens**: Inoperability
- **Affected section**: §6.7 / D10 Part A ("This needs a spike before implementation… it is a blocker for the Linux build.")
- **Suggestion**: The ADR's own framing ("fails closed and loudly… not silently") strongly implies the intended fallback is "ship everything else, keep the app-layer deny, defer the Linux kernel-layer closure" if the spike reveals Landlock can't cleanly do child-only denial — but this is never stated as the explicit fallback. Say so, so an implementer under time pressure doesn't read "blocker for the Linux build" as "blocks this entire ADR."

---

#### [OBS-002] No quantified success metric for the stated cost savings

- **Lens**: Infeasibility
- **Affected section**: §3 Positive ("Grants become free… the per-message cost of skills drops to the menu.")
- **Suggestion**: Add a measurable target (e.g., expected static-prompt token delta for a representative N-skill grant list) so the claim is falsifiable after shipping, not just asserted.

---

#### [OBS-003] Heartbeat/cron-triggered turns are never mentioned

- **Lens**: Incompleteness
- **Affected section**: Whole document.
- **Suggestion**: CLAUDE.md documents per-agent heartbeats as a standing "system actor" for scheduled work. Presumably heartbeat-triggered turns flow through the same `BuildMessages`/`activeSkillNames` path unaffected — but the ADR never says so. A one-line confirmation would close this off rather than leave it implicit.

---

## Structural Integrity

### Variant B: Structured Spec

| Check | Result | Notes |
|-------|--------|-------|
| Every goal/objective has acceptance criteria | PARTIAL | §1.4's three requirements map to D1–D10, but success is stated qualitatively ("grants become free") without measurable thresholds — see OBS-002. |
| Cross-references are consistent | PARTIAL | Spot-checked references to ADR-071, ADR-032, ADR-037, ADR-063, code symbols all verified accurate — but the ADR-062 §4.0 table it cites as authoritative is itself already stale on an unrelated row (MAJ-003), and D4 vs. D6 are internally contradictory (CRIT-001). |
| Scope boundaries are explicit | PASS | In-scope/out-of-scope is stated clearly (N2, D4's "out of scope" carve-out for authoring verbs). |
| Success criteria are measurable | FAIL | No quantified thresholds anywhere; benefits are asserted, not bounded (OBS-002). |
| Error/failure scenarios addressed | PARTIAL | Strong for D4/D9 (loud, structured failures specified in detail); absent for D8 cache growth (MAJ-004) and for multi-mount skill collisions (MAJ-001). |
| Dependencies between requirements identified | PASS | D8 and D10's Linux spike are explicitly flagged as blocking prerequisites — better than most specs reviewed under this skill. |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Regression (existing code) | No test asserts `coreagent.SeedConfig`'s per-boot loop does not clobber an operator's explicit empty skill list | CRIT-002 |
| Negative / security | No named test for each of D4's five `skillAllowed` gate points post-change | MAJ-005 |
| Negative / security | No named test for D9's "denied" and "unresolvable" outcomes specifically (only "granted" is implicitly covered by existing `ForcedSkills` tests) | MAJ-005, D9 |
| Cross-platform | No named Linux-host test proving the child-only kernel deny doesn't blind `SkillsLoader` in-process | D10, §6.7 |
| Concurrency / collision | No test for multi-mount slug collision or cross-shelf slug shadowing | MAJ-001, CRIT-001 |
| Resource growth | No test bounding the (agent × workspace) cache's size or eviction | MAJ-004 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|-----------------|
| Agent skill grant list | Empty (`[]`) on an *existing* core agent across a restart, vs. never-configured | Test `coreagent.SeedConfig` twice in sequence with an operator-set empty list between runs; assert it stays empty (CRIT-002) |
| `requested_skill` slug | Slug that exists in one shelf but not another, for a child granted the registry version only | Exercises CRIT-001/MIN-004's shadowing question directly |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `Skill` tool (load/search) | ok | ok | ok | ok | risk (minor) | ok | No stated rate/size bound of its own on search mode (MIN-003); likely inherits `ToolSearch`'s. |
| `skillAllowed` grant gate | ok | risk | ok | ok | ok | risk | Tampering/Elevation risk if project skills bypass the gate (CRIT-001); shadowing an already-granted slug via a mount (MIN-004). |
| `delegate`'s `requested_skill` (D9) | ok | ok | ok | **risk** | ok | ok | Enumeration oracle for the target's full skill grant list (MAJ-002). |
| Mount-derived project skills (D6) | ok | **risk** | ok | ok | ok | **risk** | Slug collision across mounts is unresolved (MAJ-001); slug shadowing of a registry grant is unresolved (CRIT-001, MIN-004). |
| `$OMNIPUS_HOME/skills` kernel deny (D10 Part A) | ok | ok | ok | ok | ok | ok | Well-analyzed in the ADR itself; Linux spike correctly flagged as unresolved, not silently assumed. |
| `coreagent.SeedConfig` skill re-seed (existing code, unmentioned) | ok | **risk** | ok | ok | ok | ok | Silently overwrites operator intent on every boot (CRIT-002). |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. Does the per-agent grant list (`skillAllowed`) apply to mount-derived project skills at all, or does mounting bypass it? (CRIT-001)
2. If an operator empties a core agent's skill list on purpose, does it stay empty across a restart — and has anyone run `coreagent.SeedConfig` twice in sequence to check? (CRIT-002)
3. When two mounted repositories in the same workspace define a project skill with the same slug, which one loads? (MAJ-001)
4. Is it acceptable that any agent permitted to delegate to another can enumerate that agent's full skill grant list via `requested_skill` probing? If so, say why; if not, what changes? (MAJ-002)
5. Given ADR-062's table is already wrong about `system/`, should this ADR fix that row while it's already amending the table, or file it separately? (MAJ-003)
6. What bounds the (agent × workspace) prompt-cache's memory growth, and what invalidates an entry when workspace *membership* (not file content) changes? (MAJ-004)
7. What test suite proves D5's flipped nil-allowlist semantics actually ship correctly, given CRIT-002 shows adjacent code already fighting that exact semantic? (MAJ-005)
8. If the Linux child-only Landlock spike (D10 Part A) fails or is delayed, does the rest of this ADR (menu, `Skill` tool, D9) still ship on Linux without that specific kernel-layer closure, or is the whole ADR blocked? (OBS-001)

---

## Verdict Rationale

REVISE, not BLOCK, because the ADR's evidentiary base is genuinely strong — every code citation checked (skillAllowed, ForcedSkills, SkillListTool, the delegate snapshot channel, the Agent.yaml contract text, inherit_skills) was accurate, and the founder-resolution mechanism (§6) shows real adversarial thinking already happened during drafting. But two CRITICAL findings mean this cannot go to implementation as written: CRIT-001 (D4 vs. D6 contradiction on whether project skills are grant-gated) is not a nuance, it's a coin-flip that determines whether the feature's headline promise or its headline security claim is the one that's true, and an implementer will guess one way or the other without knowing they're guessing. CRIT-002 is worse in a different way — it's not a gap in the design, it's a piece of already-shipped code that will quietly undo this ADR's central security fix (D5) for the entire core agent roster, and the ADR shows no awareness it exists. Both are concrete, code-grounded, and fixable with a paragraph each; neither requires redesigning the ADR's actual architecture.

### Recommended Next Actions

- [ ] Resolve CRIT-001: state explicitly whether project-sourced skills go through the per-agent `skillAllowed` gate, and reconcile D4's and D6's language accordingly.
- [ ] Resolve CRIT-002: decide whether `pkg/coreagent/core.go`'s skill re-enforcement loop (line 1741) is gated to fresh-install-only, or whether D5 explicitly exempts the core roster from "empty means none."
- [ ] Address MAJ-001 through MAJ-006 — at minimum, state the multi-mount collision rule (MAJ-001), name the D9 enumeration trade-off explicitly (MAJ-002), and add the missing test obligations (MAJ-005).
- [ ] Correct or flag ADR-062's `system/` row while amending its table for `skills/` (MAJ-003).
