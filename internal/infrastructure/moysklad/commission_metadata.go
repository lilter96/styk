package moysklad

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

var commissionReadPath = regexp.MustCompile(`^entity/((organization|counterparty|currency|contract)/[0-9a-fA-F-]{36}|(organization|counterparty)/[0-9a-fA-F-]{36}/accounts(/[0-9a-fA-F-]{36})?|commissionreportin/[0-9a-fA-F-]{36}(/positions|/returntocommissionerpositions)?)$`)

func allowedCommissionReadPath(path string) bool {
	return path == "entity/contract" || path == "entity/commissionreportin" || commissionReadPath.MatchString(path)
}

type commissionContractDTO struct {
	catalogReference
	ID           string           `json:"id"`
	AccountID    string           `json:"accountId"`
	Name         string           `json:"name"`
	ContractType string           `json:"contractType"`
	RewardType   string           `json:"rewardType"`
	Archived     *bool            `json:"archived"`
	Agent        catalogReference `json:"agent"`
	OwnAgent     catalogReference `json:"ownAgent"`
	Rate         struct {
		Currency catalogReference `json:"currency"`
	} `json:"rate"`
	OrganizationAccount catalogReference `json:"organizationAccount"`
	AgentAccount        catalogReference `json:"agentAccount"`
}

func (c *OrderClient) ReadCommissionMetadata(ctx context.Context, scope orders.Scope) (orders.CommissionMetadata, error) {
	if !c.scopeAllowed(scope) {
		return orders.CommissionMetadata{}, ErrOrderIdentity
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return orders.CommissionMetadata{}, err
	}
	raw, err := c.client.get(ctx, "entity/contract", url.Values{"limit": {"100"}, "offset": {"0"}, "order": {"id,asc"}})
	if err != nil {
		return orders.CommissionMetadata{}, err
	}
	collection, err := commissionCollection(raw, 100, 0)
	if err != nil {
		return orders.CommissionMetadata{}, err
	}
	result := orders.CommissionMetadata{Scope: scope, Contracts: make([]orders.CommissionContract, 0, len(*collection.Rows)), Truncated: *collection.Meta.Size > len(*collection.Rows), ObservedAt: time.Now().UTC()}
	seen := make(map[string]bool, len(*collection.Rows))
	for _, row := range *collection.Rows {
		contract, e := c.decodeCommissionContract(row)
		if e != nil || seen[contract.ID] {
			return orders.CommissionMetadata{}, ErrOrderIdentity
		}
		seen[contract.ID] = true
		if contract.ContractType == "Commission" && !contract.Archived && contract.RewardType == "None" {
			result.Contracts = append(result.Contracts, contract)
		}
	}
	return result, nil
}

func (c *OrderClient) decodeCommissionContract(raw []byte) (orders.CommissionContract, error) {
	var dto commissionContractDTO
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &dto) != nil || dto.AccountID != c.scope.MoyskladAccount || dto.Archived == nil {
		return orders.CommissionContract{}, ErrOrderSchema
	}
	id, err := c.referenceID(dto.catalogReference, "entity/contract", "contract")
	if err != nil || id != dto.ID {
		return orders.CommissionContract{}, ErrOrderIdentity
	}
	result := orders.CommissionContract{ID: id, Name: dto.Name, ContractType: dto.ContractType, RewardType: dto.RewardType, Archived: *dto.Archived}
	if dto.ContractType != "Commission" {
		return result, nil
	}
	result.OrganizationID, err = c.referenceID(dto.OwnAgent, "entity/organization", "organization")
	if err != nil {
		return result, err
	}
	result.AgentID, err = c.referenceID(dto.Agent, "entity/counterparty", "counterparty")
	if err != nil {
		return result, err
	}
	result.CurrencyID, err = c.referenceID(dto.Rate.Currency, "entity/currency", "currency")
	if err != nil {
		return result, err
	}
	if dto.OrganizationAccount.Meta.Href != "" {
		result.OrganizationAccountID, err = c.referenceID(dto.OrganizationAccount, "entity/organization/"+result.OrganizationID+"/accounts", "account")
		if err != nil {
			return result, err
		}
	}
	if dto.AgentAccount.Meta.Href != "" {
		result.AgentAccountID, err = c.referenceID(dto.AgentAccount, "entity/counterparty/"+result.AgentID+"/accounts", "account")
	}
	return result, err
}

func commissionCollection(raw []byte, limit, offset int) (page, error) {
	var result page
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &result) != nil || result.Meta == nil || result.Rows == nil || result.Meta.Size == nil || result.Meta.Offset == nil || result.Meta.Limit == nil {
		return result, ErrOrderSchema
	}
	if *result.Meta.Size < 0 || *result.Meta.Offset != offset || *result.Meta.Limit != limit || len(*result.Rows) > limit || offset+len(*result.Rows) > *result.Meta.Size || len(*result.Rows) == 0 && offset < *result.Meta.Size {
		return result, ErrOrderSchema
	}
	return result, nil
}

func commissionOffset(cursor int) url.Values {
	return url.Values{"limit": {"100"}, "offset": {strconv.Itoa(cursor)}}
}
