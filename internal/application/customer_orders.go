package application

import (
	"context"
	"fmt"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

type CustomerOrderReader interface {
	ReadCustomerOrders(context.Context, string, string, time.Time, time.Time) (domain.CustomerOrderImport, error)
}

type CustomerOrderStore interface {
	CheckCustomerOrderAccount(context.Context, string, string) error
	SaveCustomerOrders(context.Context, domain.CustomerOrderImport) (int64, error)
}

type CustomerOrderImporter struct {
	Reader CustomerOrderReader
	Store  CustomerOrderStore
}

func (i CustomerOrderImporter) Run(ctx context.Context, tenant, account string, since, until time.Time) (int64, error) {
	if err := i.Store.CheckCustomerOrderAccount(ctx, tenant, account); err != nil {
		return 0, fmt.Errorf("customer order scope: %w", err)
	}
	b, err := i.Reader.ReadCustomerOrders(ctx, tenant, account, since, until)
	if err != nil {
		return 0, fmt.Errorf("collect customer orders: %w", err)
	}
	if b.Tenant != tenant || b.Account != account || !b.Since.Equal(since) || !b.Until.Equal(until) || b.StartedAt.IsZero() || b.FinishedAt.IsZero() {
		return 0, fmt.Errorf("inconsistent customer order batch")
	}
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	id, err := i.Store.SaveCustomerOrders(ctx, b)
	if err != nil {
		return 0, fmt.Errorf("persist customer orders: %w", err)
	}
	return id, nil
}
