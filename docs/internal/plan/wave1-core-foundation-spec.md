# Feature Specification: Wave 1 -- Core Foundation

**Created**: 2026-03-28
**Status**: Draft
**Input**: Omnipus BRD v1.0 (sections 4-5), Appendix E (Data Model), discovery session confirming Wave 1 scope

---

## User Stories & Acceptance Criteria

### User Story 1 -- Directory Initialization (Priority: P0)

An operator installs Omnipus for the first time and runs the binary. The system must bootstrap the `~/.omnipus/` directory tree per Appendix E section E.3 so that all subsequent operations (config loading, session writing, credential storage) have a valid filesystem layout. Without this, nothing else works.

**Why this priority**: Every other feature depends on the directory structure existing. This is the absolute foundation.

**Independent Test**: Run the binary on a clean system with no `~/.omnipus/` directory. Verify the complete directory tree is created with correct permissions.

**Acceptance Scenarios**:

1. **Given** no `~/.omnipus/` directory exists, **When** `omnipus` is launched, **Then** the full directory tree is created (`config.json`, `agents/`, `tasks/`, `pins/`, `system/`, `backups/`, `skills/`, `channels/`) with `0700` permissions on the root directory.
2. **Given** a partial `~/.omnipus/` directory exists (e.g., missing `tasks/`), **When** `omnipus` is launched, **Then** only missing directories/files are created; existing files are not overwritten.
3. **Given** `~/.omnipus/` exists but is not writable, **When** `omnipus` is launched, **Then** the system exits with a clear error message naming the path and the permission issue.

---

### User Story 2 -- Config File Loading and Validation (Priority: P0)

An operator configures Omnipus by editing `~/.omnipus/config.json`. The system must load, validate, and apply this configuration at startup, providing clear error messages for malformed or invalid configs. Existing Omnipus-style configs must be loadable (additive extension, not breaking change).

**Why this priority**: Configuration drives all runtime behavior -- agents, models, tools, channels.

**Independent Test**: Provide various `config.json` files (valid, malformed, missing required fields, extra unknown fields) and verify correct loading or error reporting.

**Acceptance Scenarios**:

1. **Given** a valid `config.json` with agent definitions and model config, **When** `omnipus` starts, **Then** all agents and settings are loaded and accessible at runtime.
2. **Given** a `config.json` with unknown fields (forward compatibility), **When** `omnipus` starts, **Then** unknown fields are preserved (round-trip safe) and a debug-level log is emitted.
3. **Given** a `config.json` with invalid JSON syntax, **When** `omnipus` starts, **Then** the system exits with an error identifying the line/character position of the parse error.
4. **Given** a `config.json` with a required field missing (e.g., `agents.list[0].id`), **When** `omnipus` starts, **Then** the system exits with an error naming the missing field and its JSON path.
5. **Given** no `config.json` exists, **When** `omnipus` starts, **Then** a default config is generated with sensible defaults (no agents configured, empty provider list) and a log message indicates first-run setup is needed.

---

### User Story 3 -- Credential Encryption at Rest (Priority: P0)

An operator stores API keys for LLM providers. The system must encrypt credentials using AES-256-GCM with Argon2id key derivation per SEC-23/23a-e, storing them in `~/.omnipus/credentials.json`. Raw credentials must never appear in `config.json` or any log output.

**Why this priority**: Credentials are required to connect to any LLM provider. Storing them in plaintext is a security violation per the BRD.

**Independent Test**: Store a credential, verify the file on disk contains only encrypted ciphertext, then decrypt and verify the original value is recovered.

**Acceptance Scenarios**:

1. **Given** a master key is available, **When** a credential is stored via `omnipus credentials set ANTHROPIC_API_KEY <value>`, **Then** `credentials.json` contains `{"version": 1, "salt": "<base64>", "credentials": {"ANTHROPIC_API_KEY": {"nonce": "<base64>", "ciphertext": "<base64>"}}}`.
2. **Given** an encrypted `credentials.json` and a valid master key, **When** the system needs a credential at runtime, **Then** it decrypts and returns the original plaintext value.
3. **Given** an encrypted `credentials.json` and an incorrect master key, **When** decryption is attempted, **Then** AES-GCM authentication fails and the system returns an error (not garbage plaintext).
4. **Given** a credential is stored, **When** `config.json` is inspected, **Then** only a `_ref` key (e.g., `"api_key_ref": "ANTHROPIC_API_KEY"`) appears, never the raw value.

---

### User Story 4 -- Credential Key Provisioning Modes (Priority: P0)

An operator deploys Omnipus in different environments (headless server, interactive desktop, CI/CD). The system must support three key provisioning modes per SEC-23b, tried in order: (1) `OMNIPUS_MASTER_KEY` env var, (2) `OMNIPUS_KEY_FILE` path, (3) interactive passphrase prompt.

**Why this priority**: Without a key provisioning mechanism, encrypted credentials cannot be accessed. This gates all LLM provider connectivity.

**Independent Test**: Test each provisioning mode independently by setting/unsetting environment variables and verifying key resolution.

**Acceptance Scenarios**:

1. **Given** `OMNIPUS_MASTER_KEY` is set to a hex-encoded 256-bit key, **When** `omnipus` starts, **Then** that key is used directly (bypassing Argon2id KDF) and credentials are accessible.
2. **Given** `OMNIPUS_MASTER_KEY` is not set but `OMNIPUS_KEY_FILE` points to a file with `0600` permissions containing a hex key, **When** `omnipus` starts, **Then** the key is read from the file and credentials are accessible.
3. **Given** `OMNIPUS_KEY_FILE` points to a file with `0644` permissions (world-readable), **When** `omnipus` starts, **Then** the system refuses to use the key file, logs a security warning, and falls through to the next provisioning mode.
4. **Given** no env vars are set and a TTY is available, **When** `omnipus` starts for the first time, **Then** it prompts for a passphrase, derives the key via Argon2id (time=3, memory=64MB, parallelism=4), and uses the derived key.
5. **Given** no env vars are set and no TTY is available, **When** `omnipus` starts, **Then** it starts with credentials inaccessible, logs a warning, and providers requiring credentials fail gracefully with a clear message.

---

### User Story 5 -- Session Day-Partitioned Storage (Priority: P0)

An operator has long-running conversations that span multiple days. The system must store session transcripts as day-partitioned JSONL files per Appendix E section E.5.2, with a `meta.json` file tracking session metadata and partition list.

**Why this priority**: Sessions are the primary data artifact. Day-partitioning is required for retention policies and prevents unbounded file growth.

**Independent Test**: Create a session, send messages across two calendar days (UTC), and verify two partition files exist with correct content.

**Acceptance Scenarios**:

1. **Given** a new session is started, **When** the first message is sent at 2026-03-28T10:00:00Z, **Then** `sessions/<id>/meta.json` and `sessions/<id>/2026-03-28.jsonl` are created.
2. **Given** an active session with messages on 2026-03-28, **When** a message arrives at 2026-03-29T00:00:01Z, **Then** a new partition `2026-03-29.jsonl` is created and `meta.json.partitions` is updated to include both files.
3. **Given** a session with multiple partitions, **When** `meta.json` is read, **Then** `stats` (tokens_in, tokens_out, cost, tool_calls, message_count) reflect aggregates across all partitions.
4. **Given** a message with tool calls, **When** it is appended to a partition, **Then** the JSONL entry includes `tool_calls` array with `id`, `tool`, `status`, `duration_ms`, `parameters`, and `result`.

---

### User Story 6 -- Multi-Agent Routing (Priority: P0)

An operator runs multiple agents (e.g., personal assistant and work assistant) on the same Omnipus instance. The system must route inbound messages from channels to the correct agent based on routing rules (FUNC-10, FUNC-10a), with a default agent for unmatched messages.

**Why this priority**: Multi-agent is the primary orchestration capability for Wave 1 and a core differentiator.

