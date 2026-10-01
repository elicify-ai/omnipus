// panelShell.workspaceSwitch.test.ts — side-panel-shell-spec.md §12 #20
// (SP-29, FR-020). The per-panel workspace-switch rule is not a function
// anywhere in this tree (verified: no resolveWorkspaceSwitch / workspace-switch
// helper under src/components/panel-shell). RED loads the module the shell
// must grow and fails LOUDLY with BLOCKED until it exists — never a skip.
//
// Oracle (US-3 AS-5, FR-020), applied to the open panel when the workspace
// changes from `from` to `to`:
//   browser                         → stay (session anchor; context untouched)
//   library opened with no workspace (sidebar root / `app`) → stay
//   library opened scoped to `from` → follow `to`, but only after beforeLeave
//                                     resolves true; false cancels (FR-013)
//   tasks / calendar / mail         → follow `to`

import { describe, expect, it } from 'vitest'

const MODULE_PATH = '@/components/panel-shell/workspaceSwitch'

type Decision =
  | { action: 'stay' }
  | { action: 'follow'; workspaceId: string }
  | { action: 'cancel' }

type SwitchInput = {
  panelId: 'library' | 'browser' | 'mail' | 'tasks' | 'team' | 'calendar'
  openedWorkspaceId?: string
  nextWorkspaceId: string
  beforeLeave?: () => Promise<boolean>
}

async function load(): Promise<{
  resolveWorkspaceSwitch: (input: SwitchInput) => Promise<Decision>
}> {
  let mod: unknown
  try {
    mod = await import(/* @vite-ignore */ MODULE_PATH)
  } catch (importErr) {
    throw new Error(
      `BLOCKED: workspace-switch rule module not implemented — required by side-panel-shell-spec.md §12 test #20 / FR-020 (import of ${MODULE_PATH} failed: ${importErr instanceof Error ? importErr.message : String(importErr)})`,
      { cause: importErr },
    )
  }
  const fn = (mod as { resolveWorkspaceSwitch?: (input: SwitchInput) => Promise<Decision> })
    .resolveWorkspaceSwitch
  if (typeof fn !== 'function') {
    throw new Error(
      `BLOCKED: ${MODULE_PATH} exports no resolveWorkspaceSwitch — required by side-panel-shell-spec.md §12 test #20 / FR-020`,
    )
  }
  return { resolveWorkspaceSwitch: fn }
}

describe('workspace switch per panel (§12 #20, SP-29, FR-020)', () => {
  it('exposes the rule module (missing module fails loudly, never skips)', async () => {
    let blocked = ''
    try {
      await load()
    } catch (err) {
      blocked = err instanceof Error ? err.message : String(err)
    }
    expect(blocked, blocked || 'module must exist').toBe('')
  })

  it('Browser stays anchored — session context is not closed and not moved', async () => {
    const { resolveWorkspaceSwitch } = await load()
    const decision = await resolveWorkspaceSwitch({
      panelId: 'browser',
      nextWorkspaceId: 'ws-b',
    })
    expect(decision).toEqual({ action: 'stay' })
  })

  it('a sidebar-rooted Library (no workspace) does NOT follow', async () => {
    const { resolveWorkspaceSwitch } = await load()
    const decision = await resolveWorkspaceSwitch({
      panelId: 'library',
      nextWorkspaceId: 'ws-b',
    })
    expect(decision).toEqual({ action: 'stay' })
  })

  it('a workspace-scoped Library follows ONLY after beforeLeave resolves true', async () => {
    const { resolveWorkspaceSwitch } = await load()
    const allowed = await resolveWorkspaceSwitch({
      panelId: 'library',
      openedWorkspaceId: 'ws-a',
      nextWorkspaceId: 'ws-b',
      beforeLeave: async () => true,
    })
    expect(allowed).toEqual({ action: 'follow', workspaceId: 'ws-b' })
  })

  it('a declined Library guard CANCELS — the outgoing workspace stays', async () => {
    const { resolveWorkspaceSwitch } = await load()
    const decision = await resolveWorkspaceSwitch({
      panelId: 'library',
      openedWorkspaceId: 'ws-a',
      nextWorkspaceId: 'ws-b',
      beforeLeave: async () => false,
    })
    expect(decision).toEqual({ action: 'cancel' })
  })

  it('Tasks, Calendar and Mail follow the new workspace (FR-020)', async () => {
    const { resolveWorkspaceSwitch } = await load()
    for (const panelId of ['tasks', 'calendar', 'mail'] as const) {
      const decision = await resolveWorkspaceSwitch({
        panelId,
        openedWorkspaceId: 'ws-a',
        nextWorkspaceId: 'ws-b',
      })
      expect(decision).toEqual({ action: 'follow', workspaceId: 'ws-b' })
    }
  })
})
