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
 *   - fix-button: async handleFix, streaming guard, duplicate-send guard, remount guard
 *   - streaming handoff: dropping `streaming` prop triggers error card (not silent)
 *   - two distinct malformed codes produce distinct, non-contaminated error cards
 *   - toolbar toggle: image/code view switch
 *   - toolbar copy: calls correct media-actions helper per view
 *   - toolbar enlarge: opens the global media lightbox (store) with the sanitized svg
 *   - toolbar NOT rendered during streaming or on error path
 *
 * `mermaid` is the dynamic-import target inside getMermaid(); we mock it. The
 * component keeps module-level `initialized`/`initFailed` flags, so each test does
 * vi.resetModules() + a fresh dynamic import to get clean module state.
 *
 * FIX 2: the store mock now uses getState() (matching the new lazy-import pattern in
 * handleFix). The eager top-level store import was removed from mermaid-renderer.tsx,
 * so the module no longer drags the full chat store into every markdown consumer.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, cleanup, fireEvent } from '@testing-library/react'
import { normalizeMermaidSource } from './mermaid-renderer'

// Layer-2 deterministic normalizer: smart/typographic quotes → straight ASCII.
describe('normalizeMermaidSource', () => {
  it('replaces smart double quotes (“ ” « » „ ‟) with straight "', () => {
    expect(normalizeMermaidSource('A[“📦 Box”]')).toBe('A["📦 Box"]')
    expect(normalizeMermaidSource('subgraph «Title»')).toBe('subgraph "Title"')
    expect(normalizeMermaidSource('x[„low‟]')).toBe('x["low"]')
  })
  it('replaces smart single quotes (‘ ’ ‚ ‛) with straight apostrophes', () => {
    expect(normalizeMermaidSource('G[“Wind me at midnight.”]')).toBe('G["Wind me at midnight."]')
    expect(normalizeMermaidSource('Elara’s box')).toBe("Elara's box")
  })
  it('leaves already-valid source (straight quotes) untouched', () => {
    const valid = 'graph TD\n  A["Start"] --> B{Decide}\n'
    expect(normalizeMermaidSource(valid)).toBe(valid)
  })
  it('does NOT rewrite structure — bracket/keyword mismatches are left as-is', () => {
    // It only fixes quotes; a `]`/`}` mismatch or reserved-word id stays for the
    // error card + the LLM fix flow.
    expect(normalizeMermaidSource('D[“x”}')).toBe('D["x"}')
    expect(normalizeMermaidSource('class A,B end')).toBe('class A,B end')
  })
})

// Mock the `mermaid` module. The default export carries initialize + render, which
// getMermaid()/render() call. These vi.fn()s are reconfigured per test.
const initialize = vi.fn()
const renderFn = vi.fn()
vi.mock('mermaid', () => ({ default: { initialize, render: renderFn } }))

// FIX 2: The error card's handleFix now does a LAZY `await import('@/store/chat')`
// and then calls `useChatStore.getState()` — match that shape here.
// isStreaming is mutable so tests can set it to true/false between assertions.
const sendMessageMock = vi.fn()
const storeState = { sendMessage: sendMessageMock, isStreaming: false }
vi.mock('@/store/chat', () => ({ useChatStore: { getState: () => storeState } }))

// Mock Phase A media-actions helpers so tests can assert calls without DOM/canvas.
const copyTextMock = vi.fn().mockResolvedValue(undefined)
const copyImageBlobMock = vi.fn().mockResolvedValue(undefined)
const svgToPngBlobMock = vi.fn().mockResolvedValue(new Blob(['png'], { type: 'image/png' }))
const downloadBlobMock = vi.fn()
const canCopyImageMock = vi.fn().mockReturnValue(true)

vi.mock('./media-actions', () => ({
  copyText: (...args: unknown[]) => copyTextMock(...args),
  copyImageBlob: (...args: unknown[]) => copyImageBlobMock(...args),
  svgToPngBlob: (...args: unknown[]) => svgToPngBlobMock(...args),
  downloadBlob: (...args: unknown[]) => downloadBlobMock(...args),
  canCopyImage: () => canCopyImageMock(),
}))

