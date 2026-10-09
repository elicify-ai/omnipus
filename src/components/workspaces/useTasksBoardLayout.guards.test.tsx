import { useRef } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { Button } from '@/components/ui/button'
import { useUiStore } from '@/store/ui'
import { useTasksBoardLayout } from './useTasksBoardLayout'

afterEach(() => { vi.unstubAllGlobals(); useUiStore.setState({ activePanel: null, panelWidth: null }) })

function WidthRequest({ takeover }: { takeover: boolean }) {
  const surface = useRef<HTMLDivElement>(null)
  const frame = useRef<HTMLDivElement>(null)
  const { requestBoardWidth } = useTasksBoardLayout(surface, frame, false, 'ws-guards')
  return <aside data-testid="side-panel" data-takeover={takeover ? 'true' : 'false'}><div ref={surface}><div ref={frame}><Button onClick={requestBoardWidth}>Request Board width</Button></div></div></aside>
}

it('does not request a docked width from a takeover Tasks panel', () => {
  useUiStore.setState({ activePanel: { id: 'tasks', context: { workspaceId: 'ws-guards' } }, panelWidth: 641 })
  render(<WidthRequest takeover />)
  fireEvent.click(screen.getByRole('button', { name: 'Request Board width' }))
  expect(useUiStore.getState().panelWidth).toBe(641)
})

it('does not resize the active non-Tasks panel from a retained Tasks surface', () => {
  useUiStore.setState({ activePanel: { id: 'calendar', context: { workspaceId: 'ws-guards' } }, panelWidth: 641 })
  render(<WidthRequest takeover={false} />)
  fireEvent.click(screen.getByRole('button', { name: 'Request Board width' }))
  expect(useUiStore.getState().panelWidth).toBe(641)
  expect(useUiStore.getState().activePanel?.id).toBe('calendar')
})
