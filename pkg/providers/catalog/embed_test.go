package catalog

// T067-06 — the first real embedded snapshot (spec §7 TDD Plan):
//
//	T16 TestEmbeddedSnapshot_Valid_And_Bounded    — parses under FR-002, ≤ 8 MB [A-2],
//	                                                tier set exact, byte-for-byte with
//	                                                the committed file (US-2.AC5)
//	T17 TestEmbeddedSnapshot_PopularTier          — popular set exact (FR-018, US-8.AC1)
//	T18 TestEmbeddedSnapshot_LocalProvidersPresent — 11 local-file ids (US-2.AC4); no
//	                                                custom row; cli_kind on every cli row
//	                                                (FR-026)
//	T19 TestEmbeddedSnapshot_UnsupportedHaveReason — every unsupported row carries a
//	                                                reason; amazon-bedrock = cloud-iam,
//	                                                azure = deployment-url (US-8.AC2)
//
// The snapshot under test is the committed pkg/providers/catalog/data/
// providers_catalog.json — the assembly repository's release document
// (FR-006), never a fixture.

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// maxEmbeddedSnapshotBytes is the FR-026 / A-2 bound on the committed file.
const maxEmbeddedSnapshotBytes = 8 << 20

// parseEmbedded parses the embedded snapshot once per test that needs the
// document, failing the test on any FR-002 violation.
func parseEmbedded(t *testing.T) *Document {
	t.Helper()
	doc, err := ParseDocument(EmbeddedSnapshot)
	if err != nil {
		t.Fatalf("embedded snapshot does not parse under FR-002: %v", err)
	}
	return doc
}

func TestEmbeddedSnapshot_Valid_And_Bounded(t *testing.T) {
	if n := len(EmbeddedSnapshot); n == 0 {
		t.Fatal("embedded snapshot is empty")
	} else if n > maxEmbeddedSnapshotBytes {
		t.Fatalf("embedded snapshot is %d bytes, above the 8 MB bound [A-2]", n)
	}

	doc := parseEmbedded(t)

	// Hermetic-build half of US-2.AC5: the binary's embedded catalog equals
	// the committed file byte-for-byte.
	committed, err := os.ReadFile("data/providers_catalog.json")
	if err != nil {
		t.Fatalf("read committed snapshot: %v", err)
	}
	if !bytes.Equal(EmbeddedSnapshot, committed) {
		t.Fatal("EmbeddedSnapshot differs from the committed data/providers_catalog.json")
	}

	// Tier set exact: every row carries one of the three tiers and all
	// three occur in a real release (FR-018 — tiers are data).
	seen := map[Tier]bool{}
	for _, p := range doc.Providers {
		switch p.Tier {
		case TierPopular, TierStandard, TierUnsupported:
			seen[p.Tier] = true
		default:
			t.Fatalf("provider %q has tier %q outside popular|standard|unsupported", p.ID, p.Tier)
		}
	}
	for _, want := range []Tier{TierPopular, TierStandard, TierUnsupported} {
		if !seen[want] {
			t.Errorf("no provider with tier %q in the snapshot", want)
		}
	}
}

func TestEmbeddedSnapshot_PopularTier(t *testing.T) {
	doc := parseEmbedded(t)

	// FR-018 [A-9]: the popular set is pinned by name. Twelve, usage-backed
	// (catalog repo commit b50f5a6): groq demoted to standard (an inference
	// host, not a model author, so it has no author-usage ranking); ollama
	// promoted (local-model support, on brand for a self-hosted product).
	want := []string{
		"alibaba", "anthropic", "deepseek", "google", "minimax", "mistral",
		"moonshotai", "ollama", "openai", "openrouter", "xai", "zai",
	}

	var got []string
	for _, p := range doc.Providers {
		if p.Tier == TierPopular {
			got = append(got, p.ID)
		}
	}
	sort.Strings(got)

	if len(got) != len(want) {
		t.Fatalf("popular tier has %d providers %v, want exactly %v", len(got), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("popular tier is %v, want exactly %v", got, want)
		}
	}
}

