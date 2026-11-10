# pkg/policy — security policy configuration

What it owns: Prompt-guard strictness settings, system-agent classification and approval-saturation limits.
What it does not own: Command execution decisions; those live in the tool compositor and shell/agent runtime.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestSaturationGuard_NegativeCapRejected$' -v -p 1 ./pkg/policy/`

## Pitfalls here

- A negative approval cap is invalid; `saturation_test.go::TestSaturationGuard_NegativeCapRejected` checks this boundary.

## Never bring back

- The retired per-binary exec allowlist; see `scripts/check-no-shell-deny-patterns.sh` and root `CLAUDE.md` (Retired surfaces).

## Where decisions live

`docs/internal/architecture/ADR-092-shell-permission-modes.md` (live shell permission modes).
