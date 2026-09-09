// baseViewMatch.ts — matches a `.base` embed's written fragment
// (`![[Tasks.base#Needs Daniel]]`) against the views GET
// /knowledge/base-views actually reports for that file (ADR-083 EMB-040
// through EMB-042).
//
// THE SERVER'S SLUG IS THE ONLY ADDRESS (KnowledgeBaseView.yaml). A fragment
// names a DISPLAY LABEL, never a machine name — the machine name is only
// tried as a last-resort fallback, for the rare note that was hand-written
// against the slug directly, and this function never CONSTRUCTS one: every
// candidate it compares against comes from the server's own `views` list.
//
// EMB-041's matching ladder, in order, stopping at the first step that
// produces ANY match (one or more) — never falling through past a step that
// matched at all, even ambiguously:
//   1. exact label
//   2. case-insensitive label
//   3. machine name (exact)
// Zero matches at every step is `not_found`. More than one match at whichever
// step first produced one is `ambiguous` (EMB-042) — refused, naming every
// view that matched, never silently picking the first.

import type { KnowledgeBaseView } from '@/lib/api/generated/openapi-types'

export type BaseViewMatch =
  | { kind: 'matched'; view: KnowledgeBaseView; chosenByDefault: boolean }
  | { kind: 'ambiguous'; label: string; matches: KnowledgeBaseView[] }
  | { kind: 'not_found' }

/**
 * `fragment` is the raw text after `#` in `![[File.base#fragment]]`, or
 * `undefined` when the embed named no view at all — EMB-043's case, which
 * this function answers by choosing the first declared view (`chosenByDefault:
 * true`) rather than refusing, since "no view was named" is not the same
 * fact as "the named view does not exist".
 */
export function matchBaseView(
  views: readonly KnowledgeBaseView[],
  fragment: string | undefined,
): BaseViewMatch {
  if (fragment === undefined || fragment === '') {
    if (views.length === 0) return { kind: 'not_found' }
    return { kind: 'matched', view: views[0] as KnowledgeBaseView, chosenByDefault: true }
  }

  const byExactLabel = views.filter((v) => v.label === fragment)
  if (byExactLabel.length === 1) {
    return { kind: 'matched', view: byExactLabel[0] as KnowledgeBaseView, chosenByDefault: false }
  }
  if (byExactLabel.length > 1) return { kind: 'ambiguous', label: fragment, matches: byExactLabel }

  const lower = fragment.toLowerCase()
  const byCiLabel = views.filter((v) => v.label.toLowerCase() === lower)
  if (byCiLabel.length === 1) {
    return { kind: 'matched', view: byCiLabel[0] as KnowledgeBaseView, chosenByDefault: false }
  }
  if (byCiLabel.length > 1) return { kind: 'ambiguous', label: fragment, matches: byCiLabel }

  const byName = views.filter((v) => v.name === fragment)
  if (byName.length === 1) {
    return { kind: 'matched', view: byName[0] as KnowledgeBaseView, chosenByDefault: false }
  }
  if (byName.length > 1) return { kind: 'ambiguous', label: fragment, matches: byName }

  return { kind: 'not_found' }
}
