# UAT test plan — vault records (ADR-068)

**Branch:** `feat/library-improvements`
**Date:** 2026-08-28
**Covers:** ADR-068 revision 8; `vault-records-spec-2026-08-25.md` Draft 6
**Audience:** a human tester with the app in front of them. No Go, no terminal beyond
starting the binary, no reading of source code.
**Previous round:** `uat-library-records-2026-08-26.md` and its results
`uat-results-2026-08-27.md`. Every defect that round found is carried forward here as a
numbered case, because those are the ones that recur.

---

## 1. Read this first: what is, and is not, testable by hand

**State of the branch at the time of writing (2026-08-28).** The record layer described by
ADR-068 is **mostly unbuilt**. At the moment this plan was written:

- `pkg/records` exists as a **library with no consumer** — the same finding the previous
  round recorded. Nothing in the gateway, the agent tool surface or the SPA reaches it.
- **None of the six `vault_*` tools exists** anywhere in the backend, the contracts or the
  SPA. Searching the tree for `vault_describe`, `vault_find` or `vault_configure` outside
  the design documents returns nothing.
- There is **no record table, no vault health view and no problems screen** in the SPA.
- `pkg/records/money.go` is still present. ADR-068 revision 7 **deleted the `money` type**;
  the code has not caught up yet. That is expected mid-flight and is *not* a finding — but
  a `money` property type that is still **accepted by a schema** is (Case C-8).

**Five implementing agents are writing this code concurrently with this plan.** So the tree
will not look like the paragraph above by the time anyone runs these cases. **Do not assume
either way.** Run **Part 0** first. It is a five-minute, UI-only check that tells you which
parts of this plan can run today, and everything else follows from its answer.

**What this means for a verdict.** A case describing a screen or a tool that does not exist
yet is **Blocked — feature absent**. Blocked is an honest, complete answer and it is what we
want. It is **never** a pass, it is never "N/A", and it is never quietly dropped from the
report. A plan that pretends a missing screen was tested is worse than no plan.

**What is testable by hand today, regardless of how far the record layer got:** everything
in **Part A** — mounting, indexing, previews, embeds, the library screen. That is the part
the previous round exercised, and every defect it found is re-run here.

---

## 2. The tester's rules

These were earned in the previous round. They are not advice.

1. **Verify your own console capture before you trust its silence.** Before you report "no
   console errors", deliberately produce one and confirm you saw it — see §4.4 for exactly
   how. A silent console equally means a broken listener. Do this at the **start and the end**
   of your session; an instrument that worked an hour ago is not evidence about now.
2. **"I restarted and then it worked" is a FAILURE, not a pass.** Restarting hides the whole
   class of defect this product has shipped twice. Report the restart as the finding.
3. **Assume every failure is real.** Not a glitch, not a slow machine, not flakiness, not
   "probably my fixture". If it happened once it is worth reporting. If it happened twice,
   say so and say how many times out of how many.
4. **Do not diagnose. Do not work around. Do not read the source.** Your job is to say
   exactly what you did and exactly what happened. Explaining *why* is somebody else's job
   and a wrong explanation is worse than none — the previous round had three "must fix" items
   that did not exist and one root cause that was a dead measuring instrument.
5. **Report Pass / Fail / Blocked.** Blocked means you could not run it: the feature is
   absent, the fixture would not build, something upstream failed. It is a real answer.
6. **Do not "helpfully" fix a fixture that refuses.** If the app rejects something this plan
   told you to write, that rejection is the result. Record it and move on.
7. **One case, one verdict.** If a case has five steps and step 3 fails, the case fails —
   say which step, then say whether the remaining steps still ran.

---

## 3. How to drive an agent tool from chat

Five of the six tools have **no screen of their own**. You reach them the way a user does:
you ask an agent in chat, and the tool call renders in the thread.

**Before you start:** Settings → Chat → turn **Verbose chat ON**. Some tool calls are hidden
from the thread by default. With verbose chat on, nothing is hidden — and for this plan you
need to see every call and every argument.

**The template.** Be literal. Do not ask in English and hope; tell the agent the call:

> Call `vault_find` with `type` = `specimen` and `filter` =
> `{"all":[{"property":"condition","op":"=","value":"dry"}]}`.
> Show me the tool result exactly as you received it, with nothing summarised or omitted.

**Then read the tool call in the thread, not the agent's prose.** Two things to check every
time, in this order:

1. **The arguments the tool actually received match what you asked.** If they do not, re-ask
   once with the literal instruction. If they still do not, the case is **Blocked** (the
   agent would not send it) — record what it sent instead, because that is its own finding.
2. **The tool result**, verbatim. The agent's summary of a result is not the result. If the
   agent's prose and the tool result disagree, that is a **finding** and the tool result is
   the truth.

**If the tool is set to `ask`**, an approval prompt appears. Approve it unless the case says
otherwise, and note what the prompt told you it was about to do.

---

## 4. Setup

### 4.1 A clean install

```
export OMNIPUS_HOME=/tmp/omnipus-uat-records
rm -rf "$OMNIPUS_HOME" && mkdir -p "$OMNIPUS_HOME"
OMNIPUS_BEARER_TOKEN="" ./omnipus gateway --allow-empty
```

Open the app in a browser and complete onboarding. Use a model that supports tool use —
`z-ai/glm-5-turbo`, `google/gemini-2.5-flash` or `anthropic/claude-3.5-haiku`. A model without
tool support returns 404 on every turn and every case in this plan will look broken.

If the gateway exits without a message, look in
`/tmp/omnipus-uat-records/logs/gateway_panic.log`.

### 4.2 Building the fixture vaults

You will create folders and text files. A file manager and a plain-text editor are enough —
this is not terminal work, and every file below is short enough to type or paste.

**Vault Alpha** — `/tmp/uat-vault-alpha/`

Inside it, create an **empty folder named `.obsidian`**. That marker is what makes a folder a
knowledge base. Without it, Omnipus mounts the folder and indexes nothing (Case A-2 tests
exactly that, so do not skip the marker here and do not add one there).

Then create the folder `/tmp/uat-vault-alpha/.omnipus-vault/records/` and put the four schema
files of §4.3 in it.

**Vault Beta** — `/tmp/uat-vault-beta/` — with an empty `.obsidian` folder, a handful of
ordinary notes, and **no `.omnipus-vault` folder at all**. This is the "empty install ships
nothing" fixture (Part M).

**Vault Gamma** — `/tmp/uat-vault-gamma/` — a plain folder of markdown with **no `.obsidian`
and no `.omnipus-vault`**. This is the not-a-knowledge-base fixture (Case A-2).

### 4.3 The fixture schemas

**Read this before you type them.** Every name below — `specimen`, `expedition`, `keeper`,
`condition`, `dry` — is **invented for this test**. Omnipus is supposed to know none of them.
That is the point: if the product behaves differently for a word it has heard of, it has
domain vocabulary baked in and Part M will catch it.

`/tmp/uat-vault-alpha/.omnipus-vault/records/specimen.yaml`

```yaml
schema_version: 1
type: specimen
label: Specimen
id_prefix: SP
properties:
  label:        { type: text, required: true }
  condition:    { type: enum, values: [dry, damp, frozen, ambient] }
  tags:         { type: enum, values: [fragile, loaned, sealed], many: true }
  collected_on: { type: date }
  count:        { type: integer }
  mass_g:       { type: decimal, scale: 3 }
  curator:      { type: person }
  expedition:   { type: relation, to: expedition, inverse: specimens }
```

`/tmp/uat-vault-alpha/.omnipus-vault/records/expedition.yaml`

