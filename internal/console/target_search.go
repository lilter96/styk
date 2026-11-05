package console

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lilter96/styk/internal/postgres"
)

func targetOptions(r *http.Request, offset int) (postgres.MappingSearch, error) {
	q := r.URL.Query()
	v := postgres.MappingSearch{Query: strings.TrimSpace(q.Get("search")), SearchBy: "all"}
	if q.Has("query") {
		v.Query = strings.TrimSpace(q.Get("query"))
	}
	if q.Has("searchBy") {
		v.SearchBy = q.Get("searchBy")
	}
	if len(v.Query) > 100 || !strings.Contains("|all|sku|article|name|id|code|", "|"+v.SearchBy+"|") {
		return v, fmt.Errorf("invalid search")
	}
	var err error
	v.Offset, v.PageSize, err = productPagination(r, offset)
	if err != nil {
		return v, err
	}
	v.Page = v.Offset/v.PageSize + 1
	if err = targetFilters(q, &v); err != nil {
		return v, err
	}
	return v, nil
}

func targetFilters(q url.Values, v *postgres.MappingSearch) error {
	for key, dest := range map[string]*bool{"activeOnly": &v.ActiveOnly, "inStockOnly": &v.InStockOnly} {
		if q.Has(key) {
			value, err := strconv.ParseBool(q.Get(key))
			if err != nil {
				return fmt.Errorf("invalid %s: %w", key, err)
			}
			*dest = value
		}
	}
	return nil
}
