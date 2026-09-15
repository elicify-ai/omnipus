import { describe, expect, it } from 'vitest';
import { decodeAudioPressureWords } from './audio-pressure-probe';

// Fixed literals come from the fixture's four-word contract, not its encoder.
// In particular, 0x001AA531 is magic A531 + phase 2 + context 2 + started.
describe('audio stimulus pixel words', () => {
  it('decodes the tone phase and both independent clocks exactly', () => {
    expect(decodeAudioPressureWords([0x001aa531, 720001, 240001, 14142])).toEqual({
      phase: 2, contextState: 2, started: true, fault: false,
      elapsedMs: 720001, contextMs: 240001, analyserRMS: 0.014142,
    });
  });

  it.each([
    [0x0000a531, 0, 0, false, false],
    [0x0015a531, 1, 1, true, false],
    [0x001fa531, 3, 3, true, false],
    [0x003aa531, 2, 2, true, true],
  ] as const)('keeps phase/context/started/fault bits independent for %i', (header, phase, contextState, started, fault) => {
    expect(decodeAudioPressureWords([header, 0, 0, 0])).toEqual({
      phase, contextState, started, fault, elapsedMs: 0, contextMs: 0, analyserRMS: 0,
    });
  });

  it.each([0x001aa530, 0x005aa531, 0x801aa531])('rejects wrong magic or reserved bits in %i', header => {
    expect(() => decodeAudioPressureWords([header, 0, 0, 0])).toThrow('Invalid audio stimulus signature or reserved bits');
  });

  it.each([
    [], [0xa531, 0, 0], [0xa531, 0, 0, 0, 0],
    [0xa531, -1, 0, 0], [0xa531, 1.5, 0, 0],
    [0xa531, 0x100000000, 0, 0], [0xa531, Number.NaN, 0, 0],
    [0xa531, Number.POSITIVE_INFINITY, 0, 0],
  ])('rejects a malformed four-word packet %j', (...words) => {
    expect(() => decodeAudioPressureWords(words)).toThrow('Four unsigned stimulus words required');
  });

  it('accepts the full unsigned range for clocks and scaled measurement', () => {
    expect(decodeAudioPressureWords([0xa531, 0xffffffff, 0xffffffff, 0xffffffff])).toEqual({
      phase: 0, contextState: 0, started: false, fault: false,
      elapsedMs: 4294967295, contextMs: 4294967295, analyserRMS: 4294.967295,
    });
  });
});
