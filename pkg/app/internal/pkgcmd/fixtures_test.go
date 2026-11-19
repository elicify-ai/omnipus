//go:build goolm && stdjson

// RED fixtures for the T-6 CLI tests (epic #1094 slice S1a).
//
// Two process-edge doubles only:
//   - fakeGateway: an httptest server standing in for the real gateway REST
//     surface (the spec's own test plan prescribes "a fake server"); response
//     Error/result JSON follows ErrorResponse.yaml and PackageImportResult.yaml.
//     Element type ("agent") is distinct from the stored agent type
//     ("custom" for Main, "worker" for native Subagent).
//   - buildPackage: a real spec-shaped .omnpkg builder (tar + zstd, manifest
//     first), so inspect/verify fixtures are valid by construction and any
//     tampering is explicit.
//
// Expected values everywhere come from the spec (FR-050…FR-055, D4, D10, D11,
// the CLI rows of "UI Screens and States", SC-002/003/006) and the final
// contracts, never from observed CLI output.

package pkgcmd

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// testTimeout is the hard deadline every command run gets (RED brief: hard
// timeouts, no sleeps). A GREEN implementation that blocks (for example by
// waiting on a prompt with no terminal) fails the run instead of hanging it.
const testTimeout = 15 * time.Second

// fixtureAgentID is the source agent ID inside every built package (ADR D2:
// IDs inside the package are the source install's IDs).
const fixtureAgentID = "11111111-2222-3333-4444-555555555555"

// fixtureStagingID is the staging_id the fake gateway hands out in previews.
const fixtureStagingID = "staging-1"

// fixtureNewID is the new agent ID the fake gateway's apply result reports.
const fixtureNewID = "99999999-8888-7777-6666-555555555555"

// fixtureResearchWorkspaceID is the fake gateway's "Research" workspace ID
// (used with --workspace Research, D10 row 11).
const fixtureResearchWorkspaceID = "ws-research-1"

// ─── command running ─────────────────────────────────────────────────────────

// testRoot assembles the three package commands under an `omnipus` root the
// same way GREEN's app.NewRootCommand will register them (Reachability: from
// pkg/app/internal/pkgcmd/command.go), so the args passed to runCommand are
// the real CLI argv and cobra resolves the subcommand itself.
func testRoot(f *fakeGateway, opts Options) *cobra.Command {
	if opts.GatewayURL == "" {
		opts.GatewayURL = f.srv.URL
	}
	root := &cobra.Command{Use: "omnipus"}
	root.AddCommand(NewExportCommand(opts), NewImportCommand(opts), NewPackageCommand(opts))
	return root
}

// runCommand executes a pkgcmd command with the given args and stdin and
// returns what it wrote to its stdout and stderr streams plus the command
// error (inspect it with exitCodeOf, FR-053).
func runCommand(t *testing.T, cmd *cobra.Command, stdin string, args ...string) (string, string, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	err := cmd.ExecuteContext(ctx)
	return stdout.String(), stderr.String(), err
}

// exitCodeOf maps a command result to the FR-053 exit code (0/1/2). A command
// error that carries no exit code is itself the failure: FR-053 requires every
// refusal to land on 0, 1 or 2.
func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ec ExitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	t.Fatalf("command error carries no exit code (FR-053 requires 0/1/2): %v", err)
	return -1
}

// mustJSON encodes v, failing the test on a marshal error.
func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// ─── .omnpkg fixture builder ──────────────────────────────────────────────────

// pkgFixture describes the package a test builds. Zero value = a valid
// S1 package: one Main agent "Writer" with agent.json + SOUL.md + AGENT.md
// (the three files the spec's verify row counts: "OK: 3 files verified").
type pkgFixture struct {
	// AgentID overrides the source agent ID (default fixtureAgentID).
	AgentID string
	// AgentName overrides the agent name (default "Writer").
	AgentName string
	// AgentType is the stored record type: "custom" (Main, default) or
	// "worker" (native Subagent), not the UI label or manifest element type.
	AgentType string
	// FormatVersion overrides the manifest's format_version (default 1;
	// 2 is "made by a newer Omnipus", D1 row 7 / SC-003).
	FormatVersion int
	// NeedsSecrets is written into the manifest's needs_secrets list (the
	// "secrets to re-enter" inspect shows; shape per PackageManifest.yaml).
	NeedsSecrets []map[string]any
	// MutateFiles edits file contents before hashing (both hashes and bytes move).
	MutateFiles func(map[string][]byte)
	// TamperAfterHash changes file bytes AFTER the manifest hashes were
	// computed — the one-byte-change case SC-002 asks for.
	TamperAfterHash func(map[string][]byte)
	// LargeZstdWindow declares a 512 MiB zstd window (D1 row 26 offline:
	// FR-008 caps the decoder window at 8 MiB). The header is hand-written:
	// WithWindowSize alone does not force a small frame to declare that window.
	LargeZstdWindow bool
	// DeclaredWindow hand-writes a zstd window descriptor of exactly this
	// size. Only sizes the descriptor can name exactly are used: 8 MiB
	// (FR-008's cap), 9 MiB (the next representable window above that cap)
	// and 512 MiB (D1 row 26, also set by LargeZstdWindow).
	DeclaredWindow int
}

