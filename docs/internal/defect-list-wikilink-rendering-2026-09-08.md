# Defect list — wikilink rendering (2026-09-08, round 2)

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

## Summary

| ID | Title | Severity | Status |
|---|---|---|---|
| WL-1 | Base links render white; note links render gold | Medium | Open — cause confirmed |
| WL-2 | Raw `[[wikilink]]` text in some notes | Medium | Open — mechanism confirmed, surface unreproduced |

**Both trace to the same root theme** as the KB-8 work: wikilink rendering is
correct on the note surface and partial everywhere else. WL-1 is a resolver
scoped too narrowly; WL-2 is a render path that never parses wikilinks at all.
Fixing them together, by making the note surface's mechanism the single one every
surface uses, is likely cheaper than two separate fixes.
