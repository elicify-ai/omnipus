// viewEvaluationPool.ts — bounds how many `.base` VIEW EVALUATIONS may be in
// flight page-wide at once (ADR-083 embedded-content spec ~line 661 / spec
// ~line 901): "No more than 4 view evaluations may be in flight page-wide at
// any instant... Beyond that, an embed shows 'queued' — visible ... never a
// silent wait. An embed scrolled back out of view before its turn cancels
// its queue slot."
//
// THIS IS THE SAME PRIMITIVE AS pdfWorkerPool.ts (EMB-032), applied to a
// different resource — deliberately, per the review that found this gap:
// "Follow the pdfWorkerPool precedent... rather than inventing a second
// shape." A synchronous reservation (activeCount is checked and incremented
// in the same synchronous branch, never across an `await`, so N concurrent
// acquires in one tick cannot all pass), idempotent release, FIFO queue
// order, and an aborted still-queued request leaves the queue immediately
// rather than silently starving the next real waiter — all copied from that
// file's own contract, not reinvented.
//
// WHERE THIS DIFFERS FROM pdfWorkerPool, on purpose: a "view evaluation" is
// NOT held for a component's whole mounted lifetime the way a PDFWorker is
// (LazyEmbedMount's own child stays mounted and keeps talking to its PDF
// worker for Edit mode / Save). A `.base` view evaluation is ONE bounded
// fetch — BasePreview's own `resultQuery`, "the EXPENSIVE fetch" per that
// file's own comment — that starts and finishes. BasePreview releases its
// lease as soon as that one fetch settles, success or error; see
// BasePreview.tsx's `queryFn` for exactly where.

/** Spec's own number (~line 901): "No more than 4 view evaluations may be
 *  in flight page-wide at any instant." Named so a change to the ceiling is
 *  a one-line edit with a citation, not a magic `4` a reader has to trust. */
export const VIEW_EVALUATION_POOL_CEILING = 4

export interface ViewEvaluationLease {
  /** Frees this lease's slot, letting the next queued acquire (if any)
   *  proceed. Idempotent — safe to call once when the wrapped fetch settles
   *  and again from any later cleanup path. */
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

class ViewEvaluationPool {
  private activeCount = 0
  private queue: QueueEntry[] = []

  /**
   * Resolves once a slot is available. If `signal` aborts first (the
   * embed unmounted before its turn — LazyEmbedMount scrolling it well
   * outside the far margin, EMB-065, applied to a still-queued lease — or
   * TanStack Query cancelling the fetch), the request leaves the queue and
   * the promise rejects with an AbortError, matching `fetch`'s own
   * cancellation contract.
   *
   * `onQueued` fires SYNCHRONOUSLY, and only when the request could not be
   * granted immediately, so a caller can show a waiting state precisely
   * when the ceiling is the reason it is waiting, never flashing one when
   * it is not.
   */
  acquire(signal: AbortSignal, onQueued?: () => void): Promise<ViewEvaluationLease> {
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

      if (this.activeCount < VIEW_EVALUATION_POOL_CEILING) {
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

/** One pool, page-wide — matching the spec's "no more than 4 view
 *  evaluations may be in flight page-wide at any instant" regardless of how
 *  many `BasePreview` instances (embedded or the pane's own) the page has
 *  mounted. */
export const viewEvaluationPool = new ViewEvaluationPool()
