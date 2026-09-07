// Package knowledge turns a folder of markdown notes into something Omnipus can
// search, navigate and (later) write to — a knowledge base in the Obsidian
// on-disk format, without depending on the Obsidian application.
//
// # What this package is for, in plain language
//
// An operator mounts a folder into a workspace. Some of those folders are just
// folders. Some of them are note collections: they have wikilinks between
// notes, an outline of headings, a set of notes that link back to the one you
// are reading. This package is the part that answers three questions about such
// a folder, and nothing else:
//
//  1. Is this folder a knowledge base at all? (detect.go)
//  2. What is it called, and where are its templates? (marker.go)
//  3. Which folder, exactly, is "this collection"? (detect.go, Collection)
//
// Indexing, search, link resolution and the write path are separate files in
// this same package; this file describes only the identity layer they all sit
// on top of.
//
// # The asymmetry: we read .obsidian/, we never write it
//
// A folder is a knowledge base if its root contains .omnipus-vault/ OR
// .obsidian/ (FR-020). But Omnipus only ever creates .omnipus-vault/, never
// .obsidian/ (FR-022, FR-023). That looks inconsistent, and it is deliberate:
//
//   - Reading .obsidian/ is a compatibility signal. An operator who already
//     keeps notes in Obsidian should be able to mount that folder and have
//     everything work with no conversion step. Its presence tells us "someone
//     treats this folder as a note collection" — that is all we take from it.
//     We never parse Obsidian's settings, and we never rely on them.
//   - Writing .obsidian/ would be Omnipus editing another application's
//     configuration state. That directory belongs to Obsidian; it decides its
//     own layout, its own schema and its own migrations. Creating it — even
//     empty — makes Omnipus a second author of a file format it does not own,
//     which is a maintenance burden we would carry forever and a way to corrupt
//     an operator's setup we would never notice.
//
// So: compatible with Obsidian, not a replacement for it, and never a writer of
// its files. Obsidian creates .obsidian/ itself on first open, so the
// compatibility costs us nothing. See ADR-067 D1.
//
// # One knowledge base is exactly one folder
//
// A Collection is bound to exactly one root directory, resolved through symlinks
// (its "real path"). Attaching a second, different root is refused with a typed
// error naming both (FR-026), and every path that a link, backlink or search hit
// could name must pass through Collection.ResolveInside, which refuses anything
// outside that single root. The reason is not tidiness: two folders merged into
// one collection would let a wikilink in one operator's notes resolve into
// another's, and would make "what links here?" answer with files the reader
// never granted access to.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge
