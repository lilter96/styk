package catalogsync

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

type securityProvider struct {
	read func(context.Context, catalog.ReadRequest) (catalog.Snapshot, error)
}

func (provider *securityProvider) ReadCatalog(ctx context.Context, request catalog.ReadRequest) (catalog.Snapshot, error) {
	return provider.read(ctx, request)
}

func (*securityProvider) WriteCatalog(context.Context, catalog.Command) (catalog.Receipt, error) {
	return catalog.Receipt{}, ErrBlocked
}

func (*securityProvider) BindCatalogIntent(catalog.Command, func(context.Context) (bool, error)) error {
	return ErrBlocked
}

func securityScope() catalog.Scope {
	return catalog.Scope{Tenant: "synthetic-security-tenant", MoyskladAccount: "synthetic-moy", OzonAccount: "synthetic-ozon"}
}

func securityIdentity() catalog.Identity {
	return catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: securityScope().OzonAccount}, EntityType: "product", ID: "101", OfferID: "SYNTHETIC-SECURITY", SKU: "201"}
}

func TestSecurityResolverRejectsSubstitutedBinding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Binding)
	}{
		{name: "tenant", change: func(b *Binding) { b.Scope.Tenant = "foreign" }},
		{name: "source_scope", change: func(b *Binding) { b.Scope.MoyskladAccount = "foreign" }},
		{name: "destination_scope", change: func(b *Binding) { b.Scope.OzonAccount = "foreign" }},
		{name: "account", change: func(b *Binding) { b.Account.ID = "foreign" }},
		{name: "provider", change: func(b *Binding) { b.Account.Provider = catalog.ProviderMoySklad }},
		{name: "old_credential", change: func(b *Binding) { b.CredentialRevision-- }},
		{name: "new_credential", change: func(b *Binding) { b.CredentialRevision++ }},
		{name: "missing_client", change: func(b *Binding) { b.Client = nil }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			account := securityIdentity().Account
			resolver := ResolveFunc(func(_ context.Context, scope catalog.Scope, resolved catalog.Account, revision int64) (Binding, error) {
				if scope != securityScope() || resolved != account || revision != 7 {
					t.Fatal("resolver received a widened scope or another provider's revision")
				}
				binding := Binding{Scope: scope, Account: resolved, CredentialRevision: revision, Client: &securityProvider{}}
				tc.change(&binding)
				return binding, nil
			})
			provider, err := resolveProvider(t.Context(), resolver, securityScope(), account, CredentialRevisions{Moysklad: 3, Ozon: 7})
			if !errors.Is(err, ErrStale) || provider != nil {
				t.Fatalf("substituted resolver binding accepted: provider=%T error=%v", provider, err)
			}
		})
	}
}

func TestSecurityFreshReadRejectsSubstitutedSnapshot(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*catalog.Snapshot)
	}{
		{name: "tenant", change: func(s *catalog.Snapshot) { s.Scope.Tenant = "foreign" }},
		{name: "scope_account", change: func(s *catalog.Snapshot) { s.Scope.OzonAccount = "foreign" }},
		{name: "snapshot_account", change: func(s *catalog.Snapshot) { s.Account.ID = "foreign" }},
		{name: "product_account", change: func(s *catalog.Snapshot) { s.Products[0].Identity.Account.ID = "foreign" }},
		{name: "product_id", change: func(s *catalog.Snapshot) { s.Products[0].Identity.ID = "201" }},
		{name: "offer_id", change: func(s *catalog.Snapshot) { s.Products[0].Identity.OfferID = "foreign" }},
		{name: "sku", change: func(s *catalog.Snapshot) { s.Products[0].Identity.SKU = "101" }},
		{name: "entity_type", change: func(s *catalog.Snapshot) { s.Products[0].Identity.EntityType = "variant" }},
		{name: "missing_revision", change: func(s *catalog.Snapshot) { s.Products[0].Revision = "" }},
		{name: "incomplete", change: func(s *catalog.Snapshot) { s.Complete = false }},
		{name: "unread_page", change: func(s *catalog.Snapshot) { s.NextCursor = "unread" }},
		{name: "extra_product", change: func(s *catalog.Snapshot) { s.Products = append(s.Products, s.Products[0]) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := securitySnapshotProvider(t, tc.change)
			product, err := readProduct(t.Context(), provider, securityScope(), securityIdentity())
			if !errors.Is(err, ErrStale) || product.Identity.ID != "" {
				t.Fatalf("untrusted source observation accepted: identity=%+v error=%v", product.Identity, err)
			}
		})
	}
}

func securitySnapshotProvider(t *testing.T, change func(*catalog.Snapshot)) *securityProvider {
	t.Helper()
	return &securityProvider{read: func(ctx context.Context, request catalog.ReadRequest) (catalog.Snapshot, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second || ctx.Err() != nil {
			t.Fatal("fresh provider read has no bounded deadline")
		}
		if request.Scope != securityScope() || request.Account != securityIdentity().Account || !reflect.DeepEqual(request.Identities, []catalog.Identity{securityIdentity()}) {
			t.Fatal("fresh read request substituted the reviewed identity")
		}
		snapshot := catalog.Snapshot{Scope: request.Scope, Account: request.Account, Complete: true,
			Products: []catalog.Product{{Identity: securityIdentity(), Revision: "reviewed-revision", Fields: map[catalog.Field]catalog.Value{catalog.FieldName: catalog.Text("Synthetic")}}}}
		change(&snapshot)
		return snapshot, nil
	}}
}
