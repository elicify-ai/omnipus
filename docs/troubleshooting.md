# Troubleshooting

Fixes for problems, grouped by symptom. For a running-but-odd gateway (the Omnipus server process), [debugging](operations/debug.md) covers reading its logs in depth.

## What it is

A symptom-first fix list for startup failures, unreachable gateways, login and API errors, model-provider rejections, credential lockouts, and stale builds. It assumes Omnipus is installed — if not, start with [getting-started.md](getting-started.md).

## How to start diagnosing

1. Open `$OMNIPUS_HOME/logs/gateway_panic.log` (default: `~/.omnipus/logs/`). A startup crash always writes there, even when nothing appears on screen.
2. Open `$OMNIPUS_HOME/logs/gateway.log` — the running gateway's log stream, and the reason behind most failed requests. If you set `OMNIPUS_LOG_FILE`, read that file instead; see [choosing the diagnostic log file](operations/debug.md#choosing-the-diagnostic-log-file).
3. Run `omnipus doctor`, which checks your configuration for known unsafe settings.
4. Still unexplained? Restart with `omnipus start --debug` — see [debugging](operations/debug.md).

## The gateway exits during startup

If it exits with no output at all, read `gateway_panic.log`, as above.

### Exit code 78: the sandbox failed on a capable kernel

Exit 78 now happens only when a kernel that claims to support Landlock fails for a reason other than a right the kernel does not know — a malformed rule, a port rule the kernel rejects, or the step that activates the policy. Omnipus refuses to start rather than listen half-protected. Look for `sandbox.apply_failed` in `gateway.log`, then start with `omnipus start --sandbox=permissive` (violations logged, not blocked) or, for development only, `--sandbox=off`. Valid values are `enforce`, `permissive`, and `off`; a typo exits with code 2.

A kernel that is too old for a right Omnipus asks for is different: Omnipus starts anyway, at application-level enforcement, and logs `sandbox.degraded` instead of exiting — see [operations/sandbox-limitations.md](operations/sandbox-limitations.md). `OMNIPUS_ENV=production` with the sandbox weakened prints a repeating warning banner — deliberate, not a fault. Full detail: [operations/sandbox-config.md](operations/sandbox-config.md).

### It exits with "bind: address already in use"

The gateway serves everything — web app, API, and agent-built previews — on a single listener, `gateway.port`, default **5000**. There is no second preview port. If another process holds the port, the gateway retries the bind twice, then exits with a clear boot error; it never falls back to a different port.

Before returning the final bind error, the gateway runs cleanup for services created during that startup attempt. It requests shutdown of scheduled jobs, the task and mail pollers, task triggers and the plan engine, where present. Cleanup can delay the final error while active work is stopping; the delay is not guaranteed to be short. Some stop requests do not wait for every in-flight operation to finish. The same cleanup runs for errors returned by other steps in this service-startup sequence, skipping services that were not created. Check what holds the port:

```bash
lsof -i :5000 | grep LISTEN
```

Stop that process, or set another port in `~/.omnipus/config.json` and start again:

```json
{
  "gateway": { "host": "127.0.0.1", "port": 5500 }
}
```

The port is read at startup; a running gateway does not move its listener.

### Stopping the gateway takes a moment

When you stop the gateway normally — Ctrl-C, or SIGTERM from a service manager — Omnipus stops its chat channels and cancels the agent loop. It tries to let tracked work finish writing before tearing down the stores it uses, including background task runs and helper-agent turns. This cleanup also covers a helper turn already being resumed by a redirect as shutdown begins.

Shutdown has several wait budgets, not one 30-second limit. The gateway waits up to 65 seconds for active requests, limited by the time remaining on its 70-second shutdown context. Agent cleanup has separate waits of up to 30 seconds for recaps, task dispatches and helper-agent turns, and up to five seconds for each session worker. These are individual wait budgets, not a guaranteed total stop time. If the gateway's shutdown context expires, it can continue shutdown without waiting for agent cleanup to finish.

When a wait runs out, cleanup continues and the log shows a warning such as `Close: steered-turn drain budget exceeded; proceeding with teardown`. Some work or its final writes may be left unfinished.

## A connection shows a credential error (degraded)

