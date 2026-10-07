import { afterEach, expect, it, vi } from 'vitest'
import { act, render } from '@testing-library/react'
import { WordBoundaryText } from './word-boundary-text'

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

it('preserves exact text and keeps only words that fit the measured owner width atomic across resizes', () => {
  let width = 100
  let resize: ResizeObserverCallback | undefined
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(() => width)
  const widths: Record<string, number> = { Keep: 25, 'whole-word': 90, 'https://example.com/oversized': 140 }
  const measureText = vi.fn((word: string) => ({ width: widths[word] }))
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ measureText } as unknown as CanvasRenderingContext2D)
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: ResizeObserverCallback) { resize = callback }
    observe() {}
    unobserve() {}
    disconnect() {}
  })
  const text = 'Keep  whole-word\t https://example.com/oversized\n'
  const mounted = render(<WordBoundaryText as="p" text={text} />)
  const owner = mounted.container.querySelector('p')!
  const atomicWords = () => [...owner.querySelectorAll('[data-word-boundary]')].map((span) => span.textContent)
  expect(measureText.mock.calls.map(([word]) => word)).toEqual(['Keep', 'whole-word', 'https://example.com/oversized'])
  expect(atomicWords()).toEqual(['Keep', 'whole-word'])
  expect(owner.textContent).toBe(text)

  width = 80
  act(() => resize!([], {} as ResizeObserver))
  expect(atomicWords()).toEqual(['Keep'])
  expect(owner.textContent).toBe(text)

  width = 160
  act(() => resize!([], {} as ResizeObserver))
  expect(atomicWords()).toEqual(['Keep', 'whole-word', 'https://example.com/oversized'])
  expect(owner.textContent).toBe(text)
})
