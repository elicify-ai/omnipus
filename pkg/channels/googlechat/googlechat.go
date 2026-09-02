package googlechat

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/identity"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

const (
	googleChatAPIBase       = "https://chat.googleapis.com/v1"
	googleChatTokenURL      = "https://oauth2.googleapis.com/token"
	googleChatJWKSURL       = "https://www.googleapis.com/service_accounts/jwks"
	googleChatMaxBodySize   = 1 << 20 // 1 MiB — matches LINE webhook limit
	googleChatTokenExpiry   = 55 * time.Minute
	googleChatTypingRefresh = 25 * time.Second
	maxRetries              = 3
	maxJWKSAge              = 1 * time.Hour
)

var (
	ErrNotRunning = errors.New("channel not running")
	ErrSendFailed = errors.New("send failed")
	ErrAuthFailed = errors.New("authentication failed")
)

// GoogleChatClient is the interface for making Google Chat API calls.
// Exists for unit testability — real implementation uses net/http.
type GoogleChatClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// GoogleChatChannel implements the Channel interface for Google Chat.
type GoogleChatChannel struct {
	*channels.BaseChannel
	config        config.GoogleChatConfig
	mode          string // "webhook" | "bot"
	saKey         *rsa.PrivateKey
	saEmail       string
	saKeyID       string // kid for the SA key
	jwksCache     map[string]*rsa.PublicKey
	jwksMu        sync.RWMutex
	jwksLastFetch time.Time
	client        GoogleChatClient
	healthOK      atomic.Bool
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewGoogleChatChannel creates a new Google Chat channel.
//
// Secrets (webhook_url, service_account_json) are credential-store-routed
// (SEC-23 / #289): when the corresponding <field>_ref is set, the plaintext is
// resolved from the SecretBundle and never lives in config.json. For
// backward-compat / env-injected configs that still carry an inline
// SecureString, the inline value is used as a fallback when the ref is empty.
func NewGoogleChatChannel(
	cfg config.GoogleChatConfig,
	secrets credentials.SecretBundle,
	b *bus.MessageBus,
) (*GoogleChatChannel, error) {
	// Resolve secrets ref-first, inline-fallback. cfg is a value copy, so
	// mutating its SecureString fields here is local to this channel instance
	// and keeps every downstream read (sendWebhook, initBotAuth) unchanged.
	if cfg.WebhookURLRef != "" {
		cfg.WebhookURL.Set(secrets.GetString(cfg.WebhookURLRef))
	}
	if cfg.ServiceAccountJSONRef != "" {
		cfg.ServiceAccountJSON.Set(secrets.GetString(cfg.ServiceAccountJSONRef))
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" && cfg.WebhookURL.String() != "" {
		mode = "webhook"
	} else if mode == "" {
		mode = "bot"
	}

	if mode == "webhook" {
		if cfg.WebhookURL.String() == "" {
			return nil, fmt.Errorf("google-chat webhook mode requires webhook_url")
		}
	} else { // bot
		hasSAFile := cfg.ServiceAccountFile != ""
		hasSAJSON := cfg.ServiceAccountJSON.String() != ""
		if !hasSAFile && !hasSAJSON {
			return nil, fmt.Errorf("google-chat bot mode requires service_account_file or service_account_json")
		}
		if hasSAFile && hasSAJSON {
			logger.WarnC("google-chat", "Both service_account_file and service_account_json provided; using file")
		}
	}

	base := channels.NewBaseChannel("google-chat", cfg, b, cfg.AllowFrom,
		channels.WithGroupTrigger(cfg.GroupTrigger),
		channels.WithReasoningChannelID(cfg.ReasoningChannelID),
	)

	ch := &GoogleChatChannel{
		BaseChannel: base,
		config:      cfg,
		mode:        mode,
		client:      &http.Client{Timeout: 30 * time.Second},
		jwksCache:   make(map[string]*rsa.PublicKey),
	}
	base.SetOwner(ch)

	return ch, nil
}

// Start initializes the channel.
func (c *GoogleChatChannel) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	if c.mode == "bot" {
		if err := c.initBotAuth(); err != nil {
			logger.WarnCF("google-chat", "Bot auth init failed (webhook-only features available)", map[string]any{
				"error": err.Error(),
			})
		}
	}

	c.SetRunning(true)
	logger.InfoCF("google-chat", "Google Chat channel started", map[string]any{
		"mode": c.mode,
	})
	return nil
}

