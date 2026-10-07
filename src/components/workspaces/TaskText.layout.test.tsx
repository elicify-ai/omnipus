// Oracle: T3 — bounded wrapping, preserved clamps, full text on hover AND focus.
// Compile the real installed Tailwind, not a made-up stylesheet; pixels are checked in the live browser.
import { expect, it, vi } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { compile } from 'tailwindcss'
import { TaskCard } from './TaskCard'
import { PlansFilterBand } from './PlansFilterBand'
import { ListView } from './ListView'
import { layoutAgent, layoutPlan, layoutTask, renderLayout } from './tasksLayoutFixtures'

it('T3 constrains spaced/unbroken plan and task titles, preserves clamps and reveals full text on hover/focus', async () => {
  const user = userEvent.setup()
  const compiler = await compile('@tailwind utilities;')
  const style = document.createElement('style')
  style.textContent = compiler.build(['whitespace-nowrap', 'whitespace-normal', 'max-w-full', 'w-full', 'min-w-0', 'wrap-anywhere', 'truncate', 'line-clamp-2', 'flex-1', 'block'])
  document.head.appendChild(style)
  try {
    for (const title of ['Omnipus marketing + docs website in a new private GitHub repo', 'X'.repeat(200)]) {
      const mounted = renderLayout(<>
        <section aria-label="Plan titles"><PlansFilterBand plans={[layoutPlan({ title })]} tasks={[]} agents={[layoutAgent()]} selectedPlanId={null} onSelectPlan={vi.fn()} onNewPlan={vi.fn()} onEditPlan={vi.fn()} onClearPlan={vi.fn()} /></section>
        <section aria-label="Board titles"><TaskCard task={layoutTask({ title })} onClick={vi.fn()} showActions={false} /></section>
        <section aria-label="List titles"><ListView tasks={[layoutTask({ title })]} agents={[layoutAgent()]} onTaskClick={vi.fn()} /></section>
      </>)
      const plan = within(screen.getByRole('region', { name: 'Plan titles' }))
      const select = plan.getByRole('button', { name: title })
      expect(getComputedStyle(select).whiteSpace, 'plan titles cannot inherit Button nowrap').toBe('normal')
      const planTitle = plan.getByText(title)
      const cardTitle = within(screen.getByRole('region', { name: 'Board titles' })).getByText(title)
      for (const element of [planTitle, cardTitle]) {
        expect(element.textContent).toBe(title)
        expect(getComputedStyle(element).overflowWrap).toBe('anywhere')
        expect(getComputedStyle(element).minWidth).toBe('0px')
        expect(element).toHaveClass('line-clamp-2')
      }
      const listTitle = within(screen.getByRole('region', { name: 'List titles' })).getByRole('button', { name: `${title}, status Inbox` })
      expect(getComputedStyle(listTitle).textOverflow).toBe('ellipsis')
      expect(getComputedStyle(listTitle).whiteSpace).toBe('nowrap')
      for (const trigger of [select, cardTitle, listTitle]) {
        await user.hover(trigger)
        expect(await screen.findByRole('tooltip')).toHaveTextContent(title)
        // The same full text must be keyboard-reachable, not a native-title-only hover.
        fireEvent.blur(trigger.closest('[tabindex]') ?? trigger)
        const focusTarget = trigger.closest<HTMLElement>('[tabindex]') ?? trigger
        fireEvent.focus(focusTarget)
        expect(screen.getByRole('tooltip')).toHaveTextContent(title)
        fireEvent.blur(focusTarget)
      }
      mounted.unmount()
      mounted.client.clear()
    }
  } finally { style.remove() }
})
