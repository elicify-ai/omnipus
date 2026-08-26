# ADR-068 D16 storage spike — measured results

- **Date:** 2026-08-25
- **Gates:** ADR-068 D16 (S-1, S-2, S-3), spec FR-020 / FR-020a, wave W1
- **Status:** Reports. Recommends **(a) bleve**, under three named conditions, one of
  which is a blocking dependency bump that has nothing to do with records.
- **Nothing in `pkg/knowledge` was edited.** Everything below was built and run in a
  scratch module; the code is reproduced in §7.

---

## 0. The short version

| # | Question | Answer |
|---|---|---|
| **S-1** | How are record candidates selected? | **An indexed field works.** A `record_type` keyword field selects exactly its population in 0.10–5.07 ms over a 100,000-document index, and a second indexed predicate over a `record_props` array narrows the largest type from 25,000 to 5,018 in ~3 ms. Cost is linear in *hits*, not in index size. |
| **S-2** | How does an existing index acquire a new field? | **It does not, on its own — the silent no-op is real and was reproduced.** Two independent guards close it. A rebuild path is demonstrated end-to-end, and the guard that does not depend on human memory is the one that catches every case tested. |
| **S-3** | What does Go-side evaluation cost? | **Expression evaluation is 0.5–0.9 % of the true per-record cost.** Retrieval and decode are the other 99 %. At the spec's 10,000-record cap: 327–343 ms wall / 424–478 ms CPU, peak RSS 23.6–24.0 MB streamed. At 50,000 records it is 1.8–2.9 s and 61–143 MB, which breaches ADR-067's budget. |

**And one finding the spike was not looking for, which blocks everything:**

> **F-0.** The bleve version this repository pins (`bleve v2.6.0` → `zapx/v17 v17.1.2`)
> **writes corrupt segment data at 100,000 documents** — the exact scale ADR-067 targets.
> A search then panics the process or returns a decode error. It was reproduced with the
> **current shipping mapping and no record fields at all**, so it is an ADR-067 defect that
> ADR-068 merely tripped over. Upstream fixed it in **zapx v17.1.4**; the fix is verified
> here. See §2.

---

## 1. What was measured, and on what

