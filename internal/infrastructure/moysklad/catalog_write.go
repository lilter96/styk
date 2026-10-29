package moysklad

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lilter96/styk/internal/catalog"
)

// BindCatalogIntent freezes one durable command and the worker's final database
// authorization. Even a loopback write requires this one-use binding.
func (c *CatalogClient) BindCatalogIntent(cmd catalog.Command, authorize func(context.Context) (bool, error)) error {
	if !c.scopeAllowed(cmd.Scope) || authorize == nil {
		return ErrCatalogWritesDisabled
	}
	if _, err := c.catalogPayload(cmd); err != nil {
		return err
	}
	frozen, err := json.Marshal(cmd)
	if err != nil {
		return ErrCatalogInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.intent != nil {
		return ErrCatalogWritesDisabled
	}
	c.intent, c.auth = frozen, authorize
	return nil
}

func (c *CatalogClient) consumeCatalogIntent(cmd catalog.Command) (func(context.Context) (bool, error), error) {
	frozen, err := json.Marshal(cmd)
	if err != nil || !c.scopeAllowed(cmd.Scope) {
		return nil, ErrCatalogWritesDisabled
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.intent == nil || c.used || c.auth == nil || !bytes.Equal(frozen, c.intent) {
		return nil, ErrCatalogWritesDisabled
	}
	c.used = true
	return c.auth, nil
}

// WriteCatalog makes at most one external mutation attempt. A preflight reads
// the target revision or unique durable create marker. An uncertain result is
// UNKNOWN and never an instruction to retry; use ReconcileCatalog explicitly.
func (c *CatalogClient) WriteCatalog(ctx context.Context, cmd catalog.Command) (catalog.Receipt, error) {
	auth, err := c.consumeCatalogIntent(cmd)
	if err != nil {
		return catalogRejected(cmd), err
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	body, err := c.catalogPayload(cmd)
	if err != nil {
		return catalogRejected(cmd), err
	}
	if err := c.verifyAccount(ctx, cmd.Target.Account); err != nil {
		return catalogRejected(cmd), err
	}
	prior, err := c.catalogPreflight(ctx, cmd, body)
	if err != nil {
		if errors.Is(err, ErrCatalogWriteUnknown) {
			return catalogUnknown(cmd, 0)
		}
		return catalogRejected(cmd), err
	}
	if prior != nil {
		return *prior, nil
	}
	return c.dispatchCatalog(ctx, cmd, body, auth)
}

func catalogRejected(cmd catalog.Command) catalog.Receipt {
	return catalog.Receipt{CommandID: cmd.ID, State: catalog.ReceiptRejected}
}

func catalogApplied(cmd catalog.Command, id string, status int) catalog.Receipt {
	return catalog.Receipt{CommandID: cmd.ID, State: catalog.ReceiptApplied, ExternalID: id, HTTPStatus: status}
}

func catalogUnknown(cmd catalog.Command, status int) (catalog.Receipt, error) {
	return catalog.Receipt{CommandID: cmd.ID, State: catalog.ReceiptUnknown, HTTPStatus: status}, ErrCatalogWriteUnknown
}

func (c *CatalogClient) catalogPreflight(ctx context.Context, cmd catalog.Command, body map[string]any) (*catalog.Receipt, error) {
	if cmd.Action == catalog.ActionCreate {
		raw, err := c.findCatalogMarker(ctx, cmd)
		if err != nil || raw == nil {
			return nil, err
		}
		return c.catalogVerifiedReceipt(cmd, body, raw, 0)
	}
	raw, err := c.client.get(ctx, "entity/"+cmd.Target.EntityType+"/"+cmd.Target.ID, nil)
	if err != nil {
		return nil, err
	}
	p, err := c.decodeCatalogProduct(raw, cmd.Target.Account, cmd.Target.EntityType, time.Now().UTC())
	if err != nil || p.Identity != cmd.Target {
		return nil, ErrCatalogIdentity
	}
	if p.Revision != cmd.ExpectedTargetRevision {
		return nil, ErrCatalogConflict
	}
	if catalogBodyMatches(raw, body) {
		r := catalogApplied(cmd, p.Identity.ID, 0)
		return &r, nil
	}
	return nil, nil
}

func (c *CatalogClient) findCatalogMarker(ctx context.Context, cmd catalog.Command) (json.RawMessage, error) {
	q := url.Values{"limit": {"2"}, "offset": {"0"}, "filter": {"externalCode=" + cmd.CreateExternalCode + ";archived=true;archived=false"}}
	raw, err := c.client.get(ctx, "entity/"+cmd.Target.EntityType, q)
	if err != nil {
		return nil, err
	}
	rows, total, err := catalogPage(raw, q)
	if err != nil {
		return nil, err
	}
	if total > 1 || len(rows) > 1 {
		return nil, ErrCatalogWriteUnknown
	}
	if len(rows) == 0 {
		return nil, nil
	}
	var marker struct {
		ExternalCode string `json:"externalCode"`
	}
	if json.Unmarshal(rows[0], &marker) != nil || marker.ExternalCode != cmd.CreateExternalCode {
		return nil, ErrCatalogIdentity
	}
	return rows[0], nil
}

func (c *CatalogClient) dispatchCatalog(ctx context.Context, cmd catalog.Command, body map[string]any, authorize func(context.Context) (bool, error)) (catalog.Receipt, error) {
	req, err := c.catalogWriteRequest(ctx, cmd, body)
	if err != nil {
		return catalogRejected(cmd), err
	}
	if err := c.client.wait(ctx); err != nil {
		return catalogRejected(cmd), err
	}
	requestContext, err := c.client.authorizeRequest(ctx, func(check context.Context) error { return c.authorizeCatalog(check, cmd.Scope, authorize) })
	if err != nil {
		return catalogRejected(cmd), err
	}
	req = req.WithContext(requestContext)
	b, _ := ctx.Value(budgetKey{}).(*budget)
	resp, err := b.http.Do(req)
	observeCatalogRequest(ctx, req.Method, strings.TrimPrefix(req.URL.Path, c.client.base.Path), resp, err)
	if err != nil {
		return catalogUnknown(cmd, 0)
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, catalogResponseLimit+1))
	closeErr := resp.Body.Close()
	c.client.deferRequests(rateDelay(resp.Header, resp.StatusCode, time.Now()))
	if readErr != nil || closeErr != nil || len(raw) > catalogResponseLimit || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return catalogUnknown(cmd, resp.StatusCode)
	}
	r, err := c.catalogVerifiedReceipt(cmd, body, raw, resp.StatusCode)
	if err != nil {
		return catalogUnknown(cmd, resp.StatusCode)
	}
	return *r, nil
}

func (c *CatalogClient) catalogWriteRequest(ctx context.Context, cmd catalog.Command, body map[string]any) (*http.Request, error) {
	payload, err := json.Marshal(body)
	if err != nil || len(payload) > catalogResponseLimit {
		return nil, ErrCatalogInvalid
	}
	method, path := http.MethodPost, "entity/"+cmd.Target.EntityType
	if cmd.Action != catalog.ActionCreate {
		method, path = http.MethodPut, path+"/"+cmd.Target.ID
	}
	u := c.client.base.ResolveReference(&url.URL{Path: path})
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(payload))
	if err != nil {
		return nil, ErrCatalogInvalid
	}
	// No replayable body is exposed to net/http. The transport uses fresh
	// connections, and this path never calls the read client's retry loop.
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+c.client.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json;charset=utf-8")
	return req, nil
}

