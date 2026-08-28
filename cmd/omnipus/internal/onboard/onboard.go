// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package onboard provides the interactive `omnipus onboard` CLI wizard.
//
// The wizard performs the same end-state mutations as the REST onboarding
// handler (pkg/gateway.HandleCompleteOnboarding) but without requiring a
// running gateway or a browser — necessary for headless container deployments
// where the docker entrypoint previously gated boot on this command. See
// issue #159.
//
// On success the wizard:
//
//  1. ensures ~/.omnipus/ is initialized (datamodel.Init);
//  2. unlocks the credentials store (auto-generating master.key on a fresh
//     install per the same boot contract gateway uses);
//  3. encrypts the provider API key into credentials.json;
//  4. writes a provider entry and an admin user entry into config.json;
//  5. mints the cli principal and token file (clitoken.EnsureCLIToken);
//  6. marks onboarding complete in system/state.json.
//
// After this runs, `omnipus start` boots without requiring dev_mode_bypass
// or env-injected secrets — the admin can log in immediately with the
// username/password they entered.
package onboard

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/clitoken"
	"github.com/elicify-ai/omnipus/cmd/omnipus/internal/netinfo"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/onboarding"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// wizardIO is the dependency surface the wizard talks to. Splitting it out
// makes the command testable against a scripted stdin without going through
// the real terminal.
type wizardIO struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// readPassword reads a line from stdin without echoing it. In production
	// this is term.ReadPassword over os.Stdin; in tests it is a stub that
	// returns the next scripted line.
	readPassword func() (string, error)
	// skipVerify controls whether the live provider-key probe is skipped.
	// Set by --skip-verify; passed through to applyInput in the interactive path.
	skipVerify bool
}

// Input is the validated set of answers the wizard collected. Exposed for
// test injection — production code never instantiates this directly.
type Input struct {
	Home       string
	ProviderID string
	APIKey     string
	Model      string
	Username   string
	Password   string
	// SkipVerify mirrors the --skip-verify flag: when true, no live key probe
	// is performed and the key is persisted unconditionally (FR-015).
	SkipVerify bool
	// NonInteractive mirrors the --non-interactive flag: when true, an InvalidKey
	// outcome exits non-zero rather than re-prompting.
	NonInteractive bool
}

// providerMenuItem is one entry in the numbered provider menu.
type providerMenuItem struct {
	label      string // display label, e.g. "OpenRouter"
	providerID string // protocol id passed downstream, e.g. "openrouter"
}

// providerMenu is the curated short list shown to the user during interactive
// onboarding (FR-010/US-8). "Other" (the last entry) has an empty providerID
// and triggers a raw protocol-id prompt.
// Every providerID below is an exact CATALOG id (ADR-067 FR-011); the labels
// are the catalog's own display names. "Other" prompts for any of the ~190
// remaining catalog ids.
var providerMenu = []providerMenuItem{
	{label: "OpenRouter", providerID: "openrouter"},
	{label: "Anthropic", providerID: "anthropic"},
	{label: "OpenAI", providerID: "openai"},
	{label: "Google Gemini", providerID: "google"},
	{label: "Groq", providerID: "groq"},
	{label: "DeepSeek", providerID: "deepseek"},
	{label: "Other (enter provider id)", providerID: ""},
}

// defaultModelFor provides the CLI's default model for a provider when the
// user doesn't name one: the first active, tool-calling, text model the
// EMBEDDED CATALOG SNAPSHOT lists for that provider (ADR-067 A-21, FR-022).
//
// It used to be a six-case table of hand-typed slugs maintained separately
// from the REST onboarding defaults. Both drifted, and a retired slug there
// wrote a config whose very first turn 404'd. The snapshot ships in the
// binary, so this needs no network and cannot disagree with what the runtime
// will accept.
func defaultModelFor(providerID string) string {
	return providers.DefaultProbeModel(providerID)
}

