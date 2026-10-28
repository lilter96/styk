package moyskladfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.moysklad.ru/api/remap/1.2/"

var entityPath = regexp.MustCompile(`^entity/(store|product|variant|counterparty|enter|customerorder|demand)(/[0-9a-fA-F-]{36}(/positions)?)?$`)
var characteristicPath = regexp.MustCompile(`^entity/variant/metadata/characteristics(/[0-9a-fA-F-]{36})?$`)
var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// Client is isolated fixture infrastructure; it has no generic mutation method.
// A transport can be injected by tests without contacting any real service.
type Client struct {
	account, token        string
	http                  *http.Client
	interval              time.Duration
	next                  time.Time
	requests, maxRequests int
	organization          string
	counts                map[string]int
}

func NewClient(account, token string, timeout, interval time.Duration, maxRequests int, transport http.RoundTripper) (*Client, error) {
	if !uuidPattern.MatchString(account) || strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("test account and token are required")
	}
	if timeout <= 0 || timeout > 30*time.Second || interval < 400*time.Millisecond || maxRequests < 1 || maxRequests > 300 {
		return nil, errors.New("invalid bounded fixture client settings")
	}
	h := &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Client{account: account, token: token, http: h, interval: interval, maxRequests: maxRequests, counts: map[string]int{}}, nil
}

func allowed(method, path string) bool {
	if method == http.MethodGet {
		if entityPath.MatchString(path) || characteristicPath.MatchString(path) {
			return true
		}
		switch path {
		case "entity/organization", "entity/webhook", "entity/webhookstock", "entity/supply", "entity/inventory", "entity/variant/metadata", "report/stock/bystore", "report/stock/all":
			return true
		}
	}
	if method == http.MethodPost {
		switch path {
		case "entity/store", "entity/product", "entity/variant", "entity/variant/metadata/characteristics", "entity/counterparty", "entity/enter", "entity/customerorder", "entity/demand":
			return true
		}
	}
	return false
}

func (c *Client) call(ctx context.Context, method, path string, q url.Values, payload any, out any) error {
	if !allowed(method, path) {
		return errors.New("fixture operation is not allowlisted")
	}
	if c.requests >= c.maxRequests {
		return errors.New("fixture request budget exhausted")
	}
	if delay := time.Until(c.next); delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.next = time.Now().Add(c.interval)
	c.requests++
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return errors.New("invalid fixture request")
		}
		body = bytes.NewReader(b)
	}
	u := apiBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return errors.New("cannot construct fixture request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json;charset=utf-8")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return errors.New("fixture transport failed")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(data) > 4<<20 {
		return errors.New("fixture response could not be read safely")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("fixture API returned HTTP %d", response.StatusCode)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err = dec.Decode(out); err != nil {
		return errors.New("fixture API returned an invalid JSON response")
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return errors.New("fixture API returned trailing data")
	}
	return nil
}

type page struct {
	Meta *struct{ Size, Limit, Offset *int } `json:"meta"`
	Rows *[]map[string]any                   `json:"rows"`
}

func (c *Client) page(ctx context.Context, path string, q url.Values) (page, error) {
	var p page
	if err := c.call(ctx, http.MethodGet, path, q, nil, &p); err != nil {
		return p, err
	}
	if p.Meta == nil || p.Meta.Size == nil || p.Meta.Limit == nil || p.Meta.Offset == nil || p.Rows == nil || *p.Meta.Size < 0 || *p.Meta.Limit < 1 || *p.Meta.Offset != 0 || len(*p.Rows) > *p.Meta.Limit || len(*p.Rows) > *p.Meta.Size {
		return p, errors.New("incomplete fixture collection")
	}
	expected := *p.Meta.Size
	if expected > *p.Meta.Limit {
		expected = *p.Meta.Limit
	}
	if len(*p.Rows) != expected {
		return p, errors.New("fixture collection page is incomplete")
	}
	return p, nil
}

func str(m map[string]any, key string) string { s, _ := m[key].(string); return s }