// buildPackage writes a spec-shaped .omnpkg (tar + zstd, manifest.json first,
// FR-001) and returns the bytes. Hashes are computed from the same captured
// bytes that are packaged (FR-018).
func buildPackage(t *testing.T, fx pkgFixture) []byte {
	t.Helper()

	agentID := fx.AgentID
	if agentID == "" {
		agentID = fixtureAgentID
	}
	name := fx.AgentName
	if name == "" {
		name = "Writer"
	}

	agentType := fx.AgentType
	if agentType == "" {
		agentType = "custom"
	}

	agentJSONPath := fmt.Sprintf("omnipus/agents/%s/agent.json", agentID)
	soulPath := fmt.Sprintf("omnipus/agents/%s/SOUL.md", agentID)
	agentMDPath := fmt.Sprintf("omnipus/agents/%s/AGENT.md", agentID)

	files := map[string][]byte{
		agentJSONPath: mustJSON(t, map[string]any{
			"id":          agentID,
			"name":        name,
			"type":        agentType,
			"description": "Drafts",
			"model":       map[string]any{"provider": "openrouter", "primary": "deepseek/deepseek-v4.1-flash"},
		}),
		soulPath:    []byte("Answer in rhyme.\n"),
		agentMDPath: []byte("rules\n"),
	}

	manifest := map[string]any{
		"format":         "omnpkg",
		"format_version": 1,
		"scope":          "custom",
		"elements": []map[string]any{{
			"type":      "agent",
			"schema":    1,
			"source_id": agentID,
			"name":      name,
			"files":     []string{agentJSONPath, soulPath, agentMDPath},
		}},
		"counts":            map[string]any{"agent": 1},
		"files":             map[string]any{},
		"source_build":      "v0.1.1-test",
		"created_at":        "2026-11-19T00:00:00Z",
		"includes_sessions": false,
		"needs_secrets":     []any{},
	}
	if fx.NeedsSecrets != nil {
		manifest["needs_secrets"] = fx.NeedsSecrets
	}
	if fx.MutateFiles != nil {
		fx.MutateFiles(files)
	}

	checksums := map[string]any{}
	for path, data := range files {
		sum := sha256.Sum256(data)
		checksums[path] = map[string]any{"sha256": hex.EncodeToString(sum[:]), "size": len(data)}
	}
	manifest["files"] = checksums

	if fx.FormatVersion != 0 {
		manifest["format_version"] = fx.FormatVersion
	}
	if fx.TamperAfterHash != nil {
		fx.TamperAfterHash(files)
	}

	entries := make([]tarEntry, 0, 1+len(files))
	entries = append(entries, tarEntry{name: "manifest.json", data: mustJSON(t, manifest)})
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		entries = append(entries, tarEntry{name: path, data: files[path]})
	}

	windowSize := fx.DeclaredWindow
	if fx.LargeZstdWindow {
		windowSize = 512 << 20 // D1 row 26: a declared 512 MiB window
	}
	return tarZstd(t, entries, windowSize)
}

// tarEntry is one archive member.
type tarEntry struct {
	name string
	data []byte
}

// tarZstd tars the entries (in order — the manifest must come first, FR-001)
// and zstd-compresses the stream. windowSize 0 uses the library default;
// a non-zero size hand-writes a frame that declares that window (RFC 8878),
// carrying the same valid tar in one raw block, as in the engine's limit test.
func tarZstd(t *testing.T, entries []tarEntry, windowSize int) []byte {
	t.Helper()

	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, e := range entries {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     e.name,
			Mode:     0o644,
			Size:     int64(len(e.data)),
			ModTime:  time.Unix(0, 0).UTC(),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write(e.data)
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())

	if windowSize != 0 {
		return declaredWindowFrame(t, tarBuf.Bytes(), windowSize)
	}
	var zbuf bytes.Buffer
	enc, err := zstd.NewWriter(&zbuf)
	require.NoError(t, err)
	_, err = enc.Write(tarBuf.Bytes())
	require.NoError(t, err)
	require.NoError(t, enc.Close())
	return zbuf.Bytes()
}

