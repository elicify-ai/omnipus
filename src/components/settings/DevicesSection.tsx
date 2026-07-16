// DevicesSection — admin-only device pairing management panel
// Shipped, feature-flag-gated: the "Devices" tab only mounts this component when
// isDevicePairingEnabled() is true (see SettingsScreen.tsx). The approve/reject
// flow below is real and wired to the live /devices endpoint + respondToPairing.
//
// Traces to: wave3-skill-ecosystem-spec.md line 846 (Test #16: RBAC — admin-only REST endpoints)

import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { DeviceMobile, CheckCircle, XCircle, Trash, Clock, Fingerprint, Info, ArrowRight } from '@phosphor-icons/react'
import { Button } from '@/components/ui/button'
import { fetchDevices, type DevicePending, type DevicePaired } from '@/lib/api'
import { useChatStore } from '@/store/chat'
import { useConnectionStore } from '@/store/connection'
import { useUiStore } from '@/store/ui'

// ── PairDeviceInstructions — help panel shown when "Pair a device" is clicked ──

function PairDeviceInstructions({ onClose }: { onClose: () => void }) {
  return (
    <div
      data-testid="pair-device-instructions"
      className="rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-4 space-y-3"
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <DeviceMobile size={15} className="text-[var(--color-accent)] shrink-0" />
          <p className="text-sm font-semibold text-[var(--color-secondary)]">Pairing a device</p>
        </div>
        <button tabIndex={0}
          type="button"
          onClick={onClose}
          className="text-xs text-[var(--color-muted)] hover:text-[var(--color-secondary)] transition-colors"
          aria-label="Close pairing instructions"
        >
          Close
        </button>
      </div>
      <ol className="space-y-2 text-xs text-[var(--color-muted)] list-decimal list-inside">
        <li>Open the Omnipus app on the device you want to pair.</li>
        <li>Go to <span className="font-semibold text-[var(--color-secondary)]">Settings → Connect to gateway</span> and enter this gateway&apos;s URL.</li>
        <li>The device will request pairing and appear in the <span className="font-semibold text-[var(--color-secondary)]">Pending Requests</span> list below.</li>
        <li>Verify the 6-digit code shown on both devices, then click <span className="font-semibold text-[var(--color-secondary)]">Approve</span>.</li>
      </ol>
      <p className="text-[11px] text-[var(--color-muted)]">
        Once approved, the device can connect to your gateway as a linked client — like Linked Devices on messaging apps.
      </p>
      <a tabIndex={0}
        href="https://omnipus.ai/docs/device-pairing"
        target="_blank"
        rel="noopener noreferrer"
        className="inline-flex items-center gap-1 text-xs text-[var(--color-accent)] hover:opacity-80 transition-opacity"
        data-testid="pair-device-docs-link"
      >
        Learn more <ArrowRight size={11} />
      </a>
    </div>
  )
}

