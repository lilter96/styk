package moysklad

import (
	"context"
	"net/http"
	"strings"

	"github.com/lilter96/styk/internal/catalog"
)

func observeCatalogRequest(ctx context.Context, method, path string, response *http.Response, err error) {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if orderUUID.MatchString(part) {
			parts[index] = "{id}"
		}
	}
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	catalog.ObserveRequest(ctx, catalog.RequestEvent{Provider: catalog.ProviderMoySklad, Method: method,
		Operation: strings.Join(parts, "/"), Status: status, Interrupted: err != nil})
}
