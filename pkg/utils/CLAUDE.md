# pkg/utils — shared utility helpers

What it owns: Shared HTTP, skill-name, media and scoring helpers used across package boundaries.
What it does not own: Product policy or module-specific storage.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestValidateSkillIdentifier_DestructiveIdentifiersRefused$' -v -p 1 ./pkg/utils/`

## Pitfalls here

- Skill identifiers can address filesystem paths; reject destructive shapes before callers use them. `skills_test.go::TestValidateSkillIdentifier_DestructiveIdentifiersRefused` checks this case.

## Never bring back

— none known

## Where decisions live

— none known