// Stop gracefully stops the channel.
func (c *GoogleChatChannel) Stop(ctx context.Context) error {
	logger.InfoC("google-chat", "Stopping Google Chat channel")
	if c.cancel != nil {
		c.cancel()
	}
	c.SetRunning(false)
	logger.InfoC("google-chat", "Google Chat channel stopped")
	return nil
}

// Send sends a message to Google Chat.
func (c *GoogleChatChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return ErrNotRunning
	}

	if c.mode == "webhook" {
		return c.sendWebhook(ctx, msg)
	}
	return c.sendBotMessage(ctx, msg)
}

// sendWebhook posts a message via incoming webhook (outbound only).
func (c *GoogleChatChannel) sendWebhook(ctx context.Context, msg bus.OutboundMessage) error {
	payload := map[string]any{
		"text": msg.Content,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.WebhookURL.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			respBody = []byte("(could not read response body)")
		}
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// spaceResourceName normalizes a Google Chat chat_id to the space resource
// name the Chat API expects ("spaces/<id>"), adding the "spaces/" prefix
// exactly once regardless of whether the caller already supplied it.
//
// processEvent (below) sets an INBOUND message's ChatID to event.Space.Name,
// which Google's own API always returns already prefixed ("spaces/AAAA") —
// so that is this channel's native chat_id format, and it is what turnChat/
// ToolChatID(ctx) hands back to an agent that inspects its current
// conversation. An agent forwarding that same value into send_message for a
// proactive/out-of-band message (the tool's own documented use case) was
// hitting a double-prefix bug: every outbound call site built its endpoint
// as fmt.Sprintf(".../spaces/%s/...", spaceID) unconditionally, so an
// already-prefixed spaceID produced ".../spaces/spaces/AAAA/..." — a
// malformed, always-404 resource name. Normalizing here, once, keeps every
// call site correct for BOTH an already-prefixed chat_id (the common case,
// since it is the value this channel itself hands out) and a bare space ID
// an agent might reasonably supply instead, matching how send_message
// already tolerates unprefixed/raw chat ids for every other channel.
func spaceResourceName(chatID string) string {
	if strings.HasPrefix(chatID, "spaces/") {
		return chatID
	}
	return "spaces/" + chatID
}

// sendBotMessage sends a message via Google Chat Bot API.
func (c *GoogleChatChannel) sendBotMessage(ctx context.Context, msg bus.OutboundMessage) error {
	token, err := c.getAccessToken(ctx)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	spaceID := spaceResourceName(msg.ChatID)
	endpoint := fmt.Sprintf("%s/%s/messages", googleChatAPIBase, spaceID)

	// Thread reply if ReplyToMessageID is set
	if msg.ReplyToMessageID != "" {
		endpoint = fmt.Sprintf("%s?messageThreadKey=%s", endpoint, msg.ReplyToMessageID)
	}

	payload := map[string]any{
		"text": msg.Content,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.callWithRetry(ctx, req)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		logger.WarnCF("google-chat", "Thread not found, sending as new message", map[string]any{
			"chat_id": msg.ChatID,
		})
		// Retry without thread key. spaceID is already normalized above.
		newEndpoint := fmt.Sprintf("%s/%s/messages", googleChatAPIBase, spaceID)
		parsedURL, err := url.Parse(newEndpoint)
		if err != nil {
			return fmt.Errorf("parse fallback URL: %w", err)
		}
		req.URL = parsedURL
		resp, err = c.callWithRetry(ctx, req)
		if err != nil {
			return fmt.Errorf("send (fallback): %w", err)
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			respBody = []byte("(could not read response body)")
		}
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// callWithRetry executes an HTTP request with retry and backoff per spec.
func (c *GoogleChatChannel) callWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	var lastErr error
	var lastStatusCode int
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := c.client.Do(req.WithContext(ctx))
		if err != nil {
			lastErr = err
			continue
		}

		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			resp.Body.Close()
			time.Sleep(parseRetryAfter(resp))
			lastStatusCode = resp.StatusCode
			continue
		case 500, 502, 503, 504:
			resp.Body.Close()
			time.Sleep(backoff(attempt))
			lastStatusCode = resp.StatusCode
			continue
		case 400, 401, 403, 404:
			return resp, nil
		default:
			return resp, nil
		}
	}

	// All retries exhausted — classify the failure
	if lastStatusCode == http.StatusTooManyRequests {
		return nil, channels.ErrRateLimit
	}
	return nil, fmt.Errorf("max retries exceeded: %w", lastErr)
}

