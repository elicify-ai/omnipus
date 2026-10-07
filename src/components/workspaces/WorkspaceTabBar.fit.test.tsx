// Oracle: founder brief R44.1–R44.4. Full=537 and icons=300 are independent
// synthetic natural-width fixtures; 792 reproduces the reported available space.
// Geometry/ResizeObserver and router navigation are browser edges. The real
// WorkspaceTabBar, kit Tooltip/DropdownMenu and panel store remain under test.
import { act, cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'
import { mockWorkspaceHeaderMeasurements } from '@/test/workspaceHeaderMeasurement'
import { WorkspaceTabBar } from './WorkspaceTabBar'

const navigate = vi.fn()
vi.mock('@tanstack/react-router', () => ({
  useLocation: () => ({ pathname: '/workspaces/ws-1/chat' }),
  useNavigate: () => navigate,
}))

const panels = [
  { segment: 'board', id: 'tasks', label: 'Tasks' },
  { segment: 'calendar', id: 'calendar', label: 'Calendar' },
  { segment: 'media', id: 'library', label: 'Library' },
  { segment: 'mail', id: 'mail', label: 'Mail' },
  { segment: 'team', id: 'team', label: 'Team' },
] as const

beforeEach(() => {
  navigate.mockReset().mockResolvedValue(undefined)
  useUiStore.setState({ activePanel: null, guardPending: false, toasts: [] })
})
afterEach(() => {
  cleanup()
  useUiStore.getState().closePanel()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function renderHeader(width: number) {
  const geometry = mockWorkspaceHeaderMeasurements(width)
  const view = render(<WorkspaceTabBar workspaceId="ws-1" workspaceName="Workspace One" />)
  return { ...view, geometry }
}

describe('R44 measured widest-fit mode selection', () => {
  it.each([
    [1, 'narrow'], [299, 'narrow'], [299.99, 'narrow'],
    [300, 'icons'], [300.01, 'icons'], [301, 'icons'],
    [536, 'icons'], [536.99, 'icons'],
    [537, 'full'], [537.01, 'full'], [538, 'full'], [792, 'full'],
  ])('available %s selects %s from the natural strip widths', (width, mode) => {
    renderHeader(width as number)
    expect(screen.getByTestId('workspace-header-entries')).toHaveAttribute('data-mode', mode as string)
    if (mode === 'narrow') {
      expect(screen.getByRole('button', { name: 'Open panels menu' })).toHaveTextContent('Workspace One')
      expect(screen.queryByRole('button', { name: 'Tasks' })).toBeNull()
    } else {
      const tasks = screen.getByRole('button', { name: 'Tasks' })
      expect(tasks.textContent).toBe(mode === 'full' ? 'Tasks' : '')
      expect(screen.getByRole('button', { name: 'Workspace One — workspace settings' })).toBeVisible()
    }
  })

  it('does not guess a viewport breakpoint before natural sizes are measurable', () => {
    const { geometry } = renderHeader(0)
    expect(screen.getByTestId('workspace-header-entries')).toHaveAttribute('data-mode', 'full')
    geometry.setAvailableWidth(400)
    expect(screen.getByTestId('workspace-header-entries')).toHaveAttribute('data-mode', 'icons')
  })

  it('responds to row resizing in both directions without mode-dependent oscillation', () => {
    const { geometry } = renderHeader(792)
    const row = screen.getByTestId('workspace-header-entries')
    for (const [width, mode] of [[400, 'icons'], [220, 'narrow'], [792, 'full'], [537, 'full'], [537, 'full'], [300, 'icons'], [300, 'icons']] as const) {
      geometry.setAvailableWidth(width)
      expect(row).toHaveAttribute('data-mode', mode)
    }
  })

  it('observes natural widths too, so a changed font or name recomputes at the same available width', () => {
    const { geometry, unmount } = renderHeader(792)
    expect([...geometry.observed].map((element) =>
      (element as HTMLElement).dataset.workspaceHeaderMeasure ?? (element as HTMLElement).dataset.testid,
    ).sort()).toEqual(['full', 'icons', 'workspace-header-entries'])
    geometry.setNaturalWidths(850, 400)
    expect(screen.getByTestId('workspace-header-entries')).toHaveAttribute('data-mode', 'icons')
    geometry.setNaturalWidths(1000, 820)
    expect(screen.getByTestId('workspace-header-entries')).toHaveAttribute('data-mode', 'narrow')
    geometry.setNaturalWidths(537, 300)
    expect(screen.getByTestId('workspace-header-entries')).toHaveAttribute('data-mode', 'full')
    unmount()
    expect(geometry.disconnects).toBe(1)
  })
})

describe('R44 one workspace name, never a second hamburger', () => {
  it.each([792, 400, 220])('has no separate workspace-view-switcher trigger at width %s', (width) => {
    renderHeader(width)
    expect(screen.queryByTestId('workspace-view-switcher')).toBeNull()
    expect(screen.getAllByTestId('workspace-name-button')).toHaveLength(1)
    expect(screen.getByTestId('workspace-name-button')).toHaveTextContent('Workspace One')
  })

  it('uses the name as the real keyboard menu trigger with the exact Settings + five-toggle inventory', async () => {
    renderHeader(220)
    const user = userEvent.setup()
    const trigger = screen.getByRole('button', { name: 'Open panels menu' })
    expect(trigger).toHaveAttribute('data-testid', 'workspace-name-button')
    expect(trigger).toHaveTextContent('Workspace One')
    expect(trigger).toHaveAttribute('aria-haspopup', 'menu')
    act(() => trigger.focus())
    await user.keyboard('{Enter}')
    const menu = await screen.findByRole('menu')
    const items = within(menu).getAllByRole('menuitem')
    expect(items.map((item) => item.textContent)).toEqual(['Settings', 'Tasks', 'Calendar', 'Library', 'Mail', 'Team'])
    expect(items.map((item) => item.getAttribute('data-testid'))).toEqual([
      'workspace-view-switcher-settings', 'workspace-view-switcher-board', 'workspace-view-switcher-calendar',
      'workspace-view-switcher-media', 'workspace-view-switcher-mail', 'workspace-view-switcher-team',
    ])
    await user.click(within(menu).getByRole('menuitem', { name: 'Settings' }))
    expect(navigate).toHaveBeenCalledExactlyOnceWith({
      to: '/workspaces/$workspaceId/settings', params: { workspaceId: 'ws-1' },
    })
  })

  it.each(panels)('the name menu reaches $label and preserves its scoped toggle action', async ({ segment, id, label }) => {
    renderHeader(220)
    const user = userEvent.setup()
    const trigger = screen.getByRole('button', { name: 'Open panels menu' })
    await user.click(trigger)
    const item = await screen.findByRole('menuitem', { name: label })
    expect(item).toHaveAttribute('data-testid', `workspace-view-switcher-${segment}`)
    expect(item).toHaveAttribute('aria-pressed', 'false')
    await user.click(item)
    expect(useUiStore.getState().activePanel).toEqual({ id, context: { workspaceId: 'ws-1' } })
    await user.click(trigger)
    const pressed = await screen.findByRole('menuitem', { name: label })
    expect(pressed).toHaveAttribute('aria-pressed', 'true')
    expect(pressed).toHaveClass('text-[var(--color-accent)]')
    await user.click(pressed)
    expect(useUiStore.getState().activePanel).toBeNull()
    expect(navigate).not.toHaveBeenCalled()
  })
})

describe('R44 icons keep names, tooltips and pressed state', () => {
  it.each(panels)('$label has no visible label but has a real hover/focus tooltip and the same toggle semantics', async ({ segment, id, label }) => {
    renderHeader(400)
    const user = userEvent.setup()
    const button = screen.getByRole('button', { name: label })
    expect(button).toHaveAttribute('data-testid', `workspace-tab-${segment}`)
    expect(button.textContent).toBe('')
    expect(button).toHaveAttribute('aria-pressed', 'false')
    await user.hover(button)
    expect(screen.getByRole('tooltip').textContent).toBe(label)
    await user.click(button)
    expect(useUiStore.getState().activePanel).toEqual({ id, context: { workspaceId: 'ws-1' } })
    expect(button).toHaveAttribute('aria-pressed', 'true')
    expect(button).toHaveClass('text-[var(--color-accent)]')
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('tooltip')).toBeNull()
    act(() => { button.blur(); button.focus() })
    expect(screen.getByRole('tooltip').textContent).toBe(label)
    expect(button).toHaveAttribute('aria-describedby', screen.getByRole('tooltip').id)
    await user.click(button)
    expect(useUiStore.getState().activePanel).toBeNull()
  })

  it('keeps direct Settings navigation in icons mode', async () => {
    renderHeader(400)
    await userEvent.setup().click(screen.getByRole('button', { name: 'Workspace One — workspace settings' }))
    expect(navigate).toHaveBeenCalledExactlyOnceWith({
      to: '/workspaces/$workspaceId/settings', params: { workspaceId: 'ws-1' },
    })
  })
})
