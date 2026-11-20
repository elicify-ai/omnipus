package omnpkg_test

// Test-side fixture harness for the pkg/omnpkg RED tests (T-2, epic #1094).
// Everything here is test-owned: packages are built and decoded with the
// standard library (archive/tar) and klauspost/compress/zstd directly, never
// through the engine under test, so expectations stay independent of the
// implementation (elicify-test-writing: oracle independence).

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/elicify-ai/omnipus/pkg/omnpkg"
	"github.com/stretchr/testify/require"
)

const (
	testCtxTimeout = 30 * time.Second // hard timeout for every engine call
	raceTimeout    = 5 * time.Second  // hard timeout for goroutine synchronisation
)

// testCtx gives every engine call a hard deadline (tests sleep nowhere).
func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testCtxTimeout)
	t.Cleanup(cancel)
	return ctx
}

// wantCode asserts a specific wire error code (the spec's oracle). On the RED
// stub every engine call fails with ErrNotImplemented, so this reports
// expected code vs "not implemented" — the failing assertion the RED round
// is proven from.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	require.Error(t, err, "expected coded error %s, got success", code)
	require.Equal(t, code, omnpkg.CodeOf(err), "expected coded error %s, got %v", code, err)
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Agent record fixtures. JSON names are the "stored JSON name" column of the
// spec's Field mapping table (checked against pkg/config AgentConfig tags).
// ---------------------------------------------------------------------------

const (
	testSourceID   = "5f0c1a2b-3d4e-4f5a-8b7c-9d0e1f2a3b4c"
	testSourceID2  = "aaabbbcc-1111-4222-8333-444455556666"
	testBuild      = "v0.1.1-test"
	testRecordName = "Writer"
	testSoulBody   = "Answer in rhyme, every time.\n"
	testAgentBody  = "House rules: keep answers short.\n"
)

// baseRecord returns a valid Main-agent record map for the given source id.
func baseRecord(id string) map[string]any {
	return map[string]any{
		"id":          id,
		"name":        testRecordName,
		"description": "Drafts and edits prose",
		"type":        "custom", // Main agent (Field mapping row `type`)
		"model": map[string]any{
			"primary":  "claude-sonnet-4.6",
			"provider": "openrouter",
		},
		"fallback_models": []any{
			map[string]any{"model": "claude-opus-4.7", "provider": "openrouter", "reasoning_effort": "high"},
		},
		"reasoning_effort":        "high",
		"max_tool_iterations":     40,
		"model_params":            map[string]any{"temperature": 0.2, "max_tokens": 4096},
		"skills":                  []string{"writing"},
		"tools":                   map[string]any{},
		"voice":                   "alloy",
		"figure":                  "Robot",
		"role":                    "editor",
		"color":                   "#1f6feb",
		"memory_enabled":          false,
		"context_window_override": 64000,
	}
}

// soulPath / recordPath / instructionsPath are the agent layout entries.
func recordPath(id string) string { return omnpkg.AgentsPrefix + id + "/" + omnpkg.AgentRecordName }
func soulPath(id string) string   { return omnpkg.AgentsPrefix + id + "/" + omnpkg.AgentSoulName }
func instructionsPath(id string) string {
	return omnpkg.AgentsPrefix + id + "/" + omnpkg.AgentInstructionsNm
}

// validManifest builds the manifest for a one-agent package whose payload is
// exactly agent.json + SOUL.md (+ AGENT.md when withAgentMD).
func validManifest(id string, withAgentMD bool) (*omnpkg.Manifest, map[string][]byte) {
	record, _ := json.Marshal(baseRecord(id))
	soul := []byte(testSoulBody)
	files := map[string][]byte{
		recordPath(id): record,
		soulPath(id):   soul,
	}
	owned := []string{recordPath(id), soulPath(id)}
	if withAgentMD {
		files[instructionsPath(id)] = []byte(testAgentBody)
		owned = append(owned, instructionsPath(id))
	}
	m := &omnpkg.Manifest{
		Format:        omnpkg.FormatName,
		FormatVersion: omnpkg.FormatVersion,
		Scope:         "custom",
		Elements: []omnpkg.ManifestElement{{
			Type:     "agent",
			Schema:   omnpkg.AgentSchemaVersion,
			SourceID: id,
			Name:     testRecordName,
			Files:    owned,
		}},
		Counts:           map[string]int{"agent": 1},
		Files:            map[string]omnpkg.FileInfo{},
		SourceBuild:      testBuild,
		CreatedAt:        time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		IncludesSessions: false,
	}
	for p, b := range files {
		m.Files[p] = omnpkg.FileInfo{SHA256: sha256hex(b), Size: int64(len(b))}
	}
	return m, files
}

