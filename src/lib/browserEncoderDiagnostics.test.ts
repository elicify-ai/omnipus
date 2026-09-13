import { readFileSync } from 'node:fs'
import { expect, it } from 'vitest'

it('extracts numeric encoder/network counters without copying identities or invalid readings', async () => {
  const source = readFileSync('pkg/tools/browser/captureext/embedded/encoder.js', 'utf8')
  const body = source.slice(source.indexOf('const previousSenderSamples ='), source.indexOf('async function applyAdaptScale'))
  const read = new Function(`${body}; return readVideoSenderSample`)() as (pc: unknown) => Promise<{ timing: Record<string, number> }>
  const report = new Map([['video', { type: 'outbound-rtp', kind: 'video', timestamp: 2000, framesEncoded: 60, totalEncodeTime: .3, totalPacketSendDelay: .5, packetsSent: 120, bytesSent: 4000, retransmittedPacketsSent: 2, retransmittedBytesSent: Infinity, ssrc: 99, remoteId: 'secret' }]])
  const sample = await read({ getSenders: () => [{ track: { kind: 'video' }, getStats: async () => report }] })
  expect(sample.timing).toEqual({ totalEncodeTime: .3, totalPacketSendDelay: .5, packetsSent: 120, bytesSent: 4000, framesEncoded: 60, retransmittedPacketsSent: 2 })
})
