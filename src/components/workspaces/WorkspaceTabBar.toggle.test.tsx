// WorkspaceTabBar.toggle.test.tsx — RED pack for side-panel-shell-spec.md
// US-5 / FR-007 / MAJ-007 (wave 1 strip model; wave 3 entries):
//
//   "the strip stops being a role="tablist" of all entries; Chat and the
//   workspace-name entry keep tab/link semantics (aria-current="page" on
//   Chat while it is the underlying page), and panel entries become toggle
//   buttons with aria-pressed ... Mixed mode (waves 1-2, MAJ-012): entries
//   for panels not yet registered (Tasks/Team/Calendar in waves 1-2) remain
//   ordinary navigation links to their full-page routes, with no
//   aria-pressed."
//
// Wave 1 registers only `library` and `browser` (§10) — `browser` has no
// WORKSPACE_TABS entry at all, so the ONE segment this file exercises as a
// real toggle is `media` (labelled "Library", WORKSPACE_TABS's existing
// entry) — the rest (board/calendar/team) stay mixed-mode links per MAJ-012.
//
// Oracle: US-5 AS-1/AS-2/AS-6, FR-007, §11's "Mixed-mode strip renders links
// and toggles together" BDD scenario, §12 test 11.
//
// This is a NEW file, not an edit of the existing
// -WorkspaceTabBar.test.tsx — that file pins TODAY's role="tab"/
// aria-selected/tablist model (correctly, for the current code) and is
// EXPECTED to need rework once this feature lands (the spec itself says so:
// "assertions on navigation to board/team/calendar from the STRIP change
// deliberately in wave 3, and those specs are updated in that wave, not
// silently" — §12 Regression test requirements). Editing production code or
// an existing test to make it agree with a not-yet-implemented model is
// GREEN's job, not RED's; this file only adds the new, currently-failing
// expectations.
//
// RED evidence (2026-09-27, read src/components/workspaces/WorkspaceTabBar.tsx
// in full): the full strip's container still has `role="tablist"`; the
// Library entry (segment "media") is still a `<Link>` navigating to
// `/workspaces/{id}/media` (which redirects back to chat after a store call —
// see workspaces.$workspaceId.media.tsx) with `role="tab"` /
// `aria-selected` — there is no `<button>`, no `aria-pressed`, and clicking
// it does NOT call `openPanel`/toggle any panel state directly; it navigates.
// Every assertion below expecting a button with aria-pressed, or no
// navigation call, fails against this code.

import React from 'react'
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { useUiStore } from '@/store/ui'

let mockPathname = '/workspaces/ws-1/chat'
const mockNavigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: mockPathname }),
  useNavigate: () => mockNavigate,
  Link: ({
    children,
    to,
    params,
    role,
    'aria-selected': ariaSelected,
    'data-testid': testId,
    'aria-label': ariaLabel,
  }: {
    children: React.ReactNode
    to: string
    params?: Record<string, string>
    role?: string
    'aria-selected'?: boolean
    'data-testid'?: string
    'aria-label'?: string
  }) => {
    const href = params ? to.replace('$workspaceId', params.workspaceId) : to
    return (
      <a href={href} role={role} aria-selected={ariaSelected} data-testid={testId} aria-label={ariaLabel}>
        {children}
      </a>
    )
  },
}))

vi.mock('framer-motion', () => ({
  motion: {
    div: ({ children, ...rest }: React.HTMLAttributes<HTMLDivElement>) => <div {...rest}>{children}</div>,
  },
}))

vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="view-switcher-menu">{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
    className,
    ...rest
  }: {
    children: React.ReactNode
    onClick?: () => void
    className?: string
  } & Record<string, unknown>) => (
    <button type="button" onClick={onClick} className={className} {...rest}>
      {children}
    </button>
  ),
}))

import { WorkspaceTabBar } from './WorkspaceTabBar'

/** The §8.1 shape this feature introduces — read defensively since it does
 *  not exist on the store yet. */
function activePanel(): { id: string; context?: { workspaceId?: string } } | null {
  const state = useUiStore.getState() as unknown as {
    activePanel?: { id: string; context?: { workspaceId?: string } } | null
  }
  return state.activePanel ?? null
}

beforeEach(() => {
  mockNavigate.mockClear()
  mockPathname = '/workspaces/ws-1/chat'
  act(() => {
    // Reset the panel fields this pack reads (CHECK finding: `activePanel`
    // leaked between tests — it was missing from the reset while tests 4/5
    // click the Library entry, so a GREEN-side open would leak into the next
    // test). No assertion weakened: every expectation is unchanged.
    useUiStore.setState({
      libraryPanel: null,
      browserPanel: null,
      activePanel: null,
    } as never)
  })
})

