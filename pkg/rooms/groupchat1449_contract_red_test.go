// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Contract oracles from docs/internal/specs/1449-group-chat-spec.md
// "Contract Changes" and T-55. Both copies of a room schema must state the
// same 65,536-byte maximum. These read the YAML; they do not execute a handler.

func TestGroupChat1449_ContractStatesTheClampedTextLimit(t *testing.T) {
	root := groupChatRepoRoot(t)
	request := groupChatRead(t, filepath.Join(root, "contracts/components/schemas/RoomMessageRequest.yaml"))
	entry := groupChatRead(t, filepath.Join(root, "contracts/components/schemas/RoomEntry.yaml"))
	async := groupChatSchemaSection(t, filepath.Join(root, "contracts/asyncapi.yaml"), "RoomEntry:")

	for _, src := range []struct{ name, body string }{
		{"RoomMessageRequest.yaml", request},
		{"RoomEntry.yaml", entry},
		{"asyncapi RoomEntry", async},
	} {
		if !strings.Contains(src.body, "maxLength: 65536") {
			t.Errorf("%s: text maxLength is not 65536 (D24, D27, T-55). Current schema still states a different maximum.", src.name)
		}
		if strings.Contains(src.body, "maxLength: 5242880") {
			t.Errorf("%s: still states maxLength 5242880; the spec replaces that with 65,536 UTF-8 bytes", src.name)
		}
	}
	textProp := groupChatProperty(request, "text:")
	if textProp == "" {
		t.Fatal("RoomMessageRequest.yaml has no text property to check against S-21")
	}
	if strings.Contains(textProp, "minLength: 1") {
		t.Error("RoomMessageRequest.text must be minLength 0 so an attachment-only message is not a schema rejection (S-21)")
	}
	media := groupChatProperty(request, "media:")
	if !strings.Contains(media, "maxItems: 16") {
		t.Error("RoomMessageRequest.media must be an array with maxItems 16 (US-3, S-24)")
	}
	attachments := groupChatProperty(entry, "attachments:")
	if !strings.Contains(attachments, "maxItems: 16") {
		t.Error("RoomEntry.attachments must be an array of MediaPart with maxItems 16 (contract table)")
	}
}

func TestGroupChat1449_ContractCarriesQuestionAndLibraryAssociation(t *testing.T) {
	root := groupChatRepoRoot(t)
	card := groupChatRead(t, filepath.Join(root, "contracts/components/schemas/AskUserQuestionCard.yaml"))
	cardInline := groupChatSchemaSection(t, filepath.Join(root, "contracts/asyncapi.yaml"), "AskUserQuestionCard:")
	for _, src := range []struct{ name, body string }{
		{"AskUserQuestionCard.yaml", card},
		{"asyncapi AskUserQuestionCard", cardInline},
	} {
		if !strings.Contains(src.body, "room_ref:") || !strings.Contains(src.body, "origin_label:") {
			t.Errorf("%s: must add optional room_ref and origin_label (US-9, contract table)", src.name)
		}
	}
	library := groupChatRead(t, filepath.Join(root, "contracts/components/schemas/LibraryEntry.yaml"))
	if !strings.Contains(library, "chat_copy:") {
		t.Error("LibraryEntry.chat_copy (read-only boolean) is required by D26 so the delete confirmation can say a chat copy will be removed")
	}
	media := groupChatRead(t, filepath.Join(root, "contracts/components/schemas/MediaLibraryEntry.yaml"))
	if !strings.Contains(media, "library_path:") {
		t.Error("MediaLibraryEntry.library_path is required by D26 as the durable association between the two copies")
	}
}

func TestGroupChat1449_ContractDescribesLibraryDeleteAndWorkspaceUpload(t *testing.T) {
	root := groupChatRepoRoot(t)
	openapi := groupChatRead(t, filepath.Join(root, "contracts/openapi.yaml"))
	del := groupChatOperation(t, openapi, "operationId: deleteLibraryEntry")
	if !strings.Contains(del, "chat copy") {
		t.Error("deleteLibraryEntry must say that deleting a file with an associated chat copy also deletes that chat copy (D26)")
	}
	if !strings.Contains(del, "could not delete") {
		t.Error(`deleteLibraryEntry 500 must be described as "could not delete <name> completely" (machine-verifiable constraints)`)
	}
	upload := groupChatOperation(t, openapi, "operationId: uploadFiles")
	if !strings.Contains(upload, "workspace_id") {
		t.Error("uploadFiles must document workspace_id (query or form) and that it routes to the library (contract table)")
	}
	if !strings.Contains(upload, "'503'") && !strings.Contains(upload, "503:") {
		t.Error("uploadFiles must document a 503 when the workspace library is unavailable (S-22)")
	}
}

func groupChatRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod from the test working directory")
		}
		dir = parent
	}
}

func groupChatRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// groupChatSchemaSection returns the asyncapi component that starts at marker
// (for example "RoomEntry:") through the next sibling key at the same indent.
func groupChatSchemaSection(t *testing.T, path, marker string) string {
	t.Helper()
	body := groupChatRead(t, path)
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("%s has no %s section", path, marker)
	}
	rest := body[start+len(marker):]
	// The next components.schemas key is a line of four spaces, an identifier
	// and a colon. Nested properties are indented further.
	searchFrom := 0
	for {
		i := strings.Index(rest[searchFrom:], "\n    ")
		if i < 0 {
			return body[start:]
		}
		i += searchFrom
		lineEnd := strings.Index(rest[i+1:], "\n")
		if lineEnd < 0 {
			lineEnd = len(rest) - (i + 1)
		}
		line := rest[i+1 : i+1+lineEnd]
		if strings.HasSuffix(strings.TrimRight(line, " "), ":") && !strings.Contains(line, " ") {
			// "    RoomEntryFrame:" has a space before the colon name... the
			// line includes the indent. Treat a line of four spaces plus an
			// identifier and a colon as the next schema.
			trimmed := strings.TrimSpace(line)
			if strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") && trimmed != ":" {
				return body[start : start+len(marker)+i]
			}
		}
		searchFrom = i + 1
		if searchFrom >= len(rest) {
			return body[start:]
		}
	}
}

func groupChatProperty(body, key string) string {
	i := strings.Index(body, "\n  "+key)
	if i < 0 {
		i = strings.Index(body, "\n    "+key)
	}
	if i < 0 {
		return ""
	}
	rest := body[i+1:]
	// Next property at the same indent, or end.
	nl := strings.Index(rest, "\n")
	if nl < 0 {
		return rest
	}
	indent := rest[:nl]
	pad := indent[:len(indent)-len(strings.TrimLeft(indent, " "))]
	// A following property starts a line with the same indent and no extra indent.
	from := nl + 1
	for from < len(rest) {
		lineEnd := strings.Index(rest[from:], "\n")
		if lineEnd < 0 {
			return rest
		}
		line := rest[from : from+lineEnd]
		if strings.HasPrefix(line, pad) && !strings.HasPrefix(line, pad+" ") && strings.Contains(line, ":") {
			return rest[:from]
		}
		from += lineEnd + 1
	}
	return rest
}

func groupChatOperation(t *testing.T, openapi, marker string) string {
	t.Helper()
	start := strings.Index(openapi, marker)
	if start < 0 {
		t.Fatalf("openapi.yaml has no %s", marker)
	}
	rest := openapi[start:]
	next := strings.Index(rest[len(marker):], "\n      operationId:")
	if next < 0 {
		return rest
	}
	return rest[:len(marker)+next]
}
