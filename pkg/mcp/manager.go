package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// sandboxedCommandTransport is an mcp.Transport that spawns a stdio MCP server
// subprocess via sandbox.StartLocked so the child inherits the gateway's
// Landlock domain (ADR-019). It is a drop-in replacement for mcp.CommandTransport
// that does NOT bypass the kernel sandbox.
//
// Why not use mcp.CommandTransport directly? CommandTransport.Connect calls
// cmd.Start() on whatever OS thread Go's M:N scheduler happens to have routed
// the spawning goroutine onto — almost always a worker thread that never had
// restrict_self applied, so the child silently escapes the gateway's Landlock
// domain. StartLocked locks a fresh OS thread, re-applies the Landlock domain
// to it, forks the child from THAT thread, and exits without unlocking so the
// runtime retires the (now-restricted) thread. On non-Linux platforms or when
// the sandbox is disabled, StartLocked is a thin wrapper around cmd.Start() —
// graceful degradation per the "graceful degradation" hard constraint.
//
// Process lifecycle (Wait/SIGTERM/SIGKILL on Close) mirrors
// mcp.CommandTransport.pipeRWC.Close so behavior is identical to the upstream
// SDK transport. The Connection returned by Connect is the SDK's own ioConn
// (built via mcp.IOTransport) wrapped in a sandboxedStdioConn that adds the
// process reaping — we cannot construct an ioConn directly because newIOConn is
// unexported in the SDK.
type sandboxedCommandTransport struct {
	cmd *exec.Cmd
	// terminateDuration is how long Close waits after closing stdin for the
	// child to exit before sending SIGTERM, then SIGKILL. Defaults to 5s.
	terminateDuration time.Duration
}

// newSandboxedCommandTransport wraps an *exec.Cmd (already populated with Env,
// Dir, etc. by the caller) in a sandboxedCommandTransport. The cmd must NOT
// have been Start()ed — Connect does that via StartLocked.
func newSandboxedCommandTransport(cmd *exec.Cmd) *sandboxedCommandTransport {
	return &sandboxedCommandTransport{cmd: cmd, terminateDuration: 5 * time.Second}
}

// Connect implements mcp.Transport. It sets up the stdin/stdout pipes, spawns
// the child through sandbox.StartLocked (inheriting the gateway Landlock
// domain), and returns a Connection that reaps the process on Close.
func (t *sandboxedCommandTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	// StdoutPipe / StdinPipe must be called BEFORE Start — they install the
	// pipe FDs on cmd.Stdout / cmd.Stdin. Mirrors mcp.CommandTransport.Connect.
	stdout, err := t.cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdout pipe: %w", err)
	}
	// NopCloser on stdout so the SDK only closes the write side (stdin) —
	// matches CommandTransport.pipeRWC semantics ("close the connection by
	// closing stdin, not stdout").
	stdout = io.NopCloser(stdout)
	stdin, err := t.cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp stdio: stdin pipe: %w", err)
	}

	// ADR-019: route the spawn through StartLocked so the forked child
	// inherits the gateway's Landlock domain. On non-Linux / fallback
	// sandbox this is a thin wrapper around cmd.Start().
	if err = sandbox.StartLocked(t.cmd); err != nil {
		return nil, fmt.Errorf("mcp stdio: sandboxed start: %w", err)
	}

	// Build the Connection via the SDK's exported IOTransport (newIOConn is
	// unexported, so this is the only public way to get an ioConn over raw
	// reader/writer pipes).
	baseConn, err := (&mcp.IOTransport{Reader: stdout, Writer: stdin}).Connect(ctx)
	if err != nil {
		// Spawned but connection setup failed — kill the orphan so we don't
		// leak a process. Best-effort; logged by the caller.
		_ = t.cmd.Process.Kill()
		_, _ = t.cmd.Process.Wait()
		return nil, fmt.Errorf("mcp stdio: io transport connect: %w", err)
	}
	return &sandboxedStdioConn{
		Connection:        baseConn,
		cmd:               t.cmd,
		terminateDuration: t.terminateDuration,
	}, nil
}

