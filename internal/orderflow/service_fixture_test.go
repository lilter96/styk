package orderflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

var flowTestNow = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

type flowStore struct {
	review      orders.ReviewContext
	reviews     map[orders.Scope]orders.ReviewContext
	previews    map[string]orders.Preview
	approval    orders.Approval
	due         []orders.Config
	work        *orders.Work
	completions []orders.Completion
	beginDenied bool
	guardDenied bool
	staleSave   bool
	claims      int
	intents     int
}

func (s *flowStore) OrderConfig(_ context.Context, scope orders.Scope) (orders.Config, error) {
	review, err := s.OrderReviewContext(context.Background(), scope)
	return review.Config, err
}

func (s *flowStore) SaveOrderConfig(_ context.Context, request orders.SaveConfigRequest, _ time.Time) (orders.Config, error) {
	s.review.Config = request.Config
	return request.Config, nil
}

func (s *flowStore) DueOrderConfigs(context.Context, time.Time, int) ([]orders.Config, error) {
	return s.due, nil
}

func (s *flowStore) OrderReviewContext(_ context.Context, scope orders.Scope) (orders.ReviewContext, error) {
	if review, exists := s.reviews[scope]; exists {
		return review, nil
	}
	if s.review.Config.Scope != scope {
		return orders.ReviewContext{}, orders.ErrNotFound
	}
	return s.review, nil
}

func (s *flowStore) SaveOrderPreview(_ context.Context, preview orders.Preview) (orders.Preview, error) {
	if s.previews == nil {
		s.previews = make(map[string]orders.Preview)
	}
	s.previews[preview.ID] = preview
	return preview, nil
}

func (s *flowStore) OrderPreview(_ context.Context, scope orders.Scope, id string) (orders.Preview, error) {
	preview, exists := s.previews[id]
	if !exists || preview.Scope != scope {
		return orders.Preview{}, orders.ErrNotFound
	}
	return preview, nil
}

func (s *flowStore) ApproveOrderPreview(_ context.Context, approval orders.Approval, _ time.Time) (orders.Operation, error) {
	s.approval = approval
	return orders.Operation{ID: "operation", Scope: approval.Scope, PreviewID: approval.PreviewID, State: orders.StateQueued}, nil
}

func (s *flowStore) OrderPostings(context.Context, orders.Scope, orders.PageRequest) (orders.PostingPage, error) {
	return orders.PostingPage{Postings: s.review.Postings}, nil
}

func (s *flowStore) OrderHistory(context.Context, orders.Scope, orders.PageRequest) (orders.HistoryPage, error) {
	return orders.HistoryPage{}, nil
}

func (s *flowStore) ClaimOrderWork(_ context.Context, request orders.ClaimRequest) (*orders.Work, error) {
	if s.work == nil || s.work.State.Terminal() {
		return nil, nil
	}
	s.claims++
	s.work.ClaimToken, s.work.LeaseUntil = fmt.Sprintf("claim-%d", s.claims), request.LeaseUntil
	if s.work.State == orders.StateQueued {
		s.work.State = orders.StateClaimed
	}
	copyOfWork := *s.work
	return &copyOfWork, nil
}

func (s *flowStore) GuardOrderDispatch(_ context.Context, guard orders.DispatchGuard) (bool, error) {
	if s.guardDenied || guard.Begin && s.beginDenied {
		return false, nil
	}
	if guard.Begin {
		if s.work.State != orders.StateClaimed || s.work.ClaimToken != guard.Work.ClaimToken {
			return false, orders.ErrStale
		}
		s.work.State, s.work.DispatchCount = orders.StateDispatching, 1
		s.intents++
	}
	return true, nil
}

func (s *flowStore) CompleteOrderWork(_ context.Context, completion orders.Completion) error {
	if s.staleSave || s.work.State != completion.ExpectedState || s.work.ClaimToken != completion.ClaimToken {
		return orders.ErrStale
	}
	if !orders.CanTransition(completion.ExpectedState, completion.State) {
		return orders.ErrConflict
	}
	s.completions = append(s.completions, completion)
	s.work.State, s.work.Code = completion.State, completion.Code
	if completion.Receipt.State != "" {
		s.work.Receipt = completion.Receipt
	}
	return nil
}

