import { describe, expect, it } from 'vitest'
import { AUTO_APPROVE_ASK_GROUPS, AUTO_APPROVE_ASK_GROUP_BY_TOOL } from './autoApproveAskGroups'

// FR-032: additive new membership only; do not rewrite unrelated legacy group
// counts or retired names in the existing independent full-inventory test.
describe('Q7 outbound policy tool is visible in the Auto-approve ask inventory', () => {
  it('FR-032: lists set_channel_outbound exactly once in the settings group', () => {
    const matches = AUTO_APPROVE_ASK_GROUPS.filter(group => group.tools.includes('set_channel_outbound'))
    expect(matches.map(group => group.label)).toEqual(['Changing or testing settings, providers, channels, agents or skills'])
    expect(matches.flatMap(group => group.tools).filter(name => name === 'set_channel_outbound')).toEqual(['set_channel_outbound'])
    expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL.set_channel_outbound).toBe('Changing or testing settings, providers, channels, agents or skills')
  })

  it('positive control: settings grouping still lists an existing connection tool', () => {
    expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL.configure_channel).toBe('Changing or testing settings, providers, channels, agents or skills')
  })
})
