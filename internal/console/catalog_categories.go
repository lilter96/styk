package console

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/infrastructure/ozon"
)

type categoryOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type categoryAttribute struct {
	ProviderType      string           `json:"provider_type"`
	Collection        bool             `json:"collection"`
	ComplexID         int64            `json:"complex_id"`
	ID                string           `json:"id"`
	Name              string           `json:"name"`
	Required          bool             `json:"required"`
	Type              string           `json:"type"`
	Options           []categoryOption `json:"options,omitempty"`
	Dictionary        bool             `json:"dictionary,omitempty"`
	OptionsIncomplete bool             `json:"options_incomplete,omitempty"`
}
type merchantCategory struct {
	ID               string              `json:"id"`
	Name             string              `json:"name"`
	Attributes       []categoryAttribute `json:"attributes"`
	AttributesLoaded bool                `json:"attributes_loaded"`
}

func (a *catalogAPI) categoryClient(ctx context.Context, scope catalog.Scope) (*ozon.CatalogClient, error) {
	if _, err := a.service.Config(ctx, scope); err != nil {
		return nil, err
	}
	binding, err := a.factory.ResolveCatalog(ctx, scope, catalog.Account{Provider: catalog.ProviderOzon, ID: scope.OzonAccount}, 0)
	if err != nil {
		return nil, err
	}
	client, ok := binding.Client.(*ozon.CatalogClient)
	if !ok {
		return nil, catalogsync.ErrBlocked
	}
	return client, nil
}

func (a *catalogAPI) categories(w http.ResponseWriter, r *http.Request) {
	if p := r.URL.Query().Get("provider"); p != "" && p != "ozon" {
		writeError(w, 400, "CATALOG_PROVIDER_UNSUPPORTED")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	client, err := a.categoryClient(ctx, catalogScope(r))
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	if id := r.URL.Query().Get("category_id"); id != "" {
		a.categoryDetail(w, r.WithContext(ctx), client, id)
		return
	}
	nodes, err := client.CategoryTree(ctx)
	if err != nil {
		catalogCategoryError(w, err)
		return
	}
	items, err := flattenCatalogCategories(nodes)
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	items, next, err := pageCatalogCategories(items, r.URL.Query().Get("search"), r.URL.Query().Get("cursor"), catalogPage(r).Limit)
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	writeJSON(w, struct {
		Items []merchantCategory `json:"items"`
		Next  string             `json:"next_cursor,omitempty"`
	}{items, next})
}

type catalogCategoryTraversal struct {
	items []merchantCategory
	count int
}

func flattenCatalogCategories(nodes []ozon.CategoryNode) ([]merchantCategory, error) {
	t := &catalogCategoryTraversal{items: make([]merchantCategory, 0)}
	if err := t.walk(nodes, 0, "", false, 0); err != nil {
		return nil, err
	}
	return t.items, nil
}

func (t *catalogCategoryTraversal) walk(nodes []ozon.CategoryNode, parent int64, path string, disabled bool, depth int) error {
	if depth > 16 {
		return catalogsync.ErrInvalid
	}
	for _, node := range nodes {
		t.count++
		if t.count > 50000 {
			return catalogsync.ErrInvalid
		}
		if err := t.node(node, parent, path, disabled, depth); err != nil {
			return err
		}
	}
	return nil
}

func (t *catalogCategoryTraversal) node(node ozon.CategoryNode, parent int64, path string, disabled bool, depth int) error {
	category := node.DescriptionCategoryID
	if category == 0 {
		category = parent
	}
	label := node.CategoryName
	if label == "" {
		label = node.TypeName
	}
	name := label
	if path != "" {
		name = path + " / " + label
	}
	blocked := disabled || node.Disabled
	if len(node.Children) > 0 {
		return t.walk(node.Children, category, name, blocked, depth+1)
	}
	if !blocked && category > 0 && node.TypeID > 0 {
		t.items = append(t.items, merchantCategory{ID: strconv.FormatInt(category, 10) + ":" + strconv.FormatInt(node.TypeID, 10), Name: name, Attributes: []categoryAttribute{}})
	}
	return nil
}

func parseCatalogCategory(raw string) (ozon.CategorySelection, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return ozon.CategorySelection{}, catalogsync.ErrInvalid
	}
	category, e1 := strconv.ParseInt(parts[0], 10, 64)
	kind, e2 := strconv.ParseInt(parts[1], 10, 64)
	if e1 != nil || e2 != nil || category <= 0 || kind <= 0 {
		return ozon.CategorySelection{}, catalogsync.ErrInvalid
	}
	return ozon.CategorySelection{DescriptionCategoryID: category, TypeID: kind}, nil
}

