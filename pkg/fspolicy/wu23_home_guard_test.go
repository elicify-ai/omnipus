package fspolicy_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	fixture "github.com/elicify-ai/omnipus/tests/l1_red/protectedfixture"
)

// Binding amendment §2 D1/D2/D4. The table is independent of SecretPaths.
func TestWU23_InternalInventoryDefaultDeny(t *testing.T) {
	f := fixture.New(t)
	for _, work := range []string{f.Work, f.AgentHome} {
		for _, restrict := range []bool{true, false} {
			name := filepath.Base(work)
			if !restrict {
				name += "_unrestricted"
			}
			t.Run(name, func(t *testing.T) {
				p, err := fspolicy.EffectiveFSPolicy(context.Background(), f.AgentHome, work, restrict, f.Home, "self", "alpha")
				if err != nil {
					t.Fatal(err)
				}
				t.Run("workspace_positive", func(t *testing.T) {
					if fspolicy.IsCarveOut(filepath.Join(work, "allowed.txt"), p) {
						t.Fatal("D3 A1: working file must remain reachable")
					}
				})
				for _, rel := range fixture.ProtectedRelative() {
					t.Run(rel, func(t *testing.T) {
						path := filepath.Join(f.Home, filepath.FromSlash(rel))
						p.AllowedRoots = []string{f.Home, filepath.Dir(path)}
						if !fspolicy.IsCarveOut(path, p) {
							t.Errorf("D1/D2/D4: internal %s is reachable (even with overlapping grant); want protected", rel)
						}
					})
				}
				for _, rel := range []string{"future-created-later/new.txt", "sessions/not-created-yet.txt", "boot_epoch-next"} {
					if !fspolicy.IsCarveOut(filepath.Join(f.Home, filepath.FromSlash(rel)), p) {
						t.Errorf("D1: nonexistent internal path %s not denied by default", rel)
					}
				}
				outside := filepath.Join(f.Outside, "plain.txt")
				if fspolicy.IsCarveOut(outside, p) {
					t.Error("D3 A9: ordinary external path must not become internal")
				}
			})
		}
	}
	t.Run("required_HomeGuard_seam", func(t *testing.T) {
		field, ok := reflect.TypeOf(fspolicy.FSPolicy{}).FieldByName("HomeGuard")
		if !ok {
			t.Fatal("BLOCKED: FSPolicy.HomeGuard not implemented — required by amendment §2 D5")
		}
		typ := field.Type
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			t.Fatal("D5: HomeGuard must carry the home and explicit allowances")
		}
		if _, ok := typ.FieldByName("Home"); !ok {
			t.Fatal("BLOCKED: HomeGuard.Home not implemented — required by amendment §2 D5")
		}
		if _, ok := typ.FieldByName("Allow"); !ok {
			t.Fatal("BLOCKED: HomeGuard.Allow not implemented — required by amendment §2 D5")
		}
	})
}

// D5 anti-drift: asserting equality alone would bless both layers being wrong.
func TestWU23_KernelAndAppInventoryAgreeWithSpec(t *testing.T) {
	f := fixture.New(t)
	for _, work := range []string{f.Work, f.AgentHome} {
		p, err := fspolicy.EffectiveFSPolicy(context.Background(), f.AgentHome, work, true, f.Home, "self", "alpha")
		if err != nil {
			t.Fatal(err)
		}
		denied, err := fspolicy.KernelDeniedPathsFor(f.Home, work)
		if err != nil {
			t.Fatal(err)
		}
		for _, rel := range fixture.ProtectedRelative() {
			path := filepath.Join(f.Home, filepath.FromSlash(rel))
			app := fspolicy.IsCarveOut(path, p)
			kernel := false
			for _, root := range denied {
				kernel = kernel || fspolicy.CoversForDeny(root, path)
			}
			if !app || !kernel {
				t.Errorf("D5: %s: app_denied=%t kernel_denied=%t; both must be true", rel, app, kernel)
			}
		}
	}
}

// Identity cases: internal files remain internal even when aliased into work.
func TestWU23_ProtectedAliases(t *testing.T) {
	f := fixture.New(t)
	p, err := fspolicy.EffectiveFSPolicy(context.Background(), f.AgentHome, f.Work, false, f.Home, "self", "alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range fixture.ProtectedRelative() {
		t.Run(rel, func(t *testing.T) {
			src := filepath.Join(f.Home, filepath.FromSlash(rel))
			alias := filepath.Join(f.Work, strings.ReplaceAll(rel, "/", "-")+".link")
			if err := os.Link(src, alias); err != nil {
				t.Fatalf("hardlink fixture unavailable, not a passing protection test: %v", err)
			}
			if !fspolicy.IsCarveOut(alias, p) {
				t.Errorf("D2 identity: hardlinked %s readable through workspace alias", rel)
			}
		})
	}
}
