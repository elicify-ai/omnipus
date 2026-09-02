# Knowledge & Vault Tools — UAT Plan (agent-driven)

**Status:** draft, not yet executed
**Testers:** real Omnipus agents driven over the API, not humans and not Go tests
**Tester model:** GLM 5.3 Flash via OpenRouter
**Tester agent:** a new, dedicated agent created for this purpose — never one of the built-in roster

---

## 1. Why this plan exists, and what it is not

The Go suite already proves the knowledge tools behave correctly when called
correctly. It cannot tell us the thing we actually need to know: **whether an
agent that is not an expert in this vault can use these tools without
degrading it.** That question is only answerable by putting a real model behind
the tools and watching what it does.

So the testers here are Omnipus agents, and their job is explicitly
**adversarial**. They are instructed to try to break the vault, not to confirm
it works. A suite in which every agent reports success is a suite that has told
us nothing.

**This plan is not:**
- a benchmark of the model's intelligence — a tester failing to write a valid
  note is a FINDING about the tools, not about GLM;
- a replacement for the Go tests — it sits on top of them;
- a UI test — everything here goes through the API.

### 1.1 The false-green rule, applied to this plan

`docs/internal/false-green-patterns.md` governs this document. Concretely:

- **Every scenario states, in advance, what a FAILING run looks like.** A
  scenario with no described failure mode is unfalsifiable and does not count
  as evidence, however green it comes back.
- **Ground truth is fixed BEFORE the run**, committed as a fixture, never
  derived from the run's own output. A search test whose expected answers were
  read off the result set measures nothing.
- **A tester's own claim of success is not evidence.** Every pass must be
  independently checkable from the captured artefacts (WS frames, vault diff,
  timings) without trusting the tester's narration.
- **Absence of an error is not a pass.** If a tool silently did nothing, that
  is a FAIL and the scenario must be written so it can tell the difference.

---

## 2. Environment

| item | value |
|---|---|
| Gateway | local binary, isolated `OMNIPUS_HOME`, port 5000 (`pkg/config/defaults.go`) |
| Vault | a purpose-built fixture corpus (§3), never the founder's real vault |
| Tester model | `model: "z-ai/glm-5.3-flash"`, `provider: "openrouter"` — **two fields, bare slug** |
| Tester agent | new, `type: Main`, six knowledge tools explicitly `allow` |
| Auth | `Authorization: Bearer <token>` from `$OMNIPUS_HOME/cli.token` |
| Transport | REST for setup, **WebSocket `/api/v1/chat/ws`** to drive turns |

**The model field is two fields, not one.** An earlier draft of this plan said
the id was `openrouter/z-ai/glm-5.3-flash`, inferred from `pkg/config/defaults.go`.
That combined form is **legacy** — the config loader splits it on migration.
The API takes a bare vendor slug in `model` and the routing key separately in
`provider`. The vendor namespace before the slash (`z-ai`) is not the provider.

**Setup is scriptable end to end**, in this order:

1. `omnipus start` with a throwaway `OMNIPUS_HOME` — this mints both
   `master.key` and `cli.token`, so there is no credential-unlock step.
2. `POST /api/v1/onboarding/complete` — creates the admin AND stores the
   OpenRouter key encrypted, in one CSRF-exempt, unauthenticated call.
   ⚠️ **It performs a real, billable provider probe.** A key the provider
   rejects returns 400 and persists nothing, so a placeholder will not do.
   Rate limited to 3/IP/min and can take ~25s.
3. `POST /api/v1/workspaces` with `core_team: [agentID]` — see §2.2, this is
   not optional.
4. `POST /api/v1/agents` — §2.1.
5. WebSocket, auth frame first, then message frames.

`--allow-empty` is deprecated and inert. `gateway.dev_mode_bypass` must stay
**off** — with a real CLI token it is unnecessary, and it makes some admin
routes return 503.

**The founder's real vault is never the target.** Every run works on a
disposable copy or a synthetic corpus. Write scenarios mutate data by design.

