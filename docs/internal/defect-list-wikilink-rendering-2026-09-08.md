# Defect list — retest round 2 (2026-09-08)

Wikilink rendering (WL-1, WL-2) and knowledge-base creation (WL-3, WL-4).

Founder findings from retesting build `94bb13e61`. Companion to
`defect-list-knowledge-base-ux-2026-09-08.md`. **This file documents; it does
not fix.** Each entry says what was verified in code and what was not.

---

### WL-1 — links in a base render WHITE; the same links in a note render GOLD
**Severity:** medium · **Area:** Library SPA (base/view cells) · **Reported by:** founder
**Status:** Open · **Confirmed in code**

A relation link inside a base is styled as *unverified* (white) while an
equivalent wikilink in a markdown note is styled as *resolved* (gold). Same
concept, two appearances, and the base consistently looks like the degraded one.

**Verified mechanism.** `src/components/library/preview/knowledgeMarkdown.tsx`:

```
LINK_CLASS            = text-[var(--color-accent)]     -> Forge Gold
UNVERIFIED_LINK_CLASS = text-[var(--color-secondary)]  -> white/secondary
```

`viewparts/ViewCellLink.tsx:91` selects between them with
`const className = verified ? LINK_CLASS : UNVERIFIED_LINK_CLASS`.

**Root cause — an honest design decision with a bad practical outcome.** The
base's resolver (added with KB-8) matches a wikilink target against **only the
rows currently loaded in that view**. It was deliberately built to answer just
two states — `resolved` (found in these rows) or `unknown` (not found here) —
and never `unresolved`, because absence from a subset is not absence from the
collection. That reasoning is correct in isolation.

The consequence is that **most base links are `unknown`**, since a view's own
rows rarely contain the link's target, so they render white. Meanwhile
`KnowledgeNoteView` resolves against the **link graph** (`loadGraph`, kind
`links`), which knows the whole collection, so the same link resolves and renders
gold.

**So the colour difference is not a styling bug — it is a resolver-scope
difference showing through the styling.** Fixing only the colour would make the
base *claim* verification it does not have, which is the dishonesty the
three-state model exists to prevent.

**Direction (not a decision):** give the base resolver the same collection-wide
source the note view uses (the link graph) rather than the view's loaded rows.
Then `resolved` means the same thing on both surfaces and the colours agree
because the *facts* agree. If that is too expensive per view, the fallback is to
make `unknown` visually distinct from BOTH resolved and unresolved, and document
that a base cannot verify beyond its own rows — but that is a worse product than
resolving properly.

---

### WL-2 — `[[Daniel Piatkowski]]` renders as plain text in some notes
**Severity:** medium · **Area:** Library SPA (render path selection) · **Reported by:** founder
**Status:** Open · **Mechanism confirmed; exact surface NOT reproduced**

Some notes show the raw wikilink notation `[[Daniel Piatkowski]]` as literal
text, brackets included, instead of a link.

**Verified mechanism.** Only TWO modules apply wikilink parsing:
`src/components/library/knowledge/KnowledgeReader.tsx` and
`preview/knowledgeMarkdown.tsx` (which defines `remarkKbWikilinks`). Any markdown
rendered through a different path — e.g. the plain text body used for ordinary
files — has **no wikilink parsing at all** (grepped: zero references), so the
raw `[[...]]` survives to the screen verbatim.

**Therefore a note renders raw wikilinks whenever it is displayed by a surface
that does not route through `KnowledgeReader`.** Candidates, in likelihood order:
1. A `.md` file opened from a plain folder rather than a knowledge base — the
   file is markdown but the surface is the generic text preview.
2. A note inside a knowledge base whose detection did not complete, so the UI
   treats it as an ordinary file (this is the failure mode KB-6/E-9 covers).
3. A property/frontmatter value surfaced somewhere as a plain string. NOTE the
   note body path deliberately DROPS frontmatter
   (`remarkKbFrontmatter`), so `owner: "[[Daniel Piatkowski]]"` in YAML cannot
   be what appears in the rendered body — if the founder saw it there, a
   different surface is rendering frontmatter as text.

**NOT reproduced.** I could not confirm which of these the founder actually saw:
the browser session's auth was cleared when the test binary restarted, and I
chose to report the verified mechanism rather than spend the time guessing at a
note. **Before fixing, reproduce it** — the fix differs per cause: (1) is a
routing decision, (2) is the detection-error surfacing already tracked in KB-6,
(3) is a new renderer.

**Question for the founder that would settle it immediately:** was the note
opened from inside "Elicify KB", or from a plain folder? And was the `[[...]]`
in the body text or in a properties/metadata area?

---

---

### WL-3 — the New knowledge base dialog still shows a Location field
**Severity:** low · **Area:** Library SPA · **Reported by:** founder
**Status:** Open · **Confirmed in code**

KB-3 removed the workspace picker and the free-text path box, but left a
read-only **Location** line showing the destination. The founder's instruction
is that the dialog should be **identical to New folder**: a name, and nothing
else.

**Verified.** `LibraryNewVaultDialog.tsx:152` renders `<Label>Location</Label>`
plus a destination line (`destinationLabel()`, :54). `LibraryNewFolderDialog.tsx`
renders only `Folder name` (:83) and its validation messages — no location
display of any kind.

**Note on the reasoning being overridden, so it is a decision and not an
oversight:** the read-only line was added deliberately, on the argument that
removing the picker should not make the destination invisible. The founder has
overruled that — New folder already creates silently where you are and is
understood, so the knowledge base dialog should behave the same. Remove the
Location block and its helper, and update the two tests that assert the
destination text.

