// MermaidDiagram — lazy-loads mermaid.js and renders Mermaid code as SVG.
// Dark-themed: transparent background, Liquid Silver text, Forge Gold lines.

import { useEffect, useRef, useState, memo } from 'react'
import DOMPurify from 'dompurify'

interface MermaidDiagramProps {
  code: string
}

// Module-level flag to avoid re-initialising mermaid on every render.
let initialized = false
let initFailed = false

// Cache of rendered SVG keyed by diagram source. A finalized chat message can
// remount (the virtualized message list re-renders, dropping and recreating this
// component); without this, every remount would re-run the async mermaid render
// and flash "Rendering diagram..." each time. Process-lifetime; in practice
// bounded by the number of distinct diagrams in a session.
const renderedSvgCache = new Map<string, string>()

async function getMermaid() {
  const m = (await import('mermaid')).default
  // If a previous init failed, reset both flags to allow a retry on the next render
  if (initFailed) {
    initialized = false
    initFailed = false
  }
  if (!initialized) {
    try {
      m.initialize({
        startOnLoad: false,
        // Pinned explicitly: this sink renders on the persisted/reload path
        // (historical messages), so the safe posture must be asserted in-repo
        // rather than inherited from mermaid's ever-changing defaults.
        securityLevel: 'strict',
        theme: 'dark',
        themeVariables: {
          background: 'transparent',
          primaryTextColor: '#E2E8F0',
          lineColor: '#D4AF37',
          primaryColor: '#1a1a2e',
          secondaryColor: '#16213e',
          tertiaryColor: '#0f3460',
          edgeLabelBackground: '#0A0A0B',
          clusterBkg: '#0A0A0B',
          titleColor: '#E2E8F0',
          nodeBorder: '#D4AF37',
          mainBkg: '#1a1a2e',
          fontFamily: 'Inter, sans-serif',
        },
      })
      initialized = true
    } catch (err) {
      console.error('[mermaid] Initialization failed:', err instanceof Error ? err.message : err)
      initFailed = true
      initialized = true
    }
  }
  return m
}

function MermaidDiagramImpl({ code }: MermaidDiagramProps) {
  // Seed from the cache so a remount paints the already-rendered SVG synchronously
  // (no loading flash, no redundant render) — see renderedSvgCache above.
  const [svg, setSvg] = useState<string | null>(() => renderedSvgCache.get(code) ?? null)
  const [error, setError] = useState<string | null>(null)
  const idRef = useRef(`mermaid-${Math.random().toString(36).slice(2)}`)

  useEffect(() => {
    // Cache hit: show the stored SVG and skip the async render entirely.
    const cached = renderedSvgCache.get(code)
    if (cached) {
      setSvg(cached)
      setError(null)
      return
    }

    let cancelled = false

    async function render() {
      try {
        const m = await getMermaid()
        if (initFailed) {
          if (!cancelled) setError('Mermaid initialization failed')
          return
        }
        const { svg: rendered } = await m.render(idRef.current, code.trim())
        renderedSvgCache.set(code, rendered)
        if (!cancelled) setSvg(rendered)
      } catch (e) {
        if (!cancelled) {
          const msg = e instanceof Error ? e.message : String(e)
          setError(msg)
        }
      }
    }

    setSvg(null)
    setError(null)
    render()
    return () => {
      cancelled = true
    }
  }, [code])

  if (error) {
    // Show the mermaid source as a code block with the error as a small note
    return (
      <pre className="my-2 rounded-lg bg-[var(--color-surface-2)] border border-[var(--color-border)] p-4 text-xs font-mono text-[var(--color-secondary)] overflow-x-auto whitespace-pre-wrap">
        <div className="text-[10px] text-[var(--color-error)] mb-2 font-sans">{error}</div>
        {code}
      </pre>
    )
  }

  if (!svg) {
    return (
      <div className="my-2 flex items-center gap-2 text-xs text-[var(--color-muted)] px-1">
        <span className="inline-block w-1.5 h-1.5 rounded-full bg-[var(--color-accent)] animate-pulse" />
        Rendering diagram...
      </div>
    )
  }

  return (
    <div
      className="my-3 flex justify-center overflow-x-auto rounded-lg bg-[var(--color-surface-2)] border border-[var(--color-border)] p-4"
      dangerouslySetInnerHTML={{
        __html: DOMPurify.sanitize(svg, { USE_PROFILES: { svg: true }, ADD_TAGS: ['foreignObject'] }),
      }}
    />
  )
}

export const MermaidDiagram = memo(MermaidDiagramImpl)
