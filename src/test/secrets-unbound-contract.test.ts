// secrets-unbound-contract.test.ts — WU-6 K6 kind ruling, oracle O10's zod
// half (qa-lead, train2-red).
//
// SPEC SOURCES (expected values derive from these, never from the
// implementation):
//   - The architect ruling
//     coordination/designs/WU6-K6-SECRET-KIND-RULING-20261018.md §2.4 #1/#2:
//     SecretNameEntry.kind and SecretRecipientDisplay.kind each gain the
//     enum value "unbound"; requiredness is UNCHANGED — recipient stays
//     REQUIRED, so a provider_key entry with no recipient must still be
//     rejected (§2.6: "requiredness intact").
//   - An unbound entry's shape per ruling §3.2 rule 1: kind "unbound",
//     recipient {kind: "unbound", lines: ["Stored in Settings; no recipient
//     recorded"]} (exactly one line), version 1, no owner, no
//     available_in_channel_turns, consumers [].
//
// RED at a1536fba0: the generated zod schema's kind enums do not contain
// "unbound" yet, so the accept leg fails. The reject leg (requiredness) is
// GREEN by design and stays as the requiredness pin across the
// regeneration. The Go-side half of O10 lives in
// pkg/gateway/wu6_k6_unbound_listing_test.go::TestWU6K6_Contract_UnboundEnumValues.

import { describe, expect, it } from 'vitest'
import { SecretNameEntry } from '@/lib/api/generated/schemas'

const UNBOUND_ENTRY = {
  reference: 'SETTINGS_STORED_TOKEN',
  kind: 'unbound',
  version: 1,
  recipient: {
    kind: 'unbound',
    lines: ['Stored in Settings; no recipient recorded'],
  },
  consumers: [],
  state: 'stored',
}

describe('SecretNameEntry — the K6 kind ruling widens the enum to "unbound"', () => {
  it('accepts an unbound entry with the single fixed recipient line', () => {
    // RED until the contract change regenerates: "unbound" is outside the
    // current five-value enum.
    expect(() => SecretNameEntry.parse(UNBOUND_ENTRY)).not.toThrow()
  })

  it('still requires recipient for a known kind — requiredness intact', () => {
    // GREEN by design (§2.4: no requiredness change); this is the pin that
    // must SURVIVE the regeneration. The restore leg proves the rejection is
    // the MISSING recipient and nothing else about the fixture: the same
    // body with the recipient restored parses (the recipient schema has no
    // per-kind refinement, so the unbound-shaped recipient is itself valid
    // on a provider_key entry).
    const { recipient, ...noRecipient } = UNBOUND_ENTRY
    const providerKeyBody = { ...noRecipient, kind: 'provider_key' }
    expect(() => SecretNameEntry.parse(providerKeyBody)).toThrow()
    expect(() =>
      SecretNameEntry.parse({ ...providerKeyBody, recipient }),
    ).not.toThrow()
  })

  it('accepts the unbound kind with the fixed single line — lines non-empty per minItems', () => {
    // §2.4 #2: "lines are exactly the single fixed line (satisfies
    // minItems: 1)". An unbound entry with NO lines must still be rejected:
    // the widening changes the enum, never the requiredness of lines.
    expect(() =>
      SecretNameEntry.parse({
        ...UNBOUND_ENTRY,
        recipient: { kind: 'unbound', lines: [] },
      }),
    ).toThrow()
  })
})
