/**
 * chat.knowledge-index-frame.test.ts — ADR-067 FR-080 / US-6 AS-4, AS-6.
 *
 * ── The regression this pins down ────────────────────────────────────────────
 * `knowledge_index_progress` was defined in contracts/asyncapi.yaml, generated
 * into asyncapi-types.ts, admitted by the generated Zod frame union, and
 * BROADCAST by the gateway (pkg/gateway/knowledge_lifecycle.go) — and then
 * routed nowhere. `handleFrame` had no case for it and no store to put it in,
 * so every knowledge base in the Library rendered "Omnipus has not received any
 * indexing progress for it" forever, and the `phase: "idle"` state that
 * satisfies US-6 AS-4/AS-6 was unreachable in production on every collection.
 *
 * The frame parsing/validation layer was green throughout, because a frame that
 * validates and is then dropped looks exactly like a frame that is handled.
 *
 * DIES ON: removing the `case 'knowledge_index_progress'` from
 * src/store/chat.ts's handleFrame.
 */

import { describe, it, expect, beforeEach } from 'vitest'

import { useChatStore } from './chat'
import { useKnowledgeIndexStore, selectKnowledgeIndexProgress } from './knowledgeIndex'
import type { KnowledgeIndexProgressFrame } from '@/lib/api/generated/asyncapi-types'

function frame(over: Partial<KnowledgeIndexProgressFrame> = {}): KnowledgeIndexProgressFrame {
  return {
    type: 'knowledge_index_progress',
    collection_id: 'kb_3d1c9a7e5b2f4806',
    workspace_id: 'ws_7f3a',
    phase: 'indexing',
    indexed_files: 120,
    total_known: true,
    total_files: 900,
    skipped_files: 0,
    ...over,
  }
}

describe('chat handleFrame → knowledge index store (ADR-067 FR-080)', () => {
  beforeEach(() => {
    useKnowledgeIndexStore.setState({ byCollection: {} })
  })

  it('routes a progress frame into the store, keyed by collection', () => {
    useChatStore.getState().handleFrame(frame())

    const stored = selectKnowledgeIndexProgress(
      useKnowledgeIndexStore.getState(),
      'kb_3d1c9a7e5b2f4806',
    )
    expect(stored?.phase).toBe('indexing')
    expect(stored?.indexed_files).toBe(120)
    expect(stored?.total_files).toBe(900)
  })

  it('keeps two collections apart — one frame must not answer for the other', () => {
    // A frame for another collection driving this pane would be a fabricated
    // answer of the worst kind: a plausible one.
    useChatStore.getState().handleFrame(frame({ collection_id: 'kb_a', indexed_files: 1 }))
    useChatStore.getState().handleFrame(frame({ collection_id: 'kb_b', indexed_files: 2 }))

    const s = useKnowledgeIndexStore.getState()
    expect(selectKnowledgeIndexProgress(s, 'kb_a')?.indexed_files).toBe(1)
    expect(selectKnowledgeIndexProgress(s, 'kb_b')?.indexed_files).toBe(2)
    expect(selectKnowledgeIndexProgress(s, 'kb_never_seen')).toBeUndefined()
  })

  it('the latest frame for a collection replaces the previous one', () => {
    useChatStore.getState().handleFrame(frame({ phase: 'enumerating', indexed_files: 3 }))
    useChatStore.getState().handleFrame(frame({ phase: 'idle', indexed_files: 900 }))

    const stored = selectKnowledgeIndexProgress(
      useKnowledgeIndexStore.getState(),
      'kb_3d1c9a7e5b2f4806',
    )
    expect(stored?.phase).toBe('idle')
    expect(stored?.indexed_files).toBe(900)
  })

  it('stores the enumerating frame WITHOUT inventing a total (FR-036)', () => {
    // While the tree is being walked there is no denominator. The store must
    // carry the absence through rather than defaulting it to a number, because
    // every reader downstream decides what it may say from these two fields.
    useChatStore.getState().handleFrame(
      // total_files omitted entirely, which is the shape the contract requires
      // while total_known is false.
      {
        type: 'knowledge_index_progress',
        collection_id: 'kb_walking',
        workspace_id: 'ws_7f3a',
        phase: 'enumerating',
        indexed_files: 42,
        total_known: false,
        skipped_files: 0,
      },
    )

    const stored = selectKnowledgeIndexProgress(useKnowledgeIndexStore.getState(), 'kb_walking')
    expect(stored?.total_known).toBe(false)
    expect(stored?.total_files).toBeUndefined()
    expect(stored?.indexed_files).toBe(42)
  })

  it('an unset collection id selects nothing rather than an arbitrary entry', () => {
    useChatStore.getState().handleFrame(frame())
    const s = useKnowledgeIndexStore.getState()
    expect(selectKnowledgeIndexProgress(s, undefined)).toBeUndefined()
    expect(selectKnowledgeIndexProgress(s, '')).toBeUndefined()
  })
})
