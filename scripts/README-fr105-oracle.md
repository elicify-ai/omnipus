# FR-105 oracle

`fr105_oracle.py` answers one question about an imported view: **does it return
more rows than the original `.base` view did?** FR-105 is one-directional —
fewer rows with the loss named is acceptable, more rows never is.

## Why it is written in Python, outside the Go tree

Deliberately. The value of an oracle is that it is INDEPENDENT. A Go oracle in
this repo would reach for the same helpers, the same date parsing and the same
absent-value rules as the importer it is checking, and the two would then agree
while both being wrong. This one is written against `.base` semantics directly
and shares no code with `pkg/vaultimport`.

It **refuses rather than guesses**: any construct it does not fully understand
makes that view UNGRADEABLE and it says so. An oracle that quietly returned an
empty row set for a construct it did not understand would turn every broadened
view into a pass — the exact failure it exists to prevent.

## The clock is pinned

Half these views filter on `today()`. The clock is an argument, not a default,
because an expectation whose answer changes overnight cannot be committed.

    python3 scripts/fr105_oracle.py <vault-dir> <out.json> 2026-08-24

## The expectation file is NOT in this repo

The measurement that matters runs against the founder's real vault — 757 notes,
18 real `.base` files, and real dirt (17 distinct `PLACEHOLDER — …` strings sit
in date fields in Subscriptions alone). That is the whole point: the number has
to come from files somebody actually wrote, not from a fixture built to pass.

It also means the expected row sets contain real customer names, so **they are
not committed to this MIT-licensed repo** and neither is the vault. The
acceptance harness (`pkg/vaultimport/fixture_vault_test.go`) is env-gated for
that reason and SKIPS when the variable is unset.

A skipped test is green, so the CI-visible signal from that harness is nil. The
tests that must hold on every machine live beside it and use committed
fixtures. Treat the real-vault number as a release measurement someone runs and
reports, not as a gate CI enforces.

## Status at time of writing

69 of 69 views gradeable, clock pinned to 2026-08-24, against the 0-clean-of-18
baseline this work started from.
