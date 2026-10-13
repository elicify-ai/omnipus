// agentRows.test.ts — GitHub #1076: one invalid agent row must not take down
// the whole list. Oracle: the issue's Expected section and the generated
// Agent schema bound (name maxLength 100).
import { describe, it, expect } from 'vitest'
import { makeAgent } from '@/test/factories'
import { invalidAgentRowText, partitionAgentRows } from './agentRows'

describe('partitionAgentRows', () => {
  it('keeps every valid row and names the invalid one by id and field', () => {
    const ok = makeAgent({ id: 'mia', name: 'Mia' })
    const boundary = makeAgent({ id: 'b100', name: 'x'.repeat(100) })
    const tooLong = makeAgent({ id: 'long', name: 'x'.repeat(101) })
    const { agents, invalid } = partitionAgentRows([ok, tooLong, boundary])
    expect(agents.map((agent) => agent.id)).toEqual(['mia', 'b100'])
    expect(invalid).toHaveLength(1)
    expect(invalid[0].id).toBe('long')
    expect(invalid[0].field).toBe('name')
  })

  it('names a row without an id by its position', () => {
    const { invalid } = partitionAgentRows([{ name: 'no id' }])
    expect(invalid[0].id).toBe('#1')
  })

  it('the notice names the agent and field and never suggests a version mismatch', () => {
    const text = invalidAgentRowText({ id: 'long', field: 'name', message: 'too long' })
    expect(text).toContain('long')
    expect(text).toContain('field name')
    expect(text.toLowerCase()).not.toContain('version')
  })
})
