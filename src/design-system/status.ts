// Import the status slices, not `tokens` / `resolvedTokens`. A dynamic index
// keeps every property of the imported object, and the full catalog is what
// pushed the production bundle over its raw-size budget.
import { statusResolvedTokens as resolvedTokens, statusTokens as tokens } from './tokens'

const TOKEN_ROLES = ['foreground', 'background', 'border', 'icon', 'label', 'hover', 'focus', 'filledForeground'] as const
type TokenRole = (typeof TOKEN_ROLES)[number]
type VisionDeficiency = 'protanopia' | 'deuteranopia' | 'tritanopia'
type StatusTokenSet = Readonly<Record<TokenRole, string>>

const ROLE_SUFFIX: Readonly<Record<TokenRole, string>> = {
  foreground: 'foreground', background: 'background', border: 'border', icon: 'icon',
  label: 'label', hover: 'hover', focus: 'focus', filledForeground: 'filled-foreground',
}
const AMBIGUITY_DISTANCE = 30
const generatedTokens = tokens as Readonly<Record<string, string>>
const generatedValues = resolvedTokens as Readonly<Record<string, string | number>>

export interface StatusPresentation {
  readonly label: string
  readonly resolvedColor: string
  readonly nonColorCue: string
  readonly tokens: StatusTokenSet
  readonly contrast: Readonly<{ tintedLabel: number; hoverLabel: number; filledLabel: number; border: number }>
}

function generatedColor(id: string): string {
  const value = generatedValues[id]
  if (typeof value !== 'string' || !/^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$/.test(value)) {
    throw new Error(`generated colour token is missing or invalid: ${id}`)
  }
  return value.toUpperCase()
}

const SURFACE = generatedColor('color.surface.page')
const FILLED_FOREGROUND = generatedColor('color.text.on-status-fill')

function status(tokenName: string, label: string, nonColorCue: string): StatusPresentation {
  const tokenSet = Object.fromEntries(TOKEN_ROLES.map((role) => {
    const id = `component.status.${tokenName}.${ROLE_SUFFIX[role]}`
    const value = generatedTokens[id]
    if (value === undefined) throw new Error(`generated token is missing: ${id}`)
    return [role, value]
  })) as unknown as StatusTokenSet
  const color = generatedColor(`color.status.${tokenName}`)
  const background = compositeOver(generatedColor(`color.status.${tokenName}.background`), SURFACE)
  const hover = compositeOver(generatedColor(`color.status.${tokenName}.hover`), SURFACE)
  return Object.freeze({
    label, resolvedColor: color, nonColorCue, tokens: Object.freeze(tokenSet),
    contrast: Object.freeze({
      tintedLabel: contrastRatio(color, background),
      hoverLabel: contrastRatio(color, hover),
      filledLabel: contrastRatio(FILLED_FOREGROUND, color),
      border: contrastRatio(color, SURFACE),
    }),
  })
}

export const statusContract = Object.freeze({
  inbox: status('inbox', 'Inbox', 'quiet-circle'),
  next: status('next', 'Next', 'ready-info'),
  inProgress: status('in-progress', 'In progress', 'live-work'),
  blocked: status('blocked', 'Blocked', 'prohibit'),
  done: status('done', 'Done', 'check'),
  failed: status('failed', 'Failed', 'x'),
  cancelled: status('cancelled', 'Cancelled', 'stopped-by-user'),
})

