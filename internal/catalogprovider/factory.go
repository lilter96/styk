// Package catalogprovider resolves catalog clients without widening stock permissions.
package catalogprovider

import (
	"context"
	"errors"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
)

type Resolver interface {
	ResolveWithRevision(context.Context, credentials.Scope) (string, int64, error)
}

type Factory struct {
	Credentials Resolver
	NewMoysklad func(string, string) (*moysklad.Client, error)
	NewOzon     func(string, string) (*ozon.Client, error)
}

func (f Factory) ResolveCatalog(ctx context.Context, scope catalog.Scope, account catalog.Account, expected int64) (catalogsync.Binding, error) {
	if scope.Validate() != nil || !scope.Contains(account) || f.Credentials == nil {
		return catalogsync.Binding{}, catalogsync.ErrInvalid
	}
	secret, revision, err := f.Credentials.ResolveWithRevision(ctx, credentials.Scope{Tenant: scope.Tenant, Provider: string(account.Provider), Account: account.ID})
	if err != nil {
		return catalogsync.Binding{}, err
	}
	if revision < 1 {
		return catalogsync.Binding{}, catalogsync.ErrBlocked
	}
	if expected > 0 && revision != expected {
		return catalogsync.Binding{}, catalogsync.ErrStale
	}
	client, err := f.client(scope, account, secret)
	if err != nil {
		return catalogsync.Binding{}, err
	}
	return catalogsync.Binding{Scope: scope, Account: account, CredentialRevision: revision, Client: client}, nil
}

func (f Factory) client(scope catalog.Scope, account catalog.Account, secret string) (catalogsync.Provider, error) {
	switch account.Provider {
	case catalog.ProviderMoySklad:
		return f.moysklad(scope, account.ID, secret)
	case catalog.ProviderOzon:
		return f.ozon(scope, account.ID, secret)
	default:
		return nil, catalogsync.ErrInvalid
	}
}

func (f Factory) moysklad(scope catalog.Scope, account, secret string) (catalogsync.Provider, error) {
	makeClient := f.NewMoysklad
	if makeClient == nil {
		makeClient = defaultMoysklad
	}
	base, err := makeClient(account, secret)
	if err != nil {
		return nil, err
	}
	client, err := moysklad.NewControlledTestCatalogClient(base, scope)
	if errors.Is(err, moysklad.ErrCatalogWritesDisabled) {
		client, err = moysklad.NewCatalogClient(base)
	}
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (f Factory) ozon(scope catalog.Scope, account, secret string) (catalogsync.Provider, error) {
	makeClient := f.NewOzon
	if makeClient == nil {
		makeClient = defaultOzon
	}
	base, err := makeClient(account, secret)
	if err != nil {
		return nil, err
	}
	client, err := ozon.NewControlledTestCatalogClientFromClient(base, scope)
	if errors.Is(err, ozon.ErrCatalogWritesDisabled) {
		client, err = ozon.NewCatalogReaderFromClient(base, scope)
	}
	if err != nil {
		return nil, err
	}
	return client, nil
}

func defaultMoysklad(_, secret string) (*moysklad.Client, error) {
	return moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: secret, Timeout: 15 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
}

func defaultOzon(account, secret string) (*ozon.Client, error) {
	return ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: account, APIKey: secret, Timeout: 15 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
}