// ---------------------------------------------------------------------------
// Package building (test-side; independent of the engine).
// ---------------------------------------------------------------------------

type tarEntry struct {
	Name     string
	Body     []byte
	Typeflag byte
	Linkname string
	Mode     int64
}

// buildArchive writes entries as a tar (no engine involvement) and compresses
// it with zstd.
func buildArchive(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.Name,
			Size:     int64(len(e.Body)),
			Typeflag: e.Typeflag,
			Linkname: e.Linkname,
			Mode:     e.Mode,
			ModTime:  time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC),
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if len(e.Body) > 0 {
			_, err := tw.Write(e.Body)
			require.NoError(t, err)
		}
	}
	require.NoError(t, tw.Close())

	var out bytes.Buffer
	zw, err := zstd.NewWriter(&out)
	require.NoError(t, err)
	_, err = zw.Write(raw.Bytes())
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return out.Bytes()
}

// manifestEntry renders m to JSON with optional patches: extra top-level keys
// (unknown-field cases), dropped top-level keys ("missing field" cases), and
// padding with an unknown ignored string field until the manifest is at least
// minLength bytes (size-cap cases keep valid semantics).
func manifestEntry(t *testing.T, m *omnpkg.Manifest, extra map[string]any, drop []string, minLength int) tarEntry {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	if len(extra) > 0 || len(drop) > 0 || minLength > 0 {
		var doc map[string]any
		require.NoError(t, json.Unmarshal(raw, &doc))
		for k, v := range extra {
			doc[k] = v
		}
		for _, k := range drop {
			delete(doc, k)
		}
		if minLength > 0 {
			raw, err = json.Marshal(doc)
			require.NoError(t, err)
			// Exact padding: the padded JSON gains `,"vendor_padding":"…"`
			// — comma 1 + `"vendor_padding"` 16 + colon 1 + two quotes 2,
			// so n = minLength - len(raw) - 20 lands exactly.
			const padOverhead = 20
			if n := minLength - len(raw) - padOverhead; n > 0 {
				doc["vendor_padding"] = string(bytes.Repeat([]byte("p"), n))
			}
			raw, err = json.Marshal(doc)
			require.NoError(t, err)
			require.Equal(t, minLength, len(raw), "padding arithmetic must be exact")
		}
	}
	return tarEntry{Name: omnpkg.ManifestEntry, Body: raw}
}

// buildPackagePadded builds a package whose manifest is padded to at least
// targetLen bytes (unknown ignored field).
func buildPackagePadded(t *testing.T, m *omnpkg.Manifest, payload map[string][]byte, targetLen int) []byte {
	t.Helper()
	return buildPackagePatched(t, m, payload, nil, nil, targetLen)
}

// buildPackage assembles a complete valid package (manifest first, FR-001).
func buildPackage(t *testing.T, m *omnpkg.Manifest, payload map[string][]byte) []byte {
	t.Helper()
	return buildPackagePatched(t, m, payload, nil, nil, 0)
}

// buildPackagePatched is buildPackage with manifest patches (see manifestEntry).
func buildPackagePatched(t *testing.T, m *omnpkg.Manifest, payload map[string][]byte, extra map[string]any, drop []string, minLength int) []byte {
	t.Helper()
	entries := make([]tarEntry, 0, 1+len(payload))
	entries = append(entries, manifestEntry(t, m, extra, drop, minLength))
	for _, name := range sortedKeys(payload) {
		entries = append(entries, tarEntry{Name: name, Body: payload[name]})
	}
	return buildArchive(t, entries)
}

func sortedKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// deterministic order: manifest first already, the rest sorted
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// gzipWrap compresses with gzip instead of zstd (D1 17).
func gzipWrap(t *testing.T, b []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gw := gzip.NewWriter(&out)
	_, err := gw.Write(b)
	require.NoError(t, err)
	require.NoError(t, gw.Close())
	return out.Bytes()
}

// ---------------------------------------------------------------------------
// Package decoding (test-side; verifies export output without the engine).
// ---------------------------------------------------------------------------

type decodedPackage struct {
	Order   []string // entry order as stored in the tar
	Payload map[string][]byte
}

// decodePackagePlain decodes a zstd+tar package with no limits and returns the
// entries in stored order plus the payload (manifest.json included).
func decodePackagePlain(t *testing.T, pkg []byte) decodedPackage {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(pkg))
	require.NoError(t, err)
	defer zr.Close()
	tr := tar.NewReader(zr)
	dec := decodedPackage{Payload: map[string][]byte{}}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err, "tar decode of fixture/export output")
		dec.Order = append(dec.Order, hdr.Name)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		dec.Payload[hdr.Name] = body
	}
	return dec
}

