import type { AttachSessionFrame } from '@/lib/api/generated/asyncapi-types'
import type { HistoryPageSizing } from '@/store/chat/types'

/** Latest 100 distinct stable drawn rows in one width/presentation generation. */
export class HistoryRowMeasurements {
  private samples = new Map<string, number>()
  private total = 0
  get average(): number { return this.samples.size ? Math.max(1, this.total / this.samples.size) : 80 }
  get count(): number { return this.samples.size }
  observe(key: string, height: number, stable = true): boolean {
    if (!stable || !Number.isFinite(height) || height <= 0) return false
    const previous = this.samples.get(key)
    if (previous === height) return false
    if (previous !== undefined) { this.total -= previous; this.samples.delete(key) }
    this.samples.set(key, height)
    this.total += height
    if (this.samples.size > 100) {
      const first = this.samples.keys().next().value!
      this.total -= this.samples.get(first)!
      this.samples.delete(first)
    }
    return true
  }
  retainPositions(positions: ReadonlySet<string>): void {
    for (const [key, height] of this.samples) {
      let kept = positions.has(key)
      for (let i = key.indexOf(':'); !kept && i !== -1; i = key.indexOf(':', i + 1)) kept = positions.has(key.slice(0, i))
      if (!kept) { this.samples.delete(key); this.total -= height }
    }
  }
  reset(): void { this.samples.clear(); this.total = 0 }
}
export function historyPageSizing(height: number, average: number, screens = 2): HistoryPageSizing {
  const floor = Math.max(1, Math.ceil(2 * height / Math.max(1, average)))
  const limit = Math.min(100, Math.max(20, Math.ceil(screens * height / Math.max(1, average))))
  return { limit, min_rows: Math.min(limit, floor) }
}
export function historyPixelTrigger(distance: number, height: number, visible = true): boolean {
  return visible && Number.isFinite(height) && height > 0 && Number.isFinite(distance) && distance <= 2 * height
}
const geometry = new Map<string, { height: number; average: number }>()
export function recordHistoryGeometry(sessionId: string, height: number, average: number): void { geometry.set(sessionId, { height, average }) }
export function forgetHistoryGeometry(sessionId: string): void { geometry.delete(sessionId) }
export function historyAttachSizing(sessionId: string): Pick<AttachSessionFrame, 'history_limit' | 'history_min_rows'> {
  const sample = geometry.get(sessionId)
  if (!sample || !Number.isFinite(sample.height) || sample.height <= 0) return {}
  const size = historyPageSizing(sample.height, sample.average, 4)
  return { history_limit: size.limit, history_min_rows: size.min_rows }
}