// Mock MediaActionToolbar as a simple passthrough that renders buttons by label,
// so tests can query by aria-label without needing real toolbar styling.
vi.mock('./MediaActionToolbar', () => ({
  MediaActionToolbar: ({ actions }: { actions: Array<{ label: string; onClick: () => void }> }) => (
    <div role="toolbar" aria-label="Media actions">
      {actions.map((a) => (
        <button key={a.label} type="button" aria-label={a.label} onClick={a.onClick}>
          {a.label}
        </button>
      ))}
    </div>
  ),
}))

// Enlarge no longer renders a per-row ImageLightbox — it opens the single app-root
// MediaLightbox via the real @/store/ui store, which these tests assert directly.

// DOMPurify is intentionally NOT mocked — the sanitization test exercises the real one.

beforeEach(() => {
  vi.resetModules() // fresh mermaid-renderer module -> initialized=false each test
  initialize.mockReset().mockReturnValue(undefined)
  renderFn.mockReset()
  sendMessageMock.mockReset()
  storeState.isStreaming = false
  copyTextMock.mockReset().mockResolvedValue(undefined)
  copyImageBlobMock.mockReset().mockResolvedValue(undefined)
  svgToPngBlobMock.mockReset().mockResolvedValue(new Blob(['png'], { type: 'image/png' }))
  downloadBlobMock.mockReset()
  canCopyImageMock.mockReset().mockReturnValue(true)
})
afterEach(() => cleanup())

// Re-import the component AFTER configuring the mocks so module-level flags are fresh.
async function importDiagram() {
  const mod = await import('./mermaid-renderer')
  return mod.MermaidDiagram
}

const DIAGRAM = 'graph TD; A-->B'
const SVG_FIXTURE = '<svg data-testid="diagram"><g id="node"/></svg>'

