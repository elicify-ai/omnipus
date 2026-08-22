package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// computeTestZipSHA256 returns the lowercase hex SHA-256 of the given bytes.
func computeTestZipSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// TestSHA256HashVerification verifies that a correct SHA-256 hash passes verification
// and result.Verified is set to true.
// Traces to: wave3-skill-ecosystem-spec.md line 831 (Test #1: TestSHA256HashVerification)
// BDD: Given skill `aws-cost-analyzer` with manifest hash sha256:abc123def456,
// When the downloaded ZIP is verified,
// Then the SHA-256 hash of the ZIP matches the manifest → result.Verified = true.

func TestSHA256HashVerification(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 888 (Dataset: SHA-256 Hash Verification row 1)
	zipBuf := createTestZip(t, map[string]string{
		"SKILL.md": "---\nname: aws-cost-analyzer\ndescription: Analyze AWS costs\n---\nHello",
	})
	expectedHash := computeTestZipSHA256(zipBuf)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/aws-cost-analyzer":
			json.NewEncoder(w).Encode(clawhubSkillResponse{
				Slug:        "aws-cost-analyzer",
				DisplayName: "AWS Cost Analyzer",
				Summary:     "Analyze AWS costs",
				LatestVersion: &clawhubVersionInfo{
					Version: "1.0.0",
					SHA256:  expectedHash, // correct hash
				},
			})
		case "/api/v1/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zipBuf)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	targetDir := t.TempDir()
	reg := newTestRegistry(srv.URL, "")
	result, err := reg.DownloadAndInstall(context.Background(), "aws-cost-analyzer", "1.0.0", targetDir)

	require.NoError(t, err, "install with matching hash must succeed")
	assert.True(t, result.Verified, "result.Verified must be true when hash matches manifest")
	assert.Equal(t, "1.0.0", result.Version)
}

// TestHashMismatchDetection verifies that a tampered skill ZIP is detected and blocked.
// Traces to: wave3-skill-ecosystem-spec.md line 832 (Test #2: TestHashMismatchDetection)
// BDD: Given skill `bad-skill` with manifest hash sha256:expected123,
// And downloaded ZIP has actual hash sha256:actual456,
// When install completes download,
// Then skill is NOT installed, error returned containing "hash verification failed".

func TestHashMismatchDetection(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 396 (Scenario: Install fails on hash mismatch)
	zipBuf := createTestZip(t, map[string]string{
		"SKILL.md": "---\nname: bad-skill\ndescription: Tampered skill\n---\nTampered",
	})
	// Use a wrong (all-zeros) hash — will not match actual ZIP SHA-256
	wrongHash := "0000000000000000000000000000000000000000000000000000000000000000"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/bad-skill":
			json.NewEncoder(w).Encode(clawhubSkillResponse{
				Slug:        "bad-skill",
				DisplayName: "Bad Skill",
				Summary:     "Tampered",
				LatestVersion: &clawhubVersionInfo{
					Version: "1.0.0",
					SHA256:  wrongHash, // wrong hash — mismatch
				},
			})
		case "/api/v1/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zipBuf)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	targetDir := t.TempDir()
	reg := newTestRegistry(srv.URL, "")
	result, err := reg.DownloadAndInstall(context.Background(), "bad-skill", "1.0.0", targetDir)

	require.Error(t, err, "install with mismatched hash must fail")
	assert.Contains(t, err.Error(), "hash verification failed",
		"error must describe hash verification failure")
	assert.Nil(t, result, "no InstallResult must be returned on hash mismatch")
}

// TestTrustPolicyBlockUnverified verifies that InstallResult.Verified=false
// signals callers to enforce block_unverified policy (SEC-09).
// Traces to: wave3-skill-ecosystem-spec.md line 838 (Test #8: TestTrustPolicyBlockUnverified)
// BDD Scenario Outline: Install with different trust policies
// policy=block_unverified, can_verify=cannot → install blocked, audit logged.
// NOTE: The install pipeline itself returns result.Verified=false when no hash is available.
// The trust policy blocking is enforced by the caller using config.SkillTrustLevel
// (pkg/config/sandbox.go), wired via pkg/gateway/rest_skill_trust.go.

