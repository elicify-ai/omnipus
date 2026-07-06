// Package memrooms — frozen log format tests (FR-7.5 / NFR-1 / TDD #6).
//
// Verifies that:
//   - CounterRecord JSON field names match the frozen v0.1.0 schema.
//   - AppendCounterRecord writes valid JSONL (one JSON line per call).
//   - Multiple appends produce multiple lines (append-only guarantee).
//
// Testing policy: written but NOT run locally (CI authority per CLAUDE.md).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package memrooms_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/memrooms"
)

// TestLogs_CounterRecord_FrozenFieldNames verifies the frozen counters.jsonl
// record format (FR-7.5 / NFR-1 / TDD #6).
func TestLogs_CounterRecord_FrozenFieldNames(t *testing.T) {
	ts := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	rec := memrooms.CounterRecord{
		TS:       ts,
		MemoryID: "mem-001",
		Op:       memrooms.CounterOpAccess,
		By:       "agent-mia",
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal CounterRecord: %v", err)
	}
	s := string(data)

	// Verify every frozen field is present.
	requiredFields := []string{`"ts"`, `"memory_id"`, `"op"`, `"by"`}
	for _, f := range requiredFields {
		if !strings.Contains(s, f) {
			t.Errorf("frozen field %s missing from CounterRecord JSON: %s", f, s)
		}
	}

	// amount must be omitempty (not present when nil).
	if strings.Contains(s, `"amount"`) {
		t.Errorf("amount must be omitted when nil, but found in JSON: %s", s)
	}
}

