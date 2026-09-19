// Portable contract-schema validation harness — no build constraint; runs
// on every supported platform (Linux, macOS, Windows).
//
// History: this harness used to live in contract_test.go behind a
// //go:build !windows tag because its file-URL handling was naive string
// concatenation ("file://" + path, decode by TrimPrefix) — correct only for
// POSIX paths free of characters that mean something in a URL. That tag
// cascaded onto every file using these helpers, and left the UNTAGGED users
// of the helpers (adr066/adr068/askuser/…) unable to compile for Windows at
// all: `GOOS=windows go vet ./pkg/api/generated/` failed with "undefined:
// mustPassAsyncAPI" (TEST-008 baseline). The URL handling is now
// encoded with net/url — local paths are percent-encoded on the way out so ' ',
// '#', '%' and non-ASCII survive, and decoded via net/url on the way in,
// mirroring the decode contract of jsonschema/v6's own FileLoader.ToFile —
// so the !windows tags are gone from this package's test files entirely.
//
// Manual break test: temporarily revert fileURL to
// "file://" + filepath.ToSlash(absPath) and/or yamlLoader.Load to
// TrimPrefix decoding, run TestSchemaHarness_* — the tricky-path tests must
// fail; restore, observe them pass.

package generated

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ── Schema loader setup ──────────────────────────────────────────────────────

var (
	schemaSetupOnce sync.Once
	errSchemaSetup  error

	// asyncapiFilePath is the absolute path to contracts/asyncapi.yaml.
	// Used to build file:// URLs for asyncapi schema fragments.
	asyncapiFilePath string

	// componentSchemaDir is the absolute path to contracts/components/schemas/.
	// Used to build file:// URLs for component schema files.
	componentSchemaDir string

	// sharedCompiler is the singleton compiler with all schemas pre-loaded.
	sharedCompiler *jsonschema.Compiler

	// sharedCompilerMu guards concurrent calls to sharedCompiler.Compile.
	// jsonschema/v6's Compiler mutates internal state during Compile (it
	// caches resolved schemas in an unsynchronised map), so calling Compile
	// from multiple goroutines on the same instance is a data race —
	// observed as "fatal error: concurrent map read and map write" on CI
	// runners running TestCompileInboundSchema_ConcurrentDifferentSchemas.
	// We serialize access here; the lock is held only across the Compile
	// call so the cache hit path stays fast.
	sharedCompilerMu sync.Mutex
)

// contractsDir returns the absolute path to the contracts/ directory.
// Resolved relative to the location of this test file (pkg/api/generated/).
func contractsDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("runtime.Caller failed — cannot resolve contracts dir")
	}
	// file is /path/to/pkg/api/generated/schema_harness_test.go
	// contracts/ is three dirs up
	return filepath.Join(filepath.Dir(file), "..", "..", "..", "contracts")
}

// ── Portable path ⇄ file:// URL conversion ──────────────────────────────────

// isSlashDrivePath reports whether slash is a Windows drive-letter path in
// forward-slash form ("C:/…"), i.e. the shape filepath.ToSlash produces on
// Windows for absolute paths. On POSIX a leading "X:/" never occurs for an
// absolute path (those start with '/'), so the check is shape-driven rather
// than GOOS-driven.
func isSlashDrivePath(slash string) bool {
	return len(slash) >= 3 &&
		(slash[0] >= 'a' && slash[0] <= 'z' || slash[0] >= 'A' && slash[0] <= 'Z') &&
		slash[1] == ':' && slash[2] == '/'
}

