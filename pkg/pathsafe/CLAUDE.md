# pkg/pathsafe — path-component validation

What it owns: Cross-platform rules for accepting filesystem path components.
What it does not own: Filesystem scope or sandbox confinement; callers enforce those after validation.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestRuleSet_ControlCharsRejectedUnderEverySet$' -v -p 1 ./pkg/pathsafe/`

## Pitfalls here

- Control characters must be rejected under every supported rule set, not only the host platform's. `ruleset_value_test.go::TestRuleSet_ControlCharsRejectedUnderEverySet` checks all sets.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-063-unified-file-access-engine-and-mounts.md` (file access and mounts).