// NewOnboardCommand returns the `omnipus onboard` Cobra command.
func NewOnboardCommand() *cobra.Command {
	var (
		homeFlag           string
		providerFlag       string
		apiKeyFlag         string
		apiKeyStdin        bool
		modelFlag          string
		adminUsernameFlag  string
		adminPasswordFlag  string
		adminPasswordStdin bool
		nonInteractive     bool
		skipVerify         bool
	)
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "First-run setup (provider, API key, admin user) — interactive or headless",
		Long: `Configure a fresh Omnipus install.

Two modes:

  Interactive (default)
    Presents a numbered provider menu, prompts for API key, default model,
    and admin username/password, then mints the CLI token.

  Headless
    Pass --non-interactive together with all of --provider, --api-key
    (or --api-key-stdin), --admin-username, and --admin-password (or
    --admin-password-stdin). --model is optional and defaults to a
    sensible per-provider model.

In both modes the API key is encrypted into credentials.json (creating
master.key on first run); the admin user and provider entry are written
to config.json; the cli principal and its token file are created; and
system/state.json is marked complete.

After running this command, ` + "`omnipus start`" + ` can boot without
` + "`dev_mode_bypass`" + ` and the operator can log in with the credentials
they just entered. The web onboarding wizard remains available for users
who prefer a browser.

Examples:

  # interactive
  omnipus onboard

  # headless (key + password on command line — visible to other users on the box)
  omnipus onboard --non-interactive \
    --provider openrouter \
    --api-key sk-or-v1-... \
    --model 'z-ai/glm-5v-turbo' \
    --admin-username admin \
    --admin-password 'p@ssw0rd!'

  # headless (key + password from stdin — safer)
  printf 'sk-or-v1-...\np@ssw0rd!\n' | omnipus onboard --non-interactive \
    --provider openrouter \
    --api-key-stdin \
    --admin-username admin \
    --admin-password-stdin`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := homeFlag
			if home == "" {
				home = os.Getenv("OMNIPUS_HOME")
			}
			if home == "" {
				h, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("resolve $HOME: %w", err)
				}
				home = filepath.Join(h, ".omnipus")
			}
			if nonInteractive {
				in, err := inputFromFlags(cmd.InOrStdin(), inputFlags{
					providerID:         providerFlag,
					apiKey:             apiKeyFlag,
					apiKeyStdin:        apiKeyStdin,
					model:              modelFlag,
					username:           adminUsernameFlag,
					password:           adminPasswordFlag,
					adminPasswordStdin: adminPasswordStdin,
				})
				if err != nil {
					return err
				}
				in.SkipVerify = skipVerify
				in.NonInteractive = true
				return RunHeadless(home, defaultIO(cmd), in)
			}
			wio := defaultIO(cmd)
			wio.skipVerify = skipVerify
			return Run(home, wio)
		},
	}
	cmd.Flags().StringVar(&homeFlag, "home", "", "Omnipus home directory (default: $OMNIPUS_HOME or ~/.omnipus)")
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Skip all prompts; require the answers via flags")
	cmd.Flags().StringVar(&providerFlag, "provider", "", "Provider id (e.g. openrouter, anthropic, openai, gemini)")
	cmd.Flags().StringVar(
		&apiKeyFlag,
		"api-key",
		"",
		"Provider API key (visible to other users via /proc — prefer --api-key-stdin)",
	)
	cmd.Flags().BoolVar(&apiKeyStdin, "api-key-stdin", false, "Read the provider API key from the first line of stdin")
	cmd.Flags().StringVar(
		&modelFlag,
		"model",
		"",
		"Default model id (e.g. z-ai/glm-5v-turbo); falls back to a per-provider default",
	)
	cmd.Flags().StringVar(&adminUsernameFlag, "admin-username", "", "Admin username to create")
	cmd.Flags().StringVar(
		&adminPasswordFlag,
		"admin-password",
		"",
		"Admin password (min 8 chars; prefer --admin-password-stdin)",
	)
	cmd.Flags().BoolVar(
		&adminPasswordStdin,
		"admin-password-stdin",
		false,
		"Read the admin password from stdin (next line after the api key if --api-key-stdin is also set)",
	)
	cmd.Flags().BoolVar(
		&skipVerify,
		"skip-verify",
		false,
		"Skip the live provider-key check (offline/air-gapped setup or known-good key).",
	)
	return cmd
}