// pathToFileURLForGOOS converts an absolute filesystem path to a file://
// URL, percent-encoded with net/url: characters that cannot appear literally
// in a URL path are escaped (' ' → %20, '#' → %23 — it would otherwise start the
// fragment component — '%' → %25 — it would otherwise introduce a
// percent-escape — and non-ASCII as its UTF-8 bytes). Windows drive-letter
// paths use the RFC 8089 form file:///C:/… (empty authority, drive letter
// as the first path segment).
//
// goos selects the path syntax of the INPUT (a Windows path uses '\' as its
// separator, and a '\' can never be part of a Windows filename; on POSIX a
// '\' is ordinary filename data and is preserved, escaped as %5C), so the
// Windows branch is unit-testable on any platform.
func pathToFileURLForGOOS(goos, absPath string) string {
	slash := absPath
	if goos == "windows" {
		slash = strings.ReplaceAll(absPath, `\`, "/")
	}
	if isSlashDrivePath(slash) {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}

// fileURLToOSPathForGOOS converts a file:// URL back to a filesystem path,
// mirroring the decode contract of jsonschema/v6's FileLoader.ToFile: parse
// the URL, take the (percent-DECODED) path, and on Windows strip the
// authority slash before the drive letter and restore '\' separators. The
// compiler hands the loader URLs it has re-serialized through
// ResolveReference().String(), so decoding by string surgery instead of
// net/url is what used to corrupt paths containing percent-escaped bytes.
//
// Non-file schemes and URLs carrying a host (UNC, file://server/…) are
// rejected loudly: this harness only ever produces local-file URLs, and a
// silently-wrong path is worse than an error.
func fileURLToOSPathForGOOS(goos, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("fileURLToOSPath: parse %q: %w", rawURL, err)
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("fileURLToOSPath: expected a file:// URL, got %q", rawURL)
	}
	if u.Host != "" {
		return "", fmt.Errorf(
			"fileURLToOSPath: URL %q carries host %q — UNC paths are not produced by this harness",
			rawURL, u.Host)
	}
	path := u.Path // percent-decoded
	if goos == "windows" {
		// filepath.FromSlash keys on runtime.GOOS (a no-op on POSIX), which
		// would break the goos-parameterized round-trip when these tests run
		// on darwin — so the '\' restoration is spelled out on the parameter.
		path = strings.ReplaceAll(strings.TrimPrefix(path, "/"), "/", `\`)
	}
	return path, nil
}

// fileURL converts an absolute file path to a file:// URL string.
func fileURL(absPath string) string {
	return pathToFileURLForGOOS(runtime.GOOS, absPath)
}

// ── YAML-capable loader ──────────────────────────────────────────────────────

// yamlLoader is a URLLoader that reads .yaml files by parsing them with yaml.v3.
// The jsonschema/v6 library's built-in FileLoader only handles JSON; this wrapper
// intercepts file:// URLs and returns parsed YAML as map[string]any.
type yamlLoader struct{}

func (yamlLoader) Load(rawURL string) (any, error) {
	path, err := fileURLToOSPathForGOOS(runtime.GOOS, rawURL)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("yamlLoader: read %s: %w", path, err)
	}

	// Try JSON first (some .gen.go files embed JSON); fall back to YAML.
	if len(data) > 0 && data[0] == '{' {
		var doc any
		if jsonErr := json.Unmarshal(data, &doc); jsonErr == nil {
			return doc, nil
		}
	}

	// Parse as YAML.
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("yamlLoader: unmarshal %s: %w", path, err)
	}
	return doc, nil
}

// initSchemas initializes the shared compiler once per test binary run.
// Called lazily from validateAgainstSchema — not from TestMain so tests can
// run individually without requiring the full environment.
func initSchemas(t *testing.T) *jsonschema.Compiler {
	t.Helper()

	schemaSetupOnce.Do(func() {
		cdir := contractsDir()
		asyncapiFilePath = filepath.Join(cdir, "asyncapi.yaml")
		componentSchemaDir = filepath.Join(cdir, "components", "schemas")

		// Verify the contracts directory is accessible before building the compiler.
		if _, statErr := os.Stat(asyncapiFilePath); statErr != nil {
			errSchemaSetup = fmt.Errorf("contracts/asyncapi.yaml not found at %s: %w", asyncapiFilePath, statErr)
			return
		}
		if _, statErr := os.Stat(componentSchemaDir); statErr != nil {
			errSchemaSetup = fmt.Errorf(
				"contracts/components/schemas/ not found at %s: %w",
				componentSchemaDir, statErr,
			)
			return
		}

		c := jsonschema.NewCompiler()

		// Use our YAML-capable loader for file:// URLs.
		c.UseLoader(jsonschema.SchemeURLLoader{
			"file": yamlLoader{},
		})

		sharedCompiler = c
	})

	require.NoError(t, errSchemaSetup, "schema compiler setup failed")
	return sharedCompiler
}

// validateAgainstAsyncAPISchema validates v against a named schema from asyncapi.yaml.
// schemaName is the key under components.schemas in asyncapi.yaml
// (e.g. "ToolApprovalRequiredFrame", "DoneFrame").
func validateAgainstAsyncAPISchema(t *testing.T, schemaName string, v any) error {
	t.Helper()
	c := initSchemas(t)

	raw, err := json.Marshal(v)
	require.NoError(t, err, "json.Marshal failed for fixture")

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc), "json.Unmarshal of marshaled fixture failed")

	// Compile the schema as a fragment of the asyncapi.yaml document.
	// Fragment path: /components/schemas/<schemaName>
	// URL encodes as: file:///path/to/asyncapi.yaml#/components/schemas/SchemaName
	fragment := "/components/schemas/" + schemaName
	url := fileURL(asyncapiFilePath) + "#" + fragment

	sharedCompilerMu.Lock()
	sch, err := c.Compile(url)
	sharedCompilerMu.Unlock()
	require.NoError(t, err, "could not compile asyncapi schema %q", schemaName)

	return sch.Validate(doc)
}

// validateAgainstComponentSchema validates v against a named component schema file.
// schemaName is the filename without .yaml extension
// (e.g. "Session", "LoginResponse", "ToolApprovalRequiredFrame").
func validateAgainstComponentSchema(t *testing.T, schemaName string, v any) error {
	t.Helper()
	c := initSchemas(t)

	raw, err := json.Marshal(v)
	require.NoError(t, err, "json.Marshal failed for fixture")

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc), "json.Unmarshal of marshaled fixture failed")

	schemaPath := filepath.Join(componentSchemaDir, schemaName+".yaml")
	url := fileURL(schemaPath)

	sharedCompilerMu.Lock()
	sch, err := c.Compile(url)
	sharedCompilerMu.Unlock()
	require.NoError(t, err, "could not compile component schema %q from %s", schemaName, schemaPath)

	return sch.Validate(doc)
}

// validateAgainstComponentSchemaRawJSON validates pre-marshaled JSON bytes against a component schema.
func validateAgainstComponentSchemaRawJSON(t *testing.T, schemaName string, raw []byte) error {
	t.Helper()
	c := initSchemas(t)

	var doc any
	require.NoError(t, json.Unmarshal(raw, &doc))

	schemaPath := filepath.Join(componentSchemaDir, schemaName+".yaml")
	url := fileURL(schemaPath)

	sharedCompilerMu.Lock()
	sch, err := c.Compile(url)
	sharedCompilerMu.Unlock()
	require.NoError(t, err, "could not compile component schema %q", schemaName)

	return sch.Validate(doc)
}

// ── Helper assertions ────────────────────────────────────────────────────────

// mustPassComponent asserts the fixture validates against a component schema file.
func mustPassComponent(t *testing.T, schemaName string, fixture any) {
	t.Helper()
	err := validateAgainstComponentSchema(t, schemaName, fixture)
	assert.NoError(t, err, "fixture must validate against component schema %q", schemaName)
}

// mustFailComponent asserts the fixture produces schema-INVALID JSON.
func mustFailComponent(t *testing.T, schemaName string, fixture any, reason string) {
	t.Helper()
	err := validateAgainstComponentSchema(t, schemaName, fixture)
	assert.Error(t, err, "expected validation error for component schema %q — %s", schemaName, reason)
}

// mustPassAsyncAPI asserts the fixture validates against an asyncapi schema.
func mustPassAsyncAPI(t *testing.T, schemaName string, fixture any) {
	t.Helper()
	err := validateAgainstAsyncAPISchema(t, schemaName, fixture)
	assert.NoError(t, err, "fixture must validate against asyncapi schema %q", schemaName)
}

// mustFailAsyncAPI asserts the fixture produces schema-INVALID JSON.
func mustFailAsyncAPI(t *testing.T, schemaName string, fixture any, reason string) {
	t.Helper()
	err := validateAgainstAsyncAPISchema(t, schemaName, fixture)
	assert.Error(t, err, "expected validation error for asyncapi schema %q — %s", schemaName, reason)
}

// ── Concurrent compile race test ──────────────────────────────────────────────
// Traces to: Phase 7 fix-Y — concurrent schema compilation must be race-free

func TestCompileInboundSchema_ConcurrentDifferentSchemas(t *testing.T) {
	// This test must be run with -race to detect data races in the schema compiler cache.
	// Traces to: pkg/gateway/rest_inbound_validate.go — compileInboundSchema with sync.Map cache.
	t.Parallel()

	// 10 different schema names to compile concurrently.
	schemas := []string{
		"AgentCreateRequestMain", "AgentUpdateRequest", "SessionCreateRequest",
		"ProbeProviderRequest", "SandboxConfigUpdate", "ExecAllowlist",
		"SessionScopeRequest", "AuditLogToggleRequest", "SkillTrustUpdateRequest",
		"PromptGuardUpdateRequest",
	}

	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(schemas))

	for _, name := range schemas {
		n := name
		go func() {
			// Always call initSchemas first — sync.Once serializes the write
			// to componentSchemaDir + sharedCompiler. Reading the global var
			// directly (the previous "skip init if non-empty" optimisation)
			// races with the in-flight Once.Do on the first call, producing
			// "fatal error: concurrent map read and map write" under -race
			// when many goroutines hit this path on a cold cache.
			_ = initSchemas(t)
			raw := []byte(`{"name":"test"}`)
			err := validateAgainstComponentSchemaRawJSON(t, n, raw)
			// We expect validation to either pass or fail — no panic or race.
			// The nil-vs-error outcome depends on the schema, but the important
			// thing is no data race occurs.
			results <- result{name: n, err: err}
		}()
	}

	for range schemas {
		r := <-results
		// Each schema must compile without panicking (err may be non-nil for invalid fixture data).
		t.Logf("schema %s: validate result=%v", r.name, r.err != nil)
	}
}

// ── Portability of the path ⇄ URL conversions (TEST-008) ────────────────────

// TestSchemaHarness_FileURLEncoding pins the file:// URL forms this harness
// produces. The expected strings are derived from the standards, not from
// running this code: RFC 3986 §2.1 — special filename characters are percent-encoded, so ' ' → %20, '#' → %23 (a raw '#' would otherwise
// start the fragment component), '%' → %25 (a raw '%' would otherwise
// introduce a percent-escape), and non-ASCII encodes as its UTF-8 bytes
// (ü = 0xC3 0xBC → %C3%BC); RFC 8089 §2 — a Windows drive-letter path is
// file:///C:/… (empty authority, drive letter after the authority slash).
// On POSIX a '\' is ordinary filename data, preserved as %5C.
//
// Every row also round-trips: decoding the produced URL must return the
// exact input path.
func TestSchemaHarness_FileURLEncoding(t *testing.T) {
	cases := []struct {
		name string
		goos string
		path string
		want string
	}{
		{"space", "linux", "/a b/c.yaml", "file:///a%20b/c.yaml"},
		{"hash", "linux", "/a#b/c.yaml", "file:///a%23b/c.yaml"},
		{"percent", "linux", "/a%b/c.yaml", "file:///a%25b/c.yaml"},
		{"unicode", "linux", "/aüb/c.yaml", "file:///a%C3%BCb/c.yaml"},
		{
			"all combined", "linux",
			"/all tog ether #50% ü.yaml",
			"file:///all%20tog%20ether%20%2350%25%20%C3%BC.yaml",
		},
		{"posix backslash is data", "linux", `/tmp/a\b/c.yaml`, "file:///tmp/a%5Cb/c.yaml"},
		{"darwin temp dir", "darwin", "/Users/dan iel/ünïcode.yaml", "file:///Users/dan%20iel/%C3%BCn%C3%AFcode.yaml"},
		{"windows drive with hazards", "windows", `C:\Foo Bar\x#y%.yaml`, "file:///C:/Foo%20Bar/x%23y%25.yaml"},
		{"windows drive plain", "windows", `C:\omnipus\contracts.yaml`, "file:///C:/omnipus/contracts.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pathToFileURLForGOOS(tc.goos, tc.path)
			require.Equal(t, tc.want, got, "file:// URL for %q", tc.path)

			roundTripped, err := fileURLToOSPathForGOOS(tc.goos, got)
			require.NoError(t, err)
			require.Equal(t, tc.path, roundTripped, "decoded path must equal the original input")
		})
	}
}

