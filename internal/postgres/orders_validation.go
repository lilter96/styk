package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/lilter96/styk/internal/orders"
)

func ordersGrantOwner(ctx context.Context, tx pgx.Tx, config orders.Config, actor string) (string, error) {
	grant := config.Automation
	if grant == nil || actor != orderAutomationActor(*grant) {
		return "", orders.ErrBlocked
	}
	if err := ordersOwnerCurrent(ctx, tx, config.Scope.Tenant, grant.OwnerID); err != nil {
		return "", err
	}
	now, err := ordersClock(ctx, tx)
	if err != nil {
		return "", err
	}
	if !config.Enabled || !grant.Active(now, config.Scope, config.Revision) {
		return "", orders.ErrBlocked
	}
	var recorded bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM orders_automation_grants WHERE id=$1 AND revision=$2 AND tenant_id=$3 AND moysklad_account=$4 AND ozon_account=$5 AND config_revision=$6 AND owner_user_id=$7 AND revoked_at IS NULL AND expires_at>clock_timestamp())`, grant.ID, grant.Revision, config.Scope.Tenant, config.Scope.MoyskladAccount, config.Scope.OzonAccount, config.Revision, grant.OwnerID).Scan(&recorded)
	if err != nil {
		return "", ordersError("check recorded order grant", err)
	}
	if !recorded {
		return "", orders.ErrBlocked
	}
	return grant.OwnerID, nil
}

func ordersOwnerCurrent(ctx context.Context, tx pgx.Tx, tenant, owner string) error {
	_, err := tx.Exec(ctx, `SELECT 1 FROM saas_merchant_accounts a JOIN saas_users u ON u.id=a.user_id JOIN saas_memberships m ON m.workspace_id=a.tenant_id AND m.user_id=a.user_id JOIN saas_workspaces w ON w.id=a.tenant_id WHERE a.tenant_id=$1 AND a.user_id=$2 FOR SHARE OF a,u,m,w`, tenant, owner)
	if err != nil {
		return ordersError("lock order grant owner", err)
	}
	var current bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saas_merchant_accounts a JOIN saas_users u ON u.id=a.user_id JOIN saas_memberships m ON m.workspace_id=a.tenant_id AND m.user_id=a.user_id JOIN saas_workspaces w ON w.id=a.tenant_id WHERE a.tenant_id=$1 AND a.user_id=$2 AND u.disabled_at IS NULL AND u.email_verified_at IS NOT NULL AND m.role='OWNER' AND m.revoked_at IS NULL AND NOT w.legacy_unclaimed)`, tenant, owner).Scan(&current)
	if err != nil {
		return ordersError("check current order grant owner", err)
	}
	if !current {
		return orders.ErrBlocked
	}
	return nil
}

func validateOrderDecision(decision orders.Decision, current orders.ReviewContext, automatic bool, now time.Time) error {
	for _, command := range decision.Commands {
		if err := validateOrderCommandCurrent(command, decision.Posting, current, automatic, now); err != nil {
			return err
		}
	}
	return nil
}

func validateOrderCommandCurrent(command orders.Command, posting orders.Posting, current orders.ReviewContext, automatic bool, now time.Time) error {
	if err := command.Validate(); err != nil {
		return err
	}
	if command.Scope != current.Config.Scope || command.ConfigRevision != current.Config.Revision || command.Credentials != current.Credentials || command.PostingNumber != posting.PostingNumber || command.CommercialOrderID != posting.CommercialOrderID || command.ExpectedSourceRevision != posting.Evidence.Revision || command.SourceOrigin != posting.Evidence.Origin {
		return orders.ErrStale
	}
	if err := validateOrderCorrelationCurrent(command, current.Correlations); err != nil {
		return err
	}
	if err := validateOrderCommandMappings(command, current); err != nil {
		return err
	}
	if command.CustomerOrder != nil {
		if err := validateOrderNativeWrite(*command.CustomerOrder, current); err != nil {
			return err
		}
	}
	if err := validateOrderPolicyFields(command, current.Config); err != nil {
		return err
	}
	if automatic {
		return validateOrderAutomaticCommand(command, current.Config, now)
	}
	return nil
}