describe('WorkspaceTabBar — toggle ARIA model (MAJ-007, wave 1)', () => {
  it('the full strip container is NOT role="tablist" anymore (MAJ-007)', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    expect(screen.queryByRole('tablist', { name: 'Workspace views' })).not.toBeInTheDocument()
  })

  it('the Chat entry shows aria-current="page" (not aria-selected) while it is the underlying page', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const chatTab = screen.getByTestId('workspace-tab-chat')
    expect(chatTab.getAttribute('aria-current')).toBe('page')
  })

  it('the registered Library entry (segment "media") is a toggle button with aria-pressed, not a role="tab" link', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const libraryEntry = screen.getByTestId('workspace-tab-media')
    expect(libraryEntry.tagName).toBe('BUTTON')
    expect(libraryEntry.getAttribute('role')).not.toBe('tab')
    expect(libraryEntry.getAttribute('aria-pressed')).toBe('false')
  })

  it('clicking the Library entry opens the panel scoped to this workspace WITHOUT navigating (US-5 AS-1)', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const libraryEntry = screen.getByTestId('workspace-tab-media')
    fireEvent.click(libraryEntry)

    expect(activePanel()).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })
    // The chat route stays the page underneath — no router navigation to the
    // /media redirect stub or anywhere else.
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('a second click on the Library entry closes the panel and clears aria-pressed (US-5 AS-2, SP-11)', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const libraryEntry = screen.getByTestId('workspace-tab-media')
    fireEvent.click(libraryEntry)
    // Intermediate assertion, deliberately: without it, a no-op click (today's
    // code — clicking the Link does nothing under fireEvent.click in jsdom,
    // no onClick handler exists) would leave activePanel null both before
    // AND after two clicks, passing this test VACUOUSLY on unmodified code.
    // Pinning the open state after click 1 is what makes this test capable
    // of failing for the right reason.
    expect(activePanel()).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })

    fireEvent.click(libraryEntry)
    expect(activePanel()).toBeNull()
  })
})

// ── Wave 2 (side-panel-shell-spec.md §10 "Wave 2 — Mail adopts the shell" +
//    §15 item 1: "Tasks / Calendar / Library / Team entries toggle panels
//    (Mail joins in wave 2)"): the Mail strip entry is a registered-panel
//    toggle, exactly like Library's — aria-pressed model, leave-gated
//    open/close, no navigation (US-5 AS-1/AS-2, SP-7 for the replace case).
//    At wave 1 the mail entry is a plain navigation Link to the /mail
//    redirect stub, so every assertion below fails on HEAD: the entry is an
//    <a> with an href, carries no aria-pressed, and clicking it navigates
//    instead of touching panel state. ─────────────────────────────────────
describe('WorkspaceTabBar — Mail entry toggle (wave 2, §15 item 1 / US-5)', () => {
  it('the Mail entry is a toggle button with aria-pressed, not a navigation link', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const mailEntry = screen.getByTestId('workspace-tab-mail')
    expect(mailEntry.tagName).toBe('BUTTON')
    expect(mailEntry.getAttribute('href')).toBeNull()
    expect(mailEntry.getAttribute('aria-pressed')).toBe('false')
  })

  it('clicking the Mail entry opens the Mail panel scoped to this workspace WITHOUT navigating (US-5 AS-1)', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const mailEntry = screen.getByTestId('workspace-tab-mail')
    fireEvent.click(mailEntry)

    expect(activePanel()).toEqual({ id: 'mail', context: { workspaceId: 'ws-1' } })
    // No navigation: the chat route stays the page underneath (the /mail
    // route remains only the expand target + bookmarked-URL stub).
    expect(mockNavigate).not.toHaveBeenCalled()
  })

  it('a second click on the Mail entry closes the panel (US-5 AS-2, SP-11)', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    const mailEntry = screen.getByTestId('workspace-tab-mail')
    fireEvent.click(mailEntry)
    // Intermediate assertion, deliberately (same vacuous-pass guard as the
    // Library test above): pin the open state after click 1.
    expect(activePanel()).toEqual({ id: 'mail', context: { workspaceId: 'ws-1' } })

    fireEvent.click(mailEntry)
    expect(activePanel()).toBeNull()
  })

  it('opening Mail while Library is open REPLACES it — one panel at a time (SP-7)', () => {
    render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="My Workspace" />)
    fireEvent.click(screen.getByTestId('workspace-tab-media'))
    expect(activePanel()).toEqual({ id: 'library', context: { workspaceId: 'ws-1' } })

    fireEvent.click(screen.getByTestId('workspace-tab-mail'))
    expect(activePanel()).toEqual({ id: 'mail', context: { workspaceId: 'ws-1' } })
  })
})