func defaultIO(cmd *cobra.Command) wizardIO {
	return wizardIO{
		stdin:  cmd.InOrStdin(),
		stdout: cmd.OutOrStdout(),
		stderr: cmd.ErrOrStderr(),
		readPassword: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			if err != nil {
				return "", err
			}
			return string(b), nil
		},
	}
}

// inputFlags is the raw flag bundle the Cobra binding hands to
// inputFromFlags. Kept as a struct rather than a parameter list so the
// signature stays stable as we add knobs.
type inputFlags struct {
	providerID         string
	apiKey             string
	apiKeyStdin        bool
	model              string
	username           string
	password           string
	adminPasswordStdin bool
}

// inputFromFlags assembles an Input from --non-interactive flags, validating
// each field with the same rules the interactive prompt applies. Secret
// values are read from stdin when --api-key-stdin or --admin-password-stdin
// is set; the api key (if requested) is read first, then the admin password,
// each as a single line.
func inputFromFlags(stdin io.Reader, f inputFlags) (Input, error) {
	in := Input{}

	if f.providerID == "" {
		return in, errors.New("--provider is required in --non-interactive mode")
	}
	// ADR-067 FR-019/FR-035, A-21: the SAME admission gate the gateway's PUT
	// and onboarding probe apply, resolved against the EMBEDDED catalog
	// snapshot (no network, no gateway). The wizard has no --api-base /
	// --protocol pair, so a custom row cannot be created here and an id the
	// snapshot does not carry is simply unknown; a `tier: unsupported` row is
	// refused with the catalog's own reason instead of being written into a
	// config whose very first turn cannot construct a provider.
	if _, admitErr := providers.Admit(f.providerID, "", ""); admitErr != nil {
		return in, admitErr
	}
	in.ProviderID = f.providerID

	if f.apiKey != "" && f.apiKeyStdin {
		return in, errors.New("set exactly one of --api-key or --api-key-stdin, not both")
	}
	if f.password != "" && f.adminPasswordStdin {
		return in, errors.New("set exactly one of --admin-password or --admin-password-stdin, not both")
	}

	apiKey := f.apiKey
	password := f.password
	if f.apiKeyStdin || f.adminPasswordStdin {
		reader := bufio.NewReader(stdin)
		if f.apiKeyStdin {
			line, err := reader.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return in, fmt.Errorf("read api key from stdin: %w", err)
			}
			apiKey = strings.TrimRight(line, "\r\n")
		}
		if f.adminPasswordStdin {
			line, err := reader.ReadString('\n')
			if err != nil && !errors.Is(err, io.EOF) {
				return in, fmt.Errorf("read admin password from stdin: %w", err)
			}
			password = strings.TrimRight(line, "\r\n")
		}
	}

	if strings.TrimSpace(apiKey) == "" {
		return in, errors.New("--api-key (or --api-key-stdin) is required in --non-interactive mode")
	}
	in.APIKey = strings.TrimSpace(apiKey)

	if f.username == "" {
		return in, errors.New("--admin-username is required in --non-interactive mode")
	}
	in.Username = strings.TrimSpace(f.username)

	if password == "" {
		return in, errors.New("--admin-password (or --admin-password-stdin) is required in --non-interactive mode")
	}
	if len(password) < 8 {
		return in, errors.New("admin password must be at least 8 characters")
	}
	in.Password = password

	in.Model = strings.TrimSpace(f.model)
	if in.Model == "" {
		in.Model = defaultModelFor(in.ProviderID)
	}

	return in, nil
}

// RunHeadless writes the headless onboarding state from a pre-validated Input.
// It is the non-interactive counterpart of Run.
func RunHeadless(home string, wio wizardIO, in Input) error {
	if err := datamodel.Init(home); err != nil {
		return fmt.Errorf("init home: %w", err)
	}
	mgr := onboarding.NewManager(home)
	if mgr.IsComplete() {
		fmt.Fprintln(wio.stdout, "Onboarding is already complete; nothing to do.")
		fmt.Fprintln(wio.stdout,
			"To re-run, delete ~/.omnipus/system/state.json"+
				" (or the equivalent under your OMNIPUS_HOME).",
		)
		return nil
	}

	in.Home = home
	if err := applyInput(in, wio); err != nil {
		return err
	}

	fmt.Fprintln(wio.stdout, "")
	fmt.Fprintln(wio.stdout, "Onboarding complete.")
	fmt.Fprintf(wio.stdout, "Log in as %q with the password you just entered.\n", in.Username)
	printAccessURLBlock(wio.stdout, home)
	return nil
}

