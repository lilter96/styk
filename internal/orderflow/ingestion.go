package orderflow

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

const (
	DiscoveryStream   = "ozon-orders-discovery"
	UnfulfilledStream = "ozon-orders-unfulfilled"
	ActiveStream      = "ozon-orders-active"
)

// OrderWindow is a bounded creation-time or assembly-cutoff traversal, never an update watermark.
type OrderWindow = orders.OrderWindow
type OrderSourcePage = orders.OrderSourcePage

// SourceReader reads one page and separately refreshes persisted active keys.
// It cannot issue a provider mutation.
type SourceReader interface {
	ReadOrderPage(context.Context, orders.Scope, OrderWindow, string) (OrderSourcePage, error)
	ReadOrderPosting(context.Context, orders.Scope, string) (orders.Posting, error)
}

type ActivePostingReader interface {
	ActiveOrderPostings(context.Context, orders.Scope, int, string) (orders.PostingPage, error)
}

type NativePointCandidateReader interface {
	NativeOrderCandidates(context.Context, orders.Scope, int, string) (orders.NativePointCandidates, error)
}

type OrderConfigReader interface {
	OrderConfig(context.Context, orders.Scope) (orders.Config, error)
}

type IngestionStore interface {
	OrderConfigReader
	orders.IngestionPageStore
	orders.IngestionClaimer
	ActivePostingReader
}

type PageProtector interface {
	ProtectOrderPage(context.Context, orders.Config, *orders.SourcePage) error
}

type IngestionOptions struct {
	WorkerID       string
	NativeResolver orders.Resolver
	NativeBatch    int
	// UnfulfilledLookahead is a local discovery horizon, not a provider maximum.
	// Zero selects seven days; values above thirty days are rejected locally.
	UnfulfilledLookahead time.Duration
	Lookback             time.Duration
	Overlap              time.Duration
	LeaseDuration        time.Duration
	PageSize             int
	ActiveBatch          int
	Protector            PageProtector
	Now                  func() time.Time
}

type Ingestor struct {
	store   IngestionStore
	source  SourceReader
	options IngestionOptions
}

func NewIngestor(store IngestionStore, source SourceReader, options IngestionOptions) (*Ingestor, error) {
	options = defaultIngestionOptions(options)
	if store == nil || source == nil || !validIngestionOptions(options) {
		return nil, orders.ErrInvalid
	}
	if options.NativeResolver != nil {
		if _, ok := store.(NativePointCandidateReader); !ok {
			return nil, orders.ErrInvalid
		}
	}
	return &Ingestor{store: store, source: source, options: options}, nil
}

func defaultIngestionOptions(options IngestionOptions) IngestionOptions {
	if options.NativeBatch == 0 {
		options.NativeBatch = 10
	}
	if options.UnfulfilledLookahead == 0 {
		options.UnfulfilledLookahead = 7 * 24 * time.Hour
	}
	if options.Lookback == 0 {
		options.Lookback = 31 * 24 * time.Hour
	}
	if options.Overlap == 0 {
		options.Overlap = time.Hour
	}
	if options.LeaseDuration == 0 {
		options.LeaseDuration = 3 * time.Minute
	}
	if options.PageSize == 0 {
		options.PageSize = 25
	}
	if options.ActiveBatch == 0 {
		options.ActiveBatch = 10
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	return options
}

func validIngestionOptions(options IngestionOptions) bool {
	return options.NativeBatch > 0 && options.NativeBatch <= orders.MaxPagePostings && options.UnfulfilledLookahead > 0 && options.UnfulfilledLookahead <= 30*24*time.Hour &&
		options.WorkerID != "" && options.Lookback > 0 && options.Lookback <= 31*24*time.Hour &&
		options.Overlap > 0 && options.Overlap < options.Lookback && options.LeaseDuration > 0 && options.LeaseDuration <= 5*time.Minute &&
		options.PageSize > 0 && options.PageSize <= orders.MaxPagePostings && options.ActiveBatch > 0 && options.ActiveBatch <= orders.MaxPagePostings
}
