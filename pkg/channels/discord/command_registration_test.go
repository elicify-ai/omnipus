package discord

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/commands"
)

// TestDiscordChannel_ImplementsCommandRegistrarCapable verifies the interface
// is satisfied at compile time.
func TestDiscordChannel_ImplementsCommandRegistrarCapable(t *testing.T) {
	var _ channels.CommandRegistrarCapable = (*DiscordChannel)(nil)
}

// TestDiscordRegisterCommands_NoSession verifies graceful no-op when the
// session is nil (channel not yet started).
func TestDiscordRegisterCommands_NoSession(t *testing.T) {
	ch := &DiscordChannel{}
	defs := []commands.Definition{
		{Name: "cancel", Description: "Cancel the running agent task"},
	}
	// Should return nil, not panic.
	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Fatalf("RegisterCommands() with nil session returned error: %v", err)
	}
}

// TestDiscordRegisterCommands_NoAppID verifies graceful no-op when the session
// exists but the application ID is not yet available (READY not received).
func TestDiscordRegisterCommands_NoAppID(t *testing.T) {
	session, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	ch := &DiscordChannel{
		session:   session,
		botUserID: "", // no bot user ID either
	}
	defs := []commands.Definition{
		{Name: "cancel", Description: "Cancel the running agent task"},
	}
	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Fatalf("RegisterCommands() with missing app ID returned error: %v", err)
	}
}

// TestDiscordRegisterCommands_CallsBulkOverwrite is T12c: verifies that when
// the bot user ID is set (used as app ID fallback), RegisterCommands calls
// ApplicationCommandBulkOverwrite with the /cancel command in the payload.
func TestDiscordRegisterCommands_CallsBulkOverwrite(t *testing.T) {
	const fakeAppID = "111222333444555"

	// Track whether the bulk-overwrite endpoint was called with /cancel.
	var called atomic.Bool
	var receivedNames []string

	// Fake Discord API server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept any PUT to the application commands endpoint.
		if r.Method != http.MethodPut {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var cmds []*discordgo.ApplicationCommand
		if err := json.NewDecoder(r.Body).Decode(&cmds); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		for _, c := range cmds {
			receivedNames = append(receivedNames, c.Name)
		}
		called.Store(true)

		// Respond with the same list (BulkOverwrite returns created commands).
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(cmds); err != nil {
			t.Errorf("json.Encode response: %v", err)
		}
	}))
	defer srv.Close()

	// Patch the package-level endpoint function so discordgo sends requests to
	// the test server instead of discord.com.
	origFn := discordgo.EndpointApplicationGlobalCommands
	discordgo.EndpointApplicationGlobalCommands = func(aID string) string {
		return srv.URL + "/applications/" + aID + "/commands"
	}
	defer func() { discordgo.EndpointApplicationGlobalCommands = origFn }()

	session, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	// Point all HTTP calls to our test server.
	session.Client = srv.Client()

	ch := &DiscordChannel{
		session:   session,
		botUserID: fakeAppID, // used as app ID fallback
	}

	defs := []commands.Definition{
		{Name: "cancel", Description: "Cancel the running agent task"},
		{Name: "", Description: "no name — should be filtered"},
		{Name: "noDesc", Description: ""},
	}

	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Fatalf("RegisterCommands() returned error: %v", err)
	}

	if !called.Load() {
		t.Fatal("expected ApplicationCommandBulkOverwrite HTTP call, got none")
	}

	found := false
	for _, n := range receivedNames {
		if n == "cancel" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected /cancel in BulkOverwrite payload, got: %v", receivedNames)
	}

	// Verify incomplete defs were filtered out.
	for _, n := range receivedNames {
		if n == "" || n == "noDesc" {
			t.Errorf("unexpected command %q in payload (should have been filtered)", n)
		}
	}
}

// TestDiscordRegisterCommands_WithBuiltinCancel is T12c using the real builtin list.
func TestDiscordRegisterCommands_WithBuiltinCancel(t *testing.T) {
	// Use a server that accepts any PUT and returns an empty list.
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			called.Store(true)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte("[]"))
		}
	}))
	defer srv.Close()

	origFn := discordgo.EndpointApplicationGlobalCommands
	discordgo.EndpointApplicationGlobalCommands = func(aID string) string {
		return srv.URL + "/applications/" + aID + "/commands"
	}
	defer func() { discordgo.EndpointApplicationGlobalCommands = origFn }()

	session, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = srv.Client()

	ch := &DiscordChannel{
		session:   session,
		botUserID: "app-id-from-botuser",
	}

	allDefs := commands.BuiltinDefinitions()
	if err := ch.RegisterCommands(context.Background(), allDefs); err != nil {
		t.Fatalf("RegisterCommands(BuiltinDefinitions()) returned error: %v", err)
	}
}

// TestDiscordRegisterCommands_SkipsEmptyList verifies no HTTP call is made
// when all defs are filtered out.
func TestDiscordRegisterCommands_SkipsEmptyList(t *testing.T) {
	var called atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
	}))
	defer srv.Close()

	origFn := discordgo.EndpointApplicationGlobalCommands
	discordgo.EndpointApplicationGlobalCommands = func(aID string) string {
		return srv.URL + "/applications/" + aID + "/commands"
	}
	defer func() { discordgo.EndpointApplicationGlobalCommands = origFn }()

	session, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = srv.Client()

	ch := &DiscordChannel{
		session:   session,
		botUserID: "app-id-from-botuser",
	}

	// All defs missing name or description — should all be filtered.
	defs := []commands.Definition{
		{Name: "", Description: "no name"},
		{Name: "noDesc", Description: ""},
	}
	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Fatalf("RegisterCommands() returned error: %v", err)
	}
	if called.Load() {
		t.Fatal("expected no HTTP call for empty filtered list")
	}
}

// TestDiscordRegisterCommands_BulkOverwriteFailureReturnsNil is H-3: verifies
// that a platform API failure (e.g., 503 from Discord) returns nil (not an
// error) so channel startup is not blocked.  Text-parsing fallback covers the
// /cancel path per FR-28.
func TestDiscordRegisterCommands_BulkOverwriteFailureReturnsNil(t *testing.T) {
	// Server that always returns 503.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "discord unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	origFn := discordgo.EndpointApplicationGlobalCommands
	discordgo.EndpointApplicationGlobalCommands = func(aID string) string {
		return srv.URL + "/applications/" + aID + "/commands"
	}
	defer func() { discordgo.EndpointApplicationGlobalCommands = origFn }()

	session, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}
	session.Client = srv.Client()

	ch := &DiscordChannel{
		session:   session,
		botUserID: "app-id-503-test",
	}

	defs := []commands.Definition{
		{Name: "cancel", Description: "Cancel the running agent task"},
	}

	// Must return nil — startup must not be blocked by a platform API failure.
	if err := ch.RegisterCommands(context.Background(), defs); err != nil {
		t.Errorf("RegisterCommands() returned non-nil error on 503: %v (H-3: must return nil per FR-28)", err)
	}
}
