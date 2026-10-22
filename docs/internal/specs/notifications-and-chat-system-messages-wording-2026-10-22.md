# Notifications and chat system messages — agreed wording baseline

**Status:** Requirements appendix (Draft) to `notifications-and-chat-system-messages-requirements-2026-10-22.md` (R-WD-9)
**Date:** 2026-10-22
**Source:** founder message review (`messages-review.json`, saved 2026-10-22 11:34Z) with the voice rule R-WD-5…8 (D47–D50) applied by team-lead. Full data set (392 messages, with today's text and every proposal): `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/notification-review-20261022/revised/revised.json`.

Placeholders in `{braces}` are dynamic parts. `{provider_says}` is the provider's own message, quoted (R-WD-2). Basis: *your edit* = founder's own text; *proposal* = team-lead proposal, rated or assumed agreed (D49); *voice rule* = rewritten under D47.

## 1. ADR-051 provider and model errors (all 29)

| Code | Final wording (in chat) | Basis | Note |
|---|---|---|---|
| `media_unsupported` | {model} can't read this attachment. {provider} said: "{provider_says}". Send the file in another format, or switch to a model that reads it. | proposal (rated) | Founder: “looks fine” |
| `provider_rejected` | {model} declined to answer: "{provider_says}". Rephrase the request, or switch model. | proposal (rated) |  |
| `request_too_large` | The request was too large for {model}. {provider} said: "{provider_says}". Shorten the message or attachments, or switch to a model with a larger limit. | voice rule + your edit | Your edit; dangling dash tidied. |
| `provider_auth_failed` | {provider} rejected the API key: "{provider_says}". Update the key in Settings → Providers. | proposal (rated) |  |
| `rate_limited` | {provider} is limiting requests: "{provider_says}". Retry in a moment, or switch model. | proposal (rated) |  |
| `quota_billing` | {provider} says your account is out of credit: "{provider_says}". Top up your {provider} account, or switch to another provider. | proposal (rated) |  |
| `network` | SPLIT INTO TWO: (a) network — "I can't reach {provider}: {reason, e.g. connection refused / no such host / timed out}. Retrying in {n} s…"  (b) API — "{provider} answered with an error ({status}): "{provider_says}". Retry, or switch model if it repeats." | voice rule | "Omnipus can't reach" → "I can't reach". |
| `provider_stalled` | {provider} stopped sending data for {minutes} minutes, so I ended the call. Retry; if it happens again, try another model. | voice rule | "Omnipus ended" → "I ended". |
| `content_policy` | {provider} blocked this request under its content policy: "{provider_says}". Rephrase the request. | proposal (rated) |  |
| `context_too_long` | This conversation is too long for {model}, even after I shortened older turns. {provider} said: "{provider_says}". Start a new chat, shorten the message, or switch to a model with a larger context. | voice rule | "Omnipus trimmed" → "I shortened". |
| `tool_args` | {model} sent invalid input to the tool {tool}: {reason}. Retry — if it repeats, try another model. | proposal (rated) | is this not covvered in the tools error handling ? |
| `tool_call_truncated` | {model} ran out of output space while writing a call to {tool}, so the call was cut off. Ask for the work in smaller steps. | proposal (rated) |  |
| `schema` | {provider} couldn't process the request — that is a bug in the software, not your input. {provider} said: "{provider_says}". Retry; if it repeats, please report it to the Omnipus.ai team. | voice rule | Neutral: the agent did not cause the bug. Report line copied from your provider_request_rejected edit. |
| `agent_not_configured` | {agent} isn't on any workspace team yet, so it has nowhere to work. Add it to a workspace team. | proposal (rated) | is this case realy possible in the current version? |
| `workspace_unavailable` | I couldn't open my working folder ({path}): {reason}. Check disk space and folder permissions. | voice rule | Outside the chat (e.g. a task-failed notification): "{agent}'s working folder ({path}) could not be opened: {reason}." |
| `model_unavailable` | {model_requested} isn't available right now, so this reply used {model_used}. Check the model in the agent's settings. | proposal (rated) |  |
| `model_retired` | {provider} no longer offers {model}: "{provider_says}". Pick a new model in the agent's settings. | proposal (rated) |  |
| `needs_provider` | Your sign-in to {provider} expired. Sign in again under Settings → Providers. | proposal (rated) |  |
| `model_unassigned` | {agent} has no model. Pick one in the agent's settings. | proposal (rated) |  |
| `turn_canceled` | You stopped this reply.  (shutdown variant: "This reply stopped because the server shut down.") | voice rule | "Omnipus shut down" → "the server shut down". |
| `turn_timed_out` | {provider} didn't answer within {seconds} s, so I ended the call. Retry; if it repeats, try another model. | voice rule | "Omnipus ended" → "I ended". |
| `judge_time_limit` | The Judge ran out of time ({seconds} s) before it finished checking this work. Raise the Judge time limit in Settings, then check again.  (Today the message tells you to edit config.json by hand.) | proposal (rated) |  |
| `judge_conclusion_failed` | The Judge's conclusion couldn't be saved: {reason}. Reload the chat; if it repeats, check disk space. | voice rule | Neutral (the Judge is not the agent you talk to). |
| `delegated_task_limit` | The delegated task "{task}" stopped at its {limit_name} limit ({value}). Open the task to raise the limit or continue. | proposal (rated) |  |
| `context_window_unknown` | {provider} didn't report how much context {model} can hold. Set it under Settings → Models → Context length. | proposal (rated) |  |
| `bad_model` | {provider} doesn't offer the model {model}: "{provider_says}". Pick a different model in the agent's settings.  (Drops the internal word "(bad_model)" from the text.) | proposal (rated) |  |
| `provider_request_rejected` | {provider} rejected the request for {model}: "{provider_says}". Retry; if it repeats, please report it to the Omnipus.ai team. | voice rule + your edit | Your edit; stray space and dash tidied. |
| `model_route_unavailable` | I couldn't connect to {provider} with its saved settings, so nothing was sent: {reason}. Check the API key and address in Settings → Providers. | voice rule | "Omnipus couldn't connect" → "I couldn't connect". |
| `unknown` | This reply failed with an error. {provider} said: "{provider_says}". Retry, or switch model if it repeats. | voice rule + your edit | Your edit; full stop added after "error". |

## 2. Other messages changed by the voice rule or a founder edit

| ID | Where | Today | Final wording | Note |
|---|---|---|---|---|
| BE-001 | In the chat, as an error line on that conversation. | This turn finished, but saving the conversation's final state failed. Repair the gateway storage, then continue: {detail} | This reply was sent, but the chat's final state couldn't be saved. Try sending another message. If this keeps happening, free disk space or check that the data folder is writable. | Neutral: storage, not the agent. |
| BE-003 | In the chat, as the failed turn's reply. | The external CLI failed. See gateway.log for details. | The external agent failed, and the cause couldn't be identified. Try the run again. If it keeps failing, check your external agent setup and provider. | Your edit, with "Omnipus couldn't classify the error" made neutral. |
| BE-006 | In the chat, as the command reply. | Goal clear deferred (the goal record could not be transitioned — will retry on next turn). | Goal could not be cleared — will retry on the next turn. | Your edit; stray bracket removed. |
| BE-008 | In the chat, as the command reply. | The goal could not be compiled (no reason given). Please restate it. | The goal could not be compiled. Please restate it. | Your edit, unchanged. |
| BE-016 | In the chat, as a system line and as the turn's reply. | Goal state could not be read; {text} deferred: {detail} | I couldn't read this chat's goal, so I didn't continue. Try again. If it keeps failing, the goal file may be unreadable. | "Omnipus" → "I". |
| BE-017 | In the chat, as a system line. | A helper event could not be saved in this chat. Omnipus will retry. This is not a failure of the helper's work. | A background worker's update couldn't be saved in this chat. It will be retried. The worker's own result is not lost. | Neutral. |
| BE-018 | In the chat, as a system line. | A helper report for this chat could not be read yet. Omnipus will retry. This is not a failure of the helper's work. | A background worker's report is saved but couldn't be read just now. It will be retried. Nothing was lost. | Neutral. |
| BE-024 | In the chat, as a context-window notice. The entry type is documented as Verbose-only (session::EntryTypeContextWindowNotice). The frame is still published on the socket. | Context window exceeded. Compressing history and retrying... | This conversation was too long for the model. I'm shortening the earlier part and trying again. You don't need to do anything. | "Omnipus is shortening" → "I'm shortening". |
| BE-029 | In the chat, as the failed turn's reply. | steer: revive {name}: {text} | I couldn't resume this chat: {plain cause}. Try again in a moment. | "Omnipus" → "I". |
| BE-033 | In the task's own chat, as a system line. | The completion notice for {recipient} could not be delivered: {reason}. | The finished-task notice couldn't be posted to the chat that started it. Open the task to read the result. | Neutral. |
| BE-036 | In the chat, as the only reply the sender sees. | Sorry, I couldn't save your message — please try again later. | I couldn't save your message, so it wasn't sent. Try again. If it keeps failing, check that the data folder has free space. | Back to "I", as today's text already does. |
| BE-037 | That channel's chat, as a message from Omnipus. | [message delivery failed] | I couldn't deliver a message to this chat, and it isn't queued. Send it again if you still need it. | Channel chat (e.g. WhatsApp): the agent speaks, so "I". |
| BE-038 | That channel's chat, as a message from Omnipus. | [message dropped: channel is overloaded, please retry] | I dropped a message because this chat was overloaded. Please send it again. | Channel chat: "I". |
| BE-039 | That channel's chat, as a message from Omnipus. | [media dropped: channel is overloaded, please retry] | I dropped an attachment because this chat was overloaded. Please send it again. | Channel chat: "I". |
| BE-043 | In the chat, as the command reply. | Cancel request failed: {detail} | I couldn't cancel this: {plain cause}. Try Stop again. | "Omnipus" → "I". |
| BE-050 | In the chat, as the command reply. | Failed to reload configuration: {detail} | The configuration couldn't be reloaded: {plain cause}. Fix the config file and try again. | Neutral: a system operation. |
| BE-057 | In the chat, as the command reply. | Stop request failed: {detail} | I couldn't stop this chat: {plain cause}. Try Stop again. | "Omnipus" → "I". |
| BE-060 | In the chat, as the command reply. | Redirect request failed: {detail} | I couldn't switch to your new instruction: {plain cause}. Try again. | "Omnipus" → "I". |
| BE-081 | The live browser panel, as a status line. | Live video connection needs attention: {causes}. Free the configured ports or update the configuration, then restart. | Live video may not reach other viewers: {plain cause}. Free the configured ports, or change them in settings, then restart. | Live browser panel: neutral. |
| BE-096 | Notification centre, as the body. | Omnipus checks each knowledge base against its folder on a schedule. This check found that its search index for {path} no longer matched the folder: {findings}. Your files were not changed — only Omnipus's own index of them was wrong. It is re-reading the folder now; search results for this knowledge base may be incomplete until that finishes. | Knowledge bases are checked against their folders on a schedule. The search index for {path} no longer matched the folder: {findings}. Your files were not changed — only the search index was out of date. It is being rebuilt now; search results for this knowledge base may be incomplete until that finishes. | Notification centre: neutral. Previously had no proposal. |
| BE-097 | Notification centre (and the live notification push). | {text} in the folder that had never been added to the index | {text} in the folder that had never been added to the index | Fragment of BE-096's {findings}. The earlier proposal was worker filler and is withdrawn. |
| BE-099 | Notification centre (and the live notification push). | {text} that have been edited since they were indexed | {text} that have been edited since they were indexed | Fragment of BE-096's {findings}. Earlier proposal withdrawn. |
| BE-101 | Notification centre (and the live notification push). | {n} rename{text} that was interrupted part-way | {n} rename{text} that was interrupted part-way | Fragment of BE-096's {findings}. Earlier proposal withdrawn. |
| BE-102 | Notification centre (and the live notification push). | the index holds a different number of entries than the folder accounts for | the index holds a different number of entries than the folder accounts for | Fragment of BE-096's {findings}. Earlier proposal withdrawn. |
| BE-104 | Notification centre (and the live notification push). | {n} problem{text} of type {name} | {n} problem{text} of type {name} | Fragment of BE-096's {findings}. Earlier proposal withdrawn. |
| BE-691 | That channel's chat, as a message from Omnipus. | ⚠ Scheduled task {name} failed: {run error text} | Scheduled task "{name}" failed: {cause}. Open the app to see the run and try again. | Channel chat: "Open Omnipus" → "Open the app". |
| FE-039 | Message box at the bottom of the chat | Connecting to gateway... | Connecting… | Message box placeholder: product name dropped. |
| FE-060 | Under your message in the chat | Received by Omnipus. ${agentName} will pick it up after finishing the current step. | Received. ${agentName} will pick it up after finishing the current step. | "Received by Omnipus" → "Received". Previously had no proposal. |
| FE-076 | Center of the chat, above the message box | Can't reach Omnipus right now. Your agents keep working. | Can't reach the server right now. Your agents keep working. | "Can't reach Omnipus" → "the server". Previously had no proposal. |
| FE-077 | Center of the chat, above the message box | Your device isn't connected to the internet. Omnipus reconnects on its own when it's back — your messages and your agents' work are kept. | Your device isn't connected to the internet. It reconnects on its own when you're back online — your messages and your agents' work are kept. | Product name dropped. Previously had no proposal. |
| FE-098 | Pop-up only | Action failed | That action failed, and no reason was given. Try it again. | Pop-up: neutral. |
| FE-099 | In place of the Omnipus chat | Omnipus unavailable | Chat isn't available right now. Retry, or reload the page if it keeps failing. | Neutral. |
| FE-127 | On the browser tool row in the chat | Unexpected result type: ${typeof result} | The browser tool returned a result that can't be shown. | Neutral. |
| FE-158 | Sidebar, that agent's row | Unknown error | That didn't work, and no reason was given. Try again. | Sidebar: neutral. |
| FE-430 | Any screen that shows this request's error | API response schema mismatch for ${endpoint}: ${zodIssues[0]?.message ?? 'unknown'} | A response from {where} couldn't be read. Reload and try again. | Neutral. |
| FE-470 | Live browser, in place of the video | Live video is not available because the managed Chrome build cannot capture the browser tab. Run the gateway installer to download a full Chrome build, or set tools.browser.exec_path to a local full Chrome binary. See gateway logs for the e | Live video isn't available with this Chrome build. Run the installer again to get a full Chrome, or set the browser path in settings. | "Omnipus installer" → "the installer". |
| FE-490 | In the chat, as the error line | Something went wrong — please try again. | Something went wrong, and the details couldn't be read. Try again. | Neutral. Still vague — see the spec's wording rewrite. |
| FE-513 | Red bar at the top of every screen | Failed to create WebSocket: ${err instanceof Error ? err.message : String(err)} | Couldn't open the connection to the server. Reload the page. | Banner: neutral. |

## 3. Founder questions answered during the review

| Question | Answer | Certainty |
|---|---|---|
| `tool_args` — “is this not covered in the tools error handling?” | No. Normal tool error handling covers a call that *reaches* a tool with bad input: the tool returns an error and the model corrects itself. `tool_args` fires only when the call never reaches a tool — either the provider rejects the model's malformed tool call (`pkg/agent/translate_error.go::classifyByMessage`), or the model keeps writing its tool call as unparseable text and the two automatic repair attempts run out (`pkg/agent/loop_run_turn_iterations.go`, `maxOrphanToolMarkupRepairs = 2`). | Verified in code |
| `agent_not_configured` — “is this case really possible in the current version?” | Yes in the code: an agent created outside a workspace (`pkg/sysagent/tools/agent.go`, status `metadata_only`) is on no team, and its turns are refused (`pkg/agent/workspace_reroot.go::resolveTurnWorkDirAndWorkspaceOrRefuse`). Whether the screens let a chat or task reach such an agent is not yet checked — follow-up F10. | Code path verified; UI reachability unknown |
