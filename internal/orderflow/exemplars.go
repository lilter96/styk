package orderflow

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (s *Service) validateExemplarProposal(ctx context.Context, posting orders.Posting, proposal orders.ActionProposal, credentials orders.CredentialRevisions, now time.Time) []orders.Validation {
	if proposal.Action != orders.ActionExemplarSet {
		return nil
	}
	if s.options.ExemplarReader == nil || proposal.Parameters.Exemplars == nil {
		return []orders.Validation{{Field: "exemplars", Code: "exemplar_prepared_snapshot_required"}}
	}
	base, secret, err := s.options.ExemplarReader.ReadOrderExemplars(ctx, posting.Scope, proposal.Parameters.ExemplarWorkID)
	if err != nil || !secret.Ready || secret.CredentialRevision != credentials.Ozon || !secret.ExpiresAt.After(now) {
		return []orders.Validation{{Field: "exemplar_work_id", Code: "exemplar_prepared_snapshot_stale"}}
	}
	hash, err := orders.PackingTargetFingerprint(posting)
	if err != nil || secret.TargetFingerprint != hash {
		return []orders.Validation{{Field: "exemplar_work_id", Code: "exemplar_target_or_units_changed"}}
	}
	if validations := orders.ExemplarPreservationInputValidation(*proposal.Parameters.Exemplars); len(validations) > 0 {
		return validations
	}
	complete, err := orders.ApplyExemplarInput(base, *proposal.Parameters.Exemplars, posting)
	if err != nil {
		return []orders.Validation{{Field: "exemplars", Code: "exemplar_unknown_or_duplicate_identifier"}}
	}
	orders.CompleteExemplarRequirements(&complete, posting)
	if orders.ExemplarValuesEqual(base, complete) {
		return []orders.Validation{{Field: "exemplars", Code: "exemplar_no_changes"}}
	}
	return orders.ValidateExemplarSet(complete, posting)
}