// TestSchemaHarness_FileURLToOSPathRejects pins the loud-failure contract of
// the decode side: anything this harness never produces (foreign schemes,
// URLs with a host — UNC paths — and malformed percent-escapes) must error,
// not silently resolve to a wrong file.
func TestSchemaHarness_FileURLToOSPathRejects(t *testing.T) {
	cases := []struct {
		name   string
		rawURL string
	}{
		{"non-file scheme", "https://example.com/schema.yaml"},
		{"host present means UNC", "file://server/share/schema.yaml"},
		{"invalid percent escape", "file:///a%zz.yaml"},
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		for _, tc := range cases {
			t.Run(goos+"/"+tc.name, func(t *testing.T) {
				_, err := fileURLToOSPathForGOOS(goos, tc.rawURL)
				require.Error(t, err, "decode must fail loudly for %q", tc.rawURL)
			})
		}
	}
}

// TestSchemaHarness_CompilerLoadsSchemasFromTrickyPaths drives the REAL
// jsonschema/v6 compiler end to end over a temp directory whose names
// contain every character class the old string-concatenation loader
// corrupted: space, '#', '%', and a non-ASCII rune. The schema pair uses a
// relative $ref, so the library's own ref resolution — ResolveReference →
// String(), which re-serializes the URL with escaping — sits between the
// two loads: the exact path where a raw ' ' comes back to the loader as
// %20 and a raw '#' would truncate the URL into a fragment.
//
// Oracle independence: the pattern constraint exists only in the linked
// (second) file, so the three verdicts below each prove a distinct fact —
// the valid doc proves both files loaded AND the ref was followed; the
// non-hex doc proves the linked pattern is enforced (a skipped ref would
// wrongly accept it); the extraneous-property doc proves the main schema's
// additionalProperties:false is enforced.
func TestSchemaHarness_CompilerLoadsSchemasFromTrickyPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "space dir #frag %pct ümlaut")
	require.NoError(t, os.MkdirAll(dir, 0o755))

	mainPath := filepath.Join(dir, "main #50%.yaml")
	linkedPath := filepath.Join(dir, "linked #50%.yaml")

	// RFC 3986 relative reference: '#' inside an unencoded reference would
	// be read as the fragment delimiter, so the target filename must be
	// percent-encoded in the ref. Built with url.URL rather than typed by
	// hand — a hand-typed escape is exactly how this fixture first shipped
	// wrong (%20%23%2050%25 decodes to "linked # 50%.yaml", one space away
	// from the file on disk).
	linkedRef := (&url.URL{Path: filepath.Base(linkedPath)}).String()

	main := "type: object\n" +
		"required: [revision]\n" +
		"additionalProperties: false\n" +
		"properties:\n" +
		"  revision:\n" +
		"    $ref: \"" + linkedRef + "\"\n"
	linked := `type: string
pattern: "^[a-f0-9]{64}$"
`
	require.NoError(t, os.WriteFile(mainPath, []byte(main), 0o644), "writing main schema")
	require.NoError(t, os.WriteFile(linkedPath, []byte(linked), 0o644), "writing linked schema")

	c := jsonschema.NewCompiler()
	c.UseLoader(jsonschema.SchemeURLLoader{"file": yamlLoader{}})
	sch, err := c.Compile(fileURL(mainPath))
	require.NoError(t, err,
		"compiler must load a schema and its $ref from a path containing space, #, %% and unicode")

	require.NoError(t, sch.Validate(map[string]any{"revision": strings.Repeat("ab", 32)}),
		"a 64-hex-char revision must validate — proves both files loaded and the $ref was followed")
	require.Error(t, sch.Validate(map[string]any{"revision": "not-hex"}),
		"the linked file's pattern must be enforced — a skipped or mis-decoded $ref would wrongly accept this")
	require.Error(t, sch.Validate(map[string]any{"revision": strings.Repeat("ab", 32), "extra": true}),
		"additionalProperties:false from the main schema must be enforced — proves the main schema was really loaded")
}
