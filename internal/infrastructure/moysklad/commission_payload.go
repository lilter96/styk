package moysklad

import (
	"encoding/json"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) commissionPayload(plan orders.CommissionPlan) (map[string]any, error) {
	if plan.Validate() != nil || !c.scopeAllowed(plan.Scope) || plan.Moment() == "" {
		return nil, orders.ErrInvalid
	}
	policy := plan.Policy
	body := map[string]any{"externalCode": plan.ExternalCode, "name": plan.Source.Number, "applicable": false, "organization": c.reference("entity/organization", "organization", policy.OrganizationID), "agent": c.reference("entity/counterparty", "counterparty", policy.AgentID), "contract": c.reference("entity/contract", "contract", policy.ContractID), "rate": map[string]any{"currency": c.reference("entity/currency", "currency", policy.CurrencyID), "value": json.Number(policy.RateValue)}, "moment": plan.Moment(), "commissionPeriodStart": plan.PeriodStart(), "commissionPeriodEnd": plan.PeriodEnd(), "vatEnabled": *policy.VATEnabled.Value, "vatIncluded": *policy.VATIncluded.Value, "rewardType": "None", "rewardPercent": 0, "returnToCommissionerPositions": []any{}}
	body["commissionOverhead"] = map[string]any{"sum": 0}
	if plan.References.OrganizationAccountID != "" {
		body["organizationAccount"] = c.reference("entity/organization/"+policy.OrganizationID+"/accounts", "account", plan.References.OrganizationAccountID)
	}
	if plan.References.AgentAccountID != "" {
		body["agentAccount"] = c.reference("entity/counterparty/"+policy.AgentID+"/accounts", "account", plan.References.AgentAccountID)
	}
	positions, err := c.commissionPayloadPositions(plan.Lines, policy)
	if err != nil {
		return nil, err
	}
	returned, err := c.commissionPayloadPositions(plan.Returns, policy)
	if err != nil {
		return nil, err
	}
	body["positions"], body["returnToCommissionerPositions"] = positions, returned
	if len(positions) == 0 {
		delete(body, "commissionOverhead")
	}
	return body, nil
}

func (c *OrderClient) commissionPayloadPositions(lines []orders.CommissionLine, policy orders.CommissionPolicy) ([]map[string]any, error) {
	positions := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		position, err := c.commissionPosition(line, policy)
		if err != nil {
			return nil, err
		}
		positions = append(positions, position)
	}
	return positions, nil
}

func (c *OrderClient) commissionPosition(line orders.CommissionLine, policy orders.CommissionPolicy) (map[string]any, error) {
	price, err := ordermoney.MoneyToMinor(line.Price)
	if err != nil {
		return nil, err
	}
	reward, err := ordermoney.CommissionMoneyToMinor(line.Reward)
	if err != nil {
		return nil, err
	}
	return map[string]any{"assortment": c.reference("entity/"+line.Mapping.MoyskladKind, line.Mapping.MoyskladKind, line.Mapping.MoyskladID), "quantity": json.Number(line.Quantity), "price": price, "reward": reward, "vat": json.Number(policy.VAT), "vatEnabled": *policy.VATEnabled.Value}, nil
}