**Independent Test**: Configure two agents with routing rules mapping different channel/user patterns. Send messages matching each pattern and verify correct agent receives each.

**Acceptance Scenarios**:

1. **Given** two agents configured with channel routing rules (Telegram user 123 -> agent-A, Telegram user 456 -> agent-B), **When** a message arrives from user 123 on Telegram, **Then** agent-A processes it.
2. **Given** a message arrives from an unrecognized user/channel with no matching routing rule, **When** a default agent is configured, **Then** the default agent processes it.
3. **Given** a message arrives from an unrecognized user/channel with no matching routing rule and no default agent, **When** the MessageBus attempts routing, **Then** the message is rejected with a log entry and the sender receives an error response.
4. **Given** routing rules are defined in `config.json` under `channels.<id>.policies.routing_rules`, **When** the config is loaded, **Then** all routing rules are registered in the MessageBus.

---

### User Story 7 -- Per-Agent Isolated Workspaces (Priority: P0)

An operator runs multiple agents that must not access each other's data. Each agent gets its own workspace directory (FUNC-11) with independent sessions, memory, skills, and heartbeat. Filesystem isolation is enforced.

**Why this priority**: Workspace isolation is a security requirement and prerequisite for multi-agent operation.

**Independent Test**: Start two agents, have each write to their workspace, and verify neither can read the other's files.

**Acceptance Scenarios**:

1. **Given** an agent `general-assistant` is activated, **When** the workspace is initialized, **Then** `~/.omnipus/agents/general-assistant/` is created with subdirectories: `sessions/`, `memory/`, `skills/`.
2. **Given** two agents are running, **When** agent-A attempts to read a path inside agent-B's workspace via a tool, **Then** the operation is denied at the application level (Wave 1 enforcement; kernel-level Landlock enforcement is a separate BRD requirement).
3. **Given** an agent definition in `config.json` with no explicit `workspace` field, **When** the agent is loaded, **Then** the workspace path defaults to `~/.omnipus/agents/<agent-id>/`.

---

### User Story 8 -- Streaming Response Output (Priority: P0)

An operator or user interacting via WebUI or CLI expects real-time token-by-token streaming from the LLM (FUNC-26). The system must stream tokens as they arrive from the provider, using SSE for WebUI and chunked output for CLI/channels.

**Why this priority**: Streaming is essential for responsive UX. Without it, users see nothing until the full response completes, which can take 30+ seconds for complex queries.

**Independent Test**: Send a prompt, verify that partial tokens arrive at the client before the full response is complete.

**Acceptance Scenarios**:

1. **Given** a WebUI client is connected via SSE, **When** the LLM streams tokens, **Then** each token chunk is delivered as an SSE event with `event: token` and `data: {"content": "<chunk>"}`.
2. **Given** a CLI session, **When** the LLM streams tokens, **Then** tokens are printed to stdout incrementally (no buffering until completion).
3. **Given** a streaming response is in progress, **When** the LLM provider returns an error mid-stream, **Then** the partial response received so far is preserved in the session transcript with `"status": "error"` and the error is surfaced to the user.
4. **Given** a streaming response completes, **When** the full response is assembled, **Then** token counts and cost are computed and written to the session transcript entry and `meta.json` stats.

---

### User Story 9 -- Graceful Shutdown (Priority: P0)

An operator stops Omnipus (SIGTERM, Ctrl+C, service stop). The system must execute a 5-step graceful shutdown per FUNC-36: stop accepting -> wait for in-flight -> save partial -> flush -> exit. Data integrity must be preserved.

**Why this priority**: Unclean shutdowns on constrained hardware or headless deployments are common. Data loss erodes trust.

**Independent Test**: Start a long-running LLM call, send SIGTERM, and verify the partial response is saved and the session is marked as interrupted.

**Acceptance Scenarios**:

1. **Given** no in-flight operations, **When** SIGTERM is received, **Then** the system shuts down within 1 second after flushing all buffered writes.
2. **Given** an in-flight LLM call, **When** SIGTERM is received, **Then** the system waits up to 10 seconds (configurable) for the call to complete.
3. **Given** an in-flight LLM call that does not complete within the timeout, **When** the timeout expires, **Then** the partial streamed response is saved to the session transcript with `"status": "interrupted"` and all buffered data is flushed to disk.
4. **Given** a session was interrupted on previous shutdown, **When** Omnipus restarts and the session is resumed, **Then** the partial response is visible with a system message: "Session interrupted. Last response may be incomplete."
5. **Given** multiple agents have in-flight operations, **When** SIGTERM is received, **Then** all agents are shut down concurrently (not sequentially), respecting the same timeout.

---

### User Story 10 -- Atomic Writes and Concurrency Safety (Priority: P0)

The system handles concurrent access to shared files (config, credentials, session transcripts) safely. All writes use atomic temp-file-plus-rename. Shared files are protected by advisory file locking (`flock`/`LockFileEx`) and single-writer goroutine serialization per Appendix E section E.2.

**Why this priority**: Data corruption from concurrent writes would undermine all other features.

**Independent Test**: Simulate concurrent writes to the same session partition from multiple goroutines. Verify no data loss or corruption.

**Acceptance Scenarios**:

1. **Given** a write to any JSON/JSONL file, **When** the write is executed, **Then** it writes to a temp file in the same directory, then renames atomically. The file is never in a partially-written state.
2. **Given** two goroutines attempt to write to `config.json` simultaneously, **When** both writes are submitted, **Then** they are serialized through a single-writer channel; both complete without corruption.
3. **Given** an external process holds an advisory lock on a file, **When** Omnipus attempts to write, **Then** it blocks until the lock is released or times out with an error (not silently skipping the write).
4. **Given** a crash occurs during a write, **When** the system restarts, **Then** the original file is intact (the temp file may be orphaned but the canonical file is not corrupted).

---

### User Story 11 -- Credential Injection at Runtime (Priority: P0)

When an agent needs to call an LLM provider, the system resolves credential references (`_ref` keys in config) from the encrypted credential store and injects them via environment variables (SEC-22). The agent and tool code never directly read `credentials.json`.

**Why this priority**: This is the runtime counterpart to credential encryption. Without it, encrypted credentials have no path to usage.

**Independent Test**: Configure a provider with `api_key_ref: "ANTHROPIC_API_KEY"`, store the key encrypted, and verify the LLM call receives the decrypted key via environment variable.

**Acceptance Scenarios**:

1. **Given** `config.json` has `"api_key_ref": "ANTHROPIC_API_KEY"` for a provider, **When** the provider is initialized, **Then** the system resolves `ANTHROPIC_API_KEY` from `credentials.json`, decrypts it, and sets it in the process environment.
2. **Given** a credential reference that does not exist in `credentials.json`, **When** the provider is initialized, **Then** initialization fails with a clear error: "Credential 'ANTHROPIC_API_KEY' not found in credential store."
3. **Given** the master key is not available (no env var, no key file, no TTY), **When** a provider requiring credentials is initialized, **Then** it fails with: "Credential store is locked. Provide a master key via OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, or interactive passphrase."

---

### User Story 12 -- CLI Credential Management Commands (Priority: P1)

An operator manages credentials via CLI commands: `omnipus credentials set`, `omnipus credentials list`, `omnipus credentials delete`, `omnipus credentials rotate` (SEC-23c). These commands interact with the encrypted credential store.

**Why this priority**: P1 because operators need a way to manage credentials beyond initial setup, but the core encryption/decryption (P0) must work first.

**Independent Test**: Run each CLI command and verify the credential store is modified correctly.

**Acceptance Scenarios**:

1. **Given** a valid master key, **When** `omnipus credentials set MY_KEY secret123` is run, **Then** the credential is encrypted and added to `credentials.json`.
2. **Given** credentials exist in the store, **When** `omnipus credentials list` is run, **Then** credential names are listed (never values).
3. **Given** a credential exists, **When** `omnipus credentials delete MY_KEY` is run, **Then** the credential is removed from `credentials.json` atomically.
4. **Given** credentials are encrypted with passphrase A, **When** `omnipus credentials rotate` is run, **Then** the operator is prompted for the current passphrase and a new passphrase, all credentials are re-encrypted with the new key, and the file is written atomically.

