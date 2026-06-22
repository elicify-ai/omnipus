import { describe, it, expect } from 'vitest'
import { pickCapableDefaultModel } from './defaultModel'

describe('pickCapableDefaultModel (UAT onboarding default)', () => {
  it('returns empty string for an empty list', () => {
    expect(pickCapableDefaultModel([])).toBe('')
  })

  it('prefers glm-5.2 when present', () => {
    expect(
      pickCapableDefaultModel(['z-ai/glm-5-turbo', 'z-ai/glm-5.2', 'openai/gpt-4o']),
    ).toBe('z-ai/glm-5.2')
  })

  it('falls back to any glm-5* when glm-5.2 is absent', () => {
    expect(pickCapableDefaultModel(['z-ai/glm-5-turbo', 'openai/gpt-4o'])).toBe('z-ai/glm-5-turbo')
  })

  it('never selects the fable preview entry', () => {
    // The reported 404 model was the first alphabetical "…fable-latest".
    const result = pickCapableDefaultModel([
      'anthropic/claude-fable-latest',
      'anthropic/claude-sonnet-4-5',
    ])
    expect(result).toBe('anthropic/claude-sonnet-4-5')
    expect(result).not.toContain('fable')
  })

  it('prefers a flagship claude (sonnet/opus) over a haiku', () => {
    expect(
      pickCapableDefaultModel(['anthropic/claude-3.5-haiku', 'anthropic/claude-sonnet-4-5']),
    ).toBe('anthropic/claude-sonnet-4-5')
  })

  it('prefers gemini pro over flash', () => {
    expect(
      pickCapableDefaultModel(['google/gemini-2.5-flash', 'google/gemini-2.5-pro']),
    ).toBe('google/gemini-2.5-pro')
  })

  it('prefers gpt-4o over gpt-4o-mini', () => {
    expect(pickCapableDefaultModel(['openai/gpt-4o-mini', 'openai/gpt-4o'])).toBe('openai/gpt-4o')
  })

  it('never leaves the field empty when the list is non-empty', () => {
    const list = ['some-obscure/model-x', 'another/model-y']
    expect(pickCapableDefaultModel(list)).not.toBe('')
    expect(list).toContain(pickCapableDefaultModel(list))
  })
})
