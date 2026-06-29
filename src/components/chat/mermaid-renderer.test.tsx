/**
 * mermaid-renderer.tsx — tests for MermaidDiagram (chat Mermaid diagram rendering).
 *
 * Covers the behaviours that make a ```mermaid code fence safe and robust in chat:
 *   - happy path: valid Mermaid -> mermaid.render() -> inline SVG
 *   - error fallback: render throws -> show the source + a small error note (no crash)
 *   - init failure: mermaid.initialize() throws -> "Mermaid initialization failed" fallback
 *   - SECURITY: the rendered SVG is DOMPurify-sanitized (a <script> is stripped)
 *   - loading state before the async render resolves
 *   - unmount: the `cancelled` guard prevents a setState-after-unmount (no act warning)
 *   - re-render when the `code` prop changes
 *
 * `mermaid` is the dynamic-import target inside getMermaid(); we mock it. The
 * component keeps module-level `initialized`/`initFailed` flags, so each test does
 * vi.resetModules() + a fresh dynamic import to get clean module state.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, cleanup } from '@testing-library/react'

// Mock the `mermaid` module. The default export carries initialize + render, which
// getMermaid()/render() call. These vi.fn()s are reconfigured per test.
const initialize = vi.fn()
const renderFn = vi.fn()
vi.mock('mermaid', () => ({ default: { initialize, render: renderFn } }))

// DOMPurify is intentionally NOT mocked — the sanitization test exercises the real one.

beforeEach(() => {
  vi.resetModules() // fresh mermaid-renderer module -> initialized=false each test
  initialize.mockReset().mockReturnValue(undefined)
  renderFn.mockReset()
})
afterEach(() => cleanup())

// Re-import the component AFTER configuring the mocks so module-level flags are fresh.
async function importDiagram() {
  const mod = await import('./mermaid-renderer')
  return mod.MermaidDiagram
}

const DIAGRAM = 'graph TD; A-->B'

describe('MermaidDiagram', () => {
  it('renders a valid diagram as inline SVG (happy path)', async () => {
    renderFn.mockResolvedValue({ svg: '<svg data-testid="diagram"><g id="node"/></svg>' })
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)

    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())
    expect(document.querySelector('#node')).toBeInTheDocument()
    // The diagram source (with a stable per-instance id) is passed to mermaid.render, trimmed.
    expect(renderFn).toHaveBeenCalledWith(expect.stringMatching(/^mermaid-/), DIAGRAM)
  })

  it('shows a loading indicator before the render resolves', async () => {
    let resolve!: (v: { svg: string }) => void
    renderFn.mockReturnValue(new Promise((r) => (resolve = r)))
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)

    // Synchronous first paint: no svg/error yet -> the "Rendering diagram..." state.
    expect(screen.getByText(/Rendering diagram/i)).toBeInTheDocument()
    resolve({ svg: '<svg/>' })
    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())
  })

  it('falls back to the source + error note when render() throws', async () => {
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={'this is not valid mermaid'} />)

    await waitFor(() => expect(screen.getByText('Parse error on line 1')).toBeInTheDocument())
    // The raw source is shown so the user still sees what failed.
    expect(screen.getByText(/this is not valid mermaid/)).toBeInTheDocument()
    // No diagram is injected on the error path.
    expect(document.querySelector('svg')).toBeNull()
  })

  it('shows the init-failure fallback when mermaid.initialize() throws', async () => {
    initialize.mockImplementation(() => {
      throw new Error('init boom')
    })
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)

    await waitFor(() =>
      expect(screen.getByText('Mermaid initialization failed')).toBeInTheDocument(),
    )
    // render() must NOT be reached once init fails.
    expect(renderFn).not.toHaveBeenCalled()
  })

  it('SECURITY: sanitizes the rendered SVG (strips an embedded <script>)', async () => {
    renderFn.mockResolvedValue({
      svg: '<svg><script>window.__pwned = 1</script><g id="safe"/></svg>',
    })
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)

    await waitFor(() => expect(document.querySelector('#safe')).toBeInTheDocument())
    // DOMPurify (svg profile) must have removed the <script>; the benign node survives.
    expect(document.querySelector('script')).toBeNull()
    expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined()
  })

  it('does not warn/update after unmount (cancelled guard)', async () => {
    let resolve!: (v: { svg: string }) => void
    renderFn.mockReturnValue(new Promise((r) => (resolve = r)))
    const MermaidDiagram = await importDiagram()

    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    const { unmount } = render(<MermaidDiagram code={DIAGRAM} />)
    unmount() // unmount BEFORE the render promise resolves
    resolve({ svg: '<svg/>' }) // late resolution must be swallowed by the cancelled guard
    await Promise.resolve()
    await Promise.resolve()

    // An unguarded post-unmount setState would emit a React act/update warning.
    const warned = errSpy.mock.calls.some((c) =>
      String(c[0]).match(/not wrapped in act|unmounted component|update to .* inside a test/i),
    )
    expect(warned).toBe(false)
    errSpy.mockRestore()
  })

  it('SECURITY: initializes mermaid with securityLevel "strict"', async () => {
    // The renderer pins securityLevel:'strict' (mermaid's most restrictive: HTML
    // labels off, click/script directives disabled) because this sink renders on the
    // persisted/reload path. A regression to 'loose' would re-enable script execution
    // in agent-authored diagrams — guard the pin explicitly (DOMPurify is a separate,
    // output-side defense; this asserts mermaid's own input-side posture).
    renderFn.mockResolvedValue({ svg: '<svg/>' })
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)

    await waitFor(() => expect(initialize).toHaveBeenCalled())
    expect(initialize).toHaveBeenCalledWith(expect.objectContaining({ securityLevel: 'strict' }))
  })

  it('paints synchronously from cache on remount (no loading flash, no re-render)', async () => {
    // A finalized message can remount when the virtualized list re-renders. The
    // SVG cache must make the second mount render the stored diagram synchronously
    // instead of flashing "Rendering diagram..." and calling mermaid.render again.
    renderFn.mockResolvedValue({ svg: '<svg id="cached-diagram"/>' })
    const MermaidDiagram = await importDiagram()

    const first = render(<MermaidDiagram code={DIAGRAM} />)
    await waitFor(() => expect(document.querySelector('#cached-diagram')).toBeInTheDocument())
    expect(renderFn).toHaveBeenCalledTimes(1)
    first.unmount()

    // Remount with the SAME code → cache hit: SVG is present on first paint and
    // mermaid.render is NOT called again.
    render(<MermaidDiagram code={DIAGRAM} />)
    expect(screen.queryByText(/Rendering diagram/i)).toBeNull()
    expect(document.querySelector('#cached-diagram')).toBeInTheDocument()
    expect(renderFn).toHaveBeenCalledTimes(1)
  })

  it('re-renders when the code prop changes', async () => {
    renderFn.mockResolvedValue({ svg: '<svg id="first"/>' })
    const MermaidDiagram = await importDiagram()

    const { rerender } = render(<MermaidDiagram code={DIAGRAM} />)
    await waitFor(() => expect(document.querySelector('#first')).toBeInTheDocument())

    renderFn.mockResolvedValue({ svg: '<svg id="second"/>' })
    rerender(<MermaidDiagram code={'graph LR; X-->Y'} />)
    await waitFor(() =>
      expect(renderFn).toHaveBeenLastCalledWith(expect.any(String), 'graph LR; X-->Y'),
    )
  })
})
