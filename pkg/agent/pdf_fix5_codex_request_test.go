package agent

// FR-12 + F4/I3/SF-1: test the factory-produced Codex provider, not Azure.
// Credentials are disposable fixtures. Only the outbound HTTP transport is
// redirected; the actual factory, OAuth source, SDK and serializers stay real.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

type pdfFix5RoundTrip func(*http.Request) (*http.Response, error)

func (f pdfFix5RoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func pdfFix5FactoryCodex(t *testing.T) providers.LLMProvider {
	t.Helper()
	store := credentials.NewStore(filepath.Join(t.TempDir(), "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x45}, 32)))
	t.Cleanup(func() { store.Close() })
	cred, err := json.Marshal(map[string]any{
		"access_token": "pdf-fixture-not-a-real-token", "account_id": "pdf-fixture-account",
		"expires_at": time.Now().Add(time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, store.Set(credentials.OAuthEntryName(providers.OAuthVendorID("openai-chatgpt")), string(cred)))
	previous := providers.DefaultCredentialStore
	providers.SetDefaultCredentialStore(store)
	t.Cleanup(func() { providers.SetDefaultCredentialStore(previous) })
	p, model, err := providers.CreateProviderFromConfig(&config.ModelConfig{
		Provider: "openai-chatgpt", Model: "gpt-5.4", Custom: true,
		Protocol: "openai-compatible", APIBase: "https://chatgpt.com/backend-api/codex",
	})
	require.NoError(t, err)
	require.IsType(t, &providers.CodexProvider{}, p, "premise: the actual factory route must be exercised")
	require.Equal(t, "gpt-5.4", model)
	return p
}

func TestPDF_CodexFactoryWholeRequestLimit(t *testing.T) {
	var mu sync.Mutex
	var bodies []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read", 500)
			return
		}
		mu.Lock()
		bodies = append(bodies, string(raw))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-pdf\",\"status\":\"completed\",\"model\":\"gpt-5.4\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"msg-pdf\",\"content\":[{\"type\":\"output_text\",\"text\":\"codex reached\",\"annotations\":[]}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	t.Cleanup(peer.Close)
	target, err := url.Parse(peer.URL)
	require.NoError(t, err)
	original := http.DefaultTransport
	http.DefaultTransport = pdfFix5RoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "chatgpt.com" || r.URL.Path != "/backend-api/codex/responses" {
			return nil, fmt.Errorf("unexpected outbound target %s", r.URL.Redacted())
		}
		requestCopy := r.Clone(r.Context())
		u := *r.URL
		u.Scheme, u.Host = target.Scheme, target.Host
		requestCopy.URL = &u
		requestCopy.Host = target.Host
		return original.RoundTrip(requestCopy)
	})
	t.Cleanup(func() { http.DefaultTransport = original })
	p := pdfFix5FactoryCodex(t)
	assert.Implements(t, (*providers.RequestBodyMeasurer)(nil), p, "F4: the actual factory route must support whole-body measurement")
	f := pdfFix5Flow(t, p, "openai-chatgpt", "gpt-5.4", nil)
	oldPDF := pdf1968PDFOfSize(t, 16<<10)
	newPDF := pdf1968PDFOfSize(t, 18<<10)
	messages := []providers.Message{
		{Role: "system", Content: "whole-body-instruction " + strings.Repeat("non-PDF envelope ", 200)},
		{Role: "user", Content: "older PDF", Media: []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(oldPDF)}},
		{Role: "user", Content: "newer PDF", Media: []string{"data:application/pdf;base64," + base64.StdEncoding.EncodeToString(newPDF)}},
	}
	defs := []providers.ToolDefinition{{Type: "function", Function: providers.ToolFunctionDefinition{Name: "inspect_receipt", Description: strings.Repeat("tool envelope ", 100), Parameters: map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}}}}
	opts := map[string]any{"max_tokens": 256, "native_search": true}
	send := func(limit int) string {
		t.Helper()
		var limits map[string]any
		if limit > 0 {
			limits = map[string]any{"max_request_bytes": limit}
		}
		pdf1968SetLimits(t, f.al, nil, []pdf1968ProviderRow{pdf1968CapableRow("openai-chatgpt", "gpt-5.4", limits)})
		prepared, prepErr := f.rt.prepareAttemptRequest(context.Background(), p, "openai-chatgpt", "gpt-5.4", messages, defs, opts, false)
		require.NoError(t, prepErr)
		mu.Lock()
		before := len(bodies)
		mu.Unlock()
		resp, callErr := p.Chat(context.Background(), prepared.messages, defs, "gpt-5.4", opts)
		require.NoError(t, callErr)
		require.NotNil(t, resp)
		require.Equal(t, "codex reached", resp.Content)
		mu.Lock()
		defer mu.Unlock()
		require.Len(t, bodies, before+1, "measurement must not send any extra provider request")
		return bodies[before]
	}
	native := send(0)
	require.Equal(t, [][]byte{oldPDF, newPDF}, pdf1968BodyPDFs(t, native), "positive control: factory client sends both exact PDFs in history order")
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(native), &envelope))
	require.Contains(t, string(envelope["instructions"]), "whole-body-instruction")
	require.Contains(t, string(envelope["tools"]), "inspect_receipt")
	require.Equal(t, "true", string(envelope["stream"]), "Codex is streaming even through Chat")
	require.Contains(t, string(envelope["tools"]), "web_search", "transport-added native tool must count in the byte ceiling")
	aboveLimit := send(len(native) + 1)
	assert.Equal(t, native, aboveLimit, "positive control: body is one byte under its ceiling")
	atLimit := send(len(native))
	assert.Equal(t, native, atLimit, "FR-12 equality is inclusive for the entire SDK body")
	oneLess := send(len(native) - 1)
	assert.Equal(t, [][]byte{newPDF}, pdf1968BodyPDFs(t, oneLess), "F4: one byte over the whole body ceiling demotes the oldest PDF, not the newest")
	assert.LessOrEqual(t, len(oneLess), len(native)-1, "FR-12: remeasure the entire serialized streaming body")
	assert.Contains(t, oneLess, "PDF sent as text")
	assert.Contains(t, oneLess, "PAGE 1 text")
	// Re-measurement must continue when one demotion still does not fit.
	allText := send(1)
	assert.Empty(t, pdf1968BodyPDFs(t, allText), "both PDFs must demote when the first demotion is still over")
	assert.Greater(t, len(allText), 1, "FR-12 all-text-over is sent normally, not falsely refused")
	assert.Contains(t, allText, "PDF sent as text")
	assert.NotContains(t, allText, pdf1968P1Sentence, "proactive fitting is not provider-rejection recovery")
	assert.False(t, f.rt.ts.mediaRetryDone.Load())
}
