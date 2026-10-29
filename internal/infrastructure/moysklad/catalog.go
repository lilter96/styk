package moysklad

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lilter96/styk/internal/catalog"
	"github.com/lilter96/styk/internal/domain"
	"github.com/lilter96/styk/internal/testpublication"
)

const catalogResponseLimit = 4 << 20

var catalogEntityPath = regexp.MustCompile(`^entity/(product|variant|productfolder|uom)/[0-9a-fA-F-]{36}$`)

var (
	ErrCatalogWritesDisabled = errors.New("moysklad catalog writes are disabled")
	ErrCatalogWriteUnknown   = errors.New("moysklad catalog write outcome is unknown")
	ErrCatalogConflict       = errors.New("moysklad catalog target revision changed")
	ErrCatalogInvalid        = errors.New("invalid moysklad catalog intent")
	ErrCatalogIdentity       = errors.New("moysklad catalog account or identity mismatch")
)

// CatalogClient has a separate catalog-only write gate. It cannot publish stock
// or call document, financial, inventory movement, image-upload or delete APIs.
type CatalogClient struct {
	client *Client
	scope  *catalog.Scope
	mu     sync.Mutex
	intent []byte
	used   bool
	auth   func(context.Context) (bool, error)
}

func allowedCatalogReadPath(path string) bool {
	switch path {
	case "entity/productfolder", "entity/uom", "entity/product/metadata", "entity/variant/metadata", "entity/product/metadata/attributes", "context/companysettings/pricetype":
		return true
	}
	if !catalogEntityPath.MatchString(path) {
		return false
	}
	return orderUUID.MatchString(path[strings.LastIndexByte(path, '/')+1:])
}

func catalogLoopback(u *url.URL) bool {
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback() && u.Scheme == "http"
}

func catalogHostAllowed(c *Client) bool {
	return c != nil && (c.base.String() == BaseURL || catalogLoopback(c.base))
}

func NewCatalogClient(c *Client) (*CatalogClient, error) {
	if !catalogHostAllowed(c) {
		return nil, ErrCatalogIdentity
	}
	return &CatalogClient{client: c}, nil
}

// NewControlledTestCatalogClient admits the fixed owner-authorized account pair
// on the official host, or an explicit loopback simulator. The catalog switch
// is independent from the production stock publication hold.
func NewControlledTestCatalogClient(c *Client, scope catalog.Scope) (*CatalogClient, error) {
	x, err := NewCatalogClient(c)
	if err != nil {
		return nil, err
	}
	if !validCatalogScope(scope) || (!catalogLoopback(c.base) && !catalogTestEnabled(scope)) {
		return nil, ErrCatalogWritesDisabled
	}
	x.scope = &scope
	return x, nil
}

func validCatalogScope(s catalog.Scope) bool {
	return s.Validate() == nil && orderUUID.MatchString(s.MoyskladAccount)
}

func catalogTestEnabled(s catalog.Scope) bool {
	return os.Getenv("STYK_TEST_ACCOUNT_CATALOG") == "true" && s == (catalog.Scope{Tenant: testpublication.Tenant, MoyskladAccount: testpublication.MoyskladAccount, OzonAccount: testpublication.OzonAccount})
}

func (c *CatalogClient) scopeAllowed(s catalog.Scope) bool {
	return c.scope != nil && *c.scope == s && (catalogLoopback(c.client.base) || catalogTestEnabled(s))
}

func catalogAccount(scope catalog.Scope, account catalog.Account) error {
	if !validCatalogScope(scope) || account.Provider != catalog.ProviderMoySklad || account.ID != scope.MoyskladAccount {
		return ErrCatalogIdentity
	}
	return nil
}

func catalogIdentity(account catalog.Account, id catalog.Identity) error {
	if id.Account != account || !orderUUID.MatchString(id.ID) || (id.EntityType != "product" && id.EntityType != "variant") || id.OfferID != "" || id.SKU != "" {
		return ErrCatalogIdentity
	}
	return nil
}

func (c *CatalogClient) verifyAccount(ctx context.Context, account catalog.Account) error {
	id, err := c.client.Identity(ctx)
	if err != nil {
		return fmt.Errorf("verify catalog account: %w", err)
	}
	if id != account.ID {
		return ErrCatalogIdentity
	}
	return nil
}

func catalogHTTP(source *http.Client) (*http.Client, func()) {
	hc := *source
	transport := hc.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	tr, ok := transport.(*http.Transport)
	if !ok {
		return &hc, func() {}
	}
	clone := tr.Clone()
	clone.DisableKeepAlives = true
	hc.Transport = clone
	return &hc, clone.CloseIdleConnections
}

func (c *CatalogClient) boundedContext(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	hc, closeHTTP := catalogHTTP(c.client.http)
	b := &budget{http: hc, limits: domain.ImportLimits{Requests: 128, Attempts: 256, Pages: 128, ResponseBytes: catalogResponseLimit}}
	return context.WithValue(ctx, budgetKey{}, b), func() { cancel(); closeHTTP() }
}

func catalogDigest(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

type catalogReference struct {
	Meta struct {
		Href      string `json:"href"`
		Type      string `json:"type"`
		MediaType string `json:"mediaType,omitempty"`
	} `json:"meta"`
}

func (c *CatalogClient) referenceID(r catalogReference, path, kind string) (string, error) {
	u, err := url.Parse(r.Meta.Href)
	if err != nil || u.User != nil || u.Scheme != c.client.base.Scheme || u.Host != c.client.base.Host || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || r.Meta.Type != kind {
		return "", ErrCatalogIdentity
	}
	prefix := c.client.base.Path + path + "/"
	id := strings.TrimPrefix(u.Path, prefix)
	if !strings.HasPrefix(u.Path, prefix) || !orderUUID.MatchString(id) {
		return "", ErrCatalogIdentity
	}
	return id, nil
}

func (c *CatalogClient) reference(kind, id string) catalogReference {
	var r catalogReference
	r.Meta.Href = c.client.base.ResolveReference(&url.URL{Path: "entity/" + kind + "/" + id}).String()
	r.Meta.Type, r.Meta.MediaType = kind, "application/json"
	return r
}