**Machine.** macOS 26.5.2, darwin/amd64, 8 cores, 32 GB RAM, Go 1.26.6.
`github.com/blevesearch/bleve/v2 v2.6.0`, `github.com/blevesearch/zapx/v17 v17.1.2`
(the versions in this repository's `go.mod`).

> **Honesty note on wall-clock.** Throughout the timed runs this machine carried a load
> average between **41 and 117 on 8 cores**, from unrelated agent sessions that were not
> mine to stop (`ps` showed eight Node processes at 75–100 % CPU each). Every wall-clock
> figure below is therefore an **upper bound**, not a clean-machine number. CPU time
> (`getrusage(RUSAGE_SELF)`, user+sys) is reported alongside it because it is far less
> sensitive to contention; where the two disagree, trust the ratios more than the
> absolutes. Repeat runs agreed within about ±10 %, so the *shape* of the result is solid
> even though the absolute latency is pessimistic.

**Corpus.** 100,000 index documents, which is ADR-067's stated scale:

| Group | Count | Shape |
|---|---|---|
| Record notes | 50,000 | 8 types, deliberately skewed: company 25,000, contact 12,000, deal 6,000, meeting 3,000, project 1,600, invoice 1,200, vendor 700, asset 500. The largest type alone **exceeds the spec's 10,000 materialisation cap (FR-064)**, so the worst case is measured rather than assumed away. Each record carries 10 properties of mixed scalar type. |
| Plain notes | 50,000 | No record fields at all, so selectivity is measured against a realistic mixed vault rather than a corpus where every document is a record. |

Body text is ~432 bytes per note. The generator is seeded (`20260825`) and deterministic.
Batching mirrors `pkg/knowledge` exactly: commit at 128 documents or 512 KiB of body.

**Mapping.** The baseline variant is `pkg/knowledge/index.go::buildIndexMapping`
reproduced verbatim. The record variant adds three fields and changes nothing else.

---

## 2. F-0 — the pinned bleve corrupts a 100,000-document index

This was found while running the S-1 selectivity query. It is reported first because
nothing else in this document matters until it is fixed.

### 2.1 What happens

Against the 100,000-document index, with the **shipping** mapping:

```
term  record_type=company    PANIC runtime error: slice bounds out of range
                                   [:18878559] with capacity 18876348
match name=company           ERROR error reading norm: memUvarintReader overflow
term  name=deal              ERROR error reading frequency: memUvarintReader overflow
```

`18876348` is **exactly** the byte size of `store/0000000001f5.zap`, the large merged
segment. The reader is being told to read 2,211 bytes past the end of the file it has
mapped. The offset it was given came out of the segment itself.

The panic is **not recovered** anywhere in the bleve call stack — it unwinds through
`indexImpl.Search` into the caller. In the gateway that is a crash, not an error response.

### 2.2 It is not the record layer

| Index | Fields added | `record_type=company` | `match name=company` |
|---|---|---|---|
| `idx-base` — shipping mapping, **no record fields** | none | n/a (field absent) | **ERROR** reading norm |
| `idx-e1` — `record_type` only | 1 | **PANIC** | — |
| `idx-record` — all three record fields | 3 | **PANIC** | — |

The `idx-base` row is the one that matters: **an index built by today's mapping, holding
today's documents, is already corrupt.** `name` is a field ADR-067 ships and
`Index.Search` queries on every call.

### 2.3 What it depends on

| Variation | Result |
|---|---|
| 100,000 docs (50k records + 50k plain notes) | **corrupt** |
| 50,000 docs (records only, same generator) | clean |
| 25,000 docs | clean |
| 10,000 docs | clean |
| 100,000 docs, `forceSegmentVersion: 16` | **clean** |
| 100,000 docs, zapx **v17.1.4**, default segment version | **clean** |

It needs the segment layout that a 100,000-document build produces (15 segments, one
large merge). That is why no existing test in `pkg/knowledge` catches it: the suite's
fixtures are orders of magnitude smaller than the trigger.

### 2.4 Root cause, upstream

`blevesearch/zapx` PR **#409**, *"MB-71829: Fixed Chunk Offset Miscalculation in
IntCoders"*, merged 2026-05-18. The defect is on the **write** side, in
`chunkedIntCoder.Write`: the chunk boundaries were computed from end-offsets taken
*before* each chunk was processed by the `FileWriter`, and then the processed (different
length) bytes were written against those stale offsets. The reader's out-of-range slice is
the symptom, not the bug.

That `FileWriter` branch does not exist in zapx v16, which is precisely why forcing
segment version 16 is clean.

**First released in `zapx/v17 v17.1.4`.** This repository pins `v17.1.2` (`go.mod:105`).
Latest at time of writing is `v17.2.3`. No upstream issue describes the failure mode; the
fix arrived through Couchbase's internal tracker.

### 2.5 What to do

1. **Bump `github.com/blevesearch/zapx/v17` to ≥ v17.1.4.** Verified here: same corpus,
   same mapping, default segment version, every query correct.
2. **Force a rebuild of existing indexes**, because segments already written are corrupt
   and a version bump does not repair them. The S-2 machinery in §4 is exactly the
   mechanism for that — which is convenient, but note the two changes are independent and
   the bump must not wait for records.
3. **Add a large-corpus index test.** The existing suite cannot see this class of defect.
   A test that builds ~100,000 synthetic documents and asserts every term's hit count is
   slow, but it is the only kind that would have caught it.

---

## 3. S-1 — how record candidates are selected

**Answer: an indexed field can be added, and it selects.** `indexDoc` being a closed
5-field struct is not an obstacle — it is an ordinary Go struct, and the constraint that
mattered was never the struct, it was `doc.Dynamic = false` (§4).

### 3.1 The three fields

| Field | Mapping | Purpose |
|---|---|---|
| `record_type` | keyword analyzer, `Index=true`, `Store=true` | Narrows to one record type. The S-1 exit criterion. |
| `record_props` | keyword analyzer, `Index=true`, `Store=false`, **`[]string`** | Indexed equality on **arbitrary, runtime-named** properties, encoded as `name\x1fvalue` tokens. |
| `record_json` | `Index=false`, `Store=true` | The property map as JSON, for Go-side filter/group/aggregate (S-3). |

`record_props` is the part worth dwelling on. ADR-068 revision 2 was told that property
names are runtime data and dynamic mapping is off, and concluded that per-property fields
were impossible. That is true — but it does not follow that indexed property *filtering* is
impossible. **A `[]string` field under a single static field mapping is indexed
element-wise**, even with `Dynamic=false`, `IndexDynamic=false` and `StoreDynamic=false`.
One declared field therefore carries an unbounded, runtime-determined set of
`property=value` terms. Measured, not reasoned: see the hit counts below.

### 3.2 Selectivity, 100,000-document index (count-only term query)

| Query | Hits | Expected | Fraction of index | ms (3 runs) |
|---|---|---|---|---|
| `record_type=company` | 25,000 | 25,000 | 0.2500 | 4.74 / 5.07 / 4.85 |
| `record_type=contact` | 12,000 | 12,000 | 0.1200 | 2.60 / 2.20 / 2.18 |
| `record_type=deal` | 6,000 | 6,000 | 0.0600 | 1.24 / 1.15 / 1.06 |
| `record_type=meeting` | 3,000 | 3,000 | 0.0300 | 0.87 / 0.56 / 0.55 |
| `record_type=project` | 1,600 | 1,600 | 0.0160 | 0.30 / 0.32 / 0.30 |
| `record_type=invoice` | 1,200 | 1,200 | 0.0120 | 0.26 / 0.26 / 0.24 |
| `record_type=vendor` | 700 | 700 | 0.0070 | 0.46 / 0.17 / 0.14 |
| `record_type=asset` | 500 | 500 | 0.0050 | 0.34 / 0.10 / 0.11 |

Every count is exact. Cost tracks the **hit count**, not the 100,000-document index size:
500 hits in ~0.1 ms, 25,000 hits in ~4.9 ms.

### 3.3 Narrowing past the cap with a second indexed predicate

The largest type breaches FR-064's 10,000-record cap on its own. A conjunction with an
indexed property term fixes that *inside* bleve, with no Go-side scan:

| Query | Hits | Fraction of index | ms |
|---|---|---|---|
| `record_type=company` | 25,000 | 0.2500 | ~4.9 — **over the cap, refused** |
| `… AND record_props="status\x1fopen"` | 5,018 | 0.0502 | 3.11 / 3.21 / 4.12 |
| `… AND record_props="owner\x1fsam"` | 586 | 0.0059 | 2.81 / 1.45 / 1.43 |
| `record_props="nosuchprop\x1fx"` (undeclared) | 0 | 0 | 0.05 |

The last row is the one the ADR should care about: a property the vault never declared
returns **nothing, with no error** — not everything, and not a crash.

### 3.4 What the alternative costs

Without such a field the only available query is "everything". Measured on the baseline
index:

- `match_all` count: 100,000 in 13.0 ms;
- retrieving 50,000 of them with one stored field: **1,009 ms, peak RSS 79.4 MB**.

That is the behaviour ADR-068 D16 predicted — breach the candidate cap, refuse the query —
and it is now measured rather than asserted.

---

## 4. S-2 — how an existing index acquires a new field

### 4.1 The premise, confirmed

`openOrCreateBleve` calls `bleve.OpenUsing(path, cfg)` for an existing index. **`OpenUsing`
takes no mapping argument.** The mapping is persisted inside the scorch store at creation
and is authoritative forever after. Reproduced directly:

```
persisted mapping has record_type field: false
SILENT NO-OP CHECK: term query record_type=company -> hits=0, err=<nil>
idx.Fields() = [path _id _all body kind name offset]

CONTROL (fresh index, new mapping): term query record_type=company -> hits=1
CONTROL idx.Fields() = [path record_type _id _all body kind name offset]
```

Same code, same document, same query: **1 hit on a fresh index, 0 hits and no error on an
existing one.** That is the outcome D16 named as the worst available, and it is real.

Two corrections to the ADR's working assumptions, both from the probe:

- `index_meta.json` does **not** contain the mapping. It is 77 bytes:
  `{"storage":"scorch","index_type":"scorch","config":{"bolt_timeout":"5s"}}`. The mapping
  lives in the store.
- The persisted mapping **is readable** at runtime, via `idx.Mapping()`. That is what makes
  guard **G2** below possible, and it is the fact three revisions of D16 were missing.

### 4.2 Two guards

**G1 — an index format version.** A `index_format.json` sidecar next to the bleve
directory, holding an integer. Absent means "written before the format was tracked".
Mismatch or absence removes the bleve directory *and the manifest* and recreates the index
with the current mapping. Removing the manifest is what makes the following `Sync` re-scan
everything — this is not new machinery, it is exactly what `OpenIndex` already does for a
corrupt index.

**G2 — a mapping assertion.** After opening, compare the **persisted** mapping against the
mapping the current code would build, field by field, on the settings that decide whether a
query can work at all (`analyzer`, `index`, `store`, `docvalues`, `type`). Any difference
triggers the same rebuild.

Both exist because they fail differently. G1 depends on a human remembering to bump a
constant. **G2 depends on nobody remembering anything.**

### 4.3 Demonstrated, including the ways it breaks

Phase 1 builds an index with the pre-record mapping and a manifest. Phase 2 opens it with
record-aware code. Phase 3 writes a record and queries it.

| # | Scenario | Guard fired | `record_type=company` | Verdict |
|---|---|---|---|---|
| A | No guard (today's `openOrCreateBleve`) | — | **0 hits, err=nil** | **SILENT NO-OP** |
| B | G1 only | yes — `format version 1 != 2` | 1 hit | PASS |
| C | G2 only | yes — `field "record_props" is absent from the persisted mapping` | 1 hit | PASS |
| D | G1 + G2 | yes (G1 first) | 1 hit | PASS |
| E | G1 only, **developer forgot to bump the constant** | **no** | **0 hits, err=nil** | **SILENT NO-OP** |
| F | G1 + G2, same forgotten bump | yes — G2 caught it | 1 hit | PASS |

Rows A and E are why the recommendation is *both*, not either.

### 4.4 The mutation that proves G2 is doing work

A guard that compares only field **names** would have passed rows C and F just as well. To
show the settings comparison is not decorative, the code's mapping was mutated to declare
`record_props` with the `en` analyzer instead of `keyword`, against an index already built
with `keyword` — a drift a name-only guard cannot see:

| Scenario | Guard fired | Term query on the literal keyword token |
|---|---|---|
| **Strong G2** (compares settings) | **yes** — `persisted analyzer=keyword …, code wants analyzer=en …` → rebuild | 0 hits |
| **Weak G2** (names only) — *the mutation* | **no** | 1 hit |
| **Strong G2, nothing drifted** (control) | **no** — correctly does not rebuild | 1 hit |

The same query returns **0 or 1** depending only on which analyzer is actually in force,
and the caller has no way to tell which mapping it got. The strong guard sees it; the
weakened guard does not. The third row is the one that stops G2 from being a rebuild-always
guard: with no drift it stays silent.

### 4.5 So: is a silent no-op impossible?

**For every case tested, yes**, with G1 and G2 together — and the two cover different
failure modes, so neither is redundant. The honest boundary: G2 compares the fields the
code declares. A field the code *stops* declaring, or a mapping property outside the five
compared, is not covered. Both are cheap to add if the ADR wants them; neither was needed
for any scenario above.

---

## 5. S-3 — what Go-side evaluation actually costs

Pipeline: bleve search → retrieve the stored `record_json` → `json.Unmarshal` into
`map[string]any` → filter on one property → two-level group (`status` × `owner`) with a sum
aggregate. 32 groups, 8,027 of 10,000 records surviving the filter.

Two variants, because the difference decides the budget question:

- **materialised** — `SearchRequest.Fields = ["record_json"]`, every hit and its stored
  field held in memory, then processed. This is the obvious implementation.
- **streamed** — hits carry ids only; each record is fetched, folded into the aggregate and
  dropped. Peak memory becomes independent of the candidate count.

### 5.1 Measured, 3 runs each, isolated processes

| Candidates | Mode | Wall ms | CPU ms | Peak RSS MB |
|---|---|---|---|---|
| 500 (`asset`) | streamed | 15.1 / 17.6 / 17.8 | 16.7 / 20.7 / 21.4 | 14.7 / 14.8 / 15.2 |
| 5,018 (two predicates) | streamed | 193.9 / 188.1 / 170.6 | 232.0 / 245.0 / 231.3 | 24.1 / 24.3 / 24.1 |
| 6,000 (`deal`) | streamed | 203.3 / 192.3 / 210.5 | 257.7 / 258.6 / 264.9 | 19.8 / 20.4 / 19.4 |
| **10,000 (the FR-064 cap)** | **streamed** | **326.9 / 353.1 / 334.9** | **423.5 / 436.1 / 431.0** | **23.6 / 24.0 / 23.6** |
| 10,000 | materialised | 342.9 / 333.4 / 340.3 | 463.9 / 478.0 / 461.9 | 34.7 / 34.4 / 38.1 |
| 50,000 (all records) | streamed | 1820.7 / 1798.3 / 1864.7 | 2431.3 / 2420.0 / 2512.5 | 60.9 / 61.1 / 61.5 |
| 50,000 | materialised | 2892.9 / 1965.3 / 2025.2 | 2881.1 / 2584.0 / 2578.0 | 134.8 / 131.3 / 142.3 |

Steady-state RSS with the index open and idle: **12.9–15.1 MB** (identical with and without
the record fields).

### 5.2 Where the time goes — and the ~794 ns/record figure

Phase breakdown at the 10,000 cap, materialised (so the phases are separable):

| Phase | Wall ms | CPU ms | ns per record | Share of total |
|---|---|---|---|---|
| bleve search **+ stored-field retrieval** | 197–224 | 273–294 | ~20,000 | ~60 % |
| `json.Unmarshal` to `map[string]any` | 123–156 | 145–182 | ~13,000 | ~39 % |
| **filter + two-level group + sum** | **2.2–3.2** | **2.2–3.8** | **218–376** | **0.5–0.9 %** |

Retrieval alone is isolated by re-running the identical query with no stored fields
requested: 50,000 ids in **147–161 ms** (~3 µs/record) against **1,092–1,549 ms** with the
stored field (~22–31 µs/record). **Stored-field retrieval is roughly 7× the cost of the
search that finds the documents.**

So, on ADR-068's warning: expression evaluation measures **218–376 ns/record** here, in the
same order as the ~794 ns/record figure in circulation — and that phase is **under one
percent** of what a query actually costs. Anyone sizing a query from that number is off by
roughly **two orders of magnitude**. Total measured cost is **33,000–48,000 ns/record**.

### 5.3 Against ADR-067's < 64 MB steady-state budget

| Path | Peak RSS | Verdict |
|---|---|---|
| Idle, index open | 12.9–15.1 MB | — |
| 10,000 records, streamed | **23.6–24.0 MB** | **fits, with room** |
| 10,000 records, materialised | 34.4–38.1 MB | fits |
| 50,000 records, streamed | 60.9–61.5 MB | **1.5–3 MB under a 64 MB budget, in a process doing nothing else. Not acceptable.** |
| 50,000 records, materialised | 131.3–142.3 MB | **breaches by >2×** |

The spec already forbids the two failing rows: FR-064 refuses a candidate set above 10,000.
The measurement's contribution is that the cap is now known to be **load-bearing for the
memory budget**, not merely a politeness limit — and that the difference between streamed
and materialised evaluation is 3–6× peak RSS at no cost in time.

---

## 6. Recommendation

**(a) bleve — subject to three conditions.** §3.6's dedicated aggregation store is **not**
needed on these numbers, so ADR-067's A2 and the CLAUDE.md SQLite rule stand.

| # | Condition | Why |
|---|---|---|
| **C-1** | Bump `zapx/v17` to **≥ v17.1.4** and force a rebuild of existing indexes | §2. Blocking, and **independent of records** — today's index is corrupt at 100,000 documents. Do not let this wait on ADR-068. |
| **C-2** | Evaluate **streamed**, never materialised | §5.3. 3–6× lower peak RSS, no time penalty. Materialising 10,000 records still fits, but it spends a third of the budget to buy nothing. |
| **C-3** | Enforce FR-064's 10,000-record cap as a hard precondition, and count candidates **before** retrieving anything | §5.3. 50,000 records breaches the budget even streamed. The cap is the thing keeping the design inside ADR-067. |

Design that follows from the measurements:

- `indexDoc` gains `record_type` (keyword, indexed, stored), `record_props` (keyword,
  indexed, **`[]string`** of `name\x1fvalue`) and `record_json` (stored, not indexed).
- Candidate selection is `record_type` plus, where the caller gave equality filters, one
  `record_props` term per filter — pushed into bleve, so the cap is met by narrowing rather
  than by refusing.
- Everything bleve cannot express (ranges, comparisons, grouping, aggregates) is evaluated
  in Go over the streamed candidate set, which §5.2 shows is not where the cost is anyway.
- `openOrCreateBleve` gains G1 **and** G2 (§4.2), rebuilding through the path `OpenIndex`
  already uses for a corrupt index.

### 6.1 What this does not say

Stated plainly, because the alternative is a fourth revision resting on something
unmeasured:

- **Not measured:** concurrent queries; the record **write** path; property counts above
  10 per record; interaction with ADR-067's other index consumers; any non-macOS platform.
- **Wall-clock is an upper bound** — every timing above was taken on a machine at 5–15×
  oversubscription (§1). CPU time is the more transferable number.
- **~460 ms of CPU for a 10,000-record query is not fast.** It fits the memory budget,
  which is what D16 gated on, but a dedicated aggregation store would be substantially
  quicker. If interactive latency later becomes the binding constraint rather than memory,
  §3.6 should be reconsidered on that basis — it is not reopened here because latency is
  not what D16 asked about.
- **The `record_props` encoding covers equality only.** Ranges, prefixes and comparisons
  are Go-side. That is consistent with FR-021, and it is why the cap matters.

---

## 7. The code that was run

Everything lives in a scratch module (`module adr068spike`) whose `go.mod` and `go.sum`
are copies of this repository's, so the dependency versions are identical. `pkg/knowledge`
was not modified.

### 7.1 `probe1` — S-2, the silent no-op (§4.1)

Creates an index with the shipping 5-field mapping, closes it, reopens with
`bleve.OpenUsing`, indexes a document carrying a sixth field, and queries it. Prints the
persisted mapping's field set, `idx.Fields()`, and the query result. A control repeats the
whole thing on a fresh index built with the new mapping.

### 7.2 `probe3` / `probe4` — F-0 isolation (§2)

`probe3` runs one query per field against a given index with `recover()` around each, so a
panic maps to a field rather than killing the run. `probe4` sweeps every type word against
one field with both a term query (unscored — exercises the freq/loc chunk decoder) and a
match query (scored — exercises the norm decoder), which is how the two distinct error
messages were separated.

### 7.3 `probe5` — S-2, the guards and their mutations (§4.3, §4.4)

Implements `openOrCreate` with G1 and G2 and the rebuild path. Scenario selection is by
environment variable so a mutation is a flag, not an edit: `GUARD=none|g1|g2|both`,
`NOBUMP=1` (forget the version bump), `WEAKG2=1` (degrade G2 to field names),
`MUTATE_ANALYZER=1` (declare `record_props` with `en`), `PHASE1=record` (pre-existing index
already has the record fields).

Key excerpt — the assertion that closes the no-op:

```go
// fieldSignatures reduces a mapping to the comparable facts: for each field,
// the settings that decide whether a query against it can work at all.
func fieldSignatures(m bleveMapping.IndexMapping, weak bool) map[string]string {
	out := map[string]string{}
	im, ok := m.(*bleveMapping.IndexMappingImpl)
	if !ok || im.DefaultMapping == nil {
		return out
	}
	for name, dm := range im.DefaultMapping.Properties {
		for _, fm := range dm.Fields {
			if weak {
				out[name] = "present" // the mutation: names only
				continue
			}
			out[name] = fmt.Sprintf("analyzer=%s index=%v store=%v docvalues=%v type=%s",
				fm.Analyzer, fm.Index, fm.Store, fm.DocValues, fm.Type)
		}
	}
	return out
}
```

### 7.4 `bench` — S-1 and S-3 (§3, §5)

`bench build <dir> <base|record>` writes the corpus; `bench query <dir> <variant>` runs the
measurements. Knobs: `SEGVER` (pins `forceSegmentType`/`forceSegmentVersion`), `DOCVALUES`,
`F_TYPE`/`F_PROPS`/`F_JSON` (which record fields exist), `SCALE_DIV`/`PLAIN_NOTES` (corpus
size), `ONLY` (which pipeline runs, so each is measured in a fresh process with a clean
RSS baseline).

Peak RSS is real resident set, polled from `ps -o rss=` every 20 ms — not
`runtime.MemStats`, which cannot see scorch's mmap'ed segments and would have understated
every figure in §5.

The record fields, as mapped:

```go
rt := bleve.NewTextFieldMapping()          // record_type
rt.Analyzer, rt.Store = "keyword", true
rt.IncludeTermVectors, rt.IncludeInAll, rt.DocValues = false, false, false
doc.AddFieldMappingsAt("record_type", rt)

props := bleve.NewTextFieldMapping()       // record_props — []string on the Go side
props.Analyzer, props.Store = "keyword", false
props.IncludeTermVectors, props.IncludeInAll, props.DocValues = false, false, false
doc.AddFieldMappingsAt("record_props", props)

rj := bleve.NewTextFieldMapping()          // record_json — stored payload only
rj.Store, rj.Index = true, false
rj.IncludeTermVectors, rj.IncludeInAll, rj.DocValues = false, false, false
doc.AddFieldMappingsAt("record_json", rj)

doc.Dynamic = false                        // unchanged from the shipping mapping
```

### 7.5 `adr068e2e` — F-0 through the real `pkg/knowledge`

A separate scratch module with `replace github.com/elicify-ai/omnipus => <worktree>`, so it
imports the **actual shipping package** rather than a replica. It writes a real
100,000-note collection to disk and calls `knowledge.OpenIndex`, `Index.Sync` and
`Index.Search`. Result: §2.6.
