# pkg/onboarding — first-run state

What it owns: First-run onboarding state and its completion/resume decisions.
What it does not own: The setup screen or provider configuration persistence.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestOnboardingNeverReshow$' -v -p 1 ./pkg/onboarding/`

## Pitfalls here

- Completed onboarding must not reappear on later starts. `onboarding_test.go::TestOnboardingNeverReshow` checks the persisted completion path.

## Never bring back

— none known

## Where decisions live

— none known
