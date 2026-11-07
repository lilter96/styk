package moysklad

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"time"

	"github.com/lilter96/styk/internal/orders"
)

func (c *OrderClient) ReadCommissionReferences(ctx context.Context, scope orders.Scope, policy orders.CommissionPolicy) (orders.CommissionReferences, error) {
	if !c.scopeAllowed(scope) || !policy.Enabled || policy.Scope != scope || policy.Validate() != nil {
		return orders.CommissionReferences{}, ErrOrderIdentity
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return orders.CommissionReferences{}, err
	}
	return c.readCommissionReferences(ctx, policy)
}

func (c *OrderClient) readCommissionReferences(ctx context.Context, policy orders.CommissionPolicy) (orders.CommissionReferences, error) {
	result := orders.CommissionReferences{Scope: c.scope, OrganizationID: policy.OrganizationID, AgentID: policy.AgentID, ContractID: policy.ContractID, CurrencyID: policy.CurrencyID, CurrencyISO: policy.CurrencyISO}
	parts := make([]string, 0, 6)
	org, digest, err := c.commissionEntity(ctx, "organization", policy.OrganizationID)
	if err != nil {
		return result, err
	}
	parts = append(parts, digest)
	agent, digest, err := c.commissionEntity(ctx, "counterparty", policy.AgentID)
	if err != nil {
		return result, err
	}
	parts = append(parts, digest)
	contractRaw, digest, err := c.commissionEntity(ctx, "contract", policy.ContractID)
	if err != nil {
		return result, err
	}
	parts = append(parts, digest)
	contract, err := c.decodeCommissionContract(contractRaw)
	if err != nil || contract.Archived {
		return result, ErrOrderIdentity
	}
	result.ContractType, result.ContractOrganizationID, result.ContractAgentID, result.ContractCurrencyID, result.ContractRewardType = contract.ContractType, contract.OrganizationID, contract.AgentID, contract.CurrencyID, contract.RewardType
	currency, digest, err := c.commissionEntity(ctx, "currency", policy.CurrencyID)
	if err != nil {
		return result, err
	}
	parts = append(parts, digest)
	var currencyDTO struct {
		ISOCode string `json:"isoCode"`
	}
	if json.Unmarshal(currency, &currencyDTO) != nil || currencyDTO.ISOCode != policy.CurrencyISO {
		return result, ErrOrderIdentity
	}
	return c.commissionReferenceAccounts(ctx, policy, result, org, agent, parts)
}

func (c *OrderClient) commissionReferenceAccounts(ctx context.Context, policy orders.CommissionPolicy, result orders.CommissionReferences, org, agent []byte, parts []string) (orders.CommissionReferences, error) {
	var digest string
	var err error
	result.OrganizationAccountID, digest, err = c.commissionPartyAccount(ctx, "organization", policy.OrganizationID, policy.OrganizationAccountID, policy.CurrencyID, org)
	if err != nil {
		return result, err
	}
	parts = append(parts, digest)
	result.AgentAccountID, digest, err = c.commissionPartyAccount(ctx, "counterparty", policy.AgentID, policy.AgentAccountID, policy.CurrencyID, agent)
	if err != nil {
		return result, err
	}
	parts = append(parts, digest)
	raw, err := json.Marshal(parts)
	if err != nil {
		return result, err
	}
	hash := sha256.Sum256(raw)
	result.Revision, result.ObservedAt, result.Complete = hex.EncodeToString(hash[:]), time.Now().UTC(), true
	if !result.Matches(policy) {
		return result, ErrOrderIdentity
	}
	return result, nil
}

func (c *OrderClient) commissionEntity(ctx context.Context, kind, id string) ([]byte, string, error) {
	if !orderUUID.MatchString(id) {
		return nil, "", ErrOrderIdentity
	}
	raw, err := c.client.get(ctx, "entity/"+kind+"/"+id, nil)
	if err != nil {
		return nil, "", err
	}
	var dto struct {
		catalogReference
		ID        string `json:"id"`
		AccountID string `json:"accountId"`
		Archived  *bool  `json:"archived"`
	}
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &dto) != nil || dto.ID != id || kind != "currency" && dto.AccountID != c.scope.MoyskladAccount || dto.Archived == nil || *dto.Archived {
		return nil, "", ErrOrderIdentity
	}
	actual, err := c.referenceID(dto.catalogReference, "entity/"+kind, kind)
	if err != nil || actual != id {
		return nil, "", ErrOrderIdentity
	}
	canonical, err := orderCanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return nil, "", err
	}
	hash := sha256.Sum256(canonical)
	return raw, hex.EncodeToString(hash[:]), nil
}

func (c *OrderClient) commissionPartyAccount(ctx context.Context, kind, owner, selected, currency string, raw []byte) (string, string, error) {
	var party struct {
		Accounts struct {
			Meta struct {
				Href string `json:"href"`
				Size *int   `json:"size"`
			} `json:"meta"`
		} `json:"accounts"`
	}
	if json.Unmarshal(raw, &party) != nil || party.Accounts.Meta.Size == nil || *party.Accounts.Meta.Size < 0 || *party.Accounts.Meta.Size > 1000 {
		return "", "", ErrOrderSchema
	}
	path := "entity/" + kind + "/" + owner + "/accounts"
	if party.Accounts.Meta.Href != c.client.base.ResolveReference(&url.URL{Path: path}).String() {
		return "", "", ErrOrderIdentity
	}
	count := *party.Accounts.Meta.Size
	if selected == "" && count == 0 {
		return "", "no-accounts", nil
	}
	if selected == "" && count > 1 {
		return "", "", orders.ErrBlocked
	}
	if selected != "" {
		return c.commissionAccount(ctx, path, selected, currency)
	}
	return c.commissionSoleAccount(ctx, path, currency)
}

func (c *OrderClient) commissionSoleAccount(ctx context.Context, path, currency string) (string, string, error) {
	data, err := c.client.get(ctx, path, url.Values{"limit": {"2"}, "offset": {"0"}})
	if err != nil {
		return "", "", err
	}
	collection, err := commissionCollection(data, 2, 0)
	if err != nil || *collection.Meta.Size != 1 || len(*collection.Rows) != 1 {
		return "", "", ErrOrderSchema
	}
	var account struct {
		ID string `json:"id"`
	}
	if json.Unmarshal((*collection.Rows)[0], &account) != nil {
		return "", "", ErrOrderSchema
	}
	return c.commissionAccount(ctx, path, account.ID, currency)
}

func (c *OrderClient) commissionAccount(ctx context.Context, path, id, currency string) (string, string, error) {
	if !orderUUID.MatchString(id) {
		return "", "", ErrOrderIdentity
	}
	raw, err := c.client.get(ctx, path+"/"+id, nil)
	if err != nil {
		return "", "", err
	}
	var dto struct {
		catalogReference
		ID        string           `json:"id"`
		AccountID string           `json:"accountId"`
		Currency  catalogReference `json:"currency"`
	}
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &dto) != nil || dto.ID != id || dto.AccountID != c.scope.MoyskladAccount {
		return "", "", ErrOrderIdentity
	}
	actual, err := c.referenceID(dto.catalogReference, path, "account")
	if err != nil || actual != id {
		return "", "", ErrOrderIdentity
	}
	actual, err = c.referenceID(dto.Currency, "entity/currency", "currency")
	if err != nil || actual != currency {
		return "", "", ErrOrderIdentity
	}
	canonical, err := orderCanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256(canonical)
	return id, hex.EncodeToString(hash[:]), nil
}
