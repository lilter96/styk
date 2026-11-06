package orderflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

// Collect bounds one cycle to one discovery page and one persisted-active batch.
// Each batch owns a serial scope lease before its first provider read.
func (i *Ingestor) Collect(ctx context.Context, scope orders.Scope) error {
	stream, err := i.nextDiscoveryStream(ctx, scope)
	if err != nil {
		return err
	}
	discoveryErr := i.CollectStream(ctx, scope, stream)
	activeErr := i.collectActiveAfterDiscovery(ctx, scope, discoveryErr)
	var nativeErr error
	if i.options.NativeResolver != nil {
		nativeErr = i.CollectStream(ctx, scope, orders.NativePointStream)
	}
	return errors.Join(discoveryErr, activeErr, nativeErr)
}

func (i *Ingestor) nextDiscoveryStream(ctx context.Context, scope orders.Scope) (string, error) {
	discovery, err := i.store.OrderCheckpoint(ctx, scope, DiscoveryStream)
	if err != nil {
		return "", fmt.Errorf("read discovery checkpoint: %w", err)
	}
	unfulfilled, err := i.store.OrderCheckpoint(ctx, scope, UnfulfilledStream)
	if err != nil {
		return "", fmt.Errorf("read unfulfilled checkpoint: %w", err)
	}
	if unfulfilled.UpdatedAt.Before(discovery.UpdatedAt) {
		return UnfulfilledStream, nil
	}
	return DiscoveryStream, nil
}

func (i *Ingestor) CollectStream(ctx context.Context, scope orders.Scope, stream string) (err error) {
	plan, err := i.planCollection(ctx, scope, stream)
	if err != nil {
		return err
	}
	lease, window, err := i.claimPageLease(ctx, plan.claim, plan.checkpoint, plan.window)
	if err != nil {
		return fmt.Errorf("claim order ingestion: %w", err)
	}
	if lease == nil {
		return nil
	}
	defer func() { err = errors.Join(err, i.releaseLease(ctx, *lease)) }()
	config, err := i.currentConfig(ctx, scope)
	if err != nil {
		return err
	}
	if config.Revision != lease.ConfigRevision || config.Scope != lease.Scope {
		return orders.ErrStale
	}
	ctx, cancel := ingestionContext(ctx, config, *lease)
	defer cancel()
	ctx, stopReadAuthority := i.ingestionReadAuthority(ctx, *lease)
	defer stopReadAuthority()
	return i.collectLeasedPage(ctx, config, plan.checkpoint, window, *lease)
}

type ingestionPlan struct {
	checkpoint orders.Checkpoint
	window     orders.OrderWindow
	claim      orders.IngestionClaimRequest
}

func (i *Ingestor) planCollection(ctx context.Context, scope orders.Scope, stream string) (ingestionPlan, error) {
	if !ingestionStream(stream) || scope.Validate() != nil {
		return ingestionPlan{}, orders.ErrInvalid
	}
	config, err := i.currentConfig(ctx, scope)
	if err != nil {
		return ingestionPlan{}, err
	}
	checkpoint, err := i.store.OrderCheckpoint(ctx, scope, stream)
	if err != nil {
		return ingestionPlan{}, fmt.Errorf("read order checkpoint: %w", err)
	}
	window, key := i.nextWindow(config, checkpoint, stream)
	now := i.options.Now()
	request := orders.IngestionClaimRequest{Scope: scope, Stream: stream, RefreshKey: key, WorkerID: i.options.WorkerID, Now: now, LeaseUntil: now.Add(i.options.LeaseDuration)}
	return ingestionPlan{checkpoint: checkpoint, window: window, claim: request}, nil
}

func (i *Ingestor) claimPageLease(ctx context.Context, request orders.IngestionClaimRequest, checkpoint orders.Checkpoint, window orders.OrderWindow) (*orders.IngestionLease, orders.OrderWindow, error) {
	lease, err := i.store.ClaimOrderIngestion(ctx, request)
	if err != nil || lease == nil {
		return lease, window, err
	}
	if checkpoint.Revision == 0 || checkpoint.Credentials == lease.Credentials || checkpoint.RefreshKey != lease.RefreshKey {
		return lease, window, nil
	}
	if err = i.releaseLease(ctx, *lease); err != nil {
		return nil, window, err
	}
	now := i.options.Now().UTC()
	window = i.anchoredWindow(request.Stream, now)
	request.RefreshKey = ingestionDigest(checkpoint.RefreshKey, "credentials", strconv.FormatInt(lease.Credentials.Ozon, 10), strconv.FormatInt(lease.Credentials.Moysklad, 10))
	request.Now, request.LeaseUntil = now, now.Add(i.options.LeaseDuration)
	lease, err = i.store.ClaimOrderIngestion(ctx, request)
	return lease, window, err
}

