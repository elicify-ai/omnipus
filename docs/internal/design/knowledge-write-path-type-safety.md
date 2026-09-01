# Deterministic type safety in the knowledge write path

**Status:** design, pre-implementation
**Problem owner:** the founder — *"I do not want the agents to be experts in how
to build a vault; the tools should deterministically maintain the vault and
types properly without the agent thinking about it."*

---

## 1. The defect, stated as a fact rather than a worry

`records.Validate` — the type checker this product already owns — has four
callers in the whole codebase. Three are in `knowledge_configure` (guarding
schema CHANGES). One is in the vault importer (guarding an IMPORT). The tools
that write notes have **none**.

Verified by inspection of every write-path file — `authoring_tools.go`,
`knowledge_edit.go`, `lifecycle.go`, `author.go` — none of which contains a
single schema lookup.

The consequence is concrete. `knowledge_set_property` takes two free-form
strings, a property `name` and a `value`, with no record type and no schema
consultation. So today an agent may:

- write a value outside a declared enum — **accepted**;
- misspell a property name, silently creating a new undeclared property —
  **accepted**;
- put prose in a date field — **accepted**.

Nothing tells the agent. Nothing tells the operator until the next import
reports it. The schema is enforced everywhere except the one path that writes.

### 1.1 What is already right, and must not be broken

Three things already work and are load-bearing:

1. **`knowledge_create` starts from the collection's own template**, so a new
   note arrives with the frontmatter the collection expects instead of blank.
2. **Agents never write YAML by hand** — the tools do. This already removes a
   whole class of corruption and is why the problem is limited to *values*.
3. **`knowledge_configure` validates before and after a schema change**, so a
   schema edit cannot silently orphan existing notes.

The discipline exists. It is simply not wired to the path agents use most.

---

## 2. The principle

> **The tool holds the schema knowledge. The agent holds none.**

An agent should need to know nothing about this vault's types. It attempts a
write; the tool either performs it or refuses with everything required to
proceed. The agent's only competence is reading a refusal.

This yields the determinism the founder asked for: the outcome is a pure
function of `(schema, property, value)`. No model judgement enters the
decision, so the same write behaves identically on every run, for every agent,
on every model.

---

## 3. Rulings, by violation class

Each class gets one ruling. The refusal text is part of the contract, not
decoration — an agent that cannot proceed from the message has been failed by
the tool even if the tool was technically right.

| # | the agent wrote | ruling | the refusal must carry |
|---|---|---|---|
| 1 | a property the type does not declare | **refuse** | the nearest declared name within one edit, when one exists |
| 2 | a value that does not parse as the declared type | **refuse** | the expected type **and a valid example** |
| 3 | a value outside a declared enum | **refuse** | the complete list of legal values |
| 4 | a single value into a list property, or vice versa | **refuse** | which arity is expected |
| 5 | a property on a note whose record type is unknown | **allow**, and say so | that no schema governed this write |

**Class 1 reuses machinery this repo already has.** The importer's
`nearestWithinOneEdit` is exactly the typo check needed, already written and
already tested; the write path should call it rather than grow a second copy.

**Class 5 is deliberately permissive.** Untyped notes are a legitimate state
in this product — type inference for untyped notes exists as a feature. A write
to a note with no governing schema cannot be checked against one, and refusing
it would break a supported workflow. Saying so in the result is what keeps it
honest.

---

## 4. What this design refuses to do

These are decisions, not omissions.

- **Never silently coerce.** Turning `"12,500"` into `12500` guesses at intent
  and is unrecoverable once written. Refuse and let the caller be explicit.
- **Never auto-widen the schema as a side effect of writing a note.** A schema
  change is a deliberate, audited act via `knowledge_configure`. If writing an
  unknown enum value quietly extended the enum, the enum would stop meaning
  anything within a week and class 3 would protect nothing.
- **Never block the repair of an already-invalid note.** The founder's vault
  has 27 invalid records today. An agent must be able to fix them. Validation
  therefore judges **the value being written**, not the whole record — a note
  may remain invalid elsewhere and the write still succeeds.
- **Never claim semantic correctness.** This stops `status: sent` on a type
  that has no such status. It cannot stop `status: draft` on something that
  should have been `final`. Type safety is not judgement, and the refusal
  messages must not imply otherwise.

---

## 5. Interaction with what just shipped

The vault import now reads a base file's `Sum` as evidence a property holds a
number, so `invoice.amount` is `decimal` rather than `text`. Under this design
an agent writing `amount: PLACEHOLDER — amount unknown` to an invoice would be
**refused** where today it is accepted.

That is the correct outcome and it is also a real behaviour change for the
founder's demonstrated house style. The refusal must therefore be good enough
to be useful on its own: it has to name the expected type, show an example, and
name the `knowledge_configure` call that would make the property text if that
is what he actually wants. Class 2's "and a valid example" requirement exists
because of this case.

The same applies to the 26-value `status` enums the adoption rule now writes
onto empty record types: an agent writing a sensible-but-absent status is
refused, and the refusal listing all legal values is what lets it recover.

---

## 6. Acceptance

The change is accepted when an agent **that has never been told this vault's
schema** can be handed a task, hit a refusal, and complete the task correctly
using only the refusal text — no schema lookup, no human help, no guessing.

That is scenario **D-08** in `docs/internal/uat/knowledge-tools-uat-plan.md`,
and it is the criterion that matters. The other scenarios check that the guard
fires; D-08 checks that it achieves its purpose. A guard that refuses correctly
but leaves the agent stuck has relocated the problem, not solved it.

---

## 7. Open — settled by implementation recon

- **The choke point.** Whether all writes funnel through one shared apply/save
  function (one insertion point) or each tool writes independently (a check per
  tool, with a shared helper). This decides the shape of the change and is the
  single most important unknown.
- **Schema access.** Whether the authoring tools' dependency struct already
  carries the loaded schema set, or must be extended to.
- **Per-value entry point.** Whether `records` exposes single-property
  validation, or only whole-record — §4's "judge the value, not the record"
  ruling needs the former, and may require adding it.
