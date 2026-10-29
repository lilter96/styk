package catalogsync

import (
	"context"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

const providerTimeout = 20 * time.Second

func resolveProvider(ctx context.Context, resolver Resolver, scope catalog.Scope, account catalog.Account, revisions CredentialRevisions) (Provider, error) {
	revision := revisions.forAccount(account)
	if resolver == nil || revision <= 0 || !scope.Contains(account) {
		return nil, ErrBlocked
	}
	binding, err := resolver.ResolveCatalog(ctx, scope, account, revision)
	if err != nil {
		return nil, fmt.Errorf("resolve scoped catalog provider: %w", err)
	}
	if binding.Scope != scope || binding.Account != account || binding.CredentialRevision != revision || binding.Client == nil {
		return nil, ErrStale
	}
	return binding.Client, nil
}

func readProduct(ctx context.Context, reader catalog.Reader, scope catalog.Scope, identity catalog.Identity) (catalog.Product, error) {
	snapshot, err := readSnapshot(ctx, reader, catalog.ReadRequest{Scope: scope, Account: identity.Account, Identities: []catalog.Identity{identity}, Limit: 2})
	if err != nil {
		return catalog.Product{}, err
	}
	if len(snapshot.Products) != 1 || !identity.SameEntity(snapshot.Products[0].Identity) {
		return catalog.Product{}, ErrStale
	}
	product := snapshot.Products[0]
	if product.Revision == "" {
		return catalog.Product{}, ErrStale
	}
	return product, nil
}

func readSnapshot(ctx context.Context, reader catalog.Reader, request catalog.ReadRequest) (catalog.Snapshot, error) {
	read, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	snapshot, err := reader.ReadCatalog(read, request)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("read fresh catalog facts: %w", err)
	}
	if snapshot.Scope != request.Scope || snapshot.Account != request.Account || !snapshot.Complete || snapshot.NextCursor != "" || len(snapshot.Products) > 1 {
		return catalog.Snapshot{}, ErrStale
	}
	for _, product := range snapshot.Products {
		if product.Identity.Validate() != nil || product.Identity.Account != request.Account {
			return catalog.Snapshot{}, ErrStale
		}
	}
	return snapshot, nil
}

// Creation lookup is exact and bounded; a sync ID is never a provider entity ID.
func readCreationTarget(ctx context.Context, reader catalog.Reader, scope catalog.Scope, enrollment Enrollment) (catalog.Snapshot, error) {
	if finder, ok := reader.(CreationFinder); ok {
		return readCreationSnapshot(ctx, finder, scope, enrollment)
	}
	if enrollment.Target.Account.Provider != catalog.ProviderOzon || enrollment.Target.OfferID == "" {
		return catalog.Snapshot{}, ErrBlocked
	}
	identity := enrollment.Target
	return readSnapshot(ctx, reader, catalog.ReadRequest{Scope: scope, Account: identity.Account,
		Identities: []catalog.Identity{identity}, Limit: 2})
}

func readCreationSnapshot(ctx context.Context, finder CreationFinder, scope catalog.Scope, enrollment Enrollment) (catalog.Snapshot, error) {
	create := enrollment.Create
	command := catalog.Command{ID: "creation-lookup", Scope: scope, Source: enrollment.Source, Target: enrollment.Target,
		Action: catalog.ActionCreate, Values: create.Values, Extensions: create.Extensions, Parent: create.Parent,
		CreateID: create.ID, CreateExternalCode: create.ExternalCode, IdempotencyKey: "creation-lookup"}
	read, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	snapshot, err := finder.LookupCatalogCreate(read, command)
	if err != nil {
		return catalog.Snapshot{}, fmt.Errorf("lookup exact catalog creation marker: %w", err)
	}
	if snapshot.Scope != scope || snapshot.Account != enrollment.Target.Account || !snapshot.Complete || snapshot.NextCursor != "" || len(snapshot.Products) > 1 {
		return catalog.Snapshot{}, ErrStale
	}
	for _, product := range snapshot.Products {
		if product.Identity.Validate() != nil || product.Identity.Account != snapshot.Account {
			return catalog.Snapshot{}, ErrStale
		}
	}
	return snapshot, nil
}

func sameFacts(left, right catalog.Product, revisions bool) bool {
	if revisions && left.Revision != right.Revision {
		return false
	}
	a, err := catalog.ProductFingerprint(left)
	if err != nil {
		return false
	}
	b, err := catalog.ProductFingerprint(right)
	return err == nil && a == b
}