```yaml
schema_version: 1
type: expedition
label: Expedition
id_prefix: EX
properties:
  label:   { type: text, required: true }
  region:  { type: enum, values: [northern, southern, coastal] }
  started: { type: date }
```

`/tmp/uat-vault-alpha/.omnipus-vault/records/keeper.yaml`

```yaml
schema_version: 1
type: keeper
label: Keeper
id_prefix: KP
properties:
  label: { type: text, required: true }
```

`/tmp/uat-vault-alpha/.omnipus-vault/records/token.yaml` — the folding and ordering fixture

```yaml
schema_version: 1
type: token
label: Token
properties:
  label:  { type: text }
  word:   { type: enum, values: [straße, σίσυφος, müller, łódź, ﬁle, istanbul] }
  phase:  { type: enum, values: [ember, azure, cinder, dune] }
  ranked: { type: enum, values: [1-ember, 2-azure, 3-cinder, 4-dune] }
  mood:   { type: enum, values: [calm] }
```

`phase` is declared in the order `ember, azure, cinder, dune` and its **lexical** order is
`azure, cinder, dune, ember`. The two are deliberately different — that is what makes Part E
able to tell them apart. `ranked` is the same four values carrying a domain order as a prefix.

### 4.4 Proving your console capture works

Open the browser console (F12). At the **start** of your session, and again at the **end**:

1. In the console, type `console.error('UAT probe')` and press Enter. **You must see it.**
2. In the console, type `fetch('/api/v1/definitely-not-a-real-endpoint')` and press Enter.
   **You must see a failed request in the console or the network tab.**

If either probe is invisible, your instrument is dead and **you cannot report "no console
errors" for anything you did with it**. Say so in the report instead. This is not a formality
— the previous round discarded a plausible-looking root cause that turned out to be a dead
socket reporting silence as evidence.

### 4.5 One standing rule for the whole run

**Do not restart the gateway.** Not between parts, not to "reset", not to make something work.
Run the whole session on one process. If you must restart, that is itself a finding: record
what forced it, what you had just done, and what changed afterwards.

---

## Part 0 — What exists today (run this first, ~5 minutes)

*This part does not test behaviour. It tells you which of the parts below can run at all, so
you mark the rest **Blocked — feature absent** instead of failing them.*

| Step | Do this | Record |
|---|---|---|
| 0.1 | Go to **Agents** → pick **Mia** → **Tools & Permissions** | Which of `vault_describe`, `vault_find`, `vault_read`, `vault_edit`, `vault_restructure`, `vault_configure` are listed |
| 0.2 | On the same screen | Whether the nine `knowledge_*` tools are still listed (`knowledge_search`, `knowledge_graph`, `knowledge_tasks`, `knowledge_create`, `knowledge_link`, `knowledge_set_property`, `knowledge_append_section`, `knowledge_move`, `knowledge_rename`) |
| 0.3 | In chat, ask any agent: *"List every tool whose name begins with `vault_`. Do not guess — name only tools you actually have."* | The list it reports |
| 0.4 | Go to **Library** | Whether there is anything resembling a record table, a record-type list, or a vault health / problems view |

**Write the answers at the top of your report.** Then use this map:

| If Part 0 shows | Then these parts can run |
|---|---|
| No `vault_*` tools at all | **Part A only.** Everything else is Blocked — feature absent |
| `vault_describe` present | Part A, Part B, Part M |
| `vault_find` present | + Parts C, D, E, F, G (except the health-view halves) |
| `vault_read` present | + the read halves of Part I |
| `vault_edit` present | + Part I |
| `vault_restructure` present | + Part J |
| `vault_configure` present | + Part K, Part L |
| A record table or health view in Library | + Part N |

**Both lists in 0.1 and 0.2 matter.** The end state is **six `vault_*` names and zero
`knowledge_*` names**. Seeing all fifteen at once is a normal mid-flight state today and is
**not** a finding *yet* — but record the counts, because "the old nine were never removed" is
a shipped defect and someone has to be the one who noticed.

---

## Part A — The library, and every defect the last round found

*These run whatever state the record layer is in. Five of the eight are re-runs of confirmed
findings from 2026-08-27; they are here because a fixed defect that comes back is the most
expensive kind, and because three of them are the exact traps that made the last round's own
test fixtures silently wrong.*

### Case A-1 — Mount a vault and watch it index

| Step | Do this | Expect |
|---|---|---|
| A-1.1 | Go to **Library** | Loads with no console error |
| A-1.2 | Mount **Vault Alpha** (`/tmp/uat-vault-alpha`) | The mount is accepted |
| A-1.3 | Watch the screen **without refreshing** | Indexing progress appears **on its own**, within a few seconds, and advances |
| A-1.4 | Wait for it to finish | Progress reaches completion, and a note count is shown that is plausible for the folder |

**Fail if:** progress never appears; progress appears only after a manual refresh; the count is
zero for a folder that clearly has notes; you must restart the app to make indexing start; or
there is no note count anywhere in the UI.

*Previous round: **FAIL** (F2). Indexing worked; the progress display never appeared, at any
vault size, and the banner permanently read "No indexing progress has arrived since you opened
it". There was no note count anywhere.*

### Case A-2 — A folder that is not a knowledge base must say so

*Why this matters: this is the defect that made the previous round's own fixtures wrong. A
plain folder mounts, gets a `MOUNTED` badge, is counted in the mount total, and indexes
**nothing** — and the only signal is the absence of two things you would have to already know
to look for.*

| Step | Do this | Expect |
|---|---|---|
| A-2.1 | Mount **Vault Gamma** (`/tmp/uat-vault-gamma`, no `.obsidian`) | Either the mount is refused with a message saying what is missing, **or** it is accepted and the UI says plainly, before or immediately after mounting, that this folder will not be indexed and what would make it a knowledge base |
| A-2.2 | Look at the Gamma row in the mount list beside the Alpha row | You can tell them apart **without** clicking into either |
| A-2.3 | Open Gamma's detail view | It states its status; it does not merely omit the search box |
| A-2.4 | Search for a word you know is in a Gamma note | Either a result, or an explicit "this folder is not indexed because…" — never a bare "no results" |

**Fail if:** Gamma is indistinguishable from Alpha at any of these four points, or a search
over an unindexable folder reports an ordinary empty result.

*Previous round: **FAIL** (F6), confirmed by two testers.*

### Case A-3 — Index state for someone who arrives late

*Why this matters: progress is delivered live only. A browser that connects **after** an index
finished has nothing to receive — so it renders "no progress has arrived" about an index that
completed successfully hours ago, permanently. For any fast index that is the normal case, not
the edge case.*

| Step | Do this | Expect |
|---|---|---|
| A-3.1 | With Alpha fully indexed, **reload the page** | The Library shows Alpha's real, completed state |
| A-3.2 | Open the app in a **second browser window** (or a private window) and go to Library | Same completed state, immediately |
| A-3.3 | Compare what the panel says with what a search reports about index freshness | The two agree |

**Fail if:** either window shows "no indexing progress has arrived", an empty state, or an
unknown state, for a collection that has finished indexing.

### Case A-4 — Indexing still works after a settings change

*Why this matters: this exact defect shipped once. Changing a setting triggers an internal
reload; the reload killed the indexing service and never restarted it. Mounting a vault
afterwards returned success and indexed nothing — no error, no log line — until the process
was restarted.*

| Step | Do this | Expect |
|---|---|---|
| A-4.1 | Go to **Settings**, change any setting, save | Saved confirmation |
| A-4.2 | Return to **Library** and mount **Vault Beta** | The mount is accepted |
| A-4.3 | Watch without refreshing and **without restarting** | Progress appears and completes, exactly as in A-1 |
| A-4.4 | Search for a word you know is in a Beta note | The note is found |

