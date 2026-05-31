# Feature Specification: Browser Automation — Integration Wiring (v1.0)

**Created**: 2026-04-05
**Status**: Draft
**Input**: Omnipus v1.0 critical feature 1 — browser automation integration gap

---

## Overview

The browser automation package (`pkg/tools/browser/`) is 85% complete — `BrowserManager`, all 7 tools, SSRF protection, URL validation, tab pooling, and tests exist. The **only missing piece** is wiring `RegisterTools` into the agent startup path and adding config fields.

This is an integration-only task. No new behavioral logic is being designed.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `pkg/tools/browser/register.go:RegisterTools` | Must be called | Registers 7 browser tools; returns `*BrowserManager` for shutdown |
| `pkg/tools/browser/manager.go:BrowserManager` | Must be stored on `AgentInstance` | Chromium lifecycle manager; `Shutdown()` must be called on exit |
| `pkg/agent/instance.go:NewAgentInstance` | Must add browser registration | Tool registration block at lines 92–117; browser tools absent |
| `pkg/agent/instance.go:AgentInstance.Close` | Must call `mgr.Shutdown()` | Currently only closes session store |
| `pkg/config/config.go:ToolsConfig` | Must add `Browser` field | Switch case at `IsToolEnabled("browser")` returns false by default |
| `pkg/config/defaults.go` | Must add browser defaults | `BrowserConfig` in `ToolsConfig` defaults |

### Impact Assessment

| Symbol Modified | Risk Level | Direct Dependents | Indirect Dependents |
|----------------|------------|-------------------|---------------------|
| `pkg/agent/instance.go` | LOW | `NewAgentInstance` callers (agent loop) | Agent startup, tool execution |
| `pkg/config/config.go` | LOW | `IsToolEnabled("browser")` callers | Frontend config panel |
| `pkg/config/defaults.go` | LOW | Browser defaults wiring | None |

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| Agent startup | `NewAgentInstance` → tool registration block → browser tools conditionally registered |
| Agent shutdown | `AgentInstance.Close()` → `BrowserManager.Shutdown()` → Chromium processes terminated |
| Config parsing | `config.json` → `ToolsConfig.Browser` → `BrowserConfig` → `browser.RegisterTools` |

---

## Functional Requirements

- **FR-001**: System MUST register browser automation tools (`browser.navigate`, `browser.click`, `browser.type`, `browser.screenshot`, `browser.get_text`, `browser.wait`, `browser.evaluate`) when `tools.browser.enabled = true` in config.
- **FR-002**: System MUST NOT register browser tools when `tools.browser.enabled = false` (default) — deny-by-default per CLAUDE.md constraint.
- **FR-003**: System MUST call `BrowserManager.Shutdown()` when `AgentInstance.Close()` is called, releasing all Chromium resources.
- **FR-004**: System MUST reject startup with an error if SSRF checker is nil when browser is enabled (SSRF protection is mandatory, per SEC-24 in BRD).
- **FR-005**: System MUST expose `tools.browser.*` config fields: `enabled`, `headless`, `cdp_url`, `page_timeout`, `max_tabs`, `persist_session`, `profile_dir`.

---

## Implementation Tasks

### 1. Add `Browser` field to `ToolsConfig` (`pkg/config/config.go`)

Add a `Browser BrowserConfig` field to `ToolsConfig`. Add a case to `IsToolEnabled("browser")` returning `c.Browser.Enabled`.

```go
// ToolsConfig struct — add:
Browser BrowserConfig `json:"browser" yaml:"browser,omitempty"`

// IsToolEnabled switch — add:
case "browser":
    return c.Browser.Enabled
```

### 2. Add browser defaults (`pkg/config/defaults.go`)

Add `Browser: toolsBrowserDefaults()` to `ToolsConfig` defaults, where `toolsBrowserDefaults()` returns `browser.DefaultConfig()`.

