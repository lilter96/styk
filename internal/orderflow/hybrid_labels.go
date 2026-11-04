package orderflow

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Service) validateHybridLabel(ctx context.Context, posting orders.Posting, proposal orders.ActionProposal, now time.Time) []orders.Validation {
	if !orders.HybridLabelRequired(posting, proposal.Action) {
		return nil
	}
	if !proposal.Parameters.LabelPrintedConfirmed || proposal.Parameters.LabelWorkID == "" {
		return []orders.Validation{{Field: "label_work_id", Code: "hybrid_printed_label_required"}}
	}
	reader, ok := s.store.(orders.LabelProofReader)
	if !ok {
		return []orders.Validation{{Field: "label_work_id", Code: "hybrid_label_evidence_unavailable"}}
	}
	proof, err := reader.OrderLabelProof(ctx, posting.Scope, posting.PostingNumber, proposal.Parameters.LabelWorkID)
	if err != nil || proof.ValidateFor(posting, proposal.Parameters.LabelWorkID, now) != nil {
		return []orders.Validation{{Field: "label_work_id", Code: "hybrid_label_evidence_stale_or_mismatched"}}
	}
	return nil
}
