package catalogprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
)

type fixtureResolver struct {
	calls    int
	revision int64
	err      error
	scope    credentials.Scope
}

func (r *fixtureResolver) ResolveWithRevision(_ context.Context, s credentials.Scope) (string, int64, error) {
	r.calls++
	r.scope = s
	return "synthetic-secret", r.revision, r.err
}
func fixtureScope() catalog.Scope {
	return catalog.Scope{Tenant: "merchant-a", MoyskladAccount: "source-a", OzonAccount: "market-a"}
}

func TestFactoryRejectsScopeAndStaleCredentialsBeforeConstructingProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name               string
		scope              catalog.Scope
		account            catalog.Account
		revision, expected int64
		want               error
		calls              int
	}{
		{"foreign account", fixtureScope(), catalog.Account{Provider: catalog.ProviderOzon, ID: "market-b"}, 1, 1, catalogsync.ErrInvalid, 0},
		{"unsupported provider", fixtureScope(), catalog.Account{Provider: "unsupported", ID: "market-a"}, 1, 1, catalogsync.ErrInvalid, 0},
		{"changed credential", fixtureScope(), catalog.Account{Provider: catalog.ProviderOzon, ID: "market-a"}, 2, 1, catalogsync.ErrStale, 1},
		{"legacy credentials", fixtureScope(), catalog.Account{Provider: catalog.ProviderOzon, ID: "market-a"}, 0, 0, catalogsync.ErrBlocked, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resolver := &fixtureResolver{revision: tc.revision}
			built := false
			f := Factory{Credentials: resolver, NewOzon: func(string, string) (*ozon.Client, error) {
				built = true
				return nil, errors.New("should not construct client")
			}}
			_, err := f.ResolveCatalog(context.Background(), tc.scope, tc.account, tc.expected)
			if !errors.Is(err, tc.want) || built || resolver.calls != tc.calls {
				t.Fatalf("scope/revision bypass: err=%v built=%v calls=%d", err, built, resolver.calls)
			}
		})
	}
}

func TestFactoryResolvesOnlyTheExactRegisteredCredentialScope(t *testing.T) {
	t.Parallel()
	resolver := &fixtureResolver{revision: 4, err: credentials.ErrOwnership}
	scope := fixtureScope()
	account := catalog.Account{Provider: catalog.ProviderOzon, ID: scope.OzonAccount}
	_, err := (Factory{Credentials: resolver}).ResolveCatalog(context.Background(), scope, account, 4)
	want := credentials.Scope{Tenant: scope.Tenant, Provider: "ozon", Account: scope.OzonAccount}
	if !errors.Is(err, credentials.ErrOwnership) || resolver.scope != want {
		t.Fatalf("credential scope changed: scope=%+v error=%v", resolver.scope, err)
	}
}