```go
// In newDefaultConfig or wherever ToolsConfig{} is populated:
Browser: func() BrowserConfig {
    cfg, err := browser.DefaultConfig()
    if err != nil {
        // DefaultConfig only fails if UserHomeDir fails; log and use safe fallback
        logger.WarnCF("config", "browser.DefaultConfig fallback", map[string]any{"error": err.Error()})
        return browser.BrowserConfig{Enabled: false, Headless: true, PageTimeout: 30 * time.Second, MaxTabs: 5}
    }
    return cfg
}(),
```

### 3. Add `BrowserManager` field to `AgentInstance` (`pkg/agent/instance.go`)

```go
type AgentInstance struct {
    // ... existing fields ...
    BrowserManager *browser.BrowserManager  // nil when browser is disabled
}
```

### 4. Register browser tools in `NewAgentInstance` (`pkg/agent/instance.go`)

Add after the existing tool registration block (after `append_file`, around line 117):

```go
// Browser tools (SEC-24 / US-4 / US-6 / US-7)
if cfg.Tools.IsToolEnabled("browser") {
    mgr, err := browser.RegisterTools(toolsRegistry, cfg.Tools.Browser, &cfg.Security.SSRF)
    if err != nil {
        logger.ErrorCF("agent", "Browser tools registration failed; continuing without browser",
            map[string]any{"error": err.Error()})
    } else {
        ai.BrowserManager = mgr
    }
}
```

**Security gate**: `cfg.Security.SSRF` is the existing `*security.SSRFChecker` already present in `Config`. The `browser.RegisterTools` function already enforces `ssrf != nil` and returns an error. We log and continue without browser rather than failing the entire agent startup — graceful degradation per CLAUDE.md.

### 5. Shutdown browser manager in `AgentInstance.Close` (`pkg/agent/instance.go`)

```go
func (a *AgentInstance) Close() error {
    if a.BrowserManager != nil {
        if shutErr := a.BrowserManager.Shutdown(); shutErr != nil {
            logger.WarnCF("agent", "BrowserManager shutdown error", map[string]any{"error": shutErr.Error()})
        }
    }
    if a.Sessions != nil {
        return a.Sessions.Close()
    }
    return nil
}
```

### 6. Add browser config YAML/JSON tags

`BrowserConfig` in `pkg/tools/browser/manager.go` already has JSON tags (`json:"enabled"`, `json:"headless"`, etc.). Verify these map cleanly to `config.json` at path `tools.browser.*`.

---

## Config Schema (`config.json`)

