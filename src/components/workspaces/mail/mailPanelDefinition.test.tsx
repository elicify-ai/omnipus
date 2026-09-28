// mailPanelDefinition.test.tsx — wave-2 RED pack for Mail's §8.1
// PanelDefinition payload (side-panel-shell-spec.md §10 "Wave 2 — Mail
// adopts the shell": "satisfied by registering a Mail PanelDefinition — no
// shell change. Mail's expand target follows the email spec").
//
// Oracle — side-panel-shell-spec.md §1 panel inventory, mail row:
//   "| mail | Mail | Mail panel (feature/email-mail, D11) | Mail full page
//    (per email spec) |"
// with the email spec naming the full page:
//   email-mail-view-spec.md §17 (chat_link row): "the chat_link URL scheme:
//   …/#/workspaces/{wsId}/mail?mailbox={agentId}&folder=drafts&message=…"
//   and its router row: "The SPA router uses hash history (verified) — deep
//   links carry #/…".
//
// The `/#/` prefix is derived, not observed: the shell's Expand does
// window.open(expandTarget(context)) (src/components/panel-shell/
// usePanelShell.ts), and in a hash-history SPA a new-tab URL without the
// hash fragment names a GATEWAY path, not the SPA route — Library's and
// Browser's registry entries both carry it for exactly this reason. A
// expand target without `/#/` opens a dead tab.
//
// Also pinned: no beforeLeave (§8.1: "beforeLeave is supplied ONLY by
// panels with unsaved-edit risk — Library, in wave 1"; Mail keeps no
// unsaved-edit state outside its compose dialog, same posture as Browser).

import { Suspense } from 'react'
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, it, expect, vi } from 'vitest'

const { mailPanelModuleLoaded } = vi.hoisted(() => ({
  mailPanelModuleLoaded: vi.fn(),
}))

vi.mock('./MailPanel', () => {
  mailPanelModuleLoaded()
  return {
    MailPanel: ({ workspaceId, onLocationChange }: {
      workspaceId: string
      onLocationChange?: (location: { mailboxId: string | null; folder: string; messageRef: string | null }) => void
    }) => (
      <div>
        Loaded Mail for {workspaceId}
        <button
          type="button"
          onClick={() => onLocationChange?.({
            mailboxId: 'mia agent',
            folder: 'drafts',
            messageRef: 'mid:<draft/42@test.local>',
          })}
        >
          Select draft
        </button>
      </div>
    ),
  }
})

import { mailPanelDefinition } from './mailPanelDefinition'

describe('Mail PanelDefinition payload (§10 Wave 2, §1 mail row, email spec §17)', () => {
  it('registers id "mail" with title "Mail" (§1 panel inventory mail row)', () => {
    expect(mailPanelDefinition.id).toBe('mail')
    expect(mailPanelDefinition.title).toBe('Mail')
  })

  it('expand target is the Mail full page, hash-router form (email spec §17 chat_link scheme; §1 "Mail full page")', () => {
    const target = mailPanelDefinition.expandTarget({ workspaceId: 'ws-1' })
    const s = typeof target === 'string' ? target : String((target as unknown as { to?: string }).to)
    // The SPA route lives in the hash fragment (createHashHistory) — a
    // pop-out URL without `/#/` is a gateway path, not the Mail page.
    expect(s).toContain('/#/workspaces/ws-1/mail')
  })

  it('carries NO beforeLeave — the CRIT-001 unsaved-edits guard is Library-only (§8.1)', () => {
    expect(mailPanelDefinition.beforeLeave).toBeUndefined()
  })

  it('loads Mail panel code only when the registered content is rendered', async () => {
    expect(mailPanelModuleLoaded).not.toHaveBeenCalled()

    const Content = mailPanelDefinition.content
    render(
      <Suspense fallback={<div>Loading Mail…</div>}>
        <Content
          context={{ workspaceId: 'ws-1' }}
          close={() => undefined}
          expand={() => undefined}
          registerExpand={() => undefined}
          onWidthSettle={() => undefined}
        />
      </Suspense>,
    )

    expect(await screen.findByText('Loaded Mail for ws-1')).toBeInTheDocument()
    expect(mailPanelModuleLoaded).toHaveBeenCalledTimes(1)
  })

  it('carries the current mailbox, folder and message into the full-page Expand URL (D49)', async () => {
    let expandAction: (() => boolean) | null = null
    const open = vi.spyOn(window, 'open').mockReturnValue({ closed: false, opener: window } as unknown as Window)
    const Content = mailPanelDefinition.content

    render(
      <Suspense fallback={<div>Loading Mail…</div>}>
        <Content
          context={{ workspaceId: 'ws-1' }}
          close={() => undefined}
          expand={() => undefined}
          registerExpand={(action) => { expandAction = action }}
          onWidthSettle={() => undefined}
        />
      </Suspense>,
    )

    fireEvent.click(await screen.findByRole('button', { name: 'Select draft' }))
    expect(expandAction).not.toBeNull()
    expect((expandAction as unknown as () => boolean)()).toBe(true)

    const target = String(open.mock.calls[0]?.[0])
    const url = new URL(target, 'http://omnipus.test')
    expect(url.hash).toBe(
      '#/workspaces/ws-1/mail?view=full&mailbox=mia+agent&folder=drafts&message=mid%3A%3Cdraft%2F42%40test.local%3E',
    )
    open.mockRestore()
  })
})
