// Round-1 C: real portalled Hover Card and task actions; no preview mocks.
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Button } from '@/components/ui/button'
import { TaskHoverDetails } from './TaskHoverDetails'
import { TaskCard } from './TaskCard'
import { layoutTask, renderLayout } from './tasksLayoutFixtures'

beforeEach(() => {
  vi.stubGlobal('PointerEvent', class extends MouseEvent {
    pointerType: string
    constructor(type: string, init: PointerEventInit = {}) { super(type, init); this.pointerType = init.pointerType ?? 'mouse' }
  })
})
afterEach(() => vi.unstubAllGlobals())

it('C click dismissal survives restored focus until leaving the task, then keyboard focus can preview again', async () => {
  const user = userEvent.setup()
  const onOpen = vi.fn()
  const task = layoutTask()
  renderLayout(<TaskCard task={task} onClick={onOpen} showActions={false} />)
  const trigger = screen.getByRole('button', { name: /Ray report/ })
  await user.hover(trigger)
  expect(await screen.findByRole('dialog', { name: 'Task preview' })).toBeVisible()
  await user.click(trigger)
  expect(onOpen).toHaveBeenCalledTimes(1)
  fireEvent.focus(trigger) // The task dialog restores focus programmatically.
  expect(screen.queryByRole('dialog', { name: 'Task preview' })).not.toBeInTheDocument()
  await user.unhover(trigger)
  fireEvent.blur(trigger)
  fireEvent.keyDown(document, { key: 'Tab' })
  fireEvent.focus(trigger)
  expect(await screen.findByRole('dialog', { name: 'Task preview' })).toBeVisible()
})

it.each(['Enter', ' '])('C %j opens a keyboard-focused task once and does not reopen its preview on focus restoration', async (key) => {
  const user = userEvent.setup()
  const onOpen = vi.fn()
  renderLayout(<TaskCard task={layoutTask()} onClick={onOpen} showActions={false} />)
  const trigger = screen.getByRole('button', { name: /Ray report/ })
  await user.tab()
  expect(trigger).toHaveFocus()
  expect(await screen.findByRole('dialog', { name: 'Task preview' })).toBeVisible()
  fireEvent.keyDown(trigger, { key })
  expect(onOpen).toHaveBeenCalledTimes(1)
  fireEvent.blur(trigger)
  fireEvent.focus(trigger)
  expect(screen.queryByRole('dialog', { name: 'Task preview' })).not.toBeInTheDocument()
})

it('C Escape dismissal does not reopen on restored focus, but a new keyboard visit previews', async () => {
  const user = userEvent.setup()
  renderLayout(<TaskCard task={layoutTask()} onClick={vi.fn()} showActions={false} />)
  const trigger = screen.getByRole('button', { name: /Ray report/ })
  await user.tab()
  expect(await screen.findByRole('dialog', { name: 'Task preview' })).toBeVisible()
  await user.keyboard('{Escape}')
  fireEvent.focus(trigger)
  expect(screen.queryByRole('dialog', { name: 'Task preview' })).not.toBeInTheDocument()
  fireEvent.blur(trigger)
  fireEvent.keyDown(document, { key: 'Tab' })
  fireEvent.focus(trigger)
  expect(await screen.findByRole('dialog', { name: 'Task preview' })).toBeVisible()
})

it.each([['inbox', 'Run'], ['in_progress', 'Stop']] as const)('C touch on the nested %s action opens its own confirmation without opening a task or preview', async (status, label) => {
  const onOpen = vi.fn()
  renderLayout(<TaskCard task={layoutTask({ status })} onClick={onOpen} />)
  const action = screen.getByRole('button', { name: `${label} task Ray report` })
  const icon = action.querySelector('svg')!
  expect(icon, 'tap the rendered action icon, not just the button background').not.toBeNull()
  fireEvent.pointerDown(icon, { pointerType: 'touch' })
  fireEvent.focus(action)
  fireEvent.click(icon)
  const confirmation = await screen.findByRole('alertdialog')
  expect(within(confirmation).getByText(`${label} this task?`)).toBeVisible()
  expect(onOpen).not.toHaveBeenCalled()
  expect(screen.queryByRole('dialog', { name: 'Task preview' })).not.toBeInTheDocument()
})

it('C enabled=false returns the original child and all pointer, focus, key and click handlers unchanged', async () => {
  const onClick = vi.fn()
  const onFocus = vi.fn()
  const onKeyDown = vi.fn()
  const onPointerDown = vi.fn()
  renderLayout(<TaskHoverDetails task={layoutTask()} onOpenTask={vi.fn()} enabled={false}>
    <Button aria-describedby="existing-description" onClick={onClick} onFocus={onFocus} onKeyDown={onKeyDown} onPointerDown={onPointerDown}>Original action</Button>
  </TaskHoverDetails>)
  const trigger = screen.getByRole('button', { name: 'Original action' })
  fireEvent.pointerDown(trigger, { pointerType: 'touch' })
  fireEvent.focus(trigger)
  fireEvent.keyDown(trigger, { key: 'Enter' })
  fireEvent.click(trigger)
  expect(onPointerDown).toHaveBeenCalledTimes(1)
  expect(onFocus).toHaveBeenCalledTimes(1)
  expect(onKeyDown).toHaveBeenCalledTimes(1)
  expect(onClick).toHaveBeenCalledTimes(1)
  expect(trigger).toHaveAttribute('aria-describedby', 'existing-description')
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Task preview' })).not.toBeInTheDocument())
})