func TestTrustPolicyBlockUnverified(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 447 (Scenario Outline: Install with different trust policies)
	// Install a skill where metadata has no hash → result.Verified = false
	zipBuf := createTestZip(t, map[string]string{
		"SKILL.md": "---\nname: unverified-skill\ndescription: No hash available\n---\nContent",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/unverified-skill":
			json.NewEncoder(w).Encode(clawhubSkillResponse{
				Slug:        "unverified-skill",
				DisplayName: "Unverified Skill",
				Summary:     "No hash in manifest",
				LatestVersion: &clawhubVersionInfo{
					Version: "1.0.0",
					SHA256:  "", // no hash → cannot verify → result.Verified=false
				},
			})
		case "/api/v1/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zipBuf)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	targetDir := t.TempDir()
	reg := newTestRegistry(srv.URL, "")
	result, err := reg.DownloadAndInstall(context.Background(), "unverified-skill", "1.0.0", targetDir)

	require.NoError(t, err)
	assert.False(t, result.Verified,
		"result.Verified must be false when manifest provides no hash (caller must enforce block_unverified policy)")
	// The caller (CLI / tool) must check: if !result.Verified && policy == block_unverified → block
}

// runNoHashInstall installs a skill with no SHA256 hash available and returns
// the result. The skill is served by a local httptest server. This helper
// eliminates structural duplication between TestTrustPolicyWarnUnverified and
// TestTrustPolicyAllowAll — both exercise the same pipeline path; only the
// caller's handling of result.Verified differs.
func runNoHashInstall(t *testing.T, slug, displayName, skillMDContent string) (*InstallResult, error) {
	t.Helper()
	zipBuf := createTestZip(t, map[string]string{"SKILL.md": skillMDContent})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/" + slug:
			json.NewEncoder(w).Encode(clawhubSkillResponse{ // errcheck rationale (out of errcheck scope; kept as documentation): test HTTP handler response write; the test server discards write errors, only response content matters for the assertions
				Slug:          slug,
				DisplayName:   displayName,
				Summary:       displayName,
				LatestVersion: &clawhubVersionInfo{Version: "1.0.0", SHA256: ""},
			})
		case "/api/v1/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zipBuf) // errcheck rationale (out of errcheck scope; kept as documentation): test HTTP handler response write; the test server discards write errors, only response content matters for the assertions
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	targetDir := t.TempDir()
	reg := newTestRegistry(srv.URL, "")
	return reg.DownloadAndInstall(context.Background(), slug, "1.0.0", targetDir)
}

// TestTrustPolicyWarnUnverified verifies that when no hash is provided,
// result.Verified=false signals warn_unverified to warn-but-proceed.
// Traces to: wave3-skill-ecosystem-spec.md line 839 (Test #9: TestTrustPolicyWarnUnverified)
// BDD Scenario Outline: policy=warn_unverified, can_verify=cannot → warning, install proceeds.
func TestTrustPolicyWarnUnverified(t *testing.T) {
	// Same as block scenario: Verified=false is the signal; warn_unverified proceeds (no error from pipeline)
	result, err := runNoHashInstall(t, "warn-skill", "Warn Skill",
		"---\nname: warn-skill\ndescription: Warn on unverified\n---\nContent")
	// warn_unverified: pipeline succeeds (no error), caller warns
	require.NoError(t, err, "warn_unverified: pipeline must not block — caller decides")
	assert.False(t, result.Verified,
		"result.Verified=false signals caller to emit a warning before proceeding")
}

// TestTrustPolicyAllowAll verifies that when allow_all policy is in effect,
// install proceeds even without hash — result.Verified stays false, no warning emitted.
// Traces to: wave3-skill-ecosystem-spec.md line 840 (Test #10: TestTrustPolicyAllowAll)
// BDD Scenario Outline: policy=allow_all, can_verify=cannot → install proceeds silently.
func TestTrustPolicyAllowAll(t *testing.T) {
	// allow_all: same pipeline behavior as warn, but caller skips warning too.
	result, err := runNoHashInstall(t, "allow-skill", "Allow Skill",
		"---\nname: allow-skill\ndescription: Allow all\n---\nContent")
	require.NoError(t, err, "allow_all: pipeline must proceed silently when no hash available")
	assert.False(t, result.Verified,
		"result.Verified stays false — allow_all caller skips both warning and blocking")
}

// TestClawHubInstallHashMismatchIntegration verifies the full install pipeline
// rejects a skill when the downloaded ZIP hash does not match the manifest.
// Traces to: wave3-skill-ecosystem-spec.md line 855 (Test #25: TestClawHubInstallHashMismatchIntegration)
// BDD: Given mock server returns ZIP + mismatched hash, When install runs, Then error returned.

func TestClawHubInstallHashMismatchIntegration(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 396 (Scenario: Install fails on hash mismatch)
	// Integration: mock server serves correct ZIP but wrong hash in metadata.
	zipBuf := createTestZip(t, map[string]string{
		"SKILL.md": "---\nname: tampered-skill\ndescription: Tampered\n---\nContent",
	})
	// Compute the real hash, then corrupt it
	correctHash := computeTestZipSHA256(zipBuf)
	// Flip the first char to create a mismatch
	corruptedHash := "f" + correctHash[1:]
	if corruptedHash == correctHash {
		corruptedHash = "0" + correctHash[1:]
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/tampered-skill":
			json.NewEncoder(w).Encode(clawhubSkillResponse{
				Slug:          "tampered-skill",
				DisplayName:   "Tampered Skill",
				LatestVersion: &clawhubVersionInfo{Version: "1.0.0", SHA256: corruptedHash},
			})
		case "/api/v1/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zipBuf)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	targetDir := t.TempDir()
	reg := newTestRegistry(srv.URL, "")
	_, err := reg.DownloadAndInstall(context.Background(), "tampered-skill", "1.0.0", targetDir)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "hash verification failed")
	// Verify temp extraction did NOT happen (skill not installed)
	entries, readErr := filepath.Glob(filepath.Join(targetDir, "*"))
	assert.NoError(t, readErr)
	assert.Empty(t, entries, "tampered skill must not be extracted to targetDir")
}

// TestClawHubMalwareBlockIntegration verifies that malware-flagged skills carry
// IsMalwareBlocked=true in the InstallResult.
// Traces to: wave3-skill-ecosystem-spec.md line 856 (Test #26: TestClawHubMalwareBlockIntegration)
// BDD: Given skill flagged isMalwareBlocked: true, When install attempted,
// Then InstallResult.IsMalwareBlocked=true (caller must enforce blocking).

func TestClawHubMalwareBlockIntegration(t *testing.T) {
	// Traces to: wave3-skill-ecosystem-spec.md line 856 (Test #26)
	// The install pipeline surfaces IsMalwareBlocked=true from moderation metadata.
	// Blocking logic lives in the caller (CLI / tool), not in DownloadAndInstall itself.
	zipBuf := createTestZip(t, map[string]string{
		"SKILL.md": "---\nname: malware-skill\ndescription: Malware\n---\nEvil content",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/malware-skill":
			json.NewEncoder(w).Encode(clawhubSkillResponse{
				Slug:          "malware-skill",
				DisplayName:   "Malware Skill",
				LatestVersion: &clawhubVersionInfo{Version: "1.0.0"},
				Moderation: &clawhubModerationInfo{
					IsMalwareBlocked: true,
					IsSuspicious:     true,
				},
			})
		case "/api/v1/download":
			w.Header().Set("Content-Type", "application/zip")
			w.Write(zipBuf)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	targetDir := t.TempDir()
	reg := newTestRegistry(srv.URL, "")
	result, err := reg.DownloadAndInstall(context.Background(), "malware-skill", "1.0.0", targetDir)

	// The pipeline itself does NOT block — it surfaces the flag for the caller.
	// Caller (CLI / tool) must check result.IsMalwareBlocked before using the skill.
	require.NoError(t, err, "pipeline returns result even for malware-flagged skills — caller blocks")
	require.NotNil(t, result)
	assert.True(t, result.IsMalwareBlocked,
		"IsMalwareBlocked must be true when moderation flags the skill")
	assert.True(t, result.IsSuspicious,
		"IsSuspicious must be true when moderation flags the skill as suspicious")
}