// parseRetryAfter extracts Retry-After delay from response headers.
func parseRetryAfter(resp *http.Response) time.Duration {
	if v := resp.Header.Get("Retry-After"); v != "" {
		if seconds, err := time.ParseDuration(v + "s"); err == nil {
			return seconds
		}
	}
	return backoff(0)
}

// backoff computes exponential backoff with jitter.
func backoff(attempt int) time.Duration {
	base := time.Duration(attempt+1) * time.Second
	jitter := time.Duration(time.Now().UnixNano()%int64(time.Second)) / 2
	if jitter > 500*time.Millisecond {
		jitter = 500 * time.Millisecond
	}
	maxDuration := 30 * time.Second
	if base+jitter > maxDuration {
		return maxDuration
	}
	return base + jitter
}

// WebhookPath returns the webhook mount path.
func (c *GoogleChatChannel) WebhookPath() string { return "/webhook/google-chat" }

// ServeHTTP implements http.Handler for inbound bot mode events.
func (c *GoogleChatChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.webhookHandler(w, r)
}

// webhookHandler handles incoming Google Chat webhook events.
func (c *GoogleChatChannel) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, googleChatMaxBodySize+1))
	if err != nil {
		logger.ErrorCF("google-chat", "Failed to read request body", map[string]any{"error": err.Error()})
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > googleChatMaxBodySize {
		logger.WarnC("google-chat", "Webhook request body too large, rejected")
		http.Error(w, "Request entity too large", http.StatusRequestEntityTooLarge)
		return
	}

	sigHeader := r.Header.Get("Google-Signature")
	if sigHeader == "" {
		logger.WarnC("google-chat", "Missing google-signature header")
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if !c.verifySignature(body, sigHeader) {
		logger.WarnC("google-chat", "Invalid webhook signature")
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var event googleChatEvent
	if err := json.Unmarshal(body, &event); err != nil {
		logger.ErrorCF("google-chat", "Failed to parse webhook payload", map[string]any{"error": err.Error()})
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	go c.processEvent(event)
}

type googleChatEvent struct {
	Type      string          `json:"type"`
	Token     string          `json:"token,omitempty"`
	Space     googleChatSpace `json:"space,omitempty"`
	Sender    googleChatUser  `json:"sender,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`
	EventTime string          `json:"eventTime,omitempty"`
}

type googleChatSpace struct {
	Name                        string `json:"name"`
	DisplayName                 string `json:"displayName,omitempty"`
	Type                        string `json:"type,omitempty"`
	SingleUserBotDirectMessages bool   `json:"singleUserBotDirectMessages,omitempty"`
}

type googleChatUser struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Email       string `json:"email,omitempty"`
	Type        string `json:"type,omitempty"`
}

type googleChatMessage struct {
	Name       string           `json:"name"`
	Text       string           `json:"text,omitempty"`
	Sender     googleChatUser   `json:"sender,omitempty"`
	Thread     googleChatThread `json:"thread,omitempty"`
	Annotation []any            `json:"annotation,omitempty"`
}

type googleChatThread struct {
	Name string `json:"name"`
}

// verifySignature verifies the google-signature header.
// The header format is "kid:base64signature". The signature is an
// RSA signature over the SHA-256 hash of the request body.
func (c *GoogleChatChannel) verifySignature(body []byte, sigHeader string) bool {
	parts := strings.SplitN(sigHeader, ":", 2)
	if len(parts) != 2 {
		return false
	}
	kid := parts[0]
	sigBytes, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}

	pubKey, err := c.getPublicKey(kid)
	if err != nil {
		logger.WarnCF("google-chat", "Failed to get public key for kid",
			map[string]any{"kid": kid, "error": err.Error()})
		return false
	}

	// Compute SHA-256 hash of body and verify RSA signature
	hash := sha256.Sum256(body)
	err = rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, hash[:], sigBytes)
	return err == nil
}

