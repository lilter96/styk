package providercall

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// NotDispatchedError proves admission or final authorization failed before HTTP.
type NotDispatchedError struct{ Cause error }

func (e *NotDispatchedError) Error() string { return "provider request was not dispatched" }
func (e *NotDispatchedError) Unwrap() error { return e.Cause }
func IsNotDispatched(err error) bool        { var local *NotDispatchedError; return errors.As(err, &local) }

type guardKey struct{}
type targetKey struct{}
type labelDownloadKey struct{}

// WithLabelDownload keeps signed file paths out of durable admission records.
// It is only used after a scoped provider task authorizes the transient URL.
func WithLabelDownload(ctx context.Context) context.Context {
	return context.WithValue(ctx, labelDownloadKey{}, true)
}

// WithGuard performs the final database authorization after rate-limit waiting.
// It must not dispatch a request itself.
func WithGuard(ctx context.Context, guard func(context.Context) error) context.Context {
	return context.WithValue(ctx, guardKey{}, guard)
}
func WithTarget(ctx context.Context, target string) context.Context {
	return context.WithValue(ctx, targetKey{}, target)
}

type Transport struct {
	Base    http.RoundTripper
	Gate    Gate
	Scope   Scope
	Timeout time.Duration
}

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.Gate == nil || t.Base == nil {
		return nil, &NotDispatchedError{Cause: ErrInvalid}
	}
	if err := enterDispatchEvidence(req.Context()); err != nil {
		return nil, &NotDispatchedError{Cause: err}
	}
	operation := operationKey(req.URL.Path)
	if label, _ := req.Context().Value(labelDownloadKey{}).(bool); label && req.Method == http.MethodGet {
		operation = "orders/label/pdf"
	}
	target, _ := req.Context().Value(targetKey{}).(string)
	permit, err := t.Gate.Begin(req.Context(), Request{Scope: t.Scope, Method: req.Method, Operation: operation, Timeout: t.Timeout, IsWrite: isMutation(req), TargetKey: target, Endpoint: req.URL.Scheme + "://" + req.URL.Host})
	if err != nil {
		return nil, &NotDispatchedError{Cause: err}
	}
	defer func() {
		if cleanupErr := permit.Abort(req.Context()); cleanupErr != nil {
			slog.Warn("provider admission cleanup pending")
		}
	}()
	if err = startProviderDispatch(req.Context(), permit); err != nil {
		return nil, &NotDispatchedError{Cause: err}
	}
	response, err := t.Base.RoundTrip(req)
	// Complete admission bookkeeping before diagnostic I/O. An observer failure
	// must not conceal an already observed response from the durable coordinator.
	if response != nil {
		if finishErr := permit.Finish(req.Context(), response.StatusCode, response.Header); finishErr != nil {
			response.Header.Set("X-STYK-Coordination-Pending", "true")
		}
	}
	if evidenceErr := observeProviderRead(req, t.Scope, response, err); evidenceErr != nil {
		if response != nil {
			if closeErr := response.Body.Close(); closeErr != nil {
				evidenceErr = errors.Join(evidenceErr, closeErr)
			}
		}
		return nil, evidenceErr
	}
	if response == nil {
		return nil, err
	}
	return response, err
}

func startProviderDispatch(ctx context.Context, permit *Permit) error {
	if err := permit.Start(ctx); err != nil {
		return err
	}
	if guard, ok := ctx.Value(guardKey{}).(func(context.Context) error); ok {
		if err := guard(ctx); err != nil {
			return err
		}
	}
	return startDispatchEvidence(ctx)
}

func operationKey(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i, part := range parts {
		if len(part) == 36 || len(part) > 40 {
			parts[i] = "id"
		}
	}
	return strings.Join(parts, "/")
}
func isMutation(req *http.Request) bool {
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		return false
	}
	path := req.URL.Path
	if req.Method != http.MethodPost {
		return true
	}
	if strings.HasPrefix(path, "/api/remap/") || strings.HasPrefix(path, "/entity/") {
		return true
	}
	if strings.HasSuffix(path, "/list") || strings.HasSuffix(path, "/get") || strings.HasSuffix(path, "/info") || strings.HasPrefix(path, "/v1/description-category/") {
		return false
	}
	switch path {
	case "/v1/roles", "/v2/finance/realization", "/v1/finance/accrual/types", "/v1/finance/accrual/by-day", "/v1/finance/accrual/postings", "/v1/carriage/get", "/v2/carriage/delivery/list", "/v2/posting/fbs/act/get-postings", "/v5/fbs/posting/product/exemplar/status", "/v2/posting/fbs/product/country/list", "/v2/product/info/stocks-by-warehouse/fbs", "/v3/products/info/attributes", "/v1/posting/fbs/cancel-reason":
		return false
	}
	return true
}

func Bind(client *http.Client, gate Gate, scope Scope, timeout time.Duration) (*http.Client, error) {
	if client == nil || gate == nil || timeout <= 0 {
		return nil, errors.New("invalid coordinated provider client")
	}
	copyClient := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	if _, bound := base.(Transport); bound {
		return nil, errors.New("provider client already coordinated")
	}
	copyClient.Transport = Transport{Base: base, Gate: gate, Scope: scope, Timeout: timeout}
	return &copyClient, nil
}
