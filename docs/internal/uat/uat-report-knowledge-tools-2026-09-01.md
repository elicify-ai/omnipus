# UAT report — knowledge tools, agent-driven (2026-09-01)

**Status:** partial run. Suites Z and D executed; A, B, C, E, F not run.
**Testers:** one Omnipus agent, `type: Main`, created for this run.
**Model:** `z-ai/glm-5.3-flash`, provider `openrouter` — verified live, not assumed.
**Build:** working tree at the time of the run, before the G1/G3/G4 fixes landed.
**Corpus:** a 3-note smoke vault, hand-built. **Not** the generated corpus the
plan specifies — so no accuracy or efficiency number here is meaningful, and
Suites A and B were deliberately not attempted.

---

## 1. Headline

**The live write path already enforces its schema, and enforces it well.** The
worry that motivated this work — that an agent could silently corrupt the vault
through the ordinary tools — **is not true of `knowledge_edit`'s
`set_property`**, which refuses and explains itself well enough that the agent
recovered unaided.

**One real corruption vector was found and reproduced**: the identical value
that `set_property` refuses is accepted without any check when it arrives
inside `op: create`'s `body`. One door is locked and the other is open.

---

## 2. Preflight — Suite Z

| id | result | evidence |
|---|---|---|
| Z-01 model | **PASS** | `GET /agents/{id}` → `model: z-ai/glm-5.3-flash`, `provider: openrouter`. Also confirmed against OpenRouter's live model list before onboarding, rather than discovered during a billable probe. |
| Z-02 policies | **PASS** | `GET /agents/{id}/tools` → all six knowledge tools `effective_policy: allow` (and `configured_policy: allow`). The sparse create-time policy map merged onto the deny-all seed exactly as the contract says. |
| Z-03 scope | **PASS** | The agent called `knowledge_read` on a planted note and returned its `status`. This is the check that would otherwise let every later suite pass by finding nothing. |

Turn latency on this model: **22s–94s** per turn, single agent, no contention.
Recorded as an observation, not a budget — B was not run.

---

## 3. Suite D — type safety

### D-03 — value outside a declared enum · **PASS**

Asked to set `status: liquidated` where the schema permits
`active, dormant, acquired`.

Tool refused, verbatim:

```
knowledge.note.edit: knowledge: value does not conform to the declared
property: company.status holds "liquidated", which is not a single
enum(active, dormant, acquired); permitted values are active, dormant, acquired
```

Vault checked directly afterwards: `status: dormant`, unchanged. The refusal
names the property, the offending value, the constraint and the full permitted
set — everything §3 of the design asks a class-3 refusal to carry.

### D-08 — can the agent recover unaided? · **PASS**

This is the acceptance criterion for the whole design, and it held on the first
attempt. From the refusal text alone — with no schema lookup and no help — the
agent identified both correct routes: use one of the permitted values, or
change the schema through `knowledge_configure`. It did not guess, did not
loop, and did not retry the same write.

**The bar the design sets is already met on this path.**

### D-04 — misspelled property name · **PASS**

Asked to set a property named `stauts`. Refused:

```
knowledge.note.edit: knowledge: record schema declares no such property:
company declares no property "stauts"; declared properties are name, status,
revenue, founded
```

No stray property was created — verified on disk (`grep -c '^stauts:'` → 0).

**This honestly downgrades one piece of planned work.** The design proposed
adding a nearest-match "did you mean?" to this refusal. With four declared
properties, listing all of them already makes the intended spelling obvious and
the agent said so unprompted. The improvement is real but it is a
**nice-to-have on wide record types**, not a fix for a defect. It should be
described that way rather than as closing a gap.

### D-06 — repairing an already-invalid note · **PASS**

`Delta.md` was left invalid by the G1 failure below (`status: liquidated`,
`revenue: not-a-number`). Asked to set `status` to `dormant`: accepted, and the
vault confirms `status: dormant` while `revenue: not-a-number` remains.

This is the design's §4 ruling — *judge the value being written, not the whole
record* — already correctly implemented. An agent can repair a partly-invalid
note one property at a time, which is exactly what the founder's vault (27
invalid records) will need.

### G3 — write to a note whose record type has no schema · **FAIL (as designed, and that is the problem)**