// sandboxedStdioConn wraps the mcp.IOTransport Connection and adds process
// lifecycle reaping on Close (Wait → SIGTERM → SIGKILL), mirroring
// mcp.CommandTransport.pipeRWC.Close. Without this, the child would become a
// zombie when the session ends because IOTransport.Close only closes the pipes.
type sandboxedStdioConn struct {
	mcp.Connection
	cmd               *exec.Cmd
	terminateDuration time.Duration
}

// Close closes the underlying pipes (EOF to child via stdin) and then reaps
// the process. Errors from both phases are joined.
func (s *sandboxedStdioConn) Close() error {
	innerErr := s.Connection.Close()
	reapErr := s.reap()
	return errors.Join(innerErr, reapErr)
}

// reap waits for the child to exit, escalating to SIGTERM then SIGKILL if it
// does not exit within terminateDuration. This is the same sequence
// mcp.CommandTransport uses.
func (s *sandboxedStdioConn) reap() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	td := s.terminateDuration
	if td <= 0 {
		td = 5 * time.Second
	}
	resChan := make(chan error, 1)
	go func() { resChan <- s.cmd.Wait() }()
	wait := func() (error, bool) {
		select {
		case err := <-resChan:
			return err, true
		case <-time.After(td):
		}
		return nil, false
	}
	if err, ok := wait(); ok {
		return err
	}
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err == nil {
		if err, ok := wait(); ok {
			return err
		}
	}
	if err := s.cmd.Process.Kill(); err != nil {
		return err
	}
	if err, ok := wait(); ok {
		return err
	}
	return fmt.Errorf("unresponsive MCP stdio subprocess")
}

// headerTransport is an http.RoundTripper that adds custom headers to requests
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	req = req.Clone(req.Context())

	// Add custom headers
	for key, value := range t.headers {
		req.Header.Set(key, value)
	}

	// Use the base transport
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

// loadEnvFile loads environment variables from a file in .env format
// Each line should be in the format: KEY=value
// Lines starting with # are comments
// Empty lines are ignored
func loadEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open env file: %w", err)
	}
	defer file.Close()

	envVars := make(map[string]string)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=value
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid format at line %d: %s", lineNum, line)
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		if key == "" {
			return nil, fmt.Errorf("invalid format at line %d: empty key", lineNum)
		}

		// Remove surrounding quotes if present
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		envVars[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading env file: %w", err)
	}

	return envVars, nil
}

// ResolveServerEnvFile resolves a server config's relative EnvFile path
// against workspacePath and returns a COPY of cfg with EnvFile updated; no
// other field is touched. An absolute or empty EnvFile is returned
// unchanged. A relative EnvFile with an empty workspacePath cannot be
// resolved and is reported as an error — the caller decides what to do with
// an unresolvable server (skip it, fail a connectivity test, etc.); this
// function does not have enough context to make that call itself.
//
// This is the single resolution authority for relative env_file paths:
// pkg/agent's MCP reconciliation (building its desired-server set) and the
// gateway's MCP-server test endpoint both call it, so a server's env_file
// resolves identically whether it is being connected live or just tested.
// The semantics mirror LoadFromMCPConfig's original per-server resolution
// above (workspace-relative Join, empty-workspace error) so config drift
// detection (which compares against the resolved config a server was
// connected with) stays consistent across both callers.
func ResolveServerEnvFile(
	cfg config.MCPServerConfig,
	workspacePath string,
) (config.MCPServerConfig, error) {
	if cfg.EnvFile == "" || filepath.IsAbs(cfg.EnvFile) {
		return cfg, nil
	}
	if workspacePath == "" {
		return cfg, fmt.Errorf(
			"workspace path is empty while resolving relative env_file %q",
			cfg.EnvFile,
		)
	}
	resolved := cfg
	resolved.EnvFile = filepath.Join(workspacePath, cfg.EnvFile)
	return resolved, nil
}

