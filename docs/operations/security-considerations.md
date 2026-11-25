# Security Considerations — Operator Guide

## Single-listener preview boundary

The web app, API, WebSocket connection, and `/preview/` routes share the gateway listener on port `5000`. Preview is a link that opens in a browser tab, not a second listener or embedded frame. Set `gateway.public_url` to the public HTTPS origin so preview links and origin checks use the address users actually reach. See [reverse proxy](reverse-proxy.md) for the routing and header contract.

## Bearer-token contract for `/preview/`

Preview URLs contain a time-limited bearer token embedded in the path. Anyone who has the URL can load the content until the token expires — the gateway does not require a logged-in session to serve preview responses. Both static-served directories (formerly `serve_workspace`) and dev-server processes (formerly `run_in_workspace`) are now reachable through the unified `/preview/<agent>/<token>/` route.

Operators who want tighter access control have two levers:

1. **Shorten the token lifetime** — lower `tools.serve_workspace.max_duration_seconds` from the default `86400` (24 hours) to a value appropriate for the deployment, for example `3600` (1 hour). Tokens issued after the change use the new duration; existing tokens are not revoked. (The config key retains the pre-unification name for back-compat with existing operator configs; the `serve_workspace` key controls both static and dev-mode preview durations.)

2. **Treat preview URLs as secrets** — avoid sharing a preview URL outside the trusted user who triggered the agent turn that generated it. The URL itself is the credential for that preview.

There is no per-token revocation endpoint in the current release. To invalidate all outstanding tokens, restart the gateway (tokens are stored in memory and are not persisted).

---

## Kernel-enforced bind-port allow-list