**Fail if:** the second vault reports success but never indexes. **Do not restart the app to
make it work** — restarting is what hides this defect, and hiding it is the whole reason this
case exists.

*Previous round: **PASS**.*

### Case A-5 — Embedded images

| Step | Do this | Expect |
|---|---|---|
| A-5.1 | Put an image in Alpha and a note embedding it (`![[picture.png]]`) | — |
| A-5.2 | After indexing, open the note in the preview panel | The image is **displayed** — a real image, not a text link, not a broken icon |
| A-5.3 | Try a note whose image sits in a subfolder | Also displays |
| A-5.4 | Try a note embedding an image that does not exist | Degrades visibly: you can **tell it apart** from a working embed |

**Fail if:** the embed renders as a link; clicking that link 404s a file you can see in the
listing; or a missing embed renders identically to a present one.

**Check the preview panel, not a raw URL.** They behave differently and a raw URL passing is
not evidence about the panel.

*Previous round: **FAIL** (F4). Zero `<img>` elements; the generated link dropped the mount
prefix, so `vault-a/picture.png` returned 200 and the emitted `picture.png` returned 404. A
missing embed rendered identically to a present one.*

### Case A-6 — HTML files

| Step | Do this | Expect |
|---|---|---|
| A-6.1 | Put an `.html` file with a heading and a paragraph into Alpha | — |
| A-6.2 | After indexing, open it in the library | Its readable text is shown |
| A-6.3 | Search for a word that appears only in that file | The file is found |

**Fail if:** the panel says "preview unavailable" — especially if the content itself loads
(you can check in the network tab: a 200 with a visible "unavailable" message is the failure),
or the file shows raw markup, or its text is not searchable.

*Previous round: **FAIL** (F3). "Preview unavailable … needs the isolated preview endpoint,
which this build does not serve yet", while the content fetch returned 200. 3 of 3 attempts.*

### Case A-7 — "The file changed since it was indexed", for files that never changed

| Step | Do this | Expect |
|---|---|---|
| A-7.1 | Search for a fragment of a **filename** in Alpha | Results, each with a real excerpt |
| A-7.2 | Search for a word from the **body** of the same file | Results, each with a real excerpt |
| A-7.3 | Compare the two | Both behave the same way |

**Fail if:** the filename search reports "No excerpt: the file changed since it was indexed"
for a file that nothing has written to — particularly when a body-word search on the *same
file, in the same session, seconds apart* returns a full excerpt.

*Previous round: **FAIL** (F5). Reproduced on every markdown file matched by filename, twice.*

### Case A-8 — Unmounting mid-index

| Step | Do this | Expect |
|---|---|---|
| A-8.1 | Mount a large folder (several thousand notes if you have one) | Indexing starts |
| A-8.2 | About three seconds in, unmount it | The confirmation dialog closes cleanly |
| A-8.3 | Watch the mount list **without reloading**, for a full minute | The row disappears |
| A-8.4 | Now reload | It is still gone (i.e. step A-8.3 was telling the truth) |

**Fail if:** the row stays, still badged `MOUNTED`, until you reload; the dialog stays painted;
or the page stops responding.

*Previous round: **FAIL** (F7), 3 of 3 attempts, the stale row persisting 50–70 seconds. Not a
size problem — the same vault unmounted after indexing updated in 3 seconds.*

### Case A-9 — The gateway stays up

*Why this matters: the previous round saw the process terminate on its own with **exit code 0**
— no panic, nothing in the panic log — while the UI sat idle. The user loses their session with
no message and no indication anything is wrong. Two independent testers, two occurrences.*

| Step | Do this | Expect |
|---|---|---|
| A-9.1 | At the end of every part of this plan, check the app still responds | It does |
| A-9.2 | If it stopped responding, check whether the process is still alive | It is |

**Fail if:** the gateway exits on its own at any point during the run. Record what you had just
done, the wall-clock time, and the last few lines of
`/tmp/omnipus-uat-records/logs/gateway.log`. **Do not investigate further** — the log lines are
the finding.

### Case A-10 — Awkward but legitimate content

| Step | Do this | Expect |
|---|---|---|
| A-10.1 | A note with a single line a few thousand characters long | Handled |
| A-10.2 | A note with accented and non-Latin characters in the body **and in the filename** | Displayed correctly everywhere: list row, preview header, body, search excerpt |
| A-10.3 | A note with frontmatter and an empty body | Handled, not an error |
| A-10.4 | A file with an unknown extension (`.xyz`) | Ignored quietly or reported clearly — never a crash, never a silent vanish |
| A-10.5 | An Obsidian `.base` file | Handled: rendered, or refused with a message saying what it is |
| A-10.6 | A folder with several hundred notes | Indexes without freezing the UI |

*Previous round: **PASS** on all of these. They are re-run because they are cheap and because
Part C is about to put much stranger content into the same pipeline.*

### Case A-11 — Error messages a person can read

| Step | Do this | Expect |
|---|---|---|
| A-11.1 | Try to mount a path that does not exist | A refusal in plain language, not truncated mid-word |
| A-11.2 | Try to mount a file instead of a folder | Same |
| A-11.3 | Mount the same folder twice | Handled with a clear message |

**Fail if:** the message is a raw error string with Go package names, a status code and a
`stat` call in it, or is cut off mid-sentence.

*Previous round: **F8 (minor)** — mount rejections were correct but surfaced as
`400: workspace: mount target is not an existing directory: "/private/…": stat /private/…`,
truncated mid-word.*

---

## The fixture notes

*Everything from Part B onward needs these. Create them in Vault Alpha with a text editor.
Type them exactly, including the deliberately wrong values — the wrong ones are the point.*

**Clean records** — `/tmp/uat-vault-alpha/Specimens/`

```markdown
<!-- Specimens/fern.md -->
---
type: specimen
id: SP-0001
label: Bracken frond
condition: dry
tags: [fragile, sealed]
collected_on: 2026-03-14
count: 12
mass_g: 4.500
expedition: "[[Northern sweep]]"
---
Collected on the ridge. Notes about pricing of transport crates.
```

```markdown
<!-- Specimens/moss.md -->
---
type: specimen
id: SP-0002
label: Cushion moss
condition: damp
tags: [loaned]
collected_on: 2026-04-02
count: 3
mass_g: 0.125
expedition: "[[Northern sweep]]"
---
Second sample.
```

```markdown
<!-- Specimens/lichen.md -->
---
type: specimen
id: SP-0003
label: Crustose lichen

# note: condition is deliberately absent on this record
count: 0
mass_g: 12.345678901234567890123456789012
expedition: "[[Coastal survey]]"
---
Zero count is a value, not an absence.
```

```markdown
<!-- Expeditions/northern-sweep.md -->
---
type: expedition
id: EX-0001
label: Northern sweep
region: northern
started: 2026-03-01
---
```

```markdown
<!-- Expeditions/coastal-survey.md -->
---
type: expedition
id: EX-0002
label: Coastal survey
region: coastal
started: 2026-04-01
---
```

**Deliberately broken records** — `/tmp/uat-vault-alpha/Specimens/broken/`

```markdown
<!-- broken/b1.md -->  count is not a number
---
type: specimen
id: SP-0101
label: Bad count
count: "many"
---
```

```markdown
<!-- broken/b2.md -->  mass_g carries a unit
---
type: specimen
id: SP-0102
label: Bad mass
mass_g: "2.5kg"
---
```

