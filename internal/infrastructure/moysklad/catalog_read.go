package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

type catalogRow struct {
	catalogReference
	ID        string            `json:"id"`
	AccountID string            `json:"accountId"`
	Name      *string           `json:"name"`
	Archived  *bool             `json:"archived"`
	Product   *catalogReference `json:"product"`
}

// ReadCatalog returns one bounded, explicitly paginated account observation.
// Complete describes this traversal; callers must follow NextCursor before
// treating a complete account catalog as authoritative.
func (c *CatalogClient) ReadCatalog(ctx context.Context, req catalog.ReadRequest) (catalog.Snapshot, error) {
	if err := validateCatalogRead(req); err != nil {
		return catalog.Snapshot{}, err
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx, req.Account); err != nil {
		return catalog.Snapshot{}, err
	}
	snap := catalog.Snapshot{Scope: req.Scope, Account: req.Account, ObservedAt: time.Now().UTC(), Products: []catalog.Product{}}
	var err error
	if len(req.Identities) != 0 {
		err = c.readCatalogIdentities(ctx, req, &snap)
	} else {
		err = c.readCatalogCollection(ctx, req, &snap)
	}
	if err != nil {
		return catalog.Snapshot{}, err
	}
	snap.Revision = catalogSnapshotRevision(snap.Products)
	return snap, nil
}

func validateCatalogRead(req catalog.ReadRequest) error {
	if err := catalogAccount(req.Scope, req.Account); err != nil {
		return err
	}
	if req.Limit < 0 || req.Limit > 1000 || len(req.Identities) > 1000 || len(req.Search) > 255 || strings.ContainsAny(req.Search, "\r\n") {
		return ErrCatalogInvalid
	}
	if len(req.Identities) != 0 && (req.Cursor != "" || req.Search != "") {
		return ErrCatalogInvalid
	}
	for _, id := range req.Identities {
		if err := catalogIdentity(req.Account, id); err != nil {
			return err
		}
	}
	_, _, err := catalogCursor(req.Cursor)
	return err
}

func catalogCursor(cursor string) (string, int, error) {
	if cursor == "" {
		return "product", 0, nil
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 2 || (parts[0] != "product" && parts[0] != "variant") {
		return "", 0, ErrCatalogInvalid
	}
	offset, err := strconv.Atoi(parts[1])
	if err != nil || offset < 0 || offset > maxRows || strconv.Itoa(offset) != parts[1] {
		return "", 0, ErrCatalogInvalid
	}
	return parts[0], offset, nil
}

func (c *CatalogClient) readCatalogIdentities(ctx context.Context, req catalog.ReadRequest, snap *catalog.Snapshot) error {
	seen := make(map[string]bool, len(req.Identities))
	for _, id := range req.Identities {
		key := id.EntityType + ":" + id.ID
		if seen[key] {
			return ErrCatalogIdentity
		}
		seen[key] = true
		product, err := c.readCatalogProduct(ctx, id, snap.ObservedAt)
		if err != nil {
			return err
		}
		snap.Products = append(snap.Products, product)
	}
	snap.Complete = true
	return nil
}

func (c *CatalogClient) readCatalogProduct(ctx context.Context, id catalog.Identity, at time.Time) (catalog.Product, error) {
	raw, err := c.client.get(ctx, "entity/"+id.EntityType+"/"+id.ID, nil)
	if err != nil {
		return catalog.Product{}, fmt.Errorf("read catalog product: %w", err)
	}
	product, err := c.decodeCatalogProduct(raw, id.Account, id.EntityType, at)
	if err != nil {
		return catalog.Product{}, err
	}
	if product.Identity != id {
		return catalog.Product{}, ErrCatalogIdentity
	}
	return product, nil
}

func (c *CatalogClient) readCatalogCollection(ctx context.Context, req catalog.ReadRequest, snap *catalog.Snapshot) error {
	kind, offset, err := catalogCursor(req.Cursor)
	if err != nil {
		return err
	}
	limit := req.Limit
	if limit == 0 {
		limit = 100
	}
	seen := make(map[string]bool, limit)
	totals := make(map[string]int, 2)
	for len(snap.Products) < limit {
		q := catalogPageQuery(offset, min(limit-len(snap.Products), c.client.pageSize), req.Search)
		n, total, err := c.readCatalogPage(ctx, kind, q, snap, seen)
		if err != nil {
			return err
		}
		if previous, ok := totals[kind]; ok && previous != total {
			return errors.New("moysklad catalog changed during traversal")
		}
		totals[kind] = total
		offset += n
		if offset < total {
			continue
		}
		if kind == "variant" {
			snap.Complete = true
			return nil
		}
		kind, offset = "variant", 0
	}
	snap.NextCursor = kind + ":" + strconv.Itoa(offset)
	return nil
}

func catalogPageQuery(offset, limit int, search string) url.Values {
	q := url.Values{"limit": {strconv.Itoa(limit)}, "offset": {strconv.Itoa(offset)}}
	if search != "" {
		q.Set("search", search)
	}
	return q
}

func (c *CatalogClient) readCatalogPage(ctx context.Context, kind string, q url.Values, snap *catalog.Snapshot, seen map[string]bool) (int, int, error) {
	b, _ := ctx.Value(budgetKey{}).(*budget)
	if err := b.add("pages"); err != nil {
		return 0, 0, err
	}
	raw, err := c.client.get(ctx, "entity/"+kind, q)
	if err != nil {
		return 0, 0, err
	}
	rows, total, err := catalogPage(raw, q)
	if err != nil {
		return 0, 0, err
	}
	for _, row := range rows {
		p, err := c.decodeCatalogProduct(row, snap.Account, kind, snap.ObservedAt)
		if err != nil {
			return 0, 0, err
		}
		key := kind + ":" + p.Identity.ID
		if seen[key] {
			return 0, 0, ErrCatalogIdentity
		}
		seen[key] = true
		snap.Products = append(snap.Products, p)
	}
	return len(rows), total, nil
}

func catalogPage(raw []byte, q url.Values) ([]json.RawMessage, int, error) {
	var p page
	if json.Unmarshal(raw, &p) != nil || p.Meta == nil || p.Rows == nil || p.Meta.Size == nil || p.Meta.Offset == nil || p.Meta.Limit == nil {
		return nil, 0, errors.New("incomplete moysklad catalog page")
	}
	offset, offsetErr := strconv.Atoi(q.Get("offset"))
	limit, limitErr := strconv.Atoi(q.Get("limit"))
	m, n := p.Meta, len(*p.Rows)
	if offsetErr != nil || limitErr != nil || *m.Offset != offset || *m.Limit < 1 || *m.Limit > limit || n > *m.Limit || *m.Size < 0 || *m.Size > maxRows || offset+n > *m.Size || (n == 0 && offset < *m.Size) {
		return nil, 0, errors.New("inconsistent moysklad catalog pagination")
	}
	return *p.Rows, *m.Size, nil
}

func (c *CatalogClient) decodeCatalogProduct(raw []byte, account catalog.Account, kind string, at time.Time) (catalog.Product, error) {
	var row catalogRow
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &row) != nil || json.Unmarshal(raw, &fields) != nil || row.AccountID != account.ID || !orderUUID.MatchString(row.ID) || row.Name == nil {
		return catalog.Product{}, ErrCatalogIdentity
	}
	id, err := c.referenceID(row.catalogReference, "entity/"+kind, kind)
	if err != nil || id != row.ID {
		return catalog.Product{}, ErrCatalogIdentity
	}
	p := catalog.Product{Identity: catalog.Identity{Account: account, EntityType: kind, ID: id}, Variant: kind == "variant", Lifecycle: catalog.LifecycleUnknown, Revision: catalogDigest(fields), ObservedAt: at}
	if row.Archived != nil {
		p.Lifecycle = catalog.LifecycleActive
		if *row.Archived {
			p.Lifecycle = catalog.LifecycleArchived
		}
	}
	if err := c.catalogParent(&p, row.Product); err != nil {
		return catalog.Product{}, err
	}
	p.Fields, p.Extensions = catalogFields(fields), catalogExtensions(fields)
	setCatalogProvenance(&p)
	return p, nil
}