type flowProvider struct {
	posting      orders.Posting
	target       orders.TargetObservation
	readErr      error
	correlation  orders.Correlation
	correlateErr error
	receipt      orders.Receipt
	writeErr     error
	authorize    func(context.Context) (bool, error)
	beforeGuard  func()
	onWrite      func()
	skipGuard    bool
	reads        int
	lookups      int
	writes       int
}

func (p *flowProvider) ReadOrderPosting(context.Context, orders.Scope, string) (orders.Posting, error) {
	p.reads++
	return p.posting, p.readErr
}

func (p *flowProvider) ReadOrderCorrelation(context.Context, orders.Correlation) (orders.Correlation, error) {
	p.lookups++
	return p.correlation, p.correlateErr
}

func (p *flowProvider) ReadCustomerOrder(context.Context, orders.Correlation) (orders.TargetObservation, error) {
	p.reads++
	return p.target, p.readErr
}

func (p *flowProvider) BindOrderIntent(_ orders.Command, authorize func(context.Context) (bool, error)) error {
	p.authorize = authorize
	return nil
}

func (p *flowProvider) WriteOrder(ctx context.Context, command orders.Command) (orders.Receipt, error) {
	if p.beforeGuard != nil {
		p.beforeGuard()
	}
	if !p.skipGuard {
		allowed, err := p.authorize(ctx)
		if err != nil || !allowed {
			return orders.Receipt{}, orders.ErrBlocked
		}
	}
	p.writes++
	if p.onWrite != nil {
		p.onWrite()
	}
	receipt := p.receipt
	if receipt.CommandID == "" {
		receipt.CommandID = command.ID
	}
	return receipt, p.writeErr
}

type flowResolver struct {
	source   *flowProvider
	target   *flowProvider
	badScope bool
}

func (r flowResolver) ResolveOrder(_ context.Context, scope orders.Scope, provider, account string, revision int64) (orders.Binding, error) {
	client := r.source
	if provider == "moysklad" {
		client = r.target
	}
	if client == nil {
		return orders.Binding{}, errors.New("provider unavailable")
	}
	if r.badScope {
		scope.Tenant = "other-tenant"
	}
	return orders.Binding{Scope: scope, Provider: provider, Account: account, CredentialRevision: revision, Client: client}, nil
}

func flowReview(t *testing.T, number string) orders.ReviewContext {
	t.Helper()
	scope := orders.Scope{Tenant: "tenant", OzonAccount: "ozon-account", MoyskladAccount: "moysklad-account"}
	config := orders.Config{Scope: scope, Revision: 1, Enabled: true, TestEnabled: true, IntervalSeconds: 300,
		SourceFreshnessSeconds: 300, PreviewTTLSeconds: 300, OrganizationID: "organization", AgentID: "agent", StoreID: "store",
		StateID: "state", SalesChannelID: "channel", CurrencyID: "11111111-1111-4111-8111-111111111111", FieldMappings: flowMappings()}
	line := orders.PostingLine{Identity: orders.LineIdentity{SKU: "sku", OfferID: "offer"}, Quantity: "2",
		Price: orders.Present(orders.Money{Amount: "12.34", Currency: "RUB"})}
	posting := orders.Posting{Scope: scope, CommercialOrderID: "commercial-order", PostingNumber: number, Status: orders.Present("awaiting_packaging"),
		Scheme: orders.Present("FBS"), WarehouseID: orders.Present("warehouse"), DeliveryMethodID: orders.Present("delivery-method"), Lines: []orders.PostingLine{line},
		Evidence: orders.EvidenceRevision{Revision: "opaque-source-version", Origin: orders.OriginOzon, ObservedAt: flowTestNow.Add(-24 * time.Hour), CheckedAt: flowTestNow}}
	product := orders.ProductMappingRef{ID: "1", Version: 1, Scope: scope, OzonProductID: "product-id", SKU: "sku", OfferID: "offer",
		MoyskladKind: "product", MoyskladID: "native-product", Confirmed: true, ConfirmedAt: flowTestNow.Add(-time.Hour)}
	warehouse := orders.WarehouseMappingRef{ID: "1", Version: 1, Scope: scope, OzonWarehouseID: "warehouse", MoyskladStoreID: "store", Confirmed: true, ConfirmedAt: flowTestNow.Add(-time.Hour)}
	correlation, err := orders.CorrelationFor(scope, number)
	if err != nil {
		t.Fatal(err)
	}
	return orders.ReviewContext{Config: config, Credentials: orders.CredentialRevisions{Ozon: 1, Moysklad: 2}, Postings: []orders.Posting{posting},
		ProductMappings: []orders.ProductMappingRef{product}, WarehouseMappings: []orders.WarehouseMappingRef{warehouse}, Correlations: []orders.Correlation{correlation}}
}