// Run executes the wizard against the supplied IO and home directory. It is
// exported only so the regression tests can drive it without going through
// Cobra's command-execution machinery.
func Run(home string, wio wizardIO) error {
	if err := datamodel.Init(home); err != nil {
		return fmt.Errorf("init home: %w", err)
	}
	mgr := onboarding.NewManager(home)
	if mgr.IsComplete() {
		fmt.Fprintln(wio.stdout, "Onboarding is already complete; nothing to do.")
		fmt.Fprintln(wio.stdout,
			"To re-run, delete ~/.omnipus/system/state.json"+
				" (or the equivalent under your OMNIPUS_HOME).",
		)
		return nil
	}

	// Interactive path: prompt collects the raw answers; then the validation loop
	// may re-prompt for the API key when the live probe returns InvalidKey.
	in, err := promptWithValidation(wio)
	if err != nil {
		return err
	}
	in.Home = home

	if err := applyInput(in, wio); err != nil {
		return err
	}

	fmt.Fprintln(wio.stdout, "")
	fmt.Fprintln(wio.stdout, "Onboarding complete.")
	fmt.Fprintf(wio.stdout, "Log in as %q with the password you just entered.\n", in.Username)
	printAccessURLBlock(wio.stdout, home)
	return nil
}

// printAccessURLBlock reads the freshly-written config to obtain host/port/
// public_url and prints the bind-aware URL block plus a next-step hint.
// Errors reading config are silently swallowed — the URL block is informational
// and must never cause onboarding to fail.
func printAccessURLBlock(out io.Writer, home string) {
	configPath := filepath.Join(home, "config.json")
	urls := netinfo.AccessURLsFromConfig(configPath)
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintln(out, "  1. Run: omnipus start")
	fmt.Fprintln(out, "  2. Open the dashboard:")
	fmt.Fprint(out, netinfo.Render(urls))
}

// promptWithValidation calls prompt then carries wio.skipVerify into the result.
// The interactive key-validation re-prompt loop is handled in applyInput, which
// has access to wio for re-reading the key. promptWithValidation is therefore a
// thin wrapper that transfers the flag from wizardIO into the returned Input.
func promptWithValidation(wio wizardIO) (Input, error) {
	in, err := prompt(wio)
	if err != nil {
		return in, err
	}
	in.SkipVerify = wio.skipVerify
	return in, nil
}

// providerDisplayName returns the display name for a provider id, used in the
// FR-7 validation messages. The curated menu label wins so the wizard's own
// wording stays stable; anything else comes from the catalog, and an id the
// catalog does not carry is echoed verbatim (ADR-067 A-14) — never
// title-cased into a brand Omnipus does not know.
func providerDisplayName(providerID string) string {
	for _, item := range providerMenu {
		if item.providerID != "" && item.providerID == providerID {
			return item.label
		}
	}
	return providers.DisplayName(providerID)
}

