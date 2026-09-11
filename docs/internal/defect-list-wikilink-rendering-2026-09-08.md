# Defect list — retest round 2 (2026-09-08)

Wikilink rendering (WL-1, WL-2) and knowledge-base creation (WL-3, WL-4).

Founder findings from retesting build `94bb13e61`. Companion to
`defect-list-knowledge-base-ux-2026-09-08.md` and
`defect-list-embedded-content-review-2026-09-11.md`. Each entry says what was
verified in code and what was not.

> **Status pass, 2026-09-11.** Every entry below was re-checked against the
> code on `integrate/library-improvements-v0.1.1` at `4e2ef3dbb`, not against
> the commit messages that claimed the fixes. A status says **Fixed** only
> where the change is visible in the file the defect names. Commit hashes are
> cited so any claim here can be re-checked in one command.

---

### WL-1 — links in a base render WHITE; the same links in a note render GOLD
**Severity:** medium · **Area:** Library SPA (base/view cells) · **Reported by:** founder
**Status: FIXED, with a stated bound.**
Fixed inside an embedded view (`15f60b861`), and then on the surface it was
actually reported from — a `.base` file opened directly — by `f3a9154de`. The
bound is real and deliberate: only the first 40 loaded rows are queried for
link edges (`COLLECTION_LINK_ROW_QUERY_CAP`). Beyond that cap a row keeps the
older resolved-or-unknown fallback and is never silently promoted to gold.

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

#### What actually shipped — and what did not (verified 2026-09-11)

**Half of this is fixed.** Your Q8 ruling on ADR-083 said an embedded base view
should resolve its links against the graph of the note you are reading, not
against the handful of rows that view happened to load. That is now true in the
code: `KbBaseEmbedContent` (`preview/knowledgeMarkdown.tsx`) hands the note
reader's own resolver down to `BasePreview`, and `BasePreview` takes it over
completely when supplied (`BasePreview.tsx`, the `resolveWikilink` memo:
`if (embed?.resolveWikilink) return embed.resolveWikilink`). So inside a
dashboard, a link tells the truth and is coloured by it — gold when it resolves,
visibly unresolved when the target really is missing.

**The other half is untouched, and it is the half you reported.** When you open
a `.base` file directly in the Library, nothing changed. `LibraryPreviewPane`
mounted `<BasePreview>` with no embed options at all, so it fell back to a
row-scoped resolver that could only answer `resolved` or `unknown` — never
`unresolved` — and most links in that pane rendered white. `f3a9154de` closed
this: the resolver now fetches the link graph per loaded row's path and checks
those edges before the old title/id/basename fallback.

**A commit message overstated this once, and the record is kept rather than
quietly dropped now that the defect is closed.** `15f60b861` said the Q8 work
*"also fixes WL-1 where base links rendered white while identical note links
rendered gold."* It did not — it fixed WL-1 in embedded views only, and ADR-083
said so twice and deliberately: D8 states *"WL-1 itself is not fixed here"*, and
§7.3 *"Embedded views inherit WL-1 in whichever form Q8 chooses. This ADR does
not fix WL-1 and must not paper over it."* The ADR was right and that commit
message was wrong. The standalone half was closed later, by `f3a9154de`, which
is a different commit doing different work — so the correction stands even
though the outcome is now the one the earlier message claimed.

**How it was fixed, and the trap that was avoided.** Mirroring the note reader
literally — asking for the `.base` file's OWN outbound links — would have been a
no-op: `pkg/knowledge/graph.go` only ever opens markdown as a link source, so a
`.base` file's links answer is always empty. That change would have shipped,
passed review and fixed nothing. The wikilink actually lives in each ROW's own
markdown file, so the resolver queries per row path instead.

**What remains:** rows beyond the 40-row cap still cannot answer `unresolved`.
That is a deliberate honesty bound, not an oversight — the three-state model
exists to stop a base claiming verification it does not have, and fixing the
COLOUR without fixing the FACTS would be the dishonesty it prevents. If large
collections make the cap visible in practice, `unknown` needs to look different
from both other states and the limitation needs saying in the UI.

---

### WL-2 — `[[Daniel Piatkowski]]` renders as plain text in some notes
**Severity:** medium · **Area:** Library SPA (render path selection) · **Reported by:** founder
**Status: FIXED (`57501bca6`) — reproduced on a FOURTH surface nobody had named.**

The blocker really was reproduction, and holding the build until it landed was
the right call: all three candidate causes named below were wrong, and each was
closed by evidence rather than by argument. A `.md` file in a plain folder
already routes through `KnowledgeNoteView`, which says "link target not
verified" rather than showing brackets; the mounted vault was detected as a
knowledge base immediately; and frontmatter IS the content involved, but not on
any surface previously named.

The surface is the **Library search results**. Searching the founder's own vault
for `Daniel Piatkowski` rendered, verbatim:
`owner: "[[Daniel Piatkowski]]" share_class: ordinary …`

A search snippet is a RAW BYTE EXCERPT from the search engine — it never renders
markdown — and `NoteRow`/`RecordRow` piped it straight into `highlightQuery`, a
plain-text splitter with no wikilink awareness. This entry's own text predicted
exactly that: *"if the founder saw `[[...]]` where frontmatter lives, a DIFFERENT
surface is rendering frontmatter as text."*

