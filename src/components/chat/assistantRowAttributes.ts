/**
 * Read-only DOM correlation attributes for an assistant row.
 *
 * WHY these exist: the row's `data-message-id` is the DISPLAY id of the
 * bubble, and one bubble can stand for SEVERAL persisted transcript
 * entries — a replay merges the follow-up segments of one turn into the
 * bubble that opened it, and the entries folded in are recorded in
 * `ChatMessage.mergedReplayIds` (the store NEVER puts the row's own id in
 * that list: the primary entry is covered by the message order instead).
 * Neither the turn-correlation id nor those merged entry ids were readable
 * from the DOM, so a check driving the real browser could not tell WHICH
 * persisted entry a rendered reply came from.
 *
 * The attributes mirror store state and are read-only: nothing in the app
 * reads them back, and they change nothing a user sees.
 */

/** The store fields an assistant row's correlation attributes mirror. */
export interface AssistantRowSource {
  /** The row's own (display) id — never repeated in the merged id list. */
  id?: string
  /** Turn-correlation id of the row's producing turn. */
  turnId?: string
  /** Ids of the transcript entries merged INTO this row beyond its own. */
  mergedReplayIds?: readonly string[]
}

/** The attributes a row carries, ready to spread onto the rendered element. */
export interface AssistantRowCorrelation {
  'data-turn-id'?: string
  'data-merged-message-ids'?: string
}

/**
 * The correlation attributes for one assistant row.
 *
 * `data-turn-id` — the row's turn-correlation id; OMITTED when the row has
 * none (a live token-streamed bubble carries no turn id until the wire names
 * one).
 *
 * `data-merged-message-ids` — the merged transcript entry ids, space
 * separated in the store's order, EXCLUDING the row's own id; OMITTED when
 * the row merged nothing.
 */
export function assistantRowCorrelationAttributes(
  message: AssistantRowSource | undefined,
): AssistantRowCorrelation {
  const merged = (message?.mergedReplayIds ?? []).filter((id) => id !== message?.id)
  return {
    'data-turn-id': message?.turnId ? message.turnId : undefined,
    'data-merged-message-ids': merged.length > 0 ? merged.join(' ') : undefined,
  }
}
