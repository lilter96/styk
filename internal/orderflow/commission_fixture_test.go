package orderflow

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

type commissionFlowStore struct {
	*flowStore
	preview      orders.CommissionPreview
	financeWork  *orders.CommissionWork
	approval     orders.CommissionApproval
	completions  []orders.CommissionCompletion
	saves        int
	approved     int
	existing     bool
	observations []orders.RealizationReport
}

func (s *commissionFlowStore) ObserveCommissionReport(_ context.Context, report orders.RealizationReport) error {
	s.observations = append(s.observations, report)
	return nil
}

func (s *commissionFlowStore) CommissionReviewContext(ctx context.Context, scope orders.Scope) (orders.ReviewContext, error) {
	return s.OrderReviewContext(ctx, scope)
}
func (s *commissionFlowStore) SaveCommissionPreview(_ context.Context, preview orders.CommissionPreview) (orders.CommissionPreview, error) {
	s.saves++
	if s.existing {
		preview.ExistingWork = &orders.CommissionWork{ID: "already-enrolled"}
	}
	s.preview = preview
	return preview, nil
}
func (s *commissionFlowStore) CommissionPreview(_ context.Context, scope orders.Scope, id string) (orders.CommissionPreview, error) {
	if s.preview.ID != id || s.preview.Plan.Scope != scope {
		return orders.CommissionPreview{}, orders.ErrNotFound
	}
	return s.preview, nil
}
func (s *commissionFlowStore) ApproveCommissionPreview(_ context.Context, approval orders.CommissionApproval, now time.Time) (orders.CommissionWork, error) {
	s.approved++
	s.approval = approval
	s.financeWork = &orders.CommissionWork{ID: "finance-work", Scope: s.preview.Plan.Scope, Plan: s.preview.Plan, PreviewID: s.preview.ID, ConfigRevision: s.preview.Plan.ConfigRevision, Credentials: s.preview.Plan.Credentials, State: orders.StateQueued, CreatedAt: now, UpdatedAt: now}
	return *s.financeWork, nil
}
func (s *commissionFlowStore) CommissionHistory(context.Context, orders.Scope, int) ([]orders.CommissionWork, error) {
	if s.financeWork == nil {
		return nil, nil
	}
	return []orders.CommissionWork{*s.financeWork}, nil
}
func (s *commissionFlowStore) DueCommissionConfigs(context.Context, time.Time, int) ([]orders.Config, error) {
	return s.due, nil
}
func (s *commissionFlowStore) ClaimCommissionWork(_ context.Context, request orders.ClaimRequest) (*orders.CommissionWork, error) {
	if s.financeWork == nil || s.financeWork.State.Terminal() {
		return nil, nil
	}
	s.financeWork.ClaimToken, s.financeWork.LeaseUntil = "finance-claim", request.LeaseUntil
	if s.financeWork.State == orders.StateQueued {
		s.financeWork.State = orders.StateClaimed
	}
	copy := *s.financeWork
	return &copy, nil
}
func (s *commissionFlowStore) GuardCommissionDispatch(_ context.Context, work orders.CommissionWork, begin bool) (bool, error) {
	if work.State.ReadOnly() {
		return !s.guardDenied && s.review.Config.ReadsEnabled() && work.Credentials == s.review.Credentials, nil
	}
	if s.guardDenied || s.review.Config.Finance == nil || !s.review.Config.Finance.Enabled || work.ConfigRevision != s.review.Config.Revision {
		return false, nil
	}
	if begin {
		s.financeWork.State, s.financeWork.DispatchCount = orders.StateDispatching, 1
	}
	return true, nil
}
func (s *commissionFlowStore) CompleteCommissionWork(_ context.Context, result orders.CommissionCompletion) error {
	if result.ClaimToken != s.financeWork.ClaimToken || result.ExpectedState != s.financeWork.State {
		return orders.ErrStale
	}
	s.completions = append(s.completions, result)
	s.financeWork.State, s.financeWork.Code, s.financeWork.Receipt = result.State, result.Code, result.Receipt
	s.financeWork.SourceChanged = s.financeWork.SourceChanged || result.SourceChanged
	return nil
}

