package pkgcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/app/internal"
	"github.com/elicify-ai/omnipus/pkg/app/internal/clitoken"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// gatewayClient is the CLI's REST client for the package routes. It
// authenticates with the cli.token bearer and runs the shared auto-start
// step at most once per command, followed by one retry (FR-054).
type gatewayClient struct {
	base      string
	token     string
	autoStart func() error
	started   bool
	http      *http.Client
}

// apiError is a coded gateway refusal decoded from gen.ErrorResponse.
type apiError struct {
	status int
	body   gen.ErrorResponse
}

func (e *apiError) Error() string {
	code := e.code()
	switch {
	case e.body.Error != "" && code != "":
		return fmt.Sprintf("%s (code %s)", e.body.Error, code)
	case code != "":
		return fmt.Sprintf("the gateway refused the request (code %s)", code)
	case e.body.Error != "":
		return e.body.Error
	}
	return fmt.Sprintf("the gateway answered HTTP %d", e.status)
}

func (e *apiError) code() string {
	if e.body.Code == nil {
		return ""
	}
	return *e.body.Code
}

// detail returns details.<key> as a string ("" when absent).
func (e *apiError) detail(key string) string {
	if e.body.Details == nil {
		return ""
	}
	if s, ok := (*e.body.Details)[key].(string); ok {
		return s
	}
	return ""
}

// errUnreachable marks a gateway that did not accept a connection even after
// the auto-start step.
var errUnreachable = errors.New("the gateway is not reachable")

// newGatewayClient resolves the address (Options.GatewayURL, else the local
// gateway port) and the bearer (Options.Token, else cli.token in Home). It
// reads nothing else from the data folder (FR-055).
func newGatewayClient(opts Options) (*gatewayClient, error) {
	base := opts.GatewayURL
	if base == "" {
		addr, err := LocalGatewayAddr()
		if err != nil {
			return nil, err
		}
		base = "http://" + addr
	}
	token := opts.Token
	if token == "" {
		home := opts.Home
		if home == "" {
			home = internal.GetOmnipusHome()
		}
		var err error
		if token, err = clitoken.LoadCLIToken(home); err != nil {
			return nil, fmt.Errorf("no CLI key: %w", err)
		}
	}
	return &gatewayClient{
		base:      strings.TrimRight(base, "/"),
		token:     token,
		autoStart: opts.AutoStart,
		http:      &http.Client{},
	}, nil
}

// LocalGatewayAddr returns "localhost:<port>" for the local gateway: the
// OMNIPUS_GATEWAY_PORT override, else gateway.port in config.json, else the
// shipped default. It only reads config.json (never writes or migrates it,
// FR-055), which is why it does not go through config.LoadConfig.
func LocalGatewayAddr() (string, error) {
	port := config.DefaultConfig().Gateway.Port
	if env := os.Getenv("OMNIPUS_GATEWAY_PORT"); env != "" {
		p, err := strconv.Atoi(env)
		if err != nil {
			return "", fmt.Errorf("OMNIPUS_GATEWAY_PORT is not a number: %q", env)
		}
		return fmt.Sprintf("localhost:%d", p), nil
	}
	raw, err := os.ReadFile(internal.GetConfigPath())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("localhost:%d", port), nil
	case err != nil:
		return "", fmt.Errorf("read config.json: %w", err)
	}
	var cfg struct { // not-wire-format: reads only gateway.port from the local config.json file
		Gateway struct {
			Port int `json:"port"`
		} `json:"gateway"`
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return "", fmt.Errorf("read config.json: %w", err)
	}
	if cfg.Gateway.Port > 0 {
		port = cfg.Gateway.Port
	}
	return fmt.Sprintf("localhost:%d", port), nil
}

// isDialError reports a connection that was never established (gateway not
// running), as opposed to one lost after the request was sent.
func isDialError(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// send performs one request. On a refused connection it runs the shared
// auto-start step once per command and retries once; a gateway that stays
// unreachable yields errUnreachable. Any other transport error is returned
// as is (the caller decides what a lost connection means).
func (c *gatewayClient) send(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	resp, err := c.sendOnce(ctx, method, path, contentType, body)
	if err == nil || !isDialError(err) {
		return resp, err
	}
	if c.started || c.autoStart == nil {
		return nil, fmt.Errorf("%w at %s: %w", errUnreachable, c.base, err)
	}
	c.started = true
	if startErr := c.autoStart(); startErr != nil {
		return nil, fmt.Errorf("%w at %s; starting it failed: %w", errUnreachable, c.base, startErr)
	}
	resp, err = c.sendOnce(ctx, method, path, contentType, body)
	if err != nil && isDialError(err) {
		return nil, fmt.Errorf("%w at %s after starting it: %w", errUnreachable, c.base, err)
	}
	return resp, err
}

func (c *gatewayClient) sendOnce(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.http.Do(req)
}

// decodeAnswer checks the status and decodes a JSON success body into out,
// or returns the coded refusal as *apiError.
func decodeAnswer(resp *http.Response, okStatus int, out any) error {
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading the gateway answer: %w", err)
	}
	if resp.StatusCode != okStatus {
		ae := &apiError{status: resp.StatusCode}
		// A non-JSON error body leaves the generic HTTP-status message.
		_ = json.Unmarshal(raw, &ae.body)
		return ae
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("the gateway answer is not readable: %w", err)
	}
	return nil
}

// getJSON runs a GET and decodes a 200 body into out.
func (c *gatewayClient) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.send(ctx, http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}
	return decodeAnswer(resp, http.StatusOK, out)
}
