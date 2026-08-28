# Vault records — specification

- **Implements:** [ADR-068](../architecture/ADR-068-vault-records-typed-record-layer.md) revision 3
- **Builds on:** [ADR-067](../architecture/ADR-067-omnipus-knowledge-base-and-render-first-preview.md) — `pkg/knowledge` is **reused, never duplicated**
- **Date:** 2026-08-25
- **Branch:** `feat/library-improvements`
- **Status:** Draft revision 2, after round-2 review (BLOCK: 8 critical, 21 major)

---

## 0. What already exists, and is reused

Verified against the tree at time of writing. This specification adds **no second
implementation** of any of these.

| Surface | Lines | Reused for |
|---|---|---|
| `pkg/knowledge/scope.go` | 390 | workspace scoping (`Scope.WorkspaceID`, `.Roots`, `.Contains`, `.Truncated`) — FR-060 |
| `pkg/knowledge/author.go` | 1,183 | scalar splice only — FR-040. `SetProperty(key, value string)` takes strings and rejects line breaks; **there is no list or nested writer**, so FR-040a adds one |
| `pkg/knowledge/index.go` | 1,277 | the index gains **one** stored field for record properties — FR-020. Its mapping is closed (`Dynamic=false`) and `indexDoc` is a closed 5-field struct, so per-property fields are impossible |
| `pkg/knowledge/search.go` | 603 | result caps (`SearchDefaultTopN`=20, `SearchMaxTopN`=100) as the precedent for FR-063 |
| `pkg/knowledge/tools.go` | 1,135 | tool registration shape and rate limiter |
| `contracts/components/schemas/Knowledge*.yaml` | 13 files | the contract-first pattern FR-090 follows |

**Three corrections carried from ADR-068 revision 2, restated so this spec cannot
re-introduce them:**

1. There is **no SQLite**. ADR-067 uses bleve scorch and explicitly rejected SQLite (its A2).
2. Tool names contain **no dots** — `record_query`, not `vault.query`. A §7 invariant test
   asserts zero dots across builtin tool names.
3. **Boot does not abort** on a tool-policy coverage gap. `repairAndValidateToolPolicyCoverage`
   repairs to `deny` first and validates after, so a forgotten tool ships silently denied.

---

## 1. Existing codebase context

GitNexus was not consulted for this spec; the surfaces above were read directly. Recorded as a
gap: an impact analysis on `pkg/knowledge/index.go` before W2 would be prudent, since record
fields are added to a mapping that search already depends on.

| Symbol | Role | Note |
|---|---|---|
| `knowledge.Index` | **extended** | gains one stored field + an index-format version bump forcing rebuild. Blast radius: every existing index is rebuilt once |
| `knowledge.Scope` | **called** | unchanged; record tools resolve through it |
| `knowledge.author` splice | **called and extended** | scalar path reused unchanged; a list-valued splice is new work (FR-040a) |
| `config.RepairIncompleteToolPolicyCoverage` | **not called, but must be defeated** | FR-081 asserts zero *repaired* pairs, not zero gaps after repair |

---

## 2. User stories

### US-1 — Declare what a record is (P0)

An operator or an agent declares a record type once, in a file, and every record of that type
is then validated against it. Without this, nothing else in the spec can fail loudly, because
there is no statement of what "correct" means.

**Why P0:** every other story depends on a schema existing.

**Independent test:** write a schema, write a conforming record and a non-conforming one, and
confirm validation accepts one and names the fault in the other.

1. **Given** no schema exists, **When** a note declares `type: company`, **Then** the note is
   an ordinary note and no error is raised.
2. **Given** a `company` schema requiring `name`, **When** a record omits `name`, **Then**
   validation reports that record, the missing property, and that it is required.
3. **Given** a schema declaring `status` as an enum, **When** a record holds a value outside
   the declared set, **Then** validation reports the record, the offending value, and the
   permitted values.
4. **Given** a schema declaring `segment` as scalar, **When** a record holds a list, **Then**
   validation reports an arity violation naming the expected shape.
5. **Given** a schema file with no `schema_version`, **When** it is loaded, **Then** it is
   rejected and no records of that type are validated against it.

### US-2 — Ask a question and get a trustworthy answer (P0)

An agent filters and groups records and receives, in one response, both the records and an
account of anything the query could not include. It cannot receive a total without also
receiving the caveats attached to it.

**Why P0:** this is the requirement the whole ADR exists to serve.

**Independent test:** query a corpus containing deliberately malformed records; confirm the
response names them rather than silently omitting them.

1. **Given** a corpus where every record is valid, **When** a filter runs, **Then** matching
   records are returned and completeness is reported as true.
2. **Given** a corpus where some records hold a non-numeric value in a numeric property,
   **When** an aggregate runs over that property, **Then** the response reports incompleteness,
   names the offending records, and states the reason.
3. **Given** records in four currencies, **When** a total is requested, **Then** no combined
   total is returned; the currencies present are listed instead.
4. **Given** a filter naming a property that does not exist in the schema, **When** it runs,
   **Then** the query is rejected with the valid property names listed — it does not return
   zero records.
5. **Given** a candidate set exceeding the materialisation cap, **When** the query runs,
   **Then** it is refused with a narrowing instruction, and no partial answer is returned.
6. **Given** a query grouping by a multi-value property, **When** a record holds two values,
   **Then** that record appears under both groups.

### US-3 — Follow a relation without maintaining it (P0)

An agent asks a question that crosses from one record type to another, and the reverse
direction exists without anyone having written it down.

**Why P0:** this is the capability whose absence costs five manual steps in the incumbent.

**Independent test:** create two record types and a relation, and query the inverse without
ever writing an inverse property.

1. **Given** a deal related to a company, **When** the company's related deals are requested,
   **Then** they are returned, and no inverse property exists in any file.
2. **Given** a relation pointing at a note that does not exist, **When** validation runs,
   **Then** the dangling relation is reported with the offending record and target.