type commissionFlowProvider struct {
	*flowProvider
	report          orders.RealizationReport
	refs            orders.CommissionReferences
	actual          *orders.CommissionObservation
	beforeAuthorize func()
	writes          int
	reads           int
	nativeReads     int
	lost            bool
}

func (p *commissionFlowProvider) ReadRealizationReport(_ context.Context, scope orders.Scope, period orders.RealizationPeriod) (orders.RealizationReport, error) {
	p.reads++
	if p.report.Scope != scope || p.report.Period != period {
		return orders.RealizationReport{}, orders.ErrStale
	}
	return p.report, nil
}
func (p *commissionFlowProvider) ReadCommissionMetadata(_ context.Context, scope orders.Scope) (orders.CommissionMetadata, error) {
	return orders.CommissionMetadata{Scope: scope, Contracts: []orders.CommissionContract{}}, nil
}
func (p *commissionFlowProvider) ReadCommissionReferences(context.Context, orders.Scope, orders.CommissionPolicy) (orders.CommissionReferences, error) {
	return p.refs, nil
}
func (p *commissionFlowProvider) ReadCommissionDraft(context.Context, orders.CommissionPlan, string) (orders.CommissionObservation, error) {
	p.nativeReads++
	if p.actual == nil {
		return orders.CommissionObservation{}, orders.ErrNotFound
	}
	return *p.actual, nil
}
func (p *commissionFlowProvider) WriteCommissionDraft(ctx context.Context, plan orders.CommissionPlan, authorize func(context.Context) (bool, error)) (orders.Receipt, error) {
	if p.beforeAuthorize != nil {
		p.beforeAuthorize()
	}
	allowed, err := authorize(ctx)
	if err != nil || !allowed {
		return orders.Receipt{State: orders.StateBlocked}, err
	}
	p.writes++
	actual := commissionFlowObservation(plan)
	p.actual = &actual
	if p.lost {
		return orders.Receipt{CommandID: plan.ID, State: orders.StateUnknown}, errors.New("synthetic lost acknowledgement")
	}
	return orders.Receipt{CommandID: plan.ID, State: orders.StateAcknowledged, ExternalID: actual.DocumentID, HTTPStatus: 200}, nil
}

type commissionFlowResolver struct {
	provider  *commissionFlowProvider
	revisions orders.CredentialRevisions
}

func (r commissionFlowResolver) ResolveOrder(_ context.Context, scope orders.Scope, provider, account string, revision int64) (orders.Binding, error) {
	current := r.revisions.Ozon
	if provider == "moysklad" {
		current = r.revisions.Moysklad
	}
	if revision != 0 && revision != current {
		return orders.Binding{}, orders.ErrStale
	}
	return orders.Binding{Scope: scope, Provider: provider, Account: account, CredentialRevision: current, Client: r.provider}, nil
}

