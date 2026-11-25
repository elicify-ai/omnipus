// U2-2 oracle: US-9 AS4, Accessibility and Keyboard. Real AppShell and Sidebar.
// T4-3: U2-4 and the U4 histories now each have their own *.history-*.test.tsx
// file (one page per process); assertions are shared in settings-u4-history-cases.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useSidebarStore } from '@/store/sidebar'
import { browserEdgesU2, cleanupNavigationU2, hashAppU2, wideViewportU2 } from '../../../tests/helpers/settings-u2-navigation'
import { transportG1 } from '../../../tests/helpers/settings-gate-g1'

beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)

it('U2-2 — pinned sidebar: the first Tab from a fresh Settings address reaches Skip to settings content', async () => {
  transportG1()
  wideViewportU2()
  useSidebarStore.getState().pin()
  await hashAppU2('/settings?tab=gateway')
  await screen.findByRole('spinbutton', { name: 'Port' })
  expect(screen.getByRole('button', { name: 'Unpin sidebar' }), 'instrument: the real pinned Sidebar is mounted').toBeVisible()
  expect(useSidebarStore.getState().isPinned).toBe(true)
  expect(document.activeElement, 'fresh address premise: the fixture did not focus a control').toBe(document.body)
  const address = window.location.href
  const user = userEvent.setup()
  await user.tab()
  expect(screen.getByRole('link', { name: 'Skip to settings content' }),
    'U2-2/US-9 AS4: pinned navigation must not precede the Settings skip link').toHaveFocus()
  await user.keyboard('{Enter}')
  expect(document.getElementById('settings-content')).toHaveFocus()
  expect(window.location.href).toBe(address)
})
