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
