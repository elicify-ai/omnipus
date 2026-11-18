package agent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// Issue #1626 / T2: MCP reconciliation is also on Run's startup path. Its
// ingest-bound config read must be synchronized with config publication.
// Start the publisher at the credential-store boundary AFTER hook loading and
// the protected MCP snapshots, so hook races cannot make this test falsely red.
func TestAgentLoop_RunMCPStartupDoesNotRaceConfigPublication(t *testing.T) {
	for _, method := range []string{"MutateConfig", "SwapConfig"} {
		t.Run(method, func(t *testing.T) {
			al := newConfiguredHookLoop(t, &llmHookTestProvider{}, config.HooksConfig{})
			defer al.Close()
			al.bus.Close()
			cfg, err := al.GetConfig().Clone()
			if err != nil {
				t.Fatalf("prepare MCP startup config: %v", err)
			}
			// Deliberately absent executable: reach real MCP setup without an
			// installed helper, a model request, or an external service.
			const serverName = "issue-1626-mcp-startup"
			const credentialRef = "issue-1626-fixture-ref"
			const ingestBound = 64 * 1024 // Fixture input preserved by every publisher.
			cfg.Context.IngestBoundBytes = ingestBound
			cfg.Tools.MCP.Enabled = true
			cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
				serverName: {
					Enabled: true,
					Command: filepath.Join(t.TempDir(), "absent-mcp-server"),
					EnvRefs: map[string]string{"ISSUE_1626_FIXTURE": credentialRef},
				},
			}
			al.SwapConfig(cfg)
			publish := configPublisher1626(t, al, method)
			ready := make(chan struct{})
			stopWriter := make(chan struct{})
			writerDone := make(chan configPublicationResult1626, 1)
			resolverCalls := 0
			resolvedRef := ""
			al.SetCredentialResolver(func(ref string) (string, error) {
				resolverCalls++
				resolvedRef = ref
				if resolverCalls == 1 {
					go publishConfigUntilStopped1626(publish, ready, stopWriter, writerDone)
					<-ready
				}
				// Synthetic fixture data, never a real credential/store read.
				return "not-a-secret-issue-1626-fixture", nil
			})

			runDone := make(chan error, 1)
			go func() { runDone <- al.Run(context.Background()) }()
			runErr := <-runDone
			if resolverCalls == 0 {
				t.Fatalf("Run never reached the MCP credential boundary (Run error: %v)", runErr)
			}
			close(stopWriter)
			result := <-writerDone // Join before closing any loop resources.

			if runErr != nil {
				t.Fatalf("Run MCP startup: %v", runErr)
			}
			if result.err != nil {
				t.Fatalf("%s during MCP startup: %v", method, result.err)
			}
			if result.writes < minimumConfigPublications1626 {
				t.Fatalf("config publications = %d, want at least %d", result.writes, minimumConfigPublications1626)
			}
			if resolverCalls != 1 || resolvedRef != credentialRef {
				t.Fatalf("MCP credential lookups = %d for %q, want exactly 1 for %q", resolverCalls, resolvedRef, credentialRef)
			}
			if !al.mcp.isInitialized() || al.mcp.getManager() == nil {
				t.Fatal("Run did not complete the real MCP manager initialization path")
			}
			if got := al.GetConfig().Context.IngestBoundBytes; got != ingestBound {
				t.Fatalf("published ingest bound = %d, want unchanged fixture value %d", got, ingestBound)
			}
			t.Logf("Run reached MCP manager initialization; joined %s writer after %d publications", method, result.writes)
		})
	}
}
