# pkg/security — security checks shared by runtime callers

What it owns: Prompt-injection handling, network target checks, approval grants and local rate limits.
What it does not own: Tool-policy composition or kernel filesystem confinement.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestSSRFChecker_PrivateIPv4Ranges$' -v -p 1 ./pkg/security/`

## Pitfalls here

- Requests to private IPv4 ranges must not pass the server-side request-forgery check. `ssrf_test.go::TestSSRFChecker_PrivateIPv4Ranges` covers these ranges.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-057-session-parent-child-parity.md` (approval-grant inheritance); root `CLAUDE.md` (security constraints).
