// kbImageDownloadPool.ts — bounds how many plain-picture DOWNLOADS the
// knowledge composition may have in flight page-wide at once (UAT D-101,
// row U-48: "a knowledge note with 40 embedded images has at most 4 image
// downloads in flight at once, all images eventually mount").
//
// THIS IS THE SAME PRIMITIVE AS pdfWorkerPool.ts (EMB-032) and
// viewEvaluationPool.ts (EMB-067), applied to a third resource — a
// synchronous reservation (activeCount is checked and incremented in the
// same synchronous branch, never across an `await`, so N concurrent
// acquires in one tick cannot all pass), idempotent release, FIFO queue
// order, and an aborted still-queued request leaves the queue immediately
// rather than silently starving the next real waiter — copied from those
// files' own contract, not reinvented.
//
// WHY THIS EXISTS SEPARATELY FROM LazyEmbedMount. LazyEmbedMount (EMB-065)
// only gates MOUNTING on VISIBILITY — "begin work when it enters the
// viewport plus a margin". It deliberately has no cap of its own: every
// embed inside the mount margin mounts at once (see that module's own doc,
// "NO HARD CAP"). With a 600px mount margin on each side of the viewport,
// several 200px-tall pictures fit inside that combined window at once —
// UAT D-101 measured 40 concurrent downloads with only 6 pictures actually
// in view, because "near the viewport" and "at most 4 at a time" are two
// different properties. KbMarkdownImage mounting through LazyEmbedMount
// answers "should this picture be doing anything at all right now" (yes,
// once near-viewport); this pool answers the SEPARATE question "is a
// download slot available right now" — the `<img src>` is not rendered
// (so no request starts) until a lease is granted.
//
// A lease is held from the moment a slot is granted until the picture's
// own `load` or `error` event fires — an image that finishes loading OR
// fails both free their slot; a picture the reader scrolls away from
// before its lease was granted gives up its QUEUED place (the acquiring
// component's `AbortSignal` fires); one scrolled away from after a lease
// was already granted (still downloading) also releases it on unmount,
// exactly like `pdfWorkerPool`'s lease-holder does when it unmounts
// mid-flight.

/** UAT D-101's own number (row U-48: "at most 4 image downloads in flight
 *  at once") — named so a change to the ceiling is a one-line edit with a
 *  citation, not a magic `4` a reader has to trust. */
export const KB_IMAGE_DOWNLOAD_POOL_CEILING = 4

export interface KbImageDownloadLease {
  /** Frees this lease's slot, letting the next queued acquire (if any)
   *  proceed. Idempotent — safe to call once from the picture's own
   *  load/error handler and again from the component's unmount cleanup. */
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

class KbImageDownloadPool {
  private activeCount = 0
  private queue: QueueEntry[] = []

  /**
   * Resolves once a slot is available. If `signal` aborts first (the
   * picture unmounted, or was scrolled far enough out of view to give up
   * its queued place — EMB-065's "stops when well outside", applied to a
   * still-queued lease), the request leaves the queue and the promise
   * rejects with an AbortError, matching `fetch`'s own cancellation
   * contract.
   *
   * `onQueued` fires SYNCHRONOUSLY, and only when the request could not be
   * granted immediately, so a caller can show a waiting state precisely
   * when the ceiling is the reason it is waiting, never flashing one when
   * it is not.
   */
  acquire(signal: AbortSignal, onQueued?: () => void): Promise<KbImageDownloadLease> {
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

      if (this.activeCount < KB_IMAGE_DOWNLOAD_POOL_CEILING) {
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

/** One pool, page-wide — matching D-101's "at most 4 image downloads in
 *  flight at once" regardless of how many `KbMarkdownImage` instances the
 *  page has mounted. Chat's `MarkdownImage`/`ChatImage` never import this
 *  module — the cap is for the knowledge composition only. */
export const kbImageDownloadPool = new KbImageDownloadPool()