func (a *catalogAPI) categoryDetail(w http.ResponseWriter, r *http.Request, client *ozon.CatalogClient, id string) {
	selection, err := parseCatalogCategory(id)
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	schema, err := client.CategoryAttributes(r.Context(), selection)
	if err != nil {
		catalogCategoryError(w, err)
		return
	}
	writeJSON(w, describeCatalogCategory(id, schema))
}

func describeCatalogCategory(id string, schema []ozon.CategoryAttribute) merchantCategory {
	attrs := make([]categoryAttribute, 0, len(schema))
	for _, s := range schema {
		kind := "string"
		switch strings.ToLower(s.Type) {
		case "integer", "decimal", "number":
			kind = "number"
		case "boolean":
			kind = "boolean"
		}
		dictionary := s.DictionaryID > 0
		if dictionary {
			kind = "select"
		}
		attrs = append(attrs, categoryAttribute{ID: strconv.FormatInt(s.ID, 10), ProviderType: s.Type, Collection: s.Collection, ComplexID: s.ComplexID, Name: s.Name, Required: s.Required, Type: kind, Dictionary: dictionary, OptionsIncomplete: dictionary})
	}
	return merchantCategory{ID: id, Name: id, Attributes: attrs, AttributesLoaded: true}
}

func (a *catalogAPI) attributes(w http.ResponseWriter, r *http.Request) {
	selection, err := parseCatalogCategory(r.URL.Query().Get("category_id"))
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	attribute, err := strconv.ParseInt(r.URL.Query().Get("attribute_id"), 10, 64)
	if err != nil || attribute <= 0 {
		writeError(w, 400, "CATALOG_INVALID_ATTRIBUTE")
		return
	}
	last, err := strconv.ParseInt("0"+r.URL.Query().Get("cursor"), 10, 64)
	if err != nil || last < 0 {
		writeError(w, 400, "CATALOG_INVALID_PAGE")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	client, err := a.categoryClient(ctx, catalogScope(r))
	if err != nil {
		catalogHTTPError(w, err)
		return
	}
	page, err := client.AttributeValues(ctx, selection, attribute, last, 100)
	if err != nil {
		catalogCategoryError(w, err)
		return
	}
	options := make([]categoryOption, 0, len(page.Values))
	for _, v := range page.Values {
		options = append(options, categoryOption{ID: strconv.FormatInt(v.ID, 10), Name: v.Value})
	}
	next := ""
	if page.HasNext {
		next = strconv.FormatInt(page.LastValueID, 10)
	}
	writeJSON(w, struct {
		Items []categoryOption `json:"items"`
		Next  string           `json:"next_cursor,omitempty"`
	}{options, next})
}
func catalogCategoryError(w http.ResponseWriter, err error) {
	var api *ozon.APIError
	if errors.As(err, &api) && (api.Status == 401 || api.Status == 403) {
		writeError(w, 422, "CATALOG_CATEGORY_ACCESS_REQUIRED")
		return
	}
	catalogHTTPError(w, err)
}

func pageCatalogCategories(items []merchantCategory, search, cursor string, limit int) ([]merchantCategory, string, error) {
	search = strings.ToLower(strings.TrimSpace(search))
	filtered := make([]merchantCategory, 0, len(items))
	for _, item := range items {
		if search == "" || strings.Contains(strings.ToLower(item.Name), search) {
			filtered = append(filtered, item)
		}
	}
	start, err := strconv.Atoi("0" + cursor)
	if err != nil || start < 0 || start > len(filtered) {
		return nil, "", catalogsync.ErrInvalid
	}
	if limit < 1 || limit > 50 {
		limit = 20
	}
	end := min(start+limit, len(filtered))
	next := ""
	if end < len(filtered) {
		next = strconv.Itoa(end)
	}
	return filtered[start:end], next, nil
}
