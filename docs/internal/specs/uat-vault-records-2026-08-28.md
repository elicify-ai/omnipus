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
