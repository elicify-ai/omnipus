// voice-provider-detect.ts — Runtime detection of the active voice provider's
// capability for the agent edit slide-over's voice widget.
//
// W5 of agent-form-requirements delivery. Per spec §4.10.1:
//   - Fetch GET /api/v1/voice/provider (the gateway source-of-truth for the
//     active voice provider config).
//   - Cache the result with a 10s SWR window (so multiple widgets on the same
//     edit page share the fetch).
//   - On 5xx / network / non-array response: return
//     { mode: 'disabled', reason: 'Voice provider unavailable' }.
//   - On 'enum' mode with 0 voices: fall back to 'free-text' (empty enum is
//     the same as no enum).
//
// Provider-change subscription: Settings → Voice publishes a
// `voice-provider-change` event on save; the agent-edit slide-over listens
// and re-calls detectVoiceProvider() on receipt. The SWR cache is invalidated
// by bumpVersion() so the next read re-fetches immediately.

import type { VoiceProvider } from '@/lib/api/generated/openapi-types'

export type VoiceProviderMode = 'enum' | 'free-text' | 'disabled'

export interface VoiceProviderDetectResult {
  mode: VoiceProviderMode
  voices?: string[]
  reason?: string
  fetchedAt: string
}

// 10s SWR — old value is returned while a fresh fetch runs in the background.
const SWR_WINDOW_MS = 10_000

interface CacheEntry {
  result: VoiceProviderDetectResult
  fetchedAtMs: number
}

// In-memory cache; one slot is enough (single provider per gateway).
let cache: CacheEntry | null = null
let inflight: Promise<VoiceProviderDetectResult> | null = null
let cacheVersion = 0

/**
 * Bump the cache version, forcing the next detectVoiceProvider() call to
 * re-fetch from the gateway. Called by the voice-provider-change listener
 * in the agent-edit slide-over (per spec §4.10.1).
 */
export function bumpVoiceProviderCacheVersion(): void {
  cacheVersion += 1
  cache = null
  inflight = null
}

/**
 * Detect the active voice provider's capability. Returns a stable
 * VoiceProviderDetectResult; the caller renders the matching widget variant.
 */
export async function detectVoiceProvider(): Promise<VoiceProviderDetectResult> {
  const now = Date.now()

  // Fresh cache + same version → return.
  if (cache && now - cache.fetchedAtMs < SWR_WINDOW_MS) {
    return cache.result
  }

  // In-flight fetch → share the same promise (deduplication).
  if (inflight) {
    return inflight
  }

  inflight = (async (): Promise<VoiceProviderDetectResult> => {
    const versionAtFetch = cacheVersion
    let result: VoiceProviderDetectResult
    try {
      const resp = await fetch('/api/v1/voice/provider', {
        credentials: 'include',
        headers: { Accept: 'application/json' },
      })

      if (!resp.ok) {
        result = disabledResult(`Voice provider unavailable (HTTP ${resp.status})`)
        return result
      }

      let body: VoiceProvider | null = null
      try {
        body = (await resp.json()) as VoiceProvider
      } catch {
        result = disabledResult('Voice provider unavailable (malformed response)')
        return result
      }

      if (!body || body.provider == null) {
        result = disabledResult('Voice provider unavailable (no provider configured)')
        return result
      }

      const voices = Array.isArray(body.voices) ? body.voices : []
      if (voices.length > 0) {
        result = {
          mode: 'enum',
          voices,
          fetchedAt: new Date().toISOString(),
        }
      } else {
        // No voices enum (provider has no list, or empty list) → free-text.
        result = {
          mode: 'free-text',
          fetchedAt: new Date().toISOString(),
        }
      }
      return result
    } catch (err) {
      // Network failure / fetch threw.
      result = disabledResult(
        `Voice provider unavailable (${err instanceof Error ? err.message : 'network'})`,
      )
      return result
    } finally {
      inflight = null
      // Only commit to cache if no version bump happened during the fetch.
      if (cacheVersion === versionAtFetch && result !== undefined) {
        cache = { result, fetchedAtMs: Date.now() }
      }
    }
  })()

  return inflight
}

function disabledResult(reason: string): VoiceProviderDetectResult {
  return {
    mode: 'disabled',
    reason,
    fetchedAt: new Date().toISOString(),
  }
}

export default detectVoiceProvider