// buildStdioServerEnv computes the environment slice for a spawned stdio MCP
// server subprocess, applying the C7 secret-scrub and the documented override
// semantics. It is extracted from ConnectServer so the scrub is unit-testable
// at the integration level (a real spawn is not required to prove the leak is
// closed).
//
// C7: Start from the SCRUBBED gateway environment, NOT the raw parent
// environment. Seeding from os.Environ() would leak gateway secrets —
// OMNIPUS_MASTER_KEY, OMNIPUS_KEY_FILE, OMNIPUS_BEARER_TOKEN, and any future
// sensitive var — into every spawned MCP server subprocess. An MCP server is
// third-party code; a malicious or compromised one could read the master key (a
// total credential-store compromise) straight out of its own environment via
// os.Getenv.
//
// sandbox.ScrubGatewayEnv() applies the same closed allowlist the hardened-exec
// child path uses (allowedChildEnvKeys + the LC_*/XDG_*/OMNIPUS_CHILD_*
// prefixes): PATH, HOME, USER, LOGNAME, SHELL, LANG, TZ, TMPDIR, TERM, and the
// proxy vars all pass through — everything a generic stdio child legitimately
// needs to locate binaries and write per-user caches — while unknown /
// secret-bearing keys are stripped by default (fail-closed).
//
// Any value an MCP server genuinely needs beyond that allowlist must be declared
// explicitly via the server's `env` / `envFile` config, which override these
// base values (and an operator may use the OMNIPUS_CHILD_* rename escape hatch
// the allowlist documents).
func buildStdioServerEnv(name string, cfg config.MCPServerConfig) ([]string, error) {
	// Build environment variables with proper override semantics.
	// Use a map to ensure config variables override file variables.
	envMap := make(map[string]string)

	for _, e := range sandbox.ScrubGatewayEnv() {
		if idx := strings.Index(e, "="); idx > 0 {
			envMap[e[:idx]] = e[idx+1:]
		}
	}

	// Load environment variables from file if specified.
	if cfg.EnvFile != "" {
		envVars, err := loadEnvFile(cfg.EnvFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load env file %s: %w", cfg.EnvFile, err)
		}
		for k, v := range envVars {
			envMap[k] = v
		}
		logger.DebugCF("mcp", "Loaded environment variables from file",
			map[string]any{
				"server":    name,
				"envFile":   cfg.EnvFile,
				"var_count": len(envVars),
			})
	}

	// Environment variables from config override those from file.
	for k, v := range cfg.Env {
		envMap[k] = v
	}

	// Convert map to slice.
	env := make([]string, 0, len(envMap))
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env, nil
}

// ServerConnection represents a connection to an MCP server
type ServerConnection struct {
	Name    string
	Client  *mcp.Client
	Session *mcp.ClientSession
	Tools   []*mcp.Tool
	// Config is the configuration snapshot ConnectServer connected with. Used
	// by reconciliation to detect config drift (reflect.DeepEqual against the
	// desired config) without re-reading config.json.
	Config config.MCPServerConfig
}

// Manager manages multiple MCP server connections
type Manager struct {
	servers map[string]*ServerConnection
	mu      sync.RWMutex
	closed  atomic.Bool    // changed from bool to atomic.Bool to avoid TOCTOU race
	wg      sync.WaitGroup // tracks in-flight CallTool calls
}

// NewManager creates a new MCP manager
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*ServerConnection),
	}
}

// LoadFromConfig loads MCP servers from configuration
func (m *Manager) LoadFromConfig(ctx context.Context, cfg *config.Config) error {
	return m.LoadFromMCPConfig(ctx, cfg.Tools.MCP, cfg.AgentHomeBasePath())
}

