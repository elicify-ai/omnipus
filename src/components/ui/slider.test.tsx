import { fireEvent, render, screen } from '@testing-library/react'
import { beforeAll, describe, expect, it, vi } from 'vitest'
import { Slider } from './slider'

beforeAll(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

describe('Slider — value contract', () => {
  it('exposes controlled value and keyboard changes through the slider role', () => {
    const onValueChange = vi.fn()
    render(<Slider aria-label="Volume" value={[40]} onValueChange={onValueChange} />)
    const slider = screen.getByRole('slider', { name: 'Volume' })
    expect(slider).toHaveAttribute('aria-valuenow', '40')
    fireEvent.keyDown(slider, { key: 'ArrowRight' })
    expect(onValueChange).toHaveBeenCalled()
  })

  it('does not change or invoke its callback while disabled', () => {
    const onValueChange = vi.fn()
    render(<Slider aria-label="Volume" value={[40]} disabled onValueChange={onValueChange} />)
    const slider = screen.getByRole('slider', { name: 'Volume' })
    fireEvent.keyDown(slider, { key: 'ArrowRight' })
    expect(slider).toHaveAttribute('aria-valuenow', '40')
    expect(onValueChange).not.toHaveBeenCalled()
    expect(slider).toHaveAttribute('data-disabled')
  })

  it('changes track geometry for vertical orientation', () => {
    const { container } = render(<Slider aria-label="Volume" defaultValue={[40]} orientation="vertical" />)
    expect(container.firstElementChild).toHaveClass('data-[orientation=vertical]:h-full')
    expect(container.querySelector('[data-orientation="vertical"] > span')).toHaveClass('data-[orientation=vertical]:h-full')
  })

  it('renders and changes every controlled range thumb with a distinct name', () => {
    const onValueChange = vi.fn()
    render(
      <Slider
        value={[20, 80]}
        min={0}
        max={100}
        thumbLabels={['Minimum price', 'Maximum price']}
        aria-describedby="price-help"
        onValueChange={onValueChange}
      />,
    )
    const minimum = screen.getByRole('slider', { name: 'Minimum price' })
    const maximum = screen.getByRole('slider', { name: 'Maximum price' })
    expect(minimum).toHaveAttribute('aria-valuenow', '20')
    expect(maximum).toHaveAttribute('aria-valuenow', '80')
    expect(minimum).toHaveAttribute('aria-describedby', 'price-help')
    expect(maximum).toHaveAttribute('aria-describedby', 'price-help')
    fireEvent.keyDown(minimum, { key: 'ArrowRight' })
    expect(onValueChange).toHaveBeenCalledWith([21, 80])
  })

  it('supports an uncontrolled multi-thumb default without dropping a value', () => {
    render(<Slider defaultValue={[10, 90]} thumbLabels={['Lower bound', 'Upper bound']} />)
    expect(screen.getByRole('slider', { name: 'Lower bound' })).toHaveAttribute('aria-valuenow', '10')
    expect(screen.getByRole('slider', { name: 'Upper bound' })).toHaveAttribute('aria-valuenow', '90')
  })

  it('submits single and multi-thumb values through native form data', () => {
    const { container } = render(
      <form>
        <Slider name="volume" defaultValue={[40]} aria-label="Volume" />
        <Slider name="price" defaultValue={[20, 80]} thumbLabels={['Minimum price', 'Maximum price']} />
      </form>,
    )
    const data = new FormData(container.querySelector('form')!)
    expect(data.getAll('volume')).toEqual(['40'])
    expect(data.getAll('price[]')).toEqual(['20', '80'])
    expect(data.getAll('price')).toEqual([])
  })

  it('forwards visible-label identity and description to the single operative thumb', () => {
    render(
      <>
        <span id="volume-label">Volume</span>
        <span id="volume-help">Choose an output level</span>
        <Slider value={[40]} aria-labelledby="volume-label" aria-describedby="volume-help" />
      </>,
    )
    const slider = screen.getByRole('slider', { name: 'Volume' })
    expect(slider).toHaveAttribute('aria-labelledby', 'volume-label')
    expect(slider).toHaveAttribute('aria-describedby', 'volume-help')
  })

  it.each([
    undefined,
    ['Only one'],
    ['Same', 'Same'],
    ['Lower', ''],
  ])('rejects missing or ambiguous multi-thumb labels: %s', (thumbLabels) => {
    expect(() => render(<Slider value={[20, 80]} thumbLabels={thumbLabels} />))
      .toThrow('thumbLabels must provide one distinct non-empty name for each slider thumb')
  })
})
