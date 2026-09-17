// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// stdjson-only gate (NOT goolm): this file needs nothing from goolm — the tag
// only matters for pkg/channels/matrix, which degrades gracefully when absent.
// Gating on stdjson alone keeps the fail-fast against tag-less builds while
// letting the mipsle targets (which must strip goolm: its transitive deps
// don't build on mips softfloat) link. A goolm gate here broke build-lite's
// mipsle line and build-linux-mipsle with "function main is undeclared".
//go:build stdjson

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata" // FR-018: embed IANA tzdata so RRULE `tz` resolution works on minimal systems (single-binary Constraint #1) without a system zoneinfo database.

	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"

	"github.com/elicify-ai/omnipus/cmd/omnipus/internal"
	auditcmd "github.com/elicify-ai/omnipus/cmd/omnipus/internal/audit"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/clitoken"
	credcmd "github.com/elicify-ai/omnipus/cmd/omnipus/internal/credentials"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/doctor"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/gateway"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/onboard"
	recordscmd "github.com/elicify-ai/omnipus/cmd/omnipus/internal/records"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/run"
	stopcmd "github.com/elicify-ai/omnipus/cmd/omnipus/internal/stop"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/version"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/daemon"
)

// loadConfigWithAgents loads config.json AND repopulates cfg.Agents.List from
// the agent store (ADR-054 D2/D3: agents are per-entity records under
// entities/agents/<id>.json now, not config.json's agents.list — config.
// LoadConfig strips any legacy agents.list content on every load,
// legacy_agents_list.go — so every CLI call site that reads cfg.Agents.List
// must go through this instead of a bare config.LoadConfig).
//
// homePath is threaded through explicitly rather than derived from cfg (a
// now-deleted helper used a filepath.Dir(cfg.AgentHomeBasePath())
// derivation) — that derivation assumes Agents.Defaults.Home is always
// exactly <home>/workspace, which does not hold for every fixture/config
// shape and was verified (2026-07-25) to silently point at the wrong
// directory in several pkg/gateway test fixtures once swapped in. Best-effort:
// a store-list failure only logs — an empty roster degrades to "no agents
// configured" (an existing, already-handled CLI case) rather than aborting.
func loadConfigWithAgents(configPath, homePath string) (*config.Config, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	agents, skipped, listErr := agentstore.New(homePath).List()
	if listErr != nil {
		fmt.Fprintf(os.Stderr, "warning: could not list agent entity records: %v\n", listErr)
		return cfg, nil
	}
	cfg.Agents.List = agents
	cfg.SkippedAgentIDs = skipped
	// Entity-loaded agents never pass through config.loadConfigInternal's own
	// NormalizeFallbacks/migrateAgentPrimaryProvider passes — those only run
	// against config.json's agents.list inside config.LoadConfig itself,
	// which is stripped to empty before this bridge re-injects the real
	// per-entity roster (ADR-054, legacy_agents_list.go). Apply both here so
	// an agent whose FallbackModel/primary-model fields were written
	// pre-split still resolves correctly for this CLI invocation.
	config.NormalizeAgentRoster(cfg)
	return cfg, nil
}

// rosterLine describes a single chat-target agent for the no-args listing.
type rosterLine struct {
	ID   string
	Name string
}

// buildRoster returns the chat-target agents from the given config, excluding
// workers. The ordering mirrors the order in cfg.Agents.List (FR-008/US-6).
func buildRoster(cfg *config.Config) []rosterLine {
	lines := make([]rosterLine, 0, len(cfg.Agents.List))
	for _, a := range cfg.Agents.List {
		if a.IsChatTarget() {
			lines = append(lines, rosterLine{ID: a.ID, Name: a.Name})
		}
	}
	return lines
}

