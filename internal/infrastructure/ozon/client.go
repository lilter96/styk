package ozon

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

const BaseURL = "https://api-seller.ozon.ru"
const maxBody = 16 << 20
const maxRows = 200000

var ErrTransport = errors.New("ozon transport failure")

const productPath = "/v3/product/list"
const warehousePath = "/v2/warehouse/list"
const stockPath = "/v2/product/info/stocks-by-warehouse/fbs"

type Options struct {
	BaseURL, ClientID, APIKey                 string
	Timeout, Interval, Backoff, MaxRetryAfter time.Duration
	Attempts, PageSize                        int
	HTTPClient                                *http.Client
	AllowSyntheticStockWrites                 bool
	IncludeProductNames                       bool
}
type Client struct {
	coordinated                               bool
	base                                      *url.URL
	id, key                                   string
	http                                      *http.Client
	timeout, interval, backoff, maxRetryAfter time.Duration
	attempts, pageSize                        int
	mu                                        sync.Mutex
	next                                      time.Time
	allowSyntheticStockWrites                 bool
	controlledTestScope                       *domain.StockSyncScope
	controlledIntent                          *StockUpdate
	controlledAuthorize                       func(context.Context) (bool, error)
	intentMu                                  sync.Mutex
	controlledIntentUsed                      bool
	includeProductNames                       bool
}