func (c *CatalogClient) catalogParent(p *catalog.Product, parent *catalogReference) error {
	if !p.Variant {
		if parent != nil {
			return ErrCatalogIdentity
		}
		return nil
	}
	if parent == nil {
		return ErrCatalogIdentity
	}
	id, err := c.referenceID(*parent, "entity/product", "product")
	if err != nil {
		return err
	}
	p.Parent = &catalog.Identity{Account: p.Identity.Account, EntityType: "product", ID: id}
	return nil
}

func catalogFields(raw map[string]json.RawMessage) map[catalog.Field]catalog.Value {
	names := map[catalog.Field]string{catalog.FieldName: "name", catalog.FieldDescription: "description", catalog.FieldArticle: "article", catalog.FieldPrice: "salePrices", catalog.FieldBarcodes: "barcodes", catalog.FieldBarcode: "barcodes", catalog.FieldCategory: "productFolder", catalog.FieldAttributes: "attributes", catalog.FieldUnit: "uom", catalog.FieldImages: "images"}
	out := make(map[catalog.Field]catalog.Value, len(names))
	for field, key := range names {
		out[field] = catalogObservedValue(raw[key])
	}
	if image := raw["images"]; len(image) > 0 && image[0] == '{' {
		out[catalog.FieldImages] = catalog.Value{State: catalog.StateUnknown}
	}
	return out
}

func catalogObservedValue(raw json.RawMessage) catalog.Value {
	if len(raw) == 0 {
		return catalog.Value{State: catalog.StateAbsent}
	}
	if string(raw) == "null" || string(raw) == `""` || string(raw) == "[]" {
		return catalog.Empty()
	}
	return catalog.Value{State: catalog.StateKnown, Data: append(json.RawMessage(nil), raw...)}
}

func catalogExtensions(raw map[string]json.RawMessage) map[string]catalog.Value {
	out := make(map[string]catalog.Value, 4)
	for _, key := range []string{"code", "article", "externalCode", "characteristics"} {
		out["moysklad."+key] = catalogObservedValue(raw[key])
	}
	return out
}

func setCatalogProvenance(p *catalog.Product) {
	provenance := catalog.Provenance{Kind: catalog.EvidenceAPICurated, Provider: p.Identity.Account.Provider, AccountID: p.Identity.Account.ID, EntityID: p.Identity.ID, Revision: p.Revision, ObservedAt: p.ObservedAt, Evidence: "moysklad-json-api"}
	for field, value := range p.Fields {
		value.Provenance = provenance
		p.Fields[field] = value
	}
	for key, value := range p.Extensions {
		value.Provenance = provenance
		p.Extensions[key] = value
	}
}

func catalogSnapshotRevision(products []catalog.Product) string {
	type revision struct{ Kind, ID, Revision string }
	items := make([]revision, 0, len(products))
	for _, p := range products {
		items = append(items, revision{p.Identity.EntityType, p.Identity.ID, p.Revision})
	}
	return catalogDigest(items)
}