### 2.1 The tester agent

A NEW agent, created via the API for this purpose. It must not be Mia, Jim,
Ava or Ray — reusing a roster agent would contaminate its persona and its tool
policy, and would make a tool-policy failure indistinguishable from a
roster-seeding difference.

It is `type: Main`. `Subagent` and `subagent_3p` are delegation-only workers
and are structurally excluded from chat (`AgentInstance.IsWorker` — "a worker
is never a chat target"), so neither can be driven this way. `core` and
`system` are not creatable at all.

```json
POST /api/v1/agents
{
  "type": "Main",
  "name": "UAT Tester",
  "soul": "...",
  "model": "z-ai/glm-5.3-flash",
  "provider": "openrouter",
  "tools_cfg": { "builtin": { "policies": {
    "knowledge_describe": "allow", "knowledge_find": "allow",
    "knowledge_read": "allow", "knowledge_edit": "allow",
    "knowledge_restructure": "allow", "knowledge_configure": "allow"
  } } }
}
```

**The policy map may be sparse here.** `POST /agents` seeds a complete
deny-everything map first and merges these entries on top, so coverage is
satisfied. That is specific to create — `PUT /agents/{id}/tools` does not merge
and a sparse map there can 400.

**Use `allow`, never `ask`.** An `ask` policy emits a `tool_approval_required`
frame and blocks the turn until a REST call resolves it. The built-in roster
seeds `knowledge_edit`/`restructure`/`configure` as `ask` — for an unattended
run that is a hang, and it is a second reason not to reuse a roster agent.

**A coverage gap does not fail loudly.** Boot repairs-then-validates: a missing
entry is backfilled to `deny` with one WARN line, so a forgotten tool ships
silently denied rather than aborting. Z-02 exists because of this.

### 2.2 Workspace scope — the trap that makes every knowledge tool a no-op

All six tools resolve their scope from the **turn's workspace**, never from a
tool argument (`pkg/knowledge/scope_turn.go::ResolveTurnScope`). A freshly
created agent belongs to no workspace's `core_team`, so the fallback finds
nothing, the scope is empty, and **the agent is told in plain words that no
knowledge base is available — no error, no log, no failed turn.**

Every scenario would "pass" by finding nothing.

So the harness must do BOTH, belt and braces:

- create the workspace with the tester in `core_team`, and
- send `metadata.workspace_id` on every message frame.

Z-03 asserts a known note is readable before any scenario runs, which is what
turns this from a silent zero into a caught misconfiguration.

*Unverified:* whether the server checks that `metadata.workspace_id` names a
workspace the agent actually belongs to. Worth confirming before relying on the
header alone.

---

## 3. The corpus, and why it is synthetic

Search accuracy needs **known** answers. A real vault cannot provide them: no
one can say with certainty that exactly 7 notes are about a topic.

The corpus is therefore generated, with the answer key generated alongside it:

- **Scale:** enough notes that search is non-trivial (target ~2,000), with a
  smaller ~200-note tier for fast iteration.
- **Planted answers:** for each search scenario, a known set of notes is
  planted to match and a known set of near-misses planted NOT to match
  (same words, different meaning; matching words in a different property;
  matching text inside a code block).
- **Distractors are the point.** A search that returns the planted matches AND
  the near-misses has failed on precision even though it "found everything".
- **Type coverage:** the corpus declares every property type the schema
  supports — text, integer, decimal, date, checkbox, enum, relation, person —
  and includes list-valued and empty-valued cases for each.
- **Deliberate invalidity:** a known number of notes violate their own schema,
  so tools that must cope with a partly-invalid vault are exercised on one.

The corpus generator and its answer key are committed together. **The key is
never regenerated from a run.**

---

## 4. Suites

Each scenario below states: what the tester is asked to do, what a PASS
requires, and — mandatory — **what a FAIL looks like**.

### Suite Z — Preflight (must pass before anything else runs)

| id | scenario | pass | fail |
|---|---|---|---|
| Z-01 | Tester agent runs the intended model | `GET /api/v1/agents/{id}` reports `model: z-ai/glm-5.3-flash`, `provider: openrouter`, and a turn completes | wrong or unroutable model → every later timing and quality number describes a different model. Ids are free-form config, not a whitelist, so a typo fails at CALL time, not create time |
| Z-02 | All six tools resolve to `allow` | `GET /api/v1/agents/{id}/tools` shows `effective_policy == "allow"` for all six | any `ask` (turn hangs unattended) or `deny`. **Check `effective_policy`, not `configured_policy`** — resolution is most-restrictive-wins across global × agent, so a global ceiling silently overrules a per-agent allow |
| Z-03 | The agent can actually see the corpus | a `knowledge_read` of a known planted note returns its content | empty scope → the agent is told "no knowledge base is available", every scenario finds nothing, and every suite passes vacuously. **This is the single most dangerous failure in the plan** (§2.2) |
| Z-04 | Corpus matches its answer key | note count and per-type counts equal the key's metadata | a short corpus makes every recall number optimistic |
| Z-05 | The harness detects a failed turn | a deliberately impossible request yields `done.stats.turn_failed == true` and the harness reports it | harness ignores the flag → a failed turn is indistinguishable from a successful one, and every later "the agent did nothing" result is misattributed |

**Z is not a formality.** Z-03 and Z-05 are both silent-zero classes: without
them a completely broken run reports green. Three of the four false readings
recorded in this project came from a harness that was not measuring what it
claimed to.

### Suite A — Search accuracy

| id | scenario | pass | fail |
|---|---|---|---|
| A-01 | Exact-term search for a planted term | returns exactly the planted set | any near-miss returned (precision), any planted note missing (recall) |
| A-02 | Search where distractors share vocabulary | planted set only | near-misses returned → the index is matching words, not meaning as claimed |
| A-03 | Property-scoped find (`knowledge_find`) on each property type | exactly the notes whose property matches | a type where the filter silently matches everything or nothing |
| A-04 | Filter on an enum value that exists | exact match set | returns 0 (the refusal path fired silently) or returns all |
| A-05 | Filter on an enum value that does NOT exist | a clear refusal naming the legal values | silently returns 0 rows — indistinguishable from "no matches", the worst outcome |
| A-06 | Negation (`!=`, `not:`) on a property some notes do not carry | the absence rule is applied as documented | more rows than the positive complement — the broadening bug class |
| A-07 | Search over a partly-invalid corpus | invalid notes handled per contract, stated either way | whole request refused because one note is bad |

**A-05 and A-06 are the highest-value scenarios here.** Both are silent-wrong
classes: they return a plausible answer that is wrong, which no amount of
eyeballing catches.

### Suite B — Search efficiency

Measured, not felt. Every number is p50/p95 over ≥20 runs, captured from the
harness rather than reported by the tester.

| id | scenario | pass | fail |
|---|---|---|---|
| B-01 | Latency vs corpus size (200 vs 2,000 notes) | scaling is sub-linear or explained | linear-or-worse growth unexplained → the index is not being used |
| B-02 | Latency by query shape (term / property filter / negation / aggregate) | each within a stated budget | one shape an order of magnitude slower than its peers |
| B-03 | Large result set (limit at maximum) | completes within budget, result truncation is stated | truncation happens silently — the caller believes it saw everything |
| B-04 | Repeated identical query | stable timings | high variance → contention or a cold path on every call |

Budgets are set from the FIRST measured run and recorded, not guessed in
advance; the point of B is to establish them and then detect regression.

### Suite C — Concurrency (read/write under contention)

The most important suite, and the one most likely to find a real defect.

| id | scenario | pass | fail |
|---|---|---|---|
| C-01 | N agents read the same note concurrently | all get identical, valid content | any torn or partial read |
| C-02 | N agents write DIFFERENT notes concurrently | all writes land; final vault matches the union | any lost write |
| C-03 | N agents write the SAME note concurrently | either all serialize correctly, or losers get a version-conflict refusal | **a silently lost update** — the defect this suite exists to find |
| C-04 | Concurrent read while another agent writes | reader sees either the old or new value, never a mixture | a half-written frontmatter observed |
| C-05 | Concurrent schema change (`knowledge_configure`) during writes | schema change either blocks or is rejected coherently | notes written against a schema that no longer exists |
| C-06 | Sustained mixed load, N agents, several minutes | no corruption, no deadlock, throughput recorded | any vault file left unparseable |

`expect_version` exists on the write tools — **C-03 must verify it actually
prevents a lost update, not merely that the parameter is accepted.** The test
must be constructed so a build that ignores `expect_version` FAILS.

N is scaled up across runs (2, 8, 32) rather than fixed, because contention
bugs are load-dependent.

### Suite D — Deterministic type safety (the write path)

This suite grades the tool changes made alongside this plan. Its premise: **an
agent with no knowledge of the vault's schema should be unable to corrupt it,
and should be told precisely how to proceed.**

| id | scenario | pass | fail |
|---|---|---|---|
| D-01 | Write a valid value to a typed property | accepted | rejected → the guard is too strict to use |
| D-02 | Write a value of the wrong type (e.g. prose into a date) | refused, message names the expected type AND an example | accepted → the vault is corrupted silently |
| D-03 | Write a value outside a declared enum | refused, message lists the legal values | accepted, or refused without listing them (agent cannot proceed) |
| D-04 | Write a MISSPELLED property name | refused, message suggests the near-match | accepted → a new undeclared property is created silently |
| D-05 | Write a genuinely new property | refused with the exact `knowledge_configure` call that would declare it | accepted silently, or refused with no way forward |
| D-06 | Correct an already-invalid value on an existing note | accepted | refused → agents cannot repair the vault |
| D-07 | Write to a note whose record type is unknown | behaviour is stated and consistent | differs run to run |
| D-08 | **Can the tester recover unaided?** After any refusal, the agent proceeds correctly using only the refusal text | agent succeeds without being told the schema | agent loops, guesses, or gives up → the message is not actionable, which is the whole point of the change |

**D-08 is the acceptance criterion for the entire type-safety change.** The
others check the guard fires; D-08 checks it achieves its purpose — removing
the need for the agent to be a vault expert. A guard that refuses correctly but
leaves the agent stuck has moved the problem, not solved it.

### Suite E — Coverage of every vault tool

**There are SIX registered knowledge tools, not sixteen.** An earlier draft of
this plan listed sixteen names harvested by grep. Ten of those
(`knowledge_create`, `knowledge_set_property`, `knowledge_append_section`,
`knowledge_link`, `knowledge_move`, `knowledge_rename`, `knowledge_search`,
`knowledge_graph`, `knowledge_tasks`, `knowledge_version_conflict`) are
**retired and unregistered** — they compile, they have no production caller,
and no agent can invoke them. Writing scenarios for them would have produced
ten permanently-skipped tests, or worse, ten that appeared to pass.

The six live tools, registered unconditionally for every agent by
`pkg/agent/knowledge_tools.go::registerKnowledgeTools`:

| tool | writes? | must cover |
|---|---|---|
| `knowledge_describe` | no | schema discovery — this is how an agent learns the vault |
| `knowledge_find` | no | Suites A and B in full |
| `knowledge_read` | no | happy path + a note that does not exist |
| `knowledge_edit` | **yes** | every `op`: `create`, `set_property`, `link`, `append_section`, `replace_body` |
| `knowledge_restructure` | **yes** | every `op`: `rename`, `move`, `trash`, `restore` |
| `knowledge_configure` | **yes** | schema create / edit / delete, and the cascade report |

Two write paths deserve their own scenarios because recon identified them as
under-checked:

- **E-01 — `knowledge_edit` `op: create` with a `body` that contains its own
  `---` frontmatter block.** Historically unvalidated (gap G1 in the design).
  PASS: refused or validated. FAIL: written unchecked.
- **E-02 — `knowledge_restructure` rename, where inbound notes reference the
  renamed note from a `relation` property.** The rename rewrites wikilinks
  *inside frontmatter* by byte offset with no schema awareness (gap G2). PASS:
  every rewritten note still validates. FAIL: a rewrite that leaves a note
  non-conforming.

### Suite F — Critical feedback (unscripted)

Each tester is given the tools, the corpus, a goal, and **no script**, and is
asked to report what was confusing, what it got wrong and why, and what it
would change. Prompts are adversarial: *"try to corrupt this vault"*, *"find
something the search cannot find"*, *"make two of your writes conflict"*.

This suite has no pass/fail. Its output is a findings list. It is included
because scripted scenarios only ever find the failures we already imagined.

---

## 5. How results are recorded

Per run, committed under `docs/internal/uat/`:

- a report naming the exact build (commit sha), model id, corpus tier and N;
- raw WS frame logs per tester session;
- the timing table for Suites B and C, from the harness;
- a vault diff before/after every write suite;
- every finding, including ones with no fix, with a severity and an owner.

A scenario that was not run is recorded as **not run** — never as passed, and
never omitted.

---

## 6. Open questions

Most of the original opens are now closed by recon and recorded inline (§2,
§2.1, §2.2, Suite E). What remains:

- **Does the server validate `metadata.workspace_id` against the agent's actual
  membership?** Untraced. If it does not, the header alone is enough; if it
  does, `core_team` membership is mandatory. The plan does both, so this is a
  simplification question rather than a blocker.
- **Separate agents or separate sessions for the concurrency suite?** C-03's
  meaning differs between the two — separate sessions of one agent share more
  server-side state than separate agents do. Must be settled before C runs, and
  the answer recorded in the run report, because a lost-update result means
  different things under each.
- **An OpenRouter key with real credit is required.** Onboarding performs a
  billable probe and refuses a key the provider rejects. There is no
  `--skip-verify` on the REST path (the CLI has one). Budget for it: the
  concurrency suite at N=32 across several minutes is the expensive part.

---

## 7. Corrections made to this document

Recorded rather than quietly edited, because a plan that silently changed its
own facts is a plan nobody can audit:

1. **Sixteen tools → six.** The first draft listed tool names harvested by
   grep; ten are retired and unregistered, and scenarios for them would have
   been permanently unrunnable.
2. **`openrouter/z-ai/glm-5.3-flash` → `model` + `provider` as two fields.**
   The combined form is a legacy shape the config loader splits on migration;
   the API takes a bare vendor slug.
3. **Workspace scope added (§2.2).** It was absent from the first draft, and it
   is the one misconfiguration that makes every scenario pass while testing
   nothing.

---

## Suite G — Index freshness (added 2026-09-02)

Grades `docs/internal/design/knowledge-index-freshness.md`. Every scenario
states what a FAILING run looks like; a scenario without one is unfalsifiable
however green it comes back.

**Ground truth is read from the vault on disk and from the index, never from the
agent's narration.** An agent saying "I can find it" is not evidence that the
index contains it.

### G-1 — read-your-own-write · **the load-bearing scenario**

| | |
|---|---|
| do | agent writes a note through `knowledge_edit`, then searches for its content **in the same turn, with no delay** |
| pass | the note is found |
| fail | not found — the index was updated asynchronously, and an agent cannot trust its own writes |

**This is the scenario the whole direct-update layer exists for.** If it fails,
the design's §4 argument has not been implemented, whatever the unit tests say.

### G-2 — writes through every op

Each `knowledge_edit` op (`create`, `set_property`, `link`, `append_section`,
`replace_body`) and each `knowledge_restructure` op (`rename`, `move`, `trash`,
`restore`) leaves the index correct immediately.

**FAIL:** any op whose change is not searchable straight afterwards. Rename is
the sharp one — it must update BOTH the old path (gone) and the new (present).

### G-3 — a file added outside Omnipus, while it is running

| | |
|---|---|
| do | write a `.md` file into the collection directly on disk; wait a short bounded period |
| pass | it becomes findable without a restart |
| fail | still not findable → the watcher is not running, or is not reaching the index |

### G-4 — a file EDITED outside Omnipus

Old content must stop matching and new content start matching. **FAIL:** the old
term still matches — a stale document was left behind, which is worse than a
missing one because it answers confidently.

### G-5 — a file DELETED outside Omnipus

**FAIL:** it still matches. A search returning a note that no longer exists is
the same confident-wrong-answer class as F-9.

### G-6 — burst escalation, not event-dropping

| | |
|---|---|
| do | create/modify a large number of files at once (hundreds), as an iCloud sync or a bulk copy would |
| pass | ALL of them end up correctly indexed, and the run shows the burst was handled by a sweep rather than N individual updates |
| fail | any file missing → events were dropped, which is the silent-staleness failure the design forbids |

**Assert the escalation happened, not only the end state.** A correct end state
reached by processing 500 events individually is a different system from the one
designed, and would fail differently under load.

### G-7 — restart is incremental, not a rebuild

| | |
|---|---|
| do | restart the gateway over an already-indexed collection |
| pass | the sweep reports the files as unchanged and re-indexes ~none |
| fail | everything re-indexed → requirement 2 regressed |

### G-8 — attachments stay body-free on EVERY path

A `.pdf` written through a tool, and one added externally, must both be findable
by **name** with their body text **not** searchable.

**FAIL:** body text matches → "instant indexing" has quietly started extracting
documents, reversing the operator's ruling through a side door. This is the
negative assertion that keeps O1 enforced rather than merely documented.

### G-9 — watching unavailable is STATED

| | |
|---|---|
| do | run where watching cannot start (unsupported platform, or the OS refuses) |
| pass | the condition is visible, and the periodic sweep still keeps the index correct |
| fail | silence — which is Obsidian's actual failure mode and the one §8 exists to prevent |

### G-10 — Omnipus's own writes do not cause double work

Its own writes trigger its own watcher. The event must arrive, the hash match,
and nothing happen.

**FAIL:** a second re-index of the same unchanged file — harmless to
correctness, but it shows the hash check is not doing its job, and under a burst
that doubling is what turns a manageable load into a queue overflow.

---

## Suite B — Search efficiency (now runnable)

Previously blocked: the corpus was unindexed and `knowledge_find` was uncallable.
Both are fixed, so Suite B can finally run. Every number is p50/p95 over ≥20
runs, taken from the harness rather than reported by the agent.

| id | scenario | fail |
|---|---|---|
| B-01 | latency vs corpus size (200 vs 2,000 notes) | linear-or-worse growth, unexplained |
| B-02 | latency by query shape — term, property filter, negation, aggregate | one shape an order of magnitude off its peers |
| B-03 | result set at the limit | truncation happens **silently** |
| B-04 | repeated identical query | high variance → contention or a cold path every call |
| B-05 | **indexing** a 2,000-note collection from cold | no budget yet — this run establishes it |
| B-06 | a single-file update's latency | must be far below a full sweep, or the direct-update layer buys nothing |

Budgets are ESTABLISHED by the first run and recorded; the point of B is to fix
them so a later regression is detectable.

---

## Suite C — Concurrency (extended)

C-01…C-06 stand as written. One passed already: five agents writing the same
note produced four version-conflict refusals, one success, zero lost updates.

Added, because indexing is now a concurrent writer too:

| id | scenario | fail |
|---|---|---|
| C-07 | N agents write DIFFERENT notes while a sweep runs | any write missing from the index afterwards |
| C-08 | an agent writes while the watcher fires on the same file | double-index, or a lost update |
| C-09 | sustained mixed read/write, several minutes | any unparseable file, or an index that disagrees with disk at the end |
| C-10 | a burst arrives DURING a sweep | events lost, or the two writers corrupting each other's manifest |

**C-10 is the sharp one.** The design says a sweep and a single-file update
serialise on the same lock; this is the scenario that proves it under real load
rather than in a unit test.
