package moysklad

import (
	"encoding/json"
	"net/url"

	"github.com/lilter96/styk/internal/ordermoney"
	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) commissionHeader(plan orders.CommissionPlan, doc commissionDocumentDTO, fields map[string]json.RawMessage, refs orders.CommissionReferences) (orders.CommissionObservation, error) {
	result := orders.CommissionObservation{Scope: plan.Scope, DocumentID: doc.ID, ExternalCode: doc.ExternalCode, References: refs, RateValue: orders.Decimal(doc.Rate.Value)}
	if doc.Updated == "" || result.RateValue.Validate() != nil {
		return result, ErrOrderSchema
	}
	if err := commissionNoExtraExpenses(fields, len(plan.Lines) == 0 && len(plan.Returns) > 0); err != nil {
		return result, err
	}
	if err := c.commissionHeaderReferences(doc, refs); err != nil {
		return result, err
	}
	if err := c.commissionPositionMetadata(doc); err != nil {
		return result, err
	}
	result.ReturnCount = *doc.Returns.Meta.Size
	fieldsToRead := []struct {
		key string
		out any
	}{{"applicable", &result.Applicable}, {"vatEnabled", &result.VATEnabled}, {"vatIncluded", &result.VATIncluded}, {"rewardType", &result.RewardType}, {"commissionPeriodStart", &result.PeriodStart}, {"commissionPeriodEnd", &result.PeriodEnd}, {"moment", &result.Moment}}
	for _, field := range fieldsToRead {
		raw, exists := fields[field.key]
		if !exists || string(raw) == "null" || json.Unmarshal(raw, field.out) != nil {
			return result, ErrOrderSchema
		}
	}
	var err error
	result.Gross, err = commissionReadMoney(fields, "sum", plan.Policy.CurrencyISO)
	if err != nil {
		return result, err
	}
	result.Net, err = commissionReadMoney(fields, "commitentSum", plan.Policy.CurrencyISO)
	return result, err
}

func commissionNoExtraExpenses(fields map[string]json.RawMessage, withoutSales bool) error {
	if _, deleted := fields["deleted"]; deleted {
		return orders.ErrBlocked
	}
	if _, present := fields["commissionOverhead"]; !present && withoutSales {
		return nil
	}
	var overhead map[string]json.RawMessage
	if json.Unmarshal(fields["commissionOverhead"], &overhead) != nil {
		return ErrOrderSchema
	}
	value, err := orderReadDecimal(overhead, "sum")
	if err != nil || !value.IsKnown() || !orders.ExactDecimalEqual(*value.Value, "0") {
		return orders.ErrBlocked
	}
	return nil
}

func (c *OrderClient) commissionHeaderReferences(doc commissionDocumentDTO, refs orders.CommissionReferences) error {
	references := []struct {
		ref                catalogReference
		path, kind, wanted string
	}{{doc.Organization, "entity/organization", "organization", refs.OrganizationID}, {doc.Agent, "entity/counterparty", "counterparty", refs.AgentID}, {doc.Contract, "entity/contract", "contract", refs.ContractID}, {doc.Rate.Currency, "entity/currency", "currency", refs.CurrencyID}, {doc.OrganizationAccount, "entity/organization/" + refs.OrganizationID + "/accounts", "account", refs.OrganizationAccountID}, {doc.AgentAccount, "entity/counterparty/" + refs.AgentID + "/accounts", "account", refs.AgentAccountID}}
	for _, ref := range references {
		if ref.wanted == "" {
			if ref.ref.Meta.Href != "" {
				return ErrOrderIdentity
			}
			continue
		}
		actual, err := c.referenceID(ref.ref, ref.path, ref.kind)
		if err != nil || actual != ref.wanted {
			return ErrOrderIdentity
		}
	}
	return nil
}

func (c *OrderClient) commissionPositionMetadata(doc commissionDocumentDTO) error {
	entries := []struct {
		meta   commissionPositionMeta
		suffix string
	}{{doc.Positions, "positions"}, {doc.Returns, "returntocommissionerpositions"}}
	for _, entry := range entries {
		expected := c.client.base.ResolveReference(&url.URL{Path: "entity/commissionreportin/" + doc.ID + "/" + entry.suffix}).String()
		if entry.meta.Meta.Size == nil || *entry.meta.Meta.Size < 0 || *entry.meta.Meta.Size > orders.MaxCommissionLines || entry.meta.Meta.Href != expected {
			return ErrOrderSchema
		}
	}
	return nil
}

func commissionReadMoney(fields map[string]json.RawMessage, key, currency string) (orders.Money, error) {
	raw, exists := fields[key]
	if !exists || string(raw) == "null" {
		return orders.Money{}, ErrOrderSchema
	}
	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return orders.Money{}, ErrOrderSchema
	}
	return ordermoney.CommissionMoneyFromMinor(number, currency)
}
