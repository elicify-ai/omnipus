# Provider messages (#711 and beyond) — founder decisions, 2026-09-26

Input: /Users/danielpiatkowski/AI-Agent-Workspace/research/provider-messages-research-2026-09-26.md (OpenCode vs Omnipus).

| ID | Decision (founder, verbatim first) |
|---|---|
| PM-1 | Direction A "facts instead of raw text" — "in principle A is correct but it can not be displayed as raw json, we need to assemble a nice human readable message from it, one line". Structured provider facts are assembled server-side or client-side into ONE plain-English line per error (examples: "OpenRouter is busy. Retrying automatically in 1:32 (attempt 2 of 5)." / "OpenRouter says your account is out of credit." / "OpenRouter rejected the API key. Check the key in Settings → Providers." / "OpenRouter no longer offers z-ai/glm-4. Choose another model for this agent."). |
| PM-2 | Facts that may be named in the line: provider name and model name. No key label ("the keys are not named, provider and model is sufficient"). Request ID only in Verbose chat. Key material or fragments: never. |
| PM-3 | Billing/quota gets its own message; the "Open billing page" button only "if we have this data already" (a known billing URL for that provider); otherwise the plain sentence without a button. |
| PM-4 | Retry countdown: yes. Fallback note ("Answered by X because Y was unavailable"): yes. |
| PM-5 | Context length: keep as is (automatic trimming of oldest turns, no note, no compaction). |
| PM-6 | Verbose chat: "facts plus via accordion open raw json, like we have it in many tool calls already" — the one-line message and facts, plus a collapsible accordion with the raw provider JSON, reusing the existing tool-call accordion pattern. Raw provider text must still not reach non-Verbose viewers (#711) — the delivery mechanism (per-tab Verbose signal vs other) is for the spec to settle and the founder to confirm. |
| PM-7 | #711 is handled by this design, not by the gateway-security squad's blunt "drop detail" commit (c13c4d279), which is removed from that branch. |
