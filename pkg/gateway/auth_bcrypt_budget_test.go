package gateway

import (
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// bcryptBudgetDecoys is how many unrelated user tokens sit in front of the
// real account. The old resolver bcrypt-scanned all of them before accepting
// a CLI bearer or an id-tagged token for a later account.
const bcryptBudgetDecoys = 8

type bcryptBudgetFixture struct {
	cfg       *config.Config
	aliceTok  string
	cliTok    string
	legacyTok string
	compares  *int
}

func newBcryptBudgetFixture(t *testing.T) bcryptBudgetFixture {
	t.Helper()
	aliceTok := "omnipus_a11ce001_" + strings.Repeat("a", 64)
	cliTok := "omnipus_" + strings.Repeat("b", 64)
	legacyTok := "omnipus_" + strings.Repeat("c", 64)

	decoys := make([]config.TokenEntry, bcryptBudgetDecoys)
	for i := range decoys {
		id := fmt.Sprintf("d%07d", i) // 8 chars, distinct from alice's id
		raw := "omnipus_" + id + "_" + fmt.Sprintf("%064x", i+1)
		decoys[i] = config.TokenEntry{ID: id, Hash: bcryptBudgetHash(t, config.TokenSecret(raw))}
	}

	cfg := &config.Config{}
	cfg.Gateway.Users = []config.UserConfig{
		{Username: "other", Tokens: decoys},
		{Username: "alice", Tokens: []config.TokenEntry{{
			ID:   "a11ce001",
			Hash: bcryptBudgetHash(t, config.TokenSecret(aliceTok)),
		}}},
		{Username: "bob", TokenHash: bcryptBudgetHash(t, legacyTok)},
	}
	cfg.Gateway.CLIToken = &config.TokenEntry{Hash: bcryptBudgetHash(t, cliTok)}

	compares := 0
	restore := config.SetCompareHashAndPasswordForTest(func(hashed, pw []byte) error {
		compares++
		return bcrypt.CompareHashAndPassword(hashed, pw)
	})
	t.Cleanup(restore)
	return bcryptBudgetFixture{cfg: cfg, aliceTok: aliceTok, cliTok: cliTok, legacyTok: legacyTok, compares: &compares}
}

func bcryptBudgetHash(t *testing.T, secret string) config.BcryptHash {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return config.BcryptHash(h)
}

func (f bcryptBudgetFixture) reset() { *f.compares = 0 }

// TestResolveBearerIdentity_BcryptBudget is the gateway-side budget. It fails
// on the old resolver: a CLI bearer and an id-tagged user bearer both paid one
// bcrypt per stored user token before this change.
func TestResolveBearerIdentity_BcryptBudget(t *testing.T) {
	fx := newBcryptBudgetFixture(t)

	t.Run("cli token is one compare", func(t *testing.T) {
		fx.reset()
		user, viaCLI, ok := resolveBearerIdentity(fx.cfg, fx.cliTok)
		if !ok || !viaCLI || user == nil || user.Username != "cli" {
			t.Fatalf("cli token: user=%v viaCLI=%v ok=%v", user, viaCLI, ok)
		}
		if *fx.compares != 1 {
			t.Fatalf("cli bcrypt compares = %d, want 1", *fx.compares)
		}
	})

	t.Run("human id token stays the user and is one compare", func(t *testing.T) {
		fx.reset()
		user, viaCLI, ok := resolveBearerIdentity(fx.cfg, fx.aliceTok)
		if !ok || viaCLI || user == nil || user.Username != "alice" {
			t.Fatalf("alice token: user=%v viaCLI=%v ok=%v", user, viaCLI, ok)
		}
		if *fx.compares != 1 {
			t.Fatalf("alice bcrypt compares = %d, want 1", *fx.compares)
		}
	})

	t.Run("unknown id compares nothing and does not match", func(t *testing.T) {
		fx.reset()
		unknown := "omnipus_00000000_" + strings.Repeat("e", 64)
		user, viaCLI, ok := resolveBearerIdentity(fx.cfg, unknown)
		if ok || viaCLI || user != nil {
			t.Fatalf("unknown id must not match, got user=%v viaCLI=%v ok=%v", user, viaCLI, ok)
		}
		if *fx.compares != 0 {
			t.Fatalf("unknown id bcrypt compares = %d, want 0", *fx.compares)
		}
	})

	t.Run("wrong body with a real id is rejected after one compare", func(t *testing.T) {
		fx.reset()
		forged := "omnipus_a11ce001_" + strings.Repeat("d", 64)
		_, _, ok := resolveBearerIdentity(fx.cfg, forged)
		if ok {
			t.Fatal("forged body must not match")
		}
		if *fx.compares != 1 {
			t.Fatalf("forged body bcrypt compares = %d, want 1", *fx.compares)
		}
	})

	t.Run("legacy user token still matches the user", func(t *testing.T) {
		fx.reset()
		user, viaCLI, ok := resolveBearerIdentity(fx.cfg, fx.legacyTok)
		if !ok || viaCLI || user == nil || user.Username != "bob" {
			t.Fatalf("legacy token: user=%v viaCLI=%v ok=%v", user, viaCLI, ok)
		}
		// One failed CLI compare, then the no-id scan: every decoy, alice's
		// entry, and bob's legacy hash.
		want := 1 + bcryptBudgetDecoys + 1 + 1
		if *fx.compares != want {
			t.Fatalf("legacy bcrypt compares = %d, want %d", *fx.compares, want)
		}
	})

	t.Run("wrong legacy token is rejected", func(t *testing.T) {
		fx.reset()
		wrong := "omnipus_" + strings.Repeat("f", 64)
		_, _, ok := resolveBearerIdentity(fx.cfg, wrong)
		if ok {
			t.Fatal("wrong legacy token must not match")
		}
		want := 1 + bcryptBudgetDecoys + 1 + 1
		if *fx.compares != want {
			t.Fatalf("wrong legacy bcrypt compares = %d, want %d", *fx.compares, want)
		}
	})
}