3. **Given** a relation pointing at a note that exists but is not the declared target type,
   **When** validation runs, **Then** it is reported as a type violation, not silently accepted.
4. **Given** a record referenced by a relation is renamed, **When** the inverse is queried
   again, **Then** the same records are returned.
5. **Given** a query requesting three relation hops, **When** it runs, **Then** it is refused
   with the hop limit stated.

### US-4 — Correct a record without damaging the file (P0)

An agent writes a property. Everything it was not asked to change is left exactly as it was.

**Why P0:** the vault is simultaneously a human's working notes. A writer that reformats
degrades it invisibly.

**Independent test:** write one property into a file rich in comments and unusual formatting;
diff the result.

1. **Given** a file with comments, blank lines and mixed quoting, **When** one property is
   written, **Then** the file is byte-identical outside the patched span.
2. **Given** a write whose value violates the schema, **When** it is attempted, **Then** it is
   rejected, the expected shape is named, and the file is unmodified.
3. **Given** a stale version token, **When** a write is attempted, **Then** it is refused and
   the refusal is audited.
4. **Given** a successful write, **When** it completes, **Then** an audit entry records agent,
   workspace, record and outcome.

### US-5 — Records stay findable when things move (P1)

Identity survives renames, and two records can never share an identity.

**Why P1:** important but only observable once relations exist.

1. **Given** a new record, **When** it is created, **Then** it receives an identifier unique
   within its type.
2. **Given** two processes creating records concurrently, **When** both complete, **Then** no
   two records share an identifier.
3. **Given** two records that somehow share an identifier, **When** validation runs, **Then**
   both paths are reported and neither is silently preferred.
4. **Given** the sequence file is deleted, **When** the vault is opened, **Then** allocation
   resumes above the highest existing identifier.

### US-6 — Interaction history nobody maintains (P1)

Contact history is derived from what is already written, not from a field somebody has to
remember to update.

1. **Given** a dated note mentioning a record, **When** history is requested, **Then** that
   date appears.
2. **Given** a mention inside an **unchecked** to-do, **When** history is requested, **Then**
   it does **not** count as contact.
3. **Given** a mention inside a **completed** task, **When** history is requested, **Then** it
   does count.
4. **Given** a mention inside an embed, a quote, or a code block, **When** history is
   requested, **Then** it does not count.

### US-7 — Bring existing base files across (P2)

A one-shot importer translates what it recognises and names what it does not.

**Why P2:** valuable, but nothing else depends on it.

1. **Given** a `.base` file using only supported filters, **When** it is imported, **Then** an
   equivalent native view is produced.
2. **Given** a `.base` file containing an expression the importer does not support, **When** it
   is imported, **Then** the view is produced without that clause and the untranslated
   expression is reported verbatim.
3. **Given** a `.base` file referencing a property absent from any schema, **When** it is
   imported, **Then** the import is reported as incomplete and names the property.

### US-8 — Only the records I am entitled to (P0)

**Why P0:** ADR-067 was blocked on precisely this for a weaker primitive.

1. **Given** a vault mounted only into workspace B, **When** an agent in workspace A queries,
   **Then** zero records are returned.
2. **Given** the same, **When** the agent inspects the response, **Then** it cannot distinguish
   the scoping from the vault being empty.

### Edge cases

| Condition | Expected |
|---|---|
| Property absent entirely | distinct from any value; `is absent` matches it; a negative filter includes it unless excluded |
| Enum value differing only in case | rejected — the schema set is exact |
| Record type declared in two schema files | both rejected, both paths named |
| Relation to a record in another workspace's vault | invisible; treated as dangling within scope |
| Money property with amount but no currency | rejected at write |
| Vault is not a git repository | schemas still work; no version history is claimed |
| Windows, two processes allocating IDs | collision possible (flock is a no-op); healed by reconcile, reported by validation |

---

## 3. Behavioural contract

- When a query cannot include a record, the system **names that record and the reason**.
- When a total spans multiple currencies, the system **returns no total** and lists the
  currencies.
- When a filter names an unknown property or enum value, the system **rejects the query** and
  lists the valid names.
- When a bound is exceeded, the system **refuses** and states which bound and how to narrow.
- When a write violates the schema, the system **rejects it** and leaves the file untouched.
- When a write succeeds, the file is **byte-identical outside the patched span**.
- When a relation target is missing or mistyped, the system **reports it** at validation.
- When a record is out of workspace scope, the system returns **empty, not an error**.

### Explicit non-behaviours

- The system must **not** return a partial result set that looks complete — that is the defect
  this specification exists to remove.
- The system must **not** write derived values into frontmatter, because a reader cannot then
  distinguish a stale derived value from a fact.
- The system must **not** auto-create an enum value on write, because that is how one column
  comes to hold `Won`, `won` and `Closed Won`.
- The system must **not** re-serialise a file it was asked to make one edit to.
- The system must **not** convert between currencies, because no rate source is in scope
  (ADR-068 O-2).
- The system must **not** implement a text query language or parser (ADR-068 O-3).
- The system must **not** read `.base` files at query time — the importer is one-shot
  (ADR-068 O-1).
- The system must **not** treat an absent property as `false`.

### Machine-verifiable constraints

| Constraint | Value |
|---|---|
| Results per page | default 50, max 200; a clamp is reported in the response |
| Candidate set materialised | 10,000 records; beyond this the query is **refused** |
| Relation hops | 2; a third is refused |
| Supported records per vault | 50,000 records. **Note:** the index counts segments, not records, so this is an unknown larger document count; the segment ratio MUST be measured at W2 and recorded here |
| Peak RSS | inherited unchanged: ADR-067's < 64 MB steady state for this process. **No record-specific latency or memory target is stated** — revision 1's numbers came from a measurement of expression evaluation alone, and D16's spike establishes real ones |
| Rate limit | shared with ADR-067's knowledge limiter; 429 carries `Retry-After` |
| Money arithmetic | exact decimal; no binary floating point anywhere in the path |