// soulSHA returns the SHA-256 the manifest records for SOUL.md of a default
// fixture, so inspect-output assertions can name the exact expected checksum.
// zstdWindowDescriptor is the RFC 8878 window-descriptor byte for a size
// the 5-bit exponent and 3-bit mantissa can name exactly:
// size = 2^(10+exp) + mantissa * 2^(10+exp-3).
func zstdWindowDescriptor(t *testing.T, window int) byte {
	t.Helper()
	switch window {
	case 8 << 20: // FR-008 cap: exp 13, mantissa 0 → 2^23 = 8 MiB
		return 13 << 3
	case 9 << 20: // next representable above the cap: exp 13, mantissa 1 → 8 MiB + 1 MiB
		return (13 << 3) | 1
	case 512 << 20: // D1 row 26: exp 19, mantissa 0 → 2^29 = 512 MiB
		return 19 << 3
	default:
		t.Fatalf("declared window %d is not an exact zstd window these fixtures use", window)
		return 0
	}
}

// declaredWindowFrame hand-writes magic + FHD 0x00 + a window descriptor,
// then one final raw block holding payload. Header checks are independent
// of the package reader under test.
func declaredWindowFrame(t *testing.T, payload []byte, window int) []byte {
	t.Helper()
	require.LessOrEqual(t, len(payload), 128<<10, "RFC 8878: a raw block is at most 128 KiB")
	desc := zstdWindowDescriptor(t, window)
	// 4 magic + FHD + window descriptor + 3-byte block header + payload.
	frame := make([]byte, 0, 9+len(payload))
	frame = append(frame, 0x28, 0xB5, 0x2F, 0xFD, 0x00, desc)
	// FHD 0: no single-segment flag, no frame-content size, no checksum, no dictionary.
	hdr := uint32(1) | uint32(len(payload))<<3 // last block, raw, payload length
	frame = append(frame, byte(hdr), byte(hdr>>8), byte(hdr>>16))
	frame = append(frame, payload...)

	var header zstd.Header
	require.NoError(t, header.Decode(frame))
	require.False(t, header.SingleSegment, "fixture must declare a window, not just a content size")
	require.Equal(t, uint64(window), header.WindowSize)
	require.True(t, header.FirstBlock.OK)
	require.True(t, header.FirstBlock.Last)
	require.False(t, header.FirstBlock.Compressed, "raw block: the window is not implied by a content size")
	require.Equal(t, len(payload), header.FirstBlock.DecompressedSize)
	return frame
}

func soulSHA(t *testing.T) string {
	t.Helper()
	sum := sha256.Sum256([]byte("Answer in rhyme.\n"))
	return hex.EncodeToString(sum[:])
}

// writeFixtureFile writes a built package to dir/name and returns its path.
func writeFixtureFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0o600))
	return path
}

// emptyHome makes a fresh empty OMNIPUS_HOME for FR-055/SC-006 assertions
// and binds it as the process home. package inspect/verify ignore Options
// (NewPackageCommand drops them), so checking an unbound TempDir would stay
// green if a regression wrote the resolved real home. The directory asserted
// empty is the one OmnipusHomeDir returns.
func emptyHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	return home
}

// requireDirEmpty fails unless dir holds no entries at all.
func requireDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries, "the data folder must stay empty (FR-055/SC-006), found %d entries", len(entries))
}

// unreachableGatewayURL returns an http:// URL on 127.0.0.1 whose listener is
// already closed, so connections are refused deterministically (FR-054 test).
func unreachableGatewayURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	_ = ln.Close()
	return "http://" + addr
}

// ─── fake gateway ─────────────────────────────────────────────────────────────

// capturedRequest is one request the CLI made against the fake gateway.
type capturedRequest struct {
	method string
	path   string
	body   []byte
	header http.Header
}

// applyReply is the scripted answer for one apply call.
type applyReply struct {
	status int
	body   string
}

