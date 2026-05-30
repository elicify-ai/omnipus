package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ergochat/readline"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// cliCancellerUser returns the OS user for cancel audit attribution in CLI mode.
func cliCancellerUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u := os.Getenv("USERNAME"); u != "" {
		return u
	}
	return "cli-user"
}

func agentCmd(message, sessionKey, model string, debug bool) error {
	if sessionKey == "" {
		sessionKey = "cli:default"
	}

	cfg, err := internal.LoadConfig()
	if err != nil {
		return fmt.Errorf("error loading config: %w", err)
	}

	logger.ConfigureFromEnv()

	if debug {
		logger.SetLevel(logger.DEBUG)
		fmt.Println("Debug mode enabled")
	}

	if model != "" {
		cfg.Agents.Defaults.ModelName = model
	}

	provider, modelID, err := providers.CreateProvider(cfg)
	if err != nil {
		return fmt.Errorf("error creating provider: %w", err)
	}

	// Use the resolved model ID from provider creation
	if modelID != "" {
		cfg.Agents.Defaults.ModelName = modelID
	}

	msgBus := bus.NewMessageBus()
	defer msgBus.Close()
	var agentLoop *agent.AgentLoop
	agentLoop, err = agent.NewAgentLoop(cfg, msgBus, provider)
	if err != nil {
		return fmt.Errorf("agent: boot failed: %w", err)
	}
	defer agentLoop.Close()

	// Print agent startup info (only for interactive mode)
	startupInfo := agentLoop.GetStartupInfo()
	logger.InfoCF("agent", "Agent initialized",
		map[string]any{
			"tools_count":      startupInfo["tools"].(map[string]any)["count"],
			"skills_total":     startupInfo["skills"].(map[string]any)["total"],
			"skills_available": startupInfo["skills"].(map[string]any)["available"],
		})

	if message != "" {
		// Non-interactive: attach the raw-stdin cancel watcher so pressing
		// Escape twice during inference cancels the turn (FR-3, FR-31a).
		ctx, cancel := context.WithCancel(context.Background())
		stopWatcher, _ := startRawStdinWatcher(ctx, cancel)
		response, err := agentLoop.ProcessDirect(ctx, message, sessionKey)
		stopWatcher()
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(os.Stderr, "(interrupted)")
				return nil
			}
			return fmt.Errorf("error processing message: %w", err)
		}
		fmt.Printf("\n%s %s\n", internal.Logo, response)
		return nil
	}

	fmt.Printf("%s Interactive mode (Ctrl+C to exit, Esc Esc to cancel inference)\n\n", internal.Logo)
	interactiveMode(agentLoop, sessionKey)

	return nil
}

func interactiveMode(agentLoop *agent.AgentLoop, sessionKey string) {
	prompt := fmt.Sprintf("%s You: ", internal.Logo)

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		HistoryFile:     filepath.Join(os.TempDir(), ".omnipus_history"),
		HistoryLimit:    100,
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Printf("Error initializing readline: %v\n", err)
		fmt.Println("Falling back to simple input mode...")
		simpleInteractiveMode(agentLoop, sessionKey)
		return
	}
	defer rl.Close()

	for {
		// readline owns stdin here. No watcher is active.
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt || err == io.EOF {
				fmt.Println("\nGoodbye!")
				return
			}
			fmt.Printf("Error reading input: %v\n", err)
			continue
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			return
		}

		// readline has returned; we now own stdin. Start the raw-stdin watcher
		// for the duration of this ProcessDirect call (FR-3, FR-31a, EC-12).
		ctx, cancel := context.WithCancel(context.Background())
		stopWatcher, watcherActive := startRawStdinWatcher(ctx, cancel)

		response, procesErr := agentLoop.ProcessDirect(ctx, input, sessionKey)

		// Transfer stdin ownership back to readline: stop the watcher and
		// restore canonical mode before calling rl.Readline() again.
		stopWatcher()
		cancel() // ensure ctx is always canceled so the goroutine exits

		if procesErr != nil {
			if ctx.Err() != nil {
				// Canceled by double-Escape. Fire the full cancel state machine so
				// audit, transcript marking, abuse detection, and the 2-stage timer
				// apply uniformly (cancel-centralization, resolves finding B2).
				if _, err := agentLoop.RequestCancel(
					context.Background(),
					agent.CancelScope{SessionID: sessionKey},
					agent.CancelCanceller{UserID: cliCancellerUser(), Channel: "cli"},
					agent.CancelHooks{}, // no transport-specific side-effects in CLI
				); err != nil {
					slog.Warn("cli: cancel request failed", "session", sessionKey, "error", err)
				}
				fmt.Println("\n(interrupted)")
				if watcherActive {
					// Print a blank line so the next readline prompt is clean.
					fmt.Println()
				}
				continue
			}
			fmt.Printf("Error: %v\n", procesErr)
			continue
		}

		fmt.Printf("\n%s %s\n\n", internal.Logo, response)
	}
}

func simpleInteractiveMode(agentLoop *agent.AgentLoop, sessionKey string) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print(fmt.Sprintf("%s You: ", internal.Logo))
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println("\nGoodbye!")
				return
			}
			fmt.Printf("Error reading input: %v\n", err)
			continue
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		if input == "exit" || input == "quit" {
			fmt.Println("Goodbye!")
			return
		}

		// Simple mode: no raw-stdin watcher (bufio.Reader already owns stdin
		// at the level below; adding a concurrent raw reader would race). The
		// user falls back to Ctrl+C to cancel.
		ctx := context.Background()
		response, err := agentLoop.ProcessDirect(ctx, input, sessionKey)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		fmt.Printf("\n%s %s\n\n", internal.Logo, response)
	}
}
