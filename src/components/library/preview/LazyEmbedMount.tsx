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
//
// M2 / EMB-071 (ADR-083 spec ~line 513/1990, ADR ~line 671) — "While any
// embed on the page is unmounted, the reader shows one line stating that
// find-in-page and printing will not reach modules that have not been
// scrolled to. The notice disappears once every embed on the page is
// mounted, and never appears on a note with no embeds." N3 DROPPED print
// support for a lazily-mounted note entirely (`beforeprint` cannot await
// async work); this notice is the compensating control that ruling depends
// on — without it a reader prints or Ctrl+F's a dashboard and silently gets
// a PARTIAL SUBSET with nothing saying so.
//
// SELF-REGISTERING, not caller-aggregated. `onMountedChange` above already
// lets a caller aggregate mount state itself (its own doc comment says so:
// "that notice... lives above this component, not inside it"), but nothing
// has ever wired it — this module additionally tracks every currently-
// mounted `LazyEmbedMount` INSTANCE (not just its content-mounted bit) in a
// page-wide registry, so `UnmountedEmbedsNotice` below works the moment it
// is rendered anywhere on the page, with zero per-instance wiring. The two
// mechanisms coexist: a caller that still wants its own `onMountedChange`
// callback keeps getting it, unaffected.
import { useEffect, useId, useRef, useState, useSyncExternalStore } from 'react'
import type { ReactNode } from 'react'
import { Info } from '@phosphor-icons/react'

/** id -> is this instance's content currently mounted. Module-level (one
 *  registry, page-wide) because the notice this exists to drive is itself
 *  page-wide ("While any embed on the page is unmounted") — not scoped to
 *  one note's own component subtree. */
const embedMountRegistry = new Map<string, boolean>()
const embedMountRegistryListeners = new Set<() => void>()

function notifyEmbedMountRegistryListeners(): void {
  for (const listener of embedMountRegistryListeners) listener()
}

function subscribeToEmbedMountRegistry(listener: () => void): () => void {
  embedMountRegistryListeners.add(listener)
  return () => embedMountRegistryListeners.delete(listener)
}

/** False when the registry is empty (EMB-071's "never appears on a note
 *  with no embeds") or when every registered instance is mounted; true the
 *  moment at least one is not. A primitive return keeps this a stable
 *  `useSyncExternalStore` snapshot — no unnecessary re-render from a
 *  same-value recompute. */
function getAnyEmbedUnmountedSnapshot(): boolean {
  for (const mounted of embedMountRegistry.values()) {
    if (!mounted) return true
  }
  return false
}

/** True while at least one currently-mounted `LazyEmbedMount` instance has
 *  NOT mounted its content — see the module doc's M2/EMB-071 section. Test
 *  seam: exported so a test can assert the registry itself independent of
 *  `UnmountedEmbedsNotice`'s own rendered text. */
export function useAnyEmbedUnmounted(): boolean {
  return useSyncExternalStore(subscribeToEmbedMountRegistry, getAnyEmbedUnmountedSnapshot, getAnyEmbedUnmountedSnapshot)
}

/** EMB-071's own line, verbatim to the spec's two named limitations
 *  (find-in-page AND printing — N3 folded printing into what was originally
 *  a find-in-page-only statement). */
export const UNMOUNTED_EMBEDS_NOTICE_TEXT =
  'Find-in-page and printing will not reach content that has not been scrolled into view yet.'

/**
 * Drop this anywhere on a page that renders `LazyEmbedMount` instances (no
 * props, no wiring) — it renders EMB-071's one line while at least one of
 * them is unmounted, and nothing while every embed on the page is mounted or
 * the page has no embeds at all.
 */
export function UnmountedEmbedsNotice() {
  const anyUnmounted = useAnyEmbedUnmounted()
  if (!anyUnmounted) return null
  return (
    <div
      data-testid="unmounted-embeds-notice"
      className="flex shrink-0 items-center gap-1.5 border-b border-[var(--color-border)] bg-[var(--color-surface-1)] px-3 py-1.5 text-[11px] text-[var(--color-muted)]"
    >
      <Info size={13} />
      {UNMOUNTED_EMBEDS_NOTICE_TEXT}
    </div>
  )
}

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
  /** Per-instance notification of this embed's own mount transitions. NOT
   *  what drives EMB-071's page-wide "find-in-page and printing will not
   *  reach unmounted embeds" notice — `UnmountedEmbedsNotice` (this module's
   *  own export) gets that from every instance's SELF-registration, with no
   *  wiring required. This prop exists for a caller that wants its own,
   *  separate per-instance signal (e.g. instance-scoped analytics or UI);
   *  optional, omit if the caller does not need one. */
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

  // Latest-ref pattern, deliberately NOT `[mounted, onMountedChange]`. A
  // caller's `onMountedChange` is expected to setState from it (that is the
  // whole point — EMB-071's mount-count notice reads it) but is very likely
  // to be an unmemoized inline function, a fresh identity every render. If
  // this effect depended on that identity directly, an inline callback would
  // re-run it on EVERY parent render, not just on a real mount/unmount
  // transition; calling a callback that itself setStates would then trigger
  // another parent render, producing ANOTHER new identity, re-running the
  // effect again — an infinite loop with no caller having done anything
  // wrong by the type's own contract. Reading the CURRENT callback out of a
  // ref, and keying the notifying effect on `mounted` alone, reports every
  // real transition exactly once regardless of whether the caller memoizes.
  const onMountedChangeRef = useRef(onMountedChange)
  useEffect(() => {
    onMountedChangeRef.current = onMountedChange
  })
  useEffect(() => {
    onMountedChangeRef.current?.(mounted)
  }, [mounted])

  // M2 / EMB-071 — self-registration into the page-wide registry
  // `UnmountedEmbedsNotice` reads (module doc above). `instanceId` is stable
  // for this component instance's whole lifetime (`useId`), so it is safe to
  // both register AND unregister under the same key.
  //
  // Split into two effects, DELIBERATELY not combined into one keyed on
  // `[instanceId, mounted]`: a single combined effect's cleanup re-runs on
  // EVERY `mounted` transition (not just true unmount), which would delete
  // then immediately re-add this instance's registry entry on every mount ⇄
  // unmount flip — two extra listener notifications per transition, and a
  // window (between the delete and the re-add, both inside the same effect
  // flush) where `getAnyEmbedUnmountedSnapshot` could transiently disagree
  // with the real state. Keeping "update the value" and "remove the entry"
  // as separate effects with different dependency lists means the entry is
  // removed exactly once, only on the real unmount.
  const instanceId = useId()
  useEffect(() => {
    embedMountRegistry.set(instanceId, mounted)
    notifyEmbedMountRegistryListeners()
  }, [instanceId, mounted])
  useEffect(() => {
    return () => {
      embedMountRegistry.delete(instanceId)
      notifyEmbedMountRegistryListeners()
    }
  }, [instanceId])

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