```markdown
<!-- broken/b3.md -->  ambiguous date, and an enum value that is not declared
---
type: specimen
id: SP-0103
label: Bad date
collected_on: 03/04/2026
condition: soggy
---
```

```markdown
<!-- broken/b4.md -->  relation stored as text, not a wikilink
---
type: specimen
id: SP-0104
label: Text relation
expedition: "Northern sweep"
---
```

```markdown
<!-- broken/b5.md -->  relation to a note of the wrong type
---
type: specimen
id: SP-0105
label: Wrong-type relation
expedition: "[[Bracken frond]]"
---
```

```markdown
<!-- broken/b6.md -->  relation to a note that does not exist
---
type: specimen
id: SP-0106
label: Dangling relation
expedition: "[[Southern traverse]]"
---
```

```markdown
<!-- broken/b7.md -->  integer above the 64-bit bound
---
type: specimen
id: SP-0107
label: Too big
count: 9223372036854775808
---
```

```markdown
<!-- broken/b8.md -->  a scalar property given a list
---
type: specimen
id: SP-0108
label: List in a scalar
condition: [dry, damp]
---
```

**Two records sharing one identifier** — for the duplicate-id check

```markdown
<!-- broken/dup-a.md -->
---
type: specimen
id: SP-0900
label: Duplicate A
---
```

```markdown
<!-- broken/dup-b.md -->
---
type: specimen
id: SP-0900
label: Duplicate B
---
```

**An orphan and a broken ordinary wikilink** — these are notes, not records

```markdown
<!-- Notes/scratch.md -->  nothing links here, and it links nowhere
Just some text.
```

```markdown
<!-- Notes/journal.md -->
A note that points at [[A note that does not exist]].
```

**Folding and ordering records** — `/tmp/uat-vault-alpha/Tokens/`

Create six `token` notes with `word` set, one each, spelled in the **opposite case** to the
declared value: `STRASSE`, `ΣΊΣΥΦΟΣ`, `MÜLLER`, `ŁÓDŹ`, `file` (declared `ﬁle`), and
`İSTANBUL`. Give each a `label` holding the same string.

Create four more with `phase` set to `ember`, `azure`, `cinder`, `dune`, and four with
`ranked` set to `1-ember`, `2-azure`, `3-cinder`, `4-dune`.

Create three with `mood` set to `calm`, `Calm` and `CALM` respectively.

---

## Part B — `vault_describe`

*The mandatory cheap first call. An agent that has not called it is guessing at property names.*

### Case B-1 — Orientation on a vault with schemas

| Step | Do this | Expect |
|---|---|---|
| B-1.1 | Ask an agent to call `vault_describe` on Vault Alpha | A response listing: index freshness, the collections in scope, the four record types, saved views, templates |
| B-1.2 | Read the property table for `specimen` | Each property with its **type**, its arity (`many` or not), and whether it is required |
| B-1.3 | Look at how `count` and `mass_g` are rendered | `integer` and `decimal` appear as **two distinct types**. Not "number" for both, not "numeric" |
| B-1.4 | Look at the id prefix | `SP` is shown for `specimen`, `EX` for `expedition` |
| B-1.5 | Look at `condition`'s enum values | All four are listed |
| B-1.6 | Look at the whole response | It is **compact text**. No JSON object anywhere in it |

**Fail if:** `integer` and `decimal` are collapsed into one displayed type; the id prefix is
missing; the response is a JSON blob; or any record type, property or enum value the vault did
not declare appears in it.

### Case B-2 — The response must not imply an enum order

*Why this matters: enum ordering is **lexical**, not the order values are declared in
(Part E). A reader who infers a sort order from the sequence `vault_describe` prints will be
wrong, so the response has to say so.*

| Step | Do this | Expect |
|---|---|---|
| B-2.1 | Look at `token.phase`'s value list in the describe output | The response states, in words, that the set is unordered and that a sort order must not be inferred from the sequence shown |

**Fail if:** the values are printed as a bare list with nothing said about ordering.

### Case B-3 — Refusals name what is valid

| Step | Do this | Expect |
|---|---|---|
| B-3.1 | Call `vault_describe` with `record_type` = `specimne` (a typo) | A **refusal** listing the declared types: `expedition, keeper, specimen, token` |
| B-3.2 | Call `vault_describe` with `collection` = a name that is not mounted | A refusal listing the collections in scope |

**Fail if:** either returns an empty description, an empty success, or an error that does not
name the valid options.

### Case B-4 — `check_integrity` finds all six kinds of problem

| Step | Do this | Expect |
|---|---|---|
| B-4.1 | Call `vault_describe` with `check_integrity` = true | A findings block, grouped by kind, every finding naming a path |
| B-4.2 | Look for the **duplicate identifier** | `SP-0900` is reported naming **both** `broken/dup-a.md` and `broken/dup-b.md`, and stating that neither is preferred |
| B-4.3 | Look for the **unresolved relation** | `SP-0106`'s `[[Southern traverse]]` is named as resolving to nothing |
| B-4.4 | Look for the **wrong-type relation** | `SP-0105`'s expedition points at a note of type `specimen`, where `expedition` was declared |
| B-4.5 | Look for the **broken ordinary wikilink** | `Notes/journal.md` → `[[A note that does not exist]]`. This is a plain note, not a record — it must still be found |
| B-4.6 | Look for the **orphan** | `Notes/scratch.md` |
| B-4.7 | Count the sweep | The response states how many notes were swept and over what scope |

**Fail if:** any of the six kinds is missing; the duplicate report picks a winner; the ordinary
wikilink check is absent (that capability exists today under a different name and must not be
lost); or the response reports "0 findings" for a category it could not actually check.

### Case B-5 — `check_integrity` is bounded, and says so

| Step | Do this | Expect |
|---|---|---|
| B-5.1 | Run `check_integrity` scoped to `record_type` = `specimen` | Only specimen findings; the scope is stated |
| B-5.2 | If you have a very large folder, run it unscoped over that | Either it completes and states the sweep size, or it is **refused** naming the collection, the limit, and the scoped remedy |

**Fail if:** a sweep silently truncates, or a clamped category is shown without a "showing N of
M" line naming the real total.

### Case B-6 — What `check_integrity` cannot check, it names

*This one is only reachable if you have been given a build without the properties index
(`linux/mipsle`). If you have not, mark it **Blocked — no such build available**. Do not
approximate it.*

| Step | Do this | Expect |
|---|---|---|
| B-6.1 | Run `check_integrity` on such a build | It states **by name** which categories it could not run and why, and does **not** report zero findings for them |

---

## Part C — The seven property types

*There are exactly seven: `text`, `enum`, `relation`, `date`, `integer`, `decimal`, `person`.
These seven names are **ours** and are shipped. Every other name in this plan — `specimen`,
`condition`, `dry` — belongs to the vault. Keep the two straight; a product that ships a
record type is a defect, and a product that fails to ship a property type is a different one.*

### Case C-1 — `text`

| Step | Do this | Expect |
|---|---|---|
| C-1.1 | `vault_find` with `filter` = `{"property":"label","op":"LIKE","value":"%frond%"}` | `SP-0001` |
| C-1.2 | Same with `op` = `=` and `value` = `Bracken frond` | `SP-0001` |
| C-1.3 | Same with `value` = `bracken FROND` | `SP-0001` — text matching is case-insensitive |

### Case C-2 — `enum` is closed

