import { describe, expect, it } from 'vitest'
import type { Agent } from '@/lib/api'
import { buildAgentUpdate, ProtectedAgentFieldChangeError } from './agentDraft'

const baseline = {
  id: 'mia',
  revision: 'a'.repeat(64),
  name: 'Mia',
  description: 'Colleague',
  type: 'core',
  locked: true,
  status: 'idle',
  soul: '',
  timeout_seconds: 300,
  max_tool_iterations: 50,
  memory_enabled: true,
  needs_model: false,
  skills: ['plan'],
  editable_fields: [
    { name: 'name', editable: false, reason: 'Built-in identity is fixed.' },
    { name: 'soul', editable: false, reason: 'Built-in instructions are fixed.' },
    { name: 'skills', editable: true },
    { name: 'model', editable: true },
  ],
} satisfies Agent

describe('buildAgentUpdate', () => {
  it('sends only changed editable fields with the reviewed revision', () => {
    expect(buildAgentUpdate(baseline, {
      name: 'Mia',
      soul: '',
      skills: ['plan', 'verify'],
      model: 'z-ai/glm-5.3-flash',
    })).toEqual({
      revision: 'a'.repeat(64),
      skills: ['plan', 'verify'],
      model: 'z-ai/glm-5.3-flash',
    })
  })

  it('preserves an explicit empty assignment as a changed clear', () => {
    expect(buildAgentUpdate(baseline, { skills: [] })).toEqual({
      revision: 'a'.repeat(64),
      skills: [],
    })
  })

  it('returns null for a no-op draft instead of issuing a revision-only write', () => {
    expect(buildAgentUpdate(baseline, {
      name: 'Mia', soul: '', skills: ['plan'], provider: '', default: false,
      voice: null, model_params: { temperature: 1, max_tokens: 4096 },
    })).toBeNull()
  })

  it('refuses a changed protected value instead of silently dropping the edit', () => {
    expect(() => buildAgentUpdate(baseline, { name: 'Not Mia' })).toThrow(
      new ProtectedAgentFieldChangeError('name', 'Built-in identity is fixed.'),
    )
  })
})