func (i *Ingestor) currentConfig(ctx context.Context, scope orders.Scope) (orders.Config, error) {
	config, err := i.store.OrderConfig(ctx, scope)
	if err != nil {
		return orders.Config{}, fmt.Errorf("read order ingestion config: %w", err)
	}
	if config.Scope != scope || config.Revision < 1 || !config.ReadsEnabled() {
		return orders.Config{}, orders.ErrBlocked
	}
	if !config.ReadEnabled && config.Automation != nil && !config.Automation.Active(i.options.Now(), scope, config.Revision) {
		return orders.Config{}, orders.ErrBlocked
	}
	return config, nil
}

func ingestionContext(ctx context.Context, config orders.Config, lease orders.IngestionLease) (context.Context, context.CancelFunc) {
	deadline := lease.LeaseUntil
	if grant := config.Automation; !config.ReadEnabled && grant != nil && !grant.Perpetual && grant.ExpiresAt.Before(deadline) {
		deadline = grant.ExpiresAt
	}
	return context.WithDeadline(ctx, deadline)
}

func (i *Ingestor) releaseLease(ctx context.Context, lease orders.IngestionLease) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := i.store.ReleaseOrderIngestion(ctx, lease)
	if errors.Is(err, orders.ErrStale) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("release order ingestion lease: %w", err)
	}
	return nil
}

func (i *Ingestor) nextWindow(config orders.Config, checkpoint orders.Checkpoint, stream string) (orders.OrderWindow, string) {
	now := i.options.Now().UTC()
	window := i.anchoredWindow(stream, now)
	if checkpoint.ConfigRevision == config.Revision && checkpoint.Revision > 0 && stream != ActiveStream && stream != orders.NativePointStream {
		if !checkpoint.Complete && !checkpoint.Exhausted {
			window.From, window.To = checkpoint.WindowFrom, checkpoint.WindowTo
			return window, checkpoint.RefreshKey
		}
		if stream == DiscoveryStream {
			window.From = checkpoint.WindowTo.Add(-i.options.Overlap)
		}
		if window.To.Sub(window.From) > 31*24*time.Hour {
			window.To = window.From.Add(31 * 24 * time.Hour)
		}
	}
	if (stream == ActiveStream || stream == orders.NativePointStream) && !checkpoint.Complete && !checkpoint.Exhausted && checkpoint.ConfigRevision == config.Revision && checkpoint.Revision > 0 {
		window.From, window.To = checkpoint.WindowFrom, checkpoint.WindowTo
		return window, checkpoint.RefreshKey
	}
	key := ingestionDigest(stream, window.From.Format(time.RFC3339Nano), window.To.Format(time.RFC3339Nano), strconv.FormatInt(config.Revision, 10), strconv.FormatInt(checkpoint.Revision, 10))
	return window, key
}

// anchoredWindow bounds every request to our existing 31-day local span.
// Assembly deadlines may be future-dated even for old-created active orders.
// Each completed cutoff traversal therefore starts a new now-anchored epoch;
// its future upper bound must never become the next epoch's lower bound.
// Neither the seven-day default nor the span asserts a provider API limit.
func (i *Ingestor) anchoredWindow(stream string, now time.Time) orders.OrderWindow {
	past, future := i.options.Lookback, time.Duration(0)
	if stream == UnfulfilledStream {
		future = i.options.UnfulfilledLookahead
		past = min(past, 31*24*time.Hour-future)
	}
	return orders.OrderWindow{Stream: stream, From: now.Add(-past), To: now.Add(future), Limit: i.options.PageSize}
}

