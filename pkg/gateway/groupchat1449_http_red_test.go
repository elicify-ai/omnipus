// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// HTTP oracles: docs/internal/specs/1449-group-chat-spec.md machine-verifiable
// constraints, DS-1 rows 7–13 and 26–27, S-18, S-21, S-24, S-26, S-150, D21.
// The real room route and the real room service decide. Only the model
// provider is the harness's scripted double.

func TestGroupChat1449_HTTPMessageRules(t *testing.T) {
	h := wu15NewHarness(t)
	room := h.create("squad", "Launch", "mia", "jim")

	t.Run("default 65537 is refused naming 65536 and stores nothing", func(t *testing.T) {
		before := len(h.snapshot(room.Id).Entries)
		status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", []byte(groupChatMessage("over-default", strings.Repeat("a", 65537), nil)))
		groupChatRequireError(t, status, body, http.StatusUnprocessableEntity, "65536")
		if got := len(h.snapshot(room.Id).Entries); got != before {
			t.Fatalf("S-118: refused message stored entries %d -> %d", before, got)
		}
	})

	t.Run("steer_body 131072 still refuses 65537 naming 65536", func(t *testing.T) {
		// D27. The live config is what Limits() reads.
		h.cfg.SessionMessaging.SteerBody = 131072
		before := len(h.snapshot(room.Id).Entries)
		status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", []byte(groupChatMessage("over-clamp", strings.Repeat("a", 65537), nil)))
		groupChatRequireError(t, status, body, http.StatusUnprocessableEntity, "65536")
		if got := len(h.snapshot(room.Id).Entries); got != before {
			t.Fatalf("S-150: a larger operator setting stored a 65537-byte message (%d -> %d)", before, got)
		}
	})

	t.Run("steer_body 1000 refuses 1001 naming 1000", func(t *testing.T) {
		h.cfg.SessionMessaging.SteerBody = 1000
		before := len(h.snapshot(room.Id).Entries)
		status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", []byte(groupChatMessage("over-lowered", strings.Repeat("a", 1001), nil)))
		groupChatRequireError(t, status, body, http.StatusUnprocessableEntity, "1000")
		if got := len(h.snapshot(room.Id).Entries); got != before {
			t.Fatalf("S-150: lowered limit stored the message (%d -> %d)", before, got)
		}
		h.cfg.SessionMessaging.SteerBody = 0
	})

	for _, validation := range []bool{false, true} {
		validation := validation
		t.Run(fmt.Sprintf("validation_%t empty text is 422 and stores nothing", validation), func(t *testing.T) {
			h.cfg.Gateway.ValidateInbound = validation
			before := len(h.snapshot(room.Id).Entries)
			status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", []byte(groupChatMessage("empty-"+fmt.Sprint(validation), "", nil)))
			groupChatRequireError(t, status, body, http.StatusUnprocessableEntity, "")
			if got := len(h.snapshot(room.Id).Entries); got != before {
				t.Fatalf("S-18: empty message stored an entry (validation %t)", validation)
			}
		})
		t.Run(fmt.Sprintf("validation_%t missing media ref is 422 and stores nothing", validation), func(t *testing.T) {
			// DS-1 row 9, S-26. A reference that does not resolve refuses the
			// whole message. Validation on must not 400 the media field as unknown.
			h.cfg.Gateway.ValidateInbound = validation
			before := len(h.snapshot(room.Id).Entries)
			raw := groupChatMessage("missing-media-"+fmt.Sprint(validation), "hi", []string{"media://workspace/sales/does-not-exist"})
			status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", []byte(raw))
			groupChatRequireError(t, status, body, http.StatusUnprocessableEntity, "")
			if got := len(h.snapshot(room.Id).Entries); got != before {
				t.Fatalf("S-26: bad media stored an entry (validation %t, %d -> %d)", validation, before, got)
			}
		})
		t.Run(fmt.Sprintf("validation_%t a 65537-character message is 422 not 400", validation), func(t *testing.T) {
			// A-12. Every oversized message is the server's 422, with request
			// validation on and off. A 65,537-character ASCII message is
			// 65,537 bytes, one past the clamp.
			h.cfg.Gateway.ValidateInbound = validation
			before := len(h.snapshot(room.Id).Entries)
			status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", []byte(groupChatMessage("chars-"+fmt.Sprint(validation), strings.Repeat("a", 65537), nil)))
			groupChatRequireError(t, status, body, http.StatusUnprocessableEntity, "65536")
			if got := len(h.snapshot(room.Id).Entries); got != before {
				t.Fatalf("A-12: oversized message stored an entry (validation %t, %d -> %d)", validation, before, got)
			}
		})
		t.Run(fmt.Sprintf("validation_%t seventeen media refs are 400 and store nothing", validation), func(t *testing.T) {
			h.cfg.Gateway.ValidateInbound = validation
			refs := make([]string, 17)
			for i := range refs {
				refs[i] = "media://workspace/sales/file"
			}
			before := len(h.snapshot(room.Id).Entries)
			status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/messages", []byte(groupChatMessage("media-17-"+fmt.Sprint(validation), "hi", refs)))
			groupChatRequireError(t, status, body, http.StatusBadRequest, "")
			if got := len(h.snapshot(room.Id).Entries); got != before {
				t.Fatalf("S-24: 17 attachments stored an entry (validation %t)", validation)
			}
		})
	}

	t.Run("no stop route on the group", func(t *testing.T) {
		// D21. The group has no stop endpoint.
		status, body := h.rawHTTP(http.MethodPost, "/api/v1/rooms/"+room.Id+"/stop", []byte(`{}`))
		if status != http.StatusNotFound && status != http.StatusMethodNotAllowed {
			t.Fatalf("D21: POST /rooms/{id}/stop status = %d, want 404 or 405; body %s", status, body)
		}
	})

	t.Run("getRoom carries invitation_context_status", func(t *testing.T) {
		status, body := h.rawHTTP(http.MethodGet, "/api/v1/rooms/"+room.Id, nil)
		if status != http.StatusOK {
			t.Fatalf("GET room status = %d, body %s", status, body)
		}
		var obj map[string]any
		if err := json.Unmarshal(body, &obj); err != nil {
			t.Fatal(err)
		}
		got, _ := obj["invitation_context_status"].(string)
		switch got {
		case "generating", "ready", "unavailable":
		default:
			t.Fatalf("S-61: invitation_context_status = %#v, want generating, ready or unavailable", obj["invitation_context_status"])
		}
	})
}

func groupChatMessage(id, text string, media []string) string {
	payload := map[string]any{
		"client_message_id": id,
		"text":              text,
		"mentions":          []string{},
	}
	if media != nil {
		payload["media"] = media
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func groupChatRequireError(t *testing.T, status int, body []byte, want int, snippet string) {
	t.Helper()
	if status != want {
		t.Fatalf("status = %d, want %d; body %s", status, want, body)
	}
	var parsed gen.ErrorResponse
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Error == "" {
		t.Fatalf("want ErrorResponse, got %s (%v)", body, err)
	}
	if snippet != "" && !strings.Contains(parsed.Error, snippet) {
		t.Fatalf("error %q does not contain %q", parsed.Error, snippet)
	}
}