```json
{
  "tools": {
    "browser": {
      "enabled": false,
      "headless": true,
      "cdp_url": "",
      "page_timeout": "30s",
      "max_tabs": 5,
      "persist_session": false,
      "profile_dir": "~/.omnipus/browser/profiles/default"
    }
  }
}
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `false` | Enable browser automation tools |
| `headless` | bool | `true` | Run Chromium headless |
| `cdp_url` | string | `""` | Remote CDP URL (空白 = local Chromium) |
| `page_timeout` | duration | `30s` | Per-page navigation timeout |
| `max_tabs` | int | `5` | Max concurrent browser tabs |
| `persist_session` | bool | `false` | Persist cookies/localStorage across restarts |
| `profile_dir` | string | `~/.omnipus/browser/profiles/default` | Chromium user data directory |

---

## Test Datasets

#### Dataset: Browser config parsing

| # | Config Input | Expected Behavior | Traces to |
|---|-------------|-------------------|-----------|
| 1 | `browser.enabled = false` (default) | No browser tools registered; `BrowserManager = nil` | FR-002 |
| 2 | `browser.enabled = true`, valid local chromedp path | Browser tools registered; `BrowserManager != nil` | FR-001 |
| 3 | `browser.enabled = true`, `cdp_url = "ws://localhost:9222"` | Browser tools registered against remote CDP | FR-001 |
| 4 | `browser.enabled = true`, `ssrf = nil` (config error) | Logged warning; agent starts without browser | FR-004 |
| 5 | `browser.max_tabs = 0` (invalid) | `RegisterTools` returns error; no browser registered | Edge case |
| 6 | `browser.page_timeout = "0s"` (invalid) | `RegisterTools` returns error; no browser registered | Edge case |

---

## Behavioral Contract

- When `tools.browser.enabled` is `false` (default), `AgentInstance` has no browser tools and `BrowserManager` is `nil`.
- When `tools.browser.enabled` is `true`, `AgentInstance` has 7 additional tools: `browser.navigate`, `browser.click`, `browser.type`, `browser.screenshot`, `browser.get_text`, `browser.wait`, `browser.evaluate`.
- When `AgentInstance.Close()` is called, Chromium processes are terminated via `BrowserManager.Shutdown()`.
- When Chromium cannot start (missing binary, permission error, SSRF checker absent), the agent starts without browser tools and logs a warning — it does not crash.

---

## Explicit Non-Behaviors

- The system must not register browser tools without explicit `tools.browser.enabled = true` in config (deny-by-default).
- The system must not start Chromium without an SSRF checker — this is enforced in `RegisterTools` and propagated as a startup warning with graceful degradation.
- Browser tools must not be available in the open-source binary by default (they require `enabled: true` and a local Chromium installation).

---

## Edge Cases

- What happens when `chromedp` cannot find a Chromium binary? Expected: `RegisterTools` returns an error wrapping the chromedp init error; logged as warning; agent starts without browser tools.
- What happens when the user data directory (`profile_dir`) is not writable? Expected: `chromedp.NewExecAllocator` fails; error propagates through `RegisterTools`; graceful degradation applies.
- What happens when `cdp_url` points to an unreachable CDP endpoint? Expected: First browser tool call to that endpoint times out after `page_timeout`; tool returns error; session is reused on retry.
- What happens when `max_tabs` is set above a reasonable limit (e.g., 100)? Expected: `BrowserManager` respects the configured value; no artificial cap — the system's RAM is the natural limit.

---

## BDD Scenarios

### Feature: Browser Automation Wiring

---

#### Scenario: Browser tools not registered when disabled

**Traces to**: FR-002
**Category**: Happy Path

- **Given** `tools.browser.enabled` is `false` in config
- **When** `NewAgentInstance` is called
- **Then** `browser.RegisterTools` is NOT called
- **And** `AgentInstance.Tools` does not contain any `browser.*` tools
- **And** `AgentInstance.BrowserManager` is `nil`

---

#### Scenario: Browser tools registered when enabled

**Traces to**: FR-001
**Category**: Happy Path

- **Given** `tools.browser.enabled` is `true` in config and Chromium is available
- **When** `NewAgentInstance` is called
- **Then** `browser.RegisterTools` is called
- **And** `AgentInstance.Tools` contains all 7 browser tools
- **And** `AgentInstance.BrowserManager` is not `nil`

---

#### Scenario: Browser manager shut down on agent close

**Traces to**: FR-003
**Category**: Happy Path

- **Given** an `AgentInstance` with browser enabled and a running `BrowserManager`
- **When** `AgentInstance.Close()` is called
- **Then** `BrowserManager.Shutdown()` is called
- **And** all Chromium processes are terminated
- **And** the method returns the result of `Sessions.Close()`

---

#### Scenario: Graceful degradation when browser init fails

**Traces to**: FR-004
**Category**: Edge Case

- **Given** `tools.browser.enabled` is `true` but Chromium binary is missing or SSRF checker is nil
- **When** `NewAgentInstance` is called
- **Then** `browser.RegisterTools` returns an error
- **And** the error is logged as a warning with `logger.ErrorCF`
- **And** the agent continues to start WITHOUT browser tools
- **And** `AgentInstance.BrowserManager` is `nil`

---

#### Scenario: Browser config parsed correctly

**Traces to**: FR-005
**Category**: Happy Path

- **Given** a config with `tools.browser.enabled = true`, `page_timeout = "60s"`, `max_tabs = 3`
- **When** `NewAgentInstance` processes the config
- **Then** `BrowserConfig.PageTimeout` is 60 seconds
- **And** `BrowserConfig.MaxTabs` is 3
- **And** `BrowserManager` respects these values

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | `NewAgentInstance` with browser enabled/disabled | Validates browser tool registration path |
| Unit | `AgentInstance.Close()` with browser manager | Validates shutdown path |
| Unit | Config parsing `tools.browser.*` fields | Validates config → `BrowserConfig` mapping |
| Integration | Full agent startup with browser enabled | Validates chromedp init + tool registration |

### Test Implementation Order

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestBrowserToolsNotRegisteredWhenDisabled` | Unit | Scenario: Browser tools not registered when disabled | Verify `toolsRegistry.List()` lacks `browser.*` when `enabled=false` |
| 2 | `TestBrowserToolsRegisteredWhenEnabled` | Unit | Scenario: Browser tools registered when enabled | Verify `toolsRegistry.List()` contains all 7 `browser.*` tools when `enabled=true` |
| 3 | `TestAgentInstanceBrowserManagerNilByDefault` | Unit | Scenario: Browser tools not registered when disabled | Verify `AgentInstance.BrowserManager == nil` when disabled |
| 4 | `TestAgentInstanceCloseCallsBrowserShutdown` | Unit | Scenario: Browser manager shut down on agent close | Capture shutdown call; verify called once with no args |
| 5 | `TestBrowserConfigParsing` | Unit | Scenario: Browser config parsed correctly | Parse JSON with `tools.browser.*`; verify fields mapped correctly |
| 6 | `TestGracefulDegradationOnBrowserInitFailure` | Unit | Scenario: Graceful degradation when browser init fails | Mock `RegisterTools` error; verify agent still starts, warning logged |
| 7 | `TestFullAgentStartupWithBrowserEnabled` | Integration | Scenario: Browser tools registered when enabled | Start agent with `browser.enabled=true`; verify tools registered; close cleanly |

