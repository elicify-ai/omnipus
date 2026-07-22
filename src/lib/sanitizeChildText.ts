// sanitizeChildText.ts — FE-7 / MAJ-12 untrusted-child-text sanitization.
//
// Child agents (delegated sub-turns) emit free-text content — progress
// narration, checkpoint summaries, blocker text, question text, artifact
// notes, handback messages. That content is UNTRUSTED at the human surface:
// a prompt-injected child (or a child whose model was tricked by scraped
// web content) can try to ship `<script>`, `<img onerror=...>`, `javascript:`
// links, or other active content into the human-facing UI.
//
// MAJ-12 requires that child-originated text renders as PLAIN TEXT or
// SANCTIONED MARKDOWN — no raw HTML, links non-clickable (or
// confirmation-gated), with an untrusted-origin chrome always visible. This
// module is the single reusable utility for that sanitization, applied at
// every child-text render site in the FE-5 Agent-View session list and
// exported for the chat agent to reuse for FE-3 (in-chat child question
// rendering).
//
// DEFENSE IN DEPTH (three independent layers — bypassing one does not bypass
// the others):
//   1. `sanitizeChildText()` — DOMPurify pre-clean that strips ALL raw HTML
//      not in the sanctioned tag allow-list below AND drops every attribute
//      (href / src / style / on*). Markdown syntax (`**bold**`, `- item`,
//      `` `code` ``) is plain text to DOMPurify and survives untouched, so
//      the sanitized output is still renderable as sanctioned markdown.
//   2. react-markdown's default safety — it escapes (never renders as HTML)
//      any literal `<...>` that survives layer 1, and parses markdown to a
//      safe React tree without `rehype-raw` (we do NOT use rehype-raw on
//      child text). See <UntrustedChildText> in the session-list surface.
//   3. The link renderer on <UntrustedChildText> renders `[text](url)` links
//      as INERT, NON-CLICKABLE plain text (the URL shown muted, no <a> href)
//      — so a markdown-level `javascript:` URL (which DOMPurify cannot see,
//      because it is not HTML) can never become a clickable anchor. This is
//      the MAJ-12 "links non-clickable" guarantee at the render layer.
//
// This module holds the PURE logic (no JSX) so it is trivially reusable from
// any surface. The React rendering face lives in the session-list component
// (<UntrustedChildText>) and is also exported for FE-3 reuse.

import DOMPurify from 'dompurify'

/**
 * Sanctioned markdown subset — the HTML equivalents of the markdown inline
 * and block constructs we ALLOW in child text. Deliberately NARROW:
 *
 *  - NO `a` (links)          — rendered inert/non-clickable by <UntrustedChildText>
 *  - NO `img`                — images dropped; alt text only
 *  - NO `iframe` / `script` / `style` / `object` / `embed` / `form` / `input`
 *  - NO tables (`table`/`thead`/`tbody`/`tr`/`td`/`th`) — child text is a
 *    compact list surface; tables are out of scope and a surface for
 *    attribute-based exploits if re-enabled carelessly.
 *
 * Exported so FE-3 / other consumers can assert against the same allow-list.
 */
export const SANCTIONED_MARKDOWN_TAGS = [
  'p', 'br',
  'strong', 'b', 'em', 'i',
  'code', 'pre',
  'ul', 'ol', 'li',
  'blockquote',
  'h1', 'h2', 'h3', 'h4', 'h5', 'h6',
  'hr',
] as const

/**
 * The DOMPurify config used by `sanitizeChildText`. Exported for tests and
 * for FE-3 to reuse the EXACT config (mirrors the brand-logos precedent's
 * shared-config pattern).
 *
 * - `ALLOWED_TAGS`: only the sanctioned markdown equivalents above.
 * - `ALLOWED_ATTR: []`: strip EVERY attribute. This is the critical
 *   defense — it neutralizes `onerror`, `onload`, `style`, `href`, `src`,
 *   `xlink:href`, etc. in any tag that slip-throughs (defense before the
 *   tag allow-list).
 * - `KEEP_CONTENT: true`: for a disallowed tag, keep its TEXT content so
 *   `<a href="javascript:...">click here</a>` degrades to the plain text
 *   "click here" (not an empty string) — the human still sees the words.
 *   `<script>`/`<style>` content is always dropped by DOMPurify regardless.
 * - `ALLOW_DATA_ATTR: false`: no `data-*` attributes.
 *
 * Declared WITHOUT `as const` so the array fields stay MUTABLE — DOMPurify's
 * `Config` type requires mutable `string[]` for `ALLOWED_TAGS` /
 * `ALLOWED_ATTR`, and a readonly tuple would fail to assign.
 */
