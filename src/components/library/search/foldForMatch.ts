// foldForMatch.ts — UAT 2026-09-13 D-129 (web half).
//
// The search bar's client-side matching — the per-term coverage chips, the
// "which cells explain this hit" ordering and the excerpt highlight — did a
// bare `toLowerCase().includes()`. That is a THIRD folding rule beside the
// engine's two (the index folded `ü` and not `é`), and it contradicts the
// server the moment the server folds: a `cafe` hit on "Café" would carry a
// "cafe: not found" chip and no highlight, although it appeared BECAUSE it
// matched. Every client-side comparison now goes through one rule:
// lower-case, Unicode-decompose (NFD), drop combining marks — so `cafe`,
// `café` (NFC) and `café` (NFD, `e` + U+0301) are the same word here, as
// they must be on screen where they are indistinguishable.
//
// Provenance for the hit itself stays with the engine; this only stops the
// bar from disagreeing with it about the words it was given.

/** One folding rule for every client-side comparison in the search bar. */
export function foldForMatch(s: string): string {
  return s.normalize('NFD').replace(/\p{M}+/gu, '').toLowerCase()
}

/** Folds `text` for matching while keeping a map back to the original
 *  string, so a match found on the folded text can be highlighted on the
 *  text the reader actually sees. `map[i]` is the index in `text` of the
 *  original code point that produced folded character `i`;
 *  `map[folded.length]` is `text.length`. A source combining mark folds to
 *  nothing and so has no entry; a code point that folds to several
 *  characters (rare — e.g. `İ`) maps each of them to its own start. */
export function foldWithMap(text: string): { folded: string; map: number[] } {
  let folded = ''
  const map: number[] = []
  let origIdx = 0
  for (const ch of text) {
    const piece = foldForMatch(ch)
    for (let k = 0; k < piece.length; k++) map.push(origIdx)
    folded += piece
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