---

### WL-4 — a knowledge base can be created INSIDE another knowledge base
**Severity:** high · **Area:** pkg/knowledge (both UI and agent paths) · **Reported by:** founder
**Status:** Open · **Confirmed in code**

Nothing prevents creating a knowledge base nested inside an existing one. This
must be blocked, and blocked in ONE place so both callers inherit it.

**Verified.** `pkg/knowledge/detect.go:326` `CreateInWorkspace` returns
`ErrAlreadyKnowledgeBase` only when the TARGET PATH ITSELF is already a
knowledge base. There is no ancestor check — no walk up the parent chain looking
for a `.omnipus-vault` / `.obsidian` marker above the target. Both callers
inherit the gap:
- the SPA path, `handleLibraryCreateVault` (`pkg/gateway/rest_library.go`), and
- the new agent tool, `knowledge_base_create`
  (`pkg/knowledge/knowledge_base_create.go`), which deliberately reuses
  `CreateInWorkspace` rather than duplicating creation logic.

**That shared reuse is what makes this cheap to fix correctly:** add the ancestor
check inside `CreateInWorkspace` and BOTH surfaces are covered at once. Adding it
in the handler or the tool instead would leave the other open and create the
second rule that can drift — the failure mode this diff has already been bitten
by more than once.

**Why it matters beyond tidiness:** a nested knowledge base has ambiguous
ownership of the notes beneath it — two collections would each claim the same
files, two indexes would scan them, and `knowledge_list` would report a
containment relationship the rest of the system has no model for. The detection
rule (`Detect`) resolves a folder to at most one collection, so the nested case
is undefined behaviour, not merely untidy.

**Also worth deciding while fixing:** what should happen to a knowledge base that
is nested TODAY, on an existing install, because nothing stopped it? Refusing new
nesting is straightforward; the migration question for existing data is a
separate decision and should be stated rather than discovered later.

---

### WL-5 — a Base VIEW cannot be embedded inside a note (`![[Tasks.base#View]]`)
**Severity:** high · **Area:** Library SPA (markdown embed resolution) · **Reported by:** founder
**Status:** Open · **Confirmed in code — MISSING CAPABILITY, not a bad import**

The founder's dashboards compose themselves out of embedded Base views. Example:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/e-vault-fix/07-Dashboards/Founder Cockpit.md`
contains 15+ embeds of the form:

```
![[Tasks.base#Needs Daniel]]
![[Decisions.base#Awaiting founder]]
![[Subscriptions.base#Renewing <14d]]
![[CRM.base#Companies by Segment]]
```

and states its own composition rule: *"assembled entirely from Base-view embeds
— never hand-typed data. Adding a module means dropping in one more
`![[Domain.base#View]]` embed."* An entire `07-Dashboards/` folder is built on
this pattern.

**The migration is NOT at fault — verified.** All 18 `.base` files are present in
`06-Bases/`, alongside 40 record types and 69 views under the marker directory.
Opening a `.base` file directly works: `preview/BasePreview.tsx` renders it as its
views rather than a download.

**What is missing.** `KbWikilinkOptions` (`preview/knowledgeMarkdown.tsx:344`)
exposes exactly one embed hook — `resolveEmbedUrl?: (target) => string |
undefined` — documented as *"Resolves an embedded ATTACHMENT (`![[diagram.png]]`)
to a URL the browser can load."* There is no branch for a `.base#View` target
anywhere in the preview tree (grepped: zero hits outside `BasePreview`'s own
file-open path). So a dashboard embed falls through the attachment path,
`resolveEmbedUrl` returns undefined for a non-attachment target, and the embed
renders as a visibly-marked unresolved reference instead of the view.

**Consequence:** every dashboard in the vault renders as a list of broken
references rather than data. For a founder whose operating view IS the dashboard,
the knowledge base is materially less useful than the Obsidian original — this is
the largest functional gap found so far.

**Direction (not a decision):** the pieces already exist and mostly need
connecting — `parseWikilink` already distinguishes the `![[...]]` embed form and
already parses a `#fragment`; `BasePreview` already renders a named view from a
`.base`; `ViewPartsRenderer` already renders one view's parts. The work is a
render path that recognises a `.base` target with a view fragment and mounts the
existing view renderer inline, plus decisions on: how many embeds may render on
one page (each is a query), whether an embedded view is read-only, and what an
embed of a MISSING view renders as — it must be visibly unresolved, never an
empty box that reads as "no data".

**Check before building:** confirm whether the `#fragment` in these embeds
matches the migrated view NAMES in `.omnipus-vault/views/*.yaml`, or only the
`views:` block inside each `.base` file. If the two disagree, resolution needs a
mapping and that is worth knowing up front rather than mid-implementation.

## Summary

| ID | Title | Severity | Status |
|---|---|---|---|
| WL-1 | Base links render white; note links render gold | Medium | Open — cause confirmed |
| WL-2 | Raw `[[wikilink]]` text in some notes | Medium | Open — mechanism confirmed, surface unreproduced |
| WL-3 | New KB dialog still shows a Location field | Low | Open — confirmed |
| WL-4 | A knowledge base can be created inside another | High | Open — confirmed, both paths |
| WL-5 | Base views cannot be embedded in a note (dashboards) | High | Open — missing capability, import is fine |

**Both trace to the same root theme** as the KB-8 work: wikilink rendering is
correct on the note surface and partial everywhere else. WL-1 is a resolver
scoped too narrowly; WL-2 is a render path that never parses wikilinks at all.
Fixing them together, by making the note surface's mechanism the single one every
surface uses, is likely cheaper than two separate fixes.
