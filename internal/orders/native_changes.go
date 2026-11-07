package orders

import (
	"context"
	"time"
)

const NativeChangesStream = "moysklad-order-changes"

func IsNativeReadStream(stream string) bool {
	return stream == NativePointStream || stream == NativeChangesStream
}

type NativeOrderChange struct {
	ID      string `json:"id"`
	Updated string `json:"updated"`
}

type NativeOrderChanges struct {
	Changes    []NativeOrderChange `json:"changes"`
	NextCursor string              `json:"next_cursor"`
}

// Changes are hints. A command requires separately rereading the exact bound
// customerorder and its complete positions under the current credentials.
type NativeChangesReader interface {
	ReadNativeOrderChanges(context.Context, Scope, time.Time, time.Time, int, string) (NativeOrderChanges, error)
}
