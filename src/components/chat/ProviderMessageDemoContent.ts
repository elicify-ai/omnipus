import { useEffect, useState } from 'react'

/**
 * Provider message visual options — design-exploration mock content (PM-6).
 *
 * Static and presentational only: no chat store, no frames, no API calls.
 * The four provider-message cases the founder asked to compare are driven
 * from this one module so every visual option renders identical content and
 * the comparison stays like for like.
 *
 * Exploration scaffolding for the provider-messages design track (branch
 * feat/provider-messages-visual-demo). NOT wired into MessageItem /
 * ChatScreen — the production indicator is specified separately (PM-6).
 */

/** The three provider-event kinds every visual option must cover. */
export type ProviderDemoKind = 'rate-limit' | 'fallback' | 'out-of-credit'

export interface ProviderDemoContent {
  /** Provider name shown in UI copy. */
  provider: string
  /** Complete user-facing sentence (fallback / out-of-credit). */
  message?: string
  /** Rate-limit sentence split around the LIVE countdown value. */
  countdownLead?: string
  countdownTail?: string
  countdownSeconds?: number
  /** Realistic provider error body shown by the verbose "Technical details" disclosure. */
  rawPayload: string
}

export const PROVIDER_DEMO_CONTENT: Record<ProviderDemoKind, ProviderDemoContent> = {
  'rate-limit': {
    provider: 'OpenRouter',
    countdownLead: 'OpenRouter is busy. Retrying automatically in',
    countdownTail: '(attempt 2 of 3).',
    countdownSeconds: 92,
    rawPayload: JSON.stringify(
      {
        error: {
          message: 'Rate limit exceeded: too many requests for anthropic/claude-haiku on the free tier',
          code: 429,
          metadata: {
            provider_name: 'Anthropic',
            raw: "anthropic_api_error: This request would exceed your organization's rate limit of 40000 input tokens per minute",
            reset: 92,
          },
        },
      },
      null,
      2,
    ),
  },
  fallback: {
    provider: 'OpenRouter',
    message: 'Answered by the Fallback model (Claude Haiku) because GPT-5 was unavailable.',
    rawPayload: JSON.stringify(
      {
        error: {
          message: 'Provider-down error for openrouter/auto (anthropic/claude-sonnet-4.5)',
          code: 502,
          metadata: {
            provider_name: 'Anthropic',
            raw: 'upstream connect error or disconnect/reset before headers. reset reason: connection termination',
          },
        },
      },
      null,
      2,
    ),
  },
  'out-of-credit': {
    provider: 'OpenRouter',
    message: 'OpenRouter says your account is out of credit.',
    rawPayload: JSON.stringify(
      {
        error: {
          message: 'Insufficient credits for this request',
          code: 402,
          metadata: {
            provider_name: 'Anthropic',
            raw: "402 {'error': {'message': 'Your credit balance is too low to access the Anthropic API...', 'type': 'billing_error'}}",
          },
        },
      },
      null,
      2,
    ),
  },
}

/**
 * Mirrors MessageItem's ADR-051 cap on the verbose disclosure content so a
 * runaway provider payload cannot blow out the demo either.
 */
export const PROVIDER_DEMO_MAX_DETAIL_CHARS = 512

/** m:ss form ("1:32") — the shape the founder's copy uses. */
export function formatProviderCountdown(totalSeconds: number): string {
  const minutes = Math.floor(totalSeconds / 60)
  const seconds = totalSeconds % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}

/**
 * Ticking countdown — RateLimitIndicator's exact effect pattern: reset on
 * prop change, tick every second, stop at zero.
 */
export function useProviderDemoCountdown(initialSeconds: number): number {
  const [remaining, setRemaining] = useState(Math.max(0, initialSeconds))
  useEffect(() => {
    setRemaining(Math.max(0, initialSeconds))
  }, [initialSeconds])
  useEffect(() => {
    if (remaining <= 0) return
    const id = setInterval(() => {
      setRemaining((prev) => Math.max(0, prev - 1))
    }, 1000)
    return () => clearInterval(id)
  }, [remaining])
  return remaining
}