---

## Behavioral Contract

### Primary flows

- When Omnipus starts with a valid config and accessible credentials, the system initializes all configured agents, sets up the MessageBus with routing rules, and begins accepting messages on configured channels.
- When a message arrives on a channel, the MessageBus matches it against routing rules and dispatches it to the target agent's processing loop.
- When an agent processes a message, it resolves credential references, calls the LLM provider with streaming enabled, and appends the conversation to the current day's session partition.
- When a day boundary (UTC midnight) is crossed during an active session, the system creates a new partition file and updates `meta.json`.
- When `omnipus credentials set NAME VALUE` is executed, the value is encrypted with the master key and written to `credentials.json`; a `_ref` entry is added to `config.json`.

### Error flows

- When `config.json` contains invalid JSON, the system exits with a parse error including line and character position.
- When a credential reference cannot be resolved (missing from store, store locked), the affected provider fails to initialize with a descriptive error; other providers continue.
- When an LLM provider returns a non-retryable error during streaming, the partial response is saved with `"status": "error"` and the error is surfaced to the user.
- When an LLM provider returns a retryable error (429, 503), the system retries with exponential backoff (max 3 retries) before marking the response as failed.
- When the filesystem is full during a write, the atomic write fails (temp file creation fails), the original file is untouched, and the error is logged with the specific path and errno.

### Boundary conditions

- When available disk space drops below 50MB, the system logs a warning on each write operation but does not stop accepting messages.
- When a session partition file exceeds 100MB, a warning is logged but no automatic splitting occurs (partitions are day-based, not size-based).
- When `OMNIPUS_MASTER_KEY` contains a value that is not valid hex or not exactly 64 hex characters (256 bits), the system rejects it with: "Invalid master key: expected 64 hex characters (256 bits)."
- When the Argon2id KDF is running (during passphrase-based key derivation), the system blocks startup for up to ~2 seconds; this is expected and documented.

---

## Edge Cases

- What happens when `credentials.json` exists but is corrupted (invalid JSON)? Expected: the system logs an error, treats the credential store as empty, and refuses to overwrite the corrupted file. The operator must manually fix or delete it.
- What happens when `credentials.json` exists but was encrypted with a different master key? Expected: AES-GCM decryption fails with an authentication error. The system logs "Credential decryption failed -- wrong master key?" and treats credentials as inaccessible.
- What happens when two Omnipus processes start simultaneously against the same `~/.omnipus/`? Expected: the second process detects the advisory lock on `system/state.json` held by the first, waits briefly, then exits with "Another Omnipus instance is running."
- What happens when a session message arrives at exactly UTC midnight (00:00:00.000Z)? Expected: it goes into the new day's partition (the boundary is inclusive at the start).
- What happens when the system clock jumps backward (NTP correction) during a session? Expected: the message is appended to the partition corresponding to the current (corrected) time. If this is an earlier date than the latest partition, the earlier partition is reopened. `meta.json.partitions` remains sorted.
- What happens when context compression triggers during a streaming response? Expected: compression does not occur mid-stream. It is evaluated before sending the next user message to the LLM, not during response streaming.
- What happens when SIGTERM arrives during an atomic write (between temp-file-write and rename)? Expected: the rename has not occurred, so the original file is intact. The orphaned temp file is cleaned up on next startup.
- What happens when SIGTERM arrives during credential rotation (between re-encrypt and atomic write)? Expected: the old `credentials.json` is intact (rename has not happened). Rotation must be retried.
- What happens when an agent's `tools.allow` list references a tool that does not exist? Expected: a warning is logged at startup naming the unknown tool. The agent starts without that tool.
- What happens when `config.json` specifies an agent with `type: "core"` but uses a non-standard `id`? Expected: core agent IDs are validated against the known set; unknown IDs with `type: "core"` trigger a validation error.
- What happens when the passphrase is an empty string? Expected: the system rejects it with "Passphrase must not be empty."
- What happens when `OMNIPUS_KEY_FILE` points to a non-existent file? Expected: the system logs a warning and falls through to the next provisioning mode (interactive passphrase or inaccessible).

---

## Explicit Non-Behaviors

- The system must not store raw credential values in `config.json`, log output, or any file other than the encrypted `credentials.json`, because credential leakage is a critical security violation (SEC-23d).
- The system must not use SQLite, PostgreSQL, Redis, or any database for Omnipus's own data model, because the BRD mandates file-based storage only (Appendix E, E.2). Exception: WhatsApp session via whatsmeow (not in Wave 1).
- The system must not use CGo or shell out to external programs for any security-critical path, because the BRD requires pure Go implementation (constraint 2).
- The system must not modify existing Omnipus config fields or break backward compatibility with Omnipus config format, because new sections must be additive (constraint 5).
- The system must not implement Landlock or seccomp sandboxing in Wave 1, because kernel-level sandboxing is Phase 1 of the BRD delivery phases (separate from Wave 1 core foundation).
- The system must not implement hot-reload of config files in Wave 1, because hot-reload is a Phase 3 feature (SEC-13).
- The system must not queue or buffer undeliverable messages silently, because the BRD specifies explicit rejection with cooldown for rate-limited operations (SEC-26), and unroutable messages must be logged (FUNC-10).
- The system must not cache the derived key in OS keyring in Wave 1, because keyring integration adds platform-specific complexity that can follow in a later wave. The key is held in memory for the process lifetime only.
- The system must not implement RBAC or gateway authentication in Wave 1, because these are Phase 2 features (SEC-19, SEC-20).

---

## Integration Boundaries

### LLM Provider APIs (OpenRouter, Anthropic/Claude, Haiku)

- **Data in**: Chat completion requests (model, messages array, temperature, max_tokens, stream=true)
- **Data out**: Streaming token chunks (SSE for Anthropic, SSE for OpenRouter), final usage stats (tokens, cost)
- **Contract**: HTTPS REST API. Anthropic uses `x-api-key` header, `anthropic-version` header. OpenRouter uses `Authorization: Bearer` header. Both return SSE streams with `data:` lines.
- **On failure**: Retryable errors (429, 503) trigger exponential backoff (3 retries, 1s/2s/4s base). Non-retryable errors (401, 400) fail immediately. Network timeouts default to 30s connect, 120s response.
- **Development**: Real services with test API keys. Mock provider for unit tests implementing the same streaming interface.

### Filesystem (~/.omnipus/)

- **Data in**: JSON/JSONL reads (config, credentials, sessions, meta)
- **Data out**: JSON/JSONL writes (atomic: temp file + rename), directory creation
- **Contract**: POSIX filesystem semantics. Advisory locking via `flock(2)` on Linux, `LockFileEx` on Windows. File permissions: `0600` for credentials, `0644` for config, `0700` for directories.
- **On failure**: Disk full returns error on temp file creation; original file untouched. Permission denied returns error with path. Corrupt JSON returns parse error.
- **Development**: Real filesystem. Tests use `t.TempDir()` for isolation.

### Omnipus Fork (source codebase)

- **Data in**: Omnipus Go source code, existing config format, command patterns, channel implementations
- **Data out**: Renamed binary (`omnipus`), extended config schema, new packages (credentials, datamodel, messagebus, streaming)
- **Contract**: Fork and rename. `omnipus` -> `omnipus` in module path, binary name, and user-facing strings. Existing Omnipus config keys are preserved; Omnipus adds new top-level sections.
- **On failure**: N/A (build-time dependency, not runtime)
- **Development**: Git fork with rename script. CI validates that Omnipus's existing test suite passes after rename.

---

## BDD Scenarios

### Feature: Directory Initialization

