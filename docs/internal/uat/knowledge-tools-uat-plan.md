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
| Gateway | local binary, embedded SPA, isolated `OMNIPUS_HOME` |
| Vault | a purpose-built fixture corpus (§3), never the founder's real vault |
| Tester model | GLM 5.3 Flash via OpenRouter |
| Tester agent | new, dedicated; all `knowledge_*` tools explicitly allowed |
| Transport | HTTP + WebSocket against the gateway API |

**The founder's real vault is never the target.** Every run works on a
disposable copy or a synthetic corpus. Write scenarios mutate data by design.

### 2.1 The tester agent

A NEW agent, created via the API for this purpose. It must not be Mia, Jim,
Ava or Ray — reusing a roster agent would contaminate its persona and its tool
policy, and would make a tool-policy failure indistinguishable from a
roster-seeding difference.

Because this repo forbids any default tool-policy fallback, the create request
must carry an **explicit, literal, wildcard-free `allow` entry for every
`knowledge_*` tool the suite exercises**. A missing entry is a boot-time abort
or a 400, not a silent denial — which is good, and the suite should verify it
by asserting the agent can actually call each tool before any scenario runs
(§4, Suite Z).

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
| Z-01 | Tester agent exists with the intended model | agent responds and reports its own model as the GLM id | wrong model → every later timing and quality number is about a different model |
| Z-02 | Every `knowledge_*` tool is callable | each tool invoked once, returns a non-denial | a denial here means the tool policy is wrong and all later "tool did nothing" results are misattributed |
| Z-03 | The corpus loaded and the schema is as expected | note count and per-type counts match the answer key | a short corpus silently makes every recall number optimistic |

**Z is not a formality.** Three of the four false readings recorded in this
project came from a harness that was not measuring what it claimed to measure.

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

Every tool gets at least one happy path and one failure path. Tools with no
scenario are listed explicitly as untested rather than omitted silently.

`knowledge_create`, `knowledge_read`, `knowledge_edit`, `knowledge_set_property`,
`knowledge_append_section`, `knowledge_link`, `knowledge_move`,
`knowledge_rename`, `knowledge_restructure`, `knowledge_search`,
`knowledge_find`, `knowledge_describe`, `knowledge_graph`, `knowledge_tasks`,
`knowledge_configure`, `knowledge_version_conflict`.

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

- Exact OpenRouter model id string for GLM 5.3 Flash. This codebase prefixes
  OpenRouter models with `openrouter/` (`pkg/config/defaults.go` carries
  `openrouter/auto` and `openrouter/openai/gpt-5.4`, both against API base
  `https://openrouter.ai/api/v1`), so the expected string is
  **`openrouter/z-ai/glm-5.3-flash`**. Model ids are free-form config, not a
  whitelist, so a wrong id fails at CALL time rather than at create time —
  which is exactly why Z-01 asserts the model before any scenario runs.
- Whether the API exposes a synchronous ask-and-wait path or the harness must
  consume WS frames (affects harness shape, not scenarios).
- Whether concurrency scenarios need separate agents or separate sessions of
  one agent — C-03's meaning differs between the two, and this must be settled
  before C is run.
