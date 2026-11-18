import { describe, expect, it } from 'vitest'
import type { Agent, ExecutorConfig, ExecutorConfigUpdate } from '@/lib/api/generated/openapi-types'
import { buildAgentUpdate, buildExecutorUpdate, MaskedEnvValueError, ProtectedAgentFieldChangeError } from './agentDraft'

// T11 FR-8 / ExecutorConfigUpdate: masks belong to READ, never WRITE.
// Exact error copy is a characterization pin commissioned by this fix round;
// rejection and the closed write delta are specified independently by the contract.
const savedExecutor: ExecutorConfig = {
  kind: 'external-cli', cli: 'codex', effective_cli_path: '/opt/codex',
  env_overrides: { KEEP: '••••••', DROP: '••••••' }, cli_args: '--max-turns 5',
}
const executorCases: Array<{
  name: string
  draft: ExecutorConfig
  update?: ExecutorConfigUpdate | null
  rejectedName?: string
}> = [
  { name: 'omits untouched masks on an arguments edit', draft: { ...savedExecutor, cli_args: '--max-turns 7' }, update: { cli_args: '--max-turns 7' } },
  { name: 'rejects a renamed masked row', draft: { ...savedExecutor, env_overrides: { KEEP: '••••••', RENAMED: '••••••' } }, rejectedName: 'RENAMED' },
  { name: 'rejects the mask typed as a new value', draft: { ...savedExecutor, env_overrides: { ...savedExecutor.env_overrides, TYPED: '••••••' } }, rejectedName: 'TYPED' },
  { name: 'skips an unfinished empty-name row', draft: { ...savedExecutor, env_overrides: { ...savedExecutor.env_overrides, '': 'unfinished' } }, update: null },
  { name: 'lists removal explicitly without resending the kept mask', draft: { ...savedExecutor, env_overrides: { KEEP: '••••••' } }, update: { env_overrides_remove: ['DROP'] } },
  { name: 'sends an explicit empty string to clear cli_args', draft: { ...savedExecutor, cli_args: '' }, update: { cli_args: '' } },
  { name: 'returns null and no executor write when nothing changed', draft: { ...savedExecutor, env_overrides: { ...savedExecutor.env_overrides } }, update: null },
]

describe('buildExecutorUpdate — mask-safe closed WRITE deltas', () => {
  it.each(executorCases)('$name', ({ draft, update, rejectedName }) => {
    const agent: Agent = {
      ...baseline, id: 'draft-worker', type: 'subagent_3p', locked: false,
      executor: savedExecutor,
      editable_fields: ['executor.env_overrides', 'executor.env_overrides_remove', 'executor.cli_args'].map(name => ({ name, editable: true })),
    }
    if (rejectedName) {
      const message = `Enter the value for ${rejectedName} again: a hidden value cannot be saved under a new name.`
      for (const build of [() => buildExecutorUpdate(savedExecutor, draft), () => buildAgentUpdate(agent, { executor: draft })]) {
        try {
          build()
          expect.unreachable('a changed mask must stop the write, not disappear or reach the server')
        } catch (error) {
          expect(error).toBeInstanceOf(MaskedEnvValueError)
          expect(error).toHaveProperty('name', 'MaskedEnvValueError')
          expect(error).toHaveProperty('envName', rejectedName)
          expect(error).toHaveProperty('message', message)
        }
      }
      return
    }
    expect(buildExecutorUpdate(savedExecutor, draft)).toEqual(update)
    expect(buildAgentUpdate(agent, { executor: draft })).toEqual(
      update === null ? null : { revision: agent.revision, executor: update },
    )
  })
})

const baseline = {
  id: 'mia',
  revision: 'a'.repeat(64),
  name: 'Mia',
  description: 'Colleague',
  type: 'core',
  locked: true,
  status: 'idle',
  soul: '',
  max_tool_iterations: 50,
  max_tool_iterations_source: 'global',
  max_tool_iterations_override_ignored: false,
  memory_enabled: true,
  needs_model: false,
  figure: 'Omnipus',
  role: 'general',
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