function rgb(hex: string): readonly [number, number, number] {
  if (!/^#[0-9a-fA-F]{6}$/.test(hex)) throw new Error(`invalid six-digit hex colour: ${hex}`)
  return [Number.parseInt(hex.slice(1, 3), 16), Number.parseInt(hex.slice(3, 5), 16), Number.parseInt(hex.slice(5, 7), 16)]
}

function compositeOver(foreground: string, background: string): string {
  if (!/^#[0-9a-fA-F]{8}$/.test(foreground)) throw new Error(`invalid eight-digit hex colour: ${foreground}`)
  const foregroundRgb = rgb(foreground.slice(0, 7))
  const backgroundRgb = rgb(background)
  const alpha = Number.parseInt(foreground.slice(7, 9), 16) / 255
  return `#${foregroundRgb.map((channel, index) => Math.round(channel * alpha + backgroundRgb[index] * (1 - alpha)).toString(16).padStart(2, '0')).join('').toUpperCase()}`
}

function luminance(hex: string): number {
  const channels = rgb(hex).map((channel) => {
    const normalized = channel / 255
    return normalized <= 0.04045 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * channels[0] + 0.7152 * channels[1] + 0.0722 * channels[2]
}

export function contrastRatio(first: string, second: string): number {
  const light = Math.max(luminance(first), luminance(second))
  const dark = Math.min(luminance(first), luminance(second))
  return (light + 0.05) / (dark + 0.05)
}

const CVD_MATRICES: Readonly<Record<VisionDeficiency, readonly (readonly number[])[]>> = {
  protanopia: [[0.567, 0.433, 0], [0.558, 0.442, 0], [0, 0.242, 0.758]],
  deuteranopia: [[0.625, 0.375, 0], [0.7, 0.3, 0], [0, 0.3, 0.7]],
  tritanopia: [[0.95, 0.05, 0], [0, 0.433, 0.567], [0, 0.475, 0.525]],
}

export function simulateColorVision(hex: string, deficiency: VisionDeficiency): string {
  const source = rgb(hex)
  return `#${CVD_MATRICES[deficiency].map((row) => {
    const value = Math.round(row.reduce((sum, coefficient, index) => sum + coefficient * source[index], 0))
    return Math.max(0, Math.min(255, value)).toString(16).padStart(2, '0')
  }).join('').toUpperCase()}`
}

export function findColorVisionAmbiguities<T extends Readonly<Record<string, StatusPresentation>>>(contract: T, deficiency: VisionDeficiency): [keyof T, keyof T][] {
  const entries = Object.entries(contract) as [keyof T, StatusPresentation][]
  const ambiguities: [keyof T, keyof T][] = []
  for (let first = 0; first < entries.length; first += 1) {
    for (let second = first + 1; second < entries.length; second += 1) {
      const firstRgb = rgb(simulateColorVision(entries[first][1].resolvedColor, deficiency))
      const secondRgb = rgb(simulateColorVision(entries[second][1].resolvedColor, deficiency))
      const distance = Math.hypot(...firstRgb.map((channel, index) => channel - secondRgb[index]))
      if (distance < AMBIGUITY_DISTANCE) ambiguities.push([entries[first][0], entries[second][0]])
    }
  }
  return ambiguities
}

export function validateStatusContract(contract: Readonly<Record<string, StatusPresentation>>): string[] {
  const diagnostics: string[] = []
  const labels = new Map<string, string>()
  const cues = new Map<string, string>()
  for (const [name, presentation] of Object.entries(contract)) {
    if (contrastRatio(presentation.resolvedColor, SURFACE) < 4.5) diagnostics.push(`status "${name}" has contrast below 4.5:1 on ${SURFACE}`)
    for (const role of TOKEN_ROLES) if (!(role in presentation.tokens)) diagnostics.push(`status "${name}" is missing token role "${role}"`)
    const previousLabel = labels.get(presentation.label)
    if (previousLabel !== undefined) diagnostics.push(`status label "${presentation.label}" is duplicated by "${previousLabel}" and "${name}"`)
    else labels.set(presentation.label, name)
    const previousCue = cues.get(presentation.nonColorCue)
    if (previousCue !== undefined) diagnostics.push(`status non-colour cue "${presentation.nonColorCue}" is duplicated by "${previousCue}" and "${name}"`)
    else cues.set(presentation.nonColorCue, name)
  }
  return diagnostics
}