// validateAndResolveKey validates in.APIKey against the provider at baseURL before
// it is persisted, implementing the FR-014/FR-015 policy. The caller resolves baseURL
// via providers.APIBaseFor and passes it in, which keeps this function a pure,
// httptest-injectable seam (the tests exercise it directly — there is no mirror).
//
//   - in.SkipVerify == true: no probe; emit R-F slog audit line; return in.APIKey.
//   - Outcome == Valid: proceed silently; return in.APIKey.
//   - Outcome == InvalidKey + in.NonInteractive: print message to wio.stderr; return error.
//   - Outcome == InvalidKey + interactive: print message; re-prompt for key; loop.
//   - Outcome == NoCredit/Unreachable/Restricted: print warning to wio.stdout; emit R-F
//     audit slog line; return in.APIKey.
//
// It returns the resolved key (which may differ from in.APIKey after an interactive
// re-prompt). The caller must NOT persist anything if this returns a non-nil error.
func validateAndResolveKey(ctx context.Context, in Input, wio wizardIO, baseURL string) (string, error) {
	if in.SkipVerify {
		// R-F best-effort audit: emit structured slog line; never fail onboard.
		slog.Info("provider_key_validation_skipped",
			"provider", in.ProviderID,
			"source", "cli",
			"flag", "--skip-verify",
		)
		return in.APIKey, nil
	}

	displayName := providerDisplayName(in.ProviderID)

	apiKey := in.APIKey
	for {
		result := providers.ValidateKey(ctx, providers.ValidateInput{
			ProviderID:   in.ProviderID,
			ProviderName: displayName,
			BaseURL:      baseURL,
			APIKey:       apiKey,
			Catalog:      nil,
		}, providers.NoopChecker{}) // CLI: explicit no-SSRF-guard (operator-run — R-A/spec)

		// SEC-16: RawDetail for debug log only, never to the user.
		slog.Debug("providers: CLI key probe result",
			"provider", in.ProviderID,
			"outcome", result.Outcome,
			"raw_detail", result.RawDetail,
		)

		if result.Blocks() {
			// Outcome is InvalidKey.
			if in.NonInteractive {
				fmt.Fprintln(wio.stderr, result.Message)
				return "", fmt.Errorf("provider key rejected: %s", result.Message)
			}
			// Interactive: print the error and re-prompt for the key.
			fmt.Fprintf(wio.stdout, "\n%s\n", result.Message)
			fmt.Fprintf(wio.stdout, "%s API key (input hidden): ", in.ProviderID)
			newKey, err := wio.readPassword()
			if err != nil {
				return "", fmt.Errorf("read api key: %w", err)
			}
			fmt.Fprintln(wio.stdout, "")
			newKey = strings.TrimSpace(newKey)
			if newKey == "" {
				return "", errors.New("api key must not be empty")
			}
			apiKey = newKey
			continue
		}

		// Non-blocking outcomes: warn if not Valid, then proceed.
		if result.Outcome != providers.OutcomeValid {
			fmt.Fprintf(wio.stdout, "Warning: %s\n", result.Message)
			// R-F best-effort audit slog line for warning-proceed.
			slog.Info("provider_key_validated",
				"provider", in.ProviderID,
				"outcome", string(result.Outcome),
				"action", "proceeded",
			)
		}
		return apiKey, nil
	}
}

// prompt walks the user through the wizard and returns the validated answers.
// Returns an error if input is malformed, empty where required, or the
// password fails length validation.
//
// The provider step now presents a numbered menu (FR-010/US-8): the user types
// a digit (1–N); an out-of-range entry re-prompts without crashing; choosing
// "Other" prompts for a raw provider id validated against the embedded
// catalog snapshot via providers.Admit (ADR-067 A-21).
func prompt(wio wizardIO) (Input, error) {
	reader := bufio.NewReader(wio.stdin)
	in := Input{}

	fmt.Fprintln(wio.stdout, "Omnipus first-run setup")
	fmt.Fprintln(wio.stdout, "=======================")
	fmt.Fprintln(wio.stdout, "")

	// Provider — numbered menu with re-prompt on invalid input.
	providerID, err := promptProviderMenu(wio.stdout, reader)
	if err != nil {
		return in, err
	}
	in.ProviderID = providerID

	// API key (hidden input).
	fmt.Fprintf(wio.stdout, "%s API key (input hidden): ", providerID)
	apiKey, err := wio.readPassword()
	if err != nil {
		return in, fmt.Errorf("read api key: %w", err)
	}
	fmt.Fprintln(wio.stdout, "")
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return in, errors.New("api key must not be empty")
	}
	in.APIKey = apiKey

	// Model (default per provider).
	defaultModel := defaultModelFor(providerID)
	model, err := readWithDefault(wio.stdout, reader, "Model", defaultModel)
	if err != nil {
		return in, err
	}
	in.Model = model

	// Admin user.
	username, err := readNonEmpty(wio.stdout, reader, "Admin username: ", "")
	if err != nil {
		return in, err
	}
	in.Username = username

	fmt.Fprint(wio.stdout, "Admin password (min 8 chars, input hidden): ")
	password, err := wio.readPassword()
	if err != nil {
		return in, fmt.Errorf("read password: %w", err)
	}
	fmt.Fprintln(wio.stdout, "")
	if len(password) < 8 {
		return in, errors.New("password must be at least 8 characters")
	}
	in.Password = password

	return in, nil
}

