// T15 supersedes the former C hover/focus activation. Keep dismissal, native
// main actions, SVG touch isolation and visual-only drag-clone coverage.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { TaskCard } from './TaskCard'
import { layoutTask, renderLayout } from './tasksLayoutFixtures'

beforeEach(() => {
  vi.stubGlobal('PointerEvent', class extends MouseEvent {
    pointerType: string
    constructor(type: string, init: PointerEventInit = {}) { super(type, init); this.pointerType = init.pointerType ?? 'mouse' }
  })
})
afterEach(() => vi.unstubAllGlobals())

it('T15 task click and restored focus never open the info popover', async () => {
  const user = userEvent.setup()
  const onOpen = vi.fn()
  renderLayout(<TaskCard task={layoutTask()} onClick={onOpen} showActions={false} />)
  const trigger = screen.getByText('Ray report').closest<HTMLElement>('[role="button"]')!
  await user.hover(trigger)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  await user.click(trigger)
  expect(onOpen).toHaveBeenCalledTimes(1)
  fireEvent.focus(trigger)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it.each(['Enter', ' '])('T15 %j still opens the main task once, while the focused info icon opens only its details', async (key) => {
  const user = userEvent.setup()
  const onOpen = vi.fn()
  renderLayout(<TaskCard task={layoutTask()} onClick={onOpen} showActions={false} />)
  const trigger = screen.getByText('Ray report').closest<HTMLElement>('[role="button"]')!
  fireEvent.keyDown(trigger, { key })
  expect(onOpen).toHaveBeenCalledTimes(1)
  fireEvent.focus(trigger)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  const info = screen.getByRole('button', { name: 'Task details: Ray report' })
  info.focus()
  await user.keyboard(key === ' ' ? ' ' : '{Enter}')
  expect(await screen.findByRole('dialog', { name: 'Task details' })).toBeVisible()
  expect(onOpen).toHaveBeenCalledTimes(1)
})

it('T15 Escape closes details and restored info focus does not reopen them', async () => {
  const user = userEvent.setup()
  renderLayout(<TaskCard task={layoutTask()} onClick={vi.fn()} showActions={false} />)
  const info = screen.getByRole('button', { name: 'Task details: Ray report' })
  await user.click(info)
  expect(await screen.findByRole('dialog', { name: 'Task details' })).toBeVisible()
  await user.keyboard('{Escape}')
  fireEvent.focus(info)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})

it.each([['inbox', 'Run'], ['in_progress', 'Stop']] as const)('T15 touch on the nested %s action opens its own confirmation without opening a task or details', async (status, label) => {
  const onOpen = vi.fn()
  renderLayout(<TaskCard task={layoutTask({ status })} onClick={onOpen} />)
  const action = screen.getByRole('button', { name: `${label} task Ray report` })
  const icon = action.querySelector('svg')!
  expect(icon).not.toBeNull()
  fireEvent.pointerDown(icon, { pointerType: 'touch' })
  fireEvent.focus(action)
  fireEvent.click(icon)
  const confirmation = await screen.findByRole('alertdialog')
  expect(within(confirmation).getByText(`${label} this task?`)).toBeVisible()
  expect(onOpen).not.toHaveBeenCalled()
  expect(screen.queryByRole('dialog', { name: 'Task details' })).not.toBeInTheDocument()
})

it('T15 a visual drag clone has no details controls and preserves its original main action handlers', () => {
  const onOpen = vi.fn()
  renderLayout(<TaskCard task={layoutTask()} onClick={onOpen} showDetails={false} showActions={false} />)
  expect(screen.queryByRole('button', { name: /Task details:/ })).not.toBeInTheDocument()
  const trigger = screen.getByText('Ray report').closest<HTMLElement>('[role="button"]')!
  fireEvent.pointerDown(trigger, { pointerType: 'touch' })
  fireEvent.focus(trigger)
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
  expect(onOpen).toHaveBeenCalledTimes(2)
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})
