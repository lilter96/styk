package ozon

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

var (
	ErrCatalogWritesDisabled = errors.New("ozon catalog writes are disabled")
	ErrCatalogWriteUnknown   = errors.New("ozon catalog write outcome is unknown")
	ErrCatalogContract       = errors.New("incomplete or inconsistent Ozon catalog response")
	ErrCatalogUnsupported    = errors.New("ozon catalog operation is unsupported")
)

// CatalogClient keeps catalog permissions separate from the stock writer. An
// ordinary reader can never opt in to real-host catalog writes.
type CatalogClient struct {
	read       *Client
	scope      catalog.Scope
	synthetic  bool
	controlled bool
	mu         sync.Mutex
	intent     []byte
	authorize  func(context.Context) (bool, error)
	used       bool
}

func NewCatalogClient(options Options, scope catalog.Scope) (*CatalogClient, error) {
	if scope.Validate() != nil || options.ClientID != scope.OzonAccount || options.Timeout > 30*time.Second {
		return nil, catalog.ErrInvalidScope
	}
	client, err := New(options)
	if err != nil {
		return nil, err
	}
	return NewCatalogReaderFromClient(client, scope)
}

func NewCatalogReaderFromClient(client *Client, scope catalog.Scope) (*CatalogClient, error) {
	if client == nil || scope.Validate() != nil || client.id != scope.OzonAccount || client.timeout > 30*time.Second {
		return nil, catalog.ErrInvalidScope
	}
	return &CatalogClient{read: client, scope: scope}, nil
}

func NewControlledTestCatalogClient(scope catalog.Scope, key string) (*CatalogClient, error) {
	client, err := New(Options{BaseURL: BaseURL, ClientID: scope.OzonAccount, APIKey: key, Timeout: 20 * time.Second, Interval: time.Second, Backoff: time.Second, MaxRetryAfter: time.Second, Attempts: 1, PageSize: 100})
	if err != nil {
		return nil, err
	}
	return NewControlledTestCatalogClientFromClient(client, scope)
}

func NewControlledTestCatalogClientFromClient(client *Client, scope catalog.Scope) (*CatalogClient, error) {
	reader, err := NewCatalogReaderFromClient(client, scope)
	if err != nil {
		return nil, err
	}
	reader.synthetic = client.allowSyntheticStockWrites
	reader.controlled = client.base.String() == BaseURL && catalogTestEnabled(scope)
	if !reader.writeEnabled() {
		return nil, ErrCatalogWritesDisabled
	}
	copyClient, err := New(Options{BaseURL: client.base.String(), ClientID: client.id, APIKey: client.key, Timeout: client.timeout, Interval: client.interval, Backoff: client.backoff, MaxRetryAfter: client.maxRetryAfter, Attempts: 1, PageSize: client.pageSize})
	if err != nil {
		return nil, err
	}
	if err := hardenCatalogTransport(copyClient); err != nil {
		return nil, err
	}
	reader.read = copyClient
	return reader, nil
}

func hardenCatalogTransport(client *Client) error {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return ErrCatalogWritesDisabled
	}
	transport := base.Clone()
	transport.Proxy, transport.DisableKeepAlives, transport.ForceAttemptHTTP2 = nil, true, false
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	client.http.Transport = transport
	return nil
}

func catalogTestEnabled(scope catalog.Scope) bool {
	stockScope := domain.StockSyncScope{Tenant: scope.Tenant, MoyskladAccount: scope.MoyskladAccount, OzonAccount: scope.OzonAccount}
	return os.Getenv("STYK_TEST_ACCOUNT_CATALOG") == "true" && stockScope == testpublication.Scope()
}

// NewSyntheticCatalogClient accepts only an explicit IP loopback HTTP server
// and the standard transport. It cannot enable a real provider host.
func NewSyntheticCatalogClient(options Options, scope catalog.Scope) (*CatalogClient, error) {
	options.AllowSyntheticStockWrites = true
	client, err := NewCatalogClient(options, scope)
	if err != nil {
		return nil, err
	}
	transport, ok := client.read.http.Transport.(*http.Transport)
	if !ok {
		return nil, ErrCatalogWritesDisabled
	}
	transport.Proxy = nil
	client.synthetic = true
	return client, nil
}

func (c *CatalogClient) writeEnabled() bool {
	if c.synthetic {
		return true
	}
	return c.controlled && catalogTestEnabled(c.scope)
}

// BindCatalogIntent freezes the entire journalled command. The callback must
// revalidate its durable claim; it is consumed once immediately before dispatch.
func (c *CatalogClient) BindCatalogIntent(command catalog.Command, authorize func(context.Context) (bool, error)) error {
	if !c.writeEnabled() || authorize == nil || command.Scope != c.scope || command.Target.Account.ID != c.read.id || catalog.ValidateCommand(command) != nil {
		return ErrCatalogWritesDisabled
	}
	data, err := json.Marshal(command)
	if err != nil {
		return catalog.ErrInvalidCommand
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.intent != nil {
		return ErrCatalogWritesDisabled
	}
	c.intent, c.authorize = data, authorize
	return nil
}

func (c *CatalogClient) consumeCatalogIntent(command catalog.Command) (func(context.Context) (bool, error), error) {
	data, err := json.Marshal(command)
	if err != nil {
		return nil, ErrCatalogWritesDisabled
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.writeEnabled() || c.used || c.authorize == nil || !bytes.Equal(data, c.intent) {
		return nil, ErrCatalogWritesDisabled
	}
	c.used = true
	return c.authorize, nil
}

func (c *CatalogClient) frozenCatalogCommand(command catalog.Command) (catalog.Command, error) {
	data, err := json.Marshal(command)
	if err != nil {
		return catalog.Command{}, catalog.ErrInvalidCommand
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.writeEnabled() || c.used || c.authorize == nil || !bytes.Equal(data, c.intent) {
		return catalog.Command{}, ErrCatalogWritesDisabled
	}
	var frozen catalog.Command
	if err := json.Unmarshal(c.intent, &frozen); err != nil {
		return catalog.Command{}, catalog.ErrInvalidCommand
	}
	return frozen, nil
}

func catalogBudgetContext(ctx context.Context) context.Context {
	if existing, _ := ctx.Value(budgetKey{}).(*budget); existing != nil {
		return ctx
	}
	limits := Limits{Requests: 110, Attempts: 110, Pages: 110, Entities: 100, Products: 100, Warehouses: 1, Postings: 1, Observations: 100, ResponseBytes: 4 << 20, Duration: 2 * time.Minute}
	return context.WithValue(ctx, budgetKey{}, &budget{limits: limits})
}

func (c *CatalogClient) catalogPost(ctx context.Context, path string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return c.read.post(catalogBudgetContext(ctx), path, body, out)
}

func (c *CatalogClient) validateRead(request catalog.ReadRequest) error {
	account := catalog.Account{Provider: catalog.ProviderOzon, ID: c.read.id}
	if request.Scope != c.scope || request.Account != account || request.Limit < 0 || request.Limit > 100 || request.Search != "" {
		return catalog.ErrInvalidScope
	}
	for _, identity := range request.Identities {
		if identity.Account != account || (identity.ID == "" && identity.OfferID == "") {
			return catalog.ErrInvalidIdentity
		}
	}
	return nil
}