// LoadFromMCPConfig loads MCP servers from MCP configuration and workspace path.
// This is the minimal dependency version that doesn't require the full Config object.
func (m *Manager) LoadFromMCPConfig(
	ctx context.Context,
	mcpCfg config.MCPConfig,
	workspacePath string,
) error {
	if !mcpCfg.Enabled {
		logger.InfoCF("mcp", "MCP integration is disabled", nil)
		return nil
	}

	if len(mcpCfg.Servers) == 0 {
		logger.InfoCF("mcp", "No MCP servers configured", nil)
		return nil
	}

	logger.InfoCF("mcp", "Initializing MCP servers",
		map[string]any{
			"count": len(mcpCfg.Servers),
		})

	var wg sync.WaitGroup
	errs := make(chan error, len(mcpCfg.Servers))
	enabledCount := 0

	for name, serverCfg := range mcpCfg.Servers {
		if !serverCfg.Enabled {
			logger.DebugCF("mcp", "Skipping disabled server",
				map[string]any{
					"server": name,
				})
			continue
		}

		enabledCount++
		wg.Add(1)
		go func(name string, serverCfg config.MCPServerConfig, workspace string) {
			defer wg.Done()

			// Resolve relative envFile paths relative to workspace
			if serverCfg.EnvFile != "" && !filepath.IsAbs(serverCfg.EnvFile) {
				if workspace == "" {
					err := fmt.Errorf(
						"workspace path is empty while resolving relative envFile %q for server %s",
						serverCfg.EnvFile,
						name,
					)
					logger.ErrorCF("mcp", "Invalid MCP server configuration",
						map[string]any{
							"server":   name,
							"env_file": serverCfg.EnvFile,
							"error":    err.Error(),
						})
					errs <- err
					return
				}
				serverCfg.EnvFile = filepath.Join(workspace, serverCfg.EnvFile)
			}

			if err := m.ConnectServer(ctx, name, serverCfg); err != nil {
				logger.ErrorCF("mcp", "Failed to connect to MCP server",
					map[string]any{
						"server": name,
						"error":  err.Error(),
					})
				errs <- fmt.Errorf("failed to connect to server %s: %w", name, err)
			}
		}(name, serverCfg, workspacePath)
	}

	wg.Wait()
	close(errs)

	// Collect errors
	var allErrors []error
	for err := range errs {
		allErrors = append(allErrors, err)
	}

	connectedCount := len(m.GetServers())

	// If all enabled servers failed to connect, return aggregated error
	if enabledCount > 0 && connectedCount == 0 {
		logger.ErrorCF("mcp", "All MCP servers failed to connect",
			map[string]any{
				"failed": len(allErrors),
				"total":  enabledCount,
			})
		return errors.Join(allErrors...)
	}

	if len(allErrors) > 0 {
		logger.WarnCF("mcp", "Some MCP servers failed to connect",
			map[string]any{
				"failed":    len(allErrors),
				"connected": connectedCount,
				"total":     enabledCount,
			})
		// Don't fail completely if some servers successfully connected
	}

	logger.InfoCF("mcp", "MCP server initialization complete",
		map[string]any{
			"connected": connectedCount,
			"total":     enabledCount,
		})

	return nil
}

