package moysklad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lilter96/styk/internal/domain"
)

const BaseURL = "https://api.moysklad.ru/api/remap/1.2/"
const maxBody = 16 << 20
const maxRows = 200000

var orderReadPath = regexp.MustCompile(`^entity/customerorder/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(/positions)?$`)
var orderUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func allowedReadPath(path string) bool {
	switch path {
	case "context/employee", "entity/product", "entity/variant", "entity/store", "report/stock/all", "report/stock/bystore", "entity/customerorder":
		return true
	}
	return orderReadPath.MatchString(path) || allowedCatalogReadPath(path)
}

type Options struct {
	BaseURL, Token                            string
	Timeout, Interval, Backoff, MaxRetryAfter time.Duration
	Attempts, PageSize                        int
	HTTPClient                                *http.Client
}
type Client struct {
	base                             *url.URL
	token                            string
	http                             *http.Client
	interval, backoff, maxRetryAfter time.Duration
	attempts, pageSize               int
	mu                               sync.Mutex
	next                             time.Time
}
type APIError struct {
	Status   int
	Category string
}

func (e *APIError) Error() string { return fmt.Sprintf("moysklad %s (HTTP %d)", e.Category, e.Status) }
func New(o Options) (*Client, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid MoySklad base URL")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	if strings.TrimSpace(o.Token) == "" || strings.ContainsAny(o.Token, "\r\n") {
		return nil, errors.New("MoySklad token is required")
	}
	if o.Timeout <= 0 || o.Interval <= 0 || o.Backoff <= 0 || o.Backoff > 30*time.Second || o.MaxRetryAfter <= 0 || o.Attempts < 1 || o.Attempts > 10 || o.PageSize < 1 || o.PageSize > 1000 {
		return nil, errors.New("invalid MoySklad client settings")
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: o.Timeout}
	} else {
		clone := *hc
		hc = &clone
		hc.Timeout = o.Timeout
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, token: o.Token, http: hc, interval: o.Interval, backoff: o.Backoff, maxRetryAfter: o.MaxRetryAfter, attempts: o.Attempts, pageSize: o.PageSize}, nil
}
func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	now := time.Now()
	at := c.next
	if at.Before(now) {
		at = now
	}
	c.next = at.Add(c.interval)
	c.mu.Unlock()
	return sleep(ctx, time.Until(at))
}
func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func retryAfter(v string, now time.Time) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	trimmed := strings.TrimSpace(v)
	if trimmed != "" && strings.Trim(trimmed, "0123456789") == "" {
		n, e := strconv.ParseUint(trimmed, 10, 64)
		if e != nil || n > uint64((1<<63-1)/int64(time.Second)) {
			return time.Duration(1<<63 - 1), true
		}
		return time.Duration(n) * time.Second, true
	}
	if t, e := http.ParseTime(v); e == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}
