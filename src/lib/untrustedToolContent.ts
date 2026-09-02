// SEC-25 PromptGuard wrapper handling for tool results rendered in the SPA.
//
// Backend: pkg/security/promptguard.go's PromptGuard.Sanitize wraps the
// result of any tool classified as "untrusted" (pkg/agent/prompt_guard.go's
// untrustedToolResults — web_search, web_fetch, read_file, and the browser.*
// family except browser.screenshot) before it re-enters the LLM's context:
//
//   - Low strictness:    "[UNTRUSTED_CONTENT]\n<content>\n[/UNTRUSTED_CONTENT]"
//   - Medium strictness: same wrapper, plus known injection phrases inside
//     <content> get an invisible zero-width non-joiner (U+200C) spliced in —
//     this does not add or remove any JSON structural characters.
//   - High strictness:   the entire result is replaced with the static
//     placeholder "[UNTRUSTED_CONTENT_REDACTED_FOR_SUMMARIZATION]".
//
// That same sanitized string — not a separate "raw" copy — is what the
// gateway emits to the SPA as the tool result (ToolExecEndPayload.Result in
// pkg/agent/loop.go's runTurn). So any renderer for an untrusted-classified
// tool that expects raw JSON/text back must unwrap this marker first.

const UNTRUSTED_OPEN = '[UNTRUSTED_CONTENT]'
const UNTRUSTED_CLOSE = '[/UNTRUSTED_CONTENT]'
const UNTRUSTED_REDACTED = '[UNTRUSTED_CONTENT_REDACTED_FOR_SUMMARIZATION]'

/**
 * Strips the SEC-25 PromptGuard untrusted-content wrapper from a tool
 * result string, if present.
 *
 * - No wrapper present → returns `raw` unchanged.
 * - Low/Medium wrapper → returns the inner content (still needs its own
 *   parsing, e.g. JSON.parse, by the caller).
 * - High-strictness full redaction → returns `null` (there is no inner
 *   content to recover; the caller should show a "content withheld"
 *   notice rather than treat this as a malformed payload).
 */
export function stripUntrustedContentWrapper(raw: string): string | null {
  const trimmed = raw.trim()
  if (trimmed === UNTRUSTED_REDACTED) {
    return null
  }
  if (trimmed.startsWith(UNTRUSTED_OPEN) && trimmed.endsWith(UNTRUSTED_CLOSE)) {
    return trimmed.slice(UNTRUSTED_OPEN.length, trimmed.length - UNTRUSTED_CLOSE.length).trim()
  }
  return raw
}