// NewControlledTestStockClient permits the owner-authorized test scope and
// official Ozon host. Every write requires a bound, database-revalidated intent.
func NewControlledTestStockClient(scope domain.StockSyncScope, key string) (*Client, error) {
	if !testpublication.Enabled(scope) {
		return nil, ErrStockWritesDisabled
	}
	c, err := New(Options{BaseURL: BaseURL, ClientID: scope.OzonAccount, APIKey: key, Timeout: 20 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		return nil, err
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("standard stock transport unavailable")
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	// Disabling the HTTP/2 handler alone does not stop ALPN from negotiating
	// h2. In that case an HTTP/1 request is written to an HTTP/2 connection,
	// and even a read produces a malformed-response transport error. Advertise
	// only HTTP/1.1 as well, retaining the single-use connection boundary.
	transport.ForceAttemptHTTP2 = false
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	c.http.Transport = transport
	c.controlledTestScope = &scope
	return c, nil
}

type APIError struct {
	Status   int
	Category string
	Code     string
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("Ozon %s (HTTP %d, code %s)", e.Category, e.Status, e.Code)
	}
	return fmt.Sprintf("Ozon %s (HTTP %d)", e.Category, e.Status)
}
func New(o Options) (*Client, error) {
	u, e := url.Parse(o.BaseURL)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return nil, errors.New("invalid Ozon base URL")
	}
	if o.AllowSyntheticStockWrites {
		if o.HTTPClient != nil {
			return nil, errors.New("synthetic stock writes require the standard loopback HTTP transport")
		}
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() || u.Port() == "" {
			return nil, errors.New("stock writes restricted to an explicit loopback test server")
		}
	}
	if strings.TrimSpace(o.ClientID) == "" || strings.TrimSpace(o.APIKey) == "" || strings.ContainsAny(o.ClientID+o.APIKey, "\r\n") {
		return nil, errors.New("ozon client ID and API key are required")
	}
	if o.Timeout <= 0 || o.Interval <= 0 || o.Backoff <= 0 || o.Backoff > 30*time.Second || o.MaxRetryAfter <= 0 || o.Attempts < 1 || o.Attempts > 10 || o.PageSize < 1 || o.PageSize > 200 {
		return nil, errors.New("invalid Ozon client settings")
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = &http.Client{}
	} else {
		h := *hc
		hc = &h
	}
	if o.AllowSyntheticStockWrites {
		baseTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, errors.New("standard synthetic HTTP transport unavailable")
		}
		transport := baseTransport.Clone()
		// Go's standard Transport can transparently retry requests on reused
		// connections. Never reuse a connection for these writes.
		transport.DisableKeepAlives = true
		hc.Transport = transport
	}
	hc.Timeout = o.Timeout
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, id: o.ClientID, key: o.APIKey, http: hc, timeout: o.Timeout, interval: o.Interval, backoff: o.Backoff, maxRetryAfter: o.MaxRetryAfter, attempts: o.Attempts, pageSize: o.PageSize, allowSyntheticStockWrites: o.AllowSyntheticStockWrites, includeProductNames: o.IncludeProductNames}, nil
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
func retryAfter(v string) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if strings.Trim(v, "0123456789") == "" {
		n, e := strconv.ParseUint(v, 10, 64)
		if e != nil || n > uint64((1<<63-1)/int64(time.Second)) {
			return time.Duration(1<<63 - 1), true
		}
		return time.Duration(n) * time.Second, true
	}
	if t, e := http.ParseTime(v); e == nil {
		d := time.Until(t)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

// Only these documented read operations can be invoked; POST does not imply a write.
func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	if path != productPath && path != productInfoPath && path != warehousePath && path != stockPath && path != "/v4/posting/fbs/list" && path != "/v3/posting/fbs/get" && !catalogReadPath(path) && !orderReadPath(path) {
		return errors.New("ozon operation not allowed")
	}
	b, _ := ctx.Value(budgetKey{}).(*budget)
	if b != nil {
		if err := b.add("requests", 1); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	u := c.base.ResolveReference(&url.URL{Path: path})
	for attempt := 0; attempt < c.attempts; attempt++ {
		if err = c.wait(ctx); err != nil {
			return err
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
		if e != nil {
			return errors.New("construct Ozon request")
		}
		req.Header.Set("Client-Id", c.id)
		req.Header.Set("Api-Key", c.key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		if b != nil {
			if b.counts.Attempts >= b.limits.Attempts {
				return &LimitError{Resource: "attempts", Counts: b.counts}
			}
			if attempt == 0 && path != "/v3/posting/fbs/get" {
				if err := b.add("pages", 1); err != nil {
					return err
				}
			}
			if err := b.add("attempts", 1); err != nil {
				return err
			}
		}
		resp, e := c.http.Do(req)
		observeCatalogRequest(ctx, path, resp, e)
		if e == nil && resp.StatusCode == http.StatusTooManyRequests && b != nil {
			b.counts.RateLimitResponses++
		}
		var last error
		retry := false
		var delay time.Duration
		if e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			last = ErrTransport
			retry = true
		} else {
			bodyLimit := int64(maxBody)
			if b != nil {
				bodyLimit = b.limits.ResponseBytes
			}
			data, re := io.ReadAll(io.LimitReader(resp.Body, bodyLimit+1))
			ce := resp.Body.Close()
			if re != nil || ce != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				last = ErrTransport
				retry = true
			} else if int64(len(data)) > bodyLimit {
				if b != nil {
					return &LimitError{Resource: "response_bytes", Counts: b.counts}
				}
				return errors.New("ozon response too large")
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if err = json.Unmarshal(data, out); err != nil {
					return fmt.Errorf("decode Ozon response: %w", err)
				}
				if b != nil {
					b.counts.CompletedRequests++
				}
				return nil
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
				var api struct {
					Code json.RawMessage `json:"code"`
				}
				_ = json.Unmarshal(data, &api)
				code := strings.Trim(string(api.Code), "\"")
				if len(code) > 32 || strings.Trim(code, "-0123456789") != "" {
					code = ""
				}
				last = &APIError{Status: resp.StatusCode, Category: category, Code: code}
				if retry {
					if d, ok := retryAfter(resp.Header.Get("Retry-After")); ok {
						if d > c.maxRetryAfter {
							return fmt.Errorf("%w: Retry-After exceeds configured maximum", last)
						}
						delay = d
					}
				}
			}
		}
		if !retry || attempt+1 == c.attempts {
			return last
		}
		backoff := c.backoff * time.Duration(1<<attempt)
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		backoff += time.Duration(rand.Int64N(int64(backoff) + 1))
		if delay < backoff {
			delay = backoff
		}
		if e = sleep(ctx, delay); e != nil {
			return e
		}
	}
	return errors.New("ozon retry limit reached")
}

func observeCatalogRequest(ctx context.Context, path string, response *http.Response, err error) {
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	catalog.ObserveRequest(ctx, catalog.RequestEvent{Provider: catalog.ProviderOzon, Method: http.MethodPost, Operation: path, Status: status, Interrupted: err != nil || ctx.Err() != nil})
}

// Ozon int64 values may appear as numbers or decimal strings.
type integer int64

func (i *integer) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), "\"")
	v, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return errors.New("invalid Ozon integer")
	}
	*i = integer(v)
	return nil
}
func id(i *integer) string {
	if i == nil || *i <= 0 {
		return ""
	}
	return strconv.FormatInt(int64(*i), 10)
}

