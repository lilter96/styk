package orderflow

import (
	"context"
	"testing"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

type hybridProofStore struct {
	*flowStore
	proof orders.LabelProof
	err   error
}

func (s *hybridProofStore) OrderLabelProof(context.Context, orders.Scope, string, string) (orders.LabelProof, error) {
	return s.proof, s.err
}

func TestHybridPreviewRequiresStoredExactLabelEvidence(t *testing.T) {
	t.Parallel()
	review := flowReview(t, "posting")
	posting := review.Postings[0]
	posting.Delivery.IntegrationType = orders.Present("hybrid")
	proposal := orders.ActionProposal{Action: orders.ActionPostingShip, Parameters: orders.ActionParameters{LabelWorkID: "label-work", LabelPrintedConfirmed: true}}
	store := &hybridProofStore{flowStore: &flowStore{review: review}, proof: orders.LabelProof{Scope: posting.Scope, WorkID: "label-work", TaskID: "99", SourceRevision: posting.Evidence.Revision, PostingNumber: posting.PostingNumber, CommercialOrderID: posting.CommercialOrderID, WarehouseID: *posting.WarehouseID.Value, DeliveryMethodID: *posting.DeliveryMethodID.Value, ExpiresAt: flowTestNow.Add(time.Hour)}}
	service := &Service{store: store}
	if got := service.validateHybridLabel(context.Background(), posting, proposal, flowTestNow); len(got) != 0 {
		t.Fatalf("valid proof blocked: %+v", got)
	}
	store.proof.Scope.Tenant = "other"
	if len(service.validateHybridLabel(context.Background(), posting, proposal, flowTestNow)) == 0 {
		t.Fatal("foreign label accepted")
	}
	store.proof.Scope = posting.Scope
	store.err = orders.ErrNotFound
	if len(service.validateHybridLabel(context.Background(), posting, proposal, flowTestNow)) == 0 {
		t.Fatal("missing label treated as printed")
	}
	store.err = nil
	proposal.Parameters.LabelPrintedConfirmed = false
	if len(service.validateHybridLabel(context.Background(), posting, proposal, flowTestNow)) == 0 {
		t.Fatal("label generation implied physical printing")
	}
}
