# pkg/voice — speech transcription adapters

What it owns: Audio-to-text provider adapters and their shared multipart upload flow.
What it does not own: Microphone capture, chat UI or audio storage.

## Run its tests

`CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestGroqTranscribe$' -v -p 1 ./pkg/voice/`

## Pitfalls here

- Multipart requests and provider errors must retain the provider's expected shape. `groq_transcriber_test.go::TestGroqTranscribe` covers one adapter's request/response path.

## Never bring back

— none known

## Where decisions live

— none known