func commissionFlowFixture(t *testing.T) (*Service, *CommissionRunner, *commissionFlowStore, *commissionFlowProvider) {
	t.Helper()
	review := flowReview(t, "finance-posting")
	review.Config.ReadConfigured, review.Config.ReadEnabled = true, true
	uuid := func(value int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", value) }
	review.Config.Finance = &orders.CommissionPolicy{ID: "finance-policy", Scope: review.Config.Scope, Revision: 1, ConfigRevision: review.Config.Revision, OwnerID: "finance-owner", Enabled: true, Automatic: true, GrantedAt: flowTestNow.Add(-time.Hour), OrganizationID: uuid(101), AgentID: uuid(102), ContractID: uuid(103), CurrencyID: uuid(104), CurrencyISO: "RUB", RateValue: "1", PriceBasis: orders.CommissionSellerPrice, VATEnabled: orders.Present(false), VATIncluded: orders.Present(false), VAT: "0", OtherComponents: orders.CommissionZeroOtherComponents}
	policy := review.Config.Finance
	refs := orders.CommissionReferences{Scope: review.Config.Scope, OrganizationID: policy.OrganizationID, AgentID: policy.AgentID, ContractID: policy.ContractID, CurrencyID: policy.CurrencyID, CurrencyISO: "RUB", ContractType: "Commission", ContractOrganizationID: policy.OrganizationID, ContractAgentID: policy.AgentID, ContractCurrencyID: policy.CurrencyID, ContractRewardType: "None", Revision: "native-references-version", ObservedAt: flowTestNow, Complete: true}
	store := &commissionFlowStore{flowStore: &flowStore{review: review}}
	provider := &commissionFlowProvider{flowProvider: &flowProvider{}, refs: refs, report: commissionFlowReport(t, review.Config.Scope)}
	resolver := commissionFlowResolver{provider: provider, revisions: review.Credentials}
	service, err := NewService(store, resolver, ServiceOptions{AllowScope: func(orders.Scope) bool { return true }, Now: func() time.Time { return flowTestNow }})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewCommissionRunner(store, resolver, CommissionRunnerOptions{WorkerID: "finance-worker", Service: service, AllowScope: func(orders.Scope) bool { return true }, Now: func() time.Time { return flowTestNow }})
	if err != nil {
		t.Fatal(err)
	}
	return service, runner, store, provider
}

func commissionFlowReport(t *testing.T, scope orders.Scope) orders.RealizationReport {
	t.Helper()
	zero := orders.Present(orders.Decimal("0"))
	returns := orders.RealizationAmounts{Presence: orders.PresenceValue, Quantity: zero, Amount: zero, Bonus: zero, StandardFee: zero, BankCoinvestment: zero, PickUpPointCoinvestment: zero, Stars: zero, Total: zero}
	delivery := returns
	delivery.Quantity, delivery.Amount, delivery.Bonus, delivery.StandardFee, delivery.Total, delivery.PricePerInstance = orders.Present(orders.Decimal("2")), orders.Present(orders.Decimal("200")), orders.Present(orders.Decimal("5")), orders.Present(orders.Decimal("20")), orders.Present(orders.Decimal("185")), orders.Present(orders.Decimal("100"))
	report := orders.RealizationReport{Scope: scope, Period: orders.RealizationPeriod{Year: 2026, Month: 9}, Number: "finance-report", Currency: "RUB", DocumentDate: orders.Present("2026-10-05"), StartDate: orders.Present("2026-09-01"), StopDate: orders.Present("2026-09-30"), ObservedAt: flowTestNow, Rows: []orders.RealizationRow{{Number: 1, SKU: orders.Present("sku"), OfferID: orders.Present("offer"), SellerUnitPrice: orders.Present(orders.Decimal("100")), Delivery: delivery, Return: returns}}}
	var err error
	report.Fingerprint, err = ordermoney.CommissionSourceFingerprint(report)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func commissionFlowObservation(plan orders.CommissionPlan) orders.CommissionObservation {
	actual := orders.CommissionObservation{Scope: plan.Scope, DocumentID: "native-commission-document", ExternalCode: plan.ExternalCode, References: plan.References, RewardType: "None", RateValue: plan.Policy.RateValue, Moment: plan.Moment(), PeriodStart: plan.PeriodStart(), PeriodEnd: plan.PeriodEnd(), Gross: orders.Money{Amount: plan.Totals.Gross, Currency: plan.Totals.Currency}, Net: orders.Money{Amount: plan.Totals.Net, Currency: plan.Totals.Currency}, Revision: "document-version", ObservedAt: flowTestNow, Complete: true, VATEnabled: *plan.Policy.VATEnabled.Value, VATIncluded: *plan.Policy.VATIncluded.Value}
	for _, line := range plan.Lines {
		actual.Lines = append(actual.Lines, orders.CommissionObservedLine{ID: "native-commission-line", ItemKind: line.Mapping.MoyskladKind, ItemID: line.Mapping.MoyskladID, Quantity: line.Quantity, Price: line.Price, Reward: line.Reward, VAT: plan.Policy.VAT, VATEnabled: *plan.Policy.VATEnabled.Value})
	}
	for _, line := range plan.Returns {
		actual.Returns = append(actual.Returns, orders.CommissionObservedLine{ID: "native-commission-return-line", ItemKind: line.Mapping.MoyskladKind, ItemID: line.Mapping.MoyskladID, Quantity: line.Quantity, Price: line.Price, Reward: line.Reward, VAT: plan.Policy.VAT, VATEnabled: *plan.Policy.VATEnabled.Value})
	}
	actual.ReturnCount = len(actual.Returns)
	return actual
}
