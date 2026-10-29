package moysklad

import (
	"context"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

// LookupCatalogCreate resolves an exact durable externalCode marker without
// requiring a dispatchable command. Empty is an observation of absence, never
// permission to resend an uncertain creation.
func (c *CatalogClient) LookupCatalogCreate(ctx context.Context, cmd catalog.Command) (catalog.Snapshot, error) {
	if err := validateCatalogLookup(cmd); err != nil {
		return catalog.Snapshot{}, err
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx, cmd.Target.Account); err != nil {
		return catalog.Snapshot{}, err
	}
	snap := catalog.Snapshot{Scope: cmd.Scope, Account: cmd.Target.Account, Complete: true, ObservedAt: time.Now().UTC(), Products: []catalog.Product{}}
	raw, err := c.findCatalogMarker(ctx, cmd)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	if len(raw) != 0 {
		p, err := c.decodeCatalogProduct(raw, cmd.Target.Account, cmd.Target.EntityType, snap.ObservedAt)
		if err != nil || (cmd.Parent != nil && (p.Parent == nil || *p.Parent != *cmd.Parent)) {
			return catalog.Snapshot{}, ErrCatalogIdentity
		}
		snap.Products = append(snap.Products, p)
	}
	snap.Revision = catalogSnapshotRevision(snap.Products)
	return snap, nil
}

func validateCatalogLookup(cmd catalog.Command) error {
	if err := catalogAccount(cmd.Scope, cmd.Target.Account); err != nil {
		return err
	}
	if cmd.Action != catalog.ActionCreate || cmd.Target.ID != "" || cmd.Target.OfferID != "" || cmd.Target.SKU != "" || (cmd.Target.EntityType != "product" && cmd.Target.EntityType != "variant") || !catalogMarker.MatchString(cmd.CreateExternalCode) {
		return ErrCatalogInvalid
	}
	if cmd.Parent != nil && (catalogIdentity(cmd.Target.Account, *cmd.Parent) != nil || cmd.Parent.EntityType != "product") {
		return ErrCatalogIdentity
	}
	return nil
}