type product struct {
	ProductID *integer `json:"product_id"`
	OfferID   string   `json:"offer_id"`
	SKU       *integer `json:"sku"`
	Archived  *bool    `json:"archived"`
}
type productResponse struct {
	Result *struct {
		Items  *[]product `json:"items"`
		LastID string     `json:"last_id"`
		Total  *integer   `json:"total_items"`
	} `json:"result"`
}
type warehouse struct {
	ID            *integer `json:"warehouse_id"`
	Name, Status  string
	WarehouseType string `json:"warehouse_type"`
	IsRFBS        *bool  `json:"is_rfbs"`
}
type warehouseResponse struct {
	Warehouses *[]warehouse `json:"warehouses"`
	Cursor     string       `json:"cursor"`
	HasNext    *bool        `json:"has_next"`
}
type stock struct {
	ProductID     *integer `json:"product_id"`
	OfferID       string   `json:"offer_id"`
	SKU           *integer `json:"sku"`
	WarehouseID   *integer `json:"warehouse_id"`
	WarehouseName string   `json:"warehouse_name"`
	Present       *integer `json:"present"`
	Reserved      *integer `json:"reserved"`
	FreeStock     *integer `json:"free_stock"`
}
type stockResponse struct {
	Products *[]stock `json:"products"`
	Cursor   string   `json:"cursor"`
	HasNext  *bool    `json:"has_next"`
}