// promptProviderMenu prints the numbered menu and reads the user's selection,
// re-prompting on out-of-range or blank input. Returns the selected protocol id.
// Selecting "Other" prompts for a raw provider id and admits it against
// the embedded catalog snapshot (providers.Admit).
func promptProviderMenu(out io.Writer, reader *bufio.Reader) (string, error) {
	for {
		fmt.Fprintln(out, "Select your LLM provider:")
		for i, item := range providerMenu {
			fmt.Fprintf(out, "  %d) %s\n", i+1, item.label)
		}
		fmt.Fprint(out, "Choice [1]: ")

		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read provider choice: %w", err)
		}
		line = strings.TrimSpace(line)

		// Default to 1 on empty input.
		if line == "" {
			line = "1"
		}

		n, parseErr := strconv.Atoi(line)
		if parseErr != nil || n < 1 || n > len(providerMenu) {
			fmt.Fprintf(out, "Please enter a number between 1 and %d.\n\n", len(providerMenu))
			continue
		}

		chosen := providerMenu[n-1]

		// "Other" entry: prompt for raw protocol id.
		if chosen.providerID == "" {
			rawID, readErr := readNonEmpty(out, reader, "Provider protocol id (e.g. ollama, litellm): ", "")
			if readErr != nil {
				return "", readErr
			}
			// Trim only — ids are exact and case-significant (A-19), so
			// lowercasing here would quietly "fix" a typo into a different
			// provider than the one the operator typed.
			rawID = strings.TrimSpace(rawID)
			// Same admission gate as --non-interactive (ADR-067 FR-019/FR-035).
			if _, admitErr := providers.Admit(rawID, "", ""); admitErr != nil {
				return "", admitErr
			}
			return rawID, nil
		}

		return chosen.providerID, nil
	}
}

func readNonEmpty(out io.Writer, r *bufio.Reader, promptStr, defaultVal string) (string, error) {
	fmt.Fprint(out, promptStr)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		if defaultVal != "" {
			return defaultVal, nil
		}
		return "", fmt.Errorf("input must not be empty")
	}
	return line, nil
}

func readWithDefault(out io.Writer, r *bufio.Reader, label, defaultVal string) (string, error) {
	fmt.Fprintf(out, "%s [%s]: ", label, defaultVal)
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultVal, nil
	}
	return line, nil
}