// ConnectServer connects to a single MCP server
func (m *Manager) ConnectServer(
	ctx context.Context,
	name string,
	cfg config.MCPServerConfig,
) error {
	// Belt-and-braces: reject up front on an already-closed manager so a
	// racing reconcile pass cannot start a connect attempt against a
	// manager that is tearing down. This does not by itself close the
	// close/connect race — see the re-check at the store site below.
	if m.closed.Load() {
		return fmt.Errorf("manager is closed")
	}

	logger.InfoCF("mcp", "Connecting to MCP server",
		map[string]any{
			"server":     name,
			"command":    cfg.Command,
			"args_count": len(cfg.Args),
		})

	// Create client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "omnipus",
		Version: "1.0.0",
	}, nil)

	// Create transport based on configuration
	// Auto-detect transport type if not explicitly specified
	var transport mcp.Transport
	transportType := cfg.Type

	// Auto-detect: if URL is provided, use SSE; if command is provided, use stdio
	if transportType == "" {
		if cfg.URL != "" {
			transportType = "sse"
		} else if cfg.Command != "" {
			transportType = "stdio"
		} else {
			return fmt.Errorf("either URL or command must be provided")
		}
	}

	switch transportType {
	case "sse", "http":
		if cfg.URL == "" {
			return fmt.Errorf("URL is required for SSE/HTTP transport")
		}
		// Data-leaves-machine warning: a remote MCP URL sends prompts, tool
		// arguments, and tool results to a third-party endpoint over HTTP/SSE.
		// The operator should be aware this is NOT a local stdio subprocess —
		// every request body leaves the machine. Logged at WARN on every
		// connect so it is visible without a separate consent gate (the
		// ws_approval infrastructure is per-tool-call, not per-server-config;
		// a full consent gate is tracked separately).
		logger.WarnCF("mcp", "Remote MCP server configured — data leaves the machine",
			map[string]any{
				"server": name,
				"url":    cfg.URL,
				"note":   "prompts, tool args, and tool results are sent to this remote endpoint",
			})
		logger.DebugCF("mcp", "Using SSE/HTTP transport",
			map[string]any{
				"server": name,
				"url":    cfg.URL,
			})

		sseTransport := &mcp.StreamableClientTransport{
			Endpoint: cfg.URL,
		}

		// Add custom headers if provided
		if len(cfg.Headers) > 0 {
			// Create a custom HTTP client with header-injecting transport
			sseTransport.HTTPClient = &http.Client{
				Transport: &headerTransport{
					base:    http.DefaultTransport,
					headers: cfg.Headers,
				},
			}
			logger.DebugCF("mcp", "Added custom HTTP headers",
				map[string]any{
					"server":       name,
					"header_count": len(cfg.Headers),
				})
		}

		transport = sseTransport
	case "stdio":
		if cfg.Command == "" {
			return fmt.Errorf("command is required for stdio transport")
		}
		logger.DebugCF("mcp", "Using stdio transport",
			map[string]any{
				"server":  name,
				"command": cfg.Command,
			})
		// ADR-019 / FR-5.3: spawn via sandbox.StartLocked so the child
		// inherits the gateway's Landlock domain. The previous raw
		// exec.CommandContext + mcp.CommandTransport path bypassed
		// StartLocked, so the fork happened on an unrestricted worker thread
		// and the child silently escaped the kernel sandbox.
		//
		// Deliberately exec.Command, NOT exec.CommandContext(ctx, ...): ctx
		// here bounds only this ConnectServer call, i.e. the handshake below
		// (client.Connect). Tying the child's os/exec lifetime to it would
		// SIGKILL the subprocess the moment ctx ends — including the routine
		// case of a short-lived per-connect timeout whose deferred cancel()
		// fires right after a successful connect, which silently killed every
		// stdio server reconciliation touched. The child's actual lifetime is
		// owned by sandboxedStdioConn.Close's reap (Wait -> SIGTERM ->
		// SIGKILL), invoked when the session is closed — via DisconnectServer,
		// Manager.Close, or (on a failed/timed-out handshake) the SDK's own
		// Client.Connect error path, which calls ClientSession.Close on every
		// failure branch after the transport connects successfully.
		cmd := exec.Command(cfg.Command, cfg.Args...)

		env, err := buildStdioServerEnv(name, cfg)
		if err != nil {
			return err
		}
		cmd.Env = env

		transport = newSandboxedCommandTransport(cmd)
	default:
		return fmt.Errorf(
			"unsupported transport type: %s (supported: stdio, sse, http)",
			transportType,
		)
	}

	// Connect to server
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	// Get server info
	initResult := session.InitializeResult()
	logger.InfoCF("mcp", "Connected to MCP server",
		map[string]any{
			"server":        name,
			"serverName":    initResult.ServerInfo.Name,
			"serverVersion": initResult.ServerInfo.Version,
			"protocol":      initResult.ProtocolVersion,
		})

	// List available tools if supported
	var tools []*mcp.Tool
	if initResult.Capabilities.Tools != nil {
		for tool, err := range session.Tools(ctx, nil) {
			if err != nil {
				logger.WarnCF("mcp", "Error listing tool",
					map[string]any{
						"server": name,
						"error":  err.Error(),
					})
				continue
			}
			tools = append(tools, tool)
		}

		logger.InfoCF("mcp", "Listed tools from MCP server",
			map[string]any{
				"server":    name,
				"toolCount": len(tools),
			})
	}

	// Store connection. Re-check closed here, not just at the top of this
	// method: Close() drains m.servers to empty exactly once, under this
	// same lock. If Close() ran the drain before we reach this critical
	// section, storing anyway would leak a live session into a manager that
	// already believes it holds none — nothing will ever close it. Closing
	// the session we just created and returning an error keeps that
	// impossible.
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		if cerr := session.Close(); cerr != nil {
			logger.ErrorCF("mcp", "Failed to close session after connecting to an already-closed manager",
				map[string]any{
					"server": name,
					"error":  cerr.Error(),
				})
		}
		return fmt.Errorf("manager is closed")
	}
	m.servers[name] = &ServerConnection{
		Name:    name,
		Client:  client,
		Session: session,
		Tools:   tools,
		Config:  cfg,
	}
	m.mu.Unlock()

	return nil
}

