package orderflow

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type nativeChangesCorrelator interface {
	NativeOrderCorrelationsForIDs(context.Context, orders.Scope, []string) ([]orders.Correlation, error)
}

func (i *Ingestor) readNativeChangesPage(ctx context.Context, scope orders.Scope, credentials orders.CredentialRevisions, window orders.OrderWindow, cursor string) (orders.OrderSourcePage, error) {
	client, err := resolveOrderClient(ctx, i.options.NativeResolver, scope, "moysklad", credentials.Moysklad)
	if err != nil {
		return orders.OrderSourcePage{}, err
	}
	reader, ok := client.(orders.NativeChangesReader)
	correlator, bound := i.store.(nativeChangesCorrelator)
	pointReader, points := client.(nativePointReader)
	if !ok || !bound || !points {
		return orders.OrderSourcePage{}, orders.ErrBlocked
	}
	changes, err := reader.ReadNativeOrderChanges(ctx, scope, window.From, window.To, window.Limit, cursor)
	if err != nil {
		return orders.OrderSourcePage{}, err
	}
	if len(changes.Changes) > window.Limit || changes.NextCursor != "" && changes.NextCursor == cursor {
		return orders.OrderSourcePage{}, orders.ErrInvalid
	}
	ids := make([]string, 0, len(changes.Changes))
	for _, change := range changes.Changes {
		ids = append(ids, change.ID)
	}
	correlations, err := correlator.NativeOrderCorrelationsForIDs(ctx, scope, ids)
	if err != nil {
		return orders.OrderSourcePage{}, err
	}
	if err = validateNativePointCandidates(scope, orders.NativePointCandidates{Correlations: correlations}, window.Limit); err != nil {
		return orders.OrderSourcePage{}, err
	}
	return i.readChangedNativePoints(ctx, pointReader, credentials.Moysklad, correlations, changes)
}

func (i *Ingestor) readChangedNativePoints(ctx context.Context, pointReader nativePointReader, revision int64, correlations []orders.Correlation, changes orders.NativeOrderChanges) (orders.OrderSourcePage, error) {
	page := orders.OrderSourcePage{NativeReads: make([]orders.NativeOrderPointRead, 0, len(correlations)), NextCursor: changes.NextCursor, Complete: changes.NextCursor == "", Exhausted: changes.NextCursor == ""}
	for _, correlation := range correlations {
		read, stop := i.readNativePoint(ctx, pointReader, revision, correlation)
		if stop {
			return orders.OrderSourcePage{}, orders.ErrBlocked
		}
		page.NativeReads = append(page.NativeReads, read)
		if read.Code != "" {
			page.Complete = false
			page.Failures = append(page.Failures, orders.ReadFailure{PostingNumber: correlation.PostingNumber, Code: read.Code})
		}
	}
	return page, nil
}

func nativeChangesWindow(now time.Time, checkpoint orders.Checkpoint, revision int64) orders.OrderWindow {
	upper := now.UTC().Add(-2 * time.Second).Truncate(time.Millisecond)
	window := orders.OrderWindow{Stream: orders.NativeChangesStream, From: upper.Add(-15 * time.Minute), To: upper}
	if checkpoint.ConfigRevision != revision || checkpoint.Revision == 0 {
		return window
	}
	if !checkpoint.Complete && !checkpoint.Exhausted {
		window.From, window.To = checkpoint.WindowFrom, checkpoint.WindowTo
		return window
	}
	window.From = checkpoint.WindowTo.Add(-5 * time.Minute)
	if !window.From.Before(upper) {
		window.From = upper.Add(-5 * time.Minute)
	}
	if window.To.Sub(window.From) > 31*24*time.Hour {
		window.To = window.From.Add(31 * 24 * time.Hour)
	}
	return window
}