// applyInput performs the side-effecting writes: credentials.json,
// config.json, cli.token, and state.json. It is split out so the regression
// test can invoke it with a pre-built Input without going through stdin parsing.
//
// When in.SkipVerify is false the function validates the API key against the
// live provider before persisting it (FR-014/FR-015):
//   - InvalidKey + non-interactive → print to stderr, return error, persist nothing.
//   - InvalidKey + interactive → print message, re-prompt for the key, repeat.
//   - NoCredit/Unreachable/Restricted → print warning to stdout, proceed.
//   - Valid → proceed silently.
//
// When in.SkipVerify is true the probe is skipped, the key is persisted, and a
// structured slog audit line is emitted (R-F best-effort).
func applyInput(in Input, wio wizardIO) error {
	// 1. Unlock credentials store (auto-generates master.key on a fresh install).
	credPath := filepath.Join(in.Home, "credentials.json")
	store := credentials.NewStore(credPath)
	if err := credentials.Unlock(store); err != nil {
		return fmt.Errorf("unlock credentials store: %w", err)
	}

	// 2. Validate the API key before persisting (FR-014/FR-015).
	//    resolvedKey may differ from in.APIKey when the interactive loop re-prompts.
	//    vErr is named distinctly so the idiomatic `if err :=` blocks below do not
	//    shadow a lingering function-scoped err (govet shadow).
	resolvedKey, vErr := validateAndResolveKey(
		context.Background(), in, wio, providers.APIBaseFor(in.ProviderID))
	if vErr != nil {
		return vErr
	}
	in.APIKey = resolvedKey

	// 3. Store API key under a deterministic name (e.g. openai_api_key).
	credRef := strings.ToLower(in.ProviderID) + "_api_key"
	if err := store.Set(credRef, in.APIKey); err != nil {
		return fmt.Errorf("save api key: %w", err)
	}

	// 4. Pre-compute bcrypt hashes + bearer token outside any locks.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	token, err := clitoken.GenerateBearerToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash token: %w", err)
	}

	// 5. Mutate config.json. datamodel.Init has already written a minimal
	// default config if none existed, so the file exists at this point.
	configPath := filepath.Join(in.Home, "config.json")
	if err := mutateConfigFile(configPath, in, credRef, string(passwordHash), string(tokenHash)); err != nil {
		return fmt.Errorf("update config.json: %w", err)
	}

	// 6. Mint the cli principal and token file (create-if-absent).
	// EnsureCLIToken reads config.json which was just written above.
	if _, err := clitoken.EnsureCLIToken(in.Home); err != nil {
		return fmt.Errorf("mint cli token: %w", err)
	}

	// 7. Mark onboarding complete. NewManager reads state.json fresh — the
	// previous IsComplete() check inside Run() may have used a stale view if
	// state.json was just written by datamodel.Init, but it correctly
	// reports OnboardingComplete=false from the seeded state.
	mgr := onboarding.NewManager(in.Home)
	if err := mgr.CompleteOnboarding(); err != nil {
		return fmt.Errorf("mark onboarding complete: %w", err)
	}

	fmt.Fprintln(wio.stdout, "")
	fmt.Fprintf(wio.stdout, "Wrote provider %q (model %q) to %s\n", in.ProviderID, in.Model, configPath)
	fmt.Fprintf(wio.stdout, "Encrypted API key under credential ref %q in %s\n", credRef, credPath)
	fmt.Fprintf(wio.stdout, "Bearer token for %q (save this — it is only shown once):\n  %s\n", in.Username, token)
	return nil
}

func mutateConfigFile(path string, in Input, credRef, passwordHash, tokenHash string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	var m map[string]any
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return fmt.Errorf("parse: %w", unmarshalErr)
	}

	// --- Providers ---
	providerList, _ := m["providers"].([]any)
	if providerList == nil {
		providerList = []any{}
	}
	newEntry := map[string]any{
		"model_name":  in.Model,
		"provider":    in.ProviderID,
		"model":       in.Model,
		"api_key_ref": credRef,
	}
	found := false
	for i, entry := range providerList {
		em, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if em["provider"] == in.ProviderID && em["model"] == in.Model {
			em["api_key_ref"] = credRef
			delete(em, "api_key")
			delete(em, "api_keys")
			em["model_name"] = in.Model
			providerList[i] = em
			found = true
			break
		}
	}
	if !found {
		providerList = append(providerList, newEntry)
	}
	m["providers"] = providerList

	// --- Default model ---
	agentsMap, _ := m["agents"].(map[string]any)
	if agentsMap == nil {
		agentsMap = map[string]any{}
	}
	defaultsMap, _ := agentsMap["defaults"].(map[string]any)
	if defaultsMap == nil {
		defaultsMap = map[string]any{}
	}
	// ADR-068 D14.1: the default model is the exact (provider, model) pair.
	delete(defaultsMap, "model_name")
	defaultsMap["default_model"] = map[string]any{
		"provider": in.ProviderID,
		"model":    in.Model,
	}
	agentsMap["defaults"] = defaultsMap
	m["agents"] = agentsMap

	// --- Admin user ---
	gatewayMap, _ := m["gateway"].(map[string]any)
	if gatewayMap == nil {
		gatewayMap = map[string]any{}
	}
	users, _ := gatewayMap["users"].([]any)
	if users == nil {
		users = []any{}
	}
	updated := false
	for i, u := range users {
		um, ok := u.(map[string]any)
		if !ok {
			continue
		}
		if um["username"] == in.Username {
			um["password_hash"] = passwordHash
			um["token_hash"] = tokenHash
			users[i] = um
			updated = true
			break
		}
	}
	if !updated {
		users = append(users, map[string]any{
			"username":      in.Username,
			"password_hash": passwordHash,
			"token_hash":    tokenHash,
		})
	}
	gatewayMap["users"] = users
	m["gateway"] = gatewayMap

	// Serialize + atomic write.
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("serialize: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, out, 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}
