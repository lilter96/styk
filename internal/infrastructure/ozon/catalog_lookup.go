package ozon

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

// LookupCatalogCreate is an exact seller-offer lookup. An absent target is a
// complete empty observation, distinct from losing an established source ID.
func (c *CatalogClient) LookupCatalogCreate(ctx context.Context, command catalog.Command) (catalog.Snapshot, error) {
	account := catalog.Account{Provider: catalog.ProviderOzon, ID: c.read.id}
	if command.Action != catalog.ActionCreate || command.Scope != c.scope || command.Target.Account != account || !catalogTextValid(command.Target.OfferID, 50) {
		return catalog.Snapshot{}, catalog.ErrInvalidScope
	}
	var response struct {
		Items *[]catalogWire `json:"items"`
	}
	if err := c.catalogPost(ctx, productInfoPath, map[string]any{"offer_id": []string{command.Target.OfferID}}, &response); err != nil {
		return catalog.Snapshot{}, err
	}
	if response.Items == nil || len(*response.Items) > 1 {
		return catalog.Snapshot{}, ErrCatalogContract
	}
	if len(*response.Items) == 0 {
		return catalog.Snapshot{Scope: c.scope, Account: account, Products: []catalog.Product{}, ObservedAt: time.Now().UTC(), Complete: true, Revision: catalogDigest([]string{})}, nil
	}
	if err := validateCatalogInfo([]catalog.Identity{command.Target}, *response.Items); err != nil {
		return catalog.Snapshot{}, err
	}
	identity := command.Target
	identity.ID = wireID((*response.Items)[0], "id")
	return c.ReadCatalog(ctx, catalog.ReadRequest{Scope: c.scope, Account: account, Identities: []catalog.Identity{identity}, Limit: 1})
}