| Step | Do this | Expect |
|---|---|---|
| C-2.1 | Ask `vault_edit` to set `condition` = `soggy` on `Specimens/fern.md` | **Refused**, naming the permitted values: `ambient, damp, dry, frozen` |
| C-2.2 | Check the file afterwards | Unchanged |
| C-2.3 | `vault_find` on `condition` = `soggy` | **Refused**, naming the permitted values — not an empty result |
| C-2.4 | Look at `SP-0103` (fixture `b3`, which already holds `soggy` on disk) | It is **reported as a bad value**, named, with the permitted set and the fix — it is not silently dropped, and it does not silently become a fifth permitted value |
| C-2.5 | Now run `vault_describe` again | `condition` still has exactly four values. `soggy` did **not** join them |

**Fail if:** an invented value is accepted, is auto-created as a new option, or causes the
record to vanish from results with no problem row.

### Case C-3 — `relation`

| Step | Do this | Expect |
|---|---|---|
| C-3.1 | `vault_read` `Specimens/fern.md` | `expedition` shows as a resolved link to `Northern sweep` |
| C-3.2 | `vault_read` `Expeditions/northern-sweep.md` | Its `specimens` inverse lists `SP-0001` and `SP-0002` |
| C-3.3 | Open `Expeditions/northern-sweep.md` in a text editor | The inverse is **not written into the file**. The file has no `specimens:` key |
| C-3.4 | `SP-0104` (relation stored as plain text) | Reported: text where a relation was declared, with the fix naming the wikilink form |
| C-3.5 | `SP-0105` (relation to a note of the wrong type) | Reported as wrong-type, not silently accepted |
| C-3.6 | `SP-0106` (relation to a note that does not exist) | Reported as unresolved, not rendered as a group of one |

**Fail if:** the inverse appears in the file on disk; or any of C-3.4 to C-3.6 is accepted
silently.

### Case C-4 — `date` is strict ISO, and never guessed

| Step | Do this | Expect |
|---|---|---|
| C-4.1 | `SP-0103` holds `collected_on: 03/04/2026` | **Reported** as ambiguous, with both readings named and the instruction to write `2026-04-03` or `2026-03-04`. Never guessed |
| C-4.2 | Ask `vault_edit` to write `collected_on` = `2026-9-1` | **Refused**, naming zero-padding and the corrected form `2026-09-01` |
| C-4.3 | `vault_find` with `collected_on` `>=` `2026-04-01` | `SP-0002` (2026-04-02) is in; `SP-0001` (2026-03-14) is out |
| C-4.4 | Ask for the same range written as `01/04/2026` | Refused the same way as C-4.1 |

**Fail if:** any date format outside `YYYY-MM-DD` is accepted, or an ambiguous one is silently
interpreted either way.

### Case C-5 — `integer` is int64, and overflow is refused

*Changed recently: the old `number` type is gone, split into `integer` and `decimal`.*

| Step | Do this | Expect |
|---|---|---|
| C-5.1 | `vault_find` with `count` `>` `5` | `SP-0001` (12). `SP-0002` (3) and `SP-0003` (0) are out |
| C-5.2 | Confirm `SP-0003` with `count: 0` is out of C-5.1 but **in** a query for `count` `IS NOT NULL` | Zero is a value, not an absence |
| C-5.3 | Ask `vault_edit` to set `count` = `9223372036854775808` | **Refused**, naming the bound `9223372036854775807` and suggesting `decimal` if the value is genuinely larger |
| C-5.4 | Read `SP-0107` (which already holds that value on disk) | Reported as a bad value naming the bound. **Not** silently saturated to `9223372036854775807`, and not widened to a decimal |
| C-5.5 | Query `count` `=` `9223372036854775807` | Does **not** match `SP-0107` |
| C-5.6 | Try declaring an `integer` property with `scale: 2` in a schema | Refused: an integer has no scale, and the message says to declare it `decimal` |

**Fail if:** an out-of-range integer is accepted at any layer, silently clipped to the bound,
or silently promoted to a decimal. Any of the three is a change to a number nobody asked for.

### Case C-6 — `decimal` is exact, and never rounded to fit

| Step | Do this | Expect |
|---|---|---|
| C-6.1 | `vault_read` `Specimens/lichen.md` | `mass_g` reads back **exactly** `12.345678901234567890123456789012` — every digit, no rounding, no `1.2345678901234568e+01` |
| C-6.2 | `vault_find` with `mass_g` `>` `1` | `SP-0001` (4.500) and `SP-0003` are in; `SP-0002` (0.125) is out |
| C-6.3 | Compare `mass_g` `=` `4.5` against the stored `4.500` | They match — trailing zeros do not change the number |
| C-6.4 | Look at how `4.500` renders in a result row | Rendered at the declared scale (3 places) |
| C-6.5 | Ask `vault_edit` to write a `mass_g` with **140 decimal places** | **Refused**, naming the 100-place bound **and the value's own scale**. Never rounded to fit |
| C-6.6 | `SP-0102` (`mass_g: "2.5kg"`) | Reported as a bad value: a unit glued to a number, with the fix naming what to write |
| C-6.7 | Compare an `integer` against a `decimal`: query `count` `=` `12.0` | Matches `SP-0001` (`count: 12`). The split decides storage, not a comparison domain |

**Fail if:** any decimal changes value between write and read; a value is rounded to satisfy a
bound instead of refused; or any rendered number shows floating-point artefacts.

### Case C-7 — `person`

| Step | Do this | Expect |
|---|---|---|
| C-7.1 | Create a `keeper` note and set `curator` on a specimen to point at it | Accepted |
| C-7.2 | `vault_read` the specimen | `curator` resolves to the keeper note, distinctly from a name typed as text |
| C-7.3 | Set `curator` to a bare name (`curator: Alex`) instead of a link | Reported: a person property is a link to a record, not a typed name |

*If the tool refuses `type: person` outright, or has no way to say **which** record type a
person resolves to, mark this **Blocked** and quote the message. The specification does not
settle that question and it may not be answerable yet.*

### Case C-8 — `money` no longer exists

*Why this matters: `money` was deleted from the design. Code implementing it is still in the
tree. A type that is half-removed is exactly the sort of thing that survives into a release.*

| Step | Do this | Expect |
|---|---|---|
| C-8.1 | Ask `vault_configure` to create a record type with a property declared `{ type: money }` | **Refused**, and the message lists the permitted types: `text, enum, relation, date, integer, decimal, person` |
| C-8.2 | Look at that list carefully | `money` is **not** in it. Neither is `number` |
| C-8.3 | Try `{ type: number }` | Refused the same way |
| C-8.4 | Try `{ type: decimal, unit: GBP }` | `unit` is not a schema key. Either the whole declaration is refused naming the permitted keys, **or** `unit` is ignored — but it must not behave as a currency |
| C-8.5 | Search the entire UI and every tool response you have collected for the words "currency", "ISO-4217", "GBP" or "minor units" | Nothing |
| C-8.6 | Run `vault_describe` and read the property-type list | Exactly seven types |

**Fail if:** `money` or `number` is accepted anywhere; a currency concept appears in any
response; or the type list has any count other than seven.

### Case C-9 — Arity is declared, and a scalar never silently becomes a list

| Step | Do this | Expect |
|---|---|---|
| C-9.1 | `SP-0108` holds `condition: [dry, damp]` on a scalar property | Reported: the property holds one value, got a list of 2, with the fix naming both options (send one value, or declare `many: true`) |
| C-9.2 | Ask `vault_edit` to write a list into `condition` | Refused the same way; the file is unchanged |
| C-9.3 | Ask `vault_edit` to write a list into `tags` (declared `many: true`) | Accepted |
| C-9.4 | Query `tags` `=` `fragile` | `SP-0001` — `=` matches an **element** of a list |
| C-9.5 | Query `tags` `IN` `["loaned","sealed"]` | `SP-0001` and `SP-0002` |
| C-9.6 | Query `tags` `>` `f` | **Refused**: ordering comparisons are not defined over a list, naming `=`, `IN` and `LIKE` as what to use instead |

