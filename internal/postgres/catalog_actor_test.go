package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/lilter96/styk/internal/catalogsync"
	"github.com/lilter96/styk/internal/identity"
)

func TestCatalogActorSession(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, actor, audit, want string
		err                      error
	}{
		{name: "local", actor: "matching-test"},
		{name: "merchant", actor: "user.test", want: "user.test"},
		{name: "provenance", actor: "MERCHANT_SESSION:user.test", want: "user.test"},
		{name: "context", actor: "user.test", audit: "user.test", want: "user.test"},
		{name: "forged context", actor: "user.other", audit: "user.test", err: catalogsync.ErrBlocked},
		{name: "empty", err: catalogsync.ErrInvalid},
		{name: "empty principal", actor: "MERCHANT_SESSION:", err: catalogsync.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			if tc.audit != "" {
				ctx = identity.WithSession(ctx, identity.Session{AuditID: tc.audit})
			}
			got, err := catalogActorSession(ctx, tc.actor)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("actor: %q %v", got, err)
			}
		})
	}
}