func millisHeader(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, false
	}
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil || n > uint64((1<<63-1)/int64(time.Millisecond)) {
		return time.Duration(1<<63 - 1), true
	}
	return time.Duration(n) * time.Millisecond, true
}
func rateDelay(h http.Header, status int, now time.Time) time.Duration {
	var delay time.Duration
	if status == http.StatusTooManyRequests {
		if d, ok := retryAfter(h.Get("Retry-After"), now); ok && d > delay {
			delay = d
		}
		if d, ok := millisHeader(h.Get("X-Lognex-Retry-After")); ok && d > delay {
			delay = d
		}
	}
	if status == http.StatusTooManyRequests || strings.TrimSpace(h.Get("X-RateLimit-Remaining")) == "0" {
		if d, ok := millisHeader(h.Get("X-Lognex-Reset")); ok && d > delay {
			delay = d
		}
	}
	return delay
}
func (c *Client) deferRequests(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	at := time.Now().Add(d)
	if at.After(c.next) {
		c.next = at
	}
}
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, error) {
	if !allowedReadPath(path) {
		return nil, errors.New("MoySklad read path is not allowlisted")
	}
	b, _ := ctx.Value(budgetKey{}).(*budget)
	if err := b.add("requests"); err != nil {
		return nil, err
	}
	bodyLimit := int64(maxBody)
	if b != nil && b.limits.ResponseBytes < bodyLimit {
		bodyLimit = b.limits.ResponseBytes
	}
	u := c.base.ResolveReference(&url.URL{Path: path})
	u.RawQuery = q.Encode()
	for attempt := 0; attempt < c.attempts; attempt++ {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, errors.New("construct MoySklad request")
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json;charset=utf-8")
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := b.add("attempts"); err != nil {
			return nil, err
		}
		hc := c.http
		if b != nil {
			hc = b.http
		}
		resp, err := hc.Do(req)
		if err == nil && resp.StatusCode == http.StatusTooManyRequests && b != nil {
			b.counts.RateLimitResponses++
		}
		var retry bool
		var delay time.Duration
		var last error
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			last = ErrTransport
			retry = true
		} else {
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
			closeErr := resp.Body.Close()
			if readErr != nil || closeErr != nil {
				last = ErrTransport
				retry = true
			} else if int64(len(data)) > bodyLimit {
				return nil, &LimitError{"response_bytes"}
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				delay := rateDelay(resp.Header, resp.StatusCode, time.Now())
				if delay > c.maxRetryAfter {
					return nil, errors.New("MoySklad rate limit reset exceeds configured maximum")
				}
				c.deferRequests(delay)
				return data, nil
			} else {
				category := "request rejected"
				switch {
				case resp.StatusCode == 401:
					category = "authentication failed"
				case resp.StatusCode == 403:
					category = "access forbidden"
				case resp.StatusCode == 429:
					category = "rate limited"
					retry = true
				case resp.StatusCode >= 500:
					category = "server error"
					retry = true
				}
				last = &APIError{resp.StatusCode, category}
				if retry {
					delay = rateDelay(resp.Header, resp.StatusCode, time.Now())
					if delay > c.maxRetryAfter {
						return nil, fmt.Errorf("%w: rate limit delay exceeds configured maximum", last)
					}
					c.deferRequests(delay)
				}
			}
		}
		if !retry || attempt+1 == c.attempts {
			return nil, last
		}
		backoff := c.backoff * time.Duration(1<<attempt)
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		backoff += time.Duration(rand.Int64N(int64(backoff) + 1))
		if delay < backoff {
			delay = backoff
		}
		if err = sleep(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("retry limit reached")
}

type meta struct {
	Size   *int `json:"size"`
	Limit  *int `json:"limit"`
	Offset *int `json:"offset"`
}
type page struct {
	Meta *meta              `json:"meta"`
	Rows *[]json.RawMessage `json:"rows"`
}

type orderPageSizeKey struct{}

func (c *Client) pages(ctx context.Context, path string, extra url.Values, consume func(json.RawMessage) error) error {
	b, _ := ctx.Value(budgetKey{}).(*budget)
	pageSize := c.pageSize
	if v, ok := ctx.Value(orderPageSizeKey{}).(int); ok && v >= 1 && v <= 1000 {
		pageSize = v
	}
	offset, total := 0, -1
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.report(ctx, path); err != nil {
			return err
		}
		if err := b.add("pages"); err != nil {
			return err
		}
		q := url.Values{}
		for k, v := range extra {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", strconv.Itoa(offset))
		data, err := c.get(ctx, path, q)
		if err != nil {
			return fmt.Errorf("read %s page at %d: %w", path, offset, err)
		}
		var p page
		if err = json.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("decode %s page: %w", path, err)
		}
		if b != nil {
			b.counts.CompletedRequests++
		}
		if p.Meta == nil || p.Rows == nil || p.Meta.Size == nil || p.Meta.Offset == nil || p.Meta.Limit == nil {
			return fmt.Errorf("%s: incomplete page metadata or rows", path)
		}
		m := p.Meta
		n := len(*p.Rows)
		if *m.Offset != offset || *m.Limit < 1 || *m.Limit > 1000 || n > *m.Limit || *m.Size < 0 || *m.Size > maxRows || offset+n > *m.Size {
			return fmt.Errorf("%s: inconsistent pagination", path)
		}
		if total < 0 {
			total = *m.Size
		} else if total != *m.Size {
			return fmt.Errorf("%s: collection changed during pagination", path)
		}
		for _, row := range *p.Rows {
			if err := consume(row); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
		offset += n
		if n == 0 && offset < total {
			return fmt.Errorf("%s: incomplete pagination", path)
		}
		if b != nil {
			b.counts.CompletedPages++
			if err := b.report(ctx, path); err != nil {
				return err
			}
		}
		if offset == total {
			return nil
		}
		if n == 0 {
			return fmt.Errorf("%s: incomplete pagination", path)
		}
	}
}

type ref struct {
	Meta struct {
		Href string `json:"href"`
		Type string `json:"type"`
	} `json:"meta"`
}
type entity struct {
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	Name      string `json:"name"`
	Code      string `json:"code"`
	Article   string `json:"article"`
	Updated   string `json:"updated"`
	Product   *ref   `json:"product"`
}
type stockRow struct {
	Meta struct {
		Href string `json:"href"`
		Type string `json:"type"`
	} `json:"meta"`
	Stock     *json.Number `json:"stock"`
	Reserve   *json.Number `json:"reserve"`
	InTransit *json.Number `json:"inTransit"`
	Quantity  *json.Number `json:"quantity"`
	FreeStock *json.Number `json:"freeStock"`
}

type warehouseRow struct {
	Meta struct {
		Href string `json:"href"`
		Type string `json:"type"`
	} `json:"meta"`
	Stores *[]struct {
		Meta struct {
			Href string `json:"href"`
			Type string `json:"type"`
		} `json:"meta"`
		Stock     *json.Number `json:"stock"`
		Reserve   *json.Number `json:"reserve"`
		InTransit *json.Number `json:"inTransit"`
	} `json:"stockByStore"`
}

func number(n *json.Number) *string {
	if n == nil {
		return nil
	}
	s := n.String()
	return &s
}
func idFromHref(href, kind string) (string, error) {
	u, e := url.Parse(href)
	if e != nil {
		return "", errors.New("invalid source identity")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != kind || parts[len(parts)-1] == "" {
		return "", errors.New("missing source identity")
	}
	return parts[len(parts)-1], nil
}
func (c *Client) Read(ctx context.Context, account string) (domain.InventoryImport, error) {
	if strings.TrimSpace(account) == "" {
		return domain.InventoryImport{}, errors.New("account identifier required")
	}
	b, _ := ctx.Value(budgetKey{}).(*budget)
	snap := domain.InventoryImport{Account: account, StartedAt: time.Now().UTC()}
	seen := map[string]bool{}
	for _, kind := range []string{"product", "variant", "store"} {
		err := c.pages(ctx, "entity/"+kind, url.Values{"order": {"id,asc"}}, func(raw json.RawMessage) error {
			var e entity
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			if e.ID == "" {
				return errors.New("entity missing id")
			}
			if e.AccountID != "" && e.AccountID != account {
				return errors.New("entity account does not match configured account")
			}
			key := kind + ":" + e.ID
			if seen[key] {
				return fmt.Errorf("duplicate %s identifier", kind)
			}
			seen[key] = true
			parent := ""
			if e.Product != nil {
				var err error
				parent, err = idFromHref(e.Product.Meta.Href, "product")
				if err != nil {
					return fmt.Errorf("invalid variant product reference: %w", err)
				}
			}
			resource := map[string]string{"product": "products", "variant": "variants", "store": "warehouses"}[kind]
			if err := b.add(resource); err != nil {
				return err
			}
			snap.Entities = append(snap.Entities, domain.ExternalEntity{Kind: kind, ID: e.ID, Name: e.Name, Code: e.Code, Article: e.Article, ParentID: parent, SourceUpdated: e.Updated})
			return nil
		})
		if err != nil {
			return domain.InventoryImport{}, err
		}
	}
	stockSeen := map[string]bool{}
	err := c.pages(ctx, "report/stock/all", url.Values{"filter": {"stockMode=all;quantityMode=all"}}, func(raw json.RawMessage) error {
		var row stockRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		if row.Meta.Type == "" || row.Meta.Href == "" {
			return errors.New("stock row missing identity")
		}
		id, err := idFromHref(row.Meta.Href, row.Meta.Type)
		if err != nil {
			return err
		}
		key := row.Meta.Type + ":" + id
		if stockSeen[key] {
			return errors.New("duplicate stock identifier")
		}
		stockSeen[key] = true
		if row.Stock == nil || row.Reserve == nil || row.InTransit == nil || row.Quantity == nil {
			return errors.New("stock row missing values")
		}
		if err := b.add("observations"); err != nil {
			return err
		}
		snap.Stock = append(snap.Stock, domain.StockObservation{Kind: row.Meta.Type, ExternalID: id, SourceHref: row.Meta.Href, Stock: number(row.Stock), Reserve: number(row.Reserve), InTransit: number(row.InTransit), Quantity: number(row.Quantity), FreeStock: number(row.FreeStock)})
		return nil
	})
	if err != nil {
		return domain.InventoryImport{}, err
	}
	warehouseItems := map[string]bool{}
	err = c.pages(ctx, "report/stock/bystore", url.Values{"filter": {"stockMode=all"}, "groupBy": {"variant"}}, func(raw json.RawMessage) error {
		var row warehouseRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return err
		}
		if row.Meta.Type != "product" && row.Meta.Type != "variant" {
			return errors.New("unexpected warehouse stock item type")
		}
		id, err := idFromHref(row.Meta.Href, row.Meta.Type)
		if err != nil {
			return err
		}
		key := row.Meta.Type + ":" + id
		if warehouseItems[key] {
			return errors.New("duplicate warehouse report item")
		}
		warehouseItems[key] = true
		if row.Stores == nil {
			return errors.New("warehouse stock list missing")
		}
		stores := map[string]bool{}
		for _, store := range *row.Stores {
			if store.Meta.Type != "store" {
				return errors.New("invalid warehouse type")
			}
			storeID, err := idFromHref(store.Meta.Href, "store")
			if err != nil {
				return err
			}
			if stores[storeID] {
				return errors.New("duplicate warehouse identifier")
			}
			stores[storeID] = true
			if store.Stock == nil || store.Reserve == nil || store.InTransit == nil {
				return errors.New("warehouse stock values missing")
			}
			if len(snap.WarehouseStock) >= maxRows {
				return errors.New("warehouse stock observation limit exceeded")
			}
			if err := b.add("warehouse_observations"); err != nil {
				return err
			}
			snap.WarehouseStock = append(snap.WarehouseStock, domain.WarehouseStockObservation{Kind: row.Meta.Type, ExternalID: id, SourceHref: row.Meta.Href, WarehouseID: storeID, WarehouseHref: store.Meta.Href, Stock: number(store.Stock), Reserve: number(store.Reserve), InTransit: number(store.InTransit)})
		}
		return nil
	})
	if err != nil {
		return domain.InventoryImport{}, err
	}
	snap.FinishedAt = time.Now().UTC()
	return snap, nil
}