func parseAmount(value string) (*big.Rat, bool) {
	if len(value) > 128 || !decimalPattern.MatchString(value) {
		return nil, false
	}
	if i := strings.IndexAny(value, "eE"); i >= 0 {
		n, err := strconv.Atoi(value[i+1:])
		if err != nil || n < -100 || n > 100 {
			return nil, false
		}
	}
	return new(big.Rat).SetString(value)
}

func equalNumber(a, b any) bool {
	left, ok := parseAmount(fmt.Sprint(a))
	if !ok {
		return false
	}
	right, ok := parseAmount(fmt.Sprint(b))
	return ok && left.Cmp(right) == 0
}

// Preflight requires account proof and rejects active or incompletely visible
// API webhooks. Operator isolation attestation is an additional CLI requirement.
func (c *Client) preflightCheck(ctx context.Context, account string) error {
	if !strings.EqualFold(account, c.account) {
		return errors.New("account mismatch")
	}
	checks := []string{"store", "product", "variant", "customerorder", "enter", "supply", "demand", "inventory", "organization"}
	var accountVerified bool
	for _, kind := range checks {
		p, err := c.page(ctx, "entity/"+kind, url.Values{"limit": {"2"}, "offset": {"0"}})
		if err != nil {
			return err
		}
		c.counts[kind] = *p.Meta.Size
		for _, row := range *p.Rows {
			if str(row, "accountId") == "" || !strings.EqualFold(str(row, "accountId"), account) {
				return errors.New("source account could not be verified")
			}
			accountVerified = true
		}
		if kind == "organization" {
			if *p.Meta.Size != 1 || len(*p.Rows) != 1 || !uuidPattern.MatchString(str((*p.Rows)[0], "id")) {
				return errors.New("exactly one verified organization is required")
			}
			c.organization = str((*p.Rows)[0], "id")
		}
	}
	if !accountVerified {
		return errors.New("source account could not be verified")
	}
	for _, kind := range []string{"webhook", "webhookstock"} {
		p, err := c.page(ctx, "entity/"+kind, url.Values{"limit": {"1000"}, "offset": {"0"}})
		if err != nil {
			return err
		}
		if len(*p.Rows) != *p.Meta.Size {
			return errors.New("webhook coverage incomplete")
		}
		for _, row := range *p.Rows {
			enabled, ok := row["enabled"].(bool)
			if !ok || enabled || !strings.EqualFold(str(row, "accountId"), account) {
				return errors.New("active, cross-account or unknown webhook state")
			}
		}
	}
	// An absent characteristics field is not evidence of an empty collection.
	var meta map[string]any
	if err := c.call(ctx, http.MethodGet, "entity/variant/metadata", nil, nil, &meta); err != nil {
		return err
	}
	return nil
}

func (c *Client) preflight(ctx context.Context, account string) (map[string]int, error) {
	if err := c.preflightCheck(ctx, account); err != nil {
		return nil, err
	}
	return c.Counts(), nil
}

func (c *Client) Counts() map[string]int {
	result := map[string]int{}
	for k, v := range c.counts {
		result[k] = v
	}
	return result
}

func includeInactive(kind, filter string) string {
	switch kind {
	case "store", "product", "variant", "counterparty":
		return filter + ";archived=true;archived=false"
	case "enter", "customerorder", "demand":
		return filter + ";isDeleted=true;isDeleted=false"
	default:
		return filter
	}
}

// CheckNamespace runs before the first POST and fails closed on collisions,
// missing pagination metadata or an unexpected source response.
func (c *Client) checkNamespace(ctx context.Context, m Manifest) error {
	for _, kind := range []string{"store", "product", "variant", "counterparty", "enter", "customerorder", "demand"} {
		field := "externalCode"
		if kind == "store" {
			field = "code"
		}
		p, err := c.page(ctx, "entity/"+kind, url.Values{"limit": {"1"}, "offset": {"0"}, "filter": {includeInactive(kind, field+"~="+m.Namespace)}})
		if err != nil {
			return err
		}
		if *p.Meta.Size != 0 || len(*p.Rows) != 0 {
			return ErrDuplicate
		}
	}
	return nil
}

