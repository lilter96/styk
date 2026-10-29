// Package orderprovider resolves credentials only for a verified order scope.
package orderprovider

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/credentials"
	"github.com/lilter96/styk/internal/delivery"
	"github.com/lilter96/styk/internal/infrastructure/moysklad"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

type CredentialResolver interface {
	ResolveWithRevision(context.Context, credentials.Scope) (string, int64, error)
}
type Factory struct {
	Credentials      CredentialResolver
	Gate             providercall.Gate
	DeliveryObserver delivery.Observer
	NewMoysklad      func(string, string) (*moysklad.Client, error)
	NewOzon          func(string, string) (*ozon.Client, error)
}

func (f Factory) ResolveOrder(ctx context.Context, scope orders.Scope, provider, account string, expected int64) (orders.Binding, error) {
	if scope.Validate() != nil || f.Credentials == nil || f.Gate == nil || (provider != "ozon" && provider != "moysklad") || (provider == "ozon" && account != scope.OzonAccount) || (provider == "moysklad" && account != scope.MoyskladAccount) {
		return orders.Binding{}, orders.ErrInvalid
	}
	secret, revision, err := f.Credentials.ResolveWithRevision(ctx, credentials.Scope{Tenant: scope.Tenant, Provider: provider, Account: account})
	if err != nil {
		return orders.Binding{}, err
	}
	if revision < 1 || (expected > 0 && expected != revision) {
		return orders.Binding{}, orders.ErrStale
	}
	client, err := f.client(scope, provider, account, secret, revision)
	if err != nil {
		return orders.Binding{}, err
	}
	return orders.Binding{Scope: scope, Provider: provider, Account: account, CredentialRevision: revision, Client: client}, nil
}
func (f Factory) client(scope orders.Scope, provider, account, secret string, revision int64) (orders.Provider, error) {
	gateScope := providercall.Scope{Tenant: scope.Tenant, Provider: provider, Account: account, CredentialRevision: revision}
	if provider == "ozon" {
		create := f.NewOzon
		if create == nil {
			create = defaultOzon
		}
		base, err := create(account, secret)
		if err != nil {
			return nil, err
		}
		if err = base.Coordinate(f.Gate, gateScope); err != nil {
			return nil, err
		}
		return ozon.NewOrderClientWithOptions(base, scope, ozon.OrderReadOptions{AnalyticsData: true, FinancialData: true, Barcodes: true, ResolveDelivery: true, DeliveryObserver: f.DeliveryObserver})
	}
	create := f.NewMoysklad
	if create == nil {
		create = defaultMoysklad
	}
	base, err := create(account, secret)
	if err != nil {
		return nil, err
	}
	if err = base.Coordinate(f.Gate, gateScope); err != nil {
		return nil, err
	}
	return moysklad.NewOrderClient(base, scope)
}
func defaultOzon(account, secret string) (*ozon.Client, error) {
	return ozon.New(ozon.Options{BaseURL: ozon.BaseURL, ClientID: account, APIKey: secret, Timeout: 15 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
}
func defaultMoysklad(_, secret string) (*moysklad.Client, error) {
	return moysklad.New(moysklad.Options{BaseURL: moysklad.BaseURL, Token: secret, Timeout: 15 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
}