func (i *Ingestor) collectLeasedPage(ctx context.Context, config orders.Config, checkpoint orders.Checkpoint, window orders.OrderWindow, lease orders.IngestionLease) error {
	cursor := ""
	if checkpoint.RefreshKey == lease.RefreshKey {
		cursor = checkpoint.Cursor
	}
	page := orders.SourcePage{Scope: config.Scope, ConfigRevision: config.Revision, Credentials: lease.Credentials, Stream: window.Stream,
		RefreshKey: lease.RefreshKey, ExpectedCheckpointRevision: checkpoint.Revision, ExpectedCursor: cursor, WindowFrom: window.From,
		WindowTo: window.To, StartedAt: i.options.Now(), FenceToken: lease.Token, FreshDetail: true,
		IdempotencyKey: ingestionDigest(lease.Token, lease.RefreshKey, cursor)}
	result, err := i.readLeasedPage(ctx, config.Scope, lease.Credentials, window, cursor)
	if err != nil && len(result.Postings) == 0 && len(result.Failures) == 0 {
		return err
	}
	readErr := err
	page.Postings, page.NextCursor, page.Complete = result.Postings, result.NextCursor, result.Complete
	page.NativeReads = result.NativeReads
	page.NativeDocumentReads = result.NativeDocumentReads
	page.NativeDocumentFailures = result.NativeDocumentFailures
	page.Failures, page.Exhausted = result.Failures, result.Exhausted
	retainRefreshFailures(&page, checkpoint)
	if ctx.Err() != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		ctx = cleanup
		if _, err = i.currentConfig(ctx, config.Scope); err != nil {
			return err
		}
	}
	return i.persistLeasedResult(ctx, config, &page, lease, readErr)
}

func (i *Ingestor) persistLeasedPage(ctx context.Context, config orders.Config, page *orders.SourcePage, lease orders.IngestionLease) error {
	// The protector uses the traversal finish time for secret retention. Setting
	// this only after sealing would create expired year-one secret timestamps.
	page.FinishedAt = i.options.Now()
	stampIngestionEvidence(page, lease)
	if err := i.protectPage(ctx, config, page); err != nil {
		return err
	}
	if err := i.renewLease(ctx, lease); err != nil {
		return err
	}
	if err := page.Validate(); err != nil {
		return fmt.Errorf("validate collected order page: %w", err)
	}
	if _, err := i.store.CommitOrderPage(ctx, *page); err != nil {
		return fmt.Errorf("commit order observations and checkpoint: %w", err)
	}
	return nil
}

func (i *Ingestor) readLeasedPage(ctx context.Context, scope orders.Scope, credentials orders.CredentialRevisions, window orders.OrderWindow, cursor string) (orders.OrderSourcePage, error) {
	if window.Stream == orders.NativePointStream {
		return i.readNativePointPage(ctx, scope, credentials, cursor)
	}
	if window.Stream != ActiveStream {
		return i.source.ReadOrderPage(ctx, scope, window, cursor)
	}
	active, err := i.store.ActiveOrderPostings(ctx, scope, i.options.ActiveBatch, cursor)
	if err != nil {
		return orders.OrderSourcePage{}, fmt.Errorf("read active posting keys: %w", err)
	}
	return i.readActivePage(ctx, scope, active), nil
}

func stampIngestionEvidence(page *orders.SourcePage, lease orders.IngestionLease) {
	for index := range page.Postings {
		evidence := &page.Postings[index].Evidence
		evidence.CollectionKind, evidence.CollectionID, evidence.LocalSequence = orders.CollectionSerialDetail, lease.Token, lease.Sequence
	}
}

func (i *Ingestor) protectPage(ctx context.Context, config orders.Config, page *orders.SourcePage) error {
	if !config.PIIEnabled {
		for index := range page.Postings {
			page.Postings[index].PII = nil
		}
		return nil
	}
	if i.options.Protector != nil {
		return i.options.Protector.ProtectOrderPage(ctx, config, page)
	}
	for _, posting := range page.Postings {
		if posting.PII != nil {
			return orders.ErrBlocked
		}
	}
	return nil
}

func (i *Ingestor) renewLease(ctx context.Context, lease orders.IngestionLease) error {
	valid, err := i.store.RenewOrderIngestion(ctx, lease, i.options.Now().Add(i.options.LeaseDuration))
	if err != nil {
		return fmt.Errorf("renew order ingestion fence: %w", err)
	}
	if !valid {
		return orders.ErrStale
	}
	return nil
}

func ingestionStream(stream string) bool {
	return stream == DiscoveryStream || stream == UnfulfilledStream || stream == ActiveStream || stream == orders.NativePointStream
}

func ingestionDigest(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(hash[:])
}