func flowMappings() []orders.FieldMapping {
	return []orders.FieldMapping{{Source: "posting_number", Target: "name"}, {Source: "lines.quantity", Target: "positions.quantity"},
		{Source: "lines.price", Target: "positions.price"}, {Source: "constant.zero", Target: "positions.reserve"}, {Source: "constant.false", Target: "applicable"}}
}

func flowService(t *testing.T, store *flowStore, resolver flowResolver) *Service {
	t.Helper()
	service, err := NewService(store, resolver, ServiceOptions{AllowScope: func(orders.Scope) bool { return true }, Now: func() time.Time { return flowTestNow }})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func flowPreview(t *testing.T, service *Service, review orders.ReviewContext) orders.Preview {
	t.Helper()
	preview, err := service.Preview(context.Background(), orders.PreviewRequest{Scope: review.Config.Scope, ExpectedRevision: review.Config.Revision, Actor: "authenticated-owner"})
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func flowWork(t *testing.T, preview orders.Preview) orders.Work {
	t.Helper()
	if len(preview.Decisions) != 1 || len(preview.Decisions[0].Commands) != 1 {
		t.Fatalf("expected one reviewed command, got %+v", preview.Decisions)
	}
	command := preview.Decisions[0].Commands[0]
	fingerprint, err := orders.CommandFingerprint(command)
	if err != nil {
		t.Fatal(err)
	}
	return orders.Work{ID: "work", OperationID: "operation", Scope: preview.Scope, PreviewID: preview.ID, ConfigRevision: preview.ConfigRevision,
		Credentials: preview.Credentials, Command: command, CommandFingerprint: fingerprint, State: orders.StateQueued}
}

func flowTarget(command orders.Command) orders.TargetObservation {
	correlation := command.Correlation
	correlation.CustomerOrderID = "native-order"
	positions := make([]orders.TargetPosition, 0, len(command.CustomerOrder.Positions))
	for index, position := range command.CustomerOrder.Positions {
		positions = append(positions, orders.TargetPosition{ID: fmt.Sprintf("native-position-%d", index), Identity: position.Identity,
			MoyskladKind: position.Mapping.MoyskladKind, MoyskladID: position.Mapping.MoyskladID, Quantity: position.Quantity,
			Reserve: position.Reserve, Price: position.Price, VAT: position.VAT})
	}
	return orders.TargetObservation{Scope: command.Scope, Correlation: correlation, Revision: "opaque-target-version", Fields: *command.CustomerOrder,
		Positions: positions, Complete: true, ObservedAt: flowTestNow}
}

func flowRunner(t *testing.T, store *flowStore, resolver flowResolver, service *Service) *Runner {
	t.Helper()
	runner, err := NewRunner(store, resolver, RunnerOptions{WorkerID: "worker", Service: service, AllowScope: func(orders.Scope) bool { return true }, Now: func() time.Time { return flowTestNow }})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}