**Fail if:** a scalar accepts a list; a `many` property refuses a list; or an ordering operator
over a list returns a result instead of a refusal.

### Case C-10 — Absent is a state of its own

*Why this matters: the "days I did not meditate" problem. A negative filter that quietly omits
every record with no value omits precisely the records being asked about.*

`SP-0003` has **no** `condition`. Use it throughout.

| Step | Do this | Expect |
|---|---|---|
| C-10.1 | `filter` = `{"property":"condition","op":"IS NULL"}` | `SP-0003` is returned |
| C-10.2 | `filter` = `{"property":"condition","op":"IS NOT NULL"}` | `SP-0001`, `SP-0002`; `SP-0003` is **not** there |
| C-10.3 | `filter` = `{"not":{"property":"condition","op":"=","value":"dry"}}` | `SP-0003` **is included** — a negative *tree* includes the absent |
| C-10.4 | `filter` = `{"property":"condition","op":"<>","value":"dry"}` | `SP-0003` is **not** included — a `<>` *leaf* does not match an absent property |
| C-10.5 | Read C-10.3 and C-10.4 together | They deliberately differ. If they behave the same, one of them is wrong |
| C-10.6 | Nest it: `{"not":{"all":[{"property":"condition","op":"=","value":"dry"},{"property":"count","op":">","value":5}]}}` | `SP-0003` is still included. The rule holds at depth, not only at the top leaf |

**Fail if:** C-10.3 and C-10.4 give the same answer; or C-10.6 drops the record with the absent
property while C-10.3 kept it.

### Case C-11 — Property types are scoped to their record type

