// KbMarkdownImage — the knowledge composition's OWN `img` slot
// (UAT D-40 / D-134 / D-101, fan-out round).
//
// Until now the stage-2 knowledge composition inherited chat's image slot
// (MarkdownImage → ChatImage, FR-013d's "never touch the img slot" as it was
// first written). That inheritance carried three defects at once:
//
//   D-40  an INLINE `![[photo.png|400]]` dropped its width hint — ChatImage
//         takes no width, so every inline picture rendered at container
//         width regardless of the author's |N (the standalone case was fixed
//         by KbImageEmbedMount; the inline case never was).
//   D-134 an SVG with no intrinsic size laid out at 0×0 — nothing gave the
//         picture a box that did not depend on intrinsic metrics.
//   D-101 every picture on the page downloaded immediately — ChatImage leans
//         on the browser's native loading="lazy", which UAT D-101 measured
//         as 40 requests at once with 6 in view; the KB's own mount budget
//         (LazyEmbedMount, EMB-065) never applied to plain pictures.
//
// D-101, ROUND 2 (UAT re-test, row U-48): mounting through LazyEmbedMount
// alone was NOT the fix — LazyEmbedMount only gates on VISIBILITY (begin
// work near the viewport), and deliberately has no cap of its own (see
// that module's own "NO HARD CAP" doc). With a 600px mount margin on each
// side, several 300×200px pictures fit inside the combined near-viewport
// window at once; the re-test measured all 40 downloads dispatched at
// once on a realistic scroll-gap fixture. The ceiling on CONCURRENT
// downloads is a separate resource limit from the mount gate — the same
// relationship pdfWorkerPool.ts (EMB-032) has to LazyEmbedMount for PDF
// workers. `kbImageDownloadPool` is that limit for plain pictures: once
// LazyEmbedMount decides a picture should mount, its `<img src>` still
// does not render until a page-wide download slot is granted (at most 4
// at once), held until the picture's own load or error fires.
//
// This component is used ONLY by the knowledge composition
// (knowledgeMarkdownComponents.img). Chat's rendering is unchanged, and so
// is stage 1's (kbMarkdownComponents still maps img to MarkdownImage) —
// neither imports kbImageDownloadPool.
//
// It renders a plain <img> — the KB surface family's own image treatment
// (LibraryImagePreview, KbImageEmbedMount): no lightbox, no chat action
// toolbar, the same dark-safe rounded border, an honest unavailable notice
// on error. Sized like the author asked (D-40), boxed when the picture has
// no intrinsic size (D-134), and mounted through LazyEmbedMount so only
// pictures near the viewport ever attempt to download, throttled through
// kbImageDownloadPool so no more than the ceiling ever download at once
// (D-101) — inline-block, because an inline picture must stay in the
// sentence it was written in.

import { useEffect, useRef, useState } from 'react'
import type { ComponentPropsWithoutRef, SyntheticEvent } from 'react'

import { isDisplayableImageSrc } from '@/lib/url-safe'
import { LazyEmbedMount } from './LazyEmbedMount'
import { kbImageDownloadPool, KB_IMAGE_DOWNLOAD_POOL_CEILING } from './kbImageDownloadPool'

/** Reserved height while an inline picture is outside the mount margin
 *  (EMB-066). Deliberately modest: the author's |N is a WIDTH, not a
 *  predictor of aspect ratio, and guessing tall reserves a bigger reflow
 *  than reserving nothing kind-specific. */
export const KB_INLINE_IMAGE_RESERVED_HEIGHT_PX = 160

/** D-134's bounded box: what an intrinsically-sizeless picture (an SVG with
 *  a viewBox but no width/height) is drawn at when the author gave no width
 *  hint. A viewBox-only SVG scales into this box preserving its own aspect
 *  (the element's default preserveAspectRatio + object-fit: contain), so it
 *  is VISIBLE at a sane size instead of collapsing to 0×0. */
export const KB_INTRINSICITLESS_FALLBACK_WIDTH_PX = 320
export const KB_INTRINSICITLESS_FALLBACK_HEIGHT_PX = 240

/** react-markdown hands the mdast node's data-hProperties to the component
 *  as verbatim data-* props — this is how a sized `![[x|N]]` picture's N,
 *  which remarkKbWikilinks attached in the AST (EMB-030), reaches the img
 *  slot that finally renders it. */
type KbMarkdownImageProps = ComponentPropsWithoutRef<'img'> & {
  'data-kb-embed-width'?: string
}

function parseWidthHint(raw: string | undefined): number | undefined {
  if (raw === undefined) return undefined
  const n = Number.parseInt(raw, 10)
  return Number.isFinite(n) && n > 0 ? n : undefined
}

export function KbMarkdownImage(props: KbMarkdownImageProps) {
  const { src, alt, 'data-kb-embed-width': widthProp, ...rest } = props

  // An unsafe scheme never becomes an <img> at all — the same render-boundary
  // gate ChatImage applies (defense in depth alongside the URL sanitiser).
  if (src !== undefined && !isDisplayableImageSrc(src)) {
    return alt ? <span className="text-xs italic text-[var(--color-muted)]">[image: {alt}]</span> : null
  }

  return (
    <LazyEmbedMount
      reservedHeight={KB_INLINE_IMAGE_RESERVED_HEIGHT_PX}
      className="inline-block max-w-full align-baseline"
    >
      <KbMarkdownImageContent src={src ?? ''} alt={alt} widthHint={parseWidthHint(widthProp)} {...rest} />
    </LazyEmbedMount>
  )
}

