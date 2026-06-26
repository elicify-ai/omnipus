import { describe, it, expect } from 'vitest'
import { formatTokens } from './formatTokens'

describe('formatTokens', () => {
  it('shows numbers below 1000 as-is', () => {
    expect(formatTokens(0)).toBe('0')
    expect(formatTokens(1)).toBe('1')
    expect(formatTokens(999)).toBe('999')
  })

  it('formats thousands as k with one decimal', () => {
    expect(formatTokens(1000)).toBe('1.0k')
    expect(formatTokens(1500)).toBe('1.5k')
    expect(formatTokens(44000)).toBe('44.0k')
    expect(formatTokens(999999)).toBe('1000.0k')
  })

  it('formats millions as M with one decimal', () => {
    expect(formatTokens(1_000_000)).toBe('1.0M')
    expect(formatTokens(1_200_000)).toBe('1.2M')
    expect(formatTokens(10_000_000)).toBe('10.0M')
  })
})