// printRosterAndUsage writes the agent roster and a usage block to w.
// Called when omnipus is invoked with no arguments (FR-008/US-6).
func printRosterAndUsage(w *os.File, roster []rosterLine) {
	fmt.Fprintln(w, "Agents:")
	for _, r := range roster {
		fmt.Fprintf(w, "  %-20s %s\n", r.ID, r.Name)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, `  omnipus <agent> "<prompt>"     run a one-shot task`)
	fmt.Fprintln(w, `  omnipus <agent> --model <slug> "<prompt>"`)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  omnipus onboard      first-time setup — configure a provider and admin account")
	fmt.Fprintln(w, "  omnipus start        start Omnipus (SPA + API on port 5000)")
	fmt.Fprintln(w, "  omnipus stop         stop a running Omnipus gateway")
	fmt.Fprintln(w, "  omnipus credentials  manage secrets (set/list/delete/rotate)")
	fmt.Fprintln(w, "  omnipus audit        view the audit log")
	fmt.Fprintln(w, "  omnipus doctor       diagnose configuration issues")
	fmt.Fprintln(w, "  omnipus version      print build version")
	fmt.Fprintln(w)
	fmt.Fprintln(w, `Run "omnipus --help" for full documentation.`)
}

// removedVerbs is the set of CLI verbs removed in the CLI redesign. When the
// user types one of these, RunE prints a helpful message instead of the
// confusing "unknown agent" error that would otherwise appear because the root
// uses cobra.ArbitraryArgs (US-11/AC-1).
//
// Note: "stop" is intentionally NOT in this map — it is a real registered
// subcommand. It is called out here only so nobody accidentally re-adds it as
// a removed verb.
var removedVerbs = map[string]bool{
	"agent":   true,
	"auth":    true,
	"status":  true,
	"cron":    true,
	"migrate": true,
	"model":   true,
	"skills":  true,
}

// autoStartPollTimeout is the maximum time to wait for the gateway to be ready
// after daemon.Spawn before giving up and returning an error.
const autoStartPollTimeout = 30 * time.Second

// hasNonInteractiveKeyMode reports whether a non-interactive unlock mode is
// available (i.e. the process can unlock credentials without a TTY passphrase
// prompt). This is the prerequisite for auto-starting the gateway from the CLI.
//
// The three non-interactive modes are:
//  1. OMNIPUS_MASTER_KEY env var set (64-char hex key).
//  2. OMNIPUS_KEY_FILE env var set (path to a 0600 file with the hex key).
//  3. <home>/master.key exists on disk (default key file path).
func hasNonInteractiveKeyMode(home string) bool {
	if os.Getenv("OMNIPUS_MASTER_KEY") != "" {
		return true
	}
	if os.Getenv("OMNIPUS_KEY_FILE") != "" {
		return true
	}
	_, err := os.Stat(filepath.Join(home, "master.key"))
	return err == nil
}

