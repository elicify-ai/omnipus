import {
  CheckCircle,
  Clock,
  ArrowsClockwise,
  Spinner,
  Warning,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import { QRCodeSVG } from 'qrcode.react'
import type { WhatsAppPairingState } from '@/store/whatsappPairing'

// RetryableState: shared layout for the timeout and error cases — both show an
// icon + message line above a Retry button. Deduplicates the two identical blocks.
function RetryableState({
  icon: IconComponent,
  iconProps,
  message,
  onRetry,
}: {
  icon: Icon
  iconProps?: React.ComponentProps<Icon>
  message: string
  onRetry?: () => void
}) {
  return (
    <div className="flex flex-col items-center gap-3">
      <div className={`flex items-center gap-2 ${iconProps?.className ?? ''}`}>
        <IconComponent size={14} {...iconProps} className={undefined} />
        <p className="text-xs">{message}</p>
      </div>
      {onRetry && (
        <button tabIndex={0}
          type="button"
          onClick={onRetry}
          data-testid="whatsapp-retry"
          className="flex items-center gap-1.5 text-xs text-[var(--color-accent)] hover:text-[var(--color-accent)]/80 transition-colors"
        >
          <ArrowsClockwise size={13} />
          Retry
        </button>
      )}
    </div>
  )
}

// WhatsAppPairingBody renders the inner QR/status block for the linked-device
// notice (#283 / US-C3). Implements the full 5-state machine by real wire names:
// waiting | code | linked | timeout | error
export function WhatsAppPairingBody({
  pairing,
  onRetry,
}: {
  pairing?: WhatsAppPairingState
  onRetry?: () => void
}) {
  // Shared spinner — used for both the explicit 'waiting' state and any
  // unexpected/unknown status that falls through the switch below.
  const generatingSpinner = (
    <div className="flex items-center gap-2 text-[var(--color-muted)]">
      <Spinner size={14} className="animate-spin" />
      <p className="text-xs">Generating your QR code&hellip;</p>
    </div>
  )

  // waiting: no frame yet, or explicit waiting state — show spinner
  if (!pairing || pairing.status === 'waiting') {
    return generatingSpinner
  }

  // code: QR delivered — show scannable QR + exact Linked Devices steps
  if (pairing.status === 'code' && pairing.qr) {
    return (
      <>
        {/* QR must sit on a light background to scan reliably in dark mode. */}
        <div data-testid="whatsapp-qr" className="rounded-md bg-white p-3">
          <QRCodeSVG value={pairing.qr} size={184} level="L" />
        </div>
        <p className="text-xs text-[var(--color-secondary)] text-center">
          Open <span className="font-medium">WhatsApp</span> on your phone, go to{' '}
          <span className="font-medium">Settings &rarr; Linked Devices &rarr; Link a Device</span>,
          then scan this code. It refreshes every 20s.
        </p>
      </>
    )
  }

  switch (pairing.status) {
    case 'linked':
      return (
        <div className="flex items-center gap-2 text-[var(--color-success)]">
          <CheckCircle size={16} weight="fill" />
          <p className="text-xs font-medium">Linked successfully.</p>
        </div>
      )
    case 'timeout':
      return (
        <RetryableState
          icon={Clock}
          iconProps={{ className: 'text-[var(--color-muted)]' }}
          message="QR expired — tap to get a fresh one."
          onRetry={onRetry}
        />
      )
    case 'error':
      return (
        <RetryableState
          icon={Warning}
          iconProps={{ weight: 'fill', className: 'text-[var(--color-error)]' }}
          message="Pairing failed — tap to retry."
          onRetry={onRetry}
        />
      )
    default:
      // Fallback for any unexpected status — show spinner
      return generatingSpinner
  }
}
