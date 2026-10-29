// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package rooms

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Contract oracles from docs/internal/specs/1449-group-chat-spec.md
// "Contract Changes", T-55 and TD-8. RoomMessageRequest.text is 65,536.
// RoomEntry.text keeps the 5,242,880 read bound and has no 65,536 maximum.
// These read the YAML; they do not execute a handler.

func TestGroupChat1449_ContractStatesTheClampedTextLimit(t *testing.T) {
	root := groupChatRepoRoot(t)
	request := groupChatRead(t, filepath.Join(root, "contracts/components/schemas/RoomMessageRequest.yaml"))
	entry := groupChatRead(t, filepath.Join(root, "contracts/components/schemas/RoomEntry.yaml"))
	asyncEntry := groupChatSchemaSection(t, filepath.Join(root, "contracts/asyncapi.yaml"), "RoomEntry:")
	ref := groupChatSchemaSection(t, filepath.Join(root, "contracts/openapi.yaml"), "RoomMessageRequest:")
	if !strings.Contains(ref, "./components/schemas/RoomMessageRequest.yaml") {
		t.Fatalf("openapi RoomMessageRequest is not the component schema: %s", ref)
	}

	for _, src := range []struct{ name, body string }{
		{"RoomMessageRequest.yaml", request},
	} {
		if got := groupChatFieldMaxLength(t, src.body, "text:"); got != MaxGroupMessageBytes {
			t.Errorf("%s text maxLength = %d, want %d (T-55, the server clamp)", src.name, got, MaxGroupMessageBytes)
		}
	}
	for _, src := range []struct{ name, body string }{
		{"RoomEntry.yaml", entry},
		{"asyncapi RoomEntry", asyncEntry},
	} {
		got := groupChatFieldMaxLength(t, src.body, "text:")
		if got != 5242880 {
			t.Errorf("%s text maxLength = %d, want 5242880 (TD-8 read bound)", src.name, got)
		}
		if got == 65536 {
			t.Errorf("%s puts the 65536 write limit on stored entries (TD-8)", src.name)
		}
	}
	textProp := groupChatFieldBlock(request, "text:")
	if textProp == "" {
		t.Fatal("RoomMessageRequest.yaml has no text property to check against S-21")
	}
	if strings.Contains(textProp, "minLength: 1") {
		t.Error("RoomMessageRequest.text must be minLength 0 so an attachment-only message is not a schema rejection (S-21)")
	}
	media := groupChatFieldBlock(request, "media:")
	if !strings.Contains(media, "maxItems: 16") {
		t.Error("RoomMessageRequest.media must be an array with maxItems 16 (US-3, S-24)")
	}
	attachments := groupChatFieldBlock(entry, "attachments:")
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

// groupChatSchemaSection returns the component whose key is exactly marker
// (for example "RoomEntry:"), not a key that merely contains that text.
// "receiveRoomEntry:" must not be read as "RoomEntry:".
func groupChatSchemaSection(t *testing.T, path, marker string) string {
	t.Helper()
	body := groupChatRead(t, path)
	needle := "\n    " + marker
	start := strings.Index(body, needle)
	if start < 0 {
		t.Fatalf("%s has no %s section", path, marker)
	}
	start++ // keep the indented key, drop the preceding newline
	rest := body[start+len("    "+marker):]
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
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "     ") && strings.HasSuffix(trimmed, ":") && !strings.Contains(trimmed, " ") {
			return body[start : start+len("    "+marker)+i]
		}
		searchFrom = i + 1
		if searchFrom >= len(rest) {
			return body[start:]
		}
	}
}

// groupChatFieldBlock is the YAML of one property, at whatever indent it uses.
func groupChatFieldBlock(body, field string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != field {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		var block []string
		block = append(block, line)
		for _, next := range lines[i+1:] {
			if strings.TrimSpace(next) == "" {
				block = append(block, next)
				continue
			}
			nextIndent := len(next) - len(strings.TrimLeft(next, " "))
			if nextIndent <= indent {
				break
			}
			block = append(block, next)
		}
		return strings.Join(block, "\n")
	}
	return ""
}

func groupChatFieldMaxLength(t *testing.T, body, field string) int {
	t.Helper()
	block := groupChatFieldBlock(body, field)
	if block == "" {
		t.Fatalf("no %s property", field)
	}
	for _, line := range strings.Split(block, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "maxLength:") {
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trim, "maxLength:")))
			if err != nil {
				t.Fatalf("%s maxLength: %v", field, err)
			}
			return n
		}
	}
	t.Fatalf("%s has no maxLength", field)
	return 0
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
