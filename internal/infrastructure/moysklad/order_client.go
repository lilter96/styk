package moysklad

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/ordercontract"
	"github.com/lilter96/styk/internal/orders"
	"github.com/lilter96/styk/internal/providercall"
)

const orderResponseLimit = 4 << 20

var (
	ErrOrderIntent      = errors.New("MoySklad order intent is not authorized")
	ErrOrderIdentity    = errors.New("MoySklad order scope or identity mismatch")
	ErrOrderSchema      = errors.New("MoySklad order response is not supported")
	ErrOrderUnknown     = errors.New("MoySklad order write outcome is unknown")
	ErrOrderCorrelation = fmt.Errorf("MoySklad order correlation is not yet established: %w", orders.ErrNotFound)
)

// OrderClient projects exactly one posting into its native customerorder. A
// binding admits one mutation attempt; syncId is correlation, not idempotency.
type OrderClient struct {
	client    *Client
	scope     orders.Scope
	manifest  ordercontract.Manifest
	mu        sync.Mutex
	intent    []byte
	used      bool
	authorize func(context.Context) (bool, error)
}

var _ orders.Provider = (*OrderClient)(nil)
var _ orders.TargetReader = (*OrderClient)(nil)

func NewOrderClient(client *Client, scope orders.Scope) (*OrderClient, error) {
	if !catalogHostAllowed(client) || scope.Validate() != nil || !orderUUID.MatchString(scope.MoyskladAccount) {
		return nil, ErrOrderIdentity
	}
	if !orderTransportScope(client.http.Transport, scope) {
		return nil, ErrOrderIdentity
	}
	if !orderSupportedTransport(client.http.Transport) {
		return nil, ErrOrderIdentity
	}
	manifest, err := ordercontract.ContractManifest()
	if err != nil {
		return nil, ErrOrderSchema
	}
	return &OrderClient{client: client, scope: scope, manifest: manifest}, nil
}

func orderTransportScope(transport http.RoundTripper, scope orders.Scope) bool {
	bound, ok := transport.(providercall.Transport)
	return !ok || (bound.Scope.Tenant == scope.Tenant && bound.Scope.Provider == "moysklad" && bound.Scope.Account == scope.MoyskladAccount && bound.Scope.PendingID == "")
}

func orderSupportedTransport(transport http.RoundTripper) bool {
	if transport == nil {
		return true
	}
	if coordinated, ok := transport.(providercall.Transport); ok {
		return coordinated.Gate != nil && orderSupportedTransport(coordinated.Base)
	}
	_, supported := transport.(*http.Transport)
	return supported
}

func (c *OrderClient) scopeAllowed(scope orders.Scope) bool {
	return c != nil && scope == c.scope && scope.Validate() == nil
}

func (c *OrderClient) boundedContext(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	httpClient, closeHTTP := orderHTTP(c.client.http)
	limits := domain.ImportLimits{Requests: 128, Attempts: 256, Pages: 128, ResponseBytes: orderResponseLimit}
	return context.WithValue(ctx, budgetKey{}, &budget{http: httpClient, limits: limits}), func() {
		cancel()
		closeHTTP()
	}
}

func (c *OrderClient) verifyAccount(ctx context.Context) error {
	raw, err := c.client.get(ctx, "context/employee", nil)
	if err != nil {
		return err
	}
	var employee struct {
		AccountID string `json:"accountId"`
	}
	if json.Unmarshal(raw, &employee) != nil || employee.AccountID != c.scope.MoyskladAccount {
		return ErrOrderIdentity
	}
	return nil
}

func orderIntentBytes(command orders.Command) ([]byte, error) {
	// Include transient PII in the one-use digest even though it is deliberately
	// excluded from normal command JSON and durable command fingerprints.
	raw, err := json.Marshal(struct {
		Command orders.Command   `json:"command"`
		PII     *orders.Customer `json:"pii"`
	}{command, command.PII})
	if err != nil {
		return nil, ErrOrderIntent
	}
	digest := sha256.Sum256(raw)
	return digest[:], nil
}

func (c *OrderClient) BindOrderIntent(command orders.Command, authorize func(context.Context) (bool, error)) error {
	if !c.scopeAllowed(command.Scope) || authorize == nil || command.Validate() != nil {
		return ErrOrderIntent
	}
	if _, err := c.orderPayload(command); err != nil {
		return err
	}
	digest, err := orderIntentBytes(command)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.intent != nil {
		return ErrOrderIntent
	}
	c.intent, c.authorize = digest, authorize
	return nil
}

func (c *OrderClient) consumeIntent(command orders.Command) (func(context.Context) (bool, error), error) {
	digest, err := orderIntentBytes(command)
	if err != nil || !c.scopeAllowed(command.Scope) {
		return nil, ErrOrderIntent
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.used || c.authorize == nil || !bytes.Equal(digest, c.intent) {
		return nil, ErrOrderIntent
	}
	c.used = true
	return c.authorize, nil
}

func (c *OrderClient) reference(path, kind, id string) catalogReference {
	var reference catalogReference
	reference.Meta.Href = c.client.base.ResolveReference(&url.URL{Path: path + "/" + id}).String()
	reference.Meta.Type, reference.Meta.MediaType = kind, "application/json"
	return reference
}

func (c *OrderClient) referenceID(reference catalogReference, path, kind string) (string, error) {
	u, err := url.Parse(reference.Meta.Href)
	if err != nil || u.User != nil || u.Scheme != c.client.base.Scheme || u.Host != c.client.base.Host || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || reference.Meta.Type != kind {
		return "", ErrOrderIdentity
	}
	prefix := c.client.base.Path + path + "/"
	id := strings.TrimPrefix(u.Path, prefix)
	if !strings.HasPrefix(u.Path, prefix) || !orderUUID.MatchString(id) {
		return "", ErrOrderIdentity
	}
	return id, nil
}

func (c *OrderClient) ReadOrderPosting(context.Context, orders.Scope, string) (orders.Posting, error) {
	return orders.Posting{}, orders.ErrBlocked
}
