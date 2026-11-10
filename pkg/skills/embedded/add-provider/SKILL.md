---
name: add-provider
description: Use when the user wants to add or change an AI provider or its key (W12).
metadata:
  display_name: Add or Change Provider (W12)
---
# Add or change an AI provider (W12)

## Prerequisites
Load `provider-config` first — it states what the provider tools can and cannot connect. `tool:list_providers` for current entries; the key only through `tool:request_secret`.

## Steps
1. Ask which provider. From `provider-config`: the tools connect API-key providers, keyless local providers and custom endpoints (`api_base`). Account sign-in, multi-field credentials (for example `amazon-bedrock`) and coding-plan variants are Settings → Providers only — say so plainly and stop the workflow there when that is what the user wants.
2. Use the exact catalogue id (`amazon-bedrock`, not `bedrock`; `google`, not `gemini`).
3. Have the user enter the key on the secret card — never in chat text; you receive the reference name. `configure_provider` does not validate the key at save time by itself; its own check after saving is what proves the endpoint.
4. Present one card: provider id, endpoint (custom when given), which models the user plans to use.
5. One Go / Change / Cancel question. Never call a change tool before Go.
6. After Go: `tool:configure_provider` with the reference — it checks the saved effective endpoint, credential and protocol itself.

## Expected output
The check's own verdict, with its scope: "working: model listing" also states that executing a chosen model was not tested (BDD-9.7). Keyless local providers check and report working without a key.

## Stop and handoff
Cancel stores nothing; a submitted key stays stored, unused, listed and removable. A dead custom endpoint with a valid catalogue key is "saved but not working" — never "working".