A connector, provider, mailbox, MCP server, or assistant-connector whose stored secret is missing, was deleted, or can no longer be read is shown as degraded — the row's or card's error names the exact credential reference to fix, for example `secret reference MY_BOT_TOKEN is missing from the credential store`. Missing credential references are handled as integration-specific errors. An individual authentication failure is handled the same way when another stored entry decrypts. Every integration using that reference can be affected. Other credential failures—including malformed entry encodings—can stop startup or reject reloads and settings refreshes even if another entry decrypts. More than one integration can depend on the same credential reference. A whole-vault failure is different, as described below. The reason shows where the integration lives: a chat-channel or mailbox row on the **Connectors** screen shows it under its status badge, a provider's row under **Settings → Providers** shows it as the row's error line, and an MCP server's card under **Skills & Tools → MCP Servers** shows it under the status badge. **Doctor** (Settings → Security, Security health) flags degraded chat channels only; for providers, mailboxes and MCP servers, read the reason on their own row or card.

To fix it, re-enter the credential where the row points you — **Connectors** for a chat channel or mailbox, **Settings → Providers** for a model key, the Credential Vault for a raw entry — and save. The connection recovers on the next settings save or restart.

A failure of the whole Credential Vault is different: a locked store, an unreadable or damaged file, or a master key that cannot decrypt any entry stops startup and rejects reloads and settings refreshes. Omnipus checks the store itself even when only MCP servers use its secrets, or no integration currently references them. A rejected update keeps the previous live configuration; a settings write may already have saved the proposed configuration to disk. Fix the store or supply its original master key before retrying. See [Saving a secret fails with "credential store locked"](#saving-a-secret-fails-with-credential-store-locked) below.

A fresh install with no credential-store file and no secret references can still start normally. A missing secret, or an entry that fails authentication while another stored entry can be decrypted, remains scoped to the integrations that depend on it rather than becoming a whole-vault failure.

## Every request returns 401 Unauthorized

The web app signs you in once, then rides a session cookie. Scripts must present a Bearer token belonging to a configured account or the gateway's CLI token, or match `OMNIPUS_BEARER_TOKEN`.

| Message you see | Why it happens | What to do |
|---|---|---|
| `unauthorized: no users configured, complete onboarding first` | Fresh install: no account, no `OMNIPUS_BEARER_TOKEN` | Complete onboarding — next section |
| `unauthorized: missing Bearer token` | A script sent no token, or your browser session expired | Sign in again, or send a Bearer token |
| `unauthorized: invalid Bearer token` | The token matches no account and no CLI token | Re-issue the token, or fix `OMNIPUS_BEARER_TOKEN` |

For local development only, `"dev_mode_bypass": true` under `gateway` lets requests through with no token; the gateway warns at boot and answers 503 on admin routes while it is on. Never enable it on a machine others can reach.

## Chat replies with a limited-mode message

On a fresh install the gateway boots into **limited mode**: it serves the web app but has no model, and every chat message returns the reason, such as `no default model configured; gateway started in limited mode`. The same happens when the API key behind your default model stops resolving; each reply then names the credential at fault.

Finish onboarding to fix it. Open `http://localhost:5000` — on a fresh install every screen leads to the wizard (your name, a password, then a provider and its API key). Without a browser, `omnipus onboard` prompts for provider, key, model, and account.

## A chat turn stops before a finished answer

If a turn fails after text starts streaming, chat shows a failure message in place of the unfinished reply, even with **Verbose chat** off. Enable **Settings → Chat → Verbose chat** before retrying to inspect tool activity and any available technical details. A turn you stop yourself still keeps its partial reply.

A provider can also report an error after it has already started streaming a reply (OpenRouter does this when an upstream model fails). Omnipus discards the partial text, runs no tools, and names the failure by the provider's own error code instead of calling everything a network fault:

| What the provider reported | What you see | Retried? |
|---|---|---|
| A temporary fault (server error, overloaded, timeout) | The connection-failure message | Yes, inline, like a dropped connection |
| Rate limit (429) | The rate-limit message | Handed to the fallback chain |
| Billing or credit (402) | The billing message | No |
| Bad or missing key (401, 403) | The provider-rejected-your-key message | No |
| The request itself was rejected (400, 422) | The provider-rejected message | No |
| Moderation or content policy | The content-policy message | No |
| A code Omnipus does not recognise | The generic unknown-failure message | No |

A billing or key error is never retried, because every attempt would be billed and fail the same way. The provider's own error text appears only with **Verbose chat** on.

If the agent reaches its tool-call limit before a final answer, chat keeps the narration and adds a separate limit notice that remains when you reopen the conversation. Closing the originating browser connection does not cancel the turn or redirect that notice to another conversation: terminal delivery retains the conversation's session ID. Conflicting tool names produce the standard “This turn didn’t finish, and we can’t tell why” failure notice rather than silence.

## A helper finished but the open parent chat is blank

Check the parent's current state first. Progress, checkpoints and shared artifacts do not start a parent reply. An intentionally stopped parent retains a helper's result until a legitimate continuation; a parent with an unanswered question card handles the report with your answer rather than starting a separate reply.

A helper's finished result, question, blocker or failure is saved as an event line in the parent chat, independently of whether the parent repeats it. The parent's new reply appears in the open chat as it is written, without a reload. A Judge ending also appears in the parent chat. Saved event lines and the parent's replies remain after reload; the internal continuation input is not displayed as a message from you.

If an expected event or reply is missing, inspect the helper's current state and the gateway's delivery or storage errors before assuming the report was lost. A storage failure can leave an event pending repair, and a failed wake publication remains retryable. Report the parent and helper session IDs and whether reloading reveals the missing event or reply. Do not restart the gateway as a workaround.

## A background result says the agent was not woken

This warning concerns delivery of one notification, not rerunning or undoing the background work. Omnipus tries the handoff up to three times within the caller's existing window: up to five seconds per attempt, with one- and two-second cancellable waits. A caller deadline after an unsuccessful attempt still triggers the warning; intentional cancellation suppresses it and stops later attempts. A notification accepted by the bus is queued, not a guarantee that the receiving agent has executed it.

Check the original conversation and any saved tool result before asking to rerun work. A real tool warning names the tool (or **background tool** if its name is unsafe) and a short tool-call reference. Generic warnings identify only a fixed source, such as a plan update; another path may still have dispatched the agent directly. Send a normal message if you want the agent to continue from the result.

If saving succeeded, the same system line is used live and in reopened history for that session. Only the permitted original platform conversation gets a single best-effort external warning, with no redirect to another chat/account. A full or closed outbound queue can reject it; enqueue acceptance is not network delivery. If history cannot be written, the durable line may be absent, although an allowed live error or origin warning may still be possible. Check disk space, file permissions and session paths before relying on saved history; repairing them does not by itself replay the failed notification.

The gateway's new **Async notification publish failed** records identify actual failed attempt numbers, their retry/final decision, a fixed source and a notice ID; optional tool/session references are shortened digests. They do not contain notification output, command arguments, raw routes or raw error causes. **Async notification origin warning enqueue rejected** separately means the one external enqueue was not accepted; it does not distinguish a full queue from a closed one.

**Async notification secondary warning failed** is an error-level record about the warning itself, not another attempt to wake the agent:

| Stage | Meaning |
|---|---|
| `notice_save` | Omnipus could not save the system warning: the save failed, the conversation storage was unavailable, or the notification had no conversation to save it in. |
| `origin_read` | Omnipus could not read the saved conversation origin to decide whether an external warning was allowed. |

Both stages use `error_class=secondary_warning_failure`, a fixed source and the same `notice_id` as the failed notification's other diagnostics. Only the existing validated tool name and shortened tool/session references may be added. These records contain no raw error cause or type, conversation route, notification content, command arguments, tool output, question or approval text. A refused audience without a read error does not produce this secondary-error record.

Logging does not repair or replay the handoff, prove that the warning was saved or delivered, or guarantee that a configured log sink was enabled and wrote successfully. These are safe notification diagnostics, not a claim that every unrelated log record is content-free. See [Background results](tools.md#background-results).

## A task is refused before work starts

Errors saying the assigned agent's store was not found, or that a task session could not be created, mean Omnipus could not create the task's conversation. A queued dispatch or manual start refuses to hand work to either a native agent or an external command-line agent in that case. It does not consume an execution attempt or add a run-history entry.

Read the returned error and the gateway log. A conversation-creation error keeps the underlying storage cause, such as a path that is not a directory or cannot be written. Resolve that cause, or restore the assigned agent's conversation store, before retrying. Do not create a replacement conversation by hand.

A queued dispatch tries to save **Failed** and verifies the saved status. A new manual start through the web app or `run_task` instead tries to restore the task's previous status. If task storage itself cannot save that change, **In Progress** is not proof that work started; check the error and run history. See [tasks](tasks.md#if-the-task-conversation-cannot-be-created).

This is a pre-start refusal, not a failure after tools or an external command have already acted. It does not imply that a later execution failure undoes those actions.

## A heartbeat or scheduled check-in is skipped

Read the gateway diagnostic log. These cron warnings remain visible at the default **warn** log level and carry the affected `job_id`.

| Warning | Meaning | What to check |
|---|---|---|
| `previous run still in progress` (`reason: overlap`) | Omnipus still considers an earlier run in progress and skips this tick rather than starting overlapping work. | Inspect that job's current work and cleanup before retrying it. |
| `no owning agent` (`reason: owner-missing`) | The job has no owning agent ID, so it cannot run through the agent runner. | Check its agent assignment and workspace heartbeat configuration. |

Cron's `failed to save store`, `failed to persist skip-reschedule` and `failed to persist Running reset` diagnostics are logged at **ERROR**, with `job_id` (or `job_ids` for a batch) and the underlying storage error. Work may already have happened even though its latest state was not saved. Resolve the disk, permissions or path problem before relying on the saved state after a restart. The error does not undo completed work, and retrying may repeat earlier actions.

## The provider rejects your model requests

A `model_route_unavailable` notice means Omnipus could not set up the named provider/model connection from its saved settings. No request was sent to that pair, and a global-default connection was not substituted. Check the provider's API key or sign-in and endpoint settings before trying again. The notice names the pair; scrubbed setup details are available only live under **Verbose chat**.

For an HTTP 400 rejection, the failure notice distinguishes a wrong model from an otherwise unclassified bad request:

| Notice | Meaning and next step |
|---|---|
| `bad_model`, naming the provider and model | The provider specifically reported that the model was not found or is not offered. When the agent has a backup model configured, Omnipus answers from it and a fallback notice names both models; if no backup exists, or every model in the chain is rejected this way, the failure stops the chat turn or task. Choose a model offered by that provider in the agent's settings. |
| `HTTP 400: Bad Request; format`, naming the provider and model | The provider rejected the request, but did not identify a more specific cause. This does **not** prove the model is unavailable. Enable **Verbose chat** before reproducing it to inspect the available technical detail. |

A confirmed wrong-model error stops all attachment recovery too: removing an image or converting a PDF cannot make a missing model exist. For availability prose, Omnipus reads the clause's **subject** — what is unavailable — rather than searching for the word “model”.

The supported model wording is a simple **model is not available/offered** statement. It can start with **the**, **this** or **a**, include **requested** before **model**, and name a model identifier. It can end with **on/by this provider**. A neutral error prefix can introduce the clause with punctuation, including a colon with no following space. Input-type subjects are different, even when they mention a model:

| Provider wording | How Omnipus treats it |
|---|---|
| `The model 'm' is not available.` or `Model 'm' is not offered by this provider.` | Positive model-unavailability evidence: `bad_model`, with no attachment retry. |
| `This model is not available.`, `The requested model m is not available.` or `A model 'm' is not available.` | The subject is still the model: `bad_model`, with no attachment retry. |
| `Request rejected:model m is not available.` | The colon introduces a model-subject clause; a missing space after it does not change the classification. |
| `Model 'm': image input is not available.` or `Model 'm' is not available for image input.` | An image-input restriction, not proof that the model is missing. Existing attachment recovery can still apply. |
| `Image input for model 'm' is not available.` or `Image input for this model is not available.` | The unavailable subject is image input, even though the sentence mentions a model. Existing image recovery can still apply. |
| `PDF input for model 'm' is not available.` or `PDF input for this model is not available.` | A PDF-input restriction, not proof that the model is missing. Existing PDF-to-text recovery can still apply. |

This is a limited diagnostic grammar, not a general English interpretation. Unsupported subjects, qualifiers or compound availability messages are not treated as proof of a missing model; existing bad-request classification and eligible attachment recovery remain available. A generic bad-request notice does not prove that the configured model is valid, either. Other specific error classifications keep their existing rules.

A structured `model_not_found` code is authoritative even if its text also mentions attachments, authentication or rate limits.

The same safe explanation is saved in the conversation and remains after reopening it, including when an empty-response retry fails. Helper failure handbacks use translated explanations even for unfamiliar streaming error codes. Raw provider response text is not saved as that explanation. Technical detail is live-only and scrubbed for credentials; turning **Verbose chat** on after delivery or reopening history does not recover discarded detail. Existing attachment recovery, context-length handling, and authentication, billing and rate-limit classifications still take their own paths.

**"Context window exceeded. Compressing history and retrying..."** means the model provider rejected a request as too long. Omnipus first removes injected recall, then removes the oldest eligible whole piece — a complete old step (assistant calls plus all their matching results), or an older assistant reply that made no tool calls, whichever comes first in the conversation; if nothing can leave, it shortens retained tool-result text. It does this even when its own estimate says the request fits. Each retry must contain less retained content: the context retry counter is not reset, and there are at most two such retries after the initial request.

This is an internal retry notice, not a data-loss event: relief changes the model's view, not the admitted archive text. It keeps the original user message and media, instructions, call/result structure and unconsumed steering. If those required parts still do not fit, Omnipus returns the last real provider rejection when no further reduction is possible or the retry limit is reached. It does not stop merely because a local size estimate says the immutable remainder is too large.

A storage or projection error is different. If Omnipus cannot save the window change, it stops with that genuine error instead of sending a request that disagrees with the saved state. See [memory](memory.md#how-long-conversations-stay-in-view) for what can leave the live window and what an aborted turn rolls back.

If the final streamed reply cannot be saved, the live chat shows **"Sorry, I couldn't save your message — please try again later."** and reports a failed turn rather than a successful answer. This failure is visible in normal chat; **Verbose chat** is for technical details. Other conversation-storage failures also stop the affected operation, but their wording depends on the failing path.

A failed write does not prove that the reply or its error notice was saved. The current connection's retained event history can carry the notice, and active-turn handling attempts to record it, but failing storage can prevent that record too. After a reload or restart, inspect the saved history and gateway log rather than assuming the notice must return. Fix disk space, permissions or the path problem before retrying. Work already done outside the conversation is not undone.

The **context-window compression/retry notice** described above — not a failed-save notice — is shown or hidden by **Settings → Chat → Verbose chat**. Live chat, reopened history and conversation replay use that same display setting.

### "This endpoint did not report a context length for this model"

A local or self-hosted model's endpoint did not report how much it can read at once, and no operator override exists. Omnipus refuses to run on a guessed window rather than risk silent overflow. Set the context length explicitly at **Settings → Models → Model overrides → Context length**, then retry.

### 404 "No endpoints found that support tool use"

The model cannot call tools, and Omnipus sends its tool list with every request, so the request is rejected outright. Small open models often lack tool support. In the model picker, the **Recommended for chat** mark appears only on models that can call tools. Change the default in **Settings → Providers**, or edit `agents.defaults.default_model` in `config.json`.

### "model … is offered by several providers; choose the provider"

You picked a model in the chat's model picker, which sends only the model name, and more than one connected provider offers that name. Omnipus does not pick an account for this switch, and the reply uses the agent's current model. Choose the model together with its provider in the agent's model setting instead, or remove the provider you do not want to use. Pin agents' models to a provider in general: a model saved without one can start on the default provider when its name is ambiguous.

### "The model returned an empty response"

The model sent back no text and no tool call, even after one automatic retry. Omnipus does not switch to a fallback model for this, because an empty answer usually points to a defect rather than an outage. The gateway log has one warning line starting `LLM returned empty response after retry`. It names the provider and model, the finish reason, token counts, and the shape of what came back: stream event types with counts, output item types, status and incomplete reason. It never includes message content. Include that line when you report the problem.

### 400 "invalid model ID" or a rejected provider

The `model` value reaches the provider exactly as written; on OpenRouter, IDs carry a vendor prefix (`z-ai/glm-5.2`, not `glm-5.2`). The `provider` value must be an ID the catalog knows, such as `openrouter` or `anthropic`. A working entry:

```json
{
  "providers": [
    {
      "name": "fast",
      "provider": "openrouter",
      "model": "z-ai/glm-5.2",
      "api_key_ref": "openrouter_api_key"
    }
  ],
  "agents": {
    "defaults": {
      "default_model": { "provider": "openrouter", "model": "z-ai/glm-5.2" }
    }
  }
}
```

`api_key_ref` names an entry in the encrypted credential store. Set it without touching files with `omnipus credentials set openrouter_api_key sk-or-v1-...`, or use **Settings → Security → Credential Vault** in the web app.

## Saving a search integration fails with "could not read the current configuration"

Saving a default search provider can require another read of the current configuration to check the automatic DuckDuckGo fallback. If that read fails, the request returns HTTP 500 with this message before saving any key or configuration changes. Restore readable, valid configuration, then retry the save.

## A secret card says "Submission unknown" or "Request unavailable"

If the connection drops after you submit, do not paste the value into chat or submit it automatically again. The card checks the server's saved request state. **Saved** means the vault has the new version; it does not mean the integration has switched to that version or passed a connection check.

| Server state | What to do |
|---|---|
| Pending | The value has not been committed. Retry from the card when the connection is working. |
| Submitted / Saved | Do not enter the value again. The agent's continuation uses the saved reference names. |
| Cancelled or stopped | Ask for a new card if you still want to provide the secret. |
| Interrupted / Request unavailable | The gateway restarted before submission, or the request is no longer accessible. Ask for a new card. |
| Destination changed | Ask for a new card and review its destination before submitting. |
| Secret saved; continuation not confirmed | Keep the saved reference. Reload the chat; unless you explicitly stopped it, the gateway retries a continuation that has not reached the conversation yet. Do not replace the secret just to restart the conversation. |

A failed vault write leaves the request pending and stores none of the submitted fields. A repeated submit after a successful save returns the existing submitted state instead of overwriting its version.

## Saving a secret fails with "credential store locked"

The encrypted credential store needs a master key. The gateway looks for one in this order:

| Priority | Source of the key | Notes |
|---|---|---|
| 1 | `OMNIPUS_MASTER_KEY` | 64-character hex key in the environment |
| 2 | `OMNIPUS_KEY_FILE` | Path to a 0600-permission file holding the hex key |
| 3 | `$OMNIPUS_HOME/master.key` | Loaded automatically when present with 0600 permissions |
| 4 | Auto-generate | Only on a fresh install with no key and no `credentials.json` |
| 5 | Interactive prompt | Terminal only |

A fresh install warns you to back up `master.key`. Heed it: losing that file makes every stored credential permanently unreadable, with no recovery. See [Security for users](security.md).

## Credential-file format or authentication errors

Every stored value is encrypted together with the name it is stored under, so a value cannot be moved from one entry to another and still open. Normal credential reads require the name-bound, version-2 file format. Unlock can convert a version-1 file before use, but refuses it when `credentials.json.migrated` already records a completed conversion. The conversion authenticates every entry and replaces the whole file atomically. A crash leaves either the original file or a complete converted file, not a mixture. Saving `credentials.json.migrated` is a separate, best-effort step after that replacement. If its write fails, a warning is logged, but conversion and unlocking can still succeed; the success event `credentials.store_migrated` is emitted even when the marker could not be saved. Without the marker, a restored version-1 file can be converted again.

During that format conversion, the gateway refuses to start and leaves the credential file unchanged in these cases:

| Message says | What it means | What to do |
|---|---|---|
| "every entry failed" | The master key is not the one the vault was written with | Supply the original master key (`master.key`, `OMNIPUS_MASTER_KEY` or `OMNIPUS_KEY_FILE`) |
| "N of M entries failed authentication" and names them | The named entries could not be authenticated | Back up `credentials.json`, remove the named entries, restart, then enter their values again |
| "already migrated it once" | A version-1 file is present alongside the completed-conversion marker | Remove `credentials.json.migrated` only if you deliberately restored a trusted version-1 backup. Otherwise treat the file as potentially tampered with and investigate it |

To remove an entry by hand: stop the gateway, back up `credentials.json`, open it in a text editor, and delete the named entry (its name and its `nonce` and `ciphertext` lines) from the `credentials` section. Restart the gateway, then enter the value again in **Settings**, **Security**, **Credential Vault**, or with `omnipus credentials set <name> <value>`.

### Restoring a credential backup

Restore only a backup whose source you trust, with the master key or passphrase that opens it. A version-1 backup with a completed-conversion marker is refused. If that restore was deliberate, stop the gateway, remove `credentials.json.migrated` next to the vault, and start again to permit conversion. Do not remove the marker to bypass an unexplained file change.

### Windows: concurrent credential-file conversion

On Windows, separate processes do not share a file lock. If two Omnipus processes (for example the gateway and an `omnipus credentials` command) open the same version-1 file concurrently, both may convert it. Each writes a complete, valid vault, and the last write wins. In passphrase mode, the process whose write was replaced holds a key for the overwritten file and cannot read credentials until it is restarted. Run only one Omnipus process while converting a version-1 credential file.

Normal entry reads never fall back to a different encryption format after an authentication failure. The gateway names a failing entry in its log. Inspect the named entry and its key, and re-enter the value through the same credential controls when needed.

## The page shows "Something went wrong loading this page"

A page, panel, or the onboarding wizard could not load or render — for example when a tab still references JavaScript files from an earlier deployment. This is not a sign-in failure and not a reason to restart setup. Press **Reload page** to fetch the current files and try again. If the message keeps returning, open your browser's developer console and copy the error when reporting the problem. Look for `[router] route load/render failed:` or `[app] Uncaught render error:`.

## An agent is missing and a notice says its data is invalid

If one agent's saved data does not match what the web app expects (for example a name longer than 100 characters), that one agent is left out of the agent lists and a notice names it: *One agent's data is invalid and is hidden from the list: <agent id>, field <field> (<reason>).* Every other agent stays usable. Repair the named field through the agent API using its current revision, then refresh the list. Agent records are stored separately from the gateway's global configuration; editing a legacy embedded agent list does not repair them. If the API cannot read or repair the record, report the named agent and field rather than changing unrelated configuration. A response the web app cannot read at all says *The server sent data this page could not read* with the address and field; it does not mean the browser and server versions differ.

## The web app looks outdated after a source build

The Go binary embeds the web app from `pkg/gateway/spa/` — a copy of the frontend build output, not the output itself, so building without refreshing that copy serves an old interface. `make build` refreshes it and builds in one step. Confirm a change reached the binary by searching the bundled assets for a string only it contains:

```bash
grep -c "<your new string>" pkg/gateway/spa/assets/index-*.js
```

A count of 0 means the copy is stale.

## Mail is busy or cannot refresh

The mail panel can answer with a **busy** notice instead of message data. Omnipus allows two connections per mailbox and eight across the whole application, shared fairly between the panel, your agents' mail tools, and the background new-mail watcher; a request that cannot get a slot inside a few seconds is refused rather than piling on. The notice says which of three things happened: the mailbox is in a **backoff** window after a failed check (the panel's **Retry** click bypasses only this), the **mailbox is busy** (its two slots were taken — wait a moment and retry), or the **server limit** was hit. Backoff clears on its own when the watcher's next attempt succeeds.

Folder lists load 25 messages at a time, up to 200 per view; **Search** runs on your mail server within that same window, so older messages are reachable without loading everything. If a list or message was changed or deleted on the server in the meantime, the panel says so and lets you refresh once rather than showing you stale content.

## Mail cache unavailable

A **cache unavailable** warning on the mail panel means Omnipus could not prove that its private mail metadata storage is protected on this install — for example a disk failure or a permission problem, or a version-control repository in the data directory whose state Omnipus cannot verify. It is deliberately careful: until the proof succeeds, no cached mail metadata is written, and the mailbox keeps working live. The warning never means your mail is unavailable, and it is not asking you to install extra tooling — check disk space and the data directory's permissions first. See [Security for users](security.md#mail-metadata-and-temporary-previews) for what the cache holds.

## Mailbox removal left cleanup pending

Removing a mailbox always removes its configuration and stored password first — the account stops working immediately. Omnipus then erases its cached mail metadata and new-mail watching state. When that erase could not fully finish (a file held open or a disk error), the removal result says **cleanup pending** with a safe reason code and a retry key, instead of claiming success. Nothing of the mailbox remains usable in the meantime. Retry the cleanup with the key — `POST /api/v1/mailboxes/cleanup` with `{"cleanup_intent": "<the retry key>"}` — which also works after the mailbox row is already gone from the settings; the response is truthful again: **removed** when every step has now succeeded, **cleanup pending** with the same key if something still failed. Removing an agent or a workspace runs the same erase for each mailbox it held; files you saved from mail into a workspace are ordinary workspace files and are never touched by this cleanup.

## An agent's main chat says it "could not be prepared"

Opening an agent's main chat (see [Each agent's own chat, per workspace](workspaces.md#each-agents-own-chat-per-workspace)) can answer with this fixed message:

> This agent's main chat could not be prepared because session storage failed. Check disk space and permissions, then retry. Details are in the server log.

It means the agent is entitled to a main chat in that workspace, but Omnipus could not create it, or could not trust the one it found. The message deliberately names no file, folder or id; the full cause is in the gateway log as an error-level line starting `rest: main session could not be prepared on open`, with the `workspace_id`, `agent_id` and the underlying error. (A missing chat for an agent that is *not* on the team, a worker, or Omnipus outside the default workspace is different: that stays a plain "session not found".)

**A workspace can be saved while its main chat is unavailable.** Saving team membership and preparing the chat are separate steps. The workspace-details response includes a safe preparation reason for each failed member instead of a main-chat address. It does not expose filesystem paths or the contents of a damaged saved identity. If a file occupies a location that must be a folder, the reason is:

> The main chat could not be prepared because session storage has a file where a folder is required. Ask the server operator to fix it, then retry.

When this failure prevents the workspace's first conversation agent from opening, Chat shows the server's reason with **Retry**, keeps the message box disabled, and does not say **Your agent is ready**. If no reason is provided, Chat keeps the generic **This chat is unavailable right now** notice instead. It never switches to the next teammate to hide the failure.

The operator must correct the storage layout; **Retry** does not remove or replace the blocking file. Retry reads the same workspace's details and makes one preparation attempt per eligible member. Once storage is repaired, the next read returns the matching main-chat address and no preparation error; Chat opens that main and clears the notice. Sending resumes after the conversation finishes loading. Corrupt, mismatched or unclassified failures keep the fixed generic message above; full diagnostics stay in the server log. A healthy teammate's main chat remains available.

**Why it can happen at startup too.** On every start Omnipus creates the main chats of the default workspace's team (the system agent Omnipus included) and of every saved workspace team. A failure there does not stop the gateway. It is logged at error level — `rest: boot mains: ...`, naming the workspace (and the agent) — and the chat is tried again the next time someone opens it.

**What to do.**

1. Read the error line in the gateway log (see [debugging](operations/debug.md)); it names the workspace and agent and the cause.
2. If the cause is disk space or permissions on the data directory's `sessions` folder, fix that, then open the chat again. Opening it re-attempts the creation, so no restart is needed: the chat is created, or — if it already existed — the same chat is reused with its history.
3. If the log says a saved main chat is unreadable or "does not match the pair" (for example its `meta.json` is damaged, or it records a different owner or workspace), Omnipus will not repair, adopt or replace it. An administrator must fix or remove that chat's folder under `sessions/` (its name is `main-session-<workspace>+<agent>`), after which opening the chat creates a fresh one.

Other session reads and writes that hit a storage failure answer in the same way — "This session could not be read / renamed / deleted / created because session storage failed. Check disk space and permissions, then retry. Details are in the server log." — with the cause only in the log.

## Two more errors with short fixes

**"priority must be between 1 and 5"** — task priority runs from 1 (highest) to 5 (lowest); 3 is the default, and anything outside the range is rejected.

**"failed to move downloaded file ... invalid cross-device link"** — the skills installer downloads to the system temp directory, then moves the file into the workspace; the move fails when the two sit on different filesystems, as in containers with mounted volumes. Point `TMPDIR` at a directory on the workspace's filesystem before starting the gateway:

```bash
export TMPDIR=/path/to/workspace/tmp
mkdir -p "$TMPDIR"
```

## A shell command is "blocked by safety guard (path outside working dir)"

The shell guard reads the command's text and refuses anything that looks like a path outside the agent's working folder, unless it can tell the reference is harmless. Since 2026-10-29 a stand-alone quoted value whose top folder cannot exist, such as `'/api/v1/users'` in a Python one-liner, is accepted. Text the guard still cannot judge keeps its existing decision: `../` anywhere in the command, quoted pieces that are joined, formatted or brace-expanded into a path, and globs. The exception is off on Windows and whenever the account running Omnipus can write the filesystem root (normally when it runs as root).

**Fix:** have the agent write the script to a file in its workspace with `write_file`, then run the file (`python3 script.py`). The guard does not read the file's contents; this avoids only the false refusal and does not grant access to files outside the workspace. For a real folder outside the workspace, request a mount. Details: [tools](tools.md#limits-and-things-to-watch).

## Limits and things to watch

- Sandbox strength depends on the operating system: Linux confines the gateway and everything it starts, macOS confines the commands Omnipus runs through its sandboxed execution path (not the gateway itself), and Windows uses application-level checks alone. External command-line workers (Claude Code, Codex, OpenCode) and the CLI version check start directly, outside the Omnipus sandbox on every platform. Details: [operations/sandbox-limitations.md](operations/sandbox-limitations.md).
- Behind a reverse proxy, set `gateway.public_url` to the address browsers actually reach; preview links and WebSocket origin checks derive from it. See [operations/reverse-proxy.md](operations/reverse-proxy.md).

## Related pages

- [debugging](operations/debug.md) — reading gateway logs and running with `--debug`.
- [getting-started.md](getting-started.md) — install and first-run setup, including onboarding.
- [operator configuration](operations/configuration.md) — the `config.json` areas named here.
- [Security for users](security.md) — how the master key and encrypted credentials affect you.