| Step | Do this | Expect |
|---|---|---|
| C-11.1 | Note that `specimen.label` and `expedition.label` are separate declarations | — |
| C-11.2 | Ask `vault_configure` to change `expedition.label` to an `enum` | Accepted (it is that type's own declaration) |
| C-11.3 | `vault_describe` afterwards | `specimen.label` is **still `text`**. It did not follow |

**Fail if:** changing a property on one record type changes the same-named property on another.
That is the vault-wide property binding this design exists to avoid.

---

## Part D — Unicode case folding

**Read this whole preamble before running any case in this part.**

Matching text, enum values and relation **paths** is **case-insensitive**, in every alphabet —
not only in English. This is a live operator requirement, and it is the single easiest thing in
this design to get subtly wrong: the obvious implementations fold English correctly and fold
German, Greek and Turkish incorrectly, in three different ways.

**Two of these cases are NEGATIVES.** They are cases where the correct behaviour is *not to
match*. If you see `istanbul` and `İSTANBUL` treated as different words, **that is correct and
you must not report it as a folding gap.** The dotted `İ` and the plain `i` are different
letters in Turkish; collapsing them is a real bug that has broken real software, and this plan
asserts the correct answer explicitly so nobody "fixes" it.

Use the `token` fixture notes. For each pair, run the query with the **left** spelling and
check whether the note holding the **right** spelling comes back.

### Case D-1 — The six literal pairs

| # | Query for | Note holds | Expected | Why this pair is here |
|---|---|---|---|---|
| D-1.1 | `straße` | `STRASSE` | **MATCH** | German `ß` folds to `ss`. This is the cell that fails if anyone reaches for a standard-library shortcut |
| D-1.2 | `σίσυφος` | `ΣΊΣΥΦΟΣ` | **MATCH** | Greek final sigma — the pair where the two obvious implementations disagree with *each other* |
| D-1.3 | `müller` | `MÜLLER` | **MATCH** | German umlaut. The ordinary case; everything gets this right |
| D-1.4 | `łódź` | `ŁÓDŹ` | **MATCH** | Polish. The control — a test containing only rows like this one proves nothing |
| D-1.5 | `istanbul` | `İSTANBUL` | **MUST NOT MATCH** | **Turkish dotted `İ`. Different letters. A match here is the bug.** Do not report a non-match as a finding |
| D-1.6 | `file` | `ﬁle` | **MATCH** | The `fi` ligature. A second, independent witness that simple folding is not enough |

Run each of these **twice**: once against a `text` property (`label`) and once against an
`enum` property (`word`). Both must give the same six answers.

**Fail if:** any of D-1.1, D-1.2, D-1.3, D-1.4 or D-1.6 fails to match — **or** if D-1.5
matches.

**A diagnostic worth recording rather than diagnosing:** if D-1.3 and D-1.4 pass while D-1.1
and D-1.6 fail, say exactly that in your report — "the two full-folding pairs failed, the two
simple pairs passed". That sentence is worth more to the implementer than any theory, and it
costs you nothing to write it.

### Case D-2 — Folding applies to enum resolution, not just search

| Step | Do this | Expect |
|---|---|---|
| D-2.1 | Ask `vault_edit` to write `word` = `STRASSE` on a token note | **Accepted.** It resolves to the declared value `straße` |
| D-2.2 | Look at the file on disk | It says `STRASSE` — the file keeps its own spelling |
| D-2.3 | `vault_describe` afterwards | `word` still declares six values. `STRASSE` did **not** become a seventh |
| D-2.4 | Ask `vault_edit` to write `word` = `strasse-x` | **Refused**, naming the permitted values |

**Fail if:** a case variant is refused (that is the bug this rule exists to prevent), or is
accepted *as a new value* (that is the opposite bug, and it is worse).

### Case D-3 — One value, one group, one place in a sort

| Step | Do this | Expect |
|---|---|---|
| D-3.1 | `vault_find` on `type` = `token`, `group_by` = `mood` | **One** group, containing all three of `calm`, `Calm` and `CALM` |
| D-3.2 | Same query, `sort` by `mood` | The three sit **together**, not scattered with capitals first |
| D-3.3 | Look at how each row renders | Each shows the spelling its own file uses |

**Fail if:** three groups appear where one value was declared; or grouping collapses them into
one group while sorting scatters them into three places. Those two answers disagreeing is the
finding, and it is worth reporting even if each half looks reasonable alone.

### Case D-4 — `LIKE` folds the pattern's letters, not its wildcards

*This one is fiddly and it is here because it is exactly the behaviour a tester would otherwise
read off whatever the implementation happened to do. `straße` is six characters and folds to
`strasse`, which is seven. `_` matches exactly one character **of the folded form**.*

| Step | Do this | Expect |
|---|---|---|
| D-4.1 | `label` `LIKE` `stra_e` against the note holding `straße` | **No match** (the folded form has two characters where the pattern has one) |
| D-4.2 | `label` `LIKE` `stra__e` against the same note | **Match** |
| D-4.3 | `label` `LIKE` `%ÄCM%` against a note holding `äcme` | Match — patterns fold too |
| D-4.4 | `label` `LIKE` `100\%` against a note holding `100%` | Match — the backslash escapes the wildcard |

### Case D-5 — Record identifiers are matched exactly

*Paths fold. Identifiers do not — because two legitimately distinct identifiers that fold
together would collide into one, which is data loss for a case nobody chose.*

| Step | Do this | Expect |
|---|---|---|
| D-5.1 | Create a second specimen with `id: sp-0001` (lower case), alongside the existing `SP-0001` | They are treated as **two different identifiers** |
| D-5.2 | `check_integrity` | They are **not** reported as a duplicate identifier — they are not duplicates |
| D-5.3 | A relation written as `[[bracken frond]]` (lower case path) | **Resolves** to `Bracken frond` — paths *do* fold |

**Fail if:** `SP-0001` and `sp-0001` are treated as one identifier, or a lower-case wikilink
path fails to resolve.

---

## Part E — Enum ordering is lexical

**This changed.** Enums used to sort in the order their values were declared. They now sort
**lexically** — alphabetically, by the folded form. A vault that wants a domain order writes
the order into the values as a prefix. Both behaviours are tested here, because one is the
mechanism and the other is the convention the mechanism forces.

The `token` fixture declares `phase` in the order `ember, azure, cinder, dune`, whose lexical
order is `azure, cinder, dune, ember`. They are deliberately different.

### Case E-1 — An unprefixed enum sorts lexically

| Step | Do this | Expect |
|---|---|---|
| E-1.1 | `vault_find` `type` = `token`, `sort` by `phase` ascending | `azure, cinder, dune, ember` |
| E-1.2 | Compare that with the declaration order | It is **not** `ember, azure, cinder, dune`. That is correct |
| E-1.3 | `filter` `phase` `>=` `cinder` | `cinder, dune, ember` — and **not** `azure`, even though `azure` was declared second |

**Fail if:** the sort follows declaration order. **Do not report E-1.2 or E-1.3 as a bug** —
this is the specified behaviour, and it is the reason Case E-2 exists.

### Case E-2 — A domain order is a prefix

| Step | Do this | Expect |
|---|---|---|
| E-2.1 | `vault_find` `type` = `token`, `sort` by `ranked` ascending | `1-ember, 2-azure, 3-cinder, 4-dune` |
| E-2.2 | `filter` `ranked` `>=` `3-cinder` | `3-cinder` and `4-dune` only |

**Fail if:** prefixing does not produce the intended order. This is the entire escape hatch for
domain ordering; if it does not work, there is no way to express one.

### Case E-3 — Ordering agrees with grouping and with equality

| Step | Do this | Expect |
|---|---|---|
| E-3.1 | Sort by `mood` across the three case variants (Case D-3) | They sit together |
| E-3.2 | Group by `mood` | One group |
| E-3.3 | Filter `mood` `=` `CALM` | All three |

**Fail if:** any two of these three disagree about how many distinct values `mood` has.

### Case E-4 — Nothing anywhere implies a declared order

| Step | Do this | Expect |
|---|---|---|
| E-4.1 | Re-read `vault_describe`'s enum output (Case B-2) | It says the set is unordered |
| E-4.2 | Look at any UI that renders an enum — a filter dropdown, a group header, a cell editor | Either lexical order, or an explicit statement of what order it is showing. Never declaration order presented as *the* order |

---

## Part F — `vault_find`: the operators, the refusals, the response

*The operators are SQL's own: `=`, `<>`, `<`, `<=`, `>`, `>=`, `LIKE`, `IN`, `IS NULL`,
`IS NOT NULL`, each carrying SQL's meaning. Anything else is refused. There is no query
language and no parser — filters are structured objects.*

### Case F-1 — `=` is exact

| Step | Do this | Expect |
|---|---|---|
| F-1.1 | `condition` `=` `dry` | `SP-0001` only |
| F-1.2 | `condition` `=` `DRY` | The same result (case-insensitive, Part D) |
| F-1.3 | `label` `=` `Bracken` | **No match** — `=` is exact, not partial. `Bracken frond` does not match `Bracken` |
| F-1.4 | `tags` `=` `sealed` on the `many` property | `SP-0001` — element-wise |

**Fail if:** `=` behaves as a substring match. That would make F-1.3 return a row, and would
make `LIKE` redundant.

### Case F-2 — `LIKE` is partial, with `%` and `_`

| Step | Do this | Expect |
|---|---|---|
| F-2.1 | `label` `LIKE` `%frond%` | `SP-0001` |
| F-2.2 | `label` `LIKE` `Bracken%` | `SP-0001` |
| F-2.3 | `label` `LIKE` `%frond` | `SP-0001` |
| F-2.4 | `label` `LIKE` `Bracken` (no wildcard) | Whatever the response says it means — record it. Then check the tool's own description says the same thing |
| F-2.5 | `label` `LIKE` `` (empty) | **Refused**: an empty pattern matches everything; the message names `IS NOT NULL` as what you probably meant |
| F-2.6 | `label` `LIKE` `%` (bare) | Refused the same way |

**Fail if:** an empty or bare-`%` pattern silently returns every record. A query that matches
everything by accident is the failure mode this refusal exists for.

### Case F-3 — `IN` is membership

| Step | Do this | Expect |
|---|---|---|
| F-3.1 | `condition` `IN` `["dry","damp"]` | `SP-0001` and `SP-0002` |
| F-3.2 | `condition` `IN` `["dry","soggy"]` | **Refused** — `soggy` is not a declared enum value — naming the permitted set. Not a partial result |
| F-3.3 | `IN` with a single-element list | Behaves as `=` |
| F-3.4 | `IN` with an empty list | Refused or returns nothing, with the response saying which. Record the answer |

### Case F-4 — `IS NULL` and `IS NOT NULL`

Covered by Case C-10. Additionally:

| Step | Do this | Expect |
|---|---|---|
| F-4.1 | A note with `label: ""` (an empty string) | `IS NOT NULL` **returns it** — an empty string is a value |
| F-4.2 | A note with `tags: []` (an empty list) | `IS NOT NULL` returns it |
| F-4.3 | `SP-0003` with `count: 0` | `IS NOT NULL` returns it |

**Fail if:** any of the three is treated as absent. "Empty" and "absent" are different states.

### Case F-5 — Ordering comparisons

| Step | Do this | Expect |
|---|---|---|
| F-5.1 | `count` `>` `5`, `>=` `12`, `<` `3`, `<=` `0` | Correct on each |
| F-5.2 | `mass_g` `>` `1` | `SP-0001`, `SP-0003` |
| F-5.3 | `collected_on` `>=` `2026-04-01` | `SP-0002` |
| F-5.4 | `tags` `>` `f` (a `many` property) | **Refused**, naming `=`, `IN` and `LIKE` |
| F-5.5 | Compare a text value with `>` against a number, e.g. `label` `>` `5` | Whatever happens, it must not silently return everything or nothing. Record the exact response |

### Case F-6 — Unsupported SQL constructs are refused by name

*This is the one that must never return an empty result. A model that asked for something we do
not support has to be told what we do support, in the same message.*

| Step | Do this | Expect |
|---|---|---|
| F-6.1 | `op` = `JOIN` | Refused, naming the **`join` parameter** as what does that job |
| F-6.2 | `op` = `COALESCE` | Refused, listing the ten supported operators |
| F-6.3 | `op` = `BETWEEN` | Refused, listing the supported operators **and** telling you to express a range as two leaves, `>=` and `<=` |
| F-6.4 | `op` = `CASE` | Refused, listing the supported operators |
| F-6.5 | A nested `SELECT` as a value | Refused, not parsed, not evaluated |
| F-6.6 | Free text as the whole filter, e.g. `"condition = 'dry' AND count > 5"` | Refused — there is no query language. The message says the filter is a structured object |

**Fail if:** any of these returns an empty success, a partial result, or an error that does not
name the supported set. **An empty result to an unsupported operator is the single worst
outcome in this plan** — it is a wrong answer with no error channel, which is the failure the
whole design exists to remove.

### Case F-7 — Unknown names are refused, with the valid ones

| Step | Do this | Expect |
|---|---|---|
| F-7.1 | `filter` on property `conditon` (typo) | Refused: `unknown property 'conditon' on record type 'specimen'`, followed by the declared property names |
| F-7.2 | `type` = `speciman` (typo) | Refused, listing the declared record types |
| F-7.3 | `sort` by an unknown property | Refused, listing the declared properties |
| F-7.4 | `group_by` an unknown property | Refused, listing the declared properties |
| F-7.5 | `join` on a relation name that does not exist | Refused, listing the declared relations |
| F-7.6 | An argument name the tool does not declare, e.g. `sort_by` instead of `sort` | Refused, listing the accepted argument names |

**Fail if:** any of these returns zero results. A typo must be a rejection naming the valid
options, never an empty answer — because an empty answer to a typo looks exactly like an empty
answer to a correct query.

### Case F-8 — `near` and `hops`

| Step | Do this | Expect |
|---|---|---|
| F-8.1 | `near` = `Expeditions/northern-sweep.md`, `hops` = 1 | The notes directly linked to it |
| F-8.2 | `hops` = 2 | A wider set, still bounded |
| F-8.3 | `hops` = 3 | **Refused**: the limit is 2, with the instruction to run a second query from one of the results |
| F-8.4 | `near` + `words` = `pricing` + a filter on `condition` = `dry` | The **intersection**: a note inside the hop radius that fails the filter is absent, and one matching the filter but outside the radius is absent |

**Fail if:** F-8.4 returns a union rather than an intersection. Composing text search with graph
traversal is the capability this tool is for; a union quietly makes it useless.

### Case F-9 — `join` marks borrowed values as borrowed

| Step | Do this | Expect |
|---|---|---|
| F-9.1 | `type` = `specimen`, `join` = `["expedition"]` | Each row carries the expedition's properties, rendered **as borrowed** — e.g. `expedition [[Northern sweep]]: region northern` |
| F-9.2 | Look at the row | The borrowed value is **not** merged into the specimen's own columns |

**Fail if:** a borrowed value is indistinguishable from a property the note itself holds. That
is how an agent comes to believe a property exists on a note that does not have it.

### Case F-10 — Grouping

| Step | Do this | Expect |
|---|---|---|
| F-10.1 | `group_by` = `["condition"]` | Groups by condition |
| F-10.2 | `group_by` = `["expedition","condition"]` | **Two levels**, nested |
| F-10.3 | `group_by` on `tags` (a `many` property) | `SP-0001`, which holds `fragile` and `sealed`, appears under **both** groups |
| F-10.4 | `group_by` with three properties | Refused, naming the two-level limit |
| F-10.5 | `group_by` on a **relation** | Supported — grouping by a relation is not a degraded case |

### Case F-11 — Totals state their scope, and count each record once

| Step | Do this | Expect |
|---|---|---|
| F-11.1 | `aggregate` = `[{"op":"sum","property":"mass_g"}]` over all specimens | A total that **states the set it covers** — e.g. "over 14 of 14 evaluated rows (12 shown)". Never a bare number |
| F-11.2 | Run the same query with `limit` = 2 | The total is **unchanged**. It covers the full evaluated set, not the page you can see |
| F-11.3 | Add a `many` property to the filter so a record matches on **two** of its values (`tags` `IN` `["fragile","sealed"]`) | `SP-0001` is counted **once**, and its mass contributes **once** |
| F-11.4 | Now add a second matching tag to `SP-0002` and re-run | The count and sum change by exactly one record's worth, not two |
| F-11.5 | `count`, `min`, `max` | Same rules |
| F-11.6 | Aggregate over a set that includes the broken records | Every excluded record is **named**, and no combined figure is offered for a set that could not be fully evaluated |

**Fail if:** the total changes when you change `limit` (that means it is page-scoped and is a
wrong answer to the question being asked); or a record matching twice is counted twice.

### Case F-12 — `explain` evaluates nothing

| Step | Do this | Expect |
|---|---|---|
| F-12.1 | Any query with `explain` = true | A **plan**: every property the query touches and which index answers it. No records |
| F-12.2 | Run the identical call twice | The two responses are **identical, character for character** — including any freshness or epoch line |
| F-12.3 | Now add three new notes to the vault, let them index, and run it a third time | Still identical to the first two |

**Fail if:** the two explain calls differ in any character, or the response includes result
rows. A plan that changes with the corpus means evaluation is happening.

### Case F-13 — `kind: task`

| Step | Do this | Expect |
|---|---|---|
| F-13.1 | Put a note in Alpha with three checkbox lines, one of them ticked | — |
| F-13.2 | `vault_find` with `kind` = `task` | Rows carrying the path, the **line number**, the status (open/done) and the text |
| F-13.3 | Look at a task row beside an ordinary note row | You can tell them apart. The line number is always shown |

**Fail if:** a task row is indistinguishable from a note row, or a task row omits its line
number — many rows come from one file and a reader must never mistake one for the other.

### Case F-14 — Nothing found is an answer, not a guess

| Step | Do this | Expect |
|---|---|---|
| F-14.1 | `words` = a term that appears nowhere, close to one that does (`speciman`, `bracken frnod`) | Zero results, **plus the nearest indexed terms** the vault actually holds |
| F-14.2 | Compare what you asked with what came back | The system did **not** broaden your query and answer a different question |

**Fail if:** a zero-hit query silently returns results for a broader or corrected term. That is
answering a question nobody asked, with no error channel.

### Case F-15 — Paging and clamps

| Step | Do this | Expect |
|---|---|---|
| F-15.1 | `limit` = 500 | Clamped to 200, and **the clamp is stated in the response** |
| F-15.2 | Page through with the cursor the response gives you | Consistent, non-overlapping pages |
| F-15.3 | Take a cursor, add several notes, let them index, then use the old cursor | An **error** naming the stale cursor and telling you to re-run — never a silent restart from page one |

### Case F-16 — The candidate caps

*These need a vault of tens of thousands of records. If you do not have one, mark **Blocked —
fixture too small** and say so; do not approximate.*

| Step | Do this | Expect |
|---|---|---|
| F-16.1 | A row-returning query matching more than 10,000 records | **Refused**, telling you to tighten the filter or ask for a total instead. It does not quote an exact survivor count it never finished computing |
| F-16.2 | A typed query whose candidate population exceeds 50,000 | Refused, quoting the **exact** candidate count and naming the scope/kind remedy |
| F-16.3 | An aggregate-only query (no rows requested) over the same large set | Permitted — it is exempt from the row cap |
| F-16.4 | An aggregate over a refused set | **No total at all**. Never a partial one |

### Case F-17 — The shape of the response

*Every `vault_find` response, in every case above, must have this shape. Check it once
carefully here, then spot-check it throughout.*

| Step | Do this | Expect |
|---|---|---|
| F-17.1 | Look at the **first line** | The completeness verdict. Not the last line, not buried after the rows |
| F-17.2 | Look for the query echo | The query you sent, rendered readably |
| F-17.3 | Look for the freshness line | Whether the returned records agree across both indexes |
| F-17.4 | Look at the rows | Compact text. **No JSON object anywhere in what the model reads** |
| F-17.5 | Look for the totals line, if you asked for one | Present, with its scope stated |
| F-17.6 | Look for the problems block | Present whenever anything was excluded, each entry naming the record, the reason **and the fix** |
| F-17.7 | Look at the **last** block | Addressable next actions — what to narrow by, what to try next |
| F-17.8 | Ask for a very large result and watch the response length | If it is truncated, the truncation is stated in the header. Never silent |

**Fail if:** completeness arrives after the rows; the response is JSON; a problem row says only
"3 records excluded" without naming them; or there is no next-actions block.
