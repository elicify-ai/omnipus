# Report Format: Verification Confidence and the Output Contract

The exact templates the audit emits. `SKILL.md` states the rules; this file
holds the formats. Emit these verbatim — the caller may be another agent and
may parse the output.

## Verification Confidence block

Never report `Tests: GREEN` as the headline. Report this instead:

```
Verification Confidence
──────────────────────────────────
Original suite            1,428 / 1,428
Independent probes            187 / 190
Mutation score (changed code)      84%
Changed-code branch coverage       92%

Test integrity changes
  assertions weakened                0
  tests deleted                      0
  tests skipped / xfailed            0
  warning suppressions added         0
  tolerances widened                 0
  coverage exclusions added          0

Execution integrity
  full suite run                   YES
  collection count stable          YES
  tree changed after run            NO
  exit code intercepted             NO
  warnings increased                NO

Oracle independence
  expectations derived from spec   YES / NO / PARTIAL
```

Any line you could not establish is reported as `UNVERIFIED`, never as a
favourable default. `UNVERIFIED` caps confidence; it does not pass.

## Output contract

Return exactly this structure:

```
== TEST INTEGRITY AUDIT ==
verdict            : PASS | WARN | BLOCK
weakening score    : <int>  (threshold: BLOCK ≥ 50, or any single item ≥ 100)
scope              : <diff base...HEAD | suite | targeted paths>
tree               : <sha>   dirty: <yes/no>
mode               : AUDIT | TRIAGE | GATE | REMEDIATE
status             : COMPLETE | DEGRADED | BLOCKED

── VERIFICATION CONFIDENCE ──
<the block above, with UNVERIFIED where not established>

── FINDINGS ──  (ranked by risk, highest first)
[<RISK>] <CLASS> — <one-line claim>
  where    : <file>:<line>
  evidence : <the actual matched text, diff hunk, or command output>
  why      : <what this specific test would now fail to catch>
  fix      : <the strengthening move, concretely>

── ORACLE POWER ──
tests with zero assertions : <n> / <total>
surviving mutants          : <n>  (list each with the test that should have caught it)
oracle independence        : ESTABLISHED | NOT ESTABLISHED | PARTIAL — <why>

── WHAT I COULD NOT VERIFY ──
<explicit list; never silently omitted>

── RECOMMENDATION ──
<2–4 sentences. Lead with the consequence, not the mechanism.>
```

## Reporting rules

- **Rank by risk, not by file order.** The reader may stop after three items.
- **Every finding carries evidence.** No evidence, no finding — move it to the
  "could not verify" section instead of asserting it.
- **State what the test would fail to catch**, concretely. "Weak assertion" is
  useless; "would pass if the discount rate changed to 0.8, or to any non-null
  value" is actionable.
- **The `could not verify` section is mandatory** and is never empty by
  convenience. Silence about a gap is a false negative.
- Write for a technically literate reader who does not read code for a living:
  consequence first, mechanism second.
