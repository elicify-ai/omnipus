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

  // 17.5 (tool-parity-spec-r17.md, "autoApproveAskGroups: no new group; update_plan
  // grouped with create_plan"). Chief ruling: update_plan has the SAME group
  // membership as create_plan and no new always-ask group appears. Both plan
  // tools run without asking in pkg/tools/auto_approve.go, so neither is in any
  // group today; a group for them would put a false line in Settings "Always asks".
  it('17.5 read tools add no Ask group; plan edit shares plan creation', () => {
    // Positive control: importing an empty grouping table cannot pass this.
    expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL.request_mount).toBe('Mounting a folder, or writing files outside your workspace')
    expect(AUTO_APPROVE_ASK_GROUPS.find(g => g.tools.includes('request_mount'))?.label)
      .toBe('Mounting a folder, or writing files outside your workspace')
    for (const name of ['get_task', 'get_plan', 'get_goal']) {
      expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL[name]).toBeUndefined()
    }
    // Same group result for both plan tools, and that result is "no group".
    expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL.update_plan).toBe(AUTO_APPROVE_ASK_GROUP_BY_TOOL.create_plan)
    expect(AUTO_APPROVE_ASK_GROUP_BY_TOOL.create_plan).toBeUndefined()
    expect(AUTO_APPROVE_ASK_GROUPS.filter(g => g.tools.includes('update_plan') || g.tools.includes('create_plan'))).toHaveLength(0)
    // No new always-ask group: the label list is exactly the existing eight.
    expect(AUTO_APPROVE_ASK_GROUPS).toHaveLength(8)
    expect(AUTO_APPROVE_ASK_GROUPS.flatMap(g => g.tools)).toHaveLength(29)
  })
})
