import { describe, it, expect } from 'vitest'
import {
  extractProtocolTail,
  isKnownModelSlug,
  isKnownModelSlugInList,
  type ProviderForValidation,
} from './model-validation'

// W6-C4 / G12 — twin of the Go helper TestIsKnownModel in
// pkg/agent/model_resolution_test.go. The two test files MUST stay in sync;
// if you add a case to one, mirror it in the other.

const connectedAnthropic: ProviderForValidation = {
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  status: 'connected',
  models: ['anthropic/claude-haiku-4-5', 'anthropic/claude-sonnet-4-6'],
}

const connectedOpenAi: ProviderForValidation = {
  id: 'openai',
  name: 'openai',
  display_name: 'OpenAI',
  status: 'connected',
  models: ['openai/gpt-4o', 'openai/gpt-4o-mini'],
}

const connectedOpenRouter: ProviderForValidation = {
  id: 'openrouter',
  name: 'openrouter',
  display_name: 'OpenRouter',
  status: 'connected',
  models: ['z-ai/glm-5.2'],
}

// Disconnected / error providers are NOT trusted — G12 must surface the
// problem, not hide it.
const disconnectedOpenRouter: ProviderForValidation = {
  ...connectedOpenRouter,
  status: 'disconnected',
}
const erroredOpenRouter: ProviderForValidation = {
  ...connectedOpenRouter,
  status: 'error',
}

const ALL_CONNECTED: ProviderForValidation[] = [
  connectedAnthropic,
  connectedOpenAi,
  connectedOpenRouter,
]

describe('extractProtocolTail', () => {
  it('returns the part after the first slash', () => {
    expect(extractProtocolTail('anthropic/claude-haiku-4-5')).toBe('claude-haiku-4-5')
    expect(extractProtocolTail('openai/gpt-4o')).toBe('gpt-4o')
  })

  it('returns the input verbatim when no slash is present', () => {
    expect(extractProtocolTail('gpt-4o')).toBe('gpt-4o')
    expect(extractProtocolTail('claude-haiku-4-5')).toBe('claude-haiku-4-5')
  })

  it('only strips the first slash (vendor prefixes may embed slashes)', () => {
    expect(extractProtocolTail('z-ai/glm-5.2')).toBe('glm-5.2')
  })

  it('trims whitespace before checking', () => {
    expect(extractProtocolTail('  anthropic/claude-haiku-4-5  ')).toBe('claude-haiku-4-5')
  })
})

describe('isKnownModelSlug', () => {
  it('returns false for empty or whitespace slugs', () => {
    expect(isKnownModelSlug('', ALL_CONNECTED)).toBe(false)
    expect(isKnownModelSlug('   ', ALL_CONNECTED)).toBe(false)
  })

  it('matches the protocol-prefixed form (exact, case-insensitive)', () => {
    expect(isKnownModelSlug('anthropic/claude-haiku-4-5', ALL_CONNECTED)).toBe(true)
    expect(isKnownModelSlug('Anthropic/Claude-Haiku-4-5', ALL_CONNECTED)).toBe(true)
  })

  it('matches the bare slug extracted from the protocol-prefixed form', () => {
    expect(isKnownModelSlug('claude-haiku-4-5', ALL_CONNECTED)).toBe(true)
    expect(isKnownModelSlug('GPT-4O', ALL_CONNECTED)).toBe(true)
  })

  it('matches the provider name / display_name when used as a slug', () => {
    // Legacy single-model provider fixture: name doubles as the slug.
    const legacy: ProviderForValidation = {
      id: 'claude-haiku',
      name: 'claude-haiku',
      display_name: 'Claude (Haiku)',
      status: 'connected',
      models: [],
    }
    expect(isKnownModelSlug('claude-haiku', [legacy])).toBe(true)
    expect(isKnownModelSlug('Anthropic', [connectedAnthropic])).toBe(true)
    expect(isKnownModelSlug('OpenAI', [connectedOpenAi])).toBe(true)
  })

  it('returns false when no provider exposes the slug', () => {
    expect(isKnownModelSlug('gpt-9000-ultra', ALL_CONNECTED)).toBe(false)
    expect(isKnownModelSlug('fake-provider/some-model', ALL_CONNECTED)).toBe(false)
    expect(isKnownModelSlug('z-ai/glm-5-turbo', ALL_CONNECTED)).toBe(false)
  })

  it('ignores disconnected providers (G12 must surface the problem)', () => {
    // openrouter disconnected → openrouter entries are NOT trusted.
    const providers = [connectedAnthropic, connectedOpenAi, disconnectedOpenRouter]
    expect(isKnownModelSlug('z-ai/glm-5.2', providers)).toBe(false)
  })

  it('ignores errored providers', () => {
    const providers = [connectedAnthropic, connectedOpenAi, erroredOpenRouter]
    expect(isKnownModelSlug('z-ai/glm-5.2', providers)).toBe(false)
  })

  it('handles an empty provider list', () => {
    expect(isKnownModelSlug('gpt-4o', [])).toBe(false)
  })
})

describe('isKnownModelSlugInList', () => {
  it('matches the flat list (protocol-prefixed or bare)', () => {
    const models = ['anthropic/claude-haiku-4-5', 'openai/gpt-4o']
    expect(isKnownModelSlugInList('claude-haiku-4-5', models)).toBe(true)
    expect(isKnownModelSlugInList('GPT-4O', models)).toBe(true)
    expect(isKnownModelSlugInList('openai/gpt-4o', models)).toBe(true)
  })

  it('returns false for unknown slugs and empty inputs', () => {
    expect(isKnownModelSlugInList('gpt-9000', ['gpt-4o'])).toBe(false)
    expect(isKnownModelSlugInList('gpt-4o', [])).toBe(false)
    expect(isKnownModelSlugInList('', ['gpt-4o'])).toBe(false)
  })
})