func (c *CatalogClient) authorizeCatalog(ctx context.Context, scope catalog.Scope, authorize func(context.Context) (bool, error)) error {
	if !c.scopeAllowed(scope) || authorize == nil {
		return ErrCatalogWritesDisabled
	}
	allowed, err := authorize(ctx)
	if err != nil || !allowed || !c.scopeAllowed(scope) {
		return ErrCatalogWritesDisabled
	}
	return ctx.Err()
}

func (c *CatalogClient) catalogVerifiedReceipt(cmd catalog.Command, body map[string]any, raw []byte, status int) (*catalog.Receipt, error) {
	p, err := c.decodeCatalogProduct(raw, cmd.Target.Account, cmd.Target.EntityType, time.Now().UTC())
	if err != nil || !catalogBodyMatches(raw, body) {
		return nil, ErrCatalogWriteUnknown
	}
	if cmd.Action != catalog.ActionCreate && p.Identity != cmd.Target {
		return nil, ErrCatalogIdentity
	}
	if cmd.Parent != nil && (p.Parent == nil || *p.Parent != *cmd.Parent) {
		return nil, ErrCatalogIdentity
	}
	r := catalogApplied(cmd, p.Identity.ID, status)
	return &r, nil
}

// ReconcileCatalog reads only. An absent marker or nonmatching observation
// remains UNKNOWN because a delayed provider effect may still exist.
func (c *CatalogClient) ReconcileCatalog(ctx context.Context, cmd catalog.Command) (catalog.Receipt, error) {
	body, err := c.catalogPayload(cmd)
	if err != nil {
		return catalogRejected(cmd), err
	}
	ctx, finish := c.boundedContext(ctx)
	defer finish()
	if err := c.verifyAccount(ctx, cmd.Target.Account); err != nil {
		return catalogUnknown(cmd, 0)
	}
	raw, err := c.catalogReconciliationRead(ctx, cmd)
	if err != nil || len(raw) == 0 {
		return catalogUnknown(cmd, 0)
	}
	r, err := c.catalogVerifiedReceipt(cmd, body, raw, 0)
	if err != nil {
		return catalogUnknown(cmd, 0)
	}
	return *r, nil
}

func (c *CatalogClient) catalogReconciliationRead(ctx context.Context, cmd catalog.Command) ([]byte, error) {
	if cmd.Action == catalog.ActionCreate {
		return c.findCatalogMarker(ctx, cmd)
	}
	return c.client.get(ctx, "entity/"+cmd.Target.EntityType+"/"+cmd.Target.ID, nil)
}

func (c *CatalogClient) PollCatalog(ctx context.Context, cmd catalog.Command, receipt catalog.Receipt) (catalog.Receipt, error) {
	if receipt.CommandID != cmd.ID {
		return catalogRejected(cmd), ErrCatalogInvalid
	}
	return c.ReconcileCatalog(ctx, cmd)
}