// fakeGateway is the httptest gateway the CLI tests talk to. Every endpoint
// records its requests so tests can assert the CLI's wire behaviour (what it
// sent, in which order, and what it did NOT send).
type fakeGateway struct {
	t       *testing.T
	srv     *httptest.Server
	handler http.Handler // same mux as srv; a retry test serves it on its own listener

	mu         sync.Mutex
	agents     []map[string]any
	workspaces []map[string]any

	// Export behaviour: when exportErrCode is empty the endpoint serves
	// exportPkg as 200 application/octet-stream with a Content-Disposition
	// attachment header; otherwise it answers exportStatus/exportErrCode.
	exportPkg     []byte
	exportStatus  int
	exportErrCode string
	exportMsg     string

	// Upload behaviour: previewJSON served as 201, or the coded error.
	previewStatus int
	previewCode   string
	previewMsg    string
	previewJSON   string

	// Apply behaviour: replies consumed in order; applyDropConn drops the
	// connection without answering (the lost-connection BDD scenario).
	applyReplies []applyReply
	applyDrop    bool

	agentsReqs     []capturedRequest
	workspacesReqs []capturedRequest
	exports        []capturedRequest
	uploads        []capturedRequest
	applies        []capturedRequest
}

func newFakeGateway(t *testing.T) *fakeGateway {
	t.Helper()
	f := &fakeGateway{t: t}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/agents", f.serveAgents)
	mux.HandleFunc("GET /api/v1/workspaces", f.serveWorkspaces)
	mux.HandleFunc("POST /api/v1/packages/export", f.serveExport)
	mux.HandleFunc("POST /api/v1/packages/import", f.serveUpload)
	mux.HandleFunc("POST /api/v1/packages/import/", f.serveApply)
	f.handler = mux
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close) // Server.Close returns nothing; it waits for handlers
	return f
}

// withRoster configures the fake's GET /api/v1/agents payload (FR-050's lookup
// source: the CLI resolves names against the gateway's roster, never the data
// folder — FR-055).
func (f *fakeGateway) withRoster(agents ...map[string]any) *fakeGateway {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.agents = agents
	return f
}

// withWorkspaces configures the fake's GET /api/v1/workspaces payload.
func (f *fakeGateway) withWorkspaces(workspaces ...map[string]any) *fakeGateway {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspaces = workspaces
	return f
}

// serveExportError scripts POST /api/v1/packages/export to refuse with a
// coded error (the export refusal rows of D4/D10/D11 surface here).
func (f *fakeGateway) serveExportError(status int, code, msg string) *fakeGateway {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exportStatus, f.exportErrCode, f.exportMsg = status, code, msg
	return f
}

// serveExportPackage scripts a successful export returning the package bytes.
func (f *fakeGateway) serveExportPackage(pkg []byte) *fakeGateway {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exportPkg = pkg
	return f
}

// servePreview scripts POST /api/v1/packages/import (the upload).
func (f *fakeGateway) servePreview(body string) *fakeGateway {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previewJSON = body
	return f
}

// servePreviewError scripts a coded upload refusal.
func (f *fakeGateway) servePreviewError(status int, code, msg string) *fakeGateway {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.previewStatus, f.previewCode, f.previewMsg = status, code, msg
	return f
}

// scriptApply scripts apply answers consumed in call order; withDrop makes the
// handler drop the connection instead of answering (lost-connection scenario).
func (f *fakeGateway) scriptApply(replies []applyReply, withDrop bool) *fakeGateway {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applyReplies = replies
	f.applyDrop = withDrop
	return f
}

func (f *fakeGateway) record(list *[]capturedRequest, r *http.Request) capturedRequest {
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		f.t.Errorf("fake gateway: reading request body: %v", readErr)
	}
	captured := capturedRequest{method: r.Method, path: r.URL.Path, body: body, header: r.Header.Clone()}
	f.mu.Lock()
	*list = append(*list, captured)
	f.mu.Unlock()
	return captured
}

func (f *fakeGateway) serveAgents(w http.ResponseWriter, r *http.Request) {
	f.record(&f.agentsReqs, r)
	f.mu.Lock()
	agents := f.agents
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(mustJSON(f.t, agents))
}

func (f *fakeGateway) serveWorkspaces(w http.ResponseWriter, r *http.Request) {
	f.record(&f.workspacesReqs, r)
	f.mu.Lock()
	workspaces := f.workspaces
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(mustJSON(f.t, workspaces))
}

