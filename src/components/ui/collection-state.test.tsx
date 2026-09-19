import { act, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { CollectionState } from './collection-state'
import { Skeleton } from './skeleton'

// Shared loading contract: delay 400ms, minimum visible 300ms
// (design-system/tokens/foundations.json; src/design-system/foundations.test.ts).

describe('CollectionState — caller-controlled collection lifecycle', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('updates one persistent live region outside the busy content across lifecycle changes', () => {
    const { rerender } = render(
      <CollectionState state="initial-loading" loading={<div>Loading rows</div>} empty={<div>Nothing here</div>} error={<div>Could not load</div>} />,
    )
    const liveRegion = document.querySelector('[data-collection-announcement]') as HTMLElement
    const busyContent = liveRegion.nextElementSibling
    expect(liveRegion).toHaveTextContent('Loading')
    expect(busyContent).toHaveAttribute('aria-busy', 'true')
    expect(busyContent).not.toContainElement(liveRegion)

    rerender(<CollectionState state="refreshing" loading={<div>Loading rows</div>} empty={null} error={null}>Cached rows</CollectionState>)
    expect(document.querySelector('[data-collection-announcement]')).toBe(liveRegion)
    expect(liveRegion).toHaveTextContent('Refreshing')
    expect(busyContent).toHaveAttribute('aria-busy', 'true')

    rerender(<CollectionState state="ready" loading={<div>Loading rows</div>} empty={null} error={null}>Ready rows</CollectionState>)
    expect(document.querySelector('[data-collection-announcement]')).toBe(liveRegion)
    expect(liveRegion).toHaveTextContent('Loaded')
    expect(busyContent).toHaveAttribute('aria-busy', 'false')
  })

  it.each([
    ['empty', 'Empty'],
    ['partial', 'Partially loaded'],
    ['error', 'Unable to load'],
  ] as const)('announces the caller-controlled %s state as %s', (state, announcement) => {
    render(<CollectionState state={state} loading={null} empty={null} error={null} />)
    expect(document.querySelector('[data-collection-announcement]')).toHaveTextContent(announcement)
  })

  it.each([
    ['empty', 'Nothing here'],
    ['error', 'Could not load'],
    ['ready', 'Current items'],
    ['partial', 'Some items'],
  ] as const)('renders the caller-owned %s content', (state, text) => {
    render(
      <CollectionState
        state={state}
        loading={<div>Loading rows</div>}
        empty={<div>Nothing here</div>}
        error={<div>Could not load</div>}
      >
        <div>{state === 'partial' ? 'Some items' : 'Current items'}</div>
      </CollectionState>,
    )
    expect(screen.getByText(text)).toBeInTheDocument()
  })

  it('retains cached children and exposes a non-blocking refresh indicator', () => {
    render(
      <CollectionState state="refreshing" loading={<div>Refreshing</div>} empty={null} error={null}>
        <div>Cached item</div>
      </CollectionState>,
    )
    expect(screen.getByText('Cached item')).toBeInTheDocument()
    act(() => vi.advanceTimersByTime(400))
    const refreshIndicator = document.querySelector('[data-collection-refresh]')
    expect(refreshIndicator).toHaveTextContent('Refreshing')
    expect(refreshIndicator).not.toHaveAttribute('aria-hidden')
  })

  it('keeps a supplied control inert during the delay and accessible once visible', () => {
    render(<CollectionState state="refreshing" loading={<button>Pause refresh</button>} empty={null} error={null}>Cached</CollectionState>)
    const reservedControl = screen.getByText('Pause refresh')
    expect(reservedControl.parentElement).toHaveAttribute('inert')
    expect(screen.queryByRole('button', { name: 'Pause refresh' })).not.toBeInTheDocument()
    act(() => vi.advanceTimersByTime(399))
    expect(screen.queryByRole('button', { name: 'Pause refresh' })).not.toBeInTheDocument()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByRole('button', { name: 'Pause refresh' })).toBeVisible()
    expect(screen.getByRole('button', { name: 'Pause refresh' }).parentElement).not.toHaveAttribute('aria-hidden')
    expect(screen.getByRole('button', { name: 'Pause refresh' }).parentElement).not.toHaveAttribute('inert')
  })

  it('reserves initial-loading layout immediately', () => {
    render(
      <CollectionState state="initial-loading" loading={<div data-testid="reserved">Rows</div>} empty={null} error={null} />,
    )
    expect(screen.getByTestId('reserved')).toBeInTheDocument()
    expect(screen.getByTestId('reserved').parentElement).toHaveAttribute('data-visible', 'false')
  })

  it('appears exactly at the 400ms delay boundary', () => {
    render(<CollectionState state="initial-loading" loading={<div data-testid="loading">Rows</div>} empty={null} error={null} />)
    const wrapper = screen.getByTestId('loading').parentElement
    act(() => vi.advanceTimersByTime(399))
    expect(wrapper).toHaveAttribute('data-visible', 'false')
    act(() => vi.advanceTimersByTime(1))
    expect(wrapper).toHaveAttribute('data-visible', 'true')
  })

  it('retains a real Skeleton for its minimum dwell when ready follows visibility', () => {
    const loading = <Skeleton pending data-testid="skeleton" />
    const { rerender } = render(<CollectionState state="initial-loading" loading={loading} empty={null} error={null}>Ready rows</CollectionState>)
    act(() => vi.advanceTimersByTime(400))
    expect(screen.getByTestId('skeleton')).toHaveAttribute('data-visible', 'true')

    rerender(<CollectionState state="ready" loading={loading} empty={null} error={null}>Ready rows</CollectionState>)
    act(() => vi.advanceTimersByTime(299))
    expect(screen.getByTestId('skeleton')).toHaveAttribute('data-visible', 'true')
    expect(screen.queryByText('Ready rows')).not.toBeInTheDocument()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByTestId('skeleton')).not.toBeInTheDocument()
    expect(screen.getByText('Ready rows')).toBeVisible()
  })

  it.each([
    ['error', 'Could not load'],
    ['empty', 'Nothing here'],
  ] as const)('holds the shown skeleton through 299ms then presents %s at the 300ms dwell boundary', (state, text) => {
    const retry = vi.fn()
    const loading = <Skeleton pending data-testid="skeleton" />
    const empty = <div>Nothing here</div>
    const error = (
      <div>
        <div>Could not load</div>
        <button type="button" onClick={retry}>Retry</button>
      </div>
    )
    const { rerender } = render(<CollectionState state="initial-loading" loading={loading} empty={empty} error={error} />)
    act(() => vi.advanceTimersByTime(400))
    expect(screen.getByTestId('skeleton')).toBeInTheDocument()

    rerender(<CollectionState state={state} loading={loading} empty={empty} error={error} />)
    act(() => vi.advanceTimersByTime(299))
    expect(screen.getByTestId('skeleton')).toBeInTheDocument()
    expect(screen.queryByText(text)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(retry).not.toHaveBeenCalled()

    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByTestId('skeleton')).not.toBeInTheDocument()
    expect(screen.getByText(text)).toBeVisible()
    if (state === 'error') {
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
      expect(retry).toHaveBeenCalledOnce()
    } else {
      expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
      expect(retry).not.toHaveBeenCalled()
    }
  })

  it('keeps cached content beside a refresh indicator through its dwell', () => {
    const loading = <div>Refresh indicator</div>
    const { rerender } = render(<CollectionState state="refreshing" loading={loading} empty={null} error={null}>Cached rows</CollectionState>)
    act(() => vi.advanceTimersByTime(400))
    rerender(<CollectionState state="ready" loading={loading} empty={null} error={null}>Cached rows</CollectionState>)
    act(() => vi.advanceTimersByTime(299))
    expect(screen.getByText('Cached rows')).toBeVisible()
    expect(screen.getByText('Refresh indicator').parentElement).toHaveAttribute('data-visible', 'true')
    act(() => vi.advanceTimersByTime(1))
    expect(screen.getByText('Cached rows')).toBeVisible()
    expect(screen.queryByText('Refresh indicator')).not.toBeInTheDocument()
  })

  it('never exposes loading when the operation finishes before the delay', () => {
    const { rerender } = render(<CollectionState state="initial-loading" loading={<button>Cancel</button>} empty={null} error={null}>Ready rows</CollectionState>)
    act(() => vi.advanceTimersByTime(399))
    rerender(<CollectionState state="ready" loading={<button>Cancel</button>} empty={null} error={null}>Ready rows</CollectionState>)
    act(() => vi.runAllTimers())
    expect(screen.queryByText('Cancel')).not.toBeInTheDocument()
    expect(screen.getByText('Ready rows')).toBeVisible()
  })

  it.each([
    ['error', 'Could not load'],
    ['empty', 'Nothing here'],
  ] as const)('presents %s without a skeleton when the load finishes before the 400ms delay', (state, text) => {
    const retry = vi.fn()
    const loading = <Skeleton pending data-testid="skeleton" />
    const empty = <div>Nothing here</div>
    const error = (
      <div>
        <div>Could not load</div>
        <button type="button" onClick={retry}>Retry</button>
      </div>
    )
    const { rerender } = render(<CollectionState state="initial-loading" loading={loading} empty={empty} error={error} />)
    act(() => vi.advanceTimersByTime(399))
    rerender(<CollectionState state={state} loading={loading} empty={empty} error={error} />)
    act(() => vi.runAllTimers())
    expect(screen.queryByTestId('skeleton')).not.toBeInTheDocument()
    expect(screen.getByText(text)).toBeVisible()
    if (state === 'error') {
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
      expect(retry).toHaveBeenCalledOnce()
    } else {
      expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
      expect(retry).not.toHaveBeenCalled()
    }
  })

  it('invokes no callbacks because lifecycle ownership stays with the caller', () => {
    const callback = vi.fn()
    render(<CollectionState state="ready" loading={null} empty={null} error={null}><button onClick={callback}>Owned action</button></CollectionState>)
    expect(callback).not.toHaveBeenCalled()
  })
})


