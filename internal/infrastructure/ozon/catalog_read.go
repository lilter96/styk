package ozon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

type catalogWire map[string]json.RawMessage

func (c *CatalogClient) ReadCatalog(ctx context.Context, request catalog.ReadRequest) (catalog.Snapshot, error) {
	if err := c.validateRead(request); err != nil {
		return catalog.Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ctx = catalogBudgetContext(ctx)
	identities, complete, cursor, err := c.catalogIdentities(ctx, request)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	snapshot := catalog.Snapshot{Scope: c.scope, Account: request.Account, Complete: complete, NextCursor: cursor, ObservedAt: time.Now().UTC(), Products: []catalog.Product{}}
	if len(identities) == 0 {
		return snapshot, nil
	}
	info, err := c.catalogInformation(ctx, identities)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	attributes, err := c.catalogProductAttributes(ctx, info)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	for _, item := range info {
		product, buildErr := c.catalogProduct(ctx, item, attributes[wireID(item, "id")], snapshot.ObservedAt)
		if buildErr != nil {
			return catalog.Snapshot{}, buildErr
		}
		snapshot.Products = append(snapshot.Products, product)
	}
	revisions := make(map[string]string, len(snapshot.Products))
	for _, product := range snapshot.Products {
		revisions[product.Identity.ID] = product.Revision
	}
	snapshot.Revision = catalogDigest(revisions)
	return snapshot, ctx.Err()
}

func (c *CatalogClient) catalogIdentities(ctx context.Context, request catalog.ReadRequest) ([]catalog.Identity, bool, string, error) {
	limit := request.Limit
	if limit == 0 {
		limit = 100
	}
	if len(request.Identities) > 0 {
		if len(request.Identities) > limit || request.Cursor != "" {
			return nil, false, "", catalog.ErrInvalidIdentity
		}
		return request.Identities, true, "", nil
	}
	return c.catalogListIdentities(ctx, request.Account, request.Cursor, limit)
}

func (c *CatalogClient) catalogInformation(ctx context.Context, identities []catalog.Identity) ([]catalogWire, error) {
	ids, offers := make([]string, 0, len(identities)), make([]string, 0, len(identities))
	seen := make(map[string]bool, len(identities))
	for _, identity := range identities {
		key := identity.ID + "\x00" + identity.OfferID
		if seen[key] || (identity.ID != "" && !catalogPositiveID(identity.ID)) {
			return nil, catalog.ErrInvalidIdentity
		}
		seen[key] = true
		if identity.ID != "" {
			ids = append(ids, identity.ID)
		} else {
			offers = append(offers, identity.OfferID)
		}
	}
	out := make([]catalogWire, 0, len(identities))
	for _, query := range []struct {
		key    string
		values []string
	}{{"product_id", ids}, {"offer_id", offers}} {
		if len(query.values) == 0 {
			continue
		}
		var response struct {
			Items *[]catalogWire `json:"items"`
		}
		if err := c.catalogPost(ctx, productInfoPath, map[string]any{query.key: query.values}, &response); err != nil {
			return nil, err
		}
		if response.Items == nil {
			return nil, ErrCatalogContract
		}
		out = append(out, (*response.Items)...)
	}
	if err := validateCatalogInfo(identities, out); err != nil {
		return nil, err
	}
	return out, nil
}

func validateCatalogInfo(identities []catalog.Identity, items []catalogWire) error {
	if len(items) != len(identities) {
		return ErrCatalogContract
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		pid, offer := wireID(item, "id"), wireText(item, "offer_id")
		if pid == "" || offer == "" || seen[pid] {
			return ErrCatalogContract
		}
		seen[pid] = true
		matched := false
		for _, wanted := range identities {
			if (wanted.ID == "" || wanted.ID == pid) && (wanted.OfferID == "" || wanted.OfferID == offer) && (wanted.SKU == "" || wanted.SKU == wireID(item, "sku")) {
				matched = true
			}
		}
		if !matched {
			return ErrCatalogContract
		}
	}
	return nil
}

func (c *CatalogClient) catalogProductAttributes(ctx context.Context, info []catalogWire) (map[string]catalogWire, error) {
	groups := map[string][]string{"ALL": {}, "ARCHIVED": {}}
	for _, item := range info {
		visibility := "ALL"
		var archived bool
		if decodeCatalogJSON(item["is_archived"], &archived) == nil && archived {
			visibility = "ARCHIVED"
		}
		groups[visibility] = append(groups[visibility], wireID(item, "id"))
	}
	out := make(map[string]catalogWire, len(info))
	for _, visibility := range []string{"ALL", "ARCHIVED"} {
		if len(groups[visibility]) == 0 {
			continue
		}
		items, err := c.catalogAttributeBatch(ctx, groups[visibility], visibility)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			pid := wireID(item, "id")
			if pid == "" || out[pid] != nil {
				return nil, ErrCatalogContract
			}
			out[pid] = item
		}
	}
	for _, item := range info {
		attributes := out[wireID(item, "id")]
		if attributes == nil || wireText(attributes, "offer_id") != wireText(item, "offer_id") {
			return nil, ErrCatalogContract
		}
	}
	return out, nil
}

func (c *CatalogClient) catalogAttributeBatch(ctx context.Context, ids []string, visibility string) ([]catalogWire, error) {
	var response struct {
		Result *[]catalogWire `json:"result"`
		Total  *integer       `json:"total"`
	}
	body := map[string]any{"filter": map[string]any{"product_id": ids, "visibility": visibility}, "limit": len(ids), "sort_dir": "ASC"}
	if err := c.catalogPost(ctx, catalogAttributesPath, body, &response); err != nil {
		return nil, err
	}
	if response.Result == nil || response.Total == nil || int64(*response.Total) != int64(len(ids)) || len(*response.Result) != len(ids) {
		return nil, ErrCatalogContract
	}
	return *response.Result, nil
}

