// T5-1 GREEN control / FR-014: Leave commits one actual browser Back, not two.
import { afterEach, beforeEach, expect, it } from 'vitest'
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { browserEdgesU2, cleanupNavigationU2, hashAppU2 } from '../../../tests/helpers/settings-u2-navigation'
import { transportG1 } from '../../../tests/helpers/settings-gate-g1'
beforeEach(browserEdgesU2)
afterEach(cleanupNavigationU2)

it('T5-1 GREEN control — Leave after native Back reaches the immediately preceding About entry exactly once', async () => {
  const t = transportG1()
  const view = await hashAppU2('/settings?tab=limits')
  await screen.findByRole('heading', { name: 'Limits', level: 2 })
  await userEvent.click(screen.getByTestId('settings-nav-about'))
  await screen.findByRole('heading', { name: 'About', level: 2 })
  const about = { address: window.location.href, state: structuredClone(window.history.state) }
  await userEvent.click(screen.getByTestId('settings-nav-gateway'))
  fireEvent.change(await screen.findByRole('spinbutton', { name: 'Port' }), { target: { value: '8081' } })
  await screen.findByText('1 unsaved change')
  const length = window.history.length
  await act(async () => { window.history.back() })
  const prompt = await screen.findByRole('alertdialog', { name: 'Leave without saving 1 change?' })
  await userEvent.click(within(prompt).getByRole('button', { name: 'Leave' }))
  await screen.findByRole('heading', { name: 'About', level: 2 })
  await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
  await act(async () => { await new Promise((resolve) => setTimeout(resolve, 200)) })
  expect(window.location.href, 'FR-014: one Back/Leave reaches About, never the older Limits entry').toBe(about.address)
  expect(window.history.state).toEqual(about.state)
  expect(window.history.length).toBe(length)
  expect(view.router.state.location.search).toMatchObject({ tab: 'about' })
  expect(screen.queryByText('1 unsaved change')).not.toBeInTheDocument()
  expect(t.writes()).toEqual([])
})
