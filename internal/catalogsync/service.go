package catalogsync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

type Service struct {
	Store      ServiceStore
	Resolver   Resolver
	AllowScope func(catalog.Scope) bool
	Now        func() time.Time
}

func (service Service) Config(ctx context.Context, scope catalog.Scope) (Config, error) {
	if err := service.validateScope(scope); err != nil {
		return Config{}, err
	}
	config, err := service.Store.CatalogConfig(ctx, scope)
	if err != nil {
		return Config{}, fmt.Errorf("load catalog configuration: %w", err)
	}
	if config.Scope != scope {
		return Config{}, ErrStale
	}
	return config, nil
}

func (service Service) SaveConfig(ctx context.Context, request SaveConfigRequest) (Config, error) {
	if err := service.validateScope(request.Config.Scope); err != nil {
		return Config{}, err
	}
	if err := request.Config.Validate(); err != nil {
		return Config{}, err
	}
	if !validID(request.Actor) || !validID(request.IdempotencyKey) || request.ExpectedRevision < 0 {
		return Config{}, ErrInvalid
	}
	if (request.Config.Enabled || request.Config.TestEnabled) && !service.allowed(request.Config.Scope) {
		return Config{}, ErrBlocked
	}
	config, err := service.Store.SaveCatalogConfig(ctx, request, service.now())
	if err != nil {
		return Config{}, fmt.Errorf("save catalog configuration: %w", err)
	}
	return config, nil
}

func (service Service) Preview(ctx context.Context, request PreviewRequest) (Preview, error) {
	return service.preview(ctx, request, false)
}

func (service Service) Approve(ctx context.Context, approval Approval) (Operation, error) {
	approval.Automatic = false
	return service.approve(ctx, approval)
}

func (service Service) Products(ctx context.Context, scope catalog.Scope, page PageRequest) (ProductPage, error) {
	if err := service.validateScope(scope); err != nil {
		return ProductPage{}, err
	}
	if page.Provider != "" && page.Provider != catalog.ProviderMoySklad && page.Provider != catalog.ProviderOzon {
		return ProductPage{}, ErrInvalid
	}
	result, err := service.Store.CatalogProducts(ctx, scope, boundedPage(page))
	if err != nil {
		return ProductPage{}, fmt.Errorf("list catalog products: %w", err)
	}
	return result, nil
}

func (service Service) History(ctx context.Context, scope catalog.Scope, page PageRequest) (HistoryPage, error) {
	if err := service.validateScope(scope); err != nil {
		return HistoryPage{}, err
	}
	result, err := service.Store.CatalogHistory(ctx, scope, boundedPage(page))
	if err != nil {
		return HistoryPage{}, fmt.Errorf("list catalog history: %w", err)
	}
	return result, nil
}

func (service Service) validateScope(scope catalog.Scope) error {
	if service.Store == nil {
		return ErrBlocked
	}
	if err := scope.Validate(); err != nil {
		return fmt.Errorf("validate catalog scope: %w", err)
	}
	return nil
}

func (service Service) allowed(scope catalog.Scope) bool {
	return service.AllowScope != nil && service.AllowScope(scope)
}

func (service Service) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func boundedPage(page PageRequest) PageRequest {
	if page.Limit < 1 || page.Limit > 100 {
		page.Limit = 50
	}
	return page
}

func newID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("generate catalog intent identity: %w", err)
	}
	return hex.EncodeToString(data[:]), nil
}