func (c *CatalogClient) catalogProduct(ctx context.Context, info, attributes catalogWire, observed time.Time) (catalog.Product, error) {
	identity := catalog.Identity{Account: catalog.Account{Provider: catalog.ProviderOzon, ID: c.read.id}, EntityType: "product", ID: wireID(info, "id"), OfferID: wireText(info, "offer_id"), SKU: wireID(info, "sku")}
	description, err := c.catalogDescription(ctx, identity)
	if err != nil {
		return catalog.Product{}, err
	}
	fields := map[catalog.Field]catalog.Value{
		catalog.FieldName: wireValue(info, "name"), catalog.FieldDescription: description,
		catalog.FieldPrice: wireValue(info, "price"), catalog.FieldCurrency: wireValue(info, "currency_code"),
		catalog.FieldBarcode: wireValue(attributes, "barcode"), catalog.FieldBarcodes: wireValue(attributes, "barcodes"),
		catalog.FieldImages: wireValue(info, "images"), catalog.FieldAttributes: catalogUnownedAttributes(attributes["attributes"]),
		catalog.FieldCategory:   catalogComposite(attributes, "description_category_id", "type_id"),
		catalog.FieldDimensions: catalogComposite(attributes, "height", "depth", "width", "dimension_unit", "weight", "weight_unit"),
		catalog.FieldUnit:       {State: catalog.StateUnsupported}, catalog.FieldParent: {State: catalog.StateUnknown},
	}
	lifecycle := catalog.LifecycleUnknown
	var archived bool
	if decodeCatalogJSON(info["is_archived"], &archived) == nil {
		lifecycle = catalog.LifecycleActive
		if archived {
			lifecycle = catalog.LifecycleArchived
		}
	}
	fields[catalog.FieldLifecycle] = catalog.Value{State: catalog.StateUnknown}
	if lifecycle != catalog.LifecycleUnknown {
		fields[catalog.FieldLifecycle] = catalog.Text(string(lifecycle))
	}
	extensions := map[string]catalog.Value{"vat": wireValue(info, "vat"), "ozon:info": catalogInfoExtras(info), "ozon:attributes": catalogAttributeExtras(attributes)}
	product := catalog.Product{Identity: identity, Lifecycle: lifecycle, Fields: fields, Extensions: extensions, ObservedAt: observed}
	product.Revision = catalogDigest(struct {
		Info, Attributes catalogWire
		Description      catalog.Value
	}{info, attributes, description})
	for field, value := range product.Fields {
		value.Provenance = catalog.Provenance{Provider: catalog.ProviderOzon, AccountID: c.read.id, EntityID: identity.ID, Revision: product.Revision, ObservedAt: observed}
		product.Fields[field] = value
	}
	return product, nil
}

func (c *CatalogClient) catalogDescription(ctx context.Context, identity catalog.Identity) (catalog.Value, error) {
	var response struct {
		Result catalogWire `json:"result"`
	}
	pid, err := strconv.ParseInt(identity.ID, 10, 64)
	if err != nil {
		return catalog.Value{}, catalog.ErrInvalidIdentity
	}
	if err := c.catalogPost(ctx, catalogDescriptionPath, map[string]any{"product_id": pid, "offer_id": identity.OfferID}, &response); err != nil {
		return catalog.Value{}, err
	}
	if wireID(response.Result, "id") != identity.ID || wireText(response.Result, "offer_id") != identity.OfferID {
		return catalog.Value{}, ErrCatalogContract
	}
	return wireValue(response.Result, "description"), nil
}

func wireValue(wire catalogWire, key string) catalog.Value {
	data, ok := wire[key]
	if !ok {
		return catalog.Value{State: catalog.StateAbsent}
	}
	if string(data) == "null" {
		return catalog.Value{State: catalog.StateUnknown}
	}
	if string(data) == `""` || catalogEmptyArray(data) {
		return catalog.Empty()
	}
	return catalog.Value{State: catalog.StateKnown, Data: append(json.RawMessage(nil), data...)}
}

func catalogEmptyArray(data json.RawMessage) bool {
	var values []json.RawMessage
	return json.Unmarshal(data, &values) == nil && values != nil && len(values) == 0
}

func catalogComposite(wire catalogWire, keys ...string) catalog.Value {
	selected := make(catalogWire, len(keys))
	for _, key := range keys {
		if len(wire[key]) == 0 || string(wire[key]) == "null" {
			return catalog.Value{State: catalog.StateUnknown}
		}
		selected[key] = wire[key]
	}
	return catalogRaw(selected)
}

func catalogRaw(value any) catalog.Value {
	data, err := json.Marshal(value)
	if err != nil {
		return catalog.Value{State: catalog.StateUnknown}
	}
	return catalog.Value{State: catalog.StateKnown, Data: data}
}

func wireText(wire catalogWire, key string) string {
	var value string
	if decodeCatalogJSON(wire[key], &value) != nil || strings.ContainsRune(value, 0) {
		return ""
	}
	return value
}

func wireID(wire catalogWire, key string) string {
	var number integer
	if decodeCatalogJSON(wire[key], &number) != nil {
		return ""
	}
	return id(&number)
}

func catalogPositiveID(value string) bool {
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}

func catalogDigest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
