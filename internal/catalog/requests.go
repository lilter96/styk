package catalog

import "context"

// RequestEvent records a single HTTP dispatch attempt, including a transport
// interruption. It contains no bodies, credentials or provider entity IDs.
type RequestEvent struct {
	Provider    Provider
	Method      string
	Operation   string
	Status      int
	Interrupted bool
}

type requestObserverKey struct{}

func WithRequestObserver(ctx context.Context, observer func(RequestEvent)) context.Context {
	return context.WithValue(ctx, requestObserverKey{}, observer)
}

// ObserveRequest is inert unless an internal caller explicitly supplied an
// observer. Provider clients call it once per actual HTTP client dispatch.
func ObserveRequest(ctx context.Context, event RequestEvent) {
	if observer, ok := ctx.Value(requestObserverKey{}).(func(RequestEvent)); ok && observer != nil {
		observer(event)
	}
}