func nextCursor(hasNext *bool, cursor string, seen map[string]bool) (bool, error) {
	if hasNext == nil {
		return false, errors.New("missing pagination status")
	}
	if !*hasNext {
		return false, nil
	}
	if cursor == "" || seen[cursor] {
		return false, errors.New("missing or repeated cursor")
	}
	seen[cursor] = true
	return true, nil
}
func (c *Client) Read(ctx context.Context) (domain.OzonImport, error) {
	b, _ := ctx.Value(budgetKey{}).(*budget)
	if b != nil {
		if err := b.report(ctx, "READING_PRODUCTS", true); err != nil {
			return domain.OzonImport{}, err
		}
	}
	snap := domain.OzonImport{Account: c.id, StartedAt: time.Now().UTC()}
	productIDs := map[string]bool{}
	offers := map[string]string{}
	for _, visibility := range []string{"ALL", "ARCHIVED"} {
		cursor := ""
		seen := map[string]bool{}
		var total int64 = -1
		count := 0
		for {
			var response productResponse
			body := map[string]any{"filter": map[string]any{"visibility": visibility}, "last_id": cursor, "limit": c.pageSize}
			if err := c.post(ctx, productPath, body, &response); err != nil {
				return domain.OzonImport{}, fmt.Errorf("read product list: %w", err)
			}
			if response.Result == nil || response.Result.Items == nil || response.Result.Total == nil {
				return domain.OzonImport{}, errors.New("incomplete product page")
			}
			r := response.Result
			if int64(*r.Total) < 0 || int64(*r.Total) > maxRows {
				return domain.OzonImport{}, errors.New("invalid product total")
			}
			if total < 0 {
				total = int64(*r.Total)
			} else if total != int64(*r.Total) {
				return domain.OzonImport{}, errors.New("product count changed during pagination")
			}
			for _, p := range *r.Items {
				if b != nil {
					if err := b.add("entities", 1); err != nil {
						return domain.OzonImport{}, err
					}
					if err := b.add("products", 1); err != nil {
						return domain.OzonImport{}, err
					}
				}
				pid := id(p.ProductID)
				if pid == "" || p.OfferID == "" {
					return domain.OzonImport{}, errors.New("product missing identifier")
				}
				if productIDs[pid] || offers[p.OfferID] != "" {
					return domain.OzonImport{}, errors.New("duplicate product or offer identifier")
				}
				productIDs[pid] = true
				offers[p.OfferID] = pid
				snap.Products = append(snap.Products, domain.OzonProduct{ProductID: pid, OfferID: p.OfferID, SKU: id(p.SKU), Archived: p.Archived})
			}
			count += len(*r.Items)
			if int64(count) > total || len(snap.Products) > maxRows {
				return domain.OzonImport{}, errors.New("inconsistent product count")
			}
			if int64(count) == total {
				if b != nil {
					b.counts.CompletedPages++
					if err := b.report(ctx, "READING_PRODUCTS", false); err != nil {
						return domain.OzonImport{}, err
					}
				}
				break
			}
			if len(*r.Items) == 0 || r.LastID == "" || seen[r.LastID] {
				return domain.OzonImport{}, errors.New("incomplete product pagination")
			}
			seen[r.LastID] = true
			cursor = r.LastID
			if b != nil {
				b.counts.CompletedPages++
				if err := b.report(ctx, "READING_PRODUCTS", false); err != nil {
					return domain.OzonImport{}, err
				}
			}
		}
	}
	if c.includeProductNames {
		if err := c.readProductNames(ctx, snap.Products); err != nil {
			return domain.OzonImport{}, fmt.Errorf("read product names: %w", err)
		}
	}
	if b != nil {
		if err := b.report(ctx, "READING_WAREHOUSES", true); err != nil {
			return domain.OzonImport{}, err
		}
	}
	cursors := map[string]bool{}
	warehouseSeen := map[string]bool{}
	cursor := ""
	for {
		var response warehouseResponse
		if err := c.post(ctx, warehousePath, map[string]any{"limit": c.pageSize, "cursor": cursor}, &response); err != nil {
			return domain.OzonImport{}, fmt.Errorf("read warehouses: %w", err)
		}
		if response.Warehouses == nil {
			return domain.OzonImport{}, errors.New("missing warehouses")
		}
		for _, w := range *response.Warehouses {
			if b != nil {
				if err := b.add("entities", 1); err != nil {
					return domain.OzonImport{}, err
				}
				if err := b.add("warehouses", 1); err != nil {
					return domain.OzonImport{}, err
				}
			}
			wid := id(w.ID)
			if wid == "" || warehouseSeen[wid] {
				return domain.OzonImport{}, errors.New("missing or duplicate warehouse identifier")
			}
			warehouseSeen[wid] = true
			snap.Warehouses = append(snap.Warehouses, domain.OzonWarehouse{ID: wid, Name: w.Name, Status: w.Status, WarehouseType: w.WarehouseType, IsRFBS: w.IsRFBS})
			if len(snap.Warehouses) > maxRows {
				return domain.OzonImport{}, errors.New("too many warehouses")
			}
		}
		more, err := nextCursor(response.HasNext, response.Cursor, cursors)
		if err != nil {
			return domain.OzonImport{}, err
		}
		if !more {
			if b != nil {
				b.counts.CompletedPages++
				if err := b.report(ctx, "READING_WAREHOUSES", false); err != nil {
					return domain.OzonImport{}, err
				}
			}
			break
		}
		if len(*response.Warehouses) == 0 {
			return domain.OzonImport{}, errors.New("empty warehouse continuation page")
		}
		cursor = response.Cursor
		if b != nil {
			b.counts.CompletedPages++
			if err := b.report(ctx, "READING_WAREHOUSES", false); err != nil {
				return domain.OzonImport{}, err
			}
		}
	}
	if b != nil {
		if err := b.report(ctx, "READING_STOCK", true); err != nil {
			return domain.OzonImport{}, err
		}
	}
	stockSeen := map[string]bool{}
	for start := 0; start < len(snap.Products); start += 100 {
		end := start + 100
		if end > len(snap.Products) {
			end = len(snap.Products)
		}
		batch := make([]string, 0, end-start)
		batchIDs := map[string]string{}
		for _, p := range snap.Products[start:end] {
			batch = append(batch, p.OfferID)
			batchIDs[p.OfferID] = p.ProductID
		}
		cursor = ""
		cursors = map[string]bool{}
		for {
			var response stockResponse
			if err := c.post(ctx, stockPath, map[string]any{"offer_id": batch, "limit": c.pageSize, "cursor": cursor}, &response); err != nil {
				return domain.OzonImport{}, fmt.Errorf("read FBS stock: %w", err)
			}
			if response.Products == nil {
				return domain.OzonImport{}, errors.New("missing stock products")
			}
			for _, p := range *response.Products {
				if b != nil {
					if err := b.add("observations", 1); err != nil {
						return domain.OzonImport{}, err
					}
				}
				pid, wid := id(p.ProductID), id(p.WarehouseID)
				if pid == "" || wid == "" || p.OfferID == "" || batchIDs[p.OfferID] != pid || p.Present == nil || p.Reserved == nil || p.FreeStock == nil {
					return domain.OzonImport{}, errors.New("incomplete or inconsistent stock row")
				}
				k := pid + ":" + wid
				if stockSeen[k] {
					return domain.OzonImport{}, errors.New("duplicate product and warehouse stock")
				}
				stockSeen[k] = true
				snap.Stocks = append(snap.Stocks, domain.OzonStock{ProductID: pid, OfferID: p.OfferID, SKU: id(p.SKU), WarehouseID: wid, WarehouseName: p.WarehouseName, Present: int64(*p.Present), Reserved: int64(*p.Reserved), FreeStock: int64(*p.FreeStock)})
				if len(snap.Stocks) > maxRows {
					return domain.OzonImport{}, errors.New("too many stock rows")
				}
			}
			more, err := nextCursor(response.HasNext, response.Cursor, cursors)
			if err != nil {
				return domain.OzonImport{}, err
			}
			if !more {
				if b != nil {
					b.counts.CompletedPages++
					if err := b.report(ctx, "READING_STOCK", false); err != nil {
						return domain.OzonImport{}, err
					}
				}
				break
			}
			if len(*response.Products) == 0 {
				return domain.OzonImport{}, errors.New("empty stock continuation page")
			}
			cursor = response.Cursor
			if b != nil {
				b.counts.CompletedPages++
				if err := b.report(ctx, "READING_STOCK", false); err != nil {
					return domain.OzonImport{}, err
				}
			}
		}
	}
	snap.FinishedAt = time.Now().UTC()
	return snap, nil
}
