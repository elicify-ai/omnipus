package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/dapicom-ai/omnipus/cmd/omnipus/internal"
	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/channels/weixin"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/credentials"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
)

func newWeixinCommand() *cobra.Command {
	var baseURL string
	var proxy string
	var timeout int

	cmd := &cobra.Command{
		Use:   "weixin",
		Short: "Connect a WeChat personal account via QR code",
		Long: `Start the interactive Weixin (WeChat personal) QR code login flow.

A QR code is displayed in the terminal. Scan it with the WeChat mobile app
to authorize your account. On success, the bot token is saved to the omnipus
config so you can start the gateway immediately.

Example:
  omnipus auth weixin`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWeixinOnboard(baseURL, proxy, time.Duration(timeout)*time.Second)
		},
	}

	cmd.Flags().StringVar(&baseURL, "base-url", "https://ilinkai.weixin.qq.com/", "iLink API base URL")
	cmd.Flags().StringVar(&proxy, "proxy", "", "HTTP proxy URL (e.g. http://localhost:7890)")
	cmd.Flags().IntVar(&timeout, "timeout", 300, "Login timeout in seconds")

	return cmd
}

func runWeixinOnboard(baseURL, proxy string, timeout time.Duration) error {
	fmt.Println("Starting Weixin (WeChat personal) login...")
	fmt.Println()

	botToken, userID, accountID, returnedBaseURL, err := weixin.PerformLoginInteractive(
		context.Background(),
		weixin.AuthFlowOpts{
			BaseURL: baseURL,
			Timeout: timeout,
			Proxy:   proxy,
		},
	)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	fmt.Println()
	fmt.Println("✅ Login successful!")
	fmt.Printf("   Account ID : %s\n", accountID)
	if userID != "" {
		fmt.Printf("   User ID    : %s\n", userID)
	}
	fmt.Println()

	// Prefer the server-returned base URL (may be region-specific)
	effectiveBaseURL := returnedBaseURL
	if effectiveBaseURL == "" {
		effectiveBaseURL = baseURL
	}

	if err := saveWeixinConfig(botToken, effectiveBaseURL, proxy); err != nil {
		fmt.Printf("⚠️  Could not auto-save to config: %v\n", err)
		printManualWeixinConfig(botToken, effectiveBaseURL)
		return nil
	}

	fmt.Println("✓ Config updated. Start the gateway with:")
	fmt.Println()
	fmt.Println("  omnipus gateway")
	fmt.Println()
	fmt.Println("To restrict which WeChat users can send messages, add their user IDs")
	fmt.Println("to channels.weixin.allow_from in your config.")

	return nil
}

// weixinTokenCredRef is the credential-store key used for the Weixin bot
// token. Currently a package constant because WeixinConfig is singular
// in the config schema — only one Weixin bot per gateway. If the schema
// ever becomes multi-bot (ChannelsConfig.Weixin []WeixinConfig), this
// should become a function of the BotID: "WEIXIN_TOKEN_" + botID.
const weixinTokenCredRef = "WEIXIN_TOKEN"

// saveWeixinConfig patches channels.weixin in the config and saves the token
// to the encrypted credential store.
func saveWeixinConfig(token, baseURL, proxy string) error {
	cfgPath := internal.GetConfigPath()

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Persist the token in the encrypted credential store.
	home := internal.GetOmnipusHome()
	storePath := home + "/credentials.json"
	store := credentials.NewStore(storePath)
	if err := credentials.Unlock(store); err != nil {
		return fmt.Errorf("failed to unlock credential store: %w", err)
	}

	cfg.Channels.Weixin.Enabled = true
	cfg.Channels.Weixin.TokenRef = weixinTokenCredRef
	const defaultBase = "https://ilinkai.weixin.qq.com/"
	if baseURL != "" && baseURL != defaultBase {
		cfg.Channels.Weixin.BaseURL = baseURL
	}
	if proxy != "" {
		cfg.Channels.Weixin.Proxy = proxy
	}

	// Serialize concurrent auth runs via an advisory flock on the credentials
	// file so that two simultaneous "omnipus auth weixin" processes cannot race
	// on the same hardcoded ref and delete each other's token.
	lockPath := home + "/.credentials.lock"
	var saveErr error
	flockErr := fileutil.WithFlock(lockPath, func() error {
		// Step 1: write token to store first.  If this fails, config is never
		// touched and the user is not left with a broken ref.
		if err := store.Set(weixinTokenCredRef, token); err != nil {
			return fmt.Errorf("failed to save Weixin token to credential store: %w", err)
		}

		// Step 2: persist config with the TokenRef pointing at the stored token.
		// If SaveConfig fails, roll back by deleting the token we just wrote.
		if err := config.SaveConfig(cfgPath, cfg); err != nil {
			if delErr := store.Delete(weixinTokenCredRef); delErr != nil {
				logger.SlogError("weixin auth: rollback failed — orphaned credential requires manual cleanup",
					"ref", weixinTokenCredRef,
					"delete_error", delErr,
					"hint", "run: omnipus credentials delete "+weixinTokenCredRef,
				)
			}
			saveErr = fmt.Errorf("failed to save config: %w", err)
			return saveErr
		}
		return nil
	})
	if flockErr != nil {
		return flockErr
	}
	return saveErr
}

func printManualWeixinConfig(token, baseURL string) {
	fmt.Println()
	fmt.Println("Add the following to the channels section of your omnipus config:")
	fmt.Println()
	fmt.Println(`  "weixin": {`)
	fmt.Println(`    "enabled": true,`)
	fmt.Printf("    \"token\": %q,\n", token)
	const defaultBase = "https://ilinkai.weixin.qq.com/"
	if baseURL != "" && baseURL != defaultBase {
		fmt.Printf("    \"base_url\": %q,\n", baseURL)
	}
	fmt.Println(`    "allow_from": []`)
	fmt.Println(`  }`)
}
