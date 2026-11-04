package orderflow

import (
	"context"
	"errors"

	"github.com/lilter96/styk/internal/orders"
)

type nativePointReader interface {
	ReadOrderCorrelation(context.Context, orders.Correlation) (orders.Correlation, error)
	ReadCustomerOrder(context.Context, orders.Correlation) (orders.TargetObservation, error)
}

func (i *Ingestor) readNativePointPage(ctx context.Context, scope orders.Scope, credentials orders.CredentialRevisions, cursor string) (orders.OrderSourcePage, error) {
	store, ok := i.store.(NativePointCandidateReader)
	if !ok || i.options.NativeResolver == nil {
		return orders.OrderSourcePage{}, orders.ErrBlocked
	}
	candidates, err := store.NativeOrderCandidates(ctx, scope, i.options.NativeBatch, cursor)
	if err != nil {
		return orders.OrderSourcePage{}, err
	}
	if err = validateNativePointCandidates(scope, candidates, i.options.NativeBatch); err != nil {
		return orders.OrderSourcePage{}, err
	}
	page := orders.OrderSourcePage{NativeReads: make([]orders.NativeOrderPointRead, 0, len(candidates.Correlations)), NextCursor: candidates.NextCursor, Complete: candidates.NextCursor == "", Exhausted: candidates.NextCursor == ""}
	if len(candidates.Correlations) == 0 {
		return page, nil
	}
	client, err := resolveOrderClient(ctx, i.options.NativeResolver, scope, "moysklad", credentials.Moysklad)
	if err != nil {
		return page, err
	}
	reader, ok := client.(nativePointReader)
	if !ok {
		return page, orders.ErrBlocked
	}
	i.collectNativePoints(ctx, reader, credentials.Moysklad, candidates.Correlations, &page)
	return page, nil
}

func validateNativePointCandidates(scope orders.Scope, candidates orders.NativePointCandidates, limit int) error {
	if len(candidates.Correlations) > limit || len(candidates.Correlations) == 0 && candidates.NextCursor != "" {
		return orders.ErrInvalid
	}
	seen := make(map[string]bool, len(candidates.Correlations))
	for _, correlation := range candidates.Correlations {
		if correlation.Validate() != nil || correlation.Scope != scope || correlation.CustomerOrderID == "" || seen[correlation.PostingNumber] {
			return orders.ErrInvalid
		}
		seen[correlation.PostingNumber] = true
	}
	if candidates.NextCursor != "" && candidates.NextCursor != candidates.Correlations[len(candidates.Correlations)-1].PostingNumber {
		return orders.ErrInvalid
	}
	return nil
}

func (i *Ingestor) collectNativePoints(ctx context.Context, reader nativePointReader, revision int64, candidates []orders.Correlation, page *orders.OrderSourcePage) {
	for index, correlation := range candidates {
		read, stop := i.readNativePoint(ctx, reader, revision, correlation)
		page.NativeReads = append(page.NativeReads, read)
		if read.Code == "" {
			continue
		}
		page.Complete = false
		page.Failures = append(page.Failures, orders.ReadFailure{PostingNumber: correlation.PostingNumber, Code: read.Code})
		if stop {
			if index < len(candidates)-1 {
				page.NextCursor, page.Exhausted = correlation.PostingNumber, false
			}
			break
		}
	}
}

func (i *Ingestor) readNativePoint(ctx context.Context, reader nativePointReader, revision int64, correlation orders.Correlation) (orders.NativeOrderPointRead, bool) {
	read := orders.NativeOrderPointRead{Correlation: correlation, CredentialRevision: revision, StartedAt: i.options.Now()}
	actual, err := reader.ReadOrderCorrelation(ctx, correlation)
	if err == nil && actual != correlation {
		read.FinishedAt, read.Code = i.options.Now(), "NATIVE_BINDING_CHANGED"
		return read, false
	}
	var target orders.TargetObservation
	if err == nil {
		target, err = reader.ReadCustomerOrder(ctx, correlation)
	}
	read.FinishedAt = i.options.Now()
	if err != nil {
		code, stop := nativePointReadFailure(err)
		read.Code = code
		return read, stop
	}
	if target.Scope != correlation.Scope || target.Correlation != correlation {
		read.Code = "NATIVE_SCOPE_MISMATCH"
		return read, true
	}
	point, err := orders.NewNativeOrderPoint(target, revision, read.StartedAt, read.FinishedAt)
	if err != nil {
		read.Code = "NATIVE_ORDER_INCOMPLETE"
		return read, false
	}
	read.Point = &point
	return read, false
}

func nativePointReadFailure(err error) (string, bool) {
	if errors.Is(err, orders.ErrStale) {
		return "NATIVE_ORDER_CHANGED_DURING_READ", false
	}
	code, stop := refreshReadFailure(err)
	switch code {
	case "POSTING_NOT_FOUND":
		return "NATIVE_ORDER_NOT_FOUND", stop
	case "POSTING_SCHEMA_DRIFT":
		return "NATIVE_ORDER_SCHEMA_DRIFT", stop
	case "POSTING_SCOPE_MISMATCH":
		return "NATIVE_SCOPE_MISMATCH", stop
	case "POSTING_READ_FAILED", "":
		return "NATIVE_READ_FAILED", stop
	default:
		return "NATIVE_" + code, stop
	}
}
