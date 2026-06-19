import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { fetchVoiceProvider } from '@/lib/api'

// We are testing the detection helper, which consumes fetchVoiceProvider.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchVoiceProvider: vi.fn(),
  }
})

// Bust the module's in-memory cache between tests.
async function resetModuleCache() {
  const mod = await import('@/lib/agents/voice-provider-detect')
  mod.bumpVoiceProviderCacheVersion()
}

describe('detectVoiceProvider', () => {
  beforeEach(async () => {
    vi.mocked(fetchVoiceProvider).mockReset()
    vi.useFakeTimers()
    await resetModuleCache()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('returns enum mode when the provider exposes voices', async () => {
    vi.mocked(fetchVoiceProvider).mockResolvedValue({
      provider: 'openai-tts',
      voices: ['alloy', 'echo'],
      voices_endpoint: null,
    } as any)

    const { detectVoiceProvider } = await import('@/lib/agents/voice-provider-detect')
    const result = await detectVoiceProvider()

    expect(result.mode).toBe('enum')
    expect(result.voices).toEqual(['alloy', 'echo'])
    expect(result.fetchedAt).toBeDefined()
  })

  it('returns free-text mode when voices list is absent', async () => {
    vi.mocked(fetchVoiceProvider).mockResolvedValue({
      provider: 'custom-tts',
      voices_endpoint: null,
    } as any)

    const { detectVoiceProvider } = await import('@/lib/agents/voice-provider-detect')
    const result = await detectVoiceProvider()

    expect(result.mode).toBe('free-text')
    expect(result.voices).toBeUndefined()
  })

  it('falls back to free-text mode when the voices enum is empty', async () => {
    vi.mocked(fetchVoiceProvider).mockResolvedValue({
      provider: 'piper',
      voices: [],
      voices_endpoint: null,
    } as any)

    const { detectVoiceProvider } = await import('@/lib/agents/voice-provider-detect')
    const result = await detectVoiceProvider()

    expect(result.mode).toBe('free-text')
  })

  it('returns disabled mode when no provider is configured', async () => {
    vi.mocked(fetchVoiceProvider).mockResolvedValue({
      provider: null,
      voices_endpoint: null,
    } as any)

    const { detectVoiceProvider } = await import('@/lib/agents/voice-provider-detect')
    const result = await detectVoiceProvider()

    expect(result.mode).toBe('disabled')
    expect(result.reason).toMatch(/no provider configured/)
  })

  it('returns disabled mode on ApiError with HTTP status', async () => {
    const { ApiError } = await import('@/lib/api')
    vi.mocked(fetchVoiceProvider).mockRejectedValue(new ApiError(503, 'Service Unavailable'))

    const { detectVoiceProvider } = await import('@/lib/agents/voice-provider-detect')
    const result = await detectVoiceProvider()

    expect(result.mode).toBe('disabled')
    expect(result.reason).toMatch(/503/)
  })

  it('returns disabled mode on generic network errors', async () => {
    vi.mocked(fetchVoiceProvider).mockRejectedValue(new TypeError('Failed to fetch'))

    const { detectVoiceProvider } = await import('@/lib/agents/voice-provider-detect')
    const result = await detectVoiceProvider()

    expect(result.mode).toBe('disabled')
    expect(result.reason).toMatch(/Failed to fetch/)
  })

  it('caches the result within the SWR window', async () => {
    vi.mocked(fetchVoiceProvider).mockResolvedValue({
      provider: 'openai-tts',
      voices: ['alloy'],
      voices_endpoint: null,
    } as any)

    const { detectVoiceProvider } = await import('@/lib/agents/voice-provider-detect')
    await detectVoiceProvider()
    await detectVoiceProvider()

    expect(fetchVoiceProvider).toHaveBeenCalledTimes(1)
  })

  it('re-fetches after the SWR window expires', async () => {
    vi.mocked(fetchVoiceProvider).mockResolvedValue({
      provider: 'openai-tts',
      voices: ['alloy'],
      voices_endpoint: null,
    } as any)

    const { detectVoiceProvider } = await import('@/lib/agents/voice-provider-detect')
    await detectVoiceProvider()
    vi.advanceTimersByTime(11_000)
    await detectVoiceProvider()

    expect(fetchVoiceProvider).toHaveBeenCalledTimes(2)
  })

  it('bumping the cache version forces a fresh fetch', async () => {
    vi.mocked(fetchVoiceProvider).mockResolvedValue({
      provider: 'openai-tts',
      voices: ['alloy'],
      voices_endpoint: null,
    } as any)

    const { detectVoiceProvider, bumpVoiceProviderCacheVersion } = await import('@/lib/agents/voice-provider-detect')
    await detectVoiceProvider()
    bumpVoiceProviderCacheVersion()
    await detectVoiceProvider()

    expect(fetchVoiceProvider).toHaveBeenCalledTimes(2)
  })
})
