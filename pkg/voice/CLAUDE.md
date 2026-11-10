# pkg/voice — speech transcription adapters

What it owns: Audio-to-text provider adapters and their shared multipart upload flow.
What it does not own: Microphone capture, chat UI or audio storage.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestGroqTranscribe$' -v -p 1 ./pkg/voice/`

## Pitfalls here

- Multipart fields and provider-error detail matter. `groq_transcriber_test.go::TestGroqTranscribe` checks endpoint/authentication, decoded success and error presence, but does not inspect upload fields or error details. Do not treat its green result as a request-shape check.

## Never bring back

— none known

## Where decisions live

— none known
