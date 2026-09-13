import type { BrowserInputFrame } from '@/lib/api/generated/asyncapi-types'

/** Component-test transport boundary: represents an already negotiated input
 * peer. Frame authorization and UI behavior stay real. Negotiation, channel
 * admission and recovery are covered by the real dedicatedInput suite. */
export function dedicatedInputSessionStub(send: (input: Omit<BrowserInputFrame, 'type'>) => boolean) {
  return class {
    state = 'ready'
    controlIdentity = { input_epoch: 1, control_epoch: 0 }
    needsAttachmentRetry = false
    start() { this.state = 'ready' }
    stop() { this.state = 'idle' }
    resetAttachment() { this.state = 'ready' }
    setICEServers() {}
    applyAnswer() { return true }
    applyState() {}
    applyControlAck() { return true }
    beginControl() { return ++this.controlIdentity.control_epoch }
    sendInput(input: Omit<BrowserInputFrame, 'type'>) { return this.state === 'ready' && send(input) }
  }
}