// getPublicKey fetches and caches RSA public keys from JWKS endpoint.
func (c *GoogleChatChannel) getPublicKey(kid string) (*rsa.PublicKey, error) {
	c.jwksMu.RLock()
	if pub, ok := c.jwksCache[kid]; ok {
		c.jwksMu.RUnlock()
		return pub, nil
	}
	c.jwksMu.RUnlock()

	// Refresh if stale
	if time.Since(c.jwksLastFetch) > maxJWKSAge {
		if err := c.refreshJWKS(); err != nil {
			return nil, err
		}
	}

	c.jwksMu.RLock()
	defer c.jwksMu.RUnlock()
	if pub, ok := c.jwksCache[kid]; ok {
		return pub, nil
	}
	return nil, fmt.Errorf("kid %s not found in JWKS", kid)
}

// refreshJWKS fetches the JWKS for the service account.
func (c *GoogleChatChannel) refreshJWKS() error {
	jwksURL := fmt.Sprintf("%s/%s/", googleChatJWKSURL, c.saEmail)
	req, err := http.NewRequest(http.MethodGet, jwksURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks returned %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("jwks decode: %w", err)
	}

	c.jwksMu.Lock()
	defer c.jwksMu.Unlock()
	for _, key := range jwks.Keys {
		if key.Kty != "RSA" {
			continue
		}
		nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil {
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil {
			continue
		}
		n := new(big.Int).SetBytes(nBytes)
		e := new(big.Int).SetBytes(eBytes)
		pub := &rsa.PublicKey{N: n, E: int(e.Int64())}
		c.jwksCache[key.Kid] = pub
	}
	c.jwksLastFetch = time.Now()
	return nil
}

// processEvent processes an inbound Google Chat event.
func (c *GoogleChatChannel) processEvent(event googleChatEvent) {
	if event.Type != "MESSAGE" {
		logger.DebugCF("google-chat", "Ignoring non-MESSAGE event", map[string]any{"type": event.Type})
		return
	}

	spaceName := event.Space.Name
	chatID := spaceName // "spaces/<id>"

	var msg googleChatMessage
	if err := json.Unmarshal(event.Message, &msg); err != nil {
		logger.ErrorCF("google-chat", "Failed to parse message", map[string]any{"error": err.Error()})
		return
	}

	content := msg.Text
	if content == "" {
		return
	}

	senderID := ""
	senderDisplayName := ""
	if event.Sender.Name != "" {
		senderID = event.Sender.Name
		senderDisplayName = event.Sender.DisplayName
	} else if msg.Sender.Name != "" {
		senderID = msg.Sender.Name
		senderDisplayName = msg.Sender.DisplayName
	}

	isGroup := event.Space.Type == "ROOM"
	metadata := map[string]string{
		"platform": "google-chat",
	}

	var threadKey string
	if msg.Thread.Name != "" {
		threadKey = msg.Thread.Name
		metadata["threadKey"] = threadKey
	}

	// Group trigger check
	if isGroup {
		botMentioned := strings.Contains(content, "@"+c.config.BotUser) ||
			strings.Contains(content, "OmnipusBot")
		respond, cleaned := c.ShouldRespondInGroup(botMentioned, content)
		if !respond {
			logger.DebugCF("google-chat", "Ignoring group message by group trigger", map[string]any{"chat_id": chatID})
			return
		}
		content = cleaned
	}

	var peer bus.Peer
	if isGroup {
		peer = bus.Peer{Kind: bus.PeerGroup, ID: chatID}
	} else {
		peer = bus.Peer{Kind: bus.PeerDirect, ID: senderID}
	}

	sender := bus.SenderInfo{
		Platform:    "google-chat",
		PlatformID:  senderID,
		CanonicalID: identity.BuildCanonicalID("google-chat", senderID),
		Username:    senderID,
		DisplayName: senderDisplayName,
	}

	if !c.IsAllowedSender(sender) {
		return
	}

	logger.DebugCF("google-chat", "Received message", map[string]any{
		"sender_id": senderID,
		"chat_id":   chatID,
		"preview":   strings.TrimSpace(strings.ReplaceAll(content, "\n", " "))[:50],
	})

	c.HandleMessage(c.ctx, peer, msg.Name, senderID, chatID, content, nil, metadata, sender)
}

// initBotAuth initializes bot mode authentication (JWT signing, token fetch).
func (c *GoogleChatChannel) initBotAuth() error {
	var saJSON string
	if c.config.ServiceAccountFile != "" {
		data, err := os.ReadFile(c.config.ServiceAccountFile)
		if err != nil {
			return fmt.Errorf("read service account file: %w", err)
		}
		saJSON = string(data)
	} else {
		saJSON = c.config.ServiceAccountJSON.String()
	}

	var sa struct {
		ClientEmail  string `json:"client_email"`
		PrivateKey   string `json:"private_key"`
		TokenURI     string `json:"token_uri"`
		PrivateKeyID string `json:"private_key_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(saJSON), &sa); err != nil {
		return fmt.Errorf("parse service account JSON: %w", err)
	}

	priv, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(sa.PrivateKey))
	if err != nil {
		return fmt.Errorf("parse RSA private key: %w", err)
	}

	c.saKey = priv
	c.saEmail = sa.ClientEmail
	if sa.PrivateKeyID != "" {
		c.saKeyID = sa.PrivateKeyID
	}
	c.healthOK.Store(true)
	return nil
}

// getAccessToken returns a valid OAuth2 access token, refreshing if needed.
func (c *GoogleChatChannel) getAccessToken(ctx context.Context) (string, error) {
	if c.saKey == nil {
		return "", ErrAuthFailed
	}

	// Generate JWT
	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   c.saEmail,
		"sub":   c.saEmail,
		"aud":   googleChatTokenURL,
		"iat":   now.Unix(),
		"exp":   now.Add(googleChatTokenExpiry).Unix(),
		"scope": "https://www.googleapis.com/auth/chat.bot",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = c.saKeyID
	signed, err := token.SignedString(c.saKey)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	// Exchange for access token
	payload := fmt.Sprintf("grant_type=urn%%3Aietf%%3Aparams%%3Aoauth%%3Agrant-type%%3Ajwt-bearer&assertion=%s",
		base64.URLEncoding.EncodeToString([]byte(signed)))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleChatTokenURL, strings.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, err := io.ReadAll(resp.Body)
		if err != nil {
			respBody = []byte("(could not read response body)")
		}
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	return tokenResp.AccessToken, nil
}

// StartTyping implements TypingCapable.
func (c *GoogleChatChannel) StartTyping(ctx context.Context, chatID string) (func(), error) {
	if !c.IsRunning() || c.mode != "bot" {
		return func() {}, nil
	}
	if chatID == "" {
		return func() {}, nil
	}

	token, err := c.getAccessToken(ctx)
	if err != nil {
		return func() {}, err
	}

	endpoint := fmt.Sprintf("%s/%s/presence:startTyping", googleChatAPIBase, spaceResourceName(chatID))
	payload := map[string]any{}
	body, err := json.Marshal(payload)
	if err != nil {
		return func() {}, fmt.Errorf("marshal typing payload: %w", err)
	}

	// Fire immediately
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return func() {}, fmt.Errorf("create typing request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return func() {}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return func() {}, fmt.Errorf("typing start returned %d", resp.StatusCode)
	}

	typingCtx, cancel := context.WithCancel(ctx)
	var once sync.Once
	stop := func() { once.Do(cancel) }

	ticker := time.NewTicker(googleChatTypingRefresh)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-typingCtx.Done():
				return
			case <-ticker.C:
				req2, err := http.NewRequestWithContext(typingCtx, http.MethodPost, endpoint, bytes.NewReader(body))
				if err != nil {
					logger.WarnCF("google-chat", "Failed to create typing refresh request",
						map[string]any{"error": err.Error()})
					continue
				}
				req2.Header.Set("Authorization", "Bearer "+token)
				req2.Header.Set("Content-Type", "application/json")
				resp2, err := c.client.Do(req2)
				if err != nil {
					logger.WarnCF("google-chat", "Failed to refresh typing indicator",
						map[string]any{"error": err.Error()})
					continue
				}
				resp2.Body.Close()
				if resp2.StatusCode >= 400 {
					logger.WarnCF("google-chat", "Typing refresh rejected", map[string]any{"status": resp2.StatusCode})
				}
			}
		}
	}()

	return stop, nil
}

// HealthPath returns the health check path.
func (c *GoogleChatChannel) HealthPath() string { return "/webhook/google-chat/health" }

// HealthHandler handles health check requests.
func (c *GoogleChatChannel) HealthHandler(w http.ResponseWriter, r *http.Request) {
	baseOK := c.BaseChannel == nil || c.IsRunning()
	if baseOK && c.healthOK.Load() {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok","channel":"google-chat"}`))
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"unavailable","channel":"google-chat"}`))
	}
}
