// pdfWorkerPool.ts — bounds how many PDF.js worker INSTANCES may exist
// page-wide (ADR-083 embedded-content spec, EMB-032): "At most two PDF
// worker instances MAY exist page-wide; a lease beyond that MUST show a
// visible waiting state; a worker error MUST reject only the leases held on
// that worker; and a failed worker MUST be terminated and removed so the
// next lease creates a fresh one."
//
// This module does NOT construct, own or share a `Worker` or a `PDFWorker`
// port. `LibraryPdfPreview` still builds its own `new Worker(...)` and hands
// it to `PDFWorker.create({ name, port })` per document instance, exactly as
// it always has (see that file's own header for why: a shared port would
// break the existing `once:true` error race and cross-attribute one
// document's failure to unrelated instances). What this module bounds is HOW
// MANY documents may hold a live `Worker` AT ONCE — for as long as that
// document needs it, which is its WHOLE MOUNTED LIFETIME, not merely the
// probing/fetching/constructing/parsing that happens before its first page
// is on screen.
//
// A document whose first render pass has finished is NOT "done with" its
// worker in any sense that would make releasing the slot safe: Edit mode
// mounts a real `AnnotationLayer` against the SAME `doc`, and `handleSave`
// calls `doc.saveDocument()` — both keep talking to this worker for as long
// as the component stays mounted. Freeing the slot the moment the first
// render finishes would do one of two wrong things: kill a worker Edit mode
// or Save still needs, or let a third real `Worker` exist alongside two
// "released but not actually torn down" ones — either way breaking EMB-032's
// own ceiling, which counts INSTANCES, not construction-in-progress. So a
// lease is deliberately held past "render succeeded" — there is no
// release-on-render-success path, and that is not an oversight (see
// LibraryPdfPreview.tsx's own comment at the point its load effect finishes
// its first pass over every page).
//
// A document that fails is the one case where holding the slot for "the
// component's whole remaining lifetime" would be wrong: nothing further will
// ever come of a poisoned worker, so it is expected to release its lease
// itself, as soon as the failure is known (LibraryPdfPreview's `port` error
// listener), rather than starving the queue until eventual unmount.
// Releasing a lease never reuses anything: the NEXT lease granted from that
// freed slot always goes on to construct a genuinely new `Worker` in the
// component that acquired it. This pool hands out permission to use a slot,
// never a worker instance.

/** EMB-032's number, named so a change to the ceiling is a one-line edit
 *  with a citation, not a magic `2` a reader has to trust. */
export const PDF_WORKER_POOL_CEILING = 2

export interface PdfWorkerLease {
  /** Frees this lease's slot, letting the next queued acquire (if any)
   *  proceed. Idempotent — safe to call once from a worker-error handler and
   *  again from the component's unmount cleanup. */
  release(): void
}

interface QueueEntry {
  grant: () => void
  onAbort: () => void
}

function toAbortError(signal: AbortSignal): Error {
  const reason = (signal as AbortSignal & { reason?: unknown }).reason
  return reason instanceof Error ? reason : new DOMException('Aborted', 'AbortError')
}

class PdfWorkerPool {
  private activeCount = 0
  private queue: QueueEntry[] = []

  /**
   * Resolves once a slot is available. If `signal` aborts first (the
   * component unmounted, or was scrolled far enough out of view to give up
   * its queued place — EMB-065's "stops when well outside", applied to a
   * still-queued lease), the request leaves the queue and the promise
   * rejects with an AbortError — the same cancellation contract `fetch`
   * already uses, so a caller that already handles that shape (every
   * `LibraryPdfPreview` load effect does) needs no second error path.
   *
   * `onQueued` fires SYNCHRONOUSLY, and only when the request could not be
   * granted immediately — never on the common, under-ceiling path — so a
   * caller can show a waiting state precisely when the ceiling is the
   * reason it is waiting, and never flash one when it is not.
   */
  acquire(signal: AbortSignal, onQueued?: () => void): Promise<PdfWorkerLease> {
    return new Promise((resolve, reject) => {
      if (signal.aborted) {
        reject(toAbortError(signal))
        return
      }

      const grant = () => {
        this.activeCount++
        signal.removeEventListener('abort', onAbort)
        let released = false
        resolve({
          release: () => {
            if (released) return
            released = true
            this.activeCount--
            this.grantNext()
          },
        })
      }

      const onAbort = () => {
        this.queue = this.queue.filter((entry) => entry.grant !== grant)
        reject(toAbortError(signal))
      }

      if (this.activeCount < PDF_WORKER_POOL_CEILING) {
        grant()
        return
      }
      onQueued?.()
      this.queue.push({ grant, onAbort })
      signal.addEventListener('abort', onAbort)
    })
  }

  private grantNext(): void {
    const next = this.queue.shift()
    next?.grant()
  }

  /** Test-only introspection — never read by production code. */
  get activeLeaseCountForTests(): number {
    return this.activeCount
  }

  get queuedCountForTests(): number {
    return this.queue.length
  }
}

/** One pool, page-wide — matching EMB-032's "at most two PDF worker instances
 *  MAY exist page-wide" regardless of how many `LibraryPdfPreview` instances
 *  the page has mounted (the pane's own single instance included). */
export const pdfWorkerPool = new PdfWorkerPool()
