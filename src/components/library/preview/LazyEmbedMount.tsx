// LazyEmbedMount — the mount budget primitive for inline embeds inside a
// knowledge-base note (ADR-083 embedded-content spec, EMB-065/066, US-8):
// "An embed MUST begin work when it enters the viewport plus a margin and
// MUST stop when it is well outside; there MUST be no limit on how many
// embeds a note may contain." / "The system MUST reserve a per-kind height
// before mounting; ... one reflow is accepted."
//
// This is a GENERIC wrapper, not tied to any one renderer — every kind's
// inline embed (image, PDF, base view, transclusion, and whatever a later
// step adds) mounts through the same component, so "only visible embeds do
// work" is one property proven once rather than re-implemented per kind.
//
// WHY TWO OBSERVERS, NOT ONE. A single boundary would mount and unmount on
// every frame for an embed sitting exactly at that line as the reader
// scrolls past it. The near boundary (`mountMarginPx`) decides when work
// BEGINS; the far boundary (`unmountMarginPx`, deliberately larger) decides
// when it STOPS — the band between the two is where an already-mounted
// embed stays mounted and an already-unmounted one stays unmounted, which is
// what "stops when WELL outside" (not "the instant it leaves the viewport")
// means.
//
// NO HARD CAP (EMB-065's second clause): this component makes no attempt to
// count how many siblings exist or are mounted — every instance decides for
// itself from its own intersection state. A note with a thousand embeds
// creates a thousand independent observers; only the ones near the viewport
// ever mount their children. (A renderer with its own separate concurrency
// ceiling — LibraryPdfPreview's worker pool, EMB-032 — still applies once
// mounted; that is a different, per-kind resource limit, not a mount cap.)
//
// Reserved height (EMB-066): the wrapper always occupies `reservedHeight`
// while unmounted, so scrolling past a not-yet-mounted embed does not shift
// the page under the reader. Once mounted, the wrapper stops constraining
// height at all — the child is free to reflow to its real size (the "one
// reflow is accepted" the requirement names).

import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'

/** How far outside the viewport, in pixels, mounting begins. */
export const DEFAULT_MOUNT_MARGIN_PX = 600

/** How far outside the viewport, in pixels, a mounted embed gives up its
 *  place. Deliberately larger than the mount margin — see the module doc's
 *  "why two observers" note. */
export const DEFAULT_UNMOUNT_MARGIN_PX = 1800

export interface LazyEmbedMountProps {
  /** Reserved height, in pixels, shown before this embed has ever mounted
   *  and again once it has unmounted (EMB-066). Per-kind: callers pass a
   *  size representative of what THAT kind renders (e.g. a transclusion's
   *  fixed three lines) — this component has no opinion on the number. */
  reservedHeight: number
  /** Renders only while this embed is mounted. Receives nothing — the
   *  renderer already has everything it needs from its own props; this
   *  wrapper's only job is deciding WHEN to render it. */
  children: ReactNode
  mountMarginPx?: number
  unmountMarginPx?: number
  /** Surfaced for the page-level "find-in-page and printing will not reach
   *  unmounted embeds" notice (EMB-071) — that notice counts mounted vs.
   *  total embeds across the whole note, which lives above this component,
   *  not inside it. Optional; omit if the caller does not need to know. */
  onMountedChange?: (mounted: boolean) => void
  className?: string
}

/**
 * Mounts `children` once this element enters the viewport plus
 * `mountMarginPx`, and unmounts them once it is further than
 * `unmountMarginPx` outside — with no limit on how many `LazyEmbedMount`
 * instances a page may contain (EMB-065).
 */
export function LazyEmbedMount({
  reservedHeight,
  children,
  mountMarginPx = DEFAULT_MOUNT_MARGIN_PX,
  unmountMarginPx = DEFAULT_UNMOUNT_MARGIN_PX,
  onMountedChange,
  className,
}: LazyEmbedMountProps) {
  const ref = useRef<HTMLDivElement | null>(null)
  // Fails OPEN: a browser with no IntersectionObserver mounts immediately
  // rather than never mounting at all — the worse of the two failure modes,
  // since "always does the work" degrades to today's behaviour while "never
  // does the work" would make every embed permanently blank.
  const [mounted, setMounted] = useState(() => typeof IntersectionObserver === 'undefined')

  useEffect(() => {
    if (typeof IntersectionObserver === 'undefined') return
    const el = ref.current
    if (!el) return

    const mountObserver = new IntersectionObserver(
      (entries) => {
        if (entries[entries.length - 1]?.isIntersecting) setMounted(true)
      },
      { rootMargin: `${mountMarginPx}px` },
    )
    const unmountObserver = new IntersectionObserver(
      (entries) => {
        const entry = entries[entries.length - 1]
        if (entry && !entry.isIntersecting) setMounted(false)
      },
      { rootMargin: `${unmountMarginPx}px` },
    )
    mountObserver.observe(el)
    unmountObserver.observe(el)
    return () => {
      mountObserver.disconnect()
      unmountObserver.disconnect()
    }
  }, [mountMarginPx, unmountMarginPx])

  useEffect(() => {
    onMountedChange?.(mounted)
  }, [mounted, onMountedChange])

  return (
    <div
      ref={ref}
      data-testid="lazy-embed-mount"
      data-mounted={mounted}
      className={className}
      style={mounted ? undefined : { minHeight: reservedHeight }}
    >
      {mounted ? children : null}
    </div>
  )
}
