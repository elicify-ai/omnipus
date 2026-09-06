// LibrarySignaturePad — freehand drawn-signature capture for the Library PDF
// editor (library-b-c-design-2026-09-07.md "B — PDF form-fill and drawn
// signature": "draw a signature (freehand) placed as an /Ink annotation on
// the page"). A drawn mark of intent, never a cryptographic signature — B4
// (PKI signing) is explicitly out of scope, and this dialog says so.
//
// Deliberately PDF.js-free: this component knows nothing about PDF-space
// coordinates, annotationStorage, or pdfjs-dist. It captures strokes in its
// own fixed drawing-surface pixel space (device-independent CSS pixels,
// top-left origin) and hands them back via `onInsert`, along with the
// 1-based page number the caller chose to place them on.
// LibraryPdfPreview.tsx owns turning those into PDF-space points (via that
// page's own `PageViewport.convertToPdfPoint`) and the actual
// `annotationStorage.setValue` write — see pdfInkAnnotation.ts for why that
// write is hand-built rather than routed through PDF.js's InkEditor.

import { useEffect, useRef, useState } from 'react'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import type { SignaturePoint, SignatureStroke } from './pdfInkAnnotation'

/** Fixed drawing-surface size, CSS pixels. Independent of any page's own
 * on-screen size or zoom level — a bigger, steadier area to draw in than a
 * cramped in-place overlay would give, and it keeps the PDF-space placement
 * math in LibraryPdfPreview.tsx to one clean scale factor per axis. */
export const SIGNATURE_PAD_WIDTH = 480
export const SIGNATURE_PAD_HEIGHT = 160

const STROKE_WIDTH_CSS_PX = 2.5
const MAX_BACKING_STORE_RATIO = 2

export interface LibrarySignaturePadProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  pageCount: number
  defaultPageNumber: number
  /** Strokes are in THIS component's own pad pixel space
   * (SIGNATURE_PAD_WIDTH x SIGNATURE_PAD_HEIGHT, CSS px, top-left origin) —
   * never PDF space. `pageNumber` is 1-based, clamped to [1, pageCount]. */
  onInsert: (strokes: SignatureStroke[], pageNumber: number) => void
}

