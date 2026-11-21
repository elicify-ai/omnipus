// #1428 step 2 (spec 4.6.1, Mermaid fix row): the owning chat of a path that
// must capture at its click but keeps the chat store out of its module graph
// (mermaid-renderer.tsx lazily imports it). bound-send.ts registers the
// capture when it loads; this module imports nothing.

/** A frozen send. Returns false when the owning chat cannot take it now. */
export type CapturedSend = () => boolean

type Capture = (path: string, text: string) => CapturedSend | null

let capture: Capture | null = null

export function registerOwningChatCapture(fn: Capture): void {
  capture = fn
}

/** Freezes a send of `text` to the chat open now; null when none is registered or open. */
export function captureOwningChatSend(path: string, text: string): CapturedSend | null {
  return capture ? capture(path, text) : null
}
