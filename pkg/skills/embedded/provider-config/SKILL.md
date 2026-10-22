---
name: provider-config
description: Use when the person asks about connecting an AI provider or asks what the provider tools can and cannot connect — it states the API-key-only limit, the exact catalogue ids, and when to send them to Settings instead.
metadata:
  display_name: Provider Config
---
# Provider configuration — capabilities and limits

## What the tools can connect
The provider tools take exactly one credential shape: **one API key in, provider out** (plus an optional `api_base`).

| Connection type | Through the tools | Notes |
|---|---|---|
| API-key cloud providers (OpenAI API, Google, OpenRouter, xAI) | works | `tool:configure_provider` with the key as a reference |
| Keyless local providers (Ollama, LM Studio, vLLM) | works | the check runs without a key |
| Custom endpoint | works | set `api_base`; the check targets **that** endpoint |
| Account sign-in (for example `openai-chatgpt`) | **not supported** — Settings → Providers | say so plainly; do not attempt it |
| Multi-field credentials (for example `amazon-bedrock`: access key + secret + region) | **not supported** — Settings → Providers | never store a partial credential |
| Coding-plan variants | **not supported** — Settings → Providers | no variant concept in the tools |

## Rules
- Use exact catalogue ids: `amazon-bedrock` (not `bedrock`), `google` (not `gemini`).
- Saving a key does not prove it: `tool:configure_provider`'s own check (the saved effective endpoint, credential and protocol, via a model listing) is what reports "working" — and that proves the listing, not that a chosen model executes (say so when it matters). `tool:test_provider` is a presence check only — never claim connectivity from it. `tool:list_models` after a save confirms what the endpoint answers.
- A wrong key can be stored silently; the check afterwards is where it fails — report it as "saved but not working", never retry a different key without a new summary and Go.

## Handoff
When the user wants sign-in, a coding plan, or a multi-field provider: name the limit, point them to Settings → Providers, and stop the provider workflow there. Settings keeps the manual forms; that is by design, not a failure.