func TestEmbeddedSnapshot_LocalProvidersPresent(t *testing.T) {
	doc := parseEmbedded(t)

	byID := map[string]*Provider{}
	for i := range doc.Providers {
		byID[doc.Providers[i].ID] = &doc.Providers[i]
	}

	// US-2.AC4: the local-file providers appear with the registry shape.
	for _, id := range []string{
		"ollama", "vllm", "litellm", "lmstudio", "codex-cli", "openai-chatgpt",
		"github-copilot", "shengsuanyun", "volcengine", "avian", "mimo",
	} {
		p, ok := byID[id]
		if !ok {
			t.Errorf("local-file provider %q missing from the snapshot", id)
			continue
		}
		if p.Protocol == "" {
			t.Errorf("local-file provider %q has no protocol", id)
		}
	}

	for _, p := range doc.Providers {
		// FR-026: the published snapshot never carries a custom row —
		// custom is a factory case, not catalog data (FR-035).
		if p.Custom {
			t.Errorf("provider %q has custom: true in the published snapshot", p.ID)
		}
		// X-14: every protocol cli row names its CLI kind.
		if p.Protocol == ProtocolCLI {
			switch p.CLIKind {
			case CLIKindCodex, CLIKindCopilot:
			default:
				t.Errorf("cli provider %q has cli_kind %q, want codex|copilot", p.ID, p.CLIKind)
			}
		}
	}
}

func TestEmbeddedSnapshot_UnsupportedHaveReason(t *testing.T) {
	doc := parseEmbedded(t)

	reasons := map[string]string{}
	for _, p := range doc.Providers {
		if p.Tier != TierUnsupported {
			continue
		}
		if p.UnsupportedReason == "" {
			t.Errorf("unsupported provider %q has no unsupported_reason", p.ID)
		}
		reasons[p.ID] = p.UnsupportedReason
	}

	// US-8.AC2: cloud-IAM is listed, visible-disabled with its reason.
	if got, ok := reasons["amazon-bedrock"]; !ok {
		t.Error("amazon-bedrock missing from the unsupported tier")
	} else if got != "cloud-iam" {
		t.Errorf("amazon-bedrock unsupported_reason = %q, want cloud-iam", got)
	}
	if got, ok := reasons["azure"]; !ok {
		t.Error("azure missing from the unsupported tier")
	} else if got != "deployment-url" {
		t.Errorf("azure unsupported_reason = %q, want deployment-url", got)
	}
}

// TestSingleCatalogEmbedUnderProviders (T16, FR-005, SC-008) is the
// build-shape assertion behind the capabilities→catalog fold: after the fold
// there is exactly ONE go:embed directive anywhere under pkg/providers, and it
// names the committed catalog snapshot. A second embedded catalog is how the
// old duplication came back the last time — two documents, two sources of
// truth, and no compile error to tell anyone.
func TestSingleCatalogEmbedUnderProviders(t *testing.T) {
	root := ".."
	var directives []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//go:embed") {
				directives = append(directives, path+": "+trimmed)
			}
		}
		return nil
	})
	require.NoError(t, err)

	require.Len(t, directives, 1,
		"exactly one go:embed may exist under pkg/providers; found: %v", directives)
	assert.Contains(t, directives[0], "data/providers_catalog.json",
		"the single embed must name the committed catalog snapshot")
	assert.Contains(t, directives[0], filepath.Join("catalog", "catalog.go"),
		"the single embed must live in the catalog package")
}

// TestCapabilitiesPackageGone (SC-008) pins the deletion itself: the folded
// package must not exist, and the retired prefix-stripping resolver — which
// ADR-067 FR-003 replaced with exact-pair lookup — must not survive anywhere
// under pkg/providers. The banned identifier is assembled at runtime so this
// file is not itself a hit.
func TestCapabilitiesPackageGone(t *testing.T) {
	_, err := os.Stat(filepath.Join("..", "capabilities"))
	assert.True(t, os.IsNotExist(err),
		"pkg/providers/capabilities must not exist (FR-005, SC-008)")

	// Assembled, never written whole: a literal here would make this file its
	// own first hit.
	bannedResolver := "resolve" + "StrippedPrefix"

	var hits []string
	err = filepath.WalkDir("..", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(data), bannedResolver) {
			hits = append(hits, path)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Empty(t, hits, "%s must be gone (FR-025, SC-008)", bannedResolver)
}
