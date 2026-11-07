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

type commissionDocumentDTO struct {
	catalogReference
	ID                  string           `json:"id"`
	AccountID           string           `json:"accountId"`
	ExternalCode        string           `json:"externalCode"`
	Updated             string           `json:"updated"`
	Organization        catalogReference `json:"organization"`
	Agent               catalogReference `json:"agent"`
	Contract            catalogReference `json:"contract"`
	OrganizationAccount catalogReference `json:"organizationAccount"`
	AgentAccount        catalogReference `json:"agentAccount"`
	Rate                struct {
		Currency catalogReference `json:"currency"`
		Value    json.Number      `json:"value"`
	} `json:"rate"`
	Positions commissionPositionMeta `json:"positions"`
	Returns   commissionPositionMeta `json:"returnToCommissionerPositions"`
}

type commissionPositionMeta struct {
	Meta struct {
		Href string `json:"href"`
		Type string `json:"type"`
		Size *int   `json:"size"`
	} `json:"meta"`
}

func (c *OrderClient) ReadCommissionDraft(ctx context.Context, plan orders.CommissionPlan, documentID string) (orders.CommissionObservation, error) {
	if !c.scopeAllowed(plan.Scope) || plan.Validate() != nil || documentID != "" && !orderUUID.MatchString(documentID) {
		return orders.CommissionObservation{}, ErrOrderIdentity
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx); err != nil {
		return orders.CommissionObservation{}, err
	}
	refs, err := c.readCommissionReferences(ctx, plan.Policy)
	if err != nil {
		return orders.CommissionObservation{}, err
	}
	if documentID == "" {
		raw, lookupErr := c.lookupCommission(ctx, plan)
		if lookupErr != nil {
			return orders.CommissionObservation{}, lookupErr
		}
		document, _, decodeErr := c.decodeCommissionIdentity(raw, plan, "")
		if decodeErr != nil {
			return orders.CommissionObservation{}, decodeErr
		}
		documentID = document.ID
	}
	return c.readCommissionDocument(ctx, plan, documentID, refs)
}

func (c *OrderClient) lookupCommission(ctx context.Context, plan orders.CommissionPlan) ([]byte, error) {
	raw, err := c.client.get(ctx, "entity/commissionreportin", url.Values{"filter": {"externalCode=" + plan.ExternalCode}, "limit": {"2"}, "offset": {"0"}})
	if err != nil {
		return nil, err
	}
	collection, err := commissionCollection(raw, 2, 0)
	if err != nil {
		return nil, err
	}
	if *collection.Meta.Size == 0 {
		return nil, orders.ErrNotFound
	}
	if *collection.Meta.Size != 1 || len(*collection.Rows) != 1 {
		return nil, orders.ErrConflict
	}
	if _, _, err = c.decodeCommissionIdentity((*collection.Rows)[0], plan, ""); err != nil {
		return nil, err
	}
	return (*collection.Rows)[0], nil
}

func (c *OrderClient) decodeCommissionIdentity(raw []byte, plan orders.CommissionPlan, id string) (commissionDocumentDTO, map[string]json.RawMessage, error) {
	var document commissionDocumentDTO
	var fields map[string]json.RawMessage
	if orderCheckJSON(raw) != nil || json.Unmarshal(raw, &document) != nil || json.Unmarshal(raw, &fields) != nil {
		return document, nil, ErrOrderSchema
	}
	actual, err := c.referenceID(document.catalogReference, "entity/commissionreportin", "commissionreportin")
	if err != nil || actual != document.ID || id != "" && id != actual || document.AccountID != plan.Scope.MoyskladAccount || document.ExternalCode != plan.ExternalCode {
		return document, nil, ErrOrderIdentity
	}
	return document, fields, nil
}

func (c *OrderClient) readCommissionDocument(ctx context.Context, plan orders.CommissionPlan, id string, refs orders.CommissionReferences) (orders.CommissionObservation, error) {
	path := "entity/commissionreportin/" + id
	raw, err := c.client.get(ctx, path, nil)
	if err != nil {
		return orders.CommissionObservation{}, err
	}
	document, fields, err := c.decodeCommissionIdentity(raw, plan, id)
	if err != nil {
		return orders.CommissionObservation{}, err
	}
	observation, err := c.commissionHeader(plan, document, fields, refs)
	if err != nil {
		return observation, err
	}
	observation.Lines, err = c.readCommissionPositions(ctx, document, plan.Policy.CurrencyISO)
	if err != nil {
		return observation, err
	}
	if *document.Returns.Meta.Size > 0 {
		observation.Returns, err = c.readCommissionPositionCollection(ctx, document.ID, "returntocommissionerpositions", *document.Returns.Meta.Size, plan.Policy.CurrencyISO)
		if err != nil {
			return observation, err
		}
	}
	after, err := c.client.get(ctx, path, nil)
	if err != nil {
		return observation, err
	}
	left, err := orderCanonicalJSON(json.RawMessage(raw))
	if err != nil {
		return observation, err
	}
	right, err := orderCanonicalJSON(json.RawMessage(after))
	if err != nil || string(left) != string(right) {
		return observation, orders.ErrStale
	}
	return completeCommissionObservation(observation, left)
}

func completeCommissionObservation(observation orders.CommissionObservation, header json.RawMessage) (orders.CommissionObservation, error) {
	evidence, err := json.Marshal(struct {
		Header  json.RawMessage
		Lines   []orders.CommissionObservedLine
		Returns []orders.CommissionObservedLine `json:",omitempty"`
	}{header, observation.Lines, observation.Returns})
	if err != nil {
		return observation, err
	}
	hash := sha256.Sum256(evidence)
	observation.Revision, observation.ObservedAt, observation.Complete = hex.EncodeToString(hash[:]), time.Now().UTC(), true
	return observation, nil
}
