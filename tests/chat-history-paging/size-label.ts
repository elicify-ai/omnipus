// Test-only independent human-size oracle. The spec permits wording/units to
// vary, but the displayed value must describe the true original byte size.
// Accept only the precision's normal half-unit rounding error; never a4KiB
// preview size, zero label, magic tolerance or product formatter as the oracle.
export function hasTrueSizeLabel(text: string, originalBytes: number): boolean {
  // A2.4 leaves units/wording to frontend-lead. KB/MB have both common
  // decimal/binary display conventions; explicit KiB/MiB stay binary only.
  const units: Record<string, number[]> = { B: [1], bytes: [1], kB: [1000], KB: [1000, 1024], KiB: [1024], MB: [1_000_000, 1_048_576], MiB: [1_048_576] }
  return [...text.matchAll(/(?<![-\d.,])(\d[\d,]*(?:\.\d+)?)\s*(KiB|MiB|kB|KB|MB|B|bytes)\b/g)].some(match => {
    const displayed = match[1].replaceAll(',', '')
    const value = Number(displayed)
    const decimals = displayed.split('.')[1]?.length ?? 0
    return value > 0 && units[match[2]].some(scale =>
      Math.abs(value * scale - originalBytes) <= scale / (2 * 10 ** decimals) + Number.EPSILON * originalBytes)
  })
}
