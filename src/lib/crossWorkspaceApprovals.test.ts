// Unit tests for summarizeCrossWorkspaceApprovals — the pure copy/targeting
// logic behind CrossWorkspaceApprovalBanner (founder decision 2026-09-14).

import { describe, it, expect } from 'vitest'
import { summarizeCrossWorkspaceApprovals } from './crossWorkspaceApprovals'
import type { PendingToolApproval } from '@/store/toolApproval'

function approval(overrides: Partial<PendingToolApproval>): Pick<PendingToolApproval, 'workspaceId'> {
  return { workspaceId: undefined, ...overrides }
}

const WORKSPACE_NAMES: Record<string, string> = { 'ws-a': 'UAT-T2', 'ws-b': 'Marketing Site' }
const nameOf = (id: string) => WORKSPACE_NAMES[id] ?? id

describe('summarizeCrossWorkspaceApprovals', () => {
  it('returns null when the queue is empty', () => {
    expect(summarizeCrossWorkspaceApprovals([], 'ws-active', nameOf)).toBeNull()
  })

  it('returns null when every approval belongs to the active workspace', () => {
    const queue = [approval({ workspaceId: 'ws-active' }), approval({ workspaceId: 'ws-active' })]
    expect(summarizeCrossWorkspaceApprovals(queue, 'ws-active', nameOf)).toBeNull()
  })

  it('returns null for approvals with no workspaceId at all (in scope everywhere)', () => {
    const queue = [approval({ workspaceId: undefined })]
    expect(summarizeCrossWorkspaceApprovals(queue, 'ws-active', nameOf)).toBeNull()
  })

  it('returns null when there is no active workspace — the modal already shows everything', () => {
    const queue = [approval({ workspaceId: 'ws-a' }), approval({ workspaceId: 'ws-b' })]
    expect(summarizeCrossWorkspaceApprovals(queue, null, nameOf)).toBeNull()
  })

  it('singular copy for exactly one approval in one other workspace, resolving its name', () => {
    const queue = [approval({ workspaceId: 'ws-a' })]
    const summary = summarizeCrossWorkspaceApprovals(queue, 'ws-active', nameOf)
    expect(summary).not.toBeNull()
    expect(summary?.count).toBe(1)
    expect(summary?.workspaceIds).toEqual(['ws-a'])
    expect(summary?.targetWorkspaceId).toBe('ws-a')
    expect(summary?.label).toBe('1 approval waiting in UAT-T2')
  })

  it('plural copy for several approvals in the SAME other workspace', () => {
    const queue = [
      approval({ workspaceId: 'ws-active' }), // in-scope, excluded
      approval({ workspaceId: 'ws-a' }),
      approval({ workspaceId: 'ws-a' }),
      approval({ workspaceId: undefined }), // no workspace, excluded
    ]
    const summary = summarizeCrossWorkspaceApprovals(queue, 'ws-active', nameOf)
    expect(summary?.count).toBe(2)
    expect(summary?.workspaceIds).toEqual(['ws-a'])
    expect(summary?.label).toBe('2 approvals waiting in UAT-T2')
  })

  it('"N other workspaces" copy when the approvals span several workspaces', () => {
    const queue = [
      approval({ workspaceId: 'ws-a' }),
      approval({ workspaceId: 'ws-b' }),
      approval({ workspaceId: 'ws-b' }),
    ]
    const summary = summarizeCrossWorkspaceApprovals(queue, 'ws-active', nameOf)
    expect(summary?.count).toBe(3)
    expect(summary?.workspaceIds).toEqual(['ws-a', 'ws-b'])
    expect(summary?.label).toBe('3 approvals waiting in 2 other workspaces')
  })

  it('targets the FIRST workspace encountered in queue order', () => {
    const queue = [approval({ workspaceId: 'ws-b' }), approval({ workspaceId: 'ws-a' })]
    const summary = summarizeCrossWorkspaceApprovals(queue, 'ws-active', nameOf)
    expect(summary?.targetWorkspaceId).toBe('ws-b')
  })

  it('falls back to the raw id when the workspace name cannot be resolved', () => {
    const queue = [approval({ workspaceId: 'ws-unknown' })]
    const summary = summarizeCrossWorkspaceApprovals(queue, 'ws-active', nameOf)
    expect(summary?.label).toBe('1 approval waiting in ws-unknown')
  })
})