#### Scenario: First-run creates complete directory tree

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** no `~/.omnipus/` directory exists
- **When** `omnipus` is launched
- **Then** `~/.omnipus/` is created with permissions `0700`
- **And** subdirectories `agents/`, `tasks/`, `pins/`, `system/`, `backups/`, `skills/`, `channels/`, `projects/` exist
- **And** `~/.omnipus/config.json` exists with default content
- **And** `~/.omnipus/system/state.json` exists

#### Scenario: Partial directory is completed without overwriting

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** `~/.omnipus/` exists with `config.json` containing custom settings
- **And** `~/.omnipus/tasks/` directory does not exist
- **When** `omnipus` is launched
- **Then** `~/.omnipus/tasks/` is created
- **And** `~/.omnipus/config.json` retains the custom settings unchanged

#### Scenario: Unwritable home directory

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Error Path

- **Given** `~/.omnipus/` exists but has permissions `0444`
- **When** `omnipus` is launched
- **Then** the process exits with code 1
- **And** stderr contains "cannot write to ~/.omnipus/: permission denied"

---

### Feature: Config File Loading

#### Scenario: Valid config loads successfully

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `config.json` contains two agents in `agents.list[]` with valid model configs
- **When** `omnipus` starts
- **Then** both agents are registered in the agent registry
- **And** each agent's model configuration matches the config file

#### Scenario: Invalid JSON syntax produces clear error

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Error Path

- **Given** `config.json` contains `{"agents": [}` (invalid JSON)
- **When** `omnipus` starts
- **Then** the process exits with code 1
- **And** stderr contains "config.json: parse error" with position information

#### Scenario: Unknown fields are preserved

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** `config.json` contains `{"future_feature": true, "agents": {"list": []}}`
- **When** `omnipus` starts and later writes back config
- **Then** the `future_feature` field is preserved in the written file

#### Scenario: Missing config triggers first-run defaults

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** no `config.json` exists in `~/.omnipus/`
- **When** `omnipus` starts
- **Then** a default `config.json` is created
- **And** a log message at INFO level says "No config found, created default configuration"

---

### Feature: Credential Encryption

#### Scenario: Store and retrieve a credential

**Traces to**: User Story 3, Acceptance Scenarios 1 and 2
**Category**: Happy Path

- **Given** `OMNIPUS_MASTER_KEY` is set to a valid 64-character hex string
- **When** `omnipus credentials set ANTHROPIC_API_KEY sk-ant-abc123` is executed
- **Then** `credentials.json` contains version 1 format with encrypted entry for `ANTHROPIC_API_KEY`
- **And** the `ciphertext` field is not equal to the base64 encoding of `sk-ant-abc123`
- **And** when the credential is read back at runtime, the value equals `sk-ant-abc123`

#### Scenario: Wrong master key fails decryption

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Error Path

- **Given** credentials were encrypted with master key A
- **And** `OMNIPUS_MASTER_KEY` is now set to a different key B
- **When** the system attempts to decrypt `ANTHROPIC_API_KEY`
- **Then** decryption fails with an authentication error
- **And** the error message contains "decryption failed"

#### Scenario: Credential ref in config, never raw value

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Happy Path

- **Given** `omnipus credentials set OPENROUTER_KEY rk-123` has been executed
- **When** `config.json` is inspected
- **Then** it contains `"api_key_ref": "OPENROUTER_KEY"` (or similar `_ref` pattern)
- **And** the string `rk-123` does not appear anywhere in `config.json`

---

### Feature: Key Provisioning Modes

#### Scenario: Master key from environment variable

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `OMNIPUS_MASTER_KEY` is set to `aabbccdd...` (64 hex chars)
- **And** `credentials.json` contains credentials encrypted with this key
- **When** `omnipus` starts
- **Then** credentials are accessible without any prompt

#### Scenario: Master key from key file

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Happy Path

- **Given** `OMNIPUS_MASTER_KEY` is not set
- **And** `OMNIPUS_KEY_FILE` points to `/home/user/.omnipus-key` with permissions `0600`
- **And** the file contains a valid 64-char hex key
- **When** `omnipus` starts
- **Then** credentials are accessible without any prompt

#### Scenario: Key file with bad permissions is rejected

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path

- **Given** `OMNIPUS_KEY_FILE` points to a file with permissions `0644`
- **When** `omnipus` starts
- **Then** a security warning is logged: "Key file has insecure permissions (0644), expected 0600 or stricter"
- **And** the key file is not read
- **And** the system falls through to the next provisioning mode

#### Scenario Outline: Invalid master key values

**Traces to**: User Story 4
**Category**: Error Path

- **Given** `OMNIPUS_MASTER_KEY` is set to `<invalid_value>`
- **When** `omnipus` starts
- **Then** the system logs `<error_message>` and treats credentials as inaccessible

**Examples**:

| invalid_value | error_message |
|---|---|
| `abcdef` | "Invalid master key: expected 64 hex characters (256 bits), got 6" |
| `zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz` | "Invalid master key: contains non-hex characters" |
| (empty string) | "Invalid master key: expected 64 hex characters (256 bits), got 0" |

#### Scenario: No key available, headless mode

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Edge Case

- **Given** `OMNIPUS_MASTER_KEY` is not set
- **And** `OMNIPUS_KEY_FILE` is not set
- **And** no TTY is available
- **When** `omnipus` starts
- **Then** a warning is logged: "No master key available. Credential store is locked."
- **And** the gateway starts and accepts connections
- **And** any provider requiring credentials fails with "Credential store is locked"

---

### Feature: Session Day-Partitioning

#### Scenario: New session creates partition and metadata

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** agent `general-assistant` is active
- **When** a new session is started with a user message at `2026-03-28T10:00:00Z`
- **Then** `agents/general-assistant/sessions/<session-id>/meta.json` exists with `status: "active"` and `partitions: ["2026-03-28.jsonl"]`
- **And** `agents/general-assistant/sessions/<session-id>/2026-03-28.jsonl` contains the user message as a JSONL entry

#### Scenario: Midnight rollover creates new partition

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Edge Case

- **Given** an active session with partition `2026-03-28.jsonl`
- **And** the last message was at `2026-03-28T23:59:50Z`
- **When** a new message arrives at `2026-03-29T00:00:01Z`
- **Then** `2026-03-29.jsonl` is created in the session directory
- **And** `meta.json.partitions` is `["2026-03-28.jsonl", "2026-03-29.jsonl"]`

#### Scenario: Session stats aggregate across partitions

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a session with partition `2026-03-28.jsonl` containing 5 messages (100 tokens, $0.01)
- **And** partition `2026-03-29.jsonl` containing 3 messages (80 tokens, $0.008)
- **When** `meta.json` is read
- **Then** `stats.message_count` is 8, `stats.tokens_total` is 180, `stats.cost` is 0.018

---

### Feature: Multi-Agent Routing

#### Scenario: Route message to correct agent by channel+user

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** routing rules map Telegram user `123` to `agent-personal`
- **And** routing rules map Telegram user `456` to `agent-work`
- **When** a message from Telegram user `123` arrives
- **Then** the MessageBus dispatches it to `agent-personal`
- **And** the session is created under `agents/agent-personal/sessions/`

#### Scenario: Unrouted message goes to default agent

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** routing rules exist for specific users
- **And** `default_agent_id` is set to `general-assistant`
- **When** a message arrives from an unknown user
- **Then** the MessageBus dispatches it to `general-assistant`

#### Scenario: No default agent and no matching rule

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Error Path

- **Given** no default agent is configured
- **And** no routing rule matches the incoming message
- **When** the message arrives
- **Then** the message is not dispatched
- **And** an audit log entry records the unroutable message with channel and user identifiers

---

### Feature: Per-Agent Workspaces

#### Scenario: Agent activation creates workspace

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** agent `researcher` is defined in config with `status: "active"`
- **When** `omnipus` starts
- **Then** `~/.omnipus/agents/researcher/` exists
- **And** subdirectories `sessions/`, `memory/`, `skills/` exist within it

