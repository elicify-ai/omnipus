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
//  5. marks onboarding complete in system/state.json.
//
// After this runs, `omnipus start` boots without requiring dev_mode_bypass
// or env-injected secrets — the admin can log in immediately with the
// username/password they entered.
package onboard

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/datamodel"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/onboarding"
	"github.com/dapicom-ai/omnipus/pkg/providers"
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
}

// defaultModelFor mirrors the per-provider model defaults the REST handler
// applies when the user doesn't specify a model.
func defaultModelFor(providerID string) string {
	switch providerID {
	case "anthropic":
		return "claude-sonnet-4-6"
	case "gemini", "google":
		return "gemini-2.0-flash"
	case "openrouter":
		return "openai/gpt-4o"
	default:
		return "gpt-4o"
	}
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
	)
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "First-run setup (provider, API key, admin user) — interactive or headless",
		Long: `Configure a fresh Omnipus install.

Two modes:

  Interactive (default)
    Prompts for an LLM provider, API key, default model, and admin
    username/password.

  Headless
    Pass --non-interactive together with all of --provider, --api-key
    (or --api-key-stdin), --admin-username, and --admin-password (or
    --admin-password-stdin). --model is optional and defaults to a
    sensible per-provider model.

In both modes the API key is encrypted into credentials.json (creating
master.key on first run); the admin user and provider entry are written
to config.json; system/state.json is marked complete.

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
				return RunHeadless(home, defaultIO(cmd), in)
			}
			return Run(home, defaultIO(cmd))
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
	if !providers.IsKnownProtocol(f.providerID) {
		return in, fmt.Errorf("provider %q is not a known protocol", f.providerID)
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
func RunHeadless(home string, io wizardIO, in Input) error {
	if err := datamodel.Init(home); err != nil {
		return fmt.Errorf("init home: %w", err)
	}
	mgr := onboarding.NewManager(home)
	if mgr.IsComplete() {
		fmt.Fprintln(io.stdout, "Onboarding is already complete; nothing to do.")
		fmt.Fprintln(io.stdout,
			"To re-run, delete ~/.omnipus/system/state.json"+
				" (or the equivalent under your OMNIPUS_HOME).",
		)
		return nil
	}

	in.Home = home
	if err := applyInput(in, io); err != nil {
		return err
	}

	fmt.Fprintln(io.stdout, "")
	fmt.Fprintln(io.stdout, "Onboarding complete.")
	fmt.Fprintf(io.stdout, "Run `omnipus start` to serve the SPA + API on the configured port.\n")
	fmt.Fprintf(io.stdout, "Log in as %q with the password you just entered.\n", in.Username)
	return nil
}

// Run executes the wizard against the supplied IO and home directory. It is
// exported only so the regression tests can drive it without going through
// Cobra's command-execution machinery.
func Run(home string, io wizardIO) error {
	if err := datamodel.Init(home); err != nil {
		return fmt.Errorf("init home: %w", err)
	}
	mgr := onboarding.NewManager(home)
	if mgr.IsComplete() {
		fmt.Fprintln(io.stdout, "Onboarding is already complete; nothing to do.")
		fmt.Fprintln(io.stdout,
			"To re-run, delete ~/.omnipus/system/state.json"+
				" (or the equivalent under your OMNIPUS_HOME).",
		)
		return nil
	}

	in, err := prompt(io)
	if err != nil {
		return err
	}
	in.Home = home

	if err := applyInput(in, io); err != nil {
		return err
	}

	fmt.Fprintln(io.stdout, "")
	fmt.Fprintln(io.stdout, "Onboarding complete.")
	fmt.Fprintf(io.stdout, "Run `omnipus start` to serve the SPA + API on the configured port.\n")
	fmt.Fprintf(io.stdout, "Log in as %q with the password you just entered.\n", in.Username)
	return nil
}

// prompt walks the user through the wizard and returns the validated answers.
// Returns an error if input is malformed, empty where required, or the
// password fails length validation.
func prompt(io wizardIO) (Input, error) {
	reader := bufio.NewReader(io.stdin)
	in := Input{}

	fmt.Fprintln(io.stdout, "Omnipus first-run setup")
	fmt.Fprintln(io.stdout, "=======================")
	fmt.Fprintln(io.stdout, "")

	// Provider.
	providerHint := "openai, anthropic, openrouter, gemini, …"
	providerID, err := readNonEmpty(io.stdout, reader, "LLM provider ["+providerHint+"]: ", "openai")
	if err != nil {
		return in, err
	}
	if !providers.IsKnownProtocol(providerID) {
		return in, fmt.Errorf("provider %q is not a known protocol", providerID)
	}
	in.ProviderID = providerID

	// API key (hidden input).
	fmt.Fprintf(io.stdout, "%s API key (input hidden): ", providerID)
	apiKey, err := io.readPassword()
	if err != nil {
		return in, fmt.Errorf("read api key: %w", err)
	}
	fmt.Fprintln(io.stdout, "")
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return in, errors.New("api key must not be empty")
	}
	in.APIKey = apiKey

	// Model (default per provider).
	defaultModel := defaultModelFor(providerID)
	model, err := readWithDefault(io.stdout, reader, "Model", defaultModel)
	if err != nil {
		return in, err
	}
	in.Model = model

	// Admin user.
	username, err := readNonEmpty(io.stdout, reader, "Admin username: ", "")
	if err != nil {
		return in, err
	}
	in.Username = username

	fmt.Fprint(io.stdout, "Admin password (min 8 chars, input hidden): ")
	password, err := io.readPassword()
	if err != nil {
		return in, fmt.Errorf("read password: %w", err)
	}
	fmt.Fprintln(io.stdout, "")
	if len(password) < 8 {
		return in, errors.New("password must be at least 8 characters")
	}
	in.Password = password

	return in, nil
}

func readNonEmpty(out io.Writer, r *bufio.Reader, prompt, defaultVal string) (string, error) {
	fmt.Fprint(out, prompt)
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
// config.json, and state.json. It is split out so the regression test can
// invoke it with a pre-built Input without going through stdin parsing.
func applyInput(in Input, io wizardIO) error {
	// 1. Unlock credentials store (auto-generates master.key on a fresh install).
	credPath := filepath.Join(in.Home, "credentials.json")
	store := credentials.NewStore(credPath)
	if err := credentials.Unlock(store); err != nil {
		return fmt.Errorf("unlock credentials store: %w", err)
	}

	// 2. Store API key under a deterministic name (e.g. openai_api_key).
	credRef := strings.ToLower(in.ProviderID) + "_api_key"
	if err := store.Set(credRef, in.APIKey); err != nil {
		return fmt.Errorf("save api key: %w", err)
	}

	// 3. Pre-compute bcrypt hashes + bearer token outside any locks.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	token, err := generateBearerToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}
	tokenHash, err := bcrypt.GenerateFromPassword([]byte(token), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash token: %w", err)
	}

	// 4. Mutate config.json. datamodel.Init has already written a minimal
	// default config if none existed, so the file exists at this point.
	configPath := filepath.Join(in.Home, "config.json")
	if err := mutateConfigFile(configPath, in, credRef, string(passwordHash), string(tokenHash)); err != nil {
		return fmt.Errorf("update config.json: %w", err)
	}

	// 5. Mark onboarding complete. NewManager reads state.json fresh — the
	// previous IsComplete() check inside Run() may have used a stale view if
	// state.json was just written by datamodel.Init, but it correctly
	// reports OnboardingComplete=false from the seeded state.
	mgr := onboarding.NewManager(in.Home)
	if err := mgr.CompleteOnboarding(); err != nil {
		return fmt.Errorf("mark onboarding complete: %w", err)
	}

	fmt.Fprintln(io.stdout, "")
	fmt.Fprintf(io.stdout, "Wrote provider %q (model %q) to %s\n", in.ProviderID, in.Model, configPath)
	fmt.Fprintf(io.stdout, "Encrypted API key under credential ref %q in %s\n", credRef, credPath)
	fmt.Fprintf(io.stdout, "Bearer token for %q (save this — it is only shown once):\n  %s\n", in.Username, token)
	return nil
}

func generateBearerToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "omnipus_" + hex.EncodeToString(bytes), nil
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
	defaultsMap["model_name"] = in.Model
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
			um["role"] = "admin"
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
			"role":          "admin",
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