---

## 4. Functional requirements

### Schema and types

- **FR-001** The system MUST load record-type schemas from `<vault>/.omnipus-vault/records/<type>.yaml`.
- **FR-002** The system MUST reject a schema without `schema_version`.
- **FR-003** The system MUST reject two schema files declaring the same record type, naming both paths.
- **FR-004** The system MUST support exactly these property types: `text`, `enum`, `relation`, `date`, `number`, `money`, `person`.
- **FR-005** The system MUST treat a note whose `type` matches no schema as an ordinary note, without error.
- **FR-006** Each property MUST declare arity, and the system MUST reject a value of the wrong arity.
- **FR-007** The system MUST distinguish an absent property from every value of that property.
- **FR-008** A negative filter MUST include records where the property is absent, unless the query excludes them explicitly.
- **FR-009** Property types MUST be scoped to their record type; the same name in two types is unrelated.
- **FR-010** An enum MUST declare its values in order, and sorting MUST follow declared position, not lexical order.
- **FR-011** The system MUST reject an enum value outside the declared set, listing the permitted values.
- **FR-012** A `money` value MUST carry amount, ISO-4217 currency and declared scale together; a value missing currency MUST be rejected.
- **FR-013** Money arithmetic MUST be exact decimal.
- **FR-014** The system MUST refuse to sum money across currencies and MUST list the currencies present.

- **FR-015** A change to a schema file MUST invalidate affected records and trigger revalidation. Schemas live under a directory the scanner does not walk, so no manifest entry or mtime exists for them; the system MUST track them explicitly rather than inheriting note-scanning behaviour.

### Index and query