func (f *fakeGateway) serveExport(w http.ResponseWriter, r *http.Request) {
	f.record(&f.exports, r)
	f.mu.Lock()
	status, code, msg, pkg := f.exportStatus, f.exportErrCode, f.exportMsg, f.exportPkg
	f.mu.Unlock()
	if code != "" {
		f.writeError(w, status, code, msg)
		return
	}
	// Success shape per the contract table: octet-stream bytes with an
	// attachment filename derived from the agent name.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="writer.omnpkg"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pkg)
}

func (f *fakeGateway) serveUpload(w http.ResponseWriter, r *http.Request) {
	f.record(&f.uploads, r)
	f.mu.Lock()
	status, code, msg, preview := f.previewStatus, f.previewCode, f.previewMsg, f.previewJSON
	f.mu.Unlock()
	if code != "" {
		f.writeError(w, status, code, msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(preview))
}

func (f *fakeGateway) serveApply(w http.ResponseWriter, r *http.Request) {
	f.record(&f.applies, r)
	f.mu.Lock()
	replies, drop := f.applyReplies, f.applyDrop
	f.mu.Unlock()
	if drop {
		// Simulates the connection dying before the answer arrives: the
		// server aborts the response, the client sees a transport error.
		panic(http.ErrAbortHandler)
	}
	reply := applyReply{status: http.StatusInternalServerError, body: `{"code":"fake_gateway_bug","error":"apply answer not scripted"}`}
	if len(replies) > 0 {
		reply = replies[0]
		replies = replies[1:]
	}
	f.mu.Lock()
	f.applyReplies = replies
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(reply.status)
	_, _ = w.Write([]byte(reply.body))
}

func (f *fakeGateway) writeError(w http.ResponseWriter, status int, code, msg string) {
	if msg == "" {
		msg = code
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(mustJSON(f.t, map[string]any{"code": code, "error": msg}))
}

// counts returns how many requests hit each recorded surface. Zero-request
// assertions prove the offline contract (inspect/verify need no gateway,
// FR-052) and the refuse-before-sending order (D10 rows 3/4/12/16).
type gatewayCounts struct {
	agents, workspaces, exports, uploads, applies int
}

func (f *fakeGateway) counts() gatewayCounts {
	f.mu.Lock()
	defer f.mu.Unlock()
	return gatewayCounts{
		agents:     len(f.agentsReqs),
		workspaces: len(f.workspacesReqs),
		exports:    len(f.exports),
		uploads:    len(f.uploads),
		applies:    len(f.applies),
	}
}

// last returns the most recent request captured on a surface.
func (f *fakeGateway) last(list []capturedRequest) capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(list) == 0 {
		f.t.Errorf("fake gateway: expected at least one request, got none")
		return capturedRequest{}
	}
	return list[len(list)-1]
}

// ─── fake gateway response bodies (spec "Contract Changes" shapes) ────────────

// previewBody builds a PackageImportPreview JSON body (provisional by
// contract): staging id, will_add with proposed names, warnings, needs_secrets.
func previewBody(t *testing.T, warnings []map[string]any) string {
	t.Helper()
	if warnings == nil {
		warnings = []map[string]any{}
	}
	body := map[string]any{
		"staging_id": fixtureStagingID,
		"expires_at": "2026-11-19T00:30:00Z",
		"manifest": map[string]any{
			"format": "omnpkg", "format_version": 1, "scope": "custom",
			"source_build": "v0.1.1-test",
		},
		"will_add": []map[string]any{{
			"type": "agent", "source_id": fixtureAgentID,
			"source_name": "Writer", "proposed_name": "Writer", "renamed": false,
		}},
		"skipped_elements": []any{},
		"warnings":         warnings,
		"needs_secrets":    []any{},
	}
	return string(mustJSON(t, body))
}

// resultBody builds a PackageImportResult JSON body: created[].type is the
// element type; activation is a string with separate activation_message.
// Worker classification comes from the package's own agent.json (FR-036b).
func resultBody(t *testing.T, finalName, workspaceID, activation string, warnings []map[string]any) string {
	t.Helper()
	if warnings == nil {
		warnings = []map[string]any{}
	}
	activationMsg := "the live agent list confirmed the import"
	if activation == "pending" {
		activationMsg = "the live-publish reload did not confirm"
	}
	body := map[string]any{
		"mode": "new",
		"created": []map[string]any{{
			"type": "agent", "source_id": fixtureAgentID, "new_id": fixtureNewID,
			"name": finalName, "workspace_id": workspaceID,
		}},
		"warnings":           warnings,
		"needs_secrets":      []any{},
		"activation":         activation,
		"activation_message": activationMsg,
	}
	return string(mustJSON(t, body))
}
