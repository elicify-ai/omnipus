package config

import (
	"fmt"
	"runtime"
)

// Build-time variables injected via ldflags during build process.
// These are set by the Makefile or .goreleaser.yaml using the -X flag:
//
//	-X github.com/elicify-ai/omnipus/pkg/config.Version=<version>
//	-X github.com/elicify-ai/omnipus/pkg/config.GitCommit=<commit>
//	-X github.com/elicify-ai/omnipus/pkg/config.BuildTime=<timestamp>
//	-X github.com/elicify-ai/omnipus/pkg/config.GoVersion=<go-version>
var (
	Version   = "dev" // Default value when not built with ldflags
	GitCommit string  // Git commit SHA (short)
	BuildTime string  // Build timestamp in RFC3339 format
	GoVersion string  // Go version used for building
)

// FormatVersion returns the version string with optional git commit and the
// stamped edition (pkg/config/edition.go, ADR-0010) — e.g.
// "1.2.3 (git: abc) edition: hosted". Edition defaults to "core" in source,
// so this always prints something even for an unstamped build; that default,
// plus the build-time assertions in the engine and root Makefiles and in
// hosted/Dockerfile, is what makes a forgotten `-X …Edition=hosted` flag
// visible instead of silent (docs/plans/engine-divergence-workplan-review-2.md
// finding N2).
func FormatVersion() string {
	v := Version
	if GitCommit != "" {
		v += fmt.Sprintf(" (git: %s)", GitCommit)
	}
	v += fmt.Sprintf(" edition: %s", Edition)
	return v
}

// FormatBuildInfo returns build time and go version info
func FormatBuildInfo() (string, string) {
	build := BuildTime
	goVer := GoVersion
	if goVer == "" {
		goVer = runtime.Version()
	}
	return build, goVer
}

// GetVersion returns the version string
func GetVersion() string {
	return Version
}