Planted `Echo.md` with `type: spaceship`, which no schema declares. Asked to
set `warp` to `banana` — an undeclared property, on an undeclared type,
replacing a number with prose.

**Accepted. Verified on disk: `warp: banana`.**

The write proceeding is the specified behaviour and the design does not propose
changing it — untyped notes are a supported state. **The defect is the
silence.** Asked whether anything had been validated, the tester had to run
`knowledge_describe`, inspect the schema list and re-read the note to work out
that nothing had been. Its own words: *"Essentially no content validation was
observable"* — a conclusion it reached by investigation, not because the tool
said so.

That is G3 exactly: the agent cannot distinguish "validated and fine" from
"nothing was checked", and the three distinct misses (unparseable frontmatter,
absent type, undeclared type) are indistinguishable from each other too.

**This doubles as the first Suite F result.** Unprompted, the tester produced a
precise four-point account of what was not checked and how it knew. The
adversarial framing works.

### G1 / E-01 — `op: create` with a body carrying its own frontmatter · **FAIL**

The defect, reproduced with a live agent. Asked to create `Delta.md` passing
**only** a `body` argument containing:

```
---
type: company
status: liquidated
revenue: not-a-number
---
```

Tool response: `Delta.md — version v1:… CREATED (81 bytes)`. **Accepted.**

Verified on disk — the note exists with exactly that frontmatter.

Two schema violations were written that the same tool refuses through
`set_property`:

- `status: liquidated` — **the identical value refused in D-03, seconds earlier**;
- `revenue: not-a-number` — prose in a `decimal` property.

**Why this is the finding that matters.** It is not that validation is missing;
it is that validation is *inconsistent between two doors into the same tool*.
An agent behaving perfectly reasonably — building a note body as text, which is
the natural way to write a note — bypasses a guard that works correctly two
lines away. Nothing warns, nothing logs, and the note reads back with its
violations flagged only if someone later looks.

Severity: **high**. This is the live agent path, not a retired tool.

---

## 4. Incidental findings

**F-01 · `omnipus start` cannot mint `cli.token` on a genuinely fresh home.**
Observed on a clean `$OMNIPUS_HOME`: startup logs
`Warning: could not ensure cli token: update config.json: read config.json:
open …/config.json: no such file or directory`, and no `cli.token` is written.
It writes through `config.json`, which onboarding creates. So the documented
"just start the gateway and read `cli.token`" path does **not** work on the
first boot — the only token available is the one `POST /onboarding/complete`
returns. Low severity, but it blocks exactly the first-run scripting case the
CLI token exists for. The harness now accepts `--token` / `--token-file` and
falls back to `cli.token`.

**F-02 · `load_tool` fires before knowledge calls.** Each turn shows one or two
`load_tool` calls ahead of the real tool. Expected (tools are loaded on demand)
and noted only so a reader of the frame logs is not surprised.

---

## 5. What was NOT run, and why

Recorded as not-run rather than omitted, per the plan's own rule.

| suite | status | reason |
|---|---|---|
| A — search accuracy | **not run** | needs the generated corpus and its committed answer key; the smoke vault has no ground truth, so any number would be meaningless |
| B — search efficiency | **not run** | same; 3 notes cannot show scaling |
| C — concurrency | **not run** | needs the multi-agent harness path; single-agent only so far |
| D-01/02/05/07 | **not run** | D-03, D-04, D-06, D-08 and G1/G3 were exercised |
| E — tool coverage | **partial** | `knowledge_read`, `knowledge_edit` (`set_property`, `create`) only. `find`, `describe`, `restructure`, `configure` untouched |
| F — critical feedback | **partial** | not run as its own suite, but G3's probe produced exactly the unprompted critical account F is for |

---

## 6. Consequences for the design

The run **changes the design's priorities** and one of its claims:

1. **G1 is promoted to the top of the list.** It is no longer an inferred gap
   from reading code — it is a reproduced corruption, with the same value
   accepted and refused by two paths into one tool, minutes apart.
2. **The design's §1 correction is confirmed empirically.** `set_property`
   validates, explains itself, and leaves the file untouched on refusal. Any
   remaining work extends that layer; it does not introduce it.
3. **D-08 already passes on the validated path**, which is the strongest
   available evidence that the "refusal text as the agent's only input" model
   works in practice, on a small fast model, unaided. The remaining gaps should
   be closed by making the other doors refuse *in the same words*.
