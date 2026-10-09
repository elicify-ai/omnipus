// Founder T20–T23. Actual Accordion/Checkbox/tiles; one regression per requirement.
import { expect, it, vi } from 'vitest'
import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClientProvider } from '@tanstack/react-query'
import { PlansFilterBand } from './PlansFilterBand'
import { layoutAgent, layoutPlan, renderLayout } from './tasksLayoutFixtures'

const props = { tasks: [], agents: [layoutAgent()], selectedPlanId: null, onSelectPlan: vi.fn(), onNewPlan: vi.fn(), onEditPlan: vi.fn(), onClearPlan: vi.fn(), showNewPlanTile: false }
async function expandForInspection() {
  const trigger = screen.getByRole('button', { name: 'Plans' })
  if (trigger.getAttribute('aria-expanded') === 'false') await userEvent.setup().click(trigger)
}

it('T20 opens by default once plans arrive, respects a deliberate collapse, and starts closed with no plans', async () => {
  const user = userEvent.setup()
  const mounted = renderLayout(<PlansFilterBand {...props} plans={[]} />)
  expect(screen.getByRole('button', { name: 'Plans' })).toHaveAttribute('aria-expanded', 'false')
  mounted.rerender(<QueryClientProvider client={mounted.client}><PlansFilterBand {...props} plans={[layoutPlan()]} /></QueryClientProvider>)
  expect(screen.getByRole('button', { name: 'Plans' })).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByTestId('plan-filter-tile-plan-layout')).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Plans' }))
  mounted.rerender(<QueryClientProvider client={mounted.client}><PlansFilterBand {...props} plans={[layoutPlan(), layoutPlan({ id: 'new-plan' })]} /></QueryClientProvider>)
  expect(screen.getByRole('button', { name: 'Plans' })).toHaveAttribute('aria-expanded', 'false')
})

it('T21 bounds tile-strip top/bottom space to the compact tokens and removes the kit content default bottom pad', async () => {
  renderLayout(<PlansFilterBand {...props} plans={[layoutPlan()]} />)
  await expandForInspection()
  const tile = await screen.findByTestId('all-tasks-tile')
  const strip = tile.parentElement!
  expect(strip).toHaveClass('pt-[var(--space-1)]', 'pb-[var(--space-1)]')
  expect(strip).not.toHaveClass('py-[var(--space-3)]')
  expect(strip.closest('[role="region"]')).toHaveClass('[&>div]:pb-0')
})

it('T22 gives the Plans header one compact vertical inset with no legacy tall top/bottom padding', () => {
  renderLayout(<PlansFilterBand {...props} plans={[layoutPlan()]} />)
  const header = screen.getByRole('heading', { name: 'Plans' }).parentElement!
  expect(header).toHaveClass('py-[var(--space-1)]')
  expect(header).not.toHaveClass('pt-[var(--space-3)]', 'pb-[var(--space-2-5)]')
  expect(within(header).getByRole('button', { name: 'New Plan' })).toBeVisible()
})

it('T23 puts unchecked Unhide done plans below All tasks, outside the header, and reveals completed tiles on check', async () => {
  const user = userEvent.setup()
  renderLayout(<PlansFilterBand {...props} plans={[layoutPlan(), layoutPlan({ id: 'done-plan', title: 'Completed plan', state: 'done' })]} />)
  const header = screen.getByRole('heading', { name: 'Plans' }).parentElement!
  await expandForInspection()
  const check = screen.getByRole('checkbox', { name: 'Unhide done plans' })
  expect(check).not.toBeChecked()
  expect(header).not.toContainElement(check)
  expect(check.closest('[role="region"]')).toContainElement(screen.getByTestId('all-tasks-tile'))
  expect(screen.getByTestId('plans-done-filter').previousElementSibling).toContainElement(screen.getByTestId('all-tasks-tile'))
  expect(screen.queryByRole('button', { name: 'Completed plan' })).not.toBeInTheDocument()
  await user.click(check)
  expect(await screen.findByRole('button', { name: 'Completed plan' })).toBeVisible()
})
