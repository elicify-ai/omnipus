# UAT test plan — vault records (ADR-068)

**Branch:** `feat/library-improvements`
**Date:** 2026-08-28
**Covers:** ADR-068 revision 11; `vault-records-spec-2026-08-25.md` Draft 9;
`vault-trash-convention-2026-08-28.md`
**Audience:** a human tester with the app in front of them. No Go, no terminal beyond
starting the binary, no reading of source code.
**Previous round:** `uat-library-records-2026-08-26.md` and its results
`uat-results-2026-08-27.md`. Every defect that round found is carried forward here as a
numbered case, because those are the ones that recur.

**Extended, later the same day.** Parts 0–O below are unchanged from the version this plan
started as (ADR-068 revision 8 / Draft 6) except for a small number of new cases inserted at
the end of an existing part — B-7, B-8, F-10.6, F-18, F-19 and I-9 — each added because the
ADR or spec moved between revision 8 and revision 11 in a way the original cases did not
cover. **Part P is entirely new.** It extends Case J-3 (which stays as written and still
passes/fails on its own terms) into the full trash convention that landed today in
`vault-trash-convention-2026-08-28.md`. Two of its cases, P-8 and P-9, are **regression checks**
for a data-loss defect the convention document reported as live and unfixed — it was found and
fixed later the same day, and their expected result is a plain refusal with nothing moved; read
Part P's own preamble for the one detail that matters (creating the destination directory first,
so the case exercises the real guard rather than an unrelated, incidental refusal). Nothing
already in Parts 0–O was removed, renumbered or reworded.

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
| `vault_restructure` present | + Part J, and P-1/P-2/P-7/P-8/P-9 of Part P (the `trash` half — P-8 and P-9 are runnable as soon as `move` and `trash` exist, even before `restore` does) |
| `vault_restructure` present, and its `restore` op specifically | + the rest of Part P (P-3 through P-6, P-11) |
| `vault_configure` present | + Part K, Part L |
| A record table or health view in Library | + Part N |

**Part P-10 (the retention purge) has no routing row above and is not unlocked by any tool
existing.** It is design intent with no built mechanism yet — see its own preamble. Do not mark
it Blocked; mark it **NOT-YET-TESTABLE**, which is a different, deliberate label used nowhere
else in this plan.

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

### Case B-7 — The sixth kind: an index row with no note behind it

*Why this matters: Case B-4 names five kinds of finding, but the design commits to **six** —
duplicate identifiers, unresolved relations, wrong-type relations, broken wikilinks, orphan
**notes**, and **rows in the properties index with no note behind them**. That sixth kind is
easy to lose because its name is one word away from a kind you already checked in B-4.6 — read
the next paragraph before you run this, so you do not mark the two the same thing.*

**Do not confuse this with B-4.6.** An **orphan note** (B-4.6) is a real file — `Notes/scratch.md`
exists on disk, and nothing links to it. An **orphan row** is the opposite direction of wrong: a
row still sitting in the internal properties table for a file that **no longer exists at all**.
One is a note with no neighbours; the other is bookkeeping for a note that is gone. If your report
says "orphan" for both without naming which, that is not a usable report.

| Step | Do this | Expect |
|---|---|---|
| B-7.1 | With Vault Alpha fully indexed, note `SP-0002`'s path (`Specimens/moss.md`) | — |
| B-7.2 | Using your **file manager**, not any Omnipus tool, delete `Specimens/moss.md` from disk | The file is gone |
| B-7.3 | **Immediately** — before waiting long enough for an ordinary re-index to notice — call `check_integrity` | Either the row has already been reconciled away (re-index was faster than you), **or** `SP-0002` is reported under a finding naming it a properties-index row with no note behind it |
| B-7.4 | If B-7.3 caught it: wait a short while and run `check_integrity` again | The finding is gone. A note deleted outside the app is exactly the case this finding exists to catch, and it self-heals once the ordinary sync catches up |
| B-7.5 | Compare the wording of this finding against the B-4.6 orphan-note finding | The two use **visibly different language** — a reader must be able to tell "this file exists and nothing points to it" apart from "this row exists and no file backs it" without cross-referencing this plan |

**Fail if:** the finding never appears at all (try again — this is a timing-sensitive case, and
say so if it takes more than two or three attempts); the finding is worded identically to an
orphan-note finding; or the row survives after the vault has clearly had time to reconcile.

*If you cannot reproduce this after several attempts, mark it **Blocked — could not force the
condition** and say how many attempts and how long you waited between them. Do not report a pass
you could not produce.*

### Case B-8 — Telling a clamp from a refusal

*Why this matters: `check_integrity` has two different bounds and two different words for what
happens when each is hit. **A per-category clamp still answers** — it shows what it found and
says how much more there was. **A sweep-size refusal answers nothing** — it declines the whole
sweep and tells you how to scope it down. A tester who cannot tell the two apart will report a
refusal as "it only found some of the problems" (wrong — it found none, on purpose) or a clamp as
"it silently gave up" (also wrong — it told you). Read this table before running either half.*

| | Per-category clamp | Whole-sweep refusal |
|---|---|---|
| Trigger | more than 500 findings in one category (e.g. `broken link`) | more than 100,000 notes in the scope being swept |
| What comes back | the findings it has, up to 500 of them, **plus** the true total and the scope that would narrow it | **no findings at all** for any category, plus the note count it stopped at and the scope argument to use instead |
| How to tell them apart in the response | a "showing 500 of N" line sits **next to real findings** | there are **no findings to look at** — the whole response is the refusal |

| Step | Do this | Expect |
|---|---|---|
| B-8.1 | If you have a vault with more than 500 genuine problems of one kind, run `check_integrity` over it | A clamped response matching the left column above, with the "showing 500 of N" line naming the real total |
| B-8.2 | If you have a vault (or a scope argument) with more than 100,000 notes, run `check_integrity` unscoped over it | A refusal matching the right column above — zero findings shown, the note count named, and the scope argument that would let it complete |
| B-8.3 | Whichever of B-8.1 / B-8.2 you could run, re-read the response and confirm which column it matches | It matches exactly one column, not a blend of both |

