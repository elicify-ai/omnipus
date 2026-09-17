---
name: provider-setup
description: Configure a model provider and verify it through the real model catalog.
---
# Provider Setup
## Prerequisites
Know the provider and approved credential reference.
## Steps
1. Inspect current providers with `tool:list_providers`.
2. Apply configuration with `tool:configure_provider`.
3. Probe with `tool:test_provider` and `tool:list_models` using real catalog results.
## Expected output
A usable provider and actual model catalog, or an accurate failure.
## Stop and handoff
Credential presence alone is not verification; never print a secret.
