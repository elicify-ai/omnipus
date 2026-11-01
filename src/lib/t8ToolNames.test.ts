import { describe, expect, it } from 'vitest'
import { humanizeToolName } from './humanizeToolName'
import { AUTO_APPROVE_ASK_GROUPS, AUTO_APPROVE_ASK_GROUP_BY_TOOL } from './autoApproveAskGroups'

// Oracle: tool-parity r17 17.4/17.5, FR-161/162, not inferred labels.
describe('T8 canonical labels and existing Ask groups', () => {
  it.each([
    ['get_task', 'Read task'],
    ['get_plan', 'Read plan'],
    ['get_goal', 'Read goal'],
    ['update_plan', 'Update plan'],
  ])('17.4 %s is %s', (name, label) => {
    expect(humanizeToolName(name)).toBe(label)
  })

  it('17.5 read tools add no Ask group; plan edit shares plan creation', () => {
    // Positive control: importing an empty grouping table cannot pass this.
    expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL.request_mount).toBeDefined()
    for (const name of ['get_task', 'get_plan', 'get_goal']) {
      expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL[name]).toBeUndefined()
    }
    const groups = AUTO_APPROVE_ASK_GROUPS.filter(group => group.tools.includes('update_plan'))
    expect(groups).toHaveLength(1)
    expect(groups[0].tools).toContain('create_plan')
    expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL.update_plan).toBe(AUTO_APPROVE_ASK_GROUP_BY_TOOL.create_plan)
  })
})
