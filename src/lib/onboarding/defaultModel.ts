// defaultModel — pick a capable default model from a freshly-loaded list.
//
// UAT fix (onboarding default model): after "Connect & Load Models", the
// onboarding flow must pre-select a *capable* default instead of leaving the
// field empty (the user could submit nothing) or first-in-list (the first
// alphabetical entry is often a tiny / preview / 404-ing model such as
// `…fable-latest`).
//
// Preference order:
//   1. A GLM 5.x family entry — `glm-5.2` is the operator's preferred default;
//      otherwise any `glm-5*` / `z-ai/glm-5*` slug.
//   2. A current frontier model from a major vendor (Claude / Gemini / GPT),
//      preferring the non-mini / non-nano / non-preview variants.
//   3. The first entry in the list as a last resort (never empty when the list
//      is non-empty).
//
// Matching is case-insensitive and tolerant of provider/vendor prefixes
// ("z-ai/glm-5.2", "anthropic/claude-sonnet-4-5", "openai/gpt-4o").

/** Lowercased haystack helper. */
function lc(s: string): string {
  return s.trim().toLowerCase()
}

/** True for slugs we never want to auto-select (tiny / preview / deprecated). */
function isUndesirable(slug: string): boolean {
  const s = lc(slug)
  return (
    s.includes('fable') ||
    s.includes('preview') ||
    s.includes('-nano') ||
    s.includes('nano-') ||
    s.includes('embedding') ||
    s.includes('whisper') ||
    s.includes('tts') ||
    s.includes('moderation')
  )
}

/**
 * Pick a capable default from `models`. Returns '' only when the list is empty.
 */
export function pickCapableDefaultModel(models: readonly string[]): string {
  const list = models.filter((m) => m.trim() !== '')
  if (list.length === 0) return ''

  const usable = list.filter((m) => !isUndesirable(m))
  const pool = usable.length > 0 ? usable : list

  // 1. Exact GLM 5.2 preference, then any GLM 5.x.
  const glm52 = pool.find((m) => lc(m).includes('glm-5.2'))
  if (glm52) return glm52
  const glm5 = pool.find((m) => /glm-5/.test(lc(m)))
  if (glm5) return glm5

  // 2. Frontier vendors. For each family prefer a "flagship" tier first
  //    (sonnet / pro / a plain gpt-4o-class) before the lighter variants.
  const families: Array<{ match: (s: string) => boolean; flagship: (s: string) => boolean }> = [
    {
      match: (s) => s.includes('claude'),
      flagship: (s) => s.includes('sonnet') || s.includes('opus'),
    },
    {
      match: (s) => s.includes('gemini'),
      flagship: (s) => s.includes('pro'),
    },
    {
      match: (s) => s.includes('gpt') || s.includes('o3') || s.includes('o4'),
      flagship: (s) => !s.includes('mini') && !s.includes('nano'),
    },
  ]
  for (const fam of families) {
    const members = pool.filter((m) => fam.match(lc(m)))
    if (members.length === 0) continue
    const flagship = members.find((m) => fam.flagship(lc(m)))
    return flagship ?? members[0]
  }

  // 3. Last resort — first usable entry.
  return pool[0]
}
