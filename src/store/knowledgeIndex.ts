import { create } from 'zustand'

import type { KnowledgeIndexProgressFrame } from '@/lib/api/generated/asyncapi-types'

// Live indexing state for each knowledge base, fed by the
// `knowledge_index_progress` WS frame (see chatStore.handleFrame) and read by
// the Library's KnowledgePanel (ADR-067 FR-080, US-6 AS-4/AS-6).
//
// ── Why this store exists ────────────────────────────────────────────────────
// The frame was defined in contracts/asyncapi.yaml, generated into
// asyncapi-types.ts, validated by the generated Zod union, and BROADCAST by the
// gateway (pkg/gateway/knowledge_lifecycle.go) — and then routed nowhere. The
// SPA had no case for it and no store to hold it, so KnowledgePanel received
// `progress: undefined` forever and rendered "Omnipus has not received any
// indexing progress for it" on every knowledge base, permanently. US-6 AS-4
// ("indexing has finished → no incompleteness statement is shown") and AS-6
// ("a freshness check that finds nothing changed → no banner appears at all")
// were unreachable in production because the state that satisfies them —
// `phase: "idle"` — never arrived. This is the missing wire.
//
// ── Keyed by collection, not by workspace or mount ───────────────────────────
// `collection_id` is derived from the collection's resolved real path (FR-031),
// so two mounts of one folder — in one workspace or several — share it. Keying
// on it means one frame updates every place that collection is on screen, which
// is what the frame's own contract says it is for.
//
// ── What is deliberately NOT here ────────────────────────────────────────────
// No polling, no fetch, no derived percentage. `total_files` is absent while the
// tree is being enumerated and `total_known` says so; the frame is stored
// exactly as it arrived and the rendering decides what may be said about it.
// Computing a ratio here would put an invented denominator behind every reader
// of the store at once.

export type KnowledgeIndexStore = {
  /** Latest frame per `collection_id`. */
  byCollection: Record<string, KnowledgeIndexProgressFrame>
  /** Apply an incoming knowledge_index_progress frame. */
  apply: (frame: KnowledgeIndexProgressFrame) => void
  /** Drop everything (used on logout / workspace teardown). */
  reset: () => void
}

export const useKnowledgeIndexStore = create<KnowledgeIndexStore>((set) => ({
  byCollection: {},
  apply: (frame) =>
    set((s) => ({
      byCollection: { ...s.byCollection, [frame.collection_id]: frame },
    })),
  reset: () => set({ byCollection: {} }),
}))

/**
 * The latest frame for one collection, or undefined when none has arrived.
 *
 * Undefined is a real answer and callers must render it as one: "nothing has
 * reported" is not "the index is fine". Passing `undefined` for the collection
 * id returns undefined rather than picking an arbitrary entry.
 */
export function selectKnowledgeIndexProgress(
  state: KnowledgeIndexStore,
  collectionId: string | undefined,
): KnowledgeIndexProgressFrame | undefined {
  if (collectionId === undefined || collectionId === '') return undefined
  return state.byCollection[collectionId]
}
