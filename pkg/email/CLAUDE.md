# pkg/email — mailbox transport

What it owns: Pure-Go inbound IMAP and outbound SMTP transport and message-body decoding.
What it does not own: Conversational connector routing; email is an agent tool, not a chat channel.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestDecodeBody_MultipartAlternativePrefersPlain$' -v -p 1 ./pkg/email/`

## Pitfalls here

- For a multipart alternative, prefer readable plain text over an HTML part. `decode_test.go::TestDecodeBody_MultipartAlternativePrefersPlain` checks decoding.

## Never bring back

— none known

## Where decisions live

`docs/internal/architecture/ADR-033-per-pair-mailboxes.md` (per-pair mailboxes).