On Linux kernels with Landlock ABI v4 (kernel 6.8 and later; see
`pkg/sandbox/sandbox_linux.go:67` — "Landlock ABI v4 network access rights
(kernel 6.8+)"), the gateway and every child process it spawns are restricted
to binding TCP ports inside `cfg.Sandbox.DevServerPortRange` (default
`[18000, 18999]`) plus the gateway listener on port `5000`. Any `bind(2)` to a
port outside that allow-list returns `EACCES` from the kernel — including
`bind(0.0.0.0:5173)` from a shell-spawned dev server.

This means an agent calling `bash` with `npx vite --host 0.0.0.0 --port 5173` will
fail at the bind syscall, regardless of the agent's tool policy. The only
legal way for an agent to expose a website is through the `web_serve` tool,
which auto-picks a port from the allow-listed range and routes traffic
through `/preview/<agent>/<token>/` with the bearer-token contract above.

**Graceful degradation:** on kernels with Landlock ABI < 4 (no `NET_BIND_TCP`),
this enforcement is silently inactive. The gateway probes runtime capability
at boot via `probeLandlockABIPlatform()` (`pkg/sandbox/sandbox_linux.go:132`)
and reports `landlock_enforced: false` on the `/health` endpoint when the
feature is absent. Operators on such kernels still get tool-layer port-range
validation (the `web_serve` tool refuses out-of-range ports), but a
shell-spawned process can technically bind anywhere. Plan to upgrade to kernel
6.8+ for the full enforcement story.

---

## Kernel-enforced outbound port allow-list (raw TCP egress, #155)

On Linux kernels with Landlock ABI v4 (kernel 6.8 and later for the gateway's tested baseline; see `pkg/sandbox/sandbox_linux.go:67`), `connect(2)` from the gateway and every forked child is restricted to the union of:

- **`{53, 80, 443}`** — DNS, HTTP, HTTPS. The minimal set required by the gateway's outbound LLM/provider calls and the egress proxy's upstream connections. Defined as `sandbox.DefaultConnectPorts` in `pkg/sandbox/sandbox.go`.
- **`cfg.Sandbox.DevServerPortRange`** — every port in the configured range (default `[18000, 18999]`). Lets children connect back to gateway-owned dev servers, the egress proxy, and other agents' running web_serve sessions.

Any `connect(2)` to a destination port outside that union returns `EACCES` from the kernel. This closes the raw-TCP-egress hole documented as threat **C4** in the insider-pentest report: a child issuing `socket() + connect()` directly (bypassing `HTTP_PROXY` env vars) can no longer reach unauthorised ports — including SSH (22), MySQL (3306), Redis (6379), or arbitrary backdoor channels on uncommon ports such as the redteam test target `127.0.0.1:1`.

**Limitations of this control — read carefully.** The kernel mechanism (Landlock `NET_CONNECT_TCP`) is **port-level only**. It cannot filter by destination IP or CIDR. Specifically:

- A child can still `connect()` to **`192.168.1.1:443`**, **`10.0.0.5:443`**, or **`169.254.169.254:80`** (cloud metadata, on AWS/GCP) — the destination port is allowed for legitimate HTTPS traffic, and the kernel does not inspect the IP.
- For HTTP clients the **gateway** controls (`pkg/web` search clients, MCP fetches, the skills installer), CIDR-level blocking is enforced by `pkg/security/SSRFChecker` when `cfg.Sandbox.SSRF.Enabled = true`. Operators with a legitimate internal-service requirement add CIDRs to `cfg.Sandbox.EgressAllowCIDRs` (or the SSRF allow_internal list) to bypass the deny.
- For compiled binaries spawned via `bash` (ADR-036 merged the former `exec`/`workspace_shell`/`workspace_shell_bg` into `bash`; e.g. `curl`, `wget`, custom Go programs), the SSRFChecker does not apply — only the kernel port allow-list does. CIDR-level enforcement for these binaries would require eBPF cgroup `BPF_CGROUP_INET4_CONNECT` and is deferred. Operators concerned about RFC1918 access from compiled children should set `bash`'s tool-policy to `deny` or `ask` (CLAUDE.md hard constraint 6 — `bash` has no feature-flag gate, only an explicit per-agent tool-policy entry) on agents that handle untrusted content.

**Graceful degradation:** on kernels with Landlock ABI < 4 (no `NET_CONNECT_TCP`), the connect rules are computed but ignored by the kernel. The `pkg/security/SSRFChecker` Go-side defence still runs for HTTP clients the gateway controls; raw-TCP egress is unrestricted on these older kernels. Plan to upgrade to kernel 6.8+ for full kernel enforcement.

---

## `gateway.public_url` sets the canonical origin

Set `public_url` in `~/.omnipus/config.json` when users reach the gateway through a reverse proxy:

```json
{
  "gateway": {
    "public_url": "https://omnipus.example.com"
  }
}
```

The gateway uses this value for preview URLs, Cross-Origin Resource Sharing, WebSocket origin checks, and Content Security Policy. A change requires a restart.

---

## Master key backup

The credential store (`~/.omnipus/credentials.json`) is encrypted with a 256-bit key. Losing that key makes every stored secret — API keys, channel tokens, webhook credentials — permanently inaccessible.

Key provisioning priority, rotation procedure, and the auto-generate first-boot behavior are documented in [ADR-004](../internal/architecture/ADR-004-credential-boot-contract.md#master-key-provisioning). Follow the key rotation steps there before decommissioning a server or moving the data directory.

### Master key provisioning

The gateway tries master-key sources in this order and stops at the first one that succeeds:

| Priority | Source | Operator requirement |
|---|---|---|
| 1 | `OMNIPUS_MASTER_KEY` | Supply a 64-character hexadecimal key. |
| 2 | `OMNIPUS_KEY_FILE` | Point to a regular file containing that key with mode `0600`. |
| 3 | `$OMNIPUS_HOME/master.key` | Keep the generated or installed file at mode `0600`. |
| 4 | Fresh-install generation | Available only when neither a key nor `credentials.json` already exists. |
| 5 | Interactive passphrase | Requires an attached terminal. |

The encrypted store is `$OMNIPUS_HOME/credentials.json`. Omnipus uses AES-256-GCM and writes the store with mode `0600`. An existing store with no usable key causes startup to fail. Omnipus never generates a replacement key over existing encrypted data.

For unattended deployments, provision `OMNIPUS_MASTER_KEY` or `OMNIPUS_KEY_FILE`. Back up the corresponding key separately from `credentials.json`. A copy of the encrypted store without its key cannot be recovered.

## Sensitive-value filtering

Resolved credential values are registered at startup and again whenever the credential set changes (configuration reload, settings save, provider sign-in). Omnipus uses that complete current set to scrub tool results and other selected content before it reaches a model. It is not applied to the audit log, with one exception: the standard parameters of third-party agents, in run entries and in Settings change entries, pass through it while `filter_sensitive_data` is on and are held as typed when it is off. See "Audit log redaction" below, which matches credential formats, not your registered values, and always runs.

Filtering is best-effort and can be switched off. Configure it under `tools` in `config.json`:

```json
{
  "tools": {
    "filter_sensitive_data": true,
    "filter_min_length": 8
  }
}
```

`filter_sensitive_data` defaults to `true`. `filter_min_length` defaults to `8`; shorter content bypasses this filter, except the audit copy of third-party agents' standard parameters, which has no minimum. The matching environment variables are `OMNIPUS_TOOLS_FILTER_SENSITIVE_DATA` and `OMNIPUS_TOOLS_FILTER_MIN_LENGTH`.

This filter only covers credential values Omnipus has registered. It does not recognise credential formats, and it does not discover every secret in arbitrary text. Treat any suspected exposure as real and rotate the affected credential.

### PDFs sent to the model as documents

When a model reads PDFs, a PDF the user attaches in chat, or one an agent opens with `read_file` or `library_read`, can be sent to the model as the original file rather than as extracted text. That file is not scanned: neither the filter above nor any other check looks inside it for credentials, personal data or hidden instructions. A secret written in a PDF, or an instruction hidden in its text or layout, reaches the model provider unchanged. Do not give agents PDFs whose contents must not leave the host, and treat instructions found in a PDF as untrusted content (see "Run by trusted users only" below).

- What is sent: the exact bytes of the file, inline in the request. Omnipus decides this again for every request to a provider, using that model's entry in the model list. The entry must list PDF input, and the engine's effective `pdf_limits.inline_pdf` setting for the provider/model must not be false. A model that does not read PDFs gets the extracted text instead, if the file has any; in that case no extra note says why the file was not sent. If the file has no extractable text (for example a scanned one), the model sees only a note that the content is unavailable.
- Document limits: all limits come from engine configuration, not the model list. The embedded table supplies defaults and researched provider/model overrides; the operator's `pdf_limits` section overlays each matching row per field. Resolution uses the exact model, then matching model prefixes from longest to shortest, then the provider, then the default. See [PDF delivery limits](configuration.md#pdf-delivery-limits-pdf_limits) for the shipped defaults and how to change or neutralize an override. A positive number is an inclusive limit; an explicit zero means no limit; a limit that is missing at every level is not enforced by Omnipus (the provider may still refuse the file). Newer PDFs are considered first: later messages first and, inside a message, later attachments first. A PDF that does not fit is shown as extracted text, if it has any, with a note naming the limit and its value. If Omnipus cannot count a file's pages, it counts zero pages for the page limits, but the file still counts toward the byte and document limits. File size limits count the decoded file; `max_request_bytes` counts the whole request as sent. When a request is too large, the oldest PDFs are changed to text first, one at a time. A request with only text can still be over the provider's limit, and then fails.
- Over the media size setting: a file over the `max_media_size` setting also gets its extracted text with a note. A PDF with no text in that case gives an error for `read_file` and `library_read`, and for a chat attachment a note that its content is unavailable. If the file could not be read locally because the parser was busy or timed out (see below), `read_file` and `library_read` give an error that names that reason (`parser busy, try again later` or `timed out`) and says to read the file again later, instead of saying it has no text.
- One recovery per turn: if a provider rejects a request that carried a PDF (for example as an unsupported file, a bad request, too large or too long for the model), Omnipus tries once more in that turn with extracted text. That one recovery is then used up: every later PDF in the same turn, including files read afterwards, stays non-native. On a model that otherwise reads PDFs the note is `[native PDF rejected by the provider; extracted text shown]` or, without text, `[native PDF rejected by the provider; no extractable text, content unavailable]`. If the file was not read locally because the parser was busy or timed out, the note keeps that reason instead: `[native PDF rejected by the provider; it could not be read locally (parser busy, try again later), content unavailable]` (or `(timed out)`). On a model that does not accept PDF input, the ordinary rule still applies: extracted text comes with no extra reason note, and without text the note is `[this model does not accept PDF input here, so this PDF's content is unavailable]`. Sign-in, missing-model, content-policy, billing, rate-limit and temporary failures do not trigger it. The retry itself can still fail.
- 25 MiB cap on reading files: `read_file` and `library_read` refuse any PDF over 25 MiB (26,214,400 bytes) with `document too large to extract (limit 26214400 bytes); use a dedicated tool`, even if the media size setting and the model's limits would allow more. A chat PDF over the media size setting is read by the general document reader, which has the same 25 MiB cap, so it too is refused when larger, even if it has text.
- Files this computer cannot read: Omnipus's own parser opens PDF versions 1.0 to 1.7. When it cannot open a file (for example one it cannot decrypt, a newer version or a damaged file), `read_file` and `library_read` return `[document: <name> | could not be read locally (<reason>) | sent as is to PDF-capable models]`, where the reason is `encrypted`, `unsupported version` or `malformed`, or one of two reasons that describe the computer rather than the file: `parser busy, try again later` when too many PDFs are already being read at once (the reading of PDFs is limited for the whole of Omnipus, shared by `read_file`, `library_read`, chat attachments and the general document reader), and `timed out` when reading the file took longer than the overall time allowed for one PDF. For these two, reading the file again later can succeed. Encryption alone is not a failure: an encrypted PDF that opens with an empty password is read like any other, as text or as a scanned file. This differs from a scanned file (no text layer): the model can still get the original file if every other condition holds. A file named `.pdf` that does not start with the `%PDF-` header is refused. A PDF whose file name ends in `.png`, `.jpg`, `.jpeg`, `.heic` or `.heif` is handled as an image first and fails there, so do not give PDFs such names.
- Damaged attachment data: `[PDF omitted: malformed data]` means the PDF part of a request was not valid, non-empty base64 data, so no file was sent in that place. That is a transport problem, not a parser limit.
- What is kept: a PDF opened by `read_file` or `library_read` is sent only within the turn that read it. The chat history, transcript and archive keep the extracted text and a short notice, never the file. A PDF attached in chat stays in the conversation like any attachment and is sent again on later turns to models that read PDFs.
- Access: before a file opened by a tool is sent, Omnipus checks again that the agent may still read it. If it may not, the file is not sent and the model sees `[PDF view unavailable: access denied]`.
- Safety limits on reading text: Omnipus reads a PDF's text with its own parser, which stops on a file that needs more work, more time or more text than it allows for one file. The time limit (about ten seconds) is checked as the parser works, not at an exact moment, so a stop can come somewhat later. These limits protect Omnipus from hostile files; they are not model or provider limits, and a file can reach them while being well under the size and page limits. What a stop leaves depends on the path that read the file:
  - `read_file`, `library_read` and a PDF attached in chat within the media size setting keep the text of the pages read before the stop, ending with `…[truncated]`. If the stop comes before any readable text, the file is treated as `could not be read locally (malformed)`.
  - A PDF attached in chat over the media size setting is read by the general document reader. When the text allowance runs out, the text read so far is kept and ends with `…[truncated]`, or, with no text yet, the file is reported as "pdf exceeds the parser's safety limits before any text". When the work or time limit stops it, no text is kept and the file is reported as unreadable, with the reason.

  Re-reading the file, or paging with `offset` and `length`, reads it again from the start under the same limits: it never continues past the cut. A cut caused by the time limit can fall at a slightly different place on a busier or quieter host. A stop before any text differs from a PDF that simply has no text layer, which is reported as having no extractable text. A PDF whose character map (the table that turns its character codes into letters) declares more entries than it contains, or more than the parser allows, is also reported as unreadable locally. Other faults in a character map are not all detected: for some, the text is shown as the raw character codes. A model that reads PDFs still receives the file itself only when the file is within the model's limits and the media size setting and is read without `offset` and `length`. Otherwise: paging a file that could not be read locally, or that has no text, returns an error; and a `read_file` or `library_read` of a file over the media size setting with no readable text returns an error instead of a notice.
- Without a loaded model list, Omnipus cannot tell whether a model reads PDFs, so a request with a PDF fails with "PDF not sent: the model list is not loaded …" instead of guessing. Requests without PDFs are not affected.

### Judge evidence redaction

Everything the goal Judge is given as evidence — the working chat it reviews (messages, tool settings, errors and tool results) and the output of machine checks — is scrubbed before the Judge sees it and before a check's record is saved. This always runs: `filter_sensitive_data` and `filter_min_length` do not affect it. It removes every credential value Omnipus has registered, whatever its length, and the credential formats the audit log redacts (listed below, including the password in a URL). Email addresses are kept, so a criterion such as "email the order to supplier@x.com" can be checked against the recipient the tool was given. The exception is an email used as the login in a web address that also carries a password (`https://name@example.com:PASSWORD@host`): the whole login part is removed. Each removed value is replaced with a visible marker such as `[REDACTED]`, so the Judge can tell that something was removed. Redaction runs before any text is cut to its size limit.

The Judge's own read-only tools can still open files and session records in the workspace under review; this scrubbing covers the evidence Omnipus hands it, not what it reads itself.

### Audit log redaction

Separately from the filter above, the audit logger redacts every entry before it writes it: the command, parameters and details of tool-call and shell entries, and the fields of structured event records, pass through the credential patterns (including nested maps and lists of any type). The patterns cover `sk-…` and `key-…` keys, `Bearer …` tokens (any letter case), GitHub `ghp_…`/`gho_…` tokens, Slack `xoxb-…`/`xoxp-…` tokens, AWS access-key IDs (`AKIA…`/`ASIA…`), Google `ya29.…` access tokens, JSON Web Tokens, and the password in a URL such as `postgres://user:PASSWORD@host` or `redis://:PASSWORD@host` (only the password is replaced; a raw `/` or `@` inside the password, and scheme-less `user:pw@tcp(…)` strings, are not caught). A key prefix directly after a letter or digit is not treated as a key, so names like `project-task-…` or `risk-assessment` are left intact; after any other character, including an encoded one such as `%3D`, `\u0022` or a literal `\n`, it is. A path segment or skill slug that itself starts like a key (for example `/sk-learn-…`) is therefore redacted, so last-writer and last-invoked lookups return not-found for it. A value whose field name is exactly one of a short list (`password`, `token`, `api_key`, `secret`, `authorization` and similar; case, `_` and `-` are ignored) becomes `[REDACTED]`; a field such as `github_token` is not caught by name. There is no setting to turn it off. Redaction runs before the entry is signed, so the tamper-evident chain covers the redacted text and still verifies. Security-setting-change records keep their own, broader name-based redaction (`***redacted***`) and also pass through the credential patterns. Email addresses are deliberately not redacted in the audit log, so mail recipients and Message-IDs are recorded in full.

When an audit write fails, any gateway log line that includes the command carries it with the same credential patterns applied; most such lines do not include the command at all.

Audit files written before this was switched on (issue #914) are not rewritten — rewriting them would break the signature chain. They may contain credentials typed into `bash`, `web_serve` or `environment_setup` commands; rotate those credentials if that matters.

---

## Run by trusted users only

The gateway executes tool calls on behalf of the active agent and the user directing it. A user with chat access can instruct agents to read files, run shell commands (subject to tool policy), and make outbound HTTP requests. This is by design — the product is an agentic runtime.

Operators should extend chat access only to users they trust with shell-level capabilities on the host. T-06 in the [Threat Model](../internal/specs/chat-served-iframe-preview-spec.md#threat-model) covers the trusted-prompt boundary and what happens when an agent receives instructions from untrusted content (for example, an HTML file fetched from the web).

---

## Shell access from connectors

Shell access is governed by the `bash` tool policy, regardless of whether a request arrives in the web app or through a connector. Set `bash` to Deny for agents that must not run commands, or Ask when a person will be present to approve each call.