The fix imports the note reader's own parser rather than writing a second one,
and renders the display text as PLAIN TEXT rather than a link — a search excerpt
cannot honestly claim the resolved/unresolved verdict a real note render can.

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
**Status: FIXED** — `65f8254d2`, verified in code 2026-09-11.

**What you get now:** the New knowledge base dialog asks for a name and nothing
else, exactly like New folder. **Verified:** `LibraryNewVaultDialog.tsx` renders
one `<Label>` in the whole file — `Name`. The `Location` label and the
`destinationLabel()` helper are gone; the workspace and the parent directory
come from where you are standing, as props.

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
**Status: FIXED** — `7a9317376`, verified in code 2026-09-11.

**What you get now:** creating a knowledge base inside another one is refused,
from the Library and from an agent alike. **Verified:** `CreateInWorkspace`
(`pkg/knowledge/detect.go`) now calls `ancestorKnowledgeBase`, which walks the
target's parent chain up to and including the workspace root and returns a new
`ErrNestedKnowledgeBase` naming the enclosing knowledge base. It was fixed in
the one shared function, as this entry asked, so both the SPA handler and the
`knowledge_base_create` tool inherit it rather than each carrying their own copy
of the rule. Two details worth knowing: the walk correctly treats "the workspace
root itself is a knowledge base" as its own case rather than folding it into
"not found", and a parent directory that does not exist on disk yet is skipped
instead of erroring.

**Still undecided, as this entry predicted:** what happens to a knowledge base
that is *already* nested on an existing install. New nesting is refused;
existing nesting is neither detected nor migrated.

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
**Status: FIXED** — `15f60b861` (the capability), on `a7992fa2d` / `368188a0d`
(the resolver and the inline renderer it needed) and `0263b7629` (the query
ceiling that makes a 15-module dashboard safe). Verified in code 2026-09-11.

**What you get now:** your dashboards render. A note containing
`![[Tasks.base#Needs Daniel]]` mounts the real base view, with real data, inline
in the note. **Verified:** `KbBaseEmbedMount` / `KbBaseEmbedContent` in
`preview/knowledgeMarkdown.tsx` resolve the file, match the written view name
against the server's own view labels (`baseViewMatch.ts`), and mount the SAME
`BasePreview` the full-screen pane uses — not a second renderer that could drift
from it.

The decisions this entry asked to be made were made, and each is visible in the
code rather than assumed:

- **A missing view is visibly unresolved, never an empty box.** A fragment that
  matches nothing says so and lists the view names that do exist.
- **An ambiguous fragment refuses and names both matches** rather than silently
  picking one.
- **No fragment at all** shows the first view and says in a caption that the
  embed did not choose it.
- **Nothing loads until you scroll to it** (`LazyEmbedMount`), and N embeds of
  the same file share ONE network request.
- **At most four view queries run at once** (`viewEvaluationPool`), so a
  15-module dashboard does not fire 15 simultaneous queries at the single Go
  binary.
- **Links inside an embedded view resolve against the note's graph** — your Q8
  ruling. See WL-1 above; both halves are now fixed, subject to that entry's
  stated 40-row query cap.

**Honestly not done, and stated in the shipping commit rather than discovered
later:** local filter/sort inside an embedded view (ADR-083 EMB-047). No filter
or sort controls exist anywhere in the view renderer yet, so there was nothing
to make embed-local.

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

**What was missing.** `KbWikilinkOptions` (`preview/knowledgeMarkdown.tsx::KbWikilinkOptions`)
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

Status re-verified against the code at `9cb1153e4` on 2026-09-11 — not against
the commit messages that claimed the fixes.

| ID | Title | Severity | Status | Commit |
|---|---|---|---|---|
| WL-1 | Base links render white; note links render gold | Medium | **Fixed**, bounded — embedded half, then the standalone base pane; rows past a 40-row cap keep the older resolved-or-unknown fallback | `15f60b861` (embedded), `f3a9154de` (standalone) |
| WL-2 | Raw `[[wikilink]]` text in some notes | Medium | **Fixed** — reproduced in Library search results, not in a note render | `57501bca6` |
| WL-3 | New KB dialog still shows a Location field | Low | **Fixed** | `65f8254d2` |
| WL-4 | A knowledge base can be created inside another | High | **Fixed** — one shared check, both paths | `7a9317376` |
| WL-5 | Base views cannot be embedded in a note (dashboards) | High | **Fixed** — dashboards render | `15f60b861`, `a7992fa2d`, `368188a0d`, `0263b7629` |

**The root theme this list opened with held all the way through, and the list
is now closed.** Wikilink rendering was correct on the note surface and partial
everywhere else, and every fix here consisted of pushing the note surface's own
mechanism outwards rather than writing a second one: into embedded views (WL-5
and WL-1's embedded half), into a standalone `.base` pane (WL-1's other half),
and into search result rows (WL-2). The one surface that deliberately does NOT
adopt it wholesale is search, which renders display text as plain text because
an excerpt cannot honestly claim a resolution verdict. What is left is not a
surface but a bound: rows past WL-1's 40-row query cap.
