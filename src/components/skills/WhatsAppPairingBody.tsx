import {
  CheckCircle,
  Clock,
  ArrowsClockwise,
  Spinner,
  StopCircle,
  Warning,
} from '@phosphor-icons/react'
import type { Icon } from '@phosphor-icons/react'
import { QRCodeSVG } from 'qrcode.react'
import { Button } from '@/components/ui/button'
import type { WhatsAppPairingState } from '@/store/whatsappPairing'

// RetryableState: shared layout for the timeout, error and stopped cases — an
// icon + message line, the server's own detail when it adds one (e.g. the
// connect result "saved but not working: pairing timed out", BDD-8.5), and a
// Retry button when the caller offers one.
function RetryableState({
  icon: IconComponent,
  iconProps,
  message,
  detail,
  onRetry,
}: {
  icon: Icon
  iconProps?: React.ComponentProps<Icon>
  message: string
  detail?: string
  onRetry?: () => void
}) {
  const extra = detail && detail.trim() !== '' && detail.trim() !== message ? detail : undefined
  return (
    <div className="flex flex-col items-center gap-[var(--space-2-5)]">
      <div className={`flex items-center gap-[var(--space-2)] ${iconProps?.className ?? ''}`}>
        <IconComponent size={14} {...iconProps} className={undefined} />
        <p className="text-[length:var(--type-utility-xs-size)]">{message}</p>
      </div>
      {extra && (
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-muted)] text-center">{extra}</p>
      )}
      {onRetry && (
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={onRetry}
          data-testid="whatsapp-retry"
          className="gap-[var(--space-1)] text-[length:var(--type-utility-xs-size)]"
        >
          <ArrowsClockwise size={13} />
          Retry
        </Button>
      )}
    </div>
  )
}

// WhatsAppPairingBody renders the inner QR/status block for the linked-device
// notice (#283 / US-C3). Implements the full state machine by real wire names:
// waiting | code | linked | timeout | error | stopped (K10, BDD-8.8)
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
    <div className="flex items-center gap-[var(--space-2)] text-[var(--color-muted)]">
      <Spinner size={14} className="animate-spin" />
      <p className="text-[length:var(--type-utility-xs-size)]">Generating your QR code&hellip;</p>
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
        <div data-testid="whatsapp-qr" className="rounded-md bg-[var(--color-surface-paper)] p-[var(--space-2-5)]">
          <QRCodeSVG value={pairing.qr} size={184} level="L" title="WhatsApp pairing QR code" />
        </div>
        <p className="text-[length:var(--type-utility-xs-size)] text-[var(--color-secondary)] text-center">
          Open <span className="font-medium">WhatsApp</span> on your phone, go to{' '}
          <span className="font-medium">Settings &rarr; Linked Devices &rarr; Link a Device</span>,
          then scan this code. It refreshes every 20s.
        </p>
      </>
    )
  }

  // The generated frame has no teardown flag. This exact server message
  // means Stop is still running; only a later stopped frame permits Retry.
  const stopPending = pairing.status === 'error' && pairing.message === 'Stopping this pairing has not finished yet. Omnipus keeps trying; a new pairing can start once it has stopped.'

  switch (pairing.status) {
    case 'linked':
      return (
        <div className="flex items-center gap-[var(--space-2)] text-[var(--color-success)]">
          <CheckCircle size={16} weight="fill" />
          <p className="text-[length:var(--type-utility-xs-size)] font-medium">{pairing.message || 'Linked successfully.'}</p>
        </div>
      )
    case 'timeout':
      return (
        <RetryableState
          icon={Clock}
          iconProps={{ className: 'text-[var(--color-muted)]' }}
          message="QR expired — select to get a fresh one."
          detail={pairing.message}
          onRetry={onRetry}
        />
      )
    case 'error':
      return (
        <RetryableState
          icon={Warning}
          iconProps={{ weight: 'fill', className: 'text-[var(--color-error)]' }}
          message={stopPending ? pairing.message : 'Pairing failed — select to retry.'}
          detail={stopPending ? undefined : pairing.message}
          onRetry={stopPending ? undefined : onRetry}
        />
      )
    case 'stopped':
      // Terminal: Stop in the requesting chat ended the pairing (BDD-8.8).
      return (
        <RetryableState
          icon={StopCircle}
          iconProps={{ className: 'text-[var(--color-muted)]' }}
          message="Pairing stopped."
          detail={pairing.message}
          onRetry={onRetry}
        />
      )
    default:
      // Fallback for any unexpected status — show spinner
      return generatingSpinner
  }
}