- **FR-020** *(BLOCKED on ADR-068 D16's spike — not specifiable until S-1/S-2/S-3 report.)* Record properties MUST be persisted and retrievable such that a query can select candidates by record type without retrieving every document in the index.
- **FR-020a** *(BLOCKED — same.)* An index created before record support exists MUST NOT be opened and queried for properties it cannot hold. A silent no-op returning `complete: true` over zero properties MUST be impossible.
- **FR-020b** Money MUST never be converted to a binary floating-point number anywhere in the storage or retrieval path. *(This requirement survives the spike; it constrains whichever design is chosen.)*
- **FR-021** Filtering, grouping and aggregation MUST be evaluated in Go over the retrieved candidate set.
- **FR-022** `record_query` MUST accept a structured filter object; the system MUST NOT accept a text query language.
- **FR-023** Every property name, enum value and relation target in a query MUST be validated against the schema **before** evaluation.
- **FR-024** A query naming an unknown property or enum value MUST be rejected with valid names listed, and MUST NOT return an empty result set. **Scope MUST be resolved before this rejection**, so the valid-names list never reveals schemas outside the caller's workspace — otherwise the error channel defeats FR-062.
- **FR-025** Every query response MUST carry a completeness verdict and a problem list as **required** fields.
- **FR-026** A record excluded from an aggregate MUST be named in the problem list with the reason.
- **FR-027** Grouping MUST support two levels.
- **FR-028** Grouping by a multi-value property MUST place a record in every group it belongs to.
- **FR-029** Grouping by a relation MUST be supported.

### Relations and identity

- **FR-030** A relation MUST be stored on disk as a quoted wikilink.
- **FR-031** The index MUST resolve a relation to the target's record identifier.
- **FR-032** The inverse of a relation MUST be derived and MUST NOT be stored in any file.
- **FR-033** A relation whose target does not exist MUST be reported by validation.
- **FR-034** A relation whose target is not of the declared type MUST be reported by validation.
- **FR-035** Relation cardinality MUST be declared and enforced.
- **FR-036** Every record MUST carry an identifier unique within its type, minted on creation.
- **FR-037** Identifier allocation MUST be mutually exclusive within a process and, on POSIX, across processes.
- **FR-038** On open, the allocator MUST raise its counter to at least the highest existing identifier and MUST NEVER lower it. Reconciling *to* the maximum guarantees reuse after the highest record is deleted, which makes an existing relation resolve to a different record.
- **FR-039** A duplicate identifier MUST be a hard validation error naming both paths.

### Writes

- **FR-040** A write MUST reuse `pkg/knowledge/author.go`'s splice for scalar values; the system MUST NOT re-serialise the document.
- **FR-040a** The system MUST add a list-valued splice, preserving the source's existing list style. `SetProperty` is scalar-only and cannot satisfy FR-045 or `many: true`.
- **FR-040b** A scalar write targeting a key whose current value spans multiple lines MUST be refused and the file left unmodified, because the existing key-splice removes continuation lines and would silently delete the value.
- **FR-041** A write MUST leave the file byte-identical outside the patched span.
- **FR-042** A write violating the schema MUST be rejected with the expected shape named, leaving the file unmodified.
- **FR-043** A write MUST carry ADR-067 D14's version token; a stale token MUST be refused and the refusal audited.
- **FR-044** Every mutating record tool MUST emit an audit entry per ADR-067 D19.
- **FR-045** Relations MUST be modified through distinct add, remove and replace operations; replace MUST be named explicitly.
- **FR-046** Derived values MUST NOT be written into frontmatter.

### Interaction history

- **FR-050** A mention of a record in a dated note MUST be treated as an interaction.
- **FR-051** A mention inside an unchecked to-do MUST NOT count.
- **FR-052** A mention inside a completed task MUST count.
- **FR-053** A mention inside an embed, quote or code block MUST NOT count.

### Scope and bounds

- **FR-060** Every record tool MUST resolve through the calling agent's workspace scope.
- **FR-061** An out-of-scope record MUST yield an empty result, never a permission error.
- **FR-062** The scoped-out case MUST be indistinguishable from an empty vault.
- **FR-062a** When scope resolution is itself incomplete — ADR-067's `Scope.Truncated()` — the query MUST report incompleteness with that reason. A query that could not resolve every mount MUST NOT report success, or a whole mounted folder can go missing while the answer claims to be complete.
- **FR-063** Page size MUST default to 50 and cap at 200; a clamp MUST be reported.
- **FR-064** A candidate set exceeding 10,000 records MUST be refused with a narrowing instruction.
- **FR-065** A query requesting more than two relation hops MUST be refused.
- **FR-066** No aggregate MUST be returned over a refused candidate set.

### Tools, policy, contracts

- **FR-070** **MEANING CHANGED, revision 3 (ADR-068 D15).** The system MUST expose exactly **five** tools: `vault_describe`, `vault_find`, `vault_read`, `vault_edit`, `vault_restructure`. *Previously: nine `record_*` tools. Cited by §6 traceability, §7 test 18 and SC-011; all three are restated below. The nine `record_*` names were never implemented, so nothing but this document referenced them.*
- **FR-070a** The five tools MUST also **replace** the nine shipping `knowledge_*` tools — `knowledge_search`, `knowledge_graph`, `knowledge_tasks`, `knowledge_create`, `knowledge_link`, `knowledge_set_property`, `knowledge_append_section`, `knowledge_move`, `knowledge_rename` (`pkg/coreagent/core.go:475-482`). After W5 no `knowledge_*` name remains in `allStaticToolNames` (`pkg/coreagent/core.go:357`), in the global ceiling (`pkg/config/defaults.go:637-646`), or in any seeded per-agent map.
- **FR-070b** **The tool boundary is the policy boundary.** `vault_edit` MUST write **only** the file named in its `path` argument. `vault_restructure` is the only tool permitted to change a file the caller did not name. A reviewer MUST be able to decide which tool an operation belongs to by answering one question: *does it touch only the named file, or does it cascade?* Additive-versus-destructive is explicitly **not** the criterion — `set_property` overwrites and stays in `vault_edit`.
- **FR-070c** An operation enum **within** one tool is permitted and carries no policy meaning. Policy resolves on the tool **name** only — `resolveToolPolicyAtExec(ts *turnState, toolName string, …) string` (`pkg/agent/loop.go:12418`) takes no arguments and no operation discriminator — so every operation reachable from one tool MUST be equally acceptable to an operator who granted that tool.
- **FR-071** Tool names MUST contain no dots. *(Audit event names are not tool names: FR-077's `vault.edit` / `vault.restructure` carry a dot deliberately and are out of this requirement's scope.)*
- **FR-072** **MEANING WIDENED, revision 3 (ADR-068 D22.1).** **Every** result from **all five** tools MUST be rendered to the model as compact text, never as a JSON document. *Previously: `record_schema` alone. Cited by §6 and §7 test list.*
- **FR-073** **MEANING CHANGED, revision 3 (ADR-068 D15.3).** `explain` MUST be a **boolean argument of `vault_find`**, not a tool. With `explain: true` the system MUST report what the query would select, which properties it could not evaluate and why, and MUST NOT evaluate the query. *Previously: a `record_explain` tool. Cited by §6 and §7.*
- **FR-074** `vault_read` MUST return the note's **version token** in every successful response. Obtaining a token MUST NOT require sending a write that is expected to fail. *(Today the only source of a token is `*ConflictError` from a refused `EditNote` — `pkg/knowledge/author.go:701`; `EditNoteRequest.ExpectVersion` (`author.go:566`) refuses an empty token too.)*
- **FR-075** `vault_describe` MUST accept an argument-free whole-vault integrity sweep (`check_integrity: true`) reporting duplicate identifiers (FR-039), unresolvable and mistyped relations (FR-033, FR-034) and orphans, and MUST name every offending path.
- **FR-076** `vault_find` MUST accept `near` (a note) with `hops` (1..2) and MUST **compose** it with `words`, `type` and `filter` in a single call. A `near` query MUST NOT bypass, weaken or replace any filter supplied alongside it.
- **FR-077** Every mutating call MUST emit an audit entry named for its tool — `vault.edit` or `vault.restructure` — carrying the operation, agent, workspace, path and outcome. The operation appears in the audit record because it is known **after** the call; it MUST NOT be presented anywhere as a policy lever, because FR-070c establishes it is not one.
- **FR-078** The **catalog size** MUST fall: after W5 the static builtin catalog contains five `vault_*` names and zero `knowledge_*` names, taking `allStaticToolNames` from **102** entries to **98**.
- **FR-079** Each of the five tool **descriptions** MUST fit a budget of ~150 tokens. Operation detail belongs in **parameter descriptions and error messages**, which are paid only when used; a tool description is paid on every turn by every agent that holds the tool, whether or not it is called.
- **FR-080** Every record tool MUST have an explicit, literal, wildcard-free policy entry for **every** seeded agent.
- **FR-081** A test MUST assert **zero repaired pairs** on a fresh install — not zero gaps after repair.
- **FR-082** The global tool-policy ceiling for every record tool MUST be stated explicitly in the seed. Repair backfills a *missing agent entry* to `deny`; what can silently grant is the **global ceiling**, which the seed sets per tool. Revision 1's rationale ("absence grants in a sparse map") named the wrong mechanism.
- **FR-090** Every wire type MUST be defined in `contracts/` before Go or TS code exists.
- **FR-091** The completeness verdict and problem list MUST be required fields in the response schema.

### Import

- **FR-100** `record_view_import` MUST translate the filters, order and grouping it recognises from a `.base` file into a native view.
- **FR-101** An expression it cannot translate MUST be reported verbatim, and MUST NOT be approximated or silently dropped.
- **FR-102** The importer MUST be one-shot; `.base` files MUST NOT be read on the query path.

---

## 4.1 Tool specifications — normative

The five tools below are the **whole** agent-facing surface of the vault. Parameters are
normative: a name not listed here does not exist, and an argument the schema does not declare is
rejected with the accepted argument names listed (the same posture FR-024 takes for an unknown
property).

Every response is compact text (FR-072). Every response begins with its completeness verdict
(FR-121) and ends with next actions (FR-125). Errors are returned as a **refusal the model can
act on**, never as an empty success.

### 4.1.1 `vault_describe` — READ, orientation and integrity

The mandatory cheap first call. An agent that has not called it is guessing at property names,
and a guessed property name is the failure FR-024 exists to prevent.

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `collection` | string | all in scope | Narrow to one collection. Unknown name → refusal listing the collections in scope. |
| `record_type` | string | — | Return the full property table for one type only. |
| `include` | list of `types \| views \| templates \| index` | all | Trim the response. |
| `check_integrity` | bool | `false` | Run the whole-vault sweep (FR-075). |
| `detail` | `minimal \| standard` | `standard` | `minimal` omits property descriptions and enum value lists. |

**Response sections, in order:** index freshness → collections → record types (name, label, id
prefix, property table: name, type, arity, required, enum values in declared order) → saved
views → templates → integrity findings when requested.

**Integrity findings** are grouped by kind and every finding names a path:

```
INTEGRITY: 3 findings
  duplicate id   CO-0142 — Companies/Acme.md and Companies/Acme (old).md; neither is preferred
  unresolved     DEAL-0091 company → [[Acme Corp.]] — no note resolves; nearest: Companies/Acme Ltd.md
  wrong type     DEAL-0104 company → [[Q3 planning]] is a note of type meeting, expected company
```

- **AC-D1** — `check_integrity` on a vault with a duplicate identifier names **both** paths and
  states that neither is preferred (FR-039, ADR-068 D7.1).
- **AC-D2** — `vault_describe` with an unknown `record_type` is refused with the declared type
  names listed; it does not return an empty description.
- **AC-D3** — the response never contains a JSON object (FR-072).

### 4.1.2 `vault_find` — READ, the one retrieval path

Absorbs `record_query`, `record_explain`, `knowledge_search`, `knowledge_tasks` and
link-neighbourhood traversal. There is **no second retrieval tool**.

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `words` | string | — | Free text, ranked per FR-112. |
| `type` | string | — | Record type. Unknown → refusal listing declared types. |
| `kind` | `note \| record \| task \| attachment` | `note` | `task` replaces `knowledge_tasks`. |
| `filter` | object | — | Structured only (FR-022). Shape below. |
| `view` | string | — | A saved view, applied first; `filter` refines it. |
| `near` | string | — | A note path or wikilink. |
| `hops` | int 1..2 | `1` | Only with `near`; 3+ is refused (FR-065). |
| `join` | list of relation names | — | Borrowed columns (FR-124). |
| `group_by` | list, max 2 | — | Two levels (FR-027). |
| `sort` | list of `{property, direction}` | relevance | Enum sorts by declared position (FR-010). |
| `select` | list of property names | schema order | Columns to render. |
| `aggregate` | list of `{op, property}`; `op` ∈ `count, sum, min, max` | — | Scoped totals only (FR-123). |
| `explain` | bool | `false` | Report the plan; evaluate nothing (FR-073). |
| `limit` | int | `50` | Clamped at 200, clamp reported (FR-063). |
| `cursor` | opaque string | — | An unhonourable cursor is an error, never a silent restart. |
| `detail` | `minimal \| standard` | `standard` | `minimal` ≈ 20 tokens/hit (FR-126). |

**Filter shape** — a tree of `{all: [...]}`, `{any: [...]}`, `{not: {...}}` over leaves
`{property, op, value}`, with `op` ∈ `is, is_not, lt, lte, gt, gte, contains, is_absent,
is_present`. No text query language exists and none is accepted (FR-022).

**Normative refusal wording.** These strings are contract, not illustration; a test asserts them.

| Condition | Message |
|---|---|
| Unknown property | `unknown property 'ownr' on record type 'company'; declared: name, status, segment, owner, website, arr` |
| Unknown enum value | `'Won' is not a value of deal.status; permitted, in order: open, won, lost` |
| Equality on a `many` property (R-13) | `segment holds many values; use contains` |
| Third hop | `hops=3 exceeds the limit of 2; run a second vault_find from one of these results` |
| Candidate cap | `this query selects 24,180 records; the limit is 10,000 — add a filter on status (7 values) or a narrower type` |
| Aggregate over a refused set | `no total is returned over a refused candidate set` |
| Cross-currency total | `2 currencies present (GBP, USD); no combined total is returned` |
| Stale cursor | `that cursor was issued against index generation 8802; the index is now at 8814 — re-run the query` |

- **AC-F1** — every refusal above names the remedy in the same string. A refusal that states only
  what went wrong fails this criterion.
- **AC-F2** — `near` composed with `words` and `filter` returns the intersection. A test asserts a
  record inside the hop radius but failing the filter is **absent**, and one matching the filter
  but outside the radius is **absent** (FR-076).
- **AC-F3** — `explain: true` performs no evaluation: a corpus mutation between two identical
  `explain` calls changes nothing in the response except index generation.
- **AC-F4** — an out-of-scope vault yields `COMPLETE: yes — 0 records` and no other signal
  (FR-062).

### 4.1.3 `vault_read` — READ, a note or one section of one

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `path` | string | required | Note path within scope. |
| `section` | string | — | One heading. Unknown → refusal listing the headings present. |
| `include` | list of `frontmatter \| body \| links \| backlinks` | all | |
| `max_bytes` | int | 40,000 | Truncation is reported in the header, never silent. |

**Response:** version token first, then typed frontmatter (parsed per the record's schema, with
any non-conforming value flagged in place), then body or section, then links and backlinks
inline.

Refusals: `no section '## Pricing' in Deals/Acme.md; headings: ## Summary, ## Terms, ## Notes`.

- **AC-R1** — a successful `vault_read` carries a version token that `vault_edit` accepts
  unchanged (FR-074).
- **AC-R2** — no path exists by which an agent must send a failing write to obtain a token: a test
  drives read → edit with zero intervening failed writes.
- **AC-R3** — a note whose frontmatter violates its schema still **reads**, with the violation
  named per property. Reading is never blocked by a validation finding.

### 4.1.4 `vault_edit` — WRITE, one named file

Writes **only** `path` (FR-070b). Every op on an existing file requires `expect_version`.

| `op` | Additional parameters | Notes |
|---|---|---|
| `create` | `path`, `frontmatter` (map), `body` | Mints the identifier (FR-036) when `type` is a declared record type. |
| `set_property` | `path`, `property`, `value` (scalar **or list**), `expect_version` | Closes today's scalar-only limit (`SetProperty(key, value string)`, `pkg/knowledge/author.go:766`). |
| `append_section` | `path`, `heading`, `body`, `level` (default 2), `once` (bool), `expect_version` | Maps to `AppendSectionAt` / `AppendSectionOnce`. |
| `replace_body` | `path`, `anchor` **or** `line_range`, `body`, `expect_version` | **Unbuilt primitive** — FR-047. |
| `link` | `path`, `target`, `alias`, `section`, `relation` (property name), `expect_version` | Writes the **source** only; the inverse is derived (FR-032). |
| `write_view` | `view` (name), `definition`, `expect_version` when replacing | A view file changes no note (FR-018). |
| `create_record_type` | `type`, `definition` | **New** type only; nothing existing is reinterpreted (FR-016). |

`link` belongs to this tier because a relation is stored once, on the source (FR-030). Linking
`Deal.md` to `[[Acme]]` writes `Deal.md` and never touches `Acme.md` — it looks like a two-file
operation and is a one-file operation.

**Normative refusal wording:**

| Condition | Message |
|---|---|
| Stale token (FR-043) | `Deals/Acme.md changed since you read it; you have v1:ab12…, current is v1:cd34… — vault_read it again and re-apply` |
| Multi-line clobber (FR-040b) | `segment currently spans 3 lines; a scalar write would delete them — no change made. Send a list value instead` |
| Ambiguous anchor (FR-047) | `anchor "## Pricing" appears twice in Deals/Acme.md (lines 14 and 88) — no change made; give a unique anchor or a line_range` |
| Schema violation (FR-042) | `deal.status holds one value; got a list of 2 — send a single value, or declare many: true in .omnipus-vault/records/deal.yaml` |
| Cascading op sent here | `rename cascades to notes you did not name; use vault_restructure` |

- **AC-E1** — after any successful `vault_edit`, exactly one file has a changed mtime and hash.
  This is FR-070b as a test, and it is the tier's definition.
- **AC-E2** — `create_record_type` on a type that already exists is refused, naming
  `vault_restructure` as the tool for changing one (FR-017).
- **AC-E3** — an ambiguous `replace_body` anchor leaves the file byte-identical.

### 4.1.5 `vault_restructure` — WRITE, cascades

The only tool permitted to change a file the caller did not name.

| `op` | Additional parameters | Cascade |
|---|---|---|
| `rename` | `path`, `new_name`, `expect_version` | Inbound wikilinks in N notes are rewritten (ADR-067 D10). |
| `move` | `path`, `dest`, `expect_version` | Same. |
| `trash` | `path`, `expect_version` | Inbound links **cannot** be repaired — FR-048. |
| `edit_record_type` | `type`, `definition`, `expect_version` | Every existing record of that type is reinterpreted (FR-015, FR-017). |
| `delete_record_type` | `type`, `expect_version` | Same cascade, larger. |

**Trash stays in this tier even though it is a soft delete.** A recoverable bin fixes the trashed
note's own recoverability; it does nothing for the N notes whose links just broke. Recoverability
and blast radius are different axes, and only the second one decides the tier.

Every response MUST state the cascade in counts before the next-actions block:

```
CASCADE: 7 notes rewritten (inbound wikilinks), 1 note moved
```

- **AC-X1** — a `trash` response names the count of now-unrepairable inbound links and lists
  the linking notes up to the page limit (FR-048).
- **AC-X2** — `edit_record_type` reports how many existing records change validity as a result,
  in both directions (newly invalid, newly valid).
- **AC-X3** — an operator policy of `vault_edit: allow` + `vault_restructure: deny` permits
  `set_property` and refuses `rename` in the same session (FR-083).

---

## 5. Success criteria

- **SC-001** The two-hop question from ADR-068 §1.2 is answered by one `record_query` call with no hand-maintained state and no regular expression.
- **SC-002** For a corpus of 63 records where 22 hold non-numeric values in a numeric property, an aggregate names all 22 and returns no combined figure.
- **SC-003** A query filtering on a mistyped property name is rejected with valid names listed; zero such queries return an empty result set.
- **SC-004** Writing one property into a 200-line note leaves the file byte-identical outside the patched span, across a 50-file fixture corpus.
- **SC-005** 1,000 records created concurrently across two POSIX processes yield 1,000 distinct identifiers and zero sequence gaps.
- **SC-006** An agent in workspace A retrieves zero records from a vault mounted only into workspace B.
- **SC-007** *(pending D16's spike — no numeric target is stated until it reports.)*
- **SC-009** Zero tool-policy pairs are repaired on a fresh install.
- **SC-010** A `.base` file containing one unsupported expression imports with that expression reported verbatim and the rest translated.

---

## 6. Traceability

| FR | Story | Scenario | Test |
|---|---|---|---|
| FR-001..003 | US-1 | 1.5 | `TestSchema_LoadAndReject` |
| FR-004, 009 | US-1 | 1.2 | `TestSchema_TypesAreScopedToRecordType` |
| FR-006 | US-1 | 1.4 | `TestValidate_ArityViolationIsReported` |
| FR-007, 008 | US-2 | edge | `TestFilter_AbsentIsDistinctAndIncludedByNegation` |
| FR-010, 011 | US-1 | 1.3 | `TestEnum_OrderedAndClosed` |
| FR-012..014 | US-2 | 2.3 | `TestMoney_RefusesCrossCurrencySum` |
| FR-020, 021 | — | — | `TestIndex_PropsFieldRoundTripsExactDecimal` — asserts a money value survives the index unchanged; a float64 path fails it |
| FR-022..024 | US-2 | 2.4 | `TestQuery_UnknownPropertyIsRejectedNotEmpty` |
| FR-025, 026 | US-2 | 2.2 | `TestQuery_ProblemsAreNamedNotDropped` |
| FR-027..029 | US-2 | 2.6 | `TestGroup_MultiValueAppearsInEveryGroup` |
| FR-030..035 | US-3 | 3.1–3.4 | `TestRelation_InverseIsDerivedAndSurvivesRename` |
| FR-036..039 | US-5 | 5.1–5.4 | `TestID_ConcurrentAllocationIsCollisionFree` |
| FR-040..042 | US-4 | 4.1, 4.2 | `TestWrite_ByteIdenticalOutsidePatchedSpan` |
| FR-043, 044 | US-4 | 4.3, 4.4 | `TestWrite_StaleTokenRefusedAndAudited` |
| FR-045 | US-4 | — | `TestRelate_ReplaceMustBeNamed` |
| FR-046 | — | — | `TestDerived_NeverWrittenToFrontmatter` |
| FR-050..053 | US-6 | 6.1–6.4 | `TestInteraction_ExclusionRules` |
| FR-060..062 | US-8 | 8.1, 8.2 | `TestScope_CrossWorkspaceReturnsEmpty` |
| FR-063..066 | US-2 | 2.5 | `TestBounds_RefusalNotTruncation` |
| FR-070..073 | — | — | `TestTools_NamesHaveNoDots` |
| FR-080..082 | — | — | `TestToolPolicy_ZeroRepairedPairsOnFreshInstall` |
| FR-090, 091 | — | — | `TestContract_CompletenessFieldsAreRequired` |
| FR-100..102 | US-7 | 7.1–7.3 | `TestImport_UntranslatedExpressionIsReported` |

---

## 7. Test plan

Order is unit → integration → e2e; within a level, dependencies first.

| # | Test | Level | Traces |
|---|---|---|---|
| 1 | `TestSchema_LoadAndReject` | unit | FR-001..003 |
| 2 | `TestEnum_OrderedAndClosed` | unit | FR-010, 011 |
| 3 | `TestValidate_ArityViolationIsReported` | unit | FR-006 |
| 4 | `TestMoney_RefusesCrossCurrencySum` | unit | FR-012..014 |
| 5 | `TestFilter_AbsentIsDistinctAndIncludedByNegation` | unit | FR-007, 008 |
| 6 | `TestComparisonTruthTable` | unit | **see §8** |
| 7 | `TestQuery_UnknownPropertyIsRejectedNotEmpty` | unit | FR-024 |
| 8 | `TestQuery_ProblemsAreNamedNotDropped` | integration | FR-025, 026 |
| 9 | `TestGroup_MultiValueAppearsInEveryGroup` | integration | FR-028 |
| 10 | `TestRelation_InverseIsDerivedAndSurvivesRename` | integration | FR-030..034 |
| 11 | `TestID_ConcurrentAllocationIsCollisionFree` | integration | FR-036..039 |
| 12 | `TestWrite_ByteIdenticalOutsidePatchedSpan` | integration | FR-040, 041 |
| 13 | `TestWrite_StaleTokenRefusedAndAudited` | integration | FR-043, 044 |
| 14 | `TestInteraction_ExclusionRules` | integration | FR-050..053 |
| 15 | `TestScope_CrossWorkspaceReturnsEmpty` | integration | FR-060..062 |
| 16 | `TestBounds_RefusalNotTruncation` | integration | FR-063..066 |
| 17 | `TestToolPolicy_ZeroRepairedPairsOnFreshInstall` | integration | FR-081 |
| 18 | `TestTools_RecordToolsRegisteredAndDotFree` | unit | FR-070, 071 — all **nine** including `record_view_import` — asserts all **nine** names are actually registered, then that none contains a dot. The name-only check duplicates an existing green assertion over 35 tools and passes today with zero record tools |
| 19 | `TestContract_CompletenessFieldsAreRequired` | unit | FR-091 |
| 20 | `TestImport_UntranslatedExpressionIsReported` | integration | FR-100..102 |
| 21 | `TestRecords_PerfAtFiftyThousand` | e2e | SC-007, SC-008 |

### Test datasets

**DS-1 — property values.** Traces to FR-006, 007, 011, 012.

| Value | Property type | Expected | Traces |
|---|---|---|---|
| `active` | enum(prospect,active) | accepted | 1.3 |
| `Active` | enum(prospect,active) | **rejected** — case-exact | edge |
| absent | enum | absent, not a value | 1.4 |
| `[a, b]` | enum scalar | **rejected** — arity | 1.4 |
| `""` | text | accepted, distinct from absent | edge |
| `349.98` + `SGD` | money | accepted | 2.3 |
| `349.98`, no currency | money | **rejected** | edge |
| `PLACEHOLDER — unknown` | number | **rejected**, record named | 2.2 |
| `2026-13-45` | date | rejected | edge |
| 2^53+1 | number | accepted, exact | edge |

**DS-2 — relation targets.** Traces to FR-030..034.

| Target | Expected |
|---|---|
| existing record, correct type | resolves |
| existing note, no `type` | reported: not a record |
| existing record, wrong type | reported: type violation |
| non-existent | reported: dangling |
| record in another workspace's vault | invisible; dangling within scope |
| ambiguous basename matching two notes | reported: ambiguous, both paths named |

**DS-3 — write corpus.** Traces to FR-041. 50 real notes including: leading comments, comments
between properties, blank lines inside frontmatter, single- and double-quoted scalars,
unquoted scalars, a multi-line block scalar, CRLF line endings, and
a file whose frontmatter is the entire file.

### Regression

The record layer is new capability, but **FR-020 modifies `pkg/knowledge/index.go`**, which
search already depends on. Existing `pkg/knowledge` tests MUST pass unchanged. Seam tests:

- `TestIndex_StaleFormatIsRebuiltNotOpened` — an index written at the previous format version
  is rebuilt, not opened. **This is the seam that matters**: `openOrCreateBleve` calls
  `bleve.OpenUsing` and never re-applies the mapping, so without the version bump an upgraded
  install would query a field that does not exist and report `complete: true`.
- `TestKnowledgeSearch_ScoringUnchangedByPropsField` — BM25 scores for a fixture corpus are
  identical before and after. (Note: the weaker "results unaffected" form **cannot fail**,
  because the new field is stored-not-analysed and every existing field sets
  `IncludeInAll=false`.)
- `TestIndex_RebuildWithRecordFields` — a rebuilt index returns identical ranked results.

---

## 8. The comparison oracle — the highest-risk item, specified

ADR-068 §4.2 names the risk: to filter over real frontmatter, comparison operators must accept
`any`, and the expression engine's type checker then stops protecting them. During research a
first-attempt overload made `3 > 2` evaluate to **false**, with nothing reporting an error.

`TestComparisonTruthTable` is a first-class deliverable. Revision 1 demanded it be written
"from the specification" while the specification defined roughly **three of ~648 cells** — so
in practice the other 645 would have been read off the implementation, which is exactly the
failure the test exists to prevent.

**The oracle is these rules. Every cell is generated from them, and the rules — not the cells —
are what a human reviews.**

| # | Rule |
|---|---|
| **R-1** | A comparison between values of **different declared types** is `false`. Never an error, never a coercion. `"3" > 2` is false because one is text and one is a number. |
| **R-2** | A comparison where **either side is absent** is `false`, for every operator except `is absent`. |
| **R-3** | `is absent` is `true` exactly when the property has no value, and `false` otherwise. An empty string, an empty list and a zero are values, not absence. |
| **R-4** | A value present but **not conforming to its declared type** does not compare. It is `false` for every operator **and** the record is added to the query's problem list. Silence here is the defect. |
| **R-5** | `enum` compares by **declared position**, not by spelling: ordering follows the schema's order, and equality is exact-case against the declared set. |
| **R-6** | `money` compares only within **one currency**. Across currencies every operator is `false` and the query reports the currencies present. |
| **R-7** | `date` compares as an instant. A date and a date-time are the same declared type and compare directly. |
| **R-8** | `relation` compares by **target identity**, never by display text. Two links resolving to the same record are equal regardless of spelling. |
| **R-9** | `contains` on a list is **whole-element** membership. It is never substring matching. |
| **R-10** | `contains` on text is **substring** matching, case-sensitive. |
| **R-11** | Comparison is **total and never panics**: every type pair × every operator yields a boolean or a reported problem. There is no third outcome. |
| **R-12** | Every rule above applies **identically** whether the value came from a query literal or from a record. |
| **R-13** | Against a **`many` property, only `contains` and `is absent` are defined.** Equality and ordering (`=`, `!=`, `<`, `<=`, `>`, `>=`) are **not defined** and are reported as a problem naming the remedy — "`segment` holds many values; use `contains`". They are NOT silently treated as membership. *Added 2026-08-25 after the type-system agent surfaced the gap rather than routing around it: `segment != vendor` had no defined answer.* **Why refuse rather than help:** treating `=` as membership is the implicit coercion this design removes everywhere else, and an agent that gets a helpful answer to a malformed query never learns the schema. The refusal names the fix, exactly as FR-024 does for an unknown property. |

**AC-8.1** — the generated table covers every declared type × every declared type × every
operator, plus absent and non-conforming on both sides, and every expected value traces to a
numbered rule above.

**AC-8.2** — a comparator change that requires editing a **rule** is a specification change and
must be argued as one. A change that only regenerates cells is an implementation detail.

**AC-8.3** — `3 > 2` is `true`. Stated explicitly because it is the case that actually failed.

## 9. Holdout evaluation scenarios

**Not for use during development.** Not referenced in §6 or §7.

1. Point the system at a real vault of at least 500 notes with no schema; confirm nothing
   breaks and no errors are raised.
2. Declare a schema matching an existing note type; confirm the count of conforming records
   matches a hand count.
3. Ask for all records related to one record, and verify the answer against a manual search.
4. Corrupt one record's typed property; confirm the next query names that record.
5. Rename a heavily-referenced record; confirm every relation still resolves.
6. Run two agents writing different properties of the same record concurrently; confirm both
   land and neither file is damaged.
7. Import a real `.base` file containing an unsupported expression; confirm the translated
   view is correct and the untranslated clause is reported verbatim.

---

## 10. Ambiguity warnings — unresolved

| # | Ambiguous | Likely agent assumption | Needs |
|---|---|---|---|
| ~~A-1~~ | **RESOLVED — one rule.** ADR-068 O-5: an unresolvable target is reported as missing; the cause is not guessed at. |
| ~~A-2~~ | **RESOLVED — vault-wide.** ADR-068 O-6. FR-024's valid-names list is therefore vault-scoped, which is the same boundary the records themselves sit behind. |
| ~~A-3~~ | **RESOLVED — not ours to decide.** ADR-068 D0: the product ships mechanism, the vault ships convention. `record_log` writes a record of a vault-declared type and imposes no location or shape. |
| ~~A-4~~ | **RESOLVED — deferred.** ADR-068 D11 descoped; no FR needed. The edge-case table's sub-record rows are removed. |
| ~~A-5~~ | **RESOLVED — deferred.** ADR-068 D12 descoped; likewise. |

**A-4 and A-5 were specification defects and are now resolved by descoping the decisions that
caused them** (ADR-068 D11, D12). **A-6 replaces them as the live blocker: FR-020 and FR-020a
cannot be specified until ADR-068 D16's spike reports.** W1 does not start before then.
