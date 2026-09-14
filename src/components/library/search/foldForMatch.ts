// foldForMatch.ts — UAT 2026-09-13 D-129 (web half), revised 2026-09-14 to
// mirror the SERVER's folding table (Claude review cut-list).
//
// The search bar's client-side matching — the per-term coverage chips, the
// "which cells explain this hit" ordering and the excerpt highlight — did a
// bare `toLowerCase().includes()`. That is a THIRD folding rule beside the
// engine's two, and it contradicts the server the moment the server folds: a
// `cafe` hit on "Café" would carry a "cafe: not found" chip and no highlight,
// although it appeared BECAUSE it matched.
//
// The first fix made every client-side comparison use ONE rule: lower-case,
// NFD, drop combining marks. That closed the D-129 case (accents that
// decompose) but kept a quieter disagreement with the index: the index's
// "en_folded" analyzer (pkg/knowledge/analyzer_folded.go) folds with bleve's
// asciifolding table, which maps letters NFD cannot decompose — ø, Ø, ß, Ł,
// ł, Æ, Þ, ligatures... — while NFD+mark-strip leaves them alone. "Søren" was
// indexed under "soren", the client folded it to "søren", and a genuine hit
// for the query `soren` carried a not-found coverage chip and no highlight.
//
// The rule is now the server's, mirrored exactly:
//
//   1. lower-case ONE CODE POINT AT A TIME — the same context-free, per-rune
//      casing Go's analyzer applies (String.prototype.toLowerCase on a whole
//      string additionally applies Unicode's Final_Sigma rule, so 'ΟΔΟΣ'
//      whole-string is 'οδος' but per-rune is 'οδοσ'; the two spellings must
//      not flip depending on which of this module's functions ran, and the
//      per-rune form is what the server produces);
//   2. the generated ASCII_FOLD_TABLE — a byte-for-byte mirror of the
//      bleve v2.6.1 asciifolding entries (see asciiFoldTable.ts for
//      provenance and the regeneration recipe);
//   3. a combining mark that composes with the character before it
//      (NFC(prev+mark) is one code point) folds to nothing — that is what
//      the server's NFC-before-folding step does to an NFD spelling such as
//      "café (1).png" written the way macOS names files. A mark that does
//      NOT compose survives, exactly as it survives the server's pipeline.
//
// Provenance for the hit itself stays with the engine; this only stops the
// bar from disagreeing with it about the words it was given.

import { ASCII_FOLD_TABLE } from './asciiFoldTable'

/** One lower-cased code point's folded form. `prev` is the previous
 *  lower-cased code point, needed only for the composing-mark rule. */
function foldCodePoint(prev: string, ch: string): string {
  const hit = ASCII_FOLD_TABLE.get(ch)
  if (hit !== undefined) return hit
  if (/\p{M}/u.test(ch) && prev !== '' && (prev + ch).normalize('NFC').length === 1) {
    return ''
  }
  return ch
}

/** One folding rule for every client-side comparison in the search bar —
 *  the same walk foldWithMap performs, so the two can never disagree. */
export function foldForMatch(s: string): string {
  return foldWithMap(s).folded
}

/** Folds `text` for matching while keeping a map back to the original
 *  string, so a match found on the folded text can be highlighted on the
 *  text the reader actually sees. `map[i]` is the index in `text` of the
 *  original code point that produced folded character `i`;
 *  `map[folded.length]` is `text.length`. A source combining mark that
 *  composes with its predecessor folds to nothing and so has no entry; a
 *  code point that folds to several characters (e.g. `ß` -> `ss`) maps each
 *  of them to its own start. */
export function foldWithMap(text: string): { folded: string; map: number[] } {
  let folded = ''
  const map: number[] = []
  let origIdx = 0
  // The previous code point AFTER lower-casing but BEFORE folding — the
  // composing-mark rule composes against the character as written, not
  // against its folded replacement.
  let prevRaw = ''
  for (const ch of text) {
    // Per code point, deliberately: see the header's step 1.
    const lowered = ch.toLowerCase()
    let out = ''
    for (const lc of lowered) {
      out += foldCodePoint(prevRaw, lc)
      prevRaw = lc
    }
    for (let k = 0; k < out.length; k++) map.push(origIdx)
    folded += out
    origIdx += ch.length
  }
  map.push(text.length)
  return { folded, map }
}

/** The [start, end) ranges of `text` (original indices) that match any of
 *  `terms` under foldForMatch, left to right, non-overlapping — the input
 *  to a highlighter that must slice the original, unfolded text. Terms are
 *  folded too, so a query typed with or without its accents finds the same
 *  spans. Empty terms are ignored. */
export function foldedMatchRanges(text: string, terms: readonly string[]): [number, number][] {
  const folded = terms.map(foldForMatch).filter((t) => t.length > 0)
  if (folded.length === 0) return []
  const { folded: hay, map } = foldWithMap(text)
  const out: [number, number][] = []
  let pos = 0
  while (pos < hay.length) {
    let bestStart = -1
    let bestLen = 0
    for (const t of folded) {
      const at = hay.indexOf(t, pos)
      if (at === -1) continue
      if (bestStart === -1 || at < bestStart || (at === bestStart && t.length > bestLen)) {
        bestStart = at
        bestLen = t.length
      }
    }
    if (bestStart === -1) break
    const start = map[bestStart] ?? text.length
    const end = map[bestStart + bestLen] ?? text.length
    if (end > start) out.push([start, end])
    pos = bestStart + bestLen
  }
  return out
}
