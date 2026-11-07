package moysklad

import (
	"context"

	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) commissionOrderContract(ctx context.Context, write orders.CustomerOrderWrite) error {
	if write.ContractID == "" {
		return nil
	}
	raw, _, err := c.commissionEntity(ctx, "contract", write.ContractID)
	if err != nil {
		return err
	}
	contract, err := c.decodeCommissionContract(raw)
	if err != nil {
		return err
	}
	if contract.Archived || contract.ContractType != "Commission" || contract.OrganizationID != write.OrganizationID || contract.AgentID != write.AgentID || contract.CurrencyID != write.CurrencyID || contract.RewardType != "None" {
		return orders.ErrBlocked
	}
	return nil
}