#### Scenario: Cross-agent workspace access is denied

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Error Path

- **Given** agents `agent-a` and `agent-b` are both active
- **When** `agent-a` attempts to read `~/.omnipus/agents/agent-b/memory/memory.jsonl`
- **Then** the file operation tool returns an access denied error
- **And** an audit log entry records the denied cross-workspace access attempt

---

### Feature: Streaming Response Output

#### Scenario: SSE token streaming to WebUI

**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a WebUI client connected to `/api/v1/sessions/<id>/stream` via SSE
- **When** the LLM produces tokens incrementally
- **Then** each chunk is sent as `event: token\ndata: {"content": "..."}\n\n`
- **And** the final event is `event: done\ndata: {"stats": {...}}\n\n`

#### Scenario: Mid-stream error preserves partial response

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Error Path

- **Given** a streaming response has received 50 tokens
- **When** the LLM provider returns a 500 error
- **Then** the 50 tokens already received are saved to the session partition
- **And** the message entry has `"status": "error"`
- **And** the SSE stream emits `event: error\ndata: {"message": "Provider error"}\n\n`

---

### Feature: Graceful Shutdown

#### Scenario: Clean shutdown with no in-flight operations

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path

- **Given** no LLM calls or writes are in progress
- **When** SIGTERM is received
- **Then** the process exits within 1 second
- **And** all open file handles are flushed and closed

#### Scenario: Shutdown waits for in-flight LLM call

**Traces to**: User Story 9, Acceptance Scenario 2
**Category**: Happy Path

- **Given** an LLM call is in progress with 3 seconds remaining
- **When** SIGTERM is received
- **Then** the system continues receiving tokens
- **And** the response completes normally within the 10-second timeout
- **And** the session is saved before exit

#### Scenario: Shutdown timeout saves partial and exits

**Traces to**: User Story 9, Acceptance Scenario 3
**Category**: Edge Case

- **Given** an LLM call is in progress and will not complete within 10 seconds
- **When** SIGTERM is received and 10 seconds elapse
- **Then** the partial response is saved with `"status": "interrupted"`
- **And** the process exits
- **And** on restart, the session shows the partial response with a system message

#### Scenario: Concurrent agent shutdown

**Traces to**: User Story 9, Acceptance Scenario 5
**Category**: Edge Case

- **Given** agents A, B, and C all have in-flight LLM calls
- **When** SIGTERM is received
- **Then** all three agents enter shutdown concurrently
- **And** the total shutdown time is bounded by the slowest agent (max timeout), not the sum

---

### Feature: Atomic Writes and Concurrency

#### Scenario: Atomic write survives crash

**Traces to**: User Story 10, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a session partition `2026-03-28.jsonl` with 10 entries
- **When** a new entry is being written (temp file created, not yet renamed)
- **And** the process is killed
- **Then** on restart, `2026-03-28.jsonl` has exactly 10 entries (the 11th was not committed)

#### Scenario: Concurrent writes to config are serialized

**Traces to**: User Story 10, Acceptance Scenario 2
**Category**: Edge Case

- **Given** two goroutines submit config writes simultaneously
- **When** both writes complete
- **Then** the final `config.json` is valid JSON
- **And** the last write wins (deterministic ordering via single-writer channel)

---

### Feature: Credential Injection

#### Scenario: Provider resolves credential ref at startup

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `config.json` has provider `anthropic` with `"api_key_ref": "ANTHROPIC_API_KEY"`
- **And** `credentials.json` has an encrypted entry for `ANTHROPIC_API_KEY`
- **And** the master key is available
- **When** the Anthropic provider initializes
- **Then** the decrypted API key is injected via environment variable
- **And** the provider successfully authenticates

#### Scenario: Missing credential ref fails provider init

**Traces to**: User Story 11, Acceptance Scenario 2
**Category**: Error Path

- **Given** `config.json` references `"api_key_ref": "NONEXISTENT_KEY"`
- **And** `credentials.json` has no entry for `NONEXISTENT_KEY`
- **When** the provider initializes
- **Then** it returns an error containing "Credential 'NONEXISTENT_KEY' not found"
- **And** other providers that do not depend on this credential still initialize

---

### Feature: CLI Credential Management

#### Scenario: Set a new credential

**Traces to**: User Story 12, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the master key is available
- **And** `credentials.json` does not contain `MY_KEY`
- **When** `omnipus credentials set MY_KEY secret_value` is executed
- **Then** exit code is 0
- **And** `credentials.json` now contains an encrypted entry for `MY_KEY`

#### Scenario: List credentials shows names only

**Traces to**: User Story 12, Acceptance Scenario 2
**Category**: Happy Path

- **Given** `credentials.json` contains entries for `KEY_A` and `KEY_B`
- **When** `omnipus credentials list` is executed
- **Then** stdout shows `KEY_A` and `KEY_B`
- **And** no decrypted values appear in the output

#### Scenario: Rotate credentials re-encrypts all entries

**Traces to**: User Story 12, Acceptance Scenario 4
**Category**: Happy Path

