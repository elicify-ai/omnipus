package credentials

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
)

// NamedValue is one decrypted credential name and its plaintext value.
type NamedValue struct {
	Name  string
	Value string
}

// SensitiveNamedValues returns every stored secret as a (name, value) pair —
// the same decrypt loop as SensitiveValues, keeping each value's name so the
// skills save check can name a matched secret without showing its value
// (skills spec §7.4). An OAuth grant entry (<provider>_OAUTH) contributes its
// tokens as <NAME>.access_token / <NAME>.refresh_token instead of the raw
// grant blob.
//
// A store file that does not exist is an empty store: no values, no error
// (§7.4). A locked store refuses with ErrStoreLocked. Damaged entries surface
// as a *DamagedEntriesError alongside the readable pairs.
func (s *Store) SensitiveNamedValues() ([]NamedValue, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := os.Stat(s.path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if s.key == nil {
		return nil, ErrStoreLocked
	}
	sf, err := s.loadFileInternal()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(sf.Credentials))
	for name := range sf.Credentials {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]NamedValue, 0, len(names))
	failures := &DamagedEntriesError{}
	for _, name := range names {
		value, decryptErr := decrypt(s.key, name, sf.Credentials[name])
		if decryptErr != nil {
			failures.Names = append(failures.Names, name)
			failures.Errs = append(failures.Errs, decryptErr)
			continue
		}
		if strings.HasSuffix(name, "_OAUTH") {
			var grant map[string]json.RawMessage
			if json.Unmarshal([]byte(value), &grant) == nil {
				for _, field := range []string{"access_token", "refresh_token"} {
					var token string
					if json.Unmarshal(grant[field], &token) == nil && token != "" {
						values = append(values, NamedValue{Name: name + "." + field, Value: token})
					}
				}
				continue
			}
		}
		values = append(values, NamedValue{Name: name, Value: value})
	}
	if len(failures.Errs) > 0 {
		return values, failures
	}
	return values, nil
}
