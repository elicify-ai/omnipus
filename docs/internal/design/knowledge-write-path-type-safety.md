# Deterministic type safety in the knowledge write path

**Status:** design, pre-implementation
**Problem owner:** the founder — *"I do not want the agents to be experts in how
to build a vault; the tools should deterministically maintain the vault and
types properly without the agent thinking about it."*

---

## 1. The defect — corrected after recon

**An earlier draft of this document was wrong, and the correction matters more
than the original claim.** It said the write tools perform no schema validation
at all. That conclusion came from grepping for `records.Validate` and finding
no caller on the write path. The live write tool does not use
`records.Validate`; it uses `records.ParseValue`, which the grep never looked
for.

### 1.1 What is actually already enforced

`pkg/knowledge/knowledge_edit_schema.go` is a working schema layer for
`knowledge_edit`, the only registered note-mutating tool. On `set_property` and
on `link`, `knowledgeEditValidateValue` checks, in order:

1. the property is **declared on that record type** — else `ErrUnknownProperty`,
   which already lists the type's declared property names;
2. **arity** matches (`isList == prop.Many`) — else `ErrPropertyArity`, whose
   text names the schema file and how to set `many: true`;
3. each element through **`records.ParseValue`** — else `ErrPropertyValue`,
   quoting the expected shape and the permitted values.

Validation is composed **into the `NoteEdit` closure**, so a refusal leaves the
file's bytes untouched. `knowledge_edit_list.go::SetPropertyScalarChecked`
additionally refuses a scalar write over an existing sequence, and
`author.go::authorValidatePropertyKey` rejects malformed keys.

So the headline worry — an agent silently writing an out-of-enum value through
the normal path — **does not happen today.** The tool refuses it.

### 1.2 Why the original worry still had a real referent

The tools that genuinely validate nothing — `knowledge_set_property`,
`knowledge_create`, `knowledge_link` and four others in
`pkg/knowledge/authoring_tools.go` — are **retired and unregistered**. No agent
can call them. `SetPropertyTool` does call raw `author.go::SetProperty` with no
schema at all, which is exactly the free-form write the earlier draft
described; it simply is not reachable.

### 1.3 The five gaps that ARE real

| # | gap | why it matters |
|---|---|---|
| **G1** | `op: create` validates only its `frontmatter` **map argument**. Raw `body` and expanded `template` bytes go to `CreateNote` unchecked. | A `body` containing its own `---` block writes arbitrary, unvalidated frontmatter. This is the one true corruption vector on the live path. |
| **G2** | `knowledge_restructure` rename/move rewrites wikilinks **inside frontmatter** of every inbound note, by byte offset, via `journal.go::ApplyStep`, with no schema awareness. | A rename edits `relation`/`person` values with no check that the result still conforms. |
| **G3** | An unknown, absent or unparseable record type collapses to **total silence** — no name check, no type check, no arity check, and no statement that none were applied. | The agent cannot distinguish "validated and fine" from "nothing was checked". Three distinct misses produce one indistinguishable outcome. |
| **G4** | Every write path discards the `*SchemaLoadReport` (`set, _, err :=`). | A malformed schema file silently degrades its notes to unconstrained. The write succeeds, nothing is logged, and the type quietly stops meaning anything. |
| **G5** | The seven retired tools validate nothing and are one `reg.Register` away from being live. | A latent regression with no guard against it. |

**G3 and G4 are the same failure shape and it is this project's most expensive
one: a check that silently did not run looks exactly like a check that passed.**

### 1.4 What is already right, and must not be broken

1. **`knowledge_create` starts from the collection's own template**, so a new
   note arrives with the frontmatter the collection expects.
2. **Agents never write YAML by hand.**
3. **`knowledge_configure` validates before and after a schema change.**
4. **`knowledge_edit` already refuses bad values on `set_property` and `link`**
   — §1.1. The work below extends that layer; it does not introduce it.

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

**Class 1 reuses machinery this repo already has — but it has to be moved
first.** The importer's `nearestWithinOneEdit` (with `withinOneEdit` and
`oneSubstitutionApart`) is exactly the typo check needed and is already tested.
It is **not** callable from where it is needed, for two independent reasons,
both verified:

- all three functions are **unexported** in `pkg/vaultimport`; and
- **`pkg/vaultimport` imports `pkg/knowledge`** (`infer.go`, `run.go`,
  `scan.go`), so `pkg/knowledge` importing `pkg/vaultimport` to reach them
  would be an import cycle.

`pkg/records` has no nearest-name helper of its own, and both packages already
depend on it. The resolution is therefore to **relocate the three functions
into `pkg/records` and export the entry point**, with `pkg/vaultimport` calling
the moved version rather than keeping a copy. That is a pure move plus an
export — no behaviour change on the import side — and it leaves one
implementation of "did you mean" in the product instead of two that can drift.

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

## 7. Recon answers

- **The choke point.** `author.go::CreateNote` and `author.go::EditNote` are
  the only note create/mutate primitives, and `EditNote` applies its edits
  inside one lock between the read and the atomic write. **But three paths
  bypass them** — `journal.go::ApplyStep` (rename cascade, live), the importer's
  `typeinfer.go::writeTypeKey` (CLI only), and a dormant CAS writer with no
  production caller. "Check inside EditNote" therefore covers the agent tools
  and not the whole surface, which is why G2 is listed separately.
- **Schema access.** No dependency struct carries a `*records.SchemaSet`.
  Schemas are loaded **per call** from the collection root by
  `knowledge_edit.go::EditTool.loadSchemas`. There is no cache anywhere in the
  package, and 13 non-test `LoadSchemas` call sites.
- **Per-value entry point. It exists** — `records.ParseValue(p *Property, n
  Node) (TypedValue, *ValueError)`, which needs no `Record` at all and returns
  a `ValueError` carrying `Reason`, `Expected`, `Got`, `Permitted` and a
  `FindingCode`. §4's "judge the value, not the record" ruling is already
  satisfiable; nothing needs adding.
- **Typo relocation (§3).** Still required and unchanged: the helpers are
  unexported in `pkg/vaultimport`, and `vaultimport` already imports
  `knowledge`, so the move down to `pkg/records` is the only cycle-free route.
  Note this is now an IMPROVEMENT rather than a gap — `ErrUnknownProperty`
  already lists every declared property name; adding "did you mean" makes a
  long list actionable rather than merely complete.
