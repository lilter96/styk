package postgres

import (
	"time"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func validateNativeMovementCurrent(command orders.Command, current orders.ReviewContext, automatic bool, now time.Time) error {
	movement := command.NativeMovement
	if !command.Action.IsNativeMovement() {
		return nil
	}
	if automatic || movement == nil || !movement.AllowedBy(current.Config.NativeDocuments, command.Action, now) {
		return orders.ErrBlocked
	}
	if ordermoney.NativeConsumptionAllows(command, current.NativeConsumption[command.PostingNumber]).Code != "" {
		return orders.ErrBlocked
	}
	if ordermoney.NativeDocumentAmounts(movement).Code != "" {
		return orders.ErrBlocked
	}
	baseline, found := current.PositionBaselines[command.PostingNumber]
	if !found || !baseline.Equal(movement.SourceBaseline) {
		return orders.ErrStale
	}
	config := current.Config
	if movement.OrganizationID != config.OrganizationID || movement.AgentID != config.AgentID || movement.StoreID != config.StoreID || config.CurrencyID != "" && movement.CurrencyID != config.CurrencyID {
		return orders.ErrStale
	}
	for _, line := range movement.Lines {
		if !orderProductMappingPresent(line.Mapping, current.ProductMappings) {
			return orders.ErrStale
		}
	}
	if command.Action == orders.ActionSalesReturnCreate {
		prior, found := current.NativeDocuments[movement.DemandWorkID]
		if !found || movement.DemandBasis == nil || prior.PostingNumber != command.PostingNumber || prior.Observation.Correlation != movement.DemandBasis.Correlation || prior.Observation.Scope != command.Scope {
			return orders.ErrStale
		}
	}
	return nil
}

func validateNativeDocumentCompletion(work orders.Work, completion orders.Completion) error {
	observation, target := completion.ObservedNativeDocument, completion.ObservedTarget
	if observation == nil || target == nil || !orders.NativeDocumentMatches(work.Command, *observation) || !orders.NativeParentReadback(work.Command, *target) || !ordermoney.NativeParentEffectObserved(work.Command, *target) || ordermoney.NativeDocumentTotal(work.Command, *observation).Code != "" {
		return orders.ErrStale
	}
	if work.Receipt.ExternalID != "" && work.Receipt.ExternalID != observation.Correlation.DocumentID {
		return orders.ErrStale
	}
	return nil
}