export function LibrarySignaturePad({
  open,
  onOpenChange,
  pageCount,
  defaultPageNumber,
  onInsert,
}: LibrarySignaturePadProps) {
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  const drawingRef = useRef(false)
  const strokesRef = useRef<SignatureStroke[]>([])
  const [hasInk, setHasInk] = useState(false)
  const clampedPageCount = Math.max(pageCount, 1)
  const [pageNumber, setPageNumber] = useState(() =>
    Math.min(Math.max(defaultPageNumber, 1), clampedPageCount),
  )

  const clearCanvas = () => {
    const canvas = canvasRef.current
    const ctx = canvas?.getContext('2d')
    if (ctx && canvas) ctx.clearRect(0, 0, canvas.width, canvas.height)
  }

  // Fresh pad every time the dialog opens — a stale drawing from a
  // previously-cancelled attempt must never survive to a later "Place
  // signature".
  useEffect(() => {
    if (!open) return
    strokesRef.current = []
    drawingRef.current = false
    setHasInk(false)
    setPageNumber(Math.min(Math.max(defaultPageNumber, 1), clampedPageCount))
    clearCanvas()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, defaultPageNumber, pageCount])

  function configureCanvas(canvas: HTMLCanvasElement) {
    const ratio = Math.min(window.devicePixelRatio || 1, MAX_BACKING_STORE_RATIO)
    const targetWidth = Math.floor(SIGNATURE_PAD_WIDTH * ratio)
    const targetHeight = Math.floor(SIGNATURE_PAD_HEIGHT * ratio)
    // Re-configuring an already-sized canvas clears it and resets the 2D
    // context's transform — only do it once per element.
    if (canvas.width === targetWidth && canvas.height === targetHeight) return
    canvas.width = targetWidth
    canvas.height = targetHeight
    const ctx = canvas.getContext('2d')
    if (!ctx) return
    ctx.scale(ratio, ratio)
    ctx.lineCap = 'round'
    ctx.lineJoin = 'round'
    ctx.lineWidth = STROKE_WIDTH_CSS_PX
    ctx.strokeStyle = '#111111'
  }

  function pointFromEvent(e: React.PointerEvent<HTMLCanvasElement>): SignaturePoint {
    const rect = e.currentTarget.getBoundingClientRect()
    return { x: e.clientX - rect.left, y: e.clientY - rect.top }
  }

  function handlePointerDown(e: React.PointerEvent<HTMLCanvasElement>) {
    e.currentTarget.setPointerCapture(e.pointerId)
    drawingRef.current = true
    strokesRef.current = [...strokesRef.current, [pointFromEvent(e)]]
    setHasInk(true)
  }

  function handlePointerMove(e: React.PointerEvent<HTMLCanvasElement>) {
    if (!drawingRef.current) return
    const point = pointFromEvent(e)
    const strokes = strokesRef.current
    const current = strokes[strokes.length - 1]
    if (!current) return
    const previous = current[current.length - 1]
    current.push(point)
    const ctx = canvasRef.current?.getContext('2d')
    if (ctx && previous) {
      ctx.beginPath()
      ctx.moveTo(previous.x, previous.y)
      ctx.lineTo(point.x, point.y)
      ctx.stroke()
    }
  }

  function endStroke(e: React.PointerEvent<HTMLCanvasElement>) {
    if (e.currentTarget.hasPointerCapture(e.pointerId)) {
      e.currentTarget.releasePointerCapture(e.pointerId)
    }
    drawingRef.current = false
  }

  function handleClear() {
    strokesRef.current = []
    setHasInk(false)
    clearCanvas()
  }

  function handlePageNumberChange(raw: string) {
    const parsed = Number(raw)
    if (!Number.isFinite(parsed)) return
    setPageNumber(Math.min(Math.max(Math.round(parsed), 1), clampedPageCount))
  }

  function handleInsert() {
    const strokes = strokesRef.current.filter((stroke) => stroke.length > 0)
    if (strokes.length === 0) return
    onInsert(strokes, pageNumber)
    onOpenChange(false)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent data-testid="library-pdf-signature-dialog" className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Draw a signature</DialogTitle>
          <DialogDescription>
            Draw with a mouse, stylus or finger, choose a page, then place it. This is a drawn mark
            of intent — not a cryptographic signature.
          </DialogDescription>
        </DialogHeader>

        <canvas
          ref={(node) => {
            canvasRef.current = node
            if (node) configureCanvas(node)
          }}
          style={{ width: SIGNATURE_PAD_WIDTH, height: SIGNATURE_PAD_HEIGHT, touchAction: 'none' }}
          className="mx-auto cursor-crosshair rounded-md border border-[var(--color-border)] bg-white"
          data-testid="library-pdf-signature-canvas"
          role="img"
          aria-label="Signature drawing area"
          onPointerDown={handlePointerDown}
          onPointerMove={handlePointerMove}
          onPointerUp={endStroke}
          onPointerCancel={endStroke}
        />

        <div className="flex items-center gap-3">
          <Label htmlFor="library-pdf-signature-page" className="shrink-0 text-xs">
            Place on page
          </Label>
          <Input
            id="library-pdf-signature-page"
            data-testid="library-pdf-signature-page"
            type="number"
            min={1}
            max={clampedPageCount}
            value={pageNumber}
            onChange={(e) => handlePageNumberChange(e.target.value)}
            className="h-8 w-20"
          />
          <span className="text-xs text-[var(--color-muted)]">of {clampedPageCount}</span>
        </div>

        <DialogFooter>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleClear}
            disabled={!hasInk}
            data-testid="library-pdf-signature-clear"
          >
            Clear
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => onOpenChange(false)}
            data-testid="library-pdf-signature-cancel"
          >
            Cancel
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={handleInsert}
            disabled={!hasInk}
            data-testid="library-pdf-signature-insert"
          >
            Place signature
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
