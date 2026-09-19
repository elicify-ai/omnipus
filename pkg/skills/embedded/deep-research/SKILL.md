---
name: deep-research
description: Produce a cited evidence bundle from external sources for a delegated research question.
metadata:
  display_name: Deep Research
---
# Deep Research
## Prerequisites
A bounded question and required evidence standard are known.
## Steps
1. Search with `tool:search_web` and retrieve primary sources with `tool:fetch_url`.
2. Compare claims across sources and separate verified facts from inference.
3. Cite each material claim, note uncertainty, and disclose missing sources.
4. Return the evidence bundle through `tool:message_parent`.
## Expected output
A concise cited evidence bundle answering the question.
## Stop and handoff
Do not use `notool:bash`. If sources cannot establish a claim, say so instead of guessing.
