package application

import (
	"context"
	"fmt"
	"time"

	"example.com/styk/internal/domain"
)

type PostingReader interface {
	ReadPostings(context.Context, string, time.Time, time.Time) (domain.PostingImport, error)
}
type PostingStore interface {
	SavePostings(context.Context, domain.PostingImport) (int64, error)
}
type PostingImporter struct {
	Reader PostingReader
	Store  PostingStore
}

func (i PostingImporter) Run(ctx context.Context, tenant string, since, until time.Time) (int64, error) {
	b, err := i.Reader.ReadPostings(ctx, tenant, since, until)
	if err != nil {
		return 0, fmt.Errorf("collect postings: %w", err)
	}
	if b.Tenant != tenant || !b.Since.Equal(since) || !b.Until.Equal(until) || b.StartedAt.IsZero() || b.FinishedAt.IsZero() {
		return 0, fmt.Errorf("posting reader returned inconsistent batch")
	}
	if err = ctx.Err(); err != nil {
		return 0, err
	}
	id, err := i.Store.SavePostings(ctx, b)
	if err != nil {
		return 0, fmt.Errorf("persist postings: %w", err)
	}
	return id, nil
}
