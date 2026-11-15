// Frozen test-only strict validators from pre-feature 6c71aa86759bb44e33214b45ccc7cf2776a3b4b5.
// NOT derived by omitting fields from the new schema: that would silently
// change the supposed old browser whenever the generated schema changes.
// not-wire-format: compatibility instrument, never used by the application.
import { z } from 'zod'

export const oldRetry = z.object({
  type: z.literal('provider_retry'), session_id: z.string().min(1).max(255),
  turn_id: z.string().min(1).max(128), provider: z.string().min(1).max(256),
  model: z.string().min(1).max(256), retry_at: z.string(),
  retry_after_seconds: z.number().int().min(1), sent_at: z.string(),
  attempt: z.number().int().min(2), max_attempts: z.number().int().min(1),
  error_code: z.string().max(64), seq: z.number().int().min(1).optional(),
}).strict()
export const oldStatus = z.object({
  type: z.literal('provider_status'), session_id: z.string().min(1).max(255),
  turn_id: z.string().min(1).max(128), state: z.enum(['waiting', 'responding', 'recovered']),
  sent_at: z.string(), silent_since: z.string().optional(), stall_at: z.string().optional(),
  attempt: z.number().int().min(1).optional(), max_attempts: z.number().int().min(1).optional(),
  retries: z.number().int().min(1).optional(), seq: z.number().int().min(1).optional(),
}).strict()
