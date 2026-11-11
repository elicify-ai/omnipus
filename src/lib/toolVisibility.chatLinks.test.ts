/**
 * RED: a successful show_to_user call is hidden; an error stays visible;
 * verbose chat still shows it (BS-6.14 thread card / FR-018).
 */
import { describe, expect, it } from 'vitest'
import { shouldRenderToolCall } from './toolVisibility'

describe('shouldRenderToolCall show_to_user', () => {
  it('hides a successful call', () => {
    expect(shouldRenderToolCall('show_to_user', {}, false, false)).toBe(false)
  })

  it('shows an error', () => {
    expect(shouldRenderToolCall('show_to_user', {}, false, true)).toBe(true)
  })

  it('shows the call when verbose chat is on', () => {
    expect(shouldRenderToolCall('show_to_user', {}, true, false)).toBe(true)
  })
})