// DisconnectServer closes and removes one server connection. Idempotent:
// returns nil if the server is not connected. Safe for concurrent use with
// CallTool/GetServers.
//
// Closed-manager handling deliberately differs from CallTool: CallTool
// returns an error on a closed manager because the caller wants an action to
// happen and none can. DisconnectServer's contract is the opposite — the
// caller wants the server to end up disconnected, and Close() already drains
// m.servers to empty, so "manager closed" and "server not connected" are the
// same end state. The fast m.closed check below is just that idempotent path
// taken early; it is not a distinct error case.
//
// The entry is looked up and removed from m.servers under m.mu, then the
// lock is released BEFORE Session.Close() runs — Close() can block on I/O
// (subprocess teardown / network round trip) and must not hold up
// GetServers/CallTool/ConnectServer for other servers while it drains. One
// consequence: a CallTool already in flight against this server when
// DisconnectServer runs may surface a transport error once Close() tears
// the session down underneath it; wg-based draining in Close() is
// manager-global, so a per-server drain here is out of scope.
func (m *Manager) DisconnectServer(name string) error {
	if m.closed.Load() {
		return nil
	}

	m.mu.Lock()
	conn, ok := m.servers[name]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.servers, name)
	m.mu.Unlock()

	logger.InfoCF("mcp", "Disconnecting MCP server",
		map[string]any{
			"server": name,
		})

	if err := conn.Session.Close(); err != nil {
		logger.ErrorCF("mcp", "Failed to close server connection",
			map[string]any{
				"server": name,
				"error":  err.Error(),
			})
		return fmt.Errorf("failed to close server %s: %w", name, err)
	}

	return nil
}

// GetServers returns all connected servers
func (m *Manager) GetServers() map[string]*ServerConnection {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*ServerConnection, len(m.servers))
	for k, v := range m.servers {
		result[k] = v
	}
	return result
}

// GetServer returns a specific server connection
func (m *Manager) GetServer(name string) (*ServerConnection, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	conn, ok := m.servers[name]
	return conn, ok
}

// CallTool calls a tool on a specific server
func (m *Manager) CallTool(
	ctx context.Context,
	serverName, toolName string,
	arguments map[string]any,
) (*mcp.CallToolResult, error) {
	// Check if closed before acquiring lock (fast path)
	if m.closed.Load() {
		return nil, fmt.Errorf("manager is closed")
	}

	m.mu.RLock()
	// Double-check after acquiring lock to prevent TOCTOU race
	if m.closed.Load() {
		m.mu.RUnlock()
		return nil, fmt.Errorf("manager is closed")
	}
	conn, ok := m.servers[serverName]
	if ok {
		m.wg.Add(1) // Add to WaitGroup while holding the lock
	}
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("server %s not found", serverName)
	}
	defer m.wg.Done()

	params := &mcp.CallToolParams{
		Name:      toolName,
		Arguments: arguments,
	}

	result, err := conn.Session.CallTool(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("failed to call tool: %w", err)
	}

	return result, nil
}

// Close closes all server connections
func (m *Manager) Close() error {
	// Use Swap to atomically set closed=true and get the previous value
	// This prevents TOCTOU race with CallTool's closed check
	if m.closed.Swap(true) {
		return nil // already closed
	}

	// Wait for all in-flight CallTool calls to finish before closing sessions
	// After closed=true is set, no new CallTool can start (they check closed first)
	m.wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	logger.InfoCF("mcp", "Closing all MCP server connections",
		map[string]any{
			"count": len(m.servers),
		})

	var errs []error
	for name, conn := range m.servers {
		if err := conn.Session.Close(); err != nil {
			logger.ErrorCF("mcp", "Failed to close server connection",
				map[string]any{
					"server": name,
					"error":  err.Error(),
				})
			errs = append(errs, fmt.Errorf("server %s: %w", name, err))
		}
	}

	m.servers = make(map[string]*ServerConnection)

	if len(errs) > 0 {
		return fmt.Errorf("failed to close %d server(s): %w", len(errs), errors.Join(errs...))
	}

	return nil
}

// GetAllTools returns all tools from all connected servers
func (m *Manager) GetAllTools() map[string][]*mcp.Tool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]*mcp.Tool)
	for name, conn := range m.servers {
		if len(conn.Tools) > 0 {
			result[name] = conn.Tools
		}
	}
	return result
}
