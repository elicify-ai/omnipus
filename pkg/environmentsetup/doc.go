// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package environmentsetup provides the generic installation storage layer for
// the ADR-090 environment_setup tool: path resolution for installation
// destinations, locking, and atomic additive publication of shared
// installations.
//
// It deliberately contains no installation knowledge. The agent supplies the
// installation command or script; the tool layer runs it under the existing
// approval and sandbox machinery. This package decides only WHERE an
// installation lives and WHEN it becomes visible:
//
//   - Workspace scope writes live inside the reserved subtree
//     <workspace>/.omnipus/env (direct write, no staging).
//   - Shared scope writes live in a pre-allocated immutable generation
//     directory under <dataRoot>/toolchains/environment/shared/. Publication
//     adds the generation to published.json atomically; the tree is never
//     renamed after the script ran, so embedded absolute paths (for example
//     venv shebangs) remain valid. Unrelated installations accumulate — a new
//     publication never hides an older one.
//
// No package names, versions, or any catalogue of applications exists in this
// package. A committed shared generation means the command exited 0 and the
// tree was publishable — it does not mean any installed application works.
package environmentsetup
