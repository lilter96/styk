//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/lilter96/styk/internal/testpublication"
)

const (
	browserMoyAccount = "00000000-0000-4000-8000-000000000002"
	browserMoyProduct = "00000000-0000-4000-8000-000000000012"
	browserMoyStore   = "00000000-0000-4000-8000-000000000022"
	browserOzon       = "987654321"
)

type merchantFakeProvider struct {
	writes atomic.Int32
	stockB atomic.Int32
}

func merchantFakeReply(w http.ResponseWriter, format string, args ...any) {
	if _, err := fmt.Fprintf(w, format, args...); err != nil {
		return
	}
}

func (p *merchantFakeProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/v") {
		p.ozon(w, r)
		return
	}
	p.moy(w, r)
}

func (p *merchantFakeProvider) ozon(w http.ResponseWriter, r *http.Request) {
	account := r.Header.Get("Client-Id")
	key := r.Header.Get("Api-Key")
	if (account != testpublication.OzonAccount || key != "synthetic-ozon-a") && (account != browserOzon || key != "synthetic-ozon-b") {
		http.Error(w, "synthetic authentication failed", http.StatusForbidden)
		return
	}
	product, offer, sku := "2764544165", "ATEST-897677", "2889277907"
	var stock int32
	if account == browserOzon {
		product, offer, sku = "202", "B-TEST-OFFER", "203"
		stock = p.stockB.Load()
	}
	switch r.URL.Path {
	case "/v3/product/list":
		var body struct {
			Filter struct {
				Visibility string `json:"visibility"`
			} `json:"filter"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid synthetic request", http.StatusBadRequest)
			return
		}
		if body.Filter.Visibility == "ARCHIVED" {
			merchantFakeReply(w, `{"result":{"items":[],"total_items":0,"last_id":""}}`)
			return
		}
		merchantFakeReply(w, `{"result":{"items":[{"product_id":%s,"offer_id":%q,"sku":%s,"archived":false}],"total_items":1,"last_id":""}}`, product, offer, sku)
	case "/v3/product/info/list":
		merchantFakeReply(w, `{"items":[{"id":%s,"offer_id":%q,"name":"Товар Ozon для проверки"}]}`, product, offer)
	case "/v4/posting/fbs/list":
		merchantFakeReply(w, `{"postings":[],"has_next":false,"cursor":""}`)
	case "/v2/warehouse/list":
		merchantFakeReply(w, `{"warehouses":[{"warehouse_id":1020001323428000,"name":"Тестовый FBS","status":"created","is_rfbs":false}],"has_next":false,"cursor":""}`)
	case "/v2/product/info/stocks-by-warehouse/fbs":
		merchantFakeReply(w, `{"products":[{"product_id":%s,"offer_id":%q,"sku":%s,"warehouse_id":1020001323428000,"present":%d,"reserved":0,"free_stock":%d}],"has_next":false,"cursor":""}`, product, offer, sku, stock, stock)
	case "/v2/products/stocks":
		var request struct {
			Stocks []struct {
				ProductID   int64 `json:"product_id"`
				WarehouseID int64 `json:"warehouse_id"`
				Stock       int32 `json:"stock"`
			} `json:"stocks"`
		}
		if account != browserOzon || json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Stocks) != 1 || request.Stocks[0].ProductID != 202 || request.Stocks[0].WarehouseID != 1020001323428000 || request.Stocks[0].Stock < 0 || request.Stocks[0].Stock > 10 {
			http.Error(w, "synthetic write target not allowed", http.StatusForbidden)
			return
		}
		p.stockB.Store(request.Stocks[0].Stock)
		p.writes.Add(1)
		merchantFakeReply(w, `{"result":[{"product_id":202,"warehouse_id":1020001323428000,"updated":true,"errors":[]}]}`)
	default:
		http.Error(w, "unknown synthetic Ozon method", http.StatusNotFound)
	}
}

func (p *merchantFakeProvider) moy(w http.ResponseWriter, r *http.Request) {
	account, product, store, article := testpublication.MoyskladAccount, testpublication.SourceProduct, testpublication.SourceWarehouse, "A-OFFER"
	switch r.Header.Get("Authorization") {
	case "Bearer synthetic-moy-a":
	case "Bearer synthetic-moy-b":
		account, product, store, article = browserMoyAccount, browserMoyProduct, browserMoyStore, "B-TEST-OFFER"
	default:
		http.Error(w, "synthetic authentication failed", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/context/employee":
		merchantFakeReply(w, `{"accountId":%q}`, account)
	case "/entity/product":
		merchantFakeReply(w, `{"meta":{"size":1,"limit":1,"offset":0},"rows":[{"id":%q,"accountId":%q,"name":"Тестовый товар","article":%q}]}`, product, account, article)
	case "/entity/variant":
		merchantFakeReply(w, `{"meta":{"size":0,"limit":100,"offset":0},"rows":[]}`)
	case "/entity/store":
		merchantFakeReply(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"id":%q,"name":"Тестовый склад"}]}`, store)
	case "/entity/customerorder":
		merchantFakeReply(w, `{"meta":{"size":0,"limit":100,"offset":0},"rows":[]}`)
	case "/report/stock/all":
		merchantFakeReply(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stock":2,"reserve":0,"inTransit":0,"quantity":2}]}`, product)
	case "/report/stock/bystore":
		merchantFakeReply(w, `{"meta":{"size":1,"limit":100,"offset":0},"rows":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/product/%s","type":"product"},"stockByStore":[{"meta":{"href":"https://api.moysklad.ru/api/remap/1.2/entity/store/%s","type":"store"},"stock":2,"reserve":0,"inTransit":0}]}]}`, product, store)
	default:
		http.Error(w, "unknown synthetic MoySklad method", http.StatusNotFound)
	}
}
