// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package pathsafe

// activeRules is the name-shape rule set for a Windows build: the full
// Windows set, because this binary creates files on a filesystem that
// genuinely enforces those rules.
//
// Selection is by GOOS — this file's _windows suffix — and never by a
// custom build tag. A tag would let the Linux binary be built with Windows
// rules on a filesystem that never needed them, which is precisely the
// behaviour ADR-067 Stage 0 removes. GOOS cannot be wrong about which
// filesystem the binary is going to write to.
var activeRules = WindowsRules
