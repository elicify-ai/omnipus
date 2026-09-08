# Obsidian embed capabilities — reference and gap analysis (2026-09-08)

What can be embedded inside an Obsidian note, what Omnipus supports today, and
what is missing. Sourced from the public help vault
(`github.com/obsidianmd/obsidian-help`, `en/`, last commit 2026-09-04) and the
official changelog. **`help.obsidian.md` now redirects to `obsidian.md/help/…`
and is a JavaScript shell — fetching it raw returns nothing, so the vault is the
authority.**

Claims that could not be verified are marked **UNCONFIRMED** rather than guessed.

---

## Gap analysis — Obsidian capability vs Omnipus today

Ranked by evidence of real use. Usage counts are from the founder's own migrated
vault (784 notes) and rank the work; they do NOT define the requirement — the
capability surface is Obsidian's, not the vault's.

| Capability | Syntax | Core? | Vault uses | Omnipus | Gap |
|---|---|---|---:|---|---|
| **Base, named view** | `![[File.base#View]]` | Core 1.9 | **75** | ❌ | **WL-5 — the big one** |
| Base, first view | `![[File.base]]` | Core 1.9 | 0 | ❌ | same fix |
| Base, inline | ` ```base ` fence | Core 1.9 | 0 | ❌ | same family |
| Image | `![[img.png]]` | Core | 12 | ✅ | — |
| **Image, SVG** | `![[img.svg]]` | Core | **4** | ❓ | **verify — CSP often blocks SVG** |
| Image, sized | `![[img.png\|400]]` | Core | 0 | ❓ | verify |
| External image | `![alt\|400](https://…)` | Core | 0 | ❓ | verify |
| PDF | `![[doc.pdf]]` | Core | 1 | ❓ | verify |
| PDF page / height | `![[doc.pdf#page=3]]` | Core | 0 | ❌ | likely missing |
| Note (whole) | `![[Note]]` | Core | 0 | ❌ | missing |
| Note heading | `![[Note#Heading]]` | Core | 0 | ❌ | missing |
| Note block | `![[Note#^blockid]]` | Core | 0 | ❌ | missing |
| Audio | `![[a.mp3]]` | Core | 0 | ❌ | missing |
| Video | `![[v.mp4]]` | Core (undocumented) | 0 | ❌ | missing |
| Canvas | `![[b.canvas]]` | Core | 0 | ❌ | missing (shapes only even in Obsidian) |
| Search results | ` ```query ` fence | Core | 0 | ❌ | missing |
| Mermaid | ` ```mermaid ` fence | Core | **163** | ✅ | already works, different mechanism |
| Math | `$x$` / `$$x$$` | Core | 0 | ❓ | verify |
| Iframe / YouTube / tweet | `<iframe>` / `![](url)` | Core | 0 | ❌ | missing |
| Dataview, Excalidraw, Kanban board, Templater, Charts, ABC, Admonition | various | **Plugin** | 0 | n/a | **NOT a compatibility requirement** |

**Reading the table:** the single highest-value gap is Base views — 79% of every
embed in the vault, and 100% of those name a specific view. Mermaid, the second
most-used renderer at 163 fences, already works.

---

## Traps — things that would be built wrong from a naive reading

These are the corrections that justify doing the research rather than assuming.

1. **Bases MAP view is NOT core.** It requires the community **Maps** plugin, so
   a Map view fails on a stock install. Bases KANBAN (1.14) is **early-access
   only** — the latest public build is 1.13.7. Table, Cards and List are core.
2. **`![[Search#…]]` is not real syntax.** It appears to be on the embeddables
   list, but it is the help vault transcluding its own note *titled* "Search".
   The only core search embed is the ` ```query ` fence.
3. **`|width` silently no-ops on video AND audio.** It parses, sets an HTML
   attribute, and resizes nothing. Every resize feature in Obsidian's history is
   images-only.
4. **External images need the PIPE form** — `![alt|400](url)`, not `![400](url)`.
   The bare-number form only began working in Reading mode in 1.13.2
   (2026-07-14) and still renders unsized on Obsidian Publish.
5. **`#outline`, `#interface`, `#icon` are NOT features.** They are CSS selectors
   in the help site's own `publish.css`. What matters is the underlying
   mechanism: Obsidian preserves the raw target INCLUDING the fragment in the
   wrapper's `src`, which buys theme compatibility for free.
6. **Canvas embeds show shapes but NOT card text** — documented, verbatim. An
   embedded canvas is effectively a wireframe.
7. **Math is delimiter-based, never a fence**, and **1.14 replaced MathJax with
   Temml**.
8. **Mermaid gained a one-time per-vault approval banner in 1.13** before it will
   render.
9. **Only three first-party rendering fences exist**: `mermaid`, `query`, `base`.
   Everything else goes through the public plugin API.

---

## Embedded-Base behaviour worth designing for

- **The embed is interactive** — it renders the full Bases toolbar (view switcher,
  sort, filter, properties, search, new).
- **Interaction is NOT scoped to the embed.** Changes write back to the shared
  `.base` file, so filtering an embedded base in one note changes every other
  embed of it. Users write CSS to hide the toolbar, which is itself evidence it
  is present by default. **Decide deliberately whether Omnipus embeds are
  read-only** — Obsidian's shared-state behaviour is arguably a defect to avoid
  rather than a contract to match.
- **Context switching is documented and load-bearing:** when a base is embedded,
  `this` refers to properties of the EMBEDDING file, not the base. That also
  confirms bases embed inside Canvas, not only notes.

## Note-embed behaviour worth designing for

- Embeds are **transitive** (A embeds B embeds C → C appears in A) with a
  documented depth cap of **5 layers** (0.8.15, 2020).
- Past the cap the embed **degrades silently to a bare file reference** — filename
  shows, content does not. No error, no placeholder.
- ⚠️ **There is no cycle detection.** The evidence points to a plain depth counter
  that merely bounds the loop. A 2021 report of PDF export freezing at 100% CPU
  on mutually recursive embeds was never closed. **Implement real cycle detection
  rather than copying the depth counter.**

## DOM contract (captured live; documented nowhere)

Worth matching for theme compatibility:

| Kind | Markup |
|---|---|
| Note | `<span src="Note#^id" class="internal-embed markdown-embed is-loaded"><div class="markdown-embed-content">` |
| Image + size | `<span width="100" src="img.jpg#outline" class="internal-embed image-embed is-loaded"><img width="100">` |
| Audio | `<span class="internal-embed media-embed audio-embed is-loaded"><audio controls controlslist="nodownload">` |
| Video | `<span class="internal-embed media-embed video-embed is-loaded"><video controls preload="metadata">` |

Two details that will bite: size is an HTML `width` **attribute** (not CSS), and
`alt` is inconsistent — `"file > fragment"` without a size, raw `"file#fragment"`
with one. **Parse order is fragment first, size second.**

## Accepted file formats

- **Image:** `.avif .bmp .gif .jpeg .jpg .png .svg .webp`
- **Audio:** `.flac .m4a .mp3 .ogg .wav .webm .3gp`
- **Video:** `.mkv .mov .mp4 .ogv .webm` (`.webm` resolves as VIDEO since 1.0.1)
- Codec support is explicitly device-dependent, per Obsidian's own docs.

## SVG specifics

SVG goes through the ordinary image path (`<img src>`), so scripts inside do not
execute — but that is a BROWSER guarantee, not an Obsidian one. **UNCONFIRMED:**
nothing SVG-specific was found in Obsidian's sanitiser. Two real caveats: an SVG
whose root lacks `width`/`height`/`viewBox` is cropped to 300×150 (open since
2021), and inline `<svg>` markup pasted into a note breaks in Reading mode
(WONTFIX).

---

## Open items — stated honestly

1. **Depth cap still 5** — documented 2020, unchanged in the changelog, last
   user-corroborated 2024; not re-verified against a current build.
2. **Circular-embed handling** — no evidence of true cycle detection; sources
   conflict.
3. **Live Preview vs Reading view** on depth and cycles — no source compares them.
4. **SVG sizing** — works in practice; no documentation states it for SVG.
5. **Bases GA date** — 1.9.10 (2025-08-18) is the best marker; Obsidian never
   announced Bases leaving beta.
6. **Video `#t=` timestamps** — evidence says unsupported, not definitively ruled
   out.

## Recommended order of work

1. **Base named-view embeds** (WL-5) — 75 uses, blocks every dashboard.
2. **Verify SVG and PDF actually render** — 5 uses between them, and SVG is
   commonly blocked by CSP. Cheap to check, embarrassing to assume.
3. **Note / heading / block embeds** — zero uses today but the most fundamental
   Obsidian idiom; a vault written in Obsidian will grow them.
4. **Image sizing + external images** — small, well-specified.
5. **Audio, video, PDF fragments, canvas, query fences, iframes** — no evidenced
   use; build when a real need appears.
