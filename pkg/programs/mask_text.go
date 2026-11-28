// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// tokenTextReplacer is the always-on program-token pass of boundaries B1–B6
// (connected-programs I-1, FR-020): it replaces every current and retired
// token in its original, Latin-1-escaped and JSON-escaped spellings. It is
// rebuilt each time a new token value is remembered; nil means none is known.
var tokenTextReplacer atomic.Pointer[strings.Replacer]

// MaskTokens returns s with every current and retired program token replaced
// by [FILTERED] in its original, Latin-1-escaped and JSON-escaped spellings.
// Unlike the general sensitive-data filter it ignores tools.filter_sensitive_data
// and filter_min_length: the defence-in-depth boundaries B1–B6 call it before
// truncation, encoding and admission, so a token that reached text by another
// way (for example a file a command wrote and read_file later returned) never
// reaches history, the model, a person or the log unchanged.
func MaskTokens(s string) string {
	r := tokenTextReplacer.Load()
	if r == nil || s == "" {
		return s
	}
	return r.Replace(s)
}

// RememberStoredTokens remembers the current token of every program entry
// that has a valid program-owned credential, so the always-on pass masks it
// from boot on (FR-020: current and retired values). An entry with no token
// is skipped; the first other read error is returned after every readable
// token was remembered.
func RememberStoredTokens(cfg *config.Config, store *credentials.Store) error {
	if cfg == nil || store == nil {
		return nil
	}
	entries, err := Entries(cfg)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		v, _, rerr := credentials.ReadProgramCredential(store, TokenReference(e.ID))
		var nf *credentials.NotFoundError
		switch {
		case rerr == nil:
			rememberToken(v)
		case errors.As(rerr, &nf):
		case firstErr == nil:
			firstErr = fmt.Errorf("programs: reading the token of %q: %w", e.ID, rerr)
		}
	}
	return firstErr
}

// publishTokenReplacer rebuilds the B1–B6 replacer from tokens and publishes
// it to the gateway log (B6). Called with retired held, so publications are
// ordered and the latest holds every token remembered so far.
func publishTokenReplacer(tokens []string) {
	var pairs []string
	seen := map[string]struct{}{}
	add := func(spelling string) {
		if spelling == "" {
			return
		}
		if _, dup := seen[spelling]; dup {
			return
		}
		seen[spelling] = struct{}{}
		pairs = append(pairs, spelling, FilteredMarker)
	}
	for _, v := range tokens {
		add(v)
		add(latin1Spelling(v))
		add(jsonSpelling(v))
	}
	if len(pairs) == 0 {
		tokenTextReplacer.Store(nil)
		logger.SetProgramValueReplacer(nil)
		return
	}
	r := strings.NewReplacer(pairs...)
	tokenTextReplacer.Store(r)
	logger.SetProgramValueReplacer(r)
}

// latin1Spelling writes every character in U+00A0..U+00FF as \xHH (the
// spelling the general filter also covers); "" when v has none.
func latin1Spelling(v string) string {
	var b strings.Builder
	changed := false
	for _, r := range v {
		if r >= 0xa0 && r <= 0xff {
			fmt.Fprintf(&b, `\x%02x`, r)
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return ""
	}
	return b.String()
}

// jsonSpelling is v as it appears inside a JSON string; "" when that is v
// itself.
func jsonSpelling(v string) string {
	raw, err := json.Marshal(v)
	if err != nil || len(raw) < 2 {
		return ""
	}
	inner := string(raw[1 : len(raw)-1])
	if inner == v {
		return ""
	}
	return inner
}
