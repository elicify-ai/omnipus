// api-error.rateLimit.test.ts — F5a (SILENT-FAILURES-rate-limits-dd25339bf.md).
//
// The shared 429 message ("Too many requests. Please slow down and try
// again shortly.") drops the server's stated wait time even though ApiError
// already parses it (retryAfterMs), and gives a caller no way to say a WRITE
// specifically was refused (not saved) rather than merely "slow down".
//
// Two changes here:
//   1. `defaultUserMessage(429, retryAfterMs)` includes the real wait when
//      known — a safe, generic improvement (no server-internal text leaked)
//      that benefits every 429 caller app-wide, not just Library.
//   2. `rateLimitedWriteRefusalMessage(err)` — a NEW, write-specific helper
//      that says explicitly the change was NOT SAVED, recovers the server's
//      own specific reason from `.body` when safely recoverable (the same
//      pattern `getLibraryErrorMessage` already uses, duplicated locally
//      here rather than imported — api-error.ts is a foundational lib file
//      and must not depend on component-tree code), and names the wait.
//
// Deliberately NOT changed here: `defaultUserMessage`'s "known status ->
// generic message" override for 401/403/404/408/409/410/413/5xx. See this
// suite's own "existing per-status override policy" describe block, and the
// FIX4 report's audit section, for why that stays as-is.

import { describe, it, expect } from 'vitest'
import { ApiError, rateLimitedWriteRefusalMessage } from './api-error'

describe('ApiError — 429 default message includes the real wait (F5a)', () => {
  it('THE DEFECT: a 429 with a parsed retryAfterMs used to drop it entirely', () => {
    const err = new ApiError(429, '', { retryAfterMs: 45_000 })
    expect(err.userMessage).toMatch(/45s/)
  })

  it('falls back to the plain generic line when no retryAfterMs is known', () => {
    const err = new ApiError(429, '')
    expect(err.userMessage).toMatch(/Too many requests/i)
    expect(err.userMessage).not.toMatch(/\ds\b/)
  })

  it('fromResponse-style construction (userMessage empty, retryAfterMs from header) also includes the wait', async () => {
    const res = new Response('', {
      status: 429,
      headers: { 'retry-after': '30', 'content-type': 'application/json' },
    })
    const err = await ApiError.fromResponse(res)
    expect(err.userMessage).toMatch(/30s/)
    expect(err.retryAfterMs).toBe(30_000)
  })
})

describe('rateLimitedWriteRefusalMessage — F5a, "not saved" framing for a refused WRITE', () => {
  it('THE DEFECT reproduction: the shared userMessage never says the change was not saved', () => {
    // Before this function existed, the only text a write-refusal handler
    // had was err.userMessage — which never mentions "saved" at all.
    const err = new ApiError(429, 'Too many requests. Please slow down and try again shortly.')
    expect(err.userMessage).not.toMatch(/not saved/i)
  })

  it('says explicitly the change was NOT saved, and names the real wait', () => {
    const err = new ApiError(429, '', { retryAfterMs: 12_000 })
    const message = rateLimitedWriteRefusalMessage(err)
    expect(message).toMatch(/not saved/i)
    expect(message).toMatch(/12s/)
  })

  it('recovers the server\'s own specific reason from the response body when present', () => {
    // The knowledge limiter's actual wire shape (SILENT-FAILURES report,
    // rest_knowledge.go): {"error": "too many knowledge requests — at most
    // 60 per 1m0s. Retry in 45s."}. fromResponse's known-status override
    // replaces userMessage with the generic line, but the specific text
    // survives on `.body` — recover it, same pattern as getLibraryErrorMessage.
    const err = new ApiError(429, 'Too many requests. Please slow down and try again shortly.', {
      body: '{"error":"too many knowledge requests — at most 60 per 1m0s. Retry in 45s."}',
      retryAfterMs: 45_000,
    })
    const message = rateLimitedWriteRefusalMessage(err)
    expect(message).toMatch(/not saved/i)
    expect(message).toMatch(/too many knowledge requests/i)
  })

  it('falls back to a safe generic reason when the body is absent or unparseable', () => {
    const err = new ApiError(429, 'generic', { retryAfterMs: 5_000 })
    const message = rateLimitedWriteRefusalMessage(err)
    expect(message).toMatch(/not saved/i)
    expect(message).toMatch(/too many requests/i)
    expect(message).toMatch(/5s/)
  })

  it('still produces a sensible message with no retryAfterMs at all', () => {
    const err = new ApiError(429, 'generic')
    const message = rateLimitedWriteRefusalMessage(err)
    expect(message).toMatch(/not saved/i)
    expect(message.toLowerCase()).not.toContain('undefined')
    expect(message.toLowerCase()).not.toContain('nan')
  })
})

// ── Existing per-status override policy (audit — see FIX4 report) ─────────────
//
// api-error.ts's `defaultUserMessage` deliberately overrides `userMessage`
// with a generic, safe string for "known" statuses (401/403/404/408/409/
// 410/413/429/5xx) rather than the server's raw `error` field — documented
// in `ApiError.fromResponse`'s own comment ("leaks server-internal phrasing
// into the user-facing toast... surprises tests that assume userMessage
// matches defaultUserMessage(status)"). `getLibraryErrorMessage`
// (src/components/library/libraryErrorMessage.ts) already recovers the
// server's specific reason where a SCREEN needs it, deliberately scoped
// rather than reversing this file's app-wide policy — its own header states
// exactly that. This suite locks in that 403/409 stay generic here (56+
// call sites across the app read `.userMessage`/`getErrorMessage` directly
// and were never audited for whether raw server text is safe to show them);
// a screen that needs the real reason uses a scoped helper, the same way
// Library already does.
describe('existing per-status override — 403/409 stay generic on the SHARED path (by design, unchanged)', () => {
  it('a 403 carrying a specific server reason in its body still gets the generic userMessage from fromResponse', async () => {
    const res = new Response(JSON.stringify({ error: 'mount target refused: /etc is an operating-system directory' }), {
      status: 403,
      headers: { 'content-type': 'application/json' },
    })
    const err = await ApiError.fromResponse(res)
    // The specific reason is NOT lost — it survives on `.body` for a scoped
    // caller (e.g. getLibraryErrorMessage) to recover.
    expect(err.body).toContain('/etc is an operating-system directory')
    // But the shared userMessage stays the safe generic line.
    expect(err.userMessage).toBe("You don't have permission to perform this action.")
  })

  it('a 409 carrying a specific server reason in its body still gets the generic userMessage from fromResponse', async () => {
    const res = new Response(JSON.stringify({ error: 'agent name "Mia" already exists' }), {
      status: 409,
      headers: { 'content-type': 'application/json' },
    })
    const err = await ApiError.fromResponse(res)
    expect(err.body).toContain('already exists')
    expect(err.userMessage).toBe('This conflicts with the current state. Please refresh and try again.')
  })
})
