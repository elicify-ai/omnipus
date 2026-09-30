# pkg/gitevidence — workspace Git evidence

What it owns: Scoped boundary commits, per-attempt diffs, integrity checks and the checkout-isolation ladder.
What it does not own: Shell access restrictions on `.git` or plan-state persistence.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestGitEvidence_Isolation_SelectRungReflectsSystemGitAvailability$' -v -p 1 ./pkg/gitevidence/`

## Pitfalls here

- The selected isolation rung must describe the capability actually available on the host. `isolation_test.go::TestGitEvidence_Isolation_SelectRungReflectsSystemGitAvailability` checks selection.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-053-unified-goal-plan-subagent.md` (workspace evidence and isolation).