// TestLogs_CounterRecord_WithAmount verifies the optional amount field.
func TestLogs_CounterRecord_WithAmount(t *testing.T) {
	amt := 0.95
	rec := memrooms.CounterRecord{
		TS:       time.Now().UTC(),
		MemoryID: "mem-drift-001",
		Op:       memrooms.CounterOpDrift,
		By:       "agent-v02",
		Amount:   &amt,
	}

	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"amount"`) {
		t.Errorf("amount field must be present when non-nil, but missing in JSON: %s", s)
	}
}

// TestLogs_AppendCounterRecord_WritesValidJSONL verifies that AppendCounterRecord
// writes one valid JSON line per call and multiple calls produce multiple lines.
// Traces to: FR-7.5 / TDD #6.
func TestLogs_AppendCounterRecord_WritesValidJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	countersPath := filepath.Join(tmpDir, memrooms.CountersFile)

	recs := []memrooms.CounterRecord{
		{TS: time.Now().UTC(), MemoryID: "m-001", Op: memrooms.CounterOpAccess, By: "mia"},
		{TS: time.Now().UTC(), MemoryID: "m-002", Op: memrooms.CounterOpCited, By: "ray"},
		{TS: time.Now().UTC(), MemoryID: "m-001", Op: memrooms.CounterOpAccess, By: "mia"},
	}

	for _, rec := range recs {
		if err := memrooms.AppendCounterRecord(countersPath, rec); err != nil {
			t.Fatalf("AppendCounterRecord: %v", err)
		}
	}

	data, err := os.ReadFile(countersPath)
	if err != nil {
		t.Fatalf("ReadFile counters.jsonl: %v", err)
	}

	// Count valid JSON lines.
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	lineCount := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec memrooms.CounterRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Errorf("line %d is not valid JSON: %s — error: %v", lineCount+1, line, err)
		}
		lineCount++
	}

	if lineCount != len(recs) {
		t.Errorf("expected %d JSONL lines, got %d", len(recs), lineCount)
	}
}

// TestLogs_CounterOpValues verifies the frozen op enum values (FR-7.5 / NFR-1).
func TestLogs_CounterOpValues(t *testing.T) {
	cases := []struct {
		op       memrooms.CounterOp
		expected string
	}{
		{memrooms.CounterOpAccess, "access"},
		{memrooms.CounterOpDrift, "drift"},
		{memrooms.CounterOpCited, "cited"},
	}

	for _, tc := range cases {
		rec := memrooms.CounterRecord{
			TS:       time.Now().UTC(),
			MemoryID: "m",
			Op:       tc.op,
			By:       "agent",
		}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("Marshal op %q: %v", tc.op, err)
		}
		expected := `"op":"` + tc.expected + `"`
		if !strings.Contains(string(data), expected) {
			t.Errorf("op %q: expected %s in JSON %s", tc.op, expected, string(data))
		}
	}
}

// TestLogs_RoomEnsureCreatesDir verifies EnsureRoom creates the memories dir.
// Traces to: FR-7.1 / TDD #1.
func TestLogs_RoomEnsureCreatesDir(t *testing.T) {
	tmpDir := t.TempDir()
	room := memrooms.ResolveAgentPrivateRoom(tmpDir)

	if _, err := os.Stat(room.MemoriesDir); !os.IsNotExist(err) {
		t.Skip("dir already exists — testing EnsureRoom creation")
	}

	ensured, err := memrooms.EnsureRoom(room)
	if err != nil {
		t.Fatalf("EnsureRoom: %v", err)
	}

	if _, err := os.Stat(ensured.MemoriesDir); os.IsNotExist(err) {
		t.Errorf("EnsureRoom did not create MemoriesDir %s", ensured.MemoriesDir)
	}
}

// TestMemory_PrivateVsSharedRoom_ByWorkspace verifies that the room resolver
// produces different paths for private vs shared rooms (FR-7.1 / TDD #1).
func TestMemory_PrivateVsSharedRoom_ByWorkspace(t *testing.T) {
	omnipusHome := t.TempDir()
	agentWorkspace := filepath.Join(omnipusHome, "agents", "ray")

	private := memrooms.ResolveAgentPrivateRoom(agentWorkspace)
	shared := memrooms.ResolveWorkspaceSharedRoom(omnipusHome, "ws-acme-123")

	if private.Root == shared.Root {
		t.Errorf("private and shared rooms must have different roots, both got %s", private.Root)
	}

	wantPrivate := filepath.Join(agentWorkspace, ".omnipus")
	if private.Root != wantPrivate {
		t.Errorf("private room root: got %q, want %q", private.Root, wantPrivate)
	}

	wantShared := filepath.Join(omnipusHome, "workspaces", "ws-acme-123", ".omnipus")
	if shared.Root != wantShared {
		t.Errorf("shared room root: got %q, want %q", shared.Root, wantShared)
	}
}

// TestMemory_FileFormat_FullFrontmatter verifies that WriteMemoryFile produces
// a file with every frontmatter field present (FR-7.2 / NFR-7 / TDD #2).
func TestMemory_FileFormat_FullFrontmatter(t *testing.T) {
	tmpDir := t.TempDir()

	mf := memrooms.MemoryFile{
		Frontmatter: memrooms.MemoryFrontmatter{
			ID:         "test-id-001",
			Title:      "Test memory",
			Type:       memrooms.MemoryTypeDecision,
			Tags:       []string{"infra", "database"},
			Confidence: 0.75,
			Status:     memrooms.MemoryStatusActive,
			Supersedes: "",
			Author:     "agent-jim",
			BornIn:     "sess-abc123",
		},
		Body: "We decided to use PostgreSQL because of its JSONB support.",
	}

	if err := memrooms.WriteMemoryFile(tmpDir, mf); err != nil {
		t.Fatalf("WriteMemoryFile: %v", err)
	}

	readBack, err := memrooms.ReadMemoryFile(tmpDir, "test-id-001")
	if err != nil {
		t.Fatalf("ReadMemoryFile: %v", err)
	}

	// Verify every frontmatter field round-trips correctly.
	fm := readBack.Frontmatter
	if fm.ID != mf.Frontmatter.ID {
		t.Errorf("id: got %q want %q", fm.ID, mf.Frontmatter.ID)
	}
	if fm.Title != mf.Frontmatter.Title {
		t.Errorf("title: got %q want %q", fm.Title, mf.Frontmatter.Title)
	}
	if fm.Type != mf.Frontmatter.Type {
		t.Errorf("type: got %q want %q", fm.Type, mf.Frontmatter.Type)
	}
	if len(fm.Tags) != 2 || fm.Tags[0] != "infra" || fm.Tags[1] != "database" {
		t.Errorf("tags: got %v want [infra database]", fm.Tags)
	}
	if fm.Confidence != 0.75 {
		t.Errorf("confidence: got %.4f want 0.75", fm.Confidence)
	}
	if fm.Status != memrooms.MemoryStatusActive {
		t.Errorf("status: got %q want active", fm.Status)
	}
	if fm.Supersedes != "" {
		t.Errorf("supersedes: got %q want empty", fm.Supersedes)
	}
	if fm.Author != "agent-jim" {
		t.Errorf("author: got %q want agent-jim", fm.Author)
	}
	if fm.BornIn != "sess-abc123" {
		t.Errorf("born_in: got %q want sess-abc123", fm.BornIn)
	}
	if readBack.Body != mf.Body+"\n" && readBack.Body != mf.Body {
		t.Errorf("body: got %q want %q", readBack.Body, mf.Body)
	}

	// Verify file contains all 9 frontmatter field names.
	path := filepath.Join(tmpDir, "test-id-001.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(raw)
	requiredFields := []string{
		"id:",
		"title:",
		"type:",
		"tags:",
		"confidence:",
		"status:",
		"supersedes:",
		"author:",
		"born_in:",
	}
	for _, field := range requiredFields {
		if !strings.Contains(content, field) {
			t.Errorf("frontmatter field %q missing from serialized file:\n%s", field, content)
		}
	}
}
