// Later founder T15 correction: only the info icon opens hover/focus details.
import { afterEach, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TaskCard } from './TaskCard'
import { layoutTask, renderLayout } from './tasksLayoutFixtures'

afterEach(() => vi.unstubAllGlobals())
it('T15 corrected opens only on info hover/focus, allows entering the panel, closes on leave/Escape and never activates the task', async () => {
  const user = userEvent.setup()
  const onOpen = vi.fn()
  renderLayout(<TaskCard task={layoutTask()} onClick={onOpen} showActions={false} />)
  const title = screen.getByText('Ray report')
  await user.hover(title)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  const info = screen.getByRole('button', { name: 'Task details: Ray report' })
  await user.hover(info)
  const details = await screen.findByRole('dialog', { name: 'Task details' })
  await user.unhover(info)
  await user.hover(details)
  expect(details).toBeVisible()
  expect(onOpen).not.toHaveBeenCalled()
  await user.unhover(details)
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  await user.tab()
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument() // Main card focus.
  await user.tab()
  expect(info).toHaveFocus()
  expect(await screen.findByRole('dialog', { name: 'Task details' })).toBeVisible()
  await user.keyboard('{Escape}')
  fireEvent.focus(info)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  expect(onOpen).not.toHaveBeenCalled()
})

it('T18 every metadata separator follows a value inside its own wrapping group, never leads the next line', () => {
  const mounted = renderLayout(<TaskCard task={layoutTask({ tags: ['docs', 'release'], todos: [{ text: 'Done', status: 'completed' }] })} onClick={vi.fn()} showActions={false} />)
  const separators = [...mounted.container.querySelectorAll<HTMLElement>('[aria-hidden="true"]')].filter((element) => element.textContent === '·')
  expect(separators.length).toBeGreaterThan(0)
  for (const separator of separators) {
    expect(separator.parentElement).toHaveClass('inline-flex')
    expect(separator.previousElementSibling?.textContent?.trim(), 'separator trails the preceding value within a group').toBeTruthy()
  }
})