describe('MermaidDiagram', () => {
  it('renders a valid diagram as inline SVG (happy path)', async () => {
    renderFn.mockResolvedValue({ svg: SVG_FIXTURE })
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

  it('shows the error card (short message + syntax error + collapsible source) when render() throws', async () => {
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={'this is not valid mermaid'} />)

    await waitFor(() => expect(screen.getByText('Parse error on line 1')).toBeInTheDocument())
    // Short headline, not a raw dump of the source.
    expect(screen.getByText(/Couldn't render the diagram/i)).toBeInTheDocument()
    // Source still available (in the collapsible <details>).
    expect(screen.getByText(/this is not valid mermaid/)).toBeInTheDocument()
    // A "fix" affordance is offered.
    expect(screen.getByRole('button', { name: /Ask assistant to fix/i })).toBeInTheDocument()
  })

  it('error card "Ask assistant to fix" sends the syntax error + code back to the LLM', async () => {
    renderFn.mockRejectedValue(new Error("Parse error on line 3: Expecting 'PE', got '1'"))
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={'graph TD; A--B'} />)

    const btn = await screen.findByRole('button', { name: /Ask assistant to fix/i })
    fireEvent.click(btn)

    // handleFix is async (lazy store import) — wait for sendMessage to be called.
    await waitFor(() => expect(sendMessageMock).toHaveBeenCalledTimes(1))
    const sent = sendMessageMock.mock.calls[0][0] as string
    expect(sent).toContain("Parse error on line 3: Expecting 'PE', got '1'") // the syntax detail
    expect(sent).toContain('graph TD; A--B') // the original code
    expect(sent).toMatch(/corrected Mermaid/i) // a clear fix request
    // The prompt uses a backtick fence — guard that it includes the mermaid fence marker.
    expect(sent).toContain('```mermaid')
    // Button reflects the sent state (disabled to prevent double-send).
    expect(screen.getByRole('button', { name: /Sent to assistant/i })).toBeInTheDocument()
  })

  it('fix button: a second click does NOT call sendMessage again (disabled after sent)', async () => {
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={'graph TD; A--C'} />)

    const btn = await screen.findByRole('button', { name: /Ask assistant to fix/i })
    fireEvent.click(btn)
    await waitFor(() => expect(sendMessageMock).toHaveBeenCalledTimes(1))

    // Button is now disabled — a second click must be ignored.
    fireEvent.click(btn)
    await waitFor(() => expect(sendMessageMock).toHaveBeenCalledTimes(1))
  })

  it('fix button: clicking while a turn is streaming does NOT call sendMessage', async () => {
    // Set isStreaming=true before the click — handleFix guards against this.
    storeState.isStreaming = true
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={'graph TD; A--D'} />)

    const btn = await screen.findByRole('button', { name: /Ask assistant to fix/i })
    fireEvent.click(btn)

    // Give async handleFix time to run (it awaits the store import then returns early).
    await new Promise((r) => setTimeout(r, 20))
    expect(sendMessageMock).not.toHaveBeenCalled()
  })

  it('fix button: remount with the same code shows "Sent to assistant" (module-level cache)', async () => {
    // Covers FIX 2: fixRequestedCache is module-level, so a virtualized-list remount
    // must not reset the button state to "Ask assistant to fix".
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    const { unmount } = render(<MermaidDiagram code={'graph TD; A--E'} />)

    const btn = await screen.findByRole('button', { name: /Ask assistant to fix/i })
    fireEvent.click(btn)
    await waitFor(() => expect(sendMessageMock).toHaveBeenCalledTimes(1))

    unmount()

    // The error cache from the first render means the error card shows synchronously
    // on the new mount (no async wait needed); the sent state seeds from fixRequestedCache.
    render(<MermaidDiagram code={'graph TD; A--E'} />)
    expect(screen.getByRole('button', { name: /Sent to assistant/i })).toBeInTheDocument()
  })

  it('streaming handoff: error card appears after streaming=false, not before', async () => {
    // Pins that the effect depends on `streaming` — dropping it from the deps would
    // silently break the handoff so the error card never appears.
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    const { rerender } = render(<MermaidDiagram code={DIAGRAM} streaming />)

    // While streaming: nothing rendered, including no error card.
    await waitFor(() => expect(renderFn).toHaveBeenCalled())
    expect(screen.queryByText(/Couldn't render/i)).toBeNull()

    // Handoff: streaming ends → the effect reruns → error card appears.
    rerender(<MermaidDiagram code={DIAGRAM} />)
    await waitFor(() => expect(screen.getByText(/Couldn't render the diagram/i)).toBeInTheDocument())
  })

  it('two distinct malformed codes produce distinct, non-contaminated error messages', async () => {
    // Guards that renderErrorCache is keyed by code and does not cross-contaminate:
    // a new code must call mermaid.render() again and show its own error.
    const MermaidDiagram = await importDiagram()

    renderFn.mockRejectedValue(new Error('err-A'))
    render(<MermaidDiagram code={'AAA'} />)
    await waitFor(() => expect(screen.getByText('err-A')).toBeInTheDocument())

    cleanup()

    renderFn.mockRejectedValue(new Error('err-B'))
    render(<MermaidDiagram code={'BBB'} />)
    await waitFor(() => expect(renderFn).toHaveBeenCalledWith(expect.any(String), 'BBB'))
    await waitFor(() => expect(screen.getByText('err-B')).toBeInTheDocument())
    expect(screen.queryByText('err-A')).toBeNull()
  })

  it('streaming: renders NOTHING while the partial code fails to parse (no naked source, no error)', async () => {
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    const { container } = render(<MermaidDiagram code={'graph T'} streaming />)

    await waitFor(() => expect(renderFn).toHaveBeenCalled())
    expect(screen.queryByText(/Couldn't render/i)).toBeNull()
    expect(screen.queryByText(/Rendering diagram/i)).toBeNull()
    expect(screen.queryByText(/graph T/)).toBeNull()
    expect(container).toBeEmptyDOMElement()
  })

  it('streaming: shows no placeholder while loading, then pops in the SVG when ready', async () => {
    let resolve!: (v: { svg: string }) => void
    renderFn.mockReturnValue(new Promise((r) => (resolve = r)))
    const MermaidDiagram = await importDiagram()

    const { container } = render(<MermaidDiagram code={DIAGRAM} streaming />)

    // While streaming + loading: nothing at all (no "Rendering diagram..." placeholder).
    expect(screen.queryByText(/Rendering diagram/i)).toBeNull()
    expect(container).toBeEmptyDOMElement()

    resolve({ svg: '<svg id="streamed"/>' })
    await waitFor(() => expect(document.querySelector('#streamed')).toBeInTheDocument())
  })

  it('streaming: does NOT log render failures (avoids mid-stream console spam)', async () => {
    const errSpy = vi.spyOn(console, 'error').mockImplementation(() => {})
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={'graph T'} streaming />)
    // Wait until renderFn has been invoked and the rejection settled.
    await waitFor(() => expect(renderFn).toHaveBeenCalled())
    // Allow the microtask queue to fully drain.
    await Promise.resolve()
    await Promise.resolve()

    const loggedRenderFailure = errSpy.mock.calls.some((c) =>
      String(c[0]).includes('[mermaid] render failed'),
    )
    expect(loggedRenderFailure).toBe(false)
    errSpy.mockRestore()
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

  it('SECURITY: initializes mermaid with securityLevel "strict" and suppressErrorRendering true', async () => {
    // The renderer pins securityLevel:'strict' (mermaid's most restrictive: HTML
    // labels off, click/script directives disabled) because this sink renders on the
    // persisted/reload path. A regression to 'loose' would re-enable script execution
    // in agent-authored diagrams — guard the pin explicitly (DOMPurify is a separate,
    // output-side defense; this asserts mermaid's own input-side posture).
    // suppressErrorRendering:true prevents mermaid's "Syntax error" bomb SVGs from
    // accumulating as orphaned DOM nodes — our MermaidErrorCard owns the failure UI.
    renderFn.mockResolvedValue({ svg: '<svg/>' })
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)

    await waitFor(() => expect(initialize).toHaveBeenCalled())
    expect(initialize).toHaveBeenCalledWith(
      expect.objectContaining({ securityLevel: 'strict', suppressErrorRendering: true }),
    )
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

  it('caches a render FAILURE so a remount shows the error card without re-attempting (no loop)', async () => {
    // A malformed diagram in the virtualized list remounts repeatedly. Without a failure
    // cache, each remount re-runs the async render → setError → height change → re-measure
    // → remount: a console-spamming loop. The cache makes the error card synchronous.
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    const first = render(<MermaidDiagram code={'graph T--'} />)
    await waitFor(() => expect(screen.getByText(/Couldn't render the diagram/i)).toBeInTheDocument())
    expect(renderFn).toHaveBeenCalledTimes(1)
    first.unmount()

    // Remount with the SAME malformed code → error card from cache; render NOT re-called.
    render(<MermaidDiagram code={'graph T--'} />)
    expect(screen.getByText(/Couldn't render the diagram/i)).toBeInTheDocument()
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

  // ── Toolbar + toggle tests ────────────────────────────────────────────────────

  it('toolbar: toggles to code view showing Mermaid source, back to image view showing SVG', async () => {
    renderFn.mockResolvedValue({ svg: SVG_FIXTURE })
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)
    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())

    // Code view: click "Show source" toggle
    const showSourceBtn = screen.getByRole('button', { name: 'Show source' })
    fireEvent.click(showSourceBtn)

    // Source code is now visible in a <pre>
    expect(screen.getByText(DIAGRAM)).toBeInTheDocument()
    // SVG diagram is no longer shown
    expect(document.querySelector('svg')).toBeNull()

    // Toggle back: "Show diagram"
    const showDiagramBtn = screen.getByRole('button', { name: 'Show diagram' })
    fireEvent.click(showDiagramBtn)

    // Back to image view — SVG is present, source <pre> is gone
    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())
    // The source text is not in a standalone <pre> (it's in the SVG fixture itself if present,
    // but DIAGRAM is 'graph TD; A-->B' which is not in the SVG fixture)
    expect(screen.queryByText(DIAGRAM)).toBeNull()
  })

  it('toolbar: copy in image view calls svgToPngBlob then copyImageBlob', async () => {
    renderFn.mockResolvedValue({ svg: SVG_FIXTURE })
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)
    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())

    const copyBtn = screen.getByRole('button', { name: 'Copy diagram as PNG' })
    fireEvent.click(copyBtn)

    await waitFor(() => expect(svgToPngBlobMock).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(copyImageBlobMock).toHaveBeenCalledTimes(1))
    expect(copyTextMock).not.toHaveBeenCalled()
  })

  it('toolbar: copy in code view calls copyText with the source code', async () => {
    renderFn.mockResolvedValue({ svg: SVG_FIXTURE })
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={DIAGRAM} />)
    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())

    // Switch to code view
    fireEvent.click(screen.getByRole('button', { name: 'Show source' }))
    expect(screen.getByText(DIAGRAM)).toBeInTheDocument()

    // Copy in code view
    const copyBtn = screen.getByRole('button', { name: 'Copy source' })
    fireEvent.click(copyBtn)

    await waitFor(() => expect(copyTextMock).toHaveBeenCalledWith(DIAGRAM))
    expect(svgToPngBlobMock).not.toHaveBeenCalled()
    expect(copyImageBlobMock).not.toHaveBeenCalled()
  })

  it('toolbar: enlarge opens the global media lightbox with the sanitized svg', async () => {
    renderFn.mockResolvedValue({ svg: SVG_FIXTURE })
    const MermaidDiagram = await importDiagram()
    const { useUiStore } = await import('@/store/ui')
    useUiStore.getState().closeMediaLightbox()

    render(<MermaidDiagram code={DIAGRAM} />)
    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())

    // Enlarge no longer renders a per-row lightbox; it hands off to the single
    // app-root MediaLightbox via the store (immune to virtualized-list remounts).
    expect(useUiStore.getState().mediaLightbox).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Enlarge diagram' }))

    const lb = useUiStore.getState().mediaLightbox
    expect(lb?.kind).toBe('svg')
    expect(lb?.kind === 'svg' && lb.svg).toContain('<svg')
  })

  it('toolbar: code view survives a remount (viewCache) — does NOT snap back to the diagram', async () => {
    // Toggling to source changes the row height → the virtualized list re-measures and
    // REMOUNTS the row. Without the module-level viewCache a plain useState would reset
    // to 'image' and silently revert the view. This is the exact regression that shipped
    // and was fixed by seeding the toggle from viewCache — guard it.
    renderFn.mockResolvedValue({ svg: SVG_FIXTURE })
    const MermaidDiagram = await importDiagram()

    const first = render(<MermaidDiagram code={DIAGRAM} />)
    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())

    // Switch to source view.
    fireEvent.click(screen.getByRole('button', { name: 'Show source' }))
    expect(screen.getByText(DIAGRAM)).toBeInTheDocument()
    first.unmount()

    // Remount with the SAME code → viewCache keeps the 'code' view: source shown
    // immediately, and the toggle now offers switching back to the diagram.
    render(<MermaidDiagram code={DIAGRAM} />)
    expect(screen.getByText(DIAGRAM)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Show diagram' })).toBeInTheDocument()
  })

  it('toolbar: NOT rendered while streaming (container is empty)', async () => {
    renderFn.mockRejectedValue(new Error('Parse error'))
    const MermaidDiagram = await importDiagram()

    const { container } = render(<MermaidDiagram code={'graph T'} streaming />)

    await waitFor(() => expect(renderFn).toHaveBeenCalled())
    // No toolbar rendered while streaming
    expect(container.querySelector('[role="toolbar"]')).toBeNull()
    expect(container).toBeEmptyDOMElement()
  })

  it('toolbar: NOT rendered on the error card path', async () => {
    renderFn.mockRejectedValue(new Error('Parse error on line 1'))
    const MermaidDiagram = await importDiagram()

    render(<MermaidDiagram code={'bad code'} />)
    await waitFor(() => expect(screen.getByText(/Couldn't render the diagram/i)).toBeInTheDocument())

    // The MediaActionToolbar (role="toolbar") must not be present on the error path
    expect(screen.queryByRole('toolbar')).toBeNull()
  })
})