func (c *Client) search(ctx context.Context, o Operation) ([]string, error) {
	if o.Kind == "characteristic" {
		// Both documented collection forms are checked, but optional arrays do
		// not prove completeness. Found matches block creation; no matches are
		// only an uncertain search, never authoritative evidence of absence.
		seen := map[string]bool{}
		for _, path := range []string{"entity/variant/metadata", "entity/variant/metadata/characteristics"} {
			var result map[string]any
			if err := c.call(ctx, http.MethodGet, path, nil, nil, &result); err != nil {
				return nil, err
			}
			for _, field := range []string{"characteristics", "rows"} {
				value, present := result[field]
				if !present {
					continue
				}
				values, ok := value.([]any)
				if !ok {
					return nil, errors.New("invalid characteristic collection")
				}
				for _, value := range values {
					row, ok := value.(map[string]any)
					if !ok || str(row, "name") == "" || !uuidPattern.MatchString(str(row, "id")) {
						return nil, errors.New("invalid characteristic collection entry")
					}
					if strings.HasPrefix(str(row, "name"), strings.TrimSuffix(o.Identity, "-variant-characteristic")+"-") {
						seen[str(row, "id")] = true
					}
				}
			}
		}
		var matches []string
		for id := range seen {
			matches = append(matches, id)
		}
		return matches, ErrCoverageUnknown
	}
	field := "externalCode"
	if o.Kind == "store" {
		field = "code"
	}
	p, err := c.page(ctx, "entity/"+o.Kind, url.Values{"limit": {"2"}, "offset": {"0"}, "filter": {includeInactive(o.Kind, field+"="+o.Identity)}})
	if err != nil {
		return nil, err
	}
	if len(*p.Rows) != *p.Meta.Size {
		return nil, errors.New("fixture identity search is incomplete or ambiguous")
	}
	var ids []string
	for _, row := range *p.Rows {
		id := str(row, "id")
		if str(row, field) != o.Identity || !strings.EqualFold(str(row, "accountId"), c.account) || !uuidPattern.MatchString(id) {
			return nil, errors.New("fixture identity search mismatch")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func findID(m Manifest, key string) (string, bool) {
	for _, o := range m.Operations {
		if o.Key == key && o.State == Created && uuidPattern.MatchString(o.RemoteID) {
			return o.RemoteID, true
		}
	}
	return "", false
}

func reference(kind, id string) map[string]any {
	return map[string]any{"meta": map[string]any{"href": apiBase + "entity/" + kind + "/" + id, "type": kind, "mediaType": "application/json"}}
}

func (c *Client) prepare(_ context.Context, o Operation, m Manifest) error {
	switch o.Kind {
	case "characteristic":
		if !m.MultipleOrderProductOnly {
			return ErrUnsupported
		}
	case "variant":
		if _, ok := findID(m, "variant-characteristic"); !ok {
			return ErrUnsupported
		}
		if _, ok := findID(m, "variant-parent"); !ok {
			return errors.New("variant parent unavailable")
		}
	case "enter", "customerorder", "demand":
		if c.organization == "" {
			return errors.New("organization unavailable")
		}
		if o.Key == "order-multiple" {
			if _, ok := findID(m, "variant-one"); !ok {
				variantSkipped := false
				for _, v := range m.Operations {
					if v.Key == "variant-one" && v.State == Skipped {
						variantSkipped = true
					}
				}
				if !variantSkipped {
					return errors.New("variant prerequisite is unresolved")
				}
			}
		}
	}
	_, err := c.payload(o, m)
	return err
}

func (c *Client) payload(o Operation, m Manifest) (map[string]any, error) {
	if o.Kind == "characteristic" {
		if !m.MultipleOrderProductOnly {
			return nil, ErrUnsupported
		}
		return map[string]any{"name": o.Identity}, nil
	}
	p := map[string]any{"name": o.Identity}
	if o.Kind == "store" {
		p["code"] = o.Identity
	} else {
		p["externalCode"] = o.Identity
	}
	if o.Kind == "product" {
		if o.Key != "no-article" {
			article := o.Identity + "-article"
			if o.Key == "duplicate-a" || o.Key == "duplicate-b" {
				article = m.Namespace + "-duplicate-article"
			}
			p["article"] = article
		}
		if o.Key == "product-e" {
			p["code"] = m.Namespace + "-distinct-code"
		}
		return p, nil
	}
	if o.Kind == "store" || o.Kind == "counterparty" {
		return p, nil
	}
	if o.Kind == "variant" {
		id, ok := findID(m, "variant-parent")
		characteristic, characteristicOK := findID(m, "variant-characteristic")
		if !ok || !characteristicOK {
			return nil, ErrUnsupported
		}
		delete(p, "name")
		p["product"] = reference("product", id)
		p["characteristics"] = []any{map[string]any{"id": characteristic, "value": o.Identity}}
		p["article"] = o.Identity + "-article"
		return p, nil
	}
	if c.organization == "" {
		return nil, errors.New("organization unavailable")
	}
	p["organization"] = reference("organization", c.organization)
	p["applicable"] = true
	storeKey := "warehouse-a"
	if o.Key == "stock-b" {
		storeKey = "warehouse-b"
	}
	warehouse, ok := findID(m, storeKey)
	if !ok {
		return nil, errors.New("fixture warehouse unavailable")
	}
	p["store"] = reference("store", warehouse)
	if o.Kind == "customerorder" || o.Kind == "demand" {
		agent, ok := findID(m, "counterparty")
		if !ok {
			return nil, errors.New("synthetic counterparty unavailable")
		}
		p["agent"] = reference("counterparty", agent)
	}
	if o.Kind == "demand" {
		order, ok := findID(m, "order-partial")
		if !ok {
			return nil, errors.New("synthetic customer order unavailable")
		}
		p["customerOrder"] = reference("customerorder", order)
	}
	type item struct {
		key, kind         string
		quantity, reserve int
	}
	var positions []item
	switch o.Key {
	case "stock-a":
		positions = []item{{"product-a", "product", 10, 0}, {"product-b", "product", 5, 0}}
	case "stock-b":
		positions = []item{{"product-a", "product", 4, 0}, {"product-c", "product", 12, 0}}
	case "stock-variants":
		positions = []item{{"variant-one", "variant", 8, 0}, {"variant-two", "variant", 3, 0}}
	case "order-reserved":
		positions = []item{{"product-a", "product", 3, 2}}
	case "order-zero-reserve":
		positions = []item{{"product-b", "product", 1, 0}}
	case "order-multiple":
		if _, ok := findID(m, "variant-one"); ok && !m.MultipleOrderProductOnly {
			positions = []item{{"product-c", "product", 2, 0}, {"variant-one", "variant", 1, 0}}
		} else {
			positions = []item{{"product-c", "product", 2, 0}, {"product-a", "product", 1, 0}}
		}
	case "order-partial":
		positions = []item{{"product-b", "product", 2, 2}}
	case "order-variant":
		positions = []item{{"variant-one", "variant", 2, 1}}
	case "shipment-partial":
		positions = []item{{"product-b", "product", 1, 0}}
	default:
		return nil, errors.New("unsupported fixture operation")
	}
	var values []any
	for _, x := range positions {
		id, ok := findID(m, x.key)
		if !ok {
			return nil, errors.New("fixture product unavailable")
		}
		v := map[string]any{"quantity": x.quantity, "price": 0, "assortment": reference(x.kind, id)}
		if o.Kind == "customerorder" {
			v["reserve"] = x.reserve
		}
		values = append(values, v)
	}
	p["positions"] = values
	return p, nil
}

func (c *Client) create(ctx context.Context, o Operation, m Manifest) (string, error) {
	p, err := c.payload(o, m)
	if err != nil {
		return "", err
	}
	var result map[string]any
	path := "entity/" + o.Kind
	if o.Kind == "characteristic" {
		path = "entity/variant/metadata/characteristics"
	}
	if err = c.call(ctx, http.MethodPost, path, nil, p, &result); err != nil {
		return "", err
	}
	id := str(result, "id")
	if o.Kind == "characteristic" {
		if !uuidPattern.MatchString(id) {
			return "", errors.New("characteristic response lacked an identifier")
		}
		return id, nil // Persist first; independent GET checks the full identity.
	}
	if !uuidPattern.MatchString(id) || !strings.EqualFold(str(result, "accountId"), c.account) {
		return "", errors.New("fixture create response did not verify identity")
	}
	return id, nil
}

func referenceID(value any, kind string) (string, bool) {
	v, ok := value.(map[string]any)
	if !ok {
		return "", false
	}
	meta, ok := v["meta"].(map[string]any)
	if !ok || str(meta, "type") != kind {
		return "", false
	}
	u, err := url.Parse(str(meta, "href"))
	// Stock-report entity links can include query parameters. Identity comes
	// only from the validated origin and entity path; the query is never used.
	if err != nil || u.Scheme != "https" || u.Host != "api.moysklad.ru" || u.Fragment != "" {
		return "", false
	}
	prefix := "/api/remap/1.2/entity/" + kind + "/"
	if !strings.HasPrefix(u.Path, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(u.Path, prefix)
	return id, uuidPattern.MatchString(id)
}

// Verify validates identity and fixture content using independent GETs.
func (c *Client) verify(ctx context.Context, o Operation, id string, m Manifest) error {
	if !uuidPattern.MatchString(id) {
		return errors.New("invalid fixture identifier")
	}
	var got map[string]any
	path := "entity/" + o.Kind + "/" + id
	if o.Kind == "characteristic" {
		path = "entity/variant/metadata/characteristics/" + id
	}
	if err := c.call(ctx, http.MethodGet, path, nil, nil, &got); err != nil {
		return err
	}
	if o.Kind == "characteristic" {
		meta, ok := got["meta"].(map[string]any)
		if !ok || str(got, "id") != id || str(got, "name") != o.Identity || str(got, "type") != "string" || got["required"] != false || str(meta, "type") != "attributemetadata" || str(meta, "mediaType") != "application/json" || str(meta, "href") != apiBase+path {
			return errors.New("characteristic identity or metadata mismatch")
		}
		if account := str(got, "accountId"); account != "" && !strings.EqualFold(account, c.account) {
			return errors.New("characteristic account mismatch")
		}
		return nil
	}
	field := "externalCode"
	if o.Kind == "store" {
		field = "code"
	}
	if str(got, "id") != id || !strings.EqualFold(str(got, "accountId"), c.account) || str(got, field) != o.Identity {
		return errors.New("remote fixture identity mismatch")
	}
	expected, err := c.payload(o, m)
	if err != nil {
		return err
	}
	if o.Kind != "variant" && str(got, "name") != o.Identity {
		return errors.New("remote fixture name mismatch")
	}
	if o.Kind == "product" || o.Kind == "variant" {
		if str(got, "article") != str(expected, "article") {
			return errors.New("remote article mismatch")
		}
		if code := str(expected, "code"); code != "" && str(got, "code") != code {
			return errors.New("remote code mismatch")
		}
	}
	if o.Kind == "variant" {
		characteristic, ok := findID(m, "variant-characteristic")
		if !ok {
			return errors.New("verified characteristic missing")
		}
		actual, ok := referenceID(got["product"], "product")
		wanted, _ := referenceID(expected["product"], "product")
		if !ok || actual != wanted {
			return errors.New("remote variant parent mismatch")
		}
		chars, ok := got["characteristics"].([]any)
		if !ok {
			return errors.New("variant characteristics missing")
		}
		matches := 0
		for _, v := range chars {
			if char, ok := v.(map[string]any); ok && str(char, "id") == characteristic && str(char, "value") == o.Identity {
				matches++
			}
		}
		if matches != 1 {
			return errors.New("variant characteristics mismatch")
		}
	}
	if o.Kind != "enter" && o.Kind != "customerorder" && o.Kind != "demand" {
		return nil
	}
	for _, kind := range []string{"organization", "store"} {
		actual, ok := referenceID(got[kind], kind)
		wanted, _ := referenceID(expected[kind], kind)
		if !ok || actual != wanted {
			return errors.New("remote fixture reference mismatch")
		}
	}
	if o.Kind == "customerorder" || o.Kind == "demand" {
		actual, ok := referenceID(got["agent"], "counterparty")
		wanted, _ := referenceID(expected["agent"], "counterparty")
		if !ok || actual != wanted {
			return errors.New("remote counterparty mismatch")
		}
	}
	if o.Kind == "demand" {
		actual, ok := referenceID(got["customerOrder"], "customerorder")
		wanted, _ := referenceID(expected["customerOrder"], "customerorder")
		if !ok || actual != wanted {
			return errors.New("shipment order reference mismatch")
		}
	}
	if applicable, ok := got["applicable"].(bool); !ok || !applicable {
		return errors.New("document is not applied")
	}
	if o.Key == "order-variant" {
		updated := str(got, "updated")
		if _, err := time.Parse("2006-01-02 15:04:05.000", updated); err != nil {
			if _, err = time.Parse("2006-01-02 15:04:05", updated); err != nil {
				return errors.New("variant order source timestamp missing or invalid")
			}
		}
	}
	p, err := c.page(ctx, "entity/"+o.Kind+"/"+id+"/positions", url.Values{"limit": {"100"}, "offset": {"0"}})
	if err != nil {
		return err
	}
	wanted := expected["positions"].([]any)
	if len(*p.Rows) != *p.Meta.Size || len(*p.Rows) != len(wanted) {
		return errors.New("document positions incomplete")
	}
	for _, want := range wanted {
		w := want.(map[string]any)
		kind := str(w["assortment"].(map[string]any)["meta"].(map[string]any), "type")
		expectedID, _ := referenceID(w["assortment"], kind)
		matches := 0
		for _, actual := range *p.Rows {
			actualID, ok := referenceID(actual["assortment"], kind)
			if !ok || actualID != expectedID {
				continue
			}
			matches++
			if o.Key == "order-variant" && !uuidPattern.MatchString(str(actual, "id")) {
				return errors.New("variant order position identity missing")
			}
			if !strings.EqualFold(str(actual, "accountId"), c.account) {
				return errors.New("position account mismatch")
			}
			if !equalNumber(actual["quantity"], w["quantity"]) {
				return errors.New("position quantity mismatch")
			}
			checkReserve := o.Kind == "customerorder"
			if o.Key == "order-partial" {
				for _, v := range m.Operations {
					if v.Key == "shipment-partial" && (v.State == Unknown || v.State == Created) {
						checkReserve = false
					}
				}
			}
			if checkReserve && !equalNumber(actual["reserve"], w["reserve"]) {
				return errors.New("position reserve missing or mismatched")
			}
		}
		if matches != 1 {
			return errors.New("position identity missing or duplicated")
		}
	}
	if o.Kind == "demand" {
		order, _ := findID(m, "order-partial")
		product, _ := findID(m, "product-b")
		positions, err := c.page(ctx, "entity/customerorder/"+order+"/positions", url.Values{"limit": {"100"}, "offset": {"0"}})
		if err != nil {
			return err
		}
		if len(*positions.Rows) != *positions.Meta.Size {
			return errors.New("shipment order position coverage incomplete")
		}
		matches := 0
		for _, position := range *positions.Rows {
			id, ok := referenceID(position["assortment"], "product")
			if !ok || id != product {
				continue
			}
			matches++
			if !strings.EqualFold(str(position, "accountId"), c.account) || !equalNumber(position["quantity"], 2) || !equalNumber(position["shipped"], 1) {
				return errors.New("partial shipment is not independently verified")
			}
		}
		if matches != 1 {
			return errors.New("shipment order position not uniquely verified")
		}
	}
	return nil
}

// ReadOnlyPreflight performs only account and webhook GET checks.
func (c *Client) ReadOnlyPreflight(ctx context.Context, account string) error {
	return c.preflightCheck(ctx, account)
}
