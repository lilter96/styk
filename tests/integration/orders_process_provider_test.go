//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type ordersProcessNative struct {
	account string
	posting string
	id      string
	body    map[string]any
	hidden  bool
}

type ordersProcessAttempt struct {
	account string
	at      time.Time
	status  int
}

type ordersProcessProvider struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	postings map[string]map[string]string
	native   map[string]*ordersProcessNative
	lost     map[string]bool
	writes   map[string]int
	active   map[string]int
	maximum  map[string]int
	attempts []ordersProcessAttempt
	limited  map[string]bool
}

func ordersProcessNewProvider(t *testing.T) *ordersProcessProvider {
	t.Helper()
	p := &ordersProcessProvider{t: t, postings: make(map[string]map[string]string), native: make(map[string]*ordersProcessNative), lost: make(map[string]bool), writes: make(map[string]int), active: make(map[string]int), maximum: make(map[string]int), limited: make(map[string]bool)}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.server.Close)
	return p
}

func (p *ordersProcessProvider) add(account, posting, scheme string, lost bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.postings[account] == nil {
		p.postings[account] = make(map[string]string)
	}
	p.postings[account][posting], p.lost[posting] = scheme, lost
}

func (p *ordersProcessProvider) serve(w http.ResponseWriter, r *http.Request) {
	account := r.Header.Get("Client-Id")
	if account != "" {
		if r.Header.Get("Api-Key") != "synthetic-orders-ozon-"+account {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		account = "ozon:" + account
	} else {
		account = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer synthetic-orders-moysklad-")
		if account == "" || account == r.Header.Get("Authorization") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		account = "moysklad:" + account
	}
	limited := p.begin(account)
	defer p.finish(account)
	time.Sleep(15 * time.Millisecond)
	w.Header().Set("Content-Type", "application/json")
	if limited {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		p.reply(w, map[string]any{"error": "synthetic rate limit"})
		return
	}
	if strings.HasPrefix(account, "ozon:") {
		p.ozon(w, r, strings.TrimPrefix(account, "ozon:"))
		return
	}
	p.moy(w, r, strings.TrimPrefix(account, "moysklad:"))
}

func (p *ordersProcessProvider) begin(account string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active[account]++
	p.maximum[account] = max(p.maximum[account], p.active[account])
	status := http.StatusOK
	if p.limited[account] {
		p.limited[account], status = false, http.StatusTooManyRequests
	}
	p.attempts = append(p.attempts, ordersProcessAttempt{account: account, at: time.Now(), status: status})
	return status == http.StatusTooManyRequests
}

func (p *ordersProcessProvider) finish(account string) {
	p.mu.Lock()
	p.active[account]--
	p.mu.Unlock()
}

func (p *ordersProcessProvider) ozon(w http.ResponseWriter, r *http.Request, account string) {
	var request struct {
		PostingNumber string `json:"posting_number"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&request) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case "/v4/posting/fbs/list", "/v4/posting/fbs/unfulfilled/list":
		numbers := make([]string, 0, len(p.postings[account]))
		for number := range p.postings[account] {
			numbers = append(numbers, number)
		}
		sort.Strings(numbers)
		rows := make([]any, 0, len(numbers))
		for _, number := range numbers {
			rows = append(rows, map[string]string{"posting_number": number})
		}
		p.reply(w, map[string]any{"postings": rows, "has_next": false, "cursor": ""})
	case "/v3/posting/fbs/get":
		p.reply(w, map[string]any{"result": p.posting(request.PostingNumber, p.postings[account][request.PostingNumber])})
	case "/v2/warehouse/list":
		p.reply(w, map[string]any{"warehouses": []any{map[string]any{"warehouse_id": "3001", "warehouse_type": "fbs", "status": "created", "is_rfbs": false, "is_express": false}}, "has_next": false, "cursor": ""})
	case "/v2/delivery-method/list":
		p.reply(w, map[string]any{"delivery_methods": []any{map[string]any{"id": "4001", "warehouse_id": "3001", "is_express": false}}, "has_next": false})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (p *ordersProcessProvider) posting(number, scheme string) map[string]any {
	return map[string]any{"posting_number": number, "order_id": "5001", "order_number": "synthetic-order", "status": "awaiting_deliver", "substatus": "posting_created", "scheme": scheme, "created_at": "2026-10-29T08:00:00Z", "updated_at": "2026-10-29T08:01:00Z", "delivery_method": map[string]any{"id": "4001", "warehouse_id": "3001"}, "products": []any{map[string]any{"sku": "2001", "product_id": "1001", "offer_id": "process-offer", "quantity": 2, "price": "12.34", "currency_code": "RUB", "vat": "20"}}}
}

func (p *ordersProcessProvider) moy(w http.ResponseWriter, r *http.Request, account string) {
	if r.Method == http.MethodPost && r.URL.Path == "/entity/customerorder" {
		p.create(w, r, account)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.URL.Path == "/context/employee" {
		p.reply(w, map[string]string{"accountId": account})
		return
	}
	if r.URL.Path == "/entity/customerorder" {
		p.lookup(w, r, account)
		return
	}
	for _, native := range p.native {
		if native.account == account && r.URL.Path == "/entity/customerorder/"+native.id {
			p.reply(w, p.document(native))
			return
		}
		if native.account == account && r.URL.Path == "/entity/customerorder/"+native.id+"/positions" {
			p.positions(w, r, native)
			return
		}
	}
	p.dictionary(w, r, account)
}

func (p *ordersProcessProvider) create(w http.ResponseWriter, r *http.Request, account string) {
	var body map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	number, ok := body["name"].(string)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	p.writes[number]++
	native := &ordersProcessNative{account: account, posting: number, id: ordersProcessID(100 + len(p.native)), body: body, hidden: p.lost[number]}
	p.native[body["syncId"].(string)] = native
	lost := p.lost[number]
	identity := p.identity(native)
	p.mu.Unlock()
	if lost {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			if err = conn.Close(); err != nil {
				p.t.Error("close synthetic lost acknowledgement")
			}
		}
		return
	}
	p.reply(w, identity)
}

func (p *ordersProcessProvider) lookup(w http.ResponseWriter, r *http.Request, account string) {
	rows := make([]any, 0, 1)
	filter := r.URL.Query().Get("filter")
	for syncID, native := range p.native {
		if native.account == account && !native.hidden && strings.Contains(filter, "syncId="+syncID) && strings.Contains(filter, "externalCode="+native.body["externalCode"].(string)) {
			rows = append(rows, p.identity(native))
		}
	}
	p.page(w, rows, len(rows), 2, 0)
}

func (p *ordersProcessProvider) identity(native *ordersProcessNative) map[string]any {
	row := p.reference("entity/customerorder", "customerorder", native.id)
	row["id"], row["accountId"] = native.id, native.account
	row["syncId"], row["externalCode"] = native.body["syncId"], native.body["externalCode"]
	return row
}

func (p *ordersProcessProvider) document(native *ordersProcessNative) map[string]any {
	row := p.identity(native)
	for key, value := range native.body {
		row[key] = value
	}
	row["updated"], row["sum"] = "2026-10-30 11:00:00.000", json.Number("2468")
	row["rate"] = map[string]any{"currency": p.reference("entity/currency", "currency", ordersProcessID(7)), "value": json.Number("1")}
	row["positions"] = map[string]any{"meta": map[string]any{"href": p.server.URL + "/entity/customerorder/" + native.id + "/positions", "type": "customerorderposition", "size": 1}}
	return row
}

func (p *ordersProcessProvider) positions(w http.ResponseWriter, r *http.Request, native *ordersProcessNative) {
	positions := native.body["positions"].([]any)
	position := positions[0].(map[string]any)
	row := make(map[string]any, len(position)+2)
	for key, value := range position {
		row[key] = value
	}
	row["id"], row["accountId"] = ordersProcessID(999), native.account
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	p.page(w, []any{row}, 1, limit, 0)
}

func (p *ordersProcessProvider) dictionary(w http.ResponseWriter, r *http.Request, account string) {
	if r.URL.Path == "/entity/customerorder/metadata" {
		state := p.reference("entity/customerorder/metadata/states", "state", ordersProcessID(5))
		state["id"], state["accountId"], state["entityType"], state["name"], state["stateType"] = ordersProcessID(5), account, "customerorder", "Synthetic state", "Regular"
		p.reply(w, map[string]any{"states": []any{state}, "attributes": []any{}})
		return
	}
	for index, kind := range []string{"organization", "counterparty", "store", "saleschannel", "currency"} {
		if r.URL.Path != "/entity/"+kind {
			continue
		}
		id := ordersProcessID([]int{2, 3, 4, 6, 7}[index])
		row := p.reference("entity/"+kind, kind, id)
		row["id"], row["accountId"], row["name"] = id, account, "Synthetic "+kind
		if kind == "currency" {
			row["isoCode"], row["rate"], row["default"] = "RUB", json.Number("1"), true
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		p.page(w, []any{row}, 1, limit, 0)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (p *ordersProcessProvider) reference(path, kind, id string) map[string]any {
	return map[string]any{"meta": map[string]any{"href": p.server.URL + "/" + path + "/" + id, "type": kind, "mediaType": "application/json"}}
}

func (p *ordersProcessProvider) page(w http.ResponseWriter, rows []any, size, limit, offset int) {
	p.reply(w, map[string]any{"meta": map[string]any{"size": size, "limit": limit, "offset": offset}, "rows": rows})
}

func (p *ordersProcessProvider) reply(w http.ResponseWriter, body any) {
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}

func (p *ordersProcessProvider) writeCount(number string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writes[number]
}

func (p *ordersProcessProvider) reveal(number string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, native := range p.native {
		if native.posting == number {
			native.hidden = false
		}
	}
}

func (p *ordersProcessProvider) assertBudget(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	last := make(map[string]ordersProcessAttempt)
	for account, maximum := range p.maximum {
		if maximum > 2 {
			t.Errorf("shared provider inflight exceeded for synthetic account %s: %d", account, maximum)
		}
	}
	for _, attempt := range p.attempts {
		previous, found := last[attempt.account]
		if found && attempt.at.Sub(previous.at) < 14*time.Millisecond {
			t.Error("independent processes bypassed shared provider request spacing")
		}
		if found && previous.status == http.StatusTooManyRequests && attempt.at.Sub(previous.at) < 950*time.Millisecond {
			t.Error("independent processes bypassed shared 429 cooldown")
		}
		last[attempt.account] = attempt
	}
	t.Logf("observed %d real loopback provider HTTP attempts across nine processes", len(p.attempts))
}

func ordersProcessNumber(prefix string, number int) string {
	return fmt.Sprintf("%s-%02d", prefix, number)
}