- **Given** `credentials.json` contains 3 encrypted credentials
- **And** the current passphrase is `old_pass`
- **When** `omnipus credentials rotate` is executed
- **And** the operator enters `old_pass` as the current passphrase
- **And** the operator enters `new_pass` as the new passphrase
- **Then** all 3 credentials are re-encrypted with the key derived from `new_pass`
- **And** the salt in `credentials.json` changes
- **And** all 3 credentials can be decrypted with the new passphrase

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Individual functions (crypto, config parsing, partition naming, routing logic) | Validates isolated logic correctness |
| Integration | Module interactions (credential store + config loader, MessageBus + agent registry, session writer + filesystem) | Validates components work together |
| E2E | Full workflows (startup, message routing, credential lifecycle, shutdown) | Validates complete feature from user perspective |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestArgon2idKeyDerivation` | Unit | Scenario: Store and retrieve a credential | Verify Argon2id produces deterministic 256-bit key from passphrase+salt with correct parameters (time=3, mem=64MB, par=4) |
| 2 | `TestAES256GCMEncryptDecrypt` | Unit | Scenario: Store and retrieve a credential | Verify encrypt-then-decrypt round-trip for known plaintext |
| 3 | `TestAES256GCMDecryptWrongKey` | Unit | Scenario: Wrong master key fails decryption | Verify decryption with wrong key returns authentication error, not garbage |
| 4 | `TestCredentialStoreFileFormat` | Unit | Scenario: Store and retrieve a credential | Verify serialized JSON matches `{"version": 1, "salt": ..., "credentials": {...}}` |
| 5 | `TestMasterKeyFromHex` | Unit | Scenario: Master key from environment variable | Verify hex decoding of 64-char string to 32-byte key |
| 6 | `TestMasterKeyFromHexInvalid` | Unit | Scenario Outline: Invalid master key values | Verify rejection of short, non-hex, and empty values |
| 7 | `TestKeyFilePermissionCheck` | Unit | Scenario: Key file with bad permissions is rejected | Verify permission check rejects `0644`, accepts `0600`, `0400` |
| 8 | `TestKeyProvisioningOrder` | Unit | Scenario: Master key from environment variable | Verify env -> file -> passphrase fallback chain |
| 9 | `TestConfigParseValid` | Unit | Scenario: Valid config loads successfully | Parse a valid config JSON and verify all fields are populated |
| 10 | `TestConfigParseInvalidJSON` | Unit | Scenario: Invalid JSON syntax produces clear error | Verify parse error includes position info |
| 11 | `TestConfigPreservesUnknownFields` | Unit | Scenario: Unknown fields are preserved | Round-trip config with unknown fields via `json.RawMessage` |
| 12 | `TestConfigDefaultGeneration` | Unit | Scenario: Missing config triggers first-run defaults | Verify default config has expected structure |
| 13 | `TestSessionPartitionNaming` | Unit | Scenario: New session creates partition and metadata | Verify `YYYY-MM-DD.jsonl` name from timestamp |
| 14 | `TestSessionPartitionMidnightBoundary` | Unit | Scenario: Midnight rollover creates new partition | Verify 23:59:59Z -> same day, 00:00:00Z -> new day |
| 15 | `TestMessageBusRouting` | Unit | Scenario: Route message to correct agent by channel+user | Verify routing rule matching logic |
| 16 | `TestMessageBusDefaultAgent` | Unit | Scenario: Unrouted message goes to default agent | Verify fallback to default agent |
| 17 | `TestMessageBusNoMatch` | Unit | Scenario: No default agent and no matching rule | Verify error returned for unroutable message |
| 18 | `TestAtomicWrite` | Unit | Scenario: Atomic write survives crash | Verify temp-file-plus-rename pattern |
| 19 | `TestAdvisoryFileLock` | Unit | Scenario: Concurrent writes to config are serialized | Verify flock acquisition and release |
| 20 | `TestDirectoryInitialization` | Integration | Scenario: First-run creates complete directory tree | Create full tree in temp dir, verify structure |
| 21 | `TestDirectoryInitPartialExists` | Integration | Scenario: Partial directory is completed without overwriting | Verify additive behavior |
| 22 | `TestCredentialStoreIntegration` | Integration | Scenario: Store and retrieve a credential | Full set-encrypt-persist-load-decrypt cycle |
| 23 | `TestCredentialInjection` | Integration | Scenario: Provider resolves credential ref at startup | Config ref -> credential store -> decrypted env var |
| 24 | `TestCredentialMissingRef` | Integration | Scenario: Missing credential ref fails provider init | Verify error propagation from store to provider |
| 25 | `TestSessionWriteIntegration` | Integration | Scenario: New session creates partition and metadata | Full session create -> message append -> meta update |
| 26 | `TestSessionMultiPartition` | Integration | Scenario: Midnight rollover creates new partition | Cross-day message appending |
| 27 | `TestSessionStatsAggregation` | Integration | Scenario: Session stats aggregate across partitions | Multi-partition stats rollup |
| 28 | `TestMultiAgentRoutingIntegration` | Integration | Scenario: Route message to correct agent by channel+user | MessageBus + agent registry + session creation |
| 29 | `TestWorkspaceIsolation` | Integration | Scenario: Cross-agent workspace access is denied | Two agents, cross-read attempt, verify denial |
| 30 | `TestConcurrentConfigWrites` | Integration | Scenario: Concurrent writes to config are serialized | Multiple goroutines writing config, verify no corruption |
| 31 | `TestStartupE2E` | E2E | Scenarios: First-run, Valid config | Full binary startup on clean dir, verify all initialization |
| 32 | `TestCredentialLifecycleE2E` | E2E | Scenarios: Set, List, Rotate | Full CLI credential management cycle |
| 33 | `TestMessageRoutingE2E` | E2E | Scenarios: Routing happy path | Start gateway, inject test message, verify agent processing |
| 34 | `TestGracefulShutdownE2E` | E2E | Scenarios: Shutdown timeout, partial save | Start LLM call, send SIGTERM, verify partial save |
| 35 | `TestStreamingE2E` | E2E | Scenario: SSE token streaming | Connect SSE client, trigger LLM call, verify incremental tokens |

### Test Datasets

#### Dataset: Credential Encryption Inputs

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Passphrase: `"correcthorsebatterystaple"`, Salt: 16 random bytes | Normal | 32-byte derived key, deterministic for same passphrase+salt | BDD: Store and retrieve | Standard Argon2id derivation |
| 2 | Passphrase: `"a"` (1 char) | Minimum | Valid 32-byte key | BDD: Store and retrieve | Short but non-empty passphrase is allowed |
| 3 | Passphrase: `""` (empty) | Invalid | Rejection error | BDD: Store and retrieve | Empty passphrase is rejected before KDF |
| 4 | Passphrase: 1000 unicode chars | Maximum | Valid 32-byte key | BDD: Store and retrieve | Argon2id handles arbitrary-length input |
| 5 | Hex key: `"aa"*32` (64 hex chars) | Normal env key | 32-byte key `[0xaa]*32` | BDD: Master key from env | Direct hex decode, no KDF |
| 6 | Hex key: `"aa"*31` (62 hex chars) | Invalid | Rejection: wrong length | BDD: Invalid master key | Not 256 bits |
| 7 | Hex key: `"gg"*32` (64 non-hex chars) | Invalid | Rejection: non-hex | BDD: Invalid master key | Invalid hex characters |
| 8 | Plaintext: `"sk-ant-api03-very-long-key-..."` (200 chars) | Large credential | Encrypted, decrypts to exact original | BDD: Store and retrieve | Real-world API key length |
| 9 | Plaintext: `""` (empty string) | Edge | Encrypted, decrypts to `""` | BDD: Store and retrieve | AES-GCM handles empty plaintext |
| 10 | Plaintext: binary-like `"\x00\xff\x00\xff"` | Edge | Encrypted, decrypts to exact original | BDD: Store and retrieve | Non-UTF8 content |

#### Dataset: Config Parsing

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Valid JSON with 2 agents, full model config | Normal | Parsed config with 2 agents | BDD: Valid config | Happy path |
| 2 | `{}` (empty object) | Minimum | Valid config with all defaults | BDD: Valid config | Empty config is valid |
| 3 | `{"agents": {"list": []}}` | Minimum agents | Valid config, zero agents | BDD: Valid config | No agents is valid |
| 4 | `{"agents": {"list": [{"id": "a", "type": "core", "model": {}}]}}` | Minimum agent fields | Validation error: model.provider required | BDD: Invalid config | Missing required nested field |
| 5 | Truncated JSON: `{"agents":` | Invalid | Parse error with position | BDD: Invalid JSON | Unexpected EOF |
| 6 | JSON with trailing comma: `{"a": 1,}` | Invalid | Parse error | BDD: Invalid JSON | Strict JSON, no trailing commas |
| 7 | 10MB JSON file | Performance | Parses within 1 second | BDD: Valid config | Stress test |
| 8 | JSON with unicode keys | Edge | Parsed correctly | BDD: Valid config | Go encoding/json handles this |
| 9 | Config with `future_field: true` + valid agents | Forward compat | Field preserved on round-trip | BDD: Unknown fields preserved | json.RawMessage for unknown fields |

#### Dataset: Session File Edge Cases

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Message at `2026-03-28T23:59:59.999Z` | Boundary | Written to `2026-03-28.jsonl` | BDD: Midnight rollover | Last millisecond of day |
| 2 | Message at `2026-03-29T00:00:00.000Z` | Boundary | Written to `2026-03-29.jsonl` | BDD: Midnight rollover | First millisecond of new day |
| 3 | Message at `2026-03-29T00:00:00.001Z` | Normal | Written to `2026-03-29.jsonl` | BDD: Midnight rollover | Just after midnight |
| 4 | Message content with newlines | Content edge | Single JSONL line (newlines escaped in JSON) | BDD: New session creates partition | JSONL format requires one object per line |
| 5 | Message content with 1MB of text | Size edge | Single JSONL line, file grows | BDD: New session creates partition | No per-line size limit |
| 6 | 100,000 messages in one day | Volume | All in one partition file | BDD: Session stats aggregate | Day partitioning, not size partitioning |
| 7 | Session with 0 messages, only meta.json | Empty | Valid session, `partitions: []`, `stats` all zeros | BDD: New session | Created session with no messages yet |
| 8 | Corrupt JSONL (line 50 is invalid JSON) | Corruption | Lines 1-49 readable, error on line 50, lines 51+ readable | N/A (resilience) | Partial read capability |

### Regression Test Requirements

> No regression impact -- new capability (greenfield project forked from Omnipus). Integration seams protected by: Omnipus's existing test suite validates that the rename/fork does not break existing behavior. New tests cover new functionality only.

---

## Functional Requirements

- **FR-001**: System MUST create the complete `~/.omnipus/` directory tree on first launch per Appendix E section E.3.
- **FR-002**: System MUST load and validate `config.json` at startup, exiting with descriptive errors on invalid input.
- **FR-003**: System MUST generate a default `config.json` when none exists.
- **FR-004**: System MUST preserve unknown JSON fields in config during round-trip read/write (forward compatibility).
- **FR-005**: System MUST encrypt credentials using AES-256-GCM with a unique nonce per credential.
- **FR-006**: System MUST derive encryption keys using Argon2id with parameters: time=3, memory=64MB, parallelism=4.
- **FR-007**: System MUST support three key provisioning modes in order: `OMNIPUS_MASTER_KEY` env var (hex, bypasses KDF), `OMNIPUS_KEY_FILE` (file path, permissions enforced `0600`), interactive passphrase (TTY required).
- **FR-008**: System MUST store credentials in `~/.omnipus/credentials.json` with format `{"version": 1, "salt": "<base64>", "credentials": {"NAME": {"nonce": "<base64>", "ciphertext": "<base64>"}}}`.
- **FR-009**: System MUST never persist raw credential values in `config.json`, logs, or any file other than the encrypted credential store.
- **FR-010**: System MUST resolve `_ref` credential references in config at runtime by decrypting from the credential store and injecting via environment variables.
- **FR-011**: System MUST store session transcripts as day-partitioned JSONL files (`<YYYY-MM-DD>.jsonl`) with a `meta.json` metadata file per session.
- **FR-012**: System MUST create a new session partition file when a message's UTC date differs from the current partition's date.
- **FR-013**: System MUST maintain aggregated stats in `meta.json` (tokens_in, tokens_out, cost, tool_calls, message_count) across all partitions.
- **FR-014**: System MUST route inbound messages to agents based on channel routing rules defined in config, with fallback to a default agent.
- **FR-015**: System MUST reject unroutable messages (no matching rule, no default agent) with a log entry.
- **FR-016**: System MUST create per-agent workspace directories with isolated `sessions/`, `memory/`, and `skills/` subdirectories.
- **FR-017**: System MUST deny cross-agent workspace file access at the application level.
- **FR-018**: System MUST stream LLM response tokens incrementally via SSE (WebUI) and chunked output (CLI).
- **FR-019**: System MUST execute graceful shutdown on SIGTERM/SIGINT: stop accepting -> wait for in-flight (configurable timeout, default 10s) -> save partial -> flush -> exit.
- **FR-020**: System MUST save partial streamed responses on shutdown timeout with `"status": "interrupted"` and display a system message on session resume.
- **FR-021**: System MUST perform all file writes atomically (temp file + rename in same directory).
- **FR-022**: System MUST acquire advisory file locks (`flock`/`LockFileEx`) before all write operations.
- **FR-023**: System MUST serialize writes to shared files (`config.json`, `credentials.json`) through a single-writer goroutine.
- **FR-024**: System MUST provide CLI commands: `omnipus credentials set|list|delete|rotate`.
- **FR-025**: System MUST support credential rotation by re-encrypting all credentials with a new key derived from a new passphrase, written atomically.
- **FR-026**: System SHOULD reject empty passphrases with a descriptive error.
- **FR-027**: System SHOULD log a debug-level message for unknown config fields.
- **FR-028**: System SHOULD retry retryable LLM provider errors (429, 503) with exponential backoff (max 3 retries).
- **FR-029**: System MAY cache the derived key in OS keyring (not required for Wave 1).

---

## Success Criteria

- **SC-001**: All credential round-trips (encrypt -> persist -> load -> decrypt) produce the original plaintext for 100% of test inputs, including empty strings and binary content.
- **SC-002**: The string content of any stored credential never appears in `config.json`, any `*.log` file, or stdout/stderr output during normal operation or error conditions.
- **SC-003**: Atomic write correctness: after 10,000 concurrent write attempts across 4 goroutines, `config.json` is always valid JSON and no writes are silently lost.
- **SC-004**: Session partition boundary: messages at `T23:59:59.999Z` and `T00:00:00.000Z` land in the correct day-partitioned files with 100% accuracy.
- **SC-005**: Graceful shutdown: after SIGTERM with an in-flight LLM call, the partial response is persisted to disk. Zero data loss for completed messages.
- **SC-006**: MessageBus routing: 100% of test messages are dispatched to the correct agent based on routing rules, including default agent fallback.
- **SC-007**: Startup time with credential decryption (including Argon2id KDF): under 3 seconds on commodity hardware.
- **SC-008**: Memory overhead of data model, credential store, MessageBus, and session management: under 5MB combined.
- **SC-009**: All JSON/JSONL files produced by the system are parseable by Go's `encoding/json` and by external tools (`jq`).
- **SC-010**: Cross-agent workspace access denial: 100% of cross-agent file read/write attempts are blocked.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | First-run creates directory tree; Partial directory completed | `TestDirectoryInitialization`, `TestDirectoryInitPartialExists` |
| FR-002 | US-2 | Valid config loads; Invalid JSON error | `TestConfigParseValid`, `TestConfigParseInvalidJSON`, `TestStartupE2E` |
| FR-003 | US-2 | Missing config triggers defaults | `TestConfigDefaultGeneration`, `TestStartupE2E` |
| FR-004 | US-2 | Unknown fields preserved | `TestConfigPreservesUnknownFields` |
| FR-005 | US-3 | Store and retrieve credential | `TestAES256GCMEncryptDecrypt`, `TestCredentialStoreIntegration` |
| FR-006 | US-3 | Store and retrieve credential | `TestArgon2idKeyDerivation` |
| FR-007 | US-4 | Master key from env; Key file; Bad permissions; Headless | `TestMasterKeyFromHex`, `TestKeyFilePermissionCheck`, `TestKeyProvisioningOrder` |
| FR-008 | US-3 | Store and retrieve credential | `TestCredentialStoreFileFormat` |
| FR-009 | US-3, US-11 | Credential ref in config; Provider resolves ref | `TestCredentialInjection`, `TestCredentialLifecycleE2E` |
| FR-010 | US-11 | Provider resolves credential ref; Missing ref | `TestCredentialInjection`, `TestCredentialMissingRef` |
| FR-011 | US-5 | New session creates partition | `TestSessionWriteIntegration`, `TestSessionPartitionNaming` |
| FR-012 | US-5 | Midnight rollover creates new partition | `TestSessionPartitionMidnightBoundary`, `TestSessionMultiPartition` |
| FR-013 | US-5 | Session stats aggregate across partitions | `TestSessionStatsAggregation` |
| FR-014 | US-6 | Route by channel+user; Default agent | `TestMessageBusRouting`, `TestMessageBusDefaultAgent`, `TestMultiAgentRoutingIntegration` |
| FR-015 | US-6 | No default agent no match | `TestMessageBusNoMatch` |
| FR-016 | US-7 | Agent activation creates workspace | `TestWorkspaceIsolation`, `TestStartupE2E` |
| FR-017 | US-7 | Cross-agent access denied | `TestWorkspaceIsolation` |
| FR-018 | US-8 | SSE token streaming | `TestStreamingE2E` |
| FR-019 | US-9 | Clean shutdown; Shutdown waits; Concurrent agents | `TestGracefulShutdownE2E` |
| FR-020 | US-9 | Shutdown timeout saves partial | `TestGracefulShutdownE2E` |
| FR-021 | US-10 | Atomic write survives crash | `TestAtomicWrite` |
| FR-022 | US-10 | Concurrent writes serialized | `TestAdvisoryFileLock`, `TestConcurrentConfigWrites` |
| FR-023 | US-10 | Concurrent writes serialized | `TestConcurrentConfigWrites` |
| FR-024 | US-12 | Set credential; List credentials; Rotate | `TestCredentialLifecycleE2E` |
| FR-025 | US-12 | Rotate credentials re-encrypts | `TestCredentialLifecycleE2E` |

**Completeness check**: Every FR-xxx row has at least one BDD scenario and one test. Every BDD scenario appears in at least one row.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
All ambiguities resolved on 2026-03-28:

| # | What Was Ambiguous | Resolution |
|---|---|---|
| 1 | Credential `_ref` naming | **Per-provider field.** `providers.anthropic.api_key_ref: "ANTHROPIC_API_KEY"`. |
| 2 | Session ID algorithm | **ULID** prefixed with `session_`. Sortable, unique, compact. |
| 3 | WebUI and MessageBus | **Yes, same MessageBus.** WebUI is just another channel. |
| 4 | Config write-back | **Yes, Omnipus writes `_ref` keys** to config.json programmatically. Preserves existing content. |
| 5 | Argon2id 64MB on Pi Zero | **Fixed at 64MB** per BRD. Runs once, frees after. Pi Zero users can use `OMNIPUS_MASTER_KEY` env var to skip KDF. |
| 6 | JSONL append atomicity | **O_APPEND for JSONL** (sessions, memory, audit). Temp+rename for full-file rewrites (config, credentials, meta.json). |
| 7 | Shutdown timeout config | **config.json** — `gateway.shutdown_timeout_seconds: 10`. |
| 8 | Omnipus fork version | **Latest tagged release** at implementation time (currently v0.2.3). |

---

## Evaluation Scenarios (Holdout)

> **Note**: These scenarios are for post-implementation evaluation only.
> They must NOT be visible to the implementing agent during development.
> Do not reference these in the TDD plan or traceability matrix.

### Scenario: Full credential lifecycle across restart

- **Setup**: Start Omnipus with `OMNIPUS_MASTER_KEY` set. Store credentials for Anthropic and OpenRouter. Stop the process.
- **Action**: Restart Omnipus with the same `OMNIPUS_MASTER_KEY`. Send a message that triggers an LLM call to Anthropic.
- **Expected outcome**: The LLM call succeeds using the decrypted Anthropic API key. The session transcript records the interaction. The OpenRouter credential is also accessible.
- **Category**: Happy Path

### Scenario: Multi-agent message fan-out across day boundary

- **Setup**: Configure two agents with distinct routing rules. Start a session with agent-A at 23:58 UTC. Configure a mock LLM that takes 5 minutes to respond.
- **Action**: Send a message at 23:58 UTC. While agent-A's LLM call is in-flight, send a message matching agent-B's routing rule at 00:01 UTC.
- **Expected outcome**: Agent-A's response lands in the next day's partition when it completes. Agent-B's message is routed correctly and starts a new session in agent-B's workspace with the new day's partition.
- **Category**: Happy Path

### Scenario: First-run with no TTY and no env vars

- **Setup**: Clean system, no `~/.omnipus/`, no `OMNIPUS_MASTER_KEY`, no `OMNIPUS_KEY_FILE`, stdin is `/dev/null` (no TTY).
- **Action**: Start `omnipus`.
- **Expected outcome**: Directory tree is created. Default config is generated. A warning is logged about the locked credential store. The gateway starts and accepts connections on the WebUI port. Sending a message that requires an LLM call fails with a credential error but does not crash the process.
- **Category**: Happy Path

### Scenario: Corrupted credentials.json on startup

- **Setup**: `~/.omnipus/credentials.json` contains `{"version": 1, "salt": "AAAA", "credentials": {"KEY": {"nonce": "invalid-not-base64!!", "ciphertext": "also-invalid"}}}`. Master key is available.
- **Action**: Start `omnipus`. Attempt to use the `KEY` credential.
- **Expected outcome**: The system starts (does not crash). When `KEY` is accessed, decryption fails with a descriptive error. The corrupted file is not silently overwritten. Other operations (routing, session creation) continue to work.
- **Category**: Error

### Scenario: SIGTERM during credential rotation

- **Setup**: `credentials.json` has 5 credentials. Start `omnipus credentials rotate`, enter the old passphrase.
- **Action**: Send SIGTERM after the new passphrase is entered but before the atomic write completes (simulated via test hook).
- **Expected outcome**: `credentials.json` still contains all 5 credentials encrypted with the old key (the atomic rename did not happen). Running `omnipus credentials rotate` again succeeds.
- **Category**: Error

### Scenario: Rapid message burst across midnight boundary

- **Setup**: An active session with messages at 23:59:59.990Z through 00:00:00.010Z (20 messages spanning exactly midnight, 1ms apart).
- **Action**: Send all 20 messages in rapid succession.
- **Expected outcome**: Messages before midnight are in the old partition, messages at/after midnight are in the new partition. No messages are lost. `meta.json` lists both partitions. Stats are correct.
- **Category**: Edge Case

### Scenario: Two agents write to shared tasks directory simultaneously

- **Setup**: Two agents (A and B) are active. Each creates a task (per-entity file in `tasks/`) at the same time.
- **Action**: Agent A writes `task_001.json` and Agent B writes `task_002.json` concurrently.
- **Expected outcome**: Both files are created correctly. No contention because per-entity files are independent. If both were somehow assigned the same task ID (implementation bug), the advisory lock prevents corruption.
- **Category**: Edge Case

---

## Assumptions

- Omnipus's Go source code is available for forking and the module path rename (`omnipus` -> `omnipus`) is mechanically straightforward.
- Go 1.21+ is the minimum supported version (required for `slog`).
- The `golang.org/x/crypto/argon2` package provides a production-quality Argon2id implementation.
- The target LLM providers (Anthropic, OpenRouter) support SSE-based streaming and return usage/cost data in the response.
- Advisory file locking (`flock`) is supported on all target Linux filesystems (ext4, btrfs, XFS, ZFS). Network filesystems (NFS, CIFS) may not support `flock` reliably; this is a known limitation.
- The `~/.omnipus/` directory is on a local filesystem, not a network mount.
- UTC is used for all day-partition boundaries and timestamp storage. Local timezone conversion is a UI concern only.
- Wave 1 does not include kernel-level sandboxing (Landlock/seccomp), RBAC, gateway authentication, browser automation, WhatsApp, or any channel integrations beyond the WebUI. These are in subsequent BRD delivery phases.
- The WebUI channel (SSE streaming) will use a simple HTTP server embedded in the binary. The full React UI is not in Wave 1 scope, but the SSE endpoint must be ready for it.
- OS keyring integration for caching the derived key is deferred beyond Wave 1.
- The bridge protocol for external channels is defined in the BRD (Appendix E section E.10.2) but no external channels are implemented in Wave 1.

---

## Clarifications

### 2026-03-28

- Q: Does Wave 1 include the full React UI? -> A: No. Wave 1 delivers the backend foundation (data model, credentials, sessions, routing, streaming, shutdown). The SSE endpoint is implemented for future UI consumption. CLI is the primary interface for Wave 1.
- Q: Does Wave 1 include any channel integrations? -> A: Only the WebUI/WebChat channel (HTTP + SSE). Telegram, Discord, WhatsApp, etc. are in later waves per the BRD delivery phases.
- Q: Is `credentials.json` the filename (not `security.yml` as mentioned in CLAUDE.md)? -> A: Yes. The BRD (SEC-23e) and Appendix E explicitly specify `credentials.json`. The `security.yml` reference in CLAUDE.md is outdated; JSON format is authoritative.
- Q: Should JSONL appends use `O_APPEND` or temp+rename? -> A: This is flagged as Ambiguity Warning #6. Likely answer: `O_APPEND` for JSONL appends with `flock` for safety, temp+rename for full-file rewrites. To be confirmed during implementation.
- Q: Are LLM provider implementations (HTTP clients for Anthropic/OpenRouter) in Wave 1 scope? -> A: Yes, as minimal streaming clients. Full provider abstraction with routing (FUNC-01/02/03) is Phase 3.