---

## Regression Test Requirements

> No regression impact — integration wiring only. Existing tests covering `NewAgentInstance` and `AgentInstance.Close()` continue to pass. New tests only exercise the new browser registration paths.

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|-----------------|--------------|
| FR-001 | N/A (integration) | Scenario: Browser tools registered when enabled | TestBrowserToolsRegisteredWhenEnabled, TestAgentStartupWithBrowserEnabled |
| FR-002 | N/A (integration) | Scenario: Browser tools not registered when disabled | TestBrowserToolsNotRegisteredWhenDisabled, TestBrowserManagerNilByDefault |
| FR-003 | N/A (integration) | Scenario: Browser manager shut down on agent close | TestAgentInstanceCloseCallsBrowserShutdown |
| FR-004 | N/A (integration) | Scenario: Graceful degradation when browser init fails | TestGracefulDegradationOnBrowserInitFailure |
| FR-005 | N/A (integration) | Scenario: Browser config parsed correctly | TestBrowserConfigParsing |

---

## Assumptions

- `cfg.Security.SSRF` is always non-nil when browser is enabled in a properly configured system — the graceful degradation covers the misconfiguration case only.
- The `chromedp` library handles its own binary download/installation (via `chromedp.Install`), but `RegisterTools` does not call it — if Chromium is missing, the error propagates and graceful degradation applies.
- The open-source binary with embedded UI will have `browser.enabled = false` by default (same as all tools).
- `AgentLoop` does not directly hold a `BrowserManager` — the manager is scoped to `AgentInstance` and accessed through `AgentInstance.BrowserManager`.

---

## Clarifications

### 2026-04-05

- Q: Should the agent fail to start if browser is enabled but cannot initialize? -> A: No — graceful degradation. Log a warning and continue without browser tools. This is consistent with CLAUDE.md's graceful degradation mandate and with how `exec` tool failure is handled in `instance.go:103-109`.
