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
// This component is used ONLY by the knowledge composition
// (knowledgeMarkdownComponents.img). Chat's rendering is unchanged, and so
// is stage 1's (kbMarkdownComponents still maps img to MarkdownImage).
//
// It renders a plain <img> — the KB surface family's own image treatment
// (LibraryImagePreview, KbImageEmbedMount): no lightbox, no chat action
// toolbar, the same dark-safe rounded border, an honest unavailable notice
// on error. Sized like the author asked (D-40), boxed when the picture has
// no intrinsic size (D-134), and mounted through LazyEmbedMount so only
// pictures near the viewport ever download (D-101) — inline-block, because
// an inline picture must stay in the sentence it was written in.

import { useEffect, useState } from 'react'
import type { ComponentPropsWithoutRef, SyntheticEvent } from 'react'

import { isDisplayableImageSrc } from '@/lib/url-safe'
import { LazyEmbedMount } from './LazyEmbedMount'

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

  // A src change resets both states — the same keyed-on-src convention
  // ChatImage documents for its own error card.
  useEffect(() => {
    setFailed(false)
    setIntrinsicless(false)
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
  }

  return (
    <img
      src={src}
      alt={alt ?? ''}
      data-testid="kb-markdown-image"
      onLoad={onLoad}
      onError={() => setFailed(true)}
      style={Object.keys(style).length > 0 ? style : undefined}
      className="max-w-full rounded-md border border-[var(--color-border)]"
      {...rest}
    />
  )
}