// probeGatewayOnce tries one TCP connect + WS auth round-trip against addr.
// It returns true when the gateway is ready to serve authenticated connections,
// false otherwise. It is a guard-clause helper extracted from pollGatewayReady
// to flatten nesting and enable unit testing of individual probe logic.
func probeGatewayOnce(ctx context.Context, dialer *websocket.Dialer, addr, token string) bool {
	// Try a TCP connect first to avoid the WS dialer's error noise on
	// connection refused (expected during early startup).
	tcpConn, tcpErr := net.DialTimeout("tcp", addr, 1*time.Second)
	if tcpErr != nil {
		return false
	}
	tcpConn.Close()

	// TCP up — attempt WS auth round-trip.
	wsURL := "ws://" + addr + "/api/v1/chat/ws"
	conn, wsResp, wsErr := dialer.DialContext(ctx, wsURL, nil)
	if wsResp != nil {
		wsResp.Body.Close()
	}
	if wsErr != nil {
		return false
	}

	// Send auth frame and read at least one frame back. A valid frame
	// (of any type, including an error frame) proves the WS layer is up
	// and auth is being processed. ErrKeyInvalid is fine here — the
	// gateway is up; the caller will retry run.Run which will fail on its
	// own auth path if the token is wrong.
	authFrame := generated.AuthFrame{
		Type:  string(generated.WsFrameTypeAuth),
		Token: token,
	}
	authData, _ := json.Marshal(authFrame)
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	writeErr := conn.WriteMessage(websocket.TextMessage, authData)
	if writeErr != nil {
		conn.Close()
		return false
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, readErr := conn.ReadMessage()
	conn.Close()
	if readErr == nil {
		return true
	}
	// A close-error response (e.g. ClosePolicyViolation on bad token)
	// still means the gateway is up and handling connections.
	var closeErr *websocket.CloseError
	return errors.As(readErr, &closeErr)
}

// pollGatewayReady polls until the gateway at addr accepts a full WebSocket
// auth round-trip (TCP connect + WS upgrade + auth frame + any valid response
// frame). This is stricter than /ready which goes green early before the auth
// layer is up. It polls with a short interval and returns nil when the gateway
// is fully accepting authenticated connections, or an error after timeout.
//
// spawnedPID is the PID returned by daemon.Spawn for the child we just
// started. When non-zero, each poll iteration first checks whether the child
// is still alive via daemon.Status; if the child has already exited (port
// conflict, bad config, unlock failure) the poll aborts immediately with an
// actionable error pointing at the gateway log rather than spinning the full
// timeout.
func pollGatewayReady(ctx context.Context, home, addr, token string, spawnedPID int) error {
	ctx, cancel := context.WithTimeout(ctx, autoStartPollTimeout)
	defer cancel()

	const pollInterval = 500 * time.Millisecond

	dialer := &websocket.Dialer{HandshakeTimeout: 2 * time.Second}

	for {
		// If we know which PID was spawned, check it is still alive before
		// spending time on the WS probe. A dead child means startup failed
		// (port taken, bad config, unlock failure) and we should not wait
		// out the full timeout.
		if spawnedPID > 0 {
			running, livePID, statusErr := daemon.Status(home)
			if statusErr == nil && !running {
				// Child is gone — the gateway failed to start. Point the
				// operator at the log for the real error.
				logPath := filepath.Join(home, "logs", "gateway_panic.log")
				return fmt.Errorf(
					"spawned gateway (pid %d) exited before becoming ready — "+
						"check %s for details",
					spawnedPID, logPath,
				)
			}
			// If Status reports a different live PID, a new instance raced in;
			// that is fine — keep probing the address.
			_ = livePID
		}

		if probeGatewayOnce(ctx, dialer, addr, token) {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway did not become ready within %s: %w", autoStartPollTimeout, ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

// spawnFunc is the signature for injecting a spawn implementation in tests.
// Returns the spawned PID and any error.
type spawnFunc func(home string, args []string) (pid int, err error)

// readyPollerFunc is the signature for injecting a ready-poller in tests.
type readyPollerFunc func(ctx context.Context, home, addr, token string, spawnedPID int) error

// statusFunc is the signature for injecting a daemon-status check in tests.
// Matches daemon.Status(home) (running bool, pid int, err error).
type statusFunc func(home string) (running bool, pid int, err error)

// autoStartResult is the outcome of autoStartAndRetry, used to cleanly
// separate "orchestration error (already printed, just exit)" from "retry
// produced a run sentinel error (let the switch in RunE handle printing)".
type autoStartResult struct {
	// orchErr is set when the orchestration itself failed (no key, status
	// lookup error, spawn error, poll timeout). The message has already been
	// printed to stderr by autoStartAndRetry; the caller should os.Exit(1).
	orchErr error
	// runErr is set when orchestration succeeded but the retry run.Run call
	// returned an error. The caller's sentinel switch should handle printing.
	runErr error
}

// autoStartAndRetry is the auto-start orchestration seam (FR-016). It is
// extracted from RunE so it can be tested without spawning real processes.
//
// Decision tree:
//  1. Call keyCheck(home): if no non-interactive key mode → print guidance,
//     return orchErr WITHOUT spawning.
//  2. Call daemon.Status(home): if the gateway is already running → the dial
//     error from run.Run was transient (not connection-refused); print a hint
//     and return orchErr WITHOUT spawning.
//  3. Spawn the gateway via daemon.Spawn(home, nil).
//  4. Poll until ready via pollGatewayReady.
//  5. Retry run.Run once; return its error as runErr.
func autoStartAndRetry(
	ctx context.Context,
	home, addr, token string,
	keyCheck func(string) bool,
	opts run.Options,
) autoStartResult {
	return autoStartAndRetryWith(ctx, home, addr, token, keyCheck, opts,
		daemon.Status, daemon.Spawn, pollGatewayReady)
}

// autoStartAndRetryWith is the fully-injectable version of autoStartAndRetry
// used by tests to supply fake status, spawn, and poll implementations.
func autoStartAndRetryWith(
	ctx context.Context,
	home, addr, token string,
	keyCheck func(string) bool,
	opts run.Options,
	status statusFunc,
	spawn spawnFunc,
	poller readyPollerFunc,
) autoStartResult {
	// 1. Non-interactive key mode required to auto-start.
	if !keyCheck(home) {
		fmt.Fprintln(os.Stderr,
			"no gateway running and no non-interactive key configured; run `omnipus start`")
		return autoStartResult{orchErr: fmt.Errorf("no non-interactive key mode available")}
	}

	// 2. Check whether the gateway is actually running before spawning. If
	// status reports it running, the ErrGatewayDown was a transient handshake
	// failure on a live gateway — spawning again would orphan it.
	running, _, statusErr := status(home)
	if statusErr != nil {
		// Cannot determine status — do NOT spawn; surface the ambiguity.
		fmt.Fprintf(os.Stderr,
			"error: could not determine gateway status: %v\n"+
				"The gateway may be running but unreachable. Run `omnipus start` manually.\n",
			statusErr,
		)
		return autoStartResult{orchErr: statusErr}
	}
	if running {
		// Gateway is up but the WS dial failed transiently — don't spawn.
		fmt.Fprintln(os.Stderr,
			"Omnipus is running but the connection failed — "+
				"the gateway may still be starting up. Try again shortly.")
		return autoStartResult{orchErr: run.ErrGatewayDown}
	}

	// 3. Spawn.
	fmt.Fprintln(os.Stderr, "Omnipus is not running — starting gateway...")
	spawnedPID, spawnErr := spawn(home, nil)
	if spawnErr != nil {
		fmt.Fprintf(os.Stderr, "error: failed to start gateway: %v\n", spawnErr)
		return autoStartResult{orchErr: spawnErr}
	}

	// 4. Poll until the gateway accepts authenticated WS connections.
	pollErr := poller(ctx, home, addr, token, spawnedPID)
	if pollErr != nil {
		fmt.Fprintf(os.Stderr, "error: gateway did not become ready: %v\n", pollErr)
		return autoStartResult{orchErr: pollErr}
	}

	// 5. Retry. Return the run error (if any) via runErr so the caller's
	// sentinel switch can print the appropriate message.
	return autoStartResult{runErr: run.Run(ctx, opts)}
}

// NewOmnipusCommand builds the root cobra command with the minimized CLI tree.
//
// Subcommands (resolved first by cobra): onboard, start, credentials, audit, doctor, version.
// Root RunE handles the positional <agent> [<prompt>] execute path (FR-001/002/008).
// Removed verbs (agent, auth, status, cron, migrate, model, skills) are no longer registered;
// typing one prints a helpful message (US-11/AC-1) rather than "unknown agent".
func NewOmnipusCommand() *cobra.Command {
	short := fmt.Sprintf("%s omnipus - Personal AI Assistant v%s\n\n", internal.Logo, config.GetVersion())

	// Per-run flags — declared here, attached to root.
	var flagModel string
	var flagYes bool
	var flagTimeout time.Duration
	var flagURL string

	cmd := &cobra.Command{
		Use:   "omnipus",
		Short: short,
		Long: fmt.Sprintf(`%s omnipus — Personal AI Assistant v%s

Run a one-shot task against a named agent:

  omnipus <agent> "<prompt>"
  omnipus <agent> --model <slug> "<prompt>"

With no arguments, lists available agents and usage.

Commands:
  onboard      First-time setup — configure a provider and admin account.
  start        Start Omnipus (SPA + API on port 5000). Alias: gateway, g.
  stop         Stop a running Omnipus gateway.
  credentials  Manage secrets: set, list, delete, rotate.
  audit        View the audit log.
  doctor       Diagnose configuration issues.
  version      Print build version.

Examples:
  omnipus jim "summarize the last 10 commits"
  omnipus mia "what can you do?"
  omnipus jim --model openrouter/glm-5.2 "explain Go generics"
  omnipus onboard
  omnipus start
  omnipus stop

Reserved names (shadowed by subcommands): onboard, start, stop, credentials, audit, doctor, version.
If an agent shares a name with a subcommand, use the agent's ID directly via the API or rename it.
`, internal.Logo, config.GetVersion()),
		Example: `  omnipus jim "summarize the last 10 commits"
  omnipus mia "what can you do?"
  omnipus onboard
  omnipus start`,

		// DisableFlagParsing is not set; TraverseChildren allows flags on the root
		// to be declared so --model/--yes/--timeout/--url are available on the run path.
		//
		// Cobra subcommand resolution: cobra checks args[0] against registered
		// subcommand names (and aliases) first. When args[0] matches, the subcommand
		// takes over. Only when args[0] does NOT match a subcommand does RunE fire.
		// We set Args to ArbitraryArgs so cobra doesn't error on unknown args[0]
		// before RunE gets a chance to interpret them as the agent name.
		Args: cobra.ArbitraryArgs,

		// SilenceUsage suppresses cobra's auto-printed usage on RunE errors so
		// our own formatted messages are the only thing printed.
		SilenceUsage: true,

		RunE: func(cmd *cobra.Command, args []string) error {
			home := internal.GetOmnipusHome()
			configPath := internal.GetConfigPath()

			// FR-007: --url guard (remote unsupported in P0).
			if flagURL != "" {
				fmt.Fprintln(os.Stderr, "error: remote gateways are not supported yet")
				os.Exit(1)
			}

			// 0 args → print roster + usage (FR-008/US-6).
			if len(args) == 0 {
				cfg, loadErr := loadConfigWithAgents(configPath, home)
				if loadErr != nil {
					fmt.Fprintln(os.Stderr, "error: failed to load config:", loadErr)
					os.Exit(1)
				}
				roster := buildRoster(cfg)
				if len(roster) == 0 && len(cfg.Agents.List) == 0 {
					// Pre-onboard: no agents seeded yet.
					fmt.Fprintln(os.Stderr, "No agents configured. Run `omnipus onboard` first.")
					os.Exit(1)
				}
				printRosterAndUsage(os.Stdout, roster)
				return nil
			}

			agentID := args[0]

			// US-11/AC-1: if args[0] is a removed verb, print a helpful message
			// before attempting the agent-lookup (which would print the confusing
			// "unknown agent" error because the root uses ArbitraryArgs).
			if removedVerbs[agentID] {
				fmt.Fprintf(os.Stderr,
					"%q was removed in the CLI redesign — run 'omnipus --help' for the current commands\n",
					agentID,
				)
				os.Exit(1)
			}

			// Exactly 1 arg (agent, no prompt) → usage error.
			if len(args) == 1 {
				fmt.Fprintf(os.Stderr, "error: provide a prompt: omnipus %s \"<prompt>\"\n", agentID)
				os.Exit(1)
			}

			// Join remaining args as the prompt (allows: omnipus jim word1 word2).
			prompt := strings.Join(args[1:], " ")

			// Empty prompt guard (edge case: omnipus jim "").
			if strings.TrimSpace(prompt) == "" {
				fmt.Fprintf(os.Stderr, "error: provide a prompt: omnipus %s \"<prompt>\"\n", agentID)
				os.Exit(1)
			}

			// Load config to validate the agent.
			cfg, loadErr := loadConfigWithAgents(configPath, home)
			if loadErr != nil {
				fmt.Fprintln(os.Stderr, "error: failed to load config:", loadErr)
				os.Exit(1)
			}

			// FR-002: validate agent is a chat-target.
			var agentFound bool
			for _, a := range cfg.Agents.List {
				if a.ID == agentID {
					agentFound = true
					if !a.IsChatTarget() {
						fmt.Fprintf(os.Stderr, "error: %q is a worker agent and cannot be run directly\n", agentID)
						os.Exit(1)
					}
					break
				}
			}
			if !agentFound {
				fmt.Fprintf(os.Stderr, "error: unknown agent %q — run `omnipus` to list available agents\n", agentID)
				os.Exit(1)
			}

			// FR-006: load the CLI token.
			token, tokenErr := clitoken.LoadCLIToken(home)
			if tokenErr != nil {
				if errors.Is(tokenErr, clitoken.ErrNoCLIToken) {
					fmt.Fprintln(os.Stderr, "error: no CLI key found — run `omnipus start` to create one")
					os.Exit(1)
				}
				fmt.Fprintln(os.Stderr, "error: failed to load CLI key:", tokenErr)
				os.Exit(1)
			}

			// Build the local gateway address from config.
			addr := fmt.Sprintf("localhost:%d", cfg.Gateway.Port)

			// runOptions holds the common options for run.Run calls (first attempt
			// and the auto-start retry share the same options).
			runOptions := run.Options{
				Agent:   agentID,
				Prompt:  prompt,
				Model:   flagModel,
				Yes:     flagYes,
				Timeout: flagTimeout,
				URL:     flagURL,
				Addr:    addr,
				Token:   token,
				Stdout:  os.Stdout,
				Stderr:  os.Stderr,
			}

			// Dispatch to the run client.
			runErr := run.Run(cmd.Context(), runOptions)

			// Auto-start (P1/FR-016): when the gateway is not reachable and a
			// non-interactive unlock mode is available, spawn the gateway and retry.
			if errors.Is(runErr, run.ErrGatewayDown) {
				result := autoStartAndRetry(cmd.Context(), home, addr, token,
					hasNonInteractiveKeyMode, runOptions)
				if result.orchErr != nil {
					// Orchestration failed; message already printed. Exit non-zero.
					os.Exit(1)
				}
				// Retry run error (if any) falls through to the sentinel switch
				// below so the mapped message is printed exactly once.
				runErr = result.runErr
			}

			if runErr == nil {
				return nil
			}

			// Map sentinel errors to user-facing messages + non-zero exit.
			// Sentinel values carry complete user-facing messages; print verbatim.
			// Only the default (wrapped) case prepends "error:" to avoid duplication.
			switch {
			case errors.Is(runErr, run.ErrRemoteUnsupported):
				fmt.Fprintln(os.Stderr, run.ErrRemoteUnsupported.Error())
			case errors.Is(runErr, run.ErrGatewayDown):
				fmt.Fprintln(os.Stderr, run.ErrGatewayDown.Error())
			case errors.Is(runErr, run.ErrKeyInvalid):
				fmt.Fprintln(os.Stderr, run.ErrKeyInvalid.Error())
			case errors.Is(runErr, run.ErrTimeout):
				fmt.Fprintln(os.Stderr, run.ErrTimeout.Error())
			case errors.Is(runErr, run.ErrTurnFailed):
				fmt.Fprintln(os.Stderr, run.ErrTurnFailed.Error())
			default:
				fmt.Fprintln(os.Stderr, "error:", runErr)
			}
			os.Exit(1)
			return nil // unreachable; satisfies RunE signature
		},
	}

	// Per-run flags.
	cmd.Flags().StringVar(&flagModel, "model", "", "Override the model for this run (e.g. openrouter/glm-5.2)")
	cmd.Flags().BoolVar(&flagYes, "yes", false, "Auto-approve tool-approval requests (default: deny-and-continue)")
	cmd.Flags().DurationVar(&flagTimeout, "timeout", 300*time.Second, "Maximum time to wait for the agent to finish")
	cmd.Flags().StringVar(&flagURL, "url", "", "Remote gateway URL (not supported yet; reserved for P1)")

	// Subcommands — all kept verbs. Subcommands resolve before RunE, so these
	// names shadow same-named agents (documented in Long + US-11/OBS-2).
	cmd.AddCommand(
		onboard.NewOnboardCommand(),
		auditcmd.NewAuditCommand(),
		credcmd.NewCredentialsCommand(),
		recordscmd.NewRecordsCommand(),
		doctor.NewDoctorCommand(),
		gateway.NewGatewayCommand(),
		stopcmd.NewStopCommand(),
		version.NewVersionCommand(),
	)

	return cmd
}

func main() {
	cmd := NewOmnipusCommand()
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		os.Exit(1)
	}
}
