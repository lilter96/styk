package providercall

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type guardKey struct{}
type targetKey struct{}

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
		return nil, ErrInvalid
	}
	operation := operationKey(req.URL.Path)
	target, _ := req.Context().Value(targetKey{}).(string)
	permit, err := t.Gate.Begin(req.Context(), Request{Scope: t.Scope, Method: req.Method, Operation: operation, Timeout: t.Timeout, IsWrite: isMutation(req), TargetKey: target, Endpoint: req.URL.Scheme + "://" + req.URL.Host})
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := permit.Abort(req.Context()); cleanupErr != nil {
			slog.Warn("provider admission cleanup pending")
		}
	}()
	if err = permit.Start(req.Context()); err != nil {
		return nil, err
	}
	if guard, ok := req.Context().Value(guardKey{}).(func(context.Context) error); ok {
		if err = guard(req.Context()); err != nil {
			return nil, err
		}
	}
	response, err := t.Base.RoundTrip(req)
	if response == nil {
		return nil, err
	}
	// A bookkeeping outage must not erase a provider response or cause a resend.
	if finishErr := permit.Finish(req.Context(), response.StatusCode, response.Header); finishErr != nil {
		response.Header.Set("X-STYK-Coordination-Pending", "true")
	}
	return response, err
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
	if strings.HasSuffix(path, "/list") || strings.HasSuffix(path, "/get") || strings.Contains(path, "/info") || strings.Contains(path, "/attribute") || strings.Contains(path, "/tree") {
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
