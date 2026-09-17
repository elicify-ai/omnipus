// Omnipus — deterministic, key-ordered YAML construction for generated
// schema/view files.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import "gopkg.in/yaml.v3"

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS
//
// records.ParseSchema and records.ParseView both read what this package
// writes, and both care about exact wire shape (a record schema's
// `schema_version` that decodes as a bare int, not a string; a
// `many:`/`required:` that decodes as a bool; a view's `limit` likewise).
// yaml.Marshal of a plain Go map[string]any sorts keys
// alphabetically and loses control over ordering — fine for a machine
// reader, unreadable for the operator who has to open the file afterward
// (`type` after `many` after `label`...).
//
// *yaml.Node.Encode(v) lets a leaf value's tag/kind be inferred correctly by
// the library itself (an int stays !!int, a bool stays !!bool) while this
// file controls the ORDER of a mapping's keys directly, by building
// MappingNode.Content as an explicit key/value sequence rather than letting
// the encoder walk a map. Verified against records.ParseSchema/ParseView's
// actual decode path in pkg/vaultimport/yamlnode_roundtrip_test.go.
// ---------------------------------------------------------------------------

// ordPair is one key/value pair in a deliberately ordered mapping. Value may
// be any Go value yaml.Node.Encode accepts, OR another *yaml.Node (a nested
// orderedMap/sequence), which Encode copies through unchanged.
type ordPair struct {
	Key   string
	Value any
}

// kv builds one key/value node pair.
func kv(key string, val any) (*yaml.Node, *yaml.Node) {
	k := &yaml.Node{}
	// A key is always a plain string; error is impossible for a string input.
	_ = k.Encode(key)
	// A value that is ALREADY a built node is used as-is. Encoding one
	// round-trips it through a generic value and drops everything the node
	// carries that a value cannot — HeadComment above all, which is how this
	// package explains a property to the operator in the file he opens. That
	// loss is silent: the YAML is still valid, still correct, and simply has
	// no comments, so the only thing that catches it is a test that reads the
	// rendered bytes back and looks for the account.
	if n, ok := val.(*yaml.Node); ok {
		return k, n
	}
	v := &yaml.Node{}
	_ = v.Encode(val)
	return k, v
}

// orderedMap builds a YAML mapping node whose keys appear in EXACTLY the
// order given — the ordering this package's generated files rely on for
// readability.
func orderedMap(pairs ...ordPair) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for _, p := range pairs {
		k, v := kv(p.Key, p.Value)
		n.Content = append(n.Content, k, v)
	}
	return n
}

// seq builds a YAML sequence node from a slice of already-built value nodes.
func seq(items ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.SequenceNode, Content: items}
}

// marshalDoc renders a top-level ordered mapping as a complete YAML
// document's bytes.
func marshalDoc(top *yaml.Node) ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{top}}
	return yaml.Marshal(doc)
}