**Fail if:** a refusal response includes any findings at all (that would make it a clamp wearing a
refusal's wording); or a clamped response omits the "showing N of M" line, leaving you unable to
tell whether you are looking at everything or a fraction of it.

*Neither bound is reachable by typing fixture files by hand — 500 broken records or 100,000 notes
is bulk-generation territory. If you do not have a large fixture, mark this **Blocked — fixture
too small** for whichever half you could not run, and say so for each half separately: it is
entirely possible to have one and not the other.*

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
| F-10.6 | Make F-10.5 concrete: `group_by` = `["expedition"]` over `type` = `specimen` | **Two** groups: `Northern sweep` holding `SP-0001` and `SP-0002`; `Coastal survey` holding `SP-0003`. Not a bare "supported" — the actual membership matches the fixture |

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

### Case F-18 — Group totals can legitimately add up to more than the records you matched

*Read this before you run it, or you will file it as a bug. A record with several values in a
`group_by` property appears in **every** group it belongs to (Case F-10.3). The direct
consequence — stated here explicitly because nobody reading a total-vs-total mismatch would
otherwise guess it — is that the **sum of the group counts can be larger than the number of
records the query matched**. That is correct. It is not the same claim as F-11's rule, which says
a record contributes to any **one** group's total only once; this case is about **across**
groups, not within one.*

| Step | Do this | Expect |
|---|---|---|
| F-18.1 | `vault_find` `type` = `specimen`, **no filter** | The three clean records: `SP-0001`, `SP-0002`, `SP-0003` |
| F-18.2 | Count how many of them have a `tags` value at all | Two — `SP-0001` (`fragile`, `sealed`) and `SP-0002` (`loaned`). `SP-0003` has none |
| F-18.3 | `vault_find` `type` = `specimen`, `group_by` = `["tags"]` | Three groups: `fragile` (1), `sealed` (1), `loaned` (1) |
| F-18.4 | Add the three group counts together | **3** |
| F-18.5 | Compare that to F-18.2's count of **2** records that actually hold a `tags` value | They do **not** match, and that is correct — `SP-0001` was counted once in `fragile` and once again in `sealed`. It is the same record, counted in two places, not two records |

**Do not report F-18.4 disagreeing with F-18.2 as a bug.** It is the direct and required
consequence of F-10.3. **Fail this case only if** the two group counts (`fragile` and `sealed`)
do **not** both include `SP-0001`, or if any single group's own count is wrong — i.e. if
`fragile`'s count is anything other than 1.

### Case F-19 — A filter this complicated is refused, not evaluated

*A filter tree is itself an unbounded input — nothing stops a caller from nesting hundreds of
conditions — so it carries its own limit, separate from every other bound in this plan: **64
leaf conditions, or 8 levels of nesting**, whichever is hit first.*

| Step | Do this | Expect |
|---|---|---|
| F-19.1 | Build a filter with **65** leaves under one `"all"` — the simplest way is 65 copies of `{"property":"count","op":">","value":-1}` joined by `"all"` | **Refused**, naming the 64-leaf bound and the count your filter actually reached (65) |
| F-19.2 | Build a filter nested **9** levels deep (each level a single-child `"all":[ ... ]` wrapping the next), with one real leaf at the bottom | **Refused**, naming the depth bound (8) and the depth reached |
| F-19.3 | Now trim F-19.1 to exactly 64 leaves | Accepted and evaluated normally |
| F-19.4 | Trim F-19.2 to exactly 8 levels | Accepted and evaluated normally |

**Fail if:** either oversized filter is silently evaluated (slowly or otherwise) instead of
refused; the refusal does not name which of the two bounds was exceeded; or a filter at exactly
the stated bound (F-19.3, F-19.4) is refused. The boundary itself must work — this plan does not
want a bound that is actually 63 or 7 because of an off-by-one.

---

## Part G — The honesty contract

*Every answer states whether it is complete and names what it left out, with the fix. This is
the requirement the whole design exists to serve, so it gets its own part rather than being
checked in passing.*

### Case G-1 — A bad value is reported, not dropped

| Step | Do this | Expect |
|---|---|---|
| G-1.1 | `vault_find` `type` = `specimen` with **no filter** | The clean records come back, **and** every broken fixture record appears in the problems block |
| G-1.2 | Read each problem entry | It names the record, the property, **the offending value**, and **the fix** — e.g. `SP-0102: mass_g is '2.5kg' where a decimal is required — write 2.5` |
| G-1.3 | Read the completeness line | It says the answer is **not** complete, and says so **first** |
| G-1.4 | Count | The number of records shown plus the number reported as problems accounts for every record you know is in the vault |

**Fail if:** a broken record is silently missing from both the rows and the problems; the
problem entry states the error without the fix; or the answer claims to be complete.

*This is the difference between "the query returned nothing, keep trying different spellings
until something comes back" — which is the accepted debugging technique in the products this
design is reacting to — and a system that tells you what went wrong.*

### Case G-2 — The same bad values appear in the vault health view

*Two surfaces, one truth. The answer is for the machine, at the moment of the wrong answer. The
health view is for the person, so they can clear a vault in one sitting instead of meeting its
problems one query at a time.*

| Step | Do this | Expect |
|---|---|---|
| G-2.1 | Open the vault health / problems view in the Library UI | It exists, and lists bad values **vault-wide**, grouped by record type and by reason |
| G-2.2 | Each row | Names the note path, the property, the offending value and the fix |
| G-2.3 | Compare against Case G-1's problem list | Every problem the query reported also appears here. (The health view may hold **more** — it sweeps the whole vault where a query only checks what it returned. More is correct; missing is not) |
| G-2.4 | Find a bad value in a property **no query in this plan has touched** | It is in the health view anyway |

**Fail if:** there is no health view (**Blocked — feature absent**); or a problem appears in a
query's answer and not in the health view.

### Case G-3 — Fixing the note clears both surfaces, with no extra step

| Step | Do this | Expect |
|---|---|---|
| G-3.1 | Correct `SP-0102`'s `mass_g` to `2.5` in a text editor | — |
| G-3.2 | Wait for re-indexing | — |
| G-3.3 | Re-run Case G-1's query and re-open the health view | The problem is gone from **both**, with no acknowledge/dismiss step anywhere |

**Fail if:** the health view keeps state of its own — a dismissed, acknowledged or snoozed
finding. The note is the source of truth; a health view holding its own state will drift from
the vault and start lying.

### Case G-4 — A total over a set with bad values

| Step | Do this | Expect |
|---|---|---|
| G-4.1 | `aggregate` `sum` over `mass_g` across all specimens including the broken ones | Every unusable record is **named**, and **no combined figure** is returned for the set |
| G-4.2 | Now scope the query to the clean records only | A figure, with its scope stated |

**Fail if:** a total is returned over a set that could not be fully evaluated. A number computed
over an unknown subset is the confidently-wrong answer this design is built to prevent.

### Case G-5 — Long problem lists are clamped honestly

| Step | Do this | Expect |
|---|---|---|
| G-5.1 | Create enough broken records that the problem list must be trimmed (a few dozen) | — |
| G-5.2 | Run a query over them | The list is trimmed **and** carries a line saying how many there really are — "showing 20 of 47" |
| G-5.3 | Check the "showing N of M" line is itself never dropped | It is always there when trimming happened |

**Fail if:** the list is trimmed silently. "47 records were excluded, 20 named" is a different
and more alarming verdict than "20 records were excluded", and collapsing them is exactly the
silent truncation this design refuses to ship.

### Case G-6 — Two indexes disagreeing is reported, not hidden

*The properties index and the text index can be at different generations of the same note. When
they are, the answer must say so.*

| Step | Do this | Expect |
|---|---|---|
| G-6.1 | Edit a specimen note in a text editor while the app runs, then immediately query for it | Either the answer is up to date, **or** the record is named as being mid-re-index, with the answer marked incomplete |
| G-6.2 | Repeat a few times in quick succession | Never a **confident, complete** answer over the old value |
| G-6.3 | Wait, re-run | The answer settles to the new value and reports complete |

*Forcing a genuine divergence by hand is not reliably possible. If you cannot get either
outcome to appear, mark this **Blocked — could not force the condition** and say what you tried.
Do not report a pass you could not produce.*

### Case G-7 — The one honest exception: another workspace's vault

*Read this before running it. There is exactly **one** case where the completeness verdict does
not name what it excluded, and it is deliberate.*

| Step | Do this | Expect |
|---|---|---|
| G-7.1 | Mount a vault into workspace B only | — |
| G-7.2 | From an agent in workspace A, query it | **Zero records, and the answer says it is complete.** No permission error, no hint that anything was withheld |
| G-7.3 | Compare with querying a genuinely empty vault | You **cannot tell them apart**. That is the requirement |

**This is correct. Do not report it as a bug.** Naming what was withheld would let an agent in
one workspace map the contents of another by watching an exclusion count move.

**But it is only correct if it is written down.** Check: does the tool's own documentation, or
its response reference, state this exception where a reader will find it? **If the exception is
nowhere stated, that is a finding** — an unstated exception to a headline guarantee is how the
guarantee stops being believed.

---

## Part H — Refusal paths

*Three refusals get their own cases because each one is a place where the tempting
implementation returns an empty result instead.*

### Case H-1 — An unknown property names the valid ones

Covered by F-7.1. Re-check here that the message contains **the full declared property list**,
not just "unknown property".

### Case H-2 — A build with no properties index refuses by name

*Only reachable on a `linux/mipsle` build. If you have not been given one, mark **Blocked — no
such build available**.*

| Step | Do this | Expect |
|---|---|---|
| H-2.1 | Run a typed filter on such a build | **Refused by name**, stating the platform and saying that plain-word search and `vault_read` still work |
| H-2.2 | Run a plain-word search on the same build | It works |
| H-2.3 | Try to create a record type there | Refused, stating that the schema file would be written and never enforced |

**Fail if:** any of these returns an empty result. "It doesn't work here" delivered as zero rows
is indistinguishable from "there is nothing here".

### Case H-3 — An ambiguous anchor refuses and names both matches

| Step | Do this | Expect |
|---|---|---|
| H-3.1 | Create a note with the heading `## Notes` appearing **twice** | — |
| H-3.2 | Ask `vault_edit` to `replace_body` using `## Notes` as the anchor | **Refused**, naming **both** line numbers and saying no change was made |
| H-3.3 | Check the file | **Byte-identical** to before |
| H-3.4 | Retry with a `line_range` instead | Succeeds |

**Fail if:** the first match is silently chosen; or the file changed at all after the refusal.

---

## Part I — `vault_read` and `vault_edit`

### Case I-1 — Reading gives you what writing needs

| Step | Do this | Expect |
|---|---|---|
| I-1.1 | `vault_read` `Specimens/fern.md` | A version token, then typed frontmatter parsed against the schema, then the body, then links and backlinks inline |
| I-1.2 | Immediately `vault_edit` `set_property` using that token | Accepted |
| I-1.3 | Count the failed writes in between | **Zero.** There must be no path where an agent has to send a write it knows will fail in order to obtain a token |
| I-1.4 | `vault_read` with `section` = a heading that exists | Just that section |
| I-1.5 | `vault_read` with `section` = a heading that does not | Refused, **listing the headings that are present** |
| I-1.6 | `vault_read` one of the broken fixture notes | It **still reads**. The bad value is flagged in place, per property. Reading is never blocked by a validation finding |
| I-1.7 | `vault_read` a very long note | Truncation is stated in the header, never silent |

### Case I-2 — `create` mints an identifier and touches nothing else

| Step | Do this | Expect |
|---|---|---|
| I-2.1 | Note the modification times of the vault's files | — |
| I-2.2 | `vault_edit` `create` a new specimen | The note is created with an `id` of the form `SP-<number>` |
| I-2.3 | Check what changed on disk | **Exactly two files**: the new note, and `.omnipus-vault/records/.seq`. Nothing else |
| I-2.4 | Create an ordinary note with no record type | **Exactly one** file changed |
| I-2.5 | Create three records in a row | Three distinct identifiers, ascending |
| I-2.6 | Delete the highest-numbered record, then create another | The new identifier is **above** the deleted one, never equal to it |
| I-2.7 | Look at the sequence for gaps | Gaps are fine and expected. A **repeat** is a failure |
| I-2.8 | Create a record of a type whose schema declares **no** `id_prefix` | The identifier is the counter alone. The product must **not** invent a prefix from the type name |

**Fail if:** an identifier is reused after a deletion (a relation written before the deletion
would then silently resolve to a different record), or a third file changes on a create.

### Case I-3 — Writes preserve the file

| Step | Do this | Expect |
|---|---|---|
| I-3.1 | Take a note whose frontmatter has a comment, an unusual key order, a blank line, one single-quoted value and one unquoted one | — |
| I-3.2 | Copy the file somewhere so you can compare | — |
| I-3.3 | `vault_edit` `set_property` on one key | Succeeds |
| I-3.4 | Compare the file with your copy | **Every byte outside the changed value is identical**: the comment survives, key order survives, blank lines survive, quoting style of untouched values survives |
| I-3.5 | Repeat on the body: `append_section` | Same — nothing outside the appended span moved |

**Fail if:** the file has been re-serialised — comments gone, keys reordered, quotes
normalised. A writer that degrades the vault a little on every touch is how an operator stops
trusting it.

### Case I-4 — A scalar write must not silently delete a list

| Step | Do this | Expect |
|---|---|---|
| I-4.1 | Take a note whose `tags` spans three lines as a block list | — |
| I-4.2 | Ask `vault_edit` to write a **single scalar value** into `tags` | **Refused**, saying the value currently spans three lines and that a scalar write would delete them, and naming "send a list value" as the fix |
| I-4.3 | Check the file | Unchanged |
| I-4.4 | Now send a list value | Accepted, and the existing list style (block or inline) is preserved |

### Case I-5 — `set_property`, `append_section`, `link`

| Step | Do this | Expect |
|---|---|---|
| I-5.1 | `set_property` with a scalar | Accepted |
| I-5.2 | `set_property` with a list on a `many` property | Accepted |
| I-5.3 | `append_section` with a heading and body | Appended at the requested level |
| I-5.4 | `append_section` twice with `once` = true | The second call does not duplicate the section |
| I-5.5 | `link` `Specimens/moss.md` to `[[Coastal survey]]` through the `expedition` relation | `moss.md` changes |
| I-5.6 | Check `Expeditions/coastal-survey.md` on disk | **Unchanged.** A relation is stored once, on the source; the inverse is derived |
| I-5.7 | `vault_read` the coastal survey note | Its `specimens` inverse now includes the moss — derived, not stored |

### Case I-6 — Stale tokens are refused and audited

| Step | Do this | Expect |
|---|---|---|
| I-6.1 | `vault_read` a note and keep its token | — |
| I-6.2 | Change the note in a text editor | — |
| I-6.3 | `vault_edit` using the old token | **Refused**, naming both the token you hold and the current one, and telling you to re-read and re-apply |
| I-6.4 | Check the audit log surface in the UI (Settings → Security, or wherever audit entries surface) | The refusal is recorded |
| I-6.5 | `vault_read` again and retry with the fresh token | Accepted |

### Case I-7 — Schema violations leave the file alone

| Step | Do this | Expect |
|---|---|---|
| I-7.1 | Any refused write from Cases C-2, C-4, C-5, C-6, C-9, I-4 | — |
| I-7.2 | After each one, check the target file | **Byte-identical to before the call.** No partial write, no truncation, no reordered keys |

**Fail if:** any refused write left a mark on the file. This is worth checking after every
single refusal in this plan, and it is cheap.

### Case I-8 — Wrong tool, right advice

| Step | Do this | Expect |
|---|---|---|
| I-8.1 | Ask `vault_edit` to `rename` a note | Refused: renaming cascades to notes you did not name; **use `vault_restructure`** |
| I-8.2 | Ask `vault_edit` to `create_record_type` | Refused: it changes what existing notes mean; **use `vault_configure`** |
| I-8.3 | Ask `vault_edit` to `create` with a `template` the vault defines | Accepted, and the template's content is used |

**Fail if:** a misrouted operation is silently performed by the wrong tool. The tool boundary is
the policy boundary, and a tool that quietly does its neighbour's job destroys the boundary.

### Case I-9 — `replace_body`, the ordinary case

*Case H-3 only exercises `replace_body`'s refusal, for an anchor that matches twice. This case is
the operation actually working: one unambiguous anchor, and a `line_range` alternative — both of
which H-3.4 uses in passing without checking either one's own behaviour.*

| Step | Do this | Expect |
|---|---|---|
| I-9.1 | Take a note with two headings, `## Notes` and `## Summary`, each with a paragraph under it. Copy the file so you can compare | — |
| I-9.2 | `vault_edit` `replace_body` with anchor `## Summary`, replacing its content with new text | Succeeds |
| I-9.3 | Compare the file with your copy | Only the span under `## Summary` changed. The `## Notes` heading, its paragraph, and the `## Summary` heading line itself are **byte-identical** to before |
| I-9.4 | Read the response | It states what was replaced — the anchor and the span it covered — not just "done" |
| I-9.5 | Now address the same note by `line_range` instead of an anchor, replacing a specific span of lines | Succeeds, and again only those lines change |
| I-9.6 | `replace_body` with an anchor that does not exist in the note (`## Nonexistent`) | Refused, **listing the headings that are actually present** — the same shape of refusal as `vault_read`'s I-1.5 |
| I-9.7 | Check the file after I-9.6's refusal | Unchanged |

**Fail if:** any byte outside the replaced span moved (a heading reordered, a blank line added or
removed, the untouched section re-serialised); the refusal in I-9.6 does not name the real
headings; or the file changed after a refused call.

---

## Part J — `vault_restructure`

*The only tool permitted to change a file the caller did not name. Every response must state
its cascade in counts.*

### Case J-1 — Rename repairs inbound links

| Step | Do this | Expect |
|---|---|---|
| J-1.1 | Note which files link to `Expeditions/northern-sweep.md` | `SP-0001`, `SP-0002` |
| J-1.2 | `vault_restructure` `rename` it to `Arctic sweep` | Succeeds |
| J-1.3 | Read the response | It states the cascade in counts — e.g. `CASCADE: 2 notes rewritten (inbound wikilinks), 1 note moved` |
| J-1.4 | Open the two specimen files on disk | Their wikilinks now say `[[Arctic sweep]]` |
| J-1.5 | Query the relation again | It still resolves. The record identifier did not change |
| J-1.6 | `check_integrity` | No new unresolved relations |

**Fail if:** the cascade count is absent; inbound links are not repaired; or the relation breaks
after a rename. Identity is the identifier, not the filename — a rename must not be able to
break a reference.

### Case J-2 — Move

| Step | Do this | Expect |
|---|---|---|
| J-2.1 | `move` a specimen to a different folder | Succeeds, cascade stated in counts |
| J-2.2 | Its relation to its expedition | Still resolves |
| J-2.3 | Inbound wikilinks | Rewritten |

### Case J-3 — Trash cannot repair what it breaks, and says so

| Step | Do this | Expect |
|---|---|---|
| J-3.1 | `trash` `Expeditions/coastal-survey.md`, which two specimens point at | Succeeds |
| J-3.2 | Read the response | It names the **count of now-unrepairable inbound links** and **lists the linking notes** |
| J-3.3 | `check_integrity` | Those relations are reported as unresolved |
| J-3.4 | `trash` the same path again | A message saying it was already trashed, with both timestamps |
| J-3.5 | `restore` it | Comes back with its **original identifier** |
| J-3.6 | `check_integrity` after the restore | The restored identifier is **not** reported as a duplicate. It was never reissued |
| J-3.7 | `restore` a path that is not in the trash | Refused, telling you where the trash contents are reported |

**Fail if:** trash reports success without naming what it broke. Rename can repair its cascade;
trash has nothing to repair it *to*, which makes it the worse of the two.

### Case J-4 — No version token, and it says so

| Step | Do this | Expect |
|---|---|---|
| J-4.1 | Read the tool's own description | It states that it takes **no** `expect_version`, and why: a single-file token cannot guard a change that rewrites notes you did not name |
| J-4.2 | Send `expect_version` anyway | Refused, with that explanation |

**Fail if:** the tool accepts a version token. A compare-and-swap that guards one of the several
files an operation writes is worse than none, because it reads as a guarantee.

### Case J-5 — Wrong tool, right advice

| Step | Do this | Expect |
|---|---|---|
| J-5.1 | Ask `vault_restructure` to `create_record_type` | Refused, naming `vault_configure` |
| J-5.2 | Ask it to `set_property` | Refused, naming `vault_edit` |

---

## Part K — `vault_configure`

*The control plane. It writes one file and changes what many notes **mean** — so every response
has to make that visible, because the file diff shows one small YAML file and nothing else.*

### Case K-1 — Creating a type converts notes nobody named

*This is the case the sixth tool exists for. Get it right and the rest of Part K is detail.*

| Step | Do this | Expect |
|---|---|---|
| K-1.1 | In Vault Beta (which has **no** schemas), create three notes carrying `type: sighting` and nothing else | — |
| K-1.2 | Confirm they behave as ordinary notes: `vault_find` `type` = `sighting` | **Refused** — no such record type is declared. Not an empty result |
| K-1.3 | Now `vault_configure` `create_record_type` for `sighting`, with one `required: true` property those notes do not have | Succeeds |
| K-1.4 | Read the response | It states the cascade **in meaning, in counts**: how many notes now match the type, how many validate clean, how many are **newly reported** and why, and how many lost validity |
| K-1.5 | A response that says only "type created" | **FAIL.** That is the whole defect this case exists for |
| K-1.6 | `vault_find` `type` = `sighting` now | Returns them, with the newly-failing ones in the problems block |

### Case K-2 — Changing and deleting a type

| Step | Do this | Expect |
|---|---|---|
| K-2.1 | `edit_record_type` to add a required property | Response reports how many existing records are revalidated and how many newly fail |
| K-2.2 | `edit_record_type` to remove an enum value that records are using | Response reports the records that just lost validity, by name |
| K-2.3 | `delete_record_type` | Response reports how many records **revert to ordinary notes** |
| K-2.4 | `vault_find` `type` = the deleted type | **Refused**, naming the declared types. Never an empty result |
| K-2.5 | Open one of those notes | It is intact. Deleting a type does not delete notes |

### Case K-3 — Views

| Step | Do this | Expect |
|---|---|---|
| K-3.1 | `write_view` defining a saved query over `specimen` | Succeeds; a file appears under `.omnipus-vault/views/` |
| K-3.2 | `vault_find` with `view` = that name | Returns the view's results |
| K-3.3 | `vault_find` with the same view **plus** a `filter` | The filter **refines** the view, it does not replace it |
| K-3.4 | `delete_view` | Succeeds; the view no longer resolves |
| K-3.5 | `vault_find` with a view name that does not exist | Refused, naming the views that do |
| K-3.6 | Check the cascade statement on K-3.1 | It says the view changes what a query returns and changes no note |

### Case K-4 — Schema files are validated

| Step | Do this | Expect |
|---|---|---|
| K-4.1 | Create a schema with **no** `schema_version` | Refused, naming the missing key and the fix |
| K-4.2 | Create a second schema file declaring a type that another file already declares | **Both** are rejected and **both paths are named** |
| K-4.3 | Create a type that already exists | Refused, naming the existing file and pointing at `edit_record_type` |
| K-4.4 | Reference a type that does not exist in `edit_record_type` | Refused, listing the declared types |
| K-4.5 | Declare a relation whose `to` names a type that does not exist | Refused, naming the declared types |

### Case K-5 — No version token, and an audit entry every time

| Step | Do this | Expect |
|---|---|---|
| K-5.1 | Look at the tool's parameters | No `expect_version` exists on any operation |
| K-5.2 | Send one | Refused with the explanation |
| K-5.3 | After every `vault_configure` call in this part — **including the refused ones** | An audit entry exists carrying the operation, agent, workspace, target and outcome |
| K-5.4 | Read the tool's description | It names its **widest** operation (`delete_record_type`), not its most common one |

---

## Part L — The tier boundary: three policies, independently settable

*This is the point of having six tools instead of one. An operator must be able to say "edit
the notes, but do not restructure the vault" and "manage the notes, but do not redefine what a
note is" — and each of those must be a real switch, not a label on a screen.*

**Where the switches are:** Agents → pick an agent → **Tools & Permissions**. Each tool takes
`allow`, `ask` or `deny`.

### Case L-1 — Edit yes, restructure no

| Step | Do this | Expect |
|---|---|---|
| L-1.1 | Set the agent's `vault_edit` to **allow** and `vault_restructure` to **deny**. Save | Saved confirmation |
| L-1.2 | In the same chat session, ask it to `set_property` on a note | Works |
| L-1.3 | Ask it to `rename` a note | **Refused.** The agent reports the refusal — it does not silently do nothing |
| L-1.4 | Check the file that would have been renamed | Untouched |

### Case L-2 — Edit yes, configure no

| Step | Do this | Expect |
|---|---|---|
| L-2.1 | Set `vault_edit` = **allow**, `vault_configure` = **deny**. Save | — |
| L-2.2 | Ask the agent to create a new record type | **Refused** |
| L-2.3 | Ask it to create the type "by writing the YAML file directly" through `vault_edit` | Refused — `vault_edit` does not write into `.omnipus-vault/` |
| L-2.4 | Ask it to do the same through `vault_restructure` | Refused |
| L-2.5 | Ask it to edit an existing schema file by any route it can think of | Refused |
| L-2.6 | Check `.omnipus-vault/records/` on disk | No new or changed file |

**Fail if:** any route reaches a schema file while `vault_configure` is denied. "This agent may
edit notes but may not redefine what a note is" is the posture; if there is a way around it, the
posture is decoration.

### Case L-3 — Configure yes, edit prompted

| Step | Do this | Expect |
|---|---|---|
| L-3.1 | Set `vault_configure` = **allow**, `vault_edit` = **ask** | — |
| L-3.2 | Ask the agent to author a record type | Works with no prompt |
| L-3.3 | Ask it to set a property on a note | An **approval prompt** appears first |
| L-3.4 | Read the prompt | It says which file and which property, before you approve |
| L-3.5 | Decline it | The write does not happen, and the agent says so |

### Case L-4 — All three are genuinely independent

| Step | Do this | Expect |
|---|---|---|
| L-4.1 | Set the three write tools to three **different** values (e.g. edit=allow, restructure=ask, configure=deny) | Saved |
| L-4.2 | Reload the page and re-open the screen | The three values persisted, as set |
| L-4.3 | Exercise one operation from each tool in one session | Each behaves according to **its own** setting |

**Fail if:** setting one changes another, or the settings do not survive a reload. A control
that reports "saved" and changes nothing is a specific, previously-shipped defect in this
product, and it is worth spending two minutes to rule out.

### Case L-5 — The fresh-install defaults

| Step | Do this | Expect |
|---|---|---|
| L-5.1 | On the clean install, read the vault tool policies for **Mia, Jim, Ava and Ray** | Reads (`vault_describe`, `vault_find`, `vault_read`) are **allow** for all four |
| L-5.2 | `vault_edit` | **ask** for Mia and Ray; **allow** for Jim and Ava |
| L-5.3 | `vault_restructure` and `vault_configure` | **ask** for all four |
| L-5.4 | Look at a worker/subagent (`worker`, `planner`, `explorer`, `researcher`, `judge`, `plansupervisor`) | **deny** on all six |
| L-5.5 | Look for any of the six with no entry at all | There are none. Every agent has an explicit entry for every one of the six |

**Fail if:** any agent × tool pair has no explicit policy entry, or a worker has anything other
than `deny`. A missing entry gets silently backfilled to `deny` with one log line — the feature
dies quietly and nothing in the UI says why.

### Case L-6 — A denial is visible

| Step | Do this | Expect |
|---|---|---|
| L-6.1 | With a tool set to `deny`, ask the agent to use it | The agent tells you it was refused, and what it could not do |
| L-6.2 | Check the Activity panel | The attempt is visible somewhere, or the agent's own text is the only record — note which |

**Fail if:** the agent silently produces a plausible-sounding answer as though the operation had
happened. A denied write reported as done is worse than a denied write.

---

## Part M — No hardcoded domain vocabulary

*A fresh install ships **no** record types. Not one, not even as an overridable default. What a
vault contains is that vault's business, and the product is supposed to know nothing about it.
The reason this gets its own part: the design documents use business vocabulary hundreds of
times as illustration, and an implementer skimming them could easily conclude the product knows
what a "company" is.*

### Case M-1 — An empty install is really empty

| Step | Do this | Expect |
|---|---|---|
| M-1.1 | On the clean install, mount **Vault Beta** (no `.omnipus-vault` folder at all) | Mounts and indexes as an ordinary set of notes |
| M-1.2 | `vault_describe` on it | **Zero record types. Zero enum values. Zero saved views. Zero identifier prefixes.** Not an error — a working vault of ordinary notes |
| M-1.3 | `vault_find` `type` = `company` | Refused — no such record type is declared |
| M-1.4 | Repeat with `contact`, `deal`, `lead`, `task`, `project`, `person`, `note` | **Every one refused.** None of them is quietly known |
| M-1.5 | Ask an agent: *"What record types does this vault have?"* | It says none, and does not invent a plausible set |
| M-1.6 | Look at every filter dropdown, template list and empty state in the Library UI | No pre-populated record types, no suggested property names, no example enum values that came from us |

**Fail if:** any record type, property name, enum value or identifier prefix exists that the
vault did not declare. **A single one is a finding** — a shipped default becomes the de-facto
standard and stops being questioned.

*Note the one legitimate exception: the seven **property type** names (`text`, `enum`,
`relation`, `date`, `integer`, `decimal`, `person`) are ours and are supposed to be there.
`person` is a property type, not a record type. Do not report it.*

### Case M-2 — Two vaults with unrelated vocabularies

| Step | Do this | Expect |
|---|---|---|
| M-2.1 | Keep Vault Alpha (`specimen`, `expedition`, `keeper`, `token`) mounted | — |
| M-2.2 | In Vault Beta, define a completely different vocabulary — say `recipe` with `heat` and `serves`, and `venue` | Works identically |
| M-2.3 | Query each | Each vault sees only its own types |
| M-2.4 | Compare the two experiences | Neither is smoother, better-supported or better-rendered than the other |

**Fail if:** one vocabulary works better than the other, or a type name in one vault appears in
the other's describe output.

### Case M-3 — The product never invents a prefix

Covered by I-2.8. Re-check here: a type with no declared `id_prefix` gets identifiers that are
the counter alone — never a prefix derived from the type name (`SP` from `specimen`, `RE` from
`recipe`). Deriving one would be the product inventing the vault's vocabulary.

---

## Part N — The human surface

*These are W6 deliverables. If the screens are not there yet, mark them **Blocked — feature
absent** and move on. A test script for a screen that does not exist is worse than no script.*

### Case N-1 — The record table

| Step | Do this | Expect |
|---|---|---|
| N-1.1 | Open the record table for `specimen` in the Library | Rows, with the schema's properties as columns |
| N-1.2 | Group by `condition` | Groups render |
| N-1.3 | Group by `tags` (multi-value) | `SP-0001` appears under both its groups |
| N-1.4 | Group by `expedition` (a relation) | Groups render |
| N-1.5 | Look at the problem banner | It names the excluded records — not just a count |
| N-1.6 | Click through the banner | A drill-down listing them, each with its reason and fix |
| N-1.7 | Edit a cell | The note on disk changes, and nothing else in the file moves (the byte-preservation rule of I-3 applies here too) |
| N-1.8 | Open a record's related-records panel | Its relations and derived inverses are shown, marked as derived |
| N-1.9 | Sort by an enum column | Lexical order (Part E) |
| N-1.10 | Look for anything the vault did not declare | Nothing |

### Case N-2 — The health view agrees with the answers

Covered by G-2 and G-3. Additionally:

| Step | Do this | Expect |
|---|---|---|
| N-2.1 | With two vaults mounted into **different workspaces**, open the health view from one workspace | It shows findings for **that workspace's** vaults only. An out-of-scope finding is invisible, not redacted |

### Case N-3 — Index state (see also A-3)

| Step | Do this | Expect |
|---|---|---|
| N-3.1 | Open Library in a fresh browser long after an index completed | The real state — phase, counts, completion — not "no progress has arrived" |
| N-3.2 | Compare against what a `vault_find` response says about freshness | The two agree. They are supposed to come from one source |

### Case N-4 — The `.base` importer is not an agent tool

| Step | Do this | Expect |
|---|---|---|
| N-4.1 | Look through every agent's tool list for anything resembling a view/base importer | **It is not there.** It is an operator/CLI one-shot, deliberately |
| N-4.2 | If an operator import path exists, import a `.base` file with an expression that cannot be translated | The untranslatable expression is reported **verbatim**, for a human to read and judge. Never approximated, never silently dropped |

---

## Part O — Cross-cutting checks

*Run these throughout, not once.*

### Case O-1 — The console

| Step | Do this | Expect |
|---|---|---|
| O-1.1 | Prove your capture works (§4.4) at the start | Both probes visible |
| O-1.2 | Watch the console through the whole run | No errors. WebSocket reconnect **warnings** are normal and are not findings |
| O-1.3 | Prove your capture works again at the end | Both probes visible |
| O-1.4 | If either probe failed at the end | You cannot report "no console errors" for this run. Say so |

Two known noises that are **not** findings on their own, recorded so you do not chase them: a
WS reconnect warning, and an `ERR_ABORTED` on a `DELETE` that returned 204 during unmount. The
second means you cannot use `ERR_ABORTED` as a failure signal for unmounts — check the row, not
the console.

### Case O-2 — One process, one session

| Step | Do this | Expect |
|---|---|---|
| O-2.1 | Do not restart the gateway at any point | You did not have to |
| O-2.2 | If you did have to | Record what forced it, what you had just done, and what changed afterwards. **This is a finding, not a footnote** |

### Case O-3 — Two vaults at once

| Step | Do this | Expect |
|---|---|---|
| O-3.1 | Keep Alpha and Beta mounted throughout | Both listed |
| O-3.2 | Search a term present in both | You can tell which vault each result came from |
| O-3.3 | Unmount one | The other keeps working and its results are unaffected |
| O-3.4 | Query a record type declared only in Alpha, from Beta's context | Refused naming Beta's declared types — not Alpha's results |

### Case O-4 — Nothing leaks between the parts

| Step | Do this | Expect |
|---|---|---|
| O-4.1 | At the end, run `vault_describe` on Alpha one more time | The schemas are as you left them — no type, property, enum value or view appeared that you did not create |
| O-4.2 | Run `check_integrity` one more time | The findings are the ones you know about. Nothing new appeared from the run itself |

---

## Part P — The trash convention

*This part extends Case J-3, which already exercises `trash`/`restore` at a basic level and
stays in force exactly as written — do not skip it in favour of this part, run both. What follows
comes from `vault-trash-convention-2026-08-28.md`, a design note landed today that assembles six
already-normative behaviours into one document a reviewer — and a tester — can read end to end,
and additionally names **six findings against the current code**. One of them — a live way for
`move` to bypass trash entirely and permanently destroy a note — was found and **fixed the same
day**, before this plan was extended (commit `883c26d7`,
`TestRename_RefusesToolStateDirectoriesInBothDirections`). Cases P-8 and P-9 are the regression
checks for that fix: their expected outcome is a plain refusal with nothing moved, and neither
needs a backup or a throwaway fixture — the whole point is that nothing is destroyed. Nothing
else in this part carries that history; run it in the ordinary way.*

### Case P-1 — Where a trashed note goes, and what does not happen to it

| Step | Do this | Expect |
|---|---|---|
| P-1.1 | Note the exact byte content of `Expeditions/coastal-survey.md` | — |
| P-1.2 | `vault_restructure` `trash` it | Succeeds |
| P-1.3 | Look in `/tmp/uat-vault-alpha/.omnipus-vault/trash/` | A folder named with a **colon-free** timestamp (`20260828T...Z` — no `:` characters), containing the note at its **original relative path** inside that folder, e.g. `.../trash/20260828T.../Expeditions/coastal-survey.md` |
| P-1.4 | Open that file in a text editor | **Byte-identical** to what you noted in P-1.1. Nothing was added, rewritten, or stamped into it |
| P-1.5 | `vault_describe` afterwards | The trash location does not appear as a mounted collection, a record type, or anything else queryable — it is bookkeeping, not vault content |

**Fail if:** the file's bytes changed in any way; the timestamp folder contains a colon (`:`) —
this specifically breaks on Windows, so it is worth checking carefully even if you are not on
one; or the trashed note is reachable through any of the ordinary `vault_find`/`vault_read` paths.

### Case P-2 — Inbound links are not repaired, and the response says exactly what broke

*Extends J-3.1/J-3.2. `Expeditions/coastal-survey.md` is linked from `Specimens/lichen.md`
(`SP-0003`) — use that if you have not already trashed it in J-3.*

| Step | Do this | Expect |
|---|---|---|
| P-2.1 | Before trashing, confirm which notes link to `Expeditions/coastal-survey.md` | `Specimens/lichen.md` |
| P-2.2 | `vault_restructure` `trash` it | Succeeds |
| P-2.3 | Read the response | It names the **count** of now-unrepairable inbound links (1) and **lists** `Specimens/lichen.md` by path |
| P-2.4 | Open `Specimens/lichen.md` on disk | **Unchanged.** Trash does not rewrite the notes that pointed at what it removed — there is nothing to repair them *to* |

**Fail if:** the response is silent about the broken link, names only a count with no list, or
`lichen.md` was rewritten.

### Case P-3 — A trashed target gets its own explanation, not its own category

*This is the part of the design that is easy to get wrong in a way that looks like an
improvement: adding a **third** kind of finding for "points at something trashed" would actually
be worse, because it would erase whether the broken link was a typed relation or an ordinary
wikilink — and those two need different fixes. Read this before you run `check_integrity` below.*

| Step | Do this | Expect |
|---|---|---|
| P-3.1 | With `Expeditions/coastal-survey.md` trashed (P-2) and `SP-0003`'s `expedition` relation now pointing at it | — |
| P-3.2 | Run `check_integrity` | `SP-0003`'s relation is reported under the **same category** it would be in for any other unresolved relation — not a new, separate "trashed" category |
| P-3.3 | Read that specific finding's text | It says the relation is unresolved, **and additionally** says the target was trashed, when, and that it is restorable — extra information on an existing finding, not a new kind of finding |
| P-3.4 | Count the categories `check_integrity` reports overall | The same set as Case B-4 — this did not add a seventh kind |

**Fail if:** a new category (something like "trashed reference") appears that was not one of the
kinds B-4/B-7 named; or the finding fails to say the target was trashed at all, leaving a reader
to think it is an ordinary broken relation with no faster fix available.

### Case P-4 — The index forgets a trashed note immediately, with no window

*Why this matters, and why it might fail: the design document itself flags this as the weakest
part of the current build — the obvious implementation relies on the **next scheduled re-index**
noticing the note is gone, which is not the same guarantee as "immediately", and leaves a real gap
where a search returns a note the user just told the system to throw away. **This case is
deliberately built so that only the immediate mechanism can pass it** — do not let any indexing
activity happen between the trash call and the check.*

| Step | Do this | Expect |
|---|---|---|
| P-4.1 | Pick a specimen you have not touched yet and confirm `vault_find` returns it by a word unique to its body | It is found |
| P-4.2 | `vault_restructure` `trash` it | Succeeds |
| P-4.3 | **Without pausing, without triggering any other indexing action, and without waiting** — immediately repeat the exact same `vault_find` query | The trashed note is **not** returned |
| P-4.4 | Immediately search for it by its identifier too | Also not returned |
| P-4.5 | `check_integrity` immediately afterward | The trashed note does **not** appear as an orphan row (B-7) — trashing removed its properties row along with removing it from the text index, in the same operation |

**Fail if:** the note is still returned by P-4.3 or P-4.4 — even once, even briefly. This is the
one case in this part where "it worked the second time" is not reassuring: a window that closes
in under a second is still a window, and it is the exact failure this case is built to catch. If
you find one, note how many attempts it took to observe and how many did not show it.

### Case P-5 — Restoring, addressed by the path you remember

| Step | Do this | Expect |
|---|---|---|
| P-5.1 | With `Expeditions/coastal-survey.md` trashed | — |
| P-5.2 | `vault_restructure` `restore` it, addressed by its **original** path — `Expeditions/coastal-survey.md`, not any path under `.omnipus-vault/trash/` | Succeeds |
| P-5.3 | Look at the note's `id` | `EX-0002` — the **same** identifier it had before, not a new one |
| P-5.4 | `vault_find` for it | Found again, resolvable |
| P-5.5 | `SP-0003`'s `expedition` relation | Resolves again, and P-3's trashed-target annotation is gone from `check_integrity` |
| P-5.6 | Trash the same note **twice in a row** (trash it, then trash it again after a moment) | The second call succeeds and reports it was **already trashed once**, naming **both** timestamps — the earlier one and the new one |
| P-5.7 | `restore` it with **no** `trashed_at` argument | Restores the **most recent** of the two trashed copies, and the response says which timestamp it took and names the older one it left behind |
| P-5.8 | `restore` again, this time naming the older timestamp explicitly | Restores that specific copy instead |

**Fail if:** restore requires you to know or supply a trash-internal path rather than the note's
own original path; the double-trash in P-5.6 is silently accepted with no mention of the earlier
copy; or P-5.7 restores the wrong one, or does not say which one it restored.

### Case P-6 — The four restore refusals

| Step | Do this | Expect |
|---|---|---|
| P-6.1 | `restore` a path that was never trashed, e.g. `Specimens/fern.md` | Refused: `no trashed note at Specimens/fern.md`, and it tells you where to look — `vault_describe` reports the trash contents |
| P-6.2 | Trash a note, then **create a new note at that same original path** before restoring, then attempt the restore | Refused, naming **both** paths — the live note occupying the spot and the trashed one that cannot land there |
| P-6.3 | After a genuine restore (P-5), run `check_integrity` | The restored identifier is **not** reported as a duplicate. It was never reissued — it is the same record coming back, not a second one wearing its old name |
| P-6.4 | If you can construct it: hand-edit a path inside the trash folder so it no longer matches its own record (rename the timestamp folder, or move the note within it) | The subsequent `restore` is refused rather than writing to whatever the edited path implies. Mark this **Blocked — could not construct the condition** if you cannot arrange it by hand |

**Fail if:** P-6.1's refusal does not point you at where the trash contents are reported; P-6.2
restores into the collision silently, or refuses without naming both paths; or P-6.3 reports the
restored record as a duplicate of itself.

### Case P-7 — There is no way to delete a note permanently through any tool

*The design is explicit that no agent-facing operation of any of the six tools ever permanently
deletes a note — not a `purge` operation, not a `force` flag on `trash`, nothing. This is a
negative case: you are checking that something does **not** exist.*

| Step | Do this | Expect |
|---|---|---|
| P-7.1 | Ask an agent: *"Is there any way to permanently delete a note right now, skipping the trash? List every tool and operation you have that could do it."* | It reports none exists |
| P-7.2 | Ask `vault_restructure` to `trash` with any argument resembling `permanent`, `force`, `skip_trash` or `purge` | Either the argument is refused as unrecognised, or it is silently ignored and the note is trashed normally (recoverable) — **never** actually permanently deleted |
| P-7.3 | Look at every tool's own parameter list across everything you have exercised in this plan | No tool declares a permanent-delete parameter of any name |

**Fail if:** any argument, on any tool, causes a note to be unrecoverably gone with no trash entry
at all. That is not a refusal-wording defect — treat it with the same severity as any other
permanent, unrecoverable data loss reported anywhere in this plan.

### Case P-8 — REGRESSION: a move into the vault's own bookkeeping folder is refused

*This defect is already fixed — verified before this case was rewritten. `Renamer.Plan`
(`pkg/knowledge/rename.go`) now calls `authorRefuseReserved` on **both** the source and the
destination of every `rename`/`move`, at **any** depth, for **both** `.omnipus-vault/` and
`.obsidian/` — commit `883c26d7`, `TestRename_RefusesToolStateDirectoriesInBothDirections`.
Before the fix, a move into `.omnipus-vault/` was an untracked hard delete: the note left
search, the link graph and the properties index with no trash entry, no audit record and no
restore path. This case exists so that fix stays fixed — it is a regression check, not an
attempt to reproduce data loss, and it needs no backup and no throwaway fixture: the expected
outcome is that nothing moves.*

**One step below exists to remove a mask, not to set up the scenario. Read it before skipping
it.** Before the fix, this exact move was refused too — but for the wrong reason: the
destination folder didn't exist yet, so it failed on an ordinary "destination directory does
not exist" error that has nothing to do with the guard. A tester who does not create the
destination directory first will see *a* refusal either way, and will pass this case whether or
not the real guard is present. Step P-8.1 removes that mask. (Once `trash` itself is built,
`.omnipus-vault/trash/` always exists and the mask disappears on its own — do not rely on that
happening yet.)

| Step | Do this | Expect |
|---|---|---|
| P-8.1 | Using your file manager, create the folder `/tmp/uat-vault-alpha/.omnipus-vault/trash/` if it does not already exist | The folder exists, empty |
| P-8.2 | Ask `vault_restructure` to `move` `Specimens/moss.md` to `.omnipus-vault/trash/moss.md` | **Refused** |
| P-8.3 | Read the refusal | It names the **reserved location** specifically — `.omnipus-vault/` (or your build's equivalent wording) — not a generic error, and not "destination directory does not exist" |
| P-8.4 | Check `Specimens/moss.md` on disk | Still at its original path, byte-identical to before the call |
| P-8.5 | `vault_find` for it | Still found, normally |
| P-8.6 | If you want the fuller picture: repeat P-8.2 with a destination inside `.obsidian/` instead, and again with a **nested** reserved path such as `Notes/.obsidian/plugins/moss.md` | Both refused the same way. The guard applies at any depth, not only at the top level |

**Fail if:** the move at P-8.2 is **accepted** — this is the live-data-loss defect returning.
Report it as **CRITICAL**, quote the exact call and its response verbatim, and stop there; do
not attempt further moves into `.omnipus-vault/` "to confirm" it, one demonstration is enough
and each one risks another note. Also fail if the refusal happens but does not name the reserved
location specifically (a refusal for the wrong reason is indistinguishable from the
destination-missing mask described above, and would pass this case against broken code); or if
the source file was touched in any way despite being refused.

### Case P-9 — REGRESSION: a move *out of* the bookkeeping folder is refused on the source side

*The mirror direction, checked separately because the guard has to catch it on the **source**
side rather than the destination side, and that is a different code path succeeding for the
same reason. This is also why `restore` (Case P-5) cannot be, and is not, implemented as an
ordinary `move`/`rename` call: the general-purpose `move` this case exercises refuses **any**
crossing of `.omnipus-vault/`'s boundary, in or out — which is exactly right for `move`, and
exactly why `restore` has to be its own dedicated, narrower operation with its own containment
checks (FR-048b), rather than a thin wrapper over the tool this case is testing.*

| Step | Do this | Expect |
|---|---|---|
| P-9.1 | Note that `.omnipus-vault/records/keeper.yaml` exists as a real schema file | — |
| P-9.2 | Ask `vault_restructure` to `move` `.omnipus-vault/records/keeper.yaml` to `Keepers/keeper-schema-moved.yaml` | **Refused** |
| P-9.3 | Read the refusal | It names the reserved location (`.omnipus-vault/`) specifically, this time as the **source** side, not the destination |
| P-9.4 | Check `.omnipus-vault/records/keeper.yaml` on disk | Unchanged, and no new file exists at `Keepers/keeper-schema-moved.yaml` |
| P-9.5 | `vault_describe` afterward | The `keeper` record type is exactly as it was — nothing about it changed |

**Fail if:** the move is accepted — report as **CRITICAL**, since it would mean an agent holding
only `vault_restructure: allow` could relocate or exfiltrate the vault's own schema files, saved
views, or another note's trash entry, with none of the tier-boundary protections Part L exists to
provide; or the refusal fires but does not name the reserved location.

### Case P-10 — The 30-day retention purge: not yet testable, and here is what correct will look like

*Mark this **NOT-YET-TESTABLE** rather than attempting to run it. The design document itself
states that nothing today names who runs the purge, when, or what it is permitted to delete — it
is design intent with no implementation yet. Do not invent a way to trigger it; there is not
supposed to be an agent-facing one. This case exists so that whoever runs this plan again once
the sweep ships knows exactly what to check.*

| When it ships, check | Expect |
|---|---|
| Whether a trash entry older than the retention window (30 days by default) survives a sweep | It does not — it is deleted for good |
| Whether the sweep touches anything it did not itself write into `.omnipus-vault/trash/` | It must not. Anything under that folder without a valid receipt (see P-11) is reported and left alone, never deleted on the strength of its location alone |
| Whether the retention window is configurable | It should be, per the design's recommendation, with a stated default |
| Whether the **first** purge that ever runs on a vault is reported somewhere visible | It should be — a scheduled deletion of the user's own files on a timer they did not explicitly set should not be silent the first time it happens |
| Whether any agent tool can trigger, shorten, or bypass the purge | It must not be able to. This stays true even after the sweep ships — only a human-facing surface (Settings, or the operator's own file access) should touch retention |

Do not attempt to force 30 days of elapsed time or fabricate a trash entry with a backdated
timestamp to test this early — that tests a mechanism that has not been specified yet, and a
result either way would not mean anything.

### Case P-11 — The trash receipt is bookkeeping, not a tool surface

*Each trashed note is designed to carry a small `entry.json` receipt beside it, recording the
original path, timestamp, acting agent, and (for a record) its type and identifier. This is
plumbing that other operations rely on — it is not something an agent is meant to read or write
directly.*

| Step | Do this | Expect |
|---|---|---|
| P-11.1 | Trash a note, then look inside its timestamped folder under `.omnipus-vault/trash/` with a file manager | An `entry.json` file sits beside the note |
| P-11.2 | Open it in a text editor | Readable JSON naming the original path, the timestamp, and (if the note was a record) its identifier |
| P-11.3 | Look through every agent tool's parameter list for anything that reads or writes `entry.json` directly | Nothing does. The receipt is consumed internally by `restore`, `check_integrity`'s annotation (P-3), and `vault_describe`'s trash report — never exposed as its own read or write target |
| P-11.4 | `vault_describe` on a vault with something in its trash | Reports the trash contents (count, and total size) without you having to open any receipt yourself |

**Fail if:** any tool exposes a direct read or write of `entry.json`, or `vault_describe` cannot
report the trash contents without a receipt being manually inspected first.

---

## How to report

For every numbered case: **Pass**, **Fail**, **Blocked**, or — for Part P-10 only —
**NOT-YET-TESTABLE**.

**NOT-YET-TESTABLE is not a synonym for Blocked, and the two must not be used interchangeably.**
*Blocked* means the tool exists in principle but this particular attempt could not exercise it —
the feature is absent from this build, the condition could not be forced, or the fixture was too
small. *NOT-YET-TESTABLE* means there is no mechanism anywhere yet for this behaviour to attach
to — no operation, no trigger, nothing in the design that names an actor. Do not attempt to
improvise a way to test it, and do not report it as Blocked: say NOT-YET-TESTABLE, and say why,
exactly as Part P-10 does.

For a **Fail**, give:

1. What you did, step by step, precisely enough that someone else can repeat it exactly.
2. What you expected.
3. What actually happened — **the tool result verbatim**, not the agent's summary of it.
4. Any console error, copied character for character.
5. Whether it happened again on retry, and how many times out of how many.

For a **Blocked**, give the reason in one of these forms:

- *Feature absent* — the tool or screen does not exist yet (say which, and what Part 0 showed).
- *Could not force the condition* — say what you tried.
- *Fixture too small / no such build* — say what would be needed.

**State plainly what your sign-off does and does not cover.** If Part 0 showed three of the six
tools, then passing every runnable case means those three behave; it says nothing about the
other three, and nothing about the record layer as a whole. The previous round's results opened
with exactly that paragraph and it was the most useful thing in them.

**Do not diagnose, do not work around, do not read the source.** "I restarted and then it
worked" is a finding. "Probably my fixture" is not an acceptable dismissal — if you suspect the
fixture, say what you observed *and* that you suspect it, and let someone else decide.

**Assume every failure is real.**

---

## Appendix — Case index and what each traces to

| Case | Subject | ADR-068 / spec reference | Earliest wave |
|---|---|---|---|
| Part 0 | What exists today | D15.3, D20 | — |
| A-1 | Mount and index | ADR-067; prior F2 | shipped |
| A-2 | Not a knowledge base | prior F6 | shipped |
| A-3 | Index state for a late arrival | §2.7.2, FR-020f, US-13 | W6 |
| A-4 | Indexing after a settings change | prior Case 2 | shipped |
| A-5 | Image embeds | prior F4 | shipped |
| A-6 | HTML preview | prior F3 | shipped |
| A-7 | "Changed since indexed" | prior F5 | shipped |
| A-8 | Unmount mid-index | prior F7 | shipped |
| A-9 | Gateway stays up | prior F1 | shipped |
| A-10 | Awkward content | prior Case 6 | shipped |
| A-11 | Readable errors | prior F8 | shipped |
| B-1..B-6 | `vault_describe`, `check_integrity` | D15.3, D15.5b, §4.1.1, FR-075/075a | W1 |
| B-7 | The sixth `check_integrity` kind: orphan rows | D15.3 revision 6, D16.5 (orphaned row ruling) | W1 |
| B-8 | Clamp vs refusal on `check_integrity`'s two bounds | D15.5b (`check_integrity findings`, `check_integrity notes swept` rows) | W1 |
| C-1..C-3 | text, enum, relation | D3, D4, D5, D5.1 | W1/W3 |
| C-4 | date, strict ISO | FR-021d, R-H | W1 |
| C-5 | integer, int64 bound | D3, FR-012 | W1 |
| C-6 | decimal, exact, 100 places | D3, FR-013 | W1 |
| C-7 | person | D3 | W1 |
| C-8 | `money` is gone | D3 revision 7, O-2 superseded | W1 |
| C-9 | Arity | D3.1, FR-042 | W1 |
| C-10 | Absent is a state | D3.2, R-2/R-3, §4.1.2 `<>` row | W2 |
| C-11 | Types scoped to record type | D3.3 | W1 |
| D-1 | The six folding pairs | AC-8.9, FR-011a, D16.6 Unicode receipt | W2 |
| D-2 | Enum resolution folds | D4, FR-011 | W1 |
| D-3 | One value, one group, one sort position | D4 revision 8 (folded sort key) | W2 |
| D-4 | `LIKE` folding and rune count | FR-011a, FR-022b, DS-4 | W2 |
| D-5 | Identifiers are byte-exact | D16.6 R-8 | W2 |
| E-1..E-4 | Lexical enum ordering | D4 as revised, §4.1.2 `sort` row | W2 |
| F-1..F-5 | The SQL operator set | O-3 amended, §4.1.2 filter table | W2 |
| F-6 | Unsupported constructs refused | FR-022c | W2 |
| F-7 | Unknown names refused | FR-024, AC-F1 | W2 |
| F-8 | `near` composed with filters | D15.3, FR-076, AC-F2 | W3 |
| F-9 | Borrowed values marked | D22.4, FR-124 | W2 |
| F-10 | Grouping | D10, FR-027/028/029 | W2 |
| F-11 | Totals state scope, count once | D22.5, FR-028a | W2 |
| F-12 | `explain` evaluates nothing | AC-F3, FR-073 | W2 |
| F-13 | `kind: task` | D15.3, D22.4 amended, AC-F7 | W2 |
| F-14 | Zero hits, near-miss vocabulary | D21.4, FR-114 | W2 |
| F-15 | Clamps and cursors | D15.5b | W2 |
| F-16 | Candidate caps | D16.3a C-3, FR-064/064a | W2 |
| F-17 | Response shape | D22.1..D22.7, §4.2 | W2 |
| F-18 | Group totals can sum past the matched-record count | FR-027/028/028a | W2 |
| F-19 | The filter tree's own bound: 64 leaves / depth 8 | FR-023c | W2 |
| G-1..G-5 | The honesty contract | D13, FR-025/025a/026 | W2/W6 |
| G-6 | Two indexes disagreeing | D16.5, AC-16.5, AC-F5 | W1/W2 |
| G-7 | The workspace-scoping exception | D13.1, AC-F4, FR-062 | W1 |
| H-1 | Unknown property | FR-024 | W2 |
| H-2 | SQLite-less build | D16.2a, FR-020h, AC-F6 | W1 |
| H-3 | Ambiguous anchor | D14.1, FR-047, AC-E3 | W4 |
| I-1 | `vault_read` and the version token | D15.3, AC-R1/R2/R3 | W3 |
| I-2 | Identifier allocation | D7, D7.1, AC-7.1/7.2, FR-036b/038 | W1/W4 |
| I-3 | Byte-preserving writes | D14, AC-14.1 | W4 |
| I-4 | Multi-line clobber refused | D14, AC-14.2, FR-040b | W4 |
| I-5 | Edit operations, link stores once | D5, D15.3, FR-030/032 | W4 |
| I-6 | Stale token refused and audited | D15.5c, AC-15.5c | W4 |
| I-7 | Refusals leave the file alone | §3 behavioural contract | W4 |
| I-8 | Wrong tool, right advice | D15.1, §4.1.4 refusal table | W4/W5 |
| I-9 | `replace_body`, the ordinary anchor- and line-range-addressed case | D14.1, FR-047 | W4 |
| J-1..J-3 | rename, move, trash | D15.3, FR-048/048a | W5 |
| J-4 | No version token on a cascade | D15.5c, AC-15.5d, AC-X3 | W5 |
| J-5 | Wrong tool | FR-070d/070e | W5 |
| K-1 | Creating a type converts notes | D15.6, AC-C1 | W5 |
| K-2 | Change and delete a type | FR-015/017, AC-C4 | W5 |
| K-3 | Views | D10, FR-018 | W5 |
| K-4 | Schema validation | FR-002/003/004 | W5 |
| K-5 | No token, audit every call | FR-018a, AC-C3/C5/C6 | W5 |
| L-1..L-4 | Three independent policies | D15.2, D18, D20 W5 exit criterion, AC-X2, AC-C2 | W5 |
| L-5 | Seed posture | D18, AC-18.1/18.2 | W5 |
| L-6 | A denial is visible | D18 | W5 |
| M-1..M-3 | No domain vocabulary | D0, D0.1, FR-004a, R-F | W0/W1 |
| N-1 | Record table | D20 W6 | W6 |
| N-2 | Health view scope | FR-025a, FR-025b | W6 |
| N-3 | Index state snapshot | FR-020f, FR-020g | W6 |
| N-4 | `.base` importer is not a tool | D15.4, O-1, FR-100..103 | W6 |
| O-1..O-4 | Cross-cutting | — | — |
| P-1 | Trash storage location, path format, byte-untouched | FR-048; trash convention §1 | W5 |
| P-2 | Inbound links not repaired, count and list named | FR-048; trash convention §2 | W5 |
| P-3 | A trashed target annotates its existing category, not a new one | trash convention §2 | W5 |
| P-4 | Immediate index removal, no intervening-scan window | FR-048; trash convention §3, finding F4 | W5 |
| P-5 | Restore by original path, newest-first, `trashed_at` | FR-048a; trash convention §4, finding F3 | W5 |
| P-6 | The four restore refusals | FR-038a, FR-048a, FR-048b; trash convention §4 | W5 |
| P-7 | No permanent-delete operation exists on any tool | trash convention §6 | W5 |
| P-8 | REGRESSION: `move` into `.omnipus-vault/` is refused | trash convention §8 finding F1 (blocker, fixed 883c26d7) | W5 |
| P-9 | REGRESSION: `move` out of `.omnipus-vault/` is refused on the source side | trash convention §8 findings F1/F6 (fixed 883c26d7) | W5 |
| P-10 | Retention purge — not yet testable | trash convention §6, §8 finding F5 | W5 (unbuilt) |
| P-11 | The trash receipt is internal plumbing, not a tool surface | trash convention §7 | W5 |
