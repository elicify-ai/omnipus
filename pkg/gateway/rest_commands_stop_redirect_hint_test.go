// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// UAT defect (PR #1201 hands-on run): typing `/stop-redirect <text>` in a chat
// did not stop the turn; the raw text went to the model as a chat message and
// was steered into the live turn (the session transcripts hold the literal
// "/stop-redirect now just say the word mango" as a user message, and "mango"
// arrived only after the streamed reply had ended).
//
// Oracle: the SPA's send-path grammar, src/hooks/useSlashMenu.ts::
// resolveClientCommand — a command followed by an argument is intercepted
// (and sent as the redirect frame instead of a chat message) ONLY when the
// served command list carries delivery "client" AND a non-empty
// `argument_hint` for it. The SPA fixtures hand-write that field; the server
// is what must serve it.
func TestHandleListCommands_StopRedirectCarriesTheArgumentHintTheSPAInterceptsOn(t *testing.T) {
	api := newMinimalCommandsAPI(t)
	w := doCommandsRequest(t, api, "web", true)
	cmds := parseCommandsResponse(t, w)

	var redirect *gen.SlashCommand
	for i := range cmds {
		if cmds[i].Name == "stop-redirect" {
			redirect = &cmds[i]
		}
	}
	if redirect == nil {
		t.Fatalf("/stop-redirect missing from the web command list: %v", commandNames(cmds))
	}
	if redirect.Delivery != gen.SlashCommandDeliveryClient {
		t.Fatalf("/stop-redirect delivery = %q, want %q", redirect.Delivery, gen.SlashCommandDeliveryClient)
	}
	if redirect.ArgumentHint == nil || strings.TrimSpace(*redirect.ArgumentHint) == "" {
		t.Fatalf("/stop-redirect has no argument_hint: the SPA cannot match `/stop-redirect <text>` as a command and sends it to the model as chat text")
	}
	if got, want := *redirect.ArgumentHint, "<instruction>"; got != want {
		t.Errorf("/stop-redirect argument_hint = %q, want %q (the usage's own placeholder)", got, want)
	}
	// Only a command that takes an argument may carry a hint: a hint on a
	// no-argument client command would make `/stop foo` match as `/stop`.
	for _, c := range cmds {
		if c.Name != "stop-redirect" && c.Delivery == gen.SlashCommandDeliveryClient && c.ArgumentHint != nil {
			t.Errorf("/%s takes no argument but serves argument_hint %q", c.Name, *c.ArgumentHint)
		}
	}
}