function KbMarkdownImageContent({
  src,
  alt,
  widthHint,
  ...rest
}: Omit<KbMarkdownImageProps, 'data-kb-embed-width'> & { widthHint?: number }) {
  const [failed, setFailed] = useState(false)
  const [intrinsicless, setIntrinsicless] = useState(false)
  // D-101 (round 2, U-48): the download itself does not begin the instant
  // LazyEmbedMount mounts this component — it waits for a page-wide slot
  // from kbImageDownloadPool. `false` until granted; the `<img>` below is
  // not rendered at all until then, so no request is dispatched while
  // queued.
  const [downloadGranted, setDownloadGranted] = useState(false)
  // Holds the CURRENT lease's release() once granted, so the load/error
  // handlers (defined outside the acquiring effect) and the effect's own
  // unmount cleanup can both free the same slot — release() is idempotent,
  // so whichever fires first is harmless.
  const leaseReleaseRef = useRef<(() => void) | null>(null)

  // A src change resets every per-picture state, including the download
  // slot — the same keyed-on-src convention ChatImage documents for its
  // own error card, extended to the new download-gate state.
  useEffect(() => {
    setFailed(false)
    setIntrinsicless(false)
    setDownloadGranted(false)
    leaseReleaseRef.current = null

    const controller = new AbortController()
    let cancelledBeforeGrant = false
    kbImageDownloadPool
      .acquire(controller.signal)
      .then((lease) => {
        if (cancelledBeforeGrant) {
          // Unmounted (scrolled away) between the acquire call and the
          // grant resolving — never hold a slot nothing will use.
          lease.release()
          return
        }
        leaseReleaseRef.current = lease.release
        setDownloadGranted(true)
      })
      .catch(() => {
        // Scrolled away while still queued (EMB-065's "stops when well
        // outside", applied to a still-queued lease) — no download was ever
        // started, so there is nothing to release.
      })

    return () => {
      cancelledBeforeGrant = true
      controller.abort()
      // Also releases a lease that WAS already granted but never reached
      // load/error before this picture unmounted (scrolled away while
      // actively downloading) — idempotent with the load/error handlers'
      // own release below.
      leaseReleaseRef.current?.()
      leaseReleaseRef.current = null
    }
  }, [src])

  if (failed) {
    return (
      <span
        data-testid="kb-markdown-image-unavailable"
        className="text-xs italic leading-snug text-[var(--color-muted)]"
      >
        {alt !== undefined && alt !== '' ? `“${alt}” could not be loaded.` : 'This image could not be loaded.'}
      </span>
    )
  }

  if (!downloadGranted) {
    // EMB-067's "visible waiting state", applied to the image download
    // ceiling the same way LibraryPdfPreview's own `library-pdf-queued`
    // state names PDF_WORKER_POOL_CEILING — never a silent wait.
    return (
      <span
        data-testid="kb-markdown-image-queued"
        className="inline-flex items-center text-xs italic leading-snug text-[var(--color-muted)]"
        style={{ minHeight: KB_INLINE_IMAGE_RESERVED_HEIGHT_PX }}
      >
        Only {KB_IMAGE_DOWNLOAD_POOL_CEILING} images download at once on this page. This one will
        start once another finishes or scrolls out of view.
      </span>
    )
  }

  // D-40: the author's width, capped by the container. D-134: once the load
  // reports NO intrinsic size, the box stops depending on intrinsic metrics
  // entirely — an explicit bounded box, object-fit contained.
  const style: Record<string, string | number> = {}
  if (widthHint !== undefined) {
    style.width = `${widthHint}px`
    style.maxWidth = '100%'
  }
  if (intrinsicless) {
    style.width = `${widthHint ?? KB_INTRINSICITLESS_FALLBACK_WIDTH_PX}px`
    style.height = `${KB_INTRINSICITLESS_FALLBACK_HEIGHT_PX}px`
    style.objectFit = 'contain'
    style.maxWidth = '100%'
  }

  const onLoad = (event: SyntheticEvent<HTMLImageElement>) => {
    // jsdom fires no load events and decodes nothing; real browsers report
    // 0 natural dimensions for a picture with none (the viewBox-only SVG of
    // UAT D-134, which still loaded fine and simply laid out at nothing).
    if (event.currentTarget.naturalWidth === 0 || event.currentTarget.naturalHeight === 0) {
      setIntrinsicless(true)
    }
    // The download is DONE (success) — free the slot for the next queued
    // picture. Idempotent, so a later unmount's own release is harmless.
    leaseReleaseRef.current?.()
    leaseReleaseRef.current = null
  }

  return (
    <img
      src={src}
      alt={alt ?? ''}
      data-testid="kb-markdown-image"
      onLoad={onLoad}
      onError={() => {
        setFailed(true)
        // The download is DONE (failure) — a failed picture must not hold
        // its slot forever; releasing here is what keeps a bad image from
        // wedging the rest of the page behind the ceiling.
        leaseReleaseRef.current?.()
        leaseReleaseRef.current = null
      }}
      style={Object.keys(style).length > 0 ? style : undefined}
      className="max-w-full rounded-md border border-[var(--color-border)]"
      {...rest}
    />
  )
}