func validateOrderPolicyFields(command orders.Command, config orders.Config) error {
	if !command.Action.IsMirror() {
		return nil
	}
	allowed := make(map[string]bool, len(config.FieldMappings)+1)
	allowed["state"] = true
	for _, mapping := range config.FieldMappings {
		if strings.HasPrefix(mapping.Source, "customer.") {
			return orders.ErrBlocked
		}
		allowed[mapping.Target] = true
	}
	for _, field := range command.Fields() {
		if !allowed[field] {
			return orders.ErrBlocked
		}
	}
	return nil
}

func validateOrderCorrelationCurrent(command orders.Command, correlations []orders.Correlation) error {
	for _, correlation := range correlations {
		if correlation.PostingNumber != command.PostingNumber {
			continue
		}
		if correlation != command.Correlation {
			return orders.ErrStale
		}
		return nil
	}
	return orders.ErrStale
}

func validateOrderCommandMappings(command orders.Command, current orders.ReviewContext) error {
	if command.Warehouse.ID != "" {
		found := false
		for _, mapping := range current.WarehouseMappings {
			if orderWarehouseMappingSame(command.Warehouse, mapping) {
				found = true
			}
		}
		if !found {
			return orders.ErrStale
		}
	}
	if command.CustomerOrder == nil {
		return nil
	}
	for _, position := range command.CustomerOrder.Positions {
		if !orderProductMappingPresent(position.Mapping, current.ProductMappings) {
			return orders.ErrStale
		}
	}
	return nil
}

func orderProductMappingPresent(expected orders.ProductMappingRef, mappings []orders.ProductMappingRef) bool {
	for _, actual := range mappings {
		if expected.ID == actual.ID && expected.Version == actual.Version && expected.Scope == actual.Scope && expected.OzonProductID == actual.OzonProductID && expected.OfferID == actual.OfferID && (expected.SKU == "" || expected.SKU == actual.SKU) && expected.MoyskladKind == actual.MoyskladKind && expected.MoyskladID == actual.MoyskladID && actual.Confirmed {
			return true
		}
	}
	return false
}

func orderWarehouseMappingSame(expected, actual orders.WarehouseMappingRef) bool {
	return expected.ID == actual.ID && expected.Version == actual.Version && expected.Scope == actual.Scope && expected.OzonWarehouseID == actual.OzonWarehouseID && expected.MoyskladStoreID == actual.MoyskladStoreID && actual.Confirmed
}

func validateOrderNativeWrite(write orders.CustomerOrderWrite, current orders.ReviewContext) error {
	config := current.Config
	if write.OrganizationID != config.OrganizationID || write.AgentID != config.AgentID || write.StoreID != config.StoreID || write.SalesChannelID != config.SalesChannelID {
		return orders.ErrStale
	}
	allowedState := write.StateID == config.StateID
	for _, mapping := range config.StatusMappings {
		allowedState = allowedState || write.StateID == mapping.StateID
	}
	if !allowedState {
		return orders.ErrStale
	}
	for _, attribute := range write.Attributes {
		if !orderAttributeAllowed(attribute.AttributeID, current) {
			return orders.ErrBlocked
		}
	}
	return nil
}

func orderAttributeAllowed(id string, current orders.ReviewContext) bool {
	configured := false
	for _, mapping := range current.Config.FieldMappings {
		configured = configured || (mapping.Target == "attribute" && mapping.AttributeID == id)
	}
	for _, attribute := range current.Metadata.Attributes {
		if attribute.AttributeID == id && configured {
			return true
		}
	}
	return false
}

func validateOrderAutomaticCommand(command orders.Command, config orders.Config, now time.Time) error {
	grant := config.Automation
	if grant == nil || !grant.Allows(command, now) {
		return orders.ErrBlocked
	}
	return nil
}

func ordersQuarantined(posting orders.Posting) bool {
	for _, drift := range posting.Drift {
		if strings.HasPrefix(drift.Code, "ORDERING_") {
			return true
		}
	}
	return false
}