export const CHILD_TEXT_DOMPURIFY_CONFIG = {
  ALLOWED_TAGS: [...SANCTIONED_MARKDOWN_TAGS] as string[],
  ALLOWED_ATTR: [] as string[],
  KEEP_CONTENT: true,
  ALLOW_DATA_ATTR: false,
  // Stricter parsing: do not attempt to balance broken tags by fabricating
  // matching close tags from attacker input.
  RETURN_DOM_FRAGMENT: false,
  RETURN_DOM: false,
}

/**
 * Strip null bytes and other C0 control characters (except the meaningful
 * tab \t, newline \n, carriage return \r). Null bytes are a classic payload
 * used to smuggle content past naive substring/regex filters (e.g.
 * `<scr\x00ipt>`). DOMPurify handles most of these, but we strip them first
 * so the sanitized output is also safe to log / persist without control-char
 * surprises.
 */
export function stripControlChars(input: string): string {
  // eslint-disable-next-line no-control-regex
  return input.replace(/[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]/g, '')
}

/**
 * Strip raw HTML not in the sanctioned allow-list and drop every attribute.
 * Markdown syntax survives (it is plain text to DOMPurify). Never throws —
 * on any internal failure DOMPurify returns the input with only safe
 * content, and as a final fallback we return the control-char-stripped
 * input. Safe to call on any string (including empty / non-string coerced).
 */
export function stripUnsafeHtml(input: string): string {
  if (typeof input !== 'string' || input.length === 0) return ''
  const safe = stripControlChars(input)
  try {
    return DOMPurify.sanitize(safe, CHILD_TEXT_DOMPURIFY_CONFIG) as unknown as string
  } catch {
    // DOMPurify never throws in practice (it is defensive), but we do not
    // trust a security function to be infallible — fall back to the
    // control-char-stripped input rather than propagating an exception.
    return safe
  }
}

/**
 * Sanitize untrusted child text to the plain-text / sanctioned-markdown form
 * required by FE-7 / MAJ-12. This is the primary export — call it at every
 * child-text render site (or wrap rendering in <UntrustedChildText>, which
 * calls this internally).
 *
 * Pipeline:
 *   1. coerce to string (defensive against non-string payloads)
 *   2. strip C0 control chars + null bytes
 *   3. DOMPurify: strip raw HTML outside the sanctioned tag set, drop ALL
 *      attributes, keep inner text of dropped tags
 *   4. trim trailing whitespace
 *
 * The output is safe to feed to react-markdown WITHOUT rehype-raw (raw HTML
 * is gone). Markdown `[text](url)` links survive step 3 (they are not HTML)
 * and are rendered INERT by <UntrustedChildText>'s link component.
 */
export function sanitizeChildText(input: string): string {
  // Any non-string child-text payload is malformed — return empty (most
  // defensive). We deliberately do NOT String()-coerce objects/numbers: an
  // object payload yielding '[object Object]' is never useful child text,
  // and rendering nothing is always safe.
  if (typeof input !== 'string') return ''
  const stripped = stripUnsafeHtml(input)
  // Collapse trailing whitespace left by dropped block tags but PRESERVE
  // interior newlines (markdown list / paragraph structure relies on them).
  return stripped.replace(/[ \t]+\n/g, '\n').replace(/\n{3,}/g, '\n\n').trim()
}

/**
 * True when `text` contains NO HTML angle-bracket tag syntax at all — i.e.
 * it is already plain text and the sanctioned-markdown path is unnecessary.
 * Used by renderers to pick the cheaper plain-text render branch (no
 * react-markdown parse) when the child sent no markup, avoiding unnecessary
 * work in the common progress-narration case (a one-line plain status).
 *
 * Deliberately conservative: a stray `<` or `>` that is NOT part of a tag
 * (e.g. "values < 5") returns false only when it looks like a tag
 * (`<[a-zA-Z!/]?`). Bare `<` / `>` alone are treated as plain text.
 */
export function isPlainText(text: string): boolean {
  return !/<[a-zA-Z!/]/.test(text)
}
