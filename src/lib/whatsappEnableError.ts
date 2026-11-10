import { getErrorMessage, isApiError, parseServerErrorField } from './api-error'

export const WHATSAPP_PAIRING_DELETED_MESSAGE = 'This WhatsApp connection was deleted. Add it again from Connectors.'
export const WHATSAPP_ENABLE_CANCELLED_MESSAGE = 'The WhatsApp connection is turned on, but its pairing was cancelled before it started. Press Retry to start a new one.'

// WhatsApp enable responses carry actionable, user-facing server copy. Keep
// this exception local to pairing; other API consumers retain their defaults.
export function getWhatsAppEnableErrorMessage(
  error: unknown,
  fallback: string,
  options?: Parameters<typeof getErrorMessage>[2],
): string {
  if (isApiError(error)) {
    if (error.status === 404) return WHATSAPP_PAIRING_DELETED_MESSAGE
    if (error.status === 500 || (error.status === 409 && error.code === 'pairing_in_progress')) {
      const serverMessage = parseServerErrorField(error.body)
      if (serverMessage) return serverMessage
    }
  }
  return getErrorMessage(error, fallback, options)
}
