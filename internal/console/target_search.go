package console

import (
	"fmt"
	"github.com/lilter96/styk/internal/postgres"
	"net/http"
	"strconv"
	"strings"
)

func targetOptions(r *http.Request, offset int) (postgres.MappingSearch, error) {
	q := r.URL.Query()
	v := postgres.MappingSearch{Query: strings.TrimSpace(q.Get("search")), SearchBy: "all", Page: offset/50 + 1, PageSize: 50, Offset: offset}
	if q.Has("query") {
		v.Query = strings.TrimSpace(q.Get("query"))
	}
	if q.Has("searchBy") {
		v.SearchBy = q.Get("searchBy")
	}
	if len(v.Query) > 100 || !strings.Contains("|all|sku|article|name|id|code|", "|"+v.SearchBy+"|") {
		return v, fmt.Errorf("invalid search")
	}
	for key, dest := range map[string]*bool{"activeOnly": &v.ActiveOnly, "inStockOnly": &v.InStockOnly} {
		if q.Has(key) {
			value, err := strconv.ParseBool(q.Get(key))
			if err != nil {
				return v, fmt.Errorf("invalid %s: %w", key, err)
			}
			*dest = value
		}
	}
	if q.Has("pageSize") {
		n, err := strconv.Atoi(q.Get("pageSize"))
		if err != nil || n < 1 || n > 100 {
			return v, fmt.Errorf("invalid page size")
		}
		v.PageSize = n
	}
	if q.Has("page") {
		n, err := strconv.Atoi(q.Get("page"))
		if err != nil || n < 1 || n > 100001 {
			return v, fmt.Errorf("invalid page")
		}
		v.Page = n
		v.Offset = (n - 1) * v.PageSize
	}
	if v.Offset > 100000 {
		return v, fmt.Errorf("page limit exceeded")
	}
	return v, nil
}
