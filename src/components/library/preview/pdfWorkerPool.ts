// pdfWorkerPool.ts — bounds concurrent PDF.js worker construction page-wide
// (ADR-083 embedded-content spec, EMB-032): "At most two PDF worker instances
// MAY exist page-wide; a lease beyond that MUST show a visible waiting state;
// a worker error MUST reject only the leases held on that worker; and a
// failed worker MUST be terminated and removed so the next lease creates a
// fresh one."
//
// This module does NOT construct, own or share a `Worker` or a `PDFWorker`
// port. `LibraryPdfPreview` still builds its own `new Worker(...)` and hands
// it to `PDFWorker.create({ name, port })` per document instance, exactly as
// it always has (see that file's own header for why: a shared port would
// break the existing `once:true` error race and cross-attribute one
// document's failure to unrelated instances). What this module bounds is
// HOW MANY documents may be under construction — probing assets, fetching
// bytes, constructing a worker and parsing — AT ONCE, queueing the rest with
// a visible waiting state until a slot frees.
//
// A document that fails is expected to release its lease itself, as soon as
// the failure is known (LibraryPdfPreview's `port` error listener), not only
// on unmount — otherwise a poisoned worker would hold its slot for the
// component's whole remaining lifetime and starve the queue. Releasing a
// lease never reuses anything: the NEXT lease granted from that freed slot
// always goes on to construct a genuinely new `Worker` in the component that
// acquired it. This pool hands out permission to use a slot, never a worker
// instance.

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