// ---------------------------------------------------------------------------
// Fake stores (the process edges; the engine stays real).
// ---------------------------------------------------------------------------

// fakeSource is the ExportSource edge: records and home folders in memory plus
// a temp dir holding SOUL.md / AGENT.md.
type fakeSource struct {
	records  map[string][]byte
	homeDirs map[string]string
	coreIDs  map[string]bool
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		records:  map[string][]byte{},
		homeDirs: map[string]string{},
		coreIDs:  map[string]bool{},
	}
}

func (f *fakeSource) withAgent(t *testing.T, id string, record map[string]any, soul, agentMD []byte) string {
	t.Helper()
	raw, err := json.Marshal(record)
	require.NoError(t, err)
	f.records[id] = raw
	home := t.TempDir()
	f.homeDirs[id] = home
	require.NoError(t, os.WriteFile(filepath.Join(home, omnpkg.AgentSoulName), soul, 0o644))
	if agentMD != nil {
		require.NoError(t, os.WriteFile(filepath.Join(home, omnpkg.AgentInstructionsNm), agentMD, 0o644))
	}
	return home
}

func (f *fakeSource) AgentRecord(id string) ([]byte, error) {
	raw, ok := f.records[id]
	if !ok {
		return nil, fmt.Errorf("agent %q not found", id)
	}
	return raw, nil
}

func (f *fakeSource) AgentHomeDir(id string) (string, error) {
	dir, ok := f.homeDirs[id]
	if !ok {
		return "", fmt.Errorf("agent %q not found", id)
	}
	return dir, nil
}

func (f *fakeSource) IsCoreAgent(id string) bool { return f.coreIDs[id] }

// fakeTarget is the ImportTarget edge for Dataset D5.
type fakeTarget struct {
	installedSkills map[string]bool
	knownTools      map[string]bool
	alwaysAsk       map[string]bool
	providers       map[string]bool
	globalMax       int
	seeds           map[string]map[string]string
}

func newFakeTarget() *fakeTarget {
	return &fakeTarget{
		installedSkills: map[string]bool{},
		knownTools:      map[string]bool{},
		alwaysAsk:       map[string]bool{},
		providers:       map[string]bool{},
		globalMax:       1000,
		seeds:           map[string]map[string]string{},
	}
}

func (f *fakeTarget) InstalledSkill(slug string) bool             { return f.installedSkills[slug] }
func (f *fakeTarget) KnownTool(n string) bool                     { return f.knownTools[n] }
func (f *fakeTarget) AlwaysAskTool(n string) bool                 { return f.alwaysAsk[n] }
func (f *fakeTarget) ProviderConfigured(p string) bool            { return f.providers[p] }
func (f *fakeTarget) GlobalMaxToolIterations() int                { return f.globalMax }
func (f *fakeTarget) SeedToolPolicies(t string) map[string]string { return f.seeds[t] }

func warningsOf(plan *omnpkg.AgentPlan, code string) []omnpkg.Warning {
	var out []omnpkg.Warning
	for _, w := range plan.Warnings {
		if w.Code == code {
			out = append(out, w)
		}
	}
	return out
}

func hasWarningItem(ws []omnpkg.Warning, item string) bool {
	for _, w := range ws {
		for _, it := range w.Items {
			if it == item {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Checked JSON-document access (lint: forcetypeassert). Every hop asserts the
// decoded JSON type instead of an unchecked assertion.
// ---------------------------------------------------------------------------

func mustMap(t *testing.T, v any, what string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	require.True(t, ok, "%s: expected a JSON object, got %T", what, v)
	return m
}

func mustSlice(t *testing.T, v any, what string) []any {
	t.Helper()
	s, ok := v.([]any)
	require.True(t, ok, "%s: expected a JSON array, got %T", what, v)
	return s
}

func mustFloat(t *testing.T, v any, what string) float64 {
	t.Helper()
	f, ok := v.(float64)
	require.True(t, ok, "%s: expected a JSON number, got %T", what, v)
	return f
}

// mustPath walks a decoded JSON document and returns the value at path,
// asserting every hop exists and intermediate hops are objects.
func mustPath(t *testing.T, doc map[string]any, path ...string) any {
	t.Helper()
	cur := doc
	for i, key := range path {
		v, ok := cur[key]
		require.True(t, ok, "missing key %q (path %v)", key, path[:i+1])
		if i == len(path)-1 {
			return v
		}
		cur = mustMap(t, v, "key "+key)
	}
	return nil
}