export function DevicesSection() {
  const { addToast } = useUiStore()
  const queryClient = useQueryClient()
  const respondToPairing = useChatStore((s) => s.respondToPairing)
  const isConnected = useConnectionStore((s) => s.isConnected)
  const [showPairInstructions, setShowPairInstructions] = useState(false)

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: ['devices'],
    queryFn: fetchDevices,
    retry: false,
    refetchInterval: 5000, // poll for new pending requests while panel is open
  })

  const handleApprove = (deviceId: string) => {
    if (!isConnected) {
      addToast({ message: 'Not connected to gateway. Reconnect and try again.', variant: 'error' })
      return
    }
    respondToPairing(deviceId, 'approve')
    addToast({ message: 'Device approved.', variant: 'success' })
    queryClient.invalidateQueries({ queryKey: ['devices'] })
  }

  const handleReject = (deviceId: string) => {
    if (!isConnected) {
      addToast({ message: 'Not connected to gateway. Reconnect and try again.', variant: 'error' })
      return
    }
    respondToPairing(deviceId, 'reject')
    addToast({ message: 'Device rejected.', variant: 'success' })
    queryClient.invalidateQueries({ queryKey: ['devices'] })
  }

  const pending: DevicePending[] = data?.pending ?? []
  const paired: DevicePaired[] = data?.paired ?? []

  return (
    <section className="space-y-6">
      {/* Explainer + Pair entry point (UAT fix #3) */}
      <div
        className="flex items-start gap-3 rounded-lg border border-[var(--color-border)] bg-[var(--color-surface-2)] p-4"
        data-testid="devices-explainer"
      >
        <Info size={15} className="text-[var(--color-accent)] shrink-0 mt-0.5" />
        <div className="flex-1 min-w-0 space-y-2">
          <p className="text-sm text-[var(--color-secondary)]">
            Approve other devices or clients to connect to your gateway — similar to Linked Devices on messaging apps.
          </p>
          <p className="text-xs text-[var(--color-muted)]">
            Each paired device can access Omnipus with the permissions granted during approval. Revoke at any time.
          </p>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setShowPairInstructions((v) => !v)}
            className="gap-1.5 text-xs"
            data-testid="pair-device-btn"
          >
            <DeviceMobile size={13} />
            Pair a device
          </Button>
        </div>
      </div>

      {/* Pairing instructions panel — shown when "Pair a device" is clicked */}
      {showPairInstructions && (
        <PairDeviceInstructions onClose={() => setShowPairInstructions(false)} />
      )}

      {/* Pending Requests */}
      <div>
        <h3 className="text-xs font-semibold uppercase tracking-wider" style={{ color: 'var(--color-muted)' }}>
          Pending Requests
        </h3>
        <p className="text-xs mt-0.5" style={{ color: 'var(--color-muted)' }}>
          New devices awaiting admin approval. Verify the 6-digit code shown on the device before approving.
        </p>
      </div>

      {isLoading ? (
        <div className="h-24 rounded-lg border animate-pulse" style={{ borderColor: 'var(--color-border)', backgroundColor: 'var(--color-surface-1)' }} />
      ) : isError ? (
        <div className="flex flex-col items-center justify-center gap-3 py-6 rounded-lg border border-dashed text-center" style={{ borderColor: 'var(--color-border)' }}>
          <DeviceMobile size={22} weight="duotone" style={{ color: 'var(--color-error)' }} />
          <div>
            <p className="text-xs font-medium" style={{ color: 'var(--color-error)' }}>Failed to load devices</p>
            <p className="text-xs mt-0.5" style={{ color: 'var(--color-muted)' }}>Could not reach the gateway to list pending and paired devices.</p>
          </div>
          <Button size="sm" variant="outline" onClick={() => refetch()} data-testid="devices-retry-btn">
            Retry
          </Button>
        </div>
      ) : pending.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-3 py-6 rounded-lg border border-dashed text-center" style={{ borderColor: 'var(--color-border)' }}>
          <Clock size={22} weight="duotone" style={{ color: 'var(--color-muted)' }} />
          <p className="text-xs" style={{ color: 'var(--color-muted)' }}>No pending requests</p>
        </div>
      ) : (
        <div className="space-y-2">
          {pending.map((req) => (
            <div key={req.device_id} className="p-3 rounded-lg border space-y-2" style={{ borderColor: 'var(--color-border)', backgroundColor: 'var(--color-surface-1)' }}>
              <div className="flex items-center justify-between">
                <div className="flex items-center gap-2">
                  <DeviceMobile size={16} style={{ color: 'var(--color-secondary)' }} />
                  <span className="text-sm font-medium" style={{ color: 'var(--color-secondary)' }}>{req.device_name}</span>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2"
                    onClick={() => handleReject(req.device_id)}
                    title="Reject"
                  >
                    <XCircle size={14} weight="fill" style={{ color: 'var(--color-error)' }} />
                  </Button>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 px-2"
                    onClick={() => handleApprove(req.device_id)}
                    title="Approve"
                  >
                    <CheckCircle size={14} weight="fill" style={{ color: 'var(--color-success)' }} />
                  </Button>
                </div>
              </div>
              <div className="flex items-center gap-4 text-[10px]" style={{ color: 'var(--color-muted)' }}>
                <span className="flex items-center gap-1">
                  <Fingerprint size={10} />
                  {req.fingerprint.slice(0, 12)}…
                </span>
                <span>Code: <span className="font-mono font-semibold" style={{ color: 'var(--forge-gold)' }}>{req.pairing_code}</span></span>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Paired Devices */}
      <div className="pt-2">
        <h3 className="text-xs font-semibold uppercase tracking-wider" style={{ color: 'var(--color-muted)' }}>
          Paired Devices
        </h3>
        <p className="text-xs mt-0.5" style={{ color: 'var(--color-muted)' }}>
          Devices that have been approved to access your Omnipus agent.
        </p>
      </div>

      {paired.length === 0 ? (
        <div className="flex flex-col items-center justify-center gap-3 py-8 rounded-lg border border-dashed text-center" style={{ borderColor: 'var(--color-border)' }}>
          <DeviceMobile size={28} weight="duotone" style={{ color: 'var(--color-muted)' }} />
          <div>
            <p className="text-sm" style={{ color: 'var(--color-secondary)' }}>No paired devices</p>
            <p className="text-xs mt-0.5" style={{ color: 'var(--color-muted)' }}>Approved devices will appear here.</p>
          </div>
        </div>
      ) : (
        <div className="space-y-2">
          {paired.map((dev) => (
            <div key={dev.device_id} className="flex items-center justify-between p-3 rounded-lg border" style={{ borderColor: 'var(--color-border)', backgroundColor: 'var(--color-surface-1)' }}>
              <div className="flex items-center gap-3">
                <DeviceMobile size={18} style={{ color: dev.status === 'active' ? 'var(--color-secondary)' : 'var(--color-muted)' }} />
                <div>
                  <p className="text-sm font-medium" style={{ color: dev.status === 'active' ? 'var(--color-secondary)' : 'var(--color-muted)' }}>{dev.device_name}</p>
                  <p className="text-[10px]" style={{ color: 'var(--color-muted)' }}>
                    {dev.status === 'active' ? `Last seen ${new Date(dev.last_seen_at).toLocaleDateString()}` : `Revoked ${new Date(dev.last_seen_at).toLocaleDateString()}`}
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-2">
                {dev.status === 'active' ? (
                  <CheckCircle size={14} weight="fill" style={{ color: 'var(--color-success)' }} />
                ) : (
                  <Trash size={14} style={{ color: 'var(--color-muted)' }} />
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  )
}
