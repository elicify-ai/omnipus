# pkg/auth — provider authentication exchanges

What it owns: OAuth device/refresh exchanges, provider endpoint validation and in-memory credential records.
What it does not own: On-disk secret persistence; `pkg/credentials` holds encrypted credentials.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestValidateOAuthEndpoint$' -v -p 1 ./pkg/auth/`

## Pitfalls here

- OAuth endpoint validation is a security boundary. `oauth_endpoint_validation_test.go::TestValidateOAuthEndpoint` covers rejected destinations.

## Never bring back

- The retired plaintext `auth.json` credential writer (noted in `store.go::AuthCredential`); no dedicated guard is known.

## Where decisions live

`docs/internal/architecture/ADR-004-credential-boot-contract.md` (credential boot contract).
