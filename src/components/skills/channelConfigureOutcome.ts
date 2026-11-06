// channelConfigureOutcome.ts — how the Connectors screen shows a Configure
// result (channels spec r8 table T-6c, FR-055; r3 R2-MAJ-006).
//
// Only `applied` and `no_reload_needed` may produce a success toast, and
// "Save and enable" calls enable only after those two. No text below claims
// that a restart completed or that the old settings keep running unless the
// server confirmed it (`applied`).

import type {
  ChannelAvailability,
  ChannelConfigureResponse,
} from '@/lib/api/generated/openapi-types'
import type { Toast } from '@/store/ui'

export type ConfigureSavePath = 'save' | 'save-and-enable'

export type OutcomeToast = Pick<Toast, 'message' | 'variant'>

export interface ConfigureOutcomeView {
  /** Toasts to show for this outcome, in order, before any enable call. */
  toasts: OutcomeToast[]
  /** "Save and enable" may call enable only when this is true. */
  mayEnable: boolean
  /** `failed`: re-read availability and show it when the read succeeds. */
  readAvailability: boolean
}

export const APPLIED_TEXT = 'Saved. The connection restarted with the new settings.'
export const NO_RELOAD_NEEDED_TEXT = 'Saved.'
export const UNCONFIRMED_TEXT =
  'Saved. The restart was not confirmed yet; check the connection status in a moment.'

function notRunningText(reason: string | undefined): string {
  return reason
    ? `Saved, but the connection is not running: ${reason}.`
    : 'Saved, but the connection is not running.'
}

function failedText(reason: string | undefined): string {
  const head = reason
    ? `Saved, but applying the change did not complete: ${reason}.`
    : 'Saved, but applying the change did not complete.'
  return `${head} Check the connection status before retrying.`
}

export function describeConfigureOutcome(
  response: ChannelConfigureResponse,
  path: ConfigureSavePath,
): ConfigureOutcomeView {
  const outcome = response.apply_outcome
  switch (outcome) {
    case 'applied': {
      const toasts: OutcomeToast[] = [{ message: APPLIED_TEXT, variant: 'success' }]
      if (response.apply_note) toasts.push({ message: response.apply_note, variant: 'default' })
      return { toasts, mayEnable: true, readAvailability: false }
    }
    case 'no_reload_needed':
      // "Save and enable": the enable call reports its own result (T-6c).
      return {
        toasts: path === 'save' ? [{ message: NO_RELOAD_NEEDED_TEXT, variant: 'success' }] : [],
        mayEnable: true,
        readAvailability: false,
      }
    case 'applied_not_running':
      return {
        toasts: [{ message: notRunningText(response.apply_reason), variant: 'warning' }],
        mayEnable: false,
        readAvailability: false,
      }
    case 'unconfirmed':
      return {
        toasts: [{ message: UNCONFIRMED_TEXT, variant: 'warning' }],
        mayEnable: false,
        readAvailability: false,
      }
    case 'failed':
      return {
        toasts: [{ message: failedText(response.apply_reason), variant: 'error' }],
        mayEnable: false,
        readAvailability: true,
      }
    default: {
      const unhandled: never = outcome
      throw new Error(`unhandled apply_outcome: ${String(unhandled)}`)
    }
  }
}

const AVAILABILITY_LABEL: Record<ChannelAvailability['state'], string> = {
  running: 'running',
  not_running: 'not running',
  not_loaded: 'not loaded',
}

/** T-6c `failed` row: the observed state, appended after the error toast. */
export function availabilityText(availability: ChannelAvailability): string {
  return `The connection is currently: ${AVAILABILITY_LABEL[availability.state]}.`
}