describe('CollectionState — announcement and visual dwell separation', () => {
  beforeEach(() => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date', 'performance'] }))
  afterEach(() => vi.useRealTimers())

  it('keeps a polite atomic status outside busy content through every caller state', () => {
    const { rerender } = render(<CollectionState state="initial-loading" loading={null} empty={null} error={null} />)
    const region = screen.getByRole('status')
    for (const [state, announcement, busy] of [
      ['initial-loading', 'Loading', 'true'], ['refreshing', 'Refreshing', 'true'],
      ['partial', 'Partially loaded', 'false'], ['ready', 'Loaded', 'false'],
      ['empty', 'Empty', 'false'], ['error', 'Unable to load', 'false'],
    ] as const) {
      rerender(<CollectionState state={state} loading={null} empty={null} error={null} />)
      expect(screen.getAllByRole('status')).toEqual([region])
      expect(region).toHaveAttribute('aria-live', 'polite')
      expect(region).toHaveAttribute('aria-atomic', 'true')
      expect(region.textContent).toBe(announcement)
      expect(region.closest('[aria-busy]')).toBeNull()
      expect(region.nextElementSibling).toHaveAttribute('aria-busy', busy)
    }
  })

  it.each([
    ['initial-loading', 'ready', 'Loaded'], ['initial-loading', 'partial', 'Partially loaded'],
    ['refreshing', 'ready', 'Loaded'], ['refreshing', 'partial', 'Partially loaded'],
  ] as const)('announces %s to %s immediately while completing the 300ms visual dwell', (pendingState, state, announcement) => {
    const loading = <div data-testid="dwell-placeholder">Loading rows</div>
    const { rerender } = render(<CollectionState state={pendingState} loading={loading} empty={null} error={null}>Cached rows</CollectionState>)
    const region = screen.getByRole('status')
    // Shared contract: show after 400ms, retain for 300ms once shown.
    act(() => vi.advanceTimersByTime(400))
    expect(screen.getByTestId('dwell-placeholder').parentElement).toHaveAttribute('data-visible', 'true')
    rerender(<CollectionState state={state} loading={loading} empty={null} error={null}>Current rows</CollectionState>)
    expect(screen.getByRole('status')).toBe(region)
    expect(region.textContent).toBe(announcement)
    expect(region.nextElementSibling).toHaveAttribute('aria-busy', 'false')
    expect(screen.getByTestId('dwell-placeholder').parentElement).toHaveAttribute('data-visible', 'true')
    act(() => vi.advanceTimersByTime(299))
    expect(screen.getByTestId('dwell-placeholder').parentElement).toHaveAttribute('data-visible', 'true')
    expect(region.textContent).toBe(announcement)
    expect(region.nextElementSibling).toHaveAttribute('aria-busy', 'false')
    if (pendingState === 'initial-loading') expect(screen.queryByText('Current rows')).not.toBeInTheDocument()
    else expect(screen.getByText('Current rows')).toBeVisible()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByTestId('dwell-placeholder')).not.toBeInTheDocument()
    expect(screen.getByText('Current rows')).toBeVisible()
    expect(screen.getByRole('status')).toBe(region)
    expect(region.textContent).toBe(announcement)
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByTestId('dwell-placeholder')).not.toBeInTheDocument()
  })
})